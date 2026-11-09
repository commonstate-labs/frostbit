package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dex/pb"

	"google.golang.org/protobuf/proto"
)

func TestHandleAddressReturnsZeroAccountWhenNodeReportsNotFound(t *testing.T) {
	const address = "fbc1qtestaccount"
	node := newAccountErrorTestNode(t, "account not found: not found")
	s := &server{
		client:       node.Client(),
		timeout:      time.Second,
		allowedNodes: map[string]struct{}{strings.TrimPrefix(node.URL, "https://"): {}},
	}

	response := requestAddressForTest(t, s, strings.TrimPrefix(node.URL, "https://"), address)
	if response.Error != "" {
		t.Fatalf("unexpected response error: %s", response.Error)
	}
	if response.Account == nil {
		t.Fatal("expected a synthetic zero account")
	}
	if response.Account.Address != address || response.Account.Nonce != 0 {
		t.Fatalf("zero account identity/nonce = %q/%d", response.Account.Address, response.Account.Nonce)
	}
	if response.Account.Exists {
		t.Fatal("missing node account was reported as existing")
	}
	if response.Account.RentStatus != nil {
		t.Fatalf("missing node account retained a rent projection: %+v", response.Account.RentStatus)
	}
	if len(response.Account.Balances) != 0 {
		t.Fatalf("zero account balances = %+v", response.Account.Balances)
	}
	if response.Account.TotalFBAsset != "0" || response.Account.FBBalanceBuckets != "0" || response.Account.FBIndexedClaims != "0" {
		t.Fatalf("zero account FB totals = %q/%q/%q", response.Account.TotalFBAsset, response.Account.FBBalanceBuckets, response.Account.FBIndexedClaims)
	}
}

func TestHandleAddressPreservesNonNotFoundNodeError(t *testing.T) {
	node := newAccountErrorTestNode(t, "account not found: state database unavailable")
	s := &server{
		client:       node.Client(),
		timeout:      time.Second,
		allowedNodes: map[string]struct{}{strings.TrimPrefix(node.URL, "https://"): {}},
	}

	response := requestAddressForTest(t, s, strings.TrimPrefix(node.URL, "https://"), "fbc1qtestaccount")
	if response.Error != "account not found: state database unavailable" {
		t.Fatalf("response error = %q", response.Error)
	}
	if response.Account != nil {
		t.Fatalf("unexpected synthetic account for an upstream failure: %+v", response.Account)
	}
}

func TestHandleAddressUsesDefaultNodeWhenRequestOmitsNode(t *testing.T) {
	const address = "fbc1qtestaccount"
	node := newAccountBalanceTestNode(t, address, "123456789", "")
	nodeAddress := strings.TrimPrefix(node.URL, "https://")
	s := &server{
		client:       node.Client(),
		timeout:      time.Second,
		defaultNodes: []string{nodeAddress},
		allowedNodes: map[string]struct{}{nodeAddress: {}},
	}

	response := requestAddressForTest(t, s, "", address)
	if response.Error != "" {
		t.Fatalf("unexpected response error: %s", response.Error)
	}
	if response.Account == nil || response.Account.Balances["FB"] == nil {
		t.Fatalf("missing account balance: %+v", response.Account)
	}
	if !response.Account.Exists {
		t.Fatal("existing node account was reported as absent")
	}
	if got := response.Account.Balances["FB"].Balance; got != "123456789" {
		t.Fatalf("FB balance = %q", got)
	}
	if response.Account.RentStatus == nil || response.Account.RentStatus.SpendableFB != "123456789" {
		t.Fatalf("missing projected rent status: %+v", response.Account.RentStatus)
	}
	if response.Account.RentStatus.AsOfHeight != 10 ||
		response.Account.RentStatus.LastSettledHeight != 7 ||
		response.Account.RentStatus.LastAccruedHeight != 8 {
		t.Fatalf("rent status cursors were not preserved: %+v", response.Account.RentStatus)
	}
}

func TestHandleAddressPreservesBalanceFetchError(t *testing.T) {
	const address = "fbc1qtestaccount"
	node := newAccountBalanceTestNode(t, address, "", "state database unavailable")
	nodeAddress := strings.TrimPrefix(node.URL, "https://")
	s := &server{
		client:       node.Client(),
		timeout:      time.Second,
		allowedNodes: map[string]struct{}{nodeAddress: {}},
	}

	response := requestAddressForTest(t, s, nodeAddress, address)
	if response.Error != "failed to fetch account balances: state database unavailable" {
		t.Fatalf("response error = %q", response.Error)
	}
	if response.Account != nil {
		t.Fatalf("unexpected account after balance failure: %+v", response.Account)
	}
}

