package adapters

import (
	"encoding/binary"
	"errors"
	"strings"
	"sync"
	"testing"

	"dex/frost/runtime"
	"dex/frost/runtime/types"
	"dex/frost/security"
	"dex/pb"
	"dex/utils"
)

func testRoastIdentity(t testing.TB, privateKey string) *utils.KeyManager {
	t.Helper()
	identity := utils.NewKeyManager()
	if err := identity.InitKey(privateKey); err != nil {
		t.Fatalf("initialize fixture identity: %v", err)
	}
	return identity
}

type durableSequenceWriter struct {
	mu         sync.Mutex
	committed  map[string]string
	pending    map[string]*string
	flushErr   error
	hotFlushes int
}

func newDurableSequenceWriter() *durableSequenceWriter {
	return &durableSequenceWriter{
		committed: make(map[string]string),
		pending:   make(map[string]*string),
	}
}

func (w *durableSequenceWriter) Get(key string) ([]byte, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	value, ok := w.committed[key]
	if !ok {
		return nil, errors.New("key not found")
	}
	return []byte(value), nil
}

func (w *durableSequenceWriter) EnqueueSet(key, value string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	copyValue := value
	w.pending[key] = &copyValue
}

func (w *durableSequenceWriter) EnqueueDel(key string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending[key] = nil
}

func (w *durableSequenceWriter) Scan(prefix string) (map[string][]byte, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	result := make(map[string][]byte)
	for key, value := range w.committed {
		if strings.HasPrefix(key, prefix) {
			result[key] = []byte(value)
		}
	}
	return result, nil
}

func (w *durableSequenceWriter) ForceFlush() error {
	return w.flush(false)
}

func (w *durableSequenceWriter) ForceFlushHot() error {
	return w.flush(true)
}

func (w *durableSequenceWriter) flush(hot bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if hot {
		w.hotFlushes++
	}
	if w.flushErr != nil {
		return w.flushErr
	}
	for key, value := range w.pending {
		if value == nil {
			delete(w.committed, key)
		} else {
			w.committed[key] = *value
		}
	}
	clear(w.pending)
	return nil
}

func TestSenderRoastMessengerPrepareEnvelopeSignsAndSequences(t *testing.T) {
	id := utils.GenerateTestIdentity(1)
	priv, err := utils.ParseECDSAPrivateKey(id.PrivateKeyHex)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}

	store := newDurableSequenceWriter()
	m := NewSenderRoastMessenger(nil, testRoastIdentity(t, id.PrivateKeyHex), store)
	msg := &runtime.RoastEnvelope{
		SessionID: "job-1",
		Kind:      "NonceRequest",
		From:      runtime.NodeID(id.Address),
		Chain:     "btc",
		VaultID:   3,
		SignAlgo:  int32(pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340),
		Epoch:     1,
		Round:     1,
		Payload:   []byte("nonce-request"),
	}

	env1, err := m.prepareEnvelope("receiver-1", msg)
	if err != nil {
		t.Fatalf("prepare envelope: %v", err)
	}
	if env1.To != "receiver-1" {
		t.Fatalf("To = %q, want receiver-1", env1.To)
	}
	if env1.Seq != 1 {
		t.Fatalf("Seq = %d, want 1", env1.Seq)
	}
	if len(env1.Sig) == 0 {
		t.Fatal("signature is empty")
	}
	if !security.VerifyFrostEnvelope(&priv.PublicKey, env1) {
		t.Fatal("signature verification failed")
	}

	env2, err := m.prepareEnvelope("receiver-2", msg)
	if err != nil {
		t.Fatalf("prepare second envelope: %v", err)
	}
	if env2.Seq != 1 {
		t.Fatalf("receiver-2 Seq = %d, want 1", env2.Seq)
	}
	if !security.VerifyFrostEnvelope(&priv.PublicKey, env2) {
		t.Fatal("second signature verification failed")
	}

	env3, err := m.prepareEnvelope("receiver-1", msg)
	if err != nil {
		t.Fatalf("prepare third envelope: %v", err)
	}
	if env3.Seq != 2 {
		t.Fatalf("receiver-1 second Seq = %d, want 2", env3.Seq)
	}
	if !security.VerifyFrostEnvelope(&priv.PublicKey, env3) {
		t.Fatal("third signature verification failed")
	}
	if store.hotFlushes != 2 {
		t.Fatalf("hot durability barriers = %d, want 2", store.hotFlushes)
	}
}

func TestSenderRoastMessengerPrepareEnvelopeRequiresSigner(t *testing.T) {
	m := NewSenderRoastMessenger(nil, nil, newDurableSequenceWriter())
	_, err := m.prepareEnvelope("", &runtime.RoastEnvelope{Kind: "NonceRequest"})
	if err == nil || !strings.Contains(err.Error(), "signer not configured") {
		t.Fatalf("error = %v, want signer not configured", err)
	}
}

