package main

import (
	"bytes"
	"dex/config"
	"dex/utils"
	"encoding/hex"
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseRuntimeOptionsEnableArbitrage(t *testing.T) {
	originalCommandLine := flag.CommandLine
	originalArgs := os.Args
	t.Cleanup(func() {
		flag.CommandLine = originalCommandLine
		os.Args = originalArgs
	})

	for _, tt := range []struct {
		name string
		args []string
		want bool
	}{
		{name: "default disabled", args: []string{"dex"}, want: false},
		{name: "explicit enabled", args: []string{"dex", "--enable-arbitrage"}, want: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			flag.CommandLine = flag.NewFlagSet("dex-test", flag.ContinueOnError)
			flag.CommandLine.SetOutput(io.Discard)
			os.Args = tt.args
			opts := parseRuntimeOptions()
			if opts.EnableArbitrage != tt.want {
				t.Fatalf("EnableArbitrage=%v, want %v for args %q", opts.EnableArbitrage, tt.want, tt.args)
			}
		})
	}
}

func TestApplyArbitrageRuntimeOverrideIsEnableOnly(t *testing.T) {
	disabled := config.DefaultConfig()
	applyArbitrageRuntimeOverride(disabled, false)
	if disabled.Arbitrage.Enabled {
		t.Fatal("omitted runtime flag changed a disabled config")
	}
	applyArbitrageRuntimeOverride(disabled, true)
	if !disabled.Arbitrage.Enabled {
		t.Fatal("explicit runtime flag did not enable arbitrage")
	}

	enabled := config.DefaultConfig()
	enabled.Arbitrage.Enabled = true
	applyArbitrageRuntimeOverride(enabled, false)
	if !enabled.Arbitrage.Enabled {
		t.Fatal("omitted runtime flag disabled config-enabled arbitrage")
	}
}

func writePeerConfig(t *testing.T, validators []config.GenesisValidatorConfig, endpoints []config.BootstrapEndpointConfig) string {
	t.Helper()
	if validators == nil {
		validators = []config.GenesisValidatorConfig{}
	}
	if endpoints == nil {
		endpoints = []config.BootstrapEndpointConfig{}
	}
	payload, err := json.Marshal(config.PeerManifest{
		SchemaVersion:      config.PeerManifestSchemaVersion,
		Kind:               config.PeerManifestKind,
		Notice:             "EXAMPLE PUBLIC DATA ONLY — NO SECRETS",
		ChainID:            1,
		GenesisValidators:  validators,
		BootstrapEndpoints: endpoints,
	})
	if err != nil {
		t.Fatalf("marshal peers config: %v", err)
	}
	path := filepath.Join(t.TempDir(), "peers.json")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("write peers config: %v", err)
	}
	return path
}

func publicValidator(fixture PeerInfo) config.GenesisValidatorConfig {
	return config.GenesisValidatorConfig{
		Address:       fixture.Address,
		RewardAddress: fixture.Address,
		SecpPublicKey: hex.EncodeToString(fixture.SecpPublicKey),
		BLSPublicKey:  hex.EncodeToString(fixture.BLSPublicKey),
		IsMiner:       fixture.IsMiner,
	}
}

func TestLoadPeerInfoListAcceptsCanonicalPublicOnlyMiner(t *testing.T) {
	fixture := testPeerWithIdentity(t, 7, true)
	privateKey, err := utils.ParseSecp256k1PrivateKey(testPrivateKeyForIndex(7))
	if err != nil {
		t.Fatalf("parse fixture private key: %v", err)
	}
	validator := publicValidator(fixture)
	validator.SecpPublicKey = "0X" + hex.EncodeToString(privateKey.PubKey().SerializeUncompressed())
	validator.BLSPublicKey = "0x" + validator.BLSPublicKey
	path := writePeerConfig(t, []config.GenesisValidatorConfig{validator}, []config.BootstrapEndpointConfig{{
		Address: fixture.Address, Endpoint: "miner.example:7007",
	}})

	manifest, peers, err := loadPeerManifestInfo(path)
	if err != nil {
		t.Fatalf("load peer manifest failed: %v", err)
	}
	if manifest.ChainID != 1 || len(peers) != 1 {
		t.Fatalf("unexpected manifest/peer count: chain=%d peers=%d", manifest.ChainID, len(peers))
	}
	got := peers[0]
	if got.Index != 0 || got.Address != fixture.Address || got.AdvertiseAddr != "miner.example:7007" || got.Port != "7007" {
		t.Fatalf("unexpected public peer: %+v", got)
	}
	if !bytes.Equal(got.SecpPublicKey, fixture.SecpPublicKey) || !bytes.Equal(got.BLSPublicKey, fixture.BLSPublicKey) {
		t.Fatal("public keys were not canonicalized")
	}
}

