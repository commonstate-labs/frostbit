package vm_test

import (
	"dex/keys"
	"dex/pb"
	"dex/rent"
	"dex/vm"
	"encoding/json"
	"errors"
	"testing"

	"google.golang.org/protobuf/proto"
)

type rentPriorityProtocolHandler struct {
	kind               string
	owner              string
	ownerAvailable     string
	recycleFundBalance string
}

func (h *rentPriorityProtocolHandler) Kind() string { return h.kind }

func (h *rentPriorityProtocolHandler) DryRun(tx *pb.AnyTx, sv vm.StateView) ([]vm.WriteOp, *vm.Receipt, error) {
	h.ownerAvailable = balanceAvailable(vm.GetBalance(sv, h.owner, vm.FeeToken))
	h.recycleFundBalance = balanceAvailable(vm.GetBalance(sv, rent.RecycleFundAccount, vm.FeeToken))
	err := errors.New("runtime validation failed")
	return nil, &vm.Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: err.Error()}, err
}

func (h *rentPriorityProtocolHandler) Apply(*pb.AnyTx) error { return vm.ErrNotImplemented }

func balanceAvailable(balance *pb.TokenBalance) string {
	if balance == nil || balance.Balance == "" {
		return "0"
	}
	return balance.Balance
}

func seedRentBarrierAccount(t *testing.T, db *MockDB, owner, balance string, nonce uint64) {
	t.Helper()
	account, err := proto.Marshal(&pb.Account{Address: owner, Nonce: nonce})
	if err != nil {
		t.Fatal(err)
	}
	balanceData, err := proto.Marshal(&pb.TokenBalanceRecord{
		Address: owner, Token: vm.FeeToken, Balance: &pb.TokenBalance{Balance: balance},
	})
	if err != nil {
		t.Fatal(err)
	}
	state := rent.RentState{
		Version: rent.RentStateVersion, Owner: owner, LastAccruedHeight: 1,
		Mode: rent.RentModeCharging, ExpireHeight: 10, NominalFB: balance,
		Work: &rent.RentWork{NextActionHeight: 10, Phase: rent.RentWorkSettle},
	}
	stateData, err := json.Marshal(&state)
	if err != nil {
		t.Fatal(err)
	}
	db.data[keys.KeyAccount(owner)] = account
	db.data[keys.KeyBalance(owner, vm.FeeToken)] = balanceData
	db.data[keys.KeyRentState(owner)] = stateData
	db.data[keys.KeyRentExpiry(10, owner)] = []byte(owner)
}

func rentBarrierTransfer(id, owner string, nonce uint64, fee, amount string) *pb.AnyTx {
	return &pb.AnyTx{Content: &pb.AnyTx_Transaction{Transaction: &pb.Transaction{
		Base: &pb.BaseMessage{TxId: id, FromAddress: owner, Nonce: nonce, Fee: fee, Status: pb.Status_PENDING},
		To:   "rent-barrier-recipient", TokenAddress: vm.FeeToken, Amount: amount,
	}}}
}

func newRentBarrierExecutor(t *testing.T, db *MockDB) *vm.Executor {
	t.Helper()
	registry := vm.NewHandlerRegistry()
	if err := vm.RegisterDefaultHandlers(registry); err != nil {
		t.Fatal(err)
	}
	return vm.NewExecutor(db, registry, vm.NewSpecExecLRU(16))
}

