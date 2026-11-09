                   
package statedb

type Config struct {
	Backend          string                                 
	DataDir          string                       
	ShardHexWidth    int                                      
	PageSize         int
	TableCacheShards int
	BlockCacheSize   int64
	CheckpointKeep   int
	CheckpointEvery  uint64                                      
	ReadOnly         bool
}

type KVUpdate struct {
	Key                   string
	Value                 []byte
	Deleted               bool
	OldValue              []byte
	OldExists             bool
	HasOldValue           bool
	OldValueAuthoritative bool
}

type Page struct {
	Items         []KVUpdate
	Page          int
	TotalPages    int
	NextPageToken string
}

type ShardInfo struct {
	Shard string
	Count int64
}

                                                                      
                                                               
type ShardFingerprint struct {
	Shard           string
	KeyCount        int64
	TotalKeyBytes   int64
	TotalValueBytes int64
	XorHash         uint64
	XorDigest       [StateRootSize]byte
}
