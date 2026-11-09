package handlers

import (
	"container/list"
	"crypto/sha256"
	"dex/consensus"
	"dex/pb"
	"dex/types"
	"encoding/binary"
	"sync"
	"time"
)

const (
	acceptedFrontierResponseCacheCapacity = 4096

	                                                                          
	                                                                           
	                                                                          
	acceptedFrontierRequesterRatePerSecond = 8
	acceptedFrontierRequesterBurst         = 256
	acceptedFrontierRequesterLimitCapacity = 4096
)

var acceptedFrontierReplayKeyDomain = []byte("DEX_ACCEPTED_FRONTIER_REPLAY_CACHE_V1")

                                                                        
                                                                            
                                                                               
                                                            
func acceptedFrontierReplayKey(req *pb.AcceptedFrontierRequest) [sha256.Size]byte {
	query := consensus.AcceptedFrontierQuery{
		Requester:     types.NodeID(req.RequesterId),
		Nonce:         req.Nonce,
		AnchorHeight:  req.AnchorHeight,
		AnchorBlockID: req.AnchorBlockId,
		TargetHeight:  req.TargetHeight,
	}
	requestDigest := consensus.ComputeAcceptedFrontierRequestDigest(query)

	h := sha256.New()
	_, _ = h.Write(acceptedFrontierReplayKeyDomain)
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], req.Version)
	_, _ = h.Write(size[:])
	var chainID [8]byte
	binary.BigEndian.PutUint64(chainID[:], req.ChainId)
	_, _ = h.Write(chainID[:])
	_, _ = h.Write(requestDigest)
	binary.BigEndian.PutUint32(size[:], uint32(len(req.Signature)))
	_, _ = h.Write(size[:])
	_, _ = h.Write(req.Signature)
	binary.BigEndian.PutUint32(size[:], uint32(len(req.RequesterSecpPublicKey)))
	_, _ = h.Write(size[:])
	_, _ = h.Write(req.RequesterSecpPublicKey)

	var key [sha256.Size]byte
	copy(key[:], h.Sum(nil))
	return key
}

type acceptedFrontierResponseCacheEntry struct {
	key      [sha256.Size]byte
	ready    chan struct{}
	response []byte
	err      error
	complete bool
	lru      *list.Element
}

                                                                          
                                                                          
                                                                              
                                                                            
type acceptedFrontierResponseCache struct {
	mu       sync.Mutex
	space    *sync.Cond
	capacity int
	entries  map[[sha256.Size]byte]*acceptedFrontierResponseCacheEntry
	lru      *list.List
}

func newAcceptedFrontierResponseCache(capacity int) *acceptedFrontierResponseCache {
	if capacity <= 0 {
		capacity = 1
	}
	cache := &acceptedFrontierResponseCache{
		capacity: capacity,
		entries:  make(map[[sha256.Size]byte]*acceptedFrontierResponseCacheEntry, capacity),
		lru:      list.New(),
	}
	cache.space = sync.NewCond(&cache.mu)
	return cache
}

                                                                             
                                                                                
func (c *acceptedFrontierResponseCache) get(key [sha256.Size]byte) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, exists := c.entries[key]
	if !exists || !entry.complete || entry.err != nil {
		return nil, false
	}
	if entry.lru != nil {
		c.lru.MoveToFront(entry.lru)
	}
	return cloneAcceptedFrontierResponse(entry.response), true
}

                                                                             
                                                                               
