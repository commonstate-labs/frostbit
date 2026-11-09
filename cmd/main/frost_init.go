package main

import (
	"context"
	"dex/config"
	"dex/consensus"
	"dex/frost/ceremony"
	"dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/frost/chain/evm"
	"dex/frost/guardian"
	frostrt "dex/frost/runtime"
	"dex/frost/runtime/adapters"
	"dex/frost/runtime/committee"
	"dex/logs"
	"dex/pb"
	"dex/types"
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
)

                                                                    
                                                                                            
                                                                            
func initFrostRuntime(node *NodeInstance, cfg *config.Config) (*frostrt.Manager, error) {
	fm, _, err := prepareFrostRuntime(node, cfg, true)
	return fm, err
}

                                                                  
                                                                               
                                                                                 
                                                                             
                                                                               
                                          
func prepareFrostRuntime(node *NodeInstance, cfg *config.Config, preflightCurrentShares bool) (*frostrt.Manager, frostGenesisShareStore, error) {
	if !cfg.Frost.Enabled {
		return nil, nil, nil
	}

	var localShareStore frostrt.LocalShareStore
	if node.FrostPlaintextGenesisShares {
		if node.NetworkChainID != ceremony.PlaintextGenesisChainID || len(node.FrostWrapKey) != 0 || strings.TrimSpace(node.FrostGenesisSharesDir) == "" {
			return nil, nil, errors.New("plaintext FROST genesis import requires DEX chain 51025, a bundle directory, and no external wrap key")
		}
		store := adapters.NewDBLocalShareStore(node.DBManager)
		if err := store.EnsureReady(); err != nil {
			return nil, nil, fmt.Errorf("unlock local FROST share store: %w", err)
		}
		if err := importFrostGenesisShares(node, cfg, store, nil); err != nil {
			return nil, nil, fmt.Errorf("import plaintext standalone-DKG genesis shares: %w", err)
		}
		localShareStore = store
	} else if len(node.FrostWrapKey) > 0 {
		wrapKey := node.FrostWrapKey
		node.FrostWrapKey = nil
		defer wipeBytes(wrapKey)
		store, err := adapters.NewDBLocalShareStoreWithExternalWrapKey(node.DBManager, wrapKey)
		if err != nil {
			return nil, nil, fmt.Errorf("unlock local FROST share store: %w", err)
		}
		if strings.TrimSpace(node.FrostGenesisSharesDir) != "" {
			if err := importFrostGenesisShares(node, cfg, store, wrapKey); err != nil {
				return nil, nil, fmt.Errorf("import standalone-DKG genesis shares: %w", err)
			}
		}
		localShareStore = store
	} else {
		if strings.TrimSpace(node.FrostGenesisSharesDir) != "" {
			return nil, nil, errors.New("FROST genesis share import requires an external wrap key")
		}
		store := adapters.NewDBLocalShareStore(node.DBManager)
		if err := store.EnsureReady(); err != nil {
			return nil, nil, fmt.Errorf("unlock local FROST share store: %w", err)
		}
		localShareStore = store
	}
	if preflightCurrentShares {
		if err := ensureConfiguredGenesisLocalShares(
			cfg, node.Address, node.NetworkChainID, node.FrostPeerManifestDigest,
			node.DBManager, localShareStore, node.DBManager,
		); err != nil {
			return nil, nil, fmt.Errorf("preflight configured-genesis FROST shares: %w", err)
		}
	}

	                                  
	var stateReader frostrt.ChainStateReader
	if node.DBManager != nil {
		stateReader = adapters.NewStateDBReader(node.DBManager)
	}

	                                                         
	frostConfig := cfg.Frost
	adapterFactory := chain.NewDefaultAdapterFactory()
	btcNetwork := "mainnet"
	if chainCfg, ok := frostConfig.Chains[chain.ChainBTC]; ok && strings.TrimSpace(chainCfg.BTCNetwork) != "" {
		btcNetwork = chainCfg.BTCNetwork
	}
	adapterFactory.RegisterAdapter(btc.NewBTCAdapter(btcNetwork))

	for _, chainName := range []string{chain.ChainETH, chain.ChainBNB, chain.ChainBASE, chain.ChainARB, chain.ChainTRX} {
		chainCfg, ok := frostConfig.Chains[chainName]
		if !ok {
			continue
		}
		if evmAdapter := evm.NewEVMAdapter(chainName, chainCfg.ChainID); evmAdapter != nil {
			adapterFactory.RegisterAdapter(evmAdapter)
		}
	}

	                                     
	var txSubmitter frostrt.TxSubmitter
	if node.TxPool != nil && node.SenderManager != nil {
		txPoolAdapter := adapters.NewTxPoolAdapter(node.TxPool, node.SenderManager)
		txSubmitter = adapters.NewTxPoolSubmitter(txPoolAdapter)
	}

	                                            
	var notifier frostrt.FinalityNotifier
	if node.ConsensusManager != nil {
		eventBus := node.ConsensusManager.GetEventBus()
		if eventBus != nil {
			notifier = adapters.NewEventBusFinalityNotifier(eventBus)
		}
	}

	                                
	var p2p frostrt.P2P
	if node.ConsensusManager != nil && node.ConsensusManager.Transport != nil {
		p2p = adapters.NewTransportP2P(node.ConsensusManager.Transport, frostrt.NodeID(node.Address))
	}

	                                  
	var signerProvider frostrt.SignerSetProvider
	if node.DBManager != nil {
		dbAdapter := adapters.NewDBManagerAdapter(node.DBManager)
		signerProvider = adapters.NewConsensusSignerProvider(dbAdapter)
	}

	                                          
	var vaultProvider frostrt.VaultCommitteeProvider
	if stateReader != nil {
		vaultProvider = committee.NewDefaultVaultCommitteeProvider(stateReader, committee.DefaultVaultCommitteeProviderConfig())
	}
	var pubKeyProvider frostrt.MinerPubKeyProvider
	if stateReader != nil {
		pubKeyProvider = adapters.NewStatePubKeyProvider(stateReader)
	}
	cryptoFactory := adapters.NewDefaultCryptoExecutorFactory()

	                                              
	frostCfg := frostrt.ManagerConfig{
		NodeID:                   frostrt.NodeID(node.Address),
		ScanInterval:             30 * time.Second,
		MaxInFlightPerChainAsset: frostConfig.Withdraw.MaxInFlightPerChainAsset,
		SupportedChains:          nil,
	}
	var roastMessenger frostrt.RoastMessenger
	if node.SenderManager != nil {
		roastMessenger = adapters.NewSenderRoastMessenger(node.SenderManager, node.KeyManager, node.DBManager)
	}
	frostDeps := frostrt.ManagerDeps{
		StateReader:     stateReader,
		TxSubmitter:     txSubmitter,
		Notifier:        notifier,
		P2P:             p2p,
		RoastMessenger:  roastMessenger,
		SignerProvider:  signerProvider,
		VaultProvider:   vaultProvider,
		AdapterFactory:  adapterFactory,
		PubKeyProvider:  pubKeyProvider,
		CryptoFactory:   cryptoFactory,
		LocalShareStore: localShareStore,
		NonceStore:      adapters.NewDBNonceStore(node.DBManager),
		LocalIdentity:   node.KeyManager,
		LogReporter:     adapters.NewLocalLogReporter(node.DBManager),
		Logger:          node.Logger,
	}
	if endpoint := strings.TrimSpace(frostConfig.Guardian.Endpoint); endpoint != "" {
		frostDeps.GuardianClient = guardian.NewHTTPClient(endpoint)
	}
	frostManager := frostrt.NewManager(frostCfg, frostDeps)
	frostManager.SetEmergencyStop(node.Emergency)                                                     
	node.FrostRuntime = frostManager

	                                 
	if node.HandlerManager != nil {
		node.HandlerManager.SetFrostMsgHandler(func(msg types.Message) error {
			if node.FrostRuntime == nil || !node.FrostRuntime.IsRunning() {
				return nil
			}
			if len(msg.FrostPayload) == 0 {
				return fmt.Errorf("empty frost payload")
			}
			var env pb.FrostEnvelope
			if err := proto.Unmarshal(msg.FrostPayload, &env); err != nil {
				return err
			}
			return node.FrostRuntime.HandlePBEnvelope(&env)
		})
	}

	return frostManager, localShareStore, nil
}

                                                                             
                                                                
