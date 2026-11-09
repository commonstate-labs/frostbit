package vm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"dex/formal/itftrace"
	bridgeasset "dex/frost/asset"
	"dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

const formalBTCIsolationUnit = uint64(100000)

                                                                         
                                                                
func formalBTCIsolationRecord(t *testing.T, raw json.RawMessage) itftrace.State {
	t.Helper()
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil || values == nil {
		t.Fatalf("invalid ITF record: %v", err)
	}
	return itftrace.State{Values: values}
}

func formalBTCIsolationMap(t *testing.T, s itftrace.State, field string, id int64) itftrace.State {
	t.Helper()
	var entries struct {
		Pairs [][]json.RawMessage `json:"#map"`
	}
	if err := json.Unmarshal(s.Values[field], &entries); err != nil {
		t.Fatalf("ITF map %s: %v", field, err)
	}
	for _, pair := range entries.Pairs {
		if len(pair) != 2 {
			t.Fatal("malformed ITF pair")
		}
		key := itftrace.State{Values: map[string]json.RawMessage{"key": pair[0]}}
		value, err := key.Int("key")
		if err != nil {
			t.Fatal(err)
		}
		if value == id {
			return formalBTCIsolationRecord(t, pair[1])
		}
	}
	t.Fatalf("ITF map %s has no key %d", field, id)
	return itftrace.State{}
}

