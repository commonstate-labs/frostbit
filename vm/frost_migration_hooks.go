package vm

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"dex/frost/artifact"
	chainpkg "dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/frost/chain/evm"
	"dex/keys"
	"dex/pb"
	"dex/utils"

	"google.golang.org/protobuf/proto"
)

const (
	migrationStatusWaitingWithdraw = "WAITING_WITHDRAW"
	migrationStatusWaitingRecharge = "WAITING_RECHARGE"
	migrationStatusIntentBlocked   = "INTENT_BLOCKED"
	migrationStatusIntentFinalized = "INTENT_FINALIZED"
	migrationStatusSigned          = "SIGNED"
)

                                                                       
                                                                                
                                                     
func (x *Executor) processFrostMigrationIntents(b *pb.Block, sv StateView) error {
	if b == nil || b.Header == nil || sv == nil {
		return nil
	}
	if err := processBTCIsolationAmountIndex(sv); err != nil {
		return err
	}
	active, err := sv.Scan(keys.KeyFrostVaultTransitionActivePrefix())
	if err != nil {
		return fmt.Errorf("scan active vault transitions: %w", err)
	}
	activeKeys := make([]string, 0, len(active))
	for key := range active {
		activeKeys = append(activeKeys, key)
	}
	sort.Strings(activeKeys)
	for _, activeKey := range activeKeys {
		transitionKey := string(active[activeKey])
		transitionData, exists, err := sv.Get(transitionKey)
		if err != nil || !exists || len(transitionData) == 0 {
			return fmt.Errorf("active transition index %s points to missing state", activeKey)
		}
		var transition pb.VaultTransitionState
		if err := unmarshalProtoCompat(transitionData, &transition); err != nil {
			return fmt.Errorf("parse transition %s: %w", transitionKey, err)
		}
		if transition.DkgStatus != DKGStatusKeyReady || transition.Lifecycle != VaultLifecycleDraining || len(transition.OldGroupPubkey) == 0 {
			continue
		}
		migrationKey := keys.KeyFrostVaultMigration(transition.Chain, transition.VaultId, transition.EpochId)
		if data, found, readErr := sv.Get(migrationKey); readErr != nil {
			return readErr
		} else if found && len(data) > 0 {
			var existing pb.FrostVaultMigrationState
			if unmarshalProtoCompat(data, &existing) != nil {
				return fmt.Errorf("invalid existing migration state")
			}
			if existing.BtcProtocolVersion == 3 && existing.Status == migrationStatusBuildingManifest {
				if err := advanceBTCIsolationMigrationManifest(sv, transitionKey, &transition, &existing); err != nil {
					transition.MigrationStatus, transition.MigrationError = migrationStatusIntentBlocked, err.Error()
					if err := setTransitionStateWithIndex(sv, transitionKey, &transition); err != nil {
						return err
					}
				}
			}
			continue
		}

		waiting, err := hasClaimedOldEpochWithdraw(sv, &transition)
		if err != nil {
			return err
		}
		if waiting {
			if transition.MigrationStatus != migrationStatusWaitingWithdraw {
				transition.MigrationStatus = migrationStatusWaitingWithdraw
				transition.MigrationError = ""
				if err := setTransitionStateWithIndex(sv, transitionKey, &transition); err != nil {
					return err
				}
			}
			continue
		}

		                                                                       
		                                                                        
		                                                            
		if chainpkg.NormalizeChain(transition.Chain) == chainpkg.ChainBTC {
			pendingID, err := findPendingRechargeRequestForVault(sv, transition.Chain, transition.VaultId)
			if err != nil {
				return err
			}
			if pendingID != "" {
				if transition.MigrationStatus != migrationStatusWaitingRecharge {
					transition.MigrationStatus = migrationStatusWaitingRecharge
					transition.MigrationError = ""
					if err := setTransitionStateWithIndex(sv, transitionKey, &transition); err != nil {
						return err
					}
				}
				continue
			}
		}

		if chainpkg.NormalizeChain(transition.Chain) == chainpkg.ChainBTC {
			active, err := chainpkg.BTCIsolationActive(sv.Get)
			if err != nil {
				return err
			}
			if active {
				intent, err := newBTCIsolationMigration(sv, &transition, b.Header.Height)
				if err == nil {
					err = advanceBTCIsolationMigrationManifest(sv, transitionKey, &transition, intent)
				}
				if err != nil {
					transition.MigrationStatus, transition.MigrationError = migrationStatusIntentBlocked, err.Error()
					if err := setTransitionStateWithIndex(sv, transitionKey, &transition); err != nil {
						return err
					}
				}
				continue
			}
		}
		intent, err := buildFrostMigrationIntent(sv, &transition, b.Header.Height)
		if err != nil {
			                                                                     
			                                                                       
			                                                                      
			                             
			transition.MigrationStatus = migrationStatusIntentBlocked
			transition.MigrationError = err.Error()
			if setErr := setTransitionStateWithIndex(sv, transitionKey, &transition); setErr != nil {
				return setErr
			}
			continue
		}
		                                                                        
		                                                                      
		                                                                        
		                                                                    
		hotData, err := proto.Marshal(intent)
		if err != nil {
			return err
		}
		sv.Set(migrationKey, hotData)
		sv.Set(keys.KeyFrostMigrationStateRef(intent.MigrationId), []byte(migrationKey))
		transition.MigrationJobId = intent.MigrationId
		transition.MigrationStatus = migrationStatusIntentFinalized
		transition.MigrationError = ""
		transition.MigrationTemplateHash = append([]byte(nil), intent.TemplateHash...)
		transition.MigrationIntentHash = append([]byte(nil), intent.IntentHash...)
		if err := setTransitionStateWithIndex(sv, transitionKey, &transition); err != nil {
			return err
		}
	}
	return nil
}

