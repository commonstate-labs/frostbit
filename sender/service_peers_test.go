package sender

import (
	"bytes"
	"dex/config"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"dex/servicepeer"
	"dex/utils"
)

func TestServiceSyncClientExtendsControlPlaneTimeoutWithoutMutation(t *testing.T) {
	transport := http.DefaultTransport
	shared := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	syncClient := serviceSyncHTTPClient(shared)
	if syncClient == shared {
		t.Fatal("service sync mutated the shared HTTP client")
	}
	if shared.Timeout != 10*time.Second {
		t.Fatalf("shared client timeout changed to %s", shared.Timeout)
	}
	if syncClient.Timeout != servicePeerSyncRequestTimeout || syncClient.Transport != transport {
		t.Fatalf("service client timeout/transport = %s/%T", syncClient.Timeout, syncClient.Transport)
	}
}

func servicePeerTestSigner(t *testing.T, scalar int) *utils.KeyManager {
	t.Helper()
	signer := utils.NewKeyManager()
	if err := signer.InitKey(fmt.Sprintf("%064x", scalar)); err != nil {
		t.Fatalf("InitKey: %v", err)
	}
	return signer
}

func servicePeerTestRecord(t *testing.T, signer *utils.KeyManager, chainID uint64, endpoint string, sequence uint64) servicepeer.Record {
	t.Helper()
	record, err := servicepeer.NewSignedRecord(servicepeer.Record{
		ChainID:     chainID,
		Endpoint:    endpoint,
		ArchiveFrom: 10,
		ArchiveTo:   20,
		Sequence:    sequence,
	}, signer)
	if err != nil {
		t.Fatalf("NewSignedRecord: %v", err)
	}
	return record
}

func TestConfigureServiceIdentityRequiresPublicEndpoint(t *testing.T) {
	signer := servicePeerTestSigner(t, 101)
	sm := &SenderManager{address: signer.GetAddress()}
	if err := sm.ConfigureServiceIdentity(signer, "127.0.0.1:19000", 77); err == nil {
		t.Fatal("loopback endpoint unexpectedly enabled service role")
	}
	if err := sm.ConfigureServiceIdentity(signer, "8.8.8.8:19000", 77); err != nil {
		t.Fatalf("public endpoint rejected: %v", err)
	}
	if !sm.ServicePeerEnabled() || sm.ServicePeerStore() == nil {
		t.Fatal("service peer runtime was not initialized")
	}
}

func TestLocalServiceRecordSerializesAvailabilityReadAndSigning(t *testing.T) {
	const chainID uint64 = 771
	signer := servicePeerTestSigner(t, 1001)
	sm := &SenderManager{address: signer.GetAddress()}
	if err := sm.ConfigureServiceIdentity(signer, "8.8.8.8:19000", chainID); err != nil {
		t.Fatal(err)
	}

	firstReadStarted := make(chan struct{})
	releaseFirstRead := make(chan struct{})
	secondRefreshStarted := make(chan struct{})
	readCount := 0
	sm.servicePeer.mu.Lock()
	sm.servicePeer.readAvailability = func() serviceAvailabilitySnapshot {
		readCount++
		if readCount == 1 {
			close(firstReadStarted)
			<-releaseFirstRead
			return serviceAvailabilitySnapshot{archiveFrom: 1, archiveTo: 100, checkpointHeight: 80}
		}
		return serviceAvailabilitySnapshot{archiveFrom: 50, archiveTo: 100, checkpointHeight: 90}
	}
	sm.servicePeer.mu.Unlock()

	results := make(chan servicepeer.Record, 2)
	errorsCh := make(chan error, 2)
	go func() {
		record, err := sm.LocalServiceRecord()
		results <- record
		errorsCh <- err
	}()
	<-firstReadStarted
	go func() {
		close(secondRefreshStarted)
		record, err := sm.LocalServiceRecord()
		results <- record
		errorsCh <- err
	}()
	<-secondRefreshStarted
	close(releaseFirstRead)

	first := <-results
	second := <-results
	if err := <-errorsCh; err != nil {
		t.Fatal(err)
	}
	if err := <-errorsCh; err != nil {
		t.Fatal(err)
	}
	if first.Sequence == second.Sequence {
		t.Fatalf("concurrent refreshes reused sequence %d", first.Sequence)
	}
	final, err := sm.cachedLocalServiceRecord()
	if err != nil {
		t.Fatal(err)
	}
	if final.ArchiveFrom != 50 || final.ArchiveTo != 100 || final.CheckpointHeight != 90 {
		t.Fatalf("older availability snapshot overwrote the latest record: %#v", final)
	}
	if final.Sequence != first.Sequence && final.Sequence != second.Sequence {
		t.Fatalf("cached record sequence %d was not returned by either refresh", final.Sequence)
	}
}

