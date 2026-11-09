package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dex/protocol"
)

type testRunner struct {
	info                  BinaryInfo
	gh                    string
	events                []string
	failProof, failConfig bool
}

func (r *testRunner) Run(_ context.Context, program string, args ...string) ([]byte, error) {
	if program == r.gh {
		r.events = append(r.events, "proof:"+filepath.Base(args[3]))
		if r.failProof {
			return nil, errors.New("invalid proof")
		}
		return nil, nil
	}
	if len(args) == 0 {
		return nil, fmt.Errorf("unexpected helper")
	}
	r.events = append(r.events, args[0])
	if args[0] == "--upgrade-info" {
		return json.Marshal(r.info)
	}
	if args[0] == "--check-config" {
		if r.failConfig {
			return nil, errors.New("bad config")
		}
		return nil, nil
	}
	return nil, fmt.Errorf("unexpected helper")
}

type embeddedFixture struct {
	cfg                                Config
	dep                                Dependencies
	agent                              *Agent
	runner                             *testRunner
	server                             *httptest.Server
	release                            release
	manifest                           Manifest
	manifestBytes, node                []byte
	latestCalls, assetCalls, execCalls int
	execPath                           string
	lockHeld                           bool
	execArgs, execEnv                  []string
}

func newEmbeddedFixture(t *testing.T) *embeddedFixture {
	t.Helper()
	root := t.TempDir()
	exe := filepath.Join(root, "baseline")
	if e := os.WriteFile(exe, []byte("baseline"), 0700); e != nil {
		t.Fatal(e)
	}
	info := BinaryInfo{SchemaVersion: 1, Repository: Repository, Version: "v1.0.0", Commit: strings.Repeat("a", 40), ChainID: 51025, GOOS: "linux", GOARCH: "amd64", ProtocolVersion: 3, DatabaseVersion: 1, UpgradeSchedule: []protocol.Upgrade{}, AutoUpdateDefault: true}
	cfg := DefaultConfig()
	cfg.StateDir = filepath.Join(root, "data", ".node-updates")
	cfg.ConfigDir = filepath.Join(root, "config")
	cfg.Executable = exe
	cfg.GHPath = filepath.Join(root, "gh")
	cfg.Info = info
	cfg.MaxJitter = 0
	candidate := info
	candidate.Version = "v1.1.0"
	candidate.Commit = strings.Repeat("b", 40)
	f := &embeddedFixture{cfg: cfg, node: []byte("verified replacement executable")}
	f.runner = &testRunner{gh: cfg.GHPath, info: candidate}
	f.manifest = Manifest{SchemaVersion: 1, Repository: Repository, Tag: candidate.Version, Version: candidate.Version, Commit: candidate.Commit, GOOS: "linux", GOARCH: "amd64", ChainIDs: []uint64{51025}, ProtocolVersion: 3, DatabaseVersion: 1, RolloutMode: "compatible", RollbackSafe: true, Asset: Asset{Name: "dex-node-v1.1.0-linux-amd64", SHA256: HashBytes(f.node), Size: int64(len(f.node))}}
	f.refresh(t)
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/" + Repository + "/releases/latest":
			f.latestCalls++
			json.NewEncoder(w).Encode(f.release)
		case "/repos/" + Repository + "/releases/assets/1":
			f.assetCalls++
			w.Write(f.manifestBytes)
		case "/repos/" + Repository + "/releases/assets/2":
			f.assetCalls++
			w.Write(f.node)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.server.Close)
	f.dep = Dependencies{HTTPClient: f.server.Client(), Runner: f.runner, APIURL: f.server.URL, EUID: func() int { return 1000 }, Supported: func() bool { return true }, ValidatePaths: func(...string) error { return nil }, CurrentExecutable: func() (string, error) { return f.cfg.Executable, nil }, Args: []string{"original-node", "--private-key-fd=3", "--config-dir", cfg.ConfigDir}, Env: []string{"UNCHANGED=value"}}
	f.dep.AcquireLock = func(string) (func(), error) {
		if f.lockHeld {
			return nil, errors.New("already locked")
		}
		f.lockHeld = true
		return func() { f.lockHeld = false }, nil
	}
	f.dep.Exec = func(p string, args, env []string) error {
		if f.lockHeld {
			t.Error("cache lock held during same-PID exec")
		}
		f.execCalls++
		f.execPath = p
		f.execArgs = append([]string(nil), args...)
		f.execEnv = append([]string(nil), env...)
		return nil
	}
	a, e := New(cfg, f.dep)
	if e != nil {
		t.Fatal(e)
	}
	f.agent = a
	return f
}
func (f *embeddedFixture) refresh(t *testing.T) {
	t.Helper()
	var e error
	f.manifestBytes, e = json.Marshal(f.manifest)
	if e != nil {
		t.Fatal(e)
	}
	f.release = release{ID: 42, Tag: f.manifest.Tag, Immutable: true, Assets: []releaseAsset{{ID: 1, Name: ManifestName("linux", "amd64"), Size: int64(len(f.manifestBytes)), Digest: "sha256:" + HashBytes(f.manifestBytes)}, {ID: 2, Name: f.manifest.Asset.Name, Size: int64(len(f.node)), Digest: "sha256:" + HashBytes(f.node)}}}
}
func (f *embeddedFixture) prepared(t *testing.T) Prepared {
	t.Helper()
	p, e := f.agent.PrepareOnce(context.Background())
	if e != nil || p == nil {
		t.Fatalf("prepare=%+v error=%v", p, e)
	}
	return *p
}
func (f *embeddedFixture) state(t *testing.T) diskState {
	t.Helper()
	s, _, e := f.agent.readState()
	if e != nil {
		t.Fatal(e)
	}
	return s
}

