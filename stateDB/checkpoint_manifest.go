package statedb

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

const checkpointManifestVersion = 1

type checkpointShardManifest struct {
	Shard           string `json:"shard"`
	KeyCount        int64  `json:"key_count"`
	TotalKeyBytes   int64  `json:"total_key_bytes"`
	TotalValueBytes int64  `json:"total_value_bytes"`
	XorHash         uint64 `json:"xor_hash"`
	XorDigest       []byte `json:"xor_digest"`
}

type checkpointManifest struct {
	Version         int                       `json:"version"`
	Height          uint64                    `json:"height"`
	StateRoot       []byte                    `json:"state_root"`
	KeyCount        int64                     `json:"key_count"`
	TotalKeyBytes   int64                     `json:"total_key_bytes"`
	TotalValueBytes int64                     `json:"total_value_bytes"`
	Shards          []checkpointShardManifest `json:"shards"`
	CreatedUnixNano int64                     `json:"created_unix_nano"`
}

func (s *DB) checkpointManifestPath(height uint64) string {
	return s.checkpointPath(height) + ".manifest.json"
}

func (s *DB) buildCheckpointManifestLocked(height uint64, createdUnixNano int64) checkpointManifest {
	return buildCheckpointManifestFromFingerprints(height, createdUnixNano, s.liveFingerprints)
}

func buildCheckpointManifestFromFingerprints(height uint64, createdUnixNano int64, fingerprints map[string]*ShardFingerprint) checkpointManifest {
	var acc StateRootAccumulator
	m := checkpointManifest{
		Version:         checkpointManifestVersion,
		Height:          height,
		CreatedUnixNano: createdUnixNano,
		Shards:          make([]checkpointShardManifest, 0, len(fingerprints)),
	}
	for _, fp := range fingerprints {
		if fp == nil {
			continue
		}
		acc.KeyCount += fp.KeyCount
		acc.TotalKeyBytes += fp.TotalKeyBytes
		acc.TotalValueBytes += fp.TotalValueBytes
		xorStateDigest(&acc.XorDigest, fp.XorDigest)
		m.Shards = append(m.Shards, checkpointShardManifest{
			Shard:           fp.Shard,
			KeyCount:        fp.KeyCount,
			TotalKeyBytes:   fp.TotalKeyBytes,
			TotalValueBytes: fp.TotalValueBytes,
			XorHash:         fp.XorHash,
			XorDigest:       append([]byte(nil), fp.XorDigest[:]...),
		})
	}
	m.StateRoot = acc.RootBytes()
	m.KeyCount = acc.KeyCount
	m.TotalKeyBytes = acc.TotalKeyBytes
	m.TotalValueBytes = acc.TotalValueBytes
	sort.Slice(m.Shards, func(i, j int) bool { return m.Shards[i].Shard < m.Shards[j].Shard })
	return m
}

func (s *DB) buildCheckpointManifestFromStore(height uint64, createdUnixNano int64, store kvStore) (checkpointManifest, error) {
	fingerprints := make(map[string]*ShardFingerprint, len(s.shards))
	for _, shard := range s.shards {
		fp := &ShardFingerprint{Shard: shard}
		fingerprints[shard] = fp
		prefix := kStatePrefix(shard)
		prefixStr := string(prefix)
		err := store.View(func(tx kvReader) error {
			return tx.IteratePrefix(prefix, nil, func(key []byte, value []byte) error {
				k := string(key)
				bizKey := k
				if len(k) > len(prefixStr) {
					bizKey = k[len(prefixStr):]
				}
				leaf := HashStateKVLeaf(bizKey, value)
				fp.KeyCount++
				fp.TotalKeyBytes += int64(len(bizKey))
				fp.TotalValueBytes += int64(len(value))
				fp.XorHash ^= digestLogID(leaf[:])
				xorStateDigest(&fp.XorDigest, leaf)
				return nil
			})
		})
		if err != nil {
			return checkpointManifest{}, err
		}
	}
	return buildCheckpointManifestFromFingerprints(height, createdUnixNano, fingerprints), nil
}

