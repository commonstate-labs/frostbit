package artifact

import (
	"fmt"
	"strings"

	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

                                                                          
                                                                           
                                                                          
                                                                              
                                                       
func ResolveAccountMigrationHead(get func(string) ([]byte, bool, error), chain string, vaultID uint32, epoch uint64) (string, error) {
	chain = strings.ToLower(strings.TrimSpace(chain))
	if get == nil || chain == "" || chain == "btc" {
		return "", fmt.Errorf("invalid account migration head lookup")
	}
	validate := func(id string) (string, error) {
		if id == "" || strings.TrimSpace(id) != id {
			return "", fmt.Errorf("empty or malformed account migration head")
		}
		raw, found, err := get(keys.KeyFrostArtifactMeta(id))
		if err != nil {
			return "", err
		}
		if !found || len(raw) == 0 {
			return "", fmt.Errorf("account migration head metadata missing")
		}
		meta, err := UnmarshalMetadata(raw)
		if err != nil || meta.Version != Version || meta.ArtifactID != id || meta.Kind != KindMigration ||
			!strings.EqualFold(meta.Chain, chain) || meta.VaultID != vaultID || meta.Epoch != epoch {
			return "", fmt.Errorf("account migration head scope/epoch mismatch")
		}
		return id, nil
	}
	if raw, found, err := get(keys.KeyFrostVaultLastMigrationArtifact(chain, vaultID)); err != nil {
		return "", err
	} else if found {
		return validate(string(raw))
	}

	                                                                         
	                                                                       
	                                                                           
	raw, found, err := get(keys.KeyFrostVaultMigration(chain, vaultID, epoch))
	if err != nil {
		return "", err
	}
	if !found {
		return "", nil                                                          
	}
	var migration pb.FrostVaultMigrationState
	if len(raw) == 0 || proto.Unmarshal(raw, &migration) != nil || migration.Status != "SIGNED" ||
		!strings.EqualFold(migration.Chain, chain) || migration.VaultId != vaultID || migration.NewEpoch != epoch ||
		strings.TrimSpace(migration.MigrationId) == "" {
		return "", fmt.Errorf("invalid signed account migration marker")
	}
	id, found, err := get(keys.KeyFrostMigrationArtifact(migration.MigrationId))
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("signed account migration artifact reference missing")
	}
	return validate(string(id))
}
