package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"dex/config"
	"dex/consensus"
	"dex/db"
	"dex/keys"
	"dex/lifecycle"
	"dex/logs"
	"dex/pb"
	"dex/txpool"
	"dex/txverify"
	"dex/utils"
	"dex/vm"

	"google.golang.org/protobuf/proto"
)

func newDebugMinerTestKey(t *testing.T) *utils.KeyManager {
	t.Helper()
	km := utils.NewKeyManager()
	if err := km.InitKeyRandom(); err != nil {
		t.Fatal(err)
	}
	return km
}

func TestBuildDebugMinerJoinUsesOnlyLocalIdentity(t *testing.T) {
	const chainID = uint64(51016)
	oldChainID := pb.GetDefaultChainID()
	pb.SetDefaultChainID(chainID)
	t.Cleanup(func() { pb.SetDefaultChainID(oldChainID) })

	km := newDebugMinerTestKey(t)
	hm := &HandlerManager{address: km.GetAddress(), keyMgr: km}
	hm.SetNodeAdvertiseAddr("  miner-06.test:6005  ")

	anyTx, txID, err := hm.buildDebugMinerTx(pb.OrderOp_ADD, defaultDebugMinerJoinAmount, 7)
	if err != nil {
		t.Fatalf("build debug miner registration: %v", err)
	}
	if err := txverify.Verify(anyTx, chainID); err != nil {
		t.Fatalf("verify signed registration: %v", err)
	}
	if txID == "" || anyTx.GetTxId() != txID {
		t.Fatalf("tx id mismatch: returned=%q base=%q", txID, anyTx.GetTxId())
	}

	minerTx := anyTx.GetMinerTx()
	if minerTx == nil {
		t.Fatal("missing MinerTx payload")
	}
	if minerTx.Base.FromAddress != km.GetAddress() || minerTx.Base.Nonce != 7 || minerTx.Base.ChainId != chainID {
		t.Fatalf("unexpected base identity: %+v", minerTx.Base)
	}
	if minerTx.RewardAddress != km.GetAddress() {
		t.Fatalf("reward address=%q want local hot address %q", minerTx.RewardAddress, km.GetAddress())
	}
	if minerTx.Endpoint != "miner-06.test:6005" {
		t.Fatalf("endpoint=%q want trusted local endpoint", minerTx.Endpoint)
	}
	wantBLS, err := km.BLSPublicKeyBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(minerTx.BlsPublicKey, wantBLS) {
		t.Fatal("registration did not use the local key manager's BLS public key")
	}
	digest, err := vm.MinerRegistrationPoPDigest(minerTx)
	if err != nil {
		t.Fatalf("registration digest: %v", err)
	}
	publicPoint, err := utils.DeserializePublicKey(minerTx.BlsPublicKey)
	if err != nil {
		t.Fatalf("deserialize registration BLS key: %v", err)
	}
	if err := utils.BLSVerifySignature(publicPoint, string(digest), minerTx.BlsProof); err != nil {
		t.Fatalf("verify registration proof: %v", err)
	}

	                                                                            
	                                                                        
	                                                                           
	state := vm.NewStateView(
		func(string) ([]byte, error) { return nil, nil },
		func(string) (map[string][]byte, error) { return map[string][]byte{}, nil },
	)
	accountData, err := proto.Marshal(&pb.Account{Address: km.GetAddress()})
	if err != nil {
		t.Fatal(err)
	}
	state.Set(keys.KeyAccount(km.GetAddress()), accountData)
	vm.SetBalance(state, km.GetAddress(), "FB", &pb.TokenBalance{Balance: "6400000000", MinerLockedBalance: "0"})
	_, receipt, err := (&vm.MinerTxHandler{}).DryRun(anyTx, state)
	if err != nil || receipt == nil || receipt.Status != "SUCCEED" {
		t.Fatalf("first miner registration rejected: receipt=%+v err=%v", receipt, err)
	}
}

