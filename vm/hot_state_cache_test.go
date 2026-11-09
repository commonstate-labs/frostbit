package vm

import (
	"dex/db"
	"dex/keys"
	"dex/pb"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
)

func TestHotStateCacheScanOrderedAfterAndDiff(t *testing.T) {
	prefix := keys.KeyOrderPriceIndexPrefix("BTC_USDT", pb.OrderSide_SELL, false)
	k1 := keys.KeyOrderPriceIndex("BTC_USDT", pb.OrderSide_SELL, false, hotTestPriceKey("10"), "order1")
	k2 := keys.KeyOrderPriceIndex("BTC_USDT", pb.OrderSide_SELL, false, hotTestPriceKey("20"), "order2")
	k3 := keys.KeyOrderPriceIndex("BTC_USDT", pb.OrderSide_SELL, false, hotTestPriceKey("30"), "order3")
	base := map[string][]byte{
		k2: []byte("v2"),
		k1: []byte("v1"),
	}
	loads := 0
	load := func(prefix string) (map[string][]byte, error) {
		loads++
		out := make(map[string][]byte)
		for k, v := range base {
			if strings.HasPrefix(k, prefix) {
				out[k] = cloneBytes(v)
			}
		}
		return out, nil
	}

	cache := NewHotStateCache()
	pairs, ok, err := cache.ScanOrderedAfter(prefix, "", 10, false, load)
	if err != nil || !ok {
		t.Fatalf("scan hot prefix: ok=%v err=%v", ok, err)
	}
	assertHotKeys(t, pairs, []string{k1, k2})
	if loads != 1 {
		t.Fatalf("expected one DB load, got %d", loads)
	}
	pairs[0].Value[0] = 'X'
	pairs, ok, err = cache.ScanOrderedAfter(prefix, "", 1, false, load)
	if err != nil || !ok {
		t.Fatalf("rescan hot prefix: ok=%v err=%v", ok, err)
	}
	if string(pairs[0].Value) != "v1" {
		t.Fatalf("cached value should be isolated from caller mutation, got %q", pairs[0].Value)
	}

	base[k3] = []byte("base-only")
	pairs, ok, err = cache.ScanOrderedAfter(prefix, k1, 10, false, load)
	if err != nil || !ok {
		t.Fatalf("scan cached prefix: ok=%v err=%v", ok, err)
	}
	assertHotKeys(t, pairs, []string{k2})
	if loads != 1 {
		t.Fatalf("cache should not reload built prefix, got %d loads", loads)
	}

	cache.ApplyDiff([]WriteOp{
		{Key: k1, Del: true},
		{Key: k3, Value: []byte("v3")},
	})
	pairs, ok, err = cache.ScanOrderedAfter(prefix, "", 10, false, load)
	if err != nil || !ok {
		t.Fatalf("scan after diff: ok=%v err=%v", ok, err)
	}
	assertHotKeys(t, pairs, []string{k2, k3})
	if string(pairs[1].Value) != "v3" {
		t.Fatalf("diff value not applied: %q", pairs[1].Value)
	}

	pairs, ok, err = cache.ScanOrderedAfter(prefix, k3, 10, true, load)
	if err != nil || !ok {
		t.Fatalf("reverse scan after diff: ok=%v err=%v", ok, err)
	}
	assertHotKeys(t, pairs, []string{k2})

	cache.Reset()
	pairs, ok, err = cache.ScanOrderedAfter(prefix, "", 10, false, load)
	if err != nil || !ok {
		t.Fatalf("scan after reset: ok=%v err=%v", ok, err)
	}
	assertHotKeys(t, pairs, []string{k1, k2, k3})
	if loads != 2 {
		t.Fatalf("reset should force reload, got %d loads", loads)
	}
}

func TestHotStateCacheApplyDiffUpdatesOnlyMatchingPrefix(t *testing.T) {
	btcPrefix := keys.KeyFuturePairPositionPrefix("BTC_USDT")
	ethPrefix := keys.KeyFuturePairPositionPrefix("ETH_USDT")
	btc1 := keys.KeyFuturePairPosition("BTC_USDT", "pos1")
	btc2 := keys.KeyFuturePairPosition("BTC_USDT", "pos2")
	eth1 := keys.KeyFuturePairPosition("ETH_USDT", "pos1")
	base := map[string][]byte{
		btc1: []byte("btc1"),
		eth1: []byte("eth1"),
	}
	loads := 0
	load := func(prefix string) (map[string][]byte, error) {
		loads++
		out := make(map[string][]byte)
		for k, v := range base {
			if strings.HasPrefix(k, prefix) {
				out[k] = cloneBytes(v)
			}
		}
		return out, nil
	}

	cache := NewHotStateCache()
	if _, ok, err := cache.Scan(btcPrefix, load); err != nil || !ok {
		t.Fatalf("scan btc prefix: ok=%v err=%v", ok, err)
	}
	if _, ok, err := cache.Scan(ethPrefix, load); err != nil || !ok {
		t.Fatalf("scan eth prefix: ok=%v err=%v", ok, err)
	}
	if loads != 2 {
		t.Fatalf("expected two prefix loads, got %d", loads)
	}

	cache.ApplyDiff([]WriteOp{{Key: btc2, Value: []byte("btc2")}})

	btcMap, ok, err := cache.Scan(btcPrefix, load)
	if err != nil || !ok {
		t.Fatalf("scan btc after diff: ok=%v err=%v", ok, err)
	}
	if string(btcMap[btc2]) != "btc2" {
		t.Fatalf("btc diff not applied: %q", btcMap[btc2])
	}
	ethMap, ok, err := cache.Scan(ethPrefix, load)
	if err != nil || !ok {
		t.Fatalf("scan eth after diff: ok=%v err=%v", ok, err)
	}
	if _, exists := ethMap[btc2]; exists {
		t.Fatalf("btc diff leaked into eth prefix cache")
	}
	if len(ethMap) != 1 || string(ethMap[eth1]) != "eth1" {
		t.Fatalf("eth prefix changed unexpectedly: %v", ethMap)
	}
	if loads != 2 {
		t.Fatalf("cached prefixes should not reload, got %d loads", loads)
	}
}

