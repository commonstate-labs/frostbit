package main

import (
	"bytes"
	"dex/protocol"
	"dex/updater"
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestUpgradeInfoExitsBeforeSecretsLogsAndDatabase(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "genesis.json"), []byte(`{"chain_id":51025}`), 0600); err != nil {
		t.Fatal(err)
	}
	previousFlags, previousArgs, previousStdout := flag.CommandLine, os.Args, os.Stdout
	previousVersion, previousCommit := BuildVersion, BuildCommit
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		flag.CommandLine, os.Args, os.Stdout = previousFlags, previousArgs, previousStdout
		BuildVersion, BuildCommit = previousVersion, previousCommit
		read.Close()
		write.Close()
	})
	BuildVersion, BuildCommit = "v1.2.3", strings.Repeat("a", 40)
	flag.CommandLine = flag.NewFlagSet("upgrade-info", flag.ContinueOnError)
	os.Args = []string{"node", "--upgrade-info", "--config-dir", dir, "--private-key-fd", "999999", "--data-dir", filepath.Join(dir, "must-not-open")}
	os.Stdout = write
	t.Setenv("DEX_LOG_FILE", filepath.Join(dir, "must-not-create.log"))
	t.Setenv("DEX_RETAIN_GENESIS_PROOF", "invalid-for-normal-startup")
	if err := runMain(); err != nil {
		t.Fatal(err)
	}
	write.Close()
	output, err := io.ReadAll(read)
	if err != nil {
		t.Fatal(err)
	}
	var info updater.BinaryInfo
	if err := json.Unmarshal(output, &info); err != nil {
		t.Fatalf("upgrade info is not clean JSON: %q: %v", output, err)
	}
	if info.Repository != updater.Repository || info.SchemaVersion != updater.SchemaVersion || !info.AutoUpdateDefault ||
		info.Version != BuildVersion || info.Commit != BuildCommit || info.ChainID != 51025 ||
		info.GOOS != runtime.GOOS || info.GOARCH != runtime.GOARCH ||
		info.ProtocolVersion != protocol.CurrentVersion || info.DatabaseVersion != updater.DatabaseVersion || info.UpgradeSchedule == nil {
		t.Fatalf("unexpected compatibility info: %+v", info)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "genesis.json" {
		t.Fatalf("upgrade info changed the config/data directory: %v, %v", entries, err)
	}
}

func TestUpgradeInfoRequiresExplicitNetworkIdentity(t *testing.T) {
	for _, content := range []string{"", "{}", "null", `{"chain_id":0}`, `{"chain_id":-1}`, `{"chain_id":51025} {}`} {
		t.Run(content, func(t *testing.T) {
			dir := t.TempDir()
			if content != "" {
				if err := os.WriteFile(filepath.Join(dir, "genesis.json"), []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var output bytes.Buffer
			if err := writeUpgradeInfo(&output, dir); err == nil || output.Len() != 0 {
				t.Fatalf("unidentified network accepted: output=%q, error=%v", output.String(), err)
			}
		})
	}
}
