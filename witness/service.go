                     
                                                                                                            
                                                                                                                  
package witness

import (
	"context"
	"dex/blocktrace"
	frostchain "dex/frost/chain"
	"dex/logs"
	"dex/pb"
	"dex/protocol"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"
)

                                                     
            
                           
                  
                               
                  
                                                                                          
type Service struct {
	mu     sync.RWMutex
	config *Config

	            
	selector         *Selector
	voteManager      *VoteManager
	stakeManager     *StakeManager
	challengeManager *ChallengeManager

	                                                                  
	requests        map[string]*pb.RechargeRequest                        
	votingDue       map[uint64]map[string]struct{}                                
	votingDeadline  map[string]uint64                                            
	shelvedRequests map[string]struct{}                                    

	                     
	currentHeight uint64

	               
	eventChan chan *Event

	            
	ctx    context.Context
	cancel context.CancelFunc
}

type witnessVotingTimeoutStats struct {
	Due             int
	Processed       int
	CollectLockWait time.Duration
	ProcessLockWait time.Duration
}

type witnessRetryShelvedRequestStats struct {
	Scanned     int
	Stale       int
	NonShelved  int
	Rejected    int
	Scheduled   int
	CoolingDown int
	NoCandidate int
	LockWait    time.Duration
}

                                                              
                                                                         
func NewService(config *Config) *Service {
	if config == nil {
		config = DefaultConfig()
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Service{
		config:           config,
		selector:         NewSelector(config),
		voteManager:      NewVoteManager(config),
		stakeManager:     NewStakeManager(config),
		challengeManager: NewChallengeManager(config),
		requests:         make(map[string]*pb.RechargeRequest),
		votingDue:        make(map[uint64]map[string]struct{}),
		votingDeadline:   make(map[string]uint64),
		shelvedRequests:  make(map[string]struct{}),
		eventChan:        make(chan *Event, 10000),
		ctx:              ctx,
		cancel:           cancel,
	}
}

                     
func (s *Service) Start() error {
	                     
	go s.eventLoop()

	return nil
}

                    
func (s *Service) Stop() {
	s.cancel()
}

                                            
func (s *Service) SetCurrentHeight(height uint64) {
	                                                                      
	                                                                                       
	                                                                                                                  
	                                                                                                                       
	                                                                                                 

	setAt := time.Now()
	lockWaitAt := time.Now()
	s.mu.Lock()
	lockWait := time.Since(lockWaitAt)
	s.currentHeight = height
	retryEvery := uint64(0)
	if s.config != nil {
		retryEvery = s.config.RetryIntervalBlocks
	}
	retryBoundary := retryEvery > 0 && height%retryEvery == 0
	s.mu.Unlock()
	blocktrace.Record(height, "", "WitnessService.setCurrentHeight", time.Since(setAt),
		blocktrace.F("lockWait", lockWait), blocktrace.F("retryEvery", retryEvery), blocktrace.F("retryBoundary", retryBoundary))

	                     
	voteAt := time.Now()
	voteStats := s.checkVotingTimeout()
	blocktrace.Record(height, "", "WitnessService.checkVotingTimeout", time.Since(voteAt),
		blocktrace.F("due", voteStats.Due), blocktrace.F("processed", voteStats.Processed),
		blocktrace.F("collectLockWait", voteStats.CollectLockWait), blocktrace.F("processLockWait", voteStats.ProcessLockWait))

	                                                                                     
	                                                                                               

	                     
	                                       
	                                             
	if retryBoundary {
		reqAt := time.Now()
		reqStats := s.retryShelvedRequests()
		blocktrace.Record(height, "", "WitnessService.retryShelvedRequests", time.Since(reqAt),
			blocktrace.F("scanned", reqStats.Scanned), blocktrace.F("stale", reqStats.Stale),
			blocktrace.F("nonShelved", reqStats.NonShelved), blocktrace.F("rejected", reqStats.Rejected),
			blocktrace.F("scheduled", reqStats.Scheduled), blocktrace.F("coolingDown", reqStats.CoolingDown),
			blocktrace.F("noCandidate", reqStats.NoCandidate),
			blocktrace.F("lockWait", reqStats.LockWait))
	}
}

                                            
func (s *Service) GetCurrentHeight() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentHeight
}

                                                                                                                        
func (s *Service) sortedRequestIDsLocked() []string {
	ids := make([]string, 0, len(s.requests))
	for requestID := range s.requests {
		ids = append(ids, requestID)
	}
	sort.Strings(ids)
	return ids
}

                                                                                
func terminalRechargeStatus(status pb.RechargeRequestStatus) bool {
	return status == pb.RechargeRequestStatus_RECHARGE_FINALIZED || status == pb.RechargeRequestStatus_RECHARGE_REJECTED
}

func (s *Service) clearRequestIndexesLocked(requestID string) {
	if deadline, ok := s.votingDeadline[requestID]; ok {
		delete(s.votingDeadline, requestID)
		if ids := s.votingDue[deadline]; ids != nil {
			delete(ids, requestID)
			if len(ids) == 0 {
				delete(s.votingDue, deadline)
			}
		}
	}
	delete(s.shelvedRequests, requestID)
}

