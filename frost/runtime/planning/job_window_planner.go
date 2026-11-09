                                               
                                                                                              

package planning

import (
	"bytes"
	chainpkg "dex/frost/chain"
	"dex/keys"
	"dex/pb"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
)

                                       
type JobWindowPlanner struct {
	stateReader     ChainStateReader
	adapterFactory  chainpkg.ChainAdapterFactory
	maxInFlight     int                                                      
	transientLogs   map[string][]*pb.FrostPlanningLog
	transientCache  map[uint32]*big.Int                                                                         
	btcPlans        map[string]*btcIsolatedParentPlan
	btcReadyCursor  map[string]string
	btcActiveCursor map[string]string
	btcReadyTurn    map[string]bool
	btcReserved     map[string]bool
	btcPlanBytes    int
}

                                                       
func NewJobWindowPlanner(stateReader ChainStateReader, adapterFactory chainpkg.ChainAdapterFactory, maxInFlight int) *JobWindowPlanner {
	if maxInFlight <= 0 {
		maxInFlight = 1             
	}
	return &JobWindowPlanner{
		stateReader:    stateReader,
		adapterFactory: adapterFactory,
		maxInFlight:    maxInFlight,
		transientLogs:  make(map[string][]*pb.FrostPlanningLog),
	}
}

                                                                  
                                                                                    
func (p *JobWindowPlanner) PlanJobWindow(chain, asset string) ([]*Job, map[string][]*pb.FrostPlanningLog, error) {
	                                                                              
	p.transientLogs = make(map[string][]*pb.FrostPlanningLog)
	p.transientCache = make(map[uint32]*big.Int)
	chain = chainpkg.NormalizeChain(chain)
	asset = strings.TrimSpace(asset)
	if chain != chainpkg.ChainBTC {
		jobs, err := p.planMixedAccountJobs(chain)
		return jobs, p.transientLogs, err
	}
	isolated, err := chainpkg.BTCIsolationActive(p.stateReader.Get)
	if err != nil {
		return nil, nil, err
	}
	if isolated {
		jobs, err := p.planBTCIsolatedWindow(asset)
		return jobs, p.transientLogs, err
	}

	                                                                        
	                                                                     
	claimedJobs, err := p.scanClaimedJobs(chain, asset)
	if err != nil {
		return nil, nil, err
	}
	if len(claimedJobs) > 0 {
		return claimedJobs, p.transientLogs, nil
	}

	                                                                             
	withdraws, err := p.scanQueuedWithdraws(chain, asset, chainpkg.MaxBTCWithdrawalsPerBatch)
	if err != nil {
		return nil, nil, err
	}
	if len(withdraws) == 0 {
		return nil, p.transientLogs, nil
	}
	if len(withdraws) < chainpkg.MaxBTCWithdrawalsPerBatch {
		currentHeight := uint64(0)
		if raw, found, readErr := p.stateReader.Get(keys.KeyLatestHeight()); readErr != nil {
			return nil, nil, readErr
		} else if found && len(raw) > 0 {
			currentHeight, err = strconv.ParseUint(string(raw), 10, 64)
			if err != nil {
				return nil, nil, fmt.Errorf("invalid latest height: %w", err)
			}
		}
		oldest := withdraws[0].state.RequestHeight
		if currentHeight < oldest || currentHeight-oldest < chainpkg.BTCWithdrawBatchTimeoutBlocks {
			return nil, p.transientLogs, nil
		}
	}
	jobs, err := p.planBTCJobWindow(chain, asset, withdraws)

	if err != nil {
		return nil, nil, err
	}
	return jobs, p.transientLogs, nil
}

                                                                           
                                                                            
                                                                             
func (p *JobWindowPlanner) scanClaimedJobs(chain, asset string) ([]*Job, error) {
	if p == nil || p.stateReader == nil {
		return nil, nil
	}

	chainName := chainpkg.NormalizeChain(chain)
	assetName := strings.TrimSpace(asset)

	head := uint64(1)
	if data, exists, err := p.stateReader.Get(keys.KeyFrostWithdrawFIFOHead(chainName, assetName)); err != nil {
		return nil, err
	} else if exists && len(data) > 0 {
		if parsed, parseErr := strconv.ParseUint(string(data), 10, 64); parseErr == nil && parsed > 0 {
			head = parsed
		}
	}
	seqData, exists, err := p.stateReader.Get(keys.KeyFrostWithdrawFIFOSeq(chainName, assetName))
	if err != nil {
		return nil, err
	}
	if !exists || len(seqData) == 0 {
		return nil, nil
	}
	maxSeq, err := strconv.ParseUint(string(seqData), 10, 64)
	if err != nil || head > maxSeq {
		return nil, nil
	}

	jobs := make([]*Job, 0, p.maxInFlight)
	for nextSeq := head; nextSeq <= maxSeq && len(jobs) < p.maxInFlight; {
		withdrawID, exists, err := p.stateReader.Get(keys.KeyFrostWithdrawFIFOIndex(chainName, assetName, nextSeq))
		if err != nil {
			return nil, err
		}
		if !exists || len(withdrawID) == 0 {
			return jobs, nil
		}
		withdrawData, exists, err := p.stateReader.Get(keys.KeyFrostWithdraw(string(withdrawID)))
		if err != nil {
			return nil, err
		}
		if !exists || len(withdrawData) == 0 {
			return nil, fmt.Errorf("withdraw %s at FIFO head %d not found", withdrawID, nextSeq)
		}
		var headWithdraw pb.FrostWithdrawState
		if err := proto.Unmarshal(withdrawData, &headWithdraw); err != nil {
			return nil, fmt.Errorf("decode withdraw %s at FIFO head: %w", withdrawID, err)
		}
		if headWithdraw.Status != "SIGNING" {
			return jobs, nil
		}
		if strings.TrimSpace(headWithdraw.JobId) == "" {
			return nil, fmt.Errorf("SIGNING withdraw %s has no claimed job", headWithdraw.WithdrawId)
		}

		job, lastSeq, err := p.recoverClaimedJob(chainName, assetName, nextSeq, headWithdraw.JobId)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
		if lastSeq == ^uint64(0) {
			break
		}
		nextSeq = lastSeq + 1
	}
	return jobs, nil
}

