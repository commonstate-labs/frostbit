package testharness_test

import (
	"dex/keys"
	"dex/liveness"
	"dex/pb"
	"dex/testharness"
	"testing"

	"google.golang.org/protobuf/proto"
)

func TestQueueTxAndMineBlock(t *testing.T) {
	h := testharness.New(t, 2)
	from, to := h.Committee[0], h.Committee[1]

	h.SeedAccount(from, "FB", "200000")
	h.SeedAccount(to, "FB", "0")

	tx := testharness.BuildTransferTx(from, to, "FB", "100")
	if err := h.SubmitTx(tx); err != nil {
		t.Fatalf("SubmitTx failed: %v", err)
	}
	h.AssertBalance(to, "FB", "0")

	spec, err := h.MineBlock()
	if err != nil {
		t.Fatalf("MineBlock failed: %v", err)
	}
	if spec == nil || len(spec.Receipts) != 1 {
		t.Fatalf("expected 1 receipt, got %+v", spec)
	}
	if spec.Receipts[0].Status != "SUCCEED" {
		t.Fatalf("expected receipt status SUCCEED, got %s err=%s", spec.Receipts[0].Status, spec.Receipts[0].Error)
	}
	if h.Height() != 1 {
		t.Fatalf("expected height=1, got %d", h.Height())
	}

	h.AssertBalance(to, "FB", "100")
	h.AssertBalance(from, "FB", "198900")                                    

	raw, ok := h.RawGet(keys.KeyLatestHeight())
	if !ok {
		t.Fatalf("latest height key not found")
	}
	if string(raw) != "1" {
		t.Fatalf("expected latest height key=1, got %q", string(raw))
	}
}

func TestSubmitBlockAndTxLog(t *testing.T) {
	h := testharness.New(t, 3)
	a, b, c := h.Committee[0], h.Committee[1], h.Committee[2]

	h.SeedAccount(a, "FB", "300000")
	h.SeedAccount(b, "FB", "300000")
	h.SeedAccount(c, "FB", "0")

	h.SubmitBlock(
		testharness.BuildTransferTx(a, c, "FB", "100"),
		testharness.BuildTransferTx(b, c, "FB", "200"),
	)

	if h.Height() != 1 {
		t.Fatalf("expected height=1, got %d", h.Height())
	}
	h.AssertBalance(c, "FB", "300")
	h.AssertBalance(a, "FB", "298900")
	h.AssertBalance(b, "FB", "298800")

	logs := h.TxLog()
	if len(logs) != 2 {
		t.Fatalf("expected tx log len=2, got %d", len(logs))
	}
	for i, entry := range logs {
		if entry.Height != 1 {
			t.Fatalf("entry %d expected height=1, got %d", i, entry.Height)
		}
		if entry.Kind != "transfer" {
			t.Fatalf("entry %d expected kind=transfer, got %s", i, entry.Kind)
		}
		if entry.Status != "SUCCEED" {
			t.Fatalf("entry %d expected status=SUCCEED, got %s err=%s", i, entry.Status, entry.Error)
		}
	}
}

func TestAdvanceHeightMinesEmptyBlocks(t *testing.T) {
	h := testharness.New(t, 1)

	h.AdvanceHeight(3)
	if h.Height() != 3 {
		t.Fatalf("expected height=3, got %d", h.Height())
	}
	if h.QueueLen() != 0 {
		t.Fatalf("expected queue len=0, got %d", h.QueueLen())
	}

	raw, ok := h.RawGet(keys.KeyLatestHeight())
	if !ok {
		t.Fatalf("latest height key not found")
	}
	if string(raw) != "3" {
		t.Fatalf("expected latest height key=3, got %q", string(raw))
	}
}

