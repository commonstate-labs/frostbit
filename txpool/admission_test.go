package txpool

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"dex/config"
	"dex/db"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"dex/rent"
	"dex/txverify"
	"dex/utils"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
)

type realAdmissionValidator struct{}

func (realAdmissionValidator) CheckAnyTx(tx *pb.AnyTx) error { return txverify.Verify(tx, 1) }

func newAdmissionPool(t *testing.T, configure func(*config.Config), localAddress string) *TxPool {
	t.Helper()
	logger := logs.NewNodeLogger("admission-test", 0)
	dbm, err := db.NewManager(filepath.Join(t.TempDir(), "db"), logger)
	if err != nil {
		t.Fatal(err)
	}
	dbm.InitWriteQueue(256, time.Millisecond)
	t.Cleanup(func() { dbm.Close() })
	cfg := config.DefaultConfig()
	cfg.TxPool.AppliedBloomEstimatedItems = 1000
	if configure != nil {
		configure(cfg)
	}
	pool, err := NewTxPoolWithConfig(dbm, realAdmissionValidator{}, localAddress, logger, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.Stop() })
	return pool
}

func setAdmissionAccount(t *testing.T, pool *TxPool, owner string, nonce uint64, balance string) {
	t.Helper()
	for key, record := range map[string]proto.Message{
		keys.KeyAccount(owner):       &pb.Account{Nonce: nonce},
		keys.KeyBalance(owner, "FB"): &pb.TokenBalanceRecord{Balance: &pb.TokenBalance{Balance: balance}},
	} {
		data, err := proto.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		pool.dbManager.EnqueueSet(key, string(data))
	}
	if err := pool.dbManager.ForceFlushHot(); err != nil {
		t.Fatal(err)
	}
}

func setAdmissionRentClock(t *testing.T, pool *TxPool, owner string, lastAccrued, currentHeight uint64, nominal string) {
	t.Helper()
	state := rent.RentState{
		Version: rent.RentStateVersion, Owner: owner, LastAccruedHeight: lastAccrued,
		Mode: rent.RentModeCharging, ExpireHeight: currentHeight + 100, NominalFB: nominal,
		Work: &rent.RentWork{NextActionHeight: currentHeight + 100, Phase: rent.RentWorkSettle},
	}
	data, err := json.Marshal(&state)
	if err != nil {
		t.Fatal(err)
	}
	pool.dbManager.EnqueueSet(keys.KeyRentState(owner), string(data))
	pool.dbManager.EnqueueSet(keys.KeyLatestHeight(), fmt.Sprint(currentHeight))
	if err := pool.dbManager.ForceFlushHot(); err != nil {
		t.Fatal(err)
	}
}

func signAdmissionTx(t *testing.T, tx *pb.AnyTx, identity *utils.TestIdentity) *pb.AnyTx {
	t.Helper()
	base := tx.GetBase()
	base.FromAddress = identity.Address
	base.ChainId = 1
	base.PublicKeys = &pb.PublicKeys{Keys: map[int32][]byte{int32(pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1_DEX): identity.PublicKey}}
	base.TxId = tx.GenerateTxID()
	digest := sha256.Sum256([]byte(base.TxId))
	r, s, err := ecdsa.Sign(rand.Reader, identity.PrivateKey.ToECDSA(), digest[:])
	if err != nil {
		t.Fatal(err)
	}
	base.Signature = make([]byte, 64)
	r.FillBytes(base.Signature[:32])
	s.FillBytes(base.Signature[32:])
	if err := pb.Sanitize(tx); err != nil {
		t.Fatal(err)
	}
	if err := txverify.Verify(tx, 1); err != nil {
		t.Fatal(err)
	}
	return tx
}

func admissionTransfer(t *testing.T, identity *utils.TestIdentity, nonce uint64, amount string) *pb.AnyTx {
	t.Helper()
	return signAdmissionTx(t, &pb.AnyTx{Content: &pb.AnyTx_Transaction{Transaction: &pb.Transaction{
		Base: &pb.BaseMessage{Nonce: nonce, Fee: "1000"}, To: utils.GenerateTestIdentity(41999).Address, TokenAddress: "FB", Amount: amount,
	}}}, identity)
}

