package vm

import (
	"fmt"
	"math/big"
	"strconv"
	"testing"
	"time"

	"dex/db"
	chainpkg "dex/frost/chain"
	"dex/frost/chain/evm"
	"dex/frost/core/curve"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"google.golang.org/protobuf/proto"
)

                                                                             
                                                                               
                                                                          
func TestFormalMultigenerationRealStateViewReadBounds(t *testing.T) {
	var baselineAllocs float64
	for _, history := range []int{7, 8, 9, 5000, 20000} {
		t.Run(strconv.Itoa(history), func(t *testing.T) {
			manager, err := db.NewManager(t.TempDir(), logs.NewNodeLogger("handover-v2-perf", 0))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { manager.Close() })
			manager.InitWriteQueue(1024, time.Hour)
			var writes []interface{}
			epoch := uint64(history + 2)
			group := curve.NewBN256Group()
			oldPub := group.SerializePoint(group.ScalarBaseMult(big.NewInt(17)))
			newPub := group.SerializePoint(group.ScalarBaseMult(big.NewInt(18)))
			cfg := &pb.FrostVaultConfig{Chain: "eth", EvmChainId: 1, DepositDexChainId: 51025,
				AuthorityProtocol: 3, GuardianPubkey: accountGuardianTestKey(t), VaultCount: 1, VaultRefs: []string{"0x1111111111111111111111111111111111111111"}}
			built, _, err := evm.BuildMigrationTemplateV2(evm.MigrationV2Params{
				Chain: "eth", ChainID: 1, DexChainID: 51025, ContractAddr: cfg.VaultRefs[0],
				GuardianPubkey: cfg.GuardianPubkey, OldEpoch: epoch - 1, NewEpoch: epoch, NewGroupPubkey: newPub, FrozenNextBatchSeq: "1",
			})
			if err != nil {
				t.Fatal(err)
			}
			marker := &pb.FrostVaultMigrationState{
				Chain: "eth", VaultId: 0, OldEpoch: epoch - 1, NewEpoch: epoch,
				OldGroupPubkey: oldPub, NewGroupPubkey: newPub,
				MigrationId: chainpkg.MigrationID("eth", 0, epoch), Status: migrationStatusSigned,
				IntentHash: built.SigHashes[0], TemplateHash: built.TemplateHash,
				AuthorityProtocol: 3, FrozenNextBatchSeq: "1",
			}
			markerBytes, err := proto.Marshal(marker)
			if err != nil {
				t.Fatal(err)
			}
			proof := append(signAccountThresholdTest(t, big.NewInt(17), marker.IntentHash), signAccountThresholdTest(t, big.NewInt(29), marker.IntentHash)...)
			for i := 2; i < history+2; i++ {
				writes = append(writes, &WriteOp{Key: keys.KeyFrostVaultMigration("eth", 0, uint64(i)), Value: []byte("retained-history")})
				writes = append(writes, &WriteOp{Key: keys.KeyFrostVaultHandoverProof("eth", 0, uint64(i)), Value: []byte("retained-proof")})
			}
			markerKey := keys.KeyFrostVaultMigration("eth", 0, epoch)
			proofKey := keys.KeyFrostVaultHandoverProof("eth", 0, epoch)
			writes = append(writes, &WriteOp{Key: markerKey, Value: markerBytes}, &WriteOp{Key: proofKey, Value: proof})
			if _, err := manager.SyncStateUpdates(1, writes); err != nil {
				t.Fatal(err)
			}
			if err := manager.ForceFlush(); err != nil {
				t.Fatal(err)
			}
			totalGets, totalScans, totalBaseGets := 0, 0, 0
			var gateElapsed time.Duration
			var sample StateView
			for node := 0; node < 20; node++ {
				baseGets := 0
				sv := NewStateViewWithLimitScanOrderedAndBatch(
					func(key string) ([]byte, error) { baseGets++; return manager.Get(key) },
					manager.GetKVs, manager.Scan, manager.ScanKVWithLimit, manager.ScanKVWithLimitReverse,
					manager.ScanKVWithLimitOrdered, manager.ScanKVWithLimitOrderedAfter,
				)
				                                                                 
				for i := 2; i < history+2; i += 2 {
					sv.Del(keys.KeyFrostVaultMigration("eth", 0, uint64(i)))
				}
				counted := &formalMultigenerationReadCounter{StateView: sv}
				beforeBase := baseGets
				gateStart := time.Now()
				ready, err := accountEpochAuthorityReady(counted, "eth", 0, epoch, cfg)
				gateElapsed += time.Since(gateStart)
				if err != nil || !ready {
					t.Fatalf("node%d gate: ready=%v err=%v", node, ready, err)
				}
				baseReads := baseGets - beforeBase
				if counted.gets != 2 || counted.scans != 0 || baseReads != 2 {
					t.Fatalf("history=%d node=%d Get=%d Scan=%d actualStateDBPointReads=%d want 2/0/2", history, node, counted.gets, counted.scans, baseReads)
				}
				totalGets += counted.gets
				totalScans += counted.scans
				totalBaseGets += baseReads
				if node == 0 {
					sample = sv
				}
			}

			                                                                     
			                                                                       
			allocs := testing.AllocsPerRun(5, func() {
				ready, err := accountEpochAuthorityReady(sample, "eth", 0, epoch, cfg)
				if err != nil || !ready {
					panic(fmt.Sprintf("gate failed: %v", err))
				}
			})
			if history == 7 {
				baselineAllocs = allocs
			} else if allocs > baselineAllocs+32 {
				t.Fatalf("allocation count grew with history: baseline=%g current=%g", baselineAllocs, allocs)
			}
			                                                                    
			                                                                    
			sample.Set(markerKey, markerBytes)
			sample.Del(proofKey)
			if ready, err := accountEpochAuthorityReady(sample, "eth", 0, epoch, cfg); err != nil || ready {
				t.Fatalf("tombstoned proof accepted: ready=%v err=%v", ready, err)
			}
			sample.Set(proofKey, proof)
			if ready, err := accountEpochAuthorityReady(sample, "eth", 0, epoch, cfg); err != nil || !ready {
				t.Fatalf("overlay proof not restored: ready=%v err=%v", ready, err)
			}
			t.Logf("history=%d tombstones/view=%d local20 Get=%d Scan=%d StateDBPointReads=%d warmGateAllocs=%.0f gateOnly=%s", history, (history+1)/2, totalGets, totalScans, totalBaseGets, allocs, gateElapsed)
		})
	}
}
