package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultFrostConfig(t *testing.T) {
	cfg := DefaultFrostConfig()

	                                
	if !cfg.Enabled {
		t.Error("Enabled should be true by default")
	}

	                             
	if cfg.Committee.TopN != VaultCandidateSnapshotLimit {
		t.Errorf("Committee.TopN = %d, want %d", cfg.Committee.TopN, VaultCandidateSnapshotLimit)
	}
	if cfg.Committee.ThresholdRatio != 1.0 {
		t.Errorf("Committee.ThresholdRatio = %f, want 1.0", cfg.Committee.ThresholdRatio)
	}
	if cfg.Committee.EpochBlocks != 100 {
		t.Errorf("Committee.EpochBlocks = %d, want 100", cfg.Committee.EpochBlocks)
	}

	                         
	if cfg.Vault.DefaultK != 4 {
		t.Errorf("Vault.DefaultK = %d, want 4", cfg.Vault.DefaultK)
	}
	if cfg.Vault.MinK != 4 {
		t.Errorf("Vault.MinK = %d, want 4", cfg.Vault.MinK)
	}
	if cfg.Vault.MaxK != 100 {
		t.Errorf("Vault.MaxK = %d, want 100", cfg.Vault.MaxK)
	}
	if cfg.Vault.ThresholdRatio != 0.75 {
		t.Errorf("Vault.ThresholdRatio = %f, want 0.75", cfg.Vault.ThresholdRatio)
	}
	if cfg.Vault.MaturityBlocks != 864000 {
		t.Errorf("Vault.MaturityBlocks = %d, want 864000", cfg.Vault.MaturityBlocks)
	}
	if cfg.Vault.ScaleMinerMultiplier != 100 {
		t.Errorf("Vault.ScaleMinerMultiplier = %d, want 100", cfg.Vault.ScaleMinerMultiplier)
	}

	                            
	if cfg.Timeouts.NonceCommitBlocks != 2 {
		t.Errorf("Timeouts.NonceCommitBlocks = %d, want 2", cfg.Timeouts.NonceCommitBlocks)
	}
	if cfg.Timeouts.SigShareBlocks != 3 {
		t.Errorf("Timeouts.SigShareBlocks = %d, want 3", cfg.Timeouts.SigShareBlocks)
	}
	if cfg.Timeouts.AggregatorRotateBlocks != 5 {
		t.Errorf("Timeouts.AggregatorRotateBlocks = %d, want 5", cfg.Timeouts.AggregatorRotateBlocks)
	}
	if cfg.Timeouts.SessionMaxBlocks != 60 {
		t.Errorf("Timeouts.SessionMaxBlocks = %d, want 60", cfg.Timeouts.SessionMaxBlocks)
	}

	                            
	if cfg.Withdraw.MaxInFlightPerChainAsset != 1 {
		t.Errorf("Withdraw.MaxInFlightPerChainAsset = %d, want 1", cfg.Withdraw.MaxInFlightPerChainAsset)
	}

	                              
	if cfg.Transition.TriggerChangeRatio != 0.2 {
		t.Errorf("Transition.TriggerChangeRatio = %f, want 0.2", cfg.Transition.TriggerChangeRatio)
	}
	if !cfg.Transition.PauseWithdrawDuringSwitch {
		t.Error("Transition.PauseWithdrawDuringSwitch should be true")
	}
	if cfg.Transition.ConfirmBlocks != 200 {
		t.Errorf("Transition.ConfirmBlocks = %d, want 200", cfg.Transition.ConfirmBlocks)
	}
	if cfg.Transition.DkgCommitWindowBlocks != 20 {
		t.Errorf("Transition.DkgCommitWindowBlocks = %d, want 20", cfg.Transition.DkgCommitWindowBlocks)
	}
	if cfg.Transition.DkgDisputeWindowBlocks != 20 {
		t.Errorf("Transition.DkgDisputeWindowBlocks = %d, want 20", cfg.Transition.DkgDisputeWindowBlocks)
	}
}

