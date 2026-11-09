package vm

import (
	"testing"

	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

func TestSpotAccountOrderSnapshotDoesNotMaintainRecentIndex(t *testing.T) {
	sv := newMemStateView()
	oldState := &pb.OrderState{
		OrderId: "spot-order", Owner: "alice", BaseToken: "BTC", QuoteToken: "USDT",
		Status: pb.OrderStateStatus_ORDER_OPEN, CreateHeight: 10,
	}
	oldRecentKey := keys.KeySpotAccountOrderRecentFromState(oldState)
	legacyMarker := []byte("legacy-spot-marker")
	sv.Set(oldRecentKey, legacyMarker)

	newState := proto.Clone(oldState).(*pb.OrderState)
	newState.Status = pb.OrderStateStatus_ORDER_FILLED
	data, err := proto.Marshal(newState)
	if err != nil {
		t.Fatal(err)
	}
	syncSpotAccountOrderIndex(sv, newState, data, oldState)

	if marker, exists, err := sv.Get(oldRecentKey); err != nil || !exists || string(marker) != string(legacyMarker) {
		t.Fatalf("legacy recent marker was modified: exists=%t err=%v value=%q", exists, err, marker)
	}
	if marker, exists, err := sv.Get(keys.KeySpotAccountOrderRecentFromState(newState)); err != nil || exists || len(marker) != 0 {
		t.Fatalf("new recent marker was written: exists=%t err=%v value=%q", exists, err, marker)
	}
}

func TestFutureAccountOrderSnapshotDoesNotMaintainRecentIndex(t *testing.T) {
	sv := newMemStateView()
	oldState := &pb.FutureOrderState{
		OrderId: "future-order", Owner: "alice", OwnerAddress: "alice", Pair: "BTC_USDT",
		Price: "100", Amount: "1", Status: pb.FutureOrderStatus_FUTURE_ORDER_OPEN, CreateHeight: 10,
	}
	oldRecentKey := keys.KeyFutureAccountOrderRecentFromState(oldState)
	legacyMarker := []byte("legacy-future-marker")
	sv.Set(oldRecentKey, legacyMarker)

	newState := proto.Clone(oldState).(*pb.FutureOrderState)
	newState.Status = pb.FutureOrderStatus_FUTURE_ORDER_FILLED
	if err := writeFutureOrderSnapshot(sv, newState, oldState); err != nil {
		t.Fatal(err)
	}

	if marker, exists, err := sv.Get(oldRecentKey); err != nil || !exists || string(marker) != string(legacyMarker) {
		t.Fatalf("legacy recent marker was modified: exists=%t err=%v value=%q", exists, err, marker)
	}
	if marker, exists, err := sv.Get(keys.KeyFutureAccountOrderRecentFromState(newState)); err != nil || exists || len(marker) != 0 {
		t.Fatalf("new recent marker was written: exists=%t err=%v value=%q", exists, err, marker)
	}
}
