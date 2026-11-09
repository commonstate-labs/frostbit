package handlers

import (
	"dex/interfaces"
	"dex/keys"
	"dex/pb"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"
)

const (
	defaultOrderBookDebugEntries   = 200
	maxOrderBookDebugEntries       = 500
	maxOrderBookDebugPairBytes     = 128
	maxOrderBookDebugResponseBytes = 1 << 20
)

                                 
type OrderBookEntry struct {
	Price          string `json:"price"`
	Amount         string `json:"amount"`
	Total          string `json:"total"`
	PendingCount   int    `json:"pendingCount"`                           
	ConfirmedCount int    `json:"confirmedCount"`                         
}

                                    
type OrderBookResponse struct {
	Pair string           `json:"pair"`
	Bids []OrderBookEntry `json:"bids"`                               
	Asks []OrderBookEntry `json:"asks"`                               
}

                                                        
type OrderBookDebugEntry struct {
	OrderID     string `json:"order_id"`
	Price       string `json:"price"`
	Amount      string `json:"amount"`
	FilledBase  string `json:"filled_base"`
	FilledQuote string `json:"filled_quote"`
	Remain      string `json:"remain"`
	IsFilled    bool   `json:"is_filled"`
	Side        string `json:"side"`
	IndexKey    string `json:"index_key"`
}

                                                                  
func (hm *HandlerManager) HandleOrderBookDebug(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if hm.dbManager == nil {
		http.Error(w, "database not available", http.StatusServiceUnavailable)
		return
	}
	handleOrderBookDebugWithStore(w, r, hm.dbManager.ScanKVWithLimitOrdered, hm.dbManager.Get)
}

