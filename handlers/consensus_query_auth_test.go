package handlers

import (
	"bytes"
	"crypto/sha256"
	"dex/consensus"
	"dex/pb"
	"dex/utils"
	"fmt"
	"sync"
	"testing"

	"google.golang.org/protobuf/proto"
)

func queryReplayDigest(label string) []byte {
	digest := sha256.Sum256([]byte(label))
	return digest[:]
}

func TestConsensusQueryReplayGuardAllowsReorderedFutureHeights(t *testing.T) {
	var guard consensusQueryReplayGuard
	const localAccepted = uint64(10)

	if !guard.reserve("alice", 12, localAccepted, queryReplayDigest("h12-first")) {
		t.Fatal("future height 12 should be accepted")
	}
	if !guard.reserve("alice", 11, localAccepted, queryReplayDigest("h11-late")) {
		t.Fatal("network-reordered height 11 must not be poisoned by remote height 12")
	}
	if guard.reserve("alice", 11, localAccepted, queryReplayDigest("h11-late")) {
		t.Fatal("the exact signed digest must not be accepted twice")
	}
	if !guard.reserve("alice", localAccepted, localAccepted, queryReplayDigest("finalized")) {
		t.Fatal("a query at local finality must reach the finalized Chit reply path")
	}
	farFuture := localAccepted + consensus.MaxFutureBlockCacheWindow + 1
	if guard.reserve("alice", farFuture, localAccepted, queryReplayDigest("far-future")) {
		t.Fatal("remote far-future height must not enter replay state")
	}

	                                                                           
	                                                                           
	if !guard.reserve("alice", 11, 11, queryReplayDigest("old-new-digest")) {
		t.Fatal("recently finalized height must remain answerable")
	}
	if !guard.reserve("alice", 12, 11, queryReplayDigest("h12-second-round")) {
		t.Fatal("a distinct active round at height 12 should remain valid")
	}
}

func TestConsensusQueryReplayGuardEvictsAtCapacityWithoutMutingSender(t *testing.T) {
	var guard consensusQueryReplayGuard
	first := queryReplayDigest("query-0")
	for i := 0; i < maxConsensusQueriesPerSenderHeight; i++ {
		digest := queryReplayDigest(fmt.Sprintf("query-%d", i))
		if !guard.reserve("alice", 1, 0, digest) {
			t.Fatalf("reserve %d unexpectedly failed", i)
		}
	}
	latest := queryReplayDigest("over-capacity")
	if !guard.reserve("alice", 1, 0, latest) {
		t.Fatal("a fresh authenticated query must evict the oldest entry at capacity")
	}
	if guard.reserve("alice", 1, 0, latest) {
		t.Fatal("an exact duplicate still resident in the bounded cache was accepted")
	}
	if !guard.reserve("alice", 1, 0, first) {
		t.Fatal("the oldest evicted digest should be harmlessly processable again")
	}
	state := guard.bySender["alice"]
	if state == nil || state.total != maxConsensusQueriesPerSenderHeight {
		t.Fatalf("replay memory is not bounded: state=%+v", state)
	}
}

func TestConsensusQueryReplayGuardRejectsOnlyFinalizedHeightsOutsideRecentWindow(t *testing.T) {
	var guard consensusQueryReplayGuard
	localAccepted := uint64(consensus.MaxFutureBlockCacheWindow + 10)
	if !guard.reserve("alice", localAccepted-consensus.MaxFutureBlockCacheWindow, localAccepted, queryReplayDigest("oldest-recent")) {
		t.Fatal("oldest recent finalized height should remain answerable")
	}
	if guard.reserve("alice", localAccepted-consensus.MaxFutureBlockCacheWindow-1, localAccepted, queryReplayDigest("too-old")) {
		t.Fatal("a finalized height outside the bounded recent window was accepted")
	}
}

