package main

import (
	"bufio"
	"context"
	"crypto/subtle"
	"crypto/tls"
	"dex/arbitrage"
	"dex/certchain"
	"dex/config"
	"dex/consensus"
	"dex/db"
	frostrt "dex/frost/runtime"
	"dex/handlers"
	"dex/keys"
	"dex/lifecycle"
	"dex/liveness"
	"dex/logs"
	"dex/pb"

	"dex/sender"
	"dex/txpool"
	"dex/txverify"
	"dex/types"
	"dex/utils"
	"dex/vm"
	"dex/witness"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"google.golang.org/protobuf/proto"
)

                           
type NodeInstance struct {
	ID                          int
	KeyManager                  *utils.KeyManager
	ArbitrageKeyManager         *utils.KeyManager
	ArbitrageAddress            string
	Address                     string
	Port                        string
	ListenAddr                  string
	AdvertiseAddr               string
	DataPath                    string
	Server                      *http.Server                   
	HTTP3Server                 *http3.Server                      
	PProfServer                 *http.Server                                     
	EnableDebugAPI              bool
	EnableOperatorControlAPI    bool
	EnablePprof                 bool
	PprofBind                   string
	ServerConfig                config.ServerConfig
	ConsensusManager            *consensus.ConsensusNodeManager
	DBManager                   *db.Manager
	Cancel                      context.CancelFunc
	TxPool                      *txpool.TxPool
	SenderManager               *sender.SenderManager
	HandlerManager              *handlers.HandlerManager
	FrostRuntime                *frostrt.Manager                                                  
	FrostWrapKey                []byte                                                                                       
	FrostGenesisSharesDir       string                                                                                                    
	FrostPlaintextGenesisShares bool                                                                                    
	NetworkChainID              uint64                                                                        
	FrostPeerManifestDigest     []byte                                                                         
	WitnessService              *witness.Service                           
	EventsHub                   *eventsHub                                                                 
	Emergency                   *lifecycle.EmergencyStop                                                                
	Logger                      logs.Logger
}

