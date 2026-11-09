package db

import (
	"errors"
	"strconv"
	"testing"

	"dex/keys"
	"dex/logs"

	"github.com/cockroachdb/pebble"
)

func newServiceArchiveTestManager(t *testing.T) *Manager {
	t.Helper()
	mgr, err := NewManager(t.TempDir(), logs.NewNodeLogger("service-archive", 0))
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { mgr.Close() })
	return mgr
}

func archiveRange(t *testing.T, mgr *Manager) (uint64, uint64) {
	t.Helper()
	from, through, err := mgr.GetBlockArchiveRange()
	if err != nil {
		t.Fatalf("GetBlockArchiveRange: %v", err)
	}
	return from, through
}

func TestFreshArchiveCoverageStartsAtOneAndCommitsContinuously(t *testing.T) {
	mgr := newServiceArchiveTestManager(t)
	if from, through := archiveRange(t, mgr); from != 0 || through != 0 {
		t.Fatalf("fresh unobserved range=%d..%d, want empty", from, through)
	}

	mgr.ObserveBlockArchiveHeight(1)
	mgr.ObserveBlockArchiveHeight(2)
	                                                                        
	                                                      
	if from, through := archiveRange(t, mgr); from != 0 || through != 0 {
		t.Fatalf("uncommitted range=%d..%d, want empty", from, through)
	}
	if err := mgr.CommitBlockArchiveCoverage(); err != nil {
		t.Fatalf("CommitBlockArchiveCoverage: %v", err)
	}
	if from, through := archiveRange(t, mgr); from != 1 || through != 2 {
		t.Fatalf("committed range=%d..%d, want 1..2", from, through)
	}

	mgr.ObserveBlockArchiveHeight(3)
	if err := mgr.CommitBlockArchiveCoverage(); err != nil {
		t.Fatal(err)
	}
	if from, through := archiveRange(t, mgr); from != 1 || through != 3 {
		t.Fatalf("extended range=%d..%d, want 1..3", from, through)
	}
}

func TestLegacyDatabaseWithoutMarkerDoesNotInferHistory(t *testing.T) {
	dir := t.TempDir()
	legacy, err := NewManager(dir, logs.NewNodeLogger("service-archive-legacy-seed", 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.runtimeDB.Set([]byte(keys.KeyLatestHeight()), []byte(strconv.FormatUint(250, 10)), pebble.Sync); err != nil {
		t.Fatal(err)
	}
	legacy.Close()

	                                                                       
	                                             
	mgr, err := NewManager(dir, logs.NewNodeLogger("service-archive-legacy-reopen", 0))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mgr.Close() })
	if from, through := archiveRange(t, mgr); from != 0 || through != 0 {
		t.Fatalf("legacy history was inferred as %d..%d", from, through)
	}

	                                                                       
	                                                                         
	mgr.ObserveBlockArchiveHeight(250)
	if err := mgr.CommitBlockArchiveCoverage(); err != nil {
		t.Fatal(err)
	}
	if from, through := archiveRange(t, mgr); from != 0 || through != 0 {
		t.Fatalf("old observation created range=%d..%d", from, through)
	}
	mgr.ObserveBlockArchiveHeight(251)
	if err := mgr.CommitBlockArchiveCoverage(); err != nil {
		t.Fatal(err)
	}
	if from, through := archiveRange(t, mgr); from != 251 || through != 251 {
		t.Fatalf("legacy tail range=%d..%d, want 251..251", from, through)
	}
}

func TestArchiveCoveragePersistsAndGapStartsNewTail(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(dir, logs.NewNodeLogger("service-archive-persist", 0))
	if err != nil {
		t.Fatal(err)
	}
	mgr.ObserveBlockArchiveHeight(1)
	mgr.ObserveBlockArchiveHeight(2)
	if err := mgr.CommitBlockArchiveCoverage(); err != nil {
		t.Fatal(err)
	}
	mgr.Close()

	reopened, err := NewManager(dir, logs.NewNodeLogger("service-archive-reopen", 0))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	if from, through := archiveRange(t, reopened); from != 1 || through != 2 {
		t.Fatalf("reopened range=%d..%d, want 1..2", from, through)
	}
	reopened.ObserveBlockArchiveHeight(4)
	if err := reopened.CommitBlockArchiveCoverage(); err != nil {
		t.Fatal(err)
	}
	if from, through := archiveRange(t, reopened); from != 4 || through != 4 {
		t.Fatalf("gap bridged or tail not reset: %d..%d", from, through)
	}
}

