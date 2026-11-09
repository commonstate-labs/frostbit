                                            
                                                                          

package services

import (
	"context"
	"errors"
	"sync"
	"time"

	"dex/frost/runtime/roast"
	"dex/frost/runtime/session"
	"dex/frost/runtime/types"
	"dex/logs"
)

                                                                                         
type (
	SigningService       = types.SigningService
	SigningSessionParams = types.SigningSessionParams
	SessionStatus        = types.SessionStatus
	SignedPackage        = types.SignedPackage
)

                               

                                               
type RoastSigningService struct {
	mu sync.RWMutex

	coordinator   *roast.Coordinator
	participant   *roast.Participant
	vaultProvider types.VaultCommitteeProvider

	               
	sessions  map[string]*signingSessionWrapper                             
	callbacks map[string]func(*SignedPackage, error)                         
}

                                                                 
type signingSessionWrapper struct {
	mu       sync.RWMutex
	doneOnce sync.Once

	sessionID string
	jobID     string
	params    *SigningSessionParams
	status    *SessionStatus
	callback  func(*SignedPackage, error)

	               
	doneCh chan struct{}
	result *SignedPackage
	err    error
}

                                                   
func NewRoastSigningService(
	coordinator *roast.Coordinator,
	participant *roast.Participant,
	vaultProvider types.VaultCommitteeProvider,
) *RoastSigningService {
	return &RoastSigningService{
		coordinator:   coordinator,
		participant:   participant,
		vaultProvider: vaultProvider,
		sessions:      make(map[string]*signingSessionWrapper),
		callbacks:     make(map[string]func(*SignedPackage, error)),
	}
}

                                         
func (s *RoastSigningService) StartSigningSession(ctx context.Context, params *SigningSessionParams) (string, error) {
	if params == nil {
		return "", errors.New("params is nil")
	}

	sessionID := params.JobID                                 

	s.mu.Lock()
	defer s.mu.Unlock()

	                        
	if existing, exists := s.sessions[sessionID]; exists {
		if reuseExistingSession(existing) {
			return sessionID, nil          
		}
		logs.Warn("[SigningService] replacing terminal signing session job=%s state=%s err=%v",
			params.JobID, existing.snapshotState(), existing.snapshotError())
		delete(s.sessions, sessionID)
		if s.coordinator != nil {
			s.coordinator.CloseSession(params.JobID)
		}
		if s.participant != nil {
			s.participant.CloseSession(params.JobID)
		}
	}

	                  
	wrapper := &signingSessionWrapper{
		sessionID: sessionID,
		jobID:     params.JobID,
		params:    params,
		status: &SessionStatus{
			SessionID: sessionID,
			JobID:     params.JobID,
			State:     "INIT",
			Progress:  0.0,
			StartedAt: time.Now(),
		},
		doneCh: make(chan struct{}),
	}

	s.sessions[sessionID] = wrapper

	                      
	startParams := &roast.StartSessionParams{
		JobID:     params.JobID,
		VaultID:   params.VaultID,
		Chain:     params.Chain,
		KeyEpoch:  params.KeyEpoch,
		SignAlgo:  params.SignAlgo,
		Messages:  params.Messages,
		Threshold: params.Threshold,
		Tweaks:    params.Tweaks,
	}

	if err := s.coordinator.StartSession(ctx, startParams); err != nil {
		if errors.Is(err, roast.ErrSessionAlreadyExists) && s.coordinator != nil {
			if sess := s.coordinator.GetSession(params.JobID); sess != nil && sess.GetState() == session.SignSessionStateFailed {
				logs.Warn("[SigningService] restarting failed coordinator session job=%s", params.JobID)
				s.coordinator.CloseSession(params.JobID)
				if s.participant != nil {
					s.participant.CloseSession(params.JobID)
				}
				err = s.coordinator.StartSession(ctx, startParams)
			}
		}
		if err != nil {
			delete(s.sessions, sessionID)
			return "", err
		}
	}

	                           
	go s.monitorSession(ctx, wrapper)

	return sessionID, nil
}

                                          
func (s *RoastSigningService) monitorSession(ctx context.Context, wrapper *signingSessionWrapper) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-wrapper.doneCh:
			return
		case <-ctx.Done():
			wrapper.setError(ctx.Err())
			return
		case <-ticker.C:
			                     
			sess := s.coordinator.GetSession(wrapper.jobID)
			if sess == nil {
				                                             
				continue
			}

			state := sess.GetState()
			wrapper.updateStatus(state)

			                     
			if state == session.SignSessionStateComplete {
				                     
				signatures := sess.GetFinalSignatures()
				if len(signatures) > 0 {
					signatureList := make([][]byte, len(signatures))
					totalSize := 0
					for i, sig := range signatures {
						signatureList[i] = append([]byte(nil), sig...)
						totalSize += len(sig)
					}

					                       
					                                                                                  
					signedPkg := &SignedPackage{
						SessionID:    wrapper.sessionID,
						JobID:        wrapper.jobID,
						Signature:    signatureList[0],
						Signatures:   signatureList,                                                                      
						RawTx:        nil,                                                   
						TemplateHash: nil,                                  
					}
					                                       
					if len(signatures) > 1 {
						combined := make([]byte, 0, totalSize)
						for _, sig := range signatureList {
							combined = append(combined, sig...)
						}
						signedPkg.Signature = combined
					}
					wrapper.setResult(signedPkg)
				} else {
					wrapper.setError(errors.New("session completed but no signatures"))
				}
				return
			}

			if state == session.SignSessionStateFailed {
				wrapper.setError(errors.New("session failed"))
				return
			}
		}
	}
}

                                                                  

                            
func (w *signingSessionWrapper) updateStatus(state session.SignSessionState) {
	w.mu.Lock()
	defer w.mu.Unlock()

	var stateStr string
	var progress float64

	switch state {
	case session.SignSessionStateInit:
		stateStr = "INIT"
		progress = 0.0
	case session.SignSessionStateCollectingNonces:
		stateStr = "COLLECTING_NONCES"
		progress = 0.2
	case session.SignSessionStateCollectingShares:
		stateStr = "COLLECTING_SHARES"
		progress = 0.6
	case session.SignSessionStateAggregating:
		stateStr = "AGGREGATING"
		progress = 0.9
	case session.SignSessionStateComplete:
		stateStr = "COMPLETE"
		progress = 1.0
		now := time.Now()
		w.status.CompletedAt = &now
	case session.SignSessionStateFailed:
		stateStr = "FAILED"
		progress = 0.0
	}

	w.status.State = stateStr
	w.status.Progress = progress
}

                         
func (w *signingSessionWrapper) setResult(result *SignedPackage) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.result = result
	w.status.State = "COMPLETE"
	w.status.Progress = 1.0
	now := time.Now()
	w.status.CompletedAt = &now
	w.doneOnce.Do(func() {
		close(w.doneCh)
	})

	if w.callback != nil {
		w.callback(result, nil)
	}
}

                        
func (w *signingSessionWrapper) setError(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.err = err
	w.status.State = "FAILED"
	w.status.Error = err
	now := time.Now()
	w.status.CompletedAt = &now
	w.doneOnce.Do(func() {
		close(w.doneCh)
	})

	if w.callback != nil {
		w.callback(nil, err)
	}
}

