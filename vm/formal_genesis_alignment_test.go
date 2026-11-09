package vm

import (
	"fmt"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"

	"dex/db"
	"dex/formal/itftrace"
	"dex/frost/ceremony"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"dex/protocol"
	"google.golang.org/protobuf/proto"
)

func formalGenesisAlignmentSeed(t *testing.T, noise int) (map[string][]byte, *pb.FrostActiveMiners) {
	t.Helper()
	roster := []string{"miner-0", "miner-1", "miner-2", "miner-3", "miner-4"}
	top := testRankedFrostSnapshot(roster, 1)
	for i := range top.Addresses {
		top.MaturedStakes[i] = protocol.MinMinerStakeRaw
		top.ActiveSinceHeights[i] = 0
	}
	seed := NewMockStateView()
	for _, name := range ceremony.GenesisCeremonyChainOrder() {
		                                                                       
		                                                                        
		selected := []uint64{0, 1, 2, 3}
		committee := make([]string, len(selected))
		for i, index := range selected {
			committee[i] = roster[index]
		}
		cfg := testVaultCommitteePolicyConfig(name, 4)
		cfg.CommitteeScaleMinerMultiplier = 100
		cfg.SignAlgo = pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128
		if name == "btc" {
			cfg.SignAlgo = pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340
		}
		frostAutoPolicyStoreProto(t, seed, keys.KeyFrostVaultConfig(name, 0), cfg)
		frostAutoPolicyStoreProto(t, seed, keys.KeyFrostVaultState(name, 0), &pb.FrostVaultState{Chain: name, VaultId: 0, KeyEpoch: 1, GroupPubkey: []byte{1}, SignAlgo: cfg.SignAlgo, Status: VaultStatusActive, CommitteeMembers: committee, CommitteeThreshold: 3, ActiveSinceHeight: 1, NextCommitteeReviewHeight: 1 + protocol.VaultCommitteeTermBlocks})
	}
	for i := 0; i < noise; i++ {
		seed.Set(keys.KeyFrostWithdrawJob(fmt.Sprintf("alignment-noise-%08d", i)), []byte("unrelated-state"))
	}
	return seed.data, top
}

func formalGenesisAlignmentTarget(top *pb.FrostActiveMiners, kind int64, height uint64) *pb.FrostActiveMiners {
	result := proto.Clone(top).(*pb.FrostActiveMiners)
	result.Height, result.UpdateHeight = height, height
	order := []int{0, 1, 2, 3, 4}
	if kind == 1 {
		order = []int{3, 2, 1, 0, 4}
	}
	if kind == 2 {
		order = []int{4, 0, 1, 2, 3}
	}
	for i, original := range order {
		result.Addresses[i], result.Indices[i], result.PublicKeys[i] = top.Addresses[original], top.Indices[original], top.PublicKeys[original]
		if kind != 0 {
			result.MaturedStakes[i] = protocol.MinMinerStakeRaw + uint64(len(order)-i)
		}
	}
	return result
}

