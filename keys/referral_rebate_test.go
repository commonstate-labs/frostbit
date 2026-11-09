package keys

import "testing"

func TestReferralRebateRecordIsPrunableQueryData(t *testing.T) {
	key := KeyReferralRebateRecord("referrer", 42, "tx-42", 3)
	if IsStatefulKey(key) {
		t.Fatalf("referral rebate reverse index must not affect consensus state: %s", key)
	}
	if class := StorageClassForKey(key); class != StorageQuery {
		t.Fatalf("storage class=%v want=%v", class, StorageQuery)
	}
	if !IsPrunableQueryHistoryKey(key) {
		t.Fatalf("referral rebate reverse index must follow query-history retention: %s", key)
	}
}

func TestReferralInviteeReverseIndexIsRebuildableQueryData(t *testing.T) {
	key := KeyReferralInvitee("referrer", "invitee")
	if IsStatefulKey(key) || StorageClassForKey(key) != StorageQuery || IsPrunableQueryHistoryKey(key) {
		t.Fatalf("current referral reverse index must be query-only and retained until unbound: %s", key)
	}
	if !IsStatefulKey(KeyReferrer("invitee")) {
		t.Fatal("forward referral binding must remain consensus state")
	}
}

func TestTradingFeeRecordKeyIncludesTransaction(t *testing.T) {
	first := KeyTradingFeeRecord("trader", 42, "tx-1", 0)
	second := KeyTradingFeeRecord("trader", 42, "tx-2", 0)
	if first == second {
		t.Fatal("trading fee records from the same account and height must not collide")
	}
}
