package handlers

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"dex/config"
	"dex/consensus"
	"dex/pb"
	"dex/utils"

	"google.golang.org/protobuf/proto"
)

type failingPushQueryBody struct{}

func (failingPushQueryBody) Read([]byte) (int, error) { return 0, errors.New("body must not be read") }
func (failingPushQueryBody) Close() error             { return nil }

func TestDecodePushQueryRequestReusesPeerGateMessage(t *testing.T) {
	want := &pb.PushQuery{RequestId: 7, Address: "miner", Container: []byte("large-container")}
	req := httptest.NewRequest("POST", "/pushquery", nil)
	req.Body = failingPushQueryBody{}
	req = req.WithContext(withVerifiedPushQuery(context.Background(), want))

	got, err := decodePushQueryRequest(req)
	if err != nil {
		t.Fatalf("decodePushQueryRequest() error = %v", err)
	}
	if got != want {
		t.Fatal("expected the exact PushQuery parsed and verified by PeerGate")
	}
}

func TestDecodePushQueryRequestFallsBackToBody(t *testing.T) {
	want := &pb.PushQuery{RequestId: 9, Address: "loopback", BlockId: "block-9"}
	body, err := proto.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/pushquery", bytes.NewReader(body))

	got, err := decodePushQueryRequest(req)
	if err != nil {
		t.Fatalf("decodePushQueryRequest() error = %v", err)
	}
	if !proto.Equal(got, want) {
		t.Fatalf("decoded PushQuery mismatch: got=%v want=%v", got, want)
	}
}

func TestPeerGateAcceptsMaximumPushQueryWithoutWeakeningSignatureChecks(t *testing.T) {
	consensus.ClearNodePublicKeys()
	consensus.SetNodePublicKeyResolver(nil)
	t.Cleanup(func() { consensus.ClearNodePublicKeys(); consensus.SetNodePublicKeyResolver(nil) })
	key := utils.NewKeyManager()
	if err := key.InitKeyRandom(); err != nil {
		t.Fatal(err)
	}
	consensus.RegisterNodePublicKey("alice", key.PublicKeyBytes())
	pg := newTestPeerGate(t)
	pg.promote("1.2.3.4", "alice")
	hm := &HandlerManager{peerGate: pg, cfg: config.DefaultConfig()}
	msg := &pb.PushQuery{Address: "alice", Responder: "bob", ChainId: pb.GetDefaultChainID(),
		RequestId: 1, ContainerIsBlock: true, Container: make([]byte, pb.MaxTotalSize),
		RequestedHeight: 1, BlockId: "block-1", VrfSeed: []byte("seed"), Nonce: bytes.Repeat([]byte{1}, 32)}
	var err error
	msg.Signature, err = key.Sign(consensus.ComputePushQueryDigest(msg))
	if err != nil {
		t.Fatal(err)
	}
	called := 0
	handler := hm.boundRequestBody("/pushquery", pg.Middleware("/pushquery", func(w http.ResponseWriter, r *http.Request) {
		decoded, err := decodePushQueryRequest(r)
		if err != nil || decoded != verifiedPushQueryFromContext(r.Context()) || len(decoded.Container) != pb.MaxTotalSize {
			t.Errorf("large verified query not handed to decoder: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		called++
		w.WriteHeader(http.StatusOK)
	}))
	for _, corrupt := range []bool{false, true} {
		if corrupt {
			msg.Signature[0] ^= 1
		}
		wire, err := proto.Marshal(msg)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, "/pushquery", bytes.NewReader(wire))
		r.RemoteAddr = "1.2.3.4:12345"
		rr := httptest.NewRecorder()
		handler(rr, r)
		want := http.StatusOK
		if corrupt {
			want = http.StatusForbidden
		}
		if rr.Code != want {
			t.Fatalf("corrupt=%v status=%d body=%q", corrupt, rr.Code, rr.Body.String())
		}
	}
	if called != 1 {
		t.Fatalf("handler calls=%d, want only the signed query", called)
	}
	r := httptest.NewRequest(http.MethodPost, "/pushquery", bytes.NewReader(make([]byte, pb.MaxPushQuerySize+1)))
	rr := httptest.NewRecorder()
	handler(rr, r)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("HTTP envelope bound missing: status=%d", rr.Code)
	}
}
