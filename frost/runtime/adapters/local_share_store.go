package adapters

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"dex/frost/runtime"
	iface "dex/interfaces"
	"dex/keys"
	"dex/logs"
)

                                                                          
                                                                     
                                                                                             
                                                                                     
type DBLocalShareStore struct {
	dbManager iface.NodeLocalWriter
	cipher    *shareCipher
}

func NewDBLocalShareStore(dbManager iface.NodeLocalWriter) *DBLocalShareStore {
	return &DBLocalShareStore{dbManager: dbManager, cipher: newShareCipher(dbManager)}
}

                                                                            
                                                                            
                                                                        
                                                                             
func NewDBLocalShareStoreWithExternalWrapKey(dbManager iface.NodeLocalWriter, wrapKey []byte) (*DBLocalShareStore, error) {
	cipher, err := newShareCipherWithExternalWrapKey(dbManager, wrapKey)
	if err != nil {
		return nil, err
	}
	return &DBLocalShareStore{dbManager: dbManager, cipher: cipher}, nil
}

                                                                             
                                                                                
                                                                                
func (s *DBLocalShareStore) EnsureReady() error {
	if s == nil || s.cipher == nil {
		return errors.New("local share store is not initialized")
	}
	_, err := s.cipher.ensure()
	return err
}

func (s *DBLocalShareStore) SaveLocalShare(chain string, vaultID uint32, epoch uint64, share []byte) error {
	if s == nil || s.dbManager == nil {
		return errors.New("db manager not available")
	}
	if err := s.EnsureReady(); err != nil {
		return err
	}
	if len(share) == 0 {
		return errors.New("empty local share")
	}

	key := keys.KeyFrostLocalShare(chain, vaultID, epoch)
	shareCopy := make([]byte, len(share))
	copy(shareCopy, share)
	defer clear(shareCopy)

	blob, err := s.cipher.encrypt(key, shareCopy)
	if err != nil {
		logs.Warn("[LocalShareStore] encrypt local share failed chain=%s vault=%d epoch=%d key=%s err=%v",
			chain, vaultID, epoch, key, err)
		return err
	}
	defer clear(blob)
	logs.Debug("[LocalShareStore] save local share chain=%s vault=%d epoch=%d key=%s cipher_len=%d",
		chain, vaultID, epoch, key, len(blob))

	s.dbManager.EnqueueSet(key, string(blob))
	if err := s.dbManager.ForceFlush(); err != nil {
		logs.Warn("[LocalShareStore] save local share failed chain=%s vault=%d epoch=%d key=%s err=%v",
			chain, vaultID, epoch, key, err)
		return err
	}
	logs.Debug("[LocalShareStore] save local share completed chain=%s vault=%d epoch=%d key=%s",
		chain, vaultID, epoch, key)
	return nil
}

func (s *DBLocalShareStore) LoadLocalShare(chain string, vaultID uint32, epoch uint64) ([]byte, error) {
	if s == nil || s.dbManager == nil {
		return nil, errors.New("db manager not available")
	}
	if err := s.EnsureReady(); err != nil {
		return nil, err
	}

	key := keys.KeyFrostLocalShare(chain, vaultID, epoch)
	logs.Debug("[LocalShareStore] load local share chain=%s vault=%d epoch=%d key=%s",
		chain, vaultID, epoch, key)
	data, err := s.dbManager.Get(key)
	if err != nil {
		if isLocalShareNotFound(err) {
			logs.Debug("[LocalShareStore] local share not found chain=%s vault=%d epoch=%d key=%s",
				chain, vaultID, epoch, key)
			return nil, nil
		}
		logs.Warn("[LocalShareStore] load local share failed chain=%s vault=%d epoch=%d key=%s err=%v",
			chain, vaultID, epoch, key, err)
		return nil, err
	}
	if len(data) == 0 {
		logs.Debug("[LocalShareStore] local share empty chain=%s vault=%d epoch=%d key=%s",
			chain, vaultID, epoch, key)
		return nil, nil
	}

	plain, err := s.cipher.decrypt(key, data)
	if err != nil {
		logs.Warn("[LocalShareStore] decrypt local share failed chain=%s vault=%d epoch=%d key=%s err=%v",
			chain, vaultID, epoch, key, err)
		return nil, err
	}
	logs.Debug("[LocalShareStore] loaded local share chain=%s vault=%d epoch=%d key=%s",
		chain, vaultID, epoch, key)
	return plain, nil
}

