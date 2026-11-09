package adapters_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"dex/db"
	"dex/frost/runtime/adapters"
	"dex/frost/runtime/session"
	"dex/keys"
	"dex/logs"
)

func TestDBNonceStoreRealManagerBarrierSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node-db")
	logger := logs.NewNodeLogger("nonce-realdb-test", 0)

	mgr, err := db.NewManager(path, logger)
	if err != nil {
		t.Fatalf("open first db manager: %v", err)
	}
	mgr.InitWriteQueue(128, time.Hour)
	closed := false
	t.Cleanup(func() {
		if !closed {
			mgr.Close()
		}
	})

	bindings := []session.NonceBinding{
		{Nonce: bytes.Repeat([]byte{0x51}, 32), Message: []byte("job/task/hiding"), KeyEpoch: 12},
		{Nonce: bytes.Repeat([]byte{0x52}, 32), Message: []byte("job/task/binding"), KeyEpoch: 12},
	}
	store := session.NewSessionStore(adapters.NewDBNonceStore(mgr))
	if err := store.BindNonceBatch(bindings); err != nil {
		t.Fatalf("bind nonce batch through real db: %v", err)
	}

	                                                                          
	                                                                           
	for _, binding := range bindings {
		fingerprint := sha256.Sum256(binding.Nonce)
		key := keys.KeyFrostLocalNonce(hex.EncodeToString(fingerprint[:]))
		if data, err := mgr.Get(key); err != nil || len(data) == 0 {
			t.Fatalf("tombstone not visible after barrier key=%s len=%d err=%v", key, len(data), err)
		}
	}

	mgr.Close()
	closed = true

	reopened, err := db.NewManager(path, logger)
	if err != nil {
		t.Fatalf("reopen db manager: %v", err)
	}
	reopened.InitWriteQueue(128, time.Hour)
	defer reopened.Close()
	afterRestart := session.NewSessionStore(adapters.NewDBNonceStore(reopened))
	for _, binding := range bindings {
		if err := afterRestart.BindNonce(binding.Nonce, binding.Message, binding.KeyEpoch); !errors.Is(err, session.ErrNonceAlreadyBound) {
			t.Fatalf("restart revived burned nonce: %v", err)
		}
	}
}
