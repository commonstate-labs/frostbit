package certchain

import (
	"testing"
	"time"

	"dex/interfaces"
	"dex/keys"
	"dex/pb"
	"dex/utils"

	"google.golang.org/protobuf/proto"
)

type certSignRequest struct {
	height uint64
	digest []byte
	index  uint32
}

func minerSnapshots(t *testing.T, miners []minerKey) []MinerSnapshot {
	t.Helper()
	out := make([]MinerSnapshot, 0, len(miners))
	for _, miner := range miners {
		pub, err := utils.SerializePublicKey(miner.pub)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, MinerSnapshot{
			Index:           miner.idx,
			Address:         miner.addr,
			PubKey:          pub,
			EffectiveWeight: 100,
		})
	}
	return out
}

func activeMinerSnapshots(t *testing.T, miners []minerKey) []interfaces.CertMinerSnapshot {
	t.Helper()
	internal := minerSnapshots(t, miners)
	out := make([]interfaces.CertMinerSnapshot, len(internal))
	for i, miner := range internal {
		out[i] = interfaces.CertMinerSnapshot{
			Index:           miner.Index,
			Address:         miner.Address,
			PublicKey:       append([]byte(nil), miner.PubKey...),
			EffectiveWeight: miner.EffectiveWeight,
		}
	}
	return out
}

func receiveCertSignRequest(t *testing.T, requests <-chan certSignRequest) certSignRequest {
	t.Helper()
	select {
	case request := <-requests:
		return request
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for cert sign request")
		return certSignRequest{}
	}
}

func assertNoCertSignRequest(t *testing.T, requests <-chan certSignRequest) {
	t.Helper()
	select {
	case request := <-requests:
		t.Fatalf("unexpected cert sign request for checkpoint %d", request.height)
	default:
	}
}

func TestTrackerRetriesLocalSignatureUntilObserved(t *testing.T) {
	miners := makeMiners(t, 3)
	tracker := NewTracker(Config{CheckpointInterval: 5, Quorum: 0.667})
	tracker.UpdateMinerSet(minerSnapshots(t, miners))
	requests := make(chan certSignRequest, 4)
	tracker.SetOnDigestReady(miners[0].addr, func(height uint64, digest []byte, index uint32, active bool) {
		if active {
			requests <- certSignRequest{height: height, digest: digest, index: index}
		}
	})

	tracker.OnCheckpointExecuted(5, []byte("block-5"), []byte("state-root-5"), activeMinerSnapshots(t, miners))
	first := receiveCertSignRequest(t, requests)
	if first.height != 5 || first.index != miners[0].idx {
		t.Fatalf("unexpected first request: %+v", first)
	}
	tracker.OnFinalizedHeight(14)
	assertNoCertSignRequest(t, requests)
	tracker.OnFinalizedHeight(15)
	second := receiveCertSignRequest(t, requests)
	if second.height != first.height || string(second.digest) != string(first.digest) || second.index != first.index {
		t.Fatalf("retry changed sign intent: first=%+v second=%+v", first, second)
	}

	sig, err := utils.BLSSignWithCache(miners[0].priv, string(first.digest))
	if err != nil {
		t.Fatal(err)
	}
	if err := tracker.ProcessCertSign(5, miners[0].addr, sig, first.digest); err != nil {
		t.Fatal(err)
	}
	tracker.OnFinalizedHeight(25)
	assertNoCertSignRequest(t, requests)
}

func TestTrackerResumesSignatureRetryAfterRestart(t *testing.T) {
	miners := makeMiners(t, 3)
	store := newMemoryCertStore()
	tracker := NewTracker(Config{CheckpointInterval: 5, Quorum: 0.667})
	tracker.SetStore(store)
	tracker.UpdateMinerSet(minerSnapshots(t, miners))
	tracker.OnCheckpointExecuted(5, []byte("block-5"), []byte("state-root-5"), activeMinerSnapshots(t, miners))

	reloaded := NewTracker(Config{CheckpointInterval: 5, Quorum: 0.667})
	reloaded.SetStore(store)
	requests := make(chan certSignRequest, 1)
	reloaded.SetOnDigestReady(miners[0].addr, func(height uint64, digest []byte, index uint32, active bool) {
		if active {
			requests <- certSignRequest{height: height, digest: digest, index: index}
		}
	})
	request := receiveCertSignRequest(t, requests)
	if request.height != 5 || request.index != miners[0].idx || len(request.digest) == 0 {
		t.Fatalf("restart did not restore pending sign intent: %+v", request)
	}
}

