package sender

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"dex/pb"
	"dex/servicepeer"
	"dex/types"

	lru "github.com/hashicorp/golang-lru"
	"google.golang.org/protobuf/proto"
)

const (
	servicePeerExchangeLimit       = 64
	servicePeerExchangeBodyLimit   = 128 << 10
	servicePeerProbeResponseLimit  = int64(64 << 10)
	servicePeerProbeConcurrency    = 4
	servicePeerDiscoveryFanout     = 3
	servicePeerCandidateProbeLimit = 4
	servicePeerVerifiedProbeLimit  = 4
	servicePeerReplayCapacity      = 4096
	servicePeerSyncConcurrency     = 2
	servicePeerOrdinaryConcurrency = 1
	servicePeerProofConcurrency    = 1
	liveBlockServeConcurrency      = 20
	servicePeerSyncRequestTimeout  = 35 * time.Second
	servicePeerArchiveProofTimeout = 15 * time.Second
	servicePeerProofResponseLimit  = int64(4 << 20)
)

var (
	ErrServiceContributionRequired = errors.New("service peer contribution proof required")
	ErrServiceArchiveCapacity      = errors.New("service block sync capacity exhausted")
	ErrServiceProbeCapacity        = errors.New("service probe capacity exhausted")
	ErrServiceProofUnavailable     = errors.New("service block proof is locally unavailable")
	ErrServiceProofInconclusive    = errors.New("service block proof cannot be checked against local canonical storage")
)

type serviceHTTPStatusError struct {
	StatusCode int
	Body       string
}

func (e *serviceHTTPStatusError) Error() string {
	if e == nil {
		return "service endpoint returned an invalid status"
	}
	return fmt.Sprintf("service endpoint status=%d body=%s", e.StatusCode, e.Body)
}

const (
	ServicePeerExchangePath = "/service/peers"
	ServicePeerProbePath    = "/service/probe"
	ServicePeerCallbackPath = "/service/callback"
	ServicePeerGossipPath   = "/service/gossip"
	ServicePeerSyncPath     = "/service/syncblocks"
)

type servicePeerRuntime struct {
	mu               sync.RWMutex
	chainID          uint64
	endpoint         string
	signer           servicepeer.Signer
	store            *servicepeer.Store
	record           servicepeer.Record
	readAvailability func() serviceAvailabilitySnapshot

	replay *lru.Cache

	inboundProbeSem           chan struct{}
	outboundCandidateProbeSem chan struct{}
	outboundVerifiedProbeSem  chan struct{}
	probeMu                   sync.Mutex
	inboundProbes             map[string]struct{}
	                                                                         
	                                                                           
	                                                                     
	                                                                       
	                                                                        
	outboundProbes map[string]*activeOutboundServiceProbe
	start          sync.Once
	probeWg        sync.WaitGroup
	stopCtx        context.Context
	stopCancel     context.CancelFunc
}

type serviceAvailabilitySnapshot struct {
	archiveFrom      uint64
	archiveTo        uint64
	checkpointHeight uint64
}

type activeOutboundServiceProbe struct {
	lane             chan struct{}
	nodeID           string
	recordSequence   uint64
	challengeBound   bool
	callbackConsumed bool
}

type archiveServeLane uint8

const (
	archiveServeOrdinary archiveServeLane = iota + 1
	archiveServePrivileged
	archiveServeProof
	archiveServeLiveBlock
)

                                                                             
                                                                           
                                                                            
                                                        
func (sm *SenderManager) ConfigureServiceIdentity(signer servicepeer.Signer, endpoint string, chainID uint64) error {
	if sm == nil {
		return fmt.Errorf("sender manager is nil")
	}
	canonical, err := servicepeer.CanonicalPublicEndpoint(strings.TrimSpace(endpoint))
	if err != nil {
		return err
	}
	availability := sm.readServiceAvailability()
	record, err := servicepeer.NewSignedRecord(servicepeer.Record{
		ChainID:          chainID,
		Endpoint:         canonical,
		ArchiveFrom:      availability.archiveFrom,
		ArchiveTo:        availability.archiveTo,
		CheckpointHeight: availability.checkpointHeight,
		Sequence:         nextServiceRecordSequence(0),
	}, signer)
	if err != nil {
		return err
	}
	if sm.address != "" && record.NodeID != sm.address {
		return fmt.Errorf("service identity %s does not match local node address %s", record.NodeID, sm.address)
	}
	store, err := servicepeer.NewStore(chainID, record.NodeID, servicepeer.DefaultConfig())
	if err != nil {
		return err
	}
	replay, err := lru.New(servicePeerReplayCapacity)
	if err != nil {
		return err
	}
	stopCtx, stopCancel := context.WithCancel(context.Background())
	sm.servicePeer = &servicePeerRuntime{
		chainID:                   chainID,
		endpoint:                  canonical,
		signer:                    signer,
		store:                     store,
		record:                    cloneServiceRecord(record),
		readAvailability:          sm.readServiceAvailability,
		replay:                    replay,
		inboundProbeSem:           make(chan struct{}, servicePeerProbeConcurrency),
		outboundCandidateProbeSem: make(chan struct{}, servicePeerProbeConcurrency),
		outboundVerifiedProbeSem:  make(chan struct{}, servicePeerProbeConcurrency),
		inboundProbes:             make(map[string]struct{}),
		outboundProbes:            make(map[string]*activeOutboundServiceProbe),
		stopCtx:                   stopCtx,
		stopCancel:                stopCancel,
	}
	return nil
}

func (sm *SenderManager) readServiceAvailability() serviceAvailabilitySnapshot {
	var availability serviceAvailabilitySnapshot
	if sm == nil || sm.dbManager == nil {
		return availability
	}
	if from, to, err := sm.dbManager.GetBlockArchiveRange(); err == nil {
		availability.archiveFrom = from
		availability.archiveTo = to
	}
	if checkpoint, err := sm.dbManager.GetLatestStateCheckpointHeight(); err == nil {
		availability.checkpointHeight = checkpoint
	}
	return availability
}

func (sm *SenderManager) ServicePeerEnabled() bool {
	return sm != nil && sm.servicePeer != nil
}

                                                                           
                                                                          
func (sm *SenderManager) ServicePeerStore() *servicepeer.Store {
	if sm == nil || sm.servicePeer == nil {
		return nil
	}
	return sm.servicePeer.store
}

func (sm *SenderManager) LocalServiceRecord() (servicepeer.Record, error) {
	return sm.localServiceRecord(true)
}

func (sm *SenderManager) cachedLocalServiceRecord() (servicepeer.Record, error) {
	return sm.localServiceRecord(false)
}

