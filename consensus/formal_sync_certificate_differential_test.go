package consensus

import (
	"dex/formal/itftrace"
	"dex/keys"
	"dex/pb"
	"errors"
	"os"
	"testing"
)

func TestFormalSyncCertificateITFDifferential(t *testing.T) {
	path := os.Getenv("DEX_FORMAL_ITF_SYNC")
	if path == "" {
		t.Skip("DEX_FORMAL_ITF_SYNC is set by formal/run-go-differential.ps1")
	}
	trace, err := itftrace.Load(path)
	if err != nil {
		t.Fatalf("load Quint ITF trace: %v", err)
	}
	if len(trace.States) != 3 {
		t.Fatalf("sync differential trace states=%d want=3", len(trace.States))
	}
	if target := mustSyncFormalInt(t, trace.States[1], "frontierTarget"); target == 0 {
		t.Fatal("model trace did not establish frontier authority before conflict")
	}
	if halted := mustSyncFormalBool(t, trace.States[2], "safetyHalt"); !halted {
		t.Fatal("model trace did not halt on overlap conflict")
	}

	blockStore := newFrontierCoreTestStore("block-1", 1)
	sm := newFrontierCoreSyncManager(blockStore, &frontierCoreTestTransport{}, 3, 2)
	session := sm.startSyncSession(SyncTriggerChit, 2, 1, false)
	if session == nil {
		t.Fatal("production sync session was not created")
	}
	sm.Mu.Lock()
	sm.frontierRound = &frontierRound{
		sessionID: session.ID,
		query: AcceptedFrontierQuery{
			AnchorHeight:  1,
			AnchorBlockID: "block-1",
			TargetHeight:  2,
		},
	}
	sm.Mu.Unlock()

	local := certOverlapTestCertificate(10, 0, "branch-a-block", "branch-a-root")
	remote := certOverlapTestCertificate(10, 0, "branch-b-block", "branch-b-root")
	certStore := newCertOverlapTestStore()
	certStore.values[keys.KeyCertCertificate(local.Height)] = mustMarshalCertOverlapTest(t, local)
	var safetyErr error
	sm.SetSafetyFaultHandler(func(err error) { safetyErr = err })
	conflictErr := sm.guardVerifiedCertificateOverlap(certStore, local.Height, []*pb.CheckpointCertificate{remote})
	var conflict *certificateOverlapConflictError
	if !errors.As(conflictErr, &conflict) || !errors.As(safetyErr, &conflict) {
		t.Fatalf("production did not project model overlap conflict: err=%v safety=%v", conflictErr, safetyErr)
	}
	sm.finishSyncSession(session, conflictErr)

	sm.Mu.RLock()
	active := sm.activeSession
	round := sm.frontierRound
	last := sm.lastSession
	sm.Mu.RUnlock()
	if active != nil || round != nil {
		t.Fatalf("model cleared frontier target but production retained active=%v round=%v", active != nil, round != nil)
	}
	if last == nil || last.Stage != SyncStageFailed {
		t.Fatalf("production session did not fail closed: %+v", last)
	}
	_, acceptedHeight := blockStore.GetLastAccepted()
	if modelHeight := mustSyncFormalInt(t, trace.States[2], "acceptedHeight"); uint64(modelHeight) != acceptedHeight {
		t.Fatalf("conflict changed accepted height: model=%d production=%d", modelHeight, acceptedHeight)
	}
}

func mustSyncFormalInt(t *testing.T, state itftrace.State, field string) int64 {
	t.Helper()
	value, err := state.Int(field)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustSyncFormalBool(t *testing.T, state itftrace.State, field string) bool {
	t.Helper()
	value, err := state.Bool(field)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
