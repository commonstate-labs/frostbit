package vm

import (
	"dex/db"
	"dex/future"
	"dex/keys"
	"dex/pb"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
)

type failGetManyStateView struct {
	StateView
}

func (s failGetManyStateView) GetMany([]string) (map[string][]byte, error) {
	return nil, errors.New("GetMany must not be called")
}

func TestMatchFutureOrderUsesPriceIndexSnapshot(t *testing.T) {
	sv := newMemStateView()
	pair := "BTC_USDT"
	                                                                          
	                                                                          
	                                                   
	price := big.NewInt(1_000_000_000)
	makerQty := big.NewInt(10)
	maker := &pb.FutureOrderState{
		OrderId: "maker_sell", Owner: "maker", OwnerAddress: "maker", Pair: pair,
		Side: int32(future.Short), Price: price.String(), Amount: makerQty.String(),
		FilledBase: "0", FilledQuote: "0", Leverage: 10,
		Status: pb.FutureOrderStatus_FUTURE_ORDER_OPEN, CreateHeight: 1,
	}
	data, err := proto.Marshal(maker)
	if err != nil {
		t.Fatalf("marshal maker: %v", err)
	}
	sv.Set(keys.KeyFutureOrderState(maker.OrderId), data)
	priceKey, err := db.PriceToKey128(price.String())
	if err != nil {
		t.Fatalf("price key: %v", err)
	}
	indexKey := keys.KeyFutureOrderPriceIndex(pair, pb.OrderSide_SELL, false, priceKey, maker.OrderId)
	sv.Set(indexKey, data)
	SetBalance(sv, maker.Owner, "USDT", &pb.TokenBalance{OrderFrozenBalance: future.CalcRequiredMargin(makerQty, price, maker.Leverage).String()})

	wrapped := failGetManyStateView{StateView: sv}
	trades, err := MatchFutureOrder(wrapped, pair, pb.OrderSide_BUY, big.NewInt(4), price, false, 0, "taker")
	if err != nil {
		t.Fatalf("first match failed: %v", err)
	}
	if len(trades) != 1 || trades[0].Quantity.Cmp(big.NewInt(4)) != 0 || trades[0].MakerFilled {
		t.Fatalf("unexpected first trades: %+v", trades)
	}
	indexData, exists, _ := sv.Get(indexKey)
	if !exists {
		t.Fatal("partial maker must stay in price index")
	}
	var afterPartial pb.FutureOrderState
	if err := proto.Unmarshal(indexData, &afterPartial); err != nil {
		t.Fatalf("unmarshal partial index value: %v", err)
	}
	if afterPartial.FilledBase != "4" || afterPartial.Status != pb.FutureOrderStatus_FUTURE_ORDER_OPEN {
		t.Fatalf("price index snapshot not updated after partial fill: %+v", &afterPartial)
	}

	trades, err = MatchFutureOrder(wrapped, pair, pb.OrderSide_BUY, big.NewInt(6), price, false, 0, "taker2")
	if err != nil {
		t.Fatalf("second match failed: %v", err)
	}
	if len(trades) != 1 || trades[0].Quantity.Cmp(big.NewInt(6)) != 0 || !trades[0].MakerFilled {
		t.Fatalf("unexpected second trades: %+v", trades)
	}
	if data, exists, _ := sv.Get(indexKey); exists || len(data) != 0 {
		t.Fatalf("filled maker index should be deleted: exists=%v data=%x", exists, data)
	}
	if data, exists, _ := sv.Get(keys.KeyFutureOrderState(maker.OrderId)); exists || len(data) != 0 {
		t.Fatalf("filled maker state should be deleted: exists=%v data=%x", exists, data)
	}
}

