                                         
                                                            
                                            
                                                                                  
                                                                                       
                                                                        
                                                                             
                                                     

package planning

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"testing"

	chainpkg "dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/frost/chain/evm"
	"dex/keys"
	"dex/pb"

	"github.com/btcsuite/btcd/chaincfg"
	"google.golang.org/protobuf/proto"
)

const (
	planningTestBTCFeeRate = uint64(25)
	planningTestBTCAddress = "tb1pdpd76eytdcdwnm62uuxve403srq0dtcls0uj488wjjy4z4lfm25sdwatf0"
)

                                                      

func TestGenerateJobID_DeterministicAndUnique(t *testing.T) {
	h := []byte{0xaa, 0xbb}
	a := generateJobID("eth", "USDT", 0, 1, h, 2)
	if a != generateJobID("eth", "USDT", 0, 1, h, 2) {
		t.Fatal("same input must produce same jobID")
	}
	                                       
	diffs := []string{
		generateJobID("bnb", "USDT", 0, 1, h, 2),
		generateJobID("eth", "USDC", 0, 1, h, 2),
		generateJobID("eth", "USDT", 1, 1, h, 2),
		generateJobID("eth", "USDT", 0, 2, h, 2),
		generateJobID("eth", "USDT", 0, 1, []byte{0xcc}, 2),
		generateJobID("eth", "USDT", 0, 1, h, 3),
	}
	for i, d := range diffs {
		if d == a {
			t.Fatalf("diff[%d] collides with base", i)
		}
	}
}

func TestParseBTCAmount(t *testing.T) {
	if got, err := parseBTCAmount("12345"); err != nil || got != 12345 {
		t.Fatalf("parseBTCAmount(12345)=%d err=%v", got, err)
	}
	for _, invalid := range []string{"", "0", "not_a_number", "-1", "2100000000000001"} {
		if _, err := parseBTCAmount(invalid); err == nil {
			t.Fatalf("invalid BTC amount %q was accepted", invalid)
		}
	}
}

                                                                              

func TestSumUTXOAmount(t *testing.T) {
	if sumUTXOAmount(nil) != 0 {
		t.Fatal("nil UTXOs should sum to 0")
	}
	utxos := []chainpkg.UTXO{{Amount: 100}, {Amount: 250}, {Amount: 0}}
	if got := sumUTXOAmount(utxos); got != 350 {
		t.Fatalf("sum=%d want=350", got)
	}
}

func TestEstimateBTCFee_Monotonic(t *testing.T) {
	r := &testStateReader{kvs: map[string][]byte{}}
	cfg, _ := proto.Marshal(&pb.FrostVaultConfig{BtcFeeSatsPerVbyte: planningTestBTCFeeRate})
	r.kvs[keys.KeyFrostVaultConfig("btc", 0)] = cfg
	p := &JobWindowPlanner{stateReader: r}
	script, err := btc.AddressScript(planningTestBTCAddress)
	if err != nil {
		t.Fatal(err)
	}
	f1, err := p.estimateBTCFee("btc", 1, [][]byte{script})
	if err != nil {
		t.Fatal(err)
	}
	f2, err := p.estimateBTCFee("btc", 2, [][]byte{script})
	if err != nil {
		t.Fatal(err)
	}
	f3, err := p.estimateBTCFee("btc", 1, [][]byte{script, script})
	if err != nil {
		t.Fatal(err)
	}
	if f1 == 0 {
		t.Fatal("fee should be > 0")
	}
	if f2 <= f1 {
		t.Fatalf("more inputs should increase fee: f1=%d f2=%d", f1, f2)
	}
	if f3 <= f1 {
		t.Fatalf("more outputs should increase fee: f1=%d f3=%d", f1, f3)
	}
	cfg, _ = proto.Marshal(&pb.FrostVaultConfig{BtcFeeSatsPerVbyte: planningTestBTCFeeRate * 2})
	r.kvs[keys.KeyFrostVaultConfig("btc", 0)] = cfg
	doubled, err := p.estimateBTCFee("btc", 1, [][]byte{script})
	if err != nil {
		t.Fatal(err)
	}
	if doubled != f1*2 {
		t.Fatalf("fee did not follow updated consensus config: first=%d doubled=%d", f1, doubled)
	}
}

func TestBtcNetParamsForChain(t *testing.T) {
	cases := map[string]*chaincfg.Params{
		"btc":         &chaincfg.MainNetParams,
		"btc_testnet": &chaincfg.TestNet3Params,
		"testnet3":    &chaincfg.TestNet3Params,
		"btc-regtest": &chaincfg.RegressionNetParams,
		"signet":      &chaincfg.SigNetParams,
		"  REGTEST  ": &chaincfg.RegressionNetParams,                
	}
	for in, want := range cases {
		got := btcNetParamsForChain(in)
		if got != want {
			t.Fatalf("chain=%q got=%s want=%s", in, got.Name, want.Name)
		}
	}
}

func TestBtcNetParamsForChainReadsVaultConfig(t *testing.T) {
	r := &testStateReader{kvs: map[string][]byte{}}
	cfg, _ := proto.Marshal(&pb.FrostVaultConfig{BtcNetwork: "testnet3"})
	r.kvs[keys.KeyFrostVaultConfig("btc", 0)] = cfg
	p := &JobWindowPlanner{stateReader: r}

	got := p.btcNetParamsForChain("btc")
	if got != &chaincfg.TestNet3Params {
		t.Fatalf("configured btc network not used: got=%s", got.Name)
	}
}

