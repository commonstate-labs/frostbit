package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"dex/db"
	"dex/future"
	"dex/keys"
	"dex/logs"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

func TestInsuredDeleveragedPositionStatusString(t *testing.T) {
	const want = "INSURED_DELEVERAGED"
	if got := statusStr(int32(future.StatusInsuredDeleveraged)); got != want {
		t.Fatalf("status string=%q, want %q", got, want)
	}
}

func TestHandleFuturePositionsRejectsExplorerOwnedHistory(t *testing.T) {
	hm := &HandlerManager{}
	for _, path := range []string{
		"/future/positions?owner=alice&status=closed",
		"/future/positions?owner=alice&status=LIQUIDATED",
		"/future/positions?owner=alice&status=all",
		"/future/positions?owner=alice",
	} {
		recorder := httptest.NewRecorder()
		hm.HandleFuturePositions(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusBadRequest || recorder.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("path=%s status=%d content-type=%q body=%s", path, recorder.Code, recorder.Header().Get("Content-Type"), recorder.Body.String())
		}
	}
}

func TestHandleFuturePositionsStillServesOpenConsensusState(t *testing.T) {
	dbManager, err := db.NewManager(t.TempDir(), logs.NewNodeLogger("test", 0))
	if err != nil {
		t.Fatal(err)
	}
	defer dbManager.Close()
	dbManager.InitWriteQueue(32, time.Hour)

	state := &pb.FuturePositionState{
		PositionId: "open-1", Owner: "alice", Pair: "BTC_USDT", Side: int32(future.Long),
		EntryPrice: "100", Quantity: "2", Margin: "10", Status: int32(future.StatusOpen), OpenHeight: 7,
	}
	raw, err := proto.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	dbManager.EnqueueSet(keys.KeyFutureOwnerPosition("alice", state.PositionId), string(raw))
	if err := dbManager.ForceFlush(); err != nil {
		t.Fatal(err)
	}

	hm := &HandlerManager{dbManager: dbManager}
	recorder := httptest.NewRecorder()
	hm.HandleFuturePositions(recorder, httptest.NewRequest(http.MethodGet, "/future/positions?owner=alice&status=OPEN&limit=10", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response futurePositionsResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if len(response.Positions) != 1 || response.Positions[0].PositionID != state.PositionId || response.Positions[0].Status != "OPEN" {
		t.Fatalf("positions=%+v", response.Positions)
	}
}
