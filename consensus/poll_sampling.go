package consensus

import (
	"crypto/sha256"
	"encoding/binary"
)

                                                                     
                                                
type WeightedSample[T comparable] struct {
	Value  T
	Weight int
}

                                                                           
                                                                             
                                                       
  
                                                                               
                                                                         
                                                                               
func BuildReplacementSample[T comparable](population []T, slots int, seed []byte) []WeightedSample[T] {
	if len(population) == 0 || slots <= 0 {
		return nil
	}

	result := make([]WeightedSample[T], 0, min(slots, len(population)))
	resultIndex := make(map[T]int, min(slots, len(population)))
	populationSize := uint64(len(population))
	                                                                         
	limit := ^uint64(0) - (^uint64(0) % populationSize)
	var counter uint64
	var counterBytes [8]byte

	for lenSlots := 0; lenSlots < slots; {
		binary.BigEndian.PutUint64(counterBytes[:], counter)
		counter++
		h := sha256.New()
		h.Write(seed)
		h.Write(counterBytes[:])
		digest := h.Sum(nil)
		draw := binary.BigEndian.Uint64(digest[:8])
		if draw >= limit {
			continue
		}

		value := population[draw%populationSize]
		if index, exists := resultIndex[value]; exists {
			result[index].Weight++
		} else {
			resultIndex[value] = len(result)
			result = append(result, WeightedSample[T]{Value: value, Weight: 1})
		}
		lenSlots++
	}
	return result
}
