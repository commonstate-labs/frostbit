package db

import (
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"testing"

	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func assignmentSource(t *testing.T, req *pb.RechargeRequest) syncTestOp {
	t.Helper()
	raw, err := proto.Marshal(req)
	require.NoError(t, err)
	return syncTestOp{key: keys.KeyRechargeRequest(req.RequestId), val: raw}
}

func TestWitnessAssignmentRebuildPreservesRootAndCompleteOriginalMembership(t *testing.T) {
	mgr := newQueryPruneTestManager(t)
	_, err := mgr.SyncStateUpdates(7, toIfaceOps(
		assignmentSource(t, &pb.RechargeRequest{RequestId: "active", Status: pb.RechargeRequestStatus_RECHARGE_VOTING,
			SelectedWitnesses: []string{"current"}, Votes: []*pb.WitnessVote{{WitnessAddress: "voter"}}, OriginalPositions: []*pb.WitnessVote{{WitnessAddress: "earlier"}},
			CaseCollateral: []*pb.WitnessCaseCollateral{{Address: "silent-old", RoleMask: 1}, {Address: "court-only", RoleMask: 4}}}),
		assignmentSource(t, &pb.RechargeRequest{RequestId: "closed", Status: pb.RechargeRequestStatus_RECHARGE_FINALIZED, SelectedWitnesses: []string{"current"}}),
		syncTestOp{key: keys.KeyLegacyWitnessAssigned("legacy", "active"), val: []byte("active")},
	))
	require.NoError(t, err)
	root := mgr.GetStateFingerprint()
	mgr.EnqueueSet(keys.KeyWitnessAssigned("obsolete", "closed"), "closed")
	mgr.EnqueueDel(keys.KeyWitnessAssigned("current", "active"))
	require.NoError(t, mgr.RebuildWitnessAssignmentIndex())
	require.NoError(t, mgr.ForceFlush())
	rows, err := mgr.ScanKV(keys.KeyWitnessAssignedPrefix(""))
	require.NoError(t, err)
	require.Len(t, rows, 5)
	for _, member := range []string{"current", "voter", "earlier", "silent-old"} {
		key := keys.KeyWitnessAssigned(member, "active")
		require.Equal(t, []byte("active"), rows[key])
		_, exists, err := mgr.stateDB.Get(key)
		require.NoError(t, err)
		require.False(t, exists)
	}
	require.Equal(t, root, mgr.GetStateFingerprint(), "local reconstruction must not reclassify or delete old authenticated keys")
	_, exists, err := mgr.GetState(keys.KeyLegacyWitnessAssigned("legacy", "active"))
	require.NoError(t, err)
	require.True(t, exists)
}

func TestWitnessAssignmentStartupRebuildAndFailureBarrier(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(dir, logs.NewNodeLogger("assignments", 0))
	require.NoError(t, err)
	_, err = mgr.SyncStateUpdates(7, toIfaceOps(assignmentSource(t, &pb.RechargeRequest{RequestId: "active", Status: pb.RechargeRequestStatus_RECHARGE_VOTING, SelectedWitnesses: []string{"w"}})))
	require.NoError(t, err)
	root := mgr.GetStateFingerprint()
	mgr.Close()
	mgr, err = NewManager(dir, logs.NewNodeLogger("assignments", 0))
	require.NoError(t, err)
	defer mgr.Close()
	rows, err := mgr.ScanKVWithLimitOrdered(keys.KeyWitnessAssignedPrefix("w"), 10, false)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, root, mgr.GetStateFingerprint())
	_, err = mgr.SyncStateUpdates(8, toIfaceOps(syncTestOp{key: keys.KeyRechargeRequest("broken"), val: []byte{0xff}}))
	require.NoError(t, err)
	require.Error(t, mgr.RebuildWitnessAssignmentIndex())
	_, err = mgr.ScanKVWithLimitOrdered(keys.KeyWitnessAssignedPrefix("w"), 10, false)
	require.Error(t, err, "do not serve a partial rebuild")
	_, err = mgr.SyncStateUpdates(9, toIfaceOps(syncTestOp{key: keys.KeyRechargeRequest("broken"), del: true}))
	require.NoError(t, err)
	require.NoError(t, mgr.RebuildWitnessAssignmentIndex())
	require.NoError(t, mgr.queryDB.Delete([]byte(keys.KeyWitnessAssigned("w", "active")), pebble.Sync))
	require.NoError(t, mgr.CompleteStateCheckpointInstall(8, 9))
	rows, err = mgr.ScanKVWithLimitOrdered(keys.KeyWitnessAssignedPrefix("w"), 10, false)
	require.NoError(t, err)
	require.Len(t, rows, 1, "authenticated-anchor recovery rebuilds assignments")
}
