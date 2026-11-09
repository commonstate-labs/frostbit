package handlers

import (
	"context"
	"dex/keys"
	"dex/pb"
	"fmt"
	"strings"
	"testing"
)

func TestBTCPartPagesUseBoundedBatchesAndParentBoundCursor(t *testing.T) {
	_, dbm := newFrostQueryTestHandler(t)
	defer dbm.Close()
	for _, count := range []int{49, 50, 51, 10001} {
		parent := fmt.Sprintf("parent-%d", count)
		for i := 1; i <= count; i++ {
			jobID := fmt.Sprintf("job-%d-%d", count, i)
			dbm.EnqueueSet(keys.KeyFrostBtcWithdrawPart(parent, uint64(i)), jobID)
			enqueueProto(t, dbm, keys.KeyFrostPermanentWithdrawJob(jobID), &pb.FrostWithdrawJobState{JobId: jobID, WithdrawIds: []string{parent}, BtcProtocolVersion: 3, BtcPartSeq: uint64(i), Status: "SIGNED", BtcGrossAmount: "100000", BtcFeeAmount: "200"})
		}
		flushFrostUTXOs(t, dbm)
		store := &measuredFrostUTXOStore{store: dbm}
		page, err := queryBTCParts(context.Background(), store, parent, 50, "", false)
		if err != nil {
			t.Fatal(err)
		}
		want := count
		if want > 50 {
			want = 50
		}
		if len(page.Items) != want || page.HasMore != (count > 50) || store.scans != 1 || store.batches != 2 || store.scanLimits[0] != 51 || store.batchSizes[0] != want || store.batchSizes[1] != want*2 {
			t.Fatalf("count=%d page=%+v scans=%d batches=%v", count, page, store.scans, store.batchSizes)
		}
		if count > 50 {
			id, limit, after, err := parseBTCPartQuery("withdraw_id="+parent+"&cursor="+page.NextCursor, false)
			if err != nil || id != parent || limit != 50 {
				t.Fatal(err)
			}
			next, err := queryBTCParts(context.Background(), store, parent, limit, after, false)
			if err != nil || next.Items[0].PartSeq != "51" {
				t.Fatalf("next=%+v err=%v", next, err)
			}
			if _, _, _, err := parseBTCPartQuery("withdraw_id=another&cursor="+page.NextCursor, false); err == nil {
				t.Fatal("cross-parent cursor accepted")
			}
		}
	}
}

func TestBTCIsolationPolicyRequiresFinalizedReceiptAndReadsOneBatch(t *testing.T) {
	_, dbm := newFrostQueryTestHandler(t)
	defer dbm.Close()
	dbm.EnqueueSet(keys.KeyProtocolVersion(), "3")
	enqueueProto(t, dbm, keys.KeyFrostBtcUtxoAmountBuildState(), &pb.FrostBtcIsolationIndexState{Done: true})
	flushFrostUTXOs(t, dbm)
	store := &measuredFrostUTXOStore{store: dbm}
	if _, err := queryBTCIsolationPolicy(context.Background(), store); err == nil {
		t.Fatal("missing receipt enabled v3")
	}
	dbm.EnqueueSet(keys.KeyProtocolUpgradeReceipt(3), "version=3;activation_height=200;id=btc-withdraw-isolation-v3")
	flushFrostUTXOs(t, dbm)
	store = &measuredFrostUTXOStore{store: dbm}
	policy, err := queryBTCIsolationPolicy(context.Background(), store)
	if err != nil || !policy.Active || !policy.IndexReady || policy.MinRechargeSats != "100000" || store.batches != 1 || store.batchSizes[0] != 3 || store.scans != 0 {
		t.Fatalf("policy=%+v err=%v", policy, err)
	}
}

func TestBTCPartQueryRejectsUnboundedAndConfusedParameters(t *testing.T) {
	for _, q := range []string{"withdraw_id=x&limit=201", "withdraw_id=x&limit=0", "withdraw_id=x&limit=01", "withdraw_id=x&withdraw_id=y", "withdraw_id=x&extra=1", "withdraw_id=x&cursor=" + strings.Repeat("a", 1025)} {
		if _, _, _, err := parseBTCPartQuery(q, false); err == nil {
			t.Fatalf("accepted %q", q)
		}
	}
}
