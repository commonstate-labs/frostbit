package vm

import (
	dbpkg "dex/db"
	"dex/future"
	"dex/keys"
	"dex/pb"
	"encoding/binary"
	"fmt"
	"math/big"
	"runtime"
	"sort"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
)

type fundingScanErrorView struct {
	StateView
	failPrefix string
}

func TestDeleverageActivityKind(t *testing.T) {
	if got := deleverageActivityKind(nil); got != ActKindInsuredDeleverage {
		t.Fatalf("nil withholding kind=%s", got)
	}
	if got := deleverageActivityKind(new(big.Int)); got != ActKindInsuredDeleverage {
		t.Fatalf("zero withholding kind=%s", got)
	}
	if got := deleverageActivityKind(big.NewInt(1)); got != ActKindADL {
		t.Fatalf("positive withholding kind=%s", got)
	}
}

type fundingReadErrorView struct {
	StateView
	failKey string
}

func (v *fundingReadErrorView) Get(key string) ([]byte, bool, error) {
	if key == v.failKey {
		return nil, false, fmt.Errorf("injected funding read failure")
	}
	return v.StateView.Get(key)
}

func (v *fundingScanErrorView) Scan(prefix string) (map[string][]byte, error) {
	if prefix == v.failPrefix {
		return nil, fmt.Errorf("injected funding position scan failure")
	}
	return v.StateView.Scan(prefix)
}

type liquidationScanErrorView struct {
	StateView
	failScanPrefix    string
	failForwardPrefix string
	failReversePrefix string
	failGetKey        string
}

func (v *liquidationScanErrorView) Get(key string) ([]byte, bool, error) {
	if key == v.failGetKey {
		return nil, false, fmt.Errorf("injected liquidation get failure for %s", key)
	}
	return v.StateView.Get(key)
}

func (v *liquidationScanErrorView) Scan(prefix string) (map[string][]byte, error) {
	if prefix == v.failScanPrefix {
		return nil, fmt.Errorf("injected liquidation full-scan failure")
	}
	return v.StateView.Scan(prefix)
}

func (v *liquidationScanErrorView) ScanWithLimit(prefix string, limit int) (map[string][]byte, error) {
	if prefix == v.failForwardPrefix {
		return nil, fmt.Errorf("injected liquidation forward-index failure")
	}
	return v.StateView.ScanWithLimit(prefix, limit)
}

func (v *liquidationScanErrorView) ScanWithLimitReverse(prefix string, limit int) (map[string][]byte, error) {
	if prefix == v.failReversePrefix {
		return nil, fmt.Errorf("injected liquidation reverse-index failure")
	}
	return v.StateView.ScanWithLimitReverse(prefix, limit)
}

