package vm

import (
	"dex/keys"
	"dex/pb"
	"dex/rent"
	"dex/utils"
	"encoding/json"
	"math/big"
	"strconv"
	"testing"

	"google.golang.org/protobuf/proto"
)

const (
	rechargeRentUSDTContract = "0xdAC17F958D2ee523a2206206994597C13D831ec7"
	rechargeRentThreshold    = int64(10_000_000)
	rechargeRentDeposit      = int64(6_000)
	rechargeRentLiquidity    = int64(3_000)
	rechargeRentHeight       = uint64(50_000)
	rechargeRentMaker        = "rent-recharge-maker"
	rechargeRentOrderID      = "rent-recharge-maker-order"
)

type rechargeRentFixture struct {
	sv        StateView
	read      ReadThroughFn
	req       *pb.RechargeRequest
	asset     string
	receiver  string
	maker     string
	orderID   string
	height    uint64
	deposit   int64
	liquidity int64
}

func newRechargeRentFixture(t *testing.T, requestID string, deposit, liquidity int64, withMarket bool) *rechargeRentFixture {
	t.Helper()
	if deposit <= 0 || liquidity < 0 {
		t.Fatalf("invalid recharge/rent fixture deposit=%d liquidity=%d", deposit, liquidity)
	}
	initial := newMemStateView()
	req := seedAccountRechargeFinalizationTest(t, initial, requestID, pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD)
	asset := testBridgeAsset(t, "eth", rechargeRentUSDTContract)
	req.TokenAddress = asset.ID
	req.NativeContract = asset.Contract
	req.Amount = strconv.FormatInt(deposit, 10)
	req.FinalizeHeight = rechargeRentHeight
	rawRequest, err := proto.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	initial.Set(keys.KeyRechargeRequest(req.RequestId), rawRequest)

	policyAsset := false
	for _, candidate := range rent.DefaultPolicy().ExemptAssets {
		if candidate.Token == asset.ID {
			policyAsset = true
			if candidate.Minimum.Cmp(big.NewInt(rechargeRentThreshold)) != 0 {
				t.Fatalf("unexpected USDT rent threshold %s", candidate.Minimum)
			}
			break
		}
	}
	if !policyAsset {
		t.Fatalf("USDT asset %s is absent from the built-in rent exemption policy", asset.ID)
	}

	putTestToken(t, initial, &pb.Token{
		Address: asset.ID, Owner: "system", TotalSupply: "0", Type: pb.TokenType_TOKEN_BRIDGED,
		NativeChain: asset.Chain, NativeContract: asset.Contract,
	})
	putTestToken(t, initial, &pb.Token{Address: rent.FeeToken, Owner: "system", TotalSupply: strconv.FormatInt(liquidity, 10)})
	if withMarket {
		giveSpotAccount(t, initial, rechargeRentMaker)
		giveBalance(t, initial, rechargeRentMaker, rent.FeeToken, big.NewInt(liquidity))
		fundingOrder(t, initial, rechargeRentMaker, rechargeRentOrderID, asset.ID, rent.FeeToken, pb.OrderSide_BUY, liquidity, PriceScale.Int64())
		pair := utils.GeneratePairKey(asset.ID, rent.FeeToken)
		initial.Set(keys.KeySpotLastPrice(pair), new(big.Int).Set(PriceScale).Bytes())
	}

	sv, read := persistFundingFixture(initial)
	return &rechargeRentFixture{
		sv: sv, read: read, req: req, asset: asset.ID, receiver: req.ReceiverAddress,
		maker: rechargeRentMaker, orderID: rechargeRentOrderID, height: rechargeRentHeight,
		deposit: deposit, liquidity: liquidity,
	}
}

func (f *rechargeRentFixture) finalize(t *testing.T) {
	t.Helper()
	if err := applyRechargeFinalized(f.sv, f.req, f.height, 1); err != nil {
		t.Fatalf("finalize recharge: %v", err)
	}
}

func (f *rechargeRentFixture) settleRent(t *testing.T) rent.ProcessBlockStats {
	t.Helper()
	owners, stats, err := (&Executor{}).processRentHooks(&pb.Block{Header: &pb.BlockHeader{Height: f.height}}, f.sv, nil)
	if err != nil {
		t.Fatalf("process rent hook: %v", err)
	}
	if owners == 0 {
		t.Fatal("finalized recharge receiver was not included in rent processing")
	}
	return stats
}

func (f *rechargeRentFixture) account(t *testing.T) (*pb.Account, bool) {
	t.Helper()
	raw, exists, err := f.sv.Get(keys.KeyAccount(f.receiver))
	if err != nil || !exists {
		return nil, exists
	}
	var account pb.Account
	if err := proto.Unmarshal(raw, &account); err != nil {
		t.Fatal(err)
	}
	return &account, true
}

