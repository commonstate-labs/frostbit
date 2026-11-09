package servicepeer

import (
	"bytes"
	cryptorand "crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"sort"
	"sync"
	"time"

	"dex/utils"
)

var (
	ErrSelfRecord        = errors.New("servicepeer: refusing self record")
	ErrStaleRecord       = errors.New("servicepeer: stale record sequence")
	ErrConflictingRecord = errors.New("servicepeer: conflicting record at same sequence")
	ErrDuplicateIP       = errors.New("servicepeer: public IP already belongs to another verified node")
	ErrPeerNotFound      = errors.New("servicepeer: peer not found")
	ErrSequenceMismatch  = errors.New("servicepeer: record sequence mismatch")
	ErrVerifiedCapacity  = errors.New("servicepeer: verified table is full")
)

                                                                               
                                         
type PeerState uint8

const (
	StateCandidate PeerState = iota + 1
	StateVerified
	StateDegraded
)

                                                               
type Peer struct {
	Record                  Record
	State                   PeerState
	BootstrapStartedAt      time.Time
	ArchiveProofHeight      uint64
	ArchiveProofAt          time.Time
	EverArchiveProved       bool
	ContributionDebt        bool
	SuccessfulChecks        uint64
	FailedChecks            uint64
	ConsecutiveFailures     uint32
	LastSuccessHeight       uint64
	LastFailureHeight       uint64
	SuccessfulDataResponses uint64
	FailedDataResponses     uint64
	ConsecutiveDataFailures uint32
	LastDataSuccessHeight   uint64
	LastDataFailureHeight   uint64
	DataRetryAfter          time.Time
}

                                                                         
                                                                               
                                              
type Config struct {
	CandidateCapacity        int
	CandidatePerIP           int
	VerifiedCapacity         int
	FailureThreshold         uint32
	DataFailureBackoff       time.Duration
	BootstrapGrace           time.Duration
	BootstrapIPCapacity      int
	ProviderHealthIPCapacity int
	ArchiveProofTTL          time.Duration
	Random                   io.Reader
	Now                      func() time.Time
}

func DefaultConfig() Config {
	return Config{
		CandidateCapacity:        1000,
		CandidatePerIP:           2,
		VerifiedCapacity:         64,
		FailureThreshold:         3,
		DataFailureBackoff:       2 * time.Minute,
		BootstrapGrace:           24 * time.Hour,
		BootstrapIPCapacity:      4096,
		ProviderHealthIPCapacity: 4096,
		ArchiveProofTTL:          6 * time.Hour,
		Random:                   cryptorand.Reader,
		Now:                      time.Now,
	}
}

                                                                          
type HeightRange struct {
	From uint64
	To   uint64
}

                                                                         
                                                      
type SampleOptions struct {
	Limit               int
	Archive             *HeightRange
	MinCheckpoint       uint64
	RequireGossipAccess bool
	IncludeDegraded     bool
	ExcludeNodeIDs      map[string]struct{}
}

                                                                        
type UpsertResult uint8

const (
	UpsertUnchanged UpsertResult = iota
	UpsertAdded
	UpsertUpdated
)

type storedPeer struct {
	peer  Peer
	order uint64
}

                                                                            
                                                                          
                                                                               
                                                             
type consumptionIPState struct {
	BootstrapStartedAt time.Time
	ArchiveProofHeight uint64
	ArchiveProofAt     time.Time
	EverArchiveProved  bool
	ContributionDebt   bool
}

type providerIPState struct {
	ConsecutiveFailures uint32
	RetryAfter          time.Time
	LastFailureAt       time.Time
}

                                                                              
                                                                              
                                                                          
                                                                               
                                                                            
                                                                             
                             
type Store struct {
	mu sync.RWMutex

	chainID uint64
	selfID  string
	config  Config

	candidates map[string]*storedPeer
	verified   map[string]*storedPeer
	ipOwners   map[netip.Addr]string
	                                                                   
	                                                                          
	consumptionByIP        map[netip.Addr]*consumptionIPState
	providerByIP           map[netip.Addr]*providerIPState
	providerSaturatedUntil time.Time
	nextOrder              uint64

	randMu sync.Mutex
}

