package handlers

import (
	"dex/config"
	"dex/consensus"
	"dex/db"
	"dex/interfaces"
	"dex/lifecycle"
	"dex/logs"
	"dex/pb"
	"dex/sender"
	"dex/stats"
	"dex/txpool"
	"dex/types"
	"dex/utils"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru"
)

                                           
type FrostMsgHandler func(msg types.Message) error

                                                       
type HandlerManager struct {
	dbManager        *db.Manager
	consensusManager *consensus.ConsensusNodeManager
	senderManager    *sender.SenderManager
	txPool           *txpool.TxPool
	port             string                      
	address          string                      
	adapter          *consensus.ConsensusAdapter

	                        
	seenBlocksCache             *lru.Cache                                    
	seenGossipPayloadCache      *lru.Cache                                                     
	acceptedFrontierCacheOnce   sync.Once
	acceptedFrontierCache       *acceptedFrontierResponseCache
	acceptedFrontierLimiterOnce sync.Once
	acceptedFrontierLimiter     *acceptedFrontierRequesterLimiter
	                     
	Stats *stats.Stats
	                        
	frostMsgHandler FrostMsgHandler
	Logger          logs.Logger

	                                              
	peerGate *PeerGate
	                                                                              
	queryReplay consensusQueryReplayGuard

	keyMgr *utils.KeyManager                     
	                                                                            
	                                                                       
	nodeAdvertiseAddr string
	cfg               *config.Config

	emergency          *lifecycle.EmergencyStop                                        
	operatorControlAPI bool

	registeredRouteMu sync.RWMutex
	registeredRoutes  map[string]struct{}

	metricsCacheMu      sync.Mutex
	metricsCachePayload []byte
	metricsCacheExpiry  time.Time
}

                                                            
                                                                            
                
func (hm *HandlerManager) SetOperatorControlAPI(enabled bool) {
	if hm != nil {
		hm.operatorControlAPI = enabled
	}
}

                                                                                         
func (hm *HandlerManager) SetEmergencyStop(e *lifecycle.EmergencyStop) {
	if hm == nil {
		return
	}
	hm.emergency = e
}

                                                            
func (hm *HandlerManager) EmergencyStop() *lifecycle.EmergencyStop { return hm.emergency }

func (hm *HandlerManager) SetKeyManager(km *utils.KeyManager) {
	hm.keyMgr = km
}

                                                                           
                                                                            
                                                                             
                                                                        
func (hm *HandlerManager) SetNodeAdvertiseAddr(endpoint string) {
	if hm != nil {
		hm.nodeAdvertiseAddr = strings.TrimSpace(endpoint)
	}
}

                                                   
func NewHandlerManager(
	dbMgr *db.Manager,
	consensusMgr *consensus.ConsensusNodeManager,
	port, address string,
	senderMgr *sender.SenderManager,
	txPool *txpool.TxPool,                   
	logger logs.Logger,
	cfg *config.Config,
) *HandlerManager {
	                                                                       
	seenBlocksCache, _ := lru.New(1024)
	seenGossipPayloadCache, _ := lru.New(4096)
	var pgCfg config.PeerGateConfig
	if cfg != nil {
		pgCfg = cfg.PeerGate
	}
	pg := NewPeerGate(pgCfg, logger)
	pg.SetDeps(nil, dbMgr)                                                           
	return &HandlerManager{
		dbManager:              dbMgr,
		consensusManager:       consensusMgr,
		txPool:                 txPool,
		senderManager:          senderMgr,
		port:                   port,
		address:                address,
		adapter:                consensus.NewConsensusAdapter(dbMgr),
		Stats:                  stats.NewStats(),
		seenBlocksCache:        seenBlocksCache,
		seenGossipPayloadCache: seenGossipPayloadCache,
		Logger:                 logger,
		peerGate:               pg,
		cfg:                    cfg,
		registeredRoutes:       make(map[string]struct{}),
	}
}

