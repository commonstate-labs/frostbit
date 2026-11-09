package handlers

import (
	"container/list"
	"crypto/sha256"
	"dex/consensus"
	"dex/pb"
	"dex/utils"
	"fmt"
	"sync"
)

const (
	                                                                            
	                                                                            
	                                                                       
	                                                                          
	                                                  
	maxConsensusQueriesPerSender       = 4096
	maxConsensusQueriesPerSenderHeight = 1024
)

type consensusQueryReplayKey struct {
	height uint64
	digest [sha256.Size]byte
}

type consensusQueryReplayEntry struct {
	key        consensusQueryReplayKey
	globalElem *list.Element
	heightElem *list.Element
}

type consensusQueryReplayReservation struct {
	state *consensusQueryReplaySender
	entry *consensusQueryReplayEntry
}

type consensusQueryReplayHeight struct {
	entries map[[sha256.Size]byte]*consensusQueryReplayEntry
	order   *list.List
}

type consensusQueryReplaySender struct {
	total        int
	seenByHeight map[uint64]*consensusQueryReplayHeight
	order        *list.List
}

                                                                         
                                                                            
                                                                              
                                                                          
type consensusQueryReplayGuard struct {
	mu       sync.Mutex
	bySender map[string]*consensusQueryReplaySender
}

func (g *consensusQueryReplayGuard) reserve(sender string, height, localAccepted uint64, digest []byte) bool {
	return g.reserveWithToken(sender, height, localAccepted, digest) != nil
}

func (g *consensusQueryReplayGuard) reserveWithToken(sender string, height, localAccepted uint64, digest []byte) *consensusQueryReplayReservation {
	if sender == "" || len(digest) != sha256.Size {
		return nil
	}
	if !consensusQueryHeightInReplayWindow(height, localAccepted) {
		return nil
	}
	var key [sha256.Size]byte
	copy(key[:], digest)

	g.mu.Lock()
	defer g.mu.Unlock()
	if g.bySender == nil {
		g.bySender = make(map[string]*consensusQueryReplaySender)
	}
	state := g.bySender[sender]
	if state == nil {
		state = &consensusQueryReplaySender{
			seenByHeight: make(map[uint64]*consensusQueryReplayHeight),
			order:        list.New(),
		}
		g.bySender[sender] = state
	}
	for seenHeight, bucket := range state.seenByHeight {
		if !consensusQueryHeightInReplayWindow(seenHeight, localAccepted) {
			for bucket.order.Len() > 0 {
				removeConsensusQueryReplayEntry(state, bucket.order.Front().Value.(*consensusQueryReplayEntry))
			}
		}
	}
	bucket := state.seenByHeight[height]
	if bucket == nil {
		bucket = &consensusQueryReplayHeight{
			entries: make(map[[sha256.Size]byte]*consensusQueryReplayEntry),
			order:   list.New(),
		}
		state.seenByHeight[height] = bucket
	}
	if _, duplicate := bucket.entries[key]; duplicate {
		return nil
	}
	for len(bucket.entries) >= maxConsensusQueriesPerSenderHeight {
		removeConsensusQueryReplayEntry(state, bucket.order.Front().Value.(*consensusQueryReplayEntry))
	}
	for state.total >= maxConsensusQueriesPerSender {
		removeConsensusQueryReplayEntry(state, state.order.Front().Value.(*consensusQueryReplayEntry))
	}
	                                                                          
	                                                                            
	                                                                        
	bucket = state.seenByHeight[height]
	if bucket == nil {
		bucket = &consensusQueryReplayHeight{
			entries: make(map[[sha256.Size]byte]*consensusQueryReplayEntry),
			order:   list.New(),
		}
		state.seenByHeight[height] = bucket
	}
	entry := &consensusQueryReplayEntry{key: consensusQueryReplayKey{height: height, digest: key}}
	entry.globalElem = state.order.PushBack(entry)
	entry.heightElem = bucket.order.PushBack(entry)
	bucket.entries[key] = entry
	state.total++
	return &consensusQueryReplayReservation{state: state, entry: entry}
}

func consensusQueryHeightInReplayWindow(height, localAccepted uint64) bool {
	if height > localAccepted {
		return !consensus.IsTooFarFutureHeight(height, localAccepted)
	}
	return localAccepted-height <= consensus.MaxFutureBlockCacheWindow
}

