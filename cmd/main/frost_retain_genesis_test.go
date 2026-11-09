package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"strings"
	"testing"

	"dex/config"
	"dex/frost/ceremony"
	"dex/keys"
)

func TestRetainGenesisProofRejectsInvalidStartupOptions(t *testing.T) {
	for _, tc := range []struct {
		name, env, want string
		args            []string
	}{
		{name: "invalid environment", env: "true", want: "must be 0 or 1", args: []string{"--check-config"}},
		{name: "not resuming", env: "1", want: "resume=true", args: []string{"--check-config", "--resume=false"}},
		{name: "late join", want: "bootstrap-mode=genesis", args: []string{"--check-config", "--retain-genesis-proof", "--bootstrap-mode=latejoin"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			priorFlags, priorArgs := flag.CommandLine, os.Args
			t.Cleanup(func() { flag.CommandLine, os.Args = priorFlags, priorArgs })
			flag.CommandLine = flag.NewFlagSet("retain-startup", flag.ContinueOnError)
			os.Args = append([]string{"node"}, tc.args...)
			t.Setenv("DEX_RETAIN_GENESIS_PROOF", tc.env)
			if err := runMain(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("startup error=%v want %s", err, tc.want)
			}
		})
	}
}

type retainedGenesisMarkerReader struct {
	value string
	err   error
	reads int
}

func (r *retainedGenesisMarkerReader) Read(key string) (string, error) {
	r.reads++
	if key != keys.KeyFrostConfiguredGenesisMarker() {
		return "", errors.New("unexpected retained-genesis key")
	}
	return r.value, r.err
}

func TestRetainGenesisProofRequiresFinalizedMatchingDatabase(t *testing.T) {
	f := newGenesisImportFixtureWithAssignment(t, 3, ceremony.PlaintextGenesisChainID, true, true)
	marker, err := configuredFrostGenesisMarkerValue(f.config.Frost)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		height  uint64
		resume  bool
		mode    BootstrapMode
		value   string
		readErr error
		ok      bool
	}{
		{name: "existing database", height: 25, resume: true, value: marker, ok: true},
		{name: "height zero", resume: true, value: marker},
		{name: "not resuming", height: 25, value: marker},
		{name: "late join", height: 25, resume: true, mode: LateJoin, value: marker},
		{name: "missing marker", height: 25, resume: true, readErr: errors.New("key not found")},
		{name: "empty marker", height: 25, resume: true},
		{name: "different marker", height: 25, resume: true, value: marker + "00"},
		{name: "wrong mode prefix", height: 25, resume: true, value: config.RetainedFrostGenesisMarkerPrefix + marker},
		{name: "read error", height: 25, resume: true, value: marker, readErr: errors.New("disk read failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &retainedGenesisMarkerReader{value: tc.value, err: tc.readErr}
			err := validateRetainedGenesisProofState(r, f.config.Frost, tc.height, tc.resume, tc.mode)
			if (err == nil) != tc.ok {
				t.Fatalf("retain guard error=%v wantOK=%v", err, tc.ok)
			}
			if r.reads > 1 {
				t.Fatalf("retain guard made %d point reads", r.reads)
			}
		})
	}
}

func TestRetainGenesisProofStillAuthenticatesOriginalStatement(t *testing.T) {
	f := newGenesisImportFixtureWithAssignment(t, 3, ceremony.PlaintextGenesisChainID, true, true)
	peers := make([]PeerInfo, 0, len(f.networkParticipants))
	for _, p := range f.networkParticipants {
		peers = append(peers, PeerInfo{Address: p.ID, SecpPublicKey: p.PublicKey, IsMiner: true})
	}
	original := bytes.Clone(f.config.Frost.GenesisDeploymentManifest)
	if _, err := validateFrostGenesisDeploymentProofMode(peers, f.config.Frost, f.chainID, f.peerDigest, true); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*ceremony.SignedGenesisDeploymentManifest, *config.FrostConfig, *[]PeerInfo, *uint64, *[]byte)
	}{
		{"signature", func(p *ceremony.SignedGenesisDeploymentManifest, _ *config.FrostConfig, _ *[]PeerInfo, _ *uint64, _ *[]byte) {
			p.Approvals[0].Signature[0] ^= 1
		}},
		{"statement digest", func(p *ceremony.SignedGenesisDeploymentManifest, _ *config.FrostConfig, _ *[]PeerInfo, _ *uint64, _ *[]byte) {
			p.StatementDigest[0] ^= 1
		}},
		{"untrusted participant", func(_ *ceremony.SignedGenesisDeploymentManifest, _ *config.FrostConfig, peers *[]PeerInfo, _ *uint64, _ *[]byte) {
			*peers = (*peers)[1:]
		}},
		{"chain", func(_ *ceremony.SignedGenesisDeploymentManifest, _ *config.FrostConfig, _ *[]PeerInfo, id *uint64, _ *[]byte) {
			*id++
		}},
		{"peer digest", func(_ *ceremony.SignedGenesisDeploymentManifest, _ *config.FrostConfig, _ *[]PeerInfo, _ *uint64, d *[]byte) {
			(*d)[0] ^= 1
		}},
		{"config digest", func(_ *ceremony.SignedGenesisDeploymentManifest, c *config.FrostConfig, _ *[]PeerInfo, _ *uint64, _ *[]byte) {
			c.Committee.TopN++
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := ceremony.StrictDecodeGenesisDeploymentManifest(original)
			if err != nil {
				t.Fatal(err)
			}
			cfg := f.config.Frost
			copyPeers := append([]PeerInfo(nil), peers...)
			chainID, digest := f.chainID, bytes.Clone(f.peerDigest)
			tc.mutate(p, &cfg, &copyPeers, &chainID, &digest)
			cfg.GenesisDeploymentManifest, err = json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := validateFrostGenesisDeploymentProofMode(copyPeers, cfg, chainID, digest, true); err == nil {
				t.Fatal("retained proof tampering accepted")
			}
		})
	}
	if !bytes.Equal(original, f.config.Frost.GenesisDeploymentManifest) {
		t.Fatal("retained verification rewrote original proof")
	}
}
