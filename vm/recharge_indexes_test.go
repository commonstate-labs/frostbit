package vm

import (
	"testing"

	"dex/keys"
	"dex/pb"
	"dex/witness"

	"github.com/cockroachdb/pebble"
	"google.golang.org/protobuf/proto"
)

                                                                              
                                                                      
func seedStateSelectionWitnesses(t *testing.T, sv StateView, addresses ...string) {
	t.Helper()
	for _, address := range addresses {
		info := &pb.WitnessInfo{Address: address, StakeAmount: "10000000000", Status: pb.WitnessStatus_WITNESS_ACTIVE}
		data, err := proto.Marshal(info)
		if err != nil {
			t.Fatal(err)
		}
		sv.Set(keys.KeyWitnessInfo(address), data)
		SetBalance(sv, address, "FB", &pb.TokenBalance{Balance: "0", WitnessLockedBalance: info.StakeAmount})
	}
}

func TestRechargeSelectionIndexTracksPendingSelection(t *testing.T) {
	sv := NewMockStateView()
	req := &pb.RechargeRequest{ArbitrationProtocolVersion: 1, FeeEscrow: "0",
		RequestId:       "req_selection_index",
		Status:          pb.RechargeRequestStatus_RECHARGE_PENDING_SELECTION,
		SelectionHeight: 10,
		NativeChain:     "eth",
		NativeTxHash:    "0xsel",
	}
	if err := setRechargeRequestWithIndexes(sv, req, 3); err != nil {
		t.Fatalf("set request: %v", err)
	}
	dueKey := keys.KeyRechargeSelectionDue(12, req.RequestId)
	if raw, ok, _ := sv.Get(dueKey); !ok || string(raw) != req.RequestId {
		t.Fatalf("expected due index %s, got ok=%v raw=%q", dueKey, ok, string(raw))
	}

	updated := proto.Clone(req).(*pb.RechargeRequest)
	updated.Status = pb.RechargeRequestStatus_RECHARGE_VOTING
	if err := setRechargeRequestWithIndexes(sv, updated, 3); err != nil {
		t.Fatalf("update request: %v", err)
	}
	if _, ok, _ := sv.Get(dueKey); ok {
		t.Fatalf("expected due index removed after status change")
	}
}

func TestChallengeArbitratorIndexTracksOnlyActiveUnvotedAssignments(t *testing.T) {
	sv := NewMockStateView()
	challenge := &pb.ChallengeRecord{
		ChallengeId: "challenge-index", RequestId: "request-index",
		Status:           pb.ChallengeStatus_CHALLENGE_ACTIVE,
		Arbitrators:      []string{"alice", "bob"},
		ArbitrationVotes: []*pb.WitnessVote{{WitnessAddress: "bob"}},
	}
	if err := setChallengeRecordWithIndexes(sv, challenge, 1); err != nil {
		t.Fatalf("set challenge: %v", err)
	}
	if raw, ok, _ := sv.Get(keys.KeyArbitratorChallenge("alice", challenge.ChallengeId)); !ok || string(raw) != challenge.ChallengeId {
		t.Fatalf("alice assignment missing: ok=%v raw=%q", ok, string(raw))
	}
	if _, ok, _ := sv.Get(keys.KeyArbitratorChallenge("bob", challenge.ChallengeId)); ok {
		t.Fatal("already-voted arbitrator remained indexed")
	}

	finalized := proto.Clone(challenge).(*pb.ChallengeRecord)
	finalized.Status = pb.ChallengeStatus_CHALLENGE_FINALIZED
	finalized.Finalized = true
	if err := setChallengeRecordWithIndexes(sv, finalized, 1); err != nil {
		t.Fatalf("finalize challenge: %v", err)
	}
	if _, ok, _ := sv.Get(keys.KeyArbitratorChallenge("alice", challenge.ChallengeId)); ok {
		t.Fatal("finalized challenge assignment remained indexed")
	}
}

func TestRechargeRecentQueryIndexesAreSlimAndTrackTerminalTransition(t *testing.T) {
	sv := NewMockStateView()
	req := &pb.RechargeRequest{ArbitrationProtocolVersion: 1, FeeEscrow: "0",
		RequestId: "request-query", NativeChain: "ETH", VaultId: 2,
		Status: pb.RechargeRequestStatus_RECHARGE_VOTING, CreateHeight: 10,
	}
	if err := setRechargeRequestWithIndexes(sv, req, 1); err != nil {
		t.Fatalf("set active request: %v", err)
	}
	activeKeys := []string{
		keys.KeyRechargeQueryRecentGlobal(false, 10, req.RequestId),
		keys.KeyRechargeQueryRecentChain("ETH", false, 10, req.RequestId),
		keys.KeyRechargeQueryRecentVault(2, false, 10, req.RequestId),
		keys.KeyRechargeQueryRecentChainVault("ETH", 2, false, 10, req.RequestId),
	}
	for _, key := range activeKeys {
		ref, ok, _ := sv.Get(key)
		if !ok || string(ref) != keys.KeyRechargeRequest(req.RequestId) {
			t.Fatalf("active slim index missing: key=%s ok=%v ref=%q", key, ok, string(ref))
		}
	}

	terminal := proto.Clone(req).(*pb.RechargeRequest)
	terminal.Status = pb.RechargeRequestStatus_RECHARGE_REJECTED
	terminal.FinalizeHeight = 20
	if err := setRechargeRequestWithIndexes(sv, terminal, 1); err != nil {
		t.Fatalf("set terminal request: %v", err)
	}
	for _, key := range activeKeys {
		if _, ok, _ := sv.Get(key); ok {
			t.Fatalf("active query index remained after terminal transition: %s", key)
		}
	}
	terminalKey := keys.KeyRechargeQueryRecentChainVault("ETH", 2, true, 20, req.RequestId)
	if ref, ok, _ := sv.Get(terminalKey); !ok || string(ref) != keys.KeyRechargeRequest(req.RequestId) {
		t.Fatalf("terminal query index missing: ok=%v ref=%q", ok, string(ref))
	}
}

