package runtime

import (
	"context"
	"crypto/sha256"
	bridgeasset "dex/frost/asset"
	"dex/frost/chain"
	"dex/frost/chain/evm"
	"dex/frost/runtime/planning"
	"dex/frost/runtime/session"
	"dex/frost/runtime/workers"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"dex/utils"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
)

                                                     
type FakeNotifier struct {
	mu        sync.Mutex
	callbacks []func(height uint64)
}

func NewFakeNotifier() *FakeNotifier {
	return &FakeNotifier{
		callbacks: make([]func(height uint64), 0),
	}
}

                                                         
func (f *FakeNotifier) SubscribeBlockFinalized(fn func(height uint64)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callbacks = append(f.callbacks, fn)
}

                                           
func (f *FakeNotifier) TriggerFinalized(height uint64) {
	f.mu.Lock()
	callbacks := make([]func(height uint64), len(f.callbacks))
	copy(callbacks, f.callbacks)
	f.mu.Unlock()

	for _, cb := range callbacks {
		cb(height)
	}
}

func TestManager_StartStop(t *testing.T) {
	notifier := NewFakeNotifier()

	m := NewManager(
		ManagerConfig{NodeID: "test-node-1"},
		ManagerDeps{
			Notifier:       notifier,
			Logger:         logs.NewNodeLogger("test", 100),
			StateReader:    newFakeStateReader(),
			VaultProvider:  newFakeVaultProvider(),
			SignerProvider: newFakeVaultProvider(),
			TxSubmitter:    newFakeTxSubmitter(),
			AdapterFactory: newFakeAdapterFactory(),
		},
	)

	ctx := context.Background()

	                     
	if m.IsRunning() {
		t.Fatal("Manager should not be running before Start")
	}

	         
	if err := m.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if !m.IsRunning() {
		t.Fatal("Manager should be running after Start")
	}

	         
	m.Stop()

	if m.IsRunning() {
		t.Fatal("Manager should not be running after Stop")
	}
}

func TestManagerUsesInjectedNonceStore(t *testing.T) {
	nonceStore := session.NewMemoryNonceStore()
	m := NewManager(
		ManagerConfig{NodeID: "test-node-nonce-store"},
		ManagerDeps{
			NonceStore:     nonceStore,
			Logger:         logs.NewNodeLogger("test", 100),
			StateReader:    newFakeStateReader(),
			VaultProvider:  newFakeVaultProvider(),
			SignerProvider: newFakeVaultProvider(),
			TxSubmitter:    newFakeTxSubmitter(),
			AdapterFactory: newFakeAdapterFactory(),
		},
	)

	nonce := []byte("manager-injected-nonce-store")
	if err := m.sessionStore.BindNonce(nonce, []byte("context"), 1); err != nil {
		t.Fatalf("bind through manager session store: %v", err)
	}
	if _, _, exists := nonceStore.GetNonceBind(nonce); !exists {
		t.Fatal("manager did not use injected nonce store")
	}
}

func TestManagerWithoutNonceStoreFailsClosed(t *testing.T) {
	m := NewManager(
		ManagerConfig{NodeID: "test-node-missing-nonce-store"},
		ManagerDeps{
			Logger:         logs.NewNodeLogger("test", 100),
			StateReader:    newFakeStateReader(),
			VaultProvider:  newFakeVaultProvider(),
			SignerProvider: newFakeVaultProvider(),
			TxSubmitter:    newFakeTxSubmitter(),
			AdapterFactory: newFakeAdapterFactory(),
		},
	)

	err := m.sessionStore.BindNonce([]byte("nonce"), []byte("context"), 1)
	if !errors.Is(err, session.ErrNonceAlreadyBound) {
		t.Fatalf("missing NonceStore bind = %v, want fail-closed ErrNonceAlreadyBound", err)
	}
}

