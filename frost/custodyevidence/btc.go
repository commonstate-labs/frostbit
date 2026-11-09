package custodyevidence

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"dex/frost/artifact"
	"dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/keys"
	"dex/pb"
	"google.golang.org/protobuf/proto"
)

                                                                               
                                                                                
func InternalBTCOutputs(get Get, txid string) (BTCOutputs, error) {
	result := BTCOutputs{TxID: txid, InternalVouts: []uint32{}}
	if !hashID(txid) {
		return result, fmt.Errorf("invalid canonical Bitcoin transaction ID")
	}
	metaRaw, found, err := get(keys.KeyFrostArtifactMeta(txid))
	if err != nil {
		return result, err
	}
	if !found {
		if _, permanent, err := get(keys.KeyFrostPermanentArtifact(txid)); err != nil {
			return result, err
		} else if permanent {
			return result, fmt.Errorf("permanent Bitcoin artifact is missing its metadata")
		}
		return result, nil
	}
	meta, err := artifact.UnmarshalMetadata(metaRaw)
	if err != nil || meta.Version != artifact.Version || meta.ArtifactID != txid || !strings.EqualFold(meta.Chain, "btc") || meta.SourceID == "" {
		return result, fmt.Errorf("invalid permanent Bitcoin artifact metadata")
	}
	record, err := permanentRecord(get, txid, metaRaw)
	if err != nil {
		return result, err
	}
	var template *btc.BTCTemplate
	switch meta.Kind {
	case artifact.KindWithdraw:
		var job pb.FrostWithdrawJobState
		if err := readProto(get, keys.KeyFrostPermanentWithdrawJob(meta.SourceID), &job); err != nil {
			return result, err
		}
		template, err = btc.FromJSON(job.TemplateData)
		if err != nil || job.JobId != meta.SourceID || job.Status != "SIGNED" || !strings.EqualFold(job.Chain, "btc") || job.VaultId != meta.VaultID || job.KeyEpoch != meta.Epoch || template == nil || template.KeyEpoch != meta.Epoch || !bytes.Equal(template.TemplateHash(), job.TemplateHash) || len(job.WithdrawIds) == 0 || len(job.WithdrawIds) > chain.MaxWithdrawalsPerBatch || len(template.WithdrawIDs) != len(job.WithdrawIds) {
			return result, fmt.Errorf("invalid permanent Bitcoin withdrawal template")
		}
		for i, id := range job.WithdrawIds {
			if id != template.WithdrawIDs[i] {
				return result, fmt.Errorf("Bitcoin withdrawal identity order differs")
			}
		}
		if len(template.Outputs) != len(job.WithdrawIds) && len(template.Outputs) != len(job.WithdrawIds)+1 {
			return result, fmt.Errorf("unexpected Bitcoin withdrawal outputs")
		}
		if len(template.Outputs) > len(job.WithdrawIds) {
			result.InternalVouts = []uint32{uint32(len(template.Outputs) - 1)}
		}
	case artifact.KindMigration:
		var archive pb.FrostVaultMigrationArchive
		if err := readProto(get, keys.KeyFrostPermanentMigration(meta.SourceID), &archive); err != nil {
			return result, err
		}
		state := archive.State
		if state == nil {
			return result, fmt.Errorf("missing permanent Bitcoin migration state")
		}
		template, err = btc.FromJSON(state.TemplateData)
		if err != nil || template == nil || state.MigrationId != meta.SourceID || state.Status != "SIGNED" || !strings.EqualFold(state.Chain, "btc") || state.VaultId != meta.VaultID || state.NewEpoch != meta.Epoch || template.KeyEpoch != state.OldEpoch || !bytes.Equal(template.TemplateHash(), state.TemplateHash) || len(template.Outputs) != 1 {
			return result, fmt.Errorf("invalid permanent Bitcoin migration template")
		}
		                                                                          
		                                                                           
		                                                                            
		                                                                             
		inputs := len(template.Inputs)
		if len(archive.InputSigs) != inputs || len(archive.GuardianSigs) != inputs+1 {
			return result, fmt.Errorf("invalid permanent Bitcoin migration signatures")
		}
		raw, err := template.BuildSignedRawTx(archive.InputSigs, archive.GuardianSigs[:inputs])
		if err != nil || !bytes.Equal(raw, record.RawTx) || len(archive.RawTx) != 0 && !bytes.Equal(archive.RawTx, raw) {
			return result, fmt.Errorf("permanent Bitcoin migration artifact differs")
		}
		if state.BtcProtocolVersion == 3 {
			if err := validateMigrationChildEvidence(get, state, template); err != nil {
				return result, err
			}
		}
		result.InternalVouts = []uint32{0}
	default:
		return result, fmt.Errorf("unsupported permanent Bitcoin artifact kind")
	}
	actual, err := template.TxID()
	if err != nil || actual != txid || template.VaultID != meta.VaultID {
		return result, fmt.Errorf("Bitcoin template transaction ID differs")
	}
	result.KnownArtifact = true
	return result, nil
}

