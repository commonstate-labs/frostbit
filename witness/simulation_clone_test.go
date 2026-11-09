package witness

import (
	"dex/pb"
	"testing"
)

func TestCloneForSimulationDeepCopiesRuntimeState(t *testing.T) {
	service := NewService(DefaultConfig())
	defer service.Stop()
	service.SetCurrentHeight(41)
	service.LoadWitness(&pb.WitnessInfo{
		Address:      "witness-1",
		StakeAmount:  "1000",
		Status:       pb.WitnessStatus_WITNESS_ACTIVE,
		PendingTasks: []string{"request-1"},
	})
	service.LoadRequest(&pb.RechargeRequest{
		RequestId:         "request-1",
		Status:            pb.RechargeRequestStatus_RECHARGE_VOTING,
		SelectedWitnesses: []string{"witness-1"},
	})
	service.LoadChallenge(&pb.ChallengeRecord{
		ChallengeId:    "challenge-1",
		RequestId:      "request-1",
		Status:         pb.ChallengeStatus_CHALLENGE_ACTIVE,
		DeadlineHeight: 50,
	})

	cloned := service.CloneForSimulation()
	if cloned == nil {
		t.Fatal("CloneForSimulation() returned nil")
	}
	defer cloned.Stop()
	if got := cloned.GetCurrentHeight(); got != 41 {
		t.Fatalf("cloned current height = %d, want 41", got)
	}

	clonedWitness, err := cloned.GetWitnessInfo("witness-1")
	if err != nil {
		t.Fatalf("cloned witness lookup: %v", err)
	}
	clonedWitness.StakeAmount = "9999"
	clonedWitness.PendingTasks[0] = "changed-task"
	clonedRequest, err := cloned.GetRequest("request-1")
	if err != nil {
		t.Fatalf("cloned request lookup: %v", err)
	}
	clonedRequest.SelectedWitnesses[0] = "changed-witness"
	clonedChallenge, err := cloned.GetChallenge("challenge-1")
	if err != nil {
		t.Fatalf("cloned challenge lookup: %v", err)
	}
	clonedChallenge.DeadlineHeight = 999

	originalWitness, _ := service.GetWitnessInfo("witness-1")
	if originalWitness.StakeAmount != "1000" || originalWitness.PendingTasks[0] != "request-1" {
		t.Fatalf("simulation clone mutated original witness: %+v", originalWitness)
	}
	originalRequest, _ := service.GetRequest("request-1")
	if originalRequest.SelectedWitnesses[0] != "witness-1" {
		t.Fatalf("simulation clone mutated original request: %+v", originalRequest)
	}
	originalChallenge, _ := service.GetChallenge("challenge-1")
	if originalChallenge.DeadlineHeight != 50 {
		t.Fatalf("simulation clone mutated original challenge: %+v", originalChallenge)
	}
}
