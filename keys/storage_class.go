package keys

import "strings"

                                                                     
                                                                             
                                                                            
                                          
type StorageClass int

const (
	StorageRuntime StorageClass = iota
	StorageQuery
	StorageArchive
)

func (c StorageClass) String() string {
	switch c {
	case StorageRuntime:
		return "runtime"
	case StorageQuery:
		return "query"
	case StorageArchive:
		return "archive"
	default:
		return "runtime"
	}
}

                                                                         
                                                          
func StorageClassForKey(key string) StorageClass {
	switch {
	case hasAnyPrefix(key,
		withVer("query_prune_manifest_h_"),
		withVer("trade_"),
		withVer("future_trade_"),
		withVer("trading_fee_"),
		withVer("referral_rebate_record_"),
		withVer("referral_invitee_"),
		withVer("recharge_query_recent_"),
		withVer("recharge_reward_owner_"),
		withVer("witness_assigned_query_"),
		withVer("frost_withdraw_query_"),
		withVer("user_act_"),
		withVer("spot_acc_order_"),
		withVer("future_acc_order_"),
		withVer("future_pos_history_"),
		withVer("vm_tx_error_"),
		withVer("vm_tx_height_"),
		withVer("vm_tx_fee_"),
		withVer("vmtx_fbbal_"),
		withVer("transfer_history_"),
		withVer("block_reward_"),
		withVer("frost_planning_log_"),
	):
		return StorageQuery
	case hasAnyPrefix(key,
		withVer("archive_recharge_request_"),
		withVer("archive_witness_vote_"),
		withVer("archive_challenge_"),
		withVer("archive_challengecontrib_"),
		withVer("archive_frost_withdraw_"),
		withVer("archive_frost_withdrawref_"),
		withVer("archive_frost_done_withdraw_"),
		withVer("archive_frost_job_"),
		withVer("archive_frost_signed_pkg_"),
		withVer("archive_frost_migration_"),
		withVer("archive_frost_artifact_"),
		withVer("archive_frost_artifact_h_"),
		withVer("blockdata_"),
		withVer("height_"),
		withVer("blockid_"),
		withVer("txraw_"),
	):
		return StorageArchive
	default:
		return StorageRuntime
	}
}

                                                                          
                                                                            
                                                                
func IsPrunableQueryHistoryKey(key string) bool {
	switch {
	case strings.HasPrefix(key, pfxUserAct),
		strings.HasPrefix(key, pfxRechargeRewardOwner),
		strings.HasPrefix(key, pfxTrade),
		strings.HasPrefix(key, pfxFutureTrade),
		strings.HasPrefix(key, pfxFuturePositionHistory),
		strings.HasPrefix(key, pfxTradingFee),
		strings.HasPrefix(key, pfxReferralRebateRecord):
		return true
	case strings.HasPrefix(key, pfxFrostWithdrawQuery):
		                                                                       
		                                                 
		return !strings.HasSuffix(key, "|kind:active")
	case strings.HasPrefix(key, pfxRechargeQuery):
		return strings.Contains(key, "|kind:terminal|")
	case strings.HasPrefix(key, pfxSpotAccountOrder),
		strings.HasPrefix(key, pfxFutureAccountOrder):
		return strings.Contains(key, "|status:") && !strings.Contains(key, "|status:open|")
	default:
		return false
	}
}

                                                                               
                                                                               
                                                                      
func PrunableArchiveHistoryPrefix(key string) string {
	for _, prefix := range []string{pfxArchiveRechargeRequest, pfxArchiveWitnessVote, pfxArchiveChallenge, pfxArchiveChallengeContrib} {
		if strings.HasPrefix(key, prefix) {
			return prefix
		}
	}
	return ""
}

func hasAnyPrefix(key string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}
