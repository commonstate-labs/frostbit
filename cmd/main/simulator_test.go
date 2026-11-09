package main

import (
	"bytes"
	"dex/config"
	"dex/consensus"
	"dex/db"
	bridgeasset "dex/frost/asset"
	"dex/future"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"dex/txpool"
	"dex/utils"
	"dex/vm"
	"encoding/hex"
	"math/big"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
)

type testStateWriteOp struct {
	key   string
	value []byte
}

func (op testStateWriteOp) GetKey() string   { return op.key }
func (op testStateWriteOp) GetValue() []byte { return op.value }
func (op testStateWriteOp) IsDel() bool      { return false }

type testStateDeleteOp struct{ key string }

func (op testStateDeleteOp) GetKey() string   { return op.key }
func (op testStateDeleteOp) GetValue() []byte { return nil }
func (op testStateDeleteOp) IsDel() bool      { return true }

type simulatorNoopValidator struct{}

func (simulatorNoopValidator) CheckAnyTx(*pb.AnyTx) error { return nil }

func mustDecodeHex(t *testing.T, raw string) []byte {
	t.Helper()
	out, err := hex.DecodeString(raw)
	if err != nil {
		t.Fatalf("decode hex failed: %v", err)
	}
	return out
}

func TestBuildTweakedTaprootScriptPubKey(t *testing.T) {
	groupPubKey := mustDecodeHex(t, "03d194cd76d1ba9f139cca4af1277993c90e20ac5c817dcfe93a92810c5f9b03cb")
	receiverAddr := "fbc1q8ra2f338djg0pzgms35tpkdxjx9lr39mkyw84y"

	cfg := testDualCustodyFrostConfig()
	guardian, _ := cfg.GuardianPublicKey("btc")
	got, err := buildTweakedTaprootScriptPubKey(groupPubKey, receiverAddr, guardian)
	if err != nil {
		t.Fatalf("buildTweakedTaprootScriptPubKey failed: %v", err)
	}

	rawXOnly := groupPubKey[1:]
	tweak := utils.ComputeUserTweak(rawXOnly, receiverAddr)
	tweakedPub, err := utils.ComputeTweakedPubkey(groupPubKey, tweak)
	if err != nil {
		t.Fatalf("ComputeTweakedPubkey failed: %v", err)
	}
	want, _, _, err := utils.BuildDualCustodyTaproot(tweakedPub, guardian)
	if err != nil {
		t.Fatalf("BuildTaprootScriptPubKeyFromXOnly failed: %v", err)
	}

	if !bytes.Equal(got, want) {
		t.Fatalf("scriptPubKey mismatch: got=%x want=%x", got, want)
	}
	if bytes.Equal(got[2:], rawXOnly) {
		t.Fatalf("expected tweaked scriptPubKey to differ from raw x-only pubkey")
	}
}

func TestBuildSimRechargeAssetsFromLocalMetadata(t *testing.T) {
	frostCfg := config.FrostConfig{Chains: map[string]config.ChainFrostConfig{
		"btc": {}, "eth": {}, "bnb": {}, "base": {}, "arb": {}, "trx": {},
	}}

	assets := buildSimRechargeAssets(frostCfg, config.DefaultGenesisConfig())
	got := make(map[string]simRechargeAsset, len(assets))
	for _, asset := range assets {
		got[asset.Token] = asset
		if asset.Token == "FB" {
			t.Fatal("system token FB should not be used as simulated recharge asset")
		}
		if asset.WithdrawAmount != simDefaultWithdrawRawAmount {
			t.Fatalf("asset %s withdraw amount = %q, want %q", asset.Token, asset.WithdrawAmount, simDefaultWithdrawRawAmount)
		}
	}

	resolve := func(chainName, contract string) bridgeasset.Identity {
		t.Helper()
		identity, err := bridgeasset.Resolve(chainName, contract)
		if err != nil {
			t.Fatal(err)
		}
		return identity
	}
	want := []bridgeasset.Identity{
		resolve("btc", ""),
		resolve("eth", ""),
		resolve("trx", ""),
		resolve("eth", "0xdac17f958d2ee523a2206206994597c13d831ec7"),
		resolve("eth", "0x1f9840a85d5af5bf1d1762f925bdaddc4201f984"),
		resolve("bnb", "0x55d398326f99059ff775485246999027b3197955"),
		resolve("base", "0x833589fcd6edb6e08f4c7c32d4f71b54bda02913"),
		resolve("arb", "0xaf88d065e77c8cc2239327c5edb3a432268e5831"),
		resolve("trx", "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"),
	}
	for _, identity := range want {
		if got[identity.ID].Chain != identity.Chain || got[identity.ID].NativeContract != identity.Contract {
			t.Fatalf("asset %s identity mismatch: got=%+v want=%+v", identity.ID, got[identity.ID], identity)
		}
	}

	wantAmounts := map[string]string{
		resolve("btc", "").ID: "700000000000",
		resolve("eth", "").ID: "7000000000000000000000",
		resolve("eth", "0xdac17f958d2ee523a2206206994597c13d831ec7").ID: "7000000000",
		resolve("bnb", "0x55d398326f99059ff775485246999027b3197955").ID: "7000000000000000000000",
	}
	for token, amount := range wantAmounts {
		if got[token].Amount != amount {
			t.Fatalf("asset %s recharge amount = %q, want %q", token, got[token].Amount, amount)
		}
	}
}

func TestBuildSimWithdrawAssetsSkipsUnsupportedTRX(t *testing.T) {
	frostCfg := config.FrostConfig{Chains: map[string]config.ChainFrostConfig{
		"btc": {}, "eth": {}, "bnb": {}, "base": {}, "arb": {}, "trx": {},
	}}

	assets := buildSimWithdrawAssets(buildSimRechargeAssets(frostCfg, config.DefaultGenesisConfig()))
	for _, asset := range assets {
		if asset.Chain == "trx" {
			t.Fatal("trx withdraw is not signed yet and should not be auto-simulated")
		}
	}
	if len(assets) == 0 {
		t.Fatal("expected simulated withdraw assets")
	}
}

func TestSimWithdrawAmountUsesIndependentConfiguredAmount(t *testing.T) {
	if got := simWithdrawAmountForAsset(simRechargeAsset{Amount: "700000000000", WithdrawAmount: "3500"}).String(); got != "3500" {
		t.Fatalf("configured withdraw amount = %s, want 3500", got)
	}
	if got := simWithdrawAmountForAsset(simRechargeAsset{Amount: "7000"}).String(); got != "3500" {
		t.Fatalf("legacy withdraw amount = %s, want 3500", got)
	}
	if got := simWithdrawAmountForAsset(simRechargeAsset{Amount: "1"}).String(); got != "1" {
		t.Fatalf("small withdraw amount = %s, want 1", got)
	}
	if got := simWithdrawAmountForAsset(simRechargeAsset{Amount: "bad"}).String(); got != "50" {
		t.Fatalf("fallback withdraw amount = %s, want 50", got)
	}
}

