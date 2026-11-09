package statedb

import "fmt"

func kState(shard, bizKey string) []byte {
	return []byte(fmt.Sprintf("v1_sdb_state_%s_%s", shard, bizKey))
}

func kStatePrefix(shard string) []byte {
	return []byte(fmt.Sprintf("v1_sdb_state_%s_", shard))
}

func kMetaLastAppliedHeight() []byte {
	return []byte("v1_sdb_meta_last_applied_height")
}

func kMetaAppliedBlock(height uint64) []byte {
	return []byte(fmt.Sprintf("v1_sdb_meta_applied_block_%020d", height))
}

func kMetaAppliedBlockPrefix() []byte {
	return []byte("v1_sdb_meta_applied_block_")
}

func kCpLatest() []byte {
	return []byte("v1_sdb_cp_latest")
}

func kCpIndex(height uint64) []byte {
	return []byte(fmt.Sprintf("v1_sdb_cp_h_%020d", height))
}
