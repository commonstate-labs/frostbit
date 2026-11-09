package vm

import (
	"bytes"
	"dex/frost/chain"
	"dex/keys"
	"dex/pb"
	statedb "dex/stateDB"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"google.golang.org/protobuf/proto"
)

const (
	frostWithdrawJobStatusClaimed = "CLAIMED"
	frostWithdrawJobStatusSigned  = "SIGNED"
)

type FrostWithdrawClaimTxHandler struct{}

func (h *FrostWithdrawClaimTxHandler) Kind() string {
	return "frost_withdraw_claim"
}

func (h *FrostWithdrawClaimTxHandler) DryRun(tx *pb.AnyTx, sv StateView) ([]WriteOp, *Receipt, error) {
	claimTx, ok := tx.GetContent().(*pb.AnyTx_FrostWithdrawClaimTx)
	if !ok {
		return nil, &Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: "not a frost withdraw claim transaction"}, errors.New("not a frost withdraw claim transaction")
	}
	claim := claimTx.FrostWithdrawClaimTx
	if claim == nil || claim.Base == nil {
		return nil, &Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: "invalid frost withdraw claim transaction"}, errors.New("invalid frost withdraw claim transaction")
	}

	txID := claim.Base.TxId
	jobID := strings.TrimSpace(claim.JobId)
	withdrawIDs := claim.WithdrawIds
	chainName := chain.NormalizeChain(claim.Chain)
	asset := strings.TrimSpace(claim.Asset)

	if jobID == "" {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "missing job_id"}, errors.New("missing job_id")
	}
	if len(withdrawIDs) == 0 {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "empty withdraw_ids"}, errors.New("empty withdraw_ids")
	}
	if chainName != chain.ChainBTC && len(withdrawIDs) > chain.MaxWithdrawalsPerBatch {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "account withdraw batch exceeds 64 items"}, errors.New("account withdraw batch exceeds 64 items")
	}
	if chainName == "" {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "missing chain"}, errors.New("missing chain")
	}
	if chainName != chain.ChainBTC {
		return h.dryRunMixedAccountClaim(claim, sv)
	}
	if asset == "" {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "missing asset"}, errors.New("missing asset")
	}
	if claim.BtcProtocolVersion != 0 && claim.BtcProtocolVersion != 3 {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "unsupported BTC claim protocol version"}, errors.New("unsupported BTC claim protocol version")
	}
	if len(claim.TemplateHash) == 0 {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "missing template_hash"}, errors.New("missing template_hash")
	}
	if claim.BtcProtocolVersion == 3 {
		return h.dryRunBTCIsolationClaim(claim, sv)
	}

	if existing, exists, err := readFrostWithdrawJobState(sv, jobID); err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: fmt.Sprintf("failed to read withdraw job state: %v", err)}, err
	} else if exists {
		if sameWithdrawJobClaim(existing, claim, chainName, asset) && (existing.Status == frostWithdrawJobStatusClaimed || existing.Status == frostWithdrawJobStatusSigned) {
			if err := validateExistingWithdrawClaimCoordinator(sv, existing, claim.Base.FromAddress); err != nil {
				return nil, &Receipt{TxID: txID, Status: "FAILED", Error: err.Error()}, err
			}
			return nil, &Receipt{TxID: txID, Status: "SUCCEED", WriteCount: 0}, nil
		}
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "withdraw job already exists with different claim"}, errors.New("withdraw job already exists with different claim")
	}
	                                                                        
	if active, err := chain.BTCIsolationActive(sv.Get); err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: err.Error()}, err
	} else if active {
		if old, found, err := readBTCIsolationJob(sv, jobID); err != nil {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: err.Error()}, err
		} else if found && old.Status == frostWithdrawJobStatusSigned && old.BtcProtocolVersion == 0 && claim.BtcProtocolVersion == 0 &&
			claim.BtcPartSeq == 0 && claim.BtcGrossAmount == "" && sameWithdrawJobClaim(old, claim, chainName, asset) && bytes.Equal(old.TemplateData, claim.TemplateData) {
			if err := validateBTCIsolationLegacyCoordinator(sv, old, claim.Base.FromAddress); err != nil {
				return nil, &Receipt{TxID: txID, Status: "FAILED", Error: err.Error()}, err
			}
			return nil, &Receipt{TxID: txID, Status: "SUCCEED"}, nil
		}
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "new legacy BTC claims are disabled; cancel and submit a v3 request"}, errors.New("new legacy BTC claims are disabled")
	}
	if len(withdrawIDs) > chain.MaxBTCWithdrawalsPerBatch {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "BTC withdraw batch exceeds 256 items"}, errors.New("BTC withdraw batch exceeds 256 items")
	}
	if draining, err := vaultDrainingForClaim(sv, chainName, claim.VaultId, claim.KeyEpoch); err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to inspect vault transition"}, err
	} else if draining {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "vault is draining; new old-key withdraw claims are paused"}, errors.New("vault is draining")
	}

	currentHead, err := readWithdrawQueueUint64(sv, keys.KeyFrostWithdrawFIFOHead(chainName, asset))
	if err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to read withdraw FIFO head"}, err
	}
	if currentHead == 0 {
		currentHead = 1
	}
	resolvedHead, cancelledCleanupOps, _, err := resolveCancelledWithdrawsFrom(sv, chainName, asset, currentHead, nil)
	if err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: err.Error()}, err
	}
	signedHandler := &FrostWithdrawSignedTxHandler{}
	if err := signedHandler.validateFIFOOrderAtHead(sv, chainName, asset, withdrawIDs, resolvedHead); err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: err.Error()}, err
	}
	withdraws := make([]*FrostWithdrawRequest, 0, len(withdrawIDs))
	withdrawKeys := make([]string, len(withdrawIDs))
	for i, wid := range withdrawIDs {
		withdrawKeys[i] = keys.KeyFrostWithdraw(wid)
	}
	withdrawValues, err := getManyFrostState(sv, withdrawKeys)
	if err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to batch-read withdrawals"}, err
	}
	var firstSeq, lastSeq uint64
	for i, wid := range withdrawIDs {
		withdrawData := withdrawValues[withdrawKeys[i]]
		if len(withdrawData) == 0 {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "withdraw not found: " + wid}, errors.New("withdraw not found: " + wid)
		}
		withdraw, err := unmarshalWithdrawRequest(withdrawData)
		if err != nil {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to parse withdraw"}, err
		}
		if chain.NormalizeChain(withdraw.Chain) != chainName || !strings.EqualFold(withdraw.Asset, asset) {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "inconsistent chain/asset"}, errors.New("inconsistent chain/asset")
		}
		if withdraw.Status != WithdrawStatusQueued {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "withdraw not in QUEUED status: " + wid}, errors.New("withdraw not in QUEUED status: " + wid)
		}
		if i == 0 || withdraw.Seq < firstSeq {
			firstSeq = withdraw.Seq
		}
		if withdraw.Seq > lastSeq {
			lastSeq = withdraw.Seq
		}
		withdraws = append(withdraws, withdraw)
	}

	templateData := append([]byte(nil), claim.TemplateData...)
	templateHash := append([]byte(nil), claim.TemplateHash...)
	var reservationOps []WriteOp
	var reservedAmount *big.Int
	if chainName == chain.ChainBTC {
		validated, err := validateBTCWithdrawTemplate(sv, chainName, asset, jobID, claim.VaultId, claim.KeyEpoch, templateData, templateHash, withdraws)
		if err != nil {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: err.Error()}, err
		}
		if err := validateBTCWithdrawCoordinator(validated, jobID, claim.KeyEpoch, claim.Base.FromAddress); err != nil {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: err.Error()}, err
		}
		reservationOps, err = reserveBTCWithdrawInputs(sv, validated, chainName, asset, jobID)
		if err != nil {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: err.Error()}, err
		}
		templateData = validated.canonicalData
		templateHash = validated.template.TemplateHash()
	} else {
		canonical, vault, err := validateAccountWithdrawClaimTemplate(sv, claim, chainName, asset)
		if err != nil {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: err.Error()}, err
		}
		if err := validateAccountWithdrawCoordinator(vault, jobID, claim.KeyEpoch, claim.Base.FromAddress); err != nil {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: err.Error()}, err
		}
		reservedAmount, err = sumWithdrawAmounts(withdraws)
		if err != nil {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: err.Error()}, err
		}
		reservationOps, err = reserveAccountWithdrawFunds(sv, chainName, asset, claim.VaultId, reservedAmount)
		if err != nil {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: err.Error()}, err
		}
		templateData = append([]byte(nil), canonical.TemplateData...)
		templateHash = append([]byte(nil), canonical.TemplateHash...)
	}

	jobState := &pb.FrostWithdrawJobState{
		JobId:        jobID,
		Chain:        chainName,
		Asset:        asset,
		VaultId:      claim.VaultId,
		KeyEpoch:     claim.KeyEpoch,
		WithdrawIds:  append([]string(nil), withdrawIDs...),
		FirstSeq:     firstSeq,
		LastSeq:      lastSeq,
		TemplateHash: templateHash,
		TemplateData: templateData,
		Status:       frostWithdrawJobStatusClaimed,
		ClaimHeight:  claim.Base.ExecutedHeight,
	}
	if reservedAmount != nil {
		jobState.ReservedAmount = reservedAmount.String()
	}
	jobData, err := proto.Marshal(jobState)
	if err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to marshal withdraw job state"}, err
	}

	ops := make([]WriteOp, 0, len(withdraws)+2+len(reservationOps)+len(cancelledCleanupOps))
	ops = append(ops, cancelledCleanupOps...)
	if resolvedHead != currentHead {
		ops = append(ops, WriteOp{
			Key:      keys.KeyFrostWithdrawFIFOHead(chainName, asset),
			Value:    []byte(fmt.Sprintf("%d", resolvedHead)),
			Category: "frost_withdraw_head",
		})
	}
	ops = append(ops, WriteOp{Key: keys.KeyFrostWithdrawJob(jobID), Value: jobData, Category: "frost_withdraw_job"})
	ops = append(ops, WriteOp{Key: keys.KeyFrostWithdrawActiveJob(chainName, claim.VaultId, claim.KeyEpoch, jobID), Value: []byte(jobID), Category: "frost_withdraw_active"})
	for _, withdraw := range withdraws {
		oldVaultID := withdraw.VaultID
		withdraw.Status = WithdrawStatusSigning
		withdraw.JobID = jobID
		withdraw.VaultID = claim.VaultId
		updatedData, err := marshalWithdrawRequest(withdraw)
		if err != nil {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to marshal withdraw"}, err
		}
		ops = append(ops, WriteOp{Key: keys.KeyFrostWithdraw(withdraw.WithdrawID), Value: updatedData, Category: "frost_withdraw"})
		oldQueryKey := keys.KeyFrostWithdrawQueryChainVaultState(withdraw.Chain, oldVaultID, false, withdraw.RequestHeight, withdraw.Seq, withdraw.WithdrawID)
		newQueryKey := keys.KeyFrostWithdrawQueryChainVaultState(withdraw.Chain, withdraw.VaultID, false, withdraw.RequestHeight, withdraw.Seq, withdraw.WithdrawID)
		ops = append(ops, deleteWriteOp(keys.KeyFrostWithdrawQueryChainVault(withdraw.Chain, oldVaultID, withdraw.RequestHeight, withdraw.Seq, withdraw.WithdrawID), "frost_withdraw_query_legacy_cleanup"))
		if oldQueryKey != newQueryKey {
			ops = append(ops,
				deleteWriteOp(oldQueryKey, "frost_withdraw_query_migrate"),
				WriteOp{Key: newQueryKey, Value: []byte(withdraw.WithdrawID), Category: "frost_withdraw_query_migrate"},
			)
		}
	}
	ops = append(ops, reservationOps...)
	applyFrostWithdrawTransactionOps(sv, ops)
	return ops, &Receipt{TxID: txID, Status: "SUCCEED", WriteCount: len(ops)}, nil
}

