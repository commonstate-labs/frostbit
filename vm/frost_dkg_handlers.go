                           
                                                

package vm

import (
	"bytes"
	chainpkg "dex/frost/chain"
	"dex/frost/chain/evm"
	"dex/frost/core/curve"
	"dex/frost/security"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"dex/utils"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"google.golang.org/protobuf/proto"
)

const (
	                                                                          
	                                                                        
	                                                  
	DkgComplaintBondPoolAccount = "__dkg_complaint_bond_pool__"
	DkgSlashPoolAccount         = "__dkg_slash_pool__"

	                                                                       
	                                                                        
	dkgMinRevealResponseBlocks uint64 = 2
)

func moveDkgComplaintBond(sv StateView, from, to string, amount *big.Int) error {
	if from == "" || to == "" || from == to {
		return errors.New("invalid dkg complaint bond transfer accounts")
	}
	if amount == nil || amount.Sign() <= 0 {
		return errors.New("dkg complaint bond transfer amount must be positive")
	}

	fromBalance := GetBalance(sv, from, DkgComplaintBondToken)
	fromAvailable, err := parseBalanceStrict("dkg complaint bond source balance", fromBalance.Balance)
	if err != nil {
		return err
	}
	if fromAvailable.Cmp(amount) < 0 {
		return fmt.Errorf("insufficient dkg complaint bond balance: have %s need %s", fromAvailable, amount)
	}

	toBalance := GetBalance(sv, to, DkgComplaintBondToken)
	toAvailable, err := parseBalanceStrict("dkg complaint bond destination balance", toBalance.Balance)
	if err != nil {
		return err
	}
	nextFrom, err := SafeSub(fromAvailable, amount)
	if err != nil {
		return err
	}
	nextTo, err := SafeAdd(toAvailable, amount)
	if err != nil {
		return err
	}

	fromBalance.Balance = nextFrom.String()
	toBalance.Balance = nextTo.String()
	SetBalance(sv, from, DkgComplaintBondToken, fromBalance)
	SetBalance(sv, to, DkgComplaintBondToken, toBalance)
	return nil
}

func frostVaultGroupPubkeysEqual(chain string, left, right []byte) bool {
	if isBTCChainName(chain) {
		leftXOnly, leftErr := utils.NormalizeSecp256k1XOnlyPubKey(left)
		rightXOnly, rightErr := utils.NormalizeSecp256k1XOnlyPubKey(right)
		return leftErr == nil && rightErr == nil && bytes.Equal(leftXOnly, rightXOnly)
	}
	return bytes.Equal(left, right)
}

                                                     

type FrostVaultDkgCommitTxHandler struct{}

func (h *FrostVaultDkgCommitTxHandler) Kind() string {
	return "frost_vault_dkg_commit"
}

func (h *FrostVaultDkgCommitTxHandler) DryRun(tx *pb.AnyTx, sv StateView) ([]WriteOp, *Receipt, error) {
	commitTx, ok := tx.GetContent().(*pb.AnyTx_FrostVaultDkgCommitTx)
	if !ok {
		return nil, &Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: "not a dkg commit tx"}, errors.New("not a dkg commit tx")
	}

	req := commitTx.FrostVaultDkgCommitTx
	if req == nil || req.Base == nil {
		return nil, &Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: "invalid dkg commit tx"}, errors.New("invalid dkg commit tx")
	}

	txID := req.Base.TxId
	sender := req.Base.FromAddress
	height := req.Base.ExecutedHeight
	chain := req.Chain
	vaultID := req.VaultId
	epochID := req.EpochId
	signAlgo := req.SignAlgo
	sessionID := strings.TrimSpace(req.SessionId)

	logs.Debug("[DKGCommit] sender=%s chain=%s vault=%d epoch=%d algo=%v", sender, chain, vaultID, epochID, signAlgo)
	if sessionID == "" {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "missing session_id"}, errors.New("missing session_id")
	}

	                                                       
	transitionKey := keys.KeyFrostVaultTransition(chain, vaultID, epochID)
	transitionData, transitionExists, _ := sv.Get(transitionKey)
	if !transitionExists || len(transitionData) == 0 {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "transition not found"}, errors.New("transition not found")
	}

	transition, err := unmarshalFrostVaultTransition(transitionData)
	if err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to parse transition"}, err
	}
	if transition.DkgSessionId != sessionID {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "session_id mismatch"}, errors.New("session_id mismatch")
	}

	                                                
	existingKey := keys.KeyFrostVaultDkgCommit(chain, vaultID, epochID, sessionID, sender)
	existingData, exists, _ := sv.Get(existingKey)
	if exists && len(existingData) > 0 {
		logs.Debug("[DKGCommit] already committed, idempotent")
		return nil, &Receipt{TxID: txID, Status: "SUCCEED", WriteCount: 0}, nil
	}

	                                                                             
	if height < transition.TriggerHeight || height > transition.DkgCommitDeadline {
		logs.Warn("[DKGCommit] height out of window: height=%d window=[%d, %d]", height, transition.TriggerHeight, transition.DkgCommitDeadline)
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: fmt.Sprintf("commit window violation (height %d, window [%d, %d])", height, transition.TriggerHeight, transition.DkgCommitDeadline)}, nil
	}

	                       
	if transition.DkgStatus != DKGStatusCommitting && transition.DkgStatus != DKGStatusNotStarted {
		                                                                           
		                                                                                                                                                    
		logs.Warn("[DKGCommit] invalid dkg_status for commit: %s", transition.DkgStatus)
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: fmt.Sprintf("invalid dkg_status=%s", transition.DkgStatus)}, nil
	}

	                        
	ops := []WriteOp{}

	                                                                                                 
	if transition.DkgStatus == DKGStatusNotStarted {
		transition.DkgStatus = DKGStatusCommitting
		transitionData, err := proto.Marshal(transition)
		if err == nil {
			ops = append(ops, WriteOp{
				Key:      transitionKey,
				Value:    transitionData,
				Category: "frost_vault_transition",
			})
		}
	}

	                                
	if transition.SignAlgo != signAlgo {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "sign_algo mismatch"}, errors.New("sign_algo mismatch")
	}

	                                                       
	if !isInCommittee(sender, transition.NewCommitteeMembers) {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "sender not in committee"}, errors.New("sender not in committee")
	}

	                                                                                                                     
	if len(req.CommitmentPoints) == 0 || len(req.AI0) == 0 {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "empty commitment data"}, errors.New("empty commitment data")
	}
	if int(transition.DkgThresholdT) > 0 && len(req.CommitmentPoints) != int(transition.DkgThresholdT) {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: fmt.Sprintf("commitment points length mismatch: got=%d threshold=%d", len(req.CommitmentPoints), transition.DkgThresholdT)}, errors.New("commitment points length mismatch")
	}
	grp, err := groupForSignAlgo(signAlgo)
	if err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "unsupported sign algo"}, err
	}
	for i, pt := range req.CommitmentPoints {
		p := grp.DecompressPoint(pt)
		if p.X == nil || p.Y == nil {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: fmt.Sprintf("invalid commitment point at index %d", i)}, errors.New("invalid commitment point")
		}
	}
	if p := grp.DecompressPoint(req.AI0); p.X == nil || p.Y == nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "invalid AI0 commitment"}, errors.New("invalid AI0 commitment")
	}

	                  
	commitment := &pb.FrostVaultDkgCommitment{
		Chain:            chain,
		VaultId:          vaultID,
		EpochId:          epochID,
		MinerAddress:     sender,
		SignAlgo:         signAlgo,
		CommitmentPoints: req.CommitmentPoints,
		AI0:              req.AI0,
		CommitHeight:     height,
	}

	commitData, err := proto.Marshal(commitment)
	if err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to marshal commitment"}, err
	}

	ops = append(ops, WriteOp{
		Key:      existingKey,
		Value:    commitData,
		Category: "frost_dkg_commit",
	})

	ops = appendTransitionIndexOpsFromWriteSet(ops)
	for _, op := range ops {
		if op.Del {
			sv.Del(op.Key)
		} else {
			sv.Set(op.Key, op.Value)
		}
	}

	logs.Debug("[DKGCommit] stored commitment for sender=%s", sender)
	return ops, &Receipt{TxID: txID, Status: "SUCCEED", WriteCount: len(ops)}, nil
}

