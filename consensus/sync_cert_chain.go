package consensus

import (
	"bytes"
	"dex/certchain"
	"dex/keys"
	"dex/pb"
	"dex/types"
	"errors"
	"fmt"
	"sort"
)

type certificateReader interface {
	GetCertificate(height uint64) ([]byte, bool)
}

type certificateOverlapConflictError struct {
	Height       uint64
	LocalDigest  []byte
	RemoteDigest []byte
}

func (e *certificateOverlapConflictError) Error() string {
	if e == nil {
		return "certificate overlap conflict"
	}
	return fmt.Sprintf("verified certificate overlap conflict at height %d: local=%x remote=%x",
		e.Height, e.LocalDigest, e.RemoteDigest)
}

                                                                          
                                                                            
                                                                    
func validateVerifiedCertificateOverlap(local certificateReader, localVerifiedHeight uint64, remote []*pb.CheckpointCertificate) error {
	if local == nil || localVerifiedHeight == 0 {
		return nil
	}
	for _, remoteCert := range remote {
		if remoteCert == nil || remoteCert.Height == 0 || remoteCert.Height > localVerifiedHeight {
			continue
		}
		rawLocal, ok := local.GetCertificate(remoteCert.Height)
		if !ok || len(rawLocal) == 0 {
			return fmt.Errorf("local verified certificate missing at overlapping height %d", remoteCert.Height)
		}
		localCert, err := certchain.ParseCertificate(rawLocal)
		if err != nil {
			return fmt.Errorf("parse local verified certificate at height %d: %w", remoteCert.Height, err)
		}
		if localCert.Height != remoteCert.Height {
			return fmt.Errorf("local certificate key height %d contains certificate height %d", remoteCert.Height, localCert.Height)
		}
		localDigest := certchain.CertDigestFromCert(localCert)
		remoteDigest := certchain.CertDigestFromCert(remoteCert)
		if !bytes.Equal(localDigest, remoteDigest) {
			return &certificateOverlapConflictError{
				Height:       remoteCert.Height,
				LocalDigest:  append([]byte(nil), localDigest...),
				RemoteDigest: append([]byte(nil), remoteDigest...),
			}
		}
	}
	return nil
}

func (sm *SyncManager) guardVerifiedCertificateOverlap(local certificateReader, localVerifiedHeight uint64, remote []*pb.CheckpointCertificate) error {
	err := validateVerifiedCertificateOverlap(local, localVerifiedHeight, remote)
	var conflict *certificateOverlapConflictError
	if errors.As(err, &conflict) {
		sm.invokeSafetyFaultHandler(err)
	}
	return err
}

                                                                            
                                                                              
                                                                       
func (sm *SyncManager) installVerifiedCertificateChain(
	local certificateReader,
	localVerifiedHeight uint64,
	store certchain.Store,
	result *certchain.DownloadAndVerifyResult,
	flush func() error,
) (string, error) {
	if result == nil {
		return "", fmt.Errorf("nil verified certificate chain")
	}
	if err := sm.guardVerifiedCertificateOverlap(local, localVerifiedHeight, result.Certs); err != nil {
		return "", err
	}

	certBlockHash := ""
	for _, cert := range result.Certs {
		if cert == nil || cert.Height != result.VerifiedHeight {
			continue
		}
		if len(cert.BlockHash) > 0 {
			certBlockHash = string(cert.BlockHash)
		}
		break
	}

	certchain.PersistCerts(store, result)
	if flush != nil {
		if err := flush(); err != nil {
			return "", fmt.Errorf("persist complete certificate chain: %w", err)
		}
	}
	return certBlockHash, nil
}