func TestPreviewFutureLiquidationCandidatesParallelMatchesSerial(t *testing.T) {
	oldProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(oldProcs)

	sv := newMemStateView()
	pair := "BTC_USDT"
	sv.Set(keys.KeyFutureMarkPrice(pair), big.NewInt(100).Bytes())
	sv.Set(keys.KeyFutureSettleMarkPrice(pair), big.NewInt(100).Bytes())
	if err := (&svCumulativeFundingStore{sv: sv}).SetCumulativeFunding(pair, big.NewInt(10), big.NewInt(10)); err != nil {
		t.Fatal(err)
	}
	eng := newFutureEngine(sv, 1)
	cfg := &future.MarketConfig{Pair: pair, QuoteToken: "USDT", MaintenanceMarginRateBps: 50}
	positions := make([]*future.Position, 0, parallelFutureHookPreviewMinPositions+64)
	for i := 0; i < parallelFutureHookPreviewMinPositions+64; i++ {
		side := future.Long
		if i%2 == 1 {
			side = future.Short
		}
		margin := big.NewInt(1000000)
		if i%11 == 0 {
			margin = big.NewInt(1)
		}
		positions = append(positions, &future.Position{
			ID:                    fmt.Sprintf("pos_%05d", i),
			Owner:                 fmt.Sprintf("owner_%05d", i),
			Pair:                  pair,
			Side:                  side,
			EntryPrice:            big.NewInt(100),
			Quantity:              big.NewInt(int64(i + 1)),
			Margin:                margin,
			Status:                future.StatusOpen,
			LastCumulativeFunding: big.NewInt(0),
		})
	}

	serial, err := previewFutureLiquidationCandidates(eng, cfg, positions, big.NewInt(100))
	if err != nil {
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(4)
	parallel, err := previewFutureLiquidationCandidates(eng, cfg, positions, big.NewInt(100))
	if err != nil {
		t.Fatal(err)
	}
	if len(serial) != len(parallel) {
		t.Fatalf("candidate len mismatch: serial=%d parallel=%d", len(serial), len(parallel))
	}
	for i := range serial {
		if serial[i] != parallel[i] {
			t.Fatalf("candidate[%d] mismatch: serial=%+v parallel=%+v", i, serial[i], parallel[i])
		}
	}
}

                                                          
  
                                                                                                  
                                                                                                         
                                                                                              
                                                                                                     
  
                                                                                                      
                     
func TestUpdateMarkPriceEMA_IdleSideNotRefed(t *testing.T) {
	sv := newMemStateView()
	pair := "FB_USDT"

	const unit int64 = 1e8
	price := func(v int64) []byte { return big.NewInt(v * unit).Bytes() }

	                                     
	sv.Set(keys.KeyFutureSpotEMA(pair), price(15))
	sv.Set(keys.KeyFutureEMA(pair), price(15))

	                                                                 
	sv.Set(keys.KeyFutureLastPrice(pair), price(10))
	sv.Set(keys.KeySpotLastPrice(pair), price(15))
	updateMarkPriceEMA(sv, pair, true, true)

	snapFutEMA := readPosBig(sv, keys.KeyFutureEMA(pair))
	                                                                                                           
	if snapFutEMA.Cmp(big.NewInt(14*unit)) <= 0 || snapFutEMA.Cmp(big.NewInt(15*unit)) >= 0 {
		t.Fatalf("futEMA after one perp trade should be in (14,15)*unit, got %v", snapFutEMA)
	}

	                                                                                                          
	for i := 0; i < 200; i++ {
		updateMarkPriceEMA(sv, pair, true, false)
	}

	                                                                                               
	futEMA := readPosBig(sv, keys.KeyFutureEMA(pair))
	if futEMA.Cmp(snapFutEMA) != 0 {
		t.Fatalf("futEMA drifted while perp was idle: snap=%v now=%v", snapFutEMA, futEMA)
	}

	                                                                                                             
	                                                                                                           
}

                                                                             
                                                  
func TestUpdateMarkPriceEMA_BothSidesConverge(t *testing.T) {
	sv := newMemStateView()
	pair := "FB_USDT"

	sv.Set(keys.KeySpotLastPrice(pair), big.NewInt(15).Bytes())
	sv.Set(keys.KeyFutureLastPrice(pair), big.NewInt(15).Bytes())

	for i := 0; i < 50; i++ {
		updateMarkPriceEMA(sv, pair, true, true)
	}

	spotEMA := readPosBig(sv, keys.KeyFutureSpotEMA(pair))
	futEMA := readPosBig(sv, keys.KeyFutureEMA(pair))
	if spotEMA == nil || futEMA == nil {
		t.Fatalf("both EMAs should be seeded, got spot=%v fut=%v", spotEMA, futEMA)
	}

	rate := future.CalcFundingRate(futEMA, spotEMA, future.DefaultMaxFundingRateBps)
	if rate != 0 {
		t.Fatalf("equal EMAs should give rate=0, got %d", rate)
	}
}

                                                                                   
                                                     
func TestUpdateMarkPriceEMA_OnlyFutureChanged(t *testing.T) {
	sv := newMemStateView()
	pair := "FB_USDT"

	sv.Set(keys.KeySpotLastPrice(pair), big.NewInt(10).Bytes())
	sv.Set(keys.KeyFutureSpotEMA(pair), big.NewInt(10).Bytes())

	sv.Set(keys.KeyFutureLastPrice(pair), big.NewInt(20).Bytes())
	for i := 0; i < 50; i++ {
		updateMarkPriceEMA(sv, pair, false, true)
	}

	spotEMA := readPosBig(sv, keys.KeyFutureSpotEMA(pair))
	if spotEMA == nil || spotEMA.Cmp(big.NewInt(10)) != 0 {
		t.Fatalf("spotEMA should stay at 10 (no spot trade), got %v", spotEMA)
	}
	futEMA := readPosBig(sv, keys.KeyFutureEMA(pair))
	if futEMA == nil || futEMA.Cmp(big.NewInt(19)) < 0 || futEMA.Cmp(big.NewInt(21)) > 0 {
		t.Fatalf("futEMA should converge near 20, got %v", futEMA)
	}
}

func TestUpdateMarkPriceEMA_ColdStartFutureDoesNotSeedSpotFromFuture(t *testing.T) {
	sv := newMemStateView()
	pair := "BTC_USDT"
	spot := big.NewInt(100)
	fut := big.NewInt(200)
	sv.Set(keys.KeySpotLastPrice(pair), spot.Bytes())
	sv.Set(keys.KeyFutureLastPrice(pair), fut.Bytes())

	updateMarkPriceEMA(sv, pair, false, true)

	mark := readPosBig(sv, keys.KeyFutureMarkPrice(pair))
	if mark == nil || mark.Cmp(spot) != 0 {
		t.Fatalf("cold-start mark should stay anchored to spot last price, got %v want %v", mark, spot)
	}
}

                                                                            
                                                                                                      
                                                                                                                  
                                                                                   
  
                                                                                                    
                                                                                                    
                                                              
  
                                                                                             
                                                                             
func TestProcessFutureBlockHooks_FundingRateNotStuckAtClamp(t *testing.T) {
	sv := newMemStateView()
	pair := "FB_USDT"
	sv.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))

	const unit int64 = 1e8
	price := func(v int64) []byte { return big.NewInt(v * unit).Bytes() }

	                                                                                                        
	seq := []int64{12, 18, 11, 19, 13, 17, 10, 20, 14, 16, 13, 17}

	onlySpot := map[string]bool{pair: true}
	onlyFut := map[string]bool{pair: true}
	empty := map[string]bool{}

	var height uint64 = 1
	                                                                                          
	for cycle := 0; cycle < 4; cycle++ {
		for i := range seq {
			sv.Set(keys.KeySpotLastPrice(pair), price(seq[i]))
			processFutureBlockHooks(sv, height, onlySpot, empty)
			height++

			sv.Set(keys.KeyFutureLastPrice(pair), price(seq[i]))
			processFutureBlockHooks(sv, height, empty, onlyFut)
			height++
		}
	}

	                                                   
	for ; height%future.DefaultFundingInterval != 0; height++ {
		processFutureBlockHooks(sv, height, empty, empty)
	}
	processFutureBlockHooks(sv, height, empty, empty)

	data, exists, _ := sv.Get(keys.KeyFutureFundingRate(pair))
	if !exists || len(data) != 8 {
		t.Fatal("funding rate not written")
	}
	rate := int64(binary.BigEndian.Uint64(data))

	if rate == future.DefaultMaxFundingRateBps || rate == -future.DefaultMaxFundingRateBps {
		spotEMA := readPosBig(sv, keys.KeyFutureSpotEMA(pair))
		futEMA := readPosBig(sv, keys.KeyFutureEMA(pair))
		t.Fatalf("funding rate clamped at %d — spotEMA=%v futEMA=%v (idle side corruption?)",
			rate, spotEMA, futEMA)
	}
}

                                                                                                                                   
func TestProcessFutureBlockHooks_FundingSettlementSweepsHealthyPositions(t *testing.T) {
	sv := newMemStateView()
	pair := "BTC_USDT"
	sv.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))

	const unit int64 = 1e8
	                                                 
	spotPrice := big.NewInt(50000 * unit)
	sv.Set(keys.KeySpotLastPrice(pair), spotPrice.Bytes())
	sv.Set(keys.KeyFutureLastPrice(pair), new(big.Int).Mul(big.NewInt(50500), big.NewInt(unit)).Bytes())
	sv.Set(keys.KeyFutureSpotEMA(pair), spotPrice.Bytes())
	sv.Set(keys.KeyFutureEMA(pair), new(big.Int).Mul(big.NewInt(50500), big.NewInt(unit)).Bytes())
	sv.Set(keys.KeyFutureMarkPrice(pair), spotPrice.Bytes())

	                                          
	aliceQty := big.NewInt(unit)         
	aliceMargin := future.CalcRequiredMargin(aliceQty, big.NewInt(50000*unit), 2)
	alicePos := &pb.FuturePositionState{
		PositionId: "alice_pos", Owner: "alice", Pair: pair,
		Side:                  int32(future.Long),
		EntryPrice:            big.NewInt(50000 * unit).String(),
		Quantity:              aliceQty.String(),
		Margin:                aliceMargin.String(),
		Status:                int32(future.StatusOpen),
		OpenHeight:            1,
		LastCumulativeFunding: "0",
	}
	data, _ := proto.Marshal(alicePos)
	sv.Set(keys.KeyFuturePosition("alice_pos"), data)
	sv.Set(keys.KeyFuturePairPosition(pair, "alice_pos"), data)

	bobPos := &pb.FuturePositionState{
		PositionId: "bob_pos", Owner: "bob", Pair: pair,
		Side:                  int32(future.Short),
		EntryPrice:            big.NewInt(50000 * unit).String(),
		Quantity:              aliceQty.String(),
		Margin:                aliceMargin.String(),
		Status:                int32(future.StatusOpen),
		OpenHeight:            1,
		LastCumulativeFunding: "0",
	}
	bobData, _ := proto.Marshal(bobPos)
	sv.Set(keys.KeyFuturePosition("bob_pos"), bobData)
	sv.Set(keys.KeyFuturePairPosition(pair, "bob_pos"), bobData)

	                                                                                             
	idx := &svLiquidationIndex{sv: sv}
	cfg := &future.MarketConfig{Pair: pair, MaxLeverage: future.DefaultMaxLeverage, MaintenanceMarginRateBps: future.DefaultMaintenanceMarginRateBps, QuoteToken: "USDT"}
	liqPrice := future.CalcLiquidationPrice(future.Long, big.NewInt(50000*unit), aliceMargin, aliceQty, cfg.MaintenanceMarginRateOrDefault())
	_ = idx.Upsert("alice_pos", pair, future.Long, liqPrice)

	                                                                             
	spotChanged := map[string]bool{pair: true}
	futChanged := map[string]bool{pair: true}
	for h := uint64(1); h <= 3*future.DefaultFundingInterval; h++ {
		processFutureBlockHooks(sv, h, spotChanged, futChanged)
	}

	                                                                                
	cfData, exists, _ := sv.Get(keys.KeyFutureCumulativeFunding(pair))
	if !exists {
		t.Fatal("cumulative funding not written")
	}
	var cf pb.FutureCumulativeFunding
	if err := proto.Unmarshal(cfData, &cf); err != nil {
		t.Fatal(err)
	}
	if cf.CumulativeLong == "" || cf.CumulativeLong == "0" {
		t.Fatalf("cumulative_long should grow, got %q", cf.CumulativeLong)
	}

	posData, _, _ := sv.Get(keys.KeyFuturePosition("alice_pos"))
	var back pb.FuturePositionState
	_ = proto.Unmarshal(posData, &back)
	if back.LastCumulativeFunding != cf.CumulativeLong {
		t.Fatalf("alice funding should be swept at settlement: lastCum=%s cum=%s", back.LastCumulativeFunding, cf.CumulativeLong)
	}

	bobBackData, _, _ := sv.Get(keys.KeyFuturePosition("bob_pos"))
	var bobBack pb.FuturePositionState
	_ = proto.Unmarshal(bobBackData, &bobBack)
	if bobBack.LastCumulativeFunding != cf.CumulativeShort {
		t.Fatalf("bob funding should be swept at settlement: lastCum=%s cum=%s", bobBack.LastCumulativeFunding, cf.CumulativeShort)
	}
	bobBal := GetBalance(sv, "bob", "USDT")
	if bobBal == nil || bobBal.Balance == "" || bobBal.Balance == "0" {
		t.Fatalf("bob should receive funding during settlement sweep, balance=%+v", bobBal)
	}
}

