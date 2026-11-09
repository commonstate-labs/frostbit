package testharness_test

import (
	"dex/liveness"
	"dex/pb"
	"dex/testharness"
	"dex/vm"
	"fmt"
	"testing"
)

                                                                   
  
                        
                                                                       
                                                                                                 
                                                                     
  
                                                                                              
                                                     
func probationCfg() liveness.Config {
	const (
		minStake = uint64(3200000000)                               
	)
	return liveness.Config{
		ActivationThreshold: 0.2,
		WindowFloor:         5,
		WindowCeil:          500,
		MinSamples:          3,
		MinReporters:        3,
		MinStake:            minStake,
		SlashPerReport:      minStake / 10,                                     
		FreezeBlocks:        5,
		Threshold:           0.8,
		PauseRatio:          0.5,
		SafetyMultiplier:    2,
		BetaRounds:          15,
		K:                   20,
		MinActiveMiners:     1,
	}
}

const (
	                                                                              
	initStake = "6400000000"
	                                                        
	seedBalance = "6400010000"
)

                                                                                             
                                                                                     
func registerActiveMiners(t *testing.T, h *testharness.Harness, n int, stake uint64) {
	t.Helper()
	for i := 0; i < n; i++ {
		addr := fmt.Sprintf("genesis_active_%d", i)
		wv := &liveness.DirectWriter{
			SetFn: func(k string, v []byte) { h.DB.EnqueueSet(k, string(v)) },
			DelFn: func(k string) { h.DB.EnqueueDel(k) },
		}
		if _, err := h.Liveness.RegisterMinerAtHeight(wv, addr, stake, 0); err != nil {
			t.Fatalf("RegisterMinerAtHeight genesis %d: %v", i, err)
		}
	}
}

                                                                        
func minerIdx(t *testing.T, h *testharness.Harness, addr string) uint32 {
	t.Helper()
	idx, ok := h.Liveness.GetMinerIndex(addr)
	if !ok {
		t.Fatalf("minerIdx: address %s not found in tracker", addr)
	}
	return idx
}

                                                                      
                                                              
                                                                      
                                                                        
                                                                            
func TestProbation_OnlineMiner_Promoted(t *testing.T) {
	cfg := probationCfg()
	h := testharness.New(t, 1, testharness.WithLivenessConfig(cfg))
	miner := h.Committee[0]

	h.SeedToken("FB")
	h.SeedAccount(miner, "FB", seedBalance)
	                                         
	h.SubmitTxExpectSuccess(h.BuildMinerTx(miner, pb.OrderOp_ADD, initStake))

	if got := h.Liveness.GetMinerState(miner); got != int(liveness.StateProbation) {
		t.Fatalf("expected Probation after join, got %d", got)
	}

	idx := minerIdx(t, h, miner)
	                                                                                    
	for i := 0; i < cfg.WindowFloor; i++ {
		h.SetSampledReports([]uint32{idx})
		h.SetOfflineReports(nil)
		if _, err := h.MineBlockAs([]string{"reporter-a", "reporter-b", "reporter-c"}[i%3]); err != nil {
			t.Fatal(err)
		}
	}

	if got := h.Liveness.GetMinerState(miner); got != int(liveness.StateActive) {
		t.Fatalf("expected Active after probation, got %d", got)
	}
}

                                                                      
                                                                        
                                                                      
func TestProbation_IndependentOfflineReportsBlockPromotion(t *testing.T) {
	cfg := probationCfg()
	cfg.MinActiveMiners = 10                               
	h := testharness.New(t, 1, testharness.WithLivenessConfig(cfg))
	miner := h.Committee[0]

	h.SeedToken("FB")
	h.SeedAccount(miner, "FB", seedBalance)
	h.SubmitTxExpectSuccess(h.BuildMinerTx(miner, pb.OrderOp_ADD, initStake))

	idx := minerIdx(t, h, miner)

	                                                           
	for i := uint64(0); i < uint64(cfg.WindowFloor); i++ {
		h.SetSampledReports([]uint32{idx})
		h.SetOfflineReports([]uint32{idx})
		if _, err := h.MineBlockAs([]string{"reporter-a", "reporter-b", "reporter-c"}[int(i)%3]); err != nil {
			t.Fatal(err)
		}
	}
	if got := h.Liveness.GetMinerState(miner); got != int(liveness.StateProbation) {
		t.Fatalf("expected Probation after sustained offline evidence, got %d", got)
	}

	m := h.Liveness.GetMinerByAddress(miner)
	if m == nil {
		t.Fatal("miner info not found")
	}
	initialStake, err := parseUint64(initStake)
	if err != nil {
		t.Fatal(err)
	}
	if m.Stake != initialStake || m.TotalSlashed != 0 || m.ExitHeight != 0 {
		t.Fatalf("unsigned reports mutated miner: %+v", m)
	}
}

                                                                      
                                                                      
                                                                      
func TestProbation_MaliciousProposerReportsCannotKick(t *testing.T) {
	cfg := probationCfg()
	cfg.MinSamples = 1
	cfg.Threshold = 0
	cfg.MinActiveMiners = 0
	cfg.PauseRatio = 2
	h := testharness.New(t, 1, testharness.WithLivenessConfig(cfg))
	miner := h.Committee[0]

	h.SeedToken("FB")
	h.SeedAccount(miner, "FB", seedBalance)
	h.SubmitTxExpectSuccess(h.BuildMinerTx(miner, pb.OrderOp_ADD, initStake))

	if got := h.Liveness.GetMinerState(miner); got != int(liveness.StateProbation) {
		t.Fatalf("expected Probation after join, got %d", got)
	}

	idx := minerIdx(t, h, miner)

	                                                                                 
	for i := 0; i < cfg.WindowFloor+3; i++ {
		h.SetSampledReports([]uint32{idx})
		h.SetOfflineReports([]uint32{idx, idx, 999999})
		if _, err := h.MineBlockAs("single-malicious-reporter"); err != nil {
			t.Fatal(err)
		}
	}

	if got := h.Liveness.GetMinerState(miner); got != int(liveness.StateProbation) {
		t.Fatalf("single reporter decided membership, got state=%d", got)
	}
	m := h.Liveness.GetMinerByAddress(miner)
	if m == nil {
		t.Fatal("miner info not found after kick")
	}
	initialStake, _ := parseUint64(initStake)
	if m.ExitHeight != 0 || m.Kicked || m.Stake != initialStake || m.TotalSlashed != 0 {
		t.Fatalf("malicious reports punished miner: %+v", m)
	}
}

                                                                      
                                    
                                                                      
func TestProbation_UnsignedReportsNeverAccumulateSlash(t *testing.T) {
	cfg := probationCfg()
	cfg.MinActiveMiners = 10                               
	h := testharness.New(t, 1, testharness.WithLivenessConfig(cfg))
	miner := h.Committee[0]

	h.SeedAccount(miner, "FB", seedBalance)
	h.SubmitTxExpectSuccess(h.BuildMinerTx(miner, pb.OrderOp_ADD, initStake))

	idx := minerIdx(t, h, miner)

	                                    
	const reports = 4
	for i := 0; i < reports; i++ {
		h.SetSampledReports([]uint32{idx})
		h.SetOfflineReports([]uint32{idx})
		h.SubmitBlock()
	}

	m := h.Liveness.GetMinerByAddress(miner)
	if m == nil {
		t.Fatal("miner info not found")
	}

	                                                          
	if got := h.Liveness.GetMinerState(miner); got != int(liveness.StateProbation) {
		t.Fatalf("expected Probation before age threshold, got %d", got)
	}

	initS, _ := parseUint64(initStake)
	if m.Stake != initS {
		t.Fatalf("unsigned reports changed stake: want=%d got=%d", initS, m.Stake)
	}
	if m.TotalSlashed != 0 {
		t.Fatalf("unsigned reports accumulated TotalSlashed=%d", m.TotalSlashed)
	}
}

                                                                      
                                                                                      
                                                                      
                                                                  
                                                                            
                                                                                   
                                                                                  
                                                                               
  
                                                                                    
                                                                          
func TestProbation_RuntimeJoiner_AppearsInParticipantsSnapshot(t *testing.T) {
	cfg := probationCfg()
	h := testharness.New(t, 1, testharness.WithLivenessConfig(cfg))
	miner := h.Committee[0]

	h.SeedAccount(miner, "FB", seedBalance)
	h.SubmitTxExpectSuccess(h.BuildMinerTx(miner, pb.OrderOp_ADD, initStake))

	                          
	if got := h.Liveness.GetMinerState(miner); got != int(liveness.StateProbation) {
		t.Fatalf("expected Probation in tracker, got %d", got)
	}

	                                                    
	snap, err := h.DB.GetMinerParticipantsSnapshot()
	if err != nil {
		t.Fatalf("GetMinerParticipantsSnapshot: %v", err)
	}
	found := false
	for _, acct := range snap {
		if acct != nil && acct.Address == miner {
			found = true
			if !acct.IsMiner {
				t.Fatalf("snapshot has miner %s but IsMiner=false", miner)
			}
			break
		}
	}
	if !found {
		addrs := make([]string, 0, len(snap))
		for _, acct := range snap {
			if acct != nil {
				addrs = append(addrs, acct.Address)
			}
		}
		t.Fatalf("runtime-joined miner %s missing from GetMinerParticipantsSnapshot, snapshot=%v", miner, addrs)
	}

	                                                                                     
	h.DB.InvalidateMinerCache()
	snap2, err := h.DB.GetMinerParticipantsSnapshot()
	if err != nil {
		t.Fatalf("GetMinerParticipantsSnapshot after invalidate: %v", err)
	}
	found = false
	for _, acct := range snap2 {
		if acct != nil && acct.Address == miner {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("after InvalidateMinerCache, runtime-joined miner %s still missing", miner)
	}
}

func TestProbation_FailedRuntimeJoiner_NotRegisteredInLiveness(t *testing.T) {
	cfg := probationCfg()
	h := testharness.New(t, 1, testharness.WithLivenessConfig(cfg))
	miner := h.Committee[0]

	h.SeedAccount(miner, "FB", "1000010000")
	tx := h.BuildMinerTx(miner, pb.OrderOp_ADD, "1000000000")
	spec, err := h.MineBlock(tx)
	if err != nil {
		t.Fatalf("MineBlock: %v", err)
	}
	rc := receiptByTxID(spec, tx.GetTxId())
	if rc == nil || rc.Status != "FAILED" {
		t.Fatalf("expected failed receipt, got %+v", rc)
	}
	if got := h.Liveness.GetMinerByAddress(miner); got != nil {
		t.Fatalf("failed miner tx registered liveness: %+v", got)
	}
}

func TestProbation_RuntimeJoiner_RegistersTotalLockedStake(t *testing.T) {
	cfg := probationCfg()
	h := testharness.New(t, 1, testharness.WithLivenessConfig(cfg))
	miner := h.Committee[0]

	                                                                            
	                                                                          
	h.SeedMinerAccount(miner, "1000010000", "3200000000")

	h.SubmitTxExpectSuccess(h.BuildMinerTx(miner, pb.OrderOp_ADD, "1000000000"))
	info := h.Liveness.GetMinerByAddress(miner)
	if info == nil {
		t.Fatal("miner not registered")
	}
	if info.Stake != 4200000000 {
		t.Fatalf("expected total locked stake 4200000000, got %d", info.Stake)
	}
}

func receiptByTxID(spec *vm.SpecResult, txID string) *vm.Receipt {
	if spec == nil {
		return nil
	}
	for _, rc := range spec.Receipts {
		if rc != nil && rc.TxID == txID {
			return rc
		}
	}
	return nil
}

                                                 
func parseUint64(s string) (uint64, error) {
	var v uint64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("parseUint64: invalid char %q in %q", c, s)
		}
		v = v*10 + uint64(c-'0')
	}
	return v, nil
}