func TestMatchFutureOrderUsesCompactSnapshotScanner(t *testing.T) {
	pair := "BTC_USDT"
	price := big.NewInt(1_000_000_000)
	priceKey, err := db.PriceToKey128(price.String())
	if err != nil {
		t.Fatalf("price key: %v", err)
	}
	maker := &pb.FutureOrderState{
		OrderId: "maker_compact", Owner: "maker", OwnerAddress: "maker", Pair: pair,
		Side: int32(future.Short), Price: price.String(), Amount: "10",
		FilledBase: "0", FilledQuote: "0", Leverage: 10,
		Status: pb.FutureOrderStatus_FUTURE_ORDER_OPEN, CreateHeight: 1,
	}
	data, err := proto.Marshal(maker)
	if err != nil {
		t.Fatalf("marshal maker: %v", err)
	}
	prefix := keys.KeyFutureOrderPriceIndexPrefix(pair, pb.OrderSide_SELL, false)
	indexKey := keys.KeyFutureOrderPriceIndex(pair, pb.OrderSide_SELL, false, priceKey, maker.OrderId)
	baseEntries := []futureOrderbookEntry{{
		priceKey: priceKey,
		orderID:  maker.OrderId,
		snapshot: compactFutureOrderSnapshot(maker.OrderId, int32(future.Short), data),
	}}
	scanCalls := 0
	failScan := func(string) (map[string][]byte, error) { return nil, errors.New("generic scan must not be called") }
	failLimit := func(string, int) (map[string][]byte, error) { return nil, errors.New("limit scan must not be called") }
	failOrdered := func(string, int, bool) ([]KVPair, error) { return nil, errors.New("ordered scan must not be called") }
	failAfter := func(string, string, int, bool) ([]KVPair, error) {
		return nil, errors.New("ordered-after scan must not be called")
	}
	failKeys := func(string, int, bool) ([]string, error) { return nil, errors.New("key scan must not be called") }
	failKeysAfter := func(string, string, int, bool) ([]string, error) {
		return nil, errors.New("key-after scan must not be called")
	}
	scanFuture := func(scanPrefix, startAfter string, limit int, reverse bool) ([]futureOrderbookEntry, bool, error) {
		return nil, true, errors.New("slice snapshot scanner must not be called")
	}
	scanFutureVisit := func(scanPrefix, startAfter string, limit int, reverse bool, visit FutureOrderbookVisitFn) (bool, error) {
		if scanPrefix != prefix {
			return false, nil
		}
		scanCalls++
		count := 0
		for _, e := range baseEntries {
			if !futureOrderbookEntryAfter(e, startAfter, reverse) {
				continue
			}
			count++
			if !visit(e) {
				break
			}
			if limit > 0 && count >= limit {
				break
			}
		}
		return true, nil
	}
	sv := NewStateViewWithLimitScanOrderedBatchKeysAndFuture(
		func(string) ([]byte, error) { return nil, nil },
		nil,
		failScan,
		failLimit,
		failLimit,
		failOrdered,
		failAfter,
		failKeys,
		failKeysAfter,
		scanFuture,
		scanFutureVisit,
	)
	SetBalance(sv, maker.Owner, "USDT", &pb.TokenBalance{OrderFrozenBalance: future.CalcRequiredMargin(big.NewInt(10), price, maker.Leverage).String()})

	trades, err := MatchFutureOrder(sv, pair, pb.OrderSide_BUY, big.NewInt(4), price, false, 0, "taker")
	if err != nil {
		t.Fatalf("first match failed: %v", err)
	}
	if scanCalls == 0 {
		t.Fatal("compact snapshot scanner was not used")
	}
	if len(trades) != 1 || trades[0].Quantity.Cmp(big.NewInt(4)) != 0 || trades[0].MakerFilled {
		t.Fatalf("unexpected first trades: %+v", trades)
	}
	indexData, exists, _ := sv.Get(indexKey)
	if !exists {
		t.Fatal("partial maker must stay in price index")
	}
	var afterPartial pb.FutureOrderState
	if err := proto.Unmarshal(indexData, &afterPartial); err != nil {
		t.Fatalf("unmarshal partial index value: %v", err)
	}
	if afterPartial.FilledBase != "4" || afterPartial.Status != pb.FutureOrderStatus_FUTURE_ORDER_OPEN {
		t.Fatalf("price index snapshot not updated after partial fill: %+v", &afterPartial)
	}

	trades, err = MatchFutureOrder(sv, pair, pb.OrderSide_BUY, big.NewInt(6), price, false, 0, "taker2")
	if err != nil {
		t.Fatalf("second match failed: %v", err)
	}
	if len(trades) != 1 || trades[0].Quantity.Cmp(big.NewInt(6)) != 0 || !trades[0].MakerFilled {
		t.Fatalf("unexpected second trades: %+v", trades)
	}
	if data, exists, _ := sv.Get(indexKey); exists || len(data) != 0 {
		t.Fatalf("filled maker index should be deleted: exists=%v data=%x", exists, data)
	}
	if data, exists, _ := sv.Get(keys.KeyFutureOrderState(maker.OrderId)); exists || len(data) != 0 {
		t.Fatalf("filled maker state should be deleted: exists=%v data=%x", exists, data)
	}
}