func TestSettleFundingRateIfDue_RollsBackWholeRoundOnSweepError(t *testing.T) {
	base := newMemStateView()
	const pair = "BTC_USDT"
	height := future.DefaultFundingInterval
	spotPrice := big.NewInt(50_000 * 1e8)
	futurePrice := big.NewInt(50_500 * 1e8)
	base.Set(keys.KeyFutureSpotEMA(pair), spotPrice.Bytes())
	base.Set(keys.KeyFutureEMA(pair), futurePrice.Bytes())
	base.Set(keys.KeyFutureMarkPrice(pair), spotPrice.Bytes())

	view := &fundingScanErrorView{
		StateView:  base,
		failPrefix: keys.KeyFuturePairPositionPrefix(pair),
	}
	settled, err := settleFundingRateIfDue(view, pair, height)
	if err == nil {
		t.Fatal("failed funding sweep must propagate its error")
	}
	if settled {
		t.Fatal("failed funding sweep must not be reported as settled")
	}

	for _, key := range []string{
		keys.KeyFutureCumulativeFunding(pair),
		keys.KeyFutureSettleMarkPrice(pair),
		keys.KeyFutureFundingHeight(pair),
		keys.KeyFutureFundingRate(pair),
	} {
		if data, exists, err := base.Get(key); err != nil || exists || len(data) != 0 {
			t.Fatalf("funding rollback left key %q: exists=%v data=%x err=%v", key, exists, data, err)
		}
	}

	base.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
	scheduleFutureFundingDue(base, pair, height)
	if _, err := processFutureBlockHooksWithStats(view, height, nil, nil); err == nil {
		t.Fatal("future block hook must propagate the funding sweep error")
	}
	if _, exists, err := base.Get(keys.KeyFutureFundingDue(height, pair)); err != nil || !exists {
		t.Fatalf("failed hook must restore current due entry: exists=%v err=%v", exists, err)
	}
	if _, exists, err := base.Get(keys.KeyFutureFundingDue(height+future.DefaultFundingInterval, pair)); err != nil || exists {
		t.Fatalf("failed hook must not silently defer funding by one interval: exists=%v err=%v", exists, err)
	}
}

