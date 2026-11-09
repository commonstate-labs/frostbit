                               
                                                               
package witness

import (
	"dex/pb"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"
)

                                                                                                   
var ErrArbitrationHeightRequired = errors.New("arbitration vote requires execution height")

                                                                                                            
func arbitrationVoteTally(record *pb.ChallengeRecord) (pass, fail uint32, err error) {
	if record == nil || record.RequestId == "" || record.BallotRound == 0 {
		return 0, 0, fmt.Errorf("invalid arbitration ballot state")
	}
	if _, err = ArbitrationQuorum(record); err != nil {
		return 0, 0, err
	}
	members := make(map[string]bool, len(record.Arbitrators))
	for _, address := range record.Arbitrators {
		if address == "" || members[address] {
			return 0, 0, fmt.Errorf("invalid arbitration committee")
		}
		members[address] = true
	}
	if record.ArbitrationStage == pb.ArbitrationStage_ARBITRATION_FINAL_COURT {
		if len(members) != 4 || !record.OrdinaryVotesFrozen {
			return 0, 0, fmt.Errorf("invalid frozen final court")
		}
		court := make(map[string]bool)
		for _, address := range record.FinalCourt {
			if !members[address] || court[address] {
				return 0, 0, fmt.Errorf("committee differs from frozen final court")
			}
			court[address] = true
		}
	} else if uint32(len(members)) > record.OrdinaryCommitteeSizes[record.ArbitrationRound-1] {
		return 0, 0, fmt.Errorf("committee exceeds frozen planned size")
	}
	seen := make(map[string]bool)
	for _, vote := range record.ArbitrationVotes {
		if vote == nil || vote.RequestId != record.RequestId || vote.BallotRound != record.BallotRound || !members[vote.WitnessAddress] || seen[vote.WitnessAddress] || !IsValidVoteType(vote.VoteType) {
			return 0, 0, fmt.Errorf("invalid persisted arbitration vote")
		}
		seen[vote.WitnessAddress] = true
		if vote.VoteType == pb.WitnessVoteType_VOTE_PASS {
			pass++
		} else if vote.VoteType == pb.WitnessVoteType_VOTE_FAIL {
			fail++
		}
	}
	return pass, fail, nil
}

func arbitrationConsensus(record *pb.ChallengeRecord) ConsensusResult {
	pass, fail, err := arbitrationVoteTally(record)
	if err != nil {
		return ConsensusNone
	}
	quorum, _ := ArbitrationQuorum(record)
	if pass >= quorum {
		return ConsensusPass
	}
	if fail >= quorum {
		return ConsensusFail
	}
	return ConsensusNone
}

                                   
type ChallengeManager struct {
	mu     sync.RWMutex
	config *Config

	                     
	challenges map[string]*pb.ChallengeRecord                         

	activeByDeadline map[uint64]map[string]struct{}                                  
	activeDeadline   map[string]uint64                                              

	                               
	requestChallenges map[string]string                            

	                     
	arbitrationVotes map[string]map[string]*pb.WitnessVote                                         

	               
	eventChan chan *Event
}

                                            
func NewChallengeManager(config *Config) *ChallengeManager {
	if config == nil {
		config = DefaultConfig()
	}
	return &ChallengeManager{
		config:            config,
		challenges:        make(map[string]*pb.ChallengeRecord),
		activeByDeadline:  make(map[uint64]map[string]struct{}),
		activeDeadline:    make(map[string]uint64),
		requestChallenges: make(map[string]string),
		arbitrationVotes:  make(map[string]map[string]*pb.WitnessVote),
		eventChan:         make(chan *Event, 100),
	}
}

                           
func (cm *ChallengeManager) Reset() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.challenges = make(map[string]*pb.ChallengeRecord)
	cm.activeByDeadline = make(map[uint64]map[string]struct{})
	cm.activeDeadline = make(map[string]uint64)
	cm.requestChallenges = make(map[string]string)
	cm.arbitrationVotes = make(map[string]map[string]*pb.WitnessVote)
}

                                                                             
func (cm *ChallengeManager) clearChallengeIndexesLocked(challengeID string) {
	if deadline, ok := cm.activeDeadline[challengeID]; ok {
		delete(cm.activeDeadline, challengeID)
		if ids := cm.activeByDeadline[deadline]; ids != nil {
			delete(ids, challengeID)
			if len(ids) == 0 {
				delete(cm.activeByDeadline, deadline)
			}
		}
	}
}

