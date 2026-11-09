package consensus

import (
	"bytes"
	"dex/types"
	"dex/utils"
	"dex/vm"
	"errors"
	"testing"
	"time"
)

func assertSyncPause(t *testing.T, sm *SyncManager, want bool) {
	t.Helper()
	manager := &ConsensusNodeManager{Node: &Node{SyncManager: sm}}
	pressure := manager.SyncPressureState()
	if got := sm.ShouldPauseConsensusForCatchUp(); got != want {
		t.Fatalf("consensus pause=%t, want %t", got, want)
	}
	if pressure.CatchUp != want || pressure.Syncing != want || manager.GetSyncStatus().IsSyncing != want || manager.IsSyncing() != want {
		t.Fatalf("pause/status/transaction pressure diverged: %+v, want pause=%t", pressure, want)
	}
}

func TestUnverifiedSyncProbeNeverPausesConsensusOrTransactions(t *testing.T) {
	store := newFrontierCoreTestStore("block-5", 5)
	transport := &frontierCoreTestTransport{peers: []types.NodeID{"peer-1", "peer-2", "peer-3"}}
	sm := newFrontierCoreSyncManager(store, transport, 3, 2)
	sm.PeerHeights["liar"] = 7
	sm.runSyncPipeline(SyncTriggerChit, 7, 5)
	assertSyncPause(t, sm, false)
	sm.Mu.Lock()
	if sm.activeSession == nil || sm.frontierRound == nil || sm.Syncing {
		sm.Mu.Unlock()
		t.Fatal("height hint must start a non-pausing frontier probe")
	}
	sm.activeSession.StageRetries = maxStageRetries
	expired := sm.frontierRound.startedAt.Add(sm.frontierPollTimeout() + time.Second)
	sm.Mu.Unlock()
	sm.processAcceptedFrontierTimeout(expired)
	assertSyncPause(t, sm, false)
	sm.Mu.RLock()
	defer sm.Mu.RUnlock()
	if sm.activeSession != nil || sm.lastSession == nil || sm.lastSession.Stage != SyncStageFailed {
		t.Fatal("unconfirmed probe must close after its retry budget")
	}
	if sm.PeerHeights["liar"] != 7 {
		t.Fatal("test must retain the malicious hint to prove it has no residual pause authority")
	}
}

func TestFrontierQuorumPausesOnlyUntilItsSessionEnds(t *testing.T) {
	signers := []*utils.KeyManager{newAcceptedFrontierTestSigner(t), newAcceptedFrontierTestSigner(t)}
	peers := []types.NodeID{types.NodeID(signers[0].GetAddress()), types.NodeID(signers[1].GetAddress())}
	transport := &frontierCoreTestTransport{peers: peers}
	sm := newFrontierCoreSyncManager(newFrontierCoreTestStore("block-5", 5), transport, 2, 2)
	sm.runSyncPipeline(SyncTriggerChit, 7, 5)
	assertSyncPause(t, sm, false)
	sm.Mu.RLock()
	query := sm.frontierRound.query
	session := sm.activeSession
	sm.Mu.RUnlock()
	for i, signer := range signers {
		vote := AcceptedFrontierVote{
			Peer: peers[i], RequestDigest: ComputeAcceptedFrontierRequestDigest(query),
			Status: AcceptedFrontierStatusOK, AcceptedHeight: 7, ObservedAnchorID: "block-5",
			TargetHeight: 7, TargetBlockID: "block-7",
		}
		var err error
		vote.Signature, err = signer.Sign(ComputeAcceptedFrontierDigest(query, vote))
		if err != nil {
			t.Fatal(err)
		}
		sm.HandleAcceptedFrontierVote(vote)
		assertSyncPause(t, sm, i == 1)
	}
	deadline := time.Now().Add(time.Second)
	for {
		sm.Mu.RLock()
		requested := len(sm.SyncRequestMeta) > 0
		sm.Mu.RUnlock()
		if requested {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("verified frontier did not request the exact block batch")
		}
		time.Sleep(time.Millisecond)
	}
	sm.finishSyncSession(session, errors.New("supporters unavailable"))
	assertSyncPause(t, sm, false)
	sm.Mu.RLock()
	defer sm.Mu.RUnlock()
	if sm.Syncing || len(sm.SyncRequests) != 0 || len(sm.SyncRequestMeta) != 0 || len(sm.InFlightSyncRanges) != 0 {
		t.Fatal("finishing a session must retire its requests and ranges with its pause authority")
	}
}