func TestConsensusQueryReplayGuardReattachesCurrentBucketAfterGlobalEviction(t *testing.T) {
	var guard consensusQueryReplayGuard
	if !guard.reserve("alice", 1, 0, queryReplayDigest("height-1-oldest")) {
		t.Fatal("failed to reserve oldest current-height entry")
	}
	remaining := maxConsensusQueriesPerSender - 1
	for i := 0; i < remaining; i++ {
		height := uint64(2 + i/maxConsensusQueriesPerSenderHeight)
		if !guard.reserve("alice", height, 0, queryReplayDigest(fmt.Sprintf("fill-%d", i))) {
			t.Fatalf("global fill %d unexpectedly failed", i)
		}
	}

	latest := queryReplayDigest("height-1-after-global-eviction")
	reservation := guard.reserveWithToken("alice", 1, 0, latest)
	if reservation == nil {
		t.Fatal("fresh current-height query was muted at global capacity")
	}
	state := guard.bySender["alice"]
	bucket := state.seenByHeight[1]
	var latestKey [sha256.Size]byte
	copy(latestKey[:], latest)
	if bucket == nil || bucket.entries[latestKey] == nil {
		t.Fatal("new entry was inserted into a bucket detached by global eviction")
	}
	guard.release(reservation)
	if _, exists := state.seenByHeight[1]; exists {
		t.Fatal("release did not remove the reattached current-height bucket")
	}
}

func TestConsensusQueryReplayGuardReleaseAllowsTransportRetry(t *testing.T) {
	var guard consensusQueryReplayGuard
	digest := queryReplayDigest("retry")
	reservation := guard.reserveWithToken("alice", 1, 0, digest)
	if reservation == nil {
		t.Fatal("initial reservation failed")
	}
	guard.release(reservation)
	if !guard.reserve("alice", 1, 0, digest) {
		t.Fatal("failed enqueue reservation must be retryable")
	}
}

func TestConsensusQueryReplayGuardStaleReleaseCannotDeleteNewGeneration(t *testing.T) {
	var guard consensusQueryReplayGuard
	digest := queryReplayDigest("same-signed-query")
	oldReservation := guard.reserveWithToken("alice", 1, 0, digest)
	if oldReservation == nil {
		t.Fatal("initial reservation failed")
	}
	for i := 1; i <= maxConsensusQueriesPerSenderHeight; i++ {
		if !guard.reserve("alice", 1, 0, queryReplayDigest(fmt.Sprintf("evict-%d", i))) {
			t.Fatalf("eviction fill %d failed", i)
		}
	}
	newReservation := guard.reserveWithToken("alice", 1, 0, digest)
	if newReservation == nil {
		t.Fatal("same digest was not accepted after its old generation was evicted")
	}

	guard.release(oldReservation)
	if guard.reserve("alice", 1, 0, digest) {
		t.Fatal("stale release deleted the newer reservation for the same digest")
	}
	guard.release(newReservation)
	if !guard.reserve("alice", 1, 0, digest) {
		t.Fatal("current reservation release did not allow transport retry")
	}
}

func TestConsensusQueryReplayGuardConcurrentDuplicateHasSingleReservation(t *testing.T) {
	var guard consensusQueryReplayGuard
	digest := queryReplayDigest("concurrent-duplicate")
	const workers = 64
	var wg sync.WaitGroup
	results := make(chan *consensusQueryReplayReservation, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- guard.reserveWithToken("alice", 1, 0, digest)
		}()
	}
	wg.Wait()
	close(results)

	accepted := 0
	for reservation := range results {
		if reservation != nil {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("concurrent exact duplicate reservations = %d, want 1", accepted)
	}
}

func newConsensusQueryTestKey(t *testing.T) *utils.KeyManager {
	t.Helper()
	key := utils.NewKeyManager()
	if err := key.InitKeyRandom(); err != nil {
		t.Fatalf("InitKeyRandom: %v", err)
	}
	return key
}

