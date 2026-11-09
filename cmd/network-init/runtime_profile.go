package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"dex/config"
)

type runtimeProfile string

const (
	runtimeProfileGeneric               runtimeProfile = "generic"
	runtimeProfilePhysical20Stress      runtimeProfile = "physical20-stress"
	runtimeProfilePhysical20FrostStress runtimeProfile = "physical20-frost-stress"
)

                                                                            
                                                                               
                                                            
type runtimeTxPoolProfileConfig struct {
	PendingTxCacheSize      int `json:"PendingTxCacheSize"`
	ShortPendingTxCacheSize int `json:"ShortPendingTxCacheSize"`
	CacheTxSize             int `json:"CacheTxSize"`
	ShortTxCacheSize        int `json:"ShortTxCacheSize"`
	MessageQueueSize        int `json:"MessageQueueSize"`
	MaxPendingTxs           int `json:"MaxPendingTxs"`
	TargetTxsPerBlock       int `json:"TargetTxsPerBlock"`
	MaxTxsPerBlock          int `json:"MaxTxsPerBlock"`
	MaxTxsLimitPerBlock     int `json:"MaxTxsLimitPerBlock"`
}

type runtimeSenderProfileConfig struct {
	QueueCapacity             int `json:"QueueCapacity"`
	TxGossipFanout            int `json:"TxGossipFanout"`
	TxGossipInputCapacity     int `json:"TxGossipInputCapacity"`
	TxGossipMaxPendingPerPeer int `json:"TxGossipMaxPendingPerPeer"`
}

type runtimePeerGateProfileConfig struct {
	TrustedRPS         float64 `json:"trustedRPS"`
	TrustedBurst       int     `json:"trustedBurst"`
	SyncRPS            float64 `json:"syncRPS"`
	SyncBurst          int     `json:"syncBurst"`
	UnknownRPS         float64 `json:"unknownRPS"`
	UnknownBurst       int     `json:"unknownBurst"`
	BanThreshold       int     `json:"banThreshold"`
	TxBatchMaxInflight int     `json:"txBatchMaxInflight"`
}

type runtimeProfileOverrides struct {
	TxPool   *runtimeTxPoolProfileConfig
	Sender   *runtimeSenderProfileConfig
	PeerGate *runtimePeerGateProfileConfig
}

func parseRuntimeProfile(raw string) (runtimeProfile, error) {
	profile := runtimeProfile(strings.TrimSpace(raw))
	switch profile {
	case runtimeProfileGeneric, runtimeProfilePhysical20Stress, runtimeProfilePhysical20FrostStress:
		return profile, nil
	default:
		return "", fmt.Errorf("--runtime-profile must be %q, %q, or %q", runtimeProfileGeneric, runtimeProfilePhysical20Stress, runtimeProfilePhysical20FrostStress)
	}
}

func overridesForRuntimeProfile(profile runtimeProfile) (runtimeProfileOverrides, error) {
	switch profile {
	case runtimeProfileGeneric:
		return runtimeProfileOverrides{}, nil
	case runtimeProfilePhysical20Stress, runtimeProfilePhysical20FrostStress:
		return runtimeProfileOverrides{
			TxPool: &runtimeTxPoolProfileConfig{
				PendingTxCacheSize:      50000,
				ShortPendingTxCacheSize: 50000,
				CacheTxSize:             50000,
				ShortTxCacheSize:        50000,
				MessageQueueSize:        50000,
				MaxPendingTxs:           30000,
				TargetTxsPerBlock:       5000,
				MaxTxsPerBlock:          5000,
				MaxTxsLimitPerBlock:     5000,
			},
			Sender: &runtimeSenderProfileConfig{
				QueueCapacity:             5000,
				TxGossipFanout:            20,
				TxGossipInputCapacity:     50000,
				TxGossipMaxPendingPerPeer: 20000,
			},
			PeerGate: &runtimePeerGateProfileConfig{
				TrustedRPS:         5000,
				TrustedBurst:       10000,
				SyncRPS:            5000,
				SyncBurst:          10000,
				UnknownRPS:         5000,
				UnknownBurst:       10000,
				BanThreshold:       100000,
				TxBatchMaxInflight: 64,
			},
		}, nil
	default:
		return runtimeProfileOverrides{}, fmt.Errorf("unsupported runtime profile %q", profile)
	}
}

func applyRuntimeProfileToDocument(document *runtimeConfigDocument, profile runtimeProfile) error {
	overrides, err := overridesForRuntimeProfile(profile)
	if err != nil {
		return err
	}
	document.TxPool = overrides.TxPool
	document.Sender = overrides.Sender
	document.PeerGate = overrides.PeerGate
	return nil
}

                                                                               
                                                                                
func witnessConfigForRuntimeProfile(profile runtimeProfile) (config.WitnessConfig, error) {
	cfg := config.DefaultWitnessConfig()
	cfg.Enabled = false
	switch profile {
	case runtimeProfileGeneric, runtimeProfilePhysical20Stress:
		return cfg, nil
	case runtimeProfilePhysical20FrostStress:
		cfg.ArbitrationTimingProfile = "local20"
		cfg.ArbitrationPeriodBlocks = 20
		return cfg, nil
	default:
		return config.WitnessConfig{}, fmt.Errorf("unsupported runtime profile %q", profile)
	}
}

