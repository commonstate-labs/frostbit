package syncer

import (
	"context"
	"dex/cmd/explorer/indexdb"
	"dex/db"
	"dex/keys"
	"dex/pb"
	"dex/utils/diskspace"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cockroachdb/pebble"
)

const testGiB uint64 = 1 << 30

func TestSyncerSettersCannotDisableResourceBounds(t *testing.T) {
	s := &Syncer{}
	s.SetMaxBlocksPerSync(0)
	if s.maxBlocksPerSync != DefaultMaxBlocksPerSync {
		t.Fatalf("max blocks=%d, want default %d", s.maxBlocksPerSync, DefaultMaxBlocksPerSync)
	}
	s.SetMaxBlocksPerSync(-1)
	if s.maxBlocksPerSync != DefaultMaxBlocksPerSync {
		t.Fatalf("negative max blocks=%d, want default %d", s.maxBlocksPerSync, DefaultMaxBlocksPerSync)
	}
	s.SetIndexRetentionHeights(0)
	if s.indexRetentionHeights != DefaultIndexRetentionHeights {
		t.Fatalf("retention=%d, want default %d", s.indexRetentionHeights, DefaultIndexRetentionHeights)
	}

	s.SetMaxBlocksPerSync(7)
	s.SetIndexRetentionHeights(1234)
	if s.maxBlocksPerSync != 7 || s.indexRetentionHeights != 1234 {
		t.Fatalf("positive bounds changed: blocks=%d retention=%d", s.maxBlocksPerSync, s.indexRetentionHeights)
	}

	s.SetIndexDiskPressureFreeBytes(0)
	s.SetIndexDiskPressureKeepRecentHeights(99)
	if s.indexDiskPressureFreeBytes != DefaultIndexDiskPressureFreeBytes {
		t.Fatalf("disk pressure threshold=%d, want default %d", s.indexDiskPressureFreeBytes, DefaultIndexDiskPressureFreeBytes)
	}
	if s.indexDiskPressureKeepRecentHeights != DefaultIndexDiskPressureKeepRecentHeights {
		t.Fatalf("disk pressure keep=%d, want default %d", s.indexDiskPressureKeepRecentHeights, DefaultIndexDiskPressureKeepRecentHeights)
	}
	s.SetIndexDiskPressureFreeBytes(17 * testGiB)
	s.SetIndexDiskPressureKeepRecentHeights(250)
	if s.indexDiskPressureFreeBytes != 17*testGiB || s.indexDiskPressureKeepRecentHeights != 250 {
		t.Fatalf("positive disk pressure settings changed: threshold=%d keep=%d", s.indexDiskPressureFreeBytes, s.indexDiskPressureKeepRecentHeights)
	}
}

func TestSyncOnceNotifiesOnlyAfterAtomicIndexCommit(t *testing.T) {
	s, idb := newTestSyncer(t)
	s.diskUsage = func(path string) (diskspace.Snapshot, error) {
		return diskspace.Snapshot{Path: path, TotalBytes: 100 * testGiB, FreeBytes: 50 * testGiB, FreePercent: 50}, nil
	}
	s.getLatestHeightOverride = fixedLatestHeight(4)
	s.fetchBlockOverride = retainedBlocksFrom(1, nil)

	fixedNow := time.Unix(100, 0)
	s.indexedNotifyNow = func() time.Time { return fixedNow }
	var callbackHeights, visibleHeights []uint64
	var callbackErr error
	s.SetOnBlockIndexed(func(height uint64) {
		callbackHeights = append(callbackHeights, height)
		visibleHeight, err := idb.GetSyncHeight()
		if err != nil {
			callbackErr = err
			return
		}
		visibleHeights = append(visibleHeights, visibleHeight)
		visibleHash, err := idb.GetBlockHash(height)
		if err != nil {
			callbackErr = err
			return
		}
		if visibleHash != fmt.Sprintf("hash-%d", height) {
			callbackErr = fmt.Errorf("height %d visible hash %q", height, visibleHash)
		}
	})

	s.syncOnce(context.Background(), 4)

	if callbackErr != nil {
		t.Fatalf("read committed index from callback: %v", callbackErr)
	}
	if got, want := fmt.Sprint(callbackHeights), "[1 4]"; got != want {
		t.Fatalf("callback heights=%s, want %s", got, want)
	}
	if got, want := fmt.Sprint(visibleHeights), "[1 4]"; got != want {
		t.Fatalf("visible sync heights=%s, want %s", got, want)
	}
}

func TestBlockIndexedNotificationThrottleForceAndDedup(t *testing.T) {
	s := &Syncer{indexedNotifyEvery: 2 * time.Second}
	now := time.Unix(100, 0)
	s.indexedNotifyNow = func() time.Time { return now }
	var got []uint64
	s.SetOnBlockIndexed(func(height uint64) {
		                                                                          
		_, _ = s.GetStatus()
		got = append(got, height)
	})

	s.notifyBlockIndexed(1)
	s.notifyBlockIndexed(1)
	s.forceNotifyBlockIndexed(1)
	s.notifyBlockIndexed(2)
	now = now.Add(2 * time.Second)
	s.notifyBlockIndexed(3)
	s.notifyBlockIndexed(2)
	s.notifyBlockIndexed(4)
	s.forceNotifyBlockIndexed(4)
	s.forceNotifyBlockIndexed(4)

	if gotText, want := fmt.Sprint(got), "[1 3 4]"; gotText != want {
		t.Fatalf("callback heights=%s, want %s", gotText, want)
	}
}

