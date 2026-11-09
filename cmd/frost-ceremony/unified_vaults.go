package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"reflect"

	"dex/config"
)

const unifiedVaultDepositProtocol = "DEX_VAULT_DEPOSIT_V1"

func isTronVaultContract(name string) bool {
	return name == "SchnorrExampleTronV3"
}

                                                                            
                                                                          
func identifyVaultImmutables(expected *expectedContractArtifact, abi json.RawMessage, build hardhatBuildInfo, refs map[string][]byteRangeJSON) error {
	var entries []struct {
		Type   string `json:"type"`
		Inputs []struct {
			Type string `json:"type"`
		} `json:"inputs"`
	}
	if err := json.Unmarshal(abi, &entries); err != nil {
		return err
	}
	var types []string
	constructors := 0
	for _, entry := range entries {
		if entry.Type == "constructor" {
			constructors++
			for _, input := range entry.Inputs {
				types = append(types, input.Type)
			}
		}
	}
	unified := []string{"uint256", "uint256", "uint256", "uint256", "uint256", "uint256"}
	if isTronVaultContract(expected.contractName) {
		unified = append(unified, "address")
	}
	if constructors != 1 || !reflect.DeepEqual(types, unified) {
		return errors.New("unexpected dual custody V3 constructor ABI")
	}
	expected.unifiedVault = true
	names := make(map[string]string)
	var walk func(any)
	walk = func(value any) {
		switch node := value.(type) {
		case map[string]any:
			if node["nodeType"] == "VariableDeclaration" && node["mutability"] == "immutable" {
				if id, ok := node["id"].(json.Number); ok {
					names[id.String()], _ = node["name"].(string)
				}
			}
			for _, child := range node {
				walk(child)
			}
		case []any:
			for _, child := range node {
				walk(child)
			}
		}
	}
	for _, source := range build.Output.Sources {
		decoder := json.NewDecoder(bytes.NewReader(source.AST))
		decoder.UseNumber()
		var node any
		if err := decoder.Decode(&node); err != nil {
			return err
		}
		walk(node)
	}
	wanted := make(map[string]bool)
	if expected.unifiedVault {
		wanted["dexChainId"] = true
	}
	if isTronVaultContract(expected.contractName) {
		wanted["nonstandardToken"] = true
	}
	if len(refs) != len(wanted) {
		return errors.New("Vault immutable count disagrees with constructor")
	}
	expected.vaultImmutables = make(map[string][]byteRange)
	for id, ranges := range refs {
		name := names[id]
		if !wanted[name] || len(ranges) == 0 || len(expected.vaultImmutables[name]) != 0 {
			return errors.New("unexpected or duplicate named Vault immutable")
		}
		for _, item := range ranges {
			if item.Start < 0 || item.Length != 32 || item.Start > len(expected.runtimeCode)-32 || !allZero(expected.runtimeCode[item.Start:item.Start+32]) {
				return errors.New("invalid Vault immutable slot")
			}
			expected.vaultImmutables[name] = append(expected.vaultImmutables[name], byteRange{start: item.Start, length: 32})
		}
	}
	return nil
}

func expectedVaultRuntime(deployment verifiedDeployment, artifact expectedContractArtifact) ([]byte, error) {
	unified := deployment.receipt.DepositProtocol == unifiedVaultDepositProtocol
	if unified != artifact.unifiedVault {
		return nil, errors.New("Vault receipt protocol disagrees with trusted constructor ABI")
	}
	if artifact.vaultImmutables == nil {                                                       
		var token []byte
		if deployment.chain == "trx" {
			var err error
			token, err = tronTokenABIWord(deployment.receipt.NonstandardToken)
			if err != nil {
				return nil, err
			}
		}
		patched := bytes.Clone(artifact.runtimeCode)
		for _, item := range artifact.immutableRanges {
			if item.start < 0 || item.length != len(token) || item.start+item.length > len(patched) {
				return nil, errors.New("invalid legacy immutable range")
			}
			copy(patched[item.start:item.start+item.length], token)
		}
		return patched, nil
	}
	patched := bytes.Clone(artifact.runtimeCode)
	for name, ranges := range artifact.vaultImmutables {
		var word []byte
		switch name {
		case "dexChainId":
			if !unified || deployment.dexChainID == 0 {
				return nil, errors.New("missing Vault DEX domain")
			}
			word = appendUint256(nil, new(big.Int).SetUint64(deployment.dexChainID))
		case "nonstandardToken":
			var err error
			word, err = tronTokenABIWord(deployment.receipt.NonstandardToken)
			if err != nil {
				return nil, err
			}
		default:
			return nil, errors.New("unknown Vault immutable")
		}
		for _, item := range ranges {
			if item.start < 0 || item.length != len(word) || item.start+item.length > len(patched) {
				return nil, errors.New("invalid Vault immutable range")
			}
			copy(patched[item.start:item.start+item.length], word)
		}
	}
	return patched, nil
}

func requireUnifiedVaultReceipts(deployments []verifiedDeployment, dexChainID uint64) error {
	for _, deployment := range deployments {
		if deployment.receipt.DepositProtocol != unifiedVaultDepositProtocol || deployment.dexChainID != dexChainID {
			return fmt.Errorf("%s/%d requires a verified unified Vault receipt for DEX chain %d", deployment.chain, deployment.vaultID, dexChainID)
		}
	}
	return nil
}

func applyUnifiedVaultDeposits(cfg *config.FrostConfig, deployments []verifiedDeployment) {
	for _, deployment := range deployments {
		chain := cfg.Chains[deployment.chain]
		chain.DepositDexChainID, chain.DepositProtocol = 0, 0
		chain.AuthorityProtocol = deployment.receipt.AuthorityProtocol
		if deployment.receipt.DepositProtocol == unifiedVaultDepositProtocol {
			chain.DepositProtocol = 2
			chain.DepositDexChainID = deployment.dexChainID
		}
		cfg.Chains[deployment.chain] = chain
	}
}

func validateUnifiedReceiptIdentity(receipt *deploymentReceipt) (uint64, error) {
	if receipt == nil || receipt.AuthorityProtocol != 3 {
		return 0, errors.New("unknown Vault authority protocol")
	}
	if receipt.AuthorityProtocol == 3 && receipt.DepositProtocol != unifiedVaultDepositProtocol {
		return 0, errors.New("V3 authority requires a unified Vault DEX domain")
	}
	domain, err := canonicalUint64(receipt.DEXChainID, "dexChainId")
	if receipt.DepositProtocol != unifiedVaultDepositProtocol || receipt.SchemaVersion != 1 || receipt.Status != "verified" || err != nil || domain == 0 || receipt.BlockNumber == 0 {
		return 0, errors.New("unified Vault requires schemaVersion=1, verified status, deposit protocol, DEX domain and deployment block")
	}
	wantPrefix := "0x"
	if isTronVaultContract(receipt.Contract) {
		wantPrefix = ""
	}
	decoded, err := decodeFixedHex(receipt.TxHash, 32)
	if err != nil || receipt.TxHash != wantPrefix+fmt.Sprintf("%x", decoded) {
		return 0, errors.New("noncanonical unified Vault transaction hash")
	}
	if receipt.MinimumEnergy != "" {
		if _, err := canonicalUint64(receipt.MinimumEnergy, "minimumEnergy"); err != nil {
			return 0, err
		}
	}
	return domain, nil
}
