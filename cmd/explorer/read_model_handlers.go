package main

import (
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"dex/cmd/explorer/indexdb"
	"dex/future"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

const (
	explorerOrderPageDefault       = 100
	explorerReadModelPageMax       = 500
	explorerActivityPageDefault    = 50
	explorerActivityOffsetMax      = 5000
	explorerReadModelAddressMaxLen = 256
	explorerReadModelFilterMaxLen  = 256
	explorerReadModelCursorMaxLen  = 2048
)

type explorerSpotOrderEntry struct {
	OrderID      string `json:"order_id"`
	Pair         string `json:"pair"`
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

type explorerFutureOrderEntry struct {
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

type explorerFuturePositionEntry struct {
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
	RealizedPnl           string `json:"realized_pnl"`
	ADLWithheldPnl        string `json:"adl_withheld_pnl"`
	ClosedQty             string `json:"closed_qty"`
	ExitNotional          string `json:"exit_notional"`
	MaxQuantity           string `json:"max_quantity"`
	InitialMargin         string `json:"initial_margin"`
	CloseHeight           uint64 `json:"close_height"`
}

func (s *server) handleSpotOrdersReadModel(w http.ResponseWriter, r *http.Request) {
	address, pair, status, limit, ok := parseOrderReadModelQuery(w, r)
	if !ok {
		return
	}
	if s.indexDB == nil {
		writeReadModelUnavailable(w)
		return
	}
	rows, err := s.indexDB.ScanSpotAccountOrders(address, pair, status, limit)
	if err != nil {
		writeReadModelError(w, err)
		return
	}
	writeExplorerJSON(w, http.StatusOK, map[string]any{"orders": materializeSpotOrders(rows, address, pair, status, limit)})
}

func (s *server) handleFutureOrdersReadModel(w http.ResponseWriter, r *http.Request) {
	address, pair, status, limit, ok := parseOrderReadModelQuery(w, r)
	if !ok {
		return
	}

	if s.indexDB == nil {
		writeReadModelUnavailable(w)
		return
	}
	rows, err := s.indexDB.ScanFutureAccountOrders(address, pair, status, limit)
	if err != nil {
		writeReadModelError(w, err)
		return
	}
	writeExplorerJSON(w, http.StatusOK, map[string]any{"orders": materializeFutureOrders(rows, address, pair, status, limit)})
}

                                                                              
                                                                            
                                                                            
func (s *server) handleFuturePositionsReadModel(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	owner := strings.TrimSpace(query.Get("owner"))
	pair := strings.TrimSpace(query.Get("pair"))
	status := strings.TrimSpace(query.Get("status"))
	cursor := query.Get("cursor")
	if len(owner) > explorerReadModelAddressMaxLen || len(pair) > explorerReadModelFilterMaxLen ||
		len(status) > explorerReadModelFilterMaxLen || len(cursor) > explorerReadModelCursorMaxLen {
		writeExplorerJSON(w, http.StatusBadRequest, APIError{Error: "read-model query parameter is too long", Code: "INVALID_PARAMS"})
		return
	}
	if s.indexDB == nil {
		writeReadModelUnavailable(w)
		return
	}

	limit := boundedReadModelInt(query.Get("limit"), 10, 1, explorerReadModelPageMax)
	page, err := s.indexDB.GetFuturePositionHistoryPage(owner, pair, status, cursor, limit)
	if err != nil {
		switch {
		case errors.Is(err, indexdb.ErrInvalidFuturePositionCursor):
			writeExplorerJSON(w, http.StatusBadRequest, APIError{Error: "invalid cursor", Code: "INVALID_CURSOR"})
		case errors.Is(err, indexdb.ErrInvalidFuturePositionQuery):
			writeExplorerJSON(w, http.StatusBadRequest, APIError{Error: "invalid terminal position query", Code: "INVALID_PARAMS"})
		case errors.Is(err, indexdb.ErrFuturePositionHistoryUnavailable):
			writeReadModelUnavailable(w)
		default:
			writeReadModelError(w, err)
		}
		return
	}
	if page == nil {
		writeReadModelError(w, errors.New("nil future position history page"))
		return
	}

	positions := make([]explorerFuturePositionEntry, 0, len(page.Positions))
	for _, state := range page.Positions {
		if state == nil {
			writeReadModelError(w, errors.New("nil future position history row"))
			return
		}
		positions = append(positions, materializeFuturePosition(state))
	}
	writeExplorerJSON(w, http.StatusOK, map[string]any{
		"positions":   positions,
		"limit":       page.Limit,
		"has_more":    page.HasMore,
		"next_cursor": page.NextCursor,
	})
}

func (s *server) handleUserActivity(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	address := query.Get("address")
	kind := query.Get("kind")
	if address == "" {
		writeExplorerJSON(w, http.StatusBadRequest, APIError{Error: "missing address parameter", Code: "INVALID_PARAMS"})
		return
	}
	if len(address) > explorerReadModelAddressMaxLen || len(kind) > explorerReadModelFilterMaxLen {
		writeExplorerJSON(w, http.StatusBadRequest, APIError{Error: "read-model query parameter is too long", Code: "INVALID_PARAMS"})
		return
	}

	limit := boundedReadModelInt(query.Get("limit"), explorerActivityPageDefault, 1, explorerReadModelPageMax)
	offset := boundedReadModelInt(query.Get("offset"), 0, 0, explorerActivityOffsetMax)
	if s.indexDB == nil {
		writeReadModelUnavailable(w)
		return
	}
	page, err := s.indexDB.GetUserActivities(address, kind, limit, offset)
	if err != nil {
		writeReadModelError(w, err)
		return
	}
	if !validActivityItems(page.Items) {
		writeExplorerJSON(w, http.StatusInternalServerError, APIError{Error: "corrupt local user activity read model", Code: "READ_MODEL_ERROR"})
		return
	}
	items := page.Items
	if items == nil {
		items = make([]indexdb.UserActivityItem, 0)
	}
	if len(items) > limit {
		items = items[:limit]
	}
	writeExplorerJSON(w, http.StatusOK, map[string]any{
		"address":  address,
		"total":    page.Total,
		"has_more": page.HasMore,
		"offset":   offset,
		"limit":    limit,
		"items":    items,
	})
}

func parseOrderReadModelQuery(w http.ResponseWriter, r *http.Request) (address, pair, status string, limit int, ok bool) {
	query := r.URL.Query()
	address = query.Get("address")
	pair = query.Get("pair")
	status = strings.ToLower(query.Get("status"))
	if address == "" {
		writeExplorerJSON(w, http.StatusBadRequest, APIError{Error: "missing address parameter", Code: "INVALID_PARAMS"})
		return "", "", "", 0, false
	}
	if len(address) > explorerReadModelAddressMaxLen || len(pair) > explorerReadModelFilterMaxLen || len(status) > explorerReadModelFilterMaxLen {
		writeExplorerJSON(w, http.StatusBadRequest, APIError{Error: "read-model query parameter is too long", Code: "INVALID_PARAMS"})
		return "", "", "", 0, false
	}
	limit = boundedOrderReadModelLimit(query.Get("limit"))
	return address, pair, status, limit, true
}

func boundedOrderReadModelLimit(raw string) int {
	value, err := strconv.Atoi(raw)
	if raw == "" || err != nil || value <= 0 {
		return explorerOrderPageDefault
	}
	if value > explorerReadModelPageMax {
		return explorerReadModelPageMax
	}
	return value
}

func boundedReadModelInt(raw string, fallback, minimum, maximum int) int {
	value, err := strconv.Atoi(raw)
	if raw == "" || err != nil {
		return fallback
	}
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func materializeSpotOrders(rows map[string][]byte, address, pair, status string, limit int) []explorerSpotOrderEntry {
	keys := sortedReadModelKeys(rows)
	orders := make([]explorerSpotOrderEntry, 0, minInt(len(keys), limit))
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		var state pb.OrderState
		if err := proto.Unmarshal(rows[key], &state); err != nil || state.OrderId == "" || state.Owner != address {
			continue
		}
		orderPair := state.BaseToken + "_" + state.QuoteToken
		orderStatus := explorerSpotOrderStatus(state.Status)
		if (pair != "" && orderPair != pair) || !readModelStatusMatches(status, orderStatus) {
			continue
		}
		if _, exists := seen[state.OrderId]; exists {
			continue
		}
		seen[state.OrderId] = struct{}{}
		side := "buy"
		if state.Side == pb.OrderSide_SELL {
			side = "sell"
		}
		orders = append(orders, explorerSpotOrderEntry{
			OrderID: state.OrderId, Pair: orderPair, Side: side, Price: state.Price,
			Amount: state.Amount, FilledBase: state.FilledBase, FilledQuote: state.FilledQuote,
			IsFilled: state.IsFilled, Status: orderStatus, Owner: state.Owner, CreateHeight: state.CreateHeight,
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
	return orders
}

func materializeFutureOrders(rows map[string][]byte, address, pair, status string, limit int) []explorerFutureOrderEntry {
	keys := sortedReadModelKeys(rows)
	orders := make([]explorerFutureOrderEntry, 0, minInt(len(keys), limit))
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		var state pb.FutureOrderState
		if err := proto.Unmarshal(rows[key], &state); err != nil || state.OrderId == "" {
			continue
		}
		owner := state.OwnerAddress
		if owner == "" {
			owner = state.Owner
		}
		if owner != address {
			continue
		}
		orderStatus := explorerFutureOrderStatus(state.Status)
		if (pair != "" && state.Pair != pair) || !readModelStatusMatches(status, orderStatus) {
			continue
		}
		if _, exists := seen[state.OrderId]; exists {
			continue
		}
		seen[state.OrderId] = struct{}{}
		side := "buy"
		if state.Side == 2 {
			side = "sell"
		}
		orders = append(orders, explorerFutureOrderEntry{
			OrderID: state.OrderId, Pair: state.Pair, Side: side, Price: state.Price,
			Amount: state.Amount, FilledBase: state.FilledBase, FilledQuote: explorerFutureFilledQuote(&state),
			Leverage: state.Leverage, IsClose: state.IsClose, Status: orderStatus,
			Owner: owner, CreateHeight: state.CreateHeight,
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
	return orders
}

func materializeFuturePosition(state *pb.FuturePositionState) explorerFuturePositionEntry {
	return explorerFuturePositionEntry{
		PositionID:            state.PositionId,
		Owner:                 state.Owner,
		Pair:                  state.Pair,
		Side:                  explorerFuturePositionSide(state.Side),
		EntryPrice:            state.EntryPrice,
		Quantity:              state.Quantity,
		Margin:                state.Margin,
		Status:                explorerFuturePositionStatus(state.Status),
		OpenHeight:            state.OpenHeight,
		LastCumulativeFunding: state.LastCumulativeFunding,
		SettledFunding:        state.SettledFunding,
		LastSettledFunding:    state.LastSettledFunding,
		RealizedPnl:           state.RealizedPnl,
		ADLWithheldPnl:        state.AdlWithheldPnl,
		ClosedQty:             state.ClosedQty,
		ExitNotional:          state.ExitNotional,
		MaxQuantity:           state.MaxQuantity,
		InitialMargin:         state.InitialMargin,
		CloseHeight:           state.CloseHeight,
	}
}

func explorerFuturePositionSide(side int32) string {
	if future.Side(side) == future.Long {
		return "LONG"
	}
	return "SHORT"
}

func explorerFuturePositionStatus(status int32) string {
	switch future.PositionStatus(status) {
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

func sortedReadModelKeys(rows map[string][]byte) []string {
	keys := make([]string, 0, len(rows))
	for key := range rows {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func readModelStatusMatches(filter, actual string) bool {
	return filter == "" || filter == "all" || filter == actual
}

func explorerSpotOrderStatus(status pb.OrderStateStatus) string {
	switch status {
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

func explorerFutureOrderStatus(status pb.FutureOrderStatus) string {
	switch status {
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

func explorerFutureFilledQuote(state *pb.FutureOrderState) string {
	if state.FilledQuote != "" && state.FilledQuote != "0" {
		return state.FilledQuote
	}
	filled, filledOK := new(big.Int).SetString(state.FilledBase, 10)
	price, priceOK := new(big.Int).SetString(state.Price, 10)
	if !filledOK || !priceOK || filled == nil || price == nil {
		return "0"
	}
	return future.CalcNotional(filled, price).String()
}

func validActivityItems(items []indexdb.UserActivityItem) bool {
	for _, item := range items {
		if raw := item.Record; len(raw) == 0 || !json.Valid(raw) {
			return false
		}
	}
	return true
}

func writeReadModelUnavailable(w http.ResponseWriter) {
	writeExplorerJSON(w, http.StatusServiceUnavailable, APIError{Error: "local Explorer read model is unavailable", Code: "READ_MODEL_UNAVAILABLE"})
}

func writeReadModelError(w http.ResponseWriter, err error) {
	writeExplorerJSON(w, http.StatusInternalServerError, APIError{Error: "failed to query local Explorer read model", Code: "READ_MODEL_ERROR", Details: err.Error()})
}

func writeExplorerJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
