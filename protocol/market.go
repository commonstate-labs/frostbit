package protocol

import (
	"encoding/binary"
	"fmt"
)

const (
	marketIdentityEncodingVersion byte = 1
	maxMarketAssetIDLength             = 4096
)

                                                                         
                                                                               
                                                
type MarketIdentity struct {
	Pair       string
	BaseToken  string
	QuoteToken string
}

func NewMarketIdentity(baseToken, quoteToken string) (MarketIdentity, error) {
	if baseToken == "" || quoteToken == "" {
		return MarketIdentity{}, fmt.Errorf("market base_token and quote_token are required")
	}
	if baseToken == quoteToken {
		return MarketIdentity{}, fmt.Errorf("market base_token and quote_token must differ")
	}
	if len(baseToken) > maxMarketAssetIDLength || len(quoteToken) > maxMarketAssetIDLength {
		return MarketIdentity{}, fmt.Errorf("market asset ID exceeds %d bytes", maxMarketAssetIDLength)
	}
	return MarketIdentity{
		Pair:       baseToken + "_" + quoteToken,
		BaseToken:  baseToken,
		QuoteToken: quoteToken,
	}, nil
}

                                                                       
                                                                         
func EncodeMarketIdentity(baseToken, quoteToken string) ([]byte, error) {
	identity, err := NewMarketIdentity(baseToken, quoteToken)
	if err != nil {
		return nil, err
	}
	encoded := make([]byte, 1+4+len(identity.BaseToken)+4+len(identity.QuoteToken))
	encoded[0] = marketIdentityEncodingVersion
	offset := 1
	binary.BigEndian.PutUint32(encoded[offset:offset+4], uint32(len(identity.BaseToken)))
	offset += 4
	copy(encoded[offset:], identity.BaseToken)
	offset += len(identity.BaseToken)
	binary.BigEndian.PutUint32(encoded[offset:offset+4], uint32(len(identity.QuoteToken)))
	offset += 4
	copy(encoded[offset:], identity.QuoteToken)
	return encoded, nil
}

                                                                              
                                                                             
func DecodeMarketIdentity(pair string, encoded []byte) (MarketIdentity, error) {
	if len(encoded) < 9 || encoded[0] != marketIdentityEncodingVersion {
		return MarketIdentity{}, fmt.Errorf("invalid market identity encoding")
	}
	offset := 1
	baseLength := int(binary.BigEndian.Uint32(encoded[offset : offset+4]))
	offset += 4
	if baseLength <= 0 || baseLength > maxMarketAssetIDLength || offset+baseLength+4 > len(encoded) {
		return MarketIdentity{}, fmt.Errorf("invalid market base_token length")
	}
	baseToken := string(encoded[offset : offset+baseLength])
	offset += baseLength
	quoteLength := int(binary.BigEndian.Uint32(encoded[offset : offset+4]))
	offset += 4
	if quoteLength <= 0 || quoteLength > maxMarketAssetIDLength || offset+quoteLength != len(encoded) {
		return MarketIdentity{}, fmt.Errorf("invalid market quote_token length")
	}
	identity, err := NewMarketIdentity(baseToken, string(encoded[offset:]))
	if err != nil {
		return MarketIdentity{}, err
	}
	if identity.Pair != pair {
		return MarketIdentity{}, fmt.Errorf("market identity pair mismatch: have %s want %s", identity.Pair, pair)
	}
	return identity, nil
}
