package vm

import (
	"dex/keys"
	"dex/pb"
	"dex/witness"
	"testing"

	"google.golang.org/protobuf/proto"
)

func TestAccountDepositRejectedStateWritesReleaseGlobalPending(t *testing.T) {
	for _, arbitration := range []bool{false, true} {
		name := "direct"
		if arbitration {
			name = "arbitration"
		}
		t.Run(name, func(t *testing.T) {
			sv, handler := seedAccountDepositAudit(t, "eth")
			first := admitAccountDepositAudit(t, sv, handler, signedAccountDepositAudit(t, 8201, "eth", 4, 1))
			if _, receipt, err := handler.DryRun(signedAccountDepositAudit(t, 8202, "eth", 4, 1), sv); err != nil || receipt == nil || receipt.Status != "FAILED" {
				t.Fatalf("other requester bypassed global lock: receipt=%+v err=%v", receipt, err)
			}
			rejected := proto.Clone(first).(*pb.RechargeRequest)
			rejected.Status = pb.RechargeRequestStatus_RECHARGE_REJECTED
			rejected.FinalizeHeight = 20
			var err error
			if arbitration {
				err = applyArbitrationOutcomeToState(sv, &witness.ArbitrationVoteOutcome{Request: rejected}, 1, 0, 20)
			} else {
				err = setRechargeRequestWithIndexes(sv, rejected, 1)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, exists, _ := sv.Get(accountChainRechargePendingKey(first)); exists {
				t.Fatal("rejected claim retained the global pending lock")
			}
			                                                                      
			retry := admitAccountDepositAudit(t, sv, handler, signedAccountDepositAudit(t, 8202, "eth", 4, 1))
			if retry.RequestId == first.RequestId {
				t.Fatal("retry did not create a new request")
			}
		})
	}
}

func TestAccountDepositLateRejectionCannotDowngradeCreditedRequest(t *testing.T) {
	sv := NewMockStateView()
	req := seedAccountRechargeFinalizationTest(t, sv, "router_late_rejection", pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD)
	if err := applyRechargeFinalized(sv, req, 100, 1); err != nil {
		t.Fatal(err)
	}
	late := proto.Clone(req).(*pb.RechargeRequest)
	late.Status = pb.RechargeRequestStatus_RECHARGE_REJECTED
	late.FinalizeHeight = 101
	if err := applyRechargeRejected(sv, late, 1); err != nil {
		t.Fatal(err)
	}
	if err := setRechargeRequestWithIndexes(sv, late, 1); err != nil {
		t.Fatal(err)
	}
	stored := loadRechargeRequestAtKey(sv, keys.KeyRechargeRequest(req.RequestId))
	if stored.Status != pb.RechargeRequestStatus_RECHARGE_FINALIZED || stored.FinalizeHeight != 100 {
		t.Fatalf("late rejection changed finalized state: %+v", stored)
	}
	if _, exists, _ := sv.Get(keys.KeyRechargeCredited(req.RequestId)); !exists {
		t.Fatal("late rejection removed credited marker")
	}
	if owner, exists, _ := sv.Get(keys.KeyRechargeEvent(req.NativeChain, req.NativeTxHash, req.DepositEventIndex)); !exists || string(owner) != req.RequestId {
		t.Fatal("late rejection changed consumed deposit event")
	}
	if err := applyRechargeFinalized(sv, req, 102, 1); err != nil {
		t.Fatalf("idempotent finalization failed after stale rejection: %v", err)
	}
	if got := GetBalance(sv, req.ReceiverAddress, req.TokenAddress).Balance; got != req.Amount {
		t.Fatalf("late rejection or repeated finalization changed balance: %s", got)
	}
}
