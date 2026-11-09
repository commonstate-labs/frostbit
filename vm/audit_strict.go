package vm

import (
	"os"
	"sync/atomic"
)

                         
                                                                                                  
                                                                                 
var strictAuditEnabled atomic.Bool

func init() {
	                                                                                                         
	if v := os.Getenv("DEX_STRICT_AUDIT"); v == "1" || v == "true" || v == "TRUE" {
		strictAuditEnabled.Store(true)
	}
}

                                                                                                                
func SetStrictAudit(on bool) { strictAuditEnabled.Store(on) }

                                                                                    
func StrictAuditEnabled() bool { return strictAuditEnabled.Load() }
