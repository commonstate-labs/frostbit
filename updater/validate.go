package updater

import (
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

var (
	ErrUnsupported     = errors.New("automatic updates require an unprivileged native Linux node")
	ErrUnknownBaseline = errors.New("automatic updates require a versioned baseline with a complete commit")
	ErrUnsafeBootstrap = errors.New("cached update journal cannot be interpreted safely")
)

func canonicalVersion(version string) (string, error) {
	v := version
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	if !semver.IsValid(v) || semver.Prerelease(v) != "" || semver.Build(v) != "" {
		return "", fmt.Errorf("stable semantic version required: %q", version)
	}
	return semver.Canonical(v), nil
}
func digestValid(s string, size int) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == size && s == strings.ToLower(s)
}

func (c Config) validate() error {
	for _, p := range []string{c.StateDir, c.ConfigDir, c.Executable, c.GHPath} {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return fmt.Errorf("updater paths must be clean absolute paths: %q", p)
		}
	}
	if c.PollInterval < time.Minute || c.CommandTimeout <= 0 || c.MaxAssetBytes <= 0 || c.MaxAssetBytes > 2<<30 || c.MaxJitter < 0 {
		return fmt.Errorf("invalid updater resource limits")
	}
	return nil
}

func (m Manifest) Validate() error {
	if m.SchemaVersion != SchemaVersion || m.Repository != Repository {
		return fmt.Errorf("unsupported manifest identity")
	}
	v, e := canonicalVersion(m.Version)
	if e != nil {
		return e
	}
	t, e := canonicalVersion(m.Tag)
	if e != nil || t != v {
		return fmt.Errorf("manifest tag/version mismatch")
	}
	if !digestValid(m.Commit, 20) || !digestValid(m.Asset.SHA256, 32) {
		return fmt.Errorf("invalid manifest commit/digest")
	}
	if m.GOOS != "linux" || m.GOARCH != "amd64" && m.GOARCH != "arm64" {
		return fmt.Errorf("unsupported updater platform")
	}
	if len(m.ChainIDs) == 0 || len(m.ChainIDs) > 64 {
		return fmt.Errorf("manifest requires bounded chain IDs")
	}
	seen := map[uint64]bool{}
	for _, id := range m.ChainIDs {
		if id == 0 || seen[id] {
			return fmt.Errorf("invalid manifest chain IDs")
		}
		seen[id] = true
	}
	if m.ProtocolVersion == 0 || m.DatabaseVersion == 0 || m.RolloutMode != "compatible" || !m.RollbackSafe {
		return fmt.Errorf("release requires coordinated deployment")
	}
	if m.Asset.Name == "" || filepath.Base(m.Asset.Name) != m.Asset.Name || strings.ContainsAny(m.Asset.Name, "/\\\x00") || strings.HasPrefix(m.Asset.Name, ".") || m.Asset.Size <= 0 {
		return fmt.Errorf("invalid node asset")
	}
	return nil
}

func validateInfo(info BinaryInfo) error {
	if _, e := canonicalVersion(info.Version); e != nil || !digestValid(info.Commit, 20) {
		return ErrUnknownBaseline
	}
	if info.SchemaVersion != SchemaVersion || info.Repository != Repository || info.ChainID == 0 || info.ProtocolVersion == 0 || info.DatabaseVersion == 0 || len(info.UpgradeSchedule) != 0 {
		return fmt.Errorf("node requires coordinated deployment")
	}
	return nil
}

func compatible(current, candidate BinaryInfo, m Manifest) error {
	if e := m.Validate(); e != nil {
		return e
	}
	if e := validateInfo(current); e != nil {
		return e
	}
	if e := validateInfo(candidate); e != nil {
		return e
	}
	found := false
	for _, id := range m.ChainIDs {
		found = found || id == current.ChainID
	}
	if !found || candidate.ChainID != current.ChainID || candidate.GOOS != current.GOOS || candidate.GOARCH != current.GOARCH || m.GOOS != current.GOOS || m.GOARCH != current.GOARCH {
		return fmt.Errorf("release chain/platform mismatch")
	}
	if current.ProtocolVersion != candidate.ProtocolVersion || candidate.ProtocolVersion != m.ProtocolVersion || current.DatabaseVersion != candidate.DatabaseVersion || candidate.DatabaseVersion != m.DatabaseVersion {
		return fmt.Errorf("protocol/database changes require coordinated deployment")
	}
	cv, _ := canonicalVersion(current.Version)
	nv, _ := canonicalVersion(candidate.Version)
	mv, _ := canonicalVersion(m.Version)
	if nv != mv || candidate.Commit != m.Commit || semver.Compare(nv, cv) <= 0 {
		return fmt.Errorf("candidate version/commit mismatch or downgrade")
	}
	return nil
}
