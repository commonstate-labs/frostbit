package updater

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/semver"
)

type Agent struct {
	cfg           Config
	dep           Dependencies
	apiURL        string
	mu            sync.Mutex
	etag          string
	cachedRelease *release
	nextCheck     time.Time
}

func New(cfg Config, dep Dependencies) (*Agent, error) {
	if e := cfg.validate(); e != nil {
		return nil, e
	}
	if dep.Runner == nil {
		dep.Runner = ExecRunner{GHPath: cfg.GHPath, GHConfigDir: filepath.Join(cfg.StateDir, "gh")}
	}
	if dep.HTTPClient == nil {
		dep.HTTPClient = &http.Client{Timeout: 5 * time.Minute}
	}
	if dep.Now == nil {
		dep.Now = time.Now
	}
	if dep.Logf == nil {
		dep.Logf = func(string, ...any) {}
	}
	if dep.AcquireLock == nil {
		dep.AcquireLock = lockState
	}
	if dep.ValidatePaths == nil {
		dep.ValidatePaths = validateOwnedPaths
	}
	if dep.Exec == nil {
		dep.Exec = platformExec
	}
	if dep.Args == nil {
		dep.Args = os.Args
	}
	if dep.Env == nil {
		dep.Env = os.Environ()
	}
	if dep.EUID == nil {
		dep.EUID = os.Geteuid
	}
	if dep.CurrentExecutable == nil {
		dep.CurrentExecutable = os.Executable
	}
	if dep.Supported == nil {
		dep.Supported = PlatformSupported
	}
	api := "https://api.github.com"
	if dep.APIURL != "" {
		api = strings.TrimRight(dep.APIURL, "/")
	}
	client, e := restrictedClient(dep.HTTPClient, api)
	if e != nil {
		return nil, e
	}
	dep.HTTPClient = client
	return &Agent{cfg: cfg, dep: dep, apiURL: api}, nil
}
func PlatformSupported() bool {
	return runtime.GOOS == "linux" && (runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64")
}
func (a *Agent) supported() error {
	if !a.dep.Supported() || a.dep.EUID() <= 0 {
		return ErrUnsupported
	}
	return nil
}
func wait(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
func startupDelay(host string, cfg Config) time.Duration {
	if cfg.MaxJitter <= 0 {
		return 0
	}
	sum := sha256.Sum256([]byte(host + "\x00" + cfg.StateDir))
	return time.Duration(binary.BigEndian.Uint64(sum[:8]) % uint64(cfg.MaxJitter))
}

                                                                                
                                                                                 
func Run(ctx context.Context, cfg Config, dep Dependencies, ready chan<- Prepared) error {
	if !cfg.Enabled {
		return nil
	}
	a, e := New(cfg, dep)
	if e != nil {
		return e
	}
	if e = a.supported(); e != nil {
		return e
	}
	if e = validateInfo(cfg.Info); e != nil {
		return e
	}
	host, _ := os.Hostname()
	if e = wait(ctx, startupDelay(host, cfg)); e != nil {
		return nil
	}
	for {
		p, e := a.PrepareOnce(ctx)
		if e != nil {
			a.dep.Logf("automatic update check: %v", e)
		}
		if p != nil {
			select {
			case ready <- *p:
				return nil
			case <-ctx.Done():
				return nil
			}
		}
		delay := cfg.PollInterval
		if d := a.nextCheck.Sub(a.dep.Now()); d > delay {
			delay = d
		}
		if e = wait(ctx, delay); e != nil {
			return nil
		}
	}
}

func (a *Agent) setupCache() error {
	if e := ensureRealDir(a.cfg.StateDir, 0700); e != nil {
		return e
	}
	if e := a.checkCache(a.cfg.StateDir); e != nil {
		return e
	}
	for _, p := range []string{filepath.Join(a.cfg.StateDir, "versions"), filepath.Join(a.cfg.StateDir, "gh")} {
		if e := ensureRealDir(p, 0700); e != nil {
			return e
		}
		if e := a.checkCache(p); e != nil {
			return e
		}
	}
	return nil
}

func (a *Agent) PrepareOnce(ctx context.Context) (*Prepared, error) {
	if !a.cfg.Enabled {
		return nil, nil
	}
	if e := a.supported(); e != nil {
		return nil, e
	}
	if e := validateInfo(a.cfg.Info); e != nil {
		return nil, e
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if e := a.setupCache(); e != nil {
		return nil, e
	}
	unlock, e := a.dep.AcquireLock(filepath.Join(a.cfg.StateDir, "updater.lock"))
	if e != nil {
		return nil, e
	}
	defer unlock()
	s, _, e := a.readState()
	if e != nil {
		return nil, e
	}
	if s.Pending != nil {
		return nil, fmt.Errorf("previous update awaits startup confirmation")
	}
	if e = a.prune(s, ""); e != nil {
		return nil, e
	}
	entries, e := os.ReadDir(a.cfg.StateDir)
	if e != nil {
		return nil, e
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "download-") {
			p := filepath.Join(a.cfg.StateDir, entry.Name())
			if e = a.checkCache(p); e != nil {
				return nil, e
			}
			if e = os.RemoveAll(p); e != nil {
				return nil, e
			}
		}
	}
	if a.dep.Now().Before(a.nextCheck) {
		return nil, nil
	}
	r, e := a.latest(ctx)
	if e != nil {
		return nil, e
	}
	rv, _ := canonicalVersion(r.Tag)
	cv, _ := canonicalVersion(a.cfg.Info.Version)
	if semver.Compare(rv, cv) <= 0 {
		return nil, nil
	}
	if r.ID <= s.HighReleaseID {
		return nil, fmt.Errorf("release replay rejected")
	}
	if s.HighVersion != "" {
		hv, _ := canonicalVersion(s.HighVersion)
		if semver.Compare(rv, hv) <= 0 {
			return nil, fmt.Errorf("release downgrade rejected")
		}
	}
	asset, e := findAsset(r, ManifestName(a.cfg.Info.GOOS, a.cfg.Info.GOARCH))
	if e != nil {
		return nil, e
	}
	dir, e := os.MkdirTemp(a.cfg.StateDir, "download-")
	if e != nil {
		return nil, e
	}
	defer os.RemoveAll(dir)
	manifestPath := filepath.Join(dir, asset.Name)
	if e = a.download(ctx, asset, manifestPath, maxManifestBytes); e != nil {
		return nil, e
	}
	if e = a.verify(ctx, r, manifestPath); e != nil {
		return nil, e
	}
	f, e := os.Open(manifestPath)
	if e != nil {
		return nil, e
	}
	var m Manifest
	e = decodeBounded(f, maxManifestBytes, &m)
	f.Close()
	if e != nil {
		return nil, e
	}
	if e = m.Validate(); e != nil {
		return nil, e
	}
	if m.Tag != r.Tag {
		return nil, fmt.Errorf("manifest tag mismatch")
	}
	if m.Asset.SHA256 == s.FailedDigest {
		return nil, fmt.Errorf("failed release is quarantined")
	}
	nodeAsset, e := findAsset(r, m.Asset.Name)
	if e != nil {
		return nil, e
	}
	if nodeAsset.Size != m.Asset.Size || strings.TrimPrefix(nodeAsset.Digest, "sha256:") != m.Asset.SHA256 {
		return nil, fmt.Errorf("node/manifest binding mismatch")
	}
	nodePath := filepath.Join(dir, m.Asset.Name)
	if e = a.download(ctx, nodeAsset, nodePath, a.cfg.MaxAssetBytes); e != nil {
		return nil, e
	}
	if e = a.verify(ctx, r, nodePath); e != nil {
		return nil, e
	}
	if e = os.Chmod(nodePath, 0700); e != nil {
		return nil, e
	}
	b, e := a.command(ctx, nodePath, "--upgrade-info", "--config-dir", a.cfg.ConfigDir)
	if e != nil {
		return nil, fmt.Errorf("candidate metadata check: %w", e)
	}
	var info BinaryInfo
	if e = json.Unmarshal(b, &info); e != nil {
		return nil, e
	}
	if e = compatible(a.cfg.Info, info, m); e != nil {
		return nil, e
	}
	if _, e = a.command(ctx, nodePath, "--check-config", "--config-dir", a.cfg.ConfigDir, fmt.Sprintf("--retain-genesis-proof=%t", a.cfg.RetainGenesisProof)); e != nil {
		return nil, fmt.Errorf("candidate config check: %w", e)
	}
	p := Prepared{Path: filepath.Join(a.cfg.StateDir, "versions", m.Asset.SHA256, "node"), ReleaseID: r.ID, Tag: r.Tag, Version: m.Version, Commit: m.Commit, Digest: m.Asset.SHA256, Manifest: m}
	if e = ensureRealDir(filepath.Dir(p.Path), 0700); e != nil {
		return nil, e
	}
	if e = a.checkCache(filepath.Dir(p.Path)); e != nil {
		return nil, e
	}
	if _, e = os.Lstat(p.Path); e == nil {
		if digest, e := fileDigest(p.Path); e != nil || digest != p.Digest {
			return nil, fmt.Errorf("existing candidate cache was modified")
		}
	} else if os.IsNotExist(e) {
		if e = os.Rename(nodePath, p.Path); e != nil {
			return nil, e
		}
	} else {
		return nil, e
	}
	if e = syncDir(filepath.Dir(p.Path)); e != nil {
		return nil, e
	}
	if e = syncDir(filepath.Dir(filepath.Dir(p.Path))); e != nil {
		return nil, e
	}
	if e = a.validatePrepared(p, true); e != nil {
		return nil, e
	}
	return &p, nil
}
