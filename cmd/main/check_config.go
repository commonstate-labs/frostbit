package main

import (
	"dex/config"
	"dex/frost/ceremony"
	"fmt"
	"os"
)

                                                                               
                                                                               
func checkConfigDirectory(dir string) error {
	return checkConfigDirectoryWithRetainedProof(dir, false)
}

func checkConfigDirectoryWithRetainedProof(dir string, retain bool) error {
	for _, name := range []string{"config.json", "genesis.json", "frost_default.json", "witness_default.json", "peers.json"} {
		info, err := os.Stat(config.ConfigPath(dir, name))
		if err != nil {
			return fmt.Errorf("check config %s: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("check config %s: expected a regular file", name)
		}
	}
	cfg, err := config.LoadFromDir(dir)
	if err != nil {
		return fmt.Errorf("check config from %s: %w", dir, err)
	}
	genesis, err := config.LoadGenesisConfig(config.ConfigPath(dir, "genesis.json"))
	if err != nil {
		return fmt.Errorf("check genesis config from %s: %w", dir, err)
	}
	manifest, peers, err := loadPeerManifestInfo(config.ConfigPath(dir, "peers.json"))
	if err != nil {
		return fmt.Errorf("check peers manifest from %s: %w", dir, err)
	}
	if err := validatePeerManifestChainID(manifest, genesis.EffectiveChainID()); err != nil {
		return err
	}
	if len(peers) == 0 || countMinerPeers(peers) == 0 {
		return fmt.Errorf("public peer manifest contains no genesis validators")
	}
	if err := validateFrostStartupMinerSet(peers, cfg.Frost); err != nil {
		return fmt.Errorf("FROST startup validation failed: %w", err)
	}
	if err := config.ValidateFreshFrostGenesisStakePolicy(cfg.Frost, manifest, genesis); err != nil {
		return fmt.Errorf("FROST genesis ranking validation failed: %w", err)
	}
	peerManifestDigest, err := ceremony.ComputePeerManifestDigest(manifest)
	if err != nil {
		return fmt.Errorf("bind FROST ceremony to peer manifest: %w", err)
	}
	if _, err := validateFrostGenesisDeploymentProofMode(peers, cfg.Frost, genesis.EffectiveChainID(), peerManifestDigest, retain); err != nil {
		return fmt.Errorf("FROST genesis deployment proof validation failed: %w", err)
	}
	if retain {
		fmt.Println("Original signed genesis proof validated; the finalized database and exact genesis marker must still be verified at startup. No database was opened.")
	} else {
		fmt.Println("Configuration files validated; no node was started.")
	}
	return nil
}
