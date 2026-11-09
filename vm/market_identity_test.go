package vm

import (
	"dex/keys"
	"dex/protocol"
	"testing"
)

func TestFutureMarketConfigPreservesBrQuoteAssetID(t *testing.T) {
	sv := newMemStateView()
	baseToken := "FB"
	quoteToken := "br_718d932d6adfb5e61bfbbbb7b266e13884b6604908067bbe8d5bad1a7159159c"
	pair := baseToken + "_" + quoteToken
	sv.Set(keys.KeyToken(baseToken), []byte{1})
	sv.Set(keys.KeyToken(quoteToken), []byte{1})

	if _, err := registerSpotMarket(sv, baseToken, quoteToken); err != nil {
		t.Fatal(err)
	}
	if err := ensureFutureMarket(sv, pair, 10); err != nil {
		t.Fatal(err)
	}
	encoded, exists, err := sv.Get(keys.KeyFutureMarketConfig(pair))
	if err != nil || !exists {
		t.Fatalf("future market config missing: exists=%v err=%v", exists, err)
	}
	identity, err := protocol.DecodeMarketIdentity(pair, encoded)
	if err != nil {
		t.Fatal(err)
	}
	if identity.BaseToken != baseToken || identity.QuoteToken != quoteToken {
		t.Fatalf("persisted identity=%+v", identity)
	}
	cfg, err := newFutureEngine(sv, 10).GetMarketConfig(pair)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BaseToken != baseToken || cfg.QuoteToken != quoteToken {
		t.Fatalf("engine config=%+v", cfg)
	}
}

func TestRegisterSpotMarketRequiresBothAssetsInTokenRegistry(t *testing.T) {
	sv := newMemStateView()
	sv.Set(keys.KeyToken("BASE"), []byte{1})

	if _, err := registerSpotMarket(sv, "BASE", "QUOTE"); err == nil {
		t.Fatal("unregistered quote asset was accepted")
	}
	if _, exists, err := sv.Get(keys.KeySpotMarketConfig("BASE_QUOTE")); err != nil || exists {
		t.Fatalf("invalid market was persisted: exists=%v err=%v", exists, err)
	}

	sv.Set(keys.KeyToken("QUOTE"), []byte{1})
	identity, err := registerSpotMarket(sv, "BASE", "QUOTE")
	if err != nil {
		t.Fatal(err)
	}
	if identity.BaseToken != "BASE" || identity.QuoteToken != "QUOTE" {
		t.Fatalf("identity=%+v", identity)
	}
}

func TestLegacyMarketResolverUsesExactRegisteredAssetIDs(t *testing.T) {
	sv := newMemStateView()
	baseToken := "br_base_hash"
	quoteToken := "br_quote_hash"
	pair := baseToken + "_" + quoteToken
	sv.Set(keys.KeyToken(baseToken), []byte{1})
	sv.Set(keys.KeyToken(quoteToken), []byte{1})

	identity, err := resolveLegacyMarketIdentity(sv, pair)
	if err != nil {
		t.Fatal(err)
	}
	if identity.BaseToken != baseToken || identity.QuoteToken != quoteToken {
		t.Fatalf("resolved identity=%+v", identity)
	}
}
