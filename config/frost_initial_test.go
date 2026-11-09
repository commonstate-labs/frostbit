package config

import (
	"bytes"
	"encoding/hex"
	"math/big"
	"strings"
	"testing"

	"dex/frost/core/curve"
	"github.com/btcsuite/btcd/btcec/v2"
)

func testInitialFrostConfig(name string) (ChainFrostConfig, []byte) {
	var pubkey []byte
	cfg := ChainFrostConfig{VaultsPerChain: 1}
	if name == "btc" {
		cfg.SignAlgo = "SCHNORR_SECP256K1_BIP340"
		grp := curve.NewSecp256k1Group()
		pubkey = grp.SerializePoint(grp.ScalarBaseMult(big.NewInt(1)))
	} else {
		cfg.SignAlgo = "SCHNORR_ALT_BN128"
		cfg.VaultRefs = []string{"0x1111111111111111111111111111111111111111"}
		if name == "trx" {
			cfg.VaultRefs = []string{"TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"}
		}
		grp := curve.NewBN256Group()
		pubkey = grp.SerializePoint(grp.ScalarBaseMult(big.NewInt(1)))
	}
	cfg.InitialGroupPubkeys = []string{hex.EncodeToString(pubkey)}
	return cfg, pubkey
}

func TestDecodeInitialFrostGroupPubkeysAllSixChains(t *testing.T) {
	for _, name := range []string{"btc", "eth", "bnb", "base", "arb", "trx"} {
		t.Run(name, func(t *testing.T) {
			cfg, want := testInitialFrostConfig(name)
			cfg.InitialGroupPubkeys[0] = "0X" + cfg.InitialGroupPubkeys[0]
			got, err := DecodeInitialFrostGroupPubkeys(name, cfg)
			if err != nil || len(got) != 1 || !bytes.Equal(got[0], want) {
				t.Fatalf("decode: got=%x err=%v", got, err)
			}
		})
	}
}

func TestDecodeInitialFrostGroupPubkeysPreservesDKGPoint(t *testing.T) {
	btc, pubkey := testInitialFrostConfig("btc")
	pubkey[0] = 3                                                            
	btc.InitialGroupPubkeys[0] = hex.EncodeToString(pubkey)
	got, err := DecodeInitialFrostGroupPubkeys("btc", btc)
	if err != nil || !bytes.Equal(got[0], pubkey) {
		t.Fatalf("BTC DKG parity was changed: %x, %v", got, err)
	}
	parsed, err := btcec.ParsePubKey(got[0])
	if err != nil || parsed.SerializeCompressed()[0] != 3 {
		t.Fatalf("odd-Y key was not preserved: %v", err)
	}
	eth, pubkey := testInitialFrostConfig("eth")
	grp := curve.NewBN256Group()
	new(big.Int).Sub(grp.Modulus(), big.NewInt(2)).FillBytes(pubkey[32:])
	eth.InitialGroupPubkeys[0] = hex.EncodeToString(pubkey)
	got, err = DecodeInitialFrostGroupPubkeys("eth", eth)
	if err != nil || !bytes.Equal(got[0], pubkey) {
		t.Fatalf("BN128 DKG high-Y point was changed: %x, %v", got, err)
	}
}

func TestDecodeInitialFrostGroupPubkeysRejectsPartialConfiguration(t *testing.T) {
	for _, name := range []string{"btc", "eth", "bnb", "base", "arb", "trx"} {
		t.Run(name, func(t *testing.T) {
			cfg, _ := testInitialFrostConfig(name)
			cfg.InitialGroupPubkeys = nil
			if name == "btc" {
				cfg.VaultRefs = []string{"bc1pnot-a-contract"}
			}
			if _, err := DecodeInitialFrostGroupPubkeys(name, cfg); err == nil {
				t.Fatal("refs-only configured genesis fell back to online DKG")
			}
			cfg, _ = testInitialFrostConfig(name)
			if name != "btc" {
				cfg.VaultRefs = nil
				if _, err := DecodeInitialFrostGroupPubkeys(name, cfg); err == nil {
					t.Fatal("keys-only account genesis accepted")
				}
			}
			cfg.VaultRefs, cfg.InitialGroupPubkeys = nil, nil
			got, err := DecodeInitialFrostGroupPubkeys(name, cfg)
			if err != nil || got != nil {
				t.Fatalf("unconfigured online DKG changed: %x %v", got, err)
			}
		})
	}
}

