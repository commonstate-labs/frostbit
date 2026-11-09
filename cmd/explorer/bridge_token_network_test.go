package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"dex/pb"
)

func TestBridgeTokenNetworkUsesRegisteredChainOnDemand(t *testing.T) {
	for _, include := range []bool{false, true} {
		t.Run(fmt.Sprint(include), func(t *testing.T) {
			configCalls := 0
			srv := newFrostWithdrawV2TestServer([]string{"node.example:443"}, func(req *http.Request) (*http.Response, error) {
				switch req.URL.Path {
				case "/gettoken":
					return frostWithdrawV2ProtoResponse(req, http.StatusOK, &pb.GetTokenResponse{Token: &pb.Token{Address: "br_new", Type: pb.TokenType_TOKEN_BRIDGED, NativeChain: "base", NativeContract: "0x1111111111111111111111111111111111111111"}})
				case "/frost/vault/config":
					configCalls++
					if req.URL.Query().Get("chain") != "base" {
						t.Fatalf("wrong token network: %s", req.URL)
					}
					return frostWithdrawV2ProtoResponse(req, http.StatusOK, &pb.FrostVaultConfig{Chain: "base", EvmChainId: 9007199254740993})
				default:
					return nil, fmt.Errorf("unexpected directory request: %s", req.URL)
				}
			})
			url := "/api/token?node=node-0&addr=br_new"
			if include {
				url += "&include_network=1"
			}
			rec := httptest.NewRecorder()
			srv.handleTokenQuery(rec, httptest.NewRequest(http.MethodGet, url, nil))
			var response struct {
				Token map[string]any `json:"token"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.Token["address"] != "br_new" {
				t.Fatalf("token query failed: %s", rec.Body)
			}
			if include {
				if configCalls != 1 || response.Token["evm_chain_id"] != "9007199254740993" {
					t.Fatalf("network lost precision or not read: %s", rec.Body)
				}
			} else if configCalls != 0 || response.Token["evm_chain_id"] != nil {
				t.Fatalf("unrequested network read: %s", rec.Body)
			}
		})
	}
}

func TestBridgeTokenNetworkFailureDoesNotReturnUsableMetadata(t *testing.T) {
	srv := newFrostWithdrawV2TestServer([]string{"node.example:443"}, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/gettoken" {
			return frostWithdrawV2ProtoResponse(req, http.StatusOK, &pb.GetTokenResponse{Token: &pb.Token{Address: "br_new", Type: pb.TokenType_TOKEN_BRIDGED, NativeChain: "eth"}})
		}
		return nil, fmt.Errorf("network config unavailable")
	})
	rec := httptest.NewRecorder()
	srv.handleTokenQuery(rec, httptest.NewRequest(http.MethodGet, "/api/token?node=node-0&addr=br_new&include_network=1", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
}
