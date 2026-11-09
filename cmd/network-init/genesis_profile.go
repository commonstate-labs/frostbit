package main

import (
	"fmt"
	"math/big"
	"reflect"
	"strings"

	"dex/config"
	"dex/vm"
)

const (
	physical20FrostStressTokenCount = 16
	tailArbitrageSupplyDivisor      = int64(100)
	                                                                      
	                                                                           
	                                                                       
	physical20FrostStressSupplyMultiplier = int64(1_000_000)
)

func defaultGenesisForRuntimeProfile(profile runtimeProfile) (*config.GenesisConfig, error) {
	genesis := config.DefaultGenesisConfig()
	if profile != runtimeProfilePhysical20FrostStress {
		return genesis, nil
	}
	multiplier := big.NewInt(physical20FrostStressSupplyMultiplier)
	for i := range genesis.Tokens {
		total, ok := new(big.Int).SetString(strings.TrimSpace(genesis.Tokens[i].TotalSupply), 10)
		if !ok || total.Sign() <= 0 {
			return nil, fmt.Errorf("physical20 FROST stress token %q has invalid default total supply", genesis.Tokens[i].Address)
		}
		genesis.Tokens[i].TotalSupply = total.Mul(total, multiplier).String()
	}
	return genesis, nil
}

func validateTailArbitrageFundingRequest(profile runtimeProfile, validatorCount, identityCount int, enabled bool) error {
	if !enabled {
		return nil
	}
	if profile != runtimeProfilePhysical20FrostStress {
		return fmt.Errorf("--fund-tail-arbitrage-accounts requires --runtime-profile=%s", runtimeProfilePhysical20FrostStress)
	}
	if validatorCount < 1 || identityCount < 2*validatorCount {
		return fmt.Errorf("--fund-tail-arbitrage-accounts requires --identities >= 2*--validators so tail arbitrage identities cannot overlap genesis validators")
	}
	return nil
}

func tailArbitrageAllocationForRuntimeProfile(profile runtimeProfile, tokens []config.GenesisToken) (config.GenesisAlloc, error) {
	if profile != runtimeProfilePhysical20FrostStress {
		return config.GenesisAlloc{}, fmt.Errorf("tail arbitrage funding is unsupported for runtime profile %q", profile)
	}
	if err := validatePhysical20FrostStressTokenRegistry(tokens); err != nil {
		return config.GenesisAlloc{}, err
	}
	denominator := big.NewInt(tailArbitrageSupplyDivisor)
	balances := make(map[string]config.GenesisBalance, len(tokens))
	for _, token := range tokens {
		supply, ok := new(big.Int).SetString(strings.TrimSpace(token.TotalSupply), 10)
		if !ok || supply.Sign() <= 0 {
			return config.GenesisAlloc{}, fmt.Errorf("physical20 FROST stress token %q has invalid total supply", token.Address)
		}
		available := new(big.Int).Quo(supply, denominator)
		if available.Sign() <= 0 {
			return config.GenesisAlloc{}, fmt.Errorf("physical20 FROST stress token %q supply is too small for tail arbitrage funding", token.Address)
		}
		balances[token.Address] = config.GenesisBalance{
			Balance: available.String(),
		}
	}
	return config.GenesisAlloc{Balances: balances}, nil
}

func genesisAllocationForRuntimeProfile(profile runtimeProfile, tokens []config.GenesisToken, validatorCount int) (config.GenesisAlloc, error) {
	stake := vm.MinMinerStake.String()
	switch profile {
	case runtimeProfileGeneric, runtimeProfilePhysical20Stress:
		return config.GenesisAlloc{Balances: map[string]config.GenesisBalance{
			"FB": {Balance: stake, MinerLockedBalance: stake},
		}}, nil
	case runtimeProfilePhysical20FrostStress:
		if validatorCount < 1 {
			return config.GenesisAlloc{}, fmt.Errorf("physical20 FROST stress profile requires at least one genesis validator")
		}
		if err := validatePhysical20FrostStressTokenRegistry(tokens); err != nil {
			return config.GenesisAlloc{}, err
		}
		denominator := big.NewInt(int64(2 * validatorCount))
		balances := make(map[string]config.GenesisBalance, len(tokens))
		for _, token := range tokens {
			supply, ok := new(big.Int).SetString(strings.TrimSpace(token.TotalSupply), 10)
			if !ok || supply.Sign() <= 0 {
				return config.GenesisAlloc{}, fmt.Errorf("physical20 FROST stress token %q has invalid total supply", token.Address)
			}
			available := new(big.Int).Quo(supply, denominator)
			if available.Sign() <= 0 {
				return config.GenesisAlloc{}, fmt.Errorf("physical20 FROST stress token %q supply is too small for %d validators", token.Address, validatorCount)
			}
			balances[token.Address] = config.GenesisBalance{Balance: available.String()}
		}
		fb := balances["FB"]
		fb.MinerLockedBalance = stake
		balances["FB"] = fb
		return config.GenesisAlloc{Balances: balances}, nil
	default:
		return config.GenesisAlloc{}, fmt.Errorf("unsupported runtime profile %q", profile)
	}
}

