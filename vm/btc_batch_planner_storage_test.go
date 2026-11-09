package vm_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"dex/db"
	chainpkg "dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/frost/runtime/planning"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"dex/utils"
	"dex/vm"

	"google.golang.org/protobuf/proto"
)

type btcPlannerStorageReader struct {
	sv                                              vm.StateView
	gets, batches, batchKeys, scans, withdrawalGets int
}

func (r *btcPlannerStorageReader) Get(key string) ([]byte, bool, error) {
	r.gets++
	if strings.HasPrefix(key, keys.KeyFrostWithdraw("btc-real-")) {
		r.withdrawalGets++
	}
	return r.sv.Get(key)
}
func (r *btcPlannerStorageReader) GetMany(k []string) (map[string][]byte, error) {
	r.batches++
	r.batchKeys += len(k)
	return r.sv.(interface {
		GetMany([]string) (map[string][]byte, error)
	}).GetMany(k)
}
func (r *btcPlannerStorageReader) Scan(prefix string, fn func(string, []byte) bool) error {
	r.scans++
	return fmt.Errorf("unexpected unbounded scan: %s", prefix)
}
func (r *btcPlannerStorageReader) ScanOrderedAfter(prefix, cursor string, limit int, reverse bool, fn func(string, []byte) bool) error {
	r.scans++
	rows, err := r.sv.(interface {
		ScanWithLimitOrderedAfter(string, string, int, bool) ([]vm.KVPair, error)
	}).ScanWithLimitOrderedAfter(prefix, cursor, limit, reverse)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if !fn(row.Key, row.Value) {
			break
		}
	}
	return nil
}

                                                                           
                                                                            
