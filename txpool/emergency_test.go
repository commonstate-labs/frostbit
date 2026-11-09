package txpool

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"dex/db"
	"dex/interfaces"
	"dex/lifecycle"
	"dex/logs"
	"dex/pb"
)

                                                                                        
func txWithID(id string) *pb.AnyTx {
	return &pb.AnyTx{Content: &pb.AnyTx_CertSignTx{CertSignTx: &pb.CertSignTx{Base: &pb.BaseMessage{TxId: id}}}}
}

func normalTxWithID(id string) *pb.AnyTx {
	return &pb.AnyTx{Content: &pb.AnyTx_Transaction{Transaction: &pb.Transaction{Base: &pb.BaseMessage{TxId: id}}}}
}

func TestPendingAnyTxPriorityTreatsCertSignAsProtocolTx(t *testing.T) {
	if got := pendingAnyTxPriority(txWithID("cert-sign")); got != 0 {
		t.Fatalf("CertSignTx priority = %d, want 0", got)
	}
}

                                                            
                                                                                                 
                                                                                      

type stubTxValidator struct{}

func (v *stubTxValidator) CheckAnyTx(_ *pb.AnyTx) error { return nil }

type stubSyncPressureReader struct {
	state interfaces.SyncPressureState
}

func (r stubSyncPressureReader) SyncPressureState() interfaces.SyncPressureState {
	return r.state
}

func newTestTxPool(t *testing.T) *TxPool {
	t.Helper()
	logger := logs.NewNodeLogger("txpool-emergency-test", 0)
	dbManager, err := db.NewManager(filepath.Join(t.TempDir(), "txpool"), logger)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { dbManager.Close() })

	tp, err := NewTxPool(dbManager, &stubTxValidator{}, "test-node", logger)
	if err != nil {
		t.Fatalf("NewTxPool: %v", err)
	}
	return tp
}

func TestSubmitTx_RejectsWhenEmergencyStopped(t *testing.T) {
	tp := newTestTxPool(t)
	em := lifecycle.New()
	tp.SetEmergencyStop(em)

	em.Stop("p1.7 unit test")

	tx := &pb.AnyTx{}                                                                              
	err := tp.SubmitTx(tx, "127.0.0.1", nil)
	if !errors.Is(err, lifecycle.ErrEmergencyStop) {
		t.Fatalf("SubmitTx must return ErrEmergencyStop when emergency engaged, got %v", err)
	}
}

func TestSubmitTx_AllowsAfterResume(t *testing.T) {
	tp := newTestTxPool(t)
	em := lifecycle.New()
	tp.SetEmergencyStop(em)

	em.Stop("first stop")
	if err := tp.SubmitTx(&pb.AnyTx{}, "127.0.0.1", nil); !errors.Is(err, lifecycle.ErrEmergencyStop) {
		t.Fatalf("expected ErrEmergencyStop, got %v", err)
	}

	em.Resume()
	if err := tp.SubmitTx(&pb.AnyTx{}, "127.0.0.1", nil); errors.Is(err, lifecycle.ErrEmergencyStop) {
		t.Fatal("after Resume, SubmitTx must not return ErrEmergencyStop")
	}
}

                                                                                 
                                                               
func TestSubmitTx_NilEmergencyStopIsTransparent(t *testing.T) {
	tp := newTestTxPool(t)
	                                                                  

	if err := tp.SubmitTx(&pb.AnyTx{}, "127.0.0.1", nil); errors.Is(err, lifecycle.ErrEmergencyStop) {
		t.Fatal("nil EmergencyStop must not cause SubmitTx to return ErrEmergencyStop")
	}
}

                                                 
func TestSubmitTx_NilTxRejected(t *testing.T) {
	tp := newTestTxPool(t)
	if err := tp.SubmitTx(nil, "127.0.0.1", nil); err == nil {
		t.Fatal("nil tx must be rejected")
	}
}

                                                                                                                       
func TestSubmitTx_DuplicateIsIdempotent(t *testing.T) {
	tp := newTestTxPool(t)

	tx := txWithID("dup-tx-1")
	                                                            
	tp.mu.Lock()
	tp.pendingAnyTxCache.Add("dup-tx-1", tx)
	tp.mu.Unlock()

	                                                                                                  
	if err := tp.SubmitTx(tx, "127.0.0.1", nil); err != nil {
		t.Fatalf("duplicate must be idempotent (nil err), got %v", err)
	}
}

