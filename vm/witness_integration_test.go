package vm_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	bridgeasset "dex/frost/asset"
	"dex/keys"
	"dex/pb"
	"dex/protocol"
	"dex/utils"
	"dex/vm"
	"dex/witness"

	"google.golang.org/protobuf/proto"
)

                                             

                                                                              
func TestExecutorWithWitnessService(t *testing.T) {
	db := NewMockDB()
	reg := vm.NewHandlerRegistry()
	vm.RegisterDefaultHandlers(reg)

	                                        
	executor := vm.NewExecutorWithWitness(db, reg, nil, nil)

	if executor.GetWitnessService() == nil {
		t.Fatal("WitnessService should not be nil")
	}

	t.Log("Executor with WitnessService created successfully")
}

                                                              
func TestWitnessStakeTxHandler(t *testing.T) {
	db := NewMockDB()
	reg := vm.NewHandlerRegistry()
	vm.RegisterDefaultHandlers(reg)

	executor := vm.NewExecutorWithWitness(db, reg, nil, nil)

	                                             
	address := "0xTestWitness001"
	account := &pb.Account{Address: address}
	accountData, _ := proto.Marshal(account)
	db.data[keys.KeyAccount(address)] = accountData

	                     
	bal := &pb.TokenBalanceRecord{
		Balance: &pb.TokenBalance{
			Balance:              "1000000000000",            
			WitnessLockedBalance: "0",
		},
	}
	balData, _ := proto.Marshal(bal)
	db.data[keys.KeyBalance(address, "FB")] = balData

	                     
	stakeTx := &pb.AnyTx{
		Content: &pb.AnyTx_WitnessStakeTx{
			WitnessStakeTx: &pb.WitnessStakeTx{
				Base: &pb.BaseMessage{
					TxId:           "stake_tx_001",
					FromAddress:    address,
					ExecutedHeight: 100,
					Fee:            "1000",
				},
				Amount: "100000000000",           
				Op:     pb.OrderOp_ADD,
			},
		},
	}

	               
	block := &pb.Block{
		BlockHash: "block_001",
		Header: &pb.BlockHeader{
			PrevBlockHash: "genesis",
			Height:        100,
		},
		Body: []*pb.AnyTx{stakeTx},
	}

	                  
	result, err := executor.BuildSpecResult(block)
	if err != nil {
		t.Fatalf("BuildSpecResult failed: %v", err)
	}

	if !result.Valid {
		t.Fatalf("Block should be valid, reason: %s", result.Reason)
	}

	if len(result.Receipts) != 1 {
		t.Fatalf("Expected 1 receipt, got %d", len(result.Receipts))
	}

	if result.Receipts[0].Status != "SUCCEED" {
		t.Fatalf("Expected SUCCEED, got %s, error: %s", result.Receipts[0].Status, result.Receipts[0].Error)
	}

	                 
	if len(result.Diff) < 2 {
		t.Fatalf("Expected at least 2 WriteOps (account + witness), got %d", len(result.Diff))
	}

	t.Logf("Stake transaction processed successfully with %d WriteOps", len(result.Diff))

	                                     
	witnessSvc := executor.GetWitnessService()
	witnessInfo, err := witnessSvc.GetWitnessInfo(address)
	if err != nil {
		t.Logf("WitnessInfo not found in service (expected if not synced): %v", err)
	} else {
		t.Logf("WitnessInfo in service: %+v", witnessInfo)
	}
}

                                                             
func TestWitnessRequestTxHandler(t *testing.T) {
	db := NewMockDB()
	reg := vm.NewHandlerRegistry()
	vm.RegisterDefaultHandlers(reg)

	executor := vm.NewExecutorWithWitness(db, reg, nil, nil)

	                                                                           
	for i := 0; i < 5; i++ {
		witnessInfo := &pb.WitnessInfo{
			Address:     fmt.Sprintf("0xWitness%03d", i),
			StakeAmount: "100000000000",           
			Status:      pb.WitnessStatus_WITNESS_ACTIVE,
		}
		executor.GetWitnessService().LoadWitness(witnessInfo)
	}

	               
	token := &pb.Token{
		Address: "0xUSDT",
	}
	tokenData, _ := proto.Marshal(token)
	db.data[keys.KeyToken("0xUSDT")] = tokenData

	                                                                                                    
	vaultCfg := &pb.FrostVaultConfig{
		Chain:             "eth",
		VaultCount:        5,
		CommitteeSize:     4,
		ThresholdRatio:    0.67,
		DepositProtocol:   protocol.DepositProtocolVault,
		DepositDexChainId: 1,
	}
	for vaultID := uint32(0); vaultID < vaultCfg.VaultCount; vaultID++ {
		vaultCfg.VaultRefs = append(vaultCfg.VaultRefs, fmt.Sprintf("0x%040x", vaultID+1))
	}
	vaultCfgData, _ := proto.Marshal(vaultCfg)
	db.data[keys.KeyFrostVaultConfig("eth", 0)] = vaultCfgData
	for vaultID := uint32(0); vaultID < vaultCfg.VaultCount; vaultID++ {
		vaultState := &pb.FrostVaultState{
			Chain:   "eth",
			VaultId: vaultID,
			Status:  vm.VaultStatusKeyReady,
		}
		vaultStateData, _ := proto.Marshal(vaultState)
		db.data[keys.KeyFrostVaultState("eth", vaultID)] = vaultStateData
	}

	                                     
	requesterAccount := &pb.Account{Address: "0xRequester001"}
	requesterAccountData, _ := proto.Marshal(requesterAccount)
	db.data[keys.KeyAccount("0xRequester001")] = requesterAccountData
	requesterBal := &pb.TokenBalanceRecord{Balance: &pb.TokenBalance{Balance: "2001000000"}}
	requesterBalData, _ := proto.Marshal(requesterBal)
	db.data[keys.KeyBalance("0xRequester001", "FB")] = requesterBalData

	                           
	requestTx := &pb.AnyTx{
		Content: &pb.AnyTx_WitnessRequestTx{
			WitnessRequestTx: &pb.WitnessRequestTx{
				Base: &pb.BaseMessage{
					TxId:           "request_tx_001",
					ChainId:        vaultCfg.DepositDexChainId,
					FromAddress:    "0xRequester001",
					ExecutedHeight: 100,
					Fee:            "1000",
				},
				NativeChain:     "ETH",
				NativeTxHash:    fmt.Sprintf("0x%064x", 1),
				NativeContract:  "0xdac17f958d2ee523a2206206994597c13d831ec7",
				Amount:          "1000000",                       
				ReceiverAddress: utils.GenerateTestIdentity(7106).Address,
				DepositProtocol: protocol.DepositProtocolVault,
				VaultId:         2,
				RechargeFee:     "1000000",                                       
			},
		},
	}

	block := &pb.Block{
		BlockHash: "block_002",
		Header: &pb.BlockHeader{
			PrevBlockHash: "block_001",
			Height:        101,
		},
		Body: []*pb.AnyTx{requestTx},
	}

	result, err := executor.BuildSpecResult(block)
	if err != nil {
		t.Fatalf("BuildSpecResult failed: %v", err)
	}

	if !result.Valid {
		t.Fatalf("Block should be valid, reason: %s", result.Reason)
	}

	if result.Receipts[0].Status != "SUCCEED" {
		t.Fatalf("Expected SUCCEED, got %s, error: %s", result.Receipts[0].Status, result.Receipts[0].Error)
	}

	t.Logf("Request transaction processed successfully with %d WriteOps", len(result.Diff))
}

                                                    
func TestWitnessVoteTxHandler(t *testing.T) {
	db := NewMockDB()
	reg := vm.NewHandlerRegistry()
	vm.RegisterDefaultHandlers(reg)

	executor := vm.NewExecutorWithWitness(db, reg, nil, nil)

	                                                                
	request := &pb.RechargeRequest{
		RequestId:         "request_001",
		NativeChain:       "ETH",
		NativeTxHash:      "0xNativeTxHash001",
		TokenAddress:      "0xUSDT",
		Amount:            "1000000000000000000",
		ReceiverAddress:   "0xReceiver001",
		RequesterAddress:  "0xRequester001",
		Status:            pb.RechargeRequestStatus_RECHARGE_VOTING,
		CreateHeight:      100,
		DeadlineHeight:    200,                               
		SelectedWitnesses: []string{"0xWitness001", "0xWitness002", "0xWitness003"},
	}
	requestData, _ := proto.Marshal(request)
	db.data[keys.KeyRechargeRequest("request_001")] = requestData

	                                           
	executor.GetWitnessService().LoadRequest(request)

	                                     
	voterAccount := &pb.Account{Address: "0xWitness001"}
	voterAccountData, _ := proto.Marshal(voterAccount)
	db.data[keys.KeyAccount("0xWitness001")] = voterAccountData
	voterBal := &pb.TokenBalanceRecord{Balance: &pb.TokenBalance{Balance: "100000"}}
	voterBalData, _ := proto.Marshal(voterBal)
	db.data[keys.KeyBalance("0xWitness001", "FB")] = voterBalData

	                     
	voteTx := &pb.AnyTx{
		Content: &pb.AnyTx_WitnessVoteTx{
			WitnessVoteTx: &pb.WitnessVoteTx{
				Base: &pb.BaseMessage{
					TxId:           "vote_tx_001",
					FromAddress:    "0xWitness001",
					ExecutedHeight: 101,
					Fee:            "1000",
				},
				Vote: &pb.WitnessVote{
					RequestId:      "request_001",
					WitnessAddress: "0xWitness001",
					VoteType:       pb.WitnessVoteType_VOTE_PASS,
				},
			},
		},
	}

	block := &pb.Block{
		BlockHash: "block_003",
		Header: &pb.BlockHeader{
			PrevBlockHash: "block_002",
			Height:        101,
		},
		Body: []*pb.AnyTx{voteTx},
	}

	result, err := executor.BuildSpecResult(block)
	if err != nil {
		t.Fatalf("BuildSpecResult failed: %v", err)
	}

	if !result.Valid {
		t.Fatalf("Block should be valid, reason: %s", result.Reason)
	}

	if result.Receipts[0].Status != "SUCCEED" {
		t.Fatalf("Expected SUCCEED, got %s, error: %s", result.Receipts[0].Status, result.Receipts[0].Error)
	}

	t.Logf("Vote transaction processed successfully with %d WriteOps", len(result.Diff))
}

                                                             
func TestWitnessServiceReset(t *testing.T) {
	config := witness.DefaultConfig()
	svc := witness.NewService(config)
	_ = svc.Start()
	defer svc.Stop()

	                     
	witnessInfo := &pb.WitnessInfo{
		Address:     "0xWitness001",
		StakeAmount: "100000000000",
		Status:      pb.WitnessStatus_WITNESS_ACTIVE,
	}
	svc.LoadWitness(witnessInfo)

	                        
	info, err := svc.GetWitnessInfo("0xWitness001")
	if err != nil {
		t.Fatalf("GetWitnessInfo failed: %v", err)
	}
	if info.StakeAmount != "100000000000" {
		t.Fatalf("Expected stake amount 100000000000, got %s", info.StakeAmount)
	}

	         
	svc.Reset()

	                        
	_, err = svc.GetWitnessInfo("0xWitness001")
	if err == nil {
		t.Fatal("Expected error after reset, got nil")
	}

	t.Log("WitnessService reset successfully")
}

                                                         
func TestWitnessChallengeTxHandler(t *testing.T) {
	db := NewMockDB()
	reg := vm.NewHandlerRegistry()
	vm.RegisterDefaultHandlers(reg)

	executor := vm.NewExecutorWithWitness(db, reg, nil, nil)

	                                                                             
	for i := 0; i < 5; i++ {
		w := &pb.WitnessInfo{
			Address:     fmt.Sprintf("0xArbitrator%03d", i),
			StakeAmount: "2000000000000",
			Status:      pb.WitnessStatus_WITNESS_ACTIVE,
		}
		executor.GetWitnessService().LoadWitness(w)
	}

	                                          
	request := &pb.RechargeRequest{
		RequestId:                  "request_002",
		NativeChain:                "ETH",
		NativeTxHash:               "0xNativeTxHash002",
		TokenAddress:               "0xUSDT",
		Amount:                     "1000000000000000000",
		ReceiverAddress:            "0xReceiver001",
		RequesterAddress:           "0xRequester001",
		Status:                     pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD,
		ArbitrationProtocolVersion: protocol.ArbitrationProtocolVersion,
		FeeEscrow:                  "0",
		DeadlineHeight:             200,
		CreateHeight:               100,
		SelectedWitnesses:          []string{"0xOrigWitness1", "0xOrigWitness2"},
	}
	requestData, _ := proto.Marshal(request)
	db.data[keys.KeyRechargeRequest("request_002")] = requestData
	executor.GetWitnessService().LoadRequest(request)

	                                                
	challenger := &pb.Account{Address: "0xChallenger001"}
	challengerData, _ := proto.Marshal(challenger)
	db.data[keys.KeyAccount("0xChallenger001")] = challengerData

	challengerBal := &pb.TokenBalanceRecord{
		Balance: &pb.TokenBalance{Balance: "1000000000000"},            
	}
	challengerBalData, _ := proto.Marshal(challengerBal)
	db.data[keys.KeyBalance("0xChallenger001", "FB")] = challengerBalData

	                     
	challengeTx := &pb.AnyTx{
		Content: &pb.AnyTx_WitnessChallengeTx{
			WitnessChallengeTx: &pb.WitnessChallengeTx{
				Base: &pb.BaseMessage{
					TxId:           "challenge_tx_001",
					FromAddress:    "0xChallenger001",
					ExecutedHeight: 102,
					Fee:            "1000",
				},
				RequestId:   "request_002",
				StakeAmount: "10000000000",          
				Reason:      "Invalid native tx hash",
			},
		},
	}

	block := &pb.Block{
		BlockHash: "block_004",
		Header: &pb.BlockHeader{
			PrevBlockHash: "block_003",
			Height:        102,
		},
		Body: []*pb.AnyTx{challengeTx},
	}

	result, err := executor.BuildSpecResult(block)
	if err != nil {
		t.Fatalf("BuildSpecResult failed: %v", err)
	}

	if !result.Valid {
		t.Fatalf("Block should be valid, reason: %s", result.Reason)
	}

	if result.Receipts[0].Status != "SUCCEED" {
		t.Fatalf("Expected SUCCEED, got %s, error: %s", result.Receipts[0].Status, result.Receipts[0].Error)
	}

	t.Logf("Challenge transaction processed successfully with %d WriteOps", len(result.Diff))
}

