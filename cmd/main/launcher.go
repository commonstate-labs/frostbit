package main

import (
	"context"
	"dex/config"
	"dex/consensus"
	frostrt "dex/frost/runtime"
	"dex/logs"
	"dex/protocol"
	"dex/types"
	"dex/utils"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

                                                                     
type BootstrapMode int

const (
	                                                                                                  
	GenesisBootstrap BootstrapMode = iota
	                                                                       
	LateJoin
)

                                                                    
type NodeConfig struct {
	           
	ID                  int                                                                                      
	KeyManager          *utils.KeyManager                                             
	ArbitrageKeyManager *utils.KeyManager                                   
	ArbitrageAddress    string                                                               
	Port                string                                 
	DataPath            string                                      
	ListenAddr          string                                                                        
	AdvertiseAddr       string                                                                    

	          
	                                                                          
	                                                                            
	                                                                          
	BootstrapEndpoints []BootstrapEndpoint
	                                                                             
	                                                                            
	                                                                       
	                                                                            
	GenesisPeers []PeerInfo

	                 
	Mode BootstrapMode

	                        
	Config        *config.Config
	GenesisConfig *config.GenesisConfig

	                                       
	FrostConfig                 config.FrostConfig
	FrostWrapKey                []byte                                             
	FrostGenesisSharesDir       string                                                                 
	FrostPlaintextGenesisShares bool                                                                  
	NetworkChainID              uint64
	FrostPeerManifestDigest     []byte

	                     
	ActiveMinerCount int                                                                  

	         
	ResumeFromExisting bool                                      
	RetainGenesisProof bool                                                                 

	                                                               
	                                                                           
	                            
	EnableOperatorControlAPI bool
}

                                                                                  
type PeerInfo struct {
	Index         int                                                             
	Address       string
	RewardAddress string                                                      
	SecpPublicKey []byte                                              
	BLSPublicKey  []byte                                      
	Port          string
	AdvertiseAddr string
	IsMiner       bool
}

                                                                              
                                                                           
                                                      
type BootstrapEndpoint struct {
	Address  string
	Endpoint string
}

                                                                          
                                                                              
                                                                            
func installBootstrapRouting(node *NodeInstance, endpoints []BootstrapEndpoint) error {
	if node == nil || node.ConsensusManager == nil {
		return fmt.Errorf("node routing dependencies are unavailable")
	}
	routes := make(map[types.NodeID]string, len(endpoints))
	seenEndpoints := make(map[string]string, len(endpoints))
	for i, configured := range endpoints {
		address := strings.TrimSpace(configured.Address)
		endpoint := strings.TrimSpace(configured.Endpoint)
		if !utils.IsDexAddress(address) {
			return fmt.Errorf("bootstrap route %d has invalid DEX address %q", i, address)
		}
		_, port, err := net.SplitHostPort(endpoint)
		if err != nil {
			return fmt.Errorf("bootstrap route %d endpoint must be host:port: %w", i, err)
		}
		portNumber, err := strconv.Atoi(port)
		if err != nil || portNumber < 1 || portNumber > 65535 {
			return fmt.Errorf("bootstrap route %d port must be in 1..65535", i)
		}
		id := types.NodeID(address)
		if previous, exists := routes[id]; exists && previous != endpoint {
			return fmt.Errorf("bootstrap address %s has conflicting endpoints", address)
		}
		if previous, exists := seenEndpoints[endpoint]; exists && previous != address {
			return fmt.Errorf("bootstrap endpoint %s is shared by %s and %s", endpoint, previous, address)
		}
		routes[id] = endpoint
		seenEndpoints[endpoint] = address
	}

	transport, ok := node.ConsensusManager.Transport.(*consensus.RealTransport)
	if !ok || transport == nil {
		return fmt.Errorf("real consensus transport is unavailable")
	}
	transport.SetBootstrapPeers(routes)
	return nil
}

                                                                 
type NodeLauncher struct {
	config   NodeConfig
	instance *NodeInstance

	mu       sync.Mutex
	started  bool
	stopped  bool
	stopOnce sync.Once
	stopErr  error
	                                                                          
	                                                               
	frostStartCancel context.CancelFunc
	frostStartDone   <-chan struct{}

	                                                                            
	                                                                  
	httpReady      chan struct{}
	listenersBound bool
}

                                                            
                                                                    
func NewNodeLauncher(cfg NodeConfig) (*NodeLauncher, error) {
	if cfg.RetainGenesisProof && (!cfg.ResumeFromExisting || cfg.Mode != GenesisBootstrap) {
		return nil, errors.New("retaining genesis requires resume=true and genesis bootstrap mode")
	}
	                                                                        
	genesisPeers := make([]PeerInfo, len(cfg.GenesisPeers))
	for i, peer := range cfg.GenesisPeers {
		peer.SecpPublicKey = append([]byte(nil), peer.SecpPublicKey...)
		peer.BLSPublicKey = append([]byte(nil), peer.BLSPublicKey...)
		peer.Port = ""
		peer.AdvertiseAddr = ""
		genesisPeers[i] = peer
	}
	cfg.GenesisPeers = genesisPeers
	cfg.BootstrapEndpoints = append([]BootstrapEndpoint(nil), cfg.BootstrapEndpoints...)
	cfg.FrostPeerManifestDigest = append([]byte(nil), cfg.FrostPeerManifestDigest...)

	if cfg.KeyManager == nil || !cfg.KeyManager.HasPrivateKey() {
		return nil, fmt.Errorf("node runtime identity is missing")
	}
	address := cfg.KeyManager.GetAddress()
	arbitrageEnabled := cfg.Config != nil && cfg.Config.Arbitrage.Enabled
	arbitrageKey := cfg.ArbitrageKeyManager
	arbitrageAddress := strings.TrimSpace(cfg.ArbitrageAddress)
	if arbitrageEnabled {
		if arbitrageKey == nil || !arbitrageKey.HasPrivateKey() {
			return nil, fmt.Errorf("arbitrage is enabled but its private key is missing")
		}
		derivedArbitrageAddress := arbitrageKey.GetAddress()
		if arbitrageAddress != "" && arbitrageAddress != derivedArbitrageAddress {
			return nil, fmt.Errorf("arbitrage private key derives address %s, not configured runtime address %s", derivedArbitrageAddress, arbitrageAddress)
		}
		if err := validateIndependentArbitrageAddress(address, derivedArbitrageAddress); err != nil {
			return nil, err
		}
		arbitrageAddress = derivedArbitrageAddress
	} else if arbitrageKey != nil || arbitrageAddress != "" {
		return nil, fmt.Errorf("arbitrage identity supplied while arbitrage is disabled")
	}
	dataPath, err := validateNodeDataPath(cfg.DataPath, cfg.ResumeFromExisting)
	if err != nil {
		return nil, err
	}
	listenAddr, err := resolveNodeListenAddr(cfg.ListenAddr, cfg.AdvertiseAddr, cfg.Port)
	if err != nil {
		return nil, err
	}
	                                                                           
	                                                                            
	                                                  
	consensus.ClearNodePublicKeys()
	for i, peer := range cfg.GenesisPeers {
		if !peer.IsMiner {
			continue
		}
		normalized, err := normalizePeerIdentity(peer)
		if err != nil {
			return nil, fmt.Errorf("invalid genesis validator manifest entry %d: %w", i, err)
		}
		consensus.RegisterNodePublicKey(types.NodeID(normalized.Address), normalized.SecpPublicKey)
	}

	var frostWrapKey []byte
	if len(cfg.FrostWrapKey) > 0 {
		frostWrapKey = append([]byte(nil), cfg.FrostWrapKey...)
	}
	node := &NodeInstance{
		ID:                          cfg.ID,
		KeyManager:                  cfg.KeyManager,
		ArbitrageKeyManager:         arbitrageKey,
		ArbitrageAddress:            arbitrageAddress,
		Address:                     address,
		Port:                        cfg.Port,
		DataPath:                    dataPath,
		ListenAddr:                  listenAddr,
		AdvertiseAddr:               normalizeAdvertiseAddr(cfg.AdvertiseAddr, cfg.Port),
		FrostWrapKey:                frostWrapKey,
		FrostGenesisSharesDir:       strings.TrimSpace(cfg.FrostGenesisSharesDir),
		FrostPlaintextGenesisShares: cfg.FrostPlaintextGenesisShares,
		NetworkChainID:              cfg.NetworkChainID,
		FrostPeerManifestDigest:     append([]byte(nil), cfg.FrostPeerManifestDigest...),
		EnableOperatorControlAPI:    cfg.EnableOperatorControlAPI,
	}
	                                                                             
	cfg.KeyManager = nil
	cfg.ArbitrageKeyManager = nil
	if cfg.Config != nil {
		node.EnableDebugAPI = cfg.Config.Server.EnableDebugAPI
		node.EnablePprof = cfg.Config.Server.EnablePprof
		node.PprofBind = cfg.Config.Server.PprofBind
	}
	                                                                           
	                                                                          
	wipeBytes(cfg.FrostWrapKey)
	cfg.FrostWrapKey = nil

	                
	node.Logger = logs.NewNodeLogger(address, 2000)

	return &NodeLauncher{
		config:    cfg,
		instance:  node,
		httpReady: make(chan struct{}),
	}, nil
}

func validateNodeDataPath(path string, resume bool) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", fmt.Errorf("node data directory is empty")
	}
	abs, err := filepath.Abs(filepath.Clean(trimmed))
	if err != nil {
		return "", fmt.Errorf("resolve node data directory: %w", err)
	}
	if filepath.Dir(abs) == abs {
		return "", fmt.Errorf("node data directory must not be a filesystem root")
	}
	if cwd, cwdErr := os.Getwd(); cwdErr == nil {
		if cwdAbs, absErr := filepath.Abs(cwd); absErr == nil && strings.EqualFold(filepath.Clean(cwdAbs), abs) {
			return "", fmt.Errorf("node data directory must not be the workspace root")
		}
	}
	info, statErr := os.Lstat(abs)
	if os.IsNotExist(statErr) {
		return abs, nil
	}
	if statErr != nil {
		return "", fmt.Errorf("inspect node data directory: %w", statErr)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("node data directory must not be a symbolic link")
	}
	if !info.IsDir() {
		return "", fmt.Errorf("node data path is not a directory")
	}
	if !resume {
		entries, readErr := os.ReadDir(abs)
		if readErr != nil {
			return "", fmt.Errorf("inspect node data directory contents: %w", readErr)
		}
		if len(entries) != 0 {
			return "", fmt.Errorf("node data directory is not empty; use --resume to reuse it or clear it explicitly outside the node process")
		}
	}
	return abs, nil
}

                                                
func (nl *NodeLauncher) Instance() *NodeInstance {
	return nl.instance
}

                                                           
func (nl *NodeLauncher) IsReady() bool {
	select {
	case <-nl.httpReady:
		return true
	default:
		return false
	}
}

                                                                      
                                                                            
                                                                           
                                                                      