func TestClassifyBTCUTXOInsufficient(t *testing.T) {
	cases := []struct {
		name string
		got  uint64
		stat btcUTXOScanStats
		code string
	}{
		{"empty window", 0, btcUTXOScanStats{Scanned: 0}, "fifo_window_empty"},
		{"all missing", 0, btcUTXOScanStats{Scanned: 3, IndexMissing: 3}, "fifo_index_missing_all"},
		{"all locked", 0, btcUTXOScanStats{Scanned: 2, Locked: 2}, "all_locked"},
		{"all decode failed", 0, btcUTXOScanStats{Scanned: 1, DecodeFailed: 1}, "decode_failed_all"},
		{"all zero amount", 0, btcUTXOScanStats{Scanned: 1, ZeroAmount: 1}, "zero_amount_all"},
		{"mixed reasons, nothing spendable", 0, btcUTXOScanStats{Scanned: 4, Locked: 1, DecodeFailed: 1, ZeroAmount: 1, IndexMissing: 1}, "no_spendable_utxo"},
		{"selected but insufficient", 100, btcUTXOScanStats{Scanned: 1, Selected: 1}, "selected_but_insufficient_amount"},
		{"no selected with got=0 default", 0, btcUTXOScanStats{Scanned: 5}, "no_spendable_utxo"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, reason := classifyBTCUTXOInsufficient(c.got, c.stat)
			if code != c.code {
				t.Fatalf("code=%s want=%s", code, c.code)
			}
			if reason == "" {
				t.Fatal("reason must be non-empty")
			}
		})
	}
}

func TestBTCUTXOSelectionError_ErrorString(t *testing.T) {
	err := newBTCUTXOSelectionError("test_code", "why", "btc", 7, 100, 50, 1, 10,
		btcUTXOScanStats{Scanned: 3, Selected: 1, Locked: 1})
	msg := err.Error()
	for _, s := range []string{"test_code", "why", "btc", "need=100", "got=50", "vault=7"} {
		if !strings.Contains(msg, s) {
			t.Fatalf("err msg missing %q: %s", s, msg)
		}
	}
}

                                                             

func seedUtxo(t *testing.T, r *testStateReader, vaultID uint32, seq uint64, txid string, vout uint32, amount string, locked bool) {
	t.Helper()
	data, err := proto.Marshal(&pb.FrostUtxo{Txid: txid, Vout: vout, Amount: amount, VaultId: vaultID, FinalizeHeight: 1, ScriptPubkey: planningBTCScript()})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	r.kvs[keys.KeyFrostBtcUtxoFIFOIndex(vaultID, seq)] = data
	r.kvs[keys.KeyFrostBtcUtxo(vaultID, txid, vout)] = data
	if locked {
		r.kvs[keys.KeyFrostBtcLockedUtxo(vaultID, txid, vout)] = []byte("job_x")
	}
}

func seedSpendableUtxo(t *testing.T, r *testStateReader, vaultID uint32, seq uint64, txid string, vout uint32, amount string, locked bool) {
	t.Helper()
	data, err := proto.Marshal(&pb.FrostUtxo{Txid: txid, Vout: vout, Amount: amount, VaultId: vaultID, FinalizeHeight: 1, ScriptPubkey: planningBTCScript()})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	r.kvs[keys.KeyFrostBtcSpendableUtxoIndex(vaultID, seq)] = data
	r.kvs[keys.KeyFrostBtcUtxo(vaultID, txid, vout)] = data
	r.kvs[keys.KeyFrostBtcUtxoSeqRef(vaultID, txid, vout)] = []byte(strconv.FormatUint(seq, 10))
	if locked {
		r.kvs[keys.KeyFrostBtcLockedUtxo(vaultID, txid, vout)] = []byte("job_x")
	}
}

func seedPlanningWithdraw(t *testing.T, r *testStateReader, chain, asset, withdrawID string, seq uint64, amount string) {
	t.Helper()
	withdraw, err := proto.Marshal(&pb.FrostWithdrawState{
		WithdrawId: withdrawID,
		Seq:        seq,
		Chain:      chain,
		Asset:      asset,
		To:         planningTestBTCAddress,
		Amount:     amount,
		Status:     "QUEUED",
	})
	if err != nil {
		t.Fatalf("marshal withdraw: %v", err)
	}
	r.kvs[keys.KeyFrostWithdraw(withdrawID)] = withdraw
	r.kvs[keys.KeyFrostWithdrawFIFOIndex(chain, asset, seq)] = []byte(withdrawID)
}

func TestSelectBTCUTXOs_PicksFirstUnlocked(t *testing.T) {
	r := &testStateReader{kvs: map[string][]byte{}}
	p := &JobWindowPlanner{stateReader: r}
	vault := uint32(0)
	                               
	r.kvs[keys.KeyFrostBtcUtxoFIFOHead(vault)] = []byte("1")
	r.kvs[keys.KeyFrostBtcUtxoFIFOSeq(vault)] = []byte("3")
	seedUtxo(t, r, vault, 1, "aa", 0, "1000", true)
	seedUtxo(t, r, vault, 2, "bb", 0, "500", false)
	seedUtxo(t, r, vault, 3, "cc", 0, "700", false)

	got, err := p.selectBTCUTXOs("btc", vault)
	if err != nil {
		t.Fatalf("selectBTCUTXOs: %v", err)
	}
	if len(got) != 1 || got[0].TxID != "bb" || got[0].Amount != 500 {
		t.Fatalf("want single UTXO bb/500, got %+v", got)
	}
}

func TestSelectBTCUTXOsCovering_TakesFIFOUntilCovered(t *testing.T) {
	r := &testStateReader{kvs: map[string][]byte{}}
	p := &JobWindowPlanner{stateReader: r}
	vault := uint32(4)
	r.kvs[keys.KeyFrostBtcUtxoFIFOHead(vault)] = []byte("1")
	r.kvs[keys.KeyFrostBtcUtxoFIFOSeq(vault)] = []byte("2")
	seedUtxo(t, r, vault, 1, "tiny", 0, "4", false)
	seedUtxo(t, r, vault, 2, "large", 0, "422222", false)

	got, err := p.selectBTCUTXOsCovering("btc", vault, 2470)
	if err != nil {
		t.Fatalf("selectBTCUTXOsCovering: %v", err)
	}
	if len(got) != 2 || got[0].TxID != "tiny" || got[1].TxID != "large" || sumUTXOAmount(got) != 422226 {
		t.Fatalf("want FIFO tiny+large, got %+v", got)
	}
}

