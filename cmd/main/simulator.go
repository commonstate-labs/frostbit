package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"dex/config"
	bridgeasset "dex/frost/asset"
	frostchain "dex/frost/chain"
	frostbtc "dex/frost/chain/btc"
	"dex/future"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"dex/txverify"
	"dex/utils"
	"dex/vm"
	"dex/witness"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	mrand "math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcutil"
	"google.golang.org/protobuf/proto"
)

                              
type TxSimulator struct {
	nodes               []*NodeInstance
	nonces              map[string]uint64
	protocolNonces      map[string]uint64
	nonceLocks          map[string]*sync.Mutex
	accountSubmissions  map[string]*simAccountSubmissions                                              
	mu                  sync.Mutex
	capitalMu           sync.Mutex
	capitalHolds        map[string]simCapitalHold
	capitalHeldTotals   map[string]map[string]*big.Int
	capitalPending      map[string]map[uint64]simCapitalReservation
	capitalPendingTotal map[string]map[string]*big.Int
	capitalFinalized    map[string]uint64
	paused              atomic.Bool
	submitLimiter       *simTxLimiter
	submitThrottle      atomic.Int64
	targetTxsPerBlock   atomic.Int64
	targetPartitions    atomic.Int64
	targetPartitionID   atomic.Int64
	rateBlockSeconds    atomic.Int64
	withdrawInterval    atomic.Int64
	chains              []string                                                       
	btcNetwork          string
	rechargeAssets      []simRechargeAsset
	withdrawAssets      []simRechargeAsset
	tradingPairs        []simTradingPair
	witnessStakeAmount  string
	remoteClient        *http.Client
	                                                  
	disableTransfers    atomic.Bool
	disableWitnessReg   atomic.Bool
	disableRecharge     atomic.Bool
	disableWitnessVote  atomic.Bool
	disableWithdraw     atomic.Bool
	disableOrders       atomic.Bool
	disableFutureOrders atomic.Bool
	disableMassOrders   atomic.Bool
	disableArbitration  atomic.Bool
}

                                                                                                 
                                                                                
const simUnit = int64(100_000_000)

const simRechargeBatchPerTick = 3
const simSpotOrdersPerPairPerTick = 1
const simFutureOrdersPerPairPerTick = 1
const simSpotTargetTxPerPairPerSecond = 5
const simFutureTargetTxPerPairPerSecond = 5
const simDefaultRateBlockSeconds = 5
const simMinRateBlockSeconds = 1
const simMaxRateBlockSeconds = 60
const simDefaultTargetTxsPerBlock = int64(250)
const simMaxTargetTxsPerBlock = int64(5000)
const simTransferTargetTxsPerBlock = int64(1000)
const simTransferAttemptTick = 100 * time.Millisecond
const simSpotAttemptTick = 100 * time.Millisecond
const simFutureAttemptTick = 10 * time.Millisecond
const simSpotOrderCleanupInterval = time.Second
const simSpotOrderCancelLimit = 64
const simSpotCrossHalfSpreadBPS = int64(50)
const simCrossPriceWalkBoundBPS = int64(100)
const simCrossPriceWalkMaxStepBPS = int64(8)
const simSpotLiquidityLevels = 3
const simSpotLiquidityBaseHalfSpreadBPS = int64(200)
const simSpotLiquidityStepBPS = int64(100)
const simSpotLiquidityRefreshInterval = 15 * time.Second
const simSpotLiquiditySeedOrdersPerTick = 2
const simSpotBuyAvailableBPS = int64(500)
const simFutureCrossLeverage = uint64(5)
const simSpotTargetSharePermille = int64(200)
const simFutureTargetSharePermille = int64(800)
const simDefaultWithdrawIntervalSeconds = int64(15)
const simMinWithdrawIntervalSeconds = int64(1)
const simMaxWithdrawIntervalSeconds = int64(3600)
const simWithdrawAmountPermille = int64(500)
const simRechargeWholeAmount = int64(7000)
const simDefaultWithdrawRawAmount = "3500"
const simWithdrawQueueBackpressureLimit = uint64(1000)
const simWitnessRegistrationHeight = uint64(1)
const simThrottleFullPermille = int64(1000)
const simThrottleMinPermille = int64(50)
const simRemoteProbeInterval = 2 * time.Second

var simTransferAmountChoices = [...]int64{
	                
	           
	                  
	10,
	100,
	200,
	1000,
}

type simRemotePressure struct {
	ok             bool
	active         int
	maxPending     int
	maxPendingNode int
	maxLag         uint64
	lagNode        int
	catchUp        bool
	catchUpNode    int
	catchUpReason  string
}

func simPerPairOrderTickInterval(pairCount, targetTxPerPairPerSecond, ordersPerPairPerTick int) time.Duration {
	if pairCount <= 0 || targetTxPerPairPerSecond <= 0 || ordersPerPairPerTick <= 0 {
		return time.Second
	}
	averagePairsPerTick := 2                                
	interval := time.Second * time.Duration(averagePairsPerTick*ordersPerPairPerTick) / time.Duration(targetTxPerPairPerSecond*pairCount)
	if interval < time.Millisecond {
		return time.Millisecond
	}
	return interval
}

func simNormalizeTargetTxsPerBlock(value int64) int64 {
	if value <= 0 {
		return simDefaultTargetTxsPerBlock
	}
	if value > simMaxTargetTxsPerBlock {
		return simMaxTargetTxsPerBlock
	}
	return value
}

func simNormalizeRateBlockSeconds(value int64) int64 {
	if value <= 0 {
		return simDefaultRateBlockSeconds
	}
	if value > simMaxRateBlockSeconds {
		return simMaxRateBlockSeconds
	}
	return value
}

func validateSimulatorRateBlockSeconds(value int) error {
	if value < simMinRateBlockSeconds || value > simMaxRateBlockSeconds {
		return fmt.Errorf("--simulator-rate-block-seconds must be in %d..%d", simMinRateBlockSeconds, simMaxRateBlockSeconds)
	}
	return nil
}

func validateSimulatorTargetTxsPerBlock(value int) error {
	if value < 1 || int64(value) > simMaxTargetTxsPerBlock {
		return fmt.Errorf("--simulator-target-txs-per-block must be in 1..%d", simMaxTargetTxsPerBlock)
	}
	return nil
}

func simTargetTxPerSecond(targetTxsPerBlock, rateBlockSeconds int64) float64 {
	return float64(simNormalizeTargetTxsPerBlock(targetTxsPerBlock)) / float64(simNormalizeRateBlockSeconds(rateBlockSeconds))
}

func simEffectiveTargetTxsPerBlock(targetTxsPerBlock, partitions, partitionID int64) int64 {
	target := simNormalizeTargetTxsPerBlock(targetTxsPerBlock)
	if partitions <= 1 {
		return target
	}
	if partitionID < 0 || partitionID >= partitions {
		return 0
	}
	base := target / partitions
	if partitionID < target%partitions {
		base++
	}
	return base
}

func simEffectiveTargetTxPerSecond(targetTxsPerBlock, rateBlockSeconds int64) float64 {
	if targetTxsPerBlock <= 0 {
		return 0
	}
	return float64(targetTxsPerBlock) / float64(simNormalizeRateBlockSeconds(rateBlockSeconds))
}

func simAttemptsPerTick(targetTxsPerBlock, sharePermille int64, tick time.Duration, rateBlockSeconds int64) int {
	if targetTxsPerBlock <= 0 || sharePermille <= 0 || tick <= 0 {
		return 0
	}
	if sharePermille > simThrottleFullPermille {
		sharePermille = simThrottleFullPermille
	}
	numerator := simNormalizeTargetTxsPerBlock(targetTxsPerBlock) * sharePermille * int64(tick)
	denominator := simNormalizeRateBlockSeconds(rateBlockSeconds) * int64(time.Second) * simThrottleFullPermille
	attempts := (numerator + denominator - 1) / denominator
	if attempts < 1 {
		return 1
	}
	if attempts > 256 {
		return 256
	}
	return int(attempts)
}

func simAttemptsForBlockTarget(targetTxsPerBlock int64, tick time.Duration, rateBlockSeconds int64) int {
	if targetTxsPerBlock <= 0 || tick <= 0 {
		return 0
	}
	numerator := targetTxsPerBlock * int64(tick)
	denominator := simNormalizeRateBlockSeconds(rateBlockSeconds) * int64(time.Second)
	attempts := (numerator + denominator - 1) / denominator
	if attempts < 1 {
		return 1
	}
	if attempts > 256 {
		return 256
	}
	return int(attempts)
}

func simNormalizeWithdrawIntervalSeconds(value int64) int64 {
	if value <= 0 {
		return simDefaultWithdrawIntervalSeconds
	}
	if value < simMinWithdrawIntervalSeconds {
		return simMinWithdrawIntervalSeconds
	}
	if value > simMaxWithdrawIntervalSeconds {
		return simMaxWithdrawIntervalSeconds
	}
	return value
}

type simTxLimiter struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
}

func newSimTxLimiter(rate float64) *simTxLimiter {
	if rate <= 0 {
		rate = simTargetTxPerSecond(simDefaultTargetTxsPerBlock, simDefaultRateBlockSeconds)
	}
	burst := simLimiterBurst(rate)
	return &simTxLimiter{rate: rate, burst: burst, tokens: burst, last: time.Now()}
}

func simLimiterBurst(rate float64) float64 {
	if rate <= 0 {
		return 0
	}
	burst := rate / 10
	if burst < 8 {
		return 8
	}
	if burst > 64 {
		return 64
	}
	return burst
}

