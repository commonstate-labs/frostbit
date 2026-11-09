package stats

import (
	"sync"
	"time"
)

type Stats struct {
	statsLock     sync.RWMutex
	apiCallCounts map[string]uint64
	latency       *LatencyRecorder
}

func NewStats() *Stats {
	return &Stats{
		apiCallCounts: make(map[string]uint64),
		latency:       NewLatencyRecorder(4096),
	}
}

                  
func (h *Stats) RecordAPICall(apiName string) {
	h.statsLock.Lock()
	defer h.statsLock.Unlock()

	if h.apiCallCounts == nil {
		h.apiCallCounts = make(map[string]uint64)
	}
	h.apiCallCounts[apiName]++
}

                        
func (h *Stats) GetAPICallStats() map[string]uint64 {
	h.statsLock.RLock()
	defer h.statsLock.RUnlock()

	                     
	stats := make(map[string]uint64)
	for api, count := range h.apiCallCounts {
		stats[api] = count
	}
	return stats
}

               
func (h *Stats) RecordLatency(name string, d time.Duration) {
	if h == nil || name == "" {
		return
	}

	h.statsLock.RLock()
	rec := h.latency
	h.statsLock.RUnlock()
	if rec == nil {
		h.statsLock.Lock()
		if h.latency == nil {
			h.latency = NewLatencyRecorder(4096)
		}
		rec = h.latency
		h.statsLock.Unlock()
	}

	rec.Record(name, d)
}

                                                                             
func (h *Stats) GetLatencyStats(reset bool) map[string]LatencySummary {
	if h == nil {
		return nil
	}
	h.statsLock.RLock()
	rec := h.latency
	h.statsLock.RUnlock()
	if rec == nil {
		return nil
	}
	return rec.Snapshot(reset)
}
