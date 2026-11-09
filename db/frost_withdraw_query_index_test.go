package db

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"dex/keys"
	"dex/logs"
	"dex/pb"

	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func frostQuerySource(t *testing.T, id, status string, seq uint64) []interface{} {
	t.Helper()
	raw, err := proto.Marshal(&pb.FrostWithdrawState{WithdrawId: id, Chain: "btc", Asset: "BTC", VaultId: 2, RequestHeight: 7, Seq: seq, Status: status})
	require.NoError(t, err)
	return toIfaceOps(syncTestOp{key: keys.KeyFrostWithdraw(id), val: raw}, syncTestOp{key: keys.KeyFrostWithdrawFIFOIndex("btc", "BTC", seq), val: []byte(id)})
}

func frostActiveQueryKeys(id string, seq uint64) []string {
	return []string{
		keys.KeyFrostWithdrawQueryGlobalState(false, 7, seq, id),
		keys.KeyFrostWithdrawQueryChainState("btc", false, 7, seq, id),
		keys.KeyFrostWithdrawQueryChainVaultState("btc", 2, false, 7, seq, id),
	}
}

func TestFrostActiveQueryRebuildPreservesStateHistoryAndDrainsOldWrites(t *testing.T) {
	mgr := newQueryPruneTestManager(t)
	var updates []interface{}
	for i := 1; i <= 400; i++ {
		updates = append(updates, frostQuerySource(t, fmt.Sprintf("active-%03d", i), "SIGNING", uint64(i))...)
	}
	updates = append(updates, frostQuerySource(t, "cancelled", "CANCELLED", 401)...)
	_, err := mgr.SyncStateUpdates(7, updates)
	require.NoError(t, err)
	root := mgr.GetStateFingerprint()
	stale := keys.KeyFrostWithdrawQueryGlobalState(false, 1, 1, "gone")
	terminal := keys.KeyFrostWithdrawQueryGlobalState(true, 1, 1, "retained")
	mgr.EnqueueSet(stale, "gone")
	mgr.EnqueueSet(terminal, "retained")
	mgr.EnqueueDel(frostActiveQueryKeys("active-001", 1)[0])
	require.NoError(t, mgr.RebuildActiveFrostWithdrawQueryIndex())
	require.NoError(t, mgr.ForceFlush())
	rows, err := mgr.ScanKV(keys.KeyFrostWithdrawQueryPrefix())
	require.NoError(t, err)
	require.Len(t, rows, 400*3+1)
	require.NotContains(t, rows, stale)
	require.Equal(t, []byte("retained"), rows[terminal])
	for i := 1; i <= 400; i++ {
		id := fmt.Sprintf("active-%03d", i)
		for _, key := range frostActiveQueryKeys(id, uint64(i)) {
			require.Equal(t, []byte(id), rows[key])
		}
	}
	require.Equal(t, root, mgr.GetStateFingerprint())
	_, exists, err := mgr.stateDB.Get(frostActiveQueryKeys("active-001", 1)[0])
	require.NoError(t, err)
	require.False(t, exists)
}

func TestFrostActiveQueryStartupRebuildAndFailureBarrier(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(dir, logs.NewNodeLogger("withdraw-index", 0))
	require.NoError(t, err)
	_, err = mgr.SyncStateUpdates(7, frostQuerySource(t, "old-active", "QUEUED", 1))
	require.NoError(t, err)
	root := mgr.GetStateFingerprint()
	mgr.Close()
	mgr, err = NewManager(dir, logs.NewNodeLogger("withdraw-index", 0))
	require.NoError(t, err)
	defer mgr.Close()
	for _, key := range frostActiveQueryKeys("old-active", 1) {
		raw, err := mgr.Get(key)
		require.NoError(t, err)
		require.Equal(t, "old-active", string(raw))
	}
	require.Equal(t, root, mgr.GetStateFingerprint())
	_, err = mgr.SyncStateUpdates(8, toIfaceOps(syncTestOp{key: keys.KeyFrostWithdraw("old-active"), val: []byte{0xff}}))
	require.NoError(t, err)
	require.Error(t, mgr.RebuildActiveFrostWithdrawQueryIndex())
	prefix := keys.KeyFrostWithdrawQueryGlobalPrefix()
	_, err = mgr.ScanKV(prefix)
	require.ErrorContains(t, err, "active withdrawal query index unavailable")
	_, err = mgr.ScanKVWithLimitOrdered(prefix, 10, false)
	require.ErrorContains(t, err, "active withdrawal query index unavailable")
	_, err = mgr.ScanKVWithLimitOrderedAfter(prefix, "", 10, false)
	require.ErrorContains(t, err, "active withdrawal query index unavailable")
	_, err = mgr.ScanKeysWithLimitOrderedAfter(prefix, "", 10, false)
	require.ErrorContains(t, err, "active withdrawal query index unavailable")
	_, err = mgr.ScanKV(keys.KeyTransferHistory(""))
	require.NoError(t, err)
	_, err = mgr.SyncStateUpdates(9, frostQuerySource(t, "old-active", "QUEUED", 1))
	require.NoError(t, err)
	require.NoError(t, mgr.RebuildActiveFrostWithdrawQueryIndex())
	rows, err := mgr.ScanKV(prefix)
	require.NoError(t, err)
	require.Len(t, rows, 1)
}

