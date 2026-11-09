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

func newCertQueryTestHandler(t *testing.T) (*HandlerManager, *db.Manager) {
	t.Helper()
	dbManager, err := db.NewManager(t.TempDir(), logs.NewNodeLogger("cert-query-test", 0))
	if err != nil {
		t.Fatalf("new db manager: %v", err)
	}
	dbManager.InitWriteQueue(4096, time.Hour)
	return &HandlerManager{dbManager: dbManager}, dbManager
}

func enqueueCertProto(t *testing.T, dbManager *db.Manager, key string, message proto.Message) {
	t.Helper()
	raw, err := proto.Marshal(message)
	if err != nil {
		t.Fatalf("marshal %s: %v", key, err)
	}
	dbManager.EnqueueSet(key, string(raw))
}

func TestHandleGetCertChainUsesBoundedHeightPage(t *testing.T) {
	hm, dbManager := newCertQueryTestHandler(t)
	defer dbManager.Close()
	for _, height := range []uint64{10, 20, 30, 40, 50} {
		dbManager.EnqueueSet(keys.KeyCertCertificate(height), fmt.Sprintf("cert-%d", height))
		                                                                      
		dbManager.EnqueueSet(keys.KeyCertSignature(height, 1), "signature")
	}
	if err := dbManager.ForceFlush(); err != nil {
		t.Fatalf("force flush: %v", err)
	}

	recorder := httptest.NewRecorder()
	hm.HandleGetCertChain(recorder, httptest.NewRequest(http.MethodGet, "/cert/chain?from=15&to=50&limit=2", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var page struct {
		Certs    []certEntry `json:"certs"`
		Count    int         `json:"count"`
		HasMore  bool        `json:"has_more"`
		NextFrom uint64      `json:"next_from"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if page.Count != 2 || len(page.Certs) != 2 || page.Certs[0].Height != 20 || page.Certs[1].Height != 30 {
		t.Fatalf("unexpected page: %+v", page)
	}
	if !page.HasMore || page.NextFrom != 31 {
		t.Fatalf("unexpected cursor: %+v", page)
	}
}

func TestHandleGetCertSummaryReadsRecentTypedWindows(t *testing.T) {
	hm, dbManager := newCertQueryTestHandler(t)
	defer dbManager.Close()
	for _, height := range []uint64{10, 20, 30} {
		enqueueCertProto(t, dbManager, keys.KeyCertCertificate(height), &pb.CheckpointCertificate{
			Height: height, SignerCount: 3, MinerCount: 4, StateRoot: []byte{byte(height)},
		})
		                                                                           
		enqueueCertProto(t, dbManager, keys.KeyCertPendingCheckpoint(height), &pb.CheckpointCertificate{
			Height: height, MinerCount: 4,
		})
	}
	enqueueCertProto(t, dbManager, keys.KeyCertPendingCheckpoint(40), &pb.CheckpointCertificate{
		Height: 40, MinerCount: 3, StateRoot: []byte{40},
	})
	dbManager.EnqueueSet(keys.KeyCertSignature(40, 0), "sig-0")
	dbManager.EnqueueSet(keys.KeyCertSignature(40, 1), "sig-1")
	                                                                       
	                                                        
	for i := 0; i < 100; i++ {
		dbManager.EnqueueSet(keys.KeyCertSignature(1, uint32(i)), "old")
	}
	enqueueCertProto(t, dbManager, keys.KeyCertGlobal(), &pb.CertGlobalState{
		LastCheckpoint: 40, LastAssembledCert: 30,
	})
	if err := dbManager.ForceFlush(); err != nil {
		t.Fatalf("force flush: %v", err)
	}

	recorder := httptest.NewRecorder()
	hm.HandleGetCertSummary(recorder, httptest.NewRequest(http.MethodGet, "/cert/summary?limit=2", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var page struct {
		Certs          []certSummaryEntry `json:"certs"`
		Count          int                `json:"count"`
		LastCheckpoint uint64             `json:"last_checkpoint"`
		LastAssembled  uint64             `json:"last_assembled_cert"`
		HasMore        bool               `json:"has_more"`
		NextBefore     uint64             `json:"next_before"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if page.Count != 2 || page.Certs[0].Height != 30 || page.Certs[0].Status != "certified" ||
		page.Certs[1].Height != 40 || page.Certs[1].Status != "pending" || page.Certs[1].SignerCount != 2 {
		t.Fatalf("unexpected summary: %+v", page)
	}
	if page.LastCheckpoint != 40 || page.LastAssembled != 30 || !page.HasMore || page.NextBefore != 30 {
		t.Fatalf("unexpected summary metadata: %+v", page)
	}
}

func TestCertQueryLimitsAreHardCapped(t *testing.T) {
	if got := parseCertLimit("999999", defaultCertChainLimit, maxCertChainLimit); got != maxCertChainLimit {
		t.Fatalf("chain limit=%d want %d", got, maxCertChainLimit)
	}
	if got := parseCertLimit("999999", defaultCertSummaryLimit, maxCertSummaryLimit); got != maxCertSummaryLimit {
		t.Fatalf("summary limit=%d want %d", got, maxCertSummaryLimit)
	}
}
