package vm

import (
	"dex/keys"
	"dex/pb"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestTerminalWitnessCleanupIncludesReplacedUnvotedMembersAndOldVotes(t *testing.T) {
	for _, status := range []pb.RechargeRequestStatus{pb.RechargeRequestStatus_RECHARGE_FINALIZED, pb.RechargeRequestStatus_RECHARGE_REJECTED} {
		t.Run(status.String(), func(t *testing.T) {
			sv := newMemStateView()
			req := &pb.RechargeRequest{RequestId: "case", Status: status, SelectedWitnesses: []string{"new"},
				Votes:             []*pb.WitnessVote{{RequestId: "case", WitnessAddress: "new", VoteType: pb.WitnessVoteType_VOTE_PASS}},
				OriginalPositions: []*pb.WitnessVote{{RequestId: "case", WitnessAddress: "earlier"}},
				CaseCollateral:    []*pb.WitnessCaseCollateral{{Address: "silent-old", RoleMask: WitnessCaseRoleOriginal}, {Address: "ordinary", RoleMask: WitnessCaseRoleOrdinary}}}
			old := &pb.WitnessVote{RequestId: "case", WitnessAddress: "old-voter", VoteType: pb.WitnessVoteType_VOTE_FAIL, Reason: "old round evidence"}
			raw, err := proto.Marshal(old)
			require.NoError(t, err)
			sv.Set(keys.KeyWitnessVote("case", "old-voter"), raw)
			other := &pb.WitnessVote{RequestId: "case_child", WitnessAddress: "other"}
			otherRaw, err := proto.Marshal(other)
			require.NoError(t, err)
			sv.Set(keys.KeyWitnessVote(other.RequestId, other.WitnessAddress), otherRaw)
			members := []string{"new", "earlier", "silent-old", "ordinary", "old-voter"}
			for _, address := range members {
				sv.Set(keys.KeyWitnessAssigned(address, "case"), []byte("case"))
				sv.Set(keys.KeyWitnessAssignedCurrent(address, "case"), []byte("case"))
				sv.Set(keys.KeyLegacyWitnessAssigned(address, "case"), []byte("case"))
				sv.Set(keys.KeyWitnessAssigned(address, "different-case"), []byte("different-case"))
			}
			require.NoError(t, cleanupTerminalRechargeHotState(sv, req))
			for _, address := range members {
				for _, key := range []string{keys.KeyWitnessAssigned(address, "case"), keys.KeyWitnessAssignedCurrent(address, "case"), keys.KeyLegacyWitnessAssigned(address, "case"), keys.KeyWitnessVote("case", address)} {
					_, exists, err := sv.Get(key)
					require.NoError(t, err)
					require.False(t, exists, key)
				}
				_, exists, err := sv.Get(keys.KeyWitnessAssigned(address, "different-case"))
				require.NoError(t, err)
				require.True(t, exists)
			}
			archived, exists, err := sv.Get(keys.KeyArchiveWitnessVote("case", "old-voter"))
			require.NoError(t, err)
			require.True(t, exists)
			require.Equal(t, raw, archived)
			got, exists, err := sv.Get(keys.KeyWitnessVote(other.RequestId, other.WitnessAddress))
			require.NoError(t, err)
			require.True(t, exists)
			require.Equal(t, otherRaw, got)
			require.NoError(t, cleanupTerminalRechargeHotState(sv, req), "cleanup is idempotent")
		})
	}
}

type assignmentScanFailure struct {
	StateView
	prefix string
}

func (v assignmentScanFailure) Scan(prefix string) (map[string][]byte, error) {
	if prefix == v.prefix {
		return nil, errors.New("injected scan failure")
	}
	return v.StateView.Scan(prefix)
}

func TestTerminalWitnessCleanupFailureRollsBackArchiveAndIndexes(t *testing.T) {
	sv := newMemStateView()
	req := &pb.RechargeRequest{RequestId: "case", Status: pb.RechargeRequestStatus_RECHARGE_REJECTED, SelectedWitnesses: []string{"w"}}
	sv.Set(keys.KeyWitnessAssigned("w", "case"), []byte("case"))
	before := sv.Diff()
	require.ErrorContains(t, cleanupTerminalRechargeHotState(assignmentScanFailure{sv, keys.KeyWitnessVotePrefix("case")}, req), "injected scan failure")
	require.Equal(t, before, sv.Diff())
	sv.Set(keys.KeyWitnessVote("case", "w"), []byte{0xff})
	before = sv.Diff()
	require.Error(t, cleanupTerminalRechargeHotState(sv, req))
	require.Equal(t, before, sv.Diff())
}

func TestLegacyWitnessAssignmentsMigrateOnlyInConsensusAndOnce(t *testing.T) {
	sv := newMemStateView()
	legacy := keys.KeyLegacyWitnessAssigned("old", "case")
	sv.Set(legacy, []byte("case"))
	sv.Set(keys.KeyWitnessAssigned("new", "case"), []byte("case"))
	sv.Set(keys.KeyWitnessVote("case", "old"), []byte("untouched live vote"))
	before := sv.Diff()
	require.Error(t, migrateLegacyWitnessAssignments(assignmentScanFailure{sv, keys.KeyLegacyWitnessAssignedPrefix()}))
	require.Equal(t, before, sv.Diff())
	                                                                               
	require.NoError(t, (&Executor{}).applyPendingRechargeSelections(sv, 10))
	_, exists, err := sv.Get(legacy)
	require.NoError(t, err)
	require.False(t, exists)
	for _, key := range []string{keys.KeyWitnessAssigned("new", "case"), keys.KeyWitnessVote("case", "old"), keys.KeyWitnessAssignmentQueryMigrated()} {
		_, exists, err := sv.Get(key)
		require.NoError(t, err)
		require.True(t, exists)
	}
	require.NoError(t, migrateLegacyWitnessAssignments(assignmentScanFailure{sv, keys.KeyLegacyWitnessAssignedPrefix()}), "marker avoids future full scans")
}

func TestWitnessAssignmentReselectionKeepsHistoryButMovesCurrentTask(t *testing.T) {
	sv := newMemStateView()
	old := &pb.RechargeRequest{RequestId: "case", Status: pb.RechargeRequestStatus_RECHARGE_VOTING,
		SelectedWitnesses: []string{"old"}, CaseCollateral: []*pb.WitnessCaseCollateral{{Address: "old", RoleMask: 1}}}
	syncRechargeRequestIndexes(sv, nil, old, 1)
	next := proto.Clone(old).(*pb.RechargeRequest)
	next.SelectedWitnesses = []string{"new"}
	next.CaseCollateral = append(next.CaseCollateral, &pb.WitnessCaseCollateral{Address: "new", RoleMask: 1})
	syncRechargeRequestIndexes(sv, old, next, 1)
	for _, key := range []string{keys.KeyWitnessAssigned("old", "case"), keys.KeyWitnessAssigned("new", "case"), keys.KeyWitnessAssignedCurrent("new", "case")} {
		_, exists, err := sv.Get(key)
		require.NoError(t, err)
		require.True(t, exists, key)
	}
	_, exists, err := sv.Get(keys.KeyWitnessAssignedCurrent("old", "case"))
	require.NoError(t, err)
	require.False(t, exists)
	done := proto.Clone(next).(*pb.RechargeRequest)
	done.Status = pb.RechargeRequestStatus_RECHARGE_REJECTED
	syncRechargeRequestIndexes(sv, next, done, 1)
	rows, err := sv.Scan(keys.KeyWitnessAssignedPrefix(""))
	require.NoError(t, err)
	require.Empty(t, rows)
}
