                                                                                     
                                                                                 
                                                                           
  
                                                                                  
                                                                                       
package peerrep

import (
	"container/list"
	"sync"
	"time"
)

                                                                   
                                                                 
type Tracker struct {
	mu         sync.Mutex
	failCount  map[string]int
	banUntil   map[string]time.Time
	lruEntries map[string]*list.Element
	lru        *list.List
	maxEntries int
	threshold  int
	duration   time.Duration
	now        func() time.Time
	onBan      func(key, reason string, count int)
}

                                                                                    
func New(threshold int, duration time.Duration) *Tracker {
	return &Tracker{
		failCount:  make(map[string]int),
		banUntil:   make(map[string]time.Time),
		lruEntries: make(map[string]*list.Element),
		lru:        list.New(),
		threshold:  threshold,
		duration:   duration,
		now:        time.Now,
	}
}

                                                                           
                                                                              
                                                                            
                                                   
func (t *Tracker) SetMaxEntries(max int) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.maxEntries = max
	t.trimLocked()
	t.mu.Unlock()
}

                                           
func (t *Tracker) SetNow(fn func() time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if fn != nil {
		t.now = fn
	}
}

                                                                                                        
                              
func (t *Tracker) SetOnBan(fn func(key, reason string, count int)) {
	t.mu.Lock()
	t.onBan = fn
	t.mu.Unlock()
}

                                                                             
                                                     
func (t *Tracker) RecordFailure(key, reason string) (banned bool, count int) {
	if t == nil || key == "" || t.threshold <= 0 {
		return false, 0
	}
	t.mu.Lock()
	t.touchLocked(key)
	t.failCount[key]++
	count = t.failCount[key]
	if count >= t.threshold {
		t.banUntil[key] = t.now().Add(t.duration)
		delete(t.failCount, key)
		banned = true
	}
	cb := t.onBan
	t.mu.Unlock()
	if banned && cb != nil {
		cb(key, reason, count)
	}
	return banned, count
}

                                                                                                    
func (t *Tracker) Reset(key string) {
	if t == nil || key == "" {
		return
	}
	t.mu.Lock()
	delete(t.failCount, key)
	if _, banned := t.banUntil[key]; !banned {
		t.removeTrackingLocked(key)
	}
	t.mu.Unlock()
}

                                                                                              
func (t *Tracker) Forget(key string) {
	if key == "" {
		return
	}
	t.mu.Lock()
	delete(t.failCount, key)
	delete(t.banUntil, key)
	t.removeTrackingLocked(key)
	t.mu.Unlock()
}

                                                                                
func (t *Tracker) IsBanned(key string) bool {
	if t == nil || key == "" {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.isBannedLocked(key)
}

func (t *Tracker) isBannedLocked(key string) bool {
	until, ok := t.banUntil[key]
	if !ok {
		return false
	}
	if t.now().After(until) {
		delete(t.banUntil, key)
		if _, failing := t.failCount[key]; !failing {
			t.removeTrackingLocked(key)
		}
		return false
	}
	return true
}

                                                                                              
func (t *Tracker) Filter(keys []string) []string {
	if len(keys) == 0 {
		return keys
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if k == "" {
			continue
		}
		if t.isBannedLocked(k) {
			continue
		}
		out = append(out, k)
	}
	return out
}

                                                                           
func (t *Tracker) Snapshot() (banned int, tracking int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	for _, until := range t.banUntil {
		if now.Before(until) {
			banned++
		}
	}
	tracking = len(t.lruEntries)
	return
}

func (t *Tracker) touchLocked(key string) {
	if key == "" {
		return
	}
	if t.lru == nil {
		t.lru = list.New()
	}
	if t.lruEntries == nil {
		t.lruEntries = make(map[string]*list.Element)
	}
	if entry := t.lruEntries[key]; entry != nil {
		t.lru.MoveToBack(entry)
	} else {
		t.lruEntries[key] = t.lru.PushBack(key)
	}
	t.trimLocked()
}

func (t *Tracker) trimLocked() {
	if t.maxEntries <= 0 {
		return
	}
	for len(t.lruEntries) > t.maxEntries {
		if t.lru == nil {
			return
		}
		oldest := t.lru.Front()
		if oldest == nil {
			return
		}
		oldestKey, ok := oldest.Value.(string)
		if !ok {
			t.lru.Remove(oldest)
			continue
		}
		t.removeTrackingLocked(oldestKey)
		delete(t.failCount, oldestKey)
		delete(t.banUntil, oldestKey)
	}
}

func (t *Tracker) removeTrackingLocked(key string) {
	entry := t.lruEntries[key]
	if entry != nil && t.lru != nil {
		t.lru.Remove(entry)
	}
	delete(t.lruEntries, key)
}