func (s *Service) indexRequestLocked(req *pb.RechargeRequest) {
	if req == nil || req.RequestId == "" || terminalRechargeStatus(req.Status) {
		return
	}
	switch req.Status {
	case pb.RechargeRequestStatus_RECHARGE_VOTING:
		if s.votingDue[req.DeadlineHeight] == nil {
			s.votingDue[req.DeadlineHeight] = make(map[string]struct{})
		}
		s.votingDue[req.DeadlineHeight][req.RequestId] = struct{}{}
		s.votingDeadline[req.RequestId] = req.DeadlineHeight
	case pb.RechargeRequestStatus_RECHARGE_SHELVED:
		s.shelvedRequests[req.RequestId] = struct{}{}
	}
}

func (s *Service) setRequestLocked(req *pb.RechargeRequest) {
	if req == nil || req.RequestId == "" {
		return
	}
	if s.votingDue == nil {
		s.votingDue = make(map[uint64]map[string]struct{})
	}
	if s.votingDeadline == nil {
		s.votingDeadline = make(map[string]uint64)
	}
	if s.shelvedRequests == nil {
		s.shelvedRequests = make(map[string]struct{})
	}
	if terminalRechargeStatus(req.Status) {
		s.removeRequestLocked(req.RequestId)
		return
	}
	s.clearRequestIndexesLocked(req.RequestId)
	s.requests[req.RequestId] = req
	s.indexRequestLocked(req)
}

func (s *Service) removeRequestLocked(requestID string) {
	s.clearRequestIndexesLocked(requestID)
	delete(s.requests, requestID)
	s.voteManager.ClearRequest(requestID)
}

func (s *Service) dueVotingRequestIDsLocked(height uint64) []string {
	ids := make([]string, 0)
	for deadline, bucket := range s.votingDue {
		if deadline > height {
			continue
		}
		for requestID := range bucket {
			ids = append(ids, requestID)
		}
	}
	sort.Strings(ids)
	return ids
}

func (s *Service) shelvedRequestIDsLocked() []string {
	ids := make([]string, 0, len(s.shelvedRequests))
	for requestID := range s.shelvedRequests {
		ids = append(ids, requestID)
	}
	sort.Strings(ids)
	return ids
}

                                                
                                                                                       
func (s *Service) retryShelvedRequests() witnessRetryShelvedRequestStats {
	stats := witnessRetryShelvedRequestStats{}
	lockWaitAt := time.Now()
	s.mu.Lock()
	stats.LockWait = time.Since(lockWaitAt)
	defer s.mu.Unlock()

	requestIDs := s.shelvedRequestIDsLocked()
	stats.Scanned = len(requestIDs)
	for _, requestID := range requestIDs {
		request := s.requests[requestID]
		if request == nil {
			s.clearRequestIndexesLocked(requestID)
			stats.Stale++
			continue
		}
		if request.Status != pb.RechargeRequestStatus_RECHARGE_SHELVED {
			s.clearRequestIndexesLocked(requestID)
			stats.NonShelved++
			continue
		}

		                                                                                       
		if s.config.MaxShelveWaitBlocks > 0 && request.ShelveHeight > 0 &&
			s.currentHeight > request.ShelveHeight+s.config.MaxShelveWaitBlocks {
			request.Status = pb.RechargeRequestStatus_RECHARGE_REJECTED
			for _, w := range request.SelectedWitnesses {
				_ = s.stakeManager.RemovePendingTask(w, request.RequestId)
			}
			s.emitEvent(&Event{
				Type:      EventRechargeRejected,
				RequestID: request.RequestId,
				Data:      request,
				Height:    s.currentHeight,
			})
			s.removeRequestLocked(request.RequestId)
			stats.Rejected++
			continue
		}

		                                                                                  
		                                                                                       
		                                                                          
		cooldownBase := request.DeadlineHeight
		if cooldownBase == 0 {
			cooldownBase = request.ShelveHeight
		}
		if s.config.RetryIntervalBlocks > 0 &&
			s.currentHeight < addBlockHeightSaturating(cooldownBase, s.config.RetryIntervalBlocks) {
			stats.CoolingDown++
			continue
		}

		                                                                                   
		                                                                                 
		                                                                                                      
		candidates := s.stakeManager.GetWitnessCandidates()
		active := uint32(0)
		for _, candidate := range candidates {
			if candidate != nil && candidate.IsActive {
				active++
			}
		}
		if active < s.MinSelectedWitnessCount() {
			stats.NoCandidate++
			continue
		}

		s.scheduleRechargeReselectionLocked(request, s.currentHeight)
		stats.Scheduled++
	}
	return stats
}

                                                                                                  
func (s *Service) canForceFinalize(challenge *pb.ChallengeRecord) bool {
	if challenge == nil || challenge.Finalized || challenge.Settled || challenge.Status != pb.ChallengeStatus_CHALLENGE_ACTIVE {
		return false
	}
	result := arbitrationConsensus(challenge)
	return result == ConsensusPass || result == ConsensusFail
}

                                              
func (s *Service) checkVotingTimeout() witnessVotingTimeoutStats {
	stats := witnessVotingTimeoutStats{}
	lockWaitAt := time.Now()
	s.mu.Lock()
	stats.CollectLockWait = time.Since(lockWaitAt)
	                                                                     
	requests := make([]*pb.RechargeRequest, 0)
	currentHeight := s.currentHeight
	for _, requestID := range s.dueVotingRequestIDsLocked(currentHeight) {
		req := s.requests[requestID]
		if req != nil && req.Status == pb.RechargeRequestStatus_RECHARGE_VOTING {
			requests = append(requests, req)
		}
	}
	stats.Due = len(requests)
	s.mu.Unlock()

	for _, req := range requests {
		if currentHeight >= req.DeadlineHeight {
			lockWaitAt := time.Now()
			s.mu.Lock()
			stats.ProcessLockWait += time.Since(lockWaitAt)
			                                                   
			current := s.requests[req.RequestId]
			if current != nil && current.Status == pb.RechargeRequestStatus_RECHARGE_VOTING {
				s.checkAndProcessConsensus(current, currentHeight, true)
				stats.Processed++
			}
			s.mu.Unlock()
		}
	}
	return stats
}

                                                         

                                                             
                                                                             
