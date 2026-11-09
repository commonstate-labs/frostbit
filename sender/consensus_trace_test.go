package sender

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"dex/config"
	"dex/logs"
	"dex/pb"
)

func TestConsensusSendTraceMetadata(t *testing.T) {
	queue := &SendQueue{controlQueue: newControlTaskQueue(2), immediateChan: make(chan *SendTask, 1)}
	sm := &SenderManager{address: "trace-node", cfg: config.DefaultConfig(), SendQueue: queue}
	if err := sm.PushQueryWithFailure("127.0.0.1:19000", &pb.PushQuery{RequestId: 21, RequestedHeight: 501}, nil); err != nil {
		t.Fatal(err)
	}
	if err := sm.PullQueryWithFailure("127.0.0.1:19000", &pb.PullQuery{RequestId: 22, RequestedHeight: 502}, nil); err != nil {
		t.Fatal(err)
	}
	if err := sm.SendChits("127.0.0.1:19000", &pb.Chits{RequestId: 23, PreferredBlockAtHeight: 503}); err != nil {
		t.Fatal(err)
	}
	for i, task := range []*SendTask{<-queue.immediateChan, queue.controlQueue.pop(controlTaskReady), queue.controlQueue.pop(controlTaskReady)} {
		if task.TraceKind != []string{"push", "pull", "chit"}[i] || task.TraceRequestID != uint32(21+i) || task.TraceHeight != uint64(501+i) || task.NodeAddress != "trace-node" {
			t.Fatalf("metadata kind=%s request=%d height=%d node=%s", task.TraceKind, task.TraceRequestID, task.TraceHeight, task.NodeAddress)
		}
	}
}

func TestConsensusSendTraceInflightIndependentOfTraceToggle(t *testing.T) {
	queue := newTestSendQueueWithLimits(1, 1, 3)
	t.Setenv("DEX_CONSENSUS_TRACE", "0")
	for i := 0; i < 3; i++ {
		if !queue.tryAcquireInflight("peer", "data") {
			t.Fatal("data request unexpectedly blocked")
		}
	}
	t.Setenv("DEX_CONSENSUS_TRACE", "1")
	acquired, occupied := queue.tryAcquireInflightObserved("peer", "control")
	if !acquired || occupied.Total != 3 || occupied.Limit != 1 || occupied.Classes != [3]int32{3, 0, 0} {
		t.Fatalf("trace toggle changed class accounting acquired=%t occupied=%+v", acquired, occupied)
	}
	if !queue.tryAcquireInflight("peer", "immediate") {
		t.Fatal("independent immediate slot blocked")
	}
	t.Setenv("DEX_CONSENSUS_TRACE", "0")
	acquired, occupied = queue.tryAcquireInflightObserved("peer", "control")
	if acquired || occupied.Total != 5 || occupied.Classes != [3]int32{3, 1, 1} {
		t.Fatalf("class limit/accounting changed when trace disabled: acquired=%t %+v", acquired, occupied)
	}
	for _, kind := range []string{"data", "data", "data", "control", "immediate"} {
		queue.releaseInflight("peer", kind)
	}
	if len(queue.InflightMap) != 0 || len(queue.inflightByClass) != 0 {
		t.Fatal("inflight accounting retained released peers")
	}
}

func TestConsensusSendTraceInflightConcurrent(t *testing.T) {
	t.Setenv("DEX_CONSENSUS_TRACE", "1")
	queue := newTestSendQueueWithLimits(0, 0, 0)
	var wg sync.WaitGroup
	for worker := 0; worker < 32; worker++ {
		kind := []string{"data", "control", "immediate"}[worker%3]
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				ok, occupied := queue.tryAcquireInflightObserved("peer", kind)
				if !ok || occupied.Total != occupied.Classes[0]+occupied.Classes[1]+occupied.Classes[2] {
					t.Errorf("non-atomic observation: %+v", occupied)
				}
				queue.releaseInflight("peer", kind)
			}
		}()
	}
	wg.Wait()
	if len(queue.InflightMap) != 0 || len(queue.inflightByClass) != 0 {
		t.Fatal("concurrent acquisition/release leaked counts")
	}
}

func TestConsensusSendTraceTimingSamplingAndSafeFields(t *testing.T) {
	t.Setenv("DEX_CONSENSUS_TRACE", "1")
	logger := logs.NewNodeLogger(t.Name(), 20)
	defer logs.SetThreadLogger(logger)()
	queue := &SendQueue{}
	created := time.Now()
	task := &SendTask{
		Target: "peer:19000", NodeAddress: "node-A", Priority: PriorityControl,
		TraceKind: "pull", TraceRequestID: 31, TraceHeight: 987, CreatedAt: created,
		Message: "secret payload must never be logged",
	}
	if shouldTraceSend(task, time.Millisecond, time.Millisecond, false) {
		t.Fatal("fast unsampled request should not emit")
	}
	if !shouldTraceSend(task, 200*time.Millisecond, 0, false) || !shouldTraceSend(task, 0, 0, true) {
		t.Fatal("slow queue and failure must emit")
	}
	task.TraceRequestID = 32
	if !shouldTraceSend(task, 0, 0, false) {
		t.Fatal("normal 1/16 sample missing")
	}
	task.TraceRequestID = 31
	task.controlWaitSince = created.Add(50 * time.Millisecond)
	queue.traceTaskBlocked(task, "control", sendInflightTrace{Total: 6, Limit: 3, Classes: [3]int32{3, 3, 0}}, created)
	queue.traceTaskBlocked(task, "control", sendInflightTrace{}, created.Add(25*time.Millisecond))
	if task.traceRequeues != 2 || !task.traceBlockedAt.Equal(created) {
		t.Fatal("blocked attempts not counted or per-task throttle failed")
	}
	queue.traceTaskOutcome(task, "done", "ok", created.Add(250*time.Millisecond), 17*time.Millisecond, false)
	lines := logger.GetBuffer()
	if len(lines) != 2 {
		t.Fatalf("want blocked+done lines, got %d", len(lines))
	}
	for _, field := range []string{"event=send_done", "node=node-A", "target=peer:19000", "kind=pull", "request=31", "height=987", "queue_ms=250", "wire_ms=17", "requeues=2", "result=ok", "target_wait_ms=200"} {
		if !strings.Contains(lines[1].Message, field) {
			t.Errorf("missing %q in %s", field, lines[1].Message)
		}
	}
	if !strings.Contains(lines[0].Message, "inflight_data=3") ||
		!strings.Contains(lines[0].Message, "result=class_target_limit") || strings.Contains(lines[1].Message, "secret") {
		t.Fatal("occupancy missing or payload leaked")
	}
	if sendTraceQueueWait(task, created.Add(-time.Second)) != 0 {
		t.Fatal("queue duration became negative")
	}
	for _, test := range []struct {
		err    error
		result string
	}{
		{nil, "ok"}, {context.DeadlineExceeded, "timeout"},
		{&httpStatusError{statusCode: 503, body: "secret body"}, "http_503"},
		{errors.New("secret error"), "error"},
	} {
		if got := sendTraceResult(test.err); got != test.result {
			t.Fatalf("unsafe/wrong result %q, want %q", got, test.result)
		}
	}
}