func TestManager_ReceiveFinalizedEvent(t *testing.T) {
	notifier := NewFakeNotifier()

	m := NewManager(
		ManagerConfig{NodeID: "test-node-1"},
		ManagerDeps{
			Notifier:       notifier,
			Logger:         logs.NewNodeLogger("test", 100),
			StateReader:    newFakeStateReader(),
			VaultProvider:  newFakeVaultProvider(),
			SignerProvider: newFakeVaultProvider(),
			TxSubmitter:    newFakeTxSubmitter(),
			AdapterFactory: newFakeAdapterFactory(),
		},
	)

	ctx := context.Background()

	                        
	receivedHeights := make(chan uint64, 10)
	m.SetOnBlockFinalized(func(height uint64) {
		receivedHeights <- height
	})

	                 
	if err := m.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer m.Stop()

	                          
	testHeights := []uint64{100, 101, 102, 200, 500}

	for _, h := range testHeights {
		notifier.TriggerFinalized(h)
	}

	                        
	for _, expected := range testHeights {
		select {
		case received := <-receivedHeights:
			if received != expected {
				t.Errorf("Expected height %d, got %d", expected, received)
			}
		case <-time.After(time.Second):
			t.Fatalf("Timeout waiting for height %d", expected)
		}
	}

	                             
	if got := m.LastFinalizedHeight(); got != 500 {
		t.Errorf("LastFinalizedHeight() = %d, want 500", got)
	}
}

func TestManager_FinalizedHeightMonotonicDedupe(t *testing.T) {
	logger := logs.NewNodeLogger("test-finalized-dedupe", 100)
	m := &Manager{
		Logger:      logger,
		finalizedCh: make(chan uint64, 10),
	}
	m.running.Store(true)

	callbacks := make(chan uint64, 10)
	m.SetOnBlockFinalized(func(height uint64) {
		callbacks <- height
	})

	for _, height := range []uint64{100, 100, 99, 101, 101, 100, 102} {
		m.handleBlockFinalized(height)
	}

	want := []uint64{100, 101, 102}
	for i, expected := range want {
		select {
		case got := <-m.finalizedCh:
			if got != expected {
				t.Fatalf("finalizedCh[%d] = %d, want %d", i, got, expected)
			}
		default:
			t.Fatalf("finalizedCh missing height %d", expected)
		}
		select {
		case got := <-callbacks:
			if got != expected {
				t.Fatalf("callback[%d] = %d, want %d", i, got, expected)
			}
		default:
			t.Fatalf("callback missing height %d", expected)
		}
	}
	if got := len(m.finalizedCh); got != 0 {
		t.Fatalf("finalizedCh has %d duplicate or regressing events", got)
	}
	if got := len(callbacks); got != 0 {
		t.Fatalf("callback has %d duplicate or regressing events", got)
	}
	if got := m.LastFinalizedHeight(); got != 102 {
		t.Fatalf("LastFinalizedHeight() = %d, want 102", got)
	}
	if got := countFinalizedManagerLogs(logger); got != len(want) {
		t.Fatalf("finalized log count = %d, want %d", got, len(want))
	}
}

func TestManager_ConcurrentDuplicateFinalizedHeight(t *testing.T) {
	logger := logs.NewNodeLogger("test-finalized-concurrent", 256)
	m := &Manager{
		Logger:      logger,
		finalizedCh: make(chan uint64, 128),
	}
	m.running.Store(true)

	callbacks := make(chan uint64, 128)
	m.SetOnBlockFinalized(func(height uint64) {
		callbacks <- height
	})

	const callers = 128
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func() {
			defer wg.Done()
			<-start
			m.handleBlockFinalized(500)
		}()
	}
	close(start)
	wg.Wait()

	if got := len(m.finalizedCh); got != 1 {
		t.Fatalf("finalizedCh length = %d, want 1", got)
	}
	if got := <-m.finalizedCh; got != 500 {
		t.Fatalf("finalizedCh height = %d, want 500", got)
	}
	if got := len(callbacks); got != 1 {
		t.Fatalf("callback count = %d, want 1", got)
	}
	if got := <-callbacks; got != 500 {
		t.Fatalf("callback height = %d, want 500", got)
	}
	if got := m.LastFinalizedHeight(); got != 500 {
		t.Fatalf("LastFinalizedHeight() = %d, want 500", got)
	}
	if got := countFinalizedManagerLogs(logger); got != 1 {
		t.Fatalf("finalized log count = %d, want 1", got)
	}
}

