package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"dex/cmd/explorer/indexdb"
	"dex/cmd/explorer/syncer"
	"dex/config"
	"dex/db"
	"dex/frost/chain/btc"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"dex/rent"
	statedb "dex/stateDB"

	"github.com/cockroachdb/pebble"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type server struct {
	client                  *http.Client
	gateway                 *nodeGateway
	recentTipCache          *boundedTTLCache[recentNodeTip]
	recentTxCache           *boundedTTLCache[[]*txRecord]
	tokenDirectoryCache     *boundedTTLCache[*tradingTokenDirectory]
	tokenDirectoryCacheOnce sync.Once
	tokenIconIdentityCache  *boundedTTLCache[tokenIconIdentity]
	tokenIconCache          *boundedTTLCache[tokenIconResult]
	tokenIconClient         *http.Client
	tokenIconClientOnce     sync.Once
	tokenIconFetchSlots     chan struct{}
	tokenIconDiskMu         sync.Mutex
	tokenIconDir            string
	withdrawMonitorCache    *boundedTTLCache[*frostWithdrawMonitorSnapshot]
	withdrawDetailCache     *boundedTTLCache[[]byte]
	withdrawCacheOnce       sync.Once
	withdrawMonitorHeight   atomic.Uint64
	withdrawRefreshEpoch    atomic.Uint64
	timeout                 time.Duration
	defaultNodes            []string
	basePort                int
	count                   int
	btcIntentURL            string
	btcIntentClient         *http.Client
	serviceToken            string

	                                                                                                    
	                                                                                          
	allowedNodes map[string]struct{}

	                                                                                        
	                                                                                 
	corsOrigins []string

	                              
	indexDB *indexdb.IndexDB
	syncer  *syncer.Syncer

	                                                                          
	nodeDB  *db.Manager
	nodeDBs []*db.Manager
	cfg     *config.Config
	chainID uint64

	                                                                        
	                                                                      
	simulatorAuth *simulatorOperatorAuth
}

const btcIntentURLEnv = "DEX_BTC_INTENT_URL"

func validateBTCIntentURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return err
	}
	if parsed.Scheme != "http" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "/v1/btc/deposit-intents" {
		return fmt.Errorf("BTC intent URL must be http://<loopback>/v1/btc/deposit-intents without credentials, query, or fragment")
	}
	ip := net.ParseIP(parsed.Hostname())
	if ip == nil || !ip.IsLoopback() || parsed.Port() == "" {
		return fmt.Errorf("BTC intent URL must use an explicit loopback IP and port")
	}
	return nil
}

type recentNodeTip struct {
	node   string
	height uint64
}

type tradingPairToken struct {
	Address        string `json:"address"`
	Type           string `json:"type"`
	NativeChain    string `json:"native_chain,omitempty"`
	NativeContract string `json:"native_contract,omitempty"`
	IconURL        string `json:"icon_url,omitempty"`
}

type tradingPairInfo struct {
	Pair       string `json:"pair"`                         
	BaseToken  string `json:"base_token"`                       
	QuoteToken string `json:"quote_token"`                       
	Activity   uint64 `json:"activity"`                                                           
}

const hotTradingPairLimit = 100

                                                                              
                                                                          
func selectRegisteredTradingPairs(tokens []tradingPairToken, registered []string) []tradingPairInfo {
	byAddress := make(map[string]tradingPairToken, len(tokens))
	for _, token := range tokens {
		if token.Address != "" {
			byAddress[token.Address] = token
		}
	}
	return selectRegisteredTradingPairsByAddress(byAddress, registered)
}

func selectRegisteredTradingPairsByAddress(byAddress map[string]tradingPairToken, registered []string) []tradingPairInfo {
	pairs := make([]tradingPairInfo, 0, len(registered))
	seen := make(map[string]struct{}, len(registered))
	for _, marketKey := range registered {
		marketKey = strings.TrimSpace(marketKey)
		if marketKey == "" {
			continue
		}
		if _, duplicate := seen[marketKey]; duplicate {
			continue
		}
		info, ok := resolveRegisteredTradingPair(marketKey, byAddress)
		if !ok {
			continue
		}
		seen[marketKey] = struct{}{}
		info.Pair = marketKey
		pairs = append(pairs, info)
	}
	return pairs
}

func resolveRegisteredTradingPair(marketKey string, byAddress map[string]tradingPairToken) (tradingPairInfo, bool) {
	build := func(base, quote tradingPairToken) (tradingPairInfo, bool) {
		if base.Address == "" || quote.Address == "" || base.Address == quote.Address {
			return tradingPairInfo{}, false
		}
		return tradingPairInfo{BaseToken: base.Address, QuoteToken: quote.Address}, true
	}
	                                                                         
	                                                                             
	                                                              
	var match tradingPairInfo
	matched := false
	for offset := 0; offset < len(marketKey); {
		relative := strings.IndexByte(marketKey[offset:], '_')
		if relative < 0 {
			break
		}
		split := offset + relative
		offset = split + 1
		if split == 0 || split == len(marketKey)-1 {
			continue
		}
		base, baseExists := byAddress[marketKey[:split]]
		quote, quoteExists := byAddress[marketKey[split+1:]]
		if !baseExists || !quoteExists {
			continue
		}
		candidate, ok := build(base, quote)
		if !ok {
			continue
		}
		if matched && (candidate.BaseToken != match.BaseToken || candidate.QuoteToken != match.QuoteToken) {
			return tradingPairInfo{}, false
		}
		match = candidate
		matched = true
	}
	return match, matched
}

func marketActivityFromBlock(block *pb.Block, market string) map[string]uint64 {
	activity := make(map[string]uint64)
	if block == nil {
		return activity
	}
	for _, tx := range block.Body {
		if tx == nil || tx.GetBase() == nil {
			continue
		}
		switch market {
		case "spot":
			order := tx.GetOrderTx()
			if order == nil || order.Op != pb.OrderOp_ADD || order.BaseToken == "" || order.QuoteToken == "" || order.BaseToken == order.QuoteToken {
				continue
			}
			activity[order.BaseToken+"_"+order.QuoteToken]++
		case "future":
			order := tx.GetFutureOrderTx()
			if order == nil || strings.TrimSpace(order.Pair) == "" {
				continue
			}
			activity[order.Pair]++
		}
	}
	return activity
}

func rankTradingPairs(pairs []tradingPairInfo, activity map[string]uint64, limit int) []tradingPairInfo {
	for i := range pairs {
		pairs[i].Activity = activity[pairs[i].Pair]
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].Activity != pairs[j].Activity {
			return pairs[i].Activity > pairs[j].Activity
		}
		return pairs[i].Pair < pairs[j].Pair
	})
	if limit > 0 && len(pairs) > limit {
		pairs = pairs[:limit]
	}
	return pairs
}

func (s *server) fetchLatestFinalizedMarketActivity(ctx context.Context, node, market string) (map[string]uint64, uint64, error) {
	height, err := s.fetchHeight(ctx, node)
	if err != nil {
		return nil, 0, err
	}
	block, err := s.fetchBlockByHeight(ctx, node, height.LastAcceptedHeight)
	if err != nil {
		return nil, 0, err
	}
	if block.Header == nil || block.Header.Height != height.LastAcceptedHeight {
		return nil, 0, fmt.Errorf("latest finalized block height mismatch")
	}
	return marketActivityFromBlock(block, market), height.LastAcceptedHeight, nil
}

type registeredMarketDirectory struct {
	Structured bool
	Markets    []tradingPairInfo
	PairKeys   []string
}

