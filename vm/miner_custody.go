package vm

import (
	"dex/keys"
	"dex/pb"
)

                                                                           
                                                                             
                                                                              
                                                                            
func RewardAddressOf(sv StateView, hot string) string {
	if hot == "" {
		return hot
	}
	data, ok, err := sv.Get(keys.KeyAccount(hot))
	if err != nil || !ok || len(data) == 0 {
		return hot
	}
	var acct pb.Account
	if unmarshalProtoCompat(data, &acct) != nil {
		return hot
	}
	if acct.RewardAddress != "" {
		return acct.RewardAddress
	}
	return hot
}

                                                                                                                                           
func ResolveMinerHot(tx *pb.MinerTx) string {
	if tx == nil {
		return ""
	}
	if tx.MinerAddress != "" {
		return tx.MinerAddress
	}
	if tx.Base != nil {
		return tx.Base.FromAddress
	}
	return ""
}
