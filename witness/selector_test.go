package witness

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"
)

                                                                                                    
                                           
                                                            
                                           
                                       
                         
                                                    
                      
                                                     

                                                       

func mkCandidate(addr string, stake int64, active bool) *WitnessCandidate {
	return &WitnessCandidate{Address: addr, StakeAmount: decimal.NewFromInt(stake), IsActive: active}
}

func TestSelector_CalculateWitnessCount(t *testing.T) {
	cfg := DefaultConfig()
	cfg.InitialWitnessCount = 3
	cfg.ExpandMultiplier = 2
	s := NewSelector(cfg)

	cases := map[uint32]uint32{0: 3, 1: 6, 2: 12, 3: 24}
	for round, want := range cases {
		if got := s.GetWitnessCountForRound(round); got != want {
			t.Errorf("round=%d count=%d want=%d", round, got, want)
		}
	}
}

func TestSelector_DeterministicSameInputSameOutput(t *testing.T) {
	s := NewSelector(DefaultConfig())
	cands := []*WitnessCandidate{
		mkCandidate("a", 1000, true),
		mkCandidate("b", 1000, true),
		mkCandidate("c", 1000, true),
		mkCandidate("d", 1000, true),
		mkCandidate("e", 1000, true),
		mkCandidate("f", 1000, true),
	}

	first, err := s.SelectWitnesses("req-1", 0, cands, nil, nil)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	for i := 0; i < 10; i++ {
		got, _ := s.SelectWitnesses("req-1", 0, cands, nil, nil)
		if len(got) != len(first) {
			t.Fatal("non-deterministic count")
		}
		for j := range got {
			if got[j] != first[j] {
				t.Fatalf("non-deterministic order at %d: got=%v first=%v", j, got, first)
			}
		}
	}
}

func TestSelector_DifferentRequestIDDifferentResult(t *testing.T) {
	s := NewSelector(DefaultConfig())
	cands := []*WitnessCandidate{
		mkCandidate("a", 1000, true), mkCandidate("b", 1000, true),
		mkCandidate("c", 1000, true), mkCandidate("d", 1000, true),
		mkCandidate("e", 1000, true), mkCandidate("f", 1000, true),
		mkCandidate("g", 1000, true), mkCandidate("h", 1000, true),
	}
	a, _ := s.SelectWitnesses("req-A", 0, cands, nil, nil)
	b, _ := s.SelectWitnesses("req-B", 0, cands, nil, nil)
	                                                                                          
	differ := false
	for i := range a {
		if i < len(b) && a[i] != b[i] {
			differ = true
			break
		}
	}
	if !differ {
		t.Fatalf("different requestID should permute selection: a=%v b=%v", a, b)
	}
}

func TestSelector_ExcludesAddressesAndInactive(t *testing.T) {
	s := NewSelector(DefaultConfig())
	cands := []*WitnessCandidate{
		mkCandidate("a", 1000, true),
		mkCandidate("b", 1000, false),                            
		mkCandidate("c", 1000, true),
		mkCandidate("d", 1000, true),
	}
	exclude := map[string]bool{"a": true}
	got, err := s.SelectWitnesses("req", 0, cands, exclude, nil)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	for _, addr := range got {
		if addr == "a" || addr == "b" {
			t.Fatalf("excluded/inactive address %q must not appear, got=%v", addr, got)
		}
	}
}

                                                                               
func TestSelector_NoActiveCandidatesReturnsError(t *testing.T) {
	s := NewSelector(DefaultConfig())
	cands := []*WitnessCandidate{
		mkCandidate("a", 100, false),
		mkCandidate("b", 100, false),
	}
	if _, err := s.SelectWitnesses("req", 0, cands, nil, nil); err == nil {
		t.Fatal("must error when no active candidates")
	}
}

func TestSelector_FewerCandidatesShrinksToAll(t *testing.T) {
	cfg := DefaultConfig()
	cfg.InitialWitnessCount = 5
	s := NewSelector(cfg)
	cands := []*WitnessCandidate{
		mkCandidate("a", 1, true), mkCandidate("b", 1, true), mkCandidate("c", 1, true),
	}
	got, err := s.SelectWitnesses("req", 0, cands, nil, nil)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("count must shrink to %d, got %d", 3, len(got))
	}
}

