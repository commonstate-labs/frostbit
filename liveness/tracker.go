package liveness

import (
	iface "dex/interfaces"
	"dex/keys"
	"dex/logs"
	"dex/protocol"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

                                                                                
                                                                                           
                                                         
type Store interface {
	Get(key string) ([]byte, error)
	Scan(prefix string) (map[string][]byte, error)
}

                                                                                       
                                                                                                   
type WriteView = iface.LivenessWriteView

                                                                                 
                                                                                
type DirectWriter struct {
	SetFn func(key string, val []byte)
	DelFn func(key string)
}

func (d *DirectWriter) Set(k string, v []byte) {
	if d.SetFn != nil {
		d.SetFn(k, v)
	}
}

func (d *DirectWriter) Del(k string) {
	if d.DelFn != nil {
		d.DelFn(k)
	}
}

                          
type MinerState int

const (
	StateProbation MinerState = iota
	StateActive
	StateFrozen
)

                            
                                                                                            
                                                                                       
                                 
type MinerInfo struct {
	Index               uint32     `json:"index"`
	Address             string     `json:"address"`
	State               MinerState `json:"state"`
	Stake               uint64     `json:"stake"`
	JoinHeight          uint64     `json:"join_height"`
	ActiveSinceHeight   uint64     `json:"active_since_height"`
	OfflineSinceHeight  uint64     `json:"offline_since_height,omitempty"`
	VaultEligibleHeight uint64     `json:"vault_eligible_height"`
	MaturedStake        uint64     `json:"matured_stake"`
	                                                                           
	                                                                           
	                                                    
	PendingStake             uint64               `json:"-"`
	PendingStakeMatureHeight uint64               `json:"-"`
	PendingStakeBatches      []StakeMaturityBatch `json:"pending_stake_batches,omitempty"`
	ExitHeight               uint64               `json:"exit_height"`
	TotalSlashed             uint64               `json:"total_slashed"`
	Kicked                   bool                 `json:"kicked,omitempty"`
}

                                                                            
                                                                            
                                                                      
type StakeMaturityBatch struct {
	Amount       uint64 `json:"amount"`
	MatureHeight uint64 `json:"mature_height"`
}

                                                   
                                                                                                                    
                                                                                                     
                                     
type CounterInfo struct {
	Samples int `json:"s"`
	Offline int `json:"f"`
}

                                            
type GlobalInfo struct {
	NextIndex                        uint32 `json:"next_index"`
	ActiveCount                      int    `json:"active_count"`
	ProbationCnt                     int    `json:"probation_cnt"`
	WindowSize                       int    `json:"window_size"`
	LastProcessedHeight              uint64 `json:"last_processed_height"`
	LastVaultEligibilityChangeHeight uint64 `json:"last_vault_eligibility_change_height"`
}

                                                                         
                                                            
type BlockReport struct {
	Reporter string   `json:"r,omitempty"`
	Sampled  []uint32 `json:"s,omitempty"`
	Offline  []uint32 `json:"o,omitempty"`
	Online   []uint32 `json:"n,omitempty"`
}

                                                                                    
                                                                                             
type SlashEvent = iface.LivenessSlashEvent

                                   
type Tracker struct {
	mu  sync.RWMutex
	cfg Config
	db  Store                             

	                  
	miners       map[uint32]*MinerInfo
	addrToIndex  map[string]uint32
	nextIndex    uint32
	activeCount  int
	probationCnt int
	                                                                        
	                                                                          
	                                                                          
	                                                                         
	consensusSnapshotHeight uint64
	consensusSnapshotCount  int

	                        
	  
	                                                                                          
	                                                           
	                                                                                           
	                                                                  
	  
	                                                                            
	                                                                             
	                                                        
	  
	                                                                 
	                                                                                        
	                                                                                                
	sampleCount  map[uint32]int
	offlineCount map[uint32]int
	                                                                            
	                                                                             
	sampleReporterRefs  map[uint32]map[string]int
	offlineReporterRefs map[uint32]map[string]int

	                                                                         
	reportHistory map[uint64]BlockReport

	windowSize                       int
	lastProcessedHeight              uint64
	vaultMaturityBlocks              uint64
	lastVaultEligibilityChangeHeight uint64
}

                                                   
func NewTracker(cfg Config) *Tracker {
	return NewTrackerWithVaultMaturity(cfg, 0)
}

                                                                            
                                                                          
                                                                              
                                               
func NewTrackerWithVaultMaturity(cfg Config, vaultMaturityBlocks uint64) *Tracker {
	return &Tracker{
		cfg:                 cfg,
		miners:              make(map[uint32]*MinerInfo),
		addrToIndex:         make(map[string]uint32),
		sampleCount:         make(map[uint32]int),
		offlineCount:        make(map[uint32]int),
		sampleReporterRefs:  make(map[uint32]map[string]int),
		offlineReporterRefs: make(map[uint32]map[string]int),
		reportHistory:       make(map[uint64]BlockReport),
		windowSize:          cfg.WindowFloor,
		vaultMaturityBlocks: vaultMaturityBlocks,
	}
}

                                                                              
                                                                               
                                                                          
                                                  
func (t *Tracker) SetVaultMaturityBlocks(blocks uint64) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.vaultMaturityBlocks = blocks
	t.mu.Unlock()
}

                                                                                              
func (t *Tracker) SetStore(store Store) {
	t.mu.Lock()
	t.db = store
	t.mu.Unlock()
	t.loadFromDB()
}

                                                                
                                                                                            
                                                                                                         
func (t *Tracker) ReloadFromDB() {
	t.mu.Lock()
	t.miners = make(map[uint32]*MinerInfo)
	t.addrToIndex = make(map[string]uint32)
	t.nextIndex = 0
	t.activeCount = 0
	t.probationCnt = 0
	t.sampleCount = make(map[uint32]int)
	t.offlineCount = make(map[uint32]int)
	t.sampleReporterRefs = make(map[uint32]map[string]int)
	t.offlineReporterRefs = make(map[uint32]map[string]int)
	t.reportHistory = make(map[uint64]BlockReport)
	t.windowSize = t.cfg.WindowFloor
	t.lastProcessedHeight = 0
	t.lastVaultEligibilityChangeHeight = 0
	t.consensusSnapshotHeight = 0
	t.consensusSnapshotCount = 0
	t.mu.Unlock()
	t.loadFromDB()
}

                                                       

                                                                              
                                                                           
                                                                               
func (t *Tracker) ProcessBlock(sv WriteView, height uint64, sampled, offline []uint32) ([]uint32, []SlashEvent) {
	return t.ProcessBlockWithReporter(sv, height, "", sampled, offline)
}

                                                                           
                                                                          
                                                                               
                                                                            
                                                                                
func (t *Tracker) ProcessBlockWithReporter(sv WriteView, height uint64, reporter string, sampled, offline []uint32) ([]uint32, []SlashEvent) {
	return t.ProcessBlockWithOnlineReporter(sv, height, reporter, sampled, offline, nil)
}

