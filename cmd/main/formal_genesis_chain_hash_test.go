package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dex/config"
	"dex/formal/itftrace"
	"dex/frost/ceremony"
	"dex/keys"
	"dex/utils"
)

type formalRetainedMarker struct {
	value string
	err   error
	reads int
}

func (r *formalRetainedMarker) Read(key string) (string, error) {
	r.reads++
	if key != keys.KeyFrostConfiguredGenesisMarker() {
		return "", errors.New("formal retained guard read an unrelated key")
	}
	return r.value, r.err
}

func formalRetainedPeers(f *genesisImportFixture) []PeerInfo {
	peers := make([]PeerInfo, len(f.networkParticipants))
	for i, p := range f.networkParticipants {
		peers[len(peers)-1-i] = PeerInfo{Address: p.ID, SecpPublicKey: bytes.Clone(p.PublicKey), IsMiner: true}
	}
	return peers
}

                                                                           
                                                                               
func formalRetainedOtherSignedStatement(t *testing.T, f *genesisImportFixture) config.FrostConfig {
	t.Helper()
	proof, err := ceremony.StrictDecodeGenesisDeploymentManifest(f.config.Frost.GenesisDeploymentManifest)
	if err != nil {
		t.Fatal(err)
	}
	proof.Statement.CeremonyManifestDigest[0] ^= 1
	digest, err := ceremony.GenesisDeploymentStatementDigest(&proof.Statement)
	if err != nil {
		t.Fatal(err)
	}
	participants := make([]ceremony.Participant, len(proof.Statement.Participants))
	approvals := make([]*ceremony.GenesisDeploymentApproval, len(participants))
	for i, id := range proof.Statement.Participants {
		private := f.identities[id]
		participants[i] = ceremony.Participant{ID: id, PublicKey: private.PubKey().SerializeCompressed()}
		a := &ceremony.GenesisDeploymentApproval{Version: ceremony.GenesisDeploymentVersion, StatementDigest: digest, ParticipantID: id}
		d, err := ceremony.GenesisDeploymentApprovalDigest(a)
		if err != nil {
			t.Fatal(err)
		}
		a.Signature = signGenesisImportDigest(t, private, d)
		approvals[i] = a
	}
	other, err := ceremony.AssembleGenesisDeploymentManifest(&proof.Statement, approvals, participants, utils.VerifyECDSASignature)
	if err != nil {
		t.Fatal(err)
	}
	cfg := f.config.Frost
	cfg.GenesisDeploymentManifest, err = json.Marshal(other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := validateFrostGenesisDeploymentProofMode(formalRetainedPeers(f), cfg, f.chainID, f.peerDigest, true); err != nil {
		t.Fatalf("other statement must independently authenticate: %v", err)
	}
	return cfg
}

func formalRetainedProofCase(t *testing.T, f *genesisImportFixture, cfg config.FrostConfig, auth int64, flag bool) error {
	t.Helper()
	peers, chainID, peerDigest := formalRetainedPeers(f), f.chainID, bytes.Clone(f.peerDigest)
	proof, err := ceremony.StrictDecodeGenesisDeploymentManifest(cfg.GenesisDeploymentManifest)
	if err != nil {
		t.Fatal(err)
	}
	switch auth {
	case 0:
	case 1:
		proof.Approvals = proof.Approvals[:len(proof.Approvals)-1]
	case 2:
		cfg.Committee.TopN++
	case 3:
		peerDigest[0] ^= 1
	case 4:
		chainID++
	case 5:
		peers = peers[1:]
	case 6:
		proof.Statement.Participants[0] = proof.Statement.Participants[1]
	case 7:
		peers[0].SecpPublicKey = bytes.Clone(peers[1].SecpPublicKey)
	default:
		t.Fatal("unmapped auth case")
	}
	cfg.GenesisDeploymentManifest, err = json.Marshal(proof)
	if err != nil {
		t.Fatal(err)
	}
	_, err = validateFrostGenesisDeploymentProofMode(peers, cfg, chainID, peerDigest, flag)
	return err
}

func TestFormalGenesisChainHashRetainedIdentityAndFullApproval(t *testing.T) {
	f := newGenesisImportFixtureWithAssignment(t, 3, ceremony.PlaintextGenesisChainID, true, true)
	before := bytes.Clone(f.config.Frost.GenesisDeploymentManifest)
	for auth := int64(0); auth <= 7; auth++ {
		t.Run(fmt.Sprint(auth), func(t *testing.T) {
			err := formalRetainedProofCase(t, f, f.config.Frost, auth, true)
			if (err == nil) != (auth == 0) {
				t.Fatalf("auth%d accepted=%t", auth, err == nil)
			}
		})
	}
	if !bytes.Equal(before, f.config.Frost.GenesisDeploymentManifest) || f.store.saves != 0 || f.store.manifestWrites != 0 {
		t.Fatal("retained authentication mutated the configured proof or share store")
	}
}

func TestFormalGenesisChainHashRetainedStateReadErrorFailsClosed(t *testing.T) {
	f := newGenesisImportFixtureWithAssignment(t, 3, ceremony.PlaintextGenesisChainID, true, true)
	marker, err := configuredFrostGenesisMarkerValue(f.config.Frost)
	if err != nil {
		t.Fatal(err)
	}
	for _, readErr := range []error{errors.New("key not found"), errors.New("disk failure")} {
		r := &formalRetainedMarker{value: marker, err: readErr}
		if err := validateRetainedGenesisProofState(r, f.config.Frost, 10, true, GenesisBootstrap); err == nil || r.reads != 1 {
			t.Fatalf("read failure accepted or IO bound changed: %v reads=%d", err, r.reads)
		}
	}
}

func formalRetainedRecord(t *testing.T, s itftrace.State) itftrace.State {
	t.Helper()
	if _, found := s.Values["retainAttempted"]; !found {
		t.Fatal("missing model retained authority fields")
	}
	return s
}

func formalRetainedInt(t *testing.T, s itftrace.State, name string) int64 {
	t.Helper()
	v, err := s.Int(name)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func formalRetainedBool(t *testing.T, s itftrace.State, name string) bool {
	t.Helper()
	v, err := s.Bool(name)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestFormalGenesisChainHashRetainedITFDifferential(t *testing.T) {
	dir := os.Getenv("DEX_FORMAL_ITF_GENESIS_CHAIN_HASH")
	if dir == "" {
		t.Skip("formal_genesis_chain_hash.ps1 supplies the model ITF")
	}
	f := newGenesisImportFixtureWithAssignment(t, 3, ceremony.PlaintextGenesisChainID, true, true)
	original := bytes.Clone(f.config.Frost.GenesisDeploymentManifest)
	other := formalRetainedOtherSignedStatement(t, f)
	marker, err := configuredFrostGenesisMarkerValue(f.config.Frost)
	if err != nil {
		t.Fatal(err)
	}
	edges := 0
	for _, name := range []string{"existingExactProofRetainsTest", "emptyOrHeightZeroCannotRetainTest", "conflictingProofCannotRetainTest", "explicitResumeAndNonJoinRequiredTest", "invalidIdentityOrSignatureCannotRetainTest"} {
		paths, err := filepath.Glob(filepath.Join(dir, name+"_*.itf.json"))
		if err != nil || len(paths) != 1 {
			t.Fatalf("missing or ambiguous trace %s: %v", name, err)
		}
		trace, err := itftrace.Load(paths[0])
		if err != nil || len(trace.States) < 2 {
			t.Fatalf("invalid retained trace: %v", err)
		}
		for i, frame := range trace.States {
			if i == 0 {
				continue
			}
			s := formalRetainedRecord(t, frame)
			if !formalRetainedBool(t, s, "retainAttempted") {
				t.Fatal("retained trace lost its attempted action")
			}
			cfg := f.config.Frost
			if formalRetainedInt(t, s, "proofDigest") != 1 {
				cfg = other
			}
			reader := &formalRetainedMarker{value: marker}
			switch formalRetainedInt(t, s, "marker") {
			case 0:
				reader.value, reader.err = "", errors.New("key not found")
			case 2:
				reader.value = "conflicting-mode:" + strings.Repeat("00", 32)
			}
			err := formalRetainedProofCase(t, f, cfg, formalRetainedInt(t, s, "authCase"), formalRetainedBool(t, s, "flag"))
			if err == nil {
				mode := GenesisBootstrap
				if formalRetainedBool(t, s, "lateJoin") {
					mode = LateJoin
				}
				err = validateRetainedGenesisProofState(reader, cfg, uint64(formalRetainedInt(t, s, "height")), formalRetainedBool(t, s, "resume"), mode)
			}
			want := formalRetainedBool(t, s, "retainAccepted")
			if (err == nil) != want || reader.reads > 1 {
				t.Fatalf("%s edge%d actual=%t model=%t reads=%d err=%v", name, i, err == nil, want, reader.reads, err)
			}
			edges++
		}
	}
	if edges != 10 || !bytes.Equal(original, f.config.Frost.GenesisDeploymentManifest) || f.store.saves != 0 || f.store.manifestWrites != 0 {
		t.Fatalf("retained projection edges=%d or immutable authority changed", edges)
	}
}
