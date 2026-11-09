package consensus

import (
	"dex/blocktrace"
	"time"
)

type BlockTimingSnapshot struct {
	Height                 uint64                    `json:"height"`
	BlockID                string                    `json:"block_id"`
	TxCount                int                       `json:"tx_count"`
	RecordedAtUnixMs       int64                     `json:"recorded_at_unix_ms"`
	TotalMs                int64                     `json:"total_ms"`
	ConsensusMs            int64                     `json:"consensus_ms"`
	PrepareMs              int64                     `json:"prepare_ms"`
	LoadBlockMs            int64                     `json:"load_block_ms"`
	VMCommitWaitMs         int64                     `json:"vm_commit_wait_ms"`
	VMCommitMs             int64                     `json:"vm_commit_ms"`
	VMBuildSpecMs          int64                     `json:"vm_build_spec_ms,omitempty"`
	VMApplyAndFlushMs      int64                     `json:"vm_apply_and_flush_ms,omitempty"`
	PoolRemoveMs           int64                     `json:"pool_remove_ms"`
	MaterializeBlockMs     int64                     `json:"materialize_block_ms"`
	SaveBlockMs            int64                     `json:"save_block_ms"`
	FinalizeFlushMs        int64                     `json:"finalize_flush_ms"`
	UpdateFinalizedStateMs int64                     `json:"update_finalized_state_ms"`
	CleanupMs              int64                     `json:"cleanup_ms"`
	CacheHit               bool                      `json:"cache_hit"`
	LoadedFromDB           bool                      `json:"loaded_from_db"`
	Error                  string                    `json:"error,omitempty"`
	Steps                  []blocktrace.StepSnapshot `json:"steps,omitempty"`
}

func durationMs(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	return d.Milliseconds()
}

func finalizationOverheadMs(total, vmCommit time.Duration) int64 {
	overhead := total - vmCommit
	if overhead <= 0 {
		return 0
	}
	return overhead.Milliseconds()
}
