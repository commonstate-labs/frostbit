                                                            
                                                                                               
                                                                                              

package workers

import (
	"bytes"
	"fmt"
	"testing"

	chainpkg "dex/frost/chain"
	"dex/frost/chain/evm"
	"dex/frost/runtime/planning"
	"dex/frost/runtime/types"
	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

                                                                                                               
type committeeVaultProvider struct {
	testVaultProvider
	members []SignerInfo
	err     error
}

func (p *committeeVaultProvider) VaultCommittee(chain string, vaultID uint32, epoch uint64) ([]SignerInfo, error) {
	if p.err != nil {
		return nil, p.err
	}
	return p.members, nil
}

func makeCommittee(ids ...string) []SignerInfo {
	out := make([]SignerInfo, len(ids))
	for i, id := range ids {
		out[i] = SignerInfo{ID: types.NodeID(id), Index: uint32(i)}
	}
	return out
}

                                                                        

func TestComputeJobCoordinatorSeed_Deterministic(t *testing.T) {
	a := computeJobCoordinatorSeed("job_1", 7)
	b := computeJobCoordinatorSeed("job_1", 7)
	if !bytes.Equal(a, b) {
		t.Fatalf("seed must be deterministic for same input")
	}
	if len(a) != 32 {
		t.Fatalf("seed must be 32 bytes (sha256), got %d", len(a))
	}

	if bytes.Equal(a, computeJobCoordinatorSeed("job_2", 7)) {
		t.Fatal("seed must differ when jobID differs")
	}
	if bytes.Equal(a, computeJobCoordinatorSeed("job_1", 8)) {
		t.Fatal("seed must differ when keyEpoch differs")
	}
}

                                                                     

func TestPermuteSignerCommittee_PreservesMembership(t *testing.T) {
	committee := makeCommittee("a", "b", "c", "d", "e")
	seed := computeJobCoordinatorSeed("job_x", 1)
	permuted := permuteSignerCommittee(committee, seed)

	if len(permuted) != len(committee) {
		t.Fatalf("length mismatch: got=%d want=%d", len(permuted), len(committee))
	}
	                           
	got := make(map[types.NodeID]bool)
	for _, m := range permuted {
		got[m.ID] = true
	}
	for _, m := range committee {
		if !got[m.ID] {
			t.Fatalf("member %s lost after permutation", m.ID)
		}
	}
	                                                                                 
	other := permuteSignerCommittee(committee, computeJobCoordinatorSeed("job_x", 99))
	diff := 0
	for i := range permuted {
		if permuted[i].ID != other[i].ID {
			diff++
		}
	}
	if diff == 0 {
		t.Fatal("expected different permutations for different seeds")
	}
}

func TestPermuteSignerCommittee_EmptyAndSingle(t *testing.T) {
	if got := permuteSignerCommittee(nil, []byte{0}); len(got) != 0 {
		t.Fatal("nil committee should return empty")
	}
	single := makeCommittee("solo")
	if got := permuteSignerCommittee(single, []byte{1}); len(got) != 1 || got[0].ID != "solo" {
		t.Fatalf("single member permutation must be unchanged, got %+v", got)
	}
}

                                                                         

func TestComputeJobCoordinatorIndex_InRange(t *testing.T) {
	committee := makeCommittee("n1", "n2", "n3", "n4")
	                                                                 
	for i := 0; i < 20; i++ {
		idx := computeJobCoordinatorIndex(fmt.Sprintf("job_%d", i), uint64(i), committee)
		if idx < 0 || idx >= len(committee) {
			t.Fatalf("idx=%d out of range for job_%d", idx, i)
		}
	}
}

func TestComputeJobCoordinatorIndex_EmptyCommittee(t *testing.T) {
	if idx := computeJobCoordinatorIndex("job", 1, nil); idx != 0 {
		t.Fatalf("empty committee should return 0, got %d", idx)
	}
}

func TestComputeJobCoordinatorIndex_Deterministic(t *testing.T) {
	committee := makeCommittee("alpha", "beta", "gamma")
	a := computeJobCoordinatorIndex("job_stable", 42, committee)
	b := computeJobCoordinatorIndex("job_stable", 42, committee)
	if a != b {
		t.Fatalf("index must be deterministic: %d vs %d", a, b)
	}
}

                                                            

func seedQueuedWithdraw(t *testing.T, r *testStateReader, id string) {
	t.Helper()
	data, err := proto.Marshal(&pb.FrostWithdrawState{WithdrawId: id, Status: "QUEUED"})
	if err != nil {
		t.Fatalf("marshal withdraw: %v", err)
	}
	r.data[keys.KeyFrostWithdraw(id)] = data
}

func seedPendingWithdrawSignedTx(t *testing.T, r *testStateReader, jobID, txID string) *pb.FrostWithdrawSignedTx {
	t.Helper()
	signed := &pb.FrostWithdrawSignedTx{
		Base:  &pb.BaseMessage{TxId: txID},
		JobId: jobID,
	}
	data, err := proto.Marshal(&pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: signed}})
	if err != nil {
		t.Fatalf("marshal pending signed tx: %v", err)
	}
	r.data[keys.KeyPendingAnyTx(txID)] = data
	return signed
}

