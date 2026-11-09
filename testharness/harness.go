package testharness

import (
	"context"
	"dex/certchain"
	"dex/config"
	"dex/db"
	runtimeTypes "dex/frost/runtime/types"
	"dex/keys"
	"dex/liveness"
	"dex/logs"
	"dex/pb"
	"dex/protocol"
	"dex/utils"
	"dex/vm"
	"dex/witness"
	"fmt"
	"math/big"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
)

                                              
type Option struct {
	livenessConfig *liveness.Config
	witnessConfig  *witness.Config
}

                                                                                
                                                                                                          
func WithLivenessConfig(cfg liveness.Config) Option {
	return Option{livenessConfig: &cfg}
}

                                                                              
                                                                                 
func WithWitnessConfig(cfg witness.Config) Option {
	return Option{witnessConfig: &cfg}
}

type Harness struct {
	t *testing.T

	DB             *db.Manager
	Exec           *vm.Executor
	Registry       *vm.HandlerRegistry
	WitnessService *witness.Service
	Liveness       *liveness.Tracker
	CertChain      *certchain.Tracker

	mu                 sync.Mutex
	height             uint64
	prevBlockHash      string
	blockSeq           uint64
	queue              []*pb.AnyTx
	txLog              []txLogEntry
	nonces             map[string]uint64
	nextOfflineReports []uint32                                                            
	nextSampledReports []uint32                                                            

	tmpDir    string
	closeOnce sync.Once

	Committee   []string
	PrivateKeys map[string]string
	PublicKeys  map[string][]byte
	KeyManagers map[string]*utils.KeyManager
}

type txLogEntry struct {
	Height uint64
	TxID   string
	Kind   string
	Status string
	Error  string
}

func New(t *testing.T, committeeSize int, opts ...Option) *Harness {
	t.Helper()

	                                                                                
	                                                                              
	vm.SetStrictAudit(true)

	tmpDir, err := os.MkdirTemp("", "dex_testharness_*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}

	logger := logs.NewNodeLogger("testharness", 0)
	dbMgr, err := db.NewManager(tmpDir, logger)
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		t.Fatalf("create db manager: %v", err)
	}
	dbMgr.InitWriteQueue(1024, 10*time.Millisecond)

	witnessCfg := witness.DefaultConfig()
	for _, o := range opts {
		if o.witnessConfig != nil {
			cfg := *o.witnessConfig
			witnessCfg = &cfg
		}
	}
	witnessSvc := witness.NewService(witnessCfg)
	if err := witnessSvc.Start(); err != nil {
		dbMgr.Close()
		_ = os.RemoveAll(tmpDir)
		t.Fatalf("start witness service: %v", err)
	}

	reg := vm.NewHandlerRegistry()
	if err := vm.RegisterDefaultHandlers(reg, witnessSvc); err != nil {
		witnessSvc.Stop()
		dbMgr.Close()
		_ = os.RemoveAll(tmpDir)
		t.Fatalf("register default handlers: %v", err)
	}

	exec := vm.NewExecutorWithWitnessService(dbMgr, reg, nil, witnessSvc)
	if err := exec.LoadWitnessState(); err != nil {
		witnessSvc.Stop()
		dbMgr.Close()
		_ = os.RemoveAll(tmpDir)
		t.Fatalf("load witness state: %v", err)
	}

	ltCfg := liveness.DefaultConfig()
	for _, o := range opts {
		if o.livenessConfig != nil {
			ltCfg = *o.livenessConfig
		}
	}
	lt := liveness.NewTracker(ltCfg)
	lt.SetStore(dbMgr)
	exec.LivenessTracker = lt

	ct := certchain.NewTracker(certchain.DefaultConfig())
	ct.SetStore(dbMgr)
	exec.CertChainTracker = ct

	h := &Harness{
		t:              t,
		DB:             dbMgr,
		Exec:           exec,
		Registry:       reg,
		WitnessService: witnessSvc,
		Liveness:       lt,
		CertChain:      ct,
		height:         0,
		prevBlockHash:  "genesis",
		tmpDir:         tmpDir,
		queue:          make([]*pb.AnyTx, 0),
		txLog:          make([]txLogEntry, 0),
		nonces:         make(map[string]uint64),
		Committee:      make([]string, 0, committeeSize),
		PrivateKeys:    make(map[string]string),
		PublicKeys:     make(map[string][]byte),
		KeyManagers:    make(map[string]*utils.KeyManager),
	}

	ids := utils.GenerateTestIdentities(committeeSize)
	for _, id := range ids {
		h.Committee = append(h.Committee, id.Address)
		h.PrivateKeys[id.Address] = id.PrivateKeyHex
		h.PublicKeys[id.Address] = id.PublicKey
		km := utils.NewKeyManager()
		if err := km.InitKey(id.PrivateKeyHex); err != nil {
			h.Close()
			t.Fatalf("init key for committee member: %v", err)
		}
		h.KeyManagers[id.Address] = km
	}

	if err := h.setLatestHeightLocked(0); err != nil {
		h.Close()
		t.Fatalf("seed latest height: %v", err)
	}

	t.Cleanup(h.Close)
	return h
}