func (s *Service) ProcessStake(address string, amount decimal.Decimal) (*pb.WitnessInfo, error) {
	         
	if err := s.stakeManager.ValidateStake(address, amount); err != nil {
		return nil, err
	}

	                                          
	info, err := s.stakeManager.ProcessStake(address, amount, s.GetCurrentHeight())
	if err != nil {
		return nil, err
	}

	return info, nil
}

                                                                  
                                                                             
func (s *Service) ProcessUnstake(address string) (*pb.WitnessInfo, error) {
	         
	if err := s.stakeManager.ValidateUnstake(address); err != nil {
		return nil, err
	}

	                                             
	info, err := s.stakeManager.ProcessUnstake(address, s.GetCurrentHeight())
	if err != nil {
		return nil, err
	}

	return info, nil
}

                                                               

                                                                                                                                      
func (s *Service) CreateRechargeRequest(tx *pb.WitnessRequestTx) (*pb.RechargeRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	requestID := tx.Base.TxId

	                        
	if old, exists := s.requests[requestID]; exists {
		return old, nil                                                                                 
	}

	                  
	if !IsSupportedChain(tx.NativeChain) {
		return nil, ErrNativeChainNotSupport
	}
	nativeChain := frostchain.NormalizeChain(tx.NativeChain)

	                                                                                                               
	height := s.currentHeight
	if tx.Base != nil && tx.Base.ExecutedHeight > 0 {
		height = tx.Base.ExecutedHeight
	}

	                                                                                                                      
	                                                                                    
	request := &pb.RechargeRequest{
		RequestId:         requestID,
		NativeChain:       nativeChain,
		NativeTxHash:      tx.NativeTxHash,
		NativeVout:        tx.NativeVout,
		NativeScript:      tx.NativeScript,
		Amount:            tx.Amount,
		ReceiverAddress:   tx.ReceiverAddress,
		RequesterAddress:  tx.Base.FromAddress,
		Status:            pb.RechargeRequestStatus_RECHARGE_PENDING_SELECTION,
		CreateHeight:      height,
		SelectionHeight:   height + s.config.SelectionBeaconDepth,
		Round:             0,
		RechargeFee:       tx.RechargeFee,
		NativeContract:    tx.NativeContract,
		VaultId:           tx.VaultId,
		DepositEventIndex: tx.DepositEventIndex,
		DepositProtocol:   tx.DepositProtocol,
	}
	request.EconomicsPolicy = protocol.RechargeEconomicsLegacy
	if protocol.WitnessRechargeEconomicsActive(tx.Base.ChainId) {
		request.EconomicsPolicy = protocol.RechargeEconomicsLaunchV1
	}

	                                                      
	s.setRequestLocked(request)

	               
	s.emitEvent(&Event{
		Type:      EventRechargeRequested,
		RequestID: requestID,
		Data:      request,
		Height:    height,
	})

	return request, nil
}

                                    
func (s *Service) ProcessVote(vote *pb.WitnessVote) (*pb.RechargeRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.processVoteLocked(vote, s.currentHeight)
}

                                                                               
                                                                        
                                                                                
func (s *Service) ProcessVoteAtHeight(vote *pb.WitnessVote, executionHeight uint64) (*pb.RechargeRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.processVoteLocked(vote, executionHeight)
}