func (cm *ChallengeManager) indexChallengeLocked(record *pb.ChallengeRecord) {
	if record == nil || record.ChallengeId == "" || record.Finalized || record.Status == pb.ChallengeStatus_CHALLENGE_FINALIZED {
		return
	}
	switch record.Status {
	case pb.ChallengeStatus_CHALLENGE_ACTIVE:
		if cm.activeByDeadline[record.DeadlineHeight] == nil {
			cm.activeByDeadline[record.DeadlineHeight] = make(map[string]struct{})
		}
		cm.activeByDeadline[record.DeadlineHeight][record.ChallengeId] = struct{}{}
		cm.activeDeadline[record.ChallengeId] = record.DeadlineHeight
	}
}

func (cm *ChallengeManager) setChallengeLocked(record *pb.ChallengeRecord) {
	if record == nil || record.ChallengeId == "" {
		return
	}
	if cm.activeByDeadline == nil {
		cm.activeByDeadline = make(map[uint64]map[string]struct{})
	}
	if cm.activeDeadline == nil {
		cm.activeDeadline = make(map[string]uint64)
	}
	cm.clearChallengeIndexesLocked(record.ChallengeId)
	cm.challenges[record.ChallengeId] = record
	if record.RequestId != "" {
		cm.requestChallenges[record.RequestId] = record.ChallengeId
	}
	cm.indexChallengeLocked(record)
}

                                                                                                              
func (cm *ChallengeManager) LoadChallenge(record *pb.ChallengeRecord) {
	if record == nil || record.ChallengeId == "" {
		return
	}
	cm.mu.Lock()
	defer cm.mu.Unlock()
	updated := proto.Clone(record).(*pb.ChallengeRecord)
	cm.setChallengeLocked(updated)
	cm.arbitrationVotes[updated.ChallengeId] = make(map[string]*pb.WitnessVote)
	for _, vote := range updated.ArbitrationVotes {
		if vote != nil {
			cm.arbitrationVotes[updated.ChallengeId][vote.WitnessAddress] = vote
		}
	}
}

                                 
func (cm *ChallengeManager) ValidateChallenge(
	requestID string,
	challengerAddr string,
	stakeAmount decimal.Decimal,
) error {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	                              
	if requestID == "" || challengerAddr == "" || !stakeAmount.IsPositive() || !stakeAmount.Equal(stakeAmount.Truncate(0)) {
		return ErrInsufficientChallenge
	}

	                           
	return nil
}

                                                                                                           
func (cm *ChallengeManager) CreateChallenge(challengeID, requestID, challengerAddr, stakeAmount, reason string, height uint64, arbitrators []string) (*pb.ChallengeRecord, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if challengeID == "" || requestID == "" {
		return nil, fmt.Errorf("missing challenge identity")
	}
	stake, err := parseStakeManagerInteger(stakeAmount)
	if err != nil || stake.IsNegative() || (challengerAddr != "system" && (challengerAddr == "" || !stake.IsPositive())) {
		return nil, ErrInsufficientChallenge
	}
	if _, exists := cm.challenges[challengeID]; exists {
		return nil, fmt.Errorf("challenge already exists")
	}
	if _, exists := cm.requestChallenges[requestID]; exists {
		return nil, fmt.Errorf("request already has a challenge")
	}
	record := &pb.ChallengeRecord{ChallengeId: challengeID, RequestId: requestID, ChallengerAddress: challengerAddr, StakeAmount: stakeAmount, Reason: reason, CreateHeight: height, Status: pb.ChallengeStatus_CHALLENGE_PENDING_SELECTION, ArbitrationPeriodBlocks: configuredArbitrationPeriodBlocks(cm.config)}
	if err := PrepareArbitrationRecord(record, &pb.RechargeRequest{RequestId: requestID}, cm.config); err != nil {
		return nil, err
	}
	if len(arbitrators) > 0 {
		record, err = StartArbitrationRound(record, arbitrators, nil, height, cm.config.ArbitrationPeriodBlocks)
		if err != nil {
			return nil, err
		}
	}
	cm.setChallengeLocked(record)
	cm.arbitrationVotes[challengeID] = make(map[string]*pb.WitnessVote)
	cm.emitEvent(&Event{Type: EventChallengeInitiated, RequestID: requestID, Data: record, Height: height})
	return proto.Clone(record).(*pb.ChallengeRecord), nil
}

                                  
func (cm *ChallengeManager) GetChallenge(challengeID string) (*pb.ChallengeRecord, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	record, exists := cm.challenges[challengeID]
	if !exists {
		return nil, ErrChallengeNotFound
	}
	return proto.Clone(record).(*pb.ChallengeRecord), nil
}

                                                   
func (cm *ChallengeManager) GetChallengeByRequest(requestID string) (*pb.ChallengeRecord, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	challengeID, exists := cm.requestChallenges[requestID]
	if !exists {
		return nil, ErrChallengeNotFound
	}

	record, exists := cm.challenges[challengeID]
	if !exists {
		return nil, ErrChallengeNotFound
	}
	return proto.Clone(record).(*pb.ChallengeRecord), nil
}

                                                                                                 
func (cm *ChallengeManager) AddArbitrationVote(challengeID string, vote *pb.WitnessVote) error {
	if vote == nil || !IsValidVoteType(vote.VoteType) {
		return ErrInvalidVote
	}
	if _, err := cm.GetChallenge(challengeID); err != nil {
		return err
	}
	return ErrArbitrationHeightRequired
}

                                                                                                               
func (cm *ChallengeManager) AddArbitrationVoteAtHeight(challengeID string, vote *pb.WitnessVote, height uint64) error {
	if vote == nil || !IsValidVoteType(vote.VoteType) {
		return ErrInvalidVote
	}
	cm.mu.Lock()
	defer cm.mu.Unlock()
	record, exists := cm.challenges[challengeID]
	if !exists {
		return ErrChallengeNotFound
	}
	if record.Finalized || record.Settled || record.Status != pb.ChallengeStatus_CHALLENGE_ACTIVE {
		return fmt.Errorf("arbitration is not active")
	}
	if height > record.DeadlineHeight {
		return ErrArbitrationExpired
	}
	if vote.RequestId != record.RequestId || vote.BallotRound == 0 || vote.BallotRound != record.BallotRound {
		return fmt.Errorf("arbitration vote request or round mismatch")
	}
	if _, _, err := arbitrationVoteTally(record); err != nil {
		return err
	}
	selected := false
	for _, address := range record.Arbitrators {
		if address == vote.WitnessAddress {
			selected = true
			break
		}
	}
	if !selected {
		return ErrNotArbitrator
	}
	for _, old := range record.ArbitrationVotes {
		if old.WitnessAddress == vote.WitnessAddress {
			return ErrDuplicateVote
		}
	}
	if arbitrationConsensus(record) != ConsensusNone {
		return fmt.Errorf("arbitration quorum already reached")
	}
	updated := proto.Clone(record).(*pb.ChallengeRecord)
	accepted := proto.Clone(vote).(*pb.WitnessVote)
	updated.ArbitrationVotes = append(updated.ArbitrationVotes, accepted)
	pass, fail, err := arbitrationVoteTally(updated)
	if err != nil {
		return err
	}
	if updated.ArbitrationStage == pb.ArbitrationStage_ARBITRATION_ORDINARY {
		if updated.OrdinaryVotesFrozen {
			return fmt.Errorf("ordinary positions are frozen")
		}
		updated.OrdinaryVotes = LastDirectionalPositions(updated.OrdinaryVotes, accepted)
	}
	updated.PassCount, updated.FailCount = pass, fail
	cm.setChallengeLocked(updated)
	if cm.arbitrationVotes[challengeID] == nil {
		cm.arbitrationVotes[challengeID] = make(map[string]*pb.WitnessVote)
	}
	cm.arbitrationVotes[challengeID][accepted.WitnessAddress] = accepted
	return nil
}

                                                                                            
func (cm *ChallengeManager) CheckArbitrationConsensus(challengeID string, _ bool) ConsensusResult {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	record := cm.challenges[challengeID]
	if record == nil || record.Settled || (!record.Finalized && record.Status != pb.ChallengeStatus_CHALLENGE_ACTIVE) {
		return ConsensusNone
	}
	return arbitrationConsensus(record)
}

                                                                                                               
                                                                                             
