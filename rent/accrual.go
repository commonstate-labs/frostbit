package rent

import (
	"dex/keys"
	"dex/pb"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strings"

	"google.golang.org/protobuf/proto"
)

const (
	RentStateVersion uint32 = 1

	RentModeCharging     = "charging"
	RentModeNeedsFunding = "needs_funding"
	RentModeExemptDebt   = "exempt_debt"
	RentModeExempt       = "exempt"
	RentModeSweeping     = "sweeping"

	RentWorkSettle            = "settle"
	RentWorkFunding           = "funding"
	RentWorkSweep             = "sweep"                                                 
	RentWorkSweepClaims       = "sweep_claims"
	RentWorkSweepBalances     = "sweep_balances"
	RentWorkSweepClaimMarkers = "sweep_claim_markers"
	RentWorkSweepClaimTotals  = "sweep_claim_totals"
	RentWorkSweepFinalize     = "sweep_finalize"
)

                                                                              
                                                                               
                         
type RentProjection struct {
	Owner                string    `json:"owner"`
	Height               uint64    `json:"as_of_height"`
	Managed              bool      `json:"managed"`
	StateVersion         uint32    `json:"state_version"`
	LastSettledHeight    uint64    `json:"last_settled_height,omitempty"`
	LastAccruedHeight    uint64    `json:"last_accrued_height,omitempty"`
	NewlyAccruedFB       string    `json:"newly_accrued_fb"`
	PendingRentFB        string    `json:"pending_rent_fb"`
	PayableFB            string    `json:"payable_fb"`
	UnpaidRentFB         string    `json:"unpaid_rent_fb"`
	AvailableFB          string    `json:"available_fb"`
	EffectiveAvailableFB string    `json:"effective_available_fb"`
	SpendableFB          string    `json:"spendable_fb"`
	RequiredFundingFB    string    `json:"required_funding_fb"`
	NominalFB            string    `json:"nominal_fb"`
	EffectiveNominalFB   string    `json:"effective_nominal_fb"`
	Mode                 string    `json:"mode"`
	ExemptAsset          string    `json:"exempt_asset,omitempty"`
	ExemptAssetMin       string    `json:"exempt_asset_min,omitempty"`
	ExemptAssetTotal     string    `json:"exempt_asset_total,omitempty"`
	ExpireHeight         uint64    `json:"expire_height,omitempty"`
	NeedsFunding         bool      `json:"needs_funding"`
	Work                 *RentWork `json:"work,omitempty"`

	state *RentState
}

                                                                              
                                            
