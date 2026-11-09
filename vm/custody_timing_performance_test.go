package vm

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"dex/config"
	"dex/db"
	"dex/interfaces"
	"dex/keys"
	"dex/liveness"
	"dex/logs"
	"dex/pb"
	"dex/protocol"
	"google.golang.org/protobuf/proto"
)

                                                                       
                                                                           
                                                                                 
type custodyTimingIO struct {
	gets, batches, batchKeys, scans, scanRows, logicalGets, logicalBatches, sets, dels, minerWrites, historyReads int
	prefixes                                                                                                      map[string]int
	readKeys                                                                                                      map[string]int
}

func (c *custodyTimingIO) recordKey(k string) {
	c.readKeys[k]++
	if strings.Contains(k, "history") || strings.Contains(k, "archive") {
		c.historyReads++
	}
}
func (c *custodyTimingIO) reset() {
	*c = custodyTimingIO{prefixes: make(map[string]int), readKeys: make(map[string]int)}
}

type custodyTimingView struct {
	StateView
	io *custodyTimingIO
}

func (s *custodyTimingView) Get(k string) ([]byte, bool, error) {
	s.io.logicalGets++
	return s.StateView.Get(k)
}
func (s *custodyTimingView) GetMany(ks []string) (map[string][]byte, error) {
	s.io.logicalBatches++
	return s.StateView.(interface {
		GetMany([]string) (map[string][]byte, error)
	}).GetMany(ks)
}
func (s *custodyTimingView) Set(k string, v []byte) {
	s.io.sets++
	if strings.HasPrefix(k, strings.TrimSuffix(keys.KeyLivenessMiner(0), "0")) {
		s.io.minerWrites++
	}
	s.StateView.Set(k, v)
}
func (s *custodyTimingView) Del(k string) { s.io.dels++; s.StateView.Del(k) }

type custodyTimingFixture struct {
	manager  *db.Manager
	executor *Executor
	tracker  *liveness.Tracker
	top      *pb.FrostActiveMiners
	chains   []string
	members  []string
	n, k     int
	due      uint64
}

