package consensus

import (
	"errors"
	"testing"

	"dex/pb"
	"dex/types"

	"google.golang.org/protobuf/proto"
)

func TestPushQueryToConsensusMessageRejectsNestedBlockRepeatedBloat(t *testing.T) {
	const blockID = "pushquery-nested-block-repeated-bloat"
	RemoveCachedBlock(blockID)
	t.Cleanup(func() { RemoveCachedBlock(blockID) })

	body := make([]*pb.AnyTx, 10_001)
	for i := range body {
		body[i] = &pb.AnyTx{}
	}
	encoded, err := proto.Marshal(&pb.Block{
		BlockHash: blockID,
		Header:    &pb.BlockHeader{Height: 1},
		Body:      body,
	})
	if err != nil {
		t.Fatal(err)
	}

	query := &pb.PushQuery{ContainerIsBlock: true, Container: encoded}
	if err := pb.Sanitize(query); err != nil {
		t.Fatalf("outer PushQuery must remain within its wire limits: %v", err)
	}

	_, err = (&ConsensusAdapter{}).PushQueryToConsensusMessage(query, types.NodeID("attacker"))
	if !errors.Is(err, pb.ErrDataBloat) {
		t.Fatalf("nested block repeated bloat error = %v, want %v", err, pb.ErrDataBloat)
	}
	if _, exists := GetCachedBlock(blockID); exists {
		t.Fatal("rejected nested block was cached")
	}
}
