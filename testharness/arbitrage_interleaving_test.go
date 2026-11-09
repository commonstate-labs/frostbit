package testharness_test

import (
	"dex/keys"
	"dex/pb"
	"dex/testharness"
	"dex/vm"
	"testing"

	"google.golang.org/protobuf/proto"
)

func TestSpotArbitrageInterleavingCapturesCrossedPublicOrders(t *testing.T) {
	h := testharness.New(t, 3)
	defer h.Close()

	const (
		seller      = "public-seller"
		buyer       = "public-buyer"
		arbitrageur = "private-arbitrageur"
		baseToken   = "ARB_BASE"
		quoteToken  = "ARB_QUOTE"
		publicBase  = "1000000000"
		privateBase = "500000000"
		askPrice    = "100000000"
		bidPrice    = "200000000"
		privateCost = "500000000"
		privateOut  = "1000000000"
		buyerQuote  = "2000000000"
	)

	h.SeedToken(baseToken)
	h.SeedToken(quoteToken)

	                                                                           
	                                                               
	for _, owner := range []string{seller, buyer, arbitrageur} {
		h.SeedAccount(owner, "FB", "10000")
	}
	h.SeedAccount(seller, baseToken, publicBase)
	h.SeedAccount(buyer, quoteToken, buyerQuote)
	h.SeedAccount(arbitrageur, quoteToken, privateCost)

	publicAsk := testharness.BuildOrderTx(seller, baseToken, quoteToken, publicBase, askPrice, pb.OrderSide_SELL)
	privateBuy := testharness.BuildOrderTx(arbitrageur, baseToken, quoteToken, privateBase, askPrice, pb.OrderSide_BUY)
	publicBid := testharness.BuildOrderTx(buyer, baseToken, quoteToken, publicBase, bidPrice, pb.OrderSide_BUY)
	privateSell := testharness.BuildOrderTx(arbitrageur, baseToken, quoteToken, privateBase, bidPrice, pb.OrderSide_SELL)
	for _, tx := range []*pb.AnyTx{privateBuy, privateSell} {
		tx.GetBase().TargetHeight = 1
	}

	                                                                      
	                                                           
	spec, err := h.MineBlock(publicAsk, privateBuy, publicBid, privateSell)
	if err != nil {
		t.Fatalf("mine interleaved arbitrage block: %v", err)
	}
	if spec == nil || len(spec.Receipts) != 4 {
		t.Fatalf("receipts=%v, want four", spec)
	}
	assertPrivateMatch(t, spec, privateBuy.GetTxId(), publicAsk.GetTxId(), privateBase, askPrice)
	assertPrivateMatch(t, spec, privateSell.GetTxId(), publicBid.GetTxId(), privateBase, bidPrice)

	                                                                             
	                                                                             
	h.AssertBalance(arbitrageur, baseToken, "0")
	h.AssertBalance(arbitrageur, quoteToken, privateOut)
	h.AssertBalance(arbitrageur, "FB", "8000")
	h.AssertBalance(seller, quoteToken, privateOut)
	h.AssertBalance(buyer, baseToken, publicBase)
	for _, tx := range []*pb.AnyTx{privateBuy, privateSell} {
		if _, exists := h.RawGet(keys.KeyOrderState(tx.GetTxId())); exists {
			t.Fatalf("fully filled private order %s remained open", tx.GetTxId())
		}
	}
}

