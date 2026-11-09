package consensus

import (
	"strings"
	"testing"
	"time"

	"dex/config"
	"dex/logs"
	"dex/types"
)

func cachedProposalStats(pm *ProposalManager) (count int, bytes uint64, slots int) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return len(pm.cachedProposalIDs), pm.cachedProposalBytes, len(pm.cachedProposalSlots)
}

func testFutureProposal(id string, height uint64, proposer string, window int, parent string) *types.Block {
	return &types.Block{
		ID: id,
		Header: types.BlockHeader{
			Height:   height,
			ParentID: parent,
			Proposer: proposer,
			Window:   window,
		},
	}
}

func TestFutureWindowAheadOfLocalClockIsDeferredNotRejected(t *testing.T) {
	store, pm, _ := newLiveWindowHarness(t)
	block := testFutureProposal("clock-lag-window-1", 1, "proposer", 1, "genesis")

	added, deferred, err := pm.admitLiveBlock(block)
	if err != nil || added || !deferred {
		t.Fatalf("valid future window should be deferred: added=%t deferred=%t err=%v", added, deferred, err)
	}
	if calls := store.calls(); calls != 0 {
		t.Fatalf("future window reached candidate store early: calls=%d", calls)
	}

	openLiveWindow(pm, 1)
	if _, exists := store.Get(block.ID); !exists {
		t.Fatal("deferred window-1 proposal was not admitted when the local clock reached window 1")
	}
}

func TestLiveAdmissionRejectsOnlyStructurallyInvalidWindows(t *testing.T) {
	t.Run("enabled out of range", func(t *testing.T) {
		_, pm, _ := newLiveWindowHarness(t)
		block := testFutureProposal("invalid-window-3", 1, "proposer", 3, "genesis")
		added, deferred, err := pm.admitLiveBlock(block)
		if added || deferred || err == nil || !strings.Contains(err.Error(), "configured range") {
			t.Fatalf("out-of-range window admission: added=%t deferred=%t err=%v", added, deferred, err)
		}
		if count, bytes, slots := cachedProposalStats(pm); count != 0 || bytes != 0 || slots != 0 {
			t.Fatalf("invalid window changed cache accounting: count=%d bytes=%d slots=%d", count, bytes, slots)
		}
	})

	t.Run("disabled only permits zero", func(t *testing.T) {
		store := NewMemoryBlockStore()
		pm := mustNewProposalManagerWithProposer(t,
			"disabled-window",
			nil,
			store,
			&NodeConfig{ProposalInterval: time.Hour},
			NewEventBus(),
			nil,
			config.WindowConfig{Enabled: false},
			logs.NewNodeLogger(t.Name(), 0),
		)
		block := testFutureProposal("disabled-window-1", 1, "proposer", 1, "genesis")
		added, deferred, err := pm.admitLiveBlock(block)
		if added || deferred || err == nil || !strings.Contains(err.Error(), "disabled") {
			t.Fatalf("disabled window admission: added=%t deferred=%t err=%v", added, deferred, err)
		}
	})
}

func TestProposalCacheAllowsOnlyOneBlockPerProposerSlot(t *testing.T) {
	_, pm, _ := newLiveWindowHarness(t)
	first := testFutureProposal("slot-first", 2, "same-proposer", 0, "parent-1")
	second := testFutureProposal("slot-second", 2, "same-proposer", 0, "parent-1")

	if added, deferred, err := pm.admitLiveBlock(first); err != nil || added || !deferred {
		t.Fatalf("first slot proposal admission: added=%t deferred=%t err=%v", added, deferred, err)
	}
	added, deferred, err := pm.admitLiveBlock(second)
	if added || deferred || err == nil || !strings.Contains(err.Error(), "slot already occupied") {
		t.Fatalf("second slot proposal admission: added=%t deferred=%t err=%v", added, deferred, err)
	}
	count, bytes, slots := cachedProposalStats(pm)
	if count != 1 || slots != 1 || bytes != proposalCacheSize(first) {
		t.Fatalf("slot rejection corrupted accounting: count=%d bytes=%d slots=%d", count, bytes, slots)
	}
}

