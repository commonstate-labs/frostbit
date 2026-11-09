                   
package config

import (
	"encoding/json"
	"fmt"
	"math"
	"net"
	"os"
	"runtime"
	"strings"
	"time"

	"dex/arbitrage"
	"dex/certchain"
	bridgeasset "dex/frost/asset"
	"dex/liveness"
	"dex/logs"
	"dex/protocol"
)

const DefaultConsensusPollInterval = 25 * time.Millisecond

func DefaultFinalizedVMCommitConcurrency() int {
	n := runtime.GOMAXPROCS(0) / 2
	if n < 2 {
		return 2
	}
	if n > 8 {
		return 8
	}
	return n
}

                                                 
type PeerGateConfig struct {
	TrustedRPS         float64            `json:"trustedRPS"`                                             
	TrustedBurst       int                `json:"trustedBurst"`                                             
	SyncRPS            float64            `json:"syncRPS"`                                                 
	SyncBurst          int                `json:"syncBurst"`                                                 
	UnknownRPS         float64            `json:"unknownRPS"`                                          
	UnknownBurst       int                `json:"unknownBurst"`                                       
	ExplorerRPS        float64            `json:"explorerRPS"`                                          
	ExplorerBurst      int                `json:"explorerBurst"`                                   
	MaxTrackedIPs      int                `json:"maxTrackedIPs"`                                                                           
	BanThreshold       int                `json:"banThreshold"`                                                            
	BanDuration        time.Duration      `json:"banDuration"`                                       
	TrustedTTL         time.Duration      `json:"trustedTTL"`                                                   
	PathRateLimits     map[string]float64 `json:"pathRateLimits"`                                  
	DefaultPathRPS     float64            `json:"defaultPathRPS"`                                          
	DefaultPathBurst   int                `json:"defaultPathBurst"`                                          
	MaxInflight        int                `json:"maxInflight"`                                                                               
	ConsensusReserve   int                `json:"consensusReserve"`                                                                  
	TxBatchMaxInflight int                `json:"txBatchMaxInflight"`                                  
	EnforceLoopback    bool               `json:"enforceLoopback"`                                                                
}

const (
	peerGateInflightPerCPU = 64
	peerGateMinInflight    = 256
	peerGateMaxInflight    = 2048
)

func peerGateInflightForCPU(cpu int) (maxInflight, consensusReserve int) {
	switch {
	case cpu <= peerGateMinInflight/peerGateInflightPerCPU:
		maxInflight = peerGateMinInflight
	case cpu >= peerGateMaxInflight/peerGateInflightPerCPU:
		maxInflight = peerGateMaxInflight
	default:
		maxInflight = cpu * peerGateInflightPerCPU
	}
	return maxInflight, maxInflight / 4
}

                                                                                                        
                                                                                                      
func DefaultPeerGateInflight() (maxInflight, consensusReserve int) {
	return peerGateInflightForCPU(runtime.GOMAXPROCS(0))
}

                                                                                       
                                                                                                 
