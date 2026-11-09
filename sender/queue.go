package sender

import (
	"dex/config"
	"dex/logs"
	"dex/stats"
	"errors"
	"fmt"
	"log"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

                           
type TaskPriority int

const (
	PriorityData      TaskPriority = iota                                  
	PriorityControl                                                     
	PriorityImmediate                                                                              
)

                                             
type SendTask struct {
	Target      string
	Message     interface{}
	RetryCount  int
	MaxRetries  int
	CreatedAt   time.Time                                           
	NextAttempt time.Time
	SendFunc    func(task *SendTask, client *http.Client) error
	HttpClient  *http.Client
	Priority    TaskPriority                   
	NodeAddress string                                              
	Timeout     time.Duration
	OnDone      func()
	OnFailure   func(error)
	doneOnce    sync.Once

	                                                                         
	TraceKind        string
	TraceRequestID   uint32
	TraceHeight      uint64
	traceRequeues    uint32
	traceBlockedAt   time.Time
	controlWaitSince time.Time
}

func (task *SendTask) finish() {
	if task == nil {
		return
	}
	task.doneOnce.Do(func() {
		if task.OnDone != nil {
			task.OnDone()
		}
	})
}

func (task *SendTask) fail(err error) {
	if task == nil {
		return
	}
	task.doneOnce.Do(func() {
		if task.OnFailure != nil {
			task.OnFailure(err)
		}
		if task.OnDone != nil {
			task.OnDone()
		}
	})
}

                                              
                                                                                                   
type SendQueue struct {
	controlWorkerCount int
	dataWorkerCount    int
	controlQueue       *controlTaskQueue                                                                
	immediateChan      chan *SendTask                                              
	dataChan           chan *SendTask                                         
	stopChan           chan struct{}
	stopOnce           sync.Once
	wg                 sync.WaitGroup
	httpClient         *http.Client
	cfg                *config.Config
	nodeID             int                                                   
	address            string                          
	Logger             logs.Logger                         
	InflightMap        map[string]int32                                                   
	InflightMutex      sync.RWMutex
	inflightByClass    map[string][3]int32                                                              
	latency            *stats.LatencyRecorder

	                                                                          
	delayedTimerBacklog atomic.Int64
	nextAttemptSleeping atomic.Int64
	dropImmediateFull   atomic.Uint64
	dropControlFull     atomic.Uint64
	dropDataFull        atomic.Uint64
	dropStaleWorker     atomic.Uint64
	dropStaleOverload   atomic.Uint64
	inflightRequeue     atomic.Uint64
	controlTargetWaits  atomic.Uint64
	retryExhausted      atomic.Uint64
	retryExpired        atomic.Uint64
	sendSuccess         atomic.Uint64
	sendError           atomic.Uint64
	sendTimeout         atomic.Uint64
	healthSkipped       atomic.Uint64
	peerHealth          *peerHealthTracker
}

                                                
                                                                  
                                                                
func NewSendQueue(
	workerCount, queueCapacity int,
	httpClient *http.Client,
	nodeID int,
	address string,
	logger logs.Logger,
	cfg *config.Config,
) *SendQueue {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}

	                                                                        
	controlWorkers := workerCount / 3
	if controlWorkers < 1 {
		controlWorkers = 1
	}
	dataWorkers := workerCount - controlWorkers
	if dataWorkers < 1 {
		dataWorkers = 1
	}

	                                                               
	controlCapacity := queueCapacity / 4
	if controlCapacity < 64 {
		controlCapacity = 64
	}
	dataCapacity := queueCapacity - controlCapacity

	sq := &SendQueue{
		nodeID:             nodeID,
		address:            address,
		Logger:             logger,
		controlWorkerCount: controlWorkers,
		dataWorkerCount:    dataWorkers,
		controlQueue:       newControlTaskQueue(controlCapacity),
		immediateChan:      make(chan *SendTask, 1024),                            
		dataChan:           make(chan *SendTask, dataCapacity),
		stopChan:           make(chan struct{}),
		httpClient:         httpClient,
		cfg:                cfg,
		InflightMap:        make(map[string]int32),
		inflightByClass:    make(map[string][3]int32),
		latency:            stats.NewLatencyRecorder(4096),
		peerHealth:         newPeerHealthTracker(),
	}
	sq.Start()
	return sq
}

                             
func (sq *SendQueue) Start() {
	                         
	sq.wg.Add(sq.controlWorkerCount)
	for i := 0; i < sq.controlWorkerCount; i++ {
		go sq.controlWorkerLoop(i)
	}

	                                                                                
	const immediateWorkers = 8
	sq.wg.Add(immediateWorkers)
	for i := 0; i < immediateWorkers; i++ {
		go sq.workerLoop(900+i, sq.immediateChan, "immediate")
	}

	                         
	sq.wg.Add(sq.dataWorkerCount)
	for i := 0; i < sq.dataWorkerCount; i++ {
		go sq.workerLoop(i, sq.dataChan, "data")
	}

	sq.Logger.Verbose("[SendQueue] Started with %d control workers + %d data workers",
		sq.controlWorkerCount, sq.dataWorkerCount)
}

                                              
func (sq *SendQueue) Stop() {
	var pending []*SendTask
	sq.stopOnce.Do(func() {
		close(sq.stopChan)
		if sq.controlQueue != nil {
			pending = sq.controlQueue.close()
		}
	})
	for _, task := range pending {
		sq.traceTaskOutcome(task, "drop", "queue_stopped", time.Now(), 0, true)
		task.fail(fmt.Errorf("send queue stopped before control task was sent"))
	}
	sq.wg.Wait()
	log.Println("[SendQueue] Stopped.")
}