func (h *FrostWithdrawClaimTxHandler) Apply(tx *pb.AnyTx) error {
	return ErrNotImplemented
}

func vaultDrainingForClaim(sv StateView, chainName string, vaultID uint32, keyEpoch uint64) (bool, error) {
	data, exists, err := sv.Get(keys.KeyFrostVaultTransition(chain.NormalizeChain(chainName), vaultID, keyEpoch+1))
	if err != nil {
		if isStateViewNotFound(err) {
			return false, nil
		}
		return false, err
	}
	if !exists || len(data) == 0 {
		return false, nil
	}
	var transition pb.VaultTransitionState
	if err := unmarshalProtoCompat(data, &transition); err != nil {
		return false, err
	}
	return transition.Lifecycle == VaultLifecycleDraining, nil
}

func validateAccountWithdrawClaimTemplate(sv StateView, claim *pb.FrostWithdrawClaimTx, chainName, asset string) (*chain.TemplateResult, *pb.FrostVaultState, error) {
	switch chainName {
	case chain.ChainETH, chain.ChainBNB, chain.ChainBASE, chain.ChainARB, chain.ChainTRX:
		canonicalTemplate, firstSeq, err := buildCanonicalAccountTemplate(sv, chainName, asset, claim.VaultId, claim.KeyEpoch, claim.WithdrawIds)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to rebuild canonical template: %w", err)
		}
		if !bytes.Equal(canonicalTemplate.TemplateHash, claim.TemplateHash) {
			return nil, nil, errors.New("template_hash mismatch")
		}
		expectedJobID := chain.ComputeWithdrawJobID(chainName, asset, claim.VaultId, firstSeq, canonicalTemplate.TemplateHash, claim.KeyEpoch)
		if strings.TrimSpace(claim.JobId) != expectedJobID {
			return nil, nil, errors.New("account withdraw job_id mismatch")
		}

		vaultData, exists, err := sv.Get(keys.KeyFrostVaultState(chainName, claim.VaultId))
		if err != nil {
			return nil, nil, fmt.Errorf("read account withdraw vault state: %w", err)
		}
		if !exists || len(vaultData) == 0 {
			return nil, nil, errors.New("account withdraw vault state not found")
		}
		var vault pb.FrostVaultState
		if err := unmarshalProtoCompat(vaultData, &vault); err != nil {
			return nil, nil, fmt.Errorf("invalid account withdraw vault state: %w", err)
		}
		if chain.NormalizeChain(vault.Chain) != chainName || vault.VaultId != claim.VaultId || vault.KeyEpoch != claim.KeyEpoch ||
			vault.Status != "ACTIVE" || vault.SignAlgo != pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128 || len(vault.GroupPubkey) != 64 || len(vault.CommitteeMembers) == 0 {
			return nil, nil, errors.New("account withdraw vault context mismatch")
		}
		return canonicalTemplate, &vault, nil
	default:
		return nil, nil, fmt.Errorf("unsupported account withdraw claim chain: %s", chainName)
	}
}

