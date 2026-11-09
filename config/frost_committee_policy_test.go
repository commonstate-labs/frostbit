package config

import (
	"testing"

	"dex/pb"
	"dex/protocol"
	"google.golang.org/protobuf/proto"
)

func TestVaultCommitteeThresholdsAreExactThreeQuarters(t *testing.T) {
	want := map[int]int{4: 3, 8: 6, 20: 15, 32: 24, 64: 48, 100: 75}
	for k, threshold := range want {
		got, err := ComputeVaultCommitteeThreshold(k)
		if err != nil || got != threshold {
			t.Fatalf("K=%d threshold=(%d,%v), want %d", k, got, err, threshold)
		}
	}
	for _, unsupported := range []int{0, 5, 16, 101} {
		if _, err := ComputeVaultCommitteeThreshold(unsupported); err == nil {
			t.Fatalf("unsupported K=%d was accepted", unsupported)
		}
	}
}

func TestTargetVaultCommitteeSizeUsesOnePercentAndNeverShrinks(t *testing.T) {
	tests := []struct {
		eligible uint32
		current  int
		want     int
	}{
		{0, 0, 4}, {799, 4, 4}, {800, 4, 8}, {1999, 8, 8},
		{2000, 8, 20}, {3200, 20, 32}, {6400, 32, 64},
		{10000, 64, 100}, {799, 20, 20},
	}
	for _, test := range tests {
		got, err := TargetVaultCommitteeSize(test.eligible, 100, 100, test.current)
		if err != nil || got != test.want {
			t.Fatalf("eligible=%d current=%d got=(%d,%v), want %d", test.eligible, test.current, got, err, test.want)
		}
	}
}

func TestTargetVaultCommitteeSizeLocal20Profile(t *testing.T) {
	for _, test := range []struct {
		eligible uint32
		want     int
	}{{5, 4}, {8, 8}, {19, 8}, {20, 20}} {
		got, err := TargetVaultCommitteeSize(test.eligible, 1, 100, 4)
		if err != nil || got != test.want {
			t.Fatalf("eligible=%d got=(%d,%v), want %d", test.eligible, got, err, test.want)
		}
	}
}

func TestVaultCandidateSnapshotLimitCoversAllK100RetryReserves(t *testing.T) {
	threshold, err := ComputeVaultCommitteeThreshold(100)
	if err != nil {
		t.Fatal(err)
	}
	want := 100 + (100-threshold)*3
	if VaultCandidateSnapshotLimit != want {
		t.Fatalf("candidate reserve=%d, want K+(K-t)*3=%d", VaultCandidateSnapshotLimit, want)
	}
}

func TestOnChainVaultCommitteePolicyIsFullyFrozen(t *testing.T) {
	valid := pb.FrostVaultConfig{
		Chain: "eth", VaultCount: 1, CommitteeSize: 4, ThresholdRatio: 0.75,
		VaultMaturityBlocks: 30, CommitteeScaleMinerMultiplier: 1,
		CommitteeTermBlocks:     protocol.VaultCommitteeTermBlocks,
		TransitionConfirmBlocks: 10, CommitteeMaxSize: 100,
		TransitionTriggerRatio: 0.2, DkgCommitWindowBlocks: 5,
		DkgSharingWindowBlocks: 5, DkgDisputeWindowBlocks: 5,
	}
	if err := ValidateOnChainVaultCommitteePolicy(&valid); err != nil {
		t.Fatalf("valid on-chain policy rejected: %v", err)
	}
	tests := map[string]func(*pb.FrostVaultConfig){
		"chain":        func(c *pb.FrostVaultConfig) { c.Chain = "" },
		"vault-count":  func(c *pb.FrostVaultConfig) { c.VaultCount = 2 },
		"initial-k":    func(c *pb.FrostVaultConfig) { c.CommitteeSize = 8 },
		"threshold":    func(c *pb.FrostVaultConfig) { c.ThresholdRatio = 0.8 },
		"maturity":     func(c *pb.FrostVaultConfig) { c.VaultMaturityBlocks = 0 },
		"multiplier":   func(c *pb.FrostVaultConfig) { c.CommitteeScaleMinerMultiplier = 0 },
		"term-missing": func(c *pb.FrostVaultConfig) { c.CommitteeTermBlocks = 0 },
		"term-short":   func(c *pb.FrostVaultConfig) { c.CommitteeTermBlocks-- },
		"term-long":    func(c *pb.FrostVaultConfig) { c.CommitteeTermBlocks++ },
		"confirmation": func(c *pb.FrostVaultConfig) {
			c.TransitionConfirmBlocks = 0
		},
		"max-k":   func(c *pb.FrostVaultConfig) { c.CommitteeMaxSize = 64 },
		"change":  func(c *pb.FrostVaultConfig) { c.TransitionTriggerRatio = 0.1 },
		"windows": func(c *pb.FrostVaultConfig) { c.DkgSharingWindowBlocks = 0 },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := proto.Clone(&valid).(*pb.FrostVaultConfig)
			mutate(candidate)
			if err := ValidateOnChainVaultCommitteePolicy(candidate); err == nil {
				t.Fatal("invalid on-chain policy was accepted")
			}
		})
	}
}

func TestLocalVaultTermCannotOverrideProtocol(t *testing.T) {
	valid := DefaultFrostConfig()
	if valid.Vault.CommitteeTermBlocks != protocol.VaultCommitteeTermBlocks || protocol.VaultCommitteeTermBlocks != 120960 {
		t.Fatal("default term differs from consensus protocol")
	}
	for _, term := range []uint64{0, protocol.VaultCommitteeTermBlocks - 1, protocol.VaultCommitteeTermBlocks + 1} {
		candidate := valid
		candidate.Vault.CommitteeTermBlocks = term
		if err := ValidateVaultCommitteePolicy(candidate); err == nil {
			t.Fatalf("local term override %d accepted", term)
		}
	}
}
