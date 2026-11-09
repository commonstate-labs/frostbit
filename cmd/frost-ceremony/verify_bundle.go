package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"

	"dex/config"
	"dex/frost/ceremony"
	"dex/utils"
)

func runVerifyBundle(args []string) error {
	fs := flag.NewFlagSet("verify-bundle", flag.ContinueOnError)
	var bundleDir, frostConfigPath, peersPath, nodeID string
	var wrapKeyFD int
	var plaintextGenesisShares bool
	fs.StringVar(&bundleDir, "bundle", "", "existing node-specific genesis bundle directory")
	fs.StringVar(&frostConfigPath, "frost-config", "", "final proof-embedded frost_default.json")
	fs.StringVar(&peersPath, "peers", "", "the exact peers.json bound into the ceremony")
	fs.StringVar(&nodeID, "node-id", "", "the node's full canonical DEX address")
	fs.IntVar(&wrapKeyFD, "wrap-key-fd", -1, "optional inherited FD >= 3 containing the node's 32-byte hex wrap key")
	fs.BoolVar(&plaintextGenesisShares, "plaintext-genesis-shares", false, "explicit development mode: fully verify unencrypted genesis shares for DEX chain 51025")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if bundleDir == "" || frostConfigPath == "" || peersPath == "" || nodeID == "" {
		return errors.New("verify-bundle requires --bundle, --frost-config, --peers, and --node-id")
	}
	if wrapKeyFD != -1 && wrapKeyFD < 3 {
		return errors.New("verify-bundle --wrap-key-fd must be an inherited descriptor >= 3")
	}
	if plaintextGenesisShares && wrapKeyFD != -1 {
		return errors.New("plaintext genesis verification rejects --wrap-key-fd")
	}
	frostConfig, err := loadFrostConfigStrict(frostConfigPath)
	if err != nil {
		return err
	}
	if !frostConfig.Enabled || len(frostConfig.GenesisDeploymentManifest) == 0 {
		return errors.New("verify-bundle requires an enabled final FROST config with a signed deployment proof")
	}
	peers, err := loadPeerManifestStrict(peersPath)
	if err != nil {
		return err
	}
	var wrapKey []byte
	if wrapKeyFD >= 3 {
		wrapKey, err = readWrapKey(wrapKeyFD)
		if err != nil {
			return err
		}
		defer wipe(wrapKey)
	}
	format := ceremony.NodeBundleSealed
	if plaintextGenesisShares {
		format = ceremony.NodeBundlePlaintextGenesis51025
	}
	manifest, err := ceremony.VerifyNodeBundleWithFormat(bundleDir, nodeID, wrapKey, format, utils.VerifyECDSASignature)
	if err != nil {
		return err
	}
	return validateVerifiedBundleEnvironment(manifest, frostConfig, peers)
}

func validateVerifiedBundleEnvironment(manifest *ceremony.PublicManifest, frostConfig config.FrostConfig, peers *config.PeerManifest) error {
	if manifest == nil || peers == nil || peers.ChainID == 0 {
		return errors.New("bundle environment is incomplete")
	}
	peerDigest, err := ceremony.ComputePeerManifestDigest(peers)
	if err != nil {
		return err
	}
	if manifest.Plan.NetworkChainID != peers.ChainID || !bytes.Equal(manifest.Plan.PeerManifestDigest, peerDigest) {
		return errors.New("bundle ceremony manifest does not match peers.json or its network chain ID")
	}
	if err := ceremony.ValidateCeremonyConfigBinding(frostConfig, manifest); err != nil {
		return fmt.Errorf("bundle ceremony manifest does not match the base fields of the final FROST config: %w", err)
	}
	if err := ceremony.ValidateConfiguredGenesisDeploymentEnvironment(frostConfig, peers, peers.ChainID, utils.VerifyECDSASignature); err != nil {
		return fmt.Errorf("validate final configured-genesis deployment proof: %w", err)
	}
	proof, err := ceremony.StrictDecodeGenesisDeploymentManifest(frostConfig.GenesisDeploymentManifest)
	if err != nil {
		return fmt.Errorf("decode final configured-genesis deployment proof: %w", err)
	}
	if err := ceremony.ValidateGenesisDeploymentManifest(
		proof, frostConfig, manifest, nil, peers.ChainID, peerDigest, utils.VerifyECDSASignature,
	); err != nil {
		return fmt.Errorf("bind final configured-genesis deployment proof to bundle manifest: %w", err)
	}
	return nil
}