type statusCodeRecorder struct {
	http.ResponseWriter
	statusCode int
}

                                                                            
                                                                             
                                                                
func requireExplorerServiceToken(next http.HandlerFunc) http.HandlerFunc {
	want := config.ExplorerServiceToken()
	return func(w http.ResponseWriter, r *http.Request) {
		serviceOK := strings.EqualFold(strings.TrimSpace(r.Header.Get(config.ExplorerServiceHeader)), "explorer")
		got := strings.TrimSpace(r.Header.Get(config.ExplorerServiceTokenHeader))
		tokenOK := want != "" && len(got) == len(want) && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
		if !serviceOK || !tokenOK {
			http.Error(w, "explorer service authentication required", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func buildCertSignTx(address string, checkpointHeight uint64, signerIndex uint32, digest, blsSignature []byte, keyManager *utils.KeyManager) (*pb.AnyTx, error) {
	if keyManager == nil {
		return nil, fmt.Errorf("missing key manager")
	}
	tx := &pb.AnyTx{Content: &pb.AnyTx_CertSignTx{CertSignTx: &pb.CertSignTx{
		Base: &pb.BaseMessage{
			FromAddress: address,
			                                                                                          
			Nonce: checkpointHeight,
			Fee:   "1000",
		},
		CheckpointHeight: checkpointHeight,
		SignerIndex:      signerIndex,
		BlsSignature:     append([]byte(nil), blsSignature...),
		CheckpointDigest: append([]byte(nil), digest...),
	}}}
	if _, err := txverify.SignWithKeyManager(tx, keyManager); err != nil {
		return nil, err
	}
	return tx, nil
}

func (r *statusCodeRecorder) WriteHeader(code int) {
	r.statusCode = code
	r.ResponseWriter.WriteHeader(code)
}

                                                                                
                                                         
func (r *statusCodeRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := r.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("hijack not supported by underlying ResponseWriter")
}

                                                                                    
func (r *statusCodeRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *statusCodeRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

                                                                              
                                              
func wrapHandlerWithLatencyMetrics(node *NodeInstance, mux *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusCodeRecorder{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rec, r)

		if node == nil || node.HandlerManager == nil || node.HandlerManager.Stats == nil {
			return
		}
		routeID := handlers.CanonicalRouteID(mux, r)
		metric := "handler" + routeID
		if rec.statusCode >= 500 {
			metric += ".5xx"
		}
		node.HandlerManager.Stats.RecordLatency(metric, time.Since(start))
	})
}

                        
func initializeNode(node *NodeInstance, cfg *config.Config, chainID uint64) error {
	node.ServerConfig = cfg.Server
	if chainID == 0 {
		chainID = config.DefaultChainID
	}
	node.NetworkChainID = chainID
	defer func() { node.ArbitrageKeyManager = nil }()
	                                                                            
	keyMgr := node.KeyManager
	if keyMgr == nil || !keyMgr.HasPrivateKey() {
		return fmt.Errorf("node runtime identity is missing")
	}
	node.Address = keyMgr.GetAddress()
	var arbitrageKeyMgr *utils.KeyManager
	if cfg.Arbitrage.Enabled {
		if node.ArbitrageKeyManager == nil || !node.ArbitrageKeyManager.HasPrivateKey() {
			return fmt.Errorf("arbitrage is enabled but its runtime private key is missing")
		}
		arbitrageKeyMgr = node.ArbitrageKeyManager
		derivedAddress := arbitrageKeyMgr.GetAddress()
		if node.ArbitrageAddress != "" && node.ArbitrageAddress != derivedAddress {
			return fmt.Errorf("arbitrage private key derives %s, not runtime address %s", derivedAddress, node.ArbitrageAddress)
		}
		if err := validateIndependentArbitrageAddress(node.Address, derivedAddress); err != nil {
			return err
		}
		node.ArbitrageAddress = derivedAddress
	} else if node.ArbitrageKeyManager != nil || strings.TrimSpace(node.ArbitrageAddress) != "" {
		return fmt.Errorf("arbitrage identity supplied while arbitrage is disabled")
	}

	                                                      
	utils.Port = node.Port

	                        
	dbManager, err := db.NewManagerWithConfig(node.DataPath, node.Logger, cfg)
	if err != nil {
		return fmt.Errorf("failed to init db: %v", err)
	}
	node.DBManager = dbManager

	                              
	maxBatchSize := cfg.Database.MaxBatchSize
	if maxBatchSize <= 0 {
		maxBatchSize = 1000
	}
	flushInterval := cfg.Database.FlushInterval
	if flushInterval <= 0 {
		flushInterval = 500 * time.Millisecond
	}
	dbManager.InitWriteQueue(maxBatchSize, flushInterval)

	                                                              
	validator := &TestValidator{ChainID: chainID}

	                                                                            
	if node.Emergency == nil {
		node.Emergency = lifecycle.New()
	}

	                                                   
	txPool, err := txpool.NewTxPoolWithConfig(dbManager, validator, node.Address, node.Logger, cfg)
	if err != nil {
		return fmt.Errorf("failed to create TxPool: %v", err)
	}
	txPool.SetEmergencyStop(node.Emergency)
	if err := txPool.Start(); err != nil {
		return fmt.Errorf("failed to start TxPool: %v", err)
	}
	node.TxPool = txPool

	                                                                                 
	                                                                                    
	senderManager := sender.NewSenderManager(dbManager, node.Address, node.ListenAddr, txPool, node.ID, node.Logger, cfg)
	node.SenderManager = senderManager
	if err := senderManager.ConfigureServiceIdentity(keyMgr, node.AdvertiseAddr, chainID); err != nil {
		                                                                       
		                                                                         
		                                                    
		node.Logger.Info("[ServicePeer] public service role unavailable for %s: %v", node.AdvertiseAddr, err)
	} else {
		node.Logger.Info("[ServicePeer] public service role configured: endpoint=%s", node.AdvertiseAddr)
	}

	                                                                                                      
	consCfg := consensus.NewRuntimeConfig(cfg.Consensus, cfg.Window)

	                     
	packetLossRate := cfg.Network.PacketLossRate
	minLatency := cfg.Network.MinLatency
	maxLatency := cfg.Network.MaxLatency

	consensusManager, err := consensus.InitConsensusManagerWithSimulation(
		types.NodeID(node.Address),
		dbManager,
		consCfg,
		senderManager,
		txPool,
		node.Logger,
		packetLossRate,
		minLatency,
		maxLatency,
		cfg,
	)
	if err != nil {
		return fmt.Errorf("failed to initialize consensus manager: %w", err)
	}
	consensusManager.SetLocalIdentity(keyMgr, node.Address)
	                                                                          
	                                                                 
	consensusManager.SetServicePeerStore(senderManager.ServicePeerStore())
	                                                                            
	                                                                            
	                         
	senderManager.SetFinalizedBlockLookup(consensusManager)
	consensusManager.SetEmergencyStop(node.Emergency)                                           
	node.ConsensusManager = consensusManager
	txPool.SetSyncPressureReader(consensusManager)

	                                                                           
	if consensusManager != nil && consensusManager.Node != nil {
		if rs, ok := consensusManager.Node.GetBlockStore().(*consensus.RealBlockStore); ok {
			node.WitnessService = rs.GetWitnessService()
		}
	}

	                                                                                                                                
	                                                                                                             
	                                                                          
	                                                                             
	                           
	livenessTracker := liveness.NewTracker(cfg.Liveness)
	livenessTracker.SetStore(dbManager)
	livenessReporter := liveness.NewLocalReporter(livenessTracker)
	consensusManager.SetLivenessTracker(livenessTracker)
	consensusManager.SetLivenessReporter(livenessReporter)
	if consensusManager.Node != nil {
		if rs, ok := consensusManager.Node.GetBlockStore().(*consensus.RealBlockStore); ok {
			if exec := rs.GetExecutor(); exec != nil {
				exec.SetNetworkChainID(chainID)
				exec.LivenessTracker = livenessTracker

				                                                                               
				                                                                                     
				if cfg.Arbitrage.Enabled {
					if arbitrageKeyMgr == nil || arbitrageKeyMgr.GetAddress() == "" {
						return fmt.Errorf("initialize proposer arbitrage machine: independent arbitrage signer is unavailable")
					}
					arbCfg := cfg.Arbitrage
					arbCfg.ChainID = chainID
					machine, arbErr := arbitrage.NewMachine(
						arbCfg,
						dbManager,
						arbitrageKeyMgr.GetAddress(),
						func(tx *pb.AnyTx) (string, error) {
							return txverify.SignWithKeyManager(tx, arbitrageKeyMgr)
						},
						node.Logger,
					)
					if arbErr != nil {
						return fmt.Errorf("initialize proposer arbitrage machine: %w", arbErr)
					}
					consensusManager.SetPrivateTxProvider(machine)
					consensusManager.SetProposalPreflight(consensus.NewVMProposalPreflight(exec))
					logs.Info("[Arbitrage] Enabled proposer-private dynamic spot arbitrage account=%s miner=%s from VM-projected txpool prefixes", arbitrageKeyMgr.GetAddress(), node.Address)
				}

				                                                                            
				certCfg := certchain.DefaultConfig()
				stateCheckpointEvery := cfg.Database.StateDB.CheckpointEvery
				if stateCheckpointEvery == 0 {
					stateCheckpointEvery = certCfg.CheckpointInterval
				}
				if stateCheckpointEvery != certCfg.CheckpointInterval {
					logs.Warn("[Config] StateDB CheckpointEvery=%d differs from CertChain CheckpointInterval=%d; cert-height snapshots are forced, but mixed checkpoint cadences can shrink or skew the retained height window", stateCheckpointEvery, certCfg.CheckpointInterval)
				}
				certTracker := certchain.NewTracker(certCfg)
				certTracker.SetStore(dbManager)
				exec.CertChainTracker = certTracker

				                                                                               
				localTxPool := txPool
				localKeyMgr := keyMgr
				localSenderMgr := senderManager
				localCertTracker := certTracker
				certTracker.SetOnDigestReady(node.Address, func(cpHeight uint64, digest []byte, signerIndex uint32, isActive bool) {
					if !isActive || localTxPool == nil || localKeyMgr == nil {
						return
					}
					blsSig, err := localKeyMgr.SignBLS(digest)
					if err != nil {
						logs.Warn("[CertChain] Auto-sign failed: %v", err)
						return
					}

					                                                                                               
					myPubBytes, expectedIndex, found := localCertTracker.GetPendingMinerByAddress(cpHeight, node.Address)
					if !found || expectedIndex != signerIndex {
						logs.Warn("[CertChain] Auto-sign context disappeared: checkpoint=%d signer=%d", cpHeight, signerIndex)
						return
					}
					myPub, deserErr := utils.DeserializePublicKey(myPubBytes)
					if deserErr != nil {
						logs.Warn("[CertChain] Pre-verify: deserialize own pubKey failed (signer=%d): %v", signerIndex, deserErr)
						return
					}
					if verErr := utils.BLSVerifySignature(myPub, string(digest), blsSig); verErr != nil {
						logs.Error("[CertChain] Pre-verify failed (signer=%d cp=%d): %v", signerIndex, cpHeight, verErr)
						return
					}

					anyTx, signErr := buildCertSignTx(node.Address, cpHeight, signerIndex, digest, blsSig, localKeyMgr)
					if signErr != nil {
						logs.Warn("[CertChain] Auto-sign tx failed: %v", signErr)
						return
					}
					if err := localTxPool.SubmitTx(anyTx, "127.0.0.1", nil); err != nil {
						logs.Debug("[CertChain] Auto-submit CertSignTx failed: %v", err)
					} else {
						logs.Info("[CertChain] Auto-submitted CertSignTx for checkpoint %d (signer=%d)", cpHeight, signerIndex)
					}
					                                                                                                
					if localSenderMgr != nil {
						localSenderMgr.BroadcastTx(anyTx)
					}
				})
			}
		}
	}

	                                                                                                                           
	node.EventsHub = newEventsHub()
	node.EventsHub.attach(consensusManager.GetEventBus())

	                            
	handlerManager := handlers.NewHandlerManager(
		dbManager,
		consensusManager,
		node.Port,
		node.Address,
		senderManager,
		txPool,
		node.Logger,
		cfg,
	)
	node.HandlerManager = handlerManager
	handlerManager.SetPeerGateLiveness(livenessTracker)
	handlerManager.SetKeyManager(keyMgr)
	handlerManager.SetNodeAdvertiseAddr(nodeAdvertiseAddr(node))
	handlerManager.SetEmergencyStop(node.Emergency)
	handlerManager.SetOperatorControlAPI(node.EnableOperatorControlAPI)

	return nil
}

                                                 
func startHTTPServerWithSignal(node *NodeInstance, readyChan chan<- int, errorChan chan<- error) error {
	                   
	mux := http.NewServeMux()

	                                                 
	restoreThreadLogger := logs.SetThreadLogger(node.Logger)
	defer restoreThreadLogger()

	                                            
	restoreNodeContext := logs.SetThreadNodeContext(node.Address)
	defer restoreNodeContext()

	                                   
	node.HandlerManager.RegisterRoutes(mux)

	registerSimulatorControlRoute(mux, node)

	                                                                                             
	if node.EventsHub != nil {
		mux.HandleFunc("/events/stream", node.EventsHub.handleWS)
	}

	                                                                             
	                                                                           
	handler := node.HandlerManager.WrapTraffic(mux, wrapHandlerWithLatencyMetrics(node, mux, mux))

	                                                                            
	                                                                             
	                                                                           
	tlsDir := filepath.Join(node.DataPath, "runtime", "tls")
	if err := os.MkdirAll(tlsDir, 0o700); err != nil {
		errorChan <- fmt.Errorf("Node %d: Failed to create TLS directory: %v", node.ID, err)
		return err
	}
	certFile := filepath.Join(tlsDir, "server.crt")
	keyFile := filepath.Join(tlsDir, "server.key")

	if err := generateSelfSignedCert(certFile, keyFile); err != nil {
		errorChan <- fmt.Errorf("Node %d: Failed to generate certificate: %v", node.ID, err)
		return err
	}

	                  
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{},
		MinVersion:   tls.VersionTLS13,
		MaxVersion:   tls.VersionTLS13,
		                                              
		NextProtos: []string{"h3", "h3-29", "h3-28", "h3-27", "http/1.1"},                              
	}

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		errorChan <- fmt.Errorf("Node %d: Failed to load certificate: %v", node.ID, err)
		return err
	}
	tlsConfig.Certificates = append(tlsConfig.Certificates, cert)

	serverCfg := node.ServerConfig
	if serverCfg.MaxConnections <= 0 {
		serverCfg = config.DefaultConfig().Server
	}
	connectionBudget := newConnectionGuard(serverCfg.MaxConnections, serverCfg.MaxConnectionsPerIP, node.HandlerManager.IsVerifiedPeerIP)

	                   
	quicConfig := &quic.Config{
		KeepAlivePeriod:                serverCfg.QUICKeepAlivePeriod,
		HandshakeIdleTimeout:           serverCfg.QUICHandshakeIdleTimeout,
		MaxIdleTimeout:                 serverCfg.QUICMaxIdleTimeout,
		InitialStreamReceiveWindow:     512 << 10,
		MaxStreamReceiveWindow:         2 << 20,
		InitialConnectionReceiveWindow: 1 << 20,
		MaxConnectionReceiveWindow:     8 << 20,
		MaxIncomingStreams:             serverCfg.QUICMaxIncomingStreams,
		MaxIncomingUniStreams:          serverCfg.QUICMaxIncomingUniStreams,
		Allow0RTT:                      false,
	}

	                        
	quicListenAddr := strings.TrimSpace(node.ListenAddr)
	if quicListenAddr == "" {
		quicListenAddr, err = resolveNodeListenAddr("", nodeAdvertiseAddr(node), node.Port)
		if err != nil {
			errorChan <- fmt.Errorf("Node %d: Invalid listen address: %v", node.ID, err)
			return err
		}
	}

	server := &http3.Server{
		Addr:           quicListenAddr,
		Handler:        handler,
		TLSConfig:      tlsConfig,
		QUICConfig:     quicConfig,
		MaxHeaderBytes: serverCfg.MaxHeaderBytes,
		IdleTimeout:    serverCfg.HTTPIdleTimeout,
	}
	node.HTTP3Server = server

	udpListenAddr, err := net.ResolveUDPAddr("udp4", quicListenAddr)
	if err != nil {
		errorChan <- fmt.Errorf("Node %d: Failed to resolve QUIC listener %s: %v", node.ID, quicListenAddr, err)
		return err
	}
	udpConn, err := net.ListenUDP("udp4", udpListenAddr)
	if err != nil {
		errorChan <- fmt.Errorf("Node %d: Failed to create QUIC UDP listener %s: %v", node.ID, quicListenAddr, err)
		return err
	}
	quicTransport := &quic.Transport{
		Conn:                udpConn,
		VerifySourceAddress: shouldVerifyQUICSource,
		ConnContext: func(ctx context.Context, info *quic.ClientInfo) (context.Context, error) {
			release, ok := connectionBudget.acquire(info.RemoteAddr)
			if !ok {
				return nil, errors.New("QUIC connection limit reached")
			}
			go func() {
				<-ctx.Done()
				release()
			}()
			return ctx, nil
		},
	}
	listener, err := quicTransport.ListenEarly(tlsConfig, quicConfig)
	if err != nil {
		_ = quicTransport.Close()
		errorChan <- fmt.Errorf("Node %d: Failed to create QUIC listener: %v", node.ID, err)
		return err
	}
	defer quicTransport.Close()

	                                                                          
	                                                                             
	                              
	tcpServer := &http.Server{
		Addr:              quicListenAddr,
		Handler:           handler,
		TLSConfig:         tlsConfig,
		ReadHeaderTimeout: serverCfg.HTTPReadHeaderTimeout,
		ReadTimeout:       serverCfg.HTTPTimeout,
		WriteTimeout:      serverCfg.HTTPTimeout,
		IdleTimeout:       serverCfg.HTTPIdleTimeout,
		MaxHeaderBytes:    serverCfg.MaxHeaderBytes,
	}
	node.Server = tcpServer
	tcpListener, err := net.Listen("tcp", tcpServer.Addr)
	if err != nil {
		_ = listener.Close()
		err = fmt.Errorf("Node %d: Failed to create TCP TLS listener %s: %w", node.ID, tcpServer.Addr, err)
		errorChan <- err
		return err
	}
	defer tcpListener.Close()
	defer tcpServer.Close()

	logs.Info("Node %d: Starting HTTP/3 and TCP TLS servers on %s", node.ID, quicListenAddr)

	                                                                            
	                                                                          
	readyChan <- node.ID

	go func() {
		limitedListener := &guardedListener{Listener: tcpListener, guard: connectionBudget}
		if err := tcpServer.ServeTLS(limitedListener, "", ""); err != nil && err != http.ErrServerClosed {
			logs.Error("Node %d: TCP TLS Server error: %v", node.ID, err)
		}
	}()

	                                                                                    
	pprofPort := 0
	if p, err := strconv.Atoi(node.Port); err == nil {
		pprofPort = p + 1000                       
	}
	if node.EnablePprof && pprofPort > 0 {
		pprofMux := http.NewServeMux()
		pprofMux.HandleFunc("/debug/pprof/", pprof.Index)
		pprofMux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		pprofMux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		pprofMux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		pprofMux.HandleFunc("/debug/pprof/trace", pprof.Trace)
		pprofMux.HandleFunc("/debug/pprof/heap", pprof.Handler("heap").ServeHTTP)

		bindHost := strings.TrimSpace(node.PprofBind)
		if bindHost == "" {
			bindHost = "127.0.0.1"
		}
		pprofAddr := net.JoinHostPort(bindHost, strconv.Itoa(pprofPort))
		pprofServer := &http.Server{
			Addr:              pprofAddr,
			Handler:           wrapPprofHandler(pprofMux, 1),
			ReadHeaderTimeout: serverCfg.HTTPReadHeaderTimeout,
			IdleTimeout:       serverCfg.HTTPIdleTimeout,
			MaxHeaderBytes:    serverCfg.MaxHeaderBytes,
		}
		pprofListener, err := net.Listen("tcp", pprofAddr)
		if err != nil {
			logs.Error("Node %d: pprof HTTP listen error: %v", node.ID, err)
		} else {
			node.PProfServer = pprofServer
			defer pprofListener.Close()
			defer pprofServer.Close()
			pprofConnections := newConnectionGuard(2, 2, nil)
			go func() {
				logs.Info("Node %d: Starting pprof HTTP server on %s", node.ID, pprofAddr)
				limitedListener := &guardedListener{Listener: pprofListener, guard: pprofConnections}
				if err := pprofServer.Serve(limitedListener); err != nil && err != http.ErrServerClosed {
					logs.Error("Node %d: pprof HTTP Server error: %v", node.ID, err)
				}
			}()
		}
	}

	                                          
	if err := server.ServeListener(listener); err != nil {
		if isServerClosedErr(err) {
			return nil
		}
		logs.Error("Node %d: HTTP/3 Server error: %v", node.ID, err)
		return err
	}

	return nil
}

                                                                             
                                                     
func registerSimulatorControlRoute(mux *http.ServeMux, node *NodeInstance) {
	if mux == nil || node == nil || !node.EnableOperatorControlAPI {
		return
	}
	mux.HandleFunc("/simulator/config", requireExplorerServiceToken(func(w http.ResponseWriter, r *http.Request) {
		if globalSimulator == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintf(w, `{"error":"simulator not running"}`)
			return
		}
		globalSimulator.HandleSimulatorConfig(w, r)
	}))
}

                     
func shutdownAllNodes(nodes []*NodeInstance) {
	for _, node := range nodes {
		if node == nil {
			continue
		}
		node.Logger.Info("Stopping node %d...", node.ID)
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
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := node.Server.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
				node.Logger.Warn("Node %d: failed to shutdown TCP server: %v", node.ID, err)
			}
			cancel()
		}
		if node.PProfServer != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			if err := node.PProfServer.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
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
			node.DBManager.Close()
		}

		node.Logger.Info("Node %d stopped.", node.ID)
	}
}

func isServerClosedErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, http.ErrServerClosed) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "server closed") ||
		strings.Contains(msg, "closed network connection") ||
		strings.Contains(msg, "use of closed network connection")
}

                                                                                                                
                                                                        
func postBootstrapInitCertChain(
	node *NodeInstance,
	cfg *config.Config,
	mode BootstrapMode,
	shouldBootstrap bool,
	genesisPeers []PeerInfo,
	genesisCfg *config.GenesisConfig,
) error {
	if node == nil || node.ConsensusManager == nil || node.ConsensusManager.Node == nil {
		return fmt.Errorf("consensus node unavailable")
	}
	rs, ok := node.ConsensusManager.Node.GetBlockStore().(*consensus.RealBlockStore)
	if !ok {
		return fmt.Errorf("real block store unavailable")
	}
	exec := rs.GetExecutor()
	if exec == nil || exec.LivenessTracker == nil {
		return fmt.Errorf("VM liveness tracker unavailable")
	}
	lt, ok := exec.LivenessTracker.(*liveness.Tracker)
	if !ok || lt == nil {
		return fmt.Errorf("unsupported liveness tracker %T", exec.LivenessTracker)
	}
	dbm := node.DBManager
	                                                                           
	                                                                                   
	                                                                                     
	_, voterCount := lt.GetConsensusVoterSnapshot()
	if voterCount == 0 && shouldBootstrap {
		if err := bootstrapMinersToTracker(lt, dbm); err != nil {
			return err
		}
	} else if voterCount == 0 {
		return fmt.Errorf("persisted liveness registry is missing from resumed state")
	}
	                                                                         
	                                                                           
	                                                                            
	                                                                   
	if count, err := dbm.ReconcileMinerIndexFromLiveness(lt); err != nil {
		return fmt.Errorf("reconcile miner index from persisted liveness: %w", err)
	} else {
		logs.Info("[Liveness] Reconciled %d runtime miner index mappings", count)
	}
	                                      
	if exec.CertChainTracker != nil {
		certTracker, ok := exec.CertChainTracker.(*certchain.Tracker)
		if !ok || certTracker == nil {
			return fmt.Errorf("unsupported cert tracker %T", exec.CertChainTracker)
		}
		if err := bootstrapCertMinerSet(certTracker, lt, dbm, mode, shouldBootstrap, genesisPeers, genesisCfg); err != nil {
			return err
		}
	}
	return nil
}

                                                                                                   
func bootstrapMinersToTracker(tracker *liveness.Tracker, dbm *db.Manager) error {
	if tracker == nil || dbm == nil {
		return fmt.Errorf("liveness bootstrap dependencies are unavailable")
	}
	accts, err := dbm.Scan(keys.KeyAccount(""))
	if err != nil {
		return fmt.Errorf("scan genesis accounts: %w", err)
	}
	type minerSeed struct {
		Address string
		Index   uint64
	}
	miners := make([]minerSeed, 0, len(accts))
	for _, data := range accts {
		var acct pb.Account
		if err := proto.Unmarshal(data, &acct); err != nil {
			return fmt.Errorf("decode genesis account: %w", err)
		}
		if !acct.IsMiner || acct.Address == "" {
			continue
		}
		miners = append(miners, minerSeed{Address: acct.Address, Index: acct.Index})
	}
	sort.Slice(miners, func(i, j int) bool {
		if miners[i].Index != miners[j].Index {
			return miners[i].Index < miners[j].Index
		}
		return miners[i].Address < miners[j].Address
	})

	wv := &liveness.DirectWriter{
		SetFn: func(k string, v []byte) { dbm.EnqueueSet(k, string(v)) },
		DelFn: func(k string) { dbm.EnqueueDel(k) },
	}
	count := 0
	for _, m := range miners {
		balData, err := dbm.Get(keys.KeyBalance(m.Address, "FB"))
		if err != nil || len(balData) == 0 {
			return fmt.Errorf("genesis miner %s has no FB balance: %w", m.Address, err)
		}
		var rec pb.TokenBalanceRecord
		if err := proto.Unmarshal(balData, &rec); err != nil || rec.Balance == nil {
			return fmt.Errorf("genesis miner %s has an invalid FB balance", m.Address)
		}
		stake, err := strconv.ParseUint(rec.Balance.MinerLockedBalance, 10, 64)
		if err != nil {
			return fmt.Errorf("genesis miner %s has invalid miner_locked_balance: %w", m.Address, err)
		}
		if stake < vm.MinMinerStake.Uint64() {
			return fmt.Errorf("genesis miner %s stake %d is below minimum %s", m.Address, stake, vm.MinMinerStake)
		}
		idx, err := tracker.RegisterMinerAtHeight(wv, m.Address, stake, 0)                      
		if err != nil {
			return fmt.Errorf("register genesis miner %s: %w", m.Address, err)
		}
		if uint64(idx) != m.Index {
			return fmt.Errorf("genesis miner %s slot mismatch: account=%d liveness=%d", m.Address, m.Index, idx)
		}
		count++
	}
	if count == 0 {
		return fmt.Errorf("no genesis miners found")
	}
	logs.Info("[Liveness] Bootstrapped %d genesis miners as Active", count)
	return nil
}

                                                                         
                                                                             
                                                                            
                                                                      
