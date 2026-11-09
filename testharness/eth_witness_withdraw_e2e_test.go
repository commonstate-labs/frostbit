package testharness

import (
	"fmt"
	"testing"

	"dex/config"
	bridgeasset "dex/frost/asset"
	"dex/keys"
	"dex/pb"
	"dex/protocol"
	"dex/utils"
	"dex/witness"

	"google.golang.org/protobuf/proto"
)

                                                                                       
  
                                                            
                                                                   
                                                                               
                                                                                                   
                                                                                            
                                                                                     
                                                                                                        
func TestETH_WitnessToWithdraw_E2E(t *testing.T) {
	const (
		chainName      = "eth"                               
		nativeChain    = "ETH"                      
		nativeContract = "0xdAC17F958D2ee523a2206206994597C13D831ec7"
		rechargeAmt    = "10000000"                                            
		withdrawAmt    = "10000000"
		requester      = "0xReq001"
		nativeTxHash   = "0x1111111111111111111111111111111111111111111111111111111111111111"
		rechargeFee    = "1000"
	)

	witnessCfg := witness.DefaultConfig()
	witnessCfg.ChallengePeriodBlocks = 20
	h := New(t, 5, WithWitnessConfig(*witnessCfg))
	receiver := utils.GenerateTestIdentity(9001).Address
	identity, err := bridgeasset.Resolve(nativeChain, nativeContract)
	if err != nil {
		t.Fatal(err)
	}
	tokenAddr := identity.ID

	                                       
	stake := config.DefaultWitnessConfig().MinStakeAmount
	for _, addr := range h.Committee {
		                                                           
		h.SeedAccount(addr, "FB", "10000000000")
		h.SubmitTxExpectSuccess(BuildWitnessStakeTx(addr, pb.OrderOp_ADD, stake))
	}

	                                                                                            
	seedETHVault(t, h, chainName, 0)

	                                                      
	h.SeedAccount(requester, "FB", "1000000")

	                                  
	reqTx := BuildWitnessRequestTx(requester, nativeChain, nativeTxHash, 0, nil, nativeContract, rechargeAmt, receiver, rechargeFee)
	reqTx.GetBase().ChainId = config.DefaultChainID
	reqTx.GetWitnessRequestTx().DepositProtocol = protocol.DepositProtocolVault
	requestID := reqTx.GetBase().TxId
	h.SubmitTxExpectSuccess(reqTx)

	                                                                                                                
	h.AdvanceHeight(9)

	                                                          
	stored := h.GetRechargeRequest(requestID)
	if stored == nil {
		t.Fatal("RechargeRequest not found after WitnessRequestTx")
	}
	if stored.Status != pb.RechargeRequestStatus_RECHARGE_VOTING {
		t.Fatalf("want RECHARGE_VOTING, got %v", stored.Status)
	}
	if len(stored.SelectedWitnesses) < 3 {
		t.Fatalf("expected >=3 selected witnesses, got %d", len(stored.SelectedWitnesses))
	}

	                                                              
	needPass := (uint32(len(stored.SelectedWitnesses))*60 + 99) / 100               
	for i, w := range stored.SelectedWitnesses {
		if uint32(i) >= needPass {
			break
		}
		                                                                                                
		raw, ok := h.RawGet(keys.KeyBalance(w, "FB"))
		var balance pb.TokenBalanceRecord
		if !ok || proto.Unmarshal(raw, &balance) != nil || balance.GetBalance().GetWitnessLockedBalance() != stake {
			t.Fatalf("witness %s must retain its real staked balance %s before voting", w, stake)
		}
		h.SubmitTxExpectSuccess(buildWitnessVoteTx(w, requestID, pb.WitnessVoteType_VOTE_PASS))
	}

	                                                                                    
	afterVote := h.GetRechargeRequest(requestID)
	if afterVote.Status != pb.RechargeRequestStatus_RECHARGE_CONSENSUS_PASS {
		t.Fatalf("want RECHARGE_CONSENSUS_PASS after quorum, got %v", afterVote.Status)
	}
	if afterVote.VoteAcceptUntilHeight != afterVote.DecisionHeight+1 {
		t.Fatalf("unexpected vote grace window: decision=%d acceptUntil=%d", afterVote.DecisionHeight, afterVote.VoteAcceptUntilHeight)
	}
	h.AdvanceHeight(1)
	afterGrace := h.GetRechargeRequest(requestID)
	if afterGrace.Status != pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD {
		t.Fatalf("want RECHARGE_CHALLENGE_PERIOD after vote grace, got %v", afterGrace.Status)
	}

	                                                                            
	h.AdvanceHeight(30)                                     

	final := h.GetRechargeRequest(requestID)
	if final.Status != pb.RechargeRequestStatus_RECHARGE_FINALIZED {
		t.Fatalf("want RECHARGE_FINALIZED after challenge period, got %v", final.Status)
	}
	                                    
	h.AssertBalance(receiver, tokenAddr, rechargeAmt)

	                                                                         
	wdReq := &pb.FrostWithdrawRequestTx{
		Base:   &pb.BaseMessage{TxId: "wd_req_eth_001", FromAddress: receiver, Fee: "1000"},
		Chain:  chainName,
		Asset:  tokenAddr,
		To:     "0xde709f2102306220921060314715629080e2fb77",
		Amount: withdrawAmt,
	}
	                             
	h.SeedAccount(receiver+"_fb_fund", "FB", "0")                                              
	h.RawSet(keys.KeyBalance(receiver, "FB"), mustMarshalFBBalance(t, "10000"))
	h.SubmitTxExpectSuccess(&pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawRequestTx{FrostWithdrawRequestTx: wdReq}})

	                
	h.AssertBalance(receiver, tokenAddr, "0")

	                                                             
	withdrawIDRaw, ok := h.RawGet(keys.KeyFrostWithdrawLiveIndex(chainName, 0, 1))
	if !ok {
		t.Fatal("mixed withdrawal live index missing")
	}
	wdState := h.GetWithdrawState(string(withdrawIDRaw))
	if wdState == nil || wdState.Status != string("QUEUED") {
		t.Fatalf("withdraw state want QUEUED, got %+v", wdState)
	}
	if wdState.Amount != withdrawAmt {
		t.Fatalf("withdraw amount want %s, got %s", withdrawAmt, wdState.Amount)
	}
	if wdState.GrossAmount != withdrawAmt || wdState.FeeAmount != "1000" || wdState.NetAmount != "9999000" || wdState.FeeVersion != 1 {
		t.Fatalf("withdraw deterministic fee fields mismatch: gross=%s fee=%s net=%s version=%d", wdState.GrossAmount, wdState.FeeAmount, wdState.NetAmount, wdState.FeeVersion)
	}
	if wdState.LiveSeq != 1 {
		t.Fatalf("mixed withdrawal live sequence want 1, got %d", wdState.LiveSeq)
	}
	if wdState.VaultId != 0 || wdState.WithdrawId != string(withdrawIDRaw) {
		t.Fatalf("mixed withdrawal allocation mismatch: vault=%d state=%q index=%q", wdState.VaultId, wdState.WithdrawId, string(withdrawIDRaw))
	}
	pending, ok := h.RawGet(keys.KeyFrostWithdrawLivePending(chainName, wdState.VaultId))
	if !ok || string(pending) != "1" {
		t.Fatalf("mixed withdrawal pending count want 1, found=%v value=%q", ok, string(pending))
	}

	                                                                              
	assertETHSignedRejectedWhenTemplateHashEmpty(t, h, chainName, tokenAddr, string(withdrawIDRaw))
}

                                                                                                               
func TestTRX_WithdrawSigned_RejectedByVM(t *testing.T) {
	h := New(t, 3)

	                                                         
	h.RawSet(keys.KeyFrostWithdraw("wid_trx_guard"), mustMarshalProto(t, &pb.FrostWithdrawState{
		WithdrawId: "wid_trx_guard",
		Seq:        1,
		Chain:      "trx",
		Asset:      "USDT",
		Amount:     "1",
		To:         "TRXaddr",
		Status:     "QUEUED",
	}))
	h.RawSet(keys.KeyFrostWithdrawFIFOIndex("trx", "USDT", 1), []byte("wid_trx_guard"))
	h.RawSet(keys.KeyFrostWithdrawFIFOSeq("trx", "USDT"), []byte("2"))

	signed := &pb.FrostWithdrawSignedTx{
		Base: &pb.BaseMessage{
			TxId:        "trx_signed_guard",
			FromAddress: h.Committee[0],
			Fee:         "1000",
		},
		JobId:              "job_trx_1",
		SignedPackageBytes: []byte("x"),
		WithdrawIds:        []string{"wid_trx_guard"},
		Chain:              "trx",
		Asset:              "USDT",
		TemplateHash:       []byte{0xaa},
	}
	             
	h.SeedAccount(h.Committee[0], "FB", "100000")

	spec, err := h.MineBlock(&pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: signed}})
	if err != nil {
		t.Fatalf("MineBlock: %v", err)
	}
	rc := findReceipt(spec, "trx_signed_guard")
	if rc == nil {
		t.Fatal("receipt missing for TRX signed tx")
	}
	if rc.Status != "FAILED" {
		t.Fatalf("TRX signed must FAIL, got status=%s err=%s", rc.Status, rc.Error)
	}
}

                                

                                                                                                                          
                                                                                  
