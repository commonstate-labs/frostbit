package consensus

import (
	"bytes"
	"testing"

	"dex/pb"
)

func exactPayloadTestTx(id string) *pb.AnyTx {
	return &pb.AnyTx{Content: &pb.AnyTx_Transaction{Transaction: &pb.Transaction{
		Base: &pb.BaseMessage{TxId: id},
	}}}
}

func TestExactCandidatePayloadIgnoresUnsealedSameIDCacheEntry(t *testing.T) {
	const blockID = "exact-payload-same-id"
	clearCachedBlockForTest(blockID)
	t.Cleanup(func() { clearCachedBlockForTest(blockID) })

	good := &pb.Block{
		BlockHash:      blockID,
		Header:         &pb.BlockHeader{Height: 1},
		Body:           []*pb.AnyTx{exactPayloadTestTx("good-tx")},
		MinerSignature: []byte("good-signature"),
	}
	bad := &pb.Block{
		BlockHash:      blockID,
		Header:         &pb.BlockHeader{Height: 1},
		Body:           []*pb.AnyTx{exactPayloadTestTx("injected-tx")},
		MinerSignature: []byte("bad-signature"),
	}

	adapter := &ConsensusAdapter{}
	goodCandidate, err := adapter.DBBlockToConsensusWithPayload(good)
	if err != nil {
		t.Fatal(err)
	}
	if len(goodCandidate.CanonicalPayload) == 0 {
		t.Fatal("network candidate did not retain its exact payload")
	}
	if !CacheBlock(bad) {
		t.Fatal("failed to install competing unsealed cache payload")
	}

	roundTrip := adapter.ConsensusBlockToDB(goodCandidate, nil)
	if !bytes.Equal(roundTrip.MinerSignature, good.MinerSignature) {
		t.Fatalf("unsealed cache replaced exact signature: got=%q want=%q", roundTrip.MinerSignature, good.MinerSignature)
	}
	if len(roundTrip.Body) != 1 || roundTrip.Body[0].GetTxId() != "good-tx" {
		t.Fatalf("unsealed cache replaced exact body: %+v", roundTrip.Body)
	}
}

func TestSealedCanonicalPayloadOverridesSameIDRelayPayload(t *testing.T) {
	const blockID = "sealed-payload-same-id"
	clearCachedBlockForTest(blockID)
	t.Cleanup(func() { clearCachedBlockForTest(blockID) })

	canonical := &pb.Block{
		BlockHash:      blockID,
		Header:         &pb.BlockHeader{Height: 1},
		Body:           []*pb.AnyTx{exactPayloadTestTx("canonical-tx")},
		MinerSignature: []byte("canonical-signature"),
	}
	if !SealCachedBlock(canonical) {
		t.Fatal("failed to seal canonical payload")
	}

	relay := &pb.Block{
		BlockHash:      blockID,
		Header:         &pb.BlockHeader{Height: 1},
		Body:           []*pb.AnyTx{exactPayloadTestTx("relay-injected-tx")},
		MinerSignature: []byte("relay-signature"),
	}
	relayCandidate, err := (&ConsensusAdapter{}).DBBlockToConsensusWithPayload(relay)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip := (&ConsensusAdapter{}).ConsensusBlockToDB(relayCandidate, nil)
	if !bytes.Equal(roundTrip.MinerSignature, canonical.MinerSignature) {
		t.Fatalf("relay signature replaced sealed signature: got=%q want=%q", roundTrip.MinerSignature, canonical.MinerSignature)
	}
	if len(roundTrip.Body) != 1 || roundTrip.Body[0].GetTxId() != "canonical-tx" {
		t.Fatalf("relay body replaced sealed body: %+v", roundTrip.Body)
	}
}

func TestDBBlockToConsensusOnlyAttachesPayloadAtAdmissionBoundary(t *testing.T) {
	block := &pb.Block{BlockHash: "payload-boundary", Header: &pb.BlockHeader{Height: 1}}
	adapter := &ConsensusAdapter{}
	ordinary, err := adapter.DBBlockToConsensus(block)
	if err != nil {
		t.Fatal(err)
	}
	if len(ordinary.CanonicalPayload) != 0 {
		t.Fatal("ordinary DB conversion retained a full payload copy")
	}
	admission, err := adapter.DBBlockToConsensusWithPayload(block)
	if err != nil {
		t.Fatal(err)
	}
	if len(admission.CanonicalPayload) == 0 {
		t.Fatal("admission conversion did not retain exact payload")
	}
}