func TestCheckpointPauseStartsAfterVerificationAndEndsBeforeTailProbe(t *testing.T) {
	store := newFrontierCoreTestStore("block-5", 5)
	sm := newFrontierCoreSyncManager(store, &frontierCoreTestTransport{}, 2, 2)
	session := sm.startSyncSession(SyncTriggerChit, 100, 5, false)
	assertSyncPause(t, sm, false)
	sm.setSyncSessionStage(session, SyncStageCertChain, "")
	assertSyncPause(t, sm, false)
	sm.updateSyncSession(session, func(s *SyncSession) {
		s.CertifiedHeight = 50
		s.CertStateRoot = bytes.Repeat([]byte{1}, 32)
		s.CertBlockHash = "certified-50"
	})
	sm.setSyncSessionStage(session, SyncStageStateSnapshot, "")
	assertSyncPause(t, sm, true)
	sm.setSyncSessionStage(session, SyncStageFastForward, "")
	assertSyncPause(t, sm, true)
	sm.setSyncSessionStage(session, SyncStageFrontier, "checkpoint tail needs fresh evidence")
	assertSyncPause(t, sm, false)
	if sm.Syncing {
		t.Fatal("checkpoint evidence must not pause its unverified tail probe")
	}
	sm.finishSyncSession(session, nil)
	assertSyncPause(t, sm, false)
	if got := sm.verifiedSyncCompletions.Load(); got != 0 {
		t.Fatalf("unfinished frontier probe advanced verified completion generation: %d", got)
	}
}

func TestVerifiedSyncCompletionGenerationAcceptsInstalledBlockCatchUpTarget(t *testing.T) {
	store := newFrontierCoreTestStore("block-5", 5)
	sm := newFrontierCoreSyncManager(store, &frontierCoreTestTransport{}, 2, 2)
	var observed VerifiedSyncPoint
	sm.SetVerifiedSyncObserver(func(point VerifiedSyncPoint) error {
		observed = point
		return nil
	})
	session := sm.startSyncSession(SyncTriggerChit, 5, 4, false)
	sm.updateSyncSession(session, func(s *SyncSession) {
		                                                                      
		                                                                    
		s.Stage = SyncStageBlockCatchUp
		s.TargetHeight = 5
	})
	sm.finishSyncSession(session, nil)
	if got := sm.verifiedSyncCompletions.Load(); got != 1 {
		t.Fatalf("verified completion generation=%d want=1", got)
	}
	if got := sm.verifiedSyncHeight.Load(); got != 5 {
		t.Fatalf("verified sync height=%d want=5", got)
	}
	if observed.Height != 5 || observed.BlockID != "block-5" {
		t.Fatalf("observer point=%+v want block-5 at height 5", observed)
	}
	                                                                             
	                                                                  
	sm.PeerHeights["liar"] = 1_000_000
	status := (&ConsensusNodeManager{Node: &Node{SyncManager: sm}}).GetSyncStatus()
	if status.SyncTargetHeight != 1_000_000 || status.VerifiedSyncHeight != 5 || status.VerifiedSyncCompletions != 1 {
		t.Fatalf("raw hint contaminated verified status: %+v", status)
	}
}

func TestVerifiedSyncCompletionGenerationRejectsObserverFailure(t *testing.T) {
	store := newFrontierCoreTestStore("block-5", 5)
	sm := newFrontierCoreSyncManager(store, &frontierCoreTestTransport{}, 2, 2)
	observed := false
	sm.SetVerifiedSyncObserver(func(point VerifiedSyncPoint) error {
		observed = true
		if point.Height != 5 || point.BlockID != "block-5" {
			t.Fatalf("observer point=%+v want block-5 at height 5", point)
		}
		return errors.New("durability gate rejected target")
	})
	session := sm.startSyncSession(SyncTriggerChit, 5, 4, false)
	sm.updateSyncSession(session, func(s *SyncSession) {
		s.Stage = SyncStageBlockCatchUp
		s.TargetHeight = 5
	})
	sm.finishSyncSession(session, nil)
	if !observed {
		t.Fatal("verified-sync observer was not invoked")
	}
	if got := sm.verifiedSyncCompletions.Load(); got != 0 {
		t.Fatalf("observer failure advanced verified completion generation: %d", got)
	}
	if got := sm.verifiedSyncHeight.Load(); got != 0 {
		t.Fatalf("observer failure advanced verified sync height: %d", got)
	}
}

