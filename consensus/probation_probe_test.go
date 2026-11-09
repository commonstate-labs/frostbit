package consensus

import (
	"dex/interfaces"
	"dex/logs"
	"dex/types"
	"fmt"
	"reflect"
	"sort"
	"testing"
)

type addressMembershipTracker struct {
	interfaces.LivenessTracker
	states map[string]int
}

type probationProbeReportRecorder struct {
	sampled []string
	offline []string
}

func (r *probationProbeReportRecorder) ReportSampled(addresses []string) {
	r.sampled = append(r.sampled, addresses...)
}

func (r *probationProbeReportRecorder) ReportOffline(addresses []string) {
	r.offline = append(r.offline, addresses...)
}

func (t *addressMembershipTracker) IsActive(address string) bool {
	return t.states[address] == 1
}

func (t *addressMembershipTracker) IsActiveOrProbation(address string) bool {
	state, ok := t.states[address]
	return ok && (state == 0 || state == 1)
}

func (t *addressMembershipTracker) GetMinerState(address string) int {
	if state, ok := t.states[address]; ok {
		return state
	}
	return -1
}

func (t *addressMembershipTracker) GetActiveMiners() []string {
	miners := make([]string, 0, len(t.states))
	for address, state := range t.states {
		if state == 0 || state == 1 {
			miners = append(miners, address)
		}
	}
	sort.Strings(miners)
	return miners
}

func TestProbationProbeReporterGroupBoundedAndFairAtTenThousand(t *testing.T) {
	const activeCount = 10_000
	params := ConsensusPollParams{K: 15, Beta: 15}
	reporterCount := probationProbeReporterCount(params, activeCount)
	if reporterCount != 225 {
		t.Fatalf("reporter budget=%d want 225", reporterCount)
	}
	active := make([]types.NodeID, activeCount)
	for i := range active {
		active[i] = types.NodeID(fmt.Sprintf("active-%05d", i))
	}
	target := types.NodeID("probation-only")
	first := buildProbationProbeReporterGroup(active, target, 1, reporterCount)
	if len(first) != reporterCount {
		t.Fatalf("global probes=%d want=%d", len(first), reporterCount)
	}
	assigned := 0
	for _, reporter := range active {
		if containsNodeID(first, reporter) {
			assigned++
		}
	}
	if assigned != reporterCount {
		t.Fatalf("N=10000/P=1 assigned reporters=%d want=%d", assigned, reporterCount)
	}
	for height := uint64(2); height <= probationProbeGroupHeights; height++ {
		if got := buildProbationProbeReporterGroup(active, target, height, reporterCount); !reflect.DeepEqual(got, first) {
			t.Fatalf("group changed inside ten-height round at height %d: first=%v got=%v", height, first, got)
		}
	}
	second := buildProbationProbeReporterGroup(active, target, probationProbeGroupHeights+1, reporterCount)
	if reflect.DeepEqual(second, first) {
		t.Fatalf("group did not rotate after %d heights: %v", probationProbeGroupHeights, first)
	}

	seen := make(map[types.NodeID]int, activeCount)
	rounds := (activeCount + reporterCount - 1) / reporterCount
	for round := 0; round < rounds; round++ {
		height := uint64(round*probationProbeGroupHeights + 1)
		for _, reporter := range buildProbationProbeReporterGroup(active, target, height, reporterCount) {
			seen[reporter]++
		}
	}
	if len(seen) != activeCount {
		t.Fatalf("reporter rotation starved %d of %d Active identities", activeCount-len(seen), activeCount)
	}
	for reporter, count := range seen {
		if count < 1 || count > 2 {
			t.Fatalf("unfair reporter load %s=%d", reporter, count)
		}
	}
}