func (l *simTxLimiter) setRate(rate float64) {
	if l == nil {
		return
	}
	if rate < 0 {
		rate = simTargetTxPerSecond(simDefaultTargetTxsPerBlock, simDefaultRateBlockSeconds)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rate = rate
	l.burst = simLimiterBurst(rate)
	if l.tokens > l.burst {
		l.tokens = l.burst
	}
	if l.last.IsZero() {
		l.last = time.Now()
	}
}

func (l *simTxLimiter) allow(throttlePermille int64) bool {
	return l.allowN(throttlePermille, 1)
}

func (l *simTxLimiter) allowN(throttlePermille int64, count int) bool {
	if l == nil {
		return true
	}
	if throttlePermille <= 0 || count <= 0 {
		return false
	}
	if throttlePermille > simThrottleFullPermille {
		throttlePermille = simThrottleFullPermille
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	rate := l.rate * float64(throttlePermille) / float64(simThrottleFullPermille)
	l.tokens += now.Sub(l.last).Seconds() * rate
	l.last = now
	if l.tokens > l.burst {
		l.tokens = l.burst
	}
	needed := float64(count)
	if l.tokens < needed {
		return false
	}
	l.tokens -= needed
	return true
}

func simThrottleByInt(value, soft, hard int) int64 {
	if hard <= soft {
		if value > soft {
			return 0
		}
		return simThrottleFullPermille
	}
	if value <= soft {
		return simThrottleFullPermille
	}
	if value >= hard {
		return 0
	}
	scale := simThrottleFullPermille - int64(value-soft)*(simThrottleFullPermille-simThrottleMinPermille)/int64(hard-soft)
	if scale < simThrottleMinPermille {
		return simThrottleMinPermille
	}
	return scale
}

func simThrottleByUsage(value, soft, hard float64) int64 {
	if hard <= soft {
		if value > soft {
			return 0
		}
		return simThrottleFullPermille
	}
	if value <= soft {
		return simThrottleFullPermille
	}
	if value >= hard {
		return 0
	}
	scale := simThrottleFullPermille - int64((value-soft)*(float64(simThrottleFullPermille-simThrottleMinPermille))/(hard-soft))
	if scale < simThrottleMinPermille {
		return simThrottleMinPermille
	}
	return scale
}

func simMinThrottle(values ...int64) int64 {
	out := simThrottleFullPermille
	for _, v := range values {
		if v < out {
			out = v
		}
	}
	return out
}

type simRechargeAsset struct {
	Chain          string
	Token          string
	NativeContract string
	Decimals       uint32
	Amount         string
	WithdrawAmount string
	Fee            string
}

                                                                           
                                                                         
                                           
var defaultSimExternalAssets = []struct {
	Chain, Contract string
	Decimals        uint32
}{
	{Chain: "btc", Decimals: 8},
	{Chain: "eth", Decimals: 18},
	{Chain: "bnb", Decimals: 18},
	{Chain: "base", Decimals: 18},
	{Chain: "arb", Decimals: 18},
	{Chain: "trx", Decimals: 6},
	{Chain: "eth", Contract: "0xdAC17F958D2ee523a2206206994597C13D831ec7", Decimals: 6},
	{Chain: "eth", Contract: "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48", Decimals: 6},
	{Chain: "eth", Contract: "0x1f9840a85d5aF5bf1D1762F925BDADdC4201F984", Decimals: 18},
	{Chain: "bnb", Contract: "0x55d398326f99059fF775485246999027B3197955", Decimals: 18},
	{Chain: "base", Contract: "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913", Decimals: 6},
	{Chain: "arb", Contract: "0xaf88d065e77c8cC2239327C5EDb3A432268e5831", Decimals: 6},
	{Chain: "trx", Contract: "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t", Decimals: 6},
}

type simTradingPair struct {
	BaseToken     string
	QuoteToken    string
	BaseDecimals  uint32
	QuoteDecimals uint32
	MinPrice      int64
	MaxPrice      int64
	MinAmount     int64
	MaxAmount     int64
}

type simPendingSpotCross struct {
	node      *NodeInstance
	pair      simTradingPair
	holdID    string
	amount    *big.Int
	buyPrice  *big.Int
	sellOrder simTrackedSpotOrder
	buyOrder  simTrackedSpotOrder
}

                                                                           
                                                                        
                                                                      
type simPendingFutureCross struct {
	node     *NodeInstance
	pair     simTradingPair
	holdID   string
	amount   *big.Int
	buyPrice *big.Int
	leverage uint64
}

type simTrackedSpotOrder struct {
	orderID     string
	nonce       uint64
	cancelID    string
	cancelNonce uint64
	terminal    bool
}

type simSpotLiquidityState struct {
	node          *NodeInstance
	pair          simTradingPair
	buyOrders     []simTrackedSpotOrder
	sellOrders    []simTrackedSpotOrder
	lastReconcile time.Time
}

func (p simTradingPair) pairKey() string {
	return utils.GeneratePairKey(p.BaseToken, p.QuoteToken)
}

func (p simTradingPair) randomPrice() *big.Int {
	return simEncodePrice(simRandRange(p.MinPrice, p.MaxPrice), p.BaseDecimals, p.QuoteDecimals)
}

func (p simTradingPair) randomAmount() *big.Int {
	return simEncodeAmount(simRandRange(p.MinAmount, p.MaxAmount), p.BaseDecimals)
}

func (p simTradingPair) crossedPrices() (sellPrice, buyPrice *big.Int) {
	return p.crossedPricesAtOffsetBPS(0)
}

                                                                          
                                                                             
                                                                           
                                 
func (p simTradingPair) crossedPricesAtOffsetBPS(offsetBPS int64) (sellPrice, buyPrice *big.Int) {
	if offsetBPS > simCrossPriceWalkBoundBPS {
		offsetBPS = simCrossPriceWalkBoundBPS
	} else if offsetBPS < -simCrossPriceWalkBoundBPS {
		offsetBPS = -simCrossPriceWalkBoundBPS
	}
	midPrice := p.seedPrice()
	bpsScale := big.NewInt(10_000)
	midPrice.Mul(midPrice, big.NewInt(10_000+offsetBPS))
	midPrice.Quo(midPrice, bpsScale)
	sellPrice = new(big.Int).Mul(midPrice, big.NewInt(10_000-simSpotCrossHalfSpreadBPS))
	sellPrice.Quo(sellPrice, bpsScale)
	buyPrice = new(big.Int).Mul(midPrice, big.NewInt(10_000+simSpotCrossHalfSpreadBPS))
	buyPrice.Quo(buyPrice, bpsScale)
	return sellPrice, buyPrice
}

                                                                              
                                                                              
                                                                            
func simNextCrossPriceOffsetBPS(current int64) int64 {
	if current > simCrossPriceWalkBoundBPS {
		current = simCrossPriceWalkBoundBPS
	} else if current < -simCrossPriceWalkBoundBPS {
		current = -simCrossPriceWalkBoundBPS
	}
	step := int64(1) + mrand.Int63n(simCrossPriceWalkMaxStepBPS)
	if mrand.Intn(2) == 0 {
		step = -step
	}
	next := current + step
	if next > simCrossPriceWalkBoundBPS {
		next = simCrossPriceWalkBoundBPS - (next - simCrossPriceWalkBoundBPS)
	} else if next < -simCrossPriceWalkBoundBPS {
		next = -simCrossPriceWalkBoundBPS + (-simCrossPriceWalkBoundBPS - next)
	}
	if next == current {
		if current >= 0 {
			return current - 1
		}
		return current + 1
	}
	return next
}

                                                                              
                                                                        
                                                   
func (p simTradingPair) restingPrices(level int) (buyPrice, sellPrice *big.Int) {
	if level < 0 {
		level = 0
	}
	midPrice := p.seedPrice()
	spreadBPS := simSpotLiquidityBaseHalfSpreadBPS + int64(level)*simSpotLiquidityStepBPS
	if spreadBPS >= 10_000 {
		return new(big.Int), new(big.Int)
	}
	bpsScale := big.NewInt(10_000)
	buyPrice = new(big.Int).Mul(midPrice, big.NewInt(10_000-spreadBPS))
	buyPrice.Quo(buyPrice, bpsScale)
	sellPrice = new(big.Int).Mul(midPrice, big.NewInt(10_000+spreadBPS))
	sellPrice.Quo(sellPrice, bpsScale)
	return buyPrice, sellPrice
}

func capSimSpotBuyAmount(requested, price, availableQuote *big.Int) *big.Int {
	if requested == nil || price == nil || availableQuote == nil ||
		requested.Sign() <= 0 || price.Sign() <= 0 || availableQuote.Sign() <= 0 {
		return new(big.Int)
	}
	quoteBudget := new(big.Int).Mul(availableQuote, big.NewInt(simSpotBuyAvailableBPS))
	quoteBudget.Quo(quoteBudget, big.NewInt(10_000))
	maxAmount := new(big.Int).Mul(quoteBudget, vm.PriceScale)
	maxAmount.Quo(maxAmount, price)
	if maxAmount.Cmp(requested) >= 0 {
		return new(big.Int).Set(requested)
	}
	return maxAmount
}

func capSimSpotSellAmount(requested, availableBase *big.Int) *big.Int {
	if requested == nil || availableBase == nil || requested.Sign() <= 0 || availableBase.Sign() <= 0 {
		return new(big.Int)
	}
	baseBudget := new(big.Int).Mul(availableBase, big.NewInt(simSpotBuyAvailableBPS))
	baseBudget.Quo(baseBudget, big.NewInt(10_000))
	if baseBudget.Cmp(requested) >= 0 {
		return new(big.Int).Set(requested)
	}
	return baseBudget
}

func finalizedSimAvailableBalance(node *NodeInstance, token string) (*big.Int, bool) {
	if node == nil || node.DBManager == nil || strings.TrimSpace(node.Address) == "" || strings.TrimSpace(token) == "" {
		return nil, false
	}
	raw, err := node.DBManager.Get(keys.KeyBalance(node.Address, token))
	if err != nil || len(raw) == 0 {
		return nil, false
	}
	var record pb.TokenBalanceRecord
	if proto.Unmarshal(raw, &record) != nil || record.Balance == nil {
		return nil, false
	}
	available, ok := new(big.Int).SetString(strings.TrimSpace(record.Balance.Balance), 10)
	if !ok || available.Sign() < 0 {
		return nil, false
	}
	return available, true
}

func (p simTradingPair) seedPrice() *big.Int {
	displayPrice := p.MinPrice + (p.MaxPrice-p.MinPrice)/2
	return simEncodePrice(displayPrice, p.BaseDecimals, p.QuoteDecimals)
}

func (p simTradingPair) seedAmount() *big.Int {
	displayAmount := p.MinAmount + (p.MaxAmount-p.MinAmount)/2
	return simEncodeAmount(displayAmount, p.BaseDecimals)
}

func (p simTradingPair) hasValidEncoding() bool {
	minPrice := simEncodePrice(p.MinPrice, p.BaseDecimals, p.QuoteDecimals)
	maxPrice := simEncodePrice(p.MaxPrice, p.BaseDecimals, p.QuoteDecimals)
	minAmount := simEncodeAmount(p.MinAmount, p.BaseDecimals)
	maxAmount := simEncodeAmount(p.MaxAmount, p.BaseDecimals)
	return minPrice.Sign() > 0 && maxPrice.Cmp(minPrice) >= 0 &&
		minAmount.Sign() > 0 && maxAmount.Cmp(minAmount) >= 0
}

                                                                                 
                                                                                 
                                                                        
const simMaxTokenDecimals = uint32(255)

func simPow10(decimals uint32) *big.Int {
	if decimals > simMaxTokenDecimals {
		return nil
	}
	return new(big.Int).Exp(big.NewInt(10), new(big.Int).SetUint64(uint64(decimals)), nil)
}

                                                                             
                                  
func simEncodeAmount(displayAmount int64, decimals uint32) *big.Int {
	if displayAmount <= 0 {
		return new(big.Int)
	}
	scale := simPow10(decimals)
	if scale == nil {
		return new(big.Int)
	}
	encoded := new(big.Int).Mul(big.NewInt(displayAmount), scale)
	return encoded.Quo(encoded, big.NewInt(simUnit))
}

                                                                             
                                       
func simEncodePrice(displayPrice int64, baseDecimals, quoteDecimals uint32) *big.Int {
	if displayPrice <= 0 {
		return new(big.Int)
	}
	encoded := big.NewInt(displayPrice)
	if quoteDecimals >= baseDecimals {
		scale := simPow10(quoteDecimals - baseDecimals)
		if scale == nil {
			return new(big.Int)
		}
		return encoded.Mul(encoded, scale)
	}
	scale := simPow10(baseDecimals - quoteDecimals)
	if scale == nil {
		return new(big.Int)
	}
	return encoded.Quo(encoded, scale)
}

func simRandRange(min, max int64) int64 {
	if max <= min {
		return min
	}
	return min + mrand.Int63n(max-min+1)
}

func simRandomTransferAmount() string {
	return strconv.FormatInt(simTransferAmountChoices[mrand.Intn(len(simTransferAmountChoices))], 10)
}

func NewTxSimulator(nodes []*NodeInstance, frostCfg config.FrostConfig, witnessCfg config.WitnessConfig, genesisCfg *config.GenesisConfig) *TxSimulator {
	chains := make([]string, 0, len(frostCfg.Chains))
	for name := range frostCfg.Chains {
		chains = append(chains, frostchain.NormalizeChain(name))
	}
	rechargeAssets := buildSimRechargeAssets(frostCfg, genesisCfg)
	tradingPairs := buildSimTradingPairs(genesisCfg)
	witnessStakeAmount := strings.TrimSpace(witnessCfg.MinStakeAmount)
	if witnessStakeAmount == "" {
		witnessStakeAmount = config.DefaultWitnessConfig().MinStakeAmount
	}
	sim := &TxSimulator{
		nodes:               nodes,
		nonces:              make(map[string]uint64),
		protocolNonces:      make(map[string]uint64),
		nonceLocks:          make(map[string]*sync.Mutex),
		capitalHolds:        make(map[string]simCapitalHold),
		capitalHeldTotals:   make(map[string]map[string]*big.Int),
		capitalPending:      make(map[string]map[uint64]simCapitalReservation),
		capitalPendingTotal: make(map[string]map[string]*big.Int),
		capitalFinalized:    make(map[string]uint64),
		submitLimiter:       newSimTxLimiter(simTargetTxPerSecond(simDefaultTargetTxsPerBlock, simDefaultRateBlockSeconds)),
		chains:              chains,
		btcNetwork:          simBTCNetwork(frostCfg),
		rechargeAssets:      rechargeAssets,
		withdrawAssets:      buildSimWithdrawAssets(rechargeAssets),
		tradingPairs:        tradingPairs,
		witnessStakeAmount:  witnessStakeAmount,
		remoteClient: &http.Client{
			Timeout: 800 * time.Millisecond,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
	}
	sim.submitThrottle.Store(simThrottleFullPermille)
	sim.targetTxsPerBlock.Store(simDefaultTargetTxsPerBlock)
	sim.targetPartitions.Store(1)
	sim.targetPartitionID.Store(0)
	sim.rateBlockSeconds.Store(simDefaultRateBlockSeconds)
	sim.withdrawInterval.Store(simDefaultWithdrawIntervalSeconds)
	return sim
}

func simBTCNetwork(frostCfg config.FrostConfig) string {
	for name, chainCfg := range frostCfg.Chains {
		if frostchain.NormalizeChain(name) == frostchain.ChainBTC {
			if network := strings.TrimSpace(chainCfg.BTCNetwork); network != "" {
				return network
			}
		}
	}
	return config.DefaultFrostConfig().Chains[frostchain.ChainBTC].BTCNetwork
}

func (s *TxSimulator) setTargetTxsPerBlock(value int64) {
	target := simNormalizeTargetTxsPerBlock(value)
	s.targetTxsPerBlock.Store(target)
	s.refreshSubmitLimiter()
}

func (s *TxSimulator) setTargetPartition(nodeID, partitions int) {
	if partitions <= 0 {
		partitions = 1
	}
	s.targetPartitionID.Store(int64(nodeID))
	s.targetPartitions.Store(int64(partitions))
	s.refreshSubmitLimiter()
}

func (s *TxSimulator) effectiveTargetTxsPerBlock() int64 {
	if s == nil {
		return 0
	}
	return simEffectiveTargetTxsPerBlock(s.targetTxsPerBlock.Load(), s.targetPartitions.Load(), s.targetPartitionID.Load())
}

func (s *TxSimulator) effectiveTransferTargetTxsPerBlock() int64 {
	if s == nil {
		return 0
	}
	return simEffectiveTargetTxsPerBlock(simTransferTargetTxsPerBlock, s.targetPartitions.Load(), s.targetPartitionID.Load())
}

func (s *TxSimulator) setRateBlockSeconds(value int64) {
	if s == nil {
		return
	}
	s.rateBlockSeconds.Store(simNormalizeRateBlockSeconds(value))
	s.refreshSubmitLimiter()
}

func (s *TxSimulator) rateBlockSecondsValue() int64 {
	if s == nil {
		return simDefaultRateBlockSeconds
	}
	return simNormalizeRateBlockSeconds(s.rateBlockSeconds.Load())
}

func (s *TxSimulator) targetTxsPerSecond() int64 {
	if s == nil {
		return 0
	}
	return int64(simTargetTxPerSecond(s.targetTxsPerBlock.Load(), s.rateBlockSecondsValue()))
}

func (s *TxSimulator) localTargetTxsPerSecond() int64 {
	if s == nil {
		return 0
	}
	return int64(simEffectiveTargetTxPerSecond(s.effectiveTargetTxsPerBlock(), s.rateBlockSecondsValue()))
}

func (s *TxSimulator) refreshSubmitLimiter() {
	if s == nil || s.submitLimiter == nil {
		return
	}
	s.submitLimiter.setRate(simEffectiveTargetTxPerSecond(s.effectiveTargetTxsPerBlock(), s.rateBlockSecondsValue()))
}

func (s *TxSimulator) setWithdrawIntervalSeconds(value int64) {
	s.withdrawInterval.Store(simNormalizeWithdrawIntervalSeconds(value))
}

func (s *TxSimulator) withdrawIntervalDuration() time.Duration {
	return time.Duration(simNormalizeWithdrawIntervalSeconds(s.withdrawInterval.Load())) * time.Second
}

func buildSimTradingPairs(_ *config.GenesisConfig) []simTradingPair {
	                                                                         
	                                                            
	tokenDecimals := map[string]uint32{"FB": 8}
	contractAssets := make([]string, 0, len(defaultSimExternalAssets))
	profiles := []struct {
		base                 string
		minPrice, maxPrice   int64
		minAmount, maxAmount int64
	}{
		{base: "FB", minPrice: 10 * simUnit, maxPrice: 20 * simUnit, minAmount: simUnit, maxAmount: 10 * simUnit},
	}
	for _, configured := range defaultSimExternalAssets {
		identity, err := bridgeasset.Resolve(configured.Chain, configured.Contract)
		if err != nil {
			continue
		}
		tokenDecimals[identity.ID] = configured.Decimals
		if identity.Contract != "" {
			contractAssets = append(contractAssets, identity.ID)
			continue
		}
		profiles = append(profiles, struct {
			base                 string
			minPrice, maxPrice   int64
			minAmount, maxAmount int64
		}{base: identity.ID, minPrice: simUnit, maxPrice: 100 * simUnit, minAmount: simUnit / 100, maxAmount: 10 * simUnit})
	}
	pairs := make([]simTradingPair, 0, len(profiles)*len(contractAssets))
	for _, profile := range profiles {
		baseDecimals, hasBase := tokenDecimals[profile.base]
		if !hasBase {
			continue
		}
		for _, quote := range contractAssets {
			quoteDecimals, hasQuote := tokenDecimals[quote]
			if !hasQuote || quote == profile.base {
				continue
			}
			pair := simTradingPair{
				BaseToken: profile.base, QuoteToken: quote,
				BaseDecimals: baseDecimals, QuoteDecimals: quoteDecimals,
				MinPrice: profile.minPrice, MaxPrice: profile.maxPrice,
				MinAmount: profile.minAmount, MaxAmount: profile.maxAmount,
			}
			if !pair.hasValidEncoding() {
				continue
			}
			pairs = append(pairs, pair)
		}
	}
	return pairs
}

func simPositiveBalance(raw string) bool {
	v, ok := new(big.Int).SetString(strings.TrimSpace(raw), 10)
	return ok && v.Sign() > 0
}

func buildSimRechargeAssets(frostCfg config.FrostConfig, _ *config.GenesisConfig) []simRechargeAsset {
	chainSet := make(map[string]bool, len(frostCfg.Chains))
	for name := range frostCfg.Chains {
		chainName := frostchain.NormalizeChain(name)
		if chainName != "" {
			chainSet[chainName] = true
		}
	}
	seen := make(map[string]bool)
	assets := make([]simRechargeAsset, 0, len(defaultSimExternalAssets))
	for _, configured := range defaultSimExternalAssets {
		identity, err := bridgeasset.Resolve(configured.Chain, configured.Contract)
		if err != nil || !chainSet[identity.Chain] {
			continue
		}
		key := identity.ID
		if seen[key] {
			continue
		}
		seen[key] = true
		assets = append(assets, simRechargeAsset{
			Chain:          identity.Chain,
			Token:          identity.ID,
			NativeContract: identity.Contract,
			Decimals:       configured.Decimals,
			Amount:         simRechargeAmount(configured.Decimals),
			WithdrawAmount: simDefaultWithdrawRawAmount,
			Fee:            "5",
		})
	}
	return assets
}

                                                                         
                                                                               
                                                                               
                                                 
func simRechargeAmount(decimals uint32) string {
	scale := simPow10(decimals)
	if scale == nil {
		return strconv.FormatInt(simRechargeWholeAmount, 10)
	}
	return scale.Mul(scale, big.NewInt(simRechargeWholeAmount)).String()
}

func buildSimWithdrawAssets(rechargeAssets []simRechargeAsset) []simRechargeAsset {
	assets := make([]simRechargeAsset, 0, len(rechargeAssets))
	for _, asset := range rechargeAssets {
		if simWithdrawSupportedChain(asset.Chain) {
			assets = append(assets, asset)
		}
	}
	return assets
}

func simWithdrawSupportedChain(chainName string) bool {
	switch frostchain.NormalizeChain(chainName) {
	case frostchain.ChainBTC, frostchain.ChainETH, frostchain.ChainBNB, frostchain.ChainBASE, frostchain.ChainARB:
		return true
	default:
		return false
	}
}

func simWithdrawAmountForAsset(asset simRechargeAsset) *big.Int {
	if raw := strings.TrimSpace(asset.WithdrawAmount); raw != "" {
		amount, ok := new(big.Int).SetString(raw, 10)
		if ok && amount.Sign() > 0 {
			return amount
		}
		return big.NewInt(50)
	}

	                                                             
	amount, ok := new(big.Int).SetString(strings.TrimSpace(asset.Amount), 10)
	if !ok || amount.Sign() <= 0 {
		return big.NewInt(50)
	}
	withdraw := new(big.Int).Mul(amount, big.NewInt(simWithdrawAmountPermille))
	withdraw.Div(withdraw, big.NewInt(simThrottleFullPermille))
	if withdraw.Sign() <= 0 {
		return big.NewInt(1)
	}
	return withdraw
}

type simWithdrawQueueReader interface {
	Get(key string) ([]byte, error)
}

func (s *TxSimulator) simWithdrawQueueReader() simWithdrawQueueReader {
	if s == nil {
		return nil
	}
	for _, node := range s.nodes {
		if node != nil && node.DBManager != nil {
			return node.DBManager
		}
	}
	return nil
}

func simParseUint64Bytes(raw []byte) uint64 {
	v, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return 0
	}
	return v
}

func (s *TxSimulator) simOpenWithdrawQueueLen() (uint64, bool) {
	reader := s.simWithdrawQueueReader()
	if reader == nil {
		return 0, false
	}
	seen := make(map[string]bool, len(s.withdrawAssets))
	var total uint64
	for _, asset := range s.withdrawAssets {
		chain := frostchain.NormalizeChain(asset.Chain)
		token := strings.ToUpper(strings.TrimSpace(asset.Token))
		key := chain + ":" + token
		if chain == "" || token == "" || seen[key] {
			continue
		}
		seen[key] = true
		seqRaw, err := reader.Get(keys.KeyFrostWithdrawFIFOSeq(chain, token))
		if err != nil {
			continue
		}
		seq := simParseUint64Bytes(seqRaw)
		if seq == 0 {
			continue
		}
		head := uint64(1)
		if headRaw, err := reader.Get(keys.KeyFrostWithdrawFIFOHead(chain, token)); err == nil {
			if parsed := simParseUint64Bytes(headRaw); parsed > 0 {
				head = parsed
			}
		}
		if seq >= head {
			total += seq - head + 1
		}
	}
	return total, true
}

func (s *TxSimulator) shouldBackpressureSimWithdrawQueue() (bool, uint64, bool) {
	openCount, ok := s.simOpenWithdrawQueueLen()
	return ok && openCount >= simWithdrawQueueBackpressureLimit, openCount, ok
}

func (s *TxSimulator) getNextNonce(addr string) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.nonces == nil {
		s.nonces = make(map[string]uint64)
	}
	nonce, ok := s.nonces[addr]
	if !ok {
		nonce = s.loadAccountNonce(addr)
	}
	s.nonces[addr] = nonce + 1
	return nonce
}

func (s *TxSimulator) getNextProtocolNonce(addr string) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.protocolNonces == nil {
		s.protocolNonces = make(map[string]uint64)
	}
	nonce, ok := s.protocolNonces[addr]
	if !ok {
		nonce = s.loadAccountNonce(addr)
	}
	s.protocolNonces[addr] = nonce + 1
	return nonce
}

