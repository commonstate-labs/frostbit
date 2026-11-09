package planning

import (
	"fmt"
	"strconv"
	"testing"

	chainpkg "dex/frost/chain"

	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

func newBTCBatchFixture(t *testing.T, count int, height, oldest uint64, maxInFlight int) (*testStateReader, *JobWindowPlanner, *fakeBTCPlanningAdapter) {
	t.Helper()
	r := &testStateReader{kvs: map[string][]byte{}}
	a := &fakeBTCPlanningAdapter{}
	factory := chainpkg.NewDefaultAdapterFactory()
	factory.RegisterAdapter(a)
	cfg, err := proto.Marshal(&pb.FrostVaultConfig{VaultCount: 1, BtcFeeSatsPerVbyte: planningTestBTCFeeRate})
	if err != nil {
		t.Fatal(err)
	}
	r.kvs[keys.KeyFrostVaultConfig("btc", 0)] = cfg
	seedVaultState(t, r, "btc", 0, "ACTIVE", 1, 10, nil)
	r.kvs[keys.KeyFrostVaultAvailableBalance("btc", "BTC", 0)] = []byte("1000000000")
	r.kvs[keys.KeyFrostWithdrawFIFOHead("btc", "BTC")] = []byte("1")
	r.kvs[keys.KeyFrostWithdrawFIFOSeq("btc", "BTC")] = []byte(strconv.Itoa(count))
	r.kvs[keys.KeyLatestHeight()] = []byte(strconv.FormatUint(height, 10))
	for i := 1; i <= count; i++ {
		id := fmt.Sprintf("btc-batch-%d", i)
		seedPlanningWithdraw(t, r, "btc", "BTC", id, uint64(i), "100000")
		setBTCBatchState(t, r, id, func(w *pb.FrostWithdrawState) { w.RequestHeight = oldest })
	}
	seedSpendableUtxo(t, r, 0, 1, "funded", 0, "1000000000", false)
	u := &pb.FrostUtxo{}
	if err := proto.Unmarshal(r.kvs[keys.KeyFrostBtcSpendableUtxoIndex(0, 1)], u); err != nil {
		t.Fatal(err)
	}
	u.ScriptPubkey = planningBTCScript()
	err = nil
	if err != nil {
		t.Fatal(err)
	}
	r.kvs[keys.KeyFrostBtcSpendableUtxoIndex(0, 1)], err = proto.Marshal(u)
	if err != nil {
		t.Fatal(err)
	}
	r.kvs[keys.KeyFrostBtcUtxo(0, u.Txid, u.Vout)], _ = proto.Marshal(u)
	return r, NewJobWindowPlanner(r, factory, maxInFlight), a
}

func setBTCBatchState(t *testing.T, r *testStateReader, id string, edit func(*pb.FrostWithdrawState)) {
	t.Helper()
	w := &pb.FrostWithdrawState{}
	if err := proto.Unmarshal(r.kvs[keys.KeyFrostWithdraw(id)], w); err != nil {
		t.Fatal(err)
	}
	edit(w)
	raw, err := proto.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	r.kvs[keys.KeyFrostWithdraw(id)] = raw
}

func TestBTCBatchPlannerCountAndAge(t *testing.T) {
	for _, tc := range []struct {
		name           string
		count          int
		height, oldest uint64
		want           int
	}{
		{"empty", 0, 137, 37, 0},
		{"255-age99", 255, 136, 37, 0},
		{"255-age100", 255, 137, 37, 255},
		{"256-immediate", 256, 37, 37, 256},
		{"257-first256", 257, 37, 37, 256},
		{"singleton-age100", 1, 137, 37, 1},
		{"future-height", 255, 36, 37, 0},
		{"near-overflow", 255, ^uint64(0), ^uint64(0) - 99, 0},
	} {
		for _, inFlight := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s-inflight%d", tc.name, inFlight), func(t *testing.T) {
				_, p, a := newBTCBatchFixture(t, tc.count, tc.height, tc.oldest, inFlight)
				jobs, _, err := p.PlanJobWindow(" BTC ", " BTC ")
				if err != nil {
					t.Fatal(err)
				}
				if tc.want == 0 {
					if len(jobs) != 0 {
						t.Fatalf("unexpected jobs: %+v", jobs)
					}
					return
				}
				if len(jobs) != 1 || len(jobs[0].WithdrawIDs) != tc.want {
					t.Fatalf("jobs=%+v want %d recipients", jobs, tc.want)
				}
				if len(a.params.Outputs) != tc.want || a.params.ChangeAmount == 0 {
					t.Fatalf("missing recipients/change: %+v", a.params)
				}
				var sum uint64
				for i, out := range a.params.Outputs {
					if out.WithdrawID != fmt.Sprintf("btc-batch-%d", i+1) {
						t.Fatal("FIFO order changed")
					}
					amount, err := strconv.ParseUint(out.Amount, 10, 64)
					if err != nil {
						t.Fatal(err)
					}
					sum += amount
				}
				if sum+a.params.Fee != uint64(tc.want)*100000 || sum+a.params.Fee+a.params.ChangeAmount != a.params.Inputs[0].Amount {
					t.Fatal("BTC amount conservation failed")
				}
			})
		}
	}
}

