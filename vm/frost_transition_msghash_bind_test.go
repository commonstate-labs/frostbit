package vm

import (
	"fmt"
	"math/big"
	"reflect"
	"testing"

	"dex/frost/core/curve"
	"dex/keys"
	"dex/pb"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"google.golang.org/protobuf/proto"
)

                                                                          
                                                                              
                                                                             

func seedTransitionSignedState(t *testing.T, sv *MockStateView, chain string, oldVaultID, newVaultID uint32, epochID uint64, validationHash []byte) {
	t.Helper()
	                    
	grp := curve.NewSecp256k1Group()
	oldPoint := grp.ScalarBaseMult(big.NewInt(2))
	oldGroupPubkey := oldPoint.X.FillBytes(make([]byte, 32))
	oldVault := &pb.FrostVaultState{
		VaultId:     oldVaultID,
		Chain:       chain,
		KeyEpoch:    epochID - 1,
		GroupPubkey: oldGroupPubkey,
		SignAlgo:    pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		Status:      VaultStatusActive,
	}
	oldData, _ := proto.Marshal(oldVault)
	sv.Set(keys.KeyFrostVaultState(chain, oldVaultID), oldData)

	                       
	newGroupPubkey := grp.SerializePoint(grp.ScalarBaseMult(big.NewInt(3)))
	newVault := &pb.FrostVaultState{
		VaultId:     newVaultID,
		Chain:       chain,
		KeyEpoch:    epochID,
		GroupPubkey: newGroupPubkey,
		SignAlgo:    pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		Status:      VaultStatusKeyReady,
	}
	if oldVaultID != newVaultID {
		newData, _ := proto.Marshal(newVault)
		sv.Set(keys.KeyFrostVaultState(chain, newVaultID), newData)
	}

	                                                  
	transition := &pb.VaultTransitionState{
		Chain:             chain,
		VaultId:           newVaultID,
		EpochId:           epochID,
		SignAlgo:          pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		DkgStatus:         DKGStatusKeyReady,
		NewGroupPubkey:    newGroupPubkey,
		ValidationMsgHash: validationHash,
	}
	tData, _ := proto.Marshal(transition)
	sv.Set(keys.KeyFrostVaultTransition(chain, newVaultID, epochID), tData)
}

func signTransitionHashForTest(t *testing.T, msgHash []byte) []byte {
	t.Helper()
	privBytes := big.NewInt(2).FillBytes(make([]byte, 32))
	priv, _ := btcec.PrivKeyFromBytes(privBytes)
	sig, err := schnorr.Sign(priv, msgHash)
	if err != nil {
		t.Fatalf("sign transition hash: %v", err)
	}
	return sig.Serialize()
}

func TestTransitionSigned_T1_RejectsMsgHashMismatch(t *testing.T) {
	sv := NewMockStateView()
	chain, oldVaultID, newVaultID, epochID := "eth", uint32(1), uint32(1), uint64(2)
	expected := make([]byte, 32)
	for i := range expected {
		expected[i] = byte(i + 1)
	}
	seedTransitionSignedState(t, sv, chain, oldVaultID, newVaultID, epochID, expected)

	                                                                          
	                                                                              
	                                    
	wrongHash := make([]byte, 32)
	for i := range wrongHash {
		wrongHash[i] = 0xff
	}

	tx := &pb.AnyTx{
		Content: &pb.AnyTx_FrostVaultTransitionSignedTx{
			FrostVaultTransitionSignedTx: &pb.FrostVaultTransitionSignedTx{
				Base:       &pb.BaseMessage{TxId: "tx_t1_mismatch", FromAddress: "anyone", ExecutedHeight: 100},
				Chain:      chain,
				OldVaultId: oldVaultID,
				NewVaultId: newVaultID,
				EpochId:    epochID,
				MsgHash:    wrongHash,
				Signature:  []byte{0x01},                                       
			},
		},
	}

	h := &FrostVaultTransitionSignedTxHandler{}
	_, receipt, err := h.DryRun(tx, sv)
	if err == nil {
		t.Fatal("transition with mismatched msg_hash must be rejected")
	}
	if receipt == nil || receipt.Status != "FAILED" {
		t.Fatalf("expected FAILED receipt, got %+v", receipt)
	}
	if receipt.Error != "missing migration identity" {
		t.Fatalf("unexpected error: %s", receipt.Error)
	}
}