func TestVerifiedSyncCompletionGenerationRejectsUninstalledTarget(t *testing.T) {
	store := newFrontierCoreTestStore("block-4", 4)
	sm := newFrontierCoreSyncManager(store, &frontierCoreTestTransport{}, 2, 2)
	session := sm.startSyncSession(SyncTriggerChit, 5, 4, false)
	sm.updateSyncSession(session, func(s *SyncSession) {
		s.Stage = SyncStageBlockCatchUp
		s.TargetHeight = 5
	})
	sm.finishSyncSession(session, nil)
	if got := sm.verifiedSyncCompletions.Load(); got != 0 {
		t.Fatalf("uninstalled target advanced verified completion generation: %d", got)
	}
	if got := sm.verifiedSyncHeight.Load(); got != 0 {
		t.Fatalf("uninstalled target advanced verified sync height: %d", got)
	}
}

func TestFrontierPauseEndsImmediatelyWhenLocalAnchorAdvances(t *testing.T) {
	store := newFrontierCoreTestStore("block-5", 5)
	sm := newFrontierCoreSyncManager(store, &frontierCoreTestTransport{}, 2, 2)
	sm.activeSession = &SyncSession{
		Stage:                SyncStageBlockCatchUp,
		FrontierAnchorHeight: 5, FrontierAnchorID: "block-5",
		FrontierTargetHeight: 7, FrontierTargetID: "block-7",
		frontierNonce:      bytes.Repeat([]byte{1}, AcceptedFrontierNonceSize),
		frontierSupporters: map[types.NodeID]struct{}{"peer-1": {}, "peer-2": {}},
	}
	assertSyncPause(t, sm, true)
	store.mu.Lock()
	store.lastID, store.lastHeight = "block-6", 6
	store.mu.Unlock()
	assertSyncPause(t, sm, false)
	if err := sm.execBlockCatchUpStage(sm.activeSession); err != nil {
		t.Fatal(err)
	}
	if sm.activeSession.Stage != SyncStageFrontier || sm.Syncing {
		t.Fatal("advanced local anchor must clear authorization and return to non-pausing sampling")
	}
}

func TestLightFrontierAndOrphanedFlagsCannotPause(t *testing.T) {
	sm := newFrontierCoreSyncManager(newFrontierCoreTestStore("block-5", 5), nil, 2, 2)
	sm.Syncing = true
	sm.SyncRequests[1] = time.Now()
	assertSyncPause(t, sm, false)
	sm.activeSession = &SyncSession{
		Light: true, Stage: SyncStageBlockCatchUp,
		FrontierAnchorHeight: 5, FrontierAnchorID: "block-5",
		FrontierTargetHeight: 6, FrontierTargetID: "block-6",
		frontierNonce:      bytes.Repeat([]byte{1}, AcceptedFrontierNonceSize),
		frontierSupporters: map[types.NodeID]struct{}{"peer-1": {}, "peer-2": {}},
	}
	assertSyncPause(t, sm, false)
}

func TestSnapshotSwitchExcludesWholeFinalizationIncludingPostVMWork(t *testing.T) {
	block := &types.Block{ID: "block-5", Header: types.BlockHeader{Height: 5}}
	store := &RealBlockStore{
		vmExecutor:        &vm.Executor{},
		finalizedBlocks:   map[uint64]*types.Block{5: block},
		finalizationChits: make(map[uint64]*types.FinalizationChits),
	}
	sm := &SyncManager{store: store}
	if !sm.lockSnapshotSwitch() {
		t.Fatal("snapshot switch did not acquire the real store locks")
	}
	locked := true
	defer func() {
		if locked {
			sm.unlockSnapshotSwitch()
		}
	}()
	done := make(chan error, 1)
	go func() { done <- store.SetFinalized(5, block.ID) }()
	select {
	case err := <-done:
		t.Fatalf("finalization mutated its non-VM indexes while snapshot switch was held: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	sm.unlockSnapshotSwitch()
	locked = false
	select {
	case err := <-done:
		if !errors.Is(err, ErrAlreadyFinalized) {
			t.Fatalf("finalization failed after snapshot released: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("finalization did not recover after snapshot released")
	}
}
