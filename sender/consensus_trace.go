package sender

import (
	"errors"
	"fmt"
	"time"

	"dex/logs"
)

                                                                              
                                                                               
type sendInflightTrace struct {
	Total   int32
	Limit   int
	Classes [3]int32                                                 
}

func sendQueueClass(queueType string) TaskPriority {
	switch queueType {
	case "control":
		return PriorityControl
	case "immediate":
		return PriorityImmediate
	default:
		return PriorityData
	}
}

func sendTraceQueue(priority TaskPriority) string {
	switch priority {
	case PriorityControl:
		return "control"
	case PriorityImmediate:
		return "immediate"
	default:
		return "data"
	}
}

func sendTraceQueueWait(task *SendTask, started time.Time) time.Duration {
	if task.CreatedAt.IsZero() || started.Before(task.CreatedAt) {
		return 0
	}
	return started.Sub(task.CreatedAt)
}

func shouldTraceSend(task *SendTask, queueWait, wire time.Duration, force bool) bool {
	return task.TraceKind != "" && (force || queueWait >= 200*time.Millisecond ||
		wire >= 200*time.Millisecond || task.TraceRequestID%16 == 0)
}

func (sq *SendQueue) traceTaskOutcome(task *SendTask, event, result string, started time.Time, wire time.Duration, force bool) {
	if task == nil || !logs.ConsensusTraceEnabled() {
		return
	}
	wait := sendTraceQueueWait(task, started)
	if !shouldTraceSend(task, wait, wire, force) {
		return
	}
	targetWait := time.Duration(0)
	if !task.controlWaitSince.IsZero() && started.After(task.controlWaitSince) {
		targetWait = started.Sub(task.controlWaitSince)
	}
	logs.ConsensusTracef("event=send_%s node=%s target=%s kind=%s request=%d height=%d queue=%s queue_ms=%d wire_ms=%d requeues=%d retry=%d result=%s queued_control=%d queued_data=%d queued_immediate=%d target_wait_ms=%d",
		event, task.NodeAddress, task.Target, task.TraceKind, task.TraceRequestID, task.TraceHeight,
		sendTraceQueue(task.Priority), wait.Milliseconds(), wire.Milliseconds(), task.traceRequeues,
		task.RetryCount, result, sq.ControlQueueLen(), len(sq.dataChan), len(sq.immediateChan), targetWait.Milliseconds())
}

func (sq *SendQueue) traceTaskBlocked(task *SendTask, queueType string, occupied sendInflightTrace, now time.Time) {
	if !logs.ConsensusTraceEnabled() || task.TraceKind == "" {
		return
	}
	                                                                          
	task.traceRequeues++
	if !task.traceBlockedAt.IsZero() && now.Sub(task.traceBlockedAt) < 200*time.Millisecond {
		return
	}
	task.traceBlockedAt = now
	logs.ConsensusTracef("event=send_blocked node=%s target=%s kind=%s request=%d height=%d queue=%s queue_ms=%d requeues=%d result=class_target_limit inflight_total=%d inflight_control=%d inflight_data=%d inflight_immediate=%d limit=%d",
		task.NodeAddress, task.Target, task.TraceKind, task.TraceRequestID, task.TraceHeight, queueType,
		sendTraceQueueWait(task, now).Milliseconds(), task.traceRequeues, occupied.Total,
		occupied.Classes[PriorityControl], occupied.Classes[PriorityData], occupied.Classes[PriorityImmediate], occupied.Limit)
}

                                                                          
func sendTraceResult(err error) string {
	if err == nil {
		return "ok"
	}
	var statusErr *httpStatusError
	if errors.As(err, &statusErr) {
		return fmt.Sprintf("http_%d", statusErr.statusCode)
	}
	if isTimeoutSendError(err) {
		return "timeout"
	}
	return "error"
}
