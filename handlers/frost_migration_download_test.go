package handlers

import (
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"dex/frost/artifact"
	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

func storeDownloadTestArtifact(t *testing.T, dbManager interface {
	EnqueueSet(string, string)
}, record *artifact.Record) {
	t.Helper()
	metadata, err := artifact.MarshalMetadata(record.Metadata)
	if err != nil {
		t.Fatal(err)
	}
	fullRecord, err := artifact.MarshalRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	dbManager.EnqueueSet(keys.KeyFrostArtifactMeta(record.Metadata.ArtifactID), string(metadata))
	dbManager.EnqueueSet(keys.KeyArchiveFrostArtifact(record.Metadata.ArtifactID), string(fullRecord))
}

func TestHandleDownloadVaultMigrationOrdersDependenciesAndReportsPrunedRaw(t *testing.T) {
	hm, dbManager := newFrostQueryTestHandler(t)
	defer dbManager.Close()

	parent, err := artifact.NewRecord(artifact.Metadata{
		Kind: artifact.KindWithdraw, Chain: "ETH", SourceID: "withdraw-1",
	}, []byte("parent-calldata"))
	if err != nil {
		t.Fatal(err)
	}
	root, err := artifact.NewRecord(artifact.Metadata{
		Kind: artifact.KindMigration, Chain: "ETH", SourceID: "migration-1",
		ParentIDs: []string{parent.Metadata.ArtifactID},
	}, []byte("migration-calldata"))
	if err != nil {
		t.Fatal(err)
	}
	storeDownloadTestArtifact(t, dbManager, parent)
	storeDownloadTestArtifact(t, dbManager, root)
	dbManager.EnqueueSet(keys.KeyFrostMigrationArtifact("migration-1"), root.Metadata.ArtifactID)
	if err := dbManager.ForceFlush(); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	hm.HandleDownloadVaultMigration(recorder, httptest.NewRequest(http.MethodGet,
		"/frost/vault/migration/download?migration_id=migration-1", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("download status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	bundle, err := artifact.UnmarshalBundle(recorder.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Artifacts) != 2 ||
		bundle.Artifacts[0].Metadata.ArtifactID != parent.Metadata.ArtifactID ||
		bundle.Artifacts[1].Metadata.ArtifactID != root.Metadata.ArtifactID {
		t.Fatalf("bundle is not parent-before-child: %+v", bundle.Artifacts)
	}

	dbManager.EnqueueDelete(keys.KeyArchiveFrostArtifact(parent.Metadata.ArtifactID))
	if err := dbManager.ForceFlush(); err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	hm.HandleDownloadVaultMigration(recorder, httptest.NewRequest(http.MethodGet,
		"/frost/vault/migration/download?migration_id=migration-1", nil))
	if recorder.Code != http.StatusGone {
		t.Fatalf("pruned dependency status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	                                                                          
	for _, record := range []*artifact.Record{parent, root} {
		raw, _ := artifact.MarshalRecord(record)
		dbManager.EnqueueSet(keys.KeyFrostPermanentArtifact(record.Metadata.ArtifactID), string(raw))
		dbManager.EnqueueDelete(keys.KeyArchiveFrostArtifact(record.Metadata.ArtifactID))
	}
	if err := dbManager.ForceFlush(); err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	hm.HandleDownloadVaultMigration(recorder, httptest.NewRequest(http.MethodGet,
		"/frost/vault/migration/download?migration_id=migration-1", nil))
	bundle, err = artifact.UnmarshalBundle(recorder.Body.Bytes())
	if recorder.Code != http.StatusOK || err != nil || len(bundle.Artifacts) != 2 || bundle.Artifacts[0].Metadata.ArtifactID != parent.Metadata.ArtifactID {
		t.Fatalf("permanent bundle status=%d body=%s err=%v", recorder.Code, recorder.Body.String(), err)
	}

	                                                                 
	storeDownloadTestArtifact(t, dbManager, parent)
	dbManager.EnqueueSet(keys.KeyFrostPermanentArtifact(parent.Metadata.ArtifactID), "invalid")
	if err := dbManager.ForceFlush(); err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	hm.HandleDownloadVaultMigration(recorder, httptest.NewRequest(http.MethodGet,
		"/frost/vault/migration/download?migration_id=migration-1", nil))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("damaged state dependency status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestVaultMigrationQueryUsesPermanentReceiptWithoutArchive(t *testing.T) {
	hm, dbManager := newFrostQueryTestHandler(t)
	defer dbManager.Close()
	state := &pb.FrostVaultMigrationState{MigrationId: "migration-state", Chain: "ETH", OldEpoch: 1, NewEpoch: 2, Status: "SIGNED", TemplateData: []byte("template")}
	record, err := artifact.NewRecord(artifact.Metadata{Kind: artifact.KindMigration, Chain: "ETH", SourceID: state.MigrationId}, []byte("calldata"))
	if err != nil {
		t.Fatal(err)
	}
	metadata, _ := artifact.MarshalMetadata(record.Metadata)
	raw, _ := artifact.MarshalRecord(record)
	dbManager.EnqueueSet(keys.KeyFrostArtifactMeta(record.Metadata.ArtifactID), string(metadata))
	dbManager.EnqueueSet(keys.KeyFrostPermanentArtifact(record.Metadata.ArtifactID), string(raw))
	dbManager.EnqueueSet(keys.KeyFrostMigrationArtifact(state.MigrationId), record.Metadata.ArtifactID)
	archive := &pb.FrostVaultMigrationArchive{State: state, Signature: []byte("signature")}
	enqueueProto(t, dbManager, keys.KeyFrostPermanentMigration(state.MigrationId), archive)
	if err := dbManager.ForceFlush(); err != nil {
		t.Fatal(err)
	}
	query := func() *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		hm.HandleGetVaultMigration(r, httptest.NewRequest(http.MethodGet, "/frost/vault/migration?migration_id="+state.MigrationId, nil))
		return r
	}
	r := query()
	var got pb.FrostVaultMigrationArchive
	if r.Code != http.StatusOK || proto.Unmarshal(r.Body.Bytes(), &got) != nil || string(got.RawTx) != "calldata" || string(got.Signature) != "signature" {
		t.Fatalf("migration status=%d body=%s", r.Code, r.Body.String())
	}
	enqueueProto(t, dbManager, keys.KeyArchiveFrostMigration(state.MigrationId), archive)
	dbManager.EnqueueSet(keys.KeyFrostPermanentMigration(state.MigrationId), "invalid")
	if err := dbManager.ForceFlush(); err != nil {
		t.Fatal(err)
	}
	if r := query(); r.Code != http.StatusInternalServerError {
		t.Fatalf("corrupt permanent migration status=%d body=%s", r.Code, r.Body.String())
	}
}

func TestVaultDepositKeySwitchesOnlyAfterSignedVaultUpdate(t *testing.T) {
	hm, dbManager := newFrostQueryTestHandler(t)
	defer dbManager.Close()
	oldKey := []byte{1, 2, 3}
	newKey := []byte{4, 5, 6}
	enqueueProto(t, dbManager, keys.KeyFrostVaultState("BTC", 7), &pb.FrostVaultState{
		Chain: "BTC", VaultId: 7, KeyEpoch: 4, GroupPubkey: oldKey, Status: "ACTIVE",
	})
	enqueueProto(t, dbManager, keys.KeyFrostVaultTransition("BTC", 7, 5), &pb.VaultTransitionState{
		Chain: "BTC", VaultId: 7, EpochId: 5, NewGroupPubkey: newKey,
		MigrationStatus: "INTENT_FINALIZED",
	})
	if err := dbManager.ForceFlush(); err != nil {
		t.Fatal(err)
	}

	readDepositKey := func() *pb.GetVaultGroupPubKeyResponse {
		t.Helper()
		recorder := httptest.NewRecorder()
		hm.HandleGetVaultGroupPubKey(recorder, httptest.NewRequest(http.MethodGet,
			"/frost/vault/group_pubkey?chain=BTC&vault_id=7", nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("group key status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		var response pb.GetVaultGroupPubKeyResponse
		if err := proto.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return &response
	}
	beforeSigned := readDepositKey()
	if beforeSigned.DepositKeyEpoch != 4 || beforeSigned.DepositGroupPubkey != hex.EncodeToString(oldKey) {
		t.Fatalf("intent switched deposit key early: %+v", beforeSigned)
	}

	enqueueProto(t, dbManager, keys.KeyFrostVaultState("BTC", 7), &pb.FrostVaultState{
		Chain: "BTC", VaultId: 7, KeyEpoch: 5, GroupPubkey: newKey, Status: "ACTIVE",
	})
	if err := dbManager.ForceFlush(); err != nil {
		t.Fatal(err)
	}
	afterSigned := readDepositKey()
	if afterSigned.DepositKeyEpoch != 5 || afterSigned.DepositGroupPubkey != hex.EncodeToString(newKey) {
		t.Fatalf("signed vault did not switch deposit key: %+v", afterSigned)
	}
}
