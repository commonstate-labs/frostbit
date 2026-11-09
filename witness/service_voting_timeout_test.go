package witness

import (
	"testing"

	"dex/pb"
)

func TestVotingDeadlineWithoutVotesSchedulesReselectionWithOldWitnessesEligible(t *testing.T) {
	cfg := DefaultConfig()
	cfg.InitialWitnessCount = 2
	cfg.MinSelectedWitnessCount = 2
	cfg.ExpandMultiplier = 2
	cfg.VotingPeriodBlocks = 10
	cfg.RetryIntervalBlocks = 0
	cfg.SelectionBeaconDepth = 3

	svc := NewService(cfg)
	for _, address := range []string{"w1", "w2", "w3", "w4"} {
		svc.LoadWitness(&pb.WitnessInfo{
			Address:       address,
			StakeAmount:   cfg.MinStakeAmount,
			PendingReward: "0",
			Status:        pb.WitnessStatus_WITNESS_ACTIVE,
		})
	}

	const oldDeadline uint64 = 100
	svc.LoadRequest(&pb.RechargeRequest{
		RequestId:         "req_silent_deadline",
		Status:            pb.RechargeRequestStatus_RECHARGE_VOTING,
		Round:             0,
		DeadlineHeight:    oldDeadline,
		SelectedWitnesses: []string{"w1", "w2"},
	})

	svc.SetCurrentHeight(oldDeadline)

	got, err := svc.GetRequest("req_silent_deadline")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != pb.RechargeRequestStatus_RECHARGE_PENDING_SELECTION {
		t.Fatalf("silent deadline status=%s, want PENDING_SELECTION", got.Status)
	}
	if got.Round != 1 {
		t.Fatalf("silent deadline round=%d, want 1", got.Round)
	}
	if got.SelectionHeight != oldDeadline+cfg.SelectionBeaconDepth {
		t.Fatalf("selection height=%d, want %d", got.SelectionHeight, oldDeadline+cfg.SelectionBeaconDepth)
	}
	if got.ShelveHeight != oldDeadline {
		t.Fatalf("reselection wait start=%d, want %d", got.ShelveHeight, oldDeadline)
	}

	svc.mu.RLock()
	oldDue := svc.dueVotingRequestIDsLocked(oldDeadline)
	svc.mu.RUnlock()
	if len(oldDue) != 0 {
		t.Fatalf("expired deadline remained due after scheduling reselection: %v", oldDue)
	}

	selected, err := svc.ApplyRechargeSelection(got, []byte("next-round-beacon"), 104)
	if err != nil {
		t.Fatalf("apply reselection: %v", err)
	}
	if selected.Status != pb.RechargeRequestStatus_RECHARGE_VOTING || selected.Round != 1 {
		t.Fatalf("selected status=%s round=%d", selected.Status, selected.Round)
	}
	if len(selected.SelectedWitnesses) != 4 || !containsString(selected.SelectedWitnesses, "w1") || !containsString(selected.SelectedWitnesses, "w2") {
		t.Fatalf("old witnesses were not reusable: %v", selected.SelectedWitnesses)
	}
	if selected.ShelveHeight != 0 {
		t.Fatalf("successful selection retained wait marker %d", selected.ShelveHeight)
	}
}

func TestVotingDeadlineSchedulesReselectionEvenWhenActiveSetIsBelowMinimum(t *testing.T) {
	cfg := DefaultConfig()
	cfg.InitialWitnessCount = 2
	cfg.MinSelectedWitnessCount = 4
	cfg.ExpandMultiplier = 2
	cfg.VotingPeriodBlocks = 10
	cfg.RetryIntervalBlocks = 0

	svc := NewService(cfg)
	for _, address := range []string{"w1", "w2", "w3"} {
		svc.LoadWitness(&pb.WitnessInfo{
			Address:       address,
			StakeAmount:   cfg.MinStakeAmount,
			PendingReward: "0",
			Status:        pb.WitnessStatus_WITNESS_ACTIVE,
		})
	}

	const deadline uint64 = 100
	svc.LoadRequest(&pb.RechargeRequest{
		RequestId:         "req_below_minimum",
		Status:            pb.RechargeRequestStatus_RECHARGE_VOTING,
		Round:             0,
		DeadlineHeight:    deadline,
		SelectedWitnesses: []string{"w1", "w2"},
	})

	svc.SetCurrentHeight(deadline)
	got, err := svc.GetRequest("req_below_minimum")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != pb.RechargeRequestStatus_RECHARGE_PENDING_SELECTION {
		t.Fatalf("status=%s, want PENDING_SELECTION", got.Status)
	}
	if got.Round != 1 {
		t.Fatalf("scheduled round=%d, want 1", got.Round)
	}
	if got.ShelveHeight != deadline {
		t.Fatalf("shelve height=%d, want %d", got.ShelveHeight, deadline)
	}
}

