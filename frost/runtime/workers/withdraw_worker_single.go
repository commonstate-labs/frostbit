                                                  
                                                                                                

package workers

import (
	"context"
	"fmt"
	"time"

	"dex/frost/runtime/planning"
	"dex/pb"
	"dex/txverify"

	"google.golang.org/protobuf/proto"
)

                                                      
func (w *WithdrawWorker) processJobAsync(ctx context.Context, job *planning.Job) {
	if skip, reason, err := w.shouldSkipJob(job); err != nil {
		w.Logger.Warn("[WithdrawWorker] skip-check failed for job %s: %v", job.JobID, err)
		return
	} else if skip {
		w.Logger.Trace("[WithdrawWorker] skip job %s: %s", job.JobID, reason)
		return
	}

	isCoordinator, coordinatorAddr, err := w.isJobCoordinator(job)
	if err != nil {
		w.Logger.Warn("[WithdrawWorker] failed to resolve coordinator for job %s: %v", job.JobID, err)
		return
	}
	if !isCoordinator {
		w.Logger.Trace("[WithdrawWorker] skip job %s: local=%s coordinator=%s", job.JobID, w.localAddress, coordinatorAddr)
		return
	}

	                                                                        
	                                                                           
	                                             
	if cached := w.cachedWithdrawSignedTx(job.JobID); cached != nil {
		w.submitPreparedWithdrawSignedTx(ctx, job, cached)
		return
	}

	claimApplied, err := w.isWithdrawClaimApplied(job)
	if err != nil {
		w.Logger.Error("[WithdrawWorker] invalid finalized claim for job %s: %v", job.JobID, err)
		return
	}
	if !claimApplied {
		if err := w.submitWithdrawClaim(ctx, job); err != nil {
			w.Logger.Error("[WithdrawWorker] failed to submit claim tx for job %s: %v", job.JobID, err)
			return
		}
		if err := w.waitForWithdrawClaim(ctx, job); err != nil {
			w.Logger.Error("[WithdrawWorker] claim tx for job %s not applied: %v", job.JobID, err)
			return
		}
	} else {
		w.Logger.Info("[WithdrawWorker] resuming finalized claim for job %s", job.JobID)
	}
	if !isBTCChain(job.Chain) {
		if err := w.validateAccountClaimReservation(job); err != nil {
			w.Logger.Error("[WithdrawWorker] invalid finalized account reservation for job %s: %v", job.JobID, err)
			return
		}
	}
	w.Logger.Info("[WithdrawWorker] claim applied for job %s, starting signing", job.JobID)

	threshold, err := w.calculateThreshold(job.Chain, job.VaultID)
	if err != nil {
		w.Logger.Error("[WithdrawWorker] failed to calculate threshold: %v", err)
		return
	}

	signAlgo, err := w.getSignAlgo(job.Chain, job.VaultID)
	if err != nil {
		w.Logger.Error("[WithdrawWorker] failed to get sign algo: %v", err)
		return
	}

	messages, err := w.extractMessages(job)
	if err != nil {
		w.Logger.Error("[WithdrawWorker] failed to extract messages: %v", err)
		return
	}
	w.logSigningContext(job, threshold, signAlgo, messages)

	var tweaks [][]byte
	if isBTCChain(job.Chain) {
		tweaks = w.loadBTCInputTweaks(job)
	}

	sessionParams := &SigningSessionParams{
		RequireGuardian: true,
		JobID:           job.JobID,
		Chain:           job.Chain,
		VaultID:         job.VaultID,
		KeyEpoch:        job.KeyEpoch,
		SignAlgo:        signAlgo,
		Messages:        messages,
		Threshold:       threshold,
		Tweaks:          tweaks,
	}

	sessionID, err := w.signingService.StartSigningSession(ctx, sessionParams)
	if err != nil {
		w.Logger.Error("[WithdrawWorker] failed to start signing session: %v", err)
		return
	}

	w.Logger.Info("[WithdrawWorker] started signing session %s for job %s", sessionID, job.JobID)
	defer w.signingService.CancelSession(sessionID)

	w.reportPlanningLogs(ctx, &planning.Job{
		JobID:       job.JobID,
		WithdrawIDs: job.WithdrawIDs,
		Logs: []*pb.FrostPlanningLog{
			{Step: "StartSigning", Status: "OK", Message: fmt.Sprintf("Signing session %s started", sessionID), Timestamp: uint64(time.Now().UnixMilli())},
		},
	})

	w.waitAndSubmit(ctx, job, sessionID)
}

                                                        
func (w *WithdrawWorker) waitAndSubmit(ctx context.Context, job *planning.Job, sessionID string) bool {
	timeout := 5 * time.Minute
	waitCtx := context.Background()
	if ctx != nil {
		                                                                                  
		waitCtx = context.WithoutCancel(ctx)
	}
	signedPkg, err := w.signingService.WaitForCompletion(waitCtx, sessionID, timeout)
	if err != nil {
		reason := err.Error()
		if status, statusErr := w.signingService.GetSessionStatus(sessionID); statusErr == nil && status != nil {
			if status.Error != nil {
				reason = fmt.Sprintf("%s (state=%s, progress=%.2f, session_error=%v)", reason, status.State, status.Progress, status.Error)
			} else {
				reason = fmt.Sprintf("%s (state=%s, progress=%.2f)", reason, status.State, status.Progress)
			}
		}
		w.Logger.Error("[WithdrawWorker] signing session %s failed: %s", sessionID, reason)
		w.reportPlanningLogs(ctx, &planning.Job{
			JobID:       job.JobID,
			WithdrawIDs: job.WithdrawIDs,
			Logs: []*pb.FrostPlanningLog{
				{Step: "Signing", Status: "FAILED", Message: fmt.Sprintf("Signing session failed: %s", reason), Timestamp: uint64(time.Now().UnixMilli())},
			},
		})
		return false
	}

	w.reportPlanningLogs(ctx, &planning.Job{
		JobID:       job.JobID,
		WithdrawIDs: job.WithdrawIDs,
		Logs: []*pb.FrostPlanningLog{
			{Step: "Signing", Status: "OK", Message: "Aggregation signature completed", Timestamp: uint64(time.Now().UnixMilli())},
		},
	})

	withdrawSignedTx, err := w.getOrBuildWithdrawSignedTx(job, signedPkg)
	if err != nil {
		w.Logger.Error("[WithdrawWorker] failed to prepare signed tx for job %s: %v", job.JobID, err)
		return false
	}
	return w.submitPreparedWithdrawSignedTx(ctx, job, withdrawSignedTx)
}

                                                                             
                                                                             
                                                                
func (w *WithdrawWorker) getOrBuildWithdrawSignedTx(job *planning.Job, signedPkg *SignedPackage) (*pb.FrostWithdrawSignedTx, error) {
	if w == nil || job == nil {
		return nil, fmt.Errorf("withdraw job is nil")
	}

	w.signedTxMu.Lock()
	defer w.signedTxMu.Unlock()
	if cached := w.signedTxByJob[job.JobID]; cached != nil {
		return proto.Clone(cached).(*pb.FrostWithdrawSignedTx), nil
	}

	signedPackageBytes, err := w.buildSignedPackageBytes(signedPkg)
	if err != nil {
		return nil, fmt.Errorf("build signed package: %w", err)
	}

	withdrawSignedTx := &pb.FrostWithdrawSignedTx{
		GuardianSigs:       cloneMigrationByteSlices(signedPkg.GuardianSignatures),
		Base:               w.newBaseMessage(),
		JobId:              job.JobID,
		SignedPackageBytes: signedPackageBytes,
		WithdrawIds:        job.WithdrawIDs,
		VaultId:            job.VaultID,
		KeyEpoch:           job.KeyEpoch,
		TemplateHash:       job.TemplateHash,
		Chain:              job.Chain,
		Asset:              job.Asset,
		TemplateData:       job.TemplateData,
		BatchSeq:           job.BatchSeq,
		IncludesHandover:   job.IncludesHandover,
	}

	if isBTCChain(job.Chain) {
		template, err := w.parseBTCTemplate(job)
		if err != nil {
			return nil, fmt.Errorf("parse BTC template: %w", err)
		}

		inputSigs, err := w.resolveBTCInputSignatures(signedPkg, len(template.Inputs))
		if err != nil {
			return nil, fmt.Errorf("resolve BTC input signatures: %w", err)
		}

		scriptPubkeys, err := w.loadBTCScriptPubkeys(job.Chain, job.VaultID, template)
		if err != nil {
			return nil, fmt.Errorf("load BTC input scriptPubkeys: %w", err)
		}
		inputTweaks := w.loadBTCInputTweaks(job)
		if err := w.validateBTCSignaturePreSubmit(job, template, scriptPubkeys, inputSigs, inputTweaks, signedPkg.GuardianSignatures); err != nil {
			return nil, fmt.Errorf("verify BTC signature before submit: %w", err)
		}

		withdrawSignedTx.InputSigs = inputSigs
		withdrawSignedTx.ScriptPubkeys = scriptPubkeys
	}

	anyTx := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: withdrawSignedTx}}
	if _, err := txverify.SignWithSigner(anyTx, w.localIdentity); err != nil {
		return nil, fmt.Errorf("sign withdraw tx envelope: %w", err)
	}

	if w.signedTxByJob == nil {
		w.signedTxByJob = make(map[string]*pb.FrostWithdrawSignedTx)
	}
	w.signedTxByJob[job.JobID] = proto.Clone(withdrawSignedTx).(*pb.FrostWithdrawSignedTx)
	return proto.Clone(withdrawSignedTx).(*pb.FrostWithdrawSignedTx), nil
}

func (w *WithdrawWorker) submitPreparedWithdrawSignedTx(ctx context.Context, job *planning.Job, tx *pb.FrostWithdrawSignedTx) bool {
	if w == nil || job == nil || tx == nil {
		return false
	}
	toSubmit := proto.Clone(tx).(*pb.FrostWithdrawSignedTx)
	if err := w.txSubmitter.SubmitWithdrawSignedTx(ctx, toSubmit); err != nil {
		w.Logger.Error("[WithdrawWorker] failed to submit signed tx job=%s tx=%s: %v", job.JobID, tx.GetBase().GetTxId(), err)
		return false
	}
	w.Logger.Info("[WithdrawWorker] successfully submitted signed tx job=%s tx=%s", job.JobID, tx.GetBase().GetTxId())
	return true
}
