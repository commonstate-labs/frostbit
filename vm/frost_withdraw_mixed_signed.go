package vm

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strconv"

	"dex/frost/artifact"
	"dex/frost/chain"
	"dex/frost/chain/evm"
	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

func (h *FrostWithdrawSignedTxHandler) dryRunMixedAccountSigned(signed *pb.FrostWithdrawSignedTx, sv StateView) ([]WriteOp, *Receipt, error) {
	txID := signed.GetBase().GetTxId()
	fail := func(message string, err error) ([]WriteOp, *Receipt, error) {
		if err == nil {
			err = errors.New(message)
		}
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: message}, err
	}
	chainName := chain.NormalizeChain(signed.Chain)
	if chainName == "" || chainName == chain.ChainBTC || signed.Asset != "" || len(signed.WithdrawIds) == 0 || len(signed.WithdrawIds) > chain.MaxWithdrawalsPerBatch || len(signed.SignedPackageBytes) != evm.SchnorrSigWithYSize || len(signed.GuardianSigs) != 1 || len(signed.GuardianSigs[0]) != evm.SchnorrSigWithYSize || len(signed.TemplateHash) == 0 || len(signed.TemplateData) == 0 {
		return fail("invalid mixed account withdrawal Signed transaction", nil)
	}
	if _, err := evm.ParseBatchSeq(signed.BatchSeq); err != nil {
		return fail("invalid account withdrawal batch_seq", err)
	}
	job, exists, err := readFrostWithdrawJobState(sv, signed.JobId)
	if err != nil || !exists || job.Status != frostWithdrawJobStatusClaimed {
		return fail("withdrawal Claim is missing or not active", err)
	}
	if job.JobId != signed.JobId || chain.NormalizeChain(job.Chain) != chainName || job.Asset != "" || job.VaultId != signed.VaultId || job.KeyEpoch != signed.KeyEpoch || job.BatchSeq != signed.BatchSeq || job.IncludesHandover != signed.IncludesHandover || !sameOrderedWithdrawIDs(job.WithdrawIds, signed.WithdrawIds) || !bytes.Equal(job.TemplateHash, signed.TemplateHash) || !bytes.Equal(job.TemplateData, signed.TemplateData) {
		return fail("Signed transaction does not match finalized Claim", nil)
	}
	active, activeExists, err := sv.Get(keys.KeyFrostWithdrawActiveVaultJob(chainName, signed.VaultId))
	if err != nil || !activeExists || string(active) != signed.JobId {
		return fail("physical Vault active job mismatch", err)
	}

	vaultData, vaultExists, err := sv.Get(keys.KeyFrostVaultState(chainName, signed.VaultId))
	if err != nil || !vaultExists || len(vaultData) == 0 {
		return fail("Vault state not found", err)
	}
	var vault pb.FrostVaultState
	if proto.Unmarshal(vaultData, &vault) != nil || vault.VaultId != signed.VaultId || chain.NormalizeChain(vault.Chain) != chainName || vault.Status != "ACTIVE" || vault.KeyEpoch != signed.KeyEpoch || vault.SignAlgo != pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128 || len(vault.GroupPubkey) != 64 {
		return fail("Vault authority context mismatch", nil)
	}

	nextSeqKey := keys.KeyFrostVaultDEXNextBatchSeq(chainName, signed.VaultId)
	nextSeqRaw, nextSeqExists, err := sv.Get(nextSeqKey)
	if err != nil {
		return fail("failed to read DEX next batch sequence", err)
	}
	expectedSeq := "1"
	if nextSeqExists && len(nextSeqRaw) > 0 {
		expectedSeq = string(nextSeqRaw)
	}
	seq, err := evm.ParseBatchSeq(expectedSeq)
	if err != nil || expectedSeq != signed.BatchSeq {
		return fail("Signed batch sequence is stale, replayed, or exhausted", err)
	}
	nextSeq := new(big.Int).Add(new(big.Int).Set(seq), big.NewInt(1)) // safe-math-ok: 下一行显式拒绝超过 256 位的批次序号
	if nextSeq.BitLen() > 256 {
		return fail("account withdrawal batch sequence overflow", nil)
	}

	stateKeys := make([]string, len(signed.WithdrawIds))
	for i, id := range signed.WithdrawIds {
		stateKeys[i] = keys.KeyFrostWithdraw(id)
	}
	values, err := frostStateGetMany(sv, stateKeys)
	if err != nil {
		return fail("failed to batch-read claimed withdrawals", err)
	}
	withdraws := make([]*FrostWithdrawRequest, len(signed.WithdrawIds))
	totals := make(map[string]*big.Int)
	for i, id := range signed.WithdrawIds {
		withdraw, err := unmarshalWithdrawRequest(values[stateKeys[i]])
		if err != nil || withdraw.WithdrawID != id || chain.NormalizeChain(withdraw.Chain) != chainName || withdraw.VaultID != signed.VaultId || withdraw.Status != WithdrawStatusSigning || withdraw.JobID != signed.JobId || withdraw.LiveSeq == 0 {
			return fail("claimed mixed withdrawal state mismatch", err)
		}
		indexed, found, readErr := sv.Get(keys.KeyFrostWithdrawLiveIndex(chainName, signed.VaultId, withdraw.LiveSeq))
		if readErr != nil || !found || string(indexed) != id {
			return fail("claimed mixed withdrawal FIFO index mismatch", readErr)
		}
		gross, parseErr := chain.ParseWithdrawAmount(withdraw.Amount)
		if parseErr != nil {
			return fail("claimed mixed withdrawal gross amount is invalid", parseErr)
		}
		if totals[withdraw.Asset] == nil {
			totals[withdraw.Asset] = new(big.Int)
		}
		totals[withdraw.Asset].Add(totals[withdraw.Asset], gross)
		if totals[withdraw.Asset].BitLen() > 256 {
			return fail("mixed withdrawal token total overflows uint256", nil)
		}
		withdraws[i] = withdraw
	}
	canonical, firstSeq, err := buildCanonicalAccountTemplate(sv, chainName, "", signed.VaultId, signed.KeyEpoch, signed.WithdrawIds)
	if err != nil || firstSeq != job.FirstSeq || !bytes.Equal(canonical.TemplateHash, signed.TemplateHash) || !bytes.Equal(canonical.TemplateData, signed.TemplateData) {
		return fail("canonical mixed withdrawal template mismatch", err)
	}
	template, err := evm.FromJSON(canonical.TemplateData)
	if err != nil || template.BatchSeq != signed.BatchSeq || !sameStrings(template.RewardTokens, job.RewardTokens) {
		return fail("canonical mixed withdrawal batch fields mismatch", err)
	}
	signingHash, err := evm.SigningHashFromTemplateData(canonical.TemplateData)
	if err != nil {
		return fail("failed to compute mixed withdrawal signing hash", err)
	}
	valid, err := verifySignature(vault.SignAlgo, vault.GroupPubkey, signingHash, signed.SignedPackageBytes)
	if err != nil || !valid {
		return fail("mixed withdrawal signature verification failed", err)
	}

	cfgData, cfgFound, cfgErr := sv.Get(keys.KeyFrostVaultConfig(chainName, 0))
	var guardianCfg pb.FrostVaultConfig
	if cfgErr != nil || !cfgFound || proto.Unmarshal(cfgData, &guardianCfg) != nil || guardianCfg.AuthorityProtocol != 3 || !bytes.Equal(guardianCfg.GuardianPubkey, template.GuardianPubkey) {
		return fail("invalid fixed guardian policy", cfgErr)
	}
	operatorPub, operatorErr := evm.CanonicalBN128PublicKey(vault.GroupPubkey)
	if operatorErr != nil || bytes.Equal(operatorPub, guardianCfg.GuardianPubkey) {
		return fail("operator and guardian keys must be distinct", operatorErr)
	}
	valid, err = verifySignature(vault.SignAlgo, guardianCfg.GuardianPubkey, signingHash, signed.GuardianSigs[0])
	if err != nil || !valid {
		return fail("mixed withdrawal guardian signature verification failed", err)
	}
	consumedKey := keys.KeyFrostVaultHandoverConsumed(chainName, signed.VaultId, signed.KeyEpoch)
	_, consumed, err := sv.Get(consumedKey)
	if err != nil {
		return fail("failed to read handover marker", err)
	}
	requiresHandover := signed.KeyEpoch > 1 && !consumed
	if signed.IncludesHandover != requiresHandover {
		return fail("first successful batch in a new epoch must include handover", nil)
	}
	packaged, err := packageAccountWithdrawal(sv, chainName, signed.VaultId, canonical.TemplateData, signed.SignedPackageBytes, signed.GuardianSigs[0], signed.IncludesHandover)
	if err != nil {
		return fail("failed to package canonical mixed withdrawal calldata", err)
	}

	                                                                          
	assets := make([]string, 0, len(totals))
	reservedKeys := make([]string, 0, len(totals))
	for asset := range totals {
		assets = append(assets, asset)
	}
	sort.Strings(assets)
	for _, asset := range assets {
		reservedKeys = append(reservedKeys, keys.KeyFrostVaultReservedBalance(chainName, asset, signed.VaultId))
	}
	reservedValues, err := frostStateGetMany(sv, reservedKeys)
	if err != nil {
		return fail("failed to batch-read Vault reservations", err)
	}
	reservedNext := make(map[string]*big.Int, len(assets))
	for i, asset := range assets {
		reserved, parseErr := parseStateUint256(reservedValues[reservedKeys[i]])
		if parseErr != nil || reserved.Cmp(totals[asset]) < 0 {
			return fail("Vault token reservation is insufficient", parseErr)
		}
		reservedNext[asset] = new(big.Int).Sub(reserved, totals[asset]) // safe-math-ok: 前面按 uint256 解析并确认 reserved >= totals[asset]
	}

	                                                                
	lotSnapshot := sv.Snapshot()
	hasFundsLots := make(map[string]bool, len(assets))
	for _, asset := range assets {
		consumedAmount, hasLots, consumeErr := h.consumeVaultFundsLots(sv, chainName, asset, signed.VaultId, totals[asset])
		if consumeErr != nil || (hasLots && consumedAmount.Cmp(totals[asset]) < 0) {
			_ = sv.Revert(lotSnapshot)
			return fail("insufficient Vault funds lots for mixed withdrawal", consumeErr)
		}
		hasFundsLots[asset] = hasLots
	}
	if err := sv.Revert(lotSnapshot); err != nil {
		return fail("failed to revert mixed funds lot simulation", err)
	}
	lotHeadOps := make([]WriteOp, 0, len(assets))
	for _, asset := range assets {
		if hasFundsLots[asset] {
			if _, _, consumeErr := h.consumeVaultFundsLots(sv, chainName, asset, signed.VaultId, totals[asset]); consumeErr != nil {
				return fail("failed to consume Vault funds lots for mixed withdrawal", consumeErr)
			}
		}
		head := GetFundsLotHead(sv, chainName, asset, signed.VaultId)
		lotHeadOps = append(lotHeadOps, WriteOp{Key: keys.KeyFrostFundsLotHead(chainName, asset, signed.VaultId), Value: []byte(strconv.FormatUint(head.Height, 10) + "|" + strconv.FormatUint(head.Seq, 10)), Category: "frost_funds_lot_head"})
	}

	job.Status = frostWithdrawJobStatusSigned
	job.SignedTxId = txID
	jobData, err := proto.Marshal(job)
	if err != nil {
		return fail("failed to marshal signed mixed withdrawal job", err)
	}
	pkg := &pb.FrostSignedPackage{JobId: signed.JobId, GuardianSigs: signed.GuardianSigs, SignedPackageBytes: append(append([]byte(nil), signed.SignedPackageBytes...), signed.GuardianSigs[0]...), SubmitHeight: signed.Base.ExecutedHeight, Submitter: signed.Base.FromAddress}
	pkgData, err := proto.Marshal(pkg)
	if err != nil {
		return fail("failed to marshal mixed withdrawal signature", err)
	}

	lastSeq := withdraws[len(withdraws)-1].LiveSeq
	tailData, _, err := sv.Get(keys.KeyFrostWithdrawLiveTail(chainName, signed.VaultId))
	if err != nil {
		return fail("failed to read mixed FIFO tail", err)
	}
	tail, err := parseOptionalUint64(tailData)
	if err != nil || tail < lastSeq {
		return fail("mixed FIFO tail is invalid", err)
	}
	nextHead, err := nextMixedLiveHead(sv, chainName, signed.VaultId, lastSeq+1, tail)
	if err != nil {
		return fail("failed to advance mixed FIFO head", err)
	}
	pendingData, _, err := sv.Get(keys.KeyFrostWithdrawLivePending(chainName, signed.VaultId))
	if err != nil {
		return fail("failed to read mixed FIFO pending count", err)
	}
	pending, err := parseOptionalUint64(pendingData)
	if err != nil || pending < uint64(len(withdraws)) {
		return fail("mixed FIFO pending count underflow", err)
	}

	                                                                            
	                                                                          
	artifactMeta := artifact.Metadata{Kind: artifact.KindWithdraw, Chain: chainName, VaultID: signed.VaultId, Epoch: signed.KeyEpoch, Height: signed.Base.ExecutedHeight, SourceID: signed.JobId}
	if signed.IncludesHandover {
		migrationParent, resolveErr := artifact.ResolveAccountMigrationHead(sv.Get, chainName, signed.VaultId, signed.KeyEpoch)
		if resolveErr != nil || migrationParent == "" {
			return fail("current epoch handover artifact chain is incomplete", resolveErr)
		}
		artifactMeta.ParentIDs = []string{migrationParent}
	}
	record, err := storeFrostArtifact(sv, artifactMeta, packaged.RawTx)
	if err != nil {
		return fail("failed to archive mixed withdrawal artifact", err)
	}

	ops := make([]WriteOp, 0, len(withdraws)*8+len(assets)+12)
	ops = append(ops, lotHeadOps...)
	for _, asset := range assets {
		ops = append(ops, WriteOp{Key: keys.KeyFrostVaultReservedBalance(chainName, asset, signed.VaultId), Value: []byte(reservedNext[asset].String()), Category: "frost_vault_reserved"})
	}
	for _, withdraw := range withdraws {
		withdraw.Status = WithdrawStatusSigned
		withdraw.SignedTxID = txID
		data, marshalErr := marshalWithdrawRequest(withdraw)
		if marshalErr != nil {
			return fail("failed to marshal terminal mixed withdrawal", marshalErr)
		}
		ops = append(ops,
			archiveWriteOp(keys.KeyArchiveFrostWithdraw(withdraw.WithdrawID), data, "archive_frost_withdraw"),
			archiveWriteOp(keys.KeyFrostPermanentWithdraw(withdraw.WithdrawID), data, "frost_permanent_withdraw"),
			archiveWriteOp(keys.KeyArchiveFrostWithdrawDone(chainName, withdraw.Asset, withdraw.LiveSeq, withdraw.WithdrawID), []byte(withdraw.WithdrawID), "archive_frost_withdraw_done"),
			archiveWriteOp(keys.KeyFrostWithdrawArtifact(withdraw.WithdrawID), []byte(record.Metadata.ArtifactID), "archive_frost_withdraw_artifact"),
			archiveWriteOp(keys.KeyFrostPermanentWithdrawArtifact(withdraw.WithdrawID), []byte(record.Metadata.ArtifactID), "frost_permanent_withdrawref"),
			deleteWriteOp(keys.KeyFrostWithdraw(withdraw.WithdrawID), "frost_withdraw_prune"),
			deleteWriteOp(keys.KeyFrostWithdrawLiveIndex(chainName, signed.VaultId, withdraw.LiveSeq), "frost_withdraw_live"),
		)
		ops = append(ops, finalizeFrostWithdrawQueryIndexes(withdraw)...)
	}
	ops = append(ops,
		archiveWriteOp(keys.KeyArchiveFrostWithdrawJob(signed.JobId), jobData, "archive_frost_withdraw_job"),
		archiveWriteOp(keys.KeyFrostPermanentWithdrawJob(signed.JobId), jobData, "frost_permanent_job"),
		archiveWriteOp(keys.KeyArchiveFrostSignedPackage(signed.JobId, 0), pkgData, "archive_frost_signed_pkg"),
		archiveWriteOp(keys.KeyFrostPermanentSignedPackage(signed.JobId, 0), pkgData, "frost_permanent_package"),
		deleteWriteOp(keys.KeyFrostWithdrawJob(signed.JobId), "frost_withdraw_job_prune"),
		deleteWriteOp(keys.KeyFrostWithdrawActiveVaultJob(chainName, signed.VaultId), "frost_withdraw_active_vault"),
		WriteOp{Key: keys.KeyFrostWithdrawLiveHead(chainName, signed.VaultId), Value: []byte(strconv.FormatUint(nextHead, 10)), Category: "frost_withdraw_live"},
		WriteOp{Key: keys.KeyFrostWithdrawLivePending(chainName, signed.VaultId), Value: []byte(strconv.FormatUint(pending-uint64(len(withdraws)), 10)), Category: "frost_withdraw_live"},
		WriteOp{Key: nextSeqKey, Value: []byte(nextSeq.String()), Category: "frost_vault_batch_seq"},
	)
	if template.AuthorityProtocol == 3 {
		ops = append(ops, WriteOp{Key: keys.KeyFrostWithdrawBatch(chainName, signed.VaultId, job.BatchSeq), Value: []byte(job.WithdrawIds[0]), Category: "frost_withdraw_batch"})
	}
	if signed.IncludesHandover {
		ops = append(ops, WriteOp{Key: consumedKey, Value: []byte(signed.JobId), Category: "frost_vault_handover"})
	}
	applyFrostWithdrawTransactionOps(sv, ops)
	return nil, &Receipt{TxID: txID, Status: "SUCCEED", WriteCount: len(ops)}, nil
}

func nextMixedLiveHead(sv StateView, chainName string, vaultID uint32, start, tail uint64) (uint64, error) {
	if start == 0 {
		return 0, fmt.Errorf("mixed FIFO head overflow")
	}
	for cursor := start; cursor <= tail; {
		count := 128
		if remaining := tail - cursor + 1; remaining < uint64(count) {
			count = int(remaining)
		}
		keysBatch := make([]string, count)
		for i := range keysBatch {
			keysBatch[i] = keys.KeyFrostWithdrawLiveIndex(chainName, vaultID, cursor+uint64(i))
		}
		values, err := frostStateGetMany(sv, keysBatch)
		if err != nil {
			return 0, err
		}
		for i, key := range keysBatch {
			if len(values[key]) > 0 {
				return cursor + uint64(i), nil
			}
		}
		cursor += uint64(count)
	}
	if tail == ^uint64(0) {
		return 0, fmt.Errorf("mixed FIFO tail exhausted")
	}
	return tail + 1, nil
}

func sameStrings(left, right []string) bool {
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