func TestPruneCursorRaisesAdvertisedFrom(t *testing.T) {
	mgr := newServiceArchiveTestManager(t)
	for height := uint64(1); height <= 250; height++ {
		mgr.ObserveBlockArchiveHeight(height)
	}
	if err := mgr.CommitBlockArchiveCoverage(); err != nil {
		t.Fatal(err)
	}
	if err := mgr.setBlockPruneCursor(101); err != nil {
		t.Fatal(err)
	}
	if from, through := archiveRange(t, mgr); from != 101 || through != 250 {
		t.Fatalf("pruned range=%d..%d, want 101..250", from, through)
	}
	if err := mgr.setBlockPruneCursor(251); err != nil {
		t.Fatal(err)
	}
	if from, through := archiveRange(t, mgr); from != 0 || through != 0 {
		t.Fatalf("fully pruned range=%d..%d, want empty", from, through)
	}
}

func TestPrepareArchivePruneShrinksDurableClaimBeforeCursorMoves(t *testing.T) {
	mgr := newServiceArchiveTestManager(t)
	for height := uint64(1); height <= 10; height++ {
		mgr.ObserveBlockArchiveHeight(height)
	}
	if err := mgr.CommitBlockArchiveCoverage(); err != nil {
		t.Fatal(err)
	}
	if err := mgr.PrepareBlockArchivePrune(6); err != nil {
		t.Fatal(err)
	}
	                                                                       
	                                                        
	if from, through := archiveRange(t, mgr); from != 6 || through != 10 {
		t.Fatalf("prepared range=%d..%d, want 6..10", from, through)
	}
	marker, err := mgr.loadBlockArchiveCoverage()
	if err != nil {
		t.Fatal(err)
	}
	if marker.From != 6 || marker.Through != 10 {
		t.Fatalf("durable prepared marker=%+v", marker)
	}

	if err := mgr.PrepareBlockArchivePrune(11); err != nil {
		t.Fatal(err)
	}
	if from, through := archiveRange(t, mgr); from != 0 || through != 0 {
		t.Fatalf("fully prepared range=%d..%d, want empty", from, through)
	}
}

func TestPruneOldBlockDataPreparesCoverageEvenAcrossMissingHeights(t *testing.T) {
	mgr := newServiceArchiveTestManager(t)
	for height := uint64(1); height <= 10; height++ {
		mgr.ObserveBlockArchiveHeight(height)
	}
	if err := mgr.CommitBlockArchiveCoverage(); err != nil {
		t.Fatal(err)
	}
	result, err := mgr.PruneOldBlockData(1_000, 5)
	if err != nil {
		t.Fatal(err)
	}
	if result.NextCursor != 6 {
		t.Fatalf("next cursor=%d want=6", result.NextCursor)
	}
	if from, through := archiveRange(t, mgr); from != 6 || through != 10 {
		t.Fatalf("post-prune range=%d..%d, want 6..10", from, through)
	}
}