func TestSimulatorWithdrawBackpressureUsesGlobalOpenQueue(t *testing.T) {
	dbMgr, err := db.NewManager(filepath.Join(t.TempDir(), "db"), logs.NewNodeLogger("sim-withdraw-global-backpressure-test", 0))
	if err != nil {
		t.Fatalf("new db manager: %v", err)
	}
	t.Cleanup(dbMgr.Close)

	s := &TxSimulator{
		nodes: []*NodeInstance{{DBManager: dbMgr}},
		withdrawAssets: []simRechargeAsset{
			{Chain: "btc", Token: "BTC", Amount: "7000"},
			{Chain: "eth", Token: "ETH.USDT", Amount: "7000"},
		},
	}
	blocked, openCount, ok := s.shouldBackpressureSimWithdrawQueue()
	if !ok || blocked || openCount != 0 {
		t.Fatalf("empty queue should not backpressure: blocked=%v open=%d ok=%v", blocked, openCount, ok)
	}

	if _, err := dbMgr.SyncFinalizedStateUpdates(1, []interface{}{
		testStateWriteOp{key: keys.KeyFrostWithdrawFIFOSeq("btc", "BTC"), value: []byte("600")},
		testStateWriteOp{key: keys.KeyFrostWithdrawFIFOHead("btc", "BTC"), value: []byte("1")},
		testStateWriteOp{key: keys.KeyFrostWithdrawFIFOSeq("eth", "ETH.USDT"), value: []byte("399")},
		testStateWriteOp{key: keys.KeyFrostWithdrawFIFOHead("eth", "ETH.USDT"), value: []byte("1")},
	}, true); err != nil {
		t.Fatalf("sync below-limit FIFO counters: %v", err)
	}
	blocked, openCount, ok = s.shouldBackpressureSimWithdrawQueue()
	if !ok || blocked || openCount != simWithdrawQueueBackpressureLimit-1 {
		t.Fatalf("below-limit queue should not backpressure: blocked=%v open=%d ok=%v", blocked, openCount, ok)
	}

	if _, err := dbMgr.SyncFinalizedStateUpdates(2, []interface{}{
		testStateWriteOp{key: keys.KeyFrostWithdrawFIFOSeq("eth", "ETH.USDT"), value: []byte("400")},
	}, true); err != nil {
		t.Fatalf("sync at-limit FIFO counters: %v", err)
	}
	blocked, openCount, ok = s.shouldBackpressureSimWithdrawQueue()
	if !ok || !blocked || openCount != simWithdrawQueueBackpressureLimit {
		t.Fatalf("at-limit queue should backpressure: blocked=%v open=%d ok=%v", blocked, openCount, ok)
	}

	if _, err := dbMgr.SyncFinalizedStateUpdates(3, []interface{}{
		testStateWriteOp{key: keys.KeyFrostWithdrawFIFOHead("btc", "BTC"), value: []byte("2")},
	}, true); err != nil {
		t.Fatalf("sync advanced FIFO head: %v", err)
	}
	blocked, openCount, ok = s.shouldBackpressureSimWithdrawQueue()
	if !ok || blocked || openCount != simWithdrawQueueBackpressureLimit-1 {
		t.Fatalf("advanced FIFO head should relieve backpressure: blocked=%v open=%d ok=%v", blocked, openCount, ok)
	}
}

func TestBuildSimTradingPairsUsesLocalMetadataNotGenesisBalances(t *testing.T) {
	genesis := config.DefaultGenesisConfig()
	genesis.Alloc = nil

	pairs := buildSimTradingPairs(genesis)
	got := make(map[string]bool, len(pairs))
	for _, pair := range pairs {
		got[pair.pairKey()] = true
		if pair.MinPrice <= 0 || pair.MaxPrice < pair.MinPrice || pair.MinAmount <= 0 || pair.MaxAmount < pair.MinAmount {
			t.Fatalf("invalid simulated pair bounds: %+v", pair)
		}
	}

	btc, _ := bridgeasset.Resolve("btc", "")
	ethUSDT, _ := bridgeasset.Resolve("eth", "0xdac17f958d2ee523a2206206994597c13d831ec7")
	baseUSDC, _ := bridgeasset.Resolve("base", "0x833589fcd6edb6e08f4c7c32d4f71b54bda02913")
	for _, pair := range []string{"FB_" + ethUSDT.ID, "FB_" + baseUSDC.ID, btc.ID + "_" + ethUSDT.ID, btc.ID + "_" + baseUSDC.ID} {
		if !got[pair] {
			t.Fatalf("expected simulated pair %s", pair)
		}
	}
}

func TestBuildSimTradingPairsEncodesTokenDecimals(t *testing.T) {
	pairs := buildSimTradingPairs(config.DefaultGenesisConfig())
	byKey := make(map[string]simTradingPair, len(pairs))
	for _, pair := range pairs {
		byKey[pair.pairKey()] = pair
	}
	uni, _ := bridgeasset.Resolve("eth", "0x1f9840a85d5af5bf1d1762f925bdaddc4201f984")
	fbDai, ok := byKey["FB_"+uni.ID]
	if !ok {
		t.Fatal("expected FB/ETH.UNI simulated pair")
	}
	if fbDai.BaseDecimals != 8 || fbDai.QuoteDecimals != 18 {
		t.Fatalf("FB_ETH.DAI decimals = %d/%d, want 8/18", fbDai.BaseDecimals, fbDai.QuoteDecimals)
	}
	fbAmount := fbDai.seedAmount()
	fbPrice := fbDai.seedPrice()
	if got, want := fbAmount.String(), "550000000"; got != want {
		t.Fatalf("FB_ETH.DAI seed amount raw = %s, want %s", got, want)
	}
	if got, want := fbPrice.String(), "15000000000000000000"; got != want {
		t.Fatalf("FB_ETH.DAI seed price raw = %s, want %s", got, want)
	}
	if fbPrice.IsInt64() {
		t.Fatalf("FB_ETH.DAI price should exercise the value above int64: %s", fbPrice)
	}
	if got, want := future.CalcNotional(fbAmount, fbPrice).String(), "82500000000000000000"; got != want {
		t.Fatalf("FB_ETH.DAI quote raw = %s, want %s (82.5 DAI)", got, want)
	}

	trx, _ := bridgeasset.Resolve("trx", "")
	usdc, _ := bridgeasset.Resolve("eth", "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48")
	trxUsdc, ok := byKey[trx.ID+"_"+usdc.ID]
	if !ok {
		t.Fatal("expected TRX_ETH.USDC simulated pair")
	}
	if trxUsdc.BaseDecimals != 6 || trxUsdc.QuoteDecimals != 6 {
		t.Fatalf("TRX_ETH.USDC decimals = %d/%d, want 6/6", trxUsdc.BaseDecimals, trxUsdc.QuoteDecimals)
	}
	trxAmount := trxUsdc.seedAmount()
	trxPrice := trxUsdc.seedPrice()
	if got, want := trxAmount.String(), "5005000"; got != want {
		t.Fatalf("TRX_ETH.USDC seed amount raw = %s, want %s", got, want)
	}
	if got, want := trxPrice.String(), "5050000000"; got != want {
		t.Fatalf("TRX_ETH.USDC seed price raw = %s, want %s", got, want)
	}
	if got, want := future.CalcNotional(trxAmount, trxPrice).String(), "252752500"; got != want {
		t.Fatalf("TRX/ETH.USDC quote raw = %s, want %s", got, want)
	}
}

func TestBuildSimTradingPairsIncludesDynamicallyRegisteredStyleAsset(t *testing.T) {
	pairs := buildSimTradingPairs(config.DefaultGenesisConfig())
	byKey := make(map[string]simTradingPair, len(pairs))
	for _, pair := range pairs {
		byKey[pair.pairKey()] = pair
	}

	btc, _ := bridgeasset.Resolve("btc", "")
	uni, _ := bridgeasset.Resolve("eth", "0x1f9840a85d5af5bf1d1762f925bdaddc4201f984")
	if _, ok := byKey[btc.ID+"_"+uni.ID]; !ok {
		t.Fatal("expected canonical BTC/ETH.UNI simulator pair")
	}
}

func TestBuildSimTradingPairsCreatesCrossedSpotPrices(t *testing.T) {
	pairs := buildSimTradingPairs(config.DefaultGenesisConfig())
	if len(pairs) == 0 {
		t.Fatal("expected simulated spot pairs")
	}
	for _, pair := range pairs {
		sellPrice, buyPrice := pair.crossedPrices()
		if sellPrice.Sign() <= 0 || buyPrice.Cmp(sellPrice) <= 0 {
			t.Fatalf("pair %s crossed prices = %s/%s, want positive sell < buy", pair.pairKey(), sellPrice, buyPrice)
		}
		midPrice := pair.seedPrice()
		wantSell := new(big.Int).Mul(midPrice, big.NewInt(10_000-simSpotCrossHalfSpreadBPS))
		wantSell.Quo(wantSell, big.NewInt(10_000))
		wantBuy := new(big.Int).Mul(midPrice, big.NewInt(10_000+simSpotCrossHalfSpreadBPS))
		wantBuy.Quo(wantBuy, big.NewInt(10_000))
		if sellPrice.Cmp(wantSell) != 0 || buyPrice.Cmp(wantBuy) != 0 {
			t.Fatalf("pair %s crossed prices = %s/%s, want midpoint spread %s/%s", pair.pairKey(), sellPrice, buyPrice, wantSell, wantBuy)
		}
	}
}

