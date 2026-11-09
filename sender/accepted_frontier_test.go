package sender

import (
	"dex/config"
	"dex/logs"
	"dex/pb"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
)

func TestDoSendAcceptedFrontierRoundTrip(t *testing.T) {
	request := &pb.AcceptedFrontierRequest{
		Version:       2,
		ChainId:       7,
		RequesterId:   "requester",
		Nonce:         make([]byte, 32),
		AnchorHeight:  10,
		AnchorBlockId: "block-10",
		TargetHeight:  11,
		Signature:     []byte("request-signature"),
	}
	requestBytes, err := proto.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	response := &pb.AcceptedFrontierResponse{
		Version:          2,
		ChainId:          7,
		RequestDigest:    []byte("request-digest"),
		ResponderId:      "responder",
		Status:           pb.AcceptedFrontierStatus_ACCEPTED_FRONTIER_STATUS_OK,
		AcceptedHeight:   12,
		ObservedAnchorId: "block-10",
		TargetHeight:     11,
		TargetBlockId:    "block-11",
		Signature:        []byte("response-signature"),
	}

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/acceptedfrontier" {
			http.NotFound(w, r)
			return
		}
		var got pb.AcceptedFrontierRequest
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := proto.Unmarshal(body, &got); err != nil || got.TargetHeight != request.TargetHeight {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		encoded, _ := proto.Marshal(response)
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(encoded)
	}))
	defer server.Close()

	var received *pb.AcceptedFrontierResponse
	task := &SendTask{
		Target: strings.TrimPrefix(server.URL, "https://"),
		Message: &acceptedFrontierMessage{
			requestData: requestBytes,
			onSuccess: func(resp *pb.AcceptedFrontierResponse) error {
				received = resp
				return nil
			},
		},
		Timeout: time.Second,
	}
	if err := doSendAcceptedFrontier(task, server.Client()); err != nil {
		t.Fatalf("doSendAcceptedFrontier: %v", err)
	}
	if received == nil || received.TargetBlockId != response.TargetBlockId || received.ResponderId != response.ResponderId {
		t.Fatalf("unexpected response: %#v", received)
	}
}

func TestDoSendAcceptedFrontierReturnsCallbackError(t *testing.T) {
	responseBytes, err := proto.Marshal(&pb.AcceptedFrontierResponse{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(responseBytes)
	}))
	defer server.Close()

	wantErr := errors.New("frontier response verification failed")
	task := &SendTask{
		Target: strings.TrimPrefix(server.URL, "https://"),
		Message: &acceptedFrontierMessage{
			requestData: []byte{0},
			onSuccess: func(*pb.AcceptedFrontierResponse) error {
				return wantErr
			},
		},
		Timeout: time.Second,
	}
	if err := doSendAcceptedFrontier(task, server.Client()); !errors.Is(err, wantErr) {
		t.Fatalf("expected callback error %v, got %v", wantErr, err)
	}
}

func TestSendQueueEnqueueReportsAdmission(t *testing.T) {
	sq := &SendQueue{
		controlQueue:  newControlTaskQueue(1),
		immediateChan: make(chan *SendTask, 1),
		dataChan:      make(chan *SendTask, 1),
		stopChan:      make(chan struct{}),
		Logger:        logs.NewNodeLogger("frontier-admission-test", 0),
		peerHealth:    newPeerHealthTracker(),
	}
	if !sq.Enqueue(&SendTask{Target: "peer", Priority: PriorityControl}) {
		t.Fatal("first control task should be admitted")
	}
	if sq.Enqueue(&SendTask{Target: "peer", Priority: PriorityControl}) {
		t.Fatal("task must be rejected when the control queue is full")
	}

	quarantined := "unhealthy-peer"
	sq.peerHealth.recordResult(quarantined, errors.New("connection refused"))
	sq.peerHealth.recordResult(quarantined, errors.New("connection refused"))
	if sq.Enqueue(&SendTask{Target: quarantined, Priority: PriorityData}) {
		t.Fatal("task must be rejected while peer health quarantines the target")
	}

	delayed := &SendTask{
		Target:      "delayed-peer",
		Priority:    PriorityData,
		NextAttempt: time.Now().Add(time.Hour),
	}
	if !sq.Enqueue(delayed) {
		t.Fatal("delayed task should report admission once its timer is scheduled")
	}
	close(sq.stopChan)
}

func TestSendAcceptedFrontierReturnsQueueRejection(t *testing.T) {
	cfg := config.DefaultConfig()
	sq := &SendQueue{
		controlQueue: newControlTaskQueue(1),
		Logger:       logs.NewNodeLogger("frontier-manager-admission-test", 0),
		peerHealth:   newPeerHealthTracker(),
	}
	sq.controlQueue.push(&SendTask{})
	sm := &SenderManager{
		address:      "local",
		SendQueue:    sq,
		cfg:          cfg,
		minerIPCache: make(map[string]string),
	}
	err := sm.SendAcceptedFrontier(
		"127.0.0.1:65535",
		&pb.AcceptedFrontierRequest{},
		func(*pb.AcceptedFrontierResponse) error { return nil },
	)
	if err == nil || !strings.Contains(err.Error(), "not admitted") {
		t.Fatalf("expected queue admission error, got %v", err)
	}
}

func TestDoSendAcceptedFrontierRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(make([]byte, (16<<10)+1))
	}))
	defer server.Close()

	task := &SendTask{
		Target: strings.TrimPrefix(server.URL, "https://"),
		Message: &acceptedFrontierMessage{
			requestData: []byte{0},
		},
		Timeout: time.Second,
	}
	err := doSendAcceptedFrontier(task, server.Client())
	if err == nil || !strings.Contains(err.Error(), "response body exceeds") {
		t.Fatalf("expected bounded response error, got %v", err)
	}
}
