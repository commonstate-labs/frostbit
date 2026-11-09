package vm_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"dex/keys"
	"dex/pb"
	"dex/vm"
)

var errInjectedHotMetadataFlush = errors.New("injected crash before hot metadata flush")

type finalizedCrashBoundaryDB struct {
	*MockDB
	commitMu       sync.RWMutex
	stateBlocks    map[uint64]string
	failHotFlush   bool
	blockSyncCalls int
	recoveryCalls  int
}

func newFinalizedCrashBoundaryDB() *finalizedCrashBoundaryDB {
	return &finalizedCrashBoundaryDB{
		MockDB:      NewMockDB(),
		stateBlocks: make(map[uint64]string),
	}
}

func (db *finalizedCrashBoundaryDB) SyncFinalizedStateUpdatesForBlock(height uint64, blockID string, updates []interface{}, forceCheckpoint bool) (int, error) {
	type writeOp interface {
		GetKey() string
		GetValue() []byte
		IsDel() bool
	}

	db.commitMu.Lock()
	defer db.commitMu.Unlock()
	if existing := db.stateBlocks[height]; existing != "" && existing != blockID {
		return 0, errors.New("conflicting finalized block identity")
	}
	count := 0
	db.mu.Lock()
	for _, update := range updates {
		op, ok := update.(writeOp)
		if !ok || op == nil || !keys.IsStatefulKey(op.GetKey()) {
			continue
		}
		if op.IsDel() {
			delete(db.data, op.GetKey())
		} else {
			db.data[op.GetKey()] = append([]byte(nil), op.GetValue()...)
		}
		count++
	}
	db.stateSyncCalls++
	db.stateSyncHeight = height
	db.stateSyncOps = count
	db.stateSyncForce = forceCheckpoint
	db.mu.Unlock()
	db.stateBlocks[height] = blockID
	db.blockSyncCalls++
	return count, nil
}

func (db *finalizedCrashBoundaryDB) FinalizedStateBlockID(height uint64) (string, bool, error) {
	db.commitMu.RLock()
	defer db.commitMu.RUnlock()
	blockID := db.stateBlocks[height]
	return blockID, blockID != "", nil
}

func (db *finalizedCrashBoundaryDB) ForceFlushHot() error {
	if db.failHotFlush {
		return errInjectedHotMetadataFlush
	}
	return db.MockDB.ForceFlush()
}

func (db *finalizedCrashBoundaryDB) RecoverFinalizedStateCommit(height uint64, blockID string, _ bool) error {
	db.commitMu.Lock()
	defer db.commitMu.Unlock()
	if db.stateBlocks[height] != blockID {
		return errors.New("cannot recover unmatched finalized state")
	}
	db.mu.Lock()
	db.data[keys.KeyVMCommitHeight(height)] = []byte(blockID)
	db.mu.Unlock()
	db.recoveryCalls++
	return nil
}

func cloneFinalizedCrashBoundaryDB(src *finalizedCrashBoundaryDB) *finalizedCrashBoundaryDB {
	dst := newFinalizedCrashBoundaryDB()
	src.mu.RLock()
	for key, value := range src.data {
		dst.data[key] = append([]byte(nil), value...)
	}
	src.mu.RUnlock()
	src.commitMu.RLock()
	for height, blockID := range src.stateBlocks {
		dst.stateBlocks[height] = blockID
	}
	src.commitMu.RUnlock()
	return dst
}

func TestFinalizedCommitCrashAfterStateBatchUsesAuthoritativeStateMarker(t *testing.T) {
	db := newFinalizedCrashBoundaryDB()
	db.failHotFlush = true
	registry := vm.NewHandlerRegistry()
	if err := vm.RegisterDefaultHandlers(registry); err != nil {
		t.Fatal(err)
	}
	executor := vm.NewExecutor(db, registry, vm.NewSpecExecLRU(16))
	block := &pb.Block{
		BlockHash: "block-crash-boundary-1",
		Header:    &pb.BlockHeader{PrevBlockHash: "genesis", Height: 1},
	}

	err := executor.CommitFinalizedBlock(block)
	if !errors.Is(err, errInjectedHotMetadataFlush) && (err == nil || !strings.Contains(err.Error(), errInjectedHotMetadataFlush.Error())) {
		t.Fatalf("first commit error=%v want injected metadata failure", err)
	}
	if db.blockSyncCalls != 1 {
		t.Fatalf("state batches=%d want=1", db.blockSyncCalls)
	}
	if raw, _ := db.Get(keys.KeyVMCommitHeight(1)); raw != nil {
		t.Fatalf("runtime commit marker unexpectedly survived simulated crash: %q", raw)
	}

	                                                                         
	                                                                           
	                                      
	restartedDB := cloneFinalizedCrashBoundaryDB(db)
	restartedRegistry := vm.NewHandlerRegistry()
	if err := vm.RegisterDefaultHandlers(restartedRegistry); err != nil {
		t.Fatal(err)
	}
	restarted := vm.NewExecutor(restartedDB, restartedRegistry, vm.NewSpecExecLRU(16))
	if err := restarted.CommitFinalizedBlock(block); err != nil {
		t.Fatalf("restart did not recognize StateDB-committed block: %v", err)
	}
	if restartedDB.blockSyncCalls != 0 {
		t.Fatalf("restart re-executed finalized state: sync calls=%d", restartedDB.blockSyncCalls)
	}
	if restartedDB.recoveryCalls != 1 {
		t.Fatalf("restart recovery calls=%d want=1", restartedDB.recoveryCalls)
	}
	if raw, _ := restartedDB.Get(keys.KeyVMCommitHeight(1)); string(raw) != block.BlockHash {
		t.Fatalf("restart did not repair runtime commit marker: %q", raw)
	}

	conflict := &pb.Block{BlockHash: "conflicting-block-1", Header: &pb.BlockHeader{PrevBlockHash: "genesis", Height: 1}}
	if err := restarted.CommitFinalizedBlock(conflict); err == nil || !strings.Contains(err.Error(), "already committed with different hash") {
		t.Fatalf("conflicting block after restart error=%v", err)
	}
}
