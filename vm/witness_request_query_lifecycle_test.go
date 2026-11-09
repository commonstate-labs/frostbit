package vm

import (
	"fmt"
	"testing"

	"dex/keys"
	"dex/pb"
	"dex/protocol"
	"dex/utils"
	"dex/witness"
	"github.com/stretchr/testify/require"
)

                                                                                                        
                                                                                                
func TestWitnessRequestQueryIndexesAcrossRealTerminalPaths(t *testing.T) {
	for _, name := range []string{"unchallenged_pass", "original_fail", "challenge_success", "challenge_failure"} {
		t.Run(name, func(t *testing.T) {
			const id, createHeight = "query-lifecycle-request", uint64(10)
			cfg := witness.DefaultConfig()
			cfg.InitialWitnessCount, cfg.MinSelectedWitnessCount = 4, 4
			cfg.ChallengePeriodBlocks = 20
			cfg.OrdinaryCommitteeSizes, cfg.OrdinaryThresholdBps = []uint32{4, 8, 16}, 7500
			svc := witness.NewService(cfg)
			t.Cleanup(svc.Stop)
			x := &Executor{WitnessService: svc}
			sv := newMemStateView()
			seedChallengeSettlementFBToken(t, sv, "85000000000")
			for i := 0; i < 8; i++ {
				a := fmt.Sprintf("query-witness-%d", i)
				settlementPut(t, sv, keys.KeyWitnessInfo(a), &pb.WitnessInfo{Address: a, Status: pb.WitnessStatus_WITNESS_ACTIVE, StakeAmount: "10000000000"})
				SetBalance(sv, a, FeeToken, &pb.TokenBalance{Balance: "0", WitnessLockedBalance: "10000000000"})
			}
			SetBalance(sv, "query-payer", FeeToken, &pb.TokenBalance{Balance: "3000000000"})
			SetBalance(sv, "query-challenger", FeeToken, &pb.TokenBalance{Balance: "2000000000"})
			settlementPut(t, sv, keys.KeyAccount("query-challenger"), &pb.Account{Address: "query-challenger"})
			putTestVaultConfig(t, sv, "eth", 1)
			putTestVaultState(t, sv, "eth", 0, VaultStatusActive)
			receiver := utils.GenerateTestIdentity(99716).Address
			tx := &pb.AnyTx{Content: &pb.AnyTx_WitnessRequestTx{WitnessRequestTx: &pb.WitnessRequestTx{
				Base:        &pb.BaseMessage{TxId: id, FromAddress: "query-payer", ExecutedHeight: createHeight, ChainId: accountRechargeTestChainID},
				NativeChain: "ETH", NativeTxHash: accountRechargeTestHash(id), Amount: "12300",
				ReceiverAddress: receiver, RechargeFee: "1000000", DepositProtocol: protocol.DepositProtocolVault,
			}}}
			_, receipt, err := (&WitnessRequestTxHandler{witnessSvc: svc}).DryRun(tx, sv)
			require.NoError(t, err)
			require.Equal(t, "SUCCEED", receipt.Status, receipt.Error)
			assertIndexes := func(stage string, terminal bool) *pb.RechargeRequest {
				t.Helper()
				req := loadRechargeRequestAtKey(sv, keys.KeyRechargeRequest(id))
				require.NotNil(t, req)
				height := createHeight
				if terminal {
					require.NotZero(t, req.FinalizeHeight, stage)
					height = req.FinalizeHeight
				}
				want := []string{
					keys.KeyRechargeQueryRecentGlobal(terminal, height, id),
					keys.KeyRechargeQueryRecentChain("eth", terminal, height, id),
					keys.KeyRechargeQueryRecentVault(0, terminal, height, id),
					keys.KeyRechargeQueryRecentChainVault("eth", 0, terminal, height, id),
				}
				rows, err := sv.Scan(keys.KeyRechargeQueryRecentPrefix())
				require.NoError(t, err)
				require.Len(t, rows, 4, "%s: stale or missing lifecycle index: %v", stage, rows)
				for _, k := range want {
					require.Equal(t, []byte(keys.KeyRechargeRequest(id)), rows[k], "%s: %s", stage, k)
				}
				return req
			}
			req := assertIndexes("creation", false)
			height := rechargeSelectionReadyHeight(req, cfg.BeaconMixBlocks)
			for h := req.SelectionHeight; h <= height; h++ {
				sv.Set(keys.KeyBlockHashByHeight(h), []byte(fmt.Sprintf("query-beacon-%d", h)))
			}
			require.NoError(t, x.applyPendingRechargeSelections(sv, height))
			req = assertIndexes("original_selection", false)
			require.Len(t, req.SelectedWitnesses, 4)
			direction := pb.WitnessVoteType_VOTE_PASS
			if name == "original_fail" {
				direction = pb.WitnessVoteType_VOTE_FAIL
			}
			for _, a := range req.SelectedWitnesses {
				vote := &pb.AnyTx{Content: &pb.AnyTx_WitnessVoteTx{WitnessVoteTx: &pb.WitnessVoteTx{
					Base: &pb.BaseMessage{TxId: "query-original-" + a, FromAddress: a, ExecutedHeight: height},
					Vote: &pb.WitnessVote{RequestId: id, WitnessAddress: a, VoteType: direction, Round: req.Round},
				}}}
				ops, receipt, err := (&WitnessVoteTxHandler{witnessSvc: svc}).DryRun(vote, sv)
				require.NoError(t, err)
				require.Equal(t, "SUCCEED", receipt.Status)
				for _, op := range ops {
					require.NoError(t, applyWriteOpWithRechargeIndexes(sv, op, cfg.BeaconMixBlocks, cfg.ChallengePeriodBlocks, height))
				}
			}
			req = assertIndexes("original_votes", false)
			require.NoError(t, x.applyRechargeVoteGraceClosures(sv, req.VoteAcceptUntilHeight, cfg.BeaconMixBlocks))
			if name == "original_fail" {
				req = assertIndexes("rejected_original", true)
				require.Equal(t, pb.RechargeRequestStatus_RECHARGE_REJECTED, req.Status)
				return
			}
			req = assertIndexes("publicity", false)
			require.Equal(t, pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD, req.Status)
			if name == "unchallenged_pass" {
				require.NoError(t, x.applyRechargeChallengeFinalizations(sv, req.DeadlineHeight+1, cfg.BeaconMixBlocks))
				req = assertIndexes("credited_unchallenged", true)
				require.Equal(t, pb.RechargeRequestStatus_RECHARGE_FINALIZED, req.Status)
				return
			}
			                                                                                             
			for _, a := range req.SelectedWitnesses {
				var info pb.WitnessInfo
				acceptanceFundingRead(t, sv, keys.KeyWitnessInfo(a), &info)
				info.Status, info.UnstakeHeight = pb.WitnessStatus_WITNESS_UNSTAKING, height
				settlementPut(t, sv, keys.KeyWitnessInfo(a), &info)
			}
			fund := &pb.AnyTx{Content: &pb.AnyTx_WitnessChallengeTx{WitnessChallengeTx: &pb.WitnessChallengeTx{
				Base:      &pb.BaseMessage{TxId: "query-challenge", FromAddress: "query-challenger", ExecutedHeight: req.DeadlineHeight},
				RequestId: id, StakeAmount: "1000000000",
			}}}
			_, receipt, err = (&WitnessChallengeTxHandler{witnessSvc: svc}).DryRun(fund, sv)
			require.NoError(t, err)
			require.Equal(t, "SUCCEED", receipt.Status, receipt.Error)
			assertIndexes("challenge_funding", false)
			ch := loadChallengeRecordAtKey(sv, keys.KeyChallengeRecord("query-challenge"))
			height = challengeSelectionReadyHeight(ch, cfg.BeaconMixBlocks)
			require.NoError(t, x.applyPendingArbitrationSelections(sv, height))
			ch = loadChallengeRecordAtKey(sv, keys.KeyChallengeRecord("query-challenge"))
			require.Len(t, ch.Arbitrators, 4)
			assertIndexes("ordinary_selection", false)
			finalVote, finalStatus := pb.WitnessVoteType_VOTE_PASS, pb.RechargeRequestStatus_RECHARGE_FINALIZED
			if name == "challenge_success" {
				finalVote, finalStatus = pb.WitnessVoteType_VOTE_FAIL, pb.RechargeRequestStatus_RECHARGE_REJECTED
			}
			for _, a := range ch.Arbitrators[:3] {
				lifecycleBallot(t, sv, x, ch, a, finalVote, height, true)
			}
			req = assertIndexes("arbitration_final", true)
			require.Equal(t, finalStatus, req.Status)
		})
	}
}