func TestBlockIndexedNotificationResetAllowsLowerHeight(t *testing.T) {
	s := &Syncer{indexedNotifyEvery: 2 * time.Second}
	now := time.Unix(100, 0)
	s.indexedNotifyNow = func() time.Time { return now }
	var got []uint64
	s.SetOnBlockIndexed(func(height uint64) { got = append(got, height) })

	s.forceNotifyBlockIndexed(100)
	s.resetBlockIndexedNotification()
	s.notifyBlockIndexed(1)

	if gotText, want := fmt.Sprint(got), "[100 1]"; gotText != want {
		t.Fatalf("callback heights after reset=%s, want %s", gotText, want)
	}
}

func TestIndexDiskPressureUsesStrictByteThresholdAndRecentFloor(t *testing.T) {
	s, idb := newTestSyncer(t)
	indexSyncTestBlocks(t, idb, 205)
	s.SetIndexPruneMaxHeights(1000)

	freeBytes := DefaultIndexDiskPressureFreeBytes
	usageCalls := 0
	s.diskUsage = func(path string) (diskspace.Snapshot, error) {
		usageCalls++
		if path != idb.Path() {
			t.Fatalf("disk usage path=%q, want %q", path, idb.Path())
		}
		return diskspace.Snapshot{Path: path, TotalBytes: 100 * testGiB, FreeBytes: freeBytes, FreePercent: float64(freeBytes) * 100 / float64(100*testGiB)}, nil
	}

	active, err := s.pruneIndexUnderDiskPressure(205)
	if err != nil {
		t.Fatal(err)
	}
	if active {
		t.Fatal("exactly 10 GiB available unexpectedly activated pressure pruning")
	}
	if hash, err := idb.GetBlockHash(1); err != nil || hash == "" {
		t.Fatalf("height 1 was pruned at exact threshold: hash=%q err=%v", hash, err)
	}

	freeBytes = DefaultIndexDiskPressureFreeBytes + 1
	active, err = s.pruneIndexUnderDiskPressure(205)
	if err != nil || active {
		t.Fatalf("above-threshold pressure result: active=%v err=%v", active, err)
	}

	freeBytes = DefaultIndexDiskPressureFreeBytes - 1
	active, err = s.pruneIndexUnderDiskPressure(205)
	if err != nil {
		t.Fatal(err)
	}
	if !active {
		t.Fatal("below-threshold disk usage did not activate pressure pruning")
	}
	if hash, err := idb.GetBlockHash(105); err != nil || hash != "" {
		t.Fatalf("height 105 was not pruned: hash=%q err=%v", hash, err)
	}
	if hash, err := idb.GetBlockHash(106); err != nil || hash == "" {
		t.Fatalf("recent-height floor was crossed: hash=%q err=%v", hash, err)
	}
	assertPartialBase(t, idb, 106)
	if usageCalls != 3 {
		t.Fatalf("disk usage calls=%d, want one per pressure pass", usageCalls)
	}
}

func TestSyncOnceRunsOneBoundedDiskPressurePassBeforeEarlyReturn(t *testing.T) {
	s, idb := newTestSyncer(t)
	indexSyncTestBlocks(t, idb, 205)
	s.SetIndexPruneMaxHeights(3)
	usageCalls := 0
	s.diskUsage = func(path string) (diskspace.Snapshot, error) {
		usageCalls++
		return diskspace.Snapshot{Path: path, TotalBytes: 100 * testGiB, FreeBytes: 9 * testGiB, FreePercent: 9}, nil
	}
	s.getLatestHeightOverride = fixedLatestHeight(205)
	s.lastIntegrityCheck = time.Now()

	s.syncOnce(context.Background(), 205)

	if usageCalls != 1 {
		t.Fatalf("disk usage calls=%d, want 1", usageCalls)
	}
	assertPartialBase(t, idb, 4)
	if hash, err := idb.GetBlockHash(3); err != nil || hash != "" {
		t.Fatalf("height 3 was not pruned within budget: hash=%q err=%v", hash, err)
	}
	if hash, err := idb.GetBlockHash(4); err != nil || hash == "" {
		t.Fatalf("pressure pass exceeded three-height budget: hash=%q err=%v", hash, err)
	}
}

