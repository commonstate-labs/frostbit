package consensus

import (
	"bytes"
	"crypto/sha256"
	"dex/certchain"
	"dex/keys"
	"dex/pb"
	statedb "dex/stateDB"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cockroachdb/pebble"
	"google.golang.org/protobuf/proto"
)

const certifiedAnchorRecoveryVersion uint32 = 1

                                                                           
                                                                              
                                                                            
                                                                             
                                         
type certifiedAnchorRecoveryRecord struct {
	Version           uint32 `json:"version"`
	PreviousHeight    uint64 `json:"previous_height"`
	Height            uint64 `json:"height"`
	BlockID           string `json:"block_id"`
	StateRoot         []byte `json:"state_root"`
	CertificateDigest []byte `json:"certificate_digest"`
	AnchorBlock       []byte `json:"anchor_block"`
}

func (s *RealBlockStore) prepareCertifiedAnchorRecovery(previousHeight, height uint64, blockID string, stateRoot []byte, anchorBlock *pb.Block) error {
	if s == nil || s.dbManager == nil {
		return fmt.Errorf("certified anchor recovery database is unavailable")
	}
	if len(stateRoot) != statedb.StateRootSize || statedb.IsZeroFingerprintBytes(stateRoot) {
		return fmt.Errorf("certified anchor recovery state root has invalid length or value")
	}
	if _, err := validateCertifiedAnchorBlock(anchorBlock, height, blockID); err != nil {
		return err
	}
	if err := s.lightweightValidateBlock(anchorBlock); err != nil {
		return fmt.Errorf("certified anchor recovery transaction validation failed: %w", err)
	}

	cert, certDigest, err := s.certifiedAnchorCertificate(height, blockID, stateRoot)
	if err != nil {
		return err
	}
	_ = cert
	anchorRaw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(anchorBlock)
	if err != nil {
		return fmt.Errorf("marshal certified anchor recovery block: %w", err)
	}
	record := certifiedAnchorRecoveryRecord{
		Version:           certifiedAnchorRecoveryVersion,
		PreviousHeight:    previousHeight,
		Height:            height,
		BlockID:           blockID,
		StateRoot:         append([]byte(nil), stateRoot...),
		CertificateDigest: certDigest,
		AnchorBlock:       anchorRaw,
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal certified anchor recovery record: %w", err)
	}
	s.dbManager.EnqueueSet(keys.KeyCertifiedAnchorRecovery(), string(raw))
	if err := s.dbManager.ForceFlushHot(); err != nil {
		return fmt.Errorf("flush certified anchor recovery record: %w", err)
	}
	written, err := s.dbManager.GetKV(keys.KeyCertifiedAnchorRecovery())
	if err != nil || !bytes.Equal(written, raw) {
		return fmt.Errorf("certified anchor recovery record durability verification failed: %w", err)
	}
	return nil
}

