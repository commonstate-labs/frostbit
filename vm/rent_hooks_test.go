package vm

import (
	"dex/future"
	"dex/keys"
	"dex/pb"
	"dex/rent"
	"math/big"
	"testing"

	"google.golang.org/protobuf/proto"
)

func TestRentSweepRecyclesFuturePositionMargin(t *testing.T) {
	sv := NewMockStateView()
	owner := "fbc1qfutureowner0000000000000000000000000000"
	pos := &future.Position{
		ID: "pos_rent_1", Owner: owner, Pair: "BTC_FB", Side: future.Long,
		Margin: big.NewInt(500), Status: future.StatusOpen,
	}
	if err := (&svPositionStore{sv: sv}).SetPosition(pos); err != nil {
		t.Fatalf("set position: %v", err)
	}
	claimKey := keys.KeyAssetClaim(owner, FeeToken, rentClaimFuturePositionMargin, pos.ID)
	if _, exists, _ := sv.Get(claimKey); !exists {
		t.Fatalf("future margin claim was not written")
	}
	summary, err := rent.NewService(rentStateViewStore{sv: sv}).NominalFBAsset(owner)
	if err != nil {
		t.Fatalf("nominal FB asset: %v", err)
	}
	if summary.IndexedClaims != "500" || summary.Total != "500" {
		t.Fatalf("nominal FB indexed=%s total=%s, want 500/500", summary.IndexedClaims, summary.Total)
	}
	sv.Del(keys.KeyFutureOwnerPosition(owner, pos.ID))
	if err := (vmRentCleaner{sv: sv}).sweepFuturePositions(owner); err != nil {
		t.Fatalf("sweep future positions: %v", err)
	}
	if _, exists, _ := sv.Get(keys.KeyFuturePosition(pos.ID)); exists {
		t.Fatalf("future position was not deleted")
	}
	if _, exists, _ := sv.Get(claimKey); exists {
		t.Fatalf("future margin claim was not deleted")
	}
	if _, exists, _ := sv.Get(keys.KeyAssetClaimTotal(owner, FeeToken)); exists {
		t.Fatalf("future margin claim total was not deleted")
	}
	if got := GetBalance(sv, rent.RecycleFundAccount, FeeToken).Balance; got != "500" {
		t.Fatalf("recycle FB balance = %s, want 500", got)
	}
}

func TestRentSweepRecyclesChallengeContribution(t *testing.T) {
	sv := NewMockStateView()
	owner := "fbc1qchallenger000000000000000000000000000"
	challengeID := "challenge_rent_1"
	txID := "challenge_tx_1"

	rec := &pb.ChallengeRecord{ChallengeId: challengeID, StakeAmount: "300", Status: pb.ChallengeStatus_CHALLENGE_ACTIVE}
	recData, err := proto.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal challenge: %v", err)
	}
	sv.Set(keys.KeyChallengeRecord(challengeID), recData)

	contrib := &pb.WitnessChallengeTx{Base: &pb.BaseMessage{TxId: txID, FromAddress: owner}, StakeAmount: "200"}
	contribData, err := proto.Marshal(contrib)
	if err != nil {
		t.Fatalf("marshal contribution: %v", err)
	}
	sv.Set(keys.KeyChallengeContribution(challengeID, txID), contribData)
	if err := syncChallengeContributionRentClaim(sv, contrib, challengeID, big.NewInt(200)); err != nil {
		t.Fatal(err)
	}

	claimKey := keys.KeyAssetClaim(owner, FeeToken, rentClaimChallengeStake, rentChallengeContributionSourceID(challengeID, txID))
	if _, exists, _ := sv.Get(claimKey); !exists {
		t.Fatalf("challenge contribution claim was not written")
	}
	if data, exists, _ := sv.Get(keys.KeyAssetClaimTotal(owner, FeeToken)); !exists || string(data) != "200" {
		t.Fatalf("challenge contribution claim total = %q exists=%v, want 200/true", string(data), exists)
	}
	if err := (vmRentCleaner{sv: sv}).sweepChallengeContributions(owner); err != nil {
		t.Fatalf("sweep challenge contributions: %v", err)
	}
	if got := GetBalance(sv, rent.RecycleFundAccount, FeeToken).Balance; got != "200" {
		t.Fatalf("recycle FB balance = %s, want 200", got)
	}
	if _, exists, _ := sv.Get(keys.KeyChallengeContribution(challengeID, txID)); exists {
		t.Fatalf("challenge contribution was not deleted")
	}
	if _, exists, _ := sv.Get(claimKey); exists {
		t.Fatalf("challenge contribution claim was not deleted")
	}
	if _, exists, _ := sv.Get(keys.KeyAssetClaimTotal(owner, FeeToken)); exists {
		t.Fatalf("challenge contribution claim total was not deleted")
	}
	data, exists, _ := sv.Get(keys.KeyChallengeRecord(challengeID))
	if !exists {
		t.Fatalf("challenge record missing")
	}
	var updated pb.ChallengeRecord
	if err := proto.Unmarshal(data, &updated); err != nil {
		t.Fatalf("parse updated challenge: %v", err)
	}
	if updated.StakeAmount != "100" {
		t.Fatalf("challenge stake = %s, want 100", updated.StakeAmount)
	}
}

