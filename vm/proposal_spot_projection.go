package vm

import (
	"dex/keys"
	"dex/pb"
	"dex/utils"
	"fmt"
	"math/big"
)

const proposalSpotProjectionScanBatch = 200

                                                                              
                                
type ProposalSpotMakerProjection struct {
	OrderID   string
	Owner     string
	Remaining string
}

                                                                             
                                                       
type ProposalSpotLevelProjection struct {
	BaseToken  string
	QuoteToken string
	TakerSide  pb.OrderSide
	Price      string
	Makers     []ProposalSpotMakerProjection
}

                                                                             
                                                          
type ProposalSpotMatchProjection struct {
	MakerOrderID string
	Quantity     string
	Price        string
}

                                                                            
                                                                               
                                                                             
type ProposalSpotExecutionProjection struct {
	PublicPosition int
	OrderID        string
	Owner          string
	BaseToken      string
	QuoteToken     string
	Side           pb.OrderSide
	Price          string
	Amount         string
	Remaining      string
	Matches        []ProposalSpotMatchProjection
	Levels         []ProposalSpotLevelProjection
}

                                                                           
                                           
type ProposalSpotProjection struct {
	Levels     []ProposalSpotLevelProjection
	Executions []ProposalSpotExecutionProjection
}

type specExecutionCapture interface {
	capturePostTransactions(StateView, *pb.Block, []*Receipt) error
}

type specExecutionStepCapture interface {
	capturePostTransaction(StateView, *pb.Block, int, *pb.AnyTx, *Receipt) error
}

type proposalSpotPair = spotAddPair

type proposalSpotCapture struct {
	takerAddress string
	maxOrders    int
	result       *ProposalSpotProjection
}

func (*proposalSpotCapture) stopAfterTransactions() bool { return true }

func (c *proposalSpotCapture) capturePostTransaction(sv StateView, _ *pb.Block, index int, tx *pb.AnyTx, receipt *Receipt) error {
	if c == nil || c.maxOrders <= 0 || sv == nil || tx == nil || receipt == nil || receipt.Status != "SUCCEED" {
		return nil
	}
	order := tx.GetOrderTx()
	base := tx.GetBase()
	if order == nil || base == nil || order.Op != pb.OrderOp_ADD || order.IsMarket ||
		base.TxId == "" || receipt.TxID != base.TxId || base.FromAddress == "" ||
		order.BaseToken == "" || order.QuoteToken == "" || order.BaseToken == order.QuoteToken ||
		(order.Side != pb.OrderSide_BUY && order.Side != pb.OrderSide_SELL) {
		return nil
	}
	amount, ok := new(big.Int).SetString(order.Amount, 10)
	if !ok || amount.Sign() <= 0 {
		return fmt.Errorf("successful spot ADD %s has invalid amount %q", base.TxId, order.Amount)
	}
	price, ok := new(big.Int).SetString(order.Price, 10)
	if !ok || price.Sign() <= 0 {
		return fmt.Errorf("successful spot ADD %s has invalid price %q", base.TxId, order.Price)
	}

	remaining := new(big.Int)
	stateData, exists, err := sv.Get(keys.KeyOrderState(base.TxId))
	if err != nil {
		return fmt.Errorf("read successful spot ADD %s state: %w", base.TxId, err)
	}
	if exists && len(stateData) > 0 {
		var state pb.OrderState
		if err := unmarshalProtoCompat(stateData, &state); err != nil {
			return fmt.Errorf("decode successful spot ADD %s state: %w", base.TxId, err)
		}
		if state.OrderId != base.TxId || state.Owner != base.FromAddress ||
			state.BaseToken != order.BaseToken || state.QuoteToken != order.QuoteToken ||
			state.Side != order.Side || state.Price != order.Price || state.Amount != order.Amount ||
			state.Status != pb.OrderStateStatus_ORDER_OPEN || state.IsFilled {
			return fmt.Errorf("successful spot ADD %s has inconsistent open state", base.TxId)
		}
		filled, parsed := new(big.Int).SetString(state.FilledBase, 10)
		if !parsed || filled.Sign() < 0 || filled.Cmp(amount) >= 0 {
			return fmt.Errorf("successful spot ADD %s has invalid filled_base %q", base.TxId, state.FilledBase)
		}
		remaining.Sub(amount, filled)
	}

	step := ProposalSpotExecutionProjection{
		PublicPosition: index + 1,
		OrderID:        base.TxId,
		Owner:          base.FromAddress,
		BaseToken:      order.BaseToken,
		QuoteToken:     order.QuoteToken,
		Side:           order.Side,
		Price:          price.String(),
		Amount:         amount.String(),
		Remaining:      remaining.String(),
	}
	for _, event := range receipt.Events {
		if event.Kind != EventOrderMatched {
			continue
		}
		step.Matches = append(step.Matches, ProposalSpotMatchProjection{
			MakerOrderID: event.Payload["maker_order_id"],
			Quantity:     event.Payload["quantity"],
			Price:        event.Payload["price"],
		})
	}
	pair := proposalSpotPair{
		baseToken:  order.BaseToken,
		quoteToken: order.QuoteToken,
		pairKey:    utils.GeneratePairKey(order.BaseToken, order.QuoteToken),
	}
	for _, takerSide := range []pb.OrderSide{pb.OrderSide_BUY, pb.OrderSide_SELL} {
		level, err := projectSpotBestLevel(sv, pair, takerSide, "", 1)
		if err != nil {
			return fmt.Errorf("capture successful spot ADD %s post level: %w", base.TxId, err)
		}
		if level != nil {
			step.Levels = append(step.Levels, *level)
		}
	}
	if c.result == nil {
		c.result = &ProposalSpotProjection{}
	}
	c.result.Executions = append(c.result.Executions, step)
	return nil
}

