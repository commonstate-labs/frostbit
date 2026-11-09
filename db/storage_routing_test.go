package db

import (
	"dex/keys"
	"dex/logs"
	"errors"
	"testing"
	"time"

	"github.com/cockroachdb/pebble"
)

func mustGetPebbleValue(t *testing.T, db *pebble.DB, key string) string {
	t.Helper()
	raw, closer, err := db.Get([]byte(key))
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}
	defer closer.Close()
	return string(raw)
}

func mustNotExistPebbleKey(t *testing.T, db *pebble.DB, key string) {
	t.Helper()
	_, closer, err := db.Get([]byte(key))
	if err == nil {
		_ = closer.Close()
		t.Fatalf("key %s should not exist", key)
	}
	if !errors.Is(err, pebble.ErrNotFound) {
		t.Fatalf("get %s: %v", key, err)
	}
}

func TestKVStorageRoutesToPhysicalStores(t *testing.T) {
	mgr, err := NewManager(t.TempDir(), logs.NewNodeLogger("test", 0))
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	mgr.InitWriteQueue(100, time.Hour)
	t.Cleanup(func() { mgr.Close() })

	runtimeKey := keys.KeyPendingAnyTx("tx-runtime")
	queryKey := keys.KeyFutureTradeRecord("BTC_USDT", 1, "trade-query")
	archiveKey := keys.KeyArchiveRechargeRequest("req-archive")

	mgr.EnqueueSet(runtimeKey, "runtime")
	mgr.EnqueueSet(queryKey, "query")
	mgr.EnqueueSet(archiveKey, "archive")
	if err := mgr.ForceFlush(); err != nil {
		t.Fatalf("force flush: %v", err)
	}

	if got := mustGetPebbleValue(t, mgr.runtimeDB, runtimeKey); got != "runtime" {
		t.Fatalf("runtime value = %q", got)
	}
	if got := mustGetPebbleValue(t, mgr.queryDB, queryKey); got != "query" {
		t.Fatalf("query value = %q", got)
	}
	if got := mustGetPebbleValue(t, mgr.archiveDB, archiveKey); got != "archive" {
		t.Fatalf("archive value = %q", got)
	}
	mustNotExistPebbleKey(t, mgr.runtimeDB, queryKey)
	mustNotExistPebbleKey(t, mgr.runtimeDB, archiveKey)
	if keys.StorageClassForKey("v1_frost_planning_log_withdraw_reporter") != keys.StorageQuery {
		t.Fatalf("frost planning logs should route to query store")
	}
	if keys.StorageClassForKey(keys.KeyBlockPruneCursor()) != keys.StorageRuntime {
		t.Fatalf("block prune cursor should route to runtime store")
	}
	if keys.StorageClassForKey(keys.KeyQueryPruneHeightCursor()) != keys.StorageRuntime {
		t.Fatalf("query prune height cursor should route to runtime store")
	}
	if keys.StorageClassForKey(keys.KeyQueryPruneSyncGap(1)) != keys.StorageRuntime {
		t.Fatalf("query prune sync gap should route to runtime store")
	}
	if keys.StorageClassForKey(keys.KeyQueryPruneManifestChunk(1, 0)) != keys.StorageQuery {
		t.Fatalf("query prune manifest should route to query store")
	}
	if keys.StorageClassForKey(keys.KeyArchiveFrostSignedPackage("job", 0)) != keys.StorageArchive {
		t.Fatalf("archive frost signed packages should route to archive store")
	}
	if keys.StorageClassForKey(keys.KeyArchiveFrostWithdrawDone("BTC", "native", 1, "wid")) != keys.StorageArchive {
		t.Fatalf("archive frost withdraw done index should route to archive store")
	}
}

func TestForceFlushHotDoesNotFlushColdQueues(t *testing.T) {
	mgr, err := NewManager(t.TempDir(), logs.NewNodeLogger("test", 0))
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	mgr.InitWriteQueue(100, time.Hour)
	t.Cleanup(func() { mgr.Close() })

	runtimeKey := keys.KeyVMAppliedTx("tx-hot")
	queryKey := keys.KeyUserActivity("addr", 1, 0)
	archiveKey := keys.KeyBlockData("block-cold")
	mgr.EnqueueSet(runtimeKey, "hot")
	mgr.EnqueueSet(queryKey, "query")
	mgr.EnqueueSet(archiveKey, "archive")

	if err := mgr.ForceFlushHot(); err != nil {
		t.Fatalf("force hot flush: %v", err)
	}
	if got := mustGetPebbleValue(t, mgr.runtimeDB, runtimeKey); got != "hot" {
		t.Fatalf("hot value = %q", got)
	}
	mustNotExistPebbleKey(t, mgr.queryDB, queryKey)
	mustNotExistPebbleKey(t, mgr.archiveDB, archiveKey)

	if err := mgr.ForceFlush(); err != nil {
		t.Fatalf("force full flush: %v", err)
	}
	if got := mustGetPebbleValue(t, mgr.queryDB, queryKey); got != "query" {
		t.Fatalf("query value = %q", got)
	}
	if got := mustGetPebbleValue(t, mgr.archiveDB, archiveKey); got != "archive" {
		t.Fatalf("archive value = %q", got)
	}
}

func TestReadOnlyManagerReadsSplitStoresAndStateDB(t *testing.T) {
	root := t.TempDir()
	mgr, err := NewManager(root, logs.NewNodeLogger("test", 0))
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	mgr.InitWriteQueue(100, time.Hour)

	stateKey := keys.KeyAccount("readonly_state")
	queryKey := keys.KeyFutureTradeRecord("BTC_USDT", 1, "readonly_query")
	archiveKey := keys.KeyTxRaw("readonly_archive")
	mgr.EnqueueSet(stateKey, "state")
	mgr.EnqueueSet(queryKey, "query")
	mgr.EnqueueSet(archiveKey, "archive")
	if err := mgr.ForceFlush(); err != nil {
		t.Fatalf("force flush: %v", err)
	}
	mgr.Close()

	ro, err := NewReadOnlyManager(root, logs.NewNodeLogger("readonly", 0))
	if err != nil {
		t.Fatalf("new readonly manager: %v", err)
	}
	t.Cleanup(func() { ro.Close() })

	if got, err := ro.Get(stateKey); err != nil || string(got) != "state" {
		t.Fatalf("readonly state = %q err=%v", string(got), err)
	}
	if got, err := ro.GetKV(queryKey); err != nil || string(got) != "query" {
		t.Fatalf("readonly query = %q err=%v", string(got), err)
	}
	if got, err := ro.GetKV(archiveKey); err != nil || string(got) != "archive" {
		t.Fatalf("readonly archive = %q err=%v", string(got), err)
	}
}