func TestConsensusQueryValidationBindsResponderChainAndPullSignature(t *testing.T) {
	oldChainID := pb.GetDefaultChainID()
	pb.SetDefaultChainID(1001)
	consensus.ClearNodePublicKeys()
	consensus.SetNodePublicKeyResolver(nil)
	t.Cleanup(func() {
		pb.SetDefaultChainID(oldChainID)
		consensus.ClearNodePublicKeys()
		consensus.SetNodePublicKeyResolver(nil)
	})

	key := newConsensusQueryTestKey(t)
	consensus.RegisterNodePublicKey("alice", key.PublicKeyBytes())
	hm := &HandlerManager{address: "bob"}

	push := &pb.PushQuery{
		RequestId: 4, Address: "alice", Responder: "bob", ChainId: 1001,
		ContainerIsBlock: true, Container: []byte("block"), RequestedHeight: 8,
		BlockId: "block-8", Nonce: bytes.Repeat([]byte{9}, consensus.SnowmanQueryNonceSize), VrfSeed: []byte("seed"), SeqId: 2,
	}
	var err error
	push.Signature, err = key.Sign(consensus.ComputePushQueryDigest(push))
	if err != nil {
		t.Fatalf("sign PushQuery: %v", err)
	}
	if _, err := hm.validatePushQuery(push); err != nil {
		t.Fatalf("valid PushQuery rejected: %v", err)
	}
	shortPushNonce := proto.Clone(push).(*pb.PushQuery)
	shortPushNonce.Nonce = shortPushNonce.Nonce[:consensus.SnowmanQueryNonceSize-1]
	if _, err := hm.validatePushQuery(shortPushNonce); err == nil {
		t.Fatal("PushQuery with non-32-byte nonce was accepted")
	}

	wrongTarget := proto.Clone(push).(*pb.PushQuery)
	wrongTarget.Responder = "carol"
	wrongTarget.Signature, err = key.Sign(consensus.ComputePushQueryDigest(wrongTarget))
	if err != nil {
		t.Fatalf("sign wrong-target PushQuery: %v", err)
	}
	if _, err := hm.validatePushQuery(wrongTarget); err == nil {
		t.Fatal("validly signed query for another responder was accepted")
	}

	pull := &pb.PullQuery{
		RequestId: 5, Address: "alice", Responder: "bob", ChainId: 1001,
		BlockId: "block-8", RequestedHeight: 8, VrfSeed: []byte("seed"), SeqId: 3,
		Nonce: bytes.Repeat([]byte{10}, consensus.SnowmanQueryNonceSize),
	}
	pull.Signature, err = key.Sign(consensus.ComputePullQueryDigest(pull))
	if err != nil {
		t.Fatalf("sign PullQuery: %v", err)
	}
	if _, err := hm.validatePullQuery(pull); err != nil {
		t.Fatalf("valid signed PullQuery rejected: %v", err)
	}
	shortPullNonce := proto.Clone(pull).(*pb.PullQuery)
	shortPullNonce.Nonce = shortPullNonce.Nonce[:consensus.SnowmanQueryNonceSize-1]
	if _, err := hm.validatePullQuery(shortPullNonce); err == nil {
		t.Fatal("PullQuery with non-32-byte nonce was accepted")
	}

	wrongChain := proto.Clone(pull).(*pb.PullQuery)
	wrongChain.ChainId++
	wrongChain.Signature, err = key.Sign(consensus.ComputePullQueryDigest(wrongChain))
	if err != nil {
		t.Fatalf("sign wrong-chain PullQuery: %v", err)
	}
	if _, err := hm.validatePullQuery(wrongChain); err == nil {
		t.Fatal("validly signed cross-chain PullQuery was accepted")
	}
}

func TestChitsEnvelopeMustTargetLocalRequester(t *testing.T) {
	oldChainID := pb.GetDefaultChainID()
	pb.SetDefaultChainID(1001)
	t.Cleanup(func() { pb.SetDefaultChainID(oldChainID) })

	hm := &HandlerManager{address: "bob"}
	chits := &pb.Chits{
		Address: "alice", Requester: "bob", ChainId: 1001,
		QueryNonce:    bytes.Repeat([]byte{1}, consensus.SnowmanQueryNonceSize),
		ChitSignature: []byte("signature-presence-is-checked-before-QueryManager-verification"),
	}
	if err := hm.validateChitsEnvelope(chits); err != nil {
		t.Fatalf("valid local Chits envelope rejected: %v", err)
	}
	wrongRequester := proto.Clone(chits).(*pb.Chits)
	wrongRequester.Requester = "carol"
	if err := hm.validateChitsEnvelope(wrongRequester); err == nil {
		t.Fatal("Chits intended for another requester was accepted")
	}
	shortNonce := proto.Clone(chits).(*pb.Chits)
	shortNonce.QueryNonce = shortNonce.QueryNonce[:consensus.SnowmanQueryNonceSize-1]
	if err := hm.validateChitsEnvelope(shortNonce); err == nil {
		t.Fatal("Chits with non-32-byte query nonce was accepted")
	}
}
