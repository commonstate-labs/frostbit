package chain

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

const migrationIntentDomain = "DEX_FROST_VAULT_MIGRATION_V1"

                                                                           
                                                                        
func MigrationID(chainName string, vaultID uint32, newEpoch uint64) string {
	digest := sha256.Sum256(encodeMigrationFields(
		[]byte(NormalizeChain(chainName)),
		uint32Bytes(vaultID),
		uint64Bytes(newEpoch),
	))
	return fmt.Sprintf("migration_%s_%d_%d_%s", NormalizeChain(chainName), vaultID, newEpoch, hex.EncodeToString(digest[:8]))
}

                                                                            
                                                                            
                                                                         
                                                              
func MigrationIntentHash(chainName string, vaultID uint32, oldEpoch, newEpoch uint64, oldPubkey, newPubkey, templateHash []byte, noFunds bool) []byte {
	flag := byte(0)
	if noFunds {
		flag = 1
	}
	payload := encodeMigrationFields(
		[]byte(migrationIntentDomain),
		[]byte(NormalizeChain(chainName)),
		uint32Bytes(vaultID),
		uint64Bytes(oldEpoch),
		uint64Bytes(newEpoch),
		oldPubkey,
		newPubkey,
		templateHash,
		[]byte{flag},
	)
	digest := sha256.Sum256(payload)
	return digest[:]
}

func EmptyMigrationTemplateHash() []byte {
	digest := sha256.Sum256(nil)
	return digest[:]
}

func encodeMigrationFields(fields ...[]byte) []byte {
	var out bytes.Buffer
	for _, field := range fields {
		_ = binary.Write(&out, binary.BigEndian, uint32(len(field)))
		_, _ = out.Write(field)
	}
	return out.Bytes()
}

func uint32Bytes(v uint32) []byte {
	out := make([]byte, 4)
	binary.BigEndian.PutUint32(out, v)
	return out
}

func uint64Bytes(v uint64) []byte {
	out := make([]byte, 8)
	binary.BigEndian.PutUint64(out, v)
	return out
}
