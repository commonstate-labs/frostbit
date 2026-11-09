package consensus

import (
	"fmt"
	"strings"
	"testing"

	"dex/keys"
	"dex/pb"
	"dex/txpool"
	"dex/utils"

	"google.golang.org/protobuf/proto"
)

func proposerSignatureTestBlock(t *testing.T, keyManager *utils.KeyManager, legs int) *pb.Block {
	t.Helper()
	proposer := keyManager.GetAddress()
	publicKey := proposalValidationBLSKey(t, keyManager)
	body := make([]*pb.AnyTx, 0, legs)
	for i := 0; i < legs; i++ {
		body = append(body, &pb.AnyTx{Content: &pb.AnyTx_Transaction{Transaction: &pb.Transaction{
			Base: &pb.BaseMessage{
				TxId:         fmt.Sprintf("guarded-leg-%d", i),
				FromAddress:  proposer,
				Nonce:        uint64(i),
				TargetHeight: 9,
				OnlyProposer: proposer,
			},
		}}})
	}
	block := &pb.Block{
		Header: &pb.BlockHeader{
			Height:    9,
			Miner:     keys.KeyNode() + proposer,
			PublicKey: append([]byte(nil), publicKey...),
			TxsHash:   txpool.ComputeTxsHash(body),
		},
		Body: body,
	}
	converted, err := (&ConsensusAdapter{}).DBBlockToConsensus(block)
	if err != nil {
		t.Fatal(err)
	}
	block.BlockHash = converted.Header.ComputeIDForChain(pb.GetDefaultChainID())
	block.MinerSignature, err = signBlockProposal(keyManager, block.BlockHash)
	if err != nil {
		t.Fatal(err)
	}
	return block
}

func TestBlockProposerSignatureBindsCompleteOrderedBody(t *testing.T) {
	keyManager := utils.NewKeyManager()
	if err := keyManager.InitKeyRandom(); err != nil {
		t.Fatal(err)
	}
	block := proposerSignatureTestBlock(t, keyManager, 2)
	if err := validateBlockProposerSignature(block); err != nil {
		t.Fatalf("valid signed block rejected: %v", err)
	}

	tampered := proto.Clone(block).(*pb.Block)
	tampered.Body = tampered.Body[:1]
	tampered.Header.TxsHash = txpool.ComputeTxsHash(tampered.Body)
	converted, err := (&ConsensusAdapter{}).DBBlockToConsensus(tampered)
	if err != nil {
		t.Fatal(err)
	}
	tampered.BlockHash = converted.Header.ComputeIDForChain(pb.GetDefaultChainID())
	if err := ValidateBlockCommitment(tampered); err != nil {
		t.Fatalf("tampered test block must have a valid recomputed commitment: %v", err)
	}
	if err := validateBlockProposerSignature(tampered); err == nil || !strings.Contains(err.Error(), "invalid block proposer signature") {
		t.Fatalf("body replacement reused proposer signature: %v", err)
	}
}

func TestBlockProposerSignatureRejectsMissingOrWrongSigner(t *testing.T) {
	keyManager := utils.NewKeyManager()
	if err := keyManager.InitKeyRandom(); err != nil {
		t.Fatal(err)
	}
	block := proposerSignatureTestBlock(t, keyManager, 1)
	missing := proto.Clone(block).(*pb.Block)
	missing.MinerSignature = nil
	if err := validateBlockProposerSignature(missing); err == nil || !strings.Contains(err.Error(), "missing proposer signature") {
		t.Fatalf("missing signature accepted: %v", err)
	}

	attacker := utils.NewKeyManager()
	if err := attacker.InitKeyRandom(); err != nil {
		t.Fatal(err)
	}
	wrong := proto.Clone(block).(*pb.Block)
	wrong.MinerSignature, _ = signBlockProposal(attacker, wrong.BlockHash)
	if err := validateBlockProposerSignature(wrong); err == nil || !strings.Contains(err.Error(), "invalid block proposer signature") {
		t.Fatalf("wrong signer accepted: %v", err)
	}

	malleable := proto.Clone(block).(*pb.Block)
	malleable.MinerSignature = append(malleable.MinerSignature, 0)
	if err := validateBlockProposerSignature(malleable); err == nil || !strings.Contains(err.Error(), "invalid BLS signature length") {
		t.Fatalf("proposer signature suffix accepted: %v", err)
	}
}

