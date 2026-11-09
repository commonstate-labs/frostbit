package vm

import (
	"reflect"
	"testing"

	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

type frostDkgReplayHandler interface {
	DryRun(*pb.AnyTx, StateView) ([]WriteOp, *Receipt, error)
}

type frostDkgReplayCase struct {
	name      string
	handler   frostDkgReplayHandler
	tx        *pb.AnyTx
	seedState func(*MockStateView)
	wantError string
}

func TestFrostDkgHandlersRejectStaleSessionReplay(t *testing.T) {
	const (
		chain          = "btc"
		vaultID        = uint32(4)
		currentEpoch   = uint64(8)
		currentSession = "btc_4_8_2"
		staleSession   = "btc_4_8_1"
	)

	for _, tc := range frostDkgReplayCases(t, chain, vaultID, currentEpoch, staleSession, "session_id mismatch") {
		t.Run(tc.name, func(t *testing.T) {
			sv := seedFrostDkgReplayTransition(t, chain, vaultID, currentEpoch, currentSession)
			if tc.seedState != nil {
				tc.seedState(sv)
			}
			before := cloneMockStateData(sv.data)

			ops, receipt, _ := tc.handler.DryRun(tc.tx, sv)
			assertFrostDkgReplayRejected(t, ops, receipt, tc.wantError)
			if !reflect.DeepEqual(sv.data, before) {
				t.Fatal("stale session replay changed StateView")
			}
		})
	}
}

func TestFrostDkgHandlersRejectStaleEpochReplay(t *testing.T) {
	const (
		chain          = "btc"
		vaultID        = uint32(5)
		currentEpoch   = uint64(9)
		staleEpoch     = uint64(8)
		currentSession = "btc_5_9_1"
		staleSession   = "btc_5_8_1"
	)

	for _, tc := range frostDkgReplayCases(t, chain, vaultID, staleEpoch, staleSession, "transition not found") {
		t.Run(tc.name, func(t *testing.T) {
			sv := seedFrostDkgReplayTransition(t, chain, vaultID, currentEpoch, currentSession)
			if tc.seedState != nil {
				tc.seedState(sv)
			}
			before := cloneMockStateData(sv.data)

			ops, receipt, _ := tc.handler.DryRun(tc.tx, sv)
			assertFrostDkgReplayRejected(t, ops, receipt, tc.wantError)
			if !reflect.DeepEqual(sv.data, before) {
				t.Fatal("stale epoch replay changed StateView")
			}
		})
	}
}

func frostDkgReplayCases(t *testing.T, chain string, vaultID uint32, epoch uint64, sessionID, wantError string) []frostDkgReplayCase {
	t.Helper()
	const (
		dealer   = "node-a"
		receiver = "node-b"
	)
	base := func(txID, sender string, height uint64) *pb.BaseMessage {
		return &pb.BaseMessage{TxId: txID, FromAddress: sender, ExecutedHeight: height}
	}

	revealSeed := func(sv *MockStateView) {
		complaint := &pb.FrostVaultDkgComplaint{
			Chain:           chain,
			VaultId:         vaultID,
			EpochId:         epoch,
			DealerId:        dealer,
			ReceiverId:      receiver,
			Bond:            MinDkgComplaintBond,
			ComplaintHeight: 21,
			RevealDeadline:  40,
			Status:          ComplaintStatusPending,
		}
		data, err := proto.Marshal(complaint)
		if err != nil {
			t.Fatalf("marshal replay complaint: %v", err)
		}
		sv.Set(keys.KeyFrostVaultDkgComplaint(chain, vaultID, epoch, sessionID, dealer, receiver), data)
	}

	return []frostDkgReplayCase{
		{
			name:    "commit",
			handler: &FrostVaultDkgCommitTxHandler{},
			tx: &pb.AnyTx{Content: &pb.AnyTx_FrostVaultDkgCommitTx{FrostVaultDkgCommitTx: &pb.FrostVaultDkgCommitTx{
				Base: base("tx-replayed-commit", dealer, 5), Chain: chain, VaultId: vaultID, EpochId: epoch,
				SignAlgo: pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340, CommitmentPoints: [][]byte{{0x01}}, AI0: []byte{0x01}, SessionId: sessionID,
			}}},
			wantError: wantError,
		},
		{
			name:    "share",
			handler: &FrostVaultDkgShareTxHandler{},
			tx: &pb.AnyTx{Content: &pb.AnyTx_FrostVaultDkgShareTx{FrostVaultDkgShareTx: &pb.FrostVaultDkgShareTx{
				Base: base("tx-replayed-share", dealer, 15), Chain: chain, VaultId: vaultID, EpochId: epoch,
				DealerId: dealer, ReceiverId: receiver, Ciphertext: []byte{0x01}, SessionId: sessionID,
			}}},
			wantError: wantError,
		},
		{
			name:    "complaint",
			handler: &FrostVaultDkgComplaintTxHandler{},
			tx: &pb.AnyTx{Content: &pb.AnyTx_FrostVaultDkgComplaintTx{FrostVaultDkgComplaintTx: &pb.FrostVaultDkgComplaintTx{
				Base: base("tx-replayed-complaint", receiver, 25), Chain: chain, VaultId: vaultID, EpochId: epoch,
				DealerId: dealer, ReceiverId: receiver, Bond: MinDkgComplaintBond, SessionId: sessionID,
			}}},
			wantError: wantError,
		},
		{
			name:      "reveal",
			handler:   &FrostVaultDkgRevealTxHandler{},
			seedState: revealSeed,
			tx: &pb.AnyTx{Content: &pb.AnyTx_FrostVaultDkgRevealTx{FrostVaultDkgRevealTx: &pb.FrostVaultDkgRevealTx{
				Base: base("tx-replayed-reveal", dealer, 30), Chain: chain, VaultId: vaultID, EpochId: epoch,
				DealerId: dealer, ReceiverId: receiver, Share: []byte{0x01}, EncRand: []byte{0x01}, SessionId: sessionID,
			}}},
			wantError: wantError,
		},
		{
			name:    "validation",
			handler: &FrostVaultDkgValidationSignedTxHandler{},
			tx: &pb.AnyTx{Content: &pb.AnyTx_FrostVaultDkgValidationSignedTx{FrostVaultDkgValidationSignedTx: &pb.FrostVaultDkgValidationSignedTx{
				Base: base("tx-replayed-validation", dealer, 31), Chain: chain, VaultId: vaultID, EpochId: epoch,
				MsgHash: []byte{0x01}, NewGroupPubkey: []byte{0x02}, Signature: []byte{0x03}, SessionId: sessionID,
			}}},
			wantError: wantError,
		},
	}
}

func seedFrostDkgReplayTransition(t *testing.T, chain string, vaultID uint32, epoch uint64, sessionID string) *MockStateView {
	t.Helper()
	sv := NewMockStateView()
	transition := &pb.VaultTransitionState{
		Chain:               chain,
		VaultId:             vaultID,
		EpochId:             epoch,
		SignAlgo:            pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		DkgStatus:           DKGStatusSharing,
		DkgSessionId:        sessionID,
		TriggerHeight:       1,
		DkgCommitDeadline:   10,
		DkgSharingDeadline:  20,
		DkgDisputeDeadline:  30,
		DkgThresholdT:       2,
		NewCommitteeMembers: []string{"node-a", "node-b"},
	}
	data, err := proto.Marshal(transition)
	if err != nil {
		t.Fatalf("marshal transition: %v", err)
	}
	sv.Set(keys.KeyFrostVaultTransition(chain, vaultID, epoch), data)
	return sv
}

func assertFrostDkgReplayRejected(t *testing.T, ops []WriteOp, receipt *Receipt, wantError string) {
	t.Helper()
	if len(ops) != 0 {
		t.Fatalf("replay returned writes: got=%d", len(ops))
	}
	if receipt == nil || receipt.Status != "FAILED" {
		t.Fatalf("expected FAILED receipt, got %+v", receipt)
	}
	if receipt.Error != wantError {
		t.Fatalf("unexpected rejection: got=%q want=%q", receipt.Error, wantError)
	}
}

func cloneMockStateData(src map[string][]byte) map[string][]byte {
	dst := make(map[string][]byte, len(src))
	for key, value := range src {
		dst[key] = append([]byte(nil), value...)
	}
	return dst
}