func reuseExistingSession(wrapper *signingSessionWrapper) bool {
	if wrapper == nil {
		return false
	}

	wrapper.mu.RLock()
	defer wrapper.mu.RUnlock()

	select {
	case <-wrapper.doneCh:
		return wrapper.err == nil && wrapper.result != nil
	default:
		return true
	}
}

func (w *signingSessionWrapper) snapshotState() string {
	if w == nil {
		return ""
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.status == nil {
		return ""
	}
	return w.status.State
}

func (w *signingSessionWrapper) snapshotError() error {
	if w == nil {
		return nil
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.err
}

                                      
func (s *RoastSigningService) GetSessionStatus(sessionID string) (*SessionStatus, error) {
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

                             
func (s *RoastSigningService) CancelSession(sessionID string) error {
	s.mu.Lock()
	wrapper, exists := s.sessions[sessionID]
	if !exists {
		s.mu.Unlock()
		return errors.New("session not found")
	}
	delete(s.sessions, sessionID)
	delete(s.callbacks, sessionID)
	s.mu.Unlock()

	                                                                        
	                                                                           
	                                                    
	wrapper.setError(errors.New("session cancelled"))
	if s.coordinator != nil {
		s.coordinator.CloseSession(wrapper.params.JobID)
	}
	if s.participant != nil {
		s.participant.CloseSession(wrapper.params.JobID)
	}
	return nil
}

                                       
func (s *RoastSigningService) WaitForCompletion(ctx context.Context, sessionID string, timeout time.Duration) (*SignedPackage, error) {
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

                                           
func (s *RoastSigningService) SetCompletionCallback(sessionID string, callback func(*SignedPackage, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	wrapper, exists := s.sessions[sessionID]
	if exists {
		wrapper.mu.Lock()
		wrapper.callback = callback
		wrapper.mu.Unlock()
	} else {
		s.callbacks[sessionID] = callback
	}
}
