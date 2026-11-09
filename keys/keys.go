               
                                                             
package keys

import (
	"crypto/sha256"
	"dex/pb"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

                                                           
                                                                          
                                                                                           
const KeyVersion = "v1"

                                                                            
func withVer(s string) string {
	if KeyVersion == "" {
		return s
	}
	return KeyVersion + "_" + s
}

                                                                                                 
                                                               
func StripVersion(prefixed string) string {
	if KeyVersion == "" {
		return prefixed
	}
	p := KeyVersion + "_"
	return strings.TrimPrefix(prefixed, p)
}

                                                           
  
                                                                       

var (
	pfxBlockData          = regKV("blockdata_", "Block data")
	pfxHeight             = regKV("height_", "Height to block mapping")
	pfxBlockID            = regKV("blockid_", "Block hash to height")
	pfxLatestHeight       = regKV("latest_block_height", "Latest block height")
	pfxBlockReward        = regKV("block_reward_", "Block reward records")
	pfxBlockPruneCursor   = regKV("block_prune_cursor", "Local block archive prune cursor")
	pfxBlockArchiveCover  = regExcluded("block_archive_coverage", "Local contiguous block archive coverage marker")
	pfxCertifiedAnchorRec = regExcluded("certified_anchor_recovery", "Authenticated checkpoint anchor crash-recovery record")
	pfxQueryPruneHeight   = regKV("query_prune_height_cursor", "Local query history prune height cursor")
	pfxQueryPruneSyncGap  = regKV("query_prune_sync_gap_", "Query prune gap created by checkpoint sync or missing-manifest recovery")
	pfxQueryPruneManifest = regKV("query_prune_manifest_h_", "Height-indexed query prune manifest")
	pfxBlockReadModel     = regKV("block_read_model_", "Finalized block read-model sidecar")
	pfxFrostArtifactPrune = regExcluded("frost_artifact_prune_cursor", "Local FROST artifact prune cursor")
	pfxFrostArchiveCover  = regExcluded("frost_archive_complete_through", "Local FROST archive coverage marker")
	pfxBlockHashByHeight  = regState("block_hash_at_", "Block hash indexed by height (forward beacon source)")
	pfxProtocolVersion    = regState("protocol_version", "Activated state-transition protocol version")
	pfxProtocolUpgrade    = regState("protocol_upgrade_", "Applied binary-governed protocol migration receipt")
)

                            
                                 
func KeyBlockData(blockHash string) string {
	return pfxBlockData + blockHash
}

                                           
                                  
func KeyHeightBlocks(height uint64) string {
	return fmt.Sprintf("%s%d_blocks", pfxHeight, height)
}

                                                    
                               
func KeyBlockIDToHeight(blockHash string) string {
	return pfxBlockID + blockHash
}

                                     
                               
func KeyLatestHeight() string {
	return pfxLatestHeight
}

func KeyFrostArtifactPruneCursor() string { return pfxFrostArtifactPrune }

func KeyFrostArchiveCompleteThrough() string { return pfxFrostArchiveCover }

                                          
                                 
func KeyBlockReward(height uint64) string {
	return fmt.Sprintf("%s%020d", pfxBlockReward, height)
}

func KeyBlockRewardPrefix() string {
	return pfxBlockReward
}

                                                                           
                                                               
func KeyProtocolVersion() string { return pfxProtocolVersion }

                                                                            
                                                                      
func KeyProtocolUpgradeReceipt(version uint64) string {
	return fmt.Sprintf("%s%020d", pfxProtocolUpgrade, version)
}

func KeyBlockPruneCursor() string {
	return pfxBlockPruneCursor
}

func KeyBlockArchiveCoverage() string {
	return pfxBlockArchiveCover
}

                                                                            
                                                                             
                                                                           
func KeyCertifiedAnchorRecovery() string {
	return pfxCertifiedAnchorRec
}

func KeyQueryPruneHeightCursor() string {
	return pfxQueryPruneHeight
}

func KeyQueryPruneSyncGap(startHeight uint64) string {
	return fmt.Sprintf("%s%020d", pfxQueryPruneSyncGap, startHeight)
}

func KeyQueryPruneManifestPrefix() string {
	return pfxQueryPruneManifest
}

func KeyQueryPruneManifestHeightPrefix(height uint64) string {
	return fmt.Sprintf("%s%020d_", pfxQueryPruneManifest, height)
}

func KeyQueryPruneManifestChunkPrefix(height uint64) string {
	return KeyQueryPruneManifestHeightPrefix(height) + "chunk_"
}

func KeyQueryPruneManifestChunk(height uint64, chunk uint32) string {
	return fmt.Sprintf("%s%08d", KeyQueryPruneManifestChunkPrefix(height), chunk)
}

func KeyQueryPruneManifestDone(height uint64) string {
	return KeyQueryPruneManifestHeightPrefix(height) + "done"
}

                                                                             
                                                                             
                                                              
func KeyBlockReadModel(blockID string) string {
	return pfxBlockReadModel + blockID
}

func KeyBlockReadModelPrefix() string {
	return pfxBlockReadModel
}

                                                                           
                                  
                                                                                                                                  
func KeyBlockHashByHeight(height uint64) string {
	return fmt.Sprintf("%s%020d", pfxBlockHashByHeight, height)
}

                                                           
  
                                                                                                      

var (
	pfxPendingAnyTx = regKV("pending_anytx_", "Pending transactions")
	pfxTxRaw        = regKV("txraw_", "Transaction raw data (immutable)")
)

                                 
                                
func KeyPendingAnyTx(txID string) string {
	return pfxPendingAnyTx + txID
}

                                       
                                                                           
                        
func KeyTxRaw(txID string) string {
	return pfxTxRaw + txID
}

                                                           

var (
	pfxAccount         = regState("account_", "Account state")
	pfxAccOrder        = regState("acc_order_", "Account-order relation")
	pfxBalance         = regState("balance_", "Per-token account balances")
	pfxAssetClaim      = regState("asset_claim_", "Owner asset attribution index")
	pfxAssetClaimTotal = regState("asset_claim_total_", "Owner token asset attribution aggregate")
	pfxAssetClaimMark  = regState("asset_claim_marker_", "Owner asset attribution marker")
	pfxRentOwnerMark   = regState("rent_owner_marker_", "Owner rent sweep fast-path marker")
	pfxRentState       = regState("rent_state_", "Account rent state")
	pfxRentExpiry      = regState("rent_expiry_", "Account rent expiry index")
	pfxIndexToAccount  = regKV("indexToAccount_", "Index to account mapping")
	pfxFreeIdx         = regKV("free_idx_", "Free index")
	pfxStakeIndex      = regKV("stakeIndex_", "Stake index")
)

                          
                             
func KeyAccount(addr string) string {
	return pfxAccount + addr
}

                                                                          
                                         
func KeyAccountOrderItem(addr string, orderID string) string {
	return fmt.Sprintf("%s%s_%s", pfxAccOrder, addr, orderID)
}

                                                     
                                
func KeyAccountOrdersPrefix(addr string) string {
	return fmt.Sprintf("%s%s_", pfxAccOrder, addr)
}

                                                        
                                     
func KeyBalance(addr, token string) string {
	return fmt.Sprintf("%s%s_%s", pfxBalance, addr, token)
}

                                                                             
                              
func KeyBalancePrefix(addr string) string {
	return fmt.Sprintf("%s%s_", pfxBalance, addr)
}

                                                                             
                                                                               
func KeyAssetClaim(owner, token, source, sourceID string) string {
	return fmt.Sprintf("%s%s_%s_%s_%s", pfxAssetClaim, owner, token, source, sourceID)
}

func KeyAssetClaimPrefix(owner, token string) string {
	return fmt.Sprintf("%s%s_%s_", pfxAssetClaim, owner, token)
}

func KeyAssetClaimOwnerPrefix(owner string) string {
	return fmt.Sprintf("%s%s_", pfxAssetClaim, owner)
}

func KeyAssetClaimTotal(owner, token string) string {
	return fmt.Sprintf("%s%s_%s", pfxAssetClaimTotal, owner, token)
}

func KeyAssetClaimTotalOwnerPrefix(owner string) string {
	return fmt.Sprintf("%s%s_", pfxAssetClaimTotal, owner)
}

func KeyAssetClaimMarker(owner, token string) string {
	return fmt.Sprintf("%s%s_%s", pfxAssetClaimMark, owner, token)
}

func KeyAssetClaimMarkerOwnerPrefix(owner string) string {
	return fmt.Sprintf("%s%s_", pfxAssetClaimMark, owner)
}

func KeyRentOwnerMarker(owner, kind string) string {
	return fmt.Sprintf("%s%s_%s", pfxRentOwnerMark, owner, kind)
}

func KeyRentOwnerMarkerPrefix(owner string) string {
	return fmt.Sprintf("%s%s_", pfxRentOwnerMark, owner)
}

func KeyRentState(owner string) string {
	return pfxRentState + owner
}

func KeyRentExpiry(height uint64, owner string) string {
	return fmt.Sprintf("%s%020d_%s", pfxRentExpiry, height, owner)
}

func KeyRentExpiryPrefix(height uint64) string {
	return fmt.Sprintf("%s%020d_", pfxRentExpiry, height)
}

                                                                              
                                                                             
                                                                            
                 
func KeyRentExpiryAllPrefix() string {
	return pfxRentExpiry
}

                                             
                                
func KeyIndexToAccount(idx uint64) string {
	return fmt.Sprintf("%s%d", pfxIndexToAccount, idx)
}

                                                   
func NameOfKeyIndexToAccount() string {
	return pfxIndexToAccount
}

                                
                     
func KeyFreeIdx() string {
	return pfxFreeIdx
}

                             
                                                        
func KeyStakeIndex(invertedPadded32, addr string) string {
	return fmt.Sprintf("%s%s_address:%s", pfxStakeIndex, invertedPadded32, addr)
}

                                                           

var (
	pfxOrderState       = regState("orderstate_", "Mutable order execution state")
	pfxOrderExpiry      = regState("order_expiry_", "Spot no-fill order expiry index")
	pfxPair             = regState("pair:", "Spot orderbook price index")
	pfxSpotMarket       = regState("spot_market_cfg_", "Spot market semantic base/quote identity")
	pfxSpotAccountOrder = regExcluded("spot_acc_order_", "Spot account order history/index (KV-only)")
)

                                         
                                               
                                
func KeyOrderState(orderID string) string {
	return pfxOrderState + orderID
}

                                         
                       
func KeyOrderStatePrefix() string {
	return pfxOrderState
}

                                                      
                                                       
func KeyOrderExpiry(height uint64, orderID string) string {
	return fmt.Sprintf("%s%020d_%s", pfxOrderExpiry, height, orderID)
}

                                                                                 
func KeyOrderExpiryPrefix(height uint64) string {
	return fmt.Sprintf("%s%020d_", pfxOrderExpiry, height)
}

                                        
                                                                                                 
func KeyOrderPriceIndex(pair string, side pb.OrderSide, isFilled bool, priceKey67 string, orderID string) string {
	return fmt.Sprintf("%s%s|side:%d|is_filled:%t|price:%s|order_id:%s", pfxPair, pair, side, isFilled, priceKey67, orderID)
}

                                                                                                          
                                                           
func KeyOrderPriceIndexPrefix(pair string, side pb.OrderSide, isFilled bool) string {
	return fmt.Sprintf("%s%s|side:%d|is_filled:%t|", pfxPair, pair, side, isFilled)
}

                                                                                                                 
func KeyOrderPriceIndexGeneralPrefix(pair string, isFilled bool) string {
	return fmt.Sprintf("%s%s|", pfxPair, pair)
}

                                                                   
func KeyOrderBookPrefix() string {
	return pfxPair
}

                                                                             
                                                                          
                       
func KeySpotMarketConfig(pair string) string {
	return pfxSpotMarket + pair
}

func KeySpotMarketConfigPrefix() string {
	return pfxSpotMarket
}

func OrderStatusKey(status pb.OrderStateStatus) string {
	switch status {
	case pb.OrderStateStatus_ORDER_OPEN:
		return "open"
	case pb.OrderStateStatus_ORDER_FILLED:
		return "filled"
	case pb.OrderStateStatus_ORDER_CANCELLED:
		return "cancelled"
	default:
		return "unknown"
	}
}

func SpotAccountOrderStatusKeys() []string {
	return []string{"open", "filled", "cancelled", "unknown"}
}

func KeySpotAccountOrder(owner, pair string, status pb.OrderStateStatus, createHeight uint64, orderID string) string {
	return fmt.Sprintf("%sowner:%s|pair:%s|status:%s|height:%020d|order_id:%s", pfxSpotAccountOrder, owner, pair, OrderStatusKey(status), ^createHeight, orderID)
}

func KeySpotAccountOrderFromState(st *pb.OrderState) string {
	if st == nil || st.Owner == "" || st.BaseToken == "" || st.QuoteToken == "" || st.OrderId == "" {
		return ""
	}
	return KeySpotAccountOrder(st.Owner, st.BaseToken+"_"+st.QuoteToken, st.Status, st.CreateHeight, st.OrderId)
}

func KeySpotAccountOrderPrefix(owner, pair, status string) string {
	return fmt.Sprintf("%sowner:%s|pair:%s|status:%s|", pfxSpotAccountOrder, owner, pair, status)
}

func KeySpotAccountOrderOwnerPrefix(owner string) string {
	return fmt.Sprintf("%sowner:%s|", pfxSpotAccountOrder, owner)
}

func KeySpotAccountOrderFamilyPrefix() string {
	return pfxSpotAccountOrder
}

                                                                             
                                                                               
                                                                              
                                                               
func KeySpotAccountOrderRecent(owner string, status pb.OrderStateStatus, createHeight uint64, pair, orderID string) string {
	return fmt.Sprintf("%srecent|owner:%s|status:%s|height:%020d|pair:%s|order_id:%s", pfxSpotAccountOrder, owner, OrderStatusKey(status), ^createHeight, pair, orderID)
}

func KeySpotAccountOrderRecentFromState(st *pb.OrderState) string {
	if st == nil || st.Owner == "" || st.BaseToken == "" || st.QuoteToken == "" || st.OrderId == "" {
		return ""
	}
	return KeySpotAccountOrderRecent(st.Owner, st.Status, st.CreateHeight, st.BaseToken+"_"+st.QuoteToken, st.OrderId)
}

func KeySpotAccountOrderRecentPrefix(owner, status string) string {
	return fmt.Sprintf("%srecent|owner:%s|status:%s|", pfxSpotAccountOrder, owner, status)
}

                                                                 

var pfxTrade = regKV("trade_", "Trade records")

                              
                                              
func KeyTradeRecord(pair string, timestamp int64, tradeID string) string {
	                                                               
	invertedTimestamp := ^uint64(timestamp)
	return fmt.Sprintf("%s%s_%020d_%s", pfxTrade, pair, invertedTimestamp, tradeID)
}

                                                            
                         
func KeyTradeRecordPrefix(pair string) string {
	return fmt.Sprintf("%s%s_", pfxTrade, pair)
}

                                                           

var (
	pfxToken       = regState("token_", "Token metadata and registry")
	pfxFreeze      = regState("freeze_", "Freeze mark state")
	pfxNativeToken = regState("native_token_", "External (chain,contract) -> DEX token_address index")
)

                        
                                
func KeyToken(tokenAddress string) string {
	return pfxToken + tokenAddress
}

                                                                     
                  
func KeyTokenPrefix() string {
	return pfxToken
}

                                  
                          
func KeyTokenRegistry() string {
	return pfxToken + "registry"
}

                                                                                       
                                                                                             
                                                                                                        
func KeyNativeTokenIndex(chain, contract string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(chain) + "\x00" + strings.TrimSpace(contract)))
	return pfxNativeToken + hex.EncodeToString(digest[:])
}

                         
                                           
