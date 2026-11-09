package statedb

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func testPebbleConfig(dir string) Config {
	return Config{
		Backend:         BackendPebble,
		DataDir:         dir,
		CheckpointEvery: 10,
		ShardHexWidth:   1,
		PageSize:        2,
		CheckpointKeep:  2,
	}
}

func testAggregateRoot(t *testing.T, fps []ShardFingerprint) []byte {
	t.Helper()
	var acc StateRootAccumulator
	for _, fp := range fps {
		acc.KeyCount += fp.KeyCount
		acc.TotalKeyBytes += fp.TotalKeyBytes
		acc.TotalValueBytes += fp.TotalValueBytes
		xorStateDigest(&acc.XorDigest, fp.XorDigest)
	}
	return acc.RootBytes()
}

func requireLiveMatchesStore(t *testing.T, db *DB) {
	t.Helper()
	live := db.GetLiveFingerprintBytes()
	fps, err := db.ComputeShardFingerprints(0)
	if err != nil {
		t.Fatalf("compute live store fingerprints failed: %v", err)
	}
	storeRoot := testAggregateRoot(t, fps)
	if !bytes.Equal(live, storeRoot) {
		t.Fatalf("live fingerprint drift: live=%x store=%x", live[:8], storeRoot[:8])
	}
}

func TestDefaultCheckpointEvery(t *testing.T) {
	cfg := testPebbleConfig(t.TempDir())
	cfg.CheckpointEvery = 0
	db, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := db.checkpointEvery(); got != stateDBDefaultCheckpointEvery {
		t.Fatalf("checkpoint every = %d, want %d", got, stateDBDefaultCheckpointEvery)
	}
}

func requireStoreKeyMissing(t *testing.T, db *DB, key string) {
	t.Helper()
	if _, err := db.store.Get([]byte(key)); !db.store.IsNotFound(err) {
		t.Fatalf("store key %q exists or returned unexpected error: %v", key, err)
	}
}

