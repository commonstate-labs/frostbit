package ceremony

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	"dex/utils"

	"github.com/btcsuite/btcd/btcec/v2"
)

func signedTestAttestations(t *testing.T, plan *Plan, keys *ceremonyTestKeys) []*PublicAttestation {
	t.Helper()
	packages := packagesFor(t, plan)
	attestations := make([]*PublicAttestation, 0, len(plan.Participants))
	for _, participant := range plan.Participants {
		result, err := Finalize(plan, participant.ID, keys.Encryption[participant.ID], clone(t, packages))
		if err != nil {
			t.Fatal(err)
		}
		attestation, err := BuildAttestation(plan, result)
		for i := range result.Vaults {
			clear(result.Vaults[i].LocalShare)
		}
		if err != nil {
			t.Fatal(err)
		}
		signTestAttestation(t, plan, attestation, keys.Identity[participant.ID])
		attestations = append(attestations, attestation)
	}
	return attestations
}

func signTestAttestation(t *testing.T, plan *Plan, attestation *PublicAttestation, key []byte) {
	t.Helper()
	digest, err := AttestationDigest(plan, attestation)
	if err != nil {
		t.Fatal(err)
	}
	attestation.Signature = signTestAttestationDigest(t, key, digest)
}

func signTestAttestationDigest(t *testing.T, key, digest []byte) []byte {
	t.Helper()
	privateKey, _ := btcec.PrivKeyFromBytes(key)
	defer privateKey.Zero()
	r, s, err := ecdsa.Sign(rand.Reader, privateKey.ToECDSA(), digest)
	if err != nil {
		t.Fatal(err)
	}
	if s.Cmp(new(big.Int).Rsh(new(big.Int).Set(btcec.S256().Params().N), 1)) > 0 {
		s.Sub(btcec.S256().Params().N, s)
	}
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])
	return signature
}

func TestPublicManifestRoundTripAndTamperRejection(t *testing.T) {
	plan, keys := testPlan(t)
	attestations := signedTestAttestations(t, plan, keys)
	manifest, err := Assemble(plan, attestations, utils.VerifyECDSASignature)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateManifest(manifest, utils.VerifyECDSASignature); err != nil {
		t.Fatal(err)
	}
	manifestDigest, err := ManifestDigest(manifest, utils.VerifyECDSASignature)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Vaults) != len(plan.Vaults) || len(manifest.Attestations) != len(plan.Participants) {
		t.Fatal("assembled manifest is incomplete")
	}
	for i, vault := range manifest.Vaults {
		expected, err := CommitteeDigest(vault.Chain, vault.VaultID, vault.Epoch, vault.SignAlgo, vault.Committee)
		if err != nil || !bytes.Equal(vault.CommitteeDigest, expected) || vault.SignerIndex != 0 || len(vault.VerificationShare) != 0 ||
			len(vault.AggregateCommitments) != int(plan.Threshold) || !bytes.Equal(vault.RawGroupPubkey, vault.AggregateCommitments[0]) {
			t.Fatalf("invalid assembled vault %d: %v", i, err)
		}
	}

	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := StrictDecodeManifest(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateManifest(decoded, utils.VerifyECDSASignature); err != nil {
		t.Fatal(err)
	}
	roundTripDigest, err := ManifestDigest(decoded, utils.VerifyECDSASignature)
	if err != nil || !bytes.Equal(manifestDigest, roundTripDigest) {
		t.Fatal("manifest JSON roundtrip changed its digest", err)
	}

	t.Run("signature", func(t *testing.T) {
		copy := clone(t, manifest)
		copy.Attestations[0].Signature[0] ^= 1
		if err := ValidateManifest(copy, utils.VerifyECDSASignature); err == nil {
			t.Fatal("tampered signature accepted")
		}
	})
	t.Run("assembled group key", func(t *testing.T) {
		copy := clone(t, manifest)
		copy.Vaults[0].RawGroupPubkey = bytes.Clone(copy.Vaults[1].RawGroupPubkey)
		if err := ValidateManifest(copy, utils.VerifyECDSASignature); err == nil {
			t.Fatal("substituted assembled group key accepted")
		}
	})
	t.Run("transcript split", func(t *testing.T) {
		copy := clone(t, attestations)
		copy[0].TranscriptDigest[0] ^= 1
		copy[0].Signature = nil
		signTestAttestation(t, plan, copy[0], keys.Identity[copy[0].ParticipantID])
		if _, err := Assemble(plan, copy, utils.VerifyECDSASignature); err == nil {
			t.Fatal("different participant transcript accepted")
		}
	})
	t.Run("missing and duplicate", func(t *testing.T) {
		if _, err := Assemble(plan, attestations[:len(attestations)-1], utils.VerifyECDSASignature); err == nil {
			t.Fatal("missing attestation accepted")
		}
		copy := clone(t, attestations)
		copy[1] = clone(t, copy[0])
		if _, err := Assemble(plan, copy, utils.VerifyECDSASignature); err == nil {
			t.Fatal("duplicate attestation accepted")
		}
	})
}

