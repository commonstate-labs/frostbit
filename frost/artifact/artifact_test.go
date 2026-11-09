package artifact

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRecordAndBundleRejectTamperedRawTransaction(t *testing.T) {
	parent, err := NewRecord(Metadata{Kind: KindWithdraw, Chain: "eth", Height: 1}, []byte{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	child, err := NewRecord(Metadata{Kind: KindMigration, Chain: "eth", Height: 2, ParentIDs: []string{parent.Metadata.ArtifactID}}, []byte{4, 5, 6})
	if err != nil {
		t.Fatal(err)
	}
	bundle := &Bundle{Version: Version, MigrationID: "migration-1", Artifacts: []Record{*parent, *child}}
	if err := ValidateBundle(bundle); err != nil {
		t.Fatalf("valid bundle rejected: %v", err)
	}
	tampered := *parent
	tampered.RawTx = []byte{9}
	if err := tampered.Validate(); err == nil {
		t.Fatal("tampered raw transaction was accepted")
	}
	if err := ValidateBundle(&Bundle{Version: Version, MigrationID: "migration-1", Artifacts: []Record{*child, *parent}}); err == nil {
		t.Fatal("child-before-parent bundle was accepted")
	}
}

func TestNoFundsBTCMigrationArtifactsBindSourceIdentity(t *testing.T) {
	a, err := NewRecord(Metadata{Kind: KindMigration, Chain: "btc", VaultID: 0, Epoch: 2, SourceID: "migration_btc_0_2_a"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewRecord(Metadata{Kind: KindMigration, Chain: "btc", VaultID: 0, Epoch: 3, SourceID: "migration_btc_0_3_b"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.Metadata.ArtifactID == b.Metadata.ArtifactID {
		t.Fatal("different empty migrations collided")
	}
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := b.Validate(); err != nil {
		t.Fatal(err)
	}
	a.Metadata.SourceID = b.Metadata.SourceID
	if err := a.Validate(); err == nil {
		t.Fatal("modified empty migration source accepted")
	}
	if _, err := NewRecord(Metadata{Kind: KindMigration, Chain: "btc"}, nil); err == nil {
		t.Fatal("missing migration source accepted")
	}
}

func TestDownloadMigrationPrefersReadyArchiveAndOnlyDownloads(t *testing.T) {
	record, err := NewRecord(Metadata{Kind: KindMigration, Chain: "eth", Height: 2}, []byte{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := MarshalBundle(&Bundle{Version: Version, MigrationID: "migration-1", Artifacts: []Record{*record}})
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/frost/archive/status":
			_ = json.NewEncoder(w).Encode(NodeStatus{ArchiveNode: true, Ready: true, CompleteThrough: 10, LatestHeight: 10})
		case "/frost/vault/migration/download":
			_, _ = w.Write(payload)
		default:
			http.Error(w, "unexpected path", http.StatusNotFound)
		}
	}))
	defer server.Close()

	bundle, source, err := DownloadMigration(context.Background(), server.Client(), []string{server.URL}, "migration-1")
	if err != nil {
		t.Fatal(err)
	}
	if source != server.URL || len(bundle.Artifacts) != 1 || !bytes.Equal(bundle.Artifacts[0].RawTx, record.RawTx) {
		t.Fatalf("unexpected download: source=%s bundle=%+v", source, bundle)
	}
	for _, path := range paths {
		if path != "/frost/archive/status" && path != "/frost/vault/migration/download" {
			t.Fatalf("download client invoked non-download path %s", path)
		}
	}
}
