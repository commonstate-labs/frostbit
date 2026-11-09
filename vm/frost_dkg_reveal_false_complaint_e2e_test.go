package vm

import (
	"bytes"
	"math/big"
	"testing"

	"dex/frost/core/curve"
	"dex/frost/security"
	"dex/keys"
	"dex/pb"
	"dex/utils"

	"google.golang.org/protobuf/proto"
)

                                                                                         
  
                                                                     
                                                                                                  
                                                      
                                       
                                    
                                                              
                                                              
                                                                                          
                                                                                                              
                                                                     
  
                                                                                     
                                                                                                          
                                                              

func TestFrostVaultDkgReveal_E2E_HonestDealer_FalseComplaint(t *testing.T) {
	sv := NewMockStateView()

	chain := "eth"
	vaultID := uint32(0)
	epochID := uint64(1)
	sessionID := "sess_e2e_false_complaint"
	dealerID := "0xDEALER_HONEST"
	const revealDeadline = uint64(200)
	const execHeight = uint64(120)

	                                                                        
	km := utils.NewKeyManager()
	if err := km.InitKeyRandom(); err != nil {
		t.Fatalf("InitKeyRandom: %v", err)
	}
	receiverID := km.GetAddress()
	receiverPub := km.PublicKeyBytes()
	receiverXOnly, err := utils.NormalizeSecp256k1XOnlyPubKey(receiverPub)
	if err != nil {
		t.Fatalf("NormalizeSecp256k1XOnlyPubKey: %v", err)
	}

	                                                                                   
	receiverAcct := &pb.Account{
		Address: receiverID,
		PublicKeys: &pb.PublicKeys{
			Keys: map[int32][]byte{
				int32(pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340): receiverXOnly,
			},
		},
	}
	racctData, _ := proto.Marshal(receiverAcct)
	sv.Set(keys.KeyAccount(receiverID), racctData)

	am := &pb.FrostActiveMiners{
		Addresses:  []string{receiverID},
		PublicKeys: [][]byte{receiverPub},
	}
	amData, _ := proto.Marshal(am)
	sv.Set(keys.KeyFrostActiveMiners(), amData)

	                                                                             
	grp := curve.NewSecp256k1Group()
	share := bytes.Repeat([]byte{0x42}, 32)
	encRand := bytes.Repeat([]byte{0x17}, 32)

	ciphertext, err := security.ECIESEncrypt(receiverPub, share, encRand)
	if err != nil {
		t.Fatalf("ECIESEncrypt: %v", err)
	}
	commitA0 := grp.SerializePoint(grp.ScalarBaseMult(new(big.Int).SetBytes(share)))

	                             
	                                                                                         
	                                                                                                       
	transition := &pb.VaultTransitionState{
		Chain:               chain,
		VaultId:             vaultID,
		EpochId:             epochID,
		SignAlgo:            pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		DkgStatus:           DKGStatusResolving,
		DkgSessionId:        sessionID,
		DkgSharingDeadline:  100,
		DkgDisputeDeadline:  revealDeadline,
		DkgThresholdT:       1,
		DkgN:                2,
		NewCommitteeMembers: []string{dealerID, receiverID},
	}
	transitionKey := keys.KeyFrostVaultTransition(chain, vaultID, epochID)
	if err := setTransitionStateWithIndex(sv, transitionKey, transition); err != nil {
		t.Fatalf("seed transition: %v", err)
	}

	                                                             
	commitment := &pb.FrostVaultDkgCommitment{
		Chain:            chain,
		VaultId:          vaultID,
		EpochId:          epochID,
		MinerAddress:     dealerID,
		SignAlgo:         pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		CommitmentPoints: [][]byte{commitA0},
		AI0:              commitA0,
	}
	cData, _ := proto.Marshal(commitment)
	sv.Set(keys.KeyFrostVaultDkgCommit(chain, vaultID, epochID, sessionID, dealerID), cData)

	                                                         
	shareRecord := &pb.FrostVaultDkgShare{
		Chain:      chain,
		VaultId:    vaultID,
		EpochId:    epochID,
		DealerId:   dealerID,
		ReceiverId: receiverID,
		Ciphertext: ciphertext,
	}
	sData, _ := proto.Marshal(shareRecord)
	sv.Set(keys.KeyFrostVaultDkgShare(chain, vaultID, epochID, sessionID, dealerID, receiverID), sData)

	                                                
	const bondAmount = "100000000"                              
	complaint := &pb.FrostVaultDkgComplaint{
		Chain:           chain,
		VaultId:         vaultID,
		EpochId:         epochID,
		DealerId:        dealerID,
		ReceiverId:      receiverID,
		Bond:            bondAmount,
		ComplaintHeight: 110,
		RevealDeadline:  revealDeadline,
		Status:          ComplaintStatusPending,
	}
	complaintKey := keys.KeyFrostVaultDkgComplaint(chain, vaultID, epochID, sessionID, dealerID, receiverID)
	complaintData, _ := proto.Marshal(complaint)
	sv.Set(complaintKey, complaintData)

	                                                                                       
	const dealerLocked = "5000000000"         
	dealerAcct := &pb.Account{Address: dealerID, IsMiner: true}
	dacctData, _ := proto.Marshal(dealerAcct)
	sv.Set(keys.KeyAccount(dealerID), dacctData)
	dealerBal := &pb.TokenBalanceRecord{
		Address: dealerID, Token: "FB",
		Balance: &pb.TokenBalance{
			Balance:            "0",
			MinerLockedBalance: dealerLocked,
		},
	}
	dbalData, _ := proto.Marshal(dealerBal)
	sv.Set(keys.KeyBalance(dealerID, "FB"), dbalData)

	                                         
	tx := &pb.AnyTx{
		Content: &pb.AnyTx_FrostVaultDkgRevealTx{
			FrostVaultDkgRevealTx: &pb.FrostVaultDkgRevealTx{
				Base: &pb.BaseMessage{
					TxId:           "tx_reveal_honest_dealer",
					FromAddress:    dealerID,                               
					ExecutedHeight: execHeight,
				},
				Chain:      chain,
				VaultId:    vaultID,
				EpochId:    epochID,
				DealerId:   dealerID,
				ReceiverId: receiverID,
				Share:      share,
				EncRand:    encRand,
				SessionId:  sessionID,
			},
		},
	}

	handler := &FrostVaultDkgRevealTxHandler{}
	ops, receipt, err := handler.DryRun(tx, sv)
	if err != nil {
		t.Fatalf("DryRun unexpected err: %v", err)
	}
	if receipt == nil || receipt.Status != "SUCCEED" {
		t.Fatalf("expected SUCCEED receipt, got %+v", receipt)
	}

	                                                  
	updatedComplaintData, exists, _ := sv.Get(complaintKey)
	if !exists {
		t.Fatal("complaint 记录应保留（Status 更新而非删除）")
	}
	var updatedComplaint pb.FrostVaultDkgComplaint
	if err := proto.Unmarshal(updatedComplaintData, &updatedComplaint); err != nil {
		t.Fatalf("unmarshal complaint: %v", err)
	}
	if updatedComplaint.Status != ComplaintStatusResolved {
		t.Fatalf("诬告结论应为 RESOLVED（complaint rejected），实际=%s", updatedComplaint.Status)
	}

	                                                                        
	                                                              
	deferred := mustLoadTransition(t, sv, transitionKey)
	if !isInCommittee(receiverID, deferred.NewCommitteeMembers) {
		t.Fatalf("receiver must remain until dispute deadline, committee=%v", deferred.NewCommitteeMembers)
	}
	commitKey := keys.KeyFrostVaultDkgCommit(chain, vaultID, epochID, sessionID, dealerID)
	if _, stillExists, _ := sv.Get(commitKey); !stillExists {
		t.Fatal("reveal tx must preserve the current transcript")
	}

	(&Executor{}).processFrostDkgTimeouts(&pb.Block{Header: &pb.BlockHeader{Height: revealDeadline}}, sv)

	                                                              
	updatedTData, _, _ := sv.Get(transitionKey)
	var updatedTransition pb.VaultTransitionState
	if err := proto.Unmarshal(updatedTData, &updatedTransition); err != nil {
		t.Fatalf("unmarshal transition: %v", err)
	}
	for _, m := range updatedTransition.NewCommitteeMembers {
		if m == receiverID {
			t.Fatalf("诬告者 %s 应被从 qualified_set 移除，当前 committee=%v",
				receiverID, updatedTransition.NewCommitteeMembers)
		}
	}
	                                  
	foundDealer := false
	for _, m := range updatedTransition.NewCommitteeMembers {
		if m == dealerID {
			foundDealer = true
			break
		}
	}
	if !foundDealer {
		t.Fatalf("诚实 dealer 必须仍在 qualified_set 内，当前 committee=%v",
			updatedTransition.NewCommitteeMembers)
	}
	if updatedTransition.DkgN != uint32(len(updatedTransition.NewCommitteeMembers)) {
		t.Fatalf("DkgN 应与 NewCommitteeMembers 长度同步：DkgN=%d len=%d",
			updatedTransition.DkgN, len(updatedTransition.NewCommitteeMembers))
	}

	                                                                                     
	if _, stillExists, _ := sv.Get(commitKey); stillExists {
		t.Fatal("old dealer commitment must be cleaned after fresh retry")
	}
	for _, op := range ops {
		if op.Key == commitKey && op.Del {
			t.Fatal("reveal tx itself must not delete dealer commitment")
		}
	}

	                                                                            
	postDealerBalData, _, _ := sv.Get(keys.KeyBalance(dealerID, "FB"))
	var postDealerBal pb.TokenBalanceRecord
	if err := proto.Unmarshal(postDealerBalData, &postDealerBal); err != nil {
		t.Fatalf("unmarshal dealer balance: %v", err)
	}
	if postDealerBal.Balance == nil || postDealerBal.Balance.MinerLockedBalance != dealerLocked {
		t.Fatalf("诚实 dealer 的 MinerLocked 不应被扣：pre=%s post=%v",
			dealerLocked, postDealerBal.Balance)
	}

	                                                                                                                     
	                                                                                                
	recvBalData, recvHasKey, _ := sv.Get(keys.KeyBalance(receiverID, DkgComplaintBondToken))
	if recvHasKey {
		var recvBal pb.TokenBalanceRecord
		if err := proto.Unmarshal(recvBalData, &recvBal); err != nil {
			t.Fatalf("unmarshal receiver balance: %v", err)
		}
		if recvBal.Balance != nil {
			avail, _ := new(big.Int).SetString(recvBal.Balance.Balance, 10)
			if avail != nil && avail.Sign() > 0 {
				t.Fatalf("诬告者的 bond 不应被退回（Insurance→Available），实得 available=%s",
					recvBal.Balance.Balance)
			}
		}
	}
}