func TestRechargeChallengeDueIndexTracksChallengePeriod(t *testing.T) {
	sv := NewMockStateView()
	req := &pb.RechargeRequest{ArbitrationProtocolVersion: 1, FeeEscrow: "0",
		RequestId:      "req_challenge_due_index",
		Status:         pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD,
		DeadlineHeight: 42,
		NativeChain:    "eth",
		NativeTxHash:   "0xchal",
	}
	if err := setRechargeRequestWithIndexes(sv, req, 1); err != nil {
		t.Fatalf("set request: %v", err)
	}
	dueKey := keys.KeyRechargeChallengeDue(43, req.RequestId)
	if raw, ok, _ := sv.Get(dueKey); !ok || string(raw) != req.RequestId {
		t.Fatalf("expected challenge due index %s, got ok=%v raw=%q", dueKey, ok, string(raw))
	}

	updated := proto.Clone(req).(*pb.RechargeRequest)
	updated.Status = pb.RechargeRequestStatus_RECHARGE_CHALLENGED
	if err := setRechargeRequestWithIndexes(sv, updated, 1); err != nil {
		t.Fatalf("update request: %v", err)
	}
	if _, ok, _ := sv.Get(dueKey); ok {
		t.Fatalf("expected challenge due index removed after status change")
	}
}

func TestChallengeSelectionDueIndexTracksPendingSelection(t *testing.T) {
	sv := NewMockStateView()
	ch := &pb.ChallengeRecord{
		ChallengeId:     "ch_selection_index",
		RequestId:       "req_selection_index",
		Status:          pb.ChallengeStatus_CHALLENGE_PENDING_SELECTION,
		SelectionHeight: 30,
	}
	if err := setChallengeRecordWithIndexes(sv, ch, 3); err != nil {
		t.Fatalf("set challenge: %v", err)
	}
	dueKey := keys.KeyChallengeSelectionDue(32, ch.ChallengeId)
	if raw, ok, _ := sv.Get(dueKey); !ok || string(raw) != ch.ChallengeId {
		t.Fatalf("expected challenge due index %s, got ok=%v raw=%q", dueKey, ok, string(raw))
	}

	updated := proto.Clone(ch).(*pb.ChallengeRecord)
	updated.Status = pb.ChallengeStatus_CHALLENGE_ACTIVE
	if err := setChallengeRecordWithIndexes(sv, updated, 3); err != nil {
		t.Fatalf("update challenge: %v", err)
	}
	if _, ok, _ := sv.Get(dueKey); ok {
		t.Fatalf("expected challenge due index removed after status change")
	}
}

func TestChallengeTimeoutDueIndexTracksActiveAndFinalized(t *testing.T) {
	sv := NewMockStateView()
	ch := &pb.ChallengeRecord{
		ChallengeId:    "ch_timeout_index",
		RequestId:      "req_timeout_index",
		Status:         pb.ChallengeStatus_CHALLENGE_ACTIVE,
		DeadlineHeight: 42,
	}
	if err := setChallengeRecordWithTimeoutIndexes(sv, ch, 1, 10, 0); err != nil {
		t.Fatalf("set active challenge: %v", err)
	}
	activeDueKey := keys.KeyChallengeTimeoutDue(43, ch.ChallengeId)
	if raw, ok, _ := sv.Get(activeDueKey); !ok || string(raw) != ch.ChallengeId {
		t.Fatalf("expected active timeout due index %s, got ok=%v raw=%q", activeDueKey, ok, string(raw))
	}

	finalized := proto.Clone(ch).(*pb.ChallengeRecord)
	finalized.Status = pb.ChallengeStatus_CHALLENGE_FINALIZED
	finalized.Finalized = true
	finalized.Reason = "large reason"
	finalized.Evidence = "large evidence"
	finalized.Arbitrators = []string{"arb1", "arb2"}
	finalized.ArbitrationVotes = []*pb.WitnessVote{{WitnessAddress: "arb1", VoteType: pb.WitnessVoteType_VOTE_PASS}}
	finalized.StakeAmount = "123"
	sv.Set(keys.KeyArbitrationVote(ch.ChallengeId, "arb1"), []byte("vote-1"))
	sv.Set(keys.KeyArbitrationVote(ch.ChallengeId, "orphaned-arb"), []byte("vote-2"))
	sv.Set(keys.KeyArbitrationVote("other-challenge", "arb1"), []byte("keep"))
	if err := setChallengeRecordWithTimeoutIndexes(sv, finalized, 1, 10, 0); err != nil {
		t.Fatalf("set finalized challenge: %v", err)
	}
	if _, ok, _ := sv.Get(activeDueKey); ok {
		t.Fatalf("expected timeout due index removed after finalization")
	}
	rawHot, ok, _ := sv.Get(keys.KeyChallengeRecord(ch.ChallengeId))
	if !ok {
		t.Fatalf("expected compact finalized challenge marker")
	}
	var hot pb.ChallengeRecord
	if err := proto.Unmarshal(rawHot, &hot); err != nil {
		t.Fatalf("unmarshal compact challenge: %v", err)
	}
	if hot.StakeAmount != "0" || hot.Reason != "" || hot.Evidence != "" || len(hot.ArbitrationVotes) != 0 || len(hot.Arbitrators) != 0 {
		t.Fatalf("challenge hot marker not compact enough: stake=%s reason=%q evidence=%q votes=%d arbs=%d", hot.StakeAmount, hot.Reason, hot.Evidence, len(hot.ArbitrationVotes), len(hot.Arbitrators))
	}
	rawArchive, ok, _ := sv.Get(keys.KeyArchiveChallengeRecord(ch.ChallengeId))
	if !ok {
		t.Fatalf("expected archived finalized challenge")
	}
	var archived pb.ChallengeRecord
	if err := proto.Unmarshal(rawArchive, &archived); err != nil {
		t.Fatalf("unmarshal archived challenge: %v", err)
	}
	if archived.StakeAmount != "123" || archived.Reason == "" || archived.Evidence == "" || len(archived.ArbitrationVotes) != 1 || len(archived.Arbitrators) != 2 {
		t.Fatalf("archived challenge lost details: stake=%s reason=%q evidence=%q votes=%d arbs=%d", archived.StakeAmount, archived.Reason, archived.Evidence, len(archived.ArbitrationVotes), len(archived.Arbitrators))
	}
	for _, arbitrator := range []string{"arb1", "orphaned-arb"} {
		if _, exists, _ := sv.Get(keys.KeyArbitrationVote(ch.ChallengeId, arbitrator)); exists {
			t.Fatalf("terminal arbitration vote for %s was retained", arbitrator)
		}
	}
	if raw, exists, _ := sv.Get(keys.KeyArbitrationVote("other-challenge", "arb1")); !exists || string(raw) != "keep" {
		t.Fatalf("unrelated arbitration vote was removed: exists=%t raw=%q", exists, string(raw))
	}
	if err := setChallengeRecordWithTimeoutIndexes(sv, &hot, 1, 10, 0); err != nil {
		t.Fatalf("replay compact finalized challenge: %v", err)
	}
	replayedArchive, exists, _ := sv.Get(keys.KeyArchiveChallengeRecord(ch.ChallengeId))
	if !exists || proto.Unmarshal(replayedArchive, &archived) != nil || archived.Reason == "" || len(archived.ArbitrationVotes) != 1 {
		t.Fatalf("compact terminal replay overwrote full challenge archive: %+v", &archived)
	}
}