func TestSyncOnceUsesOneRegularRetentionBudgetAcrossMultipleBlocks(t *testing.T) {
	s, idb := newTestSyncer(t)
	indexSyncTestBlocks(t, idb, 205)
	s.SetMaxBlocksPerSync(20)
	s.SetIndexRetentionHeights(100)
	s.SetIndexPruneMaxHeights(3)
	s.lastIntegrityCheck = time.Now()
	s.getLatestHeightOverride = fixedLatestHeight(225)
	s.fetchBlockOverride = retainedBlocksFrom(1, nil)
	usageCalls := 0
	s.diskUsage = func(path string) (diskspace.Snapshot, error) {
		usageCalls++
		return diskspace.Snapshot{Path: path, TotalBytes: 100 * testGiB, FreeBytes: 50 * testGiB, FreePercent: 50}, nil
	}

	s.syncOnce(context.Background(), 0)

	assertSyncHeight(t, idb, 225)
	assertPartialBase(t, idb, 4)
	if hash, err := idb.GetBlockHash(3); err != nil || hash != "" {
		t.Fatalf("height 3 was not pruned within the round budget: hash=%q err=%v", hash, err)
	}
	if hash, err := idb.GetBlockHash(4); err != nil || hash == "" {
		t.Fatalf("regular retention exceeded one three-height round budget: hash=%q err=%v", hash, err)
	}
	if usageCalls != 1 {
		t.Fatalf("disk usage calls=%d, want 1", usageCalls)
	}
}

func TestDiskPressureFloorWinsOverSmallerRegularWindowInSameRound(t *testing.T) {
	s, idb := newTestSyncer(t)
	indexSyncTestBlocks(t, idb, 205)
	s.SetMaxBlocksPerSync(1)
	s.SetIndexRetentionHeights(1)
	s.SetIndexPruneMaxHeights(1000)
	s.lastIntegrityCheck = time.Now()
	s.getLatestHeightOverride = fixedLatestHeight(206)
	s.fetchBlockOverride = retainedBlocksFrom(1, nil)
	s.diskUsage = func(path string) (diskspace.Snapshot, error) {
		return diskspace.Snapshot{Path: path, TotalBytes: 100 * testGiB, FreeBytes: 9 * testGiB, FreePercent: 9}, nil
	}

	s.syncOnce(context.Background(), 0)

	assertSyncHeight(t, idb, 206)
	assertPartialBase(t, idb, 106)
	if hash, err := idb.GetBlockHash(106); err != nil || hash == "" {
		t.Fatalf("pressure recent-height floor was crossed: hash=%q err=%v", hash, err)
	}
	if hash, err := idb.GetBlockHash(206); err != nil || hash == "" {
		t.Fatalf("newly indexed tip missing after pressure pass: hash=%q err=%v", hash, err)
	}
}

func TestSyncOnceBootstrapsPartialHistoryOnlyForEmptyIndex(t *testing.T) {
	s, idb := newTestSyncer(t)
	s.maxBlocksPerSync = 3
	s.partialHistoryWindow = 20
	s.getLatestHeightOverride = fixedLatestHeight(200)
	s.fetchBlockOverride = retainedBlocksFrom(100, nil)

	s.syncOnce(context.Background(), 0)

	assertSyncHeight(t, idb, 183)
	assertPartialBase(t, idb, 181)
	status, _ := s.GetStatus()
	if !strings.Contains(status, "partial history from height 181") || !strings.Contains(status, "synced to 183") {
		t.Fatalf("unexpected status: %q", status)
	}
}

func TestSyncOnceResumesPersistedPartialBaseline(t *testing.T) {
	s, idb := newTestSyncer(t)
	if err := idb.SetPartialHistoryBase(50, "persisted before first block"); err != nil {
		t.Fatal(err)
	}
	s.maxBlocksPerSync = 2
	s.getLatestHeightOverride = fixedLatestHeight(60)
	requested := make([]uint64, 0)
	s.fetchBlockOverride = retainedBlocksFrom(50, &requested)

	s.syncOnce(context.Background(), 0)

	assertSyncHeight(t, idb, 51)
	assertPartialBase(t, idb, 50)
	if len(requested) == 0 || requested[0] != 50 {
		t.Fatalf("requested heights = %v, want first 50", requested)
	}
}

func TestSyncOnceIgnoresLaggingTriggerBelowPartialBaseline(t *testing.T) {
	s, idb := newTestSyncer(t)
	if err := idb.SetPartialHistoryBase(100, "source prefix pruned"); err != nil {
		t.Fatal(err)
	}
	for height := uint64(100); height <= 120; height++ {
		indexSyncTestBlock(t, idb, syncTestBlock(height, fmt.Sprintf("hash-%d", height)))
	}
	s.getLatestHeightOverride = fixedLatestHeight(120)
	s.lastIntegrityCheck = time.Now()
	fetchCalls := 0
	s.fetchBlockOverride = func(context.Context, string, uint64) (*pb.Block, error) {
		fetchCalls++
		return nil, errBlockNotFound
	}

	s.syncOnce(context.Background(), 99)

	assertSyncHeight(t, idb, 120)
	assertPartialBase(t, idb, 100)
	if fetchCalls != 0 {
		t.Fatalf("block fetches = %d, want 0 for an already-covered trigger", fetchCalls)
	}
	if hash, err := idb.GetBlockHash(120); err != nil || hash != "hash-120" {
		t.Fatalf("indexed tip hash after lagging trigger = %q, err=%v", hash, err)
	}
	status, _ := s.GetStatus()
	if !strings.Contains(status, "partial history from height 100") || !strings.Contains(status, "trigger height 99 already covered at 120") {
		t.Fatalf("unexpected status: %q", status)
	}
}