func (h *Harness) Close() {
	h.closeOnce.Do(func() {
		if h.WitnessService != nil {
			h.WitnessService.Stop()
		}
		if h.DB != nil {
			h.DB.Close()
		}
		if h.tmpDir != "" {
			_ = os.RemoveAll(h.tmpDir)
		}
	})
}

func (h *Harness) Height() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.height
}

func (h *Harness) QueueLen() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.queue)
}

func (h *Harness) QueueTx(tx *pb.AnyTx) error {
	if tx == nil {
		return fmt.Errorf("nil tx")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.queueTxLocked(tx)
}

func (h *Harness) SubmitTx(tx *pb.AnyTx) error {
	return h.QueueTx(tx)
}

func (h *Harness) SubmitTxExpectSuccess(tx *pb.AnyTx) {
	h.t.Helper()

	if tx == nil {
		h.t.Fatalf("SubmitTxExpectSuccess: nil tx")
	}
	if tx.GetTxId() == "" {
		if base := tx.GetBase(); base != nil {
			base.TxId = nextTxID("tx")
		}
	}

	targetTxID := tx.GetTxId()
	if err := h.QueueTx(tx); err != nil {
		h.t.Fatalf("SubmitTxExpectSuccess queue: %v", err)
	}

	spec, err := h.MineBlock()
	if err != nil {
		h.t.Fatalf("SubmitTxExpectSuccess mine: %v", err)
	}
	rc := findReceipt(spec, targetTxID)
	if rc == nil {
		h.t.Fatalf("SubmitTxExpectSuccess: receipt not found for tx=%s", targetTxID)
	}
	if rc.Status != "SUCCEED" {
		h.t.Fatalf("SubmitTxExpectSuccess: tx=%s status=%s err=%s", targetTxID, rc.Status, rc.Error)
	}
}

func (h *Harness) SubmitBlock(txs ...*pb.AnyTx) {
	h.t.Helper()
	if _, err := h.MineBlock(txs...); err != nil {
		h.t.Fatalf("SubmitBlock: %v", err)
	}
}

func (h *Harness) MineBlock(txs ...*pb.AnyTx) (*vm.SpecResult, error) {
	return h.mineBlock("", txs...)
}

func (h *Harness) MineBlockAs(miner string, txs ...*pb.AnyTx) (*vm.SpecResult, error) {
	return h.mineBlock(miner, txs...)
}

func (h *Harness) mineBlock(miner string, txs ...*pb.AnyTx) (*vm.SpecResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for _, tx := range txs {
		if err := h.queueTxLocked(tx); err != nil {
			return nil, err
		}
	}

	pending := append([]*pb.AnyTx(nil), h.queue...)
	h.queue = h.queue[:0]

	height := h.height + 1
	h.blockSeq++
	blockHash := fmt.Sprintf("harness_block_%020d_%06d", height, h.blockSeq)
	stateRoot := h.DB.GetStateFingerprint()
	header := &pb.BlockHeader{
		Height:         height,
		PrevBlockHash:  h.prevBlockHash,
		Timestamp:      time.Now().Unix(),
		StateRoot:      append([]byte(nil), stateRoot...),
		Miner:          miner,
		OfflineReports: h.nextOfflineReports,
		SampledReports: h.nextSampledReports,
	}
	h.nextOfflineReports = nil
	h.nextSampledReports = nil
	block := &pb.Block{
		BlockHash: blockHash,
		Header:    header,
		Body:      pending,
	}

	spec, err := h.Exec.BuildSpecResult(block)
	if err != nil {
		h.queue = append(pending, h.queue...)
		return nil, fmt.Errorf("build spec result: %w", err)
	}
	if !spec.Valid {
		h.queue = append(pending, h.queue...)
		return spec, fmt.Errorf("invalid block: %s", spec.Reason)
	}
	if err := h.Exec.CommitFinalizedBlock(block); err != nil {
		h.queue = append(pending, h.queue...)
		return spec, fmt.Errorf("commit block: %w", err)
	}

	h.height = height
	h.prevBlockHash = blockHash
	if err := h.setLatestHeightLocked(height); err != nil {
		return spec, fmt.Errorf("update latest height key: %w", err)
	}

	h.recordTxLogLocked(pending, spec, height)
	return spec, nil
}

func (h *Harness) AdvanceHeight(n uint64) {
	h.t.Helper()
	for i := uint64(0); i < n; i++ {
		if _, err := h.MineBlock(); err != nil {
			h.t.Fatalf("AdvanceHeight(%d) failed at step %d: %v", n, i+1, err)
		}
	}
}

                                                                                                            
                                                                                                                       
func (h *Harness) SetOfflineReports(reports []uint32) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextOfflineReports = append([]uint32(nil), reports...)
}

                                                                                                            
                                                                                                   
                                                         
                                                     
  
                                                                                                
                                                                                                      
func (h *Harness) SetSampledReports(reports []uint32) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextSampledReports = append([]uint32(nil), reports...)
}

func (h *Harness) AutoAdvanceHeight(interval time.Duration) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = h.MineBlock()
			}
		}
	}()
	return cancel
}

