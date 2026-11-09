package config

import (
	"dex/protocol"
	"encoding/json"
	"fmt"
	"os"
)

                                                                           
                                                                               
                                                                              
const RetainedFrostGenesisMarkerPrefix = "retained-frost-keys-v1:"

                                          
type FrostConfig struct {
	Guardian                  GuardianConfig              `json:"guardian"`
	Enabled                   bool                        `json:"enabled"`
	Committee                 CommitteeConfig             `json:"committee"`
	Vault                     VaultConfig                 `json:"vault"`
	Timeouts                  TimeoutsConfig              `json:"timeouts"`
	Withdraw                  WithdrawConfig              `json:"withdraw"`
	Transition                TransitionConfig            `json:"transition"`
	Chains                    map[string]ChainFrostConfig `json:"chains"`
	GenesisDeploymentManifest json.RawMessage             `json:"genesisDeploymentManifest,omitempty"`
	                                                                           
	                                                                       
	GenesisKeyReuse json.RawMessage `json:"genesisKeyReuse,omitempty"`
}

                                                                             
                                                                         
type GuardianConfig struct {
	BTCPublicKey     string `json:"btcPublicKey"`
	AccountPublicKey string `json:"accountPublicKey"`
	Endpoint         string `json:"endpoint"`
}

                                     
type CommitteeConfig struct {
	TopN           int     `json:"topN"`
	ThresholdRatio float64 `json:"thresholdRatio"`                                                     
	EpochBlocks    int     `json:"epochBlocks"`
}

                                    
type VaultConfig struct {
	DefaultK             int     `json:"defaultK"`
	MinK                 int     `json:"minK"`
	Count                int     `json:"count"`
	MaxK                 int     `json:"maxK"`
	ThresholdRatio       float64 `json:"thresholdRatio"`
	MaturityBlocks       uint64  `json:"maturityBlocks"`
	ScaleMinerMultiplier uint32  `json:"scaleMinerMultiplier"`
	CommitteeTermBlocks  uint64  `json:"committeeTermBlocks"`
}

                                 
type TimeoutsConfig struct {
	NonceCommitBlocks      int `json:"nonceCommitBlocks"`
	SigShareBlocks         int `json:"sigShareBlocks"`
	AggregatorRotateBlocks int `json:"aggregatorRotateBlocks"`
	SessionMaxBlocks       int `json:"sessionMaxBlocks"`
}

                                 
type WithdrawConfig struct {
	MaxInFlightPerChainAsset int `json:"maxInFlightPerChainAsset"`
}

                                           
type TransitionConfig struct {
	TriggerChangeRatio        float64 `json:"triggerChangeRatio"`
	PauseWithdrawDuringSwitch bool    `json:"pauseWithdrawDuringSwitch"`
	ConfirmBlocks             uint64  `json:"confirmBlocks"`
	DkgCommitWindowBlocks     int     `json:"dkgCommitWindowBlocks"`
	DkgSharingWindowBlocks    int     `json:"dkgSharingWindowBlocks"`
	DkgDisputeWindowBlocks    int     `json:"dkgDisputeWindowBlocks"`
}

                                          
type ChainFrostConfig struct {
	SignAlgo        string   `json:"signAlgo"`
	FrostVariant    string   `json:"frostVariant,omitempty"`
	ThresholdScheme string   `json:"thresholdScheme,omitempty"`
	VaultsPerChain  int      `json:"vaultsPerChain"`
	VaultRefs       []string `json:"vaultRefs,omitempty"`
	                                                                             
	                                                                           
	                                                                        
	                                                
	OnlineDKGVaultRefs []string `json:"onlineDkgVaultRefs,omitempty"`
	                                                                        
	                                                                            
	InitialGroupPubkeys []string `json:"initialGroupPubkeys,omitempty"`
	BTCNetwork          string   `json:"btcNetwork,omitempty"`
	ChainID             uint64   `json:"chainId,omitempty"`
	DepositDexChainID   uint64   `json:"depositDexChainId,omitempty"`
	DepositProtocol     uint32   `json:"depositProtocol,omitempty"`
	AuthorityProtocol   uint32   `json:"authorityProtocol,omitempty"`

	FeeSatsPerVByte uint64 `json:"feeSatsPerVByte,omitempty"`
	GasPriceGwei    int    `json:"gasPriceGwei,omitempty"`
	GasLimit        int    `json:"gasLimit,omitempty"`
	FeeLimitSun     int    `json:"feeLimitSun,omitempty"`
}

                                                  
func DefaultFrostConfig() FrostConfig {
	return FrostConfig{
		Enabled: true,
		Committee: CommitteeConfig{
			TopN:           VaultCandidateSnapshotLimit,
			ThresholdRatio: 1.0,
			EpochBlocks:    100,
		},
		Vault: VaultConfig{
			DefaultK:             4,
			MinK:                 4,
			MaxK:                 100,
			ThresholdRatio:       0.75,
			MaturityBlocks:       864000,
			ScaleMinerMultiplier: 100,
			CommitteeTermBlocks:  protocol.VaultCommitteeTermBlocks,
			Count:                1,
		},
		Timeouts: TimeoutsConfig{
			NonceCommitBlocks:      2,
			SigShareBlocks:         3,
			AggregatorRotateBlocks: 5,
			SessionMaxBlocks:       60,
		},
		Withdraw: WithdrawConfig{
			MaxInFlightPerChainAsset: 1,
		},
		Transition: TransitionConfig{
			TriggerChangeRatio:        0.2,
			PauseWithdrawDuringSwitch: true,
			ConfirmBlocks:             200,
			DkgCommitWindowBlocks:     20,
			DkgSharingWindowBlocks:    20,
			DkgDisputeWindowBlocks:    20,
		},
		Chains: map[string]ChainFrostConfig{
			"btc": {
				SignAlgo:        "SCHNORR_SECP256K1_BIP340",
				FrostVariant:    "frost-secp256k1",
				VaultsPerChain:  1,
				BTCNetwork:      "mainnet",
				FeeSatsPerVByte: 25,
			},
			"eth": {
				SignAlgo:          "SCHNORR_ALT_BN128",
				AuthorityProtocol: 3,
				FrostVariant:      "frost-bn128",
				VaultsPerChain:    1,
				ChainID:           1,
				GasPriceGwei:      30,
				GasLimit:          180000,
			},
			"bnb": {
				SignAlgo:          "SCHNORR_ALT_BN128",
				AuthorityProtocol: 3,
				FrostVariant:      "frost-bn128",
				VaultsPerChain:    1,
				ChainID:           56,
				GasPriceGwei:      3,
				GasLimit:          180000,
			},
			"base": {
				SignAlgo:          "SCHNORR_ALT_BN128",
				AuthorityProtocol: 3,
				FrostVariant:      "frost-bn128",
				VaultsPerChain:    1,
				ChainID:           8453,
				GasPriceGwei:      1,
				GasLimit:          180000,
			},
			"arb": {
				SignAlgo:          "SCHNORR_ALT_BN128",
				AuthorityProtocol: 3,
				FrostVariant:      "frost-bn128",
				VaultsPerChain:    1,
				ChainID:           42161,
				GasPriceGwei:      1,
				GasLimit:          180000,
			},
			"trx": {
				SignAlgo:          "SCHNORR_ALT_BN128",
				AuthorityProtocol: 3,
				ChainID:           728126428,
				FrostVariant:      "frost-bn128",
				VaultsPerChain:    1,
				FeeLimitSun:       30000000,
			},
		},
	}
}

                                                  
func LoadFrostConfig(path string) (FrostConfig, error) {
	cfg := DefaultFrostConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return FrostConfig{}, fmt.Errorf("failed to read frost config: %w", err)
	}

	if err := json.Unmarshal(data, &cfg); err != nil {
		return FrostConfig{}, fmt.Errorf("failed to parse frost config: %w", err)
	}
	mergeFrostChainDefaults(&cfg)
	if err := ValidateVaultCommitteePolicy(cfg); err != nil {
		return FrostConfig{}, fmt.Errorf("invalid FROST committee policy: %w", err)
	}
	if err := ValidateDeposits(cfg, 0); err != nil {
		return FrostConfig{}, fmt.Errorf("invalid deposit configuration: %w", err)
	}
	if err := ValidateOnlineDKGVaultRefs(cfg); err != nil {
		return FrostConfig{}, fmt.Errorf("invalid online-DKG vault references: %w", err)
	}
	if err := ValidateFrostGuardianConfig(cfg); err != nil {
		return FrostConfig{}, err
	}

	return cfg, nil
}