func TestSelectBTCUTXOsCovering_SpendableIndexKeepsFIFO(t *testing.T) {
	r := &testStateReader{kvs: map[string][]byte{}}
	p := &JobWindowPlanner{stateReader: r}
	vault := uint32(5)
	seedSpendableUtxo(t, r, vault, 1, "first", 0, "1000", false)
	seedSpendableUtxo(t, r, vault, 2, "second", 0, "2000", false)
	seedSpendableUtxo(t, r, vault, 3, "large", 0, "10000", false)

	got, err := p.selectBTCUTXOsCovering("btc", vault, 2500)
	if err != nil {
		t.Fatalf("selectBTCUTXOsCovering: %v", err)
	}
	if len(got) != 2 || got[0].TxID != "first" || got[1].TxID != "second" || sumUTXOAmount(got) != 3000 {
		t.Fatalf("want FIFO first+second, got %+v", got)
	}
}

func TestSelectBTCUTXOs_SeqMissing(t *testing.T) {
	r := &testStateReader{kvs: map[string][]byte{}}
	p := &JobWindowPlanner{stateReader: r}
	_, err := p.selectBTCUTXOs("btc", 0)
	if err == nil {
		t.Fatal("expected error when seq missing")
	}
	se, ok := err.(*btcUTXOSelectionError)
	if !ok || se.Code != "fifo_seq_missing" {
		t.Fatalf("want code=fifo_seq_missing, got %T %+v", err, err)
	}
}

func TestSelectBTCUTXOs_HeadGreaterThanSeq(t *testing.T) {
	r := &testStateReader{kvs: map[string][]byte{}}
	p := &JobWindowPlanner{stateReader: r}
	vault := uint32(0)
	r.kvs[keys.KeyFrostBtcUtxoFIFOHead(vault)] = []byte("5")
	r.kvs[keys.KeyFrostBtcUtxoFIFOSeq(vault)] = []byte("3")
	_, err := p.selectBTCUTXOs("btc", vault)
	se, ok := err.(*btcUTXOSelectionError)
	if !ok || se.Code != "fifo_seq_invalid" {
		t.Fatalf("want code=fifo_seq_invalid, got %+v", err)
	}
}

func TestSelectBTCUTXOs_AllLocked(t *testing.T) {
	r := &testStateReader{kvs: map[string][]byte{}}
	p := &JobWindowPlanner{stateReader: r}
	vault := uint32(0)
	r.kvs[keys.KeyFrostBtcUtxoFIFOHead(vault)] = []byte("1")
	r.kvs[keys.KeyFrostBtcUtxoFIFOSeq(vault)] = []byte("2")
	seedUtxo(t, r, vault, 1, "aa", 0, "1000", true)
	seedUtxo(t, r, vault, 2, "bb", 0, "2000", true)
	_, err := p.selectBTCUTXOs("btc", vault)
	se, ok := err.(*btcUTXOSelectionError)
	if !ok {
		t.Fatalf("expected btcUTXOSelectionError, got %T", err)
	}
	if se.Code != "all_locked" {
		t.Fatalf("want code=all_locked, got %s", se.Code)
	}
}

type fakeBTCPlanningAdapter struct {
	params chainpkg.WithdrawTemplateParams
}

func (a *fakeBTCPlanningAdapter) Chain() string { return "btc" }

func (a *fakeBTCPlanningAdapter) SignAlgo() pb.SignAlgo {
	return pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340
}

func (a *fakeBTCPlanningAdapter) BuildWithdrawTemplate(params chainpkg.WithdrawTemplateParams) (*chainpkg.TemplateResult, error) {
	a.params = params
	return &chainpkg.TemplateResult{TemplateHash: []byte{1, 2, 3}, TemplateData: []byte("btc-template")}, nil
}

func (a *fakeBTCPlanningAdapter) TemplateHash(templateData []byte) ([]byte, error) {
	return []byte{1, 2, 3}, nil
}

func (a *fakeBTCPlanningAdapter) PackageSigned(templateData []byte, signatures, guardianSigs [][]byte) (*chainpkg.SignedPackage, error) {
	return &chainpkg.SignedPackage{TemplateData: templateData, Signatures: signatures}, nil
}

type fakeAccountPlanningAdapter struct {
	params []chainpkg.WithdrawTemplateParams
}

func seedMixedPlanningToken(t *testing.T, r *testStateReader, chainName, asset, nativeContract string) {
	t.Helper()
	r.kvs[keys.KeyToken(asset)] = accountTemplateToken(t, asset, chainName, nativeContract)
}

func seedMixedPlanningWithdraw(t *testing.T, r *testStateReader, chainName, asset, id string, vaultID uint32, liveSeq, requestHeight uint64, amount string) {
	t.Helper()
	state := &pb.FrostWithdrawState{
		WithdrawId: id, Seq: liveSeq, Chain: chainName, Asset: asset,
		To: fmt.Sprintf("0x%040x", liveSeq+1), Amount: amount, Status: "QUEUED",
		RequestHeight: requestHeight,
	}
	r.kvs[keys.KeyFrostWithdraw(id)] = canonicalAccountTemplateFixture(t, state, vaultID)
	r.kvs[keys.KeyFrostWithdrawLiveIndex(chainName, vaultID, liveSeq)] = []byte(id)
}

