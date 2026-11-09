package vm

import (
	"dex/frost/chain/evm"
	"dex/frost/core/curve"
	"dex/pb"
	"dex/protocol"
	"math/big"
)

func testGenesisFrostSnapshot(addresses []string) *pb.FrostActiveMiners {
	top := &pb.FrostActiveMiners{
		Addresses:     append([]string(nil), addresses...),
		Indices:       make([]uint64, len(addresses)),
		PublicKeys:    make([][]byte, len(addresses)),
		EligibleCount: uint32(len(addresses)),
	}
	for i := range addresses {
		top.Indices[i] = uint64(i)
		top.PublicKeys[i] = []byte{byte(i + 1)}
	}
	return top
}

func testRankedFrostSnapshot(addresses []string, height uint64) *pb.FrostActiveMiners {
	if height == 0 {
		height = 1
	}
	top := &pb.FrostActiveMiners{
		Height:             height,
		UpdateHeight:       height,
		Addresses:          append([]string(nil), addresses...),
		Indices:            make([]uint64, len(addresses)),
		PublicKeys:         make([][]byte, len(addresses)),
		MaturedStakes:      make([]uint64, len(addresses)),
		ActiveSinceHeights: make([]uint64, len(addresses)),
		EligibleCount:      uint32(len(addresses)),
	}
	for i := range addresses {
		top.Indices[i] = uint64(i)
		top.PublicKeys[i] = []byte{byte(i%255 + 1)}
		top.MaturedStakes[i] = protocol.MinMinerStakeRaw + uint64(len(addresses)-i)
		top.ActiveSinceHeights[i] = 1
	}
	return top
}

func testVaultCommitteePolicyConfig(chain string, committeeSize uint32) *pb.FrostVaultConfig {
	guardian := btcDualTestGuardianPubkey()
	if chain != "btc" {
		guardian, _ = evm.CanonicalBN128PublicKey(curve.NewBN256Group().SerializePoint(curve.NewBN256Group().ScalarBaseMult(big.NewInt(29))))
	}
	return &pb.FrostVaultConfig{
		GuardianPubkey:                guardian,
		Chain:                         chain,
		VaultCount:                    1,
		CommitteeSize:                 committeeSize,
		ThresholdRatio:                0.75,
		VaultMaturityBlocks:           30,
		CommitteeTermBlocks:           protocol.VaultCommitteeTermBlocks,
		CommitteeScaleMinerMultiplier: 1,
		TransitionConfirmBlocks:       10,
		CommitteeMaxSize:              100,
		TransitionTriggerRatio:        0.2,
		DkgCommitWindowBlocks:         5,
		DkgSharingWindowBlocks:        5,
		DkgDisputeWindowBlocks:        5,
	}
}
