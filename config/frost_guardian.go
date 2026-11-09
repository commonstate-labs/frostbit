package config

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strings"

	"dex/frost/chain"
	"dex/frost/chain/evm"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
)

const FrostDualCustodyProtocol uint32 = 3

                                                                           
                                                                                 
func (c FrostConfig) GuardianPublicKey(name string) ([]byte, error) {
	name = chain.NormalizeChain(name)
	if !chain.IsValidChain(name) {
		return nil, fmt.Errorf("unsupported guardian chain %q", name)
	}
	encoded := c.Guardian.AccountPublicKey
	if name == chain.ChainBTC {
		encoded = c.Guardian.BTCPublicKey
	}
	key, err := hex.DecodeString(strings.TrimPrefix(strings.TrimSpace(encoded), "0x"))
	if err != nil {
		return nil, fmt.Errorf("guardian public key for %s: %w", name, err)
	}
	if name == chain.ChainBTC {
		if len(key) != 32 {
			return nil, fmt.Errorf("guardian.btcPublicKey must be a 32-byte BIP340 public key")
		}
		if _, err := schnorr.ParsePubKey(key); err != nil {
			return nil, fmt.Errorf("invalid guardian.btcPublicKey: %w", err)
		}
	} else {
		canonical, err := evm.CanonicalBN128PublicKey(key)
		if err != nil || !bytes.Equal(key, canonical) {
			return nil, fmt.Errorf("guardian.accountPublicKey must be a canonical 64-byte BN128 public key")
		}
	}
	return key, nil
}

func ValidateFrostGuardianConfig(c FrostConfig) error {
	if !c.Enabled {
		return nil
	}
	for _, name := range chain.SupportedChains {
		if _, enabled := c.Chains[name]; !enabled {
			continue
		}
		if _, err := c.GuardianPublicKey(name); err != nil {
			return err
		}
	}
	return nil
}
