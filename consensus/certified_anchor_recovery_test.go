package consensus

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"dex/config"
	"dex/db"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"dex/types"
	"dex/utils"

	"github.com/cockroachdb/pebble"
	"google.golang.org/protobuf/proto"
)

type certifiedAnchorRecoveryWriteOp struct {
	key   string
	value []byte
}

func (op certifiedAnchorRecoveryWriteOp) GetKey() string   { return op.key }
func (op certifiedAnchorRecoveryWriteOp) GetValue() []byte { return op.value }
func (certifiedAnchorRecoveryWriteOp) IsDel() bool         { return false }

func prepareCertifiedAnchorRecoveryFixture(t *testing.T, committedStateBlockID string) (*db.Manager, *RealBlockStore, *pb.Block, []byte, string) {
	t.Helper()
	cfg := config.DefaultConfig()
	root := t.TempDir()
	manager, err := db.NewManagerWithConfig(root, logs.NewNodeLogger("certified-anchor-recovery", 0), cfg)
	if err != nil {
		t.Fatal(err)
	}
	manager.InitWriteQueue(128, time.Hour)

	store, ok := NewRealBlockStore(types.NodeID("recovery-node"), manager, nil, cfg).(*RealBlockStore)
	if !ok {
		t.Fatal("expected RealBlockStore")
	}
	if err := store.RecoveryError(); err != nil {
		t.Fatal(err)
	}
	if err := manager.ForceFlush(); err != nil {
		t.Fatal(err)
	}

	keyManager := utils.NewKeyManager()
	if err := keyManager.InitKeyRandom(); err != nil {
		t.Fatal(err)
	}
	proposer := keyManager.GetAddress()
	blsKey := proposalValidationBLSKey(t, keyManager)
	const height uint64 = 10
	const parentID = "certified-parent-9"
	output, proof, err := keyManager.GenerateVRF(height, 0, parentID, types.NodeID(proposer))
	if err != nil {
		t.Fatal(err)
	}
	anchor := &pb.Block{Header: &pb.BlockHeader{
		Height:        height,
		PrevBlockHash: parentID,
		Timestamp:     123456789,
		StateRoot:     bytes.Repeat([]byte{0x42}, 32),
		TxsHash:       emptyTxsHash,
		Miner:         keys.KeyNode() + proposer,
		Window:        0,
		VrfProof:      proof,
		VrfOutput:     output,
		PublicKey:     blsKey,
	}}
	converted, err := (&ConsensusAdapter{}).DBBlockToConsensus(anchor)
	if err != nil {
		t.Fatal(err)
	}
	anchor.BlockHash = converted.Header.ComputeIDForChain(pb.GetDefaultChainID())
	anchor.MinerSignature, err = signBlockProposal(keyManager, anchor.BlockHash)
	if err != nil {
		t.Fatal(err)
	}

	accountRaw, err := proto.Marshal(&pb.Account{
		Address: proposer,
		Index:   7,
		IsMiner: true,
		PublicKeys: &pb.PublicKeys{Keys: map[int32][]byte{
			int32(pb.SignAlgo_SIGN_ALGO_BLS_BN256_G2): blsKey,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if committedStateBlockID == "" {
		committedStateBlockID = anchor.BlockHash
	}
	if _, err := manager.SyncFinalizedStateUpdatesForBlock(height, anchor.BlockHash, []interface{}{
		certifiedAnchorRecoveryWriteOp{key: keys.KeyAccount(proposer), value: accountRaw},
		certifiedAnchorRecoveryWriteOp{key: keys.KeyBlockHashByHeight(height), value: []byte(committedStateBlockID)},
	}, false); err != nil {
		t.Fatal(err)
	}
	stateRoot := manager.GetStateFingerprint()
	certRaw, err := proto.Marshal(&pb.CheckpointCertificate{
		Height:    height,
		BlockHash: []byte(anchor.BlockHash),
		StateRoot: stateRoot,
		ChainId:   pb.GetDefaultChainID(),
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.EnqueueSet(keys.KeyCertCertificate(height), string(certRaw))
	if err := manager.ForceFlushHot(); err != nil {
		t.Fatal(err)
	}
	if err := store.prepareCertifiedAnchorRecovery(0, height, anchor.BlockHash, stateRoot, anchor); err != nil {
		t.Fatal(err)
	}
	return manager, store, anchor, stateRoot, root
}

func TestRealBlockStoreRecoversCertifiedSnapshotAnchorFromDurableRecord(t *testing.T) {
	manager, _, anchor, _, root := prepareCertifiedAnchorRecoveryFixture(t, "")
	if _, err := manager.GetBlockByID(anchor.BlockHash); err == nil {
		t.Fatal("fixture unexpectedly persisted the formal block archive before recovery")
	}
	manager.Close()
	manager, err := db.NewManagerWithConfig(root, logs.NewNodeLogger("certified-anchor-recovery-restart", 0), config.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	manager.InitWriteQueue(128, time.Hour)

	restarted := NewRealBlockStore(types.NodeID("recovery-node"), manager, nil, config.DefaultConfig()).(*RealBlockStore)
	if err := restarted.RecoveryError(); err != nil {
		t.Fatalf("certified recovery failed: %v", err)
	}
	if id, height := restarted.GetLastAccepted(); id != anchor.BlockHash || height != anchor.Header.Height {
		t.Fatalf("recovered tip=(%q,%d), want (%q,%d)", id, height, anchor.BlockHash, anchor.Header.Height)
	}
	if persisted, err := manager.GetBlockByID(anchor.BlockHash); err != nil || persisted == nil || !proto.Equal(persisted, anchor) {
		t.Fatalf("formal anchor archive mismatch: block=%v err=%v", persisted, err)
	}
	if raw, err := manager.GetKV(keys.KeyVMCommitHeight(anchor.Header.Height)); err != nil || string(raw) != anchor.BlockHash {
		t.Fatalf("VM commit marker=%q err=%v, want %q", raw, err, anchor.BlockHash)
	}
	if _, err := manager.GetKV(keys.KeyCertifiedAnchorRecovery()); !errors.Is(err, pebble.ErrNotFound) {
		t.Fatalf("recovery record was not cleared: %v", err)
	}
	if from, through, err := manager.GetBlockArchiveRange(); err != nil || from != 0 || through != 0 {
		t.Fatalf("checkpoint anchor incorrectly advertised historical coverage: %d-%d err=%v", from, through, err)
	}
	if raw, err := manager.GetKV(keys.KeyQueryPruneHeightCursor()); err != nil || string(raw) != strconv.FormatUint(anchor.Header.Height+1, 10) {
		t.Fatalf("query prune cursor=%q err=%v, want %d", raw, err, anchor.Header.Height+1)
	}
}

func TestRealBlockStoreCertifiedSnapshotRecoveryRejectsConsensusStateBlockConflict(t *testing.T) {
	manager, _, anchor, _, _ := prepareCertifiedAnchorRecoveryFixture(t, "conflicting-state-block")
	defer manager.Close()
	restarted := NewRealBlockStore(types.NodeID("recovery-node"), manager, nil, config.DefaultConfig()).(*RealBlockStore)
	err := restarted.RecoveryError()
	if err == nil || !strings.Contains(err.Error(), "consensus-state block identity") {
		t.Fatalf("conflicting state block identity was not rejected: %v", err)
	}
	if id, height := restarted.GetLastAccepted(); id != "genesis" || height != 0 {
		t.Fatalf("failed recovery advanced tip=(%q,%d), want genesis", id, height)
	}
	if _, err := manager.GetKV(keys.KeyCertifiedAnchorRecovery()); err != nil {
		t.Fatalf("failed recovery erased its evidence: %v", err)
	}
	if _, err := manager.GetBlockByID(anchor.BlockHash); err == nil {
		t.Fatal("failed recovery persisted an unauthenticated anchor")
	}
}

func TestRealBlockStoreCertifiedSnapshotRecoveryRepairsPartialArchiveTip(t *testing.T) {
	manager, _, anchor, _, _ := prepareCertifiedAnchorRecoveryFixture(t, "")
	defer manager.Close()
	ids, err := json.Marshal([]string{anchor.BlockHash})
	if err != nil {
		t.Fatal(err)
	}
	                                                                           
	                                                                            
	                                                                    
	manager.EnqueueSet(keys.KeyLatestHeight(), strconv.FormatUint(anchor.Header.Height, 10))
	manager.EnqueueSet(keys.KeyHeightBlocks(anchor.Header.Height), string(ids))
	manager.EnqueueSet(keys.KeyBlockIDToHeight(anchor.BlockHash), strconv.FormatUint(anchor.Header.Height, 10))
	if err := manager.ForceFlush(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.GetKV(keys.KeyBlockData(anchor.BlockHash)); !errors.Is(err, pebble.ErrNotFound) {
		t.Fatalf("partial archive fixture unexpectedly contains block payload: %v", err)
	}

	restarted := NewRealBlockStore(types.NodeID("recovery-node"), manager, nil, config.DefaultConfig()).(*RealBlockStore)
	if err := restarted.RecoveryError(); err != nil {
		t.Fatalf("partial archive recovery failed: %v", err)
	}
	if id, height := restarted.GetLastAccepted(); id != anchor.BlockHash || height != anchor.Header.Height {
		t.Fatalf("recovered partial-archive tip=(%q,%d), want (%q,%d)", id, height, anchor.BlockHash, anchor.Header.Height)
	}
	if _, err := manager.GetKV(keys.KeyCertifiedAnchorRecovery()); !errors.Is(err, pebble.ErrNotFound) {
		t.Fatalf("partial archive recovery record was not cleared: %v", err)
	}
}
