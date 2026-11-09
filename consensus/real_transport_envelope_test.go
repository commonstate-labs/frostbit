package consensus

import (
	"bytes"
	"runtime"
	"testing"

	"dex/pb"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func TestQueryEnvelopePreservesSerializedFieldsWithoutMutatingBase(t *testing.T) {
	unknown := protowire.AppendTag(nil, 100, protowire.BytesType)
	unknown = protowire.AppendBytes(unknown, []byte("future-field"))
	push := &pb.PushQuery{
		RequestId: 17, Address: "requester", ContainerIsBlock: true,
		Container: []byte("block-payload"), RequestedHeight: 21, BlockId: "block-22",
		Signature: []byte("signature"), Nonce: bytes.Repeat([]byte{2}, 32),
		VrfSeed: []byte("vrf-seed"), SeqId: 7, Responder: "old-peer", ChainId: 99,
	}
	pull := &pb.PullQuery{
		RequestId: 17, Address: "requester", BlockId: "block-22", RequestedHeight: 21,
		VrfSeed: []byte("vrf-seed"), SeqId: 7, Responder: "old-peer", ChainId: 99,
		Signature: []byte("signature"), Nonce: bytes.Repeat([]byte{2}, 32),
	}
	for _, test := range []struct {
		name string
		base proto.Message
		copy func() proto.Message
	}{
		{"push", push, func() proto.Message { return pushQueryEnvelope(push) }},
		{"pull", pull, func() proto.Message { return pullQueryEnvelope(pull) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.base.ProtoReflect().SetUnknown(unknown)
			before, err := proto.Marshal(test.base)
			if err != nil {
				t.Fatal(err)
			}
			copy := test.copy()
			after, err := proto.Marshal(copy)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("per-peer envelope changed serialized query context")
			}
			switch query := copy.(type) {
			case *pb.PushQuery:
				query.Responder, query.ChainId, query.Signature = "new-peer", 100, []byte("new-signature")
				if &query.Container[0] != &push.Container[0] {
					t.Fatal("block payload was copied")
				}
			case *pb.PullQuery:
				query.Responder, query.ChainId, query.Signature = "new-peer", 100, []byte("new-signature")
			}
			unchanged, err := proto.Marshal(test.base)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, unchanged) {
				t.Fatal("per-peer signing mutated the prepared query")
			}
		})
	}
}

func TestPushQueryLocal20EnvelopeAllocationDoesNotCopyBlock(t *testing.T) {
	const peers, payloadSize = 20, 2 << 20
	base := &pb.PushQuery{Container: make([]byte, payloadSize), Nonce: make([]byte, 32)}
	                                                                         
	_ = pushQueryEnvelope(base)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	queries := make([]*pb.PushQuery, peers)
	for i := range queries {
		queries[i] = pushQueryEnvelope(base)
	}
	runtime.ReadMemStats(&after)
	for _, query := range queries {
		if &query.Container[0] != &base.Container[0] {
			t.Fatal("peer envelope duplicated the large block")
		}
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 128<<10 {
		t.Fatalf("20 envelopes copied block data: allocated=%d payload=%d", allocated, payloadSize)
	}
	t.Logf("20 peers share a 2MiB block; envelope allocation=%d bytes", after.TotalAlloc-before.TotalAlloc)
	runtime.KeepAlive(queries)
}