func (sm *SenderManager) localServiceRecord(refresh bool) (servicepeer.Record, error) {
	if sm == nil || sm.servicePeer == nil {
		return servicepeer.Record{}, fmt.Errorf("service peer identity is unavailable")
	}
	runtime := sm.servicePeer
	if !refresh || runtime.readAvailability == nil {
		runtime.mu.RLock()
		record := cloneServiceRecord(runtime.record)
		runtime.mu.RUnlock()
		return record, nil
	}

	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	                                                                         
	                                                                      
	                                                               
	availability := runtime.readAvailability()
	if runtime.record.ArchiveFrom == availability.archiveFrom &&
		runtime.record.ArchiveTo == availability.archiveTo &&
		runtime.record.CheckpointHeight == availability.checkpointHeight {
		return cloneServiceRecord(runtime.record), nil
	}
	record, err := servicepeer.NewSignedRecord(servicepeer.Record{
		ChainID:          runtime.chainID,
		Endpoint:         runtime.endpoint,
		ArchiveFrom:      availability.archiveFrom,
		ArchiveTo:        availability.archiveTo,
		CheckpointHeight: availability.checkpointHeight,
		Sequence:         nextServiceRecordSequence(runtime.record.Sequence),
	}, runtime.signer)
	if err != nil {
		return servicepeer.Record{}, err
	}
	runtime.record = cloneServiceRecord(record)
	return cloneServiceRecord(record), nil
}

func nextServiceRecordSequence(previous uint64) uint64 {
	now := uint64(time.Now().UnixNano())
	if now == 0 {
		now = 1
	}
	if now <= previous {
		return previous + 1
	}
	return now
}

                                                                           
                                                                                
                         
func (sm *SenderManager) BuildServicePeerExchange(req servicepeer.ExchangeRequest, sourceIP string) (servicepeer.ExchangeResponse, error) {
	if sm == nil || sm.servicePeer == nil {
		return servicepeer.ExchangeResponse{}, fmt.Errorf("service peer identity is unavailable")
	}
	if err := sm.acceptServiceCandidate(req.Self, sourceIP); err != nil {
		return servicepeer.ExchangeResponse{}, err
	}
	self, err := sm.LocalServiceRecord()
	if err != nil {
		return servicepeer.ExchangeResponse{}, err
	}
	peers, err := sm.servicePeer.store.SampleVerified(servicepeer.SampleOptions{Limit: servicePeerExchangeLimit})
	if err != nil {
		return servicepeer.ExchangeResponse{}, err
	}
	response := servicepeer.ExchangeResponse{Self: self, Peers: make([]servicepeer.Record, 0, len(peers))}
	for _, peer := range peers {
		response.Peers = append(response.Peers, cloneServiceRecord(peer.Record))
	}

	if peer, ok := sm.servicePeer.store.Get(req.Self.NodeID); !ok || peer.State != servicepeer.StateVerified {
		                                                                     
		                                                                       
		                                                                     
		sm.startServiceCandidateProbe(cloneServiceRecord(req.Self))
	}
	return response, nil
}

func (sm *SenderManager) acceptServiceCandidate(record servicepeer.Record, sourceIP string) error {
	runtime := sm.servicePeer
	if runtime == nil {
		return fmt.Errorf("service peer identity is unavailable")
	}
	if err := servicepeer.VerifyRecord(record, runtime.chainID); err != nil {
		return err
	}
	if !serviceEndpointMatchesSource(record.Endpoint, sourceIP) {
		return fmt.Errorf("service endpoint %s does not match source IP %s", record.Endpoint, sourceIP)
	}
	_, err := runtime.store.UpsertCandidate(record)
	if err == servicepeer.ErrStaleRecord || err == servicepeer.ErrSelfRecord {
		return err
	}
	return err
}

                                                                           
                                                                              
                                     
func (sm *SenderManager) AnswerServiceProbe(ctx context.Context, req servicepeer.ProbeRequest, sourceIP string) (servicepeer.ProbeResponse, func(), error) {
	if err := sm.validateInboundServiceChallenge(req, sourceIP, ServicePeerProbePath); err != nil {
		return servicepeer.ProbeResponse{}, nil, err
	}
	if !sm.acquireInboundServiceProbe(sourceIP) {
		return servicepeer.ProbeResponse{}, nil, ErrServiceProbeCapacity
	}
	defer sm.releaseInboundServiceProbe(sourceIP)
	if err := sm.callServiceCallback(ctx, req.Caller); err != nil {
		_ = sm.servicePeer.store.MarkFailure(req.Caller.NodeID, sm.serviceCurrentHeight())
		return servicepeer.ProbeResponse{}, nil, err
	}
	if err := sm.servicePeer.store.MarkVerified(req.Caller.NodeID, req.Caller.Sequence, sm.serviceCurrentHeight()); err != nil {
		return servicepeer.ProbeResponse{}, nil, err
	}
	if req.Challenge.RequestedHeight == servicepeer.ConnectivityProbeHeight {
		response, err := sm.buildServiceProbeResponse(req.Challenge)
		return response, nil, err
	}
	if !sm.servicePeer.store.CanConsumeData(req.Caller.NodeID) {
		return servicepeer.ProbeResponse{}, nil, ErrServiceContributionRequired
	}
	release, ok := sm.AcquireServiceProofServe(sourceIP)
	if !ok {
		return servicepeer.ProbeResponse{}, nil, ErrServiceArchiveCapacity
	}
	response, err := sm.buildServiceProbeResponse(req.Challenge)
	if err != nil {
		release()
		return servicepeer.ProbeResponse{}, nil, err
	}
	                                                                        
	                                                                               
	                                           
	if !sm.servicePeer.store.AuthorizeDataConsumption(req.Caller.NodeID) {
		release()
		return servicepeer.ProbeResponse{}, nil, ErrServiceContributionRequired
	}
	return response, release, nil
}

                                                                               
                    
func (sm *SenderManager) AnswerServiceCallback(req servicepeer.ProbeRequest, sourceIP string) (servicepeer.ProbeResponse, error) {
	if err := sm.validateInboundServiceChallenge(req, sourceIP, ServicePeerCallbackPath); err != nil {
		return servicepeer.ProbeResponse{}, err
	}
	if req.Challenge.RequestedHeight != servicepeer.ConnectivityProbeHeight {
		return servicepeer.ProbeResponse{}, fmt.Errorf("service callback is connectivity-only")
	}
	if !sm.consumeExpectedServiceCallback(req.Caller, sourceIP) {
		return servicepeer.ProbeResponse{}, fmt.Errorf("service callback does not match an active outbound probe")
	}
	                                                                          
	                                                                          
	                                                                        
	if err := sm.servicePeer.store.MarkVerified(req.Caller.NodeID, req.Caller.Sequence, sm.serviceCurrentHeight()); err != nil {
		return servicepeer.ProbeResponse{}, err
	}
	return sm.buildServiceProbeResponse(req.Challenge)
}