func TestStateViewFutureOrderbookSnapshotsMergeOverlay(t *testing.T) {
	pair := "BTC_USDT"
	prefix := keys.KeyFutureOrderPriceIndexPrefix(pair, pb.OrderSide_SELL, false)
	price100, _ := db.PriceToKey128("100000000")
	price105, _ := db.PriceToKey128("105000000")
	price110, _ := db.PriceToKey128("110000000")
	baseA := futureOrderbookEntry{priceKey: price100, orderID: "a", snapshot: hotFutureOrderSnapshot{indexOrderID: "a", stateOrderID: "a", owner: "a", amount: "1", status: pb.FutureOrderStatus_FUTURE_ORDER_OPEN, side: int32(future.Short)}}
	baseB := futureOrderbookEntry{priceKey: price110, orderID: "b", snapshot: hotFutureOrderSnapshot{indexOrderID: "b", stateOrderID: "b", owner: "b", amount: "1", status: pb.FutureOrderStatus_FUTURE_ORDER_OPEN, side: int32(future.Short)}}
	scanFuture := func(scanPrefix, startAfter string, limit int, reverse bool) ([]futureOrderbookEntry, bool, error) {
		if scanPrefix != prefix {
			return nil, false, nil
		}
		entries := []futureOrderbookEntry{baseA, baseB}
		if reverse {
			entries = []futureOrderbookEntry{baseB, baseA}
		}
		out := make([]futureOrderbookEntry, 0, len(entries))
		for _, e := range entries {
			if futureOrderbookEntryAfter(e, startAfter, reverse) {
				out = append(out, e)
			}
		}
		return out, true, nil
	}
	sv := NewStateViewWithLimitScanOrderedBatchKeysAndFuture(
		func(string) ([]byte, error) { return nil, nil },
		nil,
		func(string) (map[string][]byte, error) { return nil, nil },
		nil, nil, nil, nil, nil, nil,
		scanFuture,
		nil,
	)
	sv.Del(keys.KeyFutureOrderPriceIndex(pair, pb.OrderSide_SELL, false, price100, "a"))
	overlay := &pb.FutureOrderState{
		OrderId: "c", Owner: "c", Pair: pair, Side: int32(future.Short), Price: "105000000", Amount: "1",
		Status: pb.FutureOrderStatus_FUTURE_ORDER_OPEN,
	}
	overlayData, err := proto.Marshal(overlay)
	if err != nil {
		t.Fatalf("marshal overlay: %v", err)
	}
	sv.Set(keys.KeyFutureOrderPriceIndex(pair, pb.OrderSide_SELL, false, price105, "c"), overlayData)

	scanner := sv.(futureOrderbookSnapshotScanner)
	got, ok, err := scanner.ScanFutureOrderbookSnapshots(prefix, "", 10, false)
	if err != nil || !ok {
		t.Fatalf("scan future snapshots: ok=%v err=%v", ok, err)
	}
	if len(got) != 2 || got[0].orderID != "c" || got[1].orderID != "b" {
		t.Fatalf("overlay merge forward = %+v", got)
	}
	got, ok, err = scanner.ScanFutureOrderbookSnapshots(prefix, "", 10, true)
	if err != nil || !ok {
		t.Fatalf("reverse scan future snapshots: ok=%v err=%v", ok, err)
	}
	if len(got) != 2 || got[0].orderID != "b" || got[1].orderID != "c" {
		t.Fatalf("overlay merge reverse = %+v", got)
	}
}

