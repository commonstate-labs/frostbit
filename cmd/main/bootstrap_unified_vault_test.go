package main

import (
	"bytes"
	"dex/config"
	"dex/pb"
	"google.golang.org/protobuf/proto"
	"testing"
)

func TestBuildVaultConfigsCarriesUnifiedDepositProtocol(t *testing.T) {
	cfg := config.FrostConfig{Guardian: testDualCustodyFrostConfig().Guardian, Chains: map[string]config.ChainFrostConfig{
		"btc": {FeeSatsPerVByte: 25},
		"eth": {AuthorityProtocol: 3, DepositProtocol: 2, DepositDexChainID: 51025, VaultRefs: []string{"0x1111111111111111111111111111111111111111"}},
		"trx": {VaultRefs: []string{"TTRXWithdrawalOnly"}},
	}}
	configs := buildVaultConfigs(cfg, 5)
	guardian, _ := cfg.GuardianPublicKey("eth")
	if !bytes.Equal(configs["eth"].GuardianPubkey, guardian) {
		t.Fatal("fixed guardian was lost")
	}
	if configs["eth"].AuthorityProtocol != 3 || configs["eth"].DepositProtocol != 2 || configs["eth"].DepositDexChainId != 51025 {
		t.Fatalf("unified=%+v", configs["eth"])
	}
	if configs["trx"].DepositProtocol != 0 || configs["trx"].DepositDexChainId != 0 {
		t.Fatalf("TRX=%+v", configs["trx"])
	}
	if configs["btc"].BtcFeeSatsPerVbyte != 25 {
		t.Fatalf("BTC fee rate was not copied into consensus config: %+v", configs["btc"])
	}
	raw, err := proto.Marshal(configs["eth"])
	if err != nil {
		t.Fatal(err)
	}
	var roundtrip pb.FrostVaultConfig
	if err := proto.Unmarshal(raw, &roundtrip); err != nil || roundtrip.DepositProtocol != 2 || roundtrip.AuthorityProtocol != 3 || !bytes.Equal(roundtrip.GuardianPubkey, guardian) {
		t.Fatalf("protobuf field 17 lost: %+v %v", &roundtrip, err)
	}
	btcRaw, err := proto.Marshal(configs["btc"])
	if err != nil {
		t.Fatal(err)
	}
	if err := proto.Unmarshal(btcRaw, &roundtrip); err != nil || roundtrip.BtcFeeSatsPerVbyte != 25 {
		t.Fatalf("protobuf field 18 lost: %+v %v", &roundtrip, err)
	}
}

func TestBuildVaultConfigsUsesOnlineDKGVaultRefsWithoutConfiguredGenesis(t *testing.T) {
	const simulatedBaseVault = "0x000000000000000000000000000000000000BA00"
	cfg := testDualCustodyFrostConfig()
	base := cfg.Chains["base"]
	base.VaultRefs = nil
	base.InitialGroupPubkeys = nil
	base.OnlineDKGVaultRefs = []string{simulatedBaseVault}
	cfg.Chains = map[string]config.ChainFrostConfig{"base": base}

	configs := buildVaultConfigs(cfg, 4)
	if got := configs["base"].VaultRefs; len(got) != 1 || got[0] != simulatedBaseVault {
		t.Fatalf("online-DKG vault refs=%v want=%s", got, simulatedBaseVault)
	}
	configured, err := configuredFrostGenesisMode(cfg)
	if err != nil || configured {
		t.Fatalf("online-DKG refs changed configured-genesis mode: configured=%v err=%v", configured, err)
	}
}
