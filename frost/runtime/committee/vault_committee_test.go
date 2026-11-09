                                        
                              

package committee

import (
	"testing"
)

func createTestTopK(n int) []uint64 {
	list := make([]uint64, n)
	for i := 0; i < n; i++ {
		list[i] = uint64(i)
	}
	return list
}

func TestPermute_Deterministic(t *testing.T) {
	list := createTestTopK(100)
	seed := ComputeSeed(1, "btc")

	                                    
	result1 := Permute(list, seed)
	result2 := Permute(list, seed)

	if len(result1) != len(result2) {
		t.Error("Results should have same length")
	}

	for i := range result1 {
		if result1[i] != result2[i] {
			t.Errorf("Results differ at index %d: %d vs %d", i, result1[i], result2[i])
		}
	}
}

func TestPermute_DifferentSeed(t *testing.T) {
	list := createTestTopK(100)
	seed1 := ComputeSeed(1, "btc")
	seed2 := ComputeSeed(2, "btc")

	result1 := Permute(list, seed1)
	result2 := Permute(list, seed2)

	                                    
	sameCount := 0
	for i := range result1 {
		if result1[i] == result2[i] {
			sameCount++
		}
	}

	                                          
	if sameCount > 10 {
		t.Errorf("Too many same positions: %d/100", sameCount)
	}
}

func TestAssignToVaults(t *testing.T) {
	topK := createTestTopK(100)
	seed := ComputeSeed(1, "btc")
	vaultCount := 5
	committeeSize := 20

	vaults := AssignToVaults(topK, seed, vaultCount, committeeSize)

	                      
	if len(vaults) != vaultCount {
		t.Errorf("Expected %d vaults, got %d", vaultCount, len(vaults))
	}

	                                        
	for i, v := range vaults {
		if len(v) != committeeSize {
			t.Errorf("Vault %d: expected %d members, got %d", i, committeeSize, len(v))
		}
	}

	                           
	seen := make(map[uint64]int)
	for vaultIdx, vault := range vaults {
		for _, member := range vault {
			if prevVault, exists := seen[member]; exists {
				t.Errorf("Member %d appears in both vault %d and %d", member, prevVault, vaultIdx)
			}
			seen[member] = vaultIdx
		}
	}
}

func TestAssignToVaults_LargeScale(t *testing.T) {
	                                                         
	topK := createTestTopK(10000)
	seed := ComputeSeed(100, "btc")
	vaultCount := 50
	committeeSize := 200

	vaults := AssignToVaults(topK, seed, vaultCount, committeeSize)

	if len(vaults) != vaultCount {
		t.Errorf("Expected %d vaults, got %d", vaultCount, len(vaults))
	}

	totalMembers := 0
	for _, v := range vaults {
		totalMembers += len(v)
	}

	                                                
	if totalMembers != 10000 {
		t.Errorf("Expected 10000 total members, got %d", totalMembers)
	}
}

func TestGetVaultCommittee(t *testing.T) {
	topK := createTestTopK(1000)
	epochID := uint64(42)
	chain := "eth"
	vaultCount := 10
	committeeSize := 100

	                              
	committee0 := GetVaultCommittee(topK, epochID, chain, 0, vaultCount, committeeSize)
	if len(committee0) != committeeSize {
		t.Errorf("Expected committee size %d, got %d", committeeSize, len(committee0))
	}

	                              
	committee5 := GetVaultCommittee(topK, epochID, chain, 5, vaultCount, committeeSize)
	if len(committee5) != committeeSize {
		t.Errorf("Expected committee size %d, got %d", committeeSize, len(committee5))
	}

	                                        
	overlap := 0
	committee5Map := make(map[uint64]bool)
	for _, m := range committee5 {
		committee5Map[m] = true
	}
	for _, m := range committee0 {
		if committee5Map[m] {
			overlap++
		}
	}

	if overlap > 0 {
		t.Errorf("Vaults should not share members, but found %d overlaps", overlap)
	}
}

func TestSortByBitIndex(t *testing.T) {
	unsorted := []uint64{5, 2, 8, 1, 9, 3}
	sorted := SortByBitIndex(unsorted)

	expected := []uint64{1, 2, 3, 5, 8, 9}
	for i, v := range sorted {
		if v != expected[i] {
			t.Errorf("Index %d: expected %d, got %d", i, expected[i], v)
		}
	}
}