func TestTransitionSigned_T1_RejectsEmptyValidationMsgHashInState(t *testing.T) {
	sv := NewMockStateView()
	chain, oldVaultID, newVaultID, epochID := "eth", uint32(1), uint32(1), uint64(2)
	                                      
	seedTransitionSignedState(t, sv, chain, oldVaultID, newVaultID, epochID, nil)

	anyHash := make([]byte, 32)
	for i := range anyHash {
		anyHash[i] = 0x42
	}
	tx := &pb.AnyTx{
		Content: &pb.AnyTx_FrostVaultTransitionSignedTx{
			FrostVaultTransitionSignedTx: &pb.FrostVaultTransitionSignedTx{
				Base:       &pb.BaseMessage{TxId: "tx_t1_empty", FromAddress: "anyone", ExecutedHeight: 100},
				Chain:      chain,
				OldVaultId: oldVaultID,
				NewVaultId: newVaultID,
				EpochId:    epochID,
				MsgHash:    anyHash,
				Signature:  []byte{0x01},
			},
		},
	}

	h := &FrostVaultTransitionSignedTxHandler{}
	_, receipt, err := h.DryRun(tx, sv)
	if err == nil {
		t.Fatal("transition when state lacks ValidationMsgHash must be rejected")
	}
	if receipt == nil || receipt.Status != "FAILED" {
		t.Fatalf("expected FAILED receipt, got %+v", receipt)
	}
	if receipt.Error != "missing migration identity" {
		t.Fatalf("unexpected error: %s", receipt.Error)
	}
}

