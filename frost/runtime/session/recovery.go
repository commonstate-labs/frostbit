                                    
                                                                                                   

package session

import (
	"encoding/json"
	"errors"
	"sync"
	"time"
)

                                     

var (
	                                 
	ErrRecoveryFailed = errors.New("session recovery failed")
	                                                     
	ErrNoSessionsToRecover = errors.New("no sessions to recover")
	                                                            
	ErrSessionExpired = errors.New("session expired")
)

                                              

                                               
type PersistedSession struct {
	JobID       string           `json:"job_id"`
	VaultID     uint32           `json:"vault_id"`
	Chain       string           `json:"chain"`
	KeyEpoch    uint64           `json:"key_epoch"`
	SignAlgo    int32            `json:"sign_algo"`
	Messages    [][]byte         `json:"messages"`
	State       SignSessionState `json:"state"`
	RetryCount  int              `json:"retry_count"`
	Round       int              `json:"round"`
	StartedAt   time.Time        `json:"started_at"`
	Committee   []Participant    `json:"committee"`
	Threshold   int              `json:"threshold"`
	MyIndex     int              `json:"my_index"`
	SelectedSet []int            `json:"selected_set"`
	StartHeight uint64           `json:"start_height"`
	                                                                 
}

                           
func (p *PersistedSession) ToJSON() ([]byte, error) {
	return json.Marshal(p)
}

                                 
func FromJSON(data []byte) (*PersistedSession, error) {
	var p PersistedSession
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

                                           

                                             
type SessionStorage interface {
	                           
	SaveSession(session *PersistedSession) error
	                                 
	LoadSession(jobID string) (*PersistedSession, error)
	                                                 
	LoadAllSessions() ([]*PersistedSession, error)
	                             
	DeleteSession(jobID string) error
}

                                           

                                                            
type MemorySessionStorage struct {
	mu       sync.RWMutex
	sessions map[string]*PersistedSession
}

                                                   
func NewMemorySessionStorage() *MemorySessionStorage {
	return &MemorySessionStorage{
		sessions: make(map[string]*PersistedSession),
	}
}

func (m *MemorySessionStorage) SaveSession(session *PersistedSession) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[session.JobID] = session
	return nil
}

func (m *MemorySessionStorage) LoadSession(jobID string) (*PersistedSession, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	session, ok := m.sessions[jobID]
	if !ok {
		return nil, ErrSessionNotFound
	}
	return session, nil
}

func (m *MemorySessionStorage) LoadAllSessions() ([]*PersistedSession, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*PersistedSession, 0, len(m.sessions))
	for _, s := range m.sessions {
		result = append(result, s)
	}
	return result, nil
}

func (m *MemorySessionStorage) DeleteSession(jobID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, jobID)
	return nil
}

                                        

                              
type RecoveryConfig struct {
	                                                                           
	MaxRecoveryAge time.Duration
	                                      
	RetryDelay time.Duration
}

                                           
func DefaultRecoveryConfig() *RecoveryConfig {
	return &RecoveryConfig{
		MaxRecoveryAge: 5 * time.Minute,
		RetryDelay:     1 * time.Second,
	}
}

                                        
type RecoveryManager struct {
	storage SessionStorage
	config  *RecoveryConfig
}

                                           
func NewRecoveryManager(storage SessionStorage, config *RecoveryConfig) *RecoveryManager {
	if config == nil {
		config = DefaultRecoveryConfig()
	}
	return &RecoveryManager{
		storage: storage,
		config:  config,
	}
}

                                                 
                                                             
func (r *RecoveryManager) RecoverSessions() ([]*Session, []string, error) {
	persisted, err := r.storage.LoadAllSessions()
	if err != nil {
		return nil, nil, err
	}

	if len(persisted) == 0 {
		return nil, nil, nil
	}

	now := time.Now()
	recovered := make([]*Session, 0)
	expired := make([]string, 0)

	for _, p := range persisted {
		                     
		age := now.Sub(p.StartedAt)
		if age > r.config.MaxRecoveryAge {
			expired = append(expired, p.JobID)
			continue
		}

		                              
		if p.State == SignSessionStateComplete || p.State == SignSessionStateFailed {
			                                 
			_ = r.storage.DeleteSession(p.JobID)
			continue
		}

		               
		session := r.restoreSession(p)
		recovered = append(recovered, session)
	}

	                     
	for _, jobID := range expired {
		_ = r.storage.DeleteSession(jobID)
	}

	return recovered, expired, nil
}

                                                
func (r *RecoveryManager) restoreSession(p *PersistedSession) *Session {
	session := NewSession(SessionParams{
		JobID:       p.JobID,
		VaultID:     p.VaultID,
		Chain:       p.Chain,
		KeyEpoch:    p.KeyEpoch,
		SignAlgo:    p.SignAlgo,
		Messages:    p.Messages,
		Committee:   p.Committee,
		Threshold:   p.Threshold,
		MyIndex:     p.MyIndex,
		StartHeight: p.StartHeight,
		StartedAt:   p.StartedAt,
	})

	session.State = SignSessionStateInit                                              
	session.RetryCount = p.RetryCount
	session.Round = p.Round
	session.SelectedSet = append([]int(nil), p.SelectedSet...)
	return session
}

                                 
func (r *RecoveryManager) PersistSession(session *Session) error {
	session.mu.RLock()
	defer session.mu.RUnlock()

	p := &PersistedSession{
		JobID:       session.JobID,
		VaultID:     session.VaultID,
		Chain:       session.Chain,
		KeyEpoch:    session.KeyEpoch,
		SignAlgo:    session.SignAlgo,
		Messages:    session.Messages,
		State:       session.State,
		RetryCount:  session.RetryCount,
		Round:       session.Round,
		StartedAt:   session.StartedAt,
		Committee:   session.Committee,
		Threshold:   session.Threshold,
		MyIndex:     session.MyIndex,
		SelectedSet: append([]int(nil), session.SelectedSet...),
		StartHeight: session.StartHeight,
	}

	return r.storage.SaveSession(p)
}

                                   
func (r *RecoveryManager) DeleteSession(jobID string) error {
	return r.storage.DeleteSession(jobID)
}

                                              
func (r *RecoveryManager) GetPendingCount() (int, error) {
	sessions, err := r.storage.LoadAllSessions()
	if err != nil {
		return 0, err
	}

	count := 0
	now := time.Now()
	for _, p := range sessions {
		if p.State != SignSessionStateComplete && p.State != SignSessionStateFailed {
			age := now.Sub(p.StartedAt)
			if age <= r.config.MaxRecoveryAge {
				count++
			}
		}
	}
	return count, nil
}