func TestEmbeddedPrepareActivateBootstrapConfirm(t *testing.T) {
	f := newEmbeddedFixture(t)
	p := f.prepared(t)
	if f.execCalls != 0 {
		t.Fatal("preparation restarted node")
	}
	if b, _ := os.ReadFile(f.cfg.Executable); string(b) != "baseline" {
		t.Fatal("installation path changed")
	}
	events := strings.Join(f.runner.events, "\n")
	if strings.Index(events, "proof:dex-node") > strings.Index(events, "--upgrade-info") || strings.Count(events, "proof:") != 2 {
		t.Fatal("candidate executed before both proofs", events)
	}
	if e := Activate(context.Background(), f.cfg, f.dep, p); e != nil {
		t.Fatal(e)
	}
	s := f.state(t)
	if s.Pending == nil || s.Pending.Attempt != 0 || s.HighReleaseID != 42 {
		t.Fatalf("activation state=%+v", s)
	}
	if e := Bootstrap(context.Background(), f.cfg, f.dep); e != nil {
		t.Fatal(e)
	}
	if f.execPath != p.Path || f.execCalls != 1 || f.lockHeld {
		t.Fatal("bootstrap did not exec selected cache")
	}
	if strings.Join(f.execArgs, "|") != strings.Join(f.dep.Args, "|") || strings.Join(f.execEnv, "|") != strings.Join(f.dep.Env, "|") {
		t.Fatal("argv/env changed")
	}
	if ok, e := PendingConfirmation(context.Background(), f.cfg, f.dep); e != nil || ok {
		t.Fatalf("baseline falsely considered candidate %t %v", ok, e)
	}
	if e := Confirm(context.Background(), f.cfg, f.dep); e == nil {
		t.Fatal("baseline confirmed candidate")
	}
	f.cfg.Executable = p.Path
	f.cfg.Info = f.runner.info
	if e := Bootstrap(context.Background(), f.cfg, f.dep); e != nil {
		t.Fatal(e)
	}
	if f.execCalls != 1 {
		t.Fatal("candidate recursively execed")
	}
	if ok, e := PendingConfirmation(context.Background(), f.cfg, f.dep); e != nil || !ok {
		t.Fatalf("candidate not pending %t %v", ok, e)
	}
	if e := Confirm(context.Background(), f.cfg, f.dep); e != nil {
		t.Fatal(e)
	}
	s = f.state(t)
	if s.Pending != nil || s.Stable == nil || s.Stable.Path != p.Path {
		t.Fatalf("confirmation=%+v", s)
	}
}

func TestUnconfirmedStartupRollsBackAndKeepsHighWater(t *testing.T) {
	f := newEmbeddedFixture(t)
	p := f.prepared(t)
	if e := Activate(context.Background(), f.cfg, f.dep, p); e != nil {
		t.Fatal(e)
	}
	if e := Bootstrap(context.Background(), f.cfg, f.dep); e != nil {
		t.Fatal(e)
	}
	if e := Bootstrap(context.Background(), f.cfg, f.dep); e != nil {
		t.Fatal(e)
	}
	s := f.state(t)
	if s.Pending != nil || s.FailedDigest != p.Digest || s.HighReleaseID != 42 || f.execCalls != 1 {
		t.Fatalf("rollback=%+v execs=%d", s, f.execCalls)
	}
	if _, e := f.agent.PrepareOnce(context.Background()); e == nil {
		t.Fatal("failed release replay accepted")
	}
}