func TestRentBarrierPersistsBeforeFeeFailureAndConsumesNonce(t *testing.T) {
	db := NewMockDB()
	owner := "fbc1qrentbarrierfee"
	seedRentBarrierAccount(t, db, owner, "600", 0)
	executor := newRentBarrierExecutor(t, db)
	tx := rentBarrierTransfer("rent-before-fee", owner, 0, "5000", "1")
	block := &pb.Block{BlockHash: "block-rent-before-fee", Header: &pb.BlockHeader{Height: 6}, Body: []*pb.AnyTx{tx}}
	if err := executor.CommitFinalizedBlock(block); err != nil {
		t.Fatal(err)
	}
	receipt := executor.TakeCommittedReceipts(block.BlockHash)[tx.GetTxId()]
	if receipt == nil || receipt.Status != "FAILED" || receipt.Fee != "0" {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	if got := readSeparatedBalance(t, db, owner, vm.FeeToken); got != "100" {
		t.Fatalf("sender balance=%s want=100", got)
	}
	if got := readSeparatedBalance(t, db, rent.RecycleFundAccount, vm.FeeToken); got != "500" {
		t.Fatalf("rent fund=%s want=500", got)
	}
	accountRaw, err := db.Get(keys.KeyAccount(owner))
	if err != nil {
		t.Fatal(err)
	}
	var account pb.Account
	if err := proto.Unmarshal(accountRaw, &account); err != nil {
		t.Fatal(err)
	}
	if account.Nonce != 1 {
		t.Fatalf("nonce=%d want=1", account.Nonce)
	}
}

func TestRentBarrierPreventsTransferAtOriginalExpiryExample(t *testing.T) {
	db := NewMockDB()
	owner := "fbc1qrentbarrier30422"
	seedRentBarrierAccount(t, db, owner, rent.DefaultActivationFeeRaw, 0)
	delete(db.data, keys.KeyRentExpiry(10, owner))
	state := rent.RentState{
		Version:           rent.RentStateVersion,
		Owner:             owner,
		LastAccruedHeight: 20423,
		Mode:              rent.RentModeCharging,
		ExpireHeight:      30423,
		NominalFB:         rent.DefaultActivationFeeRaw,
		Work:              &rent.RentWork{NextActionHeight: 30423, Phase: rent.RentWorkSettle},
	}
	stateData, err := json.Marshal(&state)
	if err != nil {
		t.Fatal(err)
	}
	db.data[keys.KeyRentState(owner)] = stateData
	db.data[keys.KeyRentExpiry(30423, owner)] = []byte(owner)

	executor := newRentBarrierExecutor(t, db)
	tx := rentBarrierTransfer("rent-expiry-evasion", owner, 0, "1000", rent.DefaultActivationFeeRaw)
	block := &pb.Block{BlockHash: "block-rent-expiry-evasion", Header: &pb.BlockHeader{Height: 30422}, Body: []*pb.AnyTx{tx}}
	if err := executor.CommitFinalizedBlock(block); err != nil {
		t.Fatal(err)
	}
	receipt := executor.TakeCommittedReceipts(block.BlockHash)[tx.GetTxId()]
	if receipt == nil || receipt.Status != "FAILED" || receipt.Fee != "0" {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	if got := readSeparatedBalance(t, db, owner, vm.FeeToken); got != "100" {
		t.Fatalf("sender balance=%s want=100", got)
	}
	if got := readSeparatedBalance(t, db, rent.RecycleFundAccount, vm.FeeToken); got != "999900" {
		t.Fatalf("rent fund=%s want=999900", got)
	}
	if got := readSeparatedBalance(t, db, "rent-barrier-recipient", vm.FeeToken); got != "0" {
		t.Fatalf("expired account transferred FB: %s", got)
	}
}

func TestRentBarrierSurvivesBusinessRollback(t *testing.T) {
	db := NewMockDB()
	owner := "fbc1qrentbarrierbusiness"
	seedRentBarrierAccount(t, db, owner, "2000", 0)
	executor := newRentBarrierExecutor(t, db)
	tx := rentBarrierTransfer("rent-before-business", owner, 0, "1000", "600")
	block := &pb.Block{BlockHash: "block-rent-before-business", Header: &pb.BlockHeader{Height: 6}, Body: []*pb.AnyTx{tx}}
	if err := executor.CommitFinalizedBlock(block); err != nil {
		t.Fatal(err)
	}
	receipt := executor.TakeCommittedReceipts(block.BlockHash)[tx.GetTxId()]
	if receipt == nil || receipt.Status != "FAILED" || receipt.Fee != "1000" {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	if got := readSeparatedBalance(t, db, owner, vm.FeeToken); got != "500" {
		t.Fatalf("sender balance=%s want=500", got)
	}
	if got := readSeparatedBalance(t, db, rent.RecycleFundAccount, vm.FeeToken); got != "500" {
		t.Fatalf("rent fund=%s want=500", got)
	}
	if got := readSeparatedBalance(t, db, vm.RuntimeFeePoolAccount, vm.FeeToken); got != "1000" {
		t.Fatalf("runtime fee pool=%s want=1000", got)
	}
}

func TestProtocolDutySettlesRentBeforeChargingFailedTxFee(t *testing.T) {
	db := NewMockDB()
	owner := "fbc1qrentbarrierprotocol"
	seedRentBarrierAccount(t, db, owner, "1600", 0)
	tx := newRuntimeFeeClaimTx("rent-before-protocol-fee", owner)
	handler := &rentPriorityProtocolHandler{kind: tx.GetKind(), owner: owner}
	registry := vm.NewHandlerRegistry()
	if err := registry.Register(handler); err != nil {
		t.Fatal(err)
	}
	executor := vm.NewExecutor(db, registry, nil)
	block := &pb.Block{
		BlockHash: "block-rent-before-protocol-fee",
		Header:    &pb.BlockHeader{Height: 7},
		Body:      []*pb.AnyTx{tx},
	}
	if err := executor.CommitFinalizedBlock(block); err != nil {
		t.Fatal(err)
	}

	                                                                        
	                                                                         
	                                                                        
	                              
	if handler.ownerAvailable != "0" || handler.recycleFundBalance != "600" {
		t.Fatalf("handler observed owner=%s recycle=%s; want owner=0 recycle=600",
			handler.ownerAvailable, handler.recycleFundBalance)
	}
	receipt := executor.TakeCommittedReceipts(block.BlockHash)[tx.GetTxId()]
	if receipt == nil || receipt.Status != "FAILED" || receipt.Fee != "1000" {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	if got := readSeparatedBalance(t, db, owner, vm.FeeToken); got != "0" {
		t.Fatalf("protocol sender balance=%s want=0", got)
	}
	if got := readSeparatedBalance(t, db, rent.RecycleFundAccount, vm.FeeToken); got != "600" {
		t.Fatalf("rent fund=%s want=600", got)
	}
	if got := readSeparatedBalance(t, db, vm.RuntimeFeePoolAccount, vm.FeeToken); got != "1000" {
		t.Fatalf("runtime fee pool=%s want=1000", got)
	}
	raw, err := db.Get(keys.KeyAccount(owner))
	if err != nil {
		t.Fatal(err)
	}
	var account pb.Account
	if err := proto.Unmarshal(raw, &account); err != nil {
		t.Fatal(err)
	}
	if account.Nonce != 0 {
		t.Fatalf("protocol duty consumed account nonce: %d", account.Nonce)
	}
}

func TestInvalidNonceDoesNotTriggerRentSettlement(t *testing.T) {
	db := NewMockDB()
	owner := "fbc1qrentbarrierinvalidnonce"
	seedRentBarrierAccount(t, db, owner, "600", 1)
	executor := newRentBarrierExecutor(t, db)
	tx := rentBarrierTransfer("rent-invalid-nonce", owner, 0, "1000", "1")
	block := &pb.Block{BlockHash: "block-rent-invalid-nonce", Header: &pb.BlockHeader{Height: 6}, Body: []*pb.AnyTx{tx}}
	if err := executor.CommitFinalizedBlock(block); err != nil {
		t.Fatal(err)
	}
	receipt := executor.TakeCommittedReceipts(block.BlockHash)[tx.GetTxId()]
	if receipt == nil || receipt.Status != "FAILED" || receipt.Fee != "0" {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	if got := readSeparatedBalance(t, db, owner, vm.FeeToken); got != "600" {
		t.Fatalf("invalid nonce changed balance: %s", got)
	}
	if got := readSeparatedBalance(t, db, rent.RecycleFundAccount, vm.FeeToken); got != "0" {
		t.Fatalf("invalid nonce charged rent: %s", got)
	}
	raw, err := db.Get(keys.KeyRentState(owner))
	if err != nil {
		t.Fatal(err)
	}
	var state rent.RentState
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	if state.LastAccruedHeight != 1 {
		t.Fatalf("invalid nonce advanced rent height: %+v", state)
	}
}

func TestSweepingAccountCannotResumeUserTransactions(t *testing.T) {
	db := NewMockDB()
	owner := "fbc1qrentbarriersweeping"
	seedRentBarrierAccount(t, db, owner, "5000", 0)
	state := rent.RentState{
		Version: rent.RentStateVersion, Owner: owner, LastAccruedHeight: 5,
		Mode: rent.RentModeSweeping, ExpireHeight: 5, NominalFB: "5000",
		Work: &rent.RentWork{NextActionHeight: 10, Phase: rent.RentWorkSweepBalances},
	}
	stateData, err := json.Marshal(&state)
	if err != nil {
		t.Fatal(err)
	}
	delete(db.data, keys.KeyRentExpiry(10, owner))
	db.data[keys.KeyRentState(owner)] = stateData
	db.data[keys.KeyRentExpiry(10, owner)] = []byte(owner)

	executor := newRentBarrierExecutor(t, db)
	tx := rentBarrierTransfer("rent-sweeping-blocked", owner, 0, "1000", "1")
	block := &pb.Block{BlockHash: "block-rent-sweeping-blocked", Header: &pb.BlockHeader{Height: 6}, Body: []*pb.AnyTx{tx}}
	if err := executor.CommitFinalizedBlock(block); err != nil {
		t.Fatal(err)
	}
	receipt := executor.TakeCommittedReceipts(block.BlockHash)[tx.GetTxId()]
	if receipt == nil || receipt.Status != "FAILED" || receipt.Fee != "0" || receipt.Error != "account rent cleanup is in progress" {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	if got := readSeparatedBalance(t, db, "rent-barrier-recipient", vm.FeeToken); got != "0" {
		t.Fatalf("blocked sweeping account transferred funds: %s", got)
	}
	if got := readSeparatedBalance(t, db, vm.RuntimeFeePoolAccount, vm.FeeToken); got != "0" {
		t.Fatalf("blocked sweeping account paid runtime fee: %s", got)
	}
	if got := readSeparatedBalance(t, db, rent.RecycleFundAccount, vm.FeeToken); got != "5000" {
		t.Fatalf("bounded cleanup did not recycle remaining FB: %s", got)
	}
}

func readSeparatedBalance(t *testing.T, db *MockDB, owner, token string) string {
	t.Helper()
	raw, err := db.Get(keys.KeyBalance(owner, token))
	if err != nil || len(raw) == 0 {
		return "0"
	}
	var record pb.TokenBalanceRecord
	if err := proto.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	if record.Balance == nil || record.Balance.Balance == "" {
		return "0"
	}
	return record.Balance.Balance
}
