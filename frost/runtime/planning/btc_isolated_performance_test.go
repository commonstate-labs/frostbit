package planning_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"dex/db"
	"dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/frost/runtime/adapters"
	"dex/frost/runtime/planning"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"dex/protocol"
	"dex/utils"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/chaincfg"
	"google.golang.org/protobuf/proto"
)

type measuredIsolatedReader struct {
	*adapters.StateDBReader
	scans, keys, batches, gets int
	amountScans, amountKeys    int
}

func (r *measuredIsolatedReader) Get(k string) ([]byte, bool, error) {
	r.gets++
	return r.StateDBReader.Get(k)
}
func (r *measuredIsolatedReader) GetMany(ks []string) (map[string][]byte, error) {
	r.batches++
	r.keys += len(ks)
	return r.StateDBReader.GetMany(ks)
}
func (r *measuredIsolatedReader) ScanOrderedAfter(prefix, after string, limit int, reverse bool, fn func(string, []byte) bool) error {
	r.scans++
	if strings.Contains(prefix, "btc_amount_") {
		r.amountScans++
	}
	return r.StateDBReader.ScanOrderedAfter(prefix, after, limit, reverse, func(k string, v []byte) bool {
		if strings.Contains(prefix, "btc_amount_") {
			r.amountKeys++
		}
		return fn(k, v)
	})
}

                                                                              
                                                                             
                                                                      
func TestBTCIsolatedPlannerRealStateDBMillionQueueBound(t *testing.T) {
	if testing.Short() {
		t.Skip("real StateDB scale regression")
	}
	dbm, e := db.NewManager(t.TempDir(), logs.NewNodeLogger("btc-isolated-scale", 0))
	if e != nil {
		t.Fatal(e)
	}
	defer dbm.Close()
	dbm.InitWriteQueue(2048, time.Hour)
	put := func(k string, m proto.Message) {
		raw, e := proto.Marshal(m)
		if e != nil {
			t.Fatal(e)
		}
		dbm.EnqueueSet(k, string(raw))
	}
	op, _ := btcec.PrivKeyFromBytes([]byte{23})
	gp, _ := btcec.PrivKeyFromBytes([]byte{31})
	operator, guardian := schnorr.SerializePubKey(op.PubKey()), schnorr.SerializePubKey(gp.PubKey())
	script, _, _, e := utils.BuildDualCustodyTaproot(operator, guardian)
	if e != nil {
		t.Fatal(e)
	}
	to, e := utils.DualCustodyTaprootAddress(operator, guardian, &chaincfg.RegressionNetParams)
	if e != nil {
		t.Fatal(e)
	}
	put(keys.KeyFrostVaultConfig("btc", 0), &pb.FrostVaultConfig{Chain: "btc", VaultCount: 1, BtcNetwork: "regtest", BtcFeeSatsPerVbyte: 1, DepositDexChainId: 51025, GuardianPubkey: guardian})
	put(keys.KeyFrostVaultState("btc", 0), &pb.FrostVaultState{Chain: "btc", VaultId: 0, Status: "ACTIVE", KeyEpoch: 1, GroupPubkey: operator, SignAlgo: chain.SignAlgoBTC})
	dbm.EnqueueSet(keys.KeyProtocolVersion(), "3")
	dbm.EnqueueSet(keys.KeyProtocolUpgradeReceipt(3), protocol.UpgradeReceiptValue(protocol.Upgrade{Version: 3, ActivationHeight: 1, ID: protocol.UpgradeBTCIsolationV3}))
	put(keys.KeyFrostBtcUtxoAmountBuildState(), &pb.FrostBtcIsolationIndexState{Done: true, JobsDone: true})
	parent := &pb.FrostWithdrawState{WithdrawId: "parent-scale", Seq: 1, Chain: "btc", Asset: "BTC", To: to, Amount: "100000000", Status: "QUEUED", BtcProtocolVersion: 3, BtcUnallocatedAmount: "100000000", BtcClaimedAmount: "0", BtcSignedAmount: "0", BtcRefundedAmount: "0", BtcMaxTotalFee: "1000000", BtcCommittedFee: "0", BtcMaxParts: 1, BtcNextPartSeq: 1}
	put(keys.KeyFrostWithdraw(parent.WithdrawId), parent)
	const queued = 1000000
	for i := 1; i <= queued; i++ {
		id := fmt.Sprintf("later-%d", i)
		if i == 1 {
			id = parent.WithdrawId
		}
		dbm.EnqueueSet(keys.KeyFrostBtcWithdrawReady("BTC", uint64(i), id), id)
	}
	const utxos = 100000
	for i := 0; i < utxos; i++ {
		txid := fmt.Sprintf("%064x", i+1)
		amount := uint64(100000)
		if i == utxos-1 {
			amount = 100000000
		}
		u := &pb.FrostUtxo{Txid: txid, Amount: strconv.FormatUint(amount, 10), VaultId: 0, ScriptPubkey: script, OriginId: chain.BTCOriginID(51025, "regtest", txid, 0, false)}
		put(keys.KeyFrostBtcUtxo(0, txid, 0), u)
		dbm.EnqueueSet(keys.KeyFrostBtcUtxoAmount(0, amount, txid, 0), "1")
	}
	if e := dbm.ForceFlush(); e != nil {
		t.Fatal(e)
	}
	const tombstones = 5000
	for i := 0; i < tombstones; i++ {
		txid := fmt.Sprintf("%064x", i+1)
		dbm.EnqueueDelete(keys.KeyFrostBtcUtxo(0, txid, 0))
		dbm.EnqueueDelete(keys.KeyFrostBtcUtxoAmount(0, 100000, txid, 0))
	}
	if e := dbm.ForceFlush(); e != nil {
		t.Fatal(e)
	}
	r := &measuredIsolatedReader{StateDBReader: adapters.NewStateDBReader(dbm)}
	factory := chain.NewDefaultAdapterFactory()
	factory.RegisterAdapter(btc.NewBTCAdapter("regtest"))
	p := planning.NewJobWindowPlanner(r, factory, 1)
	start := time.Now()
	jobs, logs, e := p.PlanJobWindow("btc", "BTC")
	elapsed := time.Since(start)
	if e != nil || len(jobs) != 1 {
		t.Fatalf("jobs=%v logs=%v err=%v", jobs, logs, e)
	}
	if r.amountScans != 1 || r.amountKeys != 1 || r.scans > 3 || r.keys > 16 || r.gets > 8 {
		t.Fatalf("index amplification: scans=%d amount_scans=%d amount_keys=%d batches=%d keys=%d gets=%d", r.scans, r.amountScans, r.amountKeys, r.batches, r.keys, r.gets)
	}
	t.Logf("real StateDB queue=%d UTXOs=%d deleted=%d selected=1 scans=%d amount_scans=%d amount_keys=%d batch_calls=%d batch_keys=%d gets=%d elapsed=%s", queued, utxos, tombstones, r.scans, r.amountScans, r.amountKeys, r.batches, r.keys, r.gets, elapsed)
}