func TestProposalCacheEnforcesCountAndByteLimits(t *testing.T) {
	t.Run("count", func(t *testing.T) {
		_, pm, _ := newLiveWindowHarness(t)
		pm.maxCachedProposals = 2
		first := testFutureProposal("count-first", 1, "proposer-1", 1, "genesis")
		second := testFutureProposal("count-second", 1, "proposer-2", 1, "genesis")
		third := testFutureProposal("count-third", 1, "proposer-3", 1, "genesis")
		for _, block := range []*types.Block{first, second} {
			if _, deferred, err := pm.admitLiveBlock(block); err != nil || !deferred {
				t.Fatalf("cache fixture %s: deferred=%t err=%v", block.ID, deferred, err)
			}
		}
		if added, deferred, err := pm.admitLiveBlock(third); added || deferred || err == nil || !strings.Contains(err.Error(), "count limit") {
			t.Fatalf("count overflow admission: added=%t deferred=%t err=%v", added, deferred, err)
		}
		if count, _, slots := cachedProposalStats(pm); count != 2 || slots != 2 {
			t.Fatalf("count overflow corrupted cache: count=%d slots=%d", count, slots)
		}
	})

	t.Run("bytes", func(t *testing.T) {
		_, pm, _ := newLiveWindowHarness(t)
		first := testFutureProposal("bytes-first", 1, "proposer-1", 1, "genesis")
		first.CanonicalPayload = []byte("first-payload")
		second := testFutureProposal("bytes-second", 1, "proposer-2", 1, "genesis")
		second.CanonicalPayload = []byte("second-payload")
		pm.maxCachedProposalBytes = proposalCacheSize(first) + proposalCacheSize(second) - 1

		if _, deferred, err := pm.admitLiveBlock(first); err != nil || !deferred {
			t.Fatalf("first byte-limited proposal: deferred=%t err=%v", deferred, err)
		}
		if added, deferred, err := pm.admitLiveBlock(second); added || deferred || err == nil || !strings.Contains(err.Error(), "byte limit") {
			t.Fatalf("byte overflow admission: added=%t deferred=%t err=%v", added, deferred, err)
		}
		if count, bytes, slots := cachedProposalStats(pm); count != 1 || bytes != proposalCacheSize(first) || slots != 1 {
			t.Fatalf("byte overflow corrupted cache: count=%d bytes=%d slots=%d", count, bytes, slots)
		}
	})
}

func TestFullProposalCacheRejectsBeforeExpensivePrevalidation(t *testing.T) {
	store := &rejectingDeferredValidatorStore{BlockStore: NewMemoryBlockStore()}
	pm := mustNewProposalManagerWithProposer(t,
		"cache-precheck",
		nil,
		store,
		&NodeConfig{ProposalInterval: time.Hour},
		NewEventBus(),
		nil,
		config.WindowConfig{Enabled: true, Stages: []config.WindowStage{
			{Duration: time.Hour},
			{Duration: 0},
		}},
		logs.NewNodeLogger(t.Name(), 0),
	)
	pm.maxCachedProposals = 1
	first := testFutureProposal("precheck-first", 2, "proposer-1", 0, "parent-1")
	second := testFutureProposal("precheck-second", 3, "proposer-2", 0, "parent-2")
	if _, deferred, err := pm.admitLiveBlock(first); err != nil || !deferred {
		t.Fatalf("first precheck fixture: deferred=%t err=%v", deferred, err)
	}
	if store.calls != 1 {
		t.Fatalf("initial prevalidation calls=%d, want 1", store.calls)
	}
	if _, _, err := pm.admitLiveBlock(second); err == nil || !strings.Contains(err.Error(), "count limit") {
		t.Fatalf("full cache accepted second proposal: %v", err)
	}
	if store.calls != 1 {
		t.Fatalf("full cache triggered expensive prevalidation: calls=%d", store.calls)
	}
}

func TestStaleProposalAccountingIsPrunedBeforeCapacityCheck(t *testing.T) {
	store, pm, _ := newLiveWindowHarness(t)
	pm.maxCachedProposals = 1
	stale := testFutureProposal("stale-cached-proposal", 1, "stale-proposer", 1, "genesis")
	if _, deferred, err := pm.admitLiveBlock(stale); err != nil || !deferred {
		t.Fatalf("cache stale fixture: deferred=%t err=%v", deferred, err)
	}

	winner := testFutureProposal("finalized-winner", 1, "winner", 0, "genesis")
	if added, err := store.BlockStore.Add(winner); err != nil || !added {
		t.Fatalf("add finalized winner: added=%t err=%v", added, err)
	}
	if err := store.BlockStore.SetFinalized(1, winner.ID); err != nil {
		t.Fatalf("finalize winner: %v", err)
	}

	fresh := testFutureProposal("fresh-after-prune", 2, "fresh-proposer", 1, winner.ID)
	if added, deferred, err := pm.admitLiveBlock(fresh); err != nil || added || !deferred {
		t.Fatalf("fresh proposal after stale prune: added=%t deferred=%t err=%v", added, deferred, err)
	}
	if count, bytes, slots := cachedProposalStats(pm); count != 1 || bytes != proposalCacheSize(fresh) || slots != 1 {
		t.Fatalf("stale accounting not replaced: count=%d bytes=%d slots=%d", count, bytes, slots)
	}
}

