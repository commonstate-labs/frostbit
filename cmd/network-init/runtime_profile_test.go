package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dex/config"
)

func TestGeneratePhysical20StressRuntimeProfile(t *testing.T) {
	output := filepath.Join(t.TempDir(), "network")
	if err := generateNetworkTopologyWithProfile(output, 2, 2, 51015, "127.0.0.1", 6000, "", runtimeProfilePhysical20Stress, testNetworkGuardian()); err != nil {
		t.Fatalf("generate stress network: %v", err)
	}

	cfg, err := config.LoadFromDir(output)
	if err != nil {
		t.Fatalf("load stress config: %v", err)
	}
	if err := validateRuntimeProfileConfig(cfg, runtimeProfilePhysical20Stress); err != nil {
		t.Fatalf("validate stress profile: %v", err)
	}
	if cfg.TxPool.TargetTxsPerBlock != 5000 || cfg.TxPool.MaxPendingTxs != 30000 || cfg.TxPool.MaxTxsLimitPerBlock != 5000 {
		t.Fatalf("unexpected stress TxPool: %#v", cfg.TxPool)
	}
	if cfg.Sender.QueueCapacity != 5000 || cfg.Sender.TxGossipFanout != 20 || cfg.Sender.TxGossipInputCapacity != 50000 || cfg.Sender.TxGossipMaxPendingPerPeer != 20000 {
		t.Fatalf("unexpected stress Sender: %#v", cfg.Sender)
	}
	if cfg.PeerGate.TrustedRPS != 5000 || cfg.PeerGate.UnknownRPS != 5000 || cfg.PeerGate.BanThreshold != 100000 || cfg.PeerGate.TxBatchMaxInflight != 64 {
		t.Fatalf("unexpected stress PeerGate: %#v", cfg.PeerGate)
	}
	if cfg.Frost.Enabled {
		t.Fatal("legacy physical20 stress profile unexpectedly enabled FROST")
	}

	raw, err := os.ReadFile(filepath.Join(output, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"Server", "Discovery"} {
		if _, exists := document[forbidden]; exists {
			t.Fatalf("stress profile unexpectedly generated %s overrides", forbidden)
		}
	}
	if strings.Contains(string(raw), "EnableDebugAPI") || strings.Contains(string(raw), "BootstrapPeers") {
		t.Fatal("stress profile restored debug API or hard-coded discovery configuration")
	}
}

func TestGeneratePhysical20FrostStressRuntimeProfile(t *testing.T) {
	output := filepath.Join(t.TempDir(), "network")
	if err := generateNetworkTopologyWithProfile(output, 5, 5, 51017, "127.0.0.1", 6000, "", runtimeProfilePhysical20FrostStress, testNetworkGuardian()); err != nil {
		t.Fatalf("generate FROST stress network: %v", err)
	}

	cfg, err := config.LoadFromDir(output)
	if err != nil {
		t.Fatalf("load FROST stress config: %v", err)
	}
	if err := validateRuntimeProfileConfig(cfg, runtimeProfilePhysical20FrostStress); err != nil {
		t.Fatalf("validate FROST stress runtime settings: %v", err)
	}
	witnessCfg, err := config.LoadWitnessConfig(filepath.Join(output, "witness_default.json"))
	if err != nil {
		t.Fatal(err)
	}
	if witnessCfg.Enabled || witnessCfg.ArbitrationTimingProfile != "local20" || witnessCfg.ArbitrationPeriodBlocks != 20 {
		t.Fatalf("FROST stress Witness timing or enablement changed: %+v", witnessCfg)
	}
	if cfg.TxPool.TargetTxsPerBlock != 5000 || cfg.TxPool.MaxTxsLimitPerBlock != 5000 {
		t.Fatalf("FROST stress profile lost throughput settings: %#v", cfg.TxPool)
	}
	if !cfg.Frost.Enabled || cfg.Frost.Committee.ThresholdRatio != 1.0 ||
		cfg.Frost.Vault.DefaultK != 4 || cfg.Frost.Vault.MinK != 4 || cfg.Frost.Vault.MaxK != 100 ||
		cfg.Frost.Vault.MaturityBlocks != 30 || cfg.Frost.Vault.ScaleMinerMultiplier != 1 ||
		cfg.Frost.Vault.Count != 1 || cfg.Frost.Transition.DkgCommitWindowBlocks != 5 ||
		cfg.Frost.Transition.DkgSharingWindowBlocks != 5 || cfg.Frost.Transition.DkgDisputeWindowBlocks != 5 ||
		cfg.Frost.Transition.ConfirmBlocks != 10 {
		t.Fatalf("unexpected physical20 FROST profile: %#v", cfg.Frost)
	}
	for name, chain := range cfg.Frost.Chains {
		if chain.VaultsPerChain != 1 {
			t.Fatalf("physical20 FROST chain %s vault count=%d want=1", name, chain.VaultsPerChain)
		}
		if len(chain.VaultRefs) != 0 || len(chain.InitialGroupPubkeys) != 0 {
			t.Fatalf("physical20 FROST chain %s must enter online DKG without preconfigured genesis material: %#v", name, chain)
		}
		if ref := physical20OnlineDKGVaultRefs[name]; ref != "" {
			if len(chain.OnlineDKGVaultRefs) != 1 || chain.OnlineDKGVaultRefs[0] != ref {
				t.Fatalf("physical20 FROST chain %s online-DKG ref=%v want=%s", name, chain.OnlineDKGVaultRefs, ref)
			}
		} else if len(chain.OnlineDKGVaultRefs) != 0 {
			t.Fatalf("physical20 FROST chain %s unexpectedly has online-DKG refs: %v", name, chain.OnlineDKGVaultRefs)
		}
	}
}

func TestWitnessRuntimeProfileArbitrationTimingBoundary(t *testing.T) {
	for _, profile := range []runtimeProfile{runtimeProfileGeneric, runtimeProfilePhysical20Stress, runtimeProfilePhysical20FrostStress} {
		t.Run(string(profile), func(t *testing.T) {
			cfg, err := witnessConfigForRuntimeProfile(profile)
			if err != nil || cfg.Enabled {
				t.Fatalf("profile enabled Witness or failed: enabled=%v err=%v", cfg.Enabled, err)
			}
			wantBlocks, wantTiming := uint64(10000), ""
			if profile == runtimeProfilePhysical20FrostStress {
				wantBlocks, wantTiming = 20, "local20"
			}
			if cfg.ArbitrationPeriodBlocks != wantBlocks || cfg.ArbitrationTimingProfile != wantTiming {
				t.Fatalf("wrong arbitration timing: profile=%q blocks=%d", cfg.ArbitrationTimingProfile, cfg.ArbitrationPeriodBlocks)
			}
			if err := validateWitnessConfigForRuntimeProfile(cfg, profile); err != nil {
				t.Fatalf("generated timing rejected: %v", err)
			}
			for _, timing := range []string{"", "prod", "dev", "local20"} {
				for _, blocks := range []uint64{20, 30, 10000} {
					candidate := cfg
					candidate.ArbitrationTimingProfile, candidate.ArbitrationPeriodBlocks = timing, blocks
					valid := blocks == wantBlocks && (timing == wantTiming || (wantTiming == "" && timing == "prod"))
					if err := validateWitnessConfigForRuntimeProfile(candidate, profile); (err == nil) != valid {
						t.Fatalf("profile=%s timing=%q blocks=%d: want valid=%v, got %v", profile, timing, blocks, valid, err)
					}
				}
			}
		})
	}
	if _, err := witnessConfigForRuntimeProfile(runtimeProfile("unknown")); err == nil {
		t.Fatal("unknown profile generated Witness timing")
	}
}

func TestPhysical20FrostProfileFailsClosed(t *testing.T) {
	cfg, err := frostConfigForRuntimeProfile(runtimeProfilePhysical20FrostStress, testNetworkGuardian())
	if err != nil {
		t.Fatal(err)
	}
	if err := validateFrostConfigForRuntimeProfile(cfg, runtimeProfilePhysical20FrostStress, 5); err != nil {
		t.Fatalf("valid physical20 FROST config rejected: %v", err)
	}
	if err := validateFrostConfigForRuntimeProfile(cfg, runtimeProfilePhysical20FrostStress, 3); err == nil {
		t.Fatal("physical20 FROST config accepted too few genesis validators")
	}

	configured := cfg
	configured.Chains = cloneFrostChains(cfg.Chains)
	bn128Generator := strings.Repeat("0", 63) + "1" + strings.Repeat("0", 63) + "2"
	btcGenerator := "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
	refs := map[string]string{
		"eth": "0x1111111111111111111111111111111111111111", "bnb": "0x2222222222222222222222222222222222222222",
		"base": "0x3333333333333333333333333333333333333333", "arb": "0x4444444444444444444444444444444444444444",
		"trx": "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t",
	}
	for name, chain := range configured.Chains {
		if name == "btc" {
			chain.InitialGroupPubkeys = []string{btcGenerator}
		} else {
			chain.VaultRefs = []string{refs[name]}
			chain.InitialGroupPubkeys = []string{bn128Generator}
		}
		configured.Chains[name] = chain
	}
	if err := validateFrostConfigForRuntimeProfile(configured, runtimeProfilePhysical20FrostStress, 5); err != nil {
		t.Fatalf("complete standalone ceremony result rejected: %v", err)
	}
	partial := configured
	partial.Chains = cloneFrostChains(configured.Chains)
	base := partial.Chains["base"]
	base.VaultRefs = nil
	base.InitialGroupPubkeys = nil
	partial.Chains["base"] = base
	if err := validateFrostConfigForRuntimeProfile(partial, runtimeProfilePhysical20FrostStress, 5); err == nil {
		t.Fatal("partial standalone ceremony result was accepted")
	}
	tamperedProfile := configured
	tamperedProfile.Chains = cloneFrostChains(configured.Chains)
	tamperedProfile.Vault.ThresholdRatio = 0.5
	if err := validateFrostConfigForRuntimeProfile(tamperedProfile, runtimeProfilePhysical20FrostStress, 5); err == nil {
		t.Fatal("ceremony results hid a non-result profile mismatch")
	}

	disabled := cfg
	disabled.Enabled = false
	if err := validateFrostConfigForRuntimeProfile(disabled, runtimeProfilePhysical20FrostStress, 5); err == nil {
		t.Fatal("physical20 FROST config accepted enabled=false")
	}

	tooManyVaults := cfg
	tooManyVaults.Chains = cloneFrostChains(cfg.Chains)
	btc := tooManyVaults.Chains["btc"]
	btc.VaultsPerChain = 100
	tooManyVaults.Chains["btc"] = btc
	if err := validateFrostConfigForRuntimeProfile(tooManyVaults, runtimeProfilePhysical20FrostStress, 5); err == nil {
		t.Fatal("physical20 FROST config accepted the generic 100-vault setting")
	}

	longWindows := cfg
	longWindows.Transition.DkgCommitWindowBlocks = 20
	if err := validateFrostConfigForRuntimeProfile(longWindows, runtimeProfilePhysical20FrostStress, 5); err == nil {
		t.Fatal("physical20 FROST config accepted the generic DKG window")
	}

	generic, err := frostConfigForRuntimeProfile(runtimeProfileGeneric, testNetworkGuardian())
	if err != nil {
		t.Fatal(err)
	}
	if err := validateFrostConfigForRuntimeProfile(generic, runtimeProfileGeneric, 1); err != nil {
		t.Fatalf("disabled generic FROST config rejected: %v", err)
	}
	generic.Enabled = true
	if err := validateFrostConfigForRuntimeProfile(generic, runtimeProfileGeneric, 1); err == nil {
		t.Fatal("generic profile accepted enabled FROST")
	}
}

func TestPhysical20FrostStressRejectsExistingDisabledNetworkWithoutMutation(t *testing.T) {
	output := filepath.Join(t.TempDir(), "network")
	if err := generateNetworkTopologyWithProfile(output, 5, 5, 51018, "127.0.0.1", 6000, "", runtimeProfilePhysical20Stress, testNetworkGuardian()); err != nil {
		t.Fatalf("generate disabled stress network: %v", err)
	}
	configPath := filepath.Join(output, "config.json")
	frostPath := filepath.Join(output, "frost_default.json")
	configBefore, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	frostBefore, err := os.ReadFile(frostPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := validateExistingNetworkTopologyWithProfile(output, 5, 5, 51018, runtimeProfilePhysical20FrostStress); err == nil {
		t.Fatal("disabled existing network was accepted as a FROST stress network")
	}
	assertFileBytes(t, configPath, configBefore)
	assertFileBytes(t, frostPath, frostBefore)
}

func cloneFrostChains(input map[string]config.ChainFrostConfig) map[string]config.ChainFrostConfig {
	out := make(map[string]config.ChainFrostConfig, len(input))
	for name, chain := range input {
		chain.VaultRefs = append([]string(nil), chain.VaultRefs...)
		chain.OnlineDKGVaultRefs = append([]string(nil), chain.OnlineDKGVaultRefs...)
		chain.InitialGroupPubkeys = append([]string(nil), chain.InitialGroupPubkeys...)
		out[name] = chain
	}
	return out
}

func TestGenericRuntimeProfileKeepsMinimalSafeConfig(t *testing.T) {
	output := filepath.Join(t.TempDir(), "network")
	if err := generateNetworkTopologyWithProfile(output, 1, 1, 51016, "127.0.0.1", 6000, "", runtimeProfileGeneric, testNetworkGuardian()); err != nil {
		t.Fatalf("generate generic network: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(output, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if len(document) != 1 || document["Network"] == nil {
		t.Fatalf("generic profile changed the minimal runtime config: %s", raw)
	}
}

func TestPhysical20StressValidationRejectsMismatchAndUnsafeSections(t *testing.T) {
	overrides, err := overridesForRuntimeProfile(runtimeProfilePhysical20Stress)
	if err != nil {
		t.Fatal(err)
	}
	document := runtimeConfigDocument{
		Network:  runtimeNetworkConfig{BasePort: 6000, MaxNodes: 20, DefaultNumNodes: 20},
		TxPool:   overrides.TxPool,
		Sender:   overrides.Sender,
		PeerGate: overrides.PeerGate,
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	if err := json.Unmarshal(encoded, cfg); err != nil {
		t.Fatal(err)
	}
	if err := validateRuntimeProfileConfig(cfg, runtimeProfilePhysical20Stress); err != nil {
		t.Fatalf("valid stress profile rejected: %v", err)
	}

	cfg.TxPool.TargetTxsPerBlock--
	if err := validateRuntimeProfileConfig(cfg, runtimeProfilePhysical20Stress); err == nil {
		t.Fatal("mismatched stress TxPool was accepted")
	}
	cfg.TxPool.TargetTxsPerBlock++
	cfg.Server.EnableDebugAPI = true
	if err := validateRuntimeProfileConfig(cfg, runtimeProfilePhysical20Stress); err != nil {
		t.Fatalf("authenticated debug API was rejected by the stress profile: %v", err)
	}
	cfg.Server.EnablePprof = true
	if err := validateRuntimeProfileConfig(cfg, runtimeProfilePhysical20Stress); err == nil {
		t.Fatal("pprof was accepted by the stress profile")
	}
	cfg.Server.EnablePprof = false
	cfg.Discovery.BootstrapPeers = []string{"192.0.2.1:6000"}
	if err := validateRuntimeProfileConfig(cfg, runtimeProfilePhysical20Stress); err == nil {
		t.Fatal("hard-coded discovery bootstrap peers were accepted by the stress profile")
	}
	cfg.Discovery.BootstrapPeers = nil
	cfg.PeerGate.TxBatchMaxInflight--
	if err := validateRuntimeProfileConfig(cfg, runtimeProfilePhysical20Stress); err == nil {
		t.Fatal("mismatched tx batch in-flight limit was accepted by the stress profile")
	}
}

func TestApplyPhysical20StressProfileOnlyReplacesPublicRuntimeConfig(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "network")
	if err := os.MkdirAll(filepath.Join(dir, "secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	originalConfig := []byte(`{
  "Network": {"BasePort": 6000, "MaxNodes": 100, "DefaultNumNodes": 20, "advertise_host": "127.0.0.1"},
  "Server": {"EnableDebugAPI": false, "EnablePprof": false, "PprofBind": "127.0.0.1"},
  "TxPool": {"MinTxsForProposal": 7},
  "CustomPublicMetadata": {"keep": true}
}
`)
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, originalConfig, 0o640); err != nil {
		t.Fatal(err)
	}
	secretPath := filepath.Join(dir, "secrets", "node.key")
	secret := []byte("operator-secret-sentinel\n")
	if err := os.WriteFile(secretPath, secret, 0o600); err != nil {
		t.Fatal(err)
	}
	identityPath := filepath.Join(dir, "identities.json")
	identity := []byte("{\"public\":\"unchanged\"}\n")
	if err := os.WriteFile(identityPath, identity, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := applyRuntimeProfileToExistingDirectory(dir, runtimeProfilePhysical20Stress); err != nil {
		t.Fatalf("apply stress profile: %v", err)
	}
	cfg, err := config.LoadFromFile(configPath)
	if err != nil {
		t.Fatalf("load applied config: %v", err)
	}
	if err := validateRuntimeProfileConfig(cfg, runtimeProfilePhysical20Stress); err != nil {
		t.Fatalf("validate applied profile: %v", err)
	}
	if cfg.TxPool.MinTxsForProposal != 7 {
		t.Fatalf("unrelated existing TxPool field was not preserved: %d", cfg.TxPool.MinTxsForProposal)
	}
	updated, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(updated, &document); err != nil {
		t.Fatal(err)
	}
	if document["CustomPublicMetadata"] == nil || document["Server"] == nil {
		t.Fatalf("unrelated public config sections were not preserved: %s", updated)
	}
	assertFileBytes(t, secretPath, secret)
	assertFileBytes(t, identityPath, identity)

	firstInfo, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := applyRuntimeProfileToExistingDirectory(dir, runtimeProfilePhysical20Stress); err != nil {
		t.Fatalf("idempotent stress apply: %v", err)
	}
	secondInfo, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(firstInfo, secondInfo) {
		t.Fatal("idempotent apply replaced config.json instead of returning without a write")
	}
	assertFileBytes(t, configPath, updated)
	assertFileBytes(t, secretPath, secret)
	if matches, err := filepath.Glob(filepath.Join(dir, ".config.json.runtime-profile-*")); err != nil || len(matches) != 0 {
		t.Fatalf("temporary profile files remain: %v err=%v", matches, err)
	}
}

func TestApplyPhysical20StressProfileFailsClosed(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "network")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	unsafeConfig := []byte(`{
  "Network": {"MaxNodes": 100},
  "Server": {"EnablePprof": true, "PprofBind": "0.0.0.0"}
}
`)
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, unsafeConfig, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := applyRuntimeProfileToExistingDirectory(dir, runtimeProfilePhysical20Stress); err == nil {
		t.Fatal("stress apply accepted enabled pprof")
	}
	assertFileBytes(t, configPath, unsafeConfig)

	invalidConfig := []byte("{not-json\n")
	if err := os.WriteFile(configPath, invalidConfig, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := applyRuntimeProfileToExistingDirectory(dir, runtimeProfilePhysical20Stress); err == nil {
		t.Fatal("stress apply accepted invalid JSON")
	}
	assertFileBytes(t, configPath, invalidConfig)
	if err := applyRuntimeProfileToExistingDirectory(dir, runtimeProfileGeneric); err == nil {
		t.Fatal("generic apply was accepted without an explicit stress profile")
	}
	if err := applyRuntimeProfileToExistingDirectory(dir, runtimeProfilePhysical20FrostStress); err == nil {
		t.Fatal("genesis-time FROST stress profile was accepted as an in-place config upgrade")
	}
	if _, err := parseRuntimeProfile("unknown"); err == nil {
		t.Fatal("unknown runtime profile was accepted")
	}
}

func assertFileBytes(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("file %s changed: got %q want %q", path, got, want)
	}
}