func TestSettleFundingRateIfDue_MissingPricesIsNotAStorageFailure(t *testing.T) {
	settled, err := settleFundingRateIfDue(newMemStateView(), "BTC_USDT", future.DefaultFundingInterval)
	if err != nil {
		t.Fatalf("missing cold-start prices should be retryable, got %v", err)
	}
	if settled {
		t.Fatal("missing prices must not report a completed funding round")
	}
}

func TestSettleFundingRateIfDue_PropagatesCumulativeReadError(t *testing.T) {
	const pair = "BTC_USDT"
	base := newMemStateView()
	spotPrice := big.NewInt(50_000 * 1e8)
	base.Set(keys.KeyFutureSpotEMA(pair), spotPrice.Bytes())
	base.Set(keys.KeyFutureEMA(pair), big.NewInt(50_500*1e8).Bytes())
	base.Set(keys.KeyFutureMarkPrice(pair), spotPrice.Bytes())
	view := &fundingReadErrorView{
		StateView: base,
		failKey:   keys.KeyFutureCumulativeFunding(pair),
	}

	settled, err := settleFundingRateIfDue(view, pair, future.DefaultFundingInterval)
	if err == nil {
		t.Fatal("expected cumulative funding read error")
	}
	if settled {
		t.Fatal("read failure must not report a completed funding round")
	}
	for _, key := range []string{
		keys.KeyFutureSettleMarkPrice(pair),
		keys.KeyFutureFundingHeight(pair),
		keys.KeyFutureFundingRate(pair),
	} {
		if _, exists, getErr := base.Get(key); getErr != nil || exists {
			t.Fatalf("read failure left partial funding state %q: exists=%v err=%v", key, exists, getErr)
		}
	}
}

func TestProcessFutureBlockHooks_FailsClosedOnFundingDiscoveryErrors(t *testing.T) {
	height := future.DefaultFundingInterval
	t.Run("due index scan", func(t *testing.T) {
		base := newMemStateView()
		view := &liquidationScanErrorView{
			StateView:      base,
			failScanPrefix: keys.KeyFutureFundingDuePrefix(height),
		}
		if _, err := processFutureBlockHooksWithStats(view, height, nil, nil); err == nil {
			t.Fatal("expected funding due-index scan error")
		}
	})

	t.Run("fallback market scan", func(t *testing.T) {
		base := newMemStateView()
		view := &liquidationScanErrorView{
			StateView:      base,
			failScanPrefix: keys.KeyFutureMarketConfigPrefix(),
		}
		if _, err := processFutureBlockHooksWithStats(view, height, nil, nil); err == nil {
			t.Fatal("expected funding fallback market scan error")
		}
	})

	t.Run("market read after due removal", func(t *testing.T) {
		const pair = "BTC_USDT"
		base := newMemStateView()
		scheduleFutureFundingDue(base, pair, height)
		view := &fundingReadErrorView{
			StateView: base,
			failKey:   keys.KeyFutureMarketConfig(pair),
		}
		if _, err := processFutureBlockHooksWithStats(view, height, nil, nil); err == nil {
			t.Fatal("expected future market read error")
		}
		if _, exists, err := base.Get(keys.KeyFutureFundingDue(height, pair)); err != nil || !exists {
			t.Fatalf("market read failure must restore due entry: exists=%v err=%v", exists, err)
		}
	})
}

func TestProcessFutureBlockHooks_FailsClosedOnExpiryDiscoveryErrors(t *testing.T) {
	height := uint64(1234)
	t.Run("expiry index scan", func(t *testing.T) {
		base := newMemStateView()
		view := &fundingScanErrorView{
			StateView:  base,
			failPrefix: keys.KeyFutureOrderExpiryPrefix(height),
		}
		if _, err := processFutureBlockHooksWithStats(view, height, nil, nil); err == nil {
			t.Fatal("expected future order expiry-index scan error")
		}
	})

	t.Run("expiring order read", func(t *testing.T) {
		const orderID = "expiry-read-failure"
		base := newMemStateView()
		state := &pb.FutureOrderState{
			OrderId: orderID, Owner: "alice", OwnerAddress: "alice", Pair: "BTC_USDT",
			Status: pb.FutureOrderStatus_FUTURE_ORDER_OPEN, ExpiryHeight: height,
		}
		data, err := proto.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		stateKey := keys.KeyFutureOrderState(orderID)
		expiryKey := keys.KeyFutureOrderExpiry(height, orderID)
		base.Set(stateKey, data)
		base.Set(expiryKey, []byte{1})
		view := &fundingReadErrorView{StateView: base, failKey: stateKey}

		if _, err := processFutureBlockHooksWithStats(view, height, nil, nil); err == nil {
			t.Fatal("expected expiring order state read error")
		}
		if _, exists, err := base.Get(expiryKey); err != nil || !exists {
			t.Fatalf("failed expiry read must preserve due index: exists=%v err=%v", exists, err)
		}
		if got, exists, err := base.Get(stateKey); err != nil || !exists || string(got) != string(data) {
			t.Fatalf("failed expiry read changed order state: exists=%v err=%v", exists, err)
		}
	})
}

func TestProcessFutureBlockHooks_MarkReadErrorRestoresFundingDue(t *testing.T) {
	const pair = "BTC_USDT"
	height := future.DefaultFundingInterval
	base := newMemStateView()
	base.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
	base.Set(keys.KeySpotLastPrice(pair), big.NewInt(100).Bytes())
	scheduleFutureFundingDue(base, pair, height)
	view := &fundingReadErrorView{StateView: base, failKey: keys.KeyFutureSpotEMA(pair)}

	if _, err := processFutureBlockHooksWithStats(view, height, map[string]bool{pair: true}, nil); err == nil {
		t.Fatal("expected mark-price EMA read error")
	}
	if _, exists, err := base.Get(keys.KeyFutureFundingDue(height, pair)); err != nil || !exists {
		t.Fatalf("mark read failure must restore current funding due: exists=%v err=%v", exists, err)
	}
	if _, exists, err := base.Get(keys.KeyFutureMarkPrice(pair)); err != nil || exists {
		t.Fatalf("mark read failure left a fabricated mark: exists=%v err=%v", exists, err)
	}
}

