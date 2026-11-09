package handlers

import (
	"dex/future"
	"dex/interfaces"
	"dex/keys"
	"dex/protocol"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"dex/pb"

	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"
)

                                         

                                                   
  
                                                                                                                   
                                                                                                  
                                                                                   
type futurePositionInfo struct {
	PositionID            string `json:"position_id"`
	Owner                 string `json:"owner"`
	Pair                  string `json:"pair"`
	Side                  string `json:"side"`                    
	EntryPrice            string `json:"entry_price"`
	Quantity              string `json:"quantity"`
	Margin                string `json:"margin"`
	Status                string `json:"status"`                                                                 
	OpenHeight            uint64 `json:"open_height"`
	LastCumulativeFunding string `json:"last_cumulative_funding"`                                    
	SettledFunding        string `json:"settled_funding"`                                                              
	LastSettledFunding    string `json:"last_settled_funding"`                                                                  
	                                                                               
	PendingFunding  string `json:"pending_funding"`
	DisplayFunding  string `json:"display_funding"`
	EffectiveMargin string `json:"effective_margin"`
	UnrealizedPnl   string `json:"unrealized_pnl"`
	MarkPrice       string `json:"mark_price"`
	CurrentLiqPrice string `json:"current_liq_price"`
	Health          string `json:"health"`                                                    
	                                                                                                      
	RealizedPnl    string `json:"realized_pnl"`
	ADLWithheldPnl string `json:"adl_withheld_pnl"`
	ClosedQty      string `json:"closed_qty"`
	ExitNotional   string `json:"exit_notional"`                                                                                
	MaxQuantity    string `json:"max_quantity"`
	InitialMargin  string `json:"initial_margin"`              
	CloseHeight    uint64 `json:"close_height"`                                        
}

                                                 
type futureMarketInfo struct {
	Pair                     string `json:"pair"`
	MarkPrice                string `json:"mark_price"`
	SpotPrice                string `json:"spot_price"`
	FuturePrice              string `json:"future_price"`
	FundingRate              string `json:"funding_rate_bps"`                        
	SettledFundingBps        string `json:"settled_funding_rate_bps"`                      
	SpotEMA                  string `json:"spot_ema"`                                                                                    
	FutureEMA                string `json:"future_ema"`                                                 
	EMAWindow                int    `json:"ema_window"`                                                        
	MaxFundingBps            int64  `json:"max_funding_rate_bps"`                                          
	LastFundingHeight        uint64 `json:"last_funding_height"`
	NextFundingHeight        uint64 `json:"next_funding_height"`
	FundingInterval          uint64 `json:"funding_interval"`
	InsuranceFund            string `json:"insurance_fund"`
	MaxLeverage              uint64 `json:"max_leverage"`
	MaintenanceMarginRateBps uint64 `json:"maintenance_margin_rate_bps"`
	CumulativeLong           string `json:"cumulative_long"`                                                                      
	CumulativeShort          string `json:"cumulative_short"`                             
	SettleMarkPrice          string `json:"settle_mark_price"`                                                                                       
}

func sideStr(s int32) string {
	if s == 1 {
		return "LONG"
	}
	return "SHORT"
}

func statusStr(s int32) string {
	switch future.PositionStatus(s) {
	case future.StatusOpen:
		return "OPEN"
	case future.StatusClosed:
		return "CLOSED"
	case future.StatusLiquidated:
		return "LIQUIDATED"
	case future.StatusADLClosed:
		return "ADL_CLOSED"
	case future.StatusInsuredDeleveraged:
		return "INSURED_DELEVERAGED"
	default:
		return "UNKNOWN"
	}
}