type RentSettlement struct {
	Projection *RentProjection
	PaidFB     string
}

                                                                          
                                                                                
                                                                             
func (s *Service) ProjectRent(owner string, height uint64, policy Policy) (*RentProjection, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("rent: nil store")
	}
	policy = normalizePolicy(policy)
	owner = strings.TrimSpace(owner)
	projection := &RentProjection{
		Owner: owner, Height: height, Managed: IsRentManagedAccount(owner),
		StateVersion: RentStateVersion, Mode: RentModeExempt,
		NewlyAccruedFB: "0", PendingRentFB: "0", PayableFB: "0", UnpaidRentFB: "0",
		AvailableFB: "0", EffectiveAvailableFB: "0", SpendableFB: "0", RequiredFundingFB: "0",
		NominalFB: "0", EffectiveNominalFB: "0",
	}
	if !projection.Managed {
		return projection, nil
	}

	state, err := s.rentState(owner)
	if err != nil {
		return nil, err
	}
	projection.state = cloneRentState(state)

	available, err := s.availableBalance(owner, policy.Token)
	if err != nil {
		return nil, err
	}
	summary, err := s.NominalAsset(owner, policy.Token)
	if err != nil {
		return nil, err
	}
	nominal, err := amountFromCanonical("nominal FB", summary.Total)
	if err != nil {
		return nil, err
	}
	projection.AvailableFB = available.String()
	projection.NominalFB = nominal.String()

	accrualCursor := height
	unpaid := new(big.Int)
	mode := RentModeCharging
	if state != nil {
		projection.LastSettledHeight = state.LastSettledHeight
		projection.LastAccruedHeight = state.LastAccruedHeight
		if projection.LastAccruedHeight == 0 {
			projection.LastAccruedHeight = state.LastSettledHeight
		}
		accrualCursor = projection.LastAccruedHeight
		if accrualCursor == 0 {
			accrualCursor = height
		}
		if height < accrualCursor {
			return nil, fmt.Errorf("rent: projection height %d precedes last accrued height %d for %s", height, accrualCursor, owner)
		}
		unpaid, err = amountFromCanonical("unpaid rent", state.UnpaidFB)
		if err != nil {
			return nil, fmt.Errorf("rent: state %s: %w", owner, err)
		}
		mode = strings.TrimSpace(state.Mode)
		if mode == "" {                  
			mode = RentModeCharging
		}
	}

	newlyAccrued := new(big.Int)
	sweeping := state != nil && (mode == RentModeSweeping || (state.Work != nil && isSweepPhase(state.Work.Phase)))
	if state != nil && mode != RentModeExemptDebt && mode != RentModeExempt && !sweeping {
		blocks := new(big.Int).SetUint64(height - accrualCursor)
		newlyAccrued.Mul(blocks, policy.RentPerBlock)
	}
	pending := new(big.Int).Add(new(big.Int).Set(unpaid), newlyAccrued)
	payable := minBig(available, pending)
	unpaidAfter := new(big.Int).Sub(new(big.Int).Set(pending), payable)
	effectiveAvailable := new(big.Int).Sub(new(big.Int).Set(available), payable)
	effectiveNominal := new(big.Int).Sub(new(big.Int).Set(nominal), payable)
	if effectiveNominal.Sign() < 0 {
		return nil, fmt.Errorf("rent: payment %s exceeds nominal FB %s for %s", payable, nominal, owner)
	}

	var exemptAsset, exemptMin, exemptTotal string
	if !sweeping {
		exemptAsset, exemptMin, exemptTotal, err = s.exemptionAfterPayment(owner, effectiveNominal, policy)
		if err != nil {
			return nil, err
		}
	}
	exempt := exemptAsset != ""
	projection.NewlyAccruedFB = newlyAccrued.String()
	projection.PendingRentFB = pending.String()
	projection.PayableFB = payable.String()
	projection.UnpaidRentFB = unpaidAfter.String()
	projection.EffectiveAvailableFB = effectiveAvailable.String()
	projection.SpendableFB = effectiveAvailable.String()
	projection.EffectiveNominalFB = effectiveNominal.String()
	projection.ExemptAsset = exemptAsset
	projection.ExemptAssetMin = exemptMin
	projection.ExemptAssetTotal = exemptTotal

	switch {
	case sweeping:
		                                                                         
		                                                                         
		                                                                        
		projection.Mode = RentModeSweeping
		projection.NeedsFunding = true
		projection.SpendableFB = "0"
		projection.RequiredFundingFB = "0"
		if state != nil {
			projection.ExpireHeight = state.ExpireHeight
		}
	case unpaidAfter.Sign() > 0 && exempt:
		projection.Mode = RentModeExemptDebt
		projection.NeedsFunding = true
		projection.ExpireHeight = height
	case unpaidAfter.Sign() > 0:
		projection.Mode = RentModeNeedsFunding
		projection.NeedsFunding = true
		projection.ExpireHeight = height
	case exempt:
		projection.Mode = RentModeExempt
		projection.ExpireHeight = 0
	case effectiveAvailable.Sign() <= 0:
		projection.Mode = RentModeNeedsFunding
		projection.NeedsFunding = true
		projection.ExpireHeight = height
	default:
		projection.Mode = RentModeCharging
		projection.ExpireHeight = addHeightSaturating(height, ceilDivUint64(effectiveAvailable, policy.RentPerBlock))
	}
	if projection.NeedsFunding && !sweeping {
		required := new(big.Int).Set(unpaidAfter)
		if projection.Mode != RentModeExemptDebt {
			required.Add(required, mustAmount(DefaultActivationFeeRaw))
			required.Sub(required, effectiveAvailable)
		}
		if required.Sign() < 0 {
			required.SetInt64(0)
		}
		projection.RequiredFundingFB = required.String()
	}
	if state != nil && state.Work != nil {
		work := *state.Work
		projection.Work = &work
	}
	return projection, nil
}

                                                                           
                                                                           
                                                              
func (r *Runtime) SettleRent(owner string, height uint64) (*RentSettlement, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("rent: nil runtime store")
	}
	projection, err := NewService(
		r.store,
		WithClaimMarkerFastPath(),
		withPrefetchedReads(r.balanceRead, r.claimMarkerRead, r.claimTotalRead),
		withPrefetchedRentStates(r.stateRead),
		withServiceStats(&r.serviceStats),
	).ProjectRent(owner, height, r.policy)
	if err != nil {
		return nil, err
	}
	result := &RentSettlement{Projection: projection, PaidFB: projection.PayableFB}
	if !projection.Managed {
		return result, nil
	}
	paid, err := amountFromCanonical("rent payment", projection.PayableFB)
	if err != nil {
		return nil, err
	}
	if paid.Sign() > 0 {
		if err := r.applyRentPayment(owner, paid); err != nil {
			return nil, err
		}
	}
	if err := r.persistProjection(projection); err != nil {
		return nil, err
	}
	r.invalidateRentSettlementReadCaches(owner)
	return result, nil
}

                                                                               
                                                                              
                                            
func (r *Runtime) RefreshRentEligibility(owner string, height uint64) (*RentSettlement, error) {
	return r.SettleRent(owner, height)
}