func TestPruneBlockHeightDeletesFinalizationChitsWithOrWithoutArchiveIndex(t *testing.T) {
	mgr := newServiceArchiveTestManager(t)
	for _, height := range []uint64{17, 18} {
		key := "finalization_chits_" + strconv.FormatUint(height, 10)
		if err := mgr.runtimeDB.Set([]byte(key), []byte(`{"height":1}`), pebble.Sync); err != nil {
			t.Fatal(err)
		}
	}
	if err := mgr.archiveDB.Set([]byte(keys.KeyHeightBlocks(17)), []byte("[]"), pebble.Sync); err != nil {
		t.Fatal(err)
	}

	for _, height := range []uint64{17, 18} {
		if _, err := mgr.pruneBlockHeight(height); err != nil {
			t.Fatalf("prune height %d: %v", height, err)
		}
		key := "finalization_chits_" + strconv.FormatUint(height, 10)
		if _, closer, err := mgr.runtimeDB.Get([]byte(key)); !errors.Is(err, pebble.ErrNotFound) {
			if closer != nil {
				_ = closer.Close()
			}
			t.Fatalf("finalization chits %d still exist: %v", height, err)
		}
	}
}

func TestCheckpointResetClearsCoverageAndStartsAfterCheckpoint(t *testing.T) {
	mgr := newServiceArchiveTestManager(t)
	for height := uint64(1); height <= 20; height++ {
		mgr.ObserveBlockArchiveHeight(height)
	}
	if err := mgr.CommitBlockArchiveCoverage(); err != nil {
		t.Fatal(err)
	}
	if err := mgr.ResetBlockArchiveCoverage(1000); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.GetKV(keys.KeyBlockArchiveCoverage()); !errors.Is(err, pebble.ErrNotFound) {
		t.Fatalf("checkpoint reset retained marker: %v", err)
	}
	if from, through := archiveRange(t, mgr); from != 0 || through != 0 {
		t.Fatalf("checkpoint reset range=%d..%d, want empty", from, through)
	}
	mgr.ObserveBlockArchiveHeight(1000)
	mgr.ObserveBlockArchiveHeight(1001)
	if err := mgr.CommitBlockArchiveCoverage(); err != nil {
		t.Fatal(err)
	}
	if from, through := archiveRange(t, mgr); from != 1001 || through != 1001 {
		t.Fatalf("post-checkpoint range=%d..%d, want 1001..1001", from, through)
	}
}

func TestMalformedCoverageNeverAdvertises(t *testing.T) {
	mgr := newServiceArchiveTestManager(t)
	if err := mgr.runtimeDB.Set([]byte(keys.KeyBlockArchiveCoverage()), []byte(`{"version":1,"from":5,"through":4}`), pebble.Sync); err != nil {
		t.Fatal(err)
	}
	if _, _, err := mgr.GetBlockArchiveRange(); err == nil {
		t.Fatal("malformed marker was accepted")
	}
	if err := mgr.runtimeDB.Set([]byte(keys.KeyLatestHeight()), []byte("10"), pebble.Sync); err != nil {
		t.Fatal(err)
	}
	mgr.initializeBlockArchiveCoverage()
	                                                                          
	                                                                   
	mgr.ObserveBlockArchiveHeight(10)
	if err := mgr.CommitBlockArchiveCoverage(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := mgr.GetBlockArchiveRange(); err == nil {
		t.Fatal("malformed marker was silently reused")
	}
	mgr.ObserveBlockArchiveHeight(11)
	if err := mgr.CommitBlockArchiveCoverage(); err != nil {
		t.Fatal(err)
	}
	if from, through := archiveRange(t, mgr); from != 11 || through != 11 {
		t.Fatalf("replacement range=%d..%d, want 11..11", from, through)
	}
}

func TestBlockArchiveCoverageKeyIsLocalRuntimeMetadata(t *testing.T) {
	key := keys.KeyBlockArchiveCoverage()
	if keys.IsStatefulKey(key) {
		t.Fatal("block archive coverage marker entered StateDB")
	}
	if class := keys.StorageClassForKey(key); class != keys.StorageRuntime {
		t.Fatalf("coverage storage class=%s, want runtime", class)
	}
}

func TestGetLatestStateCheckpointHeightEmptyStore(t *testing.T) {
	mgr := newServiceArchiveTestManager(t)
	height, err := mgr.GetLatestStateCheckpointHeight()
	if err != nil {
		t.Fatalf("get latest checkpoint: %v", err)
	}
	if height != 0 {
		t.Fatalf("latest checkpoint = %d, want 0", height)
	}
}
