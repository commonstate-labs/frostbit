package vm

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"dex/db"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"dex/protocol"
	"dex/witness"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

                                                                                  
                                                                              
                                                                                  
func TestWitnessArbitrationPersistentRestartKeepsCourtAndSettlesOnce(t *testing.T) {
	const requestID = "persistent-arbitration-request"
	const challengeID = "persistent-arbitration-funding"
	const initialSupply = "84800801000"                                                                  
	directory := t.TempDir()
	var manager *db.Manager
	open := func() {
		var err error
		manager, err = db.NewManager(directory, logs.NewNodeLogger("arbitration-restart-test", 0))
		require.NoError(t, err)
		manager.InitWriteQueue(64, 10*time.Millisecond)
	}
	closeDB := func() {
		require.NoError(t, manager.ForceFlush())
		manager.Close()
		manager = nil
	}
	t.Cleanup(func() {
		if manager != nil {
			manager.Close()
		}
	})
	newExecutor := func() *Executor {
		svc := witness.NewService(witness.DefaultConfig())
		t.Cleanup(svc.Stop)
		return &Executor{WitnessService: svc}
	}
	open()

	                                                                           
	                                                                           
	sv := NewStateView(manager.Get, manager.Scan)
	participants := make([]string, 0, 8)
	for i := 1; i <= 8; i++ {
		address := fmt.Sprintf("restart-w%02d", i)
		participants = append(participants, address)
		stake := fmt.Sprint(10000000000 + int64(8-i)*100000000)
		persistentArbitrationPut(t, sv, keys.KeyAccount(address), &pb.Account{Address: address})
		persistentArbitrationPut(t, sv, keys.KeyWitnessInfo(address), &pb.WitnessInfo{
			Address: address, StakeAmount: stake, Status: pb.WitnessStatus_WITNESS_ACTIVE,
		})
		SetBalance(sv, address, FeeToken, &pb.TokenBalance{Balance: "100000", WitnessLockedBalance: stake})
	}
	for address, balance := range map[string]string{"restart-payer": "1000", "restart-contributor": "2000000000"} {
		persistentArbitrationPut(t, sv, keys.KeyAccount(address), &pb.Account{Address: address})
		SetBalance(sv, address, FeeToken, &pb.TokenBalance{Balance: balance})
	}
	persistentArbitrationPut(t, sv, keys.KeyToken(FeeToken), &pb.Token{Address: FeeToken, TotalSupply: initialSupply})
	persistentArbitrationCommit(t, manager, sv, 1)

	sv = NewStateView(manager.Get, manager.Scan)
	x := newExecutor()
	req := &pb.RechargeRequest{
		RequestId: requestID, RequesterAddress: "restart-payer", RechargeFee: "103",
		NativeChain: "eth", NativeTxHash: fmt.Sprintf("0x%064x", 71),
		Status: pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD, CreateHeight: 2,
		DeadlineHeight: 100, PassCount: 4,
	}
	require.NoError(t, collectWitnessServiceFee(sv, req, false))
	assignments := make([]WitnessCaseAssignment, 0, 4)
	for _, address := range participants[:4] {
		req.SelectedWitnesses = append(req.SelectedWitnesses, address)
		req.OriginalPositions = append(req.OriginalPositions, &pb.WitnessVote{
			RequestId: requestID, WitnessAddress: address, VoteType: pb.WitnessVoteType_VOTE_PASS,
		})
		assignments = append(assignments, WitnessCaseAssignment{Address: address, Role: WitnessCaseRoleOriginal, Amount: WitnessCaseBondRaw})
	}
	require.NoError(t, reserveWitnessCaseCollateral(sv, req, assignments, "3200000000"))
	require.NoError(t, setRechargeRequestWithIndexes(sv, req, 3))
	fund := &pb.AnyTx{Content: &pb.AnyTx_WitnessChallengeTx{WitnessChallengeTx: &pb.WitnessChallengeTx{
		Base:      &pb.BaseMessage{TxId: challengeID, FromAddress: "restart-contributor", ExecutedHeight: 10},
		RequestId: requestID, StakeAmount: "1000000000",
	}}}
	ops, receipt, err := (&WitnessChallengeTxHandler{witnessSvc: x.WitnessService}).DryRun(fund, sv)
	require.NoError(t, err)
	require.NotNil(t, receipt)
	require.Equal(t, "SUCCEED", receipt.Status, receipt.Error)
	require.Empty(t, ops, "direct funding must not return its already-applied writes")
	ch := loadChallengeRecordAtKey(sv, keys.KeyChallengeRecord(challengeID))
	require.NotNil(t, ch)
	height := challengeSelectionReadyHeight(ch, 3)
	require.NoError(t, x.applyPendingArbitrationSelections(sv, height))
	ch = loadChallengeRecordAtKey(sv, keys.KeyChallengeRecord(challengeID))
	height = ch.DeadlineHeight + 1
	require.NoError(t, x.applyArbitrationTimeouts(sv, height))
	ch = loadChallengeRecordAtKey(sv, keys.KeyChallengeRecord(challengeID))
	require.Equal(t, uint32(2), ch.ArbitrationRound)
	                                                                         
	                                                                        
	persistentArbitrationBallot(t, sv, x, ch, participants[4], pb.WitnessVoteType_VOTE_FAIL, height, true)
	height = ch.DeadlineHeight + 1
	require.NoError(t, x.applyArbitrationTimeouts(sv, height))
	ch = loadChallengeRecordAtKey(sv, keys.KeyChallengeRecord(challengeID))
	court := append([]string(nil), participants[:4]...)
	require.Equal(t, uint32(3), ch.ArbitrationRound)
	require.Equal(t, court, ch.FinalCourt)
	require.Equal(t, height, ch.CourtSnapshotHeight)
	require.Equal(t, height+protocol.ArbitrationRoundBlocks, ch.DeadlineHeight)
	q, err := witness.ArbitrationQuorum(ch)
	require.NoError(t, err)
	require.Equal(t, uint32(12), q, "eight available members must not lower the planned third-round quorum")

	                                                                       
	var exiting pb.WitnessInfo
	persistentArbitrationDecode(t, sv, keys.KeyWitnessInfo(court[0]), &exiting)
	exiting.Status = pb.WitnessStatus_WITNESS_UNSTAKING
	exiting.UnstakeHeight = height
	persistentArbitrationPut(t, sv, keys.KeyWitnessInfo(court[0]), &exiting)
	thirdRoundDue := keys.KeyChallengeTimeoutDue(ch.DeadlineHeight+1, challengeID)
	stateKeys := []string{
		keys.KeyRechargeRequest(requestID), keys.KeyChallengeRecord(challengeID),
		keys.KeyBalance(WitnessFeePoolAccount, FeeToken), keys.KeyBalance("restart-contributor", FeeToken),
		keys.KeyChallengeContribution(challengeID, challengeID), thirdRoundDue,
	}
	for _, address := range participants {
		stateKeys = append(stateKeys, keys.KeyWitnessInfo(address), keys.KeyBalance(address, FeeToken))
	}
	expectedPersistent := map[string][]byte{}
	for _, key := range stateKeys {
		require.True(t, keys.IsStatefulKey(key), "critical case data must live in StateDB: %s", key)
		raw, exists, err := sv.Get(key)
		require.NoError(t, err)
		require.True(t, exists, key)
		expectedPersistent[key] = append([]byte(nil), raw...)
	}
	persistentArbitrationCommit(t, manager, sv, height)
	beforeRestartRoot := append([]byte(nil), manager.GetStateFingerprint()...)
	x.WitnessService.Stop()
	closeDB()

	                                                                          
	                                                                         
	open()
	require.Equal(t, beforeRestartRoot, manager.GetStateFingerprint())
	for key, want := range expectedPersistent {
		got, exists, err := manager.GetState(key)
		require.NoError(t, err)
		require.True(t, exists, key)
		require.True(t, bytes.Equal(want, got), "StateDB reload changed %s", key)
	}
	committedHeight, exists, err := manager.GetFinalizedStateHeight()
	require.NoError(t, err)
	require.True(t, exists)
	require.Equal(t, height, committedHeight)
	sv = NewStateView(manager.Get, manager.Scan)
	x = newExecutor()
	req = loadRechargeRequestAtKey(sv, keys.KeyRechargeRequest(requestID))
	ch = loadChallengeRecordAtKey(sv, keys.KeyChallengeRecord(challengeID))
	require.Equal(t, protocol.ArbitrationProtocolVersion, req.ArbitrationProtocolVersion)
	require.Equal(t, "103", req.FeeEscrow)
	require.False(t, req.FeeSettled)
	require.Len(t, req.CaseCollateral, 8)
	require.Equal(t, "1000000000", ch.StakeAmount)
	require.Equal(t, court, ch.FinalCourt)
	require.Len(t, ch.OrdinaryVotes, 1)
	require.Equal(t, participants[4], ch.OrdinaryVotes[0].WitnessAddress)
	for _, address := range participants {
		var info pb.WitnessInfo
		persistentArbitrationDecode(t, sv, keys.KeyWitnessInfo(address), &info)
		require.Equal(t, uint64(1), info.ActiveCaseCount, address)
		require.Equal(t, WitnessCaseBondRaw, info.ReservedStakeAmount, address)
	}
	height = ch.DeadlineHeight + 1
	require.NoError(t, x.applyArbitrationTimeouts(sv, height), "the persisted due index must drive court entry")
	ch = loadChallengeRecordAtKey(sv, keys.KeyChallengeRecord(challengeID))
	require.Equal(t, pb.ArbitrationStage_ARBITRATION_FINAL_COURT, ch.ArbitrationStage)
	require.True(t, ch.OrdinaryVotesFrozen)
	require.Equal(t, court, ch.Arbitrators)
	_, oldDueExists, err := sv.Get(thirdRoundDue)
	require.NoError(t, err)
	require.False(t, oldDueExists)
	for i, address := range court {
		direction := pb.WitnessVoteType_VOTE_FAIL
		if i < 2 {
			direction = pb.WitnessVoteType_VOTE_PASS
		}
		persistentArbitrationBallot(t, sv, x, ch, address, direction, height, true)
	}
	tied := loadChallengeRecordAtKey(sv, keys.KeyChallengeRecord(challengeID))
	require.False(t, tied.Settled)
	require.Equal(t, uint32(2), tied.PassCount)
	require.Equal(t, uint32(2), tied.FailCount)
	height = tied.DeadlineHeight + 1
	require.NoError(t, x.applyArbitrationTimeouts(sv, height))
	ch = loadChallengeRecordAtKey(sv, keys.KeyChallengeRecord(challengeID))
	require.Equal(t, court, ch.FinalCourt)
	require.Equal(t, court, ch.Arbitrators)
	require.Equal(t, tied.BallotRound+1, ch.BallotRound)
	require.Empty(t, ch.ArbitrationVotes)
	require.Equal(t, "1000000000", ch.StakeAmount)
	finalDue := keys.KeyChallengeTimeoutDue(ch.DeadlineHeight+1, challengeID)
	_, exists, err = sv.Get(finalDue)
	require.NoError(t, err)
	require.True(t, exists)
	for _, address := range court[:3] {
		persistentArbitrationBallot(t, sv, x, ch, address, pb.WitnessVoteType_VOTE_FAIL, height, true)
	}
	finalChallenge := loadChallengeRecordAtKey(sv, keys.KeyChallengeRecord(challengeID))
	finalRequest := loadRechargeRequestAtKey(sv, keys.KeyRechargeRequest(requestID))
	require.True(t, finalChallenge.Settled)
	require.True(t, finalChallenge.ChallengeSuccess)
	require.Equal(t, "0", finalChallenge.StakeAmount)
	require.Equal(t, pb.RechargeRequestStatus_RECHARGE_REJECTED, finalRequest.Status)
	require.True(t, finalRequest.FeeSettled)
	require.Equal(t, "0", finalRequest.FeeEscrow)
	                                                                          
	                                                                           
	for address, want := range map[string]string{
		"restart-contributor": "3800000000", "restart-payer": "897",
		participants[4]: "1800100103", WitnessFeePoolAccount: "0",
	} {
		require.Equal(t, want, GetBalance(sv, address, FeeToken).Balance, address)
	}
	for i, address := range participants {
		var info pb.WitnessInfo
		persistentArbitrationDecode(t, sv, keys.KeyWitnessInfo(address), &info)
		require.Zero(t, info.ActiveCaseCount, address)
		require.Equal(t, "0", info.ReservedStakeAmount, address)
		stake := int64(10000000000 + (8-i-1)*100000000)
		if i < 4 {
			stake -= 1000000000
			require.Equal(t, uint64(1), info.SlashedCount, address)
			require.Equal(t, "100000", GetBalance(sv, address, FeeToken).Balance, "wrong court member must not receive any reward")
		} else {
			require.Zero(t, info.SlashedCount, address)
		}
		require.Equal(t, fmt.Sprint(stake), info.StakeAmount, address)
		require.Equal(t, info.StakeAmount, GetBalance(sv, address, FeeToken).WitnessLockedBalance, address)
		_, assignmentExists, err := sv.Get(keys.KeyArbitratorChallenge(address, challengeID))
		require.NoError(t, err)
		require.False(t, assignmentExists, address)
	}
	var token pb.Token
	persistentArbitrationDecode(t, sv, keys.KeyToken(FeeToken), &token)
	require.Equal(t, "84400801000", token.TotalSupply)
	for _, key := range []string{finalDue, keys.KeyChallengeContribution(challengeID, challengeID)} {
		_, exists, err := sv.Get(key)
		require.NoError(t, err)
		require.False(t, exists, key)
	}
	persistentArbitrationCommit(t, manager, sv, height)
	terminalRoot := append([]byte(nil), manager.GetStateFingerprint()...)
	x.WitnessService.Stop()
	closeDB()

	                                                                           
	                                                                              
	open()
	require.Equal(t, terminalRoot, manager.GetStateFingerprint())
	sv = NewStateView(manager.Get, manager.Scan)
	x = newExecutor()
	var archived pb.ChallengeRecord
	persistentArbitrationDecode(t, sv, keys.KeyArchiveChallengeRecord(challengeID), &archived)
	require.Equal(t, court, archived.FinalCourt)
	require.True(t, archived.Settled)
	require.Equal(t, pb.WitnessVoteType_VOTE_FAIL, archived.FinalVote)
	var contribution pb.WitnessChallengeTx
	persistentArbitrationDecode(t, sv, keys.KeyArchiveChallengeContribution(challengeID, challengeID), &contribution)
	require.Equal(t, "1000000000", contribution.StakeAmount)
	var archivedRequest pb.RechargeRequest
	persistentArbitrationDecode(t, sv, keys.KeyArchiveRechargeRequest(requestID), &archivedRequest)
	require.Len(t, archivedRequest.CaseCollateral, 8)
	for _, bond := range archivedRequest.CaseCollateral {
		require.True(t, bond.Released, bond.Address)
		require.Equal(t, "0", bond.ReservedAmount, bond.Address)
	}
	require.True(t, loadChallengeRecordAtKey(sv, keys.KeyChallengeRecord(challengeID)).Settled)
	require.True(t, loadRechargeRequestAtKey(sv, keys.KeyRechargeRequest(requestID)).FeeSettled)
	for _, address := range participants {
		var info pb.WitnessInfo
		persistentArbitrationDecode(t, sv, keys.KeyWitnessInfo(address), &info)
		require.Zero(t, info.ActiveCaseCount, address)
		require.Equal(t, "0", info.ReservedStakeAmount, address)
		if address == court[0] {
			require.Equal(t, pb.WitnessStatus_WITNESS_UNSTAKING, info.Status)
		}
		_, exists, err := sv.Get(keys.KeyArbitratorChallenge(address, challengeID))
		require.NoError(t, err)
		require.False(t, exists, address)
	}
	for _, key := range []string{thirdRoundDue, finalDue, keys.KeyChallengeContribution(challengeID, challengeID)} {
		_, exists, err := sv.Get(key)
		require.NoError(t, err)
		require.False(t, exists, "terminal cleanup must survive reopen: %s", key)
	}
	persistentArbitrationBallot(t, sv, x, ch, court[3], pb.WitnessVoteType_VOTE_FAIL, height+1, false)
	archived.Settled = false                                                                     
	require.NoError(t, applyArbitrationOutcomeToState(sv, &witness.ArbitrationVoteOutcome{
		Challenge: &archived, Request: &archivedRequest,
	}, 3, 0, height+1))
	require.Empty(t, sv.Diff(), "reloaded terminal state must reject all second payouts and duty updates")
	require.Equal(t, "3800000000", GetBalance(sv, "restart-contributor", FeeToken).Balance)
	require.Equal(t, "1800100103", GetBalance(sv, participants[4], FeeToken).Balance)
	persistentArbitrationDecode(t, sv, keys.KeyToken(FeeToken), &token)
	require.Equal(t, "84400801000", token.TotalSupply)
}