func ResolvePeerGateInflight(cfg PeerGateConfig) PeerGateConfig {
	if cfg.MaxInflight == 0 {
		cfg.MaxInflight, _ = DefaultPeerGateInflight()
	}
	if cfg.ConsensusReserve == 0 && cfg.MaxInflight > 0 {
		cfg.ConsensusReserve = cfg.MaxInflight / 4
		if cfg.ConsensusReserve == 0 && cfg.MaxInflight > 1 {
			cfg.ConsensusReserve = 1
		}
	}
	return cfg
}

                                                     
func DefaultPeerGateConfig() PeerGateConfig {
	maxInflight, consensusReserve := DefaultPeerGateInflight()
	return PeerGateConfig{
		                                                                       
		                                                                           
		                                                                        
		TrustedRPS: 500, TrustedBurst: 1000,
		SyncRPS: 5, SyncBurst: 10,
		UnknownRPS: 0.1, UnknownBurst: 1,
		ExplorerRPS: 500, ExplorerBurst: 1000,
		MaxTrackedIPs: 8192,
		BanThreshold:  5, BanDuration: 5 * time.Minute,
		TrustedTTL:     time.Hour,
		DefaultPathRPS: 500, DefaultPathBurst: 1000,
		MaxInflight: maxInflight, ConsensusReserve: consensusReserve, TxBatchMaxInflight: 8,
		PathRateLimits: map[string]float64{
			                                   
			"/pushquery": 5000, "/pullquery": 5000, "/gossipAnyMsg": 5000, "/chits": 5000,
			"/heightquery": 2000, "/acceptedfrontier": 2000, "/statedb/snapshot/shards": 500,
			"/statedb/checkpoint/files": 500, "/statedb/checkpoint/chunk": 200,
			"/pendingblocks": 1000, "/frostmsg": 5000,
			                        
			"/tx": 3000, "/txbatch": 3000, "/getdata": 3000, "/batchgetdata": 3000,
			"/getblock": 2000, "/getsyncblocks": 2000, "/getrecentblocks": 2000,
			"/getaccount": 2000, "/getaccountbalances": 2000, "/gettxreceipt": 2000,
			"/getblockbyid": 2000, "/getchits": 1000,
			                           
			"/frost/withdraw/status": 1000, "/frost/withdraws": 1000,
			"/frost/vault/group_pubkey": 1000, "/frost/vault/config": 1000, "/frost/vault/transition_status": 1000,
			"/frost/vault/dkg_commitments": 1000, "/frost/withdraw/list": 1000,
			"/frost/dkg/list": 1000, "/witness/requests": 1000, "/witness/list": 1000,
			"/trades": 1000, "/gettoken": 1000, "/frost/config": 500,
			"/frost/signed_package": 500, "/gettokenregistry": 500, "/orderbook": 300,
			                  
			"/status": 200, "/block/timing": 200, "/nodes": 200, "/discovery/miner_ips": 200, "/put": 100,
			"/frost/health": 100, "/frost/metrics": 100, "/logs": 50,
			"/orderbook/debug": 50,
			                                                               
			"/events/stream": 5, "/simulator/config": 20, "/_unmatched": 20,
		},
	}
}

                         
type Config struct {
	Server      ServerConfig
	Database    DatabaseConfig
	Network     NetworkConfig
	Consensus   ConsensusConfig
	TxPool      TxPoolConfig
	Sender      SenderConfig
	Discovery   DiscoveryConfig
	Auth        AuthConfig
	Window      WindowConfig                          
	Frost       FrostConfig                                      
	Witness     WitnessConfig                                  
	Liveness    liveness.Config                                      
	PeerGate    PeerGateConfig                                           
	Diagnostics DiagnosticsConfig                     
	Arbitrage   arbitrage.EngineConfig                                                                
}

                                                                      
                                                                
type ConsensusConfig struct {
	K                    int
	AlphaPreference      int
	AlphaConfidence      int
	Beta                 int
	PollInterval         time.Duration
	QueryTimeout         time.Duration
	MaxConcurrentQueries int
	ProposalInterval     time.Duration
}

type consensusConfigJSON ConsensusConfig

                                                                               
                                                                                
                                                           
func (c *ConsensusConfig) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for name := range fields {
		if strings.EqualFold(name, "Alpha") {
			return fmt.Errorf("legacy Consensus.Alpha is not supported; use Consensus.AlphaPreference and Consensus.AlphaConfidence")
		}
	}
	return json.Unmarshal(data, (*consensusConfigJSON)(c))
}

                                                               
type DiagnosticsConfig struct {
	                                                               
	                                                                                    
	                                                                 
	DumpCertEvidence bool
}