type roastIdentityProbe struct {
	types.LocalIdentity
	sign func(*pb.FrostEnvelope) error
}

func (i *roastIdentityProbe) SignFrostEnvelope(env *pb.FrostEnvelope) error {
	return i.sign(env)
}

func TestSenderRoastMessengerUsesSharedIdentityAfterSequenceDurability(t *testing.T) {
	store := newDurableSequenceWriter()
	signErr := errors.New("identity locked")
	calls := 0
	identity := &roastIdentityProbe{sign: func(env *pb.FrostEnvelope) error {
		calls++
		if env.Seq != 1 || env.From != "sender" || env.To != "receiver" || store.hotFlushes != 1 {
			t.Fatalf("identity called before durable envelope context: env=%+v barriers=%d", env, store.hotFlushes)
		}
		return signErr
	}}
	m := NewSenderRoastMessenger(nil, identity, store)
	_, err := m.prepareEnvelope("receiver", &runtime.RoastEnvelope{Kind: "NonceRequest", From: "sender"})
	if !errors.Is(err, signErr) {
		t.Fatalf("identity signing error not propagated: %v", err)
	}
	if m.identity != identity || calls != 1 {
		t.Fatalf("identity reference or call count changed: calls=%d", calls)
	}
}

func TestSenderRoastMessengerSequenceSurvivesReconstruction(t *testing.T) {
	id := utils.GenerateTestIdentity(2)
	store := newDurableSequenceWriter()
	msg := &runtime.RoastEnvelope{
		SessionID: "job-restart",
		Kind:      "NonceRequest",
		From:      runtime.NodeID(id.Address),
		Chain:     "btc",
		VaultID:   1,
		Epoch:     1,
		Round:     1,
		Payload:   []byte("request"),
	}

	beforeRestart := NewSenderRoastMessenger(nil, testRoastIdentity(t, id.PrivateKeyHex), store)
	first, err := beforeRestart.prepareEnvelope("receiver-1", msg)
	if err != nil {
		t.Fatalf("prepare before restart: %v", err)
	}

	afterRestart := NewSenderRoastMessenger(nil, testRoastIdentity(t, id.PrivateKeyHex), store)
	second, err := afterRestart.prepareEnvelope("receiver-1", msg)
	if err != nil {
		t.Fatalf("prepare after restart: %v", err)
	}
	if first.Seq != 1 || second.Seq != roastSequenceRangeSize+1 {
		t.Fatalf("sequences across reconstruction = (%d, %d), want (1, %d)", first.Seq, second.Seq, roastSequenceRangeSize+1)
	}
}

func TestSenderRoastMessengerConcurrentInstancesReserveDifferentRanges(t *testing.T) {
	id := utils.GenerateTestIdentity(7)
	store := newDurableSequenceWriter()
	first := NewSenderRoastMessenger(nil, testRoastIdentity(t, id.PrivateKeyHex), store)
	second := NewSenderRoastMessenger(nil, testRoastIdentity(t, id.PrivateKeyHex), store)
	from := runtime.NodeID(id.Address)
	const receiver = runtime.NodeID("receiver-1")

	start := make(chan struct{})
	sequences := make(chan uint64, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, messenger := range []*SenderRoastMessenger{first, second} {
		wg.Add(1)
		go func(messenger *SenderRoastMessenger) {
			defer wg.Done()
			<-start
			sequence, err := messenger.nextSeq(from, receiver)
			if err != nil {
				errs <- err
				return
			}
			sequences <- sequence
		}(messenger)
	}
	close(start)
	wg.Wait()
	close(errs)
	close(sequences)
	for err := range errs {
		t.Fatalf("concurrent instance allocation: %v", err)
	}

	seen := make(map[uint64]struct{}, 2)
	for sequence := range sequences {
		seen[sequence] = struct{}{}
	}
	for _, want := range []uint64{1, roastSequenceRangeSize + 1} {
		if _, ok := seen[want]; !ok {
			t.Fatalf("concurrent instance sequences = %v, missing %d", seen, want)
		}
	}
	if store.hotFlushes != 2 {
		t.Fatalf("concurrent instance durability barriers = %d, want 2", store.hotFlushes)
	}
}

func TestSenderRoastMessengerRangeBoundaryAndConcurrentAllocation(t *testing.T) {
	id := utils.GenerateTestIdentity(5)
	store := newDurableSequenceWriter()
	messenger := NewSenderRoastMessenger(nil, testRoastIdentity(t, id.PrivateKeyHex), store)
	from := runtime.NodeID(id.Address)
	const receiver = runtime.NodeID("receiver-1")

	for want := uint64(1); want <= roastSequenceRangeSize; want++ {
		got, err := messenger.nextSeq(from, receiver)
		if err != nil {
			t.Fatalf("allocate sequence %d: %v", want, err)
		}
		if got != want {
			t.Fatalf("sequence = %d, want %d", got, want)
		}
	}
	if store.hotFlushes != 1 {
		t.Fatalf("durability barriers for first range = %d, want 1", store.hotFlushes)
	}

	const count = 32
	sequences := make(chan uint64, count)
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sequence, err := messenger.nextSeq(from, receiver)
			if err != nil {
				errs <- err
				return
			}
			sequences <- sequence
		}()
	}
	wg.Wait()
	close(errs)
	close(sequences)
	for err := range errs {
		t.Fatalf("concurrent prepare: %v", err)
	}
	seen := make(map[uint64]struct{}, count)
	for sequence := range sequences {
		if _, duplicate := seen[sequence]; duplicate {
			t.Fatalf("duplicate concurrently allocated sequence %d", sequence)
		}
		seen[sequence] = struct{}{}
	}
	for sequence := roastSequenceRangeSize + 1; sequence <= roastSequenceRangeSize+count; sequence++ {
		if _, ok := seen[sequence]; !ok {
			t.Fatalf("missing concurrently allocated sequence %d", sequence)
		}
	}
	if store.hotFlushes != 2 {
		t.Fatalf("durability barriers after crossing range boundary = %d, want 2", store.hotFlushes)
	}
}