func KeyFreeze(address, tokenAddress string) string {
	return fmt.Sprintf("%s%s_%s", pfxFreeze, address, tokenAddress)
}

                                                                 
  
                                               

var pfxTransferHistory = regKV("transfer_history_", "Transfer history")

                                  
                                   
func KeyTransferHistory(txID string) string {
	return pfxTransferHistory + txID
}

                                                              
  
                                                                             
                                                                         
                                                                   
var (
	pfxArchiveRechargeRequest   = regKV("archive_recharge_request_", "Archived completed recharge request")
	pfxArchiveWitnessVote       = regKV("archive_witness_vote_", "Archived witness vote")
	pfxArchiveChallenge         = regKV("archive_challenge_", "Archived finalized challenge record")
	pfxArchiveChallengeContrib  = regKV("archive_challengecontrib_", "Archived finalized challenge contribution")
	pfxArchiveFrostWithdraw     = regKV("archive_frost_withdraw_", "Archived FROST withdraw state")
	pfxArchiveFrostWithdrawDone = regKV("archive_frost_done_withdraw_", "Archived FROST terminal withdraw index")
	pfxArchiveFrostJob          = regKV("archive_frost_job_", "Archived FROST withdraw job")
	pfxArchiveFrostSignedPkg    = regKV("archive_frost_signed_pkg_", "Archived FROST signed package")
	pfxArchiveFrostMigration    = regKV("archive_frost_migration_", "Archived FROST vault migration package")
	pfxArchiveFrostArtifact     = regKV("archive_frost_artifact_", "Content-addressed FROST external transaction artifact")
	pfxArchiveFrostArtifactAt   = regKV("archive_frost_artifact_h_", "Height index for FROST external transaction artifacts")
	pfxFrostWithdrawArtifact    = regKV("archive_frost_withdrawref_", "Archived FROST withdraw ID to external artifact index")
)

func KeyArchiveRechargeRequest(requestID string) string {
	return pfxArchiveRechargeRequest + requestID
}

func KeyArchiveWitnessVote(requestID, witnessAddress string) string {
	return fmt.Sprintf("%s%s_%s", pfxArchiveWitnessVote, requestID, witnessAddress)
}

func KeyArchiveChallengeRecord(challengeID string) string {
	return pfxArchiveChallenge + challengeID
}

func KeyArchiveChallengeContribution(challengeID, txID string) string {
	return fmt.Sprintf("%s%s_%s", pfxArchiveChallengeContrib, challengeID, txID)
}

func KeyArchiveFrostWithdraw(withdrawID string) string {
	return pfxArchiveFrostWithdraw + withdrawID
}

func KeyArchiveFrostWithdrawDone(chain, asset string, seq uint64, withdrawID string) string {
	return fmt.Sprintf("%s%s_%s_%s_%s", pfxArchiveFrostWithdrawDone, chain, asset, padUint(seq), withdrawID)
}

func KeyArchiveFrostWithdrawDonePrefix(chain, asset string) string {
	if chain != "" && asset != "" {
		return fmt.Sprintf("%s%s_%s_", pfxArchiveFrostWithdrawDone, chain, asset)
	}
	if chain != "" {
		return fmt.Sprintf("%s%s_", pfxArchiveFrostWithdrawDone, chain)
	}
	return pfxArchiveFrostWithdrawDone
}

func KeyArchiveFrostWithdrawJob(jobID string) string {
	return pfxArchiveFrostJob + jobID
}

func KeyArchiveFrostSignedPackage(jobID string, idx uint64) string {
	return fmt.Sprintf("%s%s_%s", pfxArchiveFrostSignedPkg, jobID, padUint(idx))
}

func KeyArchiveFrostMigration(migrationID string) string {
	return pfxArchiveFrostMigration + migrationID
}

func KeyArchiveFrostArtifact(artifactID string) string {
	return pfxArchiveFrostArtifact + artifactID
}

func KeyArchiveFrostArtifactHeight(height uint64, artifactID string) string {
	return fmt.Sprintf("%s%s_%s", pfxArchiveFrostArtifactAt, padUint(height), artifactID)
}

func KeyArchiveFrostArtifactHeightPrefix(height uint64) string {
	return fmt.Sprintf("%s%s_", pfxArchiveFrostArtifactAt, padUint(height))
}

                                                                             
                                                                                
                                                                                
var (
	pfxFrostPermanentBatch       = regState("frost_permanent_batch_", "Permanent physical-Vault batch sequence to withdrawal delivery index")
	pfxFrostPermanentArtifact    = regState("frost_permanent_artifact_", "Permanent signed external transaction and direct dependencies")
	pfxFrostPermanentWithdraw    = regState("frost_permanent_withdraw_", "Permanent signed withdrawal delivery parameters")
	pfxFrostPermanentJob         = regState("frost_permanent_job_", "Permanent signed withdrawal job and canonical template")
	pfxFrostPermanentPackage     = regState("frost_permanent_package_", "Permanent accepted withdrawal signature package")
	pfxFrostPermanentMigration   = regState("frost_permanent_migration_", "Permanent signed migration delivery parameters and authorization")
	pfxFrostPermanentWithdrawRef = regState("frost_permanent_withdrawref_", "Permanent withdrawal to signed artifact reference")
)