func NewStore(chainID uint64, selfID string, config Config) (*Store, error) {
	if chainID == 0 {
		return nil, fmt.Errorf("%w: zero chain ID", ErrWrongChain)
	}
	if selfID != "" && !isNodeID(selfID) {
		return nil, fmt.Errorf("servicepeer: invalid self node ID")
	}
	defaults := DefaultConfig()
	if config.CandidateCapacity == 0 {
		config.CandidateCapacity = defaults.CandidateCapacity
	}
	if config.VerifiedCapacity == 0 {
		config.VerifiedCapacity = defaults.VerifiedCapacity
	}
	if config.CandidatePerIP == 0 {
		config.CandidatePerIP = defaults.CandidatePerIP
	}
	if config.FailureThreshold == 0 {
		config.FailureThreshold = defaults.FailureThreshold
	}
	if config.DataFailureBackoff <= 0 {
		config.DataFailureBackoff = defaults.DataFailureBackoff
	}
	if config.BootstrapGrace <= 0 {
		config.BootstrapGrace = defaults.BootstrapGrace
	}
	if config.BootstrapIPCapacity == 0 {
		config.BootstrapIPCapacity = defaults.BootstrapIPCapacity
	}
	if config.ProviderHealthIPCapacity == 0 {
		config.ProviderHealthIPCapacity = defaults.ProviderHealthIPCapacity
	}
	if config.ArchiveProofTTL <= 0 {
		config.ArchiveProofTTL = defaults.ArchiveProofTTL
	}
	if config.Random == nil {
		config.Random = defaults.Random
	}
	if config.Now == nil {
		config.Now = defaults.Now
	}
	if config.CandidateCapacity < 1 || config.CandidatePerIP < 1 || config.VerifiedCapacity < 1 || config.BootstrapIPCapacity < 1 || config.ProviderHealthIPCapacity < 1 {
		return nil, fmt.Errorf("servicepeer: table capacities must be positive")
	}
	return &Store{
		chainID:         chainID,
		selfID:          selfID,
		config:          config,
		candidates:      make(map[string]*storedPeer, config.CandidateCapacity),
		verified:        make(map[string]*storedPeer, config.VerifiedCapacity),
		ipOwners:        make(map[netip.Addr]string, config.VerifiedCapacity),
		consumptionByIP: make(map[netip.Addr]*consumptionIPState, min(config.BootstrapIPCapacity, config.VerifiedCapacity)),
		providerByIP:    make(map[netip.Addr]*providerIPState, min(config.ProviderHealthIPCapacity, config.VerifiedCapacity)),
	}, nil
}

                                                                            
                                                                       
func (s *Store) UpsertCandidate(record Record) (UpsertResult, error) {
	if err := VerifyRecord(record, s.chainID); err != nil {
		return UpsertUnchanged, err
	}
	if record.NodeID == s.selfID {
		return UpsertUnchanged, ErrSelfRecord
	}
	ip, _ := PublicEndpointIP(record.Endpoint)
	record = cloneRecord(record)

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, inVerified := s.verified[record.NodeID]
	if !inVerified {
		existing = s.candidates[record.NodeID]
	}
	if existing != nil {
		if record.Sequence < existing.peer.Record.Sequence {
			return UpsertUnchanged, ErrStaleRecord
		}
		if record.Sequence == existing.peer.Record.Sequence {
			if !bytes.Equal(RecordDigest(record), RecordDigest(existing.peer.Record)) {
				return UpsertUnchanged, ErrConflictingRecord
			}
			return UpsertUnchanged, nil
		}
	}
	if existing == nil {
		s.ensureCandidateIPSlotLocked(ip, "")
		s.ensureCandidateSlotLocked("")
		entry := &storedPeer{peer: Peer{Record: record, State: StateCandidate}, order: s.takeOrderLocked()}
		s.candidates[record.NodeID] = entry
		return UpsertAdded, nil
	}

	oldIP, _ := PublicEndpointIP(existing.peer.Record.Endpoint)
	if inVerified && record.Endpoint == existing.peer.Record.Endpoint {
		                                                                        
		                                                                       
		                                                                            
		if err := s.claimVerifiedIPLocked(ip, record.NodeID); err != nil {
			return UpsertUnchanged, err
		}
		existing.peer.Record = record
		existing.peer.State = StateVerified
		state := s.consumptionByIP[ip]
		if state != nil && state.ArchiveProofHeight != 0 && !CoversArchive(record, HeightRange{From: state.ArchiveProofHeight, To: state.ArchiveProofHeight}) {
			state.ArchiveProofHeight = 0
			state.ArchiveProofAt = time.Time{}
		}
		s.syncConsumptionSnapshotLocked(existing)
		existing.order = s.takeOrderLocked()
		return UpsertUpdated, nil
	}
	if inVerified {
		s.ensureCandidateIPSlotLocked(ip, record.NodeID)
		s.ensureCandidateSlotLocked("")
		delete(s.verified, record.NodeID)
		s.releaseVerifiedIPLocked(oldIP, record.NodeID)
	} else {
		delete(s.candidates, record.NodeID)
		s.ensureCandidateIPSlotLocked(ip, record.NodeID)
	}
	existing.peer.Record = record
	existing.peer.State = StateCandidate
	existing.peer.ConsecutiveFailures = 0
	existing.peer.BootstrapStartedAt = time.Time{}
	existing.peer.ArchiveProofHeight = 0
	existing.peer.ArchiveProofAt = time.Time{}
	existing.peer.EverArchiveProved = false
	existing.peer.ContributionDebt = false
	existing.order = s.takeOrderLocked()
	s.candidates[record.NodeID] = existing
	return UpsertUpdated, nil
}

                                                                             
                                                                             
                                                                              
                                  