func (h *FrostVaultDkgCommitTxHandler) Apply(tx *pb.AnyTx) error {
	return ErrNotImplemented
}

                                                    

type FrostVaultDkgShareTxHandler struct{}

func (h *FrostVaultDkgShareTxHandler) Kind() string {
	return "frost_vault_dkg_share"
}

func (h *FrostVaultDkgShareTxHandler) DryRun(tx *pb.AnyTx, sv StateView) ([]WriteOp, *Receipt, error) {
	shareTx, ok := tx.GetContent().(*pb.AnyTx_FrostVaultDkgShareTx)
	if !ok {
		return nil, &Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: "not a dkg share tx"}, errors.New("not a dkg share tx")
	}

	req := shareTx.FrostVaultDkgShareTx
	if req == nil || req.Base == nil {
		return nil, &Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: "invalid dkg share tx"}, errors.New("invalid dkg share tx")
	}

	txID := req.Base.TxId
	sender := req.Base.FromAddress
	height := req.Base.ExecutedHeight
	chain := req.Chain
	vaultID := req.VaultId
	epochID := req.EpochId
	dealerID := req.DealerId
	receiverID := req.ReceiverId
	sessionID := strings.TrimSpace(req.SessionId)

	logs.Debug("[DKGShare] sender=%s chain=%s vault=%d epoch=%d dealer=%s receiver=%s",
		sender, chain, vaultID, epochID, dealerID, receiverID)

	                           
	if sender != dealerID {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "sender != dealer_id"}, errors.New("sender != dealer_id")
	}
	if sessionID == "" {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "missing session_id"}, errors.New("missing session_id")
	}

	                                                       
	transitionKey := keys.KeyFrostVaultTransition(chain, vaultID, epochID)
	transitionData, transitionExists, _ := sv.Get(transitionKey)
	if !transitionExists || len(transitionData) == 0 {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "transition not found"}, errors.New("transition not found")
	}

	transition, err := unmarshalFrostVaultTransition(transitionData)
	if err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to parse transition"}, err
	}
	if transition.DkgSessionId != sessionID {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "session_id mismatch"}, errors.New("session_id mismatch")
	}

	                                                
	existingKey := keys.KeyFrostVaultDkgShare(chain, vaultID, epochID, sessionID, dealerID, receiverID)
	existingData, exists, _ := sv.Get(existingKey)
	if exists && len(existingData) > 0 {
		logs.Debug("[DKGShare] already submitted, idempotent")
		return nil, &Receipt{TxID: txID, Status: "SUCCEED", WriteCount: 0}, nil
	}

	                                                                                      
	if height <= transition.DkgCommitDeadline {
		logs.Warn("[DKGShare] too early: height=%d <= deadline=%d", height, transition.DkgCommitDeadline)
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: fmt.Sprintf("sharing window violation (too early: height %d <= commit_deadline %d)", height, transition.DkgCommitDeadline)}, nil
	}
	if height > transition.DkgSharingDeadline {
		logs.Warn("[DKGShare] too late: height=%d > deadline=%d", height, transition.DkgSharingDeadline)
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: fmt.Sprintf("sharing window violation (too late: height %d > sharing_deadline %d)", height, transition.DkgSharingDeadline)}, nil
	}

	                       
	if transition.DkgStatus != DKGStatusSharing && transition.DkgStatus != DKGStatusCommitting &&
		transition.DkgStatus != DKGStatusNotStarted {
		logs.Warn("[DKGShare] invalid dkg_status for share: %s", transition.DkgStatus)
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: fmt.Sprintf("invalid dkg_status=%s", transition.DkgStatus)}, nil
	}

	ops := []WriteOp{}

	                                                                                                            
	if transition.DkgStatus == DKGStatusCommitting || transition.DkgStatus == DKGStatusNotStarted {
		transition.DkgStatus = DKGStatusSharing
		transitionData, err := proto.Marshal(transition)
		if err == nil {
			ops = append(ops, WriteOp{
				Key:      transitionKey,
				Value:    transitionData,
				Category: "frost_vault_transition",
			})
		}
	}

	                                                                
	if !isInCommittee(dealerID, transition.NewCommitteeMembers) {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "dealer not in committee"}, errors.New("dealer not in committee")
	}
	if !isInCommittee(receiverID, transition.NewCommitteeMembers) {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "receiver not in committee"}, errors.New("receiver not in committee")
	}

	                           
	if len(req.Ciphertext) == 0 {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "empty ciphertext"}, errors.New("empty ciphertext")
	}

	                  
	share := &pb.FrostVaultDkgShare{
		Chain:       chain,
		VaultId:     vaultID,
		EpochId:     epochID,
		DealerId:    dealerID,
		ReceiverId:  receiverID,
		Ciphertext:  req.Ciphertext,
		ShareHeight: height,
	}

	shareData, err := proto.Marshal(share)
	if err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to marshal share"}, err
	}

	ops = append(ops, WriteOp{
		Key:      existingKey,
		Value:    shareData,
		Category: "frost_dkg_share",
	})

	ops = appendTransitionIndexOpsFromWriteSet(ops)
	for _, op := range ops {
		if op.Del {
			sv.Del(op.Key)
		} else {
			sv.Set(op.Key, op.Value)
		}
	}

	logs.Debug("[DKGShare] stored share dealer=%s -> receiver=%s", dealerID, receiverID)
	return ops, &Receipt{TxID: txID, Status: "SUCCEED", WriteCount: len(ops)}, nil
}

