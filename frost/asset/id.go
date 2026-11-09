package asset

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"dex/frost/chain"
	"dex/frost/chain/evm"

	"github.com/btcsuite/btcd/btcutil/base58"
)

const (
	                                                                         
	                                                                         
	                                                     
	IDPrefix = "br_"

	nativeReference = "native"
)

                                                                         
                                                        
type Identity struct {
	Chain    string
	Contract string
	ID       string
}

                                                                              
                                                                               
                                                        
func Resolve(chainName, contract string) (Identity, error) {
	chainName = chain.NormalizeChain(chainName)
	if !chain.IsValidChain(chainName) {
		return Identity{}, fmt.Errorf("unsupported native chain: %s", chainName)
	}

	canonicalContract, err := canonicalContract(chainName, contract)
	if err != nil {
		return Identity{}, err
	}
	reference := canonicalContract
	if reference == "" {
		reference = nativeReference
	}
	digest := sha256.Sum256([]byte(chainName + "\x00" + reference))
	return Identity{
		Chain:    chainName,
		Contract: canonicalContract,
		ID:       IDPrefix + hex.EncodeToString(digest[:]),
	}, nil
}

func canonicalContract(chainName, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	switch chainName {
	case chain.ChainBTC:
		if raw != "" {
			return "", fmt.Errorf("BTC native asset cannot have a contract")
		}
		return "", nil
	case chain.ChainETH, chain.ChainBNB, chain.ChainBASE, chain.ChainARB:
		if raw == "" {
			return "", nil
		}
		canonical, err := evm.NormalizeAddress(raw, false)
		if err != nil {
			return "", fmt.Errorf("invalid %s asset contract: %w", chainName, err)
		}
		return canonical, nil
	case chain.ChainTRX:
		if raw == "" {
			return "", nil
		}
		payload, version, err := base58.CheckDecode(raw)
		if err != nil || version != 0x41 || len(payload) != 20 {
			return "", fmt.Errorf("invalid TRX asset contract")
		}
		return raw, nil
	default:
		return "", fmt.Errorf("unsupported native chain: %s", chainName)
	}
}