func startFrostRuntimeAsync(node *NodeInstance, fm *frostrt.Manager) {
	_ = startFrostRuntimeAsyncAfterCatchup(context.Background(), node, fm, nil, nil, false, false)
}

                                                                        
                                                                              
                                                                         
                                                                              
                                                                               
                                          
func startFrostRuntimeAsyncAfterCatchup(
	ctx context.Context,
	node *NodeInstance,
	fm *frostrt.Manager,
	cfg *config.Config,
	shareStore frostGenesisShareStore,
	waitForCatchup bool,
	deferCurrentSharePreflight bool,
) <-chan struct{} {
	done := make(chan struct{})
	if fm == nil {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		cleanupLogger := logs.SetThreadNodeContext(node.Address)
		defer cleanupLogger()

		lastGateError := ""
		                                                                        
		                                                                           
		                                                                            
		                                                                         
		                                     
		for {
			if ctx.Err() != nil {
				return
			}
			if node.ConsensusManager == nil {
				if waitForCatchup {
					logs.Error("[Frost] Fresh late-join runtime has no consensus sync manager")
					return
				}
				break
			}
			_, height := node.ConsensusManager.GetLastAccepted()
			if height < 1 {
				if !waitFrostStartGate(ctx, 2*time.Second) {
					return
				}
				continue
			}
			if waitForCatchup {
				status := node.ConsensusManager.GetSyncStatus()
				if !frostCatchupReady(status, height) {
					if !waitFrostStartGate(ctx, 2*time.Second) {
						return
					}
					continue
				}
				blockID, ok := node.ConsensusManager.GetFinalizedBlockID(status.VerifiedSyncHeight)
				if !ok {
					if !waitFrostStartGate(ctx, 2*time.Second) {
						return
					}
					continue
				}
				if err := persistFrostLateJoinSyncMarker(node.DBManager, node.ConsensusManager, node, consensus.VerifiedSyncPoint{
					Height: status.VerifiedSyncHeight, BlockID: blockID,
				}); err != nil {
					message := err.Error()
					if message != lastGateError {
						logs.Error("[Frost] Runtime remains disabled until verified catch-up is durably recorded: %v", err)
						lastGateError = message
					}
					if !waitFrostStartGate(ctx, 2*time.Second) {
						return
					}
					continue
				}
			}
			if deferCurrentSharePreflight {
				if cfg == nil || shareStore == nil {
					logs.Error("[Frost] Late-join current-share gate is unavailable")
					return
				}
				if err := ensureConfiguredGenesisLocalShares(
					cfg, node.Address, node.NetworkChainID, node.FrostPeerManifestDigest,
					node.DBManager, shareStore, node.DBManager,
				); err != nil {
					message := err.Error()
					if message != lastGateError {
						logs.Error("[Frost] Runtime remains disabled after catch-up: %v", err)
						lastGateError = message
					}
					if !waitFrostStartGate(ctx, 2*time.Second) {
						return
					}
					continue
				}
			}
			break
		}

		if ctx.Err() != nil {
			return
		}
		if err := fm.Start(ctx); err != nil {
			logs.Error("[Frost] Failed to start runtime for node %d: %v", node.ID, err)
		} else {
			logs.Info("  ✅ Node %d FROST Runtime started (height >= 1)", node.ID)
		}
	}()
	return done
}

func preflightFrostSharesBeforeNetworking(mode BootstrapMode) bool {
	return mode != LateJoin
}

func waitFrostStartGate(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
