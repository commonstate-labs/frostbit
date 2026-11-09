package liveness

import (
	"bytes"
	"sort"
	"testing"
)

                                                                                                        
                                                                                                                  
                                                                                                                        

func snapshotStore(store *mockStore) []byte {
	ks := make([]string, 0, len(store.data))
	for k := range store.data {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	var buf bytes.Buffer
	for _, k := range ks {
		buf.WriteString(k)
		buf.WriteByte('=')
		buf.Write(store.data[k])
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

                                                                                                                            
func TestTrackerProcessBlock_Deterministic_AcrossRuns(t *testing.T) {
	                                                            
	const rounds = 5
	const N = 12                                                   

	run := func() []byte {
		cfg := defaultTestCfg()
		tr, sv := newTrackerWithStore(cfg)

		                                     
		for i := 0; i < N; i++ {
			addr := "miner-" + string(rune('a'+i))
			if _, err := tr.RegisterMinerAtHeight(sv, addr, 1000, 1); err != nil {
				t.Fatalf("register %s: %v", addr, err)
			}
		}

		                                                                                    
		sampled := make([]uint32, N)
		for i := range sampled {
			sampled[i] = uint32(i)
		}
		for h := uint64(2); h <= 7; h++ {
			reporter := []string{"reporter-a", "reporter-b", "reporter-c"}[int(h)%3]
			tr.ProcessBlockWithReporter(sv, h, reporter, sampled, nil)
		}
		                     
		if tr.GetActiveCount() != N {
			t.Fatalf("expect %d active, got %d", N, tr.GetActiveCount())
		}
		return snapshotStore(sv)
	}

	baseline := run()
	for r := 2; r <= rounds; r++ {
		if snap := run(); !bytes.Equal(baseline, snap) {
			t.Fatalf("tracker store snapshot differs across runs on round %d — map iteration non-determinism regressed", r)
		}
	}
}

                                                                                                                 
                                                                                   
func TestTrackerActiveMiners_Deterministic(t *testing.T) {
	cfg := defaultTestCfg()
	tr, sv := newTrackerWithStore(cfg)

	wantOrder := []string{"a", "b", "c", "d", "e"}
	for _, addr := range wantOrder {
		if _, err := tr.RegisterMinerAtHeight(sv, addr, 1000, 1); err != nil {
			t.Fatalf("register: %v", err)
		}
	}
	for h := uint64(2); h <= 7; h++ {
		reporter := []string{"reporter-a", "reporter-b", "reporter-c"}[int(h)%3]
		tr.ProcessBlockWithReporter(sv, h, reporter, []uint32{0, 1, 2, 3, 4}, nil)
	}

	                                              
	first := tr.GetActiveMiners()
	if len(first) != len(wantOrder) {
		t.Fatalf("active miners len=%d want %d", len(first), len(wantOrder))
	}
	for i := 0; i < 10; i++ {
		got := tr.GetActiveMiners()
		for j := range got {
			if got[j] != first[j] {
				t.Fatalf("iteration %d position %d: got %q want %q (map iteration order leaked)", i, j, got[j], first[j])
			}
		}
	}
}
