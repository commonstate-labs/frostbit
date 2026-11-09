package main

import (
	"bytes"
	"errors"
	"testing"

	"dex/consensus"

	"github.com/cockroachdb/pebble"
)

type memoryLateJoinMarkerDB struct {
	values   map[string][]byte
	pending  map[string][]byte
	flushErr error
}

func newMemoryLateJoinMarkerDB() *memoryLateJoinMarkerDB {
	return &memoryLateJoinMarkerDB{values: make(map[string][]byte), pending: make(map[string][]byte)}
}

func (db *memoryLateJoinMarkerDB) Get(key string) ([]byte, error) {
	value, ok := db.values[key]
	if !ok {
		return nil, pebble.ErrNotFound
	}
	return bytes.Clone(value), nil
}

func (db *memoryLateJoinMarkerDB) EnqueueSet(key, value string) {
	db.pending[key] = []byte(value)
}

func (db *memoryLateJoinMarkerDB) ForceFlushHot() error {
	if db.flushErr != nil {
		return db.flushErr
	}
	for key, value := range db.pending {
		db.values[key] = bytes.Clone(value)
		delete(db.pending, key)
	}
	return nil
}

type memoryLateJoinConsensus struct {
	acceptedID     string
	acceptedHeight uint64
	finalized      map[uint64]string
	observer       func(consensus.VerifiedSyncPoint) error
}

func (c *memoryLateJoinConsensus) GetLastAccepted() (string, uint64) {
	return c.acceptedID, c.acceptedHeight
}

func (c *memoryLateJoinConsensus) GetFinalizedBlockID(height uint64) (string, bool) {
	id, ok := c.finalized[height]
	return id, ok
}

func (c *memoryLateJoinConsensus) SetVerifiedSyncObserver(observer func(consensus.VerifiedSyncPoint) error) {
	c.observer = observer
}

func testLateJoinMarkerNode() *NodeInstance {
	return &NodeInstance{
		Address:                 "fbc1qmarker-test-node",
		NetworkChainID:          991,
		FrostPeerManifestDigest: bytes.Repeat([]byte{0x5a}, 32),
	}
}

func TestLateJoinSyncMarkerSurvivesResumeAndBindsNetwork(t *testing.T) {
	store := newMemoryLateJoinMarkerDB()
	chain := &memoryLateJoinConsensus{
		acceptedID: "block-7", acceptedHeight: 7,
		finalized: map[uint64]string{7: "block-7"},
	}
	node := testLateJoinMarkerNode()
	if _, present, err := loadFrostLateJoinSyncMarker(store, chain, node); err != nil || present {
		t.Fatalf("nonzero interrupted state without marker present=%t err=%v", present, err)
	}
	point := consensus.VerifiedSyncPoint{Height: 7, BlockID: "block-7"}
	if err := persistFrostLateJoinSyncMarker(store, chain, node, point); err != nil {
		t.Fatalf("persist marker: %v", err)
	}
	marker, present, err := loadFrostLateJoinSyncMarker(store, chain, node)
	if err != nil || !present || marker.VerifiedHeight != 7 || marker.VerifiedBlockID != "block-7" {
		t.Fatalf("resume marker=%+v present=%t err=%v", marker, present, err)
	}

	otherNetwork := *node
	otherNetwork.FrostPeerManifestDigest = bytes.Repeat([]byte{0x6b}, 32)
	if _, _, err := loadFrostLateJoinSyncMarker(store, chain, &otherNetwork); err == nil {
		t.Fatal("marker from another genesis peer manifest was accepted")
	}
	otherNode := *node
	otherNode.Address = "fbc1qother-node"
	if _, _, err := loadFrostLateJoinSyncMarker(store, chain, &otherNode); err == nil {
		t.Fatal("marker from another local node identity was accepted")
	}
}

func TestLateJoinSyncMarkerRequiresInstalledExactTargetAndDurability(t *testing.T) {
	point := consensus.VerifiedSyncPoint{Height: 7, BlockID: "block-7"}
	node := testLateJoinMarkerNode()
	store := newMemoryLateJoinMarkerDB()
	chain := &memoryLateJoinConsensus{
		acceptedID: "block-7", acceptedHeight: 7,
		finalized: map[uint64]string{7: "different-block"},
	}
	if err := persistFrostLateJoinSyncMarker(store, chain, node, point); err == nil {
		t.Fatal("uninstalled exact target was persisted")
	}
	chain.finalized[7] = "block-7"
	store.flushErr = errors.New("disk unavailable")
	if err := persistFrostLateJoinSyncMarker(store, chain, node, point); err == nil {
		t.Fatal("failed durable flush released the marker gate")
	}
}

func TestLateJoinSyncMarkerSurvivesHistoricalBlockPruningAfterTipAdvances(t *testing.T) {
	node := testLateJoinMarkerNode()
	store := newMemoryLateJoinMarkerDB()
	chain := &memoryLateJoinConsensus{
		acceptedID: "block-7", acceptedHeight: 7,
		finalized: map[uint64]string{7: "block-7"},
	}
	point := consensus.VerifiedSyncPoint{Height: 7, BlockID: "block-7"}
	if err := persistFrostLateJoinSyncMarker(store, chain, node, point); err != nil {
		t.Fatalf("persist marker: %v", err)
	}

	chain.acceptedID = "block-8"
	chain.acceptedHeight = 8
	chain.finalized[8] = "block-8"
	if _, present, err := loadFrostLateJoinSyncMarker(store, chain, node); err != nil || !present {
		t.Fatalf("valid ancestor marker present=%t err=%v", present, err)
	}

	delete(chain.finalized, 7)
	if _, present, err := loadFrostLateJoinSyncMarker(store, chain, node); err != nil || !present {
		t.Fatalf("pruned historical target permanently blocked a valid resumed node: present=%t err=%v", present, err)
	}
}

func TestFrostCatchupReadyIgnoresUntrustedMaximumPeerHeight(t *testing.T) {
	status := consensus.SyncStatus{
		VerifiedSyncCompletions: 1,
		VerifiedSyncHeight:      7,
		SyncTargetHeight:        1_000_000,
	}
	if !frostCatchupReady(status, 7) {
		t.Fatal("untrusted peer-height hint blocked an installed authenticated target")
	}
	status.VerifiedSyncHeight = 8
	if frostCatchupReady(status, 7) {
		t.Fatal("local state below the authenticated target passed the gate")
	}
	status.VerifiedSyncHeight = 7
	status.IsSyncing = true
	if frostCatchupReady(status, 7) {
		t.Fatal("active verified execution stage passed the gate")
	}
}

func TestLateJoinAlwaysDefersCurrentSharePreflightUntilNetworking(t *testing.T) {
	if preflightFrostSharesBeforeNetworking(LateJoin) {
		t.Fatal("late join attempted current-share preflight before networking")
	}
	if !preflightFrostSharesBeforeNetworking(GenesisBootstrap) {
		t.Fatal("genesis bootstrap deferred its fail-closed current-share preflight")
	}
}
