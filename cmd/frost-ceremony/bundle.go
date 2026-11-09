package main

import (
	"errors"
	"flag"
	"fmt"

	"dex/frost/ceremony"
	"dex/utils"
)

func runBundle(args []string) error {
	fs := flag.NewFlagSet("bundle", flag.ContinueOnError)
	var manifestPath, nodeID, sealedSharesDir, out string
	var plaintextGenesisShares bool
	fs.StringVar(&manifestPath, "manifest", "", "assembled public ceremony manifest")
	fs.StringVar(&nodeID, "node-id", "", "validator's canonical DEX address")
	fs.StringVar(&sealedSharesDir, "sealed-shares-dir", "", "directory containing exactly this validator's shares; name retained for compatibility")
	fs.StringVar(&out, "out", "", "new private node bundle directory")
	fs.BoolVar(&plaintextGenesisShares, "plaintext-genesis-shares", false, "explicit development mode: bundle unencrypted genesis shares for DEX chain 51025")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if manifestPath == "" || nodeID == "" || sealedSharesDir == "" || out == "" {
		return errors.New("bundle requires --manifest, --node-id, --sealed-shares-dir, and --out")
	}
	manifestData, err := readRegularFile(manifestPath, maxLargeJSONBytes)
	if err != nil {
		return err
	}
	manifest, err := ceremony.StrictDecodeManifest(manifestData)
	if err != nil {
		return fmt.Errorf("decode ceremony manifest: %w", err)
	}
	if err := validatePlanIdentities(&manifest.Plan); err != nil {
		return err
	}
	format := ceremony.NodeBundleSealed
	if plaintextGenesisShares {
		format = ceremony.NodeBundlePlaintextGenesis51025
	}
	if err := ceremony.WriteNodeBundleWithFormat(out, manifest, nodeID, sealedSharesDir, format, utils.VerifyECDSASignature); err != nil {
		return fmt.Errorf("write node bundle: %w", err)
	}
	return nil
}