func TestDisabledUpdatesStillLoadConfirmedStable(t *testing.T) {
	f := newEmbeddedFixture(t)
	p := f.prepared(t)
	if e := f.agent.saveState(diskState{HighReleaseID: 42, HighVersion: p.Version, Stable: &p}); e != nil {
		t.Fatal(e)
	}
	f.cfg.Enabled = false
	if e := Bootstrap(context.Background(), f.cfg, f.dep); e != nil {
		t.Fatal(e)
	}
	if f.execCalls != 1 || f.execPath != p.Path {
		t.Fatal("disabled downloads downgraded confirmed selection")
	}
	if e := Activate(context.Background(), f.cfg, f.dep, p); e == nil {
		t.Fatal("disabled updater activated pending")
	}
	f.cfg.Info.Version = "v1.2.0"
	f.cfg.Info.Commit = strings.Repeat("c", 40)
	f.execCalls = 0
	if e := Bootstrap(context.Background(), f.cfg, f.dep); e != nil {
		t.Fatal(e)
	}
	if f.execCalls != 0 {
		t.Fatal("cache overrode newer manually installed baseline")
	}
	f.cfg.Info.Version = p.Version
	f.cfg.Info.Commit = strings.Repeat("d", 40)
	if e := Bootstrap(context.Background(), f.cfg, f.dep); e != nil {
		t.Fatal(e)
	}
	if f.execCalls != 0 {
		t.Fatal("cache overrode same-version different-commit baseline")
	}
}

func TestBootstrapWithoutCacheDoesNotCreateAnything(t *testing.T) {
	f := newEmbeddedFixture(t)
	if e := Bootstrap(context.Background(), f.cfg, f.dep); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Dir(f.cfg.StateDir)); !os.IsNotExist(e) {
		t.Fatal("bootstrap created node data/cache")
	}
	if f.execCalls != 0 || f.latestCalls != 0 || len(f.runner.events) != 0 {
		t.Fatal("fresh bootstrap performed work")
	}
}

func TestRejectedCandidatesNeverExecOrActivate(t *testing.T) {
	tests := []struct {
		name   string
		change func(*embeddedFixture)
	}{{"mutable", func(f *embeddedFixture) { f.release.Immutable = false }}, {"draft", func(f *embeddedFixture) { f.release.Draft = true }}, {"prerelease", func(f *embeddedFixture) { f.release.Prerelease = true }}, {"proof", func(f *embeddedFixture) { f.runner.failProof = true }}, {"config", func(f *embeddedFixture) { f.runner.failConfig = true }}, {"chain", func(f *embeddedFixture) { f.manifest.ChainIDs = []uint64{1}; f.refresh(t) }}, {"protocol", func(f *embeddedFixture) { f.manifest.ProtocolVersion++; f.refresh(t) }}, {"db", func(f *embeddedFixture) { f.manifest.DatabaseVersion++; f.refresh(t) }}, {"tampered", func(f *embeddedFixture) { f.node = []byte("wrong") }}, {"unknown baseline", func(f *embeddedFixture) { f.agent.cfg.Info.Version = "dev" }}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newEmbeddedFixture(t)
			tc.change(f)
			if _, e := f.agent.PrepareOnce(context.Background()); e == nil {
				t.Fatal("invalid candidate accepted")
			}
			if f.execCalls != 0 {
				t.Fatal("invalid candidate execed")
			}
			if _, e := os.Stat(filepath.Join(f.cfg.StateDir, "state.json")); !os.IsNotExist(e) {
				t.Fatal("invalid candidate changed selection journal")
			}
		})
	}
}

func TestJournalExtensionsAndUnknownSchema(t *testing.T) {
	f := newEmbeddedFixture(t)
	p := f.prepared(t)
	if e := f.agent.saveState(diskState{Stable: &p}); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(f.cfg.StateDir, "state.json")
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var m map[string]any
	json.Unmarshal(raw, &m)
	m["future_observation"] = map[string]any{"a": 1}
	b, _ := json.Marshal(m)
	os.WriteFile(path, b, 0600)
	if e = Bootstrap(context.Background(), f.cfg, f.dep); e != nil {
		t.Fatal("schema1 extension broke old baseline", e)
	}
	m["schema_version"] = 2
	b, _ = json.Marshal(m)
	os.WriteFile(path, b, 0600)
	if e = Bootstrap(context.Background(), f.cfg, f.dep); !errors.Is(e, ErrUnsafeBootstrap) {
		t.Fatalf("unknown schema not fail-closed: %v", e)
	}
}

