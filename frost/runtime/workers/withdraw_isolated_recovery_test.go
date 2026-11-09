package workers

import (
	"fmt"
	"testing"

	"dex/frost/runtime/planning"
	"dex/keys"
	"dex/pb"
	"google.golang.org/protobuf/proto"
)

type isolatedRecoveryCounter struct {
	*withdrawBatchReadCounter
	scans int
}

func (r *isolatedRecoveryCounter) Scan(prefix string, fn func(string, []byte) bool) error {
	r.scans++
	return r.testStateReader.Scan(prefix, fn)
}

func TestBTCIsolatedRecoveryScansPendingOnceForWholeWorkset(t *testing.T) {
	r := &isolatedRecoveryCounter{withdrawBatchReadCounter: &withdrawBatchReadCounter{testStateReader: newTestStateReader()}}
	const count = 128
	jobs := make([]*planning.Job, count)
	for i := range jobs {
		id := fmt.Sprintf("child-%d", i)
		parent := fmt.Sprintf("parent-%d", i)
		jobs[i] = &planning.Job{JobID: id, Chain: "btc", Asset: "BTC", WithdrawIDs: []string{parent}, BTCProtocolVersion: 3, BTCPartSeq: 1, BTCGrossAmount: "100000", BTCFeeAmount: "100", BTCOriginID: make([]byte, 32)}
		state := &pb.FrostWithdrawJobState{JobId: id, Chain: "btc", Asset: "BTC", WithdrawIds: []string{parent}, Status: "CLAIMED", BtcProtocolVersion: 3, BtcPartSeq: 1, BtcGrossAmount: "100000", BtcFeeAmount: "100", BtcOriginId: make([]byte, 32)}
		r.data[keys.KeyFrostWithdrawJob(id)], _ = proto.Marshal(state)
		r.data[keys.KeyFrostWithdraw(parent)], _ = proto.Marshal(&pb.FrostWithdrawState{WithdrawId: parent, Status: "QUEUED", BtcProtocolVersion: 3})
	}
	w := &WithdrawWorker{stateReader: r}
	if e := w.preparePendingWithdrawSignedTxs(jobs); e != nil {
		t.Fatal(e)
	}
	for _, job := range jobs {
		skip, reason, e := w.shouldSkipJob(job)
		if e != nil || skip {
			t.Fatalf("child skipped: %v %s", e, reason)
		}
	}
	if r.scans != 1 || r.batches != count+1 || r.keys > 2*count {
		t.Fatalf("repeated recovery prefix scan: scans=%d batches=%d keys=%d", r.scans, r.batches, r.keys)
	}
}

func TestBTCIsolatedFailedPipelineReleasesBoundedExecutionSlot(t *testing.T) {
	r := newTestStateReader()
	r.data[keys.KeyLatestHeight()] = []byte("10")
	w := &WithdrawWorker{stateReader: r, maxInFlight: 1}
	if !w.reservePipeline("bad") || w.reservePipeline("good") {
		t.Fatal("inflight limit not enforced")
	}
	w.releasePipeline("bad")
	if w.reservePipeline("bad") {
		t.Fatal("failed child retried before its bounded height delay")
	}
	if !w.reservePipeline("good") {
		t.Fatal("failed child prevented independent ready work")
	}
	w.releasePipeline("good")
	r.data[keys.KeyLatestHeight()] = []byte("13")
	if !w.reservePipeline("bad") {
		t.Fatal("exact original child cannot resume after delay")
	}
	w.releasePipeline("bad")
}