func TestRentSweepClearsReferralBinding(t *testing.T) {
	sv := NewMockStateView()
	owner, referrer := "fbc1qinvitee", "fbc1qreferrer"
	sv.Set(keys.KeyReferrer(owner), []byte(referrer))
	sv.Set(keys.KeyReferralInvitee(referrer, owner), []byte{1})

	if err := (vmRentCleaner{sv: sv}).clearReferralBinding(owner); err != nil {
		t.Fatal(err)
	}
	if _, exists, _ := sv.Get(keys.KeyReferrer(owner)); exists {
		t.Fatal("forward referral binding was not deleted")
	}
	if _, exists, _ := sv.Get(keys.KeyReferralInvitee(referrer, owner)); exists {
		t.Fatal("reverse referral binding was not deleted")
	}
}

func TestStateViewSetClaimRetainsMarkers(t *testing.T) {
	sv := NewMockStateView()
	owner := "fbc1qclaimcleanup000000000000000000000000"
	if err := rent.SetClaim(sv, owner, FeeToken, rentClaimFuturePositionMargin, "pos1", big.NewInt(10)); err != nil {
		t.Fatal(err)
	}
	if _, exists, _ := sv.Get(keys.KeyAssetClaimMarker(owner, FeeToken)); !exists {
		t.Fatal("asset claim marker should be set")
	}
	if err := rent.SetClaim(sv, owner, FeeToken, rentClaimFuturePositionMargin, "pos1", big.NewInt(0)); err != nil {
		t.Fatal(err)
	}
	if _, exists, _ := sv.Get(keys.KeyAssetClaimMarker(owner, FeeToken)); !exists {
		t.Fatal("asset claim marker should be conservatively retained")
	}
	if _, exists, _ := sv.Get(keys.KeyRentOwnerMarker(owner, rent.OwnerMarkerClaim)); !exists {
		t.Fatal("owner claim marker should be conservatively retained")
	}
	if _, exists, _ := sv.Get(keys.KeyAssetClaimTotal(owner, FeeToken)); exists {
		t.Fatal("claim total should be deleted")
	}
}

func TestClearFinalizedChallengeContributionRetainsClaimMarkers(t *testing.T) {
	sv := NewMockStateView()
	owner := "fbc1qchallengecleanup0000000000000000000000"
	challengeID := "challenge_cleanup"
	txID := "challenge_cleanup_tx"
	contrib := &pb.WitnessChallengeTx{Base: &pb.BaseMessage{TxId: txID, FromAddress: owner}, StakeAmount: "50"}
	data, err := proto.Marshal(contrib)
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyChallengeContribution(challengeID, txID), data)
	if err := syncChallengeContributionRentClaim(sv, contrib, challengeID, big.NewInt(50)); err != nil {
		t.Fatal(err)
	}
	clearFinalizedChallengeContributionState(sv, challengeID)
	if _, exists, _ := sv.Get(keys.KeyAssetClaimMarker(owner, FeeToken)); !exists {
		t.Fatal("asset claim marker should be conservatively retained after finalized challenge cleanup")
	}
	if _, exists, _ := sv.Get(keys.KeyRentOwnerMarker(owner, rent.OwnerMarkerClaim)); !exists {
		t.Fatal("owner claim marker should be conservatively retained after finalized challenge cleanup")
	}
	if _, exists, _ := sv.Get(keys.KeyAssetClaimTotal(owner, FeeToken)); exists {
		t.Fatal("claim total should be deleted after finalized challenge cleanup")
	}
}

