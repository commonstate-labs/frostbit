package handlers

import (
	"dex/db"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
)

func TestHandleFutureMyOrdersUsesPairStatusHeightIndex(t *testing.T) {
	dbMgr, err := db.NewManager(t.TempDir(), logs.NewNodeLogger("test", 0))
	if err != nil {
		t.Fatalf("new db manager: %v", err)
	}
	defer dbMgr.Close()
	dbMgr.InitWriteQueue(128, time.Hour)
	hm := &HandlerManager{dbManager: dbMgr}

	owner := "alice"
	for i := 0; i < 220; i++ {
		putFutureOrderHistory(t, dbMgr, &pb.FutureOrderState{
			OrderId: fmt.Sprintf("0x%064x", i), Owner: owner, OwnerAddress: owner,
			Pair: "OTHER_USDT", Side: 1, Price: "100", Amount: "1", FilledBase: "0",
			Status: pb.FutureOrderStatus_FUTURE_ORDER_OPEN, CreateHeight: uint64(i + 1),
		})
	}
	putFutureOrderHistory(t, dbMgr, &pb.FutureOrderState{
		OrderId: "0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff01", Owner: owner, OwnerAddress: owner,
		Pair: "FB_ETH.USDT", Side: 1, Price: "100", Amount: "1", FilledBase: "1",
		Status: pb.FutureOrderStatus_FUTURE_ORDER_FILLED, CreateHeight: 300,
	})
	putFutureOrderHistory(t, dbMgr, &pb.FutureOrderState{
		OrderId: "0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff02", Owner: owner, OwnerAddress: owner,
		Pair: "FB_ETH.USDT", Side: 1, Price: "100", Amount: "1", FilledBase: "1",
		Status: pb.FutureOrderStatus_FUTURE_ORDER_FILLED, CreateHeight: 301,
	})
	if err := dbMgr.ForceFlush(); err != nil {
		t.Fatalf("force flush: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/future/myorders?address=alice&pair=FB_ETH.USDT&status=filled&limit=1", nil)
	rec := httptest.NewRecorder()
	hm.HandleFutureMyOrders(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp FutureMyOrdersResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Orders) != 1 || resp.Orders[0].OrderID != "0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff02" {
		t.Fatalf("orders=%+v", resp.Orders)
	}

	allPairsReq := httptest.NewRequest(http.MethodGet, "/future/myorders?address=alice&status=all&limit=1", nil)
	allPairsRec := httptest.NewRecorder()
	hm.HandleFutureMyOrders(allPairsRec, allPairsReq)
	var allPairs FutureMyOrdersResponse
	if err := json.NewDecoder(allPairsRec.Body).Decode(&allPairs); err != nil {
		t.Fatalf("decode owner-wide response: %v", err)
	}
	if len(allPairs.Orders) != 1 || allPairs.Orders[0].CreateHeight != 301 {
		t.Fatalf("owner-wide orders=%+v", allPairs.Orders)
	}
}

func TestHandleMyOrdersUsesPairStatusHeightIndex(t *testing.T) {
	dbMgr, err := db.NewManager(t.TempDir(), logs.NewNodeLogger("test", 0))
	if err != nil {
		t.Fatalf("new db manager: %v", err)
	}
	defer dbMgr.Close()
	dbMgr.InitWriteQueue(128, time.Hour)
	hm := &HandlerManager{dbManager: dbMgr}

	owner := "alice"
	for i := 0; i < 220; i++ {
		putSpotOrderHistory(t, dbMgr, &pb.OrderState{
			OrderId: fmt.Sprintf("0x%064x", i), Owner: owner,
			BaseToken: "OTHER", QuoteToken: "USDT", Side: pb.OrderSide_BUY,
			Price: "100", Amount: "1", FilledBase: "0", FilledQuote: "0",
			Status: pb.OrderStateStatus_ORDER_OPEN, CreateHeight: uint64(i + 1),
		})
	}
	putSpotOrderHistory(t, dbMgr, &pb.OrderState{
		OrderId: "0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff01", Owner: owner,
		BaseToken: "FB", QuoteToken: "ETH.USDT", Side: pb.OrderSide_BUY,
		Price: "100", Amount: "1", FilledBase: "1", FilledQuote: "100",
		Status: pb.OrderStateStatus_ORDER_FILLED, CreateHeight: 300,
	})
	putSpotOrderHistory(t, dbMgr, &pb.OrderState{
		OrderId: "0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff02", Owner: owner,
		BaseToken: "FB", QuoteToken: "ETH.USDT", Side: pb.OrderSide_BUY,
		Price: "100", Amount: "1", FilledBase: "1", FilledQuote: "100",
		Status: pb.OrderStateStatus_ORDER_FILLED, CreateHeight: 301,
	})
	if err := dbMgr.ForceFlush(); err != nil {
		t.Fatalf("force flush: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/myorders?address=alice&pair=FB_ETH.USDT&status=filled&limit=1", nil)
	rec := httptest.NewRecorder()
	hm.HandleMyOrders(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp MyOrdersResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Orders) != 1 || resp.Orders[0].OrderID != "0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff02" {
		t.Fatalf("orders=%+v", resp.Orders)
	}

	allPairsReq := httptest.NewRequest(http.MethodGet, "/myorders?address=alice&status=all&limit=1", nil)
	allPairsRec := httptest.NewRecorder()
	hm.HandleMyOrders(allPairsRec, allPairsReq)
	var allPairs MyOrdersResponse
	if err := json.NewDecoder(allPairsRec.Body).Decode(&allPairs); err != nil {
		t.Fatalf("decode owner-wide response: %v", err)
	}
	if len(allPairs.Orders) != 1 || allPairs.Orders[0].CreateHeight != 301 {
		t.Fatalf("owner-wide orders=%+v", allPairs.Orders)
	}
}

func putFutureOrderHistory(t *testing.T, dbMgr *db.Manager, st *pb.FutureOrderState) {
	t.Helper()
	data, err := proto.Marshal(st)
	if err != nil {
		t.Fatalf("marshal order: %v", err)
	}
	primaryKey := keys.KeyFutureAccountOrderFromState(st)
	dbMgr.EnqueueSet(primaryKey, string(data))
	dbMgr.EnqueueSet(keys.KeyFutureAccountOrderRecentFromState(st), primaryKey)
}

func putSpotOrderHistory(t *testing.T, dbMgr *db.Manager, st *pb.OrderState) {
	t.Helper()
	data, err := proto.Marshal(st)
	if err != nil {
		t.Fatalf("marshal order: %v", err)
	}
	primaryKey := keys.KeySpotAccountOrderFromState(st)
	dbMgr.EnqueueSet(primaryKey, string(data))
	dbMgr.EnqueueSet(keys.KeySpotAccountOrderRecentFromState(st), primaryKey)
}
