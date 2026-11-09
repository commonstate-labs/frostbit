package vm

import (
	"fmt"
	"math/big"
	"sort"

	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

const runtimeFeeEscrowKindWitnessRequest = "witness_request"

func shouldEscrowRuntimeFee(tx *pb.AnyTx, rc *Receipt) bool {
	if tx == nil || rc == nil || rc.Status != "SUCCEED" {
		return false
	}
	_, ok := tx.GetContent().(*pb.AnyTx_WitnessRequestTx)
	return ok
}

func openRuntimeFeeEscrow(sv StateView, sourceTxID, payer, amount, kind string) error {
	if sv == nil || sourceTxID == "" || payer == "" || kind == "" {
		return fmt.Errorf("runtime fee escrow identity is incomplete")
	}
	parsed, err := parseBalanceStrict("runtime fee escrow amount", amount)
	if err != nil || parsed.Sign() <= 0 {
		return fmt.Errorf("invalid runtime fee escrow amount %q", amount)
	}
	key := keys.KeyRuntimeFeeEscrow(sourceTxID)
	if raw, exists, err := sv.Get(key); err != nil {
		return err
	} else if exists {
		var existing pb.RuntimeFeeEscrow
		if err := unmarshalProtoCompat(raw, &existing); err != nil {
			return fmt.Errorf("decode runtime fee escrow %s: %w", sourceTxID, err)
		}
		if existing.SourceTxId == sourceTxID && existing.Payer == payer && existing.Amount == parsed.String() &&
			existing.Kind == kind && existing.Status == pb.RuntimeFeeEscrowStatus_RUNTIME_FEE_ESCROW_PENDING {
			return nil
		}
		return fmt.Errorf("runtime fee escrow already exists for %s", sourceTxID)
	}
	escrow := &pb.RuntimeFeeEscrow{
		SourceTxId: sourceTxID,
		Payer:      payer,
		Amount:     parsed.String(),
		Kind:       kind,
		Status:     pb.RuntimeFeeEscrowStatus_RUNTIME_FEE_ESCROW_PENDING,
	}
	raw, err := proto.Marshal(escrow)
	if err != nil {
		return fmt.Errorf("encode runtime fee escrow %s: %w", sourceTxID, err)
	}
	sv.Set(key, raw)
	return nil
}

func settleRuntimeFeeEscrow(sv StateView, sourceTxID string, height uint64, refund bool) error {
	if sv == nil || sourceTxID == "" {
		return nil
	}
	key := keys.KeyRuntimeFeeEscrow(sourceTxID)
	raw, exists, err := sv.Get(key)
	if err != nil || !exists {
		return err
	}
	var escrow pb.RuntimeFeeEscrow
	if err := unmarshalProtoCompat(raw, &escrow); err != nil {
		return fmt.Errorf("decode runtime fee escrow %s: %w", sourceTxID, err)
	}
	if escrow.SourceTxId != sourceTxID || escrow.Payer == "" || escrow.Kind == "" {
		return fmt.Errorf("runtime fee escrow %s has invalid identity", sourceTxID)
	}
	amount, err := parseBalanceStrict("runtime fee escrow amount", escrow.Amount)
	if err != nil || amount.Sign() <= 0 {
		return fmt.Errorf("runtime fee escrow %s has invalid amount", sourceTxID)
	}
	want := pb.RuntimeFeeEscrowStatus_RUNTIME_FEE_ESCROW_CONSUMED
	if refund {
		want = pb.RuntimeFeeEscrowStatus_RUNTIME_FEE_ESCROW_REFUNDED
	}
	if escrow.Status == want {
		return nil
	}
	if escrow.Status != pb.RuntimeFeeEscrowStatus_RUNTIME_FEE_ESCROW_PENDING {
		return fmt.Errorf("runtime fee escrow %s is already settled as %s", sourceTxID, escrow.Status)
	}
	if height == 0 {
		return fmt.Errorf("runtime fee escrow %s cannot settle without a terminal height", sourceTxID)
	}

	if refund {
		if _, _, err := RefundFee(sv, escrow.Payer, amount.String()); err != nil {
			return fmt.Errorf("refund runtime fee escrow %s: %w", sourceTxID, err)
		}
		recordUserActivity(sv, escrow.Payer, &UserActivityRecord{
			Kind: ActKindFeeRefund, Height: height, Amount: amount.String(), Token: FeeToken, Ref: sourceTxID,
			Note: "delayed protocol fee refund",
		})
	} else {
		releaseKey := keys.KeyRuntimeFeeRelease(height, sourceTxID)
		if existing, ok, err := sv.Get(releaseKey); err != nil {
			return err
		} else if ok && string(existing) != amount.String() {
			return fmt.Errorf("runtime fee release conflicts for %s", sourceTxID)
		}
		sv.Set(releaseKey, []byte(amount.String()))
	}
	escrow.Status = want
	escrow.SettledHeight = height
	raw, err = proto.Marshal(&escrow)
	if err != nil {
		return fmt.Errorf("encode settled runtime fee escrow %s: %w", sourceTxID, err)
	}
	sv.Set(key, raw)
	return nil
}

                                                                         
                                                                        
                                                      
func collectReleasedRuntimeFees(sv StateView, height uint64) (*big.Int, error) {
	rows, err := sv.Scan(keys.KeyRuntimeFeeReleasePrefix(height))
	if err != nil {
		return nil, fmt.Errorf("scan released runtime fees: %w", err)
	}
	keysInOrder := make([]string, 0, len(rows))
	for key := range rows {
		keysInOrder = append(keysInOrder, key)
	}
	sort.Strings(keysInOrder)
	total := new(big.Int)
	for _, key := range keysInOrder {
		amount, err := parseBalanceStrict("released runtime fee", string(rows[key]))
		if err != nil || amount.Sign() <= 0 {
			return nil, fmt.Errorf("invalid released runtime fee at %s", key)
		}
		total, err = SafeAdd(total, amount)
		if err != nil {
			return nil, fmt.Errorf("released runtime fee overflow: %w", err)
		}
		sv.Del(key)
	}
	return total, nil
}