func TestSpotArbitrageReverseInterleavingBuysBackMoreBase(t *testing.T) {
	h := testharness.New(t, 3)
	defer h.Close()

	const (
		seller          = "reverse-public-seller"
		buyer           = "reverse-public-buyer"
		arbitrageur     = "reverse-private-arbitrageur"
		baseToken       = "REV_BASE"
		quoteToken      = "REV_QUOTE"
		bidAmount       = "1000000000"
		askAmount       = "1200000000"
		privateSellBase = "500000000"
		privateBuyBase  = "700000000"
		askPrice        = "100000000"
		bidPrice        = "200000000"
		buyerQuote      = "2000000000"
		privateQuoteOut = "300000000"
		sellerQuoteOut  = "1700000000"
	)

	h.SeedToken(baseToken)
	h.SeedToken(quoteToken)

	for _, owner := range []string{seller, buyer, arbitrageur} {
		h.SeedAccount(owner, "FB", "10000")
	}
	h.SeedAccount(seller, baseToken, askAmount)
	h.SeedAccount(buyer, quoteToken, buyerQuote)
	h.SeedAccount(arbitrageur, baseToken, privateSellBase)

	publicBid := testharness.BuildOrderTx(buyer, baseToken, quoteToken, bidAmount, bidPrice, pb.OrderSide_BUY)
	privateSell := testharness.BuildOrderTx(arbitrageur, baseToken, quoteToken, privateSellBase, bidPrice, pb.OrderSide_SELL)
	publicAsk := testharness.BuildOrderTx(seller, baseToken, quoteToken, askAmount, askPrice, pb.OrderSide_SELL)
	privateBuy := testharness.BuildOrderTx(arbitrageur, baseToken, quoteToken, privateBuyBase, askPrice, pb.OrderSide_BUY)
	for _, tx := range []*pb.AnyTx{privateSell, privateBuy} {
		tx.GetBase().TargetHeight = 1
	}

	                                                                             
	                                                                    
	                                                                             
	spec, err := h.MineBlock(publicBid, privateSell, publicAsk, privateBuy)
	if err != nil {
		t.Fatalf("mine reverse interleaved arbitrage block: %v", err)
	}
	assertPrivateMatch(t, spec, privateSell.GetTxId(), publicBid.GetTxId(), privateSellBase, bidPrice)
	assertPrivateMatch(t, spec, privateBuy.GetTxId(), publicAsk.GetTxId(), privateBuyBase, askPrice)

	h.AssertBalance(arbitrageur, baseToken, privateBuyBase)
	h.AssertBalance(arbitrageur, quoteToken, privateQuoteOut)
	h.AssertBalance(arbitrageur, "FB", "8000")
	h.AssertBalance(buyer, baseToken, bidAmount)
	h.AssertBalance(seller, quoteToken, sellerQuoteOut)
	for _, tx := range []*pb.AnyTx{privateSell, privateBuy} {
		if _, exists := h.RawGet(keys.KeyOrderState(tx.GetTxId())); exists {
			t.Fatalf("fully filled private order %s remained open", tx.GetTxId())
		}
	}
}