func (cm *ChallengeManager) ExpandArbitrationScope(challengeID string, newArbitrators []string, height uint64) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	record, exists := cm.challenges[challengeID]
	if !exists {
		return ErrChallengeNotFound
	}
	if record.Finalized || record.Settled || record.Status != pb.ChallengeStatus_CHALLENGE_ACTIVE || record.ArbitrationStage != pb.ArbitrationStage_ARBITRATION_ORDINARY || record.ArbitrationRound >= 3 {
		return fmt.Errorf("ordinary arbitration cannot expand")
	}
	if height <= record.DeadlineHeight {
		return fmt.Errorf("arbitration round has not expired")
	}
	if _, _, err := arbitrationVoteTally(record); err != nil {
		return err
	}
	if arbitrationConsensus(record) != ConsensusNone {
		return fmt.Errorf("arbitration already has a verdict")
	}
	updated := proto.Clone(record).(*pb.ChallengeRecord)
	updated.ArbitrationRound++
	if updated.ArbitrationRound == 3 && len(updated.FinalCourt) != 4 {
		return fmt.Errorf("third round requires VM-frozen final court")
	}
	updated, err := StartArbitrationRound(updated, newArbitrators, nil, height, cm.config.ArbitrationPeriodBlocks)
	if err != nil {
		return err
	}
	cm.setChallengeLocked(updated)
	cm.arbitrationVotes[challengeID] = make(map[string]*pb.WitnessVote)
	return nil
}

                                                                                                   
                                                                                                   