func countFinalizedManagerLogs(logger logs.Logger) int {
	count := 0
	for _, line := range logger.GetBuffer() {
		if line != nil && strings.HasPrefix(line.Message, "[FrostManager] Block finalized: height=") {
			count++
		}
	}
	return count
}

func TestManager_IgnoreEventsAfterStop(t *testing.T) {
	notifier := NewFakeNotifier()

	m := NewManager(
		ManagerConfig{NodeID: "test-node-1"},
		ManagerDeps{
			Notifier:       notifier,
			Logger:         logs.NewNodeLogger("test", 100),
			StateReader:    newFakeStateReader(),
			VaultProvider:  newFakeVaultProvider(),
			SignerProvider: newFakeVaultProvider(),
			TxSubmitter:    newFakeTxSubmitter(),
			AdapterFactory: newFakeAdapterFactory(),
		},
	)

	ctx := context.Background()

	callCount := 0
	m.SetOnBlockFinalized(func(height uint64) {
		callCount++
	})

	                              
	if err := m.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	notifier.TriggerFinalized(100)
	time.Sleep(10 * time.Millisecond)                

	         
	m.Stop()

	                  
	notifier.TriggerFinalized(101)
	notifier.TriggerFinalized(102)
	time.Sleep(10 * time.Millisecond)

	                                       
	if callCount != 1 {
		t.Errorf("Expected 1 call, got %d", callCount)
	}
}

func TestManagerLoadsTokenRegistryChainAssets(t *testing.T) {
	reader := newFakeStateReader()
	identity, err := bridgeasset.Resolve("bnb", "0x8ac76a51cc950d9822d68b83fe1ad97b32cd580d")
	if err != nil {
		t.Fatal(err)
	}
	token := &pb.Token{
		Address:        identity.ID,
		Type:           pb.TokenType_TOKEN_BRIDGED,
		NativeChain:    identity.Chain,
		NativeContract: identity.Contract,
	}
	tokenData, _ := proto.Marshal(token)
	reader.Set(keys.KeyToken(token.Address), tokenData)
	reader.Set(keys.KeyNativeTokenIndex(identity.Chain, identity.Contract), []byte(identity.ID))

	m := &Manager{
		stateReader: reader,
		config: ManagerConfig{SupportedChains: []ChainAssetPair{
			{Chain: "btc", Asset: "BTC"},
		}},
		Logger: logs.NewNodeLogger("test", 0),
	}
	pairs, err := m.loadTokenRegistryChainAssets()
	if err != nil {
		t.Fatalf("loadTokenRegistryChainAssets failed: %v", err)
	}
	got := make(map[string]bool)
	for _, pair := range pairs {
		got[pair.Chain+"/"+pair.Asset] = true
	}
	if !got["btc/BTC"] || !got["bnb/"+identity.ID] {
		t.Fatalf("expected fallback and token-registry pairs, got %+v", pairs)
	}
}

                                                                  

                                                   
type fakeStateReader struct {
	data map[string][]byte
}

func newFakeStateReader() *fakeStateReader {
	return &fakeStateReader{data: make(map[string][]byte)}
}

func (r *fakeStateReader) Set(key string, value []byte) {
	r.data[key] = value
}

func (r *fakeStateReader) Get(key string) ([]byte, bool, error) {
	v, ok := r.data[key]
	if !ok || len(v) == 0 {
		return nil, false, nil
	}
	return v, true, nil
}