func TestSyncOnceKeepsPartialIndexWhenSourceHeightIsBehind(t *testing.T) {
	s, idb := newTestSyncer(t)
	if err := idb.SetPartialHistoryBase(100, "source prefix pruned"); err != nil {
		t.Fatal(err)
	}
	for height := uint64(100); height <= 120; height++ {
		indexSyncTestBlock(t, idb, syncTestBlock(height, fmt.Sprintf("hash-%d", height)))
	}
	s.getLatestHeightOverride = fixedLatestHeight(110)
	fetchCalls := 0
	s.fetchBlockOverride = func(_ context.Context, _ string, height uint64) (*pb.Block, error) {
		fetchCalls++
		return syncTestBlock(height, fmt.Sprintf("hash-%d", height)), nil
	}

	s.syncOnce(context.Background(), 0)

	assertSyncHeight(t, idb, 120)
	assertPartialBase(t, idb, 100)
	if fetchCalls != 1 {
		t.Fatalf("block fetches = %d, want one overlap check when source is behind the indexed tip", fetchCalls)
	}
	if hash, err := idb.GetBlockHash(120); err != nil || hash != "hash-120" {
		t.Fatalf("indexed tip hash after lagging source = %q, err=%v", hash, err)
	}
	status, _ := s.GetStatus()
	if !strings.Contains(status, "partial history from height 100") || !strings.Contains(status, "source height 110 is behind sync height 120") || !strings.Contains(status, "matches retained height 110") || !strings.Contains(status, "keeping existing index") {
		t.Fatalf("unexpected status: %q", status)
	}
}

func TestSyncOnceResetsAndReindexesWhenLowerSourceTipForksWithinRetainedHistory(t *testing.T) {
	s, idb := newTestSyncer(t)
	indexSyncTestBlocks(t, idb, 12)
	if err := idb.Set("old_chain_marker", "present"); err != nil {
		t.Fatal(err)
	}
	s.getLatestHeightOverride = fixedLatestHeight(5)
	requested := make([]uint64, 0, 6)
	s.fetchBlockOverride = func(_ context.Context, _ string, height uint64) (*pb.Block, error) {
		requested = append(requested, height)
		return syncTestBlockWithParent(height, fmt.Sprintf("new-hash-%d", height), fmt.Sprintf("new-hash-%d", height-1)), nil
	}

	s.syncOnce(context.Background(), 0)

	assertSyncHeight(t, idb, 5)
	if got, err := idb.GetBlockHash(5); err != nil || got != "new-hash-5" {
		t.Fatalf("new chain hash at height 5 = %q, err=%v", got, err)
	}
	if marker, err := idb.Get("old_chain_marker"); err != nil || marker != "" {
		t.Fatalf("old chain marker after reset = %q, err=%v", marker, err)
	}
	if got, want := fmt.Sprint(requested), "[5 1 2 3 4 5]"; got != want {
		t.Fatalf("requested heights = %s, want overlap check followed by new-chain rebuild %s", got, want)
	}
}

func TestSyncOnceKeepsHigherIndexWhenLowerSourceOverlapMatches(t *testing.T) {
	s, idb := newTestSyncer(t)
	indexSyncTestBlocks(t, idb, 12)
	s.getLatestHeightOverride = fixedLatestHeight(5)
	requested := make([]uint64, 0, 1)
	s.fetchBlockOverride = func(_ context.Context, _ string, height uint64) (*pb.Block, error) {
		requested = append(requested, height)
		return syncTestBlock(height, fmt.Sprintf("hash-%d", height)), nil
	}

	s.syncOnce(context.Background(), 0)

	assertSyncHeight(t, idb, 12)
	if got, want := fmt.Sprint(requested), "[5]"; got != want {
		t.Fatalf("requested heights = %s, want %s", got, want)
	}
	status, _ := s.GetStatus()
	if !strings.Contains(status, "matches retained height 5") || !strings.Contains(status, "keeping existing index") {
		t.Fatalf("unexpected status: %q", status)
	}
}

func TestSyncOnceKeepsHigherIndexWithoutRetainedOverlap(t *testing.T) {
	s, idb := newTestSyncer(t)
	if err := idb.SetPartialHistoryBase(10, "old retained window"); err != nil {
		t.Fatal(err)
	}
	for height := uint64(10); height <= 12; height++ {
		indexSyncTestBlock(t, idb, syncTestBlock(height, fmt.Sprintf("hash-%d", height)))
	}
	s.getLatestHeightOverride = fixedLatestHeight(5)
	fetchCalls := 0
	s.fetchBlockOverride = func(context.Context, string, uint64) (*pb.Block, error) {
		fetchCalls++
		return nil, errBlockNotFound
	}

	s.syncOnce(context.Background(), 0)

	assertSyncHeight(t, idb, 12)
	assertPartialBase(t, idb, 10)
	if fetchCalls != 0 {
		t.Fatalf("block fetches = %d, want 0 without retained overlap", fetchCalls)
	}
	status, _ := s.GetStatus()
	if !strings.Contains(status, "no overlap with retained history starting at 10") || !strings.Contains(status, "keeping existing index") {
		t.Fatalf("unexpected status: %q", status)
	}
}

