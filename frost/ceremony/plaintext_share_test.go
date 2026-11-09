package ceremony

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"dex/utils"
)

func TestPlaintextNodeBundleRejectsFormatMixAndTampering(t *testing.T) {
	fixture := newNodeBundleFixtureWithFormat(t, NodeBundlePlaintextGenesis51025)
	out := filepath.Join(t.TempDir(), "plaintext-bundle")
	if err := WriteNodeBundleWithFormat(out, fixture.manifest, fixture.nodeID, fixture.source, NodeBundlePlaintextGenesis51025, utils.VerifyECDSASignature); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyNodeBundleWithFormat(out, fixture.nodeID, nil, NodeBundlePlaintextGenesis51025, utils.VerifyECDSASignature); err != nil {
		t.Fatalf("verify plaintext bundle: %v", err)
	}
	if _, err := VerifyNodeBundle(out, fixture.nodeID, nil, utils.VerifyECDSASignature); err == nil {
		t.Fatal("sealed verifier accepted plaintext shares")
	}
	if _, err := VerifyNodeBundleWithFormat(out, fixture.nodeID, fixture.wrapKey, NodeBundlePlaintextGenesis51025, utils.VerifyECDSASignature); err == nil {
		t.Fatal("plaintext verifier accepted external wrap key")
	}
	name, err := NodeBundleShareFilename(fixture.manifest.Vaults[0].Chain, fixture.manifest.Vaults[0].VaultID)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(out, name)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.WriteFile(path, original, 0o600) }()
	var decoded map[string]any
	if err := json.Unmarshal(original, &decoded); err != nil {
		t.Fatal(err)
	}
	decoded["share"] = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAE="
	tampered, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyNodeBundleWithFormat(out, fixture.nodeID, nil, NodeBundlePlaintextGenesis51025, utils.VerifyECDSASignature); err == nil {
		t.Fatal("plaintext verifier accepted scalar that disagrees with signed verification point")
	}
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	decoded = map[string]any{}
	if err := json.Unmarshal(original, &decoded); err != nil {
		t.Fatal(err)
	}
	decoded["metadata"].(map[string]any)["node_id"] = "different-node"
	tampered, err = json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyNodeBundleWithFormat(out, fixture.nodeID, nil, NodeBundlePlaintextGenesis51025, utils.VerifyECDSASignature); err == nil {
		t.Fatal("plaintext verifier accepted a share assigned to another node")
	}
}

func TestPlaintextBundleOnlyFor51025(t *testing.T) {
	fixture := newNodeBundleFixture(t)
	if err := WriteNodeBundleWithFormat(filepath.Join(t.TempDir(), "bad"), fixture.manifest, fixture.nodeID, fixture.source, NodeBundlePlaintextGenesis51025, utils.VerifyECDSASignature); err == nil {
		t.Fatal("plaintext bundle accepted unrelated DEX chain")
	}
}

func TestPlaintextNodeBundleRejectsOneSealedShare(t *testing.T) {
	fixture := newNodeBundleFixtureWithFormat(t, NodeBundlePlaintextGenesis51025)
	vault := fixture.manifest.Vaults[0]
	name, err := NodeBundleShareFilename(vault.Chain, vault.VaultID)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(fixture.source, name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := nodeBundleExpectedShareMetadata(fixture.manifest, fixture.nodeID, vault)
	if err != nil {
		t.Fatal(err)
	}
	share, err := OpenPlaintextGenesisShare(data, metadata)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := SealShare(metadata, share, fixture.wrapKey)
	clear(share)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, sealed, 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "mixed-bundle")
	if err := WriteNodeBundleWithFormat(out, fixture.manifest, fixture.nodeID, fixture.source, NodeBundlePlaintextGenesis51025, utils.VerifyECDSASignature); err == nil {
		t.Fatal("plaintext bundle accepted mixed sealed and plaintext shares")
	}
	if _, err := os.Lstat(out); !os.IsNotExist(err) {
		t.Fatalf("mixed input created an output bundle: %v", err)
	}
}
