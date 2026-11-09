package main

import (
	"net/http"
	"net/url"
	"strconv"
)

                                                                          
                                                                              
func (s *server) handleFrostVaultUTXOs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSONStatus(w, http.StatusMethodNotAllowed, APIError{Error: "method not allowed"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if len(r.URL.RawQuery) > 4096 {
		writeJSONStatus(w, http.StatusBadRequest, APIError{Error: "UTXO query exceeds limit"})
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeJSONStatus(w, http.StatusBadRequest, APIError{Error: "invalid UTXO query encoding"})
		return
	}
	for key, values := range query {
		if key != "node" && key != "chain" && key != "vault_id" && key != "limit" && key != "cursor" || len(values) != 1 {
			writeJSONStatus(w, http.StatusBadRequest, APIError{Error: "unknown or duplicate UTXO query parameter"})
			return
		}
	}
	vault, vaultErr := strconv.ParseUint(query.Get("vault_id"), 10, 32)
	if query.Get("chain") != "btc" || vaultErr != nil || strconv.FormatUint(vault, 10) != query.Get("vault_id") {
		writeJSONStatus(w, http.StatusBadRequest, APIError{Error: "invalid BTC vault"})
		return
	}
	if query.Has("limit") {
		limit, err := strconv.ParseUint(query.Get("limit"), 10, 16)
		if err != nil || limit == 0 || limit > 200 || strconv.FormatUint(limit, 10) != query.Get("limit") {
			writeJSONStatus(w, http.StatusBadRequest, APIError{Error: "limit must be between 1 and 200"})
			return
		}
	}
	if len(query.Get("cursor")) > 1024 {
		writeJSONStatus(w, http.StatusBadRequest, APIError{Error: "cursor exceeds limit"})
		return
	}
	node := s.pickNode(query.Get("node"))
	if node == "" {
		writeJSONStatus(w, http.StatusBadRequest, APIError{Error: "missing or disallowed node parameter"})
		return
	}
	query.Del("node")
	endpoint := &url.URL{Scheme: "https", Host: node, Path: "/frost/vault/utxos", RawQuery: query.Encode()}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, endpoint.String(), nil)
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, APIError{Error: "failed to create UTXO request"})
		return
	}
	response, err := s.nodeDo(request)
	if err != nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, APIError{Error: "UTXO node unavailable"})
		return
	}
	writeBoundedNodeResponse(w, response)
}