func TestTransitionSignedRejectsPendingRechargeOnOldVault(t *testing.T) {
	for _, tc := range []struct {
		name       string
		oldVaultID uint32
		newVaultID uint32
	}{
		{name: "same vault key rotation", oldVaultID: 1, newVaultID: 1},
		{name: "cross vault retirement", oldVaultID: 1, newVaultID: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sv := NewMockStateView()
			const (
				chain   = "BSC"
				epochID = uint64(2)
			)
			validationHash := make([]byte, 32)
			seedTransitionSignedState(t, sv, chain, tc.oldVaultID, tc.newVaultID, epochID, validationHash)

			seedRechargeRequest := func(req *pb.RechargeRequest) {
				t.Helper()
				data, err := proto.Marshal(req)
				if err != nil {
					t.Fatalf("marshal recharge request: %v", err)
				}
				sv.Set(keys.KeyRechargeRequest(req.RequestId), data)
			}

			                                                                      
			                                                                         
			seedRechargeRequest(&pb.RechargeRequest{
				RequestId: "00-finalized", NativeChain: "bnb", VaultId: tc.oldVaultID,
				Status: pb.RechargeRequestStatus_RECHARGE_FINALIZED,
			})
			seedRechargeRequest(&pb.RechargeRequest{
				RequestId: "01-other-vault", NativeChain: "bnb", VaultId: tc.oldVaultID + 10,
				Status: pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD,
			})
			seedRechargeRequest(&pb.RechargeRequest{
				RequestId: "z-pending", NativeChain: "bnb", VaultId: tc.oldVaultID,
				Status: pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD,
			})
			seedRechargeRequest(&pb.RechargeRequest{
				RequestId: "a-pending", NativeChain: " bnb ", VaultId: tc.oldVaultID,
				Status: pb.RechargeRequestStatus_RECHARGE_VOTING,
			})

			tx := &pb.AnyTx{
				Content: &pb.AnyTx_FrostVaultTransitionSignedTx{
					FrostVaultTransitionSignedTx: &pb.FrostVaultTransitionSignedTx{
						Base:       &pb.BaseMessage{TxId: "tx_pending_recharge", ExecutedHeight: 100},
						Chain:      chain,
						OldVaultId: tc.oldVaultID,
						NewVaultId: tc.newVaultID,
						EpochId:    epochID,
						MsgHash:    validationHash,
						Signature:  signTransitionHashForTest(t, validationHash),
					},
				},
			}

			_, receipt, err := (&FrostVaultTransitionSignedTxHandler{}).DryRun(tx, sv)
			if err == nil {
				t.Fatal("transition with a pending recharge on the old vault must be rejected")
			}
			want := "missing migration identity"
			if tc.oldVaultID != tc.newVaultID {
				want = "cross-vault transition is unsupported"
			}
			if receipt == nil || receipt.Status != "FAILED" || receipt.Error != want {
				t.Fatalf("unexpected receipt: %+v", receipt)
			}
			if err.Error() != want {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestTransitionSignedRejectsBTCCrossVaultTransition(t *testing.T) {
	sv := NewMockStateView()
	const (
		chain    = "btc"
		oldVault = uint32(1)
		newVault = uint32(2)
		epochID  = uint64(2)
	)
	validationHash := make([]byte, 32)
	validationHash[0] = 1
	seedTransitionSignedState(t, sv, chain, oldVault, newVault, epochID, validationHash)
	tx := &pb.AnyTx{Content: &pb.AnyTx_FrostVaultTransitionSignedTx{
		FrostVaultTransitionSignedTx: &pb.FrostVaultTransitionSignedTx{
			Base:       &pb.BaseMessage{TxId: "tx_btc_cross_vault", ExecutedHeight: 100},
			Chain:      chain,
			OldVaultId: oldVault,
			NewVaultId: newVault,
			EpochId:    epochID,
			MsgHash:    validationHash,
			Signature:  signTransitionHashForTest(t, validationHash),
		},
	}}

	_, receipt, err := (&FrostVaultTransitionSignedTxHandler{}).DryRun(tx, sv)
	if err == nil || receipt == nil || receipt.Error != "cross-vault transition is unsupported" {
		t.Fatalf("unsafe BTC cross-vault transition accepted: receipt=%+v err=%v", receipt, err)
	}
}

func TestTransitionSignedRejectsBTCKeyRotationWithoutCanonicalSweep(t *testing.T) {
	sv := NewMockStateView()
	const (
		chain   = "btc"
		vaultID = uint32(1)
		epochID = uint64(2)
	)
	validationHash := make([]byte, 32)
	validationHash[0] = 2
	seedTransitionSignedState(t, sv, chain, vaultID, vaultID, epochID, validationHash)
	tx := &pb.AnyTx{Content: &pb.AnyTx_FrostVaultTransitionSignedTx{
		FrostVaultTransitionSignedTx: &pb.FrostVaultTransitionSignedTx{
			Base:       &pb.BaseMessage{TxId: "tx_btc_key_rotation", ExecutedHeight: 100},
			Chain:      chain,
			OldVaultId: vaultID,
			NewVaultId: vaultID,
			EpochId:    epochID,
			MsgHash:    validationHash,
			Signature:  signTransitionHashForTest(t, validationHash),
		},
	}}

	_, receipt, err := (&FrostVaultTransitionSignedTxHandler{}).DryRun(tx, sv)
	const want = "missing migration identity"
	if err == nil || receipt == nil || receipt.Error != want {
		t.Fatalf("unsafe BTC key rotation accepted: receipt=%+v err=%v", receipt, err)
	}
	vaultRaw, exists, getErr := sv.Get(keys.KeyFrostVaultState(chain, vaultID))
	if getErr != nil || !exists {
		t.Fatalf("load BTC vault after rejection: exists=%v err=%v", exists, getErr)
	}
	var vault pb.FrostVaultState
	if err := proto.Unmarshal(vaultRaw, &vault); err != nil {
		t.Fatal(err)
	}
	if vault.KeyEpoch != epochID-1 || vault.Status != VaultStatusActive {
		t.Fatalf("rejected BTC rotation changed vault: epoch=%d status=%s", vault.KeyEpoch, vault.Status)
	}
}

func TestTransitionSignedRejectsCrossVaultEvenWithTerminalRecharges(t *testing.T) {
	sv := NewMockStateView()
	const (
		chain    = "eth"
		oldVault = uint32(1)
		newVault = uint32(2)
		epochID  = uint64(2)
	)
	validationHash := make([]byte, 32)
	seedTransitionSignedState(t, sv, chain, oldVault, newVault, epochID, validationHash)

	for i, status := range []pb.RechargeRequestStatus{
		pb.RechargeRequestStatus_RECHARGE_FINALIZED,
		pb.RechargeRequestStatus_RECHARGE_REJECTED,
	} {
		req := &pb.RechargeRequest{
			RequestId: fmt.Sprintf("terminal-%d", i), NativeChain: "ETH", VaultId: oldVault, Status: status,
		}
		data, err := proto.Marshal(req)
		if err != nil {
			t.Fatalf("marshal recharge request: %v", err)
		}
		sv.Set(keys.KeyRechargeRequest(req.RequestId), data)
	}

	tx := &pb.AnyTx{
		Content: &pb.AnyTx_FrostVaultTransitionSignedTx{
			FrostVaultTransitionSignedTx: &pb.FrostVaultTransitionSignedTx{
				Base:       &pb.BaseMessage{TxId: "tx_terminal_recharges", ExecutedHeight: 100},
				Chain:      chain,
				OldVaultId: oldVault,
				NewVaultId: newVault,
				EpochId:    epochID,
				MsgHash:    validationHash,
				Signature:  signTransitionHashForTest(t, validationHash),
			},
		},
	}

	_, receipt, err := (&FrostVaultTransitionSignedTxHandler{}).DryRun(tx, sv)
	if err == nil || receipt == nil || receipt.Error != "cross-vault transition is unsupported" {
		t.Fatalf("cross-vault transition was not rejected: receipt=%+v err=%v", receipt, err)
	}
}

func TestTransitionSignedDoesNotMergeCrossVaultBalances(t *testing.T) {
	sv := NewMockStateView()
	const (
		chain    = "eth"
		asset    = "USDT"
		oldVault = uint32(1)
		newVault = uint32(2)
		epochID  = uint64(2)
	)
	validationHash := make([]byte, 32)
	validationHash[0] = 3
	seedTransitionSignedState(t, sv, chain, oldVault, newVault, epochID, validationHash)
	oldBalanceKey := keys.KeyFrostVaultAvailableBalance(chain, asset, oldVault)
	newBalanceKey := keys.KeyFrostVaultAvailableBalance(chain, asset, newVault)
	sv.Set(oldBalanceKey, []byte("10"))
	sv.Set(newBalanceKey, []byte("5"))

	tx := &pb.AnyTx{Content: &pb.AnyTx_FrostVaultTransitionSignedTx{
		FrostVaultTransitionSignedTx: &pb.FrostVaultTransitionSignedTx{
			Base:       &pb.BaseMessage{TxId: "tx_merge_decimal_available", ExecutedHeight: 100},
			Chain:      chain,
			OldVaultId: oldVault,
			NewVaultId: newVault,
			EpochId:    epochID,
			MsgHash:    validationHash,
			Signature:  signTransitionHashForTest(t, validationHash),
		},
	}}

	_, receipt, err := (&FrostVaultTransitionSignedTxHandler{}).DryRun(tx, sv)
	if err == nil || receipt == nil || receipt.Error != "cross-vault transition is unsupported" {
		t.Fatalf("cross-vault transition was not rejected: receipt=%+v err=%v", receipt, err)
	}
	if oldBalance, exists, getErr := sv.Get(oldBalanceKey); getErr != nil || !exists || string(oldBalance) != "10" {
		t.Fatalf("old vault balance changed: exists=%v value=%q err=%v", exists, string(oldBalance), getErr)
	}
	if newBalance, exists, getErr := sv.Get(newBalanceKey); getErr != nil || !exists || string(newBalance) != "5" {
		t.Fatalf("new vault balance changed: exists=%v value=%q err=%v", exists, string(newBalance), getErr)
	}
}

func TestTransitionSignedRejectsInvalidAvailableBalanceMergeWithoutWrites(t *testing.T) {
	tests := []struct {
		name       string
		oldBalance string
		newBalance string
		wantError  string
	}{
		{name: "malformed old balance", oldBalance: "1e", newBalance: "5", wantError: "cross-vault transition is unsupported"},
		{name: "malformed new balance", oldBalance: "10", newBalance: "1e", wantError: "cross-vault transition is unsupported"},
		{name: "uint256 overflow", oldBalance: MaxUint256.String(), newBalance: "1", wantError: "cross-vault transition is unsupported"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sv := NewMockStateView()
			const (
				chain    = "eth"
				asset    = "USDT"
				oldVault = uint32(1)
				newVault = uint32(2)
				epochID  = uint64(2)
			)
			validationHash := make([]byte, 32)
			validationHash[0] = 4
			seedTransitionSignedState(t, sv, chain, oldVault, newVault, epochID, validationHash)
			sv.Set(keys.KeyFrostVaultAvailableBalance(chain, asset, oldVault), []byte(tt.oldBalance))
			sv.Set(keys.KeyFrostVaultAvailableBalance(chain, asset, newVault), []byte(tt.newBalance))
			before := cloneMockStateData(sv.data)

			tx := &pb.AnyTx{Content: &pb.AnyTx_FrostVaultTransitionSignedTx{
				FrostVaultTransitionSignedTx: &pb.FrostVaultTransitionSignedTx{
					Base:       &pb.BaseMessage{TxId: "tx_reject_invalid_available_" + tt.name, ExecutedHeight: 100},
					Chain:      chain,
					OldVaultId: oldVault,
					NewVaultId: newVault,
					EpochId:    epochID,
					MsgHash:    validationHash,
					Signature:  signTransitionHashForTest(t, validationHash),
				},
			}}

			ops, receipt, err := (&FrostVaultTransitionSignedTxHandler{}).DryRun(tx, sv)
			if err == nil || receipt == nil || receipt.Status != "FAILED" || receipt.Error != tt.wantError || len(ops) != 0 {
				t.Fatalf("invalid balance merge was not rejected cleanly: ops=%d receipt=%+v err=%v", len(ops), receipt, err)
			}
			if !reflect.DeepEqual(sv.data, before) {
				t.Fatal("failed balance merge changed StateView")
			}
		})
	}
}