func (h *FrostVaultDkgShareTxHandler) Apply(tx *pb.AnyTx) error {
	return ErrNotImplemented
}

                                                        

type FrostVaultDkgComplaintTxHandler struct{}

func (h *FrostVaultDkgComplaintTxHandler) Kind() string {
	return "frost_vault_dkg_complaint"
}

func (h *FrostVaultDkgComplaintTxHandler) DryRun(tx *pb.AnyTx, sv StateView) ([]WriteOp, *Receipt, error) {
	complaintTx, ok := tx.GetContent().(*pb.AnyTx_FrostVaultDkgComplaintTx)
	if !ok {
		return nil, &Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: "not a dkg complaint tx"}, errors.New("not a dkg complaint tx")
	}

	req := complaintTx.FrostVaultDkgComplaintTx
	if req == nil || req.Base == nil {
		return nil, &Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: "invalid dkg complaint tx"}, errors.New("invalid dkg complaint tx")
	}

	txID := req.Base.TxId
	sender := req.Base.FromAddress
	height := req.Base.ExecutedHeight
	chain := req.Chain
	vaultID := req.VaultId
	epochID := req.EpochId
	dealerID := req.DealerId
	receiverID := req.ReceiverId
	sessionID := strings.TrimSpace(req.SessionId)

	logs.Debug("[DKGComplaint] sender=%s chain=%s vault=%d epoch=%d dealer=%s receiver=%s",
		sender, chain, vaultID, epochID, dealerID, receiverID)

	                             
	if sender != receiverID {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "sender != receiver_id"}, errors.New("sender != receiver_id")
	}
	if sessionID == "" {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "missing session_id"}, errors.New("missing session_id")
	}

	                  
	complaintKey := keys.KeyFrostVaultDkgComplaint(chain, vaultID, epochID, sessionID, dealerID, receiverID)
	existingData, exists, _ := sv.Get(complaintKey)
	if exists && len(existingData) > 0 {
		logs.Debug("[DKGComplaint] already submitted, idempotent")
		return nil, &Receipt{TxID: txID, Status: "SUCCEED", WriteCount: 0}, nil
	}

	                                                       
	transitionKey := keys.KeyFrostVaultTransition(chain, vaultID, epochID)
	transitionData, transitionExists, _ := sv.Get(transitionKey)
	if !transitionExists || len(transitionData) == 0 {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "transition not found"}, errors.New("transition not found")
	}

	transition, err := unmarshalFrostVaultTransition(transitionData)
	if err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to parse transition"}, err
	}
	if transition.DkgSessionId != sessionID {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "session_id mismatch"}, errors.New("session_id mismatch")
	}

	                                                                        
	                                                                       
	                                                                         
	if height <= transition.DkgSharingDeadline ||
		transition.DkgDisputeDeadline <= height ||
		transition.DkgDisputeDeadline-height < dkgMinRevealResponseBlocks {
		logs.Warn("[DKGComplaint] height out of window: height=%d sharing_deadline=%d dispute_deadline=%d min_reveal_blocks=%d",
			height, transition.DkgSharingDeadline, transition.DkgDisputeDeadline, dkgMinRevealResponseBlocks)
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: fmt.Sprintf(
			"dispute window violation (height %d, sharing_deadline %d, dispute_deadline %d, min_reveal_blocks %d)",
			height, transition.DkgSharingDeadline, transition.DkgDisputeDeadline, dkgMinRevealResponseBlocks)}, nil
	}

	                       
	if transition.DkgStatus != DKGStatusSharing && transition.DkgStatus != DKGStatusResolving {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: fmt.Sprintf("invalid dkg_status=%s", transition.DkgStatus)}, errors.New("invalid dkg_status")
	}

	                                                                
	if !isInCommittee(dealerID, transition.NewCommitteeMembers) {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "dealer not in committee"}, errors.New("dealer not in committee")
	}
	if !isInCommittee(receiverID, transition.NewCommitteeMembers) {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "receiver not in committee"}, errors.New("receiver not in committee")
	}

	                                     
	shareKey := keys.KeyFrostVaultDkgShare(chain, vaultID, epochID, sessionID, dealerID, receiverID)
	shareData, shareExists, _ := sv.Get(shareKey)
	if !shareExists || len(shareData) == 0 {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "share not found"}, errors.New("share not found")
	}

	                                                                                                  
	                                                                                                 
	                                                                                                             
	bondAmount, err := parsePositiveBalanceStrict("complaint bond", req.Bond)
	if err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: fmt.Sprintf("invalid bond: %v", err)}, err
	}
	minBond, _ := new(big.Int).SetString(MinDkgComplaintBond, 10)
	if bondAmount.Cmp(minBond) < 0 {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: fmt.Sprintf("bond too low: have %s need %s", bondAmount.String(), MinDkgComplaintBond)}, errors.New("bond too low")
	}
	if err := moveDkgComplaintBond(sv, sender, DkgComplaintBondPoolAccount, bondAmount); err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: fmt.Sprintf("lock bond failed: %v", err)}, err
	}

	                  
	complaint := &pb.FrostVaultDkgComplaint{
		Chain:           chain,
		VaultId:         vaultID,
		EpochId:         epochID,
		DealerId:        dealerID,
		ReceiverId:      receiverID,
		Bond:            bondAmount.String(),
		ComplaintHeight: height,
		RevealDeadline:  transition.DkgDisputeDeadline,
		Status:          ComplaintStatusPending,
	}

	complaintData, err := proto.Marshal(complaint)
	if err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to marshal complaint"}, err
	}

	ops := []WriteOp{{
		Key:      complaintKey,
		Value:    complaintData,
		Category: "frost_dkg_complaint",
	}}

	                                                                     
	if transition.DkgStatus == DKGStatusSharing {
		transition.DkgStatus = DKGStatusResolving
		transitionData, err := proto.Marshal(transition)
		if err == nil {
			ops = append(ops, WriteOp{
				Key:      transitionKey,
				Value:    transitionData,
				Category: "frost_vault_transition",
			})
		}
	}

	ops = appendTransitionIndexOpsFromWriteSet(ops)
	for _, op := range ops {
		if op.Del {
			sv.Del(op.Key)
		} else {
			sv.Set(op.Key, op.Value)
		}
	}

	logs.Debug("[DKGComplaint] stored complaint dealer=%s receiver=%s", dealerID, receiverID)
	return ops, &Receipt{TxID: txID, Status: "SUCCEED", WriteCount: len(ops)}, nil
}

