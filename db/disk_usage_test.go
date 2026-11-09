package db

import (
	"testing"

	"dex/utils/diskspace"
)

func TestDiskPressurePoliciesReuseProvidedUsage(t *testing.T) {
	manager := &Manager{}
	usage := diskspace.Snapshot{
		Path:        "/definitely-missing/shared-disk-snapshot",
		TotalBytes:  1_000,
		FreeBytes:   750,
		FreePercent: 75,
	}

	blockResult, err := manager.PruneBlockDataWithDiskUsage(1_000, usage)
	if err != nil {
		t.Fatalf("block pressure policy: %v", err)
	}
	if blockResult.DiskPath != usage.Path || blockResult.DiskTotalBytes != usage.TotalBytes || blockResult.DiskFreeBytes != usage.FreeBytes || blockResult.DiskFreePct != usage.FreePercent {
		t.Fatalf("block pressure result did not preserve shared usage: %+v", blockResult)
	}

	queryResult, err := manager.PruneQueryDataWithDiskUsage(1_000, usage)
	if err != nil {
		t.Fatalf("query pressure policy: %v", err)
	}
	if queryResult.DiskPath != usage.Path || queryResult.DiskTotalBytes != usage.TotalBytes || queryResult.DiskFreeBytes != usage.FreeBytes || queryResult.DiskFreePct != usage.FreePercent {
		t.Fatalf("query pressure result did not preserve shared usage: %+v", queryResult)
	}
}

func TestDiskPressurePoliciesRejectZeroTotalUsage(t *testing.T) {
	manager := &Manager{}
	usage := diskspace.Snapshot{Path: "."}
	if _, err := manager.PruneBlockDataWithDiskUsage(1, usage); err == nil {
		t.Fatal("block pressure policy accepted zero total bytes")
	}
	if _, err := manager.PruneQueryDataWithDiskUsage(1, usage); err == nil {
		t.Fatal("query pressure policy accepted zero total bytes")
	}
}
