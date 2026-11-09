package witness_test

import (
	"testing"

	"dex/pb"
	"dex/witness"
	"google.golang.org/protobuf/proto"
)

func TestArbitrationTimingPreparationPreservesCaseSnapshotAndLegacy(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status pb.ChallengeStatus
		stored uint64
		want   uint64
	}{
		{"new funding", pb.ChallengeStatus_CHALLENGE_FUNDING, 0, 20},
		{"legacy pending selection", pb.ChallengeStatus_CHALLENGE_PENDING_SELECTION, 0, 10000},
		{"legacy active", pb.ChallengeStatus_CHALLENGE_ACTIVE, 0, 10000},
		{"existing production case", pb.ChallengeStatus_CHALLENGE_ACTIVE, 10000, 10000},
		{"existing development case", pb.ChallengeStatus_CHALLENGE_ACTIVE, 20, 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := witness.DefaultConfig()
			cfg.ArbitrationPeriodBlocks = 20
			req := &pb.RechargeRequest{RequestId: "timing"}
			ch := &pb.ChallengeRecord{RequestId: req.RequestId, Status: tc.status, ArbitrationPeriodBlocks: tc.stored, DeadlineHeight: 12345}
			if err := witness.PrepareArbitrationRecord(ch, req, cfg); err != nil {
				t.Fatal(err)
			}
			if ch.ArbitrationPeriodBlocks != tc.want || ch.DeadlineHeight != 12345 {
				t.Fatalf("snapshot=%d deadline=%d, want %d and unchanged deadline", ch.ArbitrationPeriodBlocks, ch.DeadlineHeight, tc.want)
			}
			before := proto.Clone(ch)
			cfg.ArbitrationPeriodBlocks = 10000
			if err := witness.PrepareArbitrationRecord(ch, req, cfg); err != nil || !proto.Equal(before, ch) {
				t.Fatalf("repeated preparation changed snapshot: err=%v", err)
			}
			started, err := witness.StartArbitrationRound(ch, []string{"a", "b", "c", "d"}, nil, 20000, cfg.ArbitrationPeriodBlocks)
			if err != nil || started.DeadlineHeight != 20000+tc.want || started.ArbitrationPeriodBlocks != tc.want {
				t.Fatalf("round did not use frozen duration: %+v err=%v", started, err)
			}
		})
	}
}

func TestArbitrationTimingLegacyRoundStartCannotAdoptLiveDevelopmentConfig(t *testing.T) {
	ch := &pb.ChallengeRecord{ArbitrationRound: 2, BallotRound: 1, OrdinaryCommitteeSizes: []uint32{4, 8, 16}, OrdinaryThresholdBps: 7500}
	started, err := witness.StartArbitrationRound(ch, []string{"a", "b", "c", "d"}, nil, 20000, 20)
	if err != nil || started.ArbitrationPeriodBlocks != 10000 || started.DeadlineHeight != 30000 {
		t.Fatalf("legacy direct transition shortened old case: %+v err=%v", started, err)
	}
	if ch.ArbitrationPeriodBlocks != 0 {
		t.Fatal("pure transition mutated input")
	}
}

func TestArbitrationTimingNewManagerCaseAndServiceGetter(t *testing.T) {
	cfg := witness.DefaultConfig()
	cfg.ArbitrationPeriodBlocks = 20
	svc := witness.NewService(cfg)
	defer svc.Stop()
	if svc.ArbitrationPeriodBlocks() != 20 {
		t.Fatal("service does not expose configured duration")
	}
	cm := witness.NewChallengeManager(cfg)
	ch, err := cm.CreateChallenge("timing-case", "timing-request", "system", "0", "auto review", 100, []string{"a", "b", "c", "d"})
	if err != nil || ch.ArbitrationPeriodBlocks != 20 || ch.DeadlineHeight != 120 {
		t.Fatalf("new auto case failed to snapshot developer duration: %+v err=%v", ch, err)
	}
	var absent *witness.Service
	if absent.ArbitrationPeriodBlocks() != 10000 {
		t.Fatal("absent configuration did not retain production default")
	}
}