type ChallengeInfo struct {
	Challenge   string
	CreatedTime time.Time
}

                                                  
func (hm *HandlerManager) SetFrostMsgHandler(handler FrostMsgHandler) {
	hm.frostMsgHandler = handler
}

                                                                                          
func (hm *HandlerManager) SetPeerGateLiveness(lt interfaces.LivenessTracker) {
	if hm.peerGate != nil {
		hm.peerGate.livenessTracker = lt
	}
}

                                    
func (hm *HandlerManager) RegisterRoutes(mux *http.ServeMux) {
	debugAPIEnabled := hm.cfg != nil && hm.cfg.Server.EnableDebugAPI

	                                                                         
	pauseExempt := map[string]bool{
		"/pause":               true,
		"/status":              true,
		"/system/resources":    true,
		"/heightquery":         true,
		"/acceptedfrontier":    true,
		"/block/timing":        true,
		"/debug/toggle_miner":  true,
		"/frost/metrics":       true,
		"/logs":                true,
		"/orderbook/debug":     true,
		"/discovery/miner_ips": true,
		"/service/peers":       true,
		"/service/probe":       true,
		"/service/callback":    true,
		"/service/syncblocks":  true,
		"/emergency/stop":      true,
		"/emergency/resume":    true,
		"/emergency/status":    true,
	}
	                                                                             
	bind := func(path string, handler http.HandlerFunc) {
		hm.registeredRouteMu.Lock()
		if hm.registeredRoutes == nil {
			hm.registeredRoutes = make(map[string]struct{})
		}
		hm.registeredRoutes[path] = struct{}{}
		hm.registeredRouteMu.Unlock()

		wrapped := handler
		if !pauseExempt[path] {
			inner := handler
			wrapped = func(w http.ResponseWriter, r *http.Request) {
				if hm.consensusManager != nil && hm.consensusManager.IsPaused() {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				inner(w, r)
			}
		}
		                                                                                    
		if hm.peerGate != nil {
			wrapped = hm.peerGate.Middleware(path, wrapped)
		}
		                                                                
		mux.HandleFunc(path, hm.boundRequestBody(path, wrapped))
	}

	                      
	bind("/pushquery", hm.HandlePushQuery)
	bind("/pullquery", hm.HandlePullQuery)
	bind("/gossipAnyMsg", hm.HandleBlockGossip)
	bind("/chits", hm.HandleChits)
	bind("/heightquery", hm.HandleHeightQuery)
	bind("/acceptedfrontier", hm.HandleAcceptedFrontier)
	bind("/statedb/snapshot/shards", hm.HandleStateSnapshotShards)
	bind("/statedb/checkpoint/files", hm.HandleStateCheckpointFiles)
	bind("/statedb/checkpoint/chunk", hm.HandleStateCheckpointChunk)
	bind("/pendingblocks", hm.HandlePendingBlocks)
	               
	bind("/status", hm.HandleStatus)
	bind("/system/resources", requireExplorerServiceToken(hm.HandleSystemResources))
	bind("/block/timing", requireExplorerServiceToken(hm.HandleBlockTiming))
	bind("/tx", hm.HandleTx)
	bind("/txbatch", hm.HandleTxBatch)
	bind("/getblock", hm.HandleGetBlock)
	bind("/getsyncblocks", hm.HandleGetSyncBlocks)
	bind("/getchits", hm.HandleGetChits)                                     
	bind("/getrecentblocks", hm.HandleGetRecentBlocks)
	bind("/getdata", hm.HandleGetData)
	bind("/gettxreceipt", hm.HandleGetTxReceipt)
	bind("/batchgetdata", hm.HandleBatchGetTx)
	bind("/getaccount", hm.HandleGetAccount)
	bind("/getaccountbalances", hm.HandleGetAccountBalances)
	bind("/account/asset", hm.HandleAccountAsset)
	bind("/nodes", hm.HandleNodes)
	bind("/discovery/miner_ips", hm.HandleDiscoveryMinerIPMap)
	                                                                            
	                                                                             
	bind("/service/peers", hm.HandleServicePeerExchange)
	bind("/service/probe", hm.HandleServicePeerProbe)
	bind("/service/callback", hm.HandleServicePeerCallback)
	bind("/service/gossip", hm.HandleServiceBlockGossip)
	bind("/service/syncblocks", hm.HandleServiceSyncBlocks)
	bind("/getblockbyid", hm.HandleGet)
	bind("/put", hm.HandlePut)
	if debugAPIEnabled {
		bind("/logs", requireExplorerServiceToken(hm.HandleLogs))
	}
	            
	bind("/frostmsg", hm.HandleFrostMsg)
	bind("/frostmsgbatch", hm.HandleFrostBatch)
	                         
	bind("/frost/config", hm.HandleGetFrostConfig)
	bind("/frost/guardian/intent", hm.HandleFrostGuardianIntent)
	bind("/frost/withdraw/status", hm.HandleGetWithdrawStatus)
	bind("/frost/withdraw/result", hm.HandleFrostWithdrawResult)
	bind("/frost/btc/policy", hm.HandleFrostBTCPolicy)
	bind("/frost/withdraw/parts", hm.HandleFrostWithdrawParts)
	bind("/frost/withdraw/part", hm.HandleFrostWithdrawPart)
	bind("/frost/vault/migration/parts", hm.HandleFrostMigrationParts)
	bind("/frost/btc/internal-outputs", hm.HandleFrostBTCInternalOutputs)
	bind("/frost/withdraw/batch", hm.HandleFrostWithdrawBatch)
	bind("/frost/vault/handover/page", hm.HandleFrostHandoverPage)
	bind("/frost/withdraws", hm.HandleListWithdraws)
	bind("/frost/vault/group_pubkey", hm.HandleGetVaultGroupPubKey)
	bind("/frost/vault/config", hm.HandleGetVaultConfig)
	bind("/frost/vault/transition_status", hm.HandleGetVaultTransitionStatus)
	bind("/frost/vault/migration", hm.HandleGetVaultMigration)
	bind("/frost/vault/migration/download", hm.HandleDownloadVaultMigration)
	bind("/frost/vault/artifact/head", hm.HandleFrostArtifactHead)
	bind("/frost/artifact", hm.HandleFrostArtifact)
	bind("/frost/archive/status", hm.HandleFrostArchiveStatus)
	bind("/frost/vault/dkg_commitments", hm.HandleGetVaultDkgCommitments)
	bind("/frost/dkg/progress", hm.HandleFrostDkgProgress)
	bind("/frost/signed_package", hm.HandleDownloadSignedPackage)
	                                              
	bind("/frost/withdraw/list", hm.HandleFrostWithdrawList)
	bind("/frost/withdraw/monitor", hm.HandleFrostWithdrawMonitor)
	bind("/frost/dkg/list", hm.HandleFrostDkgList)
	bind("/frost/vault/list", hm.HandleFrostVaultList)
	bind("/frost/vault/funds", hm.HandleFrostVaultFunds)
	bind("/frost/vault/utxos", hm.HandleFrostVaultUTXOs)
	bind("/witness/requests", hm.HandleWitnessRequests)
	bind("/witness/rewards", hm.HandleWitnessRewards)
	bind("/witness/my_requests", hm.HandleWitnessMyRequests)
	bind("/witness/list", hm.HandleWitnessList)
	bind("/witness/challenges", hm.HandleWitnessChallenges)
	bind("/witness/challenge_quote", hm.HandleWitnessChallengeQuote)
	bind("/witness/config", hm.HandleWitnessConfig)
	                           
	bind("/orderbook", hm.HandleOrderBook)
	if debugAPIEnabled {
		bind("/orderbook/debug", requireExplorerServiceToken(hm.HandleOrderBookDebug))
	}
	bind("/trades", hm.HandleTrades)
	bind("/spot/markets", hm.HandleSpotMarkets)
	bind("/myorders", hm.HandleMyOrders)
	               
	bind("/gettoken", hm.HandleGetToken)
	bind("/gettokenregistry", hm.HandleGetTokenRegistry)
	bind("/listtokens", hm.HandleListTokens)
	               
	bind("/referral/info", hm.HandleReferralInfo)
	bind("/referral/rebates", hm.HandleReferralRebates)
	                                                                                
	bind("/user/activity", hm.HandleUserActivity)
	               
	bind("/future/positions", hm.HandleFuturePositions)
	bind("/future/markets", hm.HandleFutureMarkets)
	bind("/future/market", hm.HandleFutureMarket)
	bind("/future/orderbook", hm.HandleFutureOrderBook)
	bind("/future/trades", hm.HandleFutureTrades)
	bind("/future/myorders", hm.HandleFutureMyOrders)
	                     
	bind("/frost/health", hm.HandleGetHealth)
	if debugAPIEnabled {
		bind("/frost/metrics", requireExplorerServiceToken(hm.HandleGetMetrics))
	}
	                       
	bind("/cert/chain", hm.HandleGetCertChain)
	bind("/cert/get", hm.HandleGetCertificate)
	bind("/cert/summary", hm.HandleGetCertSummary)
	                                  
	if hm.operatorControlAPI {
		bind("/pause", requireExplorerServiceToken(hm.HandlePause))
		bind("/debug/toggle_miner", requireExplorerServiceToken(hm.HandleDebugToggleMiner))
	}
	                                                                                
	bind("/emergency/stop", hm.HandleEmergencyStop)
	bind("/emergency/resume", hm.HandleEmergencyResume)
	bind("/emergency/status", hm.HandleEmergencyStatus)
}

const unmatchedRouteID = "/_unmatched"
const redirectedRouteID = "/_redirect"

                                                                              
                                                                  
func CanonicalRouteID(mux *http.ServeMux, r *http.Request) string {
	if mux != nil && r != nil {
		                                                                   
		                                                                   
		if r.Method != http.MethodConnect && r.URL != nil {
			escaped := r.URL.EscapedPath()
			cleaned := path.Clean(escaped)
			if strings.HasSuffix(escaped, "/") && cleaned != "/" {
				cleaned += "/"
			}
			if escaped != cleaned {
				return redirectedRouteID
			}
		}
		_, pattern := mux.Handler(r)
		if pattern != "" {
			if r.URL != nil && r.URL.Path+"/" == pattern {
				return redirectedRouteID
			}
			return pattern
		}
	}
	return unmatchedRouteID
}

                                                                             
                                                                               
                                                         
func (hm *HandlerManager) WrapTraffic(mux *http.ServeMux, next http.Handler) http.Handler {
	if next == nil {
		next = mux
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeID := CanonicalRouteID(mux, r)

		alreadyBound := false
		if hm != nil {
			hm.registeredRouteMu.RLock()
			_, alreadyBound = hm.registeredRoutes[routeID]
			hm.registeredRouteMu.RUnlock()
		}
		if hm == nil || hm.peerGate == nil || alreadyBound {
			next.ServeHTTP(w, r)
			return
		}

		hm.boundRequestBody(routeID, hm.peerGate.Middleware(routeID, next.ServeHTTP))(w, r)
	})
}

