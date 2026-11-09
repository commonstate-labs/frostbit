package db

import (
	"errors"
	"testing"

	"dex/keys"
	"dex/pb"
	"dex/utils/diskspace"

	"github.com/cockroachdb/pebble"
	"google.golang.org/protobuf/proto"
)

func TestPruneFrostPlanningLogsDeletesOnlyTerminalWithdrawals(t *testing.T) {
	mgr := newQueryPruneTestManager(t)
	signedID := "withdraw-signed"
	permanentID := "withdraw-permanent"
	pendingID := "withdraw-pending"
	cancelledID := "withdraw-cancelled"
	malformedKey := keys.KeyFrostPlanningLog("withdraw-malformed", "node-1")

	writePlanningLog := func(withdrawID, reporter string) string {
		t.Helper()
		key := keys.KeyFrostPlanningLog(withdrawID, reporter)
		raw, err := proto.Marshal(&pb.FrostWithdrawPlanningLogTx{
			WithdrawId: withdrawID,
			Reporter:   reporter,
			Logs:       []*pb.FrostPlanningLog{{Step: "plan", Status: "OK"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		mgr.EnqueueSet(key, string(raw))
		return key
	}
	signedA := writePlanningLog(signedID, "node-1")
	signedB := writePlanningLog(signedID, "node-2")
	permanent := writePlanningLog(permanentID, "node-1")
	pending := writePlanningLog(pendingID, "node-1")
	cancelled := writePlanningLog(cancelledID, "node-1")
	mgr.EnqueueSet(malformedKey, "not-protobuf")

	signedRaw, err := proto.Marshal(&pb.FrostWithdrawState{WithdrawId: signedID, Status: "SIGNED"})
	if err != nil {
		t.Fatal(err)
	}
	pendingRaw, err := proto.Marshal(&pb.FrostWithdrawState{WithdrawId: pendingID, Status: "SIGNING"})
	if err != nil {
		t.Fatal(err)
	}
	mgr.EnqueueSet(keys.KeyArchiveFrostWithdraw(signedID), string(signedRaw))
	permanentRaw, err := proto.Marshal(&pb.FrostWithdrawState{WithdrawId: permanentID, Status: "SIGNED"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.SyncStateUpdates(1, toIfaceOps(syncTestOp{key: keys.KeyFrostPermanentWithdraw(permanentID), val: permanentRaw})); err != nil {
		t.Fatal(err)
	}
	mgr.EnqueueSet(keys.KeyFrostWithdraw(pendingID), string(pendingRaw))
	cancelledRaw, err := proto.Marshal(&pb.FrostWithdrawState{WithdrawId: cancelledID, Status: "CANCELLED"})
	if err != nil {
		t.Fatal(err)
	}
	mgr.EnqueueSet(keys.KeyArchiveFrostWithdraw(cancelledID), string(cancelledRaw))
	if err := mgr.ForceFlush(); err != nil {
		t.Fatal(err)
	}

	result, err := mgr.PruneFrostPlanningLogsWithDiskUsage(1_000, diskspace.Snapshot{TotalBytes: 100, FreeBytes: 50, FreePercent: 50})
	if err != nil {
		t.Fatal(err)
	}
	if result.DeletedRows != 4 || result.MalformedRows != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	for _, key := range []string{signedA, signedB, permanent, cancelled} {
		if _, err := mgr.GetKV(key); !errors.Is(err, pebble.ErrNotFound) {
			t.Fatalf("signed planning log %s still exists: %v", key, err)
		}
	}
	for _, key := range []string{pending, malformedKey} {
		if _, err := mgr.GetKV(key); err != nil {
			t.Fatalf("nonterminal/malformed planning log %s was removed: %v", key, err)
		}
	}
}

func TestPruneFrostPlanningLogsDoesNothingWithoutPressure(t *testing.T) {
	mgr := newQueryPruneTestManager(t)
	result, err := mgr.PruneFrostPlanningLogsWithDiskUsage(100, diskspace.Snapshot{TotalBytes: 100, FreeBytes: 75, FreePercent: 75})
	if err != nil {
		t.Fatal(err)
	}
	if result.ScannedRows != 0 || result.DeletedRows != 0 {
		t.Fatalf("unexpected prune without pressure: %+v", result)
	}
}