func TestBuildDebugMinerStopDoesNotOverrideIdentity(t *testing.T) {
	km := newDebugMinerTestKey(t)
	hm := &HandlerManager{address: km.GetAddress(), keyMgr: km, nodeAdvertiseAddr: "trusted.test:6000"}
	anyTx, _, err := hm.buildDebugMinerTx(pb.OrderOp_REMOVE, "", 3)
	if err != nil {
		t.Fatalf("build remove: %v", err)
	}
	minerTx := anyTx.GetMinerTx()
	if minerTx == nil {
		t.Fatal("missing MinerTx payload")
	}
	if minerTx.Amount != "" || minerTx.RewardAddress != "" || minerTx.Endpoint != "" || len(minerTx.BlsPublicKey) != 0 || len(minerTx.BlsProof) != 0 {
		t.Fatalf("REMOVE carried mutable identity fields: %+v", minerTx)
	}
}

func TestDebugMinerRequestRejectsIdentityInjection(t *testing.T) {
	for _, rawURL := range []string{
		"/debug/toggle_miner?op=join&endpoint=attacker.example:6000",
		"/debug/toggle_miner?op=join&reward_address=fbc1attacker",
		"/debug/toggle_miner?op=join&bls_public_key=attacker",
		"/debug/toggle_miner?op=join&op=stop",
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, rawURL, nil)
		(&HandlerManager{}).HandleDebugToggleMiner(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d body=%q, want 400", rawURL, recorder.Code, recorder.Body.String())
		}
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/debug/toggle_miner?op=join", bytes.NewBufferString(`{"endpoint":"attacker.example:6000"}`))
	(&HandlerManager{}).HandleDebugToggleMiner(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("identity body status=%d body=%q, want 400", recorder.Code, recorder.Body.String())
	}
}

func TestDebugMinerJoinFailsClosedWithoutTrustedEndpoint(t *testing.T) {
	km := newDebugMinerTestKey(t)
	hm := &HandlerManager{address: km.GetAddress(), keyMgr: km}
	if _, _, err := hm.buildDebugMinerTx(pb.OrderOp_ADD, defaultDebugMinerJoinAmount, 0); err == nil {
		t.Fatal("registration without trusted local endpoint was accepted")
	}

	other := newDebugMinerTestKey(t)
	hm.nodeAdvertiseAddr = "trusted.test:6000"
	hm.address = other.GetAddress()
	if _, _, err := hm.buildDebugMinerTx(pb.OrderOp_ADD, defaultDebugMinerJoinAmount, 0); err == nil {
		t.Fatal("registration with mismatched local key manager was accepted")
	}
}

func newDebugMinerTestDB(t *testing.T) *db.Manager {
	t.Helper()
	dbManager, err := db.NewManager(t.TempDir(), logs.NewNodeLogger("debug-miner-test", 0))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(dbManager.Close)
	return dbManager
}

type debugMinerRejectingValidator struct{}

func (debugMinerRejectingValidator) CheckAnyTx(*pb.AnyTx) error { return nil }

type debugMinerStateWrite struct {
	key   string
	value []byte
}

func (op debugMinerStateWrite) GetKey() string   { return op.key }
func (op debugMinerStateWrite) GetValue() []byte { return op.value }
func (op debugMinerStateWrite) IsDel() bool      { return false }

func debugMinerPoolConfig() *config.Config {
	cfg := config.DefaultConfig()
	cfg.TxPool.PendingTxCacheSize = 64
	cfg.TxPool.ShortPendingTxCacheSize = 64
	cfg.TxPool.CacheTxSize = 64
	cfg.TxPool.ShortTxCacheSize = 64
	cfg.TxPool.AppliedBloomEstimatedItems = 1000
	cfg.TxPool.MessageQueueSize = 16
	return cfg
}

