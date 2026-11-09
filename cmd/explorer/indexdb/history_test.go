package indexdb

import (
	"strings"
	"testing"
)

func TestHistoryMetadataLifecycle(t *testing.T) {
	idb, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer idb.Close()

	assertHistoryEmpty(t, idb, true)
	if err := idb.SetSyncNode("https://node.example"); err != nil {
		t.Fatal(err)
	}
	if err := idb.SetSyncHeight(0); err != nil {
		t.Fatal(err)
	}
	assertHistoryEmpty(t, idb, true)

	if err := idb.SetPartialHistoryBase(101, "source prefix pruned"); err != nil {
		t.Fatal(err)
	}
	metadata, err := idb.GetHistoryMetadata()
	if err != nil {
		t.Fatal(err)
	}
	if metadata == nil || metadata.Version != HistoryMetadataVersion || metadata.BaseHeight != 101 || !metadata.Partial || metadata.Reason != "source prefix pruned" {
		t.Fatalf("unexpected metadata: %+v", metadata)
	}
	assertHistoryEmpty(t, idb, true)

	if err := idb.Set(KeyTxDetail("orphan"), `{}`); err != nil {
		t.Fatal(err)
	}
	assertHistoryEmpty(t, idb, false)

	if err := idb.Reset(); err != nil {
		t.Fatal(err)
	}
	metadata, err = idb.GetHistoryMetadata()
	if err != nil || metadata != nil {
		t.Fatalf("metadata after reset = %+v, err=%v", metadata, err)
	}
	assertHistoryEmpty(t, idb, true)
}

func TestSetPartialHistoryBaseRejectsFullHistoryHeight(t *testing.T) {
	idb, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer idb.Close()
	if err := idb.SetPartialHistoryBase(1, "invalid"); err == nil || !strings.Contains(err.Error(), "greater than 1") {
		t.Fatalf("error = %v", err)
	}
}

func assertHistoryEmpty(t *testing.T, idb *IndexDB, want bool) {
	t.Helper()
	got, err := idb.IsHistoryEmpty()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("IsHistoryEmpty() = %t, want %t", got, want)
	}
}
