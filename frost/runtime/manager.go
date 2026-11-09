package runtime

import (
	"context"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	bridgeasset "dex/frost/asset"
	"dex/frost/chain"
	"dex/frost/guardian"
	"dex/frost/runtime/planning"
	"dex/frost/runtime/roast"
	"dex/frost/runtime/services"
	"dex/frost/runtime/session"
	"dex/frost/runtime/types"
	"dex/frost/runtime/workers"
	"dex/keys"
	"dex/lifecycle"
	"dex/logs"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

                                                                               
                                                                               
                                                                                  

                                                                                                    

                                
type ManagerConfig struct {
	                      
	NodeID NodeID

	               
	ScanInterval time.Duration

	                                                       
	MaxInFlightPerChainAsset int

	                        
	SupportedChains []ChainAssetPair

	                                                                                  
	AssetRefreshInterval time.Duration
}

                              
type ChainAssetPair struct {
	Chain string
	Asset string
}

                                    
func DefaultManagerConfig(nodeID NodeID) ManagerConfig {
	return ManagerConfig{
		NodeID:                   nodeID,
		ScanInterval:             30 * time.Second,
		MaxInFlightPerChainAsset: 1,
		AssetRefreshInterval:     30 * time.Second,
		SupportedChains:          nil,
	}
}

                                                 
type Manager struct {
	config ManagerConfig

	               
	stateReader    ChainStateReader
	txSubmitter    TxSubmitter
	notifier       FinalityNotifier
	p2p            P2P
	signerProvider SignerSetProvider
	vaultProvider  VaultCommitteeProvider
	adapterFactory chain.ChainAdapterFactory
	Logger         logs.Logger

	            
	scanner          *planning.Scanner
	withdrawWorker   *workers.WithdrawWorker
	transitionWorker *workers.TransitionWorker
	coordinator      *roast.Coordinator
	participant      *roast.Participant
	roastDispatcher  *roast.Dispatcher
	sessionStore     *session.SessionStore

	         
	running  atomic.Bool
	stopOnce sync.Once
	stopCh   chan struct{}
	wg       sync.WaitGroup

	                          
	finalizedMu         sync.Mutex
	lastFinalizedHeight atomic.Uint64

	assetMu           sync.RWMutex
	assetPairs        []ChainAssetPair
	lastAssetRefresh  time.Time
	assetRefreshEvery time.Duration

	                                      
	finalizedCh chan uint64

	                           
	onBlockFinalized func(height uint64)

	                                                                   
	                                                                     
	emergency *lifecycle.EmergencyStop
}

                                                                             
                                                   
func (m *Manager) SetEmergencyStop(e *lifecycle.EmergencyStop) {
	if m == nil {
		return
	}
	m.emergency = e
	if m.withdrawWorker != nil {
		m.withdrawWorker.SetEmergencyStop(e)
	}
	if m.transitionWorker != nil {
		m.transitionWorker.SetEmergencyStop(e)
	}
}

                                   
type ManagerDeps struct {
	GuardianClient  guardian.Client
	StateReader     ChainStateReader
	TxSubmitter     TxSubmitter
	Notifier        FinalityNotifier
	P2P             P2P
	RoastMessenger  RoastMessenger
	SignerProvider  SignerSetProvider
	VaultProvider   VaultCommitteeProvider
	AdapterFactory  chain.ChainAdapterFactory
	PubKeyProvider  MinerPubKeyProvider
	CryptoFactory   CryptoExecutorFactory
	LocalShareStore LocalShareStore
	NonceStore      session.NonceStore
	LocalIdentity   types.LocalIdentity
	LogReporter     LogReporter
	Logger          logs.Logger
}

                                                                           
                                                                              
                                                                         
type unavailableNonceStore struct{}

func (unavailableNonceStore) SaveNonce([]byte, []byte, uint64) error {
	return session.ErrNonceAlreadyBound
}

func (unavailableNonceStore) GetNonceBind([]byte) ([]byte, uint64, bool) {
	return nil, 0, true
}

                                         
func NewManager(config ManagerConfig, deps ManagerDeps) *Manager {
	nonceStore := deps.NonceStore
	if nonceStore == nil {
		nonceStore = unavailableNonceStore{}
	}
	sessionStore := session.NewSessionStore(nonceStore)

	m := &Manager{
		config:         config,
		stateReader:    deps.StateReader,
		txSubmitter:    deps.TxSubmitter,
		notifier:       deps.Notifier,
		p2p:            deps.P2P,
		signerProvider: deps.SignerProvider,
		vaultProvider:  deps.VaultProvider,
		adapterFactory: deps.AdapterFactory,
		sessionStore:   sessionStore,
		stopCh:         make(chan struct{}),
		finalizedCh:    make(chan uint64, 100),
		Logger:         deps.Logger,
	}
	m.assetPairs = normalizeChainAssetPairs(config.SupportedChains)
	m.assetRefreshEvery = config.AssetRefreshInterval
	if m.assetRefreshEvery <= 0 {
		m.assetRefreshEvery = 30 * time.Second
	}

	var roastMessenger types.RoastMessenger
	if deps.RoastMessenger != nil {
		                                                                            
		roastMessenger = &roastMessengerAdapter{messenger: deps.RoastMessenger}
	} else if deps.P2P != nil {
		                                                                                 
		roastMessenger = &roastMessengerAdapter{p2p: deps.P2P}
	}
	                                                                                     
	m.scanner = planning.NewScanner(deps.StateReader)

	m.coordinator = roast.NewCoordinator(config.NodeID, roastMessenger, deps.VaultProvider, deps.CryptoFactory, deps.Logger)
	m.participant = roast.NewParticipant(config.NodeID, roastMessenger, deps.VaultProvider, deps.CryptoFactory, sessionStore, deps.LocalShareStore, deps.Logger)
	m.participant.SetSigningRequestValidator(newSigningRequestValidator(deps.StateReader))

	operatorSigningService := services.NewRoastSigningService(m.coordinator, m.participant, deps.VaultProvider)
	roastSigningService := services.NewDualCustodySigningService(operatorSigningService, deps.StateReader, deps.GuardianClient)

	maxInFlight := config.MaxInFlightPerChainAsset
	if maxInFlight <= 0 {
		maxInFlight = 1
	}
	m.withdrawWorker = workers.NewWithdrawWorker(
		deps.StateReader,
		deps.AdapterFactory,
		deps.TxSubmitter,
		deps.LogReporter,
		roastSigningService,
		deps.VaultProvider,
		maxInFlight,
		string(config.NodeID),
		deps.LocalIdentity,
		deps.Logger,
	)

	m.transitionWorker = workers.NewTransitionWorker(
		deps.StateReader,
		deps.TxSubmitter,
		deps.PubKeyProvider,
		deps.CryptoFactory,
		deps.VaultProvider,
		deps.SignerProvider,
		deps.AdapterFactory,
		roastSigningService,
		deps.LocalShareStore,
		deps.LocalIdentity,
		string(config.NodeID),
		deps.Logger,
	)
	m.roastDispatcher = roast.NewDispatcher(m.coordinator, m.participant)

	return m
}

                       
func (m *Manager) Start(ctx context.Context) error {
	if m.running.Swap(true) {
		return nil                   
	}

	m.Logger.Info("[FrostManager] Starting with NodeID: %s", m.config.NodeID)

	                          
	if m.notifier != nil {
		m.notifier.SubscribeBlockFinalized(m.handleBlockFinalized)
	}

	                  
	m.wg.Add(1)
	go m.runLoop(ctx)

	m.Logger.Info("[FrostManager] Started")
	return nil
}

                      
func (m *Manager) Stop() {
	m.stopOnce.Do(func() {
		m.Logger.Info("[FrostManager] Stopping...")
		m.running.Store(false)
		close(m.stopCh)
		m.wg.Wait()
		m.Logger.Info("[FrostManager] Stopped")
	})
}

                                              
func (m *Manager) IsRunning() bool {
	return m.running.Load()
}

                                                    
func (m *Manager) LastFinalizedHeight() uint64 {
	return m.lastFinalizedHeight.Load()
}

                                                                
func (m *Manager) SetOnBlockFinalized(fn func(height uint64)) {
	m.onBlockFinalized = fn
}

                                               
func (m *Manager) handleBlockFinalized(height uint64) {
	if !m.running.Load() {
		return
	}

	                                                                         
	                                                                       
	                                                                      
	                                                                          
	                                    
	m.finalizedMu.Lock()
	defer m.finalizedMu.Unlock()
	if !m.running.Load() {
		return
	}
	if height <= m.lastFinalizedHeight.Load() {
		return
	}

	m.lastFinalizedHeight.Store(height)
	m.Logger.Info("[FrostManager] Block finalized: height=%d", height)

	                        
	select {
	case m.finalizedCh <- height:
	default:
		m.Logger.Warn("[FrostManager] finalized channel full, dropping height=%d", height)
	}

	                     
	if m.onBlockFinalized != nil {
		m.onBlockFinalized(height)
	}
}

                    
func (m *Manager) runLoop(ctx context.Context) {
	defer m.wg.Done()
	cleanupLogger := logs.SetThreadNodeContext(string(m.config.NodeID))
	defer cleanupLogger()

	                                                                          
	scanInterval := m.config.ScanInterval
	if scanInterval <= 0 {
		scanInterval = 30 * time.Second
	}
	scanTicker := time.NewTicker(scanInterval)
	defer scanTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			m.Logger.Info("[FrostManager] Context cancelled")
			return
		case <-m.stopCh:
			return
		case height := <-m.finalizedCh:
			m.onFinalized(ctx, height)
		case <-scanTicker.C:
			m.scanAndProcess(ctx)
		}
	}
}

                                      
func (m *Manager) onFinalized(ctx context.Context, height uint64) {
	m.Logger.Trace("[FrostManager] Processing finalized block: height=%d", height)

	                                                                                              
	                                                                            
	m.scanAndProcess(ctx)
}

                                             
func (m *Manager) scanAndProcess(ctx context.Context) {
	if m.emergency.IsStopped() {                                                                                   
		return
	}
	if m.transitionWorker != nil {
		if err := m.transitionWorker.StartPendingSessions(ctx); err != nil {
			m.Logger.Error("[FrostManager] StartPendingSessions error: %v", err)
		}
	}
	processedAccounts := make(map[string]struct{})
	for _, pair := range m.supportedChainAssets() {
		chainName := chain.NormalizeChain(pair.Chain)
		if chainName != chain.ChainBTC {
			if _, exists := processedAccounts[chainName]; exists {
				continue
			}
			processedAccounts[chainName] = struct{}{}
			m.processChainAsset(ctx, chainName, "")
			continue
		}
		m.processChainAsset(ctx, chainName, pair.Asset)
	}
}

