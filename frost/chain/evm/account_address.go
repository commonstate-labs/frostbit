package evm

import (
	"encoding/hex"
	"strings"

	"dex/frost/chain"
	"github.com/btcsuite/btcd/btcutil/base58"
)

                                                                           
                                                                               
                                                                             
func NormalizeAccountAddress(chainName, raw string, allowZero bool) (string, error) {
	if chain.NormalizeChain(chainName) != chain.ChainTRX {
		return NormalizeAddress(raw, allowZero)
	}
	s := strings.TrimSpace(raw)
	if decoded, version, err := base58.CheckDecode(s); err == nil && version == 0x41 && len(decoded) == 20 {
		return NormalizeAddress("0x"+hex.EncodeToString(decoded), allowZero)
	}
	withoutPrefix := strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	if len(withoutPrefix) == 42 && strings.HasPrefix(withoutPrefix, "41") {
		if _, err := hex.DecodeString(withoutPrefix); err != nil {
			return "", ErrInvalidAddress
		}
		return NormalizeAddress("0x"+strings.ToLower(withoutPrefix[2:]), allowZero)
	}
	                                                                     
	if len(s) == 40 {
		if _, err := hex.DecodeString(s); err != nil {
			return "", ErrInvalidAddress
		}
		return NormalizeAddress("0x"+strings.ToLower(s), allowZero)
	}
	                                                                          
	return NormalizeAddress(s, allowZero)
}