const DefaultMaxTxBatchTransactions = 5000

                                     
type ServerConfig struct {
	            
	TLSMinVersion string         
	TLSMaxVersion string         

	             
	QUICKeepAlivePeriod time.Duration                    
	QUICMaxIdleTimeout  time.Duration                   
	QUICAllow0RTT       bool                 

	             
	HTTPTimeout               time.Duration                    
	HTTPReadHeaderTimeout     time.Duration                   
	HTTPIdleTimeout           time.Duration                    
	MaxHeaderBytes            int                    
	MaxRequestBodySize        int64                           
	MaxTxBatchTransactions    int                                                                
	MaxConnections            int                                        
	MaxConnectionsPerIP       int                                               
	QUICHandshakeIdleTimeout  time.Duration                           
	QUICMaxIncomingStreams    int64                                          
	QUICMaxIncomingUniStreams int64                                          

	               
	CertValidityDays    int       
	TLSSessionCacheSize int       

	                                                                                                                       
	AdminToken string

	                                                                           
	                                                                            
	                                
	EnableDebugAPI bool
	EnablePprof    bool
	PprofBind      string
}

                                          
type DatabaseConfig struct {
	MemTableSize  int64                           
	MaxBatchSize  int                                   
	FlushInterval time.Duration                         
	RuntimeDBDir  string                                                         
	QueryDBDir    string                                                               
	ArchiveDBDir  string                                                          
	                                                                                           
	FinalizationForceFlushEveryN        int                                                                     
	FinalizationForceFlushInterval      time.Duration                           
	FinalizedVMCommitConcurrency        int                                                                                           
	BlockPruneStartFreePercent          float64                                                                   
	BlockPruneAggressiveFreePercent     float64                                                                   
	BlockPruneStartHeightsPerBlock      int                                                                            
	BlockPruneAggressiveHeightsPerBlock int                                                                            
	BlockPruneKeepRecentHeights         uint64                                                           
	FrostArchiveNode                    bool                                                                                  

	                  
	WriteQueueSize int

	               
	BlockCacheSize   int                              
	BlockCacheSizeDB int64                                         
	TableCacheShards int                                                                          
	NumMemtables     int                     
	NumCompactors    int                           
	StateDB          StateDBConfig
}

                                                                   
type StateDBConfig struct {
	Backend          string                      
	DataDir          string                                         
	ShardHexWidth    int
	PageSize         int
	TableCacheShards int
	CheckpointKeep   int
	CheckpointEvery  uint64                                      
}

                             
type NetworkConfig struct {
	                     
	BasePort        int           
	MaxNodes        int          
	DefaultNumNodes int          
	ByzantineNodes  int        
	AdvertiseHost   string `json:"advertise_host,omitempty"`                                                       

	                  
	PeerSampleSize     int      
	RandomMinerCount   int     
	BroadcastPeerCount int     
	MaxBroadcastPeers  int      

	               
	NetworkLatency    time.Duration                          
	ConnectionTimeout time.Duration                   
	HandshakeTimeout  time.Duration                    

	                                                
	PacketLossRate float64                             
	MinLatency     time.Duration                
	MaxLatency     time.Duration                
}

                               
type TxPoolConfig struct {
	               
	PendingTxCacheSize            int              
	ShortPendingTxCacheSize       int              
	CacheTxSize                   int              
	ShortTxCacheSize              int              
	AppliedBloomEstimatedItems    int                
	AppliedBloomFalsePositiveRate float64        
	AppliedBloomRebuildBatchSize  int              

	               
	MessageQueueSize       int            
	MaxPendingTxs          int            
	MaxPendingBytes        int64                                   
	MaxPendingPerAccount   int                               
	MaxPendingAccountBytes int64                                   
	MaxFutureNonceGap      uint64                                                

	               
	TargetTxsPerBlock   int                                                          
	MaxTxsPerBlock      int                                
	MaxTxsLimitPerBlock int                                   
	TxPerMerkleTree     int        
	ShortHashSize       int     
	MinTxsForProposal   int     

	               
	TxExpirationTime time.Duration                                                  
}

                               
type SenderConfig struct {
	               
	WorkerCount   int       
	QueueCapacity int         

	                                                 
	ControlMaxInflightPerTarget   int                                                  
	DataMaxInflightPerTarget      int                                                  
	ImmediateMaxInflightPerTarget int                                                  
	InflightRequeueDelay          time.Duration                                           

	               
	DefaultMaxRetries int               
	ControlMaxRetries int               
	DataMaxRetries    int               
	JitterFactor      float64                                  
	TaskExpireTimeout time.Duration                                             

	               
	BaseRetryDelay      time.Duration                                    
	MaxRetryDelay       time.Duration                           
	ControlTaskTimeout  time.Duration                           
	DataTaskDropTimeout time.Duration                    

	                             
	TxGossipFanout            int
	TxGossipBatchSize         int
	TxGossipFlushInterval     time.Duration
	TxGossipInputCapacity     int
	TxGossipMaxPendingPerPeer int
}