func (s *TxSimulator) loadAccountNonce(addr string) uint64 {
	if addr == "" {
		return 0
	}
	for _, node := range s.nodes {
		if node == nil || node.DBManager == nil {
			continue
		}
		if data, err := node.DBManager.Get(keys.KeyAccount(addr)); err == nil && len(data) > 0 {
			var acc pb.Account
			if proto.Unmarshal(data, &acc) == nil {
				return acc.Nonce
			}
		}
	}
	return 0
}

func (s *TxSimulator) nonceLock(addr string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.nonceLocks == nil {
		s.nonceLocks = make(map[string]*sync.Mutex)
	}
	lock := s.nonceLocks[addr]
	if lock == nil {
		lock = &sync.Mutex{}
		s.nonceLocks[addr] = lock
	}
	return lock
}

func (s *TxSimulator) releaseLatestNonce(addr string, nonce uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.nonces != nil && s.nonces[addr] == nonce+1 {
		s.nonces[addr] = nonce
	}
}

func (s *TxSimulator) releaseLatestProtocolNonce(addr string, nonce uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.protocolNonces != nil && s.protocolNonces[addr] == nonce+1 {
		s.protocolNonces[addr] = nonce
	}
}

func (s *TxSimulator) Start() {
	if s == nil {
		logs.Error("SIMULATOR: TxSimulator is nil, skip start")
		return
	}
	go s.monitorTxPool()                         
	go s.runRandomTransfers()
	go s.runWitnessRegistration(context.Background())                         
	go s.runRechargeScenario()                                                
	go s.runWitnessVoteWorker()                                                  
	go s.runWithdrawScenario()
	go s.runOrderScenario()                            
	go s.runFutureOrderScenario()                      
	go s.runArbitrationWorker()                        
}

func isActiveSimNode(node *NodeInstance) bool {
	return node != nil && node.TxPool != nil && node.ConsensusManager != nil && !node.ConsensusManager.IsPaused()
}

func (s *TxSimulator) monitorTxPool() {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	lastThrottleBucket := int64(-1)
	lastRemoteProbe := time.Time{}
	remotePressure := simRemotePressure{maxPendingNode: -1, lagNode: -1}

	for range ticker.C {
		if len(s.nodes) == 0 {
			continue
		}

		maxPending := 0
		maxPendingNode := -1
		maxQueueUsage := 0.0
		maxQueueNode := -1
		maxSendQueueUsage := 0.0
		maxSendQueueNode := -1
		maxSendQueueName := ""
		maxPendingLimit := 5000
		activeNodes := 0
		localCatchUp := false
		localCatchUpNode := -1
		localCatchUpReason := ""
		localCatchUpLag := uint64(0)

		for i, node := range s.nodes {
			if !isActiveSimNode(node) {
				continue
			}
			activeNodes++
			s.recoverSimAccount(node)
			if node.ConsensusManager != nil {
				pressure := node.ConsensusManager.SyncPressureState()
				if pressure.CatchUp && (!localCatchUp || pressure.Lag >= localCatchUpLag) {
					localCatchUp = true
					localCatchUpNode = i
					localCatchUpReason = fmt.Sprintf("%s lag=%d local=%d target=%d", pressure.Reason, pressure.Lag, pressure.LocalHeight, pressure.TargetHeight)
					localCatchUpLag = pressure.Lag
				}
			}
			if node.TxPool != nil {
				if limit := node.TxPool.MaxPendingLimit(); limit > 0 {
					maxPendingLimit = limit
				}

				pending := node.TxPool.PendingLen()
				if pending > maxPending {
					maxPending = pending
					maxPendingNode = i
				}

				qLen, qCap := node.TxPool.QueueDepth()
				if qCap > 0 {
					usage := float64(qLen) / float64(qCap)
					if usage > maxQueueUsage {
						maxQueueUsage = usage
						maxQueueNode = i
					}
				}
			}
			if node.SenderManager != nil && node.SenderManager.SendQueue != nil {
				for _, stat := range node.SenderManager.SendQueue.GetChannelStats() {
					if stat.Cap > 0 && stat.Usage > maxSendQueueUsage {
						maxSendQueueUsage = stat.Usage
						maxSendQueueNode = i
						maxSendQueueName = stat.Name
					}
				}
			}
		}
		if activeNodes == 0 {
			continue
		}
		if time.Since(lastRemoteProbe) >= simRemoteProbeInterval {
			remotePressure = s.probeRemotePressure()
			lastRemoteProbe = time.Now()
		}

		if maxPendingLimit <= 0 {
			maxPendingLimit = 5000
		}
		pendingSoft := maxPendingLimit / 5
		if pendingSoft < 500 {
			pendingSoft = 500
		}
		pendingHard := (maxPendingLimit * 4) / 5
		if pendingHard <= pendingSoft {
			pendingHard = pendingSoft + 500
		}
		queueSoft := 0.25
		queueHard := 0.80
		sendQueueSoft := 0.25
		sendQueueHard := 0.80

		remotePending := 0
		remoteLag := 0
		if remotePressure.ok {
			remotePending = remotePressure.maxPending
			remoteLag = int(remotePressure.maxLag)
		}
		catchUpThrottle := simThrottleFullPermille
		if localCatchUp || remotePressure.catchUp {
			catchUpThrottle = 0
		}
		throttle := simMinThrottle(
			catchUpThrottle,
			simThrottleByInt(maxPending, pendingSoft, pendingHard),
			simThrottleByInt(remotePending, pendingSoft, pendingHard),
			simThrottleByInt(remoteLag, 3, 12),
			simThrottleByUsage(maxQueueUsage, queueSoft, queueHard),
			simThrottleByUsage(maxSendQueueUsage, sendQueueSoft, sendQueueHard),
		)
		s.submitThrottle.Store(throttle)
		if bucket := throttle / 100; bucket != lastThrottleBucket {
			logs.Info(
				"SIMULATOR: tx throttle=%d%% (activeNodes=%d/%d, localCatchUp=%v@node=%d/%s remoteCatchUp=%v@node=%d/%s, maxPending=%d@node=%d remotePending=%d@node=%d remoteLag=%d@node=%d remoteActive=%d soft=%d hard=%d limit=%d, maxTxPoolQueue=%.0f%%@node=%d, maxSendQueue=%.0f%%@node=%d/%s)",
				throttle/10, activeNodes, len(s.nodes), localCatchUp, localCatchUpNode, localCatchUpReason, remotePressure.catchUp, remotePressure.catchUpNode, remotePressure.catchUpReason, maxPending, maxPendingNode, remotePending, remotePressure.maxPendingNode, remoteLag, remotePressure.lagNode, remotePressure.active, pendingSoft, pendingHard, maxPendingLimit, maxQueueUsage*100, maxQueueNode, maxSendQueueUsage*100, maxSendQueueNode, maxSendQueueName,
			)
			lastThrottleBucket = bucket
		}

		if throttle == 0 {
			if !s.paused.Load() {
				logs.Warn(
					"SIMULATOR: hard tx pressure detected (localCatchUp=%v@node=%d/%s remoteCatchUp=%v@node=%d/%s, maxPending=%d@node=%d remotePending=%d@node=%d remoteLag=%d@node=%d hard=%d limit=%d, maxTxPoolQueue=%.0f%%@node=%d, maxSendQueue=%.0f%%@node=%d/%s), PAUSING tx generation",
					localCatchUp, localCatchUpNode, localCatchUpReason, remotePressure.catchUp, remotePressure.catchUpNode, remotePressure.catchUpReason, maxPending, maxPendingNode, remotePending, remotePressure.maxPendingNode, remoteLag, remotePressure.lagNode, pendingHard, maxPendingLimit, maxQueueUsage*100, maxQueueNode, maxSendQueueUsage*100, maxSendQueueNode, maxSendQueueName,
				)
				s.paused.Store(true)
			}
		} else if throttle >= simThrottleMinPermille {
			if s.paused.Load() {
				logs.Info(
					"SIMULATOR: hard tx pressure relieved (localCatchUp=%v remoteCatchUp=%v maxPending=%d remotePending=%d remoteLag=%d hard=%d limit=%d, maxTxPoolQueue=%.0f%%, maxSendQueue=%.0f%%), RESUMING tx generation",
					localCatchUp, remotePressure.catchUp, maxPending, remotePending, remoteLag, pendingHard, maxPendingLimit, maxQueueUsage*100, maxSendQueueUsage*100,
				)
				s.paused.Store(false)
			}
		}
	}
}

