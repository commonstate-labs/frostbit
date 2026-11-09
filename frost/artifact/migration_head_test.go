package artifact

import (
	"errors"
	"testing"

	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

func TestResolveAccountMigrationHeadFromLegacyConsensusMarker(t *testing.T) {
	meta := Metadata{Version: Version, ArtifactID: "migration-artifact", Kind: KindMigration, Chain: "ETH", VaultID: 3, Epoch: 7}
	metaRaw, _ := MarshalMetadata(meta)
	marker, _ := proto.Marshal(&pb.FrostVaultMigrationState{MigrationId: "migration-id", Chain: "eth", VaultId: 3, NewEpoch: 7, Status: "SIGNED"})
	state := map[string][]byte{
		keys.KeyFrostVaultMigration("eth", 3, 7):       marker,
		keys.KeyFrostMigrationArtifact("migration-id"): []byte(meta.ArtifactID),
		keys.KeyFrostArtifactMeta(meta.ArtifactID):     metaRaw,
		keys.KeyFrostVaultLastArtifact("eth", 3):       []byte("unclaimed-withdrawal"),
	}
	reads := 0
	get := func(key string) ([]byte, bool, error) {
		reads++
		if key == keys.KeyFrostVaultLastArtifact("eth", 3) || key == keys.KeyFrostArtifactMeta("unclaimed-withdrawal") {
			t.Fatal("migration recovery traversed old user withdrawals")
		}
		raw, found := state[key]
		return raw, found, nil
	}
	id, err := ResolveAccountMigrationHead(get, " ETH ", 3, 7)
	if err != nil || id != meta.ArtifactID || reads != 4 {
		t.Fatalf("legacy migration recovery: id=%q reads=%d err=%v", id, reads, err)
	}
	delete(state, keys.KeyFrostMigrationArtifact("migration-id"))
	if _, err := ResolveAccountMigrationHead(get, "eth", 3, 7); err == nil {
		t.Fatal("missing permanent migration reference was treated as genesis")
	}
}

func TestResolveAccountMigrationHeadRejectsMalformedOrWrongScope(t *testing.T) {
	for _, tc := range []struct {
		name string
		meta Metadata
	}{
		{"withdrawal", Metadata{Version: Version, ArtifactID: "id", Kind: KindWithdraw, Chain: "ETH", VaultID: 3, Epoch: 7}},
		{"chain", Metadata{Version: Version, ArtifactID: "id", Kind: KindMigration, Chain: "TRX", VaultID: 3, Epoch: 7}},
		{"vault", Metadata{Version: Version, ArtifactID: "id", Kind: KindMigration, Chain: "ETH", VaultID: 4, Epoch: 7}},
		{"epoch", Metadata{Version: Version, ArtifactID: "id", Kind: KindMigration, Chain: "ETH", VaultID: 3, Epoch: 6}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := MarshalMetadata(tc.meta)
			state := map[string][]byte{keys.KeyFrostVaultLastMigrationArtifact("eth", 3): []byte("id"), keys.KeyFrostArtifactMeta("id"): raw}
			get := func(key string) ([]byte, bool, error) { raw, found := state[key]; return raw, found, nil }
			if _, err := ResolveAccountMigrationHead(get, "eth", 3, 7); err == nil {
				t.Fatal("invalid head accepted")
			}
		})
	}
	missing := func(string) ([]byte, bool, error) { return nil, false, nil }
	if id, err := ResolveAccountMigrationHead(missing, "eth", 0, 1); err != nil || id != "" {
		t.Fatalf("genesis head must be empty: %q %v", id, err)
	}
	readErr := errors.New("state unavailable")
	if _, err := ResolveAccountMigrationHead(func(string) ([]byte, bool, error) { return nil, false, readErr }, "eth", 0, 1); !errors.Is(err, readErr) {
		t.Fatalf("storage error was suppressed: %v", err)
	}
}
