package artifact

import (
	"errors"
	"testing"

	"dex/keys"
)

func TestReadRecordUsesPermanentStateAndRejectsCorruptionWithoutFallback(t *testing.T) {
	record, err := NewRecord(Metadata{Kind: KindWithdraw, Chain: "ETH", SourceID: "job"}, []byte("calldata"))
	if err != nil {
		t.Fatal(err)
	}
	meta, _ := MarshalMetadata(record.Metadata)
	raw, _ := MarshalRecord(record)
	stateKey := keys.KeyFrostPermanentArtifact(record.Metadata.ArtifactID)
	archiveKey := keys.KeyArchiveFrostArtifact(record.Metadata.ArtifactID)
	for _, tc := range []struct {
		name    string
		state   []byte
		present bool
		limit   int
		want    error
	}{
		{name: "state-only", state: raw, present: true, limit: len(raw)},
		{name: "legacy-only", limit: len(raw)},
		{name: "empty-state", present: true, want: ErrInvalidRecord},
		{name: "corrupt-state", state: []byte("invalid"), present: true, want: ErrInvalidRecord},
		{name: "over-budget", state: raw, present: true, limit: len(raw) - 1, want: ErrRecordTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			archiveReads := 0
			get := func(key string) ([]byte, bool, error) {
				switch key {
				case keys.KeyFrostArtifactMeta(record.Metadata.ArtifactID):
					return meta, true, nil
				case stateKey:
					return tc.state, tc.present, nil
				case archiveKey:
					archiveReads++
					return raw, true, nil
				default:
					return nil, false, nil
				}
			}
			got, err := ReadRecord(get, record.Metadata.ArtifactID, tc.limit)
			if !errors.Is(err, tc.want) || (err == nil && got.Metadata.ArtifactID != record.Metadata.ArtifactID) {
				t.Fatalf("record=%+v err=%v want=%v", got, err, tc.want)
			}
			if tc.present && archiveReads != 0 {
				t.Fatal("read archive despite present permanent record")
			}
		})
	}
}
