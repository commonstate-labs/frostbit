package db

import (
	"dex/config"
	"dex/keys"
	"dex/logs"
	statedb "dex/stateDB"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type syncTestOp struct {
	key       string
	val       []byte
	del       bool
	oldKnown  bool
	oldExists bool
	oldVal    []byte
}

func (o syncTestOp) GetKey() string       { return o.key }
func (o syncTestOp) GetValue() []byte     { return o.val }
func (o syncTestOp) IsDel() bool          { return o.del }
func (o syncTestOp) OldValueKnown() bool  { return o.oldKnown }
func (o syncTestOp) OldValueExists() bool { return o.oldExists }
func (o syncTestOp) GetOldValue() []byte  { return o.oldVal }

func toIfaceOps(ops ...syncTestOp) []interface{} {
	out := make([]interface{}, 0, len(ops))
	for i := range ops {
		out = append(out, ops[i])
	}
	return out
}

func TestBuildStateSyncKVsParallelMatchesSerial(t *testing.T) {
	oldProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(oldProcs)

	updates := make([]interface{}, 0, stateSyncParallelBuildMinUpdates+64)
	for i := 0; i < stateSyncParallelBuildMinUpdates+64; i++ {
		switch i % 5 {
		case 0:
			updates = append(updates, syncTestOp{key: keys.KeyAccount(fmt.Sprintf("sync_%05d", i)), val: []byte(fmt.Sprintf("account-%05d", i))})
		case 1:
			updates = append(updates, syncTestOp{key: keys.KeyBalance(fmt.Sprintf("sync_%05d", i), "FB"), val: []byte(fmt.Sprintf("balance-%05d", i)), oldKnown: true, oldExists: true, oldVal: []byte("old")})
		case 2:
			updates = append(updates, syncTestOp{key: keys.KeyRentState(fmt.Sprintf("sync_%05d", i)), del: true, oldKnown: true, oldExists: false})
		case 3:
			updates = append(updates, syncTestOp{key: keys.KeyTxRaw(fmt.Sprintf("tx_%05d", i)), val: []byte("kv-only")})
		default:
			updates = append(updates, syncTestOp{})
		}
	}

	serial, serialWorkers, err := buildStateSyncKVs(updates)
	require.NoError(t, err)
	require.Equal(t, 1, serialWorkers)
	runtime.GOMAXPROCS(4)
	parallel, parallelWorkers, err := buildStateSyncKVs(updates)
	require.NoError(t, err)
	require.Greater(t, parallelWorkers, 1)
	require.Equal(t, serial, parallel)
}

func requireManagerStateRootMatchesStore(t *testing.T, mgr *Manager) {
	t.Helper()
	live := mgr.GetStateFingerprint()
	fps, err := mgr.stateDB.ComputeShardFingerprints(0)
	require.NoError(t, err)
	var acc statedb.StateRootAccumulator
	for _, fp := range fps {
		acc.KeyCount += fp.KeyCount
		acc.TotalKeyBytes += fp.TotalKeyBytes
		acc.TotalValueBytes += fp.TotalValueBytes
		for i := range acc.XorDigest {
			acc.XorDigest[i] ^= fp.XorDigest[i]
		}
	}
	require.Equal(t, acc.RootBytes(), live)
}

func TestSyncStateUpdatesMirrorsStatefulKeys(t *testing.T) {
	root := t.TempDir()
	cfg := config.DefaultConfig()
	mgr, err := NewManagerWithConfig(root, logs.NewNodeLogger("test", 0), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Close() })
	mgr.InitWriteQueue(128, 10*time.Millisecond)

	accountKey := keys.KeyAccount("alice_sync")
	frostKey := keys.KeyFrostVaultState("BTC", 1)
	kvOnlyKey := keys.KeyTxRaw("tx_sync_1")

	accountVal := []byte("acc-v1")
	frostVal := []byte("frost-v1")
	kvOnlyVal := []byte("txraw-v1")

	mgr.EnqueueSet(accountKey, string(accountVal))
	mgr.EnqueueSet(frostKey, string(frostVal))
	mgr.EnqueueSet(kvOnlyKey, string(kvOnlyVal))

	n, err := mgr.SyncStateUpdates(1, toIfaceOps(
		syncTestOp{key: accountKey, val: accountVal},
		syncTestOp{key: frostKey, val: frostVal},
		syncTestOp{key: kvOnlyKey, val: kvOnlyVal},
	))
	require.NoError(t, err)
	require.Equal(t, 2, n, "only stateful keys should be mirrored into stateDB")
	require.NoError(t, mgr.ForceFlush())

	_, err = mgr.GetKV(accountKey)
	require.Error(t, err, "stateful key should not be written into KV main DB")

	getAccount, err := mgr.Get(accountKey)
	require.NoError(t, err)
	sdbAccount, exists, err := mgr.stateDB.Get(accountKey)
	require.NoError(t, err)
	require.True(t, exists)
	require.Equal(t, getAccount, sdbAccount)

	_, err = mgr.GetKV(frostKey)
	require.Error(t, err, "stateful key should not be written into KV main DB")

	getFrost, err := mgr.Get(frostKey)
	require.NoError(t, err)
	sdbFrost, exists, err := mgr.stateDB.Get(frostKey)
	require.NoError(t, err)
	require.True(t, exists)
	require.Equal(t, getFrost, sdbFrost)

	kvOnlyRead, err := mgr.GetKV(kvOnlyKey)
	require.NoError(t, err)
	require.Equal(t, kvOnlyVal, kvOnlyRead)

	_, exists, err = mgr.stateDB.Get(kvOnlyKey)
	require.NoError(t, err)
	require.False(t, exists, "kv-only key should not appear in stateDB")
}

