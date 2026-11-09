package handlers

import (
	"context"
	"dex/logs"
	"dex/pb"
	"net"
	"net/http"
	"net/netip"
	"time"
)

type pushTraceKey struct{}

type pushTracePhase uint8

const (
	pushBody pushTracePhase = iota
	pushGate
	pushSourceGate
	pushDecode
	pushVerify
	pushAccepted
	pushReplay
	pushAdapter
	pushEnqueue
	pushPhaseCount
)

                                                                                  
                                                                                   
type pushRequestTrace struct {
	started                     time.Time
	phases                      [pushPhaseCount]time.Duration
	node, sourceIP, peer, stage string
	request                     uint32
	height                      uint64
	bodyBytes, containerBytes   int
	verified                    bool
}

func newPushRequestTrace(node, remote string) *pushRequestTrace {
	trace := &pushRequestTrace{started: time.Now(), node: node, sourceIP: "invalid", peer: "unverified", stage: "admission", bodyBytes: -1, containerBytes: -1}
	for i := range trace.phases {
		trace.phases[i] = -1
	}
	if host, _, err := net.SplitHostPort(remote); err == nil {
		if ip, err := netip.ParseAddr(host); err == nil {
			trace.sourceIP = ip.WithZone("").Unmap().String()
		}
	}
	return trace
}

func pushTraceFromRequest(r *http.Request) *pushRequestTrace {
	trace, _ := r.Context().Value(pushTraceKey{}).(*pushRequestTrace)
	return trace
}

func (trace *pushRequestTrace) begin(stage string) time.Time {
	if trace == nil {
		return time.Time{}
	}
	trace.stage = stage
	return time.Now()
}

func (trace *pushRequestTrace) end(phase pushTracePhase, started time.Time) {
	if trace != nil {
		trace.phases[phase] = time.Since(started)
	}
}

func (trace *pushRequestTrace) query(msg *pb.PushQuery, verified bool) {
	if trace == nil || msg == nil {
		return
	}
	trace.request, trace.height, trace.containerBytes = msg.RequestId, msg.RequestedHeight, len(msg.Container)
	trace.verified = verified
	if verified {
		                                                                         
		trace.peer = msg.Address[:min(len(msg.Address), 128)]
	}
}

func pushPhaseMS(d time.Duration) float64 {
	if d < 0 {
		return -1
	}
	return float64(d) / float64(time.Millisecond)
}

func (trace *pushRequestTrace) finish(status int) {
	elapsed := time.Since(trace.started)
	if status != 0 && status < 400 && elapsed < 200*time.Millisecond && trace.request%16 != 0 {
		return
	}
	trust := "unverified"
	if trace.verified {
		trust = "verified"
	}
	                                                                         
	                                                                         
	logs.ConsensusTracef("event=push_rx node=%q source_ip=%s peer=%q request=%d height=%d query_fields=%s body_bytes=%d container_bytes=%d body_ms=%.3f gate_ms=%.3f source_gate_ms=%.3f decode_ms=%.3f verify_ms=%.3f accepted_ms=%.3f replay_ms=%.3f adapter_ms=%.3f enqueue_ms=%.3f total_ms=%.3f status=%d stage=%s",
		trace.node, trace.sourceIP, trace.peer, trace.request, trace.height, trust, trace.bodyBytes, trace.containerBytes,
		pushPhaseMS(trace.phases[pushBody]), pushPhaseMS(trace.phases[pushGate]), pushPhaseMS(trace.phases[pushSourceGate]),
		pushPhaseMS(trace.phases[pushDecode]), pushPhaseMS(trace.phases[pushVerify]), pushPhaseMS(trace.phases[pushAccepted]),
		pushPhaseMS(trace.phases[pushReplay]), pushPhaseMS(trace.phases[pushAdapter]), pushPhaseMS(trace.phases[pushEnqueue]),
		pushPhaseMS(elapsed), status, trace.stage)
}

                                                                           
                                                                                 
type pushTraceResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *pushTraceResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *pushTraceResponseWriter) WriteHeader(status int) {
	if w.status == 0 && status >= 200 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *pushTraceResponseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(body)
}

func (hm *HandlerManager) startPushRequestTrace(path string, w http.ResponseWriter, r *http.Request) (http.ResponseWriter, *http.Request, func()) {
	if path != "/pushquery" || !logs.ConsensusTraceEnabled() {
		return w, r, nil
	}
	trace := newPushRequestTrace(hm.address, r.RemoteAddr)
	recorder := &pushTraceResponseWriter{ResponseWriter: w}
	r = r.WithContext(context.WithValue(r.Context(), pushTraceKey{}, trace))
	return recorder, r, func() {
		trace.finish(recorder.status)
	}
}
