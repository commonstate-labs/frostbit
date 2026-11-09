package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dex/pb"
	"github.com/btcsuite/btcd/btcutil/base58"
)

func TestAccountDepositAddressRequiresBoundVault(t *testing.T) {
	chains := []struct {
		name            string
		externalChainID uint64
		vault           string
	}{
		{"ETH", 1, "0x2222222222222222222222222222222222222222"},
		{"TRX", 728126428, base58.CheckEncode(bytes.Repeat([]byte{0x22}, 20), 0x41)},
	}
	for _, chain := range chains {
		for _, mode := range []string{"configured", "no_domain", "missing_vault", "disabled", "wrong_dex", "unknown_protocol"} {
			t.Run(chain.name+"/"+mode, func(t *testing.T) {
				cfg := &pb.FrostVaultConfig{
					VaultRefs: []string{chain.vault}, EvmChainId: chain.externalChainID,
					DepositProtocol: 2, DepositDexChainId: 51015,
				}
				switch mode {
				case "no_domain":
					cfg.DepositDexChainId = 0
				case "missing_vault":
					cfg.VaultRefs = nil
				case "disabled":
					cfg.DepositProtocol, cfg.DepositDexChainId = 0, 0
				case "wrong_dex":
					cfg.DepositDexChainId = 1
				case "unknown_protocol":
					cfg.DepositProtocol = 3
				}
				srv := newFrostWithdrawV2TestServer([]string{"node.example:443"}, func(req *http.Request) (*http.Response, error) {
					switch req.URL.Path {
					case "/frost/vault/group_pubkey":
						if req.URL.Query().Get("vault_id") != "0" {
							return nil, fmt.Errorf("no more vaults")
						}
						return frostWithdrawV2ProtoResponse(req, http.StatusOK, &pb.GetVaultGroupPubKeyResponse{Status: "ACTIVE", GroupPubkey: "0211"})
					case "/frost/vault/config":
						return frostWithdrawV2ProtoResponse(req, http.StatusOK, cfg)
					default:
						return nil, fmt.Errorf("unexpected path %s", req.URL.Path)
					}
				})
				srv.chainID = 51015
				response := httptest.NewRecorder()
				srv.depositAddressContract(context.Background(), response, &depositAddressRequest{Node: "node-0", Chain: chain.name})
				var body depositAddressResponse
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if body.DepositAddress != "" {
					t.Fatalf("exposed unsafe plain-transfer deposit address: %s", body.DepositAddress)
				}
				if mode == "configured" {
					if body.Error != "" || body.VaultAddress != cfg.VaultRefs[0] || body.DexChainID != "51015" || body.DepositVersion != 2 {
						t.Fatalf("unexpected response: %+v", body)
					}
				} else if !strings.Contains(body.Error, "do not transfer directly to the vault") || body.VaultAddress != "" || body.ScriptPubkey != "" || body.VaultPubkey != "" || body.DepositVersion != 0 {
					t.Fatalf("configuration did not fail closed: %+v", body)
				}
			})
		}
	}
}
