package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"dex/protocol"
	"dex/updater"
)

func validateBuildPlatform(goos, goarch string) error {
	if goos != "linux" && goos != "darwin" {
		return errors.New("release platform must be linux or darwin")
	}
	if goarch != "amd64" && goarch != "arm64" {
		return errors.New("release architecture must be amd64 or arm64")
	}
	return nil
}

func validateUpgradeMode(mode string) error {
	if mode != "compatible" && mode != "coordinated" {
		return errors.New("--upgrade-mode must be compatible or coordinated")
	}
	return nil
}

func requireCleanPublishTree(root string) error {
	status, err := cleanCommandOutput(root, "git", "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return errors.New("cannot verify release source working tree")
	}
	if len(strings.TrimSpace(string(status))) != 0 {
		return errors.New("publishing requires a clean committed working tree, including untracked files")
	}
	if commit := gitFullCommit(root); len(commit) != 40 {
		return errors.New("publishing requires an exact Git commit")
	}
	return nil
}

func stageFrontendAssets(staging, explorerDist, walletDist string) error {
	if err := copyTreeChecked(explorerDist, filepath.Join(staging, "explorer_web", "dist")); err != nil {
		return err
	}
	return copyTreeChecked(walletDist, filepath.Join(staging, "wallet"))
}

                                                                              
                                                                              
                                       
func stageUpdateAssets(staging, outputDir, configDir, version, commit, goos, goarch, mode string) ([]string, error) {
	canonical, err := canonicalReleaseVersion(version)
	if err != nil || canonical != version {
		return nil, errors.New("automatic update assets require a canonical stable version")
	}
	if err := validateBuildPlatform(goos, goarch); err != nil {
		return nil, err
	}
	if err := validateUpgradeMode(mode); err != nil {
		return nil, err
	}
	if len(commit) != 40 {
		return nil, errors.New("automatic update assets require a full Git commit")
	}
	if _, err := hex.DecodeString(commit); err != nil {
		return nil, errors.New("invalid Git commit")
	}
	genesis, err := os.ReadFile(filepath.Join(configDir, "genesis.json"))
	if err != nil {
		return nil, fmt.Errorf("read explicit release genesis: %w", err)
	}
	var network struct {
		ChainID uint64 `json:"chain_id"`
	}
	if err := json.Unmarshal(genesis, &network); err != nil || network.ChainID == 0 {
		return nil, errors.New("release genesis must contain an explicit positive chain_id")
	}
	if err := protocol.ValidateUpgradeSchedule(network.ChainID); err != nil {
		return nil, err
	}
	if mode == "compatible" && len(protocol.UpgradeSchedule(network.ChainID)) != 0 {
		return nil, errors.New("compiled protocol upgrades require --upgrade-mode=coordinated")
	}
	nodeName := fmt.Sprintf("dex-node-%s-%s-%s", version, goos, goarch)
	nodePath := filepath.Join(outputDir, nodeName)
	if err := copyRegularFileChecked(filepath.Join(staging, "dex"), nodePath); err != nil {
		return nil, err
	}
	info, err := os.Stat(nodePath)
	if err != nil || info.Size() <= 0 {
		return nil, errors.New("node release asset is empty or unreadable")
	}
	f, err := os.Open(nodePath)
	if err != nil {
		return nil, err
	}
	hash := sha256.New()
	_, readErr := io.Copy(hash, f)
	closeErr := f.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	manifest := updater.Manifest{
		SchemaVersion: updater.SchemaVersion, Repository: updater.Repository, Tag: version, Version: version,
		Commit: commit, GOOS: goos, GOARCH: goarch, ChainIDs: []uint64{network.ChainID},
		ProtocolVersion: protocol.CurrentVersion, DatabaseVersion: updater.DatabaseVersion,
		RolloutMode: mode, RollbackSafe: mode == "compatible",
		Asset: updater.Asset{Name: nodeName, SHA256: hex.EncodeToString(hash.Sum(nil)), Size: info.Size()},
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	manifestPath := filepath.Join(outputDir, updater.ManifestName(goos, goarch))
	if err := os.WriteFile(manifestPath, append(raw, '\n'), 0o644); err != nil {
		return nil, err
	}
	return []string{nodePath, manifestPath}, nil
}
