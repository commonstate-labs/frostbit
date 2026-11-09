package handlers

import (
	"dex/future"
	"dex/keys"
	"dex/pb"
	"encoding/json"
	"math/big"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"
)

                                  
type MyOrderEntry struct {
	OrderID      string `json:"order_id"`
	Pair         string `json:"pair"`
	BaseToken    string `json:"base_token"`
	QuoteToken   string `json:"quote_token"`
	Side         string `json:"side"`
	Price        string `json:"price"`
	Amount       string `json:"amount"`
	FilledBase   string `json:"filled_base"`
	FilledQuote  string `json:"filled_quote"`
	IsFilled     bool   `json:"is_filled"`
	Status       string `json:"status"`
	Owner        string `json:"owner"`
	CreateHeight uint64 `json:"create_height"`
}

                                      
type MyOrdersResponse struct {
	Orders []MyOrderEntry `json:"orders"`
	Error  string         `json:"error,omitempty"`
}

                                                   
                                                              
func (hm *HandlerManager) HandleMyOrders(w http.ResponseWriter, r *http.Request) {
	addr := r.URL.Query().Get("address")
	if addr == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(MyOrdersResponse{Error: "missing address parameter"})
		return
	}

	pairFilter := r.URL.Query().Get("pair")
	statusFilter := strings.ToLower(r.URL.Query().Get("status"))                             
	limit := 100
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 {
		limit = l
	}
	if limit > 500 {
		limit = 500
	}

	indexData, err := hm.scanSpotAccountOrderIndex(addr, pairFilter, statusFilter, limit)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(MyOrdersResponse{Error: "scan failed: " + err.Error()})
		return
	}

	sortedKeys := make([]string, 0, len(indexData))
	for k := range indexData {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Strings(sortedKeys)

	orders := make([]MyOrderEntry, 0, len(indexData))
	for _, k := range sortedKeys {
		stateBytes := indexData[k]
		var state pb.OrderState
		if err := proto.Unmarshal(stateBytes, &state); err != nil || state.OrderId == "" {
			continue
		}
		if state.Owner != addr {
			continue
		}

		pair := state.BaseToken + "_" + state.QuoteToken
		if pairFilter != "" && pair != pairFilter {
			continue
		}

		statusStr := orderStateStatusStr(state.Status)
		if statusFilter != "" && statusFilter != "all" && statusStr != statusFilter {
			continue
		}

		sideStr := "buy"
		if state.Side == pb.OrderSide_SELL {
			sideStr = "sell"
		}

		orders = append(orders, MyOrderEntry{
			OrderID:      state.OrderId,
			Pair:         pair,
			BaseToken:    state.BaseToken,
			QuoteToken:   state.QuoteToken,
			Side:         sideStr,
			Price:        state.Price,
			Amount:       state.Amount,
			FilledBase:   state.FilledBase,
			FilledQuote:  state.FilledQuote,
			IsFilled:     state.IsFilled,
			Status:       statusStr,
			Owner:        state.Owner,
			CreateHeight: state.CreateHeight,
		})
	}

	                                                                                                                         
	sort.Slice(orders, func(i, j int) bool {
		if orders[i].CreateHeight != orders[j].CreateHeight {
			return orders[i].CreateHeight > orders[j].CreateHeight
		}
		return orders[i].OrderID < orders[j].OrderID
	})
	if len(orders) > limit {
		orders = orders[:limit]
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(MyOrdersResponse{Orders: orders})
}

func (hm *HandlerManager) scanSpotAccountOrderIndex(addr, pairFilter, statusFilter string, limit int) (map[string][]byte, error) {
	if pairFilter == "" {
		statuses := keys.SpotAccountOrderStatusKeys()
		if statusFilter != "" && statusFilter != "all" {
			statuses = []string{statusFilter}
		}
		out := make(map[string][]byte)
		for _, status := range statuses {
			part, err := hm.dbManager.ScanKVWithLimit(keys.KeySpotAccountOrderRecentPrefix(addr, status), limit)
			if err != nil {
				return nil, err
			}
			for recentKey, primaryKey := range part {
				data, getErr := hm.dbManager.Get(string(primaryKey))
				if getErr == nil && len(data) > 0 {
					out[recentKey] = data
				}
			}
		}
		return out, nil
	}
	if statusFilter != "" && statusFilter != "all" {
		return hm.dbManager.ScanKVWithLimit(keys.KeySpotAccountOrderPrefix(addr, pairFilter, statusFilter), limit)
	}
	out := make(map[string][]byte)
	for _, status := range keys.SpotAccountOrderStatusKeys() {
		part, err := hm.dbManager.ScanKVWithLimit(keys.KeySpotAccountOrderPrefix(addr, pairFilter, status), limit)
		if err != nil {
			return nil, err
		}
		for k, v := range part {
			out[k] = v
		}
	}
	return out, nil
}

func orderStateStatusStr(s pb.OrderStateStatus) string {
	switch s {
	case pb.OrderStateStatus_ORDER_OPEN:
		return "open"
	case pb.OrderStateStatus_ORDER_FILLED:
		return "filled"
	case pb.OrderStateStatus_ORDER_CANCELLED:
		return "cancelled"
	default:
		return "unknown"
	}
}

                                              
type FutureMyOrderEntry struct {
	OrderID      string `json:"order_id"`
	Pair         string `json:"pair"`
	Side         string `json:"side"`
	Price        string `json:"price"`
	Amount       string `json:"amount"`
	FilledBase   string `json:"filled_base"`
	FilledQuote  string `json:"filled_quote"`                                                                   
	Leverage     uint64 `json:"leverage"`
	IsClose      bool   `json:"is_close"`
	Status       string `json:"status"`
	Owner        string `json:"owner"`
	CreateHeight uint64 `json:"create_height"`
}

                                                  
type FutureMyOrdersResponse struct {
	Orders []FutureMyOrderEntry `json:"orders"`
	Error  string               `json:"error,omitempty"`
}

                                                                     
                                                                     
func (hm *HandlerManager) HandleFutureMyOrders(w http.ResponseWriter, r *http.Request) {
	addr := r.URL.Query().Get("address")
	if addr == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(FutureMyOrdersResponse{Error: "missing address parameter"})
		return
	}

	pairFilter := r.URL.Query().Get("pair")
	statusFilter := strings.ToLower(r.URL.Query().Get("status"))
	limit := 100
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 {
		limit = l
	}
	if limit > 500 {
		limit = 500
	}

	indexData, err := hm.scanFutureAccountOrderIndex(addr, pairFilter, statusFilter, limit)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(FutureMyOrdersResponse{Error: "scan failed: " + err.Error()})
		return
	}

	                                                                              
	sortedKeys := make([]string, 0, len(indexData))
	for k := range indexData {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Strings(sortedKeys)

	orders := make([]FutureMyOrderEntry, 0, len(indexData))
	for _, k := range sortedKeys {
		stateBytes := indexData[k]
		var state pb.FutureOrderState
		if err := proto.Unmarshal(stateBytes, &state); err != nil || state.OrderId == "" {
			continue
		}

		if pairFilter != "" && state.Pair != pairFilter {
			continue
		}

		statusStr := futureOrderStatusStr(state.Status)
		if statusFilter != "" && statusFilter != "all" && statusStr != statusFilter {
			continue
		}

		sideStr := "buy"
		if state.Side == 2 {
			sideStr = "sell"
		}

		orders = append(orders, FutureMyOrderEntry{
			OrderID:      state.OrderId,
			Pair:         state.Pair,
			Side:         sideStr,
			Price:        state.Price,
			Amount:       state.Amount,
			FilledBase:   state.FilledBase,
			FilledQuote:  ensureFilledQuoteFallback(&state),
			Leverage:     state.Leverage,
			IsClose:      state.IsClose,
			Status:       statusStr,
			Owner:        state.OwnerAddress,
			CreateHeight: state.CreateHeight,
		})

	}

	sort.Slice(orders, func(i, j int) bool {
		if orders[i].CreateHeight != orders[j].CreateHeight {
			return orders[i].CreateHeight > orders[j].CreateHeight
		}
		return orders[i].OrderID < orders[j].OrderID
	})
	if len(orders) > limit {
		orders = orders[:limit]
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(FutureMyOrdersResponse{Orders: orders})
}

                                                                            
func (hm *HandlerManager) scanFutureAccountOrderIndex(addr, pairFilter, statusFilter string, limit int) (map[string][]byte, error) {
	if pairFilter == "" {
		statuses := keys.FutureAccountOrderStatusKeys()
		if statusFilter != "" && statusFilter != "all" {
			statuses = []string{statusFilter}
		}
		out := make(map[string][]byte)
		for _, status := range statuses {
			part, err := hm.dbManager.ScanKVWithLimit(keys.KeyFutureAccountOrderRecentPrefix(addr, status), limit)
			if err != nil {
				return nil, err
			}
			for recentKey, primaryKey := range part {
				data, getErr := hm.dbManager.Get(string(primaryKey))
				if getErr == nil && len(data) > 0 {
					out[recentKey] = data
				}
			}
		}
		return out, nil
	}
	if statusFilter != "" && statusFilter != "all" {
		return hm.dbManager.ScanKVWithLimit(keys.KeyFutureAccountOrderPrefix(addr, pairFilter, statusFilter), limit)
	}
	out := make(map[string][]byte)
	for _, status := range keys.FutureAccountOrderStatusKeys() {
		part, err := hm.dbManager.ScanKVWithLimit(keys.KeyFutureAccountOrderPrefix(addr, pairFilter, status), limit)
		if err != nil {
			return nil, err
		}
		for k, v := range part {
			out[k] = v
		}
	}
	return out, nil
}

                                                                                                       
                                                  
                                                                 
func ensureFilledQuoteFallback(state *pb.FutureOrderState) string {
	if state.FilledQuote != "" && state.FilledQuote != "0" {
		return state.FilledQuote
	}
	filled, ok1 := new(big.Int).SetString(state.FilledBase, 10)
	price, ok2 := new(big.Int).SetString(state.Price, 10)
	if !ok1 || !ok2 || filled == nil || price == nil {
		return "0"
	}
	return future.CalcNotional(filled, price).String()
}

func futureOrderStatusStr(s pb.FutureOrderStatus) string {
	switch s {
	case pb.FutureOrderStatus_FUTURE_ORDER_OPEN:
		return "open"
	case pb.FutureOrderStatus_FUTURE_ORDER_FILLED:
		return "filled"
	case pb.FutureOrderStatus_FUTURE_ORDER_CANCELLED:
		return "cancelled"
	case pb.FutureOrderStatus_FUTURE_ORDER_CLOSED:
		return "closed"
	case pb.FutureOrderStatus_FUTURE_ORDER_LIQUIDATED:
		return "liquidated"
	default:
		return "unknown"
	}
}
