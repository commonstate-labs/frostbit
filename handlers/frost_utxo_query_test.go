package handlers

import (
	"context"
	"dex/db"
	"dex/keys"
	"dex/pb"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type measuredFrostUTXOStore struct {
	store       frostUTXOStore
	scans       int
	scanLimits  []int
	batches     int
	batchSizes  []int
	lastKeys    []string
	afterScan   func()
	beforeBatch func()
}

func (s *measuredFrostUTXOStore) ScanKeysWithLimitOrderedAfter(prefix, after string, limit int, reverse bool) ([]string, error) {
	s.scans++
	s.scanLimits = append(s.scanLimits, limit)
	rows, err := s.store.ScanKeysWithLimitOrderedAfter(prefix, after, limit, reverse)
	if s.afterScan != nil {
		s.afterScan()
	}
	return rows, err
}

func (s *measuredFrostUTXOStore) GetKVs(keys []string) (map[string][]byte, error) {
	s.batches++
	s.batchSizes = append(s.batchSizes, len(keys))
	s.lastKeys = append([]string(nil), keys...)
	if s.beforeBatch != nil {
		s.beforeBatch()
	}
	return s.store.GetKVs(keys)
}

func enqueueFrostUTXO(t *testing.T, dbm *db.Manager, vault uint32, n int) string {
	t.Helper()
	txid := fmt.Sprintf("%064x", n)
	key := keys.KeyFrostBtcUtxo(vault, txid, 0)
	enqueueProto(t, dbm, key, &pb.FrostUtxo{
		Txid: txid, Vout: 0, Amount: "100000", VaultId: vault, FinalizeHeight: 123,
		RequestId: fmt.Sprintf("recharge-%d", n), ScriptPubkey: []byte("private-script"), Tweak: []byte("private-tweak"),
	})
	return key
}

func flushFrostUTXOs(t *testing.T, dbm *db.Manager) {
	t.Helper()
	if err := dbm.ForceFlush(); err != nil {
		t.Fatal(err)
	}
}

func TestFrostVaultUTXOPagesBoundariesAndTraversal(t *testing.T) {
	hm, dbm := newFrostQueryTestHandler(t)
	defer dbm.Close()
	for vault, count := range []int{0, 49, 50, 51, 199, 200, 201} {
		for n := 0; n < count; n++ {
			enqueueFrostUTXO(t, dbm, uint32(vault), n)
		}
	}
	dbm.EnqueueSet(keys.KeyFrostBtcLockedUtxo(3, fmt.Sprintf("%064x", 0), 0), "withdraw-job")
	flushFrostUTXOs(t, dbm)
	for vault, count := range []int{0, 49, 50, 51, 199, 200, 201} {
		limit := 50
		if vault >= 4 {
			limit = 200
		}
		store := &measuredFrostUTXOStore{store: dbm}
		after, total, previous := "", 0, ""
		for {
			page, err := queryFrostVaultUTXOs(context.Background(), store, uint32(vault), limit, after)
			if err != nil {
				t.Fatalf("vault=%d: %v", vault, err)
			}
			if page.Items == nil || len(page.Items) > limit || page.HasMore != (total+limit < count) {
				t.Fatalf("vault=%d total=%d page=%+v", vault, total, page)
			}
			for _, item := range page.Items {
				if item.TxID <= previous || item.Amount != "100000" || item.FinalizeHeight != 123 {
					t.Fatalf("invalid row %+v", item)
				}
				if vault == 3 && total == 0 && item.LockJobID != "withdraw-job" {
					t.Fatalf("reservation lost: %+v", item)
				}
				previous = item.TxID
				total++
			}
			if !page.HasMore {
				if page.NextCursor != "" {
					t.Fatalf("last page has cursor: %+v", page)
				}
				break
			}
			after, err = parseFrostUTXOCursor(page.NextCursor, uint32(vault))
			if err != nil {
				t.Fatal(err)
			}
		}
		if total != count {
			t.Fatalf("vault=%d got %d rows, want %d", vault, total, count)
		}
		for _, n := range store.scanLimits {
			if n != limit+1 {
				t.Fatalf("scan=%d want %d", n, limit+1)
			}
		}
		for _, n := range store.batchSizes {
			if n > limit*2+1 {
				t.Fatalf("batch=%d exceeds %d", n, limit*2+1)
			}
		}
	}
	response := httptest.NewRecorder()
	hm.HandleFrostVaultUTXOs(response, httptest.NewRequest(http.MethodGet, "/frost/vault/utxos?chain=btc&vault_id=3", nil))
	if response.Code != 200 || strings.Contains(response.Body.String(), "private-script") || strings.Contains(response.Body.String(), "private-tweak") || strings.Contains(response.Body.String(), "script_pubkey") {
		t.Fatalf("unsafe response status=%d body=%s", response.Code, response.Body)
	}
}

func TestFrostVaultUTXOQueryRejectsUnboundedAndMismatchedInputs(t *testing.T) {
	validCursor := encodeFrostUTXOCursor(3, keys.KeyFrostBtcUtxo(3, strings.Repeat("a", 64), 0))
	for _, query := range []string{
		"chain=btc", "vault_id=0", "chain=eth&vault_id=0", "chain=BTC&vault_id=0", "chain=btc&vault_id=-1",
		"chain=btc&vault_id=4294967296", "chain=btc&vault_id=00", "chain=btc&vault_id=+1",
		"chain=btc&vault_id=0&limit=0", "chain=btc&vault_id=0&limit=201", "chain=btc&vault_id=0&limit=01",
		"chain=btc&vault_id=0&limit=1&limit=2", "chain=btc&chain=btc&vault_id=0",
		"chain=btc&vault_id=0&offset=1", "chain=btc&vault_id=0&count=1000000", "chain=btc&vault_id=0;limit=1",
		"chain=btc&vault_id=0&cursor=bad", "chain=btc&vault_id=0&cursor=" + validCursor,
		"chain=btc&vault_id=0&cursor=" + strings.Repeat("a", 1025), strings.Repeat("x", 4097),
	} {
		if _, _, _, err := parseFrostUTXOQuery(query); err == nil {
			t.Errorf("accepted invalid query %q", query)
		}
	}
	if vault, limit, after, err := parseFrostUTXOQuery("chain=btc&vault_id=3&cursor=" + validCursor); err != nil || vault != 3 || limit != 50 || after != keys.KeyFrostBtcUtxo(3, strings.Repeat("a", 64), 0) {
		t.Fatalf("valid query vault=%d limit=%d after=%q err=%v", vault, limit, after, err)
	}
	for _, patch := range []func(*frostUTXOCursor){
		func(c *frostUTXOCursor) { c.Version = 2 }, func(c *frostUTXOCursor) { c.Chain = "eth" },
		func(c *frostUTXOCursor) { c.Prefix = keys.KeyFrostBtcUtxoPrefix(99) }, func(c *frostUTXOCursor) { c.After = "" },
		func(c *frostUTXOCursor) { c.After += "_suffix" },
	} {
		c := frostUTXOCursor{Version: 1, Chain: "btc", VaultID: 3, Prefix: keys.KeyFrostBtcUtxoPrefix(3), After: keys.KeyFrostBtcUtxo(3, strings.Repeat("a", 64), 0)}
		patch(&c)
		data, _ := json.Marshal(c)
		if _, err := parseFrostUTXOCursor(base64.RawURLEncoding.EncodeToString(data), 3); err == nil {
			t.Errorf("accepted invalid cursor %+v", c)
		}
	}
	for _, raw := range []string{`{"v":1}`, `{"v":1} {}`, `{"unknown":1}`} {
		if _, err := parseFrostUTXOCursor(base64.RawURLEncoding.EncodeToString([]byte(raw)), 3); err == nil {
			t.Errorf("accepted cursor %s", raw)
		}
	}
	hm := &HandlerManager{}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		rec := httptest.NewRecorder()
		hm.HandleFrostVaultUTXOs(rec, httptest.NewRequest(method, "/frost/vault/utxos?chain=btc&vault_id=0", nil))
		if rec.Code != 405 || rec.Header().Get("Allow") != "GET" {
			t.Fatalf("method=%s status=%d", method, rec.Code)
		}
	}
}