func TestShelvedRequestWaitsForCooldownThenSchedulesFutureBeaconReselection(t *testing.T) {
	cfg := DefaultConfig()
	cfg.InitialWitnessCount = 4
	cfg.MinSelectedWitnessCount = 4
	cfg.RetryIntervalBlocks = 20
	cfg.SelectionBeaconDepth = 3
	cfg.BeaconMixBlocks = 2
	cfg.MaxShelveWaitBlocks = 200

	svc := NewService(cfg)
	for _, address := range []string{"w1", "w2", "w3", "w4"} {
		svc.LoadWitness(&pb.WitnessInfo{
			Address:       address,
			StakeAmount:   cfg.MinStakeAmount,
			PendingReward: "0",
			Status:        pb.WitnessStatus_WITNESS_ACTIVE,
		})
	}
	svc.LoadRequest(&pb.RechargeRequest{
		RequestId:         "req_shelved_reselection",
		Status:            pb.RechargeRequestStatus_RECHARGE_SHELVED,
		Round:             0,
		DeadlineHeight:    100,
		ShelveHeight:      100,
		SelectionHeight:   80,
		SelectedWitnesses: []string{"w1", "w2", "w3", "w4"},
		NativeChain:       "ETH",
		NativeTxHash:      "0xreselect",
	})

	svc.SetCurrentHeight(100)
	cooling, err := svc.GetRequest("req_shelved_reselection")
	if err != nil {
		t.Fatal(err)
	}
	if cooling.Status != pb.RechargeRequestStatus_RECHARGE_SHELVED {
		t.Fatalf("request retried without cooldown: %s", cooling.Status)
	}

	svc.SetCurrentHeight(120)
	pending, err := svc.GetRequest("req_shelved_reselection")
	if err != nil {
		t.Fatal(err)
	}
	if pending.Status != pb.RechargeRequestStatus_RECHARGE_PENDING_SELECTION {
		t.Fatalf("status=%s, want PENDING_SELECTION", pending.Status)
	}
	if pending.SelectionHeight != 123 {
		t.Fatalf("selection height=%d, want 123", pending.SelectionHeight)
	}
	if pending.Round != 1 {
		t.Fatalf("legacy recovery round=%d, want 1", pending.Round)
	}
	if len(pending.SelectionBeacon) != 0 {
		t.Fatalf("selection beacon populated before future blocks: %x", pending.SelectionBeacon)
	}

	selected, err := svc.ApplyRechargeSelection(pending, []byte("future-block-beacon"), 124)
	if err != nil {
		t.Fatalf("apply recovered selection: %v", err)
	}
	if selected.Round != pending.Round || selected.Status != pb.RechargeRequestStatus_RECHARGE_VOTING {
		t.Fatalf("recovered selection status=%s round=%d", selected.Status, selected.Round)
	}
	if len(selected.SelectedWitnesses) != 4 {
		t.Fatalf("selected=%v, want reused four-member committee", selected.SelectedWitnesses)
	}
	for _, address := range []string{"w1", "w2", "w3", "w4"} {
		if !containsString(selected.SelectedWitnesses, address) {
			t.Fatalf("old committee member %s was not reusable: %v", address, selected.SelectedWitnesses)
		}
	}
	replayed, err := svc.ApplyRechargeSelection(selected, []byte("future-block-beacon"), 124)
	if err != nil {
		t.Fatalf("replay recovered selection: %v", err)
	}
	if replayed.Round != selected.Round {
		t.Fatalf("replaying selected request advanced round from %d to %d", selected.Round, replayed.Round)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
