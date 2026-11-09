package main

import (
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"dex/config"
	"dex/vm"
)

func TestGenerateNetworkCreatesCompleteRunnableConfig(t *testing.T) {
	output := filepath.Join(t.TempDir(), "network")
	if err := generateNetwork(output, 3, 8675309, "127.0.0.1", 6100); err != nil {
		t.Fatalf("generateNetwork: %v", err)
	}

	for _, name := range []string{
		"config.json",
		"genesis.json",
		"frost_default.json",
		"witness_default.json",
		"peers.json",
		"identities.json",
		"secrets",
	} {
		if _, err := os.Stat(filepath.Join(output, name)); err != nil {
			t.Errorf("generated output is missing %s: %v", name, err)
		}
	}

	runtimeConfig, err := config.LoadFromDir(output)
	if err != nil {
		t.Fatalf("LoadFromDir: %v", err)
	}
	if runtimeConfig.Network.BasePort != 6100 || runtimeConfig.Network.DefaultNumNodes != 3 || runtimeConfig.Network.AdvertiseHost != "127.0.0.1" {
		t.Fatalf("unexpected runtime network config: %#v", runtimeConfig.Network)
	}
	if runtimeConfig.Frost.Enabled || runtimeConfig.Witness.Enabled {
		t.Fatalf("unsafe optional modules enabled: frost=%v witness=%v", runtimeConfig.Frost.Enabled, runtimeConfig.Witness.Enabled)
	}
	frostConfig, err := config.LoadFrostConfig(filepath.Join(output, "frost_default.json"))
	if err != nil || frostConfig.Enabled {
		t.Fatalf("generated FROST config is not disabled and loadable: enabled=%v err=%v", frostConfig.Enabled, err)
	}
	witnessConfig, err := config.LoadWitnessConfig(filepath.Join(output, "witness_default.json"))
	if err != nil || witnessConfig.Enabled {
		t.Fatalf("generated Witness config is not disabled and loadable: enabled=%v err=%v", witnessConfig.Enabled, err)
	}

	manifest, err := config.LoadPeerManifest(filepath.Join(output, "peers.json"))
	if err != nil {
		t.Fatalf("LoadPeerManifest: %v", err)
	}
	genesis, err := config.LoadGenesisConfig(filepath.Join(output, "genesis.json"))
	if err != nil {
		t.Fatalf("LoadGenesisConfig: %v", err)
	}
	if manifest.ChainID != 8675309 || genesis.EffectiveChainID() != manifest.ChainID {
		t.Fatalf("chain IDs differ: peers=%d genesis=%d", manifest.ChainID, genesis.EffectiveChainID())
	}
	if len(manifest.GenesisValidators) != 3 || len(manifest.BootstrapEndpoints) != 3 {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
	wantEndpoints := map[string]bool{
		"127.0.0.1:6100": false,
		"127.0.0.1:6101": false,
		"127.0.0.1:6102": false,
	}
	for _, endpoint := range manifest.BootstrapEndpoints {
		if _, expected := wantEndpoints[endpoint.Endpoint]; !expected {
			t.Errorf("unexpected default endpoint %q", endpoint.Endpoint)
		} else {
			wantEndpoints[endpoint.Endpoint] = true
		}
	}
	for endpoint, found := range wantEndpoints {
		if !found {
			t.Errorf("default endpoint %s was not generated", endpoint)
		}
	}

	publicBytes, err := os.ReadFile(filepath.Join(output, "peers.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(publicBytes)), "private_key") {
		t.Fatal("public manifest contains a private-key field")
	}
	secretFiles, err := os.ReadDir(filepath.Join(output, "secrets"))
	if err != nil {
		t.Fatal(err)
	}
	if len(secretFiles) != 3 {
		t.Fatalf("secret file count=%d want=3", len(secretFiles))
	}
	for _, validator := range manifest.GenesisValidators {
		secretPath := filepath.Join(output, "secrets", validator.Address+".key")
		secretInfo, statErr := os.Stat(secretPath)
		if statErr != nil {
			t.Fatalf("missing separate secret for %s: %v", validator.Address, statErr)
		}
		if runtime.GOOS != "windows" && secretInfo.Mode().Perm() != 0o600 {
			t.Errorf("secret permissions=%o want=600", secretInfo.Mode().Perm())
		}
		allocation, exists := genesis.Alloc[validator.Address]
		if !exists {
			t.Fatalf("missing genesis allocation for %s", validator.Address)
		}
		locked, ok := new(big.Int).SetString(allocation.Balances["FB"].MinerLockedBalance, 10)
		if !ok || locked.Cmp(vm.MinMinerStake) < 0 {
			t.Fatalf("validator %s has invalid genesis stake %q", validator.Address, allocation.Balances["FB"].MinerLockedBalance)
		}
	}
	if err := validateGeneratedNetworkDirectory(output, 3, 8675309); err != nil {
		t.Fatalf("post-generation validation: %v", err)
	}
}

func TestGenerateMixedGenesisAndLateJoinTopology(t *testing.T) {
	output := filepath.Join(t.TempDir(), "network")
	const (
		validatorCount = 2
		identityCount  = 5
		chainID        = uint64(77123)
	)
	endpoints := []string{
		"node-0.example:7000",
		"node-1.example:7001",
		"node-2.example:7002",
		"node-3.example:7003",
		"node-4.example:7004",
	}
	if err := generateNetworkTopology(output, validatorCount, identityCount, chainID, "ignored", -1, strings.Join(endpoints, ",")); err != nil {
		t.Fatalf("generate mixed topology: %v", err)
	}
	if err := validateExistingNetworkTopology(output, validatorCount, identityCount, chainID); err != nil {
		t.Fatalf("validate mixed topology: %v", err)
	}

	manifest, err := config.LoadPeerManifest(filepath.Join(output, "peers.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.GenesisValidators) != validatorCount || len(manifest.BootstrapEndpoints) != validatorCount {
		t.Fatalf("manifest contains non-genesis identities: validators=%d routes=%d", len(manifest.GenesisValidators), len(manifest.BootstrapEndpoints))
	}
	genesis, err := config.LoadGenesisConfig(filepath.Join(output, "genesis.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(genesis.Alloc) != validatorCount {
		t.Fatalf("genesis allocation count=%d want=%d", len(genesis.Alloc), validatorCount)
	}
	document, err := loadIdentityDocument(filepath.Join(output, "identities.json"))
	if err != nil {
		t.Fatal(err)
	}
	if document.Kind != identityDocumentKind || document.GenesisValidatorCount != validatorCount || len(document.Nodes) != identityCount {
		t.Fatalf("unexpected identity document: %#v", document)
	}
	validatorAddresses := make(map[string]struct{}, validatorCount)
	for _, validator := range manifest.GenesisValidators {
		validatorAddresses[validator.Address] = struct{}{}
	}
	seenAddresses := make(map[string]struct{}, identityCount)
	for slot, node := range document.Nodes {
		if node.Slot != slot || node.Endpoint != endpoints[slot] {
			t.Fatalf("slot %d mapping=%#v", slot, node)
		}
		if _, exists := seenAddresses[node.Address]; exists {
			t.Fatalf("duplicate generated address %s", node.Address)
		}
		seenAddresses[node.Address] = struct{}{}
		_, isValidator := validatorAddresses[node.Address]
		if slot < validatorCount {
			if node.Role != identityRoleGenesis || !isValidator {
				t.Fatalf("slot %d is not a manifest genesis validator: %#v", slot, node)
			}
			if _, exists := genesis.Alloc[node.Address]; !exists {
				t.Fatalf("genesis slot %d has no allocation", slot)
			}
		} else {
			if node.Role != identityRoleLateJoin || isValidator {
				t.Fatalf("slot %d leaked into genesis manifest: %#v", slot, node)
			}
			if _, exists := genesis.Alloc[node.Address]; exists {
				t.Fatalf("late-join slot %d received a genesis allocation", slot)
			}
		}
		if err := validateGeneratedSecret(filepath.Join(output, "secrets", node.Address+".key"), node.Address); err != nil {
			t.Fatalf("slot %d secret mismatch: %v", slot, err)
		}
	}
	secretEntries, err := os.ReadDir(filepath.Join(output, "secrets"))
	if err != nil {
		t.Fatal(err)
	}
	if len(secretEntries) != identityCount {
		t.Fatalf("secret count=%d want=%d", len(secretEntries), identityCount)
	}
	identityBytes, err := os.ReadFile(filepath.Join(output, "identities.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(identityBytes)), "private_key") {
		t.Fatal("operator-local identity map contains a private-key field")
	}
}

func TestMixedTopologyValidationRejectsLateJoinSecretTampering(t *testing.T) {
	output := filepath.Join(t.TempDir(), "network")
	if err := generateNetworkTopology(output, 1, 2, 91, "127.0.0.1", 7600, ""); err != nil {
		t.Fatal(err)
	}
	document, err := loadIdentityDocument(filepath.Join(output, "identities.json"))
	if err != nil {
		t.Fatal(err)
	}
	lateJoin := document.Nodes[1]
	if err := os.WriteFile(filepath.Join(output, "secrets", lateJoin.Address+".key"), []byte(strings.Repeat("0", 64)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateExistingNetworkTopology(output, 1, 2, 91); err == nil {
		t.Fatal("tampered late-join secret was accepted")
	}
}

func TestLegacyAllGenesisDirectoryWithoutIdentityMapStillValidates(t *testing.T) {
	output := filepath.Join(t.TempDir(), "network")
	if err := generateNetwork(output, 2, 92, "127.0.0.1", 7700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(output, "identities.json")); err != nil {
		t.Fatal(err)
	}
	if err := validateExistingNetworkTopology(output, 2, 2, 92); err != nil {
		t.Fatalf("legacy all-genesis directory rejected: %v", err)
	}
	if err := validateExistingNetworkTopology(output, 1, 2, 92); err == nil {
		t.Fatal("mixed topology without identities.json was accepted")
	}
}

func TestGenerateNetworkUsesExactEndpointList(t *testing.T) {
	output := filepath.Join(t.TempDir(), "network")
	exact := []string{"node-a.example:7101", "[2001:db8::2]:7102", "127.0.0.1:7103"}
	if err := generateNetworkWithEndpoints(output, len(exact), 73, "ignored:invalid", -1, strings.Join(exact, ",")); err != nil {
		t.Fatalf("generate explicit endpoints: %v", err)
	}
	manifest, err := config.LoadPeerManifest(filepath.Join(output, "peers.json"))
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]bool, len(manifest.BootstrapEndpoints))
	for _, endpoint := range manifest.BootstrapEndpoints {
		got[endpoint.Endpoint] = true
	}
	for _, endpoint := range exact {
		if !got[endpoint] {
			t.Errorf("exact endpoint %q was not preserved: %v", endpoint, got)
		}
	}
	runtimeConfig, err := config.LoadFromDir(output)
	if err != nil {
		t.Fatal(err)
	}
	if runtimeConfig.Network.AdvertiseHost != "node-a.example" || runtimeConfig.Network.BasePort != 7101 {
		t.Fatalf("runtime fallback does not use first explicit endpoint: %#v", runtimeConfig.Network)
	}
}

func TestResolveEndpointsRejectsInvalidLists(t *testing.T) {
	tests := []struct {
		name       string
		validators int
		endpoints  string
	}{
		{"wrong count", 2, "node-a:6000"},
		{"empty item", 2, "node-a:6000,"},
		{"URL scheme", 1, "https://node-a:6000"},
		{"missing port", 1, "node-a"},
		{"zero port", 1, "node-a:0"},
		{"large port", 1, "node-a:65536"},
		{"named port", 1, "node-a:https"},
		{"invalid host", 1, "bad_host:6000"},
		{"unbracketed IPv6", 1, "2001:db8::2:6000"},
		{"canonical duplicate", 2, "NODE-A:6000,node-a.:06000"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, _, err := resolveEndpoints(test.validators, "ignored", 6000, test.endpoints); err == nil {
				t.Fatalf("invalid endpoint list was accepted: %q", test.endpoints)
			}
		})
	}
}

func TestResolveDefaultEndpointsValidatesHostAndPortRange(t *testing.T) {
	tests := []struct {
		name       string
		host       string
		basePort   int
		validators int
	}{
		{"host includes port", "node-a:6000", 6000, 1},
		{"invalid DNS host", "bad_host", 6000, 1},
		{"zero port", "node-a", 0, 1},
		{"range exceeds maximum", "node-a", 65535, 2},
		{"overflowing base port", "node-a", int(^uint(0) >> 1), 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, _, err := resolveEndpoints(test.validators, test.host, test.basePort, ""); err == nil {
				t.Fatalf("invalid defaults accepted: host=%q port=%d validators=%d", test.host, test.basePort, test.validators)
			}
		})
	}
}

func TestGenerateNetworkRefusesOverwrite(t *testing.T) {
	output := filepath.Join(t.TempDir(), "network")
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(output, "operator-data")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := generateNetwork(output, 1, 1, "localhost", 6200); err == nil {
		t.Fatal("generation unexpectedly overwrote an existing directory")
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "keep" {
		t.Fatalf("existing output was changed: %q %v", data, err)
	}
}

func TestAtomicDirectoryFailureLeavesNoPartialOutput(t *testing.T) {
	parent := t.TempDir()
	output := filepath.Join(parent, "network")
	wantErr := errors.New("injected populate failure")
	err := createDirectoryAtomically(output, func(staging string) error {
		if writeErr := os.WriteFile(filepath.Join(staging, "partial"), []byte("partial"), 0o600); writeErr != nil {
			t.Fatal(writeErr)
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("error=%v want=%v", err, wantErr)
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		t.Fatalf("failed generation left target behind: %v", err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed generation left temporary artifacts: %v", entries)
	}
}

func TestInvalidEndpointGenerationLeavesNoOutput(t *testing.T) {
	output := filepath.Join(t.TempDir(), "network")
	if err := generateNetworkWithEndpoints(output, 2, 1, "localhost", 6200, "node-a:7000"); err == nil {
		t.Fatal("invalid endpoint count was accepted")
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		t.Fatalf("invalid generation left output behind: %v", err)
	}
}

func TestValidateExistingNetworkDirectoryRejectsSecretTampering(t *testing.T) {
	output := filepath.Join(t.TempDir(), "network")
	const chainID = uint64(424242)
	if err := generateNetwork(output, 2, chainID, "127.0.0.1", 7300); err != nil {
		t.Fatalf("generate network: %v", err)
	}
	if err := validateExistingNetworkDirectory(output, 2, chainID); err != nil {
		t.Fatalf("validate untouched network: %v", err)
	}
	if err := validateExistingNetworkDirectory(output, 1, chainID); err == nil {
		t.Fatal("validator-count mismatch was accepted")
	}
	if err := validateExistingNetworkDirectory(output, 2, chainID+1); err == nil {
		t.Fatal("chain-ID mismatch was accepted")
	}

	manifest, err := config.LoadPeerManifest(filepath.Join(output, "peers.json"))
	if err != nil {
		t.Fatal(err)
	}
	tamperedPath := filepath.Join(output, "secrets", manifest.GenesisValidators[0].Address+".key")
	if err := os.WriteFile(tamperedPath, []byte(strings.Repeat("0", 64)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateExistingNetworkDirectory(output, 2, chainID); err == nil {
		t.Fatal("tampered validator secret was accepted")
	}
}
