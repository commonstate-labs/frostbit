package db

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"dex/interfaces"
	"dex/keys"
	"dex/logs"
	"dex/pb"

	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func rechargeQuerySource(t *testing.T, id string, status pb.RechargeRequestStatus) syncTestOp {
	t.Helper()
	raw, err := proto.Marshal(&pb.RechargeRequest{RequestId: id, NativeChain: "BTC", VaultId: 2, CreateHeight: 7, Status: status})
	require.NoError(t, err)
	return syncTestOp{key: keys.KeyRechargeRequest(id), val: raw}
}

func activeRechargeQueryKeys(id string) []string {
	return []string{
		keys.KeyRechargeQueryRecentGlobal(false, 7, id),
		keys.KeyRechargeQueryRecentChain("BTC", false, 7, id),
		keys.KeyRechargeQueryRecentVault(2, false, 7, id),
		keys.KeyRechargeQueryRecentChainVault("BTC", 2, false, 7, id),
	}
}

func TestActiveRechargeQueryRebuildDrainsColdWritesAndPreservesTerminalAndState(t *testing.T) {
	mgr := newQueryPruneTestManager(t)
	updates := make([]interface{}, 0, 307)
	for i := 0; i < 305; i++ {
		updates = append(updates, rechargeQuerySource(t, fmt.Sprintf("active-%03d", i), pb.RechargeRequestStatus_RECHARGE_ARBITRATION))
	}
	updates = append(updates,
		rechargeQuerySource(t, "finalized-pruned", pb.RechargeRequestStatus_RECHARGE_FINALIZED),
		rechargeQuerySource(t, "rejected-pruned", pb.RechargeRequestStatus_RECHARGE_REJECTED))
	_, err := mgr.SyncStateUpdates(7, updates)
	require.NoError(t, err)
	root := mgr.GetStateFingerprint()
	stale := keys.KeyRechargeQueryRecentGlobal(false, 1, "gone")
	                                                                           
	                                                     
	terminal := keys.KeyRechargeQueryRecentGlobal(true, 9, "retained|kind:active|height:1")
	mgr.EnqueueSet(stale, keys.KeyRechargeRequest("gone"))
	mgr.EnqueueSet(terminal, keys.KeyRechargeRequest("retained"))
	mgr.EnqueueDel(activeRechargeQueryKeys("active-000")[0])
	require.NoError(t, mgr.RebuildActiveRechargeQueryIndex())
	require.NoError(t, mgr.ForceFlush())
	rows, err := mgr.ScanKV(keys.KeyRechargeQueryRecentPrefix())
	require.NoError(t, err)
	require.Len(t, rows, 305*4+1)
	require.NotContains(t, rows, stale)
	require.Equal(t, []byte(keys.KeyRechargeRequest("retained")), rows[terminal])
	for i := 0; i < 305; i++ {
		id := fmt.Sprintf("active-%03d", i)
		for _, indexKey := range activeRechargeQueryKeys(id) {
			require.Equal(t, []byte(keys.KeyRechargeRequest(id)), rows[indexKey])
		}
	}
	require.Equal(t, root, mgr.GetStateFingerprint())
	_, exists, err := mgr.stateDB.Get(activeRechargeQueryKeys("active-000")[0])
	require.NoError(t, err)
	require.False(t, exists, "query indexes must remain outside consensus state")
}

func TestActiveRechargeQueryStartupRebuildsExistingMissingRequest(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(dir, logs.NewNodeLogger("recharge-index-restart", 0))
	require.NoError(t, err)
	_, err = mgr.SyncStateUpdates(7, toIfaceOps(rechargeQuerySource(t, "old-active", pb.RechargeRequestStatus_RECHARGE_ARBITRATION)))
	require.NoError(t, err)
	root := mgr.GetStateFingerprint()
	require.NoError(t, mgr.queryDB.Set([]byte(activeRechargeQueryKeys("gone")[0]), []byte("stale"), pebble.Sync))
	mgr.Close()
	mgr, err = NewManager(dir, logs.NewNodeLogger("recharge-index-restart", 0))
	require.NoError(t, err)
	defer mgr.Close()
	for _, key := range activeRechargeQueryKeys("old-active") {
		got, err := mgr.Get(key)
		require.NoError(t, err)
		require.Equal(t, []byte(keys.KeyRechargeRequest("old-active")), got)
	}
	rows, err := mgr.ScanKV(keys.KeyRechargeQueryRecentPrefix())
	require.NoError(t, err)
	require.Len(t, rows, 4)
	require.Equal(t, root, mgr.GetStateFingerprint())
}

func TestActiveRechargeQueryRebuildUsesCreationHeightBeforeTerminalStatus(t *testing.T) {
	mgr := newQueryPruneTestManager(t)
	raw, err := proto.Marshal(&pb.RechargeRequest{
		RequestId: "settlement-intermediate", NativeChain: "BTC", VaultId: 2,
		CreateHeight: 7, FinalizeHeight: 99, Status: pb.RechargeRequestStatus_RECHARGE_ARBITRATION,
	})
	require.NoError(t, err)
	_, err = mgr.SyncStateUpdates(99, toIfaceOps(syncTestOp{key: keys.KeyRechargeRequest("settlement-intermediate"), val: raw}))
	require.NoError(t, err)
	                                                                                             
	mgr.EnqueueSet(keys.KeyRechargeQueryRecentGlobal(false, 99, "settlement-intermediate"), keys.KeyRechargeRequest("settlement-intermediate"))
	root := mgr.GetStateFingerprint()
	require.NoError(t, mgr.RebuildActiveRechargeQueryIndex())
	rows, err := mgr.ScanKV(keys.KeyRechargeQueryRecentPrefix())
	require.NoError(t, err)
	require.Len(t, rows, 4)
	for _, key := range activeRechargeQueryKeys("settlement-intermediate") {
		require.Equal(t, []byte(keys.KeyRechargeRequest("settlement-intermediate")), rows[key])
	}
	require.Equal(t, root, mgr.GetStateFingerprint())
}