type futurePositionsResponse struct {
	Positions []futurePositionInfo `json:"positions"`
	Total     int                  `json:"total"`
	Offset    int                  `json:"offset"`
	Limit     int                  `json:"limit"`
}

                                                                             
                                                                            
                                 
func (hm *HandlerManager) HandleFuturePositions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	pair := q.Get("pair")
	owner := q.Get("owner")
	if !strings.EqualFold(strings.TrimSpace(q.Get("status")), "open") {
		writeJSONStatusError(w, http.StatusBadRequest, "terminal position history is Explorer-owned; node endpoint only supports status=OPEN")
		return
	}
	offset := parseIntDefault(q.Get("offset"), 0, 0, 5_000)
	limit := parseIntDefault(q.Get("limit"), 100, 1, 500)
	scanBudget := offset + limit

	var prefix string
	if owner != "" {
		prefix = keys.KeyFutureOwnerPositionPrefix(owner)
	} else if pair != "" {
		prefix = keys.KeyFuturePairPositionPrefix(pair)
	} else {
		prefix = keys.KeyFuturePositionPrefix()
	}

	kvs, err := hm.dbManager.ScanKVWithLimit(prefix, scanBudget)
	if err != nil {
		writeJSONError(w, "scan error: "+err.Error())
		return
	}

	sortedKeys := make([]string, 0, len(kvs))
	for key := range kvs {
		sortedKeys = append(sortedKeys, key)
	}
	sort.Strings(sortedKeys)
	positions := make([]futurePositionInfo, 0, len(sortedKeys))
	for _, key := range sortedKeys {
		var ps pb.FuturePositionState
		if err := proto.Unmarshal(kvs[key], &ps); err != nil || ps.PositionId == "" || ps.Status != int32(future.StatusOpen) {
			continue
		}
		if (owner != "" && ps.Owner != owner) || (pair != "" && ps.Pair != pair) {
			continue
		}
		positions = append(positions, hm.buildFuturePositionInfo(&ps))
	}
	sort.SliceStable(positions, func(i, j int) bool {
		if positions[i].OpenHeight != positions[j].OpenHeight {
			return positions[i].OpenHeight > positions[j].OpenHeight
		}
		return positions[i].PositionID > positions[j].PositionID
	})
	total := len(positions)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	writeJSONData(w, futurePositionsResponse{Positions: positions[offset:end], Total: total, Offset: offset, Limit: limit})
}

                                                                                                         
                                                                                                               
                                                                    
func (hm *HandlerManager) buildFuturePositionInfo(ps *pb.FuturePositionState) futurePositionInfo {
	info := futurePositionInfo{
		PositionID: ps.PositionId, Owner: ps.Owner, Pair: ps.Pair,
		Side: sideStr(ps.Side), EntryPrice: ps.EntryPrice, Quantity: ps.Quantity,
		Margin: ps.Margin, Status: statusStr(ps.Status), OpenHeight: ps.OpenHeight,
		LastCumulativeFunding: ps.LastCumulativeFunding,
		SettledFunding:        ps.SettledFunding,
		LastSettledFunding:    ps.LastSettledFunding,
		RealizedPnl:           ps.RealizedPnl,
		ADLWithheldPnl:        ps.AdlWithheldPnl,
		ClosedQty:             ps.ClosedQty,
		ExitNotional:          ps.ExitNotional,
		MaxQuantity:           ps.MaxQuantity,
		InitialMargin:         ps.InitialMargin,
		CloseHeight:           ps.CloseHeight,
	}
	                                                                                             
	if ps.Status != int32(future.StatusOpen) {
		return info
	}

	entryPrice := parseFutureBigInt(ps.EntryPrice)
	quantity := parseFutureBigInt(ps.Quantity)
	margin := parseFutureBigInt(ps.Margin)
	lastCum := parseFutureBigInt(ps.LastCumulativeFunding)
	if entryPrice.Sign() <= 0 || quantity.Sign() <= 0 {
		return info
	}
	firstCum := parseFutureBigInt(ps.FirstCumulativeFunding)
	pos := &future.Position{
		ID: ps.PositionId, Owner: ps.Owner, Pair: ps.Pair,
		Side:                   future.Side(ps.Side),
		EntryPrice:             entryPrice,
		EntryNotionalNumerator: parseFutureBigInt(ps.EntryNotionalNumerator),
		Quantity:               quantity,
		Margin:                 margin,
		MarginBasisAmount:      parseFutureBigInt(ps.MarginBasisAmount),
		MarginBasisClosed:      parseFutureBigInt(ps.MarginBasisClosed),
		Status:                 future.PositionStatus(ps.Status),
		OpenHeight:             ps.OpenHeight,
		LastCumulativeFunding:  lastCum,
		FirstCumulativeFunding: firstCum,
		SettledFunding:         parseFutureBigInt(ps.SettledFunding),
		LastSettledFunding:     parseFutureBigInt(ps.LastSettledFunding),
		ClosePnLRemainder:      parseFutureBigInt(ps.ClosePnlRemainder),
	}

	                                                                                                         
	                                                                
	markPrice := hm.readFutureBigInt(keys.KeyFutureMarkPrice(ps.Pair))
	settleMarkPrice := hm.readFutureBigInt(keys.KeyFutureSettleMarkPrice(ps.Pair))
	cumLong, cumShort, prevLong, prevShort := hm.readFutureCumFunding(ps.Pair)
	cfg := hm.deriveFutureMarketConfig(ps.Pair)

	view := future.BuildViewWithSettleMark(pos, markPrice, settleMarkPrice, cumLong, cumShort, prevLong, prevShort, cfg)
	if view == nil {
		return info
	}
	info.PendingFunding = view.PendingFunding.String()
	info.DisplayFunding = view.DisplayFunding.String()
	info.EffectiveMargin = view.EffectiveMargin.String()
	info.UnrealizedPnl = view.UnrealizedPnL.String()
	if view.MarkPrice != nil {
		info.MarkPrice = view.MarkPrice.String()
	}
	if view.CurrentLiqPrice != nil {
		info.CurrentLiqPrice = view.CurrentLiqPrice.String()
	}
	if view.Health != nil && view.Health.Sign() != 0 {
		                                               
		info.Health = view.Health.FloatString(6)
	}
	return info
}