func TestSetBalanceMarksPositiveNonFBOnly(t *testing.T) {
	sv := NewMockStateView()
	owner := "fbc1qbalancecleanup00000000000000000000000"
	SetBalance(sv, owner, "USDT", &pb.TokenBalance{Balance: "7"})
	SetBalance(sv, owner, "BTC", &pb.TokenBalance{Balance: "3"})
	if _, exists, _ := sv.Get(keys.KeyRentOwnerMarker(owner, rent.OwnerMarkerNonFBBalance)); !exists {
		t.Fatal("non-FB marker should be set")
	}
	SetBalance(sv, owner, "USDT", &pb.TokenBalance{Balance: "0"})
	if _, exists, _ := sv.Get(keys.KeyBalance(owner, "USDT")); exists {
		t.Fatal("zero USDT balance key should be deleted")
	}
	if _, exists, _ := sv.Get(keys.KeyRentOwnerMarker(owner, rent.OwnerMarkerNonFBBalance)); !exists {
		t.Fatal("non-FB marker should remain while BTC balance exists")
	}
	SetBalance(sv, owner, "BTC", &pb.TokenBalance{Balance: "0"})
	if _, exists, _ := sv.Get(keys.KeyBalance(owner, "BTC")); exists {
		t.Fatal("zero BTC balance key should be deleted")
	}
	SetBalance(sv, owner, "ETH", &pb.TokenBalance{Balance: "0", OrderFrozenBalance: "4"})
	if _, exists, _ := sv.Get(keys.KeyBalance(owner, "ETH")); !exists {
		t.Fatal("balance key with frozen bucket should be retained")
	}
	if got := GetBalance(sv, owner, "USDT").Balance; got != "0" {
		t.Fatalf("missing balance key should read as zero, got %s", got)
	}
}

func TestSpotOrderMarkerRetainsOwnerMarkerWithLastOpenOrder(t *testing.T) {
	sv := NewMockStateView()
	owner := "fbc1qspotcleanup0000000000000000000000000"
	open := &pb.OrderState{
		OrderId: "spot_open_1", Owner: owner, BaseToken: "BTC", QuoteToken: "FB",
		Status: pb.OrderStateStatus_ORDER_OPEN, CreateHeight: 1,
	}
	data, err := proto.Marshal(open)
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyOrderState(open.OrderId), data)
	syncSpotAccountOrderIndex(sv, open, data)
	if _, exists, _ := sv.Get(keys.KeyRentOwnerMarker(owner, rent.OwnerMarkerSpotOrder)); !exists {
		t.Fatal("spot order marker should be set")
	}
	closed := proto.Clone(open).(*pb.OrderState)
	closed.Status = pb.OrderStateStatus_ORDER_CANCELLED
	closedData, err := proto.Marshal(closed)
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyOrderState(closed.OrderId), closedData)
	syncSpotAccountOrderIndex(sv, closed, closedData, open)
	if _, exists, _ := sv.Get(keys.KeyAccountOrderItem(owner, open.OrderId)); exists {
		t.Fatal("legacy open spot item should be deleted")
	}
	if _, exists, _ := sv.Get(keys.KeyRentOwnerMarker(owner, rent.OwnerMarkerSpotOrder)); !exists {
		t.Fatal("spot order marker should be conservatively retained without prefix scan")
	}
}

func TestFutureOrderMarkerRetainsOwnerMarkerWithLastOpenOrder(t *testing.T) {
	sv := NewMockStateView()
	owner := "fbc1qfuturecleanup00000000000000000000000"
	open := &pb.FutureOrderState{
		OrderId: "future_open_1", OwnerAddress: owner, Pair: "BTC_FB", Price: "100", Amount: "1",
		Status: pb.FutureOrderStatus_FUTURE_ORDER_OPEN, CreateHeight: 1,
	}
	if err := writeFutureOrderSnapshot(sv, open); err != nil {
		t.Fatal(err)
	}
	if _, exists, _ := sv.Get(keys.KeyRentOwnerMarker(owner, rent.OwnerMarkerFutureOrder)); !exists {
		t.Fatal("future order marker should be set")
	}
	closed := proto.Clone(open).(*pb.FutureOrderState)
	closed.Status = pb.FutureOrderStatus_FUTURE_ORDER_FILLED
	if err := writeFutureOrderSnapshot(sv, closed, open); err != nil {
		t.Fatal(err)
	}
	if _, exists, _ := sv.Get(keys.KeyRentOwnerMarker(owner, rent.OwnerMarkerFutureOrder)); !exists {
		t.Fatal("future order marker should be conservatively retained without prefix scan")
	}
}
