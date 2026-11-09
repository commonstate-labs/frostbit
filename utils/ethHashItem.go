package utils

import (
	"dex/logs"
	"encoding/hex"
	"github.com/dchest/siphash"
)

                                                                                 
type EthHashItem [32]byte

                                                            
func (t EthHashItem) XOR(t2 EthHashItem) EthHashItem {
	var res EthHashItem
	for i := 0; i < 32; i++ {
		res[i] = t[i] ^ t2[i]
	}
	return res
}

                                                     
func (t EthHashItem) Hash() uint64 {
	return siphash.Hash(0x12345678, 0x87654321, t[:])
}

                                                                    
func (t EthHashItem) String() string {
	return "0x" + hex.EncodeToString(t[:])
}

                                                                                 
func ConvertTxIdToEthItem(txID string) (EthHashItem, bool) {
	var res EthHashItem
	decoded, err := hex.DecodeString(stripHexPrefix(txID))                                                
	if err != nil {
		logs.Error("hex.DecodeString failed")
		return res, false
	}
	if len(decoded) != 32 {
		                                                       
		return res, false
	}
	copy(res[:], decoded)
	return res, true
}
func stripHexPrefix(s string) string {
	if len(s) > 2 && (s[:2] == "0x" || s[:2] == "0X") {
		return s[2:]
	}
	return s
}