func (s *Store) MarkVerified(nodeID string, sequence, atHeight uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing := s.verified[nodeID]; existing != nil {
		if existing.peer.Record.Sequence != sequence {
			return ErrSequenceMismatch
		}
		ip, _ := PublicEndpointIP(existing.peer.Record.Endpoint)
		if err := s.claimVerifiedIPLocked(ip, nodeID); err != nil {
			return err
		}
		s.syncConsumptionSnapshotLocked(existing)
		s.syncProviderHealthSnapshotLocked(existing)
		existing.peer.SuccessfulChecks++
		existing.peer.ConsecutiveFailures = 0
		existing.peer.LastSuccessHeight = atHeight
		existing.order = s.takeOrderLocked()
		return nil
	}
	entry := s.candidates[nodeID]
	if entry == nil {
		return ErrPeerNotFound
	}
	if entry.peer.Record.Sequence != sequence {
		return ErrSequenceMismatch
	}
	ip, _ := PublicEndpointIP(entry.peer.Record.Endpoint)
	if owner := s.ipOwners[ip]; owner != "" && owner != nodeID {
		return fmt.Errorf("%w: %s is owned by %s", ErrDuplicateIP, ip, owner)
	}
	if len(s.verified) >= s.config.VerifiedCapacity && !s.rotateVerifiedSlotLocked(ip, nodeID) {
		return ErrVerifiedCapacity
	}
	delete(s.candidates, nodeID)
	entry.peer.State = StateVerified
	entry.peer.SuccessfulChecks++
	entry.peer.ConsecutiveFailures = 0
	entry.peer.LastSuccessHeight = atHeight
	s.syncConsumptionSnapshotLocked(entry)
	s.syncProviderHealthSnapshotLocked(entry)
	entry.order = s.takeOrderLocked()
	s.verified[nodeID] = entry
	s.ipOwners[ip] = nodeID
	return nil
}

                                                                        
                                                                       
func (s *Store) MarkSuccess(nodeID string, atHeight uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.verified[nodeID]
	if entry == nil {
		entry = s.candidates[nodeID]
	}
	if entry == nil {
		return ErrPeerNotFound
	}
	entry.peer.SuccessfulChecks++
	entry.peer.ConsecutiveFailures = 0
	entry.peer.LastSuccessHeight = atHeight
	if entry.peer.State == StateDegraded {
		entry.peer.State = StateCandidate
	}
	return nil
}

                                                                             
                                                                             
                                                          
func (s *Store) MarkFailure(nodeID string, atHeight uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, wasVerified := s.verified[nodeID]
	if !wasVerified {
		entry = s.candidates[nodeID]
	}
	if entry == nil {
		return ErrPeerNotFound
	}
	entry.peer.FailedChecks++
	entry.peer.ConsecutiveFailures++
	entry.peer.LastFailureHeight = atHeight
	if entry.peer.ConsecutiveFailures < s.config.FailureThreshold {
		return nil
	}
	s.demoteLocked(nodeID, entry, wasVerified)
	return nil
}

                                                                                 
                                                                            
                                                                                
                  
