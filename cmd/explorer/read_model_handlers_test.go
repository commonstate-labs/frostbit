package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"dex/cmd/explorer/indexdb"
	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

func TestHandleMyOrdersUsesCompleteLocalPage(t *testing.T) {
	idb, err := indexdb.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idb.Close() })

	for _, state := range []*pb.OrderState{
		{OrderId: "older", Owner: "alice", BaseToken: "FB", QuoteToken: "ETH.USDT", Status: pb.OrderStateStatus_ORDER_OPEN, Side: pb.OrderSide_BUY, Price: "100", Amount: "2", CreateHeight: 10},
		{OrderId: "newer", Owner: "alice", BaseToken: "FB", QuoteToken: "ETH.USDT", Status: pb.OrderStateStatus_ORDER_OPEN, Side: pb.OrderSide_SELL, Price: "101", Amount: "3", CreateHeight: 11},
	} {
		data, marshalErr := proto.Marshal(state)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if err := idb.Set(keys.KeySpotAccountOrderFromState(state), string(data)); err != nil {
			t.Fatal(err)
		}
	}

	srv := &server{indexDB: idb}
	recorder := httptest.NewRecorder()
	srv.handleMyOrders(recorder, httptest.NewRequest(http.MethodGet, "/api/myorders?address=alice&pair=FB_ETH.USDT&status=open&limit=2", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Orders []explorerSpotOrderEntry `json:"orders"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Orders) != 2 || response.Orders[0].OrderID != "newer" || response.Orders[1].OrderID != "older" {
		t.Fatalf("unexpected local orders: %+v", response.Orders)
	}
}

func TestHandleMyOrdersReturnsShortLocalPageWithoutNodeFallback(t *testing.T) {
	idb, err := indexdb.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idb.Close() })

	srv := &server{indexDB: idb}
	recorder := httptest.NewRecorder()
	srv.handleMyOrders(recorder, httptest.NewRequest(http.MethodGet, "/api/myorders?node=unreachable.invalid&address=alice&limit=999", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Orders []explorerSpotOrderEntry `json:"orders"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Orders) != 0 {
		t.Fatalf("orders = %+v, want an empty local page", response.Orders)
	}
	if got := boundedOrderReadModelLimit("999"); got != explorerReadModelPageMax {
		t.Fatalf("bounded limit = %d, want %d", got, explorerReadModelPageMax)
	}
	if got := boundedOrderReadModelLimit("0"); got != explorerOrderPageDefault {
		t.Fatalf("zero limit = %d, want default %d", got, explorerOrderPageDefault)
	}
}