func TestPreviewFutureLiquidationCandidates_PropagatesFundingReadError(t *testing.T) {
	const pair = "BTC_USDT"
	base := newMemStateView()
	base.Set(keys.KeyFutureSettleMarkPrice(pair), big.NewInt(100).Bytes())
	view := &fundingReadErrorView{StateView: base, failKey: keys.KeyFutureCumulativeFunding(pair)}
	eng := newFutureEngine(view, 1)
	positions := []*future.Position{{
		ID: "funding-preview-read-failure", Owner: "alice", Pair: pair,
		Side: future.Long, EntryPrice: big.NewInt(100), Quantity: big.NewInt(1), Margin: big.NewInt(1), Status: future.StatusOpen,
	}}

	if _, err := previewFutureLiquidationCandidates(eng, &future.MarketConfig{Pair: pair, MaintenanceMarginRateBps: 50}, positions, big.NewInt(100)); err == nil {
		t.Fatal("liquidation preview silently treated funding read failure as zero pending funding")
	}
}

func TestCheckFutureLiquidations_BackfillsEmptyLiqIndex(t *testing.T) {
	sv := newMemStateView()
	pair := "BTC_USDT"
	const unit int64 = 1e8
	sv.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
	markPrice := big.NewInt(49_000 * unit)
	sv.Set(keys.KeyFutureMarkPrice(pair), markPrice.Bytes())
	qty := big.NewInt(unit)
	margin := future.CalcRequiredMargin(qty, big.NewInt(50_000*unit), 100)
	pos := &pb.FuturePositionState{
		PositionId: "alice_pos", Owner: "alice", Pair: pair,
		Side:                  int32(future.Long),
		EntryPrice:            big.NewInt(50_000 * unit).String(),
		Quantity:              qty.String(),
		Margin:                margin.String(),
		Status:                int32(future.StatusOpen),
		OpenHeight:            1,
		LastCumulativeFunding: "0",
	}
	data, _ := proto.Marshal(pos)
	sv.Set(keys.KeyFuturePosition(pos.PositionId), data)
	sv.Set(keys.KeyFuturePairPosition(pair, pos.PositionId), data)

	checkFutureLiquidations(sv, pair, 2)

	if _, exists, _ := sv.Get(keys.KeyFuturePosition(pos.PositionId)); exists {
		t.Fatalf("liquidated position should leave StateDB main key")
	}
	backData, _, _ := sv.Get(keys.KeyFuturePositionHistory(pos.PositionId))
	var back pb.FuturePositionState
	if err := proto.Unmarshal(backData, &back); err != nil {
		t.Fatal(err)
	}
	if back.Status != int32(future.StatusLiquidated) {
		t.Fatalf("position with missing liq index should be liquidated after backfill, status=%d", back.Status)
	}
	if _, exists, _ := sv.Get(keys.KeyFutureLiqIndexBackfillDone(pair)); !exists {
		t.Fatal("successful liquidation-index rebuild must persist the pair backfill marker")
	}
	assertMarkLiquidationClosingSide(t, sv, pair, pos.PositionId, pos.Owner, 2, pb.OrderSide_SELL)
}

func TestCheckFutureLiquidations_InitialBackfillRepairsUnobservablePartialIndex(t *testing.T) {
	const unit int64 = 1e8
	const pair = "BTC_USDT"
	sv := newMemStateView()
	entryPrice := big.NewInt(50_000 * unit)
	markPrice := big.NewInt(49_000 * unit)
	qty := big.NewInt(unit)
	margin := future.CalcRequiredMargin(qty, entryPrice, 100)
	sv.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
	sv.Set(keys.KeyFutureMarkPrice(pair), markPrice.Bytes())

	for _, id := range []string{"indexed_danger", "missing_danger"} {
		putFutureHookPosition(t, sv, &pb.FuturePositionState{
			PositionId: id, Owner: id, Pair: pair,
			Side: int32(future.Long), EntryPrice: entryPrice.String(),
			Quantity: qty.String(), Margin: margin.String(),
			Status: int32(future.StatusOpen), OpenHeight: 1,
		})
	}
	                                                                           
	                                                                       
	                                               
	if err := (&svLiquidationIndex{sv: sv}).Upsert("indexed_danger", pair, future.Long, markPrice); err != nil {
		t.Fatal(err)
	}

	checkFutureLiquidations(sv, pair, 2)

	if _, exists, _ := sv.Get(keys.KeyFuturePosition("missing_danger")); exists {
		t.Fatal("initial pair backfill failed to liquidate a completely unindexed dangerous position")
	}
	if _, exists, _ := sv.Get(keys.KeyFutureLiqIndexBackfillDone(pair)); !exists {
		t.Fatal("initial pair backfill marker missing")
	}
}

func TestCheckFutureLiquidations_FailsClosedOnEitherIndexScanError(t *testing.T) {
	const (
		pair = "BTC_USDT"
		unit = int64(1e8)
	)
	markPrice := big.NewInt(50_000 * unit)
	entryPrice := new(big.Int).Set(markPrice)
	qty := big.NewInt(unit)

	tests := []struct {
		name string
		fail func(*liquidationScanErrorView)
	}{
		{
			name: "long reverse scan",
			fail: func(v *liquidationScanErrorView) {
				v.failReversePrefix = keys.KeyFutureLiqIndexPrefix("long", pair)
			},
		},
		{
			name: "short forward scan",
			fail: func(v *liquidationScanErrorView) {
				v.failForwardPrefix = keys.KeyFutureLiqIndexPrefix("short", pair)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base := newMemStateView()
			base.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
			base.Set(keys.KeyFutureMarkPrice(pair), markPrice.Bytes())
			base.Set(keys.KeyFutureLiqIndexBackfillDone(pair), []byte{1})
			idx := &svLiquidationIndex{sv: base}
			for _, seed := range []struct {
				id   string
				side future.Side
			}{
				{id: "danger-long", side: future.Long},
				{id: "danger-short", side: future.Short},
			} {
				putFutureHookPosition(t, base, &pb.FuturePositionState{
					PositionId: seed.id, Owner: seed.id, Pair: pair,
					Side: int32(seed.side), EntryPrice: entryPrice.String(),
					Quantity: qty.String(), Margin: "1", Status: int32(future.StatusOpen),
				})
				if err := idx.Upsert(seed.id, pair, seed.side, markPrice); err != nil {
					t.Fatal(err)
				}
			}

			view := &liquidationScanErrorView{StateView: base}
			tc.fail(view)
			if err := checkFutureLiquidations(view, pair, 2); err == nil {
				t.Fatal("expected liquidation index scan error")
			}
			for _, id := range []string{"danger-long", "danger-short"} {
				if _, exists, err := base.Get(keys.KeyFuturePosition(id)); err != nil || !exists {
					t.Fatalf("scan error removed live position %s: exists=%v err=%v", id, exists, err)
				}
				if _, exists, err := base.Get(keys.KeyFuturePositionHistory(id)); err != nil || exists {
					t.Fatalf("scan error wrote partial history for %s: exists=%v err=%v", id, exists, err)
				}
			}
		})
	}
}