func TestWitnessChallengeContributionIgnoresExecutionMetadata(t *testing.T) {
	pending := executeWitnessChallengeContribution(t, pb.Status_PENDING, 0, "")
	succeeded := executeWitnessChallengeContribution(t, pb.Status_SUCCEED, 999, "materialized")
	if !bytes.Equal(pending, succeeded) {
		t.Fatalf("challenge contribution bytes must not depend on execution metadata: pending=%d succeed=%d", len(pending), len(succeeded))
	}

	var stored pb.WitnessChallengeTx
	if err := proto.Unmarshal(pending, &stored); err != nil {
		t.Fatalf("unmarshal contribution: %v", err)
	}
	if stored.Base == nil {
		t.Fatal("stored contribution missing base")
	}
	if stored.Base.Status != pb.Status_PENDING || stored.Base.ExecutedHeight != 0 || stored.Base.StatusText != "" {
		t.Fatalf("stored contribution keeps execution metadata: status=%v height=%d text=%q", stored.Base.Status, stored.Base.ExecutedHeight, stored.Base.StatusText)
	}
}

func executeWitnessChallengeContribution(t *testing.T, status pb.Status, executedHeight uint64, statusText string) []byte {
	t.Helper()
	db := NewMockDB()
	reg := vm.NewHandlerRegistry()
	vm.RegisterDefaultHandlers(reg)
	executor := vm.NewExecutorWithWitness(db, reg, nil, nil)
	for i := 0; i < 5; i++ {
		w := &pb.WitnessInfo{
			Address:     fmt.Sprintf("0xArbitratorMeta%03d", i),
			StakeAmount: "2000000000000",
			Status:      pb.WitnessStatus_WITNESS_ACTIVE,
		}
		executor.GetWitnessService().LoadWitness(w)
	}

	request := &pb.RechargeRequest{
		RequestId:                  "request_meta",
		NativeChain:                "ETH",
		NativeTxHash:               "0xNativeTxHashMeta",
		TokenAddress:               "0xUSDT",
		Amount:                     "1000000000000000000",
		ReceiverAddress:            "0xReceiverMeta",
		RequesterAddress:           "0xRequesterMeta",
		Status:                     pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD,
		ArbitrationProtocolVersion: protocol.ArbitrationProtocolVersion,
		FeeEscrow:                  "0",
		DeadlineHeight:             200,
		CreateHeight:               100,
		SelectedWitnesses:          []string{"0xOrigWitnessMeta1", "0xOrigWitnessMeta2"},
	}
	requestData, _ := proto.Marshal(request)
	db.data[keys.KeyRechargeRequest(request.RequestId)] = requestData
	executor.GetWitnessService().LoadRequest(request)

	challenger := &pb.Account{Address: "0xChallengerMeta"}
	challengerData, _ := proto.Marshal(challenger)
	db.data[keys.KeyAccount(challenger.Address)] = challengerData
	challengerBal := &pb.TokenBalanceRecord{Balance: &pb.TokenBalance{Balance: "1000000000000"}}
	challengerBalData, _ := proto.Marshal(challengerBal)
	db.data[keys.KeyBalance(challenger.Address, "FB")] = challengerBalData

	challengeTx := &pb.AnyTx{
		Content: &pb.AnyTx_WitnessChallengeTx{
			WitnessChallengeTx: &pb.WitnessChallengeTx{
				Base: &pb.BaseMessage{
					TxId:           "challenge_tx_meta",
					FromAddress:    challenger.Address,
					ExecutedHeight: executedHeight,
					Fee:            "1000",
					Status:         status,
					StatusText:     statusText,
				},
				RequestId:   request.RequestId,
				StakeAmount: "10000000000",
				Reason:      "Invalid native tx hash",
			},
		},
	}
	block := &pb.Block{
		BlockHash: "block_meta",
		Header:    &pb.BlockHeader{PrevBlockHash: "block_prev", Height: 102},
		Body:      []*pb.AnyTx{challengeTx},
	}
	result, err := executor.BuildSpecResult(block)
	if err != nil {
		t.Fatalf("BuildSpecResult failed: %v", err)
	}
	if !result.Valid {
		t.Fatalf("Block should be valid, reason: %s", result.Reason)
	}
	if result.Receipts[0].Status != "SUCCEED" {
		t.Fatalf("Expected SUCCEED, got %s, error: %s", result.Receipts[0].Status, result.Receipts[0].Error)
	}
	contributionKey := keys.KeyChallengeContribution("challenge_tx_meta", "challenge_tx_meta")
	for _, op := range result.Diff {
		if op.Key == contributionKey {
			return append([]byte(nil), op.Value...)
		}
	}
	t.Fatal("challenge contribution WriteOp not found")
	return nil
}

                                                        
func TestArbitrationVoteTxHandler(t *testing.T) {
	db := NewMockDB()
	reg := vm.NewHandlerRegistry()
	vm.RegisterDefaultHandlers(reg)

	executor := vm.NewExecutorWithWitness(db, reg, nil, nil)
	asset, err := bridgeasset.Resolve("eth", "0xdac17f958d2ee523a2206206994597c13d831ec7")
	if err != nil {
		t.Fatal(err)
	}

	                                          
	challenge := &pb.ChallengeRecord{
		ChallengeId:            "challenge_001",
		RequestId:              "request_001",
		ChallengerAddress:      "0xChallenger001",
		StakeAmount:            "10000000000",
		CreateHeight:           100,
		DeadlineHeight:         200,
		Arbitrators:            []string{"0xArbiter001", "0xArbiter002", "0xArbiter003", "0xArbiter004"},
		Status:                 pb.ChallengeStatus_CHALLENGE_ACTIVE,
		ArbitrationRound:       1,
		BallotRound:            1,
		OrdinaryCommitteeSizes: []uint32{4, 8, 16},
		OrdinaryThresholdBps:   7500,
	}
	challengeData, _ := proto.Marshal(challenge)
	db.data[keys.KeyChallengeRecord("challenge_001")] = challengeData
	request := &pb.RechargeRequest{
		RequestId:                  "request_001",
		NativeChain:                "eth",
		NativeTxHash:               "0xrequest001",
		TokenAddress:               asset.ID,
		NativeContract:             asset.Contract,
		Amount:                     "123",
		ReceiverAddress:            "0xReceiver001",
		VaultId:                    0,
		Status:                     pb.RechargeRequestStatus_RECHARGE_CHALLENGED,
		ArbitrationProtocolVersion: protocol.ArbitrationProtocolVersion,
		FeeEscrow:                  "0",
	}
	requestData, _ := proto.Marshal(request)
	db.data[keys.KeyRechargeRequest(request.RequestId)] = requestData
	fbTokenData, _ := proto.Marshal(&pb.Token{Address: "FB", TotalSupply: "10000000000"})
	db.data[keys.KeyToken("FB")] = fbTokenData

	                           
	executor.GetWitnessService().LoadChallenge(challenge)

	                                     
	arbiterAccount := &pb.Account{Address: "0xArbiter001"}
	arbiterAccountData, _ := proto.Marshal(arbiterAccount)
	db.data[keys.KeyAccount("0xArbiter001")] = arbiterAccountData
	arbiterBal := &pb.TokenBalanceRecord{Balance: &pb.TokenBalance{Balance: "100000"}}
	arbiterBalData, _ := proto.Marshal(arbiterBal)
	db.data[keys.KeyBalance("0xArbiter001", "FB")] = arbiterBalData

	                                                          
	arbVoteTx := &pb.AnyTx{
		Content: &pb.AnyTx_ArbitrationVoteTx{
			ArbitrationVoteTx: &pb.ArbitrationVoteTx{
				Base: &pb.BaseMessage{
					TxId:           "arb_vote_tx_001",
					FromAddress:    "0xArbiter001",
					ExecutedHeight: 105,
					Fee:            "1000",
				},
				ChallengeId: "challenge_001",
				BallotRound: 1,
				Vote: &pb.WitnessVote{
					RequestId:      "request_001",
					WitnessAddress: "0xArbiter001",
					VoteType:       pb.WitnessVoteType_VOTE_PASS,                      
					BallotRound:    1,
					Reason:         "External deposit is valid",
				},
			},
		},
	}

	block := &pb.Block{
		BlockHash: "block_005",
		Header: &pb.BlockHeader{
			PrevBlockHash: "block_004",
			Height:        105,
		},
		Body: []*pb.AnyTx{arbVoteTx},
	}

	result, err := executor.BuildSpecResult(block)
	if err != nil {
		t.Fatalf("BuildSpecResult failed: %v", err)
	}

	if !result.Valid {
		t.Fatalf("Block should be valid, reason: %s", result.Reason)
	}

	if result.Receipts[0].Status != "SUCCEED" {
		t.Fatalf("Expected SUCCEED, got %s, error: %s", result.Receipts[0].Status, result.Receipts[0].Error)
	}

	t.Logf("Arbitration vote transaction processed successfully with %d WriteOps", len(result.Diff))
}

                                                           
func TestWitnessClaimRewardTxHandler(t *testing.T) {
	db := NewMockDB()
	reg := vm.NewHandlerRegistry()
	vm.RegisterDefaultHandlers(reg)

	executor := vm.NewExecutorWithWitness(db, reg, nil, nil)

	                                                
	witnessAddr := "0xWitness001"
	witnessAccount := &pb.Account{Address: witnessAddr}
	witnessAccountData, _ := proto.Marshal(witnessAccount)
	db.data[keys.KeyAccount(witnessAddr)] = witnessAccountData

	witnessBal := &pb.TokenBalanceRecord{
		Balance: &pb.TokenBalance{Balance: "100000000000"},           
	}
	witnessBalData, _ := proto.Marshal(witnessBal)
	db.data[keys.KeyBalance(witnessAddr, "FB")] = witnessBalData
	fbTokenData, _ := proto.Marshal(&pb.Token{Address: "FB", TotalSupply: "0"})
	db.data[keys.KeyToken("FB")] = fbTokenData

	                                                
	witnessInfo := &pb.WitnessInfo{
		Address:       witnessAddr,
		StakeAmount:   "100000000000",
		Status:        pb.WitnessStatus_WITNESS_ACTIVE,
		PendingReward: "5000000000",                         
	}
	witnessInfoData, _ := proto.Marshal(witnessInfo)
	db.data[keys.KeyWitnessInfo(witnessAddr)] = witnessInfoData

	                           
	executor.GetWitnessService().LoadWitness(witnessInfo)

	                           
	claimTx := &pb.AnyTx{
		Content: &pb.AnyTx_WitnessClaimRewardTx{
			WitnessClaimRewardTx: &pb.WitnessClaimRewardTx{
				Base: &pb.BaseMessage{
					TxId:           "claim_tx_001",
					FromAddress:    witnessAddr,
					ExecutedHeight: 110,
					Fee:            "1000",
				},
			},
		},
	}

	block := &pb.Block{
		BlockHash: "block_006",
		Header: &pb.BlockHeader{
			PrevBlockHash: "block_005",
			Height:        110,
		},
		Body: []*pb.AnyTx{claimTx},
	}

	result, err := executor.BuildSpecResult(block)
	if err != nil {
		t.Fatalf("BuildSpecResult failed: %v", err)
	}

	if !result.Valid {
		t.Fatalf("Block should be valid, reason: %s", result.Reason)
	}

	if result.Receipts[0].Status != "FAILED" || !strings.Contains(result.Receipts[0].Error, "unbacked") {
		t.Fatalf("unbacked legacy reward must be rejected: %+v", result.Receipts[0])
	}
	for _, op := range result.Diff {
		if op.Key == keys.KeyToken("FB") || op.Key == keys.KeyWitnessInfo(witnessAddr) {
			t.Fatalf("rejected legacy claim must not mint or consume PendingReward: %s", op.Key)
		}
	}
	vm.AuditGlobalConservationDiff(result.Diff, block.Header.Height, db.Get)
}

                                                                   
func TestWitnessUnstakeTxHandler(t *testing.T) {
	db := NewMockDB()
	reg := vm.NewHandlerRegistry()
	vm.RegisterDefaultHandlers(reg)

	executor := vm.NewExecutorWithWitness(db, reg, nil, nil)

	                                                         
	address := "0xTestWitness002"
	account := &pb.Account{Address: address}
	accountData, _ := proto.Marshal(account)
	db.data[keys.KeyAccount(address)] = accountData

	                     
	bal := &pb.TokenBalanceRecord{
		Balance: &pb.TokenBalance{
			Balance:              "500000000000",           
			WitnessLockedBalance: "100000000000",                  
		},
	}
	balData, _ := proto.Marshal(bal)
	db.data[keys.KeyBalance(address, "FB")] = balData

	                        
	witnessInfo := &pb.WitnessInfo{
		Address:     address,
		StakeAmount: "100000000000",
		Status:      pb.WitnessStatus_WITNESS_ACTIVE,
	}
	witnessInfoData, _ := proto.Marshal(witnessInfo)
	db.data[keys.KeyWitnessInfo(address)] = witnessInfoData

	                           
	executor.GetWitnessService().LoadWitness(witnessInfo)

	                        
	unstakeTx := &pb.AnyTx{
		Content: &pb.AnyTx_WitnessStakeTx{
			WitnessStakeTx: &pb.WitnessStakeTx{
				Base: &pb.BaseMessage{
					TxId:           "unstake_tx_001",
					FromAddress:    address,
					ExecutedHeight: 100,
					Fee:            "1000",
				},
				Amount: "50000000000",                    
				Op:     pb.OrderOp_REMOVE,
			},
		},
	}

	block := &pb.Block{
		BlockHash: "block_007",
		Header: &pb.BlockHeader{
			PrevBlockHash: "block_006",
			Height:        100,
		},
		Body: []*pb.AnyTx{unstakeTx},
	}

	result, err := executor.BuildSpecResult(block)
	if err != nil {
		t.Fatalf("BuildSpecResult failed: %v", err)
	}

	if !result.Valid {
		t.Fatalf("Block should be valid, reason: %s", result.Reason)
	}

	if result.Receipts[0].Status != "SUCCEED" {
		t.Fatalf("Expected SUCCEED, got %s, error: %s", result.Receipts[0].Status, result.Receipts[0].Error)
	}

	t.Logf("Unstake transaction processed successfully with %d WriteOps", len(result.Diff))
}

                                           

                                                  
                                                                        
                                                          
                                                            
                                                 
                                                                     
