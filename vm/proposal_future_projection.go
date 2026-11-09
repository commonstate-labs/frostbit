package vm

import (
	"dex/future"
	"dex/keys"
	"dex/pb"
	"fmt"
	"math/big"
	"sort"
	"strings"
)

                                                                            
                                         
type ProposalFutureMakerProjection struct {
	OrderID   string
	Owner     string
	Remaining string
	Leverage  uint64
}

                                                                             
                               
type ProposalFutureLevelProjection struct {
	Pair       string
	QuoteToken string
	TakerSide  pb.OrderSide
	Price      string
	Makers     []ProposalFutureMakerProjection
}

type ProposalFutureMatchProjection struct {
	MakerOrderID string
	Quantity     string
	Price        string
}

                                                                              
                                                                                
                                                                     
type ProposalFutureExecutionProjection struct {
	PublicPosition int
	OrderID        string
	Owner          string
	Pair           string
	QuoteToken     string
	Side           pb.OrderSide
	Price          string
	Quantity       string
	Remaining      string
	Leverage       uint64
	Matches        []ProposalFutureMatchProjection
	Levels         []ProposalFutureLevelProjection
	                                                                           
	                                                                            
	                                                                     
	TakerAvailable map[string]string
}

type ProposalFutureProjection struct {
	Levels         []ProposalFutureLevelProjection
	Executions     []ProposalFutureExecutionProjection
	TakerAvailable map[string]string
}

type proposalFutureCapture struct {
	takerAddress string
	maxOrders    int
	result       *ProposalFutureProjection
}

func (*proposalFutureCapture) stopAfterTransactions() bool { return true }

func (c *proposalFutureCapture) ensureResult() *ProposalFutureProjection {
	if c.result == nil {
		c.result = &ProposalFutureProjection{TakerAvailable: make(map[string]string)}
	} else if c.result.TakerAvailable == nil {
		c.result.TakerAvailable = make(map[string]string)
	}
	return c.result
}

