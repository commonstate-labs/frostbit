package consensus

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"dex/logs"
	"dex/types"
)

func newProductionSnowballTestEngine(t *testing.T, cfg ConsensusConfig, blocks ...*types.Block) (*SnowmanEngine, *MemoryBlockStore) {
	t.Helper()
	store := NewMemoryBlockStoreWithConfig().(*MemoryBlockStore)
	for _, block := range blocks {
		if _, err := store.Add(block); err != nil {
			t.Fatalf("add block %s: %v", block.ID, err)
		}
	}
	engine := NewSnowmanEngine(
		"node-test",
		store,
		&cfg,
		NewEventBus(),
		logs.NewNodeLogger("snowball-engine-test", 0),
	).(*SnowmanEngine)
	return engine, store
}

func TestEngineFirstVerifiedCandidateInitializesOneAtomicPreference(t *testing.T) {
	blockA := &types.Block{
		ID:     blockID(1, "first", 0, "ffff"),
		Header: types.BlockHeader{Height: 1, ParentID: "genesis"},
	}
	blockB := &types.Block{
		ID:     blockID(1, "later", 0, "0000"),
		Header: types.BlockHeader{Height: 1, ParentID: "genesis"},
	}
	engine, _ := newProductionSnowballTestEngine(t, ConsensusConfig{
		K: 15, AlphaPreference: 8, AlphaConfidence: 12, Beta: 15,
	}, blockA, blockB)

	const readers = 64
	results := make(chan string, readers)
	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- engine.GetPreference(1)
		}()
	}
	wg.Wait()
	close(results)
	for preference := range results {
		if preference != blockA.ID {
			t.Fatalf("concurrent preference = %s, want first admitted %s", preference, blockA.ID)
		}
	}

	state := engine.GetHeightState(1)
	if state == nil || state.Preference != blockA.ID || state.Confidence != 0 {
		t.Fatalf("candidate admission changed confidence or chose another preference: %+v", state)
	}
	sb := engine.getSnowball(1)
	if sb.GetStrength(blockA.ID) != 0 || sb.GetStrength(blockB.ID) != 0 {
		t.Fatalf("candidate admission added Snowball strength: A=%d B=%d",
			sb.GetStrength(blockA.ID), sb.GetStrength(blockB.ID))
	}
}

func TestEngineSoftThresholdDoesNotCompletePollOrBuildConfidence(t *testing.T) {
	blockA := &types.Block{ID: "block-a", Header: types.BlockHeader{Height: 1, ParentID: "genesis"}}
	blockB := &types.Block{ID: "block-b", Header: types.BlockHeader{Height: 1, ParentID: "genesis"}}
	engine, _ := newProductionSnowballTestEngine(t, ConsensusConfig{
		K: 15, AlphaPreference: 8, AlphaConfidence: 12, Beta: 15,
		QueryTimeout: time.Second,
	}, blockA, blockB)

	firstWeights := make(map[types.NodeID]int, 15)
	for i := 0; i < 8; i++ {
		firstWeights[types.NodeID(fmt.Sprintf("b-%d", i))] = 1
	}
	for i := 0; i < 7; i++ {
		firstWeights[types.NodeID(fmt.Sprintf("a-%d", i))] = 1
	}
	queryKey := engine.RegisterQuery("self", 1, engine.GetPreference(1), 1, 0, nil, firstWeights)
	if queryKey == "" {
		t.Fatal("query registration failed")
	}
	for i := 0; i < 8; i++ {
		engine.SubmitChit(types.NodeID(fmt.Sprintf("b-%d", i)), queryKey, blockB.ID, nil)
	}
	if got := engine.GetActiveQueryCount(); got != 1 {
		t.Fatalf("soft threshold completed poll early: active=%d", got)
	}
	for i := 0; i < 7; i++ {
		engine.SubmitChit(types.NodeID(fmt.Sprintf("a-%d", i)), queryKey, blockA.ID, nil)
	}
	if got := engine.GetActiveQueryCount(); got != 0 {
		t.Fatalf("K responses did not complete poll: active=%d", got)
	}
	sb := engine.getSnowball(1)
	if sb.GetPreference() != blockB.ID || sb.GetStrength(blockB.ID) != 1 {
		t.Fatalf("soft winner did not gain cumulative preference: preference=%s strength=%d",
			sb.GetPreference(), sb.GetStrength(blockB.ID))
	}
	if sb.GetConfidence() != 0 {
		t.Fatalf("soft-only poll built finalization confidence: %d", sb.GetConfidence())
	}

	secondWeights := make(map[types.NodeID]int, 15)
	for i := 0; i < 12; i++ {
		secondWeights[types.NodeID(fmt.Sprintf("strong-a-%d", i))] = 1
	}
	for i := 0; i < 3; i++ {
		secondWeights[types.NodeID(fmt.Sprintf("silent-%d", i))] = 1
	}
	queryKey = engine.RegisterQuery("self", 2, engine.GetPreference(1), 1, 1, nil, secondWeights)
	if queryKey == "" {
		t.Fatal("second query registration failed")
	}
	for i := 0; i < 12; i++ {
		engine.SubmitChit(types.NodeID(fmt.Sprintf("strong-a-%d", i)), queryKey, blockA.ID, nil)
	}
	if got := engine.GetActiveQueryCount(); got != 0 {
		t.Fatalf("hard threshold did not complete poll early: active=%d", got)
	}
	if sb.GetConfidencePreference() != blockA.ID || sb.GetConfidence() != 1 {
		t.Fatalf("hard winner did not start confidence: preference=%s confidence=%d",
			sb.GetConfidencePreference(), sb.GetConfidence())
	}
}

