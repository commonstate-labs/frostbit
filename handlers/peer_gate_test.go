package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"dex/config"
	"dex/consensus"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"dex/utils"

	"google.golang.org/protobuf/proto"
)

                                                                                                       
                                                  
                                                               
                                                                 
                         
                                                                                                 

                                                           

func TestExtractIP(t *testing.T) {
	cases := map[string]string{
		"1.2.3.4:5678": "1.2.3.4",
		"[::1]:443":    "::1",
		"badformat":    "badformat",                                         
	}
	for raw, want := range cases {
		req := httptest.NewRequest("GET", "/x", nil)
		req.RemoteAddr = raw
		if got := extractIP(req); got != want {
			t.Errorf("extractIP(%q)=%q want %q", raw, got, want)
		}
	}
}

                                                                                                      
func TestComputePushQueryDigest_DeterministicAndFieldSensitive(t *testing.T) {
	base := &pb.PushQuery{
		RequestId:        1,
		Address:          "alice",
		ContainerIsBlock: true,
		Container:        []byte("block body"),
		RequestedHeight:  9,
		BlockId:          "blkA",
		Nonce:            bytes.Repeat([]byte{7}, consensus.SnowmanQueryNonceSize),
		VrfSeed:          []byte("seed"),
		SeqId:            3,
		Responder:        "bob",
		ChainId:          100,
	}
	d1 := computePushQueryDigest(base)
	d2 := computePushQueryDigest(proto.Clone(base).(*pb.PushQuery))
	if string(d1) != string(d2) {
		t.Fatal("digest must be deterministic")
	}
	mutate := func(fn func(*pb.PushQuery)) []byte {
		query := proto.Clone(base).(*pb.PushQuery)
		fn(query)
		return computePushQueryDigest(query)
	}
	mut := []struct {
		name string
		got  []byte
	}{
		{"requestID", mutate(func(q *pb.PushQuery) { q.RequestId++ })},
		{"address", mutate(func(q *pb.PushQuery) { q.Address = "mallory" })},
		{"responder", mutate(func(q *pb.PushQuery) { q.Responder = "carol" })},
		{"chainID", mutate(func(q *pb.PushQuery) { q.ChainId++ })},
		{"container type", mutate(func(q *pb.PushQuery) { q.ContainerIsBlock = false })},
		{"container", mutate(func(q *pb.PushQuery) { q.Container = []byte("other") })},
		{"height", mutate(func(q *pb.PushQuery) { q.RequestedHeight++ })},
		{"blockID", mutate(func(q *pb.PushQuery) { q.BlockId = "blkB" })},
		{"nonce", mutate(func(q *pb.PushQuery) { q.Nonce[0]++ })},
		{"vrf seed", mutate(func(q *pb.PushQuery) { q.VrfSeed = []byte("other") })},
		{"seqID", mutate(func(q *pb.PushQuery) { q.SeqId++ })},
	}
	for _, m := range mut {
		if string(m.got) == string(d1) {
			t.Errorf("digest must change when %s mutates", m.name)
		}
	}
}

                                                                            

func newTestPeerGate(t *testing.T) *PeerGate {
	t.Helper()
	cfg := config.DefaultPeerGateConfig()
	pg := NewPeerGate(cfg, logs.NewNodeLogger("pg-test", 0))
	t.Cleanup(pg.Stop)
	return pg
}

type peerGateTestDB struct {
	accounts map[string]*pb.Account
	values   map[string][]byte
}

func (db *peerGateTestDB) GetAccount(address string) (*pb.Account, error) {
	if db == nil {
		return nil, nil
	}
	return db.accounts[address], nil
}

func (db *peerGateTestDB) Scan(prefix string) (map[string][]byte, error) {
	result := make(map[string][]byte)
	if db == nil {
		return result, nil
	}
	for key, value := range db.values {
		if strings.HasPrefix(key, prefix) {
			result[key] = append([]byte(nil), value...)
		}
	}
	return result, nil
}

func (db *peerGateTestDB) Get(key string) ([]byte, error) {
	if db == nil {
		return nil, nil
	}
	return append([]byte(nil), db.values[key]...), nil
}

