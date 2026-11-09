package planning

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"

	"dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/keys"
	"dex/pb"
	"dex/protocol"
	"dex/utils"
	"github.com/btcsuite/btcd/chaincfg"
	"google.golang.org/protobuf/proto"
)

type isolatedPlanningReader struct {
	*testStateReader
	amountScans, amountKeys int
}

func (r *isolatedPlanningReader) ScanOrderedAfter(prefix, after string, limit int, reverse bool, fn func(string, []byte) bool) error {
	ks := []string{}
	for k := range r.kvs {
		if strings.HasPrefix(k, prefix) && (after == "" || !reverse && k > after || reverse && k < after) {
			ks = append(ks, k)
		}
	}
	sort.Strings(ks)
	if reverse {
		for i, j := 0, len(ks)-1; i < j; i, j = i+1, j-1 {
			ks[i], ks[j] = ks[j], ks[i]
		}
	}
	if limit > 0 && len(ks) > limit {
		ks = ks[:limit]
	}
	if strings.Contains(prefix, "btc_amount_") {
		r.amountScans++
		r.amountKeys += len(ks)
	}
	for _, k := range ks {
		if !fn(k, r.kvs[k]) {
			break
		}
	}
	return nil
}

func isolatedPlannerFixture(t *testing.T, amounts []uint64, gross uint64, parts uint32) (*isolatedPlanningReader, *JobWindowPlanner, *pb.FrostWithdrawState) {
	t.Helper()
	r := &isolatedPlanningReader{testStateReader: &testStateReader{kvs: map[string][]byte{}}}
	put := func(k string, m proto.Message) {
		raw, e := proto.Marshal(m)
		if e != nil {
			t.Fatal(e)
		}
		r.kvs[k] = raw
	}
	cfg := &pb.FrostVaultConfig{Chain: "btc", VaultCount: 1, BtcNetwork: "regtest", BtcFeeSatsPerVbyte: 1, GuardianPubkey: planningBTCGuardian(), DepositDexChainId: 51025}
	put(keys.KeyFrostVaultConfig("btc", 0), cfg)
	put(keys.KeyFrostVaultState("btc", 0), &pb.FrostVaultState{Chain: "btc", VaultId: 0, KeyEpoch: 1, Status: "ACTIVE", GroupPubkey: planningBTCOperator(), SignAlgo: chain.SignAlgoBTC})
	r.kvs[keys.KeyProtocolVersion()] = []byte("3")
	r.kvs[keys.KeyProtocolUpgradeReceipt(3)] = []byte(protocol.UpgradeReceiptValue(protocol.Upgrade{Version: 3, ActivationHeight: 1, ID: protocol.UpgradeBTCIsolationV3}))
	put(keys.KeyFrostBtcUtxoAmountBuildState(), &pb.FrostBtcIsolationIndexState{Done: true, JobsDone: true})
	to, e := utils.DualCustodyTaprootAddress(planningBTCOperator(), planningBTCGuardian(), &chaincfg.RegressionNetParams)
	if e != nil {
		t.Fatal(e)
	}
	parent := &pb.FrostWithdrawState{WithdrawId: "parent-v3", Seq: 1, Chain: "btc", Asset: "BTC", To: to, Amount: strconv.FormatUint(gross, 10), GrossAmount: strconv.FormatUint(gross, 10), FeeAmount: "0", Status: "QUEUED", BtcProtocolVersion: 3, BtcUnallocatedAmount: strconv.FormatUint(gross, 10), BtcClaimedAmount: "0", BtcSignedAmount: "0", BtcRefundedAmount: "0", BtcMaxTotalFee: "1000000", BtcCommittedFee: "0", BtcMaxParts: parts, BtcNextPartSeq: 1}
	put(keys.KeyFrostWithdraw(parent.WithdrawId), parent)
	r.kvs[keys.KeyFrostBtcWithdrawReady("BTC", 1, parent.WithdrawId)] = []byte(parent.WithdrawId)
	for i, n := range amounts {
		hash := sha256.Sum256([]byte(fmt.Sprintf("input-%d", i)))
		txid := fmt.Sprintf("%x", hash)
		u := &pb.FrostUtxo{Txid: txid, Vout: 0, Amount: strconv.FormatUint(n, 10), VaultId: 0, ScriptPubkey: planningBTCScript(), OriginId: chain.BTCOriginID(51025, "regtest", txid, 0, false)}
		put(keys.KeyFrostBtcUtxo(0, txid, 0), u)
		r.kvs[keys.KeyFrostBtcUtxoAmount(0, n, txid, 0)] = []byte{1}
	}
	f := chain.NewDefaultAdapterFactory()
	f.RegisterAdapter(btc.NewBTCAdapter("regtest"))
	return r, NewJobWindowPlanner(r, f, 1), parent
}

