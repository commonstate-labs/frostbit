package vm

import (
	"bytes"
	"fmt"
	"strconv"

	"dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/keys"
	"dex/pb"
	"dex/protocol"

	"google.golang.org/protobuf/proto"
)

const migrationStatusBuildingManifest = "BUILDING_MANIFEST"

func rejectBTCRechargeDuringFrozenMigration(sv StateView, vaultID uint32) error {
	raw, found, err := sv.Get(keys.KeyFrostVaultState(chain.ChainBTC, vaultID))
	if err != nil {
		return err
	}
	var vault pb.FrostVaultState
	if !found || proto.Unmarshal(raw, &vault) != nil {
		return fmt.Errorf("BTC recharge vault state unavailable")
	}
	if vault.KeyEpoch == ^uint64(0) {
		return fmt.Errorf("BTC vault epoch exhausted")
	}
	raw, found, err = sv.Get(keys.KeyFrostVaultMigration(chain.ChainBTC, vaultID, vault.KeyEpoch+1))
	if err != nil && !isStateViewNotFound(err) {
		return err
	}
	if !found {
		return nil
	}
	var migration pb.FrostVaultMigrationState
	if proto.Unmarshal(raw, &migration) != nil {
		return fmt.Errorf("invalid BTC migration state")
	}
	return fmt.Errorf("BTC vault migration working set is frozen")
}

