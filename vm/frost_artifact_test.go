package vm

import (
	"bytes"
	"testing"

	"dex/frost/artifact"
	"dex/keys"
)

func TestAccountMigrationHeadsPersistWithoutWithdrawalDependencies(t *testing.T) {
	base := map[string][]byte{}
	sv := newMemoryStateView(base)
	store := func(kind, name string, vault uint32, epoch uint64, raw byte) *artifact.Record {
		r, err := storeFrostArtifact(sv, artifact.Metadata{Kind: kind, Chain: name, VaultID: vault, Epoch: epoch, Height: uint64(raw), SourceID: "test-source"}, []byte{raw})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	withdrawA := store(artifact.KindWithdraw, "eth", 0, 1, 1)
	withdrawB := store(artifact.KindWithdraw, "eth", 0, 1, 2)
	for _, r := range []*artifact.Record{withdrawA, withdrawB} {
		if len(r.Metadata.ParentIDs) != 0 {
			t.Fatal("ordinary withdrawals inherited execution dependencies")
		}
	}
	if _, found, _ := sv.Get(keys.KeyFrostVaultLastMigrationArtifact("eth", 0)); found {
		t.Fatal("ordinary withdrawal created a maintenance head")
	}
	a := store(artifact.KindMigration, "eth", 0, 2, 3)
	b := store(artifact.KindMigration, "eth", 1, 2, 4)
	c := store(artifact.KindMigration, "trx", 0, 2, 5)
	for _, r := range []*artifact.Record{a, b, c} {
		if len(r.Metadata.ParentIDs) != 0 {
			t.Fatal("independent vault inherited another vault's artifact")
		}
	}
	for _, op := range sv.Diff() {
		if op.Del {
			delete(base, op.Key)
		} else {
			base[op.Key] = append([]byte(nil), op.Value...)
		}
	}
	sv = newMemoryStateView(base)
	lateWithdrawal := store(artifact.KindWithdraw, "eth", 0, 2, 6)
	if len(lateWithdrawal.Metadata.ParentIDs) != 0 {
		t.Fatal("new-epoch withdrawal is not independently executable")
	}
	next := store(artifact.KindMigration, "eth", 0, 3, 7)
	if len(next.Metadata.ParentIDs) != 1 || next.Metadata.ParentIDs[0] != a.Metadata.ArtifactID {
		t.Fatal("restarted view lost the preceding key-change dependency")
	}
	for _, r := range []*artifact.Record{next, b, c} {
		raw, found, err := sv.Get(keys.KeyFrostVaultLastMigrationArtifact(r.Metadata.Chain, r.Metadata.VaultID))
		if err != nil || !found || string(raw) != r.Metadata.ArtifactID {
			t.Fatalf("wrong isolated head: %s/%d %s %v", r.Metadata.Chain, r.Metadata.VaultID, raw, err)
		}
	}
	if _, found, _ := sv.Get(keys.KeyFrostVaultLastArtifact("eth", 0)); found {
		t.Fatal("new account artifacts updated the legacy mixed head")
	}
	for _, source := range []string{"migration_btc_0_2_a", "migration_btc_0_3_b"} {
		if _, err := storeFrostArtifact(sv, artifact.Metadata{Kind: artifact.KindMigration, Chain: "btc", VaultID: 0, Epoch: 2, Height: 5, SourceID: source}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, found, _ := sv.Get(keys.KeyFrostVaultLastMigrationArtifact("btc", 0)); found {
		t.Fatal("BTC empty artifacts must not enter account artifact chain")
	}
}

func TestPermanentArtifactRejectsArchiveOnlyParentWithoutWrites(t *testing.T) {
	base := map[string][]byte{}
	sv := newMemoryStateView(base)
	parent, err := storeFrostArtifact(sv, artifact.Metadata{Kind: artifact.KindMigration, Chain: "eth", VaultID: 0, Epoch: 2, Height: 1, SourceID: "migration-2"}, []byte{1})
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range sv.Diff() {
		if !op.Del {
			base[op.Key] = append([]byte(nil), op.Value...)
		}
	}
	permanentKey := keys.KeyFrostPermanentArtifact(parent.Metadata.ArtifactID)
	validParent := append([]byte(nil), base[permanentKey]...)
	for _, badParent := range [][]byte{nil, []byte("corrupt")} {
		if badParent == nil {
			delete(base, permanentKey)
		} else {
			base[permanentKey] = badParent
		}
		attempt := newMemoryStateView(base)
		if _, err := storeFrostArtifact(attempt, artifact.Metadata{Kind: artifact.KindMigration, Chain: "eth", VaultID: 0, Epoch: 3, Height: 2, SourceID: "migration-3"}, []byte{2}); err == nil {
			t.Fatal("archive-only/corrupt parent allowed a new permanent child")
		}
		if len(attempt.Diff()) != 0 {
			t.Fatal("failed dependency validation wrote child state")
		}
	}
	base[permanentKey] = validParent
	child, err := storeFrostArtifact(newMemoryStateView(base), artifact.Metadata{Kind: artifact.KindMigration, Chain: "eth", VaultID: 0, Epoch: 3, Height: 2, SourceID: "migration-3"}, []byte{2})
	if err != nil || len(child.Metadata.ParentIDs) != 1 || child.Metadata.ParentIDs[0] != parent.Metadata.ArtifactID {
		t.Fatalf("valid permanent parent not accepted: child=%+v err=%v", child, err)
	}
}

func assertPermanentFrostCopy(t *testing.T, sv StateView, permanent, legacy string) {
	t.Helper()
	data, found, err := sv.Get(permanent)
	if err != nil || !found || len(data) == 0 || !keys.IsStatefulKey(permanent) {
		t.Fatalf("missing permanent delivery data %s: %v", permanent, err)
	}
	old, found, err := sv.Get(legacy)
	if err != nil || !found || !bytes.Equal(data, old) {
		t.Fatalf("permanent data differs from executed delivery at %s", permanent)
	}
}

func assertPermanentWithdrawalDelivery(t *testing.T, sv StateView, withdrawID, jobID string) {
	t.Helper()
	assertPermanentFrostCopy(t, sv, keys.KeyFrostPermanentWithdraw(withdrawID), keys.KeyArchiveFrostWithdraw(withdrawID))
	assertPermanentFrostCopy(t, sv, keys.KeyFrostPermanentWithdrawJob(jobID), keys.KeyArchiveFrostWithdrawJob(jobID))
	assertPermanentFrostCopy(t, sv, keys.KeyFrostPermanentSignedPackage(jobID, 0), keys.KeyArchiveFrostSignedPackage(jobID, 0))
	assertPermanentFrostCopy(t, sv, keys.KeyFrostPermanentWithdrawArtifact(withdrawID), keys.KeyFrostWithdrawArtifact(withdrawID))
	id, _, _ := sv.Get(keys.KeyFrostPermanentWithdrawArtifact(withdrawID))
	assertPermanentFrostCopy(t, sv, keys.KeyFrostPermanentArtifact(string(id)), keys.KeyArchiveFrostArtifact(string(id)))
}

func assertPermanentMigrationDelivery(t *testing.T, sv StateView, migrationID string) {
	t.Helper()
	assertPermanentFrostCopy(t, sv, keys.KeyFrostPermanentMigration(migrationID), keys.KeyArchiveFrostMigration(migrationID))
	id, found, err := sv.Get(keys.KeyFrostMigrationArtifact(migrationID))
	if err != nil || !found {
		t.Fatalf("missing migration artifact: %v", err)
	}
	assertPermanentFrostCopy(t, sv, keys.KeyFrostPermanentArtifact(string(id)), keys.KeyArchiveFrostArtifact(string(id)))
}

func TestAccountArtifactCannotCarryCallerSelectedParents(t *testing.T) {
	for _, kind := range []string{artifact.KindWithdraw, artifact.KindMigration} {
		sv := newMemoryStateView(map[string][]byte{})
		_, err := storeFrostArtifact(sv, artifact.Metadata{Kind: kind, Chain: "eth", Epoch: 2, ParentIDs: []string{"withdraw-parent"}}, []byte{1})
		if err == nil || len(sv.Diff()) != 0 {
			t.Fatalf("%s accepted caller-selected execution dependencies", kind)
		}
	}
}