func (r *fakeStateReader) Scan(prefix string, fn func(k string, v []byte) bool) error {
	for k, v := range r.data {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			if !fn(k, v) {
				break
			}
		}
	}
	return nil
}

                             
func TestScanner(t *testing.T) {
	reader := newFakeStateReader()
	scanner := planning.NewScanner(reader)

	                  
	t.Run("EmptyQueue", func(t *testing.T) {
		result, err := scanner.ScanOnce("BTC", "native")
		if err != nil {
			t.Fatalf("ScanOnce failed: %v", err)
		}
		if result != nil {
			t.Errorf("Expected nil result for empty queue, got %+v", result)
		}
	})

	                         
	t.Run("FindQueuedWithdraw", func(t *testing.T) {
		                 
		reader.Set(keys.KeyFrostWithdrawFIFOSeq("BTC", "native"), []byte("1"))
		                                     
		reader.Set(keys.KeyFrostWithdrawFIFOIndex("BTC", "native", 1), []byte("test_withdraw_1"))
		                                  
		withdrawState := &pb.FrostWithdrawState{
			WithdrawId: "test_withdraw_1",
			Chain:      "BTC",
			Asset:      "native",
			To:         "bc1q...",
			Amount:     "1000000",
			Status:     "QUEUED",
			Seq:        1,
		}
		data, _ := proto.Marshal(withdrawState)
		reader.Set(keys.KeyFrostWithdraw("test_withdraw_1"), data)

		result, err := scanner.ScanOnce("BTC", "native")
		if err != nil {
			t.Fatalf("ScanOnce failed: %v", err)
		}
		if result == nil {
			t.Fatal("Expected non-nil result")
		}
		if result.WithdrawID != "test_withdraw_1" {
			t.Errorf("Expected withdraw_id 'test_withdraw_1', got '%s'", result.WithdrawID)
		}
		if result.Seq != 1 {
			t.Errorf("Expected seq 1, got %d", result.Seq)
		}
	})

	                                             
	t.Run("SkipSignedWithdraw", func(t *testing.T) {
		reader2 := newFakeStateReader()
		scanner2 := planning.NewScanner(reader2)

		reader2.Set(keys.KeyFrostWithdrawFIFOSeq("ETH", "native"), []byte("1"))
		reader2.Set(keys.KeyFrostWithdrawFIFOIndex("ETH", "native", 1), []byte("test_withdraw_2"))
		withdrawState := &pb.FrostWithdrawState{
			WithdrawId: "test_withdraw_2",
			Chain:      "ETH",
			Asset:      "native",
			Status:     "SIGNED",             
			Seq:        1,
		}
		data, _ := proto.Marshal(withdrawState)
		reader2.Set(keys.KeyFrostWithdraw("test_withdraw_2"), data)

		result, err := scanner2.ScanOnce("ETH", "native")
		if err != nil {
			t.Fatalf("ScanOnce failed: %v", err)
		}
		if result != nil {
			t.Errorf("Expected nil for SIGNED withdraw, got %+v", result)
		}
	})
}

                                                                          

                                                     
type fakeChainAdapter struct {
	chainName string
}

func (a *fakeChainAdapter) Chain() string {
	return a.chainName
}

func (a *fakeChainAdapter) SignAlgo() pb.SignAlgo {
	return pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340
}

func (a *fakeChainAdapter) BuildWithdrawTemplate(params chain.WithdrawTemplateParams) (*chain.TemplateResult, error) {
	                                   
	data := params.Chain + "|" + params.Asset + "|" + params.Outputs[0].To
	hash := sha256.Sum256([]byte(data))
	return &chain.TemplateResult{
		TemplateHash: hash[:],
		TemplateData: []byte(data),
	}, nil
}

func (a *fakeChainAdapter) TemplateHash(templateData []byte) ([]byte, error) {
	hash := sha256.Sum256(templateData)
	return hash[:], nil
}

