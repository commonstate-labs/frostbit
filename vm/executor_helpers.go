package vm

import (
	"dex/keys"
	"dex/pb"
	"dex/utils"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
)

                                                              
func sortedBytesMapKeys(m map[string][]byte) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

type spotAddPair struct {
	baseToken  string
	quoteToken string
	pairKey    string
}

                                                                           
                                                                              
                                                                          
                                            
func collectSpotAddPairs(b *pb.Block, include func(*pb.AnyTx) bool) []spotAddPair {
	if b == nil {
		return nil
	}
	type pairIdentity struct {
		baseToken  string
		quoteToken string
	}
	pairSet := make(map[pairIdentity]spotAddPair)
	for _, anyTx := range b.Body {
		if anyTx == nil || (include != nil && !include(anyTx)) {
			continue
		}
		orderTx := anyTx.GetOrderTx()
		if orderTx == nil || orderTx.Op != pb.OrderOp_ADD || orderTx.BaseToken == "" || orderTx.QuoteToken == "" {
			continue
		}
		identity := pairIdentity{baseToken: orderTx.BaseToken, quoteToken: orderTx.QuoteToken}
		pairSet[identity] = spotAddPair{
			baseToken:  orderTx.BaseToken,
			quoteToken: orderTx.QuoteToken,
			pairKey:    utils.GeneratePairKey(orderTx.BaseToken, orderTx.QuoteToken),
		}
	}

	pairs := make([]spotAddPair, 0, len(pairSet))
	for _, pair := range pairSet {
		pairs = append(pairs, pair)
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].baseToken != pairs[j].baseToken {
			return pairs[i].baseToken < pairs[j].baseToken
		}
		return pairs[i].quoteToken < pairs[j].quoteToken
	})
	return pairs
}

                                                                            
                                                                          
                                                           
func collectPairsFromBlock(b *pb.Block) []string {
	structured := collectSpotAddPairs(b, nil)
	pairSet := make(map[string]struct{}, len(structured))
	for _, pair := range structured {
		pairSet[pair.pairKey] = struct{}{}
	}
	pairs := make([]string, 0, len(pairSet))
	for pair := range pairSet {
		pairs = append(pairs, pair)
	}
	sort.Strings(pairs)
	return pairs
}

                                                                            
                                                                               
                                                                           
type blockTradeVWAP struct {
	totalBase      *big.Int
	totalQuote     *big.Int
	originalValue  []byte
	originalExists bool
	wrotePrice     bool
}

func newBlockTradeVWAP(original []byte, exists bool) *blockTradeVWAP {
	return &blockTradeVWAP{
		totalBase:      new(big.Int),
		totalQuote:     new(big.Int),
		originalValue:  append([]byte(nil), original...),
		originalExists: exists,
	}
}

func (a *blockTradeVWAP) add(quantityRaw, priceRaw string) error {
	qty, err := ParseBalance(quantityRaw)
	if err != nil || qty.Sign() <= 0 {
		return fmt.Errorf("invalid matched quantity %q", quantityRaw)
	}
	price, err := ParseBalance(priceRaw)
	if err != nil || price.Sign() <= 0 {
		return fmt.Errorf("invalid matched price %q", priceRaw)
	}

	                                                                       
	                                                                           
	                                                                          
	                                                                         
	quote := new(big.Int).Mul(qty, price) // safe-math-ok: VWAP 采样按成交分子先乘后除，块内周转汇总不受单笔余额上限截断。
	quote.Quo(quote, PriceScale)
	a.totalBase.Add(a.totalBase, qty)
	a.totalQuote.Add(a.totalQuote, quote)
	return nil
}

func (a *blockTradeVWAP) price() (*big.Int, error) {
	if a == nil || a.totalBase == nil || a.totalBase.Sign() <= 0 || a.totalQuote == nil || a.totalQuote.Sign() <= 0 {
		return nil, nil
	}
	price := new(big.Int).Mul(a.totalQuote, PriceScale) // safe-math-ok: VWAP 分子保留缩放精度，商在返回前检查 MaxUint256。
	price.Quo(price, a.totalBase)
	if price.Sign() <= 0 {
		return nil, nil
	}
	if price.Cmp(MaxUint256) > 0 {
		return nil, ErrOverflow
	}
	return price, nil
}

                                                                           
                                                                           
                                                           
  
                                                                               
                                                                            
                                                                             
                                                                                
type blockPriceFeedCollector struct {
	spot   map[string]*blockTradeVWAP
	future map[string]*blockTradeVWAP
}

