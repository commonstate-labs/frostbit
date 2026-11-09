package vm

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"dex/frost/chain"
	"dex/frost/chain/evm"
	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

func (h *FrostWithdrawClaimTxHandler) dryRunMixedAccountClaim(claim *pb.FrostWithdrawClaimTx, sv StateView) ([]WriteOp, *Receipt, error) {
	txID := claim.GetBase().GetTxId()
	fail := func(message string, err error) ([]WriteOp, *Receipt, error) {
		if err == nil {
			err = errors.New(message)
		}
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: message}, err
	}
	chainName := chain.NormalizeChain(claim.Chain)
	jobID := strings.TrimSpace(claim.JobId)
	if jobID == "" || claim.Asset != "" || len(claim.WithdrawIds) == 0 || len(claim.WithdrawIds) > chain.MaxWithdrawalsPerBatch || len(claim.TemplateHash) == 0 || len(claim.TemplateData) == 0 {
		return fail("invalid mixed account withdrawal claim", nil)
	}
	batchSeq, err := evm.ParseBatchSeq(claim.BatchSeq)
	if err != nil {
		return fail("invalid account withdrawal batch_seq", err)
	}
	if existing, exists, err := readFrostWithdrawJobState(sv, jobID); err != nil {
		return fail("failed to read withdrawal job", err)
	} else if exists {
		if sameWithdrawJobClaim(existing, claim, chainName, "") && existing.BatchSeq == claim.BatchSeq && existing.IncludesHandover == claim.IncludesHandover && (existing.Status == frostWithdrawJobStatusClaimed || existing.Status == frostWithdrawJobStatusSigned) {
			if err := validateExistingWithdrawClaimCoordinator(sv, existing, claim.Base.FromAddress); err != nil {
				return fail(err.Error(), err)
			}
			return nil, &Receipt{TxID: txID, Status: "SUCCEED"}, nil
		}
		return fail("withdrawal job already exists with different claim", nil)
	}
	activeKey := keys.KeyFrostWithdrawActiveVaultJob(chainName, claim.VaultId)
	if active, exists, err := sv.Get(activeKey); err != nil {
		return fail("failed to read active Vault job", err)
	} else if exists && len(active) > 0 {
		return fail("physical Vault already has an active withdrawal job", nil)
	}
	if draining, err := vaultDrainingForClaim(sv, chainName, claim.VaultId, claim.KeyEpoch); err != nil {
		return fail("failed to inspect vault transition", err)
	} else if draining {
		return fail("vault is draining; new old-key withdraw claims are paused", nil)
	}

	vaultData, exists, err := sv.Get(keys.KeyFrostVaultState(chainName, claim.VaultId))
	if err != nil || !exists || len(vaultData) == 0 {
		return fail("account withdrawal Vault state not found", err)
	}
	var vault pb.FrostVaultState
	if proto.Unmarshal(vaultData, &vault) != nil || vault.VaultId != claim.VaultId || chain.NormalizeChain(vault.Chain) != chainName || vault.Status != "ACTIVE" || vault.KeyEpoch != claim.KeyEpoch || vault.SignAlgo != pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128 || len(vault.GroupPubkey) != 64 || len(vault.CommitteeMembers) == 0 {
		return fail("account withdrawal Vault context mismatch", nil)
	}

	queueKeys := []string{keys.KeyFrostWithdrawLiveHead(chainName, claim.VaultId), keys.KeyFrostWithdrawLiveTail(chainName, claim.VaultId), keys.KeyFrostWithdrawLivePending(chainName, claim.VaultId)}
	queue, err := frostStateGetMany(sv, queueKeys)
	if err != nil {
		return fail("failed to read mixed withdrawal FIFO", err)
	}
	head, err := parseOptionalUint64(queue[queueKeys[0]])
	if err != nil {
		return fail("invalid mixed withdrawal FIFO head", err)
	}
	if head == 0 {
		head = 1
	}
	tail, err := parseOptionalUint64(queue[queueKeys[1]])
	if err != nil || tail < head {
		return fail("invalid mixed withdrawal FIFO tail", err)
	}
	pending, err := parseOptionalUint64(queue[queueKeys[2]])
	if err != nil || pending == 0 {
		return fail("invalid mixed withdrawal pending count", err)
	}
	required := pending
	if required > chain.MaxWithdrawalsPerBatch {
		required = chain.MaxWithdrawalsPerBatch
	}
	var withdraws []*FrostWithdrawRequest
	if pending < chain.MaxWithdrawalsPerBatch {
		                                                                    
		                                                               
		                                                                      
		first, readErr := readMixedClaimPrefix(sv, chainName, claim.VaultId, head, tail, 1)
		if readErr != nil || len(first) != 1 {
			return fail("mixed withdrawal FIFO is inconsistent", readErr)
		}
		oldest := first[0].RequestHeight
		if claim.Base.ExecutedHeight < oldest || claim.Base.ExecutedHeight-oldest < chain.AccountWithdrawBatchTimeoutBlocks {
			return fail("mixed withdrawal batch has not reached count or age trigger", nil)
		}
		withdraws = first
		if required > 1 && first[0].LiveSeq < tail {
			rest, readErr := readMixedClaimPrefix(sv, chainName, claim.VaultId, first[0].LiveSeq+1, tail, int(required-1))
			if readErr != nil {
				return fail("mixed withdrawal FIFO is inconsistent", readErr)
			}
			withdraws = append(withdraws, rest...)
		}
	} else {
		withdraws, err = readMixedClaimPrefix(sv, chainName, claim.VaultId, head, tail, int(required))
		if err != nil {
			return fail("mixed withdrawal FIFO is inconsistent", err)
		}
	}
	if uint64(len(withdraws)) != required {
		return fail("mixed withdrawal FIFO is inconsistent", nil)
	}
	if len(claim.WithdrawIds) != len(withdraws) {
		return fail("claim does not cover canonical mixed FIFO prefix", nil)
	}
	for i := range withdraws {
		if claim.WithdrawIds[i] != withdraws[i].WithdrawID {
			return fail("claim withdraw_ids do not match canonical mixed FIFO prefix", nil)
		}
	}

	nextSeqRaw, nextExists, err := sv.Get(keys.KeyFrostVaultDEXNextBatchSeq(chainName, claim.VaultId))
	if err != nil {
		return fail("failed to read DEX next batch sequence", err)
	}
	expectedSeq := "1"
	if nextExists && len(nextSeqRaw) > 0 {
		expectedSeq = string(nextSeqRaw)
	}
	if expectedSeq != claim.BatchSeq {
		return fail("account withdrawal batch_seq is stale or out of order", nil)
	}
	if _, err := evm.ParseBatchSeq(expectedSeq); err != nil || batchSeq.String() != expectedSeq {
		return fail("DEX next batch sequence is invalid or exhausted", err)
	}

	canonical, firstSeq, err := buildCanonicalAccountTemplate(sv, chainName, "", claim.VaultId, claim.KeyEpoch, claim.WithdrawIds)
	if err != nil {
		return fail("failed to rebuild canonical mixed withdrawal template", err)
	}
	if !bytes.Equal(canonical.TemplateHash, claim.TemplateHash) || !bytes.Equal(canonical.TemplateData, claim.TemplateData) {
		return fail("mixed withdrawal template mismatch", nil)
	}
	expectedJobID := chain.ComputeWithdrawJobID(chainName, "", claim.VaultId, firstSeq, canonical.TemplateHash, claim.KeyEpoch)
	if jobID != expectedJobID {
		return fail("mixed withdrawal job_id mismatch", nil)
	}
	if err := validateAccountWithdrawCoordinator(&vault, jobID, claim.KeyEpoch, claim.Base.FromAddress); err != nil {
		return fail(err.Error(), err)
	}
	template, err := evm.FromJSON(canonical.TemplateData)
	if err != nil || template.BatchSeq != claim.BatchSeq {
		return fail("invalid canonical mixed withdrawal template", err)
	}

	consumedKey := keys.KeyFrostVaultHandoverConsumed(chainName, claim.VaultId, claim.KeyEpoch)
	_, consumed, err := sv.Get(consumedKey)
	if err != nil {
		return fail("failed to read handover marker", err)
	}
	requiresHandover := claim.KeyEpoch > 1 && !consumed
	if claim.IncludesHandover != requiresHandover {
		return fail("first batch in a new epoch must atomically include handover", nil)
	}
	if requiresHandover {
		proof, proofExists, readErr := sv.Get(keys.KeyFrostVaultHandoverProof(chainName, claim.VaultId, claim.KeyEpoch))
		if readErr != nil || !proofExists || len(proof) == 0 {
			return fail("new epoch handover proof is unavailable", readErr)
		}
		var migration pb.FrostVaultMigrationState
		markerData, markerExists, readErr := sv.Get(keys.KeyFrostVaultMigration(chainName, claim.VaultId, claim.KeyEpoch))
		if readErr != nil || !markerExists || proto.Unmarshal(markerData, &migration) != nil || migration.Status != "SIGNED" || migration.NewEpoch != claim.KeyEpoch {
			return fail("new epoch handover marker is invalid", readErr)
		}
	}

	jobState := &pb.FrostWithdrawJobState{
		JobId: jobID, Chain: chainName, Asset: "", VaultId: claim.VaultId, KeyEpoch: claim.KeyEpoch,
		WithdrawIds: append([]string(nil), claim.WithdrawIds...), FirstSeq: withdraws[0].LiveSeq, LastSeq: withdraws[len(withdraws)-1].LiveSeq,
		TemplateHash: append([]byte(nil), canonical.TemplateHash...), TemplateData: append([]byte(nil), canonical.TemplateData...),
		Status: frostWithdrawJobStatusClaimed, ClaimHeight: claim.Base.ExecutedHeight, BatchSeq: claim.BatchSeq,
		RewardTokens: append([]string(nil), template.RewardTokens...), IncludesHandover: claim.IncludesHandover,
	}
	jobData, err := proto.Marshal(jobState)
	if err != nil {
		return fail("failed to marshal mixed withdrawal job", err)
	}
	ops := make([]WriteOp, 0, len(withdraws)+3)
	ops = append(ops,
		WriteOp{Key: keys.KeyFrostWithdrawJob(jobID), Value: jobData, Category: "frost_withdraw_job"},
		WriteOp{Key: activeKey, Value: []byte(jobID), Category: "frost_withdraw_active_vault"},
		WriteOp{Key: keys.KeyFrostWithdrawLiveHead(chainName, claim.VaultId), Value: []byte(strconv.FormatUint(withdraws[0].LiveSeq, 10)), Category: "frost_withdraw_live"},
	)
	for _, withdraw := range withdraws {
		withdraw.Status = WithdrawStatusSigning
		withdraw.JobID = jobID
		data, marshalErr := marshalWithdrawRequest(withdraw)
		if marshalErr != nil {
			return fail("failed to marshal mixed withdrawal", marshalErr)
		}
		ops = append(ops, WriteOp{Key: keys.KeyFrostWithdraw(withdraw.WithdrawID), Value: data, Category: "frost_withdraw"})
	}
	applyFrostWithdrawTransactionOps(sv, ops)
	return ops, &Receipt{TxID: txID, Status: "SUCCEED", WriteCount: len(ops)}, nil
}

                                                                         
func readMixedClaimPrefix(sv StateView, chainName string, vaultID uint32, head, tail uint64, limit int) ([]*FrostWithdrawRequest, error) {
	out := make([]*FrostWithdrawRequest, 0, limit)
	for cursor := head; cursor <= tail && len(out) < limit; {
		count := 128
		if remaining := tail - cursor + 1; remaining < uint64(count) {
			count = int(remaining)
		}
		indexKeys := make([]string, count)
		for i := range indexKeys {
			indexKeys[i] = keys.KeyFrostWithdrawLiveIndex(chainName, vaultID, cursor+uint64(i))
		}
		indexes, err := frostStateGetMany(sv, indexKeys)
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
		values, err := frostStateGetMany(sv, stateKeys)
		if err != nil {
			return nil, err
		}
		for i, id := range ids {
			data := values[stateKeys[i]]
			withdraw, err := unmarshalWithdrawRequest(data)
			if err != nil || withdraw.WithdrawID != id || withdraw.LiveSeq != seqs[i] || withdraw.VaultID != vaultID || withdraw.Status != WithdrawStatusQueued || chain.NormalizeChain(withdraw.Chain) != chainName {
				return nil, fmt.Errorf("invalid mixed withdrawal at live seq %d", seqs[i])
			}
			out = append(out, withdraw)
		}
		next := cursor + uint64(count)
		if next <= cursor {
			break
		}
		cursor = next
	}
	return out, nil
}