func TestSyncOnceKeepsHigherIndexWhenOverlapBlockCannotBeLoaded(t *testing.T) {
	for _, test := range []struct {
		name    string
		loadErr error
	}{
		{name: "missing block", loadErr: errBlockNotFound},
		{name: "source read error", loadErr: errors.New("temporary block read failure")},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, idb := newTestSyncer(t)
			indexSyncTestBlocks(t, idb, 12)
			s.getLatestHeightOverride = fixedLatestHeight(5)
			s.fetchBlockOverride = func(context.Context, string, uint64) (*pb.Block, error) {
				return nil, test.loadErr
			}

			s.syncOnce(context.Background(), 0)

			assertSyncHeight(t, idb, 12)
			status, _ := s.GetStatus()
			if !strings.Contains(status, "unable to verify overlap at height 5") || !strings.Contains(status, test.loadErr.Error()) || !strings.Contains(status, "keeping existing index") {
				t.Fatalf("unexpected status: %q", status)
			}
		})
	}
}

func TestSyncOnceLateTriggerUsesCurrentSourceTipWithoutDeletingIndex(t *testing.T) {
	s, idb := newTestSyncer(t)
	indexSyncTestBlocks(t, idb, 12)
	tipCalls := 0
	s.getLatestHeightOverride = func(context.Context, string) (uint64, error) {
		tipCalls++
		return 12, nil
	}
	s.lastIntegrityCheck = time.Now()
	fetchCalls := 0
	s.fetchBlockOverride = func(context.Context, string, uint64) (*pb.Block, error) {
		fetchCalls++
		return nil, errors.New("unexpected block fetch")
	}

	s.syncOnce(context.Background(), 5)

	assertSyncHeight(t, idb, 12)
	if tipCalls != 1 {
		t.Fatalf("source tip calls = %d, want 1", tipCalls)
	}
	if fetchCalls != 0 {
		t.Fatalf("block fetches = %d, want 0 for a covered late trigger", fetchCalls)
	}
	status, _ := s.GetStatus()
	if !strings.Contains(status, "trigger height 5 already covered at 12") {
		t.Fatalf("unexpected status: %q", status)
	}
}

func TestSyncOnceOldSourceCannotResetAfterSourceSwitch(t *testing.T) {
	s, idb := newTestSyncer(t)
	indexSyncTestBlocks(t, idb, 12)
	if err := idb.Set("old_chain_marker", "present"); err != nil {
		t.Fatal(err)
	}
	s.getLatestHeightOverride = fixedLatestHeight(5)
	s.fetchBlockOverride = func(_ context.Context, node string, height uint64) (*pb.Block, error) {
		if node != "https://node.example" || height != 5 {
			t.Fatalf("old round requested node=%q height=%d", node, height)
		}
		s.SetNode("https://replacement.example")
		return syncTestBlock(height, "different-chain-hash"), nil
	}

	s.syncOnce(context.Background(), 0)

	assertSyncHeight(t, idb, 12)
	if marker, err := idb.Get("old_chain_marker"); err != nil || marker != "present" {
		t.Fatalf("old chain marker after source switch = %q, err=%v", marker, err)
	}
	status, _ := s.GetStatus()
	if !strings.Contains(status, "source changed before stale index reset") {
		t.Fatalf("unexpected status: %q", status)
	}
}

func TestSyncOnceOldSourceCannotCommitAfterSourceSwitch(t *testing.T) {
	s, idb := newTestSyncer(t)
	indexSyncTestBlock(t, idb, syncTestBlock(1, "hash-1"))
	s.getLatestHeightOverride = fixedLatestHeight(2)
	s.lastIntegrityCheck = time.Now()
	s.fetchBlockOverride = func(_ context.Context, _ string, height uint64) (*pb.Block, error) {
		s.SetNode("https://replacement.example")
		return syncTestBlockWithParent(height, "hash-2", "hash-1"), nil
	}

	s.syncOnce(context.Background(), 0)

	assertSyncHeight(t, idb, 1)
	if hash, err := idb.GetBlockHash(2); err != nil || hash != "" {
		t.Fatalf("old-source block was committed after source switch: hash=%q err=%v", hash, err)
	}
	status, _ := s.GetStatus()
	if !strings.Contains(status, "source changed before block commit") {
		t.Fatalf("unexpected status: %q", status)
	}
}

