package vm

import (
	"dex/keys"
	"dex/pb"
	"testing"

	"google.golang.org/protobuf/proto"
)

func TestWitnessChallengeMinimumAppliesToEachNewContribution(t *testing.T) {
	for _, tc := range []struct {
		name, amount                  string
		existing, replay, wantSuccess bool
	}{
		{name: "first below minimum", amount: "999999999"},
		{name: "first exactly minimum", amount: "1000000000", wantSuccess: true},
		{name: "additional tiny contribution", amount: "1", existing: true},
		{name: "additional below minimum", amount: "999999999", existing: true},
		{name: "additional exactly minimum", amount: "1000000000", existing: true, wantSuccess: true},
		{name: "historical contribution replay", amount: "1", existing: true, replay: true, wantSuccess: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sv := newMemStateView()
			put := func(key string, message proto.Message) {
				t.Helper()
				data, err := proto.Marshal(message)
				if err != nil {
					t.Fatal(err)
				}
				sv.Set(key, data)
			}
			req := &pb.RechargeRequest{ArbitrationProtocolVersion: 1, FeeEscrow: "0", RequestId: "request", Status: pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD, DeadlineHeight: 100}
			if tc.existing {
				req.ChallengeId = "case"
				put(keys.KeyChallengeRecord("case"), &pb.ChallengeRecord{
					ChallengeId: "case", RequestId: req.RequestId, StakeAmount: "5000000000", Status: pb.ChallengeStatus_CHALLENGE_FUNDING, RequiredStake: "10000000000",
				})
			}
			put(keys.KeyRechargeRequest(req.RequestId), req)
			put(keys.KeyAccount("alice"), &pb.Account{Address: "alice"})
			seedBalance(sv, "alice", "100000000000", "0")
			tx := &pb.AnyTx{Content: &pb.AnyTx_WitnessChallengeTx{WitnessChallengeTx: &pb.WitnessChallengeTx{
				Base: &pb.BaseMessage{TxId: "contribution", FromAddress: "alice", ExecutedHeight: 10}, RequestId: req.RequestId, StakeAmount: tc.amount,
			}}}
			if tc.replay {
				put(keys.KeyChallengeContribution("case", "contribution"), tx.GetWitnessChallengeTx())
			}
			releaseStateViewHistory(sv)
			ws, receipt, err := (&WitnessChallengeTxHandler{}).DryRun(tx, sv)
			if receipt == nil || (receipt.Status == "SUCCEED") != tc.wantSuccess {
				t.Fatalf("receipt=%+v error=%v wantSuccess=%v", receipt, err, tc.wantSuccess)
			}
			if !tc.wantSuccess || tc.replay {
				if len(ws) != 0 || sv.Snapshot() != 0 || readBalance(sv, "alice").Balance != "100000000000" {
					t.Fatal("rejected/replayed contribution changed balance or state")
				}
			} else if readBalance(sv, "alice").Balance != "99000000000" {
				t.Fatal("valid 10 FB contribution did not debit exactly 10 FB")
			}
		})
	}
}
