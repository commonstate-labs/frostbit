package main

import (
	"context"
	"dex/db"
	"dex/logs"
	"dex/updater"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAutoUpdateDefaultsAndDisableFlag(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "disabled"}[disabled], func(t *testing.T) {
			previousFlags, previousArgs := flag.CommandLine, os.Args
			t.Cleanup(func() { flag.CommandLine, os.Args = previousFlags, previousArgs })
			flag.CommandLine = flag.NewFlagSet("node-auto-update", flag.ContinueOnError)
			os.Args = []string{"node"}
			if disabled {
				os.Args = append(os.Args, "--auto-update=false")
			}
			opts := parseRuntimeOptions()
			if opts.AutoUpdate == disabled || opts.AutoUpdateInterval != 10*time.Minute || opts.AutoUpdateGHPath != "/usr/bin/gh" {
				t.Fatalf("unexpected default/disabled options: %+v", opts)
			}
		})
	}
}

func TestUpdateCacheIsInsideExplicitDataDirectory(t *testing.T) {
	dir := t.TempDir()
	got, err := nodeUpdateDirectory(runtimeOptions{DataDir: dir}, filepath.Join(dir, "config"))
	if err != nil || got != filepath.Join(dir, ".node-updates") {
		t.Fatalf("cache escaped node data directory: %q, %v", got, err)
	}
	if _, err := os.Stat(got); !os.IsNotExist(err) {
		t.Fatalf("resolving cache unexpectedly created data: %v", err)
	}
	a, err := nodeUpdateDirectory(runtimeOptions{BasePort: 19000}, dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := nodeUpdateDirectory(runtimeOptions{BasePort: 19001}, dir)
	if err != nil || a == b {
		t.Fatalf("different local node instances share update state: %q, %q, %v", a, b, err)
	}
}

func TestAutoUpdateRequiresAnActualListener(t *testing.T) {
	ready := make(chan struct{})
	close(ready)
	launcher := &NodeLauncher{httpReady: ready, instance: &NodeInstance{Server: &http.Server{Addr: "bad-address"}}}
	if nodeUpdateListenerReady(launcher) {
		t.Fatal("the legacy readiness timeout alone confirmed a candidate")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	launcher.instance.Server.Addr = listener.Addr().String()
	if !nodeUpdateListenerReady(launcher) {
		t.Fatal("ready node with a bound listener was rejected")
	}
}

func TestUpdateConfirmationWaitsForStableReadiness(t *testing.T) {
	calls := 0
	if err := waitNodeUpdateHealthy(context.Background(), func() (uint64, error) {
		calls++
		if calls <= 3 {
			return 0, errors.New("not ready")
		}
		return uint64(calls), nil
	}, 3*time.Millisecond, time.Second, time.Millisecond); err != nil || calls <= 3 {
		t.Fatalf("readiness did not recover: calls=%d, err=%v", calls, err)
	}
	err := waitNodeUpdateHealthy(context.Background(), func() (uint64, error) { return 0, errors.New("not ready") }, time.Millisecond, 5*time.Millisecond, time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unready candidate did not time out: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitNodeUpdateHealthy(ctx, func() (uint64, error) { return 1, nil }, time.Second, time.Second, time.Millisecond); !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown did not cancel candidate confirmation: %v", err)
	}
}

type updateHealthFixture struct {
	height, storedHeight                                 uint64
	id, storedID                                         string
	paused, syncing                                      bool
	heightErr, identityErr                               error
	root                                                 []byte
	acceptedReads, heightReads, identityReads, rootReads int
	changeAccepted                                       bool
}

func (f *updateHealthFixture) GetLastAccepted() (string, uint64) {
	f.acceptedReads++
	if f.changeAccepted && f.acceptedReads%2 == 0 {
		return "next", f.height + 1
	}
	return f.id, f.height
}
func (f *updateHealthFixture) IsPaused() bool  { return f.paused }
func (f *updateHealthFixture) IsSyncing() bool { return f.syncing }
func (f *updateHealthFixture) GetFinalizedStateHeight() (uint64, bool, error) {
	f.heightReads++
	return f.storedHeight, f.storedHeight != 0, f.heightErr
}
func (f *updateHealthFixture) FinalizedStateBlockID(uint64) (string, bool, error) {
	f.identityReads++
	return f.storedID, f.storedID != "", f.identityErr
}
func (f *updateHealthFixture) GetStateFingerprint() []byte { f.rootReads++; return f.root }
func healthyUpdateFixture() *updateHealthFixture {
	return &updateHealthFixture{height: 12, storedHeight: 12, id: "block-12", storedID: "block-12", root: make([]byte, 32)}
}

func TestUpdateHealthRejectsUnhealthyChainDespiteListener(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*updateHealthFixture)
		halted bool
	}{
		{"healthy", func(*updateHealthFixture) {}, false},
		{"syncing", func(f *updateHealthFixture) { f.syncing = true }, false},
		{"paused", func(f *updateHealthFixture) { f.paused = true }, false},
		{"safety-stop", func(*updateHealthFixture) {}, true},
		{"uncommitted-height", func(f *updateHealthFixture) { f.storedHeight-- }, false},
		{"wrong-block", func(f *updateHealthFixture) { f.storedID = "other" }, false},
		{"missing-identity", func(f *updateHealthFixture) { f.storedID = "" }, false},
		{"height-read-error", func(f *updateHealthFixture) { f.heightErr = errors.New("injected height read") }, false},
		{"identity-read-error", func(f *updateHealthFixture) { f.identityErr = errors.New("injected identity read") }, false},
		{"missing-root", func(f *updateHealthFixture) { f.root = nil }, false},
		{"commit-race", func(f *updateHealthFixture) { f.changeAccepted = true }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := healthyUpdateFixture()
			tc.mutate(f)
			_, err := readNodeUpdateHealth(f, f, tc.halted)
			if (err == nil) != (tc.name == "healthy") {
				t.Fatalf("health error=%v", err)
			}
		})
	}
}

