package consensus

import (
	"fmt"

	statedb "dex/stateDB"
	"dex/types"
)

func validateSnapshotManifest(resp *types.StateSnapshotShardsResponse, expectedHeight uint64, certStateRoot []byte) error {
	if resp == nil {
		return fmt.Errorf("nil snapshot manifest")
	}
	if resp.Error != "" {
		return fmt.Errorf("snapshot manifest error: %s", resp.Error)
	}
	if resp.SnapshotHeight != expectedHeight {
		return fmt.Errorf("snapshot height mismatch: got=%d want=%d", resp.SnapshotHeight, expectedHeight)
	}
	root, err := snapshotManifestRoot(resp)
	if err != nil {
		return err
	}
	if len(certStateRoot) < statedb.StateRootSize {
		return fmt.Errorf("cert stateRoot unavailable: len=%d", len(certStateRoot))
	}
	if statedb.IsZeroFingerprintBytes(certStateRoot) {
		return fmt.Errorf("cert stateRoot is zero")
	}
	_, _, matched := statedb.CompareFingerprintBytes(root, certStateRoot)
	if !matched {
		return fmt.Errorf("snapshot root mismatch")
	}
	return nil
}

func snapshotManifestRoot(resp *types.StateSnapshotShardsResponse) ([]byte, error) {
	seen := make(map[string]struct{}, len(resp.Shards))
	var acc statedb.StateRootAccumulator
	for _, sh := range resp.Shards {
		if sh.Shard == "" {
			return nil, fmt.Errorf("manifest contains empty shard")
		}
		if _, ok := seen[sh.Shard]; ok {
			return nil, fmt.Errorf("manifest contains duplicate shard %q", sh.Shard)
		}
		seen[sh.Shard] = struct{}{}
		if sh.Count < 0 || sh.TotalKeyBytes < 0 || sh.TotalValueBytes < 0 {
			return nil, fmt.Errorf("manifest shard %q has negative metadata", sh.Shard)
		}
		if len(sh.XorDigest) != statedb.StateRootSize {
			return nil, fmt.Errorf("manifest shard %q xor digest length mismatch: got=%d want=%d", sh.Shard, len(sh.XorDigest), statedb.StateRootSize)
		}
		var digest [statedb.StateRootSize]byte
		copy(digest[:], sh.XorDigest)
		acc.KeyCount += sh.Count
		acc.TotalKeyBytes += sh.TotalKeyBytes
		acc.TotalValueBytes += sh.TotalValueBytes
		for i := 0; i < statedb.StateRootSize; i++ {
			acc.XorDigest[i] ^= digest[i]
		}
	}
	return acc.RootBytes(), nil
}
