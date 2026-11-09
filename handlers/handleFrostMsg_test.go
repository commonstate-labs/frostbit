package handlers

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"dex/frost/runtime/roast"
	roastsession "dex/frost/runtime/session"
	"dex/frost/security"
	"dex/pb"
	"dex/stats"
	"dex/types"

	"google.golang.org/protobuf/proto"
)

type frostHandlerTestLogger struct{}

func (frostHandlerTestLogger) Trace(string, ...interface{})   {}
func (frostHandlerTestLogger) Debug(string, ...interface{})   {}
func (frostHandlerTestLogger) Verbose(string, ...interface{}) {}
func (frostHandlerTestLogger) Info(string, ...interface{})    {}
func (frostHandlerTestLogger) Warn(string, ...interface{})    {}
func (frostHandlerTestLogger) Error(string, ...interface{})   {}
func (frostHandlerTestLogger) GetBuffer() []*pb.LogLine       { return nil }

func installFrostHandlerTestVerifier(t *testing.T, window uint64) {
	t.Helper()
	previous := GetFrostMsgVerifier()
	SetFrostMsgVerifier(&FrostMsgVerifier{
		ReplayGuard:       security.NewSeqReplayGuard(window),
		EnableSigVerify:   false,
		EnableReplayGuard: true,
	})
	t.Cleanup(func() {
		SetFrostMsgVerifier(previous)
	})
}

func newFrostHandlerTestManager(handler FrostMsgHandler) *HandlerManager {
	return &HandlerManager{
		address:         "receiver",
		Stats:           stats.NewStats(),
		frostMsgHandler: handler,
		Logger:          frostHandlerTestLogger{},
	}
}

func serveFrostHandlerTestRequest(t *testing.T, hm *HandlerManager, envelope *pb.FrostEnvelope) *httptest.ResponseRecorder {
	t.Helper()
	body, err := proto.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal FrostEnvelope: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/frostmsg", bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	hm.HandleFrostMsg(recorder, req)
	return recorder
}

func TestHandleFrostMsgFailedAttemptReleasesSequence(t *testing.T) {
	installFrostHandlerTestVerifier(t, 8)

	var calls atomic.Int32
	hm := newFrostHandlerTestManager(func(types.Message) error {
		if calls.Add(1) == 1 {
			return roast.ErrRoastStateNotReady
		}
		return nil
	})
	envelope := &pb.FrostEnvelope{
		From:    "sender",
		To:      "receiver",
		Kind:    pb.FrostEnvelopeKind_FROST_ENVELOPE_KIND_ROAST_REQUEST,
		Payload: []byte("request"),
		JobId:   "job-1",
		Seq:     7,
	}

	if recorder := serveFrostHandlerTestRequest(t, hm, envelope); recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("failed attempt status=%d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	if recorder := serveFrostHandlerTestRequest(t, hm, envelope); recorder.Code != http.StatusOK {
		t.Fatalf("retry status=%d, want %d", recorder.Code, http.StatusOK)
	}
	if recorder := serveFrostHandlerTestRequest(t, hm, envelope); recorder.Code != http.StatusOK {
		t.Fatalf("committed duplicate status=%d, want %d", recorder.Code, http.StatusOK)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("handler calls=%d, want 3 (retry plus cached-response replay)", got)
	}

	conflict := proto.Clone(envelope).(*pb.FrostEnvelope)
	conflict.Payload = []byte("different request")
	if recorder := serveFrostHandlerTestRequest(t, hm, conflict); recorder.Code != http.StatusConflict {
		t.Fatalf("same seq with different payload status=%d, want %d", recorder.Code, http.StatusConflict)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("conflicting payload reached handler: calls=%d", got)
	}
}

func TestHandleFrostMsgConcurrentDuplicateDoesNotDoubleExecute(t *testing.T) {
	installFrostHandlerTestVerifier(t, 8)

	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var calls atomic.Int32
	hm := newFrostHandlerTestManager(func(types.Message) error {
		if calls.Add(1) == 1 {
			close(firstStarted)
			<-releaseFirst
			return roast.ErrSessionNotFound
		}
		return nil
	})
	envelope := &pb.FrostEnvelope{
		From:    "sender",
		To:      "receiver",
		Kind:    pb.FrostEnvelopeKind_FROST_ENVELOPE_KIND_ROAST_REQUEST,
		Payload: []byte("request"),
		JobId:   "job-2",
		Seq:     9,
	}

	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		firstDone <- serveFrostHandlerTestRequest(t, hm, envelope)
	}()
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first handler call did not start")
	}

	inFlight := serveFrostHandlerTestRequest(t, hm, envelope)
	if inFlight.Code != http.StatusServiceUnavailable {
		t.Fatalf("in-flight duplicate status=%d, want %d", inFlight.Code, http.StatusServiceUnavailable)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("in-flight duplicate reached handler: calls=%d", got)
	}

	conflict := proto.Clone(envelope).(*pb.FrostEnvelope)
	conflict.Payload = []byte("conflict while in flight")
	if recorder := serveFrostHandlerTestRequest(t, hm, conflict); recorder.Code != http.StatusConflict {
		t.Fatalf("in-flight payload conflict status=%d, want %d", recorder.Code, http.StatusConflict)
	}

	close(releaseFirst)
	select {
	case recorder := <-firstDone:
		if recorder.Code != http.StatusServiceUnavailable {
			t.Fatalf("first failed request status=%d, want %d", recorder.Code, http.StatusServiceUnavailable)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first handler call did not finish")
	}

	if recorder := serveFrostHandlerTestRequest(t, hm, envelope); recorder.Code != http.StatusOK {
		t.Fatalf("retry after release status=%d, want %d", recorder.Code, http.StatusOK)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("handler calls after retry=%d, want 2", got)
	}
}

