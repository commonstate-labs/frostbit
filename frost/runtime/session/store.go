                                 
                                               

package session

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
)

                                     

var (
	                                                                                   
	ErrNonceAlreadyBound = errors.New("nonce already bound or burned")
	                                   
	ErrNonceNotFound = errors.New("nonce not found")
	                                  
	ErrNonceExpired = errors.New("nonce expired")
	                                     
	ErrSessionNotFound = errors.New("session not found")
)

                                     

                                         
type NonceStore interface {
	                                
	                             
	                                 
	                         
	                                                                
	SaveNonce(nonce []byte, msgHash []byte, keyEpoch uint64) error

	                                         
	                                      
	GetNonceBind(nonce []byte) (msgHash []byte, keyEpoch uint64, exists bool)
}

                                                                            
                                                                         
                                   
type NonceRecord struct {
	Nonce    []byte
	MsgHash  []byte
	KeyEpoch uint64
}

                                                                            
                                                                             
                                                              
type BatchNonceStore interface {
	SaveNonceBatch(records []NonceRecord) error
}

                                                                     
type NonceBinding struct {
	Nonce    []byte
	Message  []byte
	KeyEpoch uint64
}

                                     

                               
type nonceBind struct {
	MsgHash  []byte
	KeyEpoch uint64
}

                                                                         
                                                                      
type MemoryNonceStore struct {
	mu     sync.RWMutex
	nonces map[string]*nonceBind                    
}

                                                
func NewMemoryNonceStore() *MemoryNonceStore {
	return &MemoryNonceStore{nonces: make(map[string]*nonceBind)}
}

func (m *MemoryNonceStore) SaveNonce(nonce []byte, msgHash []byte, keyEpoch uint64) error {
	return m.SaveNonceBatch([]NonceRecord{{Nonce: nonce, MsgHash: msgHash, KeyEpoch: keyEpoch}})
}

func (m *MemoryNonceStore) SaveNonceBatch(records []NonceRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	seen := make(map[string]struct{}, len(records))
	for _, record := range records {
		key := hex.EncodeToString(record.Nonce)
		if _, duplicate := seen[key]; duplicate {
			return ErrNonceAlreadyBound
		}
		seen[key] = struct{}{}
		if _, ok := m.nonces[key]; ok {
			return ErrNonceAlreadyBound
		}
	}
	for _, record := range records {
		key := hex.EncodeToString(record.Nonce)
		m.nonces[key] = &nonceBind{MsgHash: cloneBytes(record.MsgHash), KeyEpoch: record.KeyEpoch}
	}
	return nil
}

func (m *MemoryNonceStore) GetNonceBind(nonce []byte) ([]byte, uint64, bool) {
	key := hex.EncodeToString(nonce)
	m.mu.RLock()
	defer m.mu.RUnlock()
	bind, ok := m.nonces[key]
	if !ok {
		return nil, 0, false
	}
	return cloneBytes(bind.MsgHash), bind.KeyEpoch, true
}

                                                                         
func (m *MemoryNonceStore) DeleteNonce(nonce []byte) error {
	_ = nonce
	return nil
}

                                                    
func (m *MemoryNonceStore) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.nonces)
}

                                                                        
                                                     
func (m *MemoryNonceStore) CleanupExpired(currentEpoch uint64) int {
	_ = currentEpoch
	return 0
}

                                     

                                                           
type SessionStore struct {
	mu         sync.RWMutex
	nonceStore NonceStore
	active     map[string][]byte
}

                                     
func NewSessionStore(nonceStore NonceStore) *SessionStore {
	if nonceStore == nil {
		nonceStore = NewMemoryNonceStore()
	}
	return &SessionStore{
		nonceStore: nonceStore,
		active:     make(map[string][]byte),
	}
}

                                   
                                                                      
                                                                                          
                                                                                               
func (s *SessionStore) BindNonce(nonce []byte, msg []byte, keyEpoch uint64) error {
	return s.BindNonceBatch([]NonceBinding{{Nonce: nonce, Message: msg, KeyEpoch: keyEpoch}})
}

                                                                           
                                                                         
                                                                      
func (s *SessionStore) BindNonceBatch(bindings []NonceBinding) error {
	if len(bindings) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	records := make([]NonceRecord, 0, len(bindings))
	nonceKeys := make([]string, 0, len(bindings))
	seen := make(map[string]struct{}, len(bindings))
	for _, binding := range bindings {
		nonceKey := hex.EncodeToString(binding.Nonce)
		if _, duplicate := seen[nonceKey]; duplicate {
			return ErrNonceAlreadyBound
		}
		seen[nonceKey] = struct{}{}
		if _, ok := s.active[nonceKey]; ok {
			return ErrNonceAlreadyBound
		}

		                                                                       
		                                                                         
		_, _, exists := s.nonceStore.GetNonceBind(binding.Nonce)
		if exists {
			return ErrNonceAlreadyBound
		}

		records = append(records, NonceRecord{
			Nonce:    cloneBytes(binding.Nonce),
			MsgHash:  hashMessage(binding.Message),
			KeyEpoch: binding.KeyEpoch,
		})
		nonceKeys = append(nonceKeys, nonceKey)
	}

	if batchStore, ok := s.nonceStore.(BatchNonceStore); ok {
		if err := batchStore.SaveNonceBatch(records); err != nil {
			return err
		}
	} else {
		for _, record := range records {
			if err := s.nonceStore.SaveNonce(record.Nonce, record.MsgHash, record.KeyEpoch); err != nil {
				return err
			}
		}
	}

	for i, nonceKey := range nonceKeys {
		s.active[nonceKey] = cloneBytes(records[i].MsgHash)
	}
	return nil
}

                                                  
func (s *SessionStore) GetNonceBind(nonce []byte) ([]byte, uint64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.nonceStore.GetNonceBind(nonce)
}

                                                                   
                                                                               
func (s *SessionStore) ValidateNonceForMessage(nonce []byte, msg []byte) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	msgHash := hashMessage(msg)
	activeHash, exists := s.active[hex.EncodeToString(nonce)]
	return exists && bytesEqual(activeHash, msgHash)
}

                                                                        
                                                                               
func (s *SessionStore) ReleaseNonce(nonce []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.active, hex.EncodeToString(nonce))
	return nil
}

                                     

func hashMessage(msg []byte) []byte {
	h := sha256.Sum256(msg)
	return h[:]
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func cloneBytes(src []byte) []byte {
	if len(src) == 0 {
		return nil
	}
	out := make([]byte, len(src))
	copy(out, src)
	return out
}