func validatePhysical20FrostStressTokenRegistry(tokens []config.GenesisToken) error {
	expectedGenesis, err := defaultGenesisForRuntimeProfile(runtimeProfilePhysical20FrostStress)
	if err != nil {
		return err
	}
	scaledExpected := expectedGenesis.Tokens
	legacyExpected := config.DefaultGenesisConfig().Tokens
	if len(tokens) != physical20FrostStressTokenCount || len(tokens) != len(scaledExpected) {
		return fmt.Errorf("physical20 FROST stress token count=%d, want %d", len(tokens), physical20FrostStressTokenCount)
	}
	scaledByAddress := make(map[string]config.GenesisToken, len(scaledExpected))
	legacyByAddress := make(map[string]config.GenesisToken, len(legacyExpected))
	for _, token := range scaledExpected {
		scaledByAddress[token.Address] = token
	}
	for _, token := range legacyExpected {
		legacyByAddress[token.Address] = token
	}
	seen := make(map[string]struct{}, len(tokens))
	matchesScaled := true
	matchesLegacy := true
	for _, token := range tokens {
		if strings.TrimSpace(token.Address) == "" {
			return fmt.Errorf("physical20 FROST stress token address is empty")
		}
		if _, duplicate := seen[token.Address]; duplicate {
			return fmt.Errorf("physical20 FROST stress genesis contains duplicate token %q", token.Address)
		}
		seen[token.Address] = struct{}{}
		scaled, scaledExists := scaledByAddress[token.Address]
		legacy, legacyExists := legacyByAddress[token.Address]
		if !scaledExists || !reflect.DeepEqual(token, scaled) {
			matchesScaled = false
		}
		if !legacyExists || !reflect.DeepEqual(token, legacy) {
			matchesLegacy = false
		}
	}
	if !matchesScaled && !matchesLegacy {
		return fmt.Errorf("physical20 FROST stress tokens must match the base or %dx-capital canonical 16-token registry", physical20FrostStressSupplyMultiplier)
	}
	return nil
}

func validatePhysical20FrostStressGenesis(genesis *config.GenesisConfig, validators []config.GenesisValidatorConfig) error {
	return validatePhysical20FrostStressGenesisWithTailArbitrageFunding(genesis, validators, nil)
}

