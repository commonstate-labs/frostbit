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

func TestFrostVaultDkgReveal_InvalidShareConservesBondAndSlashedStake(t *testing.T) {
	sv := NewMockStateView()
	chain, vaultID, epochID := "eth", uint32(0), uint64(1)
	sessionID := "eth_0_1_1"
	dealerID := "dealer"
	const disputeDeadline = uint64(200)
	const bondAmount = "100000000"
	const dealerStake = "500000000"

	km := utils.NewKeyManager()
	if err := km.InitKeyRandom(); err != nil {
		t.Fatalf("init receiver key: %v", err)
	}
	receiverID := km.GetAddress()
	receiverPub := km.PublicKeyBytes()
	receiverXOnly, err := utils.NormalizeSecp256k1XOnlyPubKey(receiverPub)
	if err != nil {
		t.Fatalf("normalize receiver key: %v", err)
	}
	receiverAccountData, _ := proto.Marshal(&pb.Account{
		Address: receiverID,
		PublicKeys: &pb.PublicKeys{Keys: map[int32][]byte{
			int32(pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340): receiverXOnly,
		}},
	})
	sv.Set(keys.KeyAccount(receiverID), receiverAccountData)
	activeData, _ := proto.Marshal(&pb.FrostActiveMiners{Addresses: []string{receiverID}, PublicKeys: [][]byte{receiverPub}})
	sv.Set(keys.KeyFrostActiveMiners(), activeData)

	validShare := bytes.Repeat([]byte{0x42}, 32)
	invalidRevealShare := bytes.Repeat([]byte{0x43}, 32)
	encRand := bytes.Repeat([]byte{0x17}, 32)
	ciphertext, err := security.ECIESEncrypt(receiverPub, validShare, encRand)
	if err != nil {
		t.Fatalf("encrypt share: %v", err)
	}
	group := curve.NewSecp256k1Group()
	commitA0 := group.SerializePoint(group.ScalarBaseMult(new(big.Int).SetBytes(validShare)))

	transitionKey := keys.KeyFrostVaultTransition(chain, vaultID, epochID)
	transition := &pb.VaultTransitionState{
		Chain: chain, VaultId: vaultID, EpochId: epochID,
		SignAlgo:      pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		TriggerHeight: 1, DkgAttempt: 1, DkgSessionId: sessionID,
		DkgStatus: DKGStatusResolving, ValidationStatus: "NOT_STARTED",
		DkgCommitDeadline: 50, DkgSharingDeadline: 100, DkgDisputeDeadline: disputeDeadline,
		DkgThresholdT: 1, DkgN: 2, NewCommitteeMembers: []string{dealerID, receiverID},
	}
	if err := setTransitionStateWithIndex(sv, transitionKey, transition); err != nil {
		t.Fatalf("seed transition: %v", err)
	}
	commitData, _ := proto.Marshal(&pb.FrostVaultDkgCommitment{
		Chain: chain, VaultId: vaultID, EpochId: epochID, MinerAddress: dealerID,
		SignAlgo:         pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		CommitmentPoints: [][]byte{commitA0}, AI0: commitA0,
	})
	sv.Set(keys.KeyFrostVaultDkgCommit(chain, vaultID, epochID, sessionID, dealerID), commitData)
	shareData, _ := proto.Marshal(&pb.FrostVaultDkgShare{
		Chain: chain, VaultId: vaultID, EpochId: epochID,
		DealerId: dealerID, ReceiverId: receiverID, Ciphertext: ciphertext,
	})
	sv.Set(keys.KeyFrostVaultDkgShare(chain, vaultID, epochID, sessionID, dealerID, receiverID), shareData)
	complaintKey := keys.KeyFrostVaultDkgComplaint(chain, vaultID, epochID, sessionID, dealerID, receiverID)
	complaintData, _ := proto.Marshal(&pb.FrostVaultDkgComplaint{
		Chain: chain, VaultId: vaultID, EpochId: epochID,
		DealerId: dealerID, ReceiverId: receiverID, Bond: bondAmount,
		RevealDeadline: disputeDeadline, Status: ComplaintStatusPending,
	})
	sv.Set(complaintKey, complaintData)
	seedAvailableBalance(t, sv, DkgComplaintBondPoolAccount, bondAmount)

	dealerAccountData, _ := proto.Marshal(&pb.Account{Address: dealerID, IsMiner: true})
	sv.Set(keys.KeyAccount(dealerID), dealerAccountData)
	seedMinerLockedBalance(t, sv, dealerID, dealerStake)

	tx := &pb.AnyTx{Content: &pb.AnyTx_FrostVaultDkgRevealTx{FrostVaultDkgRevealTx: &pb.FrostVaultDkgRevealTx{
		Base:  &pb.BaseMessage{TxId: "invalid-reveal", FromAddress: dealerID, ExecutedHeight: 120},
		Chain: chain, VaultId: vaultID, EpochId: epochID, SessionId: sessionID,
		DealerId: dealerID, ReceiverId: receiverID, Share: invalidRevealShare, EncRand: encRand,
	}}}
	_, receipt, err := (&FrostVaultDkgRevealTxHandler{}).DryRun(tx, sv)
	if err != nil || receipt == nil || receipt.Status != "SUCCEED" {
		t.Fatalf("invalid-share adjudication failed: receipt=%+v err=%v", receipt, err)
	}

	if got := GetBalance(sv, receiverID, DkgComplaintBondToken).Balance; got != bondAmount {
		t.Fatalf("complaint bond refund=%s want=%s", got, bondAmount)
	}
	if got := GetBalance(sv, DkgComplaintBondPoolAccount, DkgComplaintBondToken).Balance; got != "0" {
		t.Fatalf("bond pool=%s want=0", got)
	}
	if got := GetBalance(sv, dealerID, "FB").MinerLockedBalance; got != "0" {
		t.Fatalf("dealer locked stake=%s want=0", got)
	}
	if got := GetBalance(sv, DkgSlashPoolAccount, "FB").Balance; got != dealerStake {
		t.Fatalf("slash pool=%s want=%s", got, dealerStake)
	}

	totalAfter := new(big.Int)
	for _, raw := range []string{
		GetBalance(sv, receiverID, "FB").Balance,
		GetBalance(sv, DkgComplaintBondPoolAccount, "FB").Balance,
		GetBalance(sv, dealerID, "FB").MinerLockedBalance,
		GetBalance(sv, DkgSlashPoolAccount, "FB").Balance,
	} {
		value, _ := new(big.Int).SetString(raw, 10)
		totalAfter.Add(totalAfter, value)
	}
	wantTotal, _ := new(big.Int).SetString("600000000", 10)
	if totalAfter.Cmp(wantTotal) != 0 {
		t.Fatalf("invalid reveal changed total FB: got=%s want=%s", totalAfter, wantTotal)
	}

	deferred := mustLoadTransition(t, sv, transitionKey)
	if !isInCommittee(dealerID, deferred.NewCommitteeMembers) || deferred.DkgSessionId != sessionID {
		t.Fatalf("reveal changed committee before deadline: %+v", deferred)
	}
	(&Executor{}).processFrostDkgTimeouts(&pb.Block{Header: &pb.BlockHeader{Height: disputeDeadline}}, sv)
	closed := mustLoadTransition(t, sv, transitionKey)
	if isInCommittee(dealerID, closed.NewCommitteeMembers) || closed.DkgStatus != DKGStatusNotStarted || closed.DkgAttempt != 2 {
		t.Fatalf("deadline did not start fresh attempt without dealer: %+v", closed)
	}
}
