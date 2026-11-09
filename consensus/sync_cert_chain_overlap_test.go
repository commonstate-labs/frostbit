package consensus

import (
	"bytes"
	"dex/certchain"
	"dex/keys"
	"dex/pb"
	"errors"
	"fmt"
	"testing"

	"google.golang.org/protobuf/proto"
)

type certOverlapTestStore struct {
	values map[string][]byte
	writes int
}

func newCertOverlapTestStore() *certOverlapTestStore {
	return &certOverlapTestStore{values: make(map[string][]byte)}
}

func (s *certOverlapTestStore) Get(key string) ([]byte, error) {
	value, ok := s.values[key]
	if !ok {
		return nil, fmt.Errorf("key not found: %s", key)
	}
	return append([]byte(nil), value...), nil
}

func (s *certOverlapTestStore) EnqueueSet(key, value string) {
	s.writes++
	s.values[key] = []byte(value)
}

func (s *certOverlapTestStore) Scan(prefix string) (map[string][]byte, error) {
	result := make(map[string][]byte)
	for key, value := range s.values {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			result[key] = append([]byte(nil), value...)
		}
	}
	return result, nil
}

func (s *certOverlapTestStore) GetCertificate(height uint64) ([]byte, bool) {
	value, ok := s.values[keys.KeyCertCertificate(height)]
	return append([]byte(nil), value...), ok
}

func mustMarshalCertOverlapTest(t *testing.T, cert *pb.CheckpointCertificate) []byte {
	t.Helper()
	raw, err := proto.Marshal(cert)
	if err != nil {
		t.Fatalf("marshal certificate: %v", err)
	}
	return raw
}

func certOverlapTestCertificate(height, prev uint64, blockHash, stateRoot string) *pb.CheckpointCertificate {
	return &pb.CheckpointCertificate{
		Height:         height,
		PrevCheckpoint: prev,
		BlockHash:      []byte(blockHash),
		StateRoot:      []byte(stateRoot),
		MinerCount:     4,
		SignerCount:    3,
		Bitmap:         []byte{0x07},
		AggSig:         []byte("signature-a"),
	}
}

func TestValidateVerifiedCertificateOverlapAcceptsEquivalentSignerSubset(t *testing.T) {
	local := certOverlapTestCertificate(10, 0, "block-10", "root-10")
	remote := proto.Clone(local).(*pb.CheckpointCertificate)
	remote.Bitmap = []byte{0x0e}
	remote.AggSig = []byte("different-valid-aggregate-signature")
	remote.SignerCount = 4

	store := newCertOverlapTestStore()
	store.values[keys.KeyCertCertificate(local.Height)] = mustMarshalCertOverlapTest(t, local)

	if err := validateVerifiedCertificateOverlap(store, local.Height, []*pb.CheckpointCertificate{remote}); err != nil {
		t.Fatalf("equivalent canonical certificate was rejected: %v", err)
	}
	if !bytes.Equal(certchain.CertDigestFromCert(local), certchain.CertDigestFromCert(remote)) {
		t.Fatal("test fixture must differ only in non-canonical signer material")
	}
}

func TestInstallVerifiedCertificateChainRejectsRemoteHigherOverlapConflictBeforeMutation(t *testing.T) {
	local := certOverlapTestCertificate(10, 0, "local-block-10", "local-root-10")
	remoteOverlap := certOverlapTestCertificate(10, 0, "remote-block-10", "remote-root-10")
	remoteHigher := certOverlapTestCertificate(20, 10, "remote-block-20", "remote-root-20")

	store := newCertOverlapTestStore()
	localRaw := mustMarshalCertOverlapTest(t, local)
	store.values[keys.KeyCertCertificate(local.Height)] = append([]byte(nil), localRaw...)
	result := &certchain.DownloadAndVerifyResult{
		VerifiedHeight: remoteHigher.Height,
		StateRoot:      append([]byte(nil), remoteHigher.StateRoot...),
		RawCerts: [][]byte{
			mustMarshalCertOverlapTest(t, remoteOverlap),
			mustMarshalCertOverlapTest(t, remoteHigher),
		},
		Certs: []*pb.CheckpointCertificate{remoteOverlap, remoteHigher},
	}

	sm := &SyncManager{}
	var safetyErr error
	sm.SetSafetyFaultHandler(func(err error) { safetyErr = err })
	flushCalls := 0
	_, err := sm.installVerifiedCertificateChain(
		store,
		local.Height,
		store,
		result,
		func() error {
			flushCalls++
			return nil
		},
	)
	var conflict *certificateOverlapConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected overlap conflict, got %v", err)
	}
	if conflict.Height != local.Height {
		t.Fatalf("conflict height=%d, want %d", conflict.Height, local.Height)
	}
	if !errors.As(safetyErr, &conflict) {
		t.Fatalf("safety fault handler did not receive overlap conflict: %v", safetyErr)
	}
	if store.writes != 0 {
		t.Fatalf("conflicting remote chain queued %d writes", store.writes)
	}
	if flushCalls != 0 {
		t.Fatalf("conflicting remote chain flushed state %d times", flushCalls)
	}
	storedLocal, ok := store.GetCertificate(local.Height)
	if !ok || !bytes.Equal(storedLocal, localRaw) {
		t.Fatal("local certificate changed after remote overlap conflict")
	}
	if _, ok := store.GetCertificate(remoteHigher.Height); ok {
		t.Fatal("higher conflicting remote certificate was persisted")
	}
}