func admissionProtocol(t *testing.T, identity *utils.TestIdentity, checkpoint uint64) *pb.AnyTx {
	t.Helper()
	return signAdmissionTx(t, &pb.AnyTx{Content: &pb.AnyTx_CertSignTx{CertSignTx: &pb.CertSignTx{
		Base: &pb.BaseMessage{Nonce: checkpoint, Fee: "1000"}, CheckpointHeight: checkpoint,
	}}}, identity)
}

func assertAdmissionEmpty(t *testing.T, pool *TxPool) {
	t.Helper()
	if pool.PendingLen() != 0 || pool.pendingBytes != 0 || len(pool.pendingAccounts) != 0 || len(pool.pendingAdmissions) != 0 || pool.localProtocolPending != 0 {
		t.Fatalf("pending resources leaked: count=%d bytes=%d accounts=%d entries=%d protocol=%d", pool.PendingLen(), pool.pendingBytes, len(pool.pendingAccounts), len(pool.pendingAdmissions), pool.localProtocolPending)
	}
}

func TestAdmissionRejectsUnfundedAndFarFutureSignedTransactions(t *testing.T) {
	pool := newAdmissionPool(t, func(cfg *config.Config) { cfg.TxPool.MaxFutureNonceGap = 3 }, "")
	identity := utils.GenerateTestIdentity(41001)
	for _, nonce := range []uint64{0, 1000000} {
		tx := admissionTransfer(t, identity, nonce, "1")
		if err := pool.StoreAnyTx(tx); !errors.Is(err, ErrPendingAdmission) {
			t.Fatalf("nonce=%d rejection=%v", nonce, err)
		}
	}
	setAdmissionAccount(t, pool, identity.Address, 0, "100000")
	if err := pool.StoreAnyTx(admissionTransfer(t, identity, 4, "1")); !errors.Is(err, ErrPendingAdmission) {
		t.Fatalf("funded far nonce rejection=%v", err)
	}
	assertAdmissionEmpty(t, pool)
}

func TestAdmissionAllowsNonceWindowAndRejectsConflicts(t *testing.T) {
	pool := newAdmissionPool(t, func(cfg *config.Config) { cfg.TxPool.MaxFutureNonceGap = 3 }, "")
	identity := utils.GenerateTestIdentity(41002)
	setAdmissionAccount(t, pool, identity.Address, 10, "100000")
	txs := make(map[uint64]*pb.AnyTx)
	for _, nonce := range []uint64{13, 10, 12, 11} {
		tx := admissionTransfer(t, identity, nonce, "1")
		txs[nonce] = tx
		if err := pool.StoreAnyTx(tx); err != nil {
			t.Fatalf("valid reordered nonce %d: %v", nonce, err)
		}
	}
	if err := pool.StoreAnyTx(txs[11]); err != nil {
		t.Fatalf("identical duplicate rejected: %v", err)
	}
	if pool.PendingLen() != 4 {
		t.Fatalf("duplicate changed count: %d", pool.PendingLen())
	}
	for _, nonce := range []uint64{9, 14, 11} {
		if err := pool.StoreAnyTx(admissionTransfer(t, identity, nonce, "2")); !errors.Is(err, ErrPendingAdmission) {
			t.Fatalf("invalid/conflicting nonce %d: %v", nonce, err)
		}
	}
	setAdmissionAccount(t, pool, identity.Address, 11, "100000")
	pool.maintainPending(time.Now())
	if pool.HasTransaction(txs[10].GetTxId()) || pool.PendingLen() != 3 {
		t.Fatal("finalized account nonce did not prune stale pending only")
	}
}

