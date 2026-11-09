package workers

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"dex/lifecycle"
	"dex/logs"
)

                                                                
                                                                                         
                                                                                                        
                                                       
  
                                                                                                         
                                                                                                         

type countingStateReader struct {
	scanCount atomic.Int64
	getCount  atomic.Int64
	getErr    error
}

func (r *countingStateReader) Get(key string) ([]byte, bool, error) {
	r.getCount.Add(1)
	return nil, false, r.getErr
}

func (r *countingStateReader) Scan(prefix string, fn func(k string, v []byte) bool) error {
	r.scanCount.Add(1)
	return nil
}

func TestWithdrawWorker_ProcessWindow_ShortCircuitsOnEmergency(t *testing.T) {
	reader := &countingStateReader{getErr: errors.New("worker should not have called stateReader under emergency")}
	w := NewWithdrawWorker(reader, nil, nil, nil, nil, nil, 1, "self", nil, logs.NewNodeLogger("ww-emergency", 0))

	em := lifecycle.New()
	w.SetEmergencyStop(em)
	em.Stop("p1.7")

	jobs, err := w.ProcessWindow(context.Background(), "btc", "BTC")
	if err != nil {
		t.Fatalf("ProcessWindow under emergency must return nil error, got %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("ProcessWindow under emergency must produce zero jobs, got %d", len(jobs))
	}
	if reader.getCount.Load() != 0 || reader.scanCount.Load() != 0 {
		t.Fatalf("stateReader must not be touched under emergency: get=%d scan=%d",
			reader.getCount.Load(), reader.scanCount.Load())
	}
}

func TestWithdrawWorker_ProcessWindow_AllowsAfterResume(t *testing.T) {
	reader := &countingStateReader{}                                     
	w := NewWithdrawWorker(reader, nil, nil, nil, nil, nil, 1, "self", nil, logs.NewNodeLogger("ww-emergency", 0))

	em := lifecycle.New()
	w.SetEmergencyStop(em)

	em.Stop("temp")
	em.Resume()

	                                                                                               
	if _, err := w.ProcessWindow(context.Background(), "btc", "BTC"); err != nil {
		t.Fatalf("ProcessWindow after resume: %v", err)
	}
	if reader.scanCount.Load() == 0 && reader.getCount.Load() == 0 {
		t.Fatal("after Resume, stateReader must be touched by planner (no short-circuit)")
	}
}

                                                                           
func TestWithdrawWorker_NilEmergencyStop_DoesNotPanic(t *testing.T) {
	reader := &countingStateReader{}
	w := NewWithdrawWorker(reader, nil, nil, nil, nil, nil, 1, "self", nil, logs.NewNodeLogger("ww-emergency", 0))
	                                                                 
	if _, err := w.ProcessWindow(context.Background(), "btc", "BTC"); err != nil {
		t.Fatalf("nil EmergencyStop must not error: %v", err)
	}
	if reader.scanCount.Load() == 0 && reader.getCount.Load() == 0 {
		t.Fatal("nil EmergencyStop must NOT short-circuit; planner should run")
	}
}

func TestTransitionWorker_StartPendingSessions_ShortCircuitsOnEmergency(t *testing.T) {
	reader := &countingStateReader{getErr: errors.New("transition worker should not touch stateReader under emergency")}
	w := &TransitionWorker{
		stateReader: reader,
		Logger:      logs.NewNodeLogger("tw-emergency", 0),
	}
	em := lifecycle.New()
	w.SetEmergencyStop(em)
	em.Stop("p1.7")

	if err := w.StartPendingSessions(context.Background()); err != nil {
		t.Fatalf("StartPendingSessions under emergency must return nil, got %v", err)
	}
	if reader.scanCount.Load() != 0 || reader.getCount.Load() != 0 {
		t.Fatalf("stateReader must not be touched under emergency: get=%d scan=%d",
			reader.getCount.Load(), reader.scanCount.Load())
	}
}

func TestTransitionWorker_StartPendingSessions_AllowsAfterResume(t *testing.T) {
	reader := &countingStateReader{}
	w := &TransitionWorker{
		stateReader: reader,
		Logger:      logs.NewNodeLogger("tw-emergency", 0),
	}
	em := lifecycle.New()
	w.SetEmergencyStop(em)

	em.Stop("temp")
	em.Resume()

	if err := w.StartPendingSessions(context.Background()); err != nil {
		t.Fatalf("StartPendingSessions after resume: %v", err)
	}
	if reader.scanCount.Load() == 0 && reader.getCount.Load() == 0 {
		t.Fatal("after Resume, stateReader must be reachable")
	}
}