func (s *Service) processVoteLocked(vote *pb.WitnessVote, executionHeight uint64) (*pb.RechargeRequest, error) {
	if vote == nil {
		logs.Warn("[Witness][ProcessVoteReject] nil_vote")
		return nil, fmt.Errorf("nil vote")
	}

	requestID := vote.RequestId
	witnessAddr := vote.WitnessAddress

	request, exists := s.requests[requestID]
	if !exists {
		logs.Warn(
			"[Witness][ProcessVoteReject] request_not_found request=%s witness=%s tx=%s active_requests=%d",
			requestID, witnessAddr, vote.TxId, len(s.requests),
		)
		return nil, ErrRequestNotFound
	}

	if !requestAcceptsVoteAtHeight(request, executionHeight) {
		logs.Info(
			"[Witness][ProcessVoteReject] request_not_accepting request=%s witness=%s tx=%s status=%s height=%d accept_until=%d",
			requestID, witnessAddr, vote.TxId, request.Status.String(), executionHeight, request.VoteAcceptUntilHeight,
		)
		return nil, ErrVotingPeriodExpired
	}
	if vote.Round != request.Round {
		logs.Info(
			"[Witness][ProcessVoteReject] round_mismatch request=%s witness=%s tx=%s vote_round=%d request_round=%d",
			requestID, witnessAddr, vote.TxId, vote.Round, request.Round,
		)
		return nil, ErrWitnessVoteRoundMismatch
	}

	                                          
	if !s.voteManager.IsSelectedWitness(requestID, witnessAddr) {
		logs.Warn(
			"[Witness][ProcessVoteReject] not_selected request=%s witness=%s tx=%s status=%s round=%d req_selected=%v mem_selected=%v",
			requestID,
			witnessAddr,
			vote.TxId,
			request.Status.String(),
			request.Round,
			request.SelectedWitnesses,
			s.voteManager.GetSelectedWitnesses(requestID),
		)
		return nil, ErrNotSelectedWitness
	}

	                        
	if s.voteManager.HasVoted(requestID, witnessAddr) {
		logs.Info(
			"[Witness][ProcessVoteSkip] duplicate request=%s witness=%s tx=%s status=%s round=%d",
			requestID, witnessAddr, vote.TxId, request.Status.String(), request.Round,
		)
		return request, nil                
	}

	               
	if err := s.voteManager.AddVote(vote); err != nil {
		if err == ErrDuplicateVote {
			logs.Info(
				"[Witness][ProcessVoteSkip] duplicate_add request=%s witness=%s tx=%s status=%s round=%d",
				requestID, witnessAddr, vote.TxId, request.Status.String(), request.Round,
			)
			return request, nil                
		}
		logs.Warn(
			"[Witness][ProcessVoteReject] add_vote_failed request=%s witness=%s tx=%s status=%s round=%d err=%v",
			requestID, witnessAddr, vote.TxId, request.Status.String(), request.Round, err,
		)
		return nil, err
	}

	               
	switch vote.VoteType {
	case pb.WitnessVoteType_VOTE_PASS:
		request.PassCount++
	case pb.WitnessVoteType_VOTE_FAIL:
		request.FailCount++
	case pb.WitnessVoteType_VOTE_ABSTAIN:
		request.AbstainCount++
	}

	request.Votes = append(request.Votes, vote)
	request.OriginalPositions = LastDirectionalPositions(request.OriginalPositions, vote)

	                        
	s.stakeManager.UpdateWitnessStats(witnessAddr, vote.VoteType)

	                                                                        
	                                                                             
	if request.Status == pb.RechargeRequestStatus_RECHARGE_VOTING {
		s.checkAndProcessConsensus(request, executionHeight, false)
	}

	return request, nil
}

                                                 
func (s *Service) checkAndProcessConsensus(request *pb.RechargeRequest, height uint64, emitGraceEvent bool) {
	isDeadline := height >= request.DeadlineHeight
	isAllVoted := s.voteManager.IsAllVoted(request.RequestId)

	                                       
	if !s.voteManager.CanReachConsensus(request.RequestId) {
		s.expandScope(request, height)
		return
	}

	result := s.voteManager.CheckConsensus(request.RequestId, isDeadline || isAllVoted)
	                                                                         
	                                                                             
	                                                                           
	                                                                            
	                                                                              
	                                                                             
	if emitGraceEvent && isDeadline && result == ConsensusNone {
		result = ConsensusExpand
	}

	switch result {
	case ConsensusPass:
		if s.voteGraceEnabled() {
			s.lockConsensusDecision(request, result, height, emitGraceEvent)
		} else {
			s.handleConsensusPass(request, height)
		}
	case ConsensusFail:
		if s.voteGraceEnabled() {
			s.lockConsensusDecision(request, result, height, emitGraceEvent)
		} else {
			s.handleConsensusFail(request, height)
		}
	case ConsensusExpand:
		s.expandScope(request, height)
	case ConsensusConflict:
		s.handleConflict(request, height)
	}
}

func (s *Service) voteGraceEnabled() bool {
	return s.config != nil && s.config.VoteGracePeriodBlocks > 0
}

func requestAcceptsVoteAtHeight(request *pb.RechargeRequest, height uint64) bool {
	if request == nil {
		return false
	}
	switch request.Status {
	case pb.RechargeRequestStatus_RECHARGE_VOTING:
		return request.DeadlineHeight == 0 || height <= request.DeadlineHeight
	case pb.RechargeRequestStatus_RECHARGE_CONSENSUS_PASS,
		pb.RechargeRequestStatus_RECHARGE_CONSENSUS_FAIL:
		return request.VoteAcceptUntilHeight > 0 && height <= request.VoteAcceptUntilHeight
	default:
		return false
	}
}

func addBlockHeightSaturating(height, blocks uint64) uint64 {
	if blocks > math.MaxUint64-height {
		return math.MaxUint64
	}
	return height + blocks
}

func (s *Service) lockConsensusDecision(request *pb.RechargeRequest, result ConsensusResult, height uint64, emit bool) {
	acceptUntil := addBlockHeightSaturating(height, s.config.VoteGracePeriodBlocks)
	if request.DeadlineHeight > 0 && request.DeadlineHeight < acceptUntil {
		acceptUntil = request.DeadlineHeight
	}

	request.DecisionHeight = height
	request.VoteAcceptUntilHeight = acceptUntil
	switch result {
	case ConsensusPass:
		request.Status = pb.RechargeRequestStatus_RECHARGE_CONSENSUS_PASS
	case ConsensusFail:
		request.Status = pb.RechargeRequestStatus_RECHARGE_CONSENSUS_FAIL
	default:
		return
	}
	s.setRequestLocked(request)

	                                                                        
	                                                     
	for _, w := range request.SelectedWitnesses {
		_ = s.stakeManager.RemovePendingTask(w, request.RequestId)
	}

	if emit {
		eventType := EventConsensusReached
		if result == ConsensusFail {
			eventType = EventConsensusFailed
		}
		s.emitEvent(&Event{
			Type:      eventType,
			RequestID: request.RequestId,
			Data:      request,
			Height:    height,
		})
	}
}

                                         
func (s *Service) handleConsensusPass(request *pb.RechargeRequest, height uint64) {
	                           
	                                                                                                         
	request.Status = pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD
	request.DeadlineHeight = addBlockHeightSaturating(height, s.config.ChallengePeriodBlocks)
	s.setRequestLocked(request)

	                                    
	for _, w := range request.SelectedWitnesses {
		_ = s.stakeManager.RemovePendingTask(w, request.RequestId)
	}

	s.emitEvent(&Event{
		Type:      EventChallengePeriodStart,
		RequestID: request.RequestId,
		Data:      request,
		Height:    height,
	})
}

                                         
func (s *Service) handleConsensusFail(request *pb.RechargeRequest, height uint64) {
	request.Status = pb.RechargeRequestStatus_RECHARGE_REJECTED

	                                    
	for _, w := range request.SelectedWitnesses {
		_ = s.stakeManager.RemovePendingTask(w, request.RequestId)
	}

	s.emitEvent(&Event{
		Type:      EventRechargeRejected,
		RequestID: request.RequestId,
		Data:      request,
		Height:    height,
	})
	s.removeRequestLocked(request.RequestId)
}

                                                                                     
                                                                                                   