func TestBTCBatchPlannerRealStateViewReadBounds(t *testing.T) {
	for _, size := range []int{255, 256, 257, 5000, 20000} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			mgr, err := db.NewManager(t.TempDir(), logs.NewNodeLogger("btc-batch-perf", 0))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { mgr.Close() })
			mgr.InitWriteQueue(1024, time.Hour)
			var ops []interface{}
			put := func(key string, raw []byte) {
				if keys.IsStatefulKey(key) {
					ops = append(ops, &vm.WriteOp{Key: key, Value: raw})
				} else {
					mgr.EnqueueSet(key, string(raw))
				}
			}
			marshal := func(msg proto.Message) []byte {
				raw, err := proto.Marshal(msg)
				if err != nil {
					t.Fatal(err)
				}
				return raw
			}
			const address = "tb1pdpd76eytdcdwnm62uuxve403srq0dtcls0uj488wjjy4z4lfm25sdwatf0"
			_, operator := formalRollbackBTCKey(23)
			_, guardian := externalBTCGuardian()
			script, _, _, err := utils.BuildDualCustodyTaproot(operator, guardian)
			if err != nil {
				t.Fatal(err)
			}
			put(keys.KeyFrostVaultConfig("btc", 0), marshal(&pb.FrostVaultConfig{Chain: "btc", VaultCount: 1, BtcFeeSatsPerVbyte: 25, GuardianPubkey: guardian}))
			put(keys.KeyFrostVaultState("btc", 0), marshal(&pb.FrostVaultState{Chain: "btc", Status: "ACTIVE", KeyEpoch: 1, ActiveSinceHeight: 10, GroupPubkey: operator}))
			put(keys.KeyFrostVaultAvailableBalance("btc", "BTC", 0), []byte("1000000000"))
			put(keys.KeyFrostWithdrawFIFOHead("btc", "BTC"), []byte("1"))
			put(keys.KeyFrostWithdrawFIFOSeq("btc", "BTC"), []byte(strconv.Itoa(size)))
			put(keys.KeyLatestHeight(), []byte("37"))
			for i := 1; i <= size; i++ {
				id := fmt.Sprintf("btc-real-%d", i)
				put(keys.KeyFrostWithdraw(id), marshal(&pb.FrostWithdrawState{WithdrawId: id, Chain: "btc", Asset: "BTC", Seq: uint64(i), RequestHeight: 37, Amount: "100000", To: address, Status: "QUEUED"}))
				put(keys.KeyFrostWithdrawFIFOIndex("btc", "BTC", uint64(i)), []byte(id))
			}
			                                                                    
			                                                                     
			for i := 1; i <= 2; i++ {
				u := &pb.FrostUtxo{Txid: fmt.Sprintf("%064x", i), Amount: "1000000000", ScriptPubkey: script, FinalizeHeight: 10}
				put(keys.KeyFrostBtcSpendableUtxoIndex(0, uint64(i)), marshal(u))
				put(keys.KeyFrostBtcUtxo(0, u.Txid, 0), marshal(u))
			}
			if _, err := mgr.SyncStateUpdates(1, ops); err != nil {
				t.Fatal(err)
			}
			if err := mgr.ForceFlush(); err != nil {
				t.Fatal(err)
			}
			sv := vm.NewStateViewWithLimitScanOrderedAndBatch(
				mgr.Get,
				mgr.GetKVs, mgr.Scan, mgr.ScanKVWithLimit, mgr.ScanKVWithLimitReverse, mgr.ScanKVWithLimitOrdered, mgr.ScanKVWithLimitOrderedAfter,
			)
			sv.Del(keys.KeyFrostBtcSpendableUtxoIndex(0, 1))
			                                                                         
			sv.Set(keys.KeyFrostWithdraw("btc-real-1"), marshal(&pb.FrostWithdrawState{WithdrawId: "btc-real-1", Chain: "btc", Asset: "BTC", Seq: 1, RequestHeight: 37, Amount: "120000", To: address, Status: "QUEUED"}))
			for i := 0; i < size; i++ {
				sv.Del(fmt.Sprintf("unrelated-tombstone/%08d", i))
			}
			r := &btcPlannerStorageReader{sv: sv}
			factory := chainpkg.NewDefaultAdapterFactory()
			factory.RegisterAdapter(btc.NewBTCAdapter("testnet"))
			planner := planning.NewJobWindowPlanner(r, factory, 1)
			start := time.Now()
			jobs, planningLogs, err := planner.PlanJobWindow("btc", "BTC")
			elapsed := time.Since(start)
			if err != nil {
				t.Fatal(err)
			}
			if size == 255 {
				if len(jobs) != 0 || r.scans != 0 {
					t.Fatal("immature partial batch touched UTXO scan or submitted")
				}
			} else {
				if len(jobs) != 1 || len(jobs[0].WithdrawIDs) != 256 {
					t.Fatalf("jobs=%+v logs=%+v", jobs, planningLogs)
				}
				template, err := btc.FromJSON(jobs[0].TemplateData)
				if err != nil {
					t.Fatal(err)
				}
				if len(template.Outputs) != 257 || len(template.Inputs) != 1 || template.Inputs[0].TxID != fmt.Sprintf("%064x", 2) {
					t.Fatal("template lost recipient/change or used tombstoned input")
				}
				var outputs uint64
				for _, output := range template.Outputs[:256] {
					outputs += output.Amount
				}
				fee := template.Inputs[0].Amount - outputs - template.Outputs[256].Amount
				if outputs+fee != 256*100000+20000 {
					t.Fatal("overlay amount was not used")
				}
			}
			if r.batchKeys > 515 || r.batches != 2+func() int {
				if size >= 256 {
					return 1
				}
				return 0
			}() || r.gets > 16 || r.withdrawalGets != 1 || r.scans > 1 {
				t.Fatalf("read amplification: Get=%d GetMany=%d keys=%d withdrawalGet=%d scans=%d", r.gets, r.batches, r.batchKeys, r.withdrawalGets, r.scans)
			}
			t.Logf("backlog=%d tombstones=%d Get=%d GetMany=%d keys=%d scans=%d planning=%s", size, size+1, r.gets, r.batches, r.batchKeys, r.scans, elapsed)
		})
	}
}