func TestSpotArbitrageInterleavingExecutesTwoSequentialGroupsWithSharedCapital(t *testing.T) {
	h := testharness.New(t, 3)
	defer h.Close()

	const (
		arbitrageur = "multi-private-arbitrageur"
		quoteToken  = "MULTI_QUOTE"
		baseToken1  = "MULTI_BASE_1"
		baseToken2  = "MULTI_BASE_2"
		askPrice    = "100000000"
		bidPrice    = "200000000"

		seller1 = "multi-public-seller-1"
		buyer1  = "multi-public-buyer-1"
		seller2 = "multi-public-seller-2"
		buyer2  = "multi-public-buyer-2"

		group1PublicBase  = "1000000000"
		group1PrivateBase = "500000000"
		group1BuyerQuote  = "2000000000"
		group2PublicBase  = "1000000000"
		group2PrivateBase = "500000000"
		group2BuyerQuote  = "2000000000"
	)

	h.SeedToken(baseToken1)
	h.SeedToken(baseToken2)
	h.SeedToken(quoteToken)

	for _, owner := range []string{arbitrageur, seller1, buyer1, seller2, buyer2} {
		h.SeedAccount(owner, "FB", "10000")
	}
	h.SeedAccount(arbitrageur, quoteToken, "500000000")
	h.SeedAccount(seller1, baseToken1, group1PublicBase)
	h.SeedAccount(buyer1, quoteToken, group1BuyerQuote)
	h.SeedAccount(seller2, baseToken2, group2PublicBase)
	h.SeedAccount(buyer2, quoteToken, group2BuyerQuote)

	publicAsk1 := testharness.BuildOrderTx(seller1, baseToken1, quoteToken, group1PublicBase, askPrice, pb.OrderSide_SELL)
	privateBuy1 := testharness.BuildOrderTx(arbitrageur, baseToken1, quoteToken, group1PrivateBase, askPrice, pb.OrderSide_BUY)
	publicBid1 := testharness.BuildOrderTx(buyer1, baseToken1, quoteToken, group1PublicBase, bidPrice, pb.OrderSide_BUY)
	privateSell1 := testharness.BuildOrderTx(arbitrageur, baseToken1, quoteToken, group1PrivateBase, bidPrice, pb.OrderSide_SELL)
	publicAsk2 := testharness.BuildOrderTx(seller2, baseToken2, quoteToken, group2PublicBase, askPrice, pb.OrderSide_SELL)
	privateBuy2 := testharness.BuildOrderTx(arbitrageur, baseToken2, quoteToken, group2PrivateBase, askPrice, pb.OrderSide_BUY)
	publicBid2 := testharness.BuildOrderTx(buyer2, baseToken2, quoteToken, group2PublicBase, bidPrice, pb.OrderSide_BUY)
	privateSell2 := testharness.BuildOrderTx(arbitrageur, baseToken2, quoteToken, group2PrivateBase, bidPrice, pb.OrderSide_SELL)

	privateTxs := []*pb.AnyTx{privateBuy1, privateSell1, privateBuy2, privateSell2}
	for _, tx := range privateTxs {
		tx.GetBase().TargetHeight = 1
	}

	                                                                            
	                                                                               
	                                                                              
	                               
	orderedBody := []*pb.AnyTx{
		publicAsk1, privateBuy1, publicBid1, privateSell1,
		publicAsk2, privateBuy2, publicBid2, privateSell2,
	}
	spec, err := h.MineBlock(orderedBody...)
	if err != nil {
		t.Fatalf("mine two interleaved arbitrage groups: %v", err)
	}
	if spec == nil || len(spec.Receipts) != len(orderedBody) {
		t.Fatalf("receipts=%v, want %d", spec, len(orderedBody))
	}
	for i, tx := range orderedBody {
		if spec.Receipts[i] == nil || spec.Receipts[i].TxID != tx.GetTxId() {
			t.Fatalf("receipt[%d]=%v, want tx %s", i, spec.Receipts[i], tx.GetTxId())
		}
	}

	assertPrivateMatch(t, spec, privateBuy1.GetTxId(), publicAsk1.GetTxId(), group1PrivateBase, askPrice)
	assertPrivateMatch(t, spec, privateSell1.GetTxId(), publicBid1.GetTxId(), group1PrivateBase, bidPrice)
	assertPrivateMatch(t, spec, privateBuy2.GetTxId(), publicAsk2.GetTxId(), group2PrivateBase, askPrice)
	assertPrivateMatch(t, spec, privateSell2.GetTxId(), publicBid2.GetTxId(), group2PrivateBase, bidPrice)

	h.AssertBalance(arbitrageur, baseToken1, "0")
	h.AssertBalance(arbitrageur, baseToken2, "0")
	h.AssertBalance(arbitrageur, quoteToken, "1500000000")
	h.AssertBalance(arbitrageur, "FB", "6000")
	h.AssertBalance(seller1, quoteToken, group1PublicBase)
	h.AssertBalance(buyer1, baseToken1, group1PublicBase)
	h.AssertBalance(seller2, quoteToken, group2PublicBase)
	h.AssertBalance(buyer2, baseToken2, group2PublicBase)

	accountRaw, exists := h.RawGet(keys.KeyAccount(arbitrageur))
	if !exists {
		t.Fatalf("arbitrage account %s not found", arbitrageur)
	}
	var account pb.Account
	if err := proto.Unmarshal(accountRaw, &account); err != nil {
		t.Fatalf("decode arbitrage account: %v", err)
	}
	if account.Nonce != uint64(len(privateTxs)) {
		t.Fatalf("arbitrage nonce=%d, want %d after ordered private execution", account.Nonce, len(privateTxs))
	}

	for _, tx := range privateTxs {
		if _, exists := h.RawGet(keys.KeyOrderState(tx.GetTxId())); exists {
			t.Fatalf("fully filled private order %s remained open", tx.GetTxId())
		}
	}
}

func assertPrivateMatch(t *testing.T, spec *vm.SpecResult, txID, makerID, quantity, price string) {
	t.Helper()
	for _, receipt := range spec.Receipts {
		if receipt == nil || receipt.TxID != txID {
			continue
		}
		if receipt.Status != "SUCCEED" {
			t.Fatalf("private tx %s status=%s error=%s", txID, receipt.Status, receipt.Error)
		}
		if len(receipt.Events) != 1 || receipt.Events[0].Kind != vm.EventOrderMatched {
			t.Fatalf("private tx %s events=%v, want one ORDER_MATCHED", txID, receipt.Events)
		}
		payload := receipt.Events[0].Payload
		if payload["maker_order_id"] != makerID || payload["quantity"] != quantity || payload["price"] != price {
			t.Fatalf("private tx %s match=%v, want maker=%s quantity=%s price=%s", txID, payload, makerID, quantity, price)
		}
		return
	}
	t.Fatalf("private tx %s receipt not found", txID)
}
