package sender

import (
	"dex/logs"
	"net/http"
	"net/http/httptrace"
	"sync"
	"time"
)

                                                                                 
                                                                             
type pushHTTPTrace struct {
	mu      sync.Mutex
	started time.Time
	state   pushHTTPTraceSnapshot
}

type pushHTTPTraceSnapshot struct {
	gotConn, wroteHeaders, wroteRequest, firstByte, response time.Duration
	reused                                                   bool
	status                                                   int
	uploadResult                                             string
}

func attachPushHTTPTrace(task *SendTask, req *http.Request) (*http.Request, *pushHTTPTrace) {
	if task == nil || task.TraceKind != "push" || !logs.ConsensusTraceEnabled() {
		return req, nil
	}
	trace := &pushHTTPTrace{started: time.Now(), state: pushHTTPTraceSnapshot{
		gotConn: -1, wroteHeaders: -1, wroteRequest: -1, firstByte: -1, response: -1, uploadResult: "unknown",
	}}
	hooks := &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			trace.mu.Lock()
			trace.state.gotConn, trace.state.reused = time.Since(trace.started), info.Reused
			trace.mu.Unlock()
		},
		WroteHeaders: func() {
			trace.mu.Lock()
			trace.state.wroteHeaders = time.Since(trace.started)
			trace.mu.Unlock()
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			trace.mu.Lock()
			trace.state.wroteRequest, trace.state.uploadResult = time.Since(trace.started), sendTraceResult(info.Err)
			trace.mu.Unlock()
		},
		GotFirstResponseByte: func() {
			trace.mu.Lock()
			if trace.state.firstByte < 0 {
				trace.state.firstByte = time.Since(trace.started)
			}
			trace.mu.Unlock()
		},
	}
	return req.WithContext(httptrace.WithClientTrace(req.Context(), hooks)), trace
}

func (trace *pushHTTPTrace) gotResponse(resp *http.Response) {
	if trace == nil || resp == nil {
		return
	}
	trace.mu.Lock()
	trace.state.response, trace.state.status = time.Since(trace.started), resp.StatusCode
	trace.mu.Unlock()
}

func (trace *pushHTTPTrace) snapshot() pushHTTPTraceSnapshot {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	return trace.state
}

func pushTraceOffsetMS(d time.Duration) float64 {
	if d < 0 {
		return -1
	}
	return float64(d) / float64(time.Millisecond)
}

func (trace *pushHTTPTrace) finish(task *SendTask, payloadBytes int, err error) {
	if trace == nil {
		return
	}
	elapsed := time.Since(trace.started)
	if !shouldTraceSend(task, 0, elapsed, err != nil) {
		return
	}
	s := trace.snapshot()
	                                                                          
	                                                                              
	                                                                          
	logs.ConsensusTracef("event=push_http node=%q target=%q request=%d height=%d bytes=%d got_conn_at_ms=%.3f wrote_headers_at_ms=%.3f wrote_request_at_ms=%.3f first_byte_at_ms=%.3f response_at_ms=%.3f total_ms=%.3f reused=%t upload_result=%s status=%d result=%s",
		task.NodeAddress, task.Target, task.TraceRequestID, task.TraceHeight, payloadBytes,
		pushTraceOffsetMS(s.gotConn), pushTraceOffsetMS(s.wroteHeaders), pushTraceOffsetMS(s.wroteRequest),
		pushTraceOffsetMS(s.firstByte), pushTraceOffsetMS(s.response), pushTraceOffsetMS(elapsed),
		s.reused, s.uploadResult, s.status, sendTraceResult(err))
}
