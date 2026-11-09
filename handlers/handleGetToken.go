package handlers

import (
	"dex/interfaces"
	"dex/keys"
	"dex/pb"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"
)

                                                 
func (hm *HandlerManager) HandleGetToken(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("HandleGetToken")
	if !hm.checkAuth(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	var req pb.GetTokenRequest
	if err := pb.UnmarshalSanitize(bodyBytes, &req); err != nil {
		http.Error(w, "Invalid GetTokenRequest proto", http.StatusBadRequest)
		return
	}

	token, err := hm.dbManager.GetToken(req.TokenAddress)
	if err != nil {
		http.Error(w, "Failed to get token: "+err.Error(), http.StatusInternalServerError)
		return
	}

	resp := &pb.GetTokenResponse{
		Token: token,
	}

	respData, _ := proto.Marshal(resp)
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	w.Write(respData)
}

                                                            
func (hm *HandlerManager) HandleGetTokenRegistry(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("HandleGetTokenRegistry")
	if !hm.checkAuth(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	registry, err := hm.dbManager.GetTokenRegistry()
	if err != nil {
		http.Error(w, "Failed to get token registry: "+err.Error(), http.StatusInternalServerError)
		return
	}

	resp := &pb.GetTokenRegistryResponse{
		Registry: registry,
	}

	respData, _ := proto.Marshal(resp)
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	w.Write(respData)
}

                                                                       
type tokenSummary struct {
	Address        string `json:"address"`
	Type           string `json:"type"`                                   
	NativeChain    string `json:"native_chain,omitempty"`
	NativeContract string `json:"native_contract,omitempty"`
}

const (
	maxListTokenRows         = 4096
	defaultListTokenPageSize = 1024
	maxListTokenPageSize     = 4096
)

type tokenOrderedScanFunc func(prefix string, limit int, reverse bool) ([]interfaces.KVPair, error)
type tokenOrderedScanAfterFunc func(prefix, startAfter string, limit int, reverse bool) ([]interfaces.KVPair, error)

type tokenPageResponse struct {
	Tokens     []tokenSummary `json:"tokens"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

                                                                                
func (hm *HandlerManager) HandleListTokens(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("HandleListTokens")
	query := r.URL.Query()
	_, hasLimit := query["limit"]
	_, hasCursor := query["cursor"]
	if hasLimit || hasCursor {
		handleListTokensPageWithScan(w, r, hm.dbManager.ScanKVWithLimitOrderedAfter)
		return
	}
	handleListTokensWithScan(w, hm.dbManager.ScanKVWithLimitOrdered)
}

func handleListTokensPageWithScan(w http.ResponseWriter, r *http.Request, scan tokenOrderedScanAfterFunc) {
	limit, err := parseListTokenPageSize(r.URL.Query().Get("limit"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	prefix := keys.KeyTokenPrefix()
	registryKey := keys.KeyTokenRegistry()
	cursor := r.URL.Query().Get("cursor")
	startAfter := ""
	if cursor != "" {
		startAfter = keys.KeyToken(cursor)
	}

	                                                                         
	                                                                      
	items, err := scan(prefix, startAfter, limit+2, false)
	if err != nil {
		http.Error(w, "scan tokens failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	logicalRows := make([]interfaces.KVPair, 0, limit+1)
	for _, item := range items {
		if item.Key == registryKey {
			continue
		}
		address := strings.TrimPrefix(item.Key, prefix)
		if address == "" || item.Key != prefix+address {
			continue
		}
		logicalRows = append(logicalRows, item)
		if len(logicalRows) == limit+1 {
			break
		}
	}

	hasMore := len(logicalRows) > limit
	if hasMore {
		logicalRows = logicalRows[:limit]
	}
	tokens := make([]tokenSummary, 0, len(logicalRows))
	for _, item := range logicalRows {
		var token pb.Token
		if err := proto.Unmarshal(item.Value, &token); err != nil {
			continue
		}
		tokens = append(tokens, summarizeToken(&token))
	}
	response := tokenPageResponse{Tokens: tokens}
	if hasMore && len(logicalRows) > 0 {
		response.NextCursor = strings.TrimPrefix(logicalRows[len(logicalRows)-1].Key, prefix)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func parseListTokenPageSize(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultListTokenPageSize, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 {
		return 0, fmt.Errorf("limit must be a positive integer")
	}
	if limit > maxListTokenPageSize {
		limit = maxListTokenPageSize
	}
	return limit, nil
}

func handleListTokensWithScan(w http.ResponseWriter, scan tokenOrderedScanFunc) {
	prefix := keys.KeyTokenPrefix()
	registryKey := keys.KeyTokenRegistry()
	                                                                           
	                                                           
	items, err := scan(prefix, maxListTokenRows+2, false)
	if err != nil {
		http.Error(w, "scan tokens failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	tokens := make([]tokenSummary, 0, len(items))
	tokenRows := 0
	for _, item := range items {
		if item.Key == registryKey {
			continue
		}
		tokenRows++
		if tokenRows > maxListTokenRows {
			http.Error(w, fmt.Sprintf("token directory exceeds hard limit %d", maxListTokenRows), http.StatusRequestEntityTooLarge)
			return
		}
		var token pb.Token
		if err := proto.Unmarshal(item.Value, &token); err != nil {
			continue
		}
		tokens = append(tokens, summarizeToken(&token))
	}

	                                                                               
	sort.Slice(tokens, func(i, j int) bool {
		if tokens[i].Type != tokens[j].Type {
			return tokenTypeOrder(tokens[i].Type) < tokenTypeOrder(tokens[j].Type)
		}
		return tokens[i].Address < tokens[j].Address
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tokens)
}

func summarizeToken(token *pb.Token) tokenSummary {
	typeName := "native"
	switch token.Type {
	case pb.TokenType_TOKEN_BRIDGED:
		typeName = "bridged"
	case pb.TokenType_TOKEN_SYSTEM:
		typeName = "system"
	}
	return tokenSummary{
		Address:        token.Address,
		Type:           typeName,
		NativeChain:    token.NativeChain,
		NativeContract: token.NativeContract,
	}
}

func tokenTypeOrder(t string) int {
	switch t {
	case "system":
		return 0
	case "bridged":
		return 1
	default:
		return 2
	}
}
