package vm

import (
	"container/list"
	"sync"
)

                                        

type lruCache struct {
	mu    sync.RWMutex
	cap   int
	ll    *list.List
	items map[string]*list.Element
}

type lruItem struct {
	key string
	val *SpecResult
}

                                 
func NewSpecExecLRU(capacity int) SpecExecCache {
	if capacity <= 0 {
		capacity = 1024
	}
	return &lruCache{
		cap:   capacity,
		ll:    list.New(),
		items: make(map[string]*list.Element, capacity),
	}
}

                      
func (c *lruCache) Get(key string) (*SpecResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if e, ok := c.items[key]; ok {
		c.ll.MoveToFront(e)
		return e.Value.(*lruItem).val, true
	}
	return nil, false
}

                      
func (c *lruCache) Put(res *SpecResult) {
	if res == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	                                          
	if e, ok := c.items[res.BlockID]; ok {
		e.Value.(*lruItem).val = res
		c.ll.MoveToFront(e)
		return
	}

	               
	e := c.ll.PushFront(&lruItem{key: res.BlockID, val: res})
	c.items[res.BlockID] = e

	                                                   
	if c.ll.Len() > c.cap {
		last := c.ll.Back()
		if last != nil {
			it := last.Value.(*lruItem)
			delete(c.items, it.key)
			c.ll.Remove(last)
		}
	}
}

                                                  
func (c *lruCache) EvictBelow(height uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	                                                      
	toDelete := make([]string, 0)
	for k, e := range c.items {
		if e.Value.(*lruItem).val.Height < height {
			toDelete = append(toDelete, k)
		}
	}

	               
	for _, k := range toDelete {
		if e, ok := c.items[k]; ok {
			delete(c.items, k)
			c.ll.Remove(e)
		}
	}
}

                          
func (c *lruCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ll.Len()
}

                     
func (c *lruCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items = make(map[string]*list.Element, c.cap)
	c.ll.Init()
}
