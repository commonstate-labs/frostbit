package vm

import (
	"testing"

	"dex/keys"
	"dex/pb"
	"dex/witness"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func seedEscrowBalance(sv StateView, address, amount string) {
	SetBalance(sv, address, FeeToken, &pb.TokenBalance{Balance: amount})
}

func escrowBalance(t *testing.T, sv StateView, address string) string {
	t.Helper()
	return GetBalance(sv, address, FeeToken).Balance
}

func loadEscrow(t *testing.T, sv StateView, sourceTxID string) *pb.RuntimeFeeEscrow {
	t.Helper()
	raw, ok, err := sv.Get(keys.KeyRuntimeFeeEscrow(sourceTxID))
	require.NoError(t, err)
	require.True(t, ok)
	var escrow pb.RuntimeFeeEscrow
	require.NoError(t, proto.Unmarshal(raw, &escrow))
	return &escrow
}

func TestDelayedRuntimeFeeRefundIsExactlyOnce(t *testing.T) {
	sv := NewMockStateView()
	seedEscrowBalance(sv, "payer", "5000")
	_, actual, err := DeductFee(sv, "payer", "1000")
	require.NoError(t, err)
	require.NoError(t, openRuntimeFeeEscrow(sv, "request-1", "payer", actual, runtimeFeeEscrowKindWitnessRequest))

	require.NoError(t, settleRuntimeFeeEscrow(sv, "request-1", 25, true))
	require.NoError(t, settleRuntimeFeeEscrow(sv, "request-1", 25, true))
	require.Equal(t, "5000", escrowBalance(t, sv, "payer"))
	require.Equal(t, "0", escrowBalance(t, sv, RuntimeFeePoolAccount))
	escrow := loadEscrow(t, sv, "request-1")
	require.Equal(t, pb.RuntimeFeeEscrowStatus_RUNTIME_FEE_ESCROW_REFUNDED, escrow.Status)
	require.Equal(t, uint64(25), escrow.SettledHeight)
}

func TestRejectedDelayedRuntimeFeeIsReleasedOnce(t *testing.T) {
	sv := NewMockStateView()
	seedEscrowBalance(sv, "payer", "5000")
	_, actual, err := DeductFee(sv, "payer", "1000")
	require.NoError(t, err)
	require.NoError(t, openRuntimeFeeEscrow(sv, "request-2", "payer", actual, runtimeFeeEscrowKindWitnessRequest))

	require.NoError(t, settleRuntimeFeeEscrow(sv, "request-2", 30, false))
	require.NoError(t, settleRuntimeFeeEscrow(sv, "request-2", 30, false))
	released, err := collectReleasedRuntimeFees(sv, 30)
	require.NoError(t, err)
	require.Equal(t, "1000", released.String())
	releasedAgain, err := collectReleasedRuntimeFees(sv, 30)
	require.NoError(t, err)
	require.Zero(t, releasedAgain.Sign())
	require.NoError(t, SpendRuntimeFeePool(sv, released))
	require.Equal(t, "4000", escrowBalance(t, sv, "payer"))
	require.Equal(t, "0", escrowBalance(t, sv, RuntimeFeePoolAccount))
	require.Equal(t, pb.RuntimeFeeEscrowStatus_RUNTIME_FEE_ESCROW_CONSUMED, loadEscrow(t, sv, "request-2").Status)
}

func TestDelayedRuntimeFeeRequiresTerminalHeight(t *testing.T) {
	sv := NewMockStateView()
	seedEscrowBalance(sv, "payer", "5000")
	_, actual, err := DeductFee(sv, "payer", "1000")
	require.NoError(t, err)
	require.NoError(t, openRuntimeFeeEscrow(sv, "request-3", "payer", actual, runtimeFeeEscrowKindWitnessRequest))

	require.Error(t, settleRuntimeFeeEscrow(sv, "request-3", 0, false))
	require.Equal(t, pb.RuntimeFeeEscrowStatus_RUNTIME_FEE_ESCROW_PENDING, loadEscrow(t, sv, "request-3").Status)
	require.Equal(t, "1000", escrowBalance(t, sv, RuntimeFeePoolAccount))
}

func TestRejectedArbitrationOutcomeUsesExecutingHeightForFeeRelease(t *testing.T) {
	sv := NewMockStateView()
	request := &pb.RechargeRequest{RequestId: "request-4", Status: pb.RechargeRequestStatus_RECHARGE_VOTING}
	raw, err := proto.Marshal(request)
	require.NoError(t, err)
	sv.Set(keys.KeyRechargeRequest(request.RequestId), raw)
	seedEscrowBalance(sv, "payer", "5000")
	_, actual, err := DeductFee(sv, "payer", "1000")
	require.NoError(t, err)
	require.NoError(t, openRuntimeFeeEscrow(sv, request.RequestId, "payer", actual, runtimeFeeEscrowKindWitnessRequest))

	rejected := proto.Clone(request).(*pb.RechargeRequest)
	rejected.Status = pb.RechargeRequestStatus_RECHARGE_REJECTED
	require.NoError(t, applyArbitrationOutcomeToState(sv, &witness.ArbitrationVoteOutcome{Request: rejected}, 1, 0, 40))
	stored := loadRechargeRequestAtKey(sv, keys.KeyRechargeRequest(request.RequestId))
	require.NotNil(t, stored)
	require.Equal(t, uint64(40), stored.FinalizeHeight)
	released, err := collectReleasedRuntimeFees(sv, 40)
	require.NoError(t, err)
	require.Equal(t, "1000", released.String())
}