func TestHandleFrostMsgCommittedResponseDuplicateIsAcknowledgedWithoutReprocessing(t *testing.T) {
	installFrostHandlerTestVerifier(t, 8)

	var calls atomic.Int32
	hm := newFrostHandlerTestManager(func(types.Message) error {
		calls.Add(1)
		return nil
	})
	envelope := &pb.FrostEnvelope{
		From:    "sender",
		To:      "receiver",
		Kind:    pb.FrostEnvelopeKind_FROST_ENVELOPE_KIND_ROAST_RESPONSE,
		Payload: []byte("nonce commitment"),
		JobId:   "job-response",
		Seq:     12,
	}

	if recorder := serveFrostHandlerTestRequest(t, hm, envelope); recorder.Code != http.StatusOK {
		t.Fatalf("first response status=%d, want %d", recorder.Code, http.StatusOK)
	}
	if recorder := serveFrostHandlerTestRequest(t, hm, envelope); recorder.Code != http.StatusOK {
		t.Fatalf("duplicate response status=%d, want %d", recorder.Code, http.StatusOK)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("duplicate response reached coordinator handler: calls=%d", got)
	}
}

func TestHandleFrostMsgPermanentFailureIsConsumed(t *testing.T) {
	installFrostHandlerTestVerifier(t, 8)

	var calls atomic.Int32
	hm := newFrostHandlerTestManager(func(types.Message) error {
		calls.Add(1)
		return errors.New("payload does not match finalized signing context")
	})
	envelope := &pb.FrostEnvelope{
		From:    "sender",
		To:      "receiver",
		Kind:    pb.FrostEnvelopeKind_FROST_ENVELOPE_KIND_ROAST_REQUEST,
		Payload: []byte("bad request"),
		JobId:   "job-invalid",
		Seq:     14,
	}

	for attempt := 1; attempt <= 2; attempt++ {
		if recorder := serveFrostHandlerTestRequest(t, hm, envelope); recorder.Code != http.StatusUnprocessableEntity {
			t.Fatalf("attempt %d status=%d, want %d", attempt, recorder.Code, http.StatusUnprocessableEntity)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("permanently rejected duplicate re-entered handler: calls=%d", got)
	}
}

func TestHandleFrostMsgExplicitTransientFailuresReleaseSequence(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "local share finalization lag", err: roast.ErrLocalShareNotReady},
		{name: "participant response enqueue failure", err: roast.ErrRoastResponseSendFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			installFrostHandlerTestVerifier(t, 8)
			var calls atomic.Int32
			hm := newFrostHandlerTestManager(func(types.Message) error {
				if calls.Add(1) == 1 {
					return test.err
				}
				return nil
			})
			envelope := &pb.FrostEnvelope{
				From: "sender", To: "receiver",
				Kind:    pb.FrostEnvelopeKind_FROST_ENVELOPE_KIND_ROAST_REQUEST,
				Payload: []byte(test.name), JobId: "job-transient", Seq: 16,
			}
			if recorder := serveFrostHandlerTestRequest(t, hm, envelope); recorder.Code != http.StatusServiceUnavailable {
				t.Fatalf("first status=%d, want %d", recorder.Code, http.StatusServiceUnavailable)
			}
			if recorder := serveFrostHandlerTestRequest(t, hm, envelope); recorder.Code != http.StatusOK {
				t.Fatalf("same-seq retry status=%d, want %d", recorder.Code, http.StatusOK)
			}
			if got := calls.Load(); got != 2 {
				t.Fatalf("handler calls=%d, want 2", got)
			}
		})
	}
}