func newBTCIsolationMigration(sv StateView, transition *pb.VaultTransitionState, height uint64) (*pb.FrostVaultMigrationState, error) {
	if transition.EpochId == 0 {
		return nil, fmt.Errorf("invalid BTC migration epoch")
	}
	raw, found, err := sv.Get(keys.KeyFrostVaultState(chain.ChainBTC, transition.VaultId))
	if err != nil {
		return nil, err
	}
	var vault pb.FrostVaultState
	if !found || proto.Unmarshal(raw, &vault) != nil || vault.KeyEpoch+1 != transition.EpochId ||
		!frostVaultGroupPubkeysEqual(chain.ChainBTC, vault.GroupPubkey, transition.OldGroupPubkey) || len(transition.NewGroupPubkey) == 0 {
		return nil, fmt.Errorf("BTC migration old/new vault context mismatch")
	}
	cfg, err := loadFrostVaultConfig(sv, chain.ChainBTC)
	if err != nil {
		return nil, err
	}
	if networkChainIDFromView(sv) == 0 || cfg.DepositDexChainId != networkChainIDFromView(sv) {
		return nil, fmt.Errorf("BTC migration DEX network mismatch")
	}
	return &pb.FrostVaultMigrationState{
		MigrationId: chain.MigrationID(chain.ChainBTC, transition.VaultId, transition.EpochId), Chain: chain.ChainBTC,
		VaultId: transition.VaultId, OldEpoch: vault.KeyEpoch, NewEpoch: transition.EpochId,
		OldGroupPubkey: append([]byte(nil), transition.OldGroupPubkey...), NewGroupPubkey: append([]byte(nil), transition.NewGroupPubkey...),
		Status: migrationStatusBuildingManifest, IntentHeight: height, BtcProtocolVersion: protocol.BTCIsolationVersion,
		AuthorityProtocol: cfg.AuthorityProtocol, BtcBuildCursor: keys.KeyFrostBtcUtxoPrefix(transition.VaultId),
		BtcManifestAccumulator: chain.BTCMigrationManifestAccumulate(nil, nil),
		BtcTotalInputAmount:    "0", BtcTotalOutputAmount: "0", BtcTotalFeeAmount: "0",
	}, nil
}

                                                                          
                                                                           
                                                                              
func advanceBTCIsolationMigrationManifest(sv StateView, transitionKey string, transition *pb.VaultTransitionState, parent *pb.FrostVaultMigrationState) (resultErr error) {
	if parent.BtcProtocolVersion != 3 || parent.Status != migrationStatusBuildingManifest || parent.ParentMigrationId != "" {
		return fmt.Errorf("invalid BTC manifest build state")
	}
	if _, ok := sv.(orderedStateScannerAfter); !ok {
		return fmt.Errorf("BTC manifest requires ordered cursor scanning")
	}
	cfg, err := loadFrostVaultConfig(sv, chain.ChainBTC)
	if err != nil {
		return err
	}
	network := networkChainIDFromView(sv)
	if network == 0 || cfg.DepositDexChainId != network {
		return fmt.Errorf("BTC migration DEX network mismatch")
	}
	rows, err := scanStateViewOrderedAfter(sv, keys.KeyFrostBtcUtxoPrefix(parent.VaultId), parent.BtcBuildCursor, protocol.BTCIsolationIndexPageSize, false)
	if err != nil {
		return err
	}
	inputs := make([]*pb.FrostUtxo, 0, len(rows))
	readKeys := make([]string, 0, 2*len(rows))
	for _, row := range rows {
		utxo := new(pb.FrostUtxo)
		if proto.Unmarshal(row.Value, utxo) != nil || row.Key != keys.KeyFrostBtcUtxo(parent.VaultId, utxo.Txid, utxo.Vout) || utxo.VaultId != parent.VaultId {
			return fmt.Errorf("invalid BTC manifest input")
		}
		inputs = append(inputs, utxo)
		readKeys = append(readKeys, keys.KeyFrostBtcLockedUtxo(parent.VaultId, utxo.Txid, utxo.Vout), keys.KeyFrostBtcUtxoSeqRef(parent.VaultId, utxo.Txid, utxo.Vout))
	}
	values, err := getManyFrostState(sv, readKeys)
	if err != nil {
		return err
	}
	totalIn, err := parseBTCIsolationAmount("manifest total input", parent.BtcTotalInputAmount)
	if err != nil {
		return err
	}
	totalOut, err := parseBTCIsolationAmount("manifest total output", parent.BtcTotalOutputAmount)
	if err != nil {
		return err
	}
	totalFee, err := parseBTCIsolationAmount("manifest total fee", parent.BtcTotalFeeAmount)
	if err != nil {
		return err
	}
	snap := sv.Snapshot()
	defer func() {
		if resultErr != nil {
			_ = sv.Revert(snap)
		}
	}()
	for _, utxo := range inputs {
		controlled, err := btcUTXOControlledBy(parent.OldGroupPubkey, utxo, cfg.GuardianPubkey)
		if err != nil {
			return err
		}
		if !controlled {
			newControlled, err := btcUTXOControlledBy(parent.NewGroupPubkey, utxo, cfg.GuardianPubkey)
			if err != nil || !newControlled {
				return fmt.Errorf("BTC manifest input is controlled by neither transition key")
			}
			continue
		}
		if len(values[keys.KeyFrostBtcLockedUtxo(parent.VaultId, utxo.Txid, utxo.Vout)]) != 0 {
			return fmt.Errorf("BTC manifest encountered a locked old-key input")
		}
		amount, err := strconv.ParseUint(utxo.Amount, 10, 64)
		if err != nil || amount == 0 {
			return fmt.Errorf("invalid BTC manifest input amount")
		}
		utxo.Amount = strconv.FormatUint(amount, 10)
		if len(utxo.OriginId) == 0 {
			utxo.OriginId, utxo.OriginLegacy = chain.BTCOriginID(network, cfg.BtcNetwork, utxo.Txid, utxo.Vout, true), true
		} else if len(utxo.OriginId) != 32 {
			return fmt.Errorf("invalid BTC manifest input origin")
		}
		operator, err := btcInputOperatorPubkey(parent.OldGroupPubkey, utxo.Tweak)
		if err != nil {
			return err
		}
		input := chain.UTXO{TxID: utxo.Txid, Vout: utxo.Vout, Amount: amount, ScriptPubKey: utxo.ScriptPubkey, ConfirmHeight: utxo.FinalizeHeight}
		result, template, err := btc.BuildMigrationTemplate(chain.ChainBTC, parent.VaultId, parent.OldEpoch, parent.NewEpoch,
			[]chain.UTXO{input}, parent.NewGroupPubkey, cfg.BtcNetwork, cfg.BtcFeeSatsPerVbyte, cfg.GuardianPubkey, [][]byte{operator})
		if err != nil {
			return err
		}
		if parent.BtcChildCount == ^uint64(0) {
			return fmt.Errorf("BTC migration child sequence exhausted")
		}
		seq := parent.BtcChildCount + 1
		childHash := chain.BTCMigrationChildIntentHash(network, parent.MigrationId, seq, utxo.OriginId, result.TemplateHash)
		childID := chain.BTCMigrationChildID(parent.MigrationId, seq, childHash)
		child := &pb.FrostVaultMigrationState{MigrationId: childID, Chain: chain.ChainBTC, VaultId: parent.VaultId,
			OldEpoch: parent.OldEpoch, NewEpoch: parent.NewEpoch, OldGroupPubkey: parent.OldGroupPubkey, NewGroupPubkey: parent.NewGroupPubkey,
			TemplateHash: result.TemplateHash, TemplateData: result.TemplateData, IntentHash: childHash, Status: migrationStatusIntentFinalized,
			IntentHeight: parent.IntentHeight, BtcProtocolVersion: 3, ParentMigrationId: parent.MigrationId, BtcChildSeq: seq,
			BtcOriginId: append([]byte(nil), utxo.OriginId...), AuthorityProtocol: parent.AuthorityProtocol}
		out := template.Outputs[0].Amount
		fee := amount - out
		child.BtcTotalInputAmount, child.BtcTotalOutputAmount, child.BtcTotalFeeAmount = strconv.FormatUint(amount, 10), strconv.FormatUint(out, 10), strconv.FormatUint(fee, 10)
		totalIn, err = addUint64(totalIn, amount)
		if err != nil {
			return err
		}
		totalOut, err = addUint64(totalOut, out)
		if err != nil {
			return err
		}
		totalFee, err = addUint64(totalFee, fee)
		if err != nil {
			return err
		}
		seqRaw := values[keys.KeyFrostBtcUtxoSeqRef(parent.VaultId, utxo.Txid, utxo.Vout)]
		inputSeq, err := strconv.ParseUint(string(seqRaw), 10, 64)
		if err != nil || inputSeq == 0 {
			return fmt.Errorf("BTC manifest input spendable sequence missing")
		}
		data, err := proto.Marshal(child)
		if err != nil {
			return err
		}
		utxoData, err := proto.Marshal(utxo)
		if err != nil {
			return err
		}
		sv.Set(keys.KeyFrostBtcUtxo(parent.VaultId, utxo.Txid, utxo.Vout), utxoData)
		sv.Set(keys.KeyFrostBtcMigrationChildState(childID), data)
		sv.Set(keys.KeyFrostBtcMigrationChild(parent.MigrationId, seq), []byte(childID))
		sv.Set(keys.KeyFrostBtcMigrationActiveChild(parent.MigrationId, seq), []byte(childID))
		sv.Set(keys.KeyFrostMigrationStateRef(childID), []byte(keys.KeyFrostBtcMigrationChildState(childID)))
		sv.Set(keys.KeyFrostBtcLockedUtxo(parent.VaultId, utxo.Txid, utxo.Vout), []byte(childID))
		sv.Del(keys.KeyFrostBtcUtxoAmount(parent.VaultId, amount, utxo.Txid, utxo.Vout))
		sv.Del(keys.KeyFrostBtcSpendableUtxoIndex(parent.VaultId, inputSeq))
		sv.Del(keys.KeyFrostBtcUtxoFIFOIndex(parent.VaultId, inputSeq))
		sv.Del(keys.KeyFrostBtcUtxoSeqRef(parent.VaultId, utxo.Txid, utxo.Vout))
		parent.BtcChildCount = seq
		parent.BtcManifestAccumulator = chain.BTCMigrationManifestAccumulate(parent.BtcManifestAccumulator, childHash)
	}
	if len(rows) > 0 {
		parent.BtcBuildCursor = rows[len(rows)-1].Key
	}
	parent.BtcTotalInputAmount, parent.BtcTotalOutputAmount, parent.BtcTotalFeeAmount = strconv.FormatUint(totalIn, 10), strconv.FormatUint(totalOut, 10), strconv.FormatUint(totalFee, 10)
	if len(rows) < protocol.BTCIsolationIndexPageSize {
		parent.Status, parent.NoFunds = migrationStatusIntentFinalized, parent.BtcChildCount == 0
		parent.BtcManifestHash = append([]byte(nil), parent.BtcManifestAccumulator...)
		parent.TemplateHash = append([]byte(nil), parent.BtcManifestHash...)
		if parent.NoFunds {
			parent.TemplateHash = chain.EmptyMigrationTemplateHash()
		}
		parent.IntentHash = chain.BTCMigrationManifestIntentHash(network, cfg.BtcNetwork, parent)
		permanent, err := proto.Marshal(&pb.FrostVaultMigrationArchive{State: proto.Clone(parent).(*pb.FrostVaultMigrationState)})
		if err != nil {
			return err
		}
		sv.Set(keys.KeyFrostPermanentMigration(parent.MigrationId), permanent)
	}
	data, err := proto.Marshal(parent)
	if err != nil {
		return err
	}
	migrationKey := keys.KeyFrostVaultMigration(chain.ChainBTC, parent.VaultId, parent.NewEpoch)
	sv.Set(migrationKey, data)
	sv.Set(keys.KeyFrostMigrationStateRef(parent.MigrationId), []byte(migrationKey))
	transition.MigrationJobId, transition.MigrationStatus, transition.MigrationError = parent.MigrationId, parent.Status, ""
	transition.MigrationTemplateHash, transition.MigrationIntentHash = append([]byte(nil), parent.TemplateHash...), append([]byte(nil), parent.IntentHash...)
	return setTransitionStateWithIndex(sv, transitionKey, transition)
}

