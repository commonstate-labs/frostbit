package asset

import (
	"strings"
	"testing"
)

func TestResolveCanonicalEVMIdentity(t *testing.T) {
	lower, err := Resolve("ETH", "0x1f9840a85d5af5bf1d1762f925bdaddc4201f984")
	if err != nil {
		t.Fatal(err)
	}
	checksummed, err := Resolve("eth", "0x1f9840a85d5aF5bf1D1762F925BDADdC4201F984")
	if err != nil {
		t.Fatal(err)
	}
	if lower != checksummed {
		t.Fatalf("same EVM contract resolved differently: lower=%+v checksum=%+v", lower, checksummed)
	}
	if !strings.HasPrefix(lower.ID, IDPrefix) || len(lower.ID) != len(IDPrefix)+64 {
		t.Fatalf("unexpected full asset ID: %q", lower.ID)
	}
}

func TestResolveNativeAssetIsChainScoped(t *testing.T) {
	btc, err := Resolve("btc", "")
	if err != nil {
		t.Fatal(err)
	}
	eth, err := Resolve("eth", "")
	if err != nil {
		t.Fatal(err)
	}
	if btc.ID == eth.ID {
		t.Fatal("native assets on different chains must have different IDs")
	}
	if _, err := Resolve("btc", "0x1f9840a85d5aF5bf1D1762F925BDADdC4201F984"); err == nil {
		t.Fatal("BTC contract must be rejected")
	}
}

func TestResolveRejectsInvalidContracts(t *testing.T) {
	for _, tc := range []struct {
		chain    string
		contract string
	}{
		{chain: "eth", contract: "0x1234"},
		{chain: "trx", contract: "T-not-base58check"},
		{chain: "unknown", contract: ""},
	} {
		if _, err := Resolve(tc.chain, tc.contract); err == nil {
			t.Fatalf("expected invalid identity to fail: %+v", tc)
		}
	}
}