func (s *Store) MarkDataSuccess(nodeID string, atHeight uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.verified[nodeID]
	if entry == nil {
		entry = s.candidates[nodeID]
	}
	if entry == nil {
		return ErrPeerNotFound
	}
	entry.peer.SuccessfulDataResponses++
	entry.peer.LastDataSuccessHeight = atHeight
	s.clearProviderHealthLocked(entry)
	return nil
}

                                                                          
                                                                                    
                                                                            
                                                                             
                                                                              
func (s *Store) MarkArchiveProof(nodeID string, proofHeight, atHeight uint64) error {
	if proofHeight == 0 {
		return fmt.Errorf("servicepeer: block proof height must be positive")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.verified[nodeID]
	if entry == nil {
		return ErrPeerNotFound
	}
	if !CoversArchive(entry.peer.Record, HeightRange{From: proofHeight, To: proofHeight}) {
		return fmt.Errorf("servicepeer: block proof height %d is outside current signed range", proofHeight)
	}
	ip, err := PublicEndpointIP(entry.peer.Record.Endpoint)
	if err != nil {
		return err
	}
	state, ok := s.ensureConsumptionIPStateLocked(ip)
	if !ok {
		return fmt.Errorf("servicepeer: consumption IP history is full")
	}
	state.ArchiveProofHeight = proofHeight
	state.ArchiveProofAt = s.config.Now()
	state.EverArchiveProved = true
	state.ContributionDebt = false
	entry.peer.SuccessfulDataResponses++
	entry.peer.LastDataSuccessHeight = atHeight
	s.syncConsumptionSnapshotLocked(entry)
	return nil
}

                                                                            
                                                                               
                                                                               
func (s *Store) MarkArchiveBatchSuccess(nodeID string, proofHeight, atHeight uint64) error {
	if proofHeight == 0 {
		return fmt.Errorf("servicepeer: block-sync batch proof height must be positive")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.verified[nodeID]
	if entry == nil {
		entry = s.candidates[nodeID]
	}
	if entry == nil {
		return ErrPeerNotFound
	}
	if !CoversArchive(entry.peer.Record, HeightRange{From: proofHeight, To: proofHeight}) {
		return fmt.Errorf("servicepeer: block-sync batch height %d is outside current signed range", proofHeight)
	}
	state, ok := s.ensureConsumptionIPStateForEntryLocked(entry)
	if !ok {
		return fmt.Errorf("servicepeer: service IP history is full")
	}
	state.ArchiveProofHeight = proofHeight
	state.ArchiveProofAt = s.config.Now()
	state.EverArchiveProved = true
	state.ContributionDebt = false
	entry.peer.SuccessfulDataResponses++
	entry.peer.LastDataSuccessHeight = atHeight
	s.clearProviderHealthLocked(entry)
	s.syncConsumptionSnapshotLocked(entry)
	return nil
}

                                                                                 
                                                                              
                                                                               
func (s *Store) MarkDataFailure(nodeID string, atHeight uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, wasVerified := s.verified[nodeID]
	if !wasVerified {
		entry = s.candidates[nodeID]
	}
	if entry == nil {
		return ErrPeerNotFound
	}
	state, ok := s.ensureProviderIPStateForEntryLocked(entry, s.config.Now())
	if !ok {
		                                                                       
		                                                                  
		entry.peer.FailedDataResponses++
		entry.peer.LastDataFailureHeight = atHeight
		entry.peer.ConsecutiveDataFailures = s.config.FailureThreshold
		entry.peer.DataRetryAfter = s.config.Now().Add(s.config.DataFailureBackoff)
		s.demoteLocked(nodeID, entry, wasVerified)
		return nil
	}
	entry.peer.FailedDataResponses++
	entry.peer.LastDataFailureHeight = atHeight
	entry.peer.ConsecutiveDataFailures++
	state.ConsecutiveFailures++
	state.LastFailureAt = s.config.Now()
	if entry.peer.ConsecutiveDataFailures < s.config.FailureThreshold && state.ConsecutiveFailures < s.config.FailureThreshold {
		s.syncProviderHealthSnapshotLocked(entry)
		return nil
	}
	retryAfter := s.config.Now().Add(s.config.DataFailureBackoff)
	entry.peer.DataRetryAfter = retryAfter
	state.RetryAfter = retryAfter
	s.syncProviderHealthSnapshotLocked(entry)
	s.demoteLocked(nodeID, entry, wasVerified)
	return nil
}

func (s *Store) demoteLocked(nodeID string, entry *storedPeer, wasVerified bool) {
	entry.peer.State = StateDegraded
	if !wasVerified {
		return
	}
	ip, _ := PublicEndpointIP(entry.peer.Record.Endpoint)
	s.ensureCandidateIPSlotLocked(ip, nodeID)
	s.ensureCandidateSlotLocked("")
	delete(s.verified, nodeID)
	s.releaseVerifiedIPLocked(ip, nodeID)
	entry.order = s.takeOrderLocked()
	s.candidates[nodeID] = entry
}

                                                                             
                                                                    
func (s *Store) Remove(nodeID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.verified[nodeID]
	if entry != nil {
		delete(s.verified, nodeID)
		ip, _ := PublicEndpointIP(entry.peer.Record.Endpoint)
		s.releaseVerifiedIPLocked(ip, nodeID)
	} else {
		entry = s.candidates[nodeID]
		delete(s.candidates, nodeID)
	}
	if entry == nil {
		return false
	}
	return true
}

func (s *Store) Get(nodeID string) (Peer, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry := s.verified[nodeID]
	if entry == nil {
		entry = s.candidates[nodeID]
	}
	if entry == nil {
		return Peer{}, false
	}
	return clonePeer(entry.peer), true
}

                                                                            
                                                                               
                                          
func (s *Store) CanServeData(nodeID string) bool {
	now := s.config.Now()
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry := s.verified[nodeID]
	if entry == nil {
		return false
	}
	return !s.dataQuarantinedLocked(entry, now)
}

                                                                                 
                                                                             
                                                                         
func (s *Store) CanConsumeData(nodeID string) bool {
	now := s.config.Now()
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry := s.verified[nodeID]
	return s.canAuthorizeDataConsumptionLocked(entry, now)
}

                                                                              
                                                                             
                                                                       
func (s *Store) CanReceiveGossip(nodeID string) bool {
	now := s.config.Now()
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.canReceiveGossipLocked(s.verified[nodeID], now)
}

                                                                              
                                                                               
                                                                             
                                                                            
func (s *Store) AuthorizeDataConsumption(nodeID string) bool {
	now := s.config.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.authorizeDataConsumptionLocked(s.verified[nodeID], now)
}

                                                                          
                                                                               
                                                                             
                                                      
func (s *Store) VerifiedByIP(ip netip.Addr) (Peer, bool) {
	ip = ip.Unmap()
	now := s.config.Now()
	s.mu.RLock()
	defer s.mu.RUnlock()
	nodeID := s.ipOwners[ip]
	entry := s.verified[nodeID]
	if entry == nil || s.dataQuarantinedLocked(entry, now) {
		return Peer{}, false
	}
	return clonePeer(entry.peer), true
}

                                                                                
                                                                           
                                                                              
func (s *Store) CanConsumeDataByIP(ip netip.Addr) (Peer, bool) {
	ip = ip.Unmap()
	now := s.config.Now()
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry := s.verified[s.ipOwners[ip]]
	if !s.canAuthorizeDataConsumptionLocked(entry, now) {
		return Peer{}, false
	}
	return clonePeer(entry.peer), true
}

                                                                            
                                                                 
func (s *Store) AuthorizeDataConsumptionByIP(ip netip.Addr) (Peer, bool) {
	ip = ip.Unmap()
	now := s.config.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.verified[s.ipOwners[ip]]
	if !s.authorizeDataConsumptionLocked(entry, now) {
		return Peer{}, false
	}
	return clonePeer(entry.peer), true
}

func (s *Store) CandidateCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.candidates)
}

