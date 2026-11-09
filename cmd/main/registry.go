package main

import (
	"fmt"
	"sort"
	"sync"
)

                                                                  
                                     
type NodeRegistry struct {
	mu        sync.RWMutex
	launchers map[int]*NodeLauncher
	byAddr    map[string]*NodeLauncher
}

                                                  
func NewNodeRegistry() *NodeRegistry {
	return &NodeRegistry{
		launchers: make(map[int]*NodeLauncher),
		byAddr:    make(map[string]*NodeLauncher),
	}
}

                                            
func (r *NodeRegistry) Register(nl *NodeLauncher) {
	if nl == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.launchers[nl.config.ID] = nl
	if nl.instance != nil && nl.instance.Address != "" {
		r.byAddr[nl.instance.Address] = nl
	}
}

                                                              
func (r *NodeRegistry) Unregister(id int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if nl, ok := r.launchers[id]; ok {
		if nl.instance != nil {
			delete(r.byAddr, nl.instance.Address)
		}
		delete(r.launchers, id)
	}
}

                                                                     
func (r *NodeRegistry) Get(id int) *NodeLauncher {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.launchers[id]
}

                                                                                   
func (r *NodeRegistry) GetByAddress(addr string) *NodeLauncher {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byAddr[addr]
}

                                           
func (r *NodeRegistry) All() []*NodeLauncher {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ids := make([]int, 0, len(r.launchers))
	for id := range r.launchers {
		ids = append(ids, id)
	}
	sort.Ints(ids)

	result := make([]*NodeLauncher, 0, len(r.launchers))
	for _, id := range ids {
		if nl := r.launchers[id]; nl != nil {
			result = append(result, nl)
		}
	}
	return result
}

                                                                
                                                                           
func (r *NodeRegistry) AllInstances() []*NodeInstance {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ids := make([]int, 0, len(r.launchers))
	for id := range r.launchers {
		ids = append(ids, id)
	}
	sort.Ints(ids)

	result := make([]*NodeInstance, 0, len(r.launchers))
	for _, id := range ids {
		if nl := r.launchers[id]; nl != nil && nl.instance != nil {
			result = append(result, nl.instance)
		}
	}
	return result
}

                                                    
func (r *NodeRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.launchers)
}

                                            
func (r *NodeRegistry) String() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return fmt.Sprintf("NodeRegistry{count=%d}", len(r.launchers))
}
