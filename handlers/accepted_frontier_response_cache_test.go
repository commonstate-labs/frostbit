package handlers

import (
	"bytes"
	"crypto/sha256"
	"dex/consensus"
	"dex/pb"
	"dex/types"
	"fmt"
	"google.golang.org/protobuf/proto"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func acceptedFrontierCacheTestKey(label string) [sha256.Size]byte {
	return sha256.Sum256([]byte(label))
}

func TestAcceptedFrontierResponseCacheExactReplayReturnsOriginalBytes(t *testing.T) {
	cache := newAcceptedFrontierResponseCache(2)
	key := acceptedFrontierCacheTestKey("request-a")
	original := []byte("signed-response-a")
	builds := 0
	build := func() ([]byte, error) {
		builds++
		return original, nil
	}

	first, err := cache.getOrBuild(key, build)
	if err != nil {
		t.Fatalf("first getOrBuild: %v", err)
	}
	if len(first) == 0 {
		t.Fatal("first response is empty")
	}
	first[0] ^= 0xff
	original[0] ^= 0xff

	second, exists := cache.get(key)
	if !exists {
		t.Fatal("exact replay missed the completed response cache")
	}
	if got, want := string(second), "signed-response-a"; got != want {
		t.Fatalf("replay response=%q want original %q", got, want)
	}
	if builds != 1 {
		t.Fatalf("build count=%d want=1", builds)
	}
}

func TestAcceptedFrontierResponseCacheConcurrentReplayBuildsOnce(t *testing.T) {
	cache := newAcceptedFrontierResponseCache(4)
	key := acceptedFrontierCacheTestKey("concurrent-request")
	const callers = 64

	var builds atomic.Int32
	var admissions atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	start := make(chan struct{})
	var ready sync.WaitGroup
	ready.Add(callers)
	var done sync.WaitGroup
	done.Add(callers)
	errs := make(chan error, callers)
	for i := 0; i < callers; i++ {
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			response, err := cache.getOrBuildAdmitted(key, func() error {
				admissions.Add(1)
				return nil
			}, func() ([]byte, error) {
				if builds.Add(1) == 1 {
					close(entered)
				}
				<-release
				return []byte("one-signed-response"), nil
			})
			if err != nil {
				errs <- err
				return
			}
			if !bytes.Equal(response, []byte("one-signed-response")) {
				errs <- fmt.Errorf("unexpected response %q", response)
			}
		}()
	}

	ready.Wait()
	close(start)
	<-entered
	close(release)
	done.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if got := builds.Load(); got != 1 {
		t.Fatalf("build count=%d want=1", got)
	}
	if got := admissions.Load(); got != 1 {
		t.Fatalf("admission count=%d want=1", got)
	}
}

func TestAcceptedFrontierResponseCacheCapacityEvictsLeastRecentlyUsed(t *testing.T) {
	cache := newAcceptedFrontierResponseCache(2)
	keyA := acceptedFrontierCacheTestKey("request-a")
	keyB := acceptedFrontierCacheTestKey("request-b")
	keyC := acceptedFrontierCacheTestKey("request-c")
	builds := map[[sha256.Size]byte]int{}
	get := func(key [sha256.Size]byte, response string) {
		t.Helper()
		got, err := cache.getOrBuild(key, func() ([]byte, error) {
			builds[key]++
			return []byte(response), nil
		})
		if err != nil {
			t.Fatalf("getOrBuild(%q): %v", response, err)
		}
		if string(got) != response {
			t.Fatalf("response=%q want=%q", got, response)
		}
	}

	get(keyA, "a")
	get(keyB, "b")
	get(keyA, "a")                                                        
	get(keyC, "c")
	get(keyA, "a")
	get(keyB, "b")

	if got := builds[keyA]; got != 1 {
		t.Fatalf("A build count=%d want=1", got)
	}
	if got := builds[keyB]; got != 2 {
		t.Fatalf("B build count=%d want=2 after eviction", got)
	}
	if got := builds[keyC]; got != 1 {
		t.Fatalf("C build count=%d want=1", got)
	}
	if got := len(cache.entries); got > cache.capacity {
		t.Fatalf("cache entries=%d exceeds capacity=%d", got, cache.capacity)
	}
}