func TestSyncOnceDoesNotSkipMissingBlockAfterHistoryStarted(t *testing.T) {
	s, idb := newTestSyncer(t)
	if err := idb.SetPartialHistoryBase(10, "test baseline"); err != nil {
		t.Fatal(err)
	}
	indexSyncTestBlock(t, idb, syncTestBlock(10, "hash-10"))
	s.getLatestHeightOverride = fixedLatestHeight(13)
	requested := make([]uint64, 0)
	s.fetchBlockOverride = func(_ context.Context, _ string, height uint64) (*pb.Block, error) {
		requested = append(requested, height)
		if height == 11 {
			return nil, errBlockNotFound
		}
		return syncTestBlock(height, fmt.Sprintf("hash-%d", height)), nil
	}

	s.syncOnce(context.Background(), 0)

	assertSyncHeight(t, idb, 10)
	if containsHeight(requested, 12) {
		t.Fatalf("syncer skipped missing height 11 and requested 12: %v", requested)
	}
	status, _ := s.GetStatus()
	if !strings.Contains(status, "blocked at height 11") || !strings.Contains(status, "sync remains at 10") {
		t.Fatalf("unexpected status: %q", status)
	}
}

func TestSyncOnceRejectsWrongParentAfterStaleCheckPasses(t *testing.T) {
	s, idb := newTestSyncer(t)
	indexSyncTestBlock(t, idb, syncTestBlock(10, "hash-10"))
	s.getLatestHeightOverride = fixedLatestHeight(11)
	s.fetchBlockOverride = func(_ context.Context, _ string, height uint64) (*pb.Block, error) {
		switch height {
		case 10:
			return syncTestBlock(10, "hash-10"), nil
		case 11:
			return syncTestBlockWithParent(11, "hash-11", "wrong-parent"), nil
		default:
			return nil, fmt.Errorf("unexpected height %d", height)
		}
	}

	s.syncOnce(context.Background(), 0)

	assertSyncHeight(t, idb, 10)
	if hash, err := idb.GetBlockHash(11); err != nil || hash != "" {
		t.Fatalf("block 11 hash after parent mismatch = %q, err=%v", hash, err)
	}
	status, _ := s.GetStatus()
	if !strings.Contains(status, "error validating block 11") || !strings.Contains(status, `parent hash mismatch: got "wrong-parent", want "hash-10"`) {
		t.Fatalf("unexpected status: %q", status)
	}
}

func TestSyncOnceDoesNotRefetchIndexedTipWhenSourceAdvances(t *testing.T) {
	s, idb := newTestSyncer(t)
	indexSyncTestBlock(t, idb, syncTestBlock(10, "hash-10"))
	s.getLatestHeightOverride = fixedLatestHeight(11)
	s.lastIntegrityCheck = time.Now()
	requested := make([]uint64, 0, 1)
	s.fetchBlockOverride = func(_ context.Context, _ string, height uint64) (*pb.Block, error) {
		requested = append(requested, height)
		return syncTestBlockWithParent(height, fmt.Sprintf("hash-%d", height), fmt.Sprintf("hash-%d", height-1)), nil
	}

	s.syncOnce(context.Background(), 0)

	assertSyncHeight(t, idb, 11)
	if len(requested) != 1 || requested[0] != 11 {
		t.Fatalf("requested heights = %v, want only new block 11", requested)
	}
}

func TestSyncOncePartialBaseAllowsUnknownParentAndRequiresNextLink(t *testing.T) {
	for _, test := range []struct {
		name       string
		nextParent string
		wantHeight uint64
		wantError  bool
	}{
		{name: "linked next block", nextParent: "hash-50", wantHeight: 51},
		{name: "wrong next parent", nextParent: "wrong-parent", wantHeight: 50, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, idb := newTestSyncer(t)
			if err := idb.SetPartialHistoryBase(50, "source prefix pruned"); err != nil {
				t.Fatal(err)
			}
			s.getLatestHeightOverride = fixedLatestHeight(51)
			s.fetchBlockOverride = func(_ context.Context, _ string, height uint64) (*pb.Block, error) {
				switch height {
				case 50:
					return syncTestBlockWithParent(50, "hash-50", "unknown-pruned-parent"), nil
				case 51:
					return syncTestBlockWithParent(51, "hash-51", test.nextParent), nil
				default:
					return nil, fmt.Errorf("unexpected height %d", height)
				}
			}

			s.syncOnce(context.Background(), 0)

			assertSyncHeight(t, idb, test.wantHeight)
			assertPartialBase(t, idb, 50)
			status, _ := s.GetStatus()
			if test.wantError {
				if !strings.Contains(status, "error validating block 51") || !strings.Contains(status, "parent hash mismatch") {
					t.Fatalf("unexpected status: %q", status)
				}
				if hash, err := idb.GetBlockHash(51); err != nil || hash != "" {
					t.Fatalf("block 51 hash after parent mismatch = %q, err=%v", hash, err)
				}
			} else if !strings.Contains(status, "synced to 51") {
				t.Fatalf("unexpected status: %q", status)
			}
		})
	}
}

