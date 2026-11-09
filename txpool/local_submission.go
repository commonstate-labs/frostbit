package txpool

import (
	"dex/lifecycle"
	"dex/pb"
	"fmt"
	"sort"
)

                                                                                                  
                                                                                                    
                                                                                                  
func (p *TxPool) SubmitLocalTx(tx *pb.AnyTx, onAdded OnTxAddedCallback) (accepted bool, err error) {
	return p.submitLocalTx(tx, onAdded, false)
}

                                                                             
                                                                           
                                                                             
                                                                               
                                                                       
func (p *TxPool) SubmitLocalBootstrapMinerTx(tx *pb.AnyTx, onAdded OnTxAddedCallback) (accepted bool, err error) {
	if p == nil || tx == nil {
		return false, fmt.Errorf("bootstrap miner transaction is required")
	}
	miner := tx.GetMinerTx()
	base := tx.GetBase()
	if miner == nil || base == nil || miner.Op != pb.OrderOp_ADD ||
		base.FromAddress == "" || base.FromAddress != p.address ||
		miner.RewardAddress != p.address || miner.Endpoint == "" ||
		len(miner.BlsPublicKey) == 0 || len(miner.BlsProof) == 0 {
		return false, fmt.Errorf("bootstrap miner transaction must be a complete local ADD registration")
	}
	return p.submitLocalTx(tx, onAdded, true)
}

func (p *TxPool) submitLocalTx(tx *pb.AnyTx, onAdded OnTxAddedCallback, allowCatchUpBootstrap bool) (accepted bool, err error) {
	if p == nil || tx == nil || tx.GetBase() == nil || p.address == "" || tx.GetBase().FromAddress != p.address {
		return false, fmt.Errorf("local transaction must belong to this node")
	}
	if p.emergency.IsStopped() {
		return false, lifecycle.ErrEmergencyStop
	}
	select {
	case <-p.stopChan:
		return false, fmt.Errorf("txpool is stopping")
	default:
	}
	if p.isExecutionGuardExpired(tx) {
		return false, ErrExecutionGuardExpired
	}
	if p.HasTransaction(tx.GetTxId()) {
		return true, nil
	}
	if p.syncPressure != nil && pb.TxRequiresAccountNonce(tx) {
		pressure := p.syncPressure.SyncPressureState()
		if pressure.CatchUp && !allowCatchUpBootstrap {
			return false, fmt.Errorf("%w: reason=%s", ErrSyncCatchUpBackpressure, pressure.Reason)
		}
	}
	                                                                                       
	added, err := p.insertPending(tx)
	if err != nil {
		return false, err
	}
	if !added {
		return true, nil
	}
	err = p.enqueuePendingSave(tx)
	if onAdded != nil {
		onAdded(tx.GetTxId())
	}
	return true, err
}

type AccountPendingStatus struct {
	FinalizedNonce uint64
	NextNonce      uint64                                                                                          
	Count          int
	Bytes          int64
	MaxCount       int
	MaxBytes       int64
	MaxNonceGap    uint64
}

func (s AccountPendingStatus) Full() bool {
	return s.Count >= s.MaxCount || s.Bytes >= s.MaxBytes || s.NextNonce == ^uint64(0) || s.NextNonce-s.FinalizedNonce > s.MaxNonceGap
}

                                                                                                  
func (p *TxPool) AccountPendingStatus(owner string) (AccountPendingStatus, error) {
	state, err := p.readAdmissionState(owner)
	if err != nil {
		return AccountPendingStatus{}, err
	}
	s := AccountPendingStatus{FinalizedNonce: state.nonce, NextNonce: state.nonce, MaxCount: p.cfg.TxPool.MaxPendingPerAccount, MaxBytes: p.cfg.TxPool.MaxPendingAccountBytes, MaxNonceGap: p.cfg.TxPool.MaxFutureNonceGap}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if account := p.pendingAccounts[owner]; account != nil {
		s.Count, s.Bytes = len(account.txs), account.bytes
		for s.NextNonce != ^uint64(0) {
			if _, exists := account.nonces[s.NextNonce]; !exists {
				break
			}
			s.NextNonce++
		}
	}
	return s, nil
}

                                                                                                     
func (p *TxPool) PendingAccountTxs(owner string) []*pb.AnyTx {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var out []*pb.AnyTx
	if account := p.pendingAccounts[owner]; account != nil {
		for _, id := range account.nonces {
			if value, ok := p.pendingAnyTxCache.Peek(id); ok {
				out = append(out, value.(*pb.AnyTx))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GetBase().Nonce < out[j].GetBase().Nonce })
	return out
}
