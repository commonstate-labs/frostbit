package vm

import (
	"testing"
)

func TestUnifiedVaultRechargeInitRejectsConflictingProtocol(t *testing.T) {
	sv := newStateDBNotFoundStateView(map[string][]byte{})
	cfg := testVaultCommitteePolicyConfig("eth", 4)
	cfg.AuthorityProtocol = 3
	cfg.DepositProtocol = 2
	cfg.DepositDexChainId = 1
	cfg.VaultRefs = []string{"0x1111111111111111111111111111111111111111"}
	if err := InitVaultConfig(sv, "eth", cfg); err != nil {
		t.Fatal(err)
	}
	if err := InitVaultConfig(sv, "eth", cfg); err != nil {
		t.Fatal(err)
	}
	cfg.DepositProtocol, cfg.DepositDexChainId = 0, 0
	if err := InitVaultConfig(sv, "eth", cfg); err == nil {
		t.Fatal("existing unified Vault silently disabled")
	}
}