func handleOrderBookDebugWithStore(
	w http.ResponseWriter,
	r *http.Request,
	scan func(prefix string, limit int, reverse bool) ([]interfaces.KVPair, error),
	get func(key string) ([]byte, error),
) {
	pair := r.URL.Query().Get("pair")
	if pair == "" {
		http.Error(w, "missing pair parameter", http.StatusBadRequest)
		return
	}
	if len(pair) > maxOrderBookDebugPairBytes || strings.ContainsAny(pair, "|\x00\r\n\t") {
		http.Error(w, "invalid pair parameter", http.StatusBadRequest)
		return
	}

	limit := defaultOrderBookDebugEntries
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed <= 0 || parsed > maxOrderBookDebugEntries {
			http.Error(w, "limit must be between 1 and 500", http.StatusBadRequest)
			return
		}
		limit = parsed
	}
	if scan == nil || get == nil {
		http.Error(w, "database not available", http.StatusServiceUnavailable)
		return
	}

	                                                                            
	                                                                        
	buyLimit := (limit + 1) / 2
	sellLimit := limit - buyLimit
	type scanSpec struct {
		side    pb.OrderSide
		limit   int
		reverse bool
	}
	specs := []scanSpec{
		{side: pb.OrderSide_BUY, limit: buyLimit, reverse: true},
		{side: pb.OrderSide_SELL, limit: sellLimit, reverse: false},
	}
	indexRows := make([]interfaces.KVPair, 0, limit)
	truncated := false
	for _, spec := range specs {
		if spec.limit == 0 {
			continue
		}
		prefix := keys.KeyOrderPriceIndexPrefix(pair, spec.side, false)
		rows, err := scan(prefix, spec.limit+1, spec.reverse)
		if err != nil {
			http.Error(w, "failed to scan orders: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if len(rows) > spec.limit {
			rows = rows[:spec.limit]
			truncated = true
		}
		indexRows = append(indexRows, rows...)
	}

	debugEntries := make([]OrderBookDebugEntry, 0, len(indexRows))

	for _, indexRow := range indexRows {
		indexKey := indexRow.Key
		orderID := extractOrderIDFromIndexKey(indexKey)
		if orderID == "" {
			continue
		}

		                                             
		orderStateKey := keys.KeyOrderState(orderID)
		orderStateBytes, err := get(orderStateKey)
		if err == nil && orderStateBytes != nil {
			var orderState pb.OrderState
			if err := proto.Unmarshal(orderStateBytes, &orderState); err != nil {
				continue
			}

			amount, _ := decimal.NewFromString(orderState.Amount)
			filledBase, _ := decimal.NewFromString(orderState.FilledBase)
			remain := amount.Sub(filledBase)

			side := "buy"
			if orderState.Side == pb.OrderSide_SELL {
				side = "sell"
			}

			debugEntries = append(debugEntries, OrderBookDebugEntry{
				OrderID:     orderID,
				Price:       orderState.Price,
				Amount:      orderState.Amount,
				FilledBase:  orderState.FilledBase,
				FilledQuote: orderState.FilledQuote,
				Remain:      remain.String(),
				IsFilled:    orderState.IsFilled,
				Side:        side,
				IndexKey:    indexKey,
			})
			continue
		}

		debugEntries = append(debugEntries, OrderBookDebugEntry{
			OrderID:  orderID,
			IndexKey: indexKey,
			Side:     "ERROR: orderstate not found",
		})
	}

	                  
	sort.Slice(debugEntries, func(i, j int) bool {
		pi, _ := decimal.NewFromString(debugEntries[i].Price)
		pj, _ := decimal.NewFromString(debugEntries[j].Price)
		return pi.GreaterThan(pj)
	})

	data, err := json.Marshal(map[string]interface{}{
		"pair":        pair,
		"total_count": len(debugEntries),
		"index_count": len(indexRows),
		"limit":       limit,
		"truncated":   truncated,
		"orders":      debugEntries,
	})
	if err != nil {
		http.Error(w, "failed to marshal response", http.StatusInternalServerError)
		return
	}
	if len(data) > maxOrderBookDebugResponseBytes {
		http.Error(w, "debug result too large; lower limit", http.StatusRequestEntityTooLarge)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

                                              
func (hm *HandlerManager) HandleOrderBook(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("HandleOrderBook")

	pair := r.URL.Query().Get("pair")
	if pair == "" {
		http.Error(w, "missing pair parameter", http.StatusBadRequest)
		return
	}

	                                          
	                                                                
	bidPrices := make(map[string]*priceLevelDataForAgg)                       
	askPrices := make(map[string]*priceLevelDataForAgg)

	                                                                    
	                        
	buyPrefix := keys.KeyOrderPriceIndexPrefix(pair, pb.OrderSide_BUY, false)
	buyOrders, err := hm.dbManager.ScanKVWithLimitReverse(buyPrefix, 500)
	if err != nil {
		                                                                                     
		                                                                   
		                                                                                           
		         
	} else {
		for indexKey := range buyOrders {
			hm.processOrderForAgg(indexKey, bidPrices, askPrices)
		}
	}

	                         
	sellPrefix := keys.KeyOrderPriceIndexPrefix(pair, pb.OrderSide_SELL, false)
	sellOrders, err := hm.dbManager.ScanKVWithLimit(sellPrefix, 500)
	if err != nil {
		                                                                                     
		                                                                    
		                                                                                            
		         
	} else {
		for indexKey := range sellOrders {
			hm.processOrderForAgg(indexKey, bidPrices, askPrices)
		}
	}

	                        
	bids := aggregatePriceToEntriesWithStatus(bidPrices, true)           
	asks := aggregatePriceToEntriesWithStatus(askPrices, false)          

	resp := OrderBookResponse{
		Pair: pair,
		Bids: bids,
		Asks: asks,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

                                                                                     
func (hm *HandlerManager) processOrderForAgg(indexKey string, bidPrices, askPrices map[string]*priceLevelDataForAgg) {
	orderID := extractOrderIDFromIndexKey(indexKey)
	if orderID == "" {
		return
	}

	                                             
	orderStateKey := keys.KeyOrderState(orderID)
	orderStateBytes, err := hm.dbManager.Get(orderStateKey)
	if err == nil && orderStateBytes != nil {
		var orderState pb.OrderState
		if err := proto.Unmarshal(orderStateBytes, &orderState); err != nil {
			return
		}
		if orderState.IsFilled || orderState.Status == pb.OrderStateStatus_ORDER_CANCELLED {
			return
		}

		if _, err := decimal.NewFromString(orderState.Price); err != nil {
			return
		}
		amount, err := decimal.NewFromString(orderState.Amount)
		if err != nil {
			return
		}
		filledBase, _ := decimal.NewFromString(orderState.FilledBase)
		remainAmount := amount.Sub(filledBase)
		if remainAmount.LessThanOrEqual(decimal.Zero) {
			return
		}

		priceStr := orderState.Price
		                                                  
		isPending := false                                                                               

		if orderState.Side == pb.OrderSide_SELL {
			if askPrices[priceStr] == nil {
				askPrices[priceStr] = &priceLevelDataForAgg{}
			}
			askPrices[priceStr].amount = askPrices[priceStr].amount.Add(remainAmount)
			if isPending {
				askPrices[priceStr].pendingCount++
			} else {
				askPrices[priceStr].confirmedCount++
			}
		} else {            
			if bidPrices[priceStr] == nil {
				bidPrices[priceStr] = &priceLevelDataForAgg{}
			}
			bidPrices[priceStr].amount = bidPrices[priceStr].amount.Add(remainAmount)
			if isPending {
				bidPrices[priceStr].pendingCount++
			} else {
				bidPrices[priceStr].confirmedCount++
			}
		}
	}
}

                                                              
                                                                           
func extractOrderIDFromIndexKey(key string) string {
	const marker = "|order_id:"
	idx := strings.LastIndex(key, marker)
	if idx == -1 {
		return ""
	}
	return key[idx+len(marker):]
}

                                                                                    
func aggregatePriceToEntries(priceMap map[string]decimal.Decimal, descending bool) []OrderBookEntry {
	entries := make([]OrderBookEntry, 0, len(priceMap))
	cumTotal := decimal.Zero

	                     
	type pricePair struct {
		price  decimal.Decimal
		amount decimal.Decimal
	}
	var pairs []pricePair
	for priceStr, amount := range priceMap {
		price, _ := decimal.NewFromString(priceStr)
		pairs = append(pairs, pricePair{price: price, amount: amount})
	}

	sort.Slice(pairs, func(i, j int) bool {
		if descending {
			return pairs[i].price.GreaterThan(pairs[j].price)
		}
		return pairs[i].price.LessThan(pairs[j].price)
	})

	                                                                                                              
	unit := decimal.NewFromInt(1e8)
	for _, p := range pairs {
		cumTotal = cumTotal.Add(p.amount.Mul(p.price).Div(unit))
		entries = append(entries, OrderBookEntry{
			Price:  p.price.String(),
			Amount: p.amount.String(),
			Total:  cumTotal.String(),
		})
	}

	return entries
}

                                                                                                
type priceLevelDataForAgg struct {
	amount         decimal.Decimal
	pendingCount   int
	confirmedCount int
}

                                                                                                 
func aggregatePriceToEntriesWithStatus(priceMap map[string]*priceLevelDataForAgg, descending bool) []OrderBookEntry {
	entries := make([]OrderBookEntry, 0, len(priceMap))
	cumTotal := decimal.Zero

	                     
	type pricePair struct {
		price          decimal.Decimal
		amount         decimal.Decimal
		pendingCount   int
		confirmedCount int
	}
	var pairs []pricePair
	for priceStr, data := range priceMap {
		price, _ := decimal.NewFromString(priceStr)
		pairs = append(pairs, pricePair{
			price:          price,
			amount:         data.amount,
			pendingCount:   data.pendingCount,
			confirmedCount: data.confirmedCount,
		})
	}

	sort.Slice(pairs, func(i, j int) bool {
		if descending {
			return pairs[i].price.GreaterThan(pairs[j].price)
		}
		return pairs[i].price.LessThan(pairs[j].price)
	})

	                                                                                                              
	unit := decimal.NewFromInt(1e8)
	for _, p := range pairs {
		cumTotal = cumTotal.Add(p.amount.Mul(p.price).Div(unit))
		entries = append(entries, OrderBookEntry{
			Price:          p.price.String(),
			Amount:         p.amount.String(),
			Total:          cumTotal.String(),
			PendingCount:   p.pendingCount,
			ConfirmedCount: p.confirmedCount,
		})
	}

	return entries
}

                           
type TradeRecord struct {
	ID           string `json:"id"`
	Time         string `json:"time"`
	Height       uint64 `json:"height,omitempty"`
	Price        string `json:"price"`
	Amount       string `json:"amount"`
	Side         string `json:"side"`                   
	MakerOrderID string `json:"maker_order_id,omitempty"`
	TakerOrderID string `json:"taker_order_id,omitempty"`
}

                                              
func (hm *HandlerManager) HandleTrades(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("HandleTrades")

	pair := r.URL.Query().Get("pair")
	if pair == "" {
		http.Error(w, "missing pair parameter", http.StatusBadRequest)
		return
	}

	                                 
	limit := 100
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	prefix := keys.KeyTradeRecordPrefix(pair)
	trades, err := hm.dbManager.ScanByPrefix(prefix, limit)
	if err != nil {
		http.Error(w, "failed to scan trades: "+err.Error(), http.StatusInternalServerError)
		return
	}

	records := make([]TradeRecord, 0)                                                                
	for _, data := range trades {
		var trade pb.TradeRecord
		if err := proto.Unmarshal([]byte(data), &trade); err != nil {
			continue
		}
		side := "buy"
		if trade.TakerSide == pb.OrderSide_SELL {
			side = "sell"
		}
		records = append(records, TradeRecord{
			ID:           trade.TradeId,
			Time:         trade.Timestamp,
			Height:       trade.Height,
			Price:        trade.Price,
			Amount:       trade.Amount,
			Side:         side,
			MakerOrderID: trade.MakerOrderId,
			TakerOrderID: trade.TakerOrderId,
		})
	}

	                                                                                                                                      
	sort.Slice(records, func(i, j int) bool {
		if records[i].Time != records[j].Time {
			return records[i].Time > records[j].Time
		}
		return records[i].ID > records[j].ID
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(records)
}