func (sm *SyncManager) loadCertifiedAnchorForHeight(height uint64) (string, []byte, bool) {
	if sm == nil || height == 0 {
		return "", nil, false
	}
	realStore, ok := sm.store.(*RealBlockStore)
	if !ok || realStore == nil || realStore.dbManager == nil {
		return "", nil, false
	}

	raw, err := realStore.dbManager.Get(keys.KeyCertCertificate(height))
	if err != nil || len(raw) == 0 {
		return "", nil, false
	}
	cert, err := certchain.ParseCertificate(raw)
	if err != nil || cert == nil {
		return "", nil, false
	}
	if len(cert.BlockHash) == 0 {
		return "", append([]byte(nil), cert.StateRoot...), false
	}
	return string(cert.BlockHash), append([]byte(nil), cert.StateRoot...), true
}

                                                                                                                                                                                                                                                                                                                                                                                  
func (sm *SyncManager) selectStateSyncPeers(targetHeight uint64, maxPeers int) []types.NodeID {
	if maxPeers <= 0 {
		maxPeers = 1
	}

	type peerHeight struct {
		id     types.NodeID
		height uint64
	}

	                                                                                    
	sm.Mu.RLock()
	knownPeerCount := len(sm.PeerHeights)
	candidates := make([]peerHeight, 0, len(sm.PeerHeights))
	for peerID, h := range sm.PeerHeights {
		if peerID == "" || peerID == sm.nodeID {
			continue
		}
		if sm.peerRep.IsBanned(string(peerID)) {
			continue
		}
		if targetHeight == 0 || h >= targetHeight {
			candidates = append(candidates, peerHeight{id: peerID, height: h})
		}
	}
	sm.Mu.RUnlock()

	                                                                        
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].height == candidates[j].height {
			return candidates[i].id < candidates[j].id
		}
		return candidates[i].height > candidates[j].height
	})

	                           
	selected := make([]types.NodeID, 0, maxPeers)
	seen := make(map[types.NodeID]struct{}, maxPeers)
	for _, c := range candidates {
		if _, exists := seen[c.id]; exists {
			continue
		}
		seen[c.id] = struct{}{}
		selected = append(selected, c.id)
		if len(selected) >= maxPeers {
			return selected
		}
	}

	                                                                           
	extraPeers := sm.filterBannedPeers(sm.transport.SamplePeers(sm.nodeID, maxPeers*2))
	for _, peer := range extraPeers {
		if peer == "" || peer == sm.nodeID {
			continue
		}
		if _, exists := seen[peer]; exists {
			continue
		}
		seen[peer] = struct{}{}
		selected = append(selected, peer)
		if len(selected) >= maxPeers {
			break
		}
	}
	if len(selected) == 0 {
		sm.logDeepSyncTrace("peer_select", "no eligible peers selected (targetHeight=%d maxPeers=%d knownPeers=%d)", targetHeight, maxPeers, knownPeerCount)
	}
	return selected
}

                                                                     
                                                                  
                                                   
  
            
                                  
                                                                                                               
                                                                                                  
                                                 
  
                                                                                                                           
func (sm *SyncManager) downloadAndVerifyCertChain(peerAcceptedHeight, localAccepted uint64) (uint64, []byte, string, error) {
	                                            
	realStore, ok := sm.store.(*RealBlockStore)
	if !ok || realStore == nil {
		sm.logDeepSyncAbort("cert_chain", "store does not support cert chain verify, store=%T", sm.store)
		return 0, nil, "", nil
	}
	exec := realStore.GetExecutor()
	if exec == nil || exec.CertChainTracker == nil {
		sm.logDeepSyncAbort("cert_chain", "executor/cert tracker unavailable (execNil=%v trackerNil=%v)", exec == nil, exec == nil || exec.CertChainTracker == nil)
		return 0, nil, "", nil
	}

	interval := exec.CertChainTracker.GetCheckpointInterval()
	if interval == 0 {
		sm.logDeepSyncAbort("cert_chain", "checkpoint interval is 0, cannot derive cert target")
		return 0, nil, "", nil
	}
	                                                         
	maxCertTarget := (peerAcceptedHeight / interval) * interval
	sm.logDeepSyncTrace("cert_chain", "computed cert target: peerAccepted=%d localAccepted=%d interval=%d maxCertTarget=%d", peerAcceptedHeight, localAccepted, interval, maxCertTarget)

	                     
	                                                    
	                                                                                   
	var (
		localCertifiedHeight uint64
		localStateRoot       []byte
		localCertBlockHash   string
		hasLocalCertified    bool
	)
	if h, sr, found := certchain.FindLocalCertifiedHeight(exec.CertChainTracker, maxCertTarget, localAccepted, interval); found {
		hasLocalCertified = true
		localCertifiedHeight = h
		localStateRoot = sr
		if rawCert, ok := exec.CertChainTracker.GetCertificate(h); ok && len(rawCert) > 0 {
			if cert, err := certchain.ParseCertificate(rawCert); err == nil && cert != nil && len(cert.BlockHash) > 0 {
				localCertBlockHash = string(cert.BlockHash)
			}
		}
		if h >= maxCertTarget {
			sm.logDeepSyncTrace("cert_chain", "local certified reaches target (localCertified=%d target=%d), still verify remote to avoid same-height fork drift", h, maxCertTarget)
			sm.Logger.Info("[SyncPipeline] CertChain: local certified checkpoint %d reaches target %d, still verifying remote cert for chain anchor", h, maxCertTarget)
		} else {
			sm.Logger.Info("[SyncPipeline] CertChain: local certified checkpoint %d found (< target=%d), trying remote for newer cert", h, maxCertTarget)
		}
	}

	                                  
	fetcher, ok := sm.transport.(stateSnapshotFetcher)
	if !ok {
		if hasLocalCertified {
			sm.logDeepSyncTrace("cert_chain", "transport missing FetchCertChain, fallback to local certified checkpoint=%d", localCertifiedHeight)
			sm.Logger.Warn("[SyncPipeline] CertChain: transport does not support FetchCertChain, fallback to local certified checkpoint %d", localCertifiedHeight)
			return localCertifiedHeight, localStateRoot, localCertBlockHash, nil
		}
		sm.logDeepSyncAbort("cert_chain", "transport does not support FetchCertChain and no local certified checkpoint")
		sm.Logger.Warn("[SyncPipeline] CertChain: transport does not support FetchCertChain")
		return 0, nil, "", nil
	}

	tracker, trackerOK := exec.CertChainTracker.(*certchain.Tracker)
	if !trackerOK || tracker == nil {
		sm.logDeepSyncAbort("cert_chain", "cert tracker does not expose genesis trust anchor: %T", exec.CertChainTracker)
		return 0, nil, "", nil
	}
	genesisMinerSet := tracker.GetGenesisMinerSet()
	if len(genesisMinerSet) == 0 {
		if hasLocalCertified {
			sm.logDeepSyncTrace("cert_chain", "empty miner set for remote verify, fallback to local certified checkpoint=%d", localCertifiedHeight)
			sm.Logger.Warn("[SyncPipeline] CertChain: empty miner set for remote verify, fallback to local certified checkpoint %d", localCertifiedHeight)
			return localCertifiedHeight, localStateRoot, localCertBlockHash, nil
		}
		sm.logDeepSyncAbort("cert_chain", "empty miner pubkey set and no local certified checkpoint")
		sm.Logger.Warn("[SyncPipeline] CertChain: empty miner set, cannot verify")
		return 0, nil, "", nil
	}

	peers := sm.selectStateSyncPeers(peerAcceptedHeight, 5)
	if len(peers) == 0 {
		if hasLocalCertified {
			sm.logDeepSyncTrace("cert_chain", "no remote peers, fallback to local certified checkpoint=%d", localCertifiedHeight)
			sm.Logger.Warn("[SyncPipeline] CertChain: no peers available, fallback to local certified checkpoint %d", localCertifiedHeight)
			return localCertifiedHeight, localStateRoot, localCertBlockHash, nil
		}
		sm.logDeepSyncAbort("cert_chain", "no peers available and no local certified checkpoint")
		sm.Logger.Warn("[SyncPipeline] CertChain: no peers available")
		return 0, nil, "", nil
	}

	                                                                                                
	fetchers := make([]certchain.CertFetcher, len(peers))
	for i, peer := range peers {
		p := peer           
		fetchers[i] = func(from, to, intv uint64) ([][]byte, error) {
			sm.Logger.Info("[SyncPipeline] CertChain: downloading from peer %s (range %d-%d)", p, from, to)
			return fetcher.FetchCertChain(p, from, to, intv)
		}
	}

	                                           
	result, err := certchain.DownloadAndVerify(fetchers, genesisMinerSet, maxCertTarget, interval, 0.667)
	if err != nil {
		if hasLocalCertified {
			sm.logDeepSyncTrace("cert_chain", "remote download+verify failed, fallback to local certified checkpoint=%d err=%v", localCertifiedHeight, err)
			sm.Logger.Warn("[SyncPipeline] CertChain: download+verify failed (%v), fallback to local certified checkpoint %d", err, localCertifiedHeight)
			return localCertifiedHeight, localStateRoot, localCertBlockHash, nil
		}
		sm.logDeepSyncAbort("cert_chain", "download+verify failed and no local certified checkpoint: %v", err)
		sm.Logger.Warn("[SyncPipeline] CertChain: download+verify failed: %v", err)
		return 0, nil, "", nil
	}
	if overlapErr := sm.guardVerifiedCertificateOverlap(tracker, localCertifiedHeight, result.Certs); overlapErr != nil {
		sm.logDeepSyncAbort("cert_chain", "verified remote chain conflicts with local certified chain: %v", overlapErr)
		return 0, nil, "", overlapErr
	}

	if result.VerifiedHeight <= localAccepted {
		if hasLocalCertified {
			sm.logDeepSyncTrace("cert_chain", "remote verified height=%d <= local=%d, fallback to local certified checkpoint=%d", result.VerifiedHeight, localAccepted, localCertifiedHeight)
			sm.Logger.Warn("[SyncPipeline] CertChain: remote verified height %d <= local %d, fallback to local certified checkpoint %d", result.VerifiedHeight, localAccepted, localCertifiedHeight)
			return localCertifiedHeight, localStateRoot, localCertBlockHash, nil
		}
		sm.logDeepSyncAbort("cert_chain", "remote verified height=%d <= local=%d and no local certified checkpoint", result.VerifiedHeight, localAccepted)
		sm.Logger.Warn("[SyncPipeline] CertChain: verified height %d <= local %d", result.VerifiedHeight, localAccepted)
		return 0, nil, "", nil
	}

	                                                                                    
	if hasLocalCertified {
		if result.VerifiedHeight < localCertifiedHeight {
			sm.logDeepSyncTrace("cert_chain", "remote checkpoint older (remote=%d local=%d), keep local", result.VerifiedHeight, localCertifiedHeight)
			sm.Logger.Info("[SyncPipeline] CertChain: remote verified checkpoint %d older than local %d, keep local", result.VerifiedHeight, localCertifiedHeight)
			return localCertifiedHeight, localStateRoot, localCertBlockHash, nil
		}
		if result.VerifiedHeight == localCertifiedHeight {
			sm.logDeepSyncTrace("cert_chain", "remote checkpoint has same canonical certificate as local (height=%d), keep local", result.VerifiedHeight)
			sm.Logger.Info("[SyncPipeline] CertChain: remote verified checkpoint %d equals local, keep local", result.VerifiedHeight)
			return localCertifiedHeight, localStateRoot, localCertBlockHash, nil
		}
	}

	certBlockHash, err := sm.installVerifiedCertificateChain(
		tracker,
		localCertifiedHeight,
		realStore.dbManager,
		result,
		realStore.dbManager.ForceFlushHot,
	)
	if err != nil {
		sm.logDeepSyncAbort("cert_chain", "install verified certificate chain failed: %v", err)
		return 0, nil, "", err
	}
	sm.logCertRootPeerAnchors(fetcher, peers, result.VerifiedHeight, result.StateRoot, "cert_chain_result")
	if certBlockHash == "" {
		sm.logDeepSyncTrace("cert_chain", "verified cert at height=%d has empty block hash", result.VerifiedHeight)
	}

	return result.VerifiedHeight, result.StateRoot, certBlockHash, nil
}

                                                                                
                                                                            
                                                                               
func restoreCertTrackerAfterSnapshot(tracker interface {
	certificateReader
	GetCheckpointInterval() uint64
	SyncPendingFromCertificateChain([]*pb.CheckpointCertificate) error
}, height uint64) error {
	interval := tracker.GetCheckpointInterval()
	if interval == 0 || height == 0 || height%interval != 0 {
		return fmt.Errorf("invalid checkpoint height %d or interval %d", height, interval)
	}
	var certs []*pb.CheckpointCertificate
	for next := interval; ; next += interval {
		raw, ok := tracker.GetCertificate(next)
		if !ok {
			return fmt.Errorf("verified certificate %d unavailable after snapshot", next)
		}
		cert, err := certchain.ParseCertificate(raw)
		if err != nil || cert.Height != next {
			return fmt.Errorf("invalid verified certificate at height %d: %v", next, err)
		}
		certs = append(certs, cert)
		if next == height {
			break
		}
	}
	return tracker.SyncPendingFromCertificateChain(certs)
}