func TestApplyDoesNotWriteLegacySequenceMetadata(t *testing.T) {
	db, err := New(testPebbleConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.ApplyAccountUpdate(1, KVUpdate{Key: "v1_account_no_legacy_meta", Value: []byte("ok")}); err != nil {
		t.Fatal(err)
	}
	requireStoreKeyMissing(t, db, "v1_sdb_meta_h2seq_00000000000000000001")
	requireStoreKeyMissing(t, db, "v1_sdb_meta_seq2h_00000000000000000001")
	requireStoreKeyMissing(t, db, "v1_sdb_meta_commit_seq")
	if _, err := db.store.Get(kMetaLastAppliedHeight()); err != nil {
		t.Fatalf("last applied height must remain persisted: %v", err)
	}
}

func TestCanonicalStateUpdatesParallelMatchesSerialAndApplyRoot(t *testing.T) {
	oldProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(oldProcs)

	totalUpdates := stateDBApplyCanonicalMinUpdates*4 + 64
	updates := make([]KVUpdate, 0, totalUpdates)
	for i := 0; i < totalUpdates; i++ {
		switch i % 6 {
		case 0:
			updates = append(updates, KVUpdate{Key: fmt.Sprintf("v1_account_parallel_%05d", i), Value: []byte(fmt.Sprintf("account-%05d", i))})
		case 1:
			updates = append(updates, KVUpdate{Key: fmt.Sprintf("v1_balance_parallel_%05d_FB", i), Value: []byte(fmt.Sprintf("balance-%05d", i)), HasOldValue: true, OldExists: true, OldValue: []byte("old")})
		case 2:
			updates = append(updates, KVUpdate{Key: fmt.Sprintf("v1_rent_state_parallel_%05d", i), Deleted: true, HasOldValue: true, OldExists: false, OldValueAuthoritative: true})
		case 3:
			updates = append(updates, KVUpdate{Key: fmt.Sprintf("v1_txraw_parallel_%05d", i), Value: []byte("kv-only")})
		case 4:
			updates = append(updates, KVUpdate{Key: fmt.Sprintf("v1_account_parallel_dup_%03d", i%32), Value: []byte(fmt.Sprintf("dup-new-%05d", i)), HasOldValue: true, OldExists: true, OldValue: []byte("dup-old")})
		default:
			updates = append(updates, KVUpdate{Key: fmt.Sprintf("v1_account_parallel_dup_%03d", i%32), Value: []byte(fmt.Sprintf("dup-final-%05d", i))})
		}
	}

	serial, serialCount, serialWorkers, err := canonicalStateUpdates(updates)
	if err != nil {
		t.Fatal(err)
	}
	if serialWorkers != 1 || serialCount != len(serial) {
		t.Fatalf("serial canonical workers=%d count=%d len=%d", serialWorkers, serialCount, len(serial))
	}
	serialPrepared, serialPrepWorkers := prepareStateApplyItems(serial, 1)
	if serialPrepWorkers != 1 {
		t.Fatalf("serial prep workers=%d, want 1", serialPrepWorkers)
	}

	runtime.GOMAXPROCS(4)
	parallel, parallelCount, parallelWorkers, err := canonicalStateUpdates(updates)
	if err != nil {
		t.Fatal(err)
	}
	if parallelWorkers <= 1 || parallelCount != serialCount || !reflect.DeepEqual(serial, parallel) {
		t.Fatalf("parallel canonical mismatch: workers=%d serialCount=%d parallelCount=%d", parallelWorkers, serialCount, parallelCount)
	}
	parallelPrepared, parallelPrepWorkers := prepareStateApplyItems(parallel, 1)
	if parallelPrepWorkers <= 1 || !reflect.DeepEqual(serialPrepared, parallelPrepared) {
		t.Fatalf("parallel apply prep mismatch: workers=%d serial=%d parallel=%d", parallelPrepWorkers, len(serialPrepared), len(parallelPrepared))
	}

	applyUpdates := make([]KVUpdate, 0, totalUpdates)
	for i := 0; i < totalUpdates; i++ {
		if i%4 == 0 {
			applyUpdates = append(applyUpdates, KVUpdate{Key: fmt.Sprintf("v1_txraw_apply_parallel_%05d", i), Value: []byte("kv-only")})
			continue
		}
		applyUpdates = append(applyUpdates, KVUpdate{Key: fmt.Sprintf("v1_account_apply_parallel_%05d", i), Value: []byte(fmt.Sprintf("value-%05d", i))})
	}
	cfg := testPebbleConfig(t.TempDir())
	db, err := New(cfg)
	if err != nil {
		t.Fatalf("new db failed: %v", err)
	}
	defer db.Close()
	if err := db.ApplyAccountUpdate(1, applyUpdates...); err != nil {
		t.Fatalf("apply updates failed: %v", err)
	}
	requireLiveMatchesStore(t, db)
}

func TestPebbleDefaultBackend(t *testing.T) {
	cfg := testPebbleConfig(t.TempDir())
	cfg.Backend = ""

	db, err := New(cfg)
	if err != nil {
		t.Fatalf("new db failed: %v", err)
	}
	defer db.Close()

	if db.conf.Backend != BackendPebble {
		t.Fatalf("expected default backend %q, got %q", BackendPebble, db.conf.Backend)
	}

	if _, err := os.Stat(filepath.Join(cfg.DataDir, "live")); err != nil {
		t.Fatalf("expected live dir to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.DataDir, "checkpoints")); err != nil {
		t.Fatalf("expected checkpoints dir to exist: %v", err)
	}
}

func TestApplyAccountUpdateOldMissingFallbackPreventsFingerprintDrift(t *testing.T) {
	cfg := testPebbleConfig(t.TempDir())
	cfg.CheckpointEvery = 1000
	db, err := New(cfg)
	if err != nil {
		t.Fatalf("new db failed: %v", err)
	}
	defer db.Close()

	key := "v1_orderstate_old_missing_guard"
	if err := db.ApplyAccountUpdate(1, KVUpdate{Key: key, Value: []byte("open")}); err != nil {
		t.Fatalf("seed key failed: %v", err)
	}

	if err := db.ApplyAccountUpdate(2, KVUpdate{
		Key:         key,
		Deleted:     true,
		HasOldValue: true,
		OldExists:   false,
	}); err != nil {
		t.Fatalf("delete with stale missing old value failed: %v", err)
	}
	requireLiveMatchesStore(t, db)
}

func TestApplyAccountUpdateCoalescesDuplicateOldValues(t *testing.T) {
	cfg := testPebbleConfig(t.TempDir())
	cfg.CheckpointEvery = 1000
	db, err := New(cfg)
	if err != nil {
		t.Fatalf("new db failed: %v", err)
	}
	defer db.Close()

	key := "v1_orderstate_duplicate_guard"
	if err := db.ApplyAccountUpdate(1, KVUpdate{Key: key, Value: []byte("base")}); err != nil {
		t.Fatalf("seed key failed: %v", err)
	}
	if err := db.ApplyAccountUpdate(2,
		KVUpdate{Key: key, Value: []byte("mid"), HasOldValue: true, OldExists: true, OldValue: []byte("base")},
		KVUpdate{Key: key, Value: []byte("final"), HasOldValue: true, OldExists: true, OldValue: []byte("base")},
	); err != nil {
		t.Fatalf("duplicate update failed: %v", err)
	}
	requireLiveMatchesStore(t, db)
	got, exists, err := db.Get(key)
	if err != nil || !exists || string(got) != "final" {
		t.Fatalf("final value mismatch exists=%v val=%q err=%v", exists, got, err)
	}
}

func TestPebbleApplyAndGetWriteThrough(t *testing.T) {
	cfg := testPebbleConfig(t.TempDir())

	db, err := New(cfg)
	if err != nil {
		t.Fatalf("new db failed: %v", err)
	}
	defer db.Close()

	key := "v1_account_addr1"
	if err := db.ApplyAccountUpdate(1, KVUpdate{Key: key, Value: []byte("balance:1000")}); err != nil {
		t.Fatalf("apply update failed: %v", err)
	}

	got, exists, err := db.Get(key)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if !exists {
		t.Fatal("expected key to exist")
	}
	if string(got) != "balance:1000" {
		t.Fatalf("unexpected value: %s", string(got))
	}

	                                                         
	if err := db.ApplyAccountUpdate(2, KVUpdate{Key: "v1_txraw_x", Value: []byte("raw")}); err != nil {
		t.Fatalf("apply non-state update failed: %v", err)
	}
	_, exists, err = db.Get("v1_txraw_x")
	if err != nil {
		t.Fatalf("get non-state key failed: %v", err)
	}
	if exists {
		t.Fatal("non-state key should not be stored in statedb")
	}
}

func TestApplyAccountUpdateRejectsUnknownKey(t *testing.T) {
	cfg := testPebbleConfig(t.TempDir())

	db, err := New(cfg)
	if err != nil {
		t.Fatalf("new db failed: %v", err)
	}
	defer db.Close()

	err = db.ApplyAccountUpdate(1, KVUpdate{Key: "v1_unknown_consensus_state_", Value: []byte("x")})
	if err == nil {
		t.Fatal("expected unknown StateDB key prefix to be rejected")
	}
}

func TestCheckpointCreatedOnEpochBoundary(t *testing.T) {
	cfg := testPebbleConfig(t.TempDir())
	cfg.CheckpointKeep = 5

	db, err := New(cfg)
	if err != nil {
		t.Fatalf("new db failed: %v", err)
	}
	defer db.Close()

	key := "v1_account_addr_cp"
	if err := db.ApplyAccountUpdate(10, KVUpdate{Key: key, Value: []byte("v10")}); err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	waitForCheckpointAtLeast(t, db, 10)

	cpPath := filepath.Join(cfg.DataDir, "checkpoints", "h_00000000000000000010")
	if _, err := os.Stat(cpPath); err != nil {
		t.Fatalf("expected checkpoint at epoch boundary: %v", err)
	}

	latest, err := db.LatestCheckpointHeight()
	if err != nil {
		t.Fatalf("latest checkpoint read failed: %v", err)
	}
	if latest != 10 {
		t.Fatalf("expected latest checkpoint 10, got %d", latest)
	}
}

func TestCheckpointManifestInvalidExistingCheckpointIsRecreated(t *testing.T) {
	cfg := testPebbleConfig(t.TempDir())
	cfg.CheckpointKeep = 5

	db, err := New(cfg)
	if err != nil {
		t.Fatalf("new db failed: %v", err)
	}
	defer db.Close()

	key := "v1_account_manifest"
	if err := db.ApplyAccountUpdate(10, KVUpdate{Key: key, Value: []byte("v10")}); err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	waitForCheckpointAtLeast(t, db, 10)

	manifestPath := db.checkpointManifestPath(10)
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("expected checkpoint manifest: %v", err)
	}
	if err := os.WriteFile(manifestPath, []byte(`{"version":1,"height":10,"state_root":"AQ=="}`), 0o644); err != nil {
		t.Fatalf("corrupt manifest failed: %v", err)
	}
	if err := db.FlushAndRotate(10); err != nil {
		t.Fatalf("checkpoint recreate failed: %v", err)
	}

	db.mu.Lock()
	err = db.validateCheckpointManifestLocked(10)
	db.mu.Unlock()
	if err != nil {
		t.Fatalf("expected recreated checkpoint manifest to validate: %v", err)
	}
}

func TestRecreateCheckpointForcesRefreshEvenWhenManifestMatches(t *testing.T) {
	cfg := testPebbleConfig(t.TempDir())
	cfg.CheckpointKeep = 5

	db, err := New(cfg)
	if err != nil {
		t.Fatalf("new db failed: %v", err)
	}
	defer db.Close()

	key := "v1_account_recreate_force"
	if err := db.ApplyAccountUpdate(10, KVUpdate{Key: key, Value: []byte("v10")}); err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	waitForCheckpointAtLeast(t, db, 10)
	first, err := db.readCheckpointManifest(10)
	if err != nil {
		t.Fatalf("read first manifest failed: %v", err)
	}

	time.Sleep(2 * time.Millisecond)
	if err := db.RecreateCheckpoint(10); err != nil {
		t.Fatalf("force recreate checkpoint failed: %v", err)
	}
	second, err := db.readCheckpointManifest(10)
	if err != nil {
		t.Fatalf("read second manifest failed: %v", err)
	}
	if second.CreatedUnixNano <= first.CreatedUnixNano {
		t.Fatalf("expected RecreateCheckpoint to rewrite manifest timestamp, first=%d second=%d", first.CreatedUnixNano, second.CreatedUnixNano)
	}
}

func TestCheckpointKeepPrunesOld(t *testing.T) {
	cfg := testPebbleConfig(t.TempDir())
	cfg.CheckpointKeep = 2

	db, err := New(cfg)
	if err != nil {
		t.Fatalf("new db failed: %v", err)
	}
	defer db.Close()

	key := "v1_account_addr_prune"
	heights := []uint64{10, 20, 30}
	for _, h := range heights {
		if err := db.ApplyAccountUpdate(h, KVUpdate{Key: key, Value: []byte{byte(h)}}); err != nil {
			t.Fatalf("apply failed at %d: %v", h, err)
		}
	}
	waitForCheckpointAtLeast(t, db, 30)

	cpRoot := filepath.Join(cfg.DataDir, "checkpoints")
	entries, err := os.ReadDir(cpRoot)
	if err != nil {
		t.Fatalf("read checkpoints dir failed: %v", err)
	}

	found := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() {
			found[e.Name()] = true
		}
	}

	if !found["h_00000000000000000030"] {
		t.Fatalf("expected latest checkpoint 30 to exist, got %#v", found)
	}
	if len(found) > cfg.CheckpointKeep {
		t.Fatalf("expected at most %d checkpoints, got %d (%#v)", cfg.CheckpointKeep, len(found), found)
	}
}

