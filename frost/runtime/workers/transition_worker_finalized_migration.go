package workers

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	chainpkg "dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/frost/chain/evm"
	"dex/keys"
	"dex/pb"
	"dex/utils"

	"google.golang.org/protobuf/proto"
)

const migrationCoordinatorRoundBlocks uint64 = 10
const maxCachedMigrationSignedTxs = 32

                                                                            
                                                                             
                                                                          
func (w *TransitionWorker) PlanMigrationJobs(ctx context.Context, chainName string, vaultID uint32, epochID uint64) error {
	chainName = chainpkg.NormalizeChain(chainName)
	transition, intent, err := w.loadFinalizedMigration(chainName, vaultID, epochID)
	if err != nil {
		return err
	}
	if !committeeContains(transition.OldCommitteeMembers, w.localAddress) {
		return nil
	}
	if chainName == chainpkg.ChainBTC && intent.BtcProtocolVersion == 3 && !intent.NoFunds {
		return w.startBTCManifestChildren(ctx, transition, intent)
	}
	if cached := w.cachedMigrationSignedTx(intent.MigrationId); cached != nil {
		if err := w.txSubmitter.SubmitTransitionSignedTx(ctx, cached); err != nil {
			return fmt.Errorf("resubmit finalized migration: %w", err)
		}
		return nil
	}
	jobID, shouldCoordinate, err := w.migrationRoundCoordinatorJob(intent)
	if err != nil || !shouldCoordinate {
		return err
	}
	if w.signingService == nil {
		return fmt.Errorf("migration signing service unavailable")
	}
	threshold, err := w.resolveMigrationThreshold(chainName, vaultID, transition)
	if err != nil {
		return err
	}
	signAlgo := transition.SignAlgo
	if signAlgo == pb.SignAlgo_SIGN_ALGO_UNSPECIFIED {
		if chainName == chainpkg.ChainBTC {
			signAlgo = pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340
		} else {
			signAlgo = pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128
		}
	}

	switch chainName {
	case chainpkg.ChainBTC:
		return w.signFinalizedBTCMigration(ctx, jobID, transition, intent, signAlgo, threshold)
	case chainpkg.ChainETH, chainpkg.ChainBNB, chainpkg.ChainBASE, chainpkg.ChainARB, chainpkg.ChainTRX:
		return w.signFinalizedEVMMigration(ctx, jobID, transition, intent, signAlgo, threshold)
	default:
		return fmt.Errorf("unsupported migration chain %s", chainName)
	}
}

func (w *TransitionWorker) loadFinalizedMigration(chainName string, vaultID uint32, epochID uint64) (*pb.VaultTransitionState, *pb.FrostVaultMigrationState, error) {
	transitionData, exists, err := w.stateReader.Get(keys.KeyFrostVaultTransition(chainName, vaultID, epochID))
	if err != nil || !exists || len(transitionData) == 0 {
		return nil, nil, fmt.Errorf("transition state not found")
	}
	var transition pb.VaultTransitionState
	if err := proto.Unmarshal(transitionData, &transition); err != nil {
		return nil, nil, err
	}
	if transition.DkgStatus != "KEY_READY" || transition.Lifecycle != "DRAINING" || transition.MigrationStatus != "INTENT_FINALIZED" {
		return nil, nil, fmt.Errorf("migration intent is not active")
	}
	hotData, hotExists, err := w.stateReader.Get(keys.KeyFrostVaultMigration(chainName, vaultID, epochID))
	if err != nil || !hotExists || len(hotData) == 0 {
		return nil, nil, fmt.Errorf("migration state not found")
	}
	var hot pb.FrostVaultMigrationState
	if err := proto.Unmarshal(hotData, &hot); err != nil {
		return nil, nil, err
	}
	intent := &hot
	if intent.Status != "INTENT_FINALIZED" || strings.TrimSpace(intent.MigrationId) == "" ||
		transition.MigrationJobId != intent.MigrationId || !bytes.Equal(transition.MigrationTemplateHash, intent.TemplateHash) || !bytes.Equal(transition.MigrationIntentHash, intent.IntentHash) {
		return nil, nil, fmt.Errorf("migration intent does not match transition state")
	}
	return &transition, intent, nil
}

