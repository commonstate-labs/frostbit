package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"dex/config"
	"dex/frost/ceremony"
	"dex/utils"
)

func runNodeAssignment(args []string) error {
	fs := flag.NewFlagSet("node-assignment", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var frostPath, peersPath, genesisPath, nodeID string
	fs.StringVar(&frostPath, "frost-config", "", "final proof-embedded frost_default.json")
	fs.StringVar(&peersPath, "peers", "", "the exact public peers.json bound into the ceremony")
	fs.StringVar(&genesisPath, "genesis", "", "public genesis.json (defaults beside peers.json)")
	fs.StringVar(&nodeID, "node-id", "", "the node's full canonical DEX address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || frostPath == "" || peersPath == "" || nodeID == "" {
		return errors.New("node-assignment requires --frost-config, --peers, and --node-id")
	}
	frost, err := loadFrostConfigStrict(frostPath)
	if err != nil {
		return err
	}
	peers, err := loadPeerManifestStrict(peersPath)
	if err != nil {
		return err
	}
	if err := validateFreshGenesisInput(genesisPath, peersPath, frost, peers); err != nil {
		return err
	}
	assignment, err := verifiedNodeAssignment(frost, peers, nodeID)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, assignment)
	return err
}

func verifiedNodeAssignment(frost config.FrostConfig, peers *config.PeerManifest, nodeID string) (string, error) {
	if !utils.IsDexAddress(nodeID) {
		return "", errors.New("node-assignment requires a canonical DEX address")
	}
	if !frost.Enabled || peers == nil || peers.ChainID == 0 || len(frost.GenesisDeploymentManifest) == 0 {
		return "", errors.New("node-assignment requires an enabled final FROST config and signed genesis deployment proof")
	}
	if err := ceremony.ValidateConfiguredGenesisDeploymentEnvironment(frost, peers, peers.ChainID, utils.VerifyECDSASignature); err != nil {
		return "", fmt.Errorf("verify node-assignment authority: %w", err)
	}
	proof, err := ceremony.StrictDecodeGenesisDeploymentManifest(frost.GenesisDeploymentManifest)
	if err != nil {
		return "", err
	}
	for _, participant := range proof.Statement.Participants {
		if participant == nodeID {
			return "assigned", nil
		}
	}
	                                                                           
	                                                                        
	return "standby", nil
}
