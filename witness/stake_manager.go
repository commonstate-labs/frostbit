                           
                                                                     
package witness

import (
	"dex/pb"
	"fmt"
	"sort"
	"sync"

	"github.com/shopspring/decimal"
)

                               
type StakeManager struct {
	mu     sync.RWMutex
	config *Config

	                                                                       
	witnesses map[string]*pb.WitnessInfo

	               
	eventChan chan *Event
}

                                        
func NewStakeManager(config *Config) *StakeManager {
	if config == nil {
		config = DefaultConfig()
	}
	return &StakeManager{
		config:    config,
		witnesses: make(map[string]*pb.WitnessInfo),
		eventChan: make(chan *Event, 100),
	}
}

                                             
func (sm *StakeManager) LoadWitness(info *pb.WitnessInfo) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.witnesses[info.Address] = info
}

                           
func (sm *StakeManager) Reset() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.witnesses = make(map[string]*pb.WitnessInfo)
}

                                   
func (sm *StakeManager) GetWitness(address string) (*pb.WitnessInfo, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	info, exists := sm.witnesses[address]
	if !exists {
		return nil, ErrWitnessNotFound
	}
	return info, nil
}

                                   
func (sm *StakeManager) ValidateStake(address string, amount decimal.Decimal) error {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	                           
	minStake, err := decimal.NewFromString(sm.config.MinStakeAmount)
	if err != nil {
		return fmt.Errorf("invalid min stake config: %w", err)
	}

	if amount.LessThan(minStake) {
		return ErrInsufficientStake
	}

	                                       
	if info, exists := sm.witnesses[address]; exists {
		if info.Status == pb.WitnessStatus_WITNESS_ACTIVE {
			return ErrWitnessAlreadyActive
		}
	}

	return nil
}

                            
                                    
func (sm *StakeManager) ProcessStake(address string, amount decimal.Decimal, height uint64) (*pb.WitnessInfo, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	                                 
	info, exists := sm.witnesses[address]
	if !exists {
		info = &pb.WitnessInfo{
			Address:     address,
			StakeAmount: "0",
			Status:      pb.WitnessStatus_WITNESS_CANDIDATE,
		}
	}

	                     
	currentStake, _ := decimal.NewFromString(info.StakeAmount)
	newStake := currentStake.Add(amount)
	info.StakeAmount = newStake.String()

	                                                                        
	if info.Status != pb.WitnessStatus_WITNESS_UNSTAKING {
		info.Status = pb.WitnessStatus_WITNESS_ACTIVE
	}

	                  
	sm.witnesses[address] = info

	               
	sm.emitEvent(&Event{
		Type:   EventWitnessStaked,
		Data:   info,
		Height: height,
	})

	return info, nil
}

                                        
func (sm *StakeManager) ValidateUnstake(address string) error {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	info, exists := sm.witnesses[address]
	if !exists {
		return ErrWitnessNotFound
	}
	               
	if info.Status != pb.WitnessStatus_WITNESS_ACTIVE {
		return ErrWitnessNotActive
	}

	                                                                                             
	return nil
}

                                       
func (sm *StakeManager) ProcessUnstake(address string, height uint64) (*pb.WitnessInfo, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	info, exists := sm.witnesses[address]
	if !exists {
		return nil, ErrWitnessNotFound
	}

	                              
	info.Status = pb.WitnessStatus_WITNESS_UNSTAKING
	info.UnstakeHeight = height

	                  
	sm.witnesses[address] = info

	               
	sm.emitEvent(&Event{
		Type:   EventWitnessUnstaking,
		Data:   info,
		Height: height,
	})

	return info, nil
}

                                  
func (sm *StakeManager) ValidateExit(address string, currentHeight uint64) error {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	info, exists := sm.witnesses[address]
	if !exists {
		return ErrWitnessNotFound
	}
	return sm.validateExitLocked(info, currentHeight)
}

func (sm *StakeManager) validateExitLocked(info *pb.WitnessInfo, currentHeight uint64) error {
	               
	if info.Status != pb.WitnessStatus_WITNESS_UNSTAKING {
		return ErrUnstakingInProgress
	}

	                              
	if currentHeight < info.UnstakeHeight || currentHeight-info.UnstakeHeight < sm.config.UnstakeLockBlocks {
		return ErrLockPeriodNotExpired
	}

	                                                                                                            
	if info.ActiveCaseCount > 0 {
		return ErrPendingTasks
	}
	reserved, err := parseStakeManagerInteger(info.ReservedStakeAmount)
	if err != nil {
		return err
	}
	if !reserved.IsZero() {
		return ErrPendingTasks
	}

	return nil
}

                           
func (sm *StakeManager) ProcessExit(address string, height uint64) (*pb.WitnessInfo, decimal.Decimal, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	info, exists := sm.witnesses[address]
	if !exists {
		return nil, decimal.Zero, ErrWitnessNotFound
	}
	if err := sm.validateExitLocked(info, height); err != nil {
		return nil, decimal.Zero, err
	}

	                     
	refundAmount, err := parseStakeManagerInteger(info.StakeAmount)
	if err != nil {
		return nil, decimal.Zero, err
	}

	               
	info.Status = pb.WitnessStatus_WITNESS_EXITED
	info.StakeAmount = "0"

	                  
	sm.witnesses[address] = info

	               
	sm.emitEvent(&Event{
		Type:   EventWitnessExited,
		Data:   info,
		Height: height,
	})

	return info, refundAmount, nil
}

                                                                                           
func (sm *StakeManager) ProcessSlash(address string, reason string, height uint64) (*pb.WitnessInfo, decimal.Decimal, error) {
	return sm.processSlashLocked(address, sm.config.SlashRatio, reason, height, true)
}

                                                     
                                                                                             