func (sm *SenderManager) validateInboundServiceChallenge(req servicepeer.ProbeRequest, sourceIP, replayDomain string) error {
	runtime := sm.servicePeer
	if runtime == nil {
		return fmt.Errorf("service peer identity is unavailable")
	}
	if err := sm.acceptServiceCandidate(req.Caller, sourceIP); err != nil {
		return err
	}
	self, err := sm.cachedLocalServiceRecord()
	if err != nil {
		return err
	}
	if req.Challenge.ChallengerID != req.Caller.NodeID {
		return fmt.Errorf("service challenge caller mismatch")
	}
	if req.Challenge.RecordSequence != self.Sequence {
		return servicepeer.ErrSequenceMismatch
	}
	if req.Challenge.RequestedHeight != servicepeer.ConnectivityProbeHeight &&
		!servicepeer.CoversArchive(self, servicepeer.HeightRange{From: req.Challenge.RequestedHeight, To: req.Challenge.RequestedHeight}) {
		return fmt.Errorf("service challenge height is outside current signed block range")
	}
	if err := servicepeer.VerifyChallenge(req.Challenge, runtime.chainID, self.NodeID); err != nil {
		return err
	}
	if !runtime.acceptReplay(replayDomain, servicepeer.ChallengeDigest(req.Challenge)) {
		return fmt.Errorf("replayed service challenge")
	}
	return nil
}

func (runtime *servicePeerRuntime) acceptReplay(domain string, digest []byte) bool {
	if runtime == nil || runtime.replay == nil {
		return false
	}
	key := domain + ":" + string(digest)
	alreadyPresent, _ := runtime.replay.ContainsOrAdd(key, struct{}{})
	return !alreadyPresent
}

func (sm *SenderManager) ProbeServiceCandidate(ctx context.Context, record servicepeer.Record) error {
	if sm == nil || sm.servicePeer == nil {
		return fmt.Errorf("service peer identity is unavailable")
	}
	probeKey, ok := sm.acquireOutboundServiceProbe(record)
	if !ok {
		return fmt.Errorf("service probe capacity exhausted")
	}
	defer sm.releaseOutboundServiceProbe(probeKey)
	return sm.probeServiceCandidateWithSlot(ctx, record)
}

func (sm *SenderManager) startServiceCandidateProbe(record servicepeer.Record) bool {
	if sm == nil || sm.servicePeer == nil {
		return false
	}
	probeKey, ok := sm.acquireOutboundServiceProbe(record)
	if !ok {
		return false
	}
	sm.servicePeer.probeWg.Add(1)
	go func() {
		defer sm.servicePeer.probeWg.Done()
		defer sm.releaseOutboundServiceProbe(probeKey)
		ctx, cancel := context.WithTimeout(sm.servicePeer.stopCtx, servicePeerArchiveProofTimeout)
		defer cancel()
		if err := sm.probeServiceCandidateWithSlot(ctx, record); err != nil && sm.Logger != nil {
			sm.Logger.Debug("[ServicePeer] reverse probe %s failed: %v", record.NodeID, err)
		}
	}()
	return true
}

func (sm *SenderManager) probeServiceCandidateWithSlot(ctx context.Context, record servicepeer.Record) error {
	if _, err := sm.servicePeer.store.UpsertCandidate(record); err != nil {
		if !errors.Is(err, servicepeer.ErrStaleRecord) {
			return err
		}
		current, ok := sm.servicePeer.store.Get(record.NodeID)
		if !ok {
			return err
		}
		record = current.Record
	}
	self, err := sm.LocalServiceRecord()
	if err != nil {
		return err
	}
	nonce, err := servicepeer.NewNonce()
	if err != nil {
		return err
	}
	proofHeight, err := sm.selectServiceArchiveProofHeight(record)
	if err != nil {
		return err
	}
	challenge, err := servicepeer.NewSignedChallenge(sm.servicePeer.signer, sm.servicePeer.chainID, record, nonce, proofHeight)
	if err != nil {
		return err
	}
	if !sm.bindOutboundServiceProbeChallenge(record) {
		return fmt.Errorf("service probe lost its active callback binding")
	}
	request := servicepeer.ProbeRequest{Caller: self, Challenge: challenge}
	var response servicepeer.ProbeResponse
	if err := sm.postServiceJSON(ctx, record.Endpoint, ServicePeerProbePath, request, &response, servicePeerProofResponseLimit); err != nil {
		return sm.recordServiceProbePostFailure(record, proofHeight, err)
	}
	if response.Self.NodeID != record.NodeID || response.Self.Sequence != record.Sequence {
		err := fmt.Errorf("service probe response record changed")
		_ = sm.servicePeer.store.MarkFailure(record.NodeID, sm.serviceCurrentHeight())
		return err
	}
	if err := servicepeer.VerifyRecord(response.Self, sm.servicePeer.chainID); err != nil {
		_ = sm.servicePeer.store.MarkFailure(record.NodeID, sm.serviceCurrentHeight())
		return err
	}
	validatedProofHeight, err := sm.validateServiceProbeResponse(challenge, response)
	if err != nil {
		if !errors.Is(err, ErrServiceProofInconclusive) {
			_ = sm.servicePeer.store.MarkDataFailure(record.NodeID, sm.serviceCurrentHeight())
		}
		return err
	}
	if err := sm.servicePeer.store.MarkVerified(record.NodeID, record.Sequence, sm.serviceCurrentHeight()); err != nil {
		return err
	}
	if validatedProofHeight != 0 {
		if err := sm.servicePeer.store.MarkArchiveProof(record.NodeID, validatedProofHeight, sm.serviceCurrentHeight()); err != nil {
			return err
		}
	}
	return nil
}

func (sm *SenderManager) recordServiceProbePostFailure(record servicepeer.Record, proofHeight uint64, postErr error) error {
	callbackSucceeded := sm.outboundServiceCallbackConsumed(record)
	var statusErr *serviceHTTPStatusError
	if errors.As(postErr, &statusErr) && statusErr.StatusCode == http.StatusPreconditionRequired && callbackSucceeded {
		return ErrServiceContributionRequired
	}
	if errors.As(postErr, &statusErr) &&
		(statusErr.StatusCode == http.StatusTooManyRequests || statusErr.StatusCode == http.StatusServiceUnavailable ||
			(statusErr.StatusCode == http.StatusConflict && callbackSucceeded)) {
		return postErr
	}
	if callbackSucceeded && proofHeight != servicepeer.ConnectivityProbeHeight {
		_ = sm.servicePeer.store.MarkDataFailure(record.NodeID, sm.serviceCurrentHeight())
	} else {
		_ = sm.servicePeer.store.MarkFailure(record.NodeID, sm.serviceCurrentHeight())
	}
	return postErr
}

                                                                              
                                                                           
                                                                             
func (sm *SenderManager) selectServiceArchiveProofHeight(record servicepeer.Record) (uint64, error) {
	if sm == nil || sm.servicePeer == nil || sm.dbManager == nil || record.ArchiveTo == 0 ||
		!sm.servicePeer.store.NeedsArchiveProof(record.NodeID) {
		return 0, nil
	}
	localFrom, localTo, err := sm.dbManager.GetBlockArchiveRange()
	if err != nil || localTo == 0 {
		return 0, nil
	}
	from := record.ArchiveFrom
	if localFrom > from {
		from = localFrom
	}
	to := record.ArchiveTo
	if localTo < to {
		to = localTo
	}
	if from == 0 || from > to {
		return 0, nil
	}
	height, err := randomServiceProofHeight(from, to)
	if err != nil {
		return 0, err
	}
	expectedID, ok := sm.finalizedBlockID(height)
	if !ok || expectedID == "" {
		return 0, nil
	}
	expected, err := sm.dbManager.GetBlockByID(expectedID)
	if err != nil || expected == nil {
		return 0, nil
	}
	return height, nil
}

                                                                              
                                                                 
