package vm

import (
	"dex/pb"
	"errors"
	"fmt"
	"sort"
	"sync"
)

                                   
type HandlerRegistry struct {
	mu sync.RWMutex
	m  map[string]TxHandler
}

                                           
func NewHandlerRegistry() *HandlerRegistry {
	return &HandlerRegistry{m: make(map[string]TxHandler)}
}

                
func (r *HandlerRegistry) Register(h TxHandler) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if h == nil {
		return errors.New("nil handler")
	}

	kind := h.Kind()
	if kind == "" {
		return errors.New("empty handler kind")
	}

	if _, ok := r.m[kind]; ok {
		return fmt.Errorf("duplicate handler kind: %s", kind)
	}
	r.m[kind] = h
	return nil
}

                    
func (r *HandlerRegistry) Get(kind string) (TxHandler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.m[kind]
	return h, ok
}

                                             
func (r *HandlerRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	kinds := make([]string, 0, len(r.m))
	for k := range r.m {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	return kinds
}

                                                                                          
func DefaultKindFn(tx *pb.AnyTx) (string, error) {
	if tx == nil {
		return "", ErrNilTx
	}
	kind := tx.GetKind()
	if kind == "" {
		return "", fmt.Errorf("unknown tx type: %v", tx.GetTxId())
	}
	return kind, nil
}
