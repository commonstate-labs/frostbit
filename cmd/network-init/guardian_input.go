package main

import (
	"fmt"
	"os"
	"strings"

	"dex/config"
)

                                                                             
                                                                                
func loadGeneratorGuardian(path string, profile runtimeProfile) (config.GuardianConfig, error) {
	if strings.TrimSpace(path) == "" {
		if profile == runtimeProfilePhysical20FrostStress {
			return config.GuardianConfig{}, fmt.Errorf("--frost-config must supply the public guardian.btcPublicKey and guardian.accountPublicKey")
		}
		return config.GuardianConfig{}, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return config.GuardianConfig{}, fmt.Errorf("read --frost-config: %w", err)
	}
	cfg, err := decodeStrictFrostConfig(raw)
	if err != nil {
		return config.GuardianConfig{}, fmt.Errorf("decode public --frost-config: %w", err)
	}
	if err := requireGeneratorGuardian(runtimeProfilePhysical20FrostStress, []config.GuardianConfig{cfg.Guardian}); err != nil {
		return config.GuardianConfig{}, err
	}
	return cfg.Guardian, nil
}

func requireGeneratorGuardian(profile runtimeProfile, input []config.GuardianConfig) error {
	if profile != runtimeProfilePhysical20FrostStress {
		return nil
	}
	if len(input) != 1 {
		return fmt.Errorf("--frost-config must supply the fixed public guardian keys")
	}
	cfg := config.DefaultFrostConfig()
	cfg.Enabled, cfg.Guardian = true, input[0]
	if err := config.ValidateFrostGuardianConfig(cfg); err != nil {
		return fmt.Errorf("invalid --frost-config guardian: %w", err)
	}
	return nil
}
