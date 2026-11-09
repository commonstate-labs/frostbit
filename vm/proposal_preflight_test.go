package vm_test

import (
	"bytes"
	"dex/db"
	"dex/keys"
	"dex/pb"
	"dex/utils"
	"dex/vm"
	"dex/witness"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
)

type proposalPreflightCacheSpy struct {
	gets int
	puts int
}

func (c *proposalPreflightCacheSpy) Get(string) (*vm.SpecResult, bool) {
	c.gets++
	return nil, false
}

func (c *proposalPreflightCacheSpy) Put(*vm.SpecResult) { c.puts++ }
func (c *proposalPreflightCacheSpy) EvictBelow(uint64)  {}

type proposalPreflightTestHandler struct{}

func (proposalPreflightTestHandler) Kind() string { return "proposal_preflight_test" }

func (proposalPreflightTestHandler) DryRun(tx *pb.AnyTx, _ vm.StateView) ([]vm.WriteOp, *vm.Receipt, error) {
	return []vm.WriteOp{{
			Key:      "proposal_preflight_test_state",
			Value:    []byte("changed-in-overlay-only"),
			Category: "test",
		}}, &vm.Receipt{
			TxID:   tx.GetTxId(),
			Status: "SUCCEED",
		}, nil
}

func (proposalPreflightTestHandler) Apply(*pb.AnyTx) error { return vm.ErrNotImplemented }

type proposalPreflightHotReadHandler struct{ key string }

func (*proposalPreflightHotReadHandler) Kind() string { return "proposal_preflight_hot_read" }

func (h *proposalPreflightHotReadHandler) DryRun(tx *pb.AnyTx, sv vm.StateView) ([]vm.WriteOp, *vm.Receipt, error) {
	if _, _, err := sv.Get(h.key); err != nil {
		return nil, nil, err
	}
	return nil, &vm.Receipt{TxID: tx.GetTxId(), Status: "SUCCEED"}, nil
}

func (*proposalPreflightHotReadHandler) Apply(*pb.AnyTx) error { return vm.ErrNotImplemented }

type proposalPreflightWitnessHandler struct {
	service *witness.Service
}

func (*proposalPreflightWitnessHandler) Kind() string { return "proposal_preflight_witness" }

func (h *proposalPreflightWitnessHandler) SetWitnessService(service *witness.Service) {
	h.service = service
}

func (h *proposalPreflightWitnessHandler) DryRun(tx *pb.AnyTx, _ vm.StateView) ([]vm.WriteOp, *vm.Receipt, error) {
	h.service.LoadWitness(&pb.WitnessInfo{
		Address:     "speculative-witness",
		StakeAmount: "1000",
		Status:      pb.WitnessStatus_WITNESS_ACTIVE,
	})
	return nil, &vm.Receipt{TxID: tx.GetTxId(), Status: "SUCCEED"}, nil
}

func (*proposalPreflightWitnessHandler) Apply(*pb.AnyTx) error { return vm.ErrNotImplemented }

type proposalSpotProjectionHandler struct{}

func (proposalSpotProjectionHandler) Kind() string { return "proposal_spot_projection_test" }

func (proposalSpotProjectionHandler) DryRun(tx *pb.AnyTx, sv vm.StateView) ([]vm.WriteOp, *vm.Receipt, error) {
	order := tx.GetOrderTx()
	maker := &pb.OrderState{
		OrderId:    "maker-created-by-public-prefix",
		Owner:      "external-maker",
		BaseToken:  order.BaseToken,
		QuoteToken: order.QuoteToken,
		Side:       pb.OrderSide_SELL,
		Price:      "100000000",
		Amount:     "11",
		FilledBase: "3",
		Status:     pb.OrderStateStatus_ORDER_OPEN,
	}
	data, err := proto.Marshal(maker)
	if err != nil {
		return nil, nil, err
	}
	priceKey, err := db.PriceToKey128(maker.Price)
	if err != nil {
		return nil, nil, err
	}
	pair := utils.GeneratePairKey(order.BaseToken, order.QuoteToken)
	sv.Set(keys.KeyOrderState(maker.OrderId), data)
	sv.Set(keys.KeyOrderPriceIndex(pair, maker.Side, false, priceKey, maker.OrderId), []byte{1})
	return nil, &vm.Receipt{TxID: tx.GetTxId(), Status: "SUCCEED"}, nil
}