func TestSimCrossPriceWalkPreservesArbitrageAndRestingDepth(t *testing.T) {
	pairs := buildSimTradingPairs(config.DefaultGenesisConfig())
	if len(pairs) == 0 {
		t.Fatal("expected simulated spot pairs")
	}
	for _, pair := range pairs {
		restingBuy, restingSell := pair.restingPrices(0)
		for _, offsetBPS := range []int64{-simCrossPriceWalkBoundBPS, 0, simCrossPriceWalkBoundBPS} {
			sellPrice, buyPrice := pair.crossedPricesAtOffsetBPS(offsetBPS)
			if sellPrice.Sign() <= 0 || buyPrice.Cmp(sellPrice) <= 0 {
				t.Fatalf("pair %s offset=%d prices=%s/%s do not preserve sell < buy",
					pair.pairKey(), offsetBPS, sellPrice, buyPrice)
			}
			grossSpreadBPS := new(big.Int).Sub(buyPrice, sellPrice)
			grossSpreadBPS.Mul(grossSpreadBPS, big.NewInt(10_000))
			grossSpreadBPS.Quo(grossSpreadBPS, sellPrice)
			if grossSpreadBPS.Cmp(big.NewInt(2*simSpotCrossHalfSpreadBPS)) < 0 {
				t.Fatalf("pair %s offset=%d gross spread=%s bps no longer preserves the arbitrage gap",
					pair.pairKey(), offsetBPS, grossSpreadBPS)
			}
			if restingBuy.Cmp(sellPrice) >= 0 || buyPrice.Cmp(restingSell) >= 0 {
				t.Fatalf("pair %s offset=%d crossed=%s/%s escaped resting depth %s/%s",
					pair.pairKey(), offsetBPS, sellPrice, buyPrice, restingBuy, restingSell)
			}
		}
	}

	offsetBPS := int64(0)
	seen := make(map[int64]struct{})
	for i := 0; i < 1000; i++ {
		next := simNextCrossPriceOffsetBPS(offsetBPS)
		if next == offsetBPS {
			t.Fatalf("price walk did not move at step %d offset=%d", i, offsetBPS)
		}
		if next < -simCrossPriceWalkBoundBPS || next > simCrossPriceWalkBoundBPS {
			t.Fatalf("price walk escaped bound at step %d: %d", i, next)
		}
		seen[next] = struct{}{}
		offsetBPS = next
	}
	if len(seen) < 2 {
		t.Fatalf("price walk did not create varied midpoints: %v", seen)
	}
}

func TestSimSpotRestingPricesStayOutsideCrossedFlow(t *testing.T) {
	pairs := buildSimTradingPairs(config.DefaultGenesisConfig())
	if len(pairs) == 0 {
		t.Fatal("expected simulated spot pairs")
	}
	for _, pair := range pairs {
		crossSell, crossBuy := pair.crossedPrices()
		previousBuy := new(big.Int).Set(crossSell)
		previousSell := new(big.Int).Set(crossBuy)
		for level := 0; level < simSpotLiquidityLevels; level++ {
			restingBuy, restingSell := pair.restingPrices(level)
			if restingBuy.Sign() <= 0 || restingSell.Sign() <= 0 ||
				restingBuy.Cmp(crossSell) >= 0 || crossBuy.Cmp(restingSell) >= 0 {
				t.Fatalf("pair %s level %d prices resting=%s/%s crossed=%s/%s do not preserve outer depth",
					pair.pairKey(), level, restingBuy, restingSell, crossSell, crossBuy)
			}
			if level > 0 && (restingBuy.Cmp(previousBuy) >= 0 || restingSell.Cmp(previousSell) <= 0) {
				t.Fatalf("pair %s level %d does not widen depth: previous=%s/%s current=%s/%s",
					pair.pairKey(), level, previousBuy, previousSell, restingBuy, restingSell)
			}
			previousBuy.Set(restingBuy)
			previousSell.Set(restingSell)
		}
	}
}

func TestFinalizedSimSpotOrdersAtPricesFindsOnlyExpectedOpenLevels(t *testing.T) {
	logger := logs.NewNodeLogger("sim-resting-liquidity-scan-test", 0)
	dbMgr, err := db.NewManager(filepath.Join(t.TempDir(), "db"), logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(dbMgr.Close)

	const pair = "BTC_USD"
	const price = "100"
	stateData, err := db.ProtoMarshal(&pb.OrderState{
		OrderId: "resting-buy", BaseToken: "BTC", QuoteToken: "USD", Price: price,
		Side: pb.OrderSide_BUY, Status: pb.OrderStateStatus_ORDER_OPEN,
	})
	if err != nil {
		t.Fatal(err)
	}
	indexData, err := db.ProtoMarshal(&pb.OrderPriceIndex{Ok: true})
	if err != nil {
		t.Fatal(err)
	}
	priceKey, err := db.PriceToKey128(price)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dbMgr.SyncFinalizedStateUpdates(1, []interface{}{
		testStateWriteOp{key: keys.KeyOrderState("resting-buy"), value: stateData},
		testStateWriteOp{key: keys.KeyOrderPriceIndex(pair, pb.OrderSide_BUY, false, priceKey, "resting-buy"), value: indexData},
	}, true); err != nil {
		t.Fatal(err)
	}

	orders, ok := finalizedSimSpotOrdersAtPrices(&NodeInstance{DBManager: dbMgr}, pair, pb.OrderSide_BUY, []*big.Int{big.NewInt(90), big.NewInt(100)})
	if !ok || len(orders) != 2 || orders[0].orderID != "" || orders[1].orderID != "resting-buy" {
		t.Fatalf("resting level scan ok=%v orders=%+v", ok, orders)
	}
}

func TestCapSimSpotBuyAmount(t *testing.T) {
	price := new(big.Int).Mul(big.NewInt(2), vm.PriceScale)
	availableQuote := big.NewInt(1000)
	if got := capSimSpotBuyAmount(big.NewInt(100), price, availableQuote); got.Cmp(big.NewInt(25)) != 0 {
		t.Fatalf("capped amount = %s, want 25", got)
	}
	if got := capSimSpotBuyAmount(big.NewInt(10), price, availableQuote); got.Cmp(big.NewInt(10)) != 0 {
		t.Fatalf("uncapped amount = %s, want 10", got)
	}
	if got := capSimSpotBuyAmount(big.NewInt(10), price, new(big.Int)); got.Sign() != 0 {
		t.Fatalf("zero-balance amount = %s, want 0", got)
	}
}

func TestCapSimSpotSellAmount(t *testing.T) {
	if got := capSimSpotSellAmount(big.NewInt(100), big.NewInt(1000)); got.Cmp(big.NewInt(50)) != 0 {
		t.Fatalf("capped amount = %s, want 50", got)
	}
	if got := capSimSpotSellAmount(big.NewInt(10), big.NewInt(1000)); got.Cmp(big.NewInt(10)) != 0 {
		t.Fatalf("uncapped amount = %s, want 10", got)
	}
}

func TestGenerateCancelOrderTx(t *testing.T) {
	tx := generateCancelOrderTx("sim-owner", "sim-order", 17)
	order := tx.GetOrderTx()
	if order == nil || order.Base == nil {
		t.Fatal("expected cancel order transaction")
	}
	if order.Op != pb.OrderOp_REMOVE || order.OpTargetId != "sim-order" {
		t.Fatalf("cancel target = %s/%s, want REMOVE/sim-order", order.Op, order.OpTargetId)
	}
	if order.Base.FromAddress != "sim-owner" || order.Base.Nonce != 17 || order.Base.Fee != "1000" {
		t.Fatalf("cancel base = %+v", order.Base)
	}
}

func TestBuildSimWithdrawAddressByChain(t *testing.T) {
	for _, tc := range []struct {
		name    string
		network string
		prefix  string
	}{
		{name: "mainnet", network: "mainnet", prefix: "bc1p"},
		{name: "testnet", network: "testnet3", prefix: "tb1p"},
		{name: "regtest", network: "regtest", prefix: "bcrt1p"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &TxSimulator{btcNetwork: tc.network}
			btcAddr, err := s.buildSimWithdrawAddress("btc", "seed")
			if err != nil {
				t.Fatalf("build BTC address: %v", err)
			}
			if !strings.HasPrefix(btcAddr, tc.prefix) {
				t.Fatalf("expected %s taproot address, got %s", tc.network, btcAddr)
			}
		})
	}

	s := &TxSimulator{}
	evmAddr, err := s.buildSimWithdrawAddress("base", "seed")
	if err != nil {
		t.Fatalf("build EVM address: %v", err)
	}
	if !strings.HasPrefix(evmAddr, "0x") || len(evmAddr) != 42 {
		t.Fatalf("expected 20-byte EVM hex address, got %s", evmAddr)
	}
}

