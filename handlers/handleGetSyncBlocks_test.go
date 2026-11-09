package handlers

import (
	"bytes"
	"dex/config"
	"dex/db"
	"dex/liveness"
	"dex/logs"
	"dex/pb"
	"dex/sender"
	"dex/servicepeer"
	"dex/stats"
	"dex/types"
	"dex/utils"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
)

func archiveServicePeerTestSigner(t *testing.T, scalar int) *utils.KeyManager {
	t.Helper()
	signer := utils.NewKeyManager()
	if err := signer.InitKey(fmt.Sprintf("%064x", scalar)); err != nil {
		t.Fatalf("InitKey: %v", err)
	}
	return signer
}

func newArchiveServicePeerHandler(t *testing.T) (*HandlerManager, *servicepeer.Store, servicepeer.Record) {
	t.Helper()
	const chainID uint64 = 9911
	localSigner := archiveServicePeerTestSigner(t, 991101)
	sm := &sender.SenderManager{}
	if err := sm.ConfigureServiceIdentity(localSigner, "8.8.4.4:19000", chainID); err != nil {
		t.Fatalf("ConfigureServiceIdentity: %v", err)
	}
	t.Cleanup(sm.Stop)

	remoteSigner := archiveServicePeerTestSigner(t, 991102)
	remote, err := servicepeer.NewSignedRecord(servicepeer.Record{
		ChainID:     chainID,
		Endpoint:    "1.1.1.1:19000",
		ArchiveFrom: 1,
		ArchiveTo:   100,
		Sequence:    1,
	}, remoteSigner)
	if err != nil {
		t.Fatalf("NewSignedRecord: %v", err)
	}
	store := sm.ServicePeerStore()
	if _, err := store.UpsertCandidate(remote); err != nil {
		t.Fatalf("UpsertCandidate: %v", err)
	}
	if err := store.MarkVerified(remote.NodeID, remote.Sequence, 1); err != nil {
		t.Fatalf("MarkVerified: %v", err)
	}
	return &HandlerManager{Stats: stats.NewStats(), senderManager: sm}, store, remote
}

type archiveSlotObservingReader struct {
	manager   *sender.SenderManager
	inner     *bytes.Reader
	checked   bool
	slotsFree bool
}

func (r *archiveSlotObservingReader) Read(dst []byte) (int, error) {
	if !r.checked {
		r.checked = true
		firstRelease, firstOK := r.manager.AcquireServiceSyncServe("1.1.1.1")
		secondRelease, secondOK := r.manager.AcquirePrivilegedArchiveServe("9.9.9.9")
		r.slotsFree = firstOK && secondOK
		if firstOK {
			firstRelease()
		}
		if secondOK {
			secondRelease()
		}
	}
	return r.inner.Read(dst)
}

func TestHandleGetSyncBlocksRejectsMalformedRequest(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Network.AdvertiseHost = "127.0.0.1"
	hm := &HandlerManager{Stats: stats.NewStats(), cfg: cfg}
	req := httptest.NewRequest(http.MethodPost, "/getsyncblocks", bytes.NewBufferString(`{"from_height":1,"to_height":65}`))
	req.RemoteAddr = "127.0.0.1:12345"
	recorder := httptest.NewRecorder()

	hm.HandleGetSyncBlocks(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("malformed sync request status=%d want=%d", recorder.Code, http.StatusBadRequest)
	}
}

func TestHandleGetSyncBlocksRejectsValidOversizedRange(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Network.AdvertiseHost = "127.0.0.1"
	hm := &HandlerManager{Stats: stats.NewStats(), cfg: cfg}
	req := httptest.NewRequest(http.MethodPost, "/getsyncblocks", bytes.NewBufferString("{\"from_height\":1,\"to_height\":65}"))
	req.RemoteAddr = "127.0.0.1:12345"
	recorder := httptest.NewRecorder()

	hm.HandleGetSyncBlocks(recorder, req)
	if recorder.Code != http.StatusBadRequest || !bytes.Contains(recorder.Body.Bytes(), []byte("exceeds accepted-frontier maximum")) {
		t.Fatalf("oversized sync range status=%d body=%q", recorder.Code, recorder.Body.String())
	}
}

func TestHandleGetSyncBlocksRejectsAnonymousRemoteCaller(t *testing.T) {
	hm := &HandlerManager{Stats: stats.NewStats()}
	req := httptest.NewRequest(http.MethodPost, "/getsyncblocks", bytes.NewBufferString(`{"from_height":1,"to_height":1}`))
	req.RemoteAddr = "203.0.113.10:12345"
	recorder := httptest.NewRecorder()

	hm.HandleGetSyncBlocks(recorder, req)
	if recorder.Code != http.StatusForbidden || !bytes.Contains(recorder.Body.Bytes(), []byte("authenticated service sync")) {
		t.Fatalf("anonymous legacy sync status=%d body=%q", recorder.Code, recorder.Body.String())
	}
}

