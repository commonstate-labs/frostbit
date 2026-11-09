package main

import (
	"context"
	"crypto/sha256"
	"dex/config"
	"dex/logs"
	"dex/updater"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const nodeUpdateExitCode = 75

var errNodeUpdateRestart = errors.New("restart node to apply verified GitHub release")

func nodeUpdateConfig(opts runtimeOptions) (*updater.Config, error) {
	if !updater.PlatformSupported() {
		return nil, nil
	}
	configDir, err := filepath.Abs(config.ResolveConfigDir(opts.Env, opts.ConfigDir))
	if err != nil {
		return nil, err
	}
	info, err := readUpgradeInfo(configDir)
	if err != nil {
		return nil, err
	}
	stateDir, err := nodeUpdateDirectory(opts, configDir)
	if err != nil {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cfg := updater.DefaultConfig()
	cfg.Enabled, cfg.StateDir, cfg.ConfigDir = opts.AutoUpdate, stateDir, configDir
	cfg.Executable, cfg.Info = executable, info
	cfg.GHPath, cfg.PollInterval = opts.AutoUpdateGHPath, opts.AutoUpdateInterval
	cfg.RetainGenesisProof = opts.RetainGenesisProof
	for _, fd := range []int{opts.PrivateKeyFD, opts.KeystorePasswordFD, opts.FrostWrapKeyFD, opts.ArbitragePrivateKeyFD} {
		if fd >= 3 {
			cfg.SecretFDs = append(cfg.SecretFDs, fd)
		}
	}
	return &cfg, nil
}

func nodeUpdateDirectory(opts runtimeOptions, configDir string) (string, error) {
	if opts.AutoUpdateDir != "" {
		return filepath.Abs(opts.AutoUpdateDir)
	}
	if opts.DataDir != "" {
		return filepath.Abs(filepath.Join(opts.DataDir, ".node-updates"))
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	identity := fmt.Sprintf("%s\x00%s\x00%s\x00%d", configDir, opts.ExpectedAddress, opts.ListenAddr, opts.BasePort)
	hash := sha256.Sum256([]byte(identity))
	return filepath.Join(cache, "dex", "node-updates", fmt.Sprintf("%x", hash[:16])), nil
}

type nodeUpdater struct {
	ready            chan updater.Prepared
	errors           chan error
	done             chan struct{}
	startupConfirmed chan struct{}
}

func startNodeUpdater(ctx context.Context, cfg *updater.Config, launcher *NodeLauncher) *nodeUpdater {
	u := &nodeUpdater{
		ready: make(chan updater.Prepared, 1), errors: make(chan error, 1),
		done: make(chan struct{}), startupConfirmed: make(chan struct{}),
	}
	if cfg == nil {
		close(u.startupConfirmed)
		close(u.done)
		return u
	}
	go func() {
		defer close(u.done)
		dep := updater.Dependencies{Logf: logs.Info}
		pending, err := updater.PendingConfirmation(ctx, *cfg, dep)
		if err != nil {
			if ctx.Err() == nil {
				u.errors <- fmt.Errorf("updated node confirmation state invalid: %w", err)
			}
			return
		}
		if pending {
			err = waitNodeUpdateHealthy(ctx, func() (uint64, error) { return nodeUpdateHealth(launcher) }, 10*time.Second, 3*time.Minute, time.Second)
			if err == nil {
				err = updater.Confirm(ctx, *cfg, dep)
			}
			if err != nil {
				if ctx.Err() == nil {
					u.errors <- fmt.Errorf("updated node startup confirmation failed: %w", err)
				}
				return
			}
		}
		close(u.startupConfirmed)
		if !cfg.Enabled {
			return
		}
		logs.Info("Automatic GitHub updates enabled in node process: repository=%s interval=%s", updater.Repository, cfg.PollInterval)
		if err := updater.Run(ctx, *cfg, dep, u.ready); err != nil && ctx.Err() == nil {
			logs.Warn("Automatic updates unavailable; node continues running: %v", err)
		}
	}()
	return u
}

func stopAndActivateNodeUpdate(ctx context.Context, launcher *NodeLauncher, cfg *updater.Config, prepared *updater.Prepared) error {
	if err := launcher.Stop(ctx); err != nil {
		return fmt.Errorf("stop node before update/shutdown: %w", err)
	}
	if cfg != nil && prepared != nil {
		if err := updater.Activate(ctx, *cfg, updater.Dependencies{}, *prepared); err != nil {
			return fmt.Errorf("activate verified node update: %w", err)
		}
	}
	return nil
}

type nodeUpdateChain interface {
	GetLastAccepted() (string, uint64)
	IsPaused() bool
	IsSyncing() bool
}

type nodeUpdateState interface {
	GetFinalizedStateHeight() (uint64, bool, error)
	FinalizedStateBlockID(uint64) (string, bool, error)
	GetStateFingerprint() []byte
}

func nodeUpdateHealth(launcher *NodeLauncher) (uint64, error) {
	if !nodeUpdateListenerReady(launcher) {
		return 0, fmt.Errorf("local listener is not ready")
	}
	node := launcher.Instance()
	if node.ConsensusManager == nil || node.DBManager == nil {
		return 0, fmt.Errorf("chain or database is unavailable")
	}
	height, err := readNodeUpdateHealth(node.ConsensusManager, node.DBManager, node.Emergency.IsStopped())
	if err == nil && node.Emergency.IsStopped() {
		return height, fmt.Errorf("chain safety-stopped during health sample")
	}
	return height, err
}

                                                                             
                                                                                
                                                                       
func readNodeUpdateHealth(chain nodeUpdateChain, state nodeUpdateState, halted bool) (uint64, error) {
	id, height := chain.GetLastAccepted()
	if halted || chain.IsPaused() || chain.IsSyncing() {
		return height, fmt.Errorf("chain is paused, syncing or safety-stopped")
	}
	if len(state.GetStateFingerprint()) != sha256.Size {
		return height, fmt.Errorf("state fingerprint is unavailable")
	}
	storedHeight, exists, err := state.GetFinalizedStateHeight()
	if err != nil {
		return height, fmt.Errorf("read finalized height: %w", err)
	}
	if !exists || storedHeight != height || id == "" {
		return height, fmt.Errorf("finalized state height does not match accepted height")
	}
	storedID, exists, err := state.FinalizedStateBlockID(height)
	if err != nil {
		return height, fmt.Errorf("read finalized identity: %w", err)
	}
	if !exists || storedID != id {
		return height, fmt.Errorf("finalized state identity does not match accepted block")
	}
	if nextID, nextHeight := chain.GetLastAccepted(); nextID != id || nextHeight != height {
		return height, fmt.Errorf("finalization changed during health sample")
	}
	if chain.IsPaused() || chain.IsSyncing() {
		return height, fmt.Errorf("chain stopped during health sample")
	}
	return height, nil
}

func nodeUpdateListenerReady(launcher *NodeLauncher) bool {
	if launcher == nil || !launcher.IsReady() || launcher.Instance() == nil || launcher.Instance().Server == nil {
		return false
	}
	address := launcher.Instance().Server.Addr
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func waitNodeUpdateHealthy(ctx context.Context, sample func() (uint64, error), stableFor, timeout, interval time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var since time.Time
	var startHeight uint64
	var lastErr error
	for {
		height, err := sample()
		lastErr = err
		if err == nil {
			if since.IsZero() {
				since = time.Now()
				startHeight = height
			}
			if height < startHeight {
				return fmt.Errorf("accepted height regressed during candidate confirmation")
			}
			if time.Since(since) >= stableFor && height > startHeight {
				return nil
			}
		} else {
			since = time.Time{}
		}
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return fmt.Errorf("%w (last health check: %v)", ctx.Err(), lastErr)
			}
			return fmt.Errorf("%w (candidate did not demonstrate stable chain progress)", ctx.Err())
		case <-ticker.C:
		}
	}
}

func nodeServiceFromCgroup(cgroup string) string {
	for _, line := range strings.Split(cgroup, "\n") {
		fields := strings.SplitN(line, ":", 3)
		if len(fields) != 3 {
			continue
		}
		components := strings.Split(fields[2], "/")
		for i := len(components) - 1; i >= 0; i-- {
			name := components[i]
			if name == "" {
				continue
			}
			if !strings.HasSuffix(name, ".service") {
				break                                                                
			}
			valid := len(name) > len(".service") && name[0] != '-'
			for _, c := range name {
				if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_.@-", c)) {
					valid = false
				}
			}
			if valid {
				return name
			}
			break
		}
	}
	return ""
}

func nodeServiceRestartsForUpdate(properties string, pid int) bool {
	values := make(map[string]string)
	for _, line := range strings.Split(properties, "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			values[key] = value
		}
	}
	if pid <= 0 || values["MainPID"] != strconv.Itoa(pid) {
		return false
	}
	if values["Restart"] != "on-failure" && values["Restart"] != "always" {
		return false
	}
	for _, code := range strings.Fields(values["RestartPreventExitStatus"]) {
		if code == "75" || code == "TEMPFAIL" {
			return false
		}
	}
	if values["Restart"] != "always" {
		for _, code := range strings.Fields(values["SuccessExitStatus"]) {
			if code == "75" || code == "TEMPFAIL" {
				return false
			}
		}
	}
	return true
}

                                                                              
                                                                                   
func nodeUpdateCanRestart(opts runtimeOptions, managed bool) bool {
	return opts.KeystorePath == "" && managed
}