func custodyTimingMarshal(t *testing.T, m proto.Message) []byte {
	t.Helper()
	raw, err := proto.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func newCustodyTimingFixture(t *testing.T, n int) *custodyTimingFixture {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Database.StateDB.ShardHexWidth = 1
	cfg.Database.StateDB.CheckpointEvery = 1000000
	manager, err := db.NewManagerWithConfig(t.TempDir(), logs.NewNodeLogger("custody-timing-perf", 0), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { manager.Close() })
	manager.InitWriteQueue(1024, time.Hour)
	f := &custodyTimingFixture{manager: manager, executor: &Executor{DB: manager}, n: n, k: 100, due: 1 + protocol.VaultCommitteeTermBlocks, chains: []string{"btc", "eth", "bnb", "trx", "base", "arbitrum"}}
	if n < 100 {
		f.k = 20
	}
	size := n
	if size > config.VaultCandidateSnapshotLimit {
		size = config.VaultCandidateSnapshotLimit
	}
	addresses := make([]string, size)
	for i := range addresses {
		addresses[i] = fmt.Sprintf("miner-%05d", i)
	}
	f.top = testRankedFrostSnapshot(addresses, 100)
	f.top.EligibleCount = uint32(n)
	f.members = append([]string(nil), addresses[:f.k]...)
	var ops []interface{}
	put := func(k string, v []byte) { ops = append(ops, &WriteOp{Key: k, Value: v}) }
	for _, chain := range f.chains {
		vaultCfg := testVaultCommitteePolicyConfig(chain, 4)                                                           
		vaultCfg.SignAlgo = pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128
		if chain == "btc" {
			vaultCfg.SignAlgo = pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340
		}
		put(keys.KeyFrostVaultConfig(chain, 0), custodyTimingMarshal(t, vaultCfg))
		put(keys.KeyFrostVaultState(chain, 0), custodyTimingMarshal(t, &pb.FrostVaultState{Chain: chain, VaultId: 0, KeyEpoch: 1, GroupPubkey: []byte{1}, SignAlgo: vaultCfg.SignAlgo, Status: VaultStatusActive, ActiveSinceHeight: 1, NextCommitteeReviewHeight: f.due, CommitteeMembers: f.members, CommitteeThreshold: uint32(f.k * 3 / 4)}))
	}
	                                                                          
	                                                                            
	put(keys.KeyFrostVaultConfig("obsolete", 0), custodyTimingMarshal(t, testVaultCommitteePolicyConfig("obsolete", 100)))
	put(keys.KeyFrostActiveMiners(), custodyTimingMarshal(t, f.top))
	global, _ := json.Marshal(&liveness.GlobalInfo{NextIndex: uint32(n), ActiveCount: n, WindowSize: 20})
	put(keys.KeyLivenessGlobal(), global)
	for i := 0; i < n; i++ {
		address := fmt.Sprintf("miner-%05d", i)
		miner, _ := json.Marshal(&liveness.MinerInfo{Index: uint32(i), Address: address, State: liveness.StateActive, Stake: protocol.MinMinerStakeRaw, MaturedStake: protocol.MinMinerStakeRaw, OfflineSinceHeight: 1})
		put(keys.KeyLivenessMiner(uint32(i)), miner)
		put(keys.KeyBalance(address, "FB"), custodyTimingMarshal(t, &pb.TokenBalance{Balance: "100000000000"}))
	}
	if _, err := manager.SyncStateUpdates(1, ops); err != nil {
		t.Fatal(err)
	}
	if err := manager.ForceFlush(); err != nil {
		t.Fatal(err)
	}
	f.tracker = liveness.NewTracker(liveness.DefaultConfig())
	f.tracker.SetStore(manager)
	return f
}
func (f *custodyTimingFixture) view(t *testing.T, highWrite bool) (*custodyTimingView, *custodyTimingIO) {
	t.Helper()
	c := &custodyTimingIO{}
	c.reset()
	scan := func(p string) (map[string][]byte, error) {
		c.scans++
		c.prefixes[p]++
		rows, err := f.manager.Scan(p)
		c.scanRows += len(rows)
		return rows, err
	}
	sv := NewStateViewWithLimitScanOrderedAndBatch(
		func(k string) ([]byte, error) { c.gets++; c.recordKey(k); return f.manager.Get(k) },
		func(ks []string) (map[string][]byte, error) {
			c.batches++
			c.batchKeys += len(ks)
			for _, k := range ks {
				c.recordKey(k)
			}
			return f.manager.GetKVs(ks)
		}, scan,
		func(p string, l int) (map[string][]byte, error) {
			c.scans++
			c.prefixes[p]++
			rows, err := f.manager.ScanKVWithLimit(p, l)
			c.scanRows += len(rows)
			return rows, err
		},
		func(p string, l int) (map[string][]byte, error) {
			c.scans++
			c.prefixes[p]++
			rows, err := f.manager.ScanKVWithLimitReverse(p, l)
			c.scanRows += len(rows)
			return rows, err
		},
		func(p string, l int, r bool) ([]interfaces.KVPair, error) {
			c.scans++
			c.prefixes[p]++
			rows, err := f.manager.ScanKVWithLimitOrdered(p, l, r)
			c.scanRows += len(rows)
			return rows, err
		},
		func(p, a string, l int, r bool) ([]interfaces.KVPair, error) {
			c.scans++
			c.prefixes[p]++
			rows, err := f.manager.ScanKVWithLimitOrderedAfter(p, a, l, r)
			c.scanRows += len(rows)
			return rows, err
		})
	result := &custodyTimingView{StateView: sv, io: c}
	result.Del(keys.KeyFrostVaultConfig("obsolete", 0))
	result.Set(keys.KeyFrostVaultConfig("eth", 0), custodyTimingMarshal(t, testVaultCommitteePolicyConfig("eth", 4)))
	if highWrite {
		for i := 0; i < f.n; i++ {
			key := keys.KeyBalance(fmt.Sprintf("miner-%05d", i), "FB")
			if i%2 == 0 {
				result.Del(key)
			} else {
				result.Set(key, custodyTimingMarshal(t, &pb.TokenBalance{Balance: "99999999999"}))
			}
		}
	}
	releaseStateViewHistory(result.StateView)
	c.reset()
	return result, c
}
func (f *custodyTimingFixture) commit(t *testing.T, height uint64, sv StateView) {
	t.Helper()
	diff := sv.Diff()
	ops := make([]interface{}, len(diff))
	for i := range diff {
		ops[i] = &diff[i]
	}
	if _, err := f.manager.SyncStateUpdates(height, ops); err != nil {
		t.Fatal(err)
	}
}
func custodyTimingAssertBoundedAutoIO(t *testing.T, c *custodyTimingIO) {
	t.Helper()
	prefix := strings.TrimSuffix(keys.KeyFrostVaultConfig("", 0), "_0")
	if c.scans != 1 || c.scanRows != 7 || len(c.prefixes) != 1 || c.prefixes[prefix] != 1 || c.historyReads != 0 {
		t.Fatalf("unbounded/full-state/history scan: %+v", *c)
	}
	if c.gets > 3 || c.batches > 2 || c.batchKeys > 12 {
		t.Fatalf("auto-hook IO amplification: Get=%d GetMany=%d keys=%d", c.gets, c.batches, c.batchKeys)
	}
}
func custodyTimingLog(t *testing.T, label string, n int, c *custodyTimingIO, elapsed time.Duration) {
	t.Helper()
	t.Logf("%s N=%d Get=%d GetMany=%d batchKeys=%d prefixScans=%d scanRows=%d logicalGet=%d logicalGetMany=%d sets=%d deletes=%d minerWrites=%d elapsed=%s", label, n, c.gets, c.batches, c.batchKeys, c.scans, c.scanRows, c.logicalGets, c.logicalBatches, c.sets, c.dels, c.minerWrites, elapsed)
}

func TestCustodyTimingRealStateViewOrdinaryAndDueBounds(t *testing.T) {
	for _, n := range []int{20, 255, 256, 257, 10000} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			f := newCustodyTimingFixture(t, n)
			sv, c := f.view(t, true)
			started := time.Now()
			f.executor.processFrostAutoTransitions(&pb.Block{Header: &pb.BlockHeader{Height: 100}}, f.top, sv)
			elapsed := time.Since(started)
			custodyTimingAssertBoundedAutoIO(t, c)
			if c.sets+c.dels != 0 {
				t.Fatalf("ordinary term block wrote state: %+v", *c)
			}
			for _, chain := range f.chains {
				if _, ok, _ := sv.Get(keys.KeyFrostVaultTransition(chain, 0, 2)); ok {
					t.Fatal("ordinary block created DKG")
				}
			}
			custodyTimingLog(t, "ordinary/high-write-overlay", n, c, elapsed)
			sv, c = f.view(t, true)
			started = time.Now()
			f.executor.processFrostAutoTransitions(&pb.Block{Header: &pb.BlockHeader{Height: f.due}}, f.top, sv)
			elapsed = time.Since(started)
			custodyTimingAssertBoundedAutoIO(t, c)
			if c.sets != 6 || c.dels != 0 {
				t.Fatalf("same committee must write exactly one vault per chain: %+v", *c)
			}
			for _, chain := range f.chains {
				vault := frostAutoPolicyLoadVault(t, sv, chain, 0)
				if vault.NextCommitteeReviewHeight != f.due+protocol.VaultCommitteeTermBlocks || vault.KeyEpoch != 1 || vault.ActiveSinceHeight != 1 {
					t.Fatalf("renewal changed serving generation: %+v", vault)
				}
			}
			custodyTimingLog(t, "due/same-committee", n, c, elapsed)
		})
	}
}

