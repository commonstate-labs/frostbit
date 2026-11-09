package vm

import (
	"crypto/sha256"

	"dex/frost/protocol"
	"dex/pb"
)

const (
	DKGStatusNotStarted = "NOT_STARTED"
	DKGStatusCommitting = "COMMITTING"
	DKGStatusSharing    = "SHARING"
	DKGStatusResolving  = "RESOLVING"
	DKGStatusKeyReady   = "KEY_READY"
	DKGStatusFailed     = "FAILED"
)

const (
	ComplaintStatusPending      = "PENDING"
	ComplaintStatusResolved     = "RESOLVED"
	ComplaintStatusDisqualified = "DISQUALIFIED"
)

const RevealDeadlineBlocks = 100

                                                                               
                                                                           
const MinDkgComplaintBond = protocol.MinDkgComplaintBond

                                                                                     
const DkgComplaintBondToken = protocol.DkgComplaintBondToken

const (
	VaultLifecycleActive   = "ACTIVE"
	VaultLifecycleDraining = "DRAINING"
	VaultLifecycleRetired  = "RETIRED"
)

const (
	VaultStatusPending    = "PENDING"
	VaultStatusKeyReady   = "KEY_READY"
	VaultStatusActive     = "ACTIVE"
	VaultStatusDeprecated = "DEPRECATED"
)

func isInCommittee(address string, members []string) bool {
	for _, m := range members {
		if m == address {
			return true
		}
	}
	return false
}

func computeDkgValidationMsgHash(chain string, vaultID uint32, epochID uint64, signAlgo pb.SignAlgo, groupPubkey []byte) []byte {
	h := sha256.New()
	h.Write([]byte("frost_vault_dkg_validation"))
	h.Write([]byte(chain))
	h.Write([]byte{byte(vaultID >> 24), byte(vaultID >> 16), byte(vaultID >> 8), byte(vaultID)})
	h.Write([]byte{byte(epochID >> 56), byte(epochID >> 48), byte(epochID >> 40), byte(epochID >> 32),
		byte(epochID >> 24), byte(epochID >> 16), byte(epochID >> 8), byte(epochID)})
	h.Write([]byte{byte(signAlgo)})
	h.Write(groupPubkey)
	return h.Sum(nil)
}