func TestDecodeInitialFrostGroupPubkeysRejectsInvalidPublicData(t *testing.T) {
	tests := []struct {
		name, chain string
		mutate      func(*ChainFrostConfig)
	}{
		{"no vaults", "eth", func(c *ChainFrostConfig) { c.VaultsPerChain = 0 }},
		{"wrong key count", "btc", func(c *ChainFrostConfig) { c.VaultsPerChain = 2 }},
		{"wrong ref count", "eth", func(c *ChainFrostConfig) { c.VaultRefs = append(c.VaultRefs, c.VaultRefs[0]) }},
		{"bitcoin ref", "btc", func(c *ChainFrostConfig) { c.VaultRefs = []string{"0x1111111111111111111111111111111111111111"} }},
		{"bitcoin algorithm", "btc", func(c *ChainFrostConfig) { c.SignAlgo = "SCHNORR_ALT_BN128" }},
		{"tron algorithm", "trx", func(c *ChainFrostConfig) { c.SignAlgo = "ECDSA_SECP256K1" }},
		{"account algorithm", "eth", func(c *ChainFrostConfig) { c.SignAlgo = "SCHNORR_SECP256K1_BIP340" }},
		{"zero address", "eth", func(c *ChainFrostConfig) { c.VaultRefs[0] = "0x" + strings.Repeat("0", 40) }},
		{"malformed address", "eth", func(c *ChainFrostConfig) { c.VaultRefs[0] = "0x123" }},
		{"tron checksum", "trx", func(c *ChainFrostConfig) { c.VaultRefs[0] = "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj7t" }},
		{"duplicate addresses", "eth", func(c *ChainFrostConfig) {
			c.VaultsPerChain = 2
			c.VaultRefs = append(c.VaultRefs, c.VaultRefs[0])
			c.InitialGroupPubkeys = append(c.InitialGroupPubkeys, c.InitialGroupPubkeys[0])
		}},
		{"invalid hex", "eth", func(c *ChainFrostConfig) { c.InitialGroupPubkeys[0] = "xyz" }},
		{"empty key", "eth", func(c *ChainFrostConfig) { c.InitialGroupPubkeys[0] = "0x" }},
		{"BN128 short", "eth", func(c *ChainFrostConfig) { c.InitialGroupPubkeys[0] = "aabb" }},
		{"BN128 infinity", "eth", func(c *ChainFrostConfig) { c.InitialGroupPubkeys[0] = strings.Repeat("00", 64) }},
		{"BN128 off curve", "eth", func(c *ChainFrostConfig) { c.InitialGroupPubkeys[0] = strings.Repeat("00", 63) + "01" }},
		{"BN128 outside field", "eth", func(c *ChainFrostConfig) { c.InitialGroupPubkeys[0] = strings.Repeat("ff", 64) }},
		{"BTC x only", "btc", func(c *ChainFrostConfig) { c.InitialGroupPubkeys[0] = c.InitialGroupPubkeys[0][2:] }},
		{"BTC bad prefix", "btc", func(c *ChainFrostConfig) { c.InitialGroupPubkeys[0] = "04" + c.InitialGroupPubkeys[0][2:] }},
		{"BTC invalid x", "btc", func(c *ChainFrostConfig) { c.InitialGroupPubkeys[0] = "02" + strings.Repeat("ff", 32) }},
		{"unsupported chain", "sol", func(c *ChainFrostConfig) {}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, _ := testInitialFrostConfig(tt.chain)
			tt.mutate(&cfg)
			if _, err := DecodeInitialFrostGroupPubkeys(tt.chain, cfg); err == nil {
				t.Fatal("invalid configured genesis accepted")
			}
		})
	}
}
