package statedb

import (
	"strings"
	"testing"
	"time"
)

func TestLatestCheckpointHeightClosedStoreReturnsError(t *testing.T) {
	db, err := New(testPebbleConfig(t.TempDir()))
	if err != nil {
		t.Fatalf("open stateDB: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close stateDB: %v", err)
	}

	if _, err := db.LatestCheckpointHeight(); err == nil {
		t.Fatal("expected an unavailable-store error")
	} else if !strings.Contains(err.Error(), "store is not available") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLatestCheckpointHeightWaitsForStoreReplacement(t *testing.T) {
	db, err := New(testPebbleConfig(t.TempDir()))
	if err != nil {
		t.Fatalf("open stateDB: %v", err)
	}
	defer func() { _ = db.Close() }()

	type result struct {
		height uint64
		err    error
	}
	done := make(chan result, 1)

	db.mu.Lock()
	store := db.store
	db.store = nil
	go func() {
		height, err := db.LatestCheckpointHeight()
		done <- result{height: height, err: err}
	}()

	select {
	case got := <-done:
		db.store = store
		db.mu.Unlock()
		t.Fatalf("query returned during store replacement: height=%d err=%v", got.height, got.err)
	case <-time.After(50 * time.Millisecond):
	}

	db.store = store
	db.mu.Unlock()

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("query after store replacement: %v", got.err)
		}
		if got.height != 0 {
			t.Fatalf("unexpected latest checkpoint height: got %d want 0", got.height)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("query did not resume after store replacement")
	}
}