type DiscoveryConfig struct {
	Enabled        bool
	SyncInterval   time.Duration
	Fanout         int
	BootstrapPeers []string
}

                          
type AuthConfig struct {
	AuthEnabled     bool           
	ServerChallenge string                             
}

                                       
type WindowStage struct {
	Duration          time.Duration                      
	Probability       float64                                                                
	ExpectedProposers float64                                                                                        
}

                                  
type WindowConfig struct {
	Enabled bool                                     
	Stages  []WindowStage                         
}

                                                                               
                                                                             
                                                                             
func ValidateWindowConfig(window WindowConfig) error {
	if !window.Enabled {
		return nil
	}
	if len(window.Stages) == 0 {
		return fmt.Errorf("enabled window configuration has no stages")
	}
	for i, stage := range window.Stages {
		if stage.Duration < 0 {
			return fmt.Errorf("window stage %d has negative duration", i)
		}
		if stage.Duration == 0 && i != len(window.Stages)-1 {
			return fmt.Errorf("only the final window stage may have zero duration")
		}
		if stage.Probability < 0 || stage.Probability > 1 || math.IsNaN(stage.Probability) || math.IsInf(stage.Probability, 0) {
			return fmt.Errorf("window stage %d has invalid probability %.6f", i, stage.Probability)
		}
		if stage.ExpectedProposers < 0 || math.IsNaN(stage.ExpectedProposers) || math.IsInf(stage.ExpectedProposers, 0) {
			return fmt.Errorf("window stage %d has invalid expected proposer count %.6f", i, stage.ExpectedProposers)
		}
	}
	return nil
}

                                                                             
func WindowConfigsEqual(left, right WindowConfig) bool {
	if left.Enabled != right.Enabled || len(left.Stages) != len(right.Stages) {
		return false
	}
	for i := range left.Stages {
		if left.Stages[i] != right.Stages[i] {
			return false
		}
	}
	return true
}

                                   
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			TLSMinVersion:             "1.3",
			TLSMaxVersion:             "1.3",
			QUICKeepAlivePeriod:       10 * time.Second,
			QUICMaxIdleTimeout:        5 * time.Minute,
			QUICAllow0RTT:             true,
			HTTPTimeout:               30 * time.Second,
			HTTPReadHeaderTimeout:     5 * time.Second,
			HTTPIdleTimeout:           60 * time.Second,
			MaxHeaderBytes:            64 << 10,
			MaxRequestBodySize:        10 << 20,
			MaxTxBatchTransactions:    DefaultMaxTxBatchTransactions,
			MaxConnections:            1024,
			MaxConnectionsPerIP:       128,
			QUICHandshakeIdleTimeout:  5 * time.Second,
			QUICMaxIncomingStreams:    128,
			QUICMaxIncomingUniStreams: 16,
			CertValidityDays:          365,
			TLSSessionCacheSize:       128,
			EnableDebugAPI:            false,
			EnablePprof:               false,
			PprofBind:                 "127.0.0.1",
		},
		Database: DatabaseConfig{
			MemTableSize:                        8 << 20,
			MaxBatchSize:                        1000,
			FlushInterval:                       500 * time.Millisecond,
			RuntimeDBDir:                        "runtime",
			QueryDBDir:                          "query",
			ArchiveDBDir:                        "archive",
			FinalizationForceFlushEveryN:        3,
			FinalizationForceFlushInterval:      10 * time.Second,
			FinalizedVMCommitConcurrency:        DefaultFinalizedVMCommitConcurrency(),
			BlockPruneStartFreePercent:          50,
			BlockPruneAggressiveFreePercent:     40,
			BlockPruneStartHeightsPerBlock:      1,
			BlockPruneAggressiveHeightsPerBlock: 100,
			BlockPruneKeepRecentHeights:         100,
			WriteQueueSize:                      5000,
			BlockCacheSize:                      10,
			BlockCacheSizeDB:                    128 << 20,
			TableCacheShards:                    4,
			NumMemtables:                        3,
			NumCompactors:                       2,
			StateDB: StateDBConfig{
				Backend:          "pebble",
				DataDir:          "state",
				ShardHexWidth:    1,
				PageSize:         1000,
				TableCacheShards: 4,
				                                                                  
				                                                                           
				                                                            
				                                                                             
				CheckpointKeep:  50,
				CheckpointEvery: certchain.DefaultCheckpointInterval,
			},
		},
		Network: NetworkConfig{
			BasePort:           6000,
			MaxNodes:           100,
			DefaultNumNodes:    20,
			ByzantineNodes:     0,
			AdvertiseHost:      "127.0.0.1",
			PeerSampleSize:     10,
			RandomMinerCount:   3,
			BroadcastPeerCount: 5,
			MaxBroadcastPeers:  20,
			NetworkLatency:     100 * time.Millisecond,
			ConnectionTimeout:  10 * time.Second,
			HandshakeTimeout:   10 * time.Second,
			PacketLossRate:     0.0,
			MinLatency:         0,
			MaxLatency:         0,
		},
		Consensus: ConsensusConfig{
			K:                    15,
			AlphaPreference:      8,
			AlphaConfidence:      12,
			Beta:                 15,
			PollInterval:         DefaultConsensusPollInterval,
			QueryTimeout:         4 * time.Second,
			MaxConcurrentQueries: 8,
			ProposalInterval:     5 * time.Second,
		},
		TxPool: TxPoolConfig{
			PendingTxCacheSize:            10000,
			ShortPendingTxCacheSize:       10000,
			CacheTxSize:                   10000,
			ShortTxCacheSize:              10000,
			AppliedBloomEstimatedItems:    50000000,
			AppliedBloomFalsePositiveRate: 0.01,
			AppliedBloomRebuildBatchSize:  100000,
			MessageQueueSize:              10000,
			MaxPendingTxs:                 5000,
			MaxPendingBytes:               64 << 20,
			MaxPendingPerAccount:          2048,
			MaxPendingAccountBytes:        4 << 20,
			MaxFutureNonceGap:             2047,
			TargetTxsPerBlock:             512,
			MaxTxsPerBlock:                2500,
			MaxTxsLimitPerBlock:           2500,
			TxPerMerkleTree:               1000,
			ShortHashSize:                 8,
			MinTxsForProposal:             1,
			TxExpirationTime:              10 * time.Minute,
		},
		Sender: SenderConfig{
			WorkerCount:                   16,
			QueueCapacity:                 200,
			ControlMaxInflightPerTarget:   3,
			DataMaxInflightPerTarget:      6,
			ImmediateMaxInflightPerTarget: 2,
			InflightRequeueDelay:          25 * time.Millisecond,
			DefaultMaxRetries:             0,                                                            
			ControlMaxRetries:             0,                         
			DataMaxRetries:                0,                         
			JitterFactor:                  0.3,
			TaskExpireTimeout:             10 * time.Second,                                    
			BaseRetryDelay:                100 * time.Millisecond,
			MaxRetryDelay:                 2 * time.Second,
			ControlTaskTimeout:            1500 * time.Millisecond,
			DataTaskDropTimeout:           0,
			TxGossipFanout:                3,
			TxGossipBatchSize:             256,
			TxGossipFlushInterval:         50 * time.Millisecond,
			TxGossipInputCapacity:         8192,
			TxGossipMaxPendingPerPeer:     4096,
		},
		Discovery: DiscoveryConfig{
			Enabled:        true,
			SyncInterval:   10 * time.Second,
			Fanout:         2,
			BootstrapPeers: nil,
		},
		Auth: AuthConfig{
			AuthEnabled:     false,
			ServerChallenge: "server_challenge_123456",
		},
		Window: WindowConfig{
			Enabled: true,
			Stages: []WindowStage{
				{Duration: 5 * time.Second, Probability: 0.05, ExpectedProposers: 5},                                
				{Duration: 5 * time.Second, Probability: 0.15, ExpectedProposers: 15},                                
				{Duration: 10 * time.Second, Probability: 0.30, ExpectedProposers: 30},                               
				{Duration: 0, Probability: 0.50, ExpectedProposers: 50},                                              
			},
		},
		Frost:       DefaultFrostConfig(),
		Witness:     DefaultWitnessConfig(),
		Liveness:    liveness.DefaultConfig(),
		PeerGate:    DefaultPeerGateConfig(),
		Diagnostics: DiagnosticsConfig{DumpCertEvidence: false},
		Arbitrage:   arbitrage.DefaultConfig(),
	}
}

                                                                                             
func LoadFromDir(dir string) (*Config, error) {
	return loadConfig(ConfigPath(dir, "config.json"), ConfigPath(dir, "frost_default.json"), ConfigPath(dir, "witness_default.json"), true)
}

                                                       
func LoadFromFile(path string) (*Config, error) {
	configPath := strings.TrimSpace(path)
	if configPath == "" {
		configPath = "config/config.json"
	}
	return loadConfig(configPath, "config/frost_default.json", "config/witness_default.json", strings.TrimSpace(path) == "")
}

