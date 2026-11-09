package vm

import (
	keypkg "dex/keys"
	"fmt"
	"math/bits"
	"math/rand"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/cockroachdb/pebble"
)

func expectedOrderedKeys(state map[string][]byte, prefix, startAfter string, limit int, reverse bool) []string {
	keys := make([]string, 0)
	for key := range state {
		if !strings.HasPrefix(key, prefix) || !orderedAfter(key, startAfter, reverse) {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if reverse {
		for left, right := 0, len(keys)-1; left < right; left, right = left+1, right-1 {
			keys[left], keys[right] = keys[right], keys[left]
		}
	}
	if limit > 0 && len(keys) > limit {
		keys = keys[:limit]
	}
	return keys
}

func TestOverlayKeyIndexPostScanInsertionsRemainLogarithmic(t *testing.T) {
	for _, count := range []int{12_448, 12_449, 12_450} {
		t.Run(fmt.Sprintf("keys_%d", count), func(t *testing.T) {
			sv := newOverlayOrderedTestStateView(map[string][]byte{})
			if _, err := sv.(orderedStateScannerAfter).ScanWithLimitOrderedAfter("perf/", "", 1, false); err != nil {
				t.Fatal(err)
			}
			concrete := sv.(*overlayStateView)
			if concrete.overlayIndex == nil || concrete.overlayIndex.builds != 1 {
				t.Fatal("ordered scan did not lazily build exactly one overlay index")
			}
			beforeComparisons := concrete.overlayIndex.comparisons
			for i := count - 1; i >= 0; i-- {
				sv.Set(fmt.Sprintf("perf/%05d", i), []byte{1})
			}
			comparisons := concrete.overlayIndex.comparisons - beforeComparisons
			maxComparisons := uint64(count * 8 * (bits.Len(uint(count)) + 1))
			if comparisons > maxComparisons {
				t.Fatalf("index comparisons=%d want<=%d", comparisons, maxComparisons)
			}
			if concrete.overlayIndex.builds != 1 || concrete.overlayIndex.tree.Len() != count {
				t.Fatalf("index builds=%d len=%d", concrete.overlayIndex.builds, concrete.overlayIndex.tree.Len())
			}

			beforeVisits := concrete.overlayIndex.rangeVisits
			rows, err := sv.(orderedStateScannerAfter).ScanWithLimitOrderedAfter("perf/", "", 0, false)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != count {
				t.Fatalf("ordered rows=%d want=%d", len(rows), count)
			}
			visits := concrete.overlayIndex.rangeVisits - beforeVisits
			if visits > uint64(count+1) {
				t.Fatalf("range visits=%d want<=%d", visits, count+1)
			}
			t.Logf("keys=%d comparisons=%d rangeVisits=%d", count, comparisons, visits)
		})
	}
}

func TestOverlayKeyIndexHonorsFullLexicalBounds(t *testing.T) {
	index := newOverlayKeyIndex([]string{"", "aa/0", "p/0", "p/1", "q/0", "zz/0"})
	if got := index.rangeKeys("", "", "", false); !reflect.DeepEqual(got, []string{"", "aa/0", "p/0", "p/1", "q/0", "zz/0"}) {
		t.Fatalf("full lexical range=%q", got)
	}

	before := index.rangeVisits
	got := index.rangeKeys("p/", "zzzz", "", true)
	if !reflect.DeepEqual(got, []string{"p/1", "p/0"}) {
		t.Fatalf("reverse clamped prefix range=%q", got)
	}
	if visits := index.rangeVisits - before; visits > uint64(len(got)+1) {
		t.Fatalf("reverse prefix range visited unrelated keys: visits=%d got=%d", visits, len(got))
	}
}

func TestOverlayKeyIndexRandomizedStateViewDifferential(t *testing.T) {
	base := map[string][]byte{
		"p/000": {1}, "p/010": {1}, "q/005": {1}, string([]byte{0xff, '/', 'a'}): {1},
	}
	expected := cloneBytesMap(base)
	sv := newOverlayOrderedTestStateView(base)
	rng := rand.New(rand.NewSource(20260923))
	type savedState struct {
		snap  int
		state map[string][]byte
	}
	saved := make([]savedState, 0)
	prefixes := []string{"p/", "q/", string([]byte{0xff})}

	for step := 0; step < 600; step++ {
		prefix := prefixes[rng.Intn(len(prefixes))]
		key := fmt.Sprintf("%s%03d", prefix, rng.Intn(180))
		switch rng.Intn(10) {
		case 0, 1, 2, 3, 4:
			value := []byte{byte(step), byte(step >> 8)}
			sv.Set(key, value)
			expected[key] = cloneBytes(value)
		case 5, 6:
			sv.Del(key)
			delete(expected, key)
		case 7:
			saved = append(saved, savedState{snap: sv.Snapshot(), state: cloneBytesMap(expected)})
		case 8:
			if len(saved) > 0 {
				target := rng.Intn(len(saved))
				if err := sv.Revert(saved[target].snap); err != nil {
					t.Fatal(err)
				}
				expected = cloneBytesMap(saved[target].state)
				saved = saved[:target+1]
			}
		case 9:
			releaseStateViewHistory(sv)
			saved = saved[:0]
		}

		for _, reverse := range []bool{false, true} {
			cursor := ""
			if rng.Intn(2) == 0 {
				cursor = fmt.Sprintf("%s%03d", prefix, rng.Intn(180))
			}
			limit := []int{1, 7, 64}[rng.Intn(3)]
			want := expectedOrderedKeys(expected, prefix, cursor, limit, reverse)
			pairs, err := sv.(orderedStateScannerAfter).ScanWithLimitOrderedAfter(prefix, cursor, limit, reverse)
			if err != nil {
				t.Fatalf("step=%d pairs: %v", step, err)
			}
			gotPairs := make([]string, 0, len(pairs))
			for _, pair := range pairs {
				gotPairs = append(gotPairs, pair.Key)
			}
			if !reflect.DeepEqual(gotPairs, want) {
				t.Fatalf("step=%d reverse=%v cursor=%q pairs=%v want=%v", step, reverse, cursor, gotPairs, want)
			}
			gotKeys, err := sv.(orderedStateKeyScannerAfter).ScanKeysWithLimitOrderedAfter(prefix, cursor, limit, reverse)
			if err != nil {
				t.Fatalf("step=%d keys: %v", step, err)
			}
			if !reflect.DeepEqual(gotKeys, want) {
				t.Fatalf("step=%d reverse=%v cursor=%q keys=%v want=%v", step, reverse, cursor, gotKeys, want)
			}
		}
	}
}

func newOverlayOrderedTestStateView(base map[string][]byte) StateView {
	read := func(key string) ([]byte, error) {
		if v, ok := base[key]; ok {
			return cloneBytes(v), nil
		}
		return nil, nil
	}
	scanOrdered := func(prefix string, limit int, reverse bool) ([]KVPair, error) {
		keys := make([]string, 0)
		for k := range base {
			if strings.HasPrefix(k, prefix) {
				keys = append(keys, k)
			}
		}
		if reverse {
			sort.Sort(sort.Reverse(sort.StringSlice(keys)))
		} else {
			sort.Strings(keys)
		}
		if limit > 0 && len(keys) > limit {
			keys = keys[:limit]
		}
		out := make([]KVPair, 0, len(keys))
		for _, k := range keys {
			out = append(out, KVPair{Key: k, Value: cloneBytes(base[k])})
		}
		return out, nil
	}
	scan := func(prefix string) (map[string][]byte, error) {
		pairs, err := scanOrdered(prefix, 0, false)
		if err != nil {
			return nil, err
		}
		return orderedPairsToMap(pairs), nil
	}
	scanLimit := func(prefix string, limit int) (map[string][]byte, error) {
		pairs, err := scanOrdered(prefix, limit, false)
		if err != nil {
			return nil, err
		}
		return orderedPairsToMap(pairs), nil
	}
	scanLimitReverse := func(prefix string, limit int) (map[string][]byte, error) {
		pairs, err := scanOrdered(prefix, limit, true)
		if err != nil {
			return nil, err
		}
		return orderedPairsToMap(pairs), nil
	}
	return NewStateViewWithLimitScanOrdered(read, scan, scanLimit, scanLimitReverse, scanOrdered, nil)
}

func TestStateViewRevisionIsMonotonicAcrossRollbackAndHistoryRelease(t *testing.T) {
	sv := newMemStateView()
	rev0 := sv.Revision()
	snap := sv.Snapshot()
	sv.Set("v1_revision_probe", []byte{1})
	rev1 := sv.Revision()
	if rev1 <= rev0 {
		t.Fatalf("Set revision=%d, want > %d", rev1, rev0)
	}
	if err := sv.Revert(snap); err != nil {
		t.Fatal(err)
	}
	rev2 := sv.Revision()
	if rev2 <= rev1 {
		t.Fatalf("Revert revision=%d, want > %d", rev2, rev1)
	}
	releaseStateViewHistory(sv)
	rev3 := sv.Revision()
	if rev3 <= rev2 {
		t.Fatalf("ReleaseHistory revision=%d, want > %d", rev3, rev2)
	}
	snapBeforeSeed := sv.Snapshot()
	applyWriteOpsToStateView(sv, []WriteOp{{Key: "v1_revision_seed", Value: []byte{2}}})
	rev4 := sv.Revision()
	if rev4 <= rev3 {
		t.Fatalf("ApplyWriteOps revision=%d, want > %d", rev4, rev3)
	}
	if got := sv.Snapshot(); got != snapBeforeSeed {
		t.Fatalf("ApplyWriteOps changed rollback baseline cursor: got=%d want=%d", got, snapBeforeSeed)
	}
}

func TestOverlayStateViewOrderedScanUsesPrefixOverlayAndRevert(t *testing.T) {
	sv := newOverlayOrderedTestStateView(map[string][]byte{
		"book/a":  []byte("base-a"),
		"book/c":  []byte("base-c"),
		"other/z": []byte("other"),
	})
	ordered := sv.(orderedStateScanner)

	snap := sv.Snapshot()
	sv.Del("book/a")
	sv.Set("book/b", []byte("overlay-b"))
	sv.Set("book/d", []byte("overlay-d"))
	sv.Set("other/a", []byte("overlay-other"))

	asc, err := ordered.ScanWithLimitOrdered("book/", 2, false)
	if err != nil {
		t.Fatalf("scan asc: %v", err)
	}
	assertKVKeys(t, asc, []string{"book/b", "book/c", "book/d"})

	desc, err := ordered.ScanWithLimitOrdered("book/", 2, true)
	if err != nil {
		t.Fatalf("scan desc: %v", err)
	}
	assertKVKeys(t, desc, []string{"book/d", "book/c", "book/b"})

	if err := sv.Revert(snap); err != nil {
		t.Fatalf("revert: %v", err)
	}
	after, err := ordered.ScanWithLimitOrdered("book/", 10, false)
	if err != nil {
		t.Fatalf("scan after revert: %v", err)
	}
	assertKVKeys(t, after, []string{"book/a", "book/c"})
}

func TestOverlayStateViewOrderedScanOverfetchesTombstones(t *testing.T) {
	sv := newOverlayOrderedTestStateView(map[string][]byte{
		"book/a": []byte("base-a"),
		"book/c": []byte("base-c"),
		"book/e": []byte("base-e"),
	})
	ordered := sv.(orderedStateScanner)

	sv.Del("book/a")
	sv.Del("book/c")
	sv.Set("book/b", []byte("overlay-b"))
	sv.Set("book/d", []byte("overlay-d"))

	got, err := ordered.ScanWithLimitOrdered("book/", 2, false)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	assertKVKeys(t, got, []string{"book/b", "book/d", "book/e"})
}

func TestOverlayStateViewOrderedScanAfterMergesOverlay(t *testing.T) {
	sv := newOverlayOrderedTestStateView(map[string][]byte{
		"book/a": []byte("base-a"),
		"book/c": []byte("base-c"),
		"book/e": []byte("base-e"),
	})
	orderedAfter := sv.(orderedStateScannerAfter)

	sv.Set("book/b", []byte("overlay-b"))
	sv.Del("book/c")
	sv.Set("book/d", []byte("overlay-d"))

	got, err := orderedAfter.ScanWithLimitOrderedAfter("book/", "book/b", 10, false)
	if err != nil {
		t.Fatalf("scan after: %v", err)
	}
	assertKVKeys(t, got, []string{"book/d", "book/e"})
}

func TestOverlayStateViewOrderedAfterFillsAcrossTombstonesInBothDirections(t *testing.T) {
	sv := newOverlayOrderedTestStateView(map[string][]byte{
		"book/a": []byte("base-a"),
		"book/c": []byte("base-c"),
		"book/e": []byte("base-e"),
		"book/g": []byte("base-g"),
	})
	sv.Set("book/b", []byte("overlay-b"))
	sv.Del("book/c")
	sv.Set("book/d", []byte("overlay-d"))
	sv.Del("book/e")
	sv.Set("book/z", []byte("overlay-z"))

	pairs := sv.(orderedStateScannerAfter)
	forward, err := pairs.ScanWithLimitOrderedAfter("book/", "book/b", 2, false)
	if err != nil {
		t.Fatalf("forward pairs: %v", err)
	}
	assertKVKeys(t, forward, []string{"book/d", "book/g"})
	reverse, err := pairs.ScanWithLimitOrderedAfter("book/", "book/z", 2, true)
	if err != nil {
		t.Fatalf("reverse pairs: %v", err)
	}
	assertKVKeys(t, reverse, []string{"book/g", "book/d"})

	keysOnly := sv.(orderedStateKeyScannerAfter)
	forwardKeys, err := keysOnly.ScanKeysWithLimitOrderedAfter("book/", "book/b", 2, false)
	if err != nil {
		t.Fatalf("forward keys: %v", err)
	}
	if !reflect.DeepEqual(forwardKeys, []string{"book/d", "book/g"}) {
		t.Fatalf("forward keys=%v", forwardKeys)
	}
	reverseKeys, err := keysOnly.ScanKeysWithLimitOrderedAfter("book/", "book/z", 2, true)
	if err != nil {
		t.Fatalf("reverse keys: %v", err)
	}
	if !reflect.DeepEqual(reverseKeys, []string{"book/g", "book/d"}) {
		t.Fatalf("reverse keys=%v", reverseKeys)
	}
}

func TestOverlayStateViewDiffCarriesKnownOldValue(t *testing.T) {
	sv := newOverlayOrderedTestStateView(map[string][]byte{
		"book/a": []byte("base-a"),
	})
	if _, ok, err := sv.Get("book/a"); err != nil || !ok {
		t.Fatalf("read old value: exists=%v err=%v", ok, err)
	}
	sv.Set("book/a", []byte("new-a"))

	diff := sv.Diff()
	if len(diff) != 1 {
		t.Fatalf("diff len=%d diff=%+v", len(diff), diff)
	}
	if !diff[0].HasOldValue || !diff[0].OldExists || string(diff[0].OldValue) != "base-a" {
		t.Fatalf("old value not carried: %+v", diff[0])
	}
}

func TestOverlayStateViewParallelDiffMatchesSerial(t *testing.T) {
	oldProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(oldProcs)

	base := make(map[string][]byte)
	sv := newOverlayOrderedTestStateView(base)
	for i := 0; i < stateViewParallelDiffMinKeys+128; i++ {
		key := keypkg.KeyAccount(fmt.Sprintf("parallel_%05d", i))
		switch i % 3 {
		case 0:
			base[key] = []byte(fmt.Sprintf("old-%05d", i))
			if _, ok, err := sv.Get(key); err != nil || !ok {
				t.Fatalf("read base key %s: exists=%v err=%v", key, ok, err)
			}
			sv.Set(key, []byte(fmt.Sprintf("new-%05d", i)))
		case 1:
			base[key] = []byte(fmt.Sprintf("delete-%05d", i))
			if _, ok, err := sv.Get(key); err != nil || !ok {
				t.Fatalf("read deleted key %s: exists=%v err=%v", key, ok, err)
			}
			sv.Del(key)
		default:
			sv.Set(key, []byte(fmt.Sprintf("insert-%05d", i)))
		}
	}

	serial := sv.Diff()
	runtime.GOMAXPROCS(4)
	parallel := sv.Diff()
	if !reflect.DeepEqual(serial, parallel) {
		t.Fatalf("parallel diff differs from serial: serial=%d parallel=%d", len(serial), len(parallel))
	}
}

func TestOverlayStateViewPrefetchesOldValuesForBlindStateWrites(t *testing.T) {
	existingKey := keypkg.KeyAccount("blind-existing")
	missingKey := keypkg.KeyBalance("blind-missing", "FB")
	base := map[string][]byte{existingKey: []byte("base-existing")}
	var requested []string
	batchRead := func(keys []string) (map[string][]byte, error) {
		requested = append(requested, keys...)
		out := make(map[string][]byte, len(keys))
		for _, k := range keys {
			if v, ok := base[k]; ok {
				out[k] = cloneBytes(v)
			}
		}
		return out, nil
	}
	sv := NewStateViewWithLimitScanOrderedAndBatch(nil, batchRead, nil, nil, nil, nil, nil)
	sv.Set(missingKey, []byte("new-missing"))
	sv.Set(existingKey, []byte("new-existing"))
	if err := prefetchStateViewOldValues(sv); err != nil {
		t.Fatalf("prefetch old values: %v", err)
	}
	if len(requested) != 2 || requested[0] != existingKey || requested[1] != missingKey {
		t.Fatalf("expected sorted batch request [%s %s], got %v", existingKey, missingKey, requested)
	}

	byKey := make(map[string]WriteOp)
	for _, op := range sv.Diff() {
		byKey[op.Key] = op
	}
	existing := byKey[existingKey]
	if !existing.HasOldValue || !existing.OldExists || !existing.OldValueAuthoritative || string(existing.OldValue) != "base-existing" {
		t.Fatalf("existing old value not carried authoritatively: %+v", existing)
	}
	missing := byKey[missingKey]
	if !missing.HasOldValue || missing.OldExists || !missing.OldValueAuthoritative || missing.OldValue != nil {
		t.Fatalf("missing old value not carried authoritatively: %+v", missing)
	}
}

func TestOverlayStateViewTreatsPebbleNotFoundAsAbsent(t *testing.T) {
	read := func(string) ([]byte, error) { return nil, pebble.ErrNotFound }
	sv := NewStateView(read, nil)

	value, exists, err := sv.Get("v1_archive_missing")
	if err != nil {
		t.Fatalf("missing KV read leaked storage error: %v", err)
	}
	if exists || value != nil {
		t.Fatalf("missing KV read = (%q, %v), want (nil, false)", value, exists)
	}

	                                                                        
	                                                                         
	value, exists, err = sv.Get("v1_archive_missing")
	if err != nil || exists || value != nil {
		t.Fatalf("cached missing KV read = (%q, %v, %v)", value, exists, err)
	}
}

func TestOverlayStateViewDiffDoesNotInventOldMissing(t *testing.T) {
	sv := newOverlayOrderedTestStateView(map[string][]byte{
		"v1_orderstate_existing": []byte("old"),
	})
	sv.Set("v1_orderstate_existing", []byte("new"))

	diff := sv.Diff()
	if len(diff) != 1 {
		t.Fatalf("diff len=%d diff=%+v", len(diff), diff)
	}
	if diff[0].HasOldValue {
		t.Fatalf("diff should not claim old value unless execution read it: %+v", diff[0])
	}
}

func TestUserActivitySeqAllocatorCachesAndReverts(t *testing.T) {
	sv := newOverlayOrderedTestStateView(nil)
	seq, err := nextUserActivitySeq(sv, "alice", 7)
	if err != nil || seq != 0 {
		t.Fatalf("seq0=%d err=%v", seq, err)
	}
	sv.Set(keypkg.KeyUserActivity("alice", 7, seq), []byte("a"))
	seq, err = nextUserActivitySeq(sv, "alice", 7)
	if err != nil || seq != 1 {
		t.Fatalf("seq1=%d err=%v", seq, err)
	}
	sv.Set(keypkg.KeyUserActivity("alice", 7, seq), []byte("b"))
	snap := sv.Snapshot()
	seq, err = nextUserActivitySeq(sv, "alice", 7)
	if err != nil || seq != 2 {
		t.Fatalf("seq2=%d err=%v", seq, err)
	}
	sv.Set(keypkg.KeyUserActivity("alice", 7, seq), []byte("c"))
	if err := sv.Revert(snap); err != nil {
		t.Fatalf("revert: %v", err)
	}
	seq, err = nextUserActivitySeq(sv, "alice", 7)
	if err != nil || seq != 2 {
		t.Fatalf("seq after revert=%d err=%v", seq, err)
	}
}

func TestApplyWriteOpsToStateViewPreservesKnownOldValue(t *testing.T) {
	key := keypkg.KeyAccount("seed_old")
	sv := newOverlayOrderedTestStateView(map[string][]byte{
		key: []byte("db-old"),
	})

	applyWriteOpsToStateView(sv, []WriteOp{{
		Key:                   key,
		Value:                 []byte("seed-new"),
		HasOldValue:           true,
		OldExists:             true,
		OldValue:              []byte("known-old"),
		OldValueAuthoritative: true,
	}})
	if err := prefetchStateViewOldValues(sv); err != nil {
		t.Fatalf("prefetch failed: %v", err)
	}
	diff := sv.Diff()
	if len(diff) != 1 {
		t.Fatalf("expected one diff, got %d", len(diff))
	}
	if !diff[0].HasOldValue || !diff[0].OldExists || string(diff[0].OldValue) != "known-old" || !diff[0].OldValueAuthoritative {
		t.Fatalf("seeded old value not preserved: %+v", diff[0])
	}
}

func TestPrepareApplyResultKVTasksParallelMatchesSerial(t *testing.T) {
	oldProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(oldProcs)

	diff := make([]WriteOp, 0, applyResultParallelMinOps+64)
	for i := 0; i < applyResultParallelMinOps+64; i++ {
		op := WriteOp{Value: []byte(fmt.Sprintf("value-%05d", i))}
		if i%4 == 0 {
			op.Key = keypkg.KeyBalance(fmt.Sprintf("owner_%05d", i), "FB")
		} else {
			op.Key = keypkg.KeyVMAppliedTx(fmt.Sprintf("tx_%05d", i))
		}
		if i%7 == 0 {
			op.Del = true
			op.Value = nil
		}
		diff = append(diff, op)
	}

	serialStrict, serialStateOps, serialWorkers := prepareApplyResultKVTasks(diff, true)
	serialLoose, serialLooseStateOps, _ := prepareApplyResultKVTasks(diff, false)
	if serialWorkers != 1 {
		t.Fatalf("serial workers=%d, want 1", serialWorkers)
	}

	runtime.GOMAXPROCS(4)
	parallelStrict, parallelStateOps, parallelWorkers := prepareApplyResultKVTasks(diff, true)
	parallelLoose, parallelLooseStateOps, _ := prepareApplyResultKVTasks(diff, false)
	if parallelWorkers <= 1 {
		t.Fatalf("parallel workers=%d, want >1", parallelWorkers)
	}
	if serialStateOps != parallelStateOps || !reflect.DeepEqual(serialStrict, parallelStrict) {
		t.Fatalf("strict tasks differ: serialState=%d parallelState=%d serialTasks=%d parallelTasks=%d", serialStateOps, parallelStateOps, len(serialStrict), len(parallelStrict))
	}
	if serialLooseStateOps != parallelLooseStateOps || !reflect.DeepEqual(serialLoose, parallelLoose) {
		t.Fatalf("loose tasks differ: serialState=%d parallelState=%d serialTasks=%d parallelTasks=%d", serialLooseStateOps, parallelLooseStateOps, len(serialLoose), len(parallelLoose))
	}
}

func TestOverlayStateViewBaseScanCacheIsExecutionLocal(t *testing.T) {
	base := map[string][]byte{"book/a": []byte("base-a")}
	scanCalls := 0
	orderedCalls := 0
	read := func(key string) ([]byte, error) {
		if v, ok := base[key]; ok {
			return cloneBytes(v), nil
		}
		return nil, nil
	}
	scanOrdered := func(prefix string, limit int, reverse bool) ([]KVPair, error) {
		orderedCalls++
		return []KVPair{{Key: "book/a", Value: cloneBytes(base["book/a"])}}, nil
	}
	scan := func(prefix string) (map[string][]byte, error) {
		scanCalls++
		return cloneBytesMap(base), nil
	}
	sv := NewStateViewWithLimitScanOrdered(read, scan, nil, nil, scanOrdered, nil)
	ordered := sv.(orderedStateScanner)

	first, err := sv.Scan("book/")
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}
	first["book/a"][0] = 'X'
	base["book/a"] = []byte("mutated")
	second, err := sv.Scan("book/")
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if scanCalls != 1 {
		t.Fatalf("base scan should be cached, calls=%d", scanCalls)
	}
	if string(second["book/a"]) != "base-a" {
		t.Fatalf("cached scan value should be isolated, got %q", second["book/a"])
	}

	if _, err := ordered.ScanWithLimitOrdered("book/", 1, false); err != nil {
		t.Fatalf("first ordered scan: %v", err)
	}
	if _, err := ordered.ScanWithLimitOrdered("book/", 1, false); err != nil {
		t.Fatalf("second ordered scan: %v", err)
	}
	if orderedCalls != 1 {
		t.Fatalf("ordered base scan should be cached, calls=%d", orderedCalls)
	}
}

func assertKVKeys(t *testing.T, got []KVPair, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len mismatch: got %d want %d pairs=%+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Key != want[i] {
			t.Fatalf("key[%d]: got %s want %s pairs=%+v", i, got[i].Key, want[i], got)
		}
	}
}
