                                      
                            

package session

import (
	"bytes"
	"sync"
	"testing"
)

func TestSessionStore_BindNonce(t *testing.T) {
	store := NewSessionStore(nil)

	nonce := []byte("test_nonce_32_bytes_padding_here")
	msg := []byte("test message")

	                        
	err := store.BindNonce(nonce, msg, 1)
	if err != nil {
		t.Errorf("First bind should succeed: %v", err)
	}

	                                                                                   
	err = store.BindNonce(nonce, msg, 1)
	if err != ErrNonceAlreadyBound {
		t.Errorf("Second bind = %v, want ErrNonceAlreadyBound", err)
	}

	                              
	differentMsg := []byte("different message")
	err = store.BindNonce(nonce, differentMsg, 1)
	if err != ErrNonceAlreadyBound {
		t.Errorf("Expected ErrNonceAlreadyBound, got %v", err)
	}
}

func TestSessionStore_ValidateNonceForMessage(t *testing.T) {
	store := NewSessionStore(nil)

	nonce := []byte("test_nonce_32_bytes_padding_here")
	msg := []byte("test message")

	                                                                  
	if store.ValidateNonceForMessage(nonce, msg) {
		t.Error("Unbound nonce must not validate")
	}

	            
	_ = store.BindNonce(nonce, msg, 1)

	                           
	if !store.ValidateNonceForMessage(nonce, msg) {
		t.Error("Bound nonce should be valid for same message")
	}

	                              
	differentMsg := []byte("different message")
	if store.ValidateNonceForMessage(nonce, differentMsg) {
		t.Error("Bound nonce should not be valid for different message")
	}
}

func TestSessionStore_GetNonceBind(t *testing.T) {
	store := NewSessionStore(nil)

	nonce := []byte("test_nonce_32_bytes_padding_here")
	msg := []byte("test message")

	                              
	_, _, exists := store.GetNonceBind(nonce)
	if exists {
		t.Error("Unbound nonce should not exist")
	}

	                                 
	_ = store.BindNonce(nonce, msg, 42)

	msgHash, keyEpoch, exists := store.GetNonceBind(nonce)
	if !exists {
		t.Error("Bound nonce should exist")
	}
	if keyEpoch != 42 {
		t.Errorf("Expected keyEpoch 42, got %d", keyEpoch)
	}

	                                     
	expectedHash := hashMessage(msg)
	if !bytes.Equal(msgHash, expectedHash) {
		t.Error("MsgHash should match")
	}
}

func TestSessionStore_ReleaseNonce(t *testing.T) {
	store := NewSessionStore(nil)

	nonce := []byte("test_nonce_32_bytes_padding_here")
	msg := []byte("test message")

	         
	_ = store.BindNonce(nonce, msg, 1)

	         
	err := store.ReleaseNonce(nonce)
	if err != nil {
		t.Errorf("Release should succeed: %v", err)
	}

	                                   
	_, _, exists := store.GetNonceBind(nonce)
	if !exists {
		t.Error("Released nonce tombstone should still exist")
	}

	                                          
	differentMsg := []byte("different message")
	err = store.BindNonce(nonce, differentMsg, 2)
	if err != ErrNonceAlreadyBound {
		t.Errorf("Bind after release = %v, want ErrNonceAlreadyBound", err)
	}
}

