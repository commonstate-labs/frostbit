package vm_test

import (
	"errors"
	"math"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"dex/formal/itftrace"
	"dex/formal/refinement"
	"dex/keys"
	"dex/pb"
	"dex/vm"

	"google.golang.org/protobuf/proto"
)

const (
	formalNonceFrom           = "formal_nonce_alice"
	formalNonceRecipient      = "formal_nonce_bob"
	formalBusinessMarker      = "formal_nonce_business_marker"
	formalProtocolFirstMarker = "formal_nonce_protocol_first_marker"
	formalProtocolAbortMarker = "formal_nonce_protocol_abort_marker"
)

type formalBusinessRollbackHandler struct{}

func (formalBusinessRollbackHandler) Kind() string { return "transfer" }

func (formalBusinessRollbackHandler) DryRun(tx *pb.AnyTx, sv vm.StateView) ([]vm.WriteOp, *vm.Receipt, error) {
	sv.Set(formalBusinessMarker, []byte("must-be-reverted"))
	err := errors.New("formal business rejection")
	return nil, &vm.Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: err.Error()}, err
}

func (formalBusinessRollbackHandler) Apply(*pb.AnyTx) error { return vm.ErrNotImplemented }

type formalProtocolRollbackHandler struct{}

func (formalProtocolRollbackHandler) Kind() string { return "transfer" }

func (formalProtocolRollbackHandler) DryRun(tx *pb.AnyTx, sv vm.StateView) ([]vm.WriteOp, *vm.Receipt, error) {
	if strings.Contains(tx.GetTxId(), "abort") {
		sv.Set(formalProtocolAbortMarker, []byte("must-be-reverted"))
		return nil, nil, errors.New("formal protocol rejection")
	}
	sv.Set(formalProtocolFirstMarker, []byte("must-be-reverted"))
	return nil, &vm.Receipt{TxID: tx.GetTxId(), Status: "SUCCEED"}, nil
}

func (formalProtocolRollbackHandler) Apply(*pb.AnyTx) error { return vm.ErrNotImplemented }

type formalNonceHarness struct {
	db         *MockDB
	executor   *vm.Executor
	height     uint64
	parent     string
	total      int64
	succeeded  int64
	failed     int64
	lastFee    int64
	blockValid bool
	outcome    string
}

func TestFormalAccountNonceExecutionITFDifferential(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want int
	}{
		{name: "ordered-mismatch", env: "DEX_FORMAL_ITF_VM_NONCE_ORDERED", want: 6},
		{name: "fee-failure", env: "DEX_FORMAL_ITF_VM_NONCE_FEE", want: 4},
		{name: "business-failure", env: "DEX_FORMAL_ITF_VM_NONCE_BUSINESS", want: 3},
		{name: "protocol-rollback", env: "DEX_FORMAL_ITF_VM_NONCE_PROTOCOL", want: 3},
		{name: "overflow-rollback", env: "DEX_FORMAL_ITF_VM_NONCE_OVERFLOW", want: 3},
	}
	if os.Getenv(cases[0].env) == "" {
		t.Skip("VM nonce ITF paths are set by formal/run-go-differential.ps1")
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runFormalAccountNonceExecutionTrace(t, os.Getenv(tc.env), tc.want)
		})
	}
}

func runFormalAccountNonceExecutionTrace(t *testing.T, path string, wantStates int) {
	t.Helper()
	trace, err := itftrace.Load(path)
	if err != nil {
		t.Fatalf("load Quint VM nonce ITF trace: %v", err)
	}
	if len(trace.States) != wantStates {
		t.Fatalf("VM nonce differential trace states=%d want=%d", len(trace.States), wantStates)
	}
	initial, err := refinement.AccountNonceExecutionFromITF(trace.States[0])
	if err != nil {
		t.Fatal(err)
	}
	h := newFormalNonceHarness(t, initial)
	h.assertStateRelation(t, trace.States[0], 0)
	for i := 1; i < len(trace.States); i++ {
		before, err := refinement.AccountNonceExecutionFromITF(trace.States[i-1])
		if err != nil {
			t.Fatal(err)
		}
		after, err := refinement.AccountNonceExecutionFromITF(trace.States[i])
		if err != nil {
			t.Fatal(err)
		}
		h.applyModelStep(t, before, after)
		h.assertStateRelation(t, trace.States[i], i)
	}
}

