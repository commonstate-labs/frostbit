package db

import (
	"dex/keys"
	"dex/liveness"
	"dex/logs"
	"dex/pb"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/cockroachdb/pebble"
)

type minerIndexReconcileWriteOp struct {
	key   string
	value []byte
}

func (op minerIndexReconcileWriteOp) GetKey() string   { return op.key }
func (op minerIndexReconcileWriteOp) GetValue() []byte { return op.value }
func (minerIndexReconcileWriteOp) IsDel() bool         { return false }

type staticMinerIndexSource struct {
	addresses []string
	indices   map[string]uint32
}

func (s *staticMinerIndexSource) GetActiveMiners() []string {
	return append([]string(nil), s.addresses...)
}

func (s *staticMinerIndexSource) GetMinerIndex(address string) (uint32, bool) {
	index, ok := s.indices[address]
	return index, ok
}

func TestReconcileMinerIndexFromLivenessRepairsMissingAndStaleMappings(t *testing.T) {
	root := t.TempDir()
	manager, err := NewManager(root, logs.NewNodeLogger("miner-index-reconcile", 0))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if manager != nil {
			manager.Close()
		}
	})

	const hotMinerCount = 10
	const frozenIndex = hotMinerCount
	stateUpdates := make([]interface{}, 0, hotMinerCount+1)
	trackerWrites := make(map[string][]byte)
	writer := &liveness.DirectWriter{
		SetFn: func(key string, value []byte) {
			trackerWrites[key] = append([]byte(nil), value...)
		},
		DelFn: func(key string) { delete(trackerWrites, key) },
	}
	tracker := liveness.NewTracker(liveness.DefaultConfig())
	hotAddresses := make([]string, 0, hotMinerCount)
	for i := 0; i < hotMinerCount; i++ {
		address := "hot-miner-" + string(rune('a'+i))
		account := &pb.Account{
			Address: address,
			Ip:      "127.0.0.1:6" + string(rune('0'+i)),
			IsMiner: true,
			Index:   uint64(i),
		}
		raw, marshalErr := ProtoMarshal(account)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		stateUpdates = append(stateUpdates, minerIndexReconcileWriteOp{key: keys.KeyAccount(address), value: raw})
		hotAddresses = append(hotAddresses, address)
		joinHeight := uint64(0)
		if i >= 5 {
			joinHeight = 1                                                               
		}
		if index, err := tracker.RegisterMinerAtHeight(writer, address, 3_200_000_000, joinHeight); err != nil || index != uint32(i) {
			t.Fatalf("register liveness miner %s: index=%d err=%v", address, index, err)
		}
	}
	                                                                        
	                                                                          
	                                                     
	frozen := &pb.Account{Address: "frozen-miner", Ip: "127.0.0.1:6999", IsMiner: true, Index: frozenIndex}
	frozenRaw, err := ProtoMarshal(frozen)
	if err != nil {
		t.Fatal(err)
	}
	stateUpdates = append(stateUpdates, minerIndexReconcileWriteOp{key: keys.KeyAccount(frozen.Address), value: frozenRaw})
	if index, err := tracker.RegisterMinerAtHeight(writer, frozen.Address, 3_200_000_000, 0); err != nil || index != frozenIndex {
		t.Fatalf("register Frozen liveness miner: index=%d err=%v", index, err)
	}
	if err := tracker.RemoveMiner(writer, frozen.Address, 899); err != nil {
		t.Fatalf("freeze liveness miner: %v", err)
	}
	trackerKeys := make([]string, 0, len(trackerWrites))
	for key := range trackerWrites {
		trackerKeys = append(trackerKeys, key)
	}
	sort.Strings(trackerKeys)
	for _, key := range trackerKeys {
		stateUpdates = append(stateUpdates, minerIndexReconcileWriteOp{key: key, value: trackerWrites[key]})
	}
	if _, err := manager.SyncStateUpdates(900, stateUpdates); err != nil {
		t.Fatalf("seed authoritative StateDB accounts/liveness: %v", err)
	}

	                                                                          
	                                                                              
	batch := manager.Db.NewBatch()
	for i := 0; i < 5; i++ {
		address := hotAddresses[i]
		if err := batch.Set([]byte(keys.KeyIndexToAccount(uint64(i))), []byte(keys.KeyAccount(address)), nil); err != nil {
			_ = batch.Close()
			t.Fatal(err)
		}
	}
	if err := batch.Set([]byte(keys.KeyIndexToAccount(frozenIndex)), []byte(keys.KeyAccount(frozen.Address)), nil); err != nil {
		_ = batch.Close()
		t.Fatal(err)
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		_ = batch.Close()
		t.Fatal(err)
	}
	_ = batch.Close()
	manager.Close()
	manager = nil

	                                                                   
	                                                                            
	                                                                           
	manager, err = NewManager(root, logs.NewNodeLogger("miner-index-reconcile-restart", 0))
	if err != nil {
		t.Fatal(err)
	}
	reloadedTracker := liveness.NewTracker(liveness.DefaultConfig())
	reloadedTracker.SetStore(manager)
	if got := reloadedTracker.GetActiveMiners(); !reflect.DeepEqual(got, hotAddresses) {
		t.Fatalf("reloaded hot liveness members=%v, want %v", got, hotAddresses)
	}
	if state := reloadedTracker.GetMinerState(hotAddresses[4]); state != int(liveness.StateActive) {
		t.Fatalf("last genesis miner state=%d, want Active", state)
	}
	if state := reloadedTracker.GetMinerState(hotAddresses[5]); state != int(liveness.StateProbation) {
		t.Fatalf("first runtime joiner state=%d, want Probation", state)
	}
	before, err := manager.GetMinerParticipantsSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 6 {
		t.Fatalf("pre-reconcile cached participants=%d, want 5 hot + 1 stale", len(before))
	}

	count, err := manager.ReconcileMinerIndexFromLiveness(reloadedTracker)
	if err != nil {
		t.Fatalf("reconcile miner index: %v", err)
	}
	if count != hotMinerCount {
		t.Fatalf("reconciled mappings=%d, want %d", count, hotMinerCount)
	}
	wantIndices := make([]uint64, hotMinerCount)
	for i := range wantIndices {
		wantIndices[i] = uint64(i)
	}
	if got := manager.IndexMgr.SnapshotIndices(); !reflect.DeepEqual(got, wantIndices) {
		t.Fatalf("bitmap indices=%v, want %v", got, wantIndices)
	}
	for i, address := range hotAddresses {
		raw, err := manager.GetKV(keys.KeyIndexToAccount(uint64(i)))
		if err != nil || string(raw) != keys.KeyAccount(address) {
			t.Fatalf("mapping %d=(%q,%v), want %q", i, raw, err, keys.KeyAccount(address))
		}
	}
	if _, err := manager.GetKV(keys.KeyIndexToAccount(frozenIndex)); !errors.Is(err, pebble.ErrNotFound) {
		t.Fatalf("Frozen mapping survived reconciliation: %v", err)
	}
	after, err := manager.GetMinerParticipantsSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != hotMinerCount {
		t.Fatalf("post-reconcile cached participants=%d, want %d", len(after), hotMinerCount)
	}
}

