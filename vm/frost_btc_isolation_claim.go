package vm

import (
	"bytes"
	"fmt"
	"math"
	"strconv"
	"strings"

	"dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

                                                                               
                                                                             
                                                                        
func prepareBTCIsolationClaim(sv StateView, claim *pb.FrostWithdrawClaimTx, parent *FrostWithdrawRequest) (*pb.FrostWithdrawJobState, *validatedBTCWithdraw, []WriteOp, error) {
	if claim == nil || claim.Base == nil || claim.BtcProtocolVersion != 3 || len(claim.WithdrawIds) != 1 ||
		parent == nil || claim.WithdrawIds[0] != parent.WithdrawID || strings.TrimSpace(claim.Asset) != parent.Asset || claim.BatchSeq != "" || claim.IncludesHandover {
		return nil, nil, nil, fmt.Errorf("BTC v3 claim requires one parent")
	}
	l, err := readBTCIsolationLedger(parent)
	if err != nil {
		return nil, nil, nil, err
	}
	if claim.BtcPartSeq != parent.BtcNextPartSeq || parent.BtcNextPartSeq == math.MaxUint64 ||
		parent.BtcNextPartSeq-1 >= uint64(parent.BtcMaxParts) {
		return nil, nil, nil, fmt.Errorf("BTC child sequence/part budget mismatch")
	}
	gross, err := parseBTCIsolationAmount("child gross", claim.BtcGrossAmount)
	if err != nil || gross == 0 || gross > l.unallocated {
		return nil, nil, nil, fmt.Errorf("BTC child gross exceeds unallocated amount")
	}
	template, err := btc.FromJSON(claim.TemplateData)
	if err != nil || len(template.Inputs) != 1 || len(template.Outputs) < 1 || len(template.Outputs) > 2 || len(template.WithdrawIDs) != 1 {
		return nil, nil, nil, fmt.Errorf("BTC v3 child must have one input and one payment")
	}
	                                                                           
	                                                                           
	part := *parent
	part.Amount, part.Status = strconv.FormatUint(gross, 10), WithdrawStatusQueued
	validated, err := validateBTCWithdrawTemplate(sv, chain.ChainBTC, parent.Asset, claim.JobId, claim.VaultId, claim.KeyEpoch,
		claim.TemplateData, claim.TemplateHash, []*FrostWithdrawRequest{&part})
	if err != nil {
		return nil, nil, nil, err
	}
	if !bytes.Equal(claim.TemplateData, validated.canonicalData) {
		return nil, nil, nil, fmt.Errorf("BTC v3 template must use canonical encoding")
	}
	if len(validated.utxos) != 1 || len(validated.utxos[0].OriginId) != 32 {
		return nil, nil, nil, fmt.Errorf("BTC input origin is not initialized")
	}
	if err := validateBTCWithdrawCoordinator(validated, claim.JobId, claim.KeyEpoch, claim.Base.FromAddress); err != nil {
		return nil, nil, nil, err
	}
	outputTotal := uint64(0)
	for _, output := range validated.template.Outputs {
		outputTotal, err = addUint64(outputTotal, output.Amount)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	if outputTotal >= validated.totalInput {
		return nil, nil, nil, fmt.Errorf("invalid BTC child fee")
	}
	fee := validated.totalInput - outputTotal
	if fee >= gross || fee > l.maxFee-l.committedFee {
		return nil, nil, nil, fmt.Errorf("BTC child exceeds cumulative fee budget")
	}
	reservation, err := reserveBTCWithdrawInputs(sv, validated, chain.ChainBTC, parent.Asset, claim.JobId)
	if err != nil {
		return nil, nil, nil, err
	}
	job := &pb.FrostWithdrawJobState{
		JobId: claim.JobId, Chain: chain.ChainBTC, Asset: parent.Asset, VaultId: claim.VaultId, KeyEpoch: claim.KeyEpoch,
		WithdrawIds: []string{parent.WithdrawID}, FirstSeq: parent.Seq, LastSeq: parent.Seq,
		TemplateHash: validated.template.TemplateHash(), TemplateData: validated.canonicalData,
		Status: frostWithdrawJobStatusClaimed, ClaimHeight: claim.Base.ExecutedHeight,
		BtcProtocolVersion: 3, BtcPartSeq: claim.BtcPartSeq, BtcGrossAmount: strconv.FormatUint(gross, 10),
		BtcFeeAmount: strconv.FormatUint(fee, 10), BtcOriginId: append([]byte(nil), validated.utxos[0].OriginId...),
		BtcCoordinator: claim.Base.FromAddress,
	}
	l.unallocated -= gross
	l.claimed += gross
	l.committedFee += fee
	parent.BtcNextPartSeq++
	writeBTCIsolationLedger(parent, l)
	return job, validated, reservation, nil
}

func (h *FrostWithdrawClaimTxHandler) dryRunBTCIsolationClaim(claim *pb.FrostWithdrawClaimTx, sv StateView) (ops []WriteOp, receipt *Receipt, resultErr error) {
	fail := func(err error) ([]WriteOp, *Receipt, error) {
		return nil, &Receipt{TxID: claim.Base.TxId, Status: "FAILED", Error: err.Error()}, err
	}
	active, err := chain.BTCIsolationActive(sv.Get)
	if err != nil {
		return fail(err)
	}
	if !active {
		return fail(fmt.Errorf("BTC v3 isolation is not active"))
	}
	if existing, found, err := readBTCIsolationJob(sv, claim.JobId); err != nil {
		return fail(err)
	} else if found {
		if sameBTCIsolationClaim(existing, claim) && (existing.Status == frostWithdrawJobStatusClaimed || existing.Status == frostWithdrawJobStatusSigned) {
			if existing.BtcCoordinator == "" || existing.BtcCoordinator != claim.Base.FromAddress {
				return fail(fmt.Errorf("BTC withdraw submitter is not coordinator"))
			}
			return nil, &Receipt{TxID: claim.Base.TxId, Status: "SUCCEED"}, nil
		}
		return fail(fmt.Errorf("BTC child already exists with different intent"))
	}
	if len(claim.WithdrawIds) != 1 {
		return fail(fmt.Errorf("BTC v3 claim requires one parent"))
	}
	if draining, err := vaultDrainingForClaim(sv, chain.ChainBTC, claim.VaultId, claim.KeyEpoch); err != nil {
		return fail(err)
	} else if draining {
		return fail(fmt.Errorf("vault is draining; new old-key withdraw claims are paused"))
	}
	parent, err := readBTCIsolationParent(sv, claim.WithdrawIds[0])
	if err != nil {
		return fail(err)
	}
	job, validated, reservation, err := prepareBTCIsolationClaim(sv, claim, parent)
	if err != nil {
		return fail(err)
	}
	partKey := keys.KeyFrostBtcWithdrawPart(parent.WithdrawID, claim.BtcPartSeq)
	if _, exists, err := sv.Get(partKey); err != nil && !isStateViewNotFound(err) {
		return fail(err)
	} else if exists {
		return fail(fmt.Errorf("BTC child sequence already allocated"))
	}
	input := validated.utxos[0]
	seqRaw, found, err := sv.Get(keys.KeyFrostBtcUtxoSeqRef(input.VaultId, input.Txid, input.Vout))
	if err != nil {
		return fail(err)
	}
	seq, err := strconv.ParseUint(string(seqRaw), 10, 64)
	if !found || err != nil || seq == 0 {
		return fail(fmt.Errorf("BTC spendable sequence missing"))
	}
	amountKey := keys.KeyFrostBtcUtxoAmount(input.VaultId, validated.totalInput, input.Txid, input.Vout)
	indexed, exists, err := sv.Get(amountKey)
	if err != nil {
		return fail(err)
	}
	var indexUTXO pb.FrostUtxo
	if !exists || proto.Unmarshal(indexed, &indexUTXO) != nil || !proto.Equal(input, &indexUTXO) {
		return fail(fmt.Errorf("BTC input amount index mismatch"))
	}
	jobData, err := proto.Marshal(job)
	if err != nil {
		return fail(err)
	}
	snap := sv.Snapshot()
	defer func() {
		if resultErr != nil || receipt == nil || receipt.Status != "SUCCEED" {
			_ = sv.Revert(snap)
		}
	}()
	applyFrostWithdrawTransactionOps(sv, reservation)
	sv.Del(amountKey)
	sv.Del(keys.KeyFrostBtcUtxoFIFOIndex(input.VaultId, seq))
	sv.Set(keys.KeyFrostWithdrawJob(job.JobId), jobData)
	sv.Set(keys.KeyFrostWithdrawActiveJob(chain.ChainBTC, job.VaultId, job.KeyEpoch, job.JobId), []byte(job.JobId))
	sv.Set(keys.KeyFrostBtcWithdrawActiveChild(parent.Asset, job.JobId), []byte(job.JobId))
	sv.Set(partKey, []byte(job.JobId))
	if err := persistBTCIsolationParent(sv, parent); err != nil {
		return fail(err)
	}
	return nil, &Receipt{TxID: claim.Base.TxId, Status: "SUCCEED", BlockHeight: claim.Base.ExecutedHeight, WriteCount: len(reservation) + 7}, nil
}