func TestBTCIsolatedPlannerConsumesSmallInputsAndCachesTail(t *testing.T) {
	r, p, parent := isolatedPlannerFixture(t, []uint64{400000, 100000, 300000, 200000, 900000}, 1000000, 4)
	for part, want := range []uint64{100000, 200000, 300000, 400000} {
		before := r.amountScans
		jobs, logs, err := p.PlanJobWindow("btc", "BTC")
		if err != nil || len(jobs) != 1 {
			t.Fatalf("part=%d jobs=%v logs=%v err=%v", part, jobs, logs, err)
		}
		j := jobs[0]
		template, e := btc.FromJSON(j.TemplateData)
		if e != nil || len(template.Inputs) != 1 || len(template.Outputs) != 1 || j.BTCGrossAmount != strconv.FormatUint(want, 10) || j.BTCPartSeq != uint64(part+1) {
			t.Fatalf("incorrect isolated small input part: %+v", j)
		}
		if part > 0 && r.amountScans != before {
			t.Fatalf("cached tail rescanned amount index: before=%d after=%d", before, r.amountScans)
		}
		input := template.Inputs[0]
		delete(r.kvs, keys.KeyFrostBtcUtxo(0, input.TxID, input.Vout))
		delete(r.kvs, keys.KeyFrostBtcUtxoAmount(0, input.Amount, input.TxID, input.Vout))
		u, _ := strconv.ParseUint(parent.BtcUnallocatedAmount, 10, 64)
		fee, _ := strconv.ParseUint(parent.BtcCommittedFee, 10, 64)
		jf, _ := strconv.ParseUint(j.BTCFeeAmount, 10, 64)
		c, _ := strconv.ParseUint(parent.BtcClaimedAmount, 10, 64)
		parent.BtcUnallocatedAmount = strconv.FormatUint(u-want, 10)
		parent.BtcClaimedAmount = strconv.FormatUint(c+want, 10)
		parent.BtcCommittedFee = strconv.FormatUint(fee+jf, 10)
		parent.BtcNextPartSeq++
		r.kvs[keys.KeyFrostWithdraw(parent.WithdrawId)], _ = proto.Marshal(parent)
	}
	if r.amountKeys > 5+4*64 {
		t.Fatalf("unexpected index read amplification: %d", r.amountKeys)
	}
}

func TestBTCIsolatedPlannerBudgetCanPreferOneLargerInput(t *testing.T) {
	r, p, parent := isolatedPlannerFixture(t, []uint64{100000, 200000, 300000, 600000}, 600000, 1)
	_ = r
	jobs, logs, e := p.PlanJobWindow("btc", "BTC")
	if e != nil || len(jobs) != 1 {
		t.Fatalf("jobs=%v logs=%v err=%v", jobs, logs, e)
	}
	template, _ := btc.FromJSON(jobs[0].TemplateData)
	if len(template.Inputs) != 1 || template.Inputs[0].Amount != 600000 || len(template.Outputs) != 1 {
		t.Fatal("budget did not force independent exact input")
	}
	parent.BtcMaxParts = 4
	parent.BtcMaxTotalFee = "1"
	r.kvs[keys.KeyFrostWithdraw(parent.WithdrawId)], _ = proto.Marshal(parent)
	jobs, _, e = p.PlanJobWindow("btc", "BTC")
	if e != nil || len(jobs) != 0 {
		t.Fatalf("exceeded fee budget: %v %v", jobs, e)
	}
}