func validateAccountWithdrawCoordinator(vault *pb.FrostVaultState, jobID string, keyEpoch uint64, submitter string) error {
	if vault == nil {
		return errors.New("missing account withdraw vault")
	}
	expected, err := chain.WithdrawCoordinator(jobID, keyEpoch, vault.CommitteeMembers)
	if err != nil || strings.TrimSpace(submitter) != expected {
		return errors.New("account withdraw submitter is not coordinator")
	}
	return nil
}

func validateExistingWithdrawClaimCoordinator(sv StateView, job *pb.FrostWithdrawJobState, submitter string) error {
	if job == nil {
		return errors.New("missing withdraw job state")
	}
	chainName := chain.NormalizeChain(job.Chain)
	vaultData, exists, err := sv.Get(keys.KeyFrostVaultState(chainName, job.VaultId))
	if err != nil {
		return fmt.Errorf("read withdraw claim vault state: %w", err)
	}
	if !exists || len(vaultData) == 0 {
		return errors.New("withdraw claim vault state not found")
	}
	var vault pb.FrostVaultState
	if err := unmarshalProtoCompat(vaultData, &vault); err != nil {
		return fmt.Errorf("invalid withdraw claim vault state: %w", err)
	}
	if chain.NormalizeChain(vault.Chain) != chainName || vault.VaultId != job.VaultId || vault.KeyEpoch != job.KeyEpoch || len(vault.CommitteeMembers) == 0 {
		return errors.New("withdraw claim vault context mismatch")
	}
	expected, err := chain.WithdrawCoordinator(job.JobId, job.KeyEpoch, vault.CommitteeMembers)
	if err != nil {
		return fmt.Errorf("resolve withdraw claim coordinator: %w", err)
	}
	if strings.TrimSpace(submitter) != expected {
		return errors.New("withdraw claim submitter is not coordinator")
	}
	return nil
}

