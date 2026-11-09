package db

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"dex/keys"
	"dex/logs"

	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/require"
)

func TestReferralIndexRebuildDrainsColdWritesAndKeepsStateRoot(t *testing.T) {
	mgr := newQueryPruneTestManager(t)
	updates := make([]interface{}, 0, 1205)
	for i := 0; i < 1205; i++ {
		updates = append(updates, syncTestOp{key: keys.KeyReferrer(fmt.Sprintf("invitee-%04d", i)), val: []byte("bob")})
	}
	_, err := mgr.SyncStateUpdates(1, updates)
	require.NoError(t, err)
	root := mgr.GetStateFingerprint()
	                                                                             
	mgr.EnqueueSet(keys.KeyReferralInvitee("old", "gone"), "stale")
	mgr.EnqueueDel(keys.KeyReferralInvitee("bob", "invitee-0000"))
	require.NoError(t, mgr.RebuildReferralInviteeIndex())
	require.NoError(t, mgr.ForceFlush())
	rows, err := mgr.ScanKV(keys.KeyReferralInviteePrefix(""))
	require.NoError(t, err)
	require.Len(t, rows, 1205)
	require.Contains(t, rows, keys.KeyReferralInvitee("bob", "invitee-0000"))
	require.NotContains(t, rows, keys.KeyReferralInvitee("old", "gone"))
	require.Equal(t, root, mgr.GetStateFingerprint())
	_, found, err := mgr.stateDB.Get(keys.KeyReferralInvitee("bob", "invitee-0000"))
	require.NoError(t, err)
	require.False(t, found)
	_, invitees, err := mgr.ReferralInfo("bob", 10000)
	require.NoError(t, err)
	require.Len(t, invitees, 101)
}

func TestReferralIndexStartupRebuildsMissingAndStaleRows(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(dir, logs.NewNodeLogger("referral-restart", 0))
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Close() })
	_, err = mgr.SyncStateUpdates(1, toIfaceOps(syncTestOp{key: keys.KeyReferrer("alice"), val: []byte("bob")}))
	require.NoError(t, err)
	require.NoError(t, mgr.queryDB.Set([]byte(keys.KeyReferralInvitee("old", "gone")), []byte{1}, pebble.Sync))
	root := mgr.GetStateFingerprint()
	mgr.Close()
	mgr, err = NewManager(dir, logs.NewNodeLogger("referral-restart", 0))
	require.NoError(t, err)
	referrer, _, err := mgr.ReferralInfo("alice", 101)
	require.NoError(t, err)
	require.Equal(t, "bob", referrer)
	_, invitees, err := mgr.ReferralInfo("bob", 101)
	require.NoError(t, err)
	require.Equal(t, []string{keys.KeyReferralInvitee("bob", "alice")}, invitees)
	_, invitees, err = mgr.ReferralInfo("old", 101)
	require.NoError(t, err)
	require.Empty(t, invitees)
	require.Equal(t, root, mgr.GetStateFingerprint())
}

func TestReferralIndexFailedRebuildDoesNotExposePartialResults(t *testing.T) {
	mgr := newQueryPruneTestManager(t)
	_, err := mgr.SyncStateUpdates(1, toIfaceOps(syncTestOp{key: keys.KeyReferrer("alice"), val: []byte{}}))
	require.NoError(t, err)
	require.Error(t, mgr.RebuildReferralInviteeIndex())
	_, _, err = mgr.ReferralInfo("bob", 101)
	require.ErrorContains(t, err, "referral index unavailable")
	_, err = mgr.SyncStateUpdates(2, toIfaceOps(syncTestOp{key: keys.KeyReferrer("alice"), val: []byte("bob")}))
	require.NoError(t, err)
	require.NoError(t, mgr.RebuildReferralInviteeIndex())
	_, invitees, err := mgr.ReferralInfo("bob", 101)
	require.NoError(t, err)
	require.Equal(t, []string{keys.KeyReferralInvitee("bob", "alice")}, invitees)
}

func TestCheckpointRestoresHandoverProofAndRebuildsReferralIndex(t *testing.T) {
	source := newQueryPruneTestManager(t)
	_, err := source.SyncStateUpdates(9, toIfaceOps(syncTestOp{key: keys.KeyReferrer("alice"), val: []byte("bob")}))
	require.NoError(t, err)
	beforeProof := source.GetStateFingerprint()
	proofKey, proof := keys.KeyFrostVaultHandoverProof("eth", 0, 2), []byte("current-epoch-authority-signature")
	_, err = source.SyncStateUpdates(10, toIfaceOps(syncTestOp{key: proofKey, val: proof}))
	require.NoError(t, err)
	root := source.GetStateFingerprint()
	require.NotEqual(t, beforeProof, root, "handover proof must participate in stateRootHash")
	require.NoError(t, source.EnsureStateCheckpoint(10))
	files, _, err := source.ListStateCheckpointFiles(10)
	require.NoError(t, err)
	importDir := t.TempDir()
	for _, file := range files {
		path := filepath.Join(importDir, filepath.FromSlash(file.Name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		out, err := os.Create(path)
		require.NoError(t, err)
		for offset := int64(0); offset < file.Size; {
			limit := min(int64(65536), file.Size-offset)
			n, err := source.WriteStateCheckpointChunk(10, file.Name, offset, limit, out)
			require.NoError(t, err)
			require.Equal(t, limit, n)
			offset += n
		}
		require.NoError(t, out.Close())
	}
	destination := newQueryPruneTestManager(t)
	_, err = destination.SyncStateUpdates(1, toIfaceOps(syncTestOp{key: keys.KeyReferrer("gone"), val: []byte("old")}))
	require.NoError(t, err)
	destination.EnqueueSet(keys.KeyReferralInvitee("old", "gone"), "stale")
	require.NoError(t, destination.InstallStateCheckpointDir(context.Background(), 1, 10, importDir, root, "certified-block-10"))
	require.Equal(t, root, destination.GetStateFingerprint())
	got, err := destination.Get(proofKey)
	require.NoError(t, err)
	require.Equal(t, proof, got)
	_, invitees, err := destination.ReferralInfo("bob", 101)
	require.NoError(t, err)
	require.Equal(t, []string{keys.KeyReferralInvitee("bob", "alice")}, invitees)
	_, invitees, err = destination.ReferralInfo("old", 101)
	require.NoError(t, err)
	require.Empty(t, invitees)
	                                                                       
	require.NoError(t, destination.CompleteStateCheckpointInstall(1, 10))
	require.Equal(t, root, destination.GetStateFingerprint())
}