func (s *TxSimulator) probeRemotePressure() simRemotePressure {
	if s == nil || s.remoteClient == nil {
		return simRemotePressure{maxPendingNode: -1, lagNode: -1}
	}
	type result struct {
		nodeID  int
		height  uint64
		pending int
		syncing bool
		ok      bool
	}
	seen := make(map[string]struct{}, len(s.nodes))
	ch := make(chan result, len(s.nodes))
	total := 0
	for _, node := range s.nodes {
		if !isActiveSimNode(node) {
			continue
		}
		url := simHeightQueryURL(node.AdvertiseAddr)
		if url == "" {
			continue
		}
		if _, dup := seen[url]; dup {
			continue
		}
		seen[url] = struct{}{}
		total++
		go func(nodeID int, url string) {
			resp, err := s.remoteClient.Get(url)
			if err != nil {
				ch <- result{}
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				ch <- result{}
				return
			}
			body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			if err != nil {
				ch <- result{}
				return
			}
			var height pb.HeightResponse
			if err := proto.Unmarshal(body, &height); err != nil {
				ch <- result{}
				return
			}
			ch <- result{nodeID: nodeID, height: height.LastAcceptedHeight, pending: int(height.PendingTxCount), syncing: height.IsSyncing, ok: true}
		}(node.ID, url)
	}
	if total == 0 {
		return simRemotePressure{maxPendingNode: -1, lagNode: -1}
	}
	out := simRemotePressure{maxPendingNode: -1, lagNode: -1, catchUpNode: -1}
	var maxHeight uint64
	var minHeight uint64
	for i := 0; i < total; i++ {
		r := <-ch
		if !r.ok {
			continue
		}
		out.active++
		if r.pending > out.maxPending {
			out.maxPending = r.pending
			out.maxPendingNode = r.nodeID
		}
		if r.syncing && !out.catchUp {
			out.catchUp = true
			out.catchUpNode = r.nodeID
			out.catchUpReason = "syncing"
		}
		if out.active == 1 || r.height < minHeight {
			minHeight = r.height
			out.lagNode = r.nodeID
		}
		if r.height > maxHeight {
			maxHeight = r.height
		}
	}
	if out.active == 0 {
		return out
	}
	out.ok = true
	out.maxLag = maxHeight - minHeight
	return out
}

func simHeightQueryURL(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return strings.TrimRight(addr, "/") + "/heightquery"
	}
	return "https://" + strings.TrimRight(addr, "/") + "/heightquery"
}

type simSubmitOptions struct {
	bypassLimiter     bool
	skipAdmissionGate bool
	protocolNonce     bool
}

func (s *TxSimulator) submitTx(node *NodeInstance, tx *pb.AnyTx) bool {
	return s.submitTxWithOptions(node, tx, simSubmitOptions{})
}

func (s *TxSimulator) submitProtocolTx(node *NodeInstance, tx *pb.AnyTx) bool {
	return s.submitTxWithOptions(node, tx, simSubmitOptions{bypassLimiter: true})
}

func (s *TxSimulator) canSubmitNow(opts simSubmitOptions) bool {
	return s.canSubmitNowN(opts, 1)
}

func (s *TxSimulator) canSubmitNowN(opts simSubmitOptions, count int) bool {
	if opts.skipAdmissionGate {
		return true
	}
	if s.paused.Load() {
		return false
	}
	throttle := s.submitThrottle.Load()
	if throttle <= 0 {
		return false
	}
	return opts.bypassLimiter || s.submitLimiter.allowN(throttle, count)
}

func (s *TxSimulator) submitGeneratedTx(node *NodeInstance, build func(uint64) *pb.AnyTx) (uint64, *pb.AnyTx, bool) {
	return s.submitGeneratedTxWithOptions(node, simSubmitOptions{}, build)
}

func (s *TxSimulator) submitGeneratedPriorityTx(node *NodeInstance, build func(uint64) *pb.AnyTx) (uint64, *pb.AnyTx, bool) {
	return s.submitGeneratedTxWithOptions(node, simSubmitOptions{bypassLimiter: true}, build)
}

func (s *TxSimulator) submitGeneratedCleanupTx(node *NodeInstance, build func(uint64) *pb.AnyTx) (uint64, *pb.AnyTx, bool) {
	return s.submitGeneratedTxWithOptions(node, simSubmitOptions{bypassLimiter: true, skipAdmissionGate: true}, build)
}

func (s *TxSimulator) submitGeneratedProtocolTx(node *NodeInstance, build func(uint64) *pb.AnyTx) (uint64, *pb.AnyTx, bool) {
	return s.submitGeneratedTxWithOptions(node, simSubmitOptions{bypassLimiter: true, protocolNonce: true}, build)
}

func (s *TxSimulator) submitGeneratedTxWithOptions(node *NodeInstance, opts simSubmitOptions, build func(uint64) *pb.AnyTx) (uint64, *pb.AnyTx, bool) {
	if node == nil || build == nil || !isActiveSimNode(node) {
		return 0, nil, false
	}
	lock := s.nonceLock(node.Address)
	lock.Lock()
	defer lock.Unlock()
	return s.submitGeneratedTxWithOptionsLocked(node, opts, build)
}

func (s *TxSimulator) submitGeneratedTxWithOptionsLocked(node *NodeInstance, opts simSubmitOptions, build func(uint64) *pb.AnyTx) (uint64, *pb.AnyTx, bool) {
	if !s.canSubmitNow(opts) {
		return 0, nil, false
	}
	usesAccountNonce := !opts.protocolNonce
	nonce := uint64(0)
	var tx *pb.AnyTx
	if opts.protocolNonce {
		nonce = s.getNextProtocolNonce(node.Address)
		tx = build(nonce)
		if tx == nil {
			s.releaseLatestProtocolNonce(node.Address, nonce)
			return nonce, nil, false
		}
		if pb.TxRequiresAccountNonce(tx) {
			s.releaseLatestProtocolNonce(node.Address, nonce)
			logs.Warn("Simulator: account-nonce tx submitted through protocol nonce path: %T", tx.GetContent())
			return nonce, tx, false
		}
	} else {
		if !s.prepareSimAccount(node) {
			return 0, nil, false
		}
		nonce = s.getNextNonce(node.Address)
		tx = build(nonce)
		if tx == nil {
			s.releaseLatestNonce(node.Address, nonce)
			return nonce, nil, false
		}
		if !pb.TxRequiresAccountNonce(tx) {
			s.releaseLatestNonce(node.Address, nonce)
			usesAccountNonce = false
			nonce = s.getNextProtocolNonce(node.Address)
			tx = build(nonce)
			if tx == nil {
				s.releaseLatestProtocolNonce(node.Address, nonce)
				return nonce, nil, false
			}
		}
	}
	submitOpts := opts
	submitOpts.skipAdmissionGate = true
	if !s.submitTxWithOptions(node, tx, submitOpts) {
		if usesAccountNonce {
			s.releaseLatestNonce(node.Address, nonce)
		} else {
			s.releaseLatestProtocolNonce(node.Address, nonce)
		}
		return nonce, tx, false
	}
	if usesAccountNonce {
		s.rememberSimSubmission(node, tx)
	}
	return nonce, tx, true
}

type simGeneratedSubmission struct {
	nonce uint64
	tx    *pb.AnyTx
	ok    bool
}

func (s *TxSimulator) submitGeneratedSpotCross(
	node *NodeInstance,
	buildSell func(uint64) *pb.AnyTx,
	buildBuy func(uint64) *pb.AnyTx,
) (simGeneratedSubmission, simGeneratedSubmission) {
	return s.submitGeneratedOrderPair(node, buildSell, buildBuy)
}

                                                                           
                                                                            
                                                                   
func (s *TxSimulator) submitGeneratedOrderPair(
	node *NodeInstance,
	buildFirst func(uint64) *pb.AnyTx,
	buildSecond func(uint64) *pb.AnyTx,
) (simGeneratedSubmission, simGeneratedSubmission) {
	if node == nil || buildFirst == nil || buildSecond == nil || !isActiveSimNode(node) {
		return simGeneratedSubmission{}, simGeneratedSubmission{}
	}
	lock := s.nonceLock(node.Address)
	lock.Lock()
	defer lock.Unlock()
	if !s.canSubmitNowN(simSubmitOptions{}, 2) {
		return simGeneratedSubmission{}, simGeneratedSubmission{}
	}
	                                                                          
	                                                                           
	                                               
	opts := simSubmitOptions{skipAdmissionGate: true}
	firstNonce, firstTx, firstOK := s.submitGeneratedTxWithOptionsLocked(node, opts, buildFirst)
	first := simGeneratedSubmission{nonce: firstNonce, tx: firstTx, ok: firstOK}
	if !firstOK {
		return first, simGeneratedSubmission{}
	}
	secondNonce, secondTx, secondOK := s.submitGeneratedTxWithOptionsLocked(node, opts, buildSecond)
	return first, simGeneratedSubmission{nonce: secondNonce, tx: secondTx, ok: secondOK}
}

func (s *TxSimulator) submitTxWithOptions(node *NodeInstance, tx *pb.AnyTx, opts simSubmitOptions) bool {
	if node == nil || tx == nil {
		return false
	}
	if !isActiveSimNode(node) {
		return false
	}
	base := tx.GetBase()
	txID := ""
	if base != nil {
		txID = base.TxId
	}
	voteContent, isWitnessVote := tx.GetContent().(*pb.AnyTx_WitnessVoteTx)
	voteRequestID := ""
	voteWitnessAddr := ""
	if isWitnessVote && voteContent != nil && voteContent.WitnessVoteTx != nil && voteContent.WitnessVoteTx.Vote != nil {
		voteRequestID = voteContent.WitnessVoteTx.Vote.RequestId
		voteWitnessAddr = voteContent.WitnessVoteTx.Vote.WitnessAddress
	}
	                                                                             
	if !opts.skipAdmissionGate && s.paused.Load() {
		if isWitnessVote {
			logs.Warn(
				"[SIMULATOR][WitnessVoteSubmit] skipped_paused tx=%s request=%s witness=%s node=%s",
				txID, voteRequestID, voteWitnessAddr, node.Address,
			)
		}
		return false
	}
	throttle := s.submitThrottle.Load()
	if !opts.skipAdmissionGate && throttle <= 0 {
		if isWitnessVote {
			logs.Warn(
				"[SIMULATOR][WitnessVoteSubmit] skipped_hard_pressure tx=%s request=%s witness=%s node=%s throttle=%d",
				txID, voteRequestID, voteWitnessAddr, node.Address, throttle,
			)
		}
		return false
	}
	if !opts.skipAdmissionGate && !opts.bypassLimiter && !s.submitLimiter.allow(throttle) {
		return false
	}

	                                    
	if signedID, err := txverify.SignWithKeyManager(tx, node.KeyManager); err != nil {
		logs.Error("Simulator: Failed to sign tx %s: %v", txID, err)
		return false
	} else {
		txID = signedID
	}

	                                                                                          
	accepted, err := node.TxPool.SubmitLocalTx(tx, func(txID string) {
		if node.SenderManager != nil {
			node.SenderManager.BroadcastTx(tx)
		}
	})
	if !accepted {
		errStr := err.Error()
		if strings.Contains(errStr, "txpool queue is full") || strings.Contains(errStr, "txpool pending is full") {
			logs.Debug("Simulator: Drop tx %s due txpool pressure: %v", txID, err)
			if isWitnessVote {
				logs.Warn(
					"[SIMULATOR][WitnessVoteSubmit] dropped_txpool_pressure tx=%s request=%s witness=%s node=%s err=%v",
					txID, voteRequestID, voteWitnessAddr, node.Address, err,
				)
			}
			return false
		}
		logs.Error("Simulator: Failed to submit tx %s: %v", txID, err)
		if isWitnessVote {
			logs.Warn(
				"[SIMULATOR][WitnessVoteSubmit] submit_failed tx=%s request=%s witness=%s node=%s err=%v",
				txID, voteRequestID, voteWitnessAddr, node.Address, err,
			)
		}
		return false
	}
	if err != nil {
		logs.Warn("Simulator: tx %s admitted but persistence queue failed: %v", txID, err)
	}
	if isWitnessVote {
		logs.Info(
			"[SIMULATOR][WitnessVoteSubmit] accepted tx=%s request=%s witness=%s node=%s",
			txID, voteRequestID, voteWitnessAddr, node.Address,
		)
	}
	return true
}

