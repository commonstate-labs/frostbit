package testharness_test

import (
	"dex/pb"
	th "dex/testharness"
	"dex/vm"
	"testing"
)

                                                                                      
  
                                                                      
                         
  
                                                                                                        
                                                                                                 
                                                                            
func TestScenarioSpotLimitMatch(t *testing.T) {
	scn := th.NewScenario(t, 3)
	defer scn.Close()

	                                                          
	scn.Harness().SeedToken("BTC")
	scn.Harness().SeedToken("USDT")

	const (
		oneBTC      int64 = 100_000_000
		priceRaw    int64 = 50_000 * 100_000_000
		quoteForOne int64 = 50_000 * 100_000_000                         
	)

	scn.User("alice", th.Balances{"BTC": "100000000", "USDT": "0"}).
		User("bob", th.Balances{"USDT": "5000000000000", "BTC": "0"})

	                  
	scn.Step(th.SpotSell("alice", "BTC", "USDT", oneBTC, priceRaw)).
		ExpectStatus("SUCCEED").
		ExpectBalance("alice", "BTC", "0").
		ExpectFrozen("alice", "BTC", "100000000")

	                                           
	scn.Step(th.SpotBuy("bob", "BTC", "USDT", oneBTC, priceRaw)).
		ExpectStatus("SUCCEED").
		ExpectEvent(vm.EventOrderMatched, map[string]string{
			"pair":     "BTC_USDT",
			"taker":    "bob",
			"quantity": "100000000",
		}).
		ExpectBalance("bob", "BTC", "100000000").
		ExpectBalance("alice", "USDT", "5000000000000").
		ExpectFrozen("alice", "BTC", "0")
	_ = quoteForOne
}

                                                                     
func TestScenarioSpotCancel(t *testing.T) {
	scn := th.NewScenario(t, 3)
	defer scn.Close()

	                                                          
	scn.Harness().SeedToken("BTC")
	scn.Harness().SeedToken("USDT")

	const (
		oneBTC   int64 = 100_000_000
		priceRaw int64 = 50_000 * 100_000_000
	)

	                                       
	scn.User("alice", th.Balances{"USDT": "5000000000000"})

	                                                      
	scn.Step(th.SpotBuy("alice", "BTC", "USDT", oneBTC, priceRaw)).
		ExpectStatus("SUCCEED").
		ExpectFrozen("alice", "USDT", "5000000000000")

	                                   
	orderID := scn.LastTxID()

	cancel := &pb.AnyTx{Content: &pb.AnyTx_OrderTx{OrderTx: &pb.OrderTx{
		Base:       &pb.BaseMessage{TxId: "cancel_" + orderID, FromAddress: "alice", Status: pb.Status_PENDING, Fee: "1000"},
		Op:         pb.OrderOp_REMOVE,
		OpTargetId: orderID,
	}}}
	scn.Step(cancel).
		ExpectStatus("SUCCEED").
		ExpectEvent(vm.EventOrderCancelled, map[string]string{
			"order_id": orderID,
			"owner":    "alice",
		}).
		ExpectBalance("alice", "USDT", "5000000000000").
		ExpectFrozen("alice", "USDT", "0")
}
