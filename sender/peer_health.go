package sender

import (
	"errors"
	"math"
	"net"
	"strings"
	"sync"
	"time"
)

const (
	peerHealthFailureThreshold = 2
	peerHealthBaseProbeDelay   = 10 * time.Second
	peerHealthMaxProbeDelay    = 90 * time.Second
)

type peerHealthTracker struct {
	mu     sync.Mutex
	states map[string]*peerHealthState
}

type peerHealthState struct {
	consecutiveFailures int
	nextProbeAt         time.Time
	probeInFlight       bool
}

func newPeerHealthTracker() *peerHealthTracker {
	return &peerHealthTracker{states: make(map[string]*peerHealthState)}
}

func (h *peerHealthTracker) shouldSkipAtEnqueue(target string, now time.Time) bool {
	if h == nil || target == "" {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.states[target]
	if st == nil || st.consecutiveFailures < peerHealthFailureThreshold {
		return false
	}
	return st.probeInFlight || now.Before(st.nextProbeAt)
}

func (h *peerHealthTracker) beginSend(target string, now time.Time) (skip bool, probe bool) {
	if h == nil || target == "" {
		return false, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.states[target]
	if st == nil || st.consecutiveFailures < peerHealthFailureThreshold {
		return false, false
	}
	if st.probeInFlight || now.Before(st.nextProbeAt) {
		return true, false
	}
	st.probeInFlight = true
	return false, true
}

func (h *peerHealthTracker) recordResult(target string, err error) {
	if h == nil || target == "" {
		return
	}
	unavailable := isPeerUnavailableSendError(err)
	h.mu.Lock()
	defer h.mu.Unlock()
	if !unavailable {
		delete(h.states, target)
		return
	}
	st := h.states[target]
	if st == nil {
		st = &peerHealthState{}
		h.states[target] = st
	}
	st.probeInFlight = false
	st.consecutiveFailures++
	if st.consecutiveFailures >= peerHealthFailureThreshold {
		st.nextProbeAt = time.Now().Add(peerHealthProbeDelay(st.consecutiveFailures))
	}
}

func peerHealthProbeDelay(failures int) time.Duration {
	if failures < peerHealthFailureThreshold {
		return 0
	}
	exp := failures - peerHealthFailureThreshold
	if exp > 6 {
		exp = 6
	}
	delay := time.Duration(float64(peerHealthBaseProbeDelay) * math.Pow(2, float64(exp)))
	if delay > peerHealthMaxProbeDelay {
		return peerHealthMaxProbeDelay
	}
	return delay
}

func isPeerUnavailableSendError(err error) bool {
	if err == nil {
		return false
	}
	if isTimeoutSendError(err) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	low := strings.ToLower(err.Error())
	for _, token := range []string{
		"connection refused",
		"connect: refused",
		"no route to host",
		"network is unreachable",
		"host is down",
		"host unreachable",
		"connection reset",
		"broken pipe",
		"server closed",
		"no recent network activity",
	} {
		if strings.Contains(low, token) {
			return true
		}
	}
	return false
}
