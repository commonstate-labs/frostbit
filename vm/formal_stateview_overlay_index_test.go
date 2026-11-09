package vm

import (
	"dex/db"
	"dex/keys"
	"dex/pb"
	"reflect"
	"testing"
)

                                                                                
                                                                             
                                                                            
                                                                         
func TestFormalOverlayIndexCannotChangeDiff(t *testing.T) {
	indexed := newOverlayOrderedTestStateView(map[string][]byte{}).(*overlayStateView)
	reference := newOverlayOrderedTestStateView(map[string][]byte{}).(*overlayStateView)

	for _, sv := range []*overlayStateView{indexed, reference} {
		sv.Set("formal/a", []byte("a"))
		sv.Set("formal/c", []byte("c"))
	}
	if _, err := indexed.ScanWithLimitOrderedAfter("formal/", "", 1, false); err != nil {
		t.Fatal(err)
	}
	if indexed.overlayIndex == nil {
		t.Fatal("ordered scan did not build the derived index")
	}

	indexedSnap := indexed.Snapshot()
	referenceSnap := reference.Snapshot()
	for _, sv := range []*overlayStateView{indexed, reference} {
		sv.Del("formal/a")
		sv.Set("formal/b", []byte("temporary"))
	}
	if err := indexed.Revert(indexedSnap); err != nil {
		t.Fatal(err)
	}
	if err := reference.Revert(referenceSnap); err != nil {
		t.Fatal(err)
	}

	for _, sv := range []*overlayStateView{indexed, reference} {
		sv.Del("formal/c")
		sv.Set("formal/b", []byte("final"))
		sv.Set("other/z", []byte("z"))
	}

	                                                                        
	                                                                          
	indexed.overlayIndex = newOverlayKeyIndex(nil)
	if got, want := indexed.Diff(), reference.Diff(); !reflect.DeepEqual(got, want) {
		t.Fatalf("derived overlay index changed Diff:\n got=%+v\nwant=%+v", got, want)
	}
}

func TestFormalFutureOrderbookCursorRejectsCrossPrefix(t *testing.T) {
	priceKey, err := db.PriceToKey128("100000000")
	if err != nil {
		t.Fatal(err)
	}
	prefix := keys.KeyFutureOrderPriceIndexPrefix("BTC_USDT", pb.OrderSide_BUY, false)
	foreign := keys.KeyFutureOrderPriceIndex("ETH_USDT", pb.OrderSide_SELL, false, priceKey, "order-1")
	if got := canonicalFutureOrderbookCursor(prefix, foreign); got != "" {
		t.Fatalf("cross-prefix cursor became authoritative: %q", got)
	}
}