func (sq *SendQueue) Enqueue(task *SendTask) bool {
	if task == nil {
		return false
	}
	select {
	case <-sq.stopChan:
		task.fail(fmt.Errorf("send queue stopped"))
		return false
	default:
	}
	                                    
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now()
	}
	if task.NextAttempt.IsZero() {
		task.NextAttempt = time.Now()
	}
	now := time.Now()
	if sq.shouldSkipForPeerHealth(task, now, "enqueue") {
		return false
	}
	if task.NextAttempt.After(now) {
		                                                               
		                                                                
		if sq.delayedTimerBacklog.Load() > 5000 {
			sq.traceTaskOutcome(task, "drop", "delayed_queue_full", time.Now(), 0, true)
			sq.Logger.Debug("[SendQueue] Too many delayed timers (%d), dropping task target=%s func=%s",
				sq.delayedTimerBacklog.Load(), task.Target, task.FuncName())
			task.fail(fmt.Errorf("send queue delayed backlog full"))
			return false
		}
		delay := time.Until(task.NextAttempt)
		sq.delayedTimerBacklog.Add(1)
		go func(t *SendTask, d time.Duration) {
			defer sq.delayedTimerBacklog.Add(-1)
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-timer.C:
				sq.enqueueNow(t)
			case <-sq.stopChan:
				sq.traceTaskOutcome(t, "drop", "queue_stopped", time.Now(), 0, true)
				t.fail(fmt.Errorf("send queue stopped before delayed task was admitted"))
				return
			}
		}(task, delay)
		return true
	}
	return sq.enqueueNow(task)
}