func (t *Tracker) ProcessBlockWithOnlineReporter(sv WriteView, height uint64, reporter string, sampled, offline, online []uint32) ([]uint32, []SlashEvent) {
	t.mu.Lock()
	defer t.mu.Unlock()
	reporter = strings.TrimSpace(reporter)
	if len(sampled) > protocol.MaxLivenessReportsPerBlock {
		sampled = sampled[:protocol.MaxLivenessReportsPerBlock]
	}
	if len(offline) > protocol.MaxLivenessReportsPerBlock {
		offline = offline[:protocol.MaxLivenessReportsPerBlock]
	}
	if len(online) > protocol.MaxLivenessReportsPerBlock {
		online = online[:protocol.MaxLivenessReportsPerBlock]
	}

	W := t.windowSize
	previousHeight := t.lastProcessedHeight
	                                                                      
	                                                                           
	if old, ok := t.reportHistory[height]; ok && height == previousHeight && old.Reporter == reporter {
		online = append(append([]uint32(nil), online...), old.Online...)
	}

	                                         
	expireHeight := uint64(0)
	if height > uint64(W) {
		expireHeight = height - uint64(W)
	}
	touchedCounters := make(map[uint32]struct{}, len(sampled)+len(offline))
	t.expireReportsLocked(sv, expireHeight, touchedCounters)

	                                   
	var staleHeights []uint64
	for h := range t.reportHistory {
		if h < expireHeight {
			staleHeights = append(staleHeights, h)
		}
	}
	sort.Slice(staleHeights, func(i, j int) bool { return staleHeights[i] < staleHeights[j] })
	for _, h := range staleHeights {
		t.expireReportsLocked(sv, h, touchedCounters)
	}
	                                                                     
	                                                             
	t.expireReportsLocked(sv, height, touchedCounters)

	                                                                    
	sampledSet := make(map[uint32]struct{}, len(sampled)+len(offline))
	sampledDedup := make([]uint32, 0, len(sampled))
	for _, idx := range sampled {
		miner := t.miners[idx]
		if miner == nil || (miner.State != StateActive && miner.State != StateProbation) {
			continue
		}
		if _, dup := sampledSet[idx]; dup {
			continue
		}
		sampledSet[idx] = struct{}{}
		sampledDedup = append(sampledDedup, idx)
	}
	onlineSet := make(map[uint32]struct{}, len(online))
	onlineDedup := make([]uint32, 0, len(online))
	if reporter != "" {
		for _, idx := range online {
			if _, sampled := sampledSet[idx]; !sampled {
				continue
			}
			if _, duplicate := onlineSet[idx]; duplicate {
				continue
			}
			onlineSet[idx] = struct{}{}
			onlineDedup = append(onlineDedup, idx)
		}
	}
	                                                                
	offlineDedup := make([]uint32, 0, len(offline))
	offlineSet := make(map[uint32]struct{}, len(offline))
	for _, idx := range offline {
		miner := t.miners[idx]
		if miner == nil || (miner.State != StateActive && miner.State != StateProbation) {
			continue
		}
		if _, inSampled := sampledSet[idx]; !inSampled {
			                                                               
			                                                           
			continue
		}
		                                                                    
		                                                                     
		if reporter != "" && miner.Address == reporter {
			continue
		}
		if _, positive := onlineSet[idx]; positive {
			continue
		}
		if _, dup := offlineSet[idx]; dup {
			continue
		}
		offlineSet[idx] = struct{}{}
		offlineDedup = append(offlineDedup, idx)
	}
	sort.Slice(sampledDedup, func(i, j int) bool { return sampledDedup[i] < sampledDedup[j] })
	sort.Slice(offlineDedup, func(i, j int) bool { return offlineDedup[i] < offlineDedup[j] })
	sort.Slice(onlineDedup, func(i, j int) bool { return onlineDedup[i] < onlineDedup[j] })

	                  
	t.reportHistory[height] = BlockReport{
		Reporter: reporter,
		Sampled:  sampledDedup,
		Offline:  offlineDedup,
		Online:   onlineDedup,
	}
	for _, idx := range sampledDedup {
		t.sampleCount[idx]++
		t.incrementReporterRefLocked(t.sampleReporterRefs, idx, reporter)
	}
	for _, idx := range offlineDedup {
		t.offlineCount[idx]++
		t.incrementReporterRefLocked(t.offlineReporterRefs, idx, reporter)
	}

	                                                                      
	                                                                      
	                                                                 
	t.processVaultMaturityLocked(sv, height, previousHeight)

	                                                                          
	                                                                        
	                                              
	var kicked []uint32
	var slashed []SlashEvent
	activeMiners := make([]*MinerInfo, 0, t.activeCount)
	for _, m := range t.miners {
		if m.State == StateActive {
			activeMiners = append(activeMiners, m)
		}
	}
	sort.Slice(activeMiners, func(i, j int) bool { return activeMiners[i].Index < activeMiners[j].Index })
	for _, m := range activeMiners {
		_, positive := onlineSet[m.Index]
		if positive || (reporter != "" && m.Address == reporter) {
			if m.OfflineSinceHeight != 0 {
				m.OfflineSinceHeight = 0
				t.persistMiner(sv, m)
			}
			continue
		}
		samples := t.sampleCount[m.Index]
		if samples < t.effectiveMinSamples() ||
			t.distinctReporterCountLocked(t.sampleReporterRefs, m.Index) < t.effectiveMinReporters() ||
			t.distinctReporterCountLocked(t.offlineReporterRefs, m.Index) < t.effectiveMinReporters() {
			continue
		}
		ratio := float64(t.offlineCount[m.Index]) / float64(samples)
		if ratio < t.cfg.Threshold {
			continue
		}
		if m.OfflineSinceHeight == 0 {
			m.OfflineSinceHeight = height
			t.persistMiner(sv, m)
			continue
		}
		if height < m.OfflineSinceHeight || height-m.OfflineSinceHeight < t.effectiveOfflineGraceBlocks() {
			continue
		}
		if t.isVaultEligibleAtLocked(m, previousHeight) {
			t.lastVaultEligibilityChangeHeight = height
		}
		t.kickMinerLocked(sv, m, height, true)
		kicked = append(kicked, m.Index)
		logs.Info("[Liveness] Miner %d (%s) removed for liveness: samples=%d offline=%d reporters=%d", m.Index, m.Address, samples, t.offlineCount[m.Index], t.distinctReporterCountLocked(t.offlineReporterRefs, m.Index))
	}

	                           
	                                                                              
	                                                                        
	                                                                                                               
	probationMiners := make([]*MinerInfo, 0, len(t.miners))
	for _, m := range t.miners {
		if m.State == StateProbation && height >= saturatingAddHeight(m.JoinHeight, uint64(W)) {
			probationMiners = append(probationMiners, m)
		}
	}
	sort.Slice(probationMiners, func(i, j int) bool { return probationMiners[i].Index < probationMiners[j].Index })
	for _, m := range probationMiners {
		samples := t.sampleCount[m.Index]
		if samples < t.effectiveMinSamples() || t.distinctReporterCountLocked(t.sampleReporterRefs, m.Index) < t.effectiveMinReporters() {
			continue
		}
		ratio := float64(t.offlineCount[m.Index]) / float64(samples)
		                                                                 
		                                                                       
		                                                                   
		                                                        
		if ratio > t.cfg.ActivationThreshold &&
			t.distinctReporterCountLocked(t.offlineReporterRefs, m.Index) >= t.effectiveMinReporters() {
			continue
		}
		m.State = StateActive
		m.ActiveSinceHeight = height
		m.VaultEligibleHeight = saturatingAddHeight(height, t.vaultMaturityBlocks)
		t.activeCount++
		t.probationCnt--
		t.persistMiner(sv, m)
		if t.isVaultEligibleAtLocked(m, height) {
			t.lastVaultEligibilityChangeHeight = height
		}
		logs.Info("[Liveness] Miner %d (%s) promoted to active: samples=%d offline=%d reporters=%d vaultEligibleHeight=%d", m.Index, m.Address, samples, t.offlineCount[m.Index], t.distinctReporterCountLocked(t.sampleReporterRefs, m.Index), m.VaultEligibleHeight)
	}
	if len(probationMiners) > 0 || len(kicked) > 0 {
		t.recalcWindowLocked()
	}

	                                                                           
	                                                                                 
	t.lastProcessedHeight = height
	t.consensusSnapshotHeight = height
	t.consensusSnapshotCount = t.activeCount
	t.persistReport(sv, height, reporter, sampledDedup, offlineDedup, onlineDedup)
	for _, idx := range sampledDedup {
		touchedCounters[idx] = struct{}{}
	}
	for _, idx := range offlineDedup {
		touchedCounters[idx] = struct{}{}
	}
	touchedIndexes := make([]uint32, 0, len(touchedCounters))
	for idx := range touchedCounters {
		touchedIndexes = append(touchedIndexes, idx)
	}
	sort.Slice(touchedIndexes, func(i, j int) bool { return touchedIndexes[i] < touchedIndexes[j] })
	for _, idx := range touchedIndexes {
		t.persistCounter(sv, idx)
	}
	t.persistGlobal(sv)

	return kicked, slashed
}

                                     

