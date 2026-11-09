package keys

import (
	"strings"
	"testing"

	"dex/pb"
)

                                                                
                                                                        
                                                                 
  
                                                                       
                                                                      
                                                                            
func TestAllKeyBuildersUseRegisteredPrefix(t *testing.T) {
	t.Parallel()

	                                                             
	samples := map[string]string{
		                      
		"KeyBlockData":                KeyBlockData("hash"),
		"KeyHeightBlocks":             KeyHeightBlocks(1),
		"KeyBlockIDToHeight":          KeyBlockIDToHeight("hash"),
		"KeyLatestHeight":             KeyLatestHeight(),
		"KeyBlockReward":              KeyBlockReward(1),
		"KeyBlockRewardPrefix":        KeyBlockRewardPrefix(),
		"KeyBlockPruneCursor":         KeyBlockPruneCursor(),
		"KeyBlockArchiveCoverage":     KeyBlockArchiveCoverage(),
		"KeyCertifiedAnchorRecovery":  KeyCertifiedAnchorRecovery(),
		"KeyQueryPruneHeightCursor":   KeyQueryPruneHeightCursor(),
		"KeyQueryPruneSyncGap":        KeyQueryPruneSyncGap(1),
		"KeyQueryPruneManifestPrefix": KeyQueryPruneManifestPrefix(),
		"KeyQueryPruneManifestChunk":  KeyQueryPruneManifestChunk(1, 0),
		"KeyQueryPruneManifestDone":   KeyQueryPruneManifestDone(1),
		"KeyBlockHashByHeight":        KeyBlockHashByHeight(1),
		"KeyFrostArtifactPruneCursor": KeyFrostArtifactPruneCursor(),
		"KeyFrostArchiveComplete":     KeyFrostArchiveCompleteThrough(),
		"KeyFrostLateJoinSyncMarker":  KeyFrostLateJoinSyncMarker(),
		"KeyFrostGenesisManifest":     KeyFrostGenesisImportManifest(),
		"KeyFrostConfiguredGenesis":   KeyFrostConfiguredGenesisMarker(),
		"KeyProtocolVersion":          KeyProtocolVersion(),
		"KeyProtocolUpgradeReceipt":   KeyProtocolUpgradeReceipt(2),

		                            
		"KeyPendingAnyTx": KeyPendingAnyTx("tx1"),
		"KeyTxRaw":        KeyTxRaw("tx1"),

		                        
		"KeyAccount":          KeyAccount("addr"),
		"KeyAccountOrderItem": KeyAccountOrderItem("addr", "oid"),
		"KeyBalance":          KeyBalance("addr", "token"),
		"KeyAssetClaim":       KeyAssetClaim("addr", "FB", "future_margin", "pos1"),
		"KeyAssetClaimTotal":  KeyAssetClaimTotal("addr", "FB"),
		"KeyAssetClaimMarker": KeyAssetClaimMarker("addr", "FB"),
		"KeyRentOwnerMarker":  KeyRentOwnerMarker("addr", "init"),
		"KeyRentState":        KeyRentState("addr"),
		"KeyRentExpiry":       KeyRentExpiry(100, "addr"),
		"KeyIndexToAccount":   KeyIndexToAccount(1),
		"KeyFreeIdx":          KeyFreeIdx(),
		"KeyStakeIndex":       KeyStakeIndex("00000001", "addr"),

		                      
		"KeyOrderState":                   KeyOrderState("oid"),
		"KeyOrderExpiry":                  KeyOrderExpiry(100, "oid"),
		"KeyOrderExpiryPrefix":            KeyOrderExpiryPrefix(100),
		"KeyOrderPriceIndex":              KeyOrderPriceIndex("BTC_USD", pb.OrderSide_BUY, false, "0000001", "oid"),
		"KeyOrderPriceIndexPrefix":        KeyOrderPriceIndexPrefix("BTC_USD", pb.OrderSide_BUY, false),
		"KeyOrderPriceIndexGeneralPrefix": KeyOrderPriceIndexGeneralPrefix("BTC_USD", false),
		"KeySpotAccountOrder":             KeySpotAccountOrder("owner", "BTC_USD", pb.OrderStateStatus_ORDER_FILLED, 1, "oid"),
		"KeySpotAccountOrderRecent":       KeySpotAccountOrderRecent("owner", pb.OrderStateStatus_ORDER_FILLED, 1, "BTC_USD", "oid"),

		                      
		"KeyTradeRecord": KeyTradeRecord("BTC_USD", 1000, "tid"),

		                      
		"KeyToken":            KeyToken("tok"),
		"KeyTokenRegistry":    KeyTokenRegistry(),
		"KeyNativeTokenIndex": KeyNativeTokenIndex("ETH", "0xdac17"),
		"KeyFreeze":           KeyFreeze("addr", "tok"),

		                        
		"KeyTransferHistory":                KeyTransferHistory("tx1"),
		"KeyArchiveRechargeRequest":         KeyArchiveRechargeRequest("req1"),
		"KeyArchiveWitnessVote":             KeyArchiveWitnessVote("req1", "addr"),
		"KeyArchiveChallenge":               KeyArchiveChallengeRecord("cid"),
		"KeyArchiveChallengeContrib":        KeyArchiveChallengeContribution("cid", "tx1"),
		"KeyArchiveFrostWithdraw":           KeyArchiveFrostWithdraw("wid"),
		"KeyArchiveFrostWithdrawDone":       KeyArchiveFrostWithdrawDone("BTC", "native", 1, "wid"),
		"KeyArchiveFrostWithdrawJob":        KeyArchiveFrostWithdrawJob("job1"),
		"KeyArchiveFrostSignedPackage":      KeyArchiveFrostSignedPackage("job1", 0),
		"KeyArchiveFrostMigration":          KeyArchiveFrostMigration("migration1"),
		"KeyArchiveFrostArtifact":           KeyArchiveFrostArtifact("artifact1"),
		"KeyArchiveFrostArtifactHeight":     KeyArchiveFrostArtifactHeight(1, "artifact1"),
		"KeyArchiveFrostArtifactPrefix":     KeyArchiveFrostArtifactHeightPrefix(1),
		"KeyFrostWithdrawBatch":             KeyFrostWithdrawBatch("eth", 0, "1"),
		"KeyFrostPermanentArtifact":         KeyFrostPermanentArtifact("artifact1"),
		"KeyFrostPermanentWithdraw":         KeyFrostPermanentWithdraw("wid"),
		"KeyFrostPermanentWithdrawJob":      KeyFrostPermanentWithdrawJob("job1"),
		"KeyFrostPermanentSignedPackage":    KeyFrostPermanentSignedPackage("job1", 0),
		"KeyFrostPermanentMigration":        KeyFrostPermanentMigration("migration1"),
		"KeyFrostPermanentWithdrawArtifact": KeyFrostPermanentWithdrawArtifact("wid"),

		                         
		"KeyRechargeRecord":  KeyRechargeRecord("addr", "tweak"),
		"KeyRechargeAddress": KeyRechargeAddress("addr"),

		                      
		"KeyFrostFundsLotIndex":               KeyFrostFundsLotIndex("BTC", "native", 1, 100, 0),
		"KeyFrostFundsLotRemaining":           KeyFrostFundsLotRemaining("BTC", "native", 1, 100, 0),
		"KeyFrostFundsLotSeq":                 KeyFrostFundsLotSeq("BTC", "native", 1, 100),
		"KeyFrostFundsLotSeqCleanup":          KeyFrostFundsLotSeqCleanup(100, KeyFrostFundsLotSeq("BTC", "native", 1, 100)),
		"KeyFrostFundsLotSeqCleanupMarker":    KeyFrostFundsLotSeqCleanupMarker(100),
		"KeyFrostFundsLotSeqCleanupBackfill":  KeyFrostFundsLotSeqCleanupBackfillDone(),
		"KeyFrostFundsLotSeqCleanupCursor":    KeyFrostFundsLotSeqCleanupBackfillCursor(),
		"KeyFrostFundsLotHead":                KeyFrostFundsLotHead("BTC", "native", 1),
		"KeyFrostPlanningLog":                 KeyFrostPlanningLog("withdraw-1", "reporter-1"),
		"KeyFrostPlanningLogPruneCursor":      KeyFrostPlanningLogPruneCursor(),
		"KeyFrostVaultFundsLedger":            KeyFrostVaultFundsLedger("BTC", "native", 1),
		"KeyFrostVaultFundsLedgerAllPrefix":   KeyFrostVaultFundsLedgerAllPrefix(),
		"KeyFrostVaultFundsLedgerPrefix":      KeyFrostVaultFundsLedgerPrefix("BTC"),
		"KeyFrostVaultFundsLedgerAssetPrefix": KeyFrostVaultFundsLedgerAssetPrefix("BTC", "native"),
		"KeyFrostVaultAvailableBalance":       KeyFrostVaultAvailableBalance("BTC", "native", 1),
		"KeyFrostVaultReservedBalance":        KeyFrostVaultReservedBalance("BTC", "native", 1),
		"KeyFrostVaultAvailablePrefix":        KeyFrostVaultAvailablePrefix("BTC"),
		"KeyFrostBtcUtxo":                     KeyFrostBtcUtxo(1, "txid", 0),
		"KeyFrostBtcSpendableUtxoIndex":       KeyFrostBtcSpendableUtxoIndex(1, 1),
		"KeyFrostBtcUtxoSeqRef":               KeyFrostBtcUtxoSeqRef(1, "txid", 0),
		"KeyFrostBtcUtxoArtifact":             KeyFrostBtcUtxoArtifact(1, "txid", 0),
		"KeyFrostArtifactMeta":                KeyFrostArtifactMeta("artifact1"),
		"KeyFrostMigrationArtifact":           KeyFrostMigrationArtifact("migration1"),
		"KeyFrostMigrationPackageHash":        KeyFrostMigrationPackageHash("migration1"),
		"KeyFrostVaultHandoverProof":          KeyFrostVaultHandoverProof("eth", 0, 2),
		"KeyFrostMigrationStateRef":           KeyFrostMigrationStateRef("migration1"),
		"KeyFrostBtcLockedUtxo":               KeyFrostBtcLockedUtxo(1, "txid", 0),
		"KeyFrostBtcUtxoFIFOIndex":            KeyFrostBtcUtxoFIFOIndex(1, 0),
		"KeyFrostBtcUtxoFIFOSeq":              KeyFrostBtcUtxoFIFOSeq(1),
		"KeyFrostBtcUtxoFIFOHead":             KeyFrostBtcUtxoFIFOHead(1),
		"KeyFrostWithdraw":                    KeyFrostWithdraw("wid"),
		"KeyFrostWithdrawCompositePaid":       KeyFrostWithdrawCompositePaid("wid"),
		"KeyFrostWithdrawFIFOIndex":           KeyFrostWithdrawFIFOIndex("BTC", "native", 0),
		"KeyFrostWithdrawFIFOSeq":             KeyFrostWithdrawFIFOSeq("BTC", "native"),
		"KeyFrostWithdrawFIFOHead":            KeyFrostWithdrawFIFOHead("BTC", "native"),
		"KeyFrostWithdrawTxRef":               KeyFrostWithdrawTxRef("txid"),
		"KeyFrostWithdrawQueryGlobal":         KeyFrostWithdrawQueryGlobal(1, 1, "wid"),
		"KeyFrostWithdrawQueryGlobalPrefix":   KeyFrostWithdrawQueryGlobalPrefix(),
		"KeyFrostWithdrawQueryChain":          KeyFrostWithdrawQueryChain("BTC", 1, 1, "wid"),
		"KeyFrostWithdrawQueryChainPrefix":    KeyFrostWithdrawQueryChainPrefix("BTC"),
		"KeyFrostWithdrawQueryChainVault":     KeyFrostWithdrawQueryChainVault("BTC", 1, 1, 1, "wid"),
		"KeyFrostWithdrawQueryVaultPrefix":    KeyFrostWithdrawQueryChainVaultPrefix("BTC", 1),
		"KeyFrostLocalShare":                  KeyFrostLocalShare("btc", 1, 1),
		"KeyFrostPendingLocalShare":           KeyFrostPendingLocalShare("btc", 1, 1, "s1"),
		"KeyFrostPendingLocalSharePrefix":     KeyFrostPendingLocalSharePrefix(),
		"KeyFrostLocalDkgReveal":              KeyFrostLocalDkgReveal("btc", 1, 1, "s1", 0),
		"KeyFrostLocalDkgRevealSessionPrefix": KeyFrostLocalDkgRevealSessionPrefix("btc", 1, 1, "s1"),
		"KeyFrostLocalDkgSession":             KeyFrostLocalDkgSession("btc", 1, 1, "s1"),
		"KeyFrostLocalDkgSessionPrefix":       KeyFrostLocalDkgSessionPrefix(),
		"KeyFrostLocalShareWrapKey":           KeyFrostLocalShareWrapKey(),
		"KeyFrostLocalShareWrapExternal":      KeyFrostLocalShareWrapExternalMarker(),
		"KeyFrostLocalNonce":                  KeyFrostLocalNonce("fingerprint"),
		"KeyFrostLocalRoastSequence":          KeyFrostLocalRoastSequence("route-fingerprint"),
		"KeyFrostConfig":                      KeyFrostConfig(),
		"KeyFrostActiveMiners":                KeyFrostActiveMiners(),
		"KeyFrostVaultConfig":                 KeyFrostVaultConfig("BTC", 1),
		"KeyFrostVaultState":                  KeyFrostVaultState("BTC", 1),
		"KeyFrostVaultTransition":             KeyFrostVaultTransition("BTC", 1, 1),
		"KeyFrostVaultTransitionActive":       KeyFrostVaultTransitionActive("BTC", 1, 1),
		"KeyFrostVaultDkgCommit":              KeyFrostVaultDkgCommit("BTC", 1, 1, "s1", "p1"),
		"KeyFrostVaultDkgShare":               KeyFrostVaultDkgShare("BTC", 1, 1, "s1", "d1", "r1"),
		"KeyFrostVaultDkgComplaint":           KeyFrostVaultDkgComplaint("BTC", 1, 1, "s1", "d1", "r1"),
		"KeyFrostVaultDkgValidationAck":       KeyFrostVaultDkgValidationAck("BTC", 1, 1, "s1", "p1"),
		"KeyFrostSignedPackage":               KeyFrostSignedPackage("job1", 0),

		                           
		"KeyMinerExitRecord": KeyMinerExitRecord("addr"),

		                   
		"KeyVMAppliedTx":    KeyVMAppliedTx("tx1"),
		"KeyVMTxError":      KeyVMTxError("tx1"),
		"KeyVMTxHeight":     KeyVMTxHeight("tx1"),
		"KeyVMTxFee":        KeyVMTxFee("tx1"),
		"KeyVMCommitHeight": KeyVMCommitHeight(1),
		"KeyVMTxFBBalance":  KeyVMTxFBBalance("tx1"),

		                     
		"KeyNode":       KeyNode(),
		"KeyClientInfo": KeyClientInfo("127.0.0.1"),

		                        
		"KeyWitnessInfo":                    KeyWitnessInfo("addr"),
		"KeyRechargeRequest":                KeyRechargeRequest("req1"),
		"KeyRechargeQueryRecentGlobal":      KeyRechargeQueryRecentGlobal(false, 1, "req1"),
		"KeyRechargeQueryRecentChain":       KeyRechargeQueryRecentChain("BTC", false, 1, "req1"),
		"KeyRechargeQueryRecentVault":       KeyRechargeQueryRecentVault(1, false, 1, "req1"),
		"KeyRechargeQueryChainVault":        KeyRechargeQueryRecentChainVault("BTC", 1, false, 1, "req1"),
		"KeyRechargeRequestByNativeTx":      KeyRechargeRequestByNativeTx("BTC", "txhash"),
		"KeyRechargeEvent":                  KeyRechargeEvent("ETH", "txhash", 1),
		"KeyRechargeEventPending":           KeyRechargeEventPending("ETH", "txhash", 1),
		"KeyRechargeEventPendingPrefix":     KeyRechargeEventPendingPrefix(),
		"KeyRechargePendingMigration":       KeyRechargePendingMigration(),
		"KeyRuntimeFeeEscrow":               KeyRuntimeFeeEscrow("tx1"),
		"KeyRuntimeFeeRelease":              KeyRuntimeFeeRelease(100, "tx1"),
		"KeyRuntimeFeeReleasePrefix":        KeyRuntimeFeeReleasePrefix(100),
		"KeyRechargeSelectionDue":           KeyRechargeSelectionDue(100, "req1"),
		"KeyRechargeVoteGraceDue":           KeyRechargeVoteGraceDue(100, "req1"),
		"KeyRechargeChallengeDue":           KeyRechargeChallengeDue(100, "req1"),
		"KeyChallengeRecord":                KeyChallengeRecord("cid"),
		"KeyChallengeSelectionDue":          KeyChallengeSelectionDue(100, "cid"),
		"KeyChallengeTimeoutDue":            KeyChallengeTimeoutDue(100, "cid"),
		"KeyChallengeTimeoutBackfill":       KeyChallengeTimeoutIndexBackfillDone(),
		"KeyWitnessVote":                    KeyWitnessVote("req1", "addr"),
		"KeyArbitrationVote":                KeyArbitrationVote("cid", "addr"),
		"KeyArbitratorChallenge":            KeyArbitratorChallenge("addr", "cid"),
		"KeyWitnessStakeIndex":              KeyWitnessStakeIndex("00000001", "addr"),
		"KeyWitnessConfig":                  KeyWitnessConfig(),
		"KeyShelvedRequest":                 KeyShelvedRequest("req1"),
		"KeyRechargeCredited":               KeyRechargeCredited("req1"),
		"KeyWitnessAssigned":                KeyWitnessAssigned("addr", "req1"),
		"KeyWitnessAssignedCurrent":         KeyWitnessAssignedCurrent("addr", "req1"),
		"KeyWitnessAssignedCurrentPrefix":   KeyWitnessAssignedCurrentPrefix("addr"),
		"KeyLegacyWitnessAssigned":          KeyLegacyWitnessAssigned("addr", "req1"),
		"KeyLegacyWitnessAssignedPrefix":    KeyLegacyWitnessAssignedPrefix(),
		"KeyWitnessAssignmentQueryMigrated": KeyWitnessAssignmentQueryMigrated(),

		                    
		"KeyJMTRoot": KeyJMTRoot(),

		                                   
		"KeyFutureOrderPriceIndex":       KeyFutureOrderPriceIndex("BTC_USD", pb.OrderSide_BUY, false, "0000001", "oid"),
		"KeyFutureOrderState":            KeyFutureOrderState("oid"),
		"KeyFutureAccountOrder":          KeyFutureAccountOrder("owner", "BTC_USD", pb.FutureOrderStatus_FUTURE_ORDER_FILLED, 1, "oid"),
		"KeyFutureAccountOrderRecent":    KeyFutureAccountOrderRecent("owner", pb.FutureOrderStatus_FUTURE_ORDER_FILLED, 1, "BTC_USD", "oid"),
		"KeyFuturePositionHistory":       KeyFuturePositionHistory("pid"),
		"KeyFuturePositionHistoryOwner":  KeyFuturePositionHistoryOwnerIndex("owner", 2, 1, "pid"),
		"KeyFuturePositionHistoryPair":   KeyFuturePositionHistoryPairIndex("BTC_USD", 2, 1, "pid"),
		"KeyFuturePositionHistoryRecent": KeyFuturePositionHistoryRecentIndex(2, 1, "pid"),
		"KeyFuturePosition":              KeyFuturePosition("pid"),
		"KeyFuturePositionIDSequence":    KeyFuturePositionIDSequence("alice", "BTC_USD", 1),
		"KeyFutureOwnerPosition":         KeyFutureOwnerPosition("owner", "pid"),
		"KeyFuturePairPosition":          KeyFuturePairPosition("BTC_USD", "pid"),
		"KeyFutureInsuranceFund":         KeyFutureInsuranceFund("BTC_USD"),
		"KeyFutureInsuranceShortfall":    KeyFutureInsuranceShortfall("BTC_USD"),
		"KeyFutureMarketConfig":          KeyFutureMarketConfig("BTC_USD"),
		"KeyFutureCumulativeFunding":     KeyFutureCumulativeFunding("BTC_USD"),
		"KeyFutureLiqIndex":              KeyFutureLiqIndex("long", "BTC_USD", "0000001", "pid"),
		"KeyFutureLiqPosRef":             KeyFutureLiqPosRef("pid"),
		"KeyFutureLiqIndexBackfillDone":  KeyFutureLiqIndexBackfillDone("BTC_USD"),
		"KeyFutureMarkPrice":             KeyFutureMarkPrice("BTC_USD"),
		"KeyFutureSpotEMA":               KeyFutureSpotEMA("BTC_USD"),
		"KeyFutureEMA":                   KeyFutureEMA("BTC_USD"),
		"KeySpotLastPrice":               KeySpotLastPrice("BTC_USD"),
		"KeyFutureLastPrice":             KeyFutureLastPrice("BTC_USD"),
		"KeyFutureFundingRate":           KeyFutureFundingRate("BTC_USD"),
		"KeyFutureFundingHeight":         KeyFutureFundingHeight("BTC_USD"),
		"KeyFutureFundingDue":            KeyFutureFundingDue(100, "BTC_USD"),
		"KeyFutureOrderExpiry":           KeyFutureOrderExpiry(100, "oid"),
		"KeyFutureOrderReservedMargin":   KeyFutureOrderReservedMargin("oid"),
		"KeyFutureSettleMarkPrice":       KeyFutureSettleMarkPrice("BTC_USD"),
		"KeyFutureTradeRecord":           KeyFutureTradeRecord("BTC_USD", 1000, "tid"),

		                         
		"KeyLivenessMiner":   KeyLivenessMiner(0),
		"KeyLivenessCounter": KeyLivenessCounter(0),
		"KeyLivenessReport":  KeyLivenessReport(1),
		"KeyLivenessGlobal":  KeyLivenessGlobal(),

		                         
		"KeyReferrer":             KeyReferrer("addr"),
		"KeyReferralInvitee":      KeyReferralInvitee("referrer", "invitee"),
		"KeyReferrerPrefix":       KeyReferrerPrefix(),
		"KeyTradingFeeRecord":     KeyTradingFeeRecord("addr", 1, "tx", 0),
		"KeyReferralRebateRecord": KeyReferralRebateRecord("referrer", 1, "tx", 0),

		                                  
		"KeyTotalMinerStake": KeyTotalMinerStake(),

		                          
		"KeyCertSignature":         KeyCertSignature(1, 0),
		"KeyCertMinerSet":          KeyCertMinerSet(1),
		"KeyCertCertificate":       KeyCertCertificate(1),
		"KeyCertGlobal":            KeyCertGlobal(),
		"KeyCertPending":           KeyCertPending(),
		"KeyCertPendingCheckpoint": KeyCertPendingCheckpoint(1),

		                                                
		"KeyUserActivity":             KeyUserActivity("addr", 1, 0),
		"KeyUserActivityPrefix":       KeyUserActivityPrefix("addr"),
		"KeyUserActivityHeightPrefix": KeyUserActivityHeightPrefix("addr", 1),
		"KeyUserActivityKindIndex":    KeyUserActivityKindIndex("addr", "kind", 1, "tx1", 0),
		"KeyUserActivityKindPrefix":   KeyUserActivityKindIndexPrefix("addr", "kind"),
	}

	for name, key := range samples {
		matched := false
		for _, r := range prefixRegistry {
			if strings.HasPrefix(key, r.full) {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("%s produced key %q which matches no registered prefix — "+
				"register it via regState/regKV/regExcluded in keys.go", name, key)
		}
	}
}

func TestFuturePositionIDSequenceDoesNotOverlapPositionFamily(t *testing.T) {
	sequenceKey := KeyFuturePositionIDSequence("alice", "BTC_USDT", 1)
	if strings.HasPrefix(sequenceKey, KeyFuturePositionPrefix()) {
		t.Fatalf("position ID sequence key %q overlaps protobuf position family %q", sequenceKey, KeyFuturePositionPrefix())
	}
}

                                                                            
                                                                          
                                                             
func TestStatefulKeyCategorizationConsistency(t *testing.T) {
	t.Parallel()

	type sample struct {
		name string
		key  string
	}

	wantStateful := []sample{
		{"KeyProtocolVersion", KeyProtocolVersion()},
		{"KeyProtocolUpgradeReceipt", KeyProtocolUpgradeReceipt(2)},
		{"KeyAccount", KeyAccount("addr")},
		{"KeyBalance", KeyBalance("addr", "tok")},
		{"KeyAssetClaim", KeyAssetClaim("addr", "FB", "future_margin", "pos1")},
		{"KeyAssetClaimTotal", KeyAssetClaimTotal("addr", "FB")},
		{"KeyAssetClaimMarker", KeyAssetClaimMarker("addr", "FB")},
		{"KeyRentOwnerMarker", KeyRentOwnerMarker("addr", "init")},
		{"KeyRentState", KeyRentState("addr")},
		{"KeyRentExpiry", KeyRentExpiry(100, "addr")},
		{"KeyAccOrder", KeyAccountOrderItem("addr", "oid")},
		{"KeyOrderState", KeyOrderState("oid")},
		{"KeyOrderExpiry", KeyOrderExpiry(100, "oid")},
		{"KeyOrderPriceIndex", KeyOrderPriceIndex("BTC_USD", pb.OrderSide_BUY, false, "0000001", "oid")},
		{"KeySpotLastPrice", KeySpotLastPrice("BTC_USD")},
		{"KeyToken", KeyToken("tok")},
		{"KeyTokenRegistry", KeyTokenRegistry()},
		{"KeyNativeTokenIndex", KeyNativeTokenIndex("ETH", "0xdac17")},
		{"KeyFreeze", KeyFreeze("addr", "tok")},
		{"KeyFrostConfig", KeyFrostConfig()},
		{"KeyFrostConfiguredGenesis", KeyFrostConfiguredGenesisMarker()},
		{"KeyFrostVaultState", KeyFrostVaultState("BTC", 1)},
		{"KeyFrostWithdraw", KeyFrostWithdraw("wid")},
		{"KeyFrostSignedPackage", KeyFrostSignedPackage("job1", 0)},
		{"KeyRechargeRecord", KeyRechargeRecord("addr", "tweak")},
		{"KeyRechargeRequest", KeyRechargeRequest("req1")},
		{"KeyRechargeNative", KeyRechargeRequestByNativeTx("BTC", "txhash")},
		{"KeyRechargeEvent", KeyRechargeEvent("ETH", "txhash", 1)},
		{"KeyRechargeEventPending", KeyRechargeEventPending("ETH", "txhash", 1)},
		{"KeyRechargePendingMigration", KeyRechargePendingMigration()},
		{"KeyRuntimeFeeEscrow", KeyRuntimeFeeEscrow("tx1")},
		{"KeyRuntimeFeeRelease", KeyRuntimeFeeRelease(100, "tx1")},
		{"KeyRechargeCredited", KeyRechargeCredited("req1")},
		{"KeyRechargeSelectionDue", KeyRechargeSelectionDue(100, "req1")},
		{"KeyRechargeVoteGraceDue", KeyRechargeVoteGraceDue(100, "req1")},
		{"KeyRechargeChallengeDue", KeyRechargeChallengeDue(100, "req1")},
		{"KeyWitnessInfo", KeyWitnessInfo("addr")},
		{"KeyWitnessVote", KeyWitnessVote("req1", "addr")},
		{"KeyWitnessStakeIndex", KeyWitnessStakeIndex("00000001", "addr")},
		{"KeyWitnessConfig", KeyWitnessConfig()},
		{"KeyChallenge", KeyChallengeRecord("cid")},
		{"KeyChallengeSelectionDue", KeyChallengeSelectionDue(100, "cid")},
		{"KeyChallengeTimeoutDue", KeyChallengeTimeoutDue(100, "cid")},
		{"KeyChallengeTimeoutBackfill", KeyChallengeTimeoutIndexBackfillDone()},
		{"KeyShelvedRequest", KeyShelvedRequest("req1")},
		{"KeyArbitrationVote", KeyArbitrationVote("cid", "addr")},
		{"KeyArbitratorChallenge", KeyArbitratorChallenge("addr", "cid")},
		{"KeyLegacyWitnessAssigned", KeyLegacyWitnessAssigned("addr", "req1")},
		{"KeyWitnessAssignmentQueryMigrated", KeyWitnessAssignmentQueryMigrated()},
		{"KeyFutureOrderState", KeyFutureOrderState("oid")},
		{"KeyFuturePosition", KeyFuturePosition("pid")},
		{"KeyFutureMarkPrice", KeyFutureMarkPrice("BTC_USD")},
		{"KeyFutureFundingDue", KeyFutureFundingDue(100, "BTC_USD")},
		{"KeyFutureOrderExpiry", KeyFutureOrderExpiry(100, "oid")},
		{"KeyFutureOrderReservedMargin", KeyFutureOrderReservedMargin("oid")},
		{"KeyLivenessMiner", KeyLivenessMiner(0)},
		{"KeyLivenessGlobal", KeyLivenessGlobal()},
		{"KeyMinerExitRecord", KeyMinerExitRecord("addr")},
		{"KeyTotalMinerStake", KeyTotalMinerStake()},
	}

	wantKV := []sample{
		{"KeyBlockData", KeyBlockData("hash")},
		{"KeyBlockArchiveCoverage", KeyBlockArchiveCoverage()},
		{"KeyCertifiedAnchorRecovery", KeyCertifiedAnchorRecovery()},
		{"KeyQueryPruneHeightCursor", KeyQueryPruneHeightCursor()},
		{"KeyQueryPruneSyncGap", KeyQueryPruneSyncGap(1)},
		{"KeyQueryPruneManifestPrefix", KeyQueryPruneManifestPrefix()},
		{"KeyQueryPruneManifestChunk", KeyQueryPruneManifestChunk(1, 0)},
		{"KeyTxRaw", KeyTxRaw("tx1")},
		{"KeyPendingAnyTx", KeyPendingAnyTx("tx1")},
		{"KeyVMAppliedTx", KeyVMAppliedTx("tx1")},
		{"KeyVMTxFee", KeyVMTxFee("tx1")},
		{"KeyVMCommitHeight", KeyVMCommitHeight(1)},
		{"KeyTransferHistory", KeyTransferHistory("tx1")},
		{"KeyArchiveRechargeRequest", KeyArchiveRechargeRequest("req1")},
		{"KeyArchiveWitnessVote", KeyArchiveWitnessVote("req1", "addr")},
		{"KeyArchiveChallenge", KeyArchiveChallengeRecord("cid")},
		{"KeyArchiveChallengeContrib", KeyArchiveChallengeContribution("cid", "tx1")},
		{"KeyArchiveFrostWithdraw", KeyArchiveFrostWithdraw("wid")},
		{"KeyArchiveFrostWithdrawDone", KeyArchiveFrostWithdrawDone("BTC", "native", 1, "wid")},
		{"KeyArchiveFrostWithdrawJob", KeyArchiveFrostWithdrawJob("job1")},
		{"KeyArchiveFrostSignedPackage", KeyArchiveFrostSignedPackage("job1", 0)},
		{"KeyFrostLocalShare", KeyFrostLocalShare("btc", 1, 1)},
		{"KeyFrostLocalNonce", KeyFrostLocalNonce("fingerprint")},
		{"KeyFrostLocalRoastSequence", KeyFrostLocalRoastSequence("route-fingerprint")},
		{"KeyFrostLateJoinSyncMarker", KeyFrostLateJoinSyncMarker()},
		{"KeyFrostGenesisManifest", KeyFrostGenesisImportManifest()},
		{"KeyCertGlobal", KeyCertGlobal()},
		{"KeyUserActivity", KeyUserActivity("addr", 1, 0)},
		{"KeyUserActivityKindIndex", KeyUserActivityKindIndex("addr", "kind", 1, "tx1", 0)},
		{"KeySpotAccountOrder", KeySpotAccountOrder("owner", "BTC_USD", pb.OrderStateStatus_ORDER_FILLED, 1, "oid")},
		{"KeySpotAccountOrderRecent", KeySpotAccountOrderRecent("owner", pb.OrderStateStatus_ORDER_FILLED, 1, "BTC_USD", "oid")},
		{"KeyFutureAccountOrder", KeyFutureAccountOrder("owner", "BTC_USD", pb.FutureOrderStatus_FUTURE_ORDER_FILLED, 1, "oid")},
		{"KeyFutureAccountOrderRecent", KeyFutureAccountOrderRecent("owner", pb.FutureOrderStatus_FUTURE_ORDER_FILLED, 1, "BTC_USD", "oid")},
		{"KeyFuturePositionHistory", KeyFuturePositionHistory("pid")},
		{"KeyFuturePositionHistoryOwner", KeyFuturePositionHistoryOwnerIndex("owner", 2, 1, "pid")},
		{"KeyRechargeQueryRecent", KeyRechargeQueryRecentGlobal(false, 1, "req1")},
		{"KeyWitnessAssigned", KeyWitnessAssigned("addr", "req1")},
		{"KeyFrostWithdrawQuery", KeyFrostWithdrawQueryGlobal(1, 1, "wid")},
		{"KeyFutureTradeRecord", KeyFutureTradeRecord("BTC_USD", 1000, "tid")},
	}

	for _, s := range wantStateful {
		if !IsStatefulKey(s.key) {
			t.Errorf("%s → %q: expected stateful (StateDB), got KV", s.name, s.key)
		}
	}
	for _, s := range wantKV {
		if IsStatefulKey(s.key) {
			t.Errorf("%s → %q: expected KV-only, got stateful", s.name, s.key)
		}
	}
}

                                                                        
                                                                
func TestRegistryHasNoDuplicates(t *testing.T) {
	t.Parallel()

	seen := make(map[string]bool, len(prefixRegistry))
	for _, r := range prefixRegistry {
		if seen[r.full] {
			t.Errorf("duplicate registry entry for prefix %q", r.full)
		}
		seen[r.full] = true
	}
}

func TestBoundedQueryIndexesUseExpectedStorageAndRetention(t *testing.T) {
	terminalRecent := KeyFutureAccountOrderRecent("alice", pb.FutureOrderStatus_FUTURE_ORDER_FILLED, 10, "BTC_USDT", "order-1")
	openRecent := KeyFutureAccountOrderRecent("alice", pb.FutureOrderStatus_FUTURE_ORDER_OPEN, 10, "BTC_USDT", "order-2")
	positionRecent := KeyFuturePositionHistoryOwnerIndex("alice", 2, 20, "position-1")
	rechargeActive := KeyRechargeQueryRecentGlobal(false, 30, "request-active")
	rechargeTerminal := KeyRechargeQueryRecentGlobal(true, 40, "request-terminal")
	frostWithdraw := KeyFrostWithdrawQueryGlobal(50, 1, "withdraw-1")
	userActivityKind := KeyUserActivityKindIndex("alice", "FUNDING_SETTLE", 60, "tx1", 0)
	for _, key := range []string{terminalRecent, openRecent, positionRecent, rechargeActive, rechargeTerminal, frostWithdraw, userActivityKind} {
		if got := StorageClassForKey(key); got != StorageQuery {
			t.Fatalf("key %q storage=%s, want query", key, got)
		}
	}
	if !IsPrunableQueryHistoryKey(terminalRecent) || !IsPrunableQueryHistoryKey(positionRecent) {
		t.Fatal("terminal query indexes must join height retention manifests")
	}
	if IsPrunableQueryHistoryKey(openRecent) {
		t.Fatal("open order companion index must remain until its status transition deletes it")
	}
	if IsPrunableQueryHistoryKey(rechargeActive) || !IsPrunableQueryHistoryKey(rechargeTerminal) {
		t.Fatal("only terminal recharge list indexes should join retention manifests")
	}
	if !IsPrunableQueryHistoryKey(frostWithdraw) {
		t.Fatal("frost withdraw query indexes must join height retention manifests")
	}
	if !IsPrunableQueryHistoryKey(userActivityKind) {
		t.Fatal("user activity kind indexes must share canonical history retention")
	}
	if !IsStatefulKey(KeyArbitratorChallenge("arb", "challenge")) {
		t.Fatal("active arbitrator assignment must survive checkpoint/state sync")
	}
}