func (cm *ChallengeManager) FinalizeChallenge(challengeID string, success bool, height uint64) (*pb.ChallengeRecord, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	record, exists := cm.challenges[challengeID]
	if !exists {
		return nil, ErrChallengeNotFound
	}
	if record.Finalized {
		if record.ChallengeSuccess != success {
			return nil, fmt.Errorf("challenge already finalized with another verdict")
		}
		return proto.Clone(record).(*pb.ChallengeRecord), nil
	}
	if record.Settled || record.Status != pb.ChallengeStatus_CHALLENGE_ACTIVE {
		return nil, fmt.Errorf("arbitration is not active")
	}
	result := arbitrationConsensus(record)
	if result != ConsensusPass && result != ConsensusFail {
		return nil, fmt.Errorf("arbitration quorum not reached")
	}
	var originalPass, originalFail uint32
	for _, vote := range LastDirectionalPositions(record.OriginalVotes, nil) {
		if vote.VoteType == pb.WitnessVoteType_VOTE_PASS {
			originalPass++
		} else {
			originalFail++
		}
	}
	actualSuccess := (result == ConsensusPass) != (originalPass > originalFail)
	if success != actualSuccess {
		return nil, fmt.Errorf("challenge success contradicts ballot verdict")
	}
	updated := proto.Clone(record).(*pb.ChallengeRecord)
	updated.PassCount, updated.FailCount, _ = arbitrationVoteTally(record)
	updated.FinalVote = pb.WitnessVoteType_VOTE_PASS
	if result == ConsensusFail {
		updated.FinalVote = pb.WitnessVoteType_VOTE_FAIL
	}
	updated.ChallengeSuccess = actualSuccess
	updated.Finalized, updated.OrdinaryVotesFrozen = true, true
	updated.FinalizeHeight = height
	updated.Status = pb.ChallengeStatus_CHALLENGE_FINALIZED
	cm.setChallengeLocked(updated)
	cm.emitEvent(&Event{Type: EventChallengeResolved, RequestID: updated.RequestId, Data: updated, Height: height})
	return proto.Clone(updated).(*pb.ChallengeRecord), nil
}

                                                                   
                                                                           
func (cm *ChallengeManager) GetActiveChallenges() []*pb.ChallengeRecord {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	ids := make([]string, 0, len(cm.activeDeadline))
	for id := range cm.activeDeadline {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	result := make([]*pb.ChallengeRecord, 0, len(ids))
	for _, id := range ids {
		rec := cm.challenges[id]
		if rec != nil && !rec.Finalized {
			result = append(result, proto.Clone(rec).(*pb.ChallengeRecord))
		}
	}
	return result
}

                                                                              
func (cm *ChallengeManager) GetDueActiveChallenges(height uint64) []*pb.ChallengeRecord {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	ids := make([]string, 0)
	for deadline, bucket := range cm.activeByDeadline {
		if deadline >= height {
			continue
		}
		for id := range bucket {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)

	result := make([]*pb.ChallengeRecord, 0, len(ids))
	for _, id := range ids {
		rec := cm.challenges[id]
		if rec != nil && !rec.Finalized && rec.Status == pb.ChallengeStatus_CHALLENGE_ACTIVE && rec.DeadlineHeight < height {
			result = append(result, proto.Clone(rec).(*pb.ChallengeRecord))
		}
	}
	return result
}

                                    
func (cm *ChallengeManager) ClearChallenge(challengeID string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if record, exists := cm.challenges[challengeID]; exists {
		delete(cm.requestChallenges, record.RequestId)
	}
	cm.clearChallengeIndexesLocked(challengeID)
	delete(cm.challenges, challengeID)
	delete(cm.arbitrationVotes, challengeID)
}

                            
func (cm *ChallengeManager) Events() <-chan *Event {
	return cm.eventChan
}

                         
func (cm *ChallengeManager) emitEvent(event *Event) {
	select {
	case cm.eventChan <- cloneEvent(event):
	default:
	}
}
