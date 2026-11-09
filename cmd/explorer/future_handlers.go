package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const explorerAdminBroadcastMaxNodes = 64

                                                           
                                                                        
func (s *server) handleFutureProxy(nodePath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		node := s.pickNode(r.URL.Query().Get("node"))
		if node == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "missing node parameter"})
			return
		}

		endpoint := &url.URL{Scheme: "https", Host: node, Path: nodePath, RawQuery: boundedExplorerProxyQuery(r.URL.Query()).Encode()}

		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, endpoint.String(), nil)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		resp, err := s.nodeDo(req)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"error": "node unavailable: " + err.Error()})
			return
		}
		writeBoundedNodeResponse(w, resp)
	}
}

                                                                           
                                                                              
                                                                               
                                                                         
func (s *server) handleMinerToggleProxy(w http.ResponseWriter, r *http.Request) {
	node := s.pickNode(r.URL.Query().Get("node"))
	if node == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "missing or disallowed node parameter"})
		return
	}
	op := strings.TrimSpace(r.URL.Query().Get("op"))
	if op != "join" && op != "stop" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "invalid miner operation"})
		return
	}

	query := make(url.Values, 1)
	query.Set("op", op)
	endpoint := &url.URL{Scheme: "https", Host: node, Path: "/debug/toggle_miner", RawQuery: query.Encode()}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, endpoint.String(), nil)
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": "failed to create miner request"})
		return
	}
	response, err := s.nodeDo(request)
	if err != nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]string{"error": "node unavailable: " + err.Error()})
		return
	}
	writeBoundedNodeResponse(w, response)
}

func boundedExplorerProxyQuery(values url.Values) url.Values {
	query := make(url.Values, len(values))
	for key, value := range values {
		query[key] = append([]string(nil), value...)
	}
	query.Del("node")
	clamp := func(key string, fallback, maximum int) {
		raw := query.Get(key)
		if raw == "" {
			return
		}
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			query.Set(key, strconv.Itoa(fallback))
			return
		}
		if value > maximum {
			query.Set(key, strconv.Itoa(maximum))
		}
	}
	clamp("limit", 100, explorerMarketTradesMaxLimit)
	clamp("count", 100, 500)
	clamp("offset", 0, explorerTxHistoryMaxOffset)
	return query
}

                                                                               
func (s *server) handleSimulatorConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	                                             
	if r.Method == http.MethodPost {
		                                                                     
		                                                                       
		body, err := io.ReadAll(io.LimitReader(r.Body, explorerAdminBodyLimit+1))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		if int64(len(body)) > explorerAdminBodyLimit {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			json.NewEncoder(w).Encode(map[string]string{"error": "request body exceeds Explorer admin limit"})
			return
		}
		nodes := s.defaultNodes
		if len(nodes) > explorerAdminBroadcastMaxNodes {
			nodes = nodes[:explorerAdminBroadcastMaxNodes]
		}
		for _, node := range nodes {
			nodeURL := fmt.Sprintf("https://%s/simulator/config", node)
			req, err := http.NewRequestWithContext(r.Context(), "POST", nodeURL, bytes.NewReader(body))
			if err != nil {
				continue
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := s.nodeDo(req)
			if err == nil {
				resp.Body.Close()
			}
		}
	}

	                                             
	node := r.URL.Query().Get("node")
	node = s.pickNode(node)
	if node == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "no node available"})
		return
	}
	nodeURL := fmt.Sprintf("https://%s/simulator/config", node)
	req, err := http.NewRequestWithContext(r.Context(), "GET", nodeURL, nil)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	resp, err := s.nodeDo(req)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "node unavailable: " + err.Error()})
		return
	}
	writeBoundedNodeResponse(w, resp)
}
