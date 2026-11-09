                                                       
                                                                                                           

package workers

import (
	"crypto/sha256"
	"fmt"

	"dex/frost/runtime/planning"
	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

                                                                                                                 
func (w *WithdrawWorker) shouldSkipJob(job *planning.Job) (bool, string, error) {
	if w.stateReader == nil || job == nil {
		return false, "", nil
	}

	                                                                         
	                                                                 
	                                                                
	cached := w.cachedWithdrawSignedTx(job.JobID)
	if cached != nil {
		if txID := cached.GetBase().GetTxId(); txID != "" {
			if _, exists, err := w.stateReader.Get(keys.KeyPendingAnyTx(txID)); err != nil {
				return false, "", err
			} else if exists {
				return true, "withdraw tx already pending", nil
			}
			if _, exists, err := w.stateReader.Get(keys.KeyVMAppliedTx(txID)); err != nil {
				return false, "", err
			} else if exists {
				w.forgetWithdrawSignedTx(job.JobID)
				return true, "withdraw tx already applied", nil
			}
		}
	}

	claimApplied, err := w.isWithdrawClaimApplied(job)
	if err != nil {
		return false, "", err
	}

	                                                                       
	                                                                       
	                                                                         
	                                                                       
	                                                                            
	                                                 
	if cached == nil && claimApplied {
		pending, err := w.findPendingWithdrawSignedTx(job.JobID)
		if err != nil {
			return false, "", err
		}
		if pending != nil {
			w.rememberWithdrawSignedTx(pending)
			return true, "withdraw tx already pending", nil
		}
	}

	expectedStatus := "QUEUED"
	if claimApplied {
		expectedStatus = "SIGNING"
	}

	stateKeys := make([]string, len(job.WithdrawIDs))
	for i, withdrawID := range job.WithdrawIDs {
		stateKeys[i] = keys.KeyFrostWithdraw(withdrawID)
	}
	states, err := workerGetMany(w.stateReader, stateKeys)
	if err != nil {
		return false, "", err
	}
	for i, withdrawID := range job.WithdrawIDs {
		withdrawData := states[stateKeys[i]]
		if len(withdrawData) == 0 {
			return true, fmt.Sprintf("withdraw %s not found", withdrawID), nil
		}

		var state pb.FrostWithdrawState
		if err := proto.Unmarshal(withdrawData, &state); err != nil {
			return false, "", err
		}
		if state.Status != expectedStatus {
			if job.BTCProtocolVersion == 3 && state.BtcProtocolVersion == 3 && state.Status == "QUEUED" && claimApplied {
				continue                                                                      
			}
			w.forgetWithdrawSignedTx(job.JobID)
			return true, fmt.Sprintf("withdraw %s status=%s", withdrawID, state.Status), nil
		}
	}

	return false, "", nil
}

func (w *WithdrawWorker) cachedWithdrawSignedTx(jobID string) *pb.FrostWithdrawSignedTx {
	if w == nil || jobID == "" {
		return nil
	}
	w.signedTxMu.Lock()
	defer w.signedTxMu.Unlock()
	tx := w.signedTxByJob[jobID]
	if tx == nil {
		return nil
	}
	return proto.Clone(tx).(*pb.FrostWithdrawSignedTx)
}

func (w *WithdrawWorker) rememberWithdrawSignedTx(tx *pb.FrostWithdrawSignedTx) {
	if w == nil || tx == nil || tx.JobId == "" {
		return
	}
	w.signedTxMu.Lock()
	defer w.signedTxMu.Unlock()
	if w.signedTxByJob == nil {
		w.signedTxByJob = make(map[string]*pb.FrostWithdrawSignedTx)
	}
	w.signedTxByJob[tx.JobId] = proto.Clone(tx).(*pb.FrostWithdrawSignedTx)
}

func (w *WithdrawWorker) forgetWithdrawSignedTx(jobID string) {
	if w == nil || jobID == "" {
		return
	}
	w.signedTxMu.Lock()
	delete(w.signedTxByJob, jobID)
	w.signedTxMu.Unlock()
}

                                                                             
                                                                            
                                                                         
                                   
func (w *WithdrawWorker) pruneFinalizedWithdrawSignedTxs() {
	if w == nil || w.stateReader == nil {
		return
	}
	w.signedTxMu.Lock()
	jobIDs := make([]string, 0, len(w.signedTxByJob))
	for jobID := range w.signedTxByJob {
		jobIDs = append(jobIDs, jobID)
	}
	w.signedTxMu.Unlock()

	for _, jobID := range jobIDs {
		data, exists, err := w.stateReader.Get(keys.KeyFrostWithdrawJob(jobID))
		if err != nil {
			w.Logger.Warn("[WithdrawWorker] failed to inspect cached signed job %s: %v", jobID, err)
			continue
		}
		if !exists || len(data) == 0 {
			w.forgetWithdrawSignedTx(jobID)
			continue
		}
		var state pb.FrostWithdrawJobState
		if err := proto.Unmarshal(data, &state); err != nil {
			w.Logger.Warn("[WithdrawWorker] failed to decode cached signed job %s: %v", jobID, err)
			continue
		}
		if state.Status == "SIGNED" || state.Status == "FAILED" || state.SignedTxId != "" {
			w.forgetWithdrawSignedTx(jobID)
		}
	}
}

func (w *WithdrawWorker) findPendingWithdrawSignedTx(jobID string) (*pb.FrostWithdrawSignedTx, error) {
	if w == nil || w.stateReader == nil || jobID == "" {
		return nil, nil
	}
	if _, checked := w.pendingRecoveryChecked.Load(jobID); checked {
		return nil, nil
	}
	var found *pb.FrostWithdrawSignedTx
	err := w.stateReader.Scan(keys.KeyPendingAnyTx(""), func(key string, value []byte) bool {
		var anyTx pb.AnyTx
		                                                                       
		                                                                  
		if err := proto.Unmarshal(value, &anyTx); err != nil {
			return true
		}
		signed := anyTx.GetFrostWithdrawSignedTx()
		if signed == nil || signed.JobId != jobID {
			return true
		}
		txID := signed.GetBase().GetTxId()
		if txID == "" || key != keys.KeyPendingAnyTx(txID) {
			return true
		}
		found = proto.Clone(signed).(*pb.FrostWithdrawSignedTx)
		return false
	})
	return found, err
}

                                                                                
                                                                                
func (w *WithdrawWorker) preparePendingWithdrawSignedTxs(jobs []*planning.Job) error {
	if w == nil || w.stateReader == nil {
		return nil
	}
	wanted := make(map[string]bool, len(jobs))
	stateKeys := make([]string, 0, len(jobs))
	for _, job := range jobs {
		if job != nil && w.cachedWithdrawSignedTx(job.JobID) == nil {
			if _, active := w.activeJobs.Load(job.JobID); !active {
				wanted[job.JobID] = true
				stateKeys = append(stateKeys, keys.KeyFrostWithdrawJob(job.JobID))
			}
		}
	}
	states, err := workerGetMany(w.stateReader, stateKeys)
	if err != nil {
		return err
	}
	for id := range wanted {
		var state pb.FrostWithdrawJobState
		if data := states[keys.KeyFrostWithdrawJob(id)]; len(data) == 0 || proto.Unmarshal(data, &state) != nil || state.Status != "CLAIMED" {
			delete(wanted, id)
		}
	}
	if len(wanted) == 0 {
		return nil
	}
	err = w.stateReader.Scan(keys.KeyPendingAnyTx(""), func(key string, value []byte) bool {
		var anyTx pb.AnyTx
		if proto.Unmarshal(value, &anyTx) != nil {
			return true
		}
		tx := anyTx.GetFrostWithdrawSignedTx()
		if tx != nil && wanted[tx.JobId] && tx.GetBase().GetTxId() != "" && key == keys.KeyPendingAnyTx(tx.GetBase().GetTxId()) {
			w.rememberWithdrawSignedTx(tx)
		}
		return true
	})
	if err == nil {
		                                                                      
		w.pendingRecoveryChecked.Range(func(key, _ any) bool {
			if _, active := w.activeJobs.Load(key); !active {
				w.pendingRecoveryChecked.Delete(key)
			}
			return true
		})
		for id := range wanted {
			w.pendingRecoveryChecked.Store(id, true)
		}
	}
	return err
}

func (w *WithdrawWorker) isJobCoordinator(job *planning.Job) (bool, string, error) {
	committee, err := w.vaultProvider.VaultCommittee(job.Chain, job.VaultID, job.KeyEpoch)
	if err != nil {
		return false, "", err
	}
	if len(committee) == 0 {
		return false, "", fmt.Errorf("empty committee for %s/%d/%d", job.Chain, job.VaultID, job.KeyEpoch)
	}

	coordIndex := computeJobCoordinatorIndex(job.JobID, job.KeyEpoch, committee)
	if coordIndex < 0 || coordIndex >= len(committee) {
		return false, "", fmt.Errorf("invalid coordinator index=%d", coordIndex)
	}

	coordAddr := string(committee[coordIndex].ID)
	return w.localAddress == coordAddr, coordAddr, nil
}

func computeJobCoordinatorIndex(jobID string, keyEpoch uint64, committee []SignerInfo) int {
	if len(committee) == 0 {
		return 0
	}
	seed := computeJobCoordinatorSeed(jobID, keyEpoch)
	permuted := permuteSignerCommittee(committee, seed)
	target := permuted[0].ID
	for i, member := range committee {
		if member.ID == target {
			return i
		}
	}
	return 0
}

func computeJobCoordinatorSeed(jobID string, keyEpoch uint64) []byte {
	                                                          
	data := jobID + "|" + string(rune(keyEpoch)) + "|frost_agg"
	hash := sha256.Sum256([]byte(data))
	return hash[:]
}

func permuteSignerCommittee(committee []SignerInfo, seed []byte) []SignerInfo {
	result := make([]SignerInfo, len(committee))
	copy(result, committee)
	for i := len(result) - 1; i > 0; i-- {
		indexSeed := sha256.Sum256(append(seed, byte(i)))
		j := int(indexSeed[0]) % (i + 1)
		result[i], result[j] = result[j], result[i]
	}
	return result
}
