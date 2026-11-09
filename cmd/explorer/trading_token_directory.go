package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	tradingTokenDirectoryPageSize          = 4096
	tradingTokenDirectoryMaxPages          = 512
	tradingTokenDirectoryMaxRows           = 1_100_000
	tradingTokenDirectoryMaxEstimatedBytes = int64(480 << 20)
	tradingTokenDirectoryCacheBytes        = int64(512 << 20)
	tradingTokenDirectoryTTL               = 10 * time.Minute
	tradingTokenDirectoryLoadTimeout       = 2 * time.Minute
	tradingTokenAddressQueryLimit          = 256
	tradingTokenTypePageDefault            = 256
	tradingTokenTypePageMax                = 1024
)

                                                                             
                                                                               
                                                                           
                                                          
type tradingTokenDirectory struct {
	byAddress      map[string]tradingPairToken
	order          []string
	byType         map[string][]string
	estimatedBytes int64
}

type tradingTokenDirectoryPage struct {
	Tokens     []tradingPairToken `json:"tokens"`
	NextCursor string             `json:"next_cursor,omitempty"`
}

type tradingTokenDirectoryUpstreamError struct {
	status int
	msg    string
}

func (e *tradingTokenDirectoryUpstreamError) Error() string {
	if e == nil {
		return "token directory request failed"
	}
	return e.msg
}

func newTradingTokenDirectory() *tradingTokenDirectory {
	return &tradingTokenDirectory{
		byAddress: make(map[string]tradingPairToken),
		byType:    make(map[string][]string),
	}
}

func (d *tradingTokenDirectory) add(token tradingPairToken) error {
	if d == nil {
		return errors.New("token directory is nil")
	}
	                                                                         
	                                                                     
	token.IconURL = ""
	if token.Address == "" || strings.TrimSpace(token.Address) != token.Address {
		return errors.New("token directory contains an invalid address")
	}
	if len(token.Address) > 4096 {
		return fmt.Errorf("token address exceeds %d bytes", 4096)
	}
	if token.Type == "" {
		token.Type = "native"
	}
	if _, duplicate := d.byAddress[token.Address]; duplicate {
		return fmt.Errorf("token directory contains duplicate address %q", token.Address)
	}

	                                                                          
	                                                                         
	weight := int64(192 + len(token.Address) + len(token.Type) + len(token.NativeChain) + len(token.NativeContract))
	if d.estimatedBytes+weight > tradingTokenDirectoryMaxEstimatedBytes {
		return fmt.Errorf("token directory exceeds memory budget %d", tradingTokenDirectoryMaxEstimatedBytes)
	}
	d.estimatedBytes += weight
	d.byAddress[token.Address] = token
	d.order = append(d.order, token.Address)
	d.byType[token.Type] = append(d.byType[token.Type], token.Address)
	return nil
}

func (s *server) ensureTradingTokenDirectoryCache() *boundedTTLCache[*tradingTokenDirectory] {
	s.tokenDirectoryCacheOnce.Do(func() {
		if s.tokenDirectoryCache != nil {
			return
		}
		entries := len(s.defaultNodes)
		if entries < 1 {
			entries = 1
		}
		s.tokenDirectoryCache = newBoundedTTLCache[*tradingTokenDirectory](entries, tradingTokenDirectoryCacheBytes, 2)
	})
	return s.tokenDirectoryCache
}

func (s *server) getTradingTokenDirectory(ctx context.Context, node string) (*tradingTokenDirectory, error) {
	cache := s.ensureTradingTokenDirectoryCache()
	return cache.GetOrLoad(ctx, node, tradingTokenDirectoryTTL, func(loadCtx context.Context) (*tradingTokenDirectory, int64, error) {
		                                                                         
		                                                                           
		sharedCtx, cancel := context.WithTimeout(context.WithoutCancel(loadCtx), tradingTokenDirectoryLoadTimeout)
		defer cancel()
		directory, err := s.fetchTradingTokenDirectory(sharedCtx, node)
		if err != nil {
			return nil, 1, err
		}
		return directory, maxInt64(1, directory.estimatedBytes), nil
	})
}

