package statedb

import (
	"encoding/binary"
	"fmt"
)

func (s *DB) LatestCheckpointHeight() (uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.store == nil {
		return 0, fmt.Errorf("stateDB store is not available")
	}

	var h uint64
	err := s.store.View(func(tx kvReader) error {
		v, err := tx.Get(kCpLatest())
		if err != nil {
			if s.store.IsNotFound(err) {
				return nil
			}
			return err
		}
		if len(v) < 8 {
			return fmt.Errorf("invalid latest checkpoint value")
		}
		h = binary.BigEndian.Uint64(v)
		return nil
	})
	return h, err
}

func (s *DB) ComputeShardFingerprints(E uint64) ([]ShardFingerprint, error) {
	var (
		store kvStore
		err   error
		close func() error
	)
	if E == 0 {
		store = s.store
		close = func() error { return nil }
	} else {
		store, err = s.openCheckpointStore(E)
		if err != nil {
			return nil, err
		}
		close = store.Close
	}
	defer func() { _ = close() }()

	out := make([]ShardFingerprint, 0, len(s.shards))
	for _, shard := range s.shards {
		prefix := kStatePrefix(shard)
		prefixStr := string(prefix)
		fp := ShardFingerprint{Shard: shard}
		err := store.View(func(tx kvReader) error {
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
			return nil, err
		}
		out = append(out, fp)
	}
	return out, nil
}