func removeConsensusQueryReplayEntry(state *consensusQueryReplaySender, entry *consensusQueryReplayEntry) {
	if state == nil || entry == nil {
		return
	}
	bucket := state.seenByHeight[entry.key.height]
	if bucket == nil || bucket.entries[entry.key.digest] != entry {
		return
	}
	delete(bucket.entries, entry.key.digest)
	bucket.order.Remove(entry.heightElem)
	state.order.Remove(entry.globalElem)
	state.total--
	if len(bucket.entries) == 0 {
		delete(state.seenByHeight, entry.key.height)
	}
}

                                                                             
                                                                           
                                                                              
func (g *consensusQueryReplayGuard) release(reservation *consensusQueryReplayReservation) {
	if reservation == nil || reservation.state == nil || reservation.entry == nil {
		return
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	state := reservation.state
	entry := reservation.entry
	bucket := state.seenByHeight[entry.key.height]
	if bucket == nil || bucket.entries[entry.key.digest] != entry {
		return
	}
	removeConsensusQueryReplayEntry(state, entry)
}

func (hm *HandlerManager) validateConsensusQuerySignature(sender string, responder string, chainID uint64, signature, digest []byte) error {
	if sender == "" {
		return fmt.Errorf("empty query sender")
	}
	if responder == "" || responder != hm.address {
		return fmt.Errorf("query responder %q does not match local node %q", responder, hm.address)
	}
	if chainID != pb.GetDefaultChainID() {
		return fmt.Errorf("query chain ID %d does not match %d", chainID, pb.GetDefaultChainID())
	}
	if len(signature) == 0 {
		return fmt.Errorf("missing query signature")
	}
	if !hm.verifyNodeIdentity(sender, nil) {
		return fmt.Errorf("unknown query sender %q", sender)
	}
	pubKey, ok := consensus.ResolveNodePublicKey(sender)
	if !ok || !utils.VerifyECDSASignature(pubKey, digest, signature) {
		return fmt.Errorf("invalid query signature")
	}
	return nil
}

func (hm *HandlerManager) validatePushQuery(query *pb.PushQuery) ([]byte, error) {
	if query == nil {
		return nil, fmt.Errorf("nil PushQuery")
	}
	if len(query.Nonce) != consensus.SnowmanQueryNonceSize {
		return nil, fmt.Errorf("PushQuery nonce length %d, want %d", len(query.Nonce), consensus.SnowmanQueryNonceSize)
	}
	digest := consensus.ComputePushQueryDigest(query)
	if err := hm.validateConsensusQuerySignature(query.Address, query.Responder, query.ChainId, query.Signature, digest); err != nil {
		return nil, err
	}
	return digest, nil
}

func (hm *HandlerManager) validatePullQuery(query *pb.PullQuery) ([]byte, error) {
	if query == nil {
		return nil, fmt.Errorf("nil PullQuery")
	}
	if len(query.Nonce) != consensus.SnowmanQueryNonceSize {
		return nil, fmt.Errorf("PullQuery nonce length %d, want %d", len(query.Nonce), consensus.SnowmanQueryNonceSize)
	}
	digest := consensus.ComputePullQueryDigest(query)
	if err := hm.validateConsensusQuerySignature(query.Address, query.Responder, query.ChainId, query.Signature, digest); err != nil {
		return nil, err
	}
	return digest, nil
}

func (hm *HandlerManager) validateChitsEnvelope(chits *pb.Chits) error {
	if chits == nil {
		return fmt.Errorf("nil Chits")
	}
	if chits.Requester == "" || chits.Requester != hm.address {
		return fmt.Errorf("Chits requester %q does not match local node %q", chits.Requester, hm.address)
	}
	if chits.ChainId != pb.GetDefaultChainID() {
		return fmt.Errorf("Chits chain ID %d does not match %d", chits.ChainId, pb.GetDefaultChainID())
	}
	if len(chits.QueryNonce) != consensus.SnowmanQueryNonceSize {
		return fmt.Errorf("Chits query nonce length %d, want %d", len(chits.QueryNonce), consensus.SnowmanQueryNonceSize)
	}
	if len(chits.ChitSignature) == 0 {
		return fmt.Errorf("missing Chits signature")
	}
	return nil
}