func (s *Service) availableBalance(owner, token string) (*big.Int, error) {
	key := keys.KeyBalance(owner, token)
	data, exists, cached := cachedGet(s.balanceRead, key)
	if s.stats != nil {
		s.stats.BalanceReads++
		if cached {
			s.stats.BalanceCacheHits++
		}
	}
	if !cached {
		var err error
		data, exists, err = s.store.Get(key)
		if err != nil {
			return nil, err
		}
	}
	record := &pb.TokenBalanceRecord{Address: owner, Token: token, Balance: &pb.TokenBalance{}}
	if exists && len(data) > 0 {
		if err := proto.Unmarshal(data, record); err != nil {
			return nil, fmt.Errorf("rent: parse balance %s/%s: %w", owner, token, err)
		}
		if record.Balance == nil {
			return nil, fmt.Errorf("rent: balance %s/%s has no payload", owner, token)
		}
	}
	return amountFromCanonical("available FB", record.Balance.Balance)
}

func (s *Service) exemptionAfterPayment(owner string, effectiveNominal *big.Int, policy Policy) (string, string, string, error) {
	if effectiveNominal.Cmp(policy.ExemptMin) >= 0 {
		return policy.Token, policy.ExemptMin.String(), effectiveNominal.String(), nil
	}
	for _, asset := range policy.ExemptAssets {
		if strings.TrimSpace(asset.Token) == "" || asset.Minimum == nil || asset.Minimum.Sign() <= 0 {
			return "", "", "", fmt.Errorf("rent: invalid exempt asset policy")
		}
		summary, err := s.NominalAsset(owner, asset.Token)
		if err != nil {
			return "", "", "", err
		}
		total, err := amountFromCanonical("exempt asset total", summary.Total)
		if err != nil {
			return "", "", "", err
		}
		if total.Cmp(asset.Minimum) >= 0 {
			return asset.Token, asset.Minimum.String(), total.String(), nil
		}
	}
	return "", "", "", nil
}

func (r *Runtime) applyRentPayment(owner string, paid *big.Int) error {
	ownerRecord, _, err := readTokenBalanceRecord(r.store, owner, r.policy.Token)
	if err != nil {
		return err
	}
	available, err := amountFromCanonical("available FB", ownerRecord.Balance.Balance)
	if err != nil {
		return err
	}
	if available.Cmp(paid) < 0 {
		return fmt.Errorf("rent: available FB changed during settlement for %s: have %s need %s", owner, available, paid)
	}
	var fundRecord *pb.TokenBalanceRecord
	if r.recycleTotals == nil {
		fundRecord, err = r.recycleBalanceAfter(r.policy.Token, paid)
		if err != nil {
			return err
		}
	}
	ownerRecord.Balance.Balance = new(big.Int).Sub(available, paid).String()
	if err := writeTokenBalanceRecord(r.store, ownerRecord); err != nil {
		return err
	}
	if r.recycleTotals != nil {
		if r.recycleTotals[r.policy.Token] == nil {
			r.recycleTotals[r.policy.Token] = new(big.Int)
		}
		r.recycleTotals[r.policy.Token].Add(r.recycleTotals[r.policy.Token], paid)
		return nil
	}
	return writeTokenBalanceRecord(r.store, fundRecord)
}

func (r *Runtime) persistProjection(p *RentProjection) error {
	old := p.state
	if p.Mode == RentModeExempt && p.UnpaidRentFB == "0" {
		r.clearState(p.Owner, old)
		p.state = nil
		p.LastSettledHeight = 0
		p.LastAccruedHeight = 0
		p.Work = nil
		return nil
	}
	nextAction := p.ExpireHeight
	phase := RentWorkSettle
	cursor := ""
	if p.NeedsFunding {
		nextAction = p.Height
		phase = RentWorkFunding
		if old != nil && old.Work != nil {
			if p.Mode == RentModeSweeping && old.Work.NextActionHeight != 0 {
				nextAction = old.Work.NextActionHeight
			}
			if old.Work.Phase != "" && old.Work.Phase != RentWorkSettle {
				phase = old.Work.Phase
			}
			cursor = old.Work.Cursor
		}
	}
	state := &RentState{
		Version: RentStateVersion, Owner: p.Owner,
		LastSettledHeight: p.Height, LastAccruedHeight: p.Height,
		UnpaidFB: p.UnpaidRentFB, Mode: p.Mode, ExpireHeight: p.ExpireHeight,
		NominalFB: p.EffectiveNominalFB,
		Work:      &RentWork{NextActionHeight: nextAction, Phase: phase, Cursor: cursor},
	}
	if err := r.writeAccrualState(state, old); err != nil {
		return err
	}
	p.state = cloneRentState(state)
	p.LastSettledHeight = state.LastSettledHeight
	p.LastAccruedHeight = state.LastAccruedHeight
	p.Work = cloneRentState(state).Work
	return nil
}

