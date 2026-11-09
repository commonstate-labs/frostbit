package db

import (
	"encoding/binary"
	"path/filepath"
	"strings"
	"testing"

	"dex/keys"
	"dex/logs"
	"dex/pb"
	"dex/utils/diskspace"

	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/require"
)

func TestQueryPruneDeletesTerminalArchiveDetailsOnly(t *testing.T) {
	mgr := newQueryPruneTestManager(t)
	archiveKeys := []string{
		keys.KeyArchiveRechargeRequest("done"), keys.KeyArchiveWitnessVote("done", "voter"),
		keys.KeyArchiveChallengeRecord("done"), keys.KeyArchiveChallengeContribution("done", "tx"),
	}
	for _, key := range archiveKeys {
		mgr.EnqueueSet(key, "old-detail")
	}
	protected := []string{keys.KeyArchiveFrostMigration("migration"), keys.KeyArchiveRechargeRequest("recent")}
	for _, key := range protected {
		mgr.EnqueueSet(key, "keep")
	}
	stateKey := keys.KeyFrostVaultHandoverProof("eth", 0, 2)
	_, err := mgr.SyncStateUpdates(1, toIfaceOps(syncTestOp{key: stateKey, val: []byte("proof")}))
	require.NoError(t, err)
	root := mgr.GetStateFingerprint()
	mgr.EnqueueQueryPruneManifest(1, archiveKeys, nil, nil)
	mgr.EnqueueQueryPruneManifest(2, []string{protected[1]}, nil, nil)
	require.NoError(t, mgr.setBlockPruneCursor(2))
	result, err := mgr.PruneQueryDataWithDiskUsage(100, diskspace.Snapshot{TotalBytes: 100, FreePercent: 0})
	require.NoError(t, err)
	require.Equal(t, uint64(1), result.PrunedHeights)
	for _, key := range archiveKeys {
		_, err := mgr.GetKV(key)
		require.ErrorIs(t, err, pebble.ErrNotFound)
		require.Contains(t, result.CompactedFamilies, strings.TrimSuffix(keys.PrunableArchiveHistoryPrefix(key), "_"))
	}
	for _, key := range protected {
		_, err := mgr.GetKV(key)
		require.NoError(t, err)
	}
	require.Equal(t, root, mgr.GetStateFingerprint())
}

func TestQueryPruneFlushesArchiveEvenWhenManifestAlreadyVisible(t *testing.T) {
	mgr := newQueryPruneTestManager(t)
	key := keys.KeyArchiveRechargeRequest("late-cold-write")
	mgr.EnqueueQueryPruneManifest(1, []string{key}, nil, nil)
	require.NoError(t, mgr.ForceFlush())
	                                                                              
	mgr.EnqueueSet(key, "pending archive data")
	require.NoError(t, mgr.setBlockPruneCursor(2))
	_, err := mgr.PruneQueryDataWithDiskUsage(100, diskspace.Snapshot{TotalBytes: 100, FreePercent: 50})
	require.NoError(t, err)
	require.NoError(t, mgr.ForceFlush())
	_, err = mgr.GetKV(key)
	require.ErrorIs(t, err, pebble.ErrNotFound)
}

func TestArchivePruneRetainsManifestOnWriteFailureAndResumesAfterRestart(t *testing.T) {
	mgr := newQueryPruneTestManager(t)
	dir, cfg := mgr.dataDir, mgr.cfg
	key := keys.KeyArchiveChallengeRecord("finalized")
	mgr.EnqueueSet(key, "detail")
	mgr.EnqueueQueryPruneManifest(1, []string{key}, nil, nil)
	require.NoError(t, mgr.ForceFlush())
	require.NoError(t, mgr.setBlockPruneCursor(2))
	require.NoError(t, mgr.archiveDB.Close())
	archive, err := pebble.Open(filepath.Join(dir, "archive"), &pebble.Options{ReadOnly: true})
	require.NoError(t, err)
	mgr.archiveDB = archive
	_, err = mgr.PruneQueryDataWithDiskUsage(100, diskspace.Snapshot{TotalBytes: 100, FreePercent: 50})
	require.ErrorIs(t, err, pebble.ErrReadOnly)
	_, found, err := queryPruneManifestChunk(mgr.queryDB, 1, 0)
	require.NoError(t, err)
	require.True(t, found, "failed archive deletion must retain its retry manifest")
	mgr.Close()
	restarted, err := NewManagerWithConfig(dir, logs.NewNodeLogger("archive-restart", 0), cfg)
	require.NoError(t, err)
	t.Cleanup(restarted.Close)
	result, err := restarted.PruneQueryDataWithDiskUsage(100, diskspace.Snapshot{TotalBytes: 100, FreePercent: 50})
	require.NoError(t, err)
	require.Equal(t, uint64(2), result.NextCursor)
	_, err = restarted.GetKV(key)
	require.ErrorIs(t, err, pebble.ErrNotFound)
}