func TestPeerGate_PendingTransitionParticipantsRespectFailureBoundary(t *testing.T) {
	pg := newTestPeerGate(t)
	db := &peerGateTestDB{values: make(map[string][]byte)}
	addTransition := func(chain string, vaultID uint32, epoch uint64, status, lifecycle string, oldMembers, newMembers []string) {
		t.Helper()
		transitionKey := keys.KeyFrostVaultTransition(chain, vaultID, epoch)
		data, err := proto.Marshal(&pb.VaultTransitionState{
			Chain:               chain,
			VaultId:             vaultID,
			EpochId:             epoch,
			DkgStatus:           status,
			Lifecycle:           lifecycle,
			OldCommitteeMembers: oldMembers,
			NewCommitteeMembers: newMembers,
		})
		if err != nil {
			t.Fatalf("marshal transition: %v", err)
		}
		db.values[transitionKey] = data
		db.values[keys.KeyFrostVaultTransitionActive(chain, vaultID, epoch)] = []byte(transitionKey)
	}

	addTransition("btc", 1, 2, "SHARING", "DRAINING", []string{"sharing-old"}, []string{"sharing-new"})
	addTransition("btc", 2, 3, "KEY_READY", "DRAINING", []string{"ready-old"}, []string{"ready-new"})
	addTransition("btc", 3, 4, "FAILED", "DRAINING", []string{"failed-old"}, []string{"failed-new"})
	addTransition("btc", 4, 5, "KEY_READY", "ACTIVE", []string{"stale-old"}, []string{"stale-new"})
	pg.dbManager = db

	tests := []struct {
		address string
		want    bool
	}{
		{address: "sharing-old", want: true},
		{address: "sharing-new", want: true},
		{address: "ready-old", want: true},
		{address: "ready-new", want: true},
		{address: "failed-old", want: true},
		{address: "failed-new", want: false},
		{address: "stale-old", want: false},
		{address: "stale-new", want: false},
		{address: "unrelated", want: false},
	}
	for _, test := range tests {
		t.Run(test.address, func(t *testing.T) {
			if got := pg.isPendingTransitionParticipant(test.address); got != test.want {
				t.Fatalf("pending participant=%v, want %v", got, test.want)
			}
		})
	}
}

func TestPeerGate_TrustedFastPathOnlyAppliesToLegacyBlockRoutes(t *testing.T) {
	pg := newTestPeerGate(t)
	pg.promote("1.2.3.4", "alice")

	putCalled := false
	putHandler := pg.Middleware("/put", func(w http.ResponseWriter, r *http.Request) {
		putCalled = true
		w.WriteHeader(http.StatusOK)
	})
	putReq := httptest.NewRequest(http.MethodPost, "/put", nil)
	putReq.RemoteAddr = "1.2.3.4:1234"
	putRR := httptest.NewRecorder()
	putHandler(putRR, putReq)
	if !putCalled || putRR.Code != http.StatusOK {
		t.Fatalf("trusted /put must use fast path, called=%v code=%d", putCalled, putRR.Code)
	}
	                                                                            
	                                                                           
	if !pg.allowUnknown("1.2.3.4") || pg.allowUnknown("1.2.3.4") {
		t.Fatal("test setup failed to exhaust unknown limiter")
	}

	pushCalled := false
	pushHandler := pg.Middleware("/pushquery", func(w http.ResponseWriter, r *http.Request) {
		pushCalled = true
		w.WriteHeader(http.StatusOK)
	})
	pushReq := httptest.NewRequest(http.MethodPost, "/pushquery", nil)
	pushReq.RemoteAddr = "1.2.3.4:1234"
	pushRR := httptest.NewRecorder()
	pushHandler(pushRR, pushReq)
	if pushCalled {
		t.Fatal("trusted /pushquery must still pass per-request verification")
	}
	if pushRR.Code != http.StatusForbidden {
		t.Fatalf("trusted /pushquery must bypass exhausted unknown limiter and fail verification, got %d", pushRR.Code)
	}
}