func TestNewTxSimulatorUsesConfiguredBTCNetwork(t *testing.T) {
	frostCfg := config.FrostConfig{Chains: map[string]config.ChainFrostConfig{
		"BTC": {BTCNetwork: "regtest"},
	}}
	s := NewTxSimulator(nil, frostCfg, config.DefaultWitnessConfig(), nil)

	if s.btcNetwork != "regtest" {
		t.Fatalf("BTC network = %q, want regtest", s.btcNetwork)
	}
	addr, err := s.buildSimWithdrawAddress("btc", "configured-seed")
	if err != nil {
		t.Fatalf("build configured BTC address: %v", err)
	}
	if !strings.HasPrefix(addr, "bcrt1p") {
		t.Fatalf("expected configured regtest address, got %s", addr)
	}
}

func TestBuildSimWithdrawAddressRejectsUnknownBTCNetwork(t *testing.T) {
	s := &TxSimulator{btcNetwork: "unknown"}
	if _, err := s.buildSimWithdrawAddress("btc", "seed"); err == nil {
		t.Fatal("expected unknown BTC network to fail")
	}
}

func TestSimulatorTargetTxsPerBlockConfig(t *testing.T) {
	s := &TxSimulator{submitLimiter: newSimTxLimiter(1)}

	s.setTargetTxsPerBlock(250)
	cfg := s.getConfig()
	if cfg.TargetTxsPerBlock != 250 || cfg.TargetTxsPerSecond != 50 {
		t.Fatalf("default target mismatch: block=%d second=%d", cfg.TargetTxsPerBlock, cfg.TargetTxsPerSecond)
	}

	s.applyConfig(simulatorConfigDTO{
		Transfers: true, WitnessReg: true, Recharge: true, WitnessVote: true, Withdraw: true,
		Orders: true, FutureOrders: true, MassOrders: true, Arbitration: true,
		TargetTxsPerBlock: 1000,
	})
	cfg = s.getConfig()
	if cfg.TargetTxsPerBlock != 1000 || cfg.TargetTxsPerSecond != 200 {
		t.Fatalf("updated target mismatch: block=%d second=%d", cfg.TargetTxsPerBlock, cfg.TargetTxsPerSecond)
	}
}

func TestSimulatorTargetPartitionSplitsGlobalTarget(t *testing.T) {
	s := &TxSimulator{submitLimiter: newSimTxLimiter(1)}
	s.setTargetPartition(3, 20)
	s.setTargetTxsPerBlock(5000)

	cfg := s.getConfig()
	if cfg.TargetTxsPerBlock != 5000 || cfg.TargetTxsPerSecond != 1000 {
		t.Fatalf("global target mismatch: block=%d second=%d", cfg.TargetTxsPerBlock, cfg.TargetTxsPerSecond)
	}
	if cfg.TargetPartitions != 20 || cfg.TargetPartitionID != 3 || cfg.LocalTxsPerBlock != 250 || cfg.LocalTxsPerSecond != 50 {
		t.Fatalf("partition config mismatch: %+v", cfg)
	}
	if got := s.effectiveTargetTxsPerBlock(); got != 250 {
		t.Fatalf("effective target = %d, want 250", got)
	}
	s.submitLimiter.mu.Lock()
	rate := s.submitLimiter.rate
	s.submitLimiter.mu.Unlock()
	if rate != 50 {
		t.Fatalf("local limiter rate = %v, want 50", rate)
	}
}

func TestConfigureSimulatorPartitionUsesSingleLocalNodeID(t *testing.T) {
	s := &TxSimulator{submitLimiter: newSimTxLimiter(1)}
	configureSimulatorPartition(s, []*NodeInstance{{ID: 7}}, runtimeOptions{SimulatorPartitions: 20})
	if got := s.targetPartitionID.Load(); got != 7 {
		t.Fatalf("partition id = %d, want local node id 7", got)
	}
	if got := s.targetPartitions.Load(); got != 20 {
		t.Fatalf("partitions = %d, want 20", got)
	}
}

func TestSimulatorTransferTargetSplitsAcrossLocal20(t *testing.T) {
	s := &TxSimulator{}
	s.setTargetPartition(7, 20)
	if got := s.effectiveTransferTargetTxsPerBlock(); got != 50 {
		t.Fatalf("local transfer target = %d, want 50", got)
	}
	attempts := simAttemptsForBlockTarget(s.effectiveTransferTargetTxsPerBlock(), simTransferAttemptTick, simDefaultRateBlockSeconds)
	perBlock := attempts * int(time.Duration(simDefaultRateBlockSeconds)*time.Second/simTransferAttemptTick)
	if attempts != 1 || perBlock != 50 {
		t.Fatalf("transfer attempts=%d perBlock=%d, want 1 tick / 50 block", attempts, perBlock)
	}
}

func TestSimRandomTransferAmountChoices(t *testing.T) {
	want := map[string]bool{
		"10":   true,
		"100":  true,
		"200":  true,
		"1000": true,
	}
	if len(simTransferAmountChoices) != len(want) {
		t.Fatalf("transfer amount choices len = %d, want %d", len(simTransferAmountChoices), len(want))
	}
	for _, amount := range simTransferAmountChoices {
		got := strconv.FormatInt(amount, 10)
		if !want[got] {
			t.Fatalf("unexpected transfer amount choice %s", got)
		}
	}
	for i := 0; i < 128; i++ {
		if got := simRandomTransferAmount(); !want[got] {
			t.Fatalf("random transfer amount = %s, want one of %+v", got, want)
		}
	}
}

func TestSimulatorTargetPartitionRemainderAndOutOfRange(t *testing.T) {
	if got := simEffectiveTargetTxsPerBlock(503, 20, 2); got != 26 {
		t.Fatalf("remainder partition target = %d, want 26", got)
	}
	if got := simEffectiveTargetTxsPerBlock(503, 20, 3); got != 25 {
		t.Fatalf("base partition target = %d, want 25", got)
	}
	if got := simEffectiveTargetTxsPerBlock(503, 20, 20); got != 0 {
		t.Fatalf("out-of-range partition target = %d, want 0", got)
	}
}

func TestSimulatorWitnessRegistrationNodesSupportsPhysicalSingleNode(t *testing.T) {
	n0 := &NodeInstance{ID: 0, Address: "node0"}
	got := simulatorWitnessRegistrationNodes([]*NodeInstance{n0})
	if len(got) != 1 || got[0] != n0 {
		t.Fatalf("single-node registration set = %v, want node0", got)
	}
}

func TestSimulatorWitnessRegistrationNodesKeepsMultiNodeControllerOut(t *testing.T) {
	nodes := make([]*NodeInstance, 25)
	for i := range nodes {
		nodes[i] = &NodeInstance{ID: i}
	}
	got := simulatorWitnessRegistrationNodes(nodes)
	if len(got) != 20 {
		t.Fatalf("registration node count = %d, want 20", len(got))
	}
	if got[0] != nodes[1] || got[len(got)-1] != nodes[20] {
		t.Fatalf("registration range = %d..%d, want 1..20", got[0].ID, got[len(got)-1].ID)
	}
}