func mergeFrostChainDefaults(cfg *FrostConfig) {
	if cfg == nil {
		return
	}
	def := DefaultFrostConfig()
	if cfg.Chains == nil {
		cfg.Chains = def.Chains
		return
	}
	for name, defChain := range def.Chains {
		cur, ok := cfg.Chains[name]
		if !ok {
			cfg.Chains[name] = defChain
			continue
		}
		if cur.SignAlgo == "" {
			cur.SignAlgo = defChain.SignAlgo
		}
		if cur.FrostVariant == "" {
			cur.FrostVariant = defChain.FrostVariant
		}
		if cur.ThresholdScheme == "" {
			cur.ThresholdScheme = defChain.ThresholdScheme
		}
		if cur.VaultsPerChain == 0 {
			cur.VaultsPerChain = defChain.VaultsPerChain
		}
		if cur.BTCNetwork == "" {
			cur.BTCNetwork = defChain.BTCNetwork
		}
		if cur.ChainID == 0 {
			cur.ChainID = defChain.ChainID
		}
		if cur.FeeSatsPerVByte == 0 {
			cur.FeeSatsPerVByte = defChain.FeeSatsPerVByte
		}
		if cur.GasPriceGwei == 0 {
			cur.GasPriceGwei = defChain.GasPriceGwei
		}
		if cur.GasLimit == 0 {
			cur.GasLimit = defChain.GasLimit
		}
		if cur.FeeLimitSun == 0 {
			cur.FeeLimitSun = defChain.FeeLimitSun
		}
		cfg.Chains[name] = cur
	}
}
