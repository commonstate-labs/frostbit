package sender

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dex/config"
	"dex/logs"
	"dex/pb"
)

func TestLiveBlockTransfersDoNotOccupyChitQueue(t *testing.T) {
	cfg := config.DefaultConfig()
	queue := &SendQueue{
		controlQueue: newControlTaskQueue(2), immediateChan: make(chan *SendTask, 2),
		Logger: logs.NewNodeLogger(t.Name(), 0),
	}
	sm := &SenderManager{address: "self", cfg: cfg, SendQueue: queue}
	if err := sm.PushQueryWithFailure("127.0.0.1:19000", &pb.PushQuery{}, nil); err != nil {
		t.Fatal(err)
	}
	push := <-queue.immediateChan
	if push.Timeout < 5*time.Second || push.MaxRetries != 0 {
		t.Fatalf("PushQuery deadline/retry policy: timeout=%v retries=%d", push.Timeout, push.MaxRetries)
	}
	sm.PullGet("127.0.0.1:19000", "block-1", nil)
	get := <-queue.immediateChan
	if get.Timeout != liveBlockTaskTimeout || get.MaxRetries != 0 {
		t.Fatalf("PullGet deadline/retry policy: timeout=%v retries=%d", get.Timeout, get.MaxRetries)
	}
	                                                                                                
	queue.immediateChan <- push
	queue.immediateChan <- get
	if err := sm.PullQueryWithFailure("127.0.0.1:19000", &pb.PullQuery{}, nil); err != nil {
		t.Fatal(err)
	}
	if err := sm.SendChits("127.0.0.1:19000", &pb.Chits{}); err != nil {
		t.Fatal(err)
	}
	if queue.ControlQueueLen() != 2 {
		t.Fatal("large transfers blocked the control queue")
	}
	for i := 0; i < 2; i++ {
		task := queue.controlQueue.pop(controlTaskReady)
		if task.Timeout != cfg.Sender.ControlTaskTimeout || task.MaxRetries != 0 {
			t.Fatalf("small control request policy changed: timeout=%v retries=%d", task.Timeout, task.MaxRetries)
		}
	}
}

func TestLargePushQuerySurvivesOldControlDeadlineOverHTTPS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("request read: %v", err)
			return
		}
		var query pb.PushQuery
		if err := pb.UnmarshalSanitize(body, &query); err != nil || len(query.Container) != pb.MaxTotalSize {
			t.Errorf("maximum query decode: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		timer := time.NewTimer(1650 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	queue := &SendQueue{immediateChan: make(chan *SendTask, 1)}
	sm := &SenderManager{address: "self", cfg: config.DefaultConfig(), SendQueue: queue}
	query := &pb.PushQuery{Container: make([]byte, pb.MaxTotalSize)}
	if err := sm.PushQueryWithFailure(strings.TrimPrefix(server.URL, "https://"), query, nil); err != nil {
		t.Fatal(err)
	}
	task := <-queue.immediateChan
	if err := task.SendFunc(task, server.Client()); err != nil {
		t.Fatalf("legal large block failed after old 1.5s control deadline: %v", err)
	}
}

func TestLiveBlockServeIndependentBoundedAndReusable(t *testing.T) {
	sm := &SenderManager{}
	var historyReleases []func()
	for _, ip := range []string{"192.0.2.1", "192.0.2.2"} {
		release, ok := sm.AcquirePrivilegedArchiveServe(ip)
		if !ok {
			t.Fatal("cannot fill historical sync pool")
		}
		t.Cleanup(release)
		historyReleases = append(historyReleases, release)
	}
	for _, bad := range []string{"", "node.example", "192.0.2.1:19000"} {
		if release, ok := sm.AcquireLiveBlockServe(bad); ok {
			release()
			t.Fatalf("invalid source %q was admitted", bad)
		}
	}

	                                                                                                
	var liveReleases []func()
	for i := 1; i <= 20; i++ {
		ip := fmt.Sprintf("192.0.2.%d", i)
		release, ok := sm.AcquireLiveBlockServe(ip)
		if !ok {
			t.Fatalf("live response %d was rejected while history pool was full", i)
		}
		t.Cleanup(release)
		liveReleases = append(liveReleases, release)
		                                                                                           
		for _, alias := range []string{ip, "::ffff:" + ip} {
			if duplicate, ok := sm.AcquireLiveBlockServe(alias); ok {
				duplicate()
				t.Fatalf("source %q bypassed live per-source limit", alias)
			}
		}
	}
	if release, ok := sm.AcquireLiveBlockServe("192.0.2.21"); ok {
		release()
		t.Fatal("21st live response exceeded the global limit of 20")
	}
	if release, ok := sm.AcquirePrivilegedArchiveServe("192.0.2.3"); ok {
		release()
		t.Fatal("live capacity change expanded the historical sync pool")
	}
	historyReleases[0]()
	historyReplacement, ok := sm.AcquirePrivilegedArchiveServe("192.0.2.3")
	if !ok {
		t.Fatal("full live pool blocked reuse of an independent history slot")
	}
	t.Cleanup(historyReplacement)

	liveReleases[0]()
	liveReleases[0]()                                              
	replacement, ok := sm.AcquireLiveBlockServe("::ffff:192.0.2.1")
	if !ok {
		t.Fatal("released live source/slot could not be reused through its IP alias")
	}
	t.Cleanup(replacement)
	if release, ok := sm.AcquireLiveBlockServe("192.0.2.21"); ok {
		release()
		t.Fatal("duplicate release incorrectly returned more than one live slot")
	}
	replacement()
	last, ok := sm.AcquireLiveBlockServe("192.0.2.21")
	if !ok {
		t.Fatal("released live slot could not be reused by a different source")
	}
	t.Cleanup(last)
}
