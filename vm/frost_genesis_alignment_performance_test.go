package vm

import (
	"dex/config"
	"dex/db"
	"dex/frost/ceremony"
	"dex/interfaces"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"dex/protocol"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
)

type genesisAlignmentIO struct {
	StateView
	gets, batches, batchKeys, scans, rows, sets, deletes int
}

func (s *genesisAlignmentIO) Get(k string) ([]byte, bool, error) { s.gets++; return s.StateView.Get(k) }
func (s *genesisAlignmentIO) GetMany(ks []string) (map[string][]byte, error) {
	s.batches++
	s.batchKeys += len(ks)
	return s.StateView.(interface {
		GetMany([]string) (map[string][]byte, error)
	}).GetMany(ks)
}
func (s *genesisAlignmentIO) NextUserActivitySeq(p string) (uint32, error) {
	return s.StateView.(userActivitySeqAllocator).NextUserActivitySeq(p)
}
func (s *genesisAlignmentIO) Set(k string, v []byte) { s.sets++; s.StateView.Set(k, v) }
func (s *genesisAlignmentIO) Del(k string)           { s.deletes++; s.StateView.Del(k) }
func (s *genesisAlignmentIO) Scan(p string) (map[string][]byte, error) {
	s.scans++
	rows, err := s.StateView.Scan(p)
	s.rows += len(rows)
	return rows, err
}