func TestCheckFutureLiquidations_FullScanFailureRollsBackIndexRepair(t *testing.T) {
	const pair = "BTC_USDT"
	markPrice := big.NewInt(50_000 * 1e8)
	base := newMemStateView()
	base.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
	base.Set(keys.KeyFutureMarkPrice(pair), markPrice.Bytes())
	base.Set(keys.KeyFutureLiqIndexBackfillDone(pair), []byte{1})
	idx := &svLiquidationIndex{sv: base}
	if err := idx.Upsert("ghost-position", pair, future.Long, markPrice); err != nil {
		t.Fatal(err)
	}
	refKey := keys.KeyFutureLiqPosRef("ghost-position")
	indexKeyBytes, exists, err := base.Get(refKey)
	if err != nil || !exists {
		t.Fatalf("seed ghost reverse reference: exists=%v err=%v", exists, err)
	}
	indexKey := string(indexKeyBytes)

	view := &liquidationScanErrorView{
		StateView:      base,
		failScanPrefix: keys.KeyFuturePairPositionPrefix(pair),
	}
	if err := checkFutureLiquidations(view, pair, 2); err == nil {
		t.Fatal("expected authoritative position scan error")
	}
	gotRef, exists, err := base.Get(refKey)
	if err != nil || !exists || string(gotRef) != indexKey {
		t.Fatalf("failed repair did not restore reverse ref: exists=%v ref=%q err=%v", exists, gotRef, err)
	}
	if _, exists, err := base.Get(indexKey); err != nil || !exists {
		t.Fatalf("failed repair did not restore ghost index: exists=%v err=%v", exists, err)
	}
}

func TestProcessFutureBlockHooks_PropagatesLiquidationScanError(t *testing.T) {
	const pair = "BTC_USDT"
	base := newMemStateView()
	base.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
	base.Set(keys.KeyFutureMarkPrice(pair), big.NewInt(50_000*1e8).Bytes())
	base.Set(keys.KeyFutureLiqIndexBackfillDone(pair), []byte{1})
	view := &liquidationScanErrorView{
		StateView:         base,
		failReversePrefix: keys.KeyFutureLiqIndexPrefix("long", pair),
	}

	if _, err := processFutureBlockHooksWithStats(view, 2, nil, map[string]bool{pair: true}); err == nil {
		t.Fatal("expected future block hook to propagate liquidation scan failure")
	}
}

func TestCheckFutureLiquidations_PropagatesMarkPriceReadError(t *testing.T) {
	const pair = "BTC_USDT"
	base := newMemStateView()
	base.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
	base.Set(keys.KeyFutureMarkPrice(pair), big.NewInt(50_000*1e8).Bytes())
	view := &liquidationScanErrorView{
		StateView:  base,
		failGetKey: keys.KeyFutureMarkPrice(pair),
	}

	if err := checkFutureLiquidations(view, pair, 2); err == nil {
		t.Fatal("expected mark-price read failure")
	}
}

func TestCheckFutureLiquidations_LiquidationWriteErrorRollsBackWholeCheck(t *testing.T) {
	const pair = "BTC_USDT"
	const positionID = "danger-long-write-failure"
	scale := new(big.Int).Set(future.PriceScale)
	entryPrice := new(big.Int).Mul(big.NewInt(100), scale)
	markPrice := new(big.Int).Mul(big.NewInt(98), scale)
	qty := new(big.Int).Set(scale)
	margin := new(big.Int).Set(scale)

	base := newMemStateView()
	base.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
	base.Set(keys.KeyFutureMarkPrice(pair), markPrice.Bytes())
	base.Set(keys.KeyFutureLiqIndexBackfillDone(pair), []byte{1})
	putFutureHookPosition(t, base, &pb.FuturePositionState{
		PositionId: positionID, Owner: "alice", Pair: pair,
		Side: int32(future.Long), EntryPrice: entryPrice.String(),
		Quantity: qty.String(), Margin: margin.String(),
		Status: int32(future.StatusOpen), OpenHeight: 1,
	})
	if err := (&svLiquidationIndex{sv: base}).Upsert(positionID, pair, future.Long, markPrice); err != nil {
		t.Fatal(err)
	}
	view := &liquidationScanErrorView{
		StateView: base,
		failGetKey: keys.KeyAssetClaim(
			"alice", "USDT", rentClaimFuturePositionMargin, positionID,
		),
	}

	if err := checkFutureLiquidations(view, pair, 2); err == nil {
		t.Fatal("expected liquidation position-write failure")
	}
	if _, exists, err := base.Get(keys.KeyFuturePosition(positionID)); err != nil || !exists {
		t.Fatalf("failed liquidation did not restore live position: exists=%v err=%v", exists, err)
	}
	if _, exists, err := base.Get(keys.KeyFuturePositionHistory(positionID)); err != nil || exists {
		t.Fatalf("failed liquidation retained terminal history: exists=%v err=%v", exists, err)
	}
	if data, exists, err := base.Get(keys.KeyFutureInsuranceFund(pair)); err != nil {
		t.Fatal(err)
	} else if exists && new(big.Int).SetBytes(data).Sign() != 0 {
		t.Fatalf("failed liquidation retained insurance deposit: %s", new(big.Int).SetBytes(data))
	}
}

