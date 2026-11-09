package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"dex/logs"
	"dex/pb"
	"dex/txverify"
	"dex/vm"
)

const defaultDebugMinerJoinAmount = "3200000000"

const invalidDebugMinerOp pb.OrderOp = -1

                                                                              
func (hm *HandlerManager) HandleDebugToggleMiner(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]string{"error": "method not allowed"})
		return
	}

	op, amount, err := parseDebugMinerRequest(r)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	joinAdmitted := false
	if op == pb.OrderOp_ADD {
		                                                                     
		                                                                      
		                                                                   
		defer func() {
			if !joinAdmitted {
				hm.resumeDebugMinerConsensus("", "join rejected before admission")
			}
		}()
	}

	if hm == nil || hm.keyMgr == nil || hm.txPool == nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "missing key manager or txpool"})
		return
	}

	pending, err := hm.txPool.AccountPendingStatus(hm.address)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "failed to resolve local account nonce: " + err.Error()})
		return
	}

	anyTx, txID, err := hm.buildDebugMinerTx(op, amount, pending.NextNonce)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "failed to build miner tx: " + err.Error()})
		return
	}

	submit := hm.txPool.SubmitLocalTx
	if op == pb.OrderOp_ADD {
		submit = hm.txPool.SubmitLocalBootstrapMinerTx
	}
	accepted, err := submit(anyTx, func(txID string) {
		if hm.senderManager != nil {
			hm.senderManager.BroadcastTx(anyTx)
		}
	})
	if err != nil || !accepted {
		w.WriteHeader(http.StatusInternalServerError)
		if err == nil {
			err = fmt.Errorf("local transaction was not admitted")
		}
		json.NewEncoder(w).Encode(map[string]string{"error": "failed to submit tx: " + err.Error()})
		return
	}

	logs.Info("[Debug] Node miner status change (Op: %v) with txID: %s", op, txID)
	if hm.consensusManager != nil {
		if op == pb.OrderOp_ADD {
			                                                                      
			                                                                    
			                                                                     
			                                             
			hm.resumeDebugMinerConsensus(txID, "join admitted")
		} else {
			hm.consensusManager.SetPaused(true)
		}
	}
	joinAdmitted = true
	resume := "running_after_admission"
	if op != pb.OrderOp_ADD {
		resume = "paused_after_stop"
	}
	paused := hm.consensusManager != nil && hm.consensusManager.IsPaused()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":               true,
		"tx_id":            txID,
		"op":               op.String(),
		"amount":           amount,
		"consensus_paused": paused,
		"resume":           resume,
	})
}

                                                                              
                                                                               
                                                                           
func parseDebugMinerRequest(r *http.Request) (pb.OrderOp, string, error) {
	if r == nil || r.URL == nil {
		return invalidDebugMinerOp, "", fmt.Errorf("invalid request")
	}
	if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		return invalidDebugMinerOp, "", fmt.Errorf("request body is not accepted")
	}
	query := r.URL.Query()
	for key, values := range query {
		if key != "op" && key != "amount" {
			return invalidDebugMinerOp, "", fmt.Errorf("unsupported query parameter %q", key)
		}
		if len(values) != 1 {
			return invalidDebugMinerOp, "", fmt.Errorf("query parameter %q must appear exactly once", key)
		}
	}
	switch query.Get("op") {
	case "join":
		amount := defaultDebugMinerJoinAmount
		if custom := strings.TrimSpace(query.Get("amount")); custom != "" {
			amount = custom
		}
		return pb.OrderOp_ADD, amount, nil
	case "stop":
		if strings.TrimSpace(query.Get("amount")) != "" {
			return invalidDebugMinerOp, "", fmt.Errorf("stop does not accept an amount")
		}
		return pb.OrderOp_REMOVE, "", nil
	default:
		return invalidDebugMinerOp, "", fmt.Errorf("missing or invalid 'op' query param (expected 'join' or 'stop')")
	}
}

func (hm *HandlerManager) buildDebugMinerTx(op pb.OrderOp, amount string, nonce uint64) (*pb.AnyTx, string, error) {
	if hm == nil || !hm.keyMgr.HasPrivateKey() {
		return nil, "", fmt.Errorf("missing local key manager")
	}
	if hm.address == "" || hm.keyMgr.GetAddress() != hm.address {
		return nil, "", fmt.Errorf("local key manager does not match node address")
	}
	base := &pb.BaseMessage{
		FromAddress: hm.address,
		Nonce:       nonce,
		Fee:         "1000",
		ChainId:     pb.GetDefaultChainID(),
		PublicKeys: &pb.PublicKeys{Keys: map[int32][]byte{
			int32(pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1_DEX): append([]byte(nil), hm.keyMgr.PublicKeyBytes()...),
		}},
	}
	minerTx := &pb.MinerTx{Base: base, Op: op, Amount: strings.TrimSpace(amount)}

	if op == pb.OrderOp_ADD {
		endpoint := strings.TrimSpace(hm.nodeAdvertiseAddr)
		if endpoint == "" {
			return nil, "", fmt.Errorf("local advertise endpoint is not configured")
		}
		blsPublicKey, err := hm.keyMgr.BLSPublicKeyBytes()
		if err != nil {
			return nil, "", fmt.Errorf("derive local BLS public key: %w", err)
		}
		minerTx.RewardAddress = hm.address
		minerTx.BlsPublicKey = blsPublicKey
		minerTx.Endpoint = endpoint
		digest, err := vm.MinerRegistrationPoPDigest(minerTx)
		if err != nil {
			return nil, "", fmt.Errorf("validate local miner identity: %w", err)
		}
		minerTx.BlsProof, err = hm.keyMgr.SignBLS(digest)
		if err != nil {
			return nil, "", fmt.Errorf("sign local BLS proof: %w", err)
		}
	} else if op != pb.OrderOp_REMOVE {
		return nil, "", fmt.Errorf("unsupported miner operation %s", op.String())
	}

	anyTx := &pb.AnyTx{Content: &pb.AnyTx_MinerTx{MinerTx: minerTx}}
	txID, err := txverify.SignWithKeyManager(anyTx, hm.keyMgr)
	if err != nil {
		return nil, "", fmt.Errorf("sign miner transaction: %w", err)
	}
	return anyTx, txID, nil
}

func (hm *HandlerManager) resumeDebugMinerConsensus(txID, reason string) {
	if hm == nil || hm.consensusManager == nil {
		return
	}
	wasPaused := hm.consensusManager.IsPaused()
	hm.consensusManager.SetPaused(false)
	txLabel := txID
	if txLabel == "" {
		txLabel = "unsubmitted"
	}
	if wasPaused && hm.consensusManager.IsPaused() {
		                                                                     
		                                                     
		logs.Warn("[Debug] MinerTx %s %s; ordinary pause cleared but emergency stop remains active", txLabel, reason)
		return
	}
	logs.Info("[Debug] MinerTx %s %s; consensus running", txLabel, reason)
}