func TestSyncStateUpdatesMirrorsDeletes(t *testing.T) {
	root := t.TempDir()
	cfg := config.DefaultConfig()
	mgr, err := NewManagerWithConfig(root, logs.NewNodeLogger("test", 0), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Close() })
	mgr.InitWriteQueue(128, 10*time.Millisecond)

	accountKey := keys.KeyAccount("alice_del_sync")
	accountVal := []byte("acc-v1")

	mgr.EnqueueSet(accountKey, string(accountVal))
	_, err = mgr.SyncStateUpdates(1, toIfaceOps(syncTestOp{key: accountKey, val: accountVal}))
	require.NoError(t, err)
	require.NoError(t, mgr.ForceFlush())

	mgr.EnqueueDelete(accountKey)
	n, err := mgr.SyncStateUpdates(2, toIfaceOps(syncTestOp{key: accountKey, del: true}))
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.NoError(t, mgr.ForceFlush())

	_, err = mgr.GetKV(accountKey)
	require.Error(t, err, "key should be removed from KV")

	_, exists, err := mgr.stateDB.Get(accountKey)
	require.NoError(t, err)
	require.False(t, exists, "key should be removed from stateDB")
}

func TestSyncStateUpdatesPreservesKnownEmptyOldValue(t *testing.T) {
	root := t.TempDir()
	cfg := config.DefaultConfig()
	mgr, err := NewManagerWithConfig(root, logs.NewNodeLogger("test", 0), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Close() })
	mgr.InitWriteQueue(128, 10*time.Millisecond)

	key := keys.KeyOrderState("empty-old")
	empty := make([]byte, 0)
	_, err = mgr.SyncStateUpdates(1, toIfaceOps(syncTestOp{key: key, val: empty}))
	require.NoError(t, err)

	_, err = mgr.SyncStateUpdates(2, toIfaceOps(syncTestOp{
		key:       key,
		val:       []byte("new"),
		oldKnown:  true,
		oldExists: true,
		oldVal:    empty,
	}))
	require.NoError(t, err)
	requireManagerStateRootMatchesStore(t, mgr)
}

func TestSyncStateUpdatesRejectsUnknownWriteKey(t *testing.T) {
	root := t.TempDir()
	cfg := config.DefaultConfig()
	mgr, err := NewManagerWithConfig(root, logs.NewNodeLogger("test", 0), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Close() })
	mgr.InitWriteQueue(128, 10*time.Millisecond)

	_, err = mgr.SyncStateUpdates(1, toIfaceOps(syncTestOp{
		key: "v1_unknown_consensus_state_",
		val: []byte("x"),
	}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "unregistered VM write key prefix")
}

