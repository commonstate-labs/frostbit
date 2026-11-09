                                                 
                                                            

package roast

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"dex/frost/core/frost"
	roastsession "dex/frost/runtime/session"
	"dex/logs"
	"dex/pb"
)

func (c *Coordinator) logInfo(format string, args ...any) {
	if c != nil && c.Logger != nil {
		c.Logger.Info(format, args...)
		return
	}
	logs.Info(format, args...)
}

func (c *Coordinator) logWarn(format string, args ...any) {
	if c != nil && c.Logger != nil {
		c.Logger.Warn(format, args...)
		return
	}
	logs.Warn(format, args...)
}

func formatSignerInfoList(committee []SignerInfo) string {
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

func participantAddressesBySignerIDs(sess *roastsession.Session, signerIDs []int) []string {
	if len(signerIDs) == 0 {
		return nil
	}
	addresses := make([]string, 0, len(signerIDs))
	for _, signerID := range signerIDs {
		addr := fmt.Sprintf("signer#%d", signerID)
		participantIndex := signerID - 1
		if sess != nil && participantIndex >= 0 && participantIndex < len(sess.Committee) {
			if sess.Committee[participantIndex].ID != "" {
				addr = sess.Committee[participantIndex].ID
			}
		}
		addresses = append(addresses, addr)
	}
	return addresses
}

func participantIndexFor(sess *roastsession.Session, nodeID NodeID) (int, bool) {
	if sess == nil {
		return -1, false
	}
	for i, member := range sess.Committee {
		if member.ID == string(nodeID) {
			return i, true
		}
	}
	return -1, false
}

func formatSessionDump(sess *roastsession.Session) string {
	if sess == nil {
		return "<nil>"
	}

	signAlgo := pb.SignAlgo(sess.SignAlgo)
	selectedSet := sess.SelectedSetSnapshot()
	indexes := collectSessionParticipantIndexes(sess, selectedSet)
	committeeDump := make([]string, 0, len(sess.Committee))
	for i, participant := range sess.Committee {
		committeeDump = append(committeeDump,
			fmt.Sprintf("{index=%d id=%q address=%q ip=%q}", i, participant.ID, participant.Address, participant.IP))
	}

	nonceDump := make([]string, 0, len(indexes))
	shareDump := make([]string, 0, len(indexes))
	for _, idx := range indexes {
		label := formatSessionParticipantLabel(sess, idx)
		if nonce, ok := sess.GetNonce(idx); ok {
			nonceDump = append(nonceDump,
				fmt.Sprintf("{participant=%s received_at=%s hiding=%s binding=%s}",
					label,
					formatTimeForLog(nonce.ReceivedAt),
					formatByteMatrixHex(nonce.HidingNonces),
					formatByteMatrixHex(nonce.BindingNonces)))
		} else {
			nonceDump = append(nonceDump, fmt.Sprintf("{participant=%s missing=true}", label))
		}

		if share, ok := sess.GetShare(idx); ok {
			shareDump = append(shareDump,
				fmt.Sprintf("{participant=%s received_at=%s shares=%s}",
					label,
					formatTimeForLog(share.ReceivedAt),
					formatByteMatrixHex(share.Shares)))
		} else {
			shareDump = append(shareDump, fmt.Sprintf("{participant=%s missing=true}", label))
		}
	}

	return fmt.Sprintf("{job_id=%q vault_id=%d chain=%q key_epoch=%d sign_algo=%s threshold=%d my_index=%d state=%s retry_count=%d start_height=%d started_at=%s completed_at=%s selected_set=%v messages=%s committee=%s nonces=%s shares=%s final_signatures=%s}",
		sess.JobID,
		sess.VaultID,
		sess.Chain,
		sess.KeyEpoch,
		signAlgo.String(),
		sess.Threshold,
		sess.MyIndex,
		sess.GetState().String(),
		sess.RetryCount,
		sess.StartHeight,
		formatTimeForLog(sess.StartedAt),
		formatTimeForLog(sess.CompletedAt),
		selectedSet,
		formatByteMatrixHex(sess.Messages),
		"["+strings.Join(committeeDump, ",")+"]",
		"["+strings.Join(nonceDump, ",")+"]",
		"["+strings.Join(shareDump, ",")+"]",
		formatByteMatrixHex(sess.GetFinalSignatures()),
	)
}

func collectSessionParticipantIndexes(sess *roastsession.Session, selectedSet []int) []int {
	if sess == nil {
		return nil
	}
	indexes := make([]int, 0, len(sess.Committee)+len(selectedSet))
	seen := make(map[int]struct{}, len(sess.Committee)+len(selectedSet))
	for i := range sess.Committee {
		indexes = append(indexes, i)
		seen[i] = struct{}{}
	}
	for _, idx := range selectedSet {
		if _, ok := seen[idx]; ok {
			continue
		}
		indexes = append(indexes, idx)
		seen[idx] = struct{}{}
	}
	return indexes
}

func formatSessionParticipantLabel(sess *roastsession.Session, idx int) string {
	if sess != nil && idx >= 0 && idx < len(sess.Committee) {
		id := sess.Committee[idx].ID
		if id != "" {
			return fmt.Sprintf("%d(%s)", idx, id)
		}
	}
	return fmt.Sprintf("%d", idx)
}

func formatByteMatrixHex(matrix [][]byte) string {
	if len(matrix) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(matrix))
	for _, data := range matrix {
		parts = append(parts, hex.EncodeToString(data))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func formatTimeForLog(t time.Time) string {
	if t.IsZero() {
		return "zero"
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func (c *Coordinator) logVerifyPanicDiagnostics(sess *roastsession.Session, taskIdx int, msg, sig []byte, shareSignerIDs []int) {
	if c == nil || sess == nil {
		return
	}

	signAlgo := pb.SignAlgo(sess.SignAlgo)
	selectedSet := sess.SelectedSetSnapshot()
	expectedSignerIDs := make([]int, 0, len(selectedSet))
	nonceSignerIDs := make([]int, 0, len(selectedSet))

	nonceFPGroups := make(map[string][]int)
	shareFPGroups := make(map[string][]int)
	for _, idx := range selectedSet {
		signerID := idx + 1
		expectedSignerIDs = append(expectedSignerIDs, signerID)

		if nonce, ok := sess.GetNonce(idx); ok && taskIdx < len(nonce.HidingNonces) && taskIdx < len(nonce.BindingNonces) {
			nonceSignerIDs = append(nonceSignerIDs, signerID)
			nonceFP := fmt.Sprintf("h=%s,b=%s",
				dataFingerprintForLog(nonce.HidingNonces[taskIdx]),
				dataFingerprintForLog(nonce.BindingNonces[taskIdx]))
			nonceFPGroups[nonceFP] = append(nonceFPGroups[nonceFP], signerID)
		}
		if share, ok := sess.GetShare(idx); ok && taskIdx < len(share.Shares) {
			shareFP := dataFingerprintForLog(share.Shares[taskIdx])
			shareFPGroups[shareFP] = append(shareFPGroups[shareFP], signerID)
		}
	}

	c.logWarn("[Coordinator][panic-diagnose] task=%d job=%s chain=%s vault=%d epoch=%d sign_algo=%s threshold=%d selected_set=%v expected_signer_ids=%v nonce_signer_ids=%v share_signer_ids=%v msg_fp=%s sig_fp=%s",
		taskIdx, sess.JobID, sess.Chain, sess.VaultID, sess.KeyEpoch, signAlgo.String(), sess.Threshold, selectedSet,
		expectedSignerIDs, nonceSignerIDs, shareSignerIDs, dataFingerprintForLog(msg), dataFingerprintForLog(sig))

	aggNoncePayload := c.aggregateNonces(sess)
	payloadSignerIDs, nonceBlob, hasHeader := decodeAggregatedNoncePayload(aggNoncePayload)
	legacyPosSignerIDs := make([]int, len(selectedSet))
	for i := range selectedSet {
		legacyPosSignerIDs[i] = i + 1
	}
	c.logWarn("[Coordinator][panic-diagnose] nonce payload header=%t payload_signer_ids=%v legacy_pos_signer_ids=%v payload_fp=%s nonce_blob_fp=%s",
		hasHeader, payloadSignerIDs, legacyPosSignerIDs, dataFingerprintForLog(aggNoncePayload), dataFingerprintForLog(nonceBlob))

	missingShareSignerIDs, extraShareSignerIDs := diffIntSet(expectedSignerIDs, shareSignerIDs)
	if len(missingShareSignerIDs) > 0 || len(extraShareSignerIDs) > 0 {
		c.logWarn("[Coordinator][panic-diagnose] signer mismatch expected_vs_share missing=%v extra=%v",
			missingShareSignerIDs, extraShareSignerIDs)
	}

	for shareFP, signerIDs := range shareFPGroups {
		if len(signerIDs) <= 1 {
			continue
		}
		c.logWarn("[Coordinator][panic-diagnose] duplicate share fingerprint task=%d share_fp=%s signer_ids=%v (possible wrong share source/mapping)",
			taskIdx, shareFP, signerIDs)
	}
	for nonceFP, signerIDs := range nonceFPGroups {
		if len(signerIDs) <= 1 {
			continue
		}
		c.logWarn("[Coordinator][panic-diagnose] duplicate nonce fingerprint task=%d nonce_fp=%s signer_ids=%v (possible nonce/mapping issue)",
			taskIdx, nonceFP, signerIDs)
	}

	selectedPosSignerID := make(map[int]int, len(selectedSet))
	for pos, idx := range selectedSet {
		selectedPosSignerID[idx+1] = pos + 1
	}
	for _, signerID := range shareSignerIDs {
		participantIndex := signerID - 1
		participantID := formatSessionParticipantLabel(sess, participantIndex)
		posID, inSelected := selectedPosSignerID[signerID]

		nonceSummary := "missing"
		if nonce, ok := sess.GetNonce(participantIndex); ok {
			nonceSummary = fmt.Sprintf("tasks_h=%d tasks_b=%d", len(nonce.HidingNonces), len(nonce.BindingNonces))
			if taskIdx < len(nonce.HidingNonces) && taskIdx < len(nonce.BindingNonces) {
				nonceSummary = fmt.Sprintf("%s task_h_fp=%s task_b_fp=%s",
					nonceSummary, dataFingerprintForLog(nonce.HidingNonces[taskIdx]), dataFingerprintForLog(nonce.BindingNonces[taskIdx]))
			} else {
				nonceSummary = nonceSummary + " task_missing=true"
			}
		}

		shareSummary := "missing"
		if share, ok := sess.GetShare(participantIndex); ok {
			shareSummary = fmt.Sprintf("tasks=%d", len(share.Shares))
			if taskIdx < len(share.Shares) {
				shareSummary = fmt.Sprintf("%s task_share_fp=%s", shareSummary, dataFingerprintForLog(share.Shares[taskIdx]))
			} else {
				shareSummary = shareSummary + " task_missing=true"
			}
		}

		c.logWarn("[Coordinator][panic-diagnose] signer detail signer_id=%d selected_pos_id=%d in_selected=%t participant=%s nonce={%s} share={%s}",
			signerID, posID, inSelected, participantID, nonceSummary, shareSummary)
	}

	if c.vaultProvider == nil {
		c.logWarn("[Coordinator][panic-diagnose] vault provider is nil, committee/group_pubkey cross-check skipped")
		return
	}

	chainCommittee, err := c.vaultProvider.VaultCommittee(sess.Chain, sess.VaultID, sess.KeyEpoch)
	if err != nil {
		c.logWarn("[Coordinator][panic-diagnose] load committee failed chain=%s vault=%d epoch=%d err=%v",
			sess.Chain, sess.VaultID, sess.KeyEpoch, err)
	} else {
		sessionCommitteeIDs := committeeIDsFromSession(sess.Committee)
		chainCommitteeIDs := committeeIDsFromSignerInfo(chainCommittee)
		onlySession, onlyChain := diffStringSet(sessionCommitteeIDs, chainCommitteeIDs)
		c.logWarn("[Coordinator][panic-diagnose] committee compare epoch=%d same_order=%t same_set=%t session_committee=%s chain_committee=%s only_session=%s only_chain=%s",
			sess.KeyEpoch,
			sameStringSlice(sessionCommitteeIDs, chainCommitteeIDs),
			len(onlySession) == 0 && len(onlyChain) == 0,
			formatStringSliceForLog(sessionCommitteeIDs),
			formatStringSliceForLog(chainCommitteeIDs),
			formatStringSliceForLog(onlySession),
			formatStringSliceForLog(onlyChain))
	}

	for _, epoch := range diagnosticEpochCandidates(sess.KeyEpoch) {
		groupPubkey, pubErr := c.vaultProvider.VaultGroupPubkey(sess.Chain, sess.VaultID, epoch)
		if pubErr != nil {
			c.logWarn("[Coordinator][panic-diagnose] epoch verify skipped epoch=%d load group pubkey failed: %v", epoch, pubErr)
			continue
		}
		valid, verifyErr, panicVal, verifyPubkey := verifyWithGroupPubkey(signAlgo, groupPubkey, taskTweakAt(sess.Tweaks, taskIdx), msg, sig)
		c.logWarn("[Coordinator][panic-diagnose] epoch verify epoch=%d group_pubkey_fp=%s verify_pubkey_fp=%s valid=%t err=%v panic=%v",
			epoch,
			dataFingerprintForLog(groupPubkey),
			dataFingerprintForLog(verifyPubkey),
			valid, verifyErr, panicVal)
	}
}

func verifyWithGroupPubkey(signAlgo pb.SignAlgo, groupPubkey, tweak, msg, sig []byte) (valid bool, err error, panicVal any, verifyPubkey []byte) {
	verifyPubkey, _, err = deriveSigningPubkeyForTask(signAlgo, groupPubkey, tweak)
	if err != nil {
		return false, err, nil, nil
	}

	defer func() {
		if r := recover(); r != nil {
			panicVal = r
		}
	}()

	valid, err = frost.Verify(signAlgo, verifyPubkey, msg, sig)
	return valid, err, nil, verifyPubkey
}

func diagnosticEpochCandidates(epoch uint64) []uint64 {
	candidates := make([]uint64, 0, 3)
	if epoch > 0 {
		candidates = append(candidates, epoch-1)
	}
	candidates = append(candidates, epoch)
	if epoch < ^uint64(0) {
		candidates = append(candidates, epoch+1)
	}
	return candidates
}

func committeeIDsFromSession(committee []roastsession.Participant) []string {
	ids := make([]string, 0, len(committee))
	for i, member := range committee {
		if member.ID == "" {
			ids = append(ids, fmt.Sprintf("<empty@%d>", i))
			continue
		}
		ids = append(ids, member.ID)
	}
	return ids
}

func committeeIDsFromSignerInfo(committee []SignerInfo) []string {
	ids := make([]string, 0, len(committee))
	for i, member := range committee {
		memberID := strings.TrimSpace(string(member.ID))
		if memberID == "" {
			ids = append(ids, fmt.Sprintf("<empty@%d>", i))
			continue
		}
		ids = append(ids, memberID)
	}
	return ids
}

func sameStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func diffStringSet(expected, actual []string) (missing []string, extra []string) {
	expectedSet := make(map[string]struct{}, len(expected))
	actualSet := make(map[string]struct{}, len(actual))
	for _, item := range expected {
		expectedSet[item] = struct{}{}
	}
	for _, item := range actual {
		actualSet[item] = struct{}{}
	}
	for item := range expectedSet {
		if _, ok := actualSet[item]; !ok {
			missing = append(missing, item)
		}
	}
	for item := range actualSet {
		if _, ok := expectedSet[item]; !ok {
			extra = append(extra, item)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return missing, extra
}

func diffIntSet(expected, actual []int) (missing []int, extra []int) {
	expectedSet := make(map[int]struct{}, len(expected))
	actualSet := make(map[int]struct{}, len(actual))
	for _, item := range expected {
		expectedSet[item] = struct{}{}
	}
	for _, item := range actual {
		actualSet[item] = struct{}{}
	}
	for item := range expectedSet {
		if _, ok := actualSet[item]; !ok {
			missing = append(missing, item)
		}
	}
	for item := range actualSet {
		if _, ok := expectedSet[item]; !ok {
			extra = append(extra, item)
		}
	}
	sort.Ints(missing)
	sort.Ints(extra)
	return missing, extra
}

func formatStringSliceForLog(items []string) string {
	if len(items) == 0 {
		return "[]"
	}
	return "[" + strings.Join(items, ",") + "]"
}

func dataFingerprintForLog(data []byte) string {
	if len(data) == 0 {
		return "len=0"
	}
	sum := sha256.Sum256(data)
	prefixLen := 8
	if len(data) < prefixLen {
		prefixLen = len(data)
	}
	return fmt.Sprintf("len=%d,prefix=%s,sha256=%s", len(data), hex.EncodeToString(data[:prefixLen]), hex.EncodeToString(sum[:8]))
}

                                             

func splitNonceCommitPayload(payload []byte, signAlgo pb.SignAlgo) ([][]byte, [][]byte, error) {
	pointSize := getPointSize(signAlgo)
	chunks, err := splitFixedPayload(payload, 2*pointSize)
	if err != nil {
		return nil, nil, err
	}

	hiding := make([][]byte, len(chunks))
	binding := make([][]byte, len(chunks))
	for i, chunk := range chunks {
		hiding[i] = chunk[:pointSize]
		binding[i] = chunk[pointSize:]
	}
	return hiding, binding, nil
}

func splitSignatureShares(payload []byte) ([][]byte, error) {
	return splitFixedPayload(payload, 32)
}

func splitFixedPayload(payload []byte, chunkSize int) ([][]byte, error) {
	if chunkSize <= 0 || len(payload) == 0 || len(payload)%chunkSize != 0 {
		return nil, ErrInvalidRoastPayload
	}
	count := len(payload) / chunkSize
	result := make([][]byte, 0, count)
	for i := 0; i < count; i++ {
		start := i * chunkSize
		chunk := make([]byte, chunkSize)
		copy(chunk, payload[start:start+chunkSize])
		result = append(result, chunk)
	}
	return result, nil
}