func TestSenderRoastMessengerSequencePersistenceFailureFailsClosed(t *testing.T) {
	id := utils.GenerateTestIdentity(3)
	store := newDurableSequenceWriter()
	store.flushErr = errors.New("disk unavailable")
	m := NewSenderRoastMessenger(nil, testRoastIdentity(t, id.PrivateKeyHex), store)
	msg := &runtime.RoastEnvelope{
		SessionID: "job-flush-failure",
		Kind:      "NonceRequest",
		From:      runtime.NodeID(id.Address),
		Chain:     "btc",
		VaultID:   1,
		Epoch:     1,
		Round:     1,
	}

	env, err := m.prepareEnvelope("receiver-1", msg)
	if err == nil || !strings.Contains(err.Error(), "reserve outbound frost sequence range") {
		t.Fatalf("error = %v, want persistence failure", err)
	}
	if env != nil {
		t.Fatal("persistence failure returned a sendable envelope")
	}

	store.mu.Lock()
	store.flushErr = nil
	store.mu.Unlock()
	retry, err := m.prepareEnvelope("receiver-1", msg)
	if err != nil {
		t.Fatalf("prepare after storage recovery: %v", err)
	}
	if retry.Seq != 1 {
		t.Fatalf("sequence after failed, uncommitted flush = %d, want 1", retry.Seq)
	}
}

func TestSenderRoastMessengerCorruptOrExhaustedSequenceFailsClosed(t *testing.T) {
	id := utils.GenerateTestIdentity(6)
	from := runtime.NodeID(id.Address)
	const receiver = runtime.NodeID("receiver-1")
	key := roastSequenceKey(from, receiver)

	t.Run("corrupt record", func(t *testing.T) {
		store := newDurableSequenceWriter()
		store.committed[key] = "corrupt"
		messenger := NewSenderRoastMessenger(nil, testRoastIdentity(t, id.PrivateKeyHex), store)
		if _, err := messenger.nextSeq(from, receiver); err == nil || !strings.Contains(err.Error(), "invalid outbound frost sequence record") {
			t.Fatalf("error = %v, want corrupt-record failure", err)
		}
		if store.hotFlushes != 0 {
			t.Fatalf("corrupt record triggered %d flushes, want 0", store.hotFlushes)
		}
	})

	t.Run("range overflow", func(t *testing.T) {
		store := newDurableSequenceWriter()
		record := make([]byte, roastSequenceRecordSize)
		record[0] = roastSequenceRecordVersion
		binary.BigEndian.PutUint64(record[1:], ^uint64(0)-roastSequenceRangeSize+1)
		store.committed[key] = string(record)
		messenger := NewSenderRoastMessenger(nil, testRoastIdentity(t, id.PrivateKeyHex), store)
		if _, err := messenger.nextSeq(from, receiver); err == nil || !strings.Contains(err.Error(), "sequence exhausted") {
			t.Fatalf("error = %v, want overflow failure", err)
		}
		if store.hotFlushes != 0 {
			t.Fatalf("overflow triggered %d flushes, want 0", store.hotFlushes)
		}
	})
}

func TestSenderRoastMessengerRequiresSequenceStore(t *testing.T) {
	id := utils.GenerateTestIdentity(4)
	m := NewSenderRoastMessenger(nil, testRoastIdentity(t, id.PrivateKeyHex), nil)
	_, err := m.prepareEnvelope("receiver-1", &runtime.RoastEnvelope{
		Kind: "NonceRequest",
		From: runtime.NodeID(id.Address),
	})
	if err == nil || !strings.Contains(err.Error(), "sequence store not configured") {
		t.Fatalf("error = %v, want sequence store not configured", err)
	}
}