func (c *proposalFutureCapture) capturePostTransaction(sv StateView, _ *pb.Block, index int, tx *pb.AnyTx, receipt *Receipt) error {
	if c == nil || c.maxOrders <= 0 || sv == nil || tx == nil || receipt == nil || receipt.Status != "SUCCEED" {
		return nil
	}
	order := tx.GetFutureOrderTx()
	base := tx.GetBase()
	if order == nil || base == nil || order.IsClose || order.IsMarket || base.TxId == "" ||
		receipt.TxID != base.TxId || strings.TrimSpace(base.FromAddress) == "" || strings.TrimSpace(order.Pair) == "" {
		return nil
	}
	var side pb.OrderSide
	switch future.Side(order.Side) {
	case future.Long:
		side = pb.OrderSide_BUY
	case future.Short:
		side = pb.OrderSide_SELL
	default:
		return nil
	}
	quantity, ok := new(big.Int).SetString(order.Quantity, 10)
	if !ok || quantity.Sign() <= 0 {
		return fmt.Errorf("successful future order %s has invalid quantity %q", base.TxId, order.Quantity)
	}
	price, ok := new(big.Int).SetString(order.Price, 10)
	if !ok || price.Sign() <= 0 {
		return fmt.Errorf("successful future order %s has invalid price %q", base.TxId, order.Price)
	}

	remaining := new(big.Int)
	stateData, exists, err := sv.Get(keys.KeyFutureOrderState(base.TxId))
	if err != nil {
		return fmt.Errorf("read successful future order %s state: %w", base.TxId, err)
	}
	if exists && len(stateData) > 0 {
		var state pb.FutureOrderState
		if err := unmarshalProtoCompat(stateData, &state); err != nil {
			return fmt.Errorf("decode successful future order %s state: %w", base.TxId, err)
		}
		owner := state.Owner
		if owner == "" {
			owner = state.OwnerAddress
		}
		if state.OrderId != base.TxId || owner != base.FromAddress || state.Pair != order.Pair ||
			state.Side != order.Side || state.Price != order.Price || state.Amount != order.Quantity ||
			state.Leverage != order.Leverage || state.IsClose ||
			state.Status != pb.FutureOrderStatus_FUTURE_ORDER_OPEN {
			return fmt.Errorf("successful future order %s has inconsistent open state", base.TxId)
		}
		filled := new(big.Int)
		if state.FilledBase != "" {
			if _, parsed := filled.SetString(state.FilledBase, 10); !parsed {
				return fmt.Errorf("successful future order %s has invalid filled_base %q", base.TxId, state.FilledBase)
			}
		}
		if filled.Sign() < 0 || filled.Cmp(quantity) >= 0 {
			return fmt.Errorf("successful future order %s has invalid filled_base %q", base.TxId, state.FilledBase)
		}
		remaining.Sub(quantity, filled)
	}

	quoteToken, quoteErr := futureMarketQuoteToken(sv, order.Pair)
	if quoteErr != nil {
		return fmt.Errorf("capture successful future order %s market identity: %w", base.TxId, quoteErr)
	}
	takerAvailable, err := proposalFutureAvailableBalances(sv, c.takerAddress, quoteToken, FeeToken)
	if err != nil {
		return fmt.Errorf("capture successful future order %s taker balance: %w", base.TxId, err)
	}
	step := ProposalFutureExecutionProjection{
		PublicPosition: index + 1,
		OrderID:        base.TxId,
		Owner:          base.FromAddress,
		Pair:           order.Pair,
		QuoteToken:     quoteToken,
		Side:           side,
		Price:          price.String(),
		Quantity:       quantity.String(),
		Remaining:      remaining.String(),
		Leverage:       order.Leverage,
		TakerAvailable: takerAvailable,
	}
	for _, event := range receipt.Events {
		if event.Kind != EventOrderMatched {
			continue
		}
		step.Matches = append(step.Matches, ProposalFutureMatchProjection{
			MakerOrderID: event.Payload["maker_order_id"],
			Quantity:     event.Payload["quantity"],
			Price:        event.Payload["price"],
		})
	}
	for _, takerSide := range []pb.OrderSide{pb.OrderSide_BUY, pb.OrderSide_SELL} {
		level, err := projectFutureBestLevel(sv, order.Pair, quoteToken, takerSide, "", 1)
		if err != nil {
			return fmt.Errorf("capture successful future order %s post level: %w", base.TxId, err)
		}
		if level != nil {
			step.Levels = append(step.Levels, *level)
		}
	}
	c.ensureResult().Executions = append(c.result.Executions, step)
	return nil
}

func (c *proposalFutureCapture) capturePostTransactions(sv StateView, block *pb.Block, receipts []*Receipt) error {
	if c == nil {
		return nil
	}
	result := c.ensureResult()
	if c.maxOrders <= 0 || block == nil {
		return nil
	}
	succeeded := make(map[string]struct{}, len(receipts))
	for _, receipt := range receipts {
		if receipt != nil && receipt.Status == "SUCCEED" && receipt.TxID != "" {
			succeeded[receipt.TxID] = struct{}{}
		}
	}
	pairSet := make(map[string]struct{})
	for _, tx := range block.Body {
		if tx == nil {
			continue
		}
		if _, ok := succeeded[tx.GetTxId()]; !ok {
			continue
		}
		order := tx.GetFutureOrderTx()
		if order == nil || order.IsClose || order.IsMarket || strings.TrimSpace(order.Pair) == "" {
			continue
		}
		pairSet[order.Pair] = struct{}{}
	}
	pairs := make([]string, 0, len(pairSet))
	for pair := range pairSet {
		pairs = append(pairs, pair)
	}
	sort.Strings(pairs)
	for _, pair := range pairs {
		quoteToken, quoteErr := futureMarketQuoteToken(sv, pair)
		if quoteErr != nil {
			return quoteErr
		}
		for _, takerSide := range []pb.OrderSide{pb.OrderSide_BUY, pb.OrderSide_SELL} {
			level, err := projectFutureBestLevel(sv, pair, quoteToken, takerSide, c.takerAddress, c.maxOrders)
			if err != nil {
				return err
			}
			if level != nil {
				result.Levels = append(result.Levels, *level)
			}
		}
		available, err := proposalFutureAvailableBalances(sv, c.takerAddress, quoteToken)
		if err != nil {
			return err
		}
		for token, amount := range available {
			result.TakerAvailable[token] = amount
		}
	}
	available, err := proposalFutureAvailableBalances(sv, c.takerAddress, FeeToken)
	if err != nil {
		return err
	}
	for token, amount := range available {
		result.TakerAvailable[token] = amount
	}
	return nil
}