func KeyFrostPermanentArtifact(artifactID string) string {
	return pfxFrostPermanentArtifact + artifactID
}
func KeyFrostPermanentWithdraw(withdrawID string) string {
	return pfxFrostPermanentWithdraw + withdrawID
}
func KeyFrostPermanentWithdrawJob(jobID string) string { return pfxFrostPermanentJob + jobID }
func KeyFrostPermanentSignedPackage(jobID string, idx uint64) string {
	return fmt.Sprintf("%s%s_%s", pfxFrostPermanentPackage, jobID, padUint(idx))
}
func KeyFrostPermanentMigration(migrationID string) string {
	return pfxFrostPermanentMigration + migrationID
}
func KeyFrostPermanentWithdrawArtifact(withdrawID string) string {
	return pfxFrostPermanentWithdrawRef + withdrawID
}

                                                           

var (
	pfxRechargeRecord  = regState("recharge_record_", "Recharge anti-replay record")
	pfxRechargeAddress = regKV("recharge_address_", "Recharge address mapping")
)

                                 
                                             
func KeyRechargeRecord(address, tweak string) string {
	return fmt.Sprintf("%s%s_%s", pfxRechargeRecord, address, tweak)
}

                                        
                                               
func KeyRechargeAddress(generatedAddress string) string {
	return pfxRechargeAddress + generatedAddress
}

                                                           
                                                                                                                
  
                                                                          
                                                                                   
                                                              

var (
	pfxFrost                = regState("frost_", "FROST module mutable state")
	pfxLocalFrostShare      = regKV("local_frost_share_", "Local FROST share (non-consensus, AES-GCM encrypted)")
	pfxLocalFrostPending    = regKV("local_frost_pending_share_", "Pending session-scoped FROST share (non-consensus, AES-GCM encrypted)")
	pfxLocalFrostDkgReveal  = regKV("local_frost_dkg_reveal_", "Local FROST DKG reveal (non-consensus, AES-GCM encrypted)")
	pfxLocalFrostDkgSession = regKV("local_frost_dkg_session_", "Local FROST DKG session recovery marker (non-consensus)")
	pfxLocalFrostWrapKey    = regKV("local_frost_wrap_key", "Local FROST share wrap key (random, node-local, non-consensus)")
	pfxLocalFrostWrapExt    = regKV("local_frost_wrap_external", "External FROST wrap-key marker and fingerprint (node-local, non-consensus)")
	pfxLocalFrostNonce      = regKV("local_frost_nonce_", "Burned FROST nonce fingerprint (node-local, non-consensus)")
	pfxLocalFrostRoastSeq   = regKV("local_frost_roast_seq_", "Durable outbound FROST ROAST sequence (node-local, non-consensus)")
	pfxLocalFrostLateJoin   = regKV("local_frost_latejoin_sync", "Durable authenticated late-join target (node-local, non-consensus)")
	pfxLocalFrostGenesis    = regKV("local_frost_genesis_manifest", "Signed standalone-DKG genesis manifest imported by this node (node-local, non-consensus)")
	pfxFrostPlanningLog     = regExcluded("frost_planning_log_", "Local debug log (excluded from StateDB)")
	pfxFrostPlanningPrune   = regKV("local_frost_planning_log_prune_cursor", "Local planning-log pressure-prune cursor")
	pfxFrostWithdrawQuery   = regExcluded("frost_withdraw_query_", "FROST withdraw newest-first query indexes (KV-only)")
)

func KeyFrostPlanningLog(withdrawID, reporter string) string {
	return fmt.Sprintf("%s%s_%s", pfxFrostPlanningLog, withdrawID, reporter)
}

func KeyFrostPlanningLogPrefix(withdrawID string) string {
	if withdrawID == "" {
		return pfxFrostPlanningLog
	}
	return pfxFrostPlanningLog + withdrawID + "_"
}

func KeyFrostPlanningLogPruneCursor() string {
	return pfxFrostPlanningPrune
}

                                                                                                                                     
                                
func KeyFrostLocalShareWrapKey() string {
	return pfxLocalFrostWrapKey
}

                                                                             
                                                                              
                                                  
func KeyFrostLocalShareWrapExternalMarker() string {
	return pfxLocalFrostWrapExt
}

                                                                      
                                                                             
func KeyFrostLocalNonce(nonceFingerprint string) string {
	return pfxLocalFrostNonce + nonceFingerprint
}

                                                                        
                                                                             
                                                                           
                             
func KeyFrostLocalRoastSequence(routeFingerprint string) string {
	return pfxLocalFrostRoastSeq + routeFingerprint
}

                                                                           
                                                                            
                                                        
func KeyFrostLateJoinSyncMarker() string {
	return pfxLocalFrostLateJoin
}

                                                                          
                                                                               
                                                                                
                                                                          
                                              
func KeyFrostGenesisImportManifest() string {
	return pfxLocalFrostGenesis
}

                                                                              
                                                                            
                                                                              
                                                                           
func KeyFrostConfiguredGenesisMarker() string {
	return pfxFrost + "configured_genesis"
}

                                                                                    
                                                                     
func KeyFrostFundsLotIndex(chain, asset string, vaultID uint32, height, seq uint64) string {
	return fmt.Sprintf("%sfunds_lot_%s_%s_%d_%s_%s", pfxFrost, chain, asset, vaultID, padUint(height), padUint(seq))
}

                                                                
func KeyFrostFundsLotPrefix(chain, asset string, vaultID uint32) string {
	return fmt.Sprintf("%sfunds_lot_%s_%s_%d_", pfxFrost, chain, asset, vaultID)
}

                                                                                  
                                                                    
func KeyFrostFundsLotRemaining(chain, asset string, vaultID uint32, height, seq uint64) string {
	return fmt.Sprintf("%sfunds_lot_remaining_%s_%s_%d_%s_%s", pfxFrost, chain, asset, vaultID, padUint(height), padUint(seq))
}

                                                                          
                                                                   
func KeyFrostFundsLotSeq(chain, asset string, vaultID uint32, height uint64) string {
	return fmt.Sprintf("%sfunds_lot_seq_%s_%s_%d_%s", pfxFrost, chain, asset, vaultID, padUint(height))
}

                                                                           
                                                                             
                                                                                
func KeyFrostFundsLotSeqPrefix() string {
	return pfxFrost + "funds_lot_seq_"
}

func KeyFrostFundsLotSeqCleanupMarker(height uint64) string {
	return fmt.Sprintf("%sfunds_lot_seq_cleanup_%s", pfxFrost, padUint(height))
}

func KeyFrostFundsLotSeqCleanupPrefix(height uint64) string {
	return KeyFrostFundsLotSeqCleanupMarker(height) + "_"
}

func KeyFrostFundsLotSeqCleanup(height uint64, sequenceKey string) string {
	return KeyFrostFundsLotSeqCleanupPrefix(height) + hex.EncodeToString([]byte(sequenceKey))
}

func KeyFrostFundsLotSeqCleanupBackfillDone() string {
	return pfxFrost + "funds_lot_seq_cleanup_backfill_done"
}

func KeyFrostFundsLotSeqCleanupBackfillCursor() string {
	return pfxFrost + "funds_lot_seq_cleanup_backfill_cursor"
}

                                                       
                                                           
func KeyFrostFundsLotHead(chain, asset string, vaultID uint32) string {
	return fmt.Sprintf("%sfunds_lot_head_%s_%s_%d", pfxFrost, chain, asset, vaultID)
}

                                                             
                                                         
func KeyFrostVaultFundsLedger(chain, asset string, vaultID uint32) string {
	return fmt.Sprintf("%sfunds_ledger_%s_%s_%d", pfxFrost, chain, asset, vaultID)
}

                                                                              
                          
func KeyFrostVaultFundsLedgerAllPrefix() string {
	return pfxFrost + "funds_ledger_"
}

                                                                              
func KeyFrostVaultFundsLedgerPrefix(chain string) string {
	return fmt.Sprintf("%sfunds_ledger_%s_", pfxFrost, chain)
}

                                                                              
                                     
func KeyFrostVaultFundsLedgerAssetPrefix(chain, asset string) string {
	return fmt.Sprintf("%sfunds_ledger_%s_%s_", pfxFrost, chain, asset)
}

                                                            
                                                            
func KeyFrostVaultAvailableBalance(chain, asset string, vaultID uint32) string {
	return fmt.Sprintf("%svault_available_%s_%s_%d", pfxFrost, chain, asset, vaultID)
}

                                                                                                        
                                                           
func KeyFrostVaultReservedBalance(chain, asset string, vaultID uint32) string {
	return fmt.Sprintf("%svault_reserved_%s_%s_%d", pfxFrost, chain, asset, vaultID)
}

                                                                  
func KeyFrostVaultAvailableAllPrefix() string {
	return pfxFrost + "vault_available_"
}

                                                                        
                                          
func KeyFrostVaultAvailablePrefix(chain string) string {
	return fmt.Sprintf("%svault_available_%s_", pfxFrost, chain)
}

                                                                                   
                                                   
func KeyFrostBtcUtxo(vaultID uint32, txid string, vout uint32) string {
	return fmt.Sprintf("%sbtc_utxo_%d_%s_%d", pfxFrost, vaultID, txid, vout)
}

                                                               
func KeyFrostBtcUtxoPrefix(vaultID uint32) string {
	return fmt.Sprintf("%sbtc_utxo_%d_", pfxFrost, vaultID)
}

                                                               
                                                     
func KeyFrostBtcSpendableUtxoIndex(vaultID uint32, seq uint64) string {
	return fmt.Sprintf("%sbtc_spendable_utxo_%d_%s", pfxFrost, vaultID, padUint(seq))
}

func KeyFrostBtcSpendableUtxoIndexPrefix(vaultID uint32) string {
	return fmt.Sprintf("%sbtc_spendable_utxo_%d_", pfxFrost, vaultID)
}

                                                                                                         
                                                           
func KeyFrostBtcUtxoSeqRef(vaultID uint32, txid string, vout uint32) string {
	return fmt.Sprintf("%sbtc_utxo_seq_ref_%d_%s_%d", pfxFrost, vaultID, txid, vout)
}

                                                                             
                                                                            
func KeyFrostBtcUtxoArtifact(vaultID uint32, txid string, vout uint32) string {
	return fmt.Sprintf("%sbtc_utxo_artifact_%d_%s_%d", pfxFrost, vaultID, txid, vout)
}

                                                                            
                                                                     
