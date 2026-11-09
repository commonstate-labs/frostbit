package testharness

import "fmt"

                                                                                 
                                                                               
                                                                                 
func (h *Harness) SeedHeightForTest(height uint64) {
	h.t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.height != 0 || h.blockSeq != 0 || len(h.queue) != 0 || len(h.txLog) != 0 {
		h.t.Fatal("height checkpoint fixture requires a fresh, unqueued Harness")
	}
	if height == ^uint64(0) {
		h.t.Fatal("height checkpoint fixture leaves no room for a boundary block")
	}
	if err := h.setLatestHeightLocked(height); err != nil {
		h.t.Fatalf("seed checkpoint height: %v", err)
	}
	h.height = height
	h.prevBlockHash = fmt.Sprintf("harness_fixture_checkpoint_%020d", height)
}