func TestStateDBConfigIsAppliedFromMainConfig(t *testing.T) {
	root := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Database.StateDB.CheckpointEvery = 2
	cfg.Database.StateDB.DataDir = "custom_state"
	cfg.Database.StateDB.ShardHexWidth = 2
	cfg.Database.StateDB.PageSize = 128
	cfg.Database.StateDB.CheckpointKeep = 4
	cfg.Database.BlockCacheSizeDB = 8 << 20

	mgr, err := NewManagerWithConfig(root, logs.NewNodeLogger("test", 0), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Close() })
	mgr.InitWriteQueue(128, 10*time.Millisecond)

	sdbCfg := mgr.stateDB.Config()
	require.Equal(t, uint64(2), sdbCfg.CheckpointEvery)
	require.Equal(t, 2, sdbCfg.ShardHexWidth)
	require.Equal(t, 128, sdbCfg.PageSize)
	require.Equal(t, 4, sdbCfg.CheckpointKeep)
	require.Equal(t, int64(8<<20), sdbCfg.BlockCacheSize)

	accountKey := keys.KeyAccount("cfg_sync")
	update := syncTestOp{key: accountKey, val: []byte("cfg-v")}

	_, err = mgr.SyncStateUpdates(2, toIfaceOps(update))
	require.NoError(t, err)
	_, err = mgr.SyncStateUpdates(4, toIfaceOps(update))
	require.NoError(t, err)

	                                                                         
	                                                
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		latest, err := mgr.stateDB.LatestCheckpointHeight()
		if err == nil && latest >= 4 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	cpRoot := filepath.Join(root, "custom_state", "checkpoints")
	cp4 := filepath.Join(cpRoot, "h_00000000000000000004")
	cp2 := filepath.Join(cpRoot, "h_00000000000000000002")

	_, err = os.Stat(cp4)
	require.NoError(t, err, "latest checkpoint should exist")
	_, err = os.Stat(cp2)
	require.NoError(t, err, "checkpoint at earlier epoch boundary should exist")
}

func TestSyncFinalizedStateUpdatesForceCheckpointWithoutUpdates(t *testing.T) {
	root := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Database.StateDB.CheckpointEvery = 1000
	cfg.Database.StateDB.CheckpointKeep = 4

	mgr, err := NewManagerWithConfig(root, logs.NewNodeLogger("test", 0), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Close() })
	mgr.InitWriteQueue(128, 10*time.Millisecond)

	n, err := mgr.SyncFinalizedStateUpdates(3, nil, true)
	require.NoError(t, err)
	require.Equal(t, 0, n)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		latest, err := mgr.stateDB.LatestCheckpointHeight()
		if err == nil && latest >= 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cpPath := filepath.Join(root, cfg.Database.StateDB.DataDir, "checkpoints", "h_00000000000000000003")
	_, err = os.Stat(cpPath)
	require.NoError(t, err, "forced finalized checkpoint should exist even without state updates")
}

func TestGetKVsBatchesStateAndKV(t *testing.T) {
	root := t.TempDir()
	cfg := config.DefaultConfig()
	mgr, err := NewManagerWithConfig(root, logs.NewNodeLogger("test", 0), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Close() })
	mgr.InitWriteQueue(128, 10*time.Millisecond)

	stateKey := keys.KeyAccount("batch_reader")
	stateVal := []byte("state-v")
	kvKey := keys.KeyTxRaw("batch_reader_tx")
	kvVal := []byte("kv-v")

	mgr.EnqueueSet(stateKey, string(stateVal))
	mgr.EnqueueSet(kvKey, string(kvVal))

	_, err = mgr.SyncStateUpdates(1, toIfaceOps(syncTestOp{key: stateKey, val: stateVal}))
	require.NoError(t, err)
	require.NoError(t, mgr.ForceFlush())

	got, err := mgr.GetKVs([]string{stateKey, kvKey, "v1_account_missing_batch"})
	require.NoError(t, err)
	require.Equal(t, stateVal, got[stateKey])
	require.Equal(t, kvVal, got[kvKey])
	_, exists := got["v1_account_missing_batch"]
	require.False(t, exists)
}

func TestScanKVWithLimitOrderedStateful(t *testing.T) {
	root := t.TempDir()
	cfg := config.DefaultConfig()
	mgr, err := NewManagerWithConfig(root, logs.NewNodeLogger("test", 0), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Close() })
	mgr.InitWriteQueue(128, 10*time.Millisecond)

	var ops []syncTestOp
	var wantAsc []string
	for i := 9; i >= 0; i-- {
		key := keys.KeyAccount(fmt.Sprintf("ordered_%02d", i))
		ops = append(ops, syncTestOp{key: key, val: []byte(fmt.Sprintf("v-%02d", i))})
		wantAsc = append(wantAsc, key)
	}
	sort.Strings(wantAsc)
	_, err = mgr.SyncStateUpdates(1, toIfaceOps(ops...))
	require.NoError(t, err)

	gotAsc, err := mgr.ScanKVWithLimitOrdered(keys.KeyAccount(""), 3, false)
	require.NoError(t, err)
	require.Len(t, gotAsc, 3)
	require.Equal(t, wantAsc[:3], []string{gotAsc[0].Key, gotAsc[1].Key, gotAsc[2].Key})

	gotDesc, err := mgr.ScanKVWithLimitOrdered(keys.KeyAccount(""), 3, true)
	require.NoError(t, err)
	require.Len(t, gotDesc, 3)
	require.Equal(t, []string{wantAsc[9], wantAsc[8], wantAsc[7]}, []string{gotDesc[0].Key, gotDesc[1].Key, gotDesc[2].Key})
}