func newMixedPlanningFixture(t *testing.T, vaultCount uint32) (*testStateReader, *JobWindowPlanner) {
	t.Helper()
	r := &testStateReader{kvs: map[string][]byte{}}
	refs := make([]string, vaultCount)
	for id := uint32(0); id < vaultCount; id++ {
		refs[id] = fmt.Sprintf("0x%040x", id+0xaa)
		seedVaultState(t, r, "base", id, "ACTIVE", uint64(id)+1, 10, nil)
	}
	cfg, err := proto.Marshal(&pb.FrostVaultConfig{AuthorityProtocol: 3, DepositDexChainId: 51025, GuardianPubkey: accountPlanningGuardianKey(), Chain: "base", VaultCount: vaultCount, VaultRefs: refs})
	if err != nil {
		t.Fatal(err)
	}
	r.kvs[keys.KeyFrostVaultConfig("base", 0)] = cfg
	r.kvs[keys.KeyLatestHeight()] = []byte("100")
	seedMixedPlanningToken(t, r, "base", "BASE", "")
	factory := chainpkg.NewDefaultAdapterFactory()
	factory.RegisterAdapter(evm.NewEVMAdapter("base", evm.DefaultChainID("base")))
	return r, NewJobWindowPlanner(r, factory, 1)
}

func (a *fakeAccountPlanningAdapter) Chain() string { return "base" }

func (a *fakeAccountPlanningAdapter) SignAlgo() pb.SignAlgo {
	return pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128
}

func (a *fakeAccountPlanningAdapter) BuildWithdrawTemplate(params chainpkg.WithdrawTemplateParams) (*chainpkg.TemplateResult, error) {
	a.params = append(a.params, params)
	return &chainpkg.TemplateResult{TemplateHash: []byte{byte(len(a.params))}, TemplateData: []byte("account-template")}, nil
}

func (a *fakeAccountPlanningAdapter) TemplateHash(templateData []byte) ([]byte, error) {
	return []byte{1}, nil
}

func (a *fakeAccountPlanningAdapter) PackageSigned(templateData []byte, signatures, guardianSigs [][]byte) (*chainpkg.SignedPackage, error) {
	return &chainpkg.SignedPackage{TemplateData: templateData, Signatures: signatures}, nil
}

func TestEstimateBTCNeedSumsPlanningWindow(t *testing.T) {
	r := &testStateReader{kvs: map[string][]byte{}}
	p := &JobWindowPlanner{stateReader: r}
	seedPlanningWithdraw(t, r, "btc", "BTC", "wd_1", 1, "50")
	seedPlanningWithdraw(t, r, "btc", "BTC", "wd_2", 2, "70")

	got, err := p.estimateBTCNeed([]*ScanResult{
		{Chain: "btc", Asset: "BTC", WithdrawID: "wd_1", Seq: 1},
		{Chain: "btc", Asset: "BTC", WithdrawID: "wd_2", Seq: 2},
	})
	if err != nil {
		t.Fatalf("estimateBTCNeed: %v", err)
	}
	want := uint64(120)
	if got != want {
		t.Fatalf("estimateBTCNeed=%d want=%d", got, want)
	}
}

func TestPlanBTCJobWindow_UsesFundedVault(t *testing.T) {
	r := &testStateReader{kvs: map[string][]byte{}}
	factory := chainpkg.NewDefaultAdapterFactory()
	adapter := &fakeBTCPlanningAdapter{}
	factory.RegisterAdapter(adapter)
	p := &JobWindowPlanner{stateReader: r, adapterFactory: factory, maxInFlight: 1}

	cfg, _ := proto.Marshal(&pb.FrostVaultConfig{VaultCount: 2, BtcFeeSatsPerVbyte: planningTestBTCFeeRate})
	r.kvs[keys.KeyFrostVaultConfig("btc", 0)] = cfg
	seedVaultState(t, r, "btc", 0, "ACTIVE", 1, 10, nil)
	seedVaultState(t, r, "btc", 1, "ACTIVE", 2, 20, nil)
	r.kvs[keys.KeyFrostVaultAvailableBalance("btc", "BTC", 1)] = []byte("10000")

	withdrawID := "wd_btc"
	withdraw, _ := proto.Marshal(&pb.FrostWithdrawState{
		WithdrawId: withdrawID,
		Seq:        1,
		Chain:      "btc",
		Asset:      "BTC",
		To:         planningTestBTCAddress,
		Amount:     "10000",
		Status:     "QUEUED",
	})
	r.kvs[keys.KeyFrostWithdraw(withdrawID)] = withdraw
	r.kvs[keys.KeyFrostBtcUtxoFIFOSeq(1)] = []byte("1")
	seedUtxo(t, r, 1, 1, "aa", 0, "10000", false)

	jobs, err := p.planBTCJobWindow("btc", "BTC", []*ScanResult{{Chain: "btc", Asset: "BTC", WithdrawID: withdrawID, Seq: 1}})
	if err != nil {
		t.Fatalf("planBTCJobWindow: %v", err)
	}
	if len(jobs) != 1 || jobs[0].VaultID != 1 {
		t.Fatalf("expected one job on funded vault 1, got %+v", jobs)
	}
	if adapter.params.VaultID != 1 || len(adapter.params.Inputs) != 1 {
		t.Fatalf("adapter got wrong params: %+v", adapter.params)
	}
	outputScript, err := btc.AddressScript(planningTestBTCAddress)
	if err != nil {
		t.Fatal(err)
	}
	wantFee, err := p.estimateBTCFee("btc", 1, [][]byte{outputScript})
	if err != nil {
		t.Fatal(err)
	}
	if adapter.params.Fee != wantFee || adapter.params.ChangeAmount != 0 || len(adapter.params.Outputs) != 1 || adapter.params.Outputs[0].Amount != strconv.FormatUint(10000-wantFee, 10) {
		t.Fatalf("BTC user-paid fee params mismatch: %+v", adapter.params)
	}
}

