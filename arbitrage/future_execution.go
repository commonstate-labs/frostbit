package arbitrage

import (
	"dex/future"
	"dex/pb"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

type futureLegActual struct {
	matchedQuantity *big.Int
	matchedNotional *big.Int
	receipt         ExecutionReceipt
}

type futureValidationGroup struct {
	name       string
	quoteToken string
	indices    []int
}

func plannedFuturePositionID(owner, pair string, side pb.OrderSide, height, ordinal uint64) string {
	futureSide, ok := futureSideFromStrategy(side)
	if !ok {
		return ""
	}
	base := fmt.Sprintf("%s_%s_%d_%d", owner, pair, futureSide, height)
	if ordinal > 1 {
		return fmt.Sprintf("%s_%d", base, ordinal)
	}
	return base
}

func validateFutureLegRange(legs []plannedLeg, startToken string) error {
	if len(legs) != 2 {
		return fmt.Errorf("future route must contain exactly two legs, got %d", len(legs))
	}
	open, close := legs[0], legs[1]
	if open.market != plannedMarketFuture || close.market != plannedMarketFuture {
		return fmt.Errorf("future route contains a non-future leg")
	}
	if open.futureClose || !close.futureClose {
		return fmt.Errorf("future route must be open followed by IOC close")
	}
	if open.futureGroup == "" || open.futureGroup != close.futureGroup {
		return fmt.Errorf("future route position group mismatch")
	}
	if open.futurePair == "" || open.futurePair != close.futurePair {
		return fmt.Errorf("future route pair mismatch")
	}
	if (open.leg.Side != pb.OrderSide_BUY && open.leg.Side != pb.OrderSide_SELL) ||
		(close.leg.Side != pb.OrderSide_BUY && close.leg.Side != pb.OrderSide_SELL) {
		return fmt.Errorf("future route has invalid side")
	}
	baseToken, quoteToken := open.leg.BaseToken, open.leg.QuoteToken
	if baseToken == "" || quoteToken == "" || open.futurePair != baseToken+"_"+quoteToken ||
		baseToken != close.leg.BaseToken ||
		quoteToken != open.leg.QuoteToken || quoteToken != close.leg.QuoteToken || quoteToken != startToken {
		return fmt.Errorf("future route token/pair mismatch")
	}
	if oppositeSpotSide(open.leg.Side) != close.leg.Side {
		return fmt.Errorf("future route close side is not opposite to open side")
	}
	if open.futureLeverage == 0 || open.futureLeverage != close.futureLeverage {
		return fmt.Errorf("future route leverage mismatch")
	}
	if open.baseAmount == nil || close.baseAmount == nil || open.baseAmount.Sign() <= 0 || open.baseAmount.Cmp(close.baseAmount) != 0 {
		return fmt.Errorf("future route must close exactly its opened quantity")
	}
	if open.price == nil || close.price == nil || open.price.Sign() <= 0 || close.price.Sign() <= 0 {
		return fmt.Errorf("future route has invalid limit price")
	}
	return nil
}

func (m *Machine) validateActualFutureEconomics(planned *plan, batch *CandidateBatch, actuals []futureLegActual) error {
	if planned == nil || batch == nil || len(actuals) != len(planned.legs) || len(batch.Txs) != len(planned.legs) {
		return fmt.Errorf("future arbitrage execution has invalid leg data")
	}
	groups := collectFutureValidationGroups(planned)
	for groupIndex, group := range groups {
		if err := m.validateActualFutureGroup(planned, batch, actuals, group); err != nil {
			return fmt.Errorf("future arbitrage group %d (%s): %w", groupIndex, group.name, err)
		}
	}
	return nil
}

func collectFutureValidationGroups(planned *plan) []futureValidationGroup {
	if planned == nil {
		return nil
	}
	if len(planned.groups) == 0 {
		if candidateMarket(planned) != plannedMarketFuture {
			return nil
		}
		indices := make([]int, len(planned.legs))
		for i := range indices {
			indices[i] = i
		}
		return []futureValidationGroup{{name: planned.routeName, quoteToken: planned.startToken, indices: indices}}
	}
	out := make([]futureValidationGroup, 0, len(planned.groups))
	for _, group := range planned.groups {
		if group.market != plannedMarketFuture {
			continue
		}
		out = append(out, futureValidationGroup{
			name: group.routeName, quoteToken: group.startToken, indices: append([]int(nil), group.legIndices...),
		})
	}
	return out
}

func (m *Machine) validateActualFutureGroup(planned *plan, batch *CandidateBatch, actuals []futureLegActual, group futureValidationGroup) error {
	if len(group.indices) != 2 {
		return fmt.Errorf("expected two future legs, got %d", len(group.indices))
	}
	openIndex, closeIndex := group.indices[0], group.indices[1]
	if openIndex < 0 || closeIndex < 0 || openIndex >= len(planned.legs) || closeIndex >= len(planned.legs) {
		return fmt.Errorf("invalid future leg indices %d/%d", openIndex, closeIndex)
	}
	openLeg, closeLeg := planned.legs[openIndex], planned.legs[closeIndex]
	if err := validateFutureLegRange([]plannedLeg{openLeg, closeLeg}, group.quoteToken); err != nil {
		return err
	}
	openTx, closeTx := batch.Txs[openIndex].GetFutureOrderTx(), batch.Txs[closeIndex].GetFutureOrderTx()
	if openTx == nil || closeTx == nil || openTx.IsClose || !closeTx.IsClose || closeTx.ClosePositionId == "" {
		return fmt.Errorf("prepared future open/close transaction shape changed")
	}
	openActual, closeActual := actuals[openIndex], actuals[closeIndex]
	if openActual.matchedQuantity == nil || closeActual.matchedQuantity == nil ||
		openActual.matchedQuantity.Cmp(openLeg.baseAmount) != 0 || closeActual.matchedQuantity.Cmp(closeLeg.baseAmount) != 0 {
		return fmt.Errorf("future route did not fully execute")
	}

	realizedPnL, err := validateFutureClosedEvent(closeActual.receipt, m.account, closeLeg.futurePair, closeTx.ClosePositionId, closeLeg.baseAmount)
	if err != nil {
		return err
	}
	tradingFees := new(big.Int)
	for _, receipt := range []ExecutionReceipt{openActual.receipt, closeActual.receipt} {
		fee, feeErr := actualFutureTakerFee(receipt, m.account, group.quoteToken)
		if feeErr != nil {
			return feeErr
		}
		tradingFees.Add(tradingFees, fee)
	}
	runtimeFees := new(big.Int)
	for _, receipt := range []ExecutionReceipt{openActual.receipt, closeActual.receipt} {
		if strings.TrimSpace(receipt.Fee) == "" {
			return fmt.Errorf("missing actual runtime fee for %s", receipt.TxID)
		}
		fee, feeErr := parseUint256("actual runtime fee", receipt.Fee)
		if feeErr != nil {
			return feeErr
		}
		runtimeFees.Add(runtimeFees, fee)
	}

	profit := new(big.Int).Sub(new(big.Int).Set(realizedPnL), tradingFees)
	limitPrice, err := parseUint256("future open limit price", openTx.Price)
	if err != nil || limitPrice.Sign() <= 0 {
		return fmt.Errorf("invalid future open limit price %q", openTx.Price)
	}
	quantity, err := parseUint256("future open quantity", openTx.Quantity)
	if err != nil || quantity.Sign() <= 0 {
		return fmt.Errorf("invalid future open quantity %q", openTx.Quantity)
	}
	denominator := future.CalcRequiredMargin(quantity, limitPrice, openTx.Leverage)
	denominator.Add(denominator, tradingFees)
	if group.quoteToken == feeToken {
		profit.Sub(profit, runtimeFees)
		denominator.Add(denominator, runtimeFees)
	}
	if profit.Sign() <= 0 || denominator.Sign() <= 0 {
		return fmt.Errorf("actual future route is not profitable: pnl=%s trading_fees=%s runtime_fees=%s", realizedPnL, tradingFees, runtimeFees)
	}
	roiBPS := new(big.Int).Mul(new(big.Int).Set(profit), bigTenK)
	roiBPS.Quo(roiBPS, denominator)
	if roiBPS.Cmp(new(big.Int).SetUint64(uint64(m.config.MinProfitBPS))) < 0 {
		return fmt.Errorf("actual future roi_bps=%s below minimum %d", roiBPS, m.config.MinProfitBPS)
	}
	return nil
}

func validateFutureClosedEvent(receipt ExecutionReceipt, owner, pair, positionID string, quantity *big.Int) (*big.Int, error) {
	var closed *ExecutionEvent
	for i := range receipt.Events {
		event := &receipt.Events[i]
		if event.Kind != "POSITION_CLOSED" && event.Kind != "POSITION_REDUCED" {
			continue
		}
		if event.Payload["owner"] != owner || event.Payload["close_position"] != positionID {
			continue
		}
		if closed != nil {
			return nil, fmt.Errorf("close transaction %s emitted multiple terminal position events", receipt.TxID)
		}
		closed = event
	}
	if closed == nil || closed.Kind != "POSITION_CLOSED" || closed.Payload["pair"] != pair {
		return nil, fmt.Errorf("close transaction %s did not close position %s", receipt.TxID, positionID)
	}
	fullyClosed, err := strconv.ParseBool(closed.Payload["fully_closed"])
	if err != nil || !fullyClosed || strings.TrimSpace(closed.Payload["unfilled_quantity"]) != "0" ||
		strings.TrimSpace(closed.Payload["remaining_quantity"]) != "0" {
		return nil, fmt.Errorf("close transaction %s left position or IOC quantity behind", receipt.TxID)
	}
	closedQuantity, err := parseUint256("actual closed quantity", closed.Payload["quantity"])
	if err != nil || closedQuantity.Cmp(quantity) != 0 {
		return nil, fmt.Errorf("close transaction %s closed quantity=%s, want %s", receipt.TxID, closed.Payload["quantity"], quantity)
	}
	if _, err := parseUint256("future PnL shortfall", closed.Payload["pnl_shortfall"]); err != nil {
		return nil, err
	}
	return parseSignedInt256("actual realized PnL", closed.Payload["realized_pnl"])
}

func actualFutureTakerFee(receipt ExecutionReceipt, owner, quoteToken string) (*big.Int, error) {
	total := new(big.Int)
	count := 0
	for _, event := range receipt.Events {
		if event.Kind != "FEE_CHARGED" || event.Payload["owner"] != owner || event.Payload["role"] != "taker" {
			continue
		}
		if event.Payload["token"] != quoteToken {
			return nil, fmt.Errorf("future transaction %s charged taker fee in %q, want %q", receipt.TxID, event.Payload["token"], quoteToken)
		}
		fee, err := parseUint256("actual future taker fee", event.Payload["fee"])
		if err != nil {
			return nil, err
		}
		total.Add(total, fee)
		count++
	}
	if count == 0 {
		                                                                    
		                                                                    
		                                                                     
		for _, match := range receipt.Matches {
			quantity, err := parseUint256("matched future quantity", match.Quantity)
			if err != nil {
				return nil, err
			}
			price, err := parseUint256("matched future price", match.Price)
			if err != nil {
				return nil, err
			}
			if futureTakerFee(quantity, price).Sign() > 0 {
				return nil, fmt.Errorf("future transaction %s has no actual taker fee event", receipt.TxID)
			}
		}
	}
	return total, nil
}

func parseSignedInt256(label, raw string) (*big.Int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 79 {
		return nil, fmt.Errorf("invalid %s %q", label, raw)
	}
	value, ok := new(big.Int).SetString(raw, 10)
	if !ok || value.BitLen() > 256 {
		return nil, fmt.Errorf("invalid %s %q", label, raw)
	}
	return value, nil
}