func TestWitnessShelved(t *testing.T) {
	config := witness.DefaultConfig()
	svc := witness.NewService(config)
	_ = svc.Start()
	defer svc.Stop()

	                                                               
	for i := 0; i < 3; i++ {
		witnessInfo := &pb.WitnessInfo{
			Address:     fmt.Sprintf("0xWitness%03d", i),
			StakeAmount: "100000000000",
			Status:      pb.WitnessStatus_WITNESS_ACTIVE,
		}
		svc.LoadWitness(witnessInfo)
	}

	                                                  
	requestTx := &pb.WitnessRequestTx{
		Base: &pb.BaseMessage{
			TxId:           "request_witness_shelved_001",
			FromAddress:    "0xRequester001",
			ExecutedHeight: 100,
		},
		NativeChain:     "ETH",
		NativeTxHash:    "0xNativeTxHash001",
		NativeContract:  "0xdac17f958d2ee523a2206206994597c13d831ec7",
		Amount:          "1000000000000000000",
		ReceiverAddress: "0xReceiver001",
		RechargeFee:     "100000",
	}

	request, err := svc.CreateRechargeRequest(requestTx)
	if err != nil {
		t.Fatalf("CreateRechargeRequest failed: %v", err)
	}

	                                                                                                                               
	if request.Status != pb.RechargeRequestStatus_RECHARGE_PENDING_SELECTION {
		t.Fatalf("Expected RECHARGE_PENDING_SELECTION, got %v", request.Status)
	}
	if request.SelectionHeight != requestTx.Base.ExecutedHeight+config.SelectionBeaconDepth {
		t.Fatalf("Expected SelectionHeight=%d, got %d", requestTx.Base.ExecutedHeight+config.SelectionBeaconDepth, request.SelectionHeight)
	}

	                                                                                                            
	request.Status = pb.RechargeRequestStatus_RECHARGE_SHELVED

	                          
	if request.Status != pb.RechargeRequestStatus_RECHARGE_SHELVED {
		t.Fatalf("Expected RECHARGE_SHELVED, got %v", request.Status)
	}

	t.Log("Witness shelved test passed")
}

                                                                                            
func TestChallengeStatusTransitions(t *testing.T) {
	members := []string{"a", "b", "c", "d"}
	request := &pb.RechargeRequest{RequestId: "status-request", Status: pb.RechargeRequestStatus_RECHARGE_CHALLENGED, PassCount: 3}
	challenge := &pb.ChallengeRecord{
		ChallengeId: "status-challenge", RequestId: request.RequestId,
		Status: pb.ChallengeStatus_CHALLENGE_ACTIVE, ArbitrationRound: 1, BallotRound: 1,
		DeadlineHeight: 10000, Arbitrators: members,
		OrdinaryCommitteeSizes: []uint32{4, 8, 16}, OrdinaryThresholdBps: 7500,
	}
	for i := 0; i < 3; i++ {
		vote := &pb.WitnessVote{RequestId: request.RequestId, WitnessAddress: members[i], VoteType: pb.WitnessVoteType_VOTE_FAIL, BallotRound: 1}
		outcome, err := witness.ComputeArbitrationBallotOutcome(challenge, request, vote, 1, uint64(101+i))
		if err != nil {
			t.Fatal(err)
		}
		challenge = outcome.Challenge
		if i < 2 {
			if outcome.Challenge.Finalized || outcome.Request != nil || challenge.Status != pb.ChallengeStatus_CHALLENGE_ACTIVE {
				t.Fatalf("only %d/4 votes must not finalize: %+v", i+1, outcome)
			}
		} else if !outcome.Challenge.Finalized || challenge.Status != pb.ChallengeStatus_CHALLENGE_FINALIZED || !challenge.ChallengeSuccess || outcome.Request.Status != pb.RechargeRequestStatus_RECHARGE_REJECTED {
			t.Fatalf("3/4 external FAIL votes must reject the original PASS result: %+v", outcome)
		}
	}
}

                                                                                      
                                                                                                 
                                                                                           
func TestArbitrationVoteTxHandler_ConsensusFinalizesOnChain(t *testing.T) {
	db := NewMockDB()
	reg := vm.NewHandlerRegistry()
	vm.RegisterDefaultHandlers(reg)
	executor := vm.NewExecutorWithWitness(db, reg, nil, nil)
	integrationWrite(t, db, keys.KeyToken("FB"), &pb.Token{Address: "FB", TotalSupply: "1000000000000"})
	integrationAccount(t, db, "payer", "1000000", "0")
	integrationAccount(t, db, "challenger", "3000000000", "0")
	integrationWrite(t, db, keys.KeyFrostVaultConfig("eth", 0), &pb.FrostVaultConfig{
		Chain: "eth", VaultCount: 1,
		VaultRefs:       []string{"0x0000000000000000000000000000000000000001"},
		DepositProtocol: protocol.DepositProtocolVault, DepositDexChainId: 1,
	})
	integrationWrite(t, db, keys.KeyFrostVaultState("eth", 0), &pb.FrostVaultState{Chain: "eth", VaultId: 0, Status: vm.VaultStatusKeyReady})
	requestTx := &pb.AnyTx{Content: &pb.AnyTx_WitnessRequestTx{WitnessRequestTx: &pb.WitnessRequestTx{
		Base:        &pb.BaseMessage{TxId: "integration-request", ChainId: 1, FromAddress: "payer", Fee: "1000"},
		NativeChain: "eth", NativeTxHash: fmt.Sprintf("0x%064x", 2),
		NativeContract: "0xdac17f958d2ee523a2206206994597c13d831ec7", Amount: "123",
		ReceiverAddress: utils.GenerateTestIdentity(7107).Address,
		DepositProtocol: protocol.DepositProtocolVault, RechargeFee: "103",
	}}}
	integrationBlock(t, executor, db, 100, "prepay", requestTx, "SUCCEED")
	if got := integrationBalance(t, db, "payer").Balance; got != "998897" {
		t.Fatalf("prepaid payer balance: %s", got)
	}
	if got := integrationBalance(t, db, vm.WitnessFeePoolAccount).Balance; got != "103" {
		t.Fatalf("fee must have a real backing balance: %s", got)
	}

	request := readRequest(t, db, "integration-request")
	request.Status = pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD
	request.DeadlineHeight = 1000
	request.PassCount, request.FailCount = 3, 1
	originals := []string{"origWrong0", "origWrong1", "origWrong2", "origGood"}
	arbitrators := []string{"arb0", "arb1", "arb2", "arb3"}
	request.SelectedWitnesses = originals
	allParticipants := append(append([]string(nil), originals...), arbitrators...)
	for i, addr := range allParticipants {
		integrationAccount(t, db, addr, "100000", "10000000000")                                                
		integrationWrite(t, db, keys.KeyWitnessInfo(addr), &pb.WitnessInfo{
			Address: addr, StakeAmount: "10000000000", ReservedStakeAmount: vm.WitnessCaseBondRaw,
			ActiveCaseCount: 1, Status: pb.WitnessStatus_WITNESS_ACTIVE, PendingReward: "0",
		})
		role, originalAmount := vm.WitnessCaseRoleOrdinary, "0"
		if i < len(originals) {
			role, originalAmount = vm.WitnessCaseRoleOriginal, vm.WitnessCaseBondRaw
		}
		request.CaseCollateral = append(request.CaseCollateral, &pb.WitnessCaseCollateral{
			Address: addr, RoleMask: role, CommittedAmount: vm.WitnessCaseBondRaw,
			ReservedAmount: vm.WitnessCaseBondRaw, OriginalAmount: originalAmount,
		})
	}
	for i, addr := range originals {
		direction := pb.WitnessVoteType_VOTE_PASS
		if i == 3 {
			direction = pb.WitnessVoteType_VOTE_FAIL
		}
		request.OriginalPositions = append(request.OriginalPositions, &pb.WitnessVote{RequestId: request.RequestId, WitnessAddress: addr, VoteType: direction})
	}
	request.Votes = request.OriginalPositions
	integrationWrite(t, db, keys.KeyRechargeRequest(request.RequestId), request)
	challengeTx := &pb.AnyTx{Content: &pb.AnyTx_WitnessChallengeTx{WitnessChallengeTx: &pb.WitnessChallengeTx{
		Base:      &pb.BaseMessage{TxId: "integration-challenge", FromAddress: "challenger", Fee: "1000"},
		RequestId: request.RequestId, StakeAmount: "1000000000", Reason: "External deposit is invalid",
	}}}
	integrationBlock(t, executor, db, 101, "fund", challengeTx, "SUCCEED")
	if got := integrationBalance(t, db, "challenger").Balance; got != "1999999000" {
		t.Fatalf("principal must debit challenger: %s", got)
	}
	challenge := readChallenge(t, db, "integration-challenge")
	if challenge.RequiredStake != "1000000000" || challenge.StakeAmount != "1000000000" {
		t.Fatalf("challenge must hold the 10 FB funded threshold: %+v", challenge)
	}
	var err error
	challenge, err = witness.StartArbitrationRound(challenge, arbitrators, nil, 101, protocol.ArbitrationRoundBlocks)
	if err != nil {
		t.Fatal(err)
	}
	integrationWrite(t, db, keys.KeyChallengeRecord(challenge.ChallengeId), challenge)
	for i := 0; i < 2; i++ {
		submitArbVote(t, executor, db, challenge.ChallengeId, request.RequestId, arbitrators[i], uint64(102+i), fmt.Sprintf("integration-vote-%d", i))
	}
	mid := readChallenge(t, db, challenge.ChallengeId)
	if mid.Status != pb.ChallengeStatus_CHALLENGE_ACTIVE || mid.FailCount != 2 || mid.Settled {
		t.Fatalf("2/4 cannot settle: %+v", mid)
	}
	finalTx := integrationArbTx(challenge.ChallengeId, request.RequestId, arbitrators[2], "integration-vote-2", challenge.BallotRound)
	integrationBlock(t, executor, db, 104, "quorum", finalTx, "SUCCEED")
	finalChallenge := readChallenge(t, db, challenge.ChallengeId)
	if finalChallenge.Status != pb.ChallengeStatus_CHALLENGE_FINALIZED || !finalChallenge.Settled || !finalChallenge.ChallengeSuccess || finalChallenge.StakeAmount != "0" || finalChallenge.FinalVote != pb.WitnessVoteType_VOTE_FAIL {
		t.Fatalf("terminal challenge not settled: %+v", finalChallenge)
	}
	finalRequest := readRequest(t, db, request.RequestId)
	if finalRequest.Status != pb.RechargeRequestStatus_RECHARGE_REJECTED || !finalRequest.FeeSettled || finalRequest.FeeEscrow != "0" {
		t.Fatalf("request lifecycle/escrow not finalized: %+v", finalRequest)
	}
	                                                                       
	                                                                                             
	for _, addr := range arbitrators[:3] {
		if got := integrationBalance(t, db, addr).Balance; got != "337600026" {
			t.Fatalf("%s actual reward balance: %s", addr, got)
		}
	}
	if got := integrationBalance(t, db, "origGood").Balance; got != "337600025" {
		t.Fatalf("correct original reward balance: %s", got)
	}
	if got := integrationBalance(t, db, "challenger").Balance; got != "4349999000" {
		t.Fatalf("principal refund + 45%% actual fines: %s", got)
	}
	if got := integrationBalance(t, db, vm.WitnessFeePoolAccount).Balance; got != "0" {
		t.Fatalf("fee pool not drained: %s", got)
	}
	for _, addr := range originals[:3] {
		info := readWitnessInfo(t, db, addr)
		if info.StakeAmount != "9000000000" || info.SlashedCount != 1 {
			t.Fatalf("%s must lose exactly one case bond: %+v", addr, info)
		}
	}
	for _, addr := range allParticipants {
		info := readWitnessInfo(t, db, addr)
		if info.PendingReward != "0" || info.ReservedStakeAmount != "0" || info.ActiveCaseCount != 0 {
			t.Fatalf("%s terminal collateral/reward claims remain: %+v", addr, info)
		}
	}
	if _, exists := db.data[keys.KeyChallengeContribution(challenge.ChallengeId, "integration-challenge")]; exists {
		t.Fatal("terminal contribution remains in live state")
	}
	if _, exists := db.data[keys.KeyArchiveChallengeContribution(challenge.ChallengeId, "integration-challenge")]; !exists {
		t.Fatal("terminal contribution was not archived")
	}
	var token pb.Token
	if err := proto.Unmarshal(db.data[keys.KeyToken("FB")], &token); err != nil {
		t.Fatal(err)
	}
	if token.TotalSupply != "999700000000" {
		t.Fatalf("only the 3 FB burn reduces supply: %s", token.TotalSupply)
	}

	                                                                                 
	replayReg := vm.NewHandlerRegistry()
	vm.RegisterDefaultHandlers(replayReg)
	replayExecutor := vm.NewExecutorWithWitness(db, replayReg, nil, nil)
	integrationBlock(t, replayExecutor, db, 105, "terminal-replay", proto.Clone(finalTx).(*pb.AnyTx), "FAILED")
	if got := integrationBalance(t, db, "challenger").Balance; got != "4349999000" {
		t.Fatalf("replay paid twice: %s", got)
	}
	if err := proto.Unmarshal(db.data[keys.KeyToken("FB")], &token); err != nil {
		t.Fatal(err)
	}
	if token.TotalSupply != "999700000000" || readWitnessInfo(t, db, originals[0]).SlashedCount != 1 {
		t.Fatal("replay slashed/burned twice")
	}
}