func (s *DB) writeCheckpointManifestLocked(height uint64, createdUnixNano int64) error {
	path := s.checkpointManifestPath(height)
	data, err := json.MarshalIndent(s.buildCheckpointManifestLocked(height, createdUnixNano), "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	_ = os.Remove(path)
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (s *DB) readCheckpointManifest(height uint64) (checkpointManifest, error) {
	return readCheckpointManifestFile(s.checkpointManifestPath(height))
}

func readCheckpointManifestFile(path string) (checkpointManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return checkpointManifest{}, err
	}
	var got checkpointManifest
	if err := json.Unmarshal(data, &got); err != nil {
		return checkpointManifest{}, err
	}
	return got, nil
}

func (s *DB) LoadCheckpointShardMetadata(height uint64) ([]ShardInfo, []ShardFingerprint, error) {
	if height == 0 {
		return nil, nil, fmt.Errorf("checkpoint manifest height must be > 0")
	}
	got, err := s.readCheckpointManifest(height)
	if err != nil {
		return nil, nil, err
	}
	if got.Version != checkpointManifestVersion {
		return nil, nil, fmt.Errorf("manifest version mismatch: got=%d want=%d", got.Version, checkpointManifestVersion)
	}
	if got.Height != height {
		return nil, nil, fmt.Errorf("manifest height mismatch: got=%d want=%d", got.Height, height)
	}
	if len(got.StateRoot) != StateRootSize {
		return nil, nil, fmt.Errorf("manifest state root length mismatch: got=%d want=%d", len(got.StateRoot), StateRootSize)
	}

	shards := make([]ShardInfo, 0, len(got.Shards))
	fingerprints := make([]ShardFingerprint, 0, len(got.Shards))
	seen := make(map[string]struct{}, len(got.Shards))
	var acc StateRootAccumulator
	for _, sh := range got.Shards {
		if sh.Shard == "" {
			return nil, nil, fmt.Errorf("manifest contains empty shard")
		}
		if _, ok := seen[sh.Shard]; ok {
			return nil, nil, fmt.Errorf("manifest contains duplicate shard %q", sh.Shard)
		}
		seen[sh.Shard] = struct{}{}
		if sh.KeyCount < 0 || sh.TotalKeyBytes < 0 || sh.TotalValueBytes < 0 {
			return nil, nil, fmt.Errorf("manifest shard %q has negative metadata", sh.Shard)
		}
		if len(sh.XorDigest) != StateRootSize {
			return nil, nil, fmt.Errorf("manifest shard %q xor digest length mismatch: got=%d want=%d", sh.Shard, len(sh.XorDigest), StateRootSize)
		}
		var digest [StateRootSize]byte
		copy(digest[:], sh.XorDigest)
		acc.KeyCount += sh.KeyCount
		acc.TotalKeyBytes += sh.TotalKeyBytes
		acc.TotalValueBytes += sh.TotalValueBytes
		xorStateDigest(&acc.XorDigest, digest)
		shards = append(shards, ShardInfo{Shard: sh.Shard, Count: sh.KeyCount})
		fingerprints = append(fingerprints, ShardFingerprint{
			Shard:           sh.Shard,
			KeyCount:        sh.KeyCount,
			TotalKeyBytes:   sh.TotalKeyBytes,
			TotalValueBytes: sh.TotalValueBytes,
			XorHash:         sh.XorHash,
			XorDigest:       digest,
		})
	}
	if got.KeyCount != acc.KeyCount || got.TotalKeyBytes != acc.TotalKeyBytes || got.TotalValueBytes != acc.TotalValueBytes {
		return nil, nil, fmt.Errorf("manifest aggregate metadata mismatch")
	}
	if !bytes.Equal(got.StateRoot, acc.RootBytes()) {
		return nil, nil, fmt.Errorf("manifest state root does not match shard metadata")
	}
	sort.Slice(shards, func(i, j int) bool { return shards[i].Shard < shards[j].Shard })
	sort.Slice(fingerprints, func(i, j int) bool { return fingerprints[i].Shard < fingerprints[j].Shard })
	return shards, fingerprints, nil
}

func compareCheckpointManifest(got, want checkpointManifest) error {
	if got.Version != want.Version {
		return fmt.Errorf("manifest version mismatch: got=%d want=%d", got.Version, want.Version)
	}
	if got.Height != want.Height {
		return fmt.Errorf("manifest height mismatch: got=%d want=%d", got.Height, want.Height)
	}
	if !bytes.Equal(got.StateRoot, want.StateRoot) {
		return fmt.Errorf("manifest state root mismatch")
	}
	if got.KeyCount != want.KeyCount || got.TotalKeyBytes != want.TotalKeyBytes || got.TotalValueBytes != want.TotalValueBytes {
		return fmt.Errorf("manifest aggregate metadata mismatch")
	}
	if len(got.Shards) != len(want.Shards) {
		return fmt.Errorf("manifest shard count mismatch: got=%d want=%d", len(got.Shards), len(want.Shards))
	}
	sort.Slice(got.Shards, func(i, j int) bool { return got.Shards[i].Shard < got.Shards[j].Shard })
	for i := range want.Shards {
		g, w := got.Shards[i], want.Shards[i]
		if g.Shard != w.Shard || g.KeyCount != w.KeyCount || g.TotalKeyBytes != w.TotalKeyBytes || g.TotalValueBytes != w.TotalValueBytes || g.XorHash != w.XorHash || !bytes.Equal(g.XorDigest, w.XorDigest) {
			return fmt.Errorf("manifest shard %q mismatch", w.Shard)
		}
	}
	return nil
}

func (s *DB) validateCheckpointManifestFastLocked(height uint64, compareLive bool) error {
	got, err := s.readCheckpointManifest(height)
	if err != nil {
		return err
	}
	if got.Version != checkpointManifestVersion {
		return fmt.Errorf("manifest version mismatch: got=%d want=%d", got.Version, checkpointManifestVersion)
	}
	if got.Height != height {
		return fmt.Errorf("manifest height mismatch: got=%d want=%d", got.Height, height)
	}
	if len(got.StateRoot) != StateRootSize {
		return fmt.Errorf("manifest state root length mismatch: got=%d want=%d", len(got.StateRoot), StateRootSize)
	}
	if !compareLive {
		return nil
	}
	return compareCheckpointManifest(got, s.buildCheckpointManifestLocked(height, got.CreatedUnixNano))
}

func (s *DB) validateCheckpointManifestLocked(height uint64) error {
	got, err := s.readCheckpointManifest(height)
	if err != nil {
		return err
	}
	store, err := s.openCheckpointStore(height)
	if err != nil {
		return err
	}
	defer store.Close()
	want, err := s.buildCheckpointManifestFromStore(height, got.CreatedUnixNano, store)
	if err != nil {
		return err
	}
	return compareCheckpointManifest(got, want)
}

func (s *DB) removeCheckpointFiles(height uint64) error {
	if err := os.RemoveAll(s.checkpointPath(height)); err != nil {
		return err
	}
	if err := os.Remove(s.checkpointManifestPath(height)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
