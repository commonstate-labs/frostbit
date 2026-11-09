package main

import (
	"dex/config"
	"dex/protocol"
	"dex/updater"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
)

                                                                              
                                                                               
func writeUpgradeInfo(out io.Writer, configDir string) error {
	info, err := readUpgradeInfo(configDir)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(info)
}

func readUpgradeInfo(configDir string) (updater.BinaryInfo, error) {
	var empty updater.BinaryInfo
	file, err := os.Open(config.ConfigPath(configDir, "genesis.json"))
	if err != nil {
		return empty, fmt.Errorf("upgrade info genesis: %w", err)
	}
	defer file.Close()
	var network struct {
		ChainID uint64 `json:"chain_id"`
	}
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&network); err != nil {
		return empty, fmt.Errorf("upgrade info genesis: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return empty, fmt.Errorf("upgrade info genesis must contain exactly one JSON document")
	}
	chainID := network.ChainID
	if chainID == 0 {
		return empty, fmt.Errorf("upgrade info requires a positive chain ID")
	}
	if err := protocol.ValidateUpgradeSchedule(chainID); err != nil {
		return empty, err
	}
	schedule := protocol.UpgradeSchedule(chainID)
	if schedule == nil {
		schedule = []protocol.Upgrade{}
	}
	return updater.BinaryInfo{
		SchemaVersion: updater.SchemaVersion, Repository: updater.Repository,
		Version: BuildVersion, Commit: BuildCommit, ChainID: chainID,
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		ProtocolVersion: protocol.CurrentVersion, DatabaseVersion: updater.DatabaseVersion,
		UpgradeSchedule: schedule, AutoUpdateDefault: true,
	}, nil
}