func (t *Tracker) RegisterMinerAtHeight(sv WriteView, address string, stake uint64, height uint64) (uint32, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if stake < t.cfg.MinStake {
		return 0, fmt.Errorf("stake %d < min_stake %d", stake, t.cfg.MinStake)
	}

	                                          
	if oldIdx, exists := t.addrToIndex[address]; exists {
		old := t.miners[oldIdx]
		if old != nil && (old.State == StateActive || old.State == StateProbation) {
			                                                                     
			                                                                        
			                                                                        
			                                                                 
			if old.Stake != stake {
				t.normalizeStakeBucketsLocked(old)
				oldMatured := old.MaturedStake
				if stake > old.Stake {
					delta := stake - old.Stake
					if t.vaultMaturityBlocks == 0 {
						old.MaturedStake = saturatingAddStake(old.MaturedStake, delta)
					} else {
						t.addPendingStakeBatchLocked(old, delta, saturatingAddHeight(height, t.vaultMaturityBlocks))
					}
				} else {
					t.reduceStakeBucketsLocked(old, old.Stake-stake)
				}
				old.Stake = stake
				t.capStakeBucketsLocked(old)
				if old.State == StateActive && old.MaturedStake != oldMatured && t.isVaultEligibleAtLocked(old, height) {
					t.lastVaultEligibilityChangeHeight = height
				}
				t.persistMiner(sv, old)
				t.persistGlobal(sv)
			}
			return oldIdx, nil
		}
		if old != nil && old.State == StateFrozen && height >= old.ExitHeight+t.cfg.FreezeBlocks {
			                  
			delete(t.miners, oldIdx)
			delete(t.addrToIndex, address)
			delete(t.sampleCount, oldIdx)
			delete(t.offlineCount, oldIdx)
			delete(t.sampleReporterRefs, oldIdx)
			delete(t.offlineReporterRefs, oldIdx)
			if sv != nil {
				sv.Del(keys.KeyLivenessMiner(oldIdx))
				sv.Del(keys.KeyLivenessCounter(oldIdx))
			}
		} else {
			return 0, fmt.Errorf("miner %s already registered", address)
		}
	}

	idx := t.nextIndex
	t.nextIndex++

	                                                            
	state := StateProbation
	if height == 0 {
		state = StateActive
	}

	m := &MinerInfo{
		Index:      idx,
		Address:    address,
		State:      state,
		Stake:      stake,
		JoinHeight: height,
	}
	if height == 0 {
		                                                                      
		                                              
		m.ActiveSinceHeight = 0
		m.VaultEligibleHeight = 0
		m.MaturedStake = stake
	} else if t.vaultMaturityBlocks == 0 {
		m.MaturedStake = stake
	} else {
		t.addPendingStakeBatchLocked(m, stake, saturatingAddHeight(height, t.vaultMaturityBlocks))
	}
	t.miners[idx] = m
	t.addrToIndex[address] = idx
	if state == StateActive {
		t.activeCount++
	} else {
		t.probationCnt++
	}
	t.recalcWindowLocked()
	                                                                          
	                                                                          
	                                                                          
	                                                        
	if height == 0 && t.consensusSnapshotHeight == 0 {
		t.consensusSnapshotCount = t.activeCount
	}

	t.persistMiner(sv, m)
	t.persistGlobal(sv)
	logs.Info("[Liveness] Miner %d (%s) registered, stake=%d, state=%d", idx, address, stake, state)
	return idx, nil
}

                                
func (t *Tracker) RegisterMiner(sv WriteView, address string, stake uint64) (uint32, error) {
	return t.RegisterMinerAtHeight(sv, address, stake, 0)
}

                                                                            
                                                                               
                                                                            
func (t *Tracker) UpdateMinerStake(sv WriteView, address string, totalStake uint64, height uint64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	idx, ok := t.addrToIndex[address]
	if !ok || t.miners[idx] == nil {
		return fmt.Errorf("miner %s not found", address)
	}
	m := t.miners[idx]
	t.normalizeStakeBucketsLocked(m)
	wasEligible := t.isVaultEligibleAtLocked(m, height)
	oldMatured := m.MaturedStake
	if totalStake > m.Stake {
		delta := totalStake - m.Stake
		if t.vaultMaturityBlocks == 0 {
			m.MaturedStake = saturatingAddStake(m.MaturedStake, delta)
		} else {
			t.addPendingStakeBatchLocked(m, delta, saturatingAddHeight(height, t.vaultMaturityBlocks))
		}
	} else if totalStake < m.Stake {
		t.reduceStakeBucketsLocked(m, m.Stake-totalStake)
	}
	m.Stake = totalStake
	t.capStakeBucketsLocked(m)
	isEligible := t.isVaultEligibleAtLocked(m, height)
	if wasEligible != isEligible || (isEligible && oldMatured != m.MaturedStake) {
		t.lastVaultEligibilityChangeHeight = height
	}
	t.persistMiner(sv, m)
	t.persistGlobal(sv)
	return nil
}

                                                                           
                                                                              
                               
func (t *Tracker) FreezeBlocks() uint64 {
	if t == nil {
		return DefaultFreezeBlocks
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.cfg.FreezeBlocks == 0 {
		return DefaultFreezeBlocks
	}
	return t.cfg.FreezeBlocks
}

func (t *Tracker) RemoveMiner(sv WriteView, address string, height uint64) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	idx, ok := t.addrToIndex[address]
	if !ok {
		return fmt.Errorf("miner %s not found", address)
	}
	m := t.miners[idx]
	if m.State == StateFrozen {
		return fmt.Errorf("miner %s already frozen", address)
	}
	if t.isVaultEligibleAtLocked(m, height) {
		t.lastVaultEligibilityChangeHeight = height
	}
	t.kickMinerLocked(sv, m, height, false)
	logs.Info("[Liveness] Miner %d (%s) voluntarily exited", idx, address)
	return nil
}

                                                                                                          
                                                                                                                                                
