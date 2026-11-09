package logs

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"dex/pb"
)

func TestNewRollingWriterBoundsTotalFileBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bounded.log")
	w, err := NewRollingWriter(path, 128)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if _, err := w.Write([]byte("0123456789abcdef0123456789abcdef\n")); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, name := range []string{path, path + ".1"} {
		info, err := os.Stat(name)
		if err == nil {
			total += info.Size()
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	if total > 128 {
		t.Fatalf("rolling log bytes = %d, want <= 128", total)
	}
}

type threadTestLogger struct {
	name string
}

func (*threadTestLogger) Trace(string, ...interface{})   {}
func (*threadTestLogger) Debug(string, ...interface{})   {}
func (*threadTestLogger) Verbose(string, ...interface{}) {}
func (*threadTestLogger) Info(string, ...interface{})    {}
func (*threadTestLogger) Warn(string, ...interface{})    {}
func (*threadTestLogger) Error(string, ...interface{})   {}
func (*threadTestLogger) GetBuffer() []*pb.LogLine       { return nil }

func preserveCurrentThreadLogger(t *testing.T) {
	t.Helper()
	gid := getGID()
	previous, ok := GIDToLoggerMapping.Load(gid)
	GIDToLoggerMapping.Delete(gid)
	t.Cleanup(func() {
		if ok {
			GIDToLoggerMapping.Store(gid, previous)
		} else {
			GIDToLoggerMapping.Delete(gid)
		}
	})
}

func TestSetThreadLoggerCleanupRemovesMapping(t *testing.T) {
	preserveCurrentThreadLogger(t)

	logger := &threadTestLogger{name: "bound"}
	cleanup := SetThreadLogger(logger)
	if got := getActiveLogger(); got != logger {
		t.Fatalf("active logger = %T, want bound logger", got)
	}

	cleanup()
	if _, ok := GIDToLoggerMapping.Load(getGID()); ok {
		t.Fatal("goroutine logger mapping remained after cleanup")
	}
}

func TestSetThreadLoggerCleanupRestoresPreviousAndIsIdempotent(t *testing.T) {
	preserveCurrentThreadLogger(t)

	outer := &threadTestLogger{name: "outer"}
	inner := &threadTestLogger{name: "inner"}
	outerCleanup := SetThreadLogger(outer)
	innerCleanup := SetThreadLogger(inner)

	innerCleanup()
	if got := getActiveLogger(); got != outer {
		t.Fatalf("active logger after inner cleanup = %T, want outer logger", got)
	}

	replacement := &threadTestLogger{name: "replacement"}
	replacementCleanup := SetThreadLogger(replacement)
	innerCleanup()                                                          
	if got := getActiveLogger(); got != replacement {
		t.Fatalf("repeated cleanup replaced active logger: got %T", got)
	}
	replacementCleanup()
	outerCleanup()
}

func TestSetThreadNodeContextCleanup(t *testing.T) {
	preserveCurrentThreadLogger(t)

	ident := fmt.Sprintf("thread-context-%s", t.Name())
	logger := &threadTestLogger{name: ident}
	Registry.mu.Lock()
	Registry.loggers[ident] = logger
	Registry.mu.Unlock()
	t.Cleanup(func() {
		Registry.mu.Lock()
		delete(Registry.loggers, ident)
		Registry.mu.Unlock()
	})

	cleanup := SetThreadNodeContext(ident)
	if got := getActiveLogger(); got != logger {
		t.Fatalf("active logger = %T, want node-context logger", got)
	}
	cleanup()
	if _, ok := GIDToLoggerMapping.Load(getGID()); ok {
		t.Fatal("node-context mapping remained after cleanup")
	}
}

func TestShortLivedThreadLoggerMappingsAreCleaned(t *testing.T) {
	const goroutines = 256
	logger := &threadTestLogger{name: "short-lived"}
	gids := make(chan uint64, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			cleanup := SetThreadLogger(logger)
			gid := getGID()
			cleanup()
			gids <- gid
		}()
	}
	wg.Wait()
	close(gids)

	for gid := range gids {
		if _, ok := GIDToLoggerMapping.Load(gid); ok {
			t.Fatalf("mapping for completed goroutine %d was retained", gid)
		}
	}
}