func (sm *SenderManager) buildServiceProbeResponse(challenge servicepeer.Challenge) (servicepeer.ProbeResponse, error) {
	self, err := sm.cachedLocalServiceRecord()
	if err != nil {
		return servicepeer.ProbeResponse{}, err
	}
	if challenge.RequestedHeight == servicepeer.ConnectivityProbeHeight {
		response, err := servicepeer.NewSignedChallengeResponse(challenge, sm.servicePeer.chainID, sm.servicePeer.signer, false, nil)
		if err != nil {
			return servicepeer.ProbeResponse{}, err
		}
		return servicepeer.ProbeResponse{Self: self, Response: response}, nil
	}
	expectedID, ok := sm.finalizedBlockID(challenge.RequestedHeight)
	if !ok || expectedID == "" || sm.dbManager == nil {
		return servicepeer.ProbeResponse{}, fmt.Errorf("%w: height %d is not locally finalized", ErrServiceProofUnavailable, challenge.RequestedHeight)
	}
	block, err := sm.dbManager.GetBlockByID(expectedID)
	if err != nil || block == nil {
		return servicepeer.ProbeResponse{}, fmt.Errorf("%w: block %s", ErrServiceProofUnavailable, expectedID)
	}
	proofBytes, err := proto.Marshal(block)
	if err != nil || len(proofBytes) == 0 || len(proofBytes) > pb.MaxTotalSize {
		return servicepeer.ProbeResponse{}, fmt.Errorf("%w: block encoding is invalid", ErrServiceProofUnavailable)
	}
	digest := sha256.Sum256(proofBytes)
	response, err := servicepeer.NewSignedChallengeResponse(challenge, sm.servicePeer.chainID, sm.servicePeer.signer, true, digest[:])
	if err != nil {
		return servicepeer.ProbeResponse{}, err
	}
	return servicepeer.ProbeResponse{Self: self, Response: response, ProofBlockBytes: proofBytes}, nil
}

func (sm *SenderManager) validateServiceProbeResponse(challenge servicepeer.Challenge, response servicepeer.ProbeResponse) (uint64, error) {
	if err := servicepeer.VerifyChallengeResponse(challenge, response.Response, sm.servicePeer.chainID); err != nil {
		return 0, err
	}
	if challenge.RequestedHeight == servicepeer.ConnectivityProbeHeight {
		if len(response.ProofBlockBytes) != 0 {
			return 0, fmt.Errorf("connectivity-only service response included block bytes")
		}
		return 0, nil
	}
	if !response.Response.Available || len(response.ProofBlockBytes) == 0 || len(response.ProofBlockBytes) > pb.MaxTotalSize {
		return 0, fmt.Errorf("service block proof payload is unavailable or oversized")
	}
	digest := sha256.Sum256(response.ProofBlockBytes)
	if !bytes.Equal(digest[:], response.Response.PayloadDigest) {
		return 0, fmt.Errorf("service block proof payload digest mismatch")
	}
	height := challenge.RequestedHeight
	expectedID, ok := sm.finalizedBlockID(height)
	if !ok || expectedID == "" || sm.dbManager == nil {
		return 0, fmt.Errorf("%w: height %d is not locally finalized", ErrServiceProofInconclusive, height)
	}
	expected, err := sm.dbManager.GetBlockByID(expectedID)
	if err != nil || expected == nil {
		return 0, fmt.Errorf("%w: block %s", ErrServiceProofInconclusive, expectedID)
	}
	var remote pb.Block
	if err := pb.UnmarshalSanitize(response.ProofBlockBytes, &remote); err != nil {
		return 0, fmt.Errorf("service block proof payload is invalid: %w", err)
	}
	if remote.Header == nil || remote.Header.Height != height || remote.BlockHash != expectedID || !proto.Equal(expected, &remote) {
		return 0, fmt.Errorf("service block proof does not match canonical finalized block %s", expectedID)
	}
	return height, nil
}

func randomServiceProofHeight(from, to uint64) (uint64, error) {
	if from == 0 || from > to {
		return 0, fmt.Errorf("invalid service block proof range %d..%d", from, to)
	}
	span := to - from + 1
	                                                                          
	                                                                
	if span == 0 {
		for {
			nonce, err := servicepeer.NewNonce()
			if err != nil {
				return 0, err
			}
			value := binary.BigEndian.Uint64(nonce[:8])
			if value >= from {
				return value, nil
			}
		}
	}
	if span == 1 {
		return from, nil
	}
	limit := ^uint64(0) - (^uint64(0) % span)
	for {
		nonce, err := servicepeer.NewNonce()
		if err != nil {
			return 0, err
		}
		value := binary.BigEndian.Uint64(nonce[:8])
		if value < limit {
			return from + value%span, nil
		}
	}
}

func (sm *SenderManager) callServiceCallback(ctx context.Context, record servicepeer.Record) error {
	self, err := sm.cachedLocalServiceRecord()
	if err != nil {
		return err
	}
	nonce, err := servicepeer.NewNonce()
	if err != nil {
		return err
	}
	challenge, err := servicepeer.NewSignedChallenge(sm.servicePeer.signer, sm.servicePeer.chainID, record, nonce, servicepeer.ConnectivityProbeHeight)
	if err != nil {
		return err
	}
	request := servicepeer.ProbeRequest{Caller: self, Challenge: challenge}
	var response servicepeer.ProbeResponse
	if err := sm.postServiceJSON(ctx, record.Endpoint, ServicePeerCallbackPath, request, &response, servicePeerProbeResponseLimit); err != nil {
		return err
	}
	if response.Self.NodeID != record.NodeID || response.Self.Sequence != record.Sequence {
		return fmt.Errorf("service callback response record changed")
	}
	if err := servicepeer.VerifyRecord(response.Self, sm.servicePeer.chainID); err != nil {
		return err
	}
	_, err = sm.validateServiceProbeResponse(challenge, response)
	return err
}

func (sm *SenderManager) acquireInboundServiceProbe(sourceIP string) bool {
	if sm == nil || sm.servicePeer == nil || strings.TrimSpace(sourceIP) == "" {
		return false
	}
	runtime := sm.servicePeer
	key := strings.TrimSpace(sourceIP)
	runtime.probeMu.Lock()
	defer runtime.probeMu.Unlock()
	if _, active := runtime.inboundProbes[key]; active {
		return false
	}
	select {
	case runtime.inboundProbeSem <- struct{}{}:
		runtime.inboundProbes[key] = struct{}{}
		return true
	default:
		return false
	}
}

