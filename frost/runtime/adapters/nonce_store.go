package adapters

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	"dex/frost/runtime/session"
	iface "dex/interfaces"
	"dex/keys"
)

const (
	nonceRecordVersion = byte(1)
	nonceRecordSize    = 1 + 8 + sha256.Size
)

                                                                          
                                                                            
type DBNonceStore struct {
	dbManager iface.NodeLocalWriter
}

                                                                    
                                                                            
                                                                            
var dbNonceStoreMu sync.Mutex

func NewDBNonceStore(dbManager iface.NodeLocalWriter) *DBNonceStore {
	return &DBNonceStore{dbManager: dbManager}
}

func (s *DBNonceStore) SaveNonce(nonce []byte, msgHash []byte, keyEpoch uint64) error {
	return s.SaveNonceBatch([]session.NonceRecord{{Nonce: nonce, MsgHash: msgHash, KeyEpoch: keyEpoch}})
}

                                                                              
                                                                           
                                                                             
                                                             
func (s *DBNonceStore) SaveNonceBatch(records []session.NonceRecord) error {
	if s == nil || s.dbManager == nil {
		return errors.New("db manager not available")
	}
	if len(records) == 0 {
		return nil
	}

	dbNonceStoreMu.Lock()
	defer dbNonceStoreMu.Unlock()

	keysByRecord := make([]string, len(records))
	valuesByRecord := make([]string, len(records))
	seen := make(map[string]struct{}, len(records))
	for i, record := range records {
		if len(record.Nonce) == 0 {
			return errors.New("empty nonce")
		}
		if len(record.MsgHash) != sha256.Size {
			return fmt.Errorf("invalid nonce message hash length: %d", len(record.MsgHash))
		}
		key := nonceStoreKey(record.Nonce)
		if _, duplicate := seen[key]; duplicate {
			return session.ErrNonceAlreadyBound
		}
		seen[key] = struct{}{}

		_, _, exists, err := s.loadNonce(record.Nonce)
		if err != nil {
			return err
		}
		if exists {
			return session.ErrNonceAlreadyBound
		}

		recordData := make([]byte, nonceRecordSize)
		recordData[0] = nonceRecordVersion
		binary.BigEndian.PutUint64(recordData[1:9], record.KeyEpoch)
		copy(recordData[9:], record.MsgHash)
		keysByRecord[i] = key
		valuesByRecord[i] = string(recordData)
	}

	for i := range keysByRecord {
		s.dbManager.EnqueueSet(keysByRecord[i], valuesByRecord[i])
	}
	if err := flushNonceTombstones(s.dbManager); err != nil {
		return fmt.Errorf("flush burned nonce tombstone: %w", err)
	}
	return nil
}

type nodeLocalHotFlusher interface {
	ForceFlushHot() error
}

func flushNonceTombstones(writer iface.NodeLocalWriter) error {
	if hot, ok := writer.(nodeLocalHotFlusher); ok {
		return hot.ForceFlushHot()
	}
	return writer.ForceFlush()
}

func (s *DBNonceStore) GetNonceBind(nonce []byte) ([]byte, uint64, bool) {
	if s == nil || s.dbManager == nil || len(nonce) == 0 {
		return nil, 0, true               
	}

	dbNonceStoreMu.Lock()
	defer dbNonceStoreMu.Unlock()
	msgHash, keyEpoch, exists, err := s.loadNonce(nonce)
	if err != nil {
		return nil, 0, true                                                    
	}
	return msgHash, keyEpoch, exists
}

                                                                           
                                                                            
func (s *DBNonceStore) DeleteNonce([]byte) error { return nil }

func (s *DBNonceStore) loadNonce(nonce []byte) ([]byte, uint64, bool, error) {
	data, err := s.dbManager.Get(nonceStoreKey(nonce))
	if err != nil {
		if isLocalShareNotFound(err) {
			return nil, 0, false, nil
		}
		return nil, 0, false, fmt.Errorf("load burned nonce tombstone: %w", err)
	}
	if len(data) != nonceRecordSize || data[0] != nonceRecordVersion {
		return nil, 0, false, errors.New("invalid burned nonce tombstone")
	}
	msgHash := make([]byte, sha256.Size)
	copy(msgHash, data[9:])
	return msgHash, binary.BigEndian.Uint64(data[1:9]), true, nil
}

func nonceStoreKey(nonce []byte) string {
	fingerprint := sha256.Sum256(nonce)
	return keys.KeyFrostLocalNonce(hex.EncodeToString(fingerprint[:]))
}

var _ session.NonceStore = (*DBNonceStore)(nil)
var _ session.BatchNonceStore = (*DBNonceStore)(nil)
