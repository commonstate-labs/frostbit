package adapters

import (
	"bytes"
	"crypto/rand"
	"errors"
	"strings"
	"sync"
	"testing"

	"dex/keys"
)

                                                         
                                       
                                                                
                                                                   
                                                        
                                  
                                                                                            

                                                                                                   
                                                                                              
type fakeNodeLocalWriter struct {
	mu           sync.Mutex
	data         map[string]string
	getErr       func(key string) error
	flushCalled  int
	flushErrOnce error
	ops          []string
}

func newFakeWriter() *fakeNodeLocalWriter {
	return &fakeNodeLocalWriter{data: make(map[string]string)}
}

func (f *fakeNodeLocalWriter) Get(key string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		if err := f.getErr(key); err != nil {
			return nil, err
		}
	}
	v, ok := f.data[key]
	if !ok {
		return nil, errors.New("key not found")
	}
	return []byte(v), nil
}

func (f *fakeNodeLocalWriter) EnqueueSet(key, value string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data[key] = value
	f.ops = append(f.ops, "set:"+key)
}

func (f *fakeNodeLocalWriter) EnqueueDel(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.data, key)
	f.ops = append(f.ops, "del:"+key)
}

func (f *fakeNodeLocalWriter) Scan(prefix string) (map[string][]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string][]byte)
	for k, v := range f.data {
		if strings.HasPrefix(k, prefix) {
			out[k] = []byte(v)
		}
	}
	return out, nil
}

func (f *fakeNodeLocalWriter) ForceFlush() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.flushCalled++
	f.ops = append(f.ops, "flush")
	if f.flushErrOnce != nil {
		err := f.flushErrOnce
		f.flushErrOnce = nil
		return err
	}
	return nil
}

func testShareKey() string {
	return keys.KeyFrostLocalShare("btc", 1, 1)
}

func legacyBlobForTest(t *testing.T, c *shareCipher, plain []byte) []byte {
	t.Helper()
	gcm, err := c.ensure()
	if err != nil {
		t.Fatalf("initialize legacy cipher: %v", err)
	}
	nonce := make([]byte, shareNonceLen)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatalf("generate legacy nonce: %v", err)
	}
	sealed := gcm.Seal(nil, nonce, plain, nil)
	blob := append(nonce, sealed...)
	clear(sealed)
	return blob
}

func TestShareCipher_EncryptDecryptRoundTrip(t *testing.T) {
	w := newFakeWriter()
	c := newShareCipher(w)

	plain := []byte("frost share secret bytes")
	key := testShareKey()
	blob, err := c.encrypt(key, plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if len(blob) <= shareBlobHeaderLen+shareNonceLen+shareTagLen {
		t.Fatalf("blob too short: %d", len(blob))
	}
	got, err := c.decrypt(key, blob)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("decrypted mismatch: got=%q want=%q", got, plain)
	}
}

                                                                                                   
                                          