func TestTrackerRejectsCorruptPersistedSignatureAndRetries(t *testing.T) {
	miners := makeMiners(t, 3)
	store := newMemoryCertStore()
	tracker := NewTracker(Config{CheckpointInterval: 5, Quorum: 0.667})
	tracker.SetStore(store)
	tracker.UpdateMinerSet(minerSnapshots(t, miners))
	tracker.OnCheckpointExecuted(5, []byte("block-5"), []byte("state-root-5"), activeMinerSnapshots(t, miners))
	store.EnqueueSet(keys.KeyCertSignature(5, miners[0].idx), "corrupt-signature")

	reloaded := NewTracker(Config{CheckpointInterval: 5, Quorum: 0.667})
	reloaded.SetStore(store)
	requests := make(chan certSignRequest, 1)
	reloaded.SetOnDigestReady(miners[0].addr, func(height uint64, digest []byte, index uint32, active bool) {
		if active {
			requests <- certSignRequest{height: height, digest: digest, index: index}
		}
	})
	if request := receiveCertSignRequest(t, requests); request.height != 5 || request.index != miners[0].idx {
		t.Fatalf("corrupt persisted signature suppressed retry: %+v", request)
	}
}

func TestTrackerAssemblesCertificatesOnlyInHeightOrder(t *testing.T) {
	miners := makeMiners(t, 3)
	store := newMemoryCertStore()
	tracker := NewTracker(Config{CheckpointInterval: 5, Quorum: 0.667})
	tracker.SetStore(store)
	tracker.UpdateMinerSet(minerSnapshots(t, miners))
	tracker.OnCheckpointExecuted(5, []byte("block-5"), []byte("state-root-5"), activeMinerSnapshots(t, miners))
	digest5, _ := tracker.GetPendingCertDigest(5)
	tracker.OnCheckpointExecuted(10, []byte("block-10"), []byte("state-root-10"), activeMinerSnapshots(t, miners))
	digest10, _ := tracker.GetPendingCertDigest(10)

	for _, miner := range miners {
		sig, err := utils.BLSSignWithCache(miner.priv, string(digest10))
		if err != nil {
			t.Fatal(err)
		}
		if err := tracker.ProcessCertSign(10, miner.addr, sig, digest10); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Get(keys.KeyCertCertificate(10)); err == nil {
		t.Fatal("checkpoint 10 assembled across missing checkpoint 5")
	}

	for _, miner := range miners {
		sig, err := utils.BLSSignWithCache(miner.priv, string(digest5))
		if err != nil {
			t.Fatal(err)
		}
		if err := tracker.ProcessCertSign(5, miner.addr, sig, digest5); err != nil {
			t.Fatal(err)
		}
	}
	certs := make([]*pb.CheckpointCertificate, 0, 2)
	for _, height := range []uint64{5, 10} {
		raw, err := store.Get(keys.KeyCertCertificate(height))
		if err != nil {
			t.Fatalf("checkpoint %d was not assembled: %v", height, err)
		}
		var cert pb.CheckpointCertificate
		if err := proto.Unmarshal(raw, &cert); err != nil {
			t.Fatal(err)
		}
		certs = append(certs, &cert)
	}
	if _, _, err := VerifyCertChain(minerSetMap(miners), certs, 0.667, 5); err != nil {
		t.Fatalf("assembled chain does not verify: %v", err)
	}
}

func TestPendingMinerDiffAdvancesSignerSetBeforeAssembly(t *testing.T) {
	miners := makeMiners(t, 4)
	store := newMemoryCertStore()
	tracker := NewTracker(Config{CheckpointInterval: 5, Quorum: 0.667})
	tracker.SetStore(store)
	tracker.UpdateMinerSet(minerSnapshots(t, miners[:3]))
	tracker.OnCheckpointExecuted(5, []byte("block-5"), []byte("state-root-5"), activeMinerSnapshots(t, miners))
	if got := len(tracker.GetPrevMinerPubKeys()); got != 4 {
		t.Fatalf("next signer set size=%d want 4", got)
	}

	reloaded := NewTracker(Config{CheckpointInterval: 5, Quorum: 0.667})
	reloaded.SetStore(store)
	if got := len(reloaded.GetPrevMinerPubKeys()); got != 4 {
		t.Fatalf("reloaded next signer set size=%d want 4", got)
	}
	reloaded.mu.RLock()
	defer reloaded.mu.RUnlock()
	if pending := reloaded.pendingByHeight[5]; pending == nil || len(pending.MinerSet) != 3 {
		t.Fatalf("checkpoint 5 signer set was not frozen before diff: %+v", pending)
	}
}
