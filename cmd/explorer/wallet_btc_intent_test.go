package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dex/config"
	"dex/pb"
	"dex/utils"
)

func TestValidateBTCIntentURLRequiresExplicitLoopbackEndpoint(t *testing.T) {
	for _, raw := range []string{
		"http://127.0.0.1:8788/v1/btc/deposit-intents",
		"http://[::1]:8788/v1/btc/deposit-intents",
	} {
		if err := validateBTCIntentURL(raw); err != nil {
			t.Fatalf("valid loopback URL %q: %v", raw, err)
		}
	}
	for _, raw := range []string{
		"https://127.0.0.1:8788/v1/btc/deposit-intents",
		"http://localhost:8788/v1/btc/deposit-intents",
		"http://192.0.2.1:8788/v1/btc/deposit-intents",
		"http://127.0.0.1:8788/other",
		"http://127.0.0.1:8788/v1/btc/deposit-intents?redirect=1",
	} {
		if err := validateBTCIntentURL(raw); err == nil {
			t.Fatalf("unsafe BTC intent URL was accepted: %q", raw)
		}
	}
}

func TestDepositAddressBTCRegistersDerivationBeforeReturningAddress(t *testing.T) {
	const receiver = "fbc1qw508d6qejxtdg4y5r3zarvary0c5xw7kng2fs8"
	groupPubkey, err := hex.DecodeString("79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798")
	if err != nil {
		t.Fatal(err)
	}
	expectedTweak, err := utils.ComputeUserTweakForPubKey(groupPubkey, receiver)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("t", config.ExplorerServiceTokenMinLen)
	var registered btcIntentRegistrationRequest
	intentServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/btc/deposit-intents" || request.Header.Get(config.ExplorerServiceTokenHeader) != token {
			t.Errorf("unexpected intent request path=%q token=%q", request.URL.Path, request.Header.Get(config.ExplorerServiceTokenHeader))
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if err := json.NewDecoder(request.Body).Decode(&registered); err != nil {
			t.Errorf("decode registration: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(btcIntentRegistrationResponse{WatchRegistered: true, WatchExpiresAt: "2026-09-16T01:00:00Z", StartHeight: 100})
	}))
	defer intentServer.Close()

	srv := newFrostWithdrawV2TestServer([]string{"node.example:443"}, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/frost/vault/group_pubkey":
			if req.URL.Query().Get("vault_id") != "0" {
				return nil, fmt.Errorf("no more vaults")
			}
			return frostWithdrawV2ProtoResponse(req, http.StatusOK, &pb.GetVaultGroupPubKeyResponse{
				Status: "ACTIVE", DepositGroupPubkey: hex.EncodeToString(groupPubkey), DepositKeyEpoch: 9,
			})
		case "/frost/vault/config":
			return frostWithdrawV2ProtoResponse(req, http.StatusOK, &pb.FrostVaultConfig{BtcNetwork: "regtest", GuardianPubkey: mustExplorerGuardianKey(t)})
		default:
			return nil, fmt.Errorf("unexpected path %s", req.URL.Path)
		}
	})
	srv.btcIntentURL = intentServer.URL + "/v1/btc/deposit-intents"
	srv.btcIntentClient = intentServer.Client()
	srv.serviceToken = token
	recorder := httptest.NewRecorder()
	srv.depositAddressBTC(context.Background(), recorder, &depositAddressRequest{Node: "node.example:443", Chain: "BTC", ReceiverAddress: receiver})
	var response depositAddressResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Error != "" || !response.WatchRegistered || response.WatchExpiresAt == "" ||
		response.Tweak != hex.EncodeToString(expectedTweak) || !strings.HasPrefix(response.DepositAddress, "bcrt1p") {
		t.Fatalf("unexpected deposit response: %+v", response)
	}
	if registered.ReceiverAddress != receiver || registered.Tweak != response.Tweak || registered.ScriptPubkey != response.ScriptPubkey || registered.DepositAddress != response.DepositAddress || registered.KeyEpoch != 9 {
		t.Fatalf("registered=%+v response=%+v", registered, response)
	}
}

func TestDepositAddressBTCFailsClosedWhenMonitoringRegistrationFails(t *testing.T) {
	srv := &server{btcIntentURL: "http://127.0.0.1:1/v1/btc/deposit-intents"}
	if _, err := srv.registerBTCDepositIntent(context.Background(), btcIntentRegistrationRequest{}); err == nil {
		t.Fatal("missing monitor client and token were accepted")
	}
}

func mustExplorerGuardianKey(t *testing.T) []byte {
	t.Helper()
	pub, err := hex.DecodeString("c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5")
	if err != nil {
		t.Fatal(err)
	}
	return pub
}