func (t *Tracker) UnregisterMiner(sv WriteView, address string) error {
	return t.UnregisterMinersBatch(sv, []string{address})
}

                                                                        
                                                                          
                                                                          
func (t *Tracker) UnregisterMinersBatch(sv WriteView, addresses []string) error {
	if len(addresses) == 0 {
		return nil
	}
	unique := make(map[string]struct{}, len(addresses))
	ordered := make([]string, 0, len(addresses))
	for _, address := range addresses {
		address = strings.TrimSpace(address)
		if address == "" {
			continue
		}
		if _, seen := unique[address]; seen {
			continue
		}
		unique[address] = struct{}{}
		ordered = append(ordered, address)
	}
	sort.Strings(ordered)
	if len(ordered) == 0 {
		return nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	type removal struct {
		address string
		index   uint32
	}
	removals := make([]removal, 0, len(ordered))
	seenIndices := make(map[uint32]string, len(ordered))
	for _, address := range ordered {
		idx, ok := t.addrToIndex[address]
		if !ok {
			continue                                     
		}
		miner := t.miners[idx]
		if miner == nil {
			return fmt.Errorf("miner %s index %d has no tracker record", address, idx)
		}
		if miner.Address != address {
			return fmt.Errorf("miner %s index %d belongs to %s", address, idx, miner.Address)
		}
		if previous, exists := seenIndices[idx]; exists && previous != address {
			return fmt.Errorf("miner addresses %s and %s share index %d", previous, address, idx)
		}
		seenIndices[idx] = address
		removals = append(removals, removal{address: address, index: idx})
	}

	for _, item := range removals {
		delete(t.miners, item.index)
		delete(t.addrToIndex, item.address)
		delete(t.sampleCount, item.index)
		delete(t.offlineCount, item.index)
		delete(t.sampleReporterRefs, item.index)
		delete(t.offlineReporterRefs, item.index)
		if sv != nil {
			sv.Del(keys.KeyLivenessMiner(item.index))
			sv.Del(keys.KeyLivenessCounter(item.index))
		}
	}
	if len(removals) > 0 {
		t.persistGlobal(sv)
	}
	for _, item := range removals {
		logs.Info("[Liveness] Miner %d (%s) fully unregistered after stake release", item.index, item.address)
	}
	return nil
}

                                     

func (t *Tracker) IsActive(address string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	idx, ok := t.addrToIndex[address]
	if !ok {
		return false
	}
	m := t.miners[idx]
	return m != nil && m.State == StateActive
}

func (t *Tracker) IsActiveOrProbation(address string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	idx, ok := t.addrToIndex[address]
	if !ok {
		return false
	}
	m := t.miners[idx]
	return m != nil && (m.State == StateActive || m.State == StateProbation)
}

                                                                                                
func (t *Tracker) IsRegistered(address string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	idx, ok := t.addrToIndex[address]
	if !ok {
		return false
	}
	return t.miners[idx] != nil
}

func (t *Tracker) GetActiveCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.activeCount
}

