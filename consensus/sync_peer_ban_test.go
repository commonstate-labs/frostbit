package consensus

import (
	"dex/logs"
	"dex/types"
	"testing"
)

                                                         
                                              
                                    
                                               
                                                                             
  
                                                                                             
                                                   

func newBanTestSyncManager() *SyncManager {
	sm := &SyncManager{
		nodeID:      "self",
		Logger:      logs.NewNodeLogger("ban-test", 0),
		PeerHeights: map[types.NodeID]uint64{},
	}
	sm.peerRep = sm.newPeerBanTracker()
	return sm
}

func TestRecordPeerFailure_SkipsSelfAndEmpty(t *testing.T) {
	sm := newBanTestSyncManager()

	for i := 0; i < peerBanFailThreshold*2; i++ {
		sm.recordPeerFailure("", "x")                         
		sm.recordPeerFailure("self", "x")                    
	}
	if sm.isPeerBanned("self") || sm.isPeerBanned("") {
		t.Fatal("self/empty must never be banned by recordPeerFailure")
	}
}

func TestRecordPeerFailure_BansAtThresholdAndOnBanCleansPeerHeights(t *testing.T) {
	sm := newBanTestSyncManager()
	peer := types.NodeID("p1")

	                                                       
	sm.PeerHeights[peer] = 100

	for i := 0; i < peerBanFailThreshold; i++ {
		sm.recordPeerFailure(peer, "dirty")
	}
	if !sm.isPeerBanned(peer) {
		t.Fatalf("peer must be banned after %d failures", peerBanFailThreshold)
	}
	if _, ok := sm.PeerHeights[peer]; ok {
		t.Fatal("onBan callback must remove peer from PeerHeights to stop scheduling")
	}
}

func TestResetPeerFailure_ClearsAccumulatedCount(t *testing.T) {
	sm := newBanTestSyncManager()
	peer := types.NodeID("p2")

	for i := 0; i < peerBanFailThreshold-1; i++ {
		sm.recordPeerFailure(peer, "x")
	}
	sm.resetPeerFailure(peer)
	                                     
	sm.recordPeerFailure(peer, "x")
	if sm.isPeerBanned(peer) {
		t.Fatal("after reset, single failure must not ban")
	}
}

func TestFilterBannedPeers_RemovesBannedPreservesHonest(t *testing.T) {
	sm := newBanTestSyncManager()

	bad := types.NodeID("bad")
	for i := 0; i < peerBanFailThreshold; i++ {
		sm.recordPeerFailure(bad, "x")
	}
	if !sm.isPeerBanned(bad) {
		t.Fatal("bad must be banned")
	}

	got := sm.filterBannedPeers([]types.NodeID{"alice", bad, "bob", "carol"})
	for _, p := range got {
		if p == bad {
			t.Fatalf("filtered list still contains banned peer: %v", got)
		}
	}
	                           
	wantSet := map[types.NodeID]bool{"alice": true, "bob": true, "carol": true}
	for _, p := range got {
		delete(wantSet, p)
	}
	if len(wantSet) != 0 {
		t.Fatalf("missing honest peers in filtered list: %+v (got=%v)", wantSet, got)
	}
}

func TestFilterBannedPeers_EmptyAndNilSafe(t *testing.T) {
	sm := newBanTestSyncManager()
	if got := sm.filterBannedPeers(nil); len(got) != 0 {
		t.Fatalf("nil input must yield empty: %v", got)
	}
	if got := sm.filterBannedPeers([]types.NodeID{}); len(got) != 0 {
		t.Fatalf("empty input must yield empty: %v", got)
	}
}

func TestIsPeerBanned_EmptyKeyAlwaysFalse(t *testing.T) {
	sm := newBanTestSyncManager()
	if sm.isPeerBanned("") {
		t.Fatal("empty peer ID must never report banned")
	}
}