func (h *Harness) RunUntil(cond func() bool, timeout time.Duration) bool {
	h.t.Helper()
	deadline := time.After(timeout)
	for {
		if cond() {
			return true
		}
		select {
		case <-deadline:
			h.t.Fatalf("RunUntil timeout after %v", timeout)
			return false
		case <-time.After(50 * time.Millisecond):
		}
	}
}

type harnessStateReader struct {
	h *Harness
}

func (r *harnessStateReader) Get(key string) ([]byte, bool, error) {
	raw, err := r.h.DB.Get(key)
	if err != nil {
		return nil, false, err
	}
	if len(raw) == 0 {
		return nil, false, nil
	}
	return append([]byte(nil), raw...), true, nil
}

func (r *harnessStateReader) Scan(prefix string, fn func(k string, v []byte) bool) error {
	kvs, err := r.h.DB.Scan(prefix)
	if err != nil {
		return err
	}
	keysList := make([]string, 0, len(kvs))
	for k := range kvs {
		keysList = append(keysList, k)
	}
	sort.Strings(keysList)
	for _, k := range keysList {
		if !fn(k, kvs[k]) {
			break
		}
	}
	return nil
}

func (h *Harness) StateReader() runtimeTypes.StateReader {
	return &harnessStateReader{h: h}
}

type harnessTxSubmitter struct {
	h *Harness
}

func (h *Harness) TxSubmitter() runtimeTypes.TxSubmitter {
	return &harnessTxSubmitter{h: h}
}

func (s *harnessTxSubmitter) Submit(tx any) (string, error) {
	anyTx, ok := tx.(*pb.AnyTx)
	if !ok {
		return "", fmt.Errorf("unsupported tx type: %T", tx)
	}
	if err := s.h.QueueTx(anyTx); err != nil {
		return "", err
	}
	return anyTx.GetTxId(), nil
}

func (s *harnessTxSubmitter) SubmitDkgCommitTx(_ context.Context, tx *pb.FrostVaultDkgCommitTx) error {
	if tx.Base != nil {
		tx.Base.Status = pb.Status_PENDING
	}
	return s.h.QueueTx(&pb.AnyTx{Content: &pb.AnyTx_FrostVaultDkgCommitTx{FrostVaultDkgCommitTx: tx}})
}

func (s *harnessTxSubmitter) SubmitDkgShareTx(_ context.Context, tx *pb.FrostVaultDkgShareTx) error {
	if tx.Base != nil {
		tx.Base.Status = pb.Status_PENDING
	}
	return s.h.QueueTx(&pb.AnyTx{Content: &pb.AnyTx_FrostVaultDkgShareTx{FrostVaultDkgShareTx: tx}})
}

func (s *harnessTxSubmitter) SubmitDkgComplaintTx(_ context.Context, tx *pb.FrostVaultDkgComplaintTx) error {
	if tx.Base != nil {
		tx.Base.Status = pb.Status_PENDING
	}
	return s.h.QueueTx(&pb.AnyTx{Content: &pb.AnyTx_FrostVaultDkgComplaintTx{FrostVaultDkgComplaintTx: tx}})
}

func (s *harnessTxSubmitter) SubmitDkgRevealTx(_ context.Context, tx *pb.FrostVaultDkgRevealTx) error {
	if tx.Base != nil {
		tx.Base.Status = pb.Status_PENDING
	}
	return s.h.QueueTx(&pb.AnyTx{Content: &pb.AnyTx_FrostVaultDkgRevealTx{FrostVaultDkgRevealTx: tx}})
}

func (s *harnessTxSubmitter) SubmitDkgValidationSignedTx(_ context.Context, tx *pb.FrostVaultDkgValidationSignedTx) error {
	if tx.Base != nil {
		tx.Base.Status = pb.Status_PENDING
	}
	return s.h.QueueTx(&pb.AnyTx{Content: &pb.AnyTx_FrostVaultDkgValidationSignedTx{FrostVaultDkgValidationSignedTx: tx}})
}

func (s *harnessTxSubmitter) SubmitWithdrawSignedTx(_ context.Context, tx *pb.FrostWithdrawSignedTx) error {
	if tx.Base != nil {
		tx.Base.Status = pb.Status_PENDING
	}
	return s.h.QueueTx(&pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: tx}})
}

func (s *harnessTxSubmitter) SubmitWithdrawClaimTx(_ context.Context, tx *pb.FrostWithdrawClaimTx) error {
	if tx.Base != nil {
		tx.Base.Status = pb.Status_PENDING
	}
	return s.h.QueueTx(&pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawClaimTx{FrostWithdrawClaimTx: tx}})
}

func (s *harnessTxSubmitter) SubmitTransitionSignedTx(_ context.Context, tx *pb.FrostVaultTransitionSignedTx) error {
	if tx.Base != nil {
		tx.Base.Status = pb.Status_PENDING
	}
	return s.h.QueueTx(&pb.AnyTx{Content: &pb.AnyTx_FrostVaultTransitionSignedTx{FrostVaultTransitionSignedTx: tx}})
}

func (h *Harness) RawSet(key string, val []byte) {
	h.t.Helper()
	h.DB.EnqueueSet(key, string(val))
	if err := h.DB.ForceFlush(); err != nil {
		h.t.Fatalf("RawSet(%s) flush failed: %v", key, err)
	}
}