func (h *FrostVaultDkgComplaintTxHandler) Apply(tx *pb.AnyTx) error {
	return ErrNotImplemented
}

                                                     

type FrostVaultDkgRevealTxHandler struct{}

func (h *FrostVaultDkgRevealTxHandler) Kind() string {
	return "frost_vault_dkg_reveal"
}

func (h *FrostVaultDkgRevealTxHandler) DryRun(tx *pb.AnyTx, sv StateView) ([]WriteOp, *Receipt, error) {
	revealTx, ok := tx.GetContent().(*pb.AnyTx_FrostVaultDkgRevealTx)
	if !ok {
		return nil, &Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: "not a dkg reveal tx"}, errors.New("not a dkg reveal tx")
	}

	req := revealTx.FrostVaultDkgRevealTx
	if req == nil || req.Base == nil {
		return nil, &Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: "invalid dkg reveal tx"}, errors.New("invalid dkg reveal tx")
	}

	txID := req.Base.TxId
	sender := req.Base.FromAddress
	chain := req.Chain
	vaultID := req.VaultId
	epochID := req.EpochId
	dealerID := req.DealerId
	receiverID := req.ReceiverId
	sessionID := strings.TrimSpace(req.SessionId)

	logs.Debug("[DKGReveal] sender=%s chain=%s vault=%d epoch=%d dealer=%s receiver=%s",
		sender, chain, vaultID, epochID, dealerID, receiverID)

	                           
	if sender != dealerID {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "sender != dealer_id"}, errors.New("sender != dealer_id")
	}
	if sessionID == "" {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "missing session_id"}, errors.New("missing session_id")
	}

	                           
	complaintKey := keys.KeyFrostVaultDkgComplaint(chain, vaultID, epochID, sessionID, dealerID, receiverID)
	complaintData, complaintExists, _ := sv.Get(complaintKey)
	if !complaintExists || len(complaintData) == 0 {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "complaint not found"}, errors.New("complaint not found")
	}

	complaint := &pb.FrostVaultDkgComplaint{}
	if err := unmarshalProtoCompat(complaintData, complaint); err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to parse complaint"}, err
	}

	                                               
	if complaint.Status != ComplaintStatusPending {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: fmt.Sprintf("complaint status=%s", complaint.Status)}, errors.New("complaint not pending")
	}
	if req.Base.ExecutedHeight > complaint.RevealDeadline {
		logs.Warn("[DKGReveal] reveal deadline exceeded for complaint: height=%d deadline=%d", req.Base.ExecutedHeight, complaint.RevealDeadline)
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "reveal deadline exceeded"}, nil
	}

	                          
	if len(req.Share) == 0 || len(req.EncRand) == 0 {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "empty reveal data"}, errors.New("empty reveal data")
	}

	                              
	transitionKey := keys.KeyFrostVaultTransition(chain, vaultID, epochID)
	transitionData, transitionExists, _ := sv.Get(transitionKey)
	if !transitionExists || len(transitionData) == 0 {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "transition not found"}, errors.New("transition not found")
	}

	transition, err := unmarshalFrostVaultTransition(transitionData)
	if err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to parse transition"}, err
	}
	if transition.DkgSessionId != sessionID {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "session_id mismatch"}, errors.New("session_id mismatch")
	}

	                                                  
	commitKey := keys.KeyFrostVaultDkgCommit(chain, vaultID, epochID, sessionID, dealerID)
	commitData, commitExists, _ := sv.Get(commitKey)
	if !commitExists || len(commitData) == 0 {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "dealer commitment not found"}, errors.New("dealer commitment not found")
	}

	commitment := &pb.FrostVaultDkgCommitment{}
	if err := unmarshalProtoCompat(commitData, commitment); err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to parse commitment"}, err
	}

	shareKey := keys.KeyFrostVaultDkgShare(chain, vaultID, epochID, sessionID, dealerID, receiverID)
	shareData, shareExists, _ := sv.Get(shareKey)
	if !shareExists || len(shareData) == 0 {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "share not found"}, errors.New("share not found")
	}

	originalShare := &pb.FrostVaultDkgShare{}
	if err := unmarshalProtoCompat(shareData, originalShare); err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to parse share"}, err
	}

	                                                                                  
	receiverIndex := getReceiverIndexInCommittee(receiverID, transition.NewCommitteeMembers)
	valid := verifyRevealInDryRun(sv, receiverID, req.Share, req.EncRand, originalShare.Ciphertext, commitment.CommitmentPoints, receiverIndex, transition.SignAlgo)

	ops := []WriteOp{}

	                                                                           
	                                                                           
	if valid {
		                                                                       
		complaint.Status = ComplaintStatusResolved
		logs.Info("[DKGReveal] share is valid, false complaint by receiver=%s, bond %s forfeited to bond pool", receiverID, complaint.Bond)
	} else {
		                                            
		                                                                                                                                                                                                                                                  
		complaint.Status = ComplaintStatusDisqualified
		logs.Info("[DKGReveal] share is invalid, dealer %s disqualified", dealerID)

		                                                                       
		if complaint.Bond != "" && complaint.Bond != "0" {
			bondAmount, parseErr := parsePositiveBalanceStrict("complaint bond", complaint.Bond)
			if parseErr != nil {
				return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "invalid stored complaint bond"}, parseErr
			}
			if moveErr := moveDkgComplaintBond(sv, DkgComplaintBondPoolAccount, receiverID, bondAmount); moveErr != nil {
				return nil, &Receipt{TxID: txID, Status: "FAILED", Error: fmt.Sprintf("refund bond failed: %v", moveErr)}, moveErr
			}
			logs.Info("[DKGReveal] refunded bond %s to complainant %s", complaint.Bond, receiverID)
		}

		                          
		slashOps, err := slashMinerStake(sv, dealerID, req.Base.ExecutedHeight, "DKG dealer fault: invalid share")
		if err != nil {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: fmt.Sprintf("slash dealer stake failed: %v", err)}, err
		}
		ops = append(ops, slashOps...)
		logs.Info("[DKGReveal] slashed 100%% stake from dealer %s", dealerID)

		                                                                  
	}

	                                           
	updatedComplaintData, err := proto.Marshal(complaint)
	if err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to marshal complaint"}, err
	}
	ops = append(ops, WriteOp{
		Key:      complaintKey,
		Value:    updatedComplaintData,
		Category: "frost_dkg_complaint",
	})

	for _, op := range ops {
		if op.Del {
			sv.Del(op.Key)
		} else {
			sv.Set(op.Key, op.Value)
		}
	}

	logs.Debug("[DKGReveal] resolved complaint dealer=%s receiver=%s valid=%v", dealerID, receiverID, valid)
	return ops, &Receipt{TxID: txID, Status: "SUCCEED", WriteCount: len(ops)}, nil
}