func sumWithdrawAmounts(withdraws []*FrostWithdrawRequest) (*big.Int, error) {
	total := big.NewInt(0)
	for _, withdraw := range withdraws {
		if withdraw == nil {
			return nil, errors.New("missing withdraw while reserving account funds")
		}
		amount, err := ParseBalance(withdraw.Amount)
		if err != nil || amount.Sign() <= 0 {
			return nil, fmt.Errorf("invalid withdraw amount for reservation: %s", withdraw.WithdrawID)
		}
		total, err = SafeAdd(total, amount)
		if err != nil {
			return nil, fmt.Errorf("account withdraw reservation overflow: %w", err)
		}
	}
	if total.Sign() <= 0 {
		return nil, errors.New("account withdraw reservation must be positive")
	}
	return total, nil
}

func reserveAccountWithdrawFunds(sv StateView, chainName, asset string, vaultID uint32, amount *big.Int) ([]WriteOp, error) {
	if amount == nil || amount.Sign() <= 0 {
		return nil, errors.New("account withdraw reservation must be positive")
	}
	available, err := getVaultAvailableBalance(sv, chainName, asset, vaultID)
	if err != nil {
		return nil, fmt.Errorf("read account vault available balance: %w", err)
	}
	if available.Cmp(amount) < 0 {
		return nil, fmt.Errorf("insufficient account vault available balance: need %s, have %s", amount, available)
	}
	reserved, err := getVaultReservedBalance(sv, chainName, asset, vaultID)
	if err != nil {
		return nil, fmt.Errorf("read account vault reserved balance: %w", err)
	}
	nextReserved, err := SafeAdd(reserved, amount)
	if err != nil {
		return nil, fmt.Errorf("account vault reserved balance overflow: %w", err)
	}
	nextAvailable, err := SafeSub(available, amount)
	if err != nil {
		return nil, fmt.Errorf("account vault available balance underflow: %w", err)
	}
	return []WriteOp{
		{Key: keys.KeyFrostVaultAvailableBalance(chainName, asset, vaultID), Value: []byte(nextAvailable.String()), Category: "frost_vault_available"},
		{Key: keys.KeyFrostVaultReservedBalance(chainName, asset, vaultID), Value: []byte(nextReserved.String()), Category: "frost_vault_reserved"},
	}, nil
}

