package consensus

import (
	"bytes"
	"dex/pb"
	"dex/types"
	"dex/utils"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
)

func newCertifiedAnchorTestStore() *RealBlockStore {
	return &RealBlockStore{
		adapter:           NewConsensusAdapter(nil),
		blockCache:        make(map[string]*types.Block),
		heightIndex:       make(map[uint64][]*types.Block),
		finalizedBlocks:   make(map[uint64]*types.Block),
		finalizationChits: make(map[uint64]*types.FinalizationChits),
	}
}

func certifiedAnchorTestBlock(t *testing.T) *pb.Block {
	t.Helper()
	block := commitmentTestBlock(t)
	block.Header.VrfProof = []byte{0x10, 0x11, 0x12}
	block.Header.VrfOutput = []byte{0x20, 0x21, 0x22}
	block.Header.PublicKey = proposalValidationBLSKey(t, certifiedAnchorTestKey(t))
	recomputeCertifiedAnchorTestID(t, block)
	return block
}

func certifiedAnchorTestKey(t *testing.T) *utils.KeyManager {
	t.Helper()
	keyManager := utils.NewKeyManager()
	if err := keyManager.InitKey(strings.Repeat("01", 32)); err != nil {
		t.Fatalf("init certified anchor key: %v", err)
	}
	return keyManager
}

func recomputeCertifiedAnchorTestID(t *testing.T, block *pb.Block) {
	t.Helper()
	converted, err := (&ConsensusAdapter{}).DBBlockToConsensus(block)
	if err != nil {
		t.Fatalf("convert anchor header: %v", err)
	}
	block.BlockHash = converted.Header.ComputeIDForChain(pb.GetDefaultChainID())
	block.MinerSignature, err = signBlockProposal(certifiedAnchorTestKey(t), block.BlockHash)
	if err != nil {
		t.Fatalf("sign certified anchor: %v", err)
	}
}

func TestInstallCertifiedAnchorRetainsCompleteHeaderAndVRF(t *testing.T) {
	store := newCertifiedAnchorTestStore()
	anchor := certifiedAnchorTestBlock(t)
	previousAcceptedHeight := blockCacheAcceptedHeight.Load()
	defer blockCacheAcceptedHeight.Store(previousAcceptedHeight)
	defer clearCachedBlockForTest(anchor.BlockHash)

	if err := store.InstallCertifiedAnchor(anchor.Header.Height, anchor.BlockHash, anchor); err != nil {
		t.Fatalf("InstallCertifiedAnchor failed: %v", err)
	}

	got, ok := store.GetFinalizedAtHeight(anchor.Header.Height)
	if !ok || got == nil {
		t.Fatalf("certified anchor was not installed at height %d", anchor.Header.Height)
	}
	if got.ID != anchor.BlockHash || got.Header.Height != anchor.Header.Height {
		t.Fatalf("installed anchor identity = (%q,%d), want (%q,%d)", got.ID, got.Header.Height, anchor.BlockHash, anchor.Header.Height)
	}
	if got.Header.ParentID != anchor.Header.PrevBlockHash ||
		got.Header.Timestamp != anchor.Header.Timestamp ||
		got.Header.StateRoot != string(anchor.Header.StateRoot) ||
		got.Header.TxHash != anchor.Header.TxsHash ||
		got.Header.Miner != anchor.Header.Miner ||
		got.Header.Window != int(anchor.Header.Window) {
		t.Fatalf("installed anchor lost consensus header fields: got=%+v source=%+v", got.Header, anchor.Header)
	}
	if !bytes.Equal(got.Header.VRFProof, anchor.Header.VrfProof) ||
		!bytes.Equal(got.Header.VRFOutput, anchor.Header.VrfOutput) ||
		!bytes.Equal(got.Header.PublicKey, anchor.Header.PublicKey) {
		t.Fatalf("installed anchor lost VRF material: proof=%x output=%x key=%x", got.Header.VRFProof, got.Header.VRFOutput, got.Header.PublicKey)
	}
	if !equalUint32s(got.Header.OfflineReports, anchor.Header.OfflineReports) ||
		!equalUint32s(got.Header.SampledReports, anchor.Header.SampledReports) {
		t.Fatalf("installed anchor lost liveness reports: offline=%v sampled=%v", got.Header.OfflineReports, got.Header.SampledReports)
	}

	lastID, lastHeight := store.GetLastAccepted()
	if lastID != anchor.BlockHash || lastHeight != anchor.Header.Height {
		t.Fatalf("last accepted = (%q,%d), want (%q,%d)", lastID, lastHeight, anchor.BlockHash, anchor.Header.Height)
	}
	byID, ok := store.Get(anchor.BlockHash)
	if !ok || byID == nil || !bytes.Equal(byID.Header.VRFOutput, anchor.Header.VrfOutput) {
		t.Fatalf("anchor lookup by ID did not retain full header: %+v", byID)
	}
	cached, ok := GetCachedBlock(anchor.BlockHash)
	if !ok || cached == nil || cached.Header == nil || !proto.Equal(cached, anchor) {
		t.Fatalf("complete certified payload was not retained in the block cache: %+v", cached)
	}
}