func TestSessionStore_ConcurrentBindNonceIsAtomic(t *testing.T) {
	store := NewSessionStore(nil)
	nonce := []byte("same-nonce-used-by-two-goroutines")
	messages := [][]byte{[]byte("message-a"), []byte("message-b")}

	start := make(chan struct{})
	errs := make(chan error, len(messages))
	var wg sync.WaitGroup
	for _, msg := range messages {
		msg := append([]byte(nil), msg...)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- store.BindNonce(nonce, msg, 1)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	succeeded, rejected := 0, 0
	for err := range errs {
		switch err {
		case nil:
			succeeded++
		case ErrNonceAlreadyBound:
			rejected++
		default:
			t.Fatalf("unexpected bind error: %v", err)
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("concurrent bind results: succeeded=%d rejected=%d, want 1/1", succeeded, rejected)
	}
}

func TestSessionStoreBindNonceBatchIsAllOrNothing(t *testing.T) {
	backing := NewMemoryNonceStore()
	store := NewSessionStore(backing)
	existing := []byte("already-burned-batch-nonce")
	if err := store.BindNonce(existing, []byte("old-context"), 1); err != nil {
		t.Fatalf("seed existing nonce: %v", err)
	}

	fresh := []byte("fresh-batch-nonce-must-not-persist")
	err := store.BindNonceBatch([]NonceBinding{
		{Nonce: fresh, Message: []byte("fresh-context"), KeyEpoch: 2},
		{Nonce: existing, Message: []byte("new-context"), KeyEpoch: 2},
	})
	if err != ErrNonceAlreadyBound {
		t.Fatalf("batch with burned nonce = %v, want ErrNonceAlreadyBound", err)
	}
	if _, _, exists := backing.GetNonceBind(fresh); exists {
		t.Fatal("rejected batch partially persisted a fresh nonce")
	}
	if store.ValidateNonceForMessage(fresh, []byte("fresh-context")) {
		t.Fatal("rejected batch published a fresh live binding")
	}
}

func TestSessionStoreBindNonceBatchRejectsDuplicateScalar(t *testing.T) {
	store := NewSessionStore(nil)
	nonce := []byte("same-scalar-in-one-batch")
	err := store.BindNonceBatch([]NonceBinding{
		{Nonce: nonce, Message: []byte("hiding"), KeyEpoch: 1},
		{Nonce: nonce, Message: []byte("binding"), KeyEpoch: 1},
	})
	if err != ErrNonceAlreadyBound {
		t.Fatalf("duplicate scalar batch = %v, want ErrNonceAlreadyBound", err)
	}
	if _, _, exists := store.GetNonceBind(nonce); exists {
		t.Fatal("duplicate scalar batch wrote a tombstone before rejecting")
	}
}

func TestSessionStore_RestartTreatsExistingBindingAsBurned(t *testing.T) {
	backing := NewMemoryNonceStore()
	nonce := []byte("persisted-nonce-fingerprint")
	msg := []byte("same binding context")

	beforeRestart := NewSessionStore(backing)
	if err := beforeRestart.BindNonce(nonce, msg, 7); err != nil {
		t.Fatalf("initial bind: %v", err)
	}

	afterRestart := NewSessionStore(backing)
	if err := afterRestart.BindNonce(nonce, msg, 7); err != ErrNonceAlreadyBound {
		t.Fatalf("bind after restart = %v, want ErrNonceAlreadyBound", err)
	}
	if afterRestart.ValidateNonceForMessage(nonce, msg) {
		t.Fatal("a restarted SessionStore must not revive an old nonce")
	}
}

func TestTwoLiveSessionStoresCannotShareOneNonce(t *testing.T) {
	backing := NewMemoryNonceStore()
	stores := []*SessionStore{NewSessionStore(backing), NewSessionStore(backing)}
	nonce := []byte("cross-session-store-race-nonce")
	context := []byte("identical-context-does-not-make-cross-store-reuse-idempotent")

	start := make(chan struct{})
	errs := make(chan error, len(stores))
	var wg sync.WaitGroup
	for _, store := range stores {
		store := store
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- store.BindNonce(nonce, context, 1)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	succeeded, rejected := 0, 0
	for err := range errs {
		switch err {
		case nil:
			succeeded++
		case ErrNonceAlreadyBound:
			rejected++
		default:
			t.Fatalf("unexpected bind error: %v", err)
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("cross-store bind results: succeeded=%d rejected=%d, want 1/1", succeeded, rejected)
	}
}

func TestMemoryNonceStore(t *testing.T) {
	store := NewMemoryNonceStore()

	nonce := []byte("test_nonce")
	msgHash := []byte("test_hash")

	         
	err := store.SaveNonce(nonce, msgHash, 100)
	if err != nil {
		t.Errorf("SaveNonce should succeed: %v", err)
	}

	         
	hash, epoch, exists := store.GetNonceBind(nonce)
	if !exists {
		t.Error("Nonce should exist")
	}
	if !bytes.Equal(hash, msgHash) {
		t.Error("Hash should match")
	}
	if epoch != 100 {
		t.Errorf("Expected epoch 100, got %d", epoch)
	}

	                                                              
	err = store.DeleteNonce(nonce)
	if err != nil {
		t.Errorf("DeleteNonce should succeed: %v", err)
	}

	_, _, exists = store.GetNonceBind(nonce)
	if !exists {
		t.Error("DeleteNonce must retain a burned tombstone")
	}
}

func TestMemoryNonceStore_BurnedTombstonesNeverExpire(t *testing.T) {
	store := NewMemoryNonceStore()
	nonce := []byte("old-epoch-burned-nonce")
	if err := store.SaveNonce(nonce, []byte("hash"), 1); err != nil {
		t.Fatalf("save: %v", err)
	}
	if removed := store.CleanupExpired(1_000_000); removed != 0 {
		t.Fatalf("cleanup removed %d burned tombstones", removed)
	}
	if _, _, exists := store.GetNonceBind(nonce); !exists {
		t.Fatal("burned tombstone expired across key epochs")
	}
}
