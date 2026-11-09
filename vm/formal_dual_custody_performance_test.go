package vm

import (
	"bytes"
	"fmt"
	"strconv"
	"testing"
	"time"

	"dex/frost/chain/btc"
	"dex/keys"
	"dex/pb"
)

func TestFormalDualCustodyBTCInputIOBounds(t *testing.T) {
	                                                                         
	                                                                           
	                                                                           
	                                                                         
	for _, count := range []int{1, 31, 32, 33, 256} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			shards := [4]map[string][]byte{}
			for i := range shards {
				shards[i] = map[string][]byte{}
			}
			shard := func(key string) int {
				h := 0
				for _, b := range []byte(key) {
					h = (h*31 + int(b)) & 0x7fffffff
				}
				return h % 4
			}
			put := func(key string, value []byte) { shards[shard(key)][key] = bytes.Clone(value) }
			const vaultID = uint32(3)
			const epoch = uint64(7)
			put(keys.KeyFrostVaultConfig("btc", 0), mustBTCValidationMarshal(t, &pb.FrostVaultConfig{Chain: "btc", VaultCount: 1,
				AuthorityProtocol: 3, GuardianPubkey: btcDualTestGuardianPubkey(), BtcNetwork: "mainnet", BtcFeeSatsPerVbyte: 25}))
			put(keys.KeyFrostVaultState("btc", vaultID), mustBTCValidationMarshal(t, &pb.FrostVaultState{Chain: "btc", VaultId: vaultID,
				KeyEpoch: epoch, GroupPubkey: btcDualTestOperatorPubkey(), CommitteeMembers: []string{"a", "b", "c", "d"}, CommitteeThreshold: 3}))
			spk := btcDualTestScript(t)
			template := &btc.BTCTemplate{Version: 2, VaultID: vaultID, KeyEpoch: epoch}
			for i := 0; i < count; i++ {
				txID := fmt.Sprintf("%064x", i+1)
				template.Inputs = append(template.Inputs, btc.TxInput{TxID: txID, Vout: 0, Amount: 10000, Sequence: btc.DefaultSequence})
				put(keys.KeyFrostBtcUtxo(vaultID, txID, 0), mustBTCValidationMarshal(t, &pb.FrostUtxo{Txid: txID, Vout: 0, VaultId: vaultID, Amount: "10000", ScriptPubkey: spk}))
				put(keys.KeyFrostBtcLockedUtxo(vaultID, txID, 0), []byte("formal-job"))
			}
			btcDualDecorateTemplate(t, template)
			for i := 0; i < 5000; i++ {
				put(fmt.Sprintf("formal-busy-block/%08d", i), []byte{byte(i)})
			}
			points, batches, readKeys, scans := 0, 0, 0, 0
			read := func(key string) ([]byte, error) { points++; return bytes.Clone(shards[shard(key)][key]), nil }
			batch := func(wanted []string) (map[string][]byte, error) {
				batches++
				readKeys += len(wanted)
				out := map[string][]byte{}
				for _, key := range wanted {
					if value, ok := shards[shard(key)][key]; ok {
						out[key] = bytes.Clone(value)
					}
				}
				return out, nil
			}
			scan := func(string) (map[string][]byte, error) {
				scans++
				return nil, fmt.Errorf("local-input validation must never scan")
			}
			sv := NewStateViewWithLimitScanOrderedAndBatch(read, batch, scan, nil, nil, nil, nil)
			for i := 0; i < 5000; i += 2 {
				sv.Del(fmt.Sprintf("formal-busy-block/%08d", i))
			}
			started := time.Now()
			_, inputs, _, _, total, err := validateBTCLocalInputs(sv, template, vaultID, epoch, "formal-job")
			elapsed := time.Since(started)
			if err != nil {
				t.Fatal(err)
			}
			if len(inputs) != count || total != uint64(count)*10000 {
				t.Fatal("workload was skipped")
			}
			if points != 2 || batches != 1 || readKeys != 2*count || scans != 0 {
				t.Fatalf("I=%d points=%d batches=%d batchKeys=%d scans=%d; want 2/1/2I/0", count, points, batches, readKeys, scans)
			}
			t.Logf("inputs=%d unrelated=5000 overlayTombstones=2500 baselinePointShape=%d newPoints=%d batchCalls=%d batchKeys=%d scans=%d elapsed=%s", count, 2*count+1, points, batches, readKeys, scans, elapsed)
			if count == 256 {
				local20Start := time.Now()
				for node := 1; node < 20; node++ {
					replica := NewStateViewWithLimitScanOrderedAndBatch(read, batch, scan, nil, nil, nil, nil)
					for i := 0; i < 5000; i += 2 {
						replica.Del(fmt.Sprintf("formal-busy-block/%08d", i))
					}
					beforePoints, beforeBatches, beforeKeys := points, batches, readKeys
					if _, rows, _, _, amount, err := validateBTCLocalInputs(replica, template, vaultID, epoch, "formal-job"); err != nil || len(rows) != count || amount != uint64(count)*10000 {
						t.Fatalf("local20 node%d skipped work: %v", node, err)
					}
					if points-beforePoints != 2 || batches-beforeBatches != 1 || readKeys-beforeKeys != 2*count || scans != 0 {
						t.Fatal("local20 IO grew with unrelated overlay")
					}
				}
				if points != 40 || batches != 20 || readKeys != 40*count || scans != 0 {
					t.Fatal("local20 aggregate IO bound failed")
				}
				t.Logf("local20 inputs/view=%d points=%d batches=%d keys=%d scans=%d elapsed19=%s", count, points, batches, readKeys, scans, time.Since(local20Start))
			}
			                                                                    
			                                             
			sv.Del(keys.KeyFrostBtcUtxo(vaultID, template.Inputs[count-1].TxID, 0))
			if _, _, _, _, _, err := validateBTCLocalInputs(sv, template, vaultID, epoch, "formal-job"); err == nil {
				t.Fatal("deleted input resurrected from base")
			}
		})
	}
}
