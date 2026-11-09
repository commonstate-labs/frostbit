package consensus

import (
	"crypto/sha256"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"dex/config"
	"dex/db"
	"dex/keys"
	"dex/liveness"
	"dex/logs"
	"dex/pb"
	statedb "dex/stateDB"
	"dex/types"
	"dex/utils"
	"dex/vm"

	"google.golang.org/protobuf/proto"
)

func TestSetFinalizedRevalidatesSuffixedVRFProof(t *testing.T) {
	logger := logs.NewNodeLogger("finalization-vrf-test", 0)
	dbm, err := db.NewManager(filepath.Join(t.TempDir(), "db"), logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dbm.Close() })

	keyManager := utils.NewKeyManager()
	if err := keyManager.InitKeyRandom(); err != nil {
		t.Fatal(err)
	}
	proposer := keyManager.GetAddress()
	blsKey := proposalValidationBLSKey(t, keyManager)
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
	livenessRaw, err := json.Marshal(&liveness.MinerInfo{
		Index:   7,
		Address: proposer,
		State:   liveness.StateActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dbm.SyncStateUpdates(1, []interface{}{
		&vm.WriteOp{Key: keys.KeyAccount(proposer), Value: accountRaw},
		&vm.WriteOp{Key: keys.KeyLivenessMiner(7), Value: livenessRaw},
	}); err != nil {
		t.Fatal(err)
	}

	output, proof, err := keyManager.GenerateVRF(1,
		0,
		"genesis",
		types.NodeID(proposer),
	)
	if err != nil {
		t.Fatal(err)
	}
	suffixedProof := append(append([]byte(nil), proof...), []byte("grind")...)
	suffixedOutput := sha256.Sum256(suffixedProof)
	if string(output) == string(suffixedOutput[:]) {
		t.Fatal("suffix fixture did not change VRF output")
	}
	stateRoot, _ := statedb.ReadFingerprintFromProvider(dbm)

	genesis := &types.Block{ID: "genesis", Header: types.BlockHeader{Height: 0}}
	mutated := &types.Block{ID: "suffixed-candidate", Header: types.BlockHeader{
		Height:    1,
		ParentID:  genesis.ID,
		StateRoot: string(stateRoot),
		Proposer:  proposer,
		Miner:     keys.KeyNode() + proposer,
		Window:    0,
		VRFProof:  suffixedProof,
		VRFOutput: suffixedOutput[:],
		PublicKey: blsKey,
	}}
	store := &RealBlockStore{
		dbManager:          dbm,
		blockCache:         map[string]*types.Block{genesis.ID: genesis, mutated.ID: mutated},
		heightIndex:        map[uint64][]*types.Block{0: {genesis}, 1: {mutated}},
		finalizedBlocks:    map[uint64]*types.Block{0: genesis},
		lastAccepted:       genesis,
		lastAcceptedHeight: 0,
		windowConfig: config.WindowConfig{
			Enabled: true,
			Stages:  []config.WindowStage{{Probability: 1}},
		},
	}

	err = store.SetFinalized(1, mutated.ID)
	if err == nil || !strings.Contains(err.Error(), "invalid VRF proof length") {
		t.Fatalf("finalization accepted suffixed proof: %v", err)
	}
	if store.lastAcceptedHeight != 0 || store.lastAccepted.ID != genesis.ID {
		t.Fatalf("failed finalization advanced accepted tip: height=%d block=%s", store.lastAcceptedHeight, store.lastAccepted.ID)
	}
}