func TestArchiveManifestRejectsConsensusAndExecutionArtifacts(t *testing.T) {
	for _, key := range []string{keys.KeyFrostVaultHandoverProof("eth", 0, 2), keys.KeyArchiveFrostMigration("migration")} {
		mgr := newQueryPruneTestManager(t)
		raw := binary.AppendUvarint([]byte{queryPruneManifestRecordArchiveKey}, uint64(len(key)))
		raw = append(raw, key...)
		require.NoError(t, mgr.queryDB.Set([]byte(keys.KeyQueryPruneManifestChunk(1, 0)), raw, pebble.Sync))
		require.NoError(t, mgr.queryDB.Set([]byte(keys.KeyQueryPruneManifestDone(1)), []byte("1"), pebble.Sync))
		_, err := mgr.pruneQueryDataThroughHeight(100, 1, false)
		require.ErrorContains(t, err, "unsafe archive prune manifest")
		_, found, err := queryPruneManifestChunk(mgr.queryDB, 1, 0)
		require.NoError(t, err)
		require.True(t, found)
	}
}

func TestBlockPruneSecondStoreFailureKeepsRetryDirectory(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(dir, logs.NewNodeLogger("block-prune-retry", 0))
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Close() })
	blockID := "old-block"
	txID := "old-tx"
	block, err := ProtoMarshal(&pb.Block{Body: []*pb.AnyTx{{Content: &pb.AnyTx_Transaction{Transaction: &pb.Transaction{Base: &pb.BaseMessage{TxId: txID}}}}}})
	require.NoError(t, err)
	require.NoError(t, mgr.archiveDB.Set([]byte(keys.KeyHeightBlocks(1)), []byte(`["old-block"]`), pebble.Sync))
	require.NoError(t, mgr.archiveDB.Set([]byte(keys.KeyBlockData(blockID)), block, pebble.Sync))
	require.NoError(t, mgr.archiveDB.Set([]byte(keys.KeyTxRaw(txID)), []byte("raw-tx"), pebble.Sync))
	require.NoError(t, mgr.runtimeDB.Set([]byte(keys.KeyBlockReadModel(blockID)), []byte("sidecar"), pebble.Sync))
	require.NoError(t, mgr.archiveDB.Close())
	mgr.archiveDB, err = pebble.Open(filepath.Join(dir, "archive"), &pebble.Options{ReadOnly: true})
	require.NoError(t, err)
	_, err = mgr.PruneOldBlockData(1000, 1)
	require.ErrorIs(t, err, pebble.ErrReadOnly)
	_, err = mgr.GetKV(keys.KeyHeightBlocks(1))
	require.NoError(t, err, "height directory must survive until both stores are deleted")
	_, err = mgr.GetKV(keys.KeyBlockReadModel(blockID))
	require.ErrorIs(t, err, pebble.ErrNotFound, "runtime delete must precede archive commit")
	_, err = mgr.GetKV(keys.KeyTxRaw(txID))
	require.NoError(t, err, "failed archive commit must retain transaction data for retry")
	cursor, err := mgr.blockPruneCursor()
	require.NoError(t, err)
	require.Equal(t, uint64(1), cursor)
	mgr.Close()
	mgr, err = NewManager(dir, logs.NewNodeLogger("block-prune-retry", 0))
	require.NoError(t, err)
	result, err := mgr.PruneOldBlockData(1000, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(2), result.NextCursor)
	_, err = mgr.GetKV(keys.KeyHeightBlocks(1))
	require.ErrorIs(t, err, pebble.ErrNotFound)
	_, err = mgr.GetKV(keys.KeyBlockData(blockID))
	require.ErrorIs(t, err, pebble.ErrNotFound)
	_, err = mgr.GetKV(keys.KeyTxRaw(txID))
	require.ErrorIs(t, err, pebble.ErrNotFound)
}
