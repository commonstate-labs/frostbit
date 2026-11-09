package sender

import (
	"fmt"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dex/config"
	"dex/logs"
)

func controlTaskReady(*SendTask) bool { return true }

func newControlWorkerTestQueue(t *testing.T, workers, limit, capacity int) *SendQueue {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Sender.ControlMaxInflightPerTarget = limit
	cfg.Sender.InflightRequeueDelay = time.Hour                                     
	q := &SendQueue{
		controlQueue: newControlTaskQueue(capacity), controlWorkerCount: workers,
		stopChan: make(chan struct{}), cfg: cfg, httpClient: &http.Client{},
		Logger: logs.NewNodeLogger(t.Name(), 0), peerHealth: newPeerHealthTracker(),
	}
	q.wg.Add(workers)
	for i := 0; i < workers; i++ {
		go q.controlWorkerLoop(i)
	}
	t.Cleanup(q.Stop)
	return q
}

func awaitControlTest[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(3 * time.Second):
		t.Fatal("control queue did not make progress")
		var zero T
		return zero
	}
}

func waitControlCondition(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatal("control queue condition not reached")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestControlQueueOldestEligibleKeepsTargetOrderAndCapacity(t *testing.T) {
	q := newControlTaskQueue(4)
	var original []*SendTask
	for _, target := range []string{"B", "C", "B", "D"} {
		task := &SendTask{Target: target}
		original = append(original, task)
		if !q.push(task) {
			t.Fatal("unexpected rejection")
		}
	}
	if q.push(&SendTask{Target: "E"}) {
		t.Fatal("blocked tasks must count toward the fixed capacity")
	}
	ready := func(task *SendTask) bool { return task.Target != "B" }
	if q.pop(ready).Target != "C" || q.pop(ready).Target != "D" || q.len() != 2 {
		t.Fatal("blocked target prevented another target from progressing")
	}
	firstB := q.pop(controlTaskReady)
	secondB := q.pop(controlTaskReady)
	if firstB != original[0] || secondB != original[2] || q.len() != 0 {
		t.Fatal("pending tasks were lost or duplicated")
	}
	q.close()
	if q.pop(controlTaskReady) != nil || q.push(&SendTask{}) {
		t.Fatal("stopped queue must reject work and wake consumers")
	}
}

func TestControlQueueReleaseWakesOldRequestWithoutRequeue(t *testing.T) {
	q := newControlWorkerTestQueue(t, 2, 1, 32)
	if !q.tryAcquireInflight("B", "control") {
		t.Fatal("cannot hold B slot")
	}
	var releaseBOnce sync.Once
	releaseB := func() { releaseBOnce.Do(func() { q.releaseInflight("B", "control") }) }
	t.Cleanup(releaseB)
	startedB := make(chan struct{}, 1)
	if !q.Enqueue(&SendTask{Target: "B", Priority: PriorityControl,
		SendFunc: func(*SendTask, *http.Client) error { startedB <- struct{}{}; return nil },
	}) {
		t.Fatal("B enqueue failed")
	}
	waitControlCondition(t, func() bool { return q.GetRuntimeStats().ControlTargetWaits == 1 })

	heldC := make(chan struct{})
	var releaseCOnce sync.Once
	t.Cleanup(func() { releaseCOnce.Do(func() { close(heldC) }) })
	startedC := make(chan struct{}, 1)
	q.Enqueue(&SendTask{Target: "C", Priority: PriorityControl,
		SendFunc: func(*SendTask, *http.Client) error { startedC <- struct{}{}; <-heldC; return nil },
	})
	awaitControlTest(t, startedC)
	for i := 0; i < 10; i++ {
		q.Enqueue(&SendTask{Target: "C", Priority: PriorityControl,
			SendFunc: func(*SendTask, *http.Client) error { return nil },
		})
	}
	if q.ControlQueueLen() != 11 || q.GetChannelStats()[0].Len != 11 {
		t.Fatal("blocked messages disappeared from backpressure metrics")
	}
	releaseB()
	awaitControlTest(t, startedB)                                                    
	if q.GetRuntimeStats().InflightRequeue != 0 {
		t.Fatal("control dispatch re-enqueued a request")
	}
}

func TestControlQueueConcurrentWorkersBoundSlotsAndFinishOnce(t *testing.T) {
	q := newControlWorkerTestQueue(t, 8, 2, 256)
	var mu sync.Mutex
	active := make(map[string]int)
	peak, total := 0, 0
	const count = 200
	done := make(chan struct{}, count)
	var completions atomic.Int32
	for i := 0; i < count; i++ {
		if !q.Enqueue(&SendTask{Target: fmt.Sprintf("peer-%d", i%3), Priority: PriorityControl,
			SendFunc: func(task *SendTask, _ *http.Client) error {
				mu.Lock()
				active[task.Target]++
				total++
				peak = max(peak, total)
				if active[task.Target] > 2 || total > 8 {
					t.Error("target/worker limit exceeded")
				}
				mu.Unlock()
				time.Sleep(time.Millisecond)
				mu.Lock()
				active[task.Target]--
				total--
				mu.Unlock()
				return nil
			},
			OnDone: func() { completions.Add(1); done <- struct{}{} },
		}) {
			t.Fatal("unexpected full queue")
		}
	}
	for i := 0; i < count; i++ {
		awaitControlTest(t, done)
	}
	q.Stop()
	if completions.Load() != count || peak < 2 || len(q.InflightMap) != 0 || len(q.inflightByClass) != 0 {
		t.Fatal("callbacks or slots leaked, or dispatch became serial")
	}
}

func TestControlQueueExpiryNeedsNoSlotAndCallbackCanReenter(t *testing.T) {
	q := newControlWorkerTestQueue(t, 1, 1, 8)
	q.tryAcquireInflight("B", "control")
	t.Cleanup(func() { q.releaseInflight("B", "control") })
	done := make(chan struct{}, 1)
	var failures atomic.Int32
	q.Enqueue(&SendTask{Target: "B", Priority: PriorityControl, CreatedAt: time.Now().Add(-time.Hour),
		SendFunc: func(*SendTask, *http.Client) error { t.Error("sent expired task"); return nil },
		OnFailure: func(error) {
			failures.Add(1)
			q.Enqueue(&SendTask{Target: "C", Priority: PriorityControl,
				SendFunc: func(*SendTask, *http.Client) error { return nil },
				OnDone:   func() { done <- struct{}{} },
			})
		},
	})
	awaitControlTest(t, done)
	if failures.Load() != 1 || q.GetRuntimeStats().DropStaleWorker != 1 {
		t.Fatal("expired request was not completed exactly once")
	}
	q.InflightMutex.RLock()
	remaining := q.InflightMap["B"]
	q.InflightMutex.RUnlock()
	if remaining != 1 {
		t.Fatal("expiry consumed the manually held slot")
	}
}

func TestControlQueueWaitingRequestStillChecksHealth(t *testing.T) {
	q := newControlWorkerTestQueue(t, 1, 1, 8)
	q.tryAcquireInflight("B", "control")
	done := make(chan struct{}, 1)
	var failures atomic.Int32
	q.Enqueue(&SendTask{Target: "B", Priority: PriorityControl,
		SendFunc:  func(*SendTask, *http.Client) error { t.Error("unhealthy target sent"); return nil },
		OnFailure: func(error) { failures.Add(1) }, OnDone: func() { done <- struct{}{} },
	})
	waitControlCondition(t, func() bool { return q.GetRuntimeStats().ControlTargetWaits == 1 })
	q.peerHealth.recordResult("B", &fakeNetTimeout{})
	q.peerHealth.recordResult("B", &fakeNetTimeout{})
	q.releaseInflight("B", "control")
	awaitControlTest(t, done)
	q.Stop()
	if failures.Load() != 1 || q.GetRuntimeStats().HealthSkipped != 1 || len(q.InflightMap) != 0 {
		t.Fatal("health gate or release changed")
	}
}

func TestControlQueueStopDrainsWaitingCallbacksAndRejectsReentry(t *testing.T) {
	q := newControlWorkerTestQueue(t, 2, 1, 8)
	q.tryAcquireInflight("B", "control")
	t.Cleanup(func() { q.releaseInflight("B", "control") })
	var completed, failed atomic.Int32
	for i := 0; i < 8; i++ {
		q.Enqueue(&SendTask{Target: "B", Priority: PriorityControl,
			SendFunc: func(*SendTask, *http.Client) error { t.Error("blocked task sent"); return nil },
			OnFailure: func(error) {
				failed.Add(1)
				if q.Enqueue(&SendTask{Target: "C", Priority: PriorityControl}) {
					t.Error("stopped queue admitted a callback's new task")
				}
				_ = q.ControlQueueLen()
			},
			OnDone: func() { completed.Add(1) },
		})
	}
	q.Stop()
	q.Stop()
	if completed.Load() != 8 || failed.Load() != 8 || q.ControlQueueLen() != 0 {
		t.Fatal("stop lost or repeated pending callbacks")
	}
}

                                                                              
                                                                                
                                                                               
func TestControlQueueCapacityAudit(t *testing.T) {
	for _, profile := range []struct {
		workers, targetLimit int
		hotTarget            bool
	}{{5, 3, false}, {12, 3, false}, {12, 3, true}, {12, 6, true}} {
		t.Run(fmt.Sprintf("workers%d_target%d_hot%t", profile.workers, profile.targetLimit, profile.hotTarget), func(t *testing.T) {
			q := newControlWorkerTestQueue(t, profile.workers, profile.targetLimit, 64)
			var mu sync.Mutex
			var waits []float64
			done := make(chan struct{}, 48)
			begin := time.Now()
			for i := 0; i < 48; i++ {
				peer := i % 4
				if profile.hotTarget && i < 36 {
					peer = 0
				}
				if !q.Enqueue(&SendTask{Target: fmt.Sprint(peer), Priority: PriorityControl,
					SendFunc: func(task *SendTask, _ *http.Client) error {
						mu.Lock()
						waits = append(waits, float64(time.Since(task.CreatedAt))/float64(time.Millisecond))
						mu.Unlock()
						time.Sleep(160 * time.Millisecond)
						return nil
					}, OnDone: func() { done <- struct{}{} },
				}) {
					t.Fatal("fixed burst was rejected")
				}
			}
			for i := 0; i < 48; i++ {
				awaitControlTest(t, done)
			}
			q.Stop()
			sort.Float64s(waits)
			t.Logf("48 tasks; elapsed=%v queue_p50_ms=%.1f queue_p95_ms=%.1f requeues=%d",
				time.Since(begin), waits[24], waits[45], q.GetRuntimeStats().InflightRequeue)
			if len(waits) != 48 || q.GetRuntimeStats().InflightRequeue != 0 {
				t.Fatal("audit lost or requeued work")
			}
		})
	}
}