func TestEngineCountsRegisteredReplacementSlotWeights(t *testing.T) {
	blockA := &types.Block{ID: "block-a", Header: types.BlockHeader{Height: 1, ParentID: "genesis"}}
	blockB := &types.Block{ID: "block-b", Header: types.BlockHeader{Height: 1, ParentID: "genesis"}}
	engine, _ := newProductionSnowballTestEngine(t, ConsensusConfig{
		K: 15, AlphaPreference: 8, AlphaConfidence: 12, Beta: 15,
		QueryTimeout: time.Second,
	}, blockA, blockB)

	queryKey := engine.RegisterQuery(
		"self",
		1,
		engine.GetPreference(1),
		1,
		0,
		nil,
		map[types.NodeID]int{"peer-b": 8, "peer-a": 7},
	)
	if queryKey == "" {
		t.Fatal("weighted query registration failed")
	}
	engine.SubmitChit("unsampled", queryKey, blockA.ID, nil)
	if got := engine.GetActiveQueryCount(); got != 1 {
		t.Fatalf("unsampled response completed weighted poll: active=%d", got)
	}
	engine.SubmitChit("peer-b", queryKey, blockB.ID, nil)
	engine.SubmitChit("peer-b", queryKey, blockB.ID, nil)
	if got := engine.GetActiveQueryCount(); got != 1 {
		t.Fatalf("duplicate 8-slot response completed poll before remaining slots: active=%d", got)
	}
	engine.SubmitChit("peer-a", queryKey, blockA.ID, nil)
	if got := engine.GetActiveQueryCount(); got != 0 {
		t.Fatalf("all K weighted slots did not complete poll: active=%d", got)
	}

	sb := engine.getSnowball(1)
	if got := sb.GetLastVotes(); got[blockB.ID] != 8 || got[blockA.ID] != 7 {
		t.Fatalf("weighted votes = %v, want B=8 A=7", got)
	}
	if sb.GetPreference() != blockB.ID || sb.GetStrength(blockB.ID) != 1 {
		t.Fatalf("8-slot soft winner did not gain strength: preference=%s strength=%d",
			sb.GetPreference(), sb.GetStrength(blockB.ID))
	}
	if sb.GetConfidence() != 0 {
		t.Fatalf("soft-only weighted poll built confidence: %d", sb.GetConfidence())
	}

	queryKey = engine.RegisterQuery(
		"self",
		2,
		engine.GetPreference(1),
		1,
		1,
		nil,
		map[types.NodeID]int{"strong-a": 12, "silent": 3},
	)
	if queryKey == "" {
		t.Fatal("second weighted query registration failed")
	}
	engine.SubmitChit("strong-a", queryKey, blockA.ID, nil)
	if got := engine.GetActiveQueryCount(); got != 0 {
		t.Fatalf("12-slot hard winner did not complete poll early: active=%d", got)
	}
	if sb.GetConfidencePreference() != blockA.ID || sb.GetConfidence() != 1 {
		t.Fatalf("12-slot winner did not start confidence: preference=%s confidence=%d",
			sb.GetConfidencePreference(), sb.GetConfidence())
	}
	evidence := engine.collectFinalizationChits(1, blockA.ID)
	if evidence == nil || evidence.TotalVotes != 12 || len(evidence.Chits) != 1 ||
		evidence.Chits[0].SampleWeight != 12 {
		t.Fatalf("weighted observability evidence = %+v, want one 12-slot chit", evidence)
	}
}