func (s *TxSimulator) runRandomTransfers() {
	ticker := time.NewTicker(simTransferAttemptTick)
	defer ticker.Stop()

	for range ticker.C {
		if s.paused.Load() || s.disableTransfers.Load() {
			continue
		}
		attempts := simAttemptsForBlockTarget(s.effectiveTransferTargetTxsPerBlock(), simTransferAttemptTick, s.rateBlockSecondsValue())
		for n := 0; n < attempts; n++ {
			if s.paused.Load() {
				break
			}
			fromNode, toAddress, err := s.randomTransferEndpoints()
			if fromNode == nil {
				continue
			}
			if err != nil {
				logs.Warn("Simulator: failed to generate random transfer target: %v", err)
				continue
			}

			amount := simRandomTransferAmount()
			nonce, tx, ok := s.submitGeneratedTxWithOptions(fromNode, simSubmitOptions{bypassLimiter: true}, func(nonce uint64) *pb.AnyTx {
				return generateTransferTx(fromNode.Address, toAddress, "FB", amount, "1000", nonce)
			})
			if ok {
				logs.Trace("Simulator: Added random transfer %s from %s to %s amount=%s (nonce=%d)", tx.GetBase().TxId, fromNode.Address, toAddress, amount, nonce)
			}
		}
	}
}

func (s *TxSimulator) randomTransferEndpoints() (*NodeInstance, string, error) {
	fromNode := s.randomSimNode()
	if fromNode == nil {
		return nil, "", nil
	}
	toAddress, err := randomDexAddress()
	if err != nil {
		return fromNode, "", err
	}
	return fromNode, toAddress, nil
}

func (s *TxSimulator) runWitnessRegistration(ctx context.Context) {
	                                                                                                                         
	if len(s.nodes) > 0 && s.nodes[0] != nil {
		cleanupLogger := logs.SetThreadNodeContext(s.nodes[0].Address)
		defer cleanupLogger()
	}

	                                                                                                                            
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	completed := make(map[string]bool)
	loggedDisabled := false
	loggedNoNodes := false
	loggedHeightWait := false

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if s.disableWitnessReg.Load() {
			if !loggedDisabled {
				logs.Info("SIMULATOR: Witness registration disabled, waiting for enable")
				loggedDisabled = true
			}
			continue
		}
		loggedDisabled = false

		registrationNodes := simulatorWitnessRegistrationNodes(s.nodes)
		if len(registrationNodes) == 0 {
			if !loggedNoNodes {
				logs.Info("SIMULATOR: Not enough nodes for witness registration (%d)", len(s.nodes))
				loggedNoNodes = true
			}
			continue
		}
		loggedNoNodes = false

		height := s.maxAcceptedHeight()
		if height < simWitnessRegistrationHeight {
			if !loggedHeightWait {
				logs.Info("SIMULATOR: Waiting for accepted height %d before witness registration (current=%d)", simWitnessRegistrationHeight, height)
				loggedHeightWait = true
			}
			continue
		}
		loggedHeightWait = false

		for _, witnessNode := range registrationNodes {
			if witnessNode == nil || completed[witnessNode.Address] {
				continue
			}
			done, err := s.registerWitnessIfNeeded(witnessNode)
			if err != nil {
				logs.Warn("Witness registration: node=%s: %v; will retry", witnessNode.Address, err)
			} else if done {
				completed[witnessNode.Address] = true
			}
		}
		if len(completed) >= len(registrationNodes) {
			logs.Info("Witness registration: all local accounts have finalized witness records.")
			return
		}
	}
}

                                                                                           
                                                                                                    
func (s *TxSimulator) registerWitnessIfNeeded(node *NodeInstance) (bool, error) {
	if !isActiveSimNode(node) || node.DBManager == nil {
		return false, nil
	}
	lock := s.nonceLock(node.Address)
	lock.Lock()
	defer lock.Unlock()
	state := s.simAccount(node)
	status, ok := s.simAccountStatus(node, state)
	if !ok {
		return false, nil
	}
	                                                                                                   
	if _, exists, err := node.DBManager.GetState(keys.KeyWitnessInfo(node.Address)); err != nil || exists {
		return exists, err
	}
	if s.replaySimSubmission(node, state, status) {
		return false, nil
	}
	for _, tx := range state.pending {
		if tx.GetWitnessStakeTx() != nil {
			return false, nil
		}
	}
	amount, ok := new(big.Int).SetString(s.witnessStakeAmount, 10)
	if !ok || amount.Sign() <= 0 {
		return false, fmt.Errorf("invalid minimum witness stake %q", s.witnessStakeAmount)
	}
	available, ok := finalizedSimAvailableBalance(node, "FB")
	feeTx := generateWitnessStakeTx(node.Address, pb.OrderOp_ADD, s.witnessStakeAmount, 0)
	fee, _ := new(big.Int).SetString(feeTx.GetBase().Fee, 10)
	if !ok || available.Cmp(new(big.Int).Add(amount, fee)) < 0 {
		return false, fmt.Errorf("waiting for available FB to cover stake=%s and fee=%s", amount, fee)
	}
	nonce, tx, admitted := s.submitGeneratedTxWithOptionsLocked(node, simSubmitOptions{bypassLimiter: true}, func(nonce uint64) *pb.AnyTx {
		return generateWitnessStakeTx(node.Address, pb.OrderOp_ADD, s.witnessStakeAmount, nonce)
	})
	if admitted {
		logs.Info("Witness registration: admitted tx=%s node=%s amount=%s nonce=%d; waiting for finalization", tx.GetTxId(), node.Address, s.witnessStakeAmount, nonce)
	}
	return false, nil
}

func simulatorWitnessRegistrationNodes(nodes []*NodeInstance) []*NodeInstance {
	if len(nodes) == 0 {
		return nil
	}
	start := 1
	if len(nodes) == 1 {
		start = 0
	}
	end := len(nodes)
	if end-start > 20 {
		end = start + 20
	}
	selected := make([]*NodeInstance, 0, end-start)
	for i := start; i < end; i++ {
		if nodes[i] != nil {
			selected = append(selected, nodes[i])
		}
	}
	return selected
}

func (s *TxSimulator) waitForAcceptedHeight(target uint64) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		if s.maxAcceptedHeight() >= target {
			return
		}
	}
}

func (s *TxSimulator) maxAcceptedHeight() uint64 {
	var maxHeight uint64
	for _, node := range s.nodes {
		if node == nil || node.ConsensusManager == nil {
			continue
		}
		_, height := node.ConsensusManager.GetLastAccepted()
		if height > maxHeight {
			maxHeight = height
		}
	}
	return maxHeight
}

func (s *TxSimulator) runRechargeScenario() {
	                        
	if len(s.nodes) > 0 && s.nodes[0] != nil {
		cleanupLogger := logs.SetThreadNodeContext(s.nodes[0].Address)
		defer cleanupLogger()
	}

	                                                                                             
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	nextAsset := 0
	for range ticker.C {
		if len(s.nodes) == 0 || s.nodes[0] == nil || s.disableRecharge.Load() {
			continue
		}
		if len(s.rechargeAssets) == 0 {
			logs.Trace("Simulator: No configured recharge assets, waiting...")
			continue
		}

		submitted := 0
		userNode := s.nodes[0]
		for attempts := 0; attempts < len(s.rechargeAssets) && submitted < simRechargeBatchPerTick; attempts++ {
			asset := s.rechargeAssets[nextAsset%len(s.rechargeAssets)]
			nextAsset++
			if !s.checkVaultActive(asset.Chain) {
				logs.Trace("Simulator: No ACTIVE vault for chain=%s yet, waiting to send recharge request...", asset.Chain)
				continue
			}

			nonce, reqTx, ok := s.submitGeneratedPriorityTx(userNode, func(nonce uint64) *pb.AnyTx {
				nativeTxHashRaw := sha256.Sum256([]byte(fmt.Sprintf("sim_recharge:%s:%s:%d:%d", asset.Chain, asset.Token, time.Now().UnixNano(), nonce)))
				nativeTxHash := fmt.Sprintf("%x", nativeTxHashRaw)
				var script []byte
				if frostchain.NormalizeChain(asset.Chain) == frostchain.ChainBTC {
					script = s.buildRechargeTaprootScriptPubKey(asset.Chain, nativeTxHash, userNode.Address)
				}
				return generateWitnessRequestTx(
					userNode.Address,
					asset.Chain,
					nativeTxHash,
					0,
					script,
					asset.NativeContract,
					asset.Amount,
					userNode.Address,
					asset.Fee,
					nonce,
				)
			})
			if ok {
				submitted++
				logs.Info("SIMULATOR: Submitted WitnessRequestTx %s chain=%s token=%s amount=%s nonce=%d for User %s", reqTx.GetBase().TxId, asset.Chain, asset.Token, asset.Amount, nonce, userNode.Address)
			} else {
				logs.Warn("SIMULATOR: WitnessRequestTx skipped chain=%s token=%s amount=%s nonce=%d for User %s", asset.Chain, asset.Token, asset.Amount, nonce, userNode.Address)
			}
		}
	}
}

                                                                                                  
                                                                                                   
                                                         
func (s *TxSimulator) scanUsableVaultStates() []*pb.FrostVaultState {
	if len(s.nodes) == 0 || s.nodes[0] == nil || s.nodes[0].DBManager == nil {
		return nil
	}
	raw, err := s.nodes[0].DBManager.Scan(keys.KeyFrostVaultStatePrefix())
	if err != nil || len(raw) == 0 {
		return nil
	}
	out := make([]*pb.FrostVaultState, 0, len(raw))
	for _, data := range raw {
		state := &pb.FrostVaultState{}
		if err := proto.Unmarshal(data, state); err != nil {
			continue
		}
		if state.Status == vm.VaultStatusActive || state.Status == vm.VaultStatusKeyReady {
			out = append(out, state)
		}
	}
	return out
}

func mockTaprootScriptPubKey(seed string) []byte {
	digest := sha256.Sum256([]byte(seed))
	scriptPubKey, _ := utils.BuildTaprootScriptPubKeyFromXOnly(digest[:])
	return scriptPubKey
}

func buildTweakedTaprootScriptPubKey(groupPubKey []byte, receiverAddress string, guardian []byte) ([]byte, error) {
	return utils.BuildTweakedDualCustodyTaprootScriptPubKey(groupPubKey, receiverAddress, guardian)
}

func (s *TxSimulator) buildRechargeTaprootScriptPubKey(chain, nativeTxHash, receiverAddress string) []byte {
	chainName := frostchain.NormalizeChain(chain)
	for _, state := range s.scanUsableVaultStates() {
		if frostchain.NormalizeChain(state.Chain) != chainName {
			continue
		}
		if _, err := utils.NormalizeSecp256k1XOnlyPubKey(state.GroupPubkey); err != nil {
			continue
		}
		rawConfig, err := s.nodes[0].DBManager.Get(keys.KeyFrostVaultConfig(chainName, 0))
		if err != nil {
			continue
		}
		var cfg pb.FrostVaultConfig
		if proto.Unmarshal([]byte(rawConfig), &cfg) != nil {
			continue
		}
		scriptPubKey, err := buildTweakedTaprootScriptPubKey(state.GroupPubkey, receiverAddress, cfg.GuardianPubkey)
		if err != nil {
			continue
		}
		return scriptPubKey
	}
	return mockTaprootScriptPubKey("sim-p2tr:" + chain + ":" + nativeTxHash + ":" + receiverAddress)
}

                                                   
                                                                           
