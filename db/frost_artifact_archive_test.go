package db

import (
	"errors"
	"strconv"
	"testing"

	"dex/config"
	"dex/frost/artifact"
	"dex/keys"
	"dex/logs"
	"dex/utils/diskspace"

	"github.com/cockroachdb/pebble"
)

func newFrostArtifactTestManager(t *testing.T, archiveNode bool) *Manager {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Database.FrostArchiveNode = archiveNode
	cfg.Database.BlockPruneKeepRecentHeights = 1
	cfg.Database.BlockPruneStartFreePercent = 100
	cfg.Database.BlockPruneAggressiveFreePercent = 1
	mgr, err := NewManagerWithConfig(t.TempDir(), logs.NewNodeLogger("test", 0), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mgr.Close)
	return mgr
}

func seedFrostArtifact(t *testing.T, mgr *Manager, height uint64) string {
	t.Helper()
	record, err := artifact.NewRecord(artifact.Metadata{Kind: artifact.KindMigration, Chain: "eth", Height: height, SourceID: "migration-1"}, []byte{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := artifact.MarshalRecord(record)
	if err := mgr.archiveDB.Set([]byte(keys.KeyArchiveFrostArtifact(record.Metadata.ArtifactID)), raw, pebble.Sync); err != nil {
		t.Fatal(err)
	}
	if err := mgr.archiveDB.Set([]byte(keys.KeyArchiveFrostArtifactHeight(height, record.Metadata.ArtifactID)), []byte(record.Metadata.ArtifactID), pebble.Sync); err != nil {
		t.Fatal(err)
	}
	return record.Metadata.ArtifactID
}

func TestOrdinaryNodePrunesOnlyStateBackedArtifactButArchiveNodeRetainsIt(t *testing.T) {
	usage := diskspace.Snapshot{TotalBytes: 100, FreeBytes: 50, FreePercent: 50}
	ordinary := newFrostArtifactTestManager(t, false)
	id := seedFrostArtifact(t, ordinary, 1)
	raw, err := ordinary.GetKV(keys.KeyArchiveFrostArtifact(id))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ordinary.SyncStateUpdates(1, toIfaceOps(syncTestOp{key: keys.KeyFrostPermanentArtifact(id), val: raw})); err != nil {
		t.Fatal(err)
	}
	result, err := ordinary.PruneFrostArtifactsWithDiskUsage(10, usage)
	if err != nil || result.DeletedRecords != 1 {
		t.Fatalf("ordinary prune result=%+v err=%v", result, err)
	}
	if _, err := ordinary.GetKV(keys.KeyArchiveFrostArtifact(id)); !errors.Is(err, pebble.ErrNotFound) {
		t.Fatalf("ordinary node retained pruned artifact: %v", err)
	}

	backup := newFrostArtifactTestManager(t, true)
	id = seedFrostArtifact(t, backup, 1)
	result, err = backup.PruneFrostArtifactsWithDiskUsage(10, usage)
	if err != nil || !result.SkippedArchive {
		t.Fatalf("archive prune result=%+v err=%v", result, err)
	}
	if _, err := backup.GetKV(keys.KeyArchiveFrostArtifact(id)); err != nil {
		t.Fatalf("archive node lost artifact: %v", err)
	}
}

func TestArtifactPrunePreservesLegacyAndMismatchedPermanentCopies(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		mgr := newFrostArtifactTestManager(t, false)
		id := seedFrostArtifact(t, mgr, 1)
		if mismatch {
			if _, err := mgr.SyncStateUpdates(1, toIfaceOps(syncTestOp{key: keys.KeyFrostPermanentArtifact(id), val: []byte("different")})); err != nil {
				t.Fatal(err)
			}
		}
		res, err := mgr.PruneFrostArtifactsWithDiskUsage(10, diskspace.Snapshot{TotalBytes: 100, FreeBytes: 50, FreePercent: 50})
		if err != nil || res.DeletedRecords != 0 {
			t.Fatalf("mismatch=%v result=%+v err=%v", mismatch, res, err)
		}
		if _, err := mgr.GetKV(keys.KeyArchiveFrostArtifact(id)); err != nil {
			t.Fatal("lost the only valid archive", err)
		}
	}
}

func TestArchiveReadinessRequiresContinuousHeights(t *testing.T) {
	mgr := newFrostArtifactTestManager(t, true)
	if err := mgr.runtimeDB.Set([]byte(keys.KeyLatestHeight()), []byte(strconv.FormatUint(3, 10)), pebble.Sync); err != nil {
		t.Fatal(err)
	}
	mgr.ObserveFrostArchiveHeight(2)
	if err := mgr.CommitFrostArchiveCoverage(); err != nil {
		t.Fatal(err)
	}
	status, err := mgr.FrostArchiveStatus()
	if err != nil {
		t.Fatal(err)
	}
	if status.Ready || status.CompleteThrough != 0 {
		t.Fatalf("gap was incorrectly marked complete: %+v", status)
	}

	continuous := newFrostArtifactTestManager(t, true)
	if err := continuous.runtimeDB.Set([]byte(keys.KeyLatestHeight()), []byte(strconv.FormatUint(3, 10)), pebble.Sync); err != nil {
		t.Fatal(err)
	}
	for height := uint64(1); height <= 3; height++ {
		continuous.ObserveFrostArchiveHeight(height)
	}
	if err := continuous.CommitFrostArchiveCoverage(); err != nil {
		t.Fatal(err)
	}
	status, _ = continuous.FrostArchiveStatus()
	if !status.Ready || status.CompleteThrough != 3 {
		t.Fatalf("continuous archive not ready: %+v", status)
	}
}
