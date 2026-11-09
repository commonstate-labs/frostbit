package sender

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"dex/config"
	"dex/logs"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

func TestFrostSendQueueHTTPRetryPolicy(t *testing.T) {
	if frostSendMaxRetries != 7 {
		t.Fatalf("frost max retries = %d, want 7", frostSendMaxRetries)
	}

	tests := []struct {
		name            string
		statuses        []int
		wantAttempts    int
		wantRetryCount  int
		wantFailureCode int
	}{
		{
			name:            "409 is not retried",
			statuses:        []int{http.StatusConflict},
			wantAttempts:    1,
			wantRetryCount:  0,
			wantFailureCode: http.StatusConflict,
		},
		{
			name:           "503 retries the same envelope",
			statuses:       []int{http.StatusServiceUnavailable, http.StatusOK},
			wantAttempts:   2,
			wantRetryCount: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var (
				requestsMu sync.Mutex
				bodies     [][]byte
			)
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/frostmsg" {
					t.Errorf("request path = %q, want /frostmsg", r.URL.Path)
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read request body: %v", err)
				}

				requestsMu.Lock()
				attempt := len(bodies)
				bodies = append(bodies, append([]byte(nil), body...))
				requestsMu.Unlock()

				status := tc.statuses[len(tc.statuses)-1]
				if attempt < len(tc.statuses) {
					status = tc.statuses[attempt]
				}
				w.WriteHeader(status)
			}))
			defer server.Close()

			cfg := config.DefaultConfig()
			cfg.Sender.BaseRetryDelay = time.Millisecond
			cfg.Sender.MaxRetryDelay = time.Millisecond
			cfg.Sender.JitterFactor = 0
			cfg.Sender.TaskExpireTimeout = time.Second
			queue := NewSendQueue(
				2,
				128,
				server.Client(),
				0,
				"frost-retry-test",
				logs.NewNodeLogger("frost-retry-test", 0),
				cfg,
			)
			defer queue.Stop()

			envelope := &pb.FrostEnvelope{
				From:    "sender",
				To:      "receiver",
				Kind:    pb.FrostEnvelopeKind_FROST_ENVELOPE_KIND_ROAST_REQUEST,
				Payload: []byte("signed-payload"),
				Sig:     []byte("signature"),
				JobId:   "job-1",
				Seq:     77,
			}
			original := proto.Clone(envelope).(*pb.FrostEnvelope)
			message := &frostMessage{envelope: envelope}
			var failure error
			done := make(chan struct{})
			task := &SendTask{
				Target:     strings.TrimPrefix(server.URL, "https://"),
				Message:    message,
				MaxRetries: frostSendMaxRetries,
				SendFunc:   doSendFrost,
				Priority:   PriorityControl,
				OnFailure: func(err error) {
					failure = err
				},
				OnDone: func() {
					close(done)
				},
			}
			if !queue.Enqueue(task) {
				t.Fatal("enqueue Frost task failed")
			}

			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("timed out waiting for Frost send")
			}

			requestsMu.Lock()
			gotBodies := make([][]byte, len(bodies))
			for i := range bodies {
				gotBodies[i] = append([]byte(nil), bodies[i]...)
			}
			requestsMu.Unlock()

			if len(gotBodies) != tc.wantAttempts {
				t.Fatalf("attempts = %d, want %d", len(gotBodies), tc.wantAttempts)
			}
			if task.RetryCount != tc.wantRetryCount {
				t.Fatalf("retry count = %d, want %d", task.RetryCount, tc.wantRetryCount)
			}
			if tc.wantFailureCode != 0 {
				var statusErr *httpStatusError
				if !errors.As(failure, &statusErr) {
					t.Fatalf("failure = %T %v, want *httpStatusError", failure, failure)
				}
				if statusErr.statusCode != tc.wantFailureCode {
					t.Fatalf("failure status = %d, want %d", statusErr.statusCode, tc.wantFailureCode)
				}
			} else if failure != nil {
				t.Fatalf("successful retry reported failure: %v", failure)
			}

			for attempt, body := range gotBodies {
				var decoded pb.FrostEnvelope
				if err := proto.Unmarshal(body, &decoded); err != nil {
					t.Fatalf("attempt %d unmarshal: %v", attempt+1, err)
				}
				if !proto.Equal(&decoded, original) || decoded.Seq != original.Seq {
					t.Fatalf("attempt %d changed Frost envelope: got=%v want=%v", attempt+1, &decoded, original)
				}
				if attempt > 0 && !bytes.Equal(body, gotBodies[0]) {
					t.Fatalf("attempt %d body differs from first attempt", attempt+1)
				}
			}
			if message.envelope != envelope || !proto.Equal(envelope, original) || envelope.Seq != 77 {
				t.Fatal("retry mutated the queued Frost envelope or sequence")
			}
		})
	}
}

func TestSendFrostReportsControlQueueRejection(t *testing.T) {
	queue := &SendQueue{
		controlQueue: newControlTaskQueue(1),
		Logger:       logs.NewNodeLogger("frost-queue-reject-test", 0),
	}
	queue.controlQueue.push(&SendTask{})
	manager := &SenderManager{SendQueue: queue}

	err := manager.SendFrost("127.0.0.1:9000", &pb.FrostEnvelope{JobId: "job-full"})
	if err == nil || !strings.Contains(err.Error(), "send queue rejected") {
		t.Fatalf("SendFrost queue rejection error=%v", err)
	}
}
