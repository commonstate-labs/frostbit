package referral

import (
	"errors"
	"math/big"
	"testing"
)

                               

type mockReferrerStore struct {
	data map[string]string
}

func newMockReferrerStore() *mockReferrerStore { return &mockReferrerStore{data: map[string]string{}} }
func (m *mockReferrerStore) GetReferrer(user string) (string, bool, error) {
	r, ok := m.data[user]
	return r, ok, nil
}
func (m *mockReferrerStore) SetReferrer(user, referrer string) error {
	m.data[user] = referrer
	return nil
}

type mockBalance struct {
	balances map[string]*big.Int                           
	addErr   error
}

func newMockBalance() *mockBalance                    { return &mockBalance{balances: map[string]*big.Int{}} }
func (m *mockBalance) key(owner, token string) string { return owner + ":" + token }
func (m *mockBalance) Set(owner, token string, amt *big.Int) {
	m.balances[m.key(owner, token)] = new(big.Int).Set(amt)
}
func (m *mockBalance) Get(owner, token string) *big.Int {
	if v, ok := m.balances[m.key(owner, token)]; ok {
		return new(big.Int).Set(v)
	}
	return big.NewInt(0)
}
func (m *mockBalance) DeductBalance(owner, token string, amount *big.Int) error {
	k := m.key(owner, token)
	bal := m.balances[k]
	if bal == nil {
		bal = big.NewInt(0)
	}
	m.balances[k] = new(big.Int).Sub(bal, amount)
	return nil
}
func (m *mockBalance) AddBalance(owner, token string, amount *big.Int) error {
	if m.addErr != nil {
		return m.addErr
	}
	k := m.key(owner, token)
	bal := m.balances[k]
	if bal == nil {
		bal = big.NewInt(0)
	}
	m.balances[k] = new(big.Int).Add(bal, amount)
	return nil
}

type mockInsurance struct {
	funds      map[string]*big.Int
	depositErr error
}

func newMockInsurance() *mockInsurance { return &mockInsurance{funds: map[string]*big.Int{}} }
func (m *mockInsurance) DepositInsuranceFund(pair string, amount *big.Int) error {
	if m.depositErr != nil {
		return m.depositErr
	}
	if m.funds[pair] == nil {
		m.funds[pair] = big.NewInt(0)
	}
	m.funds[pair].Add(m.funds[pair], amount)
	return nil
}

func bi(n int64) *big.Int { return big.NewInt(n) }

func TestCalcAndDistributeFee_PropagatesPayoutErrors(t *testing.T) {
	rs := newMockReferrerStore()
	ba := newMockBalance()
	ins := newMockInsurance()
	ba.Set("alice", "USDT", bi(1_000_000))
	rs.SetReferrer("alice", "bob")
	ba.addErr = errors.New("rebate write failed")
	if _, err := NewEngine(rs, ba, ins, DefaultConfig).CalcAndDistributeFeeWithRole("alice", "BTC_USDT", "USDT", bi(1_000_000), RoleTaker); err == nil {
		t.Fatal("expected rebate write error")
	}

	ba = newMockBalance()
	ins = newMockInsurance()
	ba.Set("alice", "USDT", bi(1_000_000))
	ins.depositErr = errors.New("insurance write failed")
	if _, err := NewEngine(newMockReferrerStore(), ba, ins, DefaultConfig).CalcAndDistributeFeeWithRole("alice", "BTC_USDT", "USDT", bi(1_000_000), RoleTaker); err == nil {
		t.Fatal("expected insurance write error")
	}
}

                       

func TestCalcAndDistributeFee_NoReferrer(t *testing.T) {
	rs := newMockReferrerStore()
	ba := newMockBalance()
	ins := newMockInsurance()
	ba.Set("alice", "USDT", bi(1_000_000))

	eng := NewEngine(rs, ba, ins, DefaultConfig)
	                                                          
	result, err := eng.CalcAndDistributeFee("alice", "BTC_USDT", "USDT", bi(1_000_000))
	if err != nil {
		t.Fatal(err)
	}
	if result.FullFee.Cmp(bi(500)) != 0 {
		t.Fatalf("fullFee: got %s, want 500", result.FullFee)
	}
	if result.ActualFee.Cmp(bi(500)) != 0 {
		t.Fatalf("actualFee: got %s, want 500", result.ActualFee)
	}
	if result.InsuranceAmount.Cmp(bi(500)) != 0 {
		t.Fatalf("insurance: got %s, want 500", result.InsuranceAmount)
	}
	if result.ReferrerRebate.Sign() != 0 {
		t.Fatalf("rebate should be 0, got %s", result.ReferrerRebate)
	}
	                                          
	if ba.Get("alice", "USDT").Cmp(bi(999_500)) != 0 {
		t.Fatalf("alice balance: got %s, want 999500", ba.Get("alice", "USDT"))
	}
	                    
	if ins.funds["BTC_USDT"].Cmp(bi(500)) != 0 {
		t.Fatalf("insurance fund: got %s, want 500", ins.funds["BTC_USDT"])
	}
}

