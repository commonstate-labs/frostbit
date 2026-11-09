package vm

import (
	"dex/keys"
	"dex/pb"
	"dex/referral"
	"math/big"
	"testing"

	"google.golang.org/protobuf/proto"
)

func TestSVReferrerStoreWritesForwardAndReverseIndexes(t *testing.T) {
	sv := newMemStateView()
	store := &svReferrerStore{sv: sv}
	if err := store.SetReferrer("alice", "bob"); err != nil {
		t.Fatalf("set referrer: %v", err)
	}
	forward, exists, err := sv.Get(keys.KeyReferrer("alice"))
	if err != nil || !exists || string(forward) != "bob" {
		t.Fatalf("forward index: value=%q exists=%v err=%v", forward, exists, err)
	}
	if _, exists, err := sv.Get(keys.KeyReferralInvitee("bob", "alice")); err != nil || !exists {
		t.Fatalf("reverse index: exists=%v err=%v", exists, err)
	}
}

func TestWriteTradingFeeRecordWritesReferrerReverseIndex(t *testing.T) {
	sv := newMemStateView()
	result := &referral.FeeResult{
		Trader:          "alice",
		Referrer:        "bob",
		FullFee:         big.NewInt(10),
		ActualFee:       big.NewInt(8),
		ReferrerRebate:  big.NewInt(2),
		InsuranceAmount: big.NewInt(0),
	}

	writeTradingFeeRecord(sv, result, "FB_USDT", big.NewInt(100), 7, "tx-7", 3)

	key := keys.KeyReferralRebateRecord("bob", 7, "tx-7", 3)
	data, exists, err := sv.Get(key)
	if err != nil || !exists {
		t.Fatalf("reverse index missing: exists=%v err=%v", exists, err)
	}
	var record pb.TradingFeeRecord
	if err := proto.Unmarshal(data, &record); err != nil {
		t.Fatalf("unmarshal reverse index: %v", err)
	}
	if record.Trader != "alice" || record.Referrer != "bob" || record.ReferrerRebate != "2" {
		t.Fatalf("unexpected reverse record: %+v", &record)
	}
}

func TestWriteTradingFeeRecordSkipsEmptyRebateReverseIndex(t *testing.T) {
	sv := newMemStateView()
	result := &referral.FeeResult{
		Trader:          "alice",
		FullFee:         big.NewInt(10),
		ActualFee:       big.NewInt(10),
		ReferrerRebate:  big.NewInt(0),
		InsuranceAmount: big.NewInt(0),
	}

	writeTradingFeeRecord(sv, result, "FB_USDT", big.NewInt(100), 7, "tx-7", 0)
	if _, exists, err := sv.Get(keys.KeyReferralRebateRecord("", 7, "tx-7", 0)); err != nil || exists {
		t.Fatalf("unexpected reverse index: exists=%v err=%v", exists, err)
	}
}

func TestSetReferrerRejectsInvalidAddress(t *testing.T) {
	tx := &pb.AnyTx{Content: &pb.AnyTx_SetReferrerTx{SetReferrerTx: &pb.SetReferrerTx{
		Base:            &pb.BaseMessage{TxId: "tx-invalid-referrer", FromAddress: "alice"},
		ReferrerAddress: "not-an-address",
	}}}
	_, receipt, err := (&SetReferrerTxHandler{}).DryRun(tx, newMemStateView())
	if err == nil || receipt == nil || receipt.Status != "FAILED" {
		t.Fatalf("invalid referrer: receipt=%+v err=%v", receipt, err)
	}
}