func (c *proposalSpotCapture) capturePostTransactions(sv StateView, block *pb.Block, receipts []*Receipt) error {
	if c == nil {
		return nil
	}
	if c.result == nil {
		c.result = &ProposalSpotProjection{}
	}
	if c.maxOrders <= 0 || block == nil {
		return nil
	}

	succeeded := make(map[string]struct{}, len(receipts))
	for _, receipt := range receipts {
		if receipt != nil && receipt.Status == "SUCCEED" && receipt.TxID != "" {
			succeeded[receipt.TxID] = struct{}{}
		}
	}

	                                                                        
	                                                     
	pairs := collectSpotAddPairs(block, func(tx *pb.AnyTx) bool {
		_, ok := succeeded[tx.GetTxId()]
		return ok
	})

	for _, pair := range pairs {
		for _, takerSide := range []pb.OrderSide{pb.OrderSide_BUY, pb.OrderSide_SELL} {
			level, err := projectSpotBestLevel(sv, pair, takerSide, c.takerAddress, c.maxOrders)
			if err != nil {
				return err
			}
			if level != nil {
				c.result.Levels = append(c.result.Levels, *level)
			}
		}
	}
	return nil
}

func projectSpotBestLevel(sv StateView, pair proposalSpotPair, takerSide pb.OrderSide, takerAddress string, maxOrders int) (*ProposalSpotLevelProjection, error) {
	if sv == nil || maxOrders <= 0 {
		return nil, nil
	}

	counterSide := pb.OrderSide_SELL
	reverse := false
	if takerSide == pb.OrderSide_SELL {
		counterSide = pb.OrderSide_BUY
		reverse = true
	}
	prefix := keys.KeyOrderPriceIndexPrefix(pair.pairKey, counterSide, false)
	level := &ProposalSpotLevelProjection{
		BaseToken:  pair.baseToken,
		QuoteToken: pair.quoteToken,
		TakerSide:  takerSide,
		Makers:     make([]ProposalSpotMakerProjection, 0, maxOrders),
	}

	startAfter := ""
	for len(level.Makers) < maxOrders {
		indexKeys, err := scanStateViewOrderedKeysAfter(sv, prefix, startAfter, proposalSpotProjectionScanBatch, reverse)
		if err != nil {
			return nil, fmt.Errorf("scan spot proposal book %s side=%s: %w", pair.pairKey, takerSide.String(), err)
		}
		if len(indexKeys) == 0 {
			break
		}

		stateKeys := make([]string, 0, len(indexKeys))
		candidates := make([]makerIndexCandidate, 0, len(indexKeys))
		seenStateKeys := make(map[string]struct{}, len(indexKeys))
		for _, indexKey := range indexKeys {
			orderID := extractOrderIDFromKey(indexKey)
			if orderID == "" || extractPriceKeyFromKey(indexKey) == "" {
				continue
			}
			stateKey := keys.KeyOrderState(orderID)
			candidates = append(candidates, makerIndexCandidate{
				indexKey: indexKey,
				orderID:  orderID,
				stateKey: stateKey,
			})
			if _, ok := seenStateKeys[stateKey]; !ok {
				seenStateKeys[stateKey] = struct{}{}
				stateKeys = append(stateKeys, stateKey)
			}
		}

		states, err := getStateMany(sv, stateKeys)
		if err != nil {
			return nil, fmt.Errorf("read spot proposal makers %s side=%s: %w", pair.pairKey, takerSide.String(), err)
		}
		for _, candidate := range candidates {
			maker, parseErr := parseMakerOrderSnapshot(states[candidate.stateKey], MarketSpot)
			if parseErr != nil || !validProposalSpotMaker(maker, candidate.orderID, pair, counterSide) {
				                                                                    
				continue
			}
			quote, quoteErr := QuoteFromBasePrice(maker.remain, maker.price)
			if quoteErr != nil || quote.Sign() == 0 {
				continue
			}

			if level.Price == "" {
				level.Price = maker.price.String()
			} else if maker.price.String() != level.Price {
				return finishProposalSpotLevel(level), nil
			}
			if takerAddress != "" && maker.owner == takerAddress {
				                                                                       
				                                                                    
				return finishProposalSpotLevel(level), nil
			}
			level.Makers = append(level.Makers, ProposalSpotMakerProjection{
				OrderID:   candidate.orderID,
				Owner:     maker.owner,
				Remaining: maker.remain.String(),
			})
			if len(level.Makers) >= maxOrders {
				return finishProposalSpotLevel(level), nil
			}
		}

		lastScanned := indexKeys[len(indexKeys)-1]
		if len(indexKeys) < proposalSpotProjectionScanBatch || lastScanned == "" || lastScanned == startAfter {
			break
		}
		startAfter = lastScanned
	}
	return finishProposalSpotLevel(level), nil
}

func validProposalSpotMaker(maker *makerOrderSnapshot, orderID string, pair proposalSpotPair, counterSide pb.OrderSide) bool {
	if maker == nil || maker.spot == nil || maker.remain == nil || maker.remain.Sign() <= 0 || maker.price == nil || maker.price.Sign() <= 0 || maker.owner == "" {
		return false
	}
	state := maker.spot
	return state.OrderId == orderID && !state.IsFilled && state.Status == pb.OrderStateStatus_ORDER_OPEN &&
		state.BaseToken == pair.baseToken && state.QuoteToken == pair.quoteToken && state.Side == counterSide
}

func finishProposalSpotLevel(level *ProposalSpotLevelProjection) *ProposalSpotLevelProjection {
	if level == nil || level.Price == "" || len(level.Makers) == 0 {
		return nil
	}
	return level
}