func TestSyncOnceDoesNotBootstrapWhenUnsyncedIndexHasData(t *testing.T) {
	s, idb := newTestSyncer(t)
	if err := idb.Set(indexdb.KeyTxDetail("orphan"), `{}`); err != nil {
		t.Fatal(err)
	}
	s.getLatestHeightOverride = fixedLatestHeight(200)
	requested := make([]uint64, 0)
	s.fetchBlockOverride = retainedBlocksFrom(100, &requested)

	s.syncOnce(context.Background(), 0)

	assertSyncHeight(t, idb, 0)
	metadata, err := idb.GetHistoryMetadata()
	if err != nil || metadata != nil {
		t.Fatalf("metadata = %+v, err=%v", metadata, err)
	}
	if len(requested) != 1 || requested[0] != 1 {
		t.Fatalf("requested heights = %v, want only 1", requested)
	}
	status, _ := s.GetStatus()
	if !strings.Contains(status, "existing index data prevents skipping a gap") {
		t.Fatalf("unexpected status: %q", status)
	}
}

func TestSyncOnceResetCanBootstrapPartialHistory(t *testing.T) {
	s, idb := newTestSyncer(t)
	indexSyncTestBlock(t, idb, syncTestBlock(5, "old-hash"))
	if err := idb.Set("legacy_marker", "legacy"); err != nil {
		t.Fatal(err)
	}
	s.maxBlocksPerSync = 1
	s.partialHistoryWindow = 20
	s.getLatestHeightOverride = fixedLatestHeight(200)
	s.fetchBlockOverride = func(_ context.Context, _ string, height uint64) (*pb.Block, error) {
		if height == 1 {
			return nil, errBlockNotFound
		}
		return syncTestBlockWithParent(height, fmt.Sprintf("new-hash-%d", height), fmt.Sprintf("new-hash-%d", height-1)), nil
	}

	s.syncOnce(context.Background(), 0)

	assertSyncHeight(t, idb, 181)
	assertPartialBase(t, idb, 181)
	if value, err := idb.Get("legacy_marker"); err != nil || value != "" {
		t.Fatalf("legacy marker after reset = %q, err=%v", value, err)
	}
}

func TestSyncOnceFindsFirstRetainedHeightInsideWindow(t *testing.T) {
	s, idb := newTestSyncer(t)
	s.maxBlocksPerSync = 1
	s.partialHistoryWindow = 20
	s.getLatestHeightOverride = fixedLatestHeight(200)
	s.fetchBlockOverride = retainedBlocksFrom(190, nil)

	s.syncOnce(context.Background(), 0)

	assertSyncHeight(t, idb, 190)
	assertPartialBase(t, idb, 190)
}

func TestSyncOnceKeepsEmptyIndexBlockedWhenSourceTipUnavailable(t *testing.T) {
	s, idb := newTestSyncer(t)
	s.partialHistoryWindow = 20
	s.getLatestHeightOverride = fixedLatestHeight(200)
	s.fetchBlockOverride = func(_ context.Context, _ string, _ uint64) (*pb.Block, error) {
		return nil, errBlockNotFound
	}

	s.syncOnce(context.Background(), 0)

	assertSyncHeight(t, idb, 0)
	metadata, err := idb.GetHistoryMetadata()
	if err != nil || metadata != nil {
		t.Fatalf("metadata = %+v, err=%v", metadata, err)
	}
	status, _ := s.GetStatus()
	if !strings.Contains(status, "partial bootstrap failed") || !strings.Contains(status, "latest source block 200 unavailable") {
		t.Fatalf("unexpected status: %q", status)
	}
}

func TestLoadLocalBlockOnlyClassifiesMissingHeightIndexAsNotFound(t *testing.T) {
	ioErr := errors.New("disk read failed")
	if _, err := loadLocalBlock(&fakeLocalKVReader{errs: map[string]error{keys.KeyHeightBlocks(7): ioErr}}, 7); !errors.Is(err, ioErr) || errors.Is(err, errBlockNotFound) {
		t.Fatalf("I/O error classification = %v", err)
	}
	if _, err := loadLocalBlock(&fakeLocalKVReader{errs: map[string]error{keys.KeyHeightBlocks(7): pebble.ErrNotFound}}, 7); !errors.Is(err, errBlockNotFound) {
		t.Fatalf("missing height index classification = %v", err)
	}
	reader := &fakeLocalKVReader{
		values: map[string][]byte{keys.KeyHeightBlocks(7): []byte(`["block-7"]`)},
		errs:   map[string]error{keys.KeyBlockData("block-7"): pebble.ErrNotFound},
	}
	if _, err := loadLocalBlock(reader, 7); err == nil || errors.Is(err, errBlockNotFound) {
		t.Fatalf("dangling height index classification = %v", err)
	}
}