func (s *server) fetchTradingTokenDirectory(ctx context.Context, node string) (*tradingTokenDirectory, error) {
	directory := newTradingTokenDirectory()
	cursor := ""
	seenCursors := make(map[string]struct{})

	for pageNumber := 0; pageNumber < tradingTokenDirectoryMaxPages; pageNumber++ {
		endpoint := &url.URL{Scheme: "https", Host: node, Path: "/listtokens"}
		query := endpoint.Query()
		query.Set("limit", strconv.Itoa(tradingTokenDirectoryPageSize))
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		endpoint.RawQuery = query.Encode()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			return nil, err
		}
		resp, err := s.nodeDo(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := readResponseBody(resp)
			_ = resp.Body.Close()
			return nil, &tradingTokenDirectoryUpstreamError{
				status: resp.StatusCode,
				msg:    fmt.Sprintf("token directory returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body))),
			}
		}
		body, readErr := readResponseBody(resp)
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read token directory page: %w", readErr)
		}

		page, legacy, decodeErr := decodeTradingTokenDirectoryPage(body)
		if decodeErr != nil {
			return nil, decodeErr
		}
		if len(page.Tokens) == 0 && page.NextCursor != "" {
			return nil, errors.New("token directory returned an empty non-terminal page")
		}
		if len(directory.order)+len(page.Tokens) > tradingTokenDirectoryMaxRows {
			return nil, fmt.Errorf("token directory exceeds row limit %d", tradingTokenDirectoryMaxRows)
		}
		for _, token := range page.Tokens {
			if err := directory.add(token); err != nil {
				return nil, err
			}
		}

		if legacy || page.NextCursor == "" {
			return directory, nil
		}
		if page.NextCursor == cursor || len(page.NextCursor) > 4096 {
			return nil, errors.New("token directory returned an invalid cursor")
		}
		if _, duplicate := seenCursors[page.NextCursor]; duplicate {
			return nil, errors.New("token directory cursor repeated")
		}
		seenCursors[page.NextCursor] = struct{}{}
		cursor = page.NextCursor
	}

	return nil, fmt.Errorf("token directory exceeds page limit %d", tradingTokenDirectoryMaxPages)
}