func (h *FrostVaultDkgRevealTxHandler) Apply(tx *pb.AnyTx) error {
	return ErrNotImplemented
}

                     
func verifyRevealInDryRun(sv StateView, receiverID string, share, encRand, ciphertext []byte, commitmentPoints [][]byte, receiverIndex int, signAlgo pb.SignAlgo) bool {
	if len(share) == 0 || len(encRand) == 0 || len(ciphertext) == 0 || len(commitmentPoints) == 0 {
		return false
	}

	receiverPubKey := resolveReceiverECIESPubKey(sv, receiverID)
	if len(receiverPubKey) == 0 {
		logs.Warn("[verifyRevealInDryRun] receiverPubKey not found for %s", receiverID)
		return false
	}

	if !security.ECIESVerifyCiphertext(receiverPubKey, share, encRand, ciphertext) {
		logs.Debug("[verifyRevealInDryRun] ciphertext verification failed")
		return false
	}

	if receiverIndex <= 0 {
		return false
	}

	                                                                                                                        
	group, err := groupForSignAlgo(signAlgo)
	if err != nil {
		logs.Warn("[verifyRevealInDryRun] unsupported sign algo: %v", err)
		return false
	}
	receiverIndexBig := big.NewInt(int64(receiverIndex))
	if !security.VerifyShareAgainstCommitmentOnGroup(share, commitmentPoints, receiverIndexBig, group) {
		logs.Debug("[verifyRevealInDryRun] share commitment verification failed")
		return false
	}

	return true
}

                                                                                                                           
func getReceiverIndexInCommittee(receiverID string, committeeMembers []string) int {
	for i, member := range committeeMembers {
		if member == receiverID {
			return i + 1                 
		}
	}
	return 0
}

                                                              
func removeFromCommittee(committee []string, memberID string) []string {
	result := make([]string, 0, len(committee))
	for _, m := range committee {
		if m != memberID {
			result = append(result, m)
		}
	}
	return result
}

                                                               

type FrostVaultDkgValidationSignedTxHandler struct{}

func (h *FrostVaultDkgValidationSignedTxHandler) Kind() string {
	return "frost_vault_dkg_validation_signed"
}

