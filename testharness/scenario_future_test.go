package testharness_test

import (
	"dex/pb"
	th "dex/testharness"
	"dex/vm"
	"math/big"
	"testing"
)

                                                                         
  
                                                              
                                                                      
                                                                                          
  
                                                                                             
func TestScenarioFutureOpenCloseCycle(t *testing.T) {
	scn := th.NewScenario(t, 3)
	defer scn.Close()

	const (
		oneBTC   int64 = 100_000_000
		priceRaw int64 = 50_000 * 100_000_000
	)

	scn.User("alice", th.Balances{"USDT": "5000000000000"}).
		User("bob", th.Balances{"USDT": "5000000000000"}).
		SpotPrice("BTC_USDT", big.NewInt(priceRaw))

	                                                                                         
	bobSell := buildFutureOrder("bob", "BTC_USDT", futSideSell, oneBTC, priceRaw, 10)
	scn.Step(bobSell).ExpectStatus("SUCCEED")

	aliceBuy := buildFutureOrder("alice", "BTC_USDT", futSideBuy, oneBTC, priceRaw, 10)
	scn.Step(aliceBuy).
		ExpectStatus("SUCCEED").
		ExpectEvent(vm.EventOrderMatched, map[string]string{
			"pair":     "BTC_USDT",
			"taker":    "alice",
			"quantity": "100000000",
		}).
		ExpectEvent(vm.EventPositionOpened, map[string]string{
			"pair":     "BTC_USDT",
			"owner":    "alice",
			"side":     "Long",
			"quantity": "100000000",
		}).
		ExpectEvent(vm.EventFeeCharged, map[string]string{
			"pair":  "BTC_USDT",
			"owner": "alice",
			"role":  "taker",
		}).
		                                                                                                       
		                                                                                                                
		ExpectInsuranceFundGt("BTC_USDT", "0")
}

                                                                                                                     
                                                                                                         
const (
	futSideBuy  int32 = 1
	futSideSell int32 = 2
)

                                                                                                
                                                                                               
                                                                                                        

                                                                                     
                                                                                                         
  
                                                                                         
                                                                                              
func TestScenarioFutureMakerPositionCreated(t *testing.T) {
	scn := th.NewScenario(t, 3)
	defer scn.Close()

	const (
		oneBTC   int64 = 100_000_000
		priceRaw int64 = 50_000 * 100_000_000
	)

	scn.User("alice", th.Balances{"USDT": "5000000000000"}).
		User("bob", th.Balances{"USDT": "5000000000000"}).
		SpotPrice("BTC_USDT", new(big.Int).SetInt64(priceRaw))

	                                         
	scn.Step(buildFutureOrder("bob", "BTC_USDT", futSideSell, oneBTC, priceRaw, 10)).
		ExpectStatus("SUCCEED")
	                                                       
	scn.Step(buildFutureOrder("alice", "BTC_USDT", futSideBuy, oneBTC, priceRaw, 10)).
		ExpectStatus("SUCCEED")

	                                                                                    
	positions, err := scn.Harness().DB.Scan("v1_future_pair_pos_BTC_USDT_")
	if err != nil {
		t.Fatalf("scan positions: %v", err)
	}
	if len(positions) != 2 {
		t.Fatalf("expect 2 positions (alice Long + bob Short), got %d: %v", len(positions), positions)
	}
}

                                                 
func buildFutureOrder(addr, pair string, side int32, qty, price int64, leverage uint64) *pb.AnyTx {
	return &pb.AnyTx{Content: &pb.AnyTx_FutureOrderTx{FutureOrderTx: &pb.FutureOrderTx{
		Base:     &pb.BaseMessage{TxId: th.NextTxID("fut_order"), FromAddress: addr, Status: pb.Status_PENDING, Fee: "1000"},
		Pair:     pair,
		Side:     side,
		Quantity: bigStr(qty),
		Price:    bigStr(price),
		Leverage: leverage,
	}}}
}

func bigStr(v int64) string { return big.NewInt(v).String() }