func TestApplyWriteOpWithRechargeIndexesClampsOverdueChallengeTimeout(t *testing.T) {
	sv := NewMockStateView()
	ch := &pb.ChallengeRecord{
		ChallengeId:    "ch_overdue_timeout",
		RequestId:      "req_overdue_timeout",
		Status:         pb.ChallengeStatus_CHALLENGE_ACTIVE,
		DeadlineHeight: 10,
	}
	if err := setChallengeRecordWithTimeoutIndexes(sv, ch, 1, 0, 0); err != nil {
		t.Fatalf("seed active challenge: %v", err)
	}
	originalDueKey := keys.KeyChallengeTimeoutDue(11, ch.ChallengeId)
	if _, ok, _ := sv.Get(originalDueKey); !ok {
		t.Fatalf("expected original due index")
	}
	data, err := proto.Marshal(ch)
	if err != nil {
		t.Fatal(err)
	}
	if err := applyWriteOpWithRechargeIndexes(sv, WriteOp{Key: keys.KeyChallengeRecord(ch.ChallengeId), Value: data}, 1, 0, 20); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := sv.Get(originalDueKey); ok {
		t.Fatalf("expected original overdue index removed")
	}
	clampedDueKey := keys.KeyChallengeTimeoutDue(20, ch.ChallengeId)
	if raw, ok, _ := sv.Get(clampedDueKey); !ok || string(raw) != ch.ChallengeId {
		t.Fatalf("expected clamped timeout due index %s, got ok=%v raw=%q", clampedDueKey, ok, string(raw))
	}
}

func TestApplyPendingRechargeSelectionsUsesDueIndex(t *testing.T) {
	cfg := witness.DefaultConfig()
	cfg.InitialWitnessCount = 5
	cfg.MinSelectedWitnessCount = 4
	cfg.SelectionBeaconDepth = 1
	cfg.BeaconMixBlocks = 3
	svc := witness.NewService(cfg)
	defer svc.Stop()
	sv := NewMockStateView()
	seedStateSelectionWitnesses(t, sv, "w1", "w2", "w3", "w4", "w5")
	                                                            
	svc.LoadWitness(&pb.WitnessInfo{Address: "cache-only", StakeAmount: "999999999999999", Status: pb.WitnessStatus_WITNESS_ACTIVE})
	for h := uint64(20); h <= 22; h++ {
		sv.Set(keys.KeyBlockHashByHeight(h), []byte{byte(h)})
	}
	req := &pb.RechargeRequest{ArbitrationProtocolVersion: 1, FeeEscrow: "0",
		RequestId:       "req_due_selection",
		Status:          pb.RechargeRequestStatus_RECHARGE_PENDING_SELECTION,
		SelectionHeight: 20,
		NativeChain:     "eth",
		NativeTxHash:    "0xdue",
	}
	if err := setRechargeRequestWithIndexes(sv, req, cfg.BeaconMixBlocks); err != nil {
		t.Fatalf("set request: %v", err)
	}

	x := &Executor{WitnessService: svc}
	if err := x.applyPendingRechargeSelections(sv, 21); err != nil {
		t.Fatal(err)
	}
	raw, _, _ := sv.Get(keys.KeyRechargeRequest(req.RequestId))
	var before pb.RechargeRequest
	if err := proto.Unmarshal(raw, &before); err != nil {
		t.Fatal(err)
	}
	if before.Status != pb.RechargeRequestStatus_RECHARGE_PENDING_SELECTION {
		t.Fatalf("request should not be selected before due height, got %s", before.Status)
	}

	if err := x.applyPendingRechargeSelections(sv, 22); err != nil {
		t.Fatal(err)
	}
	raw, _, _ = sv.Get(keys.KeyRechargeRequest(req.RequestId))
	var after pb.RechargeRequest
	if err := proto.Unmarshal(raw, &after); err != nil {
		t.Fatal(err)
	}
	if after.Status != pb.RechargeRequestStatus_RECHARGE_VOTING || len(after.SelectedWitnesses) != 5 {
		t.Fatalf("request not selected at due height: status=%s witnesses=%v", after.Status, after.SelectedWitnesses)
	}
	if _, ok, _ := sv.Get(keys.KeyRechargeSelectionDue(22, req.RequestId)); ok {
		t.Fatalf("due index should be removed after selection")
	}
	for _, address := range after.SelectedWitnesses {
		if address == "cache-only" || address == "metadata-only" {
			t.Fatalf("selected a candidate without StateView-backed stake: %s", address)
		}
		if raw, ok, _ := sv.Get(keys.KeyWitnessAssigned(address, req.RequestId)); !ok || string(raw) != req.RequestId {
			t.Fatalf("missing assignment history for %s: ok=%v raw=%q", address, ok, string(raw))
		}
	}
}