func (w *TransitionWorker) migrationRoundCoordinatorJob(intent *pb.FrostVaultMigrationState) (string, bool, error) {
	if intent == nil {
		return "", false, fmt.Errorf("migration intent is nil")
	}
	if w.vaultProvider == nil || strings.TrimSpace(w.localAddress) == "" {
		return "", false, fmt.Errorf("vault provider and local identity are required for migration coordination")
	}
	committee, err := w.vaultProvider.VaultCommittee(intent.Chain, intent.VaultId, intent.OldEpoch)
	if err != nil {
		return "", false, fmt.Errorf("load old migration committee: %w", err)
	}
	if len(committee) == 0 {
		return "", false, fmt.Errorf("old migration committee is empty")
	}
	height := w.GetCurrentHeight()
	if !migrationCommitteeContains(committee, w.localAddress) {
		return "", false, nil
	}
	round := uint64(0)
	if height > intent.IntentHeight {
		round = (height - intent.IntentHeight) / migrationCoordinatorRoundBlocks
	}
	                                                                          
	                                                                           
	                                                                            
	                                                                       
	jobID := fmt.Sprintf("%s_r%d", intent.MigrationId, round)
	coordinator := computeJobCoordinatorIndex(jobID, intent.OldEpoch, committee)
	return jobID, string(committee[coordinator].ID) == w.localAddress, nil
}

func migrationCommitteeContains(committee []SignerInfo, address string) bool {
	for i := range committee {
		if string(committee[i].ID) == address {
			return true
		}
	}
	return false
}

func (w *TransitionWorker) signFinalizedBTCMigration(ctx context.Context, jobID string, transition *pb.VaultTransitionState, intent *pb.FrostVaultMigrationState, signAlgo pb.SignAlgo, threshold int) error {
	messages := make([][]byte, 0, 1)
	tweaks := make([][]byte, 0, 1)
	var templateData []byte
	var templateHash []byte
	var inputScripts [][]byte
	var inputCount int

	if intent.NoFunds {
		if len(intent.TemplateData) != 0 || !bytes.Equal(intent.TemplateHash, chainpkg.EmptyMigrationTemplateHash()) {
			return fmt.Errorf("invalid empty-vault BTC intent")
		}
	} else {
		cfg, err := w.loadChainVaultConfig(intent.Chain)
		if err != nil {
			return err
		}
		utxos, tweakByOutpoint, scriptByOutpoint, err := w.scanOldKeyMigrationUTXOs(intent)
		if err != nil {
			return err
		}
		inputPubkeys, err := migrationOperatorPubkeys(intent.OldGroupPubkey, utxos, tweakByOutpoint)
		if err != nil {
			return err
		}
		canonical, template, err := btc.BuildMigrationTemplate(intent.Chain, intent.VaultId, intent.OldEpoch, intent.NewEpoch, utxos, intent.NewGroupPubkey, cfg.BtcNetwork, cfg.BtcFeeSatsPerVbyte, cfg.GuardianPubkey, inputPubkeys)
		if err != nil || !bytes.Equal(canonical.TemplateHash, intent.TemplateHash) || !bytes.Equal(canonical.TemplateData, intent.TemplateData) {
			return fmt.Errorf("local BTC state does not reproduce finalized migration intent")
		}
		templateData = canonical.TemplateData
		templateHash = canonical.TemplateHash
		inputCount = len(template.Inputs)
		inputScripts = make([][]byte, inputCount)
		for i, input := range template.Inputs {
			outpoint := migrationOutpointKey(input.TxID, input.Vout)
			inputScripts[i] = append([]byte(nil), scriptByOutpoint[outpoint]...)
			tweaks = append(tweaks, append([]byte(nil), tweakByOutpoint[outpoint]...))
		}
		sighashes, err := template.ComputeTaprootSighash(inputScripts, btc.SighashDefault)
		if err != nil {
			return err
		}
		messages = append(messages, sighashes...)
	}
	messages = append(messages, append([]byte(nil), intent.IntentHash...))
	tweaks = append(tweaks, nil)
	signed, err := w.runMigrationSigningSession(ctx, jobID, intent, signAlgo, threshold, messages, tweaks)
	if err != nil {
		return err
	}
	signatures, err := exactMigrationSignatures(signed, len(messages), 64)
	if err != nil {
		return err
	}
	authorization := signatures[len(signatures)-1]
	inputSigs := cloneMigrationByteSlices(signatures[:inputCount])
	if len(signed.GuardianSignatures) != len(messages) {
		return fmt.Errorf("missing BTC migration guardian signatures")
	}
	var rawTx []byte
	if !intent.NoFunds {
		template, _ := btc.FromJSON(intent.TemplateData)
		rawTx, err = template.BuildSignedRawTx(inputSigs, signed.GuardianSignatures[:inputCount])
		if err != nil {
			return err
		}
	}
	if intent.NoFunds {
		templateHash = append([]byte(nil), intent.TemplateHash...)
	}
	return w.submitFinalizedMigration(ctx, transition, intent, authorization, templateHash, templateData, inputSigs, inputScripts, rawTx, signed.GuardianSignatures)
}