func (f *rechargeRentFixture) rentState(t *testing.T) (*rent.RentState, bool) {
	t.Helper()
	raw, exists, err := f.sv.Get(keys.KeyRentState(f.receiver))
	if err != nil || !exists {
		return nil, exists
	}
	var state rent.RentState
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	return &state, true
}

func TestRechargeRentExactExternalThresholdStaysExemptWithoutConversion(t *testing.T) {
	f := newRechargeRentFixture(t, "formal_recharge_rent_exempt_unit", rechargeRentThreshold, rechargeRentLiquidity, true)
	f.finalize(t)
	stats := f.settleRent(t)

	account, exists := f.account(t)
	if !exists || account.Nonce != 0 {
		t.Fatalf("new receiver account must remain present with nonce zero: exists=%v account=%+v", exists, account)
	}
	if got := GetBalance(f.sv, f.receiver, f.asset).Balance; got != strconv.FormatInt(rechargeRentThreshold, 10) {
		t.Fatalf("exempt recharge balance=%s", got)
	}
	if got := GetBalance(f.sv, f.receiver, rent.FeeToken).Balance; got != "0" && got != "" {
		t.Fatalf("exempt recharge unexpectedly converted to FB: %s", got)
	}
	if _, exists := f.rentState(t); exists {
		t.Fatal("externally exempt receiver retained a rent lease")
	}
	if _, orderExists, err := f.sv.Get(keys.KeyOrderState(f.orderID)); err != nil || !orderExists {
		t.Fatalf("exemption consumed available liquidity: exists=%v err=%v", orderExists, err)
	}
	if stats.ExemptAccounts != 1 || stats.SweptAccounts != 0 {
		t.Fatalf("unexpected exemption stats: %+v", stats)
	}
	AuditGlobalConservationDiff(f.sv.Diff(), f.height, f.read)
}

func TestRechargeRentSubthresholdUsesLiquidityAndRenewsNewAccount(t *testing.T) {
	f := newRechargeRentFixture(t, "formal_recharge_rent_funded_unit", rechargeRentDeposit, rechargeRentLiquidity, true)
	f.finalize(t)
	f.settleRent(t)

	account, exists := f.account(t)
	if !exists || account.Nonce != 0 {
		t.Fatalf("funded receiver account must remain present with nonce zero: exists=%v account=%+v", exists, account)
	}
	if got := GetBalance(f.sv, f.receiver, f.asset).Balance; got != "3000" {
		t.Fatalf("remaining external asset=%s want=3000", got)
	}
	if got := GetBalance(f.sv, f.receiver, rent.FeeToken).Balance; got != "3000" {
		t.Fatalf("automatic rent funding FB=%s want=3000", got)
	}
	state, exists := f.rentState(t)
	if !exists || state.NominalFB != "3000" || state.ExpireHeight != f.height+30 {
		t.Fatalf("automatic rent funding did not write the expected lease: %+v exists=%v", state, exists)
	}
	if got := GetBalance(f.sv, f.maker, f.asset).Balance; got != "3000" {
		t.Fatalf("maker external asset=%s want=3000", got)
	}
	if got := GetBalance(f.sv, rent.RecycleFundAccount, f.asset).Balance; got != "0" && got != "" {
		t.Fatalf("funded recharge was also recycled: %s", got)
	}
	AuditGlobalConservationDiff(f.sv.Diff(), f.height, f.read)
}

func TestRechargeRentSubthresholdWithoutLiquidityIsRecycled(t *testing.T) {
	f := newRechargeRentFixture(t, "formal_recharge_rent_swept_unit", rechargeRentDeposit, 0, false)
	f.finalize(t)
	stats := f.settleRent(t)

	if _, exists := f.account(t); exists {
		t.Fatal("unfunded subthreshold receiver account was not recycled")
	}
	if got := GetBalance(f.sv, f.receiver, f.asset).Balance; got != "0" && got != "" {
		t.Fatalf("recycled receiver retained external asset: %s", got)
	}
	if got := GetBalance(f.sv, rent.RecycleFundAccount, f.asset).Balance; got != strconv.FormatInt(rechargeRentDeposit, 10) {
		t.Fatalf("recycle fund asset=%s want=%d", got, rechargeRentDeposit)
	}
	if _, credited, err := f.sv.Get(keys.KeyRechargeCredited(f.req.RequestId)); err != nil || !credited {
		t.Fatalf("rent sweep removed finalized recharge evidence: credited=%v err=%v", credited, err)
	}
	if stats.SweptAccounts != 1 {
		t.Fatalf("unexpected sweep stats: %+v", stats)
	}
	AuditGlobalConservationDiff(f.sv.Diff(), f.height, f.read)
}
