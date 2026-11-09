package vm

import (
	"reflect"
	"testing"

	"dex/keys"
	"dex/pb"
	"dex/witness"
	"google.golang.org/protobuf/proto"
)

func useArbitrationTimingService(t *testing.T, x *Executor, period uint64) {
	t.Helper()
	x.WitnessService.Stop()
	cfg := witness.DefaultConfig()
	cfg.ArbitrationPeriodBlocks = period
	x.WitnessService = witness.NewService(cfg)
	t.Cleanup(x.WitnessService.Stop)
}

func TestArbitrationLifecycleDevTimingThreeRoundsCourtRetryAndRestart(t *testing.T) {
	sv, x, req := lifecycleFixture(t, 8)
	useArbitrationTimingService(t, x, 20)
	ch := lifecycleFund(t, sv, x, req.RequestId)
	if ch.ArbitrationPeriodBlocks != 20 {
		t.Fatalf("funding did not persist duration: %d", ch.ArbitrationPeriodBlocks)
	}
	height := challengeSelectionReadyHeight(ch, 3)
	if err := x.applyPendingArbitrationSelections(sv, height); err != nil {
		t.Fatal(err)
	}
	court := []string{"w01", "w02", "w03", "w04"}
	for ballot := uint64(1); ballot <= 5; ballot++ {
		ch = loadChallengeRecordAtKey(sv, keys.KeyChallengeRecord(ch.ChallengeId))
		if ch.BallotRound != ballot || ch.ArbitrationPeriodBlocks != 20 || ch.DeadlineHeight != height+20 {
			t.Fatalf("ballot %d lost actual timing: %+v", ballot, ch)
		}
		wantRound, wantQuorum := uint32(ballot), []uint32{3, 6, 12, 3, 3}[ballot-1]
		if ballot >= 4 {
			wantRound = 3
			if ch.ArbitrationStage != pb.ArbitrationStage_ARBITRATION_FINAL_COURT || !reflect.DeepEqual(ch.Arbitrators, court) {
				t.Fatalf("court did not retain frozen four: %+v", ch)
			}
		}
		if quorum, err := witness.ArbitrationQuorum(ch); err != nil || quorum != wantQuorum || ch.ArbitrationRound != wantRound {
			t.Fatalf("ballot %d weakened threshold: quorum=%d round=%d err=%v", ballot, quorum, ch.ArbitrationRound, err)
		}
		if ballot == 3 && !reflect.DeepEqual(ch.FinalCourt, court) {
			t.Fatal("third round did not snapshot final court")
		}
		if ballot == 4 {
			                                                                    
			for i, address := range court {
				direction := pb.WitnessVoteType_VOTE_PASS
				if i >= 2 {
					direction = pb.WitnessVoteType_VOTE_FAIL
				}
				lifecycleBallot(t, sv, x, ch, address, direction, height, true)
			}
			ch = loadChallengeRecordAtKey(sv, keys.KeyChallengeRecord(ch.ChallengeId))
		}
		if ballot == 5 {
			if len(ch.ArbitrationVotes) != 0 || ch.PassCount != 0 || ch.FailCount != 0 || ch.Finalized {
				t.Fatal("court retry retained old votes or prematurely finalized")
			}
			break
		}
		before := proto.Clone(ch)
		if err := x.applyArbitrationTimeouts(sv, ch.DeadlineHeight); err != nil {
			t.Fatal(err)
		}
		if after := loadChallengeRecordAtKey(sv, keys.KeyChallengeRecord(ch.ChallengeId)); !proto.Equal(before, after) {
			t.Fatal("inclusive deadline transitioned early")
		}
		if _, exists, err := sv.Get(keys.KeyChallengeTimeoutDue(ch.DeadlineHeight+1, ch.ChallengeId)); err != nil || !exists {
			t.Fatalf("actual timeout not indexed: exists=%v err=%v", exists, err)
		}
		if ballot == 2 {
			                                                                                            
			useArbitrationTimingService(t, x, 10000)
		}
		height = ch.DeadlineHeight + 1
		if err := x.applyArbitrationTimeouts(sv, height); err != nil {
			t.Fatal(err)
		}
	}
	for _, address := range court[:3] {
		lifecycleBallot(t, sv, x, ch, address, pb.WitnessVoteType_VOTE_FAIL, height, true)
	}
	if final := loadChallengeRecordAtKey(sv, keys.KeyChallengeRecord(ch.ChallengeId)); !final.Settled || !final.ChallengeSuccess {
		t.Fatalf("three matching final votes failed to settle: %+v", final)
	}
	                                                                                   
	data, exists, err := sv.Get(keys.KeyChallengeRecord(ch.ChallengeId))
	if err != nil || !exists {
		t.Fatalf("terminal marker missing: exists=%v err=%v", exists, err)
	}
	var marker pb.ChallengeRecord
	if err := proto.Unmarshal(data, &marker); err != nil || marker.ArbitrationPeriodBlocks != 20 {
		t.Fatalf("terminal marker lost actual duration: %+v err=%v", &marker, err)
	}
}

func TestArbitrationLifecycleLegacyZeroTimingStaysProduction(t *testing.T) {
	for _, active := range []bool{false, true} {
		name := "pending_selection"
		if active {
			name = "active"
		}
		t.Run(name, func(t *testing.T) {
			sv, x, req := lifecycleFixture(t, 8)
			ch := lifecycleFund(t, sv, x, req.RequestId)
			height := challengeSelectionReadyHeight(ch, 3)
			if active {
				if err := x.applyPendingArbitrationSelections(sv, height); err != nil {
					t.Fatal(err)
				}
				ch = loadChallengeRecordAtKey(sv, keys.KeyChallengeRecord(ch.ChallengeId))
				height = ch.DeadlineHeight + 1
			}
			                                                                
			ch.ArbitrationPeriodBlocks = 0
			if err := setChallengeRecordWithTimeoutIndexes(sv, ch, 3, 0, 0); err != nil {
				t.Fatal(err)
			}
			useArbitrationTimingService(t, x, 20)
			var err error
			if active {
				err = x.applyArbitrationTimeouts(sv, height)
			} else {
				err = x.applyPendingArbitrationSelections(sv, height)
			}
			if err != nil {
				t.Fatal(err)
			}
			got := loadChallengeRecordAtKey(sv, keys.KeyChallengeRecord(ch.ChallengeId))
			if got.ArbitrationPeriodBlocks != 10000 || got.DeadlineHeight != height+10000 {
				t.Fatalf("development config shortened legacy case: %+v", got)
			}
		})
	}
}