func TestAcceptedFrontierReplayKeyBindsAllCanonicalFieldsAndSignature(t *testing.T) {
	base := pb.AcceptedFrontierRequest{
		Version:                consensus.AcceptedFrontierProtocolVersion,
		ChainId:                1,
		RequesterId:            "requester-a",
		Nonce:                  bytes.Repeat([]byte{0x11}, sha256.Size),
		AnchorHeight:           10,
		AnchorBlockId:          "block-10",
		TargetHeight:           12,
		Signature:              []byte("signature-a"),
		RequesterSecpPublicKey: bytes.Repeat([]byte{0x02}, 33),
	}
	key := acceptedFrontierReplayKey(&base)
	if got := acceptedFrontierReplayKey(&base); got != key {
		t.Fatal("same authenticated request produced different replay keys")
	}

	mutations := []struct {
		name   string
		mutate func(*pb.AcceptedFrontierRequest)
	}{
		{name: "version", mutate: func(req *pb.AcceptedFrontierRequest) { req.Version++ }},
		{name: "chain", mutate: func(req *pb.AcceptedFrontierRequest) { req.ChainId++ }},
		{name: "requester", mutate: func(req *pb.AcceptedFrontierRequest) { req.RequesterId = "requester-b" }},
		{name: "nonce", mutate: func(req *pb.AcceptedFrontierRequest) { req.Nonce = bytes.Repeat([]byte{0x22}, sha256.Size) }},
		{name: "anchor height", mutate: func(req *pb.AcceptedFrontierRequest) { req.AnchorHeight++ }},
		{name: "anchor ID", mutate: func(req *pb.AcceptedFrontierRequest) { req.AnchorBlockId = "other-block-10" }},
		{name: "target height", mutate: func(req *pb.AcceptedFrontierRequest) { req.TargetHeight++ }},
		{name: "signature", mutate: func(req *pb.AcceptedFrontierRequest) { req.Signature = []byte("signature-b") }},
		{name: "requester public key", mutate: func(req *pb.AcceptedFrontierRequest) { req.RequesterSecpPublicKey[0]++ }},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			mutated := proto.Clone(&base).(*pb.AcceptedFrontierRequest)
			test.mutate(mutated)
			if got := acceptedFrontierReplayKey(mutated); got == key {
				t.Fatal("mutated request produced the completed-cache key")
			}
		})
	}
}

func TestAcceptedFrontierRejectedAdmissionDoesNotEvictCachedResponse(t *testing.T) {
	cache := newAcceptedFrontierResponseCache(1)
	retainedKey := acceptedFrontierCacheTestKey("retained")
	rejectedKey := acceptedFrontierCacheTestKey("rejected")
	if _, err := cache.getOrBuild(retainedKey, func() ([]byte, error) {
		return []byte("retained-response"), nil
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	wantErr := fmt.Errorf("requester limited")
	if _, err := cache.getOrBuildAdmitted(rejectedKey, func() error {
		return wantErr
	}, func() ([]byte, error) {
		t.Fatal("rejected request reached builder")
		return nil, nil
	}); err != wantErr {
		t.Fatalf("rejected admission err=%v want=%v", err, wantErr)
	}
	if got, exists := cache.get(retainedKey); !exists || string(got) != "retained-response" {
		t.Fatalf("retained entry was evicted: exists=%v response=%q", exists, got)
	}
}

func TestAcceptedFrontierRequesterLimiterUsesIdentityAndLocalRefill(t *testing.T) {
	limiter := newAcceptedFrontierRequesterLimiter(2, 3, 2)
	now := time.Unix(1000, 0)
	requesterA := types.NodeID("requester-a")
	requesterB := types.NodeID("requester-b")

	for i := 0; i < 3; i++ {
		if !limiter.allow(requesterA, now) {
			t.Fatalf("requester A burst request %d was rejected", i+1)
		}
	}
	if limiter.allow(requesterA, now) {
		t.Fatal("requester A exceeded its independent burst")
	}
	if !limiter.allow(requesterB, now) {
		t.Fatal("requester B was limited by requester A traffic")
	}
	if limiter.allow(requesterA, now.Add(499*time.Millisecond)) {
		t.Fatal("requester A refilled a full token too early")
	}
	if !limiter.allow(requesterA, now.Add(500*time.Millisecond)) {
		t.Fatal("requester A did not refill from local elapsed time")
	}
	if !limiter.allow(types.NodeID("requester-c"), now) {
		t.Fatal("new requester was unexpectedly rejected")
	}
	if got := len(limiter.entries); got > limiter.capacity {
		t.Fatalf("limiter entries=%d exceeds capacity=%d", got, limiter.capacity)
	}
}