func newFormalNonceHarness(t *testing.T, initial refinement.AccountNonceExecutionState) *formalNonceHarness {
	t.Helper()
	db := NewMockDB()
	productionNonce := uint64(initial.Nonce)
	if initial.Phase == "OverflowStart" {
		productionNonce = math.MaxUint64
	}
	accountBytes, err := proto.Marshal(&pb.Account{Address: formalNonceFrom, Nonce: productionNonce})
	if err != nil {
		t.Fatal(err)
	}
	balanceBytes, err := proto.Marshal(&pb.TokenBalanceRecord{
		Address: formalNonceFrom,
		Token:   vm.FeeToken,
		Balance: &pb.TokenBalance{Balance: strconv.FormatInt(initial.Balance, 10)},
	})
	if err != nil {
		t.Fatal(err)
	}
	db.data[keys.KeyAccount(formalNonceFrom)] = accountBytes
	db.data[keys.KeyBalance(formalNonceFrom, vm.FeeToken)] = balanceBytes

	registry := vm.NewHandlerRegistry()
	switch initial.Phase {
	case "BusinessStart":
		if err := registry.Register(formalBusinessRollbackHandler{}); err != nil {
			t.Fatal(err)
		}
	case "ProtocolStart":
		if err := registry.Register(formalProtocolRollbackHandler{}); err != nil {
			t.Fatal(err)
		}
	default:
		if err := vm.RegisterDefaultHandlers(registry); err != nil {
			t.Fatal(err)
		}
	}
	return &formalNonceHarness{
		db:         db,
		executor:   vm.NewExecutor(db, registry, vm.NewSpecExecLRU(16)),
		height:     1,
		parent:     "formal_nonce_genesis",
		total:      initial.Total,
		blockValid: true,
		outcome:    "NoOutcome",
	}
}

func (h *formalNonceHarness) applyModelStep(t *testing.T, before, after refinement.AccountNonceExecutionState) {
	t.Helper()
	if reflect.DeepEqual(before, after) {
		return
	}
	switch before.Phase {
	case "OrderedStart":
		h.commitSingle(t, formalNonceTransfer("ordered-0", 0, "1000"), "")
	case "OrderedOne":
		h.commitSingle(t, formalNonceTransfer("ordered-stale", 0, "1000"), "")
	case "OrderedStale":
		h.commitSingle(t, formalNonceTransfer("ordered-future", 2, "1000"), "")
	case "OrderedFuture":
		h.commitSingle(t, formalNonceTransfer("ordered-1", 1, "1000"), "")
	case "FeeStart":
		h.commitSingle(t, formalNonceTransfer("fee-failure-0", 0, "5000"), "")
	case "FeeFailed":
		h.commitSingle(t, formalNonceTransfer("fee-success-1", 1, "1000"), "")
	case "BusinessStart":
		h.commitSingle(t, formalNonceTransfer("business-failure-0", 0, "1000"), "")
	case "ProtocolStart":
		h.commitProtocolRollbackBlock(t)
	case "OverflowStart":
		h.commitSingle(t, formalNonceTransfer("overflow", math.MaxUint64, "1000"), "nonce overflow")
	default:
		t.Fatalf("unsupported VM nonce model edge: before=%+v after=%+v", before, after)
	}
}

func (h *formalNonceHarness) commitSingle(t *testing.T, tx *pb.AnyTx, wantError string) {
	t.Helper()
	block := h.nextBlock([]*pb.AnyTx{tx})
	err := h.executor.CommitFinalizedBlock(block)
	if wantError != "" {
		if err == nil || !strings.Contains(err.Error(), wantError) {
			t.Fatalf("commit %s error=%v, want substring %q", tx.GetTxId(), err, wantError)
		}
		h.blockValid = false
		h.outcome = "BlockRejected"
		h.lastFee = 0
		return
	}
	if err != nil {
		t.Fatalf("commit %s: %v", tx.GetTxId(), err)
	}
	h.parent = block.BlockHash
	h.height++
	receipt := h.executor.TakeCommittedReceipts(block.BlockHash)[tx.GetTxId()]
	if receipt == nil {
		t.Fatalf("missing receipt for %s", tx.GetTxId())
	}
	fee, err := strconv.ParseInt(receipt.Fee, 10, 64)
	if err != nil {
		t.Fatalf("receipt %s fee %q: %v", tx.GetTxId(), receipt.Fee, err)
	}
	h.blockValid = true
	h.lastFee = fee
	switch receipt.Status {
	case "SUCCEED":
		h.succeeded++
		h.outcome = "Succeeded"
	case "FAILED":
		h.failed++
		h.outcome = "Failed"
	default:
		t.Fatalf("unexpected receipt status for %s: %+v", tx.GetTxId(), receipt)
	}
}

