//go:build linux

package main

import (
	"dex/config"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

                                                                              
                                                                           
func TestGenerateNodeImportAcceptanceFixture(t *testing.T) {
	dir := os.Getenv("DEX_TEST_NODE_IMPORT_FIXTURE_DIR")
	if dir == "" {
		t.Skip("real-process acceptance fixture was not requested")
	}
	if !filepath.IsAbs(dir) {
		t.Fatal("fixture directory must be absolute")
	}
	rel, err := filepath.Rel(os.TempDir(), filepath.Clean(dir))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Fatal("fixture directory must be below TMPDIR")
	}
	configDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(path string, data []byte) {
		t.Helper()
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := f.Write(data)
		closeErr := f.Close()
		if writeErr != nil {
			t.Fatal(writeErr)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	writeJSON := func(path string, value any) {
		t.Helper()
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		write(path, data)
	}
	peer := testPeerWithIdentity(t, 6, true)                          
	const chainID = 990091
	writeJSON(filepath.Join(configDir, "config.json"), map[string]any{
		"Frost":   map[string]any{"enabled": false},
		"Witness": map[string]any{"enabled": false},
		"Server":  map[string]any{"EnableDebugAPI": false, "EnablePprof": false},
		"Network": map[string]any{"BasePort": 29601, "DefaultNumNodes": 1},
	})
	for _, file := range []string{"frost_default.json", "witness_default.json"} {
		writeJSON(filepath.Join(configDir, file), map[string]bool{"enabled": false})
	}
	genesis := config.DefaultGenesisConfig()
	genesis.ChainID = chainID
	genesis.Tokens = []config.GenesisToken{{Address: "FB", TotalSupply: "4200000000", Owner: "0x0", Type: "SYSTEM"}}
	genesis.Alloc = map[string]config.GenesisAlloc{"default": {Balances: map[string]config.GenesisBalance{
		"FB": {Balance: "1000000000", MinerLockedBalance: "3200000000"},
	}}}
	writeJSON(filepath.Join(configDir, "genesis.json"), genesis)
	manifest := config.PeerManifest{
		SchemaVersion: config.PeerManifestSchemaVersion, Kind: config.PeerManifestKind,
		Notice: "DUMMY ACCEPTANCE FIXTURE ONLY - NO SECRETS", ChainID: chainID,
		GenesisValidators:  []config.GenesisValidatorConfig{publicValidator(peer)},
		BootstrapEndpoints: []config.BootstrapEndpointConfig{{Address: peer.Address, Endpoint: "127.0.0.1:29601"}},
	}
	peersFile := filepath.Join(configDir, "peers.json")
	writeJSON(peersFile, manifest)
	source := filepath.Join(dir, "source.hex")
	write(source, []byte(testPrivateKeyForIndex(6)+"\n"))
	writeJSON(filepath.Join(dir, "metadata.json"), map[string]any{
		"version": 1, "private_key_file": source, "expected_address": peer.Address,
		"config_dir": configDir, "peers_file": peersFile,
	})
	cfg, err := config.LoadFromDir(configDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Frost.Enabled || cfg.Witness.Enabled || cfg.Server.EnableDebugAPI || cfg.Server.EnablePprof {
		t.Fatal("isolated fixture unexpectedly enables external services or diagnostics")
	}
	if _, err := config.LoadPeerManifest(peersFile); err != nil {
		t.Fatal(err)
	}
	t.Log("Wrote dummy node-start fixture and public metadata")
}