func TestFarFutureProposalsCannotConsumeNextHeightReserve(t *testing.T) {
	t.Run("count reserve", func(t *testing.T) {
		_, pm, _ := newLiveWindowHarness(t)
		pm.maxCachedProposals = 4
		far := []*types.Block{
			testFutureProposal("far-count-1", 2, "far-proposer-1", 0, "parent-1"),
			testFutureProposal("far-count-2", 3, "far-proposer-2", 0, "parent-2"),
		}
		for _, block := range far {
			if _, deferred, err := pm.admitLiveBlock(block); err != nil || !deferred {
				t.Fatalf("far cache fixture %s: deferred=%t err=%v", block.ID, deferred, err)
			}
		}
		overflow := testFutureProposal("far-count-3", 4, "far-proposer-3", 0, "parent-3")
		if _, _, err := pm.admitLiveBlock(overflow); err == nil || !strings.Contains(err.Error(), "far-future count limit") {
			t.Fatalf("far proposals consumed reserved count capacity: %v", err)
		}

		for _, block := range []*types.Block{
			testFutureProposal("next-count-1", 1, "next-proposer-1", 1, "genesis"),
			testFutureProposal("next-count-2", 1, "next-proposer-2", 1, "genesis"),
		} {
			if _, deferred, err := pm.admitLiveBlock(block); err != nil || !deferred {
				t.Fatalf("reserved next-height admission %s: deferred=%t err=%v", block.ID, deferred, err)
			}
		}
		if count, _, slots := cachedProposalStats(pm); count != 4 || slots != 4 {
			t.Fatalf("reserved count was not usable by next height: count=%d slots=%d", count, slots)
		}
	})

	t.Run("byte reserve", func(t *testing.T) {
		_, pm, _ := newLiveWindowHarness(t)
		pm.maxCachedProposals = 10
		first := testFutureProposal("far-byte-1", 2, "proposer-a", 0, "parent-001")
		second := testFutureProposal("far-byte-2", 3, "proposer-b", 0, "parent-002")
		if proposalCacheSize(first) != proposalCacheSize(second) {
			t.Fatal("byte-reserve fixtures must have equal accounted size")
		}
		pm.maxCachedProposalBytes = proposalCacheSize(first) * 2
		if _, deferred, err := pm.admitLiveBlock(first); err != nil || !deferred {
			t.Fatalf("first far-byte proposal: deferred=%t err=%v", deferred, err)
		}
		if _, _, err := pm.admitLiveBlock(second); err == nil || !strings.Contains(err.Error(), "far-future byte limit") {
			t.Fatalf("far proposals consumed reserved byte capacity: %v", err)
		}

		next := testFutureProposal("nxt-byte-1", 1, "proposer-c", 1, "genesis")
		if _, deferred, err := pm.admitLiveBlock(next); err != nil || !deferred {
			t.Fatalf("reserved next-height byte admission: deferred=%t err=%v", deferred, err)
		}
		if count, bytes, slots := cachedProposalStats(pm); count != 2 || bytes != proposalCacheSize(first)+proposalCacheSize(next) || slots != 2 {
			t.Fatalf("reserved bytes accounting: count=%d bytes=%d slots=%d", count, bytes, slots)
		}
	})
}

func TestProposalCacheReleasesBytesAndSlotsWhenBlocksLeave(t *testing.T) {
	store, pm, _ := newLiveWindowHarness(t)
	first := testFutureProposal("release-first", 1, "proposer-1", 1, "genesis")
	second := testFutureProposal("release-second", 2, "proposer-2", 0, first.ID)
	for _, block := range []*types.Block{first, second} {
		if _, deferred, err := pm.admitLiveBlock(block); err != nil || !deferred {
			t.Fatalf("cache fixture %s: deferred=%t err=%v", block.ID, deferred, err)
		}
	}
	if count, bytes, slots := cachedProposalStats(pm); count != 2 || bytes != proposalCacheSize(first)+proposalCacheSize(second) || slots != 2 {
		t.Fatalf("initial accounting: count=%d bytes=%d slots=%d", count, bytes, slots)
	}

	openLiveWindow(pm, 1)
	if count, bytes, slots := cachedProposalStats(pm); count != 1 || bytes != proposalCacheSize(second) || slots != 1 {
		t.Fatalf("accounting after first release: count=%d bytes=%d slots=%d", count, bytes, slots)
	}
	if err := store.BlockStore.SetFinalized(1, first.ID); err != nil {
		t.Fatalf("finalize first proposal: %v", err)
	}
	pm.processCachedProposals(0)
	if count, bytes, slots := cachedProposalStats(pm); count != 0 || bytes != 0 || slots != 0 {
		t.Fatalf("accounting after all releases: count=%d bytes=%d slots=%d", count, bytes, slots)
	}
}

func TestFutureProposalHeightRangeIsOneHundredBlocks(t *testing.T) {
	if MaxFutureBlockCacheWindow != 100 {
		t.Fatalf("future block window=%d, want 100", MaxFutureBlockCacheWindow)
	}
	_, pm, _ := newLiveWindowHarness(t)
	near := testFutureProposal("height-plus-100", 100, "proposer-100", 0, "parent-99")
	if added, deferred, err := pm.admitLiveBlock(near); err != nil || added || !deferred {
		t.Fatalf("accepted+100 admission: added=%t deferred=%t err=%v", added, deferred, err)
	}
	far := testFutureProposal("height-plus-101", 101, "proposer-101", 0, "parent-100")
	if added, deferred, err := pm.admitLiveBlock(far); added || deferred || err == nil || !strings.Contains(err.Error(), "too far ahead") {
		t.Fatalf("accepted+101 admission: added=%t deferred=%t err=%v", added, deferred, err)
	}
}
