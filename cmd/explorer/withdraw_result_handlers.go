package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"dex/frost/withdrawresult"
)

                                                                             
                                                                      
func (s *server) handleFrostWithdrawResult(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSONStatus(w, http.StatusMethodNotAllowed, APIError{Error: "method not allowed"})
		return
	}
	wid, txid := r.URL.Query().Get("withdraw_id"), r.URL.Query().Get("request_tx_id")
	mode := r.URL.Query().Get("summary_only")
	if mode != "" && mode != "1" {
		writeJSONStatus(w, http.StatusBadRequest, APIError{Error: "invalid summary_only"})
		return
	}
	if (wid == "" && txid == "") || (wid != "" && !withdrawresult.ValidID(wid)) || (txid != "" && !withdrawresult.ValidID(txid)) {
		writeJSONStatus(w, http.StatusBadRequest, APIError{Error: "invalid withdraw_id or request_tx_id"})
		return
	}
	node := s.pickNode(r.URL.Query().Get("node"))
	if node == "" {
		writeJSONStatus(w, http.StatusBadRequest, APIError{Error: "node is not allowed"})
		return
	}
	timeout := s.timeout
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	params := url.Values{}
	if mode == "1" {
		params.Set("summary_only", mode)
	}
	if wid != "" {
		params.Set("withdraw_id", wid)
	}
	if txid != "" {
		params.Set("request_tx_id", txid)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+node+"/frost/withdraw/result?"+params.Encode(), nil)
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, APIError{Error: "invalid node endpoint"})
		return
	}
	resp, err := s.nodeDo(req)
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, APIError{Error: "withdrawal result unavailable"})
		return
	}
	defer resp.Body.Close()
	body, err := readResponseBody(resp)
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, APIError{Error: "withdrawal result exceeds response budget"})
		return
	}
	if resp.StatusCode != http.StatusOK {
		status := resp.StatusCode
		if status < 400 || status > 599 {
			status = http.StatusBadGateway
		}
		var upstream APIError
		if json.Unmarshal(body, &upstream) != nil || upstream.Error == "" {
			upstream.Error = "withdrawal result unavailable; use an archive node for pruned signing data"
		}
		writeJSONStatus(w, status, upstream)
		return
	}
	var result withdrawresult.Result
	if json.Unmarshal(body, &result) != nil || result.Version != 1 || !withdrawresult.ValidID(result.WithdrawID) ||
		(wid != "" && result.WithdrawID != wid) || (txid != "" && result.RequestTxID != txid) ||
		(result.Status != "QUEUED" && result.Status != "SIGNING" && result.Status != "SIGNED" && result.Status != "CANCELLED" && result.Status != "PARTIALLY_CANCELLED") {
		writeJSONStatus(w, http.StatusBadGateway, APIError{Error: "invalid withdrawal result from node"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, result)
}