func (proposalSpotProjectionHandler) Apply(*pb.AnyTx) error { return vm.ErrNotImplemented }

func TestBuildProposalSpecResultClonesInputAndDoesNotPersistOrCache(t *testing.T) {
	db := NewMockDB()
	registry := vm.NewHandlerRegistry()
	if err := registry.Register(proposalPreflightTestHandler{}); err != nil {
		t.Fatalf("register handler: %v", err)
	}
	cache := &proposalPreflightCacheSpy{}
	executor := vm.NewExecutor(db, registry, cache)
	executor.KFn = func(*pb.AnyTx) (string, error) { return "proposal_preflight_test", nil }

	base := &pb.BaseMessage{TxId: "private-order", ExecutedHeight: 0}
	block := &pb.Block{
		BlockHash: "proposal-1",
		Header: &pb.BlockHeader{
			Height:        7,
			PrevBlockHash: "parent-6",
		},
		Body: []*pb.AnyTx{{Content: &pb.AnyTx_CertSignTx{CertSignTx: &pb.CertSignTx{Base: base}}}},
	}

	result, err := executor.BuildProposalSpecResult(block)
	if err != nil {
		t.Fatalf("BuildProposalSpecResult() error = %v", err)
	}
	if result == nil || !result.Valid {
		t.Fatalf("BuildProposalSpecResult() result = %+v", result)
	}
	if base.ExecutedHeight != 0 {
		t.Fatalf("caller-owned transaction mutated: executed_height = %d", base.ExecutedHeight)
	}
	if value, err := db.Get("proposal_preflight_test_state"); err != nil || value != nil {
		t.Fatalf("proposal state persisted: value=%q err=%v", value, err)
	}
	db.mu.RLock()
	stateSyncCalls := db.stateSyncCalls
	db.mu.RUnlock()
	if stateSyncCalls != 0 {
		t.Fatalf("proposal simulation invoked finalized state sync %d times", stateSyncCalls)
	}
	if cache.gets != 0 || cache.puts != 0 {
		t.Fatalf("proposal simulation touched spec cache: gets=%d puts=%d", cache.gets, cache.puts)
	}
	foundOverlayWrite := false
	for _, op := range result.Diff {
		if op.Key == "proposal_preflight_test_state" && bytes.Equal(op.Value, []byte("changed-in-overlay-only")) {
			foundOverlayWrite = true
			break
		}
	}
	if !foundOverlayWrite {
		t.Fatalf("proposal simulation omitted overlay write: %+v", result.Diff)
	}
}

