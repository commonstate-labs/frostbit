package handlers

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/subtle"
	"dex/config"
	"dex/consensus"
	"dex/frost/security"
	"dex/interfaces"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"dex/types"
	"dex/utils"
	"google.golang.org/protobuf/proto"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"golang.org/x/time/rate"
)

type peerGateContextKey uint8

const peerGatePushQueryContextKey peerGateContextKey = iota

func withVerifiedPushQuery(ctx context.Context, msg *pb.PushQuery) context.Context {
	if msg == nil {
		return ctx
	}
	return context.WithValue(ctx, peerGatePushQueryContextKey, msg)
}

func verifiedPushQueryFromContext(ctx context.Context) *pb.PushQuery {
	msg, _ := ctx.Value(peerGatePushQueryContextKey).(*pb.PushQuery)
	return msg
}

                               
type TrustedEntry struct {
	Address   string                         
	FirstSeen int64                                    
	LastSeen  int64                                    
	HitCount  uint64                               
}

                                                                
                                                                                           
                                                                                                  
                                                                                       
type PeerGate struct {
	mu         sync.RWMutex
	trustedIPs map[string]*TrustedEntry                       

	         
	failCounts  sync.Map                                
	banUntil    sync.Map                       
	stateMu     sync.Mutex
	failEntries int
	banEntries  int

	                        
	trustedLimiters        sync.Map                        
	syncLimiters           sync.Map                        
	unknownLimiters        sync.Map                        
	explorerLimiters       sync.Map                        
	trustedRPS             float64
	trustedBurst           int
	syncRPS                float64
	syncBurst              int
	unknownRPS             float64
	unknownBurst           int
	explorerRPS            float64
	explorerBurst          int
	explorerToken          string
	limiterMu              sync.Mutex
	limiterEntries         int
	priorityLimiterEntries int
	maxTrackedIPs          int
	trustedOverflow        *rate.Limiter
	syncOverflow           *rate.Limiter
	unknownOverflow        *rate.Limiter
	explorerOverflow       *rate.Limiter

	                         
	pathLimiters        sync.Map                               
	trustedPathLimiters sync.Map                                                          
	untrustedRequests   *rate.Limiter                                             
	pathRates           map[string]float64
	pathStats           sync.Map                           
	defaultPathRPS      float64
	defaultPathBurst    int

	                                                                                          
	                                                                     
	totalInflight        chan struct{}
	nonConsensusInflight chan struct{}
	txBatchInflight      chan struct{}
	unverifiedInflight   chan struct{}
	bulkInflight         [2]chan struct{}                                                

	               
	consensusPaths   map[string]bool                                                     
	trustedOnlyPaths map[string]bool
	syncPaths        map[string]bool                                                     

	         
	livenessTracker interfaces.LivenessTracker
	dbManager       interface {
		GetAccount(address string) (*pb.Account, error)
		Scan(prefix string) (map[string][]byte, error)
		Get(key string) ([]byte, error)
	}
	logger logs.Logger

	         
	banThreshold    int                                             
	banDuration     time.Duration              
	trustedTTL      time.Duration                               
	enforceLoopback bool                                     
	cleanupTicker   *time.Ticker
	stopCh          chan struct{}
	stopOnce        sync.Once
}

                                                              
type pathDropStats struct {
	mu          sync.Mutex
	dropCount   int64
	lastLogTime time.Time
}

                              
func NewPeerGate(cfg config.PeerGateConfig, logger logs.Logger) *PeerGate {
	if cfg.TrustedRPS <= 0 {
		cfg = config.DefaultPeerGateConfig()
	}
	pathRates := cfg.PathRateLimits
	if pathRates == nil {
		pathRates = make(map[string]float64)
	}
	if cfg.SyncRPS <= 0 {
		cfg.SyncRPS = 5
		cfg.SyncBurst = 10
	}
	if cfg.ExplorerRPS <= 0 {
		cfg.ExplorerRPS = 500
	}
	if cfg.ExplorerBurst <= 0 {
		cfg.ExplorerBurst = 1000
	}
	if cfg.MaxTrackedIPs <= 0 {
		cfg.MaxTrackedIPs = config.DefaultPeerGateConfig().MaxTrackedIPs
	}
	defaults := config.DefaultPeerGateConfig()
	cfg = config.ResolvePeerGateInflight(cfg)
	if cfg.DefaultPathRPS <= 0 {
		cfg.DefaultPathRPS = defaults.DefaultPathRPS
	}
	if cfg.DefaultPathBurst <= 0 {
		cfg.DefaultPathBurst = defaults.DefaultPathBurst
	}
	if cfg.MaxInflight <= 0 {
		cfg.MaxInflight = defaults.MaxInflight
	}
	if cfg.ConsensusReserve <= 0 || cfg.ConsensusReserve >= cfg.MaxInflight {
		cfg.ConsensusReserve = cfg.MaxInflight / 4
		if cfg.ConsensusReserve == 0 && cfg.MaxInflight > 1 {
			cfg.ConsensusReserve = 1
		}
		if cfg.ConsensusReserve >= cfg.MaxInflight {
			cfg.ConsensusReserve = 0
		}
	}
	if cfg.TxBatchMaxInflight <= 0 || cfg.TxBatchMaxInflight > cfg.MaxInflight-cfg.ConsensusReserve {
		cfg.TxBatchMaxInflight = defaults.TxBatchMaxInflight
		if cfg.TxBatchMaxInflight > cfg.MaxInflight-cfg.ConsensusReserve {
			cfg.TxBatchMaxInflight = cfg.MaxInflight - cfg.ConsensusReserve
		}
	}
	pg := &PeerGate{
		trustedIPs: make(map[string]*TrustedEntry),
		consensusPaths: map[string]bool{
			"/pushquery": true, "/pullquery": true, "/chits": true,
			"/gossipAnyMsg": true, "/put": true,
			"/frostmsg": true, "/frostmsgbatch": true,
		},
		trustedOnlyPaths: map[string]bool{
			"/gossipAnyMsg": true, "/put": true,
		},
		syncPaths: map[string]bool{
			"/heightquery": true, "/acceptedfrontier": true, "/getsyncblocks": true, "/getblock": true, "/getblockbyid": true,
			"/service/peers": true, "/service/probe": true, "/service/callback": true,
			"/service/gossip": true, "/service/syncblocks": true,
			"/statedb/snapshot/shards":  true,
			"/statedb/checkpoint/files": true, "/statedb/checkpoint/chunk": true,
		},
		pathRates:         pathRates,
		defaultPathRPS:    cfg.DefaultPathRPS,
		defaultPathBurst:  cfg.DefaultPathBurst,
		untrustedRequests: rate.NewLimiter(rate.Limit(cfg.DefaultPathRPS), cfg.DefaultPathBurst),
		totalInflight:     make(chan struct{}, cfg.MaxInflight),
		nonConsensusInflight: make(chan struct{},
			cfg.MaxInflight-cfg.ConsensusReserve),
		txBatchInflight:    make(chan struct{}, cfg.TxBatchMaxInflight),
		unverifiedInflight: make(chan struct{}, max(1, min(32, cfg.ConsensusReserve, cfg.MaxInflight-cfg.ConsensusReserve))),
		bulkInflight: [2]chan struct{}{
			make(chan struct{}, min(8, cfg.TxBatchMaxInflight)),
			make(chan struct{}, cfg.TxBatchMaxInflight),
		},
		trustedRPS: cfg.TrustedRPS, trustedBurst: cfg.TrustedBurst,
		syncRPS: cfg.SyncRPS, syncBurst: cfg.SyncBurst,
		unknownRPS: cfg.UnknownRPS, unknownBurst: cfg.UnknownBurst,
		explorerRPS: cfg.ExplorerRPS, explorerBurst: cfg.ExplorerBurst,
		explorerToken:    config.ExplorerServiceToken(),
		maxTrackedIPs:    cfg.MaxTrackedIPs,
		trustedOverflow:  rate.NewLimiter(rate.Limit(cfg.TrustedRPS), cfg.TrustedBurst),
		syncOverflow:     rate.NewLimiter(rate.Limit(cfg.SyncRPS), cfg.SyncBurst),
		unknownOverflow:  rate.NewLimiter(rate.Limit(cfg.UnknownRPS), cfg.UnknownBurst),
		explorerOverflow: rate.NewLimiter(rate.Limit(cfg.ExplorerRPS), cfg.ExplorerBurst),
		banThreshold:     cfg.BanThreshold, banDuration: cfg.BanDuration,
		trustedTTL: cfg.TrustedTTL, enforceLoopback: cfg.EnforceLoopback,
		logger: logger,
		stopCh: make(chan struct{}),
	}
	pg.cleanupTicker = time.NewTicker(5 * time.Minute)
	go pg.cleanupLoop()
	return pg
}

                                               
func (pg *PeerGate) SetDeps(lt interfaces.LivenessTracker, dbMgr interface {
	GetAccount(string) (*pb.Account, error)
	Scan(prefix string) (map[string][]byte, error)
	Get(key string) ([]byte, error)
}) {
	pg.livenessTracker = lt
	pg.dbManager = dbMgr
	consensus.SetNodePublicKeyResolver(func(address string) ([]byte, bool) {
		if dbMgr == nil {
			return nil, false
		}
		acc, err := dbMgr.GetAccount(address)
		if err != nil || acc == nil || acc.PublicKeys == nil {
			return nil, false
		}
		pub := acc.PublicKeys.Keys[int32(pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1_DEX)]
		return pub, len(pub) > 0
	})
}

                          
func (pg *PeerGate) Stop() {
	pg.stopOnce.Do(func() {
		pg.cleanupTicker.Stop()
		close(pg.stopCh)
	})
}

                                                                            
                                                                               
                                                                               
                                                                                 
