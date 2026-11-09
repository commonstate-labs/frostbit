package runtime

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/frost/chain/evm"
	"dex/frost/runtime/roast"
	"dex/keys"
	"dex/pb"
	"dex/utils"
	"google.golang.org/protobuf/proto"
)

                                                                              
                                                                              
                                                                             
func validateFinalizedMigrationSigningRequest(reader ChainStateReader, env *roast.Envelope, messages [][]byte) error {
	if strings.HasPrefix(env.SessionID, "migration_part_") {
		return validateBTCManifestMigrationSigningRequest(reader, env, messages)
	}
	if env.Epoch == ^uint64(0) {
		return fmt.Errorf("invalid migration epoch")
	}
	name, epoch := chain.NormalizeChain(env.Chain), env.Epoch+1
	id := chain.MigrationID(name, env.VaultID, epoch)
	prefix := id + "_r"
	if !strings.HasPrefix(env.SessionID, prefix) {
		return fmt.Errorf("migration session identity mismatch")
	}
	roundText := strings.TrimPrefix(env.SessionID, prefix)
	round, err := strconv.ParseUint(roundText, 10, 64)
	if err != nil || strconv.FormatUint(round, 10) != roundText {
		return fmt.Errorf("invalid migration coordinator round")
	}
	var tr pb.VaultTransitionState
	var intent pb.FrostVaultMigrationState
	var vault pb.FrostVaultState
	var cfg pb.FrostVaultConfig
	for _, record := range []struct {
		key   string
		value proto.Message
	}{
		{keys.KeyFrostVaultTransition(name, env.VaultID, epoch), &tr},
		{keys.KeyFrostVaultMigration(name, env.VaultID, epoch), &intent},
		{keys.KeyFrostVaultState(name, env.VaultID), &vault},
		{keys.KeyFrostVaultConfig(name, 0), &cfg},
	} {
		data, found, err := reader.Get(record.key)
		if err != nil || !found || len(data) == 0 {
			return fmt.Errorf("%w: migration state unavailable", roast.ErrSigningIntentNotReady)
		}
		if err := proto.Unmarshal(data, record.value); err != nil {
			return fmt.Errorf("invalid migration state: %w", err)
		}
	}
	algo := chain.SignAlgoETH
	if name == chain.ChainBTC {
		algo = chain.SignAlgoBTC
	}
	if env.SignAlgo != algo || vault.SignAlgo != algo || cfg.SignAlgo != algo || (tr.SignAlgo != algo && tr.SignAlgo != pb.SignAlgo_SIGN_ALGO_UNSPECIFIED) {
		return fmt.Errorf("migration signature algorithm mismatch")
	}
	if chain.NormalizeChain(tr.Chain) != name || tr.VaultId != env.VaultID || tr.EpochId != epoch || tr.DkgStatus != "KEY_READY" || tr.Lifecycle != "DRAINING" || tr.MigrationStatus != "INTENT_FINALIZED" ||
		chain.NormalizeChain(intent.Chain) != name || intent.VaultId != env.VaultID || intent.OldEpoch != env.Epoch || intent.NewEpoch != epoch || intent.Status != "INTENT_FINALIZED" || intent.MigrationId != id || tr.MigrationJobId != id ||
		!bytes.Equal(tr.MigrationTemplateHash, intent.TemplateHash) || !bytes.Equal(tr.MigrationIntentHash, intent.IntentHash) || len(intent.IntentHash) != 32 ||
		!bytes.Equal(tr.OldGroupPubkey, intent.OldGroupPubkey) || !bytes.Equal(tr.NewGroupPubkey, intent.NewGroupPubkey) ||
		chain.NormalizeChain(vault.Chain) != name || vault.VaultId != env.VaultID || vault.KeyEpoch != env.Epoch || !bytes.Equal(vault.GroupPubkey, intent.OldGroupPubkey) ||
		chain.NormalizeChain(cfg.Chain) != name || !sameMigrationCommittee(vault.CommitteeMembers, tr.OldCommitteeMembers) || !validMigrationCommittee(tr.NewCommitteeMembers) {
		return fmt.Errorf("migration context differs from finalized intent")
	}
	coordinator, err := chain.WithdrawCoordinator(env.SessionID, env.Epoch, tr.OldCommitteeMembers)
	if err != nil || string(env.From) != coordinator {
		return fmt.Errorf("migration request is not from round coordinator")
	}
	if name == chain.ChainBTC {
		if intent.BtcProtocolVersion == 3 {
			return validateBTCManifestEmptyMessages(env, messages, &intent, &cfg)
		}
		return validateBTCMigrationMessages(reader, env, messages, &intent, &cfg)
	}
	if len(env.Tweaks) != 0 || intent.NoFunds || int(env.VaultID) >= len(cfg.VaultRefs) {
		return fmt.Errorf("invalid account migration signing context")
	}
	if _, err := evm.CanonicalBN128PublicKey(intent.OldGroupPubkey); err != nil {
		return err
	}
	canonical, _, err := evm.CanonicalMigrationTemplate(&cfg, &intent)
	if err != nil || !bytes.Equal(canonical.TemplateHash, intent.TemplateHash) || !bytes.Equal(canonical.TemplateData, intent.TemplateData) || !sameSigningMatrix(canonical.SigHashes, [][]byte{intent.IntentHash}) || !sameSigningMatrix(canonical.SigHashes, messages) {
		return fmt.Errorf("account migration messages differ from locally rebuilt finalized intent")
	}
	return nil
}

