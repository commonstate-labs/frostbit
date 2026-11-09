package vm

import (
	"reflect"
	"testing"

	"dex/keys"
	"dex/pb"
)

func TestCollectQueryPruneManifestInputsDoesNotResortOrDeduplicate(t *testing.T) {
	terminalFuture := keys.KeyFutureAccountOrder("alice", "BTC_USDT", pb.FutureOrderStatus_FUTURE_ORDER_FILLED, 1, "future-filled")
	terminalSpot := keys.KeySpotAccountOrder("alice", "BTC_USDT", pb.OrderStateStatus_ORDER_CANCELLED, 1, "spot-cancelled")
	positionHistory := keys.KeyFuturePositionHistory("position-closed")
	userAct := keys.KeyUserActivity("alice", 7, 0)
	userActKind := keys.KeyUserActivityKindIndex("alice", "FUNDING_SETTLE", 7, "tx-user-act", 0)
	frostWithdraw := keys.KeyFrostWithdrawQueryGlobal(7, 1, "withdraw-queued")
	archiveRequest := keys.KeyArchiveRechargeRequest("done-request")
	archiveVote := keys.KeyArchiveWitnessVote("done-request", "witness")
	archiveChallenge := keys.KeyArchiveChallengeRecord("done-challenge")
	archiveContribution := keys.KeyArchiveChallengeContribution("done-challenge", "contribution")
	diff := []WriteOp{
		{Key: terminalFuture, Value: []byte("future")},
		{Key: positionHistory, Value: []byte("position")},
		{Key: keys.KeyFutureAccountOrder("alice", "BTC_USDT", pb.FutureOrderStatus_FUTURE_ORDER_OPEN, 1, "future-open"), Value: []byte("open")},
		{Key: terminalSpot, Value: []byte("spot")},
		{Key: keys.KeyTransferHistory("tx-transfer"), Value: []byte("transfer")},
		{Key: userAct, Value: []byte("activity")},
		{Key: userActKind, Value: []byte(userAct)},
		{Key: frostWithdraw, Value: []byte("withdraw-queued")},
		{Key: archiveRequest, Value: []byte("terminal-request")},
		{Key: archiveVote, Value: []byte("terminal-vote")},
		{Key: archiveChallenge, Value: []byte("terminal-challenge")},
		{Key: archiveContribution, Value: []byte("terminal-contribution")},
		{Key: keys.KeyFrostVaultHandoverProof("eth", 0, 2), Value: []byte("proof")},
		{Key: keys.KeyArchiveFrostMigration("migration"), Value: []byte("artifact")},
		{Key: keys.KeyFutureTradeRecord("BTC_USDT", 7_000_000, "deleted"), Del: true},
	}
	if got, want := collectQueryPruneManifestKeys(diff), []string{terminalFuture, positionHistory, terminalSpot, userAct, userActKind, frostWithdraw, archiveRequest, archiveVote, archiveChallenge, archiveContribution}; !reflect.DeepEqual(got, want) {
		t.Fatalf("manifest keys=%v, want %v", got, want)
	}

	receipts := []*Receipt{{TxID: "tx-z"}, nil, {TxID: "tx-a"}}
	if got, want := collectQueryPruneReceiptTxIDs(receipts), []string{"tx-z", "tx-a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("receipt txIDs=%v, want %v", got, want)
	}
	txs := []*pb.AnyTx{
		{Content: &pb.AnyTx_Transaction{Transaction: &pb.Transaction{Base: &pb.BaseMessage{TxId: "tx-z"}}}},
		{Content: &pb.AnyTx_WitnessVoteTx{WitnessVoteTx: &pb.WitnessVoteTx{Base: &pb.BaseMessage{TxId: "tx-a"}}}},
		{Content: &pb.AnyTx_OrderTx{OrderTx: &pb.OrderTx{Base: &pb.BaseMessage{TxId: "not-executed"}}}},
	}
	receiptMap := map[string]*Receipt{"tx-z": receipts[0], "tx-a": receipts[2]}
	if got, want := collectQueryPruneNonceReceiptTxIDs(txs, receiptMap), []string{"tx-z"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("nonce receipt txIDs=%v, want %v", got, want)
	}
}