func (h *FrostVaultDkgValidationSignedTxHandler) DryRun(tx *pb.AnyTx, sv StateView) ([]WriteOp, *Receipt, error) {
	validationTx, ok := tx.GetContent().(*pb.AnyTx_FrostVaultDkgValidationSignedTx)
	if !ok {
		return nil, &Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: "not a dkg validation signed tx"}, errors.New("not a dkg validation signed tx")
	}

	req := validationTx.FrostVaultDkgValidationSignedTx
	if req == nil || req.Base == nil {
		return nil, &Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: "invalid dkg validation signed tx"}, errors.New("invalid dkg validation signed tx")
	}

	txID := req.Base.TxId
	chain := req.Chain
	vaultID := req.VaultId
	epochID := req.EpochId
	sessionID := strings.TrimSpace(req.SessionId)

	sender := req.Base.FromAddress
	logs.Debug("[DKGValidationSigned] sender=%s chain=%s vault=%d epoch=%d", sender, chain, vaultID, epochID)
	if sessionID == "" {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "missing session_id"}, errors.New("missing session_id")
	}

	                                                    
	transitionKey := keys.KeyFrostVaultTransition(chain, vaultID, epochID)
	transitionData, transitionExists, _ := sv.Get(transitionKey)
	if !transitionExists || len(transitionData) == 0 {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "transition not found"}, errors.New("transition not found")
	}

	transition, err := unmarshalFrostVaultTransition(transitionData)
	if err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to parse transition"}, err
	}
	if transition.DkgSessionId != sessionID {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "session_id mismatch"}, errors.New("session_id mismatch")
	}

	                                                                         
	height := req.Base.ExecutedHeight
	if height <= transition.DkgDisputeDeadline {
		logs.Warn("[DKGValidation] dispute window not closed: height=%d deadline=%d", height, transition.DkgDisputeDeadline)
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: fmt.Sprintf("dispute window not closed (height %d <= deadline %d)", height, transition.DkgDisputeDeadline)}, nil
	}

	if transition.DkgStatus != DKGStatusSharing && transition.DkgStatus != DKGStatusResolving && transition.DkgStatus != DKGStatusCommitting {
		                                                                                                                                                          
		if transition.DkgStatus == DKGStatusKeyReady {
			return nil, &Receipt{TxID: txID, Status: "SUCCEED", WriteCount: 0}, nil
		}
		                                                          
		logs.Warn("[DKGValidation] invalid dkg_status: %s", transition.DkgStatus)
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: fmt.Sprintf("invalid dkg_status=%s", transition.DkgStatus)}, nil
	}

	                                                                                                                                                                                                                                                               
	if !isInCommittee(sender, transition.NewCommitteeMembers) {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "sender not in committee"}, nil
	}

	                                                                                                                                                                                                 
	expectedHash := computeDkgValidationMsgHash(chain, vaultID, epochID, transition.SignAlgo, req.NewGroupPubkey)
	if !bytes.Equal(expectedHash, req.MsgHash) {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "msg_hash mismatch"}, nil
	}

	derivedGroupPubkey, err := deriveDkgGroupPubkeyFromCommitments(sv, chain, vaultID, epochID, sessionID, transition.NewCommitteeMembers, transition.SignAlgo)
	if err != nil {
		logs.Warn("[DKGValidation] derive group pubkey failed: %v", err)
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "derive group pubkey failed"}, nil
	}
	if !bytes.Equal(req.NewGroupPubkey, derivedGroupPubkey) {
		logs.Warn("[DKGValidation] group pubkey mismatch sender=%s req=%s derived=%s",
			sender, shortHexBytes(req.NewGroupPubkey, 16), shortHexBytes(derivedGroupPubkey, 16))
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "group_pubkey mismatch with commitments"}, nil
	}
	if len(transition.NewGroupPubkey) > 0 && !bytes.Equal(transition.NewGroupPubkey, derivedGroupPubkey) {
		logs.Warn("[DKGValidation] transition group pubkey conflict transition=%s derived=%s",
			shortHexBytes(transition.NewGroupPubkey, 16), shortHexBytes(derivedGroupPubkey, 16))
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "transition group_pubkey conflict"}, nil
	}
	if len(transition.ValidationMsgHash) > 0 && !bytes.Equal(transition.ValidationMsgHash, req.MsgHash) {
		logs.Warn("[DKGValidation] validation msg_hash conflict transition=%s req=%s",
			shortHexBytes(transition.ValidationMsgHash, 16), shortHexBytes(req.MsgHash, 16))
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "validation msg_hash conflict"}, nil
	}
	if err := verifyValidationAckSignature(sv, sender, req.Base, req.MsgHash, req.Signature); err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: err.Error()}, nil
	}
	pendingComplaints, err := hasPendingDkgComplaints(sv, chain, vaultID, epochID, sessionID)
	if err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "scan complaints failed"}, err
	}
	if pendingComplaints {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "pending complaints not resolved"}, nil
	}

	ackKey := keys.KeyFrostVaultDkgValidationAck(chain, vaultID, epochID, sessionID, sender)
	existingAck, ackExists, _ := sv.Get(ackKey)
	if ackExists && validationAckAlreadyRecorded(existingAck, req.MsgHash) {
		logs.Debug("[DKGValidation] duplicate validation ack sender=%s chain=%s vault=%d epoch=%d", sender, chain, vaultID, epochID)
		return nil, &Receipt{TxID: txID, Status: "SUCCEED", WriteCount: 0}, nil
	}
	if ackExists {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "validation ack conflict"}, nil
	}

	ackCount, err := countDkgValidationAcks(sv, chain, vaultID, epochID, sessionID)
	if err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "scan validation acks failed"}, err
	}
	ackTransition, err := advanceDkgValidationAck(dkgValidationAckTransitionInput{
		transition:        transition,
		ackCount:          ackCount,
		validationMsgHash: req.MsgHash,
		derivedGroupKey:   derivedGroupPubkey,
	})
	if err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "invalid dkg threshold"}, nil
	}
	                                                                          
	                                                                           
	                                                                          
	                                                                          
	requiredAcks := ackTransition.requiredAcks
	transition = ackTransition.transition

	ackOp := WriteOp{
		Key:      ackKey,
		Value:    append([]byte(nil), req.MsgHash...),
		Category: "frost_dkg_validation_ack",
	}

	if !ackTransition.keyReady {
		ops := []WriteOp{ackOp}
		updatedTransitionData, err := proto.Marshal(transition)
		if err != nil {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to marshal transition"}, err
		}
		ops = append(ops, WriteOp{
			Key:      transitionKey,
			Value:    updatedTransitionData,
			Category: "frost_vault_transition",
		})

		ops = appendTransitionIndexOpsFromWriteSet(ops)
		for _, op := range ops {
			if op.Del {
				sv.Del(op.Key)
			} else {
				sv.Set(op.Key, op.Value)
			}
		}

		logs.Info("[DKGValidation] recorded validation ack %d/%d from %s for group pubkey %s",
			ackCount+1, requiredAcks, sender, shortHexBytes(derivedGroupPubkey, 8))
		return ops, &Receipt{TxID: txID, Status: "SUCCEED", WriteCount: len(ops)}, nil
	}

	logs.Info("[DKGValidation] all committee members durably acknowledged validation %d/%d for group pubkey %s",
		ackCount+1, requiredAcks, shortHexBytes(derivedGroupPubkey, 8))
	updatedTransitionData, err := proto.Marshal(transition)
	if err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to marshal transition"}, err
	}

	                             
	vaultKey := keys.KeyFrostVaultState(chain, vaultID)
	vaultData, vaultExists, _ := sv.Get(vaultKey)

	var vault *pb.FrostVaultState
	if vaultExists && len(vaultData) > 0 {
		vault, _ = unmarshalFrostVaultState(vaultData)
	}
	                                                                            
	                                                                         
	                                                                      
	vaultTransition, err := applyDkgVaultActivation(dkgVaultActivationTransitionInput{
		chain:      chain,
		vaultID:    vaultID,
		epochID:    epochID,
		height:     req.Base.ExecutedHeight,
		transition: transition,
		vault:      vault,
	})
	if err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: err.Error()}, nil
	}
	vault = vaultTransition.vault
	deferVaultActivation := vaultTransition.activationDeferred
	if vaultTransition.writeVault && vault.Status == VaultStatusActive {
		logs.Info("[DKGValidation] Vault %d activated automatically (first epoch)", vaultID)
	}
	                                                                                                                                                    
	                                                                                            
	if transition.Lifecycle != "" {
		                                                                                        
		                                                            
		                                                                                                                                     
	}

	ops := []WriteOp{
		{Key: transitionKey, Value: updatedTransitionData, Category: "frost_vault_transition"},
	}
	if vaultTransition.writeVault {
		updatedVaultData, err := proto.Marshal(vault)
		if err != nil {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to marshal vault"}, err
		}
		ops = append(ops, WriteOp{Key: vaultKey, Value: updatedVaultData, Category: "frost_vault_state"})
	}

	ops = appendTransitionIndexOpsFromWriteSet(ops)
	cleanupCount := 0
	ops, cleanupCount, err = appendFrostDkgSessionCleanupOps(sv, ops, chain, vaultID, epochID, sessionID)
	if err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "scan dkg temp keys failed"}, err
	}
	for _, op := range ops {
		if op.Del {
			sv.Del(op.Key)
		} else {
			sv.Set(op.Key, op.Value)
		}
	}

	logs.Debug("[DKGValidationSigned] vault=%d epoch=%d transition=KEY_READY activation_deferred=%t cleanup=%d", vaultID, epochID, deferVaultActivation, cleanupCount)
	return ops, &Receipt{TxID: txID, Status: "SUCCEED", WriteCount: len(ops)}, nil
}

