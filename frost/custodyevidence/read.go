package custodyevidence

import (
	"bytes"
	"encoding/hex"
	"fmt"

	"dex/frost/artifact"
	"dex/keys"
	"google.golang.org/protobuf/proto"
)

func hashID(id string) bool {
	raw, err := hex.DecodeString(id)
	return err == nil && len(raw) == 32 && hex.EncodeToString(raw) == id
}

func required(get Get, key string) ([]byte, error) {
	raw, found, err := get(key)
	if err != nil {
		return nil, err
	}
	if !found || len(raw) == 0 {
		return nil, fmt.Errorf("missing permanent custody record %s", key)
	}
	return raw, nil
}

func readProto(get Get, key string, dst proto.Message) error {
	raw, err := required(get, key)
	if err != nil {
		return err
	}
	if err := proto.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("invalid permanent custody record %s: %w", key, err)
	}
	return nil
}

func permanentRecord(get Get, id string, metaRaw []byte) (*artifact.Record, error) {
	raw, err := required(get, keys.KeyFrostPermanentArtifact(id))
	if err != nil {
		return nil, err
	}
	if len(raw) > 16<<20 {
		return nil, fmt.Errorf("permanent artifact exceeds custody evidence budget")
	}
	record, err := artifact.UnmarshalRecord(raw)
	if err != nil {
		return nil, err
	}
	encoded, err := artifact.MarshalMetadata(record.Metadata)
	if err != nil || record.Metadata.ArtifactID != id || !bytes.Equal(metaRaw, encoded) {
		return nil, fmt.Errorf("permanent artifact metadata mismatch")
	}
	return record, nil
}
