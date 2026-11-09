package main

import (
	"bytes"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"

	"dex/config"
	"dex/frost/ceremony"
	"dex/protocol"
	"dex/utils"
)

const physical20FrostCommitteeSize = 4

var physical20OnlineDKGVaultRefs = map[string]string{
	"eth":  "0x000000000000000000000000000000000000E100",
	"bnb":  "0x000000000000000000000000000000000000B100",
	"base": "0x000000000000000000000000000000000000BA00",
	"arb":  "0x000000000000000000000000000000000000A100",
}

                                                                                
                                                                            
                                                                               
                                                                                
func frostConfigForRuntimeProfile(profile runtimeProfile, guardians ...config.GuardianConfig) (config.FrostConfig, error) {
	cfg := config.DefaultFrostConfig()
	if len(guardians) > 1 {
		return config.FrostConfig{}, fmt.Errorf("one guardian configuration is required")
	}
	if len(guardians) == 1 {
		cfg.Guardian = guardians[0]
	}
	switch profile {
	case runtimeProfileGeneric, runtimeProfilePhysical20Stress:
		cfg.Enabled = false
		return cfg, nil
	case runtimeProfilePhysical20FrostStress:
		cfg.Enabled = true
		cfg.Committee.TopN = config.VaultCandidateSnapshotLimit
		cfg.Committee.ThresholdRatio = 1.0
		cfg.Vault.DefaultK = physical20FrostCommitteeSize
		cfg.Vault.MinK = physical20FrostCommitteeSize
		cfg.Vault.MaxK = 100
		cfg.Vault.Count = 1
		cfg.Vault.ThresholdRatio = 0.75
		cfg.Vault.MaturityBlocks = 30
		cfg.Vault.ScaleMinerMultiplier = 1
		cfg.Transition.ConfirmBlocks = 10
		cfg.Transition.DkgCommitWindowBlocks = 5
		cfg.Transition.DkgSharingWindowBlocks = 5
		cfg.Transition.DkgDisputeWindowBlocks = 5

		for name, chain := range cfg.Chains {
			chain.VaultsPerChain = 1
			chain.InitialGroupPubkeys = nil
			                                                                                        
			                                                                                               
			chain.VaultRefs = nil
			if ref := physical20OnlineDKGVaultRefs[name]; ref != "" {
				chain.OnlineDKGVaultRefs = []string{ref}
			} else {
				chain.OnlineDKGVaultRefs = nil
			}
			cfg.Chains[name] = chain
		}
		return cfg, nil
	default:
		return config.FrostConfig{}, fmt.Errorf("unsupported runtime profile %q", profile)
	}
}

                                                                           
                                                                          
func frostConfigForStandaloneCeremony(online config.FrostConfig) config.FrostConfig {
	standalone := online
	standalone.GenesisDeploymentManifest = bytes.Clone(online.GenesisDeploymentManifest)
	standalone.GenesisKeyReuse = bytes.Clone(online.GenesisKeyReuse)
	standalone.Chains = cloneFrostConfigChains(online.Chains)
	for name, chain := range standalone.Chains {
		chain.OnlineDKGVaultRefs = nil
		standalone.Chains[name] = chain
	}
	return standalone
}

func validateFrostConfigForRuntimeProfile(cfg config.FrostConfig, profile runtimeProfile, validatorCount int) error {
	expected, err := frostConfigForRuntimeProfile(profile)
	if err != nil {
		return err
	}
	if profile == runtimeProfileGeneric || profile == runtimeProfilePhysical20Stress {
		if cfg.Enabled {
			return fmt.Errorf("runtime profile %q requires FROST to remain disabled", profile)
		}
		return nil
	}
	if validatorCount < physical20FrostCommitteeSize {
		return fmt.Errorf("runtime profile %q requires at least %d genesis validators for FROST, got %d", profile, physical20FrostCommitteeSize, validatorCount)
	}
	                                                                             
	                                                                   
	                                                                               
	                                                                            
	if err := config.ValidateFrostGuardianConfig(cfg); err != nil {
		return fmt.Errorf("fixed guardian: %w", err)
	}
	expected.Guardian = cfg.Guardian
	base, hasCeremonyResults := frostConfigWithoutCeremonyResults(cfg)
	standalone := frostConfigForStandaloneCeremony(expected)
	if !reflect.DeepEqual(base, expected) && !reflect.DeepEqual(base, standalone) {
		return fmt.Errorf("runtime profile %q FROST settings do not match the one-vault physical20 test profile", profile)
	}
	if hasCeremonyResults {
		for chainName := range expected.Chains {
			chainCfg := cfg.Chains[chainName]
			groupPubkeys, err := config.DecodeInitialFrostGroupPubkeys(chainName, chainCfg)
			if err != nil {
				return fmt.Errorf("runtime profile %q chain %s ceremony result is invalid: %w", profile, chainName, err)
			}
			if len(groupPubkeys) != chainCfg.VaultsPerChain {
				return fmt.Errorf("runtime profile %q requires complete ceremony results for every chain; chain %s is missing them", profile, chainName)
			}
		}
	}
	return nil
}

func validateStandaloneFrostFinalFile(path string, profile runtimeProfile, validatorCount int) error {
	_, _, err := loadValidatedStandaloneFrostFinalFile(path, profile, validatorCount)
	return err
}