func TestHandleAddressPreservesRentStatusFetchError(t *testing.T) {
	const address = "fbc1qtestaccount"
	node := newAccountBalanceTestNode(t, address, "123", "", "state database unavailable")
	nodeAddress := strings.TrimPrefix(node.URL, "https://")
	s := &server{
		client:       node.Client(),
		timeout:      time.Second,
		allowedNodes: map[string]struct{}{nodeAddress: {}},
	}

	response := requestAddressForTest(t, s, nodeAddress, address)
	if response.Error != "failed to fetch account rent status: state database unavailable" {
		t.Fatalf("response error = %q", response.Error)
	}
	if response.Account != nil {
		t.Fatalf("unexpected account after rent status failure: %+v", response.Account)
	}
}

func TestHandleAddressRejectsMissingNodeConfiguration(t *testing.T) {
	response := requestAddressForTest(t, &server{}, "", "fbc1qtestaccount")
	if response.Error != "node is not configured" || response.Account != nil {
		t.Fatalf("response = %+v", response)
	}
}

func TestHandleAddressRequireRemoteNeverFallsBackToLocalState(t *testing.T) {
	const address = "fbc1qtestaccount"
	body, err := json.Marshal(addressRequest{Address: address, RequireRemote: true})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/address", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	(&server{nodeDB: nil}).handleAddress(rec, req)
	var response addressResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Error != "an authoritative node is required" || response.Account != nil {
		t.Fatalf("response = %+v", response)
	}
}

func newAccountErrorTestNode(t *testing.T, message string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/getaccount", func(w http.ResponseWriter, _ *http.Request) {
		data, err := proto.Marshal(&pb.GetAccountResponse{Error: message})
		if err != nil {
			t.Errorf("marshal account response: %v", err)
			return
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(data)
	})
	mux.HandleFunc("/account/asset", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("account asset endpoint must not be queried after account-not-found: %s", r.URL.String())
		writeAccountAssetForTest(t, w, r.URL.Query().Get("address"), "0", "")
	})
	node := httptest.NewTLSServer(mux)
	t.Cleanup(node.Close)
	return node
}

func newAccountBalanceTestNode(t *testing.T, address, balance, balanceError string, assetErrors ...string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/getaccount", func(w http.ResponseWriter, _ *http.Request) {
		data, err := proto.Marshal(&pb.GetAccountResponse{Account: &pb.Account{Address: address}})
		if err != nil {
			t.Errorf("marshal account response: %v", err)
			return
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(data)
	})
	mux.HandleFunc("/getaccountbalances", func(w http.ResponseWriter, _ *http.Request) {
		response := &pb.GetAccountBalancesResponse{Error: balanceError}
		if balanceError == "" {
			response.Balances = map[string]*pb.TokenBalance{"FB": {Balance: balance}}
		}
		data, err := proto.Marshal(response)
		if err != nil {
			t.Errorf("marshal balance response: %v", err)
			return
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(data)
	})
	mux.HandleFunc("/account/asset", func(w http.ResponseWriter, _ *http.Request) {
		assetError := ""
		if len(assetErrors) > 0 {
			assetError = assetErrors[0]
		}
		writeAccountAssetForTest(t, w, address, balance, assetError)
	})
	node := httptest.NewTLSServer(mux)
	t.Cleanup(node.Close)
	return node
}

func writeAccountAssetForTest(t *testing.T, w http.ResponseWriter, address, balance, assetError string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if assetError != "" {
		_ = json.NewEncoder(w).Encode(map[string]string{"error": assetError})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"owner":           address,
		"token":           "FB",
		"balance_buckets": balance,
		"indexed_claims":  "0",
		"total":           balance,
		"rent": map[string]any{
			"owner":                address,
			"token":                "FB",
			"managed":              true,
			"state":                "renting",
			"as_of_height":         10,
			"current_height":       10,
			"last_settled_height":  7,
			"last_accrued_height":  8,
			"nominal_fb":           balance,
			"effective_nominal_fb": balance,
			"available_fb":         balance,
			"spendable_fb":         balance,
			"pending_rent_fb":      "0",
			"unpaid_rent_fb":       "0",
			"required_funding_fb":  "0",
			"balance_buckets":      balance,
			"indexed_claims":       "0",
			"exempt_min":           "1000000000",
			"rent_per_block":       "100",
			"activation_fee":       "10000",
			"recycle_account":      "__rent_recycle_fund__",
		},
	})
}

func requestAddressForTest(t *testing.T, s *server, node, address string) addressResponse {
	t.Helper()
	body, err := json.Marshal(addressRequest{Node: node, Address: address})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/address", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleAddress(rec, req)
	var response addressResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode address response: %v; body=%s", err, rec.Body.String())
	}
	return response
}