func (sq *SendQueue) enqueueNow(task *SendTask) bool {
	if task == nil {
		return false
	}

	if task.Priority == PriorityImmediate {
		                                                                                    
		select {
		case sq.immediateChan <- task:
			return true
		default:
			                                                                                                                        
			sq.dropImmediateFull.Add(1)
			sq.traceTaskOutcome(task, "drop", "immediate_queue_full", time.Now(), 0, true)
			sq.Logger.Warn("[SendQueue] Immediate task DROPPED: queue full target=%s", task.Target)
			task.fail(fmt.Errorf("send queue immediate channel full"))
			return false
		}
	} else if task.Priority == PriorityControl {
		                                                                                    
		task.controlWaitSince = time.Time{}
		if sq.controlQueue.push(task) {
			return true
		}
		sq.dropControlFull.Add(1)
		sq.traceTaskOutcome(task, "drop", "control_queue_full_or_stopped", time.Now(), 0, true)
		sq.Logger.Warn("[SendQueue] Control queue full or stopped, dropping task target=%s func=%s len=%d",
			task.Target, task.FuncName(), sq.ControlQueueLen())
		task.fail(fmt.Errorf("send queue control queue full or stopped"))
		return false
	} else {
		                                                               
		select {
		case sq.dataChan <- task:
			               
			return true
		default:
			sq.dropDataFull.Add(1)
			sq.traceTaskOutcome(task, "drop", "data_queue_full", time.Now(), 0, true)
			sq.Logger.Debug("[SendQueue] Data task dropped: queue full len=%d, target=%s",
				len(sq.dataChan), task.Target)
			task.fail(fmt.Errorf("send queue data channel full"))
			return false
		}
	}
}

                                                                                         
func (sq *SendQueue) workerLoop(workerID int, taskChan chan *SendTask, queueType string) {
	defer sq.wg.Done()
	cfg := sq.cfg

	for {
		select {
		case <-sq.stopChan:
			return
		case task := <-taskChan:
			if task == nil {
				return
			}

			                                                                          
			                                                                          
			taskAge := time.Since(task.CreatedAt)
			if taskAge > cfg.Sender.TaskExpireTimeout {
				sq.dropExpiredTask(task, queueType)
				continue
			}

			now := time.Now()
			if task.NextAttempt.After(now) {
				sleepDur := task.NextAttempt.Sub(now)
				sq.nextAttemptSleeping.Add(1)
				time.Sleep(sleepDur)
				sq.nextAttemptSleeping.Add(-1)
			}

			acquired, occupied := sq.tryAcquireInflightObserved(task.Target, queueType)
			if !acquired {
				sq.traceTaskBlocked(task, queueType, occupied, time.Now())
				                                                                                          
				delay := cfg.Sender.InflightRequeueDelay
				if delay <= 0 {
					delay = 25 * time.Millisecond
				}
				time.Sleep(delay)
				sq.inflightRequeue.Add(1)
				select {
				case taskChan <- task:
				default:
					sq.traceTaskOutcome(task, "drop", "overload_requeue_full", time.Now(), 0, true)
					sq.Logger.Debug("[SendQueue][%s] Drop overload-requeue: chan full target=%s func=%s",
						queueType, task.Target, task.FuncName())
					task.fail(fmt.Errorf("send queue %s overload requeue full", queueType))
				}
				continue
			}

			sq.sendAcquiredTask(task, workerID, queueType)
		}
	}
}

func (sq *SendQueue) dropExpiredTask(task *SendTask, queueType string) {
	sq.traceTaskOutcome(task, "drop", "task_expired", time.Now(), 0, true)
	sq.dropStaleWorker.Add(1)
	task.fail(fmt.Errorf("send task expired in %s queue after %v", queueType, time.Since(task.CreatedAt)))
}

                                                                       
func (sq *SendQueue) sendAcquiredTask(task *SendTask, workerID int, queueType string) {
	if sq.shouldSkipForPeerHealth(task, time.Now(), queueType) {
		sq.releaseInflight(task.Target, queueType)
		return
	}
	err := sq.doSend(task, workerID, queueType)
	sq.recordPeerHealthResult(task, err)
	sq.releaseInflight(task.Target, queueType)
	if err != nil {
		if !sq.handleRetry(task, err) {
			task.fail(err)
		}
		return
	}
	task.finish()
}

func (sq *SendQueue) shouldSkipForPeerHealth(task *SendTask, now time.Time, queueType string) bool {
	if sq == nil || task == nil || task.Target == "" || sq.peerHealth == nil {
		return false
	}
	if queueType == "enqueue" {
		if !sq.peerHealth.shouldSkipAtEnqueue(task.Target, now) {
			return false
		}
	} else {
		skip, _ := sq.peerHealth.beginSend(task.Target, now)
		if !skip {
			return false
		}
	}
	sq.healthSkipped.Add(1)
	sq.traceTaskOutcome(task, "drop", "peer_health_skip", now, 0, true)
	sq.Logger.Debug("[SendQueue][%s] peer health skip target=%s func=%s", queueType, task.Target, task.FuncName())
	task.fail(fmt.Errorf("send skipped by peer health gate"))
	return true
}