func formalBTCIsolationInt(t *testing.T, s itftrace.State, field string) int64 {
	t.Helper()
	v, err := s.Int(field)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func formalBTCIsolationBool(t *testing.T, s itftrace.State, field string) bool {
	t.Helper()
	v, err := s.Bool(field)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func formalBTCIsolationScalarMap(t *testing.T, s itftrace.State, field string, id int64) int64 {
	t.Helper()
	var entries struct {
		Pairs [][]json.RawMessage `json:"#map"`
	}
	if err := json.Unmarshal(s.Values[field], &entries); err != nil {
		t.Fatal(err)
	}
	for _, pair := range entries.Pairs {
		if len(pair) != 2 {
			t.Fatal("malformed scalar ITF pair")
		}
		wrapped := itftrace.State{Values: map[string]json.RawMessage{"key": pair[0], "value": pair[1]}}
		if formalBTCIsolationInt(t, wrapped, "key") == id {
			return formalBTCIsolationInt(t, wrapped, "value")
		}
	}
	t.Fatalf("missing scalar ITF key %s/%d", field, id)
	return 0
}

type formalBTCIsolationHarness struct {
	sv       StateView
	asset    string
	to       string
	owners   [2]string
	parents  [2]string
	roots    [2]*pb.FrostUtxo
	origins  [2][]byte
	claims   map[int64]*pb.FrostWithdrawClaimTx
	signed   map[int64]*pb.AnyTx
	fixtures map[int64]*btcWithdrawValidationFixture
}

func formalBTCIsolationHarnessNew(t *testing.T) *formalBTCIsolationHarness {
	t.Helper()
	a, err := bridgeasset.Resolve("btc", "")
	if err != nil {
		t.Fatal(err)
	}
	h := &formalBTCIsolationHarness{sv: newMemoryStateView(map[string][]byte{}), asset: a.ID,
		to:     btcChangeAddressForTest(t, btcDualTestScript(t)),
		owners: [2]string{"formal-btc-owner-a", "formal-btc-owner-b"},
		claims: map[int64]*pb.FrostWithdrawClaimTx{}, signed: map[int64]*pb.AnyTx{},
		fixtures: map[int64]*btcWithdrawValidationFixture{}}
	enableBTCIsolationForTest(t, h.sv)
	h.sv.Set(keys.KeyFrostVaultConfig("btc", 0), mustMarshalProto(t, &pb.FrostVaultConfig{
		Chain: "btc", VaultCount: 1, SignAlgo: chain.SignAlgoBTC, BtcNetwork: "mainnet",
		BtcFeeSatsPerVbyte: 25, GuardianPubkey: btcDualTestGuardianPubkey(), DepositDexChainId: 51025,
	}))
	h.sv.Set(keys.KeyFrostVaultState("btc", 0), mustMarshalProto(t, &pb.FrostVaultState{
		Chain: "btc", VaultId: 0, KeyEpoch: 1, GroupPubkey: btcDualTestOperatorPubkey(),
		SignAlgo: chain.SignAlgoBTC, Status: "ACTIVE", CommitteeMembers: []string{"miner-a", "miner-b", "miner-c"},
	}))
	h.sv.Set(keys.KeyToken(a.ID), mustMarshalProto(t, &pb.Token{Address: a.ID, NativeChain: "btc",
		Type: pb.TokenType_TOKEN_BRIDGED, TotalSupply: "1200000"}))
	h.sv.Set(keys.KeyNativeTokenIndex("btc", ""), []byte(a.ID))
	for i := range h.owners {
		SetBalance(h.sv, h.owners[i], a.ID, &pb.TokenBalance{Balance: "600000"})
	}
	for r := range h.roots {
		h.origins[r] = bytes.Repeat([]byte{byte(r + 1)}, 32)
		u := &pb.FrostUtxo{Txid: strings.Repeat(fmt.Sprintf("%x", r+1), 64), Amount: "600000",
			VaultId: 0, FinalizeHeight: 10, ScriptPubkey: btcDualTestScript(t), OriginId: h.origins[r]}
		h.roots[r] = u
		raw := mustMarshalProto(t, u)
		h.sv.Set(keys.KeyFrostBtcUtxo(0, u.Txid, 0), raw)
		h.sv.Set(keys.KeyFrostBtcUtxoFIFOIndex(0, uint64(r+1)), raw)
		if err := setBTCSpendableUTXOIndex(h.sv, 0, uint64(r+1), u, raw); err != nil {
			t.Fatal(err)
		}
	}
	h.sv.Set(keys.KeyFrostBtcUtxoFIFOSeq(0), []byte("2"))
	h.sv.Set(keys.KeyFrostBtcUtxoFIFOHead(0), []byte("1"))
	h.sv.Set(keys.KeyFrostVaultAvailableBalance("btc", a.ID, 0), []byte("1200000"))
	return h
}

func formalBTCIsolationRequireResult(t *testing.T, ops []WriteOp, receipt *Receipt, err error, accepted bool) {
	t.Helper()
	got := err == nil && receipt != nil && receipt.Status == "SUCCEED"
	if got != accepted {
		t.Fatalf("production accepted=%v model=%v err=%v receipt=%+v", got, accepted, err, receipt)
	}
	if len(ops) != 0 {
		t.Fatalf("v3 handler returned duplicate write set (%d)", len(ops))
	}
}

func (h *formalBTCIsolationHarness) request(t *testing.T, p int64) {
	t.Helper()
	txID := fmt.Sprintf("formal-btc-request-%d", p)
	tx := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawRequestTx{FrostWithdrawRequestTx: &pb.FrostWithdrawRequestTx{
		Base:  &pb.BaseMessage{TxId: txID, FromAddress: h.owners[p], ExecutedHeight: 100 + uint64(p)},
		Chain: "btc", Asset: h.asset, To: h.to, Amount: "600000",
		BtcProtocolVersion: 3, BtcMaxTotalFee: "30000", BtcMaxParts: 3,
	}}}
	ops, receipt, err := (&FrostWithdrawRequestTxHandler{}).DryRun(tx, h.sv)
	formalBTCIsolationRequireResult(t, ops, receipt, err, true)
	ref, found, err := h.sv.Get(keys.KeyFrostWithdrawTxRef(txID))
	if err != nil || !found {
		t.Fatalf("request ref: %v", err)
	}
	h.parents[p] = string(ref)
}

func (h *formalBTCIsolationHarness) claim(t *testing.T, p, c, r, modelGross int64, accepted bool, tamperContext ...bool) {
	t.Helper()
	parent, err := readBTCIsolationParent(h.sv, h.parents[p])
	if err != nil {
		t.Fatal(err)
	}
	u := h.roots[r]
	amount, err := strconv.ParseUint(u.Amount, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	gross := uint64(modelGross) * formalBTCIsolationUnit
	recipientScript, err := btc.AddressScript(h.to)
	if err != nil {
		t.Fatal(err)
	}
	net, fee, change, err := btc.ExpectedUserPaidWithdrawAmounts(amount, []uint64{gross}, 1,
		[][]byte{recipientScript}, u.ScriptPubkey, 25)
	if err != nil {
		t.Fatal(err)
	}
	changeAddress := ""
	if change > 0 {
		changeAddress = btcChangeAddressForTest(t, u.ScriptPubkey)
	}
	template, err := btc.NewBTCTemplate(chain.WithdrawTemplateParams{Chain: "btc", Asset: h.asset,
		VaultID: 0, KeyEpoch: 1, WithdrawIDs: []string{parent.WithdrawID},
		Outputs:        []chain.WithdrawOutput{{WithdrawID: parent.WithdrawID, To: parent.To, Amount: strconv.FormatUint(net[0], 10)}},
		Inputs:         []chain.UTXO{{TxID: u.Txid, Vout: u.Vout, Amount: amount, ScriptPubKey: u.ScriptPubkey}},
		GuardianPubkey: btcDualTestGuardianPubkey(), BTCInputPubkeys: [][]byte{btcDualTestOperatorPubkey()},
		Fee: fee, ChangeAmount: change, ChangeAddress: changeAddress})
	if err != nil {
		t.Fatal(err)
	}
	data, err := template.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	jobID := chain.BTCWithdrawPartJobID(chain.BTCWithdrawPartIntent{Network: 51025, BTCNetwork: "mainnet",
		ParentID: parent.WithdrawID, PartSeq: parent.BtcNextPartSeq, VaultID: 0, KeyEpoch: 1,
		Gross: gross, Fee: fee, OriginID: u.OriginId, TemplateHash: template.TemplateHash()})
	coordinator, err := chain.WithdrawCoordinator(jobID, 1, []string{"miner-a", "miner-b", "miner-c"})
	if err != nil {
		t.Fatal(err)
	}
	claim := &pb.FrostWithdrawClaimTx{Base: &pb.BaseMessage{TxId: "claim-" + jobID, FromAddress: coordinator, ExecutedHeight: 120},
		JobId: jobID, Chain: "btc", Asset: h.asset, VaultId: 0, KeyEpoch: 1, WithdrawIds: []string{parent.WithdrawID},
		TemplateData: data, TemplateHash: template.TemplateHash(), BtcProtocolVersion: 3,
		BtcPartSeq: parent.BtcNextPartSeq, BtcGrossAmount: strconv.FormatUint(gross, 10)}
	if len(tamperContext) > 0 && tamperContext[0] {
		                                                                      
		                                                                       
		claim.BtcPartSeq++
	}
	before := formalBTCRows(t, h.sv)
	ops, receipt, err := (&FrostWithdrawClaimTxHandler{}).DryRun(&pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawClaimTx{FrostWithdrawClaimTx: claim}}, h.sv)
	formalBTCIsolationRequireResult(t, ops, receipt, err, accepted)
	if !accepted {
		if !reflect.DeepEqual(before, formalBTCRows(t, h.sv)) {
			t.Fatal("rejected Claim changed state")
		}
		return
	}
	h.claims[c] = claim
	h.fixtures[c] = &btcWithdrawValidationFixture{sv: h.sv, template: template,
		committee: []string{"miner-a", "miner-b", "miner-c"}, scriptPubKey: u.ScriptPubkey,
		txID: u.Txid, withdrawID: parent.WithdrawID, asset: h.asset, vaultID: 0, keyEpoch: 1}
}

type formalBTCIsolationFailRead struct {
	StateView
	key   string
	fired bool
}

func (v *formalBTCIsolationFailRead) Get(key string) ([]byte, bool, error) {
	if key == v.key {
		v.fired = true
		return nil, false, errors.New("formal injected late BTC read failure")
	}
	return v.StateView.Get(key)
}
func (v *formalBTCIsolationFailRead) NetworkChainID() uint64 {
	return networkChainIDFromView(v.StateView)
}

func (h *formalBTCIsolationHarness) finalize(t *testing.T, c int64, accepted bool) {
	t.Helper()
	tx := h.signed[c]
	if tx == nil {
		t.Fatal("model finalized without producing real dual-signed raw")
	}
	view := h.sv
	var failing *formalBTCIsolationFailRead
	if !accepted {
		id, err := h.fixtures[c].template.TxID()
		if err != nil {
			t.Fatal(err)
		}
		failing = &formalBTCIsolationFailRead{StateView: h.sv, key: keys.KeyFrostBtcUtxo(0, id, 1)}
		view = failing
	}
	before := formalBTCRows(t, h.sv)
	ops, receipt, err := (&FrostWithdrawSignedTxHandler{}).DryRun(tx, view)
	formalBTCIsolationRequireResult(t, ops, receipt, err, accepted)
	if !accepted {
		if !failing.fired {
			t.Fatal("late failure injection was not reached")
		}
		if !reflect.DeepEqual(before, formalBTCRows(t, h.sv)) {
			t.Fatal("late Signed failure retained artifact/UTXO/ledger writes")
		}
		return
	}
	template := h.fixtures[c].template
	id, err := template.TxID()
	if err != nil {
		t.Fatal(err)
	}
	if len(template.Outputs) == 2 {
		changeRaw, found, err := h.sv.Get(keys.KeyFrostBtcUtxo(0, id, 1))
		var change pb.FrostUtxo
		if err != nil || !found || proto.Unmarshal(changeRaw, &change) != nil {
			t.Fatalf("change missing: %v", err)
		}
		for r := range h.roots {
			if bytes.Equal(h.roots[r].OriginId, change.OriginId) {
				h.roots[r] = &change
			}
		}
	}
}

func (h *formalBTCIsolationHarness) compare(t *testing.T, s itftrace.State) {
	t.Helper()
	for p := int64(0); p < 2; p++ {
		m := formalBTCIsolationMap(t, s, "parents", p)
		wantBalance := formalBTCIsolationScalarMap(t, s, "balances", p) * int64(formalBTCIsolationUnit)
		if got := GetBalance(h.sv, h.owners[p], h.asset).Balance; got != strconv.FormatInt(wantBalance, 10) {
			t.Fatalf("owner %d balance=%s model=%d", p, got, wantBalance)
		}
		if !formalBTCIsolationBool(t, m, "requested") {
			continue
		}
		parent, err := readBTCIsolationParent(h.sv, h.parents[p])
		if err != nil {
			t.Fatal(err)
		}
		for field, got := range map[string]string{"u": parent.BtcUnallocatedAmount, "c": parent.BtcClaimedAmount,
			"s": parent.BtcSignedAmount, "refund": parent.BtcRefundedAmount} {
			want := formalBTCIsolationInt(t, m, field) * int64(formalBTCIsolationUnit)
			if got != strconv.FormatInt(want, 10) {
				t.Fatalf("parent %d %s=%s model=%d", p, field, got, want)
			}
		}
		if parent.BtcNextPartSeq != uint64(formalBTCIsolationInt(t, m, "nextSeq")) {
			t.Fatal("child sequence projection differs")
		}
		var fee uint64
		for c := int64(0); c < 4; c++ {
			mc := formalBTCIsolationMap(t, s, "children", c)
			if formalBTCIsolationInt(t, mc, "stage") == 0 || formalBTCIsolationInt(t, mc, "parent") != p {
				continue
			}
			job, found, err := readBTCIsolationJob(h.sv, h.claims[c].JobId)
			if err != nil || !found {
				t.Fatalf("child projection: %v", err)
			}
			f, err := strconv.ParseUint(job.BtcFeeAmount, 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			fee += f
		}
		if parent.BtcCommittedFee != strconv.FormatUint(fee, 10) {
			t.Fatal("cumulative actual fee projection differs")
		}
	}
	var token pb.Token
	raw, _, _ := h.sv.Get(keys.KeyToken(h.asset))
	if err := proto.Unmarshal(raw, &token); err != nil {
		t.Fatal(err)
	}
	if token.TotalSupply != strconv.FormatInt(formalBTCIsolationInt(t, s, "supply")*int64(formalBTCIsolationUnit), 10) {
		t.Fatalf("supply=%s differs from model", token.TotalSupply)
	}
	for c := int64(0); c < 4; c++ {
		m := formalBTCIsolationMap(t, s, "children", c)
		stage := formalBTCIsolationInt(t, m, "stage")
		if stage == 0 {
			continue
		}
		claim := h.claims[c]
		job, found, err := readBTCIsolationJob(h.sv, claim.JobId)
		if err != nil || !found {
			t.Fatalf("child missing: %v", err)
		}
		wantStatus := "CLAIMED"
		if stage == 2 {
			wantStatus = "SIGNED"
		}
		if job.Status != wantStatus || job.BtcGrossAmount != strconv.FormatInt(formalBTCIsolationInt(t, m, "gross")*int64(formalBTCIsolationUnit), 10) ||
			!bytes.Equal(job.BtcOriginId, h.origins[formalBTCIsolationInt(t, m, "root")]) {
			t.Fatalf("child differs: %+v", job)
		}
		if stage == 2 {
			for _, key := range []string{keys.KeyFrostPermanentWithdrawJob(job.JobId), keys.KeyFrostPermanentSignedPackage(job.JobId, 0), keys.KeyFrostPermanentWithdrawArtifact(job.JobId)} {
				if value, found, err := h.sv.Get(key); err != nil || !found || len(value) == 0 {
					t.Fatalf("Signed permanent evidence missing %s: %v", key, err)
				}
			}
		}
	}
	for r := int64(0); r < 2; r++ {
		m := formalBTCIsolationMap(t, s, "roots", r)
		u := h.roots[r]
		raw, found, err := h.sv.Get(keys.KeyFrostBtcUtxo(0, u.Txid, u.Vout))
		want := formalBTCIsolationInt(t, m, "amount") * int64(formalBTCIsolationUnit)
		if err != nil {
			t.Fatal(err)
		}
		if want == 0 {
			if found {
				t.Fatal("exhausted root retains live input")
			}
			continue
		}
		var live pb.FrostUtxo
		if !found || proto.Unmarshal(raw, &live) != nil || live.Amount != strconv.FormatInt(want, 10) || !bytes.Equal(live.OriginId, h.origins[r]) {
			t.Fatalf("root %d differs: %+v", r, &live)
		}
		lock, locked, err := h.sv.Get(keys.KeyFrostBtcLockedUtxo(0, u.Txid, u.Vout))
		if err != nil {
			t.Fatal(err)
		}
		modelLock := formalBTCIsolationInt(t, m, "lock")
		if modelLock >= 0 {
			if !locked || string(lock) != h.claims[modelLock].JobId {
				t.Fatal("root lock owner differs")
			}
		} else if locked {
			t.Fatal("unclaimed root remains locked")
		}
		_, indexed, err := h.sv.Get(keys.KeyFrostBtcUtxoAmount(0, uint64(want), u.Txid, u.Vout))
		if err != nil || indexed != (modelLock == -1) {
			t.Fatalf("amount index spendability differs: %v", err)
		}
	}
}

func TestFormalBTCJobIsolationLedgerITFDifferential(t *testing.T) {
	traceRoot := os.Getenv("DEX_FORMAL_ITF_BTC_JOB_ISOLATION")
	if traceRoot == "" {
		t.Skip("run formal/formal_btc_job_isolation.ps1 for retained model traces")
	}
	for _, name := range []string{"partialCancelAndSignedReplayTest", "independentRootContinuesWhileOneClaimStallsTest",
		"descendantGrossTelescopesTest", "failedArtifactIsAtomicTest", "changedIntentRejectedTest",
		"exclusiveClaimAndBudgetTest", "claimCannotTimeoutRefundTest"} {
		t.Run(name, func(t *testing.T) {
			files, err := filepath.Glob(filepath.Join(traceRoot, name+"_*.itf.json"))
			if err != nil || len(files) != 1 {
				t.Fatalf("expected exactly one ITF trace for %s: %v %v", name, files, err)
			}
			trace, err := itftrace.Load(files[0])
			if err != nil {
				t.Fatal(err)
			}
			h := formalBTCIsolationHarnessNew(t)
			for edge, s := range trace.States {
				if edge > 0 {
					p, c, r, gross := formalBTCIsolationInt(t, s, "lastParent"), formalBTCIsolationInt(t, s, "lastChild"), formalBTCIsolationInt(t, s, "lastRoot"), formalBTCIsolationInt(t, s, "lastGross")
					accepted := formalBTCIsolationBool(t, s, "lastAccepted")
					switch formalBTCIsolationInt(t, s, "stepId") {
					case 1:
						h.request(t, p)
					case 2:
						h.claim(t, p, c, r, gross, accepted, name == "changedIntentRejectedTest")
					case 3:
						h.signed[c] = btcIsolationSignedForTest(t, h.fixtures[c], h.claims[c])
						h.signed[c].GetFrostWithdrawSignedTx().Base.TxId = fmt.Sprintf("formal-child-signed-%d", c)
					case 4:
						h.finalize(t, c, accepted)
					case 5:
						tx := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawCancelTx{FrostWithdrawCancelTx: &pb.FrostWithdrawCancelTx{
							Base: &pb.BaseMessage{TxId: fmt.Sprintf("formal-cancel-%d", p), FromAddress: h.owners[p], ExecutedHeight: 130}, WithdrawId: h.parents[p]}}}
						ops, receipt, err := (&FrostWithdrawCancelTxHandler{}).DryRun(tx, h.sv)
						formalBTCIsolationRequireResult(t, ops, receipt, err, true)
					case 6:                                                        
					case 7:
						h.sv = newMemoryStateView(formalBTCRows(t, h.sv))
						h.sv.(interface{ BindNetworkChainID(uint64) }).BindNetworkChainID(51025)
					default:
						t.Fatalf("unmapped model edge %d", formalBTCIsolationInt(t, s, "stepId"))
					}
				}
				t.Run(fmt.Sprintf("edge-%d", edge), func(t *testing.T) { h.compare(t, s) })
			}
		})
	}
}

func TestFormalBTCJobIsolationRequestReplayAndCancelClaimRace(t *testing.T) {
	for _, cancelFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel-first-%t", cancelFirst), func(t *testing.T) {
			h := formalBTCIsolationHarnessNew(t)
			h.request(t, 0)
			beforeReplay := formalBTCRows(t, h.sv)
			h.request(t, 0)
			if !reflect.DeepEqual(beforeReplay, formalBTCRows(t, h.sv)) {
				t.Fatal("request replay repeated burn or ledger initialization")
			}
			cancel := func() {
				tx := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawCancelTx{FrostWithdrawCancelTx: &pb.FrostWithdrawCancelTx{
					Base: &pb.BaseMessage{TxId: "race-cancel", FromAddress: h.owners[0], ExecutedHeight: 130}, WithdrawId: h.parents[0]}}}
				ops, receipt, err := (&FrostWithdrawCancelTxHandler{}).DryRun(tx, h.sv)
				formalBTCIsolationRequireResult(t, ops, receipt, err, true)
			}
			if cancelFirst {
				cancel()
				h.claim(t, 0, 0, 0, 2, false)
			} else {
				h.claim(t, 0, 0, 0, 2, true)
				cancel()
			}
			p, err := readBTCIsolationParent(h.sv, h.parents[0])
			if err != nil {
				t.Fatal(err)
			}
			if cancelFirst {
				if p.BtcClaimedAmount != "0" || p.BtcRefundedAmount != "600000" {
					t.Fatalf("cancel-first: %+v", p)
				}
			} else if p.BtcClaimedAmount != "200000" || p.BtcRefundedAmount != "400000" {
				t.Fatalf("claim-first: %+v", p)
			}
		})
	}
}