func TestAdmissionAggregateFeesAndRemovalReleaseBudgets(t *testing.T) {
	pool := newAdmissionPool(t, nil, "")
	identity := utils.GenerateTestIdentity(41003)
	setAdmissionAccount(t, pool, identity.Address, 0, "2000")
	txs := []*pb.AnyTx{admissionTransfer(t, identity, 0, "1"), admissionTransfer(t, identity, 1, "1"), admissionTransfer(t, identity, 2, "1")}
	for _, tx := range txs[:2] {
		if err := pool.StoreAnyTx(tx); err != nil {
			t.Fatal(err)
		}
	}
	if err := pool.StoreAnyTx(txs[2]); !errors.Is(err, ErrPendingAdmission) || !strings.Contains(err.Error(), "fees") {
		t.Fatalf("fee sum admission=%v", err)
	}
	if err := pool.RemoveTx(txs[0].GetTxId()); err != nil {
		t.Fatal(err)
	}
	if err := pool.StoreAnyTx(txs[2]); err != nil {
		t.Fatalf("removed fee budget not released: %v", err)
	}
	pool.RemoveAppliedAnyTxs([]string{txs[1].GetTxId(), txs[1].GetTxId()})
	pool.RemoveAnyTx(txs[2].GetTxId())
	assertAdmissionEmpty(t, pool)
	state, err := pool.readAdmissionState(identity.Address)
	if err != nil || state.balance.String() != "2000" {
		t.Fatalf("local admission changed chain balance: %v %+v", err, state)
	}
}

func TestAdmissionUsesProjectedSpendableFBAfterRent(t *testing.T) {
	pool := newAdmissionPool(t, nil, "")
	identity := utils.GenerateTestIdentity(41030)
	setAdmissionAccount(t, pool, identity.Address, 0, "1500")
	setAdmissionRentClock(t, pool, identity.Address, 10, 20, "1500")

	state, err := pool.readAdmissionState(identity.Address)
	if err != nil || state.balance.String() != "400" {
		t.Fatalf("projected admission balance=%s err=%v", state.balance, err)
	}
	if err := pool.StoreAnyTx(admissionTransfer(t, identity, 0, "1")); !errors.Is(err, ErrPendingAdmission) || !strings.Contains(err.Error(), "fees") {
		t.Fatalf("rent-reserved FB was admitted for fees: %v", err)
	}
	assertAdmissionEmpty(t, pool)
}

func TestAdmissionMaintenancePrunesFeesAsRentAccrues(t *testing.T) {
	pool := newAdmissionPool(t, nil, "")
	identity := utils.GenerateTestIdentity(41031)
	setAdmissionAccount(t, pool, identity.Address, 0, "1600")
	setAdmissionRentClock(t, pool, identity.Address, 10, 15, "1600")
	tx := admissionTransfer(t, identity, 0, "1")
	if err := pool.StoreAnyTx(tx); err != nil {
		t.Fatalf("fee equal to projected spendable balance was rejected: %v", err)
	}

	pool.dbManager.EnqueueSet(keys.KeyLatestHeight(), "20")
	if err := pool.dbManager.ForceFlushHot(); err != nil {
		t.Fatal(err)
	}
	pool.maintainPending(time.Now())
	if pool.HasTransaction(tx.GetTxId()) {
		t.Fatal("pending fee reservation survived after projected rent consumed its balance")
	}
	assertAdmissionEmpty(t, pool)
}

func TestAdmissionTTLPrunesLegacyTransactionsAndLateSaves(t *testing.T) {
	pool := newAdmissionPool(t, func(cfg *config.Config) { cfg.TxPool.TxExpirationTime = time.Minute }, "")
	identity := utils.GenerateTestIdentity(41004)
	setAdmissionAccount(t, pool, identity.Address, 0, "1000")
	tx := admissionTransfer(t, identity, 0, "1")                                                    
	if err := pool.StoreAnyTx(tx); err != nil {
		t.Fatal(err)
	}
	if err := pool.dbManager.ForceFlush(); err != nil {
		t.Fatal(err)
	}
	pool.maintainPending(time.Now().Add(2 * time.Minute))
	pool.persistPendingAnyTx(tx, 0)                                             
	if err := pool.dbManager.ForceFlush(); err != nil {
		t.Fatal(err)
	}
	assertAdmissionEmpty(t, pool)
	persisted, err := pool.dbManager.LoadPendingAnyTx()
	if err != nil || len(persisted) != 0 {
		t.Fatalf("late save resurrected expired transaction: count=%d err=%v", len(persisted), err)
	}
}

