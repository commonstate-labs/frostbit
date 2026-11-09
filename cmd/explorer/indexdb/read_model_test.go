package indexdb

import (
	"bytes"
	"encoding/json"
	"strconv"
	"testing"

	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

func TestIndexBlockWithReadModelCommitsAtomicallyAndReplaysIdempotently(t *testing.T) {
	idb := newReadModelTestDB(t)
	block := readModelTestBlock(1, "block-1", "genesis")
	block.Body = []*pb.AnyTx{testTransferTx("tx-1", "alice")}
	activityKey := keys.KeyUserActivity("alice", 1, 0)
	model := sealedReadModel(t, block, readModelPut(0, activityKey, []byte(`{"kind":"FEE","height":1}`), pb.ReadModelRetention_READ_MODEL_RETENTION_BLOCK_SCOPED))

	if err := idb.IndexBlockWithReadModel(block, model); err != nil {
		t.Fatalf("IndexBlockWithReadModel: %v", err)
	}
	if got, err := idb.Get(activityKey); err != nil || got != `{"kind":"FEE","height":1}` {
		t.Fatalf("activity value = %q, err=%v", got, err)
	}
	if got, err := idb.GetSyncHeight(); err != nil || got != 1 {
		t.Fatalf("sync height = %d, err=%v", got, err)
	}
	if got, err := idb.GetBlockTxs(1); err != nil || len(got) != 1 || got[0].TxID != "tx-1" {
		t.Fatalf("ordinary transaction indexes = %+v, err=%v", got, err)
	}
	coverage, err := idb.GetReadModelCoverage()
	if err != nil || coverage == nil || coverage.SchemaVersion != pb.BlockReadModelSchemaVersion || coverage.BaseHeight != 1 || coverage.TipHeight != 1 {
		t.Fatalf("coverage = %+v, err=%v", coverage, err)
	}
	rawManifest, err := idb.Get(KeyHeightManifest(1))
	if err != nil {
		t.Fatal(err)
	}
	var manifest heightManifest
	if err := json.Unmarshal([]byte(rawManifest), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.ReadModelSchemaVersion != pb.BlockReadModelSchemaVersion || manifest.ReadModelDigest != readModelDigestHex(model) || len(manifest.OwnedKeys) != 0 || !manifestHasKey(&manifest, activityKey) {
		t.Fatalf("manifest = %+v", manifest)
	}

	if err := idb.IndexBlockWithReadModel(block, model); err != nil {
		t.Fatalf("idempotent replay: %v", err)
	}
	conflicting := sealedReadModel(t, block, readModelPut(0, activityKey, []byte(`{"kind":"FEE","height":1,"changed":true}`), pb.ReadModelRetention_READ_MODEL_RETENTION_BLOCK_SCOPED))
	if err := idb.IndexBlockWithReadModel(block, conflicting); err == nil {
		t.Fatal("conflicting read-model replay unexpectedly succeeded")
	}
}

func TestIndexBlockWithReadModelRequiresSidecar(t *testing.T) {
	idb := newReadModelTestDB(t)
	if err := idb.IndexBlockWithReadModel(readModelTestBlock(1, "block-1", "genesis"), nil); err == nil {
		t.Fatal("missing read model unexpectedly advanced the finalized index")
	}
	if got, err := idb.GetSyncHeight(); err != nil || got != 0 {
		t.Fatalf("sync height = %d, err=%v", got, err)
	}
}

func TestIndexBlockWithReadModelRejectsInvalidSidecarWithoutPartialWrites(t *testing.T) {
	tests := []struct {
		name  string
		alter func(*pb.BlockReadModel)
	}{
		{name: "digest", alter: func(model *pb.BlockReadModel) { model.PayloadHash[0] ^= 0xff }},
		{name: "parent", alter: func(model *pb.BlockReadModel) {
			model.ParentId = "wrong-parent"
			if err := pb.SealBlockReadModel(model); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "op", alter: func(model *pb.BlockReadModel) {
			model.Mutations[0].Op = pb.ReadModelMutationOp(99)
			if err := pb.SealBlockReadModel(model); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "retention", alter: func(model *pb.BlockReadModel) {
			model.Mutations[0].Retention = pb.ReadModelRetention(99)
			if err := pb.SealBlockReadModel(model); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "family retention", alter: func(model *pb.BlockReadModel) {
			model.Mutations[0].Key = keys.KeyUserActivity("alice", 1, 0)
			model.Mutations[0].Retention = pb.ReadModelRetention_READ_MODEL_RETENTION_CURRENT
			if err := pb.SealBlockReadModel(model); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			idb := newReadModelTestDB(t)
			block := readModelTestBlock(1, "block-1", "genesis")
			block.Body = []*pb.AnyTx{testTransferTx("must-not-leak", "alice")}
			model := sealedReadModel(t, block, readModelPut(0, "v1_test_read_model", []byte("value"), pb.ReadModelRetention_READ_MODEL_RETENTION_BLOCK_SCOPED))
			test.alter(model)
			if err := idb.IndexBlockWithReadModel(block, model); err == nil {
				t.Fatal("invalid read model unexpectedly succeeded")
			}
			if got, err := idb.Get("v1_test_read_model"); err != nil || got != "" {
				t.Fatalf("mutation leaked after rejected batch: %q, err=%v", got, err)
			}
			if got, err := idb.GetSyncHeight(); err != nil || got != 0 {
				t.Fatalf("sync height advanced after rejected batch: %d, err=%v", got, err)
			}
			if got, err := idb.GetBlockHash(1); err != nil || got != "" {
				t.Fatalf("block hash leaked after rejected batch: %q, err=%v", got, err)
			}
			if got, err := idb.GetTxRecord("must-not-leak"); err != nil || got != nil {
				t.Fatalf("transaction index leaked after rejected batch: %+v, err=%v", got, err)
			}
		})
	}
}

func TestReadModelCoverageRejectsLegacyAndGaps(t *testing.T) {
	t.Run("legacy", func(t *testing.T) {
		idb := newReadModelTestDB(t)
		if err := idb.IndexBlock(readModelTestBlock(1, "block-1", "genesis")); err != nil {
			t.Fatal(err)
		}
		block := readModelTestBlock(2, "block-2", "block-1")
		block.Body = []*pb.AnyTx{testTransferTx("legacy-gap-tx", "alice")}
		model := sealedReadModel(t, block, readModelPut(0, "v1_legacy_gap", []byte("must-not-leak"), pb.ReadModelRetention_READ_MODEL_RETENTION_BLOCK_SCOPED))
		if err := idb.IndexBlockWithReadModel(block, model); err == nil {
			t.Fatal("sidecar unexpectedly started on a legacy index")
		}
		if got, _ := idb.GetSyncHeight(); got != 1 {
			t.Fatalf("sync height = %d, want 1", got)
		}
		assertIndexValue(t, idb, "v1_legacy_gap", "")
		if got, err := idb.GetTxRecord("legacy-gap-tx"); err != nil || got != nil {
			t.Fatalf("legacy rejection leaked tx index: %+v, err=%v", got, err)
		}
	})

	t.Run("gap", func(t *testing.T) {
		idb := newReadModelTestDB(t)
		block1 := readModelTestBlock(1, "block-1", "genesis")
		if err := idb.IndexBlockWithReadModel(block1, sealedReadModel(t, block1)); err != nil {
			t.Fatal(err)
		}
		block3 := readModelTestBlock(3, "block-3", "block-2")
		if err := idb.IndexBlockWithReadModel(block3, sealedReadModel(t, block3)); err == nil {
			t.Fatal("non-contiguous read model unexpectedly succeeded")
		}
		coverage, err := idb.GetReadModelCoverage()
		if err != nil || coverage.TipHeight != 1 {
			t.Fatalf("coverage = %+v, err=%v", coverage, err)
		}
	})
}

func TestReadModelRetentionHonorsCurrentOwner(t *testing.T) {
	idb := newReadModelTestDB(t)
	blockScopedKey := "v1_test_block_scoped"
	currentKey := "v1_test_current"

	block1 := readModelTestBlock(1, "block-1", "genesis")
	model1 := sealedReadModel(t, block1,
		readModelPut(0, blockScopedKey, []byte("block-1"), pb.ReadModelRetention_READ_MODEL_RETENTION_BLOCK_SCOPED),
		readModelPut(1, currentKey, []byte("current-1"), pb.ReadModelRetention_READ_MODEL_RETENTION_CURRENT),
	)
	if err := idb.IndexBlockWithReadModel(block1, model1); err != nil {
		t.Fatal(err)
	}

	block2 := readModelTestBlock(2, "block-2", "block-1")
	model2 := sealedReadModel(t, block2,
		readModelPut(0, blockScopedKey, []byte("block-2"), pb.ReadModelRetention_READ_MODEL_RETENTION_BLOCK_SCOPED),
		readModelPut(1, currentKey, []byte("current-2"), pb.ReadModelRetention_READ_MODEL_RETENTION_CURRENT),
	)
	if err := idb.IndexBlockWithReadModel(block2, model2); err != nil {
		t.Fatal(err)
	}
	if pruned, err := idb.pruneHeight(1); err != nil || !pruned {
		t.Fatalf("prune height 1 = %v, err=%v", pruned, err)
	}
	assertIndexValue(t, idb, blockScopedKey, "block-2")
	assertIndexValue(t, idb, currentKey, "current-2")

	block3 := readModelTestBlock(3, "block-3", "block-2")
	if err := idb.IndexBlockWithReadModel(block3, sealedReadModel(t, block3)); err != nil {
		t.Fatal(err)
	}
	if pruned, err := idb.pruneHeight(2); err != nil || !pruned {
		t.Fatalf("prune height 2 = %v, err=%v", pruned, err)
	}
	assertIndexValue(t, idb, blockScopedKey, "")
	assertIndexValue(t, idb, currentKey, "current-2")
	assertIndexValue(t, idb, KeyReadModelOwner(blockScopedKey), "")
	assertIndexValue(t, idb, KeyReadModelOwner(currentKey), "2")
	coverage, err := idb.GetReadModelCoverage()
	if err != nil || coverage.BaseHeight != 3 || coverage.TipHeight != 3 {
		t.Fatalf("coverage after pruning = %+v, err=%v", coverage, err)
	}

	block4 := readModelTestBlock(4, "block-4", "block-3")
	deleteCurrent := &pb.ReadModelMutation{
		Ordinal:   0,
		Key:       currentKey,
		Op:        pb.ReadModelMutationOp_READ_MODEL_MUTATION_DELETE,
		Retention: pb.ReadModelRetention_READ_MODEL_RETENTION_CURRENT,
	}
	if err := idb.IndexBlockWithReadModel(block4, sealedReadModel(t, block4, deleteCurrent)); err != nil {
		t.Fatal(err)
	}
	assertIndexValue(t, idb, currentKey, "")
	assertIndexValue(t, idb, KeyReadModelOwner(currentKey), "")
}

func TestReadModelProductionFamiliesUseCompactRetention(t *testing.T) {
	idb := newReadModelTestDB(t)
	activityKey := keys.KeyUserActivity("alice", 1, 0)
	spot := &pb.OrderState{
		OrderId:      "spot-open-1",
		Owner:        "alice",
		BaseToken:    "FB",
		QuoteToken:   "USDT",
		Status:       pb.OrderStateStatus_ORDER_OPEN,
		CreateHeight: 1,
	}
	spotKey := keys.KeySpotAccountOrderFromState(spot)
	spotRaw, err := proto.Marshal(spot)
	if err != nil {
		t.Fatal(err)
	}

	block1 := readModelTestBlock(1, "block-1", "genesis")
	model1 := sealedReadModel(t, block1,
		readModelPut(0, activityKey, []byte(`{"kind":"FEE","height":1}`), pb.ReadModelRetention_READ_MODEL_RETENTION_BLOCK_SCOPED),
		readModelPut(1, spotKey, spotRaw, pb.ReadModelRetention_READ_MODEL_RETENTION_CURRENT),
	)
	if err := idb.IndexBlockWithReadModel(block1, model1); err != nil {
		t.Fatal(err)
	}
	assertIndexValue(t, idb, KeyReadModelOwner(activityKey), "")
	assertIndexValue(t, idb, KeyReadModelOwner(spotKey), "")

	rawManifest, err := idb.Get(KeyHeightManifest(1))
	if err != nil {
		t.Fatal(err)
	}
	var manifest heightManifest
	if err := json.Unmarshal([]byte(rawManifest), &manifest); err != nil {
		t.Fatal(err)
	}
	if !manifestHasKey(&manifest, activityKey) || manifestHasKey(&manifest, spotKey) || len(manifest.OwnedKeys) != 0 {
		t.Fatalf("compact manifest = %+v", manifest)
	}

	block2 := readModelTestBlock(2, "block-2", "block-1")
	if err := idb.IndexBlockWithReadModel(block2, sealedReadModel(t, block2)); err != nil {
		t.Fatal(err)
	}
	if pruned, err := idb.pruneHeight(1); err != nil || !pruned {
		t.Fatalf("prune height 1 = %v, err=%v", pruned, err)
	}
	assertIndexValue(t, idb, activityKey, "")
	assertIndexValue(t, idb, spotKey, string(spotRaw))
}

func TestReadModelLocalQueries(t *testing.T) {
	idb := newReadModelTestDB(t)
	mutations := make([]*pb.ReadModelMutation, 0)
	ordinal := uint32(0)
	addPut := func(key string, value []byte, retention pb.ReadModelRetention) {
		mutations = append(mutations, readModelPut(ordinal, key, value, retention))
		ordinal++
	}
	for seq, height := range []uint64{1, 2, 3} {
		key := keys.KeyUserActivity("alice", height, uint32(seq))
		record := []byte(`{"kind":"FEE","height":` + strconv.FormatUint(height, 10) + `}`)
		addPut(key, record, pb.ReadModelRetention_READ_MODEL_RETENTION_BLOCK_SCOPED)
		addPut(keys.KeyUserActivityKindIndex("alice", "FEE", height, "tx", uint32(seq)), []byte(key), pb.ReadModelRetention_READ_MODEL_RETENTION_BLOCK_SCOPED)
	}

	spot := &pb.OrderState{OrderId: "spot-1", Owner: "alice", BaseToken: "FB", QuoteToken: "USDT", Status: pb.OrderStateStatus_ORDER_OPEN, CreateHeight: 3}
	spotRaw, err := proto.Marshal(spot)
	if err != nil {
		t.Fatal(err)
	}
	spotPrimary := keys.KeySpotAccountOrderFromState(spot)
	addPut(spotPrimary, spotRaw, pb.ReadModelRetention_READ_MODEL_RETENTION_CURRENT)
	addPut(keys.KeySpotAccountOrderRecentFromState(spot), []byte(spotPrimary), pb.ReadModelRetention_READ_MODEL_RETENTION_CURRENT)

	futureOrder := &pb.FutureOrderState{OrderId: "future-1", Owner: "alice", OwnerAddress: "alice", Pair: "BTC_USDT", Status: pb.FutureOrderStatus_FUTURE_ORDER_FILLED, CreateHeight: 2}
	futureRaw, err := proto.Marshal(futureOrder)
	if err != nil {
		t.Fatal(err)
	}
	futurePrimary := keys.KeyFutureAccountOrderFromState(futureOrder)
	addPut(futurePrimary, futureRaw, pb.ReadModelRetention_READ_MODEL_RETENTION_BLOCK_SCOPED)
	addPut(keys.KeyFutureAccountOrderRecentFromState(futureOrder), []byte(futurePrimary), pb.ReadModelRetention_READ_MODEL_RETENTION_BLOCK_SCOPED)

	block := readModelTestBlock(1, "block-1", "genesis")
	if err := idb.IndexBlockWithReadModel(block, sealedReadModel(t, block, mutations...)); err != nil {
		t.Fatal(err)
	}

	page, err := idb.GetUserActivities("alice", "", 1, 1)
	if err != nil || len(page.Items) != 1 || page.Items[0].Key != keys.KeyUserActivity("alice", 2, 1) || !page.HasMore || page.Total != 3 {
		t.Fatalf("activity page = %+v, err=%v", page, err)
	}
	kindPage, err := idb.GetUserActivities("alice", "FEE", 2, 0)
	if err != nil || len(kindPage.Items) != 2 || kindPage.Items[0].Key != keys.KeyUserActivity("alice", 3, 2) || !kindPage.HasMore {
		t.Fatalf("kind activity page = %+v, err=%v", kindPage, err)
	}

	spotRows, err := idb.ScanSpotAccountOrders("alice", "", "open", 10)
	if err != nil || len(spotRows) != 1 || !bytes.Equal(spotRows[keys.KeySpotAccountOrderRecentFromState(spot)], spotRaw) {
		t.Fatalf("spot rows = %v, err=%v", spotRows, err)
	}
	futureRows, err := idb.ScanFutureAccountOrders("alice", "BTC_USDT", "filled", 10)
	if err != nil || len(futureRows) != 1 || !bytes.Equal(futureRows[futurePrimary], futureRaw) {
		t.Fatalf("future rows = %v, err=%v", futureRows, err)
	}
}

func newReadModelTestDB(t *testing.T) *IndexDB {
	t.Helper()
	idb, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idb.Close() })
	return idb
}

func readModelTestBlock(height uint64, blockID, parentID string) *pb.Block {
	return &pb.Block{BlockHash: blockID, Header: &pb.BlockHeader{Height: height, PrevBlockHash: parentID}}
}

func sealedReadModel(t *testing.T, block *pb.Block, mutations ...*pb.ReadModelMutation) *pb.BlockReadModel {
	t.Helper()
	model := &pb.BlockReadModel{
		SchemaVersion: pb.BlockReadModelSchemaVersion,
		Height:        block.Header.Height,
		BlockId:       block.BlockHash,
		ParentId:      block.Header.PrevBlockHash,
		Mutations:     mutations,
	}
	if err := pb.SealBlockReadModel(model); err != nil {
		t.Fatal(err)
	}
	return model
}

func readModelPut(ordinal uint32, key string, value []byte, retention pb.ReadModelRetention) *pb.ReadModelMutation {
	return &pb.ReadModelMutation{
		Ordinal:   ordinal,
		Key:       key,
		Value:     value,
		Op:        pb.ReadModelMutationOp_READ_MODEL_MUTATION_PUT,
		Retention: retention,
	}
}

func assertIndexValue(t *testing.T, idb *IndexDB, key, want string) {
	t.Helper()
	got, err := idb.Get(key)
	if err != nil || got != want {
		t.Fatalf("key %q = %q, err=%v; want %q", key, got, err, want)
	}
}

func manifestHasKey(manifest *heightManifest, want string) bool {
	if manifest == nil {
		return false
	}
	for _, key := range manifest.Keys {
		if key == want {
			return true
		}
	}
	return false
}
