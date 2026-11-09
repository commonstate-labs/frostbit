package stats

import (
	"sort"
	"sync"
	"time"
)

const (
	defaultLatencySampleCapacity = 2048
	defaultLatencyMetricLimit    = 256

	                                                                          
	                                                                     
	LatencyOverflowMetricName = "_overflow"
)

                                                   
type LatencySummary struct {
	Count uint64        `json:"count"`
	P50   time.Duration `json:"p50"`
	P95   time.Duration `json:"p95"`
	P99   time.Duration `json:"p99"`
	Max   time.Duration `json:"max"`
}

type latencyMetric struct {
	samples []int64                            
	nextIdx int
	filled  bool
	count   uint64
	maxNs   int64
}

                                                                   
type LatencyRecorder struct {
	mu             sync.RWMutex
	capacity       int
	maxMetricNames int
	metrics        map[string]*latencyMetric
}

func NewLatencyRecorder(capacity int) *LatencyRecorder {
	return NewLatencyRecorderWithLimit(capacity, defaultLatencyMetricLimit)
}

                                                                             
                                                                             
                                                                               
func NewLatencyRecorderWithLimit(capacity, maxMetricNames int) *LatencyRecorder {
	if capacity <= 0 {
		capacity = defaultLatencySampleCapacity
	}
	if maxMetricNames <= 0 {
		maxMetricNames = defaultLatencyMetricLimit
	}
	return &LatencyRecorder{
		capacity:       capacity,
		maxMetricNames: maxMetricNames,
		metrics:        make(map[string]*latencyMetric),
	}
}

func (r *LatencyRecorder) Record(name string, d time.Duration) {
	if r == nil || name == "" {
		return
	}
	ns := d.Nanoseconds()
	if ns < 0 {
		ns = 0
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	metricName := r.metricNameLocked(name)
	m, ok := r.metrics[metricName]
	if !ok {
		m = &latencyMetric{
			samples: make([]int64, r.capacity),
		}
		r.metrics[metricName] = m
	}

	m.samples[m.nextIdx] = ns
	m.nextIdx = (m.nextIdx + 1) % len(m.samples)
	if m.nextIdx == 0 {
		m.filled = true
	}
	m.count++
	if ns > m.maxNs {
		m.maxNs = ns
	}
}

                                                                               
                                                                             
                                     
func (r *LatencyRecorder) metricNameLocked(name string) string {
	if _, ok := r.metrics[name]; ok {
		return name
	}
	if name == LatencyOverflowMetricName {
		return name
	}

	                                                                            
	                                                             
	_, hasOverflow := r.metrics[LatencyOverflowMetricName]
	ordinaryLimit := r.maxMetricNames
	if !hasOverflow {
		ordinaryLimit--
	}
	if len(r.metrics) >= ordinaryLimit {
		return LatencyOverflowMetricName
	}
	return name
}

                                                                                               
func (r *LatencyRecorder) Snapshot(reset bool) map[string]LatencySummary {
	if r == nil {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	result := make(map[string]LatencySummary, len(r.metrics))
	for name, m := range r.metrics {
		n := m.nextIdx
		if m.filled {
			n = len(m.samples)
		}
		if n == 0 {
			if reset {
				m.count = 0
				m.maxNs = 0
			}
			continue
		}

		values := make([]int64, n)
		copy(values, m.samples[:n])
		sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })

		summary := LatencySummary{
			Count: m.count,
			P50:   time.Duration(percentileValue(values, 0.50)),
			P95:   time.Duration(percentileValue(values, 0.95)),
			P99:   time.Duration(percentileValue(values, 0.99)),
			Max:   time.Duration(m.maxNs),
		}
		result[name] = summary

		if reset {
			m.nextIdx = 0
			m.filled = false
			m.count = 0
			m.maxNs = 0
		}
	}
	return result
}

func percentileValue(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 1 {
		return sorted[len(sorted)-1]
	}
	idx := int(float64(len(sorted)-1) * p)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
