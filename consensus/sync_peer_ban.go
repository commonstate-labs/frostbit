package consensus

import (
	"dex/types"
	"dex/utils/peerrep"
	"time"
)

                                               
                                                        
                                                                                                                                 
                                                                          
                                                                  
                                                                                                     
                                               

const (
	                                                
	peerBanFailThreshold = 5
	                                  
	peerBanDuration = 60 * time.Second
)

                                                                                                               
func (sm *SyncManager) newPeerBanTracker() *peerrep.Tracker {
	tr := peerrep.New(peerBanFailThreshold, peerBanDuration)
	tr.SetOnBan(func(key, reason string, count int) {
		sm.Mu.Lock()
		delete(sm.PeerHeights, types.NodeID(key))
		sm.Mu.Unlock()
		sm.Logger.Warn("[SyncManager] Peer %s banned for %s after %d dirty responses: %s",
			key, peerBanDuration, peerBanFailThreshold, reason)
	})
	return tr
}

                                                                                                          
func (sm *SyncManager) recordPeerFailure(peer types.NodeID, reason string) {
	if peer == "" || peer == sm.nodeID {
		return
	}
	if sm.peerRep == nil {
		sm.peerRep = sm.newPeerBanTracker()
	}
	banned, count := sm.peerRep.RecordFailure(string(peer), reason)
	if !banned {
		sm.Logger.Debug("[SyncManager] Peer %s failure recorded (%d/%d): %s",
			peer, count, peerBanFailThreshold, reason)
	}
}

                                                                          
func (sm *SyncManager) resetPeerFailure(peer types.NodeID) {
	if sm.peerRep == nil {
		return
	}
	sm.peerRep.Reset(string(peer))
}

                                                          
func (sm *SyncManager) isPeerBanned(peer types.NodeID) bool {
	if peer == "" {
		return false
	}
	if sm.peerRep == nil {
		return false
	}
	return sm.peerRep.IsBanned(string(peer))
}

                                                                     
func (sm *SyncManager) filterBannedPeers(peers []types.NodeID) []types.NodeID {
	if len(peers) == 0 {
		return peers
	}
	out := peers[:0:len(peers)]
	for _, p := range peers {
		if sm.isPeerBanned(p) {
			continue
		}
		out = append(out, p)
	}
	return out
}
