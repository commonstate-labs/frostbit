                                           
                                                                                                         
                           
                                                             
                                                            
                                                                                                
                                                                                

package workers

import (
	"context"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/frost/chain/evm"
	"dex/frost/runtime/planning"
	"dex/keys"
	"dex/lifecycle"
	"dex/logs"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

                                       
type WithdrawWorker struct {
	stateReader            StateReader
	windowPlanner          *planning.JobWindowPlanner
	txSubmitter            TxSubmitter
	logReporter            LogReporter
	signingService         SigningService
	vaultProvider          VaultCommitteeProvider
	maxInFlight            int
	localAddress           string
	localIdentity          LocalIdentity
	Logger                 logs.Logger
	logCounter             atomic.Uint64
	activeJobs             sync.Map                                                                      
	pendingRecoveryChecked sync.Map                                                                          
	pipelineMu             sync.Mutex
	pipelineCount          int
	retryAfter             map[string]uint64
	signedTxMu             sync.Mutex
	signedTxByJob          map[string]*pb.FrostWithdrawSignedTx
	blockedLogMu           sync.Mutex
	blockedLogLast         map[string]time.Time
	nonceMu                sync.Mutex
	lastIssuedNonce        uint64
	issuedNonce            bool
	emergency              *lifecycle.EmergencyStop
}

func (w *WithdrawWorker) planningHeight() uint64 {
	if w.stateReader != nil {
		if raw, ok, e := w.stateReader.Get(keys.KeyLatestHeight()); e == nil && ok {
			n, _ := strconv.ParseUint(string(raw), 10, 64)
			return n
		}
	}
	return 0
}
func (w *WithdrawWorker) reservePipeline(jobID string) bool {
	h := w.planningHeight()
	w.pipelineMu.Lock()
	defer w.pipelineMu.Unlock()
	if w.pipelineCount >= w.maxInFlight && w.maxInFlight > 0 {
		return false
	}
	if until := w.retryAfter[jobID]; h > 0 && h < until {
		return false
	}
	w.pipelineCount++
	return true
}
func (w *WithdrawWorker) releasePipeline(jobID string) {
	h := w.planningHeight()
	w.pipelineMu.Lock()
	defer w.pipelineMu.Unlock()
	w.pipelineCount--
	if w.retryAfter == nil {
		w.retryAfter = make(map[string]uint64)
	}
	if len(w.retryAfter) >= 4096 {
		for k, until := range w.retryAfter {
			if until <= h {
				delete(w.retryAfter, k)
			}
		}
	}
	if len(w.retryAfter) < 4096 && h <= ^uint64(0)-3 {
		w.retryAfter[jobID] = h + 3
	}
}

const blockedPlanningLogCooldown = 30 * time.Second

                                                                                            
                                                    
func (w *WithdrawWorker) SetEmergencyStop(e *lifecycle.EmergencyStop) {
	if w == nil {
		return
	}
	w.emergency = e
}

                                          
func (w *WithdrawWorker) newBaseMessage() *pb.BaseMessage {
	return &pb.BaseMessage{
		FromAddress:    w.localAddress,
		Nonce:          w.nextLocalNonce(),
		ExecutedHeight: 0,
		Status:         pb.Status_PENDING,
		Fee:            "1000",
	}
}

func (w *WithdrawWorker) nextLocalNonce() uint64 {
	var current uint64
	if w != nil && w.stateReader != nil && w.localAddress != "" {
		if data, exists, err := w.stateReader.Get(keys.KeyAccount(w.localAddress)); err == nil && exists && len(data) > 0 {
			var acc pb.Account
			if err := proto.Unmarshal(data, &acc); err == nil {
				current = acc.Nonce
			}
		}
	}
	w.nonceMu.Lock()
	defer w.nonceMu.Unlock()
	nonce := current
	if w.issuedNonce && nonce <= w.lastIssuedNonce {
		nonce = w.lastIssuedNonce + 1
	}
	w.lastIssuedNonce = nonce
	w.issuedNonce = true
	return nonce
}

func NewWithdrawWorker(
	stateReader StateReader,
	adapterFactory chain.ChainAdapterFactory,
	txSubmitter TxSubmitter,
	logReporter LogReporter,
	signingService SigningService,
	vaultProvider VaultCommitteeProvider,
	maxInFlight int,
	localAddress string,
	localIdentity LocalIdentity,
	logger logs.Logger,
) *WithdrawWorker {
	if maxInFlight <= 0 {
		maxInFlight = 1
	}
	return &WithdrawWorker{
		stateReader:    stateReader,
		windowPlanner:  planning.NewJobWindowPlanner(stateReader, adapterFactory, maxInFlight),
		txSubmitter:    txSubmitter,
		logReporter:    logReporter,
		signingService: signingService,
		vaultProvider:  vaultProvider,
		maxInFlight:    maxInFlight,
		localAddress:   localAddress,
		localIdentity:  localIdentity,
		Logger:         logger,
		signedTxByJob:  make(map[string]*pb.FrostWithdrawSignedTx),
	}
}

                                                                          
func (w *WithdrawWorker) ProcessOnce(ctx context.Context, chain, asset string) (*planning.Job, error) {
	jobs, err := w.ProcessWindow(ctx, chain, asset)
	if err != nil {
		return nil, err
	}
	if len(jobs) == 0 {
		return nil, nil
	}
	if err := w.preparePendingWithdrawSignedTxs(jobs); err != nil {
		return nil, err
	}
	return jobs[0], nil
}

                                                    
                                                                 
                                   
                               
                           
func (w *WithdrawWorker) ProcessWindow(ctx context.Context, chain, asset string) ([]*planning.Job, error) {
	w.pruneFinalizedWithdrawSignedTxs()
	if w.emergency.IsStopped() {                                                                                                       
		return nil, nil
	}
	jobs, transientLogs, err := w.windowPlanner.PlanJobWindow(chain, asset)
	if err != nil {
		return nil, err
	}

	                                                                          
	withdrawIDs := make([]string, 0, len(transientLogs))
	for withdrawID := range transientLogs {
		withdrawIDs = append(withdrawIDs, withdrawID)
	}
	sortStrings(withdrawIDs)
	for _, withdrawID := range withdrawIDs {
		logsForID := w.filterBlockedPlanningLogs(chain, asset, withdrawID, transientLogs[withdrawID])
		if len(logsForID) == 0 {
			continue
		}
		w.reportPlanningLogs(ctx, &planning.Job{
			JobID:       "planning_failed",
			WithdrawIDs: []string{withdrawID},
			Logs:        logsForID,
		})
	}

	if len(jobs) == 0 {
		return nil, nil
	}

	                                                                            
	                                                                         
	                                                                        
	newJobs := make([]*planning.Job, 0, len(jobs))
	for _, job := range jobs {
		isCoordinator, coordinatorAddr, err := w.isJobCoordinator(job)
		if err != nil {
			w.Logger.Warn("[WithdrawWorker] failed to resolve coordinator for job %s: %v", job.JobID, err)
			continue
		}
		if !isCoordinator {
			w.Logger.Trace("[WithdrawWorker] skip job %s before pipeline start: local=%s coordinator=%s", job.JobID, w.localAddress, coordinatorAddr)
			continue
		}
		if skip, reason, err := w.shouldSkipJob(job); err != nil {
			w.Logger.Warn("[WithdrawWorker] skip-check failed for job %s: %v", job.JobID, err)
			continue
		} else if skip {
			w.Logger.Trace("[WithdrawWorker] skip job %s: %s", job.JobID, reason)
			continue
		}
		if _, loaded := w.activeJobs.LoadOrStore(job.JobID, struct{}{}); loaded {
			w.Logger.Trace("[WithdrawWorker] skip job %s: pipeline already active", job.JobID)
			continue
		}
		if !w.reservePipeline(job.JobID) {
			w.activeJobs.Delete(job.JobID)
			continue
		}
		newJobs = append(newJobs, job)
	}
	if len(newJobs) == 0 {
		return nil, nil
	}
	jobs = newJobs

	w.Logger.Info("[WithdrawWorker] planned %d jobs for %s/%s", len(jobs), chain, asset)

	for _, job := range jobs {
		w.reportPlanningLogs(ctx, job)
	}

	                                                           
	for _, job := range jobs {
		job := job
		go func() {
			defer w.activeJobs.Delete(job.JobID)
			defer w.pendingRecoveryChecked.Delete(job.JobID)
			defer w.releasePipeline(job.JobID)
			w.processJobAsync(ctx, job)
		}()
	}

	return jobs, nil
}

func (w *WithdrawWorker) filterBlockedPlanningLogs(chain, asset, withdrawID string, entries []*pb.FrostPlanningLog) []*pb.FrostPlanningLog {
	if len(entries) == 0 {
		return nil
	}
	now := time.Now()
	out := make([]*pb.FrostPlanningLog, 0, len(entries))
	for _, entry := range entries {
		if w.shouldReportBlockedPlanningLog(chain, asset, withdrawID, entry, now) {
			out = append(out, entry)
		}
	}
	return out
}

func (w *WithdrawWorker) shouldReportBlockedPlanningLog(chain, asset, withdrawID string, entry *pb.FrostPlanningLog, now time.Time) bool {
	if w == nil || entry == nil {
		return false
	}
	key := strings.Join([]string{chain, asset, withdrawID, entry.Step, entry.Status, entry.Message}, "\x00")
	w.blockedLogMu.Lock()
	defer w.blockedLogMu.Unlock()
	if w.blockedLogLast == nil {
		w.blockedLogLast = make(map[string]time.Time)
	}
	if last, ok := w.blockedLogLast[key]; ok && now.Sub(last) < blockedPlanningLogCooldown {
		return false
	}
	w.blockedLogLast[key] = now
	if len(w.blockedLogLast) > 4096 {
		for k, ts := range w.blockedLogLast {
			if now.Sub(ts) > blockedPlanningLogCooldown {
				delete(w.blockedLogLast, k)
			}
		}
	}
	return true
}

                                                                                                  
func (w *WithdrawWorker) reportPlanningLogs(ctx context.Context, job *planning.Job) {
	if job == nil || len(job.Logs) == 0 {
		return
	}

	for _, withdrawID := range job.WithdrawIDs {
		planningLog := &pb.FrostWithdrawPlanningLogTx{
			Reporter:   w.localAddress,
			WithdrawId: withdrawID,
			Logs:       job.Logs,
		}
		if err := w.logReporter.ReportWithdrawPlanningLog(ctx, planningLog); err != nil {
			w.Logger.Warn("[WithdrawWorker] failed to report planning logs for %s: %v", withdrawID, err)
		}
	}
}

                                                                   

                                                             
func (w *WithdrawWorker) calculateThreshold(chain string, vaultID uint32) (int, error) {
	return w.vaultProvider.CalculateThreshold(chain, vaultID)
}

                                                                                       
func (w *WithdrawWorker) getSignAlgo(chain string, vaultID uint32) (pb.SignAlgo, error) {
	switch chain {
	case "btc":
		return pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340, nil
	case "eth", "bnb", "base", "arb", "trx":
		return pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128, nil
	default:
		return pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340, nil
	}
}

                                                                                                                
func (w *WithdrawWorker) extractMessages(job *planning.Job) ([][]byte, error) {
	if isBTCChain(job.Chain) {
		template, err := w.parseBTCTemplate(job)
		if err != nil {
			return nil, err
		}

		scriptPubkeys, err := w.loadBTCScriptPubkeys(job.Chain, job.VaultID, template)
		if err != nil {
			return nil, err
		}

		sighashes, err := template.ComputeTaprootSighash(scriptPubkeys, btc.SighashDefault)
		if err != nil {
			return nil, fmt.Errorf("compute BTC sighashes: %w", err)
		}
		if len(sighashes) == 0 {
			return nil, fmt.Errorf("empty BTC sighashes for job %s", job.JobID)
		}
		return sighashes, nil
	}

	if isSupportedEVMSigningChain(job.Chain) {
		msg, err := evm.SigningHashFromTemplateData(job.TemplateData)
		if err != nil {
			return nil, fmt.Errorf("compute EVM signing hash for job %s: %w", job.JobID, err)
		}
		return [][]byte{msg}, nil
	}

	if len(job.TemplateHash) == 0 {
		return nil, fmt.Errorf("empty template hash for job %s", job.JobID)
	}
	return [][]byte{job.TemplateHash}, nil
}

func isSupportedEVMSigningChain(chainName string) bool {
	switch chain.NormalizeChain(chainName) {
	case chain.ChainETH, chain.ChainBNB, chain.ChainBASE, chain.ChainARB, chain.ChainTRX:
		return true
	default:
		return false
	}
}

                                                                                                         
func (w *WithdrawWorker) logSigningContext(job *planning.Job, threshold int, signAlgo pb.SignAlgo, messages [][]byte) {
	if job == nil {
		return
	}

	w.Logger.Info("[WithdrawWorker] signing context job=%s chain=%s asset=%s vault=%d epoch=%d threshold=%d sign_algo=%s withdraws=%v",
		job.JobID, job.Chain, job.Asset, job.VaultID, job.KeyEpoch, threshold, signAlgo.String(), job.WithdrawIDs)

	if len(messages) == 0 {
		w.Logger.Warn("[WithdrawWorker] signing context has no message to sign: job=%s", job.JobID)
	} else {
		for i, msg := range messages {
			w.Logger.Info("[WithdrawWorker] signing message job=%s chain=%s vault=%d task=%d payload=%s",
				job.JobID, job.Chain, job.VaultID, i, hex.EncodeToString(msg))
		}
	}

	if w.vaultProvider == nil {
		w.Logger.Warn("[WithdrawWorker] vault provider is nil, skip vault debug context for job=%s", job.JobID)
		return
	}

	committee, err := w.vaultProvider.VaultCommittee(job.Chain, job.VaultID, job.KeyEpoch)
	if err != nil {
		w.Logger.Warn("[WithdrawWorker] failed to load vault voter committee for job=%s chain=%s vault=%d epoch=%d: %v",
			job.JobID, job.Chain, job.VaultID, job.KeyEpoch, err)
	} else {
		w.Logger.Info("[WithdrawWorker] vault voter committee job=%s chain=%s vault=%d epoch=%d size=%d members=%s",
			job.JobID, job.Chain, job.VaultID, job.KeyEpoch, len(committee), formatSignerCommitteeForLog(committee))
	}

	groupPubkey, err := w.vaultProvider.VaultGroupPubkey(job.Chain, job.VaultID, job.KeyEpoch)
	if err != nil {
		w.Logger.Warn("[WithdrawWorker] failed to load aggregated public key for job=%s chain=%s vault=%d epoch=%d: %v",
			job.JobID, job.Chain, job.VaultID, job.KeyEpoch, err)
		return
	}
	w.Logger.Info("[WithdrawWorker] aggregated public key job=%s chain=%s vault=%d epoch=%d pubkey=%s",
		job.JobID, job.Chain, job.VaultID, job.KeyEpoch, hex.EncodeToString(groupPubkey))
}

func formatSignerCommitteeForLog(committee []SignerInfo) string {
	if len(committee) == 0 {
		return "[]"
	}
	members := make([]string, 0, len(committee))
	for i, member := range committee {
		memberID := string(member.ID)
		if memberID == "" {
			memberID = "unknown"
		}
		members = append(members, fmt.Sprintf("%d:%s", i+1, memberID))
	}
	return "[" + strings.Join(members, ",") + "]"
}

                                                                         
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
