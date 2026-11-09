package planning

import (
	"fmt"
	"testing"

	chainpkg "dex/frost/chain"
	"dex/keys"
)

func TestMixedAccountPlannerCountAndAgeBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name       string
		pending    int
		age        uint64
		wantJobs   int
		wantItems  int
		wantWaitIO bool
	}{
		{name: "63 at plus 99 waits", pending: 63, age: chainpkg.AccountWithdrawBatchTimeoutBlocks - 1, wantWaitIO: true},
		{name: "63 at plus 100 runs", pending: 63, age: chainpkg.AccountWithdrawBatchTimeoutBlocks, wantJobs: 1, wantItems: 63},
		{name: "64 runs immediately", pending: 64, age: 0, wantJobs: 1, wantItems: 64},
		{name: "65 takes first 64", pending: 65, age: 0, wantJobs: 1, wantItems: 64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, planner := newMixedPlanningFixture(t, 1)
			const currentHeight = uint64(200)
			r.kvs[keys.KeyLatestHeight()] = []byte(fmt.Sprint(currentHeight))
			oldest := currentHeight - tc.age
			for i := 1; i <= tc.pending; i++ {
				id := fmt.Sprintf("boundary-%02d", i)
				seedMixedPlanningWithdraw(t, r, "base", "BASE", id, 0, uint64(i), oldest, "100")
			}
			r.kvs[keys.KeyFrostWithdrawLiveHead("base", 0)] = []byte("1")
			r.kvs[keys.KeyFrostWithdrawLiveTail("base", 0)] = []byte(fmt.Sprint(tc.pending))
			r.kvs[keys.KeyFrostWithdrawLivePending("base", 0)] = []byte(fmt.Sprint(tc.pending))
			jobs, _, err := planner.PlanJobWindow("base", "ignored-token-dimension")
			if err != nil {
				t.Fatal(err)
			}
			if len(jobs) != tc.wantJobs {
				t.Fatalf("jobs=%d want=%d", len(jobs), tc.wantJobs)
			}
			if tc.wantJobs == 1 && len(jobs[0].WithdrawIDs) != tc.wantItems {
				t.Fatalf("items=%d want=%d", len(jobs[0].WithdrawIDs), tc.wantItems)
			}
			if tc.wantWaitIO && (r.batchCalls != 3 || r.batchKeys != 5+tc.pending+1) {
				t.Fatalf("waiting batch decoded more than its live head: batches=%d keys=%d", r.batchCalls, r.batchKeys)
			}
			if r.batchCalls > 8 || r.batchKeys > 5+128+64+64+2 {
				t.Fatalf("mixed planner IO exceeded bounded O(C+N) envelope: batches=%d keys=%d", r.batchCalls, r.batchKeys)
			}
		})
	}
}

func TestMixedAccountPlannerRejectsFutureOldestHeight(t *testing.T) {
	r, planner := newMixedPlanningFixture(t, 1)
	seedMixedPlanningWithdraw(t, r, "base", "BASE", "future-oldest", 0, 1, 101, "100")
	r.kvs[keys.KeyFrostWithdrawLiveHead("base", 0)] = []byte("1")
	r.kvs[keys.KeyFrostWithdrawLiveTail("base", 0)] = []byte("1")
	r.kvs[keys.KeyFrostWithdrawLivePending("base", 0)] = []byte("1")

	jobs, _, err := planner.PlanJobWindow("base", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("future-height withdrawal became claimable: %+v", jobs)
	}
	if r.batchCalls != 3 || r.batchKeys != 7 {
		t.Fatalf("future-height wait read beyond the live head: batches=%d keys=%d", r.batchCalls, r.batchKeys)
	}
}