func seedETHVault(t *testing.T, h *Harness, chainName string, vaultID uint32) {
	t.Helper()
	if vaultID != 0 {
		t.Fatalf("protocol supports only vaultID 0, got %d", vaultID)
	}
	cfg := localVaultConfig(chainName)
	cfg.SignAlgo = pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128
	cfg.VaultRefs = []string{"0x000000000000000000000000000000000000dead"}
	cfg.EvmChainId = 1
	cfg.DepositProtocol = protocol.DepositProtocolVault
	cfg.DepositDexChainId = config.DefaultChainID
	if err := config.ValidateOnChainVaultCommitteePolicy(cfg); err != nil {
		t.Fatalf("vault policy fixture: %v", err)
	}
	cfgData, err := proto.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal vault config: %v", err)
	}
	h.RawSet(keys.KeyFrostVaultConfig(chainName, 0), cfgData)

	vs := &pb.FrostVaultState{
		VaultId:                   vaultID,
		Chain:                     chainName,
		KeyEpoch:                  1,
		GroupPubkey:               make([]byte, 64),
		SignAlgo:                  pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128,
		Status:                    "ACTIVE",
		CommitteeMembers:          append([]string(nil), h.Committee[:4]...),
		CommitteeThreshold:        3,
		ActiveSinceHeight:         1,
		NextCommitteeReviewHeight: 1 + protocol.VaultCommitteeTermBlocks,
	}
	vsData, err := proto.Marshal(vs)
	if err != nil {
		t.Fatalf("marshal vault state: %v", err)
	}
	h.RawSet(keys.KeyFrostVaultState(chainName, vaultID), vsData)
}