func (a *fakeChainAdapter) PackageSigned(templateData []byte, signatures [][]byte, guardianSignatures [][]byte) (*chain.SignedPackage, error) {
	return &chain.SignedPackage{
		TemplateData: templateData,
		Signatures:   signatures,
	}, nil
}

func (a *fakeChainAdapter) VerifySignature(groupPubkey []byte, msg []byte, signature []byte) (bool, error) {
	return true, nil
}

                                                         
type fakeAdapterFactory struct {
	adapters map[string]*fakeChainAdapter
}

func newFakeAdapterFactory() *fakeAdapterFactory {
	return &fakeAdapterFactory{
		adapters: make(map[string]*fakeChainAdapter),
	}
}

func (f *fakeAdapterFactory) Register(chainName string) {
	f.adapters[chainName] = &fakeChainAdapter{chainName: chainName}
}

func (f *fakeAdapterFactory) Adapter(chainName string) (chain.ChainAdapter, error) {
	adapter, ok := f.adapters[chainName]
	if !ok {
		return nil, chain.ErrUnsupportedChain
	}
	return adapter, nil
}

func (f *fakeAdapterFactory) RegisterAdapter(adapter chain.ChainAdapter) {
	                    
}

                                                                         

                                                   
type fakeTxSubmitter struct {
	submitted []any
}

func newFakeTxSubmitter() *fakeTxSubmitter {
	return &fakeTxSubmitter{submitted: make([]any, 0)}
}

func (s *fakeTxSubmitter) Submit(tx any) (txID string, err error) {
	s.submitted = append(s.submitted, tx)
	return "fake_tx_id", nil
}

func (s *fakeTxSubmitter) SubmitDkgCommitTx(ctx context.Context, tx *pb.FrostVaultDkgCommitTx) error {
	s.submitted = append(s.submitted, tx)
	return nil
}

func (s *fakeTxSubmitter) SubmitDkgShareTx(ctx context.Context, tx *pb.FrostVaultDkgShareTx) error {
	s.submitted = append(s.submitted, tx)
	return nil
}

func (s *fakeTxSubmitter) SubmitDkgComplaintTx(ctx context.Context, tx *pb.FrostVaultDkgComplaintTx) error {
	s.submitted = append(s.submitted, tx)
	return nil
}

func (s *fakeTxSubmitter) SubmitDkgRevealTx(ctx context.Context, tx *pb.FrostVaultDkgRevealTx) error {
	s.submitted = append(s.submitted, tx)
	return nil
}

func (s *fakeTxSubmitter) SubmitDkgValidationSignedTx(ctx context.Context, tx *pb.FrostVaultDkgValidationSignedTx) error {
	s.submitted = append(s.submitted, tx)
	return nil
}

func (s *fakeTxSubmitter) SubmitTransitionSignedTx(ctx context.Context, tx *pb.FrostVaultTransitionSignedTx) error {
	s.submitted = append(s.submitted, tx)
	return nil
}

func (s *fakeTxSubmitter) SubmitWithdrawClaimTx(ctx context.Context, tx *pb.FrostWithdrawClaimTx) error {
	s.submitted = append(s.submitted, tx)
	return nil
}

func (s *fakeTxSubmitter) SubmitWithdrawSignedTx(ctx context.Context, tx *pb.FrostWithdrawSignedTx) error {
	s.submitted = append(s.submitted, tx)
	return nil
}

func (s *fakeTxSubmitter) SubmitWithdrawPlanningLogTx(ctx context.Context, tx *pb.FrostWithdrawPlanningLogTx) error {
	s.submitted = append(s.submitted, tx)
	return nil
}

func (s *fakeTxSubmitter) GetSubmitted() []any {
	return s.submitted
}

type fakeLogReporter struct{}

