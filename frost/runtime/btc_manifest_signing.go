package runtime

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/frost/runtime/roast"
	"dex/keys"
	"dex/pb"
	"dex/utils"
	"google.golang.org/protobuf/proto"
)

func migrationChildSessionBase(session string) (string, error) {
	i := strings.LastIndex(session, "_r")
	if i < 0 {
		return "", fmt.Errorf("migration child round is missing")
	}
	base, roundText := session[:i], session[i+2:]
	round, e := strconv.ParseUint(roundText, 10, 64)
	hash, e2 := hex.DecodeString(strings.TrimPrefix(base, "migration_part_"))
	if e != nil || strconv.FormatUint(round, 10) != roundText || !strings.HasPrefix(base, "migration_part_") || e2 != nil || len(hash) != 32 || hex.EncodeToString(hash) != strings.TrimPrefix(base, "migration_part_") {
		return "", fmt.Errorf("invalid migration child session")
	}
	return base, nil
}

func validateBTCManifestEmptyMessages(env *roast.Envelope, messages [][]byte, parent *pb.FrostVaultMigrationState, cfg *pb.FrostVaultConfig) error {
	if !parent.NoFunds || parent.ParentMigrationId != "" || parent.BtcChildCount != 0 || len(parent.TemplateData) != 0 || !bytes.Equal(parent.TemplateHash, chain.EmptyMigrationTemplateHash()) || cfg.DepositDexChainId == 0 {
		return fmt.Errorf("invalid BTC empty manifest")
	}
	expected := chain.BTCMigrationManifestIntentHash(cfg.DepositDexChainId, cfg.BtcNetwork, parent)
	if !bytes.Equal(parent.IntentHash, expected) || !sameSigningMatrix(messages, [][]byte{expected}) || !sameSigningMatrix(env.Tweaks, [][]byte{nil}) {
		return fmt.Errorf("BTC empty manifest authority mismatch")
	}
	return nil
}

                                                                        
                                                                             
