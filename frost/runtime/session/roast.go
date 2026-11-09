                                 
                                                                          

package session

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

                               

var (
	ErrInvalidState  = errors.New("invalid state")
	ErrSessionClosed = errors.New("session closed")
	ErrRoundMismatch = errors.New("session round mismatch")
)

                                                                        
                                       
type RoundMismatchError struct {
	Expected int
	Got      int
}

func (e *RoundMismatchError) Error() string {
	return fmt.Sprintf("%s: expected %d, got %d", ErrRoundMismatch, e.Expected, e.Got)
}

func (e *RoundMismatchError) Unwrap() error { return ErrRoundMismatch }

                                                                 
type SessionParams struct {
	JobID       string
	VaultID     uint32
	Chain       string
	KeyEpoch    uint64
	SignAlgo    int32
	Messages    [][]byte
	Committee   []Participant
	Threshold   int
	MyIndex     int
	StartHeight uint64
	StartedAt   time.Time
	Tweaks      [][]byte                              
}

                                                           
type NonceData struct {
	ParticipantIndex int
	HidingNonces     [][]byte
	BindingNonces    [][]byte
	ReceivedAt       time.Time
	Round            int                               
}

                                                          
type ShareData struct {
	ParticipantIndex int
	Shares           [][]byte
	ReceivedAt       time.Time
	Round            int                               
}

                                                             
type Session struct {
	mu sync.RWMutex                                  

	JobID    string                            
	VaultID  uint32                                                 
	Chain    string                                  
	KeyEpoch uint64                                                         
	SignAlgo int32                                                

	Messages  [][]byte                                                                     
	Committee []Participant                                              
	Threshold int                                                          
	MyIndex   int                                               

	SelectedSet []int                                                           
	Nonces      map[int]*NonceData                                                      
	Shares      map[int]*ShareData                                                     

	State       SignSessionState                                                                           
	RetryCount  int                                                           
	Round       int                                                                                               
	StartHeight uint64                                            
	StartedAt   time.Time                             
	CompletedAt time.Time                             

	FinalSignatures    [][]byte                                  
	CachedNoncePayload []byte                                                                     

	closed bool                                                       

	Tweaks [][]byte                                                                     
}

                                                                
func NewSession(params SessionParams) *Session {
	startedAt := params.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now()
	}

	return &Session{
		JobID:       params.JobID,
		VaultID:     params.VaultID,
		Chain:       params.Chain,
		KeyEpoch:    params.KeyEpoch,
		SignAlgo:    params.SignAlgo,
		Messages:    params.Messages,
		Committee:   params.Committee,
		Threshold:   params.Threshold,
		MyIndex:     params.MyIndex,
		Nonces:      make(map[int]*NonceData),
		Shares:      make(map[int]*ShareData),
		State:       SignSessionStateInit,
		StartHeight: params.StartHeight,
		StartedAt:   startedAt,
		Tweaks:      params.Tweaks,
	}
}

                                                   
func (s *Session) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrSessionClosed
	}
	if s.State != SignSessionStateInit {
		return ErrInvalidState
	}

	s.selectInitialSetLocked()
	s.State = SignSessionStateCollectingNonces
	return nil
}

                                                             
func (s *Session) SelectInitialSet() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.selectInitialSetLocked()
}

func (s *Session) selectInitialSetLocked() {
	minSigners := s.minSigners()
	s.SelectedSet = make([]int, 0, minSigners)
	for i := 0; i < len(s.Committee) && len(s.SelectedSet) < minSigners; i++ {
		s.SelectedSet = append(s.SelectedSet, i)
	}
}

                                                                    
func (s *Session) ResetForRetry(maxRetries int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return false
	}

	s.RetryCount++
	if maxRetries > 0 && s.RetryCount > maxRetries {
		return false
	}

	s.Round++
	s.selectAlternativeSetLocked()
	s.Nonces = make(map[int]*NonceData)
	s.Shares = make(map[int]*ShareData)
	s.CachedNoncePayload = nil
	s.State = SignSessionStateCollectingNonces
	return true
}