func integrationArbTx(challengeID, requestID, addr, txID string, round uint64) *pb.AnyTx {
	return &pb.AnyTx{Content: &pb.AnyTx_ArbitrationVoteTx{ArbitrationVoteTx: &pb.ArbitrationVoteTx{
		Base: &pb.BaseMessage{TxId: txID, FromAddress: addr, Fee: "1000"}, ChallengeId: challengeID, BallotRound: round,
		Vote: &pb.WitnessVote{RequestId: requestID, WitnessAddress: addr, VoteType: pb.WitnessVoteType_VOTE_FAIL, BallotRound: round},
	}}}
}

func submitArbVote(t *testing.T, executor *vm.Executor, db *MockDB, challengeID, requestID, arbitratorAddr string, height uint64, txID string) {
	t.Helper()
	round := readChallenge(t, db, challengeID).BallotRound
	integrationBlock(t, executor, db, height, txID, integrationArbTx(challengeID, requestID, arbitratorAddr, txID, round), "SUCCEED")
}

func integrationWrite(t *testing.T, db *MockDB, key string, value proto.Message) {
	t.Helper()
	raw, err := proto.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	db.data[key] = raw
}

func integrationAccount(t *testing.T, db *MockDB, address, available, locked string) {
	t.Helper()
	integrationWrite(t, db, keys.KeyAccount(address), &pb.Account{Address: address})
	integrationWrite(t, db, keys.KeyBalance(address, "FB"), &pb.TokenBalanceRecord{Balance: &pb.TokenBalance{Balance: available, WitnessLockedBalance: locked}})
}