func TestBTCIsolatedPlannerReadinessAndFairRecovery(t *testing.T) {
	r, p, _ := isolatedPlannerFixture(t, []uint64{100000, 200000}, 100000, 2)
	active := &pb.FrostWithdrawJobState{JobId: "stalled-child", Chain: "btc", Asset: "BTC", BtcProtocolVersion: 3, Status: "CLAIMED", WithdrawIds: []string{"stalled-parent"}, TemplateHash: make([]byte, 32), BtcOriginId: make([]byte, 32)}
	r.kvs[keys.KeyFrostWithdrawJob(active.JobId)], _ = proto.Marshal(active)
	r.kvs[keys.KeyFrostBtcWithdrawActiveChild("BTC", active.JobId)] = []byte(active.JobId)
	first, _, e := p.PlanJobWindow("btc", "BTC")
	if e != nil || len(first) != 1 || first[0].JobID != active.JobId {
		t.Fatalf("recovery %v %v", first, e)
	}
	second, _, e := p.PlanJobWindow("btc", "BTC")
	if e != nil || len(second) != 1 || second[0].JobID == active.JobId {
		t.Fatalf("stalled claim starved ready work %v %v", second, e)
	}
	r.kvs[keys.KeyFrostBtcUtxoAmountBuildState()], _ = proto.Marshal(&pb.FrostBtcIsolationIndexState{Done: false})
	jobs, logs, e := p.PlanJobWindow("btc", "BTC")
	if e != nil || len(jobs) != 1 || jobs[0].JobID != active.JobId || len(logs) == 0 {
		t.Fatalf("building must retain recovery: %v %v %v", jobs, logs, e)
	}
}

func TestBTCIsolatedPlannerSeekSkipsSmallFragments(t *testing.T) {
	amounts := make([]uint64, 20000)
	for i := range amounts {
		amounts[i] = 100000
	}
	amounts = append(amounts, 100000000)
	r, p, _ := isolatedPlannerFixture(t, amounts, 100000000, 1)
	jobs, logs, e := p.PlanJobWindow("btc", "BTC")
	if e != nil || len(jobs) != 1 {
		t.Fatalf("%v %v %v", jobs, logs, e)
	}
	if r.amountKeys > btcIsolationPage || r.amountScans > 1 {
		t.Fatalf("tail seek scanned small prefix: scans=%d keys=%d", r.amountScans, r.amountKeys)
	}
}

func TestBTCIsolatedPlannerBudgetRetrySkipsTooSmallPrefix(t *testing.T) {
	_, p, _ := isolatedPlannerFixture(t, []uint64{100000, 500000, 500000}, 1000000, 2)
	jobs, logs, e := p.PlanJobWindow("btc", "BTC")
	if e != nil || len(jobs) != 1 || jobs[0].BTCGrossAmount != "500000" || len(p.btcPlans["parent-v3"].jobs) != 2 {
		t.Fatalf("budget retry failed: %v %v %v", jobs, logs, e)
	}
}

func TestBTCIsolatedPlanner4096EqualCandidatesRemainLinear(t *testing.T) {
	const count = 4096
	amounts := make([]uint64, count)
	for i := range amounts {
		amounts[i] = 100000
	}
	r, p, parent := isolatedPlannerFixture(t, amounts, count*100000, count)
	jobs, logs, e := p.PlanJobWindow("btc", "BTC")
	if e != nil || len(jobs) != 1 || len(p.btcPlans[parent.WithdrawId].jobs) != count {
		t.Fatalf("full bounded plan: jobs=%d logs=%v err=%v", len(jobs), logs, e)
	}
	if r.amountKeys > 2*count || r.amountScans > 2*(count/btcIsolationPage+1) || r.batchKeys > 2*count+16 {
		t.Fatalf("nonlinear equal-input plan: scans=%d amountKeys=%d batchKeys=%d", r.amountScans, r.amountKeys, r.batchKeys)
	}
	t.Logf("parts=%d amountScans=%d amountKeys=%d batchKeys=%d cacheBytes=%d", count, r.amountScans, r.amountKeys, r.batchKeys, p.btcPlanBytes)
}