func TestFrostVaultUTXOEmptyRegistrationPageDiffersFromStoreFailure(t *testing.T) {
	hm, dbm := newFrostQueryTestHandler(t)
	defer dbm.Close()
	query := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		hm.HandleFrostVaultUTXOs(rec, httptest.NewRequest(http.MethodGet, "/frost/vault/utxos?chain=btc&vault_id=4294967295", nil))
		return rec
	}
	                                                                       
	                                                                       
	empty := query()
	var page frostVaultUTXOPage
	if err := json.Unmarshal(empty.Body.Bytes(), &page); err != nil || empty.Code != http.StatusOK || page.Items == nil || len(page.Items) != 0 || page.HasMore || page.NextCursor != "" {
		t.Fatalf("empty registration page=%d %s err=%v", empty.Code, empty.Body, err)
	}
	hm.dbManager = nil
	failed := query()
	if failed.Code != http.StatusServiceUnavailable || strings.Contains(failed.Body.String(), `"items":[]`) {
		t.Fatalf("unavailable store became empty page: %d %s", failed.Code, failed.Body)
	}
}

func TestFrostVaultUTXOConcurrentConsumptionAdvancesWithoutRefill(t *testing.T) {
	_, dbm := newFrostQueryTestHandler(t)
	defer dbm.Close()
	for n := 0; n < 4; n++ {
		enqueueFrostUTXO(t, dbm, 0, n)
		dbm.EnqueueSet(keys.KeyFrostBtcLockedUtxo(0, fmt.Sprintf("%064x", n), 0), "job")
	}
	flushFrostUTXOs(t, dbm)
	store := &measuredFrostUTXOStore{store: dbm}
	store.beforeBatch = func() {
		for n := 0; n < 2; n++ {
			dbm.EnqueueDelete(keys.KeyFrostBtcUtxo(0, fmt.Sprintf("%064x", n), 0))
			dbm.EnqueueDelete(keys.KeyFrostBtcLockedUtxo(0, fmt.Sprintf("%064x", n), 0))
		}
		flushFrostUTXOs(t, dbm)
		store.beforeBatch = nil
	}
	page, err := queryFrostVaultUTXOs(context.Background(), store, 0, 2, "")
	if err != nil || len(page.Items) != 0 || !page.HasMore || page.NextCursor == "" || store.scans != 1 || store.batches != 1 || store.batchSizes[0] != 5 {
		t.Fatalf("concurrent page=%+v err=%v scans=%d batches=%d", page, err, store.scans, store.batches)
	}
	after, err := parseFrostUTXOCursor(page.NextCursor, 0)
	if err != nil || after != keys.KeyFrostBtcUtxo(0, fmt.Sprintf("%064x", 1), 0) {
		t.Fatalf("cursor advanced over sentinel: %q %v", after, err)
	}
	next, err := queryFrostVaultUTXOs(context.Background(), store, 0, 2, after)
	if err != nil || len(next.Items) != 2 || next.HasMore || next.Items[0].LockJobID != "job" {
		t.Fatalf("next=%+v err=%v", next, err)
	}
}

