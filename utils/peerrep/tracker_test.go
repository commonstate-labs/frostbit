package peerrep

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

                               
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newTrackerWithClock(threshold int, duration time.Duration) (*Tracker, *fakeClock) {
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	tr := New(threshold, duration)
	tr.SetNow(clk.now)
	return tr, clk
}

func TestRecordFailure_BansAtThreshold(t *testing.T) {
	tr, _ := newTrackerWithClock(3, time.Minute)
	var cbCount int32
	tr.SetOnBan(func(key, reason string, count int) { atomic.AddInt32(&cbCount, 1) })

	for i := 1; i <= 2; i++ {
		if banned, _ := tr.RecordFailure("p1", "x"); banned {
			t.Fatalf("round %d: banned too early", i)
		}
		if tr.IsBanned("p1") {
			t.Fatalf("round %d: IsBanned true too early", i)
		}
	}
	banned, _ := tr.RecordFailure("p1", "x")
	if !banned {
		t.Fatal("expected ban at threshold")
	}
	if !tr.IsBanned("p1") {
		t.Fatal("IsBanned should be true after ban")
	}
	if atomic.LoadInt32(&cbCount) != 1 {
		t.Fatalf("onBan callback should fire exactly once, got %d", cbCount)
	}
}

func TestBanExpires(t *testing.T) {
	tr, clk := newTrackerWithClock(1, 10*time.Second)
	tr.RecordFailure("p", "")
	if !tr.IsBanned("p") {
		t.Fatal("should be banned")
	}
	clk.advance(9 * time.Second)
	if !tr.IsBanned("p") {
		t.Fatal("still within ban window")
	}
	clk.advance(2 * time.Second)
	if tr.IsBanned("p") {
		t.Fatal("should auto-expire")
	}
}

func TestResetClearsCountButNotBan(t *testing.T) {
	tr, _ := newTrackerWithClock(3, time.Minute)
	tr.RecordFailure("p", "")
	tr.RecordFailure("p", "")
	tr.Reset("p")
	if tr.IsBanned("p") {
		t.Fatal("should not be banned yet")
	}
	                                                      
	if banned, _ := tr.RecordFailure("p", ""); banned {
		t.Fatal("threshold should have been reset")
	}
}

func TestForgetClearsBan(t *testing.T) {
	tr, _ := newTrackerWithClock(1, time.Minute)
	tr.RecordFailure("p", "")
	if !tr.IsBanned("p") {
		t.Fatal("expected ban")
	}
	tr.Forget("p")
	if tr.IsBanned("p") {
		t.Fatal("Forget should clear ban")
	}
}

func TestFilterDropsBanned(t *testing.T) {
	tr, _ := newTrackerWithClock(1, time.Minute)
	tr.RecordFailure("bad", "")
	out := tr.Filter([]string{"good1", "bad", "", "good2"})
	if len(out) != 2 || out[0] != "good1" || out[1] != "good2" {
		t.Fatalf("unexpected filter output: %v", out)
	}
}

func TestEmptyKeyNoOp(t *testing.T) {
	tr, _ := newTrackerWithClock(1, time.Minute)
	banned, _ := tr.RecordFailure("", "")
	if banned || tr.IsBanned("") {
		t.Fatal("empty key must be ignored")
	}
}

func TestZeroThresholdDisabled(t *testing.T) {
	tr, _ := newTrackerWithClock(0, time.Minute)
	for i := 0; i < 1000; i++ {
		if banned, _ := tr.RecordFailure("p", ""); banned {
			t.Fatal("threshold=0 should disable banning")
		}
	}
}

func TestMaxEntriesBoundsFailureAndBanState(t *testing.T) {
	tr, clock := newTrackerWithClock(1, time.Hour)
	tr.SetMaxEntries(2)
	tr.RecordFailure("oldest", "x")
	clock.advance(time.Second)
	tr.RecordFailure("middle", "x")
	clock.advance(time.Second)
	tr.RecordFailure("newest", "x")

	if tr.IsBanned("oldest") {
		t.Fatal("least-recent tracker entry should be evicted at capacity")
	}
	if !tr.IsBanned("middle") || !tr.IsBanned("newest") {
		t.Fatal("most-recent tracker entries should remain present")
	}
	_, tracking := tr.Snapshot()
	if tracking != 2 {
		t.Fatalf("tracking entries=%d, want bounded capacity 2", tracking)
	}
}

func TestMaxEntriesRefreshesLRUInConstantTime(t *testing.T) {
	tr, _ := newTrackerWithClock(3, time.Hour)
	tr.SetMaxEntries(2)
	tr.RecordFailure("a", "x")
	tr.RecordFailure("b", "x")
	tr.RecordFailure("a", "x")                                           
	tr.RecordFailure("c", "x")

	tr.mu.Lock()
	_, hasA := tr.lruEntries["a"]
	_, hasB := tr.lruEntries["b"]
	_, hasC := tr.lruEntries["c"]
	front := tr.lru.Front().Value
	back := tr.lru.Back().Value
	tr.mu.Unlock()
	if !hasA || hasB || !hasC {
		t.Fatalf("unexpected retained identities: a=%v b=%v c=%v", hasA, hasB, hasC)
	}
	if front != "a" || back != "c" {
		t.Fatalf("unexpected LRU order front/back=%v/%v", front, back)
	}
	if banned, count := tr.RecordFailure("a", "x"); !banned || count != 3 {
		t.Fatalf("refreshed identity lost failure state: banned=%v count=%d", banned, count)
	}
}

func TestMaxEntriesRemainsBoundedConcurrently(t *testing.T) {
	tr, _ := newTrackerWithClock(10_000, time.Hour)
	tr.SetMaxEntries(32)
	var wg sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				tr.RecordFailure(fmt.Sprintf("%d:%d", worker, i), "x")
			}
		}(worker)
	}
	wg.Wait()

	tr.mu.Lock()
	defer tr.mu.Unlock()
	if len(tr.lruEntries) != 32 || tr.lru.Len() != 32 {
		t.Fatalf("tracker map/list size=%d/%d, want 32/32", len(tr.lruEntries), tr.lru.Len())
	}
	seen := make(map[string]struct{}, 32)
	for entry := tr.lru.Front(); entry != nil; entry = entry.Next() {
		key := entry.Value.(string)
		if _, duplicate := seen[key]; duplicate {
			t.Fatalf("duplicate LRU identity %q", key)
		}
		seen[key] = struct{}{}
		if tr.lruEntries[key] != entry {
			t.Fatalf("map/list mismatch for %q", key)
		}
	}
}

func TestConcurrentRecordAndFilter(t *testing.T) {
	tr, _ := newTrackerWithClock(100, time.Minute)
	keys := []string{"a", "b", "c", "d", "e"}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				tr.RecordFailure(keys[(gid+j)%len(keys)], "race")
				_ = tr.IsBanned(keys[j%len(keys)])
				_ = tr.Filter(keys)
				if j%50 == 0 {
					tr.Reset(keys[j%len(keys)])
				}
			}
		}(i)
	}
	wg.Wait()
	                            
	banned, tracking := tr.Snapshot()
	if banned < 0 || tracking < 0 {
		t.Fatal("nonsense snapshot")
	}
}