func KeyFrostArtifactMeta(artifactID string) string {
	return pfxFrost + "artifact_meta_" + artifactID
}

                                                                           
                                                                            
                                                                               
func KeyFrostVaultLastArtifact(chain string, vaultID uint32) string {
	return fmt.Sprintf("%svault_last_artifact_%s_%d", pfxFrost, strings.ToLower(strings.TrimSpace(chain)), vaultID)
}

                                                                          
                                                                         
func KeyFrostVaultLastMigrationArtifact(chain string, vaultID uint32) string {
	return fmt.Sprintf("%svault_last_migration_artifact_%s_%d", pfxFrost, strings.ToLower(strings.TrimSpace(chain)), vaultID)
}

                                                                             
                                                                                 
func KeyFrostWithdrawArtifact(withdrawID string) string {
	return pfxFrostWithdrawArtifact + withdrawID
}

func KeyFrostMigrationArtifact(migrationID string) string {
	return pfxFrost + "migration_artifact_" + migrationID
}

func KeyFrostMigrationPackageHash(migrationID string) string {
	return pfxFrost + "migration_package_hash_" + migrationID
}

func KeyFrostMigrationStateRef(migrationID string) string {
	return pfxFrost + "migration_state_ref_" + migrationID
}

                                                          
                                                          
func KeyFrostBtcLockedUtxo(vaultID uint32, txid string, vout uint32) string {
	return fmt.Sprintf("%sbtc_locked_utxo_%d_%s_%d", pfxFrost, vaultID, txid, vout)
}

                                                
                                             
func KeyFrostBtcUtxoFIFOIndex(vaultID uint32, seq uint64) string {
	return fmt.Sprintf("%sbtc_utxo_q_%d_%s", pfxFrost, vaultID, padUint(seq))
}

                                                                             
func KeyFrostBtcUtxoFIFOIndexPrefix(vaultID uint32) string {
	return fmt.Sprintf("%sbtc_utxo_q_%d_", pfxFrost, vaultID)
}

                                                       
                                         
func KeyFrostBtcUtxoFIFOSeq(vaultID uint32) string {
	return fmt.Sprintf("%sbtc_utxo_seq_%d", pfxFrost, vaultID)
}

                                                  
                                          
func KeyFrostBtcUtxoFIFOHead(vaultID uint32) string {
	return fmt.Sprintf("%sbtc_utxo_head_%d", pfxFrost, vaultID)
}

                                      
                                        
func KeyFrostWithdraw(withdrawID string) string {
	return pfxFrost + "withdraw_" + withdrawID
}

                                                    
func KeyFrostWithdrawJob(jobID string) string {
	return pfxFrost + "withdraw_job_" + jobID
}

                                                          
func KeyFrostWithdrawJobPrefix() string {
	return pfxFrost + "withdraw_job_"
}

                                                                         
                                                                             
                                                  
func KeyFrostWithdrawActiveJob(chain string, vaultID uint32, epoch uint64, jobID string) string {
	return fmt.Sprintf("%swithdraw_active_%s_%d_%s_%s", pfxFrost, chain, vaultID, padUint(epoch), jobID)
}

func KeyFrostWithdrawActiveJobPrefix(chain string, vaultID uint32, epoch uint64) string {
	return fmt.Sprintf("%swithdraw_active_%s_%d_%s_", pfxFrost, chain, vaultID, padUint(epoch))
}

                                                                            
                                                                             
                                                                             
                                                          
func KeyFrostWithdrawActiveVaultJob(chain string, vaultID uint32) string {
	return fmt.Sprintf("%swithdraw_active_vault_%s_%d", pfxFrost, strings.ToLower(strings.TrimSpace(chain)), vaultID)
}

                                                                             
                                                                              
                             
func KeyFrostWithdrawLiveIndex(chain string, vaultID uint32, seq uint64) string {
	return fmt.Sprintf("%swithdraw_live_%s_%d_%s", pfxFrost, strings.ToLower(strings.TrimSpace(chain)), vaultID, padUint(seq))
}

func KeyFrostWithdrawLiveIndexPrefix(chain string, vaultID uint32) string {
	return fmt.Sprintf("%swithdraw_live_%s_%d_", pfxFrost, strings.ToLower(strings.TrimSpace(chain)), vaultID)
}

func KeyFrostWithdrawLiveHead(chain string, vaultID uint32) string {
	return fmt.Sprintf("%swithdraw_live_head_%s_%d", pfxFrost, strings.ToLower(strings.TrimSpace(chain)), vaultID)
}

func KeyFrostWithdrawLiveTail(chain string, vaultID uint32) string {
	return fmt.Sprintf("%swithdraw_live_tail_%s_%d", pfxFrost, strings.ToLower(strings.TrimSpace(chain)), vaultID)
}

func KeyFrostWithdrawLivePending(chain string, vaultID uint32) string {
	return fmt.Sprintf("%swithdraw_live_pending_%s_%d", pfxFrost, strings.ToLower(strings.TrimSpace(chain)), vaultID)
}

                                                                             
                                                        
func KeyFrostWithdrawRoundRobinCursor(chain string) string {
	return fmt.Sprintf("%swithdraw_rr_%s", pfxFrost, strings.ToLower(strings.TrimSpace(chain)))
}

                                                                               
                                                                    
                                                                                  
func KeyFrostWithdrawBatch(chain string, vaultID uint32, batchSeq string) string {
	return fmt.Sprintf("%s%s_%d_%s", pfxFrostPermanentBatch, strings.ToLower(chain), vaultID, batchSeq)
}

func KeyFrostVaultDEXNextBatchSeq(chain string, vaultID uint32) string {
	return fmt.Sprintf("%svault_next_batch_seq_%s_%d", pfxFrost, strings.ToLower(strings.TrimSpace(chain)), vaultID)
}

                                                                              
func KeyFrostVaultHandoverConsumed(chain string, vaultID uint32, epoch uint64) string {
	return fmt.Sprintf("%svault_handover_consumed_%s_%d_%s", pfxFrost, strings.ToLower(strings.TrimSpace(chain)), vaultID, padUint(epoch))
}

                                                                      
                                             
func KeyFrostWithdrawCompositePaid(withdrawID string) string {
	return pfxFrost + "withdraw_paid_" + withdrawID
}

                                                                        
                                                  
func KeyFrostWithdrawFIFOIndex(chain, asset string, seq uint64) string {
	return fmt.Sprintf("%swithdraw_q_%s_%s_%s", pfxFrost, chain, asset, padUint(seq))
}

func KeyFrostWithdrawFIFOIndexPrefix() string { return pfxFrost + "withdraw_q_" }

                                                                        
                                              
func KeyFrostWithdrawFIFOSeq(chain, asset string) string {
	return fmt.Sprintf("%swithdraw_seq_%s_%s", pfxFrost, chain, asset)
}

func KeyFrostWithdrawFIFOSeqPrefix() string {
	return pfxFrost + "withdraw_seq_"
}

                                                                                      
                                               
func KeyFrostWithdrawFIFOHead(chain, asset string) string {
	return fmt.Sprintf("%swithdraw_head_%s_%s", pfxFrost, chain, asset)
}

                                                                            
                                      
func KeyFrostWithdrawTxRef(txID string) string {
	return pfxFrost + "withdraw_ref_" + txID
}

func frostWithdrawQueryChain(chain string) string {
	return strings.ToLower(strings.TrimSpace(chain))
}

                                                                              
                                                                             
func KeyFrostWithdrawQueryGlobal(requestHeight, seq uint64, withdrawID string) string {
	return fmt.Sprintf("%sglobal|height:%020d|seq:%020d|withdraw_id:%s", pfxFrostWithdrawQuery, ^requestHeight, ^seq, withdrawID)
}

func KeyFrostWithdrawQueryGlobalPrefix() string {
	return pfxFrostWithdrawQuery + "global|"
}

                                                                         
                      
func KeyFrostWithdrawQueryChain(chain string, requestHeight, seq uint64, withdrawID string) string {
	return fmt.Sprintf("%schain|chain:%s|height:%020d|seq:%020d|withdraw_id:%s",
		pfxFrostWithdrawQuery, frostWithdrawQueryChain(chain), ^requestHeight, ^seq, withdrawID)
}

func KeyFrostWithdrawQueryChainPrefix(chain string) string {
	return fmt.Sprintf("%schain|chain:%s|", pfxFrostWithdrawQuery, frostWithdrawQueryChain(chain))
}

                                                                       
                                                                               
                                               
func KeyFrostWithdrawQueryChainVault(chain string, vaultID uint32, requestHeight, seq uint64, withdrawID string) string {
	return fmt.Sprintf("%schain_vault|chain:%s|vault:%010d|height:%020d|seq:%020d|withdraw_id:%s",
		pfxFrostWithdrawQuery, frostWithdrawQueryChain(chain), vaultID, ^requestHeight, ^seq, withdrawID)
}

func KeyFrostWithdrawQueryChainVaultPrefix(chain string, vaultID uint32) string {
	return fmt.Sprintf("%schain_vault|chain:%s|vault:%010d|", pfxFrostWithdrawQuery, frostWithdrawQueryChain(chain), vaultID)
}

func KeyFrostWithdrawQueryPrefix() string { return pfxFrostWithdrawQuery }

                                                                              
                                                                                  
func frostWithdrawQueryKindSuffix(terminal bool) string {
	if terminal {
		return "|kind:terminal"
	}
	return "|kind:active"
}

func KeyFrostWithdrawQueryGlobalState(terminal bool, requestHeight, seq uint64, withdrawID string) string {
	return KeyFrostWithdrawQueryGlobal(requestHeight, seq, withdrawID) + frostWithdrawQueryKindSuffix(terminal)
}

func KeyFrostWithdrawQueryChainState(chain string, terminal bool, requestHeight, seq uint64, withdrawID string) string {
	return KeyFrostWithdrawQueryChain(chain, requestHeight, seq, withdrawID) + frostWithdrawQueryKindSuffix(terminal)
}

func KeyFrostWithdrawQueryChainVaultState(chain string, vaultID uint32, terminal bool, requestHeight, seq uint64, withdrawID string) string {
	return KeyFrostWithdrawQueryChainVault(chain, vaultID, requestHeight, seq, withdrawID) + frostWithdrawQueryKindSuffix(terminal)
}

                                                                                              
func KeyFrostLocalShare(chain string, vaultID uint32, epochID uint64) string {
	return fmt.Sprintf("%s%s_%d_%s", pfxLocalFrostShare, chain, vaultID, padUint(epochID))
}

                                                                           
                                                                 
