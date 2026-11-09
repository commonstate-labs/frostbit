package keys

import "strings"

                                                       
type KeyCategory int

const (
	CategoryKV KeyCategory = iota
	CategoryState
	CategoryUnknown
)

                                                                  
  
                                                         
                                 
func CategorizeKey(key string) KeyCategory {
	for _, ex := range excludeFromState {
		if strings.HasPrefix(key, ex) {
			return CategoryKV
		}
	}
	for _, prefix := range statePrefixes {
		if strings.HasPrefix(key, prefix) {
			return CategoryState
		}
	}
	for _, prefix := range kvPrefixes {
		if strings.HasPrefix(key, prefix) {
			return CategoryKV
		}
	}
	return CategoryUnknown
}

                                                                     
func IsStatefulKey(key string) bool {
	return CategorizeKey(key) == CategoryState
}

                                                                            
func IsFlowKey(key string) bool {
	return CategorizeKey(key) == CategoryKV
}

func IsAccountKey(key string) bool {
	return strings.HasPrefix(key, withVer("account_")) ||
		strings.HasPrefix(key, withVer("rent_owner_marker_"))
}

func IsOrderStateKey(key string) bool {
	return strings.HasPrefix(key, withVer("orderstate_"))
}

func IsBlockKey(key string) bool {
	return strings.HasPrefix(key, withVer("blockdata_")) ||
		strings.HasPrefix(key, withVer("height_")) ||
		strings.HasPrefix(key, withVer("blockid_")) ||
		strings.HasPrefix(key, withVer("block_read_model_")) ||
		key == withVer("latest_block_height")
}

func IsTxKey(key string) bool {
	return strings.HasPrefix(key, withVer("txraw_")) ||
		strings.HasPrefix(key, withVer("pending_anytx_"))
}

func IsIndexKey(key string) bool {
	return strings.HasPrefix(key, withVer("pair:")) ||
		strings.HasPrefix(key, withVer("stakeIndex_")) ||
		strings.HasPrefix(key, withVer("indexToAccount_")) ||
		strings.HasPrefix(key, withVer("trade_"))
}

func IsHistoryKey(key string) bool {
	return strings.HasPrefix(key, withVer("transfer_history_"))
}

func IsVMKey(key string) bool {
	return strings.HasPrefix(key, withVer("vm_"))
}

func IsFrostKey(key string) bool {
	return strings.HasPrefix(key, withVer("frost_"))
}

func IsWitnessKey(key string) bool {
	return strings.HasPrefix(key, withVer("winfo_")) ||
		strings.HasPrefix(key, withVer("wvote_")) ||
		strings.HasPrefix(key, withVer("wstake_")) ||
		strings.HasPrefix(key, withVer("witness_")) ||
		strings.HasPrefix(key, withVer("challenge_")) ||
		strings.HasPrefix(key, withVer("challengecontrib_")) ||
		strings.HasPrefix(key, withVer("arbitration_vote_")) ||
		strings.HasPrefix(key, withVer("arbitration_selection_due_")) ||
		strings.HasPrefix(key, withVer("shelved_request_")) ||
		strings.HasPrefix(key, withVer("recharge_selection_due_")) ||
		strings.HasPrefix(key, withVer("recharge_vote_grace_due_")) ||
		strings.HasPrefix(key, withVer("recharge_request_"))
}
