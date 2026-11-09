package vm

import (
	"dex/db"
	"dex/future"
	"dex/keys"
	"dex/pb"
	"dex/utils"
	"fmt"
	"math/big"
	"sort"

	"google.golang.org/protobuf/proto"
)

const DefaultSpotNoFillOrderTTLBlocks uint64 = future.DefaultNoFillOrderTTLBlocks

func spotOrderIsOpen(st *pb.OrderState) bool {
	return st != nil && st.Status == pb.OrderStateStatus_ORDER_OPEN
}

func spotOrderBigInt(raw string) *big.Int {
	if raw == "" {
		return new(big.Int)
	}
	v, ok := new(big.Int).SetString(raw, 10)
	if !ok {
		return new(big.Int)
	}
	return v
}

func spotOrderNeverMatched(st *pb.OrderState) bool {
	return st != nil && spotOrderBigInt(st.FilledBase).Sign() == 0
}

func spotOrderNoFillExpiryKey(st *pb.OrderState) (string, bool) {
	if st == nil || st.OrderId == "" || st.CreateHeight == 0 || DefaultSpotNoFillOrderTTLBlocks == 0 {
		return "", false
	}
	return keys.KeyOrderExpiry(st.CreateHeight+DefaultSpotNoFillOrderTTLBlocks, st.OrderId), true
}

func spotOrderPriceIndexKey(st *pb.OrderState) (string, bool) {
	if st == nil || st.OrderId == "" || st.BaseToken == "" || st.QuoteToken == "" || st.Price == "" {
		return "", false
	}
	priceKey67, err := db.PriceToKey128(st.Price)
	if err != nil {
		return "", false
	}
	return keys.KeyOrderPriceIndex(utils.GeneratePairKey(st.BaseToken, st.QuoteToken), st.Side, st.IsFilled, priceKey67, st.OrderId), true
}

func syncSpotOrderExpiryIndex(sv StateView, st *pb.OrderState, oldStates ...*pb.OrderState) {
	newKey := ""
	if spotOrderIsOpen(st) && spotOrderNeverMatched(st) {
		if key, ok := spotOrderNoFillExpiryKey(st); ok {
			newKey = key
			sv.Set(key, []byte(st.OrderId))
		}
	}
	for _, old := range oldStates {
		if key, ok := spotOrderNoFillExpiryKey(old); ok && key != newKey {
			sv.Del(key)
		}
	}
}

func writeSpotOrderSnapshot(sv StateView, st *pb.OrderState, oldStates ...*pb.OrderState) error {
	if st == nil || st.OrderId == "" {
		return nil
	}
	data, err := proto.Marshal(st)
	if err != nil {
		return err
	}
	newIndexKey := ""
	if spotOrderIsOpen(st) {
		sv.Set(keys.KeyOrderState(st.OrderId), data)
		if indexKey, ok := spotOrderPriceIndexKey(st); ok {
			newIndexKey = indexKey
			indexData, err := proto.Marshal(&pb.OrderPriceIndex{Ok: true})
			if err != nil {
				return err
			}
			sv.Set(indexKey, indexData)
		}
	} else {
		sv.Del(keys.KeyOrderState(st.OrderId))
	}
	for _, old := range oldStates {
		if spotOrderIsOpen(old) {
			if indexKey, ok := spotOrderPriceIndexKey(old); ok && indexKey != newIndexKey {
				sv.Del(indexKey)
			}
		}
	}
	syncSpotAccountOrderIndex(sv, st, data, oldStates...)
	syncSpotOrderExpiryIndex(sv, st, oldStates...)
	return nil
}

func processSpotBlockHooks(sv StateView, height uint64) {
	expireUnmatchedSpotOrders(sv, height)
}