func parseFutureBigInt(s string) *big.Int {
	if v, ok := new(big.Int).SetString(s, 10); ok {
		return v
	}
	return new(big.Int)
}

                                                                                 
func (hm *HandlerManager) readFutureBigInt(key string) *big.Int {
	data, err := hm.dbManager.Get(key)
	if err != nil || len(data) == 0 {
		return nil
	}
	return new(big.Int).SetBytes(data)
}

                                                                                                      
func (hm *HandlerManager) readFutureCumFunding(pair string) (cumLong, cumShort, prevLong, prevShort *big.Int) {
	cumLong, cumShort = new(big.Int), new(big.Int)
	prevLong, prevShort = new(big.Int), new(big.Int)
	data, err := hm.dbManager.Get(keys.KeyFutureCumulativeFunding(pair))
	if err != nil || len(data) == 0 {
		return
	}
	var cf pb.FutureCumulativeFunding
	if err := proto.Unmarshal(data, &cf); err != nil {
		return
	}
	parse := func(s string, dst **big.Int) {
		if v, ok := new(big.Int).SetString(s, 10); ok {
			*dst = v
		}
	}
	parse(cf.CumulativeLong, &cumLong)
	parse(cf.CumulativeShort, &cumShort)
	parse(cf.PrevCumulativeLong, &prevLong)
	parse(cf.PrevCumulativeShort, &prevShort)
	return
}

                                                                          
                                                                           
               
func (hm *HandlerManager) deriveFutureMarketConfig(pair string) *future.MarketConfig {
	baseToken, quoteToken := "", ""
	if data, err := hm.dbManager.Get(keys.KeyFutureMarketConfig(pair)); err == nil {
		if identity, decodeErr := protocol.DecodeMarketIdentity(pair, data); decodeErr == nil {
			baseToken = identity.BaseToken
			quoteToken = identity.QuoteToken
		}
	}
	                                                                         
	                                                                            
	if baseToken == "" && strings.Count(pair, "_") == 1 {
		baseToken, quoteToken, _ = strings.Cut(pair, "_")
	}
	return &future.MarketConfig{
		Pair:                     pair,
		BaseToken:                baseToken,
		MaxLeverage:              future.DefaultMaxLeverage,
		MaintenanceMarginRateBps: future.DefaultMaintenanceMarginRateBps,
		QuoteToken:               quoteToken,
	}
}

