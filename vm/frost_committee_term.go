package vm

import (
	"fmt"
	"math"

	"dex/pb"
	"dex/protocol"
)

                                                                               
                                                                              
func vaultCommitteeTermDue(vault *pb.FrostVaultState, height uint64) bool {
	if vault == nil || len(vault.GroupPubkey) == 0 {
		return true                                                      
	}
	return vault.NextCommitteeReviewHeight != 0 &&
		vault.NextCommitteeReviewHeight >= vault.ActiveSinceHeight &&
		vault.NextCommitteeReviewHeight-vault.ActiveSinceHeight >= protocol.VaultCommitteeTermBlocks &&
		height >= vault.NextCommitteeReviewHeight
}

func nextVaultCommitteeReviewHeight(height uint64) (uint64, error) {
	if height > math.MaxUint64-protocol.VaultCommitteeTermBlocks {
		return 0, fmt.Errorf("vault committee term height overflow")
	}
	return height + protocol.VaultCommitteeTermBlocks, nil
}

                                                                               
                                                                          
func renewVaultCommitteeTerm(vault *pb.FrostVaultState, height uint64) error {
	next, err := nextVaultCommitteeReviewHeight(height)
	if err != nil {
		return err
	}
	vault.NextCommitteeReviewHeight = next
	clearPendingFrostCommittee(vault)
	return nil
}

func activateVaultCommitteeTerm(vault *pb.FrostVaultState, height uint64) error {
	if err := renewVaultCommitteeTerm(vault, height); err != nil {
		return err
	}
	vault.ActiveSinceHeight = height
	return nil
}

func sameVaultCommitteeMembers(current, target []string) bool {
	if len(current) != len(target) {
		return false
	}
	members := make(map[string]struct{}, len(current))
	for _, member := range current {
		if member == "" {
			return false
		}
		members[member] = struct{}{}
	}
	if len(members) != len(current) {
		return false
	}
	for _, member := range target {
		if _, ok := members[member]; !ok {
			return false
		}
		delete(members, member)
	}
	return len(members) == 0
}