func TestEngineWeightedTimeoutKeepsMissingSlotsAsAbstentions(t *testing.T) {
	blockA := &types.Block{ID: "block-a", Header: types.BlockHeader{Height: 1, ParentID: "genesis"}}
	blockB := &types.Block{ID: "block-b", Header: types.BlockHeader{Height: 1, ParentID: "genesis"}}
	engine, _ := newProductionSnowballTestEngine(t, ConsensusConfig{
		K: 15, AlphaPreference: 8, AlphaConfidence: 12, Beta: 15,
		QueryTimeout: time.Millisecond,
	}, blockA, blockB)

	queryKey := engine.RegisterQuery(
		"self",
		1,
		engine.GetPreference(1),
		1,
		0,
		nil,
		map[types.NodeID]int{"responded": 7, "silent": 8},
	)
	if queryKey == "" {
		t.Fatal("weighted query registration failed")
	}
	engine.SubmitChit("responded", queryKey, blockB.ID, nil)
	engine.mu.Lock()
	engine.activeQueries[queryKey].startTime = time.Now().Add(-time.Second)
	engine.mu.Unlock()
	engine.checkTimeouts()

	if got := engine.GetActiveQueryCount(); got != 0 {
		t.Fatalf("timed-out weighted poll remained active: %d", got)
	}
	sb := engine.getSnowball(1)
	if sb.GetStrength(blockB.ID) != 0 || sb.GetConfidence() != 0 {
		t.Fatalf("7 returned slots were promoted by missing slots: strength=%d confidence=%d",
			sb.GetStrength(blockB.ID), sb.GetConfidence())
	}
}

func TestEngineQueryTimeoutUsesExactPerPollTimer(t *testing.T) {
	blockA := &types.Block{ID: "block-a", Header: types.BlockHeader{Height: 1, ParentID: "genesis"}}
	const timeout = 30 * time.Millisecond
	engine, _ := newProductionSnowballTestEngine(t, ConsensusConfig{
		K: 15, AlphaPreference: 8, AlphaConfidence: 12, Beta: 15,
		QueryTimeout: timeout,
	}, blockA)

	start := time.Now()
	queryKey := engine.RegisterQuery(
		"self",
		1,
		blockA.ID,
		1,
		0,
		nil,
		map[types.NodeID]int{"silent": 15},
	)
	if queryKey == "" {
		t.Fatal("query registration failed")
	}

	deadline := time.Now().Add(300 * time.Millisecond)
	for engine.GetActiveQueryCount() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	elapsed := time.Since(start)
	if engine.GetActiveQueryCount() != 0 {
		t.Fatalf("query did not expire within 300ms; configured timeout=%v", timeout)
	}
	if elapsed >= 300*time.Millisecond {
		t.Fatalf("query timeout was delayed to a coarse cleanup tick: elapsed=%v", elapsed)
	}
}