func TestApplyPendingRechargeSelectionsRejectsUnbackedStakeMetadata(t *testing.T) {
	cfg := witness.DefaultConfig()
	cfg.BeaconMixBlocks = 1
	svc := witness.NewService(cfg)
	defer svc.Stop()
	sv := newMemStateView()
	seedStateSelectionWitnesses(t, sv, "w1", "w2", "w3", "w4")
	unbacked, err := proto.Marshal(&pb.WitnessInfo{Address: "metadata-only", StakeAmount: "999999999999999", Status: pb.WitnessStatus_WITNESS_ACTIVE})
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyWitnessInfo("metadata-only"), unbacked)
	req := &pb.RechargeRequest{ArbitrationProtocolVersion: 1, FeeEscrow: "0", RequestId: "unbacked-candidate", Status: pb.RechargeRequestStatus_RECHARGE_PENDING_SELECTION, SelectionHeight: 10}
	if err := setRechargeRequestWithIndexes(sv, req, 1); err != nil {
		t.Fatal(err)
	}
	before, _, _ := sv.Get(keys.KeyRechargeRequest(req.RequestId))
	if err := (&Executor{WitnessService: svc}).applyPendingRechargeSelections(sv, 10); err == nil {
		t.Fatal("unbacked stake metadata was accepted")
	}
	after, _, _ := sv.Get(keys.KeyRechargeRequest(req.RequestId))
	if string(before) != string(after) {
		t.Fatal("failed selection mutated the request")
	}
	if _, ok, _ := sv.Get(keys.KeyRechargeSelectionDue(10, req.RequestId)); !ok {
		t.Fatal("failed selection lost its retryable due index")
	}
	for _, a := range []string{"w1", "w2", "w3", "w4"} {
		data, _, _ := sv.Get(keys.KeyWitnessInfo(a))
		info := &pb.WitnessInfo{}
		if err := proto.Unmarshal(data, info); err != nil {
			t.Fatal(err)
		}
		if info.ActiveCaseCount != 0 || (info.ReservedStakeAmount != "" && info.ReservedStakeAmount != "0") {
			t.Fatalf("failed selection partially reserved %s", a)
		}
	}
}

func TestApplyPendingRechargeSelectionsRetriesAfterSelectionFailure(t *testing.T) {
	cfg := witness.DefaultConfig()
	cfg.InitialWitnessCount = 4
	cfg.MinSelectedWitnessCount = 4
	cfg.BeaconMixBlocks = 1
	svc := witness.NewService(cfg)
	defer svc.Stop()

	sv := NewMockStateView()
	sv.Set(keys.KeyBlockHashByHeight(10), []byte("h10"))
	req := &pb.RechargeRequest{ArbitrationProtocolVersion: 1, FeeEscrow: "0",
		RequestId:       "req_retry_selection",
		Status:          pb.RechargeRequestStatus_RECHARGE_PENDING_SELECTION,
		SelectionHeight: 10,
		NativeChain:     "eth",
		NativeTxHash:    "0xretry",
	}
	if err := setRechargeRequestWithIndexes(sv, req, cfg.BeaconMixBlocks); err != nil {
		t.Fatalf("set request: %v", err)
	}

	x := &Executor{WitnessService: svc}
	if err := x.applyPendingRechargeSelections(sv, 10); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := sv.Get(keys.KeyRechargeSelectionDue(10, req.RequestId)); ok {
		t.Fatalf("due index at failed height should be removed")
	}
	retryHeight := uint64(10) + cfg.RetryIntervalBlocks
	if raw, ok, _ := sv.Get(keys.KeyRechargeSelectionDue(retryHeight, req.RequestId)); !ok || string(raw) != req.RequestId {
		t.Fatalf("expected delayed retry due index at %d, got ok=%v raw=%q", retryHeight, ok, string(raw))
	}
	if _, ok, _ := sv.Get(keys.KeyRechargeSelectionDue(11, req.RequestId)); ok {
		t.Fatal("insufficient candidates must not cause per-block retries")
	}
	raw, _, _ := sv.Get(keys.KeyRechargeRequest(req.RequestId))
	var pending pb.RechargeRequest
	if err := proto.Unmarshal(raw, &pending); err != nil {
		t.Fatal(err)
	}
	if pending.Status != pb.RechargeRequestStatus_RECHARGE_PENDING_SELECTION {
		t.Fatalf("request should remain pending after failed selection, got %s", pending.Status)
	}

	seedStateSelectionWitnesses(t, sv, "w1", "w2", "w3", "w4")
	if err := x.applyPendingRechargeSelections(sv, retryHeight-1); err != nil {
		t.Fatal(err)
	}
	if got := loadRechargeRequestAtKey(sv, keys.KeyRechargeRequest(req.RequestId)); got == nil || got.Status != pb.RechargeRequestStatus_RECHARGE_PENDING_SELECTION {
		t.Fatal("newly available candidates bypassed retry deadline")
	}
	sv.Set(keys.KeyBlockHashByHeight(retryHeight), []byte("retry-beacon"))
	if err := x.applyPendingRechargeSelections(sv, retryHeight); err != nil {
		t.Fatal(err)
	}
	raw, _, _ = sv.Get(keys.KeyRechargeRequest(req.RequestId))
	var selected pb.RechargeRequest
	if err := proto.Unmarshal(raw, &selected); err != nil {
		t.Fatal(err)
	}
	if selected.Status != pb.RechargeRequestStatus_RECHARGE_VOTING || len(selected.SelectedWitnesses) != 4 {
		t.Fatalf("request should be selected on retry, status=%s witnesses=%v", selected.Status, selected.SelectedWitnesses)
	}
	if _, ok, _ := sv.Get(keys.KeyRechargeSelectionDue(retryHeight, req.RequestId)); ok {
		t.Fatalf("retry due index should be removed after selection")
	}
}

