package indexdb

import (
	"bytes"
	"runtime"
	"testing"
)

func TestIndexDBOptionsBoundBulkWriteResources(t *testing.T) {
	opts := newIndexDBOptions()
	if opts.MemTableSize != indexDBMemTableSize {
		t.Fatalf("memtable size = %d, want %d", opts.MemTableSize, indexDBMemTableSize)
	}
	if opts.MemTableStopWritesThreshold != indexDBMemTableStopWritesThreshold {
		t.Fatalf("memtable stop threshold = %d, want %d", opts.MemTableStopWritesThreshold, indexDBMemTableStopWritesThreshold)
	}
	if len(opts.Levels) != 1 || opts.Level(0).TargetFileSize != indexDBBaseTargetFileSize {
		t.Fatalf("base target file size = %d across %d configured levels, want %d across 1", opts.Level(0).TargetFileSize, len(opts.Levels), indexDBBaseTargetFileSize)
	}
	if got, want := opts.Level(1).TargetFileSize, int64(indexDBBaseTargetFileSize*2); got != want {
		t.Fatalf("next-level target file size = %d, want %d", got, want)
	}
	if opts.MaxConcurrentCompactions == nil {
		t.Fatal("max concurrent compactions callback is nil")
	}
	if got, want := opts.MaxConcurrentCompactions(), boundedIndexDBCompactions(runtime.GOMAXPROCS(0)); got != want {
		t.Fatalf("max concurrent compactions = %d, want %d", got, want)
	}

	validated := opts.Clone().EnsureDefaults()
	if err := validated.Validate(); err != nil {
		t.Fatalf("invalid Pebble options: %v", err)
	}
}

func TestBoundedIndexDBCompactions(t *testing.T) {
	for _, test := range []struct {
		procs int
		want  int
	}{
		{procs: 0, want: 1},
		{procs: 1, want: 1},
		{procs: 2, want: 2},
		{procs: 4, want: 4},
		{procs: 64, want: 4},
	} {
		if got := boundedIndexDBCompactions(test.procs); got != test.want {
			t.Errorf("boundedIndexDBCompactions(%d) = %d, want %d", test.procs, got, test.want)
		}
	}
}

func TestIndexBatchSetBytesPreservesBinaryValue(t *testing.T) {
	idb, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idb.Close() })

	want := []byte{0x00, 0xff, 0x80, 'r', 'm'}
	batch := idb.NewBatch()
	defer batch.Close()
	if err := batch.SetBytes("binary", want); err != nil {
		t.Fatal(err)
	}
	if err := batch.Commit(); err != nil {
		t.Fatal(err)
	}

	got, closer, err := idb.db.Get([]byte("binary"))
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Close()
	if !bytes.Equal(got, want) {
		t.Fatalf("binary value = %v, want %v", got, want)
	}
}
