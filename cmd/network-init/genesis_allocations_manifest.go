package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"dex/config"
	"dex/utils"
)

const (
	publicGenesisAllocationsSchema = 1
	publicGenesisAllocationsKind   = "public-genesis-allocations"
	maxGenesisAllocationsFileBytes = 1 << 20
)

                                                                            
                                                                              
                                                                            
type publicGenesisAllocationsDocument struct {
	SchemaVersion int                            `json:"schema_version"`
	Kind          string                         `json:"kind"`
	Notice        string                         `json:"_notice"`
	Alloc         map[string]config.GenesisAlloc `json:"alloc"`
}

func loadPublicGenesisAllocations(path string) (map[string]config.GenesisAlloc, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect public genesis allocations file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("public genesis allocations file must be a regular, non-symlink file")
	}
	if info.Size() > maxGenesisAllocationsFileBytes {
		return nil, fmt.Errorf("public genesis allocations file exceeds %d bytes", maxGenesisAllocationsFileBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open public genesis allocations file: %w", err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat opened public genesis allocations file: %w", err)
	}
	if !os.SameFile(info, openedInfo) || !openedInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("public genesis allocations file changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxGenesisAllocationsFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read public genesis allocations file: %w", err)
	}
	if len(data) > maxGenesisAllocationsFileBytes {
		return nil, fmt.Errorf("public genesis allocations file exceeds %d bytes", maxGenesisAllocationsFileBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var document publicGenesisAllocationsDocument
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("parse public genesis allocations file: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("parse public genesis allocations file: unexpected trailing JSON value")
		}
		return nil, fmt.Errorf("parse public genesis allocations file: %w", err)
	}
	if document.SchemaVersion != publicGenesisAllocationsSchema || document.Kind != publicGenesisAllocationsKind {
		return nil, fmt.Errorf("public genesis allocations file must be schema_version=%d kind=%q", publicGenesisAllocationsSchema, publicGenesisAllocationsKind)
	}
	notice := strings.ToUpper(strings.TrimSpace(document.Notice))
	if !strings.Contains(notice, "PUBLIC") || !strings.Contains(notice, "NO PRIVATE KEYS") {
		return nil, fmt.Errorf("public genesis allocations _notice must contain PUBLIC and NO PRIVATE KEYS")
	}
	if len(document.Alloc) == 0 {
		return nil, fmt.Errorf("public genesis allocations file contains no accounts")
	}
	return document.Alloc, nil
}

func mergePublicGenesisAllocations(genesis *config.GenesisConfig, excludedIdentities map[string]struct{}, allocations map[string]config.GenesisAlloc, requireFullTokenRegistry bool) error {
	if len(allocations) == 0 {
		return nil
	}
	if genesis == nil {
		return fmt.Errorf("cannot merge public genesis allocations into a nil genesis config")
	}
	if err := validateOrdinaryGenesisAllocations(allocations, genesis.Tokens, excludedIdentities, requireFullTokenRegistry); err != nil {
		return err
	}
	for address, allocation := range allocations {
		if _, exists := genesis.Alloc[address]; exists {
			return fmt.Errorf("public genesis allocation duplicates existing account %s", address)
		}
		genesis.Alloc[address] = cloneGenesisAllocation(allocation)
	}
	return nil
}

func validatePublicGenesisAllocationsMatch(networkDir string, expected map[string]config.GenesisAlloc, requireFullTokenRegistry bool) error {
	return validatePublicGenesisAllocationsMatchWithTailArbitrageFunding(networkDir, expected, requireFullTokenRegistry, false)
}

