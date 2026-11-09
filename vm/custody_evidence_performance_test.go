package vm

import (
	"fmt"
	"testing"
	"time"

	"dex/frost/artifact"
	"dex/frost/chain/btc"
	"dex/frost/custodyevidence"
	"dex/keys"
	"dex/pb"
)

                                                                          
                                                                                 
func TestCustodyEvidenceBatchPerformanceStateView(t *testing.T) {
	f := newCustodyTimingFixture(t, 20000)
	seed := newMemoryStateView(map[string][]byte{})
	ids := make([]string, 5000)
	for i := range ids {
		jobID := fmt.Sprintf("custody-perf-job-%d", i)
		template := &btc.BTCTemplate{Version: 2, VaultID: 0, KeyEpoch: 1,
			Inputs:      []btc.TxInput{{TxID: fmt.Sprintf("%064x", i+1), Amount: 100000, Sequence: btc.DefaultSequence}},
			Outputs:     []btc.TxOutput{{Address: testBTCTaprootAddress, Amount: 10000}, {Address: testBTCChangeTaprootAddress, Amount: 89000}},
			WithdrawIDs: []string{fmt.Sprintf("%064x", i+10000)}}
		btcDualDecorateTemplate(t, template)
		raw, err := template.BuildSignedRawTx([][]byte{make([]byte, 64)}, [][]byte{make([]byte, 64)})
		if err != nil {
			t.Fatal(err)
		}
		id, err := template.TxID()
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = id
		if _, err := storeFrostArtifact(seed, artifact.Metadata{ArtifactID: id, Kind: artifact.KindWithdraw, Chain: "btc", VaultID: 0, Epoch: 1, Height: 1, SourceID: jobID}, raw); err != nil {
			t.Fatal(err)
		}
		data, err := template.ToJSON()
		if err != nil {
			t.Fatal(err)
		}
		seed.Set(keys.KeyFrostPermanentWithdrawJob(jobID), custodyTimingMarshal(t, &pb.FrostWithdrawJobState{JobId: jobID, Chain: "btc", VaultId: 0, KeyEpoch: 1, Status: "SIGNED", TemplateData: data, TemplateHash: template.TemplateHash(), WithdrawIds: template.WithdrawIDs}))
	}
	ops := make([]interface{}, 0, len(seed.Diff()))
	for _, op := range seed.Diff() {
		item := op
		ops = append(ops, &item)
	}
	if _, err := f.manager.SyncStateUpdates(2, ops); err != nil {
		t.Fatal(err)
	}
	if err := f.manager.ForceFlush(); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{63, 64, 65, 255, 256, 257, 5000} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			baseline, pointIO := f.view(t, true)
			baseline.Del(keys.KeyFrostArtifactMeta(ids[0]))
			baseline.Del(keys.KeyFrostPermanentArtifact(ids[0]))
			pointIO.reset()
			pointStart := time.Now()
			for _, id := range ids[:n] {
				if _, err := custodyevidence.InternalBTCOutputs(baseline.Get, id); err != nil {
					t.Fatal(err)
				}
			}
			pointElapsed := time.Since(pointStart)
			view, io := f.view(t, true)
			view.Del(keys.KeyFrostArtifactMeta(ids[0]))                                        
			view.Del(keys.KeyFrostPermanentArtifact(ids[0]))
			jobKey := keys.KeyFrostPermanentWithdrawJob("custody-perf-job-1")
			value, found, err := view.Get(jobKey)
			if err != nil || !found {
				t.Fatalf("overlay fixture: %v", err)
			}
			view.Set(jobKey, value)
			io.reset()
			start := time.Now()
			for begin := 0; begin < n; begin += 256 {
				end := begin + 256
				if end > n {
					end = n
				}
				out, err := custodyevidence.InternalBTCOutputsMany(view.Get, view.GetMany, ids[begin:end])
				if err != nil || len(out) != end-begin {
					t.Fatalf("batch result=%d: %v", len(out), err)
				}
				for _, item := range out {
					if item.TxID == ids[0] {
						if item.KnownArtifact || len(item.InternalVouts) != 0 {
							t.Fatal("batch ignored tombstone")
						}
					} else if !item.KnownArtifact || len(item.InternalVouts) != 1 || item.InternalVouts[0] != 1 {
						t.Fatal("batch lost exact change classification")
					}
				}
			}
			elapsed := time.Since(start)
			if io.scans != 0 || io.gets != 0 || io.batches > 2*((n+255)/256) || io.batchKeys > 3*n || io.logicalGets != 0 {
				t.Fatalf("unbounded evidence IO: gets=%d batches=%d keys=%d scans=%d logicalGets=%d", io.gets, io.batches, io.batchKeys, io.scans, io.logicalGets)
			}
			if pointIO.gets != 3*(n-1) || pointIO.scans != 0 {
				t.Fatalf("invalid point baseline: gets=%d scans=%d", pointIO.gets, pointIO.scans)
			}
			t.Logf("matched=%d unrelated/high-write=20000 baseline point reads=%d/%s batched reads=%d keys=%d scans=%d/%s", n, pointIO.gets, pointElapsed, io.batches, io.batchKeys, io.scans, elapsed)
		})
	}
}