func validatePhysical20FrostStressGenesisWithTailArbitrageFunding(genesis *config.GenesisConfig, validators []config.GenesisValidatorConfig, tailArbitrageSet map[string]struct{}) error {
	if genesis == nil {
		return fmt.Errorf("physical20 FROST stress genesis config is missing")
	}
	if _, exists := genesis.Alloc["default"]; exists {
		return fmt.Errorf("physical20 FROST stress genesis must not contain a default allocation")
	}
	if len(genesis.Alloc) < len(validators) {
		return fmt.Errorf("physical20 FROST stress allocation count=%d, want at least %d address-level validator allocations", len(genesis.Alloc), len(validators))
	}
	if err := validatePhysical20FrostStressTokenRegistry(genesis.Tokens); err != nil {
		return err
	}

	expected, err := genesisAllocationForRuntimeProfile(runtimeProfilePhysical20FrostStress, genesis.Tokens, len(validators))
	if err != nil {
		return err
	}
	for _, validator := range validators {
		allocation, exists := genesis.Alloc[validator.Address]
		if !exists {
			return fmt.Errorf("physical20 FROST stress allocation is missing for validator %s", validator.Address)
		}
		if len(allocation.Balances) != len(expected.Balances) {
			return fmt.Errorf("physical20 FROST stress validator %s asset count=%d, want %d", validator.Address, len(allocation.Balances), len(expected.Balances))
		}
		for token, want := range expected.Balances {
			got, exists := allocation.Balances[token]
			if !exists {
				return fmt.Errorf("physical20 FROST stress allocation for validator %s is missing token %s", validator.Address, token)
			}
			if got.Balance != want.Balance || got.MinerLockedBalance != want.MinerLockedBalance {
				return fmt.Errorf("physical20 FROST stress allocation for validator %s token %s does not match the funded test profile", validator.Address, token)
			}
		}
	}
	validatorSet := make(map[string]struct{}, len(validators))
	for _, validator := range validators {
		validatorSet[validator.Address] = struct{}{}
	}
	if len(tailArbitrageSet) > 0 {
		tailExpected, err := tailArbitrageAllocationForRuntimeProfile(runtimeProfilePhysical20FrostStress, genesis.Tokens)
		if err != nil {
			return err
		}
		for address := range tailArbitrageSet {
			if _, validator := validatorSet[address]; validator {
				return fmt.Errorf("physical20 FROST stress tail arbitrage account %s overlaps a genesis validator", address)
			}
			allocation, exists := genesis.Alloc[address]
			if !exists {
				return fmt.Errorf("physical20 FROST stress tail arbitrage allocation is missing for %s", address)
			}
			if !reflect.DeepEqual(allocation, tailExpected) {
				return fmt.Errorf("physical20 FROST stress tail arbitrage allocation for %s must fund every registered token with total_supply/%d and zero miner stake", address, tailArbitrageSupplyDivisor)
			}
		}
	}
	ordinary := make(map[string]config.GenesisAlloc)
	for address, allocation := range genesis.Alloc {
		if _, validator := validatorSet[address]; validator {
			continue
		}
		if _, tailArbitrage := tailArbitrageSet[address]; tailArbitrage {
			continue
		}
		ordinary[address] = allocation
	}
	excluded := make(map[string]struct{}, len(validatorSet)+len(tailArbitrageSet))
	for address := range validatorSet {
		excluded[address] = struct{}{}
	}
	for address := range tailArbitrageSet {
		excluded[address] = struct{}{}
	}
	if err := validateOrdinaryGenesisAllocations(ordinary, genesis.Tokens, excluded, true); err != nil {
		return fmt.Errorf("physical20 FROST stress ordinary allocations are invalid: %w", err)
	}
	if err := validateGenesisAllocationsWithinSupply(genesis); err != nil {
		return fmt.Errorf("physical20 FROST stress allocations exceed token supply: %w", err)
	}
	return nil
}

func validateGenesisAllocationsWithinSupply(genesis *config.GenesisConfig) error {
	if genesis == nil {
		return fmt.Errorf("genesis config is missing")
	}
	supplies := make(map[string]*big.Int, len(genesis.Tokens))
	allocated := make(map[string]*big.Int, len(genesis.Tokens))
	for _, token := range genesis.Tokens {
		supply, ok := new(big.Int).SetString(strings.TrimSpace(token.TotalSupply), 10)
		if !ok || supply.Sign() < 0 {
			return fmt.Errorf("token %q has invalid total supply", token.Address)
		}
		supplies[token.Address] = supply
		allocated[token.Address] = new(big.Int)
	}
	for address, allocation := range genesis.Alloc {
		for token, balance := range allocation.Balances {
			total, exists := allocated[token]
			if !exists {
				return fmt.Errorf("allocation %q references unregistered token %q", address, token)
			}
			for bucket, raw := range map[string]string{
				"available":    balance.Balance,
				"miner_locked": balance.MinerLockedBalance,
			} {
				if strings.TrimSpace(raw) == "" {
					continue
				}
				amount, ok := new(big.Int).SetString(strings.TrimSpace(raw), 10)
				if !ok || amount.Sign() < 0 {
					return fmt.Errorf("allocation %q token %q has invalid %s balance", address, token, bucket)
				}
				total.Add(total, amount)
			}
		}
	}
	for token, total := range allocated {
		if total.Cmp(supplies[token]) > 0 {
			return fmt.Errorf("token %q allocated=%s exceeds total_supply=%s", token, total, supplies[token])
		}
	}
	return nil
}