func loadConfig(configPath, frostPath, witnessPath string, allowMissingConfig bool) (*Config, error) {
	cfg := DefaultConfig()

	                                                                         
	                                                                         
	                                                           
	frostCfg, err := LoadFrostConfig(frostPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load frost config %s: %w", frostPath, err)
	}
	cfg.Frost = frostCfg
	logs.Info("Loaded frost config from %s", frostPath)

	                              
	witnessCfg, err := LoadWitnessConfig(witnessPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load witness config %s: %w", witnessPath, err)
	}
	cfg.Witness = witnessCfg
	logs.Info("Loaded witness config from %s", witnessPath)

	data, err := os.ReadFile(configPath)
	if err != nil {
		                                             
		if os.IsNotExist(err) && allowMissingConfig {
			if err := cfg.Validate(); err != nil {
				return nil, fmt.Errorf("invalid default config: %w", err)
			}
			return cfg, nil
		}
		return nil, fmt.Errorf("failed to read config file %s: %w", configPath, err)
	}

	                                 
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file %s: %w", configPath, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config file %s: %w", configPath, err)
	}
	logs.Info("Loaded config from %s", configPath)

	return cfg, nil
}

                                 
func (c *Config) Validate() error {
	                                                                              
	if err := ValidateVaultCommitteePolicy(c.Frost); err != nil {
		return fmt.Errorf("Frost: %w", err)
	}
	                                                                                 
	if err := validateWitnessChallengeStakeBounds(c.Witness); err != nil {
		return fmt.Errorf("Witness: %w", err)
	}
	                           
	if err := arbitrage.ValidateConfig(c.Arbitrage); err != nil {
		return fmt.Errorf("Arbitrage: %w", err)
	}
	if c.Server.HTTPTimeout <= 0 || c.Server.HTTPReadHeaderTimeout <= 0 || c.Server.HTTPIdleTimeout <= 0 {
		return fmt.Errorf("HTTP timeouts must be positive")
	}
	if c.Server.MaxHeaderBytes <= 0 || c.Server.MaxRequestBodySize <= 0 {
		return fmt.Errorf("HTTP size limits must be positive")
	}
	if c.Server.MaxTxBatchTransactions <= 0 || c.Server.MaxTxBatchTransactions > DefaultMaxTxBatchTransactions {
		return fmt.Errorf("MaxTxBatchTransactions must be within 1..%d", DefaultMaxTxBatchTransactions)
	}
	if c.Server.MaxConnections <= 0 || c.Server.MaxConnectionsPerIP <= 0 || c.Server.MaxConnectionsPerIP > c.Server.MaxConnections {
		return fmt.Errorf("connection limits must satisfy 0 < per-IP <= total")
	}
	if c.Server.QUICHandshakeIdleTimeout <= 0 || c.Server.QUICMaxIdleTimeout <= 0 || c.Server.QUICMaxIncomingStreams <= 0 || c.Server.QUICMaxIncomingUniStreams <= 0 {
		return fmt.Errorf("QUIC timeouts and stream limits must be positive")
	}
	if c.Server.EnablePprof && !isLoopbackHost(c.Server.PprofBind) {
		return fmt.Errorf("pprof must bind to a loopback address")
	}
	if c.PeerGate.DefaultPathRPS <= 0 || c.PeerGate.DefaultPathBurst <= 0 {
		return fmt.Errorf("PeerGate default path rate and burst must be positive")
	}
	if c.PeerGate.MaxInflight < 0 || c.PeerGate.ConsensusReserve < 0 {
		return fmt.Errorf("PeerGate inflight limits must be non-negative; use 0 for automatic sizing")
	}
	c.PeerGate = ResolvePeerGateInflight(c.PeerGate)
	if c.PeerGate.MaxInflight <= 0 || c.PeerGate.ConsensusReserve < 0 || c.PeerGate.ConsensusReserve >= c.PeerGate.MaxInflight {
		return fmt.Errorf("PeerGate inflight limits must satisfy 0 <= reserve < total")
	}
	if c.PeerGate.TxBatchMaxInflight <= 0 || c.PeerGate.TxBatchMaxInflight > c.PeerGate.MaxInflight-c.PeerGate.ConsensusReserve {
		return fmt.Errorf("PeerGate tx batch inflight limit must fit the non-consensus capacity")
	}
	if c.Network.MaxNodes <= 0 {
		return fmt.Errorf("MaxNodes must be positive")
	}
	if c.TxPool.MaxTxsPerBlock <= 0 {
		return fmt.Errorf("MaxTxsPerBlock must be positive")
	}
	if c.TxPool.MaxPendingBytes <= 0 || c.TxPool.MaxPendingPerAccount <= 0 || c.TxPool.MaxPendingAccountBytes <= 0 || c.TxPool.TxExpirationTime <= 0 {
		return fmt.Errorf("TxPool pending byte/account limits and expiration must be positive")
	}
	if c.TxPool.MaxTxsLimitPerBlock <= 0 {
		return fmt.Errorf("MaxTxsLimitPerBlock must be positive")
	}
	if c.TxPool.TargetTxsPerBlock < 0 {
		return fmt.Errorf("TargetTxsPerBlock must be non-negative")
	}
	if c.TxPool.TargetTxsPerBlock > 0 && c.TxPool.TargetTxsPerBlock > c.TxPool.MaxTxsLimitPerBlock {
		return fmt.Errorf("TargetTxsPerBlock must not exceed MaxTxsLimitPerBlock")
	}
	if c.Consensus.K <= 0 {
		return fmt.Errorf("Consensus.K must be positive")
	}
	if c.Consensus.AlphaPreference <= c.Consensus.K/2 || c.Consensus.AlphaPreference > c.Consensus.K {
		return fmt.Errorf("Consensus.AlphaPreference must satisfy K/2 < AlphaPreference <= K")
	}
	if c.Consensus.AlphaConfidence <= c.Consensus.K/2 || c.Consensus.AlphaConfidence > c.Consensus.K {
		return fmt.Errorf("Consensus.AlphaConfidence must satisfy K/2 < AlphaConfidence <= K")
	}
	if c.Consensus.AlphaPreference > c.Consensus.AlphaConfidence {
		return fmt.Errorf("Consensus.AlphaPreference must be less than or equal to Consensus.AlphaConfidence")
	}
	if c.Consensus.Beta <= 0 {
		return fmt.Errorf("Consensus.Beta must be positive")
	}
	if c.Consensus.PollInterval <= 0 {
		return fmt.Errorf("Consensus.PollInterval must be positive")
	}
	if c.Consensus.MaxConcurrentQueries <= 0 || c.Consensus.MaxConcurrentQueries > c.Consensus.Beta {
		return fmt.Errorf("Consensus.MaxConcurrentQueries must satisfy 0 < MaxConcurrentQueries <= Beta")
	}
	if c.Consensus.QueryTimeout <= 0 {
		return fmt.Errorf("Consensus.QueryTimeout must be positive")
	}
	if c.Consensus.ProposalInterval <= 0 {
		return fmt.Errorf("Consensus.ProposalInterval must be positive")
	}
	if err := ValidateWindowConfig(c.Window); err != nil {
		return fmt.Errorf("Window: %w", err)
	}
	                                                                            
	                                                                        
	                                                                       
	if expected := DefaultConfig().Window; !WindowConfigsEqual(c.Window, expected) {
		return fmt.Errorf("Window must exactly match the protocol network profile")
	}
	if c.Liveness.ActivationThreshold < 0 || c.Liveness.ActivationThreshold > 1 {
		return fmt.Errorf("Liveness.ActivationThreshold must be within [0,1]")
	}
	if c.Liveness.ActivationThreshold > c.Liveness.Threshold {
		return fmt.Errorf("Liveness.ActivationThreshold must not exceed Liveness.Threshold")
	}
	if c.Liveness.MinSamples <= 0 {
		return fmt.Errorf("Liveness.MinSamples must be positive")
	}
	if c.Liveness.MinStake != protocol.MinMinerStakeRaw {
		return fmt.Errorf("Liveness.MinStake must equal the protocol miner minimum %d", protocol.MinMinerStakeRaw)
	}
	if c.Liveness.FreezeBlocks == 0 {
		return fmt.Errorf("Liveness.FreezeBlocks must be positive")
	}
	if c.Liveness.SlashPerReport == 0 || c.Liveness.SlashPerReport > c.Liveness.MinStake {
		return fmt.Errorf("Liveness.SlashPerReport must satisfy 0 < SlashPerReport <= MinStake")
	}
	                                                                             
	                                                                            
	                                                                            
	                                                                           
	                                                          
	if expected := liveness.DefaultConfig(); c.Liveness != expected {
		return fmt.Errorf("Liveness must exactly match the protocol network profile")
	}
	                   
	return nil
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" || strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

                                                             

                                                                                     
                                                        
const DefaultChainID uint64 = 1

                             
type GenesisConfig struct {
	                                                                                                  
	                                               
	ChainID uint64 `json:"chain_id,omitempty"`
	               
	Tokens []GenesisToken `json:"tokens"`
	                        
	Alloc map[string]GenesisAlloc `json:"alloc"`
}

                                                                                                   
func (g *GenesisConfig) EffectiveChainID() uint64 {
	if g == nil || g.ChainID == 0 {
		return DefaultChainID
	}
	return g.ChainID
}

                                  
type GenesisToken struct {
	Address        string `json:"address"`                                                                         
	TotalSupply    string `json:"total_supply"`                                               
	Owner          string `json:"owner"`                                                                     
	CanMint        bool   `json:"can_mint"`                                    
	Type           string `json:"type,omitempty"`                                              
	NativeChain    string `json:"native_chain,omitempty"`                                                 
	NativeContract string `json:"native_contract,omitempty"`                                           
}

                                  
type GenesisAlloc struct {
	Balances map[string]GenesisBalance `json:"balances"`                              
}

                              
type GenesisBalance struct {
	Balance            string `json:"balance"`                                       
	MinerLockedBalance string `json:"miner_locked_balance,omitempty"`                      
}

                                                
func DefaultGenesisConfig() *GenesisConfig {
	return &GenesisConfig{
		Tokens: defaultGenesisTokens(),
		Alloc:  make(map[string]GenesisAlloc),
	}
}

func defaultGenesisTokens() []GenesisToken {
	bridged := func(chain, contract, totalSupply string, canMint bool) GenesisToken {
		identity, err := bridgeasset.Resolve(chain, contract)
		if err != nil {
			panic(fmt.Sprintf("invalid built-in genesis asset %s/%s: %v", chain, contract, err))
		}
		return GenesisToken{
			Address:        identity.ID,
			TotalSupply:    totalSupply,
			Owner:          "system",
			CanMint:        canMint,
			Type:           "BRIDGED",
			NativeChain:    identity.Chain,
			NativeContract: identity.Contract,
		}
	}
	return []GenesisToken{
		{Address: "FB", TotalSupply: "1000000000000000000000000", Owner: "0x0", CanMint: false, Type: "SYSTEM"},
		bridged("btc", "", "2100000000000000", false),
		bridged("eth", "", "120000000000000000000000000", false),
		bridged("bnb", "", "200000000000000000000000000", false),
		bridged("base", "", "120000000000000000000000000", false),
		bridged("arb", "", "120000000000000000000000000", false),
		bridged("trx", "", "87000000000000000", false),
		bridged("eth", "0xdAC17F958D2ee523a2206206994597C13D831ec7", "1000000000000", true),
		bridged("eth", "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48", "1000000000000", true),
		bridged("trx", "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t", "1000000000000", true),
		bridged("base", "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913", "1000000000000", true),
		bridged("arb", "0xFd086bC7CD5C481DCC9C85ebE478A1C0b69FCbb9", "1000000000000", true),
		bridged("arb", "0xaf88d065e77c8cC2239327C5EDb3A432268e5831", "1000000000000", true),
		bridged("eth", "0x6B175474E89094C44Da98b954EedeAC495271d0F", "1000000000000000000000000", true),
		bridged("bnb", "0x8AC76a51cc950d9822D68b83fE1Ad97B32Cd580d", "1000000000000000000000000", true),
		bridged("bnb", "0x55d398326f99059fF775485246999027B3197955", "1000000000000000000000000", true),
	}
}

                                                                               
                                                                              
                                                                             
func ValidateGenesisConfig(genesis *GenesisConfig) error {
	if genesis == nil {
		return fmt.Errorf("genesis config is missing")
	}
	seenAddresses := make(map[string]struct{}, len(genesis.Tokens))
	seenNative := make(map[string]string, len(genesis.Tokens))
	for _, token := range genesis.Tokens {
		address := strings.TrimSpace(token.Address)
		if address == "" {
			return fmt.Errorf("genesis token address is empty")
		}
		if token.Address != address {
			return fmt.Errorf("genesis token address %q is not canonical", token.Address)
		}
		if _, exists := seenAddresses[address]; exists {
			return fmt.Errorf("duplicate genesis token address %q", address)
		}
		seenAddresses[address] = struct{}{}
		if !strings.EqualFold(strings.TrimSpace(token.Type), "BRIDGED") {
			if token.NativeChain != "" || token.NativeContract != "" {
				return fmt.Errorf("non-bridged genesis token %q cannot declare a native identity", address)
			}
			continue
		}
		identity, err := bridgeasset.Resolve(token.NativeChain, token.NativeContract)
		if err != nil {
			return fmt.Errorf("bridged genesis token %q: %w", address, err)
		}
		if token.NativeChain != identity.Chain || token.NativeContract != identity.Contract {
			return fmt.Errorf("bridged genesis token %q native identity is not canonical", address)
		}
		if address != identity.ID {
			return fmt.Errorf("bridged genesis token %q must use canonical AssetID %q", address, identity.ID)
		}
		nativeKey := identity.Chain + "\x00" + identity.Contract
		if previous, exists := seenNative[nativeKey]; exists {
			return fmt.Errorf("bridged genesis tokens %q and %q have the same native identity", previous, address)
		}
		seenNative[nativeKey] = address
	}
	return nil
}

                                                
func LoadGenesisConfig(path string) (*GenesisConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			                                       
			return DefaultGenesisConfig(), nil
		}
		return nil, fmt.Errorf("failed to read genesis config: %w", err)
	}

	genesis := DefaultGenesisConfig()
	if err := json.Unmarshal(data, &genesis); err != nil {
		return nil, fmt.Errorf("failed to parse genesis config: %w", err)
	}
	if err := ValidateGenesisConfig(genesis); err != nil {
		return nil, fmt.Errorf("invalid genesis config: %w", err)
	}

	return genesis, nil
}

                                                
func SaveGenesisConfig(path string, genesis *GenesisConfig) error {
	if err := ValidateGenesisConfig(genesis); err != nil {
		return fmt.Errorf("invalid genesis config: %w", err)
	}
	data, err := json.MarshalIndent(genesis, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal genesis config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write genesis config: %w", err)
	}

	return nil
}