func TestHandleFutureMyOrdersUsesLocalReadModel(t *testing.T) {
	idb, err := indexdb.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idb.Close() })
	state := &pb.FutureOrderState{
		OrderId: "future-1", Owner: "alice", Pair: "FB_ETH.USDT",
		Status: pb.FutureOrderStatus_FUTURE_ORDER_FILLED, Side: 2,
		Price: "200000000", Amount: "4", FilledBase: "3", FilledQuote: "9",
		Leverage: 5, IsClose: true, CreateHeight: 12,
	}
	data, err := proto.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := idb.Set(keys.KeyFutureAccountOrderFromState(state), string(data)); err != nil {
		t.Fatal(err)
	}

	srv := &server{indexDB: idb}
	recorder := httptest.NewRecorder()
	srv.handleFutureMyOrders(recorder, httptest.NewRequest(http.MethodGet, "/api/future/myorders?address=alice&pair=FB_ETH.USDT&status=filled&limit=1", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Orders []explorerFutureOrderEntry `json:"orders"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Orders) != 1 || response.Orders[0].OrderID != "future-1" || response.Orders[0].FilledQuote != "9" {
		t.Fatalf("unexpected local futures orders: %+v", response.Orders)
	}
}

func TestHandleFuturePositionsUsesLocalReadModel(t *testing.T) {
	idb, err := indexdb.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idb.Close() })
	for key, value := range map[string]string{
		indexdb.KeyReadModelSchemaVersion: strconv.FormatUint(uint64(pb.BlockReadModelSchemaVersion), 10),
		indexdb.KeyReadModelBaseHeight:    "1",
		indexdb.KeyReadModelTipHeight:     "12",
	} {
		if err := idb.Set(key, value); err != nil {
			t.Fatal(err)
		}
	}

	state := &pb.FuturePositionState{
		PositionId: "position-1", Owner: "alice", Pair: "FB_ETH.USDT",
		Side: 1, EntryPrice: "200000000", Quantity: "0", Margin: "0",
		Status: 2, OpenHeight: 7, CloseHeight: 12,
		LastCumulativeFunding: "3", SettledFunding: "4", LastSettledFunding: "1",
		RealizedPnl: "9", AdlWithheldPnl: "2", ClosedQty: "5", ExitNotional: "10",
		MaxQuantity: "5", InitialMargin: "6",
	}
	raw, err := proto.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := idb.Set(keys.KeyFuturePositionHistory(state.PositionId), string(raw)); err != nil {
		t.Fatal(err)
	}
	if err := idb.Set(keys.KeyFuturePositionHistoryOwnerIndex(state.Owner, state.Status, state.CloseHeight, state.PositionId), state.PositionId); err != nil {
		t.Fatal(err)
	}

	srv := &server{indexDB: idb}
	recorder := httptest.NewRecorder()
	srv.handleFuturePositionsReadModel(recorder, httptest.NewRequest(
		http.MethodGet,
		"/api/future/positions?node=unreachable.invalid&owner=alice&status=closed&cursor=&limit=10",
		nil,
	))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Positions  []explorerFuturePositionEntry `json:"positions"`
		Limit      int                           `json:"limit"`
		HasMore    bool                          `json:"has_more"`
		NextCursor string                        `json:"next_cursor"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Positions) != 1 || response.Positions[0].PositionID != state.PositionId ||
		response.Positions[0].Side != "LONG" || response.Positions[0].Status != "CLOSED" ||
		response.Positions[0].RealizedPnl != "9" || response.Positions[0].ADLWithheldPnl != "2" ||
		response.Limit != 10 || response.HasMore || response.NextCursor != "" {
		t.Fatalf("unexpected local futures position page: %+v", response)
	}

	recorder = httptest.NewRecorder()
	srv.handleFuturePositionsReadModel(recorder, httptest.NewRequest(
		http.MethodGet,
		"/api/future/positions?owner=alice&status=closed&cursor=not-base64!&limit=10",
		nil,
	))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid cursor status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestHandleFuturePositionsRequiresReadModelCoverage(t *testing.T) {
	idb, err := indexdb.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idb.Close() })

	srv := &server{indexDB: idb}
	recorder := httptest.NewRecorder()
	srv.handleFuturePositionsReadModel(recorder, httptest.NewRequest(
		http.MethodGet,
		"/api/future/positions?owner=alice&status=closed&cursor=&limit=10",
		nil,
	))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response APIError
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Code != "READ_MODEL_UNAVAILABLE" {
		t.Fatalf("error = %+v", response)
	}
}

func TestHandleUserActivityUsesCompleteLocalPage(t *testing.T) {
	idb, err := indexdb.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idb.Close() })
	for i, height := range []uint64{10, 11} {
		record := `{"kind":"FEE","height":` + strconv.FormatUint(height, 10) + `}`
		if err := idb.Set(keys.KeyUserActivity("alice", height, uint32(i)), record); err != nil {
			t.Fatal(err)
		}
	}

	srv := &server{indexDB: idb}
	recorder := httptest.NewRecorder()
	srv.handleUserActivity(recorder, httptest.NewRequest(http.MethodGet, "/api/user/activity?address=alice&limit=2", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Items []indexdb.UserActivityItem `json:"items"`
		Limit int                        `json:"limit"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Limit != 2 || len(response.Items) != 2 || !strings.Contains(string(response.Items[0].Record), `"height":11`) {
		t.Fatalf("unexpected local activity response: %+v", response)
	}
}