func (h *Harness) RawGet(key string) ([]byte, bool) {
	h.t.Helper()
	v, err := h.DB.Get(key)
	if err != nil || len(v) == 0 {
		return nil, false
	}
	return append([]byte(nil), v...), true
}

func (h *Harness) SeedAccount(addr, token, balance string) {
	h.t.Helper()
	account := &pb.Account{Address: addr}
	if raw, ok := h.RawGet(keys.KeyAccount(addr)); ok && len(raw) > 0 {
		_ = proto.Unmarshal(raw, account)
		account.Address = addr
	}
	aData, _ := proto.Marshal(account)
	h.RawSet(keys.KeyAccount(addr), aData)

	bal := &pb.TokenBalanceRecord{
		Address: addr,
		Token:   token,
		Balance: &pb.TokenBalance{
			Balance:              balance,
			MinerLockedBalance:   "0",
			OrderFrozenBalance:   "0",
			WitnessLockedBalance: "0",
		},
	}
	bData, _ := proto.Marshal(bal)
	h.RawSet(keys.KeyBalance(addr, token), bData)
}

func (h *Harness) SeedMinerAccount(addr, balance, minerLocked string) {
	h.t.Helper()
	account := &pb.Account{Address: addr, IsMiner: true}
	if raw, ok := h.RawGet(keys.KeyAccount(addr)); ok && len(raw) > 0 {
		_ = proto.Unmarshal(raw, account)
		account.Address = addr
	}
	account.IsMiner = true
	if account.RewardAddress == "" {
		account.RewardAddress = addr
	}
	aData, _ := proto.Marshal(account)
	h.RawSet(keys.KeyAccount(addr), aData)

	bal := &pb.TokenBalanceRecord{
		Address: addr,
		Token:   "FB",
		Balance: &pb.TokenBalance{
			Balance:              balance,
			MinerLockedBalance:   minerLocked,
			OrderFrozenBalance:   "0",
			WitnessLockedBalance: "0",
		},
	}
	bData, _ := proto.Marshal(bal)
	h.RawSet(keys.KeyBalance(addr, "FB"), bData)
}

func (h *Harness) SeedVaultTransition(chain string, vaultID uint32, epochID uint64, committee []string,
	triggerHeight, commitDeadline, sharingDeadline, disputeDeadline uint64, signAlgo pb.SignAlgo,
) {
	h.t.Helper()
	n := uint32(len(committee))
	threshold, err := config.ComputeVaultCommitteeThreshold(len(committee))
	if err != nil {
		h.t.Fatalf("SeedVaultTransition: %v", err)
	}
	transition := &pb.VaultTransitionState{
		Chain:               chain,
		VaultId:             vaultID,
		EpochId:             epochID,
		SignAlgo:            signAlgo,
		TriggerHeight:       triggerHeight,
		DkgCommitDeadline:   commitDeadline,
		DkgSharingDeadline:  sharingDeadline,
		DkgDisputeDeadline:  disputeDeadline,
		OldCommitteeMembers: committee,
		NewCommitteeMembers: committee,
		DkgStatus:           "NOT_STARTED",
		DkgThresholdT:       uint32(threshold),
		DkgN:                n,
		ValidationStatus:    "NOT_STARTED",
		Lifecycle:           "ACTIVE",
	}
	data, _ := proto.Marshal(transition)
	h.RawSet(keys.KeyFrostVaultTransition(chain, vaultID, epochID), data)
}

func (h *Harness) SeedVaultConfig(chain string, vaultID uint32, vaultCount uint32) {
	h.t.Helper()
	if vaultID != 0 || vaultCount != 1 {
		h.t.Fatalf("SeedVaultConfig: protocol requires vaultID=0 and vaultCount=1, got vaultID=%d vaultCount=%d", vaultID, vaultCount)
	}
	cfg := localVaultConfig(chain)
	if strings.EqualFold(chain, "btc") {
		cfg.BtcNetwork = "regtest"
		cfg.BtcFeeSatsPerVbyte = 25
	}
	if err := config.ValidateOnChainVaultCommitteePolicy(cfg); err != nil {
		h.t.Fatalf("SeedVaultConfig: %v", err)
	}
	data, _ := proto.Marshal(cfg)
	h.RawSet(keys.KeyFrostVaultConfig(chain, vaultID), data)
}

func (h *Harness) SeedToken(tokenAddr string) {
	h.t.Helper()
	token := &pb.Token{Address: tokenAddr, TotalSupply: "0"}
	data, _ := proto.Marshal(token)
	h.RawSet(keys.KeyToken(tokenAddr), data)
}

func (h *Harness) SeedVaultState(chain string, vaultID uint32, status string, epochID uint64) {
	h.t.Helper()
	state := &pb.FrostVaultState{
		Chain:    chain,
		VaultId:  vaultID,
		Status:   status,
		KeyEpoch: epochID,
	}
	if status == "ACTIVE" {
		if len(h.Committee) < 4 {
			h.t.Fatalf("SeedVaultState: ACTIVE vault requires at least four test committee members")
		}
		state.CommitteeMembers = append([]string(nil), h.Committee[:4]...)
		state.CommitteeThreshold = 3
		state.ActiveSinceHeight = 1
		state.NextCommitteeReviewHeight = 1 + protocol.VaultCommitteeTermBlocks
	}
	data, _ := proto.Marshal(state)
	h.RawSet(keys.KeyFrostVaultState(chain, vaultID), data)
}

