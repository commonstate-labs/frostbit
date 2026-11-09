package indexdb

import (
	"errors"
	"reflect"
	"testing"

	"dex/future"
	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

func TestGetFuturePositionHistoryPageMergesTerminalStatusesWithStableCursor(t *testing.T) {
	idb := newReadModelTestDB(t)
	positions := []*pb.FuturePositionState{
		terminalFuturePosition("p-b", "alice", "BTC_USDT", future.StatusClosed, 110),
		terminalFuturePosition("p-a", "alice", "BTC_USDT", future.StatusLiquidated, 110),
		terminalFuturePosition("p-c", "alice", "BTC_USDT", future.StatusADLClosed, 109),
		terminalFuturePosition("p-d", "alice", "BTC_USDT", future.StatusInsuredDeleveraged, 108),
		terminalFuturePosition("p-e", "alice", "BTC_USDT", future.StatusClosed, 107),
		terminalFuturePosition("p-f", "alice", "BTC_USDT", future.StatusLiquidated, 106),
	}
	block := readModelTestBlock(1, "block-1", "genesis")
	if err := idb.IndexBlockWithReadModel(block, sealedReadModel(t, block, terminalFuturePositionMutations(t, positions...)...)); err != nil {
		t.Fatal(err)
	}

	var (
		cursor string
		got    []string
	)
	for {
		page, err := idb.GetFuturePositionHistoryPage("alice", "", "closed", cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		if page.Limit != 2 || len(page.Positions) == 0 || len(page.Positions) > 2 {
			t.Fatalf("unexpected page shape: %+v", page)
		}
		for _, position := range page.Positions {
			got = append(got, position.PositionId)
		}
		if !page.HasMore {
			if page.NextCursor != "" {
				t.Fatalf("terminal page returned cursor %q", page.NextCursor)
			}
			break
		}
		if page.NextCursor == "" || page.NextCursor == cursor {
			t.Fatalf("cursor did not advance: old=%q page=%+v", cursor, page)
		}
		cursor = page.NextCursor
	}
	want := []string{"p-a", "p-b", "p-c", "p-d", "p-e", "p-f"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("positions = %v, want %v", got, want)
	}

	liquidated, err := idb.GetFuturePositionHistoryPage("alice", "", "LIQUIDATED", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := futurePositionIDs(liquidated.Positions); !reflect.DeepEqual(got, []string{"p-a", "p-f"}) {
		t.Fatalf("explicit liquidated positions = %v", got)
	}
	pairPage, err := idb.GetFuturePositionHistoryPage("", "BTC_USDT", "closed", "", 10)
	if err != nil || !reflect.DeepEqual(futurePositionIDs(pairPage.Positions), want) {
		t.Fatalf("pair page = %+v, err=%v", pairPage, err)
	}
	recentPage, err := idb.GetFuturePositionHistoryPage("", "", "closed", "", 10)
	if err != nil || !reflect.DeepEqual(futurePositionIDs(recentPage.Positions), want) {
		t.Fatalf("recent page = %+v, err=%v", recentPage, err)
	}
}

func TestGetFuturePositionHistoryPageRejectsInvalidQueryAndCursor(t *testing.T) {
	idb := newReadModelTestDB(t)
	block := readModelTestBlock(1, "block-1", "genesis")
	if err := idb.IndexBlockWithReadModel(block, sealedReadModel(t, block)); err != nil {
		t.Fatal(err)
	}

	if _, err := idb.GetFuturePositionHistoryPage("alice", "BTC_USDT", "closed", "", 10); !errors.Is(err, ErrInvalidFuturePositionQuery) {
		t.Fatalf("owner+pair error = %v", err)
	}
	if _, err := idb.GetFuturePositionHistoryPage("alice", "", "open", "", 10); !errors.Is(err, ErrInvalidFuturePositionQuery) {
		t.Fatalf("open status error = %v", err)
	}
	if _, err := idb.GetFuturePositionHistoryPage("alice", "", "closed", "%%%", 10); !errors.Is(err, ErrInvalidFuturePositionCursor) {
		t.Fatalf("malformed base64 cursor error = %v", err)
	}
	badCursor := encodeFuturePositionHistoryCursor("not-an-order-suffix")
	if _, err := idb.GetFuturePositionHistoryPage("alice", "", "closed", badCursor, 10); !errors.Is(err, ErrInvalidFuturePositionCursor) {
		t.Fatalf("malformed suffix cursor error = %v", err)
	}

	empty := newReadModelTestDB(t)
	if _, err := empty.GetFuturePositionHistoryPage("alice", "", "closed", "", 10); !errors.Is(err, ErrFuturePositionHistoryUnavailable) {
		t.Fatalf("empty read model error = %v", err)
	}
}

func TestGetFuturePositionHistoryPageFailsClosedOnIndexMismatch(t *testing.T) {
	idb := newReadModelTestDB(t)
	position := terminalFuturePosition("mismatch", "bob", "BTC_USDT", future.StatusClosed, 10)
	raw, err := proto.Marshal(position)
	if err != nil {
		t.Fatal(err)
	}
	block := readModelTestBlock(1, "block-1", "genesis")
	model := sealedReadModel(t, block,
		readModelPut(0, keys.KeyFuturePositionHistory(position.PositionId), raw, pb.ReadModelRetention_READ_MODEL_RETENTION_BLOCK_SCOPED),
		readModelPut(1, keys.KeyFuturePositionHistoryOwnerIndex("alice", position.Status, position.CloseHeight, position.PositionId), []byte(position.PositionId), pb.ReadModelRetention_READ_MODEL_RETENTION_BLOCK_SCOPED),
	)
	if err := idb.IndexBlockWithReadModel(block, model); err != nil {
		t.Fatal(err)
	}
	if _, err := idb.GetFuturePositionHistoryPage("alice", "", "closed", "", 10); err == nil {
		t.Fatal("mismatched owner index was accepted")
	}
}

func TestFuturePositionHistoryRetentionRemovesCanonicalAndIndexesTogether(t *testing.T) {
	idb := newReadModelTestDB(t)
	oldPosition := terminalFuturePosition("old", "alice", "BTC_USDT", future.StatusClosed, 1)
	newPosition := terminalFuturePosition("new", "alice", "BTC_USDT", future.StatusLiquidated, 2)

	block1 := readModelTestBlock(1, "block-1", "genesis")
	if err := idb.IndexBlockWithReadModel(block1, sealedReadModel(t, block1, terminalFuturePositionMutations(t, oldPosition)...)); err != nil {
		t.Fatal(err)
	}
	block2 := readModelTestBlock(2, "block-2", "block-1")
	if err := idb.IndexBlockWithReadModel(block2, sealedReadModel(t, block2, terminalFuturePositionMutations(t, newPosition)...)); err != nil {
		t.Fatal(err)
	}

	before, err := idb.GetFuturePositionHistoryPage("alice", "", "closed", "", 10)
	if err != nil || !reflect.DeepEqual(futurePositionIDs(before.Positions), []string{"new", "old"}) {
		t.Fatalf("before retention = %+v, err=%v", before, err)
	}
	result, err := idb.PruneToKeepLimitedResult(2, 1, 10)
	if err != nil || result.PrunedHeights != 1 {
		t.Fatalf("retention result = %+v, err=%v", result, err)
	}
	after, err := idb.GetFuturePositionHistoryPage("alice", "", "closed", "", 10)
	if err != nil || !reflect.DeepEqual(futurePositionIDs(after.Positions), []string{"new"}) {
		t.Fatalf("after retention = %+v, err=%v", after, err)
	}

	oldKeys := []string{
		keys.KeyFuturePositionHistory(oldPosition.PositionId),
		keys.KeyFuturePositionHistoryOwnerIndex(oldPosition.Owner, oldPosition.Status, oldPosition.CloseHeight, oldPosition.PositionId),
		keys.KeyFuturePositionHistoryPairIndex(oldPosition.Pair, oldPosition.Status, oldPosition.CloseHeight, oldPosition.PositionId),
		keys.KeyFuturePositionHistoryRecentIndex(oldPosition.Status, oldPosition.CloseHeight, oldPosition.PositionId),
	}
	for _, key := range oldKeys {
		if value, getErr := idb.Get(key); getErr != nil || value != "" {
			t.Fatalf("retained old key %q = %q, err=%v", key, value, getErr)
		}
		if owner, getErr := idb.Get(KeyReadModelOwner(key)); getErr != nil || owner != "" {
			t.Fatalf("retained ownership for %q = %q, err=%v", key, owner, getErr)
		}
	}
}

func terminalFuturePosition(id, owner, pair string, status future.PositionStatus, closeHeight uint64) *pb.FuturePositionState {
	return &pb.FuturePositionState{
		PositionId:  id,
		Owner:       owner,
		Pair:        pair,
		Side:        int32(future.Long),
		EntryPrice:  "100",
		Quantity:    "0",
		Margin:      "0",
		Status:      int32(status),
		OpenHeight:  1,
		CloseHeight: closeHeight,
	}
}

func terminalFuturePositionMutations(t *testing.T, positions ...*pb.FuturePositionState) []*pb.ReadModelMutation {
	t.Helper()
	mutations := make([]*pb.ReadModelMutation, 0, len(positions)*4)
	for _, position := range positions {
		raw, err := proto.Marshal(position)
		if err != nil {
			t.Fatal(err)
		}
		keysForPosition := []struct {
			key   string
			value []byte
		}{
			{keys.KeyFuturePositionHistory(position.PositionId), raw},
			{keys.KeyFuturePositionHistoryOwnerIndex(position.Owner, position.Status, position.CloseHeight, position.PositionId), []byte(position.PositionId)},
			{keys.KeyFuturePositionHistoryPairIndex(position.Pair, position.Status, position.CloseHeight, position.PositionId), []byte(position.PositionId)},
			{keys.KeyFuturePositionHistoryRecentIndex(position.Status, position.CloseHeight, position.PositionId), []byte(position.PositionId)},
		}
		for _, item := range keysForPosition {
			mutations = append(mutations, readModelPut(uint32(len(mutations)), item.key, item.value, pb.ReadModelRetention_READ_MODEL_RETENTION_BLOCK_SCOPED))
		}
	}
	return mutations
}

func futurePositionIDs(positions []*pb.FuturePositionState) []string {
	ids := make([]string, 0, len(positions))
	for _, position := range positions {
		ids = append(ids, position.PositionId)
	}
	return ids
}