func validateAccountWithdrawReservation(sv StateView, job *pb.FrostWithdrawJobState, chainName, asset string, vaultID uint32, amount *big.Int) error {
	if job == nil || strings.TrimSpace(job.ReservedAmount) == "" {
		return errors.New("account withdraw job reservation missing")
	}
	jobReserved, err := ParseBalance(job.ReservedAmount)
	if err != nil || jobReserved.Sign() <= 0 || amount == nil || jobReserved.Cmp(amount) != 0 {
		return errors.New("account withdraw job reservation amount mismatch")
	}
	reserved, err := getVaultReservedBalance(sv, chainName, asset, vaultID)
	if err != nil {
		return fmt.Errorf("read account vault reserved balance: %w", err)
	}
	if reserved.Cmp(jobReserved) < 0 {
		return errors.New("account withdraw vault reservation missing")
	}
	return nil
}

func readFrostWithdrawJobState(sv StateView, jobID string) (*pb.FrostWithdrawJobState, bool, error) {
	data, exists, err := sv.Get(keys.KeyFrostWithdrawJob(jobID))
	if err != nil {
		if errors.Is(err, statedb.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if !exists || len(data) == 0 {
		return nil, exists, nil
	}
	state := &pb.FrostWithdrawJobState{}
	if err := unmarshalProtoCompat(data, state); err != nil {
		return nil, true, err
	}
	return state, true, nil
}

func sameWithdrawJobClaim(state *pb.FrostWithdrawJobState, claim *pb.FrostWithdrawClaimTx, chainName, asset string) bool {
	if state == nil || claim == nil {
		return false
	}
	if chain.NormalizeChain(chainName) == chain.ChainBTC && (state.BtcProtocolVersion != claim.BtcProtocolVersion || state.BtcPartSeq != claim.BtcPartSeq || state.BtcGrossAmount != claim.BtcGrossAmount) {
		return false
	}
	return state.JobId == claim.JobId &&
		chain.NormalizeChain(state.Chain) == chainName &&
		strings.EqualFold(state.Asset, asset) &&
		state.VaultId == claim.VaultId &&
		state.KeyEpoch == claim.KeyEpoch &&
		sameOrderedWithdrawIDs(state.WithdrawIds, claim.WithdrawIds) &&
		bytes.Equal(state.TemplateHash, claim.TemplateHash)
}

func validateWithdrawJobStateForSigned(state *pb.FrostWithdrawJobState, signed *pb.FrostWithdrawSignedTx, chainName, asset string, withdrawIDs []string) error {
	if state == nil {
		return errors.New("withdraw job claim not found")
	}
	if state.Status != frostWithdrawJobStatusClaimed {
		return fmt.Errorf("withdraw job not claimable: status=%s", state.Status)
	}
	if state.JobId != signed.JobId {
		return errors.New("job_id mismatch with claim")
	}
	if chain.NormalizeChain(state.Chain) != chainName {
		return errors.New("chain mismatch with claim")
	}
	if !strings.EqualFold(state.Asset, asset) {
		return errors.New("asset mismatch with claim")
	}
	if state.VaultId != signed.VaultId {
		return errors.New("vault_id mismatch with claim")
	}
	if state.KeyEpoch != signed.KeyEpoch {
		return errors.New("key_epoch mismatch with claim")
	}
	if !sameOrderedWithdrawIDs(state.WithdrawIds, withdrawIDs) {
		return errors.New("withdraw_ids mismatch with claim")
	}
	if !bytes.Equal(state.TemplateHash, signed.TemplateHash) {
		return errors.New("template_hash mismatch with claim")
	}
	if len(state.TemplateData) > 0 && len(signed.TemplateData) > 0 && !bytes.Equal(state.TemplateData, signed.TemplateData) {
		return errors.New("template_data mismatch with claim")
	}
	return nil
}