func TestAdmissionRestartRevalidatesSignatureNonceAndFunding(t *testing.T) {
	pool := newAdmissionPool(t, func(cfg *config.Config) { cfg.TxPool.MaxFutureNonceGap = 3 }, "")
	identity := utils.GenerateTestIdentity(41005)
	unfunded := utils.GenerateTestIdentity(41006)
	setAdmissionAccount(t, pool, identity.Address, 1, "1000")
	valid := admissionTransfer(t, identity, 1, "1")
	forged := admissionTransfer(t, identity, 3, "1")
	forged.GetBase().Signature[0] ^= 0xff
	for _, tx := range []*pb.AnyTx{
		admissionTransfer(t, identity, 0, "1"), valid, admissionTransfer(t, identity, 2, "1"), forged,
		admissionTransfer(t, identity, 1000000, "1"), admissionTransfer(t, unfunded, 0, "1"),
	} {
		if err := pool.dbManager.SavePendingAnyTx(tx); err != nil {
			t.Fatal(err)
		}
	}
	if err := pool.dbManager.ForceFlush(); err != nil {
		t.Fatal(err)
	}
	restored, err := NewTxPoolWithConfig(pool.dbManager, realAdmissionValidator{}, "", pool.Logger, pool.cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restored.Stop() })
	if restored.PendingLen() != 1 || !restored.HasTransaction(valid.GetTxId()) {
		t.Fatalf("restart admitted invalid pending: %v", restored.GetPendingTxs())
	}
	if err := pool.dbManager.ForceFlush(); err != nil {
		t.Fatal(err)
	}
	persisted, err := pool.dbManager.LoadPendingAnyTx()
	if err != nil || len(persisted) != 1 || persisted[0].GetTxId() != valid.GetTxId() {
		t.Fatalf("restart failed persistent cleanup: count=%d err=%v", len(persisted), err)
	}
}

func TestAdmissionQueueByteBudgetIsReleasedAfterConsumption(t *testing.T) {
	identity := utils.GenerateTestIdentity(41007)
	first, second := admissionTransfer(t, identity, 0, "1"), admissionTransfer(t, identity, 1, "1")
	pool := newAdmissionPool(t, func(cfg *config.Config) {
		cfg.TxPool.MaxPendingBytes = 4 * int64(max(proto.Size(first), proto.Size(second)))
	}, "")
	setAdmissionAccount(t, pool, identity.Address, 0, "10000")
	added := make(chan string, 2)
	onAdded := func(id string) { added <- id }
	if err := pool.SubmitTx(first, "192.0.2.1", onAdded); err != nil {
		t.Fatal(err)
	}
	if err := pool.SubmitTx(second, "192.0.2.1", onAdded); !errors.Is(err, ErrPendingAdmission) {
		t.Fatalf("queue byte overflow=%v", err)
	}
	if pool.queuedBytes.Load() != int64(proto.Size(first)) {
		t.Fatal("rejected submission leaked queued bytes")
	}
	if err := pool.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-added:
	case <-time.After(3 * time.Second):
		t.Fatal("first signed transaction not consumed")
	}
	if pool.queuedBytes.Load() != 0 {
		t.Fatal("consumption did not release queued bytes")
	}
	if err := pool.SubmitTx(second, "192.0.2.1", onAdded); err != nil {
		t.Fatalf("released queue budget not reusable: %v", err)
	}
	select {
	case <-added:
	case <-time.After(3 * time.Second):
		t.Fatal("second signed transaction not consumed")
	}
	if pool.PendingLen() != 2 || pool.queuedBytes.Load() != 0 {
		t.Fatal("queue/pending accounting differs after processing")
	}
}

