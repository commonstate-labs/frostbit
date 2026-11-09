package workers

import (
	"testing"
	"time"

	"dex/pb"
)

func TestWithdrawWorker_BlockedPlanningLogCooldown(t *testing.T) {
	w := &WithdrawWorker{}
	base := time.Unix(100, 0)
	entry := &pb.FrostPlanningLog{Step: "SelectVault", Status: "FAILED", Message: "insufficient balance"}

	if !w.shouldReportBlockedPlanningLog("eth", "ETH.USDC", "wd_1", entry, base) {
		t.Fatal("first blocked planning log should be reported")
	}
	if w.shouldReportBlockedPlanningLog("eth", "ETH.USDC", "wd_1", entry, base.Add(time.Second)) {
		t.Fatal("same blocked planning log should be throttled within cooldown")
	}
	if !w.shouldReportBlockedPlanningLog("eth", "ETH.USDC", "wd_1", entry, base.Add(blockedPlanningLogCooldown+time.Second)) {
		t.Fatal("same blocked planning log should be reported after cooldown")
	}
	if !w.shouldReportBlockedPlanningLog("eth", "ETH.USDC", "wd_1", &pb.FrostPlanningLog{Step: "SelectVault", Status: "FAILED", Message: "different reason"}, base.Add(2*time.Second)) {
		t.Fatal("different blocked reason should not be throttled")
	}
}
