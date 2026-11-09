package handlers

import (
	"dex/keys"
	"dex/protocol"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

const spotMarketDirectoryLimit = 4096

type marketDirectoryEntry struct {
	Pair       string `json:"pair"`
	BaseToken  string `json:"base_token"`
	QuoteToken string `json:"quote_token"`
}

                                                                             
                                                                            
                                                       
func (hm *HandlerManager) HandleSpotMarkets(w http.ResponseWriter, _ *http.Request) {
	handleSpotMarketsWithScan(w, hm.dbManager.ScanKVWithLimitOrdered)
}

func handleSpotMarketsWithScan(w http.ResponseWriter, scan futureOrderedScanFunc) {
	pairs := make(map[string]struct{})
	markets := make(map[string]marketDirectoryEntry)
	sources := []struct {
		prefix         string
		explicitConfig bool
	}{
		{prefix: keys.KeySpotMarketConfigPrefix(), explicitConfig: true},
		{prefix: keys.KeySpotLastPricePrefix()},
	}
	for _, source := range sources {
		rows, err := scan(source.prefix, spotMarketDirectoryLimit+1, false)
		if err != nil {
			writeJSONError(w, "scan spot markets error: "+err.Error())
			return
		}
		if len(rows) > spotMarketDirectoryLimit {
			writeJSONStatusError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("spot market directory exceeds hard limit %d", spotMarketDirectoryLimit))
			return
		}
		for _, row := range rows {
			pair := strings.TrimPrefix(row.Key, source.prefix)
			if pair == "" || row.Key != source.prefix+pair {
				continue
			}
			pairs[pair] = struct{}{}
			if _, exists := markets[pair]; exists {
				continue
			}
			var (
				entry marketDirectoryEntry
				ok    bool
			)
			if source.explicitConfig {
				entry, ok = marketDirectoryEntryFromConfig(pair, row.Value)
			} else {
				entry, ok = legacyMarketDirectoryEntry(pair)
			}
			if ok {
				markets[pair] = entry
			}
		}
		if len(pairs) > spotMarketDirectoryLimit {
			writeJSONStatusError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("spot market directory exceeds hard limit %d", spotMarketDirectoryLimit))
			return
		}
	}
	ordered := make([]string, 0, len(pairs))
	for pair := range pairs {
		ordered = append(ordered, pair)
	}
	sort.Strings(ordered)
	structured := make([]marketDirectoryEntry, 0, len(markets))
	for _, pair := range ordered {
		if entry, ok := markets[pair]; ok {
			structured = append(structured, entry)
		}
	}
	writeJSONData(w, futureMarketsResponse{Pairs: ordered, Markets: structured})
}

func marketDirectoryEntryFromConfig(pair string, encoded []byte) (marketDirectoryEntry, bool) {
	identity, err := protocol.DecodeMarketIdentity(pair, encoded)
	if err == nil {
		return marketDirectoryEntry{
			Pair:       identity.Pair,
			BaseToken:  identity.BaseToken,
			QuoteToken: identity.QuoteToken,
		}, true
	}
	                                                                         
	                                                                        
	                                                        
	if len(encoded) != 0 && string(encoded) != pair {
		return marketDirectoryEntry{}, false
	}
	return legacyMarketDirectoryEntry(pair)
}

func legacyMarketDirectoryEntry(pair string) (marketDirectoryEntry, bool) {
	if strings.Count(pair, "_") != 1 {
		return marketDirectoryEntry{}, false
	}
	baseToken, quoteToken, ok := strings.Cut(pair, "_")
	if !ok {
		return marketDirectoryEntry{}, false
	}
	identity, err := protocol.NewMarketIdentity(baseToken, quoteToken)
	if err != nil || identity.Pair != pair {
		return marketDirectoryEntry{}, false
	}
	return marketDirectoryEntry{
		Pair:       identity.Pair,
		BaseToken:  identity.BaseToken,
		QuoteToken: identity.QuoteToken,
	}, true
}
