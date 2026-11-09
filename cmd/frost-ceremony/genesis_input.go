package main

import (
	"fmt"
	"path/filepath"

	"dex/config"
)

                                                                              
                                                              
func validateFreshGenesisInput(genesisPath, peersPath string, frost config.FrostConfig, peers *config.PeerManifest) error {
	if genesisPath == "" {
		genesisPath = filepath.Join(filepath.Dir(peersPath), "genesis.json")
	}
	data, err := readRegularFile(genesisPath, maxLargeJSONBytes)
	if err != nil {
		return fmt.Errorf("read public genesis %s: %w", genesisPath, err)
	}
	var genesis *config.GenesisConfig
	if err := strictJSON(data, &genesis); err != nil {
		return fmt.Errorf("invalid public genesis: %w", err)
	}
	if err := config.ValidateGenesisConfig(genesis); err != nil {
		return fmt.Errorf("invalid public genesis: %w", err)
	}
	if err := config.ValidateFreshFrostGenesisStakePolicy(frost, peers, genesis); err != nil {
		return fmt.Errorf("fresh genesis committee policy: %w", err)
	}
	return nil
}