func KeyFrostLocalSharePrefix() string {
	return pfxLocalFrostShare
}

                                                                            
                                                  
func KeyFrostPendingLocalShare(chain string, vaultID uint32, epochID uint64, sessionID string) string {
	return fmt.Sprintf("%s%s_%d_%s_%s", pfxLocalFrostPending, chain, vaultID, padUint(epochID), sessionID)
}

func KeyFrostPendingLocalSharePrefix() string {
	return pfxLocalFrostPending
}

                                                                                                                                     
func KeyFrostLocalDkgReveal(chain string, vaultID uint32, epochID uint64, sessionID string, receiverIndex int) string {
	return fmt.Sprintf("%s%s_%d_%s_%s_%d", pfxLocalFrostDkgReveal, chain, vaultID, padUint(epochID), sessionID, receiverIndex)
}

func KeyFrostLocalDkgRevealSessionPrefix(chain string, vaultID uint32, epochID uint64, sessionID string) string {
	return fmt.Sprintf("%s%s_%d_%s_%s_", pfxLocalFrostDkgReveal, chain, vaultID, padUint(epochID), sessionID)
}

                                                                            
                                                        
func KeyFrostLocalDkgRevealPrefix() string {
	return pfxLocalFrostDkgReveal
}

func KeyFrostLocalDkgSession(chain string, vaultID uint32, epochID uint64, sessionID string) string {
	return fmt.Sprintf("%s%s_%d_%s_%s", pfxLocalFrostDkgSession, chain, vaultID, padUint(epochID), sessionID)
}

func KeyFrostLocalDkgSessionPrefix() string {
	return pfxLocalFrostDkgSession
}

                                    
                     
func KeyFrostConfig() string {
	return pfxFrost + "cfg"
}

                                                                                              
                               
func KeyFrostActiveMiners() string {
	return pfxFrost + "active_miners"
}

                                   
                                              
func KeyFrostVaultConfig(chain string, vaultID uint32) string {
	return fmt.Sprintf("%svault_cfg_%s_%d", pfxFrost, chain, vaultID)
}

                                                                          
                                                
func KeyFrostVaultState(chain string, vaultID uint32) string {
	return fmt.Sprintf("%svault_state_%s_%d", pfxFrost, chain, vaultID)
}

                                                                                                                      
                              
func KeyFrostVaultStatePrefix() string {
	return pfxFrost + "vault_state_"
}

                                                             
                                
func KeyMinerExitRecord(address string) string {
	return pfxMinerExit + address
}

                                                                          
                       
func KeyMinerExitPrefix() string {
	return pfxMinerExit
}

                                                                              
                                                                        
func KeyMinerBLSPublicKeyOwner(publicKey []byte) string {
	digest := sha256.Sum256(publicKey)
	return pfxMinerIdentity + "bls_" + hex.EncodeToString(digest[:])
}

                                             
                                                                
func KeyFrostVaultTransition(chain string, vaultID uint32, epochID uint64) string {
	return fmt.Sprintf("%svault_transition_%s_%d_%s", pfxFrost, chain, vaultID, padUint(epochID))
}

func KeyFrostVaultTransitionPrefix() string {
	return pfxFrost + "vault_transition_"
}

                                                                          
                                                                           
                                                                                
func KeyFrostVaultMigration(chain string, vaultID uint32, epochID uint64) string {
	return fmt.Sprintf("%svault_migration_%s_%d_%s", pfxFrost, chain, vaultID, padUint(epochID))
}

func KeyFrostVaultMigrationPrefix() string {
	return pfxFrost + "vault_migration_"
}

                                                                      
                                                                              
                                            
func KeyFrostVaultHandoverProof(chain string, vaultID uint32, epoch uint64) string {
	return fmt.Sprintf("%svault_handover_proof_%s_%d_%s", pfxFrost, chain, vaultID, padUint(epoch))
}

                                                         
                                                                       
func KeyFrostVaultTransitionActive(chain string, vaultID uint32, epochID uint64) string {
	return fmt.Sprintf("%svault_active_transition_%s_%d_%s", pfxFrost, chain, vaultID, padUint(epochID))
}

                                                                     
                                          
func KeyFrostVaultTransitionActivePrefix() string {
	return pfxFrost + "vault_active_transition_"
}

                                             
                                                                              
func KeyFrostVaultDkgCommit(chain string, vaultID uint32, epochID uint64, sessionID, participant string) string {
	return fmt.Sprintf("%svault_dkg_commit_%s_%d_%s_%s_%s", pfxFrost, chain, vaultID, padUint(epochID), sessionID, participant)
}

func KeyFrostVaultDkgCommitPrefix(chain string, vaultID uint32, epochID uint64, sessionID string) string {
	return fmt.Sprintf("%svault_dkg_commit_%s_%d_%s_%s_", pfxFrost, chain, vaultID, padUint(epochID), sessionID)
}

                                                    
                                                                                   
func KeyFrostVaultDkgShare(chain string, vaultID uint32, epochID uint64, sessionID, dealer, receiver string) string {
	return fmt.Sprintf("%svault_dkg_share_%s_%d_%s_%s_%s_%s", pfxFrost, chain, vaultID, padUint(epochID), sessionID, dealer, receiver)
}

func KeyFrostVaultDkgSharePrefix(chain string, vaultID uint32, epochID uint64, sessionID string) string {
	return fmt.Sprintf("%svault_dkg_share_%s_%d_%s_%s_", pfxFrost, chain, vaultID, padUint(epochID), sessionID)
}

                                             
                                                                                       
func KeyFrostVaultDkgComplaint(chain string, vaultID uint32, epochID uint64, sessionID, dealer, receiver string) string {
	return fmt.Sprintf("%svault_dkg_complaint_%s_%d_%s_%s_%s_%s", pfxFrost, chain, vaultID, padUint(epochID), sessionID, dealer, receiver)
}

                                                         
                                                                       
func KeyFrostVaultDkgComplaintPrefix(chain string, vaultID uint32, epochID uint64, sessionID string) string {
	return fmt.Sprintf("%svault_dkg_complaint_%s_%d_%s_%s_", pfxFrost, chain, vaultID, padUint(epochID), sessionID)
}

                                                               
                                                                                         
func KeyFrostVaultDkgValidationAck(chain string, vaultID uint32, epochID uint64, sessionID, participant string) string {
	return fmt.Sprintf("%svault_dkg_validation_ack_%s_%d_%s_%s_%s", pfxFrost, chain, vaultID, padUint(epochID), sessionID, participant)
}

                                                                           
                                                                            
func KeyFrostVaultDkgValidationAckPrefix(chain string, vaultID uint32, epochID uint64, sessionID string) string {
	return fmt.Sprintf("%svault_dkg_validation_ack_%s_%d_%s_%s_", pfxFrost, chain, vaultID, padUint(epochID), sessionID)
}

                                           
                                           
func KeyFrostSignedPackage(jobID string, idx uint64) string {
	return fmt.Sprintf("%ssigned_pkg_%s_%s", pfxFrost, jobID, padUint(idx))
}

func padUint(v uint64) string {
	return fmt.Sprintf("%020d", v)
}

                                                                    

var (
	pfxVMAppliedTx = regKV("vm_applied_tx_", "VM applied tx state")
	pfxVMTxError   = regKV("vm_tx_error_", "VM tx error")
	pfxVMTxHeight  = regKV("vm_tx_height_", "VM tx height")
	pfxVMTxFee     = regKV("vm_tx_fee_", "VM tx charged fee")
	pfxVMCommitH   = regKV("vm_commit_h_", "VM commit height")
	pfxVMTxFBBal   = regKV("vmtx_fbbal_", "VM tx FB balance snapshot")
)

                                             
                                
func KeyVMAppliedTx(txID string) string {
	return pfxVMAppliedTx + txID
}

                                     
                              
func KeyVMTxError(txID string) string {
	return pfxVMTxError + txID
}

                                      
                               
func KeyVMTxHeight(txID string) string {
	return pfxVMTxHeight + txID
}

                                         
func KeyVMTxFee(txID string) string {
	return pfxVMTxFee + txID
}

                                                                     
                                
func KeyVMCommitHeight(height uint64) string {
	return fmt.Sprintf("%s%d", pfxVMCommitH, height)
}

                                                      
                             
func KeyVMTxFBBalance(txID string) string {
	return pfxVMTxFBBal + txID
}

                                                                     

var (
	pfxNode       = regKV("node_", "Node info")
	pfxClientInfo = regKV("clientinfo_", "Client info")
)

                             
                 
func KeyNode() string {
	return pfxNode
}

                                                                              
func MinerAddress(miner string) string {
	return strings.TrimPrefix(miner, pfxNode)
}

                                
                           
func KeyClientInfo(ip string) string {
	return pfxClientInfo + ip
}

                                                                      

var (
	pfxWinfo                   = regState("winfo_", "Witness profile/state")
	pfxRechargeRequest         = regState("recharge_request_", "Witness recharge request state")
	pfxRechargeQuery           = regExcluded("recharge_query_recent_", "Recharge request recent-list indexes (KV-only)")
	pfxRechargeNative          = regState("recharge_native_", "Recharge native tx dedup index")
	pfxRechargeEvent           = regState("recharge_event_", "Consumed account-chain Vault deposit event")
	pfxRechargeEventPending    = regState("recharge_event_pending_", "Pending Vault deposit per event")
	pfxRechargePendingMigrate  = regState("recharge_pending_migration_", "Global pending Vault event migration marker")
	pfxRuntimeFeeEscrow        = regState("runtime_fee_escrow_", "Delayed protocol runtime fee escrow")
	pfxRuntimeFeeRelease       = regState("runtime_fee_release_", "Block-local released runtime fees")
	pfxRechargeLegacyNative    = regState("recharge_legacy_native_", "Canonical quarantine of legacy native transaction locks")
	pfxRechargeLegacyMigration = regState("recharge_legacy_migration_", "Legacy native-lock canonicalization marker")
	pfxRechargeSelDue          = regState("recharge_selection_due_", "Recharge witness selection due index")
	pfxRechargeVoteDue         = regState("recharge_vote_grace_due_", "Recharge vote grace close due index")
	pfxRechargeChalDue         = regState("recharge_challenge_due_", "Recharge challenge-period finalization due index")
	pfxChallenge               = regState("challenge_", "Challenge records")
	pfxChallengeSelDue         = regState("arbitration_selection_due_", "Challenge arbitration selection due index")
	pfxChallengeTmoDue         = regState("arbitration_timeout_due_", "Challenge arbitration timeout due index")
	pfxChallengeTmoBoot        = regState("arbitration_timeout_backfill_", "Challenge arbitration timeout index backfill marker")
	pfxChallengeContrib        = regState("challengecontrib_", "Challenge stake/evidence contributions")
	pfxWvote                   = regState("wvote_", "Witness votes")
	pfxArbitrationVote         = regState("arbitration_vote_", "Arbitration votes")
	pfxArbitratorCase          = regState("arbitrator_case_", "Active challenge assignments by arbitrator")
	pfxWstake                  = regState("wstake_", "Witness stake index")
	pfxWitnessConfig           = regState("witness_config", "Witness module config")
	pfxShelvedRequest          = regState("shelved_request_", "Shelved recharge requests")
	pfxRechargeCredited        = regState("recharge_credited_", "Recharge credited idempotency mark")
	pfxWitnessAssignedLegacy   = regState("wassign_", "Legacy witness assignments, removed by consensus migration")
	pfxWitnessAssigned         = regExcluded("witness_assigned_query_", "Rebuildable witness-to-request query index")
	pfxWitnessAssignedMigrated = regState("witness_assignment_query_migrated", "Legacy assignment StateDB cleanup marker")
)

                                 
                           
