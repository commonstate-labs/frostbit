package vm

import (
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"dex/formal/itftrace"
	"dex/formal/refinement"
	"dex/keys"
	"dex/pb"
	"dex/witness"

	"google.golang.org/protobuf/proto"
)

type formalRechargeChallengeHarness struct {
	sv          StateView
	executor    *Executor
	service     *witness.Service
	requestID   string
	challengeID string
	receiver    string
	token       string
	vaultID     uint32
	height      int64
}

func TestFormalRechargeChallengeLifecycleITFDifferential(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want int
	}{
		{name: "unchallenged-finalization", env: "DEX_FORMAL_ITF_RECHARGE_UNCHALLENGED", want: 3},
		{name: "premature-index", env: "DEX_FORMAL_ITF_RECHARGE_EARLY_INDEX", want: 4},
		{name: "active-challenge-gate", env: "DEX_FORMAL_ITF_RECHARGE_CHALLENGE_GATE", want: 3},
	}
	if os.Getenv(cases[0].env) == "" {
		t.Skip("recharge/challenge ITF paths are set by formal/run-go-differential.ps1")
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runFormalRechargeChallengeTrace(t, os.Getenv(tc.env), tc.want)
		})
	}
}

func runFormalRechargeChallengeTrace(t *testing.T, path string, wantStates int) {
	t.Helper()
	trace, err := itftrace.Load(path)
	if err != nil {
		t.Fatalf("load Quint recharge/challenge ITF trace: %v", err)
	}
	if len(trace.States) != wantStates {
		t.Fatalf("recharge/challenge differential trace states=%d want=%d", len(trace.States), wantStates)
	}
	initial, err := refinement.RechargeChallengeLifecycleFromITF(trace.States[0])
	if err != nil {
		t.Fatal(err)
	}
	h := newFormalRechargeChallengeHarness(t, initial)
	h.assertStateRelation(t, trace.States[0], 0)
	for i := 1; i < len(trace.States); i++ {
		before, err := refinement.RechargeChallengeLifecycleFromITF(trace.States[i-1])
		if err != nil {
			t.Fatal(err)
		}
		after, err := refinement.RechargeChallengeLifecycleFromITF(trace.States[i])
		if err != nil {
			t.Fatal(err)
		}
		h.applyModelStep(t, before, after)
		h.assertStateRelation(t, trace.States[i], i)
	}
}

func newFormalRechargeChallengeHarness(t *testing.T, initial refinement.RechargeChallengeLifecycleState) *formalRechargeChallengeHarness {
	t.Helper()
	sv := newMemStateView()
	requestID := "formal_recharge_" + strings.ToLower(initial.Phase)
	req := seedAccountRechargeFinalizationTest(t, sv, requestID, formalRechargeRequestStatus(initial.RequestStatus))
	req.DeadlineHeight = 9
	req.FinalizeHeight = 0
	req.PassCount = uint32(initial.RequestPass)
	req.FailCount = uint32(initial.RequestFail)

	h := &formalRechargeChallengeHarness{
		sv:        sv,
		requestID: requestID,
		receiver:  req.ReceiverAddress,
		token:     req.TokenAddress,
		vaultID:   req.VaultId,
		height:    initial.Height,
	}

	if initial.ChallengeStatus != "NoChallenge" {
		h.challengeID = "challenge_" + requestID
		req.ChallengeId = h.challengeID
		ch := &pb.ChallengeRecord{
			ChallengeId:       h.challengeID,
			RequestId:         requestID,
			ChallengerAddress: "system",
			Status:            pb.ChallengeStatus_CHALLENGE_ACTIVE,
			SelectionHeight:   1,
			DeadlineHeight:    100,
			ArbitrationRound:  uint32(initial.ArbitrationRound),
			PassCount:         uint32(initial.ArbitrationPass),
			FailCount:         uint32(initial.ArbitrationFail),
		}
		if initial.Phase == "ExpansionStart" || initial.Phase == "QuorumRejectStart" {
			ch.Arbitrators = []string{"arb1", "arb2", "arb3", "arb4", "arb5"}
		}
		switch initial.Phase {
		case "ExpansionStart":
			ch.ArbitrationVotes = []*pb.WitnessVote{{WitnessAddress: "arb1", VoteType: pb.WitnessVoteType_VOTE_PASS}}
		case "QuorumRejectStart":
			ch.ArbitrationVotes = []*pb.WitnessVote{
				{WitnessAddress: "arb1", VoteType: pb.WitnessVoteType_VOTE_FAIL},
				{WitnessAddress: "arb2", VoteType: pb.WitnessVoteType_VOTE_FAIL},
				{WitnessAddress: "arb3", VoteType: pb.WitnessVoteType_VOTE_FAIL},
			}
		}
		formalStoreProto(t, sv, keys.KeyChallengeRecord(ch.ChallengeId), ch)
	}
	formalStoreProto(t, sv, keys.KeyRechargeRequest(req.RequestId), req)

	if initial.ChallengeDue > 0 {
		sv.Set(keys.KeyRechargeChallengeDue(uint64(initial.ChallengeDue), requestID), []byte(requestID))
	}
	if initial.TimeoutDue > 0 {
		sv.Set(keys.KeyChallengeTimeoutIndexBackfillDone(), []byte("1"))
		sv.Set(keys.KeyChallengeTimeoutDue(uint64(initial.TimeoutDue), h.challengeID), []byte(h.challengeID))
	}

	if initial.Phase == "ExpansionStart" || initial.Phase == "ConservativeStart" || initial.Phase == "QuorumRejectStart" {
		cfg := witness.DefaultConfig()
		h.service = witness.NewService(cfg)
		t.Cleanup(h.service.Stop)
		for _, address := range []string{"arb1", "arb2", "arb3", "arb4", "arb5", "arb6", "arb7", "arb8"} {
			h.service.LoadWitness(&pb.WitnessInfo{
				Address: address, StakeAmount: "10000000000", Status: pb.WitnessStatus_WITNESS_ACTIVE,
			})
		}
	}
	h.executor = &Executor{WitnessService: h.service}
	return h
}