func TestApplyPendingRechargeSelectionsRetriesBelowMinimumWithoutShelving(t *testing.T) {
	cfg := witness.DefaultConfig()
	cfg.InitialWitnessCount = 4
	cfg.MinSelectedWitnessCount = 4
	cfg.BeaconMixBlocks = 1
	svc := witness.NewService(cfg)
	defer svc.Stop()
	sv := NewMockStateView()
	seedStateSelectionWitnesses(t, sv, "w1", "w2", "w3")
	sv.Set(keys.KeyBlockHashByHeight(20), []byte("h20"))
	req := &pb.RechargeRequest{ArbitrationProtocolVersion: 1, FeeEscrow: "0",
		RequestId:       "req_recovered_quorum_drop",
		Status:          pb.RechargeRequestStatus_RECHARGE_PENDING_SELECTION,
		SelectionHeight: 20,
		ShelveHeight:    10,
		Round:           2,
		NativeChain:     "eth",
		NativeTxHash:    "0xquorumdrop",
	}
	if err := setRechargeRequestWithIndexes(sv, req, cfg.BeaconMixBlocks); err != nil {
		t.Fatalf("set request: %v", err)
	}

	x := &Executor{WitnessService: svc}
	if err := x.applyPendingRechargeSelections(sv, 20); err != nil {
		t.Fatal(err)
	}
	raw, _, _ := sv.Get(keys.KeyRechargeRequest(req.RequestId))
	var got pb.RechargeRequest
	if err := proto.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != pb.RechargeRequestStatus_RECHARGE_PENDING_SELECTION || got.Round != req.Round {
		t.Fatalf("request status=%s round=%d, want PENDING_SELECTION round=%d", got.Status, got.Round, req.Round)
	}
	if got.SelectionHeight != 40 || got.ShelveHeight != req.ShelveHeight {
		t.Fatalf("retry heights selection=%d wait_start=%d", got.SelectionHeight, got.ShelveHeight)
	}
	if _, ok, _ := sv.Get(keys.KeyRechargeSelectionDue(21, req.RequestId)); ok {
		t.Fatal("request below minimum must not retry selection every block")
	}
	if due, ok, _ := sv.Get(keys.KeyRechargeSelectionDue(40, req.RequestId)); !ok || string(due) != req.RequestId {
		t.Fatalf("missing delayed retry index: ok=%v raw=%q", ok, string(due))
	}
}

func TestApplyPendingRechargeSelectionsRejectsAfterMaximumWait(t *testing.T) {
	cfg := witness.DefaultConfig()
	cfg.InitialWitnessCount = 4
	cfg.MinSelectedWitnessCount = 4
	cfg.BeaconMixBlocks = 1
	cfg.MaxShelveWaitBlocks = 5
	svc := witness.NewService(cfg)
	defer svc.Stop()
	sv := NewMockStateView()
	seedStateSelectionWitnesses(t, sv, "w1", "w2", "w3")
	sv.Set(keys.KeyBlockHashByHeight(20), []byte("h20"))
	req := &pb.RechargeRequest{ArbitrationProtocolVersion: 1, FeeEscrow: "0",
		RequestId:       "req_reselection_wait_expired",
		Status:          pb.RechargeRequestStatus_RECHARGE_PENDING_SELECTION,
		SelectionHeight: 20,
		ShelveHeight:    10,
		Round:           2,
		NativeChain:     "eth",
		NativeTxHash:    "0xexpired",
	}
	if err := setRechargeRequestWithIndexes(sv, req, cfg.BeaconMixBlocks); err != nil {
		t.Fatalf("set request: %v", err)
	}

	if err := (&Executor{WitnessService: svc}).applyPendingRechargeSelections(sv, 20); err != nil {
		t.Fatal(err)
	}
	raw, _, _ := sv.Get(keys.KeyRechargeRequest(req.RequestId))
	var got pb.RechargeRequest
	if err := proto.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != pb.RechargeRequestStatus_RECHARGE_REJECTED || got.FinalizeHeight != 20 {
		t.Fatalf("expired wait status=%s finalize=%d", got.Status, got.FinalizeHeight)
	}
	if _, ok, _ := sv.Get(keys.KeyRechargeSelectionDue(20, req.RequestId)); ok {
		t.Fatal("terminal request retained selection due index")
	}
}

func TestApplyPendingArbitrationSelectionsUsesDueIndex(t *testing.T) {
	cfg := witness.DefaultConfig()
	cfg.InitialWitnessCount = 6
	cfg.MinSelectedWitnessCount = 4
	cfg.BeaconMixBlocks = 3
	svc := witness.NewService(cfg)
	defer svc.Stop()
	sv := NewMockStateView()
	seedStateSelectionWitnesses(t, sv, "w1", "w2", "w3", "w4", "w5", "w6")
	for h := uint64(40); h <= 42; h++ {
		sv.Set(keys.KeyBlockHashByHeight(h), []byte{byte(h)})
	}
	req := &pb.RechargeRequest{ArbitrationProtocolVersion: 1, FeeEscrow: "0",
		RequestId:         "req_due_arbitration",
		Status:            pb.RechargeRequestStatus_RECHARGE_CHALLENGED,
		ChallengeId:       "ch_due_arbitration",
		NativeChain:       "eth",
		NativeTxHash:      "0xarb",
		SelectedWitnesses: []string{"w1", "w2"},
	}
	reqData, _ := proto.Marshal(req)
	sv.Set(keys.KeyRechargeRequest(req.RequestId), reqData)
	ch := &pb.ChallengeRecord{
		ChallengeId:     req.ChallengeId,
		RequestId:       req.RequestId,
		Status:          pb.ChallengeStatus_CHALLENGE_PENDING_SELECTION,
		SelectionHeight: 40,
	}
	if err := setChallengeRecordWithIndexes(sv, ch, cfg.BeaconMixBlocks); err != nil {
		t.Fatalf("set challenge: %v", err)
	}

	x := &Executor{WitnessService: svc}
	if err := x.applyPendingArbitrationSelections(sv, 41); err != nil {
		t.Fatal(err)
	}
	raw, _, _ := sv.Get(keys.KeyChallengeRecord(ch.ChallengeId))
	var before pb.ChallengeRecord
	if err := proto.Unmarshal(raw, &before); err != nil {
		t.Fatal(err)
	}
	if before.Status != pb.ChallengeStatus_CHALLENGE_PENDING_SELECTION {
		t.Fatalf("challenge should not be selected before due height, got %s", before.Status)
	}

	if err := x.applyPendingArbitrationSelections(sv, 42); err != nil {
		t.Fatal(err)
	}
	raw, _, _ = sv.Get(keys.KeyChallengeRecord(ch.ChallengeId))
	var after pb.ChallengeRecord
	if err := proto.Unmarshal(raw, &after); err != nil {
		t.Fatal(err)
	}
	if after.Status != pb.ChallengeStatus_CHALLENGE_ACTIVE || len(after.Arbitrators) == 0 {
		t.Fatalf("challenge not selected at due height: status=%s arbitrators=%v", after.Status, after.Arbitrators)
	}
	if _, ok, _ := sv.Get(keys.KeyChallengeSelectionDue(42, ch.ChallengeId)); ok {
		t.Fatalf("challenge due index should be removed after selection")
	}
}