func (sq *SendQueue) recordPeerHealthResult(task *SendTask, err error) {
	if sq == nil || task == nil || sq.peerHealth == nil {
		return
	}
	sq.peerHealth.recordResult(task.Target, err)
}

func (sq *SendQueue) maxInflightPerTarget(queueType string) int {
	if sq == nil || sq.cfg == nil {
		return 0
	}
	switch queueType {
	case "control":
		return sq.cfg.Sender.ControlMaxInflightPerTarget
	case "immediate":
		return sq.cfg.Sender.ImmediateMaxInflightPerTarget
	default:
		return sq.cfg.Sender.DataMaxInflightPerTarget
	}
}

func (sq *SendQueue) tryAcquireInflight(target, queueType string) bool {
	acquired, _ := sq.tryAcquireInflightObserved(target, queueType)
	return acquired
}

func (sq *SendQueue) tryAcquireInflightObserved(target, queueType string) (bool, sendInflightTrace) {
	limit := sq.maxInflightPerTarget(queueType)

	sq.InflightMutex.Lock()
	defer sq.InflightMutex.Unlock()

	current := sq.InflightMap[target]
	classes := sq.inflightByClass[target]
	class := sendQueueClass(queueType)
	occupied := sendInflightTrace{Total: current, Limit: limit, Classes: classes}
	if limit > 0 && int(classes[class]) >= limit {
		return false, occupied
	}
	if sq.InflightMap == nil {
		sq.InflightMap = make(map[string]int32)
	}
	if sq.inflightByClass == nil {
		sq.inflightByClass = make(map[string][3]int32)
	}
	sq.InflightMap[target] = current + 1
	classes[class]++
	sq.inflightByClass[target] = classes
	return true, occupied
}

func (sq *SendQueue) releaseInflight(target, queueType string) {
	sq.InflightMutex.Lock()

	classes := sq.inflightByClass[target]
	class := sendQueueClass(queueType)
	if classes[class] == 0 {
		sq.InflightMutex.Unlock()
		return                                                                          
	}
	classes[class]--
	sq.inflightByClass[target] = classes
	sq.InflightMap[target]--
	if sq.InflightMap[target] <= 0 {
		delete(sq.InflightMap, target)
		delete(sq.inflightByClass, target)
	}
	sq.InflightMutex.Unlock()
	                                                                           
	                                                                            
	if queueType == "control" && sq.controlQueue != nil {
		sq.controlQueue.notify()
	}
}

func (sq *SendQueue) requeueForTargetOverload(task *SendTask, queueType string) {
	if task == nil {
		return
	}
	cfg := sq.cfg
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	if time.Since(task.CreatedAt) > cfg.Sender.TaskExpireTimeout {
		sq.traceTaskOutcome(task, "drop", "overload_expired", time.Now(), 0, true)
		sq.dropStaleOverload.Add(1)
		sq.Logger.Debug("[SendQueue][%s] Drop stale overloaded task: age=%v target=%s func=%s",
			queueType, time.Since(task.CreatedAt), task.Target, task.FuncName())
		task.fail(fmt.Errorf("send task expired while target was overloaded"))
		return
	}

	delay := cfg.Sender.InflightRequeueDelay
	if delay <= 0 {
		delay = 25 * time.Millisecond
	}
	                                                      
	jitterRange := float64(delay) * 0.2
	jitter := time.Duration(jitterRange * (rand.Float64()*2 - 1))
	task.NextAttempt = time.Now().Add(delay + jitter)
	sq.inflightRequeue.Add(1)
	sq.Enqueue(task)
}

