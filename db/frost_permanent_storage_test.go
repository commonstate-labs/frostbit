package db

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"dex/frost/artifact"
	"dex/keys"
	"dex/logs"
	"dex/utils/diskspace"
	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/require"
)

func TestPermanentFrostDeliverySurvivesPruneCheckpointAndRestart(t *testing.T) {
	source := newFrostArtifactTestManager(t, false)
	id := seedFrostArtifact(t, source, 1)
	raw, err := source.GetKV(keys.KeyArchiveFrostArtifact(id))
	require.NoError(t, err)
	record, err := artifact.UnmarshalRecord(raw)
	require.NoError(t, err)
	meta, err := artifact.MarshalMetadata(record.Metadata)
	require.NoError(t, err)
	expected := map[string][]byte{
		keys.KeyFrostPermanentArtifact(id):             raw,
		keys.KeyFrostArtifactMeta(id):                  meta,
		keys.KeyFrostPermanentWithdraw("wid"):          []byte("signed withdrawal"),
		keys.KeyFrostPermanentWithdrawJob("job"):       []byte("canonical template"),
		keys.KeyFrostPermanentSignedPackage("job", 0):  []byte("accepted signature"),
		keys.KeyFrostPermanentMigration("migration-1"): []byte("signed migration"),
		keys.KeyFrostPermanentWithdrawArtifact("wid"):  []byte(id),
		keys.KeyFrostWithdrawTxRef("request-tx"):       []byte("wid"),
	}
	var updates []syncTestOp
	for key, value := range expected {
		updates = append(updates, syncTestOp{key: key, val: value})
	}
	before := source.GetStateFingerprint()
	_, err = source.SyncStateUpdates(10, toIfaceOps(updates...))
	require.NoError(t, err)
	root := source.GetStateFingerprint()
	require.NotEqual(t, before, root, "permanent delivery must participate in state root")
	for key, value := range expected {
		got, err := source.Get(key)
		require.NoError(t, err)
		require.Equal(t, value, got)
		mustNotExistPebbleKey(t, source.runtimeDB, key)
		mustNotExistPebbleKey(t, source.queryDB, key)
		mustNotExistPebbleKey(t, source.archiveDB, key)
	}
	require.NoError(t, source.archiveDB.Set([]byte(keys.KeyArchiveFrostMigration("migration-1")), expected[keys.KeyFrostPermanentMigration("migration-1")], pebble.Sync))
	res, err := source.PruneFrostArtifactsWithDiskUsage(10, diskspace.Snapshot{TotalBytes: 100, FreeBytes: 50, FreePercent: 50})
	require.NoError(t, err)
	require.EqualValues(t, 1, res.DeletedRecords)
	mustNotExistPebbleKey(t, source.archiveDB, keys.KeyArchiveFrostArtifact(id))
	mustNotExistPebbleKey(t, source.archiveDB, keys.KeyArchiveFrostMigration("migration-1"))
	require.Equal(t, root, source.GetStateFingerprint(), "local pruning must not change state")
	require.NoError(t, source.EnsureStateCheckpoint(10))
	files, _, err := source.ListStateCheckpointFiles(10)
	require.NoError(t, err)
	importDir := t.TempDir()
	for _, file := range files {
		path := filepath.Join(importDir, filepath.FromSlash(file.Name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		out, err := os.Create(path)
		require.NoError(t, err)
		for offset := int64(0); offset < file.Size; {
			limit := min(int64(65536), file.Size-offset)
			n, err := source.WriteStateCheckpointChunk(10, file.Name, offset, limit, out)
			require.NoError(t, err)
			require.Equal(t, limit, n)
			offset += n
		}
		require.NoError(t, out.Close())
	}
	destinationDir := t.TempDir()
	destination, err := NewManager(destinationDir, logs.NewNodeLogger("permanent", 0))
	require.NoError(t, err)
	t.Cleanup(func() { destination.Close() })
	require.NoError(t, destination.InstallStateCheckpointDir(context.Background(), 0, 10, importDir, root, "certified-block-10"))
	for attempt := 0; attempt < 2; attempt++ {
		for key, value := range expected {
			got, err := destination.Get(key)
			require.NoError(t, err)
			require.Equal(t, value, got, key)
		}
		require.Equal(t, root, destination.GetStateFingerprint())
		mustNotExistPebbleKey(t, destination.archiveDB, keys.KeyArchiveFrostArtifact(id))
		if attempt == 0 {
			destination.Close()
			destination, err = NewManager(destinationDir, logs.NewNodeLogger("permanent-restart", 0))
			require.NoError(t, err)
		}
	}
}

func TestArtifactPruneKeepsRelatedLegacySourceWithoutPermanentCopy(t *testing.T) {
	mgr := newFrostArtifactTestManager(t, false)
	id := seedFrostArtifact(t, mgr, 1)
	raw, err := mgr.GetKV(keys.KeyArchiveFrostArtifact(id))
	require.NoError(t, err)
	_, err = mgr.SyncStateUpdates(1, toIfaceOps(syncTestOp{key: keys.KeyFrostPermanentArtifact(id), val: raw}))
	require.NoError(t, err)
	legacy := keys.KeyArchiveFrostMigration("migration-1")
	require.NoError(t, mgr.archiveDB.Set([]byte(legacy), []byte("only surviving authorization"), pebble.Sync))
	res, err := mgr.PruneFrostArtifactsWithDiskUsage(10, diskspace.Snapshot{TotalBytes: 100, FreeBytes: 50, FreePercent: 50})
	require.NoError(t, err)
	require.EqualValues(t, 1, res.DeletedRecords)
	require.Equal(t, "only surviving authorization", mustGetPebbleValue(t, mgr.archiveDB, legacy))
}
