                               
                                                                                       

package session

import (
	"errors"
	"sync"
	"time"
)

                                         

                            
type DKGPhase int

const (
	DKGPhaseInit       DKGPhase = iota
	DKGPhaseCommitting                            
	DKGPhaseSharing                                          
	DKGPhaseResolving                                    
	DKGPhaseKeyReady                                 
	DKGPhaseFailed                       
)

func (p DKGPhase) String() string {
	switch p {
	case DKGPhaseInit:
		return "INIT"
	case DKGPhaseCommitting:
		return "COMMITTING"
	case DKGPhaseSharing:
		return "SHARING"
	case DKGPhaseResolving:
		return "RESOLVING"
	case DKGPhaseKeyReady:
		return "KEY_READY"
	case DKGPhaseFailed:
		return "FAILED"
	default:
		return "UNKNOWN"
	}
}

                                     

var (
	ErrDKGInvalidPhase      = errors.New("invalid DKG phase")
	ErrDKGSessionClosed     = errors.New("DKG session closed")
	ErrDKGDuplicateCommit   = errors.New("duplicate commitment")
	ErrDKGDuplicateShare    = errors.New("duplicate share")
	ErrDKGCommitmentMissing = errors.New("commitment missing for dealer")
)

                                           

                                                
type DKGSessionParams struct {
	SessionID string
	Chain     string
	VaultID   uint32
	EpochID   uint64
	SignAlgo  int32
	Committee []string                               
	Threshold int                 
	MyIndex   int                                                    
	MyAddress string                     
}

                                     

                                 
type DKGCommitmentData struct {
	DealerIndex      int
	DealerAddress    string
	CommitmentPoints [][]byte                         
	AI0              []byte                             
	ReceivedAt       time.Time
}

                                   
type DKGShareData struct {
	DealerIndex   int
	DealerAddress string
	Ciphertext    []byte
	ReceivedAt    time.Time
}

                                
type DKGComplaintData struct {
	DealerIndex   int
	ReceiverIndex int
	Bond          uint64
	CreatedAt     time.Time
	Status        string                                
}

                                    

                                 
type DKGSession struct {
	mu sync.RWMutex

	         
	SessionID string
	Chain     string
	VaultID   uint32
	EpochID   uint64
	SignAlgo  int32

	            
	Committee []string
	Threshold int
	MyIndex   int           
	MyAddress string

	         
	Phase       DKGPhase
	StartedAt   time.Time
	CompletedAt time.Time
	closed      bool

	                  
	Commitments map[int]*DKGCommitmentData                               
	Shares      map[int]*DKGShareData                                                
	Complaints  map[string]*DKGComplaintData                                  

	                                               
	LocalCommitmentPoints [][]byte
	LocalAI0              []byte

	         
	LocalShare  []byte                      
	GroupPubkey []byte             
}

                                        
func NewDKGSession(params DKGSessionParams) *DKGSession {
	return &DKGSession{
		SessionID:   params.SessionID,
		Chain:       params.Chain,
		VaultID:     params.VaultID,
		EpochID:     params.EpochID,
		SignAlgo:    params.SignAlgo,
		Committee:   params.Committee,
		Threshold:   params.Threshold,
		MyIndex:     params.MyIndex,
		MyAddress:   params.MyAddress,
		Phase:       DKGPhaseInit,
		StartedAt:   time.Now(),
		Commitments: make(map[int]*DKGCommitmentData),
		Shares:      make(map[int]*DKGShareData),
		Complaints:  make(map[string]*DKGComplaintData),
	}
}

                                           

                                                     
func (s *DKGSession) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrDKGSessionClosed
	}
	if s.Phase != DKGPhaseInit {
		return ErrDKGInvalidPhase
	}

	s.Phase = DKGPhaseCommitting
	return nil
}

                                                              
func (s *DKGSession) TransitionToSharing() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrDKGSessionClosed
	}
	if s.Phase != DKGPhaseCommitting {
		return ErrDKGInvalidPhase
	}

	                                             
	if !s.hasEnoughCommitmentsLocked() {
		return errors.New("not enough commitments")
	}

	s.Phase = DKGPhaseSharing
	return nil
}

                                                 
func (s *DKGSession) TransitionToResolving() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrDKGSessionClosed
	}
	if s.Phase != DKGPhaseSharing {
		return ErrDKGInvalidPhase
	}

	s.Phase = DKGPhaseResolving
	return nil
}

                                                
func (s *DKGSession) TransitionToKeyReady(localShare, groupPubkey []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrDKGSessionClosed
	}
	if s.Phase != DKGPhaseSharing && s.Phase != DKGPhaseResolving {
		return ErrDKGInvalidPhase
	}

	s.LocalShare = localShare
	s.GroupPubkey = groupPubkey
	s.Phase = DKGPhaseKeyReady
	s.CompletedAt = time.Now()
	return nil
}

                                
func (s *DKGSession) MarkFailed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Phase = DKGPhaseFailed
	s.CompletedAt = time.Now()
}

                                           

                                   
func (s *DKGSession) AddCommitment(dealerIndex int, dealerAddr string, points [][]byte, ai0 []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrDKGSessionClosed
	}
	if s.Phase != DKGPhaseCommitting {
		return ErrDKGInvalidPhase
	}
	if _, exists := s.Commitments[dealerIndex]; exists {
		return ErrDKGDuplicateCommit
	}

	s.Commitments[dealerIndex] = &DKGCommitmentData{
		DealerIndex:      dealerIndex,
		DealerAddress:    dealerAddr,
		CommitmentPoints: points,
		AI0:              ai0,
		ReceivedAt:       time.Now(),
	}
	return nil
}

                              
func (s *DKGSession) AddShare(dealerIndex int, dealerAddr string, ciphertext []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrDKGSessionClosed
	}
	if s.Phase != DKGPhaseSharing {
		return ErrDKGInvalidPhase
	}
	if _, exists := s.Shares[dealerIndex]; exists {
		return ErrDKGDuplicateShare
	}

	                                           
	if _, hasCommit := s.Commitments[dealerIndex]; !hasCommit {
		return ErrDKGCommitmentMissing
	}

	s.Shares[dealerIndex] = &DKGShareData{
		DealerIndex:   dealerIndex,
		DealerAddress: dealerAddr,
		Ciphertext:    ciphertext,
		ReceivedAt:    time.Now(),
	}
	return nil
}

                                                       
func (s *DKGSession) SetLocalCommitment(points [][]byte, ai0 []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LocalCommitmentPoints = points
	s.LocalAI0 = ai0
}

                                     

                              
func (s *DKGSession) GetPhase() DKGPhase {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Phase
}

                                                            
func (s *DKGSession) HasEnoughCommitments() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.hasEnoughCommitmentsLocked()
}

