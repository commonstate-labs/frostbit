package vm

import (
	"strings"
	"testing"

	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

func configuredVaultStateFixture(t *testing.T, miners int) (*MockStateView, *pb.FrostVaultConfig) {
	t.Helper()
	sv := NewMockStateView()
	cfg := testVaultCommitteePolicyConfig("btc", 4)
	cfg.SignAlgo = pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340
	cfgData, err := proto.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyFrostVaultConfig("btc", 0), cfgData)
	addresses := canonicalGenesisAddressesForTest(t, miners)
	topK := testGenesisFrostSnapshot(addresses)
	topKData, err := proto.Marshal(topK)
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyFrostActiveMiners(), topKData)
	return sv, cfg
}

func TestInitConfiguredActiveVaultStatesRejectsIncompleteCommittee(t *testing.T) {
	sv, _ := configuredVaultStateFixture(t, 3)
	err := InitConfiguredActiveVaultStates(sv, "btc", 1, 1, [][]byte{{1}})
	if err == nil || !strings.Contains(err.Error(), "insufficient VaultEligible miners") {
		t.Fatalf("expected incomplete committee failure, got %v", err)
	}
	if _, exists, _ := sv.Get(keys.KeyFrostVaultState("btc", 0)); exists {
		t.Fatal("failed configured genesis must not write a vault state")
	}
}

func TestInitConfiguredActiveVaultStatesIsIdempotentButRejectsConflict(t *testing.T) {
	sv, _ := configuredVaultStateFixture(t, 4)
	group := [][]byte{{1, 2, 3}}
	if err := InitConfiguredActiveVaultStates(sv, "btc", 1, 1, group); err != nil {
		t.Fatalf("first configured init failed: %v", err)
	}
	if err := InitConfiguredActiveVaultStates(sv, "btc", 1, 1, group); err != nil {
		t.Fatalf("idempotent configured init failed: %v", err)
	}

	data, exists, err := sv.Get(keys.KeyFrostVaultState("btc", 0))
	if err != nil || !exists {
		t.Fatalf("read configured vault state: exists=%v err=%v", exists, err)
	}
	var state pb.FrostVaultState
	if err := proto.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	state.GroupPubkey = []byte{9, 9, 9}
	conflict, err := proto.Marshal(&state)
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyFrostVaultState("btc", 0), conflict)
	if err := InitConfiguredActiveVaultStates(sv, "btc", 1, 1, group); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("expected conflicting state failure, got %v", err)
	}
}

func TestInitVaultConfigRejectsExistingConflict(t *testing.T) {
	sv := NewMockStateView()
	first := testVaultCommitteePolicyConfig("btc", 4)
	if err := InitVaultConfig(sv, "btc", first); err != nil {
		t.Fatalf("first vault config init failed: %v", err)
	}
	if err := InitVaultConfig(sv, "btc", proto.Clone(first).(*pb.FrostVaultConfig)); err != nil {
		t.Fatalf("idempotent vault config init failed: %v", err)
	}
	conflict := proto.Clone(first).(*pb.FrostVaultConfig)
	conflict.DkgCommitWindowBlocks++
	if err := InitVaultConfig(sv, "btc", conflict); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("expected conflicting vault config failure, got %v", err)
	}
}
