package consensus

import (
	chainconfig "dex/config"
	"fmt"
	"time"
)

                                                        
type Config struct {
	Network   NetworkConfig
	Consensus ConsensusConfig
	Node      NodeConfig
	Sync      SyncConfig
	Gossip    GossipConfig
	Window    chainconfig.WindowConfig
}

type NetworkConfig struct {
	NumNodes          int
	NumByzantineNodes int
	NetworkLatency    time.Duration
	PacketLossRate    float64
}

type ConsensusConfig struct {
	K                    int
	AlphaPreference      int
	AlphaConfidence      int
	Beta                 int
	PollInterval         time.Duration
	QueryTimeout         time.Duration
	MaxConcurrentQueries int
	NumHeights           int
	BlocksPerHeight      int
}

type NodeConfig struct {
	ProposalInterval time.Duration
}

type SyncConfig struct {
	CheckInterval   time.Duration
	BehindThreshold uint64
	BatchSize       uint64
	Timeout         time.Duration
	                                                                 
	                                                                                                                              
	DeepLagStateSyncThreshold uint64
	                                                                                 
	StateSyncPeers int
	                                                     
	StateSyncShardConcurrency int
	                                                   
	StateSyncPageSize int

	                                                                              
	                                                                            
	FrontierPollTimeout time.Duration
	ChitSoftGap         uint64
	ChitHardGap         uint64
	ChitGracePeriod     time.Duration
	ChitCooldown        time.Duration
	                                                                          
	                                                                             
	                                                                             
	                                                                               
	DeepLagMinAdditionalPeers int
}

type GossipConfig struct {
	Fanout   int
	Interval time.Duration
}

func DefaultConfig() *Config {
	windowConfig := chainconfig.DefaultConfig().Window
	windowConfig.Stages = append([]chainconfig.WindowStage(nil), windowConfig.Stages...)
	return &Config{
		Network: NetworkConfig{
			NumNodes:          100,
			NumByzantineNodes: 10,
			NetworkLatency:    100 * time.Millisecond,
			PacketLossRate:    0.1,
		},
		Consensus: ConsensusConfig{
			K:                    15,
			AlphaPreference:      8,
			AlphaConfidence:      12,
			Beta:                 15,
			PollInterval:         chainconfig.DefaultConsensusPollInterval,
			QueryTimeout:         4 * time.Second,
			MaxConcurrentQueries: 8,
			NumHeights:           10,
			BlocksPerHeight:      5,
		},
		Node: NodeConfig{
			ProposalInterval: 3000 * time.Millisecond,
		},
		Sync: SyncConfig{
			CheckInterval:   30 * time.Second,
			BehindThreshold: 2,
			BatchSize:       50,
			Timeout:         10 * time.Second,
			                                                                                         
			DeepLagStateSyncThreshold: 10,
			                                                                       
			StateSyncPeers:            4,
			StateSyncShardConcurrency: 8,
			StateSyncPageSize:         1000,

			FrontierPollTimeout:       2 * time.Second,
			ChitSoftGap:               1,
			ChitHardGap:               3,
			ChitGracePeriod:           1 * time.Second,
			ChitCooldown:              1500 * time.Millisecond,
			DeepLagMinAdditionalPeers: 2,
		},
		Gossip: GossipConfig{
			Fanout:   15,
			Interval: 50 * time.Millisecond,
		},
		Window: windowConfig,
	}
}

                                                                            
                                                                 
func NewRuntimeConfig(protocol chainconfig.ConsensusConfig, window chainconfig.WindowConfig) *Config {
	cfg := DefaultConfig()
	cfg.Consensus.K = protocol.K
	cfg.Consensus.AlphaPreference = protocol.AlphaPreference
	cfg.Consensus.AlphaConfidence = protocol.AlphaConfidence
	cfg.Consensus.Beta = protocol.Beta
	cfg.Consensus.PollInterval = protocol.PollInterval
	cfg.Consensus.QueryTimeout = protocol.QueryTimeout
	cfg.Consensus.MaxConcurrentQueries = protocol.MaxConcurrentQueries
	cfg.Node.ProposalInterval = protocol.ProposalInterval
	window.Stages = append([]chainconfig.WindowStage(nil), window.Stages...)
	cfg.Window = window
	return cfg
}

func windowConfigurationsEqual(left, right chainconfig.WindowConfig) bool {
	return chainconfig.WindowConfigsEqual(left, right)
}

func validateRuntimeWindowCompatibility(runtime, store chainconfig.WindowConfig) error {
	if err := validateWindowConfiguration(runtime); err != nil {
		return fmt.Errorf("invalid runtime window configuration: %w", err)
	}
	if err := validateWindowConfiguration(store); err != nil {
		return fmt.Errorf("invalid block store window configuration: %w", err)
	}
	if !windowConfigurationsEqual(runtime, store) {
		return fmt.Errorf("consensus window configuration does not match block store chain configuration")
	}
	if protocol := chainconfig.DefaultConfig().Window; !windowConfigurationsEqual(runtime, protocol) {
		return fmt.Errorf("consensus window configuration does not match the protocol network profile")
	}
	return nil
}
