package consensus

import (
	"dex/interfaces"
	"dex/logs"
	"dex/types"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
)

type mutableConsensusSnapshotTracker struct {
	interfaces.LivenessTracker
	mu     sync.RWMutex
	height uint64
	count  int
}

func (t *mutableConsensusSnapshotTracker) GetConsensusVoterSnapshot() (uint64, int) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.height, t.count
}

func (t *mutableConsensusSnapshotTracker) set(height uint64, count int) {
	t.mu.Lock()
	t.height = height
	t.count = count
	t.mu.Unlock()
}

func dynamicParamsTestBase() ConsensusConfig {
	return ConsensusConfig{
		K:                    15,
		AlphaPreference:      8,
		AlphaConfidence:      12,
		Beta:                 15,
		PollInterval:         25 * time.Millisecond,
		QueryTimeout:         4 * time.Second,
		MaxConcurrentQueries: 8,
		NumHeights:           10,
		BlocksPerHeight:      5,
	}
}

func TestResolveDynamicConsensusParamsProfiles(t *testing.T) {
	base := dynamicParamsTestBase()
	tests := []struct {
		name            string
		voterCount      int
		k               int
		alphaPreference int
		alphaConfidence int
	}{
		{name: "two voters", voterCount: 2, k: 1, alphaPreference: 1, alphaConfidence: 1},
		{name: "four voters", voterCount: 4, k: 3, alphaPreference: 2, alphaConfidence: 2},
		{name: "five voters", voterCount: 5, k: 4, alphaPreference: 3, alphaConfidence: 3},
		{name: "six voters", voterCount: 6, k: 5, alphaPreference: 3, alphaConfidence: 4},
		{name: "nine voters", voterCount: 9, k: 7, alphaPreference: 4, alphaConfidence: 6},
		{name: "fourteen voters", voterCount: 14, k: 11, alphaPreference: 6, alphaConfidence: 9},
		{name: "nineteen voters", voterCount: 19, k: 14, alphaPreference: 8, alphaConfidence: 11},
		{name: "twenty voters reaches static profile", voterCount: 20, k: 15, alphaPreference: 8, alphaConfidence: 12},
		{name: "large network remains capped", voterCount: 100, k: 15, alphaPreference: 8, alphaConfidence: 12},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveDynamicConsensusParams(base, tt.voterCount)
			if got.K != tt.k || got.AlphaPreference != tt.alphaPreference || got.AlphaConfidence != tt.alphaConfidence {
				t.Fatalf(
					"voterCount=%d resolved K/AP/AC=%d/%d/%d, want %d/%d/%d",
					tt.voterCount,
					got.K,
					got.AlphaPreference,
					got.AlphaConfidence,
					tt.k,
					tt.alphaPreference,
					tt.alphaConfidence,
				)
			}
			if got.Beta != base.Beta {
				t.Fatalf("voterCount=%d changed Beta from %d to %d", tt.voterCount, base.Beta, got.Beta)
			}
		})
	}
}

func TestResolveDynamicConsensusParamsFallsBackWhenVoterCountIsUnsafe(t *testing.T) {
	base := dynamicParamsTestBase()
	for _, voterCount := range []int{-1, 0, 1} {
		t.Run(fmt.Sprintf("voter_count_%d", voterCount), func(t *testing.T) {
			got := ResolveDynamicConsensusParams(base, voterCount)
			if !reflect.DeepEqual(got, base) {
				t.Fatalf("voterCount=%d should preserve the static config: got=%+v want=%+v", voterCount, got, base)
			}
		})
	}
}

func TestResolveDynamicConsensusParamsPreservesConfiguredThresholdsAtBaseK(t *testing.T) {
	base := dynamicParamsTestBase()
	base.K = 7
	base.AlphaPreference = 5
	base.AlphaConfidence = 7

	got := ResolveDynamicConsensusParams(base, 9)
	if !reflect.DeepEqual(got, base) {
		t.Fatalf("reaching base K should retain the configured static profile: got=%+v want=%+v", got, base)
	}

	got = ResolveDynamicConsensusParams(base, 5)
	if got.K != 4 || got.AlphaPreference != 3 || got.AlphaConfidence != 3 {
		t.Fatalf("below base K, thresholds should be derived dynamically: got=%d/%d/%d want=4/3/3",
			got.K, got.AlphaPreference, got.AlphaConfidence)
	}
}

