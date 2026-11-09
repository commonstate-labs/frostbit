package vm

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"dex/frost/artifact"
	"dex/frost/chain/btc"
	"dex/keys"
)

func frostBTCParentArtifactIDs(sv StateView, vaultID uint32, inputs []btc.TxInput) ([]string, error) {
	parents := make([]string, 0, len(inputs))
	seen := make(map[string]struct{}, len(inputs))
	for _, input := range inputs {
		raw, found, err := sv.Get(keys.KeyFrostBtcUtxoArtifact(vaultID, input.TxID, input.Vout))
		if err != nil && !isStateViewNotFound(err) {
			return nil, err
		}
		if !found {
			continue                                                                 
		}
		id := strings.TrimSpace(string(raw))
		if id == "" {
			return nil, fmt.Errorf("empty BTC parent artifact reference")
		}
		if _, duplicate := seen[id]; !duplicate {
			seen[id] = struct{}{}
			parents = append(parents, id)
		}
	}
	sort.Strings(parents)
	return parents, nil
}

func storeFrostArtifact(sv StateView, meta artifact.Metadata, rawTx []byte) (*artifact.Record, error) {
	account := !strings.EqualFold(meta.Chain, "BTC")
	if account {
		switch strings.ToUpper(strings.TrimSpace(meta.Kind)) {
		case artifact.KindWithdraw:
			                                                                      
			                                                                        
			                                                                  
			if len(meta.ParentIDs) > 0 {
				expected, err := artifact.ResolveAccountMigrationHead(sv.Get, meta.Chain, meta.VaultID, meta.Epoch)
				if err != nil {
					return nil, fmt.Errorf("resolve current account migration artifact: %w", err)
				}
				if expected == "" || len(meta.ParentIDs) != 1 || meta.ParentIDs[0] != expected {
					return nil, fmt.Errorf("account handover batch parent does not match current migration artifact")
				}
			}
		case artifact.KindMigration:
			if len(meta.ParentIDs) != 0 {
				return nil, fmt.Errorf("account migration parents must be derived from migration state")
			}
			if meta.Epoch == 0 {
				return nil, fmt.Errorf("account migration epoch cannot be zero")
			}
			previous, err := artifact.ResolveAccountMigrationHead(sv.Get, meta.Chain, meta.VaultID, meta.Epoch-1)
			if err != nil {
				return nil, err
			}
			if previous != "" {
				meta.ParentIDs = []string{previous}
			}
		default:
			return nil, fmt.Errorf("unsupported account artifact kind")
		}
	}
	record, err := artifact.NewRecord(meta, rawTx)
	if err != nil {
		return nil, err
	}
	                                                                       
	                                                                      
	                                                                            
	for _, parentID := range record.Metadata.ParentIDs {
		raw, found, err := sv.Get(keys.KeyFrostPermanentArtifact(parentID))
		if err != nil && !isStateViewNotFound(err) {
			return nil, err
		}
		if !found || len(raw) == 0 {
			return nil, fmt.Errorf("permanent artifact dependency unavailable; historical state migration required: %s", parentID)
		}
		parent, err := artifact.UnmarshalRecord(raw)
		if err != nil || parent.Metadata.ArtifactID != parentID {
			return nil, fmt.Errorf("invalid permanent artifact dependency: %s", parentID)
		}
		canonical, err := artifact.MarshalMetadata(parent.Metadata)
		if err != nil {
			return nil, err
		}
		anchored, exists, err := sv.Get(keys.KeyFrostArtifactMeta(parentID))
		if err != nil && !isStateViewNotFound(err) {
			return nil, err
		}
		if !exists || !bytes.Equal(canonical, anchored) {
			return nil, fmt.Errorf("permanent artifact dependency metadata mismatch: %s", parentID)
		}
	}
	metaData, err := artifact.MarshalMetadata(record.Metadata)
	if err != nil {
		return nil, err
	}
	metaKey := keys.KeyFrostArtifactMeta(record.Metadata.ArtifactID)
	if existing, found, readErr := sv.Get(metaKey); readErr != nil && !isStateViewNotFound(readErr) {
		return nil, readErr
	} else if found && len(existing) > 0 && !bytes.Equal(existing, metaData) {
		return nil, fmt.Errorf("artifact ID already has different metadata")
	}
	recordData, err := artifact.MarshalRecord(record)
	if err != nil {
		return nil, err
	}
	sv.Set(metaKey, metaData)
	sv.Set(keys.KeyFrostPermanentArtifact(record.Metadata.ArtifactID), recordData)
	sv.Set(keys.KeyArchiveFrostArtifact(record.Metadata.ArtifactID), recordData)
	sv.Set(keys.KeyArchiveFrostArtifactHeight(record.Metadata.Height, record.Metadata.ArtifactID), []byte(record.Metadata.ArtifactID))
	if account && record.Metadata.Kind == artifact.KindMigration {
		sv.Set(keys.KeyFrostVaultLastMigrationArtifact(meta.Chain, meta.VaultID), []byte(record.Metadata.ArtifactID))
	}
	return record, nil
}