func (h *FrostVaultDkgValidationSignedTxHandler) Apply(tx *pb.AnyTx) error {
	return ErrNotImplemented
}

func deriveDkgGroupPubkeyFromCommitments(sv StateView, chain string, vaultID uint32, epochID uint64, sessionID string, committee []string, signAlgo pb.SignAlgo) ([]byte, error) {
	if len(committee) == 0 {
		return nil, fmt.Errorf("empty committee")
	}

	grp, err := groupForSignAlgo(signAlgo)
	if err != nil {
		return nil, err
	}

	var sum curve.Point
	hasPoint := false

	for _, member := range committee {
		commitKey := keys.KeyFrostVaultDkgCommit(chain, vaultID, epochID, sessionID, member)
		commitData, exists, err := sv.Get(commitKey)
		if err != nil {
			return nil, fmt.Errorf("load commitment %s: %w", commitKey, err)
		}
		if !exists || len(commitData) == 0 {
			return nil, fmt.Errorf("missing commitment for member=%s", member)
		}

		var commit pb.FrostVaultDkgCommitment
		if err := proto.Unmarshal(commitData, &commit); err != nil {
			return nil, fmt.Errorf("decode commitment for member=%s: %w", member, err)
		}
		if len(commit.AI0) == 0 {
			return nil, fmt.Errorf("empty AI0 commitment for member=%s", member)
		}

		pt := grp.DecompressPoint(commit.AI0)
		if pt.X == nil || pt.Y == nil {
			return nil, fmt.Errorf("invalid AI0 commitment for member=%s", member)
		}

		if !hasPoint {
			sum = pt
			hasPoint = true
			continue
		}
		sum = grp.Add(sum, pt)
	}

	if !hasPoint {
		return nil, fmt.Errorf("no valid AI0 commitments")
	}
	raw := grp.SerializePoint(sum)
	if signAlgo == pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128 {
		if _, err := evm.CanonicalBN128PublicKey(raw); err != nil {
			return nil, fmt.Errorf("invalid aggregate DKG authority: %w", err)
		}
	}
	return raw, nil
}

func groupForSignAlgo(signAlgo pb.SignAlgo) (curve.Group, error) {
	switch signAlgo {
	case pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340:
		return curve.NewSecp256k1Group(), nil
	case pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128:
		return curve.NewBN256Group(), nil
	default:
		return nil, fmt.Errorf("unsupported sign algo: %v", signAlgo)
	}
}

func shortHexBytes(data []byte, n int) string {
	if len(data) == 0 {
		return ""
	}
	if n <= 0 || len(data) <= n {
		return fmt.Sprintf("%x", data)
	}
	return fmt.Sprintf("%x", data[:n])
}

                                                            

type FrostVaultTransitionSignedTxHandler struct{}

func (h *FrostVaultTransitionSignedTxHandler) Kind() string {
	return "frost_vault_transition_signed"
}