func TestHandleGetSyncBlocksReadsBodyBeforeTakingArchiveSlot(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Network.AdvertiseHost = "127.0.0.1"
	sm := &sender.SenderManager{}
	body := &archiveSlotObservingReader{
		manager: sm,
		inner:   bytes.NewReader([]byte(`{"from_height":1,"to_height":1}`)),
	}
	hm := &HandlerManager{Stats: stats.NewStats(), cfg: cfg, senderManager: sm}
	req := httptest.NewRequest(http.MethodPost, "/getsyncblocks", body)
	req.RemoteAddr = "127.0.0.1:12345"
	recorder := httptest.NewRecorder()

	hm.HandleGetSyncBlocks(recorder, req)
	if !body.checked || !body.slotsFree {
		t.Fatal("block-sync capacity was occupied before the bounded request body was read")
	}
	if recorder.Code != http.StatusServiceUnavailable || bytes.Contains(recorder.Body.Bytes(), []byte("capacity exhausted")) {
		t.Fatalf("unexpected post-read response status=%d body=%q", recorder.Code, recorder.Body.String())
	}
}

func TestArchiveServiceAdmissionAndFailedBuildDoNotConsumeEntitlement(t *testing.T) {
	hm, store, remote := newArchiveServicePeerHandler(t)
	request := httptest.NewRequest(http.MethodPost, "/getsyncblocks", bytes.NewBufferString(`{"from_height":1,"to_height":1}`))
	request.RemoteAddr = "1.1.1.1:23456"

	if !hm.allowArchiveDataRequest(request) {
		t.Fatal("verified service peer was denied read-only block-sync admission")
	}
	peer, _ := store.Get(remote.NodeID)
	if !peer.BootstrapStartedAt.IsZero() {
		t.Fatal("read-only block-sync admission consumed bootstrap entitlement")
	}

	recorder := httptest.NewRecorder()
	hm.HandleGetSyncBlocks(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable block-sync build status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	peer, _ = store.Get(remote.NodeID)
	if !peer.BootstrapStartedAt.IsZero() {
		t.Fatal("failed block-sync response build consumed bootstrap entitlement")
	}

	if !hm.authorizeArchiveDataRequest(request) {
		t.Fatal("real response boundary did not authorize verified service peer")
	}
	peer, _ = store.Get(remote.NodeID)
	if peer.BootstrapStartedAt.IsZero() {
		t.Fatal("explicit response authorization did not start bootstrap entitlement")
	}
}

func TestMissingBlockRoutesDoNotConsumeServiceEntitlement(t *testing.T) {
	serviceHandler, store, remote := newArchiveServicePeerHandler(t)
	logger := logs.NewNodeLogger("archive-missing-block-auth-test", 0)
	dbManager, err := db.NewManager(t.TempDir(), logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(dbManager.Close)
	hm := &HandlerManager{
		dbManager:     dbManager,
		senderManager: serviceHandler.senderManager,
		Stats:         stats.NewStats(),
		Logger:        logger,
	}

	heightRequest, err := proto.Marshal(&pb.GetBlockRequest{Height: 999})
	if err != nil {
		t.Fatal(err)
	}
	idRequest, err := proto.Marshal(&pb.GetBlockByIDRequest{BlockId: "missing-block"})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		path    string
		body    []byte
		handler http.HandlerFunc
	}{
		{name: "height", path: "/getblock", body: heightRequest, handler: hm.HandleGetBlock},
		{name: "block id", path: "/getblockbyid", body: idRequest, handler: hm.HandleGet},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.path, bytes.NewReader(test.body))
			request.RemoteAddr = "1.1.1.1:23456"
			recorder := httptest.NewRecorder()
			test.handler(recorder, request)
			if recorder.Code != http.StatusNotFound {
				t.Fatalf("missing block status=%d body=%q", recorder.Code, recorder.Body.String())
			}
			peer, _ := store.Get(remote.NodeID)
			if !peer.BootstrapStartedAt.IsZero() {
				t.Fatal("missing block response consumed bootstrap entitlement")
			}
		})
	}
}

