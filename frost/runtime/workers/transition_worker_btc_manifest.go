package workers

import (
	"bytes"
	"context"
	"fmt"
	"strconv"

	"dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/keys"
	"dex/pb"
	"google.golang.org/protobuf/proto"
)

const btcMigrationConcurrentChildren = 8
const btcMigrationChildPage = 64

func (w *TransitionWorker) startBTCManifestChildren(ctx context.Context, tr *pb.VaultTransitionState, parent *pb.FrostVaultMigrationState) error {
	ordered, ok := w.stateReader.(interface {
		ScanOrderedAfter(string, string, int, bool, func(string, []byte) bool) error
	})
	if !ok {
		return fmt.Errorf("BTC migration requires ordered child pagination")
	}
	w.mu.Lock()
	if w.migrationChildActive == nil {
		w.migrationChildActive = make(map[string]bool)
		w.migrationChildCursor = make(map[string]string)
	}
	cursor := w.migrationChildCursor[parent.MigrationId]
	w.mu.Unlock()
	ids := []string{}
	last := ""
	prefix := keys.KeyFrostBtcMigrationActiveChildPrefix(parent.MigrationId)
	scan := func() error {
		return ordered.ScanOrderedAfter(prefix, cursor, btcMigrationChildPage, false, func(k string, v []byte) bool { last = k; ids = append(ids, string(v)); return true })
	}
	if err := scan(); err != nil {
		return err
	}
	if len(ids) == 0 && cursor != "" {
		cursor = ""
		if err := scan(); err != nil {
			return err
		}
	}
	w.mu.Lock()
	if len(ids) < btcMigrationChildPage {
		last = ""
	}
	w.migrationChildCursor[parent.MigrationId] = last
	w.mu.Unlock()
	ks := make([]string, len(ids))
	for i, id := range ids {
		ks[i] = keys.KeyFrostBtcMigrationChildState(id)
	}
	values, err := workerGetMany(w.stateReader, ks)
	if err != nil {
		return err
	}
	threshold, err := w.resolveMigrationThreshold(parent.Chain, parent.VaultId, tr)
	if err != nil {
		return err
	}
	for i, id := range ids {
		var child pb.FrostVaultMigrationState
		if len(values[ks[i]]) == 0 || proto.Unmarshal(values[ks[i]], &child) != nil || child.MigrationId != id || child.ParentMigrationId != parent.MigrationId || child.Status != "INTENT_FINALIZED" {
			return fmt.Errorf("invalid BTC active migration child")
		}
		jobID, coordinate, e := w.migrationRoundCoordinatorJob(&child)
		if e != nil {
			return e
		}
		if !coordinate {
			continue
		}
		w.mu.Lock()
		if w.migrationChildActive[id] || len(w.migrationChildActive) >= btcMigrationConcurrentChildren {
			w.mu.Unlock()
			continue
		}
		w.migrationChildActive[id] = true
		w.mu.Unlock()
		childCopy := proto.Clone(&child).(*pb.FrostVaultMigrationState)
		parent := proto.Clone(parent).(*pb.FrostVaultMigrationState)
		tr := proto.Clone(tr).(*pb.VaultTransitionState)
		go func() {
			defer func() { w.mu.Lock(); delete(w.migrationChildActive, id); w.mu.Unlock() }()
			if cached := w.cachedMigrationSignedTx(id); cached != nil {
				if e := w.txSubmitter.SubmitTransitionSignedTx(ctx, cached); e != nil {
					w.Logger.Warn("[TransitionWorker] resubmit BTC child %s: %v", id, e)
				}
				return
			}
			if e := w.signBTCManifestChild(ctx, jobID, tr, parent, childCopy, threshold); e != nil {
				w.Logger.Warn("[TransitionWorker] BTC migration child %s: %v", id, e)
			}
		}()
	}
	return nil
}

func (w *TransitionWorker) signBTCManifestChild(ctx context.Context, jobID string, tr *pb.VaultTransitionState, parent, child *pb.FrostVaultMigrationState, threshold int) error {
	cfg, err := w.loadChainVaultConfig(chain.ChainBTC)
	if err != nil {
		return err
	}
	template, err := btc.FromJSON(child.TemplateData)
	if err != nil || len(template.Inputs) != 1 || !bytes.Equal(template.TemplateHash(), child.TemplateHash) {
		return fmt.Errorf("invalid finalized BTC migration child")
	}
	in := template.Inputs[0]
	ks := []string{keys.KeyFrostBtcUtxo(child.VaultId, in.TxID, in.Vout), keys.KeyFrostBtcLockedUtxo(child.VaultId, in.TxID, in.Vout)}
	values, err := workerGetMany(w.stateReader, ks)
	if err != nil {
		return err
	}
	var u pb.FrostUtxo
	if len(values[ks[0]]) == 0 || proto.Unmarshal(values[ks[0]], &u) != nil || u.Txid != in.TxID || u.Vout != in.Vout || u.Amount != strconv.FormatUint(in.Amount, 10) || !bytes.Equal(u.OriginId, child.BtcOriginId) || string(values[ks[1]]) != child.MigrationId {
		return fmt.Errorf("BTC migration child input changed")
	}
	operator, err := migrationOperatorPubkeys(parent.OldGroupPubkey, []chain.UTXO{{TxID: u.Txid, Vout: u.Vout}}, map[string][]byte{migrationOutpointKey(u.Txid, u.Vout): u.Tweak})
	if err != nil {
		return err
	}
	canonical, t, err := btc.BuildMigrationTemplate(chain.ChainBTC, parent.VaultId, parent.OldEpoch, parent.NewEpoch, []chain.UTXO{{TxID: u.Txid, Vout: u.Vout, Amount: in.Amount, ScriptPubKey: u.ScriptPubkey, ConfirmHeight: u.FinalizeHeight}}, parent.NewGroupPubkey, cfg.BtcNetwork, cfg.BtcFeeSatsPerVbyte, cfg.GuardianPubkey, operator)
	if err != nil || !bytes.Equal(canonical.TemplateData, child.TemplateData) || !bytes.Equal(canonical.TemplateHash, child.TemplateHash) {
		return fmt.Errorf("BTC migration child does not match canonical input")
	}
	messages, err := t.ComputeTaprootSighash([][]byte{u.ScriptPubkey}, btc.SighashDefault)
	if err != nil {
		return err
	}
	messages = append(messages, parent.IntentHash)
	signed, err := w.runMigrationSigningSession(ctx, jobID, child, chain.SignAlgoBTC, threshold, messages, [][]byte{u.Tweak, nil})
	if err != nil {
		return err
	}
	sigs, err := exactMigrationSignatures(signed, 2, 64)
	if err != nil {
		return err
	}
	if len(signed.GuardianSignatures) != 2 {
		return fmt.Errorf("missing BTC migration child guardian signatures")
	}
	raw, err := t.BuildSignedRawTx(sigs[:1], signed.GuardianSignatures[:1])
	if err != nil {
		return err
	}
	                                                                           
	                                                                           
	commit := proto.Clone(child).(*pb.FrostVaultMigrationState)
	commit.IntentHash = append([]byte(nil), parent.IntentHash...)
	return w.submitFinalizedMigration(ctx, tr, commit, sigs[1], child.TemplateHash, child.TemplateData, sigs[:1], [][]byte{u.ScriptPubkey}, raw, signed.GuardianSignatures)
}
