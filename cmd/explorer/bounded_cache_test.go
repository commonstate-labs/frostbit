package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBoundedCacheCanceledCallerDoesNotWaitForSharedFill(t *testing.T) {
	cache := newBoundedTTLCache[string](1, 1024, 1)
	started := make(chan struct{})
	release := make(chan struct{})
	loader := func(context.Context) (string, int64, error) {
		close(started)
		<-release
		return "ready", 5, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := cache.GetOrLoad(ctx, "key", time.Minute, loader)
		result <- err
	}()
	<-started
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled caller error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled caller remained blocked on shared fill")
	}

	second := make(chan struct {
		value string
		err   error
	}, 1)
	go func() {
		value, err := cache.GetOrLoad(context.Background(), "key", time.Minute, loader)
		second <- struct {
			value string
			err   error
		}{value, err}
	}()
	close(release)
	got := <-second
	if got.err != nil || got.value != "ready" {
		t.Fatalf("shared fill result = %q, %v; want ready, nil", got.value, got.err)
	}
}