func TestHandleFrostMsgCommittedRequestReplayRetriesResponseSendFailure(t *testing.T) {
	installFrostHandlerTestVerifier(t, 8)
	var calls atomic.Int32
	hm := newFrostHandlerTestManager(func(types.Message) error {
		switch calls.Add(1) {
		case 2:
			return roast.ErrRoastResponseSendFailed
		default:
			return nil
		}
	})
	envelope := &pb.FrostEnvelope{
		From: "sender", To: "receiver",
		Kind:    pb.FrostEnvelopeKind_FROST_ENVELOPE_KIND_ROAST_REQUEST,
		Payload: []byte("cached response"), JobId: "job-replay-send", Seq: 17,
	}

	if recorder := serveFrostHandlerTestRequest(t, hm, envelope); recorder.Code != http.StatusOK {
		t.Fatalf("initial request status=%d, want %d", recorder.Code, http.StatusOK)
	}
	if recorder := serveFrostHandlerTestRequest(t, hm, envelope); recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("failed cached resend status=%d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	if recorder := serveFrostHandlerTestRequest(t, hm, envelope); recorder.Code != http.StatusOK {
		t.Fatalf("cached resend retry status=%d, want %d", recorder.Code, http.StatusOK)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("handler calls=%d, want 3", got)
	}
}

func TestHandleFrostMsgValidatesRecipientBeforeReplayState(t *testing.T) {
	installFrostHandlerTestVerifier(t, 8)

	var calls atomic.Int32
	hm := newFrostHandlerTestManager(func(types.Message) error {
		calls.Add(1)
		return nil
	})
	mismatched := &pb.FrostEnvelope{From: "sender", To: "another-node", Payload: []byte("request"), Seq: 20}
	if recorder := serveFrostHandlerTestRequest(t, hm, mismatched); recorder.Code != http.StatusBadRequest {
		t.Fatalf("recipient mismatch status=%d, want %d", recorder.Code, http.StatusBadRequest)
	}
	matching := proto.Clone(mismatched).(*pb.FrostEnvelope)
	matching.To = "receiver"
	if recorder := serveFrostHandlerTestRequest(t, hm, matching); recorder.Code != http.StatusOK {
		t.Fatalf("matching recipient status=%d, want %d", recorder.Code, http.StatusOK)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("handler calls=%d, want only matching recipient", got)
	}
}

func TestHandleFrostMsgEmptyLocalAddressKeepsCompatibility(t *testing.T) {
	installFrostHandlerTestVerifier(t, 8)

	var calls atomic.Int32
	hm := newFrostHandlerTestManager(func(types.Message) error {
		calls.Add(1)
		return nil
	})
	hm.address = ""
	envelope := &pb.FrostEnvelope{From: "sender", To: "legacy-receiver", Payload: []byte("request"), Seq: 21}
	if recorder := serveFrostHandlerTestRequest(t, hm, envelope); recorder.Code != http.StatusOK {
		t.Fatalf("empty-local compatibility status=%d, want %d", recorder.Code, http.StatusOK)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("handler calls=%d, want 1", got)
	}
}

func TestRetryableFrostProcessingErrorClassification(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		retryable bool
	}{
		{name: "coordinator session lag", err: fmt.Errorf("wrapped: %w", roast.ErrSessionNotFound), retryable: true},
		{name: "future participant round", err: roast.ErrFutureRoastRound, retryable: true},
		{name: "recoverable state transition", err: roast.ErrRoastStateNotReady, retryable: true},
		{name: "local share promotion lag", err: roast.ErrLocalShareNotReady, retryable: true},
		{name: "response send failure", err: roast.ErrRoastResponseSendFailed, retryable: true},
		{name: "signing intent finalization lag", err: roast.ErrSigningIntentNotReady, retryable: true},
		{name: "persistent session lag", err: roastsession.ErrSessionNotFound, retryable: true},
		{name: "failed or otherwise invalid session state", err: roastsession.ErrInvalidState, retryable: false},
		{name: "payload mismatch", err: errors.New("payload mismatch"), retryable: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isRetryableFrostProcessingError(test.err); got != test.retryable {
				t.Fatalf("retryable=%v, want %v", got, test.retryable)
			}
		})
	}
}

func TestFrostSequenceJumpWarningAllowsReservedRestartGap(t *testing.T) {
	if frostSeqJumpWarnThreshold < security.FrostSequenceReservationSize {
		t.Fatalf("jump warning threshold=%d, want at least reservation size %d", frostSeqJumpWarnThreshold, security.FrostSequenceReservationSize)
	}
}

func TestHandleFrostMsgRejectsStaleSequence(t *testing.T) {
	installFrostHandlerTestVerifier(t, 2)

	var calls atomic.Int32
	hm := newFrostHandlerTestManager(func(types.Message) error {
		calls.Add(1)
		return nil
	})
	latest := &pb.FrostEnvelope{From: "sender", To: "receiver", Payload: []byte("latest"), Seq: 10}
	if recorder := serveFrostHandlerTestRequest(t, hm, latest); recorder.Code != http.StatusOK {
		t.Fatalf("latest status=%d, want %d", recorder.Code, http.StatusOK)
	}
	stale := &pb.FrostEnvelope{From: "sender", To: "receiver", Payload: []byte("stale"), Seq: 8}
	if recorder := serveFrostHandlerTestRequest(t, hm, stale); recorder.Code != http.StatusConflict {
		t.Fatalf("stale status=%d, want %d", recorder.Code, http.StatusConflict)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("stale request reached handler: calls=%d", got)
	}
}
