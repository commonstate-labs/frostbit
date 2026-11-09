                                         
                                                 
package adapters

import (
	"context"
	"crypto/sha256"
	"dex/frost/runtime"
	iface "dex/interfaces"
	"dex/keys"
	"dex/pb"
	"dex/sender"
	"dex/txpool"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	"google.golang.org/protobuf/proto"
)

                              
var ErrTxPoolFull = errors.New("txpool is full")

                                              
type TxPool interface {
	                              
	AddTx(tx proto.Message) error
	                         
	Broadcast(tx proto.Message) error
}

                                                                
type TxPoolAdapter struct {
	txPool *txpool.TxPool
	sender *sender.SenderManager
}

                                              
func NewTxPoolAdapter(txPool *txpool.TxPool, sender *sender.SenderManager) *TxPoolAdapter {
	return &TxPoolAdapter{
		txPool: txPool,
		sender: sender,
	}
}

                              
func (a *TxPoolAdapter) AddTx(tx proto.Message) error {
	                                       
	anyTx, ok := tx.(*pb.AnyTx)
	if !ok {
		return fmt.Errorf("AddTx: expected *pb.AnyTx, got %T", tx)
	}

	                                    
	err := a.txPool.SubmitTx(anyTx, "", func(txID string) {
		               
		if a.sender != nil {
			a.sender.BroadcastTx(anyTx)
		}
	})
	if err != nil {
		return fmt.Errorf("AddTx: SubmitTx failed: %w", err)
	}
	return nil
}

                         
func (a *TxPoolAdapter) Broadcast(tx proto.Message) error {
	if a.sender == nil {
		return nil
	}
	anyTx, ok := tx.(*pb.AnyTx)
	if !ok {
		return nil
	}
	a.sender.BroadcastTx(anyTx)
	return nil
}

                                                       
type TxPoolSubmitter struct {
	pool TxPool
}

                                                  
func NewTxPoolSubmitter(pool TxPool) *TxPoolSubmitter {
	return &TxPoolSubmitter{pool: pool}
}

                      
func (s *TxPoolSubmitter) Submit(tx any) (txID string, err error) {
	msg, ok := tx.(proto.Message)
	if !ok {
		return "", errors.New("tx must be proto.Message")
	}

	anyTx, ok := tx.(*pb.AnyTx)
	if ok && anyTx.GetBase() != nil {
		txID = anyTx.GetBase().TxId
	} else {
		                                             
		data, err := proto.Marshal(msg)
		if err != nil {
			return "", err
		}
		hash := sha256.Sum256(data)
		txID = hex.EncodeToString(hash[:])
	}

	                           
	if err := s.pool.AddTx(msg); err != nil {
		return "", err
	}
	if err := s.pool.Broadcast(msg); err != nil {
		                                                              
		                           
	}

	return txID, nil
}

                                            
func (s *TxPoolSubmitter) SubmitDkgCommitTx(ctx context.Context, tx *pb.FrostVaultDkgCommitTx) error {
	anyTx := &pb.AnyTx{Content: &pb.AnyTx_FrostVaultDkgCommitTx{FrostVaultDkgCommitTx: tx}}
	_, err := s.Submit(anyTx)
	return err
}

                                           
func (s *TxPoolSubmitter) SubmitDkgShareTx(ctx context.Context, tx *pb.FrostVaultDkgShareTx) error {
	anyTx := &pb.AnyTx{Content: &pb.AnyTx_FrostVaultDkgShareTx{FrostVaultDkgShareTx: tx}}
	_, err := s.Submit(anyTx)
	return err
}

                                               
func (s *TxPoolSubmitter) SubmitDkgComplaintTx(ctx context.Context, tx *pb.FrostVaultDkgComplaintTx) error {
	anyTx := &pb.AnyTx{Content: &pb.AnyTx_FrostVaultDkgComplaintTx{FrostVaultDkgComplaintTx: tx}}
	_, err := s.Submit(anyTx)
	return err
}

                                            
func (s *TxPoolSubmitter) SubmitDkgRevealTx(ctx context.Context, tx *pb.FrostVaultDkgRevealTx) error {
	anyTx := &pb.AnyTx{Content: &pb.AnyTx_FrostVaultDkgRevealTx{FrostVaultDkgRevealTx: tx}}
	_, err := s.Submit(anyTx)
	return err
}

                                                            
func (s *TxPoolSubmitter) SubmitDkgValidationSignedTx(ctx context.Context, tx *pb.FrostVaultDkgValidationSignedTx) error {
	anyTx := &pb.AnyTx{Content: &pb.AnyTx_FrostVaultDkgValidationSignedTx{FrostVaultDkgValidationSignedTx: tx}}
	_, err := s.Submit(anyTx)
	return err
}

                                                    
func (s *TxPoolSubmitter) SubmitTransitionSignedTx(ctx context.Context, tx *pb.FrostVaultTransitionSignedTx) error {
	anyTx := &pb.AnyTx{Content: &pb.AnyTx_FrostVaultTransitionSignedTx{FrostVaultTransitionSignedTx: tx}}
	_, err := s.Submit(anyTx)
	return err
}

                                                          
func (s *TxPoolSubmitter) SubmitWithdrawClaimTx(ctx context.Context, tx *pb.FrostWithdrawClaimTx) error {
	anyTx := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawClaimTx{FrostWithdrawClaimTx: tx}}
	_, err := s.Submit(anyTx)
	return err
}

                                                  
func (s *TxPoolSubmitter) SubmitWithdrawSignedTx(ctx context.Context, tx *pb.FrostWithdrawSignedTx) error {
	anyTx := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: tx}}
	_, err := s.Submit(anyTx)
	return err
}

                                                           
                                                                                                           
