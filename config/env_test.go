package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveConfigDir(t *testing.T) {
	if got := ResolveConfigDir("", ""); got != filepath.Join("config", "env", "dev") {
		t.Fatalf("default config dir = %q", got)
	}
	if got := ResolveConfigDir("test", ""); got != filepath.Join("config", "env", "test") {
		t.Fatalf("env config dir = %q", got)
	}
	if got := ResolveConfigDir("test", filepath.Join("x", "prod")); got != filepath.Join("x", "prod") {
		t.Fatalf("explicit config dir = %q", got)
	}
}

func TestLoadPeers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "peers.json")
	data := []byte(`{
		"schema_version":1,
		"kind":"public-peer-manifest",
		"_notice":"EXAMPLE PUBLIC DATA ONLY — NO SECRETS",
		"chain_id":1,
        "genesis_validators":[{"address":"addr","reward_address":"reward","secp_public_key":"02","bls_public_key":"03","is_miner":true}],
		"bootstrap_endpoints":[{"address":"addr","endpoint":"node:6000"}]
	}`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	peers, err := LoadPeers(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 1 || peers[0].Address != "addr" || !peers[0].IsMiner {
		t.Fatalf("unexpected peers: %+v", peers)
	}
	manifest, err := LoadPeerManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ChainID != 1 || len(manifest.BootstrapEndpoints) != 1 || manifest.BootstrapEndpoints[0].Endpoint != "node:6000" {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}

	invalid := map[string]string{
		"array":         `[{"address":"addr"}]`,
		"notice":        `{"schema_version":1,"kind":"public-peer-manifest","_notice":"public","chain_id":1,"genesis_validators":[],"bootstrap_endpoints":[]}`,
		"private_key":   `{"schema_version":1,"kind":"public-peer-manifest","chain_id":1,"genesis_validators":[{"address":"addr","private_key":""}],"bootstrap_endpoints":[]}`,
		"index":         `{"schema_version":1,"kind":"public-peer-manifest","chain_id":1,"genesis_validators":[{"address":"addr","index":0}],"bootstrap_endpoints":[]}`,
		"missing_route": `{"schema_version":1,"kind":"public-peer-manifest","_notice":"PUBLIC NO SECRETS","chain_id":1,"genesis_validators":[{"address":"addr","reward_address":"reward","secp_public_key":"02","bls_public_key":"03","is_miner":true}],"bootstrap_endpoints":[]}`,
		"trailing":      `{"schema_version":1,"kind":"public-peer-manifest","chain_id":1,"genesis_validators":[],"bootstrap_endpoints":[]} {}`,
	}
	for name, raw := range invalid {
		t.Run(name, func(t *testing.T) {
			invalidPath := filepath.Join(dir, name+".json")
			if err := os.WriteFile(invalidPath, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadPeerManifest(invalidPath); err == nil {
				t.Fatalf("invalid manifest %s was accepted", name)
			}
		})
	}
}