type pendingShareData struct {
	Chain     string `json:"chain"`
	VaultID   uint32 `json:"vault_id"`
	Epoch     uint64 `json:"epoch"`
	SessionID string `json:"session_id"`
	Share     []byte `json:"share"`
}

func dkgLocalSessionMarkerData(chain string, vaultID uint32, epoch uint64, sessionID string) ([]byte, error) {
	return json.Marshal(runtime.DkgLocalSessionRef{
		Chain: chain, VaultID: vaultID, Epoch: epoch, SessionID: sessionID,
	})
}

func decodePendingShareData(plain []byte, chain string, vaultID uint32, epoch uint64, sessionID string) (*pendingShareData, error) {
	var record pendingShareData
	if err := json.Unmarshal(plain, &record); err != nil {
		clear(record.Share)
		return nil, err
	}
	if record.Chain != chain || record.VaultID != vaultID || record.Epoch != epoch || record.SessionID != sessionID {
		clear(record.Share)
		return nil, errors.New("pending local share metadata mismatch")
	}
	if len(record.Share) == 0 {
		clear(record.Share)
		return nil, errors.New("pending local share is empty")
	}
	return &record, nil
}

func (s *DBLocalShareStore) SavePendingLocalShare(chain string, vaultID uint32, epoch uint64, sessionID string, share []byte) error {
	if s == nil || s.dbManager == nil {
		return errors.New("db manager not available")
	}
	if strings.TrimSpace(sessionID) == "" {
		return errors.New("empty dkg session id")
	}
	if len(share) == 0 {
		return errors.New("empty pending local share")
	}
	if err := s.EnsureReady(); err != nil {
		return err
	}

	key := keys.KeyFrostPendingLocalShare(chain, vaultID, epoch, sessionID)
	if existing, err := s.dbManager.Get(key); err == nil {
		plain, decryptErr := s.cipher.decrypt(key, existing)
		if decryptErr != nil {
			return fmt.Errorf("decrypt existing pending local share: %w", decryptErr)
		}
		defer clear(plain)
		record, decodeErr := decodePendingShareData(plain, chain, vaultID, epoch, sessionID)
		if decodeErr != nil {
			return fmt.Errorf("decode existing pending local share: %w", decodeErr)
		}
		defer clear(record.Share)
		if bytes.Equal(record.Share, share) {
			markerData, markerErr := dkgLocalSessionMarkerData(chain, vaultID, epoch, sessionID)
			if markerErr != nil {
				return markerErr
			}
			s.dbManager.EnqueueSet(keys.KeyFrostLocalDkgSession(chain, vaultID, epoch, sessionID), string(markerData))
			return s.dbManager.ForceFlush()
		}
		return errors.New("conflicting pending local share for dkg session")
	} else if !isLocalShareNotFound(err) {
		return fmt.Errorf("read existing pending local share: %w", err)
	}

	record := &pendingShareData{
		Chain: chain, VaultID: vaultID, Epoch: epoch, SessionID: sessionID, Share: append([]byte(nil), share...),
	}
	defer clear(record.Share)
	recordData, err := json.Marshal(record)
	if err != nil {
		return err
	}
	defer clear(recordData)
	blob, err := s.cipher.encrypt(key, recordData)
	if err != nil {
		return err
	}
	defer clear(blob)
	markerData, err := dkgLocalSessionMarkerData(chain, vaultID, epoch, sessionID)
	if err != nil {
		return err
	}
	s.dbManager.EnqueueSet(key, string(blob))
	s.dbManager.EnqueueSet(keys.KeyFrostLocalDkgSession(chain, vaultID, epoch, sessionID), string(markerData))
	if err := s.dbManager.ForceFlush(); err != nil {
		return fmt.Errorf("flush pending local share: %w", err)
	}
	return nil
}