func KeyWitnessInfo(address string) string {
	return pfxWinfo + address
}

                                             
                  
func KeyWitnessInfoPrefix() string {
	return pfxWinfo
}

                                  
                                        
func KeyRechargeRequest(requestID string) string {
	return pfxRechargeRequest + requestID
}

                                              
                             
func KeyRechargeRequestPrefix() string {
	return pfxRechargeRequest
}

                                                                            
                                                                              
var pfxRechargeRewardOwner = regKV("recharge_reward_owner_", "Recharge credit attribution by owner and request")

func KeyRechargeRewardOwner(owner, requestID string) string {
	return pfxRechargeRewardOwner + owner + "_" + requestID
}

func KeyRechargeRewardOwnerPrefix(owner string) string {
	return pfxRechargeRewardOwner + owner + "_"
}

func rechargeQueryKind(terminal bool) string {
	if terminal {
		return "terminal"
	}
	return "active"
}

func rechargeQueryChain(chain string) string {
	return strings.ToLower(strings.TrimSpace(chain))
}

func KeyRechargeQueryRecentPrefix() string {
	return pfxRechargeQuery
}

func KeyRechargeQueryRecentGlobal(terminal bool, orderHeight uint64, requestID string) string {
	return fmt.Sprintf("%sglobal|kind:%s|height:%020d|request_id:%s", pfxRechargeQuery, rechargeQueryKind(terminal), ^orderHeight, requestID)
}

func KeyRechargeQueryRecentGlobalPrefix(terminal bool) string {
	return fmt.Sprintf("%sglobal|kind:%s|", pfxRechargeQuery, rechargeQueryKind(terminal))
}

func KeyRechargeQueryRecentChain(chain string, terminal bool, orderHeight uint64, requestID string) string {
	return fmt.Sprintf("%schain:%s|kind:%s|height:%020d|request_id:%s", pfxRechargeQuery, rechargeQueryChain(chain), rechargeQueryKind(terminal), ^orderHeight, requestID)
}

func KeyRechargeQueryRecentChainPrefix(chain string, terminal bool) string {
	return fmt.Sprintf("%schain:%s|kind:%s|", pfxRechargeQuery, rechargeQueryChain(chain), rechargeQueryKind(terminal))
}

func KeyRechargeQueryRecentVault(vaultID uint32, terminal bool, orderHeight uint64, requestID string) string {
	return fmt.Sprintf("%svault:%010d|kind:%s|height:%020d|request_id:%s", pfxRechargeQuery, vaultID, rechargeQueryKind(terminal), ^orderHeight, requestID)
}

func KeyRechargeQueryRecentVaultPrefix(vaultID uint32, terminal bool) string {
	return fmt.Sprintf("%svault:%010d|kind:%s|", pfxRechargeQuery, vaultID, rechargeQueryKind(terminal))
}

func KeyRechargeQueryRecentChainVault(chain string, vaultID uint32, terminal bool, orderHeight uint64, requestID string) string {
	return fmt.Sprintf("%schain:%s|vault:%010d|kind:%s|height:%020d|request_id:%s", pfxRechargeQuery, rechargeQueryChain(chain), vaultID, rechargeQueryKind(terminal), ^orderHeight, requestID)
}

func KeyRechargeQueryRecentChainVaultPrefix(chain string, vaultID uint32, terminal bool) string {
	return fmt.Sprintf("%schain:%s|vault:%010d|kind:%s|", pfxRechargeQuery, rechargeQueryChain(chain), vaultID, rechargeQueryKind(terminal))
}

                                                                       
                                            
func KeyRechargeRequestByNativeTx(chain, txHash string) string {
	return fmt.Sprintf("%s%s_%s", pfxRechargeNative, chain, txHash)
}

func KeyRechargeNativePrefix() string { return pfxRechargeNative }

func KeyRechargeEvent(chain, txHash string, eventIndex uint32) string {
	return fmt.Sprintf("%s%s_%s_%010d", pfxRechargeEvent, chain, txHash, eventIndex)
}

func KeyRechargeEventPending(chain, txHash string, eventIndex uint32) string {
	return fmt.Sprintf("%s%s_%s_%010d", pfxRechargeEventPending, chain, txHash, eventIndex)
}

func KeyRechargeEventPendingPrefix() string { return pfxRechargeEventPending }

func KeyRechargePendingMigration() string { return pfxRechargePendingMigrate + "v2" }

func KeyRuntimeFeeEscrow(sourceTxID string) string {
	return pfxRuntimeFeeEscrow + sourceTxID
}

func KeyRuntimeFeeRelease(height uint64, sourceTxID string) string {
	return fmt.Sprintf("%s%020d_%s", pfxRuntimeFeeRelease, height, sourceTxID)
}

func KeyRuntimeFeeReleasePrefix(height uint64) string {
	return fmt.Sprintf("%s%020d_", pfxRuntimeFeeRelease, height)
}

func KeyRechargeLegacyNative(chain, txHash string) string {
	return pfxRechargeLegacyNative + chain + "_" + txHash
}

func KeyRechargeLegacyMigration() string { return pfxRechargeLegacyMigration + "v1" }

                                                                                       
                                                                     
func KeyRechargeSelectionDue(readyHeight uint64, requestID string) string {
	return fmt.Sprintf("%s%020d_%s", pfxRechargeSelDue, readyHeight, requestID)
}

                                                                         
func KeyRechargeSelectionDuePrefix(readyHeight uint64) string {
	return fmt.Sprintf("%s%020d_", pfxRechargeSelDue, readyHeight)
}

                                                                                     
func KeyRechargeSelectionDueAllPrefix() string {
	return pfxRechargeSelDue
}

                                                                                       
                                                                                
func KeyRechargeVoteGraceDue(acceptUntilHeight uint64, requestID string) string {
	return fmt.Sprintf("%s%020d_%s", pfxRechargeVoteDue, acceptUntilHeight, requestID)
}

                                                                                      
func KeyRechargeVoteGraceDuePrefix(acceptUntilHeight uint64) string {
	return fmt.Sprintf("%s%020d_", pfxRechargeVoteDue, acceptUntilHeight)
}

                                                                             
func KeyRechargeVoteGraceDueAllPrefix() string {
	return pfxRechargeVoteDue
}

                                                                                             
                                                                     
func KeyRechargeChallengeDue(finalizeHeight uint64, requestID string) string {
	return fmt.Sprintf("%s%020d_%s", pfxRechargeChalDue, finalizeHeight, requestID)
}

                                                                                
func KeyRechargeChallengeDuePrefix(finalizeHeight uint64) string {
	return fmt.Sprintf("%s%020d_", pfxRechargeChalDue, finalizeHeight)
}

                                                                                                         
func KeyRechargeChallengeDueAllPrefix() string {
	return pfxRechargeChalDue
}

                                  
                                   
func KeyChallengeRecord(challengeID string) string {
	return pfxChallenge + challengeID
}

                                              
                      
func KeyChallengeRecordPrefix() string {
	return pfxChallenge
}

                                                                             
                                                           
func KeyArbitratorChallenge(arbitrator, challengeID string) string {
	return fmt.Sprintf("%s%s_%s", pfxArbitratorCase, arbitrator, challengeID)
}

func KeyArbitratorChallengePrefix(arbitrator string) string {
	return fmt.Sprintf("%s%s_", pfxArbitratorCase, arbitrator)
}

                                                                                 
                                                                          
func KeyChallengeSelectionDue(readyHeight uint64, challengeID string) string {
	return fmt.Sprintf("%s%020d_%s", pfxChallengeSelDue, readyHeight, challengeID)
}

                                                                          
func KeyChallengeSelectionDuePrefix(readyHeight uint64) string {
	return fmt.Sprintf("%s%020d_", pfxChallengeSelDue, readyHeight)
}

                                                                                       
func KeyChallengeSelectionDueAllPrefix() string {
	return pfxChallengeSelDue
}

                                                                                             
                                                                        
func KeyChallengeTimeoutDue(dueHeight uint64, challengeID string) string {
	return fmt.Sprintf("%s%020d_%s", pfxChallengeTmoDue, dueHeight, challengeID)
}

                                                                                
func KeyChallengeTimeoutDuePrefix(dueHeight uint64) string {
	return fmt.Sprintf("%s%020d_", pfxChallengeTmoDue, dueHeight)
}

                                                                                         
func KeyChallengeTimeoutDueAllPrefix() string {
	return pfxChallengeTmoDue
}

                                                                                            
func KeyChallengeTimeoutIndexBackfillDone() string {
	return pfxChallengeTmoBoot + "done"
}

                                                                                
                                                    
func KeyChallengeContribution(challengeID, txID string) string {
	return fmt.Sprintf("%s%s_%s", pfxChallengeContrib, challengeID, txID)
}

                                                                                
func KeyChallengeContributionPrefix(challengeID string) string {
	return fmt.Sprintf("%s%s_", pfxChallengeContrib, challengeID)
}

                              
                                              
func KeyWitnessVote(requestID, witnessAddress string) string {
	return fmt.Sprintf("%s%s_%s", pfxWvote, requestID, witnessAddress)
}

                                                           
                              
func KeyWitnessVotePrefix(requestID string) string {
	return fmt.Sprintf("%s%s_", pfxWvote, requestID)
}

                                  
                                                              