func TestResolveDynamicConsensusParamsPreservesNonThresholdFields(t *testing.T) {
	base := dynamicParamsTestBase()
	wantBase := base

	got := ResolveDynamicConsensusParams(base, 5)
	got.K = base.K
	got.AlphaPreference = base.AlphaPreference
	got.AlphaConfidence = base.AlphaConfidence

	if !reflect.DeepEqual(got, base) {
		t.Fatalf("dynamic resolution changed non-threshold fields: got=%+v want=%+v", got, base)
	}
	if !reflect.DeepEqual(base, wantBase) {
		t.Fatalf("dynamic resolution mutated its base argument: got=%+v want=%+v", base, wantBase)
	}
}

func TestResolveDynamicConsensusParamsSafetyBoundsAndMonotonicK(t *testing.T) {
	base := dynamicParamsTestBase()
	previousK := 0
	for voterCount := 2; voterCount <= 256; voterCount++ {
		got := ResolveDynamicConsensusParams(base, voterCount)
		if got.K < 1 || got.K > base.K || got.K > voterCount-1 {
			t.Fatalf("voterCount=%d produced invalid K=%d (base K=%d)", voterCount, got.K, base.K)
		}
		if got.AlphaPreference <= got.K/2 || got.AlphaPreference > got.AlphaConfidence || got.AlphaConfidence > got.K {
			t.Fatalf("voterCount=%d produced invalid thresholds K/AP/AC=%d/%d/%d",
				voterCount, got.K, got.AlphaPreference, got.AlphaConfidence)
		}
		if got.K < previousK {
			t.Fatalf("K decreased as voter count grew: voterCount=%d previousK=%d currentK=%d", voterCount, previousK, got.K)
		}
		previousK = got.K
	}
}

func TestConsensusParamsProviderBindsCountToFinalizedAnchor(t *testing.T) {
	tracker := &mutableConsensusSnapshotTracker{height: 5, count: 5}
	provider := NewConsensusParamsProvider(dynamicParamsTestBase())
	provider.SetLivenessTracker(tracker)

	params, count, ok := provider.ParamsForAnchor(5)
	if !ok || count != 5 || params.K != 4 || params.AlphaPreference != 3 || params.AlphaConfidence != 3 {
		t.Fatalf("anchor 5 params = %+v count=%d ok=%t, want 4/3/3 count=5", params, count, ok)
	}

	                                                                          
	                                                              
	tracker.set(6, 9)
	if _, _, ok := provider.ParamsForAnchor(5); ok {
		t.Fatal("provider accepted voter state from height 6 for anchor 5")
	}
	params, count, ok = provider.ParamsForAnchor(6)
	if !ok || count != 9 || params.K != 7 || params.AlphaPreference != 4 || params.AlphaConfidence != 6 {
		t.Fatalf("anchor 6 params = %+v count=%d ok=%t, want 7/4/6 count=9", params, count, ok)
	}
}

func TestSnowmanQueryCapturesCompleteDynamicTuple(t *testing.T) {
	store := NewMemoryBlockStore()
	block := &types.Block{
		ID: "block-1-peer-a-w0-dynamic-params",
		Header: types.BlockHeader{
			Height:   1,
			ParentID: "genesis",
			Proposer: "peer-a",
		},
	}
	if _, err := store.Add(block); err != nil {
		t.Fatalf("add candidate: %v", err)
	}
	base := dynamicParamsTestBase()
	engine := NewSnowmanEngine(
		"self",
		store,
		&base,
		NewEventBus(),
		logs.NewNodeLogger("dynamic-query-test", 0),
	).(*SnowmanEngine)
	want := ConsensusPollParams{K: 4, AlphaPreference: 3, AlphaConfidence: 3, Beta: 15}
	weights := map[types.NodeID]int{"p1": 1, "p2": 1, "p3": 1, "p4": 1}
	key := engine.RegisterQueryWithParams("self", 1, block.ID, 1, 0, nil, weights, want)
	if key == "" {
		t.Fatal("dynamic query registration failed")
	}

	                                                                         
	                                  
	base.K = 15
	base.AlphaPreference = 8
	base.AlphaConfidence = 12
	base.Beta = 99
	engine.mu.RLock()
	got := engine.activeQueries[key].params
	engine.mu.RUnlock()
	if got != want {
		t.Fatalf("captured query tuple changed: got=%+v want=%+v", got, want)
	}
}