func expireUnmatchedSpotOrders(sv StateView, height uint64) {
	if height == 0 || DefaultSpotNoFillOrderTTLBlocks == 0 {
		return
	}
	kvs, err := sv.Scan(keys.KeyOrderExpiryPrefix(height))
	if err != nil || len(kvs) == 0 {
		return
	}
	expiryKeys := make([]string, 0, len(kvs))
	for key := range kvs {
		expiryKeys = append(expiryKeys, key)
	}
	sort.Strings(expiryKeys)
	for _, expiryKey := range expiryKeys {
		orderID := string(kvs[expiryKey])
		if orderID == "" {
			sv.Del(expiryKey)
			continue
		}
		data, exists, err := sv.Get(keys.KeyOrderState(orderID))
		if err != nil || !exists || len(data) == 0 {
			sv.Del(expiryKey)
			continue
		}
		var state pb.OrderState
		if err := proto.Unmarshal(data, &state); err != nil || state.OrderId == "" {
			sv.Del(expiryKey)
			continue
		}
		dueHeight := state.CreateHeight + DefaultSpotNoFillOrderTTLBlocks
		if state.CreateHeight == 0 || dueHeight > height {
			if correctedKey, ok := spotOrderNoFillExpiryKey(&state); ok && correctedKey != expiryKey {
				sv.Set(correctedKey, []byte(state.OrderId))
			}
			sv.Del(expiryKey)
			continue
		}
		if state.Status != pb.OrderStateStatus_ORDER_OPEN || state.IsFilled || !spotOrderNeverMatched(&state) {
			sv.Del(expiryKey)
			continue
		}
		if err := cancelSpotOpenOrderState(sv, &state, height, "spot_order_expired_unfreeze"); err != nil {
			continue
		}
		sv.Del(expiryKey)
	}
}

func cancelSpotOpenOrderState(sv StateView, state *pb.OrderState, height uint64, reason string) error {
	if state == nil || state.OrderId == "" {
		return fmt.Errorf("missing order state")
	}
	if state.Status != pb.OrderStateStatus_ORDER_OPEN || state.IsFilled {
		return fmt.Errorf("order not open, status=%d filled=%v", state.Status, state.IsFilled)
	}
	owner := state.Owner
	if owner == "" {
		return fmt.Errorf("missing order owner")
	}
	totalAmount, err := parseBalanceStrict("order amount", state.Amount)
	if err != nil {
		return err
	}
	filledBase, err := parseBalanceStrict("order filled_base", state.FilledBase)
	if err != nil {
		return err
	}
	remainQty, err := SafeSub(totalAmount, filledBase)
	if err != nil {
		return err
	}
	if remainQty.Sign() <= 0 {
		return fmt.Errorf("no unfilled quantity")
	}
	ledger := NewLedger(sv)
	if state.Side == pb.OrderSide_SELL {
		if reason == "" {
			reason = "spot_order_cancel_unfreeze"
		}
		if err := ledger.Move(MoveRequest{
			Account: owner, Token: state.BaseToken, Amount: remainQty,
			From: BucketOrderFrozen, To: BucketAvailable,
			Reason: reason + "_sell", Height: height, Ref: state.OrderId,
		}); err != nil {
			return err
		}
	} else {
		limitPrice, err := parseBalanceStrict("order price", state.Price)
		if err != nil {
			return err
		}
		refundQuote, err := QuoteFromBasePrice(remainQty, limitPrice)
		if err != nil {
			return err
		}
		if reason == "" {
			reason = "spot_order_cancel_unfreeze"
		}
		if err := ledger.Move(MoveRequest{
			Account: owner, Token: state.QuoteToken, Amount: refundQuote,
			From: BucketOrderFrozen, To: BucketAvailable,
			Reason: reason + "_buy", Height: height, Ref: state.OrderId,
		}); err != nil {
			return err
		}
	}

	cancelled := proto.Clone(state).(*pb.OrderState)
	cancelled.Status = pb.OrderStateStatus_ORDER_CANCELLED
	if err := writeSpotOrderSnapshot(sv, cancelled, state); err != nil {
		return err
	}
	return nil
}