type LocalLogReporter struct {
	dbManager iface.NodeLocalWriter
}

const planningLogDuplicateThrottleMs uint64 = 15_000

func NewLocalLogReporter(dbManager iface.NodeLocalWriter) *LocalLogReporter {
	return &LocalLogReporter{dbManager: dbManager}
}

func clonePlanningLog(src *pb.FrostPlanningLog) *pb.FrostPlanningLog {
	if src == nil {
		return nil
	}
	return &pb.FrostPlanningLog{
		Step:      src.Step,
		Status:    src.Status,
		Message:   src.Message,
		Timestamp: src.Timestamp,
	}
}

func shouldAppendPlanningLog(last, next *pb.FrostPlanningLog) bool {
	if next == nil {
		return false
	}
	if last == nil {
		return true
	}
	if last.Step != next.Step || last.Status != next.Status || last.Message != next.Message {
		return true
	}
	if next.Timestamp == 0 || last.Timestamp == 0 {
		return false
	}
	if next.Timestamp < last.Timestamp {
		return false
	}
	return next.Timestamp-last.Timestamp > planningLogDuplicateThrottleMs
}

func (r *LocalLogReporter) ReportWithdrawPlanningLog(ctx context.Context, log *pb.FrostWithdrawPlanningLogTx) error {
	if r.dbManager == nil || log == nil {
		return nil
	}
	                                                                                        
	                                                                                                  
	key := keys.KeyFrostPlanningLog(log.WithdrawId, log.Reporter)

	merged := &pb.FrostWithdrawPlanningLogTx{
		Reporter:   log.Reporter,
		WithdrawId: log.WithdrawId,
		Logs:       make([]*pb.FrostPlanningLog, 0, len(log.Logs)+8),
	}
	appendLog := func(entry *pb.FrostPlanningLog) {
		cloned := clonePlanningLog(entry)
		if cloned == nil {
			return
		}
		var last *pb.FrostPlanningLog
		if n := len(merged.Logs); n > 0 {
			last = merged.Logs[n-1]
		}
		if !shouldAppendPlanningLog(last, cloned) {
			return
		}
		merged.Logs = append(merged.Logs, cloned)
	}

	if oldData, err := r.dbManager.Get(key); err == nil && len(oldData) > 0 {
		var old pb.FrostWithdrawPlanningLogTx
		if unmarshalErr := proto.Unmarshal(oldData, &old); unmarshalErr == nil {
			for _, oldLog := range old.Logs {
				appendLog(oldLog)
			}
		}
	}

	for _, newLog := range log.Logs {
		appendLog(newLog)
	}

	                                               
	const maxLogsPerKey = 256
	if len(merged.Logs) > maxLogsPerKey {
		merged.Logs = merged.Logs[len(merged.Logs)-maxLogsPerKey:]
	}

	data, err := proto.Marshal(merged)
	if err != nil {
		return err
	}
	r.dbManager.EnqueueSet(key, string(data))
	return nil
}

                                                         
var _ runtime.LogReporter = (*LocalLogReporter)(nil)

                                                        
var _ runtime.TxSubmitter = (*TxPoolSubmitter)(nil)

                                                   
type FakeTxSubmitter struct {
	mu          sync.Mutex
	submitted   []any
	submitCount int
	shouldFail  bool
	failErr     error
}

                                                  
func NewFakeTxSubmitter() *FakeTxSubmitter {
	return &FakeTxSubmitter{
		submitted: make([]any, 0),
	}
}

                                     
func (s *FakeTxSubmitter) Submit(tx any) (txID string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.shouldFail {
		return "", s.failErr
	}

	s.submitted = append(s.submitted, tx)
	s.submitCount++

	                   
	txID = hex.EncodeToString(sha256.New().Sum([]byte{byte(s.submitCount)}))
	return txID, nil
}

                                              
func (s *FakeTxSubmitter) GetSubmitted() []any {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]any, len(s.submitted))
	copy(result, s.submitted)
	return result
}

                                 
func (s *FakeTxSubmitter) SubmitCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.submitCount
}

                                                        
func (s *FakeTxSubmitter) SetShouldFail(fail bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.shouldFail = fail
	s.failErr = err
}

                     
func (s *FakeTxSubmitter) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.submitted = make([]any, 0)
	s.submitCount = 0
	s.shouldFail = false
	s.failErr = nil
}

                                                           
func (s *FakeTxSubmitter) SubmitDkgCommitTx(ctx context.Context, tx *pb.FrostVaultDkgCommitTx) error {
	_, err := s.Submit(tx)
	return err
}

                                                          
func (s *FakeTxSubmitter) SubmitDkgShareTx(ctx context.Context, tx *pb.FrostVaultDkgShareTx) error {
	_, err := s.Submit(tx)
	return err
}

                                                              
func (s *FakeTxSubmitter) SubmitDkgComplaintTx(ctx context.Context, tx *pb.FrostVaultDkgComplaintTx) error {
	_, err := s.Submit(tx)
	return err
}

                                                           
func (s *FakeTxSubmitter) SubmitDkgRevealTx(ctx context.Context, tx *pb.FrostVaultDkgRevealTx) error {
	_, err := s.Submit(tx)
	return err
}

                                                                           
func (s *FakeTxSubmitter) SubmitDkgValidationSignedTx(ctx context.Context, tx *pb.FrostVaultDkgValidationSignedTx) error {
	_, err := s.Submit(tx)
	return err
}

                                                                   
func (s *FakeTxSubmitter) SubmitTransitionSignedTx(ctx context.Context, tx *pb.FrostVaultTransitionSignedTx) error {
	_, err := s.Submit(tx)
	return err
}

                                                                         
func (s *FakeTxSubmitter) SubmitWithdrawClaimTx(ctx context.Context, tx *pb.FrostWithdrawClaimTx) error {
	_, err := s.Submit(tx)
	return err
}

                                                                 
func (s *FakeTxSubmitter) SubmitWithdrawSignedTx(ctx context.Context, tx *pb.FrostWithdrawSignedTx) error {
	_, err := s.Submit(tx)
	return err
}

                                                   
type FakeLogReporter struct {
	Logs []*pb.FrostWithdrawPlanningLogTx
}

func (s *FakeLogReporter) ReportWithdrawPlanningLog(ctx context.Context, log *pb.FrostWithdrawPlanningLogTx) error {
	s.Logs = append(s.Logs, log)
	return nil
}

                                                        
var _ runtime.TxSubmitter = (*FakeTxSubmitter)(nil)
var _ runtime.LogReporter = (*FakeLogReporter)(nil)
