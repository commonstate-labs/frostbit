package ceremony

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"dex/utils"
)

type nodeBundleFixture struct {
	manifest *PublicManifest
	nodeID   string
	source   string
	wrapKey  []byte
}

func newNodeBundleFixture(t *testing.T) *nodeBundleFixture {
	return newNodeBundleFixtureWithFormat(t, NodeBundleSealed)
}

func newNodeBundleFixtureWithFormat(t *testing.T, format NodeBundleShareFormat) *nodeBundleFixture {
	t.Helper()
	plan, keys := testPlan(t)
	if format == NodeBundlePlaintextGenesis51025 {
		plan.NetworkChainID = PlaintextGenesisChainID
		signTestPlanRegistrations(t, plan, keys)
	}
	packages := packagesFor(t, plan)
	result, err := Finalize(plan, "alice", keys.Encryption["alice"], clone(t, packages))
	if err != nil {
		t.Fatal(err)
	}
	attestations := make([]*PublicAttestation, 0, len(plan.Participants))
	for _, participant := range plan.Participants {
		localResult := result
		if participant.ID != "alice" {
			localResult, err = Finalize(plan, participant.ID, keys.Encryption[participant.ID], clone(t, packages))
			if err != nil {
				t.Fatal(err)
			}
		}
		attestation, err := BuildAttestation(plan, localResult)
		if err != nil {
			t.Fatal(err)
		}
		signTestAttestation(t, plan, attestation, keys.Identity[participant.ID])
		attestations = append(attestations, attestation)
		if participant.ID != "alice" {
			for i := range localResult.Vaults {
				clear(localResult.Vaults[i].LocalShare)
			}
		}
	}
	manifest, err := Assemble(plan, attestations, utils.VerifyECDSASignature)
	if err != nil {
		t.Fatal(err)
	}
	wrapKey := bytes.Repeat([]byte{0x71}, 32)
	source := filepath.Join(t.TempDir(), "sealed")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(source, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, local := range result.Vaults {
		var public *PublicVaultResult
		for i := range manifest.Vaults {
			if manifest.Vaults[i].Chain == local.Chain && manifest.Vaults[i].VaultID == local.VaultID {
				public = &manifest.Vaults[i]
				break
			}
		}
		if public == nil {
			t.Fatal("local result missing from manifest")
		}
		verificationShare, err := manifestParticipantVerificationShare(manifest, "alice", public.Chain, public.VaultID)
		if err != nil {
			t.Fatal(err)
		}
		metadata := ShareMetadata{
			CeremonyDigest: hex.EncodeToString(manifest.TranscriptDigest), NodeID: "alice",
			Chain: public.Chain, VaultID: public.VaultID, Epoch: public.Epoch, SignAlgo: public.SignAlgo,
			CommitteeDigest: hex.EncodeToString(public.CommitteeDigest), GroupPubkey: public.RawGroupPubkey,
			VerificationShare: verificationShare,
		}
		var sealed []byte
		if format == NodeBundlePlaintextGenesis51025 {
			sealed, err = EncodePlaintextGenesisShare(metadata, local.LocalShare)
		} else {
			sealed, err = SealShare(metadata, local.LocalShare, wrapKey)
		}
		clear(verificationShare)
		clear(local.LocalShare)
		if err != nil {
			t.Fatal(err)
		}
		name, err := NodeBundleShareFilename(public.Chain, public.VaultID)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(source, name)
		if err := os.WriteFile(path, sealed, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatal(err)
		}
		clear(sealed)
	}
	t.Cleanup(func() { clear(wrapKey) })
	return &nodeBundleFixture{manifest: manifest, nodeID: "alice", source: source, wrapKey: wrapKey}
}

func TestWriteNodeBundleProducesExactImporterContract(t *testing.T) {
	fixture := newNodeBundleFixture(t)
	out := filepath.Join(t.TempDir(), "alice-bundle")
	if err := WriteNodeBundle(out, fixture.manifest, fixture.nodeID, fixture.source, utils.VerifyECDSASignature); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	gotNames := make([]string, 0, len(entries))
	for _, entry := range entries {
		gotNames = append(gotNames, entry.Name())
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			t.Fatalf("invalid bundle entry %q", entry.Name())
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
			t.Fatalf("bundle file %q mode=%#o want 0600", entry.Name(), info.Mode().Perm())
		}
	}
	sort.Strings(gotNames)
	wantNames := []string{NodeBundleManifestFilename}
	for _, vault := range fixture.manifest.Vaults {
		name, _ := NodeBundleShareFilename(vault.Chain, vault.VaultID)
		wantNames = append(wantNames, name)
	}
	sort.Strings(wantNames)
	if len(gotNames) != len(wantNames) {
		t.Fatalf("bundle entries=%v want=%v", gotNames, wantNames)
	}
	for i := range gotNames {
		if gotNames[i] != wantNames[i] {
			t.Fatalf("bundle entries=%v want=%v", gotNames, wantNames)
		}
	}
	dirInfo, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("bundle directory mode=%#o want 0700", dirInfo.Mode().Perm())
	}

	manifestData, err := os.ReadFile(filepath.Join(out, NodeBundleManifestFilename))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := StrictDecodeManifest(manifestData)
	if err != nil || ValidateManifest(decoded, utils.VerifyECDSASignature) != nil {
		t.Fatalf("bundle manifest is not independently valid: %v", err)
	}
	for _, name := range wantNames {
		if name == NodeBundleManifestFilename {
			continue
		}
		source, err := os.ReadFile(filepath.Join(fixture.source, name))
		if err != nil {
			t.Fatal(err)
		}
		copied, err := os.ReadFile(filepath.Join(out, name))
		if err != nil || !bytes.Equal(source, copied) {
			t.Fatalf("sealed share %s was not copied byte-for-byte", name)
		}
	}

	before, _ := os.ReadFile(filepath.Join(out, NodeBundleManifestFilename))
	if err := WriteNodeBundle(out, fixture.manifest, fixture.nodeID, fixture.source, utils.VerifyECDSASignature); err == nil {
		t.Fatal("existing bundle directory must never be overwritten")
	}
	after, _ := os.ReadFile(filepath.Join(out, NodeBundleManifestFilename))
	if !bytes.Equal(before, after) {
		t.Fatal("exclusive-create rejection changed an existing bundle")
	}
}