func (sm *SenderManager) releaseInboundServiceProbe(sourceIP string) {
	if sm == nil || sm.servicePeer == nil {
		return
	}
	runtime := sm.servicePeer
	key := strings.TrimSpace(sourceIP)
	runtime.probeMu.Lock()
	if _, active := runtime.inboundProbes[key]; active {
		delete(runtime.inboundProbes, key)
		<-runtime.inboundProbeSem
	}
	runtime.probeMu.Unlock()
}

func (sm *SenderManager) acquireOutboundServiceProbe(record servicepeer.Record) (string, bool) {
	if sm == nil || sm.servicePeer == nil || strings.TrimSpace(record.NodeID) == "" {
		return "", false
	}
	ip, err := servicepeer.PublicEndpointIP(record.Endpoint)
	if err != nil {
		return "", false
	}
	runtime := sm.servicePeer
	key := ip.String()
	lane := runtime.outboundCandidateProbeSem
	if current, ok := runtime.store.Get(record.NodeID); ok && current.State == servicepeer.StateVerified && current.Record.Endpoint == record.Endpoint {
		                                                                       
		                               
		lane = runtime.outboundVerifiedProbeSem
	}
	runtime.probeMu.Lock()
	defer runtime.probeMu.Unlock()
	if _, active := runtime.outboundProbes[key]; active {
		return "", false
	}
	select {
	case lane <- struct{}{}:
		runtime.outboundProbes[key] = &activeOutboundServiceProbe{
			lane:           lane,
			nodeID:         record.NodeID,
			recordSequence: record.Sequence,
		}
		return key, true
	default:
		return "", false
	}
}

func (sm *SenderManager) releaseOutboundServiceProbe(probeKey string) {
	if sm == nil || sm.servicePeer == nil {
		return
	}
	runtime := sm.servicePeer
	runtime.probeMu.Lock()
	if probe, active := runtime.outboundProbes[probeKey]; active {
		delete(runtime.outboundProbes, probeKey)
		<-probe.lane
	}
	runtime.probeMu.Unlock()
}

func (sm *SenderManager) bindOutboundServiceProbeChallenge(record servicepeer.Record) bool {
	if sm == nil || sm.servicePeer == nil {
		return false
	}
	ip, err := servicepeer.PublicEndpointIP(record.Endpoint)
	if err != nil {
		return false
	}
	runtime := sm.servicePeer
	runtime.probeMu.Lock()
	defer runtime.probeMu.Unlock()
	probe := runtime.outboundProbes[ip.String()]
	if probe == nil || probe.nodeID != record.NodeID || probe.challengeBound {
		return false
	}
	                                                                           
	                                                                            
	                                                
	probe.recordSequence = record.Sequence
	probe.challengeBound = true
	return true
}

func (sm *SenderManager) consumeExpectedServiceCallback(caller servicepeer.Record, sourceIP string) bool {
	if sm == nil || sm.servicePeer == nil {
		return false
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(sourceIP))
	if err != nil {
		return false
	}
	runtime := sm.servicePeer
	key := ip.Unmap().String()
	runtime.probeMu.Lock()
	defer runtime.probeMu.Unlock()
	probe := runtime.outboundProbes[key]
	if probe == nil || !probe.challengeBound || probe.callbackConsumed || probe.nodeID != caller.NodeID || probe.recordSequence != caller.Sequence {
		return false
	}
	probe.callbackConsumed = true
	return true
}

func (sm *SenderManager) outboundServiceCallbackConsumed(record servicepeer.Record) bool {
	if sm == nil || sm.servicePeer == nil {
		return false
	}
	ip, err := servicepeer.PublicEndpointIP(record.Endpoint)
	if err != nil {
		return false
	}
	runtime := sm.servicePeer
	runtime.probeMu.Lock()
	defer runtime.probeMu.Unlock()
	probe := runtime.outboundProbes[ip.String()]
	return probe != nil && probe.nodeID == record.NodeID && probe.recordSequence == record.Sequence && probe.callbackConsumed
}

                                                                               
                                                                              
                                                                               
func (sm *SenderManager) AcquireServiceSyncServe(sourceIP string) (func(), bool) {
	return sm.acquireArchiveServe(sourceIP, archiveServeOrdinary)
}

                                                                            
                                                                            
                                                             
func (sm *SenderManager) AcquirePrivilegedArchiveServe(sourceIP string) (func(), bool) {
	return sm.acquireArchiveServe(sourceIP, archiveServePrivileged)
}

                                                                                              
                                                                                               
func (sm *SenderManager) AcquireLiveBlockServe(sourceIP string) (func(), bool) {
	return sm.acquireArchiveServe(sourceIP, archiveServeLiveBlock)
}

                                                                              
                                                                                 
                             
func (sm *SenderManager) AcquireServiceProofServe(sourceIP string) (func(), bool) {
	return sm.acquireArchiveServe(sourceIP, archiveServeProof)
}

func (sm *SenderManager) acquireArchiveServe(sourceIP string, lane archiveServeLane) (func(), bool) {
	if sm == nil || strings.TrimSpace(sourceIP) == "" {
		return nil, false
	}
	if lane != archiveServeOrdinary && lane != archiveServePrivileged && lane != archiveServeProof && lane != archiveServeLiveBlock {
		return nil, false
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(sourceIP))
	if err != nil {
		return nil, false
	}
	sm.archiveServeInit.Do(func() {
		sm.archiveServeSem = make(chan struct{}, servicePeerSyncConcurrency)
		sm.archiveOrdinarySem = make(chan struct{}, servicePeerOrdinaryConcurrency)
		sm.archiveProofSem = make(chan struct{}, servicePeerProofConcurrency)
		sm.liveBlockServeSem = make(chan struct{}, liveBlockServeConcurrency)
		sm.archiveServeBySource = make(map[string]archiveServeLane)
	})
	key := ip.Unmap().String()
	if lane == archiveServeLiveBlock {
		key = "live:" + key
	}
	sm.archiveServeMu.Lock()
	defer sm.archiveServeMu.Unlock()
	if _, active := sm.archiveServeBySource[key]; active {
		return nil, false
	}
	if !sm.acquireArchiveLaneLocked(lane) {
		return nil, false
	}
	sm.archiveServeBySource[key] = lane
	var once sync.Once
	return func() {
		once.Do(func() {
			sm.archiveServeMu.Lock()
			if activeLane, active := sm.archiveServeBySource[key]; active {
				delete(sm.archiveServeBySource, key)
				sm.releaseArchiveLaneLocked(activeLane)
			}
			sm.archiveServeMu.Unlock()
		})
	}, true
}

func (sm *SenderManager) acquireArchiveLaneLocked(lane archiveServeLane) bool {
	if lane == archiveServeLiveBlock {
		select {
		case sm.liveBlockServeSem <- struct{}{}:
			return true
		default:
			return false
		}
	}
	if lane == archiveServeProof {
		select {
		case sm.archiveProofSem <- struct{}{}:
			return true
		default:
			return false
		}
	}
	if lane == archiveServeOrdinary {
		select {
		case sm.archiveOrdinarySem <- struct{}{}:
		default:
			return false
		}
	}
	select {
	case sm.archiveServeSem <- struct{}{}:
		return true
	default:
		if lane == archiveServeOrdinary {
			<-sm.archiveOrdinarySem
		}
		return false
	}
}

