package vm

import (
	"dex/keys"
	"dex/rent"
	"math/big"
	"testing"

	"github.com/shopspring/decimal"
)

func TestCalculateBlockReward_FirstYear(t *testing.T) {
	params := DefaultBlockRewardParams

	                                                  
	reward := CalculateBlockReward(0, params)
	if reward.Cmp(params.InitialReward) != 0 {
		t.Errorf("height 0: expected %s, got %s", params.InitialReward, reward)
	}

	                                
	reward = CalculateBlockReward(1000, params)
	if reward.Cmp(params.InitialReward) != 0 {
		t.Errorf("height 1000: expected %s, got %s", params.InitialReward, reward)
	}

	                                      
	reward = CalculateBlockReward(31535999, params)
	if reward.Cmp(params.InitialReward) != 0 {
		t.Errorf("height 31535999: expected %s, got %s", params.InitialReward, reward)
	}
}

func TestCalculateBlockReward_SecondYear(t *testing.T) {
	params := DefaultBlockRewardParams

	                                                                     
	reward := CalculateBlockReward(31536000, params)
	expected := new(big.Int).Div(params.InitialReward, big.NewInt(2))       

	if reward.Cmp(expected) != 0 {
		t.Errorf("height 31536000: expected %s, got %s", expected, reward)
	}
}

func TestCalculateBlockReward_ThirdYear(t *testing.T) {
	params := DefaultBlockRewardParams

	                                             
	reward := CalculateBlockReward(63072000, params)
	                 
	expected := big.NewInt(250000000)                     

	if reward.Cmp(expected) != 0 {
		t.Errorf("height 63072000: expected %s, got %s", expected, reward)
	}
}

func TestCalculateBlockReward_MinReward(t *testing.T) {
	                                                         
	params := BlockRewardParams{
		InitialReward:   big.NewInt(1000),
		HalvingInterval: 100,
		DecayRate:       decimal.NewFromFloat(0.1),                          
		MinReward:       big.NewInt(50),
		BlocksPerYear:   100,
	}

	                                          
	reward := CalculateBlockReward(500, params)                                      
	if reward.Cmp(params.MinReward) != 0 {
		t.Errorf("expected min reward %s, got %s", params.MinReward, reward)
	}
}

func TestCalculateBlockReward_ZeroBlocksPerYear(t *testing.T) {
	params := BlockRewardParams{
		InitialReward:   big.NewInt(1000000000),
		DecayRate:       decimal.NewFromFloat(0.9),
		MinReward:       big.NewInt(100000),
		BlocksPerYear:   0,                         
		HalvingInterval: 31536000,
	}

	reward := CalculateBlockReward(0, params)
	if reward.Cmp(params.InitialReward) != 0 {
		t.Errorf("expected initial reward %s, got %s", params.InitialReward, reward)
	}
}

func TestSplitBlockReward_NoOtherMinersGetsAll(t *testing.T) {
	total := big.NewInt(1000113991)
	proposer, otherTotal, perMiner := SplitBlockReward(total, 0, decimal.NewFromFloat(0.5))
	if proposer.Cmp(total) != 0 {
		t.Fatalf("expected proposer to get all %s, got %s", total, proposer)
	}
	if otherTotal.Sign() != 0 {
		t.Fatalf("expected no other miner reward, got %s", otherTotal)
	}
	if perMiner != nil {
		t.Fatalf("expected nil perMiner, got %s", perMiner)
	}
}

func TestSplitBlockReward_RemainderGoesToProposer(t *testing.T) {
	total := big.NewInt(100)
	proposer, otherTotal, perMiner := SplitBlockReward(total, 3, decimal.NewFromFloat(0.5))
	if proposer.Cmp(big.NewInt(52)) != 0 {
		t.Fatalf("expected proposer=52, got %s", proposer)
	}
	if otherTotal.Cmp(big.NewInt(48)) != 0 {
		t.Fatalf("expected otherTotal=48, got %s", otherTotal)
	}
	if perMiner == nil || perMiner.Cmp(big.NewInt(16)) != 0 {
		t.Fatalf("expected perMiner=16, got %v", perMiner)
	}
	sum := new(big.Int).Add(proposer, otherTotal)
	if sum.Cmp(total) != 0 {
		t.Fatalf("split should conserve total reward: total=%s proposer=%s other=%s", total, proposer, otherTotal)
	}
}

func TestSafeAdd_Basic(t *testing.T) {
	a := big.NewInt(100)
	b := big.NewInt(200)

	result, err := SafeAdd(a, b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Cmp(big.NewInt(300)) != 0 {
		t.Errorf("expected 300, got %s", result)
	}
}

func TestSafeAdd_Overflow(t *testing.T) {
	a := new(big.Int).Set(MaxUint256)
	b := big.NewInt(1)

	_, err := SafeAdd(a, b)
	if err != ErrOverflow {
		t.Errorf("expected ErrOverflow, got %v", err)
	}
}

func TestSafeAdd_NilValues(t *testing.T) {
	result, err := SafeAdd(nil, big.NewInt(100))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Cmp(big.NewInt(100)) != 0 {
		t.Errorf("expected 100, got %s", result)
	}
}

func TestSafeSub_Basic(t *testing.T) {
	a := big.NewInt(300)
	b := big.NewInt(100)

	result, err := SafeSub(a, b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Cmp(big.NewInt(200)) != 0 {
		t.Errorf("expected 200, got %s", result)
	}
}

func TestSafeSub_Underflow(t *testing.T) {
	a := big.NewInt(100)
	b := big.NewInt(200)

	_, err := SafeSub(a, b)
	if err != ErrUnderflow {
		t.Errorf("expected ErrUnderflow, got %v", err)
	}
}

func TestParseBalance_Valid(t *testing.T) {
	balance, err := ParseBalance("12345678901234567890")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected, _ := new(big.Int).SetString("12345678901234567890", 10)
	if balance.Cmp(expected) != 0 {
		t.Errorf("expected %s, got %s", expected, balance)
	}
}

func TestEnsureAccountExistsReadFailureDoesNotWriteInit(t *testing.T) {
	owner := "fbc1qrewardreaderror000000000000000000000"
	base := newMemStateView()
	sv := keyedReadErrorStateView{StateView: base, key: keys.KeyAccount(owner)}
	if err := ensureAccountExists(sv, owner); err == nil {
		t.Fatal("account read failure must fail closed")
	}
	if _, exists, err := base.Get(keys.KeyAccount(owner)); err != nil {
		t.Fatal(err)
	} else if exists {
		t.Fatal("account read failure created an account")
	}
	if _, exists, err := base.Get(keys.KeyRentOwnerMarker(owner, rent.OwnerMarkerInit)); err != nil {
		t.Fatal(err)
	} else if exists {
		t.Fatal("account read failure wrote a complete owner marker")
	}
}

func TestAddBalanceReadFailureDoesNotOverwrite(t *testing.T) {
	owner := "fbc1qrewardbalanceerror0000000000000000000"
	base := newMemStateView()
	key := keys.KeyBalance(owner, "FB")
	sv := keyedReadErrorStateView{StateView: base, key: key}
	if err := addBalance(sv, owner, "FB", big.NewInt(1)); err == nil {
		t.Fatal("balance read failure must fail closed")
	}
	if _, exists, err := base.Get(key); err != nil {
		t.Fatal(err)
	} else if exists {
		t.Fatal("balance read failure wrote a replacement balance")
	}
}