func TestPeerGate_TrustedPushUsesTrustedLimiterAndStillVerifiesSignature(t *testing.T) {
	consensus.ClearNodePublicKeys()
	consensus.SetNodePublicKeyResolver(nil)
	t.Cleanup(func() {
		consensus.ClearNodePublicKeys()
		consensus.SetNodePublicKeyResolver(nil)
	})
	key := utils.NewKeyManager()
	if err := key.InitKeyRandom(); err != nil {
		t.Fatalf("init key: %v", err)
	}
	consensus.RegisterNodePublicKey("alice", key.PublicKeyBytes())

	pg := newTestPeerGate(t)
	pg.promote("1.2.3.4", "alice")
	if !pg.allowUnknown("1.2.3.4") || pg.allowUnknown("1.2.3.4") {
		t.Fatal("test setup failed to exhaust unknown limiter")
	}
	msg := &pb.PushQuery{
		Address:          "alice",
		Responder:        "bob",
		ChainId:          pb.GetDefaultChainID(),
		RequestId:        7,
		ContainerIsBlock: true,
		Container:        []byte("signed-container"),
		RequestedHeight:  1,
		BlockId:          "block-1",
		VrfSeed:          []byte("seed"),
		SeqId:            2,
		Nonce:            bytes.Repeat([]byte{1}, consensus.SnowmanQueryNonceSize),
	}
	var err error
	msg.Signature, err = key.Sign(consensus.ComputePushQueryDigest(msg))
	if err != nil {
		t.Fatalf("sign PushQuery: %v", err)
	}
	body, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal PushQuery: %v", err)
	}

	called := false
	handler := pg.Middleware("/pushquery", func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodPost, "/pushquery", bytes.NewReader(body))
	req.RemoteAddr = "1.2.3.4:1234"
	rr := httptest.NewRecorder()
	handler(rr, req)
	if !called || rr.Code != http.StatusOK {
		t.Fatalf("valid trusted PushQuery was throttled or rejected: called=%v code=%d", called, rr.Code)
	}
}

