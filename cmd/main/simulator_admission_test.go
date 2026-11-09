package main

import (
	"dex/config"
	"dex/consensus"
	"dex/db"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"dex/txpool"
	"dex/txverify"
	"dex/utils"
	"math/big"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type simulatorSignedValidator struct{}

func (simulatorSignedValidator) CheckAnyTx(tx *pb.AnyTx) error {
	return txverify.Verify(tx, tx.GetBase().ChainId)
}

func newSimulatorAdmissionFixture(t *testing.T, configure func(*config.Config)) (*TxSimulator, *NodeInstance) {
	t.Helper()
	logger := logs.NewNodeLogger("sim-admission", 0)
	dbm, err := db.NewManager(filepath.Join(t.TempDir(), "db"), logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(dbm.Close)
	dbm.InitWriteQueue(256, time.Millisecond)
	priv := strings.Repeat("0", 63) + "1"
	key, err := utils.ParseSecp256k1PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	address, err := utils.DeriveDexAddress(key)
	if err != nil {
		t.Fatal(err)
	}
	syncSimulatorCapitalState(t, dbm, 1, 0, address, "FB", "100000000000")
	cfg := config.DefaultConfig()
	cfg.TxPool.AppliedBloomEstimatedItems = 1000
	if configure != nil {
		configure(cfg)
	}
	pool, err := txpool.NewTxPoolWithConfig(dbm, simulatorSignedValidator{}, address, logger, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.Stop() })
	node := &NodeInstance{Address: address, KeyManager: testRuntimeKey(t, priv), DBManager: dbm, TxPool: pool, ConsensusManager: &consensus.ConsensusNodeManager{}}
	s := &TxSimulator{nodes: []*NodeInstance{node}, submitLimiter: newSimTxLimiter(1000)}
	s.submitThrottle.Store(simThrottleFullPermille)
	return s, node
}

func simAdmissionTransfer(node *NodeInstance) func(uint64) *pb.AnyTx {
	return func(nonce uint64) *pb.AnyTx {
		return generateTransferTx(node.Address, node.Address, "FB", "1", "1000", nonce)
	}
}

func TestSimulatorAdmissionRejectDoesNotAdvanceNonce(t *testing.T) {
	s, node := newSimulatorAdmissionFixture(t, func(cfg *config.Config) { cfg.TxPool.MaxFutureNonceGap = 0 })
	_, first, ok := s.submitGeneratedPriorityTx(node, simAdmissionTransfer(node))
	if !ok || !node.TxPool.HasTransaction(first.GetTxId()) {
		t.Fatal("submission success must mean admitted, not merely queued")
	}
	for i := 0; i < 5; i++ {
		if _, _, ok := s.submitGeneratedPriorityTx(node, simAdmissionTransfer(node)); ok {
			t.Fatal("far future transaction reported as accepted")
		}
	}
	if got := s.nonces[node.Address]; got != 1 {
		t.Fatalf("rejected submissions advanced nonce to %d, want 1", got)
	}
	syncSimulatorCapitalState(t, node.DBManager, 2, 1, node.Address, "FB", "100000000000")
	node.TxPool.RemoveTx(first.GetTxId())
	if nonce, _, ok := s.submitGeneratedPriorityTx(node, simAdmissionTransfer(node)); !ok || nonce != 1 {
		t.Fatalf("resume = %d/%v, want 1/true", nonce, ok)
	}
}

func TestSimulatorAdmissionRecoversDroppedOriginalAndCapital(t *testing.T) {
	s, node := newSimulatorAdmissionFixture(t, nil)
	_, original, ok := s.submitGeneratedTxWithCapital(node, "FB", big.NewInt(5000), simAdmissionTransfer(node))
	if !ok {
		t.Fatal("initial submit failed")
	}
	_, next, ok := s.submitGeneratedPriorityTx(node, simAdmissionTransfer(node))
	if !ok {
		t.Fatal("second submit failed")
	}
	                                                                                                        
	node.TxPool.RemoveTx(original.GetTxId())
	s.recoverSimAccount(node)
	if !node.TxPool.HasTransaction(original.GetTxId()) || !node.TxPool.HasTransaction(next.GetTxId()) {
		t.Fatal("original gap was not restored")
	}
	if got, _ := s.availableSimCapital(node, "FB"); got.Cmp(big.NewInt(100000000000-5000)) != 0 {
		t.Fatalf("replay duplicated/released capital: %v", got)
	}
	syncSimulatorCapitalState(t, node.DBManager, 2, 2, node.Address, "FB", "99999995000")
	node.TxPool.RemoveTx(original.GetTxId())
	node.TxPool.RemoveTx(next.GetTxId())
	s.recoverSimAccount(node)
	if len(s.accountSubmissions[node.Address].pending) != 0 {
		t.Fatal("finalized originals retained")
	}
	if got, _ := s.availableSimCapital(node, "FB"); got.Cmp(big.NewInt(99999995000)) != 0 {
		t.Fatalf("finalized capital not released: %v", got)
	}
}

func TestSimulatorAdmissionRestartUsesFirstGapNotCachedOrMaximumNonce(t *testing.T) {
	s, node := newSimulatorAdmissionFixture(t, nil)
	firstNonce, first, ok := s.submitGeneratedPriorityTx(node, simAdmissionTransfer(node))
	if !ok || firstNonce != 0 {
		t.Fatal("first submit failed")
	}
	_, second, ok := s.submitGeneratedPriorityTx(node, simAdmissionTransfer(node))
	if !ok {
		t.Fatal("second submit failed")
	}
	                                                                                            
	node.TxPool.RemoveTx(first.GetTxId())
	restarted := &TxSimulator{nodes: []*NodeInstance{node}, nonces: map[string]uint64{node.Address: 999999}, submitLimiter: newSimTxLimiter(1000)}
	restarted.submitThrottle.Store(simThrottleFullPermille)
	if nonce, _, ok := restarted.submitGeneratedPriorityTx(node, simAdmissionTransfer(node)); !ok || nonce != 0 {
		t.Fatalf("gap fill=%d/%v, want 0/true", nonce, ok)
	}
	if !node.TxPool.HasTransaction(second.GetTxId()) {
		t.Fatal("existing future pending was replaced")
	}
	if nonce, _, ok := restarted.submitGeneratedPriorityTx(node, simAdmissionTransfer(node)); !ok || nonce != 2 {
		t.Fatalf("next=%d/%v, want 2/true", nonce, ok)
	}
	if _, ok := restarted.availableSimCapital(node, "FB"); ok {
		t.Fatal("untracked startup capital must wait for old window")
	}
	syncSimulatorCapitalState(t, node.DBManager, 2, 2, node.Address, "FB", "100000000000")
	if _, ok := restarted.availableSimCapital(node, "FB"); !ok {
		t.Fatal("startup window finalized but capital remains blocked")
	}
}

func TestSimulatorAdmissionConcurrentProducersStayInsideWindow(t *testing.T) {
	s, node := newSimulatorAdmissionFixture(t, func(c *config.Config) { c.TxPool.MaxFutureNonceGap = 7; c.TxPool.MaxPendingPerAccount = 8 })
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 12; i++ {
				s.submitGeneratedPriorityTx(node, simAdmissionTransfer(node))
			}
		}()
	}
	wg.Wait()
	status, err := node.TxPool.AccountPendingStatus(node.Address)
	if err != nil || status.Count != 8 || status.NextNonce != 8 {
		t.Fatalf("status=%+v/%v", status, err)
	}
	if s.nonces[node.Address] != 8 || len(s.accountSubmissions[node.Address].pending) != 8 {
		t.Fatal("nonce or recovery buffer escaped bounded window")
	}
}

