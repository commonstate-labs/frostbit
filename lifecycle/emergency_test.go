package lifecycle

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestEmergencyStop_BasicToggle(t *testing.T) {
	e := New()
	if e.IsStopped() {
		t.Fatal("new EmergencyStop should not be stopped")
	}
	e.Stop("manual")
	if !e.IsStopped() {
		t.Fatal("Stop() should engage")
	}
	if e.Reason() != "manual" {
		t.Fatalf("Reason() = %q want %q", e.Reason(), "manual")
	}
	if e.StoppedAt().IsZero() {
		t.Fatal("StoppedAt should be set")
	}
	e.Resume()
	if e.IsStopped() {
		t.Fatal("Resume() should clear flag")
	}
	if e.Reason() != "" || !e.StoppedAt().IsZero() {
		t.Fatal("Resume() should clear reason/stoppedAt")
	}
}

func TestEmergencyStop_StopIdempotent(t *testing.T) {
	e := New()
	e.Stop("first")
	first := e.StoppedAt()
	time.Sleep(2 * time.Millisecond)
	e.Stop("second")                      
	if e.Reason() != "first" {
		t.Fatalf("reason overwritten: got %q", e.Reason())
	}
	if !e.StoppedAt().Equal(first) {
		t.Fatal("stoppedAt overwritten on duplicate Stop")
	}
}

func TestEmergencyStop_NilSafe(t *testing.T) {
	var e *EmergencyStop
	if e.IsStopped() {
		t.Fatal("nil IsStopped should be false")
	}
	e.Stop("x")             
	e.Resume()
	if e.Reason() != "" {
		t.Fatal("nil Reason should be empty")
	}
	if !e.StoppedAt().IsZero() {
		t.Fatal("nil StoppedAt should be zero")
	}
}

func TestEmergencyStop_Concurrent(t *testing.T) {
	e := New()
	const N = 200
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			if i%3 == 0 {
				e.Stop("race")
			} else {
				e.Resume()
			}
		}(i)
		go func() {
			defer wg.Done()
			_ = e.IsStopped()
			_ = e.Reason()
			_ = e.StoppedAt()
		}()
	}
	wg.Wait()
	                                      
	e.Stop("final")
	if !e.IsStopped() {
		t.Fatal("after final Stop should be stopped")
	}
}

func TestErrEmergencyStop_Sentinel(t *testing.T) {
	if !errors.Is(ErrEmergencyStop, ErrEmergencyStop) {
		t.Fatal("ErrEmergencyStop should be its own sentinel")
	}
}