func TestCustodyTimingRealStateViewCandidateChangeAndShortage(t *testing.T) {
	f := newCustodyTimingFixture(t, 10000)
	short := testRankedFrostSnapshot(f.top.Addresses[:99], 100)
	short.EligibleCount = 10000
	sv, c := f.view(t, true)
	started := time.Now()
	f.executor.processFrostAutoTransitions(&pb.Block{Header: &pb.BlockHeader{Height: f.due}}, short, sv)
	elapsed := time.Since(started)
	custodyTimingAssertBoundedAutoIO(t, c)
	if c.sets+c.dels != 0 {
		t.Fatal("shortage wrote or advanced review")
	}
	for _, chain := range f.chains {
		if got := frostAutoPolicyLoadVault(t, sv, chain, 0); got.NextCommitteeReviewHeight != f.due || got.CommitteeThreshold != 75 {
			t.Fatalf("shortage changed serving vault %+v", got)
		}
	}
	custodyTimingLog(t, "due/candidate-shortage", f.n, c, elapsed)
	target := proto.Clone(f.top).(*pb.FrostActiveMiners)
	target.Addresses[0] = "replacement-00000"
	sv, c = f.view(t, true)
	started = time.Now()
	f.executor.processFrostAutoTransitions(&pb.Block{Header: &pb.BlockHeader{Height: f.due}}, target, sv)
	elapsed = time.Since(started)
	                                                                           
	                                                                               
	if c.scans != 101 || c.scanRows != 7 || c.historyReads != 0 || c.gets > 3 || c.batches > 2 || c.batchKeys > 12 || c.sets != 6*(100+3) {
		t.Fatalf("candidate change IO amplification: %+v", *c)
	}
	configPrefix := strings.TrimSuffix(keys.KeyFrostVaultConfig("", 0), "_0")
	for prefix, calls := range c.prefixes {
		if calls != 1 || (prefix != configPrefix && !strings.HasPrefix(prefix, keys.KeyUserActivityFamilyPrefix())) {
			t.Fatalf("unexpected or repeated activity scan: prefix=%s calls=%d", prefix, calls)
		}
	}
	for _, chain := range f.chains {
		raw, exists, err := sv.Get(keys.KeyFrostVaultTransition(chain, 0, 2))
		if err != nil || !exists {
			t.Fatalf("missing target transition: exists=%v err=%v", exists, err)
		}
		ts := &pb.VaultTransitionState{}
		if err := proto.Unmarshal(raw, ts); err != nil {
			t.Fatal(err)
		}
		if ts.DkgN != 100 || ts.DkgThresholdT != 75 || !reflect.DeepEqual(ts.NewCommitteeMembers, target.Addresses[:100]) || !reflect.DeepEqual(ts.OldCommitteeMembers, f.members) {
			t.Fatalf("target lost frozen K/t or member snapshot: %+v", ts)
		}
	}
	custodyTimingLog(t, "due/changed-committee", f.n, c, elapsed)
}