func TestSubmitTxAndWaitReturnsFinalAdmissionResult(t *testing.T) {
	pool := newAdmissionPool(t, nil, "")
	identity := utils.GenerateTestIdentity(41070)
	setAdmissionAccount(t, pool, identity.Address, 0, "10000")
	if err := pool.Start(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := pool.SubmitTxAndWait(ctx, admissionTransfer(t, identity, 0, "1"), "192.0.2.1", nil); err != nil {
		t.Fatalf("valid transaction admission failed: %v", err)
	}
	err := pool.SubmitTxAndWait(ctx, admissionTransfer(t, identity, 0, "2"), "192.0.2.1", nil)
	if !errors.Is(err, ErrPendingAdmission) || !strings.Contains(err.Error(), "account nonce already pending") {
		t.Fatalf("conflicting nonce result = %v", err)
	}
}

func TestAdmissionQueueFullDoesNotLeakByteReservation(t *testing.T) {
	identity := utils.GenerateTestIdentity(41008)
	pool := newAdmissionPool(t, nil, "")
	pool.Queue.MsgChan = make(chan *txPoolMessage, 1)
	first := admissionTransfer(t, identity, 0, "1")
	if err := pool.SubmitTx(first, "", nil); err != nil {
		t.Fatal(err)
	}
	if err := pool.SubmitTx(admissionTransfer(t, identity, 1, "1"), "", nil); err == nil || !strings.Contains(err.Error(), "queue is full") {
		t.Fatalf("queue capacity admission=%v", err)
	}
	if pool.queuedBytes.Load() != int64(proto.Size(first)) {
		t.Fatal("full channel leaked byte reservation")
	}
}

func TestAdmissionLocalProtocolReserveRequiresAuthenticSignature(t *testing.T) {
	local := utils.GenerateTestIdentity(41009)
	remote := utils.GenerateTestIdentity(41010)
	pool := newAdmissionPool(t, func(cfg *config.Config) { cfg.TxPool.MaxPendingTxs = 10 }, local.Address)
	setAdmissionAccount(t, pool, local.Address, 0, "1000")
	setAdmissionAccount(t, pool, remote.Address, 0, "100000")
	for nonce := uint64(0); nonce < 9; nonce++ {
		if err := pool.StoreAnyTx(admissionTransfer(t, remote, nonce, "1")); err != nil {
			t.Fatal(err)
		}
	}
	if err := pool.StoreAnyTx(admissionProtocol(t, remote, 100)); err == nil {
		t.Fatal("remote self-declared protocol consumed local reserve")
	}
	pool.Queue.MsgChan = make(chan *txPoolMessage, 1)
	if err := pool.SubmitTx(admissionTransfer(t, remote, 9, "1"), "192.0.2.1", nil); err != nil {
		t.Fatalf("fill public validation queue: %v", err)
	}
	forged := admissionProtocol(t, local, 100)
	forged.GetBase().Signature[0] ^= 0xff
	if err := pool.SubmitTx(forged, "192.0.2.1", nil); err == nil {
		t.Fatal("forged local protocol signature consumed reserve")
	}
	if pool.localProtocolPending != 0 || pool.PendingLen() != 9 {
		t.Fatal("rejected protocol message changed capacity")
	}
	valid := admissionProtocol(t, local, 100)
	callback := false
	if err := pool.SubmitTx(valid, "", func(string) { callback = true }); err != nil || !callback {
		t.Fatalf("authentic local protocol could not bypass full public queue: callback=%v err=%v", callback, err)
	}
	if pool.PendingLen() != 10 || pool.localProtocolPending != 1 {
		t.Fatal("local reserve accounting incorrect")
	}
	if err := pool.StoreAnyTx(admissionProtocol(t, local, 200)); err == nil {
		t.Fatal("local protocol exceeded total pending limit")
	}
	if err := pool.RemoveTx(valid.GetTxId()); err != nil {
		t.Fatal(err)
	}
	if pool.localProtocolPending != 0 {
		t.Fatal("protocol reservation did not release")
	}
}

func TestAdmissionPruneDoesNotUseFeeSnapshotAfterConcurrentRemoval(t *testing.T) {
	pool := newAdmissionPool(t, nil, "")
	identity := utils.GenerateTestIdentity(41012)
	setAdmissionAccount(t, pool, identity.Address, 0, "2000")
	first := admissionTransfer(t, identity, 0, "1")
	second := admissionTransfer(t, identity, 1, "1")
	                                                                      
	                                                                 
	if pool.pendingPersistenceLock(first.GetTxId()) == pool.pendingPersistenceLock(second.GetTxId()) {
		second = admissionTransfer(t, identity, 1, "2")
	}
	if pool.pendingPersistenceLock(first.GetTxId()) == pool.pendingPersistenceLock(second.GetTxId()) {
		t.Fatal("test transaction lock collision")
	}
	for _, tx := range []*pb.AnyTx{first, second} {
		if err := pool.StoreAnyTx(tx); err != nil {
			t.Fatal(err)
		}
	}
	setAdmissionAccount(t, pool, identity.Address, 0, "1000")
	state, err := pool.readAdmissionState(identity.Address)
	if err != nil {
		t.Fatal(err)
	}
	lock := pool.pendingPersistenceLock(second.GetTxId())
	lock.Lock()
	held := true
	done := make(chan struct{})
	go func() { pool.prunePendingAccount(identity.Address, state, time.Now()); close(done) }()
	defer func() {
		if held {
			lock.Unlock()
		}
		<-done
	}()
	                                                                         
	                                                                           
	deadline := time.Now().Add(3 * time.Second)
	for {
		stacks := make([]byte, 128<<10)
		n := runtime.Stack(stacks, true)
		blocked := false
		for _, stack := range strings.Split(string(stacks[:n]), "\n\n") {
			if strings.Contains(stack, "(*TxPool).prunePendingAccount(") && strings.Contains(stack, "sync.(*Mutex).Lock") {
				blocked = true
				break
			}
		}
		if blocked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("maintenance did not reach the blocked deletion")
		}
		time.Sleep(time.Millisecond)
	}
	if err := pool.RemoveTx(first.GetTxId()); err != nil {
		t.Fatal(err)
	}
	lock.Unlock()
	held = false
	<-done
	if pool.PendingLen() != 1 || !pool.HasTransaction(second.GetTxId()) {
		t.Fatal("maintenance evicted a now-affordable transaction using stale fee allocation")
	}
	pool.maintainPending(time.Now())
	if !pool.HasTransaction(second.GetTxId()) {
		t.Fatal("fresh maintenance rejected the remaining funded transaction")
	}
}

func TestAdmissionAccountCountAndBytesAreBounded(t *testing.T) {
	for _, mode := range []string{"count", "bytes", "global bytes"} {
		t.Run(mode, func(t *testing.T) {
			identity := utils.GenerateTestIdentity(41011)
			first := admissionTransfer(t, identity, 0, "1")
			pool := newAdmissionPool(t, func(cfg *config.Config) {
				switch mode {
				case "count":
					cfg.TxPool.MaxPendingPerAccount = 1
				case "bytes":
					cfg.TxPool.MaxPendingAccountBytes = int64(proto.Size(first)) + 16
				case "global bytes":
					cfg.TxPool.MaxPendingBytes = int64(proto.Size(first)) * 3 / 2
				}
			}, "")
			setAdmissionAccount(t, pool, identity.Address, 0, "100000")
			if err := pool.StoreAnyTx(first); err != nil {
				t.Fatal(err)
			}
			if err := pool.StoreAnyTx(admissionTransfer(t, identity, 1, "1")); !errors.Is(err, ErrPendingAdmission) {
				t.Fatalf("%s budget error=%v", mode, err)
			}
			if pool.PendingLen() != 1 {
				t.Fatalf("%s overflow changed pending count", mode)
			}
		})
	}
}
