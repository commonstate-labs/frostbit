package handlers

import (
	"dex/db"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"dex/stats"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
)

func newFrostQueryTestHandler(t *testing.T) (*HandlerManager, *db.Manager) {
	t.Helper()
	dbMgr, err := db.NewManager(t.TempDir(), logs.NewNodeLogger("test", 0))
	if err != nil {
		t.Fatalf("new db manager: %v", err)
	}
	dbMgr.InitWriteQueue(2048, time.Hour)
	return &HandlerManager{dbManager: dbMgr, Stats: stats.NewStats()}, dbMgr
}

func enqueueProto(t *testing.T, dbMgr *db.Manager, key string, message proto.Message) {
	t.Helper()
	data, err := proto.Marshal(message)
	if err != nil {
		t.Fatalf("marshal %s: %v", key, err)
	}
	dbMgr.EnqueueSet(key, string(data))
}

func decodeChallengeList(t *testing.T, rec *httptest.ResponseRecorder) *pb.ChallengeRecordList {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response pb.ChallengeRecordList
	if err := proto.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode challenge list: %v", err)
	}
	return &response
}

func TestHandleWitnessChallengesIsBoundedAndCursorPaged(t *testing.T) {
	hm, dbMgr := newFrostQueryTestHandler(t)
	defer dbMgr.Close()
	for i := 0; i < 105; i++ {
		id := fmt.Sprintf("c%03d", i)
		enqueueProto(t, dbMgr, keys.KeyChallengeRecord(id), &pb.ChallengeRecord{
			ChallengeId: id, RequestId: "r-" + id, Status: pb.ChallengeStatus_CHALLENGE_ACTIVE,
		})
	}
	if err := dbMgr.ForceFlush(); err != nil {
		t.Fatalf("force flush: %v", err)
	}

	firstRec := httptest.NewRecorder()
	hm.HandleWitnessChallenges(firstRec, httptest.NewRequest(http.MethodGet, "/witness/challenges", nil))
	first := decodeChallengeList(t, firstRec)
	if len(first.Records) != 100 || first.Records[0].ChallengeId != "c000" || first.Records[99].ChallengeId != "c099" {
		t.Fatalf("first page size/order=%d %q..%q", len(first.Records), first.Records[0].ChallengeId, first.Records[len(first.Records)-1].ChallengeId)
	}

	nextRec := httptest.NewRecorder()
	hm.HandleWitnessChallenges(nextRec, httptest.NewRequest(http.MethodGet, "/witness/challenges?after=c099&limit=10", nil))
	next := decodeChallengeList(t, nextRec)
	if len(next.Records) != 5 || next.Records[0].ChallengeId != "c100" || next.Records[4].ChallengeId != "c104" {
		t.Fatalf("next page=%v", next.Records)
	}

	batchRec := httptest.NewRecorder()
	hm.HandleWitnessChallenges(batchRec, httptest.NewRequest(http.MethodGet, "/witness/challenges?challenge_ids=c104,c002,c104", nil))
	batch := decodeChallengeList(t, batchRec)
	if len(batch.Records) != 2 || batch.Records[0].ChallengeId != "c104" || batch.Records[1].ChallengeId != "c002" {
		t.Fatalf("batch=%v", batch.Records)
	}
}

