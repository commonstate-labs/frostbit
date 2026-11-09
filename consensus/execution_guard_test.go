package consensus

import (
	"dex/keys"
	"dex/pb"
	"dex/txpool"
	"dex/txverify"
	"dex/utils"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
)

func signedExecutionGuardTransfer(t *testing.T, height uint64, proposer string) *pb.AnyTx {
	t.Helper()
	sender := utils.GenerateTestIdentity(7001)
	receiver := utils.GenerateTestIdentity(7002)
	tx := &pb.AnyTx{Content: &pb.AnyTx_Transaction{Transaction: &pb.Transaction{
		Base: &pb.BaseMessage{
			FromAddress:  sender.Address,
			Fee:          "1000",
			ChainId:      pb.GetDefaultChainID(),
			TargetHeight: height,
			OnlyProposer: proposer,
		},
		To:           receiver.Address,
		Amount:       "1",
		TokenAddress: "FB",
	}}}
	if _, err := txverify.SignWithPrivateKey(tx, sender.PrivateKeyHex); err != nil {
		t.Fatalf("sign guarded transfer: %v", err)
	}
	return tx
}

func TestLightweightValidateBlockEnforcesExecutionGuard(t *testing.T) {
	const height = uint64(77)
	proposerIdentity := utils.GenerateTestIdentity(7003)
	proposer := proposerIdentity.Address
	keyManager := utils.NewKeyManager()
	if err := keyManager.InitKey(proposerIdentity.PrivateKeyHex); err != nil {
		t.Fatal(err)
	}
	publicKey := proposalValidationBLSKey(t, keyManager)

	tests := []struct {
		name          string
		height        uint64
		miner         string
		guardProposer string
		missingSig    bool
		wantErrSub    string
	}{
		{name: "matching target height", height: height, miner: keys.KeyNode() + proposer},
		{name: "wrong height", height: height + 1, miner: keys.KeyNode() + proposer, wantErrSub: "target_height"},
		{name: "wrong proposer", height: height, miner: keys.KeyNode() + "fbc1qotherminer", guardProposer: proposer, wantErrSub: "only_proposer"},
		{name: "matching proposer requires block signature", height: height, miner: keys.KeyNode() + proposer, guardProposer: proposer, missingSig: true, wantErrSub: "missing proposer signature"},
	}

	store := &RealBlockStore{}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tx := proto.Clone(signedExecutionGuardTransfer(t, height, tc.guardProposer)).(*pb.AnyTx)
			block := &pb.Block{
				Header: &pb.BlockHeader{
					Height:    tc.height,
					Miner:     tc.miner,
					PublicKey: publicKey,
					TxsHash:   txpool.ComputeTxsHash([]*pb.AnyTx{tx}),
				},
				Body: []*pb.AnyTx{tx},
			}
			converted, err := (&ConsensusAdapter{}).DBBlockToConsensus(block)
			if err != nil {
				t.Fatal(err)
			}
			block.BlockHash = converted.Header.ComputeIDForChain(pb.GetDefaultChainID())
			if !tc.missingSig {
				block.MinerSignature, err = signBlockProposal(keyManager, block.BlockHash)
				if err != nil {
					t.Fatal(err)
				}
			}
			err = store.lightweightValidateBlock(block)
			if tc.wantErrSub == "" {
				if err != nil {
					t.Fatalf("lightweightValidateBlock() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Fatalf("lightweightValidateBlock() error = %v, want substring %q", err, tc.wantErrSub)
			}
		})
	}
}