func bootstrapCertMinerSet(
	certTracker *certchain.Tracker,
	lt *liveness.Tracker,
	dbm *db.Manager,
	mode BootstrapMode,
	shouldBootstrap bool,
	genesisPeers []PeerInfo,
	genesisCfg *config.GenesisConfig,
) error {
	if certTracker == nil || lt == nil || dbm == nil {
		return fmt.Errorf("certificate bootstrap dependencies are unavailable")
	}
	if len(certTracker.GetGenesisMinerSet()) > 0 {
		if len(certTracker.GetPrevMinerPubKeys()) == 0 {
			return fmt.Errorf("persisted certificate trust anchor has no current signer set")
		}
		return nil
	}
	if !shouldBootstrap {
		return fmt.Errorf("persisted genesis certificate validator set is missing")
	}

	if mode == LateJoin {
		snapshots, err := buildTrustedGenesisCertMinerSet(genesisPeers, genesisCfg)
		if err != nil {
			return fmt.Errorf("derive locally trusted genesis certificate validator set: %w", err)
		}
		if err := certTracker.UpdateMinerSet(snapshots); err != nil {
			return fmt.Errorf("install locally trusted genesis certificate anchor: %w", err)
		}
		logs.Info("[CertChain] Installed locally trusted genesis Active set with %d miners", len(snapshots))
		return nil
	}

	active, err := vm.BuildCertificateActiveMiners(lt, vm.NewStateView(dbm.Get, dbm.Scan))
	if err != nil {
		return fmt.Errorf("derive certificate bootstrap Active set: %w", err)
	}
	snapshots := make([]certchain.MinerSnapshot, len(active))
	for i, miner := range active {
		snapshots[i] = certchain.MinerSnapshot{
			Index:           miner.Index,
			Address:         miner.Address,
			PubKey:          append([]byte(nil), miner.PublicKey...),
			EffectiveWeight: miner.EffectiveWeight,
		}
	}
	if err := certTracker.UpdateMinerSet(snapshots); err != nil {
		return fmt.Errorf("install certificate bootstrap trust anchor: %w", err)
	}
	logs.Info("[CertChain] Bootstrapped complete strict Active set with %d miners", len(snapshots))
	return nil
}

                                                                             
                                                                    
                                                                              
                                                           
