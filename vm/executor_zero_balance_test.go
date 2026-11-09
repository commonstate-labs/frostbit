package vm_test

import (
	"testing"
	"time"

	"dex/db"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"dex/vm"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

                                                                          
                                                                               
type zeroBalanceTransferHandler struct {
	mode string
}

func (h zeroBalanceTransferHandler) Kind() string          { return "transfer" }
func (h zeroBalanceTransferHandler) Apply(*pb.AnyTx) error { return vm.ErrNotImplemented }
func (h zeroBalanceTransferHandler) DryRun(tx *pb.AnyTx, sv vm.StateView) ([]vm.WriteOp, *vm.Receipt, error) {
	snapshot := sv.Snapshot()
	ws, rc, err := (&vm.TransferTxHandler{}).DryRun(tx, sv)
	if err != nil {
		return ws, rc, err
	}
	if h.mode == "returned_writes" {
		if err := sv.Revert(snapshot); err != nil {
			return nil, nil, err
		}
		return ws, rc, nil
	}
	for _, op := range ws {
		if op.Del {
			sv.Del(op.Key)
		} else {
			sv.Set(op.Key, op.Value)
		}
	}
	return nil, rc, nil
}

func zeroBalanceTransfer(txID, token, amount string) *pb.AnyTx {
	                                                                                
	return &pb.AnyTx{Content: &pb.AnyTx_Transaction{Transaction: &pb.Transaction{
		Base: &pb.BaseMessage{TxId: txID, FromAddress: "zero-balance-sender", Fee: "1000"},
		To:   "zero-balance-receiver", TokenAddress: token, Amount: amount,
	}}}
}

func TestExecutorZeroBalanceTransfer(t *testing.T) {
	for _, mode := range []string{"default_handler", "returned_writes", "direct_writes"} {
		for _, tc := range []struct {
			name, initialFB, token, amount, wantFB, wantReceived, wantStatus string
		}{
			{"exact_drain", "6000", "FB", "5000", "0", "5000", "SUCCEED"},
			{"remaining_balance", "6001", "FB", "5000", "1", "5000", "SUCCEED"},
			{"fee_drains_fb", "1000", "ETH.USDT", "5", "0", "5", "SUCCEED"},
			{"business_failure", "6000", "FB", "5001", "5000", "0", "FAILED"},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				store := NewMockDB()
				seedRuntimeFeeAccount(t, store, "zero-balance-sender", tc.initialFB)
				asset, err := proto.Marshal(&pb.TokenBalanceRecord{
					Address: "zero-balance-sender", Token: "ETH.USDT", Balance: &pb.TokenBalance{Balance: "10"},
				})
				require.NoError(t, err)
				store.data[keys.KeyBalance("zero-balance-sender", "ETH.USDT")] = asset
				reg := vm.NewHandlerRegistry()
				if mode == "default_handler" {
					require.NoError(t, reg.Register(&vm.TransferTxHandler{}))
				} else {
					require.NoError(t, reg.Register(zeroBalanceTransferHandler{mode: mode}))
				}
				executor := vm.NewExecutor(store, reg, nil)
				tx := zeroBalanceTransfer("tx-"+tc.name, tc.token, tc.amount)
				block := &pb.Block{BlockHash: "block-" + tc.name, Header: &pb.BlockHeader{Height: 1}, Body: []*pb.AnyTx{tx}}
				var spec *vm.SpecResult
				require.NotPanics(t, func() { spec, err = executor.BuildSpecResult(block) }, "the conservation audit must accept the transfer")
				require.NoError(t, err)
				require.True(t, spec.Valid, spec.Reason)
				require.Len(t, spec.Receipts, 1)
				require.Equal(t, tc.wantStatus, spec.Receipts[0].Status)
				require.Equal(t, "1000", spec.Receipts[0].Fee)
				if tc.wantFB == "0" {
					found := false
					for _, op := range spec.Diff {
						if op.Key == keys.KeyBalance("zero-balance-sender", vm.FeeToken) {
							found = true
							require.True(t, op.Del, "zero balance must remain a deletion, never an empty record")
						}
					}
					require.True(t, found, "missing sender balance deletion")
				}
				require.NotPanics(t, func() { err = executor.CommitFinalizedBlock(block) })
				require.NoError(t, err)
				sv := vm.NewStateView(store.Get, nil)
				require.Equal(t, tc.wantFB, vm.GetBalance(sv, "zero-balance-sender", vm.FeeToken).Balance)
				require.Equal(t, tc.wantReceived, vm.GetBalance(sv, "zero-balance-receiver", tc.token).Balance)
				require.Equal(t, "1000", vm.GetBalance(sv, vm.RuntimeFeePoolAccount, vm.FeeToken).Balance)
				if tc.wantFB == "0" {
					_, exists := store.data[keys.KeyBalance("zero-balance-sender", vm.FeeToken)]
					require.False(t, exists, "committed zero balance key must not exist")
				}
				raw, err := store.Get(keys.KeyAccount("zero-balance-sender"))
				require.NoError(t, err)
				var account pb.Account
				require.NoError(t, proto.Unmarshal(raw, &account))
				require.Equal(t, uint64(1), account.Nonce)
			})
		}
	}
}

