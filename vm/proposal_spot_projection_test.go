package vm

import (
	"dex/db"
	"dex/keys"
	"dex/pb"
	"dex/utils"
	"fmt"
	"math/big"
	"testing"

	"google.golang.org/protobuf/proto"
)

func putProposalSpotMaker(t *testing.T, sv StateView, baseToken, quoteToken, orderID, owner string, side pb.OrderSide, price, amount, filled *big.Int) {
	t.Helper()
	state := &pb.OrderState{
		OrderId:    orderID,
		Owner:      owner,
		BaseToken:  baseToken,
		QuoteToken: quoteToken,
		Side:       side,
		Price:      price.String(),
		Amount:     amount.String(),
		FilledBase: filled.String(),
		Status:     pb.OrderStateStatus_ORDER_OPEN,
	}
	data, err := proto.Marshal(state)
	if err != nil {
		t.Fatalf("marshal maker %s: %v", orderID, err)
	}
	priceKey, err := db.PriceToKey128(price.String())
	if err != nil {
		t.Fatalf("price key %s: %v", orderID, err)
	}
	pair := utils.GeneratePairKey(baseToken, quoteToken)
	sv.Set(keys.KeyOrderState(orderID), data)
	sv.Set(keys.KeyOrderPriceIndex(pair, side, false, priceKey, orderID), []byte{1})
}

func TestProposalSpotCaptureUsesPostPrefixBookOrderAndStopsAtTakerMaker(t *testing.T) {
	const (
		baseToken  = "TOKEN_A"
		quoteToken = "USD_T"
		taker      = "arbitrage-account"
	)
	sv := newMemStateView()
	one := big.NewInt(1)
	quantity := big.NewInt(10)

	                                                                           
	                                                                          
	                                        
	putProposalSpotMaker(t, sv, baseToken, quoteToken, "a-external-sell", "local-miner", pb.OrderSide_SELL, big.NewInt(100_000_000), quantity, one)
	putProposalSpotMaker(t, sv, baseToken, quoteToken, "m-self-sell", taker, pb.OrderSide_SELL, big.NewInt(100_000_000), quantity, big.NewInt(0))
	putProposalSpotMaker(t, sv, baseToken, quoteToken, "z-hidden-sell", "zoe", pb.OrderSide_SELL, big.NewInt(100_000_000), quantity, big.NewInt(0))
	putProposalSpotMaker(t, sv, baseToken, quoteToken, "worse-sell", "will", pb.OrderSide_SELL, big.NewInt(101_000_000), quantity, big.NewInt(0))

	                                                                            
	                                                                              
	putProposalSpotMaker(t, sv, baseToken, quoteToken, "z-external-buy", "local-miner", pb.OrderSide_BUY, big.NewInt(110_000_000), quantity, big.NewInt(2))
	putProposalSpotMaker(t, sv, baseToken, quoteToken, "m-self-buy", taker, pb.OrderSide_BUY, big.NewInt(110_000_000), quantity, big.NewInt(0))
	putProposalSpotMaker(t, sv, baseToken, quoteToken, "a-hidden-buy", "amy", pb.OrderSide_BUY, big.NewInt(110_000_000), quantity, big.NewInt(0))
	putProposalSpotMaker(t, sv, baseToken, quoteToken, "worse-buy", "wendy", pb.OrderSide_BUY, big.NewInt(109_000_000), quantity, big.NewInt(0))

	block := &pb.Block{Body: []*pb.AnyTx{{Content: &pb.AnyTx_OrderTx{OrderTx: &pb.OrderTx{
		Base:       &pb.BaseMessage{TxId: "public-add"},
		Op:         pb.OrderOp_ADD,
		BaseToken:  baseToken,
		QuoteToken: quoteToken,
	}}}}}
	capture := &proposalSpotCapture{takerAddress: taker, maxOrders: 8}
	if err := capture.capturePostTransactions(sv, block, []*Receipt{{TxID: "public-add", Status: "SUCCEED"}}); err != nil {
		t.Fatalf("capture projection: %v", err)
	}
	if capture.result == nil || len(capture.result.Levels) != 2 {
		t.Fatalf("levels = %+v, want BUY and SELL", capture.result)
	}

	buy := capture.result.Levels[0]
	if buy.BaseToken != baseToken || buy.QuoteToken != quoteToken || buy.TakerSide != pb.OrderSide_BUY || buy.Price != "100000000" {
		t.Fatalf("BUY level = %+v", buy)
	}
	if len(buy.Makers) != 1 || buy.Makers[0].OrderID != "a-external-sell" || buy.Makers[0].Remaining != "9" {
		t.Fatalf("BUY makers = %+v", buy.Makers)
	}

	sell := capture.result.Levels[1]
	if sell.BaseToken != baseToken || sell.QuoteToken != quoteToken || sell.TakerSide != pb.OrderSide_SELL || sell.Price != "110000000" {
		t.Fatalf("SELL level = %+v", sell)
	}
	if len(sell.Makers) != 1 || sell.Makers[0].OrderID != "z-external-buy" || sell.Makers[0].Remaining != "8" {
		t.Fatalf("SELL makers = %+v", sell.Makers)
	}
}

