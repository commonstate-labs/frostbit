package vm

import (
	"encoding/json"
	"testing"

	"dex/future"
	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

func TestBuildBlockReadModelDerivesFrontendIndexes(t *testing.T) {
	activity, err := json.Marshal(&UserActivityRecord{
		Kind: ActKindFundingSettle, Height: 7, TxID: "activity-tx", Amount: "5", Token: "USDT",
	})
	if err != nil {
		t.Fatal(err)
	}
	spot := &pb.OrderState{
		OrderId: "spot-1", Owner: "alice", BaseToken: "FB", QuoteToken: "USDT",
		Status: pb.OrderStateStatus_ORDER_OPEN, CreateHeight: 7,
	}
	spotData, err := proto.Marshal(spot)
	if err != nil {
		t.Fatal(err)
	}
	futureOrder := &pb.FutureOrderState{
		OrderId: "future-1", OwnerAddress: "alice", Pair: "FB_USDT",
		Status: pb.FutureOrderStatus_FUTURE_ORDER_FILLED, CreateHeight: 6,
	}
	futureData, err := proto.Marshal(futureOrder)
	if err != nil {
		t.Fatal(err)
	}
	position := &pb.FuturePositionState{
		PositionId: "position-1", Owner: "alice", Pair: "FB_USDT", Side: 1,
		EntryPrice: "100", Quantity: "0", Margin: "0", Status: int32(future.StatusLiquidated),
		OpenHeight: 2, CloseHeight: 7,
	}
	positionData, err := proto.Marshal(position)
	if err != nil {
		t.Fatal(err)
	}

	activityKey := keys.KeyUserActivity("alice", 7, 0)
	spotKey := keys.KeySpotAccountOrderFromState(spot)
	futureKey := keys.KeyFutureAccountOrderFromState(futureOrder)
	positionKey := keys.KeyFuturePositionHistory(position.PositionId)
	model, err := buildBlockReadModel(&pb.Block{
		BlockHash: "block-7", Header: &pb.BlockHeader{Height: 7, PrevBlockHash: "block-6"},
	}, []WriteOp{
		{Key: activityKey, Value: activity},
		{Key: spotKey, Value: spotData},
		{Key: futureKey, Value: futureData},
		{Key: positionKey, Value: positionData},
	})
	if err != nil {
		t.Fatalf("build read model: %v", err)
	}
	if err := pb.VerifyBlockReadModel(model, 7, "block-7"); err != nil {
		t.Fatalf("verify read model: %v", err)
	}

	got := readModelMutationMap(model)
	wantKeys := []string{
		activityKey,
		keys.KeyUserActivityKindIndex("alice", ActKindFundingSettle, 7, "activity-tx", 0),
		spotKey,
		keys.KeySpotAccountOrderRecentFromState(spot),
		futureKey,
		keys.KeyFutureAccountOrderRecentFromState(futureOrder),
		positionKey,
		keys.KeyFuturePositionHistoryOwnerIndex(position.Owner, position.Status, position.CloseHeight, position.PositionId),
		keys.KeyFuturePositionHistoryPairIndex(position.Pair, position.Status, position.CloseHeight, position.PositionId),
		keys.KeyFuturePositionHistoryRecentIndex(position.Status, position.CloseHeight, position.PositionId),
	}
	for _, key := range wantKeys {
		if got[key] == nil || got[key].Op != pb.ReadModelMutationOp_READ_MODEL_MUTATION_PUT {
			t.Fatalf("missing PUT mutation for %q", key)
		}
	}
	if got[keys.KeySpotAccountOrderRecentFromState(spot)].Retention != pb.ReadModelRetention_READ_MODEL_RETENTION_CURRENT {
		t.Fatal("open spot order projection must be CURRENT")
	}
	if got[keys.KeyFutureAccountOrderRecentFromState(futureOrder)].Retention != pb.ReadModelRetention_READ_MODEL_RETENTION_BLOCK_SCOPED {
		t.Fatal("terminal future order projection must be BLOCK_SCOPED")
	}
	for _, key := range []string{
		positionKey,
		keys.KeyFuturePositionHistoryOwnerIndex(position.Owner, position.Status, position.CloseHeight, position.PositionId),
		keys.KeyFuturePositionHistoryPairIndex(position.Pair, position.Status, position.CloseHeight, position.PositionId),
		keys.KeyFuturePositionHistoryRecentIndex(position.Status, position.CloseHeight, position.PositionId),
	} {
		if got[key].Retention != pb.ReadModelRetention_READ_MODEL_RETENTION_BLOCK_SCOPED {
			t.Fatalf("terminal future position mutation %q must be BLOCK_SCOPED", key)
		}
	}
	for _, key := range []string{
		keys.KeyFuturePositionHistoryOwnerIndex(position.Owner, position.Status, position.CloseHeight, position.PositionId),
		keys.KeyFuturePositionHistoryPairIndex(position.Pair, position.Status, position.CloseHeight, position.PositionId),
		keys.KeyFuturePositionHistoryRecentIndex(position.Status, position.CloseHeight, position.PositionId),
	} {
		if string(got[key].Value) != position.PositionId {
			t.Fatalf("terminal future position index %q ref=%q", key, got[key].Value)
		}
	}
}

func TestBuildBlockReadModelDerivesRecentDeleteFromPrimaryKey(t *testing.T) {
	old := &pb.FutureOrderState{
		OrderId: "future-1", OwnerAddress: "alice", Pair: "FB_USDT",
		Status: pb.FutureOrderStatus_FUTURE_ORDER_OPEN, CreateHeight: 4,
	}
	oldKey := keys.KeyFutureAccountOrderFromState(old)
	model, err := buildBlockReadModel(&pb.Block{
		BlockHash: "block-8", Header: &pb.BlockHeader{Height: 8, PrevBlockHash: "block-7"},
	}, []WriteOp{{Key: oldKey, Del: true}})
	if err != nil {
		t.Fatalf("build read model: %v", err)
	}
	got := readModelMutationMap(model)
	recent := got[keys.KeyFutureAccountOrderRecentFromState(old)]
	if recent == nil || recent.Op != pb.ReadModelMutationOp_READ_MODEL_MUTATION_DELETE {
		t.Fatalf("missing derived recent DELETE: %#v", recent)
	}
}

func TestBuildBlockReadModelRejectsFuturePositionHistoryDelete(t *testing.T) {
	key := keys.KeyFuturePositionHistory("position-1")
	_, err := buildBlockReadModel(&pb.Block{
		BlockHash: "block-9", Header: &pb.BlockHeader{Height: 9, PrevBlockHash: "block-8"},
	}, []WriteOp{{Key: key, Del: true}})
	if err == nil {
		t.Fatal("append-only future position history delete must fail")
	}
}

func TestReadModelDiffSeparation(t *testing.T) {
	activity := WriteOp{Key: keys.KeyUserActivity("alice", 1, 0), Value: []byte("activity")}
	spot := WriteOp{Key: keys.KeySpotAccountOrder("alice", "FB_USDT", pb.OrderStateStatus_ORDER_OPEN, 1, "s1"), Value: []byte("spot")}
	future := WriteOp{Key: keys.KeyFutureAccountOrder("alice", "FB_USDT", pb.FutureOrderStatus_FUTURE_ORDER_OPEN, 1, "f1"), Value: []byte("future")}
	positionHistory := WriteOp{Key: keys.KeyFuturePositionHistory("p1"), Value: []byte("position")}
	state := WriteOp{Key: keys.KeyAccount("alice"), Value: []byte("state")}
	diff := []WriteOp{activity, future, spot, positionHistory, state}

	node := finalizedNodeDiff(diff)
	if len(node) != 2 || node[0].Key != future.Key || node[1].Key != state.Key {
		t.Fatalf("unexpected node diff: %#v", node)
	}
	audit := conservationAuditDiff(diff)
	if len(audit) != 1 || audit[0].Key != state.Key {
		t.Fatalf("unexpected audit diff: %#v", audit)
	}
}

func readModelMutationMap(model *pb.BlockReadModel) map[string]*pb.ReadModelMutation {
	out := make(map[string]*pb.ReadModelMutation, len(model.Mutations))
	for _, mutation := range model.Mutations {
		out[mutation.Key] = mutation
	}
	return out
}