func TestVerifyServiceSignedPayloadRequiresVerifiedSourceAndRejectsReplay(t *testing.T) {
	const chainID uint64 = 78
	localSigner := servicePeerTestSigner(t, 102)
	sm := &SenderManager{address: localSigner.GetAddress()}
	if err := sm.ConfigureServiceIdentity(localSigner, "8.8.4.4:19000", chainID); err != nil {
		t.Fatalf("ConfigureServiceIdentity: %v", err)
	}

	remoteSigner := servicePeerTestSigner(t, 103)
	remote := servicePeerTestRecord(t, remoteSigner, chainID, "1.1.1.1:19001", 1)
	if _, err := sm.ServicePeerStore().UpsertCandidate(remote); err != nil {
		t.Fatalf("UpsertCandidate: %v", err)
	}
	payload := []byte("gossip")
	auth, err := servicepeer.NewRequestAuth(remote, ServicePeerGossipPath, payload, remoteSigner)
	if err != nil {
		t.Fatalf("NewRequestAuth: %v", err)
	}
	envelope := servicepeer.SignedPayload{Record: remote, Auth: auth, Payload: payload}
	if _, err := sm.VerifyServiceSignedPayload(envelope, ServicePeerGossipPath, "1.1.1.1"); err == nil {
		t.Fatal("candidate was accepted as a verified service source")
	}
	if err := sm.ServicePeerStore().MarkVerified(remote.NodeID, remote.Sequence, 1); err != nil {
		t.Fatalf("MarkVerified: %v", err)
	}
	if _, err := sm.VerifyServiceSignedPayload(envelope, ServicePeerGossipPath, "8.8.8.8"); err == nil {
		t.Fatal("wrong source IP was accepted")
	}
	if _, err := sm.VerifyServiceSignedPayload(envelope, ServicePeerGossipPath, "1.1.1.1"); err != nil {
		t.Fatalf("verified service request rejected: %v", err)
	}
	if _, err := sm.VerifyServiceSignedPayload(envelope, ServicePeerGossipPath, "1.1.1.1"); err == nil {
		t.Fatal("replayed service request was accepted")
	}
}