func TestCheckpointChunkedInstallRoundTrip(t *testing.T) {
	srcCfg := testPebbleConfig(t.TempDir())
	srcCfg.CheckpointKeep = 5

	src, err := New(srcCfg)
	if err != nil {
		t.Fatalf("new source db failed: %v", err)
	}
	defer src.Close()

	k1 := "v1_account_chunked_archive_1"
	k2 := "v1_account_chunked_archive_2"
	if err := src.ApplyAccountUpdateWithOptions(9, ApplyOptions{BlockID: "peer-owned-block-9"}); err != nil {
		t.Fatalf("source height-9 metadata failed: %v", err)
	}
	if err := src.ApplyAccountUpdateWithOptions(10, ApplyOptions{BlockID: "peer-owned-block-10"}, KVUpdate{Key: k1, Value: []byte("chunk-v10-1")}, KVUpdate{Key: k2, Value: []byte("chunk-v10-2")}); err != nil {
		t.Fatalf("source apply failed: %v", err)
	}
	waitForCheckpointAtLeast(t, src, 10)
	expectedRoot := src.GetLiveFingerprintBytes()

	files, totalBytes, err := src.ListCheckpointFiles(10)
	if err != nil {
		t.Fatalf("list checkpoint files failed: %v", err)
	}
	if len(files) == 0 || totalBytes == 0 {
		t.Fatalf("expected non-empty checkpoint file manifest, files=%d total=%d", len(files), totalBytes)
	}

	importDir := t.TempDir()
	const testChunkSize int64 = 7
	for _, file := range files {
		localPath := filepath.Join(importDir, filepath.FromSlash(file.Name))
		if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
			t.Fatalf("mkdir import parent failed: %v", err)
		}
		out, err := os.OpenFile(localPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatalf("create import file failed: %v", err)
		}
		for offset := int64(0); offset < file.Size; offset += testChunkSize {
			limit := testChunkSize
			if remain := file.Size - offset; remain < limit {
				limit = remain
			}
			n, err := src.WriteCheckpointChunk(10, file.Name, offset, limit, out)
			if err != nil {
				_ = out.Close()
				t.Fatalf("write checkpoint chunk failed file=%s offset=%d: %v", file.Name, offset, err)
			}
			if n != limit {
				_ = out.Close()
				t.Fatalf("chunk size mismatch file=%s offset=%d got=%d want=%d", file.Name, offset, n, limit)
			}
		}
		if err := out.Close(); err != nil {
			t.Fatalf("close import file failed: %v", err)
		}
	}

	dstCfg := testPebbleConfig(t.TempDir())
	dstCfg.CheckpointKeep = 5
	dst, err := New(dstCfg)
	if err != nil {
		t.Fatalf("new destination db failed: %v", err)
	}
	defer dst.Close()
	if err := dst.ApplyAccountUpdate(1, KVUpdate{Key: "v1_account_old_chunked_archive", Value: []byte("old")}); err != nil {
		t.Fatalf("destination seed failed: %v", err)
	}
	if err := dst.InstallCheckpointDir(context.Background(), 10, "certified-block-10", importDir, expectedRoot); err != nil {
		t.Fatalf("install chunked checkpoint failed: %v", err)
	}
	if !bytes.Equal(dst.GetLiveFingerprintBytes(), expectedRoot) {
		t.Fatal("destination root mismatch after chunked checkpoint install")
	}
	got, exists, err := dst.Get(k2)
	if err != nil || !exists || string(got) != "chunk-v10-2" {
		t.Fatalf("destination value mismatch exists=%v value=%q err=%v", exists, got, err)
	}
	if height, exists, err := dst.LastAppliedHeight(); err != nil || !exists || height != 10 {
		t.Fatalf("normalized applied height=(%d,%t,%v), want 10", height, exists, err)
	}
	if blockID, exists, err := dst.AppliedBlockID(10); err != nil || !exists || blockID != "certified-block-10" {
		t.Fatalf("normalized applied block=(%q,%t,%v), want certified-block-10", blockID, exists, err)
	}
	if blockID, exists, err := dst.AppliedBlockID(9); err != nil || exists || blockID != "" {
		t.Fatalf("peer-owned historical applied block survived normalization: (%q,%t,%v)", blockID, exists, err)
	}
}

