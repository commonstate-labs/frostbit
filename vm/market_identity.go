package vm

import (
	"dex/future"
	"dex/keys"
	"dex/protocol"
	"fmt"
	"sort"
	"strings"
)

const maxMarketIdentityTokens = 4096

func validateRegisteredMarketAssets(sv StateView, baseToken, quoteToken string) error {
	for _, asset := range []struct{ role, id string }{{"base", baseToken}, {"quote", quoteToken}} {
		role, assetID := asset.role, asset.id
		if _, exists, getErr := sv.Get(keys.KeyToken(assetID)); getErr != nil {
			return fmt.Errorf("read %s asset %s from token registry: %w", role, assetID, getErr)
		} else if !exists {
			return fmt.Errorf("%s asset is not registered: %s", role, assetID)
		}
	}
	return nil
}

func registerSpotMarket(sv StateView, baseToken, quoteToken string) (protocol.MarketIdentity, error) {
	identity, err := protocol.NewMarketIdentity(baseToken, quoteToken)
	if err != nil {
		return protocol.MarketIdentity{}, err
	}
	if err := validateRegisteredMarketAssets(sv, baseToken, quoteToken); err != nil {
		return protocol.MarketIdentity{}, err
	}
	encoded, err := protocol.EncodeMarketIdentity(baseToken, quoteToken)
	if err != nil {
		return protocol.MarketIdentity{}, err
	}
	key := keys.KeySpotMarketConfig(identity.Pair)
	if existing, exists, getErr := sv.Get(key); getErr != nil {
		return protocol.MarketIdentity{}, fmt.Errorf("read spot market config for %s: %w", identity.Pair, getErr)
	} else if exists {
		registered, decodeErr := protocol.DecodeMarketIdentity(identity.Pair, existing)
		if decodeErr != nil {
			return protocol.MarketIdentity{}, fmt.Errorf("decode spot market config for %s: %w", identity.Pair, decodeErr)
		}
		if registered.BaseToken != baseToken || registered.QuoteToken != quoteToken {
			return protocol.MarketIdentity{}, fmt.Errorf("spot market identity conflict for %s", identity.Pair)
		}
		return registered, nil
	}
	sv.Set(key, encoded)
	return identity, nil
}

func loadSpotMarketIdentity(sv StateView, pair string) (protocol.MarketIdentity, error) {
	data, exists, err := sv.Get(keys.KeySpotMarketConfig(pair))
	if err != nil {
		return protocol.MarketIdentity{}, fmt.Errorf("read spot market config for %s: %w", pair, err)
	}
	if exists {
		identity, decodeErr := protocol.DecodeMarketIdentity(pair, data)
		if decodeErr != nil {
			return protocol.MarketIdentity{}, fmt.Errorf("decode spot market config for %s: %w", pair, decodeErr)
		}
		return identity, nil
	}
	return resolveLegacyMarketIdentity(sv, pair)
}

                                                                          
                                                                               
                                                          
func resolveLegacyMarketIdentity(sv StateView, pair string) (protocol.MarketIdentity, error) {
	rows, err := sv.ScanWithLimit(keys.KeyTokenPrefix(), maxMarketIdentityTokens+1)
	if err != nil {
		return protocol.MarketIdentity{}, fmt.Errorf("scan token registry for market %s: %w", pair, err)
	}
	if len(rows) > maxMarketIdentityTokens {
		return protocol.MarketIdentity{}, fmt.Errorf("token registry exceeds market resolution limit %d", maxMarketIdentityTokens)
	}
	prefix := keys.KeyTokenPrefix()
	tokens := make(map[string]struct{}, len(rows))
	for key := range rows {
		assetID := strings.TrimPrefix(key, prefix)
		if assetID != "" && key == prefix+assetID {
			tokens[assetID] = struct{}{}
		}
	}
	matches := make([]protocol.MarketIdentity, 0, 1)
	for baseToken := range tokens {
		if !strings.HasPrefix(pair, baseToken+"_") {
			continue
		}
		quoteToken := strings.TrimPrefix(pair, baseToken+"_")
		if _, exists := tokens[quoteToken]; !exists {
			continue
		}
		identity, identityErr := protocol.NewMarketIdentity(baseToken, quoteToken)
		if identityErr == nil && identity.Pair == pair {
			matches = append(matches, identity)
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].BaseToken == matches[j].BaseToken {
			return matches[i].QuoteToken < matches[j].QuoteToken
		}
		return matches[i].BaseToken < matches[j].BaseToken
	})
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return protocol.MarketIdentity{}, fmt.Errorf("ambiguous legacy market pair %s", pair)
	}
	                                                                   
	                                                                        
	                                                                           
	                             
	if split := strings.LastIndex(pair, "_"); split > 0 && split < len(pair)-1 {
		baseToken, quoteToken := pair[:split], pair[split+1:]
		if !strings.HasSuffix(baseToken, "_br") {
			return protocol.NewMarketIdentity(baseToken, quoteToken)
		}
	}
	return protocol.MarketIdentity{}, fmt.Errorf("spot market identity not registered for %s", pair)
}

func loadFutureMarketConfig(sv StateView, pair string) (*future.MarketConfig, error) {
	data, exists, err := sv.Get(keys.KeyFutureMarketConfig(pair))
	if err != nil {
		return nil, fmt.Errorf("read future market config for %s: %w", pair, err)
	}
	if !exists || len(data) == 0 {
		identity, resolveErr := loadSpotMarketIdentity(sv, pair)
		if resolveErr != nil {
			return nil, fmt.Errorf("future market not registered: %s: %w", pair, resolveErr)
		}
		return &future.MarketConfig{
			Pair:                     pair,
			BaseToken:                identity.BaseToken,
			QuoteToken:               identity.QuoteToken,
			MaxLeverage:              future.DefaultMaxLeverage,
			MaintenanceMarginRateBps: future.DefaultMaintenanceMarginRateBps,
		}, nil
	}
	identity, decodeErr := protocol.DecodeMarketIdentity(pair, data)
	if decodeErr != nil {
		                                                                          
		                                                                     
		if string(data) != pair {
			return nil, fmt.Errorf("decode future market config for %s: %w", pair, decodeErr)
		}
		identity, decodeErr = loadSpotMarketIdentity(sv, pair)
		if decodeErr != nil {
			return nil, decodeErr
		}
	}
	return &future.MarketConfig{
		Pair:                     pair,
		BaseToken:                identity.BaseToken,
		QuoteToken:               identity.QuoteToken,
		MaxLeverage:              future.DefaultMaxLeverage,
		MaintenanceMarginRateBps: future.DefaultMaintenanceMarginRateBps,
	}, nil
}

func futureMarketQuoteToken(sv StateView, pair string) (string, error) {
	cfg, err := loadFutureMarketConfig(sv, pair)
	if err != nil {
		return "", err
	}
	if cfg == nil || cfg.QuoteToken == "" {
		return "", fmt.Errorf("future market %s has no quote token", pair)
	}
	return cfg.QuoteToken, nil
}
