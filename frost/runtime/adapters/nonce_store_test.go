package adapters

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"testing"

	"dex/frost/runtime/session"
	"dex/keys"
)

type nonceBarrierWriter struct {
	mu          sync.Mutex
	persisted   map[string]string
	pending     map[string]*string
	hotFlushes  int
	fullFlushes int
	flushErr    error
}

func newNonceBarrierWriter() *nonceBarrierWriter {
	return &nonceBarrierWriter{persisted: make(map[string]string), pending: make(map[string]*string)}
}

func (w *nonceBarrierWriter) Get(key string) ([]byte, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	value, exists := w.persisted[key]
	if !exists {
		return nil, errors.New("key not found")
	}
	return []byte(value), nil
}

func (w *nonceBarrierWriter) EnqueueSet(key, value string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	copyValue := value
	w.pending[key] = &copyValue
}

func (w *nonceBarrierWriter) EnqueueDel(key string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending[key] = nil
}

func (w *nonceBarrierWriter) Scan(prefix string) (map[string][]byte, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make(map[string][]byte)
	for key, value := range w.persisted {
		if strings.HasPrefix(key, prefix) {
			out[key] = []byte(value)
		}
	}
	return out, nil
}

func (w *nonceBarrierWriter) ForceFlush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.fullFlushes++
	return w.flushLocked()
}

func (w *nonceBarrierWriter) ForceFlushHot() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.hotFlushes++
	return w.flushLocked()
}

func (w *nonceBarrierWriter) flushLocked() error {
	if w.flushErr != nil {
		return w.flushErr
	}
	for key, value := range w.pending {
		if value == nil {
			delete(w.persisted, key)
		} else {
			w.persisted[key] = *value
		}
	}
	clear(w.pending)
	return nil
}

func TestDBNonceStorePersistsBurnedTombstoneAcrossRestart(t *testing.T) {
	writer := newFakeWriter()
	rawNonce := bytes.Repeat([]byte{0x42}, 32)
	bindingContext := []byte("length-delimited job/task/message context")

	storeBeforeRestart := session.NewSessionStore(NewDBNonceStore(writer))
	if err := storeBeforeRestart.BindNonce(rawNonce, bindingContext, 9); err != nil {
		t.Fatalf("initial durable bind: %v", err)
	}
	if err := storeBeforeRestart.BindNonce(rawNonce, bindingContext, 9); !errors.Is(err, session.ErrNonceAlreadyBound) {
		t.Fatalf("same live-store rebind = %v, want ErrNonceAlreadyBound", err)
	}
	if writer.flushCalled == 0 {
		t.Fatal("nonce binding must be flushed before a commitment can be sent")
	}

	fingerprint := sha256.Sum256(rawNonce)
	wantKey := keys.KeyFrostLocalNonce(hex.EncodeToString(fingerprint[:]))
	if _, err := writer.Get(wantKey); err != nil {
		t.Fatalf("durable tombstone not found at fingerprint key: %v", err)
	}
	if _, ok := writer.data[keys.KeyFrostLocalNonce(hex.EncodeToString(rawNonce))]; ok {
		t.Fatal("raw secret nonce must not be embedded in the persistent key")
	}

	                                                                        
	                                                                        
	storeAfterRestart := session.NewSessionStore(NewDBNonceStore(writer))
	if err := storeAfterRestart.BindNonce(rawNonce, bindingContext, 9); !errors.Is(err, session.ErrNonceAlreadyBound) {
		t.Fatalf("same-context bind after restart = %v, want ErrNonceAlreadyBound", err)
	}
	if err := storeAfterRestart.BindNonce(rawNonce, []byte("different context"), 9); !errors.Is(err, session.ErrNonceAlreadyBound) {
		t.Fatalf("different-context bind after restart = %v, want ErrNonceAlreadyBound", err)
	}
}

func TestDBNonceStoreCorruptionFailsClosed(t *testing.T) {
	writer := newFakeWriter()
	nonce := bytes.Repeat([]byte{0x24}, 32)
	writer.EnqueueSet(nonceStoreKey(nonce), "corrupt")

	store := session.NewSessionStore(NewDBNonceStore(writer))
	if err := store.BindNonce(nonce, []byte("message"), 1); !errors.Is(err, session.ErrNonceAlreadyBound) {
		t.Fatalf("bind over corrupt tombstone = %v, want fail-closed ErrNonceAlreadyBound", err)
	}
}