func (s *Session) selectAlternativeSetLocked() {
	if len(s.Committee) == 0 {
		s.SelectedSet = nil
		return
	}

	minSigners := s.minSigners()
	offset := s.RetryCount % len(s.Committee)
	s.SelectedSet = make([]int, 0, minSigners)
	for i := 0; i < len(s.Committee) && len(s.SelectedSet) < minSigners; i++ {
		idx := (i + offset) % len(s.Committee)
		s.SelectedSet = append(s.SelectedSet, idx)
	}
}

func (s *Session) addNonceLocked(round, participantIndex int, hidingNonces, bindingNonces [][]byte) error {
	if s.closed {
		return ErrSessionClosed
	}
	if s.State != SignSessionStateCollectingNonces {
		return ErrInvalidState
	}

	s.Nonces[participantIndex] = &NonceData{
		ParticipantIndex: participantIndex,
		HidingNonces:     hidingNonces,
		BindingNonces:    bindingNonces,
		ReceivedAt:       time.Now(),
		Round:            round,
	}
	return nil
}

                                                                  
func (s *Session) AddNonce(participantIndex int, hidingNonces, bindingNonces [][]byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addNonceLocked(s.Round, participantIndex, hidingNonces, bindingNonces)
}

                                                                           
func (s *Session) AddNonceForRound(round, participantIndex int, hidingNonces, bindingNonces [][]byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if round != s.Round {
		return &RoundMismatchError{Expected: s.Round, Got: round}
	}
	return s.addNonceLocked(round, participantIndex, hidingNonces, bindingNonces)
}

func (s *Session) addShareLocked(round, participantIndex int, shares [][]byte) error {
	if s.closed {
		return ErrSessionClosed
	}
	if s.State != SignSessionStateCollectingShares {
		return ErrInvalidState
	}

	                                                                                  
	if n, ok := s.Nonces[participantIndex]; !ok || n.Round != round {
		return fmt.Errorf("no matching nonce for round %d from participant %d", round, participantIndex)
	}

	s.Shares[participantIndex] = &ShareData{
		ParticipantIndex: participantIndex,
		Shares:           shares,
		ReceivedAt:       time.Now(),
		Round:            round,
	}
	return nil
}

                                                                 
func (s *Session) AddShare(participantIndex int, shares [][]byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addShareLocked(s.Round, participantIndex, shares)
}

                                                                           
func (s *Session) AddShareForRound(round, participantIndex int, shares [][]byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if round != s.Round {
		return &RoundMismatchError{Expected: s.Round, Got: round}
	}
	return s.addShareLocked(round, participantIndex, shares)
}

                                                         
func (s *Session) HasEnoughNonces() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	minSigners := s.minSigners()
	count := 0
	for _, idx := range s.SelectedSet {
		if n, ok := s.Nonces[idx]; ok && n.Round == s.Round {
			count++
		}
	}
	return count >= minSigners
}

                                                         
func (s *Session) HasEnoughShares() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	minSigners := s.minSigners()
	count := 0
	for _, idx := range s.SelectedSet {
		if sh, ok := s.Shares[idx]; ok && sh.Round == s.Round {
			count++
		}
	}
	return count >= minSigners
}

                                              
func (s *Session) GetState() SignSessionState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.State
}

                                                        
func (s *Session) CurrentRound() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Round
}

                                      
func (s *Session) SetState(state SignSessionState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.State = state
}

                                                          
func (s *Session) SelectedSetSnapshot() []int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]int, len(s.SelectedSet))
	copy(result, s.SelectedSet)
	return result
}

                                                           
func (s *Session) GetNonce(index int) (*NonceData, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	nonce, ok := s.Nonces[index]
	return nonce, ok
}

                                                           
func (s *Session) GetShare(index int) (*ShareData, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	share, ok := s.Shares[index]
	return share, ok
}

                                                         
func (s *Session) GetFinalSignatures() [][]byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([][]byte, len(s.FinalSignatures))
	copy(result, s.FinalSignatures)
	return result
}

                                                                        
func (s *Session) MarkCompleted(signatures [][]byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.FinalSignatures = signatures
	s.State = SignSessionStateComplete
	s.CompletedAt = time.Now()
}

                                                                 
func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
}

func (s *Session) minSigners() int {
	minSigners := s.Threshold
	if minSigners <= 0 {
		minSigners = 1
	}
	return minSigners
}
