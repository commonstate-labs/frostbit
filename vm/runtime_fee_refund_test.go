package vm_test

import (
	"errors"
	"fmt"
	"testing"

	"dex/keys"
	"dex/pb"
	"dex/vm"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

type runtimeFeeTestHandler struct {
	kind string
	err  error
}

func (h runtimeFeeTestHandler) Kind() string { return h.kind }

func (h runtimeFeeTestHandler) DryRun(tx *pb.AnyTx, sv vm.StateView) ([]vm.WriteOp, *vm.Receipt, error) {
	base := tx.GetBase()
	rc := &vm.Receipt{TxID: base.TxId, Status: "SUCCEED"}
	if h.err != nil {
		rc.Status = "FAILED"
		rc.Error = h.err.Error()
		return nil, rc, h.err
	}
	return nil, rc, nil
}

func (h runtimeFeeTestHandler) Apply(tx *pb.AnyTx) error { return vm.ErrNotImplemented }

func TestRuntimeFeeRefundsSuccessfulProtocolWorkTx(t *testing.T) {
	for _, tc := range []struct {
		name string
		tx   func(string, string) *pb.AnyTx
	}{
		{name: "frost_claim", tx: newRuntimeFeeClaimTx},
		{name: "frost_signed", tx: newRuntimeFeeSignedTx},
		{name: "witness_vote", tx: newRuntimeFeeWitnessVoteTx},
		{name: "arbitration_vote", tx: newRuntimeFeeArbitrationVoteTx},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := NewMockDB()
			seedRuntimeFeeAccount(t, db, "coordinator", "1000000")
			tx := tc.tx("tx_"+tc.name+"_success", "coordinator")
			executor := newRuntimeFeeExecutor(t, db, tx.GetKind(), nil)
			block := &pb.Block{
				BlockHash: "block_" + tc.name + "_success",
				Header:    &pb.BlockHeader{Height: 1},
				Body:      []*pb.AnyTx{tx},
			}

			spec, err := executor.BuildSpecResult(block)
			require.NoError(t, err)
			require.True(t, spec.Valid, spec.Reason)
			require.Len(t, spec.Receipts, 1)
			require.Equal(t, "SUCCEED", spec.Receipts[0].Status)
			require.Equal(t, "0", spec.Receipts[0].Fee)

			require.NoError(t, executor.CommitFinalizedBlock(block))
			require.Equal(t, "1000000", readRuntimeFeeFBBalance(t, db, "coordinator"))
			require.Equal(t, "0", readRuntimeFeeKey(t, db, keys.KeyVMTxFee(tx.GetTxId())))
			require.Equal(t, "SUCCEED", readRuntimeFeeKey(t, db, keys.KeyVMAppliedTx(tx.GetTxId())))
		})
	}
}

func TestRuntimeFeeKeepsFailedProtocolWorkTxFee(t *testing.T) {
	for _, tc := range []struct {
		name string
		tx   func(string, string) *pb.AnyTx
	}{
		{name: "frost_claim", tx: newRuntimeFeeClaimTx},
		{name: "frost_signed", tx: newRuntimeFeeSignedTx},
		{name: "witness_vote", tx: newRuntimeFeeWitnessVoteTx},
		{name: "arbitration_vote", tx: newRuntimeFeeArbitrationVoteTx},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := NewMockDB()
			seedRuntimeFeeAccount(t, db, "coordinator", "1000000")
			tx := tc.tx("tx_"+tc.name+"_failed", "coordinator")
			executor := newRuntimeFeeExecutor(t, db, tx.GetKind(), errors.New("runtime validation failed"))
			block := &pb.Block{
				BlockHash: "block_" + tc.name + "_failed",
				Header:    &pb.BlockHeader{Height: 1},
				Body:      []*pb.AnyTx{tx},
			}

			spec, err := executor.BuildSpecResult(block)
			require.NoError(t, err)
			require.True(t, spec.Valid, spec.Reason)
			require.Len(t, spec.Receipts, 1)
			require.Equal(t, "FAILED", spec.Receipts[0].Status)
			require.Equal(t, "1000", spec.Receipts[0].Fee)

			require.NoError(t, executor.CommitFinalizedBlock(block))
			require.Equal(t, "999000", readRuntimeFeeFBBalance(t, db, "coordinator"))
			require.Equal(t, "1000", readRuntimeFeeKey(t, db, keys.KeyVMTxFee(tx.GetTxId())))
			require.Equal(t, "FAILED", readRuntimeFeeKey(t, db, keys.KeyVMAppliedTx(tx.GetTxId())))
		})
	}
}

