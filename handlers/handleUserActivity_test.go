package handlers

import (
	"dex/db"
	"dex/keys"
	"dex/logs"
	"dex/stats"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type userActivityTestResponse struct {
	Total   int  `json:"total"`
	Offset  int  `json:"offset"`
	Limit   int  `json:"limit"`
	HasMore bool `json:"has_more"`
	Items   []struct {
		Key    string `json:"key"`
		Record struct {
			Kind   string `json:"kind"`
			Height uint64 `json:"height"`
		} `json:"record"`
	} `json:"items"`
}

func newUserActivityTestHandler(t *testing.T) (*HandlerManager, *db.Manager) {
	t.Helper()
	dbMgr, err := db.NewManager(t.TempDir(), logs.NewNodeLogger("test", 0))
	if err != nil {
		t.Fatalf("new db manager: %v", err)
	}
	dbMgr.InitWriteQueue(4096, time.Hour)
	return &HandlerManager{dbManager: dbMgr, Stats: stats.NewStats()}, dbMgr
}

func enqueueUserActivity(t *testing.T, dbMgr *db.Manager, owner, kind string, height uint64, seq uint32, withKindIndex bool) {
	t.Helper()
	canonicalKey := keys.KeyUserActivity(owner, height, seq)
	raw, err := json.Marshal(map[string]any{"kind": kind, "height": height, "tx_id": fmt.Sprintf("tx-%d", height)})
	if err != nil {
		t.Fatalf("marshal activity: %v", err)
	}
	dbMgr.EnqueueSet(canonicalKey, string(raw))
	if withKindIndex {
		dbMgr.EnqueueSet(keys.KeyUserActivityKindIndex(owner, kind, height, fmt.Sprintf("tx-%d", height), seq), canonicalKey)
	}
}

func requestUserActivity(t *testing.T, hm *HandlerManager, path string) userActivityTestResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	hm.HandleUserActivity(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response userActivityTestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return response
}

func TestHandleUserActivityUnfilteredScanGrowsWithPage(t *testing.T) {
	hm, dbMgr := newUserActivityTestHandler(t)
	defer dbMgr.Close()
	for height := uint64(1); height <= 505; height++ {
		enqueueUserActivity(t, dbMgr, "alice", "KIND", height, 0, true)
	}
	if err := dbMgr.ForceFlush(); err != nil {
		t.Fatalf("force flush: %v", err)
	}

	page := requestUserActivity(t, hm, "/user/activity?address=alice&offset=2&limit=3")
	if page.Total != 6 || page.Offset != 2 || page.Limit != 3 || !page.HasMore || len(page.Items) != 3 {
		t.Fatalf("page metadata=%+v", page)
	}
	if page.Items[0].Record.Height != 503 || page.Items[2].Record.Height != 501 {
		t.Fatalf("page order=%v", page.Items)
	}

	clamped := requestUserActivity(t, hm, "/user/activity?address=alice&limit=999")
	if clamped.Total != 501 || !clamped.HasMore || clamped.Limit != 500 || len(clamped.Items) != 500 || clamped.Items[0].Record.Height != 505 || clamped.Items[499].Record.Height != 6 {
		t.Fatalf("clamped page size/order=%d first=%d last=%d limit=%d", len(clamped.Items), clamped.Items[0].Record.Height, clamped.Items[len(clamped.Items)-1].Record.Height, clamped.Limit)
	}
}

func TestHandleUserActivityKindUsesSlimIndexWithoutFallback(t *testing.T) {
	hm, dbMgr := newUserActivityTestHandler(t)
	defer dbMgr.Close()
	for height := uint64(1); height <= 10; height++ {
		kind := "OTHER"
		if height%2 == 0 {
			kind = "TARGET"
		}
		enqueueUserActivity(t, dbMgr, "alice", kind, height, 0, true)
	}
	                                                                           
	                                                                  
	enqueueUserActivity(t, dbMgr, "alice", "TARGET", 100, 0, false)
	if err := dbMgr.ForceFlush(); err != nil {
		t.Fatalf("force flush: %v", err)
	}

	page := requestUserActivity(t, hm, "/user/activity?address=alice&kind=TARGET&offset=1&limit=2")
	if page.Total != 4 || !page.HasMore || len(page.Items) != 2 {
		t.Fatalf("page metadata=%+v", page)
	}
	if page.Items[0].Record.Height != 8 || page.Items[1].Record.Height != 6 {
		t.Fatalf("filtered page=%v", page.Items)
	}
	last := requestUserActivity(t, hm, "/user/activity?address=alice&kind=TARGET&offset=4&limit=2")
	if last.Total != 5 || last.HasMore || len(last.Items) != 1 || last.Items[0].Record.Height != 2 {
		t.Fatalf("last page=%+v", last)
	}
}