func TestFrostVaultUTXOInvalidStoredRecordsFailExplicitly(t *testing.T) {
	for _, mutate := range []func(*pb.FrostUtxo){
		func(u *pb.FrostUtxo) { u.VaultId = 9 }, func(u *pb.FrostUtxo) { u.Txid = strings.Repeat("b", 64) },
		func(u *pb.FrostUtxo) { u.Amount = "0" }, func(u *pb.FrostUtxo) { u.Amount = "01" },
		func(u *pb.FrostUtxo) { u.Amount = "18446744073709551616" }, func(u *pb.FrostUtxo) { u.RequestId = strings.Repeat("r", 513) },
	} {
		hm, dbm := newFrostQueryTestHandler(t)
		func() {
			defer dbm.Close()
			u := &pb.FrostUtxo{Txid: strings.Repeat("a", 64), VaultId: 0, Amount: "1000"}
			mutate(u)
			enqueueProto(t, dbm, keys.KeyFrostBtcUtxo(0, strings.Repeat("a", 64), 0), u)
			flushFrostUTXOs(t, dbm)
			rec := httptest.NewRecorder()
			hm.HandleFrostVaultUTXOs(rec, httptest.NewRequest(http.MethodGet, "/frost/vault/utxos?chain=btc&vault_id=0", nil))
			if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "unavailable") || strings.Contains(rec.Body.String(), `"items":[]`) {
				t.Fatalf("corrupt record returned success/empty: %d %s", rec.Code, rec.Body)
			}
		}()
	}
	for _, raw := range []string{"not-protobuf", "", strings.Repeat("x", maxFrostUTXORecord+1)} {
		hm, dbm := newFrostQueryTestHandler(t)
		func() {
			defer dbm.Close()
			dbm.EnqueueSet(keys.KeyFrostBtcUtxo(0, strings.Repeat("a", 64), 0), raw)
			flushFrostUTXOs(t, dbm)
			rec := httptest.NewRecorder()
			hm.HandleFrostVaultUTXOs(rec, httptest.NewRequest(http.MethodGet, "/frost/vault/utxos?chain=btc&vault_id=0", nil))
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("bad protobuf status=%d body=%s", rec.Code, rec.Body)
			}
		}()
	}
}

