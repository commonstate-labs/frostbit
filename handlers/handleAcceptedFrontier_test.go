package handlers

import (
	"bytes"
	"dex/consensus"
	"dex/lifecycle"
	"dex/logs"
	"dex/pb"
	"dex/stats"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
)

func TestHandleAcceptedFrontierRejectsDuringEmergencyStop(t *testing.T) {
	emergency := lifecycle.New()
	emergency.Stop("test")
	hm := &HandlerManager{Stats: stats.NewStats(), emergency: emergency}
	req := httptest.NewRequest(http.MethodPost, "/acceptedfrontier", strings.NewReader("request"))
	recorder := httptest.NewRecorder()

	hm.HandleAcceptedFrontier(recorder, req)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("emergency-stopped frontier status=%d want=%d", recorder.Code, http.StatusServiceUnavailable)
	}
}

func TestHandleAcceptedFrontierRejectsOversizedBody(t *testing.T) {
	hm := &HandlerManager{Stats: stats.NewStats()}
	req := httptest.NewRequest(http.MethodPost, "/acceptedfrontier", strings.NewReader(strings.Repeat("x", (16<<10)+1)))
	recorder := httptest.NewRecorder()

	hm.HandleAcceptedFrontier(recorder, req)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized frontier request status=%d want=%d", recorder.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestHandleAcceptedFrontierInvalidSignatureCannotPopulateReplayState(t *testing.T) {
	request := &pb.AcceptedFrontierRequest{
		Version:       consensus.AcceptedFrontierProtocolVersion,
		ChainId:       pb.GetDefaultChainID(),
		RequesterId:   "invalid-requester",
		Nonce:         bytes.Repeat([]byte{0x42}, consensus.AcceptedFrontierNonceSize),
		AnchorHeight:  5,
		AnchorBlockId: "block-5",
		TargetHeight:  6,
		Signature:     []byte("invalid-signature"),
	}
	encoded, err := proto.Marshal(request)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	hm := &HandlerManager{
		Stats:  stats.NewStats(),
		Logger: logs.NewNodeLogger("frontier-invalid-signature-test", 0),
	}
	req := httptest.NewRequest(http.MethodPost, "/acceptedfrontier", bytes.NewReader(encoded))
	recorder := httptest.NewRecorder()

	hm.HandleAcceptedFrontier(recorder, req)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("invalid-signature status=%d want=%d", recorder.Code, http.StatusUnauthorized)
	}
	if hm.acceptedFrontierCache == nil {
		t.Fatal("completed-cache lookup did not initialize cache")
	}
	hm.acceptedFrontierCache.mu.Lock()
	entries := len(hm.acceptedFrontierCache.entries)
	hm.acceptedFrontierCache.mu.Unlock()
	if entries != 0 {
		t.Fatalf("invalid signature populated %d replay cache entries", entries)
	}
	if hm.acceptedFrontierLimiter != nil {
		t.Fatal("invalid signature populated requester limiter state")
	}
}