func (sm *StakeManager) ProcessSlashByRatio(address string, ratio float64, reason string, height uint64, markSlashed bool) (*pb.WitnessInfo, decimal.Decimal, error) {
	return sm.processSlashLocked(address, ratio, reason, height, markSlashed)
}

func (sm *StakeManager) processSlashLocked(address string, ratio float64, reason string, height uint64, markSlashed bool) (*pb.WitnessInfo, decimal.Decimal, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	info, exists := sm.witnesses[address]
	if !exists {
		return nil, decimal.Zero, ErrWitnessNotFound
	}
	                                                                                                                  
	reserved, err := parseStakeManagerInteger(info.ReservedStakeAmount)
	if err != nil {
		return nil, decimal.Zero, err
	}
	if info.ActiveCaseCount > 0 || !reserved.IsZero() {
		return nil, decimal.Zero, fmt.Errorf("case-backed witness requires per-case collateral settlement")
	}

	stakeAmount, _ := decimal.NewFromString(info.StakeAmount)
	slashAmount := stakeAmount.Mul(decimal.NewFromFloat(ratio))

	info.StakeAmount = stakeAmount.Sub(slashAmount).String()
	info.SlashedCount++
	if markSlashed {
		info.Status = pb.WitnessStatus_WITNESS_SLASHED
	}

	sm.witnesses[address] = info

	sm.emitEvent(&Event{
		Type:   EventWitnessSlashed,
		Data:   map[string]interface{}{"info": info, "reason": reason, "amount": slashAmount},
		Height: height,
	})

	return info, slashAmount, nil
}

                                       
func (sm *StakeManager) AddPendingTask(address, taskID string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	info, exists := sm.witnesses[address]
	if !exists {
		return ErrWitnessNotFound
	}

	for _, t := range info.PendingTasks {
		if t == taskID {
			return nil
		}
	}
	info.PendingTasks = append(info.PendingTasks, taskID)
	sm.witnesses[address] = info

	return nil
}

                                          
func (sm *StakeManager) RemovePendingTask(address, taskID string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	info, exists := sm.witnesses[address]
	if !exists {
		return ErrWitnessNotFound
	}

	                        
	filtered := info.PendingTasks[:0]
	for _, t := range info.PendingTasks {
		if t != taskID {
			filtered = append(filtered, t)
		}
	}
	info.PendingTasks = filtered

	sm.witnesses[address] = info
	return nil
}

                                                 
func (sm *StakeManager) GetActiveWitnesses() []*pb.WitnessInfo {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	result := make([]*pb.WitnessInfo, 0)
	for _, info := range sm.witnesses {
		if sm.eligibleForNewCaseLocked(info) {
			result = append(result, info)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Address < result[j].Address })
	return result
}

                                                                           
func (sm *StakeManager) GetWitnessCandidates() []*WitnessCandidate {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	result := make([]*WitnessCandidate, 0)
	for _, info := range sm.witnesses {
		stake, err := parseStakeManagerInteger(info.StakeAmount)
		if err != nil {
			continue
		}
		result = append(result, &WitnessCandidate{
			Address:     info.Address,
			StakeAmount: stake,
			IsActive:    sm.eligibleForNewCaseLocked(info),
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Address < result[j].Address })
	return result
}

func parseStakeManagerInteger(raw string) (decimal.Decimal, error) {
	if raw == "" {
		return decimal.Zero, nil
	}
	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			return decimal.Zero, fmt.Errorf("witness stake must be an unsigned integer")
		}
	}
	return decimal.NewFromString(raw)
}

func (sm *StakeManager) eligibleForNewCaseLocked(info *pb.WitnessInfo) bool {
	if info == nil || info.Status != pb.WitnessStatus_WITNESS_ACTIVE {
		return false
	}
	stake, err := parseStakeManagerInteger(info.StakeAmount)
	if err != nil {
		return false
	}
	minimum, err := parseStakeManagerInteger(sm.config.MinStakeAmount)
	return err == nil && minimum.IsPositive() && stake.GreaterThanOrEqual(minimum)
}

                                                                      
func (sm *StakeManager) GetAllWitnesses() []*pb.WitnessInfo {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	result := make([]*pb.WitnessInfo, 0, len(sm.witnesses))
	for _, info := range sm.witnesses {
		result = append(result, info)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Address < result[j].Address })
	return result
}

                                           
func (sm *StakeManager) UpdateWitnessStats(address string, voteType pb.WitnessVoteType) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	info, exists := sm.witnesses[address]
	if !exists {
		return ErrWitnessNotFound
	}

	info.TotalWitnessCount++
	switch voteType {
	case pb.WitnessVoteType_VOTE_PASS:
		info.PassCount++
	case pb.WitnessVoteType_VOTE_FAIL:
		info.FailCount++
	case pb.WitnessVoteType_VOTE_ABSTAIN:
		info.AbstainCount++
	}

	sm.witnesses[address] = info
	return nil
}

                                
func (sm *StakeManager) DistributeReward(witnesses []string, amount decimal.Decimal) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if len(witnesses) == 0 {
		return nil
	}

	                              
	perWitnessAmount := amount.Div(decimal.NewFromInt(int64(len(witnesses))))

	for _, addr := range witnesses {
		info, exists := sm.witnesses[addr]
		if !exists {
			continue
		}

		                        
		currentPending, _ := decimal.NewFromString(info.PendingReward)
		info.PendingReward = currentPending.Add(perWitnessAmount).String()

		sm.witnesses[addr] = info
	}

	return nil
}

                            
func (sm *StakeManager) Events() <-chan *Event {
	return sm.eventChan
}

                         
func (sm *StakeManager) emitEvent(event *Event) {
	select {
	case sm.eventChan <- event:
	default:
	}
}