func validateBTCIsolationManifestChild(parent, child *pb.FrostVaultMigrationState, req *pb.FrostVaultTransitionSignedTx) error {
	if parent == nil || child == nil || child.BtcProtocolVersion != 3 || parent.BtcProtocolVersion != 3 ||
		parent.Chain != chain.ChainBTC || child.Chain != chain.ChainBTC || chain.NormalizeChain(req.Chain) != chain.ChainBTC ||
		parent.MigrationId != chain.MigrationID(chain.ChainBTC, parent.VaultId, parent.NewEpoch) || req.OldVaultId != parent.VaultId || req.NewVaultId != parent.VaultId || req.EpochId != parent.NewEpoch ||
		parent.Status != migrationStatusIntentFinalized || child.ParentMigrationId != parent.MigrationId ||
		child.BtcChildSeq == 0 || child.BtcChildSeq > parent.BtcChildCount || child.MigrationId != req.MigrationId ||
		child.VaultId != parent.VaultId || child.OldEpoch != parent.OldEpoch || child.NewEpoch != parent.NewEpoch ||
		!bytes.Equal(child.OldGroupPubkey, parent.OldGroupPubkey) || !bytes.Equal(child.NewGroupPubkey, parent.NewGroupPubkey) ||
		!bytes.Equal(req.MsgHash, parent.IntentHash) || !bytes.Equal(req.TemplateHash, child.TemplateHash) || !bytes.Equal(req.TemplateData, child.TemplateData) {
		return fmt.Errorf("BTC migration child differs from sealed manifest intent")
	}
	return nil
}