func TestBuildProposalSpecResultSharesFinalizationMutex(t *testing.T) {
	executor := vm.NewExecutor(NewMockDB(), vm.NewHandlerRegistry(), vm.NewSpecExecLRU(1))
	block := &pb.Block{
		BlockHash: "proposal-lock",
		Header:    &pb.BlockHeader{Height: 1, PrevBlockHash: "genesis"},
	}

	executor.LockForSnapshotSwitch()
	locked := true
	defer func() {
		if locked {
			executor.UnlockForSnapshotSwitch()
		}
	}()

	done := make(chan error, 1)
	go func() {
		_, err := executor.BuildProposalSpecResult(block)
		done <- err
	}()

	select {
	case err := <-done:
		t.Fatalf("proposal execution bypassed executor mutex: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	executor.UnlockForSnapshotSwitch()
	locked = false
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("BuildProposalSpecResult() after unlock error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("proposal execution remained blocked after executor mutex was released")
	}
}

func TestBuildProposalSpecResultIsolatesAndRestoresWitnessService(t *testing.T) {
	live := witness.NewService(witness.DefaultConfig())
	defer live.Stop()
	handler := &proposalPreflightWitnessHandler{service: live}
	registry := vm.NewHandlerRegistry()
	if err := registry.Register(handler); err != nil {
		t.Fatalf("register handler: %v", err)
	}
	executor := vm.NewExecutorWithWitnessService(NewMockDB(), registry, vm.NewSpecExecLRU(1), live)
	executor.KFn = func(*pb.AnyTx) (string, error) { return handler.Kind(), nil }

	block := &pb.Block{
		BlockHash: "proposal-witness-isolation",
		Header:    &pb.BlockHeader{Height: 1, PrevBlockHash: "genesis"},
		Body: []*pb.AnyTx{{Content: &pb.AnyTx_CertSignTx{CertSignTx: &pb.CertSignTx{
			Base: &pb.BaseMessage{TxId: "speculative-witness-tx"},
		}}}},
	}
	result, err := executor.BuildProposalSpecResult(block)
	if err != nil || result == nil || !result.Valid {
		t.Fatalf("BuildProposalSpecResult() result=%+v err=%v", result, err)
	}
	if _, err := live.GetWitnessInfo("speculative-witness"); err == nil {
		t.Fatal("proposal simulation leaked witness cache mutation into live service")
	}
	if handler.service != live {
		t.Fatal("proposal simulation did not restore handler witness dependency")
	}
}

func TestBuildProposalSpecResultDoesNotPopulateLiveHotState(t *testing.T) {
	database := NewMockDB()
	hotKey := keys.KeyOrderState("speculative-only-order")
	database.mu.Lock()
	database.data[hotKey] = []byte("speculative-value")
	database.mu.Unlock()

	handler := &proposalPreflightHotReadHandler{key: hotKey}
	registry := vm.NewHandlerRegistry()
	if err := registry.Register(handler); err != nil {
		t.Fatalf("register hot-read handler: %v", err)
	}
	executor := vm.NewExecutor(database, registry, vm.NewSpecExecLRU(1))
	executor.KFn = func(*pb.AnyTx) (string, error) { return handler.Kind(), nil }
	liveHotState := executor.HotState
	block := &pb.Block{
		BlockHash: "proposal-hot-state-isolation",
		Header:    &pb.BlockHeader{Height: 1, PrevBlockHash: "genesis"},
		Body: []*pb.AnyTx{{Content: &pb.AnyTx_CertSignTx{CertSignTx: &pb.CertSignTx{
			Base: &pb.BaseMessage{TxId: "speculative-hot-read"},
		}}}},
	}

	result, err := executor.BuildProposalSpecResult(block)
	if err != nil || result == nil || !result.Valid {
		t.Fatalf("BuildProposalSpecResult() result=%+v err=%v", result, err)
	}
	if executor.HotState != liveHotState {
		t.Fatal("proposal simulation did not restore the live HotState pointer")
	}
	loads := 0
	value, ok, err := liveHotState.Get(hotKey, func(string) ([]byte, error) {
		loads++
		return []byte("live-load"), nil
	})
	if err != nil || !ok || string(value) != "live-load" || loads != 1 {
		t.Fatalf("live HotState was polluted by proposal: value=%q ok=%v loads=%d err=%v", value, ok, loads, err)
	}
}

func TestBuildProposalSpotProjectionCapturesSamePostTransactionStateView(t *testing.T) {
	registry := vm.NewHandlerRegistry()
	handler := proposalSpotProjectionHandler{}
	if err := registry.Register(handler); err != nil {
		t.Fatalf("register projection handler: %v", err)
	}
	executor := vm.NewExecutor(NewMockDB(), registry, vm.NewSpecExecLRU(1))
	executor.KFn = func(*pb.AnyTx) (string, error) { return handler.Kind(), nil }

	base := &pb.BaseMessage{TxId: "public-add"}
	block := &pb.Block{
		Header: &pb.BlockHeader{
			Height:        9,
			PrevBlockHash: "parent-8",
		},
		Body: []*pb.AnyTx{{Content: &pb.AnyTx_OrderTx{OrderTx: &pb.OrderTx{
			Base: base, Op: pb.OrderOp_ADD, BaseToken: "TOKEN_A", QuoteToken: "USD_T", Side: pb.OrderSide_BUY,
		}}}},
	}

	projection, err := executor.BuildProposalSpotProjection(block, 4, "arbitrage-account")
	if err != nil {
		t.Fatalf("BuildProposalSpotProjection() error = %v", err)
	}
	if base.ExecutedHeight != 0 {
		t.Fatalf("caller-owned public prefix mutated: executed_height=%d", base.ExecutedHeight)
	}
	if projection == nil || len(projection.Levels) != 1 {
		t.Fatalf("projection = %+v", projection)
	}
	level := projection.Levels[0]
	if level.BaseToken != "TOKEN_A" || level.QuoteToken != "USD_T" || level.TakerSide != pb.OrderSide_BUY || level.Price != "100000000" {
		t.Fatalf("projected level = %+v", level)
	}
	if len(level.Makers) != 1 || level.Makers[0].OrderID != "maker-created-by-public-prefix" || level.Makers[0].Remaining != "8" {
		t.Fatalf("projected makers = %+v", level.Makers)
	}
}