func TestSimulatorProbeRemotePressureSkipsInactiveNodes(t *testing.T) {
	heightServer := func(resp *pb.HeightResponse) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/heightquery" {
				http.NotFound(w, r)
				return
			}
			data, err := proto.Marshal(resp)
			if err != nil {
				t.Fatalf("marshal height response: %v", err)
			}
			_, _ = w.Write(data)
		}))
	}

	activeHigh := heightServer(&pb.HeightResponse{LastAcceptedHeight: 100, PendingTxCount: 5})
	defer activeHigh.Close()
	activeLow := heightServer(&pb.HeightResponse{LastAcceptedHeight: 99, PendingTxCount: 7})
	defer activeLow.Close()
	inactiveLow := heightServer(&pb.HeightResponse{LastAcceptedHeight: 1, PendingTxCount: 1000, IsSyncing: true})
	defer inactiveLow.Close()

	s := &TxSimulator{
		remoteClient: activeHigh.Client(),
		nodes: []*NodeInstance{
			{ID: 0, AdvertiseAddr: activeHigh.URL, TxPool: &txpool.TxPool{}, ConsensusManager: &consensus.ConsensusNodeManager{}},
			{ID: 1, AdvertiseAddr: activeLow.URL, TxPool: &txpool.TxPool{}, ConsensusManager: &consensus.ConsensusNodeManager{}},
			{ID: 20, AdvertiseAddr: inactiveLow.URL},
		},
	}

	got := s.probeRemotePressure()
	if !got.ok || got.active != 2 {
		t.Fatalf("remote pressure active mismatch: ok=%v active=%d", got.ok, got.active)
	}
	if got.maxLag != 1 || got.lagNode != 1 {
		t.Fatalf("remote lag included inactive node: lag=%d node=%d", got.maxLag, got.lagNode)
	}
	if got.maxPending != 7 || got.maxPendingNode != 1 {
		t.Fatalf("remote pending included inactive node: pending=%d node=%d", got.maxPending, got.maxPendingNode)
	}
	if got.catchUp {
		t.Fatalf("inactive syncing node should not trigger catch-up: %+v", got)
	}
}

func TestBuildSingleModeSimulatorNodesUsesOnlyLocalAccount(t *testing.T) {
	local := &NodeInstance{ID: 7, Address: "local-address"}
	got := buildSingleModeSimulatorNodes([]*NodeInstance{local})
	if len(got) != 1 || got[0] != local {
		t.Fatalf("single-mode simulator nodes = %+v, want only local node", got)
	}
}

func TestRandomTransferEndpointsUsesGeneratedDexTargetWithSingleNode(t *testing.T) {
	local := &NodeInstance{ID: 7, Address: "local-address", TxPool: &txpool.TxPool{}, ConsensusManager: &consensus.ConsensusNodeManager{}}
	s := &TxSimulator{nodes: []*NodeInstance{local}}

	from, to, err := s.randomTransferEndpoints()
	if err != nil {
		t.Fatalf("randomTransferEndpoints failed: %v", err)
	}
	if from != local {
		t.Fatalf("from node = %+v, want local node", from)
	}
	if !strings.HasPrefix(to, utils.DEXAddressPrefix) {
		t.Fatalf("target address = %s, want %s...", to, utils.DEXAddressPrefix)
	}
	if to == local.Address {
		t.Fatal("target address should be freshly generated, not the sender")
	}
}

func TestSimulatorNonceStartsFromPersistedAccount(t *testing.T) {
	dbMgr, err := db.NewManager(filepath.Join(t.TempDir(), "db"), logs.NewNodeLogger("sim-nonce-test", 0))
	if err != nil {
		t.Fatalf("new db manager: %v", err)
	}
	t.Cleanup(dbMgr.Close)

	const addr = "sim-account"
	accountData, err := db.ProtoMarshal(&pb.Account{Address: addr, Nonce: 42})
	if err != nil {
		t.Fatalf("marshal account: %v", err)
	}
	if _, err := dbMgr.SyncFinalizedStateUpdates(1, []interface{}{testStateWriteOp{key: keys.KeyAccount(addr), value: accountData}}, true); err != nil {
		t.Fatalf("sync account: %v", err)
	}

	s := &TxSimulator{nodes: []*NodeInstance{{Address: addr, DBManager: dbMgr}}}
	if got := s.getNextNonce(addr); got != 42 {
		t.Fatalf("first nonce = %d, want 42", got)
	}
	if got := s.getNextNonce(addr); got != 43 {
		t.Fatalf("second nonce = %d, want 43", got)
	}
}

func TestSubmitGeneratedSpotCrossUsesConsecutiveAccountNonces(t *testing.T) {
	logger := logs.NewNodeLogger("sim-cross-nonce-test", 0)
	dbMgr, err := db.NewManager(filepath.Join(t.TempDir(), "db"), logger)
	if err != nil {
		t.Fatalf("new db manager: %v", err)
	}
	t.Cleanup(dbMgr.Close)

	priv := strings.Repeat("0", 63) + "1"
	privKey, err := utils.ParseSecp256k1PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	address, err := utils.DeriveDexAddress(privKey)
	if err != nil {
		t.Fatal(err)
	}
	accountData, err := db.ProtoMarshal(&pb.Account{Address: address, Nonce: 42})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dbMgr.SyncFinalizedStateUpdates(1, []interface{}{
		testStateWriteOp{key: keys.KeyAccount(address), value: accountData},
	}, true); err != nil {
		t.Fatal(err)
	}
	syncSimulatorCapitalState(t, dbMgr, 2, 42, address, "FB", "1000000000")
	pool, err := txpool.NewTxPool(dbMgr, simulatorNoopValidator{}, address, logger)
	if err != nil {
		t.Fatal(err)
	}
	node := &NodeInstance{
		Address: address, KeyManager: testRuntimeKey(t, priv), DBManager: dbMgr, TxPool: pool,
		ConsensusManager: &consensus.ConsensusNodeManager{},
	}
	s := &TxSimulator{
		nodes: []*NodeInstance{node}, nonces: make(map[string]uint64), protocolNonces: make(map[string]uint64),
		nonceLocks: make(map[string]*sync.Mutex), submitLimiter: newSimTxLimiter(1000),
	}
	s.submitThrottle.Store(simThrottleFullPermille)
	sell, buy := s.submitGeneratedSpotCross(
		node,
		func(nonce uint64) *pb.AnyTx {
			return generateOrderTx(address, "BTC", "USD", "1", "1", nonce, pb.OrderSide_SELL)
		},
		func(nonce uint64) *pb.AnyTx {
			return generateOrderTx(address, "BTC", "USD", "1", "2", nonce, pb.OrderSide_BUY)
		},
	)
	if !sell.ok || !buy.ok || sell.nonce != 42 || buy.nonce != 43 {
		t.Fatalf("spot cross submissions = sell(%v,%d) buy(%v,%d), want true/42 true/43", sell.ok, sell.nonce, buy.ok, buy.nonce)
	}
	if sell.tx == nil || buy.tx == nil || sell.tx.GetTxId() == "" || buy.tx.GetTxId() == "" || sell.tx.GetTxId() == buy.tx.GetTxId() {
		t.Fatal("both crossed orders must be signed and accepted as distinct transactions")
	}
	if got := s.getNextNonce(address); got != 44 {
		t.Fatalf("next nonce after crossed pair = %d, want 44", got)
	}
}