func TestCalcAndDistributeFee_WithReferrer(t *testing.T) {
	rs := newMockReferrerStore()
	ba := newMockBalance()
	ins := newMockInsurance()
	ba.Set("alice", "USDT", bi(1_000_000))

	rs.SetReferrer("alice", "bob")

	eng := NewEngine(rs, ba, ins, DefaultConfig)
	                                                                                                        
	result, err := eng.CalcAndDistributeFee("alice", "BTC_USDT", "USDT", bi(1_000_000))
	if err != nil {
		t.Fatal(err)
	}
	if result.FullFee.Cmp(bi(500)) != 0 {
		t.Fatalf("fullFee: got %s, want 500", result.FullFee)
	}
	if result.ActualFee.Cmp(bi(450)) != 0 {
		t.Fatalf("actualFee: got %s, want 450", result.ActualFee)
	}
	if result.ReferrerRebate.Cmp(bi(100)) != 0 {
		t.Fatalf("rebate: got %s, want 100", result.ReferrerRebate)
	}
	if result.InsuranceAmount.Cmp(bi(350)) != 0 {
		t.Fatalf("insurance: got %s, want 350", result.InsuranceAmount)
	}
	                                   
	if ba.Get("alice", "USDT").Cmp(bi(999_550)) != 0 {
		t.Fatalf("alice balance: got %s, want 999550", ba.Get("alice", "USDT"))
	}
	                     
	if ba.Get("bob", "USDT").Cmp(bi(100)) != 0 {
		t.Fatalf("bob balance: got %s, want 100", ba.Get("bob", "USDT"))
	}
	                 
	if ins.funds["BTC_USDT"].Cmp(bi(350)) != 0 {
		t.Fatalf("insurance: got %s, want 350", ins.funds["BTC_USDT"])
	}
}

func TestBindReferrer(t *testing.T) {
	rs := newMockReferrerStore()
	eng := NewEngine(rs, nil, nil, DefaultConfig)

	               
	if err := eng.BindReferrer("alice", "bob"); err != nil {
		t.Fatal(err)
	}

	                           
	if err := eng.BindReferrer("charlie", "charlie"); err != ErrSelfReferral {
		t.Fatalf("want ErrSelfReferral, got %v", err)
	}

	                     
	if err := eng.BindReferrer("alice", "charlie"); err != ErrAlreadyBound {
		t.Fatalf("want ErrAlreadyBound, got %v", err)
	}
}

func TestCalcAndDistributeFee_ZeroNotional(t *testing.T) {
	eng := NewEngine(newMockReferrerStore(), newMockBalance(), newMockInsurance(), DefaultConfig)
	result, err := eng.CalcAndDistributeFee("alice", "BTC_USDT", "USDT", bi(0))
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Fatal("expected nil result for zero notional")
	}
}

func TestCalcAndDistributeFee_NilNotional(t *testing.T) {
	eng := NewEngine(newMockReferrerStore(), newMockBalance(), newMockInsurance(), DefaultConfig)
	result, err := eng.CalcAndDistributeFee("alice", "BTC_USDT", "USDT", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Fatal("expected nil result for nil notional")
	}
}

func TestCalcAndDistributeFee_NegativeNotional(t *testing.T) {
	eng := NewEngine(newMockReferrerStore(), newMockBalance(), newMockInsurance(), DefaultConfig)
	result, err := eng.CalcAndDistributeFee("alice", "BTC_USDT", "USDT", bi(-100))
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Fatal("expected nil result for negative notional")
	}
}