func TestDefaultFrostConfigChains(t *testing.T) {
	cfg := DefaultFrostConfig()

	                              
	expectedChains := []string{"btc", "eth", "bnb", "trx", "base", "arb"}
	if len(cfg.Chains) != len(expectedChains) {
		t.Errorf("Chains count = %d, want %d", len(cfg.Chains), len(expectedChains))
	}
	for _, chain := range expectedChains {
		if _, ok := cfg.Chains[chain]; !ok {
			t.Errorf("missing chain config for %s", chain)
		}
	}

	                    
	btc := cfg.Chains["btc"]
	if btc.SignAlgo != "SCHNORR_SECP256K1_BIP340" {
		t.Errorf("btc.SignAlgo = %s, want SCHNORR_SECP256K1_BIP340", btc.SignAlgo)
	}
	if btc.FrostVariant != "frost-secp256k1" {
		t.Errorf("btc.FrostVariant = %s, want frost-secp256k1", btc.FrostVariant)
	}
	if btc.VaultsPerChain != 1 {
		t.Errorf("btc.VaultsPerChain = %d, want 1", btc.VaultsPerChain)
	}
	if btc.FeeSatsPerVByte != 25 {
		t.Errorf("btc.FeeSatsPerVByte = %d, want 25", btc.FeeSatsPerVByte)
	}

	                    
	for _, chain := range []string{"eth", "bnb", "base", "arb"} {
		evm := cfg.Chains[chain]
		if evm.SignAlgo != "SCHNORR_ALT_BN128" {
			t.Errorf("%s.SignAlgo = %s, want SCHNORR_ALT_BN128", chain, evm.SignAlgo)
		}
		if evm.FrostVariant != "frost-bn128" {
			t.Errorf("%s.FrostVariant = %s, want frost-bn128", chain, evm.FrostVariant)
		}
	}
	eth := cfg.Chains["eth"]
	if eth.GasPriceGwei != 30 {
		t.Errorf("eth.GasPriceGwei = %d, want 30", eth.GasPriceGwei)
	}
	if eth.GasLimit != 180000 {
		t.Errorf("eth.GasLimit = %d, want 180000", eth.GasLimit)
	}

	                    
	trx := cfg.Chains["trx"]
	if trx.SignAlgo != "SCHNORR_ALT_BN128" || trx.ChainID != 728126428 {
		t.Errorf("trx authority must use BN128 and TVM mainnet chain ID, got %+v", trx)
	}
	if trx.FrostVariant != "frost-bn128" {
		t.Errorf("trx.FrostVariant must be frost-bn128, got %s", trx.FrostVariant)
	}
}

func TestDefaultConfigHasFrost(t *testing.T) {
	cfg := DefaultConfig()

	                                           
	if cfg.Frost.Committee.TopN != VaultCandidateSnapshotLimit {
		t.Errorf("DefaultConfig().Frost.Committee.TopN = %d, want %d", cfg.Frost.Committee.TopN, VaultCandidateSnapshotLimit)
	}
	if cfg.Frost.Vault.DefaultK != 4 {
		t.Errorf("DefaultConfig().Frost.Vault.DefaultK = %d, want 4", cfg.Frost.Vault.DefaultK)
	}
	if len(cfg.Frost.Chains) != 6 {
		t.Errorf("DefaultConfig().Frost.Chains count = %d, want 6", len(cfg.Frost.Chains))
	}
}

func TestDefaultConfigDatabaseBlockCache(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Database.BlockCacheSizeDB != 128<<20 {
		t.Errorf("DefaultConfig().Database.BlockCacheSizeDB = %d, want %d", cfg.Database.BlockCacheSizeDB, int64(128<<20))
	}
	if cfg.Database.BlockPruneStartFreePercent != 50 {
		t.Errorf("DefaultConfig().Database.BlockPruneStartFreePercent = %v, want 50", cfg.Database.BlockPruneStartFreePercent)
	}
	if cfg.Database.BlockPruneAggressiveFreePercent != 40 {
		t.Errorf("DefaultConfig().Database.BlockPruneAggressiveFreePercent = %v, want 40", cfg.Database.BlockPruneAggressiveFreePercent)
	}
	if cfg.Database.BlockPruneStartHeightsPerBlock != 1 {
		t.Errorf("DefaultConfig().Database.BlockPruneStartHeightsPerBlock = %d, want 1", cfg.Database.BlockPruneStartHeightsPerBlock)
	}
	if cfg.Database.BlockPruneAggressiveHeightsPerBlock != 100 {
		t.Errorf("DefaultConfig().Database.BlockPruneAggressiveHeightsPerBlock = %d, want 100", cfg.Database.BlockPruneAggressiveHeightsPerBlock)
	}
	if cfg.Database.BlockPruneKeepRecentHeights != 100 {
		t.Errorf("DefaultConfig().Database.BlockPruneKeepRecentHeights = %d, want 100", cfg.Database.BlockPruneKeepRecentHeights)
	}
}