func hasClaimedOldEpochWithdraw(sv StateView, transition *pb.VaultTransitionState) (bool, error) {
	if transition == nil || transition.EpochId == 0 {
		return false, nil
	}
	chainName := chainpkg.NormalizeChain(transition.Chain)
	oldEpoch := transition.EpochId - 1
	if chainName != chainpkg.ChainBTC {
		rawJobID, exists, err := sv.Get(keys.KeyFrostWithdrawActiveVaultJob(chainName, transition.VaultId))
		if err != nil {
			return false, fmt.Errorf("read active physical Vault withdrawal job: %w", err)
		}
		if !exists || len(rawJobID) == 0 {
			return false, nil
		}
		jobID := strings.TrimSpace(string(rawJobID))
		jobData, found, err := sv.Get(keys.KeyFrostWithdrawJob(jobID))
		if err != nil {
			return false, fmt.Errorf("read active physical Vault withdrawal state: %w", err)
		}
		var job pb.FrostWithdrawJobState
		if jobID == "" || !found || len(jobData) == 0 || proto.Unmarshal(jobData, &job) != nil ||
			job.JobId != jobID || chainpkg.NormalizeChain(job.Chain) != chainName || job.VaultId != transition.VaultId ||
			job.KeyEpoch != oldEpoch || job.Asset != "" || job.Status != frostWithdrawJobStatusClaimed {
			return false, fmt.Errorf("invalid active physical Vault withdrawal job during handover")
		}
		return true, nil
	}
	jobs, err := scanStateViewOrderedKeysAfter(sv, keys.KeyFrostWithdrawActiveJobPrefix(chainName, transition.VaultId, oldEpoch), "", 1, false)
	if err != nil {
		return false, fmt.Errorf("scan active withdraw jobs: %w", err)
	}
	return len(jobs) > 0, nil
}