func (nl *NodeLauncher) Start(ctx context.Context, peers []PeerInfo) error {
	nl.mu.Lock()
	if nl.started {
		nl.mu.Unlock()
		return fmt.Errorf("node %d already started", nl.config.ID)
	}
	nl.started = true
	nl.mu.Unlock()

	node := nl.instance
	cfg := nl.config.Config
	if nl.config.RetainGenesisProof {
		if _, err := validateFrostGenesisDeploymentProofMode(nl.config.GenesisPeers, cfg.Frost, nl.config.NetworkChainID, nl.config.FrostPeerManifestDigest, true); err != nil {
			return fmt.Errorf("retained genesis proof: %w", err)
		}
	}

	                                          
	logs.Info("  [Node %d] Phase 1: Initializing core...", node.ID)
	if err := initializeNode(node, cfg, nl.config.GenesisConfig.EffectiveChainID()); err != nil {
		return fmt.Errorf("phase 1 (init core) failed: %v", err)
	}
	protocolHeight, _, err := node.DBManager.GetFinalizedStateHeight()
	if err != nil {
		return fmt.Errorf("phase 1 (protocol finalized height) failed: %w", err)
	}
	protocolState, err := validatePersistedProtocolUpgradeState(node.DBManager, node.NetworkChainID, protocolHeight)
	if err != nil {
		return fmt.Errorf("phase 1 (protocol upgrade state) failed: %w", err)
	}
	if protocolState.Pending != nil {
		logs.Info("  [Node %d] Protocol v%d active; binary upgrade v%d (%s) is scheduled for height %d",
			node.ID, protocolState.Version, protocolState.Pending.Version, protocolState.Pending.ID, protocolState.Pending.ActivationHeight)
	} else {
		logs.Info("  [Node %d] Protocol v%d active; binary supports through v%d", node.ID, protocolState.Version, protocol.CurrentVersion)
	}
	if nl.config.RetainGenesisProof {
		if err := validateRetainedGenesisProofState(node.DBManager, cfg.Frost, protocolHeight, nl.config.ResumeFromExisting, nl.config.Mode); err != nil {
			return fmt.Errorf("phase 1 (retained-genesis database anchor) failed: %w", err)
		}
	} else if _, err := validatePersistedFrostGenesisMarker(node.DBManager, cfg.Frost); err != nil {
		return fmt.Errorf("phase 1 (configured-genesis environment) failed: %w", err)
	}
	lateJoinNeedsVerifiedCatchup, err := configureFrostLateJoinSyncGate(node, nl.config.Mode, cfg.Frost)
	if err != nil {
		return fmt.Errorf("phase 1 (late-join FROST sync gate) failed: %w", err)
	}
	if err := installBootstrapRouting(node, nl.config.BootstrapEndpoints); err != nil {
		return fmt.Errorf("phase 1 (install bootstrap routing) failed: %w", err)
	}

	                         
	time.Sleep(500 * time.Millisecond)

	                     
	shouldBootstrap := true
	if nl.config.ResumeFromExisting {
		if node.ConsensusManager != nil {
			_, height := node.ConsensusManager.GetLastAccepted()
			if height > 0 {
				shouldBootstrap = false
				logs.Info("  [Node %d] Phase 2: Reusing existing state (height=%d)", node.ID, height)
				logs.RegisterNodeMapping(fmt.Sprintf("%d", node.ID), node.Address)
				logs.RegisterNodeMapping(node.Port, node.Address)
				logs.RegisterNodeMapping(nodeAdvertiseAddr(node), node.Address)
				logs.RegisterNodeMapping(node.Address, node.Address)
			}
		}
	}

	if shouldBootstrap {
		switch nl.config.Mode {
		case GenesisBootstrap:
			logs.Info("  [Node %d] Phase 2: Genesis bootstrap (%d peers)...", node.ID, len(peers))
			if err := registerPeersLocally(node, peers, nl.config.GenesisConfig, nl.config.FrostConfig); err != nil {
				return fmt.Errorf("phase 2 (genesis bootstrap) failed: %v", err)
			}
		case LateJoin:
			                                                                        
			                                                                        
			                                                                       
			                                                                   
			logs.Info("  [Node %d] Phase 2: Reconstructing trusted genesis state before late-join sync...", node.ID)
			if err := seedTrustedGenesisState(node, peers, nl.config.GenesisConfig, nl.config.FrostConfig, false); err != nil {
				return fmt.Errorf("phase 2 (late-join genesis state) failed: %w", err)
			}
		}
	}

	                                                
	logs.Info("  [Node %d] Phase 2.5: Bootstrapping liveness & cert chain...", node.ID)
	if err := postBootstrapInitCertChain(
		node,
		cfg,
		nl.config.Mode,
		shouldBootstrap,
		nl.config.GenesisPeers,
		nl.config.GenesisConfig,
	); err != nil {
		return fmt.Errorf("phase 2.5 (liveness/cert bootstrap) failed: %w", err)
	}
	if node.DBManager != nil {
		if err := node.DBManager.ForceFlush(); err != nil {
			logs.Warn("Node %d: phase2.5 ForceFlush failed: %v", node.ID, err)
		}
	}

	                                                                      
	                                                                            
	                                                                          
	                                                                       
	                                                                         
	                                                                           
	                                                                           
	                      
	var frostManager *frostrt.Manager
	var frostShareStore frostGenesisShareStore
	if cfg.Frost.Enabled {
		fm, store, err := prepareFrostRuntime(node, cfg, preflightFrostSharesBeforeNetworking(nl.config.Mode))
		if err != nil {
			return fmt.Errorf("phase 2.6 (FROST unlock) failed: %w", err)
		}
		frostManager = fm
		frostShareStore = store
	}

	                                            
	logs.Info("  [Node %d] Phase 3: Starting HTTP/3 server on port %s...", node.ID, node.Port)
	serverReadyChan := make(chan int, 1)
	serverErrorChan := make(chan error, 1)
	go func() {
		if err := startHTTPServerWithSignal(node, serverReadyChan, serverErrorChan); err != nil {
			serverErrorChan <- err
		}
	}()

	                                 
	select {
	case <-serverReadyChan:
		nl.listenersBound = true
		logs.Info("  [Node %d] Phase 3: HTTP/3 server ready", node.ID)
		close(nl.httpReady)
	case err := <-serverErrorChan:
		return fmt.Errorf("phase 3 (HTTP server) failed: %v", err)
	case <-time.After(30 * time.Second):
		close(nl.httpReady)                                                  
		logs.Info("  [Node %d] Phase 3: HTTP server timeout, continuing...", node.ID)
	case <-ctx.Done():
		return ctx.Err()
	}

	                                  
	if node.SenderManager != nil {
		                                                                         
		                                                                       
		bootstrapPeers := bootstrapEndpointStrings(nl.config.BootstrapEndpoints)
		cfg.Discovery.BootstrapPeers = append([]string(nil), bootstrapPeers...)
		node.SenderManager.StartMinerIPDiscovery()
		                                                                      
		                                                                    
		node.SenderManager.StartServicePeerDiscovery()
	}

	                                                                       
	                                                                           
	                                                                         
	                                                       
	if node.ConsensusManager != nil {
		node.ConsensusManager.Start()
		logs.Info("  [Node %d] Phase 3.5: Network/sync engine started (consensus participation is chain-gated)", node.ID)
	}

	                                                                            
	                                                                             
	                                                                          
	                       
	if frostManager != nil {
		frostStartCtx, cancelFrostStart := context.WithCancel(ctx)
		nl.mu.Lock()
		nl.frostStartCancel = cancelFrostStart
		nl.frostStartDone = startFrostRuntimeAsyncAfterCatchup(
			frostStartCtx, node, frostManager, cfg, frostShareStore,
			lateJoinNeedsVerifiedCatchup, nl.config.Mode == LateJoin,
		)
		nl.mu.Unlock()
	}

	                            
	if node.ConsensusManager != nil {
		node.ConsensusManager.ResetProposalTimer()
		go func() {
			cleanupLogger := logs.SetThreadNodeContext(node.Address)
			defer cleanupLogger()
			time.Sleep(time.Duration(node.ID*100) * time.Millisecond)                   
			node.ConsensusManager.StartQuery()
		}()
		logs.Info("  [Node %d] Phase 4: Query loop started", node.ID)
	}

	logs.Info("  ✅ Node %d fully started (address=%s)", node.ID, node.Address)
	return nil
}

                                                
func (nl *NodeLauncher) Stop(ctx context.Context) error {
	nl.stopOnce.Do(func() { nl.stopErr = nl.stop(ctx) })
	return nl.stopErr
}