func TestMixedAccountPlannerAgesCanonicalLiveHeadAfterCancellation(t *testing.T) {
	r, planner := newMixedPlanningFixture(t, 1)
	r.kvs[keys.KeyLatestHeight()] = []byte("200")
	                                                                         
	r.kvs[keys.KeyFrostWithdrawLiveIndex("base", 0, 1)] = nil
	seedMixedPlanningWithdraw(t, r, "base", "BASE", "oldest-live", 0, 2, 101, "100")
	r.kvs[keys.KeyFrostWithdrawLiveHead("base", 0)] = []byte("1")
	r.kvs[keys.KeyFrostWithdrawLiveTail("base", 0)] = []byte("2")
	r.kvs[keys.KeyFrostWithdrawLivePending("base", 0)] = []byte("1")

	jobs, _, err := planner.PlanJobWindow("base", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("cancelled queue head incorrectly started the age timer: %+v", jobs)
	}
	r.kvs[keys.KeyLatestHeight()] = []byte("201")
	jobs, _, err = planner.PlanJobWindow("base", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || len(jobs[0].WithdrawIDs) != 1 || jobs[0].WithdrawIDs[0] != "oldest-live" {
		t.Fatalf("canonical live head did not become claimable at age 100: %+v", jobs)
	}
}

func TestMixedAccountPlannerSkipsHeadMiddleTailCancellationTombstonesMonotonically(t *testing.T) {
	r, planner := newMixedPlanningFixture(t, 1)
	for seq := uint64(1); seq <= 67; seq++ {
		if seq == 1 || seq == 33 || seq == 67 {
			continue
		}
		id := fmt.Sprintf("live-%02d", seq)
		seedMixedPlanningWithdraw(t, r, "base", "BASE", id, 0, seq, 100, "100")
	}
	r.kvs[keys.KeyFrostWithdrawLiveHead("base", 0)] = []byte("1")
	r.kvs[keys.KeyFrostWithdrawLiveTail("base", 0)] = []byte("67")
	r.kvs[keys.KeyFrostWithdrawLivePending("base", 0)] = []byte("64")

	jobs, _, err := planner.PlanJobWindow("base", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || len(jobs[0].WithdrawIDs) != 64 || jobs[0].WithdrawIDs[0] != "live-02" || jobs[0].WithdrawIDs[63] != "live-66" {
		t.Fatalf("tombstone traversal produced wrong canonical prefix: %+v", jobs)
	}
	if r.batchCalls > 8 || r.batchKeys > 5+128+64+64+2 {
		t.Fatalf("tombstone traversal repeated prefixes: batches=%d keys=%d", r.batchCalls, r.batchKeys)
	}
}

func TestMixedAccountPlannerLargeTombstonePrefixIsLinear(t *testing.T) {
	const (
		tombstones = 4096
		live       = 64
	)
	r, planner := newMixedPlanningFixture(t, 1)
	for seq := uint64(1); seq <= tombstones; seq++ {
		                                                                        
		                                                                
		r.kvs[keys.KeyFrostWithdrawLiveIndex("base", 0, seq)] = nil
	}
	for i := uint64(1); i <= live; i++ {
		seq := uint64(tombstones) + i
		id := fmt.Sprintf("large-live-%02d", i)
		seedMixedPlanningWithdraw(t, r, "base", "BASE", id, 0, seq, 100, "100")
	}
	r.kvs[keys.KeyFrostWithdrawLiveHead("base", 0)] = []byte("1")
	r.kvs[keys.KeyFrostWithdrawLiveTail("base", 0)] = []byte(fmt.Sprint(tombstones + live))
	r.kvs[keys.KeyFrostWithdrawLivePending("base", 0)] = []byte(fmt.Sprint(live))

	jobs, _, err := planner.PlanJobWindow("base", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || len(jobs[0].WithdrawIDs) != live || jobs[0].WithdrawIDs[0] != "large-live-01" || jobs[0].WithdrawIDs[live-1] != "large-live-64" {
		t.Fatalf("large tombstone traversal produced wrong canonical prefix: %+v", jobs)
	}
	                                                                          
	if r.batchCalls != 37 || r.batchKeys != 4294 || r.getCalls != 4 {
		t.Fatalf("large tombstone traversal IO changed: gets=%d batches=%d keys=%d", r.getCalls, r.batchCalls, r.batchKeys)
	}
}

func TestMixedAccountPlannerMatureUnderfullContinuesAfterLargeTombstonePrefix(t *testing.T) {
	const (
		tombstones = 4096
		live       = 63
	)
	r, planner := newMixedPlanningFixture(t, 1)
	r.kvs[keys.KeyLatestHeight()] = []byte("200")
	for seq := uint64(1); seq <= tombstones; seq++ {
		r.kvs[keys.KeyFrostWithdrawLiveIndex("base", 0, seq)] = nil
	}
	for i := uint64(1); i <= live; i++ {
		seq := uint64(tombstones) + i
		id := fmt.Sprintf("mature-live-%02d", i)
		seedMixedPlanningWithdraw(t, r, "base", "BASE", id, 0, seq, 100, "100")
	}
	r.kvs[keys.KeyFrostWithdrawLiveHead("base", 0)] = []byte("1")
	r.kvs[keys.KeyFrostWithdrawLiveTail("base", 0)] = []byte(fmt.Sprint(tombstones + live))
	r.kvs[keys.KeyFrostWithdrawLivePending("base", 0)] = []byte(fmt.Sprint(live))

	jobs, _, err := planner.PlanJobWindow("base", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || len(jobs[0].WithdrawIDs) != live || jobs[0].WithdrawIDs[0] != "mature-live-01" || jobs[0].WithdrawIDs[live-1] != "mature-live-63" {
		t.Fatalf("mature underfull traversal produced wrong prefix: %+v", jobs)
	}
	                                                                            
	                                                                      
	if r.batchCalls != 39 || r.batchKeys != 4353 || r.getCalls != 4 {
		t.Fatalf("mature underfull traversal was not monotonic: gets=%d batches=%d keys=%d", r.getCalls, r.batchCalls, r.batchKeys)
	}
}
