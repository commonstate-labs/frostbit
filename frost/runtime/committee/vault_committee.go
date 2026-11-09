                                             
                                                                  

package committee

import (
	"crypto/sha256"
	"encoding/binary"
	"sort"
)

                                        

                                                                    
type VaultCommitteeAssigner struct{}

                                            
func NewVaultCommitteeAssigner() *VaultCommitteeAssigner {
	return &VaultCommitteeAssigner{}
}

                                          
                              
func ComputeSeed(epochID uint64, chain string) []byte {
	h := sha256.New()
	epochBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(epochBytes, epochID)
	h.Write(epochBytes)
	h.Write([]byte(chain))
	return h.Sum(nil)
}

                                                                
                                                    
                              
func Permute(list []uint64, seed []byte) []uint64 {
	if len(list) == 0 {
		return list
	}

	                                       
	result := make([]uint64, len(list))
	copy(result, list)

	                                     
	rng := newSeededRNG(seed)

	                      
	for i := len(result) - 1; i > 0; i-- {
		j := rng.Intn(i + 1)
		result[i], result[j] = result[j], result[i]
	}

	return result
}

                                                
            
                                                      
                                                   
                                   
                                                         
  
                                                                                               
func AssignToVaults(topK []uint64, seed []byte, vaultCount, committeeSize int) [][]uint64 {
	                  
	permuted := Permute(topK, seed)

	                  
	vaults := make([][]uint64, vaultCount)
	for j := 0; j < vaultCount; j++ {
		start := j * committeeSize
		end := start + committeeSize
		if start >= len(permuted) {
			vaults[j] = []uint64{}                      
			continue
		}
		if end > len(permuted) {
			end = len(permuted)
		}
		vaults[j] = make([]uint64, end-start)
		copy(vaults[j], permuted[start:end])
	}

	return vaults
}

                                                          
func GetVaultCommittee(topK []uint64, epochID uint64, chain string, vaultID uint32, vaultCount, committeeSize int) []uint64 {
	seed := ComputeSeed(epochID, chain)
	vaults := AssignToVaults(topK, seed, vaultCount, committeeSize)

	if int(vaultID) >= len(vaults) {
		return nil
	}
	return vaults[vaultID]
}

                                                  
func SortByBitIndex(indices []uint64) []uint64 {
	sorted := make([]uint64, len(indices))
	copy(sorted, indices)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i] < sorted[j]
	})
	return sorted
}

                                      

                                                       
type seededRNG struct {
	state []byte
	pos   int
}

func newSeededRNG(seed []byte) *seededRNG {
	return &seededRNG{
		state: seed,
		pos:   0,
	}
}

func (r *seededRNG) nextBytes(n int) []byte {
	result := make([]byte, n)
	for i := 0; i < n; i++ {
		if r.pos >= len(r.state) {
			                                       
			h := sha256.Sum256(r.state)
			r.state = h[:]
			r.pos = 0
		}
		result[i] = r.state[r.pos]
		r.pos++
	}
	return result
}

func (r *seededRNG) Intn(n int) int {
	if n <= 0 {
		return 0
	}
	                                 
	bytes := r.nextBytes(4)
	val := binary.BigEndian.Uint32(bytes)
	return int(val % uint32(n))
}