func proposalFutureAvailableBalances(sv StateView, address string, tokens ...string) (map[string]string, error) {
	if sv == nil || strings.TrimSpace(address) == "" {
		return nil, nil
	}
	unique := make(map[string]struct{}, len(tokens))
	ordered := make([]string, 0, len(tokens))
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if _, exists := unique[token]; exists {
			continue
		}
		unique[token] = struct{}{}
		ordered = append(ordered, token)
	}
	sort.Strings(ordered)
	availableByToken := make(map[string]string, len(ordered))
	for _, token := range ordered {
		balance, err := getBalanceStrict(sv, address, token)
		if err != nil {
			return nil, fmt.Errorf("read proposal future taker balance %s/%s: %w", address, token, err)
		}
		rawAvailable := balance.Balance
		if rawAvailable == "" {
			rawAvailable = "0"
		}
		available, ok := new(big.Int).SetString(rawAvailable, 10)
		if !ok || available.Sign() < 0 {
			return nil, fmt.Errorf("read proposal future taker balance %s/%s: invalid available amount %q", address, token, balance.Balance)
		}
		availableByToken[token] = available.String()
	}
	return availableByToken, nil
}

type proposalFutureCandidate struct {
	indexKey string
	orderID  string
	priceKey string
	maker    *makerOrderSnapshot
}

func scanProposalFutureCandidates(sv StateView, prefix, pair, startAfter string, limit int, reverse bool) ([]proposalFutureCandidate, bool, error) {
	if scanner, ok := sv.(futureOrderbookSnapshotScanner); ok {
		entries, used, err := scanner.ScanFutureOrderbookSnapshots(prefix, startAfter, limit, reverse)
		if err != nil || used {
			out := make([]proposalFutureCandidate, 0, len(entries))
			for _, entry := range entries {
				maker, parseErr := entry.snapshot.makerSnapshot(pair, entry.priceKey)
				if parseErr != nil {
					maker = nil
				}
				out = append(out, proposalFutureCandidate{
					indexKey: entry.indexKey(prefix), orderID: entry.orderID, priceKey: entry.priceKey, maker: maker,
				})
			}
			return out, used, err
		}
	}
	pairs, err := scanStateViewOrderedAfter(sv, prefix, startAfter, limit, reverse)
	if err != nil {
		return nil, false, err
	}
	out := make([]proposalFutureCandidate, 0, len(pairs))
	for _, pairKV := range pairs {
		orderID := extractOrderIDFromKey(pairKV.Key)
		priceKey := extractPriceKeyFromKey(pairKV.Key)
		maker, parseErr := parseMakerOrderSnapshot(pairKV.Value, MarketFuture)
		if parseErr != nil {
			maker = nil
		}
		out = append(out, proposalFutureCandidate{indexKey: pairKV.Key, orderID: orderID, priceKey: priceKey, maker: maker})
	}
	return out, true, nil
}

