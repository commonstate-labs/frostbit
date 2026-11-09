                              
                              

package vm

import (
	"dex/keys"
	"dex/pb"
	"dex/utils"
	"sort"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"google.golang.org/protobuf/proto"
)

func canonicalGenesisAddressesForTest(t *testing.T, n int) []string {
	t.Helper()
	addresses := make([]string, n)
	for i := range addresses {
		var seed [32]byte
		seed[30], seed[31] = byte((i+9)/256), byte(i+9)
		key, _ := btcec.PrivKeyFromBytes(seed[:])
		var err error
		addresses[i], err = utils.AddressFromSecp256k1PubKey(key.PubKey().SerializeCompressed())
		key.Zero()
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(addresses)
	return addresses
}

                                                  
func TestInitVaultStates(t *testing.T) {
	sv := NewMockStateView()
	chain := "btc"
	epochID := uint64(1)

	                        
	vaultCfg := &pb.FrostVaultConfig{
		Chain:          chain,
		VaultCount:     1,
		CommitteeSize:  4,
		ThresholdRatio: 0.75,
		SignAlgo:       pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
	}
	vaultCfgKey := keys.KeyFrostVaultConfig(chain, 0)
	vaultCfgData, _ := proto.Marshal(vaultCfg)
	sv.Set(vaultCfgKey, vaultCfgData)

	                                                                                 
	topK := testGenesisFrostSnapshot(canonicalGenesisAddressesForTest(t, 4))
	activeMinersKey := keys.KeyFrostActiveMiners()
	topKData, _ := proto.Marshal(topK)
	sv.Set(activeMinersKey, topKData)

	                            
	err := InitVaultStates(sv, chain, epochID)
	if err != nil {
		t.Fatalf("InitVaultStates failed: %v", err)
	}

	                                              
	for vaultID := uint32(0); vaultID < 1; vaultID++ {
		vaultStateKey := keys.KeyFrostVaultState(chain, vaultID)
		vaultStateData, exists, err := sv.Get(vaultStateKey)
		if err != nil {
			t.Fatalf("failed to get vault state: %v", err)
		}
		if !exists {
			t.Fatalf("vault state not found for vault %d", vaultID)
		}

		var vaultState pb.FrostVaultState
		if err := proto.Unmarshal(vaultStateData, &vaultState); err != nil {
			t.Fatalf("failed to unmarshal vault state: %v", err)
		}

		                     
		if vaultState.VaultId != vaultID {
			t.Errorf("vault ID mismatch: expected %d, got %d", vaultID, vaultState.VaultId)
		}
		if vaultState.Chain != chain {
			t.Errorf("chain mismatch: expected %s, got %s", chain, vaultState.Chain)
		}
		if vaultState.KeyEpoch != epochID {
			t.Errorf("epoch mismatch: expected %d, got %d", epochID, vaultState.KeyEpoch)
		}
		if vaultState.Status != "PENDING" {
			t.Errorf("status mismatch: expected PENDING, got %s", vaultState.Status)
		}
		if len(vaultState.CommitteeMembers) != 4 || vaultState.CommitteeThreshold != 3 {
			t.Errorf("committee mismatch: expected 3/4, got %d/%d", vaultState.CommitteeThreshold, len(vaultState.CommitteeMembers))
		}
	}

	                                                      
	err = InitVaultStates(sv, chain, epochID)
	if err != nil {
		t.Fatalf("InitVaultStates (second call) failed: %v", err)
	}
}

                                                               
func TestInitVaultStatesMissingConfig(t *testing.T) {
	sv := NewMockStateView()
	chain := "btc"
	epochID := uint64(1)

	                                                       
	err := InitVaultStates(sv, chain, epochID)
	if err == nil {
		t.Fatal("expected error when VaultConfig is missing")
	}
}

                                                             
func TestInitVaultStatesMissingTopK(t *testing.T) {
	sv := NewMockStateView()
	chain := "btc"
	epochID := uint64(1)

	                                         
	vaultCfg := &pb.FrostVaultConfig{
		Chain:          chain,
		VaultCount:     1,
		CommitteeSize:  4,
		ThresholdRatio: 0.75,
		SignAlgo:       pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
	}
	vaultCfgKey := keys.KeyFrostVaultConfig(chain, 0)
	vaultCfgData, _ := proto.Marshal(vaultCfg)
	sv.Set(vaultCfgKey, vaultCfgData)

	                         
	err := InitVaultStates(sv, chain, epochID)
	if err == nil {
		t.Fatal("expected error when TopK is missing")
	}
}
