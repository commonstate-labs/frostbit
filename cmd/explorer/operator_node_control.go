package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

const operatorNodePauseResponseBodyLimit = int64(4 << 10)

var (
	errOperatorNodePauseRequestTooLarge  = errors.New("request body exceeds Explorer admin limit")
	errOperatorNodePauseResponseTooLarge = errors.New("node pause response exceeds Explorer limit")
)

type operatorNodePauseRequest struct {
	Nodes  []string `json:"nodes"`
	Paused *bool    `json:"paused"`
}

type operatorNodePauseResult struct {
	Node   string `json:"node"`
	OK     bool   `json:"ok"`
	Paused bool   `json:"paused"`
	Error  string `json:"error,omitempty"`
}

type operatorNodePauseResponse struct {
	OK      bool                      `json:"ok"`
	Paused  bool                      `json:"paused"`
	Results []operatorNodePauseResult `json:"results"`
}

                                                                          
                                                                             
func (s *server) handleOperatorNodePause(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	request, err := decodeOperatorNodePauseRequest(r)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errOperatorNodePauseRequestTooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		writeJSONStatus(w, status, map[string]string{"error": err.Error()})
		return
	}
	if request.Paused == nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "paused is required"})
		return
	}
	targets, err := s.resolveOperatorPauseNodes(request.Nodes)
	if err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	paused := *request.Paused
	results := make([]operatorNodePauseResult, len(targets))
	var wg sync.WaitGroup
	for index, node := range targets {
		index, node := index, node
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[index] = s.pauseConfiguredNode(r.Context(), node, paused)
		}()
	}
	wg.Wait()

	actor := simulatorOperatorAddress(r)
	succeeded := 0
	for _, result := range results {
		if result.OK {
			succeeded++
		}
		log.Printf("[ExplorerOperator] actor=%s action=node_pause node=%s paused=%t ok=%t error=%q", actor, result.Node, paused, result.OK, result.Error)
	}
	writeJSONStatus(w, http.StatusOK, operatorNodePauseResponse{
		OK:      succeeded == len(results),
		Paused:  paused,
		Results: results,
	})
}

func decodeOperatorNodePauseRequest(r *http.Request) (operatorNodePauseRequest, error) {
	var request operatorNodePauseRequest
	if r == nil || r.Body == nil {
		return request, errors.New("request body is required")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, explorerAdminBodyLimit+1))
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			return request, errOperatorNodePauseRequestTooLarge
		}
		return request, errors.New("invalid request body")
	}
	if int64(len(body)) > explorerAdminBodyLimit {
		return request, errOperatorNodePauseRequestTooLarge
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return request, errors.New("invalid JSON body")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return request, errors.New("request body must contain one JSON object")
	}
	return request, nil
}

func (s *server) resolveOperatorPauseNodes(rawNodes []string) ([]string, error) {
	if len(rawNodes) == 0 {
		return nil, errors.New("at least one node is required")
	}
	if len(rawNodes) > explorerAdminBroadcastMaxNodes {
		return nil, fmt.Errorf("node count exceeds %d", explorerAdminBroadcastMaxNodes)
	}
	targets := make([]string, 0, len(rawNodes))
	seen := make(map[string]struct{}, len(rawNodes))
	for _, raw := range rawNodes {
		if strings.TrimSpace(raw) == "" {
			return nil, errors.New("node must not be empty")
		}
		node := s.pickNode(raw)
		if node == "" {
			return nil, fmt.Errorf("node %q is not allowed", raw)
		}
		if _, duplicate := seen[node]; duplicate {
			continue
		}
		seen[node] = struct{}{}
		targets = append(targets, node)
	}
	if len(targets) == 0 {
		return nil, errors.New("at least one node is required")
	}
	return targets, nil
}

func (s *server) pauseConfiguredNode(ctx context.Context, node string, paused bool) operatorNodePauseResult {
	result := operatorNodePauseResult{Node: node, Paused: paused}
	payload, err := json.Marshal(struct {
		Paused bool `json:"paused"`
	}{Paused: paused})
	if err != nil {
		result.Error = "failed to encode pause request"
		return result
	}
	endpoint := (&url.URL{Scheme: "https", Host: node, Path: "/pause"}).String()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		result.Error = "failed to create pause request"
		return result
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := s.nodeDo(request)
	if err != nil {
		result.Error = "node unavailable: " + err.Error()
		return result
	}
	if response == nil {
		result.Error = "node returned no response"
		return result
	}
	defer response.Body.Close()
	body, err := readOperatorNodePauseResponse(response.Body)
	if err != nil {
		if errors.Is(err, errOperatorNodePauseResponseTooLarge) {
			result.Error = errOperatorNodePauseResponseTooLarge.Error()
		} else {
			result.Error = "failed to read node pause response"
		}
		return result
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		result.Error = fmt.Sprintf("node returned status %d", response.StatusCode)
		if detail := boundedOperatorNodeError(body); detail != "" {
			result.Error += ": " + detail
		}
		return result
	}
	var nodeResponse struct {
		OK     bool   `json:"ok"`
		Paused *bool  `json:"paused"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(body, &nodeResponse); err != nil {
		result.Error = "node returned invalid JSON"
		return result
	}
	if !nodeResponse.OK {
		result.Error = strings.TrimSpace(nodeResponse.Error)
		if result.Error == "" {
			result.Error = "node rejected pause request"
		}
		return result
	}
	if nodeResponse.Paused == nil || *nodeResponse.Paused != paused {
		result.Error = "node returned an unexpected pause state"
		return result
	}
	result.OK = true
	return result
}

func readOperatorNodePauseResponse(body io.Reader) ([]byte, error) {
	if body == nil {
		return nil, errors.New("node returned an empty response body")
	}
	payload, err := io.ReadAll(io.LimitReader(body, operatorNodePauseResponseBodyLimit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) > operatorNodePauseResponseBodyLimit {
		return nil, errOperatorNodePauseResponseTooLarge
	}
	return payload, nil
}

func boundedOperatorNodeError(body []byte) string {
	const maxErrorBytes = 256
	detail := strings.TrimSpace(string(body))
	if len(detail) > maxErrorBytes {
		detail = detail[:maxErrorBytes] + "..."
	}
	return detail
}
