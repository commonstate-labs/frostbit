package consensus

import (
	"testing"
	"time"
)

                                                                                                       
                                                                                                    
                                                                                
                                                                       
                                                                                           
                                                        
  
                                                                                              
                                                                                                
                                                                                                     
                                                                      

func newTestBuffer() *PendingBlockBuffer {
	                                                                                                    
	                                                              
	return NewPendingBlockBuffer(nil, nil, nil, nil)
}

                              
                                        
                                        
                                         
                                                                            
                                
                                                                                                      
func TestPendingBlockBuffer_UpdateRetry_ExponentialBackoffThenCapped(t *testing.T) {
	b := newTestBuffer()
	entry := &pendingBlockEntry{
		blockID:    "blk_backoff",
		retryCount: 0,
		maxRetries: defaultMaxRetries,
	}
	b.pending[entry.blockID] = entry

	expectDelays := []time.Duration{
		400 * time.Millisecond,
		800 * time.Millisecond,
		1600 * time.Millisecond,
		maxRetryDelay,                 
		maxRetryDelay,                      
	}

	for i, want := range expectDelays {
		before := time.Now()
		b.updateRetry(entry, nil)
		gotDelay := entry.nextRetry.Sub(before)
		                           
		low := want - 50*time.Millisecond
		high := want + 50*time.Millisecond
		if gotDelay < low || gotDelay > high {
			t.Fatalf("第%d次退避：delay 应≈%v，实得 %v（retryCount=%d）",
				i+1, want, gotDelay, entry.retryCount)
		}
	}
}

                                                                                           
func TestPendingBlockBuffer_UpdateRetry_IncrementsCountAndMissing(t *testing.T) {
	b := newTestBuffer()
	entry := &pendingBlockEntry{blockID: "blk_count", maxRetries: 10}
	b.pending[entry.blockID] = entry

	for i := 1; i <= 5; i++ {
		b.updateRetry(entry, [][]byte{{byte(i)}})
		if entry.retryCount != i {
			t.Fatalf("第%d次 updateRetry 后 retryCount 应=%d，实得 %d", i, i, entry.retryCount)
		}
		if len(entry.missingHashes) != 1 || entry.missingHashes[0][0] != byte(i) {
			t.Fatalf("第%d次 updateRetry 应写入新 missingHashes，实得 %v", i, entry.missingHashes)
		}
	}
}

                                                                                  
func TestPendingBlockBuffer_AddPendingBlock_Idempotent(t *testing.T) {
	b := newTestBuffer()
	blockID := "blk_dup"

	b.AddPendingBlock([]byte("short"), 10, blockID, "peer_a", [][]byte{{0x01}}, nil)
	if n := b.GetPendingCount(); n != 1 {
		t.Fatalf("首次 Add 后 pending 应=1，实得 %d", n)
	}
	if !b.HasPending(blockID) {
		t.Fatal("HasPending 应为 true")
	}

	                                                                            
	b.AddPendingBlock([]byte("should_not_overwrite"), 11, blockID, "peer_b", [][]byte{{0x02}}, nil)
	if n := b.GetPendingCount(); n != 1 {
		t.Fatalf("重复 Add 后 pending 应保持 1，实得 %d", n)
	}

	                                                   
	b.mu.RLock()
	e := b.pending[blockID]
	b.mu.RUnlock()
	if e.height != 10 {
		t.Fatalf("重复 Add 不应覆盖 height：want=10 got=%d", e.height)
	}
	if string(e.shortTxs) != "short" {
		t.Fatalf("重复 Add 不应覆盖 shortTxs：got=%q", string(e.shortTxs))
	}
}

                                                                                           
func TestPendingBlockBuffer_TriggerRetry_SetsNextRetryToNow(t *testing.T) {
	b := newTestBuffer()
	blockID := "blk_trigger"
	future := time.Now().Add(10 * time.Second)
	b.pending[blockID] = &pendingBlockEntry{
		blockID:   blockID,
		nextRetry: future,
	}

	b.triggerRetry(blockID)

	b.mu.RLock()
	got := b.pending[blockID].nextRetry
	b.mu.RUnlock()

	if !got.Before(future) {
		t.Fatalf("triggerRetry 应把 nextRetry 拉到 now 之前 %v，实得 %v", future, got)
	}
	if time.Since(got) > time.Second {
		t.Fatalf("triggerRetry 应接近 now，实得距 now %v", time.Since(got))
	}
}

                                                                            
func TestPendingBlockBuffer_TriggerRetry_UnknownBlockNoop(t *testing.T) {
	b := newTestBuffer()
	b.triggerRetry("does_not_exist")                
	if n := b.GetPendingCount(); n != 0 {
		t.Fatalf("未知 blockID triggerRetry 不应创建条目，实得 count=%d", n)
	}
}

                                                                                                
                                                                                                                  
func TestPendingBlockBuffer_ProcessRetries_EvictsOnMaxRetries(t *testing.T) {
	b := newTestBuffer()
	past := time.Now().Add(-1 * time.Second)
	future := time.Now().Add(10 * time.Second)

	b.pending["evict_me"] = &pendingBlockEntry{
		blockID:    "evict_me",
		retryCount: defaultMaxRetries,                
		maxRetries: defaultMaxRetries,
		nextRetry:  past,             
	}
	b.pending["keep_me"] = &pendingBlockEntry{
		blockID:    "keep_me",
		retryCount: 0,
		maxRetries: defaultMaxRetries,
		nextRetry:  future,             
	}

	b.processRetries()

	if b.HasPending("evict_me") {
		t.Fatal("到达 maxRetries 且过期的 entry 必须被驱逐")
	}
	if !b.HasPending("keep_me") {
		t.Fatal("未到期的 entry 不应被驱逐")
	}
}

                                                                                                 
                                                                                                                   
                             
  
                                                                                                      
                                                                                                                    
                                                                                                                              
                                                                                                                        
                                                                                               
                                                         

                                                       
func TestPendingBlockBuffer_HasPendingAndCount_TrackMap(t *testing.T) {
	b := newTestBuffer()
	if b.GetPendingCount() != 0 || b.HasPending("x") {
		t.Fatal("空 buffer 的计数/HasPending 应为 0/false")
	}
	b.pending["a"] = &pendingBlockEntry{blockID: "a"}
	b.pending["b"] = &pendingBlockEntry{blockID: "b"}
	if got := b.GetPendingCount(); got != 2 {
		t.Fatalf("count 应为 2，实得 %d", got)
	}
	if !b.HasPending("a") || !b.HasPending("b") || b.HasPending("c") {
		t.Fatal("HasPending 行为不符")
	}
	delete(b.pending, "a")
	if got := b.GetPendingCount(); got != 1 {
		t.Fatalf("删 a 后 count 应=1，实得 %d", got)
	}
}

                                                                                       
                                                            
func TestPendingBlockBuffer_GetPendingBlock_NilAndMissing(t *testing.T) {
	var nilB *PendingBlockBuffer
	if blk, sh := nilB.GetPendingBlock("x"); blk != nil || sh != nil {
		t.Fatalf("nil buffer GetPendingBlock 应返回 nil,nil 实得 %v,%v", blk, sh)
	}

	b := newTestBuffer()
	if blk, sh := b.GetPendingBlock("missing"); blk != nil || sh != nil {
		t.Fatalf("缺失 blockID GetPendingBlock 应返回 nil,nil 实得 %v,%v", blk, sh)
	}
}