func TestSelector_ZeroStakeFallsBackToEqualWeight(t *testing.T) {
	s := NewSelector(DefaultConfig())
	cands := []*WitnessCandidate{
		mkCandidate("a", 0, true), mkCandidate("b", 0, true),
		mkCandidate("c", 0, true), mkCandidate("d", 0, true),
		mkCandidate("e", 0, true), mkCandidate("f", 0, true),
	}
	got, err := s.SelectWitnesses("req", 0, cands, nil, nil)
	if err != nil {
		t.Fatalf("zero-stake fallback path must succeed: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("zero-stake fallback must still select someone")
	}
}

func TestSelector_VerifyWitnessSelectionMatchesSelect(t *testing.T) {
	s := NewSelector(DefaultConfig())
	cands := []*WitnessCandidate{
		mkCandidate("a", 100, true), mkCandidate("b", 200, true),
		mkCandidate("c", 300, true), mkCandidate("d", 400, true),
		mkCandidate("e", 500, true), mkCandidate("f", 600, true),
	}
	picked, _ := s.SelectWitnesses("req-verify", 0, cands, nil, nil)
	if !s.VerifyWitnessSelection("req-verify", 0, cands, picked, nil) {
		t.Fatal("Verify must accept the same selection it produced")
	}
	tampered := append([]string{"intruder"}, picked[1:]...)
	if s.VerifyWitnessSelection("req-verify", 0, cands, tampered, nil) {
		t.Fatal("Verify must reject tampered selection")
	}
}

func TestSelector_SelectArbitrators_ExcludesOriginal(t *testing.T) {
	s := NewSelector(DefaultConfig())
	cands := []*WitnessCandidate{
		mkCandidate("w1", 1, true), mkCandidate("w2", 1, true),
		mkCandidate("a1", 1, true), mkCandidate("a2", 1, true),
		mkCandidate("a3", 1, true), mkCandidate("a4", 1, true),
		mkCandidate("a5", 1, true), mkCandidate("a6", 1, true),
		mkCandidate("a7", 1, true), mkCandidate("a8", 1, true),
		mkCandidate("a9", 1, true), mkCandidate("a10", 1, true),
	}
	arbs, err := s.SelectArbitrators("ch-1", 0, cands, []string{"w1", "w2"}, nil)
	if err != nil {
		t.Fatalf("arbitrators: %v", err)
	}
	for _, a := range arbs {
		if a == "w1" || a == "w2" {
			t.Fatalf("arbitrator %q overlaps original witnesses", a)
		}
	}
}

                                                                           

func TestGetConsensusThreshold_DescendsWithRound(t *testing.T) {
	cases := []struct {
		round uint32
		want  uint32
	}{{0, 80}, {1, 70}, {2, 60}, {3, 50}, {99, 50}}
	for _, c := range cases {
		if got := GetConsensusThreshold(c.round); got != c.want {
			t.Errorf("round=%d threshold=%d want=%d", c.round, got, c.want)
		}
	}
}

                                                                      

func TestIsSupportedChain(t *testing.T) {
	cases := map[string]bool{
		"BTC": true, "btc": true, "ETH": true, "BSC": true, "TRON": true,
		"DOGE": false, "": false, "BTC ": false,
	}
	for chain, want := range cases {
		if got := IsSupportedChain(chain); got != want {
			t.Errorf("IsSupportedChain(%q)=%v want %v", chain, got, want)
		}
	}
}

                                                                                             

func TestCalculateVoteStats(t *testing.T) {
	if got := CalculateVoteStats(0, 0, 0, 0); got.Total != 0 {
		t.Fatalf("total=0 must produce empty stats, got %+v", got)
	}
	got := CalculateVoteStats(60, 30, 10, 100)
	if got.PassRatio != 60 || got.FailRatio != 30 || got.AbstainRatio != 10 {
		t.Fatalf("ratios wrong: %+v", got)
	}
}

func TestDetermineConsensus_AllBranches(t *testing.T) {
	mk := func(p, f, a, total uint32) *VoteStats { return CalculateVoteStats(p, f, a, total) }
	cases := []struct {
		name      string
		stats     *VoteStats
		threshold uint32
		isDead    bool
		want      ConsensusResult
	}{
		{"pass over threshold", mk(80, 10, 10, 100), 80, false, ConsensusPass},
		{"fail over threshold", mk(10, 80, 10, 100), 80, false, ConsensusFail},
		{"deadline conflict (pass+fail both >0)", mk(40, 40, 20, 100), 80, true, ConsensusConflict},
		{"deadline expand (high abstain)", mk(20, 0, 80, 100), 80, true, ConsensusExpand},
		{"none yet (no deadline, low pass/fail)", mk(20, 20, 60, 100), 80, false, ConsensusNone},
		                                                                                                  
		                                                                            
		{"deadline reached but no conflict & abstain 0", mk(10, 0, 0, 100), 80, true, ConsensusNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DetermineConsensus(c.stats, c.threshold, c.isDead); got != c.want {
				t.Errorf("got=%v want=%v", got, c.want)
			}
		})
	}
}

                                                           