func (w *TransitionWorker) signFinalizedEVMMigration(ctx context.Context, jobID string, transition *pb.VaultTransitionState, intent *pb.FrostVaultMigrationState, signAlgo pb.SignAlgo, threshold int) error {
	cfg, err := w.loadChainVaultConfig(intent.Chain)
	if err != nil {
		return err
	}
	if int(intent.VaultId) >= len(cfg.VaultRefs) {
		return fmt.Errorf("EVM vault contract not configured")
	}
	canonical, _, err := evm.CanonicalMigrationTemplate(cfg, intent)
	if err != nil || !bytes.Equal(canonical.TemplateHash, intent.TemplateHash) || !bytes.Equal(canonical.TemplateData, intent.TemplateData) || len(canonical.SigHashes) != 1 || !bytes.Equal(canonical.SigHashes[0], intent.IntentHash) {
		return fmt.Errorf("local EVM config does not reproduce finalized migration intent")
	}
	signed, err := w.runMigrationSigningSession(ctx, jobID, intent, signAlgo, threshold, [][]byte{intent.IntentHash}, nil)
	if err != nil {
		return err
	}
	signatures, err := exactMigrationSignatures(signed, 1, evm.SchnorrSigWithYSize)
	if err != nil {
		return err
	}
	                                                                       
	                                                                             
	                                                                         
	if len(signed.GuardianSignatures) != 1 {
		return fmt.Errorf("missing account migration guardian signature")
	}
	return w.submitFinalizedMigration(ctx, transition, intent, signatures[0], intent.TemplateHash, intent.TemplateData, nil, nil, nil, signed.GuardianSignatures)
}

func (w *TransitionWorker) runMigrationSigningSession(ctx context.Context, jobID string, intent *pb.FrostVaultMigrationState, signAlgo pb.SignAlgo, threshold int, messages, tweaks [][]byte) (*SignedPackage, error) {
	startCtx := ctx
	if startCtx == nil {
		startCtx = context.Background()
	}
	sessionID, err := w.signingService.StartSigningSession(startCtx, &SigningSessionParams{RequireGuardian: true, JobID: jobID, Chain: intent.Chain, VaultID: intent.VaultId, KeyEpoch: intent.OldEpoch, SignAlgo: signAlgo, Messages: messages, Threshold: threshold, Tweaks: tweaks})
	if err != nil {
		return nil, err
	}
	defer w.signingService.CancelSession(sessionID)
	waitCtx := context.Background()
	if ctx != nil {
		waitCtx = context.WithoutCancel(ctx)
	}
	timeout := w.signTimeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	result, err := w.signingService.WaitForCompletion(waitCtx, sessionID, timeout)
	return result, err
}