func TestVerifiedServiceRequestCarriesSafeAvailabilityUpdate(t *testing.T) {
	const chainID uint64 = 79
	localSigner := servicePeerTestSigner(t, 104)
	sm := &SenderManager{address: localSigner.GetAddress()}
	if err := sm.ConfigureServiceIdentity(localSigner, "8.8.4.4:19000", chainID); err != nil {
		t.Fatal(err)
	}
	remoteSigner := servicePeerTestSigner(t, 105)
	remote := servicePeerTestRecord(t, remoteSigner, chainID, "9.9.9.9:19001", 1)
	if _, err := sm.ServicePeerStore().UpsertCandidate(remote); err != nil {
		t.Fatal(err)
	}
	if err := sm.ServicePeerStore().MarkVerified(remote.NodeID, remote.Sequence, 1); err != nil {
		t.Fatal(err)
	}
	updated, err := servicepeer.NewSignedRecord(servicepeer.Record{
		ChainID:          chainID,
		Endpoint:         remote.Endpoint,
		ArchiveFrom:      10,
		ArchiveTo:        30,
		CheckpointHeight: 20,
		Sequence:         2,
	}, remoteSigner)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("new availability")
	auth, err := servicepeer.NewRequestAuth(updated, ServicePeerGossipPath, payload, remoteSigner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sm.VerifyServiceSignedPayload(servicepeer.SignedPayload{
		Record: updated, Auth: auth, Payload: payload,
	}, ServicePeerGossipPath, "9.9.9.9"); err != nil {
		t.Fatalf("signed same-endpoint availability update rejected: %v", err)
	}
	peer, ok := sm.ServicePeerStore().Get(remote.NodeID)
	if !ok || peer.State != servicepeer.StateVerified || peer.Record.Sequence != 2 || peer.Record.ArchiveTo != 30 {
		t.Fatalf("availability update did not preserve verified peer: %#v", peer)
	}
}

func TestAuthenticatedInvalidPayloadFailuresAccumulate(t *testing.T) {
	const chainID uint64 = 80
	localSigner := servicePeerTestSigner(t, 106)
	sm := &SenderManager{address: localSigner.GetAddress()}
	if err := sm.ConfigureServiceIdentity(localSigner, "8.8.4.4:19000", chainID); err != nil {
		t.Fatal(err)
	}
	remoteSigner := servicePeerTestSigner(t, 107)
	remote := servicePeerTestRecord(t, remoteSigner, chainID, "1.0.0.1:19001", 1)
	if _, err := sm.ServicePeerStore().UpsertCandidate(remote); err != nil {
		t.Fatal(err)
	}
	if err := sm.ServicePeerStore().MarkVerified(remote.NodeID, remote.Sequence, 1); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		payload := []byte(fmt.Sprintf("invalid-block-%d", i))
		auth, err := servicepeer.NewRequestAuth(remote, ServicePeerGossipPath, payload, remoteSigner)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sm.VerifyServiceSignedPayload(servicepeer.SignedPayload{
			Record: remote, Auth: auth, Payload: payload,
		}, ServicePeerGossipPath, "1.0.0.1"); err != nil {
			t.Fatalf("authenticated request %d rejected before payload validation: %v", i, err)
		}
		sm.RecordServicePeerFailure(remote.NodeID)
	}

	peer, ok := sm.ServicePeerStore().Get(remote.NodeID)
	if !ok || peer.State != servicepeer.StateDegraded || peer.ConsecutiveFailures != 3 {
		t.Fatalf("invalid payload failures were reset during authentication: %#v", peer)
	}
}

func TestServiceDiscoveryTargetsExcludeUnverifiedCandidates(t *testing.T) {
	const chainID uint64 = 81
	localSigner := servicePeerTestSigner(t, 108)
	cfg := config.DefaultConfig()
	cfg.Discovery.BootstrapPeers = []string{"9.9.9.9:19009"}
	sm := &SenderManager{address: localSigner.GetAddress(), cfg: cfg}
	if err := sm.ConfigureServiceIdentity(localSigner, "8.8.4.4:19000", chainID); err != nil {
		t.Fatal(err)
	}

	candidateSigner := servicePeerTestSigner(t, 109)
	candidate := servicePeerTestRecord(t, candidateSigner, chainID, "1.1.1.1:19001", 1)
	verifiedSigner := servicePeerTestSigner(t, 110)
	verified := servicePeerTestRecord(t, verifiedSigner, chainID, "1.0.0.1:19002", 1)
	for _, record := range []servicepeer.Record{candidate, verified} {
		if _, err := sm.ServicePeerStore().UpsertCandidate(record); err != nil {
			t.Fatal(err)
		}
	}
	if err := sm.ServicePeerStore().MarkVerified(verified.NodeID, verified.Sequence, 1); err != nil {
		t.Fatal(err)
	}

	targets := sm.servicePeerDiscoveryTargets()
	got := make(map[string]bool, len(targets))
	for _, endpoint := range targets {
		got[endpoint] = true
	}
	if got[candidate.Endpoint] {
		t.Fatalf("unverified candidate %s became an exchange target", candidate.Endpoint)
	}
	if !got[verified.Endpoint] || !got["9.9.9.9:19009"] {
		t.Fatalf("verified/bootstrap targets missing: %v", targets)
	}
}