func (s *server) fetchRegisteredMarkets(ctx context.Context, node, path, label string) (registeredMarketDirectory, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://%s%s", node, path), nil)
	if err != nil {
		return registeredMarketDirectory{}, 0, err
	}
	resp, err := s.nodeDo(req)
	if err != nil {
		return registeredMarketDirectory{}, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := readResponseBody(resp)
		return registeredMarketDirectory{}, resp.StatusCode, fmt.Errorf("%s market directory returned status %d: %s", label, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	body, err := readResponseBody(resp)
	if err != nil {
		return registeredMarketDirectory{}, 0, err
	}
	var payload struct {
		Pairs   *[]string          `json:"pairs"`
		Markets *[]tradingPairInfo `json:"markets"`
		Error   string             `json:"error,omitempty"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return registeredMarketDirectory{}, 0, fmt.Errorf("decode %s market directory: %w", label, err)
	}
	if payload.Error != "" {
		return registeredMarketDirectory{}, 0, fmt.Errorf("%s market directory error: %s", label, payload.Error)
	}
	const maxRegisteredMarkets = 4096
	pairKeys := make([]string, 0)
	if payload.Pairs != nil {
		if len(*payload.Pairs) > maxRegisteredMarkets {
			return registeredMarketDirectory{}, http.StatusRequestEntityTooLarge, fmt.Errorf("%s market directory exceeds hard limit %d", label, maxRegisteredMarkets)
		}
		seenPairs := make(map[string]struct{}, len(*payload.Pairs))
		for _, pair := range *payload.Pairs {
			pair = strings.TrimSpace(pair)
			if pair == "" {
				continue
			}
			if _, duplicate := seenPairs[pair]; duplicate {
				continue
			}
			seenPairs[pair] = struct{}{}
			pairKeys = append(pairKeys, pair)
		}
	}
	if payload.Markets != nil {
		markets := *payload.Markets
		if len(markets) > maxRegisteredMarkets {
			return registeredMarketDirectory{}, http.StatusRequestEntityTooLarge, fmt.Errorf("%s market directory exceeds hard limit %d", label, maxRegisteredMarkets)
		}
		seen := make(map[string]struct{}, len(markets))
		validated := make([]tradingPairInfo, 0, len(markets))
		for _, market := range markets {
			market.Pair = strings.TrimSpace(market.Pair)
			market.BaseToken = strings.TrimSpace(market.BaseToken)
			market.QuoteToken = strings.TrimSpace(market.QuoteToken)
			market.Activity = 0
			if market.Pair == "" || market.BaseToken == "" || market.QuoteToken == "" || market.BaseToken == market.QuoteToken || market.Pair != market.BaseToken+"_"+market.QuoteToken {
				return registeredMarketDirectory{}, 0, fmt.Errorf("%s market directory contains invalid structured identity %q", label, market.Pair)
			}
			if _, duplicate := seen[market.Pair]; duplicate {
				continue
			}
			seen[market.Pair] = struct{}{}
			validated = append(validated, market)
		}
		for _, pair := range pairKeys {
			seen[pair] = struct{}{}
		}
		if len(seen) > maxRegisteredMarkets {
			return registeredMarketDirectory{}, http.StatusRequestEntityTooLarge, fmt.Errorf("%s market directory exceeds hard limit %d", label, maxRegisteredMarkets)
		}
		return registeredMarketDirectory{Structured: true, Markets: validated, PairKeys: pairKeys}, 0, nil
	}
	if payload.Pairs == nil {
		return registeredMarketDirectory{}, 0, fmt.Errorf("%s market directory is missing pairs and markets", label)
	}
	return registeredMarketDirectory{PairKeys: pairKeys}, 0, nil
}

func (s *server) fetchRegisteredFutureMarkets(ctx context.Context, node string) (registeredMarketDirectory, int, error) {
	return s.fetchRegisteredMarkets(ctx, node, "/future/markets", "future")
}

func (s *server) fetchRegisteredSpotMarkets(ctx context.Context, node string) (registeredMarketDirectory, int, error) {
	return s.fetchRegisteredMarkets(ctx, node, "/spot/markets", "spot")
}

func registerExplorerPublicOrderBookRoutes(mux *http.ServeMux, srv *server) {
	mux.HandleFunc("/api/orderbook", srv.handleOrderBook)
}

func registerExplorerAdminOrderBookRoutes(mux *http.ServeMux, srv *server) {
	mux.HandleFunc("/api/orderbook/debug", srv.handleOrderBookDebug)
}

type nodesResponse struct {
	BasePort int      `json:"base_port"`
	Count    int      `json:"count"`
	Nodes    []string `json:"nodes"`
}

type summaryRequest struct {
	Nodes        []string `json:"nodes"`
	IncludeBlock bool     `json:"include_block"`
	                                                                           
	                                                               
	IncludeFrost bool `json:"include_frost"`
}

const (
	explorerMaxSummaryNodes    = 64
	explorerMaxConfiguredNodes = 256
)

type summaryResponse struct {
	GeneratedAt string        `json:"generated_at"`
	Nodes       []nodeSummary `json:"nodes"`
	Errors      []string      `json:"errors,omitempty"`
	Selected    []string      `json:"selected"`
	ElapsedMs   int64         `json:"elapsed_ms"`
}

type nodeSummary struct {
	Address            string              `json:"address"`
	Status             string              `json:"status,omitempty"`
	Info               string              `json:"info,omitempty"`
	CurrentHeight      uint64              `json:"current_height,omitempty"`
	LastAcceptedHeight uint64              `json:"last_accepted_height,omitempty"`
	LatencyMs          int64               `json:"latency_ms,omitempty"`
	Error              string              `json:"error,omitempty"`
	Block              *blockSummary       `json:"block,omitempty"`
	PendingBlocksCount uint32              `json:"pending_blocks_count,omitempty"`                      
	PendingTxCount     uint32              `json:"pending_tx_count,omitempty"`                                       
	PendingHeaders     []pendingHeaderInfo `json:"pending_headers,omitempty"`                                       
	                     
	IsSyncing        bool   `json:"is_syncing,omitempty"`                                    
	SyncTargetHeight uint64 `json:"sync_target_height,omitempty"`                      
	IsSnapshotSync   bool   `json:"is_snapshot_sync,omitempty"`                              
	MinerAddress     string `json:"miner_address,omitempty"`                                        
	IsMiner          bool   `json:"is_miner"`
	LivenessState    int    `json:"liveness_state"`
}

type nodeDetails struct {
	nodeSummary
	RecentBlocks []blockHeaderInfo `json:"recent_blocks,omitempty"`
}

                                                                             
                                                                          
                                                 
type nodeDiagnostics struct {
	Address      string              `json:"address"`
	Resources    *systemResources    `json:"resources,omitempty"`
	BlockTiming  *blockTimingSummary `json:"block_timing,omitempty"`
	FrostMetrics *frostMetrics       `json:"frost_metrics,omitempty"`
	Logs         []*pb.LogLine       `json:"logs,omitempty"`
	IsPaused     *bool               `json:"is_paused,omitempty"`
	Errors       []string            `json:"errors,omitempty"`
}

type blockTimingSummary struct {
	Height                 uint64            `json:"height"`
	BlockID                string            `json:"block_id"`
	TxCount                int               `json:"tx_count"`
	RecordedAtUnixMs       int64             `json:"recorded_at_unix_ms"`
	TotalMs                int64             `json:"total_ms"`
	ConsensusMs            int64             `json:"consensus_ms"`
	PrepareMs              int64             `json:"prepare_ms"`
	LoadBlockMs            int64             `json:"load_block_ms"`
	VMCommitWaitMs         int64             `json:"vm_commit_wait_ms"`
	VMCommitMs             int64             `json:"vm_commit_ms"`
	VMBuildSpecMs          int64             `json:"vm_build_spec_ms,omitempty"`
	VMApplyAndFlushMs      int64             `json:"vm_apply_and_flush_ms,omitempty"`
	PoolRemoveMs           int64             `json:"pool_remove_ms"`
	MaterializeBlockMs     int64             `json:"materialize_block_ms"`
	SaveBlockMs            int64             `json:"save_block_ms"`
	FinalizeFlushMs        int64             `json:"finalize_flush_ms"`
	UpdateFinalizedStateMs int64             `json:"update_finalized_state_ms"`
	CleanupMs              int64             `json:"cleanup_ms"`
	CacheHit               bool              `json:"cache_hit"`
	LoadedFromDB           bool              `json:"loaded_from_db"`
	Error                  string            `json:"error,omitempty"`
	Steps                  []blockTimingStep `json:"steps,omitempty"`
}

type blockTimingStep struct {
	Seq          uint64             `json:"seq"`
	Name         string             `json:"name"`
	DurationMs   int64              `json:"duration_ms"`
	DurationText string             `json:"duration"`
	Fields       []blockTimingField `json:"fields,omitempty"`
}

type blockTimingField struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type blockSummary struct {
	Height        uint64         `json:"height"`
	BlockHash     string         `json:"block_hash,omitempty"`
	PrevBlockHash string         `json:"prev_block_hash,omitempty"`
	TxsHash       string         `json:"txs_hash,omitempty"`
	Miner         string         `json:"miner,omitempty"`
	TxCount       int            `json:"tx_count"`
	TxTypeCounts  map[string]int `json:"tx_type_counts,omitempty"`
	Accumulated   string         `json:"accumulated_reward,omitempty"`
	BlockReward   string         `json:"block_reward,omitempty"`
	Timestamp     int64          `json:"timestamp,omitempty"`
	Window        int32          `json:"window,omitempty"`
	StateRoot     string         `json:"state_root,omitempty"`                          
}

type frostMetrics struct {
	HeapAlloc      uint64            `json:"heap_alloc"`
	HeapSys        uint64            `json:"heap_sys"`
	NumGoroutine   int32             `json:"num_goroutine"`
	FrostJobs      int32             `json:"frost_jobs"`
	FrostWithdraws int32             `json:"frost_withdraws"`
	ApiCallStats   map[string]uint64 `json:"api_call_stats,omitempty"`
	ChannelStats   []channelStat     `json:"channel_stats,omitempty"`
}

type channelStat struct {
	Name   string  `json:"name"`
	Module string  `json:"module"`
	Len    int32   `json:"len"`
	Cap    int32   `json:"cap"`
	Usage  float64 `json:"usage"`
}

type systemResources struct {
	CollectedAtUnixMs     int64    `json:"collected_at_unix_ms,omitempty"`
	ProcessCPUPercent     *float64 `json:"process_cpu_pct,omitempty"`
	ProcessMemoryBytes    uint64   `json:"process_memory_bytes,omitempty"`
	GoHeapAllocBytes      uint64   `json:"go_heap_alloc_bytes,omitempty"`
	GoHeapSysBytes        uint64   `json:"go_heap_sys_bytes,omitempty"`
	GoHeapInuseBytes      uint64   `json:"go_heap_inuse_bytes,omitempty"`
	GoRoutines            int      `json:"go_routines,omitempty"`
	HostCPUPercent        *float64 `json:"host_cpu_pct,omitempty"`
	HostMemTotalBytes     uint64   `json:"host_mem_total_bytes,omitempty"`
	HostMemUsedBytes      uint64   `json:"host_mem_used_bytes,omitempty"`
	HostMemAvailableBytes uint64   `json:"host_mem_available_bytes,omitempty"`
	DiskPath              string   `json:"disk_path,omitempty"`
	DiskTotalBytes        uint64   `json:"disk_total_bytes,omitempty"`
	DiskUsedBytes         uint64   `json:"disk_used_bytes,omitempty"`
	DiskFreeBytes         uint64   `json:"disk_free_bytes,omitempty"`
	Error                 string   `json:"error,omitempty"`
}

                                                                                   
type pendingHeaderInfo struct {
	BlockID     string `json:"block_id"`
	Height      uint64 `json:"height"`
	Proposer    string `json:"proposer"`
	Window      int32  `json:"window"`
	Votes       int32  `json:"votes"`
	IsPreferred bool   `json:"is_preferred"`
}

                                         
type blockRequest struct {
	Node   string `json:"node"`
	Height uint64 `json:"height,omitempty"`
	Hash   string `json:"hash,omitempty"`
}

type blockResponse struct {
	Block *blockInfo `json:"block,omitempty"`
	Error string     `json:"error,omitempty"`
}

type blockInfo struct {
	Height            uint64                 `json:"height"`
	BlockHash         string                 `json:"block_hash"`
	PrevHash          string                 `json:"prev_block_hash,omitempty"`
	TxsHash           string                 `json:"txs_hash,omitempty"`
	Miner             string                 `json:"miner,omitempty"`
	TxCount           int                    `json:"tx_count"`
	Accumulated       string                 `json:"accumulated_reward,omitempty"`
	BlockReward       string                 `json:"block_reward,omitempty"`
	Timestamp         int64                  `json:"timestamp,omitempty"`
	Window            int32                  `json:"window,omitempty"`
	StateRoot         string                 `json:"state_root,omitempty"`                          
	Txs               []txSummary            `json:"transactions,omitempty"`
	FinalizationChits *finalizationChitsInfo `json:"finalization_chits,omitempty"`                         
	OfflineReports    []offlineReportInfo    `json:"offline_reports,omitempty"`                                            
}

                                                                       
                                                                                         
                                                          
type offlineReportInfo struct {
	Index   uint32 `json:"index"`
	Address string `json:"address,omitempty"`
}

                                                                
type finalizationChitsInfo struct {
	BlockID     string           `json:"block_id"`
	Height      uint64           `json:"height"`
	TotalVotes  int              `json:"total_votes"`
	TotalRounds int              `json:"total_rounds"`
	Rounds      []roundChitsInfo `json:"rounds,omitempty"`
	FinalizedAt int64            `json:"finalized_at"`
	Chits       []chitInfo       `json:"chits,omitempty"`
}

type roundChitsInfo struct {
	Round     int        `json:"round"`
	Timestamp int64      `json:"timestamp"`
	Votes     []chitInfo `json:"votes"`
}

type chitInfo struct {
	NodeID      string `json:"node_id"`
	PreferredID string `json:"preferred_id"`
	Timestamp   int64  `json:"timestamp"`
}

func explorerChitsObservedAt(chits *finalizationChitsInfo) int64 {
	if chits == nil {
		return 0
	}
	if chits.FinalizedAt > 0 {
		return chits.FinalizedAt
	}
	var latest int64
	for _, round := range chits.Rounds {
		if round.Timestamp > latest {
			latest = round.Timestamp
		}
		for _, vote := range round.Votes {
			if vote.Timestamp > latest {
				latest = vote.Timestamp
			}
		}
	}
	for _, chit := range chits.Chits {
		if chit.Timestamp > latest {
			latest = chit.Timestamp
		}
	}
	return latest
}

type txSummary struct {
	TxID        string `json:"tx_id"`
	TxType      string `json:"tx_type,omitempty"`
	FromAddress string `json:"from_address,omitempty"`
	ToAddress   string `json:"to_address,omitempty"`
	Value       string `json:"value,omitempty"`
	Status      string `json:"status,omitempty"`
	Fee         string `json:"fee,omitempty"`
	Nonce       uint64 `json:"nonce,omitempty"`
	Summary     string `json:"summary,omitempty"`
	Error       string `json:"error,omitempty"`
}

type txRequest struct {
	Node string `json:"node"`
	TxID string `json:"tx_id"`
}

type txResponse struct {
	Transaction *txInfo `json:"transaction,omitempty"`
	Error       string  `json:"error,omitempty"`
}

type txInfo struct {
	TxID           string                 `json:"tx_id"`
	TxType         string                 `json:"tx_type,omitempty"`
	FromAddress    string                 `json:"from_address,omitempty"`
	ToAddress      string                 `json:"to_address,omitempty"`
	Value          string                 `json:"value,omitempty"`
	Status         string                 `json:"status,omitempty"`
	Error          string                 `json:"error,omitempty"`
	ExecutedHeight uint64                 `json:"executed_height,omitempty"`
	Fee            string                 `json:"fee,omitempty"`
	Nonce          uint64                 `json:"nonce,omitempty"`
	Details        map[string]interface{} `json:"details,omitempty"`
}

                                
type addressRequest struct {
	Node          string `json:"node"`
	Address       string `json:"address"`
	RequireRemote bool   `json:"require_remote,omitempty"`
}

type addressResponse struct {
	Account *accountInfo `json:"account,omitempty"`
	Error   string       `json:"error,omitempty"`
}

type accountInfo struct {
	Address               string                   `json:"address"`
	Exists                bool                     `json:"exists"`
	Nonce                 uint64                   `json:"nonce"`
	Balances              map[string]*tokenBalance `json:"balances,omitempty"`
	TotalFBAsset          string                   `json:"total_fb_asset,omitempty"`
	FBBalanceBuckets      string                   `json:"fb_balance_buckets,omitempty"`
	FBIndexedClaims       string                   `json:"fb_indexed_claims,omitempty"`
	RentStatus            *rent.AccountRentStatus  `json:"rent_status,omitempty"`
	UnclaimedReward       string                   `json:"unclaimed_reward,omitempty"`
	IsMiner               bool                     `json:"is_miner"`
	MinerIdentityComplete bool                     `json:"miner_identity_complete"`
	IP                    string                   `json:"ip,omitempty"`
	Liveness              *livenessInfo            `json:"liveness,omitempty"`
	                                                                          
	RewardAddress string `json:"reward_address,omitempty"`
}

                                                                                         
                                                                              
                                                                                       
type livenessInfo struct {
	Index        uint32 `json:"index"`
	State        int32  `json:"state"`                                      
	StateName    string `json:"state_name"`                   
	Stake        uint64 `json:"stake"`
	JoinHeight   uint64 `json:"join_height"`
	ExitHeight   uint64 `json:"exit_height,omitempty"`
	TotalSlashed uint64 `json:"total_slashed,omitempty"`
	Kicked       bool   `json:"kicked,omitempty"`                                                    
}

type tokenBalance struct {
	Balance               string `json:"balance"`
	OrderFrozenBalance    string `json:"order_frozen_balance,omitempty"`
	MinerLockedBalance    string `json:"miner_locked_balance,omitempty"`
	LiquidLockedBalance   string `json:"liquid_locked_balance,omitempty"`
	WitnessLockedBalance  string `json:"witness_locked_balance,omitempty"`
	LeverageLockedBalance string `json:"leverage_locked_balance,omitempty"`
}

type explorerRentStore struct{ db *db.Manager }

func (s explorerRentStore) Get(key string) ([]byte, bool, error) {
	if s.db == nil {
		return nil, false, nil
	}
	data, err := s.db.Get(key)
	if errors.Is(err, statedb.ErrNotFound) || errors.Is(err, pebble.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func (s explorerRentStore) Scan(prefix string, limit int) (map[string][]byte, error) {
	out := make(map[string][]byte)
	if s.db == nil {
		return out, nil
	}
	kvs, err := s.db.ScanByPrefix(prefix, limit)
	if err != nil {
		return nil, err
	}
	for k, v := range kvs {
		out[k] = []byte(v)
	}
	return out, nil
}

                                                                                                                  
var (
	BuildCommit         = "unknown"
	BuildTime           = "unknown"
	BuildTag            = "dev"
	slowRequestLogState = struct {
		sync.Mutex
		last time.Time
	}{}
)

func boundedExplorerSyncMaxBlocks(value int) int {
	if value <= 0 {
		return syncer.DefaultMaxBlocksPerSync
	}
	return value
}

func boundedExplorerIndexRetentionHeights(value uint64) uint64 {
	if value == 0 {
		return syncer.DefaultIndexRetentionHeights
	}
	return value
}

func main() {
	logPath := strings.TrimSpace(os.Getenv("DEX_EXPLORER_LOG_FILE"))
	if logPath == "" {
		logPath = strings.TrimSpace(os.Getenv("DEX_LOG_FILE"))
	}
	if logPath == "" {
		logPath = "explorer.log"
	}
	rollingLog, err := logs.NewRollingWriter(logPath, logs.DefaultLogFileMaxBytes)
	if err != nil {
		log.Printf("warning: failed to enable bounded explorer log %s: %v", logPath, err)
	} else {
		defer rollingLog.Close()
		log.SetOutput(io.MultiWriter(os.Stderr, rollingLog))
	}
	log.Printf("explorer starting commit=%s time=%s tag=%s", BuildCommit, BuildTime, BuildTag)
	cfg := config.DefaultConfig()

	envFlag := flag.String("env", os.Getenv(config.EnvVarName), "runtime env name; defaults to DEX_ENV or dev")
	configDirFlag := flag.String("config-dir", "", "directory containing explorer/node env config")
	peersFlag := flag.String("peers", "", "path to peers.json; defaults to <config-dir>/peers.json")
	listenAddr := flag.String("listen", "127.0.0.1:8080", "Explorer HTTP listen address")
	host := flag.String("host", "127.0.0.1", "Default node host")
	basePort := flag.Int("base-port", cfg.Network.BasePort, "Base port for node list")
	                                                          
	count := flag.Int("count", cfg.Network.DefaultNumNodes+10, "Number of nodes")
	nodesFlag := flag.String("nodes", "", "Comma-separated node addresses (host:port)")
	operatorIdentitiesFlag := flag.String("operator-identities", "", "Path to an operator-local identities.json used for physical node routing; contains no private keys")
	webDirFlag := flag.String("web-dir", "", "Path to explorer static files")
	timeout := flag.Duration("timeout", 3*time.Second, "Per-request timeout")
	dataDir := flag.String("data-dir", "./explorer_data", "Path to explorer index database")
	syncNode := flag.String("sync-node", "", "Node to sync blocks from; empty selects the first resolved node")
	syncInterval := flag.Duration("sync-interval", 5*time.Second, "Block sync interval")
	syncMaxBlocks := flag.Int("sync-max-blocks", syncer.DefaultMaxBlocksPerSync, "Max blocks Explorer indexes per sync round; <=0 restores the finite default")
	nodeDataDir := flag.String("node-data-dir", "", "Path to node data directory for direct DB access (e.g., ./data/data_node_0)")
	maxInflight := flag.Int("max-inflight", 0, "Global Explorer HTTP work budget; <=0 uses a CPU-based default")
	maxConnections := flag.Int("max-connections", 0, "Maximum accepted public TCP connections (including idle keep-alives); <=0 derives from --max-inflight")
	maxAdminConnections := flag.Int("max-admin-connections", 16, "Maximum accepted TCP connections on the private admin listener")
	maxNodeConcurrent := flag.Int("max-node-concurrent", explorerDefaultNodeConcurrency, "Maximum concurrent Explorer-to-node requests")
	maxNodeDBs := flag.Int("max-node-dbs", 1, "Maximum locally opened node databases, including --node-data-dir; <=0 disables local DB access")
	indexRetentionHeights := flag.Uint64("index-retention-heights", syncer.DefaultIndexRetentionHeights, "Explorer index history to retain in blocks; 0 restores the finite default")
	indexPruneMaxHeights := flag.Int("index-prune-max-heights", 100, "Maximum Explorer index heights pruned per sync round")
	indexDiskPressureFreeBytes := flag.Uint64("index-disk-prune-free-bytes", syncer.DefaultIndexDiskPressureFreeBytes, "Prune additional old Explorer index heights when filesystem available space is below this byte threshold; 0 restores the 10 GiB default")
	indexDiskPressureKeepRecentHeights := flag.Uint64("index-disk-prune-keep-recent-heights", syncer.DefaultIndexDiskPressureKeepRecentHeights, "Minimum recent Explorer index heights retained during disk pressure; values below 100 restore the safe default")
	maxWSClients := flag.Int("max-ws-clients", defaultMaxWSClients, "Maximum concurrent frontend WebSocket clients")
	maxWSSubscriptions := flag.Int("max-ws-subs-per-client", defaultMaxWSSubs, "Maximum topic subscriptions per WebSocket client")
	maxWSSubscriptionGroups := flag.Int("max-ws-subscription-groups", defaultMaxWSSubscriptionGroups, "Maximum unique WebSocket topic+parameter groups")
	wsWorkers := flag.Int("ws-workers", defaultWSWorkers, "Maximum concurrent WebSocket subscription refreshes")
	maxEventStreams := flag.Int("max-event-streams", 32, "Maximum node event-stream connections")
	adminListen := flag.String("admin-listen", "127.0.0.1:8081", "Private Explorer admin listen address; empty disables admin endpoints")
	adminToken := flag.String("admin-token", os.Getenv("EXPLORER_ADMIN_TOKEN"), "Required X-Explorer-Admin-Token for the admin listener")
	simulatorOperatorAddresses := flag.String("simulator-operator-addresses", os.Getenv(simulatorOperatorAddressesEnv), "Comma-separated fbc1 accounts allowed to control the simulator and explicitly enabled Explorer operator features (max 64)")
	simulatorOperatorAddressesFile := flag.String("simulator-operator-addresses-file", "", "Path to a regular, non-symlink file containing one allowed fbc1 operator account per line; cannot be combined with --simulator-operator-addresses or its environment variable")
	enableOperatorNodeControl := flag.Bool("enable-operator-node-control", parseBool(os.Getenv(operatorNodeControlEnabledEnv)), "Expose wallet-authorized node controls and read-only diagnostics on the public Explorer listener")
	btcIntentURL := flag.String("btc-intent-url", strings.TrimSpace(os.Getenv(btcIntentURLEnv)), "Authenticated loopback endpoint for BTC deposit-intent registration")
	ipTLSEnabled := flag.Bool("ip-tls", true, "Enable optional background IP HTTPS certificate issuance and renewal; failure never blocks the normal HTTP backend")
	ipTLSIP := flag.String("ip-tls-ip", "", "One public IP to certify; empty discovers a unique public listener/interface address")
	ipTLSListen := flag.String("ip-tls-listen", ":8445", "Optional IP HTTPS listen address")
	ipTLSCache := flag.String("ip-tls-cache-dir", "", "Private IP TLS cache; defaults to <data-dir>/ip-tls")
	ipTLSDirectory := flag.String("ip-tls-acme-directory", defaultIPTLSDirectory, "ACME directory supporting the shortlived IP profile")
	flag.Parse()
	*syncMaxBlocks = boundedExplorerSyncMaxBlocks(*syncMaxBlocks)
	*indexRetentionHeights = boundedExplorerIndexRetentionHeights(*indexRetentionHeights)
	setFlags := make(map[string]bool)
	flag.Visit(func(f *flag.Flag) { setFlags[f.Name] = true })

	configDir := config.ResolveConfigDir(*envFlag, *configDirFlag)
	if envCfg, err := config.LoadFromDir(configDir); err == nil {
		cfg = envCfg
	} else {
		log.Printf("Warning: failed to load config from %s: %v", configDir, err)
	}
	genesisCfg, err := config.LoadGenesisConfig(config.ConfigPath(configDir, "genesis.json"))
	if err != nil {
		log.Printf("Warning: failed to load genesis config from %s: %v", configDir, err)
		genesisCfg = config.DefaultGenesisConfig()
	}
	if !setFlags["base-port"] {
		*basePort = cfg.Network.BasePort
	}
	if !setFlags["host"] && cfg.Network.AdvertiseHost != "" {
		*host = cfg.Network.AdvertiseHost
	}
	if !setFlags["count"] {
		*count = cfg.Network.DefaultNumNodes
	}

	explicitTopology := setFlags["config-dir"] || setFlags["peers"] || strings.TrimSpace(*operatorIdentitiesFlag) != ""
	defaultNodes, err := buildNodesFromConfig(
		*nodesFlag,
		*host,
		*basePort,
		*count,
		configDir,
		*peersFlag,
		*operatorIdentitiesFlag,
		explicitTopology,
		genesisCfg.EffectiveChainID(),
	)
	if err != nil {
		log.Fatalf("failed to resolve Explorer node topology: %v", err)
	}
	webDir := resolveWebDir(*webDirFlag)
	if webDir == "" {
		log.Fatal("unable to locate explorer web directory, use --web-dir")
	}

	client, transport := newHTTP3Client(*timeout)
	defer transport.Close()

	                           
	idb, err := indexdb.New(*dataDir)
	if err != nil {
		log.Fatalf("failed to open index database: %v", err)
	}
	defer idb.Close()

	                                                                  
	var nodeDB *db.Manager
	nodeDBs := make([]*db.Manager, 0)
	if *nodeDataDir != "" && *maxNodeDBs > 0 {
		nodeDB, err = db.NewReadOnlyManager(*nodeDataDir, logs.NewNodeLogger("explorer-db", 100))
		if err != nil {
			log.Printf("⚠️  Warning: failed to open node database in read-only mode: %v", err)
			log.Printf("   Explorer will rely on HTTP API for data access")
		} else {
			log.Printf("✅ Opened node database (read-only) from %s", *nodeDataDir)
			defer nodeDB.Close()
			nodeDBs = append(nodeDBs, nodeDB)
			for _, sibling := range discoverSiblingNodeDataDirs(*nodeDataDir) {
				if *maxNodeDBs > 0 && len(nodeDBs) >= *maxNodeDBs {
					log.Printf("Explorer local DB budget reached (%d); skipping remaining sibling databases", *maxNodeDBs)
					break
				}
				siblingDB, err := db.NewReadOnlyManager(sibling, logs.NewNodeLogger("explorer-db", 100))
				if err != nil {
					log.Printf("Warning: failed to open sibling node database %s: %v", sibling, err)
					continue
				}
				log.Printf("✅ Opened sibling node database (read-only) from %s", sibling)
				defer siblingDB.Close()
				nodeDBs = append(nodeDBs, siblingDB)
			}
		}
	}

	                     
	syncNodeAddr := *syncNode
	if syncNodeAddr == "" && len(defaultNodes) > 0 {
		syncNodeAddr = defaultNodes[0]
	}
	if syncNodeAddr != "" && !containsExplorerNode(defaultNodes, syncNodeAddr) {
		log.Fatalf("sync node %q is not present in the resolved Explorer topology", syncNodeAddr)
	}

	                  
	var blockSyncer *syncer.Syncer
	if syncNodeAddr != "" {
		blockSyncer = syncer.New(idb, "https://"+syncNodeAddr, *syncInterval)
		blockSyncer.SetMaxBlocksPerSync(*syncMaxBlocks)
		blockSyncer.SetIndexRetentionHeights(*indexRetentionHeights)
		blockSyncer.SetIndexPruneMaxHeights(*indexPruneMaxHeights)
		blockSyncer.SetIndexDiskPressureFreeBytes(*indexDiskPressureFreeBytes)
		blockSyncer.SetIndexDiskPressureKeepRecentHeights(*indexDiskPressureKeepRecentHeights)
		                                                                     
		if nodeDB != nil {
			blockSyncer.SetNodeDB(nodeDB)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if err := blockSyncer.Start(ctx); err != nil {
			log.Printf("Warning: failed to start syncer: %v", err)
		} else {
			log.Printf("Started block syncer, syncing from %s", syncNodeAddr)
		}
	} else if nodeDB != nil {
		                                                                              
		blockSyncer = syncer.New(idb, "", *syncInterval)
		blockSyncer.SetMaxBlocksPerSync(*syncMaxBlocks)
		blockSyncer.SetIndexRetentionHeights(*indexRetentionHeights)
		blockSyncer.SetIndexPruneMaxHeights(*indexPruneMaxHeights)
		blockSyncer.SetIndexDiskPressureFreeBytes(*indexDiskPressureFreeBytes)
		blockSyncer.SetIndexDiskPressureKeepRecentHeights(*indexDiskPressureKeepRecentHeights)
		blockSyncer.SetNodeDB(nodeDB)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if err := blockSyncer.Start(ctx); err != nil {
			log.Printf("Warning: failed to start local syncer: %v", err)
		} else {
			log.Printf("Started block syncer (local DB mode)")
		}
	}

	                                                              
	allowedNodes := make(map[string]struct{}, len(defaultNodes))
	for _, n := range defaultNodes {
		allowedNodes[n] = struct{}{}
	}
	gateway := newNodeGateway(client, defaultNodes, *maxNodeConcurrent)
	                                                                                     
	var corsOrigins []string
	if env := strings.TrimSpace(os.Getenv("EXPLORER_CORS_ORIGINS")); env != "" {
		for _, o := range strings.Split(env, ",") {
			if o = strings.TrimSpace(o); o != "" {
				corsOrigins = append(corsOrigins, o)
			}
		}
	}
	guard := newResourceGuard(*maxInflight)
	resolvedSimulatorOperatorAddresses, err := resolveSimulatorOperatorAddresses(
		*simulatorOperatorAddresses,
		*simulatorOperatorAddressesFile,
	)
	if err != nil {
		log.Fatalf("invalid Explorer operator address source: %v", err)
	}
	rawServiceToken := os.Getenv(config.ExplorerServiceTokenEnv)
	if strings.TrimSpace(rawServiceToken) != "" {
		if err := config.ValidateExplorerServiceToken(rawServiceToken); err != nil {
			log.Fatalf("invalid Explorer service token: %v", err)
		}
	}
	if strings.TrimSpace(*btcIntentURL) != "" {
		if err := validateBTCIntentURL(*btcIntentURL); err != nil {
			log.Fatalf("invalid BTC intent endpoint: %v", err)
		}
		if err := config.ValidateExplorerServiceToken(rawServiceToken); err != nil {
			log.Fatalf("BTC intent endpoint requires a valid Explorer service token: %v", err)
		}
	}
	if err := validateSimulatorOperatorServiceToken(resolvedSimulatorOperatorAddresses, rawServiceToken); err != nil {
		log.Fatalf("invalid Explorer operator service configuration: %v", err)
	}
	simulatorAuth, err := newSimulatorOperatorAuth(
		resolvedSimulatorOperatorAddresses,
		os.Getenv(simulatorPublicOriginEnv),
		genesisCfg.EffectiveChainID(),
		*enableOperatorNodeControl,
		guard.clientIP,
	)
	if err != nil {
		log.Fatalf("invalid Explorer operator authorization configuration: %v", err)
	}

	srv := &server{
		client:               client,
		gateway:              gateway,
		recentTipCache:       newBoundedTTLCache[recentNodeTip](len(defaultNodes)*2+1, 64<<10, 4),
		recentTxCache:        newBoundedTTLCache[[]*txRecord](128, 32<<20, 4),
		tokenDirectoryCache:  newBoundedTTLCache[*tradingTokenDirectory](maxInt(1, len(defaultNodes)), tradingTokenDirectoryCacheBytes, 2),
		tokenIconDir:         filepath.Join(*dataDir, "token-icons"),
		withdrawMonitorCache: newBoundedTTLCache[*frostWithdrawMonitorSnapshot](128, 32<<20, 4),
		withdrawDetailCache:  newBoundedTTLCache[[]byte](128, 16<<20, 4),
		timeout:              *timeout,
		defaultNodes:         defaultNodes,
		basePort:             *basePort,
		count:                len(defaultNodes),
		allowedNodes:         allowedNodes,
		corsOrigins:          corsOrigins,
		indexDB:              idb,
		syncer:               blockSyncer,
		nodeDB:               nodeDB,
		nodeDBs:              nodeDBs,
		cfg:                  cfg,
		chainID:              genesisCfg.EffectiveChainID(),
		simulatorAuth:        simulatorAuth,
		btcIntentURL:         strings.TrimSpace(*btcIntentURL),
		btcIntentClient: &http.Client{
			Transport: &http.Transport{Proxy: nil},
			Timeout:   *timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		serviceToken: strings.TrimSpace(rawServiceToken),
	}

	hub := newWSHub(srv, *maxWSClients, *maxWSSubscriptions, *maxWSSubscriptionGroups, *wsWorkers)
	hub.clientIP = guard.clientIP
	go hub.run()
	if blockSyncer != nil {
		                                                                    
		                                                                      
		blockSyncer.SetOnBlockIndexed(func(height uint64) {
			srv.markFrostWithdrawMonitorHeight(height)
			hub.TriggerTopic("my_closed_positions")
		})
	}

	                                                                                                      
	eventNodes := append([]string(nil), defaultNodes...)
	if syncNodeAddr != "" {
		found := false
		for _, n := range eventNodes {
			if n == syncNodeAddr {
				found = true
				break
			}
		}
		if !found {
			eventNodes = append(eventNodes, syncNodeAddr)
		}
	}
	if *maxEventStreams <= 0 {
		eventNodes = nil
	} else if len(eventNodes) > *maxEventStreams {
		                                                                         
		                                                                          
		                                                                 
		if syncNodeAddr != "" && !containsExplorerNode(eventNodes[:*maxEventStreams], syncNodeAddr) {
			eventNodes = append(eventNodes[:*maxEventStreams-1], syncNodeAddr)
		} else {
			eventNodes = eventNodes[:*maxEventStreams]
		}
	}
	nodeEvents := newNodeEventsClient(eventNodes)
	nodeEvents.Start()
	defer nodeEvents.Stop()
	go func() {
		for range nodeEvents.Signal() {
			hub.Trigger()
		}
	}()
	go func() {
		for msg := range nodeEvents.Events() {
			if msg.Type != "block.finalized" || msg.Height == 0 {
				continue
			}
			srv.markFrostWithdrawMonitorHeight(msg.Height)
			hub.TriggerTopic("withdraw_monitor_v2")
			if blockSyncer != nil {
				blockSyncer.Trigger(msg.Height)
			}
		}
	}()

	var ipTLS *ipTLSManager
	if *ipTLSEnabled {
		ip := strings.TrimSpace(*ipTLSIP)
		var ipErr error
		if ip == "" {
			ip, ipErr = discoverExplorerIP(*listenAddr)
		}
		if ipErr == nil {
			cache := strings.TrimSpace(*ipTLSCache)
			if cache == "" {
				cache = defaultExplorerIPTLSCache(*dataDir)
			}
			ipTLS, ipErr = newIPTLSManager(ipTLSOptions{Enabled: true, IP: ip, ListenAddr: *ipTLSListen, CacheDir: cache, DirectoryURL: *ipTLSDirectory})
		}
		if ipErr != nil {
			log.Printf("[IPTLS] optional IP HTTPS disabled; normal HTTP backend remains available: %v", ipErr)
		}
	}
	mux := http.NewServeMux()
	if ipTLS != nil {
		mux.Handle(ipTLSChallengePrefix, ipTLS.ChallengeHandler())
	}
	mux.HandleFunc("/api/ws", hub.handleWS)
	mux.HandleFunc("/api/nodes", srv.handleNodes)
	mux.HandleFunc("/api/summary", srv.handleSummary)
	mux.HandleFunc("/api/node/details", srv.handleNodeDetails)
	mux.HandleFunc("/api/block", srv.handleBlock)
	mux.HandleFunc("/api/tx", srv.handleTx)
	mux.HandleFunc("/api/address", srv.handleAddress)
	mux.HandleFunc("/api/txhistory", srv.handleTxHistory)
	mux.HandleFunc("/api/sync/status", srv.handleSyncStatus)
	mux.HandleFunc("/api/recentblocks", srv.handleRecentBlocks)                            
	mux.HandleFunc("/api/recenttxs", srv.handleRecentTxs)                                                          
	mux.HandleFunc("/api/frost/withdraw/queue", srv.handleFrostWithdrawQueue)
	mux.HandleFunc("/api/frost/withdraw/jobs", srv.handleFrostWithdrawJobs)
	mux.HandleFunc("/api/frost/withdraw/page", srv.handleFrostWithdrawMonitorPage)
	mux.HandleFunc("/api/frost/withdraw/detail", srv.handleFrostWithdrawDetail)
	mux.HandleFunc("/api/frost/withdraw/result", srv.handleFrostWithdrawResult)
	mux.HandleFunc("/api/frost/withdraw/batch", srv.handleFrostReadonlyV2Proxy("/frost/withdraw/batch"))
	mux.HandleFunc("/api/frost/btc/policy", srv.handleFrostReadonlyV2Proxy("/frost/btc/policy"))
	mux.HandleFunc("/api/frost/withdraw/parts", srv.handleFrostReadonlyV2Proxy("/frost/withdraw/parts"))
	mux.HandleFunc("/api/frost/withdraw/part", srv.handleFrostReadonlyV2Proxy("/frost/withdraw/part"))
	mux.HandleFunc("/api/frost/vault/migration/parts", srv.handleFrostReadonlyV2Proxy("/frost/vault/migration/parts"))
	mux.HandleFunc("/api/frost/vault/handover/page", srv.handleFrostReadonlyV2Proxy("/frost/vault/handover/page"))
	mux.HandleFunc("/api/witness/requests", srv.handleWitnessRequests)
	mux.HandleFunc("/api/witness/rewards", srv.handleFutureProxy("/witness/rewards"))
	mux.HandleFunc("/api/witness/my_requests", srv.handleWitnessMyRequests)
	mux.HandleFunc("/api/witness/list", srv.handleWitnessList)
	mux.HandleFunc("/api/witness/config", srv.handleFutureProxy("/witness/config"))
	mux.HandleFunc("/api/witness/challenge_quote", srv.handleFutureProxy("/witness/challenge_quote"))
	mux.HandleFunc("/api/frost/dkg/list", srv.handleFrostDKGSessions)
	mux.HandleFunc("/api/frost/dkg/progress", srv.handleFutureProxy("/frost/dkg/progress"))
	mux.HandleFunc("/api/frost/vault/migration/detail", srv.handleFrostMigrationDetail)
	mux.HandleFunc("/api/frost/vault/migration/download", srv.handleFutureProxy("/frost/vault/migration/download"))
	mux.HandleFunc("/api/frost/withdraw/availability", srv.handleFrostWithdrawAvailability)
	mux.HandleFunc("/api/frost/vault/list", srv.handleFrostVaults)
	mux.HandleFunc("/api/frost/vault/activity", srv.handleFrostVaultActivity)
	mux.HandleFunc("/api/frost/vault/utxos", srv.handleFrostVaultUTXOs)
	registerExplorerPublicOrderBookRoutes(mux, srv)
	mux.HandleFunc("/api/trades", srv.handleTrades)
	mux.HandleFunc("/api/klines", srv.handleKLines)
	mux.HandleFunc("/api/myorders", srv.handleMyOrders)
	                                       
	mux.HandleFunc("/api/referral/info", srv.handleFutureProxy("/referral/info"))
	mux.HandleFunc("/api/referral/rebates", srv.handleFutureProxy("/referral/rebates"))
	                                                                    
	mux.HandleFunc("/api/user/activity", srv.handleUserActivity)
	                                                                        
	mux.HandleFunc("/api/future/positions", srv.handleFuturePositionsReadModel)
	mux.HandleFunc("/api/future/market", srv.handleFutureProxy("/future/market"))
	mux.HandleFunc("/api/future/orderbook", srv.handleFutureProxy("/future/orderbook"))
	mux.HandleFunc("/api/future/trades", srv.handleFutureProxy("/future/trades"))
	mux.HandleFunc("/api/future/klines", srv.handleFutureKLines)
	mux.HandleFunc("/api/future/myorders", srv.handleFutureMyOrders)
	mux.HandleFunc("/api/pendingblocks", srv.handlePendingBlocks)
	                                                      
	mux.HandleFunc("/api/wallet/submittx", srv.handleWalletSubmitTx)
	mux.HandleFunc("/api/wallet/nonce", srv.handleWalletNonce)
	mux.HandleFunc("/api/wallet/receipt", srv.handleWalletReceipt)
	mux.HandleFunc("/api/wallet/deposit-address", srv.handleWalletDepositAddress)
	               
	mux.HandleFunc("/api/token", srv.handleTokenQuery)
	mux.HandleFunc("/api/tokenregistry", srv.handleTokenRegistryQuery)
	mux.HandleFunc("/api/tokens", srv.handleTokens)
	mux.HandleFunc("/api/token-icon", srv.handleTokenIcon)
	mux.HandleFunc("/api/tradingpairs", srv.handleTradingPairs)
	                                                  
	mux.HandleFunc("/api/cert/summary", srv.handleFutureProxy("/cert/summary"))
	registerSimulatorOperatorRoutes(mux, srv, simulatorAuth)
	mux.Handle("/", http.FileServer(http.Dir(webDir)))

	if *maxConnections <= 0 {
		effectiveMaxWSClients := *maxWSClients
		if effectiveMaxWSClients <= 0 {
			effectiveMaxWSClients = defaultMaxWSClients
		}
		                                                                     
		                                                                        
		                                                                        
		                                                                      
		*maxConnections = maxInt(256, cap(guard.global)*4)
		*maxConnections = maxInt(*maxConnections, effectiveMaxWSClients+256)
	}
	publicServer := &http.Server{
		Addr:              *listenAddr,
		Handler:           logRequests(srv.withPublicCORS(guard, mux)),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      20 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}

	var adminServer *http.Server
	if addr := strings.TrimSpace(*adminListen); addr != "" {
		if !isLoopbackListenAddress(addr) && strings.TrimSpace(*adminToken) == "" {
			log.Fatalf("refusing non-loopback admin listener %q without --admin-token", addr)
		}
		adminMux := http.NewServeMux()
		adminMux.HandleFunc("/api/node/pause", srv.handleNodePause)
		adminMux.HandleFunc("/api/debug/toggle_miner", srv.handleMinerToggleProxy)
		adminMux.Handle("/api/simulator/config", simulatorNoStoreHandler(http.HandlerFunc(srv.handleSimulatorConfig)))
		registerExplorerAdminOrderBookRoutes(adminMux, srv)
		adminServer = &http.Server{
			Addr:              addr,
			Handler:           logRequests(guard.WrapAdmin(adminMux, *adminToken)),
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      20 * time.Second,
			IdleTimeout:       60 * time.Second,
			MaxHeaderBytes:    16 << 10,
		}
	}

	log.Printf("Explorer public API listening at http://%s (ui: %s, data: %s)", *listenAddr, webDir, *dataDir)
	if adminServer != nil {
		log.Printf("Explorer admin API listening at http://%s", adminServer.Addr)
	}
	errCh := make(chan error, 2)
	go func() { errCh <- explorerServerError(serveExplorerWithConnectionLimit(publicServer, *maxConnections)) }()
	if adminServer != nil {
		go func() {
			errCh <- explorerServerError(serveExplorerWithConnectionLimit(adminServer, *maxAdminConnections))
		}()
	}
	if ipTLS != nil {
		ipTLS.Start(context.Background(), publicServer.Handler)
	}

	shutdownSignal, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	select {
	case err := <-errCh:
		if err != nil {
			log.Printf("Explorer server stopped: %v", err)
		}
	case <-shutdownSignal.Done():
		log.Printf("Explorer shutdown requested")
	}
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelShutdown()
	if ipTLS != nil {
		if err := ipTLS.Shutdown(shutdownCtx); err != nil {
			log.Printf("Explorer IP TLS shutdown: %v", err)
		}
	}
	if err := publicServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("Explorer public shutdown: %v", err)
	}
	if adminServer != nil {
		if err := adminServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("Explorer admin shutdown: %v", err)
		}
	}
	hub.Stop()
	if blockSyncer != nil {
		blockSyncer.Stop()
	}
}

func buildNodes(nodesFlag, host string, basePort, count int) []string {
	if strings.TrimSpace(nodesFlag) != "" {
		return normalizeNodes(strings.Split(nodesFlag, ","))
	}
	if count < 0 {
		count = 0
	}
	if count > explorerMaxConfiguredNodes {
		count = explorerMaxConfiguredNodes
	}
	nodes := make([]string, 0, count)
	for i := 0; i < count; i++ {
		nodes = append(nodes, fmt.Sprintf("%s:%d", host, basePort+i))
	}
	return nodes
}

func containsExplorerNode(nodes []string, node string) bool {
	for _, candidate := range nodes {
		if candidate == node {
			return true
		}
	}
	return false
}

func buildNodesFromConfig(nodesFlag, host string, basePort, count int, configDir, peersPath, operatorIdentitiesPath string, explicitTopology bool, expectedChainID uint64) ([]string, error) {
	if strings.TrimSpace(nodesFlag) != "" {
		nodes := buildNodes(nodesFlag, host, basePort, count)
		if len(nodes) == 0 {
			return nil, fmt.Errorf("--nodes did not contain any usable node endpoint")
		}
		return nodes, nil
	}
	if strings.TrimSpace(peersPath) == "" {
		peersPath = config.ConfigPath(configDir, "peers.json")
	}
	if strings.TrimSpace(operatorIdentitiesPath) != "" {
		manifest, err := config.LoadPeerManifest(peersPath)
		if err != nil {
			return nil, fmt.Errorf("load trusted peers manifest for --operator-identities: %w", err)
		}
		if expectedChainID == 0 || manifest.ChainID != expectedChainID {
			return nil, fmt.Errorf("trusted peers chain_id=%d does not match genesis chain_id=%d", manifest.ChainID, expectedChainID)
		}
		identities, err := config.LoadOperatorIdentityManifest(operatorIdentitiesPath, manifest)
		if err != nil {
			return nil, err
		}
		nodes := make([]string, 0, len(identities.Nodes))
		for _, identity := range identities.Nodes {
			nodes = append(nodes, identity.Endpoint)
		}
		return normalizeNodes(nodes), nil
	}
	peers, peerErr := config.LoadPeers(peersPath)
	if peerErr == nil && len(peers) > 0 {
		capacity := len(peers)
		if capacity > explorerMaxConfiguredNodes {
			capacity = explorerMaxConfiguredNodes
		}
		nodes := make([]string, 0, capacity)
		for _, p := range peers {
			if len(nodes) >= explorerMaxConfiguredNodes {
				break
			}
			if p.AdvertiseAddr != "" {
				nodes = append(nodes, p.AdvertiseAddr)
				continue
			}
			if p.Port != "" {
				nodes = append(nodes, fmt.Sprintf("%s:%s", host, p.Port))
			}
		}
		if len(nodes) > 0 {
			return normalizeNodes(nodes), nil
		}
	}
	if explicitTopology {
		if peerErr != nil {
			return nil, fmt.Errorf("load explicit peers manifest %s: %w", peersPath, peerErr)
		}
		return nil, fmt.Errorf("explicit peers manifest %s contains no usable endpoints", peersPath)
	}
	return buildNodes("", host, basePort, count), nil
}

func normalizeNodes(nodes []string) []string {
	unique := make(map[string]struct{})
	capacity := len(nodes)
	if capacity > explorerMaxConfiguredNodes {
		capacity = explorerMaxConfiguredNodes
	}
	out := make([]string, 0, capacity)
	for _, node := range nodes {
		n := strings.TrimSpace(node)
		if n == "" {
			continue
		}
		n = strings.TrimPrefix(n, "https://")
		n = strings.TrimPrefix(n, "http://")
		n = strings.TrimSuffix(n, "/")
		if _, exists := unique[n]; exists {
			continue
		}
		unique[n] = struct{}{}
		out = append(out, n)
		if len(out) >= explorerMaxConfiguredNodes {
			break
		}
	}
	return out
}

func discoverSiblingNodeDataDirs(nodeDataDir string) []string {
	nodeDataDir = filepath.Clean(nodeDataDir)
	parent := filepath.Dir(nodeDataDir)
	matches, err := filepath.Glob(filepath.Join(parent, "data_node_*"))
	if err != nil {
		return nil
	}
	sort.Strings(matches)
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		match = filepath.Clean(match)
		if match == nodeDataDir {
			continue
		}
		info, err := os.Stat(match)
		if err != nil || !info.IsDir() {
			continue
		}
		out = append(out, match)
	}
	return out
}

func resolveWebDir(explicit string) string {
	if explicit != "" {
		if dirExists(explicit) {
			return explicit
		}
		return ""
	}
	                                                                                     
	candidates := []string{
		filepath.Join("explorer_web", "dist"),
		filepath.Join("..", "explorer_web", "dist"),
		filepath.Join("..", "..", "explorer_web", "dist"),
		filepath.Join("explorer", "dist"),
		filepath.Join("..", "explorer", "dist"),
		filepath.Join("..", "..", "explorer", "dist"),
		"explorer",
		filepath.Join("..", "explorer"),
		filepath.Join("..", "..", "explorer"),
	}
	for _, candidate := range candidates {
		if dirExists(candidate) {
			return candidate
		}
	}
	return ""
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

func newHTTP3Client(timeout time.Duration) (*http.Client, *http3.Transport) {
	tlsCfg := config.ExplorerNodeTLSConfig()
	tlsCfg.MaxVersion = tls.VersionTLS13
	tlsCfg.ClientSessionCache = tls.NewLRUClientSessionCache(128)
	tlsCfg.NextProtos = []string{"h3", "h3-29", "h3-28", "h3-27"}

	tr := &http3.Transport{
		TLSClientConfig: tlsCfg,
		QUICConfig: &quic.Config{
			KeepAlivePeriod: 10 * time.Second,
			MaxIdleTimeout:  5 * time.Minute,
			Allow0RTT:       true,
		},
	}

	client := &http.Client{
		Transport: tr,
		Timeout:   timeout,
	}
	return client, tr
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		if elapsed := time.Since(start); elapsed >= 500*time.Millisecond && reserveSlowRequestLog(time.Now()) {
			                                                                        
			                                                                      
			                                  
			log.Printf("slow request %s %s %s", r.Method, r.URL.Path, elapsed.Truncate(time.Millisecond))
		}
	})
}

func reserveSlowRequestLog(now time.Time) bool {
	slowRequestLogState.Lock()
	defer slowRequestLogState.Unlock()
	if !slowRequestLogState.last.IsZero() && now.Sub(slowRequestLogState.last) < time.Second {
		return false
	}
	slowRequestLogState.last = now
	return true
}

                                                                             
                                                                           
func (s *server) pickNode(node string) string {
	resolved, ok := s.resolveNode(node)
	if !ok {
		return ""
	}
	return resolved
}

                                                                                  
                                                                                                
func (s *server) isNodeAllowed(node string) bool {
	if s != nil && s.gateway != nil {
		return s.gateway.IsAllowed(node)
	}
	node = normalizeExplorerNode(node)
	if node == "" {
		return false
	}
	                                                                            
	                                                                           
	                       
	if len(s.allowedNodes) == 0 {
		return true
	}
	_, ok := s.allowedNodes[node]
	return ok
}

                                                                                                
func (s *server) isOriginAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true                                                                           
	}
	                           
	if len(s.corsOrigins) > 0 {
		for _, o := range s.corsOrigins {
			if origin == o {
				return true
			}
		}
		return false
	}
	                                                                                        
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if u.Host == r.Host {
		return true
	}
	host := u.Hostname()
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func (s *server) handleNodes(w http.ResponseWriter, r *http.Request) {
	resp := nodesResponse{
		BasePort: s.basePort,
		Count:    s.count,
		Nodes:    s.defaultNodes,
	}
	writeJSON(w, resp)
}

func (s *server) handleSummary(w http.ResponseWriter, r *http.Request) {
	req := summaryRequest{}
	if r.Method == http.MethodPost {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			http.Error(w, "invalid json body", http.StatusBadRequest)
			return
		}
	} else {
		req.Nodes = strings.Split(r.URL.Query().Get("nodes"), ",")
		req.IncludeBlock = parseBool(r.URL.Query().Get("include_block"))
		req.IncludeFrost = parseBool(r.URL.Query().Get("include_frost"))
	}

	nodes := s.selectSummaryNodes(req.Nodes)
	if len(nodes) == 0 {
		nodes = s.selectSummaryNodes(s.defaultNodes)
	}

	start := time.Now()
	                                                                          
	                                                             
	results, errs := s.collectSummary(r.Context(), nodes, req.IncludeBlock)
	resp := summaryResponse{
		GeneratedAt: time.Now().Format(time.RFC3339),
		Nodes:       results,
		Errors:      errs,
		Selected:    nodes,
		ElapsedMs:   time.Since(start).Milliseconds(),
	}
	writeJSON(w, resp)
}

func (s *server) selectSummaryNodes(rawNodes []string) []string {
	capacity := len(rawNodes)
	if capacity > explorerMaxSummaryNodes {
		capacity = explorerMaxSummaryNodes
	}
	selected := make([]string, 0, capacity)
	seen := make(map[string]struct{}, explorerMaxSummaryNodes)
	for _, raw := range rawNodes {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		node := s.pickNode(raw)
		if node == "" {
			continue
		}
		if _, duplicate := seen[node]; duplicate {
			continue
		}
		seen[node] = struct{}{}
		selected = append(selected, node)
		if len(selected) == explorerMaxSummaryNodes {
			break
		}
	}
	return selected
}

func parseBool(value string) bool {
	if value == "" {
		return false
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false
	}
	return parsed
}

func (s *server) handleBlock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req blockRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, blockResponse{Error: "invalid request body"})
		return
	}

	var block *pb.Block
	var fetchErr error
	blockFromLocal := false

	                                       
	if req.Node != "" {
		ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
		defer cancel()

		if req.Hash != "" {
			block, fetchErr = s.fetchBlockByID(ctx, req.Node, req.Hash)
		} else {
			block, fetchErr = s.fetchBlockByHeight(ctx, req.Node, req.Height)
		}
	}

	                                                                   
	if block == nil && s.nodeDB != nil {
		if req.Hash != "" {
			block, fetchErr = localGetBlockByID(s.nodeDB, req.Hash)
		} else {
			block, fetchErr = localGetBlockByHeight(s.nodeDB, req.Height)
		}
		blockFromLocal = block != nil && fetchErr == nil
	}

	if fetchErr != nil || block == nil {
		errMsg := "block not found"
		if fetchErr != nil {
			errMsg = fetchErr.Error()
		}
		writeJSON(w, blockResponse{Error: errMsg})
		return
	}

	info := convertBlockToInfo(block)
	if blockFromLocal && block.Header != nil {
		reward := localBlockReward(s.nodeDB, block.Header.Height, info.Accumulated)
		info.Accumulated = reward
		info.BlockReward = reward
		info.Timestamp = localFinalizedObservedAt(s.nodeDB, block.Header.Height)
	}

	                                                            
	if req.Node != "" && block != nil && block.Header != nil {
		chitsCtx, chitsCancel := context.WithTimeout(r.Context(), s.timeout)
		defer chitsCancel()
		chits := s.fetchChits(chitsCtx, req.Node, block.Header.Height)
		if chits != nil {
			info.FinalizationChits = chits
			info.Timestamp = explorerChitsObservedAt(chits)
		}
	}

	                                                  
	                                                                                              
	if len(info.OfflineReports) > 0 && s.nodeDB != nil {
		for i := range info.OfflineReports {
			idx := uint64(info.OfflineReports[i].Index)
			if acct, err := s.nodeDB.GetMinerByIndex(idx); err == nil && acct != nil {
				info.OfflineReports[i].Address = acct.Address
			}
		}
	}

	writeJSON(w, blockResponse{Block: info})
}

                                                                
type blockHeaderInfo struct {
	Height      uint64 `json:"height"`
	BlockHash   string `json:"block_hash"`
	Miner       string `json:"miner,omitempty"`
	TxCount     int    `json:"tx_count"`
	Accumulated string `json:"accumulated_reward,omitempty"`
	BlockReward string `json:"block_reward,omitempty"`
	Timestamp   int64  `json:"timestamp,omitempty"`
	Window      int32  `json:"window,omitempty"`
	StateRoot   string `json:"state_root,omitempty"`
}

                                         
type recentBlocksRequest struct {
	Node  string `json:"node"`
	Count int    `json:"count,omitempty"`              
}

                                          
type recentBlocksResponse struct {
	Blocks []blockHeaderInfo `json:"blocks"`
	Error  string            `json:"error,omitempty"`
}

                                              
func (s *server) handleRecentBlocks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req recentBlocksRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, recentBlocksResponse{Error: "invalid request body"})
		return
	}

	count := req.Count
	if count <= 0 {
		count = 100
	}
	if count > 100 {
		count = 100
	}

	var blocks []blockHeaderInfo
	var fetchErr error

	                                       
	if req.Node != "" {
		ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
		defer cancel()

		if remoteBlocks, err := s.fetchRecentBlockHeaders(ctx, req.Node, count); err == nil {
			blocks = remoteBlocks
		} else {
			fetchErr = err
		}
	}

	                                                                   
	if blocks == nil && s.nodeDB != nil {
		localBlocks, err := localGetRecentBlocks(s.nodeDB, count)
		if err == nil {
			blocks = make([]blockHeaderInfo, 0, len(localBlocks))
			for _, b := range localBlocks {
				info := blockHeaderInfo{
					Height:      b.Header.Height,
					BlockHash:   b.BlockHash,
					Miner:       keys.MinerAddress(b.Header.Miner),
					TxCount:     len(b.Body),
					Accumulated: localBlockReward(s.nodeDB, b.Header.Height, b.AccumulatedReward),
					Timestamp:   localFinalizedObservedAt(s.nodeDB, b.Header.Height),
					Window:      b.Header.Window,
				}
				info.BlockReward = info.Accumulated
				if len(b.Header.StateRoot) > 0 {
					info.StateRoot = fmt.Sprintf("%x", b.Header.StateRoot)
				}
				blocks = append(blocks, info)
			}
			fetchErr = nil
		} else {
			fetchErr = err
		}
	}

	if fetchErr != nil {
		writeJSON(w, recentBlocksResponse{Error: fetchErr.Error()})
		return
	}

	if blocks == nil {
		blocks = []blockHeaderInfo{}
	}

	writeJSON(w, recentBlocksResponse{Blocks: blocks})
}

                                      
type recentTxsRequest struct {
	Node   string `json:"node"`
	TxType string `json:"tx_type,omitempty"`                      
	Count  int    `json:"count,omitempty"`              
}

                                       
type recentTxsResponse struct {
	Txs   []*txRecord `json:"txs"`
	Error string      `json:"error,omitempty"`
}

type txRecord struct {
	TxID        string `json:"tx_id"`
	TxType      string `json:"tx_type"`
	FromAddress string `json:"from_address,omitempty"`
	ToAddress   string `json:"to_address,omitempty"`
	Value       string `json:"value,omitempty"`
	Status      string `json:"status"`
	Fee         string `json:"fee,omitempty"`
	Nonce       uint64 `json:"nonce,omitempty"`
	Height      uint64 `json:"height"`
	TxIndex     int    `json:"tx_index"`
}

const (
	                                                                                                      
	recentTxFallbackMinBlocks = 8
	recentTxFallbackMaxBlocks = 64
	recentTxTipCacheTTL       = 750 * time.Millisecond
)

                                           
func (s *server) handleRecentTxs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req recentTxsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, recentTxsResponse{Error: "invalid request body"})
		return
	}
	count := req.Count
	if count <= 0 {
		count = 50
	}
	if count > 200 {
		count = 200
	}
	req.TxType = strings.TrimSpace(req.TxType)
	if len(req.TxType) > 128 {
		writeJSON(w, recentTxsResponse{Error: "transaction type is too long"})
		return
	}
	var indexRecs []*indexdb.TxRecord
	var indexQueryErr, indexHeightErr error
	var indexHeight uint64
	if s.indexDB != nil {
		indexRecs, indexQueryErr = s.indexDB.GetRecentTxs(req.TxType, count)
		if indexQueryErr == nil {
			indexHeight, indexHeightErr = s.indexDB.GetSyncHeight()
		}
	} else {
		indexQueryErr = errors.New("index db not available")
	}
	indexTxs := recentTxRecordsFromIndex(indexRecs)
	indexErr := indexQueryErr
	if indexErr == nil {
		indexErr = indexHeightErr
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()
	node, tip, tipErr := s.recentTxSourceTip(ctx, req.Node)
	if tipErr != nil {
		if indexQueryErr == nil && len(indexTxs) > 0 {
			writeJSON(w, recentTxsResponse{Txs: indexTxs})
			return
		}
		if indexErr != nil {
			writeJSON(w, recentTxsResponse{Error: fmt.Sprintf("index query failed: %v; node fallback failed: %v", indexErr, tipErr)})
		} else {
			writeJSON(w, recentTxsResponse{Error: fmt.Sprintf("node fallback failed: %v", tipErr)})
		}
		return
	}
	if indexQueryErr == nil && indexHeightErr == nil && indexHeight >= tip {
		writeJSON(w, recentTxsResponse{Txs: indexTxs})
		return
	}

	txs, fallbackErr := s.recentTxRecordsFromNodeTip(ctx, node, tip, req.TxType, count)
	if fallbackErr != nil {
		if indexErr != nil {
			writeJSON(w, recentTxsResponse{Error: fmt.Sprintf("index query failed: %v; node fallback failed: %v", indexErr, fallbackErr)})
		} else {
			writeJSON(w, recentTxsResponse{Error: fmt.Sprintf("node fallback failed: %v", fallbackErr)})
		}
		return
	}
	writeJSON(w, recentTxsResponse{Txs: txs})
}

func recentTxRecordsFromIndex(recs []*indexdb.TxRecord) []*txRecord {
	txs := make([]*txRecord, 0, len(recs))
	for _, rec := range recs {
		txs = append(txs, &txRecord{
			TxID:        rec.TxID,
			TxType:      rec.TxType,
			FromAddress: rec.FromAddress,
			ToAddress:   rec.ToAddress,
			Value:       rec.Value,
			Status:      rec.Status,
			Fee:         rec.Fee,
			Nonce:       rec.Nonce,
			Height:      rec.Height,
			TxIndex:     rec.TxIndex,
		})
	}
	return txs
}

func (s *server) recentTxSourceTip(ctx context.Context, node string) (string, uint64, error) {
	node = strings.TrimSpace(s.pickNode(node))
	if !s.isNodeAllowed(node) {
		return "", 0, fmt.Errorf("node %q is not allowed", node)
	}
	node = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(node, "https://"), "http://"), "/")
	load := func(loadCtx context.Context) (recentNodeTip, int64, error) {
		heightResp, err := s.fetchHeight(loadCtx, node)
		if err != nil {
			return recentNodeTip{}, 0, err
		}
		tip := heightResp.LastAcceptedHeight
		if tip == 0 {
			tip = heightResp.CurrentHeight
		}
		return recentNodeTip{node: node, height: tip}, 32, nil
	}
	if s.recentTipCache == nil {
		value, _, err := load(ctx)
		return value.node, value.height, err
	}
	value, err := s.recentTipCache.GetOrLoad(ctx, node, recentTxTipCacheTTL, func(context.Context) (recentNodeTip, int64, error) {
		                                                                      
		                                                                          
		                                                                
		timeout := s.timeout
		if timeout <= 0 {
			timeout = 3 * time.Second
		}
		loadCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		return load(loadCtx)
	})
	if err != nil {
		return "", 0, err
	}
	return value.node, value.height, nil
}

func (s *server) recentTxRecordsFromNode(ctx context.Context, node, txType string, limit int) ([]*txRecord, error) {
	node, tip, err := s.recentTxSourceTip(ctx, node)
	if err != nil {
		return nil, err
	}
	return s.recentTxRecordsFromNodeTip(ctx, node, tip, txType, limit)
}

func (s *server) recentTxRecordsFromNodeTip(ctx context.Context, node string, tip uint64, txType string, limit int) ([]*txRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	txType = strings.TrimSpace(txType)
	if tip == 0 {
		return []*txRecord{}, nil
	}
	if s.recentTxCache == nil {
		return s.loadRecentTxRecordsFromNodeTip(ctx, node, tip, txType, limit)
	}
	cacheKey := fmt.Sprintf("%s|%d|%s|%d", node, tip, txType, limit)
	return s.recentTxCache.GetOrLoad(ctx, cacheKey, time.Second, func(context.Context) ([]*txRecord, int64, error) {
		                                                                         
		                                                                          
		                                               
		timeout := s.timeout
		if timeout <= 0 {
			timeout = 3 * time.Second
		}
		loadCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		records, err := s.loadRecentTxRecordsFromNodeTip(loadCtx, node, tip, txType, limit)
		return records, recentTxRecordsCacheWeight(records), err
	})
}

func recentTxRecordsCacheWeight(records []*txRecord) int64 {
	weight := int64(1)
	for _, record := range records {
		if record == nil {
			continue
		}
		weight += int64(160 + len(record.TxID) + len(record.TxType) + len(record.FromAddress) + len(record.ToAddress) + len(record.Value) + len(record.Status) + len(record.Fee))
	}
	return weight
}

func (s *server) loadRecentTxRecordsFromNodeTip(ctx context.Context, node string, tip uint64, txType string, limit int) ([]*txRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	txType = strings.TrimSpace(txType)
	if tip == 0 {
		return []*txRecord{}, nil
	}
	maxScan := recentTxFallbackBlockLimit(limit)
	filterAll := txType == "" || txType == "all"
	results := make([]*txRecord, 0, limit)
	for height, scanned := tip, uint64(0); height > 0 && scanned < maxScan && len(results) < limit; height, scanned = height-1, scanned+1 {
		block, err := s.fetchBlockByHeight(ctx, node, height)
		if err != nil {
			return nil, fmt.Errorf("fetch block %d: %w", height, err)
		}
		if block == nil {
			return nil, fmt.Errorf("fetch block %d: empty block", height)
		}
		if block.Header == nil {
			return nil, fmt.Errorf("fetch block %d: empty header", height)
		}
		if block.Header.Height != height {
			return nil, fmt.Errorf("fetch block %d: source returned height %d", height, block.Header.Height)
		}
		for i := len(block.Body) - 1; i >= 0 && len(results) < limit; i-- {
			tx := block.Body[i]
			if tx == nil {
				continue
			}
			typ := indexdb.ExtractTxType(tx)
			if filterAll || typ == txType {
				results = append(results, recentTxRecordFromAnyTx(tx, typ, height, i))
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func recentTxFallbackBlockLimit(limit int) uint64 {
	if limit <= 0 {
		limit = 50
	}
	blocks := limit * 2
	if blocks < recentTxFallbackMinBlocks {
		blocks = recentTxFallbackMinBlocks
	}
	if blocks > recentTxFallbackMaxBlocks {
		blocks = recentTxFallbackMaxBlocks
	}
	return uint64(blocks)
}

func (s *server) handleTx(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req txRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, txResponse{Error: "invalid request body"})
		return
	}
	if req.TxID == "" {
		writeJSON(w, txResponse{Error: "tx_id is required"})
		return
	}

	var anyTx *pb.AnyTx
	var fetchErr error

	                                       
	if req.Node != "" {
		ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
		defer cancel()

		anyTx, fetchErr = s.fetchTxByID(ctx, req.Node, req.TxID)
	}

	                                                       
	if anyTx == nil && s.nodeDB != nil {
		anyTx, fetchErr = localGetAnyTxByID(s.nodeDB, req.TxID)
	}

	if fetchErr != nil || anyTx == nil {
		errMsg := "transaction not found"
		if fetchErr != nil {
			errMsg = fetchErr.Error()
		}
		writeJSON(w, txResponse{Error: errMsg})
		return
	}

	info := convertAnyTxToInfo(anyTx)

	var receipt *pb.Receipt
	if req.Node != "" {
		ctx2, cancel2 := context.WithTimeout(r.Context(), s.timeout)
		defer cancel2()
		receipt, _ = s.fetchReceipt(ctx2, req.Node, req.TxID)
	} else if s.nodeDB != nil {
		receipt, _ = localGetTxReceipt(s.nodeDB, req.TxID)
	}
	if receipt != nil {
		if receipt.Fee != "" {
			info.Fee = receipt.Fee
		}
		if receipt.Error != "" {
			info.Error = receipt.Error
		}
	}

	                                                 
	if orderTx := anyTx.GetOrderTx(); orderTx != nil {
		if req.Node != "" {
			ctx3, cancel3 := context.WithTimeout(r.Context(), s.timeout)
			defer cancel3()
			s.enrichTokenInfo(ctx3, req.Node, info, orderTx.BaseToken, orderTx.QuoteToken)
		} else if s.nodeDB != nil {
			                                   
			s.enrichTokenInfoLocal(info, orderTx.BaseToken, orderTx.QuoteToken)
		}
	}

	                                                                                                                 
	if anyTx.GetWitnessRequestTx() != nil && req.Node != "" {
		ctx4, cancel4 := context.WithTimeout(r.Context(), s.timeout)
		defer cancel4()
		s.enrichWitnessRequestInfo(ctx4, req.Node, info)
	}

	writeJSON(w, txResponse{Transaction: info})
}

                                          
func (s *server) fetchToken(ctx context.Context, node string, tokenAddress string) (*pb.Token, error) {
	var resp pb.GetTokenResponse
	req := &pb.GetTokenRequest{TokenAddress: tokenAddress}
	if err := s.fetchProto(ctx, node, "/gettoken", req, &resp); err != nil {
		return nil, err
	}
	if resp.Token == nil || resp.Token.Address != tokenAddress {
		return nil, fmt.Errorf("token not found or mismatched: %s", tokenAddress)
	}
	return resp.Token, nil
}

                                                                                                                                 
func (s *server) enrichTokenInfo(ctx context.Context, node string, info *txInfo, baseTokenAddr, quoteTokenAddr string) {
	if info == nil || info.Details == nil {
		return
	}

	                           
	if baseTokenAddr != "" {
		baseToken, err := s.fetchToken(ctx, node, baseTokenAddr)
		if err == nil && baseToken != nil {
			info.Details["base_token_meta"] = map[string]string{
				"address": baseTokenAddr,
			}
		}
	}

	                            
	if quoteTokenAddr != "" {
		quoteToken, err := s.fetchToken(ctx, node, quoteTokenAddr)
		if err == nil && quoteToken != nil {
			info.Details["quote_token_meta"] = map[string]string{
				"address": quoteTokenAddr,
			}
		}
	}
}

                                                            
func (s *server) enrichTokenInfoLocal(info *txInfo, baseTokenAddr, quoteTokenAddr string) {
	if info == nil || info.Details == nil || s.nodeDB == nil {
		return
	}

	if baseTokenAddr != "" {
		baseToken, err := localGetToken(s.nodeDB, baseTokenAddr)
		if err == nil && baseToken != nil {
			info.Details["base_token_meta"] = map[string]string{
				"address": baseTokenAddr,
			}
		}
	}

	if quoteTokenAddr != "" {
		quoteToken, err := localGetToken(s.nodeDB, quoteTokenAddr)
		if err == nil && quoteToken != nil {
			info.Details["quote_token_meta"] = map[string]string{
				"address": quoteTokenAddr,
			}
		}
	}
}

                                                                                            
func (s *server) enrichWitnessRequestInfo(ctx context.Context, node string, info *txInfo) {
	if info == nil || info.Details == nil || info.TxID == "" {
		return
	}
	var resp pb.RechargeRequestList
	path := "/witness/requests?request_id=" + url.QueryEscape(info.TxID)
	if err := s.fetchProto(ctx, node, path, nil, &resp); err != nil {
		return
	}
	for _, req := range resp.Requests {
		if req.RequestId != info.TxID {
			continue
		}
		if len(req.SelectedWitnesses) > 0 {
			info.Details["selected_witnesses"] = req.SelectedWitnesses
		}
		info.Details["recharge_status"] = req.Status.String()
		if req.PassCount > 0 || req.FailCount > 0 || req.AbstainCount > 0 {
			info.Details["pass_count"] = req.PassCount
			info.Details["fail_count"] = req.FailCount
			info.Details["abstain_count"] = req.AbstainCount
		}
		if len(req.Votes) > 0 {
			votes := make([]map[string]interface{}, 0, len(req.Votes))
			for _, v := range req.Votes {
				votes = append(votes, map[string]interface{}{
					"witness_address": v.WitnessAddress,
					"vote_type":       v.VoteType.String(),
					"reason":          v.Reason,
				})
			}
			info.Details["witness_votes"] = votes
		}
		break
	}
}

func (s *server) fetchBlockByHeight(ctx context.Context, node string, height uint64) (*pb.Block, error) {
	var resp pb.GetBlockResponse
	req := &pb.GetBlockRequest{Height: height}
	if err := s.fetchProto(ctx, node, "/getblock", req, &resp); err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	if resp.Block == nil {
		return nil, errors.New("block not found")
	}
	return resp.Block, nil
}

func (s *server) fetchBlockByID(ctx context.Context, node string, blockID string) (*pb.Block, error) {
	var resp pb.GetBlockResponse
	req := &pb.GetBlockByIDRequest{BlockId: blockID}
	if err := s.fetchProto(ctx, node, "/getblockbyid", req, &resp); err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	if resp.Block == nil {
		return nil, errors.New("block not found")
	}
	return resp.Block, nil
}

func (s *server) fetchTxByID(ctx context.Context, node string, txID string) (*pb.AnyTx, error) {
	var resp pb.AnyTx
	req := &pb.GetData{TxId: txID}

	if err := s.fetchProto(ctx, node, "/getdata", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (s *server) fetchReceipt(ctx context.Context, node string, txID string) (*pb.Receipt, error) {
	var resp pb.Receipt
	req := &pb.GetData{TxId: txID}

	if err := s.fetchProto(ctx, node, "/gettxreceipt", req, &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

func (s *server) handleAddress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req addressRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, addressResponse{Error: "invalid request body"})
		return
	}
	if req.Address == "" {
		writeJSON(w, addressResponse{Error: "address is required"})
		return
	}

	var account *pb.Account
	var balances map[string]*pb.TokenBalance
	var liveness *pb.MinerLivenessInfo
	var fbAsset *rent.AssetSummary
	var rentStatus *rent.AccountRentStatus
	var fetchErr error
	remoteNotFound := false
	node, hasNode := s.resolveNode(req.Node)
	if strings.TrimSpace(req.Node) != "" && !hasNode {
		writeJSON(w, addressResponse{Error: "node is not allowed"})
		return
	}
	if req.RequireRemote && !hasNode {
		writeJSON(w, addressResponse{Error: "an authoritative node is required"})
		return
	}

	                                       
	if hasNode {
		ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
		defer cancel()

		account, liveness, fetchErr = s.fetchAccountByAddress(ctx, node, req.Address)
		remoteNotFound = isAccountNotFoundError(fetchErr)
		if fetchErr == nil {
			var err error
			balances, err = s.fetchAccountBalances(ctx, node, req.Address)
			if err != nil {
				writeJSON(w, addressResponse{Error: err.Error()})
				return
			}
			asset, status, err := s.fetchAccountAsset(ctx, node, req.Address, rent.FeeToken)
			if err != nil {
				writeJSON(w, addressResponse{Error: fmt.Sprintf("failed to fetch account rent status: %v", err)})
				return
			}
			fbAsset = asset
			rentStatus = status
		}
	}

	                                                          
	                                                                                       
	                                                                                     
	if account == nil && s.nodeDB != nil && !remoteNotFound && !req.RequireRemote {
		localAccount, localErr := localGetAccount(s.nodeDB, req.Address)
		if localErr == nil {
			account = localAccount
			fetchErr = nil
			balances = localGetAccountBalances(s.nodeDB, req.Address)
			var assetErr error
			fbAsset, rentStatus, assetErr = s.localAccountAsset(req.Address, rent.FeeToken)
			if assetErr != nil {
				writeJSON(w, addressResponse{Error: fmt.Sprintf("failed to read account rent status: %v", assetErr)})
				return
			}
		} else if fetchErr == nil {
			fetchErr = localErr
		}
	}

	if fetchErr != nil && account == nil {
		                                                                               
		if !isAccountNotFoundError(fetchErr) {
			writeJSON(w, addressResponse{Error: fetchErr.Error()})
			return
		}
	}

	if account == nil {
		if !hasNode && s.nodeDB == nil {
			writeJSON(w, addressResponse{Error: "node is not configured"})
			return
		}
		                                                                                          
		                                                                        
		writeJSON(w, addressResponse{Account: &accountInfo{
			Address:          req.Address,
			Exists:           false,
			Nonce:            0,
			Balances:         map[string]*tokenBalance{},
			TotalFBAsset:     "0",
			FBBalanceBuckets: "0",
			FBIndexedClaims:  "0",
		}})
		return
	}

	if fbAsset == nil && s.nodeDB != nil {
		var err error
		fbAsset, rentStatus, err = s.localAccountAsset(req.Address, rent.FeeToken)
		if err != nil {
			writeJSON(w, addressResponse{Error: fmt.Sprintf("failed to read account rent status: %v", err)})
			return
		}
	}
	info := convertAccountToInfo(account, balances, liveness, fbAsset, rentStatus)
	writeJSON(w, addressResponse{Account: info})
}

func isAccountNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, statedb.ErrNotFound) || errors.Is(err, pebble.ErrNotFound) {
		return true
	}
	message := strings.TrimSpace(err.Error())
	if message == "account not found" {
		return true
	}
	const legacyPrefix = "account not found:"
	if !strings.HasPrefix(message, legacyPrefix) {
		return false
	}
	cause := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(message, legacyPrefix)))
	return cause == "not found" || strings.HasSuffix(cause, ": not found")
}

func (s *server) fetchAccountByAddress(ctx context.Context, node string, address string) (*pb.Account, *pb.MinerLivenessInfo, error) {
	var resp pb.GetAccountResponse
	req := &pb.GetAccountRequest{Address: address}
	if err := s.fetchProto(ctx, node, "/getaccount", req, &resp); err != nil {
		return nil, nil, err
	}
	if resp.Error != "" {
		return nil, nil, fmt.Errorf("%s", resp.Error)
	}
	if resp.Account == nil {
		return nil, nil, errors.New("account not found")
	}
	return resp.Account, resp.Liveness, nil
}

                                                                  
func (s *server) fetchAccountBalances(ctx context.Context, node string, address string) (map[string]*pb.TokenBalance, error) {
	var resp pb.GetAccountBalancesResponse
	req := &pb.GetAccountBalancesRequest{Address: address}
	if err := s.fetchProto(ctx, node, "/getaccountbalances", req, &resp); err != nil {
		return nil, fmt.Errorf("failed to fetch account balances: %w", err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("failed to fetch account balances: %s", resp.Error)
	}
	return resp.Balances, nil
}

func (s *server) fetchAccountAsset(ctx context.Context, node, address, token string) (*rent.AssetSummary, *rent.AccountRentStatus, error) {
	node = s.pickNode(node)
	if node == "" {
		return nil, nil, fmt.Errorf("node is not allowed")
	}
	u := fmt.Sprintf("https://%s/account/asset?address=%s&token=%s", node, url.QueryEscape(address), url.QueryEscape(token))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Node-Address", "explorer-api")
	resp, err := s.nodeDo(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, fmt.Errorf("status %d: %s", resp.StatusCode, readNodeErrorMessage(resp))
	}
	data, err := readResponseBody(resp)
	if err != nil {
		return nil, nil, err
	}
	var out struct {
		Owner          string                  `json:"owner"`
		Token          string                  `json:"token"`
		BalanceBuckets string                  `json:"balance_buckets"`
		IndexedClaims  string                  `json:"indexed_claims"`
		Total          string                  `json:"total"`
		Rent           *rent.AccountRentStatus `json:"rent"`
		Error          string                  `json:"error"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, nil, err
	}
	if out.Error != "" {
		return nil, nil, errors.New(out.Error)
	}
	return &rent.AssetSummary{
		Owner:          out.Owner,
		Token:          out.Token,
		BalanceBuckets: out.BalanceBuckets,
		IndexedClaims:  out.IndexedClaims,
		Total:          out.Total,
	}, out.Rent, nil
}

func (s *server) localAccountAsset(address, token string) (*rent.AssetSummary, *rent.AccountRentStatus, error) {
	if s.nodeDB == nil {
		return nil, nil, nil
	}
	svc := rent.NewService(explorerRentStore{db: s.nodeDB})
	summary, err := svc.NominalAsset(address, token)
	if err != nil {
		return nil, nil, err
	}
	var status *rent.AccountRentStatus
	if token == rent.FeeToken {
		status, err = svc.RentStatus(address, s.nodeDB.GetCurrentHeight())
		if err != nil {
			return nil, nil, err
		}
	}
	return summary, status, nil
}

func convertAccountToInfo(account *pb.Account, balances map[string]*pb.TokenBalance, liveness *pb.MinerLivenessInfo, fbAsset *rent.AssetSummary, rentStatus *rent.AccountRentStatus) *accountInfo {
	if account == nil {
		return nil
	}
	info := &accountInfo{
		Address:         account.Address,
		Exists:          true,
		Nonce:           account.Nonce,
		UnclaimedReward: account.UnclaimedReward,
		IsMiner:         account.IsMiner,
		MinerIdentityComplete: account.PublicKeys != nil &&
			len(account.PublicKeys.Keys[int32(pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1_DEX)]) > 0 &&
			len(account.PublicKeys.Keys[int32(pb.SignAlgo_SIGN_ALGO_BLS_BN256_G2)]) > 0 &&
			strings.TrimSpace(account.Ip) != "",
		IP:            account.Ip,
		RewardAddress: account.RewardAddress,
		Balances:      make(map[string]*tokenBalance),
		RentStatus:    rentStatus,
	}
	if fbAsset != nil {
		info.TotalFBAsset = fbAsset.Total
		info.FBBalanceBuckets = fbAsset.BalanceBuckets
		info.FBIndexedClaims = fbAsset.IndexedClaims
	} else if balances != nil {
		info.TotalFBAsset = rent.TokenBalanceTotal(balances[rent.FeeToken]).String()
		info.FBBalanceBuckets = info.TotalFBAsset
		info.FBIndexedClaims = "0"
	}

	                                          
	for token, bal := range balances {
		if bal != nil {
			info.Balances[token] = &tokenBalance{
				Balance:               bal.Balance,
				OrderFrozenBalance:    bal.OrderFrozenBalance,
				MinerLockedBalance:    bal.MinerLockedBalance,
				LiquidLockedBalance:   bal.LiquidLockedBalance,
				WitnessLockedBalance:  bal.WitnessLockedBalance,
				LeverageLockedBalance: bal.LeverageLockedBalance,
			}
		}
	}

	                                                                                                    
	                                                                       
	if liveness != nil {
		info.Liveness = &livenessInfo{
			Index:        liveness.Index,
			State:        liveness.State,
			StateName:    liveness.StateName,
			Stake:        liveness.Stake,
			JoinHeight:   liveness.JoinHeight,
			ExitHeight:   liveness.ExitHeight,
			TotalSlashed: liveness.TotalSlashed,
			Kicked:       liveness.Kicked,
		}
	}

	return info
}

func convertBlockToInfo(block *pb.Block) *blockInfo {
	if block == nil {
		return nil
	}
	info := &blockInfo{
		Height:      block.Header.Height,
		BlockHash:   block.BlockHash,
		PrevHash:    block.Header.PrevBlockHash,
		TxsHash:     block.Header.TxsHash,
		Miner:       keys.MinerAddress(block.Header.Miner),
		TxCount:     len(block.Body),
		Accumulated: block.AccumulatedReward,
		BlockReward: block.AccumulatedReward,
		Window:      block.Header.Window,
		Txs:         make([]txSummary, 0, len(block.Body)),
	}
	                                                        
	if len(block.Header.StateRoot) > 0 {
		info.StateRoot = fmt.Sprintf("%x", block.Header.StateRoot)
	}
	                                                                                                 
	                                                                     
	if len(block.Header.OfflineReports) > 0 {
		info.OfflineReports = make([]offlineReportInfo, 0, len(block.Header.OfflineReports))
		for _, idx := range block.Header.OfflineReports {
			info.OfflineReports = append(info.OfflineReports, offlineReportInfo{Index: idx})
		}
	}
	for _, tx := range block.Body {
		summary := convertAnyTxToSummary(tx)
		info.Txs = append(info.Txs, summary)
	}
	return info
}

func convertAnyTxToSummary(tx *pb.AnyTx) txSummary {
	if tx == nil {
		return txSummary{}
	}
	summary := txSummary{
		TxID:   tx.GetTxId(),
		TxType: indexdb.ExtractTxType(tx),
	}
	if base := tx.GetBase(); base != nil {
		summary.FromAddress = base.FromAddress
		summary.Status = base.Status.String()
		summary.Fee = base.Fee
		summary.Nonce = base.Nonce
		                                       
		                                                                               
		                                                                                                   
		                                                                                                          
	}
	                              
	toAddr, value := indexdb.ExtractToAndValue(tx)
	summary.ToAddress = toAddr
	summary.Value = value
	summary.Summary = generateTxSummary(tx)
	return summary
}

func convertAnyTxToInfo(tx *pb.AnyTx) *txInfo {
	if tx == nil {
		return nil
	}
	info := &txInfo{
		TxID:    tx.GetTxId(),
		TxType:  indexdb.ExtractTxType(tx),
		Details: make(map[string]interface{}),
	}
	if base := tx.GetBase(); base != nil {
		info.FromAddress = base.FromAddress
		info.Status = base.Status.String()
		info.ExecutedHeight = base.ExecutedHeight
		info.Fee = base.Fee
		info.Nonce = base.Nonce
	}
	                              
	toAddr, value := indexdb.ExtractToAndValue(tx)
	info.ToAddress = toAddr
	info.Value = value
	               
	fillTxDetails(tx, info)
	return info
}

func generateTxSummary(tx *pb.AnyTx) string {
	if tx == nil {
		return ""
	}
	switch c := tx.GetContent().(type) {
	case *pb.AnyTx_Transaction:
		t := c.Transaction
		return fmt.Sprintf("Transfer %s %s to %s", t.Amount, t.TokenAddress, t.To)
	case *pb.AnyTx_WitnessStakeTx:
		return fmt.Sprintf("Witness stake: %s", c.WitnessStakeTx.Op.String())
	case *pb.AnyTx_IssueTokenTx:
		return fmt.Sprintf("Issue token: %s", c.IssueTokenTx.GetBase().GetTxId())
	case *pb.AnyTx_MinerTx:
		return fmt.Sprintf("Miner: %s", c.MinerTx.Op.String())
	case *pb.AnyTx_CertSignTx:
		return fmt.Sprintf("Checkpoint %d signature from %d", c.CertSignTx.CheckpointHeight, c.CertSignTx.SignerIndex)
	case *pb.AnyTx_FrostWithdrawClaimTx:
		return fmt.Sprintf("Frost withdraw claim: job %s, %d withdraws", c.FrostWithdrawClaimTx.JobId, len(c.FrostWithdrawClaimTx.WithdrawIds))
	case *pb.AnyTx_FrostWithdrawCancelTx:
		return fmt.Sprintf("Cancel Frost withdrawal: %s", c.FrostWithdrawCancelTx.WithdrawId)
	}
	return indexdb.ExtractTxType(tx)
}

func fillTxDetails(tx *pb.AnyTx, info *txInfo) {
	if tx == nil || info == nil {
		return
	}

	contentMsg := txContentMessage(tx)
	if contentMsg == nil {
		return
	}

	raw, err := protoMessageToMap(contentMsg)
	if err != nil {
		info.Details["details_error"] = err.Error()
		return
	}
	info.Details = raw

	                                                                                                     
	if signed := tx.GetFrostWithdrawSignedTx(); signed != nil {
		delete(info.Details, "template_data_hex")
		if len(signed.SignedPackageBytes) == 0 {
			return
		}
		                                          
		info.Details["raw_tx"] = fmt.Sprintf("%x", signed.SignedPackageBytes)
		chainName := strings.ToLower(strings.TrimSpace(signed.Chain))
		if chainName == "btc" && len(signed.TemplateData) > 0 && len(signed.InputSigs) > 0 {
			if tmpl, err := btc.FromJSON(signed.TemplateData); err == nil {
				if rawTxBytes, err := tmpl.BuildSignedRawTx(signed.InputSigs, signed.GuardianSigs); err == nil {
					info.Details["raw_tx_hex"] = fmt.Sprintf("%x", rawTxBytes)
					info.Details["signed_result_kind"] = "btc_raw_tx"
				}
			}
		} else if isExplorerAccountChain(chainName) {
			                                                                    
			                                                                    
			                                                                  
			                                                       
			info.Details["signed_result_kind"] = "account_signature"
			info.Details["result_endpoint_required"] = true
		}
	}

}

func isExplorerAccountChain(chainName string) bool {
	switch strings.ToLower(strings.TrimSpace(chainName)) {
	case "eth", "bnb", "bsc", "base", "arb", "arbitrum", "arbitrum-one", "arbitrum_one", "trx", "tron":
		return true
	default:
		return false
	}
}

func txContentMessage(tx *pb.AnyTx) proto.Message {
	if tx == nil {
		return nil
	}

	m := tx.ProtoReflect()
	oneofs := m.Descriptor().Oneofs()
	for i := 0; i < oneofs.Len(); i++ {
		oneofDesc := oneofs.Get(i)
		fieldDesc := m.WhichOneof(oneofDesc)
		if fieldDesc == nil || fieldDesc.Kind() != protoreflect.MessageKind {
			continue
		}

		msgVal := m.Get(fieldDesc).Message()
		if !msgVal.IsValid() {
			continue
		}

		if pm, ok := msgVal.Interface().(proto.Message); ok {
			return pm
		}
	}

	return nil
}

func protoMessageToMap(msg proto.Message) (map[string]interface{}, error) {
	if msg == nil {
		return map[string]interface{}{}, nil
	}

	payload, err := (&protojson.MarshalOptions{
		UseProtoNames:   true,
		EmitUnpopulated: true,
	}).Marshal(msg)
	if err != nil {
		return nil, err
	}

	out := make(map[string]interface{})
	if err := json.Unmarshal(payload, &out); err != nil {
		return nil, err
	}

	                                                                         
	addHexBytesFields(msg.ProtoReflect(), out)
	return out, nil
}

func addHexBytesFields(msg protoreflect.Message, out map[string]interface{}) {
	if !msg.IsValid() || out == nil {
		return
	}

	fields := msg.Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		key := string(fd.Name())

		if fd.IsMap() {
			continue
		}

		if fd.IsList() {
			list := msg.Get(fd).List()
			switch fd.Kind() {
			case protoreflect.BytesKind:
				hexVals := make([]string, list.Len())
				for j := 0; j < list.Len(); j++ {
					hexVals[j] = fmt.Sprintf("%x", list.Get(j).Bytes())
				}
				out[key+"_hex"] = hexVals
			case protoreflect.MessageKind:
				rawList, ok := out[key].([]interface{})
				if !ok {
					continue
				}
				for j := 0; j < list.Len() && j < len(rawList); j++ {
					childMap, ok := rawList[j].(map[string]interface{})
					if !ok {
						continue
					}
					addHexBytesFields(list.Get(j).Message(), childMap)
				}
			}
			continue
		}

		switch fd.Kind() {
		case protoreflect.BytesKind:
			out[key+"_hex"] = fmt.Sprintf("%x", msg.Get(fd).Bytes())
		case protoreflect.MessageKind:
			childMap, ok := out[key].(map[string]interface{})
			if !ok {
				continue
			}
			addHexBytesFields(msg.Get(fd).Message(), childMap)
		}
	}
}
func (s *server) collectSummary(ctx context.Context, nodes []string, includeBlock bool) ([]nodeSummary, []string) {
	if len(nodes) > explorerMaxSummaryNodes {
		nodes = nodes[:explorerMaxSummaryNodes]
	}
	results := make([]nodeSummary, len(nodes))
	errs := make([]string, 0)
	var errsMu sync.Mutex
	var wg sync.WaitGroup
	if len(nodes) == 0 {
		return results, errs
	}
	workerCount := 8
	if workerCount > len(nodes) {
		workerCount = len(nodes)
	}
	jobs := make(chan int)
	for worker := 0; worker < workerCount; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				node := nodes[i]

				summary := nodeSummary{Address: node}
				start := time.Now()

				status, err := s.fetchStatus(ctx, node)
				if err != nil {
					summary.Error = err.Error()
					errsMu.Lock()
					errs = append(errs, fmt.Sprintf("%s status: %v", node, err))
					errsMu.Unlock()
				} else {
					summary.Status = status.Status
					summary.Info = status.Info
					summary.IsMiner = status.IsMiner
					summary.LivenessState = int(status.LivenessState)
				}

				height, err := s.fetchHeight(ctx, node)
				if err != nil {
					if summary.Error != "" {
						summary.Error += "; "
					}
					summary.Error += err.Error()
					errsMu.Lock()
					errs = append(errs, fmt.Sprintf("%s height: %v", node, err))
					errsMu.Unlock()
				} else {
					summary.CurrentHeight = height.CurrentHeight
					summary.LastAcceptedHeight = height.LastAcceptedHeight
					summary.PendingBlocksCount = height.PendingBlocksCount
					summary.PendingTxCount = height.PendingTxCount
					               
					summary.IsSyncing = height.IsSyncing
					summary.SyncTargetHeight = height.SyncTargetHeight
					summary.IsSnapshotSync = height.IsSnapshotSync
					if height.Address != "" {
						summary.MinerAddress = height.Address
					}
				}

				if includeBlock && height != nil {
					block, err := s.fetchBlock(ctx, node, height.LastAcceptedHeight)
					if err == nil && block != nil {
						summary.Block = buildBlockSummary(block)
					}
				}

				                                                               
				if height != nil && height.PendingBlocksCount > 0 {
					headers, _ := s.fetchPendingHeaders(ctx, node)
					summary.PendingHeaders = headers
				}

				summary.LatencyMs = time.Since(start).Milliseconds()
				results[i] = summary
			}
		}()
	}
	for i := range nodes {
		select {
		case jobs <- i:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return results, errs
		}
	}
	close(jobs)

	wg.Wait()
	return results, errs
}