func decodeTradingTokenDirectoryPage(body []byte) (tradingTokenDirectoryPage, bool, error) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return tradingTokenDirectoryPage{}, false, errors.New("token directory returned an empty response")
	}
	if trimmed[0] == '[' {
		var tokens []tradingPairToken
		if err := json.Unmarshal(body, &tokens); err != nil {
			return tradingTokenDirectoryPage{}, true, fmt.Errorf("decode legacy token directory: %w", err)
		}
		return tradingTokenDirectoryPage{Tokens: tokens}, true, nil
	}

	var envelope struct {
		Tokens     json.RawMessage `json:"tokens"`
		NextCursor string          `json:"next_cursor,omitempty"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return tradingTokenDirectoryPage{}, false, fmt.Errorf("decode token directory page: %w", err)
	}
	if len(envelope.Tokens) == 0 || string(envelope.Tokens) == "null" {
		return tradingTokenDirectoryPage{}, false, errors.New("token directory page is missing tokens")
	}
	var tokens []tradingPairToken
	if err := json.Unmarshal(envelope.Tokens, &tokens); err != nil {
		return tradingTokenDirectoryPage{}, false, fmt.Errorf("decode token directory tokens: %w", err)
	}
	return tradingTokenDirectoryPage{Tokens: tokens, NextCursor: envelope.NextCursor}, false, nil
}

func tradingTokenDirectoryStatus(err error) int {
	var upstream *tradingTokenDirectoryUpstreamError
	if errors.As(err, &upstream) {
		if upstream.status == http.StatusRequestEntityTooLarge {
			return upstream.status
		}
		return http.StatusBadGateway
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return http.StatusGatewayTimeout
	}
	if errors.Is(err, errExplorerCacheBusy) {
		return http.StatusServiceUnavailable
	}
	return http.StatusBadGateway
}

func tokensReferencedByPairs(directory *tradingTokenDirectory, pairs []tradingPairInfo) []tradingPairToken {
	byAddress := make(map[string]tradingPairToken, len(pairs)*2)
	for _, pair := range pairs {
		for _, address := range []string{pair.BaseToken, pair.QuoteToken} {
			if address == "" {
				continue
			}
			if _, seen := byAddress[address]; seen {
				continue
			}
			if directory != nil {
				if token, ok := directory.byAddress[address]; ok {
					byAddress[address] = token
					continue
				}
			}
			                                                                      
			                                                                      
			                                             
			byAddress[address] = tradingPairToken{Address: address, Type: "native"}
		}
	}

	tokens := make([]tradingPairToken, 0, len(byAddress))
	for _, token := range byAddress {
		tokens = append(tokens, token)
	}
	sort.Slice(tokens, func(i, j int) bool {
		left, right := tradingTokenTypeOrder(tokens[i].Type), tradingTokenTypeOrder(tokens[j].Type)
		if left != right {
			return left < right
		}
		return tokens[i].Address < tokens[j].Address
	})
	return tokens
}

func tradingTokenTypeOrder(tokenType string) int {
	switch tokenType {
	case "system":
		return 0
	case "bridged":
		return 1
	default:
		return 2
	}
}

func (s *server) handleTokens(w http.ResponseWriter, r *http.Request) {
	node := s.pickNode(r.URL.Query().Get("node"))
	if node == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "node is required"})
		return
	}

	tokenType := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("type")))
	addressesRaw := strings.TrimSpace(r.URL.Query().Get("addresses"))
	if (tokenType == "") == (addressesRaw == "") {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "exactly one of type=bridged or addresses is required"})
		return
	}
	if tokenType != "" && tokenType != "bridged" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "type must be bridged"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()
	directory, err := s.getTradingTokenDirectory(ctx, node)
	if err != nil {
		writeJSONStatus(w, tradingTokenDirectoryStatus(err), map[string]string{"error": err.Error()})
		return
	}

	if tokenType == "bridged" {
		s.handleTokensByType(w, r, node, directory, tokenType)
		return
	}
	s.handleTokensByAddress(w, node, directory, addressesRaw)
}

func (s *server) handleTokensByType(w http.ResponseWriter, r *http.Request, node string, directory *tradingTokenDirectory, tokenType string) {
	limit := tradingTokenTypePageDefault
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 || parsed > tradingTokenTypePageMax {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("limit must be between 1 and %d", tradingTokenTypePageMax)})
			return
		}
		limit = parsed
	}
	offset := 0
	if cursor := strings.TrimSpace(r.URL.Query().Get("cursor")); cursor != "" {
		parsed, err := strconv.Atoi(cursor)
		if err != nil || parsed < 0 {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "invalid cursor"})
			return
		}
		offset = parsed
	}
	ordered := directory.byType[tokenType]
	if offset > len(ordered) {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "cursor is outside token directory"})
		return
	}
	end := offset + limit
	if end > len(ordered) {
		end = len(ordered)
	}
	tokens := make([]tradingPairToken, 0, end-offset)
	for _, address := range ordered[offset:end] {
		tokens = append(tokens, tokenWithIconURL(node, directory.byAddress[address]))
	}
	response := tradingTokenDirectoryPage{Tokens: tokens}
	if end < len(ordered) {
		response.NextCursor = strconv.Itoa(end)
	}
	writeJSON(w, response)
}

func (s *server) handleTokensByAddress(w http.ResponseWriter, node string, directory *tradingTokenDirectory, raw string) {
	parts := strings.Split(raw, ",")
	if len(parts) > tradingTokenAddressQueryLimit {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("addresses exceeds limit %d", tradingTokenAddressQueryLimit)})
		return
	}
	seen := make(map[string]struct{}, len(parts))
	tokens := make([]tradingPairToken, 0, len(parts))
	for _, part := range parts {
		address := strings.TrimSpace(part)
		if address == "" || len(address) > 4096 {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "addresses contains an invalid token address"})
			return
		}
		if _, duplicate := seen[address]; duplicate {
			continue
		}
		seen[address] = struct{}{}
		if token, ok := directory.byAddress[address]; ok {
			tokens = append(tokens, tokenWithIconURL(node, token))
		}
	}
	writeJSON(w, tradingTokenDirectoryPage{Tokens: tokens})
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
