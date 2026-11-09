package vm

import "time"

                                                                       
                                                                    
func deterministicTradeTime(height uint64, seq int) (int64, string) {
	if seq < 0 {
		seq = 0
	}
	tsKey := int64(height)*1_000_000 + int64(seq)
	tsText := time.Unix(int64(height), int64(seq)*1_000).UTC().Format(time.RFC3339Nano)
	return tsKey, tsText
}
