package workers

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"dex/frost/runtime/planning"
	"dex/keys"
)

type withdrawBatchReadCounter struct {
	*testStateReader
	gets, batches, keys int
	batchError          error
}

func (r *withdrawBatchReadCounter) Get(key string) ([]byte, bool, error) {
	r.gets++
	return r.testStateReader.Get(key)
}
func (r *withdrawBatchReadCounter) GetMany(k []string) (map[string][]byte, error) {
	r.batches++
	r.keys += len(k)
	if r.batchError != nil {
		return nil, r.batchError
	}
	result := make(map[string][]byte, len(k))
	for _, key := range k {
		if value, exists := r.data[key]; exists {
			result[key] = value
		}
	}
	return result, nil
}

func TestShouldSkipBTCBatchUsesOneBatchRead(t *testing.T) {
	for _, size := range []int{255, 256, 257} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			r := &withdrawBatchReadCounter{testStateReader: newTestStateReader()}
			job := &planning.Job{JobID: "btc-read-batch", Chain: "btc", Asset: "BTC", WithdrawIDs: make([]string, size)}
			for i := range job.WithdrawIDs {
				job.WithdrawIDs[i] = fmt.Sprintf("wd-batch-%d", i)
				seedQueuedWithdraw(t, r.testStateReader, job.WithdrawIDs[i])
			}
			worker := &WithdrawWorker{stateReader: r}
			skip, reason, err := worker.shouldSkipJob(job)
			if skip || reason != "" || err != nil {
				t.Fatalf("skip=%v reason=%q err=%v", skip, reason, err)
			}
			if r.gets != 1 || r.batches != 1 || r.keys != size {
				t.Fatalf("point=%d batches=%d keys=%d", r.gets, r.batches, r.keys)
			}
			                                                           
			delete(r.data, keys.KeyFrostWithdraw(job.WithdrawIDs[size-1]))
			skip, reason, err = worker.shouldSkipJob(job)
			if !skip || err != nil || !strings.Contains(reason, job.WithdrawIDs[size-1]) {
				t.Fatalf("missing late request: skip=%v reason=%q err=%v", skip, reason, err)
			}
			r.batchError = errors.New("batch storage failure")
			skip, _, err = worker.shouldSkipJob(job)
			if skip || !errors.Is(err, r.batchError) {
				t.Fatalf("storage failure hidden: skip=%v err=%v", skip, err)
			}
		})
	}
}