func newBlockPriceFeedCollector() *blockPriceFeedCollector {
	return &blockPriceFeedCollector{
		spot:   make(map[string]*blockTradeVWAP),
		future: make(map[string]*blockTradeVWAP),
	}
}

                                                                         
                                                                               
                                                                               
                                                                    
func (c *blockPriceFeedCollector) recordReceipt(sv StateView, tx *pb.AnyTx, rc *Receipt) error {
	if c == nil || tx == nil || rc == nil || rc.Status != "SUCCEED" {
		return nil
	}
	var accumulators map[string]*blockTradeVWAP
	var priceKey func(string) string
	switch {
	case tx.GetFutureOrderTx() != nil:
		accumulators = c.future
		priceKey = keys.KeyFutureLastPrice
	case tx.GetOrderTx() != nil:
		accumulators = c.spot
		priceKey = keys.KeySpotLastPrice
	default:
		return nil
	}

	touched := make(map[string]struct{})
	for _, ev := range rc.Events {
		if ev.Kind != EventOrderMatched {
			continue
		}
		pair := ev.Payload["pair"]
		taker := ev.Payload["taker"]
		maker := ev.Payload["maker"]
		if pair == "" || taker == "" || maker == "" {
			return fmt.Errorf("malformed matched event: pair=%q taker=%q maker=%q", pair, taker, maker)
		}
		if taker == maker {
			continue
		}
		acc := accumulators[pair]
		if acc == nil {
			original, exists, err := sv.Get(priceKey(pair))
			if err != nil {
				return fmt.Errorf("read previous price for %s: %w", pair, err)
			}
			acc = newBlockTradeVWAP(original, exists)
			accumulators[pair] = acc
		}
		if err := acc.add(ev.Payload["quantity"], ev.Payload["price"]); err != nil {
			return fmt.Errorf("collect block VWAP for %s: %w", pair, err)
		}
		touched[pair] = struct{}{}
	}

	pairs := make([]string, 0, len(touched))
	for pair := range touched {
		pairs = append(pairs, pair)
	}
	sort.Strings(pairs)
	for _, pair := range pairs {
		price, err := accumulators[pair].price()
		if err != nil {
			return fmt.Errorf("calculate block VWAP for %s: %w", pair, err)
		}
		if price != nil {
			sv.Set(priceKey(pair), price.Bytes())
			accumulators[pair].wrotePrice = true
			continue
		}
		                                                                     
		                                                                        
		                                                                        
		                                                                 
		acc := accumulators[pair]
		if acc.wrotePrice {
			if acc.originalExists {
				sv.Set(priceKey(pair), acc.originalValue)
			} else {
				sv.Del(priceKey(pair))
			}
			acc.wrotePrice = false
		}
	}
	return nil
}

func (c *blockPriceFeedCollector) changedPairs() (map[string]bool, map[string]bool) {
	spotChanged := make(map[string]bool, len(c.spot))
	futureChanged := make(map[string]bool, len(c.future))
	for pair, acc := range c.spot {
		if price, _ := acc.price(); price != nil {
			spotChanged[pair] = true
		}
	}
	for pair, acc := range c.future {
		if price, _ := acc.price(); price != nil {
			futureChanged[pair] = true
		}
	}
	return spotChanged, futureChanged
}

func shouldRefundRuntimeFee(tx *pb.AnyTx, rc *Receipt) bool {
	if tx == nil || rc == nil || rc.Status != "SUCCEED" {
		return false
	}
	switch tx.GetContent().(type) {
	case *pb.AnyTx_FrostWithdrawClaimTx, *pb.AnyTx_FrostWithdrawSignedTx,
		*pb.AnyTx_WitnessVoteTx, *pb.AnyTx_ArbitrationVoteTx:
		return true
	default:
		return false
	}
}

                                                                   
func getBaseMessage(tx *pb.AnyTx) *pb.BaseMessage {
	if tx == nil {
		return nil
	}
	return tx.GetBase()
}

                                                                                                                  
func balanceToJSON(bal *pb.TokenBalance) string {
	if bal == nil {
		return ""
	}
	type snap struct {
		Balance       string `json:"balance"`
		MinerLocked   string `json:"miner_locked,omitempty"`
		WitnessLocked string `json:"witness_locked,omitempty"`
		OrderFrozen   string `json:"order_frozen,omitempty"`
		LiquidLocked  string `json:"liquid_locked,omitempty"`
	}
	s := snap{
		Balance:       bal.Balance,
		MinerLocked:   bal.MinerLockedBalance,
		WitnessLocked: bal.WitnessLockedBalance,
		OrderFrozen:   bal.OrderFrozenBalance,
		LiquidLocked:  bal.LiquidLockedBalance,
	}
	data, _ := json.Marshal(s)
	return string(data)
}