func (w *TransitionWorker) submitFinalizedMigration(ctx context.Context, transition *pb.VaultTransitionState, intent *pb.FrostVaultMigrationState, authorization, templateHash, templateData []byte, inputSigs, scripts [][]byte, rawTx []byte, guardianSigs [][]byte) error {
	tx := &pb.FrostVaultTransitionSignedTx{
		GuardianSigs:  cloneMigrationByteSlices(guardianSigs),
		Base:          w.newBaseMessage(),
		Chain:         intent.Chain,
		OldVaultId:    intent.VaultId,
		NewVaultId:    intent.VaultId,
		EpochId:       intent.NewEpoch,
		MsgHash:       append([]byte(nil), intent.IntentHash...),
		Signature:     append([]byte(nil), authorization...),
		MigrationId:   intent.MigrationId,
		TemplateHash:  append([]byte(nil), templateHash...),
		TemplateData:  append([]byte(nil), templateData...),
		InputSigs:     cloneMigrationByteSlices(inputSigs),
		ScriptPubkeys: cloneMigrationByteSlices(scripts),
		RawTx:         append([]byte(nil), rawTx...),
	}
	anyTx := &pb.AnyTx{Content: &pb.AnyTx_FrostVaultTransitionSignedTx{FrostVaultTransitionSignedTx: tx}}
	w.signAndSetTxID(anyTx)
	w.rememberMigrationSignedTx(intent.MigrationId, tx)
	if err := w.txSubmitter.SubmitTransitionSignedTx(ctx, proto.Clone(tx).(*pb.FrostVaultTransitionSignedTx)); err != nil {
		return fmt.Errorf("submit finalized migration: %w", err)
	}
	return nil
}

func (w *TransitionWorker) rememberMigrationSignedTx(migrationID string, tx *pb.FrostVaultTransitionSignedTx) {
	if w == nil || strings.TrimSpace(migrationID) == "" || tx == nil {
		return
	}
	w.mu.Lock()
	if w.migrationSignedTx == nil {
		w.migrationSignedTx = make(map[string]*pb.FrostVaultTransitionSignedTx)
	}
	if w.migrationSignedTx[migrationID] == nil {
		w.migrationSignedTx[migrationID] = proto.Clone(tx).(*pb.FrostVaultTransitionSignedTx)
		w.migrationSignedOrder = append(w.migrationSignedOrder, migrationID)
		for len(w.migrationSignedOrder) > maxCachedMigrationSignedTxs {
			oldest := w.migrationSignedOrder[0]
			w.migrationSignedOrder = w.migrationSignedOrder[1:]
			delete(w.migrationSignedTx, oldest)
		}
	}
	w.mu.Unlock()
}

func (w *TransitionWorker) cachedMigrationSignedTx(migrationID string) *pb.FrostVaultTransitionSignedTx {
	if w == nil {
		return nil
	}
	w.mu.RLock()
	tx := w.migrationSignedTx[migrationID]
	w.mu.RUnlock()
	if tx == nil {
		return nil
	}
	return proto.Clone(tx).(*pb.FrostVaultTransitionSignedTx)
}

