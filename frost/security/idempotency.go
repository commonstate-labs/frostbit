                                
                        

package security

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

                                        
type IdempotencyChecker struct {
	mu      sync.RWMutex
	seen    map[string]time.Time
	ttl     time.Duration
	maxSize int
}

                                                 
func NewIdempotencyChecker(ttl time.Duration, maxSize int) *IdempotencyChecker {
	ic := &IdempotencyChecker{
		seen:    make(map[string]time.Time),
		ttl:     ttl,
		maxSize: maxSize,
	}

	                         
	go ic.cleanupLoop()

	return ic
}

                                 
                                       
                                                 
func (ic *IdempotencyChecker) Check(key string) bool {
	ic.mu.Lock()
	defer ic.mu.Unlock()

	if _, exists := ic.seen[key]; exists {
		return false          
	}

	                                                
	if len(ic.seen) >= ic.maxSize {
		ic.evictOldest()
	}

	ic.seen[key] = time.Now()
	return true                
}

                                       
func (ic *IdempotencyChecker) CheckWithHash(data []byte) bool {
	hash := sha256.Sum256(data)
	key := hex.EncodeToString(hash[:])
	return ic.Check(key)
}

                                    
func (ic *IdempotencyChecker) evictOldest() {
	var oldestKey string
	var oldestTime time.Time

	for k, t := range ic.seen {
		if oldestKey == "" || t.Before(oldestTime) {
			oldestKey = k
			oldestTime = t
		}
	}

	if oldestKey != "" {
		delete(ic.seen, oldestKey)
	}
}

                                       
func (ic *IdempotencyChecker) cleanupLoop() {
	ticker := time.NewTicker(ic.ttl / 2)
	defer ticker.Stop()

	for range ticker.C {
		ic.cleanup()
	}
}

                             
func (ic *IdempotencyChecker) cleanup() {
	ic.mu.Lock()
	defer ic.mu.Unlock()

	now := time.Now()
	for k, t := range ic.seen {
		if now.Sub(t) > ic.ttl {
			delete(ic.seen, k)
		}
	}
}

                                       
func GenerateJobID(chain string, vaultID uint32, epoch uint64, seq uint64) string {
	h := sha256.New()
	h.Write([]byte("frost_job"))
	h.Write([]byte(chain))
	h.Write([]byte(fmt.Sprintf("%d", vaultID)))
	h.Write([]byte(fmt.Sprintf("%d", epoch)))
	h.Write([]byte(fmt.Sprintf("%d", seq)))
	h.Write([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
	return hex.EncodeToString(h.Sum(nil)[:16])
}

                                                 
func GenerateWithdrawID(chain string, asset string, seq uint64, height uint64) string {
	h := sha256.New()
	h.Write([]byte("frost_withdraw"))
	h.Write([]byte(chain))
	h.Write([]byte(asset))
	h.Write([]byte(fmt.Sprintf("%d", seq)))
	h.Write([]byte(fmt.Sprintf("%d", height)))
	return hex.EncodeToString(h.Sum(nil)[:16])
}

                                      
func (ic *IdempotencyChecker) Size() int {
	ic.mu.RLock()
	defer ic.mu.RUnlock()
	return len(ic.seen)
}

                                              

                                                                               
                                                                               
                                                                     
const FrostSequenceReservationSize = uint64(1024)

                                                                     
                                                                                                                       
type SeqReplayGuard struct {
	mu     sync.RWMutex
	states map[string]*seqReplayState
	window uint64
	                                                                           
	                                                                             
	                                                                    
	maxPending int
}

type seqReplayState struct {
	maxSeen       uint64
	maxDigest     [sha256.Size]byte
	maxReplayable bool
	                                                                           
	                                                                       
	slots   []seqReplaySlot
	pending map[uint64]*seqReplayPending
}

type seqReplaySlot struct {
	seq        uint64
	digest     [sha256.Size]byte
	replayable bool
}

type seqReplayPending struct {
	seq    uint64
	digest [sha256.Size]byte
}

                                                                    
type SeqReplayDecision uint8

const (
	SeqReplayAccepted SeqReplayDecision = iota
	SeqReplayInFlight
	SeqReplayAlreadyCommitted
	                                                                          
	                                                                           
	                                                                           
	                                                
	SeqReplayPermanentlyRejected
	SeqReplayStale
	SeqReplayConflict
	SeqReplayBusy
)

                                                                       
                                                                           
                                                                         
                                     
type SeqReplayReservation struct {
	sender string
	state  *seqReplayState
	entry  *seqReplayPending
}

                                                      
                                                                                       
func NewSeqReplayGuard(window uint64) *SeqReplayGuard {
	const maxConcurrentReservationsPerSender = 64
	maxPending := maxConcurrentReservationsPerSender
	if window == 0 {
		                                                                        
		                                                                        
		                                                                     
		maxPending = 1
	} else if window < uint64(maxPending) {
		maxPending = int(window)
	}
	return &SeqReplayGuard{
		states:     make(map[string]*seqReplayState),
		window:     window,
		maxPending: maxPending,
	}
}

                                                                            
                                                                            
                                             
func (g *SeqReplayGuard) Reserve(sender string, seq uint64, digest [sha256.Size]byte) (
	reservation *SeqReplayReservation,
	decision SeqReplayDecision,
	previousMax uint64,
) {
	if seq == 0 {
		return nil, SeqReplayStale, 0
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	state := g.states[sender]
	if state == nil {
		state = &seqReplayState{pending: make(map[uint64]*seqReplayPending)}
		if g.window > 0 {
			state.slots = make([]seqReplaySlot, g.window)
		}
		g.states[sender] = state
	}
	previousMax = state.maxSeen

	if pending := state.pending[seq]; pending != nil {
		if pending.digest == digest {
			return nil, SeqReplayInFlight, previousMax
		}
		return nil, SeqReplayConflict, previousMax
	}

	if seq == state.maxSeen && state.maxSeen != 0 {
		if state.maxDigest == digest {
			if !state.maxReplayable {
				return nil, SeqReplayPermanentlyRejected, previousMax
			}
			return nil, SeqReplayAlreadyCommitted, previousMax
		}
		return nil, SeqReplayConflict, previousMax
	}
	if seq < state.maxSeen {
		if g.window == 0 || state.maxSeen-seq >= g.window {
			return nil, SeqReplayStale, previousMax
		}
		slot := state.slots[seq%g.window]
		if slot.seq == seq {
			if slot.digest == digest {
				if !slot.replayable {
					return nil, SeqReplayPermanentlyRejected, previousMax
				}
				return nil, SeqReplayAlreadyCommitted, previousMax
			}
			return nil, SeqReplayConflict, previousMax
		}
	}
	if len(state.pending) >= g.maxPending {
		return nil, SeqReplayBusy, previousMax
	}

	entry := &seqReplayPending{seq: seq, digest: digest}
	state.pending[seq] = entry
	return &SeqReplayReservation{sender: sender, state: state, entry: entry}, SeqReplayAccepted, previousMax
}

                                                                            
                                                    
func (g *SeqReplayGuard) Commit(reservation *SeqReplayReservation) bool {
	return g.finish(reservation, true)
}

                                                                      
                                                                        
                                                   
func (g *SeqReplayGuard) Reject(reservation *SeqReplayReservation) bool {
	return g.finish(reservation, false)
}

func (g *SeqReplayGuard) finish(reservation *SeqReplayReservation, replayable bool) bool {
	if reservation == nil || reservation.state == nil || reservation.entry == nil {
		return false
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	state := g.states[reservation.sender]
	entry := reservation.entry
	if state != reservation.state || state.pending[entry.seq] != entry {
		return false
	}
	delete(state.pending, entry.seq)

	if entry.seq > state.maxSeen {
		state.maxSeen = entry.seq
		state.maxDigest = entry.digest
		state.maxReplayable = replayable
		if g.window > 0 {
			state.slots[entry.seq%g.window] = seqReplaySlot{seq: entry.seq, digest: entry.digest, replayable: replayable}
		}
		return true
	}
	if entry.seq == state.maxSeen {
		state.maxDigest = entry.digest
		state.maxReplayable = replayable
		if g.window > 0 {
			state.slots[entry.seq%g.window] = seqReplaySlot{seq: entry.seq, digest: entry.digest, replayable: replayable}
		}
		return true
	}
	if g.window > 0 && state.maxSeen-entry.seq < g.window {
		state.slots[entry.seq%g.window] = seqReplaySlot{seq: entry.seq, digest: entry.digest, replayable: replayable}
	}
	return true
}

                                                                               
                                                                             
func (g *SeqReplayGuard) Release(reservation *SeqReplayReservation) bool {
	if reservation == nil || reservation.state == nil || reservation.entry == nil {
		return false
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	state := g.states[reservation.sender]
	entry := reservation.entry
	if state != reservation.state || state.pending[entry.seq] != entry {
		return false
	}
	delete(state.pending, entry.seq)
	if state.maxSeen == 0 && len(state.pending) == 0 {
		delete(g.states, reservation.sender)
	}
	return true
}

                                                         
                                                                                                        
func (g *SeqReplayGuard) Check(sender string, seq uint64) (valid bool, isReplay bool) {
	var digest [sha256.Size]byte
	reservation, decision, _ := g.Reserve(sender, seq, digest)
	if decision != SeqReplayAccepted {
		return false, true
	}
	if !g.Commit(reservation) {
		return false, true
	}
	return true, false
}

                                                 
func (g *SeqReplayGuard) GetMaxSeq(sender string) uint64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	state := g.states[sender]
	if state == nil {
		return 0
	}
	return state.maxSeen
}

                                                                  
func (g *SeqReplayGuard) Reset(sender string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.states, sender)
}

                                    
func (g *SeqReplayGuard) ResetAll() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.states = make(map[string]*seqReplayState)
}