func buildFrostMigrationIntent(sv StateView, transition *pb.VaultTransitionState, height uint64) (*pb.FrostVaultMigrationState, error) {
	chainName := chainpkg.NormalizeChain(transition.Chain)
	if transition.EpochId == 0 {
		return nil, fmt.Errorf("invalid new epoch 0")
	}
	oldEpoch := transition.EpochId - 1
	vaultData, exists, err := sv.Get(keys.KeyFrostVaultState(chainName, transition.VaultId))
	if err != nil || !exists || len(vaultData) == 0 {
		return nil, fmt.Errorf("old vault state not found")
	}
	var vault pb.FrostVaultState
	if err := unmarshalProtoCompat(vaultData, &vault); err != nil {
		return nil, err
	}
	if vault.KeyEpoch != oldEpoch || !frostVaultGroupPubkeysEqual(chainName, vault.GroupPubkey, transition.OldGroupPubkey) {
		return nil, fmt.Errorf("old vault epoch/key mismatch")
	}
	if len(transition.NewGroupPubkey) == 0 {
		return nil, fmt.Errorf("new group public key is empty")
	}

	intent := &pb.FrostVaultMigrationState{
		MigrationId:    chainpkg.MigrationID(chainName, transition.VaultId, transition.EpochId),
		Chain:          chainName,
		VaultId:        transition.VaultId,
		OldEpoch:       oldEpoch,
		NewEpoch:       transition.EpochId,
		OldGroupPubkey: append([]byte(nil), transition.OldGroupPubkey...),
		NewGroupPubkey: append([]byte(nil), transition.NewGroupPubkey...),
		Status:         migrationStatusIntentFinalized,
		IntentHeight:   height,
	}

	cfgData, cfgExists, err := sv.Get(keys.KeyFrostVaultConfig(chainName, 0))
	if err != nil || !cfgExists || len(cfgData) == 0 {
		return nil, fmt.Errorf("vault config not found")
	}
	var cfg pb.FrostVaultConfig
	if err := unmarshalProtoCompat(cfgData, &cfg); err != nil {
		return nil, err
	}

	switch chainName {
	case chainpkg.ChainBTC:
		utxos, err := unlockedOldKeyBTCUTXOs(sv, transition.VaultId, transition.OldGroupPubkey, transition.NewGroupPubkey)
		if err != nil {
			return nil, err
		}
		if len(utxos) == 0 {
			intent.NoFunds = true
			intent.TemplateHash = chainpkg.EmptyMigrationTemplateHash()
		} else {
			inputPubkeys, err := btcMigrationInputPubkeys(sv, transition.VaultId, transition.OldGroupPubkey, utxos)
			if err != nil {
				return nil, err
			}
			result, _, err := btc.BuildMigrationTemplate(chainName, transition.VaultId, oldEpoch, transition.EpochId, utxos, transition.NewGroupPubkey, cfg.BtcNetwork, cfg.BtcFeeSatsPerVbyte, cfg.GuardianPubkey, inputPubkeys)
			if err != nil {
				return nil, err
			}
			intent.TemplateHash = append([]byte(nil), result.TemplateHash...)
			intent.TemplateData = append([]byte(nil), result.TemplateData...)
		}
		intent.IntentHash = chainpkg.MigrationIntentHash(chainName, transition.VaultId, oldEpoch, transition.EpochId, transition.OldGroupPubkey, transition.NewGroupPubkey, intent.TemplateHash, intent.NoFunds)
	case chainpkg.ChainETH, chainpkg.ChainBNB, chainpkg.ChainBASE, chainpkg.ChainARB, chainpkg.ChainTRX:
		if oldEpoch > 1 && cfg.AuthorityProtocol != 3 {
			if _, err := artifact.ResolveAccountMigrationHead(sv.Get, chainName, transition.VaultId, oldEpoch); err != nil {
				return nil, fmt.Errorf("previous account handover artifact chain is incomplete: %w", err)
			}
		}
		if vault.SignAlgo != chainpkg.SignAlgoETH || (transition.SignAlgo != chainpkg.SignAlgoETH && transition.SignAlgo != pb.SignAlgo_SIGN_ALGO_UNSPECIFIED) {
			return nil, fmt.Errorf("invalid account migration signing algorithm")
		}
		if _, err := evm.CanonicalBN128PublicKey(transition.OldGroupPubkey); err != nil {
			return nil, err
		}
		if int(transition.VaultId) >= len(cfg.VaultRefs) || strings.TrimSpace(cfg.VaultRefs[transition.VaultId]) == "" {
			return nil, fmt.Errorf("vault contract address not configured")
		}
		intent.AuthorityProtocol = cfg.AuthorityProtocol
		if cfg.AuthorityProtocol == 3 {
			if oldEpoch > 1 {
				ready, e := accountEpochAuthorityReady(sv, chainName, transition.VaultId, oldEpoch, &cfg)
				if e != nil || !ready {
					return nil, fmt.Errorf("previous V2 certificate unavailable: %v", e)
				}
			}
			raw, found, e := sv.Get(keys.KeyFrostVaultDEXNextBatchSeq(chainName, transition.VaultId))
			if e != nil {
				return nil, e
			}
			intent.FrozenNextBatchSeq = "1"
			if found {
				intent.FrozenNextBatchSeq = string(raw)
			}
			if _, e := evm.ParseBatchSeq(intent.FrozenNextBatchSeq); e != nil {
				return nil, e
			}
		}
		result, _, err := evm.CanonicalMigrationTemplate(&cfg, intent)
		if err != nil {
			return nil, err
		}
		if len(result.SigHashes) != 1 || len(result.SigHashes[0]) != 32 {
			return nil, fmt.Errorf("invalid EVM migration signing hash")
		}
		intent.TemplateHash = append([]byte(nil), result.TemplateHash...)
		intent.TemplateData = append([]byte(nil), result.TemplateData...)
		intent.IntentHash = append([]byte(nil), result.SigHashes[0]...)
	default:
		return nil, fmt.Errorf("authority migration is not implemented for chain %s", chainName)
	}
	return intent, nil
}