func TestStateViewFutureOrderbookPaginationBoundsOverlayWork(t *testing.T) {
	for _, pageSize := range []int{63, 64, 65} {
		t.Run(fmt.Sprintf("page_%d", pageSize), func(t *testing.T) {
			testStateViewFutureOrderbookPaginationBoundsOverlayWork(t, pageSize)
		})
	}
}

func testStateViewFutureOrderbookPaginationBoundsOverlayWork(t *testing.T, pageSize int) {
	pair := "BTC_USDT"
	prefix := keys.KeyFutureOrderPriceIndexPrefix(pair, pb.OrderSide_SELL, false)
	priceKey, err := db.PriceToKey128("100000000")
	if err != nil {
		t.Fatalf("price key: %v", err)
	}
	const baseCount = 513
	base := make([]futureOrderbookEntry, 0, baseCount)
	for i := 0; i < baseCount; i++ {
		orderID := fmt.Sprintf("order-%05d0", i)
		base = append(base, futureOrderbookEntry{
			priceKey: priceKey,
			orderID:  orderID,
			snapshot: hotFutureOrderSnapshot{indexOrderID: orderID, stateOrderID: orderID},
		})
	}

	type scanStats struct {
		calls int
		rows  int
	}
	var sliceStats, visitStats scanStats
	scanBase := func(startAfter string, limit int, reverse bool, count *scanStats, visit func(futureOrderbookEntry) bool) {
		count.calls++
		seen := 0
		walk := func(entry futureOrderbookEntry) bool {
			if !futureOrderbookEntryAfter(entry, startAfter, reverse) {
				return true
			}
			seen++
			count.rows++
			if !visit(entry) {
				return false
			}
			return limit <= 0 || seen < limit
		}
		if reverse {
			for i := len(base) - 1; i >= 0; i-- {
				if !walk(base[i]) {
					return
				}
			}
			return
		}
		for _, entry := range base {
			if !walk(entry) {
				return
			}
		}
	}
	scanFuture := func(scanPrefix, startAfter string, limit int, reverse bool) ([]futureOrderbookEntry, bool, error) {
		if scanPrefix != prefix {
			return nil, false, nil
		}
		out := make([]futureOrderbookEntry, 0, scanOutputCap(baseCount, limit))
		scanBase(startAfter, limit, reverse, &sliceStats, func(entry futureOrderbookEntry) bool {
			out = append(out, entry)
			return true
		})
		return out, true, nil
	}
	scanFutureVisit := func(scanPrefix, startAfter string, limit int, reverse bool, visit FutureOrderbookVisitFn) (bool, error) {
		if scanPrefix != prefix {
			return false, nil
		}
		scanBase(startAfter, limit, reverse, &visitStats, visit)
		return true, nil
	}
	sv := NewStateViewWithLimitScanOrderedBatchKeysAndFuture(
		func(string) ([]byte, error) { return nil, nil },
		nil,
		func(string) (map[string][]byte, error) { return nil, nil },
		nil, nil, nil, nil, nil, nil,
		scanFuture,
		scanFutureVisit,
	)

	effective := make(map[string]futureOrderbookEntry, baseCount*2)
	for _, entry := range base {
		effective[entry.indexKey(prefix)] = entry
	}
	overlayData, err := proto.Marshal(&pb.FutureOrderState{
		OrderId: "overlay", Owner: "overlay", Pair: pair, Side: int32(future.Short),
		Price: "100000000", Amount: "1", Status: pb.FutureOrderStatus_FUTURE_ORDER_OPEN,
	})
	if err != nil {
		t.Fatalf("marshal overlay: %v", err)
	}
	overlayKeys := 0
	for i, entry := range base {
		if i%4 == 0 {
			key := entry.indexKey(prefix)
			sv.Del(key)
			delete(effective, key)
			overlayKeys++
		}
		orderID := fmt.Sprintf("order-%05d5", i)
		inserted := futureOrderbookEntry{priceKey: priceKey, orderID: orderID}
		key := inserted.indexKey(prefix)
		sv.Set(key, overlayData)
		effective[key] = inserted
		overlayKeys++
	}
	expected := make([]futureOrderbookEntry, 0, len(effective))
	for _, entry := range effective {
		expected = append(expected, entry)
	}
	sort.Slice(expected, func(i, j int) bool {
		return compareFutureOrderbookKeys(expected[i].priceKey, expected[i].orderID, expected[j].priceKey, expected[j].orderID, false) < 0
	})

	entryKeys := func(entries []futureOrderbookEntry) []string {
		out := make([]string, 0, len(entries))
		for _, entry := range entries {
			out = append(out, entry.indexKey(prefix))
		}
		return out
	}
	equalKeys := func(got, want []string) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}
	expectedKeys := entryKeys(expected)

	scanner := sv.(futureOrderbookSnapshotScanner)
	visitor := sv.(futureOrderbookSnapshotVisitor)
	collectScan := func(reverse bool) []string {
		var out []string
		cursor := ""
		for pageNo := 0; pageNo <= len(expected)/pageSize+2; pageNo++ {
			page, ok, scanErr := scanner.ScanFutureOrderbookSnapshots(prefix, cursor, pageSize, reverse)
			if scanErr != nil || !ok {
				t.Fatalf("scan page reverse=%v page=%d ok=%v err=%v", reverse, pageNo, ok, scanErr)
			}
			if len(page) > pageSize {
				t.Fatalf("scan page exceeded limit: %d", len(page))
			}
			if len(page) == 0 {
				return out
			}
			if len(out)+len(page) < len(expected) && len(page) != pageSize {
				t.Fatalf("scan returned an unfilled intermediate page: got=%d limit=%d", len(page), pageSize)
			}
			keys := entryKeys(page)
			out = append(out, keys...)
			cursor = keys[len(keys)-1]
		}
		t.Fatal("scan pagination did not terminate")
		return nil
	}
	collectVisit := func(reverse bool) []string {
		var out []string
		cursor := ""
		for pageNo := 0; pageNo <= len(expected)/pageSize+2; pageNo++ {
			var page []futureOrderbookEntry
			ok, visitErr := visitor.VisitFutureOrderbookSnapshots(prefix, cursor, pageSize, reverse, func(entry futureOrderbookEntry) bool {
				page = append(page, entry)
				return true
			})
			if visitErr != nil || !ok {
				t.Fatalf("visit page reverse=%v page=%d ok=%v err=%v", reverse, pageNo, ok, visitErr)
			}
			if len(page) > pageSize {
				t.Fatalf("visit page exceeded limit: %d", len(page))
			}
			if len(page) == 0 {
				return out
			}
			if len(out)+len(page) < len(expected) && len(page) != pageSize {
				t.Fatalf("visit returned an unfilled intermediate page: got=%d limit=%d", len(page), pageSize)
			}
			keys := entryKeys(page)
			out = append(out, keys...)
			cursor = keys[len(keys)-1]
		}
		t.Fatal("visit pagination did not terminate")
		return nil
	}

	for _, reverse := range []bool{false, true} {
		want := append([]string(nil), expectedKeys...)
		if reverse {
			for i, j := 0, len(want)-1; i < j; i, j = i+1, j-1 {
				want[i], want[j] = want[j], want[i]
			}
		}
		gotScan := collectScan(reverse)
		gotVisit := collectVisit(reverse)
		if !equalKeys(gotScan, want) {
			t.Fatalf("scan pagination mismatch reverse=%v: got=%d want=%d", reverse, len(gotScan), len(want))
		}
		if !equalKeys(gotVisit, want) {
			t.Fatalf("visit pagination mismatch reverse=%v: got=%d want=%d", reverse, len(gotVisit), len(want))
		}
		if !equalKeys(gotScan, gotVisit) {
			t.Fatalf("scan/visit mismatch reverse=%v", reverse)
		}

		invalidCursor := keys.KeyFutureOrderPriceIndex("ETH_USDT", pb.OrderSide_SELL, false, priceKey, "other")
		head, _, headErr := scanner.ScanFutureOrderbookSnapshots(prefix, "", pageSize, reverse)
		fromInvalid, _, invalidErr := scanner.ScanFutureOrderbookSnapshots(prefix, invalidCursor, pageSize, reverse)
		if headErr != nil || invalidErr != nil || !equalKeys(entryKeys(fromInvalid), entryKeys(head)) {
			t.Fatalf("invalid cursor did not restart from head reverse=%v: headErr=%v invalidErr=%v", reverse, headErr, invalidErr)
		}
		fromMalformed, _, malformedErr := scanner.ScanFutureOrderbookSnapshots(prefix, prefix+"price:not-canonical", pageSize, reverse)
		if malformedErr != nil || !equalKeys(entryKeys(fromMalformed), entryKeys(head)) {
			t.Fatalf("malformed cursor did not restart from head reverse=%v: err=%v", reverse, malformedErr)
		}
		var visitInvalid []futureOrderbookEntry
		visitOK, visitInvalidErr := visitor.VisitFutureOrderbookSnapshots(prefix, invalidCursor, pageSize, reverse, func(entry futureOrderbookEntry) bool {
			visitInvalid = append(visitInvalid, entry)
			return true
		})
		if !visitOK || visitInvalidErr != nil || !equalKeys(entryKeys(visitInvalid), entryKeys(head)) {
			t.Fatalf("visit invalid cursor did not restart from head reverse=%v: ok=%v err=%v", reverse, visitOK, visitInvalidErr)
		}
	}

	view := sv.(*overlayStateView)
	if view.overlayIndex == nil {
		t.Fatal("overlay B-tree was not built")
	}
	if got, max := view.overlayIndex.rangeVisits, uint64(8*(overlayKeys+len(expected))); got > max {
		t.Fatalf("overlay range visits are not near-linear: got=%d max=%d overlay=%d effective=%d", got, max, overlayKeys, len(expected))
	}
	if got, max := sliceStats.rows+visitStats.rows, 12*baseCount; got > max {
		t.Fatalf("base rows are not near-linear: got=%d max=%d scanCalls=%d visitCalls=%d", got, max, sliceStats.calls, visitStats.calls)
	}
	t.Logf("effective=%d overlayKeys=%d rangeVisits=%d baseRows=%d scanCalls=%d visitCalls=%d", len(expected), overlayKeys, view.overlayIndex.rangeVisits, sliceStats.rows+visitStats.rows, sliceStats.calls, visitStats.calls)
}

