package vm_test

import (
	"fmt"
	"testing"

	"dex/keys"
	"dex/pb"
	"dex/vm"
	"dex/witness"

	"google.golang.org/protobuf/proto"
)

func TestWitnessVoteGraceAcceptsNextBlockVoteAndRefundsFee(t *testing.T) {
	db := NewMockDB()
	reg := vm.NewHandlerRegistry()
	if err := vm.RegisterDefaultHandlers(reg); err != nil {
		t.Fatalf("register handlers: %v", err)
	}
	cfg := witness.DefaultConfig()
	cfg.ConsensusThreshold = 60
	cfg.VoteGracePeriodBlocks = 1
	executor := vm.NewExecutorWithWitness(db, reg, nil, cfg)

	const (
		requestID = "request_vote_grace_integration"
		height    = uint64(100)
	)
	selected := []string{"witness_1", "witness_2", "witness_3", "witness_4"}
	request := &pb.RechargeRequest{
		RequestId:         requestID,
		Status:            pb.RechargeRequestStatus_RECHARGE_VOTING,
		CreateHeight:      90,
		DeadlineHeight:    110,
		SelectedWitnesses: selected,
	}
	requestData, err := proto.Marshal(request)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	db.data[keys.KeyRechargeRequest(requestID)] = requestData
	executor.GetWitnessService().LoadRequest(request)

	for _, address := range selected {
		accountData, _ := proto.Marshal(&pb.Account{Address: address})
		db.data[keys.KeyAccount(address)] = accountData
		balanceData, _ := proto.Marshal(&pb.TokenBalanceRecord{Balance: &pb.TokenBalance{Balance: "100000"}})
		db.data[keys.KeyBalance(address, "FB")] = balanceData
	}

	firstBlock := &pb.Block{
		BlockHash: "block_vote_grace_decision",
		Header: &pb.BlockHeader{
			PrevBlockHash: "block_before_vote_grace",
			Height:        height,
		},
	}
	for i, address := range selected[:3] {
		firstBlock.Body = append(firstBlock.Body, buildGraceVoteTx(
			fmt.Sprintf("vote_grace_%d", i+1),
			address,
			requestID,
			pb.WitnessVoteType_VOTE_PASS,
		))
	}

	firstResult, err := executor.BuildSpecResult(firstBlock)
	if err != nil {
		t.Fatalf("build decision block: %v", err)
	}
	if !firstResult.Valid {
		t.Fatalf("decision block invalid: %s", firstResult.Reason)
	}
	if len(firstResult.Receipts) != 3 {
		t.Fatalf("decision receipt count=%d", len(firstResult.Receipts))
	}
	for i, receipt := range firstResult.Receipts {
		if receipt.Status != "SUCCEED" {
			t.Fatalf("vote %d failed: %s", i+1, receipt.Error)
		}
		if receipt.Fee != "0" {
			t.Fatalf("vote %d fee was not refunded: %q", i+1, receipt.Fee)
		}
	}
	applySpecDiffToMockDB(db, firstResult.Diff)

	nextBlock := &pb.Block{
		BlockHash: "block_vote_grace_trailing",
		Header: &pb.BlockHeader{
			PrevBlockHash: firstBlock.BlockHash,
			Height:        height + 1,
		},
		Body: []*pb.AnyTx{buildGraceVoteTx(
			"vote_grace_4",
			selected[3],
			requestID,
			pb.WitnessVoteType_VOTE_FAIL,
		)},
	}
	result, err := executor.BuildSpecResult(nextBlock)
	if err != nil {
		t.Fatalf("build grace block: %v", err)
	}
	if !result.Valid {
		t.Fatalf("grace block invalid: %s", result.Reason)
	}
	if len(result.Receipts) != 1 || result.Receipts[0].Status != "SUCCEED" {
		t.Fatalf("next-block vote failed: %+v", result.Receipts)
	}
	if result.Receipts[0].Fee != "0" {
		t.Fatalf("next-block vote fee was not refunded: %q", result.Receipts[0].Fee)
	}

	                                                                       
	                                                                          
	                                                                           
	retryBlock := proto.Clone(nextBlock).(*pb.Block)
	retryBlock.BlockHash = "block_vote_grace_trailing_retry"
	retryResult, err := executor.BuildSpecResult(retryBlock)
	if err != nil {
		t.Fatalf("rebuild grace block: %v", err)
	}
	if !retryResult.Valid || len(retryResult.Receipts) != 1 || retryResult.Receipts[0].Status != "SUCCEED" {
		t.Fatalf("speculative rebuild diverged: valid=%v receipts=%+v", retryResult.Valid, retryResult.Receipts)
	}

	var stored pb.RechargeRequest
	found := false
	for _, op := range result.Diff {
		if op.Key != keys.KeyRechargeRequest(requestID) || op.Del {
			continue
		}
		if err := proto.Unmarshal(op.Value, &stored); err != nil {
			t.Fatalf("unmarshal request diff: %v", err)
		}
		found = true
	}
	if !found {
		t.Fatalf("request update missing from block diff")
	}
	if stored.Status != pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD {
		t.Fatalf("want grace close to enter challenge period, got %v", stored.Status)
	}
	if stored.DecisionHeight != height || stored.VoteAcceptUntilHeight != height+1 {
		t.Fatalf("unexpected grace heights: decision=%d accept=%d", stored.DecisionHeight, stored.VoteAcceptUntilHeight)
	}
	if stored.PassCount != 3 || stored.FailCount != 1 || len(stored.Votes) != 4 {
		t.Fatalf("trailing vote was not retained: pass=%d fail=%d votes=%d", stored.PassCount, stored.FailCount, len(stored.Votes))
	}
}

func buildGraceVoteTx(txID, address, requestID string, voteType pb.WitnessVoteType) *pb.AnyTx {
	return &pb.AnyTx{
		Content: &pb.AnyTx_WitnessVoteTx{WitnessVoteTx: &pb.WitnessVoteTx{
			Base: &pb.BaseMessage{
				TxId:        txID,
				FromAddress: address,
				Fee:         "1000",
			},
			Vote: &pb.WitnessVote{
				RequestId:      requestID,
				WitnessAddress: address,
				VoteType:       voteType,
			},
		}},
	}
}

func applySpecDiffToMockDB(db *MockDB, diff []vm.WriteOp) {
	for _, op := range diff {
		if op.Del {
			delete(db.data, op.Key)
			continue
		}
		db.data[op.Key] = append([]byte(nil), op.Value...)
	}
}