func TestHandleWitnessRequestsUsesBoundedRecentIndexes(t *testing.T) {
	hm, dbMgr := newFrostQueryTestHandler(t)
	defer dbMgr.Close()
	for i := 0; i < 105; i++ {
		requestID := fmt.Sprintf("request-%03d", i)
		req := &pb.RechargeRequest{
			RequestId: requestID, NativeChain: "BTC", VaultId: 1,
			Status: pb.RechargeRequestStatus_RECHARGE_VOTING, CreateHeight: uint64(i + 1),
		}
		canonicalKey := keys.KeyRechargeRequest(requestID)
		enqueueProto(t, dbMgr, canonicalKey, req)
		for _, indexKey := range []string{
			keys.KeyRechargeQueryRecentGlobal(false, req.CreateHeight, requestID),
			keys.KeyRechargeQueryRecentChain(req.NativeChain, false, req.CreateHeight, requestID),
			keys.KeyRechargeQueryRecentVault(req.VaultId, false, req.CreateHeight, requestID),
			keys.KeyRechargeQueryRecentChainVault(req.NativeChain, req.VaultId, false, req.CreateHeight, requestID),
		} {
			dbMgr.EnqueueSet(indexKey, canonicalKey)
		}
	}
	if err := dbMgr.ForceFlush(); err != nil {
		t.Fatalf("force flush: %v", err)
	}

	pageRec := httptest.NewRecorder()
	hm.HandleWitnessRequests(pageRec, httptest.NewRequest(http.MethodGet, "/witness/requests", nil))
	if pageRec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", pageRec.Code, pageRec.Body.String())
	}
	var page pb.RechargeRequestList
	if err := proto.Unmarshal(pageRec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode page: %v", err)
	}
	if len(page.Requests) != 100 {
		t.Fatalf("page size=%d", len(page.Requests))
	}
	if page.Requests[0].CreateHeight != 105 || page.Requests[99].CreateHeight != 6 {
		t.Fatalf("page order first=%d last=%d", page.Requests[0].CreateHeight, page.Requests[99].CreateHeight)
	}

	filteredRec := httptest.NewRecorder()
	hm.HandleWitnessRequests(filteredRec, httptest.NewRequest(http.MethodGet, "/witness/requests?chain=btc&vault_id=1&offset=1&limit=2", nil))
	var filtered pb.RechargeRequestList
	if err := proto.Unmarshal(filteredRec.Body.Bytes(), &filtered); err != nil {
		t.Fatalf("decode filtered page: %v", err)
	}
	if len(filtered.Requests) != 2 || filtered.Requests[0].CreateHeight != 104 || filtered.Requests[1].CreateHeight != 103 {
		t.Fatalf("filtered page=%v", filtered.Requests)
	}
}

func TestHandleWitnessMyRequestsUsesArbitratorIndexAndLimit(t *testing.T) {
	hm, dbMgr := newFrostQueryTestHandler(t)
	defer dbMgr.Close()
	for i := 0; i < 3; i++ {
		challengeID := fmt.Sprintf("challenge-%d", i)
		requestID := fmt.Sprintf("request-%d", i)
		enqueueProto(t, dbMgr, keys.KeyChallengeRecord(challengeID), &pb.ChallengeRecord{
			ChallengeId: challengeID, RequestId: requestID, Status: pb.ChallengeStatus_CHALLENGE_ACTIVE,
			Arbitrators: []string{"arb"},
		})
		enqueueProto(t, dbMgr, keys.KeyRechargeRequest(requestID), &pb.RechargeRequest{RequestId: requestID, ChallengeId: challengeID})
		dbMgr.EnqueueSet(keys.KeyArbitratorChallenge("arb", challengeID), challengeID)
	}
	if err := dbMgr.ForceFlush(); err != nil {
		t.Fatalf("force flush: %v", err)
	}

	rec := httptest.NewRecorder()
	hm.HandleWitnessMyRequests(rec, httptest.NewRequest(http.MethodGet, "/witness/my_requests?witness=arb&limit=2", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response pb.RechargeRequestList
	if err := proto.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Requests) != 2 {
		t.Fatalf("requests=%v", response.Requests)
	}
}

func TestHandleWitnessMyRequestsHistoryIncludesShelvedAssignment(t *testing.T) {
	hm, dbMgr := newFrostQueryTestHandler(t)
	defer dbMgr.Close()
	const witnessAddr = "witness-history"
	const requestID = "request-shelved"
	enqueueProto(t, dbMgr, keys.KeyRechargeRequest(requestID), &pb.RechargeRequest{
		RequestId:         requestID,
		Status:            pb.RechargeRequestStatus_RECHARGE_SHELVED,
		SelectedWitnesses: []string{witnessAddr},
		DeadlineHeight:    100,
		ShelveHeight:      100,
	})
	dbMgr.EnqueueSet(keys.KeyWitnessAssigned(witnessAddr, requestID), requestID)
	if err := dbMgr.ForceFlush(); err != nil {
		t.Fatalf("force flush: %v", err)
	}

	activeRec := httptest.NewRecorder()
	hm.HandleWitnessMyRequests(activeRec, httptest.NewRequest(http.MethodGet, "/witness/my_requests?witness="+witnessAddr, nil))
	var active pb.RechargeRequestList
	if err := proto.Unmarshal(activeRec.Body.Bytes(), &active); err != nil {
		t.Fatalf("decode active response: %v", err)
	}
	if len(active.Requests) != 0 {
		t.Fatalf("default automation query exposed shelved task: %v", active.Requests)
	}

	historyRec := httptest.NewRecorder()
	hm.HandleWitnessMyRequests(historyRec, httptest.NewRequest(http.MethodGet, "/witness/my_requests?witness="+witnessAddr+"&include_history=1", nil))
	var history pb.RechargeRequestList
	if err := proto.Unmarshal(historyRec.Body.Bytes(), &history); err != nil {
		t.Fatalf("decode history response: %v", err)
	}
	if len(history.Requests) != 1 || history.Requests[0].RequestId != requestID || history.Requests[0].Status != pb.RechargeRequestStatus_RECHARGE_SHELVED {
		t.Fatalf("history response=%v", history.Requests)
	}
}

func TestHandleWitnessListIsBoundedAndCursorPaged(t *testing.T) {
	hm, dbMgr := newFrostQueryTestHandler(t)
	defer dbMgr.Close()
	for i := 0; i < 505; i++ {
		address := fmt.Sprintf("w%03d", i)
		enqueueProto(t, dbMgr, keys.KeyWitnessInfo(address), &pb.WitnessInfo{Address: address})
	}
	if err := dbMgr.ForceFlush(); err != nil {
		t.Fatalf("force flush: %v", err)
	}

	firstRec := httptest.NewRecorder()
	hm.HandleWitnessList(firstRec, httptest.NewRequest(http.MethodGet, "/witness/list?limit=999", nil))
	if firstRec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", firstRec.Code, firstRec.Body.String())
	}
	var first pb.WitnessInfoList
	if err := proto.Unmarshal(firstRec.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode first page: %v", err)
	}
	if len(first.Witnesses) != 500 || first.Witnesses[0].Address != "w000" || first.Witnesses[499].Address != "w499" {
		t.Fatalf("first page size/order=%d %q..%q", len(first.Witnesses), first.Witnesses[0].Address, first.Witnesses[len(first.Witnesses)-1].Address)
	}

	nextRec := httptest.NewRecorder()
	hm.HandleWitnessList(nextRec, httptest.NewRequest(http.MethodGet, "/witness/list?after=w499", nil))
	var next pb.WitnessInfoList
	if err := proto.Unmarshal(nextRec.Body.Bytes(), &next); err != nil {
		t.Fatalf("decode next page: %v", err)
	}
	if len(next.Witnesses) != 5 || next.Witnesses[0].Address != "w500" || next.Witnesses[4].Address != "w504" {
		t.Fatalf("next page=%v", next.Witnesses)
	}
}

