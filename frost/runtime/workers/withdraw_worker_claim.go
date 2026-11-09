package workers

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"dex/frost/chain"
	"dex/frost/runtime/planning"
	"dex/keys"
	"dex/pb"
	"dex/txverify"

	"google.golang.org/protobuf/proto"
)

const withdrawClaimWaitTimeout = 2 * time.Minute

func (w *WithdrawWorker) submitWithdrawClaim(ctx context.Context, job *planning.Job) error {
	if w == nil || job == nil {
		return nil
	}
	if w.txSubmitter == nil {
		return fmt.Errorf("missing tx submitter")
	}
	claimTx := &pb.FrostWithdrawClaimTx{
		Base:               w.newBaseMessage(),
		JobId:              job.JobID,
		WithdrawIds:        append([]string(nil), job.WithdrawIDs...),
		VaultId:            job.VaultID,
		KeyEpoch:           job.KeyEpoch,
		TemplateHash:       append([]byte(nil), job.TemplateHash...),
		Chain:              job.Chain,
		Asset:              job.Asset,
		TemplateData:       append([]byte(nil), job.TemplateData...),
		BatchSeq:           job.BatchSeq,
		IncludesHandover:   job.IncludesHandover,
		BtcProtocolVersion: job.BTCProtocolVersion,
		BtcPartSeq:         job.BTCPartSeq,
		BtcGrossAmount:     job.BTCGrossAmount,
	}
	anyTx := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawClaimTx{FrostWithdrawClaimTx: claimTx}}
	if _, err := txverify.SignWithSigner(anyTx, w.localIdentity); err != nil {
		return fmt.Errorf("sign withdraw claim tx: %w", err)
	}
	if err := w.txSubmitter.SubmitWithdrawClaimTx(ctx, claimTx); err != nil {
		return fmt.Errorf("submit withdraw claim tx: %w", err)
	}
	w.Logger.Info("[WithdrawWorker] submitted claim tx for job %s", job.JobID)
	return nil
}

func (w *WithdrawWorker) waitForWithdrawClaim(ctx context.Context, job *planning.Job) error {
	if w == nil || job == nil {
		return nil
	}
	if w.stateReader == nil {
		return fmt.Errorf("missing state reader")
	}
	waitCtx := context.Background()
	if ctx != nil {
		waitCtx = context.WithoutCancel(ctx)
	}
	deadline := time.NewTimer(withdrawClaimWaitTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		if ok, err := w.isWithdrawClaimApplied(job); err != nil {
			return err
		} else if ok {
			return nil
		}

		select {
		case <-waitCtx.Done():
			return waitCtx.Err()
		case <-deadline.C:
			return fmt.Errorf("timeout waiting withdraw claim for job %s", job.JobID)
		case <-ticker.C:
		}
	}
}