func TestUpdateConfirmationRequiresProgressAndResetsHealthWindow(t *testing.T) {
	if err := waitNodeUpdateHealthy(context.Background(), func() (uint64, error) { return 100, nil }, time.Millisecond, 8*time.Millisecond, time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("frozen chain confirmed from historical height: %v", err)
	}
	calls, firstRecovered := 0, 0
	err := waitNodeUpdateHealthy(context.Background(), func() (uint64, error) {
		calls++
		if calls == 3 {
			return uint64(calls), errors.New("sync resumed")
		}
		if calls == 4 {
			firstRecovered = calls
		}
		return uint64(calls), nil
	}, 5*time.Millisecond, time.Second, time.Millisecond)
	if err != nil || firstRecovered != 4 || calls < firstRecovered+3 {
		t.Fatalf("health window was not reset: calls=%d first=%d err=%v", calls, firstRecovered, err)
	}
}

func TestUpdateHealthLocal20ReadBound(t *testing.T) {
	var accepted, height, identity, root int
	for i := 0; i < 20; i++ {
		f := healthyUpdateFixture()
		if _, err := readNodeUpdateHealth(f, f, false); err != nil {
			t.Fatal(err)
		}
		accepted += f.acceptedReads
		height += f.heightReads
		identity += f.identityReads
		root += f.rootReads
	}
	if accepted != 40 || height != 20 || identity != 20 || root != 20 {
		t.Fatalf("unbounded health work: accepted=%d height=%d identity=%d root=%d", accepted, height, identity, root)
	}
	t.Log("20 candidate samples: 40 metadata point reads, 20 in-memory root aggregates, no state scan or write")
}

func TestUpdateHealthChecksRealFinalizedState(t *testing.T) {
	manager, err := db.NewManager(t.TempDir(), logs.NewNodeLogger("update-health", 0))
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	chain := healthyUpdateFixture()
	if _, err := readNodeUpdateHealth(chain, manager, false); err == nil {
		t.Fatal("missing durable state was healthy")
	}
	if _, err := manager.SyncFinalizedStateUpdatesForBlock(12, chain.id, nil, false); err != nil {
		t.Fatal(err)
	}
	if _, err := readNodeUpdateHealth(chain, manager, false); err != nil {
		t.Fatalf("persisted StateDB was not healthy: %v", err)
	}
	chain.height, chain.id = 13, "block-13"
	if _, err := readNodeUpdateHealth(chain, manager, false); err == nil {
		t.Fatal("accepted height ahead of durable StateDB was healthy")
	}
	if _, err := manager.SyncFinalizedStateUpdatesForBlock(13, chain.id, nil, false); err != nil {
		t.Fatal(err)
	}
	if _, err := readNodeUpdateHealth(chain, manager, false); err != nil {
		t.Fatalf("durable state did not recover: %v", err)
	}
}