func (s *Store) VerifiedCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.verified)
}

                                                                            
                                                                             
func (s *Store) Snapshot() []Peer {
	s.mu.RLock()
	peers := make([]Peer, 0, len(s.candidates)+len(s.verified))
	for _, entry := range s.candidates {
		peers = append(peers, clonePeer(entry.peer))
	}
	for _, entry := range s.verified {
		peers = append(peers, clonePeer(entry.peer))
	}
	s.mu.RUnlock()
	sort.Slice(peers, func(i, j int) bool { return peers[i].Record.NodeID < peers[j].Record.NodeID })
	return peers
}

func (s *Store) SampleCandidates(options SampleOptions) ([]Peer, error) {
	return s.sample(false, options)
}

func (s *Store) SampleVerified(options SampleOptions) ([]Peer, error) {
	return s.sample(true, options)
}

func (s *Store) sample(verified bool, options SampleOptions) ([]Peer, error) {
	if options.Limit < 0 {
		return nil, fmt.Errorf("servicepeer: negative sample limit")
	}
	if options.Limit == 0 {
		return []Peer{}, nil
	}
	if options.Archive != nil && options.Archive.From > options.Archive.To {
		return nil, fmt.Errorf("servicepeer: reversed requested block range")
	}

	now := s.config.Now()
	s.mu.RLock()
	table := s.candidates
	if verified {
		table = s.verified
	}
	peers := make([]Peer, 0, len(table))
	for nodeID, entry := range table {
		if _, excluded := options.ExcludeNodeIDs[nodeID]; excluded {
			continue
		}
		if !verified && entry.peer.State == StateDegraded && !options.IncludeDegraded {
			continue
		}
		if options.Archive != nil && !CoversArchive(entry.peer.Record, *options.Archive) {
			continue
		}
		if verified && options.Archive != nil && !s.hasFreshArchiveProofLocked(entry, now) {
			continue
		}
		if verified && options.RequireGossipAccess && !s.canReceiveGossipLocked(entry, now) {
			continue
		}
		if verified && (options.Archive != nil || options.RequireGossipAccess) && s.dataQuarantinedLocked(entry, now) {
			continue
		}
		if entry.peer.Record.CheckpointHeight < options.MinCheckpoint {
			continue
		}
		peers = append(peers, clonePeer(entry.peer))
	}
	s.mu.RUnlock()

	if err := s.shuffle(peers); err != nil {
		return nil, err
	}
	return diversePrefixSample(peers, options.Limit), nil
}

                                                                             
                                                                               
                                                       
func (s *Store) NeedsArchiveProof(nodeID string) bool {
	now := s.config.Now()
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry := s.verified[nodeID]
	if entry == nil {
		return true
	}
	return !s.hasFreshArchiveProofLocked(entry, now)
}

                                                                            
                                                                       
                                                                              
                                                                            
                                  
func (s *Store) CanProvideArchiveRange(nodeID string, requested HeightRange) (Record, bool) {
	now := s.config.Now()
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry := s.verified[nodeID]
	if entry == nil || s.dataQuarantinedLocked(entry, now) || !CoversArchive(entry.peer.Record, requested) || !s.hasFreshArchiveProofLocked(entry, now) {
		return Record{}, false
	}
	return cloneRecord(entry.peer.Record), true
}