func validateBTCManifestMigrationSigningRequest(reader ChainStateReader, env *roast.Envelope, messages [][]byte) error {
	if chain.NormalizeChain(env.Chain) != chain.ChainBTC || env.Epoch == ^uint64(0) || env.SignAlgo != chain.SignAlgoBTC {
		return fmt.Errorf("invalid BTC child signing context")
	}
	id, err := migrationChildSessionBase(env.SessionID)
	if err != nil {
		return err
	}
	var child, parent pb.FrostVaultMigrationState
	var tr pb.VaultTransitionState
	var cfg pb.FrostVaultConfig
	var vault pb.FrostVaultState
	ks := []string{keys.KeyFrostBtcMigrationChildState(id), keys.KeyFrostVaultMigration(chain.ChainBTC, env.VaultID, env.Epoch+1), keys.KeyFrostVaultTransition(chain.ChainBTC, env.VaultID, env.Epoch+1), keys.KeyFrostVaultConfig(chain.ChainBTC, 0), keys.KeyFrostVaultState(chain.ChainBTC, env.VaultID)}
	values, err := readSigningState(reader, ks)
	if err != nil {
		return err
	}
	for i, m := range []proto.Message{&child, &parent, &tr, &cfg, &vault} {
		if len(values[ks[i]]) == 0 || proto.Unmarshal(values[ks[i]], m) != nil {
			return fmt.Errorf("%w: BTC migration child state unavailable", roast.ErrSigningIntentNotReady)
		}
	}
	if parent.BtcProtocolVersion != 3 || parent.ParentMigrationId != "" || parent.NoFunds || parent.Status != "INTENT_FINALIZED" || parent.MigrationId != chain.MigrationID(chain.ChainBTC, env.VaultID, env.Epoch+1) || parent.VaultId != env.VaultID || parent.OldEpoch != env.Epoch || parent.NewEpoch != env.Epoch+1 || tr.Chain != chain.ChainBTC || tr.VaultId != env.VaultID || tr.EpochId != parent.NewEpoch || tr.DkgStatus != "KEY_READY" || tr.Lifecycle != "DRAINING" || tr.MigrationStatus != "INTENT_FINALIZED" || tr.MigrationJobId != parent.MigrationId || !bytes.Equal(tr.MigrationIntentHash, parent.IntentHash) || !bytes.Equal(tr.MigrationTemplateHash, parent.TemplateHash) || !bytes.Equal(tr.OldGroupPubkey, parent.OldGroupPubkey) || !bytes.Equal(tr.NewGroupPubkey, parent.NewGroupPubkey) || vault.Chain != chain.ChainBTC || vault.VaultId != env.VaultID || vault.KeyEpoch != env.Epoch || vault.SignAlgo != chain.SignAlgoBTC || !bytes.Equal(vault.GroupPubkey, parent.OldGroupPubkey) || !sameMigrationCommittee(vault.CommitteeMembers, tr.OldCommitteeMembers) || !validMigrationCommittee(tr.NewCommitteeMembers) || cfg.Chain != chain.ChainBTC || cfg.SignAlgo != chain.SignAlgoBTC || cfg.DepositDexChainId == 0 || len(cfg.GuardianPubkey) != 32 {
		return fmt.Errorf("BTC migration manifest context mismatch")
	}
	if child.MigrationId != id || child.BtcProtocolVersion != 3 || child.ParentMigrationId != parent.MigrationId || child.Chain != parent.Chain || child.VaultId != parent.VaultId || child.OldEpoch != parent.OldEpoch || child.NewEpoch != parent.NewEpoch || child.Status != "INTENT_FINALIZED" || child.BtcChildSeq == 0 || child.BtcChildSeq > parent.BtcChildCount || len(child.BtcOriginId) != 32 || !bytes.Equal(child.OldGroupPubkey, parent.OldGroupPubkey) || !bytes.Equal(child.NewGroupPubkey, parent.NewGroupPubkey) {
		return fmt.Errorf("BTC migration child scope mismatch")
	}
	if !bytes.Equal(chain.BTCMigrationManifestIntentHash(cfg.DepositDexChainId, cfg.BtcNetwork, &parent), parent.IntentHash) {
		return fmt.Errorf("BTC migration manifest hash mismatch")
	}
	childHash := chain.BTCMigrationChildIntentHash(cfg.DepositDexChainId, parent.MigrationId, child.BtcChildSeq, child.BtcOriginId, child.TemplateHash)
	if !bytes.Equal(childHash, child.IntentHash) || chain.BTCMigrationChildID(parent.MigrationId, child.BtcChildSeq, childHash) != id {
		return fmt.Errorf("BTC migration child identity mismatch")
	}
	coordinator, e := chain.WithdrawCoordinator(env.SessionID, env.Epoch, tr.OldCommitteeMembers)
	if e != nil || coordinator != string(env.From) {
		return fmt.Errorf("BTC migration child coordinator mismatch")
	}
	template, err := btc.FromJSON(child.TemplateData)
	if err != nil || len(template.Inputs) != 1 || !bytes.Equal(template.TemplateHash(), child.TemplateHash) {
		return fmt.Errorf("invalid BTC migration child template")
	}
	in := template.Inputs[0]
	inputKeys := []string{keys.KeyFrostBtcUtxo(env.VaultID, in.TxID, in.Vout), keys.KeyFrostBtcLockedUtxo(env.VaultID, in.TxID, in.Vout), keys.KeyFrostBtcMigrationChild(parent.MigrationId, child.BtcChildSeq), keys.KeyFrostBtcMigrationActiveChild(parent.MigrationId, child.BtcChildSeq)}
	inputs, err := readSigningState(reader, inputKeys)
	if err != nil {
		return err
	}
	var u pb.FrostUtxo
	if len(inputs[inputKeys[0]]) == 0 || proto.Unmarshal(inputs[inputKeys[0]], &u) != nil || u.Txid != in.TxID || u.Vout != in.Vout || u.VaultId != env.VaultID || u.Amount != strconv.FormatUint(in.Amount, 10) || !bytes.Equal(u.OriginId, child.BtcOriginId) || string(inputs[inputKeys[2]]) != id || string(inputs[inputKeys[3]]) != id {
		return fmt.Errorf("BTC migration child membership/input mismatch")
	}
	if lock := string(inputs[inputKeys[1]]); lock != id {
		return fmt.Errorf("BTC migration child input reserved by another job")
	}
	operator, err := utils.NormalizeSecp256k1XOnlyPubKey(parent.OldGroupPubkey)
	if len(u.Tweak) == 32 {
		operator, err = utils.ComputeTweakedXOnlyPubkey(parent.OldGroupPubkey, u.Tweak)
	} else if len(u.Tweak) != 0 {
		return fmt.Errorf("invalid BTC migration input tweak")
	}
	if err != nil {
		return err
	}
	canonical, t, err := btc.BuildMigrationTemplate(chain.ChainBTC, env.VaultID, env.Epoch, env.Epoch+1, []chain.UTXO{{TxID: u.Txid, Vout: u.Vout, Amount: in.Amount, ScriptPubKey: u.ScriptPubkey, ConfirmHeight: u.FinalizeHeight}}, parent.NewGroupPubkey, cfg.BtcNetwork, cfg.BtcFeeSatsPerVbyte, cfg.GuardianPubkey, [][]byte{operator})
	if err != nil || !bytes.Equal(canonical.TemplateHash, child.TemplateHash) || !bytes.Equal(canonical.TemplateData, child.TemplateData) {
		return fmt.Errorf("BTC migration child is not canonical")
	}
	sighashes, err := t.ComputeTaprootSighash([][]byte{u.ScriptPubkey}, btc.SighashDefault)
	if err != nil {
		return err
	}
	expected := append(sighashes, parent.IntentHash)
	if !sameSigningMatrix(expected, messages) || !sameSigningMatrix(env.Tweaks, [][]byte{u.Tweak, nil}) {
		return fmt.Errorf("BTC migration child messages/tweaks differ from manifest")
	}
	return nil
}
