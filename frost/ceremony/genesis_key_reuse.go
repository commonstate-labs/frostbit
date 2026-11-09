package ceremony

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"dex/config"
	"dex/frost/assignment"
)

                                                                            
                                                                              
                                                                   
                                                                            
                                                                              
type GenesisKeyReuse struct {
	Version      uint32          `json:"version"`
	SourceConfig json.RawMessage `json:"source_config"`
}

                                                                            
                                                                               
type legacyFrostConfigV1 struct {
	Enabled   bool                   `json:"enabled"`
	Committee config.CommitteeConfig `json:"committee"`
	Vault     struct {
		DefaultK       int     `json:"defaultK"`
		MinK           int     `json:"minK"`
		Count          int     `json:"count"`
		MaxK           int     `json:"maxK"`
		ThresholdRatio float64 `json:"thresholdRatio"`
	} `json:"vault"`
	Timeouts   config.TimeoutsConfig `json:"timeouts"`
	Withdraw   config.WithdrawConfig `json:"withdraw"`
	Transition struct {
		TriggerChangeRatio        float64 `json:"triggerChangeRatio"`
		PauseWithdrawDuringSwitch bool    `json:"pauseWithdrawDuringSwitch"`
		DkgCommitWindowBlocks     int     `json:"dkgCommitWindowBlocks"`
		DkgSharingWindowBlocks    int     `json:"dkgSharingWindowBlocks"`
		DkgDisputeWindowBlocks    int     `json:"dkgDisputeWindowBlocks"`
	} `json:"transition"`
	Chains                    map[string]config.ChainFrostConfig `json:"chains"`
	GenesisDeploymentManifest json.RawMessage                    `json:"genesisDeploymentManifest,omitempty"`
}

func decodeReuseSource(raw json.RawMessage) (config.FrostConfig, *legacyFrostConfigV1, error) {
	var reuse GenesisKeyReuse
	if err := strictJSON(raw, maxManifestJSONSize, &reuse); err != nil {
		return config.FrostConfig{}, nil, fmt.Errorf("invalid genesis key reuse envelope: %w", err)
	}
	if reuse.Version != 1 {
		return config.FrostConfig{}, nil, errors.New("unsupported genesis key reuse version")
	}
	var frozen legacyFrostConfigV1
	if err := strictJSON(reuse.SourceConfig, maxManifestJSONSize, &frozen); err != nil {
		return config.FrostConfig{}, nil, fmt.Errorf("decode frozen ceremony config: %w", err)
	}
	var source config.FrostConfig
	if err := json.Unmarshal(reuse.SourceConfig, &source); err != nil {
		return source, nil, err
	}
	if !source.Enabled || len(source.GenesisDeploymentManifest) == 0 || source.Vault.DefaultK != 4 || source.Vault.MinK != 4 || source.Vault.MaxK != 4 || source.Vault.ThresholdRatio != 0.75 || source.Vault.Count != 1 {
		return source, nil, errors.New("unsupported original key-reuse profile")
	}
	for _, chain := range source.Chains {
		if len(chain.OnlineDKGVaultRefs) != 0 {
			return source, nil, errors.New("historical ceremony cannot contain online-DKG refs")
		}
	}
	return source, &frozen, nil
}

func legacyReuseDigest(frozen legacyFrostConfigV1, configured bool) ([]byte, error) {
	frozen.GenesisDeploymentManifest = nil
	domain := "DEX_CONFIGURED_FROST_CONFIG_V1"
	if !configured {
		domain = "DEX_CEREMONY_FROST_CONFIG_V1"
		frozen.Chains = cloneLegacyChains(frozen.Chains)
		for name, chain := range frozen.Chains {
			chain.VaultRefs, chain.InitialGroupPubkeys = nil, nil
			chain.DepositDexChainID, chain.DepositProtocol = 0, 0
			frozen.Chains[name] = chain
		}
	}
	return environmentDigest(domain, &frozen)
}

func cloneLegacyChains(chains map[string]config.ChainFrostConfig) map[string]config.ChainFrostConfig {
	copy := make(map[string]config.ChainFrostConfig, len(chains))
	for name, chain := range chains {
		copy[name] = chain
	}
	return copy
}

                                                                               
                                                                            