func (s *RealBlockStore) loadCertifiedAnchorRecovery() (*certifiedAnchorRecoveryRecord, *pb.Block, bool, error) {
	if s == nil || s.dbManager == nil {
		return nil, nil, false, nil
	}
	raw, err := s.dbManager.GetKV(keys.KeyCertifiedAnchorRecovery())
	if errors.Is(err, pebble.ErrNotFound) {
		return nil, nil, false, nil
	}
	if err != nil {
		return nil, nil, false, fmt.Errorf("read certified anchor recovery record: %w", err)
	}
	if len(raw) == 0 {
		return nil, nil, false, fmt.Errorf("certified anchor recovery record is empty")
	}
	var record certifiedAnchorRecoveryRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, nil, false, fmt.Errorf("decode certified anchor recovery record: %w", err)
	}
	canonical, err := json.Marshal(record)
	if err != nil || !bytes.Equal(raw, canonical) {
		return nil, nil, false, fmt.Errorf("certified anchor recovery record is not canonical JSON")
	}
	if record.Version != certifiedAnchorRecoveryVersion || record.Height == 0 || record.PreviousHeight >= record.Height || record.BlockID == "" {
		return nil, nil, false, fmt.Errorf("certified anchor recovery record has invalid identity")
	}
	if len(record.StateRoot) != statedb.StateRootSize || statedb.IsZeroFingerprintBytes(record.StateRoot) {
		return nil, nil, false, fmt.Errorf("certified anchor recovery record has invalid state root")
	}
	if len(record.CertificateDigest) == 0 || len(record.AnchorBlock) == 0 {
		return nil, nil, false, fmt.Errorf("certified anchor recovery record is incomplete")
	}
	var anchor pb.Block
	if err := proto.Unmarshal(record.AnchorBlock, &anchor); err != nil {
		return nil, nil, false, fmt.Errorf("decode certified anchor recovery block: %w", err)
	}
	canonicalBlock, err := (proto.MarshalOptions{Deterministic: true}).Marshal(&anchor)
	if err != nil || !bytes.Equal(canonicalBlock, record.AnchorBlock) {
		return nil, nil, false, fmt.Errorf("certified anchor recovery block is not canonical protobuf")
	}
	if _, err := validateCertifiedAnchorBlock(&anchor, record.Height, record.BlockID); err != nil {
		return nil, nil, false, err
	}
	if err := s.lightweightValidateBlock(&anchor); err != nil {
		return nil, nil, false, fmt.Errorf("certified anchor recovery transaction validation failed: %w", err)
	}
	_, digest, err := s.certifiedAnchorCertificate(record.Height, record.BlockID, record.StateRoot)
	if err != nil {
		return nil, nil, false, err
	}
	if !bytes.Equal(digest, record.CertificateDigest) {
		return nil, nil, false, fmt.Errorf("certified anchor recovery certificate commitment changed")
	}
	return &record, &anchor, true, nil
}

func (s *RealBlockStore) certifiedAnchorCertificate(height uint64, blockID string, stateRoot []byte) (*pb.CheckpointCertificate, []byte, error) {
	raw, err := s.dbManager.GetKV(keys.KeyCertCertificate(height))
	if err != nil || len(raw) == 0 {
		return nil, nil, fmt.Errorf("read authenticated checkpoint certificate %d: %w", height, err)
	}
	cert, err := certchain.ParseCertificate(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("parse authenticated checkpoint certificate %d: %w", height, err)
	}
	if cert.Height != height || string(cert.BlockHash) != blockID || !bytes.Equal(cert.StateRoot, stateRoot) {
		return nil, nil, fmt.Errorf("authenticated checkpoint certificate does not match recovery target %d/%s", height, blockID)
	}
	digest := sha256.Sum256(raw)
	return cert, append([]byte(nil), digest[:]...), nil
}

                                                                            
                                                                                
                                                                                
func (s *RealBlockStore) persistPreparedCertifiedAnchor(height uint64, blockID string, stateRoot []byte, anchorBlock *pb.Block) error {
	record, prepared, exists, err := s.loadCertifiedAnchorRecovery()
	if err != nil {
		return err
	}
	if !exists || record.Height != height || record.BlockID != blockID || !bytes.Equal(record.StateRoot, stateRoot) || !proto.Equal(prepared, anchorBlock) {
		return fmt.Errorf("certified anchor recovery record does not match installed target %d/%s", height, blockID)
	}
	if err := s.validateInstalledCertifiedAnchor(record, prepared); err != nil {
		return err
	}
	return s.persistRecoveredCertifiedAnchor(record, prepared)
}

