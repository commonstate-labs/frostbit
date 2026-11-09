package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

const usageText = `frost-ceremony performs the first-generation FROST DKG before mainnet starts.

Commands:
  context      build a random-ID, environment-bound six-chain context
  register     register an identity-signed one-time ECIES public key
  plan         verify every registration and assemble the final ceremony plan
  round1       create and identity-sign this validator's encrypted dealer package
  finalize     verify every package, write this validator's shares, and sign an attestation
  assemble     require every validator's matching attestation and assemble the public manifest
  bundle       build one exclusive private startup bundle from the manifest and local shares
  verify-bundle verify an exact node bundle; optionally unseal every share with the node wrap key
  verify-inputs verify public ceremony/configuration inputs before any Vault deployment
  node-assignment verify final public authority and print assigned or standby for one node
  attest-deployments independently verify deployed vaults and identity-sign the canonical result
  build-config require every deployment approval and write a proof-embedded frost_default.json
  key-reuse    verify original custody and online Vaults, then attest/assemble a new runtime policy

Flow:
  context -> register (every assigned participant) -> plan -> round1 (every assigned participant)
  -> finalize (every assigned participant) -> assemble -> bundle (every assigned participant) -> deploy
  -> attest-deployments (every assigned participant) -> build-config -> verify-bundle (every assigned participant)

Private material is accepted only through inherited file descriptors >= 3.
Create regular secret descriptors with mode 0600 (for example under umask 077),
and destroy each one-time ceremony encryption key after finalize succeeds.
For the development DEX network 51025 only, finalize, bundle, and verify-bundle
accept --plaintext-genesis-shares. This explicit mode writes secret shares
without a wrap key and must be delivered only to their assigned node.
Production build-config defaults to unified Vault receipts and requires --rpc-config and --solidity-artifacts-dir.
context, attest-deployments, build-config and node-assignment require public genesis.json beside peers.json, or --genesis.
Fresh genesis uses the same canonical TopK committee for all vaults; other network miners receive no shares.
All five account-chain Vault receipts, including TRON, must use the unified deposit protocol.
--accept-offline-unverified-deployments is a legacy tombstone: it is always rejected and never writes output.`

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		_, _ = fmt.Fprintf(os.Stderr, "frost-ceremony: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New(usageText)
	}
	switch args[0] {
	case "help", "-h", "--help":
		_, _ = fmt.Fprintln(os.Stdout, usageText)
		return nil
	case "context":
		return runContext(args[1:])
	case "register":
		return runRegister(args[1:])
	case "plan":
		return runPlan(args[1:])
	case "round1":
		return runRound1(args[1:])
	case "finalize":
		return runFinalize(args[1:])
	case "assemble":
		return runAssemble(args[1:])
	case "bundle":
		return runBundle(args[1:])
	case "verify-bundle":
		return runVerifyBundle(args[1:])
	case "verify-inputs":
		return runVerifyInputs(args[1:])
	case "node-assignment":
		return runNodeAssignment(args[1:])
	case "attest-deployments":
		return runAttestDeployments(args[1:])
	case "build-config":
		return runBuildConfig(args[1:])
	case "key-reuse":
		return runKeyReuse(args[1:])
	default:
		return fmt.Errorf("unknown command %q\n%s", args[0], usageText)
	}
}

func parseFlags(fs *flag.FlagSet, args []string) error {
	fs.SetOutput(os.Stdout)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", fs.Args())
	}
	return nil
}
