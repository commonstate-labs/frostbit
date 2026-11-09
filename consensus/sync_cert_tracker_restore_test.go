package consensus

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"dex/certchain"
	"dex/interfaces"
	"dex/keys"
	"dex/pb"
	"dex/utils"
	"fmt"
	"testing"

	"google.golang.org/protobuf/proto"
)

func TestCertificateDiscoveryPreservesExecutingTrackerUntilSnapshotRestore(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := utils.GetPublicKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	pubBytes, err := utils.SerializePublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	miner := certchain.MinerSnapshot{Index: 0, Address: "miner", PubKey: pubBytes, EffectiveWeight: 100}
	active := []interfaces.CertMinerSnapshot{{Index: 0, Address: "miner", PublicKey: pubBytes, EffectiveWeight: 100}}
	newTracker := func(store *certOverlapTestStore) *certchain.Tracker {
		tracker := certchain.NewTracker(certchain.Config{CheckpointInterval: 5, Quorum: 0.667})
		tracker.SetStore(store)
		if err := tracker.UpdateMinerSet([]certchain.MinerSnapshot{miner}); err != nil {
			t.Fatal(err)
		}
		return tracker
	}
	executeCheckpoint := func(tracker *certchain.Tracker, height uint64) []byte {
		tracker.OnCheckpointExecuted(height, []byte(fmt.Sprintf("block-%d", height)), bytes.Repeat([]byte{byte(height)}, 32), active)
		digest, ok := tracker.GetPendingCertDigest(height)
		if !ok {
			t.Fatalf("checkpoint %d did not create a pending digest", height)
		}
		return digest
	}
	signCheckpoint := func(tracker *certchain.Tracker, height uint64, digest []byte) {
		sig, err := utils.BLSSignWithCache(priv, string(digest))
		if err != nil {
			t.Fatal(err)
		}
		if err := tracker.ProcessCertSign(height, "miner", sig, digest); err != nil {
			t.Fatalf("checkpoint %d rejected its valid signature: %v", height, err)
		}
	}
	remote := newTracker(newCertOverlapTestStore())
	var rawCerts [][]byte
	for _, height := range []uint64{5, 10} {
		signCheckpoint(remote, height, executeCheckpoint(remote, height))
		raw, ok := remote.GetCertificate(height)
		if !ok {
			t.Fatalf("remote certificate %d was not assembled", height)
		}
		rawCerts = append(rawCerts, raw)
	}
	result, err := certchain.DownloadAndVerify([]certchain.CertFetcher{func(uint64, uint64, uint64) ([][]byte, error) {
		return rawCerts, nil
	}}, remote.GetGenesisMinerSet(), 10, 5, 0.667)
	if err != nil {
		t.Fatal(err)
	}

	localStore := newCertOverlapTestStore()
	local := newTracker(localStore)
	executeCheckpoint(local, 5)
	before := make(map[string][]byte)
	for _, key := range []string{keys.KeyCertGlobal(), keys.KeyCertPending(), keys.KeyCertPendingCheckpoint(5), keys.KeyCertMinerSet(5)} {
		before[key] = append([]byte(nil), localStore.values[key]...)
	}
	sm := newFrontierCoreSyncManager(newFrontierCoreTestStore("block-5", 5), &frontierCoreTestTransport{}, 2, 2)
	if _, err := sm.installVerifiedCertificateChain(local, 0, localStore, result, nil); err != nil {
		t.Fatal(err)
	}
	assertPreserved := func() {
		t.Helper()
		for key, expected := range before {
			if !bytes.Equal(localStore.values[key], expected) {
				t.Fatalf("certificate discovery or unsuccessful snapshot changed executing state %s", key)
			}
		}
		if _, ok := local.GetPendingCertDigest(10); ok {
			t.Fatal("certificate discovery created a future executing checkpoint")
		}
	}
	assertPreserved()
	local.ReloadFromDB()
	assertPreserved()
	session := sm.startSyncSession(SyncTriggerChit, 10, 5, false)
	sm.updateSyncSession(session, func(s *SyncSession) {
		s.CertifiedHeight, s.CertBlockHash, s.CertStateRoot = 10, "block-10", result.StateRoot
	})
	sm.setSyncSessionStage(session, SyncStageStateSnapshot, "")
	if err := sm.execSnapshotStage(session); err == nil || err.Kind != SyncErrNetwork {
		t.Fatalf("unavailable snapshot must fail as a network error, got %v", err)
	}
	assertPreserved()
	store := sm.store.(*frontierCoreTestStore)
	store.mu.Lock()
	store.lastID, store.lastHeight = "block-10", 10
	store.mu.Unlock()
	if err := sm.execSnapshotStage(session); err != nil || session.Stage != SyncStageDone {
		t.Fatalf("already-reached checkpoint must skip the stale snapshot: stage=%s err=%v", session.Stage, err)
	}
	assertPreserved()

	                                                                          
	                                                                              
	if err := restoreCertTrackerAfterSnapshot(local, 10); err != nil {
		t.Fatal(err)
	}
	local.ReloadFromDB()
	var pending pb.CheckpointCertificate
	if err := proto.Unmarshal(localStore.values[keys.KeyCertPendingCheckpoint(10)], &pending); err != nil ||
		!bytes.Equal(certchain.CertDigestFromCert(&pending), certchain.CertDigestFromCert(result.Certs[1])) {
		t.Fatalf("restart lost the restored checkpoint digest: %v", err)
	}
	if _, ok := local.GetPendingCertDigest(10); ok {
		t.Fatal("a restored, certified checkpoint must not request another signature")
	}
	localNext, remoteNext := executeCheckpoint(local, 15), executeCheckpoint(remote, 15)
	if !bytes.Equal(localNext, remoteNext) {
		t.Fatal("restored tracker generated a different next-checkpoint commitment")
	}
	signCheckpoint(local, 15, localNext)
	if _, ok := local.GetCertificate(15); !ok {
		t.Fatal("restored tracker could not assemble the next certificate")
	}
}

func TestRestoreCertTrackerRejectsMissingHistoryBeforeMutation(t *testing.T) {
	store := newCertOverlapTestStore()
	tracker := certchain.NewTracker(certchain.Config{CheckpointInterval: 5, Quorum: 0.667})
	tracker.SetStore(store)
	before := store.writes
	if err := restoreCertTrackerAfterSnapshot(tracker, 10); err == nil {
		t.Fatal("missing genesis-anchored certificate history must fail closed")
	}
	if store.writes != before {
		t.Fatal("incomplete certificate history changed tracker state")
	}
}