func TestProjectSpotBestLevelPagesPastInvalidMatcherCandidates(t *testing.T) {
	const (
		baseToken  = "BTC"
		quoteToken = "USDT"
	)
	sv := newMemStateView()
	for i := 0; i < proposalSpotProjectionScanBatch; i++ {
		                                                                        
		                                                            
		putProposalSpotMaker(t, sv, baseToken, quoteToken, fmt.Sprintf("dust-%03d", i), "dust", pb.OrderSide_SELL, big.NewInt(1), big.NewInt(1), big.NewInt(0))
	}
	putProposalSpotMaker(t, sv, baseToken, quoteToken, "valid", "alice", pb.OrderSide_SELL, big.NewInt(100_000_000), big.NewInt(7), big.NewInt(0))

	pair := proposalSpotPair{
		baseToken:  baseToken,
		quoteToken: quoteToken,
		pairKey:    utils.GeneratePairKey(baseToken, quoteToken),
	}
	level, err := projectSpotBestLevel(sv, pair, pb.OrderSide_BUY, "miner", 4)
	if err != nil {
		t.Fatalf("project best level: %v", err)
	}
	if level == nil || level.Price != "100000000" || len(level.Makers) != 1 || level.Makers[0].OrderID != "valid" {
		t.Fatalf("level = %+v", level)
	}
}

func TestProposalSpotCaptureIgnoresFailedAdd(t *testing.T) {
	sv := newMemStateView()
	putProposalSpotMaker(t, sv, "BTC", "USDT", "maker", "alice", pb.OrderSide_SELL, big.NewInt(100_000_000), big.NewInt(5), big.NewInt(0))
	block := &pb.Block{Body: []*pb.AnyTx{{Content: &pb.AnyTx_OrderTx{OrderTx: &pb.OrderTx{
		Base: &pb.BaseMessage{TxId: "failed-add"}, Op: pb.OrderOp_ADD, BaseToken: "BTC", QuoteToken: "USDT",
	}}}}}
	capture := &proposalSpotCapture{maxOrders: 4}
	if err := capture.capturePostTransactions(sv, block, []*Receipt{{TxID: "failed-add", Status: "FAILED"}}); err != nil {
		t.Fatalf("capture failed add: %v", err)
	}
	if capture.result == nil || len(capture.result.Levels) != 0 {
		t.Fatalf("failed ADD unexpectedly touched projection: %+v", capture.result)
	}
}

