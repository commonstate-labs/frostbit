package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"dex/config"
)

func TestBuildNodesFromConfigUsesOperatorIdentities(t *testing.T) {
	directory, peersPath, identitiesPath := writeExplorerTopologyFixture(t)
	nodes, err := buildNodesFromConfig("", "127.0.0.1", 6000, 20, directory, peersPath, identitiesPath, true, 51015)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"192.0.2.10:6000", "192.0.2.11:6000", "192.0.2.12:6000"}
	if !reflect.DeepEqual(nodes, want) {
		t.Fatalf("nodes=%v want=%v", nodes, want)
	}
}

func TestBuildNodesFromConfigExplicitEmptyManifestFailsClosed(t *testing.T) {
	directory := t.TempDir()
	manifest := config.PeerManifest{
		SchemaVersion:      config.PeerManifestSchemaVersion,
		Kind:               config.PeerManifestKind,
		Notice:             "PUBLIC TEMPLATE — NO SECRETS",
		ChainID:            51015,
		GenesisValidators:  []config.GenesisValidatorConfig{},
		BootstrapEndpoints: []config.BootstrapEndpointConfig{},
	}
	peersPath := writeExplorerTopologyJSON(t, directory, "peers.json", manifest)
	if _, err := buildNodesFromConfig("", "127.0.0.1", 6000, 20, directory, peersPath, "", true, 51015); err == nil {
		t.Fatal("explicit empty peers manifest silently fell back to localhost")
	}
}

func TestBuildNodesFromConfigLegacyFallbackRequiresNoExplicitTopology(t *testing.T) {
	directory := t.TempDir()
	nodes, err := buildNodesFromConfig("", "127.0.0.1", 6000, 3, directory, "", "", false, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"127.0.0.1:6000", "127.0.0.1:6001", "127.0.0.1:6002"}
	if !reflect.DeepEqual(nodes, want) {
		t.Fatalf("nodes=%v want=%v", nodes, want)
	}
}

func TestBuildNodesFromConfigExplicitNodesTakePriority(t *testing.T) {
	nodes, err := buildNodesFromConfig("node-a:6000,node-b:6000", "127.0.0.1", 6000, 20, t.TempDir(), "missing-peers.json", "missing-identities.json", true, 51015)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"node-a:6000", "node-b:6000"}
	if !reflect.DeepEqual(nodes, want) {
		t.Fatalf("nodes=%v want=%v", nodes, want)
	}
}

func TestBuildNodesFromConfigRejectsIdentityChainMismatch(t *testing.T) {
	directory, peersPath, identitiesPath := writeExplorerTopologyFixture(t)
	if _, err := buildNodesFromConfig("", "127.0.0.1", 6000, 20, directory, peersPath, identitiesPath, true, 51016); err == nil {
		t.Fatal("operator topology with a different genesis chain ID was accepted")
	}
}

func writeExplorerTopologyFixture(t *testing.T) (string, string, string) {
	t.Helper()
	directory := t.TempDir()
	addresses := []string{explorerTopologyAddress('2'), explorerTopologyAddress('3'), explorerTopologyAddress('4')}
	manifest := config.PeerManifest{
		SchemaVersion: config.PeerManifestSchemaVersion,
		Kind:          config.PeerManifestKind,
		Notice:        "PUBLIC NETWORK — NO SECRETS",
		ChainID:       51015,
		GenesisValidators: []config.GenesisValidatorConfig{
			{Address: addresses[0], RewardAddress: addresses[0], SecpPublicKey: "02aa", BLSPublicKey: "bb", IsMiner: true},
			{Address: addresses[1], RewardAddress: addresses[1], SecpPublicKey: "02cc", BLSPublicKey: "dd", IsMiner: true},
		},
		BootstrapEndpoints: []config.BootstrapEndpointConfig{
			{Address: addresses[0], Endpoint: "192.0.2.10:6000"},
			{Address: addresses[1], Endpoint: "192.0.2.11:6000"},
		},
	}
	identities := config.OperatorIdentityManifest{
		SchemaVersion:         config.OperatorIdentitySchemaVersion,
		Kind:                  config.OperatorIdentityKind,
		Notice:                "OPERATOR LOCAL — NO PRIVATE KEYS",
		ChainID:               manifest.ChainID,
		GenesisValidatorCount: len(manifest.GenesisValidators),
		Nodes: []config.OperatorNodeIdentity{
			{Slot: 0, Address: addresses[0], Endpoint: "192.0.2.10:6000", Role: config.OperatorIdentityRoleGenesis},
			{Slot: 1, Address: addresses[1], Endpoint: "192.0.2.11:6000", Role: config.OperatorIdentityRoleGenesis},
			{Slot: 2, Address: addresses[2], Endpoint: "192.0.2.12:6000", Role: config.OperatorIdentityRoleLateJoin},
		},
	}
	peersPath := writeExplorerTopologyJSON(t, directory, "peers.json", manifest)
	identitiesPath := writeExplorerTopologyJSON(t, directory, "identities.json", identities)
	return directory, peersPath, identitiesPath
}

func writeExplorerTopologyJSON(t *testing.T, directory, name string, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func explorerTopologyAddress(fill byte) string {
	return "fbc1q" + strings.Repeat(string(fill), 38)
}