func validateWitnessConfigForRuntimeProfile(cfg config.WitnessConfig, profile runtimeProfile) error {
	expected, err := witnessConfigForRuntimeProfile(profile)
	if err != nil {
		return err
	}
	timingProfile := cfg.ArbitrationTimingProfile
	if timingProfile == "prod" {
		timingProfile = ""
	}
	if timingProfile != expected.ArbitrationTimingProfile || cfg.ArbitrationPeriodBlocks != expected.ArbitrationPeriodBlocks {
		return fmt.Errorf("runtime profile %q Witness arbitration timing must use profile %q and %d blocks", profile, expected.ArbitrationTimingProfile, expected.ArbitrationPeriodBlocks)
	}
	return nil
}

func validateRuntimeProfileConfig(cfg *config.Config, profile runtimeProfile) error {
	overrides, err := overridesForRuntimeProfile(profile)
	if err != nil {
		return err
	}
	                                                                       
	                                                                           
	if profile == runtimeProfileGeneric {
		return nil
	}
	if overrides.TxPool == nil || overrides.Sender == nil || overrides.PeerGate == nil {
		return fmt.Errorf("runtime profile %q has incomplete overrides", profile)
	}
	if cfg.TxPool.PendingTxCacheSize != overrides.TxPool.PendingTxCacheSize ||
		cfg.TxPool.ShortPendingTxCacheSize != overrides.TxPool.ShortPendingTxCacheSize ||
		cfg.TxPool.CacheTxSize != overrides.TxPool.CacheTxSize ||
		cfg.TxPool.ShortTxCacheSize != overrides.TxPool.ShortTxCacheSize ||
		cfg.TxPool.MessageQueueSize != overrides.TxPool.MessageQueueSize ||
		cfg.TxPool.MaxPendingTxs != overrides.TxPool.MaxPendingTxs ||
		cfg.TxPool.TargetTxsPerBlock != overrides.TxPool.TargetTxsPerBlock ||
		cfg.TxPool.MaxTxsPerBlock != overrides.TxPool.MaxTxsPerBlock ||
		cfg.TxPool.MaxTxsLimitPerBlock != overrides.TxPool.MaxTxsLimitPerBlock {
		return fmt.Errorf("runtime profile %q TxPool settings do not match", profile)
	}
	if cfg.Sender.QueueCapacity != overrides.Sender.QueueCapacity ||
		cfg.Sender.TxGossipFanout != overrides.Sender.TxGossipFanout ||
		cfg.Sender.TxGossipInputCapacity != overrides.Sender.TxGossipInputCapacity ||
		cfg.Sender.TxGossipMaxPendingPerPeer != overrides.Sender.TxGossipMaxPendingPerPeer {
		return fmt.Errorf("runtime profile %q Sender settings do not match", profile)
	}
	if cfg.PeerGate.TrustedRPS != overrides.PeerGate.TrustedRPS ||
		cfg.PeerGate.TrustedBurst != overrides.PeerGate.TrustedBurst ||
		cfg.PeerGate.SyncRPS != overrides.PeerGate.SyncRPS ||
		cfg.PeerGate.SyncBurst != overrides.PeerGate.SyncBurst ||
		cfg.PeerGate.UnknownRPS != overrides.PeerGate.UnknownRPS ||
		cfg.PeerGate.UnknownBurst != overrides.PeerGate.UnknownBurst ||
		cfg.PeerGate.BanThreshold != overrides.PeerGate.BanThreshold ||
		cfg.PeerGate.TxBatchMaxInflight != overrides.PeerGate.TxBatchMaxInflight {
		return fmt.Errorf("runtime profile %q PeerGate settings do not match", profile)
	}
	if cfg.Server.EnablePprof {
		return fmt.Errorf("runtime profile %q requires pprof to remain disabled", profile)
	}
	if len(cfg.Discovery.BootstrapPeers) != 0 {
		return fmt.Errorf("runtime profile %q requires bootstrap routing to remain in peers.json", profile)
	}
	return nil
}

                                                                              
                                                                