func TestEngineRejectsInvalidReplacementWeightRegistration(t *testing.T) {
	blockA := &types.Block{ID: "block-a", Header: types.BlockHeader{Height: 1, ParentID: "genesis"}}
	engine, _ := newProductionSnowballTestEngine(t, ConsensusConfig{
		K: 15, AlphaPreference: 8, AlphaConfidence: 12, Beta: 15,
	}, blockA)

	if got := engine.RegisterQuery("self", 0, blockA.ID, 1, 0, nil, nil); got != "" {
		t.Fatalf("registered query without local sample weights: %s", got)
	}
	if got := engine.RegisterQuery(
		"self", 1, blockA.ID, 1, 0, nil,
		map[types.NodeID]int{"peer-a": 14},
	); got != "" {
		t.Fatalf("registered sample weights totaling less than K: %s", got)
	}
	if got := engine.RegisterQuery(
		"self", 2, blockA.ID, 1, 1, nil,
		map[types.NodeID]int{"peer-a": 15, "peer-b": -1, "peer-c": 1},
	); got != "" {
		t.Fatalf("registered non-positive sample weight: %s", got)
	}
	if got := engine.RegisterQuery(
		"self", 2, blockA.ID, 1, 1, nil,
		map[types.NodeID]int{"self": 1, "peer-a": 14},
	); got != "" {
		t.Fatalf("registered requester as a sampled peer: %s", got)
	}
	if got := engine.RegisterQuery(
		"self", 2, blockA.ID, 1, 1, nil,
		map[types.NodeID]int{"": 1, "peer-a": 14},
	); got != "" {
		t.Fatalf("registered empty sampled peer identity: %s", got)
	}
	if got := engine.RegisterQuery(
		"self", 2, blockA.ID, 1, 1, nil,
		map[types.NodeID]int{"peer-a": 16},
	); got != "" {
		t.Fatalf("registered sample weights totaling more than K: %s", got)
	}
	callerWeights := map[types.NodeID]int{"peer-a": 15}
	queryKey := engine.RegisterQuery("self", 3, blockA.ID, 1, 2, nil, callerWeights)
	if queryKey == "" {
		t.Fatal("valid replacement weights were rejected")
	}
	callerWeights["peer-a"] = 1
	callerWeights["peer-b"] = 14
	engine.SubmitChit("peer-a", queryKey, blockA.ID, nil)
	if got := engine.GetActiveQueryCount(); got != 0 {
		t.Fatalf("caller mutation changed registered weight snapshot: active=%d", got)
	}
}

func TestEngineFinalizesRecentStrongWinnerNotCumulativePreference(t *testing.T) {
	blockA := &types.Block{ID: "block-a", Header: types.BlockHeader{Height: 1, ParentID: "genesis"}}
	blockB := &types.Block{ID: "block-b", Header: types.BlockHeader{Height: 1, ParentID: "genesis"}}
	engine, store := newProductionSnowballTestEngine(t, ConsensusConfig{
		K: 5, AlphaPreference: 3, AlphaConfidence: 5, Beta: 3,
	}, blockA, blockB)
	if got := engine.GetPreference(1); got != blockA.ID {
		t.Fatalf("initial preference = %s, want A", got)
	}

	poll := func(key string, votes map[string]int) {
		t.Helper()
		weights := make(map[types.NodeID]int, len(votes))
		candidates := make(map[string]struct{}, len(votes))
		responses := make(map[types.NodeID]string, len(votes))
		index := 0
		for candidate, count := range votes {
			peer := types.NodeID(fmt.Sprintf("poll-peer-%d", index))
			weights[peer] = count
			responses[peer] = candidate
			candidates[candidate] = struct{}{}
			index++
		}
		accumulator, err := NewPollAccumulator(5, 3, 5, weights, candidates)
		if err != nil {
			t.Fatalf("create poll %s: %v", key, err)
		}
		for peer, candidate := range responses {
			accumulator.Record(peer, candidate)
		}
		accumulator.Close()
		reason := engine.processVotes(&QueryContext{
			queryKey:   key,
			poll:       accumulator,
			voters:     make(map[types.NodeID]string),
			signatures: make(map[types.NodeID][]byte),
			startTime:  time.Now(),
			height:     1,
		})
		if reason != "success" {
			t.Fatalf("poll %s returned %s", key, reason)
		}
	}
	for i := 0; i < 6; i++ {
		poll(fmt.Sprintf("soft-a-%d", i), map[string]int{blockA.ID: 3, blockB.ID: 2})
	}
	for i := 0; i < 2; i++ {
		poll(fmt.Sprintf("strong-b-%d", i), map[string]int{blockB.ID: 5})
	}

	sb := engine.getSnowball(1)
	if sb.GetPreference() != blockA.ID ||
		sb.GetConfidencePreference() != blockB.ID ||
		sb.GetConfidence() != 2 {
		t.Fatalf("setup did not separate cumulative and recent preferences: cumulative=%s recent=%s confidence=%d",
			sb.GetPreference(), sb.GetConfidencePreference(), sb.GetConfidence())
	}
	poll("strong-b-final", map[string]int{blockB.ID: 5})

	finalized, ok := store.GetFinalizedAtHeight(1)
	if !ok || finalized.ID != blockB.ID {
		t.Fatalf("engine finalized cumulative preference instead of recent strong winner: %+v ok=%v", finalized, ok)
	}
	if !sb.IsFinalized() || sb.GetPreference() != blockB.ID {
		t.Fatalf("Snowball state did not seal committed B: finalized=%v preference=%s",
			sb.IsFinalized(), sb.GetPreference())
	}
}