func TestFormalGenesisAlignmentRuntimeITFDifferential(t *testing.T) {
	path := os.Getenv("DEX_FORMAL_ITF_GENESIS_ALIGNMENT")
	if path == "" {
		t.Skip("formal/formal_genesis_alignment.ps1 supplies the exact Quint trace")
	}
	trace, err := itftrace.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(trace.States) != 21 || protocol.VaultCommitteeTermBlocks != 120960 {
		t.Fatalf("alignment states=%d term=%d", len(trace.States), protocol.VaultCommitteeTermBlocks)
	}
	seed, base := formalGenesisAlignmentSeed(t, 33)
	sv := newOverlayOrderedTestStateView(seed)
	x := &Executor{DB: &initializedDkgDBManager{}}
	scale := protocol.VaultCommitteeTermBlocks / 3
	reviews := 0
	for index, state := range trace.States {
		modelReviews, err := state.Int("reviews")
		if err != nil {
			t.Fatal(err)
		}
		if modelReviews == int64(reviews) {
			continue
		}
		if modelReviews != int64(reviews+1) {
			t.Fatal("trace skipped a review event")
		}
		h, err := state.Int("height")
		if err != nil {
			t.Fatal(err)
		}
		kind, err := state.Int("targetKind")
		if err != nil {
			t.Fatal(err)
		}
		expectedReview, err := state.Int("nextReview")
		if err != nil {
			t.Fatal(err)
		}
		created, err := state.Bool("running")
		if err != nil {
			t.Fatal(err)
		}
		                                                                   
		                                                                          
		height := 1 + uint64(h)*scale
		snapshot := formalGenesisAlignmentTarget(base, kind, height)
		x.processFrostAutoTransitions(&pb.Block{Header: &pb.BlockHeader{Height: height}}, snapshot, sv)
		for _, name := range ceremony.GenesisCeremonyChainOrder() {
			vault := frostAutoPolicyLoadVault(t, sv, name, 0)
			if len(vault.PendingCommitteeDigest) != 0 || vault.PendingCommitteeSinceHeight != 0 {
				t.Fatalf("step%d %s retained obsolete rolling confirmation", index, name)
			}
			if vault.KeyEpoch != 1 || vault.ActiveSinceHeight != 1 || vault.NextCommitteeReviewHeight != 1+uint64(expectedReview)*scale || vault.CommitteeThreshold != 3 || !slices.Equal(vault.CommitteeMembers, base.Addresses[:4]) || !reflect.DeepEqual(vault.GroupPubkey, []byte{1}) {
				t.Fatalf("step%d %s serving authority/review differs: %+v", index, name, vault)
			}
			raw, found, err := sv.Get(keys.KeyFrostVaultTransition(name, 0, 2))
			if err != nil || found != created {
				t.Fatalf("step%d %s proposed epoch2=%v model=%v err=%v", index, name, found, created, err)
			}
			if created {
				var transition pb.VaultTransitionState
				if err := proto.Unmarshal(raw, &transition); err != nil {
					t.Fatal(err)
				}
				if transition.DkgN != 4 || transition.DkgThresholdT != 3 || transition.TriggerHeight != height+1 || !slices.Equal(transition.NewCommitteeMembers, snapshot.Addresses[:4]) {
					t.Fatal("term boundary did not freeze exact target without old confirmation")
				}
			}
		}
		reviews++
	}
	if reviews != 6 {
		t.Fatalf("compared reviews=%d want6", reviews)
	}
}

func TestFormalGenesisAlignmentShortageRecovery(t *testing.T) {
	seed, base := formalGenesisAlignmentSeed(t, 0)
	sv := newOverlayOrderedTestStateView(seed)
	x := &Executor{DB: &initializedDkgDBManager{}}
	due := uint64(1) + protocol.VaultCommitteeTermBlocks
	short := proto.Clone(base).(*pb.FrostActiveMiners)
	short.Addresses, short.Indices, short.PublicKeys = short.Addresses[:3], short.Indices[:3], short.PublicKeys[:3]
	short.MaturedStakes, short.ActiveSinceHeights, short.EligibleCount = short.MaturedStakes[:3], short.ActiveSinceHeights[:3], 3
	short.Height, short.UpdateHeight = due, due
	x.processFrostAutoTransitions(&pb.Block{Header: &pb.BlockHeader{Height: due}}, short, sv)
	for _, name := range ceremony.GenesisCeremonyChainOrder() {
		vault := frostAutoPolicyLoadVault(t, sv, name, 0)
		if vault.KeyEpoch != 1 || vault.NextCommitteeReviewHeight != due || vault.ActiveSinceHeight != 1 || vault.CommitteeThreshold != 3 || len(vault.CommitteeMembers) != 4 {
			t.Fatal("candidate shortage changed expired serving authority")
		}
		frostAutoPolicyRequireNoTransition(t, sv, keys.KeyFrostVaultTransition(name, 0, 2))
	}
	x.processFrostAutoTransitions(&pb.Block{Header: &pb.BlockHeader{Height: due + 1}}, formalGenesisAlignmentTarget(base, 2, due+1), sv)
	for _, name := range ceremony.GenesisCeremonyChainOrder() {
		transition := mustLoadFormalTransition(t, sv, keys.KeyFrostVaultTransition(name, 0, 2))
		if transition.TriggerHeight != due+2 || transition.DkgN != 4 || transition.DkgThresholdT != 3 {
			t.Fatal("restored complete candidates did not retry immediately at expired review")
		}
	}
}

type formalGenesisAlignmentIO struct {
	StateView
	gets, scans, batches, batchKeys, writes int
}

func (s *formalGenesisAlignmentIO) Get(key string) ([]byte, bool, error) {
	s.gets++
	return s.StateView.Get(key)
}
func (s *formalGenesisAlignmentIO) GetMany(keys []string) (map[string][]byte, error) {
	s.batches++
	s.batchKeys += len(keys)
	return getStateMany(s.StateView, keys)
}
func (s *formalGenesisAlignmentIO) Scan(prefix string) (map[string][]byte, error) {
	s.scans++
	return s.StateView.Scan(prefix)
}
func (s *formalGenesisAlignmentIO) Set(key string, value []byte) {
	s.writes++
	s.StateView.Set(key, value)
}
func (s *formalGenesisAlignmentIO) Del(key string) { s.writes++; s.StateView.Del(key) }