func (f *fakeLogReporter) ReportWithdrawPlanningLog(ctx context.Context, log *pb.FrostWithdrawPlanningLogTx) error {
	return nil
}

                                  
type fakeSigningService struct{}

func (s *fakeSigningService) StartSigningSession(ctx context.Context, params *workers.SigningSessionParams) (string, error) {
	return "session_1", nil
}

func (s *fakeSigningService) GetSessionStatus(sessionID string) (*workers.SessionStatus, error) {
	return &workers.SessionStatus{State: "COMPLETED"}, nil
}

func (s *fakeSigningService) CancelSession(sessionID string) error {
	return nil
}

func (s *fakeSigningService) WaitForCompletion(ctx context.Context, sessionID string, timeout time.Duration) (*workers.SignedPackage, error) {
	                     
	return &workers.SignedPackage{
		SessionID:    sessionID,
		JobID:        "job_1",
		Signature:    []byte("signature"),
		RawTx:        []byte("rawtx"),
		TemplateHash: []byte("hash"),
	}, nil
}

                                           
func TestWithdrawWorker(t *testing.T) {
	reader := newFakeStateReader()
	factory := chain.NewDefaultAdapterFactory()
	factory.RegisterAdapter(evm.NewEVMAdapter("eth", 1))
	submitter := newFakeTxSubmitter()

	                                            
	var signingService workers.SigningService = &fakeSigningService{}
	var vaultProvider VaultCommitteeProvider = newFakeVaultProvider()

	localIdentity := utils.NewKeyManager()
	if err := localIdentity.InitKey(utils.GenerateTestIdentity(1).PrivateKeyHex); err != nil {
		t.Fatalf("initialize fixture identity: %v", err)
	}
	                                                
	worker := workers.NewWithdrawWorker(reader, factory, submitter, &fakeLogReporter{}, signingService, vaultProvider, 1, "member1", localIdentity, logs.NewNodeLogger("test", 0))

	                        
	vaultCfg := &pb.FrostVaultConfig{AuthorityProtocol: 3, DepositDexChainId: 51025, GuardianPubkey: runtimeAccountGuardianKey(),
		Chain:         "eth",
		VaultCount:    1,
		CommitteeSize: 100,
		VaultRefs:     []string{"0x0000000000000000000000000000000000000001"},
	}
	vaultCfgBytes, _ := proto.Marshal(vaultCfg)
	reader.Set(keys.KeyFrostVaultConfig("eth", 0), vaultCfgBytes)

	                                
	vaultState := &pb.FrostVaultState{
		Chain:    "eth",
		VaultId:  0,
		Status:   "ACTIVE",
		KeyEpoch: 1,
		SignAlgo: 1,                                               
	}
	vaultStateBytes, _ := proto.Marshal(vaultState)
	reader.Set(keys.KeyFrostVaultState("eth", 0), vaultStateBytes)

	tokenData, _ := proto.Marshal(&pb.Token{
		Address: "native", Type: pb.TokenType_TOKEN_BRIDGED, NativeChain: "eth", NativeContract: "",
	})
	reader.Set(keys.KeyToken("native"), tokenData)

	                                                    
	recharge := &pb.RechargeRequest{
		RequestId: "recharge_tx_1",
		Amount:    "2000000",
	}
	rechargeBytes, _ := proto.Marshal(recharge)
	reader.Set(keys.KeyRechargeRequest("recharge_tx_1"), rechargeBytes)

	                                  
	lotKey := fmt.Sprintf("v1_frost_funds_lot_%s_%s_%d_%d", "eth", "native", 0, 1)
	reader.Set(lotKey, []byte("recharge_tx_1"))
	reader.Set(keys.KeyFrostVaultAvailableBalance("eth", "native", 0), []byte("1000000"))
	reader.Set(keys.KeyFrostVaultReservedBalance("eth", "native", 0), []byte("1000000"))

	                         
	reader.Set(keys.KeyFrostWithdrawLiveHead("eth", 0), []byte("1"))
	reader.Set(keys.KeyFrostWithdrawLiveTail("eth", 0), []byte("1"))
	reader.Set(keys.KeyFrostWithdrawLivePending("eth", 0), []byte("1"))
	reader.Set(keys.KeyFrostWithdrawLiveIndex("eth", 0, 1), []byte("test_withdraw_1"))
	reader.Set(keys.KeyLatestHeight(), []byte("105"))

	withdrawState := &pb.FrostWithdrawState{
		WithdrawId:    "test_withdraw_1",
		Chain:         "eth",
		Asset:         "native",
		To:            "0x2222222222222222222222222222222222222222",
		Amount:        "1000000",
		GrossAmount:   "1000000",
		FeeAmount:     "100",
		NetAmount:     "999900",
		FeeVersion:    1,
		Status:        "QUEUED",
		Seq:           1,
		LiveSeq:       1,
		VaultId:       0,
		KeyEpoch:      1,
		RequestHeight: 5,
	}
	data, _ := proto.Marshal(withdrawState)
	reader.Set(keys.KeyFrostWithdraw("test_withdraw_1"), data)

	               
	ctx := context.Background()
	_, err := worker.ProcessOnce(ctx, "eth", "native")
	if err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}

	                                                        
	var submitted []any
	for i := 0; i < 20; i++ {                      
		submitted = submitter.GetSubmitted()
		if len(submitted) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if len(submitted) != 1 {
		t.Fatalf("Expected 1 submitted tx, got %d", len(submitted))
	}

	tx, ok := submitted[0].(*pb.FrostWithdrawClaimTx)
	if !ok {
		t.Fatalf("Expected *pb.FrostWithdrawClaimTx, got %T", submitted[0])
	}

	if len(tx.WithdrawIds) != 1 || tx.WithdrawIds[0] != "test_withdraw_1" {
		t.Errorf("Unexpected withdraw_ids: %v", tx.WithdrawIds)
	}
}

                                                
func TestWithdrawWorker_EmptyQueue(t *testing.T) {
	reader := newFakeStateReader()
	factory := newFakeAdapterFactory()
	factory.Register("BTC")
	submitter := newFakeTxSubmitter()

	                                            
	var signingService workers.SigningService = nil                          
	var vaultProvider VaultCommitteeProvider = nil                           
	worker := workers.NewWithdrawWorker(reader, factory, submitter, &fakeLogReporter{}, signingService, vaultProvider, 1, "test-node-address", nil, logs.NewNodeLogger("test", 0))

	                  
	ctx := context.Background()
	job, err := worker.ProcessOnce(ctx, "BTC", "native")
	if err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}
	if job != nil {
		t.Errorf("Expected nil job for empty queue, got %+v", job)
	}

	                              

}

                                                                
type fakeVaultProvider struct{}

func newFakeVaultProvider() *fakeVaultProvider {
	return &fakeVaultProvider{}
}

func (p *fakeVaultProvider) VaultCommittee(chain string, vaultID uint32, epoch uint64) ([]SignerInfo, error) {
	return []SignerInfo{{ID: "member1", Index: 1, Weight: 1}}, nil
}

func (p *fakeVaultProvider) VaultCurrentEpoch(chain string, vaultID uint32) uint64 {
	return 1
}

func (p *fakeVaultProvider) VaultGroupPubkey(chain string, vaultID uint32, epoch uint64) ([]byte, error) {
	return []byte("group_pubkey"), nil
}

func (p *fakeVaultProvider) CalculateThreshold(chain string, vaultID uint32) (int, error) {
	return 2, nil
}

func (p *fakeVaultProvider) CurrentEpoch(height uint64) uint64 {
	return 1
}

func (p *fakeVaultProvider) TopK(height uint64) ([]SignerInfo, error) {
	return []SignerInfo{{ID: "member1", Index: 1, Weight: 1}}, nil
}