func (s *Store) hasFreshArchiveProofLocked(entry *storedPeer, now time.Time) bool {
	state := s.consumptionStateForEntryLocked(entry)
	if entry == nil || state == nil || state.ArchiveProofHeight == 0 || state.ArchiveProofAt.IsZero() ||
		!CoversArchive(entry.peer.Record, HeightRange{From: state.ArchiveProofHeight, To: state.ArchiveProofHeight}) {
		return false
	}
	return now.Before(state.ArchiveProofAt.Add(s.config.ArchiveProofTTL))
}

func (s *Store) canReceiveGossipLocked(entry *storedPeer, now time.Time) bool {
	if entry == nil || s.dataQuarantinedLocked(entry, now) {
		return false
	}
	if s.hasFreshArchiveProofLocked(entry, now) {
		return true
	}
	state := s.consumptionStateForEntryLocked(entry)
	return state != nil && !state.BootstrapStartedAt.IsZero() &&
		now.Before(state.BootstrapStartedAt.Add(s.config.BootstrapGrace))
}

func (s *Store) canAuthorizeDataConsumptionLocked(entry *storedPeer, now time.Time) bool {
	if entry == nil || s.dataQuarantinedLocked(entry, now) {
		return false
	}
	ip, err := PublicEndpointIP(entry.peer.Record.Endpoint)
	if err != nil {
		return false
	}
	if state := s.consumptionByIP[ip]; state != nil {
		if state.BootstrapStartedAt.IsZero() {
			return true
		}
		if s.hasFreshArchiveProofLocked(entry, now) || now.Before(state.BootstrapStartedAt.Add(s.config.BootstrapGrace)) {
			return true
		}
		return state.EverArchiveProved && entry.peer.Record.ArchiveTo != 0 && !state.ContributionDebt
	} else if len(s.consumptionByIP) < s.config.BootstrapIPCapacity {
		return true
	}
	return false
}

func (s *Store) authorizeDataConsumptionLocked(entry *storedPeer, now time.Time) bool {
	if entry == nil || s.dataQuarantinedLocked(entry, now) {
		return false
	}
	ip, err := PublicEndpointIP(entry.peer.Record.Endpoint)
	if err != nil {
		return false
	}
	state, ok := s.ensureConsumptionIPStateLocked(ip)
	if !ok {
		return false
	}
	if state.BootstrapStartedAt.IsZero() {
		state.BootstrapStartedAt = now
		s.syncConsumptionSnapshotLocked(entry)
		return true
	}
	if s.hasFreshArchiveProofLocked(entry, now) || now.Before(state.BootstrapStartedAt.Add(s.config.BootstrapGrace)) {
		s.syncConsumptionSnapshotLocked(entry)
		return true
	}
	if !state.EverArchiveProved || entry.peer.Record.ArchiveTo == 0 || state.ContributionDebt {
		return false
	}
	                                                                    
	                                                                          
	                    
	state.ContributionDebt = true
	s.syncConsumptionSnapshotLocked(entry)
	return true
}

func (s *Store) consumptionStateForEntryLocked(entry *storedPeer) *consumptionIPState {
	if entry == nil {
		return nil
	}
	ip, err := PublicEndpointIP(entry.peer.Record.Endpoint)
	if err != nil {
		return nil
	}
	return s.consumptionByIP[ip]
}

func (s *Store) ensureConsumptionIPStateForEntryLocked(entry *storedPeer) (*consumptionIPState, bool) {
	if entry == nil {
		return nil, false
	}
	ip, err := PublicEndpointIP(entry.peer.Record.Endpoint)
	if err != nil {
		return nil, false
	}
	return s.ensureConsumptionIPStateLocked(ip)
}

func (s *Store) dataQuarantinedLocked(entry *storedPeer, now time.Time) bool {
	if entry != nil && !entry.peer.DataRetryAfter.IsZero() && now.Before(entry.peer.DataRetryAfter) {
		return true
	}
	if entry == nil {
		return false
	}
	ip, err := PublicEndpointIP(entry.peer.Record.Endpoint)
	if err != nil {
		return true
	}
	if state := s.providerByIP[ip]; state != nil {
		return !state.RetryAfter.IsZero() && now.Before(state.RetryAfter)
	}
	return now.Before(s.providerSaturatedUntil)
}

