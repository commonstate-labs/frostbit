package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"

	"dex/config"
)

                                                                           
                                                                            
                                                                       
                                                                               
                   
  
                                                                              
                                                         
func prepareFrostConfigForGenesisCeremony(rawDir string, profile runtimeProfile) error {
	if profile != runtimeProfilePhysical20FrostStress {
		return fmt.Errorf("--prepare-frost-ceremony-dir requires --runtime-profile=%s", runtimeProfilePhysical20FrostStress)
	}
	rawDir = strings.TrimSpace(rawDir)
	if rawDir == "" {
		return fmt.Errorf("--prepare-frost-ceremony-dir must name an existing operator-local network directory")
	}
	dir := filepath.Clean(rawDir)
	if dir == "." || filepath.Dir(dir) == dir {
		return fmt.Errorf("--prepare-frost-ceremony-dir must name a child directory, not a filesystem root")
	}
	dirInfo, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("inspect FROST ceremony directory: %w", err)
	}
	if !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("--prepare-frost-ceremony-dir must be a real directory, not a file or symlink")
	}

	frostPath := filepath.Join(dir, "frost_default.json")
	originalInfo, err := os.Lstat(frostPath)
	if err != nil {
		return fmt.Errorf("inspect public FROST config: %w", err)
	}
	if !originalInfo.Mode().IsRegular() || originalInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("public FROST config must be a regular file, not a symlink")
	}
	original, err := os.ReadFile(frostPath)
	if err != nil {
		return fmt.Errorf("read public FROST config: %w", err)
	}
	current, err := decodeStrictFrostConfig(original)
	if err != nil {
		return fmt.Errorf("parse public FROST config: %w", err)
	}
	expected, err := frostConfigForRuntimeProfile(profile, current.Guardian)
	if err != nil {
		return err
	}

	if err := requireGeneratorGuardian(profile, []config.GuardianConfig{current.Guardian}); err != nil {
		return err
	}
	standalone := frostConfigForStandaloneCeremony(expected)
	                                                                       
	                                                                        
	                                                                         
	                                                                          
	if reflect.DeepEqual(current, standalone) {
		return nil
	}
	if err := validateFrostConfigForRuntimeProfile(current, profile, physical20FrostCommitteeSize); err == nil {
		_, hasResults := frostConfigWithoutCeremonyResults(current)
		if hasResults {
			return nil
		}
	}
	if !reflect.DeepEqual(current, expected) && !isExactLegacyPhysical20FrostProfile(current, expected) {
		return fmt.Errorf("public FROST config is not an exact supported legacy physical20 profile; refusing to change it")
	}

	updated, err := json.MarshalIndent(standalone, "", "  ")
	if err != nil {
		return fmt.Errorf("encode public FROST config: %w", err)
	}
	updated = append(updated, '\n')
	if err := replaceUnchangedRegularFile(frostPath, dir, originalInfo, original, updated); err != nil {
		return err
	}
	return nil
}

func decodeStrictFrostConfig(data []byte) (config.FrostConfig, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var cfg config.FrostConfig
	if err := decoder.Decode(&cfg); err != nil {
		return config.FrostConfig{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return config.FrostConfig{}, fmt.Errorf("unexpected trailing JSON value")
		}
		return config.FrostConfig{}, err
	}
	return cfg, nil
}

func isExactLegacyPhysical20FrostProfile(current, expected config.FrostConfig) bool {
	withPlaceholderRefs := func(cfg config.FrostConfig) config.FrostConfig {
		cfg.Chains = cloneFrostConfigChains(cfg.Chains)
		refs := map[string]string{
			"eth":  "0x000000000000000000000000000000000000E100",
			"bnb":  "0x000000000000000000000000000000000000B100",
			"base": "0x000000000000000000000000000000000000BA00",
			"arb":  "0x000000000000000000000000000000000000A100",
		}
		for chainName, ref := range refs {
			chainCfg := cfg.Chains[chainName]
			chainCfg.VaultRefs = []string{ref}
			cfg.Chains[chainName] = chainCfg
		}
		return cfg
	}

	legacy := withPlaceholderRefs(expected)
	if reflect.DeepEqual(current, legacy) {
		return true
	}

	                                                                        
	                                                                             
	                         
	trx := legacy.Chains["trx"]
	trx.SignAlgo = "SCHNORR_SECP256K1_BIP340"
	trx.FrostVariant = ""
	trx.ThresholdScheme = "gg20 or cggmp (v1 not supported)"
	trx.ChainID = 0
	legacy.Chains["trx"] = trx
	return reflect.DeepEqual(current, legacy)
}

func cloneFrostConfigChains(input map[string]config.ChainFrostConfig) map[string]config.ChainFrostConfig {
	result := make(map[string]config.ChainFrostConfig, len(input))
	for chainName, chainCfg := range input {
		chainCfg.VaultRefs = append([]string(nil), chainCfg.VaultRefs...)
		chainCfg.OnlineDKGVaultRefs = append([]string(nil), chainCfg.OnlineDKGVaultRefs...)
		chainCfg.InitialGroupPubkeys = append([]string(nil), chainCfg.InitialGroupPubkeys...)
		result[chainName] = chainCfg
	}
	return result
}

func replaceUnchangedRegularFile(path, dir string, originalInfo os.FileInfo, original, updated []byte) error {
	temporary, err := os.CreateTemp(dir, ".frost_default.json.genesis-ceremony-*")
	if err != nil {
		return fmt.Errorf("create temporary public FROST config: %w", err)
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
		return fmt.Errorf("set temporary public FROST config permissions: %w", err)
	}
	if _, err := temporary.Write(updated); err != nil {
		return fmt.Errorf("write temporary public FROST config: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary public FROST config: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary public FROST config: %w", err)
	}
	currentInfo, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("reinspect public FROST config: %w", err)
	}
	if !os.SameFile(originalInfo, currentInfo) || !currentInfo.Mode().IsRegular() || currentInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("public FROST config changed while preparing ceremony; refusing to replace it")
	}
	current, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reread public FROST config: %w", err)
	}
	if !bytes.Equal(current, original) {
		return fmt.Errorf("public FROST config contents changed while preparing ceremony; refusing to replace it")
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("atomically replace public FROST config: %w", err)
	}
	committed = true
	if err := syncFrostProfileDirectory(dir); err != nil {
		return fmt.Errorf("sync public FROST config directory: %w", err)
	}
	return nil
}

func syncFrostProfileDirectory(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