func TestManifestRejectsSignedForgedVerificationShareAndEquivocatedPolynomial(t *testing.T) {
	plan, keys := testPlan(t)
	attestations := signedTestAttestations(t, plan, keys)

	t.Run("arbitrary signed verification share", func(t *testing.T) {
		forged := clone(t, attestations)
		attestation := forged[0]
		point, err := LocalShareVerificationPoint(
			attestation.Vaults[0].Chain, attestation.Vaults[0].SignAlgo, []byte{42},
		)
		if err != nil {
			t.Fatal(err)
		}
		attestation.Vaults[0].VerificationShare = point
		attestation.Signature = nil
		unsigned := cloneAttestation(*attestation)
		digest, err := canonicalDigest("DEX_CEREMONY_ATTESTATION_V1", &unsigned)
		if err != nil {
			t.Fatal(err)
		}
		attestation.Signature = signTestAttestationDigest(t, keys.Identity[attestation.ParticipantID], digest)
		if !utils.VerifyECDSASignature(plan.Participants[0].PublicKey, digest, attestation.Signature) {
			t.Fatal("malicious fixture signature is invalid")
		}
		if err := ValidateAttestation(plan, attestation, utils.VerifyECDSASignature); err == nil {
			t.Fatal("participant signature authenticated a verification share that is not derived from the public polynomial")
		}
	})

	t.Run("one-member polynomial equivocation", func(t *testing.T) {
		forged := clone(t, attestations)
		attestation := forged[0]
		vault := &attestation.Vaults[0]
		group, err := vaultGroup(plan.Vaults[0])
		if err != nil {
			t.Fatal(err)
		}
		vault.AggregateCommitments[1] = group.SerializePoint(group.ScalarBaseMult(big.NewInt(2)))
		verification, err := verificationShareFromCommitments(group, vault.AggregateCommitments, vault.SignerIndex)
		if err != nil {
			t.Fatal(err)
		}
		vault.VerificationShare = verification
		attestation.Signature = nil
		signTestAttestation(t, plan, attestation, keys.Identity[attestation.ParticipantID])
		if _, err := Assemble(plan, forged, utils.VerifyECDSASignature); err == nil {
			t.Fatal("committee member's alternate signed aggregate polynomial was accepted")
		}
	})
}

func TestBuildAttestationContainsNoSecretAndBindsFields(t *testing.T) {
	plan, keys := testPlan(t)
	packages := packagesFor(t, plan)
	result, err := Finalize(plan, "alice", keys.Encryption["alice"], packages)
	if err != nil {
		t.Fatal(err)
	}
	attestation, err := BuildAttestation(plan, result)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(attestation)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("local_share")) {
		t.Fatal("public attestation exposed a local share field")
	}
	expectedVerification, err := LocalShareVerificationPoint(
		result.Vaults[0].Chain, result.Vaults[0].SignAlgo, result.Vaults[0].LocalShare,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(attestation.Vaults[0].VerificationShare, expectedVerification) {
		t.Fatal("attestation did not bind the local share verification point")
	}
	clear(expectedVerification)
	digest, err := AttestationDigest(plan, attestation)
	if err != nil {
		t.Fatal(err)
	}
	attestation.Signature = make([]byte, 64)
	again, err := AttestationDigest(plan, attestation)
	if err != nil || !bytes.Equal(digest, again) {
		t.Fatal("signature changed attestation digest", err)
	}
	otherVerification, err := LocalShareVerificationPoint(
		result.Vaults[0].Chain, result.Vaults[0].SignAlgo, []byte{42},
	)
	if err != nil {
		t.Fatal(err)
	}
	attestation.Vaults[0].VerificationShare = otherVerification
	if _, err := AttestationDigest(plan, attestation); err == nil {
		t.Fatal("verification share detached from aggregate public commitments")
	}
	attestation.Vaults[0].SignerIndex++
	if _, err := AttestationDigest(plan, attestation); err == nil {
		t.Fatal("mismatched signer index accepted")
	}
	for i := range result.Vaults {
		clear(result.Vaults[i].LocalShare)
	}
}

