package types

type StateSnapshotShard struct {
	Shard           string `json:"shard"`
	Count           int64  `json:"count"`
	TotalKeyBytes   int64  `json:"total_key_bytes,omitempty"`
	TotalValueBytes int64  `json:"total_value_bytes,omitempty"`
	XorHash         uint64 `json:"xor_hash,omitempty"`
	XorDigest       []byte `json:"xor_digest,omitempty"`
	FirstKey        string `json:"first_key,omitempty"`
	LastKey         string `json:"last_key,omitempty"`
}

type StateSnapshotShardsRequest struct {
	TargetHeight uint64 `json:"target_height"`
}

type StateSnapshotShardsResponse struct {
	SnapshotHeight uint64               `json:"snapshot_height"`
	Shards         []StateSnapshotShard `json:"shards"`
	Error          string               `json:"error,omitempty"`
}

type StateCheckpointFilesRequest struct {
	TargetHeight uint64 `json:"target_height"`
}

type StateCheckpointFile struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Chunks int64  `json:"chunks,omitempty"`
}

type StateCheckpointFilesResponse struct {
	SnapshotHeight uint64                `json:"snapshot_height"`
	ChunkSize      int64                 `json:"chunk_size"`
	TotalBytes     int64                 `json:"total_bytes"`
	Files          []StateCheckpointFile `json:"files"`
	Error          string                `json:"error,omitempty"`
}

type StateCheckpointChunkRequest struct {
	TargetHeight uint64 `json:"target_height"`
	Name         string `json:"name"`
	Offset       int64  `json:"offset"`
	Limit        int64  `json:"limit"`
}