func TestSubmitTx_CatchUpRejectsNonceRequiredTx(t *testing.T) {
	tp := newTestTxPool(t)
	tp.SetSyncPressureReader(stubSyncPressureReader{state: interfaces.SyncPressureState{
		CatchUp: true,
		Reason:  "syncing",
	}})

	err := tp.SubmitTx(normalTxWithID("normal-catch-up"), "127.0.0.1", nil)
	if !errors.Is(err, ErrSyncCatchUpBackpressure) {
		t.Fatalf("nonce-required tx must be rejected during catch-up, got %v", err)
	}
}

func TestSubmitTx_CatchUpAllowsProtocolTx(t *testing.T) {
	tp := newTestTxPool(t)
	tp.SetSyncPressureReader(stubSyncPressureReader{state: interfaces.SyncPressureState{
		CatchUp: true,
		Reason:  "syncing",
	}})

	if err := tp.SubmitTx(txWithID("cert-catch-up"), "127.0.0.1", nil); err != nil {
		t.Fatalf("protocol tx must be allowed during catch-up, got %v", err)
	}
}

                                                                      
                                                                 
func TestSubmitTx_QueueFullRejected(t *testing.T) {
	tp := newTestTxPool(t)
	                       
	for i := 0; i < cap(tp.Queue.MsgChan); i++ {
		tp.Queue.MsgChan <- &txPoolMessage{}
	}
	                                                                        
	tx := txWithID("fresh-tx")
	err := tp.SubmitTx(tx, "127.0.0.1", nil)
	if err == nil {
		t.Fatal("queue-full case must return error")
	}
}

                                                                                                      
                                                                                                      
                                                                           
func TestSubmitTx_EmergencyDoesNotConsumeAlreadyQueuedMsg(t *testing.T) {
	tp := newTestTxPool(t)
	em := lifecycle.New()
	tp.SetEmergencyStop(em)

	em.Stop("seal queue")

	                                                                                   
	called := false
	tx := txWithID("seal-1")
	tp.Queue.MsgChan <- &txPoolMessage{
		Type:    msgAddTx,
		AnyTx:   tx,
		OnAdded: func(string) { called = true },
	}
	if err := tp.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = tp.Stop() })

	                                                                         
	time.Sleep(50 * time.Millisecond)

	if called {
		t.Fatal("OnAdded must not fire while EmergencyStop is engaged")
	}
	if tp.HasTransaction("seal-1") {
		t.Fatal("tx must not enter pending pool while EmergencyStop is engaged")
	}
}

func TestGetPendingTxsLimitSortedAndBounded(t *testing.T) {
	tp := newTestTxPool(t)

	tp.mu.Lock()
	for _, id := range []string{"tx-c", "tx-a", "tx-b"} {
		tp.cacheAnyTxUnlocked(id, txWithID(id), true)
	}
	tp.mu.Unlock()

	got := tp.GetPendingTxsLimit(2)
	if len(got) != 2 {
		t.Fatalf("expected 2 txs, got %d", len(got))
	}
	if got[0].GetTxId() != "tx-a" || got[1].GetTxId() != "tx-b" {
		t.Fatalf("expected sorted first two txs, got %q %q", got[0].GetTxId(), got[1].GetTxId())
	}
}

func TestGetPendingTxsLimitPrioritizesFrostDkg(t *testing.T) {
	tp := newTestTxPool(t)

	dkgTx := &pb.AnyTx{Content: &pb.AnyTx_FrostVaultDkgShareTx{FrostVaultDkgShareTx: &pb.FrostVaultDkgShareTx{
		Base: &pb.BaseMessage{TxId: "zz-dkg"},
	}}}
	tp.mu.Lock()
	tp.cacheAnyTxUnlocked("aa-normal", normalTxWithID("aa-normal"), true)
	tp.cacheAnyTxUnlocked("zz-dkg", dkgTx, true)
	tp.mu.Unlock()

	got := tp.GetPendingTxsLimit(1)
	if len(got) != 1 {
		t.Fatalf("expected 1 tx, got %d", len(got))
	}
	if got[0].GetTxId() != "zz-dkg" {
		t.Fatalf("expected DKG tx first, got %q", got[0].GetTxId())
	}
}
