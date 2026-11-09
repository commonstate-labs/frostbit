package main

import (
	"context"
	"errors"
	"sync"
	"time"
)

var errExplorerCacheBusy = errors.New("explorer cache loader budget exhausted")

                                                                             
                                                                              
                                                                              
                                                         
type boundedTTLCache[T any] struct {
	mu sync.Mutex

	entries  map[string]*boundedCacheEntry[T]
	inflight map[string]*boundedCacheLoad[T]

	maxEntries  int
	maxBytes    int64
	usedBytes   int64
	maxInflight int
}

type boundedCacheEntry[T any] struct {
	value      T
	expiresAt  time.Time
	lastAccess time.Time
	bytes      int64
}

type boundedCacheLoad[T any] struct {
	done  chan struct{}
	value T
	err   error
}

func newBoundedTTLCache[T any](maxEntries int, maxBytes int64, maxInflight int) *boundedTTLCache[T] {
	if maxEntries <= 0 {
		maxEntries = 1
	}
	if maxBytes <= 0 {
		maxBytes = 1
	}
	if maxInflight <= 0 {
		maxInflight = 1
	}
	return &boundedTTLCache[T]{
		entries:     make(map[string]*boundedCacheEntry[T]),
		inflight:    make(map[string]*boundedCacheLoad[T]),
		maxEntries:  maxEntries,
		maxBytes:    maxBytes,
		maxInflight: maxInflight,
	}
}

func (c *boundedTTLCache[T]) Get(key string) (T, bool) {
	var zero T
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.purgeExpiredLocked(now)
	entry, ok := c.entries[key]
	if !ok {
		return zero, false
	}
	entry.lastAccess = now
	return entry.value, true
}

func (c *boundedTTLCache[T]) GetOrLoad(ctx context.Context, key string, ttl time.Duration, loader func(context.Context) (T, int64, error)) (T, error) {
	var zero T
	if ctx == nil {
		ctx = context.Background()
	}
	if value, ok := c.Get(key); ok {
		return value, nil
	}

	c.mu.Lock()
	if current, ok := c.entries[key]; ok {
		now := time.Now()
		if now.Before(current.expiresAt) {
			current.lastAccess = now
			value := current.value
			c.mu.Unlock()
			return value, nil
		}
		delete(c.entries, key)
		c.usedBytes -= current.bytes
	}
	if call, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		select {
		case <-call.done:
			return call.value, call.err
		case <-ctx.Done():
			return zero, ctx.Err()
		}
	}
	if len(c.inflight) >= c.maxInflight {
		c.mu.Unlock()
		return zero, errExplorerCacheBusy
	}
	call := &boundedCacheLoad[T]{done: make(chan struct{})}
	c.inflight[key] = call
	c.mu.Unlock()

	                                                                          
	                                                                          
	                                                                          
	                                                          
	go func() {
		value, weight, err := loader(ctx)
		if weight < 1 {
			weight = 1
		}

		c.mu.Lock()
		if err == nil && weight <= c.maxBytes {
			now := time.Now()
			c.purgeExpiredLocked(now)
			c.evictForLocked(weight)
			if len(c.entries) < c.maxEntries && c.usedBytes+weight <= c.maxBytes {
				c.entries[key] = &boundedCacheEntry[T]{
					value:      value,
					expiresAt:  now.Add(ttl),
					lastAccess: now,
					bytes:      weight,
				}
				c.usedBytes += weight
			}
		}
		call.value = value
		call.err = err
		delete(c.inflight, key)
		close(call.done)
		c.mu.Unlock()
	}()

	select {
	case <-call.done:
		return call.value, call.err
	case <-ctx.Done():
		return zero, ctx.Err()
	}
}

func (c *boundedTTLCache[T]) purgeExpiredLocked(now time.Time) {
	for key, entry := range c.entries {
		if !now.Before(entry.expiresAt) {
			delete(c.entries, key)
			c.usedBytes -= entry.bytes
		}
	}
}

func (c *boundedTTLCache[T]) evictForLocked(incoming int64) {
	for len(c.entries) >= c.maxEntries || c.usedBytes+incoming > c.maxBytes {
		var oldestKey string
		var oldest time.Time
		for key, entry := range c.entries {
			if oldestKey == "" || entry.lastAccess.Before(oldest) {
				oldestKey = key
				oldest = entry.lastAccess
			}
		}
		if oldestKey == "" {
			return
		}
		entry := c.entries[oldestKey]
		delete(c.entries, oldestKey)
		c.usedBytes -= entry.bytes
	}
}

func (c *boundedTTLCache[T]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.purgeExpiredLocked(time.Now())
	return len(c.entries)
}