func (s *TxSimulator) runWitnessVoteWorker() {
	                                             
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	                                                      
	                                                                    
	votedRequests := make(map[string]bool)

	for range ticker.C {
		if len(s.nodes) == 0 || s.disableWitnessVote.Load() {
			continue
		}
		tickVotingRequests := 0
		tickTargetedRequests := 0
		tickSubmittedVotes := 0
		tickAlreadyVoted := 0
		tickSkippedNotTarget := 0
		tickNoWitnessService := 0

		                                                                                                            
		                                                               
		for _, node := range s.nodes {
			if node == nil {
				continue
			}
			if node.WitnessService == nil {
				tickNoWitnessService++
				continue
			}

			                           
			activeRequests := node.WitnessService.GetActiveRequests()
			for _, req := range activeRequests {
				if req.Status != pb.RechargeRequestStatus_RECHARGE_VOTING {
					continue
				}
				tickVotingRequests++

				                                                            
				isTarget := false
				for _, addr := range req.SelectedWitnesses {
					if addr == node.Address {
						isTarget = true
						break
					}
				}

				if !isTarget {
					tickSkippedNotTarget++
					continue
				}
				tickTargetedRequests++

				                                       
				voteKey := fmt.Sprintf("%s:%d:%s", req.RequestId, req.Round, node.Address)
				if votedRequests[voteKey] {
					tickAlreadyVoted++
					continue
				}

				               
				nonce, _, ok := s.submitGeneratedProtocolTx(node, func(nonce uint64) *pb.AnyTx {
					return generateWitnessVoteTx(
						node.Address,
						req.RequestId,
						req.Round,
						pb.WitnessVoteType_VOTE_PASS,
						nonce,
					)
				})

				                                                                                                   
				func() {
					cleanupLogger := logs.SetThreadNodeContext(node.Address)
					defer cleanupLogger()
					if ok {
						logs.Info("SIMULATOR: Witness %s auto-voted PASS for request %s (nonce=%d)", node.Address, req.RequestId, nonce)
						votedRequests[voteKey] = true
						tickSubmittedVotes++
					}
				}()
			}
		}
		if tickVotingRequests > 0 && tickSubmittedVotes == 0 {
			logs.Warn(
				"[SIMULATOR][WitnessVoteTick] no_vote_submitted voting=%d targeted=%d already_voted=%d not_target=%d no_witness_service_nodes=%d paused=%v throttle=%d",
				tickVotingRequests,
				tickTargetedRequests,
				tickAlreadyVoted,
				tickSkippedNotTarget,
				tickNoWitnessService,
				s.paused.Load(),
				s.submitThrottle.Load(),
			)
		}
		if tickSubmittedVotes > 0 {
			logs.Info(
				"[SIMULATOR][WitnessVoteTick] submitted=%d voting=%d targeted=%d already_voted=%d paused=%v",
				tickSubmittedVotes,
				tickVotingRequests,
				tickTargetedRequests,
				tickAlreadyVoted,
				s.paused.Load(),
			)
		}
	}
}

                                                
                                                                                                               
                                                                                 
func (s *TxSimulator) runArbitrationWorker() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	seen := make(map[string]bool)                                               
	challenged := make(map[string]bool)                           
	counter := 0

	for range ticker.C {
		if len(s.nodes) == 0 || s.disableArbitration.Load() || s.paused.Load() {
			continue
		}

		                                                                                        
		var challenger *NodeInstance
		for _, n := range s.nodes {
			if n != nil && n.TxPool != nil {
				challenger = n
				break
			}
		}
		if challenger == nil {
			continue
		}

		                                                             
		var scanner *NodeInstance
		for _, n := range s.nodes {
			if n != nil && n.WitnessService != nil {
				scanner = n
				break
			}
		}
		if scanner == nil {
			continue
		}

		for _, req := range scanner.WitnessService.GetActiveRequests() {
			if req.Status != pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD {
				continue
			}
			if seen[req.RequestId] || challenged[req.RequestId] {
				continue
			}
			seen[req.RequestId] = true
			counter++
			if counter%3 != 0 {
				continue
			}

			stakeAmount := quoteSimulatorChallengeStake(scanner.WitnessService, req)
			func() {
				cleanupLogger := logs.SetThreadNodeContext(challenger.Address)
				defer cleanupLogger()
				nonce, _, ok := s.submitGeneratedTx(challenger, func(nonce uint64) *pb.AnyTx {
					return generateWitnessChallengeTx(
						challenger.Address,
						req.RequestId,
						stakeAmount,
						"simulator auto-challenge",
						nonce,
					)
				})
				if ok {
					challenged[req.RequestId] = true
					logs.Info("[SIMULATOR][Arbitration] challenge_submitted request=%s challenger=%s counter=%d nonce=%d",
						req.RequestId, challenger.Address, counter, nonce)
				}
			}()
		}
	}
}

                                                                               
                                                                                                                                
func quoteSimulatorChallengeStake(svc *witness.Service, req *pb.RechargeRequest) string {
	if svc == nil || req == nil {
		return "100000"
	}
	stakes := make([]string, 0, len(req.SelectedWitnesses))
	for _, addr := range req.SelectedWitnesses {
		info, err := svc.GetWitnessInfo(addr)
		if err == nil && info != nil {
			stakes = append(stakes, info.StakeAmount)
		}
	}
	quote, err := witness.CalculateChallengeStakeRequirement(svc.ConfigSnapshot(), stakes)
	if err != nil || quote == nil || quote.RequiredChallengeStake == "" {
		return "100000"
	}
	return quote.RequiredChallengeStake
}

func (s *TxSimulator) checkVaultActive(chainName string) bool {
	chainName = frostchain.NormalizeChain(chainName)
	for _, v := range s.scanUsableVaultStates() {
		if v == nil || frostchain.NormalizeChain(v.Chain) != chainName {
			continue
		}
		logs.Info("Simulator: Found usable vault: chain=%s, vault_id=%d, epoch=%d, status=%s",
			v.Chain, v.VaultId, v.KeyEpoch, v.Status)
		return true
	}
	return false
}

                                                                                 
func (s *TxSimulator) buildSimWithdrawAddress(chainName, seed string) (string, error) {
	digest := sha256.Sum256([]byte("sim-withdraw:" + chainName + ":" + seed))
	switch frostchain.NormalizeChain(chainName) {
	case frostchain.ChainETH, frostchain.ChainBNB, frostchain.ChainBASE, frostchain.ChainARB:
		return fmt.Sprintf("0x%x", digest[12:]), nil
	case frostchain.ChainBTC:
		                                                                         
	default:
		return "", fmt.Errorf("simulated withdrawals are not supported for chain %s", chainName)
	}

	network := strings.TrimSpace(s.btcNetwork)
	if network == "" {
		network = config.DefaultFrostConfig().Chains[frostchain.ChainBTC].BTCNetwork
	}
	params, err := frostbtc.NetworkParams(network)
	if err != nil {
		return "", fmt.Errorf("resolve simulated BTC network: %w", err)
	}
	privKey, _ := btcec.PrivKeyFromBytes(digest[:])
	addr, err := btcutil.NewAddressTaproot(schnorr.SerializePubKey(privKey.PubKey()), params)
	if err != nil {
		return "", fmt.Errorf("build simulated BTC address: %w", err)
	}
	return addr.EncodeAddress(), nil
}

func (s *TxSimulator) runWithdrawScenario() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	nextAsset := 0
	nextAt := time.Now().Add(s.withdrawIntervalDuration())
	for now := range ticker.C {
		if s.disableWithdraw.Load() {
			nextAt = now.Add(s.withdrawIntervalDuration())
			continue
		}
		if now.Before(nextAt) {
			continue
		}
		nextAt = now.Add(s.withdrawIntervalDuration())
		logs.Info("Simulator: Starting Withdraw Scenario...")
		if len(s.withdrawAssets) == 0 {
			logs.Trace("Simulator: No configured withdraw assets, waiting...")
			continue
		}

		userNode := s.randomSimNode()
		if userNode == nil {
			continue
		}

		if blocked, openCount, ok := s.shouldBackpressureSimWithdrawQueue(); blocked {
			logs.Trace("Simulator: Withdraw skipped by global queue backpressure open=%d limit=%d", openCount, simWithdrawQueueBackpressureLimit)
			continue
		} else if !ok {
			logs.Trace("Simulator: Withdraw queue backpressure unavailable; continuing")
		}
		asset := s.withdrawAssets[nextAsset%len(s.withdrawAssets)]
		nextAsset++
		withdrawAmount := simWithdrawAmountForAsset(asset)
		withdrawAddr, err := s.buildSimWithdrawAddress(asset.Chain, userNode.Address)
		if err != nil {
			logs.Warn("Simulator: Withdraw Request skipped chain=%s asset=%s: %v", asset.Chain, asset.Token, err)
			continue
		}
		nonce, withdrawTx, ok := s.submitGeneratedPriorityTx(userNode, func(nonce uint64) *pb.AnyTx {
			return generateWithdrawRequestTx(
				userNode.Address,
				asset.Chain,
				asset.Token,
				withdrawAddr,
				withdrawAmount.String(),
				nonce,
			)
		})

		if ok {
			logs.Info("Simulator: Withdraw Request %s queued chain=%s asset=%s amount=%s to=%s nonce=%d", withdrawTx.GetBase().TxId, asset.Chain, asset.Token, withdrawAmount.String(), withdrawAddr, nonce)
		} else {
			logs.Warn("Simulator: Withdraw Request skipped chain=%s asset=%s amount=%s from=%s nonce=%d", asset.Chain, asset.Token, withdrawAmount.String(), userNode.Address, nonce)
		}
	}
}

func (s *TxSimulator) usableSimNodes() []*NodeInstance {
	nodes := make([]*NodeInstance, 0, len(s.nodes))
	for _, node := range s.nodes {
		if isActiveSimNode(node) {
			nodes = append(nodes, node)
		}
	}
	return nodes
}

func (s *TxSimulator) randomSimNode() *NodeInstance {
	nodes := s.usableSimNodes()
	if len(nodes) == 0 {
		return nil
	}
	return nodes[mrand.Intn(len(nodes))]
}

func randomDexAddress() (string, error) {
	privKey, err := btcec.NewPrivateKey()
	if err != nil {
		return "", err
	}
	return utils.DeriveDexAddress(privKey)
}

func (s *TxSimulator) hasSpotLastPrice(pair string) bool {
	for _, node := range s.nodes {
		if node == nil || node.DBManager == nil {
			continue
		}
		if raw, err := node.DBManager.Get(keys.KeySpotLastPrice(pair)); err == nil && len(raw) > 0 {
			return true
		}
	}
	return false
}

func partitionSimTradingPairs(pairs []simTradingPair, partitions, partitionID int64) []simTradingPair {
	if len(pairs) == 0 || partitions <= 1 {
		return pairs
	}
	slot := partitionID % partitions
	if slot < 0 {
		slot += partitions
	}
	out := make([]simTradingPair, 0, (len(pairs)+int(partitions)-1)/int(partitions))
	for i, pair := range pairs {
		if int64(i)%partitions == slot {
			out = append(out, pair)
		}
	}
	return out
}

func simFutureCrossHoldID(node *NodeInstance, pairKey, leg string) string {
	if node == nil {
		return ""
	}
	return "future:" + node.Address + ":" + pairKey + ":" + leg
}

                                                                              
                                                                             
                                                                             
                                                                            
func (s *TxSimulator) submitGeneratedFutureCross(
	node *NodeInstance,
	pair simTradingPair,
	amount, sellPrice, buyPrice *big.Int,
	leverage uint64,
) (simGeneratedSubmission, simGeneratedSubmission, *simPendingFutureCross) {
	if node == nil || amount == nil || sellPrice == nil || buyPrice == nil ||
		amount.Sign() <= 0 || sellPrice.Sign() <= 0 || buyPrice.Cmp(sellPrice) <= 0 || leverage == 0 {
		return simGeneratedSubmission{}, simGeneratedSubmission{}, nil
	}
	pairKey := pair.pairKey()
	sellMargin := future.CalcRequiredMargin(amount, sellPrice, leverage)
	buyMargin := future.CalcRequiredMargin(amount, buyPrice, leverage)
	if sellMargin.Sign() <= 0 || buyMargin.Sign() <= 0 {
		return simGeneratedSubmission{}, simGeneratedSubmission{}, nil
	}
	sellHoldID := simFutureCrossHoldID(node, pairKey, "sell")
	buyHoldID := simFutureCrossHoldID(node, pairKey, "buy")
	if !s.holdSimCapital(sellHoldID, node, map[string]*big.Int{pair.QuoteToken: sellMargin}) {
		return simGeneratedSubmission{}, simGeneratedSubmission{}, nil
	}
	if !s.holdSimCapital(buyHoldID, node, map[string]*big.Int{pair.QuoteToken: buyMargin}) {
		s.releaseSimCapitalHold(sellHoldID)
		return simGeneratedSubmission{}, simGeneratedSubmission{}, nil
	}

	sell, buy := s.submitGeneratedOrderPair(
		node,
		func(nonce uint64) *pb.AnyTx {
			return generateFutureOrderTx(
				node.Address, pairKey, amount.String(), sellPrice.String(), leverage,
				int32(future.Short), false, nonce,
			)
		},
		func(nonce uint64) *pb.AnyTx {
			return generateFutureOrderTx(
				node.Address, pairKey, amount.String(), buyPrice.String(), leverage,
				int32(future.Long), false, nonce,
			)
		},
	)
	if !sell.ok {
		s.releaseSimCapitalHold(sellHoldID)
		s.releaseSimCapitalHold(buyHoldID)
		return sell, buy, nil
	}
	if !s.commitSimCapitalHold(sellHoldID, pair.QuoteToken, sell.nonce) {
		s.releaseSimCapitalHold(sellHoldID)
	}
	if buy.ok {
		if !s.commitSimCapitalHold(buyHoldID, pair.QuoteToken, buy.nonce) {
			s.releaseSimCapitalHold(buyHoldID)
		}
		return sell, buy, nil
	}

	return sell, buy, &simPendingFutureCross{
		node: node, pair: pair, holdID: buyHoldID,
		amount: new(big.Int).Set(amount), buyPrice: new(big.Int).Set(buyPrice), leverage: leverage,
	}
}

