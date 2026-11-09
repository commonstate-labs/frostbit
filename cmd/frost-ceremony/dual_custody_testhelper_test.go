package main

import (
	"encoding/hex"
	"math/big"

	"dex/config"
	"dex/frost/chain/evm"
	"dex/frost/core/curve"
)

func testCeremonyGuardian() config.GuardianConfig {
	btc := curve.NewSecp256k1Group()
	account := curve.NewBN256Group()
	btcKey := btc.SerializePoint(btc.ScalarBaseMult(big.NewInt(31)))
	accountKey, _ := evm.CanonicalBN128PublicKey(account.SerializePoint(account.ScalarBaseMult(big.NewInt(31))))
	return config.GuardianConfig{BTCPublicKey: hex.EncodeToString(btcKey[1:]), AccountPublicKey: hex.EncodeToString(accountKey)}
}

func testCeremonyFrostConfig() config.FrostConfig {
	cfg := config.DefaultFrostConfig()
	cfg.Guardian = testCeremonyGuardian()
	return cfg
}

func testCeremonyGuardianWords() (string, string) {
	key, _ := hex.DecodeString(testCeremonyGuardian().AccountPublicKey)
	return new(big.Int).SetBytes(key[:32]).String(), new(big.Int).SetBytes(key[32:]).String()
}

func testCeremonyGuardianABI() string { return "0x" + testCeremonyGuardian().AccountPublicKey }