func TestCheckFutureLiquidations_RepairsObservablyIncompleteLiqIndex(t *testing.T) {
	const unit int64 = 1e8
	pair := "BTC_USDT"
	entryPrice := big.NewInt(50_000 * unit)
	markPrice := big.NewInt(49_000 * unit)
	qty := big.NewInt(unit)

	tests := []struct {
		name      string
		seedIndex func(t *testing.T, sv StateView)
	}{
		{
			name: "ghost indexed candidate",
			seedIndex: func(t *testing.T, sv StateView) {
				t.Helper()
				if err := (&svLiquidationIndex{sv: sv}).Upsert("ghost_pos", pair, future.Long, markPrice); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "stale risk entry resolves to healthy position",
			seedIndex: func(t *testing.T, sv StateView) {
				t.Helper()
				healthy := &pb.FuturePositionState{
					PositionId: "healthy_pos", Owner: "bob", Pair: pair,
					Side:                  int32(future.Long),
					EntryPrice:            entryPrice.String(),
					Quantity:              qty.String(),
					Margin:                future.CalcRequiredMargin(qty, entryPrice, 2).String(),
					Status:                int32(future.StatusOpen),
					OpenHeight:            1,
					LastCumulativeFunding: "0",
				}
				putFutureHookPosition(t, sv, healthy)
				                                                                     
				                                                                     
				                                      
				if err := (&svLiquidationIndex{sv: sv}).Upsert(healthy.PositionId, pair, future.Long, markPrice); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sv := newMemStateView()
			sv.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
			sv.Set(keys.KeyFutureMarkPrice(pair), markPrice.Bytes())
			                                                                      
			                            
			sv.Set(keys.KeyFutureLiqIndexBackfillDone(pair), []byte{1})
			danger := &pb.FuturePositionState{
				PositionId: "danger_pos", Owner: "alice", Pair: pair,
				Side:                  int32(future.Long),
				EntryPrice:            entryPrice.String(),
				Quantity:              qty.String(),
				Margin:                future.CalcRequiredMargin(qty, entryPrice, 100).String(),
				Status:                int32(future.StatusOpen),
				OpenHeight:            1,
				LastCumulativeFunding: "0",
			}
			putFutureHookPosition(t, sv, danger)
			tc.seedIndex(t, sv)

			checkFutureLiquidations(sv, pair, 2)

			if _, exists, _ := sv.Get(keys.KeyFuturePosition(danger.PositionId)); exists {
				t.Fatalf("dangerous position omitted from non-empty index was not liquidated")
			}
			data, exists, err := sv.Get(keys.KeyFuturePositionHistory(danger.PositionId))
			if err != nil || !exists {
				t.Fatalf("liquidated position history missing: exists=%v err=%v", exists, err)
			}
			var history pb.FuturePositionState
			if err := proto.Unmarshal(data, &history); err != nil {
				t.Fatal(err)
			}
			if history.Status != int32(future.StatusLiquidated) {
				t.Fatalf("dangerous position status=%d, want liquidated", history.Status)
			}
			if tc.name == "stale risk entry resolves to healthy position" {
				if _, exists, _ := sv.Get(keys.KeyFuturePosition("healthy_pos")); !exists {
					t.Fatalf("healthy indexed position must remain open")
				}
			} else if _, exists, _ := sv.Get(keys.KeyFutureLiqPosRef("ghost_pos")); exists {
				t.Fatalf("ghost liquidation index reverse reference should be removed during repair")
			}
		})
	}
}

func TestCheckFutureLiquidations_ShortMarkFallbackRecordsBuyClose(t *testing.T) {
	sv := newMemStateView()
	pair := "BTC_USDT"
	const unit int64 = 1e8
	entryPrice := big.NewInt(50_000 * unit)
	markPrice := big.NewInt(51_000 * unit)
	qty := big.NewInt(unit)
	sv.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
	sv.Set(keys.KeyFutureMarkPrice(pair), markPrice.Bytes())
	pos := &pb.FuturePositionState{
		PositionId: "short_pos", Owner: "alice", Pair: pair,
		Side:                  int32(future.Short),
		EntryPrice:            entryPrice.String(),
		Quantity:              qty.String(),
		Margin:                future.CalcRequiredMargin(qty, entryPrice, 100).String(),
		Status:                int32(future.StatusOpen),
		OpenHeight:            1,
		LastCumulativeFunding: "0",
	}
	putFutureHookPosition(t, sv, pos)

	checkFutureLiquidations(sv, pair, 2)

	if _, exists, _ := sv.Get(keys.KeyFuturePosition(pos.PositionId)); exists {
		t.Fatalf("short position should be liquidated by mark fallback")
	}
	assertMarkLiquidationClosingSide(t, sv, pair, pos.PositionId, pos.Owner, 2, pb.OrderSide_BUY)
}

func TestCheckFutureLiquidations_PairPrefixCollisionCannotFillScanCap(t *testing.T) {
	const (
		pair        = "A_B"
		foreignPair = "A_B_C"
		targetID    = "target-danger"
	)
	unit := new(big.Int).Set(future.PriceScale)
	entryPrice := new(big.Int).Mul(big.NewInt(50_000), unit)
	markPrice := new(big.Int).Mul(big.NewInt(49_000), unit)
	qty := new(big.Int).Set(unit)
	margin := future.CalcRequiredMargin(qty, entryPrice, 100)

	base := make(map[string][]byte, 4*(FutureLiqScanLimit+FutureLiqScanBuffer)+16)
	putBasePosition := func(pos *pb.FuturePositionState) {
		t.Helper()
		data, err := proto.Marshal(pos)
		if err != nil {
			t.Fatal(err)
		}
		base[keys.KeyFuturePosition(pos.PositionId)] = data
		base[keys.KeyFuturePairPosition(pos.Pair, pos.PositionId)] = data
	}
	putBaseLiqIndex := func(pair, positionID string, price *big.Int) string {
		t.Helper()
		priceKey, err := dbpkg.PriceToKey128(price.String())
		if err != nil {
			t.Fatal(err)
		}
		indexKey := keys.KeyFutureLiqIndex("long", pair, priceKey, positionID)
		base[indexKey] = []byte(positionID)
		base[keys.KeyFutureLiqPosRef(positionID)] = []byte(indexKey)
		return indexKey
	}

	base[keys.KeyFutureMarketConfig(pair)] = []byte(pair)
	base[keys.KeyFutureMarkPrice(pair)] = markPrice.Bytes()
	base[keys.KeyFutureLiqIndexBackfillDone(pair)] = []byte{1}
	putBasePosition(&pb.FuturePositionState{
		PositionId: targetID, Owner: "target-owner", Pair: pair,
		Side: int32(future.Long), EntryPrice: entryPrice.String(),
		Quantity: qty.String(), Margin: margin.String(), Status: int32(future.StatusOpen),
	})
	putBaseLiqIndex(pair, targetID, markPrice)

	                                                                         
	                                                                         
	                                                                              
	foreignIndexByID := make(map[string]string, FutureLiqScanLimit+FutureLiqScanBuffer)
	for i := 0; i < FutureLiqScanLimit+FutureLiqScanBuffer; i++ {
		id := fmt.Sprintf("foreign-%05d", i)
		status := future.StatusOpen
		if i == 0 {
			                                                                      
			                                                                     
			status = future.StatusClosed
		}
		putBasePosition(&pb.FuturePositionState{
			PositionId: id, Owner: id, Pair: foreignPair,
			Side: int32(future.Long), EntryPrice: unit.String(),
			Quantity: unit.String(), Margin: unit.String(), Status: int32(status),
		})
		foreignIndexByID[id] = putBaseLiqIndex(foreignPair, id, big.NewInt(1))
	}

	read := func(key string) ([]byte, error) {
		return append([]byte(nil), base[key]...), nil
	}
	scan := func(prefix string) (map[string][]byte, error) {
		out := make(map[string][]byte)
		for key, value := range base {
			if strings.HasPrefix(key, prefix) {
				out[key] = append([]byte(nil), value...)
			}
		}
		return out, nil
	}
	scanOrdered := func(prefix string, limit int, reverse bool) ([]KVPair, error) {
		keysFound := make([]string, 0)
		for key := range base {
			if strings.HasPrefix(key, prefix) {
				keysFound = append(keysFound, key)
			}
		}
		if reverse {
			sort.Sort(sort.Reverse(sort.StringSlice(keysFound)))
		} else {
			sort.Strings(keysFound)
		}
		if limit > 0 && len(keysFound) > limit {
			keysFound = keysFound[:limit]
		}
		out := make([]KVPair, 0, len(keysFound))
		for _, key := range keysFound {
			out = append(out, KVPair{Key: key, Value: append([]byte(nil), base[key]...)})
		}
		return out, nil
	}
	sv := NewStateViewWithLimitScanOrdered(read, scan, nil, nil, scanOrdered, nil)

	rawIDs, err := (&svLiquidationIndex{sv: sv}).FindLongsAtRisk(pair, markPrice)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range rawIDs {
		if id == targetID {
			t.Fatal("test setup: target unexpectedly survived foreign scan-cap pollution")
		}
	}

	checkFutureLiquidations(sv, pair, 2)
	if _, exists, _ := sv.Get(keys.KeyFuturePosition(targetID)); exists {
		t.Fatal("dangerous exact-pair position was squeezed out by foreign prefix entries")
	}
	if _, exists, _ := sv.Get(keys.KeyFuturePositionHistory(targetID)); !exists {
		t.Fatal("dangerous exact-pair position was not liquidated")
	}
	for _, id := range []string{"foreign-00000", fmt.Sprintf("foreign-%05d", FutureLiqScanLimit+FutureLiqScanBuffer-1)} {
		if _, exists, _ := sv.Get(keys.KeyFuturePosition(id)); !exists {
			t.Fatalf("foreign position %s was mutated by %s liquidation", id, pair)
		}
		ref, exists, err := sv.Get(keys.KeyFutureLiqPosRef(id))
		if err != nil || !exists || string(ref) != foreignIndexByID[id] {
			t.Fatalf("foreign reverse ref %s changed: exists=%v ref=%q err=%v", id, exists, ref, err)
		}
		if _, exists, _ := sv.Get(foreignIndexByID[id]); !exists {
			t.Fatalf("foreign liquidation index %s was deleted", id)
		}
	}
}

func putFutureHookPosition(t *testing.T, sv StateView, pos *pb.FuturePositionState) {
	t.Helper()
	data, err := proto.Marshal(pos)
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyFuturePosition(pos.PositionId), data)
	sv.Set(keys.KeyFuturePairPosition(pos.Pair, pos.PositionId), data)
}

