package db

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"dex/keys"

	"github.com/cockroachdb/pebble"
)

func waitForWriteQueueCondition(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for write queue condition")
}

func attachWriteQueueRuntimeDB(manager *Manager, runtimeDB *pebble.DB) {
	manager.mu.Lock()
	manager.Db = runtimeDB
	manager.runtimeDB = runtimeDB
	manager.mu.Unlock()
}

func attachWriteQueueQueryDB(manager *Manager, queryDB *pebble.DB) {
	manager.mu.Lock()
	manager.queryDB = queryDB
	manager.mu.Unlock()
}

func TestWriteQueueRetainsHotBatchAfterFlushError(t *testing.T) {
	manager := &Manager{}
	manager.InitWriteQueue(10, time.Hour)
	t.Cleanup(manager.Close)

	key := keys.KeyVMAppliedTx("retry-hot")
	manager.EnqueueSet(key, "hot-value")
	if err := manager.ForceFlushHot(); err == nil {
		t.Fatal("first hot flush should fail without a runtime database")
	}
	failedAttempts := atomic.LoadUint64(&manager.writeQueueFlushErrTotal)
	if err := manager.ForceFlushHot(); err == nil {
		t.Fatal("force flush during retry backoff should attempt and return the database error")
	}
	if got := atomic.LoadUint64(&manager.writeQueueFlushErrTotal); got != failedAttempts+1 {
		t.Fatalf("force flush attempts = %d, want %d", got, failedAttempts+1)
	}

	runtimeDB := openMemDB(t)
	attachWriteQueueRuntimeDB(manager, runtimeDB)
	if err := manager.ForceFlushHot(); err != nil {
		t.Fatalf("retry hot flush: %v", err)
	}
	if got := mustGetPebbleValue(t, runtimeDB, key); got != "hot-value" {
		t.Fatalf("hot value after retry = %q", got)
	}
}

func TestWriteQueueRetainsColdBatchAfterFlushError(t *testing.T) {
	manager := &Manager{}
	manager.InitWriteQueue(10, time.Hour)
	t.Cleanup(manager.Close)

	key := keys.KeyUserActivity("retry-cold", 1, 0)
	manager.EnqueueSet(key, "cold-value")
	if err := manager.ForceFlush(); err == nil {
		t.Fatal("first cold flush should fail without a query database")
	}

	queryDB := openMemDB(t)
	attachWriteQueueQueryDB(manager, queryDB)
	if err := manager.ForceFlush(); err != nil {
		t.Fatalf("retry cold flush: %v", err)
	}
	if got := mustGetPebbleValue(t, queryDB, key); got != "cold-value" {
		t.Fatalf("cold value after retry = %q", got)
	}
}

func TestWriteQueueFlushFailureRetriesWithBackoff(t *testing.T) {
	manager := &Manager{}
	manager.InitWriteQueue(1, time.Hour)
	t.Cleanup(manager.Close)

	key := keys.KeyVMAppliedTx("retry-backoff")
	manager.EnqueueSet(key, "eventual-value")
	waitForWriteQueueCondition(t, time.Second, func() bool {
		return atomic.LoadUint64(&manager.writeQueueFlushErrTotal) > 0
	})

	                                                                         
	                                                  
	time.Sleep(450 * time.Millisecond)
	errorsBeforeRecovery := atomic.LoadUint64(&manager.writeQueueFlushErrTotal)
	if errorsBeforeRecovery < 2 {
		t.Fatalf("flush errors = %d, want an automatic retry", errorsBeforeRecovery)
	}
	if errorsBeforeRecovery > 5 {
		t.Fatalf("flush errors = %d, retry loop is not backing off", errorsBeforeRecovery)
	}

	runtimeDB := openMemDB(t)
	attachWriteQueueRuntimeDB(manager, runtimeDB)
	waitForWriteQueueCondition(t, 2*time.Second, func() bool {
		value, closer, err := runtimeDB.Get([]byte(key))
		if errors.Is(err, pebble.ErrNotFound) {
			return false
		}
		if err != nil {
			return false
		}
		defer closer.Close()
		return string(value) == "eventual-value"
	})
}

func TestWriteQueueFlushFailureAppliesBackpressure(t *testing.T) {
	manager := &Manager{}
	manager.InitWriteQueue(1, time.Hour)
	t.Cleanup(manager.Close)

	firstKey := keys.KeyVMAppliedTx("retry-backpressure-first")
	manager.EnqueueSet(firstKey, "first-value")
	waitForWriteQueueCondition(t, time.Second, func() bool {
		return atomic.LoadUint64(&manager.writeQueueFlushErrTotal) >= 1
	})

	const queuedTasks = 32
	for i := 0; i < queuedTasks; i++ {
		manager.EnqueueSet(keys.KeyVMAppliedTx("retry-backpressure-"+string(rune('a'+i))), "queued-value")
	}
	                                                                         
	                                                                
	time.Sleep(writeQueueRetryInitialDelay / 4)
	if got := atomic.LoadUint64(&manager.writeQueueDequeuedTotal); got != 1 {
		t.Fatalf("dequeued tasks during retry backoff = %d, want 1", got)
	}
	if got := len(manager.writeQueueChan); got != queuedTasks {
		t.Fatalf("queued tasks during retry backoff = %d, want %d", got, queuedTasks)
	}

	runtimeDB := openMemDB(t)
	attachWriteQueueRuntimeDB(manager, runtimeDB)
	if err := manager.ForceFlushHot(); err != nil {
		t.Fatalf("force flush after recovery: %v", err)
	}
	if got := mustGetPebbleValue(t, runtimeDB, firstKey); got != "first-value" {
		t.Fatalf("retained value after recovery = %q", got)
	}
}

func TestWriteQueueFailedBatchDoesNotSpinWithQueuedWork(t *testing.T) {
	manager := &Manager{}
	manager.InitWriteQueue(1, time.Hour)

	key := keys.KeyVMAppliedTx("retry-ordered")
	manager.EnqueueSet(key, "first")
	manager.EnqueueSet(key, "second")

	flushResult := make(chan error, 1)
	go func() {
		flushResult <- manager.ForceFlushHot()
	}()
	select {
	case err := <-flushResult:
		t.Cleanup(manager.Close)
		if err == nil {
			t.Fatal("flush should fail without a runtime database")
		}
	case <-time.After(time.Second):
		                                                                    
		                                                               
		t.Fatal("force flush did not return after the first failed batch")
	}

	runtimeDB := openMemDB(t)
	attachWriteQueueRuntimeDB(manager, runtimeDB)
	if err := manager.ForceFlushHot(); err != nil {
		t.Fatalf("force flush after recovery: %v", err)
	}
	if got := mustGetPebbleValue(t, runtimeDB, key); got != "second" {
		t.Fatalf("same-key value after ordered retry = %q, want second", got)
	}
}