func (t *Tracker) GetWindowSize() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.windowSize
}

func (t *Tracker) GetMinerIndex(address string) (uint32, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	idx, ok := t.addrToIndex[address]
	return idx, ok
}

func (t *Tracker) GetMinerByAddress(address string) *MinerInfo {
	t.mu.RLock()
	defer t.mu.RUnlock()
	idx, ok := t.addrToIndex[address]
	if !ok {
		return nil
	}
	m := t.miners[idx]
	if m == nil {
		return nil
	}
	cp := *m
	cp.PendingStakeBatches = append([]StakeMaturityBatch(nil), m.PendingStakeBatches...)
	return &cp
}

func (t *Tracker) GetAddressByIndex(idx uint32) (string, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	m := t.miners[idx]
	if m == nil {
		return "", false
	}
	return m.Address, true
}

                                                                         
                                                                      
                                                                          
                           
func (t *Tracker) GetActiveMiners() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	indexes := make([]uint32, 0, t.activeCount+t.probationCnt)
	for idx, m := range t.miners {
		if m.State == StateActive || m.State == StateProbation {
			indexes = append(indexes, idx)
		}
	}
	sort.Slice(indexes, func(i, j int) bool { return indexes[i] < indexes[j] })

	result := make([]string, 0, len(indexes))
	for _, idx := range indexes {
		if m := t.miners[idx]; m != nil {
			result = append(result, m.Address)
		}
	}
	return result
}

                                                                           
                                                                      
  
                                                                              
                                                                           
                                                   
func (t *Tracker) GetConsensusVoterSnapshot() (height uint64, count int) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.consensusSnapshotHeight, t.consensusSnapshotCount
}

func (t *Tracker) GetMinerState(address string) int {
	m := t.GetMinerByAddress(address)
	if m != nil {
		return int(m.State)
	}
	return -1
}

                                                                                          
                                                 
  
                                                                                                 
func (t *Tracker) GetMinerSnapshot(address string) *iface.LivenessMinerSnapshot {
	m := t.GetMinerByAddress(address)
	if m == nil {
		return nil
	}
	return &iface.LivenessMinerSnapshot{
		Index:                    m.Index,
		Address:                  m.Address,
		State:                    int(m.State),
		Stake:                    m.Stake,
		JoinHeight:               m.JoinHeight,
		ActiveSinceHeight:        m.ActiveSinceHeight,
		VaultEligibleHeight:      m.VaultEligibleHeight,
		MaturedStake:             m.MaturedStake,
		PendingStake:             m.PendingStake,
		PendingStakeMatureHeight: m.PendingStakeMatureHeight,
		ExitHeight:               m.ExitHeight,
		TotalSlashed:             m.TotalSlashed,
		Kicked:                   m.Kicked,
	}
}

                                                                          
                                                            
