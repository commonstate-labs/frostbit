package consensus

import (
	"bytes"
	"dex/types"
	"testing"
)

func newSyncPressureManager(localHeight uint64, peerHeights map[types.NodeID]uint64, syncing bool) *ConsensusNodeManager {
	store := &stubBlockStore{lastID: "local", lastHeight: localHeight, blocks: map[string]*types.Block{}}
	sm := &SyncManager{
		store:       store,
		config:      &SyncConfig{DeepLagStateSyncThreshold: 10},
		PeerHeights: peerHeights,
		Syncing:     syncing,
	}
	if syncing {
		sm.activeSession = &SyncSession{
			Stage: SyncStageStateSnapshot, CertifiedHeight: localHeight + 10,
			CertStateRoot: bytes.Repeat([]byte{1}, 32), CertBlockHash: "certified-block",
		}
	}
	return &ConsensusNodeManager{Node: &Node{SyncManager: sm}}
}

func TestSyncPressureStateCatchUpWhenSyncing(t *testing.T) {
	m := newSyncPressureManager(100, map[types.NodeID]uint64{"peer": 100}, true)

	got := m.SyncPressureState()
	if !got.CatchUp || got.Reason != "syncing" {
		t.Fatalf("syncing node must report catch-up pressure, got %+v", got)
	}
}

func TestSyncPressureStateIgnoresUnverifiedLag(t *testing.T) {
	m := newSyncPressureManager(100, map[types.NodeID]uint64{"peer": 111}, false)

	got := m.SyncPressureState()
	if got.CatchUp || got.Syncing || got.Reason != "" || got.Lag != 11 {
		t.Fatalf("height hints remain observable but must not trigger transaction backpressure, got %+v", got)
	}
}

func TestSyncPressureStateNormalWithinThreshold(t *testing.T) {
	m := newSyncPressureManager(100, map[types.NodeID]uint64{"peer": 110}, false)

	got := m.SyncPressureState()
	if got.CatchUp || got.Lag != 10 {
		t.Fatalf("node within threshold must stay normal, got %+v", got)
	}
}