func (w *TransitionWorker) scanOldKeyMigrationUTXOs(intent *pb.FrostVaultMigrationState) ([]chainpkg.UTXO, map[string][]byte, map[string][]byte, error) {
	cfg, err := w.loadChainVaultConfig(intent.Chain)
	if err != nil {
		return nil, nil, nil, err
	}
	rows := make([]*pb.FrostUtxo, 0)
	err = w.stateReader.Scan(keys.KeyFrostBtcSpendableUtxoIndexPrefix(intent.VaultId), func(_ string, value []byte) bool {
		var utxo pb.FrostUtxo
		if proto.Unmarshal(value, &utxo) == nil {
			rows = append(rows, &utxo)
		}
		return true
	})
	if err != nil {
		return nil, nil, nil, err
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Txid != rows[j].Txid {
			return rows[i].Txid < rows[j].Txid
		}
		return rows[i].Vout < rows[j].Vout
	})
	out := make([]chainpkg.UTXO, 0, len(rows))
	tweaks := make(map[string][]byte)
	scripts := make(map[string][]byte)
	for i := range rows {
		utxo := rows[i]
		amount, err := strconv.ParseUint(strings.TrimSpace(utxo.Amount), 10, 64)
		if err != nil || amount == 0 {
			return nil, nil, nil, fmt.Errorf("invalid migration UTXO amount")
		}
		oldControlled, err := workerBTCUTXOControlledBy(intent.OldGroupPubkey, utxo, cfg.GuardianPubkey)
		if err != nil {
			return nil, nil, nil, err
		}
		if !oldControlled {
			newControlled, newErr := workerBTCUTXOControlledBy(intent.NewGroupPubkey, utxo, cfg.GuardianPubkey)
			if newErr != nil || !newControlled {
				return nil, nil, nil, fmt.Errorf("migration UTXO is controlled by neither transition key")
			}
			continue
		}
		key := migrationOutpointKey(utxo.Txid, utxo.Vout)
		out = append(out, chainpkg.UTXO{TxID: utxo.Txid, Vout: utxo.Vout, Amount: amount, ScriptPubKey: append([]byte(nil), utxo.ScriptPubkey...), ConfirmHeight: utxo.FinalizeHeight})
		tweaks[key] = append([]byte(nil), utxo.Tweak...)
		scripts[key] = append([]byte(nil), utxo.ScriptPubkey...)
	}
	return out, tweaks, scripts, nil
}

func workerBTCUTXOControlledBy(groupPubkey []byte, utxo *pb.FrostUtxo, guardian []byte) (bool, error) {
	actual := utxo.ScriptPubkey
	var err error
	var expected []byte
	if len(utxo.Tweak) == 0 {
		expected, err = utils.NormalizeSecp256k1XOnlyPubKey(groupPubkey)
	} else if len(utxo.Tweak) == 32 {
		expected, err = utils.ComputeTweakedXOnlyPubkey(groupPubkey, utxo.Tweak)
	} else {
		return false, fmt.Errorf("invalid migration UTXO tweak")
	}
	if err != nil {
		return false, err
	}
	expected, _, _, err = utils.BuildDualCustodyTaproot(expected, guardian)
	return err == nil && bytes.Equal(actual, expected), err
}

func exactMigrationSignatures(pkg *SignedPackage, count, signatureSize int) ([][]byte, error) {
	if pkg == nil || count <= 0 {
		return nil, fmt.Errorf("missing migration signatures")
	}
	if len(pkg.Signatures) == count {
		out := cloneMigrationByteSlices(pkg.Signatures)
		for _, signature := range out {
			if len(signature) != signatureSize {
				return nil, fmt.Errorf("migration signature size mismatch")
			}
		}
		return out, nil
	}
	if len(pkg.Signature) != count*signatureSize {
		return nil, fmt.Errorf("migration signature count mismatch")
	}
	out := make([][]byte, count)
	for i := 0; i < count; i++ {
		out[i] = append([]byte(nil), pkg.Signature[i*signatureSize:(i+1)*signatureSize]...)
	}
	return out, nil
}

func cloneMigrationByteSlices(in [][]byte) [][]byte {
	out := make([][]byte, len(in))
	for i := range in {
		out[i] = append([]byte(nil), in[i]...)
	}
	return out
}

func migrationOperatorPubkeys(group []byte, utxos []chainpkg.UTXO, tweaks map[string][]byte) ([][]byte, error) {
	keys := make([][]byte, len(utxos))
	for i, u := range utxos {
		var err error
		t := tweaks[migrationOutpointKey(u.TxID, u.Vout)]
		if len(t) == 0 {
			keys[i], err = utils.NormalizeSecp256k1XOnlyPubKey(group)
		} else {
			keys[i], err = utils.ComputeTweakedXOnlyPubkey(group, t)
		}
		if err != nil {
			return nil, err
		}
	}
	return keys, nil
}
