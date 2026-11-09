package servicepeer

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"sync"
	"testing"
	"time"
)

func testStore(t *testing.T, candidateCap, verifiedCap int, failureThreshold uint32) *Store {
	t.Helper()
	self := testSigner(t, 1000)
	store, err := NewStore(testChainID, self.GetAddress(), Config{
		CandidateCapacity: candidateCap,
		VerifiedCapacity:  verifiedCap,
		FailureThreshold:  failureThreshold,
		Random:            bytes.NewReader(bytes.Repeat([]byte{0x23}, 64*1024)),
	})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

func TestStoreBoundsUniqueIPAndPromotion(t *testing.T) {
	store := testStore(t, 2, 1, 3)
	r1 := testRecord(t, 1, "8.8.8.1:19000", 1, 1, 100)
	r2 := testRecord(t, 2, "1.1.1.1:19000", 1, 1, 100)
	r3 := testRecord(t, 3, "9.9.9.9:19000", 1, 1, 100)
	for _, record := range []Record{r1, r2, r3} {
		if result, err := store.UpsertCandidate(record); err != nil || result != UpsertAdded {
			t.Fatalf("UpsertCandidate(%s): result=%v err=%v", record.NodeID, result, err)
		}
	}
	if store.CandidateCount() != 2 {
		t.Fatalf("candidate capacity not enforced: %d", store.CandidateCount())
	}
	if _, exists := store.Get(r1.NodeID); exists {
		t.Fatal("oldest candidate was not evicted")
	}

	if err := store.MarkVerified(r2.NodeID, r2.Sequence, 110); err != nil {
		t.Fatalf("MarkVerified: %v", err)
	}
	duplicateIP := testRecord(t, 4, "1.1.1.1:20000", 1, 1, 100)
	if result, err := store.UpsertCandidate(duplicateIP); err != nil || result != UpsertAdded {
		t.Fatalf("same-IP candidate insertion: result=%v err=%v", result, err)
	}
	if err := store.MarkVerified(duplicateIP.NodeID, duplicateIP.Sequence, 110); !errors.Is(err, ErrDuplicateIP) {
		t.Fatalf("duplicate verified public IP: %v", err)
	}
	if store.VerifiedCount() != 1 || store.CandidateCount() != 2 {
		t.Fatalf("promotion counts: verified=%d candidate=%d", store.VerifiedCount(), store.CandidateCount())
	}
	if err := store.MarkVerified(r3.NodeID, r3.Sequence, 110); err != nil {
		t.Fatalf("reverse-verified peer did not rotate full table: %v", err)
	}
	if peer, ok := store.Get(r2.NodeID); !ok || peer.State != StateCandidate {
		t.Fatalf("old verified peer was not rotated back to candidate: %#v", peer)
	}
	if peer, ok := store.Get(r3.NodeID); !ok || peer.State != StateVerified {
		t.Fatalf("new reverse-verified peer was not admitted: %#v", peer)
	}
}

func TestRecordSequenceUpdateDemotesAndRejectsStaleOrConflicting(t *testing.T) {
	store := testStore(t, 4, 2, 3)
	r1 := testRecord(t, 21, "8.8.4.4:19000", 2, 1, 100)
	if _, err := store.UpsertCandidate(r1); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkVerified(r1.NodeID, r1.Sequence, 100); err != nil {
		t.Fatal(err)
	}

	updated := testRecord(t, 21, "1.0.0.1:19000", 3, 50, 150)
	if result, err := store.UpsertCandidate(updated); err != nil || result != UpsertUpdated {
		t.Fatalf("higher sequence update: result=%v err=%v", result, err)
	}
	peer, ok := store.Get(r1.NodeID)
	if !ok || peer.State != StateCandidate || store.VerifiedCount() != 0 {
		t.Fatalf("updated endpoint retained verification: %#v", peer)
	}
	replacement := testRecord(t, 23, "8.8.4.4:20000", 1, 1, 100)
	if _, err := store.UpsertCandidate(replacement); err != nil {
		t.Fatalf("insert peer on released old IP: %v", err)
	}
	if err := store.MarkVerified(replacement.NodeID, replacement.Sequence, 151); err != nil {
		t.Fatalf("endpoint-change demotion did not release old IP: %v", err)
	}
	if err := store.MarkVerified(updated.NodeID, r1.Sequence, 151); !errors.Is(err, ErrSequenceMismatch) {
		t.Fatalf("stale challenge promoted updated record: %v", err)
	}
	if err := store.MarkVerified(updated.NodeID, updated.Sequence, 151); err != nil {
		t.Fatalf("new challenge did not promote record: %v", err)
	}

	stale := testRecord(t, 21, "8.8.4.4:19000", 2, 1, 100)
	if _, err := store.UpsertCandidate(stale); !errors.Is(err, ErrStaleRecord) {
		t.Fatalf("stale record: %v", err)
	}
	conflict := testRecord(t, 21, "9.9.9.9:19000", 3, 50, 150)
	if _, err := store.UpsertCandidate(conflict); !errors.Is(err, ErrConflictingRecord) {
		t.Fatalf("same-sequence equivocation: %v", err)
	}
	if result, err := store.UpsertCandidate(updated); err != nil || result != UpsertUnchanged {
		t.Fatalf("idempotent record: result=%v err=%v", result, err)
	}
}

func TestAvailabilityUpdateKeepsVerifiedEndpoint(t *testing.T) {
	store := testStore(t, 4, 2, 3)
	record := testRecord(t, 22, "8.8.8.8:19000", 2, 1, 100)
	if _, err := store.UpsertCandidate(record); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkVerified(record.NodeID, record.Sequence, 100); err != nil {
		t.Fatal(err)
	}
	updated := testRecord(t, 22, record.Endpoint, 3, 50, 200)
	if result, err := store.UpsertCandidate(updated); err != nil || result != UpsertUpdated {
		t.Fatalf("availability update: result=%v err=%v", result, err)
	}
	peer, ok := store.Get(record.NodeID)
	if !ok || peer.State != StateVerified || peer.Record.Sequence != updated.Sequence || store.VerifiedCount() != 1 {
		t.Fatalf("availability-only update lost verified reachability: %#v", peer)
	}
}

func TestFailureDegradesAndRequiresRepromotion(t *testing.T) {
	store := testStore(t, 4, 2, 2)
	record := testRecord(t, 31, "8.26.56.26:19000", 1, 1, 100)
	if _, err := store.UpsertCandidate(record); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkVerified(record.NodeID, record.Sequence, 100); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkFailure(record.NodeID, 101); err != nil {
		t.Fatal(err)
	}
	peer, _ := store.Get(record.NodeID)
	if peer.State != StateVerified || peer.ConsecutiveFailures != 1 {
		t.Fatalf("premature degradation: %#v", peer)
	}
	if err := store.MarkFailure(record.NodeID, 102); err != nil {
		t.Fatal(err)
	}
	peer, _ = store.Get(record.NodeID)
	if peer.State != StateDegraded || store.VerifiedCount() != 0 || store.CandidateCount() != 1 {
		t.Fatalf("peer not degraded/demoted: %#v", peer)
	}
	peers, err := store.SampleCandidates(SampleOptions{Limit: 4})
	if err != nil || len(peers) != 0 {
		t.Fatalf("degraded peer included by default: peers=%d err=%v", len(peers), err)
	}
	peers, err = store.SampleCandidates(SampleOptions{Limit: 4, IncludeDegraded: true})
	if err != nil || len(peers) != 1 {
		t.Fatalf("degraded peer missing from explicit sample: peers=%d err=%v", len(peers), err)
	}
	replacement := testRecord(t, 32, "8.26.56.26:20000", 1, 1, 100)
	if _, err := store.UpsertCandidate(replacement); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkVerified(replacement.NodeID, replacement.Sequence, 103); err != nil {
		t.Fatalf("failure degradation did not release verified IP: %v", err)
	}
	if !store.Remove(replacement.NodeID) {
		t.Fatal("failed to remove replacement verified peer")
	}
	if err := store.MarkSuccess(record.NodeID, 103); err != nil {
		t.Fatal(err)
	}
	peer, _ = store.Get(record.NodeID)
	if peer.State != StateCandidate || peer.ConsecutiveFailures != 0 {
		t.Fatalf("successful degraded probe did not restore candidacy: %#v", peer)
	}
	if store.VerifiedCount() != 0 {
		t.Fatal("MarkSuccess bypassed explicit challenge promotion")
	}
	if err := store.MarkVerified(record.NodeID, record.Sequence, 104); err != nil {
		t.Fatal(err)
	}
}

func TestDataFailuresAreNotWashedByReachabilityOrGossipSuccess(t *testing.T) {
	store := testStore(t, 4, 2, 2)
	record := testRecord(t, 33, "208.67.222.222:19000", 1, 1, 100)
	if _, err := store.UpsertCandidate(record); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkVerified(record.NodeID, record.Sequence, 100); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDataFailure(record.NodeID, 101); err != nil {
		t.Fatal(err)
	}
	                                                                          
	                                                                           
	if err := store.MarkVerified(record.NodeID, record.Sequence, 102); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkSuccess(record.NodeID, 102); err != nil {
		t.Fatal(err)
	}
	peer, _ := store.Get(record.NodeID)
	if peer.ConsecutiveDataFailures != 1 || peer.State != StateVerified {
		t.Fatalf("connectivity success washed data failure: %#v", peer)
	}
	if err := store.MarkDataFailure(record.NodeID, 103); err != nil {
		t.Fatal(err)
	}
	peer, _ = store.Get(record.NodeID)
	if peer.State != StateDegraded || peer.FailedDataResponses != 2 {
		t.Fatalf("selective sync refusal was not degraded: %#v", peer)
	}
	if err := store.MarkVerified(record.NodeID, record.Sequence, 104); err != nil {
		t.Fatal(err)
	}
	if store.CanServeData(record.NodeID) {
		t.Fatal("connectivity re-probe bypassed data quarantine")
	}
	updated := testRecord(t, 33, "8.8.8.8:19001", 2, 1, 100)
	if _, err := store.UpsertCandidate(updated); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkVerified(updated.NodeID, updated.Sequence, 105); err != nil {
		t.Fatal(err)
	}
	peer, _ = store.Get(record.NodeID)
	if peer.ConsecutiveDataFailures != 2 || store.CanServeData(record.NodeID) {
		t.Fatalf("endpoint update washed data quarantine: %#v", peer)
	}
}

func TestValidatedDataSuccessResetsOnlyDataFailures(t *testing.T) {
	store := testStore(t, 4, 2, 3)
	record := testRecord(t, 34, "208.67.220.220:19000", 1, 1, 100)
	if _, err := store.UpsertCandidate(record); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkVerified(record.NodeID, record.Sequence, 100); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDataFailure(record.NodeID, 101); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDataSuccess(record.NodeID, 102); err != nil {
		t.Fatal(err)
	}
	peer, _ := store.Get(record.NodeID)
	if peer.ConsecutiveDataFailures != 0 || peer.SuccessfulDataResponses != 1 || peer.LastDataSuccessHeight != 102 {
		t.Fatalf("validated data success did not reset data reputation: %#v", peer)
	}
	if !store.CanServeData(record.NodeID) {
		t.Fatal("validated data success did not restore data eligibility")
	}
}

func TestVerifiedSampleFiltersBoundsAndDiversifiesPrefixes(t *testing.T) {
	store := testStore(t, 8, 8, 3)
	records := []Record{
		testRecord(t, 41, "8.8.8.1:19000", 1, 1, 500),
		testRecord(t, 42, "8.8.8.2:19000", 1, 1, 500),
		testRecord(t, 43, "1.1.1.1:19000", 1, 100, 500),
		testRecord(t, 44, "9.9.9.9:19000", 1, 200, 500),
	}
	for _, record := range records {
		if _, err := store.UpsertCandidate(record); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkVerified(record.NodeID, record.Sequence, 500); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkArchiveProof(record.NodeID, record.ArchiveFrom, 500); err != nil {
			t.Fatal(err)
		}
	}
	peers, err := store.SampleVerified(SampleOptions{Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 3 {
		t.Fatalf("sample len=%d", len(peers))
	}
	prefixes := make(map[netip.Prefix]struct{})
	for _, peer := range peers {
		ip, _ := PublicEndpointIP(peer.Record.Endpoint)
		prefixes[netip.PrefixFrom(ip, 24).Masked()] = struct{}{}
	}
	if len(prefixes) != 3 {
		t.Fatalf("diverse /24 prefixes not preferred: %#v", peers)
	}

	rangeFilter := &HeightRange{From: 50, To: 99}
	peers, err = store.SampleVerified(SampleOptions{Limit: 8, Archive: rangeFilter})
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 2 {
		t.Fatalf("stored-block filter returned %d peers, want 2", len(peers))
	}
	for _, peer := range peers {
		if !CoversArchive(peer.Record, *rangeFilter) {
			t.Fatalf("sampled record does not cover range: %#v", peer.Record)
		}
	}
	peers, err = store.SampleVerified(SampleOptions{Limit: 8, MinCheckpoint: 251})
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 0 {                                                     
		t.Fatalf("checkpoint filter returned %d peers, want 0", len(peers))
	}
}

func TestArchiveProofGatesConsumptionAfterIPBoundBootstrapGrace(t *testing.T) {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	self := testSigner(t, 4000)
	store, err := NewStore(testChainID, self.GetAddress(), Config{
		CandidateCapacity: 8,
		CandidatePerIP:    2,
		VerifiedCapacity:  4,
		FailureThreshold:  3,
		BootstrapGrace:    24 * time.Hour,
		Random:            bytes.NewReader(bytes.Repeat([]byte{0x31}, 64*1024)),
		Now:               func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	record := testRecord(t, 4001, "8.8.8.8:19000", 1, 10, 100)
	if _, err := store.UpsertCandidate(record); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkVerified(record.NodeID, record.Sequence, 100); err != nil {
		t.Fatal(err)
	}
	if !store.CanConsumeData(record.NodeID) {
		t.Fatal("newly verified peer was denied finite bootstrap grace")
	}
	if !store.AuthorizeDataConsumption(record.NodeID) {
		t.Fatal("first real block-sync benefit did not start bootstrap grace")
	}
	if _, ok := store.CanProvideArchiveRange(record.NodeID, HeightRange{From: 20, To: 30}); ok {
		t.Fatal("unproved stored-block claim was accepted as a provider")
	}
	if peers, err := store.SampleVerified(SampleOptions{Limit: 1, RequireGossipAccess: true}); err != nil || len(peers) != 1 {
		t.Fatalf("grace peer was excluded from realtime gossip: peers=%d err=%v", len(peers), err)
	}
	now = now.Add(24 * time.Hour)
	if store.CanConsumeData(record.NodeID) {
		t.Fatal("unproved peer retained data access after bootstrap grace")
	}
	if _, ok := store.CanProvideArchiveRange(record.NodeID, HeightRange{From: 20, To: 30}); ok {
		t.Fatal("expired unproved stored-block claim was accepted as a provider")
	}
	if peers, err := store.SampleVerified(SampleOptions{Limit: 1, RequireGossipAccess: true}); err != nil || len(peers) != 0 {
		t.Fatalf("expired unproved peer retained realtime gossip: peers=%d err=%v", len(peers), err)
	}
	if err := store.MarkArchiveProof(record.NodeID, 50, 101); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.CanProvideArchiveRange(record.NodeID, HeightRange{From: 20, To: 30}); !ok {
		t.Fatal("freshly proved stored-block range was not accepted as a provider")
	}
	if !store.CanConsumeData(record.NodeID) {
		t.Fatal("canonical block proof did not restore data access")
	}
	if !store.CanReceiveGossip(record.NodeID) {
		t.Fatal("freshly proved peer was excluded from realtime gossip")
	}
	peers, err := store.SampleVerified(SampleOptions{Limit: 4, Archive: &HeightRange{From: 20, To: 30}})
	if err != nil || len(peers) != 1 {
		t.Fatalf("proved provider was not sampled: peers=%d err=%v", len(peers), err)
	}
	now = now.Add(6 * time.Hour)
	if store.CanReceiveGossip(record.NodeID) {
		t.Fatal("stale proof incorrectly authorized unsolicited gossip")
	}
	if peers, err := store.SampleVerified(SampleOptions{Limit: 1, RequireGossipAccess: true}); err != nil || len(peers) != 0 {
		t.Fatalf("recovery-only peer was sampled for gossip: peers=%d err=%v", len(peers), err)
	}
	if !store.CanConsumeData(record.NodeID) || !store.AuthorizeDataConsumption(record.NodeID) {
		t.Fatal("stale proved peer did not receive its single recovery response")
	}
	if store.CanConsumeData(record.NodeID) || store.AuthorizeDataConsumption(record.NodeID) {
		t.Fatal("recovery debt allowed more than one response before reproof")
	}
	if err := store.MarkArchiveProof(record.NodeID, 50, 102); err != nil {
		t.Fatal(err)
	}

	                                                                           
	                                                                             
	                                                
	updated := testRecord(t, 4001, record.Endpoint, 2, 60, 120)
	if _, err := store.UpsertCandidate(updated); err != nil {
		t.Fatal(err)
	}
	peer, _ := store.Get(record.NodeID)
	if peer.ArchiveProofHeight != 0 || !peer.EverArchiveProved || !store.CanConsumeData(record.NodeID) {
		t.Fatalf("range update retained an out-of-range proof: %#v", peer)
	}
	if !store.AuthorizeDataConsumption(record.NodeID) || store.CanConsumeData(record.NodeID) {
		t.Fatal("range-change recovery was not atomically limited to one response")
	}
	if !store.Remove(record.NodeID) {
		t.Fatal("failed to remove original IP owner")
	}
	rotated := testRecord(t, 4002, "8.8.8.8:20000", 1, 60, 120)
	if _, err := store.UpsertCandidate(rotated); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkVerified(rotated.NodeID, rotated.Sequence, 102); err != nil {
		t.Fatal(err)
	}
	if store.CanConsumeData(rotated.NodeID) {
		t.Fatal("same-IP NodeID rotation refreshed bootstrap grace")
	}
}

func TestBootstrapIPHistoryIsBoundedAndFailsClosed(t *testing.T) {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	self := testSigner(t, 4100)
	store, err := NewStore(testChainID, self.GetAddress(), Config{
		CandidateCapacity:   4,
		CandidatePerIP:      2,
		VerifiedCapacity:    2,
		FailureThreshold:    3,
		BootstrapGrace:      24 * time.Hour,
		BootstrapIPCapacity: 1,
		Random:              bytes.NewReader(bytes.Repeat([]byte{0x41}, 4096)),
		Now:                 func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	first := testRecord(t, 4101, "8.8.8.8:19000", 1, 1, 10)
	if _, err := store.UpsertCandidate(first); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkVerified(first.NodeID, first.Sequence, 1); err != nil {
		t.Fatal(err)
	}
	if !store.CanConsumeData(first.NodeID) {
		t.Fatal("first retained IP did not receive grace")
	}
	if !store.AuthorizeDataConsumption(first.NodeID) {
		t.Fatal("first retained IP could not start grace")
	}
	store.Remove(first.NodeID)
	second := testRecord(t, 4102, "1.1.1.1:19000", 1, 1, 10)
	if _, err := store.UpsertCandidate(second); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkVerified(second.NodeID, second.Sequence, 1); err != nil {
		t.Fatal(err)
	}
	peer, _ := store.Get(second.NodeID)
	if !peer.BootstrapStartedAt.IsZero() || store.CanConsumeData(second.NodeID) {
		t.Fatalf("bootstrap history overflow granted new grace: %#v", peer)
	}
}

func TestIPIdentityAndEligibilityChecksDoNotConsumeGrace(t *testing.T) {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	self := testSigner(t, 4150)
	store, err := NewStore(testChainID, self.GetAddress(), Config{
		CandidateCapacity: 4,
		VerifiedCapacity:  2,
		FailureThreshold:  3,
		BootstrapGrace:    24 * time.Hour,
		Random:            bytes.NewReader(bytes.Repeat([]byte{0x41}, 4096)),
		Now:               func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	record := testRecord(t, 4151, "1.0.0.1:19000", 1, 1, 10)
	if _, err := store.UpsertCandidate(record); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkVerified(record.NodeID, record.Sequence, 1); err != nil {
		t.Fatal(err)
	}
	ip := netip.MustParseAddr("1.0.0.1")
	if _, ok := store.VerifiedByIP(ip); !ok {
		t.Fatal("verified identity lookup failed")
	}
	if _, ok := store.CanConsumeDataByIP(ip); !ok {
		t.Fatal("read-only eligibility check rejected first use")
	}
	peer, _ := store.Get(record.NodeID)
	if !peer.BootstrapStartedAt.IsZero() {
		t.Fatal("read-only admission consumed bootstrap grace")
	}
	now = now.Add(30 * 24 * time.Hour)
	if _, ok := store.AuthorizeDataConsumptionByIP(ip); !ok {
		t.Fatal("first real benefit did not start grace at time of use")
	}
	peer, _ = store.Get(record.NodeID)
	if !peer.BootstrapStartedAt.Equal(now) {
		t.Fatalf("bootstrap started at %v want %v", peer.BootstrapStartedAt, now)
	}
}

func TestFreshProofStillStartsGraceOnFirstRealBenefit(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	self := testSigner(t, 4170)
	store, err := NewStore(testChainID, self.GetAddress(), Config{
		CandidateCapacity: 4,
		VerifiedCapacity:  2,
		FailureThreshold:  3,
		BootstrapGrace:    24 * time.Hour,
		ArchiveProofTTL:   6 * time.Hour,
		Random:            bytes.NewReader(bytes.Repeat([]byte{0x41}, 4096)),
		Now:               func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	record := testRecord(t, 4171, "8.8.8.8:19000", 1, 1, 100)
	if _, err := store.UpsertCandidate(record); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkVerified(record.NodeID, record.Sequence, 1); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkArchiveProof(record.NodeID, 50, 1); err != nil {
		t.Fatal(err)
	}
	peer, _ := store.Get(record.NodeID)
	if !peer.BootstrapStartedAt.IsZero() {
		t.Fatal("proof alone incorrectly started consumption grace")
	}
	if !store.AuthorizeDataConsumption(record.NodeID) {
		t.Fatal("freshly proved peer was denied its first real benefit")
	}
	peer, _ = store.Get(record.NodeID)
	if !peer.BootstrapStartedAt.Equal(now) {
		t.Fatalf("first benefit started grace at %v want %v", peer.BootstrapStartedAt, now)
	}
}

func TestRecoveryAuthorizationIsAtomicUnderConcurrency(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	self := testSigner(t, 4180)
	store, err := NewStore(testChainID, self.GetAddress(), Config{
		CandidateCapacity: 4,
		VerifiedCapacity:  2,
		FailureThreshold:  3,
		BootstrapGrace:    24 * time.Hour,
		ArchiveProofTTL:   6 * time.Hour,
		Random:            bytes.NewReader(bytes.Repeat([]byte{0x41}, 4096)),
		Now:               func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	record := testRecord(t, 4181, "9.9.9.9:19000", 1, 1, 100)
	if _, err := store.UpsertCandidate(record); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkVerified(record.NodeID, record.Sequence, 1); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkArchiveProof(record.NodeID, 50, 1); err != nil {
		t.Fatal(err)
	}
	if !store.AuthorizeDataConsumption(record.NodeID) {
		t.Fatal("failed to start first-use grace")
	}
	now = now.Add(24 * time.Hour)

	const attempts = 32
	start := make(chan struct{})
	results := make(chan bool, attempts)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- store.AuthorizeDataConsumption(record.NodeID)
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	granted := 0
	for ok := range results {
		if ok {
			granted++
		}
	}
	if granted != 1 {
		t.Fatalf("concurrent recovery grants=%d want=1", granted)
	}
}

func TestProviderQuarantineSurvivesSameIPIdentityRotation(t *testing.T) {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	self := testSigner(t, 4200)
	store, err := NewStore(testChainID, self.GetAddress(), Config{
		CandidateCapacity:  8,
		CandidatePerIP:     2,
		VerifiedCapacity:   4,
		FailureThreshold:   2,
		DataFailureBackoff: 2 * time.Minute,
		Random:             bytes.NewReader(bytes.Repeat([]byte{0x42}, 4096)),
		Now:                func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	first := testRecord(t, 4201, "8.8.4.4:19000", 1, 1, 100)
	if _, err := store.UpsertCandidate(first); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkVerified(first.NodeID, first.Sequence, 1); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDataFailure(first.NodeID, 2); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDataFailure(first.NodeID, 3); err != nil {
		t.Fatal(err)
	}
	store.Remove(first.NodeID)

	rotated := testRecord(t, 4202, "8.8.4.4:20000", 1, 1, 100)
	if _, err := store.UpsertCandidate(rotated); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkVerified(rotated.NodeID, rotated.Sequence, 4); err != nil {
		t.Fatal(err)
	}
	if store.CanServeData(rotated.NodeID) {
		t.Fatal("same-IP NodeID rotation washed provider quarantine")
	}
	if err := store.MarkArchiveProof(rotated.NodeID, 50, 4); err != nil {
		t.Fatal(err)
	}
	if store.CanServeData(rotated.NodeID) {
		t.Fatal("one-block contribution proof washed failed-batch quarantine")
	}

	now = now.Add(2 * time.Minute)
	if _, ok := store.CanProvideArchiveRange(rotated.NodeID, HeightRange{From: 20, To: 30}); !ok {
		t.Fatal("provider did not recover after bounded backoff elapsed")
	}
	if err := store.MarkArchiveBatchSuccess(rotated.NodeID, 20, 5); err != nil {
		t.Fatal(err)
	}
	peer, _ := store.Get(rotated.NodeID)
	if peer.ConsecutiveDataFailures != 0 || !peer.DataRetryAfter.IsZero() {
		t.Fatalf("validated frontier batch did not clear provider health: %#v", peer)
	}
}

func TestVerifiedTableRotatesBeyondCapacityConcurrently(t *testing.T) {
	const (
		verifiedCapacity = 64
		peerCount        = 96
	)
	store := testStore(t, 128, verifiedCapacity, 3)
	records := make([]Record, 0, peerCount)
	for i := 0; i < peerCount; i++ {
		                                                                          
		                                         
		records = append(records, testRecord(t, 5000+i, fmt.Sprintf("8.%d.1.1:19000", i+1), 1, 1, 100))
	}

	start := make(chan struct{})
	errs := make(chan error, peerCount)
	var wg sync.WaitGroup
	for _, record := range records {
		record := record
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := store.UpsertCandidate(record); err != nil {
				errs <- err
				return
			}
			if err := store.MarkVerified(record.NodeID, record.Sequence, 1); err != nil {
				errs <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("healthy reverse-verified peer hit a permanent full-table barrier: %v", err)
	}
	if got := store.VerifiedCount(); got != verifiedCapacity {
		t.Fatalf("verified table size=%d want=%d", got, verifiedCapacity)
	}
	if got := store.CandidateCount(); got != peerCount-verifiedCapacity {
		t.Fatalf("rotated candidate count=%d want=%d", got, peerCount-verifiedCapacity)
	}
}

func TestStoreCopiesAndConcurrency(t *testing.T) {
	store := testStore(t, 16, 16, 3)
	record := testRecord(t, 51, "4.2.2.1:19000", 1, 1, 100)
	if _, err := store.UpsertCandidate(record); err != nil {
		t.Fatal(err)
	}
	peer, _ := store.Get(record.NodeID)
	peer.Record.SecpPublicKey[0] ^= 0xff
	peer.Record.Signature[0] ^= 0xff
	peerAgain, _ := store.Get(record.NodeID)
	if !bytes.Equal(peerAgain.Record.SecpPublicKey, record.SecpPublicKey) || !bytes.Equal(peerAgain.Record.Signature, record.Signature) {
		t.Fatal("Get leaked mutable store slices")
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_, _ = store.Get(record.NodeID)
				_ = store.Snapshot()
			}
		}()
	}
	wg.Wait()
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestSampleReportsRandomFailure(t *testing.T) {
	self := testSigner(t, 2000)
	store, err := NewStore(testChainID, self.GetAddress(), Config{CandidateCapacity: 4, VerifiedCapacity: 4, FailureThreshold: 2, Random: failingReader{}})
	if err != nil {
		t.Fatal(err)
	}
	for i, endpoint := range []string{"8.8.8.8:19000", "1.1.1.1:19000"} {
		record := testRecord(t, 2100+i, endpoint, 1, 1, 100)
		if _, err := store.UpsertCandidate(record); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.SampleCandidates(SampleOptions{Limit: 2}); err == nil {
		t.Fatal("sample silently ignored CSPRNG failure")
	}
}

func TestCandidateIPPoisoningCannotClaimOrReleaseVerifiedOwnership(t *testing.T) {
	store := testStore(t, 2, 2, 3)
	poison := testRecord(t, 61, "64.6.64.6:19000", 1, 1, 100)
	owner := testRecord(t, 62, "64.6.64.6:20000", 1, 1, 100)
	for _, record := range []Record{poison, owner} {
		if _, err := store.UpsertCandidate(record); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.MarkVerified(owner.NodeID, owner.Sequence, 100); err != nil {
		t.Fatalf("earlier same-IP candidate squatted on ownership: %v", err)
	}

	contender := testRecord(t, 63, "64.6.64.6:30000", 1, 1, 100)
	evictor := testRecord(t, 64, "64.6.64.6:40000", 1, 1, 100)
	if _, err := store.UpsertCandidate(contender); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertCandidate(evictor); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Get(poison.NodeID); ok {
		t.Fatal("oldest poison candidate was not evicted")
	}
	if err := store.MarkVerified(evictor.NodeID, evictor.Sequence, 101); !errors.Is(err, ErrDuplicateIP) {
		t.Fatalf("candidate eviction erased verified ownership: %v", err)
	}
	if !store.Remove(evictor.NodeID) {
		t.Fatal("failed to remove same-IP candidate")
	}
	if err := store.MarkVerified(contender.NodeID, contender.Sequence, 102); !errors.Is(err, ErrDuplicateIP) {
		t.Fatalf("candidate removal erased verified ownership: %v", err)
	}
	peer, ok := store.Get(contender.NodeID)
	if !ok || peer.State != StateCandidate || store.VerifiedCount() != 1 {
		t.Fatalf("failed promotion was not atomic: peer=%#v verified=%d", peer, store.VerifiedCount())
	}
	if !store.Remove(owner.NodeID) {
		t.Fatal("failed to remove verified owner")
	}
	if err := store.MarkVerified(contender.NodeID, contender.Sequence, 103); err != nil {
		t.Fatalf("verified removal did not release IP: %v", err)
	}
}

func TestCandidateChurnIsBoundedPerPublicIP(t *testing.T) {
	store := testStore(t, 16, 4, 3)
	honest := testRecord(t, 70, "1.1.1.1:19000", 1, 1, 100)
	if _, err := store.UpsertCandidate(honest); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		record := testRecord(t, 80+i, fmt.Sprintf("8.8.8.8:%d", 19000+i), 1, 1, 100)
		if _, err := store.UpsertCandidate(record); err != nil {
			t.Fatal(err)
		}
	}
	if store.CandidateCount() != 3 {
		t.Fatalf("same-IP candidate churn consumed table: count=%d want=3", store.CandidateCount())
	}
	if _, ok := store.Get(honest.NodeID); !ok {
		t.Fatal("same-IP candidate churn evicted unrelated honest candidate")
	}
}

func TestNewStoreRejectsInvalidConfiguration(t *testing.T) {
	if _, err := NewStore(0, "", Config{}); !errors.Is(err, ErrWrongChain) {
		t.Fatalf("zero chain: %v", err)
	}
	if _, err := NewStore(testChainID, "not-an-address", Config{}); err == nil {
		t.Fatal("invalid self ID accepted")
	}
	self := testSigner(t, 3000)
	if _, err := NewStore(testChainID, self.GetAddress(), Config{CandidateCapacity: -1, VerifiedCapacity: 1}); err == nil {
		t.Fatal("negative candidate capacity accepted")
	}
	if _, err := NewStore(testChainID, self.GetAddress(), Config{CandidateCapacity: 1, CandidatePerIP: -1, VerifiedCapacity: 1}); err == nil {
		t.Fatal("negative per-IP candidate capacity accepted")
	}
	if _, err := NewStore(testChainID, self.GetAddress(), Config{CandidateCapacity: 1, CandidatePerIP: 1, VerifiedCapacity: 1, BootstrapIPCapacity: -1}); err == nil {
		t.Fatal("negative bootstrap IP capacity accepted")
	}
}

func ExampleStore() {
	                                                                            
	                                                                            
	fmt.Println(StateCandidate, StateVerified, StateDegraded)
	                
}
