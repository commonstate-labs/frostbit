package adapters

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"dex/pb"
	"google.golang.org/protobuf/proto"
)

type batchRoastTransportFixture struct {
	mu      sync.Mutex
	batches [][]*pb.FrostEnvelope
	failure error
	legacy  int
}

func (f *batchRoastTransportFixture) SendFrostToAddress(string, *pb.FrostEnvelope) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.legacy++
	return nil
}
func (f *batchRoastTransportFixture) SendFrostBatchToAddress(_ context.Context, peer string, envs []*pb.FrostEnvelope) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	copyEnvs := make([]*pb.FrostEnvelope, len(envs))
	for i, e := range envs {
		copyEnvs[i] = proto.Clone(e).(*pb.FrostEnvelope)
	}
	f.batches = append(f.batches, copyEnvs)
	return f.failure
}

func TestRoastTransportBatchRetainsIndependentCompleteEnvelopes(t *testing.T) {
	for _, count := range []int{1, 63, 64, 65, 257} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			transport := &batchRoastTransportFixture{}
			batcher := newRoastEnvelopeBatcher(transport)
			var wg sync.WaitGroup
			start := make(chan struct{})
			errs := make(chan error, count)
			for i := 0; i < count; i++ {
				i := i
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					errs <- batcher.send("peer", &pb.FrostEnvelope{From: "operator", To: "peer", JobId: fmt.Sprintf("child-%d", i), Seq: uint64(i + 1), Sig: []byte{byte(i), 31}, Payload: []byte(fmt.Sprint(i))})
				}()
			}
			close(start)
			wg.Wait()
			close(errs)
			for e := range errs {
				if e != nil {
					t.Fatal(e)
				}
			}
			seen := map[string]bool{}
			for _, batch := range transport.batches {
				if len(batch) > roastTransportBatchItems {
					t.Fatal("unbounded transport batch")
				}
				wire := &pb.FrostEnvelopeBatch{}
				for _, e := range batch {
					if e.To != "peer" || e.Seq == 0 || len(e.Sig) != 2 || seen[e.JobId] {
						t.Fatal("envelope identity corrupted or duplicated")
					}
					seen[e.JobId] = true
					raw, _ := proto.Marshal(e)
					wire.Envelopes = append(wire.Envelopes, raw)
				}
				if proto.Size(wire) > roastTransportBatchBytes {
					t.Fatal("unbounded wrapper bytes")
				}
			}
			if len(seen) != count || transport.legacy != 0 {
				t.Fatalf("lost envelope: %d/%d", len(seen), count)
			}
			if batcher.queued != 0 || len(batcher.pending) != 0 {
				t.Fatal("batch resources leaked")
			}
		})
	}
}

func TestRoastTransportBatchPropagatesFailureWithoutLegacyResend(t *testing.T) {
	transport := &batchRoastTransportFixture{failure: errors.New("peer rejected")}
	batcher := newRoastEnvelopeBatcher(transport)
	if e := batcher.send("peer", &pb.FrostEnvelope{To: "peer", JobId: "child"}); !errors.Is(e, transport.failure) {
		t.Fatalf("failure hidden: %v", e)
	}
	if transport.legacy != 0 {
		t.Fatal("batch error retried as legacy transport")
	}
}

func TestRoastTransportBatchBoundsWrapperBytesAndPendingQueue(t *testing.T) {
	transport := &batchRoastTransportFixture{}
	batcher := newRoastEnvelopeBatcher(transport)
	if e := batcher.send("peer", &pb.FrostEnvelope{Payload: make([]byte, roastTransportBatchBytes)}); e == nil {
		t.Fatal("oversize wrapper accepted")
	}
	batcher.queued = roastTransportQueuedItems
	if e := batcher.send("peer", &pb.FrostEnvelope{}); e == nil {
		t.Fatal("queue capacity exceeded")
	}
}