func unlockedOldKeyBTCUTXOs(sv StateView, vaultID uint32, oldPubkey, newPubkey []byte) ([]chainpkg.UTXO, error) {
	cfg, err := loadFrostVaultConfig(sv, chainpkg.ChainBTC)
	if err != nil {
		return nil, err
	}
	                                                                            
	                                                                            
	                                                           
	rows, err := sv.Scan(keys.KeyFrostBtcSpendableUtxoIndexPrefix(vaultID))
	if err != nil {
		return nil, err
	}
	rowKeys := make([]string, 0, len(rows))
	for key := range rows {
		rowKeys = append(rowKeys, key)
	}
	sort.Strings(rowKeys)
	out := make([]chainpkg.UTXO, 0, len(rowKeys))
	for _, key := range rowKeys {
		var utxo pb.FrostUtxo
		if err := proto.Unmarshal(rows[key], &utxo); err != nil {
			return nil, fmt.Errorf("parse BTC UTXO %s: %w", key, err)
		}
		if utxo.VaultId != vaultID || strings.TrimSpace(utxo.Txid) == "" {
			return nil, fmt.Errorf("invalid BTC UTXO %s", key)
		}
		amount, err := strconv.ParseUint(strings.TrimSpace(utxo.Amount), 10, 64)
		if err != nil || amount == 0 || len(utxo.ScriptPubkey) == 0 {
			return nil, fmt.Errorf("invalid BTC UTXO amount/script %s", key)
		}
		oldControlled, err := btcUTXOControlledBy(oldPubkey, &utxo, cfg.GuardianPubkey)
		if err != nil {
			return nil, err
		}
		if !oldControlled {
			newControlled, newErr := btcUTXOControlledBy(newPubkey, &utxo, cfg.GuardianPubkey)
			if newErr != nil || !newControlled {
				return nil, fmt.Errorf("BTC UTXO is controlled by neither transition key: %s", key)
			}
			continue
		}
		out = append(out, chainpkg.UTXO{TxID: utxo.Txid, Vout: utxo.Vout, Amount: amount, ScriptPubKey: append([]byte(nil), utxo.ScriptPubkey...), ConfirmHeight: utxo.FinalizeHeight})
	}
	return out, nil
}

func btcUTXOControlledBy(groupPubkey []byte, utxo *pb.FrostUtxo, guardian []byte) (bool, error) {
	if utxo == nil {
		return false, fmt.Errorf("nil BTC UTXO")
	}
	operator, err := btcInputOperatorPubkey(groupPubkey, utxo.Tweak)
	if err != nil {
		return false, err
	}
	script, _, _, err := utils.BuildDualCustodyTaproot(operator, guardian)
	return err == nil && bytes.Equal(script, utxo.ScriptPubkey), err
}
func btcMigrationInputPubkeys(sv StateView, vaultID uint32, group []byte, utxos []chainpkg.UTXO) ([][]byte, error) {
	stateKeys := make([]string, len(utxos))
	for i, u := range utxos {
		stateKeys[i] = keys.KeyFrostBtcUtxo(vaultID, u.TxID, u.Vout)
	}
	state, err := readBTCStateMany(sv, stateKeys)
	if err != nil {
		return nil, err
	}
	out := make([][]byte, len(utxos))
	for i, k := range stateKeys {
		var u pb.FrostUtxo
		if len(state[k]) == 0 || proto.Unmarshal(state[k], &u) != nil {
			return nil, fmt.Errorf("migration BTC input unavailable")
		}
		out[i], err = btcInputOperatorPubkey(group, u.Tweak)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
