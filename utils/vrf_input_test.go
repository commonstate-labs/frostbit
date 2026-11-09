package utils

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/hex"
	"math"
	"strings"
	"testing"

	"dex/pb"
	"dex/types"
)

func TestVRFInputCanonicalVector(t *testing.T) {
	digest, err := constructVRFInputForChain(
		42,
		7,
		3,
		"block-6-parent",
		types.NodeID("fbc1qvrfvector"),
	)
	if err != nil {
		t.Fatal(err)
	}
	const expected = "b28495ad22d02f0977afe20447b98834fdf9d6f90e8b8ef06f2a1b8b42f27899"
	if got := hex.EncodeToString(digest); got != expected {
		t.Fatalf("VRF input digest=%s want=%s", got, expected)
	}
}

func TestVRFInputSeparatesChainAndVariableFieldBoundaries(t *testing.T) {
	base, err := constructVRFInputForChain(1, 9, 2, "ab", types.NodeID("c"))
	if err != nil {
		t.Fatal(err)
	}
	otherChain, err := constructVRFInputForChain(2, 9, 2, "ab", types.NodeID("c"))
	if err != nil {
		t.Fatal(err)
	}
	if string(base) == string(otherChain) {
		t.Fatal("VRF input is not bound to chain ID")
	}

	                                                                      
	otherBoundary, err := constructVRFInputForChain(1, 9, 2, "a", types.NodeID("bc"))
	if err != nil {
		t.Fatal(err)
	}
	if string(base) == string(otherBoundary) {
		t.Fatal("length-prefixed VRF fields still collide across boundaries")
	}
}

func TestVRFProofCannotVerifyOnAnotherChain(t *testing.T) {
	previousChainID := pb.GetDefaultChainID()
	t.Cleanup(func() { pb.SetDefaultChainID(previousChainID) })

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := GetPublicKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	provider := NewVRFProvider()
	const sourceChainID = uint64(1001)
	pb.SetDefaultChainID(sourceChainID)
	output, proof, err := provider.GenerateVRF(privateKey, 11, 1, "parent-10", types.NodeID("miner-chain-bound"))
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.VerifyVRFWithPublicKey(publicKey, 11, 1, "parent-10", types.NodeID("miner-chain-bound"), proof, output); err != nil {
		t.Fatalf("source-chain proof rejected: %v", err)
	}

	pb.SetDefaultChainID(sourceChainID + 1)
	if err := provider.VerifyVRFWithPublicKey(publicKey, 11, 1, "parent-10", types.NodeID("miner-chain-bound"), proof, output); err == nil {
		t.Fatal("source-chain VRF proof verified on another chain")
	}
}

func TestVRFInputRejectsNonCanonicalWindow(t *testing.T) {
	provider := NewVRFProvider()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := provider.GenerateVRF(privateKey, 1, -1, "parent", types.NodeID("miner")); err == nil || !strings.Contains(err.Error(), "invalid VRF window") {
		t.Fatalf("negative window error=%v", err)
	}
	if uint64(^uint(0)) > math.MaxUint32 {
		tooLarge := int(uint64(math.MaxUint32) + 1)
		if _, _, err := provider.GenerateVRF(privateKey, 1, tooLarge, "parent", types.NodeID("miner")); err == nil || !strings.Contains(err.Error(), "invalid VRF window") {
			t.Fatalf("oversized window error=%v", err)
		}
	}
}