func TestPeerGate_PendingBlocksIsReadOnlyDiagnostics(t *testing.T) {
	pg := newTestPeerGate(t)
	called := false
	handler := pg.Middleware("/pendingblocks", func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/pendingblocks", nil)
	req.RemoteAddr = "5.6.7.8:1234"
	rr := httptest.NewRecorder()
	handler(rr, req)

	if !called || rr.Code != http.StatusOK {
		t.Fatalf("/pendingblocks must be readable without consensus signature, called=%v code=%d", called, rr.Code)
	}
	if pg.isTrusted("5.6.7.8") {
		t.Fatal("/pendingblocks must not promote diagnostic callers into trusted peers")
	}
}

func TestPeerGate_ChitsAdmissionNeverPromotesWithoutActivePollVerification(t *testing.T) {
	consensus.ClearNodePublicKeys()
	consensus.SetNodePublicKeyResolver(nil)
	t.Cleanup(func() {
		consensus.ClearNodePublicKeys()
		consensus.SetNodePublicKeyResolver(nil)
	})

	pg := newTestPeerGate(t)
	pg.SetDeps(nil, &peerGateTestDB{
		accounts: map[string]*pb.Account{
			"alice": {
				Address: "alice",
				IsMiner: true,
				Ip:      "10.0.0.8:9000",
			},
		},
	})

	body, err := proto.Marshal(&pb.Chits{
		RequestId:              42,
		Address:                "alice",
		PreferredBlock:         "block-1",
		PreferredBlockAtHeight: 1,
		ChitSignature:          []byte("signature-bound-to-vrf-seed-and-seq-id"),
	})
	if err != nil {
		t.Fatalf("marshal chits: %v", err)
	}

	if pg.verifyChitAdmission("10.0.0.9", body) {
		t.Fatal("source IP mismatch must block /chits admission")
	}
	if pg.isTrusted("10.0.0.9") {
		t.Fatal("mismatched source IP must not become trusted")
	}

	if !pg.verifyChitAdmission("10.0.0.8", body) {
		t.Fatal("matching source IP should allow bounded /chits admission")
	}
	if pg.verifyAndPromote("10.0.0.8", "/chits", body) {
		t.Fatal("PeerGate must not pretend signature presence is full Chit verification")
	}
	if pg.isTrusted("10.0.0.8") {
		t.Fatal("Chits admission must never promote an IP before active-poll verification")
	}
}

func TestPeerGateUnknownChitsUsesSeparateLimiterAndCannotUnlockTrustedOnlyPath(t *testing.T) {
	pg := newTestPeerGate(t)
	pg.SetDeps(nil, &peerGateTestDB{accounts: map[string]*pb.Account{
		"alice": {Address: "alice", IsMiner: true, Ip: "10.0.0.8:9000"},
	}})
	body, err := proto.Marshal(&pb.Chits{
		Address:                "alice",
		Requester:              "local",
		ChainId:                pb.GetDefaultChainID(),
		PreferredBlockAtHeight: 1,
		QueryNonce:             bytes.Repeat([]byte{1}, consensus.SnowmanQueryNonceSize),
		ChitSignature:          []byte("arbitrary-non-empty-bytes"),
	})
	if err != nil {
		t.Fatalf("marshal Chits: %v", err)
	}

	called := false
	handler := pg.Middleware("/chits", func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodPost, "/chits", bytes.NewReader(body))
	req.RemoteAddr = "10.0.0.8:1234"
	rr := httptest.NewRecorder()
	handler(rr, req)
	if !called || rr.Code != http.StatusOK {
		t.Fatalf("bounded Chits admission did not reach full verifier: called=%v code=%d", called, rr.Code)
	}
	if pg.isTrusted("10.0.0.8") {
		t.Fatal("arbitrary Chit signature bytes promoted an unverified IP")
	}
	putCalled := false
	put := pg.Middleware("/put", func(w http.ResponseWriter, r *http.Request) {
		putCalled = true
	})
	putReq := httptest.NewRequest(http.MethodPost, "/put", nil)
	putReq.RemoteAddr = "10.0.0.8:1234"
	putRR := httptest.NewRecorder()
	put(putRR, putReq)
	                                                                       
	                                                                            
	                                                         
	if putCalled || putRR.Code != http.StatusForbidden {
		t.Fatalf("unverified Chit unlocked /put or consumed unknown admission: called=%v code=%d", putCalled, putRR.Code)
	}
}

func TestPeerGate_PushQueryPromotionRequiresValidSignatureAndEndpoint(t *testing.T) {
	consensus.ClearNodePublicKeys()
	consensus.SetNodePublicKeyResolver(nil)
	t.Cleanup(func() {
		consensus.ClearNodePublicKeys()
		consensus.SetNodePublicKeyResolver(nil)
	})

	km := utils.NewKeyManager()
	if err := km.InitKeyRandom(); err != nil {
		t.Fatalf("init random key: %v", err)
	}

	pg := newTestPeerGate(t)
	pg.SetDeps(nil, &peerGateTestDB{
		accounts: map[string]*pb.Account{
			"alice": {
				Address: "alice",
				IsMiner: true,
				Ip:      "10.0.0.8:9000",
				PublicKeys: &pb.PublicKeys{Keys: map[int32][]byte{
					int32(pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1_DEX): km.PublicKeyBytes(),
				}},
			},
		},
	})

	msg := &pb.PushQuery{
		RequestId: 7,
		Address:   "alice",
		Responder: "bob",
		ChainId:   1,
		BlockId:   "block-7",
		Nonce:     bytes.Repeat([]byte{11}, consensus.SnowmanQueryNonceSize),
	}
	digest := computePushQueryDigest(msg)
	sig, err := km.Sign(digest)
	if err != nil {
		t.Fatalf("sign pushquery: %v", err)
	}
	msg.Signature = sig
	body, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal pushquery: %v", err)
	}

	if pg.verifyAndPromote("10.0.0.9", "/pushquery", body) {
		t.Fatal("source IP mismatch must block /pushquery promotion")
	}
	if !pg.verifyAndPromote("10.0.0.8", "/pushquery", body) {
		t.Fatal("valid signature and matching endpoint should promote /pushquery sender")
	}
}