func (p *JobWindowPlanner) recoverClaimedJob(chainName, asset string, firstSeq uint64, jobID string) (*Job, uint64, error) {
	data, exists, err := p.stateReader.Get(keys.KeyFrostWithdrawJob(jobID))
	if err != nil {
		return nil, 0, err
	}
	if !exists || len(data) == 0 {
		return nil, 0, fmt.Errorf("claimed withdraw job %s not found", jobID)
	}
	var state pb.FrostWithdrawJobState
	if err := proto.Unmarshal(data, &state); err != nil {
		return nil, 0, fmt.Errorf("decode claimed withdraw job %s: %w", jobID, err)
	}
	if state.Status != "CLAIMED" || state.JobId != jobID || chainpkg.NormalizeChain(state.Chain) != chainName ||
		!strings.EqualFold(strings.TrimSpace(state.Asset), asset) || len(state.WithdrawIds) == 0 ||
		len(state.TemplateHash) == 0 || len(state.TemplateData) == 0 {
		return nil, 0, fmt.Errorf("claimed withdraw job %s is incomplete or mismatched", jobID)
	}
	if state.FirstSeq != firstSeq || state.LastSeq < state.FirstSeq ||
		state.LastSeq-state.FirstSeq+1 != uint64(len(state.WithdrawIds)) {
		return nil, 0, fmt.Errorf("claimed withdraw job %s FIFO range is invalid", jobID)
	}
	expectedJobID := chainpkg.ComputeWithdrawJobID(state.Chain, state.Asset, state.VaultId, state.FirstSeq, state.TemplateHash, state.KeyEpoch)
	if expectedJobID != state.JobId {
		return nil, 0, fmt.Errorf("claimed withdraw job %s ID does not bind finalized template", jobID)
	}
	if p.adapterFactory == nil {
		return nil, 0, fmt.Errorf("claimed withdraw job %s template validator is unavailable", jobID)
	}
	adapter, err := p.adapterFactory.Adapter(state.Chain)
	if err != nil {
		return nil, 0, fmt.Errorf("load claimed withdraw job %s template validator: %w", jobID, err)
	}
	recomputedHash, err := adapter.TemplateHash(state.TemplateData)
	if err != nil || !bytes.Equal(recomputedHash, state.TemplateHash) {
		return nil, 0, fmt.Errorf("claimed withdraw job %s template data/hash mismatch", jobID)
	}

	indexKeys := make([]string, len(state.WithdrawIds))
	stateKeys := make([]string, len(state.WithdrawIds))
	for i, id := range state.WithdrawIds {
		indexKeys[i] = keys.KeyFrostWithdrawFIFOIndex(chainName, asset, state.FirstSeq+uint64(i))
		stateKeys[i] = keys.KeyFrostWithdraw(id)
	}
	indexed, err := getMany(p.stateReader, indexKeys)
	if err != nil {
		return nil, 0, err
	}
	withdrawStates, err := getMany(p.stateReader, stateKeys)
	if err != nil {
		return nil, 0, err
	}
	total := big.NewInt(0)
	for i, expectedWithdrawID := range state.WithdrawIds {
		seq := state.FirstSeq + uint64(i)
		if string(indexed[indexKeys[i]]) != expectedWithdrawID {
			return nil, 0, fmt.Errorf("claimed withdraw job %s FIFO index %d mismatch", jobID, seq)
		}
		withdrawData := withdrawStates[stateKeys[i]]
		if len(withdrawData) == 0 {
			return nil, 0, fmt.Errorf("claimed withdraw %s not found", expectedWithdrawID)
		}
		var withdraw pb.FrostWithdrawState
		if err := proto.Unmarshal(withdrawData, &withdraw); err != nil {
			return nil, 0, fmt.Errorf("decode claimed withdraw %s: %w", expectedWithdrawID, err)
		}
		if withdraw.WithdrawId != expectedWithdrawID || withdraw.Seq != seq || withdraw.Status != "SIGNING" ||
			withdraw.JobId != state.JobId || withdraw.VaultId != state.VaultId ||
			chainpkg.NormalizeChain(withdraw.Chain) != chainName || !strings.EqualFold(strings.TrimSpace(withdraw.Asset), asset) {
			return nil, 0, fmt.Errorf("claimed withdraw %s does not match job %s", expectedWithdrawID, jobID)
		}
		if chainName != chainpkg.ChainBTC {
			amount, err := chainpkg.ParseWithdrawAmount(withdraw.Amount)
			if err != nil {
				return nil, 0, fmt.Errorf("claimed withdraw %s amount is invalid", expectedWithdrawID)
			}
			total.Add(total, amount)
			if total.BitLen() > 256 {
				return nil, 0, fmt.Errorf("claimed withdraw job %s amount overflows uint256", jobID)
			}
		}
	}

	if chainName != chainpkg.ChainBTC {
		reserved, err := chainpkg.ParseWithdrawAmount(state.ReservedAmount)
		if err != nil || reserved.Cmp(total) != 0 {
			return nil, 0, fmt.Errorf("claimed withdraw job %s reservation is missing or mismatched", jobID)
		}
		reservedData, exists, err := p.stateReader.Get(keys.KeyFrostVaultReservedBalance(chainName, state.Asset, state.VaultId))
		if err != nil {
			return nil, 0, err
		}
		aggregate, parseErr := parseNonNegativeAmount(string(reservedData))
		if !exists || parseErr != nil || aggregate.Cmp(reserved) < 0 {
			return nil, 0, fmt.Errorf("claimed withdraw job %s vault reservation is insufficient", jobID)
		}
	}

	return &Job{
		JobID:        state.JobId,
		Chain:        state.Chain,
		Asset:        state.Asset,
		VaultID:      state.VaultId,
		KeyEpoch:     state.KeyEpoch,
		WithdrawIDs:  append([]string(nil), state.WithdrawIds...),
		TemplateHash: append([]byte(nil), state.TemplateHash...),
		TemplateData: append([]byte(nil), state.TemplateData...),
		FirstSeq:     state.FirstSeq,
	}, state.LastSeq, nil
}

                                                                                
func (p *JobWindowPlanner) planBTCJobWindow(chain, asset string, withdraws []*ScanResult) ([]*Job, error) {
	if len(withdraws) == 0 {
		return nil, nil
	}
	batch, err := p.prepareBTCWithdrawBatch(withdraws)
	if err != nil {
		for _, wd := range withdraws {
			p.reportTransientLog(wd.WithdrawID, "EstimateBTCNeed", "FAILED", fmt.Sprintf("Failed: %v", err))
		}
		return nil, nil
	}
	vaultID, keyEpoch, err := p.selectVault(chain, asset, new(big.Int).SetUint64(batch.grossTotal))
	if err != nil {
		for _, wd := range withdraws {
			p.reportTransientLog(wd.WithdrawID, "SelectVault", "FAILED", fmt.Sprintf("Failed to select vault: %v", err))
		}
		return nil, nil
	}
	job, err := p.planBTCJobFromBatch(chain, asset, vaultID, keyEpoch, batch)
	if err != nil {
		for _, wd := range withdraws {
			p.reportTransientLog(wd.WithdrawID, "PlanBTCJob", "FAILED", fmt.Sprintf("Failed: %v", err))
		}
		return nil, nil
	}
	if job == nil || len(job.WithdrawIDs) == 0 {
		return nil, nil
	}
	return []*Job{job}, nil
}

                                                                               
                                                                     