func (s *Service) scheduleRechargeReselectionLocked(request *pb.RechargeRequest, height uint64) {
	previousWitnesses := append([]string(nil), request.SelectedWitnesses...)
	request.Status = pb.RechargeRequestStatus_RECHARGE_PENDING_SELECTION
	request.Round++
	request.SelectionHeight = addBlockHeightSaturating(height, s.config.SelectionBeaconDepth)
	request.SelectionBeacon = nil
	request.DecisionHeight = 0
	request.VoteAcceptUntilHeight = 0
	request.PassCount = 0
	request.FailCount = 0
	request.AbstainCount = 0
	request.Votes = nil
	if request.ShelveHeight == 0 {
		request.ShelveHeight = height
	}
	s.setRequestLocked(request)
	for _, w := range previousWitnesses {
		_ = s.stakeManager.RemovePendingTask(w, request.RequestId)
	}
	s.voteManager.ClearRequest(request.RequestId)
	s.emitEvent(&Event{
		Type:      EventRechargeReselectionScheduled,
		RequestID: request.RequestId,
		Data:      request,
		Height:    height,
	})
}

                                                                                                               
func (s *Service) expandScope(request *pb.RechargeRequest, height uint64) {
	s.scheduleRechargeReselectionLocked(request, height)
}

                                                            
func (s *Service) handleConflict(request *pb.RechargeRequest, height uint64) {
	request.Status = pb.RechargeRequestStatus_RECHARGE_CHALLENGED
	request.ChallengeId = "auto_" + request.RequestId
	ch := &pb.ChallengeRecord{ChallengeId: request.ChallengeId, RequestId: request.RequestId,
		ChallengerAddress: "system", StakeAmount: "0", RequiredStake: "0", CreateHeight: height,
		Reason:                  "automatic review of conflicting witness evidence",
		Status:                  pb.ChallengeStatus_CHALLENGE_PENDING_SELECTION,
		ArbitrationPeriodBlocks: s.ArbitrationPeriodBlocks(),
		SelectionHeight:         addBlockHeightSaturating(height, s.config.SelectionBeaconDepth)}
	if err := PrepareArbitrationRecord(ch, request, s.config); err != nil {
		                                                                         
		panic(err)
	}
	s.challengeManager.LoadChallenge(ch)
	s.setRequestLocked(request)
	s.emitEvent(&Event{Type: EventChallengeInitiated, RequestID: request.RequestId, Data: ch, Height: height})
	s.emitEvent(&Event{Type: EventRechargeReselectionScheduled, RequestID: request.RequestId, Data: request, Height: height})
}

                                                   
func (s *Service) checkChallengePeriod() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, requestID := range s.sortedRequestIDsLocked() {
		request := s.requests[requestID]
		if request == nil {
			continue
		}
		if request.Status != pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD {
			continue
		}

		                     
		if s.currentHeight > request.DeadlineHeight {
			if s.hasActivatedChallengeLocked(request) {
				request.Status = pb.RechargeRequestStatus_RECHARGE_CHALLENGED
				s.setRequestLocked(request)
				continue
			}

			                                             
			                                                                                                       
			                                                                                               
			                                                                                                          
			request.FinalizeHeight = s.currentHeight

			                                                                                             

			s.emitEvent(&Event{
				Type:      EventRechargeFinalized,
				RequestID: request.RequestId,
				Data:      request,
				Height:    s.currentHeight,
			})
		}
	}
}

func (s *Service) hasActivatedChallengeLocked(request *pb.RechargeRequest) bool {
	if request == nil || request.ChallengeId == "" {
		return false
	}
	challenge, err := s.challengeManager.GetChallenge(request.ChallengeId)
	if err != nil || challenge == nil {
		return false
	}
	if challenge.Finalized || challenge.Status == pb.ChallengeStatus_CHALLENGE_FINALIZED {
		return false
	}
	return challenge.SelectionHeight > 0
}

                                                                         
                                                                                                
func (s *Service) handleChallengeResolved(record *pb.ChallengeRecord) {
	if record != nil && record.Settled {
		s.SyncCommittedChallenge(record)
	}
}

                                                                        
                                                                                                                   