func TestPeerGate_PullQueryPromotionRequiresV2Signature(t *testing.T) {
	consensus.ClearNodePublicKeys()
	consensus.SetNodePublicKeyResolver(nil)
	t.Cleanup(func() {
		consensus.ClearNodePublicKeys()
		consensus.SetNodePublicKeyResolver(nil)
	})

	km := utils.NewKeyManager()
	if err := km.InitKeyRandom(); err != nil {
		t.Fatalf("init random key: %v", err)
	}
	pg := newTestPeerGate(t)
	pg.SetDeps(nil, &peerGateTestDB{accounts: map[string]*pb.Account{
		"alice": {
			Address: "alice", IsMiner: true, Ip: "10.0.0.8:9000",
			PublicKeys: &pb.PublicKeys{Keys: map[int32][]byte{
				int32(pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1_DEX): km.PublicKeyBytes(),
			}},
		},
	}})

	pull := &pb.PullQuery{
		RequestId: 9, Address: "alice", Responder: "bob", ChainId: 1,
		BlockId: "block-9", RequestedHeight: 9, VrfSeed: []byte("seed"), SeqId: 4,
		Nonce: bytes.Repeat([]byte{12}, consensus.SnowmanQueryNonceSize),
	}
	unsigned, err := proto.Marshal(pull)
	if err != nil {
		t.Fatalf("marshal unsigned PullQuery: %v", err)
	}
	if pg.verifyAndPromote("10.0.0.8", "/pullquery", unsigned) {
		t.Fatal("unsigned PullQuery promoted sender")
	}
	pull.Signature, err = km.Sign(consensus.ComputePullQueryDigest(pull))
	if err != nil {
		t.Fatalf("sign PullQuery: %v", err)
	}
	signed, err := proto.Marshal(pull)
	if err != nil {
		t.Fatalf("marshal signed PullQuery: %v", err)
	}
	if !pg.verifyAndPromote("10.0.0.8", "/pullquery", signed) {
		t.Fatal("valid signed PullQuery with matching endpoint was rejected")
	}
}

func TestPeerGate_PromoteAndTrustedHitCount(t *testing.T) {
	pg := newTestPeerGate(t)
	pg.promote("1.2.3.4", "alice")

	if !pg.isTrusted("1.2.3.4") {
		t.Fatal("promoted IP must be trusted")
	}
	                  
	for i := 0; i < 5; i++ {
		_ = pg.isTrusted("1.2.3.4")
	}
	pg.mu.RLock()
	entry := pg.trustedIPs["1.2.3.4"]
	pg.mu.RUnlock()
	if entry == nil || entry.HitCount < 6 {
		t.Fatalf("HitCount must reflect lookups, got %+v", entry)
	}
	if entry.Address != "alice" {
		t.Fatalf("trusted entry address mismatch: %+v", entry)
	}
}

func TestPeerGate_UnknownIPNotTrusted(t *testing.T) {
	pg := newTestPeerGate(t)
	if pg.isTrusted("9.9.9.9") {
		t.Fatal("unknown IP must not be trusted")
	}
}

                                                                                    

func TestPeerGate_RecordFailureBansAtThreshold(t *testing.T) {
	pg := newTestPeerGate(t)
	pg.banThreshold = 3
	                                                                                                      
	pg.banDuration = 2 * time.Second

	ip := "5.5.5.5"
	for i := 0; i < pg.banThreshold-1; i++ {
		pg.recordFailure(ip)
		if pg.isBanned(ip) {
			t.Fatalf("ban triggered too early at i=%d", i)
		}
	}
	pg.recordFailure(ip)
	if !pg.isBanned(ip) {
		t.Fatalf("must be banned after %d failures", pg.banThreshold)
	}
	                                                                                                     
	pg.banUntil.Store(ip, time.Now().Add(-time.Second).Unix())
	if pg.isBanned(ip) {
		t.Fatal("ban must be considered expired when banUntil < now")
	}
}

                                                                                                   
func TestPeerGate_RecordFailureClearsCountAfterBan(t *testing.T) {
	pg := newTestPeerGate(t)
	pg.banThreshold = 2

	ip := "6.6.6.6"
	pg.recordFailure(ip)
	pg.recordFailure(ip)

	                                   
	if v, ok := pg.failCounts.Load(ip); ok {
		t.Fatalf("failCounts must be cleared after ban, got %v", v)
	}
}

                                                     