func (sm *SenderManager) releaseArchiveLaneLocked(lane archiveServeLane) {
	if lane == archiveServeLiveBlock {
		<-sm.liveBlockServeSem
		return
	}
	if lane == archiveServeProof {
		<-sm.archiveProofSem
		return
	}
	<-sm.archiveServeSem
	if lane == archiveServeOrdinary {
		<-sm.archiveOrdinarySem
	}
}

func (sm *SenderManager) VerifyServiceSignedPayload(envelope servicepeer.SignedPayload, purpose, sourceIP string) (servicepeer.Record, error) {
	if sm == nil || sm.servicePeer == nil {
		return servicepeer.Record{}, fmt.Errorf("service peer identity is unavailable")
	}
	peer, ok := sm.servicePeer.store.Get(envelope.Auth.NodeID)
	if !ok || peer.State != servicepeer.StateVerified {
		return servicepeer.Record{}, fmt.Errorf("service peer is not locally verified")
	}
	if envelope.Record.NodeID != peer.Record.NodeID || envelope.Record.Endpoint != peer.Record.Endpoint ||
		!serviceEndpointMatchesSource(envelope.Record.Endpoint, sourceIP) {
		return servicepeer.Record{}, fmt.Errorf("verified service endpoint does not match source IP")
	}
	                                                                          
	                                                                            
	                                            
	if _, err := sm.servicePeer.store.UpsertCandidate(envelope.Record); err != nil {
		return servicepeer.Record{}, err
	}
	peer, ok = sm.servicePeer.store.Get(envelope.Auth.NodeID)
	if !ok || peer.State != servicepeer.StateVerified {
		return servicepeer.Record{}, fmt.Errorf("service peer lost verified state")
	}
	if err := servicepeer.VerifyRequestAuth(envelope.Auth, peer.Record, sm.servicePeer.chainID, purpose, envelope.Payload); err != nil {
		return servicepeer.Record{}, err
	}
	if !sm.servicePeer.acceptReplay(purpose, servicepeer.RequestAuthDigest(envelope.Auth)) {
		return servicepeer.Record{}, fmt.Errorf("replayed service request")
	}
	return cloneServiceRecord(peer.Record), nil
}

func (sm *SenderManager) SignServicePayload(purpose string, payload []byte) (servicepeer.SignedPayload, error) {
	self, err := sm.LocalServiceRecord()
	if err != nil {
		return servicepeer.SignedPayload{}, err
	}
	auth, err := servicepeer.NewRequestAuth(self, purpose, payload, sm.servicePeer.signer)
	if err != nil {
		return servicepeer.SignedPayload{}, err
	}
	return servicepeer.SignedPayload{Record: self, Auth: auth, Payload: append([]byte(nil), payload...)}, nil
}

func (sm *SenderManager) IsVerifiedServicePeer(nodeID string) (servicepeer.Record, bool) {
	if sm == nil || sm.servicePeer == nil {
		return servicepeer.Record{}, false
	}
	peer, ok := sm.servicePeer.store.Get(nodeID)
	return cloneServiceRecord(peer.Record), ok && peer.State == servicepeer.StateVerified && sm.servicePeer.store.CanServeData(nodeID)
}

                                                                              
                                                                             
                                                                       
func (sm *SenderManager) VerifiedServicePeerBySourceIP(sourceIP string) (servicepeer.Record, bool) {
	if sm == nil || sm.servicePeer == nil {
		return servicepeer.Record{}, false
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(sourceIP))
	if err != nil {
		return servicepeer.Record{}, false
	}
	peer, ok := sm.servicePeer.store.VerifiedByIP(ip)
	if !ok {
		return servicepeer.Record{}, false
	}
	return cloneServiceRecord(peer.Record), true
}

                                                                               
                                                                              
func (sm *SenderManager) CanServicePeerConsumeData(nodeID string) bool {
	return sm != nil && sm.servicePeer != nil && sm.servicePeer.store.CanConsumeData(nodeID)
}

                                                                           
                                                                          
func (sm *SenderManager) CanServicePeerConsumeDataBySourceIP(sourceIP string) (servicepeer.Record, bool) {
	if sm == nil || sm.servicePeer == nil {
		return servicepeer.Record{}, false
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(sourceIP))
	if err != nil {
		return servicepeer.Record{}, false
	}
	peer, ok := sm.servicePeer.store.CanConsumeDataByIP(ip)
	if !ok {
		return servicepeer.Record{}, false
	}
	return cloneServiceRecord(peer.Record), true
}

                                                                               
                                                                          
func (sm *SenderManager) AuthorizeServicePeerDataConsumptionBySourceIP(sourceIP string) (servicepeer.Record, bool) {
	if sm == nil || sm.servicePeer == nil {
		return servicepeer.Record{}, false
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(sourceIP))
	if err != nil {
		return servicepeer.Record{}, false
	}
	peer, ok := sm.servicePeer.store.AuthorizeDataConsumptionByIP(ip)
	if !ok {
		return servicepeer.Record{}, false
	}
	return cloneServiceRecord(peer.Record), true
}

                                                                           
                                                                          
                                                                      
func (sm *SenderManager) AuthorizeServicePeerDataConsumption(nodeID string) bool {
	return sm != nil && sm.servicePeer != nil && sm.servicePeer.store.AuthorizeDataConsumption(nodeID)
}

func (sm *SenderManager) RecordServicePeerSuccess(nodeID string) {
	if sm == nil || sm.servicePeer == nil || nodeID == "" {
		return
	}
	_ = sm.servicePeer.store.MarkSuccess(nodeID, sm.serviceCurrentHeight())
}

func (sm *SenderManager) RecordServicePeerFailure(nodeID string) {
	if sm == nil || sm.servicePeer == nil || nodeID == "" {
		return
	}
	_ = sm.servicePeer.store.MarkFailure(nodeID, sm.serviceCurrentHeight())
}

func (sm *SenderManager) SampleVerifiedServicePeers(limit int, archive *servicepeer.HeightRange, exclude map[string]struct{}) []servicepeer.Peer {
	if sm == nil || sm.servicePeer == nil || limit <= 0 {
		return nil
	}
	peers, err := sm.servicePeer.store.SampleVerified(servicepeer.SampleOptions{
		Limit:          limit,
		Archive:        archive,
		ExcludeNodeIDs: exclude,
	})
	if err != nil {
		return nil
	}
	return peers
}

func (sm *SenderManager) postServiceJSON(ctx context.Context, endpoint, path string, request, response any, responseLimit int64) error {
	if sm == nil || sm.httpClient == nil {
		return fmt.Errorf("service HTTP client is unavailable")
	}
	requestBytes, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if len(requestBytes) > servicePeerExchangeBodyLimit {
		return fmt.Errorf("service request exceeds limit")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+endpoint+path, bytes.NewReader(requestBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Encoding", "gzip")
	if sm.address != "" {
		req.Header.Set("X-Node-Address", sm.address)
	}
	resp, err := sm.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &serviceHTTPStatusError{StatusCode: resp.StatusCode, Body: responseErrorText(resp, "service response")}
	}
	if response == nil {
		return nil
	}
	if responseLimit <= 0 {
		return fmt.Errorf("invalid service response limit")
	}
	responseBytes, err := readProtobufResponseBodyLimited(resp, "service response", responseLimit)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(responseBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(response); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("service response contains multiple JSON values")
		}
		return err
	}
	return nil
}

