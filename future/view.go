package future

import "math/big"

type PositionView struct {
	*Position
	PendingFunding  *big.Int
	DisplayFunding  *big.Int
	EffectiveMargin *big.Int
	UnrealizedPnL   *big.Int
	Equity          *big.Int
	MarkPrice       *big.Int
	CurrentLiqPrice *big.Int
	Health          *big.Rat
}

func (e *Engine) ViewPosition(id string) (*PositionView, error) {
	pos, err := e.positions.GetPosition(id)
	if err != nil || pos == nil {
		return nil, err
	}
	return e.viewFromPosition(pos), nil
}

func (e *Engine) ViewPositionsByOwner(owner string) ([]*PositionView, error) {
	positions, err := e.positions.GetPositionsByOwner(owner)
	if err != nil {
		return nil, err
	}
	views := make([]*PositionView, 0, len(positions))
	for _, p := range positions {
		views = append(views, e.viewFromPosition(p))
	}
	return views, nil
}

func (e *Engine) ViewOpenPositions(pair string) ([]*PositionView, error) {
	positions, err := e.positions.GetAllOpenPositions(pair)
	if err != nil {
		return nil, err
	}
	views := make([]*PositionView, 0, len(positions))
	for _, p := range positions {
		views = append(views, e.viewFromPosition(p))
	}
	return views, nil
}

func BuildView(pos *Position, markPrice, cumLong, cumShort, prevCumLong, prevCumShort *big.Int, cfg *MarketConfig) *PositionView {
	return BuildViewWithSettleMark(pos, markPrice, markPrice, cumLong, cumShort, prevCumLong, prevCumShort, cfg)
}

                                                                                        
func BuildViewWithSettleMark(pos *Position, markPrice, settleMarkPrice, cumLong, cumShort, prevCumLong, prevCumShort *big.Int, cfg *MarketConfig) *PositionView {
	if pos == nil {
		return nil
	}
	view := &PositionView{
		Position:        pos,
		PendingFunding:  new(big.Int),
		DisplayFunding:  new(big.Int),
		EffectiveMargin: new(big.Int).Set(pos.Margin),
		UnrealizedPnL:   new(big.Int),
		Equity:          new(big.Int).Set(pos.Margin),
		Health:          new(big.Rat),
	}
	if pos.Status != StatusOpen {
		return view
	}

	fundingMark := settleMarkPrice
	if fundingMark == nil || fundingMark.Sign() <= 0 {
		fundingMark = markPrice
	}
	if fundingMark == nil || fundingMark.Sign() <= 0 {
		return view
	}

	currentCum, prevCum := cumLong, prevCumLong
	if pos.Side == Short {
		currentCum, prevCum = cumShort, prevCumShort
	}
	if currentCum == nil {
		currentCum = new(big.Int)
	}
	lastCum := pos.LastCumulativeFunding
	if lastCum == nil {
		lastCum = new(big.Int)
	}
	view.PendingFunding = CalcPendingFunding(pos.Side, pos.Quantity, fundingMark, lastCum, currentCum)
	view.DisplayFunding = CalcDisplayPendingFunding(pos.Side, pos.Quantity, fundingMark, pos.FirstCumulativeFunding, prevCum, currentCum)

	eff := new(big.Int).Add(pos.Margin, view.PendingFunding) // safe-math-ok: 只读展示叠加有符号待结资金费，负权益随后夹零。
	if eff.Sign() < 0 {
		eff = new(big.Int)
	}
	view.EffectiveMargin = eff

	if markPrice == nil || markPrice.Sign() <= 0 {
		return view
	}
	view.MarkPrice = new(big.Int).Set(markPrice)
	view.UnrealizedPnL, _ = previewIncrementalClosePnL(pos, pos.Quantity, markPrice)
	view.Equity = CalcEquity(eff, view.UnrealizedPnL)

	if cfg != nil {
		notional := CalcNotional(pos.Quantity, markPrice)
		mmRate := cfg.MaintenanceMarginRateOrDefault()
		view.CurrentLiqPrice = positionConservativeLiquidationPrice(pos, eff, mmRate)
		mm := CalcMaintenanceMargin(notional, mmRate)
		if mm.Sign() > 0 {
			view.Health = new(big.Rat).SetFrac(view.Equity, mm)
		}
	}
	return view
}

func (e *Engine) viewFromPosition(pos *Position) *PositionView {
	if pos == nil {
		return nil
	}
	var markPrice, settleMarkPrice *big.Int
	if e.markPrice != nil {
		if mp, err := e.markPrice.GetMarkPrice(pos.Pair); err == nil {
			markPrice = mp
		}
		if mp, err := e.markPrice.GetSettleMarkPrice(pos.Pair); err == nil {
			settleMarkPrice = mp
		}
	}
	var cumLong, cumShort, prevLong, prevShort *big.Int
	if e.funding != nil {
		cumLong, cumShort, _ = e.funding.GetCumulativeFunding(pos.Pair)
		prevLong, prevShort, _ = e.funding.GetPrevCumulativeFunding(pos.Pair)
	}
	cfg, _ := e.GetMarketConfig(pos.Pair)
	return BuildViewWithSettleMark(pos, markPrice, settleMarkPrice, cumLong, cumShort, prevLong, prevShort, cfg)
}