func TestLoadPeerInfoListAssignsDeterministicInternalSlots(t *testing.T) {
	first := testPeerWithIdentity(t, 11, true)
	second := testPeerWithIdentity(t, 2, true)
	path := writePeerConfig(t,
		[]config.GenesisValidatorConfig{publicValidator(first), publicValidator(second)},
		[]config.BootstrapEndpointConfig{
			{Address: second.Address, Endpoint: "second.example:6002"},
			{Address: first.Address, Endpoint: "first.example:6011"},
		},
	)
	peers, err := loadPeerInfoList(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 2 || peers[0].Index != 0 || peers[1].Index != 1 || peers[0].Address >= peers[1].Address {
		t.Fatalf("slots are not canonical address order: %+v", peers)
	}
}

func TestLoadPeerInfoListRejectsInvalidPublicManifest(t *testing.T) {
	first := testPeerWithIdentity(t, 1, true)
	second := testPeerWithIdentity(t, 2, true)
	tests := []struct {
		name       string
		validators []config.GenesisValidatorConfig
		endpoints  []config.BootstrapEndpointConfig
		wantErr    string
	}{
		{
			name: "address does not match public key",
			validators: []config.GenesisValidatorConfig{{
				Address: second.Address, RewardAddress: second.Address, SecpPublicKey: hex.EncodeToString(first.SecpPublicKey), BLSPublicKey: hex.EncodeToString(first.BLSPublicKey), IsMiner: true,
			}},
			endpoints: []config.BootstrapEndpointConfig{{Address: second.Address, Endpoint: "second.example:6000"}},
			wantErr:   "address does not match secp_public_key",
		},
		{
			name: "miner missing BLS key",
			validators: []config.GenesisValidatorConfig{{
				Address: first.Address, RewardAddress: first.Address, SecpPublicKey: hex.EncodeToString(first.SecpPublicKey), IsMiner: true,
			}},
			wantErr: "requires bls_public_key",
		},
		{
			name:       "route is not a validator",
			validators: []config.GenesisValidatorConfig{publicValidator(first)},
			endpoints:  []config.BootstrapEndpointConfig{{Address: second.Address, Endpoint: "other.example:6000"}},
			wantErr:    "is not a genesis validator",
		},
		{
			name:       "endpoint lacks port",
			validators: []config.GenesisValidatorConfig{publicValidator(first)},
			endpoints:  []config.BootstrapEndpointConfig{{Address: first.Address, Endpoint: "other.example"}},
			wantErr:    "must be host:port",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadPeerInfoList(writePeerConfig(t, tt.validators, tt.endpoints))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateLocalPeerIdentityUsesAddressAndPublicKeys(t *testing.T) {
	configured := testPeerWithIdentity(t, 5, true)
	_, local, err := deriveTestPeerIdentity(testPrivateKeyForIndex(5))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateLocalPeerIdentity(configured, local); err != nil {
		t.Fatalf("matching local identity rejected: %v", err)
	}
	_, wrong, err := deriveTestPeerIdentity(testPrivateKeyForIndex(6))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateLocalPeerIdentity(configured, wrong); err == nil {
		t.Fatal("mismatching local identity was accepted")
	}
}

func TestValidatePeerManifestChainID(t *testing.T) {
	manifest := &config.PeerManifest{ChainID: 7}
	if err := validatePeerManifestChainID(manifest, 7); err != nil {
		t.Fatalf("matching chain ID rejected: %v", err)
	}
	if err := validatePeerManifestChainID(manifest, 8); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatching chain ID error = %v", err)
	}
}

func TestNodeDataPathUsesAddressIdentity(t *testing.T) {
	const address = "fbc1qaddress"
	if got, want := nodeDataPath("", address), filepath.Join("data", address); got != want {
		t.Fatalf("default data path = %q, want %q", got, want)
	}
	if got := nodeDataPath("operator-data", address); got != "operator-data" {
		t.Fatalf("explicit data path = %q", got)
	}
}

func TestValidateExpectedAddress(t *testing.T) {
	identity := testPeerWithIdentity(t, 17, true)
	if err := validateExpectedAddress(identity.Address, identity.Address); err != nil {
		t.Fatalf("matching expected address rejected: %v", err)
	}
	other := testPeerWithIdentity(t, 18, true)
	if err := validateExpectedAddress(other.Address, identity.Address); err == nil {
		t.Fatal("mismatched expected address was accepted")
	}
	if err := validateExpectedAddress("node-17", identity.Address); err == nil {
		t.Fatal("non-address expected identity was accepted")
	}
}

func TestValidateExpectedArbitrageAddressUsesIndependentFlag(t *testing.T) {
	_, identity, err := deriveTestPeerIdentity(testPrivateKeyForIndex(15))
	if err != nil {
		t.Fatal(err)
	}
	_, other, err := deriveTestPeerIdentity(testPrivateKeyForIndex(16))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateExpectedArbitrageAddress(identity.Address, identity.Address); err != nil {
		t.Fatalf("matching arbitrage address rejected: %v", err)
	}
	if err := validateExpectedArbitrageAddress(other.Address, identity.Address); err == nil || !strings.Contains(err.Error(), "--expected-arbitrage-address") {
		t.Fatalf("mismatched arbitrage address error = %v", err)
	}
}

func TestValidateIndependentArbitrageAddress(t *testing.T) {
	_, miner, err := deriveTestPeerIdentity(testPrivateKeyForIndex(22))
	if err != nil {
		t.Fatal(err)
	}
	_, trader, err := deriveTestPeerIdentity(testPrivateKeyForIndex(23))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateIndependentArbitrageAddress(miner.Address, trader.Address); err != nil {
		t.Fatalf("independent account rejected: %v", err)
	}
	if err := validateIndependentArbitrageAddress(miner.Address, miner.Address); err == nil || !strings.Contains(err.Error(), "must differ") {
		t.Fatalf("shared miner/arbitrage identity accepted: %v", err)
	}
}