type genesisAlignmentBaseIO struct {
	points, batches, batchKeys, scans, rows int
	prefixes                                map[string]int
}

                                                                        
                                                                             
                                                                                
func TestFreshGenesisAlignmentRealStateViewReadBounds(t *testing.T) {
	for _, tc := range []struct{ candidates, history, overlay int }{
		{4, 31, 31}, {5, 32, 32}, {174, 33, 33}, {175, 20000, 5000}, {176, 20000, 5000},
	} {
		t.Run(fmt.Sprintf("N%d_H%d_O%d", tc.candidates, tc.history, tc.overlay), func(t *testing.T) {
			runtimeCfg := config.DefaultConfig()
			runtimeCfg.Database.StateDB.ShardHexWidth = 1
			manager, err := db.NewManagerWithConfig(t.TempDir(), logs.NewNodeLogger("genesis-alignment-perf", 0), runtimeCfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(manager.Close)
			manager.InitWriteQueue(1024, time.Hour)
			roster := frostAutoPolicyMembers("miner", tc.candidates)
			top := testRankedFrostSnapshot(roster, 1)
			if len(roster) > config.VaultCandidateSnapshotLimit {
				n := config.VaultCandidateSnapshotLimit
				top.Addresses, top.Indices, top.PublicKeys = top.Addresses[:n], top.Indices[:n], top.PublicKeys[:n]
				top.MaturedStakes, top.ActiveSinceHeights = top.MaturedStakes[:n], top.ActiveSinceHeights[:n]
			}
			for i := range top.MaturedStakes {
				top.MaturedStakes[i] = protocol.MinMinerStakeRaw
				top.ActiveSinceHeights[i] = 0
			}
			if err := validateFrostCandidateSnapshot(top); err != nil {
				t.Fatal(err)
			}
			chains := ceremony.GenesisCeremonyChainOrder()
			original := make(map[string]*pb.FrostVaultState)
			ops := make([]interface{}, 0, tc.history+12)
			putProto := func(key string, value proto.Message) {
				raw, err := proto.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				ops = append(ops, &WriteOp{Key: key, Value: raw})
			}
			for _, chain := range chains {
				cfg := testVaultCommitteePolicyConfig(chain, 4)
				cfg.CommitteeScaleMinerMultiplier = 100
				cfg.SignAlgo = pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128
				if chain == "btc" {
					cfg.SignAlgo = pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340
				}
				state := &pb.FrostVaultState{Chain: chain, KeyEpoch: 1, Status: VaultStatusActive, GroupPubkey: []byte{1}, SignAlgo: cfg.SignAlgo,
					CommitteeMembers: append([]string(nil), roster[:4]...), CommitteeThreshold: 3, ActiveSinceHeight: freshGenesisAlignmentActivationHeight, NextCommitteeReviewHeight: freshGenesisAlignmentFirstReview}
				putProto(keys.KeyFrostVaultConfig(chain, 0), cfg)
				putProto(keys.KeyFrostVaultState(chain, 0), state)
				original[chain] = state
			}
			for i := 0; i < tc.history; i++ {
				ops = append(ops, &WriteOp{Key: keys.KeyFrostVaultTransition("eth", 77, uint64(i+1)), Value: []byte("retained-history")})
			}
			if _, err := manager.SyncStateUpdates(1, ops); err != nil {
				t.Fatal(err)
			}
			if err := manager.ForceFlush(); err != nil {
				t.Fatal(err)
			}
			configPrefix := strings.TrimSuffix(keys.KeyFrostVaultConfig("", 0), "_0")
			newView := func() (*genesisAlignmentIO, *genesisAlignmentBaseIO) {
				base := &genesisAlignmentBaseIO{prefixes: make(map[string]int)}
				scan := func(p string) (map[string][]byte, error) {
					base.scans++
					base.prefixes[p]++
					rows, err := manager.Scan(p)
					base.rows += len(rows)
					return rows, err
				}
				sv := NewStateViewWithLimitScanOrderedAndBatch(
					func(k string) ([]byte, error) { base.points++; return manager.Get(k) },
					func(ks []string) (map[string][]byte, error) {
						base.batches++
						base.batchKeys += len(ks)
						return manager.GetKVs(ks)
					}, scan,
					func(p string, n int) (map[string][]byte, error) {
						base.scans++
						base.prefixes[p]++
						rows, err := manager.ScanKVWithLimit(p, n)
						base.rows += len(rows)
						return rows, err
					},
					func(p string, n int) (map[string][]byte, error) {
						base.scans++
						base.prefixes[p]++
						rows, err := manager.ScanKVWithLimitReverse(p, n)
						base.rows += len(rows)
						return rows, err
					},
					func(p string, n int, r bool) ([]interfaces.KVPair, error) {
						base.scans++
						base.prefixes[p]++
						rows, err := manager.ScanKVWithLimitOrdered(p, n, r)
						base.rows += len(rows)
						return rows, err
					},
					func(p, a string, n int, r bool) ([]interfaces.KVPair, error) {
						base.scans++
						base.prefixes[p]++
						rows, err := manager.ScanKVWithLimitOrderedAfter(p, a, n, r)
						base.rows += len(rows)
						return rows, err
					})
				for i := 0; i < tc.history; i += 2 {
					sv.Del(keys.KeyFrostVaultTransition("eth", 77, uint64(i+1)))
				}
				for i := 0; i < tc.overlay; i++ {
					sv.Set(fmt.Sprintf("alignment_block_overlay_%08d", i), []byte("tx-state"))
				}
				releaseStateViewHistory(sv)
				*base = genesisAlignmentBaseIO{prefixes: make(map[string]int)}
				return &genesisAlignmentIO{StateView: sv}, base
			}
			assertIO := func(c *genesisAlignmentIO, b *genesisAlignmentBaseIO, writes int) {
				t.Helper()
				if c.gets != 1 || c.batches != 2 || c.batchKeys != 12 || c.scans != 1 || c.rows != 6 || c.sets != writes || c.deletes != 0 {
					t.Fatalf("logical IO Get=%d batches=%d keys=%d scans=%d rows=%d Set=%d Del=%d; want 1/2/12/1/6/%d/0", c.gets, c.batches, c.batchKeys, c.scans, c.rows, c.sets, c.deletes, writes)
				}
				if b.points != 1 || b.batches != 2 || b.batchKeys != 12 || b.scans != 1 || b.rows != 6 || len(b.prefixes) != 1 || b.prefixes[configPrefix] != 1 {
					t.Fatalf("base IO/history amplification: %+v", b)
				}
			}
			executor := &Executor{DB: manager}
			var ordinaryElapsed, dueElapsed time.Duration
			var ordinaryAllocs, dueAllocs float64
			for node := 0; node < 20; node++ {
				sv, base := newView()
				before := sv.Diff()
				start := time.Now()
				executor.processFrostAutoTransitions(&pb.Block{Header: &pb.BlockHeader{Height: freshGenesisAlignmentFirstReview - 1}}, top, sv)
				ordinaryElapsed += time.Since(start)
				assertIO(sv, base, 0)
				if !reflect.DeepEqual(before, sv.Diff()) {
					t.Fatal("ordinary block changed high-write overlay diff")
				}
				if node == 0 {
					ordinaryAllocs = testing.AllocsPerRun(3, func() {
						executor.processFrostAutoTransitions(&pb.Block{Header: &pb.BlockHeader{Height: 100}}, top, sv.StateView)
					})
				}
				sv, base = newView()
				start = time.Now()
				executor.processFrostAutoTransitions(&pb.Block{Header: &pb.BlockHeader{Height: freshGenesisAlignmentFirstReview}}, top, sv)
				dueElapsed += time.Since(start)
				assertIO(sv, base, 6)
				requireFreshGenesisServingGeneration(t, sv.StateView, original, freshGenesisAlignmentFirstReview+protocol.VaultCommitteeTermBlocks)
				if node == 0 {
					                                                                      
					                                                                         
					dueAllocs = testing.AllocsPerRun(3, func() {
						for _, chain := range chains {
							frostAutoPolicyStoreProto(t, sv.StateView, keys.KeyFrostVaultState(chain, 0), original[chain])
						}
						executor.processFrostAutoTransitions(&pb.Block{Header: &pb.BlockHeader{Height: freshGenesisAlignmentFirstReview}}, top, sv.StateView)
					})
				}
			}
			t.Logf("local20 each phase N=%d history=%d overlay=%d tombstones=%d: ordinary Get20/batch40/keys240/scan20/rows120/Set0 elapsed=%s warmAllocs=%.0f; due same IO Set120 elapsed=%s dueWithResetAllocs=%.0f", tc.candidates, tc.history, tc.overlay, (tc.history+1)/2, ordinaryElapsed, ordinaryAllocs, dueElapsed, dueAllocs)
			if tc.candidates == 5 {
				for _, rotated := range []bool{true, false} {
					comparisonView := func() (*genesisAlignmentIO, *genesisAlignmentBaseIO) {
						sv, base := newView()
						for ordinal, chain := range chains {
							vault := proto.Clone(original[chain]).(*pb.FrostVaultState)
							if rotated {
								for i := range vault.CommitteeMembers {
									vault.CommitteeMembers[i] = roster[(ordinal+i)%len(roster)]
								}
							}
							frostAutoPolicyStoreProto(t, sv.StateView, keys.KeyFrostVaultState(chain, 0), vault)
						}
						return sv, base
					}
					sv, base := comparisonView()
					                                                                  
					*base = genesisAlignmentBaseIO{prefixes: make(map[string]int)}
					started := time.Now()
					executor.processFrostAutoTransitions(&pb.Block{Header: &pb.BlockHeader{Height: freshGenesisAlignmentFirstReview}}, top, sv)
					comparisonElapsed := time.Since(started)
					transitions, renewals := 0, 0
					for _, chain := range chains {
						raw, exists, err := sv.StateView.Get(keys.KeyFrostVaultTransition(chain, 0, 2))
						if err != nil {
							t.Fatal(err)
						}
						if exists && len(raw) > 0 {
							transitions++
						}
						if frostAutoPolicyLoadVault(t, sv.StateView, chain, 0).NextCommitteeReviewHeight == freshGenesisAlignmentFirstReview+protocol.VaultCommitteeTermBlocks {
							renewals++
						}
					}
					wantTransitions, wantRenewals := 0, 6
					if rotated {
						wantTransitions, wantRenewals = 4, 2
					}
					if transitions != wantTransitions || renewals != wantRenewals {
						t.Fatalf("rotation=%v transitions=%d renewals=%d want=%d/%d", rotated, transitions, renewals, wantTransitions, wantRenewals)
					}
					if sv.gets != 1 || sv.batches != 2 || sv.batchKeys != 12 || sv.scans != 1 || sv.rows != 6 || sv.deletes != 0 {
						t.Fatalf("comparison lost batch path: %+v", sv)
					}
					for p, calls := range base.prefixes {
						if calls != 1 || (p != configPrefix && !strings.HasPrefix(p, keys.KeyUserActivityFamilyPrefix())) {
							t.Fatalf("repeated/unbounded prefix %s count=%d", p, calls)
						}
					}
					if rotated && (sv.sets != 30 || base.scans != 5) {
						                                                            
						                                                       
						t.Fatalf("rotated work count differs: writes=%d baseScans=%d", sv.sets, base.scans)
					}
					if !rotated && (sv.sets != 6 || base.scans != 1) {
						t.Fatalf("aligned renewal work lost: writes=%d baseScans=%d", sv.sets, base.scans)
					}
					coldAllocs := testing.AllocsPerRun(3, func() {
						view, _ := comparisonView()
						executor.processFrostAutoTransitions(&pb.Block{Header: &pb.BlockHeader{Height: freshGenesisAlignmentFirstReview}}, top, view)
					})
					t.Logf("same-shape first-term baseline rotated=%v transitions=%d renewals=%d Get=%d batch=%d batchKeys=%d logicalScan=%d baseScan=%d Set=%d hookElapsed=%s coldViewAndHookAllocs=%.0f", rotated, transitions, renewals, sv.gets, sv.batches, sv.batchKeys, sv.scans, base.scans, sv.sets, comparisonElapsed, coldAllocs)
				}
			}
		})
	}
}
