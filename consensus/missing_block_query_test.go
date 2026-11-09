package consensus

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"dex/types"
	"dex/utils"
)

func TestPullQueryMissingBlockRepliesValidatedPreferenceWithoutWaiting(t *testing.T) {
	for _, preference := range []string{"known", "", "unknown"} {
		t.Run("preference="+preference, func(t *testing.T) {
			store := NewMemoryBlockStore()
			known := &types.Block{ID: "known", Header: types.BlockHeader{Height: 1, ParentID: "genesis"}}
			if _, err := store.Add(known); err != nil {
				t.Fatal(err)
			}
			transport := &candidateGateTestTransport{}
			qm := &QueryManager{nodeID: "node-1", store: store, transport: transport}
			key := newTestKeyManager(t)
			h := &MessageHandler{
				nodeID: "node-1", store: store, engine: &queryManagerSigEngine{preference: preference},
				transport: transport, queryManager: qm, signer: key, livenessTracker: verifiedParticipantTrackerForTest(),
			}
			                                                                                                          
			for i := uint32(1); i <= 12; i++ {
				nonce := queryManagerTestNonce(byte(i))
				h.handlePullQuery(types.Message{Type: types.MsgPullQuery, From: "node-2", RequestID: i,
					BlockID: "block-1-proposer-w0-missing", Height: 1, SeqID: 2, VRFSeed: []byte("seed"), QueryNonce: nonce})
				chit := transport.lastMessage()
				want := ""
				if preference == known.ID {
					want = known.ID
				}
				if chit.Type != types.MsgChits || chit.PreferredID != want || chit.PreferredIDHeight != 1 {
					t.Fatalf("reply=%+v, want signed preference %q", chit, want)
				}
				digest := queryManagerTestChitDigest(i, "node-2", "node-1", want, 1, "genesis", 0, nonce)
				if !utils.VerifyECDSASignature(key.PublicKeyBytes(), digest, chit.ChitSignature) {
					t.Fatal("Chit lost its original poll/signature binding")
				}
			}
			gets := 0
			for _, msg := range transport.sent {
				if msg.Type == types.MsgGet {
					gets++
				}
			}
			if gets != 1 || len(transport.sent) != 13 {
				t.Fatalf("gets=%d messages=%d, want 1 download and 12 Chits", gets, len(transport.sent))
			}
		})
	}
}

func TestMissingBlockRequestConcurrentDedupAndCooldown(t *testing.T) {
	qm := &QueryManager{store: NewMemoryBlockStore(), transport: &candidateGateTestTransport{}}
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); qm.RequestBlock("block-1-proposer-w0-missing", "relay") }()
	}
	wg.Wait()
	transport := qm.transport.(*candidateGateTestTransport)
	if len(transport.sent) != 1 {
		t.Fatalf("concurrent requests sent %d downloads", len(transport.sent))
	}
	now := time.Now()
	if qm.reserveMissingBlockRequest("block-1-proposer-w0-missing", now) {
		t.Fatal("cooldown was bypassed")
	}
	if !qm.reserveMissingBlockRequest("block-1-proposer-w0-missing", now.Add(missingBlockRetryInterval)) {
		t.Fatal("failed download could not be retried after cooldown")
	}
}

func TestMissingBlockRequestTableBoundedAndRecovers(t *testing.T) {
	qm := &QueryManager{}
	now := time.Now()
	for i := 0; i < maxMissingBlockRequests; i++ {
		if !qm.reserveMissingBlockRequest(fmt.Sprint(i), now) {
			t.Fatalf("early rejection at %d", i)
		}
	}
	if qm.reserveMissingBlockRequest("overflow", now) || len(qm.missingBlockRequests) != maxMissingBlockRequests {
		t.Fatal("unique block IDs bypassed the bounded table")
	}
	if qm.reserveMissingBlockRequest("0", now) {
		t.Fatal("saturation evicted an active cooldown")
	}
	if !qm.reserveMissingBlockRequest("overflow", now.Add(missingBlockRetryInterval)) || len(qm.missingBlockRequests) != 1 {
		t.Fatal("expired entries blocked recovery or were retained")
	}
}

func TestMissingBlockRequestSkipsAvailableBlock(t *testing.T) {
	transport := &candidateGateTestTransport{}
	qm := &QueryManager{store: NewMemoryBlockStore(), transport: transport}
	qm.RequestBlock("genesis", "relay")
	qm.RequestBlock("", "relay")
	qm.RequestBlock("missing", "")
	if len(transport.sent) != 0 || len(qm.missingBlockRequests) != 0 {
		t.Fatal("known/empty block request consumed download capacity")
	}
}

type failedBlockDownloadTransport struct{ candidateGateTestTransport }

func (t *failedBlockDownloadTransport) Send(to types.NodeID, msg types.Message) error {
	_ = t.candidateGateTestTransport.Send(to, msg)
	return fmt.Errorf("block sync service capacity exhausted")
}

func TestMissingBlockRequestFailureDoesNotBypassCooldown(t *testing.T) {
	transport := &failedBlockDownloadTransport{}
	qm := &QueryManager{store: NewMemoryBlockStore(), transport: transport}
	for i := 0; i < 20; i++ {
		qm.RequestBlock("block-1-proposer-w0-missing", types.NodeID(fmt.Sprint(i)))
	}
	if len(transport.sent) != 1 {
		t.Fatalf("busy/failing peer caused %d immediate retries", len(transport.sent))
	}
	qm.missingBlockRequests["block-1-proposer-w0-missing"] = time.Now().Add(-missingBlockRetryInterval)
	qm.RequestBlock("block-1-proposer-w0-missing", "another-peer")
	if len(transport.sent) != 2 {
		t.Fatal("download could not resume after failure cooldown")
	}
}
