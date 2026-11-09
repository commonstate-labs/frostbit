package vm

import (
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"dex/config"
	"dex/db"
	"dex/frost/chain"
	"dex/interfaces"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"dex/protocol"
	"google.golang.org/protobuf/proto"
)

                                                                              
                                                                     
func TestBTCIsolationRealStateViewIndexPageBounds(t *testing.T) {
	sizes := []int{1023, 1024, 1025}
	if raw := os.Getenv("DEX_BTC_SCALE"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 10000 || n > 1000000 {
			t.Fatal("DEX_BTC_SCALE must be in 10000..1000000")
		}
		sizes = append(sizes, n)
	}
	for _, n := range sizes {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.Database.StateDB.ShardHexWidth = 1
			cfg.Database.StateDB.CheckpointEvery = 1000000000
			manager, err := db.NewManagerWithConfig(t.TempDir(), logs.NewNodeLogger("btc-index-scale", 0), cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { manager.Close() })
			manager.InitWriteQueue(4096, time.Hour)
			marshal := func(m proto.Message) []byte {
				raw, e := proto.Marshal(m)
				if e != nil {
					t.Fatal(e)
				}
				return raw
			}
			vaultCfg := testVaultCommitteePolicyConfig("btc", 4)
			vaultCfg.DepositDexChainId = 51025
			vaultCfg.BtcNetwork = "mainnet"
			vaultCfg.BtcFeeSatsPerVbyte = 25
			vaultCfg.GuardianPubkey = btcDualTestGuardianPubkey()
			inputScript := btcDualTestScript(t)
			_, newGroup := migrationTestKey(t, 41)
			ops := []interface{}{
				&WriteOp{Key: keys.KeyProtocolVersion(), Value: []byte("3")},
				&WriteOp{Key: keys.KeyProtocolUpgradeReceipt(3), Value: []byte(protocol.UpgradeReceiptValue(protocol.Upgrade{Version: 3, ActivationHeight: 2, ID: protocol.UpgradeBTCIsolationV3}))},
				&WriteOp{Key: keys.KeyFrostVaultConfig("btc", 0), Value: marshal(vaultCfg)},
				&WriteOp{Key: keys.KeyFrostVaultState("btc", 0), Value: marshal(&pb.FrostVaultState{Chain: "btc", VaultId: 0, KeyEpoch: 1,
					GroupPubkey: btcDualTestOperatorPubkey(), CommitteeMembers: []string{"old-member"}, Status: VaultStatusActive,
					SignAlgo: pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340})},
			}
			flush := func() {
				if _, e := manager.SyncStateUpdates(2, ops); e != nil {
					t.Fatal(e)
				}
				ops = ops[:0]
			}
			seedStart := time.Now()
			for i := 1; i <= n; i++ {
				txid := fmt.Sprintf("%064x", i)
				utxo := &pb.FrostUtxo{Txid: txid, Vout: 0, VaultId: 0, Amount: "100000", RequestId: fmt.Sprint(i), ScriptPubkey: inputScript}
				ops = append(ops, &WriteOp{Key: keys.KeyFrostBtcUtxo(0, txid, 0), Value: marshal(utxo)}, &WriteOp{Key: keys.KeyFrostBtcUtxoSeqRef(0, txid, 0), Value: []byte(strconv.Itoa(i))})
				if len(ops) >= 8192 {
					flush()
				}
			}
			                                                                      
			                                                            
			for i := 1; i <= min(n, 10000); i++ {
				ops = append(ops, &WriteOp{Key: keys.KeyFrostBtcUtxoFIFOIndex(0, uint64(i)), Value: []byte("aux")})
				if len(ops) >= 8192 {
					flush()
				}
			}
			if len(ops) > 0 {
				flush()
			}
			if e := manager.ForceFlush(); e != nil {
				t.Fatal(e)
			}
			t.Logf("seed N=%d primary+seqref=%d auxiliary=%d elapsed=%s", n, 2*n, min(n, 10000), time.Since(seedStart))
			starts := []int{0}
			if n > 2048 {
				starts = append(starts, n/2, n-512)
			}
			for _, highWrite := range []bool{false, true} {
				for _, start := range starts {
					t.Run(fmt.Sprintf("%d/highwrite=%v", start, highWrite), func(t *testing.T) {
						gets, batches, batchKeys, scans, scanRows := 0, 0, 0, 0, 0
						var baseCursors []string
						sv := NewStateViewWithLimitScanOrderedAndBatch(
							func(k string) ([]byte, error) { gets++; return manager.Get(k) },
							func(ks []string) (map[string][]byte, error) {
								batches++
								batchKeys += len(ks)
								return manager.GetKVs(ks)
							},
							func(string) (map[string][]byte, error) { t.Fatal("unbounded scan"); return nil, nil }, nil, nil,
							func(p string, l int, r bool) ([]interfaces.KVPair, error) {
								scans++
								rows, e := manager.ScanKVWithLimitOrdered(p, l, r)
								scanRows += len(rows)
								return rows, e
							},
							func(p, a string, l int, r bool) ([]interfaces.KVPair, error) {
								scans++
								baseCursors = append(baseCursors, a)
								rows, e := manager.ScanKVWithLimitOrderedAfter(p, a, l, r)
								scanRows += len(rows)
								return rows, e
							})
						sv.(interface{ BindNetworkChainID(uint64) }).BindNetworkChainID(51025)
						cursor := keys.KeyFrostBtcUtxoAllPrefix()
						if start > 0 {
							cursor = keys.KeyFrostBtcUtxo(0, fmt.Sprintf("%064x", start), 0)
						}
						sv.Set(keys.KeyFrostBtcUtxoAmountBuildState(), marshal(&pb.FrostBtcIsolationIndexState{Cursor: cursor, JobsDone: true}))
						                                                                  
						deleted := 0
						if highWrite {
							for i := 0; i < 20000; i++ {
								key := keys.KeyBalance(fmt.Sprintf("scale-%d", i), "FB")
								if i%2 == 0 {
									sv.Del(key)
								} else {
									sv.Set(key, []byte("overlay"))
								}
							}
							for i := start + 17; i <= min(n, start+1024); i += 17 {
								sv.Del(keys.KeyFrostBtcUtxo(0, fmt.Sprintf("%064x", i), 0))
								deleted++
							}
						}
						releaseStateViewHistory(sv)
						began := time.Now()
						if e := processBTCIsolationAmountIndex(sv); e != nil {
							t.Fatal(e)
						}
						elapsed := time.Since(began)
						                                                             
						                                                            
						if scans > 2 || scanRows > 3072 || batches > 1 || batchKeys > 2048 || gets > 8 {
							t.Fatalf("unbounded page IO: gets=%d batches=%d keys=%d scans=%d rows=%d", gets, batches, batchKeys, scans, scanRows)
						}
						measuredScans, measuredRows := scans, scanRows
						for i := 1; i < len(baseCursors); i++ {
							if baseCursors[i] <= baseCursors[i-1] {
								t.Fatal("base page repeated or reversed its cursor")
							}
						}
						var state pb.FrostBtcIsolationIndexState
						raw, _, _ := sv.Get(keys.KeyFrostBtcUtxoAmountBuildState())
						if e := proto.Unmarshal(raw, &state); e != nil {
							t.Fatal(e)
						}
						if state.Cursor <= cursor {
							t.Fatal("cursor failed to advance")
						}
						if n-start-deleted < 1024 && !state.Done {
							t.Fatal("tail did not stop at auxiliary boundary")
						}
						if n-start-deleted >= 1024 && state.Done {
							t.Fatal("full page silently truncated work")
						}
						indexes, e := scanStateViewOrderedAfter(sv, keys.KeyFrostBtcUtxoAmountPrefix(0), "", 1025, false)
						if e != nil {
							t.Fatal(e)
						}
						want := min(1024, n-start-deleted)
						if len(indexes) != want {
							t.Fatalf("indexed %d want %d", len(indexes), want)
						}
						t.Logf("N=%d start=%d tombstones=%d highWrite=%v page=%d Get=%d batches=%d batchKeys=%d scans=%d scannedRows=%d elapsed=%s", n, start, deleted, highWrite, want, gets, batches, batchKeys, measuredScans, measuredRows, elapsed)
					})
				}
			}
			for _, highWrite := range []bool{false, true} {
				for _, start := range starts {
					t.Run(fmt.Sprintf("migration/%d/highwrite=%v", start, highWrite), func(t *testing.T) {
						sv, io := newBTCMigrationPerformanceView(t, manager)
						transition := &pb.VaultTransitionState{Chain: "btc", VaultId: 0, EpochId: 2,
							DkgStatus: DKGStatusKeyReady, Lifecycle: VaultLifecycleDraining, SignAlgo: pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
							OldGroupPubkey: btcDualTestOperatorPubkey(), NewGroupPubkey: newGroup,
							OldCommitteeMembers: []string{"old-member"}, NewCommitteeMembers: []string{"new-member"}, DkgThresholdT: 1}
						transitionKey := keys.KeyFrostVaultTransition("btc", 0, 2)
						sv.Set(transitionKey, marshal(transition))
						parent, err := newBTCIsolationMigration(sv, transition, 50)
						if err != nil {
							t.Fatal(err)
						}
						                                                                 
						                                                                  
						                                                                  
						if start > 0 {
							parent.BtcBuildCursor = keys.KeyFrostBtcUtxo(0, fmt.Sprintf("%064x", start), 0)
							parent.BtcChildCount = uint64(start)
							parent.BtcTotalInputAmount = strconv.FormatUint(uint64(start)*100000, 10)
							parent.BtcTotalOutputAmount = strconv.FormatUint(uint64(start)*90000, 10)
							parent.BtcTotalFeeAmount = strconv.FormatUint(uint64(start)*10000, 10)
							parent.BtcManifestAccumulator = chain.BTCMigrationManifestAccumulate(nil, []byte("previous performance pages"))
						}
						deleted := 0
						if highWrite {
							for i := 0; i < 20000; i++ {
								key := keys.KeyBalance(fmt.Sprintf("migration-scale-%d", i), "FB")
								if i%2 == 0 {
									sv.Del(key)
								} else {
									sv.Set(key, []byte("overlay"))
								}
							}
							for i := start + 17; i <= min(n, start+1024); i += 17 {
								sv.Del(keys.KeyFrostBtcUtxo(0, fmt.Sprintf("%064x", i), 0))
								deleted++
							}
						}
						releaseStateViewHistory(sv)
						*io = btcMigrationPerformanceIO{}
						began := time.Now()
						if err := advanceBTCIsolationMigrationManifest(sv, transitionKey, transition, parent); err != nil {
							t.Fatal(err)
						}
						elapsed := time.Since(began)
						want := min(1024, n-start-deleted)
						if parent.BtcChildCount != uint64(start+want) {
							t.Fatalf("manifest added %d children, want %d", parent.BtcChildCount-uint64(start), want)
						}
						if io.scans > 2 || io.scanRows > 3072 || io.batches != 1 || io.batchKeys > 2048 || io.gets > 8 {
							t.Fatalf("unbounded migration page: gets=%d batches=%d keys=%d scans=%d rows=%d", io.gets, io.batches, io.batchKeys, io.scans, io.scanRows)
						}
						for i := 1; i < len(io.cursors); i++ {
							if io.cursors[i] <= io.cursors[i-1] {
								t.Fatal("migration base scan restarted at the prefix head")
							}
						}
						if n-start-deleted < 1024 && parent.Status != migrationStatusIntentFinalized {
							t.Fatal("migration tail was not sealed")
						}
						if n-start-deleted >= 1024 && parent.Status != migrationStatusBuildingManifest {
							t.Fatal("full migration page silently truncated work")
						}
						t.Logf("migration N=%d start=%d tombstones=%d highWrite=%v child=%d Get=%d batches=%d batchKeys=%d scans=%d scannedRows=%d elapsed=%s", n, start, deleted, highWrite, want, io.gets, io.batches, io.batchKeys, io.scans, io.scanRows, elapsed)
					})
				}
			}
		})
	}
}