func TestDebugMinerAdmissionFailureClearsOrdinaryPause(t *testing.T) {
	km := newDebugMinerTestKey(t)
	dbManager := newDebugMinerTestDB(t)
	pool, err := txpool.NewTxPoolWithConfig(dbManager, debugMinerRejectingValidator{}, km.GetAddress(), logs.NewNodeLogger("debug-miner-pool", 0), debugMinerPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	                                                                       
	                                                                           
	manager := &consensus.ConsensusNodeManager{}
	manager.SetPaused(true)
	hm := &HandlerManager{
		dbManager: dbManager, consensusManager: manager, txPool: pool,
		address: km.GetAddress(), keyMgr: km, nodeAdvertiseAddr: "miner.test:6000",
	}
	recorder := httptest.NewRecorder()
	hm.HandleDebugToggleMiner(recorder, httptest.NewRequest(http.MethodPost, "/debug/toggle_miner?op=join", nil))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("admission failure status=%d body=%q, want 500", recorder.Code, recorder.Body.String())
	}
	if manager.IsPaused() {
		t.Fatal("failed registration admission left the ordinary consensus pause active")
	}
}

func TestDebugMinerJoinAdmissionResumesAndQueuesCompleteIdentity(t *testing.T) {
	km := newDebugMinerTestKey(t)
	dbManager := newDebugMinerTestDB(t)
	accountData, err := db.ProtoMarshal(&pb.Account{Address: km.GetAddress()})
	if err != nil {
		t.Fatal(err)
	}
	balanceData, err := db.ProtoMarshal(&pb.TokenBalanceRecord{
		Address: km.GetAddress(), Token: "FB", Balance: &pb.TokenBalance{Balance: "6400000000"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dbManager.SyncFinalizedStateUpdates(1, []interface{}{
		debugMinerStateWrite{key: keys.KeyAccount(km.GetAddress()), value: accountData},
		debugMinerStateWrite{key: keys.KeyBalance(km.GetAddress(), "FB"), value: balanceData},
	}, false); err != nil {
		t.Fatal(err)
	}
	pool, err := txpool.NewTxPoolWithConfig(dbManager, debugMinerRejectingValidator{}, km.GetAddress(), logs.NewNodeLogger("debug-miner-success", 0), debugMinerPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	manager := &consensus.ConsensusNodeManager{}
	manager.SetPaused(true)
	hm := &HandlerManager{
		dbManager: dbManager, consensusManager: manager, txPool: pool,
		address: km.GetAddress(), keyMgr: km, nodeAdvertiseAddr: "miner.test:6000",
	}
	recorder := httptest.NewRecorder()
	hm.HandleDebugToggleMiner(recorder, httptest.NewRequest(http.MethodPost, "/debug/toggle_miner?op=join", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("join status=%d body=%q, want 200", recorder.Code, recorder.Body.String())
	}
	if manager.IsPaused() {
		t.Fatal("admitted join did not resume ordinary consensus pause")
	}
	var response struct {
		OK   bool   `json:"ok"`
		TxID string `json:"tx_id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	queued := pool.GetTransactionById(response.TxID)
	if !response.OK || response.TxID == "" || queued == nil || queued.GetMinerTx() == nil {
		t.Fatalf("join response=%+v queued=%v", response, queued != nil)
	}
	minerTx := queued.GetMinerTx()
	if minerTx.RewardAddress != km.GetAddress() || minerTx.Endpoint != "miner.test:6000" || len(minerTx.BlsPublicKey) == 0 || len(minerTx.BlsProof) == 0 {
		t.Fatalf("queued registration identity is incomplete: %+v", minerTx)
	}
}

func TestDebugMinerResumeDoesNotBypassEmergencyStop(t *testing.T) {
	emergency := lifecycle.New()
	emergency.Stop("test emergency")
	manager := &consensus.ConsensusNodeManager{}
	manager.SetEmergencyStop(emergency)
	manager.SetPaused(true)
	hm := &HandlerManager{consensusManager: manager}

	hm.resumeDebugMinerConsensus("failed-under-emergency", "registration failed")
	if !manager.IsPaused() {
		t.Fatal("debug miner recovery bypassed the emergency stop")
	}
	emergency.Resume()
	if manager.IsPaused() {
		t.Fatal("ordinary pause was not cleared underneath the emergency stop")
	}
}
