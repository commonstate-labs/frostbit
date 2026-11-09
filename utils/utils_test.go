package utils_test

import (
	"bytes"
	"dex/utils"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
)

func TestGeneratePairKeyPreservesBaseQuoteOrder(t *testing.T) {
	got := utils.GeneratePairKey("FB", "ETH.USDT")
	if got != "FB_ETH.USDT" {
		t.Fatalf("GeneratePairKey() = %q, want %q", got, "FB_ETH.USDT")
	}
}

                                                               
func TestParseSecp256k1PrivateKey(t *testing.T) {
	t.Run("WIF compressed", func(t *testing.T) {
		                                                          
		                                          
		wifStr := "L4bgJzsnrN8ygWdG3rCFWe1iw46Jpudbzh982po71EB61DXXkzNM"
		priv, err := utils.ParseSecp256k1PrivateKey(wifStr)
		if err != nil {
			t.Fatalf("Failed to parse valid WIF: %v", err)
		}
		if priv == nil {
			t.Fatalf("Expected non-nil private key")
		}
	})

	t.Run("Hex 32 bytes", func(t *testing.T) {
		                                                                         
		hexStr := "af981abb208cf43ddc03afb57cdd92613677528794c94185236df76d77ad860f"
		priv, err := utils.ParseSecp256k1PrivateKey(hexStr)
		if err != nil {
			t.Fatalf("Failed to parse valid hex: %v", err)
		}
		                                      
		if len(priv.Serialize()) != 32 {
			t.Errorf("Private key length mismatch, want=32 got=%d", len(priv.Serialize()))
		}
	})

	t.Run("0x-prefixed hex 32 bytes", func(t *testing.T) {
		hexStr := "af981abb208cf43ddc03afb57cdd92613677528794c94185236df76d77ad860f"
		plain, err := utils.ParseSecp256k1PrivateKey(hexStr)
		if err != nil {
			t.Fatalf("parse plain hex: %v", err)
		}
		prefixed, err := utils.ParseSecp256k1PrivateKey("0x" + hexStr)
		if err != nil {
			t.Fatalf("parse 0x-prefixed hex: %v", err)
		}
		if !bytes.Equal(plain.Serialize(), prefixed.Serialize()) {
			t.Fatal("0x-prefixed key parsed to a different private key")
		}
	})

	t.Run("Invalid input", func(t *testing.T) {
		                                          
		invalid := "thisIsNotWIFNorHex"
		priv, err := utils.ParseSecp256k1PrivateKey(invalid)
		if err == nil {
			t.Fatalf("Expected error for invalid key, got nil")
		}
		if priv != nil {
			t.Fatalf("Expected nil privKey on error, got non-nil")
		}
	})
}

                                                         
func TestDeriveEthereumAddress(t *testing.T) {
	                                                         
	                     
	hexPriv := "af981abb208cf43ddc03afb57cdd92613677528794c94185236df76d77ad860f"

	raw, _ := hex.DecodeString(hexPriv)
	privKey := secp256k1.PrivKeyFromBytes(raw)

	ethAddr := utils.DeriveEthereumAddress(privKey)
	t.Logf("Derived Ethereum address: %s", ethAddr)

	                                                                          
	if len(ethAddr) != 42 || ethAddr[:2] != "0x" {
		t.Errorf("Ethereum address format error, got=%s", ethAddr)
	}
}

                                                         
func TestDeriveDexAddress(t *testing.T) {
	                                 
	privKey, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("Failed to generate private key: %v", err)
	}

	btcAddr, err := utils.DeriveDexAddress(privKey)
	if err != nil {
		t.Fatalf("DeriveDexAddress error: %v", err)
	}

	t.Logf("Derived DEX account address: %s", btcAddr)

	                                       
	if len(btcAddr) < len(utils.DEXAddressPrefix) || btcAddr[:len(utils.DEXAddressPrefix)] != utils.DEXAddressPrefix {
		t.Errorf("Expected %s prefix, got=%s", utils.DEXAddressPrefix, btcAddr)
	}
	if !utils.IsDexAddress(btcAddr) {
		t.Fatalf("derived address was rejected: %s", btcAddr)
	}
	if utils.IsDexAddress(strings.ToUpper(btcAddr)) || utils.IsDexAddress(btcAddr+"x") {
		t.Fatal("non-canonical DEX address was accepted")
	}
}

