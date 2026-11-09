package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type pendingUpdate struct {
	Prepared Prepared `json:"prepared"`
	Attempt  int      `json:"attempt"`
}
type diskState struct {
	SchemaVersion int            `json:"schema_version"`
	HighReleaseID int64          `json:"high_release_id"`
	HighVersion   string         `json:"high_version"`
	Stable        *Prepared      `json:"stable,omitempty"`
	Previous      *Prepared      `json:"previous,omitempty"`
	Pending       *pendingUpdate `json:"pending,omitempty"`
	FailedDigest  string         `json:"failed_digest,omitempty"`
}

func within(root, path string) bool {
	rel, e := filepath.Rel(root, path)
	return e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
func ensureRealDir(path string, mode os.FileMode) error {
	if e := os.MkdirAll(path, mode); e != nil {
		return e
	}
	i, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if !i.IsDir() || i.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("unsafe updater directory")
	}
	real, e := filepath.EvalSymlinks(path)
	if e != nil || real != path {
		return fmt.Errorf("updater directory traverses symbolic links")
	}
	return nil
}
func syncDir(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return syncDirectory(f)
}
func atomicJSON(path string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	b = append(b, '\n')
	f, e := os.CreateTemp(filepath.Dir(path), ".state-")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	e = f.Chmod(0600)
	if e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	if e = os.Rename(tmp, path); e != nil {
		return e
	}
	return syncDir(filepath.Dir(path))
}
func fileDigest(path string) (string, error) {
	i, e := os.Lstat(path)
	if e != nil {
		return "", e
	}
	if !i.Mode().IsRegular() {
		return "", fmt.Errorf("node cache must be a regular file")
	}
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.CopyBuffer(h, f, make([]byte, 64<<10)); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func HashBytes(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func (a *Agent) readState() (diskState, bool, error) {
	var s diskState
	path := filepath.Join(a.cfg.StateDir, "state.json")
	i, e := os.Lstat(path)
	if os.IsNotExist(e) {
		return s, false, nil
	}
	if e != nil {
		return s, false, e
	}
	if !i.Mode().IsRegular() || i.Size() > maxManifestBytes {
		return s, false, fmt.Errorf("unsafe updater journal")
	}
	if e = a.checkCache(a.cfg.StateDir, path); e != nil {
		return s, false, e
	}
	f, e := os.Open(path)
	if e != nil {
		return s, false, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, maxManifestBytes+1))
	if e != nil {
		return s, false, e
	}
	if len(b) > maxManifestBytes {
		return s, false, fmt.Errorf("%w: journal exceeds limit", ErrUnsafeBootstrap)
	}
	                                                                          
	                                                                           
	if e = json.Unmarshal(b, &s); e != nil {
		return s, false, fmt.Errorf("%w: %v", ErrUnsafeBootstrap, e)
	}
	if s.SchemaVersion != SchemaVersion {
		return s, false, fmt.Errorf("%w: unknown journal schema %d", ErrUnsafeBootstrap, s.SchemaVersion)
	}
	if s.HighReleaseID < 0 {
		return s, false, fmt.Errorf("%w: invalid update high-water mark", ErrUnsafeBootstrap)
	}
	if s.HighVersion != "" {
		if _, e = canonicalVersion(s.HighVersion); e != nil {
			return s, false, fmt.Errorf("%w: %v", ErrUnsafeBootstrap, e)
		}
	}
	for _, p := range []*Prepared{s.Stable, s.Previous} {
		if p != nil {
			if e = a.validatePrepared(*p, false); e != nil {
				return s, false, fmt.Errorf("%w: %v", ErrUnsafeBootstrap, e)
			}
		}
	}
	if s.Pending != nil {
		if s.Pending.Attempt < 0 || s.Pending.Attempt > 1 {
			return s, false, fmt.Errorf("%w: invalid pending attempt", ErrUnsafeBootstrap)
		}
		if e = a.validatePrepared(s.Pending.Prepared, false); e != nil {
			return s, false, fmt.Errorf("%w: %v", ErrUnsafeBootstrap, e)
		}
	}
	return s, true, nil
}
func (a *Agent) saveState(s diskState) error {
	s.SchemaVersion = SchemaVersion
	return atomicJSON(filepath.Join(a.cfg.StateDir, "state.json"), s)
}

func (a *Agent) checkCache(paths ...string) error {
	if e := a.dep.ValidatePaths(paths...); e != nil {
		return e
	}
	for _, p := range paths {
		if e := cacheOwnedByProcess(p); e != nil {
			return e
		}
	}
	return nil
}

func (a *Agent) validatePrepared(p Prepared, checkFile bool) error {
	if p.ReleaseID <= 0 || !digestValid(p.Digest, 32) || !digestValid(p.Commit, 20) {
		return fmt.Errorf("invalid prepared update")
	}
	expected := filepath.Join(a.cfg.StateDir, "versions", p.Digest, "node")
	if p.Path != expected {
		return fmt.Errorf("prepared update escapes executable cache")
	}
	if e := p.Manifest.Validate(); e != nil {
		return e
	}
	v, e := canonicalVersion(p.Version)
	if e != nil {
		return e
	}
	mv, _ := canonicalVersion(p.Manifest.Version)
	if v != mv || p.Tag != p.Manifest.Tag || p.Commit != p.Manifest.Commit || p.Digest != p.Manifest.Asset.SHA256 {
		return fmt.Errorf("prepared update metadata mismatch")
	}
	if checkFile {
		if e = a.checkCache(a.cfg.StateDir, filepath.Dir(p.Path), p.Path); e != nil {
			return e
		}
		digest, e := fileDigest(p.Path)
		if e != nil || digest != p.Digest {
			return fmt.Errorf("prepared executable digest mismatch")
		}
	}
	return nil
}

func (a *Agent) validateSelected(p Prepared) error {
	if e := validateInfo(a.cfg.Info); e != nil {
		return e
	}
	found := false
	for _, id := range p.Manifest.ChainIDs {
		found = found || id == a.cfg.Info.ChainID
	}
	if !found || p.Manifest.GOOS != a.cfg.Info.GOOS || p.Manifest.GOARCH != a.cfg.Info.GOARCH || p.Manifest.ProtocolVersion != a.cfg.Info.ProtocolVersion || p.Manifest.DatabaseVersion != a.cfg.Info.DatabaseVersion {
		return fmt.Errorf("selected update does not match node chain/platform/protocol")
	}
	return nil
}

func (a *Agent) prune(s diskState, extra string) error {
	dir := filepath.Join(a.cfg.StateDir, "versions")
	entries, e := os.ReadDir(dir)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	keep := map[string]bool{a.cfg.Executable: true, extra: true}
	for _, p := range []*Prepared{s.Stable, s.Previous} {
		if p != nil {
			keep[p.Path] = true
		}
	}
	if s.Pending != nil {
		keep[s.Pending.Prepared.Path] = true
	}
	for _, entry := range entries {
		if !entry.IsDir() || !digestValid(entry.Name(), 32) {
			continue
		}
		p := filepath.Join(dir, entry.Name(), "node")
		if keep[p] {
			continue
		}
		root := filepath.Dir(p)
		if e = a.checkCache(root); e != nil {
			return e
		}
		if e = os.RemoveAll(root); e != nil {
			return e
		}
	}
	return nil
}