func validMigrationCommittee(members []string) bool {
	if len(members) == 0 {
		return false
	}
	seen := make(map[string]bool, len(members))
	for _, member := range members {
		if strings.TrimSpace(member) == "" || seen[member] {
			return false
		}
		seen[member] = true
	}
	return true
}

func sameMigrationCommittee(a, b []string) bool {
	if !validMigrationCommittee(a) || !validMigrationCommittee(b) || len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func migrationUTXOControlledBy(key []byte, u *pb.FrostUtxo, guardian []byte) (bool, error) {
	if u == nil {
		return false, fmt.Errorf("nil migration UTXO")
	}
	operator, err := utils.NormalizeSecp256k1XOnlyPubKey(key)
	if len(u.Tweak) == 32 {
		operator, err = utils.ComputeTweakedXOnlyPubkey(key, u.Tweak)
	} else if len(u.Tweak) != 0 {
		return false, fmt.Errorf("invalid migration UTXO tweak")
	}
	if err != nil {
		return false, err
	}
	script, _, _, err := utils.BuildDualCustodyTaproot(operator, guardian)
	return err == nil && bytes.Equal(script, u.ScriptPubkey), err
}

func validateBTCMigrationMessages(reader ChainStateReader, env *roast.Envelope, messages [][]byte, intent *pb.FrostVaultMigrationState, cfg *pb.FrostVaultConfig) error {
	if len(cfg.GuardianPubkey) != 32 {
		return fmt.Errorf("BTC guardian is missing")
	}
	if _, err := utils.NormalizeSecp256k1XOnlyPubKey(intent.OldGroupPubkey); err != nil {
		return err
	}
	if _, err := utils.NormalizeSecp256k1XOnlyPubKey(intent.NewGroupPubkey); err != nil {
		return err
	}
	type row struct {
		u   *pb.FrostUtxo
		raw []byte
	}
	rows := []row{}
	stateKeys := []string{}
	var scanErr error
	err := reader.Scan(keys.KeyFrostBtcSpendableUtxoIndexPrefix(env.VaultID), func(_ string, data []byte) bool {
		var u pb.FrostUtxo
		if e := proto.Unmarshal(data, &u); e != nil {
			scanErr = e
			return false
		}
		amount, e := strconv.ParseUint(u.Amount, 10, 64)
		if e != nil || amount == 0 || u.VaultId != env.VaultID {
			scanErr = fmt.Errorf("invalid migration UTXO")
			return false
		}
		rows = append(rows, row{&u, append([]byte(nil), data...)})
		stateKeys = append(stateKeys, keys.KeyFrostBtcUtxo(env.VaultID, u.Txid, u.Vout), keys.KeyFrostBtcLockedUtxo(env.VaultID, u.Txid, u.Vout))
		return true
	})
	if err != nil {
		return err
	}
	if scanErr != nil {
		return scanErr
	}
	state, err := readSigningState(reader, stateKeys)
	if err != nil {
		return err
	}
	utxos := []chain.UTXO{}
	inputKeys := [][]byte{}
	byOutpoint := make(map[string]*pb.FrostUtxo, len(rows))
	for _, r := range rows {
		u := r.u
		amount, _ := strconv.ParseUint(u.Amount, 10, 64)
		if !bytes.Equal(state[keys.KeyFrostBtcUtxo(env.VaultID, u.Txid, u.Vout)], r.raw) {
			return fmt.Errorf("migration UTXO index differs from primary state")
		}
		if len(state[keys.KeyFrostBtcLockedUtxo(env.VaultID, u.Txid, u.Vout)]) != 0 {
			return fmt.Errorf("migration UTXO is already reserved")
		}
		old, err := migrationUTXOControlledBy(intent.OldGroupPubkey, u, cfg.GuardianPubkey)
		if err != nil {
			return err
		}
		if !old {
			next, err := migrationUTXOControlledBy(intent.NewGroupPubkey, u, cfg.GuardianPubkey)
			if err != nil || !next {
				return fmt.Errorf("migration UTXO controlled by neither key")
			}
			continue
		}
		outpoint := fmt.Sprintf("%s:%d", u.Txid, u.Vout)
		if byOutpoint[outpoint] != nil {
			return fmt.Errorf("duplicate migration UTXO")
		}
		byOutpoint[outpoint] = u
		op, err := utils.NormalizeSecp256k1XOnlyPubKey(intent.OldGroupPubkey)
		if len(u.Tweak) == 32 {
			op, err = utils.ComputeTweakedXOnlyPubkey(intent.OldGroupPubkey, u.Tweak)
		}
		if err != nil {
			return err
		}
		inputKeys = append(inputKeys, op)
		utxos = append(utxos, chain.UTXO{TxID: u.Txid, Vout: u.Vout, Amount: amount, ScriptPubKey: u.ScriptPubkey, ConfirmHeight: u.FinalizeHeight})
	}
	expectedMessages, expectedTweaks := [][]byte{}, [][]byte{}
	if intent.NoFunds {
		if len(utxos) != 0 || len(intent.TemplateData) != 0 || !bytes.Equal(intent.TemplateHash, chain.EmptyMigrationTemplateHash()) {
			return fmt.Errorf("BTC empty migration does not match local state")
		}
	} else {
		canonical, template, err := btc.BuildMigrationTemplate(intent.Chain, env.VaultID, intent.OldEpoch, intent.NewEpoch, utxos, intent.NewGroupPubkey, cfg.BtcNetwork, cfg.BtcFeeSatsPerVbyte, cfg.GuardianPubkey, inputKeys)
		if err != nil || !bytes.Equal(canonical.TemplateHash, intent.TemplateHash) || !bytes.Equal(canonical.TemplateData, intent.TemplateData) {
			return fmt.Errorf("BTC migration differs from local canonical sweep")
		}
		scripts := make([][]byte, len(template.Inputs))
		for i, in := range template.Inputs {
			u := byOutpoint[fmt.Sprintf("%s:%d", in.TxID, in.Vout)]
			scripts[i] = u.ScriptPubkey
			expectedTweaks = append(expectedTweaks, u.Tweak)
		}
		expectedMessages, err = template.ComputeTaprootSighash(scripts, btc.SighashDefault)
		if err != nil {
			return err
		}
	}
	authorization := chain.MigrationIntentHash(intent.Chain, env.VaultID, intent.OldEpoch, intent.NewEpoch, intent.OldGroupPubkey, intent.NewGroupPubkey, intent.TemplateHash, intent.NoFunds)
	if !bytes.Equal(authorization, intent.IntentHash) {
		return fmt.Errorf("BTC migration authority hash mismatch")
	}
	expectedMessages = append(expectedMessages, authorization)
	expectedTweaks = append(expectedTweaks, nil)
	if !sameSigningMatrix(expectedMessages, messages) || !sameSigningMatrix(expectedTweaks, env.Tweaks) {
		return fmt.Errorf("BTC migration signing messages/tweaks differ from finalized intent")
	}
	return nil
}
