package statedb

import (
	"bytes"
	"dex/keys"
	"dex/logs"
	"encoding/binary"
	"time"
)

                                                                                  
func (s *DB) GetLiveFingerprint() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return ParseFingerprintFromBytes(s.getLiveFingerprintBytesLocked())
}

func (s *DB) getLiveAccumulatorLocked() StateRootAccumulator {
	var acc StateRootAccumulator
	for _, fp := range s.liveFingerprints {
		if fp == nil {
			continue
		}
		acc.KeyCount += fp.KeyCount
		acc.TotalKeyBytes += fp.TotalKeyBytes
		acc.TotalValueBytes += fp.TotalValueBytes
		xorStateDigest(&acc.XorDigest, fp.XorDigest)
	}
	return acc
}

func (s *DB) getLiveFingerprintBytesLocked() []byte {
	return s.getLiveAccumulatorLocked().RootBytes()
}

                                                                             
func (s *DB) GetLiveShardFingerprints() []ShardFingerprint {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ShardFingerprint, 0, len(s.liveFingerprints))
	for _, fp := range s.liveFingerprints {
		out = append(out, *fp)
	}
	return out
}

                                                                                       
func (s *DB) rebuildLiveFingerprints() error {
	startAt := time.Now()
	totalKeys := int64(0)
	for _, shard := range s.shards {
		fp := s.liveFingerprints[shard]
		if fp == nil {
			fp = &ShardFingerprint{Shard: shard}
			s.liveFingerprints[shard] = fp
		}
		*fp = ShardFingerprint{Shard: shard}         

		prefix := kStatePrefix(shard)
		prefixStr := string(prefix)
		err := s.store.View(func(tx kvReader) error {
			return tx.IteratePrefix(prefix, nil, func(key []byte, value []byte) error {
				k := string(key)
				bizKey := k
				if len(k) > len(prefixStr) {
					bizKey = k[len(prefixStr):]
				}
				fp.KeyCount++
				fp.TotalKeyBytes += int64(len(bizKey))
				fp.TotalValueBytes += int64(len(value))
				leaf := HashStateKVLeaf(bizKey, value)
				xorStateDigest(&fp.XorDigest, leaf)
				fp.XorHash ^= digestLogID(leaf[:])
				return nil
			})
		})
		if err != nil {
			return err
		}
		totalKeys += fp.KeyCount
	}
	logs.Info("[StateDB] rebuildLiveFingerprints: %d keys across %d shards in %s", totalKeys, len(s.shards), time.Since(startAt))
	return nil
}

                                                                                        
func (s *DB) updateFingerprintForPut(bizKey string, oldVal, newVal []byte) {
	sh := shardOf(bizKey, s.conf.ShardHexWidth)
	fp := s.liveFingerprints[sh]
	if fp == nil {
		return
	}
	if oldVal != nil {
		                     
		oldLeaf := HashStateKVLeaf(bizKey, oldVal)
		xorStateDigest(&fp.XorDigest, oldLeaf)
		fp.XorHash ^= digestLogID(oldLeaf[:])
		fp.TotalValueBytes -= int64(len(oldVal))
	} else {
		          
		fp.KeyCount++
		fp.TotalKeyBytes += int64(len(bizKey))
	}
	                     
	newLeaf := HashStateKVLeaf(bizKey, newVal)
	xorStateDigest(&fp.XorDigest, newLeaf)
	fp.XorHash ^= digestLogID(newLeaf[:])
	fp.TotalValueBytes += int64(len(newVal))
}

                                                                      
func (s *DB) updateFingerprintForDelete(bizKey string, oldVal []byte) {
	if oldVal == nil {
		return                                
	}
	sh := shardOf(bizKey, s.conf.ShardHexWidth)
	fp := s.liveFingerprints[sh]
	if fp == nil {
		return
	}
	oldLeaf := HashStateKVLeaf(bizKey, oldVal)
	xorStateDigest(&fp.XorDigest, oldLeaf)
	fp.XorHash ^= digestLogID(oldLeaf[:])
	fp.KeyCount--
	fp.TotalKeyBytes -= int64(len(bizKey))
	fp.TotalValueBytes -= int64(len(oldVal))
}

                                                                        
func (s *DB) GetLiveFingerprintBytes() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.getLiveFingerprintBytesLocked()
}

                                                                                                      
func ParseFingerprintFromBytes(b []byte) uint64 {
	if len(b) < 8 {
		return 0
	}
	return binary.BigEndian.Uint64(b)
}

                                                                             
func IsZeroFingerprintBytes(b []byte) bool {
	return isAllZeroBytes(b)
}

                                                                    
func FingerprintToBytes(fp uint64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, fp)
	return buf
}

                                                               
                                                                                   
type FingerprintProvider interface {
	GetStateFingerprint() []byte
}

                                                                                                            
func ReadFingerprintFromProvider(p FingerprintProvider) ([]byte, uint64) {
	if p == nil {
		return nil, 0
	}
	raw := p.GetStateFingerprint()
	return raw, ParseFingerprintFromBytes(raw)
}

                                                                                               
func CompareFingerprintBytes(left, right []byte) (uint64, uint64, bool) {
	leftFP := ParseFingerprintFromBytes(left)
	rightFP := ParseFingerprintFromBytes(right)
	if len(left) >= StateRootSize || len(right) >= StateRootSize {
		return leftFP, rightFP, bytes.Equal(left, right)
	}
	return leftFP, rightFP, leftFP == rightFP
}

                                                                
func isStatefulKeyForFP(key string) bool {
	return keys.IsStatefulKey(key)
}
