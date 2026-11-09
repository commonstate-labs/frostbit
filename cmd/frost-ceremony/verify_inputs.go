package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"

	"dex/config"
	"dex/frost/ceremony"
	"dex/frost/chain/evm"
	"dex/utils"
)

type verifiedCeremonyInputs struct {
	Version           int    `json:"version"`
	Verified          bool   `json:"verified"`
	NetworkChainID    string `json:"network_chain_id"`
	Epoch             uint64 `json:"epoch"`
	ManifestSHA256    string `json:"manifest_sha256"`
	FrostConfigDigest string `json:"frost_config_digest"`
	BaseConfigSHA256  string `json:"base_config_sha256"`
}

func runVerifyInputs(args []string) error {
	flags := flag.NewFlagSet("verify-inputs", flag.ContinueOnError)
	basePath := flags.String("base-frost-config", "", "frozen public pre-ceremony FROST configuration")
	manifestPath := flags.String("manifest", "", "signed public ceremony manifest")
	expectedSHA := flags.String("expected-manifest-sha256", "", "independently pinned manifest SHA-256")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if *basePath == "" || *manifestPath == "" || *expectedSHA == "" {
		return errors.New("verify-inputs requires --base-frost-config, --manifest and --expected-manifest-sha256")
	}
	verified, err := verifyCeremonyDeploymentInputs(*basePath, *manifestPath, *expectedSHA)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(verified)
}

                                                                           
                                                                         
func verifyCeremonyDeploymentInputs(basePath, manifestPath, expectedSHA string) (*verifiedCeremonyInputs, error) {
	expected, err := hex.DecodeString(expectedSHA)
	if err != nil || len(expected) != sha256.Size {
		return nil, errors.New("expected manifest SHA-256 must be 32-byte hex")
	}
	data, err := readRegularFile(manifestPath, maxLargeJSONBytes)
	if err != nil {
		return nil, err
	}
	manifestSHA := sha256.Sum256(data)
	if !bytes.Equal(manifestSHA[:], expected) {
		return nil, errors.New("ceremony manifest SHA-256 mismatch")
	}
	configData, err := readRegularFile(basePath, maxSmallJSONBytes)
	if err != nil {
		return nil, err
	}
	cfg, err := decodeFrostConfigStrict(configData)
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled {
		return nil, errors.New("FROST must be enabled for deployment")
	}
	if err := requireSixChains(cfg.Chains); err != nil {
		return nil, err
	}
	if err := config.ValidateFrostGuardianConfig(cfg); err != nil {
		return nil, err
	}
	if len(cfg.GenesisDeploymentManifest) != 0 || len(cfg.GenesisKeyReuse) != 0 {
		return nil, errors.New("base FROST config already contains a genesis proof")
	}
	for name, chain := range cfg.Chains {
		if len(chain.VaultRefs) != 0 || len(chain.InitialGroupPubkeys) != 0 || len(chain.OnlineDKGVaultRefs) != 0 || chain.DepositDexChainID != 0 || chain.DepositProtocol != 0 {
			return nil, fmt.Errorf("base FROST config chain %s already contains ceremony output fields", name)
		}
	}
	manifest, err := ceremony.StrictDecodeManifest(data)
	if err != nil {
		return nil, err
	}
	if err := validatePlanIdentities(&manifest.Plan); err != nil {
		return nil, err
	}
	if err := ceremony.ValidateManifest(manifest, utils.VerifyECDSASignature); err != nil {
		return nil, fmt.Errorf("validate ceremony manifest: %w", err)
	}
	if manifest.Plan.Epoch != 1 {
		return nil, errors.New("Vault deployment requires ceremony epoch 1")
	}
	digest, err := ceremony.ComputeFrostConfigDigest(cfg)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(digest, manifest.Plan.FrostConfigDigest) {
		return nil, errors.New("base FROST config does not match the config bound into the ceremony plan")
	}
	if err := validatePlanAgainstFrostConfig(&manifest.Plan, cfg); err != nil {
		return nil, err
	}
	for _, vault := range manifest.Vaults {
		guardian, err := cfg.GuardianPublicKey(vault.Chain)
		if err != nil {
			return nil, err
		}
		if vault.Chain == "btc" {
			if _, _, _, err := utils.BuildDualCustodyTaproot(vault.RawGroupPubkey, guardian); err != nil {
				return nil, fmt.Errorf("BTC custody keys: %w", err)
			}
		} else {
			operator, err := evm.CanonicalBN128PublicKey(vault.RawGroupPubkey)
			if err != nil || bytes.Equal(operator, guardian) {
				return nil, errors.New("operator and guardian must be distinct valid keys")
			}
		}
	}
	configSHA := sha256.Sum256(configData)
	return &verifiedCeremonyInputs{Version: 1, Verified: true, NetworkChainID: strconv.FormatUint(manifest.Plan.NetworkChainID, 10), Epoch: 1,
		ManifestSHA256: hex.EncodeToString(manifestSHA[:]), FrostConfigDigest: hex.EncodeToString(digest), BaseConfigSHA256: hex.EncodeToString(configSHA[:])}, nil
}
