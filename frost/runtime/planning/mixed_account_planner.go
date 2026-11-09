package planning

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	chainpkg "dex/frost/chain"
	"dex/frost/chain/evm"
	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

func (p *JobWindowPlanner) planMixedAccountJobs(chainName string) ([]*Job, error) {
	chainName = chainpkg.NormalizeChain(chainName)
	cfgData, exists, err := p.stateReader.Get(keys.KeyFrostVaultConfig(chainName, 0))
	if err != nil || !exists || len(cfgData) == 0 {
		return nil, err
	}
	var cfg pb.FrostVaultConfig
	if proto.Unmarshal(cfgData, &cfg) != nil || cfg.VaultCount == 0 {
		return nil, fmt.Errorf("invalid Vault config for %s", chainName)
	}
	currentHeight := uint64(0)
	if raw, found, readErr := p.stateReader.Get(keys.KeyLatestHeight()); readErr != nil {
		return nil, readErr
	} else if found && len(raw) > 0 {
		currentHeight, err = strconv.ParseUint(string(raw), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid latest height: %w", err)
		}
	}

	baseKeys := make([]string, 0, int(cfg.VaultCount)*5)
	for id := uint32(0); id < cfg.VaultCount; id++ {
		baseKeys = append(baseKeys,
			keys.KeyFrostVaultState(chainName, id),
			keys.KeyFrostWithdrawActiveVaultJob(chainName, id),
			keys.KeyFrostWithdrawLiveHead(chainName, id),
			keys.KeyFrostWithdrawLiveTail(chainName, id),
			keys.KeyFrostWithdrawLivePending(chainName, id),
		)
	}
	base, err := getMany(p.stateReader, baseKeys)
	if err != nil {
		return nil, err
	}
	jobs := make([]*Job, 0, cfg.VaultCount)
	for id := uint32(0); id < cfg.VaultCount; id++ {
		if activeID := strings.TrimSpace(string(base[keys.KeyFrostWithdrawActiveVaultJob(chainName, id)])); activeID != "" {
			job, err := p.recoverMixedAccountJob(chainName, id, activeID)
			if err != nil {
				return nil, err
			}
			jobs = append(jobs, job)
			continue
		}
		var vault pb.FrostVaultState
		if data := base[keys.KeyFrostVaultState(chainName, id)]; len(data) == 0 || proto.Unmarshal(data, &vault) != nil || vault.Status != "ACTIVE" || vault.KeyEpoch == 0 {
			continue
		}
		pending, err := parsePlannerUint64(base[keys.KeyFrostWithdrawLivePending(chainName, id)])
		if err != nil {
			return nil, err
		}
		if pending == 0 {
			continue
		}
		head, err := parsePlannerUint64(base[keys.KeyFrostWithdrawLiveHead(chainName, id)])
		if err != nil {
			return nil, err
		}
		if head == 0 {
			head = 1
		}
		tail, err := parsePlannerUint64(base[keys.KeyFrostWithdrawLiveTail(chainName, id)])
		if err != nil || tail < head {
			return nil, fmt.Errorf("invalid mixed withdrawal FIFO for %s/%d", chainName, id)
		}
		limit := pending
		if limit > chainpkg.MaxWithdrawalsPerBatch {
			limit = chainpkg.MaxWithdrawalsPerBatch
		}
		var states []*pb.FrostWithdrawState
		if pending < chainpkg.MaxWithdrawalsPerBatch {
			                                                                   
			                                                                   
			                                                               
			first, err := p.readMixedLivePrefix(chainName, id, head, tail, 1)
			if err != nil {
				return nil, err
			}
			if len(first) != 1 {
				return nil, fmt.Errorf("mixed withdrawal pending/index mismatch for %s/%d", chainName, id)
			}
			oldest := first[0].RequestHeight
			if currentHeight < oldest || currentHeight-oldest < chainpkg.AccountWithdrawBatchTimeoutBlocks {
				continue
			}
			states = first
			if limit > 1 && first[0].LiveSeq < tail {
				rest, err := p.readMixedLivePrefix(chainName, id, first[0].LiveSeq+1, tail, int(limit-1))
				if err != nil {
					return nil, err
				}
				states = append(states, rest...)
			}
		} else {
			states, err = p.readMixedLivePrefix(chainName, id, head, tail, int(limit))
			if err != nil {
				return nil, err
			}
		}
		if uint64(len(states)) != limit {
			return nil, fmt.Errorf("mixed withdrawal pending/index mismatch for %s/%d", chainName, id)
		}
		ids := make([]string, len(states))
		for i := range states {
			ids[i] = states[i].WithdrawId
		}
		params, firstSeq, err := BuildAccountTemplateParams(p.stateReader, chainName, "", id, vault.KeyEpoch, ids, nil)
		if err != nil {
			return nil, err
		}
		adapter, err := p.adapterFactory.Adapter(chainName)
		if err != nil {
			return nil, err
		}
		result, err := adapter.BuildWithdrawTemplate(params)
		if err != nil {
			return nil, err
		}
		template, err := evm.FromJSON(result.TemplateData)
		if err != nil {
			return nil, err
		}
		includesHandover := false
		if vault.KeyEpoch > 1 {
			_, consumed, readErr := p.stateReader.Get(keys.KeyFrostVaultHandoverConsumed(chainName, id, vault.KeyEpoch))
			if readErr != nil {
				return nil, readErr
			}
			includesHandover = !consumed
		}
		job := &Job{
			JobID: generateJobID(chainName, "", id, firstSeq, result.TemplateHash, vault.KeyEpoch),
			Chain: chainName, VaultID: id, KeyEpoch: vault.KeyEpoch, WithdrawIDs: ids,
			TemplateHash: result.TemplateHash, TemplateData: result.TemplateData, FirstSeq: firstSeq,
			BatchSeq: template.BatchSeq, RewardTokens: append([]string(nil), template.RewardTokens...), IncludesHandover: includesHandover,
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

                                                                               
                                                                     
func (p *JobWindowPlanner) readMixedLivePrefix(chainName string, vaultID uint32, head, tail uint64, limit int) ([]*pb.FrostWithdrawState, error) {
	out := make([]*pb.FrostWithdrawState, 0, limit)
	for cursor := head; cursor <= tail && len(out) < limit; {
		count := 128
		if remaining := tail - cursor + 1; remaining < uint64(count) {
			count = int(remaining)
		}
		indexKeys := make([]string, count)
		for i := range indexKeys {
			indexKeys[i] = keys.KeyFrostWithdrawLiveIndex(chainName, vaultID, cursor+uint64(i))
		}
		indexes, err := getMany(p.stateReader, indexKeys)
		if err != nil {
			return nil, err
		}
		ids := make([]string, 0, count)
		seqs := make([]uint64, 0, count)
		for i, key := range indexKeys {
			if id := strings.TrimSpace(string(indexes[key])); id != "" {
				ids = append(ids, id)
				seqs = append(seqs, cursor+uint64(i))
				if len(out)+len(ids) == limit {
					break
				}
			}
		}
		if len(ids) == 0 {
			next := cursor + uint64(count)
			if next <= cursor {
				break
			}
			cursor = next
			continue
		}
		stateKeys := make([]string, len(ids))
		for i, id := range ids {
			stateKeys[i] = keys.KeyFrostWithdraw(id)
		}
		values, err := getMany(p.stateReader, stateKeys)
		if err != nil {
			return nil, err
		}
		for i, id := range ids {
			var state pb.FrostWithdrawState
			if data := values[stateKeys[i]]; len(data) == 0 || proto.Unmarshal(data, &state) != nil || state.WithdrawId != id || state.LiveSeq != seqs[i] || state.VaultId != vaultID || state.Status != "QUEUED" {
				return nil, fmt.Errorf("invalid mixed withdrawal state at seq %d", seqs[i])
			}
			out = append(out, &state)
		}
		next := cursor + uint64(count)
		if next <= cursor {
			break
		}
		cursor = next
	}
	return out, nil
}

func (p *JobWindowPlanner) recoverMixedAccountJob(chainName string, vaultID uint32, jobID string) (*Job, error) {
	data, exists, err := p.stateReader.Get(keys.KeyFrostWithdrawJob(jobID))
	if err != nil || !exists || len(data) == 0 {
		return nil, fmt.Errorf("active mixed withdrawal job %s is missing", jobID)
	}
	var state pb.FrostWithdrawJobState
	if proto.Unmarshal(data, &state) != nil || state.JobId != jobID || state.Status != "CLAIMED" || chainpkg.NormalizeChain(state.Chain) != chainName || state.VaultId != vaultID || state.Asset != "" || len(state.WithdrawIds) == 0 || len(state.WithdrawIds) > chainpkg.MaxWithdrawalsPerBatch || state.KeyEpoch == 0 || state.FirstSeq == 0 || state.LastSeq < state.FirstSeq {
		return nil, fmt.Errorf("active mixed withdrawal job %s is invalid", jobID)
	}
	if expected := chainpkg.ComputeWithdrawJobID(state.Chain, "", state.VaultId, state.FirstSeq, state.TemplateHash, state.KeyEpoch); expected != jobID {
		return nil, fmt.Errorf("active mixed withdrawal job %s scope mismatch", jobID)
	}
	adapter, err := p.adapterFactory.Adapter(chainName)
	if err != nil {
		return nil, err
	}
	hash, err := adapter.TemplateHash(state.TemplateData)
	if err != nil || !bytes.Equal(hash, state.TemplateHash) {
		return nil, fmt.Errorf("active mixed withdrawal job %s template mismatch", jobID)
	}
	template, err := evm.FromJSON(state.TemplateData)
	if err != nil || template.BatchSeq != state.BatchSeq || len(template.WithdrawIDs) != len(state.WithdrawIds) || len(template.RewardTokens) != len(state.RewardTokens) {
		return nil, fmt.Errorf("active mixed withdrawal job %s finalized fields mismatch", jobID)
	}
	if _, err := evm.ParseBatchSeq(state.BatchSeq); err != nil {
		return nil, fmt.Errorf("active mixed withdrawal job %s batch sequence is invalid", jobID)
	}
	for i := range state.WithdrawIds {
		if template.WithdrawIDs[i] != state.WithdrawIds[i] {
			return nil, fmt.Errorf("active mixed withdrawal job %s withdrawal order mismatch", jobID)
		}
	}
	for i := range state.RewardTokens {
		if template.RewardTokens[i] != state.RewardTokens[i] {
			return nil, fmt.Errorf("active mixed withdrawal job %s reward tokens mismatch", jobID)
		}
	}
	return &Job{
		JobID: jobID, Chain: state.Chain, VaultID: state.VaultId, KeyEpoch: state.KeyEpoch,
		WithdrawIDs: append([]string(nil), state.WithdrawIds...), TemplateHash: append([]byte(nil), state.TemplateHash...),
		TemplateData: append([]byte(nil), state.TemplateData...), FirstSeq: state.FirstSeq, BatchSeq: state.BatchSeq,
		RewardTokens: append([]string(nil), state.RewardTokens...), IncludesHandover: state.IncludesHandover,
	}, nil
}

func parsePlannerUint64(raw []byte) (uint64, error) {
	if len(raw) == 0 {
		return 0, nil
	}
	value := string(raw)
	if strings.TrimSpace(value) != value || (len(value) > 1 && value[0] == '0') {
		return 0, fmt.Errorf("invalid canonical uint64")
	}
	return strconv.ParseUint(value, 10, 64)
}
