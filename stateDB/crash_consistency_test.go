package statedb

import (
	"bytes"
	"errors"
	"testing"
)

var errInjectedStateBatchWrite = errors.New("injected state batch write failure")

type failingStateBatchStore struct {
	kvStore
	failAtMutation int
}

func (s *failingStateBatchStore) Update(fn func(tx kvWriter) error) error {
	return s.kvStore.Update(func(tx kvWriter) error {
		return fn(&failingStateBatchWriter{
			kvWriter:       tx,
			failAtMutation: s.failAtMutation,
		})
	})
}

type failingStateBatchWriter struct {
	kvWriter
	failAtMutation int
	mutations      int
}

func (w *failingStateBatchWriter) failNow() bool {
	w.mutations++
	return w.failAtMutation > 0 && w.mutations == w.failAtMutation
}

func (w *failingStateBatchWriter) Set(key, value []byte) error {
	if w.failNow() {
		return errInjectedStateBatchWrite
	}
	return w.kvWriter.Set(key, value)
}

func (w *failingStateBatchWriter) Delete(key []byte) error {
	if w.failNow() {
		return errInjectedStateBatchWrite
	}
	return w.kvWriter.Delete(key)
}

func TestStateBatchFailureLeavesDurableStateAndFingerprintUnchanged(t *testing.T) {
	db, err := New(testPebbleConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	beforeRoot := append([]byte(nil), db.GetLiveFingerprintBytes()...)
	db.store = &failingStateBatchStore{kvStore: db.store, failAtMutation: 2}
	err = db.ApplyAccountUpdateWithOptions(7, ApplyOptions{BlockID: "block-7"},
		KVUpdate{Key: "v1_account_crash_alice", Value: []byte("alice")},
		KVUpdate{Key: "v1_account_crash_bob", Value: []byte("bob")},
	)
	if !errors.Is(err, errInjectedStateBatchWrite) {
		t.Fatalf("state batch error=%v want injected failure", err)
	}

	for _, key := range []string{"v1_account_crash_alice", "v1_account_crash_bob"} {
		if _, exists, getErr := db.Get(key); getErr != nil || exists {
			t.Fatalf("failed state batch retained %s: exists=%t err=%v", key, exists, getErr)
		}
	}
	if _, exists, err := db.AppliedBlockID(7); err != nil || exists {
		t.Fatalf("failed state batch retained applied block: exists=%t err=%v", exists, err)
	}
	if _, err := db.store.Get(kMetaLastAppliedHeight()); !db.store.IsNotFound(err) {
		t.Fatalf("failed state batch retained height metadata: %v", err)
	}
	afterRoot := db.GetLiveFingerprintBytes()
	if !bytes.Equal(afterRoot, beforeRoot) {
		t.Fatalf("failed state batch changed live fingerprint: before=%x after=%x", beforeRoot[:8], afterRoot[:8])
	}
	requireLiveMatchesStore(t, db)
}

func TestAppliedBlockIdentityCommitsAtomicallyWithState(t *testing.T) {
	db, err := New(testPebbleConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.ApplyAccountUpdateWithOptions(9, ApplyOptions{BlockID: "block-9"},
		KVUpdate{Key: "v1_account_atomic", Value: []byte("committed")},
	); err != nil {
		t.Fatal(err)
	}
	if blockID, exists, err := db.AppliedBlockID(9); err != nil || !exists || blockID != "block-9" {
		t.Fatalf("applied block identity=(%q,%t,%v) want block-9", blockID, exists, err)
	}
	requireLiveMatchesStore(t, db)

	rootBeforeConflict := append([]byte(nil), db.GetLiveFingerprintBytes()...)
	if err := db.ApplyAccountUpdateWithOptions(9, ApplyOptions{BlockID: "conflicting-block-9"},
		KVUpdate{Key: "v1_account_atomic", Value: []byte("must-not-commit")},
	); err == nil {
		t.Fatal("conflicting block identity should fail")
	}
	if blockID, exists, err := db.AppliedBlockID(9); err != nil || !exists || blockID != "block-9" {
		t.Fatalf("conflict changed applied block identity=(%q,%t,%v)", blockID, exists, err)
	}
	if root := db.GetLiveFingerprintBytes(); !bytes.Equal(root, rootBeforeConflict) {
		t.Fatalf("conflicting block changed fingerprint: before=%x after=%x", rootBeforeConflict[:8], root[:8])
	}
	requireLiveMatchesStore(t, db)
}
