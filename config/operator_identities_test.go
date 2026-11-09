package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadOperatorIdentityManifestBindsMixedTopology(t *testing.T) {
	trusted, document := operatorIdentityFixture()
	path := writeOperatorIdentityFixture(t, document)

	got, err := LoadOperatorIdentityManifest(path, trusted)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Nodes) != 3 || got.Nodes[2].Role != OperatorIdentityRoleLateJoin {
		t.Fatalf("unexpected operator identities: %+v", got.Nodes)
	}
}

func TestLoadOperatorIdentityManifestRejectsUntrustedMutations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*PeerManifest, *OperatorIdentityManifest)
	}{
		{name: "chain mismatch", mutate: func(_ *PeerManifest, document *OperatorIdentityManifest) { document.ChainID++ }},
		{name: "noncontiguous slot", mutate: func(_ *PeerManifest, document *OperatorIdentityManifest) { document.Nodes[1].Slot = 9 }},
		{name: "duplicate address", mutate: func(_ *PeerManifest, document *OperatorIdentityManifest) {
			document.Nodes[2].Address = document.Nodes[1].Address
		}},
		{name: "duplicate endpoint", mutate: func(_ *PeerManifest, document *OperatorIdentityManifest) {
			document.Nodes[2].Endpoint = document.Nodes[1].Endpoint
		}},
		{name: "unsafe endpoint host", mutate: func(_ *PeerManifest, document *OperatorIdentityManifest) {
			document.Nodes[2].Endpoint = "user@host:6000"
		}},
		{name: "wrong role", mutate: func(_ *PeerManifest, document *OperatorIdentityManifest) {
			document.Nodes[2].Role = OperatorIdentityRoleGenesis
		}},
		{name: "genesis route mismatch", mutate: func(_ *PeerManifest, document *OperatorIdentityManifest) {
			document.Nodes[0].Endpoint = "192.0.2.90:6000"
		}},
		{name: "latejoin contamination", mutate: func(trusted *PeerManifest, document *OperatorIdentityManifest) {
			trusted.GenesisValidators = append(trusted.GenesisValidators, GenesisValidatorConfig{Address: document.Nodes[2].Address})
			trusted.BootstrapEndpoints = append(trusted.BootstrapEndpoints, BootstrapEndpointConfig{Address: document.Nodes[2].Address, Endpoint: document.Nodes[2].Endpoint})
			document.GenesisValidatorCount++
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			trusted, document := operatorIdentityFixture()
			test.mutate(trusted, document)
			if _, err := LoadOperatorIdentityManifest(writeOperatorIdentityFixture(t, document), trusted); err == nil {
				t.Fatal("mutated operator identity document was accepted")
			}
		})
	}
}

func TestLoadOperatorIdentityManifestRejectsSecretFieldAndTrailingJSON(t *testing.T) {
	trusted, document := operatorIdentityFixture()
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	withSecret := strings.Replace(string(encoded), `"role":"latejoin"`, `"role":"latejoin","private_key":"forbidden"`, 1)
	path := filepath.Join(t.TempDir(), "identities.json")
	if err := os.WriteFile(path, []byte(withSecret), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOperatorIdentityManifest(path, trusted); err == nil {
		t.Fatal("private_key field was accepted")
	}
	if err := os.WriteFile(path, append(encoded, []byte(` {}`)...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOperatorIdentityManifest(path, trusted); err == nil {
		t.Fatal("trailing JSON was accepted")
	}
}

func operatorIdentityFixture() (*PeerManifest, *OperatorIdentityManifest) {
	addresses := []string{operatorTestAddress('2'), operatorTestAddress('3'), operatorTestAddress('4')}
	trusted := &PeerManifest{
		SchemaVersion: PeerManifestSchemaVersion,
		Kind:          PeerManifestKind,
		Notice:        "PUBLIC — NO SECRETS",
		ChainID:       51015,
		GenesisValidators: []GenesisValidatorConfig{
			{Address: addresses[0]},
			{Address: addresses[1]},
		},
		BootstrapEndpoints: []BootstrapEndpointConfig{
			{Address: addresses[0], Endpoint: "192.0.2.10:6000"},
			{Address: addresses[1], Endpoint: "192.0.2.11:6000"},
		},
	}
	document := &OperatorIdentityManifest{
		SchemaVersion:         OperatorIdentitySchemaVersion,
		Kind:                  OperatorIdentityKind,
		Notice:                "OPERATOR LOCAL — NO PRIVATE KEYS",
		ChainID:               trusted.ChainID,
		GenesisValidatorCount: len(trusted.GenesisValidators),
		Nodes: []OperatorNodeIdentity{
			{Slot: 0, Address: addresses[0], Endpoint: "192.0.2.10:6000", Role: OperatorIdentityRoleGenesis},
			{Slot: 1, Address: addresses[1], Endpoint: "192.0.2.11:6000", Role: OperatorIdentityRoleGenesis},
			{Slot: 2, Address: addresses[2], Endpoint: "192.0.2.12:6000", Role: OperatorIdentityRoleLateJoin},
		},
	}
	return trusted, document
}

func operatorTestAddress(fill byte) string {
	return "fbc1q" + strings.Repeat(string(fill), 38)
}

func writeOperatorIdentityFixture(t *testing.T, document *OperatorIdentityManifest) string {
	t.Helper()
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "identities.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