func (s *RealBlockStore) validateInstalledCertifiedAnchor(record *certifiedAnchorRecoveryRecord, anchor *pb.Block) error {
	converted, err := validateCertifiedAnchorBlock(anchor, record.Height, record.BlockID)
	if err != nil {
		return err
	}
	finalizedHeight, exists, err := s.dbManager.GetFinalizedStateHeight()
	if err != nil {
		return fmt.Errorf("read finalized StateDB height for certified anchor: %w", err)
	}
	if !exists || finalizedHeight != record.Height {
		return fmt.Errorf("certified anchor StateDB height=(%d,%t), want %d", finalizedHeight, exists, record.Height)
	}
	stateID, exists, err := s.dbManager.FinalizedStateBlockID(record.Height)
	if err != nil {
		return fmt.Errorf("read certified anchor StateDB identity: %w", err)
	}
	if !exists || stateID != record.BlockID {
		return fmt.Errorf("certified anchor StateDB identity=(%q,%t), want %q", stateID, exists, record.BlockID)
	}
	committedBlockID, err := s.dbManager.Get(keys.KeyBlockHashByHeight(record.Height))
	if err != nil || string(committedBlockID) != record.BlockID {
		return fmt.Errorf("certified anchor consensus-state block identity=%q err=%v, want %q", committedBlockID, err, record.BlockID)
	}
	localRoot := s.dbManager.GetStateFingerprint()
	if !bytes.Equal(localRoot, record.StateRoot) {
		return fmt.Errorf("certified anchor StateDB fingerprint does not match authenticated checkpoint")
	}
	if err := validateProposerVRF(s.dbManager, s.windowConfig, converted, false); err != nil {
		return fmt.Errorf("certified anchor recovery VRF validation failed: %w", err)
	}
	return nil
}

func (s *RealBlockStore) persistRecoveredCertifiedAnchor(record *certifiedAnchorRecoveryRecord, anchor *pb.Block) error {
	if err := s.dbManager.CompleteStateCheckpointInstall(record.PreviousHeight, record.Height); err != nil {
		return fmt.Errorf("complete checkpoint metadata for certified anchor recovery: %w", err)
	}
	if err := s.dbManager.SaveBlock(anchor); err != nil {
		return fmt.Errorf("persist certified anchor block: %w", err)
	}
	if err := s.dbManager.ForceFlush(); err != nil {
		return fmt.Errorf("flush certified anchor block: %w", err)
	}
	persistedRaw, err := s.dbManager.GetKV(keys.KeyBlockData(record.BlockID))
	if err != nil {
		return fmt.Errorf("read persisted certified anchor block: %w", err)
	}
	var loaded pb.Block
	if err := proto.Unmarshal(persistedRaw, &loaded); err != nil {
		return fmt.Errorf("decode persisted certified anchor block: %w", err)
	}
	if loaded.Header == nil || loaded.Header.Height != record.Height || !proto.Equal(&loaded, anchor) {
		return fmt.Errorf("persisted certified anchor block does not match recovery record")
	}
	if err := s.dbManager.RecoverFinalizedStateCommit(record.Height, record.BlockID, false); err != nil {
		return fmt.Errorf("recover certified anchor VM commit marker: %w", err)
	}
	commitID, err := s.dbManager.GetKV(keys.KeyVMCommitHeight(record.Height))
	if err != nil || string(commitID) != record.BlockID {
		return fmt.Errorf("verify certified anchor VM commit marker: got=%q err=%v", commitID, err)
	}
	s.dbManager.EnqueueDel(keys.KeyCertifiedAnchorRecovery())
	if err := s.dbManager.ForceFlushHot(); err != nil {
		return fmt.Errorf("clear certified anchor recovery record: %w", err)
	}
	return nil
}

                                                                               
                                                                           
                                                                             
func (s *RealBlockStore) recoverPreparedCertifiedAnchor(finalizedHeight uint64) (bool, error) {
	record, anchor, exists, err := s.loadCertifiedAnchorRecovery()
	if err != nil || !exists {
		return false, err
	}
	if record.Height != finalizedHeight {
		return false, fmt.Errorf("certified anchor recovery height %d does not match finalized StateDB height %d", record.Height, finalizedHeight)
	}
	if err := s.validateInstalledCertifiedAnchor(record, anchor); err != nil {
		return false, err
	}
	if err := s.persistRecoveredCertifiedAnchor(record, anchor); err != nil {
		return false, err
	}
	return true, nil
}