func (s *TxSimulator) retryPendingFutureCross(pending *simPendingFutureCross) (simGeneratedSubmission, bool) {
	if pending == nil || pending.node == nil || !isActiveSimNode(pending.node) {
		if pending != nil {
			s.releaseSimCapitalHold(pending.holdID)
		}
		return simGeneratedSubmission{}, true
	}
	pairKey := pending.pair.pairKey()
	nonce, tx, ok := s.submitGeneratedTx(pending.node, func(nonce uint64) *pb.AnyTx {
		return generateFutureOrderTx(
			pending.node.Address, pairKey, pending.amount.String(), pending.buyPrice.String(), pending.leverage,
			int32(future.Long), false, nonce,
		)
	})
	if !ok {
		return simGeneratedSubmission{nonce: nonce, tx: tx}, false
	}
	if !s.commitSimCapitalHold(pending.holdID, pending.pair.QuoteToken, nonce) {
		s.releaseSimCapitalHold(pending.holdID)
	}
	return simGeneratedSubmission{nonce: nonce, tx: tx, ok: true}, true
}

                                                                               
func (s *TxSimulator) runFutureOrderScenario() {
	time.Sleep(8 * time.Second)                      

	pairs := partitionSimTradingPairs(s.tradingPairs, s.targetPartitions.Load(), s.targetPartitionID.Load())
	if len(pairs) == 0 {
		logs.Warn("SIMULATOR: No future trading pairs assigned to partition, skip future order scenario")
		return
	}
	nodes := s.usableSimNodes()
	if len(nodes) == 0 {
		logs.Warn("SIMULATOR: No active nodes available, skip future order scenario")
		return
	}
	nextPair := 0
	pending := make(map[string]*simPendingFutureCross)
	priceOffsets := make(map[string]int64, len(pairs))
	ticker := time.NewTicker(simFutureAttemptTick)
	defer ticker.Stop()

	for range ticker.C {
		if s.paused.Load() || s.disableFutureOrders.Load() || len(s.nodes) == 0 {
			continue
		}

		orderAttempts := simAttemptsPerTick(s.effectiveTargetTxsPerBlock(), simFutureTargetSharePermille, simFutureAttemptTick, s.rateBlockSecondsValue())
		crossAttempts := (orderAttempts + 1) / 2
		for n := 0; n < crossAttempts; n++ {
			if s.paused.Load() {
				break
			}
			pairIndex := nextPair % len(pairs)
			pair := pairs[pairIndex]
			nextPair++
			pairKey := pair.pairKey()
			if !s.hasSpotLastPrice(pairKey) {
				                                                                   
				                                                             
				                                          
				continue
			}
			if retry := pending[pairKey]; retry != nil {
				buy, done := s.retryPendingFutureCross(retry)
				if done {
					delete(pending, pairKey)
				}
				if buy.ok {
					logs.Trace("Simulator: Added crossed Future LONG %s pair=%s (nonce=%d) %dx qty=%s price=%s",
						buy.tx.GetBase().TxId, pairKey, buy.nonce, retry.leverage, retry.amount.String(), retry.buyPrice.String())
				}
				continue
			}

			node := nodes[pairIndex%len(nodes)]
			if !isActiveSimNode(node) {
				continue
			}
			priceOffsets[pairKey] = simNextCrossPriceOffsetBPS(priceOffsets[pairKey])
			sellPrice, buyPrice := pair.crossedPricesAtOffsetBPS(priceOffsets[pairKey])
			qty := pair.randomAmount()
			sell, buy, retry := s.submitGeneratedFutureCross(node, pair, qty, sellPrice, buyPrice, simFutureCrossLeverage)
			if retry != nil {
				pending[pairKey] = retry
			}
			if sell.ok {
				logs.Trace("Simulator: Added crossed Future SHORT %s pair=%s (nonce=%d) %dx qty=%s price=%s",
					sell.tx.GetBase().TxId, pairKey, sell.nonce, simFutureCrossLeverage, qty.String(), sellPrice.String())
			}
			if buy.ok {
				logs.Trace("Simulator: Added crossed Future LONG %s pair=%s (nonce=%d) %dx qty=%s price=%s",
					buy.tx.GetBase().TxId, pairKey, buy.nonce, simFutureCrossLeverage, qty.String(), buyPrice.String())
			}
		}
	}
}

func newSimTrackedSpotOrder(nonce uint64, tx *pb.AnyTx) simTrackedSpotOrder {
	if tx == nil || tx.GetBase() == nil {
		return simTrackedSpotOrder{}
	}
	return simTrackedSpotOrder{orderID: tx.GetBase().TxId, nonce: nonce}
}

func finalizedSimAccountNonce(node *NodeInstance) (uint64, bool) {
	if node == nil || node.DBManager == nil || strings.TrimSpace(node.Address) == "" {
		return 0, false
	}
	raw, err := node.DBManager.Get(keys.KeyAccount(node.Address))
	if err != nil || len(raw) == 0 {
		return 0, false
	}
	var account pb.Account
	if proto.Unmarshal(raw, &account) != nil {
		return 0, false
	}
	return account.Nonce, true
}

func newSimSpotLiquidityState(node *NodeInstance, pair simTradingPair) *simSpotLiquidityState {
	return &simSpotLiquidityState{
		node:       node,
		pair:       pair,
		buyOrders:  make([]simTrackedSpotOrder, simSpotLiquidityLevels),
		sellOrders: make([]simTrackedSpotOrder, simSpotLiquidityLevels),
	}
}

func simOrderIDFromPriceIndex(indexKey string) string {
	const marker = "|order_id:"
	index := strings.LastIndex(indexKey, marker)
	if index < 0 {
		return ""
	}
	return strings.TrimSpace(indexKey[index+len(marker):])
}

func finalizedSimSpotOrdersAtPrices(node *NodeInstance, pair string, side pb.OrderSide, prices []*big.Int) ([]simTrackedSpotOrder, bool) {
	result := make([]simTrackedSpotOrder, len(prices))
	if node == nil || node.DBManager == nil || strings.TrimSpace(pair) == "" || len(prices) == 0 {
		return result, false
	}
	levels := make(map[string]int, len(prices))
	for level, price := range prices {
		if price != nil && price.Sign() > 0 {
			levels[price.String()] = level
		}
	}
	rows, err := node.DBManager.ScanKeysWithLimitOrdered(keys.KeyOrderPriceIndexPrefix(pair, side, false), 500, false)
	if err != nil {
		return result, false
	}
	for _, indexKey := range rows {
		orderID := simOrderIDFromPriceIndex(indexKey)
		if orderID == "" {
			continue
		}
		raw, exists, err := node.DBManager.GetState(keys.KeyOrderState(orderID))
		if err != nil || !exists || len(raw) == 0 {
			continue
		}
		var state pb.OrderState
		if proto.Unmarshal(raw, &state) != nil || state.Status != pb.OrderStateStatus_ORDER_OPEN || state.IsFilled || state.Side != side {
			continue
		}
		level, expected := levels[state.Price]
		if !expected || result[level].orderID != "" {
			continue
		}
		result[level] = simTrackedSpotOrder{orderID: state.OrderId}
	}
	return result, true
}

func mergeSimSpotLiquidityOrders(tracked, finalized []simTrackedSpotOrder, finalizedNonce uint64, nonceKnown bool) []simTrackedSpotOrder {
	for level := range tracked {
		if tracked[level].orderID != "" && tracked[level].nonce > 0 && (!nonceKnown || finalizedNonce <= tracked[level].nonce) {
			                                                                
			                                                                      
			continue
		}
		if level < len(finalized) {
			tracked[level] = finalized[level]
		} else {
			tracked[level] = simTrackedSpotOrder{}
		}
	}
	return tracked
}

func simSpotLiquidityPrices(pair simTradingPair, side pb.OrderSide) []*big.Int {
	prices := make([]*big.Int, simSpotLiquidityLevels)
	for level := 0; level < simSpotLiquidityLevels; level++ {
		buyPrice, sellPrice := pair.restingPrices(level)
		if side == pb.OrderSide_BUY {
			prices[level] = buyPrice
		} else {
			prices[level] = sellPrice
		}
	}
	return prices
}

func simSpotLiquidityComplete(state *simSpotLiquidityState) bool {
	if state == nil || len(state.buyOrders) != simSpotLiquidityLevels || len(state.sellOrders) != simSpotLiquidityLevels {
		return false
	}
	for level := 0; level < simSpotLiquidityLevels; level++ {
		if state.buyOrders[level].orderID == "" || state.sellOrders[level].orderID == "" {
			return false
		}
	}
	return true
}

func (s *TxSimulator) reconcileSimSpotLiquidity(state *simSpotLiquidityState, now time.Time) bool {
	if state == nil || state.node == nil || state.node.DBManager == nil {
		return false
	}
	if !state.lastReconcile.IsZero() && now.Sub(state.lastReconcile) < simSpotLiquidityRefreshInterval {
		return true
	}
	pairKey := state.pair.pairKey()
	buyFinalized, buyOK := finalizedSimSpotOrdersAtPrices(state.node, pairKey, pb.OrderSide_BUY, simSpotLiquidityPrices(state.pair, pb.OrderSide_BUY))
	sellFinalized, sellOK := finalizedSimSpotOrdersAtPrices(state.node, pairKey, pb.OrderSide_SELL, simSpotLiquidityPrices(state.pair, pb.OrderSide_SELL))
	if !buyOK || !sellOK {
		return false
	}
	finalizedNonce, nonceKnown := finalizedSimAccountNonce(state.node)
	state.buyOrders = mergeSimSpotLiquidityOrders(state.buyOrders, buyFinalized, finalizedNonce, nonceKnown)
	state.sellOrders = mergeSimSpotLiquidityOrders(state.sellOrders, sellFinalized, finalizedNonce, nonceKnown)
	state.lastReconcile = now
	return true
}

func (s *TxSimulator) submitSimSpotRestingOrder(state *simSpotLiquidityState, side pb.OrderSide, level int) simTrackedSpotOrder {
	if state == nil || state.node == nil || level < 0 || level >= simSpotLiquidityLevels {
		return simTrackedSpotOrder{}
	}
	buyPrice, sellPrice := state.pair.restingPrices(level)
	price := sellPrice
	if side == pb.OrderSide_BUY {
		price = buyPrice
	}
	if price == nil || price.Sign() <= 0 {
		return simTrackedSpotOrder{}
	}
	amount := state.pair.randomAmount()
	spendToken := state.pair.BaseToken
	spendAmount := amount
	if side == pb.OrderSide_BUY {
		available, ok := s.availableSimCapital(state.node, state.pair.QuoteToken)
		if !ok {
			return simTrackedSpotOrder{}
		}
		amount = capSimSpotBuyAmount(amount, price, available)
		var err error
		spendAmount, err = vm.QuoteFromBasePrice(amount, price)
		if err != nil {
			return simTrackedSpotOrder{}
		}
		spendToken = state.pair.QuoteToken
	} else {
		available, ok := s.availableSimCapital(state.node, state.pair.BaseToken)
		if !ok {
			return simTrackedSpotOrder{}
		}
		amount = capSimSpotSellAmount(amount, available)
		spendAmount = amount
	}
	if amount.Sign() <= 0 || spendAmount.Sign() <= 0 {
		return simTrackedSpotOrder{}
	}
	nonce, tx, submitted := s.submitGeneratedTxWithCapital(state.node, spendToken, spendAmount, func(nonce uint64) *pb.AnyTx {
		return generateOrderTx(
			state.node.Address,
			state.pair.BaseToken,
			state.pair.QuoteToken,
			amount.String(),
			price.String(),
			nonce,
			side,
		)
	})
	if !submitted || tx == nil || tx.GetBase() == nil {
		return simTrackedSpotOrder{}
	}
	logs.Trace("Simulator: Added resting spot order %s pair=%s side=%s level=%d nonce=%d amount=%s price=%s",
		tx.GetBase().TxId, state.pair.pairKey(), side.String(), level, nonce, amount.String(), price.String())
	return newSimTrackedSpotOrder(nonce, tx)
}

func (s *TxSimulator) maintainSimSpotLiquidity(state *simSpotLiquidityState, now time.Time) bool {
	if !s.reconcileSimSpotLiquidity(state, now) {
		return false
	}
	submitted := 0
	for level := 0; level < simSpotLiquidityLevels && submitted < simSpotLiquiditySeedOrdersPerTick; level++ {
		if state.sellOrders[level].orderID == "" {
			if order := s.submitSimSpotRestingOrder(state, pb.OrderSide_SELL, level); order.orderID != "" {
				state.sellOrders[level] = order
				submitted++
			}
		}
		if submitted >= simSpotLiquiditySeedOrdersPerTick {
			break
		}
		if state.buyOrders[level].orderID == "" {
			if order := s.submitSimSpotRestingOrder(state, pb.OrderSide_BUY, level); order.orderID != "" {
				state.buyOrders[level] = order
				submitted++
			}
		}
	}
	return simSpotLiquidityComplete(state)
}

func (s *TxSimulator) advanceTrackedSpotOrder(node *NodeInstance, finalizedNonce uint64, item *simTrackedSpotOrder, submittedCancels *int) {
	if node == nil || item == nil || item.terminal || item.orderID == "" || finalizedNonce <= item.nonce {
		return
	}
	if item.cancelID != "" && finalizedNonce <= item.cancelNonce {
		return
	}

	raw, exists, err := node.DBManager.GetState(keys.KeyOrderState(item.orderID))
	if err != nil {
		return
	}
	if !exists || len(raw) == 0 {
		item.terminal = true
		return
	}
	var state pb.OrderState
	if proto.Unmarshal(raw, &state) != nil {
		return
	}
	if state.Owner != node.Address || state.Status != pb.OrderStateStatus_ORDER_OPEN || state.IsFilled {
		item.terminal = true
		return
	}

	                                                                          
	                                                                    
	if item.cancelID != "" {
		item.cancelID = ""
		item.cancelNonce = 0
	}
	if submittedCancels == nil || *submittedCancels >= simSpotOrderCancelLimit {
		return
	}
	nonce, tx, submitted := s.submitGeneratedCleanupTx(node, func(nonce uint64) *pb.AnyTx {
		return generateCancelOrderTx(node.Address, item.orderID, nonce)
	})
	if !submitted || tx == nil || tx.GetBase() == nil {
		return
	}
	item.cancelID = tx.GetBase().TxId
	item.cancelNonce = nonce
	(*submittedCancels)++
}

