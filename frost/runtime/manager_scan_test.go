package runtime

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"dex/logs"
)

type countingFrostScanReader struct {
	*fakeStateReader
	stateReads atomic.Int64
}

func (r *countingFrostScanReader) Get(key string) ([]byte, bool, error) {
	r.stateReads.Add(1)
	return r.fakeStateReader.Get(key)
}

func TestManager_FinalizedBlockTriggersScan(t *testing.T) {
	notifier := NewFakeNotifier()
	reader := &countingFrostScanReader{fakeStateReader: newFakeStateReader()}
	factory := newFakeAdapterFactory()
	factory.Register("eth")

	m := NewManager(
		ManagerConfig{
			NodeID:       "test-node-finalized-scan",
			ScanInterval: time.Hour,
			SupportedChains: []ChainAssetPair{
				{Chain: "eth", Asset: "ETH"},
			},
		},
		ManagerDeps{
			Notifier:       notifier,
			Logger:         logs.NewNodeLogger("test-finalized-scan", 0),
			StateReader:    reader,
			VaultProvider:  newFakeVaultProvider(),
			SignerProvider: newFakeVaultProvider(),
			TxSubmitter:    newFakeTxSubmitter(),
			AdapterFactory: factory,
		},
	)
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer m.Stop()

	before := reader.stateReads.Load()
	notifier.TriggerFinalized(1)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		                                                                          
		                                                                         
		if reader.stateReads.Load() > before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("finalized block did not trigger claimed-job recovery after queued scan")
}