func TestSubmitGeneratedFutureCrossCreatesControlledPair(t *testing.T) {
	logger := logs.NewNodeLogger("sim-future-cross-test", 0)
	dbMgr, err := db.NewManager(filepath.Join(t.TempDir(), "db"), logger)
	if err != nil {
		t.Fatalf("new db manager: %v", err)
	}
	t.Cleanup(dbMgr.Close)

	priv := strings.Repeat("0", 63) + "1"
	privKey, err := utils.ParseSecp256k1PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	address, err := utils.DeriveDexAddress(privKey)
	if err != nil {
		t.Fatal(err)
	}
	const quote = "ETH.USDT"
	syncSimulatorCapitalState(t, dbMgr, 1, 42, address, quote, "1000000000000000000000000000000")
	syncSimulatorCapitalState(t, dbMgr, 2, 42, address, "FB", "1000000000")
	pool, err := txpool.NewTxPool(dbMgr, simulatorNoopValidator{}, address, logger)
	if err != nil {
		t.Fatal(err)
	}
	node := &NodeInstance{
		Address: address, KeyManager: testRuntimeKey(t, priv), DBManager: dbMgr, TxPool: pool,
		ConsensusManager: &consensus.ConsensusNodeManager{},
	}
	s := &TxSimulator{
		nodes: []*NodeInstance{node}, nonces: make(map[string]uint64), protocolNonces: make(map[string]uint64),
		nonceLocks: make(map[string]*sync.Mutex), submitLimiter: newSimTxLimiter(1000),
	}
	s.submitThrottle.Store(simThrottleFullPermille)
	pair := simTradingPair{
		BaseToken: "BTC", QuoteToken: quote, BaseDecimals: 8, QuoteDecimals: 6,
		MinPrice: 60_000 * simUnit, MaxPrice: 70_000 * simUnit,
	}
	amount := big.NewInt(2_000_000)
	sellPrice, buyPrice := pair.crossedPrices()
	sell, buy, retry := s.submitGeneratedFutureCross(node, pair, amount, sellPrice, buyPrice, simFutureCrossLeverage)
	if retry != nil {
		t.Fatal("complete pair unexpectedly returned a retry")
	}
	if !sell.ok || !buy.ok || sell.nonce != 42 || buy.nonce != 43 {
		t.Fatalf("future cross submissions = sell(%v,%d) buy(%v,%d), want true/42 true/43", sell.ok, sell.nonce, buy.ok, buy.nonce)
	}
	shortTx, longTx := sell.tx.GetFutureOrderTx(), buy.tx.GetFutureOrderTx()
	if shortTx == nil || longTx == nil {
		t.Fatal("crossed submissions must both be futures orders")
	}
	if shortTx.Pair != pair.pairKey() || longTx.Pair != shortTx.Pair ||
		shortTx.Quantity != amount.String() || longTx.Quantity != shortTx.Quantity ||
		shortTx.Leverage != simFutureCrossLeverage || longTx.Leverage != shortTx.Leverage {
		t.Fatalf("crossed futures fields differ: short=%+v long=%+v", shortTx, longTx)
	}
	if shortTx.Side != int32(future.Short) || longTx.Side != int32(future.Long) ||
		shortTx.Price != sellPrice.String() || longTx.Price != buyPrice.String() ||
		shortTx.IsClose || longTx.IsClose || shortTx.IsMarket || longTx.IsMarket {
		t.Fatalf("unexpected crossed futures directions: short=%+v long=%+v", shortTx, longTx)
	}
	if sell.tx.GetTxId() == "" || buy.tx.GetTxId() == "" || sell.tx.GetTxId() == buy.tx.GetTxId() {
		t.Fatal("crossed futures orders must have distinct signed transaction IDs")
	}

	wantReserved := future.CalcRequiredMargin(amount, sellPrice, simFutureCrossLeverage)
	wantReserved.Add(wantReserved, future.CalcRequiredMargin(amount, buyPrice, simFutureCrossLeverage))
	s.capitalMu.Lock()
	gotReserved := simCapitalGet(s.capitalPendingTotal, address, quote)
	s.capitalMu.Unlock()
	if gotReserved.Cmp(wantReserved) != 0 {
		t.Fatalf("future cross reserved margin = %s, want %s", gotReserved, wantReserved)
	}
}

func TestSubmitGeneratedFutureCrossRetriesRejectedSecondLeg(t *testing.T) {
	logger := logs.NewNodeLogger("sim-future-cross-retry-test", 0)
	dbMgr, err := db.NewManager(filepath.Join(t.TempDir(), "db"), logger)
	if err != nil {
		t.Fatalf("new db manager: %v", err)
	}
	t.Cleanup(dbMgr.Close)

	priv := strings.Repeat("0", 63) + "1"
	privKey, err := utils.ParseSecp256k1PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	address, err := utils.DeriveDexAddress(privKey)
	if err != nil {
		t.Fatal(err)
	}
	const quote = "ETH.USDT"
	syncSimulatorCapitalState(t, dbMgr, 1, 42, address, quote, "1000000000000000000000000000000")
	                                                                                                            
	syncSimulatorCapitalState(t, dbMgr, 2, 42, address, "FB", "1000")
	pool, err := txpool.NewTxPool(dbMgr, simulatorNoopValidator{}, address, logger)
	if err != nil {
		t.Fatal(err)
	}
	node := &NodeInstance{
		Address: address, KeyManager: testRuntimeKey(t, priv), DBManager: dbMgr, TxPool: pool,
		ConsensusManager: &consensus.ConsensusNodeManager{},
	}
	s := &TxSimulator{
		nodes: []*NodeInstance{node}, nonces: make(map[string]uint64), protocolNonces: make(map[string]uint64),
		nonceLocks: make(map[string]*sync.Mutex), submitLimiter: newSimTxLimiter(1000),
	}
	s.submitThrottle.Store(simThrottleFullPermille)
	pair := simTradingPair{BaseToken: "BTC", QuoteToken: quote, BaseDecimals: 8, QuoteDecimals: 6, MinPrice: 60_000 * simUnit, MaxPrice: 70_000 * simUnit}
	amount := big.NewInt(2_000_000)
	sellPrice, buyPrice := pair.crossedPrices()
	sell, buy, retry := s.submitGeneratedFutureCross(node, pair, amount, sellPrice, buyPrice, simFutureCrossLeverage)
	if !sell.ok || buy.ok || retry == nil || sell.nonce != 42 {
		t.Fatalf("initial future pair = sell(%v,%d) buy(%v,%d) retry=%v", sell.ok, sell.nonce, buy.ok, buy.nonce, retry != nil)
	}

	syncSimulatorCapitalState(t, dbMgr, 3, 42, address, "FB", "1000000000")
	retriedBuy, done := s.retryPendingFutureCross(retry)
	if !done || !retriedBuy.ok || retriedBuy.nonce != 43 {
		t.Fatalf("retried future BUY = done=%v ok=%v nonce=%d, want true/true/43", done, retriedBuy.ok, retriedBuy.nonce)
	}
	order := retriedBuy.tx.GetFutureOrderTx()
	if order == nil || order.Side != int32(future.Long) || order.Pair != pair.pairKey() ||
		order.Quantity != amount.String() || order.Price != buyPrice.String() {
		t.Fatalf("retried future BUY changed fields: %+v", order)
	}

	wantReserved := future.CalcRequiredMargin(amount, sellPrice, simFutureCrossLeverage)
	wantReserved.Add(wantReserved, future.CalcRequiredMargin(amount, buyPrice, simFutureCrossLeverage))
	s.capitalMu.Lock()
	gotReserved := simCapitalGet(s.capitalPendingTotal, address, quote)
	heldAfterRetry := simCapitalGet(s.capitalHeldTotals, address, quote)
	s.capitalMu.Unlock()
	if gotReserved.Cmp(wantReserved) != 0 || heldAfterRetry.Sign() != 0 {
		t.Fatalf("retry reservations pending=%s held=%s, want %s/0", gotReserved, heldAfterRetry, wantReserved)
	}
}

func TestPartitionSimTradingPairsAssignsEachPairOnce(t *testing.T) {
	pairs := buildSimTradingPairs(config.DefaultGenesisConfig())
	const partitions = int64(5)
	seen := make(map[string]int, len(pairs))
	for partitionID := int64(0); partitionID < partitions; partitionID++ {
		for _, pair := range partitionSimTradingPairs(pairs, partitions, partitionID) {
			seen[pair.pairKey()]++
		}
	}
	if len(seen) != len(pairs) {
		t.Fatalf("partitioned pairs = %d, want %d", len(seen), len(pairs))
	}
	for pair, count := range seen {
		if count != 1 {
			t.Fatalf("pair %s assigned %d times, want once", pair, count)
		}
	}
}

