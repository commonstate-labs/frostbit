package db

import (
	"fmt"
	"testing"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/cockroachdb/pebble/vfs"
)

                                          
func flushAndWait(t *testing.T, mgr *Manager) {
	t.Helper()
	if err := mgr.ForceFlush(); err != nil {
		t.Fatalf("force flush: %v", err)
	}
}

func openMemDB(t *testing.T) *pebble.DB {
	t.Helper()
	db, err := pebble.Open("", &pebble.Options{FS: vfs.NewMem()})
	if err != nil {
		t.Fatalf("open pebble: %v", err)
	}
	return db
}

func TestForceFlushIsSynchronous(t *testing.T) {
	db := openMemDB(t)
	mgr := &Manager{Db: db}
	mgr.InitWriteQueue(1_000, time.Hour)
	t.Cleanup(func() { mgr.Close() })

	mgr.EnqueueSet("sync:test:key", "sync-value")
	if err := mgr.ForceFlush(); err != nil {
		t.Fatalf("force flush: %v", err)
	}
	got, err := mgr.GetKV("sync:test:key")
	if err != nil {
		t.Fatalf("read after force flush: %v", err)
	}
	if string(got) != "sync-value" {
		t.Fatalf("expected sync-value, got %q", string(got))
	}
}

func TestDrainWriteQueueHonorsMaxBatchSize(t *testing.T) {
	mgr := &Manager{
		writeQueueChan: make(chan WriteTask, 5),
		maxBatchSize:   3,
	}
	for i := 0; i < 5; i++ {
		mgr.writeQueueChan <- WriteTask{Key: []byte{byte('a' + i)}, Value: []byte("v"), Op: OpSet}
	}

	batch := mgr.drainWriteQueue(nil)
	if len(batch) != 3 {
		t.Fatalf("drained batch len = %d, want 3", len(batch))
	}
	if got := len(mgr.writeQueueChan); got != 2 {
		t.Fatalf("remaining queue len = %d, want 2", got)
	}
}

func TestFlushBatchSplitsByByteLimit(t *testing.T) {
	db := openMemDB(t)
	defer db.Close()
	mgr := &Manager{
		Db:                  db,
		maxBatchSize:        1000,
		maxPebbleBatchBytes: 256,
	}
	value := make([]byte, 128)
	for i := range value {
		value[i] = byte('a' + i%26)
	}
	tasks := make([]WriteTask, 0, 20)
	for i := 0; i < 20; i++ {
		tasks = append(tasks, WriteTask{Key: []byte(fmt.Sprintf("split:%02d", i)), Value: value, Op: OpSet})
	}

	if err := mgr.flushBatch(tasks); err != nil {
		t.Fatalf("flush batch: %v", err)
	}
	for i := 0; i < 20; i++ {
		got, closer, err := db.Get([]byte(fmt.Sprintf("split:%02d", i)))
		if err != nil {
			t.Fatalf("read split:%02d: %v", i, err)
		}
		if string(got) != string(value) {
			t.Fatalf("split:%02d value mismatch", i)
		}
		_ = closer.Close()
	}
}

func TestFlushBatchReturnsErrorWhenPebbleDBClosed(t *testing.T) {
	mgr := &Manager{maxBatchSize: 10, maxPebbleBatchBytes: 256}
	err := mgr.flushBatch([]WriteTask{{Key: []byte("closed:test"), Value: []byte("v"), Op: OpSet}})
	if err == nil {
		t.Fatalf("flush batch should fail when pebble DB is nil")
	}
}

                                                
func TestIndexAllocator(t *testing.T) {
	db := openMemDB(t)

	mgr := &Manager{Db: db}
	mgr.InitWriteQueue(1_000, 200*time.Millisecond)

	                                    
	idx1, tasks, err := getNewIndex(mgr)
	if err != nil {
		t.Fatalf("getNewIndex-1: %v", err)
	}
	if idx1 != 1 {
		t.Fatalf("expected idx1==1, got %d", idx1)
	}
	for _, w := range tasks {
		mgr.writeQueueChan <- w
	}
	flushAndWait(t, mgr)

	                             
	idx2, tasks, err := getNewIndex(mgr)
	if err != nil {
		t.Fatalf("getNewIndex-2: %v", err)
	}
	if idx2 != 2 {
		t.Fatalf("expected idx2==2, got %d", idx2)
	}
	for _, w := range tasks {
		mgr.writeQueueChan <- w
	}
	flushAndWait(t, mgr)

	                                    
	mgr.writeQueueChan <- removeIndex(idx1)
	flushAndWait(t, mgr)

	                                              
	idx3, tasks, err := getNewIndex(mgr)
	if err != nil {
		t.Fatalf("getNewIndex-3: %v", err)
	}
	if idx3 != idx1 {
		t.Fatalf("expected recycled idx %d, got %d", idx1, idx3)
	}
	for _, w := range tasks {
		mgr.writeQueueChan <- w
	}
	flushAndWait(t, mgr)

	                                              
	idx4, tasks, err := getNewIndex(mgr)
	if err != nil {
		t.Fatalf("getNewIndex-4: %v", err)
	}
	if idx4 != 3 {
		t.Fatalf("expected idx4==3, got %d", idx4)
	}
	for _, w := range tasks {
		mgr.writeQueueChan <- w
	}
	flushAndWait(t, mgr)
	mgr.Close()
}

                                                                
func TestIndexAllocatorPerformance(t *testing.T) {
	const n = 10
	db := openMemDB(t)

	mgr := &Manager{Db: db}
	mgr.InitWriteQueue(100000, 200*time.Millisecond)

	start := time.Now()
	for i := 1; i <= n; i++ {
		idx, tasks, err := getNewIndex(mgr)
		if err != nil {
			t.Fatalf("alloc %d: %v", i, err)
		}
		if uint64(i) != idx {
			t.Fatalf("expect %d, got %d", i, idx)
		}
		for _, w := range tasks {
			mgr.writeQueueChan <- w
		}
		flushAndWait(t, mgr)
	}
	dur := time.Since(start)
	t.Logf("allocated %d indexes in %s (%.2f µs/idx)", n, dur, float64(dur.Microseconds())/float64(n))
	if dur > time.Second {
		t.Fatalf("too slow: %s for %d allocations", dur, n)
	}
	mgr.Close()
}
