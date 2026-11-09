package diskspace

import (
	"math"
	"testing"
)

func TestUsageCurrentDirectory(t *testing.T) {
	snapshot, err := Usage("")
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if snapshot.Path != "." {
		t.Fatalf("Path = %q, want current directory", snapshot.Path)
	}
	if snapshot.TotalBytes == 0 {
		t.Fatal("TotalBytes = 0")
	}
	want := float64(snapshot.FreeBytes) * 100 / float64(snapshot.TotalBytes)
	if math.Abs(snapshot.FreePercent-want) > 1e-12 {
		t.Fatalf("FreePercent = %v, want %v", snapshot.FreePercent, want)
	}
}

func TestUsageMissingPath(t *testing.T) {
	missing := t.TempDir() + "/missing"
	snapshot, err := Usage(missing)
	if err == nil {
		t.Fatal("Usage missing path succeeded")
	}
	if snapshot.Path != missing {
		t.Fatalf("Path = %q, want %q", snapshot.Path, missing)
	}
}