func KeyArbitrationVote(challengeID, arbitratorAddress string) string {
	return fmt.Sprintf("%s%s_%s", pfxArbitrationVote, challengeID, arbitratorAddress)
}

                                                                     
func KeyArbitrationVoteRound(challengeID string, round uint64, address string) string {
	return KeyArbitrationVotePrefix(challengeID) + fmt.Sprintf("%020d_", round) + address
}

                                                               
                                           
func KeyArbitrationVotePrefix(challengeID string) string {
	return fmt.Sprintf("%s%s_", pfxArbitrationVote, challengeID)
}

                                                                        
                                               
func KeyWitnessStakeIndex(invertedPadded32, address string) string {
	return fmt.Sprintf("%s%s_%s", pfxWstake, invertedPadded32, address)
}

                                                         
                   
func KeyWitnessStakeIndexPrefix() string {
	return pfxWstake
}

                                   
                          
func KeyWitnessConfig() string {
	return pfxWitnessConfig
}

                                                                             
func KeyWitnessAssigned(witnessAddress, requestID string) string {
	return KeyWitnessAssignedPrefix(witnessAddress) + requestID
}

                                                                      
func KeyWitnessAssignedPrefix(witnessAddress string) string {
	if witnessAddress == "" {
		return pfxWitnessAssigned
	}
	return fmt.Sprintf("%skind:history|%s_", pfxWitnessAssigned, witnessAddress)
}

func KeyWitnessAssignedCurrent(witnessAddress, requestID string) string {
	return KeyWitnessAssignedCurrentPrefix(witnessAddress) + requestID
}

func KeyWitnessAssignedCurrentPrefix(witnessAddress string) string {
	return fmt.Sprintf("%skind:current|%s_", pfxWitnessAssigned, witnessAddress)
}

func KeyLegacyWitnessAssigned(witnessAddress, requestID string) string {
	return fmt.Sprintf("%s%s_%s", pfxWitnessAssignedLegacy, witnessAddress, requestID)
}

func KeyLegacyWitnessAssignedPrefix() string    { return pfxWitnessAssignedLegacy }
func KeyWitnessAssignmentQueryMigrated() string { return pfxWitnessAssignedMigrated }

                                    
                                       
func KeyShelvedRequest(requestID string) string {
	return pfxShelvedRequest + requestID
}

                                             
                            
func KeyShelvedRequestPrefix() string {
	return pfxShelvedRequest
}

                                               
                                         
func KeyRechargeCredited(requestID string) string {
	return pfxRechargeCredited + requestID
}

                                                         

var (
	pfxJMTRoot  = regKV("jmt_root", "JMT state root")
	pfxJMTNode  = regKV("jmt_n_", "JMT node")
	pfxJMTValue = regKV("jmt_v_", "JMT leaf value")
)

                                 
                    
func KeyJMTRoot() string {
	return pfxJMTRoot
}

                                    
func KeyJMTNodePrefix() string {
	return pfxJMTNode
}

                                        
func KeyJMTValuePrefix() string {
	return pfxJMTValue
}

                                                                 
  
                                                                                                           

var (
	pfxFuture                = regState("future_", "Perpetual engine mutable consensus state")
	pfxFutureAccountOrder    = regExcluded("future_acc_order_", "Perpetual account order history/index (KV-only)")
	pfxFutureTrade           = regExcluded("future_trade_", "Perpetual trade records (KV-only)")
	pfxFuturePositionHistory = regExcluded("future_pos_history_", "Perpetual terminal position history (KV-only)")
	pfxSpotLastPrice         = regState("spot_last_price_", "Spot last trade price")
)

                                                                                 

                                                                                     
                                                                                       
func KeyFutureOrderPriceIndex(pair string, side pb.OrderSide, isFilled bool, priceKey67, orderID string) string {
	return pfxFuture + "pair:" + pair + "|side:" + strconv.Itoa(int(side)) + "|is_filled:" + strconv.FormatBool(isFilled) + "|price:" + priceKey67 + "|order_id:" + orderID
}

                                                                      
func KeyFutureOrderPriceIndexPrefix(pair string, side pb.OrderSide, isFilled bool) string {
	return pfxFuture + "pair:" + pair + "|side:" + strconv.Itoa(int(side)) + "|is_filled:" + strconv.FormatBool(isFilled) + "|"
}

                                               
                                       
func KeyFutureOrderState(orderID string) string {
	return pfxFuture + "orderstate_" + orderID
}

                                                           
func KeyFutureOrderStatePrefix() string {
	return pfxFuture + "orderstate_"
}

                                                                                   
func FutureOrderStatusKey(status pb.FutureOrderStatus) string {
	switch status {
	case pb.FutureOrderStatus_FUTURE_ORDER_OPEN:
		return "open"
	case pb.FutureOrderStatus_FUTURE_ORDER_FILLED:
		return "filled"
	case pb.FutureOrderStatus_FUTURE_ORDER_CANCELLED:
		return "cancelled"
	case pb.FutureOrderStatus_FUTURE_ORDER_CLOSED:
		return "closed"
	case pb.FutureOrderStatus_FUTURE_ORDER_LIQUIDATED:
		return "liquidated"
	default:
		return "unknown"
	}
}

func FutureAccountOrderStatusKeys() []string {
	return []string{"open", "filled", "cancelled", "closed", "liquidated", "unknown"}
}

                                                                                            
                                                                                                                 
func KeyFutureAccountOrder(owner, pair string, status pb.FutureOrderStatus, createHeight uint64, orderID string) string {
	return fmt.Sprintf("%sowner:%s|pair:%s|status:%s|height:%020d|order_id:%s", pfxFutureAccountOrder, owner, pair, FutureOrderStatusKey(status), ^createHeight, orderID)
}

func KeyFutureAccountOrderFromState(st *pb.FutureOrderState) string {
	if st == nil {
		return ""
	}
	owner := st.OwnerAddress
	if owner == "" {
		owner = st.Owner
	}
	if owner == "" || st.Pair == "" || st.OrderId == "" {
		return ""
	}
	return KeyFutureAccountOrder(owner, st.Pair, st.Status, st.CreateHeight, st.OrderId)
}

                                                                    
func KeyFutureAccountOrderPrefix(owner, pair, status string) string {
	return fmt.Sprintf("%sowner:%s|pair:%s|status:%s|", pfxFutureAccountOrder, owner, pair, status)
}

func KeyFutureAccountOrderOwnerPrefix(owner string) string {
	return fmt.Sprintf("%sowner:%s|", pfxFutureAccountOrder, owner)
}

func KeyFutureAccountOrderFamilyPrefix() string {
	return pfxFutureAccountOrder
}

                                                                              
                                                         
func KeyFutureAccountOrderRecent(owner string, status pb.FutureOrderStatus, createHeight uint64, pair, orderID string) string {
	return fmt.Sprintf("%srecent|owner:%s|status:%s|height:%020d|pair:%s|order_id:%s", pfxFutureAccountOrder, owner, FutureOrderStatusKey(status), ^createHeight, pair, orderID)
}

func KeyFutureAccountOrderRecentFromState(st *pb.FutureOrderState) string {
	if st == nil || st.Pair == "" || st.OrderId == "" {
		return ""
	}
	owner := st.OwnerAddress
	if owner == "" {
		owner = st.Owner
	}
	if owner == "" {
		return ""
	}
	return KeyFutureAccountOrderRecent(owner, st.Status, st.CreateHeight, st.Pair, st.OrderId)
}

func KeyFutureAccountOrderRecentPrefix(owner, status string) string {
	return fmt.Sprintf("%srecent|owner:%s|status:%s|", pfxFutureAccountOrder, owner, status)
}

func KeyFuturePositionHistory(positionID string) string {
	return pfxFuturePositionHistory + positionID
}

func KeyFuturePositionHistoryPrefix() string {
	return pfxFuturePositionHistory
}

func FuturePositionStatusKey(status int32) string {
	switch status {
	case 1:
		return "open"
	case 2:
		return "closed"
	case 3:
		return "liquidated"
	case 4:
		return "adl_closed"
	case 5:
		return "insured_deleveraged"
	default:
		return "unknown"
	}
}

func FuturePositionTerminalStatusKeys() []string {
	return []string{"closed", "liquidated", "adl_closed", "insured_deleveraged", "unknown"}
}

                                                                               
                                                                              
                                                                                
                                                         
func KeyFuturePositionHistoryOwnerIndex(owner string, status int32, closeHeight uint64, positionID string) string {
	return fmt.Sprintf("%sindex|owner:%s|status:%s|height:%020d|position_id:%s", pfxFuturePositionHistory, owner, FuturePositionStatusKey(status), ^closeHeight, positionID)
}

func KeyFuturePositionHistoryOwnerIndexPrefix(owner, status string) string {
	return fmt.Sprintf("%sindex|owner:%s|status:%s|", pfxFuturePositionHistory, owner, status)
}

func KeyFuturePositionHistoryPairIndex(pair string, status int32, closeHeight uint64, positionID string) string {
	return fmt.Sprintf("%sindex|pair:%s|status:%s|height:%020d|position_id:%s", pfxFuturePositionHistory, pair, FuturePositionStatusKey(status), ^closeHeight, positionID)
}

func KeyFuturePositionHistoryPairIndexPrefix(pair, status string) string {
	return fmt.Sprintf("%sindex|pair:%s|status:%s|", pfxFuturePositionHistory, pair, status)
}

func KeyFuturePositionHistoryRecentIndex(status int32, closeHeight uint64, positionID string) string {
	return fmt.Sprintf("%sindex|recent|status:%s|height:%020d|position_id:%s", pfxFuturePositionHistory, FuturePositionStatusKey(status), ^closeHeight, positionID)
}

func KeyFuturePositionHistoryRecentIndexPrefix(status string) string {
	return fmt.Sprintf("%sindex|recent|status:%s|", pfxFuturePositionHistory, status)
}

                                                 

                                       
                                   
func KeyFuturePosition(positionID string) string {
	return pfxFuture + "pos_" + positionID
}

                                       
func KeyFuturePositionPrefix() string {
	return pfxFuture + "pos_"
}

                                                                               
                                                                                
                                                                 
func KeyFuturePositionIDSequence(owner, pair string, side int32) string {
	                                                                             
	                                                                           
	return fmt.Sprintf("%sposition_id_seq_%s_%s_%d", pfxFuture, owner, pair, side)
}

                                            
                                             