func (h *formalRechargeChallengeHarness) applyModelStep(t *testing.T, before, after refinement.RechargeChallengeLifecycleState) {
	t.Helper()
	if reflect.DeepEqual(before, after) {
		if before.Phase == "NoChallengeFinalized" || before.Phase == "EarlyIndexFinalized" {
			req := formalLoadRechargeRequest(t, h.sv, h.requestID)
			if err := applyRechargeFinalized(h.sv, req, uint64(before.Height+1), 1); err != nil {
				t.Fatalf("repeat finalized recharge: %v", err)
			}
		}
		return
	}
	h.height = after.Height
	switch before.Phase {
	case "NoChallengeStart", "EarlyIndexStart", "EarlyIndexRescheduled", "ChallengeGateStart":
		if err := h.executor.applyRechargeChallengeFinalizations(h.sv, uint64(after.Height), 1); err != nil {
			t.Fatalf("apply recharge challenge due phase=%s: %v", before.Phase, err)
		}
	case "ExpansionStart", "ConservativeStart", "QuorumRejectStart":
		if err := h.executor.applyArbitrationTimeouts(h.sv, uint64(after.Height)); err != nil {
			t.Fatalf("apply arbitration timeout phase=%s: %v", before.Phase, err)
		}
	default:
		t.Fatalf("unsupported recharge/challenge model edge: before=%+v after=%+v", before, after)
	}
}

func (h *formalRechargeChallengeHarness) assertStateRelation(t *testing.T, model itftrace.State, step int) {
	t.Helper()
	expected, err := refinement.RechargeChallengeLifecycleFromITF(model)
	if err != nil {
		t.Fatal(err)
	}
	actual := h.productionState(t, expected.Phase)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("recharge/challenge state relation failed step=%d action=%s\nactual:   %+v\nexpected: %+v",
			step, model.Action, actual, expected)
	}
}

func (h *formalRechargeChallengeHarness) productionState(t *testing.T, phase string) refinement.RechargeChallengeLifecycleState {
	t.Helper()
	req := formalLoadRechargeRequest(t, h.sv, h.requestID)
	result := refinement.RechargeChallengeLifecycleState{
		Phase:          phase,
		Height:         h.height,
		RequestStatus:  formalRechargeRequestStatusTag(req.Status),
		RequestPass:    int64(req.PassCount),
		RequestFail:    int64(req.FailCount),
		UserBalance:    formalSmallBalance(t, GetBalance(h.sv, h.receiver, h.token).Balance),
		VaultAvailable: formalSmallStateInt(t, h.sv, keys.KeyFrostVaultAvailableBalance(req.NativeChain, h.token, h.vaultID)),
		TotalSupply:    formalRechargeTotalSupply(t, h.sv, h.token),
		ChallengeDue:   formalDueIndexHeight(t, h.sv, keys.KeyRechargeChallengeDueAllPrefix(), h.requestID),
		TimeoutDue:     formalDueIndexHeight(t, h.sv, keys.KeyChallengeTimeoutDueAllPrefix(), h.challengeID),
		FinalizeHeight: int64(req.FinalizeHeight),
	}
	if _, credited, err := h.sv.Get(keys.KeyRechargeCredited(h.requestID)); err != nil {
		t.Fatal(err)
	} else {
		result.Credited = credited
	}
	if h.challengeID == "" {
		result.ChallengeStatus = "NoChallenge"
		return result
	}
	ch := formalLoadChallengeRecord(t, h.sv, h.challengeID)
	result.ArbitrationRound = int64(ch.ArbitrationRound)
	result.ArbitrationPass = int64(ch.PassCount)
	result.ArbitrationFail = int64(ch.FailCount)
	result.ChallengeSuccess = ch.ChallengeSuccess
	if ch.Finalized || ch.Status == pb.ChallengeStatus_CHALLENGE_FINALIZED {
		result.ChallengeStatus = "ResolvedChallenge"
	} else {
		result.ChallengeStatus = "ActiveChallenge"
	}
	return result
}