func cachedPrepared(t *testing.T, f *embeddedFixture, version, commit, body string, id int64) Prepared {
	t.Helper()
	digest := HashBytes([]byte(body))
	m := f.manifest
	m.Tag = version
	m.Version = version
	m.Commit = commit
	m.Asset = Asset{Name: "node-" + version, SHA256: digest, Size: int64(len(body))}
	p := Prepared{Path: filepath.Join(f.cfg.StateDir, "versions", digest, "node"), ReleaseID: id, Tag: version, Version: version, Commit: commit, Digest: digest, Manifest: m}
	if e := os.MkdirAll(filepath.Dir(p.Path), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p.Path, []byte(body), 0700); e != nil {
		t.Fatal(e)
	}
	return p
}

func TestConfirmedStableFailureIsFatalInsteadOfSilentDowngrade(t *testing.T) {
	for _, failure := range []string{"digest", "exec", "permissions"} {
		t.Run(failure, func(t *testing.T) {
			f := newEmbeddedFixture(t)
			p := f.prepared(t)
			if e := f.agent.saveState(diskState{Stable: &p}); e != nil {
				t.Fatal(e)
			}
			switch failure {
			case "digest":
				os.WriteFile(p.Path, []byte("tampered"), 0700)
			case "exec":
				f.dep.Exec = func(string, []string, []string) error { return errors.New("filesystem mounted noexec") }
			case "permissions":
				prior := f.dep.ValidatePaths
				f.dep.ValidatePaths = func(paths ...string) error {
					for _, path := range paths {
						if path == p.Path {
							return os.ErrPermission
						}
					}
					return prior(paths...)
				}
			}
			if e := Bootstrap(context.Background(), f.cfg, f.dep); !errors.Is(e, ErrUnsafeBootstrap) {
				t.Fatalf("stable failure could silently downgrade: %v", e)
			}
		})
	}
}

func TestPendingExecFailureRestoresPreviousStableAtMostOnce(t *testing.T) {
	for _, stableFails := range []bool{false, true} {
		t.Run(fmt.Sprint(stableFails), func(t *testing.T) {
			f := newEmbeddedFixture(t)
			pending := f.prepared(t)
			stable := cachedPrepared(t, f, "v1.0.5", strings.Repeat("c", 40), "confirmed stable", 41)
			if e := f.agent.saveState(diskState{Stable: &stable, HighReleaseID: 42, HighVersion: pending.Version, Pending: &pendingUpdate{Prepared: pending}}); e != nil {
				t.Fatal(e)
			}
			var calls []string
			f.dep.Exec = func(path string, _, _ []string) error {
				if f.lockHeld {
					t.Error("exec under flock")
				}
				calls = append(calls, path)
				if path == pending.Path || stableFails {
					return errors.New("exec failure")
				}
				return nil
			}
			e := Bootstrap(context.Background(), f.cfg, f.dep)
			if stableFails && !errors.Is(e, ErrUnsafeBootstrap) || !stableFails && e != nil {
				t.Fatalf("stable fallback error=%v", e)
			}
			if len(calls) != 2 || calls[0] != pending.Path || calls[1] != stable.Path {
				t.Fatalf("fallback attempts=%v", calls)
			}
			s := f.state(t)
			if s.Pending != nil || s.FailedDigest != pending.Digest || s.HighReleaseID != 42 || s.Stable.Path != stable.Path {
				t.Fatalf("failed candidate not quarantined: %+v", s)
			}
		})
	}
}

func TestManualHigherProtocolBaselineIgnoresUnselectedHistoricalCache(t *testing.T) {
	f := newEmbeddedFixture(t)
	p := f.prepared(t)
	if e := f.agent.saveState(diskState{Stable: &p, Pending: &pendingUpdate{Prepared: p}}); e != nil {
		t.Fatal(e)
	}
	f.cfg.Info.Version = "v2.0.0"
	f.cfg.Info.Commit = strings.Repeat("f", 40)
	f.cfg.Info.ProtocolVersion = 4
	f.cfg.Info.UpgradeSchedule = []protocol.Upgrade{{Version: 4, ActivationHeight: 100, ID: "manual-coordinated"}}
	if e := Bootstrap(context.Background(), f.cfg, f.dep); e != nil {
		t.Fatalf("manual coordinated baseline blocked by old cache: %v", e)
	}
	if f.execCalls != 0 {
		t.Fatal("old cache overrode coordinated baseline")
	}
}