func validateMigrationChildEvidence(get Get, child *pb.FrostVaultMigrationState, template *btc.BTCTemplate) error {
	if child.ParentMigrationId == "" || child.BtcChildSeq == 0 || len(child.BtcOriginId) != 32 || len(template.Inputs) != 1 {
		return fmt.Errorf("invalid permanent BTC isolated migration child")
	}
	var parentArchive pb.FrostVaultMigrationArchive
	if err := readProto(get, keys.KeyFrostPermanentMigration(child.ParentMigrationId), &parentArchive); err != nil {
		return err
	}
	parent := parentArchive.State
	if parent == nil {
		return fmt.Errorf("missing permanent BTC migration manifest")
	}
	if parent.MigrationId != child.ParentMigrationId || parent.ParentMigrationId != "" || parent.BtcProtocolVersion != 3 || parent.Chain != child.Chain || parent.VaultId != child.VaultId || parent.OldEpoch != child.OldEpoch || parent.NewEpoch != child.NewEpoch || child.BtcChildSeq > parent.BtcChildCount || parent.Status != "INTENT_FINALIZED" && parent.Status != "SIGNED" || !bytes.Equal(parent.OldGroupPubkey, child.OldGroupPubkey) || !bytes.Equal(parent.NewGroupPubkey, child.NewGroupPubkey) {
		return fmt.Errorf("BTC migration child differs from sealed manifest")
	}
	member, ok, err := get(keys.KeyFrostBtcMigrationChild(parent.MigrationId, child.BtcChildSeq))
	if err != nil {
		return err
	}
	if !ok || string(member) != child.MigrationId {
		return fmt.Errorf("BTC migration child manifest membership missing")
	}
	var cfg pb.FrostVaultConfig
	if err := readProto(get, keys.KeyFrostVaultConfig("btc", 0), &cfg); err != nil {
		return err
	}
	if !bytes.Equal(chain.BTCMigrationManifestIntentHash(cfg.DepositDexChainId, cfg.BtcNetwork, parent), parent.IntentHash) {
		return fmt.Errorf("BTC migration parent authority mismatch")
	}
	hash := chain.BTCMigrationChildIntentHash(cfg.DepositDexChainId, parent.MigrationId, child.BtcChildSeq, child.BtcOriginId, child.TemplateHash)
	if !bytes.Equal(hash, child.IntentHash) || chain.BTCMigrationChildID(parent.MigrationId, child.BtcChildSeq, hash) != child.MigrationId {
		return fmt.Errorf("BTC migration child authority mismatch")
	}
	return nil
}

                                                                                 
                                                                          
func InternalBTCOutputsMany(get Get, batch func([]string) (map[string][]byte, error), txids []string) ([]BTCOutputs, error) {
	if len(txids) == 0 || len(txids) > 256 {
		return nil, fmt.Errorf("Bitcoin evidence batch must contain 1..256 transaction IDs")
	}
	ids := append([]string(nil), txids...)
	sort.Strings(ids)
	unique := ids[:0]
	for _, id := range ids {
		if !hashID(id) {
			return nil, fmt.Errorf("invalid Bitcoin transaction ID")
		}
		if len(unique) == 0 || unique[len(unique)-1] != id {
			unique = append(unique, id)
		}
	}
	if batch != nil {
		type entry struct {
			raw   []byte
			found bool
		}
		cache := make(map[string]entry, len(unique)*3)
		prefetch := func(ks []string) error {
			sort.Strings(ks)
			dedup := ks[:0]
			for _, k := range ks {
				if len(dedup) == 0 || dedup[len(dedup)-1] != k {
					dedup = append(dedup, k)
				}
			}
			if len(dedup) == 0 {
				return nil
			}
			values, err := batch(dedup)
			if err != nil {
				return err
			}
			for _, k := range dedup {
				v, ok := values[k]
				cache[k] = entry{v, ok}
			}
			return nil
		}
		ks := make([]string, 0, len(unique)*2)
		for _, id := range unique {
			ks = append(ks, keys.KeyFrostArtifactMeta(id), keys.KeyFrostPermanentArtifact(id))
		}
		if err := prefetch(ks); err != nil {
			return nil, err
		}
		dependencies := make([]string, 0, len(unique)*2)
		for _, id := range unique {
			value := cache[keys.KeyFrostArtifactMeta(id)]
			if !value.found {
				continue
			}
			meta, err := artifact.UnmarshalMetadata(value.raw)
			if err != nil {
				return nil, err
			}
			switch meta.Kind {
			case artifact.KindWithdraw:
				dependencies = append(dependencies, keys.KeyFrostPermanentWithdrawJob(meta.SourceID))
			case artifact.KindMigration:
				dependencies = append(dependencies, keys.KeyFrostPermanentMigration(meta.SourceID))
			default:
				return nil, fmt.Errorf("unsupported Bitcoin artifact kind")
			}
		}
		if err := prefetch(dependencies); err != nil {
			return nil, err
		}
		parents := []string{}
		for _, id := range unique {
			value := cache[keys.KeyFrostArtifactMeta(id)]
			if !value.found {
				continue
			}
			meta, e := artifact.UnmarshalMetadata(value.raw)
			if e != nil {
				return nil, e
			}
			if meta.Kind != artifact.KindMigration {
				continue
			}
			var archive pb.FrostVaultMigrationArchive
			if raw := cache[keys.KeyFrostPermanentMigration(meta.SourceID)]; raw.found && proto.Unmarshal(raw.raw, &archive) == nil && archive.State != nil && archive.State.BtcProtocolVersion == 3 {
				s := archive.State
				parents = append(parents, keys.KeyFrostPermanentMigration(s.ParentMigrationId), keys.KeyFrostBtcMigrationChild(s.ParentMigrationId, s.BtcChildSeq), keys.KeyFrostVaultConfig("btc", 0))
			}
		}
		if err := prefetch(parents); err != nil {
			return nil, err
		}
		original := get
		get = func(k string) ([]byte, bool, error) {
			if v, ok := cache[k]; ok {
				return v.raw, v.found, nil
			}
			return original(k)
		}
	}
	results := make([]BTCOutputs, 0, len(unique))
	for _, id := range unique {
		result, err := InternalBTCOutputs(get, id)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}
