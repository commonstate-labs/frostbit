package keys

import "testing"

func TestWithdrawArtifactLookupIsArchiveOnly(t *testing.T) {
	key := KeyFrostWithdrawArtifact("withdraw-id")
	if IsStatefulKey(key) || StorageClassForKey(key) != StorageArchive || IsPrunableQueryHistoryKey(key) {
		t.Fatal("withdraw artifact lookup must stay in the FROST archive store, outside execution state and query pruning")
	}
	if !IsStatefulKey(KeyFrostVaultLastMigrationArtifact("eth", 0)) {
		t.Fatal("maintenance head must survive StateDB snapshot recovery")
	}
}

func TestPermanentFrostDeliveryKeysAreStateAndNeverHistory(t *testing.T) {
	for _, key := range []string{
		KeyFrostPermanentArtifact("artifact"), KeyFrostPermanentWithdraw("withdraw"),
		KeyFrostPermanentWithdrawJob("job"), KeyFrostPermanentSignedPackage("job", 0),
		KeyFrostPermanentMigration("migration"), KeyFrostPermanentWithdrawArtifact("withdraw"),
		KeyFrostWithdrawTxRef("request"),
	} {
		if !IsStatefulKey(key) || IsPrunableQueryHistoryKey(key) || PrunableArchiveHistoryPrefix(key) != "" {
			t.Fatalf("permanent delivery data can escape StateDB or be pruned: %s", key)
		}
	}
}