func TestApplyPendingArbitrationSelectionsEmptyCommitteeWaitsForFullRound(t *testing.T) {
	cfg := witness.DefaultConfig()
	cfg.BeaconMixBlocks = 1
	svc := witness.NewService(cfg)
	defer svc.Stop()
	sv := NewMockStateView()
	sv.Set(keys.KeyBlockHashByHeight(50), []byte("h50"))
	req := &pb.RechargeRequest{FeeEscrow: "0", RequestId: "req_empty_arbitration", Status: pb.RechargeRequestStatus_RECHARGE_CHALLENGED,
		ChallengeId: "ch_empty_arbitration", ArbitrationProtocolVersion: 1}
	if err := setRechargeRequestWithIndexes(sv, req, 1); err != nil {
		t.Fatal(err)
	}
	ch := &pb.ChallengeRecord{ChallengeId: req.ChallengeId, RequestId: req.RequestId,
		Status: pb.ChallengeStatus_CHALLENGE_PENDING_SELECTION, SelectionHeight: 50,
		StakeAmount: "1000000000", RequiredStake: "1000000000"}
	if err := setChallengeRecordWithIndexes(sv, ch, 1); err != nil {
		t.Fatal(err)
	}
	x := &Executor{WitnessService: svc}
	if err := x.applyPendingArbitrationSelections(sv, 50); err != nil {
		t.Fatal(err)
	}
	raw, _, _ := sv.Get(keys.KeyChallengeRecord(ch.ChallengeId))
	waiting := &pb.ChallengeRecord{}
	if err := proto.Unmarshal(raw, waiting); err != nil {
		t.Fatal(err)
	}
	if waiting.Status != pb.ChallengeStatus_CHALLENGE_ACTIVE || waiting.Finalized || waiting.Settled || waiting.ArbitrationRound != 1 || len(waiting.Arbitrators) != 0 || waiting.StakeAmount != ch.StakeAmount {
		t.Fatalf("empty committee must wait without settlement: %+v", waiting)
	}
	if q, err := witness.ArbitrationQuorum(waiting); err != nil || q != 3 {
		t.Fatalf("empty committee lowered quorum: %d %v", q, err)
	}
	if waiting.DeadlineHeight != 50+cfg.ArbitrationPeriodBlocks {
		t.Fatal("empty committee did not receive a full round")
	}
	stored := loadRechargeRequestAtKey(sv, keys.KeyRechargeRequest(req.RequestId))
	if stored == nil || stored.Status != pb.RechargeRequestStatus_RECHARGE_ARBITRATION {
		t.Fatalf("request left active arbitration: %+v", stored)
	}
	if _, ok, _ := sv.Get(keys.KeyChallengeSelectionDue(50, ch.ChallengeId)); ok {
		t.Fatal("consumed selection index retained")
	}
	if _, ok, _ := sv.Get(keys.KeyChallengeSelectionDue(51, ch.ChallengeId)); ok {
		t.Fatal("empty committee selected again every block")
	}
	if _, ok, _ := sv.Get(keys.KeyChallengeTimeoutDue(waiting.DeadlineHeight+1, ch.ChallengeId)); !ok {
		t.Fatal("full-round timeout index missing")
	}
	if _, ok, _ := sv.Get(keys.KeyArchiveChallengeRecord(ch.ChallengeId)); ok {
		t.Fatal("active zero-vote challenge archived")
	}
}