func TestBTCBatchPlannerCancellationBoundary(t *testing.T) {
	t.Run("cancelled-head-does-not-age-the-next-request", func(t *testing.T) {
		r, p, _ := newBTCBatchFixture(t, 256, 136, 37, 1)
		setBTCBatchState(t, r, "btc-batch-1", func(w *pb.FrostWithdrawState) { w.Status = "CANCELLED"; w.RequestHeight = 1 })
		jobs, _, err := p.PlanJobWindow("btc", "BTC")
		if err != nil || len(jobs) != 0 {
			t.Fatalf("live head sent early: %+v %v", jobs, err)
		}
		r.kvs[keys.KeyLatestHeight()] = []byte("137")
		jobs, _, err = p.PlanJobWindow("btc", "BTC")
		if err != nil || len(jobs) != 1 || len(jobs[0].WithdrawIDs) != 255 || jobs[0].FirstSeq != 2 {
			t.Fatalf("jobs=%+v err=%v", jobs, err)
		}
	})
	t.Run("cancelled-head-age-from-live-head", func(t *testing.T) {
		r, p, _ := newBTCBatchFixture(t, 257, 137, 37, 1)
		setBTCBatchState(t, r, "btc-batch-1", func(w *pb.FrostWithdrawState) { w.Status = "CANCELLED"; w.RequestHeight = 1 })
		jobs, _, err := p.PlanJobWindow("btc", "BTC")
		if err != nil || len(jobs) != 1 || len(jobs[0].WithdrawIDs) != 256 || jobs[0].FirstSeq != 2 {
			t.Fatalf("jobs=%+v err=%v", jobs, err)
		}
	})
	t.Run("middle-cancel-is-boundary", func(t *testing.T) {
		r, p, _ := newBTCBatchFixture(t, 257, 136, 37, 1)
		setBTCBatchState(t, r, "btc-batch-3", func(w *pb.FrostWithdrawState) { w.Status = "CANCELLED" })
		jobs, _, err := p.PlanJobWindow("btc", "BTC")
		if err != nil || len(jobs) != 0 {
			t.Fatalf("early jobs=%+v err=%v", jobs, err)
		}
		r.kvs[keys.KeyLatestHeight()] = []byte("137")
		jobs, _, err = p.PlanJobWindow("btc", "BTC")
		if err != nil || len(jobs) != 1 || len(jobs[0].WithdrawIDs) != 2 {
			t.Fatalf("jobs=%+v err=%v", jobs, err)
		}
		                                                                    
		                                                    
		r.kvs[keys.KeyFrostWithdrawFIFOHead("btc", "BTC")] = []byte("4")
		setBTCBatchState(t, r, "btc-batch-4", func(w *pb.FrostWithdrawState) { w.RequestHeight = 100 })
		jobs, _, err = p.PlanJobWindow("btc", "BTC")
		if err != nil || len(jobs) != 0 {
			t.Fatalf("next segment submitted early: %+v %v", jobs, err)
		}
	})
}

func TestBTCBatchPlannerCancelledPrefixReadsAreLinear(t *testing.T) {
	const cancelled = 4096
	r, p, _ := newBTCBatchFixture(t, cancelled+256, 37, 37, 1)
	for i := 1; i <= cancelled; i++ {
		setBTCBatchState(t, r, fmt.Sprintf("btc-batch-%d", i), func(w *pb.FrostWithdrawState) { w.Status = "CANCELLED" })
	}
	jobs, _, err := p.PlanJobWindow("btc", "BTC")
	if err != nil || len(jobs) != 1 || len(jobs[0].WithdrawIDs) != 256 || jobs[0].FirstSeq != cancelled+1 {
		t.Fatalf("jobs=%+v err=%v", jobs, err)
	}
	                                                                                      
	if r.batchKeys != 2*(cancelled+256)+3 || r.batchCalls != 2*(cancelled/256+1)+1 || r.getCalls > 16 {
		t.Fatalf("repeated prefix reads: keys=%d calls=%d Get=%d", r.batchKeys, r.batchCalls, r.getCalls)
	}
}