type futureMarketsResponse struct {
	Pairs   []string               `json:"pairs"`
	Markets []marketDirectoryEntry `json:"markets"`
}

type futureOrderedScanFunc func(prefix string, limit int, reverse bool) ([]interfaces.KVPair, error)

const futureMarketDirectoryLimit = 4096

                                          
func (hm *HandlerManager) HandleFutureMarkets(w http.ResponseWriter, _ *http.Request) {
	handleFutureMarketsWithScan(w, hm.dbManager.ScanKVWithLimitOrdered)
}

func handleFutureMarketsWithScan(w http.ResponseWriter, scan futureOrderedScanFunc) {
	rows, err := scan(keys.KeyFutureMarketConfigPrefix(), futureMarketDirectoryLimit+1, false)
	if err != nil {
		writeJSONError(w, "scan future markets error: "+err.Error())
		return
	}
	if len(rows) > futureMarketDirectoryLimit {
		writeJSONStatusError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("future market directory exceeds hard limit %d", futureMarketDirectoryLimit))
		return
	}

	seen := make(map[string]struct{}, len(rows))
	marketsByPair := make(map[string]marketDirectoryEntry, len(rows))
	pairs := make([]string, 0, len(rows))
	for _, row := range rows {
		pair := strings.TrimPrefix(row.Key, keys.KeyFutureMarketConfigPrefix())
		if pair == "" || row.Key != keys.KeyFutureMarketConfigPrefix()+pair {
			continue
		}
		if _, exists := seen[pair]; !exists {
			seen[pair] = struct{}{}
			pairs = append(pairs, pair)
		}
		if _, exists := marketsByPair[pair]; !exists {
			if entry, ok := marketDirectoryEntryFromConfig(pair, row.Value); ok {
				marketsByPair[pair] = entry
			}
		}
	}
	sort.Strings(pairs)
	markets := make([]marketDirectoryEntry, 0, len(marketsByPair))
	for _, pair := range pairs {
		if entry, ok := marketsByPair[pair]; ok {
			markets = append(markets, entry)
		}
	}
	writeJSONData(w, futureMarketsResponse{Pairs: pairs, Markets: markets})
}

                                                     
func (hm *HandlerManager) HandleFutureMarket(w http.ResponseWriter, r *http.Request) {
	pair := r.URL.Query().Get("pair")
	if pair == "" {
		pair = "FB_USDT"
	}

	readBigInt := func(key string) *big.Int {
		data, err := hm.dbManager.Get(key)
		if err != nil || data == nil {
			return new(big.Int)
		}
		return new(big.Int).SetBytes(data)
	}

	markPrice := readBigInt(keys.KeyFutureMarkPrice(pair))
	spotPrice := readBigInt(keys.KeySpotLastPrice(pair))
	futurePrice := readBigInt(keys.KeyFutureLastPrice(pair))

	                                                                                                                      
	                                                                                                            
	                                        
	emaSpot := readBigInt(keys.KeyFutureSpotEMA(pair))
	if emaSpot.Sign() <= 0 {
		emaSpot = spotPrice
	}
	emaFut := readBigInt(keys.KeyFutureEMA(pair))
	if emaFut.Sign() <= 0 {
		emaFut = futurePrice
	}
	if emaFut.Sign() <= 0 {
		emaFut = markPrice
	}
	liveRate := future.CalcFundingRate(emaFut, emaSpot, future.DefaultMaxFundingRateBps)

	                                                                                                                     
	var settledRate int64
	if data, err := hm.dbManager.Get(keys.KeyFutureFundingRate(pair)); err == nil && len(data) == 8 {
		settledRate = int64(binary.BigEndian.Uint64(data))
	}
	var lastHeight uint64
	if data, err := hm.dbManager.Get(keys.KeyFutureFundingHeight(pair)); err == nil && len(data) == 8 {
		lastHeight = binary.BigEndian.Uint64(data)
	}

	                                                                            
	cumLong, cumShort := "0", "0"
	if data, err := hm.dbManager.Get(keys.KeyFutureCumulativeFunding(pair)); err == nil && len(data) > 0 {
		var cf pb.FutureCumulativeFunding
		if err := proto.Unmarshal(data, &cf); err == nil {
			if cf.CumulativeLong != "" {
				cumLong = cf.CumulativeLong
			}
			if cf.CumulativeShort != "" {
				cumShort = cf.CumulativeShort
			}
		}
	}
	cfg := hm.deriveFutureMarketConfig(pair)

	info := futureMarketInfo{
		Pair:                     pair,
		MarkPrice:                markPrice.String(),
		SpotPrice:                spotPrice.String(),
		FuturePrice:              futurePrice.String(),
		FundingRate:              fmt.Sprintf("%d", liveRate),
		SettledFundingBps:        fmt.Sprintf("%d", settledRate),
		LastFundingHeight:        lastHeight,
		NextFundingHeight:        lastHeight + future.DefaultFundingInterval,
		FundingInterval:          future.DefaultFundingInterval,
		InsuranceFund:            readBigInt(keys.KeyFutureInsuranceFund(pair)).String(),
		MaxLeverage:              cfg.MaxLeverageOrDefault(),
		MaintenanceMarginRateBps: cfg.MaintenanceMarginRateOrDefault(),
		CumulativeLong:           cumLong,
		CumulativeShort:          cumShort,
		SettleMarkPrice:          readBigInt(keys.KeyFutureSettleMarkPrice(pair)).String(),
		SpotEMA:                  emaSpot.String(),
		FutureEMA:                emaFut.String(),
		EMAWindow:                future.DefaultEMAWindow,
		MaxFundingBps:            future.DefaultMaxFundingRateBps,
	}
	writeJSONData(w, info)
}

                                               
type futureOrderBookEntry struct {
	OrderID  string `json:"order_id"`
	Owner    string `json:"owner"`
	Side     string `json:"side"`
	Price    string `json:"price"`
	Amount   string `json:"amount"`
	Filled   string `json:"filled"`
	Leverage uint64 `json:"leverage"`
}