func TestPlanBTCJobWindow_UsesMultipleFIFOUTXOs(t *testing.T) {
	r := &testStateReader{kvs: map[string][]byte{}}
	factory := chainpkg.NewDefaultAdapterFactory()
	adapter := &fakeBTCPlanningAdapter{}
	factory.RegisterAdapter(adapter)
	p := NewJobWindowPlanner(r, factory, 2)

	cfg, _ := proto.Marshal(&pb.FrostVaultConfig{VaultCount: 1, BtcFeeSatsPerVbyte: planningTestBTCFeeRate})
	r.kvs[keys.KeyFrostVaultConfig("btc", 0)] = cfg
	seedVaultState(t, r, "btc", 0, "ACTIVE", 1, 10, nil)
	r.kvs[keys.KeyFrostVaultAvailableBalance("btc", "BTC", 0)] = []byte("60000")
	r.kvs[keys.KeyLatestHeight()] = []byte("100")
	r.kvs[keys.KeyFrostWithdrawFIFOHead("btc", "BTC")] = []byte("1")
	r.kvs[keys.KeyFrostWithdrawFIFOSeq("btc", "BTC")] = []byte("2")
	seedPlanningWithdraw(t, r, "btc", "BTC", "wd_1", 1, "30000")
	seedPlanningWithdraw(t, r, "btc", "BTC", "wd_2", 2, "30000")
	seedSpendableUtxo(t, r, 0, 1, "first", 0, "20000", false)
	seedSpendableUtxo(t, r, 0, 2, "second", 0, "40000", false)

	jobs, logs, err := p.PlanJobWindow("btc", "BTC")
	if err != nil {
		t.Fatalf("PlanJobWindow: %v", err)
	}
	if len(logs) != 0 {
		t.Fatalf("expected no planning logs, got %+v", logs)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected one job, got %+v", jobs)
	}
	if len(adapter.params.Inputs) != 2 || adapter.params.Inputs[0].TxID != "first" || adapter.params.Inputs[1].TxID != "second" {
		t.Fatalf("expected FIFO first+second inputs, got %+v", adapter.params.Inputs)
	}
	outputScript, err := btc.AddressScript(planningTestBTCAddress)
	if err != nil {
		t.Fatal(err)
	}
	wantFee, err := p.estimateBTCFee("btc", 2, [][]byte{outputScript, outputScript})
	if err != nil {
		t.Fatal(err)
	}
	wantShare := wantFee / 2
	if adapter.params.Fee != wantFee || adapter.params.ChangeAmount != 0 || len(adapter.params.Outputs) != 2 ||
		adapter.params.Outputs[0].Amount != strconv.FormatUint(30000-wantShare, 10) || adapter.params.Outputs[1].Amount != strconv.FormatUint(30000-wantShare, 10) {
		t.Fatalf("BTC batch fee split mismatch: %+v", adapter.params)
	}
}