func TestDBNonceStoreConcurrentInstancesRejectDuplicate(t *testing.T) {
	writer := newFakeWriter()
	stores := []*session.SessionStore{
		session.NewSessionStore(NewDBNonceStore(writer)),
		session.NewSessionStore(NewDBNonceStore(writer)),
	}
	nonce := bytes.Repeat([]byte{0x63}, 32)
	context := []byte("same-context")
	start := make(chan struct{})
	errs := make(chan error, len(stores))

	var wg sync.WaitGroup
	for _, store := range stores {
		store := store
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- store.BindNonce(nonce, context, 3)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	succeeded, rejected := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, session.ErrNonceAlreadyBound):
			rejected++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("concurrent persistent binds: succeeded=%d rejected=%d, want 1/1", succeeded, rejected)
	}
}

func TestDBNonceStoreBatchUsesSingleHotDurabilityBarrier(t *testing.T) {
	writer := newNonceBarrierWriter()
	durable := NewDBNonceStore(writer)
	store := session.NewSessionStore(durable)
	bindings := []session.NonceBinding{
		{Nonce: bytes.Repeat([]byte{0x11}, 32), Message: []byte("job/task/0/hiding"), KeyEpoch: 5},
		{Nonce: bytes.Repeat([]byte{0x12}, 32), Message: []byte("job/task/0/binding"), KeyEpoch: 5},
		{Nonce: bytes.Repeat([]byte{0x21}, 32), Message: []byte("job/task/1/hiding"), KeyEpoch: 5},
		{Nonce: bytes.Repeat([]byte{0x22}, 32), Message: []byte("job/task/1/binding"), KeyEpoch: 5},
	}
	if err := store.BindNonceBatch(bindings); err != nil {
		t.Fatalf("bind durable batch: %v", err)
	}

	writer.mu.Lock()
	if writer.hotFlushes != 1 || writer.fullFlushes != 0 {
		t.Fatalf("durability barriers: hot=%d full=%d, want 1/0", writer.hotFlushes, writer.fullFlushes)
	}
	if len(writer.persisted) != len(bindings) || len(writer.pending) != 0 {
		t.Fatalf("barrier state: persisted=%d pending=%d", len(writer.persisted), len(writer.pending))
	}
	writer.mu.Unlock()

	                                                                     
	fresh := bytes.Repeat([]byte{0x33}, 32)
	freshHash := sha256.Sum256([]byte("fresh"))
	existingHash := sha256.Sum256([]byte("existing"))
	err := durable.SaveNonceBatch([]session.NonceRecord{
		{Nonce: fresh, MsgHash: freshHash[:], KeyEpoch: 5},
		{Nonce: bindings[0].Nonce, MsgHash: existingHash[:], KeyEpoch: 5},
	})
	if !errors.Is(err, session.ErrNonceAlreadyBound) {
		t.Fatalf("batch containing existing nonce = %v, want ErrNonceAlreadyBound", err)
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if len(writer.persisted) != len(bindings) || len(writer.pending) != 0 || writer.hotFlushes != 1 {
		t.Fatalf("rejected batch changed writer: persisted=%d pending=%d hot=%d", len(writer.persisted), len(writer.pending), writer.hotFlushes)
	}
}

func TestDBNonceStoreFlushFailureDoesNotPublishLiveBatch(t *testing.T) {
	writer := newNonceBarrierWriter()
	writer.flushErr = errors.New("injected sync failure")
	store := session.NewSessionStore(NewDBNonceStore(writer))
	bindings := []session.NonceBinding{
		{Nonce: bytes.Repeat([]byte{0x41}, 32), Message: []byte("hiding"), KeyEpoch: 1},
		{Nonce: bytes.Repeat([]byte{0x42}, 32), Message: []byte("binding"), KeyEpoch: 1},
	}
	if err := store.BindNonceBatch(bindings); err == nil || !strings.Contains(err.Error(), "injected sync failure") {
		t.Fatalf("flush failure = %v", err)
	}
	for _, binding := range bindings {
		if store.ValidateNonceForMessage(binding.Nonce, binding.Message) {
			t.Fatal("failed durability barrier published a live nonce")
		}
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if len(writer.persisted) != 0 || writer.hotFlushes != 1 || writer.fullFlushes != 0 {
		t.Fatalf("flush failure state: persisted=%d hot=%d full=%d", len(writer.persisted), writer.hotFlushes, writer.fullFlushes)
	}
}