func (h *Harness) GetBalance(addr, token string) string {
	h.t.Helper()
	raw, ok := h.RawGet(keys.KeyBalance(addr, token))
	if !ok {
		return "0"
	}
	var rec pb.TokenBalanceRecord
	if err := proto.Unmarshal(raw, &rec); err != nil || rec.Balance == nil {
		return "0"
	}
	return rec.Balance.Balance
}

func (h *Harness) AssertBalance(addr, token, expected string) {
	h.t.Helper()
	got := h.GetBalance(addr, token)
	if got != expected {
		h.t.Fatalf("AssertBalance(%s,%s): want=%s got=%s", addr, token, expected, got)
	}
}

func (h *Harness) AssertBalanceGt(addr, token string, threshold int64) {
	h.t.Helper()
	got := h.GetBalance(addr, token)
	bal, ok := new(big.Int).SetString(got, 10)
	if !ok {
		h.t.Fatalf("AssertBalanceGt: invalid balance %s", got)
	}
	if bal.Int64() <= threshold {
		h.t.Fatalf("AssertBalanceGt(%s,%s): want > %d, got %s", addr, token, threshold, got)
	}
}

func (h *Harness) GetVaultState(chain string, vaultID uint32) *pb.FrostVaultState {
	h.t.Helper()
	raw, ok := h.RawGet(keys.KeyFrostVaultState(chain, vaultID))
	if !ok {
		return nil
	}
	var state pb.FrostVaultState
	if err := proto.Unmarshal(raw, &state); err != nil {
		return nil
	}
	return &state
}

func (h *Harness) AssertVaultActive(chain string, vaultID uint32) {
	h.t.Helper()
	state := h.GetVaultState(chain, vaultID)
	if state == nil {
		h.t.Fatalf("AssertVaultActive(%s,%d): vault state not found", chain, vaultID)
	}
	if state.Status != "ACTIVE" {
		h.t.Fatalf("AssertVaultActive(%s,%d): want ACTIVE got %s", chain, vaultID, state.Status)
	}
}

func (h *Harness) GetTransitionState(chain string, vaultID uint32, epochID uint64) *pb.VaultTransitionState {
	h.t.Helper()
	raw, ok := h.RawGet(keys.KeyFrostVaultTransition(chain, vaultID, epochID))
	if !ok {
		return nil
	}
	var s pb.VaultTransitionState
	if err := proto.Unmarshal(raw, &s); err != nil {
		return nil
	}
	return &s
}

func (h *Harness) AssertDkgStatus(chain string, vaultID uint32, epochID uint64, expected string) {
	h.t.Helper()
	ts := h.GetTransitionState(chain, vaultID, epochID)
	if ts == nil {
		h.t.Fatalf("AssertDkgStatus(%s,%d,%d): transition not found", chain, vaultID, epochID)
	}
	if ts.DkgStatus != expected {
		h.t.Fatalf("AssertDkgStatus(%s,%d,%d): want %s got %s", chain, vaultID, epochID, expected, ts.DkgStatus)
	}
}

func (h *Harness) GetRechargeRequest(requestID string) *pb.RechargeRequest {
	h.t.Helper()
	raw, ok := h.RawGet(keys.KeyRechargeRequest(requestID))
	if !ok {
		return nil
	}
	var req pb.RechargeRequest
	if err := proto.Unmarshal(raw, &req); err != nil {
		return nil
	}
	return &req
}

func (h *Harness) PrintTxLog() {
	h.t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	h.t.Log("=== TX Log ===")
	for _, e := range h.txLog {
		h.t.Logf("height=%d kind=%s txid=%s status=%s err=%s", e.Height, e.Kind, e.TxID, e.Status, e.Error)
	}
}

func (h *Harness) TxLog() []txLogEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]txLogEntry, len(h.txLog))
	copy(out, h.txLog)
	return out
}