func TestCustodyTimingRealStateViewLivenessReportPagination(t *testing.T) {
	for _, n := range []int{20, 255, 256, 257, 10000} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			f := newCustodyTimingFixture(t, n)
			reporter := liveness.NewLocalReporter(f.tracker)
			addresses := make([]string, n)
			for i := range addresses {
				addresses[i] = fmt.Sprintf("miner-%05d", i)
			}
			reporter.ReportLivenessWithOnline(addresses, nil, addresses)
			batches, total, minerWrites := 0, 0, 0
			var elapsed time.Duration
			for {
				sampled, offline, online := reporter.GetLivenessReportsWithOnline()
				if len(sampled) == 0 {
					break
				}
				batches++
				sv, c := f.view(t, batches == 1)
				started := time.Now()
				kicked, slashed := f.tracker.ProcessBlockWithOnlineReporter(sv, uint64(100+batches), "reporter", sampled, offline, online)
				elapsed += time.Since(started)
				if len(kicked)+len(slashed) != 0 || c.gets+c.batches+c.scans != 0 || c.minerWrites != len(online) {
					t.Fatalf("liveness IO/work differs from actual report page: %+v online=%d kicked=%v", *c, len(online), kicked)
				}
				minerWrites += c.minerWrites
				total += len(online)
				f.commit(t, uint64(1+batches), sv)
			}
			if total != n || minerWrites != n || batches != (n+protocol.MaxLivenessReportsPerBlock-1)/protocol.MaxLivenessReportsPerBlock {
				t.Fatalf("pagination omitted work: N=%d total=%d writes=%d batches=%d", n, total, minerWrites, batches)
			}
			reloaded := liveness.NewTracker(liveness.DefaultConfig())
			reloaded.SetStore(f.manager)
			for _, a := range addresses {
				if m := reloaded.GetMinerByAddress(a); m == nil || m.OfflineSinceHeight != 0 {
					t.Fatalf("committed recovery missing for %s: %+v", a, m)
				}
			}
			t.Logf("liveness N=%d pages=%d reported=%d minerWrites=%d Get=0 GetMany=0 prefixScans=0 hookElapsed=%s", n, batches, total, minerWrites, elapsed)
		})
	}
}

func TestCustodyTimingRealStateViewLocal20IndependentViews(t *testing.T) {
	f := newCustodyTimingFixture(t, 10000)
	var total custodyTimingIO
	var elapsed time.Duration
	for node := 0; node < 20; node++ {
		sv, c := f.view(t, true)
		started := time.Now()
		f.executor.processFrostAutoTransitions(&pb.Block{Header: &pb.BlockHeader{Height: 100}}, f.top, sv)
		elapsed += time.Since(started)
		custodyTimingAssertBoundedAutoIO(t, c)
		total.gets += c.gets
		total.batches += c.batches
		total.batchKeys += c.batchKeys
		total.scans += c.scans
		total.scanRows += c.scanRows
		if c.sets+c.dels != 0 {
			t.Fatal("independent ordinary view created writes")
		}
	}
	custodyTimingLog(t, "local20/independent-views/aggregate", f.n, &total, elapsed)
	                                                                         
	                                                                                
	sv, _ := f.view(t, true)
	f.executor.processFrostAutoTransitions(&pb.Block{Header: &pb.BlockHeader{Height: 100}}, f.top, sv)
	allocs := testing.AllocsPerRun(5, func() {
		f.executor.processFrostAutoTransitions(&pb.Block{Header: &pb.BlockHeader{Height: 100}}, f.top, sv)
	})
	t.Logf("local20 shape: 20 independent StateViews sharing one real StateDB, not 20 processes/hosts; high-write overlay=10000 values (5000 tombstones); warm-hook allocs=%.0f", allocs)
}