func TestShareCipher_WrapKeyPersistsAcrossInstances(t *testing.T) {
	w := newFakeWriter()
	c1 := newShareCipher(w)

	plain := []byte("persistence-probe")
	key := testShareKey()
	blob, err := c1.encrypt(key, plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	                                                     
	stored, err := w.Get(keys.KeyFrostLocalShareWrapKey())
	if err != nil {
		t.Fatalf("wrap key should be persisted: %v", err)
	}
	if len(stored) != shareWrapKeyLen {
		t.Fatalf("wrap key length=%d want %d", len(stored), shareWrapKeyLen)
	}
	if w.flushCalled == 0 {
		t.Fatal("ForceFlush should be invoked when wrap key is first written")
	}

	                                                                        
	c2 := newShareCipher(w)
	got, err := c2.decrypt(key, blob)
	if err != nil {
		t.Fatalf("decrypt with reloaded wrap key: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatal("decrypted plaintext mismatch after wrap key reload")
	}
}

                                                                                                                  
func TestShareCipher_NonceUniqueness(t *testing.T) {
	w := newFakeWriter()
	c := newShareCipher(w)
	plain := []byte("same-plain-different-nonce")

	key := testShareKey()
	b1, err := c.encrypt(key, plain)
	if err != nil {
		t.Fatalf("encrypt 1: %v", err)
	}
	b2, err := c.encrypt(key, plain)
	if err != nil {
		t.Fatalf("encrypt 2: %v", err)
	}
	nonceStart := shareBlobHeaderLen
	if bytes.Equal(b1[nonceStart:nonceStart+shareNonceLen], b2[nonceStart:nonceStart+shareNonceLen]) {
		t.Fatal("nonce reuse detected — AES-GCM nonce must be unique per call")
	}
	if bytes.Equal(b1, b2) {
		t.Fatal("ciphertext deterministic — should differ due to nonce randomness")
	}
}

func TestShareCipher_BindsCiphertextToDomainAndCompleteStorageKey(t *testing.T) {
	w := newFakeWriter()
	c := newShareCipher(w)
	tests := []struct {
		name string
		from string
		to   string
	}{
		{
			name: "canonical chain",
			from: keys.KeyFrostLocalShare("btc", 1, 1),
			to:   keys.KeyFrostLocalShare("eth", 1, 1),
		},
		{
			name: "canonical vault",
			from: keys.KeyFrostLocalShare("btc", 1, 1),
			to:   keys.KeyFrostLocalShare("btc", 2, 1),
		},
		{
			name: "canonical epoch",
			from: keys.KeyFrostLocalShare("btc", 1, 1),
			to:   keys.KeyFrostLocalShare("btc", 1, 2),
		},
		{
			name: "pending session",
			from: keys.KeyFrostPendingLocalShare("btc", 1, 1, "session-a"),
			to:   keys.KeyFrostPendingLocalShare("btc", 1, 1, "session-b"),
		},
		{
			name: "reveal receiver",
			from: keys.KeyFrostLocalDkgReveal("btc", 1, 1, "session-a", 0),
			to:   keys.KeyFrostLocalDkgReveal("btc", 1, 1, "session-a", 1),
		},
		{
			name: "record domain",
			from: keys.KeyFrostLocalShare("btc", 1, 1),
			to:   keys.KeyFrostPendingLocalShare("btc", 1, 1, "session-a"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			blob, err := c.encrypt(tc.from, []byte("bound-secret"))
			if err != nil {
				t.Fatalf("encrypt: %v", err)
			}
			if _, err := c.decrypt(tc.from, blob); err != nil {
				t.Fatalf("decrypt under original key: %v", err)
			}
			if _, err := c.decrypt(tc.to, blob); err == nil {
				t.Fatalf("ciphertext moved from %q to %q must fail authentication", tc.from, tc.to)
			}
		})
	}
}

func TestDBLocalShareStoreRejectsRelocatedCiphertextForEverySecretFamily(t *testing.T) {
	w := newFakeWriter()
	store := NewDBLocalShareStore(w)

	if err := store.SaveLocalShare("btc", 1, 1, []byte{1}); err != nil {
		t.Fatal(err)
	}
	canonicalFrom := keys.KeyFrostLocalShare("btc", 1, 1)
	canonicalTo := keys.KeyFrostLocalShare("btc", 1, 2)
	canonicalBlob, _ := w.Get(canonicalFrom)
	w.EnqueueSet(canonicalTo, string(canonicalBlob))
	if _, err := store.LoadLocalShare("btc", 1, 2); err == nil {
		t.Fatal("canonical share moved to another epoch must fail authentication")
	}

	if err := store.SavePendingLocalShare("btc", 1, 3, "session-a", []byte{2}); err != nil {
		t.Fatal(err)
	}
	pendingFrom := keys.KeyFrostPendingLocalShare("btc", 1, 3, "session-a")
	pendingTo := keys.KeyFrostPendingLocalShare("btc", 1, 3, "session-b")
	pendingBlob, _ := w.Get(pendingFrom)
	w.EnqueueSet(pendingTo, string(pendingBlob))
	if _, err := store.LoadPendingLocalShare("btc", 1, 3, "session-b"); err == nil {
		t.Fatal("pending share moved to another session must fail authentication")
	}

	if err := store.SaveDkgRevealMaterial("btc", 1, 4, "session-c", 0, []byte{3}, []byte{4}); err != nil {
		t.Fatal(err)
	}
	revealFrom := keys.KeyFrostLocalDkgReveal("btc", 1, 4, "session-c", 0)
	revealTo := keys.KeyFrostLocalDkgReveal("btc", 1, 4, "session-c", 1)
	revealBlob, _ := w.Get(revealFrom)
	w.EnqueueSet(revealTo, string(revealBlob))
	if _, _, err := store.LoadDkgRevealMaterial("btc", 1, 4, "session-c", 1); err == nil {
		t.Fatal("reveal material moved to another receiver must fail authentication")
	}
}

func TestShareCipher_RejectsUnsupportedStorageKeyAndBlobVersion(t *testing.T) {
	w := newFakeWriter()
	c := newShareCipher(w)
	if _, err := c.encrypt("unrelated-node-local-key", []byte("secret")); err == nil {
		t.Fatal("unrecognized node-local key family must be rejected")
	}
	key := testShareKey()
	blob, err := c.encrypt(key, []byte("secret"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	blob[len(shareBlobMagic)]++
	if _, err := c.decrypt(key, blob); err == nil {
		t.Fatal("unknown blob version must be rejected")
	}
}

                                                         
func TestShareCipher_TamperingRejected(t *testing.T) {
	w := newFakeWriter()
	c := newShareCipher(w)
	plain := []byte("tamper-probe")
	key := testShareKey()
	blob, err := c.encrypt(key, plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	cases := []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{"flip last tag byte", func(b []byte) []byte {
			out := append([]byte{}, b...)
			out[len(out)-1] ^= 0x01
			return out
		}},
		{"flip first ciphertext byte", func(b []byte) []byte {
			out := append([]byte{}, b...)
			out[shareBlobHeaderLen+shareNonceLen] ^= 0x80
			return out
		}},
		{"flip nonce", func(b []byte) []byte {
			out := append([]byte{}, b...)
			out[shareBlobHeaderLen] ^= 0xFF
			return out
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tampered := tc.mutate(blob)
			if _, err := c.decrypt(key, tampered); err == nil {
				t.Fatal("tampered blob must not decrypt")
			}
		})
	}
}

func TestShareCipher_RejectsEmptyAndShortBlob(t *testing.T) {
	w := newFakeWriter()
	c := newShareCipher(w)

	key := testShareKey()
	if _, err := c.encrypt(key, nil); err == nil {
		t.Fatal("empty plaintext must be rejected")
	}
	if _, err := c.encrypt(key, []byte{}); err == nil {
		t.Fatal("empty plaintext must be rejected")
	}
	if _, err := c.decrypt(key, make([]byte, shareBlobHeaderLen+shareNonceLen+shareTagLen-1)); err == nil {
		t.Fatal("blob shorter than nonce+tag must be rejected")
	}
}

                                                                                                                  
func TestShareCipher_DecryptFailsWithDifferentWrapKey(t *testing.T) {
	w1 := newFakeWriter()
	c1 := newShareCipher(w1)

	plain := []byte("isolation-probe")
	key := testShareKey()
	blob, err := c1.encrypt(key, plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	                                                         
	w2 := newFakeWriter()
	c2 := newShareCipher(w2)
	if _, err := c2.decrypt(key, blob); err == nil {
		t.Fatal("decrypt with a different wrap key must fail")
	}
}

                                                        
func TestShareCipher_NilDBManager(t *testing.T) {
	c := newShareCipher(nil)
	if _, err := c.encrypt(testShareKey(), []byte("x")); err == nil {
		t.Fatal("nil dbManager must be rejected with error, not panic")
	}
}

func TestShareCipher_ExternalFreshStorePersistsOnlyFingerprintMarker(t *testing.T) {
	w := newFakeWriter()
	wrapKey := bytes.Repeat([]byte{0x31}, shareWrapKeyLen)

	store, err := NewDBLocalShareStoreWithExternalWrapKey(w, wrapKey)
	if err != nil {
		t.Fatalf("create external store: %v", err)
	}
	clear(wrapKey)

	marker, err := w.Get(keys.KeyFrostLocalShareWrapExternalMarker())
	if err != nil {
		t.Fatalf("external marker missing: %v", err)
	}
	if len(marker) != externalWrapMarkerLen || marker[0] != externalWrapMarkerVersion {
		t.Fatalf("invalid external marker: len=%d", len(marker))
	}
	if _, err := w.Get(keys.KeyFrostLocalShareWrapKey()); err == nil {
		t.Fatal("external mode must not persist the wrap key")
	}

	plain := []byte("external-store-probe")
	key := testShareKey()
	blob, err := store.cipher.encrypt(key, plain)
	if err != nil {
		t.Fatalf("encrypt after caller key clear: %v", err)
	}
	got, err := store.cipher.decrypt(key, blob)
	if err != nil {
		t.Fatalf("decrypt after caller key clear: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatal("external cipher round trip mismatch")
	}
}

func TestShareCipher_ExternalMigrationValidatesAllThenMarksBeforeDeletingLegacyKey(t *testing.T) {
	w := newFakeWriter()
	legacy := newShareCipher(w)

	shareKey := keys.KeyFrostLocalShare("btc", 1, 1)
	pendingKey := keys.KeyFrostPendingLocalShare("btc", 1, 1, "session-1")
	revealKey := keys.KeyFrostLocalDkgReveal("btc", 1, 1, "session-1", 0)
	shareBlob := legacyBlobForTest(t, legacy, []byte("local-share"))
	pendingBlob := legacyBlobForTest(t, legacy, []byte("pending-share"))
	revealBlob := legacyBlobForTest(t, legacy, []byte("dkg-reveal"))
	w.EnqueueSet(shareKey, string(shareBlob))
	w.EnqueueSet(pendingKey, string(pendingBlob))
	w.EnqueueSet(revealKey, string(revealBlob))
	if err := w.ForceFlush(); err != nil {
		t.Fatalf("flush setup: %v", err)
	}
	legacyKey, err := w.Get(keys.KeyFrostLocalShareWrapKey())
	if err != nil {
		t.Fatalf("read legacy key: %v", err)
	}

	w.mu.Lock()
	w.ops = nil
	w.mu.Unlock()
	external, err := newShareCipherWithExternalWrapKey(w, legacyKey)
	if err != nil {
		t.Fatalf("migrate matching legacy key: %v", err)
	}
	for key, want := range map[string]string{
		shareKey: "local-share", pendingKey: "pending-share", revealKey: "dkg-reveal",
	} {
		migrated, getErr := w.Get(key)
		if getErr != nil {
			t.Fatalf("read migrated %s: %v", key, getErr)
		}
		if !bytes.HasPrefix(migrated, []byte(shareBlobMagic)) {
			t.Fatalf("record %s was not upgraded to the versioned format", key)
		}
		plain, decryptErr := external.decrypt(key, migrated)
		if decryptErr != nil {
			t.Fatalf("decrypt migrated %s: %v", key, decryptErr)
		}
		if string(plain) != want {
			t.Fatalf("migrated plaintext %s=%q want=%q", key, plain, want)
		}
	}
	if _, err := w.Get(keys.KeyFrostLocalShareWrapKey()); err == nil {
		t.Fatal("legacy wrap key must be deleted after migration")
	}
	marker, err := w.Get(keys.KeyFrostLocalShareWrapExternalMarker())
	if err != nil {
		t.Fatalf("external marker missing: %v", err)
	}
	if err := validateExternalWrapMarker(marker, legacyKey); err != nil {
		t.Fatalf("marker fingerprint mismatch: %v", err)
	}

	w.mu.Lock()
	gotOps := append([]string(nil), w.ops...)
	w.mu.Unlock()
	wantOps := []string{
		"set:" + revealKey,
		"set:" + pendingKey,
		"set:" + shareKey,
		"flush",
		"set:" + keys.KeyFrostLocalShareWrapExternalMarker(),
		"flush",
		"del:" + keys.KeyFrostLocalShareWrapKey(),
		"flush",
	}
	if !equalStrings(gotOps, wantOps) {
		t.Fatalf("migration operation order=%v want=%v", gotOps, wantOps)
	}
}

func TestShareCipher_InternalStartupMigratesMixedLegacyRecords(t *testing.T) {
	w := newFakeWriter()
	initial := newShareCipher(w)
	if _, err := initial.ensure(); err != nil {
		t.Fatalf("initialize wrap key: %v", err)
	}

	shareKey := keys.KeyFrostLocalShare("btc", 1, 1)
	pendingKey := keys.KeyFrostPendingLocalShare("btc", 1, 1, "session-a")
	revealKey := keys.KeyFrostLocalDkgReveal("btc", 1, 1, "session-a", 0)
	alreadyVersionedKey := keys.KeyFrostLocalShare("eth", 2, 3)
	w.EnqueueSet(shareKey, string(legacyBlobForTest(t, initial, []byte("share"))))
	w.EnqueueSet(pendingKey, string(legacyBlobForTest(t, initial, []byte("pending"))))
	w.EnqueueSet(revealKey, string(legacyBlobForTest(t, initial, []byte("reveal"))))
	alreadyVersioned, err := initial.encrypt(alreadyVersionedKey, []byte("current"))
	if err != nil {
		t.Fatalf("encrypt current record: %v", err)
	}
	w.EnqueueSet(alreadyVersionedKey, string(alreadyVersioned))
	w.ForceFlush()

	w.mu.Lock()
	w.ops = nil
	w.mu.Unlock()
	restarted := newShareCipher(w)
	if _, err := restarted.ensure(); err != nil {
		t.Fatalf("migrate internal store: %v", err)
	}

	for key, want := range map[string]string{
		shareKey: "share", pendingKey: "pending", revealKey: "reveal", alreadyVersionedKey: "current",
	} {
		blob, getErr := w.Get(key)
		if getErr != nil {
			t.Fatalf("read %s: %v", key, getErr)
		}
		if !bytes.HasPrefix(blob, []byte(shareBlobMagic)) {
			t.Fatalf("record %s is still legacy", key)
		}
		plain, decryptErr := restarted.decrypt(key, blob)
		if decryptErr != nil || string(plain) != want {
			t.Fatalf("record %s after migration: plain=%q err=%v", key, plain, decryptErr)
		}
	}
	gotCurrent, _ := w.Get(alreadyVersionedKey)
	if !bytes.Equal(gotCurrent, alreadyVersioned) {
		t.Fatal("already-versioned record must not be re-encrypted")
	}
	if w.flushCalled == 0 {
		t.Fatal("legacy replacements must be force-flushed")
	}
}

func TestDBLocalShareStoreLazyReadMigratesBeforeFetchingRecord(t *testing.T) {
	w := newFakeWriter()
	legacy := newShareCipher(w)
	key := keys.KeyFrostLocalShare("btc", 7, 9)
	w.EnqueueSet(key, string(legacyBlobForTest(t, legacy, []byte{5})))
	w.ForceFlush()

	restarted := NewDBLocalShareStore(w)
	share, err := restarted.LoadLocalShare("btc", 7, 9)
	if err != nil || !bytes.Equal(share, []byte{5}) {
		t.Fatalf("lazy legacy read after migration: share=%x err=%v", share, err)
	}
	migrated, err := w.Get(key)
	if err != nil || !bytes.HasPrefix(migrated, []byte(shareBlobMagic)) {
		t.Fatal("lazy read did not persist the versioned blob")
	}
}

func TestShareCipher_ExternalMigrationRetriesMixedStateAfterFlushError(t *testing.T) {
	w := newFakeWriter()
	legacy := newShareCipher(w)
	firstKey := keys.KeyFrostLocalShare("btc", 1, 1)
	secondKey := keys.KeyFrostLocalDkgReveal("btc", 1, 1, "session-a", 0)
	w.EnqueueSet(firstKey, string(legacyBlobForTest(t, legacy, []byte("first"))))
	w.EnqueueSet(secondKey, string(legacyBlobForTest(t, legacy, []byte("second"))))
	w.ForceFlush()
	legacyKey, _ := w.Get(keys.KeyFrostLocalShareWrapKey())

	w.mu.Lock()
	w.flushErrOnce = errors.New("injected migration flush failure")
	w.mu.Unlock()
	if _, err := newShareCipherWithExternalWrapKey(w, legacyKey); err == nil {
		t.Fatal("first migration must surface the flush error")
	}
	if _, err := w.Get(keys.KeyFrostLocalShareWrapKey()); err != nil {
		t.Fatal("failed migration must retain the persisted wrap key")
	}
	if _, err := w.Get(keys.KeyFrostLocalShareWrapExternalMarker()); err == nil {
		t.Fatal("failed migration must not install the external marker")
	}

	                                                                           
	                                                                           
	                                                                    
	for _, key := range []string{firstKey, secondKey} {
		blob, _ := w.Get(key)
		if !bytes.HasPrefix(blob, []byte(shareBlobMagic)) {
			t.Fatalf("test did not create mixed/retry state for %s", key)
		}
	}
	external, err := newShareCipherWithExternalWrapKey(w, legacyKey)
	if err != nil {
		t.Fatalf("retry external migration: %v", err)
	}
	for key, want := range map[string]string{firstKey: "first", secondKey: "second"} {
		blob, _ := w.Get(key)
		plain, decryptErr := external.decrypt(key, blob)
		if decryptErr != nil || string(plain) != want {
			t.Fatalf("retry result %s: plain=%q err=%v", key, plain, decryptErr)
		}
	}
}

func TestShareCipher_ExternalMigrationRejectsMismatchedLegacyKeyWithoutMutation(t *testing.T) {
	w := newFakeWriter()
	legacy := newShareCipher(w)
	blob := legacyBlobForTest(t, legacy, []byte("existing-share"))
	w.EnqueueSet(keys.KeyFrostLocalShare("btc", 1, 1), string(blob))
	w.ForceFlush()
	original, _ := w.Get(keys.KeyFrostLocalShareWrapKey())

	wrong := bytes.Repeat([]byte{0x7a}, shareWrapKeyLen)
	if _, err := newShareCipherWithExternalWrapKey(w, wrong); err == nil {
		t.Fatal("mismatched external key must fail")
	}
	stored, err := w.Get(keys.KeyFrostLocalShareWrapKey())
	if err != nil || !bytes.Equal(stored, original) {
		t.Fatal("mismatched migration must retain the legacy key")
	}
	if _, err := w.Get(keys.KeyFrostLocalShareWrapExternalMarker()); err == nil {
		t.Fatal("mismatched migration must not persist a marker")
	}
}

func TestShareCipher_ExternalMigrationRejectsAnyCorruptEncryptedRecord(t *testing.T) {
	w := newFakeWriter()
	legacy := newShareCipher(w)
	shareBlob := legacyBlobForTest(t, legacy, []byte("valid-share"))
	revealBlob := legacyBlobForTest(t, legacy, []byte("valid-reveal"))
	revealBlob[len(revealBlob)-1] ^= 0x01
	w.EnqueueSet(keys.KeyFrostLocalShare("btc", 1, 1), string(shareBlob))
	w.EnqueueSet(keys.KeyFrostLocalDkgReveal("btc", 1, 1, "session-1", 0), string(revealBlob))
	w.ForceFlush()
	legacyKey, _ := w.Get(keys.KeyFrostLocalShareWrapKey())

	if _, err := newShareCipherWithExternalWrapKey(w, legacyKey); err == nil {
		t.Fatal("migration must reject a corrupt reveal record")
	}
	storedShare, err := w.Get(keys.KeyFrostLocalShare("btc", 1, 1))
	if err != nil || !bytes.Equal(storedShare, shareBlob) {
		t.Fatal("all records must be validated before any legacy record is replaced")
	}
	if _, err := w.Get(keys.KeyFrostLocalShareWrapKey()); err != nil {
		t.Fatal("failed validation must retain the legacy key")
	}
	if _, err := w.Get(keys.KeyFrostLocalShareWrapExternalMarker()); err == nil {
		t.Fatal("failed validation must not persist a marker")
	}
}

func TestShareCipher_MissingKeyWithEncryptedRecordsFailsClosed(t *testing.T) {
	w := newFakeWriter()
	legacy := newShareCipher(w)
	shareKey := keys.KeyFrostLocalShare("btc", 1, 1)
	blob, err := legacy.encrypt(shareKey, []byte("orphaned-share"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	legacyKey, _ := w.Get(keys.KeyFrostLocalShareWrapKey())
	w.EnqueueSet(shareKey, string(blob))
	w.EnqueueDel(keys.KeyFrostLocalShareWrapKey())
	w.ForceFlush()

	if _, err := newShareCipherWithExternalWrapKey(w, legacyKey); err == nil {
		t.Fatal("external mode must reject ciphertext without legacy key or marker")
	}
	if _, err := newShareCipher(w).decrypt(shareKey, blob); err == nil {
		t.Fatal("legacy mode must not generate a replacement key over orphaned ciphertext")
	}
	if _, err := w.Get(keys.KeyFrostLocalShareWrapKey()); err == nil {
		t.Fatal("fail-closed path must not generate a new legacy key")
	}
}

func TestShareCipher_ExternalMarkerRequiresMatchingExternalKey(t *testing.T) {
	w := newFakeWriter()
	key := bytes.Repeat([]byte{0x42}, shareWrapKeyLen)
	store, err := NewDBLocalShareStoreWithExternalWrapKey(w, key)
	if err != nil {
		t.Fatalf("create external store: %v", err)
	}
	if err := store.SaveLocalShare("btc", 1, 1, []byte("share")); err != nil {
		t.Fatalf("save local share: %v", err)
	}

	if _, err := newShareCipher(w).encrypt(testShareKey(), []byte("legacy-attempt")); err == nil {
		t.Fatal("legacy mode must fail when the external marker exists")
	}
	if _, err := newShareCipherWithExternalWrapKey(w, key); err != nil {
		t.Fatalf("matching external key should reopen store: %v", err)
	}
	wrong := bytes.Repeat([]byte{0x43}, shareWrapKeyLen)
	if _, err := newShareCipherWithExternalWrapKey(w, wrong); err == nil {
		t.Fatal("marker fingerprint mismatch must fail")
	}
}

func TestDBLocalShareStoreEnsureReadyFailsWithoutRequiredExternalKey(t *testing.T) {
	w := newFakeWriter()
	key := bytes.Repeat([]byte{0x44}, shareWrapKeyLen)
	if _, err := NewDBLocalShareStoreWithExternalWrapKey(w, key); err != nil {
		t.Fatalf("create external store: %v", err)
	}
	if err := NewDBLocalShareStore(w).EnsureReady(); err == nil {
		t.Fatal("legacy startup must fail when the external marker requires an injected key")
	}
	var nilStore *DBLocalShareStore
	if err := nilStore.EnsureReady(); err == nil {
		t.Fatal("nil store must fail readiness validation")
	}
}

func TestShareCipher_RejectsMalformedPersistedLegacyKey(t *testing.T) {
	w := newFakeWriter()
	w.EnqueueSet(keys.KeyFrostLocalShareWrapKey(), "short")
	w.ForceFlush()
	if _, err := newShareCipher(w).encrypt(testShareKey(), []byte("probe")); err == nil {
		t.Fatal("legacy mode must reject malformed persisted key")
	}
	if _, err := newShareCipherWithExternalWrapKey(w, bytes.Repeat([]byte{1}, shareWrapKeyLen)); err == nil {
		t.Fatal("external mode must reject malformed persisted key")
	}
	stored, _ := w.Get(keys.KeyFrostLocalShareWrapKey())
	if stored := string(stored); stored != "short" {
		t.Fatalf("malformed key was overwritten: %q", stored)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