func TestProbeCapacityIsSplitAndOutboundSingleflightUsesPublicIP(t *testing.T) {
	const chainID uint64 = 82
	localSigner := servicePeerTestSigner(t, 111)
	sm := &SenderManager{address: localSigner.GetAddress()}
	if err := sm.ConfigureServiceIdentity(localSigner, "8.8.4.4:19000", chainID); err != nil {
		t.Fatal(err)
	}

	first := servicePeerTestRecord(t, servicePeerTestSigner(t, 112), chainID, "1.1.1.1:19001", 1)
	sameIP := servicePeerTestRecord(t, servicePeerTestSigner(t, 113), chainID, "1.1.1.1:19002", 1)
	key, ok := sm.acquireOutboundServiceProbe(first)
	if !ok {
		t.Fatal("first outbound probe was not admitted")
	}
	if _, ok := sm.acquireOutboundServiceProbe(sameIP); ok {
		t.Fatal("rotating NodeID on one public IP bypassed outbound singleflight")
	}
	sm.releaseOutboundServiceProbe(key)

	for i := 0; i < servicePeerProbeConcurrency; i++ {
		source := fmt.Sprintf("198.51.100.%d", i+1)
		if !sm.acquireInboundServiceProbe(source) {
			t.Fatalf("inbound slot %d was not admitted", i)
		}
		defer sm.releaseInboundServiceProbe(source)
	}
	if sm.acquireInboundServiceProbe("198.51.100.1") {
		t.Fatal("same inbound source occupied more than one callback slot")
	}
	key, ok = sm.acquireOutboundServiceProbe(first)
	if !ok {
		t.Fatal("inbound callback saturation blocked the independent outbound lane")
	}
	sm.releaseOutboundServiceProbe(key)

	candidateKeys := make([]string, 0, servicePeerProbeConcurrency)
	for i, endpoint := range []string{"1.0.0.1:19100", "8.8.8.8:19101", "9.9.9.9:19102", "208.67.222.222:19103"} {
		record := servicePeerTestRecord(t, servicePeerTestSigner(t, 120+i), chainID, endpoint, 1)
		key, admitted := sm.acquireOutboundServiceProbe(record)
		if !admitted {
			t.Fatalf("candidate outbound slot %d was not admitted", i)
		}
		candidateKeys = append(candidateKeys, key)
	}
	defer func() {
		for _, candidateKey := range candidateKeys {
			sm.releaseOutboundServiceProbe(candidateKey)
		}
	}()

	verified := servicePeerTestRecord(t, servicePeerTestSigner(t, 130), chainID, "4.2.2.2:19104", 1)
	if _, err := sm.ServicePeerStore().UpsertCandidate(verified); err != nil {
		t.Fatal(err)
	}
	if err := sm.ServicePeerStore().MarkVerified(verified.NodeID, verified.Sequence, 1); err != nil {
		t.Fatal(err)
	}
	verifiedKey, admitted := sm.acquireOutboundServiceProbe(verified)
	if !admitted {
		t.Fatal("candidate saturation blocked the lane reserved for verified re-probes")
	}
	sm.releaseOutboundServiceProbe(verifiedKey)
}

