package testharness

import (
	"dex/keys"
	"dex/pb"
	"dex/utils"
	"dex/vm"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
)

                                                                                           
                                                
  
                                                                                               
                                                                                                            
  
                  
  
                            
                                                         
                                                  
  
                                                           
                               
                                       
type Scenario struct {
	h *Harness
	t *testing.T
	                                                                                              
	lastReceipt *vm.Receipt
	lastErr     error
	lastTxID    string
}

                                                                                     
type Balances map[string]string

                                                                                  
func NewScenario(t *testing.T, committeeSize int) *Scenario {
	t.Helper()
	h := New(t, committeeSize)
	return &Scenario{h: h, t: t}
}

                                                                                    
func (s *Scenario) Harness() *Harness { return s.h }

                                                                                     
func (s *Scenario) Close() { s.h.Close() }

                                                                                                            
                                                                      
const DefaultFeeBudget = "1000000"

                                                                                                                       
                                                                        
func (s *Scenario) User(addr string, balances Balances) *Scenario {
	s.t.Helper()
	                           
	acc := &pb.Account{Address: addr}
	aData, err := proto.Marshal(acc)
	if err != nil {
		s.t.Fatalf("marshal account: %v", err)
	}
	s.h.RawSet(keys.KeyAccount(addr), aData)

	                                                                                                            
	if balances == nil {
		balances = Balances{}
	}
	if _, hasFB := balances["FB"]; !hasFB {
		balances["FB"] = DefaultFeeBudget
	}

	                                           
	tokens := sortedKeys(balances)
	for _, token := range tokens {
		s.h.SeedAccount(addr, token, balances[token])
	}
	return s
}

                                                                                        
func (s *Scenario) SpotPrice(pair string, price *big.Int) *Scenario {
	s.t.Helper()
	if price == nil || price.Sign() <= 0 {
		s.t.Fatalf("SpotPrice: invalid price %v", price)
	}
	s.h.RawSet(keys.KeySpotLastPrice(pair), price.Bytes())
	return s
}

                                                                                  
func (s *Scenario) MarkPrice(pair string, price *big.Int) *Scenario {
	s.t.Helper()
	s.h.RawSet(keys.KeyFutureMarkPrice(pair), price.Bytes())
	return s
}

                                                                                                          
func (s *Scenario) Step(tx *pb.AnyTx) *Scenario {
	s.t.Helper()
	s.lastTxID = ""
	if tx != nil {
		if b := tx.GetBase(); b != nil {
			s.lastTxID = b.TxId
		}
	}
	spec, err := s.h.MineBlock(tx)
	s.lastErr = err
	s.lastReceipt = nil
	if spec != nil && len(spec.Receipts) > 0 {
		s.lastReceipt = spec.Receipts[0]
	}
	return s
}

                                                                                                               
func (s *Scenario) LastTxID() string { return s.lastTxID }

                                                                                                                       
func (s *Scenario) LastReceipt() *vm.Receipt { return s.lastReceipt }

                                                                                                              
func (s *Scenario) AdvanceBlocks(n int) *Scenario {
	s.t.Helper()
	for i := 0; i < n; i++ {
		if _, err := s.h.MineBlock(); err != nil {
			s.t.Fatalf("AdvanceBlocks: mine empty block: %v", err)
		}
	}
	return s
}

                               

                                                 
func (s *Scenario) ExpectStatus(want string) *Scenario {
	s.t.Helper()
	if s.lastReceipt == nil {
		s.t.Fatalf("ExpectStatus(%q): no receipt from last step (err=%v)", want, s.lastErr)
	}
	if s.lastReceipt.Status != want {
		s.t.Fatalf("ExpectStatus: want=%q got=%q err=%s", want, s.lastReceipt.Status, s.lastReceipt.Error)
	}
	return s
}

                                                                         
func (s *Scenario) ExpectError(substr string) *Scenario {
	s.t.Helper()
	if s.lastReceipt == nil {
		if s.lastErr == nil || !strings.Contains(s.lastErr.Error(), substr) {
			s.t.Fatalf("ExpectError(%q): err=%v", substr, s.lastErr)
		}
		return s
	}
	if s.lastReceipt.Status == "SUCCEED" {
		s.t.Fatalf("ExpectError(%q): step SUCCEEDED", substr)
	}
	if !strings.Contains(s.lastReceipt.Error, substr) {
		s.t.Fatalf("ExpectError: want contains=%q got=%q", substr, s.lastReceipt.Error)
	}
	return s
}

                                                                          
func (s *Scenario) ExpectBalance(addr, token, want string) *Scenario {
	s.t.Helper()
	got := s.h.GetBalance(addr, token)
	if got != want {
		s.t.Fatalf("ExpectBalance(%s,%s): want=%s got=%s", addr, token, want, got)
	}
	return s
}

                                                                                                                     
func (s *Scenario) ExpectInsuranceFundGt(pair, threshold string) *Scenario {
	s.t.Helper()
	raw, ok := s.h.RawGet(keys.KeyFutureInsuranceFund(pair))
	got := "0"
	if ok {
		got = new(big.Int).SetBytes(raw).String()
	}
	wantMin, okTh := new(big.Int).SetString(threshold, 10)
	if !okTh {
		s.t.Fatalf("ExpectInsuranceFundGt: invalid threshold %q", threshold)
	}
	gotBI, _ := new(big.Int).SetString(got, 10)
	if gotBI == nil || gotBI.Cmp(wantMin) <= 0 {
		s.t.Fatalf("ExpectInsuranceFundGt(%s): want > %s got %s", pair, threshold, got)
	}
	return s
}

                                                              
func (s *Scenario) ExpectFrozen(addr, token, want string) *Scenario {
	s.t.Helper()
	raw, ok := s.h.RawGet(keys.KeyBalance(addr, token))
	got := "0"
	if ok {
		var rec pb.TokenBalanceRecord
		if err := proto.Unmarshal(raw, &rec); err == nil && rec.Balance != nil {
			got = rec.Balance.OrderFrozenBalance
			if got == "" {
				got = "0"
			}
		}
	}
	if got != want {
		s.t.Fatalf("ExpectFrozen(%s,%s): want=%s got=%s", addr, token, want, got)
	}
	return s
}

                                                                        
                                                                           