func PrepareGenesisKeyReuse(sourceJSON []byte) (config.FrostConfig, error) {
	raw, err := json.Marshal(GenesisKeyReuse{Version: 1, SourceConfig: sourceJSON})
	if err != nil {
		return config.FrostConfig{}, err
	}
	source, _, err := decodeReuseSource(raw)
	if err != nil {
		return source, err
	}
	defaults := config.DefaultFrostConfig()
	target := source
	target.GenesisDeploymentManifest = nil
	target.GenesisKeyReuse = raw
	target.Committee.TopN = defaults.Committee.TopN
	target.Committee.ThresholdRatio = defaults.Committee.ThresholdRatio
	target.Vault.MaxK = defaults.Vault.MaxK
	target.Vault.MaturityBlocks = defaults.Vault.MaturityBlocks
	target.Vault.ScaleMinerMultiplier = defaults.Vault.ScaleMinerMultiplier
	target.Vault.CommitteeTermBlocks = defaults.Vault.CommitteeTermBlocks
	target.Transition.ConfirmBlocks = defaults.Transition.ConfirmBlocks
	return target, config.ValidateVaultCommitteePolicy(target)
}

                                                                          
                                                                              
func ValidateGenesisKeyReuse(target config.FrostConfig, targetProof *SignedGenesisDeploymentManifest, manifest *PublicManifest, participants []Participant, chainID uint64, peerDigest []byte, verify SignatureVerifier) error {
	source, frozen, err := decodeReuseSource(target.GenesisKeyReuse)
	if err != nil {
		return err
	}
	if err := config.ValidateVaultCommitteePolicy(target); err != nil {
		return err
	}
	                                                                           
	                                                                            
	expected, err := PrepareGenesisKeyReuse(mustReuseSourceJSON(target.GenesisKeyReuse))
	if err != nil {
		return err
	}
	actual := target
	actual.GenesisDeploymentManifest = nil
	actual.GenesisKeyReuse = expected.GenesisKeyReuse
	if !reflect.DeepEqual(actual, expected) {
		return errors.New("key reuse changes fields outside the approved runtime policy")
	}
	oldProof, err := StrictDecodeGenesisDeploymentManifest(source.GenesisDeploymentManifest)
	if err != nil {
		return err
	}
	digest, err := legacyReuseDigest(*frozen, true)
	if err != nil {
		return err
	}
	if err := validateGenesisDeploymentWithConfigDigest(oldProof, source, manifest, participants, chainID, peerDigest, verify, digest); err != nil {
		return fmt.Errorf("original key-reuse proof: %w", err)
	}
	if targetProof != nil {
		a, b := cloneGenesisDeploymentStatement(oldProof.Statement), cloneGenesisDeploymentStatement(targetProof.Statement)
		a.ConfiguredFrostDigest, b.ConfiguredFrostDigest = nil, nil
		                                                                       
		if !reflect.DeepEqual(a, b) {
			return errors.New("key reuse changes the original deployment or ceremony identity")
		}
	}
	if manifest != nil {
		baseDigest, err := legacyReuseDigest(*frozen, false)
		if err != nil || !bytes.Equal(baseDigest, manifest.Plan.FrostConfigDigest) {
			return errors.New("original ceremony config digest mismatch")
		}
	}
	return nil
}

func mustReuseSourceJSON(raw json.RawMessage) json.RawMessage {
	var reuse GenesisKeyReuse
	_ = json.Unmarshal(raw, &reuse)                                             
	return reuse.SourceConfig
}

                                                                              
func ValidateCeremonyConfigBinding(cfg config.FrostConfig, manifest *PublicManifest) error {
	if manifest == nil {
		return errors.New("missing ceremony manifest")
	}
	var digest []byte
	var err error
	if len(cfg.GenesisKeyReuse) != 0 {
		_, frozen, decodeErr := decodeReuseSource(cfg.GenesisKeyReuse)
		if decodeErr != nil {
			return decodeErr
		}
		digest, err = legacyReuseDigest(*frozen, false)
	} else {
		digest, err = ComputeFrostConfigDigest(cfg)
	}
	if err != nil {
		return err
	}
	if !bytes.Equal(digest, manifest.Plan.FrostConfigDigest) {
		return errors.New("ceremony plan does not match the trusted FROST configuration")
	}
	return nil
}

                                                                            
                                                                               
                                                                          
func InitialCommitteeIndices(cfg config.FrostConfig, indices []uint64, chain string, vaultID uint32) ([]uint64, error) {
	k := cfg.Vault.DefaultK
	if len(cfg.GenesisKeyReuse) == 0 {
		return nil, errors.New("fresh genesis assignment requires validator addresses")
	}
	source, _, err := decodeReuseSource(cfg.GenesisKeyReuse)
	if err != nil {
		return nil, err
	}
	indices = assignment.SortByBitIndex(indices)
	if source.Committee.TopN > 0 && len(indices) > source.Committee.TopN {
		indices = indices[:source.Committee.TopN]
	}
	c, ok := source.Chains[chain]
	if !ok || int(vaultID) >= c.VaultsPerChain {
		return nil, errors.New("unknown original vault")
	}
	result := assignment.GetVaultCommittee(indices, 1, chain, vaultID, c.VaultsPerChain, source.Vault.DefaultK)
	if len(result) != k {
		return nil, errors.New("incomplete original committee")
	}
	return result, nil
}