type ArbitrationVoteOutcome struct {
	Challenge         *pb.ChallengeRecord
	Request           *pb.RechargeRequest
	RewardedWitnesses []*pb.WitnessInfo
	SlashedWitnesses  []*pb.WitnessInfo
	SlashAmounts      map[string]string
	ChallengerAddr    string
	ChallengerRefund  string
	ChallengerRefunds map[string]string
}

                                                                                  
                                                                                
                               
func (s *Service) ComputeArbitrationVoteOutcome(ch *pb.ChallengeRecord, req *pb.RechargeRequest, vote *pb.WitnessVote, witnesses map[string]*pb.WitnessInfo, height uint64) (*ArbitrationVoteOutcome, error) {
	if vote == nil {
		return nil, ErrInvalidVote
	}
	return ComputeArbitrationBallotOutcome(ch, req, vote, vote.BallotRound, height)
}

                                                                                                     
                                                                                                       
func (s *Service) ProcessArbitrationVote(challengeID string, vote *pb.WitnessVote) (*ArbitrationVoteOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if vote == nil {
		return nil, ErrInvalidVote
	}
	challenge, err := s.challengeManager.GetChallenge(challengeID)
	if err != nil {
		return nil, err
	}
	request := s.requests[challenge.RequestId]
	if request == nil {
		return nil, ErrRequestNotFound
	}
	outcome, err := ComputeArbitrationBallotOutcome(challenge, request, vote, vote.BallotRound, s.currentHeight)
	if err != nil {
		return nil, err
	}
	s.challengeManager.LoadChallenge(outcome.Challenge)
	return outcome, nil
}

                            
func (s *Service) Events() <-chan *Event {
	return s.eventChan
}

                               
func (s *Service) eventLoop() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case event := <-s.stakeManager.Events():
			s.emitEvent(event)
		case event := <-s.voteManager.Events():
			s.emitEvent(event)
		case event := <-s.challengeManager.Events():
			if event.Type == EventChallengeResolved {
				if record, ok := event.Data.(*pb.ChallengeRecord); ok {
					s.handleChallengeResolved(record)
				}
			}
			s.emitEvent(event)
		}
	}
}

                         
func (s *Service) emitEvent(event *Event) {
	event = cloneEvent(event)
	if event == nil {
		return
	}
	event.Timestamp = time.Now().Unix()
	select {
	case s.eventChan <- event:
	default:
	}
}

func cloneEvent(event *Event) *Event {
	if event == nil {
		return nil
	}
	cloned := *event
	switch data := event.Data.(type) {
	case *pb.RechargeRequest:
		if data != nil {
			cloned.Data = proto.Clone(data).(*pb.RechargeRequest)
		}
	case *pb.ChallengeRecord:
		if data != nil {
			cloned.Data = proto.Clone(data).(*pb.ChallengeRecord)
		}
	case *pb.WitnessInfo:
		if data != nil {
			cloned.Data = proto.Clone(data).(*pb.WitnessInfo)
		}
	}
	return &cloned
}

func drainEventChan(ch chan *Event) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

                                
func (s *Service) GetRequest(requestID string) (*pb.RechargeRequest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	request, exists := s.requests[requestID]
	if !exists {
		return nil, ErrRequestNotFound
	}
	return request, nil
}

                                                      
func (s *Service) GetActiveRequests() []*pb.RechargeRequest {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*pb.RechargeRequest, 0, len(s.requests))
	for _, requestID := range s.sortedRequestIDsLocked() {
		if req := s.requests[requestID]; req != nil {
			result = append(result, req)
		}
	}
	return result
}

                                                                                        
func (s *Service) MarkRequestChallenged(requestID, challengeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if req, ok := s.requests[requestID]; ok {
		req.Status = pb.RechargeRequestStatus_RECHARGE_CHALLENGED
		req.ChallengeId = challengeID
		s.setRequestLocked(req)
	}
}

                                       
func (s *Service) GetWitnessInfo(address string) (*pb.WitnessInfo, error) {
	return s.stakeManager.GetWitness(address)
}

                                                   
                                                                                  
func (s *Service) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	                     
	s.requests = make(map[string]*pb.RechargeRequest)
	s.votingDue = make(map[uint64]map[string]struct{})
	s.votingDeadline = make(map[string]uint64)
	s.shelvedRequests = make(map[string]struct{})
	s.stakeManager.Reset()
	s.voteManager.Reset()
	s.challengeManager.Reset()
	drainEventChan(s.eventChan)
	drainEventChan(s.stakeManager.eventChan)
	drainEventChan(s.voteManager.eventChan)
	drainEventChan(s.challengeManager.eventChan)
}

                                                                            
func (s *Service) LoadWitness(info *pb.WitnessInfo) {
	s.stakeManager.LoadWitness(info)
}

                                                                         
func (s *Service) LoadRequest(request *pb.RechargeRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if request == nil {
		logs.Warn("[Witness][LoadRequest] nil_request")
		return
	}
	if terminalRechargeStatus(request.Status) {
		s.removeRequestLocked(request.RequestId)
		return
	}
	s.setRequestLocked(request)
	s.voteManager.ClearRequest(request.RequestId)
	s.voteManager.SetSelectedWitnesses(request.RequestId, request.SelectedWitnesses)
	for _, v := range request.Votes {
		_ = s.voteManager.AddVote(v)
	}
	logs.Info(
		"[Witness][LoadRequest] request=%s status=%s round=%d selected=%v votes=%d",
		request.RequestId,
		request.Status.String(),
		request.Round,
		request.SelectedWitnesses,
		len(request.Votes),
	)
}

                                                                              
                                                            
