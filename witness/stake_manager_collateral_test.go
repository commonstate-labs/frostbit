package witness

import (
	"dex/pb"
	"errors"
	"testing"
)

func TestStakeManagerExitRequestDoesNotWaitForOldCases(t *testing.T) {
	sm := NewStakeManager(DefaultConfig())
	sm.LoadWitness(&pb.WitnessInfo{Address: "alice", StakeAmount: "3200000000", Status: pb.WitnessStatus_WITNESS_ACTIVE,
		PendingTasks: []string{"already-voted"}, ShelvedArbitrations: []string{"old"}, ActiveCaseCount: 1, ReservedStakeAmount: "1000000000"})
	if err := sm.ValidateUnstake("alice"); err != nil {
		t.Fatalf("exit request blocked by old responsibility: %v", err)
	}
	if _, err := sm.ProcessUnstake("alice", 100); err != nil {
		t.Fatal(err)
	}
	if err := sm.ValidateExit("alice", 100+sm.config.UnstakeLockBlocks); !errors.Is(err, ErrPendingTasks) {
		t.Fatalf("old duty should block withdrawal: %v", err)
	}
	if len(sm.GetActiveWitnesses()) != 0 {
		t.Fatal("exiting witness can take new case")
	}
}

func TestStakeManagerCourtDutyBlocksClaimEvenWithZeroReserved(t *testing.T) {
	sm := NewStakeManager(DefaultConfig())
	info := &pb.WitnessInfo{Address: "judge", StakeAmount: "3200000000", Status: pb.WitnessStatus_WITNESS_UNSTAKING, ActiveCaseCount: 1, ReservedStakeAmount: "0"}
	sm.LoadWitness(info)
	if err := sm.ValidateExit("judge", sm.config.UnstakeLockBlocks); !errors.Is(err, ErrPendingTasks) {
		t.Fatalf("court duty omitted: %v", err)
	}
	if _, _, err := sm.ProcessExit("judge", sm.config.UnstakeLockBlocks); !errors.Is(err, ErrPendingTasks) {
		t.Fatalf("direct exit bypassed old court duty: %v", err)
	}
	info.ActiveCaseCount = 0
	info.PendingTasks = []string{"stale-cache-task"}
	sm.LoadWitness(info)
	if err := sm.ValidateExit("judge", sm.config.UnstakeLockBlocks); err != nil {
		t.Fatalf("stale task overrode authoritative released duty: %v", err)
	}
}

func TestStakeManagerLegacySlashCannotTakeCaseCollateral(t *testing.T) {
	sm := NewStakeManager(DefaultConfig())
	sm.LoadWitness(&pb.WitnessInfo{Address: "alice", StakeAmount: "3200000000", Status: pb.WitnessStatus_WITNESS_ACTIVE,
		ReservedStakeAmount: "1000000000", ActiveCaseCount: 1})
	if _, _, err := sm.ProcessSlash("alice", "old-global-path", 1); err == nil {
		t.Fatal("legacy slash consumed funds assigned to an active case")
	}
	if info, _ := sm.GetWitness("alice"); info.StakeAmount != "3200000000" || info.ReservedStakeAmount != "1000000000" {
		t.Fatal("legacy rejection modified collateral")
	}
}

func TestStakeManagerNewCandidatesRequireCurrentRegisteredStake(t *testing.T) {
	sm := NewStakeManager(DefaultConfig())
	for address, amount := range map[string]string{"good": "3200000000", "zero": "0", "low": "3199999999", "fraction": "3200000000.5"} {
		sm.LoadWitness(&pb.WitnessInfo{Address: address, StakeAmount: amount, Status: pb.WitnessStatus_WITNESS_ACTIVE})
	}
	active := sm.GetActiveWitnesses()
	if len(active) != 1 || active[0].Address != "good" {
		t.Fatalf("unqualified active candidates: %+v", active)
	}
	for _, candidate := range sm.GetWitnessCandidates() {
		if candidate.IsActive && candidate.Address != "good" {
			t.Fatalf("unqualified selected candidate: %+v", candidate)
		}
	}
	if info, err := sm.GetWitness("zero"); err != nil || info.Status != pb.WitnessStatus_WITNESS_ACTIVE {
		t.Fatal("candidate filtering erased old identity")
	}
}