func TestPeerGate_GetBlockUsesSyncRateLimit(t *testing.T) {
	pg := newTestPeerGate(t)
	if !pg.syncPaths["/getblock"] {
		t.Fatal("/getblock must use the sync limiter so deep-sync anchor fetches are not throttled as unknown traffic")
	}
}

                                                                                 
func TestPeerGate_RateLimits_PerTier(t *testing.T) {
	pg := newTestPeerGate(t)
	                                   
	pg.trustedRPS, pg.trustedBurst = 1000, 1
	pg.syncRPS, pg.syncBurst = 1000, 1
	pg.unknownRPS, pg.unknownBurst = 1000, 1

	          
	if !pg.allowTrusted("a") {
		t.Fatal("trusted first call must pass")
	}
	if pg.allowTrusted("a") {
		t.Fatal("trusted second call within tick must be rejected (burst=1)")
	}

	       
	if !pg.allowSync("b") || pg.allowSync("b") {
		t.Fatal("sync limiter must allow then reject within burst=1")
	}

	          
	if !pg.allowUnknown("c") || pg.allowUnknown("c") {
		t.Fatal("unknown limiter must allow then reject within burst=1")
	}
}

                                                                            
func TestPeerGate_RateLimits_PerIPIsolated(t *testing.T) {
	pg := newTestPeerGate(t)
	pg.trustedRPS, pg.trustedBurst = 1, 1

	if !pg.allowTrusted("ip-1") {
		t.Fatal("first ip-1 call must pass")
	}
	if !pg.allowTrusted("ip-2") {
		t.Fatal("ip-2 must have its own bucket and pass")
	}
}

                                                                                          
func TestPeerGate_AllowPath(t *testing.T) {
	pg := newTestPeerGate(t)
	pg.defaultPathRPS = 1
	pg.defaultPathBurst = 1
	                                                  
	if !pg.allowPath("/no-rate-config") {
		t.Fatal("first unconfigured path request must pass")
	}
	if pg.allowPath("/no-rate-config") {
		t.Fatal("unconfigured path must use the finite default bucket")
	}

	pg.pathRates = map[string]float64{"/limited": 1}
	if !pg.allowPath("/limited") {
		t.Fatal("first /limited call must pass")
	}
	if pg.allowPath("/limited") {
		t.Fatal("second /limited call within tick must be rejected (rps=1, burst=1)")
	}
}

func TestPeerGateInflightReserveProtectsConsensus(t *testing.T) {
	cfg := config.DefaultPeerGateConfig()
	cfg.MaxInflight = 2
	cfg.ConsensusReserve = 1
	pg := NewPeerGate(cfg, logs.NewNodeLogger("pg-inflight-test", 0))
	t.Cleanup(pg.Stop)

	releaseQuery, ok := pg.acquireInflight("/status", false)
	if !ok {
		t.Fatal("first non-consensus request must acquire capacity")
	}
	if _, ok := pg.acquireInflight("/status", false); ok {
		t.Fatal("non-consensus request consumed the consensus reserve")
	}
	if _, ok := pg.acquireInflight("/pushquery", false); ok {
		t.Fatal("unverified consensus request consumed the reserve")
	}
	releaseConsensus, ok := pg.acquireInflight("/pushquery", true)
	if !ok {
		t.Fatal("consensus request must use reserved capacity")
	}
	if _, ok := pg.acquireInflight("/chits", true); ok {
		t.Fatal("total in-flight limit was not enforced")
	}
	releaseQuery()
	releaseConsensus()
}

