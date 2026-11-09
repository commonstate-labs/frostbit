package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveWebDirInCompleteRelease(t *testing.T) {
	dir := t.TempDir()
	                                                                            
	if err := os.WriteFile(filepath.Join(dir, "explorer"), []byte("executable"), 0755); err != nil {
		t.Fatal(err)
	}
	web := filepath.Join("explorer_web", "dist")
	if err := os.MkdirAll(filepath.Join(dir, web), 0755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	if got := resolveWebDir(""); got != web {
		t.Fatalf("packaged Explorer assets not found: got %q, want %q", got, web)
	}
}