func projectFutureBestLevel(sv StateView, pair, quoteToken string, takerSide pb.OrderSide, takerAddress string, maxOrders int) (*ProposalFutureLevelProjection, error) {
	if sv == nil || maxOrders <= 0 || pair == "" || (takerSide != pb.OrderSide_BUY && takerSide != pb.OrderSide_SELL) {
		return nil, nil
	}
	counterSide := pb.OrderSide_SELL
	reverse := false
	if takerSide == pb.OrderSide_SELL {
		counterSide = pb.OrderSide_BUY
		reverse = true
	}
	prefix := keys.KeyFutureOrderPriceIndexPrefix(pair, counterSide, false)
	level := &ProposalFutureLevelProjection{
		Pair: pair, QuoteToken: quoteToken, TakerSide: takerSide,
		Makers: make([]ProposalFutureMakerProjection, 0, maxOrders),
	}
	startAfter := ""
	for len(level.Makers) < maxOrders {
		candidates, used, err := scanProposalFutureCandidates(sv, prefix, pair, startAfter, proposalSpotProjectionScanBatch, reverse)
		if err != nil {
			return nil, fmt.Errorf("scan future proposal book %s side=%s: %w", pair, takerSide.String(), err)
		}
		if !used || len(candidates) == 0 {
			break
		}
		for _, candidate := range candidates {
			maker := candidate.maker
			if !validProposalFutureMaker(maker, candidate.orderID, pair, counterSide) {
				continue
			}
			                                                                      
			                                                                      
			                                                         
			quote, quoteErr := QuoteFromBasePrice(maker.remain, maker.price)
			if quoteErr != nil || quote.Sign() == 0 {
				continue
			}
			if level.Price == "" {
				level.Price = maker.price.String()
			} else if maker.price.String() != level.Price {
				return finishProposalFutureLevel(level), nil
			}
			if takerAddress != "" && maker.owner == takerAddress {
				return finishProposalFutureLevel(level), nil
			}
			level.Makers = append(level.Makers, ProposalFutureMakerProjection{
				OrderID: candidate.orderID, Owner: maker.owner, Remaining: maker.remain.String(), Leverage: maker.leverage,
			})
			if len(level.Makers) >= maxOrders {
				return finishProposalFutureLevel(level), nil
			}
		}
		last := candidates[len(candidates)-1].indexKey
		if len(candidates) < proposalSpotProjectionScanBatch || last == "" || last == startAfter {
			break
		}
		startAfter = last
	}
	return finishProposalFutureLevel(level), nil
}

func validProposalFutureMaker(maker *makerOrderSnapshot, orderID, pair string, counterSide pb.OrderSide) bool {
	if maker == nil || maker.future == nil || maker.remain == nil || maker.remain.Sign() <= 0 ||
		maker.price == nil || maker.price.Sign() <= 0 || maker.owner == "" || maker.leverage == 0 {
		return false
	}
	state := maker.future
	owner := state.Owner
	if owner == "" {
		owner = state.OwnerAddress
	}
	makerSide := future.Side(state.Side)
	if makerSide != future.Long && makerSide != future.Short {
		return false
	}
	return state.OrderId == orderID && owner == maker.owner && state.Pair == pair &&
		futureOrderBookSide(state) == counterSide && state.Status == pb.FutureOrderStatus_FUTURE_ORDER_OPEN && !state.IsClose
}

func finishProposalFutureLevel(level *ProposalFutureLevelProjection) *ProposalFutureLevelProjection {
	if level == nil || level.Price == "" || len(level.Makers) == 0 {
		return nil
	}
	return level
}

                                                                               
                                                                       
type ProposalMarketProjection struct {
	Spot   *ProposalSpotProjection
	Future *ProposalFutureProjection
}

type proposalMarketCapture struct {
	spot   *proposalSpotCapture
	future *proposalFutureCapture
}

func (*proposalMarketCapture) stopAfterTransactions() bool { return true }

func (c *proposalMarketCapture) capturePostTransaction(sv StateView, block *pb.Block, index int, tx *pb.AnyTx, receipt *Receipt) error {
	if c == nil {
		return nil
	}
	if c.spot != nil {
		if err := c.spot.capturePostTransaction(sv, block, index, tx, receipt); err != nil {
			return err
		}
	}
	if c.future != nil {
		return c.future.capturePostTransaction(sv, block, index, tx, receipt)
	}
	return nil
}

func (c *proposalMarketCapture) capturePostTransactions(sv StateView, block *pb.Block, receipts []*Receipt) error {
	if c == nil {
		return nil
	}
	if c.spot != nil {
		if err := c.spot.capturePostTransactions(sv, block, receipts); err != nil {
			return err
		}
	}
	if c.future != nil {
		return c.future.capturePostTransactions(sv, block, receipts)
	}
	return nil
}