func TestFrostVaultUTXOCancellationStopsFurtherReads(t *testing.T) {
	_, dbm := newFrostQueryTestHandler(t)
	defer dbm.Close()
	enqueueFrostUTXO(t, dbm, 0, 1)
	flushFrostUTXOs(t, dbm)
	ctx, cancel := context.WithCancel(context.Background())
	store := &measuredFrostUTXOStore{store: dbm, afterScan: cancel}
	if _, err := queryFrostVaultUTXOs(ctx, store, 0, 50, ""); !errors.Is(err, context.Canceled) || store.scans != 1 || store.batches != 0 {
		t.Fatalf("cancel-after-scan err=%v scans=%d batches=%d", err, store.scans, store.batches)
	}
	if _, err := queryFrostVaultUTXOs(ctx, store, 0, 50, ""); !errors.Is(err, context.Canceled) || store.scans != 1 {
		t.Fatalf("pre-cancel err=%v scans=%d", err, store.scans)
	}
}

func TestFrostVaultUTXORealPebbleDeepPageReadBound(t *testing.T) {
	if testing.Short() {
		t.Skip("real Pebble scale test")
	}
	_, dbm := newFrostQueryTestHandler(t)
	defer dbm.Close()
	const count, limit = 100000, 50
	enqueueProto(t, dbm, keys.KeyFrostVaultConfig("btc", 0), &pb.FrostVaultConfig{Chain: "btc", BtcNetwork: "mainnet"})
	for n := 0; n < count; n++ {
		txid := fmt.Sprintf("%064x", n*2+1)
		enqueueProto(t, dbm, keys.KeyFrostBtcUtxo(7, txid, 0), &pb.FrostUtxo{
			Txid: txid, VaultId: 7, Amount: "100000", FinalizeHeight: 123,
			RequestId: fmt.Sprintf("recharge-%d", n), ScriptPubkey: frostUTXOTestTaprootScript(1),
		})
		                                                                   
		                                                              
		if n%20 == 0 {
			enqueueFrostUTXO(t, dbm, 7, n*2)
		}
	}
	flushFrostUTXOs(t, dbm)
	for n := 0; n < count; n += 20 {
		dbm.EnqueueDelete(keys.KeyFrostBtcUtxo(7, fmt.Sprintf("%064x", n*2), 0))
	}
	flushFrostUTXOs(t, dbm)
	for _, n := range []int{-1, count - 101} {
		after := ""
		if n >= 0 {
			after = keys.KeyFrostBtcUtxo(7, fmt.Sprintf("%064x", n*2+1), 0)
		}
		store := &measuredFrostUTXOStore{store: dbm}
		start := time.Now()
		page, err := queryFrostVaultUTXOs(context.Background(), store, 7, limit, after)
		if err != nil || len(page.Items) != limit || !page.HasMore || store.scans != 1 || store.scanLimits[0] != limit+1 || store.batches != 1 || store.batchSizes[0] != 2*limit+1 {
			t.Fatalf("n=%d rows=%d err=%v scans=%d limits=%v batches=%v", n, len(page.Items), err, store.scans, store.scanLimits, store.batchSizes)
		}
		if n >= 0 && page.Items[0].TxID != fmt.Sprintf("%064x", (n+1)*2+1) {
			t.Fatalf("deep-page first row=%s", page.Items[0].TxID)
		}
		for _, item := range page.Items {
			if !strings.HasPrefix(item.Address, "bc1p") || page.BTCNetwork != "mainnet" {
				t.Fatalf("scale page lost address/network: %+v", item)
			}
		}
		                                                                     
		                                                           
		const rounds = 100
		for round := 1; round < rounds; round++ {
			page, err = queryFrostVaultUTXOs(context.Background(), store, 7, limit, after)
			if err != nil || len(page.Items) != limit {
				t.Fatalf("round=%d rows=%d err=%v", round, len(page.Items), err)
			}
		}
		elapsed := time.Since(start)
		if store.scans != rounds || store.batches != rounds {
			t.Fatalf("unbounded repeated scans/batches=%d/%d", store.scans, store.batches)
		}
		t.Logf("real Pebble live=%d tombstones=%d after=%d page=%d per-page scan-key-budget=%d batch-key-budget=%d rounds=%d elapsed=%s mean=%s", count, count/20, n, limit, limit+1, limit*2+1, rounds, elapsed, elapsed/rounds)
	}
	                                                                         
	                                                               
	encoded := url.QueryEscape(encodeFrostUTXOCursor(7, keys.KeyFrostBtcUtxo(7, fmt.Sprintf("%064x", count-101), 0)))
	if _, _, _, err := parseFrostUTXOQuery("chain=btc&vault_id=7&cursor=" + encoded); err != nil {
		t.Fatal(err)
	}
}
