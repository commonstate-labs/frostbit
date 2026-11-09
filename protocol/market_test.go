package protocol

import "testing"

func TestMarketIdentityRoundTripWithUnderscoreAssetIDs(t *testing.T) {
	base := "FB"
	quote := "br_718d932d6adfb5e61bfbbbb7b266e13884b6604908067bbe8d5bad1a7159159c"
	pair := base + "_" + quote
	encoded, err := EncodeMarketIdentity(base, quote)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := DecodeMarketIdentity(pair, encoded)
	if err != nil {
		t.Fatal(err)
	}
	if identity.BaseToken != base || identity.QuoteToken != quote || identity.Pair != pair {
		t.Fatalf("identity mismatch: %+v", identity)
	}
}

func TestMarketIdentityRejectsMismatchedPair(t *testing.T) {
	encoded, err := EncodeMarketIdentity("FB", "br_quote")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeMarketIdentity("FB_other", encoded); err == nil {
		t.Fatal("mismatched pair was accepted")
	}
}