func validatePublicGenesisAllocationsMatchWithTailArbitrageFunding(networkDir string, expected map[string]config.GenesisAlloc, requireFullTokenRegistry, fundTailArbitrageAccounts bool) error {
	genesis, err := config.LoadGenesisConfig(config.ConfigPath(networkDir, "genesis.json"))
	if err != nil {
		return fmt.Errorf("load genesis while matching public allocations: %w", err)
	}
	manifest, err := config.LoadPeerManifest(config.ConfigPath(networkDir, "peers.json"))
	if err != nil {
		return fmt.Errorf("load peers while matching public allocations: %w", err)
	}
	validatorSet := make(map[string]struct{}, len(manifest.GenesisValidators))
	for _, validator := range manifest.GenesisValidators {
		validatorSet[validator.Address] = struct{}{}
	}
	excludedIdentities := make(map[string]struct{}, len(validatorSet))
	for address := range validatorSet {
		excludedIdentities[address] = struct{}{}
	}
	tailArbitrageSet := make(map[string]struct{})
	identityDocument, identityErr := loadIdentityDocument(filepath.Join(networkDir, "identities.json"))
	if identityErr == nil {
		for _, node := range identityDocument.Nodes {
			excludedIdentities[node.Address] = struct{}{}
		}
		if fundTailArbitrageAccounts {
			if !requireFullTokenRegistry {
				return fmt.Errorf("tail arbitrage funding requires the physical20 FROST stress full-token registry")
			}
			validatorCount := identityDocument.GenesisValidatorCount
			if err := validateTailArbitrageFundingRequest(runtimeProfilePhysical20FrostStress, validatorCount, len(identityDocument.Nodes), true); err != nil {
				return err
			}
			for _, node := range identityDocument.Nodes[len(identityDocument.Nodes)-validatorCount:] {
				tailArbitrageSet[node.Address] = struct{}{}
			}
		}
	} else if !os.IsNotExist(identityErr) {
		return fmt.Errorf("load identities while matching public allocations: %w", identityErr)
	} else if fundTailArbitrageAccounts {
		return fmt.Errorf("tail arbitrage funding validation requires identities.json")
	}
	if err := validateOrdinaryGenesisAllocations(expected, genesis.Tokens, excludedIdentities, requireFullTokenRegistry); err != nil {
		return fmt.Errorf("validate expected public genesis allocations: %w", err)
	}
	actual := make(map[string]config.GenesisAlloc)
	for address, allocation := range genesis.Alloc {
		if _, validator := validatorSet[address]; validator {
			continue
		}
		if _, tailArbitrage := tailArbitrageSet[address]; tailArbitrage {
			continue
		}
		actual[address] = allocation
	}
	if !reflect.DeepEqual(actual, expected) {
		return fmt.Errorf("generated ordinary genesis allocations do not exactly match --genesis-allocations-file")
	}
	return nil
}

func validateOrdinaryGenesisAllocations(allocations map[string]config.GenesisAlloc, tokens []config.GenesisToken, excluded map[string]struct{}, requireFullTokenRegistry bool) error {
	registered := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		registered[token.Address] = struct{}{}
	}
	for address, allocation := range allocations {
		if address == "default" || !utils.IsDexAddress(address) {
			return fmt.Errorf("ordinary genesis allocation address %q is not a canonical fbc account", address)
		}
		if _, reserved := excluded[address]; reserved {
			return fmt.Errorf("ordinary genesis allocation duplicates node identity %s", address)
		}
		if len(allocation.Balances) == 0 {
			return fmt.Errorf("ordinary genesis allocation for %s has no balances", address)
		}
		if requireFullTokenRegistry && len(allocation.Balances) != len(registered) {
			return fmt.Errorf("ordinary genesis allocation for %s asset count=%d, want %d", address, len(allocation.Balances), len(registered))
		}
		for token, balance := range allocation.Balances {
			if _, exists := registered[token]; !exists {
				return fmt.Errorf("ordinary genesis allocation for %s references unregistered token %q", address, token)
			}
			if strings.TrimSpace(balance.MinerLockedBalance) != "" {
				return fmt.Errorf("ordinary genesis allocation for %s token %s must not contain miner_locked_balance", address, token)
			}
			if !isCanonicalPositiveDecimal(balance.Balance) {
				return fmt.Errorf("ordinary genesis allocation for %s token %s balance must be a canonical positive decimal", address, token)
			}
		}
		if requireFullTokenRegistry {
			for token := range registered {
				if _, exists := allocation.Balances[token]; !exists {
					return fmt.Errorf("ordinary genesis allocation for %s is missing token %s", address, token)
				}
			}
		}
	}
	return nil
}

func isCanonicalPositiveDecimal(raw string) bool {
	if raw == "" || strings.TrimSpace(raw) != raw || (len(raw) > 1 && raw[0] == '0') {
		return false
	}
	for _, char := range raw {
		if char < '0' || char > '9' {
			return false
		}
	}
	value, ok := new(big.Int).SetString(raw, 10)
	return ok && value.Sign() > 0
}

func cloneGenesisAllocation(input config.GenesisAlloc) config.GenesisAlloc {
	balances := make(map[string]config.GenesisBalance, len(input.Balances))
	for token, balance := range input.Balances {
		balances[token] = balance
	}
	return config.GenesisAlloc{Balances: balances}
}
