package certchain

import (
	"errors"
	"strings"
	"testing"

	"dex/keys"
	"dex/pb"
	"dex/utils"

	"google.golang.org/protobuf/proto"
)

type memoryCertStore struct {
	items map[string][]byte
}

func newMemoryCertStore() *memoryCertStore {
	return &memoryCertStore{items: make(map[string][]byte)}
}

func (s *memoryCertStore) Get(key string) ([]byte, error) {
	v, ok := s.items[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return append([]byte(nil), v...), nil
}

func (s *memoryCertStore) EnqueueSet(key, value string) {
	s.items[key] = []byte(value)
}

func (s *memoryCertStore) Scan(prefix string) (map[string][]byte, error) {
	out := make(map[string][]byte)
	for k, v := range s.items {
		if strings.HasPrefix(k, prefix) {
			out[k] = append([]byte(nil), v...)
		}
	}
	return out, nil
}

func TestSyncPendingFromCertificateKeepsVerifiedCheckpointPending(t *testing.T) {
	miners := makeMiners(t, 2)
	store := newMemoryCertStore()
	tracker := NewTracker(Config{CheckpointInterval: 5, Quorum: 0.667})
	tracker.SetStore(store)
	tracker.UpdateMinerSet(minerSnapshots(t, miners))

	cert := assembleCert(t, 80, 75, []byte("block-80"), []byte("root-80"), nil, miners)
	tracker.SyncPendingFromCertificate(cert)

	globalRaw, err := store.Get(keys.KeyCertGlobal())
	if err != nil {
		t.Fatalf("expected cert global state: %v", err)
	}
	var global pb.CertGlobalState
	if err := proto.Unmarshal(globalRaw, &global); err != nil {
		t.Fatalf("unmarshal cert global: %v", err)
	}
	if global.LastCheckpoint != 80 || global.LastAssembledCert != 75 {
		t.Fatalf("expected checkpoint/pending cursor 80/75, got %d/%d", global.LastCheckpoint, global.LastAssembledCert)
	}
	if _, err := store.Get(keys.KeyCertPending()); err != nil {
		t.Fatalf("expected pending checkpoint metadata: %v", err)
	}
	if _, err := store.Get(keys.KeyCertPendingCheckpoint(80)); err != nil {
		t.Fatalf("expected per-height pending checkpoint metadata: %v", err)
	}
	if _, err := store.Get(keys.KeyCertMinerSet(80)); err != nil {
		t.Fatalf("expected miner set snapshot for checkpoint 80: %v", err)
	}

	reloaded := NewTracker(Config{CheckpointInterval: 5, Quorum: 0.667})
	reloaded.SetStore(store)
	if got := len(reloaded.GetPrevMinerPubKeys()); got != 2 {
		t.Fatalf("expected reloaded miner set size 2, got %d", got)
	}
	reloaded.mu.RLock()
	defer reloaded.mu.RUnlock()
	if reloaded.pending == nil || reloaded.pending.Height != 80 || reloaded.lastAssembledCert != 75 {
		t.Fatalf("expected reloaded pending checkpoint 80 with assembled cursor 75")
	}
}

func TestSyncPendingFromCertificateChainRebuildsMinerSet(t *testing.T) {
	miners := makeMiners(t, 4)
	initial := miners[:3]
	joinedPub, err := utils.SerializePublicKey(miners[3].pub)
	if err != nil {
		t.Fatal(err)
	}
	cert5 := assembleCert(t, 5, 0, []byte("block-5"), []byte("root-5"), nil, initial)
	cert10 := assembleCert(t, 10, 5, []byte("block-10"), []byte("root-10"), []MinerDiffEntry{{
		Index: 3, Joined: true, Address: miners[3].addr, PubKey: joinedPub,
	}}, initial)
	result, err := DownloadAndVerify([]CertFetcher{
		func(fromHeight, toHeight, interval uint64) ([][]byte, error) {
			return [][]byte{mustMarshalSignedCert(t, cert10), mustMarshalSignedCert(t, cert5)}, nil
		},
	}, minerSetMap(initial), 10, 5, 0.667)
	if err != nil {
		t.Fatal(err)
	}

	store := newMemoryCertStore()
	tracker := NewTracker(Config{CheckpointInterval: 5, Quorum: 0.667})
	tracker.SetStore(store)
	snapshots := make([]MinerSnapshot, 0, len(initial))
	for _, miner := range initial {
		pub, _ := utils.SerializePublicKey(miner.pub)
		snapshots = append(snapshots, MinerSnapshot{Index: miner.idx, Address: miner.addr, PubKey: pub, EffectiveWeight: 100})
	}
	tracker.UpdateMinerSet(snapshots)
	PersistCerts(store, result)
	if err := tracker.SyncPendingFromCertificateChain(result.Certs); err != nil {
		t.Fatal(err)
	}
	if got := len(tracker.GetPrevMinerPubKeys()); got != 4 {
		t.Fatalf("next miner set size=%d want 4", got)
	}
	tracker.mu.RLock()
	if got := len(tracker.pending.MinerSet); got != 3 {
		tracker.mu.RUnlock()
		t.Fatalf("final cert signer set size=%d want 3", got)
	}
	tracker.mu.RUnlock()

	reloaded := NewTracker(Config{CheckpointInterval: 5, Quorum: 0.667})
	reloaded.SetStore(store)
	if got := len(reloaded.GetGenesisMinerPubKeys()); got != 3 {
		t.Fatalf("reloaded genesis miner set size=%d want 3", got)
	}
	if got := len(reloaded.GetPrevMinerPubKeys()); got != 4 {
		t.Fatalf("reloaded next miner set size=%d want 4", got)
	}
}

func TestTrackerBackfillsGenesisAnchorFromFirstCheckpoint(t *testing.T) {
	miners := makeMiners(t, 3)
	list := &pb.CertMinerSnapshotList{}
	for _, miner := range miners {
		pub, _ := utils.SerializePublicKey(miner.pub)
		list.Miners = append(list.Miners, &pb.CertMinerSnapshot{Index: miner.idx, Address: miner.addr, PubKey: pub, EffectiveWeight: 100})
	}
	raw, err := proto.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	store := newMemoryCertStore()
	store.EnqueueSet(keys.KeyCertMinerSet(5), string(raw))
	tracker := NewTracker(Config{CheckpointInterval: 5, Quorum: 0.667})
	tracker.SetStore(store)
	if got := len(tracker.GetGenesisMinerPubKeys()); got != 3 {
		t.Fatalf("genesis miner set size=%d want 3", got)
	}
	if persisted, err := store.Get(keys.KeyCertMinerSet(0)); err != nil || len(persisted) == 0 {
		t.Fatalf("genesis anchor was not backfilled: %v", err)
	}
}

func TestTrackerRestartBeforeFirstCheckpointRestoresGenesisSignerSet(t *testing.T) {
	miners := makeMiners(t, 3)
	store := newMemoryCertStore()
	tracker := NewTracker(Config{CheckpointInterval: 5, Quorum: 0.667})
	tracker.SetStore(store)
	if err := tracker.UpdateMinerSet(minerSnapshots(t, miners)); err != nil {
		t.Fatal(err)
	}

	reloaded := NewTracker(Config{CheckpointInterval: 5, Quorum: 0.667})
	reloaded.SetStore(store)
	if got := len(reloaded.GetPrevMinerPubKeys()); got != len(miners) {
		t.Fatalf("restored signer set size=%d want %d", got, len(miners))
	}
	reloaded.OnCheckpointExecuted(5, []byte("block-5"), []byte("state-root-5"), activeMinerSnapshots(t, miners))
	if _, ok := reloaded.GetPendingCertDigest(5); !ok {
		t.Fatal("first checkpoint did not start after pre-checkpoint restart")
	}
}

func TestTrackerRefusesToOverwriteGenesisActiveSet(t *testing.T) {
	miners := makeMiners(t, 2)
	tracker := NewTracker(Config{CheckpointInterval: 5, Quorum: 0.667})
	if err := tracker.UpdateMinerSet(minerSnapshots(t, miners[:1])); err != nil {
		t.Fatal(err)
	}
	if err := tracker.UpdateMinerSet(minerSnapshots(t, miners)); err == nil {
		t.Fatal("second genesis Active-set installation unexpectedly succeeded")
	}
	if got := len(tracker.GetPrevMinerPubKeys()); got != 1 {
		t.Fatalf("genesis signer set was overwritten: size=%d", got)
	}
}

func TestLateCertSignAssemblesOldCheckpoint(t *testing.T) {
	store := newMemoryCertStore()
	tracker := NewTracker(Config{CheckpointInterval: 5, Quorum: 0.667})
	tracker.SetStore(store)
	miners := makeMiners(t, 3)
	snapshots := make([]MinerSnapshot, 0, len(miners))
	for _, mk := range miners {
		pub, err := utils.SerializePublicKey(mk.pub)
		if err != nil {
			t.Fatalf("serialize pubkey: %v", err)
		}
		snapshots = append(snapshots, MinerSnapshot{Index: mk.idx, Address: mk.addr, PubKey: pub, EffectiveWeight: 100})
	}
	tracker.UpdateMinerSet(snapshots)

	tracker.OnCheckpointExecuted(5, []byte("block-5"), []byte("root-5"), activeMinerSnapshots(t, miners))
	tracker.mu.RLock()
	digest5 := append([]byte(nil), tracker.pendingByHeight[5].Digest...)
	tracker.mu.RUnlock()

	tracker.OnCheckpointExecuted(10, []byte("block-10"), []byte("root-10"), activeMinerSnapshots(t, miners))
	tracker.mu.RLock()
	_, stillPending := tracker.pendingByHeight[5]
	tracker.mu.RUnlock()
	if !stillPending {
		t.Fatal("checkpoint 5 should remain pending after checkpoint 10")
	}

	for _, mk := range miners {
		sig, err := utils.BLSSignWithCache(mk.priv, string(digest5))
		if err != nil {
			t.Fatalf("sign late cert: %v", err)
		}
		tracker.CollectCertSignTx(12, 5, mk.addr, sig, digest5)
	}
	tracker.ProcessCollectedCertSigns(10, 15)

	raw, err := store.Get(keys.KeyCertCertificate(5))
	if err != nil {
		t.Fatalf("expected late-assembled certificate for checkpoint 5: %v", err)
	}
	var cert pb.CheckpointCertificate
	if err := proto.Unmarshal(raw, &cert); err != nil {
		t.Fatalf("unmarshal cert: %v", err)
	}
	if cert.Height != 5 || cert.SignerCount != 3 {
		t.Fatalf("unexpected cert height/signers: height=%d signers=%d", cert.Height, cert.SignerCount)
	}
	if _, _, err := VerifyCertChain(minerSetMap(miners), []*pb.CheckpointCertificate{&cert}, 0.667, 5); err != nil {
		t.Fatalf("late assembled cert should verify: %v", err)
	}
	tracker.mu.RLock()
	defer tracker.mu.RUnlock()
	if _, exists := tracker.pendingByHeight[5]; exists {
		t.Fatal("checkpoint 5 should be removed from pending after assembly")
	}
	if tracker.pending == nil || tracker.pending.Height != 10 {
		t.Fatalf("latest pending should remain checkpoint 10, got %+v", tracker.pending)
	}
}