func TestStakeManager_ValidateStake(t *testing.T) {
	cfg := DefaultConfig()
	sm := NewStakeManager(cfg)
	min := decimal.RequireFromString(cfg.MinStakeAmount)

	if err := sm.ValidateStake("alice", min.Sub(decimal.NewFromInt(1))); !errors.Is(err, ErrInsufficientStake) {
		t.Fatalf("below-min must return ErrInsufficientStake, got %v", err)
	}
	if err := sm.ValidateStake("alice", min); err != nil {
		t.Fatalf("min stake must pass, got %v", err)
	}

	                                      
	if _, err := sm.ProcessStake("alice", min, 1); err != nil {
		t.Fatalf("ProcessStake: %v", err)
	}
	if err := sm.ValidateStake("alice", min); !errors.Is(err, ErrWitnessAlreadyActive) {
		t.Fatalf("active witness must return ErrWitnessAlreadyActive, got %v", err)
	}
}

func TestStakeManager_UnstakeUnknownWitnessRejected(t *testing.T) {
	sm := NewStakeManager(DefaultConfig())
	if err := sm.ValidateUnstake("nobody"); !errors.Is(err, ErrWitnessNotFound) {
		t.Fatalf("unknown unstake must error: %v", err)
	}
}

func TestStakeManager_ProcessSlashReducesStakeAndCounts(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SlashRatio = 0.5
	sm := NewStakeManager(cfg)

	stake := decimal.RequireFromString(cfg.MinStakeAmount).Mul(decimal.NewFromInt(2))
	if _, err := sm.ProcessStake("alice", stake, 1); err != nil {
		t.Fatalf("ProcessStake: %v", err)
	}
	info, slashed, err := sm.ProcessSlash("alice", "bad", 100)
	if err != nil {
		t.Fatalf("Slash: %v", err)
	}
	wantSlashed := stake.Mul(decimal.NewFromFloat(cfg.SlashRatio))
	if !slashed.Equal(wantSlashed) {
		t.Fatalf("slash amount=%s want %s", slashed, wantSlashed)
	}
	remain, _ := decimal.NewFromString(info.StakeAmount)
	wantRemain := stake.Sub(wantSlashed)
	if !remain.Equal(wantRemain) {
		t.Fatalf("remaining stake=%s want %s", info.StakeAmount, wantRemain)
	}
	if info.SlashedCount != 1 {
		t.Fatalf("SlashedCount=%d want 1", info.SlashedCount)
	}
}

func TestStakeManager_ProcessSlashUnknownErrors(t *testing.T) {
	sm := NewStakeManager(DefaultConfig())
	if _, _, err := sm.ProcessSlash("ghost", "x", 1); !errors.Is(err, ErrWitnessNotFound) {
		t.Fatalf("unknown slash must error, got %v", err)
	}
}
