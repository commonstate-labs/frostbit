package main

import (
	"dex/config"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckConfigExitsBeforeSecretsLogsAndRuntime(t *testing.T) {
	for _, tc := range []struct {
		name, witness, runtime, frost, want string
	}{
		{"production", `{"arbitrationPeriodBlocks":10000}`, `{}`, `{"enabled":false}`, ""},
		{"explicit dev", `{"arbitrationTimingProfile":"dev","arbitrationPeriodBlocks":20}`, `{}`, `{"enabled":false}`, ""},
		{"installed legacy period", `{"arbitrationPeriodBlocks":30}`, `{}`, `{}`, "production arbitrationPeriodBlocks must be 10000"},
		{"runtime override", `{}`, `{"Witness":{"arbitrationPeriodBlocks":30}}`, `{}`, "production arbitrationPeriodBlocks must be 10000"},
		{"missing witness", "", `{}`, `{}`, "witness_default.json"},
		{"installed legacy frost policy", `{}`, `{}`, `{"vault":{"maxK":4}}`, "FROST vault K policy must be default=4 min=4 max=100"},
		{"malformed frost", `{}`, `{}`, `{"vault":`, "failed to parse frost config"},
		{"frost runtime override", `{}`, `{"Frost":{"vault":{"maxK":4}}}`, `{}`, "FROST vault K policy must be default=4 min=4 max=100"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeCheckConfigNetworkFiles(t, dir, 1, 1)
			frostContents := tc.frost
			var frostJSON map[string]any
			if json.Unmarshal([]byte(frostContents), &frostJSON) == nil {
				frostJSON["guardian"] = testDualCustodyFrostConfig().Guardian
				raw, _ := json.Marshal(frostJSON)
				frostContents = string(raw)
			}
			files := map[string]string{"config.json": tc.runtime, "frost_default.json": frostContents}
			if tc.witness != "" {
				files["witness_default.json"] = tc.witness
			}
			for name, contents := range files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0600); err != nil {
					t.Fatal(err)
				}
			}
			previousFlags, previousArgs := flag.CommandLine, os.Args
			t.Cleanup(func() { flag.CommandLine, os.Args = previousFlags, previousArgs })
			flag.CommandLine = flag.NewFlagSet("check-config", flag.ContinueOnError)
			os.Args = []string{"node", "--check-config", "--config-dir", dir, "--data-dir", filepath.Join(dir, "data")}
			t.Setenv("DEX_LOG_FILE", filepath.Join(dir, "must-not-create.log"))
			err := runMain()                                                            
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("runMain error = %v; want %q", err, tc.want)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != len(files)+2 {
				t.Fatalf("read-only config check changed the directory: entries=%v err=%v", entries, err)
			}
		})
	}
}

func TestCheckConfigRejectsMissingOrMismatchedNetworkTrustFiles(t *testing.T) {
	for _, tc := range []struct {
		name      string
		chainID   uint64
		peerChain uint64
		remove    string
		want      string
	}{
		{name: "missing genesis", chainID: 1, peerChain: 1, remove: "genesis.json", want: "genesis.json"},
		{name: "missing peers", chainID: 1, peerChain: 1, remove: "peers.json", want: "peers.json"},
		{name: "chain mismatch", chainID: 7, peerChain: 8, want: "does not match"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, contents := range map[string]string{
				"config.json":          `{}`,
				"frost_default.json":   `{"enabled":false}`,
				"witness_default.json": `{}`,
			} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0600); err != nil {
					t.Fatal(err)
				}
			}
			writeCheckConfigNetworkFiles(t, dir, tc.chainID, tc.peerChain)
			if tc.remove != "" {
				if err := os.Remove(filepath.Join(dir, tc.remove)); err != nil {
					t.Fatal(err)
				}
			}
			err := checkConfigDirectory(dir)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("checkConfigDirectory error=%v want %q", err, tc.want)
			}
		})
	}
}

func writeCheckConfigNetworkFiles(t *testing.T, dir string, genesisChainID, peerChainID uint64) {
	t.Helper()
	peer := testPeerWithIdentity(t, 0, true)
	manifest := config.PeerManifest{
		SchemaVersion: config.PeerManifestSchemaVersion,
		Kind:          config.PeerManifestKind,
		Notice:        "PUBLIC TEST MANIFEST - NO SECRETS",
		ChainID:       peerChainID,
		GenesisValidators: []config.GenesisValidatorConfig{{
			Address: peer.Address, RewardAddress: peer.RewardAddress,
			SecpPublicKey: hex.EncodeToString(peer.SecpPublicKey),
			BLSPublicKey:  hex.EncodeToString(peer.BLSPublicKey), IsMiner: true,
		}},
		BootstrapEndpoints: []config.BootstrapEndpointConfig{{Address: peer.Address, Endpoint: "127.0.0.1:6000"}},
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "peers.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	genesis := []byte(`{"chain_id":` + fmt.Sprint(genesisChainID) + `,"tokens":[],"alloc":{}}`)
	if err := os.WriteFile(filepath.Join(dir, "genesis.json"), genesis, 0600); err != nil {
		t.Fatal(err)
	}
}
