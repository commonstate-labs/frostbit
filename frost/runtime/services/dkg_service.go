                                        
                                                                  

package services

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"dex/frost/runtime/session"
	"dex/frost/runtime/workers"
	"dex/pb"
)

                                     

                                                     
type DKGService interface {
	                                    
	StartDKGSession(ctx context.Context, params *DKGSessionParams) (sessionID string, err error)

	                                                     
	SubmitCommitment(ctx context.Context, sessionID string, commitment *DKGCommitment) error

	                                                      
	SubmitShare(ctx context.Context, sessionID string, share *DKGShare) error

	                                 
	GetDKGStatus(sessionID string) (*DKGStatus, error)

	                                      
	WaitForCompletion(ctx context.Context, sessionID string, timeout time.Duration) (*DKGResult, error)
}

                                    
type DKGSessionParams struct {
	SessionID string
	Chain     string
	VaultID   uint32
	EpochID   uint64
	SignAlgo  pb.SignAlgo
	Committee []string                               
	Threshold int                 
	MyIndex   int                                                    
	MyAddress string                     
}

                                 
type DKGCommitment struct {
	DealerIndex      int
	DealerAddress    string
	CommitmentPoints [][]byte                         
	AI0              []byte                             
}

                                   
type DKGShare struct {
	DealerIndex   int
	DealerAddress string
	ReceiverIndex int
	ReceiverID    string
	Ciphertext    []byte
}

                       
type DKGStatus struct {
	SessionID   string
	Chain       string
	VaultID     uint32
	EpochID     uint64
	Phase       string                                                                             
	Progress    float64             
	StartedAt   time.Time
	CompletedAt *time.Time
	Error       error

	               
	CommitmentCount int                         
	ShareCount      int                          
	QualifiedCount  int                         
}

                       
type DKGResult struct {
	SessionID   string
	Chain       string
	VaultID     uint32
	EpochID     uint64
	GroupPubkey []byte
	LocalShare  []byte
	CompletedAt time.Time
}

                               

                                                             
type TransitionDKGService struct {
	mu sync.RWMutex

	transitionWorker *workers.TransitionWorker
	sessions         map[string]*dkgSessionWrapper                        
}

                                                           
type dkgSessionWrapper struct {
	mu sync.RWMutex

	sessionID  string
	params     *DKGSessionParams
	status     *DKGStatus
	dkgSession *session.DKGSession                                    

	               
	doneCh chan struct{}
	result *DKGResult
	err    error
}

                                                                 
func NewTransitionDKGService(transitionWorker *workers.TransitionWorker) *TransitionDKGService {
	return &TransitionDKGService{
		transitionWorker: transitionWorker,
		sessions:         make(map[string]*dkgSessionWrapper),
	}
}

                                    
func (s *TransitionDKGService) StartDKGSession(ctx context.Context, params *DKGSessionParams) (string, error) {
	if params == nil {
		return "", errors.New("params is nil")
	}

	sessionID := params.SessionID
	if sessionID == "" {
		sessionID = generateDKGSessionID(params.Chain, params.VaultID, params.EpochID)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	                        
	if _, exists := s.sessions[sessionID]; exists {
		return sessionID, nil          
	}

	                  
	wrapper := &dkgSessionWrapper{
		sessionID: sessionID,
		params:    params,
		status: &DKGStatus{
			SessionID:       sessionID,
			Chain:           params.Chain,
			VaultID:         params.VaultID,
			EpochID:         params.EpochID,
			Phase:           "INIT",
			Progress:        0.0,
			StartedAt:       time.Now(),
			CommitmentCount: 0,
			ShareCount:      0,
			QualifiedCount:  0,
		},
		doneCh: make(chan struct{}),
	}

	                                   
	dkgSessionParams := session.DKGSessionParams{
		SessionID: sessionID,
		Chain:     params.Chain,
		VaultID:   params.VaultID,
		EpochID:   params.EpochID,
		SignAlgo:  int32(params.SignAlgo),
		Committee: params.Committee,
		Threshold: params.Threshold,
		MyIndex:   params.MyIndex,
		MyAddress: params.MyAddress,
	}
	wrapper.dkgSession = session.NewDKGSession(dkgSessionParams)

	s.sessions[sessionID] = wrapper

	                                                 
	if err := s.transitionWorker.StartSession(ctx, params.Chain, params.VaultID, params.EpochID, params.SignAlgo); err != nil {
		delete(s.sessions, sessionID)
		return "", err
	}

	                           
	go s.monitorSession(ctx, wrapper)

	return sessionID, nil
}

                                            
func generateDKGSessionID(chain string, vaultID uint32, epochID uint64) string {
	return fmt.Sprintf("%s_%d_%d_%d", chain, vaultID, epochID, 1)
}

                                          
func (s *TransitionDKGService) monitorSession(ctx context.Context, wrapper *dkgSessionWrapper) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			wrapper.setError(ctx.Err())
			return
		case <-ticker.C:
			                     
			wrapper.mu.RLock()
			dkgSession := wrapper.dkgSession
			wrapper.mu.RUnlock()

			if dkgSession == nil {
				continue
			}

			phase := dkgSession.GetPhase()
			wrapper.updateStatus(phase)

			                     
			if phase == session.DKGPhaseKeyReady {
				                                         
				localShare := dkgSession.GetLocalShare()
				groupPubkey := dkgSession.GetGroupPubkey()

				if localShare != nil && len(localShare) > 0 && groupPubkey != nil && len(groupPubkey) > 0 {
					result := &DKGResult{
						SessionID:   wrapper.sessionID,
						Chain:       wrapper.params.Chain,
						VaultID:     wrapper.params.VaultID,
						EpochID:     wrapper.params.EpochID,
						GroupPubkey: groupPubkey,
						LocalShare:  localShare,
						CompletedAt: time.Now(),
					}
					wrapper.setResult(result)
				} else {
					wrapper.setError(errors.New("DKG completed but no result"))
				}
				return
			}

			if phase == session.DKGPhaseFailed {
				wrapper.setError(errors.New("DKG failed"))
				return
			}
		}
	}
}

                            
func (w *dkgSessionWrapper) updateStatus(phase session.DKGPhase) {
	w.mu.Lock()
	defer w.mu.Unlock()

	var phaseStr string
	var progress float64

	switch phase {
	case session.DKGPhaseInit:
		phaseStr = "INIT"
		progress = 0.0
	case session.DKGPhaseCommitting:
		phaseStr = "COMMITTING"
		progress = 0.2
	case session.DKGPhaseSharing:
		phaseStr = "SHARING"
		progress = 0.5
	case session.DKGPhaseResolving:
		phaseStr = "RESOLVING"
		progress = 0.8
	case session.DKGPhaseKeyReady:
		phaseStr = "KEY_READY"
		progress = 1.0
		now := time.Now()
		w.status.CompletedAt = &now
	case session.DKGPhaseFailed:
		phaseStr = "FAILED"
		progress = 0.0
	}

	w.status.Phase = phaseStr
	w.status.Progress = progress

	                     
	if w.dkgSession != nil {
		commitments := w.dkgSession.GetAllCommitments()
		shares := w.dkgSession.GetAllShares()
		w.status.CommitmentCount = len(commitments)
		w.status.ShareCount = len(shares)
	}
}

                         
func (w *dkgSessionWrapper) setResult(result *DKGResult) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.result = result
	w.status.Phase = "KEY_READY"
	w.status.Progress = 1.0
	now := time.Now()
	w.status.CompletedAt = &now
	close(w.doneCh)
}

                        
func (w *dkgSessionWrapper) setError(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.err = err
	w.status.Phase = "FAILED"
	w.status.Error = err
	close(w.doneCh)
}

                                
func (s *TransitionDKGService) SubmitCommitment(ctx context.Context, sessionID string, commitment *DKGCommitment) error {
	s.mu.RLock()
	wrapper, exists := s.sessions[sessionID]
	s.mu.RUnlock()

	if !exists {
		return errors.New("session not found")
	}

	                                                                  
	                                                      
	wrapper.mu.Lock()
	if wrapper.dkgSession != nil {
		                        
		wrapper.dkgSession.AddCommitment(commitment.DealerIndex, commitment.DealerAddress, commitment.CommitmentPoints, commitment.AI0)
	}
	wrapper.mu.Unlock()

	return nil
}

                                 
func (s *TransitionDKGService) SubmitShare(ctx context.Context, sessionID string, share *DKGShare) error {
	s.mu.RLock()
	wrapper, exists := s.sessions[sessionID]
	s.mu.RUnlock()

	if !exists {
		return errors.New("session not found")
	}

	                                                                  
	                                                      
	wrapper.mu.Lock()
	if wrapper.dkgSession != nil {
		                         
		wrapper.dkgSession.AddShare(share.DealerIndex, share.DealerAddress, share.Ciphertext)
	}
	wrapper.mu.Unlock()

	return nil
}

                                 
func (s *TransitionDKGService) GetDKGStatus(sessionID string) (*DKGStatus, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	wrapper, exists := s.sessions[sessionID]
	if !exists {
		return nil, errors.New("session not found")
	}

	wrapper.mu.RLock()
	defer wrapper.mu.RUnlock()

	                     
	status := *wrapper.status
	return &status, nil
}

                                      
func (s *TransitionDKGService) WaitForCompletion(ctx context.Context, sessionID string, timeout time.Duration) (*DKGResult, error) {
	s.mu.RLock()
	wrapper, exists := s.sessions[sessionID]
	s.mu.RUnlock()

	if !exists {
		return nil, errors.New("session not found")
	}

	               
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-wrapper.doneCh:
		wrapper.mu.RLock()
		defer wrapper.mu.RUnlock()

		if wrapper.err != nil {
			return nil, wrapper.err
		}
		return wrapper.result, nil
	}
}