func TestVerifyNodeBundleRequiresExactNodeAndCompleteDecryptableShares(t *testing.T) {
	fixture := newNodeBundleFixture(t)
	out := filepath.Join(t.TempDir(), "alice-bundle")
	if err := WriteNodeBundle(out, fixture.manifest, fixture.nodeID, fixture.source, utils.VerifyECDSASignature); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyNodeBundle(out, fixture.nodeID, nil, utils.VerifyECDSASignature); err != nil {
		t.Fatalf("public bundle verification: %v", err)
	}
	if _, err := VerifyNodeBundle(out, fixture.nodeID, fixture.wrapKey, utils.VerifyECDSASignature); err != nil {
		t.Fatalf("full bundle verification: %v", err)
	}
	if _, err := VerifyNodeBundle(out, "bob", nil, utils.VerifyECDSASignature); err == nil {
		t.Fatal("bundle bound to alice was accepted for bob")
	}
	wrongKey := bytes.Repeat([]byte{0x72}, 32)
	if _, err := VerifyNodeBundle(out, fixture.nodeID, wrongKey, utils.VerifyECDSASignature); err == nil {
		t.Fatal("bundle opened with the wrong wrap key")
	}
	clear(wrongKey)
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	var sharePath string
	for _, entry := range entries {
		if entry.Name() != NodeBundleManifestFilename {
			sharePath = filepath.Join(out, entry.Name())
			break
		}
	}
	originalShare, err := os.ReadFile(sharePath)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := decodeSealedShare(originalShare)
	if err != nil {
		t.Fatal(err)
	}
	wrongScalar := make([]byte, shareScalarSize)
	wrongScalar[len(wrongScalar)-1] = 1
	if VerifyLocalShareAgainstPoint(envelope.Metadata.Chain, envelope.Metadata.SignAlgo, wrongScalar, envelope.Metadata.VerificationShare) == nil {
		wrongScalar[len(wrongScalar)-1] = 2
	}
	aad, err := shareMetadataAAD(envelope.Metadata)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := newSealedShareAEAD(fixture.wrapKey)
	if err != nil {
		t.Fatal(err)
	}
	envelope.Ciphertext = gcm.Seal(nil, envelope.Nonce, wrongScalar, aad)
	forgedShare, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sharePath, forgedShare, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyNodeBundle(out, fixture.nodeID, nil, utils.VerifyECDSASignature); err != nil {
		t.Fatalf("public verifier should leave ciphertext authentication to the wrap-key check: %v", err)
	}
	if _, err := VerifyNodeBundle(out, fixture.nodeID, fixture.wrapKey, utils.VerifyECDSASignature); err == nil {
		t.Fatal("bundle containing a decryptable share with the wrong g^s was accepted")
	}
	if err := os.WriteFile(sharePath, originalShare, 0o600); err != nil {
		t.Fatal(err)
	}
	clear(wrongScalar)
	clear(aad)
	clear(forgedShare)
	clear(originalShare)
	extraPath := filepath.Join(out, "unexpected.share.json")
	if err := os.WriteFile(extraPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyNodeBundle(out, fixture.nodeID, nil, utils.VerifyECDSASignature); err == nil {
		t.Fatal("bundle with an extra artifact was accepted")
	}
	if err := os.Remove(extraPath); err != nil {
		t.Fatal(err)
	}
	entries, err = os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() == NodeBundleManifestFilename {
			continue
		}
		if err := os.Remove(filepath.Join(out, entry.Name())); err != nil {
			t.Fatal(err)
		}
		break
	}
	if _, err := VerifyNodeBundle(out, fixture.nodeID, nil, utils.VerifyECDSASignature); err == nil {
		t.Fatal("bundle with a missing share was accepted")
	}
}

