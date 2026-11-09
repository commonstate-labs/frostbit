package main

import (
	"context"
	"dex/config"
	"dex/frost/ceremony"
	"dex/logs"
	"dex/pb"
	"dex/updater"
	"dex/utils"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var (
	                                                                                         
	                                                            
	resumeFromExistingProgress = true
	                                                                       
	enableQueueStatsMonitor = os.Getenv("DEX_MONITOR_QUEUE_STATS") == "1"
	                                                
	globalSimulator *TxSimulator
)

                                                                                                                  
var (
	BuildCommit  = "unknown"
	BuildTime    = "unknown"
	BuildTag     = "dev"
	BuildVersion = "dev"
)

func main() {
	if err := runMain(); err != nil {
		if errors.Is(err, errNodeUpdateRestart) {
			os.Exit(nodeUpdateExitCode)
		}
		logs.Error("Node startup failed: %v", err)
		os.Exit(1)
	}
}

func runMain() error {
	opts := parseRuntimeOptions()
	if opts.UpgradeInfo {
		return writeUpgradeInfo(os.Stdout, config.ResolveConfigDir(opts.Env, opts.ConfigDir))
	}
	if raw := os.Getenv("DEX_RETAIN_GENESIS_PROOF"); raw != "" && raw != "0" && raw != "1" {
		return fmt.Errorf("DEX_RETAIN_GENESIS_PROOF must be 0 or 1")
	}
	if opts.RetainGenesisProof && (!opts.Resume || !strings.EqualFold(strings.TrimSpace(opts.BootstrapMode), "genesis")) {
		return fmt.Errorf("retain-genesis-proof requires --resume=true and --bootstrap-mode=genesis")
	}
	if opts.CheckConfig {
		return checkConfigDirectoryWithRetainedProof(config.ResolveConfigDir(opts.Env, opts.ConfigDir), opts.RetainGenesisProof)
	}
	if err := validateRuntimeSecretOptions(&opts); err != nil {
		return fmt.Errorf("runtime secret options: %w", err)
	}
	                                                                          
	                                                                               
	autoUpdateConfig, autoUpdateError := nodeUpdateConfig(opts)
	if autoUpdateError == nil && autoUpdateConfig != nil {
		autoUpdateError = updater.Bootstrap(context.Background(), *autoUpdateConfig, updater.Dependencies{})
	}
	if errors.Is(autoUpdateError, updater.ErrUnsafeBootstrap) {
		return autoUpdateError
	}
	logPath := strings.TrimSpace(os.Getenv("DEX_LOG_FILE"))
	if logPath == "" {
		logPath = "log.txt"
	}
	if err := logs.EnableFileOutput(logPath); err != nil {
		logs.Warn("Failed to enable log file output: %v", err)
	}
	logs.Info("node starting version=%s commit=%s time=%s tag=%s", BuildVersion, BuildCommit, BuildTime, BuildTag)
	if autoUpdateError != nil {
		logs.Warn("Automatic update bootstrap unavailable; starting installed node: %v", autoUpdateError)
		autoUpdateConfig = nil
	}

	if err := validateSimulatorRateBlockSeconds(opts.SimulatorRateBlockSeconds); err != nil {
		return err
	}
	if err := validateSimulatorTargetTxsPerBlock(opts.SimulatorTargetTxsPerBlock); err != nil {
		return err
	}
	if opts.KeystorePath != "" {
		if err := hardenRuntimeSecretMemory(); err != nil {
			return fmt.Errorf("protect process before loading secrets: %w", err)
		}
	}
	if err := loadRuntimeSecrets(&opts); err != nil {
		return fmt.Errorf("load runtime secrets: %w", err)
	}
	importCleanup := opts.keystoreImportCleanup
	opts.keystoreImportCleanup = nil
	defer importCleanup.close()
	defer func() {
		opts.KeyManager = nil
		opts.ArbitrageKeyManager = nil
	}()
	defer wipeBytes(opts.FrostWrapKey)

	configDir := config.ResolveConfigDir(opts.Env, opts.ConfigDir)
	cfg, err := config.LoadFromDir(configDir)
	if err != nil {
		return fmt.Errorf("load config from %s: %w", configDir, err)
	}
	applyArbitrageRuntimeOverride(cfg, opts.EnableArbitrage)
	                                                                       
	                                                                   
	cfg.Database.FrostArchiveNode = opts.FrostArchiveNode
	if archiveDir := strings.TrimSpace(opts.ArchiveDBDir); archiveDir != "" {
		cfg.Database.ArchiveDBDir = archiveDir
	}
	if opts.EnableDebugAPI {
		cfg.Server.EnableDebugAPI = true
	}
	if opts.EnablePprof {
		cfg.Server.EnablePprof = true
	}
	if bind := strings.TrimSpace(opts.PprofBind); bind != "" {
		cfg.Server.PprofBind = bind
	}
	if strings.TrimSpace(cfg.Server.PprofBind) == "" {
		cfg.Server.PprofBind = "127.0.0.1"
	}
	if opts.BasePort > 0 {
		cfg.Network.BasePort = opts.BasePort
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("validate config after runtime overrides: %w", err)
	}
	if err := validateArbitrageRuntimeSecret(cfg.Arbitrage.Enabled, opts.ArbitrageKeyManager, opts.ExpectedArbitrageAddress); err != nil {
		return err
	}
	if cfg.Server.EnableDebugAPI || opts.EnableOperatorControlAPI {
		if err := config.ValidateExplorerServiceToken(os.Getenv(config.ExplorerServiceTokenEnv)); err != nil {
			return fmt.Errorf("diagnostics/operator service token: %w", err)
		}
	}
	if cfg.Server.EnableDebugAPI {
		logs.Warn("sensitive diagnostic HTTP API is enabled behind Explorer service-token authentication")
	}
	if opts.EnableOperatorControlAPI {
		logs.Warn("scoped operator control API is enabled behind Explorer service-token authentication")
	}
	if cfg.Server.EnablePprof {
		logs.Warn("pprof is enabled on host %s", cfg.Server.PprofBind)
	}
	if opts.FrostWrapKeyFD >= 0 && !cfg.Frost.Enabled {
		return fmt.Errorf("--frost-wrap-key-fd was provided but FROST is disabled")
	}
	if opts.FrostPlaintextGenesisShares {
		if !cfg.Frost.Enabled || strings.TrimSpace(opts.FrostGenesisSharesDir) == "" {
			return fmt.Errorf("--frost-plaintext-genesis-shares requires enabled FROST and --frost-genesis-shares-dir")
		}
		if opts.FrostWrapKeyFD != -1 || len(opts.FrostWrapKey) != 0 {
			return fmt.Errorf("--frost-plaintext-genesis-shares conflicts with --frost-wrap-key-fd")
		}
	}
	if sharesDir := strings.TrimSpace(opts.FrostGenesisSharesDir); sharesDir != "" {
		if !cfg.Frost.Enabled {
			return fmt.Errorf("--frost-genesis-shares-dir was provided but FROST is disabled")
		}
		if !opts.FrostPlaintextGenesisShares && len(opts.FrostWrapKey) != frostWrapKeyBytes {
			return fmt.Errorf("--frost-genesis-shares-dir requires --frost-wrap-key-fd or --frost-plaintext-genesis-shares")
		}
	}
	logs.Info("Consensus config K=%d alpha_preference=%d alpha_confidence=%d beta=%d poll_interval=%s query_timeout=%s max_concurrent=%d proposal_interval=%s",
		cfg.Consensus.K, cfg.Consensus.AlphaPreference, cfg.Consensus.AlphaConfidence, cfg.Consensus.Beta, cfg.Consensus.PollInterval, cfg.Consensus.QueryTimeout,
		cfg.Consensus.MaxConcurrentQueries, cfg.Consensus.ProposalInterval)
	basePort := cfg.Network.BasePort

	                             
	genesisPath := config.ConfigPath(configDir, "genesis.json")
	genesisCfg, err := config.LoadGenesisConfig(genesisPath)
	if err != nil {
		logs.Warn("Failed to load genesis config: %v, using defaults", err)
		genesisCfg = config.DefaultGenesisConfig()
	} else {
		logs.Info("Loaded genesis config from %s", genesisPath)
	}
	                                                                                    
	                                                                         
	pb.SetDefaultChainID(genesisCfg.EffectiveChainID())
	logs.Info("🔗 ChainID = %d", genesisCfg.EffectiveChainID())
	if opts.FrostPlaintextGenesisShares && genesisCfg.EffectiveChainID() != ceremony.PlaintextGenesisChainID {
		return fmt.Errorf("--frost-plaintext-genesis-shares is permitted only for DEX chain 51025")
	}

	peersPath := resolvePeersPath(configDir, opts.PeersPath)
	peerManifest, peers, err := loadPeerManifestInfo(peersPath)
	if err != nil {
		return fmt.Errorf("load peers from %s: %w", peersPath, err)
	}
	if err := validatePeerManifestChainID(peerManifest, genesisCfg.EffectiveChainID()); err != nil {
		return err
	}
	if len(peers) == 0 {
		return fmt.Errorf("public peer manifest %s is empty", peersPath)
	}
	activeMinerCount := countMinerPeers(peers)
	if activeMinerCount <= 0 {
		return fmt.Errorf("public peer manifest %s contains no genesis validators", peersPath)
	}
	if err := validateFrostStartupMinerSet(peers, cfg.Frost); err != nil {
		return fmt.Errorf("FROST startup validation failed: %w", err)
	}
	if err := config.ValidateFreshFrostGenesisStakePolicy(cfg.Frost, peerManifest, genesisCfg); err != nil {
		return fmt.Errorf("FROST genesis ranking validation failed: %w", err)
	}
	peerManifestDigest, err := ceremony.ComputePeerManifestDigest(peerManifest)
	if err != nil {
		return fmt.Errorf("bind FROST ceremony to peer manifest: %w", err)
	}
	if _, err := validateFrostGenesisDeploymentProofMode(peers, cfg.Frost, genesisCfg.EffectiveChainID(), peerManifestDigest, opts.RetainGenesisProof); err != nil {
		return fmt.Errorf("FROST genesis deployment proof validation failed: %w", err)
	}

	                                                                      
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	launcher, err := startSingleNode(ctx, cfg, genesisCfg, opts, peers, peerManifest.BootstrapEndpoints, peerManifestDigest, activeMinerCount, basePort)
	opts.KeyManager = nil
	opts.ArbitrageKeyManager = nil
	if err != nil {
		return fmt.Errorf("start node: %w", err)
	}

	nodes := []*NodeInstance{launcher.Instance()}
	autoRegisterWitness := shouldAutoRegisterWitness(opts, nodes[0].Address, peers)
	if shouldStartSimulator(opts) || autoRegisterWitness {
		simNodes := buildSingleModeSimulatorNodes(nodes)
		sim := NewTxSimulator(simNodes, cfg.Frost, cfg.Witness, genesisCfg)
		if shouldStartSimulator(opts) {
			globalSimulator = sim
			configureSimulatorPartition(sim, simNodes, opts)
			configureSimulatorRate(sim, opts)
			configureSingleModeSimulator(sim, opts)
			sim.disableWitnessReg.Store(!autoRegisterWitness)
			sim.Start()
		} else {
			                                                                                    
			go sim.runWitnessRegistration(ctx)
		}
	}
	time.Sleep(2 * time.Second)

	                              
	go monitorMetrics(nodes)
	if enableQueueStatsMonitor {
		go monitorQueueStats(nodes)
	}
	go monitorProgress(nodes)
	go monitorMinerParticipantsByEpoch(nodes)

	                  
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	defer signal.Stop(sigChan)
	updates := startNodeUpdater(ctx, autoUpdateConfig, launcher)
	var importCleanupReady <-chan struct{}
	var importCleanupCtx context.Context
	var stopImportCleanup context.CancelFunc
	if importCleanup != nil {
		if autoUpdateError != nil {
			logs.Warn("Skipping plaintext import source removal because startup update state could not be verified")
			importCleanup.close()
		} else {
			importCleanupCtx, stopImportCleanup = signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
			defer stopImportCleanup()
			importCleanupReady = updates.startupConfirmed
		}
	}

	logs.Info("\n✅ Node started! Press Ctrl+C to stop...")
	logs.Info("📊 Monitoring consensus progress...")

	var prepared *updater.Prepared
	var restartForUpdate bool
	var updateFailure error
waitForExit:
	for {
		select {
		case sig := <-sigChan:
			logs.Info("\n🛑 Received signal %s, shutting down node...", sig)
			break waitForExit
		case <-importCleanupReady:
			importCleanupReady = nil
			if len(sigChan) > 0 {
				stopImportCleanup()
			}
			finishKeystoreImportCleanup(importCleanupCtx, launcher, importCleanup, opts.KeystoreImportFrom)
			stopImportCleanup()
		case next := <-updates.ready:
			prepared = &next
			restartForUpdate = nodeUpdateCanRestart(opts, managedNodeUpdateRestart())
			if restartForUpdate {
				logs.Info("Verified GitHub release %s prepared; shutting down for managed restart", next.Version)
				break waitForExit
			}
			logs.Info("Verified GitHub release %s prepared; it will be activated after the next normal shutdown (manual keystore unlock or no automatic restart policy)", next.Version)
		case updateFailure = <-updates.errors:
			break waitForExit
		}
	}

	                    
	cancel()
	<-updates.done
	shutdownCtx := context.Background()
	if err := stopAndActivateNodeUpdate(shutdownCtx, launcher, autoUpdateConfig, prepared); err != nil {
		return err
	}
	if prepared != nil && autoUpdateConfig != nil {
		if restartForUpdate {
			return errNodeUpdateRestart
		}
	}

	logs.Info("👋 Node stopped. Goodbye!")
	return updateFailure
}

func validatePeerManifestChainID(manifest *config.PeerManifest, expected uint64) error {
	if manifest == nil {
		return fmt.Errorf("public peer manifest is nil")
	}
	if manifest.ChainID != expected {
		return fmt.Errorf("peer manifest chain_id=%d does not match genesis chain_id=%d", manifest.ChainID, expected)
	}
	return nil
}

func shouldStartSimulator(opts runtimeOptions) bool {
	return !opts.NoSimulator && opts.SimulatorInSingle
}

func shouldAutoRegisterWitness(opts runtimeOptions, address string, peers []PeerInfo) bool {
	peer, listed := findPeerByAddress(peers, address)
	return opts.AutoRegisterWitness && listed && peer.IsMiner
}

func configureSimulatorPartition(sim *TxSimulator, simNodes []*NodeInstance, opts runtimeOptions) {
	if sim == nil || opts.SimulatorPartitions <= 1 {
		return
	}
	partitionID := 0
	if len(simNodes) == 1 && simNodes[0] != nil {
		partitionID = simNodes[0].ID
	}
	sim.setTargetPartition(partitionID, opts.SimulatorPartitions)
}

func configureSimulatorRate(sim *TxSimulator, opts runtimeOptions) {
	if sim == nil {
		return
	}
	sim.setRateBlockSeconds(int64(opts.SimulatorRateBlockSeconds))
}

func buildSingleModeSimulatorNodes(nodes []*NodeInstance) []*NodeInstance {
	if len(nodes) == 0 || nodes[0] == nil {
		return nodes
	}
	local := nodes[0]
	logs.Info("Single-mode simulator using local node account id=%d address=%s", local.ID, local.Address)
	return nodes
}

func configureSingleModeSimulator(sim *TxSimulator, opts runtimeOptions) {
	if sim == nil {
		return
	}
	if sim.targetPartitions.Load() <= 0 {
		sim.setTargetPartition(0, 1)
	}
	sim.setTargetTxsPerBlock(int64(opts.SimulatorTargetTxsPerBlock))
	sim.disableTransfers.Store(!opts.SimulatorTransfers)
	sim.disableWitnessReg.Store(false)
	sim.disableRecharge.Store(true)
	sim.disableWitnessVote.Store(true)
	sim.disableWithdraw.Store(true)
	sim.disableOrders.Store(false)
	sim.disableFutureOrders.Store(false)
	sim.disableMassOrders.Store(true)
	sim.disableArbitration.Store(true)
	logs.Info("Single-mode simulator profile: mixed spot/futures stress, global_target_txs_per_block=%d local_target_txs_per_block=%d rate_block_seconds=%d global_target_txs_per_second=%d local_target_txs_per_second=%d partition=%d/%d spot_share=%d%% future_share=%d%%", sim.targetTxsPerBlock.Load(), sim.effectiveTargetTxsPerBlock(), sim.rateBlockSecondsValue(), sim.targetTxsPerSecond(), sim.localTargetTxsPerSecond(), sim.targetPartitionID.Load(), sim.targetPartitions.Load(), simSpotTargetSharePermille/10, simFutureTargetSharePermille/10)
}

func startSingleNode(ctx context.Context, cfg *config.Config, genesisCfg *config.GenesisConfig, opts runtimeOptions, peers []PeerInfo, manifestEndpoints []config.BootstrapEndpointConfig, peerManifestDigest []byte, activeMinerCount, basePort int) (*NodeLauncher, error) {
	localIdentity, err := deriveLocalPeerIdentity(opts.KeyManager)
	if err != nil {
		return nil, err
	}
	if err := validateExpectedAddress(opts.ExpectedAddress, localIdentity.Address); err != nil {
		return nil, err
	}
	var arbitrageAddress string
	if cfg != nil && cfg.Arbitrage.Enabled {
		var arbitrageIdentity PeerInfo
		arbitrageIdentity, err = deriveLocalPeerIdentity(opts.ArbitrageKeyManager)
		if err != nil {
			return nil, fmt.Errorf("derive arbitrage account identity: %w", err)
		}
		arbitrageAddress = arbitrageIdentity.Address
		if err := validateExpectedArbitrageAddress(opts.ExpectedArbitrageAddress, arbitrageAddress); err != nil {
			return nil, fmt.Errorf("arbitrage account: %w", err)
		}
		if err := validateIndependentArbitrageAddress(localIdentity.Address, arbitrageAddress); err != nil {
			return nil, fmt.Errorf("arbitrage account: %w", err)
		}
	}

	mode := GenesisBootstrap
	switch strings.ToLower(strings.TrimSpace(opts.BootstrapMode)) {
	case "", "genesis":
	case "latejoin":
		mode = LateJoin
	default:
		return nil, fmt.Errorf("unsupported bootstrap mode %q (want genesis or latejoin)", opts.BootstrapMode)
	}

	localPeer, listed := findPeerByAddress(peers, localIdentity.Address)
	if listed {
		if err := validateLocalPeerIdentity(localPeer, localIdentity); err != nil {
			return nil, err
		}
	} else {
		if mode == GenesisBootstrap {
			return nil, fmt.Errorf("genesis node address %s is absent from the public peer manifest", localIdentity.Address)
		}
		localPeer = localIdentity
		                                                                      
		                                                                         
		localPeer.Index = len(peers)
		localPeer.IsMiner = false
	}

	if opts.BasePort > 0 {
		localPeer.Port = strconv.Itoa(opts.BasePort)
	} else if localPeer.Port == "" {
		localPeer.Port = strconv.Itoa(basePort)
	}
	if raw := strings.TrimSpace(opts.AdvertiseAddr); raw != "" {
		advertiseAddr := normalizeAdvertiseAddr(raw, localPeer.Port)
		if listed && advertiseAddr != localPeer.AdvertiseAddr {
			return nil, fmt.Errorf("--advertise-addr %s conflicts with manifest endpoint %s for %s", advertiseAddr, localPeer.AdvertiseAddr, localPeer.Address)
		}
		localPeer.AdvertiseAddr = advertiseAddr
	}
	if localPeer.AdvertiseAddr == "" {
		localPeer.AdvertiseAddr = advertiseAddrFromHost(cfg.Network.AdvertiseHost, basePort)
	}
	if _, _, err := net.SplitHostPort(localPeer.AdvertiseAddr); err != nil {
		return nil, fmt.Errorf("local advertise address %q must be host:port: %w", localPeer.AdvertiseAddr, err)
	}
	if mode == GenesisBootstrap {
		for _, peer := range peers {
			if strings.TrimSpace(peer.AdvertiseAddr) == "" {
				return nil, fmt.Errorf("genesis validator %s has no bootstrap endpoint in the public peer manifest", peer.Address)
			}
		}
	}

	bootstrapEndpoints := buildBootstrapEndpoints(manifestEndpoints, localIdentity.Address)
	if mode == LateJoin && len(bootstrapEndpoints) == 0 {
		return nil, fmt.Errorf("latejoin requires at least one manifest bootstrap endpoint other than the local address")
	}
	launcher, err := NewNodeLauncher(NodeConfig{
		ID:                          localPeer.Index,
		KeyManager:                  opts.KeyManager,
		ArbitrageKeyManager:         opts.ArbitrageKeyManager,
		ArbitrageAddress:            arbitrageAddress,
		Port:                        localPeer.Port,
		ListenAddr:                  opts.ListenAddr,
		AdvertiseAddr:               localPeer.AdvertiseAddr,
		DataPath:                    nodeDataPath(opts.DataDir, localIdentity.Address),
		BootstrapEndpoints:          bootstrapEndpoints,
		Mode:                        mode,
		GenesisPeers:                peers,
		Config:                      cfg,
		GenesisConfig:               genesisCfg,
		FrostConfig:                 cfg.Frost,
		FrostWrapKey:                opts.FrostWrapKey,
		FrostGenesisSharesDir:       opts.FrostGenesisSharesDir,
		FrostPlaintextGenesisShares: opts.FrostPlaintextGenesisShares,
		NetworkChainID:              genesisCfg.EffectiveChainID(),
		FrostPeerManifestDigest:     peerManifestDigest,
		ActiveMinerCount:            activeMinerCount,
		ResumeFromExisting:          opts.Resume,
		RetainGenesisProof:          opts.RetainGenesisProof,
		EnableOperatorControlAPI:    opts.EnableOperatorControlAPI,
	})
	if err != nil {
		return nil, err
	}
	if err := launcher.Start(ctx, peers); err != nil {
		return nil, err
	}
	logs.Info("Node started: address=%s listen_addr=%s advertise_addr=%s mode=%v", launcher.Instance().Address, launcher.Instance().ListenAddr, nodeAdvertiseAddr(launcher.Instance()), mode)
	return launcher, nil
}

func validateArbitrageRuntimeSecret(enabled bool, identity *utils.KeyManager, expectedAddress string) error {
	expectedAddress = strings.TrimSpace(expectedAddress)
	if enabled {
		if identity == nil || !identity.HasPrivateKey() {
			return fmt.Errorf("arbitrage is enabled but no private key was supplied; use --arbitrage-private-key-fd (or --windows-arbitrage-secret-dir on Windows)")
		}
		return nil
	}
	if identity != nil || expectedAddress != "" {
		return fmt.Errorf("arbitrage private key/address was supplied but arbitrage is disabled")
	}
	return nil
}

                                                                             
                                                                               
                                                                           
                                                
func applyArbitrageRuntimeOverride(cfg *config.Config, enable bool) {
	if cfg != nil && enable {
		cfg.Arbitrage.Enabled = true
	}
}

func countMinerPeers(peers []PeerInfo) int {
	n := 0
	for _, p := range peers {
		if p.IsMiner {
			n++
		}
	}
	return n
}

func findPeerByAddress(peers []PeerInfo, address string) (PeerInfo, bool) {
	for _, p := range peers {
		if p.Address == address {
			return p, true
		}
	}
	return PeerInfo{}, false
}

func mustAtoi(raw string, fallback int) int {
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func buildBootstrapEndpoints(routes []config.BootstrapEndpointConfig, localAddress string) []BootstrapEndpoint {
	endpoints := make([]BootstrapEndpoint, 0, len(routes))
	for _, route := range routes {
		address := strings.TrimSpace(route.Address)
		endpoint := strings.TrimSpace(route.Endpoint)
		if address == localAddress {
			continue
		}
		endpoints = append(endpoints, BootstrapEndpoint{
			Address:  address,
			Endpoint: endpoint,
		})
	}
	return endpoints
}

func bootstrapEndpointStrings(endpoints []BootstrapEndpoint) []string {
	out := make([]string, 0, len(endpoints))
	for _, endpoint := range endpoints {
		if strings.TrimSpace(endpoint.Address) == "" || strings.TrimSpace(endpoint.Endpoint) == "" {
			continue
		}
		out = append(out, endpoint.Endpoint)
	}
	return out
}

                                                                                
func detectPersistedProgress(nodes []*NodeInstance) (bool, uint64) {
	var maxHeight uint64
	for _, node := range nodes {
		if node == nil || node.ConsensusManager == nil {
			continue
		}
		_, height := node.ConsensusManager.GetLastAccepted()
		if height > maxHeight {
			maxHeight = height
		}
	}
	return maxHeight > 0, maxHeight
}

func registerNodeLogMappings(nodes []*NodeInstance) {
	for _, node := range nodes {
		if node == nil {
			continue
		}
		logs.RegisterNodeMapping(strconv.Itoa(node.ID), node.Address)
		logs.RegisterNodeMapping(node.Port, node.Address)
		logs.RegisterNodeMapping(nodeAdvertiseAddr(node), node.Address)
		logs.RegisterNodeMapping(node.Address, node.Address)
	}
}
