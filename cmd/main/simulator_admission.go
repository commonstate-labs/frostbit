package main

import (
	"dex/logs"
	"dex/pb"
	"dex/txpool"
	"time"

	"google.golang.org/protobuf/proto"
)

                                                                                                      
                                                                                          
type simAccountSubmissions struct {
	pending       map[uint64]*pb.AnyTx
	restoredUntil uint64
	finalized     uint64
	blocked       string
	admitted      uint64
	replayed      uint64
	retryAt       time.Time
	lastReport    time.Time
}

                                                                                         
func (s *TxSimulator) simAccount(node *NodeInstance) *simAccountSubmissions {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.accountSubmissions == nil {
		s.accountSubmissions = make(map[string]*simAccountSubmissions)
	}
	state := s.accountSubmissions[node.Address]
	if state == nil {
		state = &simAccountSubmissions{pending: make(map[uint64]*pb.AnyTx)}
		for _, tx := range node.TxPool.PendingAccountTxs(node.Address) {
			nonce := tx.GetBase().Nonce
			state.pending[nonce] = proto.Clone(tx).(*pb.AnyTx)
			if nonce+1 > state.restoredUntil {
				state.restoredUntil = nonce + 1
			}
		}
		s.accountSubmissions[node.Address] = state
	}
	return state
}

func (s *TxSimulator) simAccountStatus(node *NodeInstance, state *simAccountSubmissions) (txpool.AccountPendingStatus, bool) {
	status, err := node.TxPool.AccountPendingStatus(node.Address)
	if err != nil {
		s.simAdmissionState(node, state, "state unavailable")
		return status, false
	}
	if status.FinalizedNonce != state.finalized {
		for nonce := range state.pending {
			if nonce < status.FinalizedNonce {
				delete(state.pending, nonce)
			}
		}
		state.finalized = status.FinalizedNonce
		state.retryAt = time.Time{}
	}
	s.mu.Lock()
	if s.nonces == nil {
		s.nonces = make(map[string]uint64)
	}
	s.nonces[node.Address] = status.NextNonce
	s.mu.Unlock()
	return status, true
}

func (s *TxSimulator) simAdmissionState(node *NodeInstance, state *simAccountSubmissions, reason string) {
	if state.blocked != reason {
		logs.Info("SIMULATOR: account=%s admission=%s finalizedNonce=%d retained=%d", node.Address, reason, state.finalized, len(state.pending))
		state.blocked = reason
	}
}

                                                                                                                
func (s *TxSimulator) replaySimSubmission(node *NodeInstance, state *simAccountSubmissions, status txpool.AccountPendingStatus) bool {
	tx := state.pending[status.NextNonce]
	if tx == nil {
		return false
	}
	if time.Now().Before(state.retryAt) {
		return true
	}
	accepted, err := node.TxPool.SubmitLocalTx(tx, func(string) {
		if node.SenderManager != nil {
			node.SenderManager.BroadcastTx(tx)
		}
	})
	if !accepted {
		state.retryAt = time.Now().Add(200 * time.Millisecond)
		s.simAdmissionState(node, state, "recovery waiting: "+err.Error())
		return true
	}
	state.replayed++
	state.retryAt = time.Time{}
	s.simAdmissionState(node, state, "recovered")
	if err != nil {
		logs.Warn("SIMULATOR: recovered tx=%s persistence queue: %v", tx.GetTxId(), err)
	}
	return true
}

func (s *TxSimulator) prepareSimAccount(node *NodeInstance) bool {
	if node.TxPool == nil {
		return false
	}
	state := s.simAccount(node)
	status, ok := s.simAccountStatus(node, state)
	if !ok || s.replaySimSubmission(node, state, status) {
		return false
	}
	if status.Full() || len(state.pending) >= status.MaxCount {
		s.simAdmissionState(node, state, "account window full")
		return false
	}
	s.simAdmissionState(node, state, "ready")
	return true
}

func (s *TxSimulator) rememberSimSubmission(node *NodeInstance, tx *pb.AnyTx) {
	state := s.simAccount(node)
	state.pending[tx.GetBase().Nonce] = proto.Clone(tx).(*pb.AnyTx)
	state.admitted++
}

                                                                                                               
func (s *TxSimulator) recoverSimAccount(node *NodeInstance) {
	if node.TxPool == nil {
		return
	}
	lock := s.nonceLock(node.Address)
	lock.Lock()
	defer lock.Unlock()
	state := s.simAccount(node)
	for i := 0; i < 8; i++ {
		status, ok := s.simAccountStatus(node, state)
		if !ok {
			return
		}
		if time.Since(state.lastReport) >= 30*time.Second {
			logs.Info("SIMULATOR: account=%s admitted=%d replayed=%d finalizedNonce=%d nextNonce=%d pending=%d retained=%d", node.Address, state.admitted, state.replayed, status.FinalizedNonce, status.NextNonce, status.Count, len(state.pending))
			state.lastReport = time.Now()
		}
		if !s.replaySimSubmission(node, state, status) || !state.retryAt.IsZero() {
			break
		}
	}
}

                                                                                                               
                                                                                                      
func (s *TxSimulator) hasRestoredSimPending(node *NodeInstance) bool {
	if node == nil || node.TxPool == nil {
		return false
	}
	lock := s.nonceLock(node.Address)
	lock.Lock()
	defer lock.Unlock()
	state := s.simAccount(node)
	if state.restoredUntil == 0 {
		return false
	}
	finalized, ok := finalizedSimAccountNonce(node)
	if !ok {
		return true
	}
	return finalized < state.restoredUntil
}