func TestLegacySyncAllowsOnlyRegisteredMinerSourceIP(t *testing.T) {
	logger := logs.NewNodeLogger("legacy-sync-auth-test", 0)
	dbManager, err := db.NewManager(t.TempDir(), logger)
	if err != nil {
		t.Fatal(err)
	}
	dbManager.InitWriteQueue(32, time.Hour)
	t.Cleanup(func() { dbManager.Close() })
	if err := dbManager.SaveAccount(&pb.Account{
		Address: "miner-address",
		Ip:      "8.8.8.8:6000",
		IsMiner: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := dbManager.ForceFlushHot(); err != nil {
		t.Fatal(err)
	}
	peerGate := NewPeerGate(config.DefaultPeerGateConfig(), logger)
	peerGate.SetDeps(nil, dbManager)
	t.Cleanup(peerGate.Stop)
	hm := &HandlerManager{dbManager: dbManager, peerGate: peerGate}

	request := httptest.NewRequest(http.MethodPost, "/getsyncblocks", nil)
	request.RemoteAddr = "8.8.8.8:23456"
	request.Header.Set("X-Node-Address", "miner-address")
	if !hm.allowLegacyMinerSync(request) {
		t.Fatal("registered miner at its public IP was denied legacy sync")
	}
	if !hm.authorizeArchiveDataRequest(request) {
		t.Fatal("registered miner at its exact public IP did not bypass service reciprocity")
	}
	request.RemoteAddr = "1.1.1.1:23456"
	if hm.allowLegacyMinerSync(request) {
		t.Fatal("miner identity was accepted from a different source IP")
	}

	                                                                      
	                                                                         
	                                                
	if err := dbManager.SaveAccount(&pb.Account{
		Address: "private-miner-address",
		Ip:      "10.0.0.8:6000",
		IsMiner: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := dbManager.ForceFlushHot(); err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Node-Address", "private-miner-address")
	request.RemoteAddr = "10.0.0.8:23456"
	if !hm.allowLegacyMinerSync(request) {
		t.Fatal("registered private-network miner was denied its legacy sync route")
	}
}

func TestLegacySyncAllowsFrozenRegisteredMinerOnlyFromBoundIP(t *testing.T) {
	logger := logs.NewNodeLogger("legacy-sync-frozen-auth-test", 0)
	dbManager, err := db.NewManager(t.TempDir(), logger)
	if err != nil {
		t.Fatal(err)
	}
	dbManager.InitWriteQueue(32, time.Hour)
	t.Cleanup(func() { dbManager.Close() })

	const address = "frozen-miner-address"
	if err := dbManager.SaveAccount(&pb.Account{
		Address: address,
		Ip:      "10.0.0.9:6000",
		IsMiner: false,
	}); err != nil {
		t.Fatal(err)
	}
	if err := dbManager.ForceFlushHot(); err != nil {
		t.Fatal(err)
	}

	tracker := liveness.NewTracker(liveness.DefaultConfig())
	view := &liveness.DirectWriter{}
	if _, err := tracker.RegisterMinerAtHeight(view, address, liveness.DefaultConfig().MinStake, 0); err != nil {
		t.Fatalf("register frozen miner fixture: %v", err)
	}
	if err := tracker.RemoveMiner(view, address, 1); err != nil {
		t.Fatalf("freeze miner fixture: %v", err)
	}
	if got := tracker.GetMinerState(address); got != int(liveness.StateFrozen) {
		t.Fatalf("miner state=%d, want frozen=%d", got, liveness.StateFrozen)
	}

	peerGate := NewPeerGate(config.DefaultPeerGateConfig(), logger)
	peerGate.SetDeps(tracker, dbManager)
	t.Cleanup(peerGate.Stop)
	hm := &HandlerManager{dbManager: dbManager, peerGate: peerGate}

	request := httptest.NewRequest(http.MethodPost, "/getsyncblocks", nil)
	request.Header.Set("X-Node-Address", address)
	request.RemoteAddr = "10.0.0.9:23456"
	if !hm.allowLegacyMinerSync(request) {
		t.Fatal("frozen registered miner at its bound IP was denied archive recovery")
	}
	if !hm.authorizeArchiveDataRequest(request) {
		t.Fatal("frozen registered miner at its bound IP did not bypass service reciprocity")
	}

	request.RemoteAddr = "10.0.0.10:23456"
	if hm.allowLegacyMinerSync(request) {
		t.Fatal("frozen miner identity was accepted from a different source IP")
	}
}

func TestLegacySyncLoopbackBypassRequiresLoopbackOnlyNodeConfig(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/getsyncblocks", nil)
	request.RemoteAddr = "127.0.0.1:23456"

	production := config.DefaultConfig()
	production.Network.AdvertiseHost = "52.128.238.74"
	if (&HandlerManager{cfg: production}).allowLegacyMinerSync(request) {
		t.Fatal("production node accepted anonymous loopback traffic from a possible reverse proxy")
	}

	development := config.DefaultConfig()
	development.Network.AdvertiseHost = "127.0.0.1"
	if !(&HandlerManager{cfg: development}).allowLegacyMinerSync(request) {
		t.Fatal("explicit loopback-only development node rejected local legacy sync")
	}
	if !(&HandlerManager{cfg: development}).authorizeArchiveDataRequest(request) {
		t.Fatal("explicit loopback-only development node did not bypass service reciprocity")
	}
}

func TestLegacySyncPrivateDevelopmentBypassRequiresDebugPrivateNode(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/getsyncblocks", nil)
	request.RemoteAddr = "192.168.1.41:23456"

	privateNode := config.DefaultConfig()
	privateNode.Network.AdvertiseHost = "192.168.1.10"
	if (&HandlerManager{cfg: privateNode}).allowLegacyMinerSync(request) {
		t.Fatal("private node without explicit debug mode accepted anonymous archive sync")
	}

	privateNode.Server.EnableDebugAPI = true
	hm := &HandlerManager{cfg: privateNode}
	if !hm.allowLegacyMinerSync(request) {
		t.Fatal("debug private node rejected private local20 archive sync")
	}
	if !hm.authorizeArchiveDataRequest(request) {
		t.Fatal("debug private node required service reciprocity from local20 peer")
	}
	if hm.isPrivilegedArchiveDataRequest(request) {
		t.Fatal("private development bypass escaped the ordinary bounded sync lane")
	}

	request.RemoteAddr = "8.8.8.8:23456"
	if hm.allowLegacyMinerSync(request) {
		t.Fatal("debug private node accepted an anonymous public source")
	}

	publicNode := config.DefaultConfig()
	publicNode.Server.EnableDebugAPI = true
	publicNode.Network.AdvertiseHost = "8.8.4.4"
	request.RemoteAddr = "192.168.1.41:23456"
	if (&HandlerManager{cfg: publicNode}).allowLegacyMinerSync(request) {
		t.Fatal("debug public node accepted anonymous private traffic")
	}
}

func TestArchiveDataAdmissionRetainsAuthenticatedExplorerAccess(t *testing.T) {
	const token = "remote-explorer-service-secret-1234"
	t.Setenv(config.ExplorerServiceTokenEnv, token)
	logger := logs.NewNodeLogger("archive-explorer-auth-test", 0)
	peerGate := NewPeerGate(config.DefaultPeerGateConfig(), logger)
	t.Cleanup(peerGate.Stop)
	hm := &HandlerManager{peerGate: peerGate}
	request := httptest.NewRequest(http.MethodPost, "/getblock", nil)
	request.RemoteAddr = "203.0.113.20:23456"
	request.Header.Set(config.ExplorerServiceHeader, "explorer")
	request.Header.Set(config.ExplorerServiceTokenHeader, token)

	if !hm.allowArchiveDataRequest(request) {
		t.Fatal("authenticated Explorer lost access to the existing block query route")
	}
	if !hm.authorizeArchiveDataRequest(request) {
		t.Fatal("authenticated Explorer did not bypass service reciprocity")
	}
	release, ok := hm.acquireArchiveDataServe(request)
	if !ok {
		t.Fatal("authenticated Explorer could not acquire block-sync response capacity")
	}
	release()

	request.Header.Set(config.ExplorerServiceTokenHeader, "wrong")
	if hm.allowArchiveDataRequest(request) {
		t.Fatal("invalid Explorer token bypassed block-sync admission")
	}
}

func TestCollectContiguousSyncBlockBundlesStopsAtFirstGap(t *testing.T) {
	var lookedUp []uint64
	bundles := collectContiguousSyncBlockBundles(1, 4, func(height uint64) (string, *pb.Block, bool) {
		lookedUp = append(lookedUp, height)
		if height == 3 {
			return "", nil, false
		}
		return "block", &pb.Block{}, true
	})

	if len(bundles) != 2 || bundles[0].Height != 1 || bundles[1].Height != 2 {
		t.Fatalf("unexpected contiguous bundles: %#v", bundles)
	}
	if want := []uint64{1, 2, 3}; !reflect.DeepEqual(lookedUp, want) {
		t.Fatalf("lookup continued past first gap: got=%v want=%v", lookedUp, want)
	}
}

func TestWriteSyncBlocksResponseRejectsOversizedStoredBlock(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeBoundedSyncBlocksResponse(recorder, types.SyncBlocksResponse{Blocks: []types.SyncBlockBundle{{
		Height:     1,
		BlockID:    "block-1",
		BlockBytes: make([]byte, pb.MaxTotalSize+1),
	}}})
	if recorder.Code != http.StatusInternalServerError || !bytes.Contains(recorder.Body.Bytes(), []byte("exceeds service limit")) {
		t.Fatalf("oversized stored block status=%d body=%q", recorder.Code, recorder.Body.String())
	}
}