func loadValidatedStandaloneFrostFinalFile(path string, profile runtimeProfile, validatorCount int) (config.FrostConfig, *ceremony.SignedGenesisDeploymentManifest, error) {
	fail := func(err error) (config.FrostConfig, *ceremony.SignedGenesisDeploymentManifest, error) {
		return config.FrostConfig{}, nil, err
	}
	if strings.TrimSpace(path) == "" {
		return fail(fmt.Errorf("standalone-ceremony final FROST config path is empty"))
	}
	cfg, err := config.LoadFrostConfig(path)
	if err != nil {
		return fail(fmt.Errorf("load standalone-ceremony final FROST config: %w", err))
	}
	if err := validateFrostConfigForRuntimeProfile(cfg, profile, validatorCount); err != nil {
		return fail(fmt.Errorf("validate standalone-ceremony final FROST config: %w", err))
	}
	_, hasCeremonyResults := frostConfigWithoutCeremonyResults(cfg)
	if !hasCeremonyResults {
		return fail(fmt.Errorf("standalone-ceremony final FROST config has no ceremony results"))
	}
	directory := filepath.Dir(path)
	peers, err := config.LoadPeerManifest(filepath.Join(directory, "peers.json"))
	if err != nil {
		return fail(fmt.Errorf("load peers.json beside standalone-ceremony final config: %w", err))
	}
	genesis, err := config.LoadGenesisConfig(filepath.Join(directory, "genesis.json"))
	if err != nil {
		return fail(fmt.Errorf("load genesis.json beside standalone-ceremony final config: %w", err))
	}
	if err := config.ValidateFreshFrostGenesisStakePolicy(cfg, peers, genesis); err != nil {
		return fail(fmt.Errorf("validate standalone-ceremony genesis ranking: %w", err))
	}
	if err := ceremony.ValidateConfiguredGenesisDeploymentEnvironment(cfg, peers, genesis.EffectiveChainID(), utils.VerifyECDSASignature); err != nil {
		return fail(fmt.Errorf("validate signed standalone-ceremony deployments: %w", err))
	}
	proof, err := ceremony.StrictDecodeGenesisDeploymentManifest(cfg.GenesisDeploymentManifest)
	return cfg, proof, err
}

                                                                             
                                                                             
                                                         
func validateStandaloneFrostRebuild(candidatePath, currentPath string, profile runtimeProfile, validatorCount int) error {
	candidate, newProof, err := loadValidatedStandaloneFrostFinalFile(candidatePath, profile, validatorCount)
	if err != nil {
		return fmt.Errorf("rebuild candidate: %w", err)
	}
	current, oldProof, err := loadValidatedStandaloneFrostFinalFile(currentPath, profile, validatorCount)
	if err != nil {
		return fmt.Errorf("rebuild current config: %w", err)
	}
	oldStatement, newStatement := oldProof.Statement, newProof.Statement
	if !bytes.Equal(oldStatement.CeremonyManifestDigest, newStatement.CeremonyManifestDigest) ||
		!bytes.Equal(oldStatement.PlanDigest, newStatement.PlanDigest) ||
		oldStatement.NetworkChainID != newStatement.NetworkChainID || oldStatement.Epoch != newStatement.Epoch ||
		!bytes.Equal(oldStatement.PeerManifestDigest, newStatement.PeerManifestDigest) {
		return fmt.Errorf("genesis rebuild must preserve the phase-one ceremony and network identity")
	}
	oldDigest, err := ceremony.ComputeFrostConfigDigest(current)
	if err != nil {
		return err
	}
	newDigest, err := ceremony.ComputeFrostConfigDigest(candidate)
	if err != nil {
		return err
	}
	if !bytes.Equal(oldDigest, newDigest) {
		return fmt.Errorf("genesis rebuild must preserve phase-one FROST parameters")
	}
	if len(oldStatement.Vaults) != len(newStatement.Vaults) {
		return fmt.Errorf("genesis rebuild must preserve phase-one vaults")
	}
	for i, vault := range oldStatement.Vaults {
		other := newStatement.Vaults[i]
		if vault.Chain != other.Chain || vault.VaultID != other.VaultID || !bytes.Equal(vault.RawGroupPubkey, other.RawGroupPubkey) {
			return fmt.Errorf("genesis rebuild must preserve phase-one group key for %s/%d", vault.Chain, vault.VaultID)
		}
	}
	for _, name := range ceremony.GenesisCeremonyChainOrder() {
		old, next := current.Chains[name], candidate.Chains[name]
		if old.DepositProtocol == protocol.DepositProtocolVault && next.DepositProtocol != protocol.DepositProtocolVault {
			return fmt.Errorf("genesis rebuild cannot downgrade unified Vault deposits: %s", name)
		}
	}
	return nil
}

func frostConfigWithoutCeremonyResults(cfg config.FrostConfig) (config.FrostConfig, bool) {
	result := cfg
	result.GenesisDeploymentManifest = nil
	result.Chains = make(map[string]config.ChainFrostConfig, len(cfg.Chains))
	hasResults := len(cfg.GenesisDeploymentManifest) != 0
	for chainName, chainCfg := range cfg.Chains {
		if len(chainCfg.VaultRefs) != 0 || len(chainCfg.InitialGroupPubkeys) != 0 || chainCfg.DepositDexChainID != 0 || chainCfg.DepositProtocol != 0 {
			hasResults = true
		}
		chainCfg.VaultRefs = nil
		chainCfg.InitialGroupPubkeys = nil
		chainCfg.DepositDexChainID = 0
		chainCfg.DepositProtocol = 0
		result.Chains[chainName] = chainCfg
	}
	return result, hasResults
}
