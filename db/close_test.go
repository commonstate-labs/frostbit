package db

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"dex/logs"
)

func TestCloseReportsEveryFailureAndFinishesCleanup(t *testing.T) {
	stages := []string{"force flush", "stateDB close", "runtime close", "query close", "archive close"}
	for _, failed := range append(slices.Clone(stages), "all") {
		t.Run(failed, func(t *testing.T) {
			root := t.TempDir()
			manager, err := NewManager(root, logs.NewNodeLogger("close-failure", 0))
			if err != nil {
				t.Fatal(err)
			}
			var visited []string
			injected := errors.New("injected shutdown failure")
			fail := func(stage string, err error) error {
				visited = append(visited, stage)
				if stage == failed || failed == "all" {
					return errors.Join(err, fmt.Errorf("%s: %w", stage, injected))
				}
				return err
			}
			err = manager.closeWithError(func() error { return fail("force flush", manager.ForceFlush()) }, func(stage string, close func() error) error { return fail(stage, close()) })
			if !errors.Is(err, injected) || !slices.Equal(visited, stages) {
				t.Fatalf("close=%v visited=%v", err, visited)
			}
			for _, stage := range stages {
				if (failed == "all" || failed == stage) && !strings.Contains(err.Error(), stage) {
					t.Fatalf("missing %s: %v", stage, err)
				}
			}
			if manager.stateDB != nil || manager.Db != nil || manager.runtimeDB != nil || manager.queryDB != nil || manager.archiveDB != nil {
				t.Fatal("failure prevented remaining resource cleanup")
			}
			if again := manager.CloseWithError(); again != err || len(visited) != len(stages) {
				t.Fatalf("repeated close forgot or repeated failure: %v", again)
			}
			reopened, openErr := NewManager(root, logs.NewNodeLogger("close-reopen", 0))
			if openErr != nil {
				t.Fatalf("closed stores could not reopen: %v", openErr)
			}
			if closeErr := reopened.CloseWithError(); closeErr != nil {
				t.Fatal(closeErr)
			}
		})
	}
}