func TestCalcAndDistributeFee_ZeroFeeRate(t *testing.T) {
	cfg := Config{TradingFeeRate: 0, ReferrerRebateRate: 2000, RefereeDiscountRate: 1000}
	eng := NewEngine(newMockReferrerStore(), newMockBalance(), newMockInsurance(), cfg)
	result, err := eng.CalcAndDistributeFee("alice", "BTC_USDT", "USDT", bi(1_000_000))
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Fatal("expected nil result for zero fee rate")
	}
}

                                                                                              
func TestCalcAndDistributeFee_TinyNotionalTruncatesToZero(t *testing.T) {
	ba := newMockBalance()
	ba.Set("alice", "USDT", bi(1_000_000))
	eng := NewEngine(newMockReferrerStore(), ba, newMockInsurance(), DefaultConfig)
	result, err := eng.CalcAndDistributeFee("alice", "BTC_USDT", "USDT", bi(100))
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Fatal("expected nil for tiny notional where fee rounds to 0")
	}
}

                                                         
func TestCalcAndDistributeFee_MinimalNonZeroFee(t *testing.T) {
	ba := newMockBalance()
	ba.Set("alice", "USDT", bi(1_000_000))
	eng := NewEngine(newMockReferrerStore(), ba, newMockInsurance(), DefaultConfig)
	result, err := eng.CalcAndDistributeFee("alice", "BTC_USDT", "USDT", bi(2000))
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.FullFee.Cmp(bi(1)) != 0 {
		t.Fatalf("fullFee: got %s, want 1", result.FullFee)
	}
}

                           
func TestCalcAndDistributeFee_CustomHighFeeRate(t *testing.T) {
	cfg := Config{TradingFeeRate: 30, ReferrerRebateRate: 3000, RefereeDiscountRate: 500}
	rs := newMockReferrerStore()
	ba := newMockBalance()
	ins := newMockInsurance()
	ba.Set("alice", "USDT", bi(10_000_000))
	rs.SetReferrer("alice", "bob")

	eng := NewEngine(rs, ba, ins, cfg)
	                                             
	                                                                        
	                                       
	                                      
	result, err := eng.CalcAndDistributeFee("alice", "ETH_USDT", "USDT", bi(10_000_000))
	if err != nil {
		t.Fatal(err)
	}
	if result.FullFee.Cmp(bi(30_000)) != 0 {
		t.Fatalf("fullFee: got %s, want 30000", result.FullFee)
	}
	if result.ActualFee.Cmp(bi(28_500)) != 0 {
		t.Fatalf("actualFee: got %s, want 28500", result.ActualFee)
	}
	if result.ReferrerRebate.Cmp(bi(9_000)) != 0 {
		t.Fatalf("rebate: got %s, want 9000", result.ReferrerRebate)
	}
	if result.InsuranceAmount.Cmp(bi(19_500)) != 0 {
		t.Fatalf("insurance: got %s, want 19500", result.InsuranceAmount)
	}
	if result.Referrer != "bob" {
		t.Fatalf("referrer: got %s, want bob", result.Referrer)
	}
	                                         
	if ba.Get("alice", "USDT").Cmp(bi(9_971_500)) != 0 {
		t.Fatalf("alice balance: got %s, want 9971500", ba.Get("alice", "USDT"))
	}
	             
	if ba.Get("bob", "USDT").Cmp(bi(9_000)) != 0 {
		t.Fatalf("bob balance: got %s, want 9000", ba.Get("bob", "USDT"))
	}
	                    
	if ins.funds["ETH_USDT"].Cmp(bi(19_500)) != 0 {
		t.Fatalf("insurance: got %s, want 19500", ins.funds["ETH_USDT"])
	}
}

                                             
func TestCalcAndDistributeFee_MultipleTrades(t *testing.T) {
	ba := newMockBalance()
	ins := newMockInsurance()
	ba.Set("alice", "USDT", bi(10_000_000))

	eng := NewEngine(newMockReferrerStore(), ba, ins, DefaultConfig)

	for i := 0; i < 5; i++ {
		_, err := eng.CalcAndDistributeFee("alice", "BTC_USDT", "USDT", bi(1_000_000))
		if err != nil {
			t.Fatal(err)
		}
	}
	                                                  
	if ins.funds["BTC_USDT"].Cmp(bi(2500)) != 0 {
		t.Fatalf("insurance after 5 trades: got %s, want 2500", ins.funds["BTC_USDT"])
	}
	                                        
	if ba.Get("alice", "USDT").Cmp(bi(9_997_500)) != 0 {
		t.Fatalf("alice balance: got %s, want 9997500", ba.Get("alice", "USDT"))
	}
}

                                                         
func TestCalcAndDistributeFee_MultipleTradesWithReferrer(t *testing.T) {
	rs := newMockReferrerStore()
	ba := newMockBalance()
	ins := newMockInsurance()
	ba.Set("alice", "USDT", bi(10_000_000))
	rs.SetReferrer("alice", "bob")

	eng := NewEngine(rs, ba, ins, DefaultConfig)
	for i := 0; i < 3; i++ {
		_, err := eng.CalcAndDistributeFee("alice", "BTC_USDT", "USDT", bi(1_000_000))
		if err != nil {
			t.Fatal(err)
		}
	}
	                                                            
	                   
	if ba.Get("bob", "USDT").Cmp(bi(300)) != 0 {
		t.Fatalf("bob balance: got %s, want 300", ba.Get("bob", "USDT"))
	}
	                          
	if ins.funds["BTC_USDT"].Cmp(bi(1050)) != 0 {
		t.Fatalf("insurance: got %s, want 1050", ins.funds["BTC_USDT"])
	}
	                                        
	if ba.Get("alice", "USDT").Cmp(bi(9_998_650)) != 0 {
		t.Fatalf("alice balance: got %s, want 9998650", ba.Get("alice", "USDT"))
	}
}

                                       
func TestCalcAndDistributeFee_DifferentPairsSeparateInsurance(t *testing.T) {
	ba := newMockBalance()
	ins := newMockInsurance()
	ba.Set("alice", "USDT", bi(10_000_000))

	eng := NewEngine(newMockReferrerStore(), ba, ins, DefaultConfig)
	eng.CalcAndDistributeFee("alice", "BTC_USDT", "USDT", bi(1_000_000))
	eng.CalcAndDistributeFee("alice", "ETH_USDT", "USDT", bi(2_000_000))

	                                
	if ins.funds["BTC_USDT"].Cmp(bi(500)) != 0 {
		t.Fatalf("BTC insurance: got %s, want 500", ins.funds["BTC_USDT"])
	}
	if ins.funds["ETH_USDT"].Cmp(bi(1000)) != 0 {
		t.Fatalf("ETH insurance: got %s, want 1000", ins.funds["ETH_USDT"])
	}
}

                                                                                                     
func TestCalcAndDistributeFee_FundConservation(t *testing.T) {
	rs := newMockReferrerStore()
	ba := newMockBalance()
	ins := newMockInsurance()
	ba.Set("alice", "USDT", bi(100_000_000))
	rs.SetReferrer("alice", "bob")

	eng := NewEngine(rs, ba, ins, DefaultConfig)

	                                          
	notionals := []int64{2000, 10000, 77777, 1_000_000, 123_456_789}
	for _, n := range notionals {
		result, err := eng.CalcAndDistributeFee("alice", "BTC_USDT", "USDT", bi(n))
		if err != nil {
			t.Fatalf("notional=%d: %v", n, err)
		}
		if result == nil {
			continue
		}
		                                          
		sum := new(big.Int).Add(result.ReferrerRebate, result.InsuranceAmount)
		if result.ActualFee.Cmp(sum) != 0 {
			t.Fatalf("notional=%d: conservation violated: actualFee=%s != rebate(%s)+insurance(%s)=%s",
				n, result.ActualFee, result.ReferrerRebate, result.InsuranceAmount, sum)
		}
	}
}

                     
func TestCalcAndDistributeFee_LargeNotional(t *testing.T) {
	ba := newMockBalance()
	ins := newMockInsurance()
	              
	largeBalance, _ := new(big.Int).SetString("1000000000000000000", 10)
	ba.Set("whale", "USDT", largeBalance)

	eng := NewEngine(newMockReferrerStore(), ba, ins, DefaultConfig)
	                                                    
	result, err := eng.CalcAndDistributeFee("whale", "BTC_USDT", "USDT", largeBalance)
	if err != nil {
		t.Fatal(err)
	}
	expectedFee, _ := new(big.Int).SetString("500000000000000", 10)        
	if result.FullFee.Cmp(expectedFee) != 0 {
		t.Fatalf("fullFee: got %s, want %s", result.FullFee, expectedFee)
	}
	if result.InsuranceAmount.Cmp(expectedFee) != 0 {
		t.Fatalf("insurance: got %s, want %s", result.InsuranceAmount, expectedFee)
	}
}

                                                                           
func TestCalcAndDistributeFee_ReferrerTradesNoSelfRebate(t *testing.T) {
	rs := newMockReferrerStore()
	ba := newMockBalance()
	ins := newMockInsurance()
	ba.Set("bob", "USDT", bi(1_000_000))

	                                                                
	rs.SetReferrer("alice", "bob")

	eng := NewEngine(rs, ba, ins, DefaultConfig)
	result, err := eng.CalcAndDistributeFee("bob", "BTC_USDT", "USDT", bi(1_000_000))
	if err != nil {
		t.Fatal(err)
	}
	                                                                        
	if result.Referrer != "" {
		t.Fatalf("bob should have no referrer, got %s", result.Referrer)
	}
	if result.ActualFee.Cmp(bi(500)) != 0 {
		t.Fatalf("actualFee: got %s, want 500", result.ActualFee)
	}
	if result.InsuranceAmount.Cmp(bi(500)) != 0 {
		t.Fatalf("insurance: got %s, want 500", result.InsuranceAmount)
	}
}

                                                               
func TestBindReferrer_Persistence(t *testing.T) {
	rs := newMockReferrerStore()
	eng := NewEngine(rs, nil, nil, DefaultConfig)

	eng.BindReferrer("alice", "bob")

	                     
	ref, exists, _ := rs.GetReferrer("alice")
	if !exists || ref != "bob" {
		t.Fatalf("expected bob, got %s (exists=%v)", ref, exists)
	}

	                                          
	_, exists2, _ := rs.GetReferrer("charlie")
	if exists2 {
		t.Fatal("charlie should not have a referrer")
	}
}

                                             
func TestBindReferrer_MultipleUsersToSameReferrer(t *testing.T) {
	rs := newMockReferrerStore()
	eng := NewEngine(rs, nil, nil, DefaultConfig)

	if err := eng.BindReferrer("alice", "bob"); err != nil {
		t.Fatal(err)
	}
	if err := eng.BindReferrer("charlie", "bob"); err != nil {
		t.Fatal(err)
	}
	if err := eng.BindReferrer("dave", "bob"); err != nil {
		t.Fatal(err)
	}

	for _, user := range []string{"alice", "charlie", "dave"} {
		ref, exists, _ := rs.GetReferrer(user)
		if !exists || ref != "bob" {
			t.Fatalf("%s: expected referrer bob, got %s", user, ref)
		}
	}
}

                                                                              
func TestBindReferrer_CyclicChain(t *testing.T) {
	rs := newMockReferrerStore()
	eng := NewEngine(rs, nil, nil, DefaultConfig)

	                                                  
	if err := eng.BindReferrer("alice", "bob"); err != nil {
		t.Fatal(err)
	}
	                                                             
	if err := eng.BindReferrer("bob", "alice"); err != ErrCyclicReferral {
		t.Fatalf("want ErrCyclicReferral, got %v", err)
	}

	                                                                         
	if err := eng.BindReferrer("charlie", "bob"); err != nil {
		t.Fatal(err)
	}
	                                                                                                               
	                                                                                                                        
	if err := eng.BindReferrer("dave", "alice"); err != nil {
		t.Fatal(err)
	}
	if err := eng.BindReferrer("bob", "dave"); err != ErrCyclicReferral {
		t.Fatalf("3-node cycle: want ErrCyclicReferral, got %v", err)
	}

	                                                                            
	if err := eng.BindReferrer("erin", "bob"); err != nil {
		t.Fatalf("legitimate bind should succeed, got %v", err)
	}
}

                                                
func TestBindReferrer_ChainDepthLimit(t *testing.T) {
	rs := newMockReferrerStore()
	eng := NewEngine(rs, nil, nil, DefaultConfig)

	                                                                                
	                                                      
	for i := 0; i < maxReferralChainDepth+1; i++ {
		rs.SetReferrer(userID(i), userID(i+1))
	}
	                                                                                                          
	if err := eng.BindReferrer("newcomer", userID(0)); err != ErrCyclicReferral {
		t.Fatalf("want ErrCyclicReferral for over-depth chain, got %v", err)
	}
}

func userID(i int) string {
	return "u" + string(rune('0'+i%10)) + string(rune('0'+(i/10)%10))
}

                                                                                     
func TestCalcAndDistributeFee_NoChainedRebate(t *testing.T) {
	rs := newMockReferrerStore()
	ba := newMockBalance()
	ins := newMockInsurance()
	ba.Set("charlie", "USDT", bi(1_000_000))

	                          
	rs.SetReferrer("bob", "alice")
	rs.SetReferrer("charlie", "bob")

	eng := NewEngine(rs, ba, ins, DefaultConfig)
	result, err := eng.CalcAndDistributeFee("charlie", "BTC_USDT", "USDT", bi(1_000_000))
	if err != nil {
		t.Fatal(err)
	}
	                                             
	if result.Referrer != "bob" {
		t.Fatalf("referrer: got %s, want bob", result.Referrer)
	}
	                   
	if ba.Get("bob", "USDT").Cmp(bi(100)) != 0 {
		t.Fatalf("bob balance: got %s, want 100", ba.Get("bob", "USDT"))
	}
	                              
	if ba.Get("alice", "USDT").Sign() != 0 {
		t.Fatalf("alice should get nothing, got %s", ba.Get("alice", "USDT"))
	}
}
