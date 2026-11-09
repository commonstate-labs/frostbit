package logs

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

                                                                                       
                                                                                                   
var consensusTraceDefault = "0"

                                                                                     
func ConsensusTraceEnabled() bool {
	if value, set := os.LookupEnv("DEX_CONSENSUS_TRACE"); set {
		return value == "1"
	}
	return consensusTraceDefault == "1"
}

type traceLimiter struct {
	sync.Mutex
	window         time.Time
	count, dropped uint64
}

var consensusTraceLimiter traceLimiter

func (l *traceLimiter) allow(now time.Time) (bool, uint64) {
	l.Lock()
	defer l.Unlock()
	var dropped uint64
	if l.window.IsZero() || now.Sub(l.window) >= time.Second {
		dropped, l.dropped, l.count, l.window = l.dropped, 0, 0, now
	}
	if l.count >= 200 {
		l.dropped++
		return false, 0
	}
	l.count++
	return true, dropped
}

                                                                                                                           
func ConsensusTracef(format string, args ...any) {
	if !ConsensusTraceEnabled() {
		return
	}
	allowed, dropped := consensusTraceLimiter.allow(time.Now())
	if !allowed {
		return
	}
	if dropped > 0 {
		Info("[ConsensusTrace] event=suppressed count=%d", dropped)
	}
	line := fmt.Sprintf(format, args...)
	if len(line) > 4096 {
		line = line[:4096] + " truncated=true"
	}
	line = strings.NewReplacer("\n", "\\n", "\r", "\\r").Replace(line)
	Info("[ConsensusTrace] %s", line)
}