func (s *server) fetchStatus(ctx context.Context, node string) (*pb.StatusResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	var resp pb.StatusResponse
	if err := s.fetchProto(ctx, node, "/status", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (s *server) fetchSystemResources(ctx context.Context, node string) (*systemResources, error) {
	node = s.pickNode(node)
	if node == "" {
		return nil, fmt.Errorf("node is not allowed")
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	url := fmt.Sprintf("https://%s/system/resources", node)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("X-Node-Address", "explorer-api")
	httpResp, err := s.nodeDo(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return nil, fmt.Errorf("status %d: %s", httpResp.StatusCode, readNodeErrorMessage(httpResp))
	}
	data, err := readResponseBody(httpResp)
	if err != nil {
		return nil, err
	}
	var resp systemResources
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (s *server) fetchHeight(ctx context.Context, node string) (*pb.HeightResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	var resp pb.HeightResponse
	if err := s.fetchProto(ctx, node, "/heightquery", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (s *server) fetchFrostMetrics(ctx context.Context, node string) (*pb.MetricsResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	var resp pb.MetricsResponse
	if err := s.fetchProto(ctx, node, "/frost/metrics", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

                                                                    
func (s *server) fetchBlockTiming(ctx context.Context, node string) (*blockTimingSummary, error) {
	node = s.pickNode(node)
	if node == "" {
		return nil, fmt.Errorf("node is not allowed")
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	url := fmt.Sprintf("https://%s/block/timing", node)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("X-Node-Address", "explorer-api")
	httpResp, err := s.nodeDo(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return nil, fmt.Errorf("status %d: %s", httpResp.StatusCode, readNodeErrorMessage(httpResp))
	}
	data, err := readResponseBody(httpResp)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Available bool                `json:"available"`
		Timing    *blockTimingSummary `json:"timing"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	if !resp.Available {
		return nil, nil
	}
	return resp.Timing, nil
}

                                                                   
func (s *server) handleNodePause(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	rawNode := strings.TrimSpace(r.URL.Query().Get("node"))
	if rawNode == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "missing node parameter"})
		return
	}
	node := rawNode
	node = s.pickNode(node)
	if node == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "node is not allowed"})
		return
	}
	nodeURL := fmt.Sprintf("https://%s/pause", node)
	req, err := http.NewRequestWithContext(r.Context(), r.Method, nodeURL, r.Body)
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": "failed to create node request"})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.nodeDo(req)
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]string{"error": "node unavailable: " + err.Error()})
		return
	}
	writeBoundedNodeResponse(w, resp)
}

                                           
func (s *server) fetchNodePaused(ctx context.Context, node string) (bool, error) {
	node = s.pickNode(node)
	if node == "" {
		return false, fmt.Errorf("node is not allowed")
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	nodeURL := fmt.Sprintf("https://%s/pause", node)
	req, err := http.NewRequestWithContext(ctx, "GET", nodeURL, nil)
	if err != nil {
		return false, err
	}
	resp, err := s.nodeDo(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	var result struct {
		Paused bool `json:"paused"`
	}
	body, err := readResponseBody(resp)
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return false, err
	}
	return result.Paused, nil
}

func (s *server) fetchBlock(ctx context.Context, node string, height uint64) (*pb.Block, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	var resp pb.GetBlockResponse
	req := &pb.GetBlockRequest{Height: height}
	if err := s.fetchProto(ctx, node, "/getblock", req, &resp); err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	if resp.Block == nil {
		return nil, errors.New("empty block")
	}
	return resp.Block, nil
}

                                                                 
func (s *server) fetchPendingHeaders(ctx context.Context, node string) ([]pendingHeaderInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	var resp pb.PendingBlocksResponse
	if err := s.fetchProto(ctx, node, "/pendingblocks", nil, &resp); err != nil {
		return nil, err
	}
	if len(resp.Blocks) == 0 {
		return nil, nil
	}
	headers := make([]pendingHeaderInfo, 0, len(resp.Blocks))
	for _, b := range resp.Blocks {
		headers = append(headers, pendingHeaderInfo{
			BlockID:     b.BlockId,
			Height:      b.Height,
			Proposer:    b.Proposer,
			Window:      b.Window,
			Votes:       b.Votes,
			IsPreferred: b.IsPreferred,
		})
	}
	return headers, nil
}

                                                        
func (s *server) fetchChits(ctx context.Context, node string, height uint64) *finalizationChitsInfo {
	node = s.pickNode(node)
	if node == "" {
		return nil
	}
	                         
	req := &pb.GetBlockRequest{Height: height}
	reqBytes, err := proto.Marshal(req)
	if err != nil {
		return nil
	}

	url := fmt.Sprintf("https://%s/getchits", node)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBytes))
	if err != nil {
		return nil
	}
	httpReq.Header.Set("Content-Type", "application/x-protobuf")

	resp, err := s.nodeDo(httpReq)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil
	}

	                                             
	var result struct {
		Height      uint64 `json:"height"`
		BlockID     string `json:"block_id"`
		TotalVotes  int    `json:"total_votes"`
		TotalRounds int    `json:"total_rounds"`
		FinalizedAt int64  `json:"finalized_at"`
		Rounds      []struct {
			Round     int   `json:"round"`
			Timestamp int64 `json:"timestamp"`
			Votes     []struct {
				NodeID      string `json:"node_id"`
				PreferredID string `json:"preferred_id"`
				Timestamp   int64  `json:"timestamp"`
			} `json:"votes"`
		} `json:"rounds"`
		Chits []struct {
			NodeID      string `json:"node_id"`
			PreferredID string `json:"preferred_id"`
			Timestamp   int64  `json:"timestamp"`
		} `json:"chits"`
		Error string `json:"error"`
	}

	bodyBytes, err := readResponseBody(resp)
	if err != nil {
		return nil
	}

	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		return nil
	}

	if result.Error != "" {
		return nil
	}

	info := &finalizationChitsInfo{
		BlockID:     result.BlockID,
		Height:      result.Height,
		TotalVotes:  result.TotalVotes,
		TotalRounds: result.TotalRounds,
		FinalizedAt: result.FinalizedAt,
		Rounds:      make([]roundChitsInfo, 0, len(result.Rounds)),
		Chits:       make([]chitInfo, 0, len(result.Chits)),
	}

	                     
	for _, r := range result.Rounds {
		roundInfo := roundChitsInfo{
			Round:     r.Round,
			Timestamp: r.Timestamp,
			Votes:     make([]chitInfo, 0, len(r.Votes)),
		}
		for _, v := range r.Votes {
			roundInfo.Votes = append(roundInfo.Votes, chitInfo{
				NodeID:      v.NodeID,
				PreferredID: v.PreferredID,
				Timestamp:   v.Timestamp,
			})
		}
		info.Rounds = append(info.Rounds, roundInfo)
	}

	                              
	for _, c := range result.Chits {
		info.Chits = append(info.Chits, chitInfo{
			NodeID:      c.NodeID,
			PreferredID: c.PreferredID,
			Timestamp:   c.Timestamp,
		})
	}
	info.FinalizedAt = explorerChitsObservedAt(info)

	return info
}

func (s *server) fetchProto(ctx context.Context, node, path string, req proto.Message, resp proto.Message) error {
	node = s.pickNode(node)
	if node == "" {
		return fmt.Errorf("node is not allowed")
	}
	url := fmt.Sprintf("https://%s%s", node, path)
	var body io.Reader
	method := http.MethodGet
	if req != nil {
		data, err := proto.Marshal(req)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
		method = http.MethodPost
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return err
	}
	httpReq.Header.Set("Accept", "application/x-protobuf")
	httpReq.Header.Set("X-Node-Address", "explorer-api")
	if req != nil {
		httpReq.Header.Set("Content-Type", "application/x-protobuf")
	}

	httpResp, err := s.nodeDo(httpReq)
	if err != nil {
		return err
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(httpResp.Body, 4<<10))
		return fmt.Errorf("status %d: %s", httpResp.StatusCode, strings.TrimSpace(string(msg)))
	}

	data, err := readResponseBody(httpResp)
	if err != nil {
		return err
	}

	return proto.Unmarshal(data, resp)
}

func readResponseBody(resp *http.Response) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, errors.New("empty node response")
	}
	if resp.ContentLength > explorerMaxDecodedResponseBytes {
		return nil, errExplorerResponseTooBig
	}
	reader := resp.Body
	if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		reader = gz
	}
	data, err := io.ReadAll(io.LimitReader(reader, explorerMaxDecodedResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > explorerMaxDecodedResponseBytes {
		return nil, errExplorerResponseTooBig
	}
	return data, nil
}

func readNodeErrorMessage(resp *http.Response) string {
	if resp == nil || resp.Body == nil {
		return ""
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	return strings.TrimSpace(string(data))
}

func buildBlockSummary(block *pb.Block) *blockSummary {
	if block == nil {
		return nil
	}
	typeCounts := make(map[string]int)
	for _, tx := range block.Body {
		typeCounts[txTypeName(tx)]++
	}

	summary := &blockSummary{
		Height:        block.Header.Height,
		BlockHash:     block.BlockHash,
		PrevBlockHash: block.Header.PrevBlockHash,
		TxsHash:       block.Header.TxsHash,
		Miner:         keys.MinerAddress(block.Header.Miner),
		TxCount:       len(block.Body),
		TxTypeCounts:  typeCounts,
		Accumulated:   block.AccumulatedReward,
		BlockReward:   block.AccumulatedReward,
		Window:        block.Header.Window,
	}
	                                                        
	if len(block.Header.StateRoot) > 0 {
		summary.StateRoot = fmt.Sprintf("%x", block.Header.StateRoot)
	}
	return summary
}

func txTypeName(tx *pb.AnyTx) string {
	if kind := tx.GetKind(); kind != "" {
		return kind
	}
	return "unknown"
}

func writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	                                                                          
	                                                                         
	_ = json.NewEncoder(w).Encode(payload)
}

func writeJSONStatus(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *server) handleNodeDetails(w http.ResponseWriter, r *http.Request) {
	node := s.pickNode(r.URL.Query().Get("address"))
	if node == "" {
		http.Error(w, "missing address", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	results, _ := s.collectSummary(ctx, []string{node}, true)
	if len(results) == 0 {
		http.Error(w, "node not found", http.StatusNotFound)
		return
	}

	details := nodeDetails{
		nodeSummary: results[0],
	}

	recentBlocks, err := s.fetchRecentBlockHeaders(ctx, node, 50)
	if err == nil {
		details.RecentBlocks = recentBlocks
	}

	writeJSON(w, details)
}

func frostMetricsFromProto(metrics *pb.MetricsResponse) *frostMetrics {
	if metrics == nil {
		return nil
	}
	result := &frostMetrics{
		HeapAlloc:      metrics.HeapAlloc,
		HeapSys:        metrics.HeapSys,
		NumGoroutine:   metrics.NumGoroutine,
		FrostJobs:      metrics.FrostJobs,
		FrostWithdraws: metrics.FrostWithdraws,
		ApiCallStats:   metrics.ApiCallStats,
	}
	if len(metrics.ChannelStats) > 0 {
		result.ChannelStats = make([]channelStat, 0, len(metrics.ChannelStats))
		for _, stat := range metrics.ChannelStats {
			result.ChannelStats = append(result.ChannelStats, channelStat{
				Name:   stat.Name,
				Module: stat.Module,
				Len:    stat.Len,
				Cap:    stat.Cap,
				Usage:  stat.Usage,
			})
		}
	}
	return result
}

                                                                              
                                                                            
                                                                    
func (s *server) handleNodeDiagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	node := s.pickNode(r.URL.Query().Get("address"))
	if node == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "missing or disallowed address"})
		return
	}
	includeLogs := parseBool(r.URL.Query().Get("include_logs"))

	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()
	diagnostics := nodeDiagnostics{Address: node}
	var mu sync.Mutex
	var wg sync.WaitGroup
	addError := func(name string, err error) {
		if err == nil {
			return
		}
		mu.Lock()
		diagnostics.Errors = append(diagnostics.Errors, fmt.Sprintf("%s: %v", name, err))
		mu.Unlock()
	}

	taskCount := 4
	if includeLogs {
		taskCount++
	}
	wg.Add(taskCount)
	go func() {
		defer wg.Done()
		resources, err := s.fetchSystemResources(ctx, node)
		if err != nil {
			addError("resources", err)
			return
		}
		mu.Lock()
		diagnostics.Resources = resources
		mu.Unlock()
	}()
	go func() {
		defer wg.Done()
		timing, err := s.fetchBlockTiming(ctx, node)
		if err != nil {
			addError("block timing", err)
			return
		}
		mu.Lock()
		diagnostics.BlockTiming = timing
		mu.Unlock()
	}()
	go func() {
		defer wg.Done()
		metrics, err := s.fetchFrostMetrics(ctx, node)
		if err != nil {
			addError("metrics", err)
			return
		}
		mu.Lock()
		diagnostics.FrostMetrics = frostMetricsFromProto(metrics)
		mu.Unlock()
	}()
	if includeLogs {
		go func() {
			defer wg.Done()
			logsResponse, err := s.fetchLogs(ctx, node)
			if err != nil {
				addError("logs", err)
				return
			}
			mu.Lock()
			diagnostics.Logs = logsResponse.Logs
			mu.Unlock()
		}()
	}
	go func() {
		defer wg.Done()
		paused, err := s.fetchNodePaused(ctx, node)
		if err != nil {
			addError("pause status", err)
			return
		}
		mu.Lock()
		diagnostics.IsPaused = &paused
		mu.Unlock()
	}()
	wg.Wait()
	sort.Strings(diagnostics.Errors)
	writeJSON(w, diagnostics)
}

func (s *server) fetchRecentBlockHeaders(ctx context.Context, node string, count int) ([]blockHeaderInfo, error) {
	node = s.pickNode(node)
	if node == "" {
		return nil, fmt.Errorf("node is not allowed")
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	if blocks, err := s.fetchRecentBlocksJSON(ctx, node, count); err == nil {
		return blocks, nil
	}
	var resp pb.GetRecentBlocksResponse
	req := &pb.GetRecentBlocksRequest{Count: int32(count)}
	if err := s.fetchProto(ctx, node, "/getrecentblocks", req, &resp); err != nil {
		return nil, err
	}
	blocks := make([]blockHeaderInfo, 0, len(resp.Blocks))
	for _, h := range resp.Blocks {
		blocks = append(blocks, blockHeaderInfo{
			Height:      h.Height,
			BlockHash:   h.BlockHash,
			Miner:       h.Miner,
			TxCount:     int(h.TxCount),
			Accumulated: h.AccumulatedReward,
			BlockReward: h.AccumulatedReward,
			Window:      h.Window,
			StateRoot:   h.StateRoot,
		})
	}
	return blocks, nil
}

func (s *server) fetchRecentBlocksJSON(ctx context.Context, node string, count int) ([]blockHeaderInfo, error) {
	node = s.pickNode(node)
	if node == "" {
		return nil, fmt.Errorf("node is not allowed")
	}
	payload, err := json.Marshal(struct {
		Count int `json:"count"`
	}{Count: count})
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("https://%s/getrecentblocks", node)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Node-Address", "explorer-api")
	httpResp, err := s.nodeDo(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return nil, fmt.Errorf("status %d: %s", httpResp.StatusCode, readNodeErrorMessage(httpResp))
	}
	data, err := readResponseBody(httpResp)
	if err != nil {
		return nil, err
	}
	var resp recentBlocksResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}
	for i := range resp.Blocks {
		if resp.Blocks[i].BlockReward == "" {
			resp.Blocks[i].BlockReward = resp.Blocks[i].Accumulated
		}
	}
	return resp.Blocks, nil
}

func (s *server) fetchLogs(ctx context.Context, node string) (*pb.LogsResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	var resp pb.LogsResponse
	req := &pb.LogsRequest{MaxLines: 1000}                            

	                                                                           
	err := s.fetchProto(ctx, node, "/logs", req, &resp)
	if err == nil {
		return &resp, nil
	}

	                                                                                                                 
	logLines := logs.GetLogsForNode(node)
	if len(logLines) > 0 {
		return &pb.LogsResponse{Logs: boundedExplorerLocalLogs(logLines)}, nil
	}

	return nil, err
}

const (
	explorerLocalLogMaxLines     = 1000
	explorerLocalLogMaxMessage   = 8 << 10
	explorerLocalLogMaxTimestamp = 64
	explorerLocalLogMaxLevel     = 16
	explorerLocalLogMaxResponse  = 1 << 20
)

func boundedExplorerLocalLogs(lines []*pb.LogLine) []*pb.LogLine {
	start := len(lines) - explorerLocalLogMaxLines
	if start < 0 {
		start = 0
	}
	selected := make([]*pb.LogLine, 0, len(lines)-start)
	wireSize := 0
	for index := len(lines) - 1; index >= start; index-- {
		if lines[index] == nil {
			continue
		}
		line := &pb.LogLine{
			Timestamp: truncateExplorerLogField(lines[index].Timestamp, explorerLocalLogMaxTimestamp),
			Level:     truncateExplorerLogField(lines[index].Level, explorerLocalLogMaxLevel),
			Message:   truncateExplorerLogField(lines[index].Message, explorerLocalLogMaxMessage),
		}
		lineSize := proto.Size(line)
		entrySize := 1 + explorerUvarintSize(uint64(lineSize)) + lineSize
		if wireSize+entrySize > explorerLocalLogMaxResponse {
			continue
		}
		selected = append(selected, line)
		wireSize += entrySize
	}
	for left, right := 0, len(selected)-1; left < right; left, right = left+1, right-1 {
		selected[left], selected[right] = selected[right], selected[left]
	}
	return selected
}

func truncateExplorerLogField(value string, maxBytes int) string {
	value = strings.ToValidUTF8(value, "")
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	cut := maxBytes
	for cut > 0 && value[cut]&0xc0 == 0x80 {
		cut--
	}
	return value[:cut]
}

func explorerUvarintSize(value uint64) int {
	size := 1
	for value >= 1<<7 {
		value >>= 7
		size++
	}
	return size
}

                                                               

const (
	explorerTxHistoryDefaultLimit = 100
	explorerTxHistoryMaxLimit     = 500
	explorerTxHistoryMaxOffset    = 5000
	                                                                        
	                                                                     
	                                                               
	explorerTxHistoryMaxTypes = 8
)

                                                 
func (s *server) handleTxHistory(w http.ResponseWriter, r *http.Request) {
	address := r.URL.Query().Get("address")
	if address == "" {
		http.Error(w, "missing address parameter", http.StatusBadRequest)
		return
	}

	limitStr := r.URL.Query().Get("limit")
	limit := explorerTxHistoryDefaultLimit
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}
	if limit > explorerTxHistoryMaxLimit {
		limit = explorerTxHistoryMaxLimit
	}
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			offset = n
		}
	}
	if offset > explorerTxHistoryMaxOffset {
		offset = explorerTxHistoryMaxOffset
	}

	                                                                     
	var typeSet map[string]struct{}
	if raw := r.URL.Query().Get("types"); raw != "" {
		typeSet = make(map[string]struct{})
		for _, t := range strings.Split(raw, ",") {
			if t = strings.TrimSpace(t); t != "" {
				typeSet[t] = struct{}{}
				if len(typeSet) > explorerTxHistoryMaxTypes {
					http.Error(w, "too many transaction types", http.StatusBadRequest)
					return
				}
			}
		}
	}

	if s.indexDB == nil {
		http.Error(w, "index database not available", http.StatusServiceUnavailable)
		return
	}

	txs, err := s.indexDB.GetAddressTxHistoryPage(address, typeSet, limit, offset)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to get tx history: %v", err), http.StatusInternalServerError)
		return
	}

	count := len(txs)
	                                                                                      
	if limit > 0 && len(typeSet) == 0 {
		if c, err := s.indexDB.GetAddressTxCount(address); err == nil {
			count = c
		}
	}

	resp := struct {
		Address    string              `json:"address"`
		TotalCount int                 `json:"total_count"`
		Limit      int                 `json:"limit"`
		Offset     int                 `json:"offset"`
		Txs        []*indexdb.TxRecord `json:"txs"`
	}{
		Address:    address,
		TotalCount: count,
		Limit:      limit,
		Offset:     offset,
		Txs:        txs,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

                                            
func (s *server) handleSyncStatus(w http.ResponseWriter, r *http.Request) {
	if s.indexDB == nil {
		http.Error(w, "index database not available", http.StatusServiceUnavailable)
		return
	}

	syncHeight, _ := s.indexDB.GetSyncHeight()
	syncNode, _ := s.indexDB.GetSyncNode()
	history, historyErr := s.indexDB.GetHistoryMetadata()

	status := "stopped"
	var lastSync time.Time
	if s.syncer != nil {
		status, lastSync = s.syncer.GetStatus()
	}

	resp := struct {
		SyncHeight     uint64 `json:"sync_height"`
		SyncNode       string `json:"sync_node"`
		Status         string `json:"status"`
		PartialHistory bool   `json:"partial_history"`
		BaseHeight     uint64 `json:"base_height,omitempty"`
		PartialReason  string `json:"partial_reason,omitempty"`
		HistoryError   string `json:"history_error,omitempty"`
		LastSync       string `json:"last_sync,omitempty"`
		LastSyncAgo    string `json:"last_sync_ago,omitempty"`
	}{
		SyncHeight: syncHeight,
		SyncNode:   syncNode,
		Status:     status,
	}
	if historyErr != nil {
		resp.HistoryError = historyErr.Error()
	} else if history != nil {
		resp.PartialHistory = history.Partial
		resp.BaseHeight = history.BaseHeight
		resp.PartialReason = history.Reason
	} else if syncHeight > 0 {
		resp.BaseHeight = 1
	}

	if !lastSync.IsZero() {
		resp.LastSync = lastSync.Format(time.RFC3339)
		resp.LastSyncAgo = time.Since(lastSync).Round(time.Second).String()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

                                                          

                                 
type OrderBookEntry struct {
	Price          string `json:"price"`
	Amount         string `json:"amount"`
	Total          string `json:"total"`
	PendingCount   int    `json:"pendingCount"`                           
	ConfirmedCount int    `json:"confirmedCount"`                         
}

                                
type OrderBookData struct {
	Pair       string           `json:"pair"`
	Bids       []OrderBookEntry `json:"bids"`
	Asks       []OrderBookEntry `json:"asks"`
	LastUpdate string           `json:"lastUpdate"`
}

                           
type TradeRecord struct {
	ID           string `json:"id"`
	Time         string `json:"time"`
	Height       uint64 `json:"height,omitempty"`
	Price        string `json:"price"`
	Amount       string `json:"amount"`
	Side         string `json:"side"`
	MakerOrderID string `json:"maker_order_id,omitempty"`
	TakerOrderID string `json:"taker_order_id,omitempty"`
}

                            
type APIError struct {
	Error   string `json:"error"`
	Code    string `json:"code,omitempty"`
	Details string `json:"details,omitempty"`
}

const explorerMarketTradesMaxLimit = 500

                                        
func (s *server) handleOrderBook(w http.ResponseWriter, r *http.Request) {
	node := r.URL.Query().Get("node")
	node = s.pickNode(node)
	pair := r.URL.Query().Get("pair")

	if node == "" || pair == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(APIError{
			Error: "missing node or pair parameter",
			Code:  "INVALID_PARAMS",
		})
		return
	}

	                                 
	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	orderBook, err := s.fetchOrderBookFromNode(ctx, node, pair)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(APIError{
			Error:   "failed to fetch order book from node",
			Code:    "NODE_ERROR",
			Details: err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(orderBook)
}

                                        
func (s *server) handleTrades(w http.ResponseWriter, r *http.Request) {
	node := r.URL.Query().Get("node")
	node = s.pickNode(node)
	pair := r.URL.Query().Get("pair")
	limitStr := r.URL.Query().Get("limit")

	if node == "" || pair == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(APIError{
			Error: "missing node or pair parameter",
			Code:  "INVALID_PARAMS",
		})
		return
	}

	limit := 100
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}
	if limit > explorerMarketTradesMaxLimit {
		limit = explorerMarketTradesMaxLimit
	}

	                              
	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	trades, err := s.fetchTradesFromNode(ctx, node, pair, limit)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(APIError{
			Error:   "failed to fetch trades from node",
			Code:    "NODE_ERROR",
			Details: err.Error(),
		})
		return
	}

	                             
	if len(trades) > limit {
		trades = trades[:limit]
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(trades)
}

                                                   
func (s *server) handleOrderBookDebug(w http.ResponseWriter, r *http.Request) {
	node := r.URL.Query().Get("node")
	node = s.pickNode(node)
	pair := r.URL.Query().Get("pair")

	if node == "" || pair == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(APIError{
			Error: "missing node or pair parameter",
			Code:  "INVALID_PARAMS",
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	endpoint := &url.URL{Scheme: "https", Host: node, Path: "/orderbook/debug"}
	query := endpoint.Query()
	query.Set("pair", pair)
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(APIError{Error: err.Error()})
		return
	}

	resp, err := s.nodeDo(req)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(APIError{
			Error:   "failed to fetch debug data from node",
			Code:    "NODE_ERROR",
			Details: err.Error(),
		})
		return
	}
	writeBoundedNodeResponse(w, resp)
}

                                                               
func (s *server) handleMyOrders(w http.ResponseWriter, r *http.Request) {
	s.handleSpotOrdersReadModel(w, r)
}

                                                                     
func (s *server) handleFutureMyOrders(w http.ResponseWriter, r *http.Request) {
	s.handleFutureOrdersReadModel(w, r)
}
func (s *server) fetchOrderBookFromNode(ctx context.Context, node, pair string) (*OrderBookData, error) {
	node = s.pickNode(node)
	if node == "" {
		return nil, fmt.Errorf("node is not allowed")
	}
	endpoint := &url.URL{Scheme: "https", Host: node, Path: "/orderbook"}
	query := endpoint.Query()
	query.Set("pair", pair)
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}

	resp, err := s.nodeDo(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("node returned status %d", resp.StatusCode)
	}

	body, err := readResponseBody(resp)
	if err != nil {
		return nil, err
	}
	var orderBook OrderBookData
	if err := json.Unmarshal(body, &orderBook); err != nil {
		return nil, err
	}

	return &orderBook, nil
}

                                                  
func (s *server) fetchTradesFromNode(ctx context.Context, node, pair string, limit int) ([]TradeRecord, error) {
	node = s.pickNode(node)
	if node == "" {
		return nil, fmt.Errorf("node is not allowed")
	}
	endpoint := &url.URL{Scheme: "https", Host: node, Path: "/trades"}
	query := endpoint.Query()
	query.Set("pair", pair)
	query.Set("limit", strconv.Itoa(limit))
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}

	resp, err := s.nodeDo(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("node returned status %d", resp.StatusCode)
	}

	body, err := readResponseBody(resp)
	if err != nil {
		return nil, err
	}
	var trades []TradeRecord
	if err := json.Unmarshal(body, &trades); err != nil {
		return nil, err
	}
	if limit > 0 && len(trades) > limit {
		trades = trades[:limit]
	}

	return trades, nil
}

                                          
type pendingBlocksRequest struct {
	Node string `json:"node"`
}

                                      
type pendingBlockInfo struct {
	BlockID     string `json:"block_id"`
	Height      uint64 `json:"height"`
	ParentID    string `json:"parent_id"`
	Proposer    string `json:"proposer"`
	Window      int32  `json:"window"`
	Votes       int32  `json:"votes"`                                
	IsPreferred bool   `json:"is_preferred"`                               
}

                                          
type heightConsensusState struct {
	Height     uint64 `json:"height"`
	Preference string `json:"preference"`
	Confidence int32  `json:"confidence"`
	Finalized  bool   `json:"finalized"`
}

                                           
type pendingBlocksResponse struct {
	Blocks       []pendingBlockInfo     `json:"blocks"`
	HeightStates []heightConsensusState `json:"height_states,omitempty"`
	Error        string                 `json:"error,omitempty"`
}

                                                        
func (s *server) handlePendingBlocks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req pendingBlocksRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, pendingBlocksResponse{Error: "invalid request body"})
		return
	}
	if req.Node == "" {
		writeJSON(w, pendingBlocksResponse{Error: "node is required"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	var pbResp pb.PendingBlocksResponse
	if err := s.fetchProto(ctx, req.Node, "/pendingblocks", nil, &pbResp); err != nil {
		writeJSON(w, pendingBlocksResponse{Error: err.Error()})
		return
	}

	blocks := make([]pendingBlockInfo, 0, len(pbResp.Blocks))
	for _, b := range pbResp.Blocks {
		blocks = append(blocks, pendingBlockInfo{
			BlockID:     b.BlockId,
			Height:      b.Height,
			ParentID:    b.ParentId,
			Proposer:    b.Proposer,
			Window:      b.Window,
			Votes:       b.Votes,
			IsPreferred: b.IsPreferred,
		})
	}

	heightStates := make([]heightConsensusState, 0, len(pbResp.HeightStates))
	for _, hs := range pbResp.HeightStates {
		heightStates = append(heightStates, heightConsensusState{
			Height:     hs.Height,
			Preference: hs.Preference,
			Confidence: hs.Confidence,
			Finalized:  hs.Finalized,
		})
	}

	writeJSON(w, pendingBlocksResponse{Blocks: blocks, HeightStates: heightStates})
}

                                                               
func (s *server) handleTokenQuery(w http.ResponseWriter, r *http.Request) {
	node := r.URL.Query().Get("node")
	node = s.pickNode(node)
	addr := r.URL.Query().Get("addr")
	if node == "" || addr == "" {
		writeJSON(w, map[string]string{"error": "node and addr are required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()
	token, err := s.fetchToken(ctx, node, addr)
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	                           
	info := map[string]interface{}{
		"address":       token.Address,
		"owner":         token.Owner,
		"totalSupply":   token.TotalSupply,
		"can_mint":      token.CanMint,
		"can_freeze":    token.CanFreeze,
		"can_burn":      token.CanBurn,
		"type":          token.Type.String(),
		"create_height": token.CreateHeight,
	}
	if token.NativeChain != "" {
		info["native_chain"] = token.NativeChain
		info["native_contract"] = token.NativeContract
		if r.URL.Query().Get("include_network") == "1" && token.Type == pb.TokenType_TOKEN_BRIDGED {
			var cfg pb.FrostVaultConfig
			if err := s.fetchProto(ctx, node, "/frost/vault/config?chain="+url.QueryEscape(token.NativeChain), nil, &cfg); err != nil {
				writeJSONStatus(w, http.StatusBadGateway, map[string]string{"error": "failed to fetch token network: " + err.Error()})
				return
			}
			info["evm_chain_id"] = strconv.FormatUint(cfg.EvmChainId, 10)
		}
	}
	writeJSON(w, map[string]interface{}{"token": info})
}

                                                              
func (s *server) handleTokenRegistryQuery(w http.ResponseWriter, r *http.Request) {
	node := r.URL.Query().Get("node")
	node = s.pickNode(node)
	if node == "" {
		writeJSON(w, map[string]string{"error": "node is required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	var resp pb.GetTokenRegistryResponse
	if err := s.fetchProto(ctx, node, "/gettokenregistry", nil, &resp); err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	info := map[string]interface{}{
		"token_count": 0,
	}
	if resp.Registry != nil {
		info["token_count"] = resp.Registry.TokenCount
	}
	writeJSON(w, info)
}

                                                       
                                                                     
                                                                               
func (s *server) handleTradingPairs(w http.ResponseWriter, r *http.Request) {
	node := r.URL.Query().Get("node")
	node = s.pickNode(node)
	if node == "" {
		writeJSON(w, map[string]string{"error": "node is required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	market := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("market")))
	if market == "" {
		market = "spot"
	}
	if market != "spot" && market != "future" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "market must be spot or future"})
		return
	}

	tokenDirectory, err := s.getTradingTokenDirectory(ctx, node)
	if err != nil {
		writeJSONStatus(w, tradingTokenDirectoryStatus(err), map[string]string{"error": err.Error()})
		return
	}

	var registered registeredMarketDirectory
	var upstreamStatus int
	var marketErr error
	switch market {
	case "spot":
		registered, upstreamStatus, marketErr = s.fetchRegisteredSpotMarkets(ctx, node)
	case "future":
		registered, upstreamStatus, marketErr = s.fetchRegisteredFutureMarkets(ctx, node)
	}
	if marketErr != nil {
		status := http.StatusBadGateway
		if upstreamStatus == http.StatusRequestEntityTooLarge {
			status = upstreamStatus
		}
		writeJSONStatus(w, status, map[string]string{"error": marketErr.Error()})
		return
	}
	pairs := append(make([]tradingPairInfo, 0, len(registered.Markets)), registered.Markets...)
	seenPairs := make(map[string]struct{}, len(pairs))
	for _, pair := range pairs {
		seenPairs[pair.Pair] = struct{}{}
	}
	for _, pair := range selectRegisteredTradingPairsByAddress(tokenDirectory.byAddress, registered.PairKeys) {
		if _, covered := seenPairs[pair.Pair]; covered {
			continue
		}
		seenPairs[pair.Pair] = struct{}{}
		pairs = append(pairs, pair)
	}
	activity, activityHeight, activityErr := s.fetchLatestFinalizedMarketActivity(ctx, node, market)
	if activityErr != nil {
		activity = map[string]uint64{}
		activityHeight = 0
	}
	pairs = rankTradingPairs(pairs, activity, hotTradingPairLimit)
	tokens := tokensReferencedByPairs(tokenDirectory, pairs)
	for i := range tokens {
		tokens[i] = tokenWithIconURL(node, tokens[i])
	}

	writeJSON(w, map[string]interface{}{
		"tokens":          tokens,
		"pairs":           pairs,
		"activity_height": activityHeight,
	})
}
