package consensus

import (
	"sort"
)

                                                                             
                                                                              
                                                                            
                                                                          
                                                                       
func (sm *SyncManager) confirmedDeepHeightLocked(localAccepted uint64) (uint64, bool) {
	if sm == nil {
		return 0, false
	}
	additional := sm.deepLagMinAdditionalPeers()
	if additional >= len(sm.PeerHeights) {
		return 0, false
	}
	heights := make([]uint64, 0, len(sm.PeerHeights))
	threshold := sm.deepLagStateSyncThreshold()
	for peer, height := range sm.PeerHeights {
		if peer == "" || peer == sm.nodeID || sm.isPeerBanned(peer) || height <= localAccepted {
			continue
		}
		if height-localAccepted <= threshold {
			continue
		}
		heights = append(heights, height)
	}
	                                                                      
	                                                                        
	if len(heights) <= additional {
		return 0, false
	}
	sort.Slice(heights, func(i, j int) bool { return heights[i] > heights[j] })
	return heights[additional], true
}

                                                                              
                                                                               
                                                                              
func (sm *SyncManager) highestBoundedHintLocked(localAccepted uint64) (uint64, bool) {
	if sm == nil {
		return 0, false
	}
	threshold := sm.deepLagStateSyncThreshold()
	var highest uint64
	for peer, height := range sm.PeerHeights {
		if peer == "" || peer == sm.nodeID || sm.isPeerBanned(peer) || height <= localAccepted {
			continue
		}
		if height-localAccepted > threshold {
			continue
		}
		if height > highest {
			highest = height
		}
	}
	return highest, highest > localAccepted
}
