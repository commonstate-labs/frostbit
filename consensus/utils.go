package consensus

import (
	"dex/logs"
)

                                               
               
                                               

func Logf(format string, args ...interface{}) {
	logs.Info(format, args...)
}

func minUint64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}