func (pg *PeerGate) acquireInflight(path string, trusted bool) (func(), bool) {
	if pg == nil || pg.totalInflight == nil {
		return func() {}, true
	}
	isConsensus := pg.consensusPaths[path] || path == "/heightquery" || path == "/acceptedfrontier"
	var lanes [5]chan struct{}
	n := 0
	if path == "/txbatch" {
		lanes[n], n = pg.txBatchInflight, n+1
	}
	if largeRequestBody(path) {
		class := 0
		if trusted {
			class = 1
		}
		lanes[n], n = pg.bulkInflight[class], n+1
	}
	if !trusted && isConsensus {
		lanes[n], n = pg.unverifiedInflight, n+1
	}
	if !trusted || !isConsensus {
		lanes[n], n = pg.nonConsensusInflight, n+1
	}
	lanes[n], n = pg.totalInflight, n+1
	for i, lane := range lanes[:n] {
		select {
		case lane <- struct{}{}:
		default:
			for _, acquired := range lanes[:i] {
				<-acquired
			}
			return nil, false
		}
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			for _, lane := range lanes[:n] {
				<-lane
			}
		})
	}, true
}

                                        

                                                        
                                                                                                        
                                                                                               
                                       
func (pg *PeerGate) Middleware(path string, next http.HandlerFunc) http.HandlerFunc {
	isConsensus := pg.consensusPaths[path]
	isSync := pg.syncPaths[path]

	return func(w http.ResponseWriter, r *http.Request) {
		ip := extractIP(r)
		loopbackBypass := (ip == "127.0.0.1" || ip == "::1") && !pg.enforceLoopback

		                        
		if ip == "127.0.0.1" || ip == "::1" {
			                                                      
			if nodeAddr := r.Header.Get("X-Node-Address"); pg.enforceLoopback && nodeAddr != "" {
				ip = "loopback:" + nodeAddr
			}
		}

		                
		if pg.isBanned(ip) {
			w.WriteHeader(http.StatusForbidden)
			return
		}

		trusted := loopbackBypass || pg.isVerifiedSource(ip)
		explorer := !isConsensus && pg.isExplorerService(r)
		                                                                          
		                                                                         
		                                                                   
		if (!trusted && !explorer && !pg.untrustedRequests.Allow()) || !pg.allowPathForSource(path, trusted || explorer) {
			pg.logPathDrop(path, ip)
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}
		                                                           
		if path != "/events/stream" {
			release, ok := pg.acquireInflight(path, trusted)
			if !ok {
				http.Error(w, "Server Busy", http.StatusServiceUnavailable)
				return
			}
			defer release()
		}
		if loopbackBypass {
			next(w, r)
			return
		}

		                                                                             
		                                                                              
		                                        
		if explorer {
			if !pg.allowExplorer(ip) {
				http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
				return
			}
			next(w, r)
			return
		}

		                                                                                    
		                                                                                  
		if trusted && pg.trustedOnlyPaths[path] {
			if !pg.allowTrusted(ip) {
				http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
				return
			}
			next(w, r)
			return
		}

		                                                           
		if isSync {
			allowed := false
			if trusted {
				allowed = pg.allowTrusted(ip)
			} else {
				allowed = pg.allowSync(ip)
			}
			if !allowed {
				http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
				return
			}
			next(w, r)
			return
		}

		                                                                                   
		                                                                                     
		                                                                                            
		                                                                                        
		                                                                                          
		                                                                
		if path == "/chits" {
			if trusted {
				if !pg.allowTrusted(ip) {
					http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
					return
				}
			} else if !pg.allowSync(ip) {
				http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
				return
			}
			bodyBytes, err := io.ReadAll(r.Body)
			r.Body.Close()
			if err != nil {
				if _, tooLarge := err.(*http.MaxBytesError); tooLarge {
					http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
					return
				}
				http.Error(w, "Failed to read body", http.StatusBadRequest)
				return
			}
			if !pg.verifyChitAdmission(ip, bodyBytes) {
				pg.recordFailure(ip)
				w.WriteHeader(http.StatusForbidden)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			next(w, r)
			return
		}

		                                                                                    
		                                                                                  
		                                                                                        
		if trusted && isConsensus {
			if !pg.allowTrusted(ip) {
				http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
				return
			}
		} else if !pg.allowUnknown(ip) {
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}

		                                   
		if !isConsensus {
			next(w, r)
			return
		}

		                                                    
		if pg.trustedOnlyPaths[path] {
			pg.recordFailure(ip)
			w.WriteHeader(http.StatusForbidden)
			return
		}

		pushTrace := pushTraceFromRequest(r)
		bodyStarted := pushTrace.begin("body")
		bodyBytes, err := io.ReadAll(r.Body)
		r.Body.Close()
		pushTrace.end(pushBody, bodyStarted)
		if pushTrace != nil {
			pushTrace.bodyBytes = len(bodyBytes)
		}
		if err != nil {
			if _, tooLarge := err.(*http.MaxBytesError); tooLarge {
				http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "Failed to read body", http.StatusBadRequest)
			return
		}

		var verifiedPushQuery *pb.PushQuery
		gateStarted := pushTrace.begin("gate")
		ok := pg.verifyAndPromoteWithPushQuery(ip, path, bodyBytes, &verifiedPushQuery, pushTrace)
		pushTrace.end(pushGate, gateStarted)
		if !ok {
			pg.recordFailure(ip)
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if verifiedPushQuery != nil {
			r = r.WithContext(withVerifiedPushQuery(r.Context(), verifiedPushQuery))
			r.Body = http.NoBody
			bodyBytes = nil
		} else {
			                                                                        
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}
		next(w, r)
	}
}

                                     

                                                                                                  
func (pg *PeerGate) verifyAndPromote(ip, path string, body []byte) bool {
	return pg.verifyAndPromoteWithPushQuery(ip, path, body, nil, nil)
}

func (pg *PeerGate) verifyAndPromoteWithPushQuery(ip, path string, body []byte, parsedPushQuery **pb.PushQuery, trace *pushRequestTrace) bool {
	var address string
	var verified bool

	switch path {
	case "/frostmsgbatch":
		batch, err := decodeFrostBatch(body)
		if err != nil {
			return false
		}
		                                                                       
		                                                                      
		for _, raw := range batch.Envelopes {
			if pg.verifyAndPromote(ip, "/frostmsg", raw) {
				return true
			}
		}
		return false
	case "/pushquery":
		msg := &pb.PushQuery{}
		if err := pb.UnmarshalSanitize(body, msg); err != nil {
			return false
		}
		trace.query(msg, false)
		address = msg.Address
		                                                                                  
		if len(msg.Signature) > 0 {
			digest := computePushQueryDigest(msg)
			verified = pg.verifySignature(address, digest, msg.Signature)
		}
		if parsedPushQuery != nil {
			*parsedPushQuery = msg
		}

	case "/chits":
		                                                                         
		                                                                            
		                                                                              
		return false

	case "/pullquery":
		var msg pb.PullQuery
		if err := pb.UnmarshalSanitize(body, &msg); err != nil {
			return false
		}
		address = msg.Address
		if len(msg.Signature) > 0 {
			verified = pg.verifySignature(address, consensus.ComputePullQueryDigest(&msg), msg.Signature)
		}

	case "/frostmsg":
		var msg pb.FrostEnvelope
		if err := pb.UnmarshalSanitize(body, &msg); err != nil {
			return false
		}
		address = msg.From
		verified = pg.verifyFrostEnvelopeSignature(address, &msg)

	default:
		return false
	}

	if address == "" {
		return false
	}

	                                                                                                  
	if path == "/frostmsg" {
		if !pg.isMiner(address) && !pg.isPendingTransitionParticipant(address) {
			return false
		}
	} else if !pg.isMiner(address) {
		return false
	}

	                                       
	if !verified {
		return false
	}

	sourceStarted := trace.begin("source_gate")
	allowedSource, boundSource := pg.sourceIPAdmission(address, ip)
	trace.end(pushSourceGate, sourceStarted)
	if !allowedSource {
		pg.logger.Warn("[PeerGate] reject %s from IP %s: source does not match registered endpoint", address, ip)
		return false
	}

	                                                                           
	                                                                             
	if boundSource {
		pg.promote(ip, address)
	}
	return true
}

                                                                              
                                                                              
                                                                                
func (pg *PeerGate) verifyChitAdmission(ip string, body []byte) bool {
	var msg pb.Chits
	if err := pb.UnmarshalSanitize(body, &msg); err != nil {
		return false
	}
	if msg.Address == "" || len(msg.ChitSignature) == 0 || !pg.isMiner(msg.Address) {
		return false
	}
	if !pg.sourceIPMatches(msg.Address, ip) {
		pg.logger.Warn("[PeerGate] reject Chits from %s at IP %s: source does not match registered endpoint", msg.Address, ip)
		return false
	}
	return true
}

                                                                
func (pg *PeerGate) extractAddressFromBody(path string, body []byte) string {
	switch path {
	case "/pullquery":
		var msg pb.PullQuery
		if pb.UnmarshalSanitize(body, &msg) == nil {
			return msg.Address
		}
	case "/frostmsg":
		var msg pb.FrostEnvelope
		if pb.UnmarshalSanitize(body, &msg) == nil {
			return msg.From
		}
	}
	return ""
}

                                                                 
func (pg *PeerGate) verifySignature(address string, digest, sig []byte) bool {
	pubBytes, ok := pg.resolveNodePublicKey(address)
	if !ok {
		return false
	}
	return utils.VerifyECDSASignature(pubBytes, digest, sig)
}

func (pg *PeerGate) verifyFrostEnvelopeSignature(address string, env *pb.FrostEnvelope) bool {
	pubKey, ok := pg.resolveNodeECDSAPublicKey(address)
	if !ok {
		return false
	}
	return security.VerifyFrostEnvelope(pubKey, env)
}

func (pg *PeerGate) resolveNodePublicKey(address string) ([]byte, bool) {
	if pubBytes, ok := consensus.GetNodePublicKey(address); ok && len(pubBytes) > 0 {
		return pubBytes, true
	}
	if pg.dbManager == nil {
		return nil, false
	}
	acc, err := pg.dbManager.GetAccount(address)
	if err != nil || acc == nil || acc.PublicKeys == nil {
		return nil, false
	}
	pubBytes := acc.PublicKeys.Keys[int32(pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1_DEX)]
	if len(pubBytes) == 0 {
		return nil, false
	}
	consensus.RegisterNodePublicKey(types.NodeID(address), pubBytes)
	return pubBytes, true
}

func (pg *PeerGate) resolveNodeECDSAPublicKey(address string) (*ecdsa.PublicKey, bool) {
	pubBytes, ok := pg.resolveNodePublicKey(address)
	if !ok {
		return nil, false
	}
	pubKey, err := btcec.ParsePubKey(pubBytes)
	if err != nil {
		return nil, false
	}
	return pubKey.ToECDSA(), true
}

func (pg *PeerGate) sourceIPMatches(address, sourceIP string) bool {
	allowed, _ := pg.sourceIPAdmission(address, sourceIP)
	return allowed
}

                                                                            
                                                                               
func (pg *PeerGate) sourceIPAdmission(address, sourceIP string) (allowed, bound bool) {
	if strings.HasPrefix(sourceIP, "loopback:") {
		                                                                        
		                                                                         
		return true, pg.enforceLoopback && strings.TrimPrefix(sourceIP, "loopback:") == address
	}
	if sourceIP == "" {
		return true, false
	}
	if pg.dbManager == nil {
		return true, false
	}
	acc, err := pg.dbManager.GetAccount(address)
	if err != nil || acc == nil {
		return false, false
	}
	host := normalizeEndpointHost(acc.Ip)
	if host == "" {
		return true, false
	}
	expectedIP := net.ParseIP(host)
	actualIP := net.ParseIP(sourceIP)
	if expectedIP == nil || actualIP == nil {
		return true, false
	}
	matched := expectedIP.Equal(actualIP)
	return matched, matched
}

func normalizeEndpointHost(endpoint string) string {
	if endpoint == "" {
		return ""
	}
	host := endpoint
	if parsedHost, _, err := net.SplitHostPort(endpoint); err == nil {
		host = parsedHost
	}
	return strings.Trim(host, "[]")
}

                                            
func (pg *PeerGate) isMiner(address string) bool {
	if pg.livenessTracker != nil {
		return pg.livenessTracker.IsActiveOrProbation(address)
	}
	if pg.dbManager == nil {
		return true                                              
	}
	acc, err := pg.dbManager.GetAccount(address)
	return err == nil && acc != nil && acc.IsMiner
}

                                                                                                  
                                                                                                  
func (pg *PeerGate) isMinerOrFrozen(address string) bool {
	if pg.livenessTracker != nil {
		return pg.livenessTracker.IsRegistered(address)
	}
	return pg.isMiner(address)
}

                                                                                         
                                                                                       
func (pg *PeerGate) isPendingTransitionParticipant(address string) bool {
	if address == "" || pg.dbManager == nil {
		return false
	}
	kvs, err := pg.dbManager.Scan(keys.KeyFrostVaultTransitionActivePrefix())
	if err != nil || len(kvs) == 0 {
		return false
	}
	for _, transitionKeyBytes := range kvs {
		transitionKey := string(transitionKeyBytes)
		if transitionKey == "" {
			continue
		}
		data, err := pg.dbManager.Get(transitionKey)
		if err != nil || len(data) == 0 {
			continue
		}
		var ts pb.VaultTransitionState
		if proto.Unmarshal(data, &ts) != nil {
			continue
		}
		                                                                      
		                                                       
		if ts.Lifecycle != "DRAINING" {
			continue
		}
		for _, m := range ts.OldCommitteeMembers {
			if m == address {
				return true
			}
		}
		                                                                       
		                                                                      
		                                                                      
		if ts.DkgStatus == "FAILED" {
			continue
		}
		for _, m := range ts.NewCommitteeMembers {
			if m == address {
				return true
			}
		}
	}
	return false
}

                                        

func (pg *PeerGate) promote(ip, address string) {
	now := time.Now().Unix()
	pg.mu.Lock()
	pg.trustedIPs[ip] = &TrustedEntry{Address: address, FirstSeen: now, LastSeen: now}
	pg.mu.Unlock()
	pg.deleteFailure(ip)
}

func (pg *PeerGate) isTrusted(ip string) bool {
	pg.mu.RLock()
	entry, ok := pg.trustedIPs[ip]
	pg.mu.RUnlock()
	if ok {
		atomic.StoreInt64(&entry.LastSeen, time.Now().Unix())
		atomic.AddUint64(&entry.HitCount, 1)
	}
	return ok
}

                                                                               
                                                                           
func (pg *PeerGate) isVerifiedSource(ip string) bool {
	pg.mu.RLock()
	entry := pg.trustedIPs[ip]
	pg.mu.RUnlock()
	return entry != nil && time.Since(time.Unix(atomic.LoadInt64(&entry.LastSeen), 0)) <= pg.trustedTTL
}

                               

func (pg *PeerGate) recordFailure(ip string) {
	pg.stateMu.Lock()
	val, ok := pg.failCounts.Load(ip)
	if !ok {
		if pg.failEntries >= pg.maxTrackedIPs {
			pg.stateMu.Unlock()
			return
		}
		actual, loaded := pg.failCounts.LoadOrStore(ip, new(int32))
		if !loaded {
			pg.failEntries++
		}
		val = actual
	}
	cnt := val.(*int32)
	n := atomic.AddInt32(cnt, 1)
	if int(n) < pg.banThreshold {
		pg.stateMu.Unlock()
		return
	}

	pg.failCounts.Delete(ip)
	if pg.failEntries > 0 {
		pg.failEntries--
	}
	if _, alreadyBanned := pg.banUntil.Load(ip); !alreadyBanned {
		if pg.banEntries >= pg.maxTrackedIPs {
			pg.stateMu.Unlock()
			return
		}
		pg.banEntries++
	}
	pg.banUntil.Store(ip, time.Now().Add(pg.banDuration).Unix())
	pg.stateMu.Unlock()
	if pg.logger != nil {
		pg.logger.Warn("[PeerGate] Banned IP %s for %v after %d failures", ip, pg.banDuration, n)
	}
}

func (pg *PeerGate) isBanned(ip string) bool {
	v, ok := pg.banUntil.Load(ip)
	if !ok {
		return false
	}
	until := v.(int64)
	if time.Now().Unix() < until {
		return true
	}
	                                                                             
	                                                                         
	pg.stateMu.Lock()
	if current, found := pg.banUntil.Load(ip); found && current.(int64) == until {
		pg.banUntil.Delete(ip)
		if pg.banEntries > 0 {
			pg.banEntries--
		}
	}
	pg.stateMu.Unlock()
	return false
}

                                     

func (pg *PeerGate) getLimiter(store *sync.Map, ip string, rps float64, burst int) *rate.Limiter {
	v, ok := store.Load(ip)
	if ok {
		return v.(*rate.Limiter)
	}
	l := rate.NewLimiter(rate.Limit(rps), burst)
	actual, _ := store.LoadOrStore(ip, l)
	return actual.(*rate.Limiter)
}

func (pg *PeerGate) allowTrusted(ip string) bool {
	return pg.getBoundedLimiter(&pg.trustedLimiters, pg.trustedOverflow, ip, pg.trustedRPS, pg.trustedBurst).Allow()
}

func (pg *PeerGate) allowSync(ip string) bool {
	return pg.getBoundedLimiter(&pg.syncLimiters, pg.syncOverflow, ip, pg.syncRPS, pg.syncBurst).Allow()
}

func (pg *PeerGate) allowUnknown(ip string) bool {
	return pg.getBoundedLimiter(&pg.unknownLimiters, pg.unknownOverflow, ip, pg.unknownRPS, pg.unknownBurst).Allow()
}

func (pg *PeerGate) allowExplorer(ip string) bool {
	return pg.getBoundedLimiter(&pg.explorerLimiters, pg.explorerOverflow, ip, pg.explorerRPS, pg.explorerBurst).Allow()
}

                                                                       
                                                                              
                                                                               
                                                                              
                       
func (pg *PeerGate) getBoundedLimiter(store *sync.Map, overflow *rate.Limiter, ip string, rps float64, burst int) *rate.Limiter {
	if v, ok := store.Load(ip); ok {
		return v.(*rate.Limiter)
	}

	pg.limiterMu.Lock()
	defer pg.limiterMu.Unlock()
	if v, ok := store.Load(ip); ok {
		return v.(*rate.Limiter)
	}
	entries := &pg.limiterEntries
	if store == &pg.trustedLimiters || store == &pg.explorerLimiters {
		                                                                      
		                                                                           
		entries = &pg.priorityLimiterEntries
	}
	if *entries >= pg.maxTrackedIPs {
		return overflow
	}
	l := rate.NewLimiter(rate.Limit(rps), burst)
	store.Store(ip, l)
	*entries++
	return l
}

func (pg *PeerGate) deleteFailure(ip string) {
	pg.stateMu.Lock()
	if _, loaded := pg.failCounts.LoadAndDelete(ip); loaded && pg.failEntries > 0 {
		pg.failEntries--
	}
	pg.stateMu.Unlock()
}

func (pg *PeerGate) isExplorerService(r *http.Request) bool {
	if r == nil || !strings.EqualFold(strings.TrimSpace(r.Header.Get(config.ExplorerServiceHeader)), "explorer") {
		return false
	}
	if pg.explorerToken == "" {
		return false
	}
	got := strings.TrimSpace(r.Header.Get(config.ExplorerServiceTokenHeader))
	return len(got) == len(pg.explorerToken) && subtle.ConstantTimeCompare([]byte(got), []byte(pg.explorerToken)) == 1
}

                                                                         
                                                                              
                                              
func (pg *PeerGate) allowPath(path string) bool {
	return pg.allowPathForSource(path, false)
}

func (pg *PeerGate) allowPathForSource(path string, trusted bool) bool {
	store := &pg.pathLimiters
	if trusted {
		store = &pg.trustedPathLimiters
	}
	rps, ok := pg.pathRates[path]
	burst := 0
	if !ok || rps <= 0 {
		rps = pg.defaultPathRPS
		burst = pg.defaultPathBurst
	}
	if rps <= 0 {
		return false
	}
	v, loaded := store.Load(path)
	if !loaded {
		if burst <= 0 {
			burst = int(rps)
		}
		if burst < 1 {
			burst = 1
		}
		l := rate.NewLimiter(rate.Limit(rps), burst)
		v, _ = store.LoadOrStore(path, l)
	}
	return v.(*rate.Limiter).Allow()
}

                                                               
func (pg *PeerGate) logPathDrop(path, ip string) {
	if pg.logger == nil {
		return
	}
	val, _ := pg.pathStats.LoadOrStore(path, &pathDropStats{lastLogTime: time.Now()})
	st := val.(*pathDropStats)
	st.mu.Lock()
	st.dropCount++
	if now := time.Now(); now.Sub(st.lastLogTime) >= 5*time.Second {
		count := st.dropCount
		st.dropCount = 0
		st.lastLogTime = now
		st.mu.Unlock()
		pg.logger.Warn("[PeerGate] Path %s overloaded: dropped %d reqs in 5s (latest IP: %s)", path, count, ip)
		return
	}
	st.mu.Unlock()
}

                                     

func (pg *PeerGate) cleanupLoop() {
	for {
		select {
		case <-pg.cleanupTicker.C:
			pg.cleanup()
		case <-pg.stopCh:
			return
		}
	}
}

func (pg *PeerGate) cleanup() {
	now := time.Now().Unix()
	ttlSec := int64(pg.trustedTTL.Seconds())

	                        
	pg.mu.Lock()
	for ip, entry := range pg.trustedIPs {
		if now-atomic.LoadInt64(&entry.LastSeen) > ttlSec {
			delete(pg.trustedIPs, ip)
		}
	}
	pg.mu.Unlock()

	                                                                          
	                                                                             
	                                                                              
	pg.stateMu.Lock()
	pg.failCounts.Range(func(k, _ interface{}) bool {
		pg.failCounts.Delete(k)
		return true
	})
	pg.failEntries = 0
	pg.banUntil.Range(func(k, v interface{}) bool {
		if now > v.(int64) {
			pg.banUntil.Delete(k)
			if pg.banEntries > 0 {
				pg.banEntries--
			}
		}
		return true
	})
	pg.stateMu.Unlock()

	                                                                         
	                                                                          
	                                
	pg.limiterMu.Lock()
	for _, store := range []*sync.Map{&pg.trustedLimiters, &pg.syncLimiters, &pg.unknownLimiters, &pg.explorerLimiters} {
		store.Range(func(k, v interface{}) bool {
			store.Delete(k)                                                
			return true
		})
	}
	pg.limiterEntries = 0
	pg.priorityLimiterEntries = 0
	pg.limiterMu.Unlock()
}

                                                                  

                                 
func (pg *PeerGate) GetStats() (trustedCount int, bannedCount int) {
	pg.mu.RLock()
	trustedCount = len(pg.trustedIPs)
	pg.mu.RUnlock()
	pg.banUntil.Range(func(_, _ interface{}) bool {
		bannedCount++
		return true
	})
	return
}

                                     

func extractIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func computePushQueryDigest(query *pb.PushQuery) []byte {
	return consensus.ComputePushQueryDigest(query)
}