func (s *DBLocalShareStore) LoadPendingLocalShare(chain string, vaultID uint32, epoch uint64, sessionID string) ([]byte, error) {
	if s == nil || s.dbManager == nil {
		return nil, errors.New("db manager not available")
	}
	if err := s.EnsureReady(); err != nil {
		return nil, err
	}
	key := keys.KeyFrostPendingLocalShare(chain, vaultID, epoch, sessionID)
	data, err := s.dbManager.Get(key)
	if err != nil {
		if isLocalShareNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	plain, err := s.cipher.decrypt(key, data)
	if err != nil {
		return nil, err
	}
	defer clear(plain)
	record, err := decodePendingShareData(plain, chain, vaultID, epoch, sessionID)
	if err != nil {
		return nil, err
	}
	defer clear(record.Share)
	return append([]byte(nil), record.Share...), nil
}

func (s *DBLocalShareStore) PromotePendingLocalShare(chain string, vaultID uint32, epoch uint64, sessionID string) error {
	if s == nil || s.dbManager == nil {
		return errors.New("db manager not available")
	}
	if err := s.EnsureReady(); err != nil {
		return err
	}
	pendingKey := keys.KeyFrostPendingLocalShare(chain, vaultID, epoch, sessionID)
	canonicalKey := keys.KeyFrostLocalShare(chain, vaultID, epoch)
	pendingBlob, err := s.dbManager.Get(pendingKey)
	if err != nil {
		if !isLocalShareNotFound(err) {
			return fmt.Errorf("read pending local share: %w", err)
		}
		if canonicalBlob, canonicalErr := s.dbManager.Get(canonicalKey); canonicalErr == nil && len(canonicalBlob) > 0 {
			canonicalShare, decryptErr := s.cipher.decrypt(canonicalKey, canonicalBlob)
			if decryptErr != nil {
				return fmt.Errorf("validate already-promoted canonical local share: %w", decryptErr)
			}
			clear(canonicalShare)
			                                                                  
			                                                                     
			return nil
		} else if canonicalErr != nil && !isLocalShareNotFound(canonicalErr) {
			return fmt.Errorf("read canonical local share: %w", canonicalErr)
		}
		return errors.New("pending local share not found")
	}
	pendingPlain, err := s.cipher.decrypt(pendingKey, pendingBlob)
	if err != nil {
		return fmt.Errorf("validate pending local share: %w", err)
	}
	defer clear(pendingPlain)
	pending, err := decodePendingShareData(pendingPlain, chain, vaultID, epoch, sessionID)
	if err != nil {
		return fmt.Errorf("decode pending local share: %w", err)
	}
	defer clear(pending.Share)

	if canonicalBlob, canonicalErr := s.dbManager.Get(canonicalKey); canonicalErr == nil {
		canonicalShare, decryptErr := s.cipher.decrypt(canonicalKey, canonicalBlob)
		if decryptErr != nil {
			return fmt.Errorf("decrypt canonical local share: %w", decryptErr)
		}
		defer clear(canonicalShare)
		if !bytes.Equal(canonicalShare, pending.Share) {
			return errors.New("canonical local share conflicts with pending dkg session")
		}
	} else if !isLocalShareNotFound(canonicalErr) {
		return fmt.Errorf("read canonical local share: %w", canonicalErr)
	}

	canonicalBlob, err := s.cipher.encrypt(canonicalKey, pending.Share)
	if err != nil {
		return fmt.Errorf("encrypt canonical local share: %w", err)
	}
	defer clear(canonicalBlob)
	revealRecords, err := s.dbManager.Scan(keys.KeyFrostLocalDkgRevealSessionPrefix(chain, vaultID, epoch, sessionID))
	if err != nil {
		return fmt.Errorf("scan reveal material for promotion: %w", err)
	}
	s.dbManager.EnqueueSet(canonicalKey, string(canonicalBlob))
	s.dbManager.EnqueueDel(pendingKey)
	for revealKey := range revealRecords {
		s.dbManager.EnqueueDel(revealKey)
	}
	if err := s.dbManager.ForceFlush(); err != nil {
		return fmt.Errorf("promote pending local share: %w", err)
	}
	return nil
}

func (s *DBLocalShareStore) HasDkgLocalSession(chain string, vaultID uint32, epoch uint64, sessionID string) (bool, error) {
	if s == nil || s.dbManager == nil {
		return false, errors.New("db manager not available")
	}
	if err := s.EnsureReady(); err != nil {
		return false, err
	}
	_, err := s.dbManager.Get(keys.KeyFrostLocalDkgSession(chain, vaultID, epoch, sessionID))
	if err == nil {
		                                                                   
		                                                                    
		return true, nil
	}
	if !isLocalShareNotFound(err) {
		return false, err
	}

	                                                                           
	                                                                             
	                                         
	if _, err := s.dbManager.Get(keys.KeyFrostPendingLocalShare(chain, vaultID, epoch, sessionID)); err == nil {
		return true, nil
	} else if !isLocalShareNotFound(err) {
		return false, err
	}
	reveals, err := s.dbManager.Scan(keys.KeyFrostLocalDkgRevealSessionPrefix(chain, vaultID, epoch, sessionID))
	if err != nil {
		return false, err
	}
	return len(reveals) > 0, nil
}

func (s *DBLocalShareStore) ListDkgLocalSessions() ([]runtime.DkgLocalSessionRef, error) {
	if s == nil || s.dbManager == nil {
		return nil, errors.New("db manager not available")
	}
	if err := s.EnsureReady(); err != nil {
		return nil, err
	}
	markers, err := s.dbManager.Scan(keys.KeyFrostLocalDkgSessionPrefix())
	if err != nil {
		return nil, err
	}
	pendingRecords, err := s.dbManager.Scan(keys.KeyFrostPendingLocalSharePrefix())
	if err != nil {
		return nil, err
	}

	refsByIdentity := make(map[string]runtime.DkgLocalSessionRef, len(markers)+len(pendingRecords))
	for markerKey, markerData := range markers {
		var ref runtime.DkgLocalSessionRef
		if err := json.Unmarshal(markerData, &ref); err != nil {
			return nil, fmt.Errorf("decode DKG session marker %s: %w", markerKey, err)
		}
		if ref.Chain == "" || ref.SessionID == "" {
			return nil, fmt.Errorf("invalid DKG session marker %s", markerKey)
		}
		expected := keys.KeyFrostLocalDkgSession(ref.Chain, ref.VaultID, ref.Epoch, ref.SessionID)
		if markerKey != expected {
			return nil, fmt.Errorf("DKG session marker key mismatch: got %s want %s", markerKey, expected)
		}
		refsByIdentity[expected] = ref
	}

	for pendingKey, pendingBlob := range pendingRecords {
		plain, err := s.cipher.decrypt(pendingKey, pendingBlob)
		if err != nil {
			return nil, fmt.Errorf("decrypt pending DKG share %s: %w", pendingKey, err)
		}
		var pending pendingShareData
		if err := json.Unmarshal(plain, &pending); err != nil {
			clear(plain)
			clear(pending.Share)
			return nil, fmt.Errorf("decode pending DKG share %s: %w", pendingKey, err)
		}
		clear(plain)
		if pending.Chain == "" || pending.SessionID == "" || len(pending.Share) == 0 {
			clear(pending.Share)
			return nil, fmt.Errorf("invalid pending DKG share metadata for %s", pendingKey)
		}
		expectedPendingKey := keys.KeyFrostPendingLocalShare(
			pending.Chain, pending.VaultID, pending.Epoch, pending.SessionID,
		)
		if pendingKey != expectedPendingKey {
			clear(pending.Share)
			return nil, fmt.Errorf("pending DKG share key mismatch: got %s want %s", pendingKey, expectedPendingKey)
		}
		ref := runtime.DkgLocalSessionRef{
			Chain: pending.Chain, VaultID: pending.VaultID, Epoch: pending.Epoch, SessionID: pending.SessionID,
		}
		identity := keys.KeyFrostLocalDkgSession(ref.Chain, ref.VaultID, ref.Epoch, ref.SessionID)
		refsByIdentity[identity] = ref
		clear(pending.Share)
	}

	refs := make([]runtime.DkgLocalSessionRef, 0, len(refsByIdentity))
	for _, ref := range refsByIdentity {
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Chain != refs[j].Chain {
			return refs[i].Chain < refs[j].Chain
		}
		if refs[i].VaultID != refs[j].VaultID {
			return refs[i].VaultID < refs[j].VaultID
		}
		if refs[i].Epoch != refs[j].Epoch {
			return refs[i].Epoch < refs[j].Epoch
		}
		return refs[i].SessionID < refs[j].SessionID
	})
	return refs, nil
}