type scanCountingStateReader struct {
	*testStateReader
	scans int
}

func (r *scanCountingStateReader) Scan(prefix string, fn func(k string, v []byte) bool) error {
	r.scans++
	return r.testStateReader.Scan(prefix, fn)
}

func TestShouldSkipJob_NilReaderOrJob(t *testing.T) {
	w := &WithdrawWorker{}
	skip, _, err := w.shouldSkipJob(&planning.Job{JobID: "j"})
	if err != nil || skip {
		t.Fatalf("nil reader must not error or skip: skip=%v err=%v", skip, err)
	}
	w.stateReader = newTestStateReader()
	skip, _, err = w.shouldSkipJob(nil)
	if err != nil || skip {
		t.Fatalf("nil job must not error or skip: skip=%v err=%v", skip, err)
	}
}

func TestShouldSkipJob_PendingTx(t *testing.T) {
	r := newTestStateReader()
	w := &WithdrawWorker{stateReader: r}
	job := seedRecoveredAccountClaim(t, r, "80", "80")
	seedPendingWithdrawSignedTx(t, r, job.JobID, "0xreal-pending-id")
	skip, reason, err := w.shouldSkipJob(job)
	if err != nil || !skip {
		t.Fatalf("expected skip: skip=%v err=%v", skip, err)
	}
	if reason != "withdraw tx already pending" {
		t.Fatalf("unexpected reason: %s", reason)
	}
	if cached := w.cachedWithdrawSignedTx(job.JobID); cached == nil || cached.GetBase().GetTxId() != "0xreal-pending-id" {
		t.Fatalf("pending transaction was not recovered into worker cache: %+v", cached)
	}
}

func TestShouldSkipJobBrandNewQueuedJobDoesNotScanPendingPrefix(t *testing.T) {
	base := newTestStateReader()
	seedQueuedWithdraw(t, base, "wd_new")
	r := &scanCountingStateReader{testStateReader: base}
	w := &WithdrawWorker{stateReader: r}

	skip, reason, err := w.shouldSkipJob(&planning.Job{JobID: "job_new", WithdrawIDs: []string{"wd_new"}})
	if err != nil || skip {
		t.Fatalf("new queued job should remain processable: skip=%v reason=%q err=%v", skip, reason, err)
	}
	if r.scans != 0 {
		t.Fatalf("new queued job caused %d pending prefix scans, want 0", r.scans)
	}
}

func TestShouldSkipJob_AppliedTx(t *testing.T) {
	r := newTestStateReader()
	w := &WithdrawWorker{stateReader: r}
	job := &planning.Job{JobID: "job_applied", WithdrawIDs: []string{"wd_1"}}
	w.rememberWithdrawSignedTx(&pb.FrostWithdrawSignedTx{
		Base:  &pb.BaseMessage{TxId: "0xreal-applied-id"},
		JobId: job.JobID,
	})
	r.data[keys.KeyVMAppliedTx("0xreal-applied-id")] = []byte{1}
	skip, reason, err := w.shouldSkipJob(job)
	if err != nil || !skip || reason != "withdraw tx already applied" {
		t.Fatalf("expected applied skip, got skip=%v reason=%q err=%v", skip, reason, err)
	}
	if cached := w.cachedWithdrawSignedTx(job.JobID); cached != nil {
		t.Fatalf("applied transaction cache was not released: %+v", cached)
	}
}

func TestShouldSkipJobCachedTxUsesPointReadsWithoutPendingScan(t *testing.T) {
	base := newTestStateReader()
	seedQueuedWithdraw(t, base, "wd_cached")
	r := &scanCountingStateReader{testStateReader: base}
	w := &WithdrawWorker{stateReader: r}
	job := &planning.Job{JobID: "job_cached", WithdrawIDs: []string{"wd_cached"}}
	w.rememberWithdrawSignedTx(&pb.FrostWithdrawSignedTx{
		Base:  &pb.BaseMessage{TxId: "0xcached-id"},
		JobId: job.JobID,
	})

	skip, reason, err := w.shouldSkipJob(job)
	if err != nil || skip {
		t.Fatalf("cached non-pending tx should remain retryable: skip=%v reason=%q err=%v", skip, reason, err)
	}
	if r.scans != 0 {
		t.Fatalf("cached tx caused %d pending prefix scans, want 0", r.scans)
	}
}

