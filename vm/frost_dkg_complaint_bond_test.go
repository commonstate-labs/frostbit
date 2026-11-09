package vm

import (
	"fmt"
	"math/big"
	"testing"

	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

                                                                                   
                                                                                     
                                                                                          

func seedComplaintState(t *testing.T, sv *MockStateView, chain string, vaultID uint32, epochID uint64, sessionID, dealerID, receiverID string, receiverFB string) {
	t.Helper()
	transition := &pb.VaultTransitionState{
		Chain:               chain,
		VaultId:             vaultID,
		EpochId:             epochID,
		SignAlgo:            pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		DkgStatus:           DKGStatusSharing,
		DkgSessionId:        sessionID,
		DkgSharingDeadline:  10,
		DkgDisputeDeadline:  100,
		DkgThresholdT:       1,
		NewCommitteeMembers: []string{dealerID, receiverID},
	}
	tData, err := proto.Marshal(transition)
	if err != nil {
		t.Fatalf("marshal transition: %v", err)
	}
	sv.Set(keys.KeyFrostVaultTransition(chain, vaultID, epochID), tData)

	                                                    
	share := &pb.FrostVaultDkgShare{
		Chain:      chain,
		VaultId:    vaultID,
		EpochId:    epochID,
		DealerId:   dealerID,
		ReceiverId: receiverID,
		Ciphertext: []byte{0x01, 0x02, 0x03},
	}
	sData, _ := proto.Marshal(share)
	sv.Set(keys.KeyFrostVaultDkgShare(chain, vaultID, epochID, sessionID, dealerID, receiverID), sData)

	                                       
	bal := &pb.TokenBalanceRecord{Balance: &pb.TokenBalance{Balance: receiverFB}}
	balData, _ := proto.Marshal(bal)
	sv.Set(keys.KeyBalance(receiverID, DkgComplaintBondToken), balData)
}

func buildComplaintTx(sender, chain string, vaultID uint32, epochID uint64, sessionID, dealerID, receiverID, bond string, height uint64) *pb.AnyTx {
	return &pb.AnyTx{
		Content: &pb.AnyTx_FrostVaultDkgComplaintTx{
			FrostVaultDkgComplaintTx: &pb.FrostVaultDkgComplaintTx{
				Base: &pb.BaseMessage{
					TxId:           fmt.Sprintf("tx_complaint_%s_%d", sender, height),
					FromAddress:    sender,
					ExecutedHeight: height,
				},
				Chain:      chain,
				VaultId:    vaultID,
				EpochId:    epochID,
				SessionId:  sessionID,
				DealerId:   dealerID,
				ReceiverId: receiverID,
				Bond:       bond,
			},
		},
	}
}

func TestDkgComplaint_D2_RejectsZeroBond(t *testing.T) {
	sv := NewMockStateView()
	chain, vaultID, epochID := "eth", uint32(0), uint64(1)
	sessionID := "sess_d2_zero"
	dealer, receiver := "miner_dealer", "miner_receiver"
	seedComplaintState(t, sv, chain, vaultID, epochID, sessionID, dealer, receiver, "10000000000")

	tx := buildComplaintTx(receiver, chain, vaultID, epochID, sessionID, dealer, receiver, "0", 50)
	h := &FrostVaultDkgComplaintTxHandler{}
	_, receipt, err := h.DryRun(tx, sv)
	if err == nil {
		t.Fatal("complaint with Bond=0 must be rejected")
	}
	if receipt == nil || receipt.Status != "FAILED" {
		t.Fatalf("expected FAILED receipt, got %+v", receipt)
	}
}

func TestDkgComplaint_D2_RejectsBelowMinimumBond(t *testing.T) {
	sv := NewMockStateView()
	chain, vaultID, epochID := "eth", uint32(0), uint64(1)
	sessionID := "sess_d2_min"
	dealer, receiver := "miner_dealer", "miner_receiver"
	seedComplaintState(t, sv, chain, vaultID, epochID, sessionID, dealer, receiver, "10000000000")

	tx := buildComplaintTx(receiver, chain, vaultID, epochID, sessionID, dealer, receiver, "1", 50)
	h := &FrostVaultDkgComplaintTxHandler{}
	_, receipt, err := h.DryRun(tx, sv)
	if err == nil {
		t.Fatal("complaint with Bond < MinDkgComplaintBond must be rejected")
	}
	if receipt == nil || receipt.Status != "FAILED" {
		t.Fatalf("expected FAILED receipt, got %+v", receipt)
	}
}

func TestDkgComplaint_D2_RejectsInsufficientBalance(t *testing.T) {
	sv := NewMockStateView()
	chain, vaultID, epochID := "eth", uint32(0), uint64(1)
	sessionID := "sess_d2_insuf"
	dealer, receiver := "miner_dealer", "miner_receiver"
	                                                 
	seedComplaintState(t, sv, chain, vaultID, epochID, sessionID, dealer, receiver, "100000000")

	tx := buildComplaintTx(receiver, chain, vaultID, epochID, sessionID, dealer, receiver, "1000000000", 50)
	h := &FrostVaultDkgComplaintTxHandler{}
	_, receipt, err := h.DryRun(tx, sv)
	if err == nil {
		t.Fatal("complaint with insufficient balance must be rejected")
	}
	if receipt == nil || receipt.Status != "FAILED" {
		t.Fatalf("expected FAILED receipt, got %+v", receipt)
	}
}

func TestDkgComplaint_D2_LocksBondOnSuccess(t *testing.T) {
	sv := NewMockStateView()
	chain, vaultID, epochID := "eth", uint32(0), uint64(1)
	sessionID := "sess_d2_ok"
	dealer, receiver := "miner_dealer", "miner_receiver"
	const initialBalance = "10000000000"          
	const bondAmount = "100000000"                                    
	seedComplaintState(t, sv, chain, vaultID, epochID, sessionID, dealer, receiver, initialBalance)

	tx := buildComplaintTx(receiver, chain, vaultID, epochID, sessionID, dealer, receiver, bondAmount, 50)
	h := &FrostVaultDkgComplaintTxHandler{}
	_, receipt, err := h.DryRun(tx, sv)
	if err != nil {
		t.Fatalf("complaint with valid bond must succeed, got err: %v", err)
	}
	if receipt == nil || receipt.Status != "SUCCEED" {
		t.Fatalf("expected SUCCEED receipt, got %+v", receipt)
	}

	                                               
	balKey := keys.KeyBalance(receiver, DkgComplaintBondToken)
	balData, exists, _ := sv.Get(balKey)
	if !exists {
		t.Fatal("receiver balance record missing")
	}
	var rec pb.TokenBalanceRecord
	if err := proto.Unmarshal(balData, &rec); err != nil {
		t.Fatalf("unmarshal balance: %v", err)
	}
	got, _ := new(big.Int).SetString(rec.Balance.Balance, 10)
	want := new(big.Int)
	initial, _ := new(big.Int).SetString(initialBalance, 10)
	bond, _ := new(big.Int).SetString(bondAmount, 10)
	want.Sub(initial, bond)
	if got.Cmp(want) != 0 {
		t.Fatalf("receiver balance after bond lock: got=%s want=%s", got.String(), want.String())
	}
	pool := GetBalance(sv, DkgComplaintBondPoolAccount, DkgComplaintBondToken)
	if pool.Balance != bondAmount {
		t.Fatalf("bond pool balance: got=%s want=%s", pool.Balance, bondAmount)
	}
	conserved := new(big.Int).Add(got, bond)
	if conserved.Cmp(initial) != 0 {
		t.Fatalf("bond lock changed total FB: got=%s want=%s", conserved, initial)
	}

	                                                    
	complaintKey := keys.KeyFrostVaultDkgComplaint(chain, vaultID, epochID, sessionID, dealer, receiver)
	cData, cExists, _ := sv.Get(complaintKey)
	if !cExists {
		t.Fatal("complaint record missing")
	}
	var complaint pb.FrostVaultDkgComplaint
	if err := proto.Unmarshal(cData, &complaint); err != nil {
		t.Fatalf("unmarshal complaint: %v", err)
	}
	if complaint.Bond != bondAmount {
		t.Fatalf("complaint bond: got=%s want=%s", complaint.Bond, bondAmount)
	}
	if complaint.RevealDeadline != 100 {
		t.Fatalf("reveal deadline must equal global dispute deadline, got=%d", complaint.RevealDeadline)
	}
}

func TestDkgComplaint_RequiresTwoRevealBlocks(t *testing.T) {
	for _, tc := range []struct {
		name   string
		height uint64
		wantOK bool
	}{
		{name: "last allowed", height: 98, wantOK: true},
		{name: "one block left", height: 99, wantOK: false},
		{name: "deadline reserved for hook", height: 100, wantOK: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sv := NewMockStateView()
			chain, vaultID, epochID := "eth", uint32(0), uint64(1)
			sessionID := "sess_window_" + tc.name
			dealer, receiver := "miner_dealer", "miner_receiver"
			seedComplaintState(t, sv, chain, vaultID, epochID, sessionID, dealer, receiver, "1000000000")

			tx := buildComplaintTx(receiver, chain, vaultID, epochID, sessionID, dealer, receiver, MinDkgComplaintBond, tc.height)
			_, receipt, err := (&FrostVaultDkgComplaintTxHandler{}).DryRun(tx, sv)
			if tc.wantOK {
				if err != nil || receipt == nil || receipt.Status != "SUCCEED" {
					t.Fatalf("expected complaint success, receipt=%+v err=%v", receipt, err)
				}
				key := keys.KeyFrostVaultDkgComplaint(chain, vaultID, epochID, sessionID, dealer, receiver)
				raw, exists, _ := sv.Get(key)
				if !exists {
					t.Fatal("complaint missing")
				}
				var complaint pb.FrostVaultDkgComplaint
				if err := proto.Unmarshal(raw, &complaint); err != nil || complaint.RevealDeadline != 100 {
					t.Fatalf("unexpected reveal deadline=%d err=%v", complaint.RevealDeadline, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("window rejection is a failed receipt, not handler error: %v", err)
			}
			if receipt == nil || receipt.Status != "FAILED" {
				t.Fatalf("expected failed receipt, got %+v", receipt)
			}
			if pool := GetBalance(sv, DkgComplaintBondPoolAccount, DkgComplaintBondToken).Balance; pool != "0" {
				t.Fatalf("rejected complaint locked bond: pool=%s", pool)
			}
		})
	}
}