func TestFrostActiveQueryReadWaitsForRebuild(t *testing.T) {
	mgr := newQueryPruneTestManager(t)
	mgr.frostWithdrawQueryIndexMu.Lock()
	locked := true
	defer func() {
		if locked {
			mgr.frostWithdrawQueryIndexMu.Unlock()
		}
	}()
	done := make(chan error, 1)
	started := make(chan struct{})
	go func() { close(started); _, err := mgr.ScanKV(keys.KeyFrostWithdrawQueryPrefix()); done <- err }()
	<-started
	select {
	case <-done:
		t.Fatal("query observed partial rebuild")
	case <-time.After(20 * time.Millisecond):
	}
	locked = false
	mgr.frostWithdrawQueryIndexMu.Unlock()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("query did not resume")
	}
}

func TestFrostActiveQueryCheckpointInstallAndRecovery(t *testing.T) {
	source := newQueryPruneTestManager(t)
	_, err := source.SyncStateUpdates(10, append(frostQuerySource(t, "from-checkpoint", "SIGNING", 1), frostQuerySource(t, "cancelled", "CANCELLED", 2)...))
	require.NoError(t, err)
	root := source.GetStateFingerprint()
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
	destination := newQueryPruneTestManager(t)
	_, err = destination.SyncStateUpdates(1, frostQuerySource(t, "gone", "QUEUED", 1))
	require.NoError(t, err)
	destination.EnqueueSet(frostActiveQueryKeys("gone", 1)[0], "gone")
	require.NoError(t, destination.InstallStateCheckpointDir(context.Background(), 1, 10, importDir, root, "certified-block-10"))
	for attempt := 0; attempt < 2; attempt++ {
		rows, err := destination.ScanKV(keys.KeyFrostWithdrawQueryPrefix())
		require.NoError(t, err)
		require.Len(t, rows, 3)
		for _, key := range frostActiveQueryKeys("from-checkpoint", 1) {
			require.Equal(t, []byte("from-checkpoint"), rows[key])
		}
		require.Equal(t, root, destination.GetStateFingerprint())
		require.NoError(t, destination.queryDB.Delete([]byte(frostActiveQueryKeys("from-checkpoint", 1)[0]), pebble.Sync))
		require.NoError(t, destination.CompleteStateCheckpointInstall(1, 10))
	}
}

func TestFrostActiveQuerySurvivesLegacyManifestAndTerminalRetentionStartsAtClose(t *testing.T) {
	mgr := newQueryPruneTestManager(t)
	active := keys.KeyFrostWithdrawQueryGlobalState(false, 1, 1, "pending")
	legacy := keys.KeyFrostWithdrawQueryGlobal(1, 1, "pending")
	terminal := keys.KeyFrostWithdrawQueryGlobalState(true, 1, 2, "closed")
	mgr.EnqueueSet(active, "pending")
	mgr.EnqueueSet(legacy, "pending")
	mgr.EnqueueSet(terminal, "closed")
	for height := uint64(1); height <= 5; height++ {
		var manifest []string
		if height == 1 {
			manifest = []string{legacy}
		}
		if height == 5 {
			manifest = []string{terminal}
		}
		mgr.EnqueueQueryPruneManifest(height, manifest, nil, nil)
	}
	require.NoError(t, mgr.ForceFlush())
	_, err := mgr.pruneQueryDataThroughHeight(100, 4, false)
	require.NoError(t, err)
	_, err = mgr.Get(legacy)
	require.ErrorIs(t, err, pebble.ErrNotFound)
	value, err := mgr.Get(active)
	require.NoError(t, err)
	require.Equal(t, "pending", string(value))
	_, err = mgr.Get(terminal)
	require.NoError(t, err, "terminal history must use closing block 5, not creation block 1")
	_, err = mgr.pruneQueryDataThroughHeight(100, 5, false)
	require.NoError(t, err)
	_, err = mgr.Get(terminal)
	require.ErrorIs(t, err, pebble.ErrNotFound)
	_, err = mgr.Get(active)
	require.NoError(t, err)
}