func TestInvalidCandidateConfirmationStateExitsEvenWhenDownloadsDisabled(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			cfg := updater.DefaultConfig()
			cfg.Enabled, cfg.StateDir = enabled, t.TempDir()
			if err := os.WriteFile(filepath.Join(cfg.StateDir, "state.json"), []byte("invalid pending state"), 0600); err != nil {
				t.Fatal(err)
			}
			                                                                    
			                                                                       
			u := startNodeUpdater(context.Background(), &cfg, nil)
			select {
			case err := <-u.errors:
				if err == nil || !strings.Contains(err.Error(), "confirmation state invalid") {
					t.Fatalf("invalid candidate metadata was not fatal: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("candidate metadata error was only logged")
			}
			<-u.done
		})
	}
	cfg := updater.DefaultConfig()
	cfg.Enabled = false
	cfg.StateDir = filepath.Join(t.TempDir(), "absent")
	u := startNodeUpdater(context.Background(), &cfg, nil)
	select {
	case <-u.done:
	case <-time.After(time.Second):
		t.Fatal("disabled fresh updater did not stop")
	}
	select {
	case err := <-u.errors:
		t.Fatalf("fresh disabled updater failed: %v", err)
	default:
	}
	if _, err := os.Stat(cfg.StateDir); !os.IsNotExist(err) {
		t.Fatal("disabled fresh updater created a cache")
	}
}

func TestDatabaseCloseErrorPreventsUpdateActivationAndSurvivesRepeatedStop(t *testing.T) {
	manager, err := db.NewManager(t.TempDir(), logs.NewNodeLogger("update-close", 0))
	if err != nil {
		t.Fatal(err)
	}
	iter, err := manager.Db.NewIter(nil)
	if err != nil {
		t.Fatal(err)
	}
	launcher := &NodeLauncher{instance: &NodeInstance{DBManager: manager, Logger: logs.NewNodeLogger("update-close", 0)}}
	stateDir := filepath.Join(t.TempDir(), "updates")
	cfg := updater.DefaultConfig()
	cfg.StateDir = stateDir
	                                                                         
	err = stopAndActivateNodeUpdate(context.Background(), launcher, &cfg, &updater.Prepared{})
	if err == nil || !strings.Contains(err.Error(), "close node database") {
		t.Fatalf("database failure did not stop activation: %v", err)
	}
	if second := launcher.Stop(context.Background()); second == nil || second.Error() != launcher.stopErr.Error() {
		t.Fatalf("repeated stop forgot failure: %v", second)
	}
	if _, statErr := os.Stat(stateDir); !os.IsNotExist(statErr) {
		t.Fatalf("failed shutdown wrote update state: %v", statErr)
	}
	_ = iter.Close()
}

func TestAutoUpdateOnlyExitsWithVerifiedRestartPolicy(t *testing.T) {
	for _, tc := range []struct {
		properties string
		want       bool
	}{
		{"Restart=on-failure\nRestartPreventExitStatus=\nSuccessExitStatus=\n", true},
		{"Restart=no\n", false},
		{"Restart=on-abnormal\n", false},
		{"Restart=always\nSuccessExitStatus=75\n", true},
		{"Restart=on-failure\nSuccessExitStatus=0 75\n", false},
		{"Restart=always\nRestartPreventExitStatus=TEMPFAIL\n", false},
		{"Restart=on-failure\nRestartPreventExitStatus=75\n", false},
		{"", false},
	} {
		if got := nodeServiceRestartsForUpdate(tc.properties+"MainPID=12345\n", 12345); got != tc.want {
			t.Errorf("restart policy %q: got %t, want %t", tc.properties, got, tc.want)
		}
	}
	if nodeServiceRestartsForUpdate("Restart=on-failure\nMainPID=1\n", 12345) {
		t.Fatal("a parent service's restart policy authorized exiting a child process")
	}
	for _, tc := range []struct{ cgroup, want string }{
		{"0::/system.slice/dex-server5.service\n", "dex-server5.service"},
		{"2:cpu:/system.slice/dex-server5.service\n", "dex-server5.service"},
		{"0::/docker/abc123.scope\n", ""},
		{"0::/user.slice/user@1000.service/app.slice/manual-node.scope\n", ""},
		{"0::/unsafe;command.service\n", ""},
	} {
		if got := nodeServiceFromCgroup(tc.cgroup); got != tc.want {
			t.Errorf("unit lookup %q: got %q, want %q", tc.cgroup, got, tc.want)
		}
	}
}

func TestPreparedUpdateKeepsManualKeystoreNodeRunning(t *testing.T) {
	for _, fd := range []int{-1, 3} {
		opts := runtimeOptions{KeystorePath: "miner.json", KeystorePasswordFD: fd}
		if nodeUpdateCanRestart(opts, true) {
			t.Fatal("prepared update would terminate a manually unlocked node")
		}
	}
	if !nodeUpdateCanRestart(runtimeOptions{}, true) {
		t.Fatal("existing managed FD startup no longer restarts")
	}
	if nodeUpdateCanRestart(runtimeOptions{}, false) {
		t.Fatal("unmanaged node may not restart automatically")
	}
}