func TestDeriveDexAddressKnownVector(t *testing.T) {
	privKey, err := utils.ParseSecp256k1PrivateKey("0000000000000000000000000000000000000000000000000000000000000001")
	if err != nil {
		t.Fatalf("ParseSecp256k1PrivateKey failed: %v", err)
	}
	addr, err := utils.DeriveDexAddress(privKey)
	if err != nil {
		t.Fatalf("DeriveDexAddress failed: %v", err)
	}
	if addr != "fbc1qw508d6qejxtdg4y5r3zarvary0c5xw7kng2fs8" {
		t.Fatalf("derived address mismatch: got=%s", addr)
	}
}

func TestParseWIFAndGenerateBtcBech32Address(t *testing.T) {
	                                                                                 
	                                             
	wifStr := "L14j65C9VW5g2PEmisQyoHkhXHoAJCnjVY1LkNrK3PHbDwqK62Es"

	                                                
	privKey, err := utils.ParseSecp256k1PrivateKey(wifStr)
	if err != nil {
		t.Fatalf("ParseSecp256k1PrivateKey(WIF) failed: %v", err)
	}
	if privKey == nil {
		t.Fatalf("Expected non-nil private key, got nil")
	}

	                       
	bc1Addr, err := utils.DeriveBtcTaprootAddress(privKey)
	if err != nil {
		t.Fatalf("DeriveBtcTaprootAddress error: %v", err)
	}

	t.Logf("Derived bc1 address from WIF: %s", bc1Addr)

	                                        
	if len(bc1Addr) < 3 || bc1Addr[:3] != "bc1" {
		t.Errorf("Expected bc1 address, got=%s", bc1Addr)
	}
}

func TestComputeTweakedPubkeyUsesXOnlyInternalKey(t *testing.T) {
	var privKey *secp256k1.PrivateKey
	for i := byte(1); i < 255; i++ {
		raw := make([]byte, 32)
		raw[31] = i
		candidate := secp256k1.PrivKeyFromBytes(raw)
		if candidate.PubKey().SerializeCompressed()[0] == 0x03 {
			privKey = candidate
			break
		}
	}
	if privKey == nil {
		t.Fatal("failed to find odd-Y private key fixture")
	}

	groupPubCompressed := privKey.PubKey().SerializeCompressed()
	groupPubXOnly := append([]byte(nil), groupPubCompressed[1:33]...)
	tweak := utils.ComputeUserTweak(groupPubXOnly, "fbc1q8ra2f338djg0pzgms35tpkdxjx9lr39mkyw84y")

	fromCompressed, err := utils.ComputeTweakedPubkey(groupPubCompressed, tweak)
	if err != nil {
		t.Fatalf("ComputeTweakedPubkey(compressed) failed: %v", err)
	}
	fromXOnly, err := utils.ComputeTweakedPubkey(groupPubXOnly, tweak)
	if err != nil {
		t.Fatalf("ComputeTweakedPubkey(xonly) failed: %v", err)
	}

	if !bytes.Equal(fromCompressed, fromXOnly) {
		t.Fatalf("tweaked pubkey mismatch: compressed=%x xonly=%x", fromCompressed, fromXOnly)
	}
}

func TestNormalizeSecp256k1XOnlyPubKey(t *testing.T) {
	xOnly := bytes.Repeat([]byte{0x11}, 32)

	got32, err := utils.NormalizeSecp256k1XOnlyPubKey(xOnly)
	if err != nil {
		t.Fatalf("NormalizeSecp256k1XOnlyPubKey(32) failed: %v", err)
	}
	if !bytes.Equal(got32, xOnly) {
		t.Fatalf("32-byte normalization mismatch: got=%x want=%x", got32, xOnly)
	}

	compressed := append([]byte{0x03}, xOnly...)
	got33, err := utils.NormalizeSecp256k1XOnlyPubKey(compressed)
	if err != nil {
		t.Fatalf("NormalizeSecp256k1XOnlyPubKey(33) failed: %v", err)
	}
	if !bytes.Equal(got33, xOnly) {
		t.Fatalf("33-byte normalization mismatch: got=%x want=%x", got33, xOnly)
	}
}

func TestTaprootScriptPubKeyRoundTrip(t *testing.T) {
	xOnly := bytes.Repeat([]byte{0x22}, 32)

	scriptPubKey, err := utils.BuildTaprootScriptPubKeyFromXOnly(xOnly)
	if err != nil {
		t.Fatalf("BuildTaprootScriptPubKeyFromXOnly failed: %v", err)
	}

	got, err := utils.ParseTaprootScriptPubKeyXOnly(scriptPubKey)
	if err != nil {
		t.Fatalf("ParseTaprootScriptPubKeyXOnly failed: %v", err)
	}
	if !bytes.Equal(got, xOnly) {
		t.Fatalf("taproot script roundtrip mismatch: got=%x want=%x", got, xOnly)
	}
}