func (s *DKGSession) hasEnoughCommitmentsLocked() bool {
	return len(s.Commitments) >= s.minParticipantsLocked()
}

                                                        
func (s *DKGSession) HasEnoughShares() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.Shares) >= s.minParticipantsLocked()
}

func (s *DKGSession) minParticipantsLocked() int {
	if s.Threshold <= 0 {
		return 1
	}
	return s.Threshold
}

                                              
func (s *DKGSession) GetCommitment(dealerIndex int) (*DKGCommitmentData, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.Commitments[dealerIndex]
	return c, ok
}

                                                  
func (s *DKGSession) GetShare(dealerIndex int) (*DKGShareData, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sh, ok := s.Shares[dealerIndex]
	return sh, ok
}

                                                
func (s *DKGSession) GetAllCommitments() map[int]*DKGCommitmentData {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[int]*DKGCommitmentData, len(s.Commitments))
	for k, v := range s.Commitments {
		result[k] = v
	}
	return result
}

                                             
func (s *DKGSession) GetAllShares() map[int]*DKGShareData {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[int]*DKGShareData, len(s.Shares))
	for k, v := range s.Shares {
		result[k] = v
	}
	return result
}

                                                                    
func (s *DKGSession) GetAllAI0s() [][]byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([][]byte, 0, len(s.Commitments))
	for i := 1; i <= len(s.Committee); i++ {
		if c, ok := s.Commitments[i]; ok {
			result = append(result, c.AI0)
		}
	}
	return result
}

                                         
func (s *DKGSession) GetLocalShare() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.LocalShare == nil {
		return nil
	}
	result := make([]byte, len(s.LocalShare))
	copy(result, s.LocalShare)
	return result
}

                                 
func (s *DKGSession) GetGroupPubkey() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.GroupPubkey == nil {
		return nil
	}
	result := make([]byte, len(s.GroupPubkey))
	copy(result, s.GroupPubkey)
	return result
}

                     
func (s *DKGSession) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
}

                                       
func (s *DKGSession) IsClosed() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.closed
}