const controlRequestBodyLimit = int64(64 << 10)

                                                                            
                                                                               
func largeRequestBody(path string) bool {
	switch path {
	case "/pushquery", "/gossipAnyMsg", "/put", "/frostmsg", "/frostmsgbatch", "/service/gossip", "/tx", "/txbatch", "/batchgetdata":
		return true
	default:
		return false
	}
}

func (hm *HandlerManager) boundRequestBody(path string, next http.HandlerFunc) http.HandlerFunc {
	limit, timeout := int64(10<<20), 5*time.Second
	if hm != nil && hm.cfg != nil {
		if hm.cfg.Server.MaxRequestBodySize > 0 {
			limit = hm.cfg.Server.MaxRequestBodySize
		}
		if hm.cfg.Server.HTTPTimeout > 0 && (largeRequestBody(path) || hm.cfg.Server.HTTPTimeout < timeout) {
			timeout = hm.cfg.Server.HTTPTimeout
		}
	}
	if !largeRequestBody(path) {
		controlLimit := controlRequestBodyLimit
		if path == "/service/syncblocks" {
			controlLimit = serviceSyncRequestLimit
		}
		limit = min(limit, controlLimit)
	}
	if path == "/pushquery" {
		limit = min(limit, int64(pb.MaxPushQuerySize))
	}
	if path == "/frostmsgbatch" {
		limit = min(limit, int64(256<<10))
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && r.Body != http.NoBody {
			controller := http.NewResponseController(w)
			                                                                  
			                                                                    
			deadline := time.Now().Add(timeout)
			_ = controller.SetReadDeadline(deadline)
			                                                                    
			                                                                    
			                                                                   
			defer func() { _ = controller.SetReadDeadline(deadline) }()
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		                                                                       
		                                                                          
		w, r, finishTrace := hm.startPushRequestTrace(path, w, r)
		if finishTrace != nil {
			defer finishTrace()
		}
		if r.ContentLength > limit {
			http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		next(w, r)
	}
}

                                                                          
                                                                              
func (hm *HandlerManager) IsVerifiedPeerIP(ip string) bool {
	return hm != nil && hm.peerGate != nil && hm.peerGate.isVerifiedSource(ip)
}

                                                                               
                                                                                                     
                                                                                       
func (hm *HandlerManager) verifyNodeIdentity(address string, message interface{}) bool {
	if address == "" {
		hm.Logger.Warn("[verifyNodeIdentity] empty address")
		return false
	}
	                                                                     
	if _, ok := consensus.ResolveNodePublicKey(address); ok {
		return true
	}
	                                                                                                 
	if hm.dbManager != nil {
		acc, err := hm.dbManager.GetAccount(address)
		if err == nil && acc != nil && acc.IsMiner {
			if acc.PublicKeys != nil {
				if pub := acc.PublicKeys.Keys[int32(pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1_DEX)]; len(pub) > 0 {
					consensus.RegisterNodePublicKey(types.NodeID(address), pub)
				}
			}
			return true
		}
	}
	hm.Logger.Warn("[verifyNodeIdentity] rejected unknown node address=%s", address)
	return false
}

               

func (hm *HandlerManager) checkAuth(r *http.Request) bool {
	if !AUTH_ENABLED {
		return true
	}

	clientIP := strings.Split(r.RemoteAddr, ":")[0]
	info, err := hm.dbManager.GetClientInfo(clientIP)
	if err != nil {
		return false
	}
	return info.GetAuthed()
}

func (hm *HandlerManager) hasBlock(blockId string) bool {
	if hm.consensusManager != nil {
		return hm.consensusManager.HasBlock(blockId)
	}
	return hm.dbManager.BlockExists(blockId)
}

func (hm *HandlerManager) Stop() {
	if hm == nil {
		return
	}
	hm.StopPeerGate()
	if hm.senderManager != nil {
		hm.senderManager.Stop()
	}
	                     
}

func (hm *HandlerManager) StopPeerGate() {
	if hm != nil && hm.peerGate != nil {
		hm.peerGate.Stop()
	}
}
