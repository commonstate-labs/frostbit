package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"dex/protocol"
	"dex/updater"
)

func TestUpdateAssetsBindExactNodeAndExplicitNetwork(t *testing.T) {
	staging, output, network := t.TempDir(), t.TempDir(), t.TempDir()
	node := []byte("exact node executable")
	writeTestFile(t, filepath.Join(staging, "dex"), string(node))
	writeTestFile(t, filepath.Join(network, "genesis.json"), `{"chain_id":51025}`)
	paths, err := stageUpdateAssets(staging, output, network, "v1.2.3", strings.Repeat("a", 40), "linux", "amd64", "compatible")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 {
		t.Fatalf("assets=%v", paths)
	}
	raw, err := os.ReadFile(paths[1])
	if err != nil {
		t.Fatal(err)
	}
	var manifest updater.Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(node)
	if manifest.Repository != updater.Repository || manifest.Tag != "v1.2.3" || manifest.Version != "v1.2.3" || manifest.Commit != strings.Repeat("a", 40) ||
		manifest.Asset.SHA256 != hex.EncodeToString(hash[:]) || manifest.Asset.Size != int64(len(node)) || filepath.Base(paths[0]) != manifest.Asset.Name ||
		len(manifest.ChainIDs) != 1 || manifest.ChainIDs[0] != 51025 || manifest.ProtocolVersion != protocol.CurrentVersion || manifest.DatabaseVersion != updater.DatabaseVersion || !manifest.RollbackSafe {
		t.Fatalf("incorrect manifest binding: %+v", manifest)
	}
	for _, contents := range []string{`{}`, `{"chain_id":0}`, `{"chain_id":`} {
		writeTestFile(t, filepath.Join(network, "genesis.json"), contents)
		if _, err := stageUpdateAssets(staging, output, network, "v1.2.3", strings.Repeat("a", 40), "linux", "amd64", "compatible"); err == nil {
			t.Fatalf("implicit/invalid network accepted: %s", contents)
		}
	}
	if err := os.Remove(filepath.Join(network, "genesis.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := stageUpdateAssets(staging, output, network, "v1.2.3", strings.Repeat("a", 40), "linux", "amd64", "compatible"); err == nil {
		t.Fatal("missing genesis accepted")
	}
}

func TestCoordinatedManifestCannotClaimRollbackSafe(t *testing.T) {
	staging, output, network := t.TempDir(), t.TempDir(), t.TempDir()
	writeTestFile(t, filepath.Join(staging, "dex"), "binary")
	writeTestFile(t, filepath.Join(network, "genesis.json"), `{"chain_id":51025}`)
	paths, err := stageUpdateAssets(staging, output, network, "v1.2.3", strings.Repeat("a", 40), "linux", "arm64", "coordinated")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(paths[1])
	if err != nil {
		t.Fatal(err)
	}
	var manifest updater.Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.RolloutMode != "coordinated" || manifest.RollbackSafe {
		t.Fatalf("unsafe coordinated metadata: %+v", manifest)
	}
}

func TestReleaseArchiveKeepsExecutablesSeparateFromStaticDirectories(t *testing.T) {
	staging, explorerDist, walletDist := t.TempDir(), t.TempDir(), t.TempDir()
	writeTestFile(t, filepath.Join(staging, "explorer"), "explorer executable")
	writeTestFile(t, filepath.Join(staging, "dex"), "node executable")
	writeTestFile(t, filepath.Join(explorerDist, "index.html"), "frontend")
	writeTestFile(t, filepath.Join(walletDist, "manifest.json"), "wallet")
	if err := stageFrontendAssets(staging, explorerDist, walletDist); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "release.zip")
	if err := zipTree(staging, archive, "dex"); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.OpenReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	want := map[string]bool{"dex/explorer": false, "dex/explorer_web/dist/index.html": false, "dex/dex": false, "dex/wallet/manifest.json": false}
	for _, file := range reader.File {
		if _, ok := want[file.Name]; ok {
			if file.FileInfo().IsDir() {
				t.Errorf("expected file at %s", file.Name)
			}
			want[file.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("archive missing %s", name)
		}
	}
}

func TestPublishingRequiresCommittedUnmodifiedSource(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if raw, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, raw)
		}
	}
	git("init")
	writeTestFile(t, filepath.Join(root, "source.go"), "initial")
	git("add", "source.go")
	git("-c", "user.name=Release Test", "-c", "user.email=release@example.invalid", "-c", "commit.gpgSign=false", "commit", "-m", "initial")
	if err := requireCleanPublishTree(root); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "source.go"), "modified")
	if err := requireCleanPublishTree(root); err == nil {
		t.Fatal("modified source accepted")
	}
	git("checkout", "--", "source.go")
	writeTestFile(t, filepath.Join(root, "untracked.go"), "untracked")
	if err := requireCleanPublishTree(root); err == nil {
		t.Fatal("untracked source accepted")
	}
}