func (s *Service) SyncCommittedRequest(request *pb.RechargeRequest) {
	if request == nil || request.RequestId == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if terminalRechargeStatus(request.Status) {
		addresses := make(map[string]struct{}, len(request.SelectedWitnesses)+len(request.Votes))
		for _, addr := range request.SelectedWitnesses {
			if addr != "" {
				addresses[addr] = struct{}{}
			}
		}
		for _, vote := range request.Votes {
			if vote != nil && vote.WitnessAddress != "" {
				addresses[vote.WitnessAddress] = struct{}{}
			}
		}
		for addr := range addresses {
			_ = s.stakeManager.RemovePendingTask(addr, request.RequestId)
		}
		s.removeRequestLocked(request.RequestId)
		return
	}
	s.setRequestLocked(request)
	s.voteManager.ClearRequest(request.RequestId)
	s.voteManager.SetSelectedWitnesses(request.RequestId, request.SelectedWitnesses)
	for _, vote := range request.Votes {
		_ = s.voteManager.AddVote(vote)
	}
}

                                                                           
func (s *Service) LoadChallenge(record *pb.ChallengeRecord) {
	s.challengeManager.LoadChallenge(record)
}

                                                                              
                                                                              
                                                                       
func (s *Service) SyncCommittedChallenge(record *pb.ChallengeRecord) {
	if record == nil || record.ChallengeId == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.challengeManager.LoadChallenge(record)
	if record.Finalized || record.Status == pb.ChallengeStatus_CHALLENGE_FINALIZED {
		s.clearArbitrationTasks(record)
		return
	}
	for _, vote := range record.ArbitrationVotes {
		if vote != nil && vote.WitnessAddress != "" {
			_ = s.stakeManager.RemovePendingTask(vote.WitnessAddress, record.ChallengeId)
		}
	}
}

                                                 
func (s *Service) GetActiveWitnesses() []*pb.WitnessInfo {
	return s.stakeManager.GetActiveWitnesses()
}

                                                                      
func (s *Service) GetAllWitnesses() []*pb.WitnessInfo {
	return s.stakeManager.GetAllWitnesses()
}

                                                                                  
func (s *Service) GetWitnessCandidates() []*WitnessCandidate {
	return s.stakeManager.GetWitnessCandidates()
}

                                                                                        
func (s *Service) SelectArbitrators(challengeID string, round uint32, candidates []*WitnessCandidate, excludeWitnesses []string, beacon []byte) ([]string, error) {
	return s.selector.SelectArbitrators(challengeID, round, candidates, excludeWitnesses, beacon)
}

                                                                                                                  
var ErrStateBackedArbitrationSelectionRequired = fmt.Errorf("arbitration selection requires StateView candidates and atomic case collateral reservation")

                                                                                        
func (s *Service) PrepareArbitration(challengeID string, height uint64, excludeWitnesses []string, beacon []byte) ([]string, uint64, uint32, error) {
	return nil, 0, 0, ErrStateBackedArbitrationSelectionRequired
}

                                                                          
                                                               
func (s *Service) ValidateChallenge(requestID, challengerAddr string, stakeAmount decimal.Decimal) error {
	return s.challengeManager.ValidateChallenge(requestID, challengerAddr, stakeAmount)
}

                                                                                                         
func (s *Service) SelectionBeaconDepth() uint64 {
	return s.config.SelectionBeaconDepth
}

func (s *Service) RetryIntervalBlocks() uint64 {
	return s.config.RetryIntervalBlocks
}

                                                                                                   
func (s *Service) ArbitrationPeriodBlocks() uint64 {
	if s == nil {
		return configuredArbitrationPeriodBlocks(nil)
	}
	return configuredArbitrationPeriodBlocks(s.config)
}

                                                                                           
func (s *Service) BeaconMixBlocks() uint64 {
	if s.config.BeaconMixBlocks == 0 {
		return 1
	}
	return s.config.BeaconMixBlocks
}

func (s *Service) MinSelectedWitnessCount() uint32 {
	if s == nil || s.config == nil || s.config.MinSelectedWitnessCount == 0 {
		return 4
	}
	return s.config.MinSelectedWitnessCount
}

                                                                             
func (s *Service) CanForceFinalize(ch *pb.ChallengeRecord) bool {
	return s.canForceFinalize(ch)
}

                                                                  
func (s *Service) MaxShelveWaitBlocks() uint64 {
	return s.config.MaxShelveWaitBlocks
}

                                                                                                                        
                                                                                                               
func (s *Service) ComputeTimeoutFinalization(ch *pb.ChallengeRecord, req *pb.RechargeRequest, _ map[string]*pb.WitnessInfo, height uint64) *ArbitrationVoteOutcome {
	if ch == nil || req == nil || ch.RequestId != req.RequestId || ch.Finalized || ch.Settled || ch.Status != pb.ChallengeStatus_CHALLENGE_ACTIVE || terminalRechargeStatus(req.Status) || height <= ch.DeadlineHeight {
		return nil
	}
	pass, fail, err := arbitrationVoteTally(ch)
	if err != nil {
		return nil
	}
	result := arbitrationConsensus(ch)
	if result != ConsensusPass && result != ConsensusFail {
		return nil
	}
	updated := proto.Clone(ch).(*pb.ChallengeRecord)
	updated.PassCount, updated.FailCount = pass, fail
	updated.FinalVote = pb.WitnessVoteType_VOTE_PASS
	if result == ConsensusFail {
		updated.FinalVote = pb.WitnessVoteType_VOTE_FAIL
	}
	updated.Finalized, updated.OrdinaryVotesFrozen = true, true
	updated.Status = pb.ChallengeStatus_CHALLENGE_FINALIZED
	updated.FinalizeHeight = height
	updated.ChallengeSuccess = (updated.FinalVote == pb.WitnessVoteType_VOTE_PASS) != (req.PassCount > req.FailCount)
	request := proto.Clone(req).(*pb.RechargeRequest)
	request.FinalizeHeight = height
	request.Status = pb.RechargeRequestStatus_RECHARGE_REJECTED
	if result == ConsensusPass {
		request.Status = pb.RechargeRequestStatus_RECHARGE_FINALIZED
	}
	return &ArbitrationVoteOutcome{Challenge: updated, Request: request}
}

                                                                                                  
func (s *Service) ComputeConservativeFinalization(ch *pb.ChallengeRecord, req *pb.RechargeRequest, witnesses map[string]*pb.WitnessInfo, height uint64) *ArbitrationVoteOutcome {
	return nil
}

