package certchain

import (
	"bytes"
	"fmt"
	"testing"

	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

type stubTracker struct {
	certs map[uint64][]byte
}

func (s stubTracker) GetCertificate(height uint64) ([]byte, bool) {
	data, ok := s.certs[height]
	return data, ok
}

func mustMarshalCert(t *testing.T, height, prev uint64, stateRoot []byte) []byte {
	t.Helper()
	raw, err := proto.Marshal(&pb.CheckpointCertificate{
		Height:         height,
		PrevCheckpoint: prev,
		StateRoot:      stateRoot,
	})
	if err != nil {
		t.Fatalf("marshal cert: %v", err)
	}
	return raw
}

func localCertChain(t *testing.T, interval, last uint64) map[uint64][]byte {
	t.Helper()
	certs := make(map[uint64][]byte, last/interval)
	for h := interval; h <= last; h += interval {
		certs[h] = mustMarshalCert(t, h, h-interval, []byte(fmt.Sprintf("root-%d", h)))
	}
	return certs
}

func mustMarshalSignedCert(t *testing.T, cert *pb.CheckpointCertificate) []byte {
	t.Helper()
	raw, err := proto.Marshal(cert)
	if err != nil {
		t.Fatalf("marshal signed cert: %v", err)
	}
	return raw
}

func TestFindLocalCertifiedHeightFallsBackToPreviousCheckpoint(t *testing.T) {
	wantRoot := []byte("root-60")
	tracker := stubTracker{certs: localCertChain(t, 5, 60)}

	gotHeight, gotRoot, found := FindLocalCertifiedHeight(tracker, 65, 61, 5)
	if !found {
		t.Fatal("expected to find local certified checkpoint")
	}
	if gotHeight != 60 {
		t.Fatalf("expected height 60, got %d", gotHeight)
	}
	if !bytes.Equal(gotRoot, wantRoot) {
		t.Fatalf("expected state root %q, got %q", wantRoot, gotRoot)
	}
}

func TestFindLocalCertifiedHeightKeepsCheckpointFloorInclusive(t *testing.T) {
	wantRoot := []byte("root-55")
	tracker := stubTracker{certs: localCertChain(t, 5, 55)}

	gotHeight, gotRoot, found := FindLocalCertifiedHeight(tracker, 60, 56, 5)
	if !found {
		t.Fatal("expected to find aligned checkpoint fallback")
	}
	if gotHeight != 55 {
		t.Fatalf("expected height 55, got %d", gotHeight)
	}
	if !bytes.Equal(gotRoot, wantRoot) {
		t.Fatalf("expected state root %q, got %q", wantRoot, gotRoot)
	}
}

func TestFindLocalCertifiedHeightRejectsStandaloneCheckpoint(t *testing.T) {
	tracker := stubTracker{certs: map[uint64][]byte{
		60: mustMarshalCert(t, 60, 55, []byte("root-60")),
	}}
	if height, _, found := FindLocalCertifiedHeight(tracker, 65, 61, 5); found {
		t.Fatalf("standalone local checkpoint must be rejected, got height %d", height)
	}
}

func TestDownloadAndVerifyRejectsStandaloneCertAfterGap(t *testing.T) {
	miners := makeMiners(t, 3)
	cert5 := assembleCert(t, 5, 0, []byte("blk-5"), []byte("root-5"), nil, miners)
	cert10 := assembleCert(t, 10, 5, []byte("blk-10"), []byte("root-10"), nil, miners)
	cert20 := assembleCert(t, 20, 15, []byte("blk-20"), []byte("root-20"), nil, miners)
	raw := [][]byte{
		mustMarshalSignedCert(t, cert20),
		mustMarshalSignedCert(t, cert5),
		mustMarshalSignedCert(t, cert10),
	}

	_, err := DownloadAndVerify([]CertFetcher{
		func(fromHeight, toHeight, interval uint64) ([][]byte, error) { return raw, nil },
	}, minerSetMap(miners), 20, 5, 0.667)
	if err == nil {
		t.Fatal("expected certificate gap to be rejected")
	}
}

func TestDownloadAndVerifyChoosesCompleteChainFromNextPeer(t *testing.T) {
	miners := makeMiners(t, 3)
	cert5 := assembleCert(t, 5, 0, []byte("blk-5"), []byte("root-5"), nil, miners)
	cert10 := assembleCert(t, 10, 5, []byte("blk-10"), []byte("root-10"), nil, miners)
	cert15 := assembleCert(t, 15, 10, []byte("blk-15"), []byte("root-15"), nil, miners)
	cert20 := assembleCert(t, 20, 15, []byte("blk-20"), []byte("root-20"), nil, miners)
	broken := [][]byte{
		mustMarshalSignedCert(t, cert20),
		mustMarshalSignedCert(t, cert5),
		mustMarshalSignedCert(t, cert10),
	}
	complete := [][]byte{
		mustMarshalSignedCert(t, cert15),
		mustMarshalSignedCert(t, cert5),
		mustMarshalSignedCert(t, cert20),
		mustMarshalSignedCert(t, cert10),
	}

	result, err := DownloadAndVerify([]CertFetcher{
		func(fromHeight, toHeight, interval uint64) ([][]byte, error) { return broken, nil },
		func(fromHeight, toHeight, interval uint64) ([][]byte, error) { return complete, nil },
	}, minerSetMap(miners), 20, 5, 0.667)
	if err != nil {
		t.Fatalf("DownloadAndVerify failed: %v", err)
	}
	if result.VerifiedHeight != 20 {
		t.Fatalf("verified height=%d want 20", result.VerifiedHeight)
	}
	if !bytes.Equal(result.StateRoot, []byte("root-20")) {
		t.Fatalf("state root=%q want root-20", result.StateRoot)
	}
	if len(result.Certs) != 4 || len(result.RawCerts) != 4 {
		t.Fatalf("verified cert count=%d raw=%d want 4", len(result.Certs), len(result.RawCerts))
	}
	for i, cert := range result.Certs {
		if want := uint64((i + 1) * 5); cert.Height != want {
			t.Fatalf("cert[%d] height=%d want %d", i, cert.Height, want)
		}
	}
	store := newMemoryCertStore()
	PersistCerts(store, result)
	for h := uint64(5); h <= 20; h += 5 {
		if raw, err := store.Get(keys.KeyCertCertificate(h)); err != nil || len(raw) == 0 {
			t.Fatalf("certificate %d was not persisted: %v", h, err)
		}
	}
}