func TestBTCIsolatedPlannerAmountPageBoundaries(t *testing.T) {
	for _, n := range []int{btcIsolationPage - 1, btcIsolationPage, btcIsolationPage + 1} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			amounts := make([]uint64, n)
			for i := range amounts {
				amounts[i] = 100000
			}
			r, p, parent := isolatedPlannerFixture(t, amounts, uint64(n)*100000, uint32(n))
			jobs, logs, e := p.PlanJobWindow("btc", "BTC")
			if e != nil || len(jobs) != 1 || len(p.btcPlans[parent.WithdrawId].jobs) != n {
				t.Fatalf("jobs=%v logs=%v err=%v", jobs, logs, e)
			}
			if r.amountKeys > 2*n || r.batchKeys > 2*n+3 {
				t.Fatalf("boundary read amplification: N=%d index=%d batch=%d", n, r.amountKeys, r.batchKeys)
			}
		})
	}
}

func TestBTCIsolatedFairQueuesDoNotSkipUndispatchedHalfPages(t *testing.T) {
	r, p, parent := isolatedPlannerFixture(t, []uint64{100000, 100000, 100000, 100000}, 100000, 2)
	p.maxInFlight = 2
	second := proto.Clone(parent).(*pb.FrostWithdrawState)
	second.WithdrawId = "parent-2"
	second.Seq = 2
	r.kvs[keys.KeyFrostWithdraw(second.WithdrawId)], _ = proto.Marshal(second)
	r.kvs[keys.KeyFrostBtcWithdrawReady("BTC", 2, second.WithdrawId)] = []byte(second.WithdrawId)
	for _, id := range []string{"active-1", "active-2"} {
		s := &pb.FrostWithdrawJobState{JobId: id, Chain: "btc", Asset: "BTC", Status: "CLAIMED", BtcProtocolVersion: 3, WithdrawIds: []string{"legacy-parent"}, TemplateHash: make([]byte, 32), BtcOriginId: make([]byte, 32)}
		r.kvs[keys.KeyFrostWithdrawJob(id)], _ = proto.Marshal(s)
		r.kvs[keys.KeyFrostBtcWithdrawActiveChild("BTC", id)] = []byte(id)
	}
	seen := map[string]bool{}
	for round := 0; round < 2; round++ {
		jobs, _, e := p.PlanJobWindow("btc", "BTC")
		if e != nil {
			t.Fatal(e)
		}
		for _, j := range jobs {
			if len(j.WithdrawIDs) == 1 {
				seen[j.WithdrawIDs[0]] = true
			}
			seen[j.JobID] = true
		}
	}
	if !seen[parent.WithdrawId] || !seen[second.WithdrawId] || !seen["active-1"] || !seen["active-2"] {
		t.Fatalf("cursor skipped work: %v", seen)
	}
}

func TestBTCIsolatedPlannerRejectsMissingNetworkBeforeCreatingChild(t *testing.T) {
	r, p, _ := isolatedPlannerFixture(t, []uint64{100000}, 100000, 1)
	var cfg pb.FrostVaultConfig
	if err := proto.Unmarshal(r.kvs[keys.KeyFrostVaultConfig("btc", 0)], &cfg); err != nil {
		t.Fatal(err)
	}
	cfg.DepositDexChainId = 0
	r.kvs[keys.KeyFrostVaultConfig("btc", 0)], _ = proto.Marshal(&cfg)
	jobs, _, err := p.PlanJobWindow("btc", "BTC")
	if err == nil || !strings.Contains(err.Error(), "network identity is missing") || len(jobs) != 0 || len(p.btcPlans) != 0 || len(p.btcReserved) != 0 || r.amountScans != 0 {
		t.Fatalf("missing immutable network generated work: jobs=%v plans=%d reservations=%d scans=%d err=%v", jobs, len(p.btcPlans), len(p.btcReserved), r.amountScans, err)
	}
}