func TestPeerGateInflightZeroUsesAutomaticCapacity(t *testing.T) {
	cfg := config.DefaultPeerGateConfig()
	cfg.MaxInflight = 0
	cfg.ConsensusReserve = 0
	resolved := config.ResolvePeerGateInflight(cfg)
	pg := NewPeerGate(cfg, logs.NewNodeLogger("pg-auto-inflight-test", 0))
	t.Cleanup(pg.Stop)

	if got := cap(pg.totalInflight); got != resolved.MaxInflight {
		t.Fatalf("total in-flight capacity=%d, want auto=%d", got, resolved.MaxInflight)
	}
	if got := cap(pg.nonConsensusInflight); got != resolved.MaxInflight-resolved.ConsensusReserve {
		t.Fatalf("non-consensus capacity=%d, want auto=%d", got, resolved.MaxInflight-resolved.ConsensusReserve)
	}
}

func TestPeerGateInflightCapsTxBatchSeparately(t *testing.T) {
	cfg := config.DefaultPeerGateConfig()
	cfg.MaxInflight = 10
	cfg.ConsensusReserve = 2
	cfg.TxBatchMaxInflight = 2
	pg := NewPeerGate(cfg, logs.NewNodeLogger("pg-txbatch-inflight-test", 0))
	t.Cleanup(pg.Stop)

	release1, ok := pg.acquireInflight("/txbatch", false)
	if !ok {
		t.Fatal("first tx batch must acquire capacity")
	}
	release2, ok := pg.acquireInflight("/txbatch", false)
	if !ok {
		t.Fatal("second tx batch must acquire capacity")
	}
	if _, ok := pg.acquireInflight("/txbatch", false); ok {
		t.Fatal("tx batch-specific in-flight limit was not enforced")
	}
	if releaseQuery, ok := pg.acquireInflight("/status", false); !ok {
		t.Fatal("tx batch saturation must not consume every query slot")
	} else {
		releaseQuery()
	}
	release1()
	release2()
}

                                                                            
func TestPeerGate_GetLimiterIdempotent(t *testing.T) {
	pg := newTestPeerGate(t)

	const N = 64
	var wg sync.WaitGroup
	wg.Add(N)
	results := make([]uintptr, N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			l := pg.getLimiter(&pg.trustedLimiters, "x", 100, 10)
			results[i] = reflect.ValueOf(l).Pointer()
		}(i)
	}
	wg.Wait()
	first := results[0]
	for i, r := range results {
		if r != first {
			t.Fatalf("getLimiter created multiple instances: results[0]=%x results[%d]=%x", first, i, r)
		}
	}
}

func TestPeerGateTemporaryIPStateIsBoundedAndReclaimed(t *testing.T) {
	cfg := config.DefaultPeerGateConfig()
	cfg.MaxTrackedIPs = 2
	cfg.BanThreshold = 10                                                   
	pg := NewPeerGate(cfg, logs.NewNodeLogger("pg-bounded-state-test", 0))
	t.Cleanup(pg.Stop)

	for _, ip := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"} {
		pg.recordFailure(ip)
	}
	if got := syncMapLen(&pg.failCounts); got != 2 {
		t.Fatalf("failure state entries=%d, want hard cap 2", got)
	}

	first := pg.getBoundedLimiter(&pg.unknownLimiters, pg.unknownOverflow, "10.0.1.1", 100, 1)
	second := pg.getBoundedLimiter(&pg.unknownLimiters, pg.unknownOverflow, "10.0.1.2", 100, 1)
	overflow := pg.getBoundedLimiter(&pg.unknownLimiters, pg.unknownOverflow, "10.0.1.3", 100, 1)
	if first == pg.unknownOverflow || second == pg.unknownOverflow {
		t.Fatal("first two sources must retain their independent per-IP buckets")
	}
	if overflow != pg.unknownOverflow {
		t.Fatal("sources over the cap must use the shared overflow bucket")
	}
	if got := pg.limiterEntries; got != 2 {
		t.Fatalf("limiter entries=%d, want hard cap 2", got)
	}

	pg.cleanup()
	if got := syncMapLen(&pg.failCounts); got != 0 {
		t.Fatalf("cleanup must reclaim failure state, got %d entries", got)
	}
	if got := pg.limiterEntries; got != 0 {
		t.Fatalf("cleanup must reclaim limiter capacity, got %d entries", got)
	}

	pg.recordFailure("10.0.0.4")
	if got := syncMapLen(&pg.failCounts); got != 1 {
		t.Fatalf("reclaimed capacity must accept a new failure entry, got %d", got)
	}
}

