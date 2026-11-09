                                                                                   
                                                                       
package updater

import (
	"context"
	"dex/protocol"
	"fmt"
	"net/http"
	"time"
)

const (
	Repository      = "commonstate-labs/frostbit"
	SchemaVersion   = 1
	DatabaseVersion = 1
)

type BinaryInfo struct {
	SchemaVersion     int                `json:"schema_version"`
	Repository        string             `json:"repository"`
	Version           string             `json:"version"`
	Commit            string             `json:"commit"`
	ChainID           uint64             `json:"chain_id"`
	GOOS              string             `json:"goos"`
	GOARCH            string             `json:"goarch"`
	ProtocolVersion   uint64             `json:"protocol_version"`
	DatabaseVersion   uint64             `json:"database_version"`
	UpgradeSchedule   []protocol.Upgrade `json:"upgrade_schedule"`
	AutoUpdateDefault bool               `json:"auto_update_default"`
}

type Asset struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type Manifest struct {
	SchemaVersion   int      `json:"schema_version"`
	Repository      string   `json:"repository"`
	Tag             string   `json:"tag"`
	Version         string   `json:"version"`
	Commit          string   `json:"commit"`
	GOOS            string   `json:"goos"`
	GOARCH          string   `json:"goarch"`
	ChainIDs        []uint64 `json:"chain_ids"`
	ProtocolVersion uint64   `json:"protocol_version"`
	DatabaseVersion uint64   `json:"database_version"`
	RolloutMode     string   `json:"rollout_mode"`
	RollbackSafe    bool     `json:"rollback_safe"`
	Asset           Asset    `json:"asset"`
}

func ManifestName(goos, goarch string) string {
	return fmt.Sprintf("dex-update-%s-%s.json", goos, goarch)
}

type Config struct {
	Enabled                         bool
	StateDir, ConfigDir, Executable string
	Info                            BinaryInfo
	SecretFDs                       []int
	GHPath                          string
	RetainGenesisProof              bool
	PollInterval, CommandTimeout    time.Duration
	MaxAssetBytes                   int64
	                                                                              
	MaxJitter time.Duration
}

func DefaultConfig() Config {
	return Config{Enabled: true, GHPath: "/usr/bin/gh", PollInterval: 10 * time.Minute, CommandTimeout: 2 * time.Minute, MaxAssetBytes: 512 << 20, MaxJitter: 30 * time.Second}
}

type Prepared struct {
	Path      string   `json:"path"`
	ReleaseID int64    `json:"release_id"`
	Tag       string   `json:"tag"`
	Version   string   `json:"version"`
	Commit    string   `json:"commit"`
	Digest    string   `json:"digest"`
	Manifest  Manifest `json:"manifest"`
}

                                                                              
type Runner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type Dependencies struct {
	HTTPClient *http.Client
	Runner     Runner
	Logf       func(string, ...any)
	Now        func() time.Time
	                                                                              
	APIURL string
	                                                                                     
	AcquireLock   func(string) (func(), error)
	ValidatePaths func(...string) error
	Exec          func(string, []string, []string) error
	Args, Env     []string
	                                                                                     
	EUID              func() int
	CurrentExecutable func() (string, error)
	Supported         func() bool
}