func (sq *SendQueue) doSend(task *SendTask, workerID int, queueType string) error {
	if task.SendFunc == nil {
		sq.traceTaskOutcome(task, "drop", "missing_send_func", time.Now(), 0, true)
		return fmt.Errorf("SendFunc is nil, cannot send")
	}
	                                 
	task.HttpClient = sq.httpClient

	start := time.Now()
	sq.traceTaskOutcome(task, "start", "sending", start, 0, false)
	wireStart := time.Now()
	err := task.SendFunc(task, sq.httpClient)
	elapsed := time.Since(wireStart)
	if task.TraceKind != "" && logs.ConsensusTraceEnabled() {
		sq.traceTaskOutcome(task, "done", sendTraceResult(err), start, elapsed, err != nil)
	}
	if sq.latency != nil {
		sq.latency.Record("sendqueue.do_send."+queueType, elapsed)
		if funcName := compactFuncName(task.FuncName()); funcName != "" {
			sq.latency.Record("sendqueue.do_send.func."+funcName, elapsed)
		}
	}

	if err != nil {
		sq.sendError.Add(1)
		if isTimeoutSendError(err) {
			sq.sendTimeout.Add(1)
		}
		if isBackpressureSendError(err) {
			sq.Logger.Debug("[SendQueue][%s] worker=%d,%s target=%s backpressure after %v: %v",
				queueType, workerID, task.FuncName(), task.Target, elapsed, err)
		} else {
			sq.Logger.Error("[SendQueue][%s] worker=%d,%s send to %s FAILED after %v: %v",
				queueType, workerID, task.FuncName(), task.Target, elapsed, err)
		}
	} else {
		sq.sendSuccess.Add(1)
		sq.Logger.Trace("[SendQueue][%s] worker=%d,%s send to %s success in %v",
			queueType, workerID, task.FuncName(), task.Target, elapsed)
	}
	return err
}

func (sq *SendQueue) handleRetry(task *SendTask, sendErr error) bool {
	if isNonRetriableSendError(sendErr) {
		sq.Logger.Debug("[SendQueue] Non-retriable send error target=%s func=%s: %v",
			task.Target, task.FuncName(), sendErr)
		return false
	}

	task.RetryCount++
	cfg := sq.cfg

	                                       
	if task.RetryCount > task.MaxRetries {
		sq.retryExhausted.Add(1)
		sq.Logger.Debug("[SendQueue] Exceed max retries(%d) target=%s func=%s, giving up",
			task.MaxRetries, task.Target, task.FuncName())
		return false
	}

	                                                                              
	if time.Since(task.CreatedAt) > cfg.Sender.TaskExpireTimeout {
		sq.traceTaskOutcome(task, "drop", "retry_expired", time.Now(), 0, true)
		sq.retryExpired.Add(1)
		sq.Logger.Debug("[SendQueue] Task expired after %v, giving up retry target=%s func=%s",
			time.Since(task.CreatedAt), task.Target, task.FuncName())
		return false
	}

	                              
	                                                         
	baseDelay := cfg.Sender.BaseRetryDelay
	backoff := baseDelay * time.Duration(math.Pow(2, float64(task.RetryCount-1)))

	                           
	if backoff > cfg.Sender.MaxRetryDelay {
		backoff = cfg.Sender.MaxRetryDelay
	}

	                                                     
	jitterRange := float64(backoff) * cfg.Sender.JitterFactor
	jitter := time.Duration(jitterRange * (rand.Float64()*2 - 1))                                 
	backoff += jitter

	task.NextAttempt = time.Now().Add(backoff)
	sq.Enqueue(task)
	sq.Logger.Debug("[SendQueue] Retry %d/%d after %v for %s (err=%v)",
		task.RetryCount, task.MaxRetries, backoff, task.Target, sendErr)
	return true
}

func isTimeoutSendError(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	low := strings.ToLower(err.Error())
	return strings.Contains(low, "timeout") || strings.Contains(low, "deadline exceeded")
}