func TestHotStateCacheFutureOrderbookUsesCompactIndex(t *testing.T) {
	pair := "BTC_USDT"
	prefix := keys.KeyFutureOrderPriceIndexPrefix(pair, pb.OrderSide_SELL, false)
	priceKey100, err := db.PriceToKey128("100")
	if err != nil {
		t.Fatalf("price key 100: %v", err)
	}
	priceKey110, err := db.PriceToKey128("110")
	if err != nil {
		t.Fatalf("price key 110: %v", err)
	}
	k1 := keys.KeyFutureOrderPriceIndex(pair, pb.OrderSide_SELL, false, priceKey100, "sell1")
	k2 := keys.KeyFutureOrderPriceIndex(pair, pb.OrderSide_SELL, false, priceKey110, "sell2")
	base := map[string][]byte{
		k2: mustFutureOrderStateBytes(t, &pb.FutureOrderState{
			OrderId: "sell2", Owner: "maker2", OwnerAddress: "maker2", Pair: pair,
			Side: 2, Price: "110", Amount: "7", FilledBase: "0", FilledQuote: "0",
			Leverage: 10, Status: pb.FutureOrderStatus_FUTURE_ORDER_OPEN, CreateHeight: 2,
		}),
		k1: mustFutureOrderStateBytes(t, &pb.FutureOrderState{
			OrderId: "sell1", Owner: "maker1", OwnerAddress: "maker1", Pair: pair,
			Side: 2, Price: "100", Amount: "5", FilledBase: "0", FilledQuote: "0",
			Leverage: 10, Status: pb.FutureOrderStatus_FUTURE_ORDER_OPEN, CreateHeight: 1,
		}),
	}
	loads := 0
	load := func(prefix string) (map[string][]byte, error) {
		loads++
		out := make(map[string][]byte)
		for k, v := range base {
			if strings.HasPrefix(k, prefix) {
				out[k] = cloneBytes(v)
			}
		}
		return out, nil
	}

	cache := NewHotStateCache()
	pairs, ok, err := cache.ScanOrderedAfter(prefix, "", 10, false, load)
	if err != nil || !ok {
		t.Fatalf("scan future hot prefix: ok=%v err=%v", ok, err)
	}
	assertHotKeys(t, pairs, []string{k1, k2})
	if loads != 1 {
		t.Fatalf("expected one DB load, got %d", loads)
	}
	if cache.prefixes[prefix] != nil {
		t.Fatal("future orderbook must not use generic value-retaining prefix cache")
	}
	if cache.futureBooks[prefix] == nil || cache.futureBooks[prefix].count != 2 {
		t.Fatalf("compact future orderbook not loaded: %+v", cache.futureBooks[prefix])
	}
	var first pb.FutureOrderState
	if err := proto.Unmarshal(pairs[0].Value, &first); err != nil || first.OrderId != "sell1" || first.Price != "100" {
		t.Fatalf("unexpected compact state: order=%+v err=%v", &first, err)
	}

	pairs[0].Value[0] ^= 0xff
	pairs, ok, err = cache.ScanOrderedAfter(prefix, "", 1, false, load)
	if err != nil || !ok {
		t.Fatalf("rescan future hot prefix: ok=%v err=%v", ok, err)
	}
	if err := proto.Unmarshal(pairs[0].Value, &first); err != nil || first.OrderId != "sell1" {
		t.Fatalf("cached compact value should be isolated from caller mutation: order=%+v err=%v", &first, err)
	}

	updated := proto.Clone(&first).(*pb.FutureOrderState)
	updated.FilledBase = "3"
	cache.ApplyDiff([]WriteOp{{Key: k1, Value: mustFutureOrderStateBytes(t, updated)}})
	pairs, ok, err = cache.ScanOrderedAfter(prefix, "", 1, false, load)
	if err != nil || !ok {
		t.Fatalf("scan after future diff: ok=%v err=%v", ok, err)
	}
	if err := proto.Unmarshal(pairs[0].Value, &first); err != nil || first.FilledBase != "3" {
		t.Fatalf("future diff not applied: order=%+v err=%v", &first, err)
	}

	cache.ApplyDiff([]WriteOp{{Key: k1, Del: true}})
	keysOnly, ok, err := cache.ScanKeysOrderedAfter(prefix, "", 10, false, load)
	if err != nil || !ok {
		t.Fatalf("scan keys after delete: ok=%v err=%v", ok, err)
	}
	if len(keysOnly) != 1 || keysOnly[0] != k2 {
		t.Fatalf("unexpected keys after delete: %v", keysOnly)
	}
}