func TestApplyPendingArbitrationSelectionsRetriesThenArchivesWithRealKVMissingSignal(t *testing.T) {
	cfg := witness.DefaultConfig()
	cfg.BeaconMixBlocks = 1
	svc := witness.NewService(cfg)
	defer svc.Stop()
	read := func(string) ([]byte, error) { return nil, pebble.ErrNotFound }
	scan := func(string) (map[string][]byte, error) { return map[string][]byte{}, nil }
	sv := NewStateView(read, scan)
	sv.Set(keys.KeyBlockHashByHeight(50), []byte("h50"))
	req := &pb.RechargeRequest{RequestId: "req_real_kv_missing", Status: pb.RechargeRequestStatus_RECHARGE_CHALLENGED,
		ChallengeId: "ch_real_kv_missing", ArbitrationProtocolVersion: 1, FeeEscrow: "0"}
	if err := setRechargeRequestWithIndexes(sv, req, 1); err != nil {
		t.Fatal(err)
	}
	ch := &pb.ChallengeRecord{ChallengeId: req.ChallengeId, RequestId: req.RequestId,
		Status: pb.ChallengeStatus_CHALLENGE_PENDING_SELECTION, SelectionHeight: 50,
		ArbitrationRound: 3, StakeAmount: "0"}
	if err := setChallengeRecordWithIndexes(sv, ch, 1); err != nil {
		t.Fatal(err)
	}
	x := &Executor{WitnessService: svc}
	if err := x.applyPendingArbitrationSelections(sv, 50); err != nil {
		t.Fatalf("selection retry leaked Pebble missing signal: %v", err)
	}
	raw, exists, err := sv.Get(keys.KeyChallengeRecord(ch.ChallengeId))
	if err != nil || !exists {
		t.Fatalf("pending challenge lost: %v", err)
	}
	pending := &pb.ChallengeRecord{}
	if err := proto.Unmarshal(raw, pending); err != nil {
		t.Fatal(err)
	}
	retryHeight := uint64(50) + cfg.RetryIntervalBlocks
	if pending.Status != pb.ChallengeStatus_CHALLENGE_PENDING_SELECTION || pending.Finalized || pending.ArbitrationRound != 3 || pending.SelectionHeight != retryHeight || len(pending.FinalCourt) != 0 {
		t.Fatalf("court unavailable should retry without freezing or settling: %+v", pending)
	}
	if _, ok, _ := sv.Get(keys.KeyChallengeSelectionDue(50, ch.ChallengeId)); ok {
		t.Fatal("failed selection retained current due index")
	}
	if _, ok, _ := sv.Get(keys.KeyChallengeSelectionDue(retryHeight, ch.ChallengeId)); !ok {
		t.Fatal("delayed third-round retry index missing")
	}
	if archive, exists, err := sv.Get(keys.KeyArchiveChallengeRecord(ch.ChallengeId)); err != nil || exists {
		t.Fatalf("waiting challenge archived or missing signal leaked: %t %x %v", exists, archive, err)
	}
	seedStateSelectionWitnesses(t, sv, "a", "b", "c", "d")
	sv.Set(keys.KeyBlockHashByHeight(retryHeight), []byte("h-retry"))
	if err := x.applyPendingArbitrationSelections(sv, retryHeight); err != nil {
		t.Fatal(err)
	}
	raw, _, _ = sv.Get(keys.KeyChallengeRecord(ch.ChallengeId))
	third := &pb.ChallengeRecord{}
	if err := proto.Unmarshal(raw, third); err != nil {
		t.Fatal(err)
	}
	if third.Status != pb.ChallengeStatus_CHALLENGE_ACTIVE || len(third.FinalCourt) != 4 || third.CourtSnapshotHeight != retryHeight {
		t.Fatalf("court did not freeze when candidates became available: %+v", third)
	}
	if err := x.applyArbitrationTimeouts(sv, third.DeadlineHeight+1); err != nil {
		t.Fatal(err)
	}
	raw, _, _ = sv.Get(keys.KeyChallengeRecord(ch.ChallengeId))
	court := &pb.ChallengeRecord{}
	if err := proto.Unmarshal(raw, court); err != nil {
		t.Fatal(err)
	}
	if court.ArbitrationStage != pb.ArbitrationStage_ARBITRATION_FINAL_COURT {
		t.Fatal("third round did not enter court")
	}
	handler := &ArbitrationVoteTxHandler{witnessSvc: svc}
	for _, a := range court.FinalCourt[:3] {
		tx := &pb.AnyTx{Content: &pb.AnyTx_ArbitrationVoteTx{ArbitrationVoteTx: &pb.ArbitrationVoteTx{
			Base:        &pb.BaseMessage{TxId: "real-kv-vote-" + a, FromAddress: a, ExecutedHeight: court.DeadlineHeight},
			ChallengeId: ch.ChallengeId, BallotRound: court.BallotRound,
			Vote: &pb.WitnessVote{RequestId: req.RequestId, WitnessAddress: a, VoteType: pb.WitnessVoteType_VOTE_FAIL, BallotRound: court.BallotRound},
		}}}
		ops, receipt, err := handler.DryRun(tx, sv)
		if err != nil || receipt == nil || receipt.Status != "SUCCEED" {
			t.Fatalf("court vote leaked real-KV missing signal: %+v %v", receipt, err)
		}
		if len(ops) != 0 {
			t.Fatal("court vote returned already-applied operations")
		}
	}
	archive, exists, err := sv.Get(keys.KeyArchiveChallengeRecord(ch.ChallengeId))
	if err != nil || !exists || len(archive) == 0 {
		t.Fatalf("terminal challenge archive missing: exists=%v bytes=%d err=%v", exists, len(archive), err)
	}
	finalized := &pb.ChallengeRecord{}
	if err := proto.Unmarshal(archive, finalized); err != nil {
		t.Fatal(err)
	}
	if !finalized.Finalized || !finalized.Settled || finalized.FinalVote != pb.WitnessVoteType_VOTE_FAIL || len(finalized.ArbitrationVotes) != 3 {
		t.Fatalf("archive lacks actual three-vote court result: %+v", finalized)
	}
}

func TestApplyRechargeChallengeFinalizationsUsesDueIndex(t *testing.T) {
	cfg := witness.DefaultConfig()
	svc := witness.NewService(cfg)
	defer svc.Stop()

	sv := NewMockStateView()
	req := seedBTCRechargeFinalizationTest(t, sv, "req_due_finalize", pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD)
	req.DeadlineHeight = 41
	req.FinalizeHeight = 0
	if err := setRechargeRequestWithIndexes(sv, req, 1); err != nil {
		t.Fatalf("set request: %v", err)
	}
	dueKey := keys.KeyRechargeChallengeDue(42, req.RequestId)
	if _, ok, _ := sv.Get(dueKey); !ok {
		t.Fatalf("expected challenge due index before finalization")
	}

	x := &Executor{WitnessService: svc}
	if err := x.applyRechargeChallengeFinalizations(sv, 41, 1); err != nil {
		t.Fatalf("apply before due: %v", err)
	}
	if _, credited, _ := sv.Get(keys.KeyRechargeCredited(req.RequestId)); credited {
		t.Fatalf("request should not be credited before due height")
	}
	if err := x.applyRechargeChallengeFinalizations(sv, 42, 1); err != nil {
		t.Fatalf("apply due finalization: %v", err)
	}
	if _, credited, _ := sv.Get(keys.KeyRechargeCredited(req.RequestId)); !credited {
		t.Fatalf("expected credited marker at due height")
	}
	if _, ok, _ := sv.Get(dueKey); ok {
		t.Fatalf("challenge due index should be removed after finalization")
	}
	raw, _, _ := sv.Get(keys.KeyRechargeRequest(req.RequestId))
	var stored pb.RechargeRequest
	if err := proto.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Status != pb.RechargeRequestStatus_RECHARGE_FINALIZED || stored.FinalizeHeight != 42 {
		t.Fatalf("unexpected stored request after finalization: status=%s height=%d", stored.Status, stored.FinalizeHeight)
	}
}