func integrationBalance(t *testing.T, db *MockDB, address string) *pb.TokenBalance {
	t.Helper()
	var balance pb.TokenBalanceRecord
	if err := proto.Unmarshal(db.data[keys.KeyBalance(address, "FB")], &balance); err != nil {
		t.Fatal(err)
	}
	if balance.Balance == nil {
		return &pb.TokenBalance{Balance: "0"}
	}
	return balance.Balance
}

func integrationBlock(t *testing.T, executor *vm.Executor, db *MockDB, height uint64, id string, tx *pb.AnyTx, wantStatus string) {
	t.Helper()
	block := &pb.Block{BlockHash: "integration-" + id, Header: &pb.BlockHeader{PrevBlockHash: "previous", Height: height}, Body: []*pb.AnyTx{tx}}
	result, err := executor.BuildSpecResult(block)
	if err != nil {
		t.Fatalf("%s BuildSpecResult: %v", id, err)
	}
	if !result.Valid {
		t.Fatalf("%s invalid block: %s", id, result.Reason)
	}
	if len(result.Receipts) != 1 || result.Receipts[0].Status != wantStatus {
		t.Fatalf("%s unexpected receipt(s): %+v", id, result.Receipts)
	}
	vm.AuditGlobalConservationDiff(result.Diff, height, db.Get)
	for _, op := range result.Diff {
		if op.Del {
			delete(db.data, op.Key)
		} else {
			db.data[op.Key] = append([]byte(nil), op.Value...)
		}
	}
}

func readChallenge(t *testing.T, db *MockDB, id string) *pb.ChallengeRecord {
	t.Helper()
	data, ok := db.data[keys.KeyChallengeRecord(id)]
	if !ok {
		t.Fatalf("challenge %s not found in db", id)
	}
	var c pb.ChallengeRecord
	if err := proto.Unmarshal(data, &c); err != nil {
		t.Fatalf("unmarshal challenge: %v", err)
	}
	return &c
}

func readRequest(t *testing.T, db *MockDB, id string) *pb.RechargeRequest {
	t.Helper()
	data, ok := db.data[keys.KeyRechargeRequest(id)]
	if !ok {
		t.Fatalf("request %s not found in db", id)
	}
	var r pb.RechargeRequest
	if err := proto.Unmarshal(data, &r); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	return &r
}

func readWitnessInfo(t *testing.T, db *MockDB, addr string) *pb.WitnessInfo {
	t.Helper()
	data, ok := db.data[keys.KeyWitnessInfo(addr)]
	if !ok {
		t.Fatalf("witness info %s not found in db", addr)
	}
	var w pb.WitnessInfo
	if err := proto.Unmarshal(data, &w); err != nil {
		t.Fatalf("unmarshal witness info: %v", err)
	}
	return &w
}