func TestPlanBTCJobWindow_WaitsWhenFullWindowNotFunded(t *testing.T) {
	r := &testStateReader{kvs: map[string][]byte{}}
	factory := chainpkg.NewDefaultAdapterFactory()
	factory.RegisterAdapter(&fakeBTCPlanningAdapter{})
	p := NewJobWindowPlanner(r, factory, 2)

	cfg, _ := proto.Marshal(&pb.FrostVaultConfig{VaultCount: 1, VaultRefs: []string{"0x00000000000000000000000000000000000000aa"}, BtcFeeSatsPerVbyte: planningTestBTCFeeRate})
	r.kvs[keys.KeyFrostVaultConfig("btc", 0)] = cfg
	seedVaultState(t, r, "btc", 0, "ACTIVE", 1, 10, nil)
	r.kvs[keys.KeyLatestHeight()] = []byte("100")
	r.kvs[keys.KeyFrostWithdrawFIFOHead("btc", "BTC")] = []byte("1")
	r.kvs[keys.KeyFrostWithdrawFIFOSeq("btc", "BTC")] = []byte("2")
	seedPlanningWithdraw(t, r, "btc", "BTC", "wd_1", 1, "2000")
	seedPlanningWithdraw(t, r, "btc", "BTC", "wd_2", 2, "2000")
	r.kvs[keys.KeyFrostBtcUtxoFIFOSeq(0)] = []byte("1")
	seedUtxo(t, r, 0, 1, "aa", 0, "2500", false)

	jobs, logs, err := p.PlanJobWindow("btc", "BTC")
	if err != nil {
		t.Fatalf("PlanJobWindow: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("expected no job when full BTC window is not funded, got %+v", jobs)
	}
	if len(logs["wd_1"]) == 0 || len(logs["wd_2"]) == 0 {
		t.Fatalf("expected waiting logs for both withdraws, got %+v", logs)
	}
}

func TestPlanAccountChainJobWindowUsesRequestTimeVaultReservation(t *testing.T) {
	r, p := newMixedPlanningFixture(t, 1)
	r.kvs[keys.KeyLatestHeight()] = []byte("200")
	seedMixedPlanningWithdraw(t, r, "base", "BASE", "wd_1", 0, 1, 100, "100")
	seedMixedPlanningWithdraw(t, r, "base", "BASE", "wd_2", 0, 2, 100, "100")
	r.kvs[keys.KeyFrostWithdrawLiveHead("base", 0)] = []byte("1")
	r.kvs[keys.KeyFrostWithdrawLiveTail("base", 0)] = []byte("2")
	r.kvs[keys.KeyFrostWithdrawLivePending("base", 0)] = []byte("2")
	                                                                      
	                                                                         
	r.kvs[keys.KeyFrostVaultAvailableBalance("base", "BASE", 0)] = []byte("0")

	jobs, logs, err := p.PlanJobWindow("base", "BASE")
	if err != nil {
		t.Fatalf("PlanJobWindow: %v", err)
	}
	if len(jobs) != 1 || len(jobs[0].WithdrawIDs) != 2 || jobs[0].WithdrawIDs[0] != "wd_1" || jobs[0].WithdrawIDs[1] != "wd_2" {
		t.Fatalf("expected the complete reserved FIFO prefix, got %+v", jobs)
	}
	if len(logs) != 0 {
		t.Fatalf("unexpected blocking logs: %+v", logs)
	}
}

func TestPlanAccountChainJobWindow_PreservesAmountAboveUint64(t *testing.T) {
	const amount = "100000000000000000000"
	r, p := newMixedPlanningFixture(t, 1)
	r.kvs[keys.KeyLatestHeight()] = []byte("200")
	seedMixedPlanningWithdraw(t, r, "base", "BASE", "wd_large", 0, 1, 100, amount)
	r.kvs[keys.KeyFrostWithdrawLiveHead("base", 0)] = []byte("1")
	r.kvs[keys.KeyFrostWithdrawLiveTail("base", 0)] = []byte("1")
	r.kvs[keys.KeyFrostWithdrawLivePending("base", 0)] = []byte("1")

	jobs, logs, err := p.PlanJobWindow("base", "BASE")
	if err != nil {
		t.Fatalf("PlanJobWindow: %v", err)
	}
	if len(jobs) != 1 || len(logs) != 0 {
		t.Fatalf("unexpected large-amount planning result jobs=%+v logs=%+v", jobs, logs)
	}
	template, err := evm.FromJSON(jobs[0].TemplateData)
	if err != nil || template.Outputs[0].GrossAmount != amount {
		t.Fatalf("large amount narrowed: template=%+v err=%v", template, err)
	}
}

func TestPlanAccountChainJobWindowBatches64AndLeaves65th(t *testing.T) {
	r, planner := newMixedPlanningFixture(t, 1)
	for i := 1; i <= 65; i++ {
		id := fmt.Sprintf("wd_%02d", i)
		seedMixedPlanningWithdraw(t, r, "base", "BASE", id, 0, uint64(i), 100, "100")
	}
	r.kvs[keys.KeyFrostWithdrawLiveHead("base", 0)] = []byte("1")
	r.kvs[keys.KeyFrostWithdrawLiveTail("base", 0)] = []byte("65")
	r.kvs[keys.KeyFrostWithdrawLivePending("base", 0)] = []byte("65")
	jobs, _, err := planner.PlanJobWindow("base", "BASE")
	if err != nil || len(jobs) != 1 || len(jobs[0].WithdrawIDs) != 64 || jobs[0].WithdrawIDs[0] != "wd_01" || jobs[0].WithdrawIDs[63] != "wd_64" {
		t.Fatalf("unexpected first batch: err=%v jobs=%+v", err, jobs)
	}
	if r.batchCalls < 3 || r.batchKeys < 64*3 || r.getCalls > 20 {
		t.Fatalf("planner did not use bounded batch reads: get=%d batches=%d batchKeys=%d", r.getCalls, r.batchCalls, r.batchKeys)
	}
	                                                                             
	                                                               
	for i := 1; i <= 64; i++ {
		delete(r.kvs, keys.KeyFrostWithdrawLiveIndex("base", 0, uint64(i)))
	}
	r.kvs[keys.KeyFrostWithdrawLiveHead("base", 0)] = []byte("65")
	r.kvs[keys.KeyFrostWithdrawLivePending("base", 0)] = []byte("1")
	r.kvs[keys.KeyLatestHeight()] = []byte("200")
	jobs, _, err = planner.PlanJobWindow("base", "BASE")
	if err != nil || len(jobs) != 1 || len(jobs[0].WithdrawIDs) != 1 || jobs[0].WithdrawIDs[0] != "wd_65" {
		t.Fatalf("65th withdrawal did not become next batch: err=%v jobs=%+v", err, jobs)
	}
}

func TestPlanAccountChainJobWindowPlansEachPhysicalVaultIndependently(t *testing.T) {
	r, planner := newMixedPlanningFixture(t, 2)
	r.kvs[keys.KeyLatestHeight()] = []byte("200")
	seedMixedPlanningWithdraw(t, r, "base", "BASE", "vault0", 0, 1, 100, "100")
	r.kvs[keys.KeyFrostWithdrawLiveHead("base", 0)] = []byte("1")
	r.kvs[keys.KeyFrostWithdrawLiveTail("base", 0)] = []byte("1")
	r.kvs[keys.KeyFrostWithdrawLivePending("base", 0)] = []byte("1")
	for i := 1; i <= 64; i++ {
		id := fmt.Sprintf("wd_%02d", i)
		seedMixedPlanningWithdraw(t, r, "base", "BASE", id, 1, uint64(i), 100, "100")
	}
	r.kvs[keys.KeyFrostWithdrawLiveHead("base", 1)] = []byte("1")
	r.kvs[keys.KeyFrostWithdrawLiveTail("base", 1)] = []byte("64")
	r.kvs[keys.KeyFrostWithdrawLivePending("base", 1)] = []byte("64")

	jobs, _, err := planner.PlanJobWindow("base", "BASE")
	if err != nil || len(jobs) != 2 {
		t.Fatalf("plan: jobs=%+v err=%v", jobs, err)
	}
	if jobs[0].VaultID != 0 || len(jobs[0].WithdrawIDs) != 1 || jobs[1].VaultID != 1 || jobs[1].KeyEpoch != 2 || len(jobs[1].WithdrawIDs) != 64 {
		t.Fatalf("planner did not preserve independent physical Vault FIFOs: %+v", jobs)
	}
	if r.batchCalls < 5 || r.getCalls > 20 {
		t.Fatalf("vault selection did not use bounded batch reads: get=%d batches=%d keys=%d", r.getCalls, r.batchCalls, r.batchKeys)
	}
}

                                                                          

func seedVaultState(t *testing.T, r *testStateReader, chain string, vaultID uint32, status string, keyEpoch, activeSince uint64, groupPubkey []byte) {
	t.Helper()
	if chain == "btc" {
		if len(groupPubkey) == 0 {
			groupPubkey = planningBTCOperator()
		}
		var cfg pb.FrostVaultConfig
		if raw := r.kvs[keys.KeyFrostVaultConfig(chain, 0)]; len(raw) > 0 && proto.Unmarshal(raw, &cfg) == nil {
			cfg.GuardianPubkey = planningBTCGuardian()
			r.kvs[keys.KeyFrostVaultConfig(chain, 0)], _ = proto.Marshal(&cfg)
		}
	}

	data, err := proto.Marshal(&pb.FrostVaultState{
		Chain:             chain,
		VaultId:           vaultID,
		Status:            status,
		KeyEpoch:          keyEpoch,
		ActiveSinceHeight: activeSince,
		GroupPubkey:       groupPubkey,
	})
	if err != nil {
		t.Fatalf("marshal vault state: %v", err)
	}
	r.kvs[keys.KeyFrostVaultState(chain, vaultID)] = data
}

func TestSelectYoungestActiveVaultID(t *testing.T) {
	r := &testStateReader{kvs: map[string][]byte{}}
	p := &JobWindowPlanner{stateReader: r}

	                                                    
	cfg, _ := proto.Marshal(&pb.FrostVaultConfig{VaultCount: 3})
	r.kvs[keys.KeyFrostVaultConfig("btc", 0)] = cfg
	seedVaultState(t, r, "btc", 0, "ACTIVE", 1, 10, nil)
	seedVaultState(t, r, "btc", 1, "ACTIVE", 3, 20, nil)             
	seedVaultState(t, r, "btc", 2, "EXITING", 5, 30, nil)

	got, err := p.selectYoungestActiveVaultID("btc")
	if err != nil {
		t.Fatalf("selectYoungestActiveVaultID: %v", err)
	}
	if got != 1 {
		t.Fatalf("expected vault 1, got %d", got)
	}

	                                      
	r2 := &testStateReader{kvs: map[string][]byte{}}
	p2 := &JobWindowPlanner{stateReader: r2}
	r2.kvs[keys.KeyFrostVaultConfig("btc", 0)] = cfg
	seedVaultState(t, r2, "btc", 0, "EXITING", 1, 10, nil)
	if _, err := p2.selectYoungestActiveVaultID("btc"); err == nil {
		t.Fatal("expected error when no ACTIVE vault")
	}
}

                                                             

func TestSelectVaultSkipsOldKeyWhileTransitionIsDraining(t *testing.T) {
	r := &testStateReader{kvs: map[string][]byte{}}
	p := &JobWindowPlanner{stateReader: r, transientCache: map[uint32]*big.Int{}}
	cfg, _ := proto.Marshal(&pb.FrostVaultConfig{VaultCount: 1})
	r.kvs[keys.KeyFrostVaultConfig("eth", 0)] = cfg
	seedVaultState(t, r, "eth", 0, "ACTIVE", 1, 10, nil)
	r.kvs[keys.KeyFrostVaultAvailableBalance("eth", "USDT", 0)] = []byte("1000")
	transition, _ := proto.Marshal(&pb.VaultTransitionState{
		Chain: "eth", VaultId: 0, EpochId: 2, Lifecycle: "DRAINING",
	})
	r.kvs[keys.KeyFrostVaultTransition("eth", 0, 2)] = transition

	if _, _, err := p.selectVault("eth", "USDT", big.NewInt(1)); err == nil || !strings.Contains(err.Error(), "no ACTIVE vault") {
		t.Fatalf("DRAINING vault was selected for a new old-key claim: %v", err)
	}
}

func TestCalculateVaultAvailableBalance_CacheHit(t *testing.T) {
	r := &testStateReader{kvs: map[string][]byte{}}
	p := &JobWindowPlanner{stateReader: r, transientCache: map[uint32]*big.Int{5: big.NewInt(999)}}
	got, err := p.calculateVaultAvailableBalance("eth", "USDT", 5)
	if err != nil || got.Cmp(big.NewInt(999)) != 0 {
		t.Fatalf("cached value not returned: got=%s err=%v", got, err)
	}
	got.SetInt64(1)
	if p.transientCache[5].Cmp(big.NewInt(999)) != 0 {
		t.Fatal("caller mutated cached balance through returned pointer")
	}
}

func TestCalculateVaultAvailableBalance_ReadsStateAndCaches(t *testing.T) {
	r := &testStateReader{kvs: map[string][]byte{}}
	p := &JobWindowPlanner{stateReader: r, transientCache: map[uint32]*big.Int{}}
	r.kvs[keys.KeyFrostVaultAvailableBalance("eth", "USDT", 0)] = []byte("1234")
	got, err := p.calculateVaultAvailableBalance("eth", "USDT", 0)
	if err != nil || got.Cmp(big.NewInt(1234)) != 0 {
		t.Fatalf("read failed: got=%s err=%v", got, err)
	}
	if p.transientCache[0].Cmp(big.NewInt(1234)) != 0 {
		t.Fatalf("cache not populated: %+v", p.transientCache)
	}
}

func TestCalculateVaultAvailableBalance_MissingKey(t *testing.T) {
	r := &testStateReader{kvs: map[string][]byte{}}
	p := &JobWindowPlanner{stateReader: r, transientCache: map[uint32]*big.Int{}}
	got, err := p.calculateVaultAvailableBalance("eth", "USDT", 9)
	if err != nil || got.Sign() != 0 {
		t.Fatalf("missing key should return 0: got=%s err=%v", got, err)
	}
	if p.transientCache[9].Sign() != 0 {
		t.Fatal("cache should record 0 for missing key")
	}
}

func TestCalculateVaultAvailableBalance_BTCReadsAggregateBalance(t *testing.T) {
	r := &testStateReader{kvs: map[string][]byte{}}
	p := &JobWindowPlanner{stateReader: r, transientCache: map[uint32]*big.Int{}}
	vault := uint32(3)
	r.kvs[keys.KeyFrostVaultAvailableBalance("btc", "BTC", vault)] = []byte("12345")
	r.kvs[keys.KeyFrostBtcUtxoFIFOHead(vault)] = []byte("1")
	r.kvs[keys.KeyFrostBtcUtxoFIFOSeq(vault)] = []byte("3")
	seedUtxo(t, r, vault, 1, "locked", 0, "9000", true)
	seedUtxo(t, r, vault, 2, "small", 0, "1000", false)
	seedUtxo(t, r, vault, 3, "large", 0, "7000", false)

	got, err := p.calculateVaultAvailableBalance("btc", "BTC", vault)
	if err != nil {
		t.Fatalf("calculateVaultAvailableBalance: %v", err)
	}
	if got.Cmp(big.NewInt(12345)) != 0 {
		t.Fatalf("expected aggregate balance 12345, got %s", got)
	}
	if p.transientCache[vault].Cmp(big.NewInt(12345)) != 0 {
		t.Fatalf("cache not populated: %+v", p.transientCache)
	}
}

func TestCalculateVaultAvailableBalance_AccountReadsAggregateBalance(t *testing.T) {
	r := &testStateReader{kvs: map[string][]byte{}}
	p := &JobWindowPlanner{stateReader: r, transientCache: map[uint32]*big.Int{}}
	chainName, asset := "eth", "USDT"
	vault := uint32(2)
	r.kvs[keys.KeyFrostVaultAvailableBalance(chainName, asset, vault)] = []byte("9999")
	r.kvs[keys.KeyFrostFundsLotHead(chainName, asset, vault)] = []byte("10|2")
	r.kvs[keys.KeyFrostFundsLotSeq(chainName, asset, vault, 10)] = []byte("2")
	r.kvs[keys.KeyFrostFundsLotSeq(chainName, asset, vault, 11)] = []byte("1")
	r.kvs[keys.KeyFrostFundsLotIndex(chainName, asset, vault, 10, 1)] = []byte("req_60")
	r.kvs[keys.KeyFrostFundsLotIndex(chainName, asset, vault, 10, 2)] = []byte("req_50")
	r.kvs[keys.KeyFrostFundsLotIndex(chainName, asset, vault, 11, 1)] = []byte("req_30")
	for id, amt := range map[string]string{"req_60": "60", "req_50": "50", "req_30": "30"} {
		data, _ := proto.Marshal(&pb.RechargeRequest{Amount: amt})
		r.kvs[keys.KeyRechargeRequest(id)] = data
	}

	got, err := p.calculateVaultAvailableBalance(chainName, asset, vault)
	if err != nil {
		t.Fatalf("calculateVaultAvailableBalance: %v", err)
	}
	if got.Cmp(big.NewInt(9999)) != 0 {
		t.Fatalf("expected aggregate balance 9999, got %s", got)
	}
}

func TestDeductVaultBalance(t *testing.T) {
	p := &JobWindowPlanner{transientCache: map[uint32]*big.Int{1: big.NewInt(1000), 2: big.NewInt(50)}}
	p.deductVaultBalance(1, big.NewInt(400))
	if p.transientCache[1].Cmp(big.NewInt(600)) != 0 {
		t.Fatalf("expected 600, got %s", p.transientCache[1])
	}
	                              
	p.deductVaultBalance(2, big.NewInt(100))
	if p.transientCache[2].Sign() != 0 {
		t.Fatalf("expected 0, got %s", p.transientCache[2])
	}
	                               
	p.deductVaultBalance(99, big.NewInt(10))
}

                                                                           

func TestCalculateAccountChainBalance(t *testing.T) {
	r := &testStateReader{kvs: map[string][]byte{}}
	p := &JobWindowPlanner{stateReader: r}
	                                                  
	r.kvs["v1_frost_funds_lot_eth_USDT_0_0000000001"] = []byte("req_A")
	r.kvs["v1_frost_funds_lot_eth_USDT_0_0000000002"] = []byte("req_B")
	for id, amt := range map[string]string{"req_A": "100", "req_B": "250"} {
		data, _ := proto.Marshal(&pb.RechargeRequest{Amount: amt})
		r.kvs[keys.KeyRechargeRequest(id)] = data
	}

	got, err := p.calculateAccountChainBalance("eth", "USDT", 0)
	if err != nil {
		t.Fatalf("calculateAccountChainBalance: %v", err)
	}
	if got.Cmp(big.NewInt(350)) != 0 {
		t.Fatalf("expected 350, got %s", got)
	}
}

func TestVaultBalanceAndDeductionPreserveAmountsAboveUint64(t *testing.T) {
	const available = "100000000000000000000000000000000000000"
	const withdrawal = "99999999999999999999999999999999999999"
	r := &testStateReader{kvs: map[string][]byte{
		keys.KeyFrostVaultAvailableBalance("eth", "native", 0): []byte(available),
	}}
	p := &JobWindowPlanner{stateReader: r, transientCache: map[uint32]*big.Int{}}
	got, err := p.calculateVaultAvailableBalance("eth", "native", 0)
	if err != nil || got.String() != available {
		t.Fatalf("large balance mismatch: got=%v err=%v", got, err)
	}
	amount, _ := new(big.Int).SetString(withdrawal, 10)
	p.deductVaultBalance(0, amount)
	if p.transientCache[0].String() != "1" {
		t.Fatalf("large deduction mismatch: %s", p.transientCache[0])
	}
}