func TestApplyRechargeChallengeFinalizationsPromotesActivatedChallenge(t *testing.T) {
	sv := NewMockStateView()
	req := &pb.RechargeRequest{ArbitrationProtocolVersion: 1, FeeEscrow: "0",
		RequestId:      "req_due_active_challenge",
		Status:         pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD,
		DeadlineHeight: 10,
		ChallengeId:    "ch_due_active",
	}
	if err := setRechargeRequestWithIndexes(sv, req, 1); err != nil {
		t.Fatalf("set request: %v", err)
	}
	challenge := &pb.ChallengeRecord{
		ChallengeId:     req.ChallengeId,
		RequestId:       req.RequestId,
		Status:          pb.ChallengeStatus_CHALLENGE_PENDING_SELECTION,
		SelectionHeight: 8,
	}
	challengeData, _ := proto.Marshal(challenge)
	sv.Set(keys.KeyChallengeRecord(challenge.ChallengeId), challengeData)

	x := &Executor{}
	if err := x.applyRechargeChallengeFinalizations(sv, 11, 1); err != nil {
		t.Fatalf("apply due challenge: %v", err)
	}
	raw, _, _ := sv.Get(keys.KeyRechargeRequest(req.RequestId))
	var stored pb.RechargeRequest
	if err := proto.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Status != pb.RechargeRequestStatus_RECHARGE_CHALLENGED {
		t.Fatalf("expected active challenge to promote request, got %s", stored.Status)
	}
	if _, ok, _ := sv.Get(keys.KeyRechargeChallengeDue(11, req.RequestId)); ok {
		t.Fatalf("challenge due index should be removed after active challenge promotion")
	}
	if _, credited, _ := sv.Get(keys.KeyRechargeCredited(req.RequestId)); credited {
		t.Fatalf("active challenge must not credit recharge")
	}
}

func TestApplyRechargeChallengeFinalizationsRejectsInvalidBTCFinalization(t *testing.T) {
	sv := NewMockStateView()
	req := seedBTCRechargeFinalizationTest(t, sv, "req_due_retry_skip", pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD)
	req.NativeScript = []byte{0x51}
	req.DeadlineHeight = 1
	req.FinalizeHeight = 0
	if err := setRechargeRequestWithIndexes(sv, req, 1); err != nil {
		t.Fatalf("set request: %v", err)
	}

	x := &Executor{}
	if err := x.applyRechargeChallengeFinalizations(sv, 2, 1); err == nil {
		t.Fatal("expected invalid BTC finalization to fail")
	}
	if _, credited, _ := sv.Get(keys.KeyRechargeCredited(req.RequestId)); credited {
		t.Fatalf("skipped finalization must not credit recharge")
	}
	if raw, ok, _ := sv.Get(keys.KeyRechargeChallengeDue(2, req.RequestId)); !ok || string(raw) != req.RequestId {
		t.Fatalf("failed finalization must keep the current due index, got ok=%v raw=%q", ok, string(raw))
	}
	if _, ok, _ := sv.Get(keys.KeyRechargeChallengeDue(3, req.RequestId)); ok {
		t.Fatal("failed BTC finalization must not create a per-block retry index")
	}
}

func TestPersistRechargeRequestDoesNotDowngradeSelectedRequest(t *testing.T) {
	sv := NewMockStateView()
	current := &pb.RechargeRequest{ArbitrationProtocolVersion: 1, FeeEscrow: "0",
		RequestId:         "req_no_downgrade",
		Status:            pb.RechargeRequestStatus_RECHARGE_VOTING,
		SelectionHeight:   30,
		SelectedWitnesses: []string{"w1", "w2", "w3", "w4"},
		DeadlineHeight:    40,
		SelectionBeacon:   []byte("beacon"),
		PassCount:         1,
		RequesterAddress:  "requester",
		ReceiverAddress:   "receiver",
		NativeChain:       "eth",
		NativeTxHash:      "0xstale",
		TokenAddress:      "USDT",
		Amount:            "100",
	}
	if err := setRechargeRequestWithIndexes(sv, current, 1); err != nil {
		t.Fatalf("set current request: %v", err)
	}
	stale := proto.Clone(current).(*pb.RechargeRequest)
	stale.Status = pb.RechargeRequestStatus_RECHARGE_PENDING_SELECTION
	stale.SelectedWitnesses = nil
	stale.DeadlineHeight = 0
	stale.SelectionBeacon = nil

	if err := persistRechargeRequest(sv, stale, 1); err != nil {
		t.Fatalf("persist stale request: %v", err)
	}
	raw, _, _ := sv.Get(keys.KeyRechargeRequest(current.RequestId))
	var got pb.RechargeRequest
	if err := proto.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != pb.RechargeRequestStatus_RECHARGE_VOTING || len(got.SelectedWitnesses) != len(current.SelectedWitnesses) {
		t.Fatalf("stale event downgraded request: %+v", &got)
	}
	if _, ok, _ := sv.Get(keys.KeyRechargeSelectionDue(30, current.RequestId)); ok {
		t.Fatalf("stale event should not recreate selection due index")
	}
}

func TestPersistRechargeRequestAllowsShelvedFutureBeaconReselection(t *testing.T) {
	sv := NewMockStateView()
	current := &pb.RechargeRequest{ArbitrationProtocolVersion: 1, FeeEscrow: "0",
		RequestId:         "req_shelved_reselection_event",
		Status:            pb.RechargeRequestStatus_RECHARGE_SHELVED,
		Round:             1,
		SelectionHeight:   30,
		DeadlineHeight:    40,
		ShelveHeight:      40,
		SelectedWitnesses: []string{"w1", "w2", "w3", "w4"},
		NativeChain:       "eth",
		NativeTxHash:      "0xshelved",
	}
	if err := setRechargeRequestWithIndexes(sv, current, 1); err != nil {
		t.Fatalf("set current request: %v", err)
	}
	pending := proto.Clone(current).(*pb.RechargeRequest)
	pending.Status = pb.RechargeRequestStatus_RECHARGE_PENDING_SELECTION
	pending.Round++
	pending.SelectionHeight = 60
	pending.SelectionBeacon = nil

	if err := persistRechargeRequest(sv, pending, 1); err != nil {
		t.Fatalf("persist reselection: %v", err)
	}
	raw, _, _ := sv.Get(keys.KeyRechargeRequest(current.RequestId))
	var got pb.RechargeRequest
	if err := proto.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != pb.RechargeRequestStatus_RECHARGE_PENDING_SELECTION || got.SelectionHeight != 60 || got.Round != current.Round+1 {
		t.Fatalf("shelved reselection event was not persisted: %+v", &got)
	}
	if due, ok, _ := sv.Get(keys.KeyRechargeSelectionDue(60, current.RequestId)); !ok || string(due) != current.RequestId {
		t.Fatalf("missing reselection due index: ok=%v raw=%q", ok, string(due))
	}
}
