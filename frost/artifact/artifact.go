package artifact

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/btcsuite/btcd/wire"
)

const Version uint32 = 1

const (
	KindWithdraw  = "WITHDRAW"
	KindMigration = "MIGRATION"
)

                                                                            
                                     
type Metadata struct {
	Version    uint32   `json:"version"`
	ArtifactID string   `json:"artifact_id"`
	Kind       string   `json:"kind"`
	Chain      string   `json:"chain"`
	VaultID    uint32   `json:"vault_id"`
	Epoch      uint64   `json:"epoch"`
	Height     uint64   `json:"height"`
	SourceID   string   `json:"source_id,omitempty"`
	RawHash    string   `json:"raw_hash"`
	ParentIDs  []string `json:"parent_ids,omitempty"`
}

                                                                          
                                                       
type Record struct {
	Metadata Metadata `json:"metadata"`
	RawTx    []byte   `json:"raw_tx"`
}

type Bundle struct {
	Version     uint32   `json:"version"`
	MigrationID string   `json:"migration_id"`
	Artifacts   []Record `json:"artifacts"`
}

func NewRecord(meta Metadata, rawTx []byte) (*Record, error) {
	meta.Version = Version
	meta.ArtifactID = strings.TrimSpace(meta.ArtifactID)
	meta.Kind = strings.ToUpper(strings.TrimSpace(meta.Kind))
	meta.Chain = strings.ToUpper(strings.TrimSpace(meta.Chain))
	meta.ParentIDs = uniqueStrings(meta.ParentIDs)
	hash := sha256.Sum256(rawTx)
	meta.RawHash = hex.EncodeToString(hash[:])
	if meta.ArtifactID == "" {
		var err error
		meta.ArtifactID, err = contentID(meta, rawTx)
		if err != nil {
			return nil, err
		}
	}
	record := &Record{Metadata: meta, RawTx: append([]byte(nil), rawTx...)}
	return record, record.Validate()
}

func (r *Record) Validate() error {
	if r == nil || r.Metadata.Version != Version || strings.TrimSpace(r.Metadata.ArtifactID) == "" {
		return fmt.Errorf("invalid artifact metadata")
	}
	hash := sha256.Sum256(r.RawTx)
	decoded, err := hex.DecodeString(r.Metadata.RawHash)
	if err != nil || !bytes.Equal(decoded, hash[:]) {
		return fmt.Errorf("artifact raw transaction hash mismatch")
	}
	if len(r.Metadata.ParentIDs) != len(uniqueStrings(r.Metadata.ParentIDs)) {
		return fmt.Errorf("artifact parent IDs contain duplicates")
	}
	for _, parent := range r.Metadata.ParentIDs {
		if parent == r.Metadata.ArtifactID {
			return fmt.Errorf("artifact depends on itself")
		}
	}
	if strings.EqualFold(r.Metadata.Chain, "BTC") && len(r.RawTx) > 0 {
		var tx wire.MsgTx
		if err := tx.Deserialize(bytes.NewReader(r.RawTx)); err != nil || tx.TxHash().String() != r.Metadata.ArtifactID {
			return fmt.Errorf("BTC artifact ID is not the raw transaction txid")
		}
	} else {
		id, err := contentID(r.Metadata, r.RawTx)
		if err != nil {
			return err
		}
		if id != r.Metadata.ArtifactID {
			return fmt.Errorf("artifact content ID mismatch")
		}
	}
	return nil
}

func contentID(meta Metadata, raw []byte) (string, error) {
	domain := meta.Chain + "|" + meta.Kind + "|"
	if strings.EqualFold(meta.Chain, "BTC") && len(raw) == 0 {
		                                                                       
		                                                                         
		                                                                          
		if meta.Kind != KindMigration || strings.TrimSpace(meta.SourceID) == "" {
			return "", fmt.Errorf("empty BTC artifact requires a migration identity")
		}
		domain += fmt.Sprintf("EMPTY_V1|%d:%s", len(meta.SourceID), meta.SourceID)
	}
	hash := sha256.Sum256(append([]byte(domain), raw...))
	return hex.EncodeToString(hash[:]), nil
}

func MarshalMetadata(meta Metadata) ([]byte, error) { return json.Marshal(meta) }
func UnmarshalMetadata(raw []byte) (Metadata, error) {
	var meta Metadata
	err := json.Unmarshal(raw, &meta)
	return meta, err
}
func MarshalRecord(record *Record) ([]byte, error) { return json.Marshal(record) }
func UnmarshalRecord(raw []byte) (*Record, error) {
	var record Record
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, err
	}
	return &record, record.Validate()
}

func MarshalBundle(bundle *Bundle) ([]byte, error) {
	if err := ValidateBundle(bundle); err != nil {
		return nil, err
	}
	return json.Marshal(bundle)
}

func UnmarshalBundle(raw []byte) (*Bundle, error) {
	var bundle Bundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		return nil, err
	}
	return &bundle, ValidateBundle(&bundle)
}

                                                                         
                                                                            
                                                                  
func ValidateBundle(bundle *Bundle) error {
	if bundle == nil || bundle.Version != Version || strings.TrimSpace(bundle.MigrationID) == "" {
		return fmt.Errorf("invalid artifact bundle")
	}
	seen := make(map[string]struct{}, len(bundle.Artifacts))
	for i := range bundle.Artifacts {
		record := &bundle.Artifacts[i]
		if err := record.Validate(); err != nil {
			return fmt.Errorf("artifact %d: %w", i, err)
		}
		for _, parent := range record.Metadata.ParentIDs {
			if _, ok := seen[parent]; !ok {
				return fmt.Errorf("artifact %s appears before parent %s", record.Metadata.ArtifactID, parent)
			}
		}
		if _, duplicate := seen[record.Metadata.ArtifactID]; duplicate {
			return fmt.Errorf("duplicate artifact %s", record.Metadata.ArtifactID)
		}
		seen[record.Metadata.ArtifactID] = struct{}{}
	}
	return nil
}

func uniqueStrings(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			set[value] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
