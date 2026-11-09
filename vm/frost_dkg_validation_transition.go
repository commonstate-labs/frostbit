package vm

import (
	"dex/pb"
	"errors"

	"google.golang.org/protobuf/proto"
)

type dkgValidationAckTransitionInput struct {
	transition        *pb.VaultTransitionState
	ackCount          int
	validationMsgHash []byte
	derivedGroupKey   []byte
}

type dkgValidationAckTransitionResult struct {
	transition   *pb.VaultTransitionState
	requiredAcks int
	keyReady     bool
}

                                                                              
                                                                              
                                               
  
                 
                                                                   
                                                                           
                                                   
  
                  
                                                       
                                                                      
                                              
func advanceDkgValidationAck(input dkgValidationAckTransitionInput) (dkgValidationAckTransitionResult, error) {
	if input.transition == nil {
		return dkgValidationAckTransitionResult{}, errors.New("missing dkg transition")
	}
	requiredAcks := len(input.transition.NewCommitteeMembers)
	if input.transition.DkgThresholdT == 0 || int(input.transition.DkgThresholdT) > requiredAcks {
		return dkgValidationAckTransitionResult{}, errors.New("invalid dkg threshold")
	}

	next := proto.Clone(input.transition).(*pb.VaultTransitionState)
	next.ValidationStatus = "SIGNING"
	next.ValidationMsgHash = append([]byte(nil), input.validationMsgHash...)
	result := dkgValidationAckTransitionResult{
		transition:   next,
		requiredAcks: requiredAcks,
		keyReady:     input.ackCount+1 >= requiredAcks,
	}
	if !result.keyReady {
		return result, nil
	}

	next.DkgStatus = DKGStatusKeyReady
	next.ValidationStatus = "PASSED"
	next.NewGroupPubkey = append([]byte(nil), input.derivedGroupKey...)
	next.Lifecycle = frostTransitionLifecycleForOldKey(next.OldGroupPubkey)
	return result, nil
}

type dkgVaultActivationTransitionInput struct {
	chain      string
	vaultID    uint32
	epochID    uint64
	height     uint64
	transition *pb.VaultTransitionState
	vault      *pb.FrostVaultState
}

type dkgVaultActivationTransitionResult struct {
	vault              *pb.FrostVaultState
	writeVault         bool
	activationDeferred bool
}

                                                                             
                                                                             
                                                   
func applyDkgVaultActivation(input dkgVaultActivationTransitionInput) (dkgVaultActivationTransitionResult, error) {
	if input.transition == nil || input.transition.DkgStatus != DKGStatusKeyReady {
		return dkgVaultActivationTransitionResult{}, errors.New("dkg transition is not key ready")
	}

	var nextVault *pb.FrostVaultState
	if input.vault == nil {
		nextVault = &pb.FrostVaultState{VaultId: input.vaultID, Chain: input.chain}
	} else {
		nextVault = proto.Clone(input.vault).(*pb.FrostVaultState)
	}

	if len(nextVault.GroupPubkey) > 0 {
		if len(input.transition.OldGroupPubkey) == 0 ||
			!frostVaultGroupPubkeysEqual(input.chain, nextVault.GroupPubkey, input.transition.OldGroupPubkey) {
			return dkgVaultActivationTransitionResult{}, errors.New("existing vault key conflicts with DKG transition")
		}
		return dkgVaultActivationTransitionResult{
			vault:              nextVault,
			writeVault:         false,
			activationDeferred: true,
		}, nil
	}

	nextVault.KeyEpoch = input.epochID
	nextVault.GroupPubkey = append([]byte(nil), input.transition.NewGroupPubkey...)
	nextVault.SignAlgo = input.transition.SignAlgo
	nextVault.Status = VaultStatusKeyReady
	if len(input.transition.OldGroupPubkey) == 0 {
		nextVault.Status = VaultStatusActive
		if err := activateVaultCommitteeTerm(nextVault, input.height); err != nil {
			return dkgVaultActivationTransitionResult{}, err
		}
	}
	if len(input.transition.NewCommitteeMembers) > 0 {
		nextVault.CommitteeMembers = append([]string(nil), input.transition.NewCommitteeMembers...)
	}
	nextVault.CommitteeThreshold = input.transition.DkgThresholdT
	nextVault.PendingCommitteeDigest = nil
	nextVault.PendingCommitteeSinceHeight = 0
	return dkgVaultActivationTransitionResult{vault: nextVault, writeVault: true}, nil
}