func TestRuntimeFeeEscrowsSuccessfulWitnessRequest(t *testing.T) {
	db := NewMockDB()
	seedRuntimeFeeAccount(t, db, "requester", "1000000")
	tx := &pb.AnyTx{Content: &pb.AnyTx_WitnessRequestTx{WitnessRequestTx: &pb.WitnessRequestTx{
		Base: &pb.BaseMessage{TxId: "request_fee_escrow", FromAddress: "requester", Fee: "1000"},
	}}}
	executor := newRuntimeFeeExecutor(t, db, tx.GetKind(), nil)
	block := &pb.Block{BlockHash: "block_request_fee_escrow", Header: &pb.BlockHeader{Height: 1}, Body: []*pb.AnyTx{tx}}

	spec, err := executor.BuildSpecResult(block)
	require.NoError(t, err)
	require.True(t, spec.Valid, spec.Reason)
	require.Len(t, spec.Receipts, 1)
	require.Equal(t, "SUCCEED", spec.Receipts[0].Status)
	require.Equal(t, "0", spec.Receipts[0].Fee)
	require.NoError(t, executor.CommitFinalizedBlock(block))
	require.Equal(t, "999000", readRuntimeFeeFBBalance(t, db, "requester"))
	require.Equal(t, "1000", readRuntimeFeeFBBalance(t, db, vm.RuntimeFeePoolAccount))

	raw, err := db.Get(keys.KeyRuntimeFeeEscrow(tx.GetTxId()))
	require.NoError(t, err)
	var escrow pb.RuntimeFeeEscrow
	require.NoError(t, proto.Unmarshal(raw, &escrow))
	require.Equal(t, tx.GetTxId(), escrow.SourceTxId)
	require.Equal(t, "requester", escrow.Payer)
	require.Equal(t, "1000", escrow.Amount)
	require.Equal(t, pb.RuntimeFeeEscrowStatus_RUNTIME_FEE_ESCROW_PENDING, escrow.Status)
}

func newRuntimeFeeExecutor(t *testing.T, db *MockDB, kind string, err error) *vm.Executor {
	t.Helper()
	registry := vm.NewHandlerRegistry()
	require.NoError(t, registry.Register(runtimeFeeTestHandler{kind: kind, err: err}))
	return vm.NewExecutor(db, registry, nil)
}

func newRuntimeFeeClaimTx(txID, from string) *pb.AnyTx {
	return &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawClaimTx{FrostWithdrawClaimTx: &pb.FrostWithdrawClaimTx{
		Base:  &pb.BaseMessage{TxId: txID, FromAddress: from, Fee: "1000"},
		JobId: "job_" + txID, WithdrawIds: []string{"withdraw_" + txID}, VaultId: 1, KeyEpoch: 1,
		Chain: "btc", Asset: "native",
	}}}
}

func newRuntimeFeeSignedTx(txID, from string) *pb.AnyTx {
	return &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: &pb.FrostWithdrawSignedTx{
		Base:  &pb.BaseMessage{TxId: txID, FromAddress: from, Fee: "1000"},
		JobId: "job_" + txID, WithdrawIds: []string{"withdraw_" + txID}, VaultId: 1, KeyEpoch: 1,
		Chain: "btc", Asset: "native", SignedPackageBytes: []byte("signed"),
	}}}
}

func newRuntimeFeeWitnessVoteTx(txID, from string) *pb.AnyTx {
	return &pb.AnyTx{Content: &pb.AnyTx_WitnessVoteTx{WitnessVoteTx: &pb.WitnessVoteTx{
		Base: &pb.BaseMessage{TxId: txID, FromAddress: from, Fee: "1000"},
		Vote: &pb.WitnessVote{RequestId: "req_" + txID, WitnessAddress: from, VoteType: pb.WitnessVoteType_VOTE_PASS, TxId: txID},
	}}}
}

func newRuntimeFeeArbitrationVoteTx(txID, from string) *pb.AnyTx {
	return &pb.AnyTx{Content: &pb.AnyTx_ArbitrationVoteTx{ArbitrationVoteTx: &pb.ArbitrationVoteTx{
		Base:        &pb.BaseMessage{TxId: txID, FromAddress: from, Fee: "1000"},
		ChallengeId: "challenge_" + txID,
		Vote:        &pb.WitnessVote{RequestId: "req_" + txID, WitnessAddress: from, VoteType: pb.WitnessVoteType_VOTE_PASS, TxId: txID},
	}}}
}

func seedRuntimeFeeAccount(t *testing.T, db *MockDB, address, fbBalance string) {
	t.Helper()
	accountData, err := proto.Marshal(&pb.Account{Address: address})
	require.NoError(t, err)
	balanceData, err := proto.Marshal(&pb.TokenBalanceRecord{
		Address: address,
		Token:   vm.FeeToken,
		Balance: &pb.TokenBalance{Balance: fbBalance},
	})
	require.NoError(t, err)
	db.data[keys.KeyAccount(address)] = accountData
	db.data[keys.KeyBalance(address, vm.FeeToken)] = balanceData
}

func readRuntimeFeeFBBalance(t *testing.T, db *MockDB, address string) string {
	t.Helper()
	data, err := db.Get(keys.KeyBalance(address, vm.FeeToken))
	require.NoError(t, err)
	require.NotNil(t, data)
	var record pb.TokenBalanceRecord
	require.NoError(t, proto.Unmarshal(data, &record))
	require.NotNil(t, record.Balance)
	return record.Balance.Balance
}

func readRuntimeFeeKey(t *testing.T, db *MockDB, key string) string {
	t.Helper()
	data, err := db.Get(key)
	require.NoError(t, err)
	require.NotNil(t, data, fmt.Sprintf("missing key %s", key))
	return string(data)
}
