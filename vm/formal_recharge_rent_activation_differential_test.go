package vm

import (
	"dex/formal/itftrace"
	"dex/formal/refinement"
	"dex/keys"
	"dex/pb"
	"dex/rent"
	"os"
	"reflect"
	"testing"
)

type formalRechargeRentHarness struct {
	fixture       *rechargeRentFixture
	mode          string
	rentProcessed bool
}

func TestFormalRechargeRentActivationITFDifferential(t *testing.T) {
	cases := []struct {
		name string
		env  string
	}{
		{name: "exact-threshold-exempt", env: "DEX_FORMAL_ITF_RECHARGE_RENT_EXEMPT"},
		{name: "subthreshold-funded", env: "DEX_FORMAL_ITF_RECHARGE_RENT_FUNDED"},
		{name: "subthreshold-no-liquidity", env: "DEX_FORMAL_ITF_RECHARGE_RENT_SWEPT"},
	}
	if os.Getenv(cases[0].env) == "" {
		t.Skip("recharge/rent activation ITF paths are set by formal/run-go-differential.ps1")
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runFormalRechargeRentActivationTrace(t, os.Getenv(tc.env))
		})
	}
}

func runFormalRechargeRentActivationTrace(t *testing.T, path string) {
	t.Helper()
	trace, err := itftrace.Load(path)
	if err != nil {
		t.Fatalf("load Quint recharge/rent activation ITF trace: %v", err)
	}
	if len(trace.States) != 4 {
		t.Fatalf("recharge/rent activation differential trace states=%d want=4", len(trace.States))
	}
	initial, err := refinement.RechargeRentActivationFromITF(trace.States[0])
	if err != nil {
		t.Fatal(err)
	}
	h := newFormalRechargeRentHarness(t, initial)
	h.assertStateRelation(t, trace.States[0], 0)
	for i := 1; i < len(trace.States); i++ {
		before, err := refinement.RechargeRentActivationFromITF(trace.States[i-1])
		if err != nil {
			t.Fatal(err)
		}
		after, err := refinement.RechargeRentActivationFromITF(trace.States[i])
		if err != nil {
			t.Fatal(err)
		}
		h.applyModelStep(t, before, after)
		h.assertStateRelation(t, trace.States[i], i)
	}
}

func newFormalRechargeRentHarness(t *testing.T, initial refinement.RechargeRentActivationState) *formalRechargeRentHarness {
	t.Helper()
	var liquidity int64
	var market bool
	switch initial.Mode {
	case "ExemptCase", "FundingCase":
		liquidity = rechargeRentLiquidity
		market = true
	case "NoLiquidityCase":
	case "":
		t.Fatal("recharge/rent model has no mode")
	default:
		t.Fatalf("unknown recharge/rent mode %q", initial.Mode)
	}
	fixture := newRechargeRentFixture(t, "formal_recharge_rent_"+initial.Mode, initial.DepositAmount, liquidity, market)
	return &formalRechargeRentHarness{fixture: fixture, mode: initial.Mode}
}

func (h *formalRechargeRentHarness) applyModelStep(t *testing.T, before, after refinement.RechargeRentActivationState) {
	t.Helper()
	switch before.Phase {
	case "AwaitingRecharge":
		h.fixture.finalize(t)
	case "Credited":
		h.fixture.settleRent(t)
		h.rentProcessed = true
	case "Exempt", "Funded", "Swept":
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("terminal recharge/rent model state changed: before=%+v after=%+v", before, after)
		}
		h.fixture.settleRent(t)
	default:
		t.Fatalf("unsupported recharge/rent phase %q", before.Phase)
	}
}

func (h *formalRechargeRentHarness) assertStateRelation(t *testing.T, model itftrace.State, step int) {
	t.Helper()
	expected, err := refinement.RechargeRentActivationFromITF(model)
	if err != nil {
		t.Fatal(err)
	}
	actual := h.productionState(t)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("recharge/rent state relation failed at step=%d action=%s\nactual:   %+v\nexpected: %+v", step, model.Action, actual, expected)
	}
	AuditGlobalConservationDiff(h.fixture.sv.Diff(), h.fixture.height, h.fixture.read)
}

func (h *formalRechargeRentHarness) productionState(t *testing.T) refinement.RechargeRentActivationState {
	t.Helper()
	f := h.fixture
	ownerAsset := formalRentBalanceTotal(t, f.sv, f.receiver, f.asset)
	ownerFB := formalRentBalanceTotal(t, f.sv, f.receiver, rent.FeeToken)
	makerAsset := formalRentBalanceTotal(t, f.sv, f.maker, f.asset)
	makerFB := formalRentBalanceTotal(t, f.sv, f.maker, rent.FeeToken)
	recycleAsset := formalRentBalanceTotal(t, f.sv, rent.RecycleFundAccount, f.asset)
	account, accountPresent := f.account(t)
	lease, rentStatePresent := f.rentState(t)
	credited := false
	if _, exists, err := f.sv.Get(keys.KeyRechargeCredited(f.req.RequestId)); err != nil {
		t.Fatal(err)
	} else {
		credited = exists
	}
	marketOrderOpen := false
	if raw, exists, err := f.sv.Get(keys.KeyOrderState(f.orderID)); err != nil {
		t.Fatal(err)
	} else if exists {
		var order pb.OrderState
		if err := unmarshalProtoCompat(raw, &order); err != nil {
			t.Fatal(err)
		}
		marketOrderOpen = order.Status == pb.OrderStateStatus_ORDER_OPEN && !order.IsFilled
	}

	result := refinement.RechargeRentActivationState{
		Mode: h.mode, Height: int64(f.height), DepositAmount: f.deposit,
		OwnerAsset: ownerAsset, OwnerFB: ownerFB, MakerAsset: makerAsset, MakerFB: makerFB,
		RecycleAsset: recycleAsset, FBTotal: f.liquidity,
		TotalSupply:    formalRechargeTotalSupply(t, f.sv, f.asset),
		VaultAvailable: formalSmallStateInt(t, f.sv, keys.KeyFrostVaultAvailableBalance(f.req.NativeChain, f.asset, f.req.VaultId)),
		Credited:       credited, AccountPresent: accountPresent, RentState: rentStatePresent,
		MarketOrderOpen: marketOrderOpen,
	}
	if account != nil {
		result.Nonce = int64(account.Nonce)
	}
	if lease != nil {
		result.ExpireHeight = int64(lease.ExpireHeight)
		if _, exists, err := f.sv.Get(keys.KeyRentExpiry(lease.ExpireHeight, f.receiver)); err != nil {
			t.Fatal(err)
		} else {
			result.ExpiryIndex = exists
		}
	}
	switch {
	case !credited:
		result.Phase = "AwaitingRecharge"
	case !h.rentProcessed:
		result.Phase = "Credited"
	case !accountPresent:
		result.Phase = "Swept"
	case ownerAsset >= rechargeRentThreshold && !rentStatePresent:
		result.Phase = "Exempt"
	case ownerFB > 0 && rentStatePresent:
		result.Phase = "Funded"
	default:
		result.Phase = "Credited"
	}
	return result
}

func formalRentBalanceTotal(t *testing.T, sv StateView, owner, token string) int64 {
	t.Helper()
	return formalSmallBalance(t, rent.TokenBalanceTotal(GetBalance(sv, owner, token)).String())
}