func TestCommitteeDigestBindsOrderAndParameters(t *testing.T) {
	plan, _ := testPlan(t)
	vault := plan.Vaults[0]
	original, err := CommitteeDigest(vault.Chain, vault.VaultID, plan.Epoch, vault.SignAlgo, vault.Committee)
	if err != nil {
		t.Fatal(err)
	}
	reordered := append([]string(nil), vault.Committee...)
	reordered[0], reordered[1] = reordered[1], reordered[0]
	other, err := CommitteeDigest(vault.Chain, vault.VaultID, plan.Epoch, vault.SignAlgo, reordered)
	if err != nil || bytes.Equal(original, other) {
		t.Fatal("committee order was not bound", err)
	}
	other, err = CommitteeDigest(vault.Chain, vault.VaultID+1, plan.Epoch, vault.SignAlgo, vault.Committee)
	if err != nil || bytes.Equal(original, other) {
		t.Fatal("vault identity was not bound", err)
	}
	if _, err := CommitteeDigest(vault.Chain, vault.VaultID, 0, vault.SignAlgo, vault.Committee); err == nil {
		t.Fatal("zero epoch accepted")
	}
}

func TestStrictArtifactDecoders(t *testing.T) {
	plan, keys := testPlan(t)
	attestations := signedTestAttestations(t, plan, keys)
	manifest, err := Assemble(plan, attestations, utils.VerifyECDSASignature)
	if err != nil {
		t.Fatal(err)
	}
	planJSON, _ := json.Marshal(plan)
	attestationJSON, _ := json.Marshal(attestations[0])
	manifestJSON, _ := json.Marshal(manifest)
	packageJSON, _ := json.Marshal(packagesFor(t, plan)[0])
	if _, err := StrictDecodePlan(planJSON); err != nil {
		t.Fatal(err)
	}
	if _, err := StrictDecodeAttestation(attestationJSON); err != nil {
		t.Fatal(err)
	}
	if _, err := StrictDecodeManifest(manifestJSON); err != nil {
		t.Fatal(err)
	}
	if _, err := StrictDecodeRound1Package(packageJSON); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		data []byte
		read func([]byte) error
	}{
		{"duplicate", []byte(strings.Replace(string(planJSON), `"version":1`, `"version":1,"version":1`, 1)), func(data []byte) error { _, err := StrictDecodePlan(data); return err }},
		{"case-aliased field", []byte(strings.Replace(string(planJSON), `"version":1`, `"Version":1`, 1)), func(data []byte) error { _, err := StrictDecodePlan(data); return err }},
		{"nested unknown", []byte(strings.Replace(string(manifestJSON), `"id":`, `"unknown":1,"id":`, 1)), func(data []byte) error { _, err := StrictDecodeManifest(data); return err }},
		{"trailing", append(bytes.Clone(attestationJSON), []byte(" true")...), func(data []byte) error { _, err := StrictDecodeAttestation(data); return err }},
		{"duplicate package", []byte(strings.Replace(string(packageJSON), `"dealer_id":`, `"dealer_id":"other","dealer_id":`, 1)), func(data []byte) error { _, err := StrictDecodeRound1Package(data); return err }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.read(test.data); err == nil {
				t.Fatal("noncanonical JSON accepted")
			}
		})
	}
}

func TestSignatureVerifierRejectsMalformedScalarParts(t *testing.T) {
	plan, keys := testPlan(t)
	attestation := signedTestAttestations(t, plan, keys)[0]
	copy(attestation.Signature[:32], new(big.Int).Set(btcec.S256().Params().N).FillBytes(make([]byte, 32)))
	if err := ValidateAttestation(plan, attestation, utils.VerifyECDSASignature); err == nil {
		t.Fatal("out-of-range ECDSA scalar accepted")
	}
}

func TestCeremonyRejectsMalleatedHighSSignature(t *testing.T) {
	plan, keys := testPlan(t)
	attestation := signedTestAttestations(t, plan, keys)[0]
	s := new(big.Int).SetBytes(attestation.Signature[32:])
	s.Sub(btcec.S256().Params().N, s)
	s.FillBytes(attestation.Signature[32:])
	if utils.VerifyECDSASignature(plan.Participants[0].PublicKey, mustAttestationDigest(t, plan, attestation), attestation.Signature) == false {
		t.Fatal("test fixture is not an otherwise-valid ECDSA malleation")
	}
	if err := ValidateAttestation(plan, attestation, utils.VerifyECDSASignature); err == nil {
		t.Fatal("high-S ECDSA attestation accepted")
	}
}

func mustAttestationDigest(t *testing.T, plan *Plan, attestation *PublicAttestation) []byte {
	t.Helper()
	digest, err := AttestationDigest(plan, attestation)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}