func (m *Manager) supportedChainAssets() []ChainAssetPair {
	if m == nil {
		return nil
	}
	now := time.Now()
	m.assetMu.RLock()
	if len(m.assetPairs) > 0 && !m.lastAssetRefresh.IsZero() && now.Sub(m.lastAssetRefresh) < m.assetRefreshEvery {
		pairs := append([]ChainAssetPair(nil), m.assetPairs...)
		m.assetMu.RUnlock()
		return pairs
	}
	m.assetMu.RUnlock()

	pairs, err := m.loadTokenRegistryChainAssets()
	if err != nil {
		m.Logger.Warn("[FrostManager] refresh token assets failed: %v", err)
		m.assetMu.RLock()
		cached := append([]ChainAssetPair(nil), m.assetPairs...)
		m.assetMu.RUnlock()
		return cached
	}
	if len(pairs) == 0 {
		pairs = normalizeChainAssetPairs(m.config.SupportedChains)
	}
	m.assetMu.Lock()
	m.assetPairs = pairs
	m.lastAssetRefresh = now
	out := append([]ChainAssetPair(nil), m.assetPairs...)
	m.assetMu.Unlock()
	return out
}

func (m *Manager) loadTokenRegistryChainAssets() ([]ChainAssetPair, error) {
	if m == nil || m.stateReader == nil {
		return normalizeChainAssetPairs(m.config.SupportedChains), nil
	}
	pairs := append([]ChainAssetPair(nil), m.config.SupportedChains...)
	registryKey := keys.KeyTokenRegistry()
	err := m.stateReader.Scan(keys.KeyTokenPrefix(), func(k string, v []byte) bool {
		if k == registryKey || len(v) == 0 {
			return true
		}
		var tok pb.Token
		if err := proto.Unmarshal(v, &tok); err != nil {
			return true
		}
		if tok.Type != pb.TokenType_TOKEN_BRIDGED || strings.TrimSpace(tok.Address) == "" {
			return true
		}
		chainName := chain.NormalizeChain(tok.NativeChain)
		if !chain.IsValidChain(chainName) {
			return true
		}
		identity, err := bridgeasset.Resolve(chainName, tok.NativeContract)
		if err != nil || tok.Address != identity.ID || tok.NativeChain != identity.Chain || tok.NativeContract != identity.Contract {
			return true
		}
		indexed, exists, err := m.stateReader.Get(keys.KeyNativeTokenIndex(identity.Chain, identity.Contract))
		if err != nil || !exists || string(indexed) != identity.ID {
			return true
		}
		pairs = append(pairs, ChainAssetPair{Chain: identity.Chain, Asset: identity.ID})
		return true
	})
	if err != nil {
		return nil, err
	}
	return normalizeChainAssetPairs(pairs), nil
}