func applyRuntimeProfileToExistingDirectory(rawDir string, profile runtimeProfile) error {
	if profile == runtimeProfileGeneric {
		return fmt.Errorf("--apply-runtime-profile-dir requires an explicit non-generic --runtime-profile")
	}
	if profile == runtimeProfilePhysical20FrostStress {
		return fmt.Errorf("runtime profile %q requires a fresh network directory because FROST bootstrap state is created at genesis", profile)
	}
	if _, err := overridesForRuntimeProfile(profile); err != nil {
		return err
	}
	rawDir = strings.TrimSpace(rawDir)
	if rawDir == "" {
		return fmt.Errorf("--apply-runtime-profile-dir must name an existing operator-local network directory")
	}
	dir := filepath.Clean(rawDir)
	if dir == "." || filepath.Dir(dir) == dir {
		return fmt.Errorf("--apply-runtime-profile-dir must name a child directory, not a filesystem root")
	}
	dirInfo, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("inspect runtime profile directory: %w", err)
	}
	if !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("--apply-runtime-profile-dir must be a real directory, not a file or symlink")
	}

	configPath := filepath.Join(dir, "config.json")
	originalInfo, err := os.Lstat(configPath)
	if err != nil {
		return fmt.Errorf("inspect public runtime config: %w", err)
	}
	if !originalInfo.Mode().IsRegular() || originalInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("public runtime config must be a regular file, not a symlink")
	}
	original, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read public runtime config: %w", err)
	}
	document, err := decodeRuntimeConfigObject(original)
	if err != nil {
		return fmt.Errorf("parse public runtime config: %w", err)
	}
	if err := mergeRuntimeProfileOverrides(document, profile); err != nil {
		return err
	}
	updated, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("encode public runtime config: %w", err)
	}
	updated = append(updated, '\n')
	if err := validateRuntimeConfigBytes(updated, profile); err != nil {
		return err
	}
	if bytes.Equal(original, updated) {
		return nil
	}

	temporary, err := os.CreateTemp(dir, ".config.json.runtime-profile-*")
	if err != nil {
		return fmt.Errorf("create temporary public runtime config: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	mode := originalInfo.Mode().Perm()
	if mode == 0 {
		mode = 0o644
	}
	if err := temporary.Chmod(mode); err != nil {
		return fmt.Errorf("set temporary public runtime config permissions: %w", err)
	}
	if _, err := temporary.Write(updated); err != nil {
		return fmt.Errorf("write temporary public runtime config: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary public runtime config: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary public runtime config: %w", err)
	}
	currentInfo, err := os.Lstat(configPath)
	if err != nil {
		return fmt.Errorf("reinspect public runtime config: %w", err)
	}
	if !os.SameFile(originalInfo, currentInfo) || !currentInfo.Mode().IsRegular() || currentInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("public runtime config changed while applying runtime profile; refusing to replace it")
	}
	current, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("reread public runtime config: %w", err)
	}
	if !bytes.Equal(current, original) {
		return fmt.Errorf("public runtime config contents changed while applying runtime profile; refusing to replace it")
	}
	if err := os.Rename(temporaryPath, configPath); err != nil {
		return fmt.Errorf("atomically replace public runtime config: %w", err)
	}
	committed = true
	return nil
}

func decodeRuntimeConfigObject(data []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var document map[string]json.RawMessage
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	if document == nil {
		return nil, fmt.Errorf("top-level config must be a JSON object")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("unexpected trailing JSON value")
		}
		return nil, err
	}
	return document, nil
}

func mergeRuntimeProfileOverrides(document map[string]json.RawMessage, profile runtimeProfile) error {
	overrides, err := overridesForRuntimeProfile(profile)
	if err != nil {
		return err
	}
	for _, section := range []struct {
		name  string
		value any
	}{
		{name: "TxPool", value: overrides.TxPool},
		{name: "Sender", value: overrides.Sender},
		{name: "PeerGate", value: overrides.PeerGate},
	} {
		if section.value == nil {
			continue
		}
		if err := mergeRuntimeConfigSection(document, section.name, section.value); err != nil {
			return err
		}
	}
	return nil
}

func mergeRuntimeConfigSection(document map[string]json.RawMessage, canonicalName string, overrides any) error {
	existingName := ""
	for name := range document {
		if strings.EqualFold(name, canonicalName) {
			if existingName != "" && existingName != name {
				return fmt.Errorf("public runtime config contains ambiguous %s sections", canonicalName)
			}
			existingName = name
		}
	}
	merged := make(map[string]json.RawMessage)
	if existingName != "" {
		if err := json.Unmarshal(document[existingName], &merged); err != nil || merged == nil {
			return fmt.Errorf("public runtime config %s section must be a JSON object", canonicalName)
		}
		delete(document, existingName)
	}
	overrideBytes, err := json.Marshal(overrides)
	if err != nil {
		return fmt.Errorf("encode %s profile overrides: %w", canonicalName, err)
	}
	var overrideFields map[string]json.RawMessage
	if err := json.Unmarshal(overrideBytes, &overrideFields); err != nil {
		return fmt.Errorf("decode %s profile overrides: %w", canonicalName, err)
	}
	for name, value := range overrideFields {
		merged[name] = value
	}
	sectionBytes, err := json.Marshal(merged)
	if err != nil {
		return fmt.Errorf("encode merged %s section: %w", canonicalName, err)
	}
	document[canonicalName] = sectionBytes
	return nil
}

func validateRuntimeConfigBytes(data []byte, profile runtimeProfile) error {
	cfg := config.DefaultConfig()
	if err := json.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("validate updated public runtime config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("validate updated public runtime config: %w", err)
	}
	if err := validateRuntimeProfileConfig(cfg, profile); err != nil {
		return fmt.Errorf("validate updated public runtime config: %w", err)
	}
	return nil
}