func TestCheckpointDoesNotCreateHistoricalSnapshotFromNewerLiveState(t *testing.T) {
	cfg := testPebbleConfig(t.TempDir())
	cfg.CheckpointKeep = 5

	db, err := New(cfg)
	if err != nil {
		t.Fatalf("new db failed: %v", err)
	}
	defer db.Close()

	key := "v1_account_hist_guard"
	if err := db.ApplyAccountUpdate(10, KVUpdate{Key: key, Value: []byte("v10")}); err != nil {
		t.Fatalf("apply 10 failed: %v", err)
	}
	waitForCheckpointAtLeast(t, db, 10)
	if err := db.ApplyAccountUpdate(11, KVUpdate{Key: key, Value: []byte("v11")}); err != nil {
		t.Fatalf("apply 11 failed: %v", err)
	}
	if err := db.RecreateCheckpoint(10); err == nil {
		t.Fatal("expected stale historical checkpoint recreation to fail")
	}
	if err := db.removeCheckpointFiles(10); err != nil {
		t.Fatalf("remove checkpoint failed: %v", err)
	}
	if err := db.FlushAndRotate(10); err == nil {
		t.Fatal("expected stale historical checkpoint creation to fail")
	}
}

func TestIterateLatestByPrefix(t *testing.T) {
	cfg := testPebbleConfig(t.TempDir())

	db, err := New(cfg)
	if err != nil {
		t.Fatalf("new db failed: %v", err)
	}
	defer db.Close()

	updates := []KVUpdate{
		{Key: "v1_account_pref_a", Value: []byte("a")},
		{Key: "v1_account_pref_b", Value: []byte("b")},
		{Key: "v1_orderstate_pref_1", Value: []byte("o1")},
	}
	if err := db.ApplyAccountUpdate(1, updates...); err != nil {
		t.Fatalf("apply batch failed: %v", err)
	}

	got := map[string]string{}
	if err := db.IterateLatestByPrefix("v1_account_", func(key string, value []byte) error {
		got[key] = string(value)
		return nil
	}); err != nil {
		t.Fatalf("iterate by prefix failed: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("expected 2 account keys, got %d", len(got))
	}
	if got["v1_account_pref_a"] != "a" {
		t.Fatalf("unexpected value for v1_account_pref_a: %q", got["v1_account_pref_a"])
	}
	if got["v1_account_pref_b"] != "b" {
		t.Fatalf("unexpected value for v1_account_pref_b: %q", got["v1_account_pref_b"])
	}
	if _, exists := got["v1_orderstate_pref_1"]; exists {
		t.Fatal("order key should not be returned by account prefix scan")
	}
}

func TestClosedDBReadPathsReturnError(t *testing.T) {
	cfg := testPebbleConfig(t.TempDir())
	db, err := New(cfg)
	if err != nil {
		t.Fatalf("new db failed: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close db failed: %v", err)
	}

	assertClosed := func(name string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), "stateDB is not initialized") {
			t.Fatalf("%s expected stateDB closed error, got %v", name, err)
		}
	}

	_, _, err = db.Get("v1_account_closed")
	assertClosed("Get", err)
	_, err = db.GetMany([]string{"v1_account_closed"})
	assertClosed("GetMany", err)
	_, err = db.PageCurrentDiff("0", 1, "")
	assertClosed("PageCurrentDiff", err)
	err = db.IterateLatestByPrefix("v1_account_", func(key string, value []byte) error { return nil })
	assertClosed("IterateLatestByPrefix", err)
	_, err = db.IterateLatestByPrefixLimitAfter("v1_account_", "", 1, false)
	assertClosed("IterateLatestByPrefixLimitAfter", err)
	_, err = db.IterateLatestKeysByPrefixLimitAfter("v1_account_", "", 1, false)
	assertClosed("IterateLatestKeysByPrefixLimitAfter", err)
}