func TestStateViewFutureOrderbookRejectsNonMonotonicBasePage(t *testing.T) {
	pair := "BTC_USDT"
	prefix := keys.KeyFutureOrderPriceIndexPrefix(pair, pb.OrderSide_SELL, false)
	priceKey, err := db.PriceToKey128("100000000")
	if err != nil {
		t.Fatalf("price key: %v", err)
	}
	a := futureOrderbookEntry{priceKey: priceKey, orderID: "a"}
	b := futureOrderbookEntry{priceKey: priceKey, orderID: "b"}
	for name, page := range map[string][]futureOrderbookEntry{
		"duplicate":     {a, a},
		"non_monotonic": {b, a},
	} {
		t.Run(name, func(t *testing.T) {
			scanFuture := func(scanPrefix, startAfter string, limit int, reverse bool) ([]futureOrderbookEntry, bool, error) {
				if scanPrefix != prefix {
					return nil, false, nil
				}
				return page, true, nil
			}
			sv := NewStateViewWithLimitScanOrderedBatchKeysAndFuture(
				func(string) ([]byte, error) { return nil, nil }, nil,
				func(string) (map[string][]byte, error) { return nil, nil },
				nil, nil, nil, nil, nil, nil, scanFuture, nil,
			)
			_, ok, scanErr := sv.(futureOrderbookSnapshotScanner).ScanFutureOrderbookSnapshots(prefix, "", 2, false)
			if !ok || scanErr == nil || !strings.Contains(scanErr.Error(), "non-monotonic future orderbook base scan") {
				t.Fatalf("expected non-monotonic rejection: ok=%v err=%v", ok, scanErr)
			}
		})
	}
}