func (c *acceptedFrontierResponseCache) getOrBuild(key [sha256.Size]byte, build func() ([]byte, error)) ([]byte, error) {
	return c.getOrBuildAdmitted(key, nil, build)
}

                                                                             
                                                                             
                                                                        
func (c *acceptedFrontierResponseCache) getOrBuildAdmitted(key [sha256.Size]byte, admit func() error, build func() ([]byte, error)) ([]byte, error) {
	c.mu.Lock()
	for {
		if entry, exists := c.entries[key]; exists {
			if entry.complete {
				if entry.lru != nil {
					c.lru.MoveToFront(entry.lru)
				}
				response, err := cloneAcceptedFrontierResponse(entry.response), entry.err
				c.mu.Unlock()
				return response, err
			}

			ready := entry.ready
			c.mu.Unlock()
			<-ready

			c.mu.Lock()
			if current, retained := c.entries[key]; retained && current == entry && entry.lru != nil {
				c.lru.MoveToFront(entry.lru)
			}
			response, err := cloneAcceptedFrontierResponse(entry.response), entry.err
			c.mu.Unlock()
			return response, err
		}

		oldest := c.lru.Back()
		if len(c.entries) >= c.capacity && oldest == nil {
			                                                                    
			                                                                     
			                               
			c.space.Wait()
			continue
		}

		if admit != nil {
			if err := admit(); err != nil {
				c.mu.Unlock()
				return nil, err
			}
		}
		if len(c.entries) >= c.capacity {
			entry := oldest.Value.(*acceptedFrontierResponseCacheEntry)
			c.lru.Remove(oldest)
			entry.lru = nil
			delete(c.entries, entry.key)
		}
		break
	}

	entry := &acceptedFrontierResponseCacheEntry{key: key, ready: make(chan struct{})}
	c.entries[key] = entry
	c.mu.Unlock()

	response, err := build()
	response = cloneAcceptedFrontierResponse(response)

	c.mu.Lock()
	entry.response = response
	entry.err = err
	entry.complete = true
	if err != nil {
		if current := c.entries[key]; current == entry {
			delete(c.entries, key)
		}
	} else {
		entry.lru = c.lru.PushFront(entry)
	}
	close(entry.ready)
	c.space.Broadcast()
	c.mu.Unlock()

	return cloneAcceptedFrontierResponse(response), err
}

type acceptedFrontierRequesterLimitEntry struct {
	requester types.NodeID
	tokens    float64
	last      time.Time
	lru       *list.Element
}

                                                                          
                                                                              
                    
type acceptedFrontierRequesterLimiter struct {
	mu       sync.Mutex
	rate     float64
	burst    float64
	capacity int
	entries  map[types.NodeID]*acceptedFrontierRequesterLimitEntry
	lru      *list.List
}

func newAcceptedFrontierRequesterLimiter(ratePerSecond float64, burst, capacity int) *acceptedFrontierRequesterLimiter {
	if ratePerSecond <= 0 {
		ratePerSecond = 1
	}
	if burst <= 0 {
		burst = 1
	}
	if capacity <= 0 {
		capacity = 1
	}
	return &acceptedFrontierRequesterLimiter{
		rate:     ratePerSecond,
		burst:    float64(burst),
		capacity: capacity,
		entries:  make(map[types.NodeID]*acceptedFrontierRequesterLimitEntry, capacity),
		lru:      list.New(),
	}
}

func (l *acceptedFrontierRequesterLimiter) allow(requester types.NodeID, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	entry := l.entries[requester]
	if entry == nil {
		if len(l.entries) >= l.capacity {
			oldest := l.lru.Back()
			if oldest != nil {
				evicted := oldest.Value.(*acceptedFrontierRequesterLimitEntry)
				delete(l.entries, evicted.requester)
				l.lru.Remove(oldest)
			}
		}
		entry = &acceptedFrontierRequesterLimitEntry{
			requester: requester,
			tokens:    l.burst,
			last:      now,
		}
		entry.lru = l.lru.PushFront(entry)
		l.entries[requester] = entry
	} else {
		l.lru.MoveToFront(entry.lru)
		if elapsed := now.Sub(entry.last).Seconds(); elapsed > 0 {
			entry.tokens += elapsed * l.rate
			if entry.tokens > l.burst {
				entry.tokens = l.burst
			}
			entry.last = now
		}
	}

	if entry.tokens < 1 {
		return false
	}
	entry.tokens--
	return true
}

func cloneAcceptedFrontierResponse(response []byte) []byte {
	if response == nil {
		return nil
	}
	return append([]byte(nil), response...)
}