func TestSimulatorAdmissionManyWindowFillDrainCycles(t *testing.T) {
	s, node := newSimulatorAdmissionFixture(t, func(c *config.Config) { c.TxPool.MaxFutureNonceGap = 3; c.TxPool.MaxPendingPerAccount = 4 })
	for round := uint64(0); round < 24; round++ {
		for i := uint64(0); i < 4; i++ {
			if nonce, _, ok := s.submitGeneratedPriorityTx(node, simAdmissionTransfer(node)); !ok || nonce != round*4+i {
				t.Fatalf("round=%d nonce=%d accepted=%v", round, nonce, ok)
			}
		}
		if _, _, ok := s.submitGeneratedPriorityTx(node, simAdmissionTransfer(node)); ok {
			t.Fatal("overfull window accepted")
		}
		pending := node.TxPool.PendingAccountTxs(node.Address)
		syncSimulatorCapitalState(t, node.DBManager, round+2, (round+1)*4, node.Address, "FB", "100000000000")
		for _, tx := range pending {
			node.TxPool.RemoveTx(tx.GetTxId())
		}
		s.recoverSimAccount(node)
		if len(s.accountSubmissions[node.Address].pending) != 0 {
			t.Fatal("recovery records leaked across cycles")
		}
	}
}

func TestSimulatorAdmissionStateReadFailureDoesNotGuessNonce(t *testing.T) {
	s, node := newSimulatorAdmissionFixture(t, nil)
	if _, _, ok := s.submitGeneratedPriorityTx(node, simAdmissionTransfer(node)); !ok {
		t.Fatal("initial submit")
	}
	_, err := node.DBManager.SyncFinalizedStateUpdates(2, []interface{}{testStateWriteOp{key: keys.KeyAccount(node.Address), value: []byte{0xff}}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := s.submitGeneratedPriorityTx(node, simAdmissionTransfer(node)); ok {
		t.Fatal("submitted with unreadable account state")
	}
	if s.nonces[node.Address] != 1 {
		t.Fatal("read failure reset local nonce")
	}
}