func (s *Service) CommitTimeoutOutcome(outcome *ArbitrationVoteOutcome, height uint64) {
	if outcome == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if outcome.Challenge != nil {
		s.challengeManager.LoadChallenge(outcome.Challenge)
		if outcome.Challenge.Finalized || outcome.Challenge.Status == pb.ChallengeStatus_CHALLENGE_FINALIZED {
			s.clearArbitrationTasks(outcome.Challenge)
		}
	}
	if outcome.Request != nil {
		s.setRequestLocked(outcome.Request)
	}

	if outcome.Request != nil {
		var eventType EventType
		switch outcome.Request.Status {
		case pb.RechargeRequestStatus_RECHARGE_FINALIZED:
			eventType = EventRechargeFinalized
		case pb.RechargeRequestStatus_RECHARGE_REJECTED:
			eventType = EventRechargeRejected
		}
		if eventType != "" {
			s.emitEvent(&Event{
				Type:      eventType,
				RequestID: outcome.Request.RequestId,
				Data:      outcome.Request,
				Height:    height,
			})
		}
	}
}

                                                                                                    
                                                                                                                                  
                                                                                                                                                      
                                                                                                                  
func (s *Service) clearArbitrationTasks(ch *pb.ChallengeRecord) {
	if ch == nil || ch.ChallengeId == "" {
		return
	}
	for _, addr := range ch.Arbitrators {
		_ = s.stakeManager.RemovePendingTask(addr, ch.ChallengeId)
	}
}

func (s *Service) ApplyRechargeSelection(in *pb.RechargeRequest, beacon []byte, height uint64) (*pb.RechargeRequest, error) {
	if in == nil {
		return nil, fmt.Errorf("nil request")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	selectionRound := in.Round
	candidates := s.stakeManager.GetWitnessCandidates()
	witnesses, err := s.selector.SelectWitnesses(in.RequestId, selectionRound, candidates, nil, beacon)
	if err != nil {
		return nil, fmt.Errorf("failed to select witnesses: %w", err)
	}
	if minSelected := s.MinSelectedWitnessCount(); uint32(len(witnesses)) < minSelected {
		return nil, fmt.Errorf("%w: selected=%d required=%d", ErrInsufficientSelectedWitnesses, len(witnesses), minSelected)
	}

	                                                                                               
	updated := proto.Clone(in).(*pb.RechargeRequest)
	updated.Status = pb.RechargeRequestStatus_RECHARGE_VOTING
	updated.Round = selectionRound
	updated.SelectedWitnesses = witnesses
	updated.DeadlineHeight = height + s.config.VotingPeriodBlocks
	updated.DecisionHeight = 0
	updated.VoteAcceptUntilHeight = 0
	updated.SelectionBeacon = append([]byte(nil), beacon...)
	updated.ShelveHeight = 0

	                                                                                                       
	s.setRequestLocked(updated)
	s.voteManager.ClearRequest(updated.RequestId)
	s.voteManager.SetSelectedWitnesses(updated.RequestId, witnesses)
	for _, w := range witnesses {
		_ = s.stakeManager.AddPendingTask(w, updated.RequestId)
	}

	return updated, nil
}

                                                                                                              
func (s *Service) ApplyArbitrationSelection(in *pb.ChallengeRecord, excludeWitnesses []string, beacon []byte, height uint64) (*pb.ChallengeRecord, error) {
	return nil, ErrStateBackedArbitrationSelectionRequired
}

                                                                     

                                                               
func (s *Service) CreateChallenge(
	challengeID string,
	requestID string,
	challengerAddr string,
	stakeAmount string,
	reason string,
	height uint64,
	arbitrators []string,
) (*pb.ChallengeRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, err := s.challengeManager.CreateChallenge(
		challengeID, requestID, challengerAddr, stakeAmount, reason, height, arbitrators,
	)
	if err != nil {
		return nil, err
	}
	if request := s.requests[requestID]; request != nil {
		record.OriginalVotes = OriginalPositions(request)
		s.challengeManager.LoadChallenge(record)
	}
	return record, nil
}

                                                                  
func (s *Service) GetChallenge(challengeID string) (*pb.ChallengeRecord, error) {
	return s.challengeManager.GetChallenge(challengeID)
}

                                                                 
func (s *Service) FinalizeChallenge(challengeID string, success bool, height uint64) (*pb.ChallengeRecord, error) {
	return s.challengeManager.FinalizeChallenge(challengeID, success, height)
}

                                                                                     
func (s *Service) ConfigSnapshot() *Config {
	if s == nil || s.config == nil {
		return DefaultConfig()
	}
	cfg := *s.config
	cfg.OrdinaryCommitteeSizes = append([]uint32(nil), s.config.OrdinaryCommitteeSizes...)
	return &cfg
}