func TestProbationProbeReporterBudgetUsesAllActiveInSmallNetwork(t *testing.T) {
	params := ConsensusPollParams{K: 9, Beta: 15}
	if got := probationProbeReporterCount(params, 12); got != 12 {
		t.Fatalf("small-network reporter budget=%d want 12", got)
	}
	if got := probationProbeReporterCount(ConsensusPollParams{K: 1, Beta: 1}, 20); got != probationProbeMinReporters {
		t.Fatalf("minimum reporter budget=%d want %d", got, probationProbeMinReporters)
	}
}

func TestProbationProbeUsesCompleteSortedChainMembership(t *testing.T) {
	tracker := &addressMembershipTracker{states: map[string]int{
		"active-z":  1,
		"active-a":  1,
		"active-m":  1,
		"probation": 0,
		"frozen":    2,
	}}
	qm := &QueryManager{livenessTracker: tracker}
	active, probation := qm.probeMembershipSnapshot()
	if !reflect.DeepEqual(active, []types.NodeID{"active-a", "active-m", "active-z"}) {
		t.Fatalf("chain Active snapshot=%v", active)
	}
	if !reflect.DeepEqual(probation, []types.NodeID{"probation"}) {
		t.Fatalf("chain Probation snapshot=%v", probation)
	}
}

func TestProbationProbeClaimsEachHeightAtMostOnce(t *testing.T) {
	qm := &QueryManager{}
	if !qm.claimProbationProbeHeight(100) {
		t.Fatal("first height claim rejected")
	}
	if qm.claimProbationProbeHeight(100) {
		t.Fatal("same height was claimed twice")
	}
	if qm.claimProbationProbeHeight(99) {
		t.Fatal("stale height was claimed")
	}
	if !qm.claimProbationProbeHeight(101) {
		t.Fatal("next height claim rejected")
	}
}

func TestProbationProbeTimeoutIsPairedOfflineEvidence(t *testing.T) {
	recorder := &probationProbeReportRecorder{}
	qm := &QueryManager{livenessReporter: recorder}
	session := &probationProbeSession{
		requestID: 41,
		targets: map[types.NodeID]struct{}{
			"responsive":  {},
			"timeout":     {},
			"send-failed": {},
		},
	}
	session.responded.Store(types.NodeID("responsive"), struct{}{})
	session.failed.Store(types.NodeID("send-failed"), struct{}{})
	qm.probeSessions.Store(session.requestID, session)

	qm.finishProbationProbe(session)

	if !reflect.DeepEqual(recorder.sampled, []string{"responsive", "timeout"}) {
		t.Fatalf("sampled=%v want responsive+timeout", recorder.sampled)
	}
	if !reflect.DeepEqual(recorder.offline, []string{"timeout"}) {
		t.Fatalf("offline=%v want timeout", recorder.offline)
	}
}

func TestPreferencePollSamplesOnlyActivePeers(t *testing.T) {
	block := &types.Block{
		ID: "block-1-peer-active-w0-active-only",
		Header: types.BlockHeader{
			Height:   1,
			ParentID: "genesis",
			Proposer: "peer-active",
		},
	}
	store := &querySeqStore{block: block}
	engine := &querySeqEngine{blockID: block.ID}
	transport := &querySeqTransport{allPeers: []types.NodeID{"peer-probation", "peer-active", "peer-frozen"}}
	qm := NewQueryManager(
		"self",
		transport,
		store,
		engine,
		&ConsensusConfig{K: 3, AlphaPreference: 2, AlphaConfidence: 2, Beta: 15, MaxConcurrentQueries: 8},
		NewEventBus(),
		logs.NewNodeLogger("active-only-sample-test", 0),
	)
	qm.SetLivenessTracker(&addressMembershipTracker{states: map[string]int{
		"self":           1,
		"peer-active":    1,
		"peer-probation": 0,
		"peer-frozen":    2,
	}})
	qm.issueQuery()

	engine.mu.Lock()
	defer engine.mu.Unlock()
	if len(engine.sampleWeights) != 1 {
		t.Fatalf("registered samples=%d want 1", len(engine.sampleWeights))
	}
	weights := engine.sampleWeights[0]
	if len(weights) != 1 || weights["peer-active"] != 3 {
		t.Fatalf("Snowman sample contains non-Active peer: %v", weights)
	}
}

