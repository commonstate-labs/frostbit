package utils

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"dex/types"
	"encoding/binary"
	"math/big"
	"strings"
	"testing"

	"go.dedis.ch/kyber/v3"
	"go.dedis.ch/kyber/v3/pairing/bn256"
)

type vrfEncodingFixture struct {
	provider        *VRFProvider
	publicKey       kyber.Point
	height          uint64
	window          int
	parentBlockHash string
	nodeID          types.NodeID
	proof           []byte
	output          []byte
}

func newVRFEncodingFixture(t *testing.T) vrfEncodingFixture {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := GetPublicKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	fixture := vrfEncodingFixture{
		provider:        NewVRFProvider(),
		publicKey:       publicKey,
		height:          42,
		window:          1,
		parentBlockHash: "parent-41",
		nodeID:          types.NodeID("miner-a"),
	}
	fixture.output, fixture.proof, err = fixture.provider.GenerateVRF(
		privateKey,
		fixture.height,
		fixture.window,
		fixture.parentBlockHash,
		fixture.nodeID,
	)
	if err != nil {
		t.Fatal(err)
	}
	return fixture
}

func (f vrfEncodingFixture) verify(proof, output []byte) error {
	return f.provider.VerifyVRFWithPublicKey(
		f.publicKey,
		f.height,
		f.window,
		f.parentBlockHash,
		f.nodeID,
		proof,
		output,
	)
}

func TestVRFEncodingSizesMatchBN256AndSHA256(t *testing.T) {
	if got := bn256.NewSuite().G1().Point().MarshalSize(); got != VRFProofSize {
		t.Fatalf("BN256 G1 marshal size = %d, VRFProofSize = %d", got, VRFProofSize)
	}
	if VRFOutputSize != sha256.Size {
		t.Fatalf("VRFOutputSize = %d, sha256.Size = %d", VRFOutputSize, sha256.Size)
	}

	fixture := newVRFEncodingFixture(t)
	if got := len(fixture.proof); got != VRFProofSize {
		t.Fatalf("generated proof length = %d, want %d", got, VRFProofSize)
	}
	if got := len(fixture.output); got != VRFOutputSize {
		t.Fatalf("generated output length = %d, want %d", got, VRFOutputSize)
	}
	if err := fixture.verify(fixture.proof, fixture.output); err != nil {
		t.Fatalf("canonical generated VRF was rejected: %v", err)
	}
}

func TestVRFVerificationRejectsSuffixGrinding(t *testing.T) {
	fixture := newVRFEncodingFixture(t)

	                                                                          
	                                                                 
	for nonce := uint64(0); nonce < 1024; nonce++ {
		var suffix [8]byte
		binary.BigEndian.PutUint64(suffix[:], nonce)
		mutatedProof := append(append([]byte(nil), fixture.proof...), suffix[:]...)
		mutatedOutput := sha256.Sum256(mutatedProof)

		err := fixture.verify(mutatedProof, mutatedOutput[:])
		if err == nil {
			t.Fatalf("accepted proof with suffix nonce %d", nonce)
		}
		if !strings.Contains(err.Error(), "invalid VRF proof length") {
			t.Fatalf("suffix nonce %d returned unexpected error: %v", nonce, err)
		}
	}
}

func TestVRFVerificationRejectsInvalidProofLengths(t *testing.T) {
	fixture := newVRFEncodingFixture(t)
	tests := []struct {
		name  string
		proof []byte
	}{
		{name: "empty", proof: nil},
		{name: "one_byte_short", proof: append([]byte(nil), fixture.proof[:VRFProofSize-1]...)},
		{name: "one_byte_long", proof: append(append([]byte(nil), fixture.proof...), 0)},
		{name: "double_length", proof: append(append([]byte(nil), fixture.proof...), fixture.proof...)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := sha256.Sum256(test.proof)
			err := fixture.verify(test.proof, output[:])
			if err == nil || !strings.Contains(err.Error(), "invalid VRF proof length") {
				t.Fatalf("proof length %d error = %v", len(test.proof), err)
			}
		})
	}
}

func TestVRFVerificationRejectsInvalidOutput(t *testing.T) {
	fixture := newVRFEncodingFixture(t)
	tests := []struct {
		name   string
		output []byte
	}{
		{name: "empty", output: nil},
		{name: "one_byte_short", output: append([]byte(nil), fixture.output[:VRFOutputSize-1]...)},
		{name: "one_byte_long", output: append(append([]byte(nil), fixture.output...), 0)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := fixture.verify(fixture.proof, test.output)
			if err == nil || !strings.Contains(err.Error(), "invalid VRF output length") {
				t.Fatalf("output length %d error = %v", len(test.output), err)
			}
		})
	}

	tampered := append([]byte(nil), fixture.output...)
	tampered[0] ^= 0x80
	if err := fixture.verify(fixture.proof, tampered); err == nil || !strings.Contains(err.Error(), "VRF output mismatch") {
		t.Fatalf("same-length tampered output error = %v", err)
	}
}

func TestVRFProofEncodingMustBeCanonical(t *testing.T) {
	suite := bn256.NewSuite()
	base := suite.G1().Point().Base()
	canonical, err := base.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	nonCanonical := equivalentNonCanonicalBN256Encoding(t, canonical)
	if len(nonCanonical) != VRFProofSize {
		t.Fatalf("non-canonical fixture length = %d, want %d", len(nonCanonical), VRFProofSize)
	}

	decoded := suite.G1().Point()
	if err := decoded.UnmarshalBinary(nonCanonical); err != nil {
		t.Fatalf("Kyber did not accept equivalent non-canonical fixture: %v", err)
	}
	if !decoded.Equal(base) {
		t.Fatal("non-canonical fixture did not decode to the original G1 point")
	}

	err = validateVRFProofEncoding(suite, nonCanonical)
	if err == nil || !strings.Contains(err.Error(), "non-canonical") {
		t.Fatalf("equivalent non-canonical proof error = %v", err)
	}
}

func TestVRFProofEncodingRejectsIdentityPoint(t *testing.T) {
	suite := bn256.NewSuite()
	identity, err := suite.G1().Point().Null().MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if err := validateVRFProofEncoding(suite, identity); err == nil || !strings.Contains(err.Error(), "identity point") {
		t.Fatalf("identity proof error = %v", err)
	}
}

func equivalentNonCanonicalBN256Encoding(t *testing.T, canonical []byte) []byte {
	t.Helper()
	if len(canonical) == 0 || len(canonical)%32 != 0 {
		t.Fatalf("invalid canonical BN256 encoding length %d", len(canonical))
	}
	fieldModulus, ok := new(big.Int).SetString(
		"65000549695646603732796438742359905742825358107623003571877145026864184071783",
		10,
	)
	if !ok {
		t.Fatal("parse BN256 field modulus")
	}
	encodingLimit := new(big.Int).Lsh(big.NewInt(1), 256)

	for offset := 0; offset < len(canonical); offset += 32 {
		coordinate := new(big.Int).SetBytes(canonical[offset : offset+32])
		candidate := new(big.Int).Add(coordinate, fieldModulus)
		if candidate.Cmp(encodingLimit) >= 0 {
			continue
		}
		result := append([]byte(nil), canonical...)
		candidate.FillBytes(result[offset : offset+32])
		if !bytes.Equal(result, canonical) {
			return result
		}
	}

	t.Fatal("could not construct an equivalent non-canonical G1 encoding")
	return nil
}
