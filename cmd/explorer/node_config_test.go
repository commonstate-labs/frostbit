package main

import (
	"testing"

	"dex/cmd/explorer/syncer"
)

func TestBuildNodesBoundsConfiguredNodeCount(t *testing.T) {
	nodes := buildNodes("", "127.0.0.1", 6000, explorerMaxConfiguredNodes+100)
	if len(nodes) != explorerMaxConfiguredNodes {
		t.Fatalf("node count = %d, want %d", len(nodes), explorerMaxConfiguredNodes)
	}
	if nodes := buildNodes("", "127.0.0.1", 6000, -1); len(nodes) != 0 {
		t.Fatalf("negative node count produced %d nodes", len(nodes))
	}
}

func TestExplorerResourceFlagsCannotDisableFiniteDefaults(t *testing.T) {
	for _, value := range []int{-10, 0} {
		if got := boundedExplorerSyncMaxBlocks(value); got != syncer.DefaultMaxBlocksPerSync {
			t.Fatalf("sync max for %d = %d, want default %d", value, got, syncer.DefaultMaxBlocksPerSync)
		}
	}
	if got := boundedExplorerSyncMaxBlocks(37); got != 37 {
		t.Fatalf("positive sync max changed to %d", got)
	}
	if got := boundedExplorerIndexRetentionHeights(0); got != syncer.DefaultIndexRetentionHeights {
		t.Fatalf("zero retention = %d, want default %d", got, syncer.DefaultIndexRetentionHeights)
	}
	if got := boundedExplorerIndexRetentionHeights(1234); got != 1234 {
		t.Fatalf("positive retention changed to %d", got)
	}
}