func buildWitnessVoteTx(witnessAddr, requestID string, voteType pb.WitnessVoteType) *pb.AnyTx {
	return &pb.AnyTx{Content: &pb.AnyTx_WitnessVoteTx{WitnessVoteTx: &pb.WitnessVoteTx{
		Base: &pb.BaseMessage{
			TxId:        fmt.Sprintf("vote_%s_%s", witnessAddr, requestID[:min(8, len(requestID))]),
			FromAddress: witnessAddr,
			Fee:         "1000",
		},
		Vote: &pb.WitnessVote{
			RequestId:      requestID,
			WitnessAddress: witnessAddr,
			VoteType:       voteType,
		},
	}}}
}

func assertETHSignedRejectedWhenTemplateHashEmpty(t *testing.T, h *Harness, chainName, tokenAddr, withdrawID string) {
	t.Helper()
	                                      
	from := h.Committee[0]
	if bal := h.GetBalance(from, "FB"); bal == "0" {
		h.RawSet(keys.KeyBalance(from, "FB"), mustMarshalFBBalance(t, "100000"))
	}

	bad := &pb.FrostWithdrawSignedTx{
		Base: &pb.BaseMessage{
			TxId:        "eth_signed_guard_empty_hash",
			FromAddress: from,
			Fee:         "1000",
		},
		JobId:              "job_eth_guard",
		SignedPackageBytes: []byte("x"),
		WithdrawIds:        []string{withdrawID},
		Chain:              chainName,
		Asset:              tokenAddr,
		TemplateHash:       nil,             
	}
	spec, err := h.MineBlock(&pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: bad}})
	if err != nil {
		t.Fatalf("MineBlock: %v", err)
	}
	rc := findReceipt(spec, "eth_signed_guard_empty_hash")
	if rc == nil {
		t.Fatal("receipt missing for ETH empty-hash signed tx")
	}
	if rc.Status != "FAILED" {
		t.Fatalf("ETH empty template_hash must FAIL, got status=%s err=%s", rc.Status, rc.Error)
	}
}

func mustMarshalFBBalance(t *testing.T, amount string) []byte {
	t.Helper()
	rec := &pb.TokenBalanceRecord{Balance: &pb.TokenBalance{Balance: amount}}
	data, err := proto.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal balance: %v", err)
	}
	return data
}

func mustMarshalProto(t *testing.T, msg proto.Message) []byte {
	t.Helper()
	data, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal proto: %v", err)
	}
	return data
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