func TestReconcileMinerIndexFromLivenessRejectsAccountIndexMismatchWithoutMutation(t *testing.T) {
	manager, err := NewManager(t.TempDir(), logs.NewNodeLogger("miner-index-reconcile-invalid", 0))
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	account := &pb.Account{Address: "miner", Ip: "127.0.0.1:6001", IsMiner: true, Index: 7}
	raw, err := ProtoMarshal(account)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.SyncStateUpdates(10, []interface{}{
		minerIndexReconcileWriteOp{key: keys.KeyAccount(account.Address), value: raw},
	}); err != nil {
		t.Fatal(err)
	}
	oldKey := keys.KeyIndexToAccount(1)
	oldValue := keys.KeyAccount(account.Address)
	if err := manager.Db.Set([]byte(oldKey), []byte(oldValue), pebble.Sync); err != nil {
		t.Fatal(err)
	}
	if err := manager.IndexMgr.RebuildBitmapFromDB(); err != nil {
		t.Fatal(err)
	}

	source := &staticMinerIndexSource{
		addresses: []string{account.Address},
		indices:   map[string]uint32{account.Address: 8},
	}
	if _, err := manager.ReconcileMinerIndexFromLiveness(source); err == nil || !strings.Contains(err.Error(), "does not match liveness index") {
		t.Fatalf("reconcile error=%v, want account/liveness index mismatch", err)
	}
	if got, err := manager.GetKV(oldKey); err != nil || string(got) != oldValue {
		t.Fatalf("old mapping mutated after rejected reconcile: value=%q err=%v", got, err)
	}
	if _, err := manager.GetKV(keys.KeyIndexToAccount(8)); !errors.Is(err, pebble.ErrNotFound) {
		t.Fatalf("new mapping was written after rejected reconcile: %v", err)
	}
	if got := manager.IndexMgr.SnapshotIndices(); !reflect.DeepEqual(got, []uint64{1}) {
		t.Fatalf("bitmap mutated after rejected reconcile: %v", got)
	}
}

func TestPublishMinerParticipantsRejectsInvalidatedGeneration(t *testing.T) {
	sentinel := &pb.Account{Address: "previous-cache"}
	manager := &Manager{minerParticipants: []*pb.Account{sentinel}}
	staleGeneration := manager.minerCacheGeneration
	manager.InvalidateMinerCache()

	if manager.publishMinerParticipants(7, staleGeneration, []*pb.Account{{Address: "stale-refresh"}}) {
		t.Fatal("refresh from an invalidated generation was published")
	}
	manager.minerCacheMu.RLock()
	if manager.minerCacheReady {
		manager.minerCacheMu.RUnlock()
		t.Fatal("invalidated cache was marked ready by stale refresh")
	}
	if len(manager.minerParticipants) != 1 || manager.minerParticipants[0] != sentinel {
		manager.minerCacheMu.RUnlock()
		t.Fatalf("stale refresh overwrote cache: %+v", manager.minerParticipants)
	}
	currentGeneration := manager.minerCacheGeneration
	manager.minerCacheMu.RUnlock()

	fresh := &pb.Account{Address: "fresh-refresh"}
	if !manager.publishMinerParticipants(7, currentGeneration, []*pb.Account{fresh}) {
		t.Fatal("current-generation refresh was rejected")
	}
	manager.minerCacheMu.RLock()
	defer manager.minerCacheMu.RUnlock()
	if !manager.minerCacheReady || len(manager.minerParticipants) != 1 || manager.minerParticipants[0] != fresh {
		t.Fatalf("fresh refresh was not published: ready=%t participants=%+v", manager.minerCacheReady, manager.minerParticipants)
	}
}