func (s *Store) ensureProviderIPStateForEntryLocked(entry *storedPeer, now time.Time) (*providerIPState, bool) {
	if entry == nil {
		return nil, false
	}
	ip, err := PublicEndpointIP(entry.peer.Record.Endpoint)
	if err != nil {
		return nil, false
	}
	if state := s.providerByIP[ip]; state != nil {
		if !state.RetryAfter.IsZero() && !now.Before(state.RetryAfter) {
			state.ConsecutiveFailures = 0
			state.RetryAfter = time.Time{}
		}
		return state, true
	}
	if len(s.providerByIP) >= s.config.ProviderHealthIPCapacity {
		var victimIP netip.Addr
		var victim *providerIPState
		for candidateIP, candidate := range s.providerByIP {
			                                                                   
			                                                                         
			if !candidate.RetryAfter.IsZero() && now.Before(candidate.RetryAfter) {
				continue
			}
			if victim == nil || candidate.LastFailureAt.Before(victim.LastFailureAt) {
				victimIP, victim = candidateIP, candidate
			}
		}
		if victim != nil {
			delete(s.providerByIP, victimIP)
		}
	}
	if len(s.providerByIP) >= s.config.ProviderHealthIPCapacity {
		                                                                         
		                                                                          
		until := now.Add(s.config.DataFailureBackoff)
		if until.After(s.providerSaturatedUntil) {
			s.providerSaturatedUntil = until
		}
		return nil, false
	}
	state := &providerIPState{}
	s.providerByIP[ip] = state
	return state, true
}

func (s *Store) syncProviderHealthSnapshotLocked(entry *storedPeer) {
	if entry == nil {
		return
	}
	ip, err := PublicEndpointIP(entry.peer.Record.Endpoint)
	if err != nil {
		return
	}
	state := s.providerByIP[ip]
	if state == nil {
		return
	}
	if state.ConsecutiveFailures > entry.peer.ConsecutiveDataFailures {
		entry.peer.ConsecutiveDataFailures = state.ConsecutiveFailures
	}
	if state.RetryAfter.After(entry.peer.DataRetryAfter) {
		entry.peer.DataRetryAfter = state.RetryAfter
	}
}

func (s *Store) clearProviderHealthLocked(entry *storedPeer) {
	if entry == nil {
		return
	}
	entry.peer.ConsecutiveDataFailures = 0
	entry.peer.DataRetryAfter = time.Time{}
	ip, err := PublicEndpointIP(entry.peer.Record.Endpoint)
	if err != nil {
		return
	}
	if state := s.providerByIP[ip]; state != nil {
		state.ConsecutiveFailures = 0
		state.RetryAfter = time.Time{}
	}
}

func (s *Store) ensureConsumptionIPStateLocked(ip netip.Addr) (*consumptionIPState, bool) {
	ip = ip.Unmap()
	if state := s.consumptionByIP[ip]; state != nil {
		return state, true
	}
	if !ip.IsValid() || len(s.consumptionByIP) >= s.config.BootstrapIPCapacity {
		return nil, false
	}
	state := &consumptionIPState{}
	s.consumptionByIP[ip] = state
	return state, true
}

func (s *Store) syncConsumptionSnapshotLocked(entry *storedPeer) {
	if entry == nil {
		return
	}
	state := s.consumptionStateForEntryLocked(entry)
	if state == nil {
		entry.peer.BootstrapStartedAt = time.Time{}
		entry.peer.ArchiveProofHeight = 0
		entry.peer.ArchiveProofAt = time.Time{}
		entry.peer.EverArchiveProved = false
		entry.peer.ContributionDebt = false
		return
	}
	entry.peer.BootstrapStartedAt = state.BootstrapStartedAt
	entry.peer.ArchiveProofHeight = state.ArchiveProofHeight
	entry.peer.ArchiveProofAt = state.ArchiveProofAt
	entry.peer.EverArchiveProved = state.EverArchiveProved
	entry.peer.ContributionDebt = state.ContributionDebt
}

                                                                            
func CoversArchive(record Record, requested HeightRange) bool {
	if requested.From > requested.To || record.ArchiveTo == 0 {
		return false
	}
	return record.ArchiveFrom <= requested.From && record.ArchiveTo >= requested.To
}

func (s *Store) shuffle(peers []Peer) error {
	s.randMu.Lock()
	defer s.randMu.Unlock()
	for i := len(peers) - 1; i > 0; i-- {
		j, err := randomIndex(s.config.Random, i+1)
		if err != nil {
			return fmt.Errorf("servicepeer: randomize sample: %w", err)
		}
		peers[i], peers[j] = peers[j], peers[i]
	}
	return nil
}

func randomIndex(reader io.Reader, upper int) (int, error) {
	if upper <= 1 {
		return 0, nil
	}
	bound := uint64(upper)
	limit := ^uint64(0) - (^uint64(0) % bound)
	var buf [8]byte
	for {
		if _, err := io.ReadFull(reader, buf[:]); err != nil {
			return 0, err
		}
		value := binary.BigEndian.Uint64(buf[:])
		if value < limit {
			return int(value % bound), nil
		}
	}
}

                                                                          
                                                                          
                                                                               
                                    
