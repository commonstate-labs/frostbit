package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"dex/protocol"
)

type planOutput struct {
	ChainID          uint64             `json:"chain_id"`
	SupportedVersion uint64             `json:"supported_version"`
	Upgrades         []protocol.Upgrade `json:"upgrades"`
}

func main() {
	chainID := flag.Uint64("chain-id", 0, "DEX network chain ID")
	flag.Parse()
	if *chainID == 0 {
		fmt.Fprintln(os.Stderr, "--chain-id must be positive")
		os.Exit(2)
	}
	if err := protocol.ValidateUpgradeSchedule(*chainID); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(planOutput{
		ChainID:          *chainID,
		SupportedVersion: protocol.CurrentVersion,
		Upgrades:         protocol.UpgradeSchedule(*chainID),
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