func TestHandleFrostDkgListIsBounded(t *testing.T) {
	hm, dbMgr := newFrostQueryTestHandler(t)
	defer dbMgr.Close()
	for i := 1; i <= 105; i++ {
		epochID := uint64(i)
		enqueueProto(t, dbMgr, keys.KeyFrostVaultTransition("BTC", 1, epochID), &pb.VaultTransitionState{
			Chain: "BTC", VaultId: 1, EpochId: epochID, DkgStatus: "COMMITTING",
		})
	}
	if err := dbMgr.ForceFlush(); err != nil {
		t.Fatalf("force flush: %v", err)
	}

	rec := httptest.NewRecorder()
	hm.HandleFrostDkgList(rec, httptest.NewRequest(http.MethodGet, "/frost/dkg/list", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response pb.VaultTransitionStateList
	if err := proto.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.States) != 100 || response.States[0].EpochId != 1 || response.States[99].EpochId != 100 {
		t.Fatalf("states size/order=%d first=%d last=%d", len(response.States), response.States[0].EpochId, response.States[len(response.States)-1].EpochId)
	}
}

func TestHandleFrostDkgProgressReportsMissingParticipantsWithoutSecrets(t *testing.T) {
	hm, dbMgr := newFrostQueryTestHandler(t)
	defer dbMgr.Close()
	transition := &pb.VaultTransitionState{
		Chain: "btc", VaultId: 1, EpochId: 9, DkgSessionId: "btc_1_9_2",
		DkgStatus: "SHARING", ValidationStatus: "SIGNING",
		NewCommitteeMembers: []string{"alice", "bob", "carol"},
	}
	enqueueProto(t, dbMgr, keys.KeyFrostVaultTransition("btc", 1, 9), transition)
	for _, member := range []string{"alice", "bob"} {
		enqueueProto(t, dbMgr, keys.KeyFrostVaultDkgCommit("btc", 1, 9, transition.DkgSessionId, member), &pb.FrostVaultDkgCommitment{MinerAddress: member})
	}
	for _, receiver := range []string{"bob", "carol"} {
		enqueueProto(t, dbMgr, keys.KeyFrostVaultDkgShare("btc", 1, 9, transition.DkgSessionId, "alice", receiver), &pb.FrostVaultDkgShare{DealerId: "alice", ReceiverId: receiver})
	}
	dbMgr.EnqueueSet(keys.KeyFrostVaultDkgValidationAck("btc", 1, 9, transition.DkgSessionId, "alice"), "ack")
	enqueueProto(t, dbMgr, keys.KeyFrostVaultDkgComplaint("btc", 1, 9, transition.DkgSessionId, "alice", "bob"), &pb.FrostVaultDkgComplaint{Status: "RESOLVED"})
	enqueueProto(t, dbMgr, keys.KeyFrostVaultDkgComplaint("btc", 1, 9, transition.DkgSessionId, "bob", "carol"), &pb.FrostVaultDkgComplaint{Status: "PENDING"})
	if err := dbMgr.ForceFlush(); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	path := "/frost/dkg/progress?chain=btc&vault_id=1&epoch_id=9&session_id=btc_1_9_2"
	hm.HandleFrostDkgProgress(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response frostDkgProgressResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.TranscriptAvailable || response.CommitCount != 2 || response.CommitExpected != 3 {
		t.Fatalf("commit progress=%+v", response)
	}
	if response.ShareCount != 2 || response.ShareExpected != 6 || response.ValidationAckCount != 1 || response.ValidationAckExpected != 3 {
		t.Fatalf("share/ack progress=%+v", response)
	}
	if response.ComplaintCount != 2 || response.PendingComplaints != 1 || response.ResolvedComplaints != 1 {
		t.Fatalf("complaint progress=%+v", response)
	}
	if len(response.Participants) != 3 || !response.Participants[0].Committed || response.Participants[0].SharesSubmitted != 2 || !response.Participants[0].ValidationAck {
		t.Fatalf("participants=%+v", response.Participants)
	}
	if strings.Contains(rec.Body.String(), "ciphertext") || strings.Contains(rec.Body.String(), "commitment_points") {
		t.Fatalf("progress response leaked transcript material: %s", rec.Body.String())
	}
}

func TestHandleFrostDkgCommitmentsRequiresSessionAndIsBounded(t *testing.T) {
	hm, dbMgr := newFrostQueryTestHandler(t)
	defer dbMgr.Close()
	transition := &pb.VaultTransitionState{Chain: "btc", VaultId: 1, EpochId: 9, DkgSessionId: "session-9"}
	enqueueProto(t, dbMgr, keys.KeyFrostVaultTransition("btc", 1, 9), transition)
	for i := 0; i < 105; i++ {
		participant := fmt.Sprintf("miner-%03d", i)
		enqueueProto(t, dbMgr, keys.KeyFrostVaultDkgCommit("btc", 1, 9, transition.DkgSessionId, participant), &pb.FrostVaultDkgCommitment{
			Chain: "btc", VaultId: 1, EpochId: 9, MinerAddress: participant,
		})
	}
	if err := dbMgr.ForceFlush(); err != nil {
		t.Fatalf("force flush: %v", err)
	}

	rec := httptest.NewRecorder()
	hm.HandleGetVaultDkgCommitments(rec, httptest.NewRequest(http.MethodGet, "/frost/vault/dkg_commitments?chain=btc&vault_id=1&epoch_id=9", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response pb.GetVaultDkgCommitmentsResponse
	if err := proto.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode commitments: %v", err)
	}
	if len(response.Commitments) != defaultFrostDkgCommitLimit {
		t.Fatalf("commitments=%d, want %d", len(response.Commitments), defaultFrostDkgCommitLimit)
	}
}

func TestFrostWithdrawListsUseNewestFirstSlimIndexes(t *testing.T) {
	hm, dbMgr := newFrostQueryTestHandler(t)
	defer dbMgr.Close()
	for i, height := range []uint64{10, 20} {
		id := fmt.Sprintf("withdraw-%d", i)
		state := &pb.FrostWithdrawState{
			WithdrawId: id, Chain: "btc", Asset: "BTC", VaultId: 7,
			RequestHeight: height, Seq: uint64(i + 1), Status: "REQUESTED",
		}
		enqueueProto(t, dbMgr, keys.KeyFrostWithdraw(id), state)
		dbMgr.EnqueueSet(keys.KeyFrostWithdrawQueryGlobal(height, state.Seq, id), id)
		dbMgr.EnqueueSet(keys.KeyFrostWithdrawQueryChain("btc", height, state.Seq, id), id)
		dbMgr.EnqueueSet(keys.KeyFrostWithdrawQueryChainVault("btc", 7, height, state.Seq, id), id)
	}
	if err := dbMgr.ForceFlush(); err != nil {
		t.Fatalf("force flush: %v", err)
	}

	rec := httptest.NewRecorder()
	hm.HandleFrostWithdrawList(rec, httptest.NewRequest(http.MethodGet, "/frost/withdraw/list?chain=btc&vault_id=7&limit=1&order=desc", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var page pb.FrostWithdrawStateList
	if err := proto.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode withdraw page: %v", err)
	}
	if len(page.States) != 1 || page.States[0].WithdrawId != "withdraw-1" {
		t.Fatalf("newest page=%v", page.States)
	}

	listRec := httptest.NewRecorder()
	hm.HandleListWithdraws(listRec, httptest.NewRequest(http.MethodGet, "/frost/withdraws?chain=btc&limit=2", nil))
	var list pb.ListWithdrawsResponse
	if err := proto.Unmarshal(listRec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode withdraw list: %v", err)
	}
	if len(list.Withdraws) != 2 || list.Withdraws[0].LotId != "withdraw-1" || list.Withdraws[1].LotId != "withdraw-0" {
		t.Fatalf("withdraw list=%v", list.Withdraws)
	}
}

func enqueueFrostWithdrawMonitorQueue(t *testing.T, dbMgr *db.Manager, chain, asset string, heightBase uint64) {
	t.Helper()
	const completedCount = 2
	const activeCount = 2

	dbMgr.EnqueueSet(keys.KeyFrostWithdrawFIFOHead(chain, asset), fmt.Sprint(completedCount+1))
	dbMgr.EnqueueSet(keys.KeyFrostWithdrawFIFOSeq(chain, asset), fmt.Sprint(completedCount+activeCount))

	for i := 1; i <= completedCount; i++ {
		id := fmt.Sprintf("%s-%s-signed-%d", chain, asset, i)
		status := "SIGNED"
		if i == completedCount {
			status = "CANCELLED"
		}
		state := &pb.FrostWithdrawState{
			WithdrawId:    id,
			Chain:         chain,
			Asset:         asset,
			Status:        status,
			Seq:           uint64(i),
			RequestHeight: heightBase + uint64(i),
		}
		enqueueProto(t, dbMgr, keys.KeyArchiveFrostWithdraw(id), state)
		dbMgr.EnqueueSet(keys.KeyArchiveFrostWithdrawDone(chain, asset, state.Seq, id), id)
	}

	for i := 1; i <= activeCount; i++ {
		seq := uint64(completedCount + i)
		id := fmt.Sprintf("%s-%s-active-%d", chain, asset, i)
		state := &pb.FrostWithdrawState{
			WithdrawId:    id,
			Chain:         chain,
			Asset:         asset,
			Status:        "QUEUED",
			Seq:           seq,
			RequestHeight: heightBase + 100 + uint64(i),
		}
		enqueueProto(t, dbMgr, keys.KeyFrostWithdraw(id), state)
		dbMgr.EnqueueSet(keys.KeyFrostWithdrawFIFOIndex(chain, asset, seq), id)
	}
}

func decodeFrostWithdrawMonitor(t *testing.T, rec *httptest.ResponseRecorder) *pb.FrostWithdrawStateList {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response pb.FrostWithdrawStateList
	if err := proto.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode frost withdraw monitor: %v", err)
	}
	return &response
}

func TestHandleFrostWithdrawMonitorDoesNotStarveLaterQueuesAndSupportsChainFilters(t *testing.T) {
	hm, dbMgr := newFrostQueryTestHandler(t)
	defer dbMgr.Close()

	enqueueFrostWithdrawMonitorQueue(t, dbMgr, "arb", "ARB", 100)
	enqueueFrostWithdrawMonitorQueue(t, dbMgr, "base", "BASE", 200)
	enqueueFrostWithdrawMonitorQueue(t, dbMgr, "btc", "BTC", 300)
	enqueueFrostWithdrawMonitorQueue(t, dbMgr, "btc", "ORDI", 400)
	if err := dbMgr.ForceFlush(); err != nil {
		t.Fatalf("force flush: %v", err)
	}

	globalRec := httptest.NewRecorder()
	hm.HandleFrostWithdrawMonitor(globalRec, httptest.NewRequest(http.MethodGet, "/frost/withdraw/monitor?limit=4&completed_limit=4", nil))
	global := decodeFrostWithdrawMonitor(t, globalRec)
	activeBTC, completedBTC := false, false
	activeCount, completedCount := 0, 0
	for _, state := range global.States {
		if isFrostWithdrawTerminal(state.Status) {
			completedCount++
			completedBTC = completedBTC || state.Chain == "btc"
			continue
		}
		activeCount++
		activeBTC = activeBTC || state.Chain == "btc"
	}
	if activeCount != 4 || completedCount != 4 || !activeBTC || !completedBTC {
		t.Fatalf("unfair monitor result: active=%d completed=%d activeBTC=%v completedBTC=%v states=%v",
			activeCount, completedCount, activeBTC, completedBTC, global.States)
	}

	chainRec := httptest.NewRecorder()
	hm.HandleFrostWithdrawMonitor(chainRec, httptest.NewRequest(http.MethodGet, "/frost/withdraw/monitor?chain=BTC&limit=20&completed_limit=20", nil))
	chainOnly := decodeFrostWithdrawMonitor(t, chainRec)
	assets := make(map[string]bool)
	for _, state := range chainOnly.States {
		if state.Chain != "btc" {
			t.Fatalf("chain filter returned %q state: %+v", state.Chain, state)
		}
		assets[state.Asset] = true
	}
	if len(chainOnly.States) != 8 || !assets["BTC"] || !assets["ORDI"] {
		t.Fatalf("chain-only monitor result: states=%d assets=%v", len(chainOnly.States), assets)
	}

	assetRec := httptest.NewRecorder()
	hm.HandleFrostWithdrawMonitor(assetRec, httptest.NewRequest(http.MethodGet, "/frost/withdraw/monitor?chain=BTC&asset=BTC&limit=20&completed_limit=20", nil))
	assetOnly := decodeFrostWithdrawMonitor(t, assetRec)
	if len(assetOnly.States) != 4 {
		t.Fatalf("chain+asset monitor states=%d, want 4", len(assetOnly.States))
	}
	for _, state := range assetOnly.States {
		if state.Chain != "btc" || state.Asset != "BTC" {
			t.Fatalf("chain+asset filter returned unexpected state: %+v", state)
		}
	}
}

func TestFrostVaultFundsUsesMaintainedAggregates(t *testing.T) {
	hm, dbMgr := newFrostQueryTestHandler(t)
	defer dbMgr.Close()
	dbMgr.EnqueueSet(keys.KeyFrostVaultAvailableBalance("btc", "BTC", 7), "60")
	dbMgr.EnqueueSet(keys.KeyFrostVaultFundsLedger("btc", "BTC", 7), "100")
	if err := dbMgr.ForceFlush(); err != nil {
		t.Fatalf("force flush: %v", err)
	}

	rec := httptest.NewRecorder()
	hm.HandleFrostVaultFunds(rec, httptest.NewRequest(http.MethodGet, "/frost/vault/funds?chain=btc", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response FrostVaultFundsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode vault funds: %v", err)
	}
	if len(response.Funds) != 1 || response.Funds[0].AvailableBalance != "60" || response.Funds[0].TotalBalance != "100" {
		t.Fatalf("funds=%+v", response.Funds)
	}
}

func TestFrostVaultListHasHardLimit(t *testing.T) {
	hm, dbMgr := newFrostQueryTestHandler(t)
	defer dbMgr.Close()
	for i := 0; i < maxFrostVaultListLimit+5; i++ {
		enqueueProto(t, dbMgr, keys.KeyFrostVaultState("btc", uint32(i)), &pb.FrostVaultState{Chain: "btc", VaultId: uint32(i)})
	}
	if err := dbMgr.ForceFlush(); err != nil {
		t.Fatalf("force flush: %v", err)
	}

	rec := httptest.NewRecorder()
	hm.HandleFrostVaultList(rec, httptest.NewRequest(http.MethodGet, "/frost/vault/list?limit=9999", nil))
	var response pb.FrostVaultStateList
	if err := proto.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode vault list: %v", err)
	}
	if len(response.States) != maxFrostVaultListLimit {
		t.Fatalf("vault list size=%d, want %d", len(response.States), maxFrostVaultListLimit)
	}
}
