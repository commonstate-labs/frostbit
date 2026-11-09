package stats

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

                                                                         
                                                               

                          

func TestNewChannelStat(t *testing.T) {
	cases := []struct {
		name      string
		length    int
		capacity  int
		wantUsage float64
	}{
		{"empty", 0, 100, 0.0},
		{"half", 50, 100, 0.5},
		{"full", 100, 100, 1.0},
		{"zero capacity safe", 0, 0, 0.0},
		{"capacity zero with len > 0 must not divide by zero", 5, 0, 0.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cs := NewChannelStat("ch", "mod", tc.length, tc.capacity)
			if cs.Usage != tc.wantUsage {
				t.Fatalf("usage=%v want %v", cs.Usage, tc.wantUsage)
			}
			if cs.Name != "ch" || cs.Module != "mod" || cs.Len != tc.length || cs.Cap != tc.capacity {
				t.Fatalf("field mismatch: %+v", cs)
			}
		})
	}
}

                    

func TestStats_RecordAPICallAndSnapshot(t *testing.T) {
	s := NewStats()
	s.RecordAPICall("get_block")
	s.RecordAPICall("get_block")
	s.RecordAPICall("get_tx")

	got := s.GetAPICallStats()
	if got["get_block"] != 2 || got["get_tx"] != 1 {
		t.Fatalf("counts wrong: %+v", got)
	}
	                                                      
	got["get_block"] = 999
	again := s.GetAPICallStats()
	if again["get_block"] != 2 {
		t.Fatal("GetAPICallStats must return a defensive copy")
	}
}

func TestStats_RecordLatency_NilOrEmptyNameNoOp(t *testing.T) {
	var s *Stats
	s.RecordLatency("x", time.Millisecond)                

	s2 := NewStats()
	s2.RecordLatency("", time.Millisecond)
	if got := s2.GetLatencyStats(false); len(got) != 0 {
		t.Fatalf("empty name must not record any sample, got %+v", got)
	}
}

func TestStats_LatencyConcurrent(t *testing.T) {
	s := NewStats()
	const N = 200
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			s.RecordAPICall("api")
			s.RecordLatency("op", time.Duration(i+1)*time.Microsecond)
		}(i)
	}
	wg.Wait()
	if s.GetAPICallStats()["api"] != N {
		t.Fatalf("expected %d calls, got %d", N, s.GetAPICallStats()["api"])
	}
	got := s.GetLatencyStats(false)
	if got["op"].Count != uint64(N) {
		t.Fatalf("latency count=%d want %d", got["op"].Count, N)
	}
}

                              

func TestLatencyRecorder_NilSafeAndDefaultCapacity(t *testing.T) {
	var r *LatencyRecorder
	r.Record("x", time.Millisecond)                             
	if r.Snapshot(false) != nil {
		t.Fatal("nil snapshot must be nil")
	}

	r2 := NewLatencyRecorder(0)                        
	for i := 0; i < 10; i++ {
		r2.Record("op", time.Duration(i+1)*time.Microsecond)
	}
	snap := r2.Snapshot(false)
	if snap["op"].Count != 10 {
		t.Fatalf("count=%d want 10", snap["op"].Count)
	}
}

func TestLatencyRecorder_RingBufferOverwrite(t *testing.T) {
	r := NewLatencyRecorder(4)
	                                                               
	for i := 1; i <= 6; i++ {
		r.Record("op", time.Duration(i)*time.Nanosecond)
	}
	snap := r.Snapshot(false)
	got := snap["op"]
	if got.Count != 6 {
		t.Fatalf("Count must be cumulative=6, got %d", got.Count)
	}
	                                           
	if got.Max != 6*time.Nanosecond {
		t.Fatalf("Max=%v want 6ns", got.Max)
	}
}

func TestLatencyRecorder_NegativeDurationClampedToZero(t *testing.T) {
	r := NewLatencyRecorder(8)
	r.Record("op", -5*time.Nanosecond)
	snap := r.Snapshot(false)
	if snap["op"].Max != 0 {
		t.Fatalf("negative duration must clamp to 0, got Max=%v", snap["op"].Max)
	}
}

func TestLatencyRecorder_SnapshotResetClearsBuffer(t *testing.T) {
	r := NewLatencyRecorder(4)
	for i := 1; i <= 3; i++ {
		r.Record("op", time.Duration(i)*time.Nanosecond)
	}
	first := r.Snapshot(true)
	if first["op"].Count != 3 {
		t.Fatalf("first count=%d want 3", first["op"].Count)
	}
	                                                                       
	second := r.Snapshot(false)
	if got, ok := second["op"]; ok && got.Count != 0 {
		t.Fatalf("after reset, count must be 0, got %d", got.Count)
	}
}

func TestLatencyRecorder_MetricNamesAreBoundedAndOverflowIsCounted(t *testing.T) {
	const (
		metricLimit = 8
		records     = 1000
	)
	r := NewLatencyRecorderWithLimit(4, metricLimit)
	for i := 0; i < records; i++ {
		r.Record(fmt.Sprintf("random-%d", i), time.Duration(i)*time.Nanosecond)
	}

	snapshot := r.Snapshot(false)
	if len(snapshot) != metricLimit {
		t.Fatalf("metric count=%d want hard limit %d", len(snapshot), metricLimit)
	}
	wantOverflow := uint64(records - (metricLimit - 1))
	if got := snapshot[LatencyOverflowMetricName].Count; got != wantOverflow {
		t.Fatalf("overflow count=%d want %d", got, wantOverflow)
	}

	                                                                        
	                                                                      
	r.Snapshot(true)
	r.Record("another-unseen-name", time.Millisecond)
	afterReset := r.Snapshot(false)
	if got := afterReset[LatencyOverflowMetricName].Count; got != 1 {
		t.Fatalf("overflow count after reset=%d want 1", got)
	}
}

func TestLatencyRecorder_ConcurrentUniqueNamesRemainBounded(t *testing.T) {
	const (
		metricLimit = 32
		records     = 1000
	)
	r := NewLatencyRecorderWithLimit(8, metricLimit)
	var wg sync.WaitGroup
	wg.Add(records)
	for i := 0; i < records; i++ {
		go func(i int) {
			defer wg.Done()
			r.Record(fmt.Sprintf("concurrent-%d", i), time.Duration(i)*time.Nanosecond)
		}(i)
	}
	wg.Wait()

	snapshot := r.Snapshot(false)
	if len(snapshot) > metricLimit {
		t.Fatalf("metric count=%d exceeds hard limit %d", len(snapshot), metricLimit)
	}
	var total uint64
	for _, summary := range snapshot {
		total += summary.Count
	}
	if total != records {
		t.Fatalf("total recorded count=%d want %d", total, records)
	}
	wantOverflow := uint64(records - (metricLimit - 1))
	if got := snapshot[LatencyOverflowMetricName].Count; got != wantOverflow {
		t.Fatalf("overflow count=%d want %d", got, wantOverflow)
	}
}

func TestPercentileValue(t *testing.T) {
	            
	if percentileValue(nil, 0.5) != 0 {
		t.Fatal("empty slice must return 0")
	}
	sorted := []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	cases := []struct {
		p    float64
		want int64
	}{
		{-1, 1},                       
		{0, 1},            
		{0.5, 5},                                        
		{0.95, 9},
		{0.99, 9},
		{1, 10},
		{2, 10},                         
	}
	for _, tc := range cases {
		if got := percentileValue(sorted, tc.p); got != tc.want {
			t.Errorf("percentileValue(%v)=%d want %d", tc.p, got, tc.want)
		}
	}
}