func (nl *NodeLauncher) stop(ctx context.Context) error {
	nl.mu.Lock()
	nl.stopped = true
	frostStartCancel := nl.frostStartCancel
	frostStartDone := nl.frostStartDone
	nl.mu.Unlock()

	node := nl.instance
	if node == nil {
		return nil
	}

	node.Logger.Info("Stopping node %d...", node.ID)
	if frostStartCancel != nil {
		frostStartCancel()
	}
	if frostStartDone != nil {
		select {
		case <-frostStartDone:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if node.Emergency != nil {
		node.Emergency.Stop("node shutdown")
	}

	                        
	if node.FrostRuntime != nil {
		node.FrostRuntime.Stop()
	}

	                           
	if node.ConsensusManager != nil {
		node.ConsensusManager.Stop()
	}

	                 
	if node.TxPool != nil {
		node.TxPool.Stop()
	}

	                                                                             
	if node.EventsHub != nil {
		node.EventsHub.closeAll()
	}

	                        
	if node.HTTP3Server != nil {
		if err := node.HTTP3Server.Close(); err != nil && !isServerClosedErr(err) {
			node.Logger.Warn("Node %d: failed to close HTTP/3 server: %v", node.ID, err)
		}
	}
	if node.Server != nil {
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if err := node.Server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			node.Logger.Warn("Node %d: failed to shutdown TCP server: %v", node.ID, err)
		}
		cancel()
	}
	if node.PProfServer != nil {
		shutdownCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		if err := node.PProfServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			node.Logger.Warn("Node %d: failed to shutdown pprof server: %v", node.ID, err)
		}
		cancel()
	}
	if node.HandlerManager != nil {
		node.HandlerManager.StopPeerGate()
	}

	                         
	if node.SenderManager != nil {
		node.SenderManager.Stop()
	}

	                           
	if node.DBManager != nil {
		if err := node.DBManager.CloseWithError(); err != nil {
			return fmt.Errorf("close node database: %w", err)
		}
	}

	node.Logger.Info("Node %d stopped.", node.ID)
	return nil
}
