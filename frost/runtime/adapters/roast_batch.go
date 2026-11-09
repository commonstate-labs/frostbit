package adapters

import (
	"context"
	"fmt"
	"sync"
	"time"

	"dex/pb"
	"google.golang.org/protobuf/proto"
)

                                                                             
                                                                              
                                     
const (
	roastTransportBatchItems  = 64
	roastTransportBatchBytes  = 256 << 10
	roastTransportQueuedItems = 4096
	roastTransportBatchDelay  = time.Millisecond
)

type roastEnvelopeTransport interface {
	SendFrostToAddress(string, *pb.FrostEnvelope) error
}

type roastEnvelopeBatchTransport interface {
	SendFrostBatchToAddress(context.Context, string, []*pb.FrostEnvelope) error
}

type queuedRoastEnvelope struct {
	envelope *pb.FrostEnvelope
	done     chan error
}

type roastTransportBatch struct {
	items []queuedRoastEnvelope
	bytes int
	timer *time.Timer
}

type roastEnvelopeBatcher struct {
	mu        sync.Mutex
	transport roastEnvelopeTransport
	pending   map[string]*roastTransportBatch
	queued    int
}

func newRoastEnvelopeBatcher(transport roastEnvelopeTransport) *roastEnvelopeBatcher {
	return &roastEnvelopeBatcher{transport: transport, pending: make(map[string]*roastTransportBatch)}
}

func (b *roastEnvelopeBatcher) send(peer string, env *pb.FrostEnvelope) error {
	if env == nil || peer == "" {
		return fmt.Errorf("invalid ROAST transport destination or envelope")
	}
	                                                                        
	                                                                            
	if _, ok := b.transport.(roastEnvelopeBatchTransport); !ok {
		return b.transport.SendFrostToAddress(peer, env)
	}
	size := proto.Size(env)
	                                                                         
	encodedSize := size + 1
	for n := size; n >= 128; n >>= 7 {
		encodedSize++
	}
	encodedSize++
	if encodedSize > roastTransportBatchBytes {
		return fmt.Errorf("ROAST envelope exceeds transport batch byte budget")
	}
	item := queuedRoastEnvelope{envelope: proto.Clone(env).(*pb.FrostEnvelope), done: make(chan error, 1)}
	b.mu.Lock()
	if b.queued >= roastTransportQueuedItems {
		b.mu.Unlock()
		return fmt.Errorf("ROAST transport queue is full")
	}
	batch := b.pending[peer]
	var ready []queuedRoastEnvelope
	if batch != nil && (len(batch.items) >= roastTransportBatchItems || batch.bytes+encodedSize > roastTransportBatchBytes) {
		ready = b.takeLocked(peer, batch)
		batch = nil
	}
	if batch == nil {
		batch = &roastTransportBatch{}
		b.pending[peer] = batch
		batch.timer = time.AfterFunc(roastTransportBatchDelay, func() { b.flush(peer, batch) })
	}
	batch.items = append(batch.items, item)
	batch.bytes += encodedSize
	b.queued++
	if len(batch.items) == roastTransportBatchItems {
		current := b.takeLocked(peer, batch)
		ready = append(ready, current...)
	}
	b.mu.Unlock()
	if len(ready) > 0 {
		go b.deliver(peer, ready)
	}
	return <-item.done
}

func (b *roastEnvelopeBatcher) takeLocked(peer string, batch *roastTransportBatch) []queuedRoastEnvelope {
	if b.pending[peer] != batch {
		return nil
	}
	delete(b.pending, peer)
	batch.timer.Stop()
	return batch.items
}

func (b *roastEnvelopeBatcher) flush(peer string, batch *roastTransportBatch) {
	b.mu.Lock()
	items := b.takeLocked(peer, batch)
	b.mu.Unlock()
	if len(items) > 0 {
		b.deliver(peer, items)
	}
}

func (b *roastEnvelopeBatcher) deliver(peer string, items []queuedRoastEnvelope) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	envelopes := make([]*pb.FrostEnvelope, len(items))
	for i := range items {
		envelopes[i] = items[i].envelope
	}
	err := b.transport.(roastEnvelopeBatchTransport).SendFrostBatchToAddress(ctx, peer, envelopes)
	b.mu.Lock()
	b.queued -= len(items)
	b.mu.Unlock()
	for _, item := range items {
		item.done <- err
	}
}