func syncSimulatorCapitalState(t *testing.T, dbMgr *db.Manager, height, nonce uint64, address, token, balance string) {
	t.Helper()
	accountData, err := db.ProtoMarshal(&pb.Account{Address: address, Nonce: nonce})
	if err != nil {
		t.Fatalf("marshal account: %v", err)
	}
	balanceData, err := db.ProtoMarshal(&pb.TokenBalanceRecord{
		Address: address,
		Token:   token,
		Balance: &pb.TokenBalance{Balance: balance},
	})
	if err != nil {
		t.Fatalf("marshal balance: %v", err)
	}
	if _, err := dbMgr.SyncFinalizedStateUpdates(height, []interface{}{
		testStateWriteOp{key: keys.KeyAccount(address), value: accountData},
		testStateWriteOp{key: keys.KeyBalance(address, token), value: balanceData},
	}, true); err != nil {
		t.Fatalf("sync simulator capital: %v", err)
	}
}

func TestSimulatorCapitalHoldSurvivesUntilFinalizedNonce(t *testing.T) {
	dbMgr, err := db.NewManager(filepath.Join(t.TempDir(), "db"), logs.NewNodeLogger("sim-capital-test", 0))
	if err != nil {
		t.Fatalf("new db manager: %v", err)
	}
	t.Cleanup(dbMgr.Close)

	const address = "sim-capital-owner"
	node := &NodeInstance{Address: address, DBManager: dbMgr}
	s := &TxSimulator{nodes: []*NodeInstance{node}}
	syncSimulatorCapitalState(t, dbMgr, 1, 0, address, "USD", "1000")

	if !s.holdSimCapital("spot:BTC_USD", node, map[string]*big.Int{"USD": big.NewInt(300)}) {
		t.Fatal("expected capital hold")
	}
	if got, ok := s.availableSimCapital(node, "USD"); !ok || got.Cmp(big.NewInt(700)) != 0 {
		t.Fatalf("available after hold = %v/%v, want 700/true", got, ok)
	}
	if s.holdSimCapital("spot:ETH_USD", node, map[string]*big.Int{"USD": big.NewInt(800)}) {
		t.Fatal("overlapping hold exceeded finalized capital")
	}
	if !s.commitSimCapitalHold("spot:BTC_USD", "USD", 0) {
		t.Fatal("expected hold to become pending reservation")
	}
	if got, ok := s.availableSimCapital(node, "USD"); !ok || got.Cmp(big.NewInt(700)) != 0 {
		t.Fatalf("available while tx pending = %v/%v, want 700/true", got, ok)
	}

	                                                                        
	                                                                          
	syncSimulatorCapitalState(t, dbMgr, 2, 1, address, "USD", "700")
	if got, ok := s.availableSimCapital(node, "USD"); !ok || got.Cmp(big.NewInt(700)) != 0 {
		t.Fatalf("available after finalization = %v/%v, want 700/true", got, ok)
	}
}

func TestSimulatorCapitalCommitAfterFastFinalizationDoesNotLeaveStaleReservation(t *testing.T) {
	dbMgr, err := db.NewManager(filepath.Join(t.TempDir(), "db"), logs.NewNodeLogger("sim-capital-fast-finalize-test", 0))
	if err != nil {
		t.Fatalf("new db manager: %v", err)
	}
	t.Cleanup(dbMgr.Close)

	const address = "sim-fast-finalize-owner"
	node := &NodeInstance{Address: address, DBManager: dbMgr}
	s := &TxSimulator{nodes: []*NodeInstance{node}}
	syncSimulatorCapitalState(t, dbMgr, 1, 0, address, "USD", "1000")
	if !s.holdSimCapital("spot:BTC_USD", node, map[string]*big.Int{"USD": big.NewInt(300)}) {
		t.Fatal("expected capital hold")
	}

	                                                                        
	                                                            
	syncSimulatorCapitalState(t, dbMgr, 2, 1, address, "USD", "700")
	if !s.commitSimCapitalHold("spot:BTC_USD", "USD", 0) {
		t.Fatal("fast-finalized hold conversion failed")
	}
	if got, ok := s.availableSimCapital(node, "USD"); !ok || got.Cmp(big.NewInt(700)) != 0 {
		t.Fatalf("available after fast finalization = %v/%v, want 700/true", got, ok)
	}
}

