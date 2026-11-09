package withdrawresult

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/http"
	"strconv"

	"dex/frost/artifact"
	"dex/frost/chain"
	"dex/keys"
	"dex/pb"
)

func fillBTCParentSummary(result *Result, parent *pb.FrostWithdrawState) error {
	total := new(big.Int)
	for _, raw := range []string{parent.BtcUnallocatedAmount, parent.BtcClaimedAmount, parent.BtcSignedAmount, parent.BtcRefundedAmount} {
		n, ok := new(big.Int).SetString(raw, 10)
		if !ok || n.Sign() < 0 || n.String() != raw || n.BitLen() > 64 {
			return fail(http.StatusConflict, "invalid BTC parent amount bucket")
		}
		total.Add(total, n)
	}
	g, err := chain.ParseWithdrawAmount(result.GrossAmount)
	if err != nil || g.Cmp(total) != 0 {
		return fail(http.StatusConflict, "BTC parent amount conservation mismatch")
	}
	maxFee, err := strconv.ParseUint(parent.BtcMaxTotalFee, 10, 64)
	if err != nil {
		return fail(http.StatusConflict, "invalid BTC parent fee budget")
	}
	fee, err := strconv.ParseUint(parent.BtcCommittedFee, 10, 64)
	if err != nil || fee > maxFee || parent.BtcNextPartSeq == 0 || parent.BtcNextPartSeq > uint64(parent.BtcMaxParts)+1 {
		return fail(http.StatusConflict, "invalid BTC parent allocation counters")
	}
	result.BTCProtocolVersion = 3
	result.UnallocatedAmount = parent.BtcUnallocatedAmount
	result.ClaimedAmount = parent.BtcClaimedAmount
	result.SignedAmount = parent.BtcSignedAmount
	result.RefundedAmount = parent.BtcRefundedAmount
	result.MaxTotalFee = parent.BtcMaxTotalFee
	result.CommittedFee = parent.BtcCommittedFee
	result.MaxParts = parent.BtcMaxParts
	result.NextPartSeq = strconv.FormatUint(parent.BtcNextPartSeq, 10)
	return nil
}

                                                                              
                                                                            
                                                                
func ResolveBTCPart(ctx context.Context, get Get, jobID string) (*Result, error) {
	if !ValidID(jobID) {
		return nil, fail(http.StatusBadRequest, "invalid job_id")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var job pb.FrostWithdrawJobState
	if err := readProto(get, &job, keys.KeyFrostPermanentWithdrawJob(jobID), keys.KeyFrostWithdrawJob(jobID), keys.KeyArchiveFrostWithdrawJob(jobID)); err != nil {
		return nil, err
	}
	if job.JobId != jobID || job.BtcProtocolVersion != 3 || job.Chain != chain.ChainBTC || len(job.WithdrawIds) != 1 || job.BtcPartSeq == 0 || len(job.BtcOriginId) != 32 {
		return nil, fail(http.StatusConflict, "invalid BTC isolated child scope")
	}
	var parent pb.FrostWithdrawState
	parentID := job.WithdrawIds[0]
	if err := readProto(get, &parent, keys.KeyFrostPermanentWithdraw(parentID), keys.KeyFrostWithdraw(parentID), keys.KeyArchiveFrostWithdraw(parentID)); err != nil {
		return nil, err
	}
	if parent.BtcProtocolVersion != 3 || parent.WithdrawId != parentID || parent.Chain != chain.ChainBTC || parent.Asset != job.Asset {
		return nil, fail(http.StatusConflict, "BTC isolated child parent mismatch")
	}
	part, found, err := get(keys.KeyFrostBtcWithdrawPart(parentID, job.BtcPartSeq))
	if err != nil {
		return nil, err
	}
	if !found || string(part) != jobID {
		return nil, fail(http.StatusConflict, "BTC isolated child membership unavailable")
	}
	var cfg pb.FrostVaultConfig
	if err := readProto(get, &cfg, keys.KeyFrostVaultConfig(chain.ChainBTC, 0)); err != nil {
		return nil, err
	}
	gross, e := strconv.ParseUint(job.BtcGrossAmount, 10, 64)
	fee, e2 := strconv.ParseUint(job.BtcFeeAmount, 10, 64)
	if e != nil || e2 != nil || gross == 0 || fee >= gross {
		return nil, fail(http.StatusConflict, "invalid BTC child amounts")
	}
	expected := chain.BTCWithdrawPartJobID(chain.BTCWithdrawPartIntent{Network: cfg.DepositDexChainId, BTCNetwork: cfg.BtcNetwork, ParentID: parentID, PartSeq: job.BtcPartSeq, VaultID: job.VaultId, KeyEpoch: job.KeyEpoch, Gross: gross, Fee: fee, OriginID: job.BtcOriginId, TemplateHash: job.TemplateHash})
	if expected != jobID {
		return nil, fail(http.StatusConflict, "BTC child ID differs from immutable intent")
	}
	result := &Result{Version: 1, BTCProtocolVersion: 3, Status: job.Status, WithdrawID: parentID, RequestTxID: parent.TxId, Requester: parent.Requester, JobID: jobID, PartSeq: strconv.FormatUint(job.BtcPartSeq, 10), OriginID: hex.EncodeToString(job.BtcOriginId), SignedTxID: job.SignedTxId, Chain: "BTC", Asset: parent.Asset, Amount: job.BtcGrossAmount, GrossAmount: job.BtcGrossAmount, BroadcastFee: "0", NetAmount: strconv.FormatUint(gross-fee, 10), RequestedAmount: job.BtcGrossAmount, NetworkFee: job.BtcFeeAmount, Receiver: parent.To, VaultID: job.VaultId, Epoch: strconv.FormatUint(job.KeyEpoch, 10), BTCNetwork: cfg.BtcNetwork, IsNative: true}
	if job.Status == "CLAIMED" {
		return result, nil
	}
	if job.Status != "SIGNED" {
		return nil, fail(http.StatusConflict, "BTC child signing state unavailable")
	}
	id, found, err := get(keys.KeyFrostPermanentWithdrawArtifact(jobID))
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fail(http.StatusGone, "BTC child artifact unavailable")
	}
	record, err := readArtifact(get, string(id))
	if err != nil {
		return nil, err
	}
	meta := record.Metadata
	if meta.Kind != artifact.KindWithdraw || meta.Chain != "BTC" || meta.SourceID != jobID || meta.VaultID != job.VaultId || meta.Epoch != job.KeyEpoch {
		return nil, fail(http.StatusConflict, "BTC child artifact scope mismatch")
	}
	result.ArtifactID = meta.ArtifactID
	if err := fillBTC(ctx, get, result, &cfg, record, jobID); err != nil {
		return nil, fmt.Errorf("BTC child result: %w", err)
	}
	return result, nil
}