func findPendingRechargeRequestForVault(sv StateView, chainName string, vaultID uint32) (string, error) {
	kvs, err := sv.Scan(keys.KeyRechargeRequestPrefix())
	if err != nil {
		return "", fmt.Errorf("scan recharge requests: %w", err)
	}

	requestKeys := make([]string, 0, len(kvs))
	for key := range kvs {
		requestKeys = append(requestKeys, key)
	}
	sort.Strings(requestKeys)

	normalizedChain := chainpkg.NormalizeChain(chainName)
	for _, key := range requestKeys {
		var req pb.RechargeRequest
		if err := proto.Unmarshal(kvs[key], &req); err != nil {
			return "", fmt.Errorf("parse recharge request %q: %w", key, err)
		}
		if req.VaultId != vaultID || chainpkg.NormalizeChain(req.NativeChain) != normalizedChain || rechargeRequestTerminal(req.Status) {
			continue
		}

		requestID := req.RequestId
		if requestID == "" {
			requestID = key
		}
		return requestID, nil
	}

	return "", nil
}

                                                                          
func (h *FrostVaultTransitionSignedTxHandler) dryRunLegacyTransitionSigned(tx *pb.AnyTx, sv StateView) ([]WriteOp, *Receipt, error) {
	return transitionSignedFailure(tx.GetTxId(), "legacy transition handler is disabled")
}

func (h *FrostVaultTransitionSignedTxHandler) Apply(tx *pb.AnyTx) error {
	return ErrNotImplemented
}

                                                                                                                                                       
                                                                                           
func slashBond(sv StateView, address string, bondAmount *big.Int, reason string) ([]WriteOp, error) {
	if bondAmount == nil || bondAmount.Sign() <= 0 {
		return nil, fmt.Errorf("invalid bond amount")
	}

	                                                           
	tokenAddr := "FB"

	                                                             
	fbBal := GetBalance(sv, address, tokenAddr)

	                                                                                                                                                                                        
	balance, err := parseBalanceStrict("balance", fbBal.Balance)
	if err != nil {
		return nil, fmt.Errorf("invalid balance state: %w", err)
	}

	                                                                                                     
	slashAmount := bondAmount
	if balance.Cmp(bondAmount) < 0 {
		slashAmount = balance
		logs.Warn("[slashBond] bond amount %s exceeds balance %s for %s, slashing only available balance", bondAmount.String(), balance.String(), address)
	}

	newBalance, err := SafeSub(balance, slashAmount)
	if err != nil {
		return nil, fmt.Errorf("failed to subtract bond: %w", err)
	}

	fbBal.Balance = newBalance.String()
	SetBalance(sv, address, tokenAddr, fbBal)

	logs.Info("[slashBond] slashed bond %s from %s (reason: %s), remaining balance: %s", slashAmount.String(), address, reason, newBalance.String())

	                                                                                                                             
	return []WriteOp{balanceWriteOp(sv, address, tokenAddr)}, nil
}

                                                                                                                                                           
                               
func slashMinerStake(sv StateView, address string, exitHeight uint64, reason string) ([]WriteOp, error) {
	tokenAddr := "FB"
	accountKey := keys.KeyAccount(address)
	accountData, exists, err := sv.Get(accountKey)
	if err != nil {
		return nil, fmt.Errorf("read dealer account: %w", err)
	}
	if !exists || len(accountData) == 0 {
		return nil, fmt.Errorf("dealer account not found: %s", address)
	}
	var account pb.Account
	if err := unmarshalProtoCompat(accountData, &account); err != nil {
		return nil, fmt.Errorf("parse dealer account %s: %w", address, err)
	}
	wasMiner := account.IsMiner
	account.IsMiner = false
	updatedAccountData, err := MarshalProto(&account)
	if err != nil {
		return nil, fmt.Errorf("marshal dealer account %s: %w", address, err)
	}

	                                                                   
	fbBal := GetBalance(sv, address, tokenAddr)
	lockedBalance, err := parseBalanceStrict("miner locked balance", fbBal.MinerLockedBalance)
	if err != nil {
		return nil, fmt.Errorf("invalid miner locked balance state: %w", err)
	}
	ws := make([]WriteOp, 0, 3)
	if wasMiner {
		ws = append(ws, WriteOp{Key: accountKey, Value: updatedAccountData, Category: "account"})
	}
	if err := ensureMinerExitRecord(sv, address, exitHeight); err != nil {
		return nil, fmt.Errorf("persist slashed miner exit: %w", err)
	}
	if exitData, exitExists, readErr := sv.Get(keys.KeyMinerExitRecord(address)); readErr == nil && exitExists {
		ws = append(ws, WriteOp{Key: keys.KeyMinerExitRecord(address), Value: exitData, Category: "miner_exit"})
	}
	if lockedBalance.Sign() <= 0 {
		logs.Warn("[slashMinerStake] no locked balance for miner=%s; miner flag still revoked", address)
		return ws, nil
	}

	                                                                           
	                                                                          
	                                                                      
	slashPool := GetBalance(sv, DkgSlashPoolAccount, tokenAddr)
	poolAvailable, err := parseBalanceStrict("dkg slash pool balance", slashPool.Balance)
	if err != nil {
		return nil, err
	}
	nextPool, err := SafeAdd(poolAvailable, lockedBalance)
	if err != nil {
		return nil, err
	}
	fbBal.MinerLockedBalance = "0"
	slashPool.Balance = nextPool.String()
	SetBalance(sv, address, tokenAddr, fbBal)
	SetBalance(sv, DkgSlashPoolAccount, tokenAddr, slashPool)

	logs.Info("[slashMinerStake] slashed 100%% stake %s from miner=%s (reason: %s)", lockedBalance.String(), address, reason)

	ws = append(ws, balanceWriteOp(sv, address, tokenAddr))
	ws = append(ws, balanceWriteOp(sv, DkgSlashPoolAccount, tokenAddr))

	return ws, nil
}