type btcMigrationPerformanceIO struct {
	gets, batches, batchKeys, scans, scanRows int
	cursors                                   []string
}

func newBTCMigrationPerformanceView(t *testing.T, manager *db.Manager) (StateView, *btcMigrationPerformanceIO) {
	t.Helper()
	io := new(btcMigrationPerformanceIO)
	sv := NewStateViewWithLimitScanOrderedAndBatch(
		func(k string) ([]byte, error) { io.gets++; return manager.Get(k) },
		func(ks []string) (map[string][]byte, error) {
			io.batches++
			io.batchKeys += len(ks)
			return manager.GetKVs(ks)
		},
		func(string) (map[string][]byte, error) { t.Fatal("unbounded migration scan"); return nil, nil }, nil, nil,
		func(p string, l int, r bool) ([]interfaces.KVPair, error) {
			io.scans++
			rows, err := manager.ScanKVWithLimitOrdered(p, l, r)
			io.scanRows += len(rows)
			return rows, err
		},
		func(p, a string, l int, r bool) ([]interfaces.KVPair, error) {
			io.scans++
			io.cursors = append(io.cursors, a)
			rows, err := manager.ScanKVWithLimitOrderedAfter(p, a, l, r)
			io.scanRows += len(rows)
			return rows, err
		})
	sv.(interface{ BindNetworkChainID(uint64) }).BindNetworkChainID(51025)
	return sv, io
}
