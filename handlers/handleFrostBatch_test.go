package handlers

import (
	"bytes"
	"crypto/ecdsa"
	"net/http"
	"net/http/httptest"
	"testing"

	"dex/config"
	"dex/consensus"
	"dex/frost/runtime/roast"
	"dex/frost/security"
	"dex/pb"
	"dex/types"
	"github.com/btcsuite/btcd/btcec/v2"
	"google.golang.org/protobuf/proto"
)

func TestFrostTransportBatchVerifiesEveryEntryMembership(t *testing.T) {
	previous := GetFrostMsgVerifier()
	t.Cleanup(func() { SetFrostMsgVerifier(previous); consensus.ClearNodePublicKeys() })
	consensus.ClearNodePublicKeys()
	private, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	pub := private.PubKey().SerializeCompressed()
	SetFrostMsgVerifier(&FrostMsgVerifier{EnableSigVerify: true, EnableReplayGuard: true, ReplayGuard: security.NewSeqReplayGuard(1000), GetPublicKey: func(string) (*ecdsa.PublicKey, error) { return private.PubKey().ToECDSA(), nil }})
	calls := 0
	hm := newFrostHandlerTestManager(func(types.Message) error { calls++; return nil })
	pg := NewPeerGate(config.DefaultPeerGateConfig(), hm.Logger)
	pg.SetDeps(nil, &peerGateTestDB{accounts: map[string]*pb.Account{
		"honest": {Address: "honest", IsMiner: true, Ip: "192.0.2.7:6000", PublicKeys: &pb.PublicKeys{Keys: map[int32][]byte{int32(pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1_DEX): pub}}},
		"guest":  {Address: "guest", IsMiner: false, Ip: "192.0.2.7:6000", PublicKeys: &pb.PublicKeys{Keys: map[int32][]byte{int32(pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1_DEX): pub}}},
	}})
	hm.peerGate = pg
	batch := &pb.FrostEnvelopeBatch{}
	for _, from := range []string{"guest", "honest"} {
		env := &pb.FrostEnvelope{From: from, To: "receiver", Seq: 1, JobId: "job-" + from, Payload: []byte("payload")}
		if err := security.SignFrostEnvelope(private.ToECDSA(), env); err != nil {
			t.Fatal(err)
		}
		raw, _ := proto.Marshal(env)
		batch.Envelopes = append(batch.Envelopes, raw)
	}
	body, _ := proto.Marshal(batch)
	if !pg.verifyAndPromote("192.0.2.7", "/frostmsgbatch", body) {
		t.Fatal("honest sibling must admit bounded framing")
	}
	r := httptest.NewRequest(http.MethodPost, "/frostmsgbatch", bytes.NewReader(body))
	r.RemoteAddr = "192.0.2.7:9999"
	w := httptest.NewRecorder()
	hm.HandleFrostBatch(w, r)
	var reply pb.FrostEnvelopeBatchReply
	if err := proto.Unmarshal(w.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	if len(reply.Statuses) != 2 || reply.Statuses[0] != 403 || reply.Statuses[1] != 200 || calls != 1 {
		t.Fatalf("non-member inherited batch authorization: statuses=%v calls=%d", reply.Statuses, calls)
	}
}

func TestFrostTransportBatchIndependentFailuresAndReplay(t *testing.T) {
	installFrostHandlerTestVerifier(t, 1000)
	calls := map[string]int{}
	hm := newFrostHandlerTestManager(func(msg types.Message) error {
		var env pb.FrostEnvelope
		if err := proto.Unmarshal(msg.FrostPayload, &env); err != nil {
			return err
		}
		calls[env.JobId]++
		if env.JobId == "not-ready" && calls[env.JobId] == 1 {
			return roast.ErrSigningIntentNotReady
		}
		return nil
	})
	batch := &pb.FrostEnvelopeBatch{}
	for i, id := range []string{"good-A", "not-ready", "good-B"} {
		raw, err := proto.Marshal(&pb.FrostEnvelope{From: "sender", To: "receiver", Seq: uint64(i + 1), Kind: pb.FrostEnvelopeKind_FROST_ENVELOPE_KIND_ROAST_RESPONSE, JobId: id, Payload: []byte(id)})
		if err != nil {
			t.Fatal(err)
		}
		batch.Envelopes = append(batch.Envelopes, raw)
	}
	batch.Envelopes = append([][]byte{{0xff}}, batch.Envelopes...)
	body, _ := proto.Marshal(batch)
	serve := func() []uint32 {
		r := httptest.NewRecorder()
		hm.HandleFrostBatch(r, httptest.NewRequest(http.MethodPost, "/frostmsgbatch", bytes.NewReader(body)))
		if r.Code != 200 {
			t.Fatalf("batch failed %d: %s", r.Code, r.Body.String())
		}
		var reply pb.FrostEnvelopeBatchReply
		if err := proto.Unmarshal(r.Body.Bytes(), &reply); err != nil {
			t.Fatal(err)
		}
		return reply.Statuses
	}
	first := serve()
	if len(first) != 4 || first[0] != 400 || first[1] != 200 || first[2] != 503 || first[3] != 200 {
		t.Fatalf("first statuses %v", first)
	}
	second := serve()
	if second[1] != 200 || second[2] != 200 || second[3] != 200 {
		t.Fatalf("retry statuses %v", second)
	}
	if calls["good-A"] != 1 || calls["good-B"] != 1 || calls["not-ready"] != 2 {
		t.Fatalf("replay changed sibling execution: %v", calls)
	}
}

func TestFrostTransportBatchRejectsUnboundedFraming(t *testing.T) {
	for _, count := range []int{0, security.FrostTransportBatchItems + 1} {
		batch := &pb.FrostEnvelopeBatch{Envelopes: make([][]byte, count)}
		for i := range batch.Envelopes {
			batch.Envelopes[i] = []byte{1}
		}
		body, _ := proto.Marshal(batch)
		if _, err := decodeFrostBatch(body); err == nil {
			t.Fatalf("accepted count %d", count)
		}
	}
	if _, err := decodeFrostBatch(make([]byte, security.FrostTransportBatchBytes+1)); err == nil {
		t.Fatal("accepted oversized framing")
	}
}
