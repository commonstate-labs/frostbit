package vm

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"dex/frost/chain"
	"dex/keys"
	"dex/pb"
	"dex/protocol"

	"github.com/btcsuite/btcd/btcutil"
	"google.golang.org/protobuf/proto"
)

const WithdrawStatusPartiallyCancelled WithdrawRequestStatus = "PARTIALLY_CANCELLED"

                                                                         
                                                                               
type btcIsolationLedger struct {
	gross, unallocated, claimed, signed, refunded uint64
	maxFee, committedFee                          uint64
}

func parseBTCIsolationAmount(label, raw string) (uint64, error) {
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || strconv.FormatUint(n, 10) != raw || n > btcutil.MaxSatoshi {
		return 0, fmt.Errorf("invalid BTC %s", label)
	}
	return n, nil
}

func readBTCIsolationLedger(parent *FrostWithdrawRequest) (*btcIsolationLedger, error) {
	if parent == nil || parent.BtcProtocolVersion != 3 || chain.NormalizeChain(parent.Chain) != chain.ChainBTC {
		return nil, fmt.Errorf("invalid BTC v3 parent")
	}
	l := new(btcIsolationLedger)
	fields := []struct {
		label, raw string
		dst        *uint64
	}{
		{"gross", parent.Amount, &l.gross}, {"unallocated amount", parent.BtcUnallocatedAmount, &l.unallocated},
		{"claimed amount", parent.BtcClaimedAmount, &l.claimed}, {"signed amount", parent.BtcSignedAmount, &l.signed},
		{"refunded amount", parent.BtcRefundedAmount, &l.refunded}, {"maximum total fee", parent.BtcMaxTotalFee, &l.maxFee},
		{"committed fee", parent.BtcCommittedFee, &l.committedFee},
	}
	for _, field := range fields {
		value, err := parseBTCIsolationAmount(field.label, field.raw)
		if err != nil {
			return nil, err
		}
		*field.dst = value
	}
	if l.gross == 0 || l.maxFee == 0 || l.maxFee >= l.gross || l.committedFee > l.maxFee ||
		parent.BtcMaxParts == 0 || parent.BtcMaxParts > protocol.BTCIsolationMaxParts || parent.BtcNextPartSeq == 0 || parent.BtcNextPartSeq-1 > uint64(parent.BtcMaxParts) {
		return nil, fmt.Errorf("invalid BTC parent budgets")
	}
	sum := uint64(0)
	for _, amount := range []uint64{l.unallocated, l.claimed, l.signed, l.refunded} {
		if math.MaxUint64-sum < amount {
			return nil, fmt.Errorf("BTC parent ledger overflow")
		}
		sum += amount
	}
	if sum != l.gross {
		return nil, fmt.Errorf("BTC parent conservation mismatch")
	}
	return l, nil
}

func writeBTCIsolationLedger(parent *FrostWithdrawRequest, l *btcIsolationLedger) bool {
	parent.BtcUnallocatedAmount = strconv.FormatUint(l.unallocated, 10)
	parent.BtcClaimedAmount = strconv.FormatUint(l.claimed, 10)
	parent.BtcSignedAmount = strconv.FormatUint(l.signed, 10)
	parent.BtcRefundedAmount = strconv.FormatUint(l.refunded, 10)
	parent.BtcCommittedFee = strconv.FormatUint(l.committedFee, 10)
	parent.BroadcastFee = parent.BtcCommittedFee
	parent.JobID, parent.SignedTxID = "", ""
	switch {
	case l.unallocated > 0:
		parent.Status = WithdrawStatusQueued
	case l.claimed > 0:
		parent.Status = WithdrawStatusSigning
	case l.refunded == 0:
		parent.Status = WithdrawStatusSigned
	case l.signed == 0:
		parent.Status = WithdrawStatusCancelled
	default:
		parent.Status = WithdrawStatusPartiallyCancelled
	}
	if l.unallocated == 0 && l.claimed == 0 && l.signed >= l.committedFee {
		parent.NetAmount = strconv.FormatUint(l.signed-l.committedFee, 10)
	}
	return l.unallocated == 0 && l.claimed == 0
}

func readBTCIsolationParent(sv StateView, id string) (*FrostWithdrawRequest, error) {
	for _, key := range []string{keys.KeyFrostWithdraw(id), keys.KeyFrostPermanentWithdraw(id)} {
		data, exists, err := sv.Get(key)
		if err != nil && !isStateViewNotFound(err) {
			return nil, err
		}
		if !exists {
			continue
		}
		if len(data) == 0 {
			return nil, fmt.Errorf("empty BTC parent state")
		}
		parent, err := unmarshalWithdrawRequest(data)
		if err != nil {
			return nil, err
		}
		if parent.WithdrawID != id {
			return nil, fmt.Errorf("BTC parent identity mismatch")
		}
		if _, err := readBTCIsolationLedger(parent); err != nil {
			return nil, err
		}
		return parent, nil
	}
	return nil, fmt.Errorf("BTC parent not found")
}

func readBTCIsolationJob(sv StateView, id string) (*pb.FrostWithdrawJobState, bool, error) {
	for _, key := range []string{keys.KeyFrostWithdrawJob(id), keys.KeyFrostPermanentWithdrawJob(id)} {
		data, exists, err := sv.Get(key)
		if err != nil && !isStateViewNotFound(err) {
			return nil, false, err
		}
		if !exists {
			continue
		}
		job := new(pb.FrostWithdrawJobState)
		if len(data) == 0 || proto.Unmarshal(data, job) != nil || job.JobId != id {
			return nil, true, fmt.Errorf("invalid BTC child state")
		}
		return job, true, nil
	}
	return nil, false, nil
}

func sameBTCIsolationClaim(job *pb.FrostWithdrawJobState, claim *pb.FrostWithdrawClaimTx) bool {
	return job != nil && claim != nil && sameWithdrawJobClaim(job, claim, chain.ChainBTC, strings.TrimSpace(claim.Asset)) &&
		job.BtcProtocolVersion == claim.BtcProtocolVersion && job.BtcPartSeq == claim.BtcPartSeq &&
		job.BtcGrossAmount == claim.BtcGrossAmount && string(job.TemplateData) == string(claim.TemplateData)
}

func persistBTCIsolationParent(sv StateView, parent *FrostWithdrawRequest) error {
	l, err := readBTCIsolationLedger(parent)
	if err != nil {
		return err
	}
	terminal := writeBTCIsolationLedger(parent, l)
	data, err := marshalWithdrawRequest(parent)
	if err != nil {
		return err
	}
	readyKey := keys.KeyFrostBtcWithdrawReady(parent.Asset, parent.Seq, parent.WithdrawID)
	if l.unallocated > 0 {
		sv.Set(readyKey, []byte(parent.WithdrawID))
	} else {
		sv.Del(readyKey)
	}
	if !terminal {
		sv.Set(keys.KeyFrostWithdraw(parent.WithdrawID), data)
		return nil
	}
	sv.Set(keys.KeyFrostPermanentWithdraw(parent.WithdrawID), data)
	sv.Set(keys.KeyArchiveFrostWithdraw(parent.WithdrawID), data)
	sv.Set(keys.KeyArchiveFrostWithdrawDone(parent.Chain, parent.Asset, parent.Seq, parent.WithdrawID), []byte(parent.WithdrawID))
	applyFrostWithdrawTransactionOps(sv, finalizeFrostWithdrawQueryIndexes(parent))
	sv.Del(keys.KeyFrostWithdraw(parent.WithdrawID))
	return nil
}