func diversePrefixSample(peers []Peer, limit int) []Peer {
	if limit > len(peers) {
		limit = len(peers)
	}
	result := make([]Peer, 0, limit)
	deferred := make([]Peer, 0)
	seen := make(map[netip.Prefix]struct{}, limit)
	for _, peer := range peers {
		ip, err := PublicEndpointIP(peer.Record.Endpoint)
		if err != nil {
			continue
		}
		prefix := netip.PrefixFrom(ip, 24).Masked()
		if _, exists := seen[prefix]; exists {
			deferred = append(deferred, peer)
			continue
		}
		seen[prefix] = struct{}{}
		result = append(result, peer)
		if len(result) == limit {
			return result
		}
	}
	for _, peer := range deferred {
		result = append(result, peer)
		if len(result) == limit {
			break
		}
	}
	return result
}

func (s *Store) ensureCandidateSlotLocked(protectedNodeID string) {
	if len(s.candidates) < s.config.CandidateCapacity {
		return
	}
	var victimID string
	var victim *storedPeer
	for nodeID, entry := range s.candidates {
		if nodeID == protectedNodeID {
			continue
		}
		if victim == nil || (entry.peer.State == StateDegraded && victim.peer.State != StateDegraded) || (entry.peer.State == victim.peer.State && entry.order < victim.order) {
			victimID, victim = nodeID, entry
		}
	}
	if victim == nil {
		return
	}
	delete(s.candidates, victimID)
}

                                                                          
                                                                             
                                                                           
                                                                           
func (s *Store) ensureCandidateIPSlotLocked(ip netip.Addr, protectedNodeID string) {
	if !ip.IsValid() || s.config.CandidatePerIP <= 0 {
		return
	}
	count := 0
	var victimID string
	var victim *storedPeer
	for nodeID, entry := range s.candidates {
		candidateIP, err := PublicEndpointIP(entry.peer.Record.Endpoint)
		if err != nil || candidateIP != ip {
			continue
		}
		count++
		if nodeID == protectedNodeID {
			continue
		}
		if victim == nil || entry.order < victim.order {
			victimID, victim = nodeID, entry
		}
	}
	if count < s.config.CandidatePerIP || victim == nil {
		return
	}
	delete(s.candidates, victimID)
}

func (s *Store) claimVerifiedIPLocked(ip netip.Addr, nodeID string) error {
	if owner := s.ipOwners[ip]; owner != "" && owner != nodeID {
		return fmt.Errorf("%w: %s is owned by %s", ErrDuplicateIP, ip, owner)
	}
	s.ipOwners[ip] = nodeID
	return nil
}

func (s *Store) releaseVerifiedIPLocked(ip netip.Addr, nodeID string) {
	if s.ipOwners[ip] == nodeID {
		delete(s.ipOwners, ip)
	}
}

                                                                               
                                                                            
                                                                               
                                                                               
                                                                       
func (s *Store) rotateVerifiedSlotLocked(incomingIP netip.Addr, incomingNodeID string) bool {
	if len(s.verified) < s.config.VerifiedCapacity {
		return true
	}
	incomingPrefix := netip.PrefixFrom(incomingIP.Unmap(), 24).Masked()
	prefixCounts := make(map[netip.Prefix]int, len(s.verified))
	for _, entry := range s.verified {
		ip, err := PublicEndpointIP(entry.peer.Record.Endpoint)
		if err == nil {
			prefixCounts[netip.PrefixFrom(ip, 24).Masked()]++
		}
	}
	_, incomingRepresented := prefixCounts[incomingPrefix]
	now := s.config.Now()
	var victimID string
	var victim *storedPeer
	victimPriority := -1
	for nodeID, entry := range s.verified {
		if nodeID == incomingNodeID {
			continue
		}
		priority := 0
		if s.dataQuarantinedLocked(entry, now) {
			priority = 2
		} else if ip, err := PublicEndpointIP(entry.peer.Record.Endpoint); err == nil {
			prefix := netip.PrefixFrom(ip, 24).Masked()
			if (incomingRepresented && prefix == incomingPrefix) || (!incomingRepresented && prefixCounts[prefix] > 1) {
				priority = 1
			}
		}
		if victim == nil || priority > victimPriority || (priority == victimPriority && entry.order < victim.order) {
			victimID = nodeID
			victim = entry
			victimPriority = priority
		}
	}
	if victim == nil {
		return false
	}

	victimIP, _ := PublicEndpointIP(victim.peer.Record.Endpoint)
	s.ensureCandidateIPSlotLocked(victimIP, victimID)
	s.ensureCandidateSlotLocked(victimID)
	delete(s.verified, victimID)
	s.releaseVerifiedIPLocked(victimIP, victimID)
	victim.peer.State = StateCandidate
	victim.order = s.takeOrderLocked()
	s.candidates[victimID] = victim
	return true
}

func (s *Store) takeOrderLocked() uint64 {
	s.nextOrder++
	return s.nextOrder
}

func clonePeer(peer Peer) Peer {
	peer.Record = cloneRecord(peer.Record)
	return peer
}

func isNodeID(nodeID string) bool {
	return utils.IsDexAddress(nodeID)
}