func TestLoadConfigOverridesBlockPrunePolicy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := `{"Database":{"BlockPruneStartFreePercent":30,"BlockPruneAggressiveFreePercent":15,"BlockPruneStartHeightsPerBlock":2,"BlockPruneAggressiveHeightsPerBlock":5,"BlockPruneKeepRecentHeights":500}}`
	if err := os.WriteFile(path, []byte(raw), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := loadConfig(path, "missing_frost.json", "missing_witness.json", false)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Database.BlockPruneStartFreePercent != 30 || cfg.Database.BlockPruneAggressiveFreePercent != 15 || cfg.Database.BlockPruneStartHeightsPerBlock != 2 || cfg.Database.BlockPruneAggressiveHeightsPerBlock != 5 || cfg.Database.BlockPruneKeepRecentHeights != 500 {
		t.Fatalf("loaded prune policy = %+v", cfg.Database)
	}
}

func TestLoadFrostConfigParsesConfiguredEVMGenesisFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "frost.json")
	raw := `{
		"enabled": true,
 "guardian":{"btcPublicKey":"79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798","accountPublicKey":"00000000000000000000000000000000000000000000000000000000000000010000000000000000000000000000000000000000000000000000000000000002"},
		"committee": {"topN": 175, "thresholdRatio": 1.0, "epochBlocks": 100},
		"vault": {"defaultK": 4, "minK": 4, "maxK": 100, "thresholdRatio": 0.75, "count": 1, "maturityBlocks": 864000, "scaleMinerMultiplier": 100},
		"timeouts": {"nonceCommitBlocks": 2, "sigShareBlocks": 3, "aggregatorRotateBlocks": 5, "sessionMaxBlocks": 60},
		"withdraw": {"maxInFlightPerChainAsset": 1},
		"transition": {"triggerChangeRatio": 0.2, "pauseWithdrawDuringSwitch": true, "confirmBlocks": 200, "dkgCommitWindowBlocks": 5, "dkgSharingWindowBlocks": 5, "dkgDisputeWindowBlocks": 5},
		"chains": {
			"eth": {
				"authorityProtocol":3,
 "signAlgo": "SCHNORR_ALT_BN128",
				"frostVariant": "frost-bn128",
				"vaultsPerChain": 1,
				"vaultRefs": ["0x1111111111111111111111111111111111111111"],
				"initialGroupPubkeys": ["aabb"]
			}
		}
	}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write frost config failed: %v", err)
	}

	cfg, err := LoadFrostConfig(path)
	if err != nil {
		t.Fatalf("LoadFrostConfig failed: %v", err)
	}

	eth, ok := cfg.Chains["eth"]
	if !ok {
		t.Fatal("missing eth chain config")
	}
	if len(eth.VaultRefs) != 1 {
		t.Fatalf("VaultRefs count = %d, want 1", len(eth.VaultRefs))
	}
	if len(eth.InitialGroupPubkeys) != 1 {
		t.Fatalf("InitialGroupPubkeys count = %d, want 1", len(eth.InitialGroupPubkeys))
	}
	if eth.VaultRefs[0] != "0x1111111111111111111111111111111111111111" {
		t.Fatalf("VaultRefs[0] = %s", eth.VaultRefs[0])
	}
	if eth.InitialGroupPubkeys[0] != "aabb" {
		t.Fatalf("InitialGroupPubkeys[0] = %s", eth.InitialGroupPubkeys[0])
	}
}