func (s *Scenario) ExpectEvent(kind vm.EventKind, want map[string]string) *Scenario {
	s.t.Helper()
	if s.lastReceipt == nil {
		s.t.Fatalf("ExpectEvent(%s): no receipt", kind)
	}
	for _, ev := range s.lastReceipt.Events {
		if ev.Kind != kind {
			continue
		}
		if payloadContains(ev.Payload, want) {
			return s
		}
	}
	s.t.Fatalf("ExpectEvent(%s, %v): not found in %d events: %s", kind, want, len(s.lastReceipt.Events), dumpEvents(s.lastReceipt.Events))
	return s
}

                                                                                
func (s *Scenario) ExpectNoEvent(kind vm.EventKind) *Scenario {
	s.t.Helper()
	if s.lastReceipt == nil {
		return s
	}
	for _, ev := range s.lastReceipt.Events {
		if ev.Kind == kind {
			s.t.Fatalf("ExpectNoEvent(%s): found %v", kind, ev.Payload)
		}
	}
	return s
}

                                                                                  
                                                                                   
func (s *Scenario) ExpectUserActivity(addr string, kind string) *Scenario {
	s.t.Helper()
	prefix := keys.KeyUserActivityPrefix(addr)
	kvs, err := s.h.DB.Scan(prefix)
	if err != nil {
		s.t.Fatalf("ExpectUserActivity scan: %v", err)
	}
	for _, v := range kvs {
		var rec vm.UserActivityRecord
		if err := json.Unmarshal(v, &rec); err != nil {
			continue
		}
		if rec.Kind == kind {
			return s
		}
	}
	s.t.Fatalf("ExpectUserActivity(%s, %s): not found", addr, kind)
	return s
}

                                         

                                                                                                                     
func SpotSell(from, base, quote string, amount, price int64) *pb.AnyTx {
	return BuildOrderTx(from, base, quote, fmt.Sprintf("%d", amount), fmt.Sprintf("%d", price), pb.OrderSide_SELL)
}

                                            
func SpotBuy(from, base, quote string, amount, price int64) *pb.AnyTx {
	return BuildOrderTx(from, base, quote, fmt.Sprintf("%d", amount), fmt.Sprintf("%d", price), pb.OrderSide_BUY)
}

                                     

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func payloadContains(have, want map[string]string) bool {
	for k, v := range want {
		if have[k] != v {
			return false
		}
	}
	return true
}

func dumpEvents(events []vm.Event) string {
	parts := make([]string, 0, len(events))
	for _, ev := range events {
		parts = append(parts, fmt.Sprintf("%s=%v", ev.Kind, ev.Payload))
	}
	return strings.Join(parts, "; ")
}

                                                                             
var _ = utils.GeneratePairKey