func (s *DBLocalShareStore) DeleteDkgSessionArtifacts(chain string, vaultID uint32, epoch uint64, sessionID string) error {
	if s == nil || s.dbManager == nil {
		return errors.New("db manager not available")
	}
	if err := s.EnsureReady(); err != nil {
		return err
	}
	revealRecords, err := s.dbManager.Scan(keys.KeyFrostLocalDkgRevealSessionPrefix(chain, vaultID, epoch, sessionID))
	if err != nil {
		return err
	}
	s.dbManager.EnqueueDel(keys.KeyFrostPendingLocalShare(chain, vaultID, epoch, sessionID))
	s.dbManager.EnqueueDel(keys.KeyFrostLocalDkgSession(chain, vaultID, epoch, sessionID))
	for revealKey := range revealRecords {
		s.dbManager.EnqueueDel(revealKey)
	}
	if err := s.dbManager.ForceFlush(); err != nil {
		return fmt.Errorf("delete dkg session artifacts: %w", err)
	}
	return nil
}

func isLocalShareNotFound(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "not found") ||
		strings.Contains(errStr, "key not found") ||
		strings.Contains(errStr, "does not exist")
}

                                                                                                
                                                                        
func localShareFingerprintForLog(data []byte) string {
	if len(data) == 0 {
		return "len=0"
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("len=%d,sha256_8=%x", len(data), sum[:8])
}

var _ runtime.LocalShareStore = (*DBLocalShareStore)(nil)

type revealData struct {
	Chain         string `json:"chain"`
	VaultID       uint32 `json:"vault_id"`
	Epoch         uint64 `json:"epoch"`
	SessionID     string `json:"session_id"`
	ReceiverIndex int    `json:"receiver_index"`
	Share         []byte `json:"share"`
	EncRand       []byte `json:"enc_rand"`
}

func (s *DBLocalShareStore) SaveDkgRevealMaterial(chain string, vaultID uint32, epoch uint64, sessionID string, receiverIndex int, share, encRand []byte) error {
	if s == nil || s.dbManager == nil {
		return errors.New("db manager not available")
	}
	if strings.TrimSpace(sessionID) == "" {
		return errors.New("empty dkg session id")
	}
	if len(share) == 0 || len(encRand) == 0 {
		return errors.New("empty reveal material")
	}
	if err := s.EnsureReady(); err != nil {
		return err
	}

	key := keys.KeyFrostLocalDkgReveal(chain, vaultID, epoch, sessionID, receiverIndex)
	rd := revealData{
		Chain: chain, VaultID: vaultID, Epoch: epoch, SessionID: sessionID, ReceiverIndex: receiverIndex,
		Share: append([]byte(nil), share...), EncRand: append([]byte(nil), encRand...),
	}
	defer clear(rd.Share)
	defer clear(rd.EncRand)
	if existing, err := s.dbManager.Get(key); err == nil {
		plain, decryptErr := s.cipher.decrypt(key, existing)
		if decryptErr != nil {
			return fmt.Errorf("decrypt existing reveal material: %w", decryptErr)
		}
		defer clear(plain)
		var stored revealData
		if unmarshalErr := json.Unmarshal(plain, &stored); unmarshalErr != nil {
			clear(stored.Share)
			clear(stored.EncRand)
			return fmt.Errorf("decode existing reveal material: %w", unmarshalErr)
		}
		defer clear(stored.Share)
		defer clear(stored.EncRand)
		if stored.Chain != chain || stored.VaultID != vaultID || stored.Epoch != epoch ||
			stored.SessionID != sessionID || stored.ReceiverIndex != receiverIndex {
			return errors.New("existing reveal material metadata mismatch")
		}
		if bytes.Equal(stored.Share, share) && bytes.Equal(stored.EncRand, encRand) {
			markerData, markerErr := dkgLocalSessionMarkerData(chain, vaultID, epoch, sessionID)
			if markerErr != nil {
				return markerErr
			}
			s.dbManager.EnqueueSet(keys.KeyFrostLocalDkgSession(chain, vaultID, epoch, sessionID), string(markerData))
			return s.dbManager.ForceFlush()
		}
		return errors.New("conflicting reveal material for dkg session and receiver")
	} else if !isLocalShareNotFound(err) {
		return fmt.Errorf("read existing reveal material: %w", err)
	}
	data, err := json.Marshal(rd)
	if err != nil {
		return err
	}
	defer clear(data)
	blob, err := s.cipher.encrypt(key, data)
	if err != nil {
		return err
	}
	defer clear(blob)
	markerData, err := dkgLocalSessionMarkerData(chain, vaultID, epoch, sessionID)
	if err != nil {
		return err
	}

	s.dbManager.EnqueueSet(key, string(blob))
	s.dbManager.EnqueueSet(keys.KeyFrostLocalDkgSession(chain, vaultID, epoch, sessionID), string(markerData))
	if err := s.dbManager.ForceFlush(); err != nil {
		return err
	}
	return nil
}

func (s *DBLocalShareStore) LoadDkgRevealMaterial(chain string, vaultID uint32, epoch uint64, sessionID string, receiverIndex int) ([]byte, []byte, error) {
	if s == nil || s.dbManager == nil {
		return nil, nil, errors.New("db manager not available")
	}
	if err := s.EnsureReady(); err != nil {
		return nil, nil, err
	}

	key := keys.KeyFrostLocalDkgReveal(chain, vaultID, epoch, sessionID, receiverIndex)
	data, err := s.dbManager.Get(key)
	if err != nil {
		if isLocalShareNotFound(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	if len(data) == 0 {
		return nil, nil, nil
	}

	plain, err := s.cipher.decrypt(key, data)
	if err != nil {
		return nil, nil, err
	}
	defer clear(plain)

	var rd revealData
	if err := json.Unmarshal(plain, &rd); err != nil {
		clear(rd.Share)
		clear(rd.EncRand)
		return nil, nil, err
	}
	if rd.Chain != chain || rd.VaultID != vaultID || rd.Epoch != epoch ||
		rd.SessionID != sessionID || rd.ReceiverIndex != receiverIndex {
		clear(rd.Share)
		clear(rd.EncRand)
		return nil, nil, errors.New("reveal material metadata mismatch")
	}
	shareOut := append([]byte(nil), rd.Share...)
	randOut := append([]byte(nil), rd.EncRand...)
	clear(rd.Share)
	clear(rd.EncRand)
	return shareOut, randOut, nil
}