func TestFindPendingWithdrawSignedTxSkipsMalformedEntries(t *testing.T) {
	r := newTestStateReader()
	r.data[keys.KeyPendingAnyTx("0xmalformed")] = []byte("not protobuf")
	missingID, err := proto.Marshal(&pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: &pb.FrostWithdrawSignedTx{
		JobId: "job-after-malformed",
	}}})
	if err != nil {
		t.Fatalf("marshal pending entry without txID: %v", err)
	}
	r.data[keys.KeyPendingAnyTx("0xmissing-in-payload")] = missingID
	seedPendingWithdrawSignedTx(t, r, "job-after-malformed", "0xvalid")
	w := &WithdrawWorker{stateReader: r}

	found, err := w.findPendingWithdrawSignedTx("job-after-malformed")
	if err != nil {
		t.Fatalf("find pending signed tx: %v", err)
	}
	if found == nil || found.GetBase().GetTxId() != "0xvalid" {
		t.Fatalf("matching pending tx not found after malformed entry: %+v", found)
	}
}

func TestPruneFinalizedWithdrawSignedTxs(t *testing.T) {
	r := newTestStateReader()
	jobID := "job-cache-cleanup"
	jobData, err := proto.Marshal(&pb.FrostWithdrawJobState{JobId: jobID, Status: "CLAIMED"})
	if err != nil {
		t.Fatalf("marshal claimed job: %v", err)
	}
	r.data[keys.KeyFrostWithdrawJob(jobID)] = jobData
	w := &WithdrawWorker{stateReader: r}
	w.rememberWithdrawSignedTx(&pb.FrostWithdrawSignedTx{
		Base:  &pb.BaseMessage{TxId: "0xcached"},
		JobId: jobID,
	})

	w.pruneFinalizedWithdrawSignedTxs()
	if cached := w.cachedWithdrawSignedTx(jobID); cached == nil {
		t.Fatal("active CLAIMED job was pruned")
	}

	delete(r.data, keys.KeyFrostWithdrawJob(jobID))
	w.pruneFinalizedWithdrawSignedTxs()
	if cached := w.cachedWithdrawSignedTx(jobID); cached != nil {
		t.Fatalf("cache retained finalized/pruned job: %+v", cached)
	}
}

func TestShouldSkipJob_WithdrawMissing(t *testing.T) {
	r := newTestStateReader()
	w := &WithdrawWorker{stateReader: r}
	job := &planning.Job{JobID: "job_miss", WithdrawIDs: []string{"wd_missing"}}
	skip, reason, err := w.shouldSkipJob(job)
	if err != nil || !skip {
		t.Fatalf("expected skip for missing withdraw: skip=%v err=%v", skip, err)
	}
	if reason == "" {
		t.Fatal("expected non-empty reason")
	}
}

func TestShouldSkipJob_WithdrawNotQueued(t *testing.T) {
	r := newTestStateReader()
	w := &WithdrawWorker{stateReader: r}
	data, _ := proto.Marshal(&pb.FrostWithdrawState{WithdrawId: "wd_done", Status: "COMPLETED"})
	r.data[keys.KeyFrostWithdraw("wd_done")] = data
	skip, reason, err := w.shouldSkipJob(&planning.Job{JobID: "job_done", WithdrawIDs: []string{"wd_done"}})
	if err != nil || !skip {
		t.Fatalf("expected skip for non-QUEUED: skip=%v err=%v", skip, err)
	}
	if reason == "" {
		t.Fatal("expected non-empty reason")
	}
}