func TestSignedProbationProbeResponseNeverEntersConsensus(t *testing.T) {
	ClearNodePublicKeys()
	SetNodePublicKeyResolver(nil)
	defer func() {
		ClearNodePublicKeys()
		SetNodePublicKeyResolver(nil)
	}()

	key := newTestKeyManager(t)
	SetNodePublicKeyResolver(func(nodeID string) ([]byte, bool) {
		if nodeID == "probation" {
			return key.PublicKeyBytes(), true
		}
		return nil, false
	})
	nonce := queryManagerTestNonce(10)
	session := &probationProbeSession{
		requestID:  77,
		height:     4,
		vrfSeed:    []byte("seed"),
		seqID:      2,
		queryNonce: nonce,
		targets:    map[types.NodeID]struct{}{"probation": {}},
	}
	engine := &queryManagerSigEngine{}
	qm := &QueryManager{
		nodeID: "requester",
		store:  &queryManagerSigStore{blocks: map[string]*types.Block{}},
		engine: engine,
	}
	qm.probeSessions.Store(uint32(77), session)
	digest := queryManagerTestChitDigest(77, "requester", "probation", "", 4, "accepted", 3, nonce)
	sig, err := key.Sign(digest)
	if err != nil {
		t.Fatal(err)
	}
	qm.HandleChit(types.Message{
		From:              "probation",
		RequestID:         77,
		PreferredIDHeight: 4,
		AcceptedID:        "accepted",
		AcceptedHeight:    3,
		QueryNonce:        nonce,
		ChitSignature:     sig,
	})
	if engine.submitted != 0 {
		t.Fatalf("probation probe entered Snowman %d time(s)", engine.submitted)
	}
	if _, ok := session.responded.Load(types.NodeID("probation")); !ok {
		t.Fatal("valid probation reachability response was not recorded")
	}
}

func TestProbationResponseRejectedFromNormalPoll(t *testing.T) {
	engine := &queryManagerSigEngine{}
	qm := &QueryManager{
		nodeID:          "requester",
		store:           &queryManagerSigStore{blocks: map[string]*types.Block{}},
		engine:          engine,
		livenessTracker: &addressMembershipTracker{states: map[string]int{"probation": 0}},
	}
	qm.activePolls.Store(uint32(1), &Poll{
		requestID:  1,
		queryKey:   "query-key",
		height:     1,
		queryNonce: queryManagerTestNonce(1),
		sampledSet: map[types.NodeID]struct{}{"probation": {}},
	})
	qm.HandleChit(types.Message{From: "probation", RequestID: 1})
	if engine.submitted != 0 {
		t.Fatal("probation response was counted in a normal Snowman poll")
	}
}

func TestAcceptedFrontierSamplesOnlyActivePeers(t *testing.T) {
	store := newFrontierCoreTestStore("block-5", 5)
	transport := &frontierCoreTestTransport{peers: []types.NodeID{"active-a", "probation", "active-b"}}
	sm := newFrontierCoreSyncManager(store, transport, 2, 2)
	sm.SetLivenessTracker(&addressMembershipTracker{states: map[string]int{
		"active-a":  1,
		"active-b":  1,
		"probation": 0,
	}})
	session := &SyncSession{ID: 91, Stage: SyncStageFrontier, TargetHeight: 6, PeerAcceptedHeight: 6}
	sm.activeSession = session
	sm.Syncing = true
	if serr := sm.execFrontierStage(session); serr != nil {
		t.Fatalf("frontier start: %v", serr)
	}
	if !reflect.DeepEqual(transport.queryPeers, []types.NodeID{"active-a", "active-b"}) {
		t.Fatalf("frontier queried non-Active peers: %v", transport.queryPeers)
	}
}