func TestIterateLatestByPrefixLimitAfterOrdered(t *testing.T) {
	cfg := testPebbleConfig(t.TempDir())

	db, err := New(cfg)
	if err != nil {
		t.Fatalf("new db failed: %v", err)
	}
	defer db.Close()

	updates := make([]KVUpdate, 0, 20)
	for i := 0; i < 20; i++ {
		key := fmt.Sprintf("v1_account_scan_%03d", i)
		updates = append(updates, KVUpdate{Key: key, Value: []byte(key)})
	}
	if err := db.ApplyAccountUpdate(1, updates...); err != nil {
		t.Fatalf("apply batch failed: %v", err)
	}

	asc, err := db.IterateLatestByPrefixLimitAfter("v1_account_scan_", "v1_account_scan_003", 4, false)
	if err != nil {
		t.Fatalf("asc scan failed: %v", err)
	}
	assertKVUpdateKeys(t, asc, []string{
		"v1_account_scan_004",
		"v1_account_scan_005",
		"v1_account_scan_006",
		"v1_account_scan_007",
	})

	desc, err := db.IterateLatestByPrefixLimitAfter("v1_account_scan_", "v1_account_scan_016", 4, true)
	if err != nil {
		t.Fatalf("desc scan failed: %v", err)
	}
	assertKVUpdateKeys(t, desc, []string{
		"v1_account_scan_015",
		"v1_account_scan_014",
		"v1_account_scan_013",
		"v1_account_scan_012",
	})
}

