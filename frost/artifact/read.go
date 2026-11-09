package artifact

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"

	"dex/keys"
)

var (
	ErrRecordUnavailable = errors.New("artifact bytes unavailable")
	ErrInvalidRecord     = errors.New("invalid artifact record")
	ErrRecordTooLarge    = errors.New("artifact exceeds download budget")
)

                                                                              
                                                                             
                                                                              
                                                        
func ReadRecord(get func(string) ([]byte, bool, error), id string, maxEncodedBytes int) (*Record, error) {
	decoded, err := hex.DecodeString(id)
	if err != nil || len(decoded) != 32 {
		return nil, fmt.Errorf("%w: malformed artifact ID", ErrInvalidRecord)
	}
	meta, found, err := get(keys.KeyFrostArtifactMeta(id))
	if err != nil {
		return nil, err
	}
	if !found || len(meta) == 0 {
		return nil, fmt.Errorf("%w: permanent metadata unavailable", ErrInvalidRecord)
	}
	raw, found, err := get(keys.KeyFrostPermanentArtifact(id))
	if err != nil {
		return nil, err
	}
	if !found {
		raw, found, err = get(keys.KeyArchiveFrostArtifact(id))
		if err != nil {
			return nil, err
		}
	}
	if !found {
		return nil, ErrRecordUnavailable
	}
	if maxEncodedBytes > 0 && len(raw) > maxEncodedBytes {
		return nil, ErrRecordTooLarge
	}
	record, err := UnmarshalRecord(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRecord, err)
	}
	canonical, err := MarshalMetadata(record.Metadata)
	if err != nil || record.Metadata.ArtifactID != id || !bytes.Equal(canonical, meta) {
		return nil, fmt.Errorf("%w: permanent metadata mismatch", ErrInvalidRecord)
	}
	return record, nil
}
