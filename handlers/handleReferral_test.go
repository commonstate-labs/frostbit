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
	"net/url"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
)

func TestHandleReferralRebatesCursorPagesDiscountHistory(t *testing.T) {
	hm, dbManager := newReferralTestHandler(t)
	for height := uint64(1); height <= 3; height++ {
		putReferralFeeRecord(t, dbManager, keys.KeyTradingFeeRecord("alice", height, fmt.Sprintf("tx-%d", height), 0), &pb.TradingFeeRecord{
			Trader: "alice", Pair: "FB_USDT", Notional: "100", FullFee: "10",
			ActualFee: "9", ReferrerRebate: "0", Height: height, TxId: fmt.Sprintf("tx-%d", height),
		})
	}
	flushReferralTestDB(t, dbManager)

	first := requestReferralPage(t, hm, "/referral/rebates?address=alice&kind=discount&limit=2")
	if len(first.Records) != 2 || first.Records[0].Height != 3 || first.Records[1].Height != 2 {
		t.Fatalf("unexpected first page: %+v", first.Records)
	}
	if !first.HasMore || first.NextCursor == "" {
		t.Fatalf("first page must expose a next cursor: %+v", first)
	}

	second := requestReferralPage(t, hm, "/referral/rebates?address=alice&kind=discount&limit=2&cursor="+url.QueryEscape(first.NextCursor))
	if len(second.Records) != 1 || second.Records[0].Height != 1 {
		t.Fatalf("unexpected second page: %+v", second.Records)
	}
	if second.HasMore || second.NextCursor != "" {
		t.Fatalf("second page must be terminal: %+v", second)
	}
}

func TestHandleReferralInfoBoundsInviteePayload(t *testing.T) {
	hm, dbManager := newReferralTestHandler(t)
	updates := make([]interface{}, 0, maxReferralInfoInvitees+1)
	for i := 0; i <= maxReferralInfoInvitees; i++ {
		updates = append(updates, referralStateWrite{
			key: keys.KeyReferrer(fmt.Sprintf("invitee-%03d", i)), value: []byte("bob"),
		})
	}
	if _, err := dbManager.SyncStateUpdates(1, updates); err != nil {
		t.Fatalf("write referral bindings: %v", err)
	}
	if err := dbManager.RebuildReferralInviteeIndex(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/referral/info?address=bob", nil)
	recorder := httptest.NewRecorder()
	hm.HandleReferralInfo(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response referralInfo
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Invitees) != maxReferralInfoInvitees || !response.HasMore {
		t.Fatalf("invitee response is not bounded: %+v", response)
	}
}

func TestHandleReferralRebatesUsesReferrerReverseIndex(t *testing.T) {
	hm, dbManager := newReferralTestHandler(t)
	for height := uint64(8); height <= 9; height++ {
		record := &pb.TradingFeeRecord{
			Trader: fmt.Sprintf("invitee-%d", height), Referrer: "bob", Pair: "FB_USDT",
			Notional: "100", FullFee: "10", ActualFee: "9", ReferrerRebate: "2",
			Height: height, TxId: fmt.Sprintf("tx-%d", height),
		}
		putReferralFeeRecord(t, dbManager, keys.KeyReferralRebateRecord("bob", height, record.TxId, 0), record)
	}
	flushReferralTestDB(t, dbManager)

	page := requestReferralPage(t, hm, "/referral/rebates?address=bob&kind=rebate&limit=1")
	if len(page.Records) != 1 || page.Records[0].Height != 9 || page.Records[0].Trader != "invitee-9" {
		t.Fatalf("unexpected rebate page: %+v", page.Records)
	}
	if !page.HasMore || page.NextCursor == "" {
		t.Fatalf("unexpected rebate pagination metadata: %+v", page)
	}
}

func TestHandleReferralRebatesRejectsForeignCursor(t *testing.T) {
	hm, _ := newReferralTestHandler(t)
	foreignCursor := encodeReferralPageCursor(keys.KeyTradingFeeRecord("mallory", 10, "tx-10", 0))
	req := httptest.NewRequest(http.MethodGet, "/referral/rebates?address=alice&kind=discount&cursor="+url.QueryEscape(foreignCursor), nil)
	recorder := httptest.NewRecorder()
	hm.HandleReferralRebates(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestHandleReferralRebatesRequiresKind(t *testing.T) {
	hm, _ := newReferralTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/referral/rebates?address=alice", nil)
	recorder := httptest.NewRecorder()
	hm.HandleReferralRebates(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func newReferralTestHandler(t *testing.T) (*HandlerManager, *db.Manager) {
	t.Helper()
	dbManager, err := db.NewManager(t.TempDir(), logs.NewNodeLogger("test", 0))
	if err != nil {
		t.Fatalf("new db manager: %v", err)
	}
	t.Cleanup(func() { dbManager.Close() })
	dbManager.InitWriteQueue(128, time.Hour)
	return &HandlerManager{dbManager: dbManager}, dbManager
}

func putReferralFeeRecord(t *testing.T, dbManager *db.Manager, key string, record *pb.TradingFeeRecord) {
	t.Helper()
	data, err := proto.Marshal(record)
	if err != nil {
		t.Fatalf("marshal fee record: %v", err)
	}
	dbManager.EnqueueSet(key, string(data))
}

func flushReferralTestDB(t *testing.T, dbManager *db.Manager) {
	t.Helper()
	if err := dbManager.ForceFlush(); err != nil {
		t.Fatalf("force flush: %v", err)
	}
}

func requestReferralPage(t *testing.T, hm *HandlerManager, target string) rebateResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	recorder := httptest.NewRecorder()
	hm.HandleReferralRebates(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response rebateResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return response
}

type referralStateWrite struct {
	key   string
	value []byte
}

func (write referralStateWrite) GetKey() string   { return write.key }
func (write referralStateWrite) GetValue() []byte { return write.value }
func (write referralStateWrite) IsDel() bool      { return false }