func isBackpressureSendError(err error) bool {
	if err == nil {
		return false
	}
	var statusErr *httpStatusError
	if !errors.As(err, &statusErr) {
		return false
	}
	return statusErr.statusCode == http.StatusTooManyRequests
}

func isNonRetriableSendError(err error) bool {
	if err == nil {
		return false
	}
	var statusErr *httpStatusError
	if !errors.As(err, &statusErr) {
		return false
	}
	                                                                             
	return statusErr.statusCode >= 400 && statusErr.statusCode < 500
}

func compactFuncName(full string) string {
	if full == "" {
		return ""
	}
	                                                            
	if idx := strings.LastIndex(full, "/"); idx >= 0 && idx < len(full)-1 {
		return full[idx+1:]
	}
	return full
}

type SendQueueRuntimeStats struct {
	DelayedTimerBacklog int64
	NextAttemptSleeping int64
	DropImmediateFull   uint64
	DropControlFull     uint64
	DropDataFull        uint64
	DropStaleWorker     uint64
	DropStaleOverload   uint64
	InflightRequeue     uint64
	ControlTargetWaits  uint64
	RetryExhausted      uint64
	RetryExpired        uint64
	SendSuccess         uint64
	SendError           uint64
	SendTimeout         uint64
	HealthSkipped       uint64
}

func (task *SendTask) FuncName() string {
	if task.SendFunc == nil {
		return ""
	}
	                                                                                  
	funcPtr := reflect.ValueOf(task.SendFunc).Pointer()
	f := runtime.FuncForPC(funcPtr)
	if f != nil {
		return f.Name()
	} else {
		return ""
	}
}

                                                            
func (sq *SendQueue) QueueLen() int {
	return sq.ControlQueueLen() + len(sq.dataChan)
}

                                              
func (sq *SendQueue) ControlQueueLen() int {
	return sq.controlQueue.len()
}

                                           
func (sq *SendQueue) DataQueueLen() int {
	return len(sq.dataChan)
}

                                             
func (sq *SendQueue) ImmediateQueueLen() int {
	return len(sq.immediateChan)
}

                                                       
func (sq *SendQueue) GetChannelStats() []stats.ChannelStat {
	controlCapacity := 0
	if sq.controlQueue != nil {
		controlCapacity = sq.controlQueue.capacity
	}
	return []stats.ChannelStat{
		                                                                      
		stats.NewChannelStat("controlChan", "SendQueue", sq.ControlQueueLen(), controlCapacity),
		stats.NewChannelStat("immediateChan", "SendQueue", len(sq.immediateChan), cap(sq.immediateChan)),
		stats.NewChannelStat("dataChan", "SendQueue", len(sq.dataChan), cap(sq.dataChan)),
	}
}

func (sq *SendQueue) GetRuntimeStats() SendQueueRuntimeStats {
	if sq == nil {
		return SendQueueRuntimeStats{}
	}
	return SendQueueRuntimeStats{
		DelayedTimerBacklog: sq.delayedTimerBacklog.Load(),
		NextAttemptSleeping: sq.nextAttemptSleeping.Load(),
		DropImmediateFull:   sq.dropImmediateFull.Load(),
		DropControlFull:     sq.dropControlFull.Load(),
		DropDataFull:        sq.dropDataFull.Load(),
		DropStaleWorker:     sq.dropStaleWorker.Load(),
		DropStaleOverload:   sq.dropStaleOverload.Load(),
		InflightRequeue:     sq.inflightRequeue.Load(),
		ControlTargetWaits:  sq.controlTargetWaits.Load(),
		RetryExhausted:      sq.retryExhausted.Load(),
		RetryExpired:        sq.retryExpired.Load(),
		SendSuccess:         sq.sendSuccess.Load(),
		SendError:           sq.sendError.Load(),
		SendTimeout:         sq.sendTimeout.Load(),
		HealthSkipped:       sq.healthSkipped.Load(),
	}
}

func (sq *SendQueue) GetLatencyStats(reset bool) map[string]stats.LatencySummary {
	if sq == nil || sq.latency == nil {
		return nil
	}
	return sq.latency.Snapshot(reset)
}
