package indexdb

import (
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"dex/keys"
	"dex/pb"
)

                                                                               
                                                                            
                                                                          
type ReadModelCoverage struct {
	SchemaVersion uint32 `json:"schema_version"`
	BaseHeight    uint64 `json:"base_height"`
	TipHeight     uint64 `json:"tip_height"`
}

func validateBlockReadModel(block *pb.Block, model *pb.BlockReadModel) error {
	if block == nil || block.Header == nil {
		return fmt.Errorf("cannot validate read model for incomplete block")
	}
	if err := pb.VerifyBlockReadModel(model, block.Header.Height, block.BlockHash); err != nil {
		return err
	}
	if model.ParentId != block.Header.PrevBlockHash {
		return fmt.Errorf("block read model parent %q does not match %q", model.ParentId, block.Header.PrevBlockHash)
	}
	for i, mutation := range model.Mutations {
		switch mutation.Op {
		case pb.ReadModelMutationOp_READ_MODEL_MUTATION_PUT,
			pb.ReadModelMutationOp_READ_MODEL_MUTATION_DELETE:
		default:
			return fmt.Errorf("unsupported read model mutation op %d at %d", mutation.Op, i)
		}
		switch mutation.Retention {
		case pb.ReadModelRetention_READ_MODEL_RETENTION_BLOCK_SCOPED,
			pb.ReadModelRetention_READ_MODEL_RETENTION_CURRENT:
		default:
			return fmt.Errorf("unsupported read model retention %d at %d", mutation.Retention, i)
		}
		if expected, compact := compactReadModelRetention(mutation.Key); compact && mutation.Retention != expected {
			return fmt.Errorf("read model key %q has retention %d, want %d", mutation.Key, mutation.Retention, expected)
		}
	}
	return nil
}

                                                                           
                                                                             
                                                                           
                                                                             
                                                                                
                                                                              
                    
func (idb *IndexDB) applyReadModelMutations(batch *IndexBatch, manifest *heightManifest, model *pb.BlockReadModel) error {
	if batch == nil || manifest == nil || model == nil {
		return fmt.Errorf("cannot apply incomplete read model batch")
	}
	var owned map[string]heightOwnedKey
	ownerHeight := strconv.FormatUint(model.Height, 10)
	for _, mutation := range model.Mutations {
		_, compactRetention := compactReadModelRetention(mutation.Key)
		ownerKey := KeyReadModelOwner(mutation.Key)
		switch mutation.Op {
		case pb.ReadModelMutationOp_READ_MODEL_MUTATION_PUT:
			if err := batch.SetBytes(mutation.Key, mutation.Value); err != nil {
				return err
			}
			if compactRetention {
				if mutation.Retention == pb.ReadModelRetention_READ_MODEL_RETENTION_BLOCK_SCOPED {
					manifest.add(mutation.Key)
				}
				continue
			}
			if err := batch.Set(ownerKey, ownerHeight); err != nil {
				return err
			}
			if mutation.Retention == pb.ReadModelRetention_READ_MODEL_RETENTION_BLOCK_SCOPED {
				if owned == nil {
					owned = make(map[string]heightOwnedKey)
				}
				owned[mutation.Key] = heightOwnedKey{Key: mutation.Key, OwnerHeight: model.Height}
			} else if owned != nil {
				delete(owned, mutation.Key)
			}
		case pb.ReadModelMutationOp_READ_MODEL_MUTATION_DELETE:
			if err := batch.Delete(mutation.Key); err != nil {
				return err
			}
			if !compactRetention {
				if err := batch.Delete(ownerKey); err != nil {
					return err
				}
			}
			if owned != nil {
				delete(owned, mutation.Key)
			}
		}
	}

	keys := make([]string, 0, len(owned))
	for key := range owned {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		manifest.OwnedKeys = append(manifest.OwnedKeys, owned[key])
	}
	return nil
}

                                                                              
                                                                        
                                                                       
                                                                          
               
func compactReadModelRetention(key string) (pb.ReadModelRetention, bool) {
	switch {
	case strings.HasPrefix(key, keys.KeyUserActivityFamilyPrefix()),
		strings.HasPrefix(key, keys.KeyFuturePositionHistoryPrefix()):
		return pb.ReadModelRetention_READ_MODEL_RETENTION_BLOCK_SCOPED, true
	case strings.HasPrefix(key, keys.KeySpotAccountOrderFamilyPrefix()),
		strings.HasPrefix(key, keys.KeyFutureAccountOrderFamilyPrefix()):
		if strings.Contains(key, "|status:open|") {
			return pb.ReadModelRetention_READ_MODEL_RETENTION_CURRENT, true
		}
		return pb.ReadModelRetention_READ_MODEL_RETENTION_BLOCK_SCOPED, true
	default:
		return 0, false
	}
}

                                                                            
                                                                            
                                                            
func (idb *IndexDB) GetReadModelCoverage() (*ReadModelCoverage, error) {
	schemaRaw, err := idb.Get(KeyReadModelSchemaVersion)
	if err != nil {
		return nil, err
	}
	baseRaw, err := idb.Get(KeyReadModelBaseHeight)
	if err != nil {
		return nil, err
	}
	tipRaw, err := idb.Get(KeyReadModelTipHeight)
	if err != nil {
		return nil, err
	}
	if schemaRaw == "" && baseRaw == "" && tipRaw == "" {
		return nil, nil
	}
	if schemaRaw == "" || baseRaw == "" || tipRaw == "" {
		return nil, fmt.Errorf("incomplete read-model coverage metadata")
	}
	schema, err := strconv.ParseUint(schemaRaw, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid read-model schema version: %w", err)
	}
	base, err := strconv.ParseUint(baseRaw, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid read-model base height: %w", err)
	}
	tip, err := strconv.ParseUint(tipRaw, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid read-model tip height: %w", err)
	}
	if uint32(schema) != pb.BlockReadModelSchemaVersion {
		return nil, fmt.Errorf("unsupported stored read-model schema %d", schema)
	}
	if base > tip {
		return nil, fmt.Errorf("invalid read-model coverage %d..%d", base, tip)
	}
	return &ReadModelCoverage{SchemaVersion: uint32(schema), BaseHeight: base, TipHeight: tip}, nil
}

