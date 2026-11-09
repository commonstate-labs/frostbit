package main

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"

	"dex/config"
	"dex/frost/ceremony"
	"dex/keys"
	"dex/utils"
)

const configuredFrostGenesisStateMarkerPrefix = "standalone-dkg-v1:"

func validateFrostGenesisDeploymentProof(peers []PeerInfo, frostCfg config.FrostConfig, networkChainID uint64, peerManifestDigest []byte) (*ceremony.SignedGenesisDeploymentManifest, error) {
	return validateFrostGenesisDeploymentProofMode(peers, frostCfg, networkChainID, peerManifestDigest, false)
}

func validateFrostGenesisDeploymentProofMode(peers []PeerInfo, frostCfg config.FrostConfig, networkChainID uint64, peerManifestDigest []byte, retain bool) (*ceremony.SignedGenesisDeploymentManifest, error) {
	configured, err := configuredFrostGenesisMode(frostCfg)
	if err != nil {
		return nil, err
	}
	if !configured {
		if retain {
			return nil, errors.New("retaining genesis requires a signed configured-genesis proof")
		}
		if len(frostCfg.GenesisDeploymentManifest) != 0 {
			return nil, errors.New("legacy online-DKG configuration must not contain a genesis deployment manifest")
		}
		return nil, nil
	}
	if len(frostCfg.GenesisDeploymentManifest) == 0 {
		return nil, errors.New("configured FROST genesis requires a fully signed deployment manifest")
	}
	proof, err := ceremony.StrictDecodeGenesisDeploymentManifest(frostCfg.GenesisDeploymentManifest)
	if err != nil {
		return nil, fmt.Errorf("decode signed genesis deployment manifest: %w", err)
	}
	var participants []ceremony.Participant
	var expectedVaultDigests [][]byte
	if retain {
		participants, err = retainedGenesisDeploymentParticipants(peers, proof.Statement.Participants)
	} else {
		participants, expectedVaultDigests, err = expectedGenesisDeploymentTrust(peers, frostCfg)
	}
	if err != nil {
		return nil, err
	}
	if err := ceremony.ValidateGenesisDeploymentManifest(
		proof, frostCfg, nil, participants, networkChainID, peerManifestDigest, utils.VerifyECDSASignature,
	); err != nil {
		return nil, fmt.Errorf("validate signed genesis deployment manifest: %w", err)
	}
	if retain {
		return proof, nil                                                                          
	}
	if len(proof.Statement.Vaults) != len(expectedVaultDigests) {
		return nil, errors.New("deployment proof vault set does not match deterministic genesis assignment")
	}
	for i := range expectedVaultDigests {
		if !bytes.Equal(proof.Statement.Vaults[i].CommitteeDigest, expectedVaultDigests[i]) {
			return nil, fmt.Errorf("deployment proof committee mismatch for %s/%d", proof.Statement.Vaults[i].Chain, proof.Statement.Vaults[i].VaultID)
		}
	}
	return proof, nil
}

func retainedGenesisDeploymentParticipants(peers []PeerInfo, ids []string) ([]ceremony.Participant, error) {
	if len(ids) == 0 || len(ids) > ceremony.MaxParticipants || len(ids) > len(peers) {
		return nil, errors.New("invalid retained genesis participant count")
	}
	byID := make(map[string]PeerInfo, len(peers))
	for _, peer := range peers {
		address, err := utils.AddressFromSecp256k1PubKey(peer.SecpPublicKey)
		if err != nil || !peer.IsMiner || address != peer.Address {
			return nil, errors.New("invalid trusted genesis validator identity")
		}
		if _, exists := byID[address]; exists {
			return nil, errors.New("duplicate trusted genesis validator")
		}
		byID[address] = peer
	}
	participants := make([]ceremony.Participant, 0, len(ids))
	for _, id := range ids {
		peer, ok := byID[id]
		if !ok {
			return nil, errors.New("retained proof participant is not a trusted genesis validator")
		}
		participants = append(participants, ceremony.Participant{ID: id, PublicKey: bytes.Clone(peer.SecpPublicKey)})
		delete(byID, id)
	}
	return participants, nil
}

                                                                          
                                                                               
func validateRetainedGenesisProofState(reader frostGenesisConsensusMarkerReader, cfg config.FrostConfig, height uint64, resume bool, mode BootstrapMode) error {
	if !resume || mode != GenesisBootstrap || height == 0 {
		return errors.New("retaining genesis requires a finalized existing genesis-mode database and resume=true")
	}
	present, err := validatePersistedFrostGenesisMarker(reader, cfg)
	if err != nil {
		return err
	}
	if !present {
		return errors.New("retaining genesis requires the existing configured-genesis marker")
	}
	return nil
}

func expectedGenesisDeploymentTrust(peers []PeerInfo, frostCfg config.FrostConfig) ([]ceremony.Participant, [][]byte, error) {
	validators := make([]ceremony.GenesisTrustValidator, len(peers))
	for i, peer := range peers {
		validators[i] = ceremony.GenesisTrustValidator{Address: peer.Address, PublicKey: bytes.Clone(peer.SecpPublicKey), IsMiner: peer.IsMiner}
	}
	return ceremony.ExpectedGenesisDeploymentTrust(validators, frostCfg)
}

func configuredFrostGenesisMarkerValue(frostCfg config.FrostConfig) (string, error) {
	if len(frostCfg.GenesisDeploymentManifest) == 0 {
		return "", errors.New("configured FROST genesis deployment manifest is missing")
	}
	proof, err := ceremony.StrictDecodeGenesisDeploymentManifest(frostCfg.GenesisDeploymentManifest)
	if err != nil {
		return "", err
	}
	if proof.Version != ceremony.GenesisDeploymentVersion {
		return "", errors.New("configured FROST genesis deployment manifest version mismatch")
	}
	digest, err := ceremony.GenesisDeploymentStatementDigest(&proof.Statement)
	if err != nil || !bytes.Equal(digest, proof.StatementDigest) {
		return "", errors.New("configured FROST genesis deployment statement digest mismatch")
	}
	prefix := configuredFrostGenesisStateMarkerPrefix
	if len(frostCfg.GenesisKeyReuse) != 0 {
		prefix = config.RetainedFrostGenesisMarkerPrefix
	}
	return prefix + hex.EncodeToString(digest), nil
}

type frostGenesisConsensusMarkerReader interface {
	Read(key string) (string, error)
}

                                                                            
                                                                            
                                                                          
func validatePersistedFrostGenesisMarker(reader frostGenesisConsensusMarkerReader, frostCfg config.FrostConfig) (bool, error) {
	if reader == nil {
		return false, errors.New("configured-genesis marker reader is unavailable")
	}
	value, err := reader.Read(keys.KeyFrostConfiguredGenesisMarker())
	if err != nil {
		if isNotFoundError(err) {
			return false, nil
		}
		return false, err
	}
	if !frostCfg.Enabled {
		return true, errors.New("persisted configured-genesis marker forbids disabling FROST")
	}
	configured, err := configuredFrostGenesisMode(frostCfg)
	if err != nil {
		return true, err
	}
	if !configured {
		return true, errors.New("persisted configured-genesis marker forbids legacy online-DKG configuration")
	}
	expected, err := configuredFrostGenesisMarkerValue(frostCfg)
	if err != nil {
		return true, err
	}
	if value != expected {
		return true, errors.New("persisted configured-genesis marker conflicts with the signed deployment statement")
	}
	return true, nil
}