func TestServiceCallbackRequiresSingleActiveProbeAndExpiredPeerCannotRequestBytes(t *testing.T) {
	const chainID uint64 = 83
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	localSigner := servicePeerTestSigner(t, 135)
	sm := &SenderManager{address: localSigner.GetAddress()}
	if err := sm.ConfigureServiceIdentity(localSigner, "8.8.4.4:19000", chainID); err != nil {
		t.Fatal(err)
	}
	controlledStore, err := servicepeer.NewStore(chainID, localSigner.GetAddress(), servicepeer.Config{
		CandidateCapacity:   8,
		CandidatePerIP:      2,
		VerifiedCapacity:    4,
		FailureThreshold:    3,
		BootstrapGrace:      time.Hour,
		BootstrapIPCapacity: 8,
		Random:              bytes.NewReader(bytes.Repeat([]byte{0x51}, 4096)),
		Now:                 func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	sm.servicePeer.store = controlledStore

	current, err := sm.LocalServiceRecord()
	if err != nil {
		t.Fatal(err)
	}
	localWithArchive, err := servicepeer.NewSignedRecord(servicepeer.Record{
		ChainID:     chainID,
		Endpoint:    current.Endpoint,
		ArchiveFrom: 10,
		ArchiveTo:   20,
		Sequence:    current.Sequence + 1,
	}, localSigner)
	if err != nil {
		t.Fatal(err)
	}
	sm.servicePeer.mu.Lock()
	sm.servicePeer.record = localWithArchive
	sm.servicePeer.mu.Unlock()

	remoteSigner := servicePeerTestSigner(t, 136)
	remote, err := servicepeer.NewSignedRecord(servicepeer.Record{
		ChainID:  chainID,
		Endpoint: "1.1.1.1:19001",
		Sequence: 1,
	}, remoteSigner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controlledStore.UpsertCandidate(remote); err != nil {
		t.Fatal(err)
	}
	if err := controlledStore.MarkVerified(remote.NodeID, remote.Sequence, 1); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Hour)

	key, ok := sm.acquireOutboundServiceProbe(remote)
	if !ok || !sm.bindOutboundServiceProbeChallenge(remote) {
		t.Fatal("failed to create active connectivity probe binding")
	}
	nonce, _ := servicepeer.NewNonce()
	malicious, err := servicepeer.NewSignedChallenge(remoteSigner, chainID, localWithArchive, nonce, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sm.AnswerServiceCallback(servicepeer.ProbeRequest{Caller: remote, Challenge: malicious}, "1.1.1.1"); err == nil {
		t.Fatal("peer without a stored-block range turned connectivity callback into block download")
	}

	                                                                            
	                                                        
	sm.releaseOutboundServiceProbe(key)
	now = now.Add(-2 * time.Hour)
	key, ok = sm.acquireOutboundServiceProbe(remote)
	if !ok || !sm.bindOutboundServiceProbeChallenge(remote) {
		t.Fatal("failed to recreate active callback binding")
	}
	defer sm.releaseOutboundServiceProbe(key)
	nonce, _ = servicepeer.NewNonce()
	connectivity, err := servicepeer.NewSignedChallenge(remoteSigner, chainID, localWithArchive, nonce, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sm.AnswerServiceCallback(servicepeer.ProbeRequest{Caller: remote, Challenge: connectivity}, "1.1.1.1"); err != nil {
		t.Fatalf("matching active callback rejected: %v", err)
	}
	nonce, _ = servicepeer.NewNonce()
	second, _ := servicepeer.NewSignedChallenge(remoteSigner, chainID, localWithArchive, nonce, 0)
	if _, err := sm.AnswerServiceCallback(servicepeer.ProbeRequest{Caller: remote, Challenge: second}, "1.1.1.1"); err == nil {
		t.Fatal("active callback binding was consumed more than once")
	}
}

func TestSuccessfulCallbackSurvivesOuterContributionRefusal(t *testing.T) {
	const chainID uint64 = 84
	localSigner := servicePeerTestSigner(t, 140)
	sm := &SenderManager{address: localSigner.GetAddress()}
	if err := sm.ConfigureServiceIdentity(localSigner, "8.8.4.4:19000", chainID); err != nil {
		t.Fatal(err)
	}
	remoteSigner := servicePeerTestSigner(t, 141)
	remote := servicePeerTestRecord(t, remoteSigner, chainID, "1.1.1.1:19001", 1)
	if _, err := sm.ServicePeerStore().UpsertCandidate(remote); err != nil {
		t.Fatal(err)
	}
	key, ok := sm.acquireOutboundServiceProbe(remote)
	if !ok || !sm.bindOutboundServiceProbeChallenge(remote) {
		t.Fatal("failed to create active outbound probe")
	}
	defer sm.releaseOutboundServiceProbe(key)

	local, err := sm.LocalServiceRecord()
	if err != nil {
		t.Fatal(err)
	}
	nonce, err := servicepeer.NewNonce()
	if err != nil {
		t.Fatal(err)
	}
	callbackChallenge, err := servicepeer.NewSignedChallenge(remoteSigner, chainID, local, nonce, servicepeer.ConnectivityProbeHeight)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sm.AnswerServiceCallback(servicepeer.ProbeRequest{Caller: remote, Challenge: callbackChallenge}, "1.1.1.1"); err != nil {
		t.Fatalf("valid callback rejected: %v", err)
	}
	peer, ok := sm.ServicePeerStore().Get(remote.NodeID)
	if !ok || peer.State != servicepeer.StateVerified {
		t.Fatalf("successful callback did not promote remote peer: %#v", peer)
	}

	postErr := &serviceHTTPStatusError{StatusCode: http.StatusPreconditionRequired, Body: ErrServiceContributionRequired.Error()}
	if err := sm.recordServiceProbePostFailure(remote, 10, postErr); !errors.Is(err, ErrServiceContributionRequired) {
		t.Fatalf("outer contribution refusal classification=%v", err)
	}
	peer, _ = sm.ServicePeerStore().Get(remote.NodeID)
	if peer.State != servicepeer.StateVerified || peer.FailedChecks != 0 || peer.FailedDataResponses != 0 {
		t.Fatalf("legitimate reciprocity refusal damaged peer health: %#v", peer)
	}

	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable, http.StatusConflict} {
		postErr = &serviceHTTPStatusError{StatusCode: status, Body: "temporary block proof refusal"}
		if err := sm.recordServiceProbePostFailure(remote, 10, postErr); err == nil {
			t.Fatalf("temporary status %d unexpectedly reported success", status)
		}
		peer, _ = sm.ServicePeerStore().Get(remote.NodeID)
		if peer.State != servicepeer.StateVerified || peer.FailedChecks != 0 || peer.FailedDataResponses != 0 {
			t.Fatalf("temporary status %d damaged peer health: %#v", status, peer)
		}
	}
}

func TestTemporaryServiceEndpointStatusesDoNotCountAsConnectivityFailures(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		if !isTemporaryServiceEndpointError(&serviceHTTPStatusError{StatusCode: status}) {
			t.Fatalf("temporary status %d was classified as a peer failure", status)
		}
	}
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusConflict} {
		if isTemporaryServiceEndpointError(&serviceHTTPStatusError{StatusCode: status}) {
			t.Fatalf("non-temporary status %d bypassed peer failure accounting", status)
		}
	}
}

func TestArchiveServeCapacityIsGlobalAndPerSource(t *testing.T) {
	                                                                          
	                                                      
	sm := &SenderManager{}
	firstRelease, ok := sm.AcquireServiceSyncServe("1.1.1.1")
	if !ok {
		t.Fatal("first block-sync response was not admitted")
	}
	defer firstRelease()
	if _, ok := sm.AcquireServiceSyncServe("1.1.1.1"); ok {
		t.Fatal("one source occupied multiple block-sync response slots")
	}
	if _, ok := sm.AcquireServiceSyncServe("9.9.9.9"); ok {
		t.Fatal("ordinary service traffic occupied the reserved block-sync slot")
	}
	secondRelease, ok := sm.AcquirePrivilegedArchiveServe("9.9.9.9")
	if !ok {
		t.Fatal("privileged block-sync traffic could not use its reserved capacity")
	}
	if _, ok := sm.AcquirePrivilegedArchiveServe("4.2.2.2"); ok {
		t.Fatal("global block-sync response concurrency limit was bypassed")
	}
	secondRelease()
	replacementRelease, ok := sm.AcquirePrivilegedArchiveServe("4.2.2.2")
	if !ok {
		t.Fatal("released global block-sync slot was not reusable")
	}
	replacementRelease()

	proofRelease, ok := sm.AcquireServiceProofServe("4.2.2.2")
	if !ok {
		t.Fatal("large block-sync saturation blocked the isolated proof lane")
	}
	if _, ok := sm.AcquireServiceProofServe("208.67.222.222"); ok {
		t.Fatal("proof response concurrency limit was bypassed")
	}
	proofRelease()
}

func TestStopCancelsServiceDiscoveryContext(t *testing.T) {
	localSigner := servicePeerTestSigner(t, 141)
	sm := &SenderManager{}
	if err := sm.ConfigureServiceIdentity(localSigner, "8.8.4.4:19000", 84); err != nil {
		t.Fatal(err)
	}
	ctx := sm.servicePeer.stopCtx
	sm.Stop()
	select {
	case <-ctx.Done():
	default:
		t.Fatal("Stop did not cancel in-flight service discovery work")
	}
}