func normalizeChainAssetPairs(in []ChainAssetPair) []ChainAssetPair {
	seen := make(map[string]ChainAssetPair, len(in))
	for _, pair := range in {
		chainName := chain.NormalizeChain(pair.Chain)
		asset := strings.TrimSpace(pair.Asset)
		if chainName == "" || asset == "" {
			continue
		}
		key := chainName + "\x00" + asset
		seen[key] = ChainAssetPair{Chain: chainName, Asset: asset}
	}
	keysSorted := make([]string, 0, len(seen))
	for key := range seen {
		keysSorted = append(keysSorted, key)
	}
	sort.Strings(keysSorted)
	out := make([]ChainAssetPair, 0, len(keysSorted))
	for _, key := range keysSorted {
		out = append(out, seen[key])
	}
	return out
}

                                             
func (m *Manager) processChainAsset(ctx context.Context, chainName, asset string) {
	                               
	scanResult, err := m.scanner.ScanOnce(chainName, asset)
	if err != nil {
		m.Logger.Error("[FrostManager] Scan error for %s/%s: %v", chainName, asset, err)
		return
	}

	if scanResult != nil {
		m.Logger.Trace("[FrostManager] Found pending withdraw: chain=%s, asset=%s, id=%s, seq=%d",
			chainName, asset, scanResult.WithdrawID, scanResult.Seq)
	}

	                                   
	                                                                       
	                                                                        
	job, err := m.withdrawWorker.ProcessOnce(ctx, chainName, asset)
	if err != nil {
		m.Logger.Info("[FrostManager] ProcessOnce error: %v", err)
		return
	}

	if job != nil {
		m.Logger.Info("[FrostManager] Created job: id=%s, withdraws=%v", job.JobID, job.WithdrawIDs)
	}
}

                                                   
func (m *Manager) GetCoordinator() *roast.Coordinator {
	return m.coordinator
}

                                                   
func (m *Manager) GetParticipant() *roast.Participant {
	return m.participant
}

                                                                         
func (m *Manager) HandleRoastEnvelope(env *roast.Envelope) error {
	if m.roastDispatcher == nil {
		return nil
	}
	return m.roastDispatcher.Handle(env)
}

                                                                     
func (m *Manager) HandleFrostEnvelope(env *FrostEnvelope) error {
	if m.roastDispatcher == nil {
		return nil
	}
	                                                       
	roastEnv := &roast.FrostEnvelope{
		SessionID: env.SessionID,
		Kind:      env.Kind,
		From:      roast.NodeID(env.From),
		Chain:     env.Chain,
		VaultID:   env.VaultID,
		SignAlgo:  env.SignAlgo,
		Epoch:     env.Epoch,
		Round:     env.Round,
		Payload:   env.Payload,
		Sig:       env.Sig,
		Tweaks:    env.Tweaks,
	}
	return m.roastDispatcher.HandleFrostEnvelope(roastEnv)
}

                                                                          
func (m *Manager) HandlePBEnvelope(env *pb.FrostEnvelope) error {
	if env == nil {
		return roast.ErrInvalidPBEnvelope
	}

	switch env.Kind {
	case pb.FrostEnvelopeKind_FROST_ENVELOPE_KIND_ROAST_REQUEST,
		pb.FrostEnvelopeKind_FROST_ENVELOPE_KIND_ROAST_RESPONSE:
		roastEnv, err := roast.EnvelopeFromPB(env)
		if err != nil {
			return err
		}
		return m.HandleRoastEnvelope(roastEnv)
	default:
		return nil
	}
}

                                                                
func (m *Manager) GetTransitionWorker() *workers.TransitionWorker {
	return m.transitionWorker
}

                                                                                           
type roastMessengerAdapter struct {
	messenger RoastMessenger
	p2p       P2P
}