func (h *Harness) TxLogCountByKind(kind string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, e := range h.txLog {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

func (h *Harness) TxLogHasKind(kind string) bool {
	return h.TxLogCountByKind(kind) > 0
}

func (h *Harness) queueTxLocked(tx *pb.AnyTx) error {
	if tx == nil {
		return fmt.Errorf("nil tx")
	}

	cloned := proto.Clone(tx)
	anyTx, ok := cloned.(*pb.AnyTx)
	if !ok {
		return fmt.Errorf("failed to clone tx")
	}
	base := anyTx.GetBase()
	if base != nil {
		if base.TxId == "" {
			base.TxId = nextTxID("tx")
		}
		if base.Fee == "" {
			base.Fee = "1000"
		}
		if base.FromAddress != "" {
			base.Nonce = h.nextNonceLocked(base.FromAddress)
		}
		if base.Status != pb.Status_FAILED && base.Status != pb.Status_SUCCEED {
			base.Status = pb.Status_PENDING
		}
	}
	h.queue = append(h.queue, anyTx)
	return nil
}

func (h *Harness) nextNonceLocked(addr string) uint64 {
	current, ok := h.nonces[addr]
	if !ok {
		if data, err := h.DB.Get(keys.KeyAccount(addr)); err == nil && len(data) > 0 {
			var acc pb.Account
			if err := proto.Unmarshal(data, &acc); err == nil {
				current = acc.Nonce
			}
		}
	}
	h.nonces[addr] = current + 1
	return current
}

func (h *Harness) setLatestHeightLocked(height uint64) error {
	h.DB.EnqueueSet(keys.KeyLatestHeight(), strconv.FormatUint(height, 10))
	return h.DB.ForceFlush()
}

func (h *Harness) recordTxLogLocked(blockTxs []*pb.AnyTx, spec *vm.SpecResult, height uint64) {
	receiptByID := make(map[string]*vm.Receipt)
	if spec != nil {
		for _, rc := range spec.Receipts {
			if rc == nil {
				continue
			}
			receiptByID[rc.TxID] = rc
		}
	}

	for _, tx := range blockTxs {
		if tx == nil {
			continue
		}
		kind, err := vm.DefaultKindFn(tx)
		if err != nil {
			kind = "unknown"
		}
		entry := txLogEntry{
			Height: height,
			TxID:   tx.GetTxId(),
			Kind:   kind,
		}
		if rc, ok := receiptByID[tx.GetTxId()]; ok {
			entry.Status = rc.Status
			entry.Error = rc.Error
		}
		h.txLog = append(h.txLog, entry)
	}
}

func findReceipt(spec *vm.SpecResult, txID string) *vm.Receipt {
	if spec == nil {
		return nil
	}
	for _, rc := range spec.Receipts {
		if rc != nil && rc.TxID == txID {
			return rc
		}
	}
	return nil
}

var txCounter uint64

func nextTxID(prefix string) string {
	n := atomic.AddUint64(&txCounter, 1)
	return fmt.Sprintf("%s_%d", prefix, n)
}

                                                                      
func NextTxID(prefix string) string { return nextTxID(prefix) }

func BuildTransferTx(from, to, token, amount string) *pb.AnyTx {
	return &pb.AnyTx{Content: &pb.AnyTx_Transaction{Transaction: &pb.Transaction{
		Base:         &pb.BaseMessage{TxId: nextTxID("transfer"), FromAddress: from, Status: pb.Status_PENDING, Fee: "1000"},
		To:           to,
		TokenAddress: token,
		Amount:       amount,
	}}}
}

func BuildOrderTx(from, baseToken, quoteToken, amount, price string, side pb.OrderSide) *pb.AnyTx {
	return &pb.AnyTx{Content: &pb.AnyTx_OrderTx{OrderTx: &pb.OrderTx{
		Base:       &pb.BaseMessage{TxId: nextTxID("order"), FromAddress: from, Status: pb.Status_PENDING, Fee: "1000"},
		BaseToken:  baseToken,
		QuoteToken: quoteToken,
		Amount:     amount,
		Price:      price,
		Side:       side,
		Op:         pb.OrderOp_ADD,
	}}}
}

func BuildMinerTx(addr string, op pb.OrderOp, amount string) *pb.AnyTx {
	return &pb.AnyTx{Content: &pb.AnyTx_MinerTx{MinerTx: &pb.MinerTx{
		Base:   &pb.BaseMessage{TxId: nextTxID("miner"), FromAddress: addr, Status: pb.Status_PENDING, Fee: "1000"},
		Op:     op,
		Amount: amount,
	}}}
}

                                                                             
                                                                             
func (h *Harness) BuildMinerTx(addr string, op pb.OrderOp, amount string) *pb.AnyTx {
	h.t.Helper()
	tx := BuildMinerTx(addr, op, amount)
	if op != pb.OrderOp_ADD {
		return tx
	}
	km := h.KeyManagers[addr]
	if !km.HasPrivateKey() {
		h.t.Fatalf("BuildMinerTx: missing key manager for %s", addr)
	}
	minerTx := tx.Content.(*pb.AnyTx_MinerTx).MinerTx
	minerTx.Base.PublicKeys = &pb.PublicKeys{Keys: map[int32][]byte{
		int32(pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1_DEX): km.PublicKeyBytes(),
	}}
	blsKey, err := km.BLSPublicKeyBytes()
	if err != nil {
		h.t.Fatalf("BuildMinerTx: derive BLS key: %v", err)
	}
	minerTx.BlsPublicKey = blsKey
	minerTx.Endpoint = "miner.test:10000"
	minerTx.RewardAddress = addr
	digest, err := vm.MinerRegistrationPoPDigest(minerTx)
	if err != nil {
		h.t.Fatalf("BuildMinerTx: registration digest: %v", err)
	}
	minerTx.BlsProof, err = km.SignBLS(digest)
	if err != nil {
		h.t.Fatalf("BuildMinerTx: BLS proof: %v", err)
	}
	return tx
}

func BuildWitnessStakeTx(addr string, op pb.OrderOp, amount string) *pb.AnyTx {
	return &pb.AnyTx{Content: &pb.AnyTx_WitnessStakeTx{WitnessStakeTx: &pb.WitnessStakeTx{
		Base:   &pb.BaseMessage{TxId: nextTxID("wstake"), FromAddress: addr, Status: pb.Status_PENDING, Fee: "1000"},
		Op:     op,
		Amount: amount,
	}}}
}

func BuildWitnessRequestTx(from, chain, nativeTxHash string, vout uint32, nativeScript []byte, nativeContract, amount, receiver, fee string) *pb.AnyTx {
	return &pb.AnyTx{Content: &pb.AnyTx_WitnessRequestTx{WitnessRequestTx: &pb.WitnessRequestTx{
		Base:            &pb.BaseMessage{TxId: nextTxID("wrequest"), FromAddress: from, Status: pb.Status_PENDING, Fee: "1000"},
		NativeChain:     chain,
		NativeTxHash:    nativeTxHash,
		NativeVout:      vout,
		NativeScript:    nativeScript,
		NativeContract:  nativeContract,
		Amount:          amount,
		ReceiverAddress: receiver,
		RechargeFee:     fee,
	}}}
}

func BuildDkgCommitTx(sender, chain string, vaultID uint32, epochID uint64, signAlgo pb.SignAlgo, commitPoints [][]byte, ai0 []byte) *pb.AnyTx {
	sessionID := fmt.Sprintf("%s_%d_%d_%d", chain, vaultID, epochID, 1)
	return &pb.AnyTx{Content: &pb.AnyTx_FrostVaultDkgCommitTx{FrostVaultDkgCommitTx: &pb.FrostVaultDkgCommitTx{
		Base:             &pb.BaseMessage{TxId: nextTxID("dkg_commit"), FromAddress: sender, Status: pb.Status_PENDING, Fee: "1000"},
		Chain:            chain,
		VaultId:          vaultID,
		EpochId:          epochID,
		SessionId:        sessionID,
		SignAlgo:         signAlgo,
		CommitmentPoints: commitPoints,
		AI0:              ai0,
	}}}
}

func BuildDkgShareTx(dealer, receiver, chain string, vaultID uint32, epochID uint64, ciphertext []byte) *pb.AnyTx {
	sessionID := fmt.Sprintf("%s_%d_%d_%d", chain, vaultID, epochID, 1)
	return &pb.AnyTx{Content: &pb.AnyTx_FrostVaultDkgShareTx{FrostVaultDkgShareTx: &pb.FrostVaultDkgShareTx{
		Base:       &pb.BaseMessage{TxId: nextTxID("dkg_share"), FromAddress: dealer, Status: pb.Status_PENDING, Fee: "1000"},
		Chain:      chain,
		VaultId:    vaultID,
		EpochId:    epochID,
		SessionId:  sessionID,
		DealerId:   dealer,
		ReceiverId: receiver,
		Ciphertext: ciphertext,
	}}}
}

func BuildDkgComplaintTx(receiver, dealer, chain string, vaultID uint32, epochID uint64) *pb.AnyTx {
	sessionID := fmt.Sprintf("%s_%d_%d_%d", chain, vaultID, epochID, 1)
	return &pb.AnyTx{Content: &pb.AnyTx_FrostVaultDkgComplaintTx{FrostVaultDkgComplaintTx: &pb.FrostVaultDkgComplaintTx{
		Base:       &pb.BaseMessage{TxId: nextTxID("dkg_complaint"), FromAddress: receiver, Status: pb.Status_PENDING, Fee: "1000"},
		Chain:      chain,
		VaultId:    vaultID,
		EpochId:    epochID,
		SessionId:  sessionID,
		DealerId:   dealer,
		ReceiverId: receiver,
	}}}
}

func BuildDkgRevealTx(dealer, receiver, chain string, vaultID uint32, epochID uint64, share, encRand []byte) *pb.AnyTx {
	sessionID := fmt.Sprintf("%s_%d_%d_%d", chain, vaultID, epochID, 1)
	return &pb.AnyTx{Content: &pb.AnyTx_FrostVaultDkgRevealTx{FrostVaultDkgRevealTx: &pb.FrostVaultDkgRevealTx{
		Base:       &pb.BaseMessage{TxId: nextTxID("dkg_reveal"), FromAddress: dealer, Status: pb.Status_PENDING, Fee: "1000"},
		Chain:      chain,
		VaultId:    vaultID,
		EpochId:    epochID,
		SessionId:  sessionID,
		DealerId:   dealer,
		ReceiverId: receiver,
		Share:      share,
		EncRand:    encRand,
	}}}
}

                                                                                     

                                                                    
func (h *Harness) SeedQueuedWithdraw(chain, asset, withdrawID, to, amount string, seq uint64) {
	h.t.Helper()
	state := &pb.FrostWithdrawState{
		WithdrawId:    withdrawID,
		Seq:           seq,
		Chain:         chain,
		Asset:         asset,
		To:            to,
		Amount:        amount,
		RequestHeight: h.height,
		Status:        "QUEUED",
	}
	data, _ := proto.Marshal(state)
	h.RawSet(keys.KeyFrostWithdraw(withdrawID), data)
	              
	h.RawSet(keys.KeyFrostWithdrawFIFOIndex(chain, asset, seq), []byte(withdrawID))
	                       
	h.RawSet(keys.KeyFrostWithdrawFIFOSeq(chain, asset), []byte(strconv.FormatUint(seq+1, 10)))
}

                                                      
func (h *Harness) SeedVaultAvailableBalance(chain, asset string, vaultID uint32, amount uint64) {
	h.t.Helper()
	h.RawSet(keys.KeyFrostVaultAvailableBalance(chain, asset, vaultID), []byte(strconv.FormatUint(amount, 10)))
}

                                    
func (h *Harness) SeedBtcUtxo(vaultID uint32, txid string, vout uint32, amount uint64, scriptPubKey []byte, confirmHeight uint64) {
	h.t.Helper()
	utxo := &pb.FrostUtxo{
		Txid:           txid,
		Vout:           vout,
		Amount:         strconv.FormatUint(amount, 10),
		VaultId:        vaultID,
		FinalizeHeight: confirmHeight,
		ScriptPubkey:   scriptPubKey,
	}
	data, _ := proto.Marshal(utxo)
	h.RawSet(keys.KeyFrostBtcUtxo(vaultID, txid, vout), data)
}

                                                               
func (h *Harness) SeedVaultConfigWithRefs(chain string, vaultCount uint32, refs []string) {
	h.t.Helper()
	if vaultCount != 1 || len(refs) != 1 {
		h.t.Fatalf("SeedVaultConfigWithRefs: protocol requires exactly one vault/ref, got vaultCount=%d refs=%d", vaultCount, len(refs))
	}
	cfg := localVaultConfig(chain)
	cfg.VaultRefs = refs
	if err := config.ValidateOnChainVaultCommitteePolicy(cfg); err != nil {
		h.t.Fatalf("SeedVaultConfigWithRefs: %v", err)
	}
	data, _ := proto.Marshal(cfg)
	h.RawSet(keys.KeyFrostVaultConfig(chain, 0), data)
}

func localVaultConfig(chain string) *pb.FrostVaultConfig {
	cfg := &pb.FrostVaultConfig{
		Chain:                         chain,
		GuardianPubkey:                harnessGuardianPubkey(chain),
		VaultCount:                    1,
		CommitteeSize:                 4,
		ThresholdRatio:                0.75,
		TransitionTriggerRatio:        0.2,
		DkgCommitWindowBlocks:         5,
		DkgSharingWindowBlocks:        5,
		DkgDisputeWindowBlocks:        5,
		VaultMaturityBlocks:           30,
		CommitteeTermBlocks:           protocol.VaultCommitteeTermBlocks,
		CommitteeScaleMinerMultiplier: 1,
		TransitionConfirmBlocks:       10,
		CommitteeMaxSize:              100,
	}
	if !strings.EqualFold(chain, "btc") {
		cfg.AuthorityProtocol, cfg.DepositProtocol, cfg.DepositDexChainId, cfg.EvmChainId = 3, 2, 771, 1
	}
	return cfg
}

                                                               
func (h *Harness) SeedTransitionKeyReady(chain string, vaultID uint32, epochID uint64, newGroupPubkey []byte, committee []string) {
	h.t.Helper()
	n := uint32(len(committee))
	threshold, err := config.ComputeVaultCommitteeThreshold(len(committee))
	if err != nil {
		h.t.Fatalf("SeedTransitionKeyReady: %v", err)
	}
	transition := &pb.VaultTransitionState{
		Chain:               chain,
		VaultId:             vaultID,
		EpochId:             epochID,
		SignAlgo:            pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		TriggerHeight:       1,
		DkgCommitDeadline:   100,
		DkgSharingDeadline:  200,
		DkgDisputeDeadline:  300,
		OldCommitteeMembers: committee,
		NewCommitteeMembers: committee,
		DkgStatus:           "KEY_READY",
		DkgThresholdT:       uint32(threshold),
		DkgN:                n,
		NewGroupPubkey:      newGroupPubkey,
		Lifecycle:           "ACTIVE",
	}
	data, _ := proto.Marshal(transition)
	h.RawSet(keys.KeyFrostVaultTransition(chain, vaultID, epochID), data)
}

                                            
func (h *Harness) GetWithdrawState(withdrawID string) *pb.FrostWithdrawState {
	h.t.Helper()
	raw, ok := h.RawGet(keys.KeyFrostWithdraw(withdrawID))
	if !ok {
		return nil
	}
	var s pb.FrostWithdrawState
	if err := proto.Unmarshal(raw, &s); err != nil {
		return nil
	}
	return &s
}

                                          
func (h *Harness) AssertWithdrawStatus(withdrawID, expected string) {
	h.t.Helper()
	s := h.GetWithdrawState(withdrawID)
	if s == nil {
		h.t.Fatalf("AssertWithdrawStatus(%s): withdraw not found", withdrawID)
	}
	if s.Status != expected {
		h.t.Fatalf("AssertWithdrawStatus(%s): want %s got %s", withdrawID, expected, s.Status)
	}
}