func formalRechargeRequestStatus(tag string) pb.RechargeRequestStatus {
	switch tag {
	case "ChallengePeriod":
		return pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD
	case "Challenged":
		return pb.RechargeRequestStatus_RECHARGE_CHALLENGED
	case "RechargeFinalized":
		return pb.RechargeRequestStatus_RECHARGE_FINALIZED
	case "RechargeRejected":
		return pb.RechargeRequestStatus_RECHARGE_REJECTED
	default:
		panic("unknown formal recharge status " + tag)
	}
}

func formalRechargeRequestStatusTag(status pb.RechargeRequestStatus) string {
	switch status {
	case pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD:
		return "ChallengePeriod"
	case pb.RechargeRequestStatus_RECHARGE_CHALLENGED:
		return "Challenged"
	case pb.RechargeRequestStatus_RECHARGE_FINALIZED:
		return "RechargeFinalized"
	case pb.RechargeRequestStatus_RECHARGE_REJECTED:
		return "RechargeRejected"
	default:
		return status.String()
	}
}

func formalStoreProto(t *testing.T, sv StateView, key string, value proto.Message) {
	t.Helper()
	data, err := proto.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(key, data)
}

func formalLoadRechargeRequest(t *testing.T, sv StateView, requestID string) *pb.RechargeRequest {
	t.Helper()
	raw, exists, err := sv.Get(keys.KeyRechargeRequest(requestID))
	if err != nil || !exists {
		t.Fatalf("load recharge request %s: exists=%v err=%v", requestID, exists, err)
	}
	var req pb.RechargeRequest
	if err := proto.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}
	return &req
}

func formalLoadChallengeRecord(t *testing.T, sv StateView, challengeID string) *pb.ChallengeRecord {
	t.Helper()
	raw, exists, err := sv.Get(keys.KeyChallengeRecord(challengeID))
	if err != nil || !exists {
		t.Fatalf("load challenge %s: exists=%v err=%v", challengeID, exists, err)
	}
	var hot pb.ChallengeRecord
	if err := proto.Unmarshal(raw, &hot); err != nil {
		t.Fatal(err)
	}
	if !hot.Finalized && hot.Status != pb.ChallengeStatus_CHALLENGE_FINALIZED {
		return &hot
	}
	archive, archiveExists, err := sv.Get(keys.KeyArchiveChallengeRecord(challengeID))
	if err != nil {
		t.Fatal(err)
	}
	if !archiveExists {
		return &hot
	}
	var full pb.ChallengeRecord
	if err := proto.Unmarshal(archive, &full); err != nil {
		t.Fatal(err)
	}
	return &full
}

func formalDueIndexHeight(t *testing.T, sv StateView, prefix, id string) int64 {
	t.Helper()
	if id == "" {
		return 0
	}
	rows, err := sv.Scan(prefix)
	if err != nil {
		t.Fatal(err)
	}
	found := int64(0)
	for key, value := range rows {
		if string(value) != id || !strings.HasPrefix(key, prefix) {
			continue
		}
		suffix := strings.TrimPrefix(key, prefix)
		if len(suffix) < 21 || suffix[20] != '_' {
			t.Fatalf("malformed due index key %q", key)
		}
		height, err := strconv.ParseInt(suffix[:20], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		if found != 0 {
			t.Fatalf("multiple due indexes for %s: %d and %d", id, found, height)
		}
		found = height
	}
	return found
}

func formalSmallStateInt(t *testing.T, sv StateView, key string) int64 {
	t.Helper()
	raw, exists, err := sv.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	if !exists || len(raw) == 0 {
		return 0
	}
	return formalSmallBalance(t, string(raw))
}

func formalRechargeTotalSupply(t *testing.T, sv StateView, token string) int64 {
	t.Helper()
	raw, exists, err := sv.Get(keys.KeyToken(token))
	if err != nil {
		t.Fatal(err)
	}
	if !exists || len(raw) == 0 {
		return 0
	}
	var record pb.Token
	if err := proto.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	return formalSmallBalance(t, record.TotalSupply)
}

func formalSmallBalance(t *testing.T, value string) int64 {
	t.Helper()
	if value == "" {
		return 0
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		t.Fatalf("parse formal balance %q: %v", value, err)
	}
	return parsed
}