func (h *formalNonceHarness) commitProtocolRollbackBlock(t *testing.T) {
	t.Helper()
	block := h.nextBlock([]*pb.AnyTx{
		formalNonceTransfer("protocol-first", 0, "1000"),
		formalNonceTransfer("protocol-abort", 1, "1000"),
	})
	err := h.executor.CommitFinalizedBlock(block)
	if err == nil || !strings.Contains(err.Error(), "formal protocol rejection") {
		t.Fatalf("protocol-error block should be rejected, got %v", err)
	}
	h.blockValid = false
	h.outcome = "BlockRejected"
	h.lastFee = 0
}

func (h *formalNonceHarness) nextBlock(body []*pb.AnyTx) *pb.Block {
	return &pb.Block{
		BlockHash: "formal_nonce_block_" + strconv.FormatUint(h.height, 10),
		Header: &pb.BlockHeader{
			PrevBlockHash: h.parent,
			Height:        h.height,
			Timestamp:     int64(h.height),
		},
		Body: body,
	}
}

func (h *formalNonceHarness) assertStateRelation(t *testing.T, model itftrace.State, step int) {
	t.Helper()
	expected, err := refinement.AccountNonceExecutionFromITF(model)
	if err != nil {
		t.Fatal(err)
	}
	actual := h.productionState(t, expected.Phase)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("VM nonce state relation failed step=%d action=%s\nactual:   %+v\nexpected: %+v",
			step, model.Action, actual, expected)
	}
}

func (h *formalNonceHarness) productionState(t *testing.T, phase string) refinement.AccountNonceExecutionState {
	t.Helper()
	accountRaw, err := h.db.Get(keys.KeyAccount(formalNonceFrom))
	if err != nil {
		t.Fatal(err)
	}
	var account pb.Account
	if err := proto.Unmarshal(accountRaw, &account); err != nil {
		t.Fatal(err)
	}
	nonce := int64(account.Nonce)
	if account.Nonce == math.MaxUint64 {
		nonce = 3
	}
	marker := false
	for _, key := range []string{formalBusinessMarker, formalProtocolFirstMarker, formalProtocolAbortMarker} {
		value, err := h.db.Get(key)
		if err != nil {
			t.Fatal(err)
		}
		marker = marker || len(value) != 0
	}
	return refinement.AccountNonceExecutionState{
		Phase:      phase,
		Nonce:      nonce,
		Balance:    formalNonceBalance(t, h.db, formalNonceFrom),
		FeePool:    formalNonceBalance(t, h.db, vm.RuntimeFeePoolAccount),
		Recipient:  formalNonceBalance(t, h.db, formalNonceRecipient),
		Total:      h.total,
		Marker:     marker,
		BlockValid: h.blockValid,
		Succeeded:  h.succeeded,
		Failed:     h.failed,
		LastFee:    h.lastFee,
		Outcome:    h.outcome,
	}
}

func formalNonceTransfer(id string, nonce uint64, fee string) *pb.AnyTx {
	return &pb.AnyTx{Content: &pb.AnyTx_Transaction{Transaction: &pb.Transaction{
		Base: &pb.BaseMessage{
			TxId:        "formal_nonce_" + id,
			FromAddress: formalNonceFrom,
			Nonce:       nonce,
			Fee:         fee,
			Status:      pb.Status_PENDING,
		},
		To:           formalNonceRecipient,
		TokenAddress: vm.FeeToken,
		Amount:       "100",
	}}}
}

func formalNonceBalance(t *testing.T, db *MockDB, owner string) int64 {
	t.Helper()
	raw, err := db.Get(keys.KeyBalance(owner, vm.FeeToken))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 {
		return 0
	}
	var record pb.TokenBalanceRecord
	if err := proto.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	if record.Balance == nil || record.Balance.Balance == "" {
		return 0
	}
	value, err := strconv.ParseInt(record.Balance.Balance, 10, 64)
	if err != nil {
		t.Fatalf("balance %s=%q: %v", owner, record.Balance.Balance, err)
	}
	return value
}