func TestWriteNodeBundleRejectsWrongOrIncompleteShareSetBeforeCreatingOutput(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		fixture := newNodeBundleFixture(t)
		entries, _ := os.ReadDir(fixture.source)
		if err := os.Remove(filepath.Join(fixture.source, entries[0].Name())); err != nil {
			t.Fatal(err)
		}
		assertNodeBundleRejectedWithoutOutput(t, fixture)
	})

	t.Run("extra", func(t *testing.T) {
		fixture := newNodeBundleFixture(t)
		if err := os.WriteFile(filepath.Join(fixture.source, "extra.share.json"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		assertNodeBundleRejectedWithoutOutput(t, fixture)
	})

	t.Run("metadata", func(t *testing.T) {
		fixture := newNodeBundleFixture(t)
		entries, _ := os.ReadDir(fixture.source)
		path := filepath.Join(fixture.source, entries[0].Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var envelope sealedShareEnvelope
		if err := json.Unmarshal(data, &envelope); err != nil {
			t.Fatal(err)
		}
		envelope.Metadata.NodeID = "bob"
		data, err = json.Marshal(&envelope)
		if err != nil || os.WriteFile(path, data, 0o600) != nil {
			t.Fatal("rewrite mismatched sealed metadata", err)
		}
		assertNodeBundleRejectedWithoutOutput(t, fixture)
	})

	t.Run("tampered manifest", func(t *testing.T) {
		fixture := newNodeBundleFixture(t)
		fixture.manifest.Attestations[0].Signature[0] ^= 1
		assertNodeBundleRejectedWithoutOutput(t, fixture)
	})

	t.Run("unknown participant", func(t *testing.T) {
		fixture := newNodeBundleFixture(t)
		fixture.nodeID = "mallory"
		assertNodeBundleRejectedWithoutOutput(t, fixture)
	})

	if runtime.GOOS != "windows" {
		t.Run("insecure source permissions", func(t *testing.T) {
			fixture := newNodeBundleFixture(t)
			if err := os.Chmod(fixture.source, 0o770); err != nil {
				t.Fatal(err)
			}
			assertNodeBundleRejectedWithoutOutput(t, fixture)
		})

		t.Run("symlink share", func(t *testing.T) {
			fixture := newNodeBundleFixture(t)
			entries, _ := os.ReadDir(fixture.source)
			path := filepath.Join(fixture.source, entries[0].Name())
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(t.TempDir(), "target.share.json")
			if err := os.WriteFile(target, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
			assertNodeBundleRejectedWithoutOutput(t, fixture)
		})
	}
}

func assertNodeBundleRejectedWithoutOutput(t *testing.T, fixture *nodeBundleFixture) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "must-not-exist")
	if err := WriteNodeBundle(out, fixture.manifest, fixture.nodeID, fixture.source, utils.VerifyECDSASignature); err == nil {
		t.Fatal("invalid node bundle input was accepted")
	}
	if _, err := os.Lstat(out); !os.IsNotExist(err) {
		t.Fatal("invalid input created a partial node bundle")
	}
}
