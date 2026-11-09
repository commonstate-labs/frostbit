package main

import (
	"encoding/hex"
	"math/big"

	"dex/config"
	"dex/frost/chain/evm"
	"dex/frost/core/curve"
)

func testDualCustodyFrostConfig() config.FrostConfig {
	cfg := config.DefaultFrostConfig()
	installTestGuardian(&cfg)
	return cfg
}

func installTestGuardian(cfg *config.FrostConfig) {
	btc := curve.NewSecp256k1Group()
	account := curve.NewBN256Group()
	btcKey := btc.SerializePoint(btc.ScalarBaseMult(big.NewInt(31)))
	accountKey, _ := evm.CanonicalBN128PublicKey(account.SerializePoint(account.ScalarBaseMult(big.NewInt(31))))
	cfg.Guardian = config.GuardianConfig{BTCPublicKey: hex.EncodeToString(btcKey[1:]), AccountPublicKey: hex.EncodeToString(accountKey)}
	for name, chain := range cfg.Chains {
		if name != "btc" {
			chain.AuthorityProtocol, chain.DepositProtocol, chain.DepositDexChainID = 3, 2, 771
			cfg.Chains[name] = chain
		}
	}
}