func (s *TxSimulator) advancePendingSpotCrosses(crosses map[string]*simPendingSpotCross) {
	submittedCancels := 0
	for pairKey, cross := range crosses {
		if cross == nil || cross.node == nil {
			delete(crosses, pairKey)
			continue
		}
		if cross.buyOrder.orderID == "" {
			continue
		}
		finalizedNonce, ok := finalizedSimAccountNonce(cross.node)
		if !ok {
			continue
		}
		s.advanceTrackedSpotOrder(cross.node, finalizedNonce, &cross.sellOrder, &submittedCancels)
		s.advanceTrackedSpotOrder(cross.node, finalizedNonce, &cross.buyOrder, &submittedCancels)
		if cross.sellOrder.terminal && cross.buyOrder.terminal {
			s.releaseSimCapitalHold(cross.holdID)
			delete(crosses, pairKey)
		}
	}
}

                                                                                                                        
func (s *TxSimulator) runOrderScenario() {
	                                             
	time.Sleep(5 * time.Second)

	pairs := s.tradingPairs
	if len(pairs) == 0 {
		logs.Warn("SIMULATOR: No spot trading pairs available, skip order scenario")
		return
	}
	nextPair := 0
	crosses := make(map[string]*simPendingSpotCross, len(pairs))
	liquidity := make(map[string]*simSpotLiquidityState, len(pairs))
	priceOffsets := make(map[string]int64, len(pairs))
	lastCleanup := time.Now()
	                                                                                                          
	ticker := time.NewTicker(simSpotAttemptTick)
	defer ticker.Stop()

	for now := range ticker.C {
		if now.Sub(lastCleanup) >= simSpotOrderCleanupInterval {
			s.advancePendingSpotCrosses(crosses)
			lastCleanup = now
		}
		if s.paused.Load() || s.disableOrders.Load() {
			continue
		}
		if len(s.nodes) == 0 {
			continue
		}

		attempts := simAttemptsPerTick(s.effectiveTargetTxsPerBlock(), simSpotTargetSharePermille, simSpotAttemptTick, s.rateBlockSecondsValue())
		for n := 0; n < attempts; n++ {
			if s.paused.Load() {
				break
			}
			pair := pairs[nextPair%len(pairs)]
			nextPair++
			pairKey := pair.pairKey()
			liquidityState := liquidity[pairKey]
			if liquidityState == nil || !isActiveSimNode(liquidityState.node) {
				liquidityNode := s.randomSimNode()
				if liquidityNode == nil {
					continue
				}
				liquidityState = newSimSpotLiquidityState(liquidityNode, pair)
				liquidity[pairKey] = liquidityState
			}
			if !s.maintainSimSpotLiquidity(liquidityState, now) {
				continue
			}
			if pending := crosses[pairKey]; pending != nil {
				if !isActiveSimNode(pending.node) {
					s.releaseSimCapitalHold(pending.holdID)
					delete(crosses, pairKey)
					continue
				}
				if pending.buyOrder.orderID != "" {
					continue
				}
				nonce, tx, ok := s.submitGeneratedTx(pending.node, func(nonce uint64) *pb.AnyTx {
					return generateOrderTx(
						pending.node.Address,
						pending.pair.BaseToken,
						pending.pair.QuoteToken,
						pending.amount.String(),
						pending.buyPrice.String(),
						nonce,
						pb.OrderSide_BUY,
					)
				})
				if !ok {
					continue
				}
				if !s.commitSimCapitalHold(pending.holdID, pending.pair.QuoteToken, nonce) {
					s.releaseSimCapitalHold(pending.holdID)
					logs.Warn("Simulator: crossed BUY capital hold disappeared pair=%s nonce=%d", pairKey, nonce)
				}
				pending.buyOrder = newSimTrackedSpotOrder(nonce, tx)
				logs.Trace("Simulator: Added crossed BUY order %s pair=%s (nonce=%d) from %s, amount=%s price=%s",
					tx.GetBase().TxId, pending.pair.pairKey(), nonce, pending.node.Address, pending.amount.String(), pending.buyPrice.String())
				continue
			}

			node := s.randomSimNode()
			if node == nil {
				continue
			}
			priceOffsets[pairKey] = simNextCrossPriceOffsetBPS(priceOffsets[pairKey])
			sellPrice, buyPrice := pair.crossedPricesAtOffsetBPS(priceOffsets[pairKey])
			if sellPrice.Sign() <= 0 || buyPrice.Cmp(sellPrice) <= 0 {
				continue
			}
			amount := pair.randomAmount()
			availableQuote, ok := s.availableSimCapital(node, pair.QuoteToken)
			if !ok {
				continue
			}
			amount = capSimSpotBuyAmount(amount, buyPrice, availableQuote)
			availableBase, ok := s.availableSimCapital(node, pair.BaseToken)
			if !ok {
				continue
			}
			amount = capSimSpotSellAmount(amount, availableBase)
			if amount.Sign() <= 0 {
				continue
			}
			buyNeed, err := vm.QuoteFromBasePrice(amount, buyPrice)
			if err != nil || buyNeed.Sign() <= 0 {
				continue
			}
			holdID := "spot:" + pairKey
			if !s.holdSimCapital(holdID, node, map[string]*big.Int{
				pair.BaseToken:  amount,
				pair.QuoteToken: buyNeed,
			}) {
				continue
			}
			sell, buy := s.submitGeneratedSpotCross(
				node,
				func(nonce uint64) *pb.AnyTx {
					return generateOrderTx(
						node.Address,
						pair.BaseToken,
						pair.QuoteToken,
						amount.String(),
						sellPrice.String(),
						nonce,
						pb.OrderSide_SELL,
					)
				},
				func(nonce uint64) *pb.AnyTx {
					return generateOrderTx(
						node.Address,
						pair.BaseToken,
						pair.QuoteToken,
						amount.String(),
						buyPrice.String(),
						nonce,
						pb.OrderSide_BUY,
					)
				},
			)
			if !sell.ok {
				s.releaseSimCapitalHold(holdID)
				continue
			}
			if !s.commitSimCapitalHold(holdID, pair.BaseToken, sell.nonce) {
				s.releaseSimCapitalHold(holdID)
				logs.Warn("Simulator: crossed SELL capital hold disappeared pair=%s nonce=%d", pairKey, sell.nonce)
			}
			logs.Trace("Simulator: Added crossed SELL order %s pair=%s (nonce=%d) from %s, amount=%s price=%s",
				sell.tx.GetBase().TxId, pairKey, sell.nonce, node.Address, amount.String(), sellPrice.String())
			cross := &simPendingSpotCross{
				node: node, pair: pair, holdID: holdID,
				amount: amount, buyPrice: buyPrice,
				sellOrder: newSimTrackedSpotOrder(sell.nonce, sell.tx),
			}
			if buy.ok {
				if !s.commitSimCapitalHold(holdID, pair.QuoteToken, buy.nonce) {
					s.releaseSimCapitalHold(holdID)
					logs.Warn("Simulator: crossed BUY capital hold disappeared pair=%s nonce=%d", pairKey, buy.nonce)
				}
				cross.buyOrder = newSimTrackedSpotOrder(buy.nonce, buy.tx)
				logs.Trace("Simulator: Added crossed BUY order %s pair=%s (nonce=%d) from %s, amount=%s price=%s",
					buy.tx.GetBase().TxId, pairKey, buy.nonce, node.Address, amount.String(), buyPrice.String())
			}
			crosses[pairKey] = cross
		}
	}
}

                                                                      
                                                                                                                                                                       
func (s *TxSimulator) RunMassOrderScenario(orderCount int) {
	if s.disableMassOrders.Load() {
		logs.Info("SIMULATOR: Mass Order Scenario disabled, skip")
		return
	}
	if orderCount <= 0 {
		orderCount = 6000                                        
	}

	logs.Info("SIMULATOR: Starting Mass Order Scenario with %d orders...", orderCount)

	nodes := s.usableSimNodes()
	if len(nodes) == 0 {
		logs.Error("SIMULATOR: No active nodes available for mass order scenario")
		return
	}
	pairs := s.tradingPairs
	if len(pairs) == 0 {
		logs.Warn("SIMULATOR: No spot trading pairs available, skip mass order scenario")
		return
	}

	startTime := time.Now()
	submitted := 0

	                     
	const batchSize = 100                                         
	const batchDelay = 50 * time.Millisecond                

	maxAttempts := orderCount * 20
	if maxAttempts < orderCount {
		maxAttempts = orderCount
	}
	for attempts := 0; submitted < orderCount && attempts < maxAttempts; attempts++ {
		                                          
		for s.paused.Load() {
			time.Sleep(1 * time.Second)
		}

		                           
		nodeIdx := attempts % len(nodes)
		node := nodes[nodeIdx]
		if node == nil {
			continue
		}

		pair := pairs[attempts%len(pairs)]
		basePrice := pair.randomPrice()
		amount := pair.randomAmount()
		isBuyOrder := mrand.Intn(2) == 0

		var side pb.OrderSide
		if isBuyOrder {
			side = pb.OrderSide_BUY
		} else {
			side = pb.OrderSide_SELL
		}

		_, _, ok := s.submitGeneratedTx(node, func(nonce uint64) *pb.AnyTx {
			return generateOrderTx(
				node.Address,
				pair.BaseToken,
				pair.QuoteToken,
				amount.String(),
				basePrice.String(),
				nonce,
				side,
			)
		})
		if !ok {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		submitted++

		                                                             
		if submitted%batchSize == 0 {
			logs.Info("SIMULATOR: Mass Order Progress: %d/%d submitted, pausing %v for gossip...",
				submitted, orderCount, batchDelay)
			time.Sleep(batchDelay)
		}
	}

	elapsed := time.Since(startTime)
	if submitted < orderCount {
		logs.Warn("SIMULATOR: Mass Order Scenario stopped early: %d/%d orders in %v (%.0f tx/s)",
			submitted, orderCount, elapsed, float64(submitted)/elapsed.Seconds())
		return
	}
	logs.Info("SIMULATOR: Mass Order Scenario completed: %d orders in %v (%.0f tx/s)",
		submitted, elapsed, float64(submitted)/elapsed.Seconds())
}

                                                                                
func (s *TxSimulator) RunMassOrderScenarioAsync(orderCount int) {
	go func() {
		                                 
		time.Sleep(30 * time.Second)
		s.RunMassOrderScenario(orderCount)
	}()
}

                                                       
type simulatorConfigDTO struct {
	Paused             bool  `json:"paused"`
	Transfers          bool  `json:"transfers"`
	WitnessReg         bool  `json:"witness_reg"`
	Recharge           bool  `json:"recharge"`
	WitnessVote        bool  `json:"witness_vote"`
	Withdraw           bool  `json:"withdraw"`
	Orders             bool  `json:"orders"`
	FutureOrders       bool  `json:"future_orders"`
	MassOrders         bool  `json:"mass_orders"`
	Arbitration        bool  `json:"arbitration"`
	TargetTxsPerBlock  int64 `json:"target_txs_per_block"`
	TargetTxsPerSecond int64 `json:"target_txs_per_second"`
	TargetPartitions   int64 `json:"target_partitions"`
	TargetPartitionID  int64 `json:"target_partition_id"`
	LocalTxsPerBlock   int64 `json:"local_txs_per_block"`
	LocalTxsPerSecond  int64 `json:"local_txs_per_second"`
	RateBlockSeconds   int64 `json:"rate_block_seconds"`
	WithdrawInterval   int64 `json:"withdraw_interval_seconds"`
}

func (s *TxSimulator) getConfig() simulatorConfigDTO {
	targetTxsPerBlock := simNormalizeTargetTxsPerBlock(s.targetTxsPerBlock.Load())
	localTxsPerBlock := s.effectiveTargetTxsPerBlock()
	return simulatorConfigDTO{
		Paused:             s.paused.Load(),
		Transfers:          !s.disableTransfers.Load(),
		WitnessReg:         !s.disableWitnessReg.Load(),
		Recharge:           !s.disableRecharge.Load(),
		WitnessVote:        !s.disableWitnessVote.Load(),
		Withdraw:           !s.disableWithdraw.Load(),
		Orders:             !s.disableOrders.Load(),
		FutureOrders:       !s.disableFutureOrders.Load(),
		MassOrders:         !s.disableMassOrders.Load(),
		Arbitration:        !s.disableArbitration.Load(),
		TargetTxsPerBlock:  targetTxsPerBlock,
		TargetTxsPerSecond: int64(simTargetTxPerSecond(targetTxsPerBlock, s.rateBlockSecondsValue())),
		TargetPartitions:   s.targetPartitions.Load(),
		TargetPartitionID:  s.targetPartitionID.Load(),
		LocalTxsPerBlock:   localTxsPerBlock,
		LocalTxsPerSecond:  int64(simEffectiveTargetTxPerSecond(localTxsPerBlock, s.rateBlockSecondsValue())),
		RateBlockSeconds:   s.rateBlockSecondsValue(),
		WithdrawInterval:   simNormalizeWithdrawIntervalSeconds(s.withdrawInterval.Load()),
	}
}

func (s *TxSimulator) applyConfig(dto simulatorConfigDTO) {
	s.setTargetTxsPerBlock(dto.TargetTxsPerBlock)
	if dto.RateBlockSeconds > 0 {
		s.setRateBlockSeconds(dto.RateBlockSeconds)
	}
	s.setWithdrawIntervalSeconds(dto.WithdrawInterval)
	s.disableTransfers.Store(!dto.Transfers)
	s.disableWitnessReg.Store(!dto.WitnessReg)
	s.disableRecharge.Store(!dto.Recharge)
	s.disableWitnessVote.Store(!dto.WitnessVote)
	s.disableWithdraw.Store(!dto.Withdraw)
	s.disableOrders.Store(!dto.Orders)
	s.disableFutureOrders.Store(!dto.FutureOrders)
	s.disableMassOrders.Store(!dto.MassOrders)
	s.disableArbitration.Store(!dto.Arbitration)
}

                                                                        
func (s *TxSimulator) HandleSimulatorConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s == nil {
		json.NewEncoder(w).Encode(map[string]string{"error": "simulator not running"})
		return
	}
	if r.Method == http.MethodPost {
		var dto simulatorConfigDTO
		if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		s.applyConfig(dto)
		logs.Info("SIMULATOR: Config updated via API: %+v", dto)
	}
	json.NewEncoder(w).Encode(s.getConfig())
}
