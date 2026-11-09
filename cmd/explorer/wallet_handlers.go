package main

                                                  
                                                                                         
                                                                 

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"dex/config"
	frostchain "dex/frost/chain"
	"dex/pb"
	"dex/protocol"
	"dex/utils"

	"github.com/btcsuite/btcd/chaincfg"
	"google.golang.org/protobuf/proto"
)

                                                                                                                                                                                              
                                                                                                             
                                                         
                                                

type walletSubmitTxRequest struct {
	Node string `json:"node"`
	Tx   string `json:"tx"`                                          
}

type walletSubmitTxResponse struct {
	Ok    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func (s *server) handleWalletSubmitTx(w http.ResponseWriter, r *http.Request) {
	addWalletCORS(w, r)
	if r.Method == http.MethodOptions {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req walletSubmitTxRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, walletSubmitTxResponse{Error: "invalid request: " + err.Error()})
		return
	}

	txBytes, err := base64.StdEncoding.DecodeString(req.Tx)
	if err != nil {
		writeJSON(w, walletSubmitTxResponse{Error: "invalid base64: " + err.Error()})
		return
	}

	                           
	var anyTx pb.AnyTx
	if err := proto.Unmarshal(txBytes, &anyTx); err != nil {
		writeJSON(w, walletSubmitTxResponse{Error: "invalid AnyTx protobuf: " + err.Error()})
		return
	}

	node := s.pickNode(req.Node)
	if node == "" {
		writeJSON(w, walletSubmitTxResponse{Error: "no node available"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	nodeURL := "https://" + node + "/tx"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, nodeURL, bytes.NewReader(txBytes))
	if err != nil {
		writeJSON(w, walletSubmitTxResponse{Error: err.Error()})
		return
	}
	httpReq.Header.Set("Content-Type", "application/x-protobuf")
	httpReq.Header.Set("X-DEX-Wait-Admission", "1")

	resp, err := s.nodeDo(httpReq)
	if err != nil {
		writeJSON(w, walletSubmitTxResponse{Error: "node request failed: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		detailBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		detail := strings.TrimSpace(string(detailBytes))
		if detail == "" {
			detail = resp.Status
		}
		writeJSON(w, walletSubmitTxResponse{Error: "node rejected tx: " + detail})
		return
	}

	writeJSON(w, walletSubmitTxResponse{Ok: true})
}

                                                                                                                                                                                                    
                                                               
                                                           
                                           

type walletNonceRequest struct {
	Node          string `json:"node"`
	Address       string `json:"address"`
	RequireRemote bool   `json:"require_remote,omitempty"`
}

type walletNonceResponse struct {
	Address string `json:"address"`
	Nonce   uint64 `json:"nonce"`
	ChainID uint64 `json:"chain_id,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (s *server) walletChainID() uint64 {
	if s != nil && s.chainID != 0 {
		return s.chainID
	}
	return config.DefaultChainID
}

func (s *server) handleWalletNonce(w http.ResponseWriter, r *http.Request) {
	addWalletCORS(w, r)
	if r.Method == http.MethodOptions {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req walletNonceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, walletNonceResponse{Error: "invalid request"})
		return
	}
	if req.Address == "" {
		writeJSON(w, walletNonceResponse{Error: "address is required"})
		return
	}

	node := strings.TrimSpace(req.Node)
	if req.RequireRemote {
		var hasNode bool
		node, hasNode = s.resolveNode(node)
		if !hasNode {
			writeJSON(w, walletNonceResponse{Address: req.Address, ChainID: s.walletChainID(), Error: "an authoritative node is required"})
			return
		}
	}
	if node == "" && s.nodeDB == nil {
		writeJSON(w, walletNonceResponse{Address: req.Address, ChainID: s.walletChainID(), Error: "no node available"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	var nonce uint64
	if node != "" {
		protoReq := &pb.GetAccountRequest{Address: req.Address}
		var protoResp pb.GetAccountResponse
		if err := s.fetchProto(ctx, node, "/getaccount", protoReq, &protoResp); err == nil {
			if protoResp.Error != "" && !strings.Contains(strings.ToLower(protoResp.Error), "not found") {
				writeJSON(w, walletNonceResponse{
					Address: req.Address,
					ChainID: s.walletChainID(),
					Error:   "failed to fetch nonce from node: " + protoResp.Error,
				})
				return
			}
			if protoResp.Account != nil {
				nonce = protoResp.Account.Nonce
			}
		} else if s.nodeDB != nil && !req.RequireRemote {
			if acc, localErr := localGetAccount(s.nodeDB, req.Address); localErr == nil && acc != nil {
				nonce = acc.Nonce
			} else {
				writeJSON(w, walletNonceResponse{
					Address: req.Address,
					ChainID: s.walletChainID(),
					Error:   "failed to fetch nonce from node: " + err.Error(),
				})
				return
			}
		} else {
			writeJSON(w, walletNonceResponse{
				Address: req.Address,
				ChainID: s.walletChainID(),
				Error:   "failed to fetch nonce from node: " + err.Error(),
			})
			return
		}
	} else if s.nodeDB != nil {
		if acc, err := localGetAccount(s.nodeDB, req.Address); err == nil && acc != nil {
			nonce = acc.Nonce
		}
	}

	writeJSON(w, walletNonceResponse{Address: req.Address, Nonce: nonce, ChainID: s.walletChainID()})
}

                                                                                                                                                                                                
                     
                                                          
                                                               

type walletReceiptRequest struct {
	Node string `json:"node"`
	TxID string `json:"tx_id"`
}

type walletReceiptResponse struct {
	TxID        string `json:"tx_id"`
	Status      string `json:"status"`
	BlockHeight uint64 `json:"block_height,omitempty"`
	Fee         string `json:"fee,omitempty"`
	Error       string `json:"error,omitempty"`
}

func (s *server) handleWalletReceipt(w http.ResponseWriter, r *http.Request) {
	addWalletCORS(w, r)
	if r.Method == http.MethodOptions {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req walletReceiptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, walletReceiptResponse{Error: "invalid request"})
		return
	}
	if req.TxID == "" {
		writeJSON(w, walletReceiptResponse{Error: "tx_id is required"})
		return
	}

	node := strings.TrimSpace(req.Node)

	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	if node != "" {
		receipt, err := s.fetchReceipt(ctx, node, req.TxID)
		if err != nil {
			writeJSON(w, walletReceiptResponse{TxID: req.TxID, Error: err.Error()})
			return
		}
		if receipt == nil {
			writeJSON(w, walletReceiptResponse{TxID: req.TxID, Error: "receipt not found"})
			return
		}
		writeJSON(w, walletReceiptResponse{
			TxID:        receipt.TxId,
			Status:      receipt.Status,
			BlockHeight: receipt.BlockHeight,
			Fee:         receipt.Fee,
			Error:       receipt.Error,
		})
		return
	}

	                                 
	if s.nodeDB != nil {
		receipt, err := localGetTxReceipt(s.nodeDB, req.TxID)
		if err == nil && receipt != nil {
			writeJSON(w, walletReceiptResponse{
				TxID:        receipt.TxId,
				Status:      receipt.Status,
				BlockHeight: receipt.BlockHeight,
				Fee:         receipt.Fee,
				Error:       receipt.Error,
			})
			return
		}
	}

	writeJSON(w, walletReceiptResponse{TxID: req.TxID, Error: "receipt not found"})
}

                                                                                                                                                                                                                             

                                                                                             
func addWalletCORS(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = "*"
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

                                                                                                                                                                                
                                             
                                                                                       
                                                                                                      
                                                                                       
                                                                                                               

type depositAddressRequest struct {
	Node            string `json:"node"`
	Chain           string `json:"chain"`
	ReceiverAddress string `json:"receiver_address"`
}

type depositAddressResponse struct {
	VaultID         uint32 `json:"vault_id"`
	KeyEpoch        uint64 `json:"key_epoch"`
	VaultPubkey     string `json:"vault_pubkey"`                                                                
	DepositAddress  string `json:"deposit_address"`                                                              
	ScriptPubkey    string `json:"script_pubkey,omitempty"`                                                         
	Tweak           string `json:"tweak,omitempty"`                                               
	BTCNetwork      string `json:"btc_network,omitempty"`
	WatchRegistered bool   `json:"watch_registered,omitempty"`
	WatchExpiresAt  string `json:"watch_expires_at,omitempty"`
	EVMChainID      uint64 `json:"evm_chain_id,omitempty"`
	VaultAddress    string `json:"vault_address,omitempty"`
	DexChainID      string `json:"dex_chain_id,omitempty"`
	DepositVersion  uint32 `json:"deposit_version,omitempty"`
	Error           string `json:"error,omitempty"`
}

type btcIntentRegistrationRequest struct {
	ReceiverAddress string `json:"receiver_address"`
	VaultID         uint32 `json:"vault_id"`
	KeyEpoch        uint64 `json:"key_epoch"`
	VaultPubkey     string `json:"vault_pubkey"`
	Tweak           string `json:"tweak"`
	ScriptPubkey    string `json:"script_pubkey"`
	DepositAddress  string `json:"deposit_address"`
	BTCNetwork      string `json:"btc_network"`
}

type btcIntentRegistrationResponse struct {
	WatchRegistered bool   `json:"watch_registered"`
	WatchExpiresAt  string `json:"watch_expires_at"`
	StartHeight     uint64 `json:"start_height"`
	Error           string `json:"error,omitempty"`
}

const maxVaultProbe = 200                                   

func (s *server) handleWalletDepositAddress(w http.ResponseWriter, r *http.Request) {
	addWalletCORS(w, r)
	if r.Method == http.MethodOptions {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req depositAddressRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, depositAddressResponse{Error: "invalid request: " + err.Error()})
		return
	}
	if req.Chain == "" {
		writeJSON(w, depositAddressResponse{Error: "chain is required"})
		return
	}

	chainUpper := strings.ToUpper(frostchain.NormalizeChain(req.Chain))
	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	if chainUpper == "BTC" {
		s.depositAddressBTC(ctx, w, &req)
		return
	}
	                                                           
	s.depositAddressContract(ctx, w, &req)
}

type vaultCandidate struct {
	id          uint32
	groupPubkey []byte
	chain       string
	keyEpoch    uint64
}

                                                                                                         
func (s *server) probeActiveVaults(ctx context.Context, node, chain string) ([]vaultCandidate, string) {
	normalized := frostchain.NormalizeChain(chain)
	for _, c := range uniqueNonEmptyStrings(normalized, strings.ToUpper(normalized), strings.ToLower(chain), strings.ToUpper(chain), chain) {
		var out []vaultCandidate
		for vaultID := uint32(0); vaultID < maxVaultProbe; vaultID++ {
			var vaultResp pb.GetVaultGroupPubKeyResponse
			path := fmt.Sprintf("/frost/vault/group_pubkey?chain=%s&vault_id=%d", c, vaultID)
			if err := s.fetchProto(ctx, node, path, nil, &vaultResp); err != nil {
				break
			}
			if vaultResp.Status != "ACTIVE" && vaultResp.Status != "KEY_READY" {
				continue
			}
			pubkeyHex := vaultResp.DepositGroupPubkey
			keyEpoch := vaultResp.DepositKeyEpoch
			if pubkeyHex == "" {
				pubkeyHex = vaultResp.GroupPubkey
				keyEpoch = vaultResp.KeyEpoch
			}
			gp, err := hex.DecodeString(pubkeyHex)
			if err != nil || len(gp) == 0 {
				continue
			}
			out = append(out, vaultCandidate{id: vaultID, groupPubkey: gp, chain: c, keyEpoch: keyEpoch})
		}
		if len(out) > 0 {
			return out, c
		}
	}
	return nil, ""
}

func (s *server) depositAddressBTC(ctx context.Context, w http.ResponseWriter, req *depositAddressRequest) {
	receiver := strings.TrimSpace(req.ReceiverAddress)
	if !utils.IsDexAddress(receiver) {
		writeJSON(w, depositAddressResponse{Error: "receiver_address is not a canonical DEX address"})
		return
	}
	candidates, chainUsed := s.probeActiveVaults(ctx, req.Node, req.Chain)
	if len(candidates) == 0 {
		writeJSON(w, depositAddressResponse{Error: fmt.Sprintf("no active vault found for chain %s", req.Chain)})
		return
	}
	chosen := candidates[rand.Intn(len(candidates))]
	btcNetwork := s.btcNetworkForChain(ctx, req.Node, chainUsed)
	netParams, err := btcNetParams(btcNetwork)
	if err != nil {
		writeJSON(w, depositAddressResponse{Error: err.Error()})
		return
	}

	tweak, err := utils.ComputeUserTweakForPubKey(chosen.groupPubkey, receiver)
	if err != nil {
		writeJSON(w, depositAddressResponse{Error: "failed to compute receiver tweak: " + err.Error()})
		return
	}
	tweakedXOnly, err := utils.ComputeTweakedXOnlyPubkey(chosen.groupPubkey, tweak)
	if err != nil {
		writeJSON(w, depositAddressResponse{Error: "failed to compute tweaked pubkey: " + err.Error()})
		return
	}
	var cfg pb.FrostVaultConfig
	if err := s.fetchProto(ctx, req.Node, "/frost/vault/config?chain="+url.QueryEscape(chainUsed), nil, &cfg); err != nil {
		writeJSON(w, depositAddressResponse{Error: "failed to load fixed BTC guardian"})
		return
	}
	scriptPubKey, _, _, err := utils.BuildDualCustodyTaproot(tweakedXOnly, cfg.GuardianPubkey)
	if err != nil {
		writeJSON(w, depositAddressResponse{Error: "failed to build tweaked scriptPubKey: " + err.Error()})
		return
	}
	xOnly, err := utils.ParseTaprootScriptPubKeyXOnly(scriptPubKey)
	if err != nil {
		writeJSON(w, depositAddressResponse{Error: "failed to parse tweaked scriptPubKey: " + err.Error()})
		return
	}
	depositAddr, err := utils.TaprootAddressFromXOnly(xOnly, netParams)
	if err != nil {
		writeJSON(w, depositAddressResponse{Error: "failed to generate taproot address: " + err.Error()})
		return
	}
	registration := btcIntentRegistrationRequest{
		ReceiverAddress: receiver, VaultID: chosen.id, KeyEpoch: chosen.keyEpoch,
		VaultPubkey: hex.EncodeToString(chosen.groupPubkey), Tweak: hex.EncodeToString(tweak),
		ScriptPubkey: hex.EncodeToString(scriptPubKey), DepositAddress: depositAddr, BTCNetwork: btcNetwork,
	}
	watchRegistered, watchExpiresAt := false, ""
	if s.btcIntentURL != "" {
		registered, err := s.registerBTCDepositIntent(ctx, registration)
		if err != nil {
			writeJSON(w, depositAddressResponse{Error: "failed to register BTC deposit monitoring: " + err.Error()})
			return
		}
		if !registered.WatchRegistered || strings.TrimSpace(registered.WatchExpiresAt) == "" {
			writeJSON(w, depositAddressResponse{Error: "BTC deposit monitoring did not confirm registration"})
			return
		}
		watchRegistered, watchExpiresAt = true, registered.WatchExpiresAt
	}
	writeJSON(w, depositAddressResponse{
		VaultID:         chosen.id,
		KeyEpoch:        chosen.keyEpoch,
		VaultPubkey:     hex.EncodeToString(chosen.groupPubkey),
		DepositAddress:  depositAddr,
		ScriptPubkey:    hex.EncodeToString(scriptPubKey),
		Tweak:           hex.EncodeToString(tweak),
		BTCNetwork:      btcNetwork,
		WatchRegistered: watchRegistered,
		WatchExpiresAt:  watchExpiresAt,
	})
}

func (s *server) registerBTCDepositIntent(ctx context.Context, registration btcIntentRegistrationRequest) (btcIntentRegistrationResponse, error) {
	if s == nil || strings.TrimSpace(s.btcIntentURL) == "" || s.btcIntentClient == nil || strings.TrimSpace(s.serviceToken) == "" {
		return btcIntentRegistrationResponse{}, fmt.Errorf("BTC deposit monitor is not configured")
	}
	body, err := json.Marshal(registration)
	if err != nil {
		return btcIntentRegistrationResponse{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.btcIntentURL, bytes.NewReader(body))
	if err != nil {
		return btcIntentRegistrationResponse{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(config.ExplorerServiceTokenHeader, s.serviceToken)
	response, err := s.btcIntentClient.Do(request)
	if err != nil {
		return btcIntentRegistrationResponse{}, err
	}
	defer response.Body.Close()
	var result btcIntentRegistrationResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&result); err != nil {
		return btcIntentRegistrationResponse{}, fmt.Errorf("decode monitoring response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || result.Error != "" {
		if result.Error == "" {
			result.Error = response.Status
		}
		return btcIntentRegistrationResponse{}, fmt.Errorf("monitoring service rejected registration: %s", result.Error)
	}
	return result, nil
}

func (s *server) depositAddressContract(ctx context.Context, w http.ResponseWriter, req *depositAddressRequest) {
	candidates, chainUsed := s.probeActiveVaults(ctx, req.Node, req.Chain)
	if len(candidates) == 0 {
		writeJSON(w, depositAddressResponse{Error: fmt.Sprintf("no active vault found for chain %s", req.Chain)})
		return
	}

	var cfg pb.FrostVaultConfig
	if err := s.fetchProto(ctx, req.Node, "/frost/vault/config?chain="+chainUsed, nil, &cfg); err != nil {
		writeJSON(w, depositAddressResponse{Error: "failed to fetch vault config: " + err.Error()})
		return
	}
	version := cfg.DepositProtocol
	if version != protocol.DepositProtocolVault || cfg.DepositDexChainId != s.walletChainID() || cfg.EvmChainId == 0 ||
		config.ValidateDepositConfig(chainUsed, cfg.DepositProtocol, cfg.DepositDexChainId, cfg.VaultRefs) != nil {
		writeJSON(w, depositAddressResponse{Error: "secure deposit entry is not configured for this chain; do not transfer directly to the vault"})
		return
	}

	var picked *vaultCandidate
	for i := range candidates {
		c := &candidates[i]
		if int(c.id) < len(cfg.VaultRefs) && strings.TrimSpace(cfg.VaultRefs[c.id]) != "" {
			picked = c
			break
		}
	}
	if picked == nil {
		writeJSON(w, depositAddressResponse{Error: fmt.Sprintf("no vault contract address configured for chain %s", req.Chain)})
		return
	}

	writeJSON(w, depositAddressResponse{
		VaultID:        picked.id,
		VaultPubkey:    hex.EncodeToString(picked.groupPubkey),
		VaultAddress:   strings.TrimSpace(cfg.VaultRefs[picked.id]),
		DexChainID:     strconv.FormatUint(cfg.DepositDexChainId, 10),
		DepositVersion: version,
		EVMChainID:     cfg.EvmChainId,
	})
}

func (s *server) btcNetworkForChain(ctx context.Context, node, chain string) string {
	if chain == "" {
		chain = "btc"
	}
	chain = frostchain.NormalizeChain(chain)
	var cfg pb.FrostVaultConfig
	if node != "" {
		if err := s.fetchProto(ctx, node, "/frost/vault/config?chain="+chain, nil, &cfg); err == nil && strings.TrimSpace(cfg.BtcNetwork) != "" {
			return cfg.BtcNetwork
		}
	}
	if local := s.localChainConfig(chain); strings.TrimSpace(local.BTCNetwork) != "" {
		return local.BTCNetwork
	}
	return "mainnet"
}

func (s *server) localChainConfig(chain string) config.ChainFrostConfig {
	if s == nil || s.cfg == nil {
		return config.ChainFrostConfig{}
	}
	chains := s.cfg.Frost.Chains
	normalized := frostchain.NormalizeChain(chain)
	for _, key := range uniqueNonEmptyStrings(normalized, strings.ToLower(strings.TrimSpace(chain)), strings.ToUpper(strings.TrimSpace(chain)), strings.TrimSpace(chain)) {
		if c, ok := chains[key]; ok {
			return c
		}
	}
	return config.ChainFrostConfig{}
}

func uniqueNonEmptyStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func btcNetParams(network string) (*chaincfg.Params, error) {
	switch strings.ToLower(strings.TrimSpace(network)) {
	case "", "main", "mainnet", "btc", "bitcoin":
		return &chaincfg.MainNetParams, nil
	case "test", "testnet", "testnet3", "btc_testnet":
		return &chaincfg.TestNet3Params, nil
	case "regtest", "regression":
		return &chaincfg.RegressionNetParams, nil
	case "signet", "sig":
		return &chaincfg.SigNetParams, nil
	default:
		return nil, fmt.Errorf("unsupported btc network %q", network)
	}
}
