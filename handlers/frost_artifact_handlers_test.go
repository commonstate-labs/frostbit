package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"dex/frost/artifact"
	"dex/keys"
	"dex/pb"
)

func TestAccountArtifactAPIUsesPermanentHeadAndRejectsArchiveMismatch(t *testing.T) {
	hm, dbm := newFrostQueryTestHandler(t)
	defer dbm.Close()
	enqueueProto(t, dbm, keys.KeyFrostVaultConfig("eth", 0), &pb.FrostVaultConfig{Chain: "eth", VaultCount: 2, EvmChainId: 1})
	r, err := artifact.NewRecord(artifact.Metadata{Kind: artifact.KindMigration, Chain: "ETH", VaultID: 1, Epoch: 3, SourceID: "migration"}, []byte("calldata"))
	if err != nil {
		t.Fatal(err)
	}
	storeDownloadTestArtifact(t, dbm, r)
	dbm.EnqueueSet(keys.KeyFrostVaultLastMigrationArtifact("eth", 1), r.Metadata.ArtifactID)
	enqueueProto(t, dbm, keys.KeyFrostVaultState("eth", 1), &pb.FrostVaultState{Chain: "eth", VaultId: 1, KeyEpoch: 3})
	enqueueProto(t, dbm, keys.KeyFrostVaultState("eth", 0), &pb.FrostVaultState{Chain: "eth", VaultId: 0, KeyEpoch: 1})
	if err := dbm.ForceFlush(); err != nil {
		t.Fatal(err)
	}
	get := func(path string, handler http.HandlerFunc) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		handler(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}
	w := get("/frost/vault/artifact/head?chain=ETH&vault_id=1", hm.HandleFrostArtifactHead)
	var head map[string]string
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &head) != nil || head["artifact_id"] != r.Metadata.ArtifactID {
		t.Fatalf("head %d %s", w.Code, w.Body)
	}
	w = get("/frost/vault/artifact/head?chain=eth&vault_id=0", hm.HandleFrostArtifactHead)
	if w.Code != 200 {
		t.Fatalf("empty head %d %s", w.Code, w.Body)
	}
	path := "/frost/artifact?artifact_id=" + r.Metadata.ArtifactID
	w = get(path, hm.HandleFrostArtifact)
	if w.Code != 200 {
		t.Fatalf("record %d %s", w.Code, w.Body)
	}
	r.Metadata.Epoch++
	raw, _ := artifact.MarshalRecord(r)
	dbm.EnqueueSet(keys.KeyArchiveFrostArtifact(r.Metadata.ArtifactID), string(raw))
	_ = dbm.ForceFlush()
	w = get(path, hm.HandleFrostArtifact)
	if w.Code != 500 {
		t.Fatalf("tampered metadata accepted %d %s", w.Code, w.Body)
	}
	dbm.EnqueueDelete(keys.KeyArchiveFrostArtifact(r.Metadata.ArtifactID))
	_ = dbm.ForceFlush()
	w = get(path, hm.HandleFrostArtifact)
	if w.Code != 410 {
		t.Fatalf("pruned must demand archive %d %s", w.Code, w.Body)
	}
	w = get(path+"&metadata_only=1", hm.HandleFrostArtifact)
	var metadata artifact.Metadata
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &metadata) != nil || metadata.ArtifactID != r.Metadata.ArtifactID || metadata.Epoch != 3 {
		t.Fatalf("permanent metadata unavailable after raw pruning %d %s", w.Code, w.Body)
	}
	w = get("/frost/artifact?artifact_id=../../private", hm.HandleFrostArtifact)
	if w.Code != 400 {
		t.Fatalf("invalid id %d", w.Code)
	}
	                                                                      
	r.Metadata.Epoch = 3
	raw, _ = artifact.MarshalRecord(r)
	dbm.EnqueueSet(keys.KeyFrostPermanentArtifact(r.Metadata.ArtifactID), string(raw))
	if err := dbm.ForceFlush(); err != nil {
		t.Fatal(err)
	}
	w = get(path, hm.HandleFrostArtifact)
	if w.Code != http.StatusOK {
		t.Fatalf("permanent artifact unavailable %d %s", w.Code, w.Body)
	}
	dbm.EnqueueSet(keys.KeyArchiveFrostArtifact(r.Metadata.ArtifactID), string(raw))
	dbm.EnqueueSet(keys.KeyFrostPermanentArtifact(r.Metadata.ArtifactID), "invalid")
	if err := dbm.ForceFlush(); err != nil {
		t.Fatal(err)
	}
	w = get(path, hm.HandleFrostArtifact)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("corrupt state hidden by archive %d %s", w.Code, w.Body)
	}
}