func TestBTCBatchPlannerInvalidHeightWaitsFailClosed(t *testing.T) {
	for _, raw := range []string{"", "not-a-height"} {
		t.Run(fmt.Sprintf("height-%q", raw), func(t *testing.T) {
			r, p, _ := newBTCBatchFixture(t, 1, 100, 0, 1)
			r.kvs[keys.KeyLatestHeight()] = []byte(raw)
			jobs, _, err := p.PlanJobWindow("btc", "BTC")
			if len(jobs) != 0 || (raw != "" && err == nil) {
				t.Fatalf("invalid height allowed submission: jobs=%+v err=%v", jobs, err)
			}
		})
	}
}

func TestBTCBatchPlannerRecoversClaimBeforeWaiting(t *testing.T) {
	for _, count := range []int{1, 256, 257} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			r, p, _ := newBTCBatchFixture(t, count, 37, 37, 1)
			ids := make([]string, count)
			hash, data := []byte{1, 2, 3}, []byte("finalized-btc-template")
			id := chainpkg.ComputeWithdrawJobID("btc", "BTC", 0, 1, hash, 1)
			for i := range ids {
				ids[i] = fmt.Sprintf("btc-batch-%d", i+1)
				setBTCBatchState(t, r, ids[i], func(w *pb.FrostWithdrawState) { w.Status = "SIGNING"; w.JobId = id; w.VaultId = 0 })
			}
			raw, err := proto.Marshal(&pb.FrostWithdrawJobState{JobId: id, Chain: "btc", Asset: "BTC", VaultId: 0, KeyEpoch: 1, FirstSeq: 1, LastSeq: uint64(count), WithdrawIds: ids, TemplateHash: hash, TemplateData: data, Status: "CLAIMED"})
			if err != nil {
				t.Fatal(err)
			}
			r.kvs[keys.KeyFrostWithdrawJob(id)] = raw
			jobs, _, err := p.PlanJobWindow("btc", "BTC")
			if err != nil || len(jobs) != 1 || jobs[0].JobID != id || string(jobs[0].TemplateData) != string(data) || len(jobs[0].WithdrawIDs) != count {
				t.Fatalf("recovery jobs=%+v err=%v", jobs, err)
			}
			if r.batchCalls != 2 || r.getCalls > 6 {
				t.Fatalf("recovery must batch reads: Get=%d GetMany=%d", r.getCalls, r.batchCalls)
			}
		})
	}
}

func TestBTCBatchPlannerBoundedReads(t *testing.T) {
	for _, count := range []int{255, 256, 257, 5000, 20000} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			r, p, _ := newBTCBatchFixture(t, count, 37, 37, 1)
			jobs, _, err := p.PlanJobWindow("btc", "BTC")
			if err != nil {
				t.Fatal(err)
			}
			if count == 255 && len(jobs) != 0 {
				t.Fatal("immature partial batch")
			}
			if r.batchKeys > 600 || r.getCalls > 16 {
				t.Fatalf("reads scale with queue/recipients: Get=%d GetMany=%d batchKeys=%d", r.getCalls, r.batchCalls, r.batchKeys)
			}
			t.Logf("queued=%d Get=%d GetMany=%d batchKeys=%d", count, r.getCalls, r.batchCalls, r.batchKeys)
		})
	}
}

                                                                            
                                                                             
func TestBTCBatchPlannerIOWorkload(t *testing.T) {
	r, p, _ := newBTCBatchFixture(t, 256, 37, 37, 256)
	jobs, logs, err := p.PlanJobWindow("btc", "BTC")
	if err != nil || len(jobs) != 1 || len(jobs[0].WithdrawIDs) != 256 {
		t.Fatalf("jobs=%+v logs=%+v err=%v", jobs, logs, err)
	}
	t.Logf("same 256-recipient workload: Get=%d GetMany=%d batchKeys=%d", r.getCalls, r.batchCalls, r.batchKeys)
}