const futureOrderBookSideLimit = 500

                                                           
func (hm *HandlerManager) HandleFutureOrderBook(w http.ResponseWriter, r *http.Request) {
	handleFutureOrderBookWithScan(w, r, hm.dbManager.ScanKVWithLimitOrdered)
}

func handleFutureOrderBookWithScan(w http.ResponseWriter, r *http.Request, scan futureOrderedScanFunc) {
	pair := r.URL.Query().Get("pair")
	if pair == "" {
		pair = "FB_USDT"
	}

	                                                                  
	buyPrefix := keys.KeyFutureOrderPriceIndexPrefix(pair, pb.OrderSide_BUY, false)
	buyKVs, err := scan(buyPrefix, futureOrderBookSideLimit, true)
	if err != nil {
		writeJSONError(w, "scan future bids error: "+err.Error())
		return
	}
	bids := parseFutureOrders(buyKVs, "BUY", true)

	                                                                   
	sellPrefix := keys.KeyFutureOrderPriceIndexPrefix(pair, pb.OrderSide_SELL, false)
	sellKVs, err := scan(sellPrefix, futureOrderBookSideLimit, false)
	if err != nil {
		writeJSONError(w, "scan future asks error: "+err.Error())
		return
	}
	asks := parseFutureOrders(sellKVs, "SELL", false)

	writeJSONData(w, map[string]interface{}{
		"pair": pair,
		"bids": bids,
		"asks": asks,
	})
}

