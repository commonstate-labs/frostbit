                                                                      
  
                                                                                                    
                                                                             
                                                                             
  
                  
                                                                 
                                                                    
                                                                                                 
                                                                             
package lifecycle

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

                                                                             
var ErrEmergencyStop = errors.New("emergency stop engaged")

                                                              
type EmergencyStop struct {
	flag      atomic.Bool
	mu        sync.RWMutex
	reason    string
	stoppedAt time.Time
}

                                    
func New() *EmergencyStop { return &EmergencyStop{} }

                                                                                                      
                               
func (e *EmergencyStop) Stop(reason string) {
	if e == nil {
		return
	}
	if e.flag.CompareAndSwap(false, true) {
		e.mu.Lock()
		e.reason = reason
		e.stoppedAt = time.Now()
		e.mu.Unlock()
	}
}

                                                      
                                                                            
func (e *EmergencyStop) Resume() {
	if e == nil {
		return
	}
	if e.flag.CompareAndSwap(true, false) {
		e.mu.Lock()
		e.reason = ""
		e.stoppedAt = time.Time{}
		e.mu.Unlock()
	}
}

                                                                                                  
func (e *EmergencyStop) IsStopped() bool {
	if e == nil {
		return false
	}
	return e.flag.Load()
}

                                              
func (e *EmergencyStop) Reason() string {
	if e == nil {
		return ""
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.reason
}

                                                    
func (e *EmergencyStop) StoppedAt() time.Time {
	if e == nil {
		return time.Time{}
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.stoppedAt
}