func TestHotStateCacheBypassesFutureOwnerPositionPrefix(t *testing.T) {
	owner := "fbc1qownerhotcache00000000000000000000000000"
	prefix := keys.KeyFutureOwnerPositionPrefix(owner)
	ownerPosKey := keys.KeyFutureOwnerPosition(owner, "pos1")
	loads := 0
	load := func(prefix string) (map[string][]byte, error) {
		loads++
		return map[string][]byte{ownerPosKey: []byte("pos")}, nil
	}

	cache := NewHotStateCache()
	pairs, ok, err := cache.ScanOrderedAfter(prefix, "", 10, false, load)
	if err != nil {
		t.Fatalf("scan owner position prefix: %v", err)
	}
	if ok || pairs != nil {
		t.Fatalf("owner position prefix should bypass hot cache: ok=%v pairs=%v", ok, pairs)
	}
	if loads != 0 {
		t.Fatalf("bypassed prefix should not call loader, got %d loads", loads)
	}
	if _, exists := cache.prefixes[prefix]; exists {
		t.Fatalf("owner position prefix should not be cached")
	}
	if bucket := hotPrefixBucket(prefix); bucket != "" {
		t.Fatalf("owner position prefix bucket = %q, want empty", bucket)
	}
	if bucket := hotPrefixBucketForKey(ownerPosKey); bucket != "" {
		t.Fatalf("owner position key bucket = %q, want empty", bucket)
	}
}

func TestHotStateCacheGetTracksHotKeysOnly(t *testing.T) {
	hotKey := keys.KeyOrderState("order1")
	coldKey := keys.KeyBalance("alice", "FB")
	base := map[string][]byte{hotKey: []byte("open"), coldKey: []byte("balance")}
	loads := 0
	load := func(key string) ([]byte, error) {
		loads++
		return cloneBytes(base[key]), nil
	}

	cache := NewHotStateCache()
	val, ok, err := cache.Get(coldKey, load)
	if err != nil || ok || val != nil {
		t.Fatalf("cold get should bypass cache: ok=%v val=%q err=%v", ok, val, err)
	}
	if loads != 0 {
		t.Fatalf("cold key should not call cache loader, got %d loads", loads)
	}

	val, ok, err = cache.Get(hotKey, load)
	if err != nil || !ok || string(val) != "open" {
		t.Fatalf("hot get: ok=%v val=%q err=%v", ok, val, err)
	}
	base[hotKey] = []byte("base-changed")
	val, ok, err = cache.Get(hotKey, load)
	if err != nil || !ok || string(val) != "open" {
		t.Fatalf("hot cached get: ok=%v val=%q err=%v", ok, val, err)
	}
	if loads != 1 {
		t.Fatalf("hot key should be loaded once, got %d", loads)
	}

	cache.ApplyDiff([]WriteOp{{Key: hotKey, Value: []byte("filled")}})
	val, ok, err = cache.Get(hotKey, load)
	if err != nil || !ok || string(val) != "filled" {
		t.Fatalf("hot diff get: ok=%v val=%q err=%v", ok, val, err)
	}
	cache.ApplyDiff([]WriteOp{{Key: hotKey, Del: true}})
	val, ok, err = cache.Get(hotKey, load)
	if err != nil || !ok || string(val) != "base-changed" {
		t.Fatalf("deleted hot key should reload from DB: ok=%v val=%q err=%v", ok, val, err)
	}
}

func assertHotKeys(t *testing.T, pairs []KVPair, want []string) {
	t.Helper()
	if len(pairs) != len(want) {
		t.Fatalf("keys len mismatch: got %d want %d (%v)", len(pairs), len(want), pairs)
	}
	for i, key := range want {
		if pairs[i].Key != key {
			t.Fatalf("key[%d]=%q want %q", i, pairs[i].Key, key)
		}
	}
}

func mustFutureOrderStateBytes(t *testing.T, st *pb.FutureOrderState) []byte {
	t.Helper()
	data, err := proto.Marshal(st)
	if err != nil {
		t.Fatalf("marshal future order state: %v", err)
	}
	return data
}

func hotTestPriceKey(v string) string {
	return strings.Repeat("0", 67-len(v)) + v
}