func KeyFutureOwnerPosition(owner, positionID string) string {
	return fmt.Sprintf("%sowner_%s_%s", pfxFuture, owner, positionID)
}

                                                        
func KeyFutureOwnerPositionPrefix(owner string) string {
	return fmt.Sprintf("%sowner_%s_", pfxFuture, owner)
}

                                                                                           
                                                        
func KeyFutureOwnerPairSidePosition(owner, pair string, side int32) string {
	return fmt.Sprintf("%sowner_pair_side_%s_%s_%d", pfxFuture, owner, pair, side)
}

                                                                   
                                               
func KeyFuturePairPosition(pair, positionID string) string {
	return fmt.Sprintf("%spair_pos_%s_%s", pfxFuture, pair, positionID)
}

                                                          
func KeyFuturePairPositionPrefix(pair string) string {
	return fmt.Sprintf("%spair_pos_%s_", pfxFuture, pair)
}

                       

                                            
                                   
func KeyFutureInsuranceFund(pair string) string {
	return pfxFuture + "insurance_" + pair
}

                                                                      
                                                                                                                                                       
                                             
func KeyFutureInsuranceShortfall(pair string) string {
	return pfxFuture + "insurance_shortfall_" + pair
}

                                           
                                    
func KeyFutureMarketConfig(pair string) string {
	return pfxFuture + "market_cfg_" + pair
}

                                                                                           
func KeyFutureMarketConfigPrefix() string {
	return pfxFuture + "market_cfg_"
}

                                                
                                     
func KeyFutureCumulativeFunding(pair string) string {
	return pfxFuture + "cum_funding_" + pair
}

                                          
                                                            
                              
func KeyFutureLiqIndex(sideTag, pair, liqPrice67, positionID string) string {
	return pfxFuture + "liq_" + sideTag + "_" + pair + "_" + liqPrice67 + "_" + positionID
}

                                                                            
func KeyFutureLiqIndexPrefix(sideTag, pair string) string {
	return pfxFuture + "liq_" + sideTag + "_" + pair + "_"
}

                                                                                                       
func KeyFutureLiqPosRef(positionID string) string {
	return pfxFuture + "liq_ref_" + positionID
}

                                                                              
                                                                          
func KeyFutureLiqIndexBackfillDone(pair string) string {
	return pfxFuture + "liq_backfill_done_" + pair
}

                                  
                                    
func KeyFutureMarkPrice(pair string) string {
	return pfxFuture + "mark_price_" + pair
}

                                                                              
                                  
func KeyFutureSpotEMA(pair string) string {
	return pfxFuture + "spot_ema_" + pair
}

                                                                                
                             
func KeyFutureEMA(pair string) string {
	return pfxFuture + "ema_" + pair
}

                                                                          
                                  
func KeySpotLastPrice(pair string) string {
	return pfxSpotLastPrice + pair
}

func KeySpotLastPricePrefix() string {
	return pfxSpotLastPrice
}

                                                                            
                                    
func KeyFutureLastPrice(pair string) string {
	return pfxFuture + "last_price_" + pair
}

                                                                      
                                      
func KeyFutureFundingRate(pair string) string {
	return pfxFuture + "funding_rate_" + pair
}

                                                              
                                        
func KeyFutureFundingHeight(pair string) string {
	return pfxFuture + "funding_height_" + pair
}

                                                  
                                                            
func KeyFutureFundingDue(height uint64, pair string) string {
	return fmt.Sprintf("%sfunding_due_%020d_%s", pfxFuture, height, pair)
}

                                                                             
func KeyFutureFundingDuePrefix(height uint64) string {
	return fmt.Sprintf("%sfunding_due_%020d_", pfxFuture, height)
}

                                                   
                                                              
func KeyFutureOrderExpiry(height uint64, orderID string) string {
	return fmt.Sprintf("%sorder_expiry_%020d_%s", pfxFuture, height, orderID)
}

                                                                              
func KeyFutureOrderExpiryPrefix(height uint64) string {
	return fmt.Sprintf("%sorder_expiry_%020d_", pfxFuture, height)
}

                                                                                            
                                                                                                   
func KeyFutureOrderReservedMargin(orderID string) string {
	return pfxFuture + "order_reserved_margin_" + orderID
}

                                                                            
                                                                                                  
                                                                            
                                     
func KeyFutureSettleMarkPrice(pair string) string {
	return pfxFuture + "settle_mark_" + pair
}

                                                
                                                             
func KeyFutureTradeRecord(pair string, timestamp int64, tradeID string) string {
	invertedTimestamp := ^uint64(timestamp)
	return fmt.Sprintf("%s%s_%020d_%s", pfxFutureTrade, pair, invertedTimestamp, tradeID)
}

                                                            
                                
func KeyFutureTradeRecordPrefix(pair string) string {
	return fmt.Sprintf("%s%s_", pfxFutureTrade, pair)
}

                                                                             
  
                                                                                

var pfxLiveness = regState("liveness_", "Miner liveness tracking state")

                                   
                                  
func KeyLivenessMiner(index uint32) string {
	return fmt.Sprintf("%sminer_%d", pfxLiveness, index)
}

                                           
                                    
func KeyLivenessCounter(index uint32) string {
	return fmt.Sprintf("%scounter_%d", pfxLiveness, index)
}

                                                                     
                                    
func KeyLivenessReport(height uint64) string {
	return fmt.Sprintf("%sreport_%020d", pfxLiveness, height)
}

                                                             
                           
func KeyLivenessGlobal() string {
	return pfxLiveness + "global"
}

                                                                                 
func KeyLivenessReportPrefix() string {
	return pfxLiveness + "report_"
}

                                    
func KeyLivenessPrefix() string {
	return pfxLiveness
}

                                                                 

var pfxTotalMinerStake = regState("total_miner_stake", "Aggregated active miner stake")

                                                                                  
                             
func KeyTotalMinerStake() string {
	return pfxTotalMinerStake
}

                                                           

var pfxMinerExit = regState("miner_exit_", "Miner exit freeze-period tracking")
var pfxMinerIdentity = regState("miner_identity_", "Miner identity uniqueness bindings")

                                                           

var pfxReferrer = regState("referrer_", "Referral binding (user → referrer)")

                                 
                               
func KeyReferrer(userAddr string) string { return pfxReferrer + userAddr }

func KeyReferrerPrefix() string { return pfxReferrer }

                                                                           
                                                                             
var pfxReferralInvitee = regKV("referral_invitee_", "Derived referral reverse query index (referrer → user)")

func KeyReferralInvitee(referrer, invitee string) string {
	return pfxReferralInvitee + referrer + "_" + invitee
}

func KeyReferralInviteePrefix(referrer string) string {
	if referrer == "" {
		return pfxReferralInvitee
	}
	return pfxReferralInvitee + referrer + "_"
}

                                                            
var pfxTradingFee = regKV("trading_fee_", "Trading fee records (local, non-consensus)")

                                                  
                                                    
func KeyTradingFeeRecord(addr string, height uint64, txID string, seq int) string {
	return fmt.Sprintf("%s%s_%020d_%s_%04d", pfxTradingFee, addr, height, txID, seq)
}

                                                                                
func KeyTradingFeePrefix(addr string) string {
	return pfxTradingFee + addr + "_"
}

                                                                            
                                                                              
                                                                  
var pfxReferralRebateRecord = regKV("referral_rebate_record_", "Referral rebate records by referrer (local, non-consensus)")

func KeyReferralRebateRecord(referrer string, height uint64, txID string, seq int) string {
	return fmt.Sprintf("%s%s_%020d_%s_%04d", pfxReferralRebateRecord, referrer, height, txID, seq)
}

func KeyReferralRebateRecordPrefix(referrer string) string {
	return pfxReferralRebateRecord + referrer + "_"
}

                                                                                
  
                                                                    

var pfxCert = regExcluded("cert_", "Checkpoint certificate chain (excluded from StateDB)")

                                                                
                                                     
func KeyCertSignature(checkpointHeight uint64, signerIndex uint32) string {
	return fmt.Sprintf("%ssig_%020d_%d", pfxCert, checkpointHeight, signerIndex)
}

                                                                 
                                        
func KeyCertSignaturePrefix(checkpointHeight uint64) string {
	return fmt.Sprintf("%ssig_%020d_", pfxCert, checkpointHeight)
}

                                                                                                                             
                                             
func KeyCertMinerSet(checkpointHeight uint64) string {
	return fmt.Sprintf("%sminer_set_%020d", pfxCert, checkpointHeight)
}

                                                              
                                               
func KeyCertCertificate(checkpointHeight uint64) string {
	return fmt.Sprintf("%scertificate_%020d", pfxCert, checkpointHeight)
}

                                                                          
                                                                               
func KeyCertCertificatePrefix() string {
	return pfxCert + "certificate_"
}

                                       
                       
func KeyCertGlobal() string {
	return pfxCert + "global"
}

                                                                                        
                        
func KeyCertPending() string {
	return pfxCert + "pending"
}

                                                                                                       
                                           
func KeyCertPendingCheckpoint(checkpointHeight uint64) string {
	return fmt.Sprintf("%spending_%020d", pfxCert, checkpointHeight)
}

                                                                  
                         
func KeyCertPendingPrefix() string {
	return pfxCert + "pending_"
}

                                 
func KeyCertPrefix() string {
	return pfxCert
}

                                                                                   
  
                                                                                                             
                                                                                           
                                                                                                    

var pfxUserAct = regKV("user_act_", "User activity records for personal center")

                                     
                                                 
func KeyUserActivity(addr string, height uint64, seq uint32) string {
	return fmt.Sprintf("%s%s_%020d_%04d", pfxUserAct, addr, height, seq)
}

                                                                               
                            
func KeyUserActivityPrefix(addr string) string {
	return pfxUserAct + addr + "_"
}

func KeyUserActivityFamilyPrefix() string {
	return pfxUserAct
}

                                                                                                
                                         
func KeyUserActivityHeightPrefix(addr string, height uint64) string {
	return fmt.Sprintf("%s%s_%020d_", pfxUserAct, addr, height)
}

                                                                          
                                                                          
                                                                          
                                                                  
func KeyUserActivityKindIndex(addr, kind string, height uint64, txID string, seq uint32) string {
	return fmt.Sprintf("%skind|owner:%s|kind:%s|height:%020d|tx_id:%s|seq:%010d", pfxUserAct, addr, kind, ^height, txID, ^seq)
}

func KeyUserActivityKindIndexPrefix(addr, kind string) string {
	return fmt.Sprintf("%skind|owner:%s|kind:%s|", pfxUserAct, addr, kind)
}
