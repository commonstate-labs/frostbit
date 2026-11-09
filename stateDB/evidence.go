package statedb

import (
	"bufio"
	"dex/keys"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

                                                                                     
                                                                                                 
                                                                  

                                                                                                  
                                                                     
                                       
func (s *DB) DumpCertEvidence(height uint64) (string, error) {
	return s.dumpStateEvidenceTagged(height, fmt.Sprintf("h_%020d.txt", height))
}

                                                                                                    
                                                                                                           
func (s *DB) DumpMismatchEvidence(height uint64, blockHash string) (string, error) {
	bh := blockHash
	if len(bh) > 16 {
		bh = bh[:16]
	}
	return s.dumpStateEvidenceTagged(height, fmt.Sprintf("mismatch_h_%020d_%s.txt", height, bh))
}

func (s *DB) dumpStateEvidenceTagged(height uint64, fileName string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	evidenceDir := filepath.Join(filepath.Dir(s.checkpointsDir), "evidence")
	if err := os.MkdirAll(evidenceDir, 0o755); err != nil {
		return "", err
	}
	outPath := filepath.Join(evidenceDir, fileName)

	specs := keys.StateDBBusinessKeySpecs()
	prefixList := make([]string, 0, len(specs))
	for _, sp := range specs {
		if sp.Prefix != "" {
			prefixList = append(prefixList, sp.Prefix)
		}
	}
	                                                                     
	sort.Slice(prefixList, func(i, j int) bool { return len(prefixList[i]) > len(prefixList[j]) })

	type prefixRoll struct {
		keys  int64
		bytes int64
		xor   uint64
	}
	type keyRow struct {
		shard  string
		bizKey string
		vLen   int
		xx     uint64
	}
	prefixRolls := make(map[string]*prefixRoll, len(prefixList))
	shardRolls := make(map[string]*ShardFingerprint, len(s.shards))
	for _, sh := range s.shards {
		shardRolls[sh] = &ShardFingerprint{Shard: sh}
	}
	unknownRoll := &prefixRoll{}
	var keyRows []keyRow
	var totalKeys int64
	var totalBytes int64
	var aggXor uint64

	err := s.store.View(func(tx kvReader) error {
		for _, shard := range s.shards {
			prefix := kStatePrefix(shard)
			prefixStr := string(prefix)
			sh := shardRolls[shard]
			err := tx.IteratePrefix(prefix, nil, func(key []byte, value []byte) error {
				k := string(key)
				bizKey := k
				if len(k) > len(prefixStr) {
					bizKey = k[len(prefixStr):]
				}
				xx := xxHashKV(bizKey, value)
				vLen := len(value)
				bkLen := int64(len(bizKey))
				sh.KeyCount++
				sh.TotalKeyBytes += bkLen
				sh.TotalValueBytes += int64(vLen)
				sh.XorHash ^= xx
				totalKeys++
				totalBytes += int64(vLen)
				aggXor ^= xx

				matched := false
				for _, p := range prefixList {
					if len(bizKey) >= len(p) && bizKey[:len(p)] == p {
						roll := prefixRolls[p]
						if roll == nil {
							roll = &prefixRoll{}
							prefixRolls[p] = roll
						}
						roll.keys++
						roll.bytes += int64(vLen)
						roll.xor ^= xx
						matched = true
						break
					}
				}
				if !matched {
					unknownRoll.keys++
					unknownRoll.bytes += int64(vLen)
					unknownRoll.xor ^= xx
				}
				keyRows = append(keyRows, keyRow{shard: shard, bizKey: bizKey, vLen: vLen, xx: xx})
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}

	sort.Slice(keyRows, func(i, j int) bool { return keyRows[i].bizKey < keyRows[j].bizKey })

	f, err := os.Create(outPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	bw := bufio.NewWriter(f)
	defer bw.Flush()

	                                                                                                         
	var memKeys int64
	var memXor uint64
	for _, fp := range s.liveFingerprints {
		memKeys += fp.KeyCount
		memXor ^= fp.XorHash
	}
	fmt.Fprintf(bw, "# height=%d ts=%d disk_keys=%d disk_bytes=%d disk_xor=%016x mem_keys=%d mem_xor=%016x drift=%v\n",
		height, time.Now().UnixNano(), totalKeys, totalBytes, aggXor, memKeys, memXor, memKeys != totalKeys || memXor != aggXor)
	sortedPrefixes := make([]string, 0, len(prefixRolls))
	for p := range prefixRolls {
		sortedPrefixes = append(sortedPrefixes, p)
	}
	sort.Strings(sortedPrefixes)
	for _, p := range sortedPrefixes {
		r := prefixRolls[p]
		fmt.Fprintf(bw, "P prefix=%s keys=%d bytes=%d xor=%016x\n", p, r.keys, r.bytes, r.xor)
	}
	if unknownRoll.keys > 0 {
		fmt.Fprintf(bw, "P prefix=<unknown> keys=%d bytes=%d xor=%016x\n", unknownRoll.keys, unknownRoll.bytes, unknownRoll.xor)
	}
	shardIDs := make([]string, 0, len(shardRolls))
	for sh := range shardRolls {
		shardIDs = append(shardIDs, sh)
	}
	sort.Strings(shardIDs)
	for _, sh := range shardIDs {
		r := shardRolls[sh]
		mem := s.liveFingerprints[sh]
		memCount, memXX := int64(0), uint64(0)
		if mem != nil {
			memCount, memXX = mem.KeyCount, mem.XorHash
		}
		drift := memCount != r.KeyCount || memXX != r.XorHash
		fmt.Fprintf(bw, "S shard=%s disk_keys=%d disk_xor=%016x mem_keys=%d mem_xor=%016x drift=%v\n",
			r.Shard, r.KeyCount, r.XorHash, memCount, memXX, drift)
	}
	for _, kr := range keyRows {
		fmt.Fprintf(bw, "K shard=%s key=%s len=%d xx=%016x\n", kr.shard, kr.bizKey, kr.vLen, kr.xx)
	}
	return outPath, nil
}
