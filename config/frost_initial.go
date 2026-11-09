package config

import (
	"encoding/hex"
	"fmt"
	"strings"

	"dex/frost/chain"
	"dex/frost/chain/evm"
	"github.com/btcsuite/btcd/btcec/v2"
)

                                                                            
                                                                               
                                                                             
                                                                            
                                                                               
                                                                                
func DecodeInitialFrostGroupPubkeys(chainName string, cfg ChainFrostConfig) ([][]byte, error) {
	if len(cfg.VaultRefs) == 0 && len(cfg.InitialGroupPubkeys) == 0 {
		return nil, nil
	}
	name := chain.NormalizeChain(chainName)
	if !chain.IsValidChain(name) {
		return nil, fmt.Errorf("unsupported configured genesis chain %q", chainName)
	}
	if cfg.VaultsPerChain <= 0 {
		return nil, fmt.Errorf("chain %s configured vault count must be positive", name)
	}
	if len(cfg.InitialGroupPubkeys) != cfg.VaultsPerChain {
		return nil, fmt.Errorf("chain %s configured initialGroupPubkeys count mismatch: have %d want %d", name, len(cfg.InitialGroupPubkeys), cfg.VaultsPerChain)
	}
	if name == chain.ChainBTC {
		if len(cfg.VaultRefs) != 0 {
			return nil, fmt.Errorf("chain btc configured genesis derives addresses from keys; vaultRefs must be empty")
		}
		if cfg.SignAlgo != "SCHNORR_SECP256K1_BIP340" {
			return nil, fmt.Errorf("chain btc configured genesis requires SCHNORR_SECP256K1_BIP340")
		}
	} else {
		if cfg.SignAlgo != "SCHNORR_ALT_BN128" {
			return nil, fmt.Errorf("chain %s configured genesis requires SCHNORR_ALT_BN128", name)
		}
		if len(cfg.VaultRefs) != cfg.VaultsPerChain {
			return nil, fmt.Errorf("chain %s configured vaultRefs count mismatch: have %d want %d", name, len(cfg.VaultRefs), cfg.VaultsPerChain)
		}
		seenRefs := make(map[string]struct{}, len(cfg.VaultRefs))
		for i, ref := range cfg.VaultRefs {
			canonical, err := evm.NormalizeAccountAddress(name, ref, false)
			if err != nil {
				return nil, fmt.Errorf("chain %s vaultRefs[%d] is invalid: %w", name, i, err)
			}
			if _, exists := seenRefs[canonical]; exists {
				return nil, fmt.Errorf("chain %s vaultRefs[%d] duplicates another vault", name, i)
			}
			seenRefs[canonical] = struct{}{}
		}
	}

	pubkeys := make([][]byte, len(cfg.InitialGroupPubkeys))
	for i, encoded := range cfg.InitialGroupPubkeys {
		raw := strings.TrimSpace(encoded)
		raw = strings.TrimPrefix(strings.TrimPrefix(raw, "0x"), "0X")
		pubkey, err := hex.DecodeString(raw)
		if err != nil {
			return nil, fmt.Errorf("chain %s initialGroupPubkeys[%d] decode failed: %w", name, i, err)
		}
		if name == chain.ChainBTC {
			if len(pubkey) != btcec.PubKeyBytesLenCompressed {
				return nil, fmt.Errorf("chain btc initialGroupPubkeys[%d] must be a 33-byte compressed secp256k1 point", i)
			}
			if _, err := btcec.ParsePubKey(pubkey); err != nil {
				return nil, fmt.Errorf("chain btc initialGroupPubkeys[%d] is invalid: %w", i, err)
			}
			                                                                   
			                                                                   
			                                                                
		} else if _, err := evm.CanonicalBN128PublicKey(pubkey); err != nil {
			return nil, fmt.Errorf("chain %s initialGroupPubkeys[%d] is not a valid 64-byte BN128 point: %w", name, i, err)
		}
		                                                                    
		                                                                       
		pubkeys[i] = pubkey
	}
	return pubkeys, nil
}