func (t *Tracker) VaultEligibilityChangedAt() uint64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.lastVaultEligibilityChangeHeight
}

                                     

func (t *Tracker) expireReportsLocked(sv WriteView, h uint64, touched map[uint32]struct{}) {
	old, ok := t.reportHistory[h]
	if !ok {
		return
	}
	for _, idx := range old.Sampled {
		t.sampleCount[idx]--
		if t.sampleCount[idx] <= 0 {
			delete(t.sampleCount, idx)
		}
		if touched != nil {
			touched[idx] = struct{}{}
		}
		t.decrementReporterRefLocked(t.sampleReporterRefs, idx, old.Reporter)
	}
	for _, idx := range old.Offline {
		t.offlineCount[idx]--
		if t.offlineCount[idx] <= 0 {
			delete(t.offlineCount, idx)
		}
		if touched != nil {
			touched[idx] = struct{}{}
		}
		t.decrementReporterRefLocked(t.offlineReporterRefs, idx, old.Reporter)
	}
	delete(t.reportHistory, h)
	if sv != nil {
		sv.Del(keys.KeyLivenessReport(h))
	}
}

func (t *Tracker) kickMinerLocked(sv WriteView, m *MinerInfo, height uint64, kicked bool) {
	if m.State == StateActive {
		t.activeCount--
	} else if m.State == StateProbation {
		t.probationCnt--
	}
	m.State = StateFrozen
	m.ExitHeight = height
	m.Kicked = kicked
	t.recalcWindowLocked()
	t.persistMiner(sv, m)
	t.persistGlobal(sv)
}

func (t *Tracker) recalcWindowLocked() {
	                                                                             
	                                                                            
	                                                        
	N := t.activeCount + t.probationCnt
	if N <= 0 {
		t.windowSize = t.cfg.WindowFloor
		return
	}
	betaK := t.cfg.BetaRounds * t.cfg.K
	if betaK <= 0 {
		betaK = 300
	}
	W := t.cfg.MinSamples * N * t.cfg.SafetyMultiplier / betaK
	if W < t.cfg.WindowFloor {
		W = t.cfg.WindowFloor
	}
	if W > t.cfg.WindowCeil {
		W = t.cfg.WindowCeil
	}
	t.windowSize = W
}

func (t *Tracker) effectiveMinSamples() int {
	if t.cfg.MinSamples > 0 {
		return t.cfg.MinSamples
	}
	return 1
}

func (t *Tracker) effectiveMinReporters() int {
	if t.cfg.MinReporters > 0 {
		return t.cfg.MinReporters
	}
	return 3
}

func (t *Tracker) effectiveOfflineGraceBlocks() uint64 {
	if t.cfg.OfflineGraceBlocks != 0 {
		return t.cfg.OfflineGraceBlocks
	}
	return protocol.OfflineGraceBlocks
}

func (t *Tracker) distinctReporterCountLocked(refs map[uint32]map[string]int, idx uint32) int {
	return len(refs[idx])
}

func (t *Tracker) incrementReporterRefLocked(refs map[uint32]map[string]int, idx uint32, reporter string) {
	if reporter == "" {
		return
	}
	byReporter := refs[idx]
	if byReporter == nil {
		byReporter = make(map[string]int)
		refs[idx] = byReporter
	}
	byReporter[reporter]++
}

func (t *Tracker) decrementReporterRefLocked(refs map[uint32]map[string]int, idx uint32, reporter string) {
	if reporter == "" {
		return
	}
	byReporter := refs[idx]
	if byReporter == nil {
		return
	}
	byReporter[reporter]--
	if byReporter[reporter] <= 0 {
		delete(byReporter, reporter)
	}
	if len(byReporter) == 0 {
		delete(refs, idx)
	}
}

func (t *Tracker) processVaultMaturityLocked(sv WriteView, height, previousHeight uint64) {
	indexes := make([]uint32, 0, len(t.miners))
	for idx := range t.miners {
		indexes = append(indexes, idx)
	}
	sort.Slice(indexes, func(i, j int) bool { return indexes[i] < indexes[j] })
	for _, idx := range indexes {
		m := t.miners[idx]
		if m == nil {
			continue
		}
		wasEligible := t.isVaultEligibleAtLocked(m, previousHeight)
		t.canonicalizePendingStakeBatchesLocked(m)
		maturedNow := false
		maturedAmount := uint64(0)
		firstPending := 0
		for firstPending < len(m.PendingStakeBatches) && height >= m.PendingStakeBatches[firstPending].MatureHeight {
			maturedAmount = saturatingAddStake(maturedAmount, m.PendingStakeBatches[firstPending].Amount)
			firstPending++
		}
		if maturedAmount > 0 {
			m.MaturedStake = saturatingAddStake(m.MaturedStake, maturedAmount)
			m.PendingStakeBatches = append([]StakeMaturityBatch(nil), m.PendingStakeBatches[firstPending:]...)
			t.canonicalizePendingStakeBatchesLocked(m)
			t.capStakeBucketsLocked(m)
			t.persistMiner(sv, m)
			maturedNow = true
		}
		isEligible := t.isVaultEligibleAtLocked(m, height)
		if maturedNow || wasEligible != isEligible {
			t.lastVaultEligibilityChangeHeight = height
		}
	}
}