func (idb *IndexDB) advanceReadModelCoverage(batch *IndexBatch, model *pb.BlockReadModel) error {
	coverage, err := idb.GetReadModelCoverage()
	if err != nil {
		return err
	}
	if coverage == nil {
		                                                                         
		                                                                        
		                                                                          
		syncRaw, err := idb.Get(KeySyncHeight)
		if err != nil {
			return err
		}
		if syncRaw != "" && syncRaw != "0" {
			return fmt.Errorf("cannot start read-model coverage on legacy sync height %s", syncRaw)
		}
		if err := batch.Set(KeyReadModelSchemaVersion, strconv.FormatUint(uint64(model.SchemaVersion), 10)); err != nil {
			return err
		}
		if err := batch.Set(KeyReadModelBaseHeight, strconv.FormatUint(model.Height, 10)); err != nil {
			return err
		}
		return batch.Set(KeyReadModelTipHeight, strconv.FormatUint(model.Height, 10))
	}
	if coverage.SchemaVersion != model.SchemaVersion {
		return fmt.Errorf("read-model schema changed from %d to %d", coverage.SchemaVersion, model.SchemaVersion)
	}
	syncHeight, err := idb.GetSyncHeight()
	if err != nil {
		return err
	}
	if syncHeight != coverage.TipHeight {
		return fmt.Errorf("generic sync height %d diverges from read-model tip %d", syncHeight, coverage.TipHeight)
	}
	if coverage.TipHeight == ^uint64(0) || model.Height != coverage.TipHeight+1 {
		return fmt.Errorf("non-contiguous read-model height %d after tip %d", model.Height, coverage.TipHeight)
	}
	return batch.Set(KeyReadModelTipHeight, strconv.FormatUint(model.Height, 10))
}

func readModelDigestHex(model *pb.BlockReadModel) string {
	if model == nil {
		return ""
	}
	return hex.EncodeToString(model.PayloadHash)
}