func (p *JobWindowPlanner) planAccountChainJobWindow(chain, asset string, withdraws []*ScanResult) ([]*Job, error) {
	if len(withdraws) == 0 {
		return nil, nil
	}
	stateKeys := make([]string, len(withdraws))
	for i, withdraw := range withdraws {
		stateKeys[i] = keys.KeyFrostWithdraw(withdraw.WithdrawID)
	}
	stateValues, err := getMany(p.stateReader, stateKeys)
	if err != nil {
		return nil, err
	}
	prefixTotals := make([]*big.Int, len(withdraws))
	runningTotal := big.NewInt(0)
	for i, candidate := range withdraws {
		data := stateValues[stateKeys[i]]
		if len(data) == 0 {
			return nil, fmt.Errorf("withdraw not found: %s", candidate.WithdrawID)
		}
		var state pb.FrostWithdrawState
		if err := proto.Unmarshal(data, &state); err != nil {
			return nil, err
		}
		amount, err := chainpkg.ParseWithdrawAmount(state.Amount)
		if err != nil {
			return nil, err
		}
		runningTotal = new(big.Int).Add(runningTotal, amount)
		if runningTotal.BitLen() > 256 {
			return nil, fmt.Errorf("account withdraw batch total overflows uint256")
		}
		prefixTotals[i] = chainpkg.CloneAmount(runningTotal)
	}
	vaultID, keyEpoch, prefixLen, err := p.selectAccountVaultForPrefix(chain, asset, prefixTotals)
	if err != nil {
		p.reportTransientLog(withdraws[0].WithdrawID, "SelectVault", "FAILED", fmt.Sprintf("Failed to select vault: %v", err))
		return nil, nil
	}
	ids := make([]string, prefixLen)
	for i := range ids {
		ids[i] = withdraws[i].WithdrawID
	}
	total := prefixTotals[prefixLen-1]
	adapter, err := p.adapterFactory.Adapter(chain)
	if err != nil {
		return nil, err
	}
	params, firstSeq, err := BuildAccountTemplateParams(p.stateReader, chain, asset, vaultID, keyEpoch, ids, nil)
	if err != nil {
		return nil, err
	}
	result, err := adapter.BuildWithdrawTemplate(params)
	if err != nil {
		return nil, err
	}
	job := &Job{JobID: generateJobID(chain, asset, vaultID, firstSeq, result.TemplateHash, keyEpoch), Chain: chain, Asset: asset, VaultID: vaultID, KeyEpoch: keyEpoch, WithdrawIDs: ids, TemplateHash: result.TemplateHash, TemplateData: result.TemplateData, FirstSeq: firstSeq}
	job.Logs = append(job.Logs, &pb.FrostPlanningLog{Step: "SelectVault", Status: "OK", Message: fmt.Sprintf("Vault %d covers %d FIFO withdrawals", vaultID, len(ids)), Timestamp: uint64(time.Now().UnixMilli())})
	p.deductVaultBalance(vaultID, total)
	return []*Job{job}, nil
}

                                                      
func (p *JobWindowPlanner) scanQueuedWithdraws(chain, asset string, maxCount int) ([]*ScanResult, error) {
	                       
	headKey := keys.KeyFrostWithdrawFIFOHead(chain, asset)
	headData, exists, err := p.stateReader.Get(headKey)
	if err != nil {
		return nil, err
	}

	var head uint64 = 1
	if exists && len(headData) > 0 {
		head, _ = strconv.ParseUint(string(headData), 10, 64)
		if head == 0 {
			head = 1
		}
	}

	                      
	seqKey := keys.KeyFrostWithdrawFIFOSeq(chain, asset)
	seqData, exists, err := p.stateReader.Get(seqKey)
	if err != nil {
		return nil, err
	}
	if !exists || len(seqData) == 0 {
		return nil, nil
	}

	maxSeq, _ := strconv.ParseUint(string(seqData), 10, 64)
	if head > maxSeq {
		return nil, nil
	}

	if maxCount <= 0 {
		return nil, nil
	}
	results := make([]*ScanResult, 0, maxCount)
	for cursor := head; cursor <= maxSeq && len(results) < maxCount; {
		count := maxCount - len(results)
		if maxSeq-cursor < uint64(count-1) {
			count = int(maxSeq-cursor) + 1
		}
		indexKeys := make([]string, count)
		for i := range indexKeys {
			indexKeys[i] = keys.KeyFrostWithdrawFIFOIndex(chain, asset, cursor+uint64(i))
		}
		indexValues, err := getMany(p.stateReader, indexKeys)
		if err != nil {
			return nil, err
		}
		stateKeys := make([]string, count)
		for i, key := range indexKeys {
			id := string(indexValues[key])
			if id == "" {
				stateKeys = stateKeys[:i]
				break
			}
			stateKeys[i] = keys.KeyFrostWithdraw(id)
		}
		stateValues, err := getMany(p.stateReader, stateKeys)
		if err != nil {
			return nil, err
		}
		for i, key := range stateKeys {
			id := string(indexValues[indexKeys[i]])
			data := stateValues[key]
			if len(data) == 0 {
				return results, nil
			}
			state := &pb.FrostWithdrawState{}
			if err := proto.Unmarshal(data, state); err != nil {
				return nil, err
			}
			if state.WithdrawId != id || state.Seq != cursor+uint64(i) ||
				chainpkg.NormalizeChain(state.Chain) != chain || !strings.EqualFold(strings.TrimSpace(state.Asset), asset) {
				return results, nil
			}
			if chain == chainpkg.ChainBTC && len(results) == 0 && state.Status == "CANCELLED" {
				continue                                                                    
			}
			if state.Status != "QUEUED" {
				return results, nil                                                
			}
			results = append(results, &ScanResult{Chain: chain, Asset: asset, WithdrawID: id, Seq: state.Seq, state: state})
		}
		if len(stateKeys) < count || maxSeq-cursor < uint64(count) {
			break
		}
		cursor += uint64(count)
	}
	return results, nil
}

                                                   
func (p *JobWindowPlanner) planJobForWithdraw(scanResult *ScanResult) (*Job, error) {
	                            
	withdrawKey := keys.KeyFrostWithdraw(scanResult.WithdrawID)
	withdrawData, exists, err := p.stateReader.Get(withdrawKey)
	if err != nil {
		return nil, err
	}
	if !exists || len(withdrawData) == 0 {
		return nil, nil
	}

	state := &pb.FrostWithdrawState{}
	if err := proto.Unmarshal(withdrawData, state); err != nil {
		return nil, err
	}

	                                                         
	amount, err := chainpkg.ParseWithdrawAmount(state.Amount)
	if err != nil {
		return nil, fmt.Errorf("invalid withdraw amount for %s: %w", scanResult.WithdrawID, err)
	}
	vaultID, keyEpoch, err := p.selectVault(scanResult.Chain, scanResult.Asset, amount)
	if err != nil {
		return nil, err
	}

	                        
	                                                                                         
	if scanResult.Chain == "btc" {
		return p.planBTCJob(scanResult.Chain, scanResult.Asset, vaultID, keyEpoch, []*ScanResult{scanResult})
	}

	adapter, err := p.adapterFactory.Adapter(scanResult.Chain)
	if err != nil {
		return nil, err
	}

	                                                                 
	params, firstSeq, err := BuildAccountTemplateParams(
		p.stateReader,
		scanResult.Chain,
		scanResult.Asset,
		vaultID,
		keyEpoch,
		[]string{scanResult.WithdrawID},
		nil,
	)
	if err != nil {
		return nil, err
	}

	                  
	result, err := adapter.BuildWithdrawTemplate(params)
	if err != nil {
		return nil, err
	}

	                   
	jobID := generateJobID(scanResult.Chain, scanResult.Asset, vaultID, firstSeq, result.TemplateHash, keyEpoch)

	return &Job{
		JobID:        jobID,
		Chain:        scanResult.Chain,
		Asset:        scanResult.Asset,
		VaultID:      vaultID,
		KeyEpoch:     keyEpoch,
		WithdrawIDs:  []string{scanResult.WithdrawID},
		TemplateHash: result.TemplateHash,
		TemplateData: result.TemplateData,
		FirstSeq:     firstSeq,
	}, nil
}

                                                                              
func (p *JobWindowPlanner) reportTransientLog(withdrawID, step, status, message string) {
	if p.transientLogs == nil {
		p.transientLogs = make(map[string][]*pb.FrostPlanningLog)
	}
	p.transientLogs[withdrawID] = append(p.transientLogs[withdrawID], &pb.FrostPlanningLog{
		Step:      step,
		Status:    status,
		Message:   message,
		Timestamp: uint64(time.Now().UnixMilli()),
	})
}

func (p *JobWindowPlanner) reportWindowBlocked(withdraws []*ScanResult, skip map[string]bool, step, status, message string) {
	for _, wd := range withdraws {
		if skip != nil && skip[wd.WithdrawID] {
			continue
		}
		p.reportTransientLog(wd.WithdrawID, step, status, message)
	}
}
