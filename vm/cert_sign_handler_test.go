package vm_test

import (
	"testing"

	"dex/certchain"
	"dex/interfaces"
	"dex/keys"
	"dex/pb"
	"dex/utils"
	"dex/vm"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestCertSignUsesPrevMinerSetAfterMinerExit(t *testing.T) {
	db, executor, tx := newCertSignExecutorAndTx(t, 7, 7)

	res, err := executor.BuildSpecResult(&pb.Block{
		BlockHash: "block_cert_sign_prev_set_ok",
		Header: &pb.BlockHeader{
			PrevBlockHash: "genesis",
			Height:        6,
		},
		Body: []*pb.AnyTx{tx},
	})
	require.NoError(t, err)
	require.True(t, res.Valid)
	require.Len(t, res.Receipts, 1)
	require.Equal(t, "SUCCEED", res.Receipts[0].Status)
	require.Empty(t, res.Receipts[0].Error)

	balData, err := db.Get(keys.KeyBalance(tx.GetBase().FromAddress, "FB"))
	require.NoError(t, err)
	require.NotNil(t, balData)
}

func TestCertSignRejectsSignerIndexMismatch(t *testing.T) {
	_, executor, tx := newCertSignExecutorAndTx(t, 7, 8)

	res, err := executor.BuildSpecResult(&pb.Block{
		BlockHash: "block_cert_sign_index_mismatch",
		Header: &pb.BlockHeader{
			PrevBlockHash: "genesis",
			Height:        6,
		},
		Body: []*pb.AnyTx{tx},
	})
	require.NoError(t, err)
	require.True(t, res.Valid)
	require.Len(t, res.Receipts, 1)
	require.Equal(t, "FAILED", res.Receipts[0].Status)
	require.Contains(t, res.Receipts[0].Error, "signer index mismatch")
}

func TestCertSignRejectsCoordinatorSuppliedDigest(t *testing.T) {
	_, executor, tx := newCertSignExecutorAndTx(t, 7, 7)
	tx.GetCertSignTx().CheckpointDigest = []byte("attacker-digest")

	res, err := executor.BuildSpecResult(&pb.Block{
		BlockHash: "block_cert_sign_digest_mismatch",
		Header:    &pb.BlockHeader{PrevBlockHash: "genesis", Height: 6},
		Body:      []*pb.AnyTx{tx},
	})
	require.NoError(t, err)
	require.True(t, res.Valid)
	require.Len(t, res.Receipts, 1)
	require.Equal(t, "FAILED", res.Receipts[0].Status)
	require.Contains(t, res.Receipts[0].Error, "checkpoint digest mismatch")
}

func newCertSignExecutorAndTx(t *testing.T, trackerIndex uint32, txIndex uint32) (*MockDB, *vm.Executor, *pb.AnyTx) {
	t.Helper()

	km := utils.NewKeyManager()
	require.NoError(t, km.InitKeyRandom())

	blsPubBytes, err := km.BLSPublicKeyBytes()
	require.NoError(t, err)

	fromAddr := km.GetAddress()
	db := NewMockDB()

	accountData, err := proto.Marshal(&pb.Account{
		Address: fromAddr,
		IsMiner: false,
		PublicKeys: &pb.PublicKeys{
			Keys: map[int32][]byte{
				int32(pb.SignAlgo_SIGN_ALGO_BLS_BN256_G2): blsPubBytes,
			},
		},
	})
	require.NoError(t, err)
	db.data[keys.KeyAccount(fromAddr)] = accountData

	balanceData, err := proto.Marshal(&pb.TokenBalanceRecord{
		Balance: &pb.TokenBalance{
			Balance:            "1000000",
			MinerLockedBalance: "0",
		},
	})
	require.NoError(t, err)
	db.data[keys.KeyBalance(fromAddr, "FB")] = balanceData

	registry := vm.NewHandlerRegistry()
	require.NoError(t, vm.RegisterDefaultHandlers(registry))

	executor := vm.NewExecutor(db, registry, vm.NewSpecExecLRU(8))
	tracker := certchain.NewTracker(certchain.Config{CheckpointInterval: 5, Quorum: 0.667})
	tracker.UpdateMinerSet([]certchain.MinerSnapshot{{
		Index:           trackerIndex,
		Address:         fromAddr,
		PubKey:          blsPubBytes,
		EffectiveWeight: 100,
	}})
	tracker.OnCheckpointExecuted(5, []byte("block-5"), []byte("state-root-5"), []interfaces.CertMinerSnapshot{{
		Index:           trackerIndex,
		Address:         fromAddr,
		PublicKey:       blsPubBytes,
		EffectiveWeight: 100,
	}})
	executor.CertChainTracker = tracker

	digest, found := tracker.GetPendingCertDigest(5)
	require.True(t, found)
	blsSig, err := km.SignBLS(digest)
	require.NoError(t, err)

	return db, executor, &pb.AnyTx{
		Content: &pb.AnyTx_CertSignTx{CertSignTx: &pb.CertSignTx{
			Base: &pb.BaseMessage{
				TxId:        "tx_cert_sign_prev_set",
				FromAddress: fromAddr,
				Fee:         "1000",
			},
			CheckpointHeight: 5,
			SignerIndex:      txIndex,
			BlsSignature:     blsSig,
			CheckpointDigest: digest,
		}},
	}
}
