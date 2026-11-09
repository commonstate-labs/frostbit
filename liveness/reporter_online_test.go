package liveness

import (
	"fmt"
	"reflect"
	"runtime"
	"sort"
	"sync/atomic"
	"testing"

	"dex/protocol"
)

type reporterOnlineResolver struct {
	indices map[string]uint32
	calls   atomic.Int64
}

func (r *reporterOnlineResolver) GetMinerIndex(address string) (uint32, bool) {
	r.calls.Add(1)
	index, ok := r.indices[address]
	return index, ok
}

func newReporterOnlineFixture(n int) (*LocalReporter, *reporterOnlineResolver, []string) {
	resolver := &reporterOnlineResolver{indices: make(map[string]uint32, n)}
	addresses := make([]string, n)
	for i := range addresses {
		addresses[i] = fmt.Sprintf("miner-%05d", i)
		resolver.indices[addresses[i]] = uint32(i)
	}
	return NewLocalReporter(resolver), resolver, addresses
}

func assertReporterOnlineBatch(t *testing.T, sampled, offline, online []uint32) {
	t.Helper()
	for _, side := range [][]uint32{sampled, offline, online} {
		if len(side) > protocol.MaxLivenessReportsPerBlock || !sort.SliceIsSorted(side, func(i, j int) bool { return side[i] < side[j] }) {
			t.Fatalf("unbounded or unsorted report batch: %v", side)
		}
		for i := 1; i < len(side); i++ {
			if side[i] == side[i-1] {
				t.Fatalf("duplicate report index: %v", side)
			}
		}
	}
	samples := make(map[uint32]bool, len(sampled))
	for _, index := range sampled {
		samples[index] = true
	}
	off := make(map[uint32]bool, len(offline))
	for _, index := range offline {
		if !samples[index] {
			t.Fatalf("offline index %d outside atomic sampled batch %v", index, sampled)
		}
		off[index] = true
	}
	for _, index := range online {
		if !samples[index] || off[index] {
			t.Fatalf("online index %d outside samples or overlaps offline: sampled=%v offline=%v", index, sampled, offline)
		}
	}
}

func TestReporterOnlineOverridesOfflineInEitherOrder(t *testing.T) {
	for _, onlineFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("online-first=%t", onlineFirst), func(t *testing.T) {
			reporter, _, addresses := newReporterOnlineFixture(3)
			if onlineFirst {
				reporter.ReportLivenessWithOnline(nil, nil, []string{addresses[0], addresses[0]})
				reporter.ReportOffline([]string{addresses[0], addresses[1], addresses[1]})
			} else {
				reporter.ReportOffline([]string{addresses[0], addresses[1], addresses[1]})
				reporter.ReportLivenessWithOnline(nil, nil, []string{addresses[0], addresses[0]})
			}
			                                                                              
			                                                         
			reporter.ReportLivenessWithOnline(nil, []string{addresses[2], "unknown"}, []string{addresses[2], "unknown"})
			sampled, offline, online := reporter.GetLivenessReportsWithOnline()
			assertReporterOnlineBatch(t, sampled, offline, online)
			if !reflect.DeepEqual(sampled, []uint32{0, 1, 2}) || !reflect.DeepEqual(offline, []uint32{1}) || !reflect.DeepEqual(online, []uint32{0, 2}) {
				t.Fatalf("positive priority lost: sampled=%v offline=%v online=%v", sampled, offline, online)
			}
			sampled, offline, online = reporter.GetLivenessReportsWithOnline()
			if len(sampled)+len(offline)+len(online) != 0 {
				t.Fatal("atomic batch drained more than once")
			}
		})
	}
}