func TestTxSubmitterQueuesTx(t *testing.T) {
	h := testharness.New(t, 2)
	from, to := h.Committee[0], h.Committee[1]

	h.SeedAccount(from, "FB", "200000")
	h.SeedAccount(to, "FB", "0")

	tx := testharness.BuildTransferTx(from, to, "FB", "50")
	txID, err := h.TxSubmitter().Submit(tx)
	if err != nil {
		t.Fatalf("submitter.Submit failed: %v", err)
	}
	if txID == "" {
		t.Fatalf("expected txID not empty")
	}
	h.AssertBalance(to, "FB", "0")

	h.AdvanceHeight(1)
	h.AssertBalance(to, "FB", "50")
}

func TestMinerExitRefundAfterFreezePeriod(t *testing.T) {
	h := testharness.New(t, 1)
	miner := h.Committee[0]

	const (
		initialFB = "3200005000"
		stakeFB   = "3200000000"
	)
	h.SeedAccount(miner, "FB", initialFB)

	h.SubmitTxExpectSuccess(h.BuildMinerTx(miner, pb.OrderOp_ADD, stakeFB))

	acc := mustGetAccount(t, h, miner)
	if !acc.IsMiner {
		t.Fatalf("expected account is_miner=true after join")
	}
	bal := mustGetTokenBalance(t, h, miner, "FB")
	if bal.MinerLockedBalance != stakeFB {
		t.Fatalf("expected locked=%s after join, got %s", stakeFB, bal.MinerLockedBalance)
	}
	if bal.Balance != "4000" {                                    
		t.Fatalf("expected liquid=4000 after join, got %s", bal.Balance)
	}

	h.SubmitTxExpectSuccess(testharness.BuildMinerTx(miner, pb.OrderOp_REMOVE, ""))

	acc = mustGetAccount(t, h, miner)
	if acc.IsMiner {
		t.Fatalf("expected account is_miner=false after exit")
	}
	bal = mustGetTokenBalance(t, h, miner, "FB")
	                                                               
	if bal.MinerLockedBalance != stakeFB {
		t.Fatalf("expected locked=%s after exit (freeze period), got %s", stakeFB, bal.MinerLockedBalance)
	}
	if bal.Balance != "3000" {                                      
		t.Fatalf("expected liquid=3000 after exit, got %s", bal.Balance)
	}

	                                   
	exitRecordKey := keys.KeyMinerExitRecord(miner)
	_, hasRecord := h.RawGet(exitRecordKey)
	if !hasRecord {
		t.Fatalf("expected miner exit record to exist")
	}

	                                                                                                   
	                                                                     
	h.AdvanceHeight(liveness.DefaultFreezeBlocks)

	bal = mustGetTokenBalance(t, h, miner, "FB")
	if bal.MinerLockedBalance != "0" {
		t.Fatalf("expected locked=0 after freeze period, got %s", bal.MinerLockedBalance)
	}
	if bal.Balance != "3200003000" {                                
		t.Fatalf("expected liquid=3200003000 after freeze release, got %s", bal.Balance)
	}
	if h.Liveness.IsRegistered(miner) {
		t.Fatalf("expected liveness record fully unregistered after stake release")
	}
}

func mustGetAccount(t *testing.T, h *testharness.Harness, addr string) *pb.Account {
	t.Helper()
	raw, ok := h.RawGet(keys.KeyAccount(addr))
	if !ok {
		t.Fatalf("account not found: %s", addr)
	}
	var acc pb.Account
	if err := proto.Unmarshal(raw, &acc); err != nil {
		t.Fatalf("unmarshal account %s: %v", addr, err)
	}
	return &acc
}

func mustGetTokenBalance(t *testing.T, h *testharness.Harness, addr, token string) *pb.TokenBalance {
	t.Helper()
	raw, ok := h.RawGet(keys.KeyBalance(addr, token))
	if !ok {
		t.Fatalf("balance not found: %s/%s", addr, token)
	}
	var rec pb.TokenBalanceRecord
	if err := proto.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("unmarshal balance %s/%s: %v", addr, token, err)
	}
	if rec.Balance == nil {
		t.Fatalf("nil balance: %s/%s", addr, token)
	}
	return rec.Balance
}