func TestAdvanceTrackedSpotOrderWaitsForCancellationFinalization(t *testing.T) {
	logger := logs.NewNodeLogger("sim-spot-cancel-test", 0)
	dbMgr, err := db.NewManager(filepath.Join(t.TempDir(), "db"), logger)
	if err != nil {
		t.Fatalf("new db manager: %v", err)
	}
	t.Cleanup(dbMgr.Close)

	priv := strings.Repeat("0", 63) + "1"
	privKey, err := utils.ParseSecp256k1PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	address, err := utils.DeriveDexAddress(privKey)
	if err != nil {
		t.Fatal(err)
	}
	syncSimulatorCapitalState(t, dbMgr, 1, 1, address, "FB", "1000000")
	stateData, err := db.ProtoMarshal(&pb.OrderState{
		OrderId: "open-order", Owner: address, Status: pb.OrderStateStatus_ORDER_OPEN,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dbMgr.SyncFinalizedStateUpdates(2, []interface{}{
		testStateWriteOp{key: keys.KeyOrderState("open-order"), value: stateData},
	}, true); err != nil {
		t.Fatal(err)
	}
	pool, err := txpool.NewTxPool(dbMgr, simulatorNoopValidator{}, address, logger)
	if err != nil {
		t.Fatal(err)
	}
	node := &NodeInstance{
		Address: address, KeyManager: testRuntimeKey(t, priv), DBManager: dbMgr, TxPool: pool,
		ConsensusManager: &consensus.ConsensusNodeManager{},
	}
	s := &TxSimulator{nodes: []*NodeInstance{node}}
	item := simTrackedSpotOrder{orderID: "open-order", nonce: 0}
	cancels := 0
	s.advanceTrackedSpotOrder(node, 1, &item, &cancels)
	if item.cancelID == "" || item.cancelNonce != 1 || cancels != 1 {
		t.Fatalf("cancel tracking = %+v cancels=%d", item, cancels)
	}
	firstCancel := item.cancelID
	s.advanceTrackedSpotOrder(node, 1, &item, &cancels)
	if item.cancelID != firstCancel || cancels != 1 || item.terminal {
		t.Fatalf("pending cancellation was duplicated or completed early: %+v cancels=%d", item, cancels)
	}

	accountData, err := db.ProtoMarshal(&pb.Account{Address: address, Nonce: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dbMgr.SyncFinalizedStateUpdates(3, []interface{}{
		testStateWriteOp{key: keys.KeyAccount(address), value: accountData},
		testStateDeleteOp{key: keys.KeyOrderState("open-order")},
	}, true); err != nil {
		t.Fatal(err)
	}
	s.advanceTrackedSpotOrder(node, 2, &item, &cancels)
	if !item.terminal {
		t.Fatalf("order was not released after finalized cancellation: %+v", item)
	}
}

func TestSimulatorProtocolTxDoesNotAdvanceAccountNonce(t *testing.T) {
	logger := logs.NewNodeLogger("sim-protocol-nonce-test", 0)
	dbMgr, err := db.NewManager(filepath.Join(t.TempDir(), "db"), logger)
	if err != nil {
		t.Fatalf("new db manager: %v", err)
	}
	t.Cleanup(dbMgr.Close)

	priv := strings.Repeat("0", 63) + "1"
	privKey, err := utils.ParseSecp256k1PrivateKey(priv)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}
	addr, err := utils.DeriveDexAddress(privKey)
	if err != nil {
		t.Fatalf("derive address: %v", err)
	}
	accountData, err := db.ProtoMarshal(&pb.Account{Address: addr, Nonce: 42})
	if err != nil {
		t.Fatalf("marshal account: %v", err)
	}
	balanceData, err := db.ProtoMarshal(&pb.TokenBalanceRecord{Balance: &pb.TokenBalance{Balance: "1000000000"}})
	if err != nil {
		t.Fatalf("marshal balance: %v", err)
	}
	if _, err := dbMgr.SyncFinalizedStateUpdates(1, []interface{}{
		testStateWriteOp{key: keys.KeyAccount(addr), value: accountData},
		testStateWriteOp{key: keys.KeyBalance(addr, "FB"), value: balanceData},
	}, true); err != nil {
		t.Fatalf("sync account: %v", err)
	}
	pool, err := txpool.NewTxPool(dbMgr, simulatorNoopValidator{}, addr, logger)
	if err != nil {
		t.Fatalf("new txpool: %v", err)
	}
	node := &NodeInstance{
		Address:          addr,
		KeyManager:       testRuntimeKey(t, priv),
		DBManager:        dbMgr,
		TxPool:           pool,
		ConsensusManager: &consensus.ConsensusNodeManager{},
	}
	s := &TxSimulator{
		nodes:          []*NodeInstance{node},
		nonces:         make(map[string]uint64),
		protocolNonces: make(map[string]uint64),
		nonceLocks:     make(map[string]*sync.Mutex),
		submitLimiter:  newSimTxLimiter(1000),
	}
	s.submitThrottle.Store(simThrottleFullPermille)

	protocolNonce, _, ok := s.submitGeneratedProtocolTx(node, func(nonce uint64) *pb.AnyTx {
		return generateWitnessVoteTx(addr, "request-1", 0, pb.WitnessVoteType_VOTE_PASS, nonce)
	})
	if !ok {
		t.Fatal("protocol tx should submit")
	}
	if protocolNonce != 42 {
		t.Fatalf("first protocol nonce = %d, want 42", protocolNonce)
	}
	if got := s.getNextNonce(addr); got != 42 {
		t.Fatalf("protocol tx advanced account nonce: got next account nonce %d, want 42", got)
	}
	s.releaseLatestNonce(addr, 42)

	accountNonce, _, ok := s.submitGeneratedTx(node, func(nonce uint64) *pb.AnyTx {
		return generateWitnessChallengeTx(addr, "req-1", "100000", "test", nonce)
	})
	if !ok {
		t.Fatal("account nonce tx through generic submit should submit")
	}
	if accountNonce != 42 {
		t.Fatalf("first account nonce = %d, want 42", accountNonce)
	}
	if got := s.getNextNonce(addr); got != 43 {
		t.Fatalf("account tx did not advance account nonce: got %d, want 43", got)
	}
}

func TestSimulatorFutureAttemptsPerTick(t *testing.T) {
	if got := simAttemptsPerTick(5000, simFutureTargetSharePermille, simFutureAttemptTick, simDefaultRateBlockSeconds); got != 8 {
		t.Fatalf("future attempts per tick = %d, want 8", got)
	}
}

func TestSimulatorSpotAttemptsPerTick(t *testing.T) {
	if got := simAttemptsPerTick(5000, simSpotTargetSharePermille, simSpotAttemptTick, simDefaultRateBlockSeconds); got != 20 {
		t.Fatalf("spot attempts per tick = %d, want 20", got)
	}
}

func TestSimulatorOneSecondRateWindowSplitsFivePartitions(t *testing.T) {
	s := &TxSimulator{submitLimiter: newSimTxLimiter(1)}
	s.setTargetPartition(3, 5)
	s.setTargetTxsPerBlock(5000)

	defaultCfg := s.getConfig()
	if defaultCfg.RateBlockSeconds != 5 || defaultCfg.TargetTxsPerSecond != 1000 || defaultCfg.LocalTxsPerSecond != 200 {
		t.Fatalf("default five-second rate mismatch: %+v", defaultCfg)
	}

	configureSimulatorRate(s, runtimeOptions{SimulatorRateBlockSeconds: 1})
	cfg := s.getConfig()
	if cfg.RateBlockSeconds != 1 || cfg.TargetTxsPerSecond != 5000 || cfg.LocalTxsPerBlock != 1000 || cfg.LocalTxsPerSecond != 1000 {
		t.Fatalf("one-second partition rate mismatch: %+v", cfg)
	}
	s.submitLimiter.mu.Lock()
	limiterRate := s.submitLimiter.rate
	s.submitLimiter.mu.Unlock()
	if limiterRate != 1000 {
		t.Fatalf("local limiter rate = %v, want 1000", limiterRate)
	}
	if got := simAttemptsPerTick(s.effectiveTargetTxsPerBlock(), simFutureTargetSharePermille, simFutureAttemptTick, s.rateBlockSecondsValue()); got != 8 {
		t.Fatalf("future attempts per tick = %d, want 8", got)
	}
	if got := simAttemptsPerTick(s.effectiveTargetTxsPerBlock(), simSpotTargetSharePermille, simSpotAttemptTick, s.rateBlockSecondsValue()); got != 20 {
		t.Fatalf("spot attempts per tick = %d, want 20", got)
	}
}

func TestSimulatorRateBlockSecondsValidation(t *testing.T) {
	for _, value := range []int{simMinRateBlockSeconds, simDefaultRateBlockSeconds, simMaxRateBlockSeconds} {
		if err := validateSimulatorRateBlockSeconds(value); err != nil {
			t.Fatalf("value %d should be valid: %v", value, err)
		}
	}
	for _, value := range []int{0, -1, simMaxRateBlockSeconds + 1} {
		if err := validateSimulatorRateBlockSeconds(value); err == nil {
			t.Fatalf("value %d should be rejected", value)
		}
	}
}

func TestSimulatorConfigWithoutRateKeepsCLIWindow(t *testing.T) {
	s := &TxSimulator{submitLimiter: newSimTxLimiter(1)}
	s.setRateBlockSeconds(1)
	s.applyConfig(simulatorConfigDTO{TargetTxsPerBlock: 5000})
	if got := s.rateBlockSecondsValue(); got != 1 {
		t.Fatalf("omitted API rate changed CLI window to %d", got)
	}
}

func TestSingleModeSimulatorProfileEnablesMixedStress(t *testing.T) {
	s := &TxSimulator{submitLimiter: newSimTxLimiter(1)}
	configureSingleModeSimulator(s, runtimeOptions{SimulatorTargetTxsPerBlock: int(simMaxTargetTxsPerBlock), SimulatorTransfers: true})
	cfg := s.getConfig()
	if cfg.TargetTxsPerBlock != simMaxTargetTxsPerBlock {
		t.Fatalf("target txs per block = %d, want %d", cfg.TargetTxsPerBlock, simMaxTargetTxsPerBlock)
	}
	if !cfg.Orders || !cfg.FutureOrders {
		t.Fatalf("single-mode simulator should enable spot and future orders: %+v", cfg)
	}
	if !cfg.WitnessReg || cfg.Recharge || cfg.WitnessVote || cfg.Arbitration {
		t.Fatalf("default profile should enable witness registration without synthetic witness activity: %+v", cfg)
	}
}

func TestSimulatorWithdrawIntervalConfig(t *testing.T) {
	s := &TxSimulator{}

	if got := s.getConfig().WithdrawInterval; got != simDefaultWithdrawIntervalSeconds {
		t.Fatalf("default withdraw interval mismatch: got=%d want=%d", got, simDefaultWithdrawIntervalSeconds)
	}

	s.applyConfig(simulatorConfigDTO{
		Transfers: true, WitnessReg: true, Recharge: true, WitnessVote: true, Withdraw: true,
		Orders: true, FutureOrders: true, MassOrders: true, Arbitration: true,
		WithdrawInterval: 3,
	})
	if got := s.getConfig().WithdrawInterval; got != 3 {
		t.Fatalf("updated withdraw interval mismatch: got=%d want=3", got)
	}

	s.setWithdrawIntervalSeconds(simMaxWithdrawIntervalSeconds + 1)
	if got := s.getConfig().WithdrawInterval; got != simMaxWithdrawIntervalSeconds {
		t.Fatalf("clamped withdraw interval mismatch: got=%d want=%d", got, simMaxWithdrawIntervalSeconds)
	}
}