func TestPeerGateStopIsIdempotent(t *testing.T) {
	pg := newTestPeerGate(t)
	pg.Stop()
	                                                                       
	                                                                          
	pg.Stop()
}

func TestPeerGateExplorerTokenAuthenticatesRemoteServiceWithoutIPTrust(t *testing.T) {
	t.Setenv(config.ExplorerServiceTokenEnv, "remote-explorer-service-secret")
	cfg := config.DefaultPeerGateConfig()
	pg := NewPeerGate(cfg, logs.NewNodeLogger("pg-explorer-token-test", 0))
	t.Cleanup(pg.Stop)

	request := func(token string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/status", nil)
		req.RemoteAddr = "198.51.100.20:443"
		req.Header.Set(config.ExplorerServiceHeader, "explorer")
		if token != "" {
			req.Header.Set(config.ExplorerServiceTokenHeader, token)
		}
		return req
	}
	if !pg.isExplorerService(request("remote-explorer-service-secret")) {
		t.Fatal("valid shared token must authenticate an Explorer from any source IP")
	}
	if pg.isExplorerService(request("wrong")) {
		t.Fatal("wrong token must not authenticate Explorer service")
	}
	if pg.isExplorerService(request("")) {
		t.Fatal("service header alone must not authenticate Explorer service")
	}
}

func TestPeerGateServiceHeaderAloneStaysInUnknownRateTier(t *testing.T) {
	t.Setenv(config.ExplorerServiceTokenEnv, "remote-explorer-service-secret")
	cfg := config.DefaultPeerGateConfig()
	pg := NewPeerGate(cfg, logs.NewNodeLogger("pg-explorer-header-test", 0))
	t.Cleanup(pg.Stop)
	handler := pg.Middleware("/status", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	serve := func(token string) int {
		req := httptest.NewRequest(http.MethodGet, "/status", nil)
		req.RemoteAddr = "203.0.113.10:443"
		req.Header.Set(config.ExplorerServiceHeader, "explorer")
		if token != "" {
			req.Header.Set(config.ExplorerServiceTokenHeader, token)
		}
		rr := httptest.NewRecorder()
		handler(rr, req)
		return rr.Code
	}
	if got := serve(""); got != http.StatusOK {
		t.Fatalf("first unknown-tier request=%d, want %d", got, http.StatusOK)
	}
	if got := serve(""); got != http.StatusTooManyRequests {
		t.Fatalf("second header-only request=%d, want unknown-tier throttling", got)
	}
	if got := serve("remote-explorer-service-secret"); got != http.StatusOK {
		t.Fatalf("authenticated Explorer request=%d, want %d", got, http.StatusOK)
	}
}

func syncMapLen(m *sync.Map) int {
	n := 0
	m.Range(func(_, _ interface{}) bool {
		n++
		return true
	})
	return n
}

                                                          

func TestExtractTxId(t *testing.T) {
	tx := &pb.AnyTx{Content: &pb.AnyTx_CertSignTx{CertSignTx: &pb.CertSignTx{Base: &pb.BaseMessage{TxId: "tx-123"}}}}
	id, err := ExtractTxId(tx)
	if err != nil {
		t.Fatalf("ExtractTxId: %v", err)
	}
	if id != "tx-123" {
		t.Fatalf("id=%q want tx-123", id)
	}

	                      
	empty := &pb.AnyTx{}
	if _, err := ExtractTxId(empty); err == nil {
		t.Fatal("ExtractTxId on empty AnyTx must fail")
	}
}