func TestInstallCertifiedAnchorRejectsMismatchedOrIncompleteBlock(t *testing.T) {
	valid := certifiedAnchorTestBlock(t)

	tests := []struct {
		name    string
		height  uint64
		blockID string
		mutate  func(*pb.Block)
		rehash  bool
		wantErr string
	}{
		{
			name:    "height mismatch",
			height:  valid.Header.Height + 1,
			blockID: valid.BlockHash,
			wantErr: "height",
		},
		{
			name:    "certificate hash mismatch",
			height:  valid.Header.Height,
			blockID: "different-certified-hash",
			wantErr: "mismatch",
		},
		{
			name:    "invalid commitment",
			height:  valid.Header.Height,
			blockID: valid.BlockHash,
			mutate: func(block *pb.Block) {
				block.Header.Timestamp++
			},
			wantErr: "commitment",
		},
		{
			name:    "missing parent",
			height:  valid.Header.Height,
			mutate:  func(block *pb.Block) { block.Header.PrevBlockHash = "" },
			rehash:  true,
			wantErr: "parent",
		},
		{
			name:   "short state root",
			height: valid.Header.Height,
			mutate: func(block *pb.Block) {
				block.Header.StateRoot = bytes.Repeat([]byte{0x44}, 31)
			},
			rehash:  true,
			wantErr: "state root",
		},
		{
			name:    "missing VRF proof",
			height:  valid.Header.Height,
			mutate:  func(block *pb.Block) { block.Header.VrfProof = nil },
			rehash:  true,
			wantErr: "VRF",
		},
		{
			name:    "missing VRF output",
			height:  valid.Header.Height,
			mutate:  func(block *pb.Block) { block.Header.VrfOutput = nil },
			rehash:  true,
			wantErr: "VRF",
		},
		{
			name:    "missing public key",
			height:  valid.Header.Height,
			mutate:  func(block *pb.Block) { block.Header.PublicKey = nil },
			rehash:  true,
			wantErr: "public key",
		},
		{
			name:    "missing miner",
			height:  valid.Header.Height,
			mutate:  func(block *pb.Block) { block.Header.Miner = "" },
			rehash:  true,
			wantErr: "proposer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newCertifiedAnchorTestStore()
			block := proto.Clone(valid).(*pb.Block)
			if tt.mutate != nil {
				tt.mutate(block)
			}
			if tt.rehash {
				recomputeCertifiedAnchorTestID(t, block)
			}
			blockID := tt.blockID
			if blockID == "" {
				blockID = block.BlockHash
			}
			defer clearCachedBlockForTest(block.BlockHash)

			err := store.InstallCertifiedAnchor(tt.height, blockID, block)
			if err == nil {
				t.Fatal("invalid certified anchor was accepted")
			}
			if tt.wantErr != "" && !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tt.wantErr)) {
				t.Fatalf("error %q does not identify %q", err, tt.wantErr)
			}
			assertCertifiedAnchorTestStoreUnchanged(t, store)
		})
	}
}

func TestInstallCertifiedAnchorRejectsNilBlockOrHeader(t *testing.T) {
	tests := []struct {
		name  string
		block *pb.Block
	}{
		{name: "nil block"},
		{name: "nil header", block: &pb.Block{BlockHash: "anchor-without-header"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newCertifiedAnchorTestStore()
			if err := store.InstallCertifiedAnchor(12, "certified-anchor", tt.block); err == nil {
				t.Fatal("incomplete certified anchor was accepted")
			}
			assertCertifiedAnchorTestStoreUnchanged(t, store)
		})
	}
}

func assertCertifiedAnchorTestStoreUnchanged(t *testing.T, store *RealBlockStore) {
	t.Helper()
	store.mu.RLock()
	defer store.mu.RUnlock()
	if store.lastAccepted != nil || store.lastAcceptedHeight != 0 || store.maxHeight != 0 {
		t.Fatalf("rejected anchor changed accepted state: block=%+v height=%d max=%d", store.lastAccepted, store.lastAcceptedHeight, store.maxHeight)
	}
	if len(store.blockCache) != 0 || len(store.heightIndex) != 0 || len(store.finalizedBlocks) != 0 {
		t.Fatalf("rejected anchor changed indexes: blocks=%d heights=%d finalized=%d", len(store.blockCache), len(store.heightIndex), len(store.finalizedBlocks))
	}
}

func equalUint32s(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
