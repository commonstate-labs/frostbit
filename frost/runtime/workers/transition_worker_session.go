package workers

import (
	"bytes"
	"context"
	"dex/config"
	"dex/keys"
	"dex/pb"
	"fmt"
	"math/big"
	"strconv"
	"time"

	"google.golang.org/protobuf/proto"
)

func dkgSessionCanStart(currentHeight, triggerHeight uint64) bool {
	if triggerHeight == 0 || currentHeight >= triggerHeight {
		return true
	}
	return triggerHeight-currentHeight == 1
}

func (w *TransitionWorker) StartSession(ctx context.Context, chain string, vaultID uint32, epochID uint64, signAlgo pb.SignAlgo) error {
	transitionKey := keys.KeyFrostVaultTransition(chain, vaultID, epochID)
	transitionData, exists, err := w.stateReader.Get(transitionKey)
	if err != nil {
		return fmt.Errorf("load transition: %w", err)
	}
	if !exists || len(transitionData) == 0 {
		return fmt.Errorf("transition not found for chain=%s vault=%d epoch=%d", chain, vaultID, epochID)
	}
	var transition pb.VaultTransitionState
	if err := proto.Unmarshal(transitionData, &transition); err != nil {
		return fmt.Errorf("unmarshal transition: %w", err)
	}
	if transition.DkgSessionId == "" {
		return fmt.Errorf("empty dkg_session_id for chain=%s vault=%d epoch=%d", chain, vaultID, epochID)
	}
	sessionID := transition.DkgSessionId
	if transition.SignAlgo != pb.SignAlgo_SIGN_ALGO_UNSPECIFIED {
		signAlgo = transition.SignAlgo
	}
	currentHeight := w.GetCurrentHeight()
	                                                                            
	                                                                          
	if !dkgSessionCanStart(currentHeight, transition.TriggerHeight) {
		w.Logger.Debug("[TransitionWorker] defer session %s until trigger height: current=%d trigger=%d",
			sessionID, currentHeight, transition.TriggerHeight)
		return nil
	}

	                                                                             
	                                                                         
	                                                 
	w.mu.RLock()
	existingSession, alreadyRunning := w.sessions[sessionID]
	w.mu.RUnlock()
	if alreadyRunning {
		w.Logger.Debug("[TransitionWorker] session %s already exists", sessionID)
		w.triggerPendingComplaintScan(ctx, existingSession)
		return nil
	}

	committeeInfos, err := w.vaultProvider.VaultCommittee(chain, vaultID, epochID)
	if err != nil {
		return fmt.Errorf("load committee: %w", err)
	}
	if len(committeeInfos) == 0 {
		return fmt.Errorf("empty committee for chain=%s vault=%d epoch=%d", chain, vaultID, epochID)
	}

	committeeMembers := make([]string, 0, len(committeeInfos))
	committeePubKeys := make(map[string][]byte, len(committeeInfos))
	myIndex := 0
	for i, member := range committeeInfos {
		memberID := string(member.ID)
		committeeMembers = append(committeeMembers, memberID)
		if len(member.PublicKey) > 0 {
			pubCopy := make([]byte, len(member.PublicKey))
			copy(pubCopy, member.PublicKey)
			committeePubKeys[memberID] = pubCopy
		}
		if memberID == w.localAddress {
			myIndex = i + 1
		}
		w.Logger.Debug("[TransitionWorker] dkg committee member session=%s chain=%s vault=%d epoch=%d member_index=%d member_id=%s pubkey_fp=%s",
			sessionID, chain, vaultID, epochID, i+1, memberID, shareFingerprintForLog(member.PublicKey))
	}
	w.Logger.Debug("[TransitionWorker] dkg start context session=%s chain=%s vault=%d epoch=%d sign_algo=%s committee=%s local=%s my_index=%d",
		sessionID, chain, vaultID, epochID, signAlgo.String(), committeeMembersForLog(committeeMembers), w.localAddress, myIndex)
	if myIndex == 0 {
		w.Logger.Debug("[TransitionWorker] node %s not in committee for session %s. Committee: %v",
			w.localAddress, sessionID, committeeMembers)
		return nil
	}
	fenced, fenceReason, err := w.hasDkgTranscriptFence(chain, vaultID, epochID, sessionID)
	if err != nil {
		return fmt.Errorf("check durable local DKG transcript: %w", err)
	}
	if fenced {
		                                                                       
		                                                                    
		                                                               
		session := &DKGSession{
			Chain: chain, VaultID: vaultID, EpochID: epochID, SessionID: sessionID,
			SignAlgo: signAlgo, Committee: committeeMembers, CommitteePubKeys: committeePubKeys,
			MyIndex: myIndex, Phase: "REVEAL_ONLY", CreatedAt: time.Now(),
			TriggerHeight: transition.TriggerHeight, CommitDeadline: transition.DkgCommitDeadline,
			SharingDeadline: transition.DkgSharingDeadline, DisputeDeadline: transition.DkgDisputeDeadline,
		}
		w.Logger.Warn("[TransitionWorker] session %s has a durable transcript fence (%s); starting fenced recovery responder", sessionID, fenceReason)
		w.startFencedRecoveryResponder(ctx, session)
		return nil
	}

	if transition.DkgCommitDeadline > 0 && currentHeight > transition.DkgCommitDeadline {
		w.Logger.Warn("[TransitionWorker] refuse to create a new DKG transcript after commit deadline: session=%s current_height=%d commit_deadline=%d",
			sessionID, currentHeight, transition.DkgCommitDeadline)
		return nil
	}

	threshold, err := w.resolveSessionThreshold(&transition, len(committeeInfos))
	if err != nil {
		return err
	}

	w.Logger.Debug("[TransitionWorker] node %s (index %d) starting DKG session %s", w.localAddress, myIndex, sessionID)

	w.mu.Lock()
	defer w.mu.Unlock()

	if _, exists := w.sessions[sessionID]; exists {
		w.Logger.Debug("[TransitionWorker] session %s already exists", sessionID)
		return nil
	}

	session := &DKGSession{
		Chain:            chain,
		VaultID:          vaultID,
		EpochID:          epochID,
		SessionID:        sessionID,
		SignAlgo:         signAlgo,
		Commitments:      make(map[string][]byte),
		ReceivedShrs:     make(map[string][]byte),
		Committee:        committeeMembers,
		CommitteePubKeys: committeePubKeys,
		MyIndex:          myIndex,
		Threshold:        threshold,
		Phase:            "COMMITTING",
		CreatedAt:        time.Now(),
	}

	                                                
	session.TriggerHeight = transition.TriggerHeight
	session.CommitDeadline = transition.DkgCommitDeadline
	session.SharingDeadline = transition.DkgSharingDeadline
	session.DisputeDeadline = transition.DkgDisputeDeadline
	w.Logger.Debug("[TransitionWorker] session %s deadlines: commit=%d, sharing=%d, dispute=%d",
		sessionID, session.CommitDeadline, session.SharingDeadline, session.DisputeDeadline)

	w.sessions[sessionID] = session
	w.Logger.Debug("[TransitionWorker] started DKG session %s", sessionID)

	go w.runSession(ctx, session)

	return nil
}

                                                
func (w *TransitionWorker) runSession(ctx context.Context, session *DKGSession) {
	w.Logger.Debug("[TransitionWorker] runSession %s phase=%s", session.SessionID, session.Phase)
	defer func() {
		w.mu.Lock()
		delete(w.sessions, session.SessionID)
		w.mu.Unlock()
	}()

	                                                                        
	                                                                       
	                                      
	if fenced, reason, err := w.hasDkgTranscriptFence(session.Chain, session.VaultID, session.EpochID, session.SessionID); err != nil {
		w.Logger.Error("[TransitionWorker] check durable transcript failed for session %s: %v", session.SessionID, err)
		return
	} else if fenced {
		session.Phase = "REVEAL_ONLY"
		w.Logger.Warn("[TransitionWorker] refusing to regenerate fenced DKG transcript for session %s (%s); entering fenced recovery", session.SessionID, reason)
		handled, recoverErr := w.resumePendingValidation(ctx, session)
		if recoverErr != nil {
			w.Logger.Error("[TransitionWorker] recover pending DKG validation failed for session %s: %v", session.SessionID, recoverErr)
			return
		}
		if !handled {
			w.monitorPendingComplaints(ctx, session)
		}
		return
	}

	                             
	if err := w.submitCommitment(ctx, session); err != nil {
		w.Logger.Error("[TransitionWorker] submitCommitment failed: %v", err)
		return
	}

	                                                            
	if session.CommitDeadline == 0 {
		w.Logger.Debug("[TransitionWorker] session %s CommitDeadline is 0, waiting for sync...", session.SessionID)
		for {
			transitionKey := keys.KeyFrostVaultTransition(session.Chain, session.VaultID, session.EpochID)
			if data, exists, err := w.stateReader.Get(transitionKey); err == nil && exists {
				var transition pb.VaultTransitionState
				if err := proto.Unmarshal(data, &transition); err == nil && transition.DkgCommitDeadline > 0 {
					session.CommitDeadline = transition.DkgCommitDeadline
					session.SharingDeadline = transition.DkgSharingDeadline
					session.DisputeDeadline = transition.DkgDisputeDeadline
					w.Logger.Debug("[TransitionWorker] session %s deadlines synced: commit=%d", session.SessionID, session.CommitDeadline)
					break
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(1 * time.Second):
			}
		}
	}

	                                                   
	if err := w.waitForHeight(ctx, session.CommitDeadline+1); err != nil {
		w.Logger.Debug("[TransitionWorker] session %s aborted during wait for sharing stage: %v", session.SessionID, err)
		return
	}
	if current, reason := w.sessionStillCurrent(session); !current {
		w.Logger.Info("[TransitionWorker] session %s stopped after commit window: %s", session.SessionID, reason)
		return
	}
	if matches, reason := w.ownFinalizedCommitmentMatchesSession(session); !matches {
		                                                                        
		                                                                         
		                                                                     
		w.Logger.Warn("[TransitionWorker] session %s will not submit shares: %s", session.SessionID, reason)
		return
	}
	if session.SharingDeadline > 0 {
		currentHeight := w.GetCurrentHeight()
		if currentHeight > session.SharingDeadline {
			w.Logger.Warn("[TransitionWorker] session %s skip share submission: current_height=%d > sharing_deadline=%d",
				session.SessionID, currentHeight, session.SharingDeadline)
			return
		}
	}

	if err := w.submitShares(ctx, session); err != nil {
		w.Logger.Error("[TransitionWorker] submitShares failed: %v", err)
		return
	}

	                                  
	monitorCtx, cancelMonitor := context.WithCancel(ctx)
	defer cancelMonitor()
	go w.monitorPendingComplaints(monitorCtx, session)

	                                   
	if session.SharingDeadline > 0 {
		w.Logger.Debug("[TransitionWorker] session %s waiting for sharing window to close (waiting for height %d)...", session.SessionID, session.SharingDeadline+1)
		if err := w.waitForHeight(ctx, session.SharingDeadline+1); err != nil {
			w.Logger.Debug("[TransitionWorker] session %s aborted during wait for sharing deadline: %v", session.SessionID, err)
			return
		}
		if current, reason := w.sessionStillCurrent(session); !current {
			w.Logger.Info("[TransitionWorker] session %s stopped after sharing window: %s", session.SessionID, reason)
			return
		}
	}

	                                               
	if err := w.verifyAndComplain(ctx, session); err != nil {
		w.Logger.Warn("[TransitionWorker] session %s verifyAndComplain encountered error: %v", session.SessionID, err)
	}

	if session.DisputeDeadline > 0 {
		w.Logger.Debug("[TransitionWorker] session %s waiting for dispute window to close (waiting for height %d)...", session.SessionID, session.DisputeDeadline+1)
		if err := w.waitForHeight(ctx, session.DisputeDeadline+1); err != nil {
			w.Logger.Debug("[TransitionWorker] session %s aborted during wait for dispute deadline: %v", session.SessionID, err)
			return
		}
		if current, reason := w.sessionStillCurrent(session); !current {
			w.Logger.Info("[TransitionWorker] session %s stopped after dispute window: %s", session.SessionID, reason)
			return
		}
	} else {
		select {
		case <-ctx.Done():
			return
		case <-time.After(20 * time.Second):
		}
		if current, reason := w.sessionStillCurrent(session); !current {
			w.Logger.Info("[TransitionWorker] session %s stopped after dispute fallback wait: %s", session.SessionID, reason)
			return
		}
	}

	                                           
	if err := w.generateKey(ctx, session); err != nil {
		w.Logger.Error("[TransitionWorker] generateKey failed: %v", err)
		return
	}

	                                       
	if err := w.submitValidation(ctx, session); err != nil {
		w.Logger.Error("[TransitionWorker] submitValidation failed: %v", err)
		return
	}

	w.Logger.Debug("[TransitionWorker] DKG session %s completed", session.SessionID)
}

func (w *TransitionWorker) hasOwnFinalizedCommitment(chain string, vaultID uint32, epochID uint64, sessionID string) (bool, error) {
	key := keys.KeyFrostVaultDkgCommit(chain, vaultID, epochID, sessionID, w.localAddress)
	data, exists, err := w.stateReader.Get(key)
	if err != nil {
		return false, err
	}
	return exists && len(data) > 0, nil
}

func (w *TransitionWorker) hasDkgTranscriptFence(chain string, vaultID uint32, epochID uint64, sessionID string) (bool, string, error) {
	if w.localShareStore != nil {
		hasArtifacts, err := w.localShareStore.HasDkgLocalSession(chain, vaultID, epochID, sessionID)
		if err != nil {
			return false, "", err
		}
		if hasArtifacts {
			return true, "node-local session artifacts exist", nil
		}
	}
	committed, err := w.hasOwnFinalizedCommitment(chain, vaultID, epochID, sessionID)
	if err != nil {
		return false, "", err
	}
	if committed {
		return true, "own commitment is finalized", nil
	}
	return false, "", nil
}

func (w *TransitionWorker) startFencedRecoveryResponder(ctx context.Context, session *DKGSession) {
	if session == nil {
		return
	}
	w.mu.Lock()
	if _, exists := w.sessions[session.SessionID]; exists {
		w.mu.Unlock()
		return
	}
	if w.sessions == nil {
		w.sessions = make(map[string]*DKGSession)
	}
	w.sessions[session.SessionID] = session
	w.mu.Unlock()

	go func() {
		defer func() {
			w.mu.Lock()
			if current := w.sessions[session.SessionID]; current == session {
				delete(w.sessions, session.SessionID)
			}
			w.mu.Unlock()
		}()
		handled, err := w.resumePendingValidation(ctx, session)
		if err != nil {
			w.Logger.Error("[TransitionWorker] recover pending DKG validation failed for session %s: %v", session.SessionID, err)
			return
		}
		if !handled {
			w.monitorPendingComplaints(ctx, session)
		}
	}()
}

                                                                             
                                                                             
                                                                                
                                                                            
func (w *TransitionWorker) resumePendingValidation(ctx context.Context, session *DKGSession) (bool, error) {
	if session == nil || session.DisputeDeadline == 0 || w.GetCurrentHeight() <= session.DisputeDeadline {
		return false, nil
	}
	if w.localShareStore == nil {
		return true, nil
	}
	pending, err := w.localShareStore.LoadPendingLocalShare(session.Chain, session.VaultID, session.EpochID, session.SessionID)
	if err != nil {
		return true, fmt.Errorf("load pending local share: %w", err)
	}
	if len(pending) == 0 {
		w.Logger.Warn("[TransitionWorker] session %s passed dispute deadline without a pending local share; waiting for VM retry", session.SessionID)
		return true, nil
	}
	if current, reason := w.sessionStillCurrent(session); !current {
		return true, fmt.Errorf("session is no longer eligible for validation recovery: %s", reason)
	}

	session.LocalShareBytes = append([]byte(nil), pending...)
	session.LocalShare = new(big.Int).SetBytes(pending)
	groupPubkey, err := w.aggregateGroupPubkey(ctx, session)
	if err != nil {
		return true, fmt.Errorf("rebuild group pubkey: %w", err)
	}
	session.GroupPubkey = groupPubkey
	session.Phase = "VALIDATION_RECOVERY"

	if current, reason := w.sessionStillCurrent(session); !current {
		return true, fmt.Errorf("session changed before recovered validation: %s", reason)
	}
	if err := w.submitValidation(ctx, session); err != nil {
		return true, err
	}
	w.Logger.Info("[TransitionWorker] resubmitted recovered DKG validation: chain=%s vault=%d epoch=%d session=%s",
		session.Chain, session.VaultID, session.EpochID, session.SessionID)
	return true, nil
}

func (w *TransitionWorker) ownFinalizedCommitmentMatchesSession(session *DKGSession) (bool, string) {
	if session == nil || session.Polynomial == nil {
		return false, "local DKG polynomial is unavailable"
	}
	key := keys.KeyFrostVaultDkgCommit(
		session.Chain, session.VaultID, session.EpochID, session.SessionID, w.localAddress,
	)
	data, exists, err := w.stateReader.Get(key)
	if err != nil {
		return false, fmt.Sprintf("load finalized local commitment: %v", err)
	}
	if !exists || len(data) == 0 {
		return false, "our commitment was not finalized by the commit deadline"
	}

	var finalized pb.FrostVaultDkgCommitment
	if err := proto.Unmarshal(data, &finalized); err != nil {
		return false, fmt.Sprintf("decode finalized local commitment: %v", err)
	}
	if finalized.MinerAddress != w.localAddress {
		return false, fmt.Sprintf("finalized commitment dealer mismatch: got %s", finalized.MinerAddress)
	}
	if finalized.SignAlgo != session.SignAlgo {
		return false, fmt.Sprintf("finalized commitment algorithm mismatch: got %s want %s", finalized.SignAlgo, session.SignAlgo)
	}

	dkgExec, err := w.cryptoFactory.NewDKGExecutor(int32(session.SignAlgo))
	if err != nil {
		return false, fmt.Sprintf("create DKG executor for commitment verification: %v", err)
	}
	expected := dkgExec.ComputeCommitments(session.Polynomial)
	if len(expected) == 0 || len(finalized.CommitmentPoints) != len(expected) {
		return false, fmt.Sprintf("finalized commitment point count mismatch: got %d want %d", len(finalized.CommitmentPoints), len(expected))
	}
	for i := range expected {
		if !bytes.Equal(finalized.CommitmentPoints[i], expected[i]) {
			return false, fmt.Sprintf("finalized commitment differs at point %d", i)
		}
	}
	if !bytes.Equal(finalized.AI0, expected[0]) {
		return false, "finalized AI0 differs from local polynomial"
	}
	return true, ""
}

                                                                         
                                                                            
                                                                   
func (w *TransitionWorker) resolveSessionThreshold(transition *pb.VaultTransitionState, committeeSize int) (int, error) {
	if transition == nil {
		return 0, fmt.Errorf("transition is nil")
	}
	if transition.DkgN != uint32(committeeSize) {
		return 0, fmt.Errorf("on-chain dkg size %d does not match frozen committee size %d", transition.DkgN, committeeSize)
	}
	threshold := int(transition.DkgThresholdT)
	if threshold <= 0 {
		return 0, fmt.Errorf("on-chain dkg threshold is missing")
	}
	if threshold > committeeSize {
		return 0, fmt.Errorf("on-chain dkg threshold %d exceeds committee size %d", threshold, committeeSize)
	}
	                                                                           
	                                                                             
	if committeeSize >= 4 {
		expected, err := config.ComputeVaultCommitteeThreshold(committeeSize)
		if err != nil {
			return 0, fmt.Errorf("invalid frozen committee size %d: %w", committeeSize, err)
		}
		if threshold != expected {
			return 0, fmt.Errorf("on-chain dkg threshold %d does not equal protocol threshold %d for committee size %d", threshold, expected, committeeSize)
		}
	}
	return threshold, nil
}

                                                                                 
                                                                                
                                                        
func (w *TransitionWorker) sessionStillCurrent(session *DKGSession) (bool, string) {
	transitionKey := keys.KeyFrostVaultTransition(session.Chain, session.VaultID, session.EpochID)
	data, exists, err := w.stateReader.Get(transitionKey)
	if err != nil {
		return false, fmt.Sprintf("reload transition: %v", err)
	}
	if !exists || len(data) == 0 {
		return false, "transition no longer exists"
	}

	var transition pb.VaultTransitionState
	if err := proto.Unmarshal(data, &transition); err != nil {
		return false, fmt.Sprintf("decode transition: %v", err)
	}
	if transition.DkgSessionId != session.SessionID {
		return false, fmt.Sprintf("superseded by session %s", transition.DkgSessionId)
	}
	switch transition.DkgStatus {
	case "COMMITTING", "SHARING", "RESOLVING":
		                                         
	case "NOT_STARTED", "FAILED":
		return false, fmt.Sprintf("transition status is %s", transition.DkgStatus)
	default:
		return false, fmt.Sprintf("transition is no longer active (status=%s)", transition.DkgStatus)
	}

	if !sameCommittee(session.Committee, transition.NewCommitteeMembers) {
		return false, fmt.Sprintf("committee changed from %s to %s without a new session",
			committeeMembersForLog(session.Committee), committeeMembersForLog(transition.NewCommitteeMembers))
	}

	localIndex := 0
	for i, member := range transition.NewCommitteeMembers {
		if member == w.localAddress {
			localIndex = i + 1
			break
		}
	}
	if localIndex == 0 {
		return false, "local node is no longer in the committee"
	}
	if localIndex != session.MyIndex {
		return false, fmt.Sprintf("local participant index changed from %d to %d", session.MyIndex, localIndex)
	}

	session.TriggerHeight = transition.TriggerHeight
	session.CommitDeadline = transition.DkgCommitDeadline
	session.SharingDeadline = transition.DkgSharingDeadline
	session.DisputeDeadline = transition.DkgDisputeDeadline
	return true, ""
}

func sameCommittee(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

                                                           
func (w *TransitionWorker) GetCurrentHeight() uint64 {
	data, exists, err := w.stateReader.Get(keys.KeyLatestHeight())
	if err != nil || !exists || len(data) == 0 {
		return 0
	}
	h, _ := strconv.ParseUint(string(data), 10, 64)
	return h
}

                                                             
func (w *TransitionWorker) waitForHeight(ctx context.Context, targetHeight uint64) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		current := w.GetCurrentHeight()
		if current >= targetHeight {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			continue
		}
	}
}

func (w *TransitionWorker) GetSession(sessionID string) *DKGSession {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.sessions[sessionID]
}