func (a *roastMessengerAdapter) Send(to types.NodeID, msg *types.RoastEnvelope) error {
	if a == nil || msg == nil {
		return nil
	}
	if a.messenger != nil {
		return a.messenger.Send(to, msg)
	}
	if a.p2p != nil {
		                                            
		frostEnv := &FrostEnvelope{
			SessionID: msg.SessionID,
			Kind:      msg.Kind,
			From:      msg.From,
			Chain:     msg.Chain,
			VaultID:   msg.VaultID,
			SignAlgo:  msg.SignAlgo,
			Epoch:     msg.Epoch,
			Round:     msg.Round,
			Payload:   msg.Payload,
			Tweaks:    msg.Tweaks,
		}
		return a.p2p.Send(to, frostEnv)
	}
	return nil
}

func (a *roastMessengerAdapter) Broadcast(peers []types.NodeID, msg *types.RoastEnvelope) error {
	if a == nil || msg == nil {
		return nil
	}
	if a.messenger != nil {
		return a.messenger.Broadcast(peers, msg)
	}
	if a.p2p != nil {
		                                            
		frostEnv := &FrostEnvelope{
			SessionID: msg.SessionID,
			Kind:      msg.Kind,
			From:      msg.From,
			Chain:     msg.Chain,
			VaultID:   msg.VaultID,
			SignAlgo:  msg.SignAlgo,
			Epoch:     msg.Epoch,
			Round:     msg.Round,
			Payload:   msg.Payload,
			Tweaks:    msg.Tweaks,
		}
		return a.p2p.Broadcast(peers, frostEnv)
	}
	return nil
}

                                                                                                     
                              