func parseFutureOrders(kvs []interfaces.KVPair, sideLabel string, descending bool) []futureOrderBookEntry {
	type entry struct {
		price decimal.Decimal
		e     futureOrderBookEntry
	}
	var entries []entry
	for _, kv := range kvs {
		var os pb.FutureOrderState
		if err := proto.Unmarshal(kv.Value, &os); err != nil || os.Status != pb.FutureOrderStatus_FUTURE_ORDER_OPEN {
			continue
		}
		p, err := decimal.NewFromString(os.Price)
		if err != nil || !p.IsPositive() {
			continue
		}
		entries = append(entries, entry{
			price: p,
			e: futureOrderBookEntry{
				OrderID: os.OrderId, Owner: os.Owner, Side: sideLabel,
				Price: os.Price, Amount: os.Amount, Filled: os.FilledBase, Leverage: os.Leverage,
			},
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if !entries[i].price.Equal(entries[j].price) {
			if descending {
				return entries[i].price.GreaterThan(entries[j].price)
			}
			return entries[i].price.LessThan(entries[j].price)
		}
		                                                                              
		return entries[i].e.OrderID < entries[j].e.OrderID
	})
	resultLen := len(entries)
	if resultLen > futureOrderBookSideLimit {
		resultLen = futureOrderBookSideLimit
	}
	result := make([]futureOrderBookEntry, resultLen)
	for i, en := range entries[:resultLen] {
		result[i] = en.e
	}
	return result
}

                                                               
func (hm *HandlerManager) HandleFutureTrades(w http.ResponseWriter, r *http.Request) {
	pair := r.URL.Query().Get("pair")
	if pair == "" {
		pair = "FB_USDT"
	}
	limit := 100
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 {
		limit = l
	}

	prefix := keys.KeyFutureTradeRecordPrefix(pair)
	rawRecords, err := hm.dbManager.ScanByPrefix(prefix, limit)
	if err != nil {
		writeJSONError(w, "scan error: "+err.Error())
		return
	}

	records := make([]futureTradeRecord, 0, len(rawRecords))
	for _, data := range rawRecords {
		var trade pb.TradeRecord
		if err := proto.Unmarshal([]byte(data), &trade); err != nil {
			continue
		}
		side := "buy"
		if trade.TakerSide == pb.OrderSide_SELL {
			side = "sell"
		}
		tradeType := "trade"
		if len(trade.TradeId) > 4 && trade.TradeId[:4] == "liq_" {
			tradeType = "liquidation"
		}
		records = append(records, futureTradeRecord{
			ID:      trade.TradeId,
			Time:    trade.Timestamp,
			Height:  trade.Height,
			Price:   trade.Price,
			Amount:  trade.Amount,
			Side:    side,
			Type:    tradeType,
			MakerID: trade.MakerOrderId,
			TakerID: trade.TakerOrderId,
		})
	}
	                                                                                                                              
	sort.Slice(records, func(i, j int) bool {
		if records[i].Height != records[j].Height {
			return records[i].Height > records[j].Height
		}
		return records[i].ID > records[j].ID
	})
	writeJSONData(w, records)
}

                                                              
type futureTradeRecord struct {
	ID      string `json:"id"`
	Time    string `json:"time"`
	Height  uint64 `json:"height,omitempty"`
	Price   string `json:"price"`
	Amount  string `json:"amount"`
	Side    string `json:"side"`
	Type    string `json:"type"`                           
	MakerID string `json:"maker_order_id,omitempty"`
	TakerID string `json:"taker_order_id,omitempty"`
}

func writeJSONData(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func writeJSONError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func writeJSONStatusError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