func TestFormalGenesisAlignmentRealStateViewIO(t *testing.T) {
	for _, noise := range []int{31, 32, 33, 5000, 20000} {
		t.Run(fmt.Sprint(noise), func(t *testing.T) {
			seed, snapshot := formalGenesisAlignmentSeed(t, noise)
			manager, err := db.NewManager(t.TempDir(), logs.NewNodeLogger("genesis-term-formal", 0))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { manager.Close() })
			manager.InitWriteQueue(1024, time.Hour)
			writes := make([]interface{}, 0, len(seed))
			for key, value := range seed {
				writes = append(writes, &WriteOp{Key: key, Value: value})
			}
			if _, err := manager.SyncStateUpdates(1, writes); err != nil {
				t.Fatal(err)
			}
			if err := manager.ForceFlush(); err != nil {
				t.Fatal(err)
			}
			views := 1
			if noise >= 5000 {
				views = 20
			}
			elapsed := time.Duration(0)
			for node := 0; node < views; node++ {
				baseGets, baseScans, baseBatches, baseKeys := 0, 0, 0, 0
				view := NewStateViewWithLimitScanOrderedAndBatch(func(key string) ([]byte, error) { baseGets++; return manager.Get(key) },
					func(keys []string) (map[string][]byte, error) {
						baseBatches++
						baseKeys += len(keys)
						return manager.GetKVs(keys)
					},
					func(prefix string) (map[string][]byte, error) { baseScans++; return manager.Scan(prefix) }, manager.ScanKVWithLimit, manager.ScanKVWithLimitReverse, manager.ScanKVWithLimitOrdered, manager.ScanKVWithLimitOrderedAfter)
				for i := 0; i < noise; i += 2 {
					view.Del(keys.KeyFrostWithdrawJob(fmt.Sprintf("alignment-noise-%08d", i)))
				}
				before := view.Diff()
				counted := &formalGenesisAlignmentIO{StateView: view}
				x := &Executor{DB: &initializedDkgDBManager{}}
				start := time.Now()
				x.processFrostAutoTransitions(&pb.Block{Header: &pb.BlockHeader{Height: 100}}, snapshot, counted)
				elapsed += time.Since(start)
				if counted.gets != 1 || counted.batches != 2 || counted.batchKeys != 12 || counted.scans != 1 || counted.writes != 0 || baseGets != 1 || baseScans != 1 || baseBatches != 2 || baseKeys != 12 {
					t.Fatalf("stable term IO logical=%d/%d/%d/%d/%d base=%d/%d/%d/%d", counted.gets, counted.batches, counted.batchKeys, counted.scans, counted.writes, baseGets, baseBatches, baseKeys, baseScans)
				}
				if !reflect.DeepEqual(before, view.Diff()) {
					t.Fatal("pre-term hook changed real overlay")
				}
				counted.gets, counted.batches, counted.batchKeys, counted.scans, counted.writes = 0, 0, 0, 0, 0
				due := uint64(1) + protocol.VaultCommitteeTermBlocks
				x.processFrostAutoTransitions(&pb.Block{Header: &pb.BlockHeader{Height: due}}, formalGenesisAlignmentTarget(snapshot, 0, due), counted)
				if counted.gets != 1 || counted.batches != 2 || counted.batchKeys != 12 || counted.scans != 1 || counted.writes != 6 {
					t.Fatalf("same-set expiry must renew all six: points=%d batches=%d keys=%d scans=%d writes=%d", counted.gets, counted.batches, counted.batchKeys, counted.scans, counted.writes)
				}
				for _, name := range ceremony.GenesisCeremonyChainOrder() {
					vault := frostAutoPolicyLoadVault(t, view, name, 0)
					if vault.NextCommitteeReviewHeight != due+protocol.VaultCommitteeTermBlocks || vault.ActiveSinceHeight != 1 || vault.KeyEpoch != 1 || vault.CommitteeThreshold != 3 {
						t.Fatal("renewal changed actual activation or key epoch")
					}
				}
			}
			t.Logf("noise=%d tombstones/view=%d readers=%d preTerm=1Get+2batches/12keys+1scan/0writes expiry=6renewalWrites hookOnly=%s", noise, (noise+1)/2, views, elapsed)
		})
	}
}