func serviceEndpointMatchesSource(endpoint, sourceIP string) bool {
	endpointIP, err := servicepeer.PublicEndpointIP(endpoint)
	if err != nil {
		return false
	}
	source, err := netip.ParseAddr(strings.TrimSpace(sourceIP))
	if err != nil {
		return false
	}
	return endpointIP == source.Unmap()
}

func cloneServiceRecord(record servicepeer.Record) servicepeer.Record {
	record.SecpPublicKey = append([]byte(nil), record.SecpPublicKey...)
	record.Signature = append([]byte(nil), record.Signature...)
	return record
}

func (sm *SenderManager) serviceCurrentHeight() uint64 {
	if sm == nil || sm.dbManager == nil {
		return 0
	}
	return sm.dbManager.GetCurrentHeight()
}

                                                                          
                                                              
func (sm *SenderManager) StartServicePeerDiscovery() {
	if sm == nil || sm.servicePeer == nil {
		return
	}
	interval := 10 * time.Second
	if sm.cfg != nil && sm.cfg.Discovery.SyncInterval > 0 {
		interval = sm.cfg.Discovery.SyncInterval
	}
	sm.servicePeer.start.Do(func() {
		sm.discoveryWg.Add(1)
		go sm.servicePeerDiscoveryLoop(interval)
		sm.Logger.Info("[ServicePeer] discovery started endpoint=%s interval=%s", sm.servicePeer.endpoint, interval)
	})
}

func (sm *SenderManager) servicePeerDiscoveryLoop(interval time.Duration) {
	defer sm.discoveryWg.Done()
	sm.runServicePeerDiscoveryRound()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-sm.servicePeer.stopCtx.Done():
			return
		case <-ticker.C:
			sm.runServicePeerDiscoveryRound()
		}
	}
}

func (sm *SenderManager) runServicePeerDiscoveryRound() {
	if sm == nil || sm.servicePeer == nil {
		return
	}
	targets := sm.servicePeerDiscoveryTargets()
	if len(targets) > servicePeerDiscoveryFanout {
		start := int(sm.discoveryRound.Add(1)-1) % len(targets)
		selected := make([]string, 0, servicePeerDiscoveryFanout)
		for i := 0; i < servicePeerDiscoveryFanout; i++ {
			selected = append(selected, targets[(start+i)%len(targets)])
		}
		targets = selected
	}
	for _, endpoint := range targets {
		if sm.servicePeerStopping() {
			return
		}
		ctx, cancel := context.WithTimeout(sm.servicePeer.stopCtx, 4*time.Second)
		err := sm.fetchServicePeerExchange(ctx, endpoint)
		cancel()
		if err != nil {
			if !isTemporaryServiceEndpointError(err) {
				sm.markVerifiedServiceEndpointFailure(endpoint)
			}
			sm.Logger.Debug("[ServicePeer] exchange with %s failed: %v", endpoint, err)
		}
	}

	                                                                           
	                                                                            
	                                                                    
	verified := sm.SampleVerifiedServicePeers(servicePeerVerifiedProbeLimit, nil, nil)
	for _, peer := range verified {
		if sm.servicePeerStopping() {
			return
		}
		ctx, cancel := context.WithTimeout(sm.servicePeer.stopCtx, 5*time.Second)
		err := sm.ProbeServiceCandidate(ctx, peer.Record)
		cancel()
		if err != nil {
			sm.Logger.Debug("[ServicePeer] verified re-probe %s failed: %v", peer.Record.NodeID, err)
		}
	}

	candidates, err := sm.servicePeer.store.SampleCandidates(servicepeer.SampleOptions{
		Limit:           servicePeerCandidateProbeLimit,
		IncludeDegraded: true,
	})
	if err != nil {
		return
	}
	for _, candidate := range candidates {
		if sm.servicePeerStopping() {
			return
		}
		ctx, cancel := context.WithTimeout(sm.servicePeer.stopCtx, 5*time.Second)
		err := sm.ProbeServiceCandidate(ctx, candidate.Record)
		cancel()
		if err != nil {
			sm.Logger.Debug("[ServicePeer] candidate probe %s failed: %v", candidate.Record.NodeID, err)
		}
	}
}

func isTemporaryServiceEndpointError(err error) bool {
	var statusErr *serviceHTTPStatusError
	return errors.As(err, &statusErr) &&
		(statusErr.StatusCode == http.StatusTooManyRequests || statusErr.StatusCode == http.StatusServiceUnavailable)
}

func (sm *SenderManager) servicePeerStopping() bool {
	if sm == nil || sm.servicePeer == nil || sm.servicePeer.stopCtx == nil {
		return true
	}
	select {
	case <-sm.servicePeer.stopCtx.Done():
		return true
	default:
		return false
	}
}

func (sm *SenderManager) markVerifiedServiceEndpointFailure(endpoint string) {
	if sm == nil || sm.servicePeer == nil || endpoint == "" {
		return
	}
	for _, peer := range sm.servicePeer.store.Snapshot() {
		if peer.State == servicepeer.StateVerified && peer.Record.Endpoint == endpoint {
			_ = sm.servicePeer.store.MarkFailure(peer.Record.NodeID, sm.serviceCurrentHeight())
			return
		}
	}
}

func (sm *SenderManager) servicePeerDiscoveryTargets() []string {
	seen := make(map[string]struct{})
	add := func(endpoint string) {
		canonical, err := servicepeer.CanonicalPublicEndpoint(strings.TrimSpace(endpoint))
		if err != nil || canonical == sm.servicePeer.endpoint {
			return
		}
		seen[canonical] = struct{}{}
	}
	if sm.cfg != nil {
		for _, endpoint := range sm.cfg.Discovery.BootstrapPeers {
			add(endpoint)
		}
	}
	for _, peer := range sm.servicePeer.store.Snapshot() {
		if peer.State == servicepeer.StateVerified {
			add(peer.Record.Endpoint)
		}
	}
	targets := make([]string, 0, len(seen))
	for endpoint := range seen {
		targets = append(targets, endpoint)
	}
	sort.Strings(targets)
	return targets
}