func (r *Runtime) writeAccrualState(state, old *RentState) error {
	if state == nil || state.Owner == "" {
		return fmt.Errorf("rent: invalid state")
	}
	if oldKey := rentStateWorkKey(old); oldKey != "" && oldKey != rentStateWorkKey(state) {
		r.store.Del(oldKey)
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return err
	}
	r.store.Set(keys.KeyRentState(state.Owner), encoded)
	if key := rentStateWorkKey(state); key != "" {
		r.store.Set(key, []byte(state.Owner))
		r.observeRentWork(state)
	}
	return nil
}

func readTokenBalanceRecord(store Store, owner, token string) (*pb.TokenBalanceRecord, bool, error) {
	data, exists, err := store.Get(keys.KeyBalance(owner, token))
	if err != nil {
		return nil, false, err
	}
	record := &pb.TokenBalanceRecord{Address: owner, Token: token, Balance: &pb.TokenBalance{}}
	if !exists || len(data) == 0 {
		return record, false, nil
	}
	if err := proto.Unmarshal(data, record); err != nil {
		return nil, false, fmt.Errorf("rent: parse balance %s/%s: %w", owner, token, err)
	}
	if record.Balance == nil {
		return nil, false, fmt.Errorf("rent: balance %s/%s has no payload", owner, token)
	}
	if record.Address == "" {
		record.Address = owner
	}
	if record.Token == "" {
		record.Token = token
	}
	return record, true, nil
}

func writeTokenBalanceRecord(store MutableStore, record *pb.TokenBalanceRecord) error {
	if record == nil || record.Balance == nil || record.Address == "" || record.Token == "" {
		return fmt.Errorf("rent: invalid balance record")
	}
	key := keys.KeyBalance(record.Address, record.Token)
	if TokenBalanceTotal(record.Balance).Sign() == 0 {
		store.Del(key)
		return nil
	}
	data, err := proto.Marshal(record)
	if err != nil {
		return err
	}
	store.Set(key, data)
	return nil
}

func amountFromCanonical(label, raw string) (*big.Int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return new(big.Int), nil
	}
	amount, ok := new(big.Int).SetString(raw, 10)
	if !ok || amount.Sign() < 0 || amount.String() != raw {
		return nil, fmt.Errorf("rent: invalid %s %q", label, raw)
	}
	return amount, nil
}

func minBig(a, b *big.Int) *big.Int {
	if a.Cmp(b) <= 0 {
		return new(big.Int).Set(a)
	}
	return new(big.Int).Set(b)
}

func tokenBalanceTotalCanonical(label string, balance *pb.TokenBalance) (*big.Int, error) {
	total := new(big.Int)
	if balance == nil {
		return total, nil
	}
	for _, field := range []struct{ name, raw string }{
		{"available", balance.Balance},
		{"miner_locked", balance.MinerLockedBalance},
		{"liquid_locked", balance.LiquidLockedBalance},
		{"witness_locked", balance.WitnessLockedBalance},
		{"leverage_locked", balance.LeverageLockedBalance},
		{"order_frozen", balance.OrderFrozenBalance},
	} {
		amount, err := amountFromCanonical(label+" "+field.name, field.raw)
		if err != nil {
			return nil, err
		}
		total.Add(total, amount)
	}
	return total, nil
}

func addHeightSaturating(height, delta uint64) uint64 {
	if delta > math.MaxUint64-height {
		return math.MaxUint64
	}
	return height + delta
}

func cloneRentState(state *RentState) *RentState {
	if state == nil {
		return nil
	}
	out := *state
	if state.Work != nil {
		work := *state.Work
		out.Work = &work
	}
	return &out
}

func rentStateWorkKey(state *RentState) string {
	if state == nil || state.Owner == "" {
		return ""
	}
	height := state.ExpireHeight
	if state.Work != nil && state.Work.NextActionHeight != 0 {
		height = state.Work.NextActionHeight
	}
	if height == 0 {
		return ""
	}
	return keys.KeyRentExpiry(height, state.Owner)
}

func isSweepPhase(phase string) bool {
	phase = strings.TrimSpace(phase)
	return phase == RentWorkSweep || strings.HasPrefix(phase, RentWorkSweep+"_")
}