func (t *Tracker) isVaultEligibleAtLocked(m *MinerInfo, height uint64) bool {
	return m != nil && m.State == StateActive && m.MaturedStake >= t.cfg.MinStake && height >= m.VaultEligibleHeight
}

func (t *Tracker) normalizeStakeBucketsLocked(m *MinerInfo) {
	if m == nil {
		return
	}
	t.canonicalizePendingStakeBatchesLocked(m)
	if m.Stake == 0 {
		m.MaturedStake = 0
		m.PendingStakeBatches = nil
		t.canonicalizePendingStakeBatchesLocked(m)
		return
	}
	if m.MaturedStake != 0 || len(m.PendingStakeBatches) != 0 {
		return
	}
	if m.State == StateActive || t.vaultMaturityBlocks == 0 {
		m.MaturedStake = m.Stake
		return
	}
	t.addPendingStakeBatchLocked(m, m.Stake, saturatingAddHeight(m.JoinHeight, t.vaultMaturityBlocks))
}

func (t *Tracker) reduceStakeBucketsLocked(m *MinerInfo, reduction uint64) {
	if m == nil || reduction == 0 {
		return
	}
	t.canonicalizePendingStakeBatchesLocked(m)
	                                                                          
	                                                                      
	for i := len(m.PendingStakeBatches) - 1; i >= 0 && reduction > 0; i-- {
		batch := &m.PendingStakeBatches[i]
		if reduction >= batch.Amount {
			reduction -= batch.Amount
			batch.Amount = 0
		} else {
			batch.Amount -= reduction
			reduction = 0
		}
	}
	t.canonicalizePendingStakeBatchesLocked(m)
	if reduction == 0 {
		return
	}
	if reduction >= m.MaturedStake {
		m.MaturedStake = 0
	} else {
		m.MaturedStake -= reduction
	}
}

func (t *Tracker) capStakeBucketsLocked(m *MinerInfo) {
	if m == nil {
		return
	}
	t.canonicalizePendingStakeBatchesLocked(m)
	if m.MaturedStake > m.Stake {
		m.MaturedStake = m.Stake
		m.PendingStakeBatches = nil
		t.canonicalizePendingStakeBatchesLocked(m)
		return
	}
	maxPending := m.Stake - m.MaturedStake
	if m.PendingStake > maxPending {
		t.reducePendingStakeBatchesLocked(m, m.PendingStake-maxPending)
	}
	t.canonicalizePendingStakeBatchesLocked(m)
}

func (t *Tracker) addPendingStakeBatchLocked(m *MinerInfo, amount, matureHeight uint64) {
	if m == nil || amount == 0 {
		return
	}
	m.PendingStakeBatches = append(m.PendingStakeBatches, StakeMaturityBatch{
		Amount:       amount,
		MatureHeight: matureHeight,
	})
	t.canonicalizePendingStakeBatchesLocked(m)
}

func (t *Tracker) reducePendingStakeBatchesLocked(m *MinerInfo, reduction uint64) {
	if m == nil || reduction == 0 {
		return
	}
	t.canonicalizePendingStakeBatchesLocked(m)
	for i := len(m.PendingStakeBatches) - 1; i >= 0 && reduction > 0; i-- {
		batch := &m.PendingStakeBatches[i]
		if reduction >= batch.Amount {
			reduction -= batch.Amount
			batch.Amount = 0
		} else {
			batch.Amount -= reduction
			reduction = 0
		}
	}
	t.canonicalizePendingStakeBatchesLocked(m)
}

func (t *Tracker) canonicalizePendingStakeBatchesLocked(m *MinerInfo) {
	if m == nil {
		return
	}
	if len(m.PendingStakeBatches) == 0 {
		m.PendingStakeBatches = nil
		m.PendingStake = 0
		m.PendingStakeMatureHeight = 0
		return
	}
	sort.Slice(m.PendingStakeBatches, func(i, j int) bool {
		return m.PendingStakeBatches[i].MatureHeight < m.PendingStakeBatches[j].MatureHeight
	})
	canonical := make([]StakeMaturityBatch, 0, len(m.PendingStakeBatches))
	for _, batch := range m.PendingStakeBatches {
		if batch.Amount == 0 {
			continue
		}
		if n := len(canonical); n > 0 && canonical[n-1].MatureHeight == batch.MatureHeight {
			canonical[n-1].Amount = saturatingAddStake(canonical[n-1].Amount, batch.Amount)
			continue
		}
		canonical = append(canonical, batch)
	}
	if len(canonical) == 0 {
		m.PendingStakeBatches = nil
		m.PendingStake = 0
		m.PendingStakeMatureHeight = 0
		return
	}
	m.PendingStakeBatches = canonical
	m.PendingStake = 0
	for _, batch := range canonical {
		m.PendingStake = saturatingAddStake(m.PendingStake, batch.Amount)
	}
	m.PendingStakeMatureHeight = canonical[0].MatureHeight
}

func saturatingAddHeight(base, delta uint64) uint64 {
	if ^uint64(0)-base < delta {
		return ^uint64(0)
	}
	return base + delta
}

func saturatingAddStake(base, delta uint64) uint64 {
	return saturatingAddHeight(base, delta)
}

                                  

func (t *Tracker) persistMiner(sv WriteView, m *MinerInfo) {
	if sv == nil {
		return
	}
	data, _ := json.Marshal(m)
	sv.Set(keys.KeyLivenessMiner(m.Index), data)
}