func assertMarkLiquidationClosingSide(t *testing.T, sv StateView, pair, positionID, owner string, height uint64, want pb.OrderSide) {
	t.Helper()
	tradeID := "liq_" + positionID
	tradeTsKey, _ := deterministicTradeTime(height, 0)
	tradeData, exists, err := sv.Get(keys.KeyFutureTradeRecord(pair, tradeTsKey, tradeID))
	if err != nil || !exists {
		t.Fatalf("liquidation trade missing: exists=%v err=%v", exists, err)
	}
	var trade pb.TradeRecord
	if err := proto.Unmarshal(tradeData, &trade); err != nil {
		t.Fatal(err)
	}
	if trade.TakerSide != want {
		t.Fatalf("liquidation trade taker side=%s, want closing side=%s", trade.TakerSide, want)
	}

	orderKey := keys.KeyFutureAccountOrder(owner, pair, pb.FutureOrderStatus_FUTURE_ORDER_LIQUIDATED, height, tradeID)
	orderData, exists, err := sv.Get(orderKey)
	if err != nil || !exists {
		t.Fatalf("liquidation order history missing: exists=%v err=%v", exists, err)
	}
	var order pb.FutureOrderState
	if err := proto.Unmarshal(orderData, &order); err != nil {
		t.Fatal(err)
	}
	if order.Side != int32(want) {
		t.Fatalf("liquidation order side=%d, want closing side=%d", order.Side, want)
	}
}