func (sm *SenderManager) fetchServicePeerExchange(ctx context.Context, endpoint string) error {
	self, err := sm.LocalServiceRecord()
	if err != nil {
		return err
	}
	var response servicepeer.ExchangeResponse
	if err := sm.postServiceJSON(ctx, endpoint, ServicePeerExchangePath, servicepeer.ExchangeRequest{Self: self}, &response, servicePeerExchangeBodyLimit); err != nil {
		return err
	}
	if response.Self.Endpoint != endpoint {
		return fmt.Errorf("service exchange responder endpoint %s does not match contacted endpoint %s", response.Self.Endpoint, endpoint)
	}
	if len(response.Peers) > servicePeerExchangeLimit {
		return fmt.Errorf("service exchange returned %d peers, limit %d", len(response.Peers), servicePeerExchangeLimit)
	}
	if _, err := sm.servicePeer.store.UpsertCandidate(response.Self); err != nil {
		if err == servicepeer.ErrSelfRecord || err == servicepeer.ErrStaleRecord {
			return nil
		}
		return fmt.Errorf("service exchange responder record rejected: %w", err)
	}
	responder, verified := sm.servicePeer.store.Get(response.Self.NodeID)
	if !verified || responder.State != servicepeer.StateVerified || responder.Record.Endpoint != endpoint || responder.Record.Sequence != response.Self.Sequence {
		                                                                           
		                                                                          
		                                          
		return nil
	}
	for _, record := range response.Peers {
		if _, err := sm.servicePeer.store.UpsertCandidate(record); err != nil && err != servicepeer.ErrSelfRecord && err != servicepeer.ErrStaleRecord {
			sm.Logger.Debug("[ServicePeer] ignore exchanged record %s: %v", record.NodeID, err)
		}
	}
	return nil
}

type servicePayloadMessage struct {
	path     string
	body     []byte
	onResult func(error)
}

func (sm *SenderManager) BroadcastServiceGossip(payload []byte, fanout int) int {
	if sm == nil || sm.servicePeer == nil || fanout <= 0 || len(payload) == 0 {
		return 0
	}
	envelope, err := sm.SignServicePayload(ServicePeerGossipPath, payload)
	if err != nil {
		sm.Logger.Debug("[ServicePeer] sign gossip failed: %v", err)
		return 0
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return 0
	}
	peers, err := sm.servicePeer.store.SampleVerified(servicepeer.SampleOptions{
		Limit:               fanout,
		RequireGossipAccess: true,
	})
	if err != nil {
		return 0
	}
	admitted := 0
	for _, peer := range peers {
		nodeID := peer.Record.NodeID
		                                                                      
		                                                                      
		if !sm.servicePeer.store.CanReceiveGossip(nodeID) {
			continue
		}
		task := &SendTask{
			Target: peer.Record.Endpoint,
			Message: &servicePayloadMessage{
				path: ServicePeerGossipPath,
				body: body,
				onResult: func(sendErr error) {
					if sendErr != nil {
						sm.RecordServicePeerFailure(nodeID)
						return
					}
					sm.RecordServicePeerSuccess(nodeID)
				},
			},
			MaxRetries:  0,
			SendFunc:    doSendServicePayload,
			Priority:    PriorityData,
			NodeAddress: sm.address,
		}
		if !sm.SendQueue.Enqueue(task) {
			sm.Logger.Debug("[ServicePeer] gossip to %s was not admitted", peer.Record.NodeID)
			continue
		}
		admitted++
	}
	return admitted
}

func doSendServicePayload(task *SendTask, client *http.Client) (resultErr error) {
	message, ok := task.Message.(*servicePayloadMessage)
	if !ok || message == nil || message.path == "" {
		return fmt.Errorf("invalid service payload task")
	}
	if message.onResult != nil {
		defer func() { message.onResult(resultErr) }()
	}
	req, cancel, err := newSendRequest(task, http.MethodPost, "https://"+task.Target+message.path, bytes.NewReader(message.body))
	if err != nil {
		return err
	}
	defer cancel()
	req.Header.Set("Content-Type", "application/json")
	if task.NodeAddress != "" {
		req.Header.Set("X-Node-Address", task.NodeAddress)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return responseStatusError(resp, "service payload")
	}
	return nil
}

type serviceSyncRequestMessage struct {
	body      []byte
	from      uint64
	to        uint64
	onSuccess func(*types.SyncBlocksResponse)
}

                                                                              
                                                                        
func (sm *SenderManager) SendServiceSyncBlocksRequest(nodeID, endpoint string, fromHeight, toHeight uint64, onSuccess func(*types.SyncBlocksResponse), onFailure func(error)) error {
	if sm == nil || sm.servicePeer == nil {
		return fmt.Errorf("service sync provider is unavailable")
	}
	record, ok := sm.servicePeer.store.CanProvideArchiveRange(nodeID, servicepeer.HeightRange{From: fromHeight, To: toHeight})
	if !ok || record.Endpoint != endpoint {
		return fmt.Errorf("service sync provider is not verified for requested range")
	}
	payload, err := json.Marshal(types.SyncBlocksRequest{FromHeight: fromHeight, ToHeight: toHeight})
	if err != nil {
		return err
	}
	envelope, err := sm.SignServicePayload(ServicePeerSyncPath, payload)
	if err != nil {
		return err
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	task := &SendTask{
		Target:      endpoint,
		Message:     &serviceSyncRequestMessage{body: body, from: fromHeight, to: toHeight, onSuccess: onSuccess},
		MaxRetries:  0,
		SendFunc:    doSendServiceSyncRequest,
		Priority:    PriorityControl,
		NodeAddress: sm.address,
		Timeout:     servicePeerSyncRequestTimeout,
		OnFailure:   onFailure,
	}
	if !sm.SendQueue.Enqueue(task) {
		return fmt.Errorf("service sync request to %s was not admitted to send queue", nodeID)
	}
	return nil
}

func doSendServiceSyncRequest(task *SendTask, client *http.Client) error {
	message, ok := task.Message.(*serviceSyncRequestMessage)
	if !ok || message == nil {
		return fmt.Errorf("invalid service sync task")
	}
	req, cancel, err := newSendRequest(task, http.MethodPost, "https://"+task.Target+ServicePeerSyncPath, bytes.NewReader(message.body))
	if err != nil {
		return err
	}
	defer cancel()
	req.Header.Set("Content-Type", "application/json")
	if task.NodeAddress != "" {
		req.Header.Set("X-Node-Address", task.NodeAddress)
	}
	                                                                             
	                                                                             
	                                                                           
	syncClient := serviceSyncHTTPClient(client)
	resp, err := syncClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return responseStatusError(resp, "service sync")
	}
	syncResponse, err := decodeSyncHTTPResponse(resp, message.from, message.to)
	if err != nil {
		return err
	}
	if syncResponse.Error != "" {
		return fmt.Errorf("service sync remote error: %s", syncResponse.Error)
	}
	if len(syncResponse.Blocks) == 0 {
		return fmt.Errorf("service sync returned no blocks")
	}
	if message.onSuccess != nil {
		message.onSuccess(syncResponse)
	}
	return nil
}

func serviceSyncHTTPClient(client *http.Client) *http.Client {
	if client == nil {
		return &http.Client{Timeout: servicePeerSyncRequestTimeout}
	}
	clone := *client
	if clone.Timeout == 0 || clone.Timeout < servicePeerSyncRequestTimeout {
		clone.Timeout = servicePeerSyncRequestTimeout
	}
	return &clone
}