func TestExecutorZeroBalanceProtocolFeeRefund(t *testing.T) {
	store := NewMockDB()
	seedRuntimeFeeAccount(t, store, "fee-only-coordinator", "1000")
	tx := newRuntimeFeeClaimTx("zero-balance-refund", "fee-only-coordinator")
	executor := newRuntimeFeeExecutor(t, store, tx.GetKind(), nil)
	block := &pb.Block{BlockHash: "block-zero-balance-refund", Header: &pb.BlockHeader{Height: 1}, Body: []*pb.AnyTx{tx}}
	spec, err := executor.BuildSpecResult(block)
	require.NoError(t, err)
	require.True(t, spec.Valid, spec.Reason)
	require.Equal(t, "0", spec.Receipts[0].Fee)
	require.NoError(t, executor.CommitFinalizedBlock(block))
	require.Equal(t, "1000", readRuntimeFeeFBBalance(t, store, "fee-only-coordinator"))
	_, exists := store.data[keys.KeyBalance(vm.RuntimeFeePoolAccount, vm.FeeToken)]
	require.False(t, exists, "refunded fee pool must be deleted")
}

func TestExecutorZeroBalancePersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	store, err := db.NewManager(dir, logs.NewNodeLogger("zero-balance", 0))
	require.NoError(t, err)
	defer func() {
		if store != nil {
			store.Close()
		}
	}()
	store.InitWriteQueue(10, 50*time.Millisecond)
	createE2ETestAccount(t, store, "zero-balance-sender", map[string]string{"FB": "6000"})
	require.NoError(t, store.ForceFlush())
	reg := vm.NewHandlerRegistry()
	require.NoError(t, vm.RegisterDefaultHandlers(reg))
	executor := vm.NewExecutor(store, reg, nil)
	tx := zeroBalanceTransfer("persist-zero-balance", "FB", "5000")
	block := &pb.Block{
		BlockHash: "block-persist-zero-balance",
		Header:    &pb.BlockHeader{Height: 1, StateRoot: store.GetStateFingerprint()}, Body: []*pb.AnyTx{tx},
	}
	require.NotPanics(t, func() { err = executor.CommitFinalizedBlock(block) })
	require.NoError(t, err)
	require.NoError(t, store.ForceFlush())
	fingerprint := append([]byte(nil), store.GetStateFingerprint()...)
	_, exists, err := store.GetState(keys.KeyBalance("zero-balance-sender", vm.FeeToken))
	require.NoError(t, err)
	require.False(t, exists)
	store.Close()
	store, err = db.NewManager(dir, logs.NewNodeLogger("zero-balance-restart", 0))
	require.NoError(t, err)
	store.InitWriteQueue(10, 50*time.Millisecond)
	require.Equal(t, fingerprint, store.GetStateFingerprint())
	_, exists, err = store.GetState(keys.KeyBalance("zero-balance-sender", vm.FeeToken))
	require.NoError(t, err)
	require.False(t, exists, "restart must not resurrect the deleted sender balance")
	require.Equal(t, "5000", getE2EBalance(t, store, "zero-balance-receiver", vm.FeeToken).Balance)
	require.Equal(t, "1000", getE2EBalance(t, store, vm.RuntimeFeePoolAccount, vm.FeeToken).Balance)
	                                                                                 
	restarted := vm.NewExecutor(store, reg, nil)
	require.NoError(t, restarted.CommitFinalizedBlock(block))
	require.Equal(t, fingerprint, store.GetStateFingerprint())
}