func TestProposalSpotCaptureRecordsPerTransactionExecutionTruth(t *testing.T) {
	const (
		baseToken  = "A"
		quoteToken = "Q"
		orderID    = "public-sell"
		owner      = "alice"
	)
	sv := newMemStateView()
	putProposalSpotMaker(t, sv, baseToken, quoteToken, orderID, owner, pb.OrderSide_SELL,
		big.NewInt(100_000_000), big.NewInt(10), big.NewInt(4))
	putProposalSpotMaker(t, sv, baseToken, quoteToken, "public-buy", "bob", pb.OrderSide_BUY,
		big.NewInt(110_000_000), big.NewInt(7), big.NewInt(2))
	tx := &pb.AnyTx{Content: &pb.AnyTx_OrderTx{OrderTx: &pb.OrderTx{
		Base:       &pb.BaseMessage{TxId: orderID, FromAddress: owner},
		Op:         pb.OrderOp_ADD,
		BaseToken:  baseToken,
		QuoteToken: quoteToken,
		Side:       pb.OrderSide_SELL,
		Price:      "100000000",
		Amount:     "10",
	}}}
	receipt := &Receipt{TxID: orderID, Status: "SUCCEED", Events: []Event{{
		Kind: EventOrderMatched,
		Payload: map[string]string{
			"maker_order_id": "older-buy",
			"quantity":       "4",
			"price":          "90000000",
		},
	}}}
	capture := &proposalSpotCapture{maxOrders: 8, takerAddress: "arbitrage-account"}
	if err := capture.capturePostTransaction(sv, nil, 2, tx, receipt); err != nil {
		t.Fatalf("capture post transaction: %v", err)
	}
	block := &pb.Block{Body: []*pb.AnyTx{tx}}
	if err := capture.capturePostTransactions(sv, block, []*Receipt{receipt}); err != nil {
		t.Fatalf("capture final projection: %v", err)
	}
	if capture.result == nil || len(capture.result.Executions) != 1 {
		t.Fatalf("execution trace=%+v", capture.result)
	}
	execution := capture.result.Executions[0]
	if execution.PublicPosition != 3 || execution.OrderID != orderID || execution.Owner != owner ||
		execution.Price != "100000000" || execution.Amount != "10" || execution.Remaining != "6" {
		t.Fatalf("execution=%+v", execution)
	}
	if len(execution.Matches) != 1 || execution.Matches[0].MakerOrderID != "older-buy" ||
		execution.Matches[0].Quantity != "4" || execution.Matches[0].Price != "90000000" {
		t.Fatalf("matches=%+v", execution.Matches)
	}
	if len(execution.Levels) != 2 {
		t.Fatalf("post levels=%+v, want both taker directions", execution.Levels)
	}
	if execution.Levels[0].TakerSide != pb.OrderSide_BUY || execution.Levels[0].Makers[0].OrderID != orderID ||
		execution.Levels[0].Makers[0].Remaining != "6" {
		t.Fatalf("post BUY level=%+v", execution.Levels[0])
	}
	if execution.Levels[1].TakerSide != pb.OrderSide_SELL || execution.Levels[1].Makers[0].OrderID != "public-buy" ||
		execution.Levels[1].Makers[0].Remaining != "5" {
		t.Fatalf("post SELL level=%+v", execution.Levels[1])
	}
}

func TestCollectSpotAddPairsPreservesSemanticBaseQuoteOrder(t *testing.T) {
	block := &pb.Block{Body: []*pb.AnyTx{
		{Content: &pb.AnyTx_OrderTx{OrderTx: &pb.OrderTx{
			Base: &pb.BaseMessage{TxId: "z-a"}, Op: pb.OrderOp_ADD, BaseToken: "Z", QuoteToken: "A",
		}}},
		{Content: &pb.AnyTx_OrderTx{OrderTx: &pb.OrderTx{
			Base: &pb.BaseMessage{TxId: "a-z"}, Op: pb.OrderOp_ADD, BaseToken: "A", QuoteToken: "Z",
		}}},
		{Content: &pb.AnyTx_OrderTx{OrderTx: &pb.OrderTx{
			Base: &pb.BaseMessage{TxId: "duplicate"}, Op: pb.OrderOp_ADD, BaseToken: "Z", QuoteToken: "A",
		}}},
		{Content: &pb.AnyTx_OrderTx{OrderTx: &pb.OrderTx{
			Base: &pb.BaseMessage{TxId: "remove"}, Op: pb.OrderOp_REMOVE, BaseToken: "ignored", QuoteToken: "ignored",
		}}},
	}}

	pairs := collectSpotAddPairs(block, func(tx *pb.AnyTx) bool {
		return tx.GetTxId() != "duplicate"
	})
	if len(pairs) != 2 {
		t.Fatalf("structured pairs = %+v, want two unique ADD pairs", pairs)
	}
	if pairs[0].baseToken != "A" || pairs[0].quoteToken != "Z" || pairs[0].pairKey != utils.GeneratePairKey("A", "Z") {
		t.Fatalf("first semantic pair = %+v", pairs[0])
	}
	if pairs[1].baseToken != "Z" || pairs[1].quoteToken != "A" || pairs[1].pairKey != utils.GeneratePairKey("Z", "A") {
		t.Fatalf("second semantic pair = %+v", pairs[1])
	}
}