func TestLoadLocalBlockReadsIndexedBlock(t *testing.T) {
	want := syncTestBlock(7, "block-7")
	raw, err := db.ProtoMarshal(want)
	if err != nil {
		t.Fatal(err)
	}
	model, err := emptyReadModelForBlock(want)
	if err != nil {
		t.Fatal(err)
	}
	modelRaw, err := db.ProtoMarshal(model)
	if err != nil {
		t.Fatal(err)
	}
	reader := &fakeLocalKVReader{values: map[string][]byte{
		keys.KeyHeightBlocks(7):           []byte(`["block-7"]`),
		keys.KeyBlockData("block-7"):      raw,
		keys.KeyBlockReadModel("block-7"): modelRaw,
	}}
	got, err := loadLocalBlock(reader, 7)
	if err != nil {
		t.Fatal(err)
	}
	if got.Block == nil || got.ReadModel == nil || got.Block.BlockHash != want.BlockHash || got.Block.Header == nil || got.Block.Header.Height != 7 {
		t.Fatalf("loaded block = %+v", got)
	}
}

func TestSyncOnceDoesNotPersistPartialBaseForInvalidSourceTip(t *testing.T) {
	s, idb := newTestSyncer(t)
	s.partialHistoryWindow = 20
	s.getLatestHeightOverride = fixedLatestHeight(200)
	s.fetchBlockOverride = func(_ context.Context, _ string, height uint64) (*pb.Block, error) {
		if height == 1 {
			return nil, errBlockNotFound
		}
		return syncTestBlock(height-1, fmt.Sprintf("wrong-hash-%d", height)), nil
	}

	s.syncOnce(context.Background(), 0)

	assertSyncHeight(t, idb, 0)
	metadata, err := idb.GetHistoryMetadata()
	if err != nil || metadata != nil {
		t.Fatalf("metadata = %+v, err=%v", metadata, err)
	}
	status, _ := s.GetStatus()
	if !strings.Contains(status, "source returned height 199 for requested height 200") {
		t.Fatalf("unexpected status: %q", status)
	}
}

func newTestSyncer(t *testing.T) (*Syncer, *indexdb.IndexDB) {
	t.Helper()
	idb, err := indexdb.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := New(idb, "https://node.example", time.Hour)
	t.Cleanup(func() {
		if closer, ok := s.httpClient.Transport.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
		_ = idb.Close()
	})
	return s, idb
}

func indexSyncTestBlocks(t *testing.T, idb *indexdb.IndexDB, count uint64) {
	t.Helper()
	for height := uint64(1); height <= count; height++ {
		indexSyncTestBlock(t, idb, syncTestBlock(height, fmt.Sprintf("hash-%d", height)))
	}
}

func indexSyncTestBlock(t *testing.T, idb *indexdb.IndexDB, block *pb.Block) {
	t.Helper()
	model, err := emptyReadModelForBlock(block)
	if err != nil {
		t.Fatal(err)
	}
	if err := idb.IndexBlockWithReadModel(block, model); err != nil {
		t.Fatalf("IndexBlockWithReadModel(%d): %v", block.Header.Height, err)
	}
}

func fixedLatestHeight(height uint64) func(context.Context, string) (uint64, error) {
	return func(context.Context, string) (uint64, error) { return height, nil }
}

func retainedBlocksFrom(first uint64, requested *[]uint64) func(context.Context, string, uint64) (*pb.Block, error) {
	return func(_ context.Context, _ string, height uint64) (*pb.Block, error) {
		if requested != nil {
			*requested = append(*requested, height)
		}
		if height < first {
			return nil, errBlockNotFound
		}
		return syncTestBlockWithParent(height, fmt.Sprintf("hash-%d", height), fmt.Sprintf("hash-%d", height-1)), nil
	}
}

func syncTestBlock(height uint64, hash string) *pb.Block {
	return &pb.Block{Header: &pb.BlockHeader{Height: height}, BlockHash: hash}
}

func syncTestBlockWithParent(height uint64, hash, parentHash string) *pb.Block {
	return &pb.Block{Header: &pb.BlockHeader{Height: height, PrevBlockHash: parentHash}, BlockHash: hash}
}

func assertSyncHeight(t *testing.T, idb *indexdb.IndexDB, want uint64) {
	t.Helper()
	got, err := idb.GetSyncHeight()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("sync height = %d, want %d", got, want)
	}
}

func assertPartialBase(t *testing.T, idb *indexdb.IndexDB, want uint64) {
	t.Helper()
	metadata, err := idb.GetHistoryMetadata()
	if err != nil {
		t.Fatal(err)
	}
	if metadata == nil || !metadata.Partial || metadata.BaseHeight != want {
		t.Fatalf("history metadata = %+v, want partial base %d", metadata, want)
	}
}

func containsHeight(heights []uint64, want uint64) bool {
	for _, height := range heights {
		if height == want {
			return true
		}
	}
	return false
}

type fakeLocalKVReader struct {
	values map[string][]byte
	errs   map[string]error
}

func (r *fakeLocalKVReader) GetKV(key string) ([]byte, error) {
	if err := r.errs[key]; err != nil {
		return nil, err
	}
	if value, ok := r.values[key]; ok {
		return value, nil
	}
	return nil, pebble.ErrNotFound
}
