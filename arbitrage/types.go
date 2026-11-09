package arbitrage

import (
	"dex/pb"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

const (
	defaultMaxOrdersPerBook    = 64
	defaultMaxCycleEvaluations = 4096
	defaultCapitalBPS          = uint32(10_000)
	defaultTxFee               = "1000"
	minimumTxFeeRaw            = int64(1000)
	priceScaleRaw              = int64(100_000_000)
	                                                                          
	                                                                  
	                               
	maxClosedRouteLegs  = 3
	multiTokenPortfolio = "<multi-token>"
	                                                                        
	                                                                            
	                                                                         
	                                                                       
	portfolioUtilityPrecision = uint(512)
	                                                                           
	                                                                           
	                                                   
	crossPortfolioBeamWidth      = 16
	maxCrossPortfolioTransitions = 1_000_000
)

var (
	priceScale = big.NewInt(priceScaleRaw)
	maxUint256 = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	bigOne     = big.NewInt(1)
	bigTwo     = big.NewInt(2)
	bigTenK    = big.NewInt(10_000)
)

                                                                              
                                                                           
                                                                       
type EngineConfig struct {
	Enabled             bool   `json:"enabled"`
	ChainID             uint64 `json:"chainId"`
	TxFee               string `json:"txFee"`
	CapitalBPS          uint32 `json:"capitalBps"`
	MinProfitBPS        uint32 `json:"minProfitBps"`
	MaxOrdersPerBook    int    `json:"maxOrdersPerBook"`
	MaxCycleEvaluations int    `json:"maxCycleEvaluations"`
}

var engineConfigJSONFields = map[string]struct{}{
	"enabled":             {},
	"chainId":             {},
	"txFee":               {},
	"capitalBps":          {},
	"minProfitBps":        {},
	"maxOrdersPerBook":    {},
	"maxCycleEvaluations": {},
}

                                                                        
                                                                              
                                                                           
                       
func (cfg *EngineConfig) UnmarshalJSON(data []byte) error {
	if cfg == nil {
		return fmt.Errorf("nil arbitrage config")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for name := range fields {
		if _, allowed := engineConfigJSONFields[name]; allowed {
			continue
		}
		if isSecretLikeConfigField(name) {
			return fmt.Errorf("arbitrage config must not contain secret field %q; supply the private key through an approved runtime secret source", name)
		}
		return fmt.Errorf("unknown arbitrage config field %q", name)
	}

	                                                                           
	                                                    
	type engineConfigJSON EngineConfig
	decoded := engineConfigJSON(*cfg)
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*cfg = EngineConfig(decoded)
	return nil
}

func isSecretLikeConfigField(name string) bool {
	normalized := strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(name))
	switch normalized {
	case "privatekey", "secret", "secretkey", "mnemonic", "seed", "seedphrase", "password", "passphrase":
		return true
	default:
		return false
	}
}

                                                                           
                                                                       
type Leg struct {
	BaseToken  string
	QuoteToken string
	Side       pb.OrderSide
}

                                                                          
                                                                            
type StateReader interface {
	Get(key string) ([]byte, error)
}

                                                                        
                                                                       
                                                                 
type SignFunc func(tx *pb.AnyTx) (string, error)

                                                                   
type Context struct {
	ParentID          string
	Height            uint64
	Proposer          string
	Window            int
	MaxTxs            int
	PrivateTxLimit    int
	PublicPrefixCount int
	PublicPrefixHash  string
}

                                                                             
                                                           
type MarketSnapshot struct {
	PublicPrefixHash  string
	PublicPrefixCount int
	Levels            []MarketLevel
	Executions        []MarketExecution
	FutureLevels      []FutureMarketLevel
	FutureExecutions  []FutureMarketExecution
	FutureAvailable   map[string]string
}

type MarketLevel struct {
	Leg    Leg
	Price  string
	Makers []MarketMaker
}

type MarketMaker struct {
	OrderID   string
	Owner     string
	Remaining string
}

type MarketMatch struct {
	MakerOrderID string
	Quantity     string
	Price        string
}

type MarketExecution struct {
	PublicPosition int
	OrderID        string
	Owner          string
	Leg            Leg
	Price          string
	Amount         string
	Remaining      string
	Matches        []MarketMatch
	Levels         []MarketLevel
}

type FutureMarketLevel struct {
	Pair       string
	QuoteToken string
	Side       pb.OrderSide
	Price      string
	Makers     []MarketMaker
}

type FutureMarketExecution struct {
	PublicPosition int
	OrderID        string
	Owner          string
	Pair           string
	QuoteToken     string
	Side           pb.OrderSide
	Price          string
	Quantity       string
	Remaining      string
	Leverage       uint64
	Matches        []MarketMatch
	Levels         []FutureMarketLevel
	TakerAvailable map[string]string
}

                                                                              
                                                   
type CandidateBatch struct {
	ID                string
	Txs               []*pb.AnyTx
	InsertAfterPublic []int
	PublicPrefixHash  string
	PublicPrefixCount int
}

                                                                    
type ExecutionMatch struct {
	MakerOrderID string
	Quantity     string
	Price        string
}

                                                                            
                                                
type ExecutionReceipt struct {
	TxID           string
	Status         string
	Error          string
	Fee            string
	FBBalanceAfter string
	Matches        []ExecutionMatch
	Events         []ExecutionEvent
}

type ExecutionEvent struct {
	Kind    string
	Payload map[string]string
}

                                                  
type CandidateExecution struct {
	Valid    bool
	Reason   string
	Receipts []ExecutionReceipt
}

type bookOrder struct {
	orderID string
	owner   string
	remain  *big.Int
}

type topLevel struct {
	price  *big.Int
	orders []bookOrder
}

type plannedLeg struct {
	leg        Leg
	baseAmount *big.Int
	price      *big.Int
	inputSpent *big.Int
	output     *big.Int
	makerIDs   []string
	market     plannedMarket
	                                                                        
	                                                                            
	                                         
	futurePair     string
	futureLeverage uint64
	futureClose    bool
	futureGroup    string
}

type plannedMarket uint8

const (
	plannedMarketSpot plannedMarket = iota
	plannedMarketFuture
)

type plan struct {
	routeName   string
	startToken  string
	profitToken string
	startSpent  *big.Int
	finalOutput *big.Int
	profit      *big.Int
	roiBPS      *big.Int
	nonce       uint64
	legs        []plannedLeg
	                                                                        
	                                                                      
	residualProfit bool
	                                                                       
	                                                                               
	                                                        
	restoresBook bool
	                                                                           
	                                                                            
	                                                     
	groups  []plannedGroup
	utility *big.Rat
	                                                                     
	                                                                         
	                                                                     
	                                                                
	insertAfterPublic []int
	                                                                            
	                                                                      
	futureStartBalances map[string]*big.Int
}

type plannedGroup struct {
	routeName      string
	startToken     string
	profitToken    string
	startSpent     *big.Int
	finalOutput    *big.Int
	profit         *big.Int
	roiBPS         *big.Int
	residualProfit bool
	market         plannedMarket
	                                                                             
	                                                                      
	legIndices []int
}

type discoveredRoute struct {
	name       string
	startToken string
	legs       []Leg
	levels     []*topLevel
}

type normalizedConfig struct {
	EngineConfig
	fee        *big.Int
	capitalBPS uint32
}

                                                                            
func DefaultConfig() EngineConfig {
	return EngineConfig{
		Enabled:             false,
		TxFee:               defaultTxFee,
		CapitalBPS:          defaultCapitalBPS,
		MaxOrdersPerBook:    defaultMaxOrdersPerBook,
		MaxCycleEvaluations: defaultMaxCycleEvaluations,
	}
}

func ValidateConfig(cfg EngineConfig) error {
	_, err := normalizeConfig(cfg)
	return err
}

func normalizeConfig(cfg EngineConfig) (normalizedConfig, error) {
	cfg.TxFee = strings.TrimSpace(cfg.TxFee)
	if cfg.TxFee == "" {
		cfg.TxFee = defaultTxFee
	}
	fee, err := parseUint256("tx fee", cfg.TxFee)
	if err != nil || fee.Cmp(big.NewInt(minimumTxFeeRaw)) < 0 {
		if err == nil {
			err = fmt.Errorf("must be at least %d", minimumTxFeeRaw)
		}
		return normalizedConfig{}, fmt.Errorf("invalid arbitrage tx fee: %w", err)
	}
	capitalBPS := cfg.CapitalBPS
	if capitalBPS == 0 {
		capitalBPS = defaultCapitalBPS
	}
	if capitalBPS > defaultCapitalBPS {
		return normalizedConfig{}, fmt.Errorf("arbitrage capital_bps %d exceeds 10000", capitalBPS)
	}
	if cfg.MaxOrdersPerBook == 0 {
		cfg.MaxOrdersPerBook = defaultMaxOrdersPerBook
	}
	if cfg.MaxOrdersPerBook < 1 || cfg.MaxOrdersPerBook > 4096 {
		return normalizedConfig{}, fmt.Errorf("arbitrage max_orders_per_book must be in [1,4096]")
	}
	if cfg.MaxCycleEvaluations == 0 {
		cfg.MaxCycleEvaluations = defaultMaxCycleEvaluations
	}
	if cfg.MaxCycleEvaluations < 1 || cfg.MaxCycleEvaluations > 1_000_000 {
		return normalizedConfig{}, fmt.Errorf("arbitrage max_cycle_evaluations must be in [1,1000000]")
	}
	return normalizedConfig{EngineConfig: cfg, fee: fee, capitalBPS: capitalBPS}, nil
}

func legInputToken(leg Leg) string {
	if leg.Side == pb.OrderSide_BUY {
		return leg.QuoteToken
	}
	return leg.BaseToken
}

func legOutputToken(leg Leg) string {
	if leg.Side == pb.OrderSide_BUY {
		return leg.BaseToken
	}
	return leg.QuoteToken
}

type spotBookID struct {
	baseToken  string
	quoteToken string
	side       pb.OrderSide
}

func legBookID(leg Leg) spotBookID {
	return spotBookID{baseToken: leg.BaseToken, quoteToken: leg.QuoteToken, side: leg.Side}
}

func parseUint256(label, raw string) (*big.Int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return new(big.Int), nil
	}
	if len(raw) > 78 {
		return nil, fmt.Errorf("%s is too long", label)
	}
	v, ok := new(big.Int).SetString(raw, 10)
	if !ok || v.Sign() < 0 || v.BitLen() > 256 {
		return nil, fmt.Errorf("invalid %s %q", label, raw)
	}
	return v, nil
}