func TestActiveRechargeQueryFailedRebuildMakesAllListPathsUnavailableUntilRetry(t *testing.T) {
	mgr := newQueryPruneTestManager(t)
	_, err := mgr.SyncStateUpdates(7, toIfaceOps(syncTestOp{key: keys.KeyRechargeRequest("bad"), val: []byte{0xff}}))
	require.NoError(t, err)
	require.Error(t, mgr.RebuildActiveRechargeQueryIndex())
	prefix := keys.KeyRechargeQueryRecentGlobalPrefix(false)
	_, err = mgr.ScanKV(prefix)
	require.ErrorContains(t, err, "active recharge query index unavailable")
	_, err = mgr.ScanKVWithLimitOrdered(prefix, 10, false)
	require.ErrorContains(t, err, "active recharge query index unavailable")
	_, err = mgr.ScanKVWithLimitOrderedAfter(prefix, "", 10, false)
	require.ErrorContains(t, err, "active recharge query index unavailable")
	_, err = mgr.ScanKeysWithLimitOrderedAfter(prefix, "", 10, false)
	require.ErrorContains(t, err, "active recharge query index unavailable")
	_, err = mgr.ScanKV(keys.KeyTransferHistory(""))
	require.NoError(t, err, "unrelated queries must remain available")
	_, err = mgr.SyncStateUpdates(8, toIfaceOps(rechargeQuerySource(t, "bad", pb.RechargeRequestStatus_RECHARGE_ARBITRATION)))
	require.NoError(t, err)
	require.NoError(t, mgr.RebuildActiveRechargeQueryIndex())
	rows, err := mgr.ScanKVWithLimitOrdered(prefix, 10, false)
	require.NoError(t, err)
	require.Len(t, rows, 1)
}

func TestActiveRechargeQueryReadWaitsForRebuildBarrier(t *testing.T) {
	mgr := newQueryPruneTestManager(t)
	mgr.rechargeQueryIndexMu.Lock()
	locked := true
	defer func() {
		if locked {
			mgr.rechargeQueryIndexMu.Unlock()
		}
	}()
	done := make(chan []interfaces.KVPair, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		rows, err := mgr.ScanKVWithLimitOrdered(keys.KeyRechargeQueryRecentGlobalPrefix(false), 10, false)
		if err != nil {
			t.Error(err)
		}
		done <- rows
	}()
	<-started
	select {
	case <-done:
		t.Fatal("list returned during a partial reconstruction")
	case <-time.After(20 * time.Millisecond):
	}
	key := activeRechargeQueryKeys("published")[0]
	require.NoError(t, mgr.queryDB.Set([]byte(key), []byte(keys.KeyRechargeRequest("published")), pebble.Sync))
	locked = false
	mgr.rechargeQueryIndexMu.Unlock()
	select {
	case rows := <-done:
		require.Len(t, rows, 1)
		require.Equal(t, key, rows[0].Key)
	case <-time.After(time.Second):
		t.Fatal("list did not resume after reconstruction")
	}
}

func TestActiveRechargeQueryCheckpointInstallAndRecoveryRebuildOnlyActiveRows(t *testing.T) {
	source := newQueryPruneTestManager(t)
	_, err := source.SyncStateUpdates(10, toIfaceOps(
		assignmentSource(t, &pb.RechargeRequest{RequestId: "from-checkpoint", NativeChain: "BTC", VaultId: 2, CreateHeight: 7,
			Status: pb.RechargeRequestStatus_RECHARGE_VOTING, SelectedWitnesses: []string{"snapshot-witness"}}),
		rechargeQuerySource(t, "terminal-not-retained", pb.RechargeRequestStatus_RECHARGE_FINALIZED)))
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
	_, err = destination.SyncStateUpdates(1, toIfaceOps(rechargeQuerySource(t, "gone", pb.RechargeRequestStatus_RECHARGE_VOTING)))
	require.NoError(t, err)
	destination.EnqueueSet(activeRechargeQueryKeys("gone")[0], keys.KeyRechargeRequest("gone"))
	require.NoError(t, destination.InstallStateCheckpointDir(context.Background(), 1, 10, importDir, root, "certified-block-10"))
	for attempt := 0; attempt < 2; attempt++ {
		rows, err := destination.ScanKV(keys.KeyRechargeQueryRecentPrefix())
		require.NoError(t, err)
		require.Len(t, rows, 4)
		for _, key := range activeRechargeQueryKeys("from-checkpoint") {
			require.Equal(t, []byte(keys.KeyRechargeRequest("from-checkpoint")), rows[key])
		}
		require.Equal(t, root, destination.GetStateFingerprint())
		for _, indexKey := range []string{keys.KeyWitnessAssigned("snapshot-witness", "from-checkpoint"), keys.KeyWitnessAssignedCurrent("snapshot-witness", "from-checkpoint")} {
			raw, err := destination.Get(indexKey)
			require.NoError(t, err)
			require.Equal(t, []byte("from-checkpoint"), raw)
			require.NoError(t, destination.queryDB.Delete([]byte(indexKey), pebble.Sync))
		}
		                                                                      
		require.NoError(t, destination.queryDB.Delete([]byte(activeRechargeQueryKeys("from-checkpoint")[0]), pebble.Sync))
		require.NoError(t, destination.CompleteStateCheckpointInstall(1, 10))
	}
}