func TestReporterOnlineBoundedDrainNoLossAtScale(t *testing.T) {
	for _, n := range []int{20, 100, protocol.MaxLivenessReportsPerBlock - 1, protocol.MaxLivenessReportsPerBlock, protocol.MaxLivenessReportsPerBlock + 1, 10_000} {
		t.Run(fmt.Sprintf("N=%d", n), func(t *testing.T) {
			reporter, resolver, addresses := newReporterOnlineFixture(n)
			evens := make([]string, 0, (n+1)/2)
			for i, address := range addresses {
				if i%2 == 0 {
					evens = append(evens, address)
				}
			}
			                                                                          
			reporter.ReportLivenessWithOnline(append(addresses, addresses...), append(addresses, addresses...), append(evens, evens...))
			resolved := resolver.calls.Load()
			seen := make(map[uint32]bool, n)
			batches := 0
			for {
				sampled, offline, online := reporter.GetLivenessReportsWithOnline()
				assertReporterOnlineBatch(t, sampled, offline, online)
				if len(sampled) == 0 {
					break
				}
				batches++
				for _, index := range sampled {
					if seen[index] {
						t.Fatalf("index %d emitted twice across overflow batches", index)
					}
					seen[index] = true
				}
				for _, index := range offline {
					if index%2 == 0 {
						t.Fatalf("online index %d reclassified offline", index)
					}
				}
				for _, index := range online {
					if index%2 != 0 {
						t.Fatalf("offline index %d manufactured online", index)
					}
				}
				if len(offline)+len(online) != len(sampled) {
					t.Fatal("one side of the atomic observation was lost")
				}
			}
			if len(seen) != n || batches != (n+protocol.MaxLivenessReportsPerBlock-1)/protocol.MaxLivenessReportsPerBlock {
				t.Fatalf("bounded draining lost work: N=%d emitted=%d batches=%d", n, len(seen), batches)
			}
			if resolver.calls.Load() != resolved {
				t.Fatal("draining re-resolved identities instead of using the pending compact indices")
			}
			t.Logf("N=%d report batches=%d emitted=%d additional identity reads=0", n, batches, len(seen))
		})
	}
}

func TestReporterOnlineOverflowRotatesBeforeFreshLowIndices(t *testing.T) {
	const n = protocol.MaxLivenessReportsPerBlock + 1
	reporter, _, addresses := newReporterOnlineFixture(n)
	reporter.ReportLivenessWithOnline(nil, nil, addresses)
	first, offline, online := reporter.GetLivenessReportsWithOnline()
	assertReporterOnlineBatch(t, first, offline, online)
	if len(first) != protocol.MaxLivenessReportsPerBlock {
		t.Fatalf("first batch size=%d", len(first))
	}
	reporter.ReportOffline(addresses[:protocol.MaxLivenessReportsPerBlock])
	second, offline, online := reporter.GetLivenessReportsWithOnline()
	assertReporterOnlineBatch(t, second, offline, online)
	if !reflect.DeepEqual(online, []uint32{protocol.MaxLivenessReportsPerBlock}) {
		t.Fatalf("older online overflow starved by newly reported low indices: %v", online)
	}
	third, offline, online := reporter.GetLivenessReportsWithOnline()
	assertReporterOnlineBatch(t, third, offline, online)
	if !reflect.DeepEqual(third, []uint32{protocol.MaxLivenessReportsPerBlock - 1}) || len(online) != 0 {
		t.Fatalf("rotated remainder lost: sampled=%v offline=%v online=%v", third, offline, online)
	}
}

func TestReporterOnlineObservationRemainsAtomicDuringConcurrentDrain(t *testing.T) {
	const n = 1_000
	reporter, _, addresses := newReporterOnlineFixture(n)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, address := range addresses {
			reporter.ReportLivenessWithOnline(nil, []string{address}, []string{address})
		}
	}()
	seen := make(map[uint32]bool, n)
	for {
		sampled, offline, online := reporter.GetLivenessReportsWithOnline()
		assertReporterOnlineBatch(t, sampled, offline, online)
		if len(offline) != 0 || !reflect.DeepEqual(sampled, online) {
			t.Fatalf("three-list update split across drains: sampled=%v offline=%v online=%v", sampled, offline, online)
		}
		for _, index := range sampled {
			if seen[index] {
				t.Fatalf("index %d duplicated during concurrent drain", index)
			}
			seen[index] = true
		}
		if len(sampled) != 0 {
			continue
		}
		select {
		case <-done:
			if len(seen) == n {
				return
			}
			                                                                  
			                                                          
			if len(reporter.pending) == 0 {
				t.Fatalf("concurrent drain lost observations: got %d want %d", len(seen), n)
			}
		default:
			runtime.Gosched()
		}
	}
}