func TestShouldSkipJob_HappyPath(t *testing.T) {
	r := newTestStateReader()
	seedQueuedWithdraw(t, r, "wd_ok")
	w := &WithdrawWorker{stateReader: r}
	skip, reason, err := w.shouldSkipJob(&planning.Job{JobID: "job_ok", WithdrawIDs: []string{"wd_ok"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if skip {
		t.Fatalf("should not skip happy path, reason=%q", reason)
	}
}

                                                               

func TestIsJobCoordinator_EmptyCommittee(t *testing.T) {
	w := &WithdrawWorker{vaultProvider: &committeeVaultProvider{}}
	_, _, err := w.isJobCoordinator(&planning.Job{JobID: "j", Chain: "eth", VaultID: 0, KeyEpoch: 1})
	if err == nil {
		t.Fatal("expected error for empty committee")
	}
}

func TestIsJobCoordinator_MatchAndMismatch(t *testing.T) {
	committee := makeCommittee("n1", "n2", "n3")
	job := &planning.Job{JobID: "job_match", Chain: "eth", VaultID: 0, KeyEpoch: 1}
	idx := computeJobCoordinatorIndex(job.JobID, job.KeyEpoch, committee)
	coord := string(committee[idx].ID)

	w := &WithdrawWorker{vaultProvider: &committeeVaultProvider{members: committee}, localAddress: coord}
	isCoord, addr, err := w.isJobCoordinator(job)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isCoord || addr != coord {
		t.Fatalf("expected coordinator match: isCoord=%v addr=%s want=%s", isCoord, addr, coord)
	}

	                                                           
	other := "n1"
	if other == coord {
		other = "n2"
	}
	w.localAddress = other
	isCoord, _, _ = w.isJobCoordinator(job)
	if isCoord {
		t.Fatalf("expected mismatch for localAddress=%s coord=%s", other, coord)
	}
}

                                                                                     

func TestWithdrawWorkerExtractMessages_EVM(t *testing.T) {
	for _, chainName := range []string{chainpkg.ChainETH, chainpkg.ChainBNB, chainpkg.ChainBASE, chainpkg.ChainARB, chainpkg.ChainTRX} {
		t.Run(chainName, func(t *testing.T) {
			w := &WithdrawWorker{stateReader: newTestStateReader()}
			result, err := evm.NewEVMAdapter(chainName, 0).BuildWithdrawTemplate(chainpkg.WithdrawTemplateParams{AuthorityProtocol: 3, DexChainID: 51025, GuardianPubkey: workerAccountGuardianKey(),
				Chain:        chainName,
				Asset:        chainName,
				VaultID:      0,
				KeyEpoch:     1,
				BatchSeq:     "1",
				WithdrawIDs:  []string{"w1"},
				ContractAddr: "0x00000000000000000000000000000000000000aa",
				Outputs:      []chainpkg.WithdrawOutput{{WithdrawID: "w1", To: "0x00000000000000000000000000000000000000bb", Amount: "1000", BroadcastFee: "1"}},
			})
			if err != nil {
				t.Fatalf("BuildWithdrawTemplate failed: %v", err)
			}
			if len(result.SigHashes) != 1 || bytes.Equal(result.SigHashes[0], result.TemplateHash) {
				t.Fatal("withdrawal signing hash must differ from template identity hash")
			}
			got, err := w.extractMessages(&planning.Job{JobID: "j", Chain: chainName, TemplateHash: result.TemplateHash, TemplateData: result.TemplateData})
			if err != nil {
				t.Fatalf("extractMessages failed: %v", err)
			}
			if len(got) != 1 || !bytes.Equal(got[0], result.SigHashes[0]) {
				t.Fatalf("want canonical signing hash %x, got %x", result.SigHashes[0], got)
			}
		})
	}
}

func TestWithdrawWorkerExtractMessages_EmptyTemplateData(t *testing.T) {
	w := &WithdrawWorker{stateReader: newTestStateReader()}
	for _, chainName := range []string{chainpkg.ChainETH, chainpkg.ChainBNB, chainpkg.ChainBASE, chainpkg.ChainARB, chainpkg.ChainTRX} {
		t.Run(chainName, func(t *testing.T) {
			if _, err := w.extractMessages(&planning.Job{JobID: "j", Chain: chainName, TemplateHash: []byte("identity hash")}); err == nil {
				t.Fatal("expected error when TemplateData is empty for EVM/TVM account chain")
			}
		})
	}
}

                                                                        

func TestGetSignAlgo(t *testing.T) {
	w := &WithdrawWorker{}
	cases := map[string]pb.SignAlgo{
		"btc":     pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		"eth":     pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128,
		"bnb":     pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128,
		"base":    pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128,
		"arb":     pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128,
		"trx":     pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128,
		"unknown": pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,           
	}
	for chain, want := range cases {
		got, err := w.getSignAlgo(chain, 0)
		if err != nil {
			t.Fatalf("chain=%s err=%v", chain, err)
		}
		if got != want {
			t.Fatalf("chain=%s: got=%s want=%s", chain, got, want)
		}
	}
}

func TestSortStringsInline(t *testing.T) {
	s := []string{"c", "a", "b", "e", "d"}
	sortStrings(s)
	want := []string{"a", "b", "c", "d", "e"}
	for i := range want {
		if s[i] != want[i] {
			t.Fatalf("sort mismatch at %d: got=%s want=%s (all=%v)", i, s[i], want[i], s)
		}
	}
}
