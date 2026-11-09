package logs

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConsensusTraceOptIn(t *testing.T) {
	for _, value := range []string{"", "0", "true", "1"} {
		t.Setenv("DEX_CONSENSUS_TRACE", value)
		if ConsensusTraceEnabled() != (value == "1") {
			t.Fatalf("unexpected trace setting %q", value)
		}
	}
}

func TestConsensusTraceBoundedConcurrent(t *testing.T) {
	var limiter traceLimiter
	now := time.Now()
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 500; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _ := limiter.allow(now); ok {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 200 {
		t.Fatalf("accepted=%d", accepted.Load())
	}
	if ok, dropped := limiter.allow(now.Add(time.Second)); !ok || dropped != 300 {
		t.Fatalf("next window allowed=%v dropped=%d", ok, dropped)
	}
}