func TestFormalBTCJobIsolationExecutorRollbackRestoresWholeBusinessState(t *testing.T) {
	for _, step := range []string{"request", "claim", "cancel", "signed"} {
		t.Run(step, func(t *testing.T) {
			h := formalBTCIsolationHarnessNew(t)
			if step != "request" {
				h.request(t, 0)
			}
			if step == "signed" {
				h.claim(t, 0, 0, 0, 6, true)
			}
			before := formalBTCRows(t, h.sv)
			snap := h.sv.Snapshot()
			switch step {
			case "request":
				h.request(t, 0)
			case "claim":
				h.claim(t, 0, 0, 0, 2, true)
			case "cancel":
				tx := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawCancelTx{FrostWithdrawCancelTx: &pb.FrostWithdrawCancelTx{Base: &pb.BaseMessage{TxId: "rollback-cancel", FromAddress: h.owners[0]}, WithdrawId: h.parents[0]}}}
				ops, r, e := (&FrostWithdrawCancelTxHandler{}).DryRun(tx, h.sv)
				formalBTCIsolationRequireResult(t, ops, r, e, true)
			case "signed":
				tx := btcIsolationSignedForTest(t, h.fixtures[0], h.claims[0])
				ops, r, e := (&FrostWithdrawSignedTxHandler{}).DryRun(tx, h.sv)
				formalBTCIsolationRequireResult(t, ops, r, e, true)
			}
			if err := h.sv.Revert(snap); err != nil {
				t.Fatal(err)
			}
			after := formalBTCRows(t, h.sv)
			if !reflect.DeepEqual(before, after) {
				changed := []string{}
				for k, v := range after {
					if !bytes.Equal(v, before[k]) {
						changed = append(changed, k)
					}
				}
				for k := range before {
					if _, ok := after[k]; !ok {
						changed = append(changed, k)
					}
				}
				t.Fatalf("executor rollback retained business keys: %v", changed)
			}
		})
	}
}
