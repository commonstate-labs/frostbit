package main

import (
	"dex/cmd/explorer/indexdb"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleSyncStatusReportsPartialHistory(t *testing.T) {
	idb, err := indexdb.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idb.Close() })
	if err := idb.SetPartialHistoryBase(101, "source prefix pruned"); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	(&server{indexDB: idb}).handleSyncStatus(rec, httptest.NewRequest(http.MethodGet, "/api/sync/status", nil))
	var got struct {
		PartialHistory bool   `json:"partial_history"`
		BaseHeight     uint64 `json:"base_height"`
		PartialReason  string `json:"partial_reason"`
		HistoryError   string `json:"history_error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.PartialHistory || got.BaseHeight != 101 || got.PartialReason != "source prefix pruned" || got.HistoryError != "" {
		t.Fatalf("unexpected sync history status: %+v", got)
	}
}