func TestBlockProposerSignatureRoundTripsAndAllNonGenesisBlocksRequireIt(t *testing.T) {
	keyManager := utils.NewKeyManager()
	if err := keyManager.InitKeyRandom(); err != nil {
		t.Fatal(err)
	}
	block := proposerSignatureTestBlock(t, keyManager, 1)
	adapter := &ConsensusAdapter{}
	converted, err := adapter.DBBlockToConsensus(block)
	if err != nil {
		t.Fatal(err)
	}
	if string(converted.MinerSignature) != string(block.MinerSignature) {
		t.Fatal("DBBlockToConsensus dropped proposer signature")
	}
	roundTrip := adapter.ConsensusBlockToDB(converted, block.Body)
	if string(roundTrip.MinerSignature) != string(block.MinerSignature) {
		t.Fatal("ConsensusBlockToDB dropped proposer signature")
	}
	bufferBlock := (&PendingBlockBuffer{}).buildFullPbBlock(converted, block.Body)
	if string(bufferBlock.MinerSignature) != string(block.MinerSignature) {
		t.Fatal("short-transaction block rebuild dropped proposer signature")
	}

	ordinary := proto.Clone(block).(*pb.Block)
	ordinary.Body[0].GetTransaction().Base.OnlyProposer = ""
	ordinary.MinerSignature = nil
	ordinary.Header.TxsHash = txpool.ComputeTxsHash(ordinary.Body)
	converted, err = adapter.DBBlockToConsensus(ordinary)
	if err != nil {
		t.Fatal(err)
	}
	ordinary.BlockHash = converted.Header.ComputeIDForChain(pb.GetDefaultChainID())
	ordinary.MinerSignature, err = signBlockProposal(keyManager, ordinary.BlockHash)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateBlockProposerSignature(ordinary); err != nil {
		t.Fatalf("signed ordinary-transfer block rejected: %v", err)
	}
	reauthored := proto.Clone(ordinary).(*pb.Block)
	reauthored.Body = nil
	reauthored.Header.TxsHash = txpool.ComputeTxsHash(nil)
	converted, err = adapter.DBBlockToConsensus(reauthored)
	if err != nil {
		t.Fatal(err)
	}
	reauthored.BlockHash = converted.Header.ComputeIDForChain(pb.GetDefaultChainID())
	if err := validateBlockProposerSignature(reauthored); err == nil || !strings.Contains(err.Error(), "invalid block proposer signature") {
		t.Fatalf("relay reauthored an ordinary block with the old proposer signature: %v", err)
	}
	ordinary.MinerSignature = nil
	if err := validateBlockProposerSignature(ordinary); err == nil || !strings.Contains(err.Error(), "missing proposer signature") {
		t.Fatalf("unsigned ordinary-transfer block accepted: %v", err)
	}

	protocol := proto.Clone(block).(*pb.Block)
	protocol.Body = []*pb.AnyTx{{Content: &pb.AnyTx_CertSignTx{CertSignTx: &pb.CertSignTx{
		Base: &pb.BaseMessage{OnlyProposer: "ignored-for-protocol"},
	}}}}
	protocol.MinerSignature = nil
	if err := validateBlockProposerSignature(protocol); err == nil || !strings.Contains(err.Error(), "missing proposer signature") {
		t.Fatalf("unsigned protocol block accepted: %v", err)
	}

	empty := proto.Clone(block).(*pb.Block)
	empty.Body = nil
	empty.MinerSignature = nil
	empty.Header.TxsHash = txpool.ComputeTxsHash(nil)
	converted, err = adapter.DBBlockToConsensus(empty)
	if err != nil {
		t.Fatal(err)
	}
	empty.BlockHash = converted.Header.ComputeIDForChain(pb.GetDefaultChainID())
	if err := validateBlockProposerSignature(empty); err == nil || !strings.Contains(err.Error(), "missing proposer signature") {
		t.Fatalf("unsigned empty block accepted: %v", err)
	}

	genesis := &pb.Block{BlockHash: "genesis", Header: &pb.BlockHeader{Height: 0}}
	if err := validateBlockProposerSignature(genesis); err != nil {
		t.Fatalf("unsigned genesis rejected: %v", err)
	}

	publicKey, err := utils.DeserializePublicKey(proposalValidationBLSKey(t, keyManager))
	if err != nil {
		t.Fatal(err)
	}
	if err := utils.BLSVerifySignature(
		publicKey,
		blockProposerSignatureMessage(pb.GetDefaultChainID()+1, block.BlockHash),
		block.MinerSignature,
	); err == nil {
		t.Fatal("proposer signature was reusable across chain IDs")
	}
}
