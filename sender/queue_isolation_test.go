package sender

import (
	"context"
	"net/http"
	"runtime"
	"sync"
	"testing"
	"time"

	"dex/config"
	"dex/logs"
)

var inflightTestClasses = []string{"data", "control", "immediate"}

func TestInflightClassesHaveIndependentBoundedSlots(t *testing.T) {
	for _, first := range inflightTestClasses {
		t.Run(first+"_saturated_first", func(t *testing.T) {
			queue := newTestSendQueueWithLimits(3, 2, 6)
			order := []string{first}
			for _, kind := range inflightTestClasses {
				if kind != first {
					order = append(order, kind)
				}
			}
			for _, kind := range order {
				for i := 0; i < queue.maxInflightPerTarget(kind); i++ {
					if !queue.tryAcquireInflight("peer", kind) {
						t.Fatalf("%s slot %d blocked by another class", kind, i)
					}
				}
				if queue.tryAcquireInflight("peer", kind) {
					t.Fatalf("%s exceeded its own target limit", kind)
				}
			}
			if got := queue.InflightMap["peer"]; got != 11 {
				t.Fatalf("aggregate inflight = %d, want 6+3+2", got)
			}
			for _, kind := range order {
				for i := 0; i < queue.maxInflightPerTarget(kind); i++ {
					queue.releaseInflight("peer", kind)
				}
				remaining := queue.InflightMap["peer"]
				queue.releaseInflight("peer", kind)                                       
				if queue.InflightMap["peer"] != remaining {
					t.Fatalf("duplicate %s release consumed another class's slot", kind)
				}
				if !queue.tryAcquireInflight("peer", kind) {
					t.Fatalf("%s slot did not reopen after release", kind)
				}
				queue.releaseInflight("peer", kind)
			}
			if len(queue.InflightMap) != 0 || len(queue.inflightByClass) != 0 {
				t.Fatalf("released target retained counts: total=%v classes=%v", queue.InflightMap, queue.inflightByClass)
			}
		})
	}
}

func TestInflightClassesConcurrentLimitsAndAggregate(t *testing.T) {
	queue := newTestSendQueueWithLimits(3, 2, 6)
	var workers sync.WaitGroup
	for i := 0; i < 24; i++ {
		kind := inflightTestClasses[i%len(inflightTestClasses)]
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := 0; i < 100; i++ {
				acquired, occupied := queue.tryAcquireInflightObserved("peer", kind)
				if occupied.Total != occupied.Classes[0]+occupied.Classes[1]+occupied.Classes[2] {
					t.Errorf("aggregate was not atomic: %+v", occupied)
				}
				if acquired {
					if occupied.Classes[sendQueueClass(kind)] >= int32(occupied.Limit) {
						t.Errorf("%s exceeded limit: %+v", kind, occupied)
					}
					runtime.Gosched()
					queue.releaseInflight("peer", kind)
				} else {
					runtime.Gosched()
				}
			}
		}()
	}
	workers.Wait()
	if len(queue.InflightMap) != 0 || len(queue.inflightByClass) != 0 {
		t.Fatal("concurrent acquisition/release leaked counts")
	}
}

                                                                          
                                                                           
func TestSendWorkersDoNotWaitForOtherClassSlots(t *testing.T) {
	for _, held := range inflightTestClasses {
		for _, incoming := range inflightTestClasses {
			if held == incoming {
				continue
			}
			t.Run(held+"_does_not_block_"+incoming, func(t *testing.T) {
				cfg := config.DefaultConfig()
				queue := NewSendQueue(cfg.Sender.WorkerCount, cfg.Sender.QueueCapacity,
					&http.Client{}, 0, t.Name(), logs.NewNodeLogger(t.Name(), 0), cfg)
				release := make(chan struct{})
				defer queue.Stop()
				defer close(release)
				limit := queue.maxInflightPerTarget(held)
				heldStarted := make(chan struct{}, limit)
				for i := 0; i < limit; i++ {
					if !queue.Enqueue(&SendTask{
						Target: "same-peer", Priority: sendQueueClass(held),
						SendFunc: func(_ *SendTask, _ *http.Client) error {
							heldStarted <- struct{}{}
							<-release
							return nil
						},
					}) {
						t.Fatal("held task enqueue rejected")
					}
				}
				for i := 0; i < limit; i++ {
					select {
					case <-heldStarted:
					case <-time.After(time.Second):
						t.Fatal("held tasks failed to occupy their class slots")
					}
				}
				started := make(chan struct{}, 1)
				if !queue.Enqueue(&SendTask{
					Target: "same-peer", Priority: sendQueueClass(incoming),
					SendFunc: func(_ *SendTask, _ *http.Client) error {
						started <- struct{}{}
						return nil
					},
				}) {
					t.Fatal("incoming task enqueue rejected")
				}
				select {
				case <-started:
				case <-time.After(time.Second):
					t.Fatal("incoming class waited for unrelated target slots")
				}
				if queue.GetRuntimeStats().InflightRequeue != 0 {
					t.Fatal("another class caused unnecessary requeue")
				}
			})
		}
	}
}

func TestSendWorkersReleaseClassSlotsOnFailureAndHealthSkip(t *testing.T) {
	for _, kind := range inflightTestClasses {
		for _, healthSkip := range []bool{false, true} {
			name := kind + "_send_failure"
			if healthSkip {
				name = kind + "_health_skip"
			}
			t.Run(name, func(t *testing.T) {
				queue := newTestSendQueueWithLimits(1, 1, 1)
				queue.Logger = logs.NewNodeLogger(t.Name(), 0)
				queue.peerHealth = newPeerHealthTracker()
				queue.stopChan = make(chan struct{})
				tasks := make(chan *SendTask, 1)
				done := make(chan struct{})
				failure := make(chan error, 1)
				                                                                  
				                                                                    
				tasks <- &SendTask{
					Target: "peer", Priority: sendQueueClass(kind), CreatedAt: time.Now(),
					OnDone: func() { close(done) }, OnFailure: func(err error) { failure <- err },
					SendFunc: func(_ *SendTask, _ *http.Client) error {
						if healthSkip {
							t.Error("unhealthy peer reached SendFunc")
						}
						return context.DeadlineExceeded
					},
				}
				if healthSkip {
					queue.peerHealth.recordResult("peer", context.DeadlineExceeded)
					queue.peerHealth.recordResult("peer", context.DeadlineExceeded)
				}
				queue.wg.Add(1)
				if kind == "control" {
					queue.controlQueue = newControlTaskQueue(1)
					queue.controlQueue.push(<-tasks)
					go queue.controlWorkerLoop(0)
				} else {
					go queue.workerLoop(0, tasks, kind)
				}
				select {
				case <-done:
				case <-time.After(time.Second):
					queue.Stop()
					t.Fatal("failure did not complete")
				}
				queue.Stop()                                                     
				if len(failure) != 1 || len(queue.InflightMap) != 0 || len(queue.inflightByClass) != 0 {
					t.Fatalf("callback/count leak: failures=%d total=%v classes=%v", len(failure), queue.InflightMap, queue.inflightByClass)
				}
				stats := queue.GetRuntimeStats()
				if healthSkip && (stats.HealthSkipped != 1 || stats.SendError != 0) {
					t.Fatalf("health gate semantics changed: %+v", stats)
				}
				if !healthSkip && (stats.SendError != 1 || stats.SendTimeout != 1) {
					t.Fatalf("send failure semantics changed: %+v", stats)
				}
			})
		}
	}
}