func buildTrustedGenesisCertMinerSet(peers []PeerInfo, genesisCfg *config.GenesisConfig) ([]certchain.MinerSnapshot, error) {
	if genesisCfg == nil {
		return nil, fmt.Errorf("genesis config is unavailable")
	}
	if len(peers) == 0 {
		return nil, fmt.Errorf("peers.json must contain the complete genesis validator manifest")
	}

	snapshots := make([]certchain.MinerSnapshot, 0, len(peers))
	for _, configured := range peers {
		if !configured.IsMiner {
			continue
		}
		peer, err := normalizePeerIdentity(configured)
		if err != nil {
			return nil, err
		}
		if peer.Index < 0 || uint64(peer.Index) > uint64(^uint32(0)) {
			return nil, fmt.Errorf("genesis miner index %d is outside uint32 range", peer.Index)
		}
		if peer.Address == "" || len(peer.BLSPublicKey) == 0 {
			return nil, fmt.Errorf("genesis miner %d requires canonical address and BLS public key", peer.Index)
		}

		stake, err := genesisMinerStake(genesisCfg, peer.Address)
		if err != nil {
			return nil, fmt.Errorf("genesis miner %d (%s): %w", peer.Index, peer.Address, err)
		}
		weight := stake.Uint64()

		snapshots = append(snapshots, certchain.MinerSnapshot{
			Index:           uint32(peer.Index),
			Address:         peer.Address,
			PubKey:          append([]byte(nil), peer.BLSPublicKey...),
			EffectiveWeight: weight,
		})
	}
	if len(snapshots) == 0 {
		return nil, fmt.Errorf("peers.json contains no genesis miners")
	}
	sort.Slice(snapshots, func(i, j int) bool {
		if snapshots[i].Index != snapshots[j].Index {
			return snapshots[i].Index < snapshots[j].Index
		}
		return snapshots[i].Address < snapshots[j].Address
	})
	return snapshots, nil
}
