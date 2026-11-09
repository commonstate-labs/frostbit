package pb

import (
	"bytes"
	"crypto/sha256"
	"fmt"

	"google.golang.org/protobuf/proto"
)

const BlockReadModelSchemaVersion uint32 = 2

                                                                           
                                                                             
                                              
func ComputeBlockReadModelHash(model *BlockReadModel) ([]byte, error) {
	if model == nil {
		return nil, fmt.Errorf("nil block read model")
	}
	clone := proto.Clone(model).(*BlockReadModel)
	clone.PayloadHash = nil
	payload, err := (proto.MarshalOptions{Deterministic: true}).Marshal(clone)
	if err != nil {
		return nil, fmt.Errorf("marshal block read model: %w", err)
	}
	digest := sha256.Sum256(payload)
	return digest[:], nil
}

                                                                       
func SealBlockReadModel(model *BlockReadModel) error {
	digest, err := ComputeBlockReadModelHash(model)
	if err != nil {
		return err
	}
	model.PayloadHash = digest
	return nil
}

                                                                            
func VerifyBlockReadModel(model *BlockReadModel, height uint64, blockID string) error {
	if model == nil {
		return fmt.Errorf("missing block read model")
	}
	if model.SchemaVersion != BlockReadModelSchemaVersion {
		return fmt.Errorf("unsupported block read model schema %d", model.SchemaVersion)
	}
	if model.Height != height {
		return fmt.Errorf("block read model height %d does not match %d", model.Height, height)
	}
	if model.BlockId == "" || model.BlockId != blockID {
		return fmt.Errorf("block read model id %q does not match %q", model.BlockId, blockID)
	}
	want, err := ComputeBlockReadModelHash(model)
	if err != nil {
		return err
	}
	if len(model.PayloadHash) != sha256.Size || !bytes.Equal(model.PayloadHash, want) {
		return fmt.Errorf("block read model payload hash mismatch")
	}
	for i, mutation := range model.Mutations {
		if mutation == nil {
			return fmt.Errorf("nil read model mutation at %d", i)
		}
		if mutation.Ordinal != uint32(i) {
			return fmt.Errorf("read model mutation ordinal %d at index %d", mutation.Ordinal, i)
		}
		if mutation.Key == "" {
			return fmt.Errorf("empty read model mutation key at %d", i)
		}
	}
	return nil
}