func persistentArbitrationCommit(t *testing.T, manager *db.Manager, sv StateView, height uint64) {
	t.Helper()
	diff := sv.Diff()
	AuditGlobalConservationDiff(diff, height, manager.Get)
	                                                                          
	                                                                          
	_, err := manager.SyncFinalizedStateUpdatesForBlock(height, fmt.Sprintf("arbitration-persist-%d", height), writeOpsAsInterfaces(diff), false)
	require.NoError(t, err)
	for _, op := range diff {
		if keys.IsStatefulKey(op.Key) {
			continue
		}
		if op.Del {
			manager.EnqueueDelete(op.Key)
		} else {
			manager.EnqueueSet(op.Key, string(op.Value))
		}
	}
	require.NoError(t, manager.ForceFlush())
}

func persistentArbitrationPut(t *testing.T, sv StateView, key string, value proto.Message) {
	t.Helper()
	raw, err := proto.Marshal(value)
	require.NoError(t, err)
	sv.Set(key, raw)
}

func persistentArbitrationDecode(t *testing.T, sv StateView, key string, value proto.Message) {
	t.Helper()
	raw, exists, err := sv.Get(key)
	require.NoError(t, err)
	require.True(t, exists, key)
	require.NoError(t, proto.Unmarshal(raw, value), key)
}

func persistentArbitrationBallot(t *testing.T, sv StateView, x *Executor, ch *pb.ChallengeRecord, address string, vote pb.WitnessVoteType, height uint64, succeed bool) {
	t.Helper()
	tx := &pb.AnyTx{Content: &pb.AnyTx_ArbitrationVoteTx{ArbitrationVoteTx: &pb.ArbitrationVoteTx{
		Base:        &pb.BaseMessage{TxId: fmt.Sprintf("persistent-vote-%d-%s", ch.BallotRound, address), FromAddress: address, ExecutedHeight: height},
		ChallengeId: ch.ChallengeId, BallotRound: ch.BallotRound,
		Vote: &pb.WitnessVote{RequestId: ch.RequestId, WitnessAddress: address, VoteType: vote, BallotRound: ch.BallotRound},
	}}}
	ops, receipt, err := (&ArbitrationVoteTxHandler{witnessSvc: x.WitnessService}).DryRun(tx, sv)
	require.NoError(t, err)
	require.NotNil(t, receipt)
	status := "FAILED"
	if succeed {
		status = "SUCCEED"
	}
	require.Equal(t, status, receipt.Status, receipt.Error)
	require.Empty(t, ops)
}