func (t *Tracker) persistCounter(sv WriteView, idx uint32) {
	if sv == nil {
		return
	}
	if t.sampleCount[idx] <= 0 && t.offlineCount[idx] <= 0 {
		sv.Del(keys.KeyLivenessCounter(idx))
		return
	}
	c := CounterInfo{
		Samples: t.sampleCount[idx],
		Offline: t.offlineCount[idx],
	}
	data, _ := json.Marshal(c)
	sv.Set(keys.KeyLivenessCounter(idx), data)
}

func (t *Tracker) persistReport(sv WriteView, height uint64, reporter string, sampled, offline, online []uint32) {
	if sv == nil {
		return
	}
	r := BlockReport{Reporter: reporter, Sampled: sampled, Offline: offline, Online: online}
	data, _ := json.Marshal(r)
	sv.Set(keys.KeyLivenessReport(height), data)
}

func (t *Tracker) persistGlobal(sv WriteView) {
	if sv == nil {
		return
	}
	g := GlobalInfo{
		NextIndex:                        t.nextIndex,
		ActiveCount:                      t.activeCount,
		ProbationCnt:                     t.probationCnt,
		WindowSize:                       t.windowSize,
		LastProcessedHeight:              t.lastProcessedHeight,
		LastVaultEligibilityChangeHeight: t.lastVaultEligibilityChangeHeight,
	}
	data, _ := json.Marshal(g)
	sv.Set(keys.KeyLivenessGlobal(), data)
}

func (t *Tracker) loadFromDB() {
	if t.db == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	                        
	if data, err := t.db.Get(keys.KeyLivenessGlobal()); err == nil && len(data) > 0 {
		var g GlobalInfo
		if json.Unmarshal(data, &g) == nil {
			t.nextIndex = g.NextIndex
			t.activeCount = g.ActiveCount
			t.probationCnt = g.ProbationCnt
			t.windowSize = g.WindowSize
			t.lastProcessedHeight = g.LastProcessedHeight
			t.lastVaultEligibilityChangeHeight = g.LastVaultEligibilityChangeHeight
		}
	}

	                           
	for idx := uint32(0); idx < t.nextIndex; idx++ {
		data, err := t.db.Get(keys.KeyLivenessMiner(idx))
		if err != nil || len(data) == 0 {
			continue
		}
		var m MinerInfo
		if json.Unmarshal(data, &m) == nil {
			t.normalizeStakeBucketsLocked(&m)
			t.capStakeBucketsLocked(&m)
			t.miners[m.Index] = &m
			t.addrToIndex[m.Address] = m.Index
		}
	}

	                     
	for idx := uint32(0); idx < t.nextIndex; idx++ {
		data, err := t.db.Get(keys.KeyLivenessCounter(idx))
		if err != nil || len(data) == 0 {
			continue
		}
		var c CounterInfo
		if json.Unmarshal(data, &c) == nil {
			if c.Samples > 0 {
				t.sampleCount[idx] = c.Samples
			}
			if c.Offline > 0 {
				t.offlineCount[idx] = c.Offline
			}
		}
	}

	                                                  
	                                                                                                               
	                                               
	reportPrefix := keys.KeyLivenessReportPrefix()
	if kvs, err := t.db.Scan(reportPrefix); err == nil {
		for k, v := range kvs {
			h := parseHeightFromReportKey(k, reportPrefix)
			if h == 0 {
				continue
			}
			var r BlockReport
			if json.Unmarshal(v, &r) == nil {
				t.reportHistory[h] = r
				if h > t.lastProcessedHeight {
					t.lastProcessedHeight = h
				}
			}
		}
	}
	t.rebuildCountersFromReportsLocked()
	t.consensusSnapshotHeight = t.lastProcessedHeight
	t.consensusSnapshotCount = t.activeCount

	if t.nextIndex > 0 {
		logs.Info("[Liveness] Restored from DB: nextIndex=%d, active=%d, probation=%d, window=%d, miners=%d, reports=%d",
			t.nextIndex, t.activeCount, t.probationCnt, t.windowSize, len(t.miners), len(t.reportHistory))
	}
}

func (t *Tracker) rebuildCountersFromReportsLocked() {
	t.sampleCount = make(map[uint32]int)
	t.offlineCount = make(map[uint32]int)
	t.sampleReporterRefs = make(map[uint32]map[string]int)
	t.offlineReporterRefs = make(map[uint32]map[string]int)
	heights := make([]uint64, 0, len(t.reportHistory))
	for h := range t.reportHistory {
		heights = append(heights, h)
	}
	sort.Slice(heights, func(i, j int) bool { return heights[i] < heights[j] })
	for _, h := range heights {
		r := t.reportHistory[h]
		for _, idx := range r.Sampled {
			t.sampleCount[idx]++
			t.incrementReporterRefLocked(t.sampleReporterRefs, idx, r.Reporter)
		}
		for _, idx := range r.Offline {
			t.offlineCount[idx]++
			t.incrementReporterRefLocked(t.offlineReporterRefs, idx, r.Reporter)
		}
	}
}

                                                                                      
func (t *Tracker) GetOfflineCountForTest(idx uint32) int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.offlineCount[idx]
}

                                                                                     
func (t *Tracker) GetSampleCountForTest(idx uint32) int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.sampleCount[idx]
}

                                                                   
                                                                                                
func parseHeightFromReportKey(key, prefix string) uint64 {
	if !strings.HasPrefix(key, prefix) {
		return 0
	}
	s := strings.TrimLeft(key[len(prefix):], "0")
	if s == "" {
		return 0
	}
	h, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0
	}
	return h
}