func assertKVUpdateKeys(t *testing.T, got []KVUpdate, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len mismatch: got %d want %d updates=%+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Key != want[i] {
			t.Fatalf("key[%d]: got %s want %s updates=%+v", i, got[i].Key, want[i], got)
		}
	}
}

func TestGetMany(t *testing.T) {
	cfg := testPebbleConfig(t.TempDir())

	db, err := New(cfg)
	if err != nil {
		t.Fatalf("new db failed: %v", err)
	}
	defer db.Close()

	updates := []KVUpdate{
		{Key: "v1_account_getmany_a", Value: []byte("a")},
		{Key: "v1_account_getmany_b", Value: []byte("b")},
	}
	if err := db.ApplyAccountUpdate(1, updates...); err != nil {
		t.Fatalf("apply updates failed: %v", err)
	}

	got, err := db.GetMany([]string{
		"v1_account_getmany_a",
		"v1_account_getmany_b",
		"v1_account_getmany_missing",
	})
	if err != nil {
		t.Fatalf("get many failed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(got))
	}
	if string(got["v1_account_getmany_a"]) != "a" {
		t.Fatalf("unexpected value for key a: %q", string(got["v1_account_getmany_a"]))
	}
	if string(got["v1_account_getmany_b"]) != "b" {
		t.Fatalf("unexpected value for key b: %q", string(got["v1_account_getmany_b"]))
	}
	if _, exists := got["v1_account_getmany_missing"]; exists {
		t.Fatal("missing key should not exist in result")
	}
}

func TestCompactBusinessPrefix(t *testing.T) {
	cfg := testPebbleConfig(t.TempDir())

	db, err := New(cfg)
	if err != nil {
		t.Fatalf("new db failed: %v", err)
	}
	defer db.Close()

	updates := []KVUpdate{
		{Key: "v1_orderstate_compact_a", Value: []byte("oa")},
		{Key: "v1_orderstate_compact_b", Value: []byte("ob")},
		{Key: "v1_account_compact_a", Value: []byte("aa")},
	}
	if err := db.ApplyAccountUpdate(1, updates...); err != nil {
		t.Fatalf("apply updates failed: %v", err)
	}

	if err := db.CompactBusinessPrefix("v1_orderstate_"); err != nil {
		t.Fatalf("compact orderstate prefix failed: %v", err)
	}

	val, exists, err := db.Get("v1_orderstate_compact_a")
	if err != nil {
		t.Fatalf("get orderstate key failed: %v", err)
	}
	if !exists || string(val) != "oa" {
		t.Fatalf("unexpected compacted key state, exists=%v value=%q", exists, string(val))
	}

	if err := db.CompactBusinessPrefix(""); err == nil {
		t.Fatal("expected empty prefix compact to fail")
	}
}

func waitForCheckpointAtLeast(t *testing.T, db *DB, height uint64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		latest, err := db.LatestCheckpointHeight()
		if err == nil && latest >= height {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	latest, err := db.LatestCheckpointHeight()
	t.Fatalf("checkpoint %d not ready before timeout, latest=%d err=%v", height, latest, err)
}