func (w *WithdrawWorker) isWithdrawClaimApplied(job *planning.Job) (bool, error) {
	data, exists, err := w.stateReader.Get(keys.KeyFrostWithdrawJob(job.JobID))
	if err != nil || !exists || len(data) == 0 {
		return false, err
	}
	var state pb.FrostWithdrawJobState
	if err := proto.Unmarshal(data, &state); err != nil {
		return false, fmt.Errorf("decode withdraw job state: %w", err)
	}
	if state.Status != "CLAIMED" && state.Status != "SIGNED" {
		return false, fmt.Errorf("withdraw job %s status=%s", job.JobID, state.Status)
	}
	if !withdrawJobStateMatchesJob(&state, job) {
		return false, fmt.Errorf("withdraw job state mismatch for job %s", job.JobID)
	}
	return true, nil
}

                                                                         
                                                                              
                                                                 
func (w *WithdrawWorker) validateAccountClaimReservation(job *planning.Job) error {
	if w == nil || w.stateReader == nil || job == nil {
		return fmt.Errorf("account withdraw claim reservation is unavailable")
	}
	jobData, exists, err := w.stateReader.Get(keys.KeyFrostWithdrawJob(job.JobID))
	if err != nil || !exists || len(jobData) == 0 {
		return fmt.Errorf("account withdraw job %s state is unavailable: %w", job.JobID, err)
	}
	var finalized pb.FrostWithdrawJobState
	if proto.Unmarshal(jobData, &finalized) != nil || !withdrawJobStateMatchesJob(&finalized, job) || finalized.Status != "CLAIMED" {
		return fmt.Errorf("account withdraw job %s state mismatch", job.JobID)
	}
	stateKeys := make([]string, len(job.WithdrawIDs))
	for i, id := range job.WithdrawIDs {
		stateKeys[i] = keys.KeyFrostWithdraw(id)
	}
	states, err := workerGetMany(w.stateReader, stateKeys)
	if err != nil {
		return err
	}
	totals := make(map[string]*big.Int)
	for i, withdrawID := range job.WithdrawIDs {
		var withdraw pb.FrostWithdrawState
		if data := states[stateKeys[i]]; len(data) == 0 || proto.Unmarshal(data, &withdraw) != nil {
			return fmt.Errorf("decode account withdraw %s reservation intent", withdrawID)
		}
		if withdraw.WithdrawId != withdrawID || withdraw.JobId != job.JobID || withdraw.Status != "SIGNING" || withdraw.VaultId != job.VaultID {
			return fmt.Errorf("account withdraw %s reservation scope mismatch", withdrawID)
		}
		amount, ok := new(big.Int).SetString(strings.TrimSpace(withdraw.GrossAmount), 10)
		if !ok || amount.Sign() <= 0 || amount.BitLen() > 256 {
			return fmt.Errorf("account withdraw %s amount is invalid", withdrawID)
		}
		if totals[withdraw.Asset] == nil {
			totals[withdraw.Asset] = new(big.Int)
		}
		totals[withdraw.Asset].Add(totals[withdraw.Asset], amount)
		if totals[withdraw.Asset].BitLen() > 256 {
			return fmt.Errorf("account withdraw token reservation overflows uint256")
		}
	}
	assets := make([]string, 0, len(totals))
	for asset := range totals {
		assets = append(assets, asset)
	}
	sort.Strings(assets)
	reservedKeys := make([]string, len(assets))
	for i, asset := range assets {
		reservedKeys[i] = keys.KeyFrostVaultReservedBalance(chain.NormalizeChain(job.Chain), asset, job.VaultID)
	}
	reserved, err := workerGetMany(w.stateReader, reservedKeys)
	if err != nil {
		return err
	}
	for i, asset := range assets {
		aggregate, ok := new(big.Int).SetString(strings.TrimSpace(string(reserved[reservedKeys[i]])), 10)
		if !ok || aggregate.Sign() <= 0 || aggregate.BitLen() > 256 || aggregate.Cmp(totals[asset]) < 0 {
			return fmt.Errorf("account withdraw job %s vault reservation is insufficient for %s", job.JobID, asset)
		}
	}
	return nil
}

func workerGetMany(reader interface {
	Get(string) ([]byte, bool, error)
}, stateKeys []string) (map[string][]byte, error) {
	if batch, ok := reader.(interface {
		GetMany([]string) (map[string][]byte, error)
	}); ok {
		return batch.GetMany(stateKeys)
	}
	result := make(map[string][]byte, len(stateKeys))
	for _, key := range stateKeys {
		value, found, err := reader.Get(key)
		if err != nil {
			return nil, err
		}
		if found {
			result[key] = value
		}
	}
	return result, nil
}

func withdrawJobStateMatchesJob(state *pb.FrostWithdrawJobState, job *planning.Job) bool {
	if state == nil || job == nil {
		return false
	}
	if state.JobId != job.JobID ||
		chain.NormalizeChain(state.Chain) != chain.NormalizeChain(job.Chain) ||
		!strings.EqualFold(state.Asset, job.Asset) ||
		state.VaultId != job.VaultID ||
		state.KeyEpoch != job.KeyEpoch ||
		state.BatchSeq != job.BatchSeq ||
		state.IncludesHandover != job.IncludesHandover ||
		state.BtcProtocolVersion != job.BTCProtocolVersion ||
		state.BtcPartSeq != job.BTCPartSeq ||
		state.BtcGrossAmount != job.BTCGrossAmount ||
		state.BtcFeeAmount != job.BTCFeeAmount ||
		!bytes.Equal(state.BtcOriginId, job.BTCOriginID) ||
		!bytes.Equal(state.TemplateHash, job.TemplateHash) ||
		len(state.WithdrawIds) != len(job.WithdrawIDs) {
		return false
	}
	for i := range state.WithdrawIds {
		if strings.TrimSpace(state.WithdrawIds[i]) != strings.TrimSpace(job.WithdrawIDs[i]) {
			return false
		}
	}
	return true
}
