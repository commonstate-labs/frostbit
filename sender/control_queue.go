package sender

import (
	"container/list"
	"sync"
	"time"
)

                                                                             
                                                                                
                                                                                
                                                          
type controlTaskQueue struct {
	mu       sync.Mutex
	changed  *sync.Cond
	tasks    list.List
	capacity int
	stopped  bool
}

func newControlTaskQueue(capacity int) *controlTaskQueue {
	q := &controlTaskQueue{capacity: capacity}
	q.changed = sync.NewCond(&q.mu)
	return q
}

func (q *controlTaskQueue) push(task *SendTask) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.stopped || q.tasks.Len() >= q.capacity {
		return false
	}
	q.tasks.PushBack(task)
	q.changed.Signal()
	return true
}

                                                                             
                                                                      
func (q *controlTaskQueue) pop(ready func(*SendTask) bool) *SendTask {
	q.mu.Lock()
	defer q.mu.Unlock()
	for !q.stopped {
		for entry := q.tasks.Front(); entry != nil; entry = entry.Next() {
			task := entry.Value.(*SendTask)
			if ready(task) {
				q.tasks.Remove(entry)
				return task
			}
		}
		q.changed.Wait()
	}
	return nil
}

func (q *controlTaskQueue) notify() {
	q.mu.Lock()
	q.changed.Broadcast()
	q.mu.Unlock()
}

func (q *controlTaskQueue) len() int {
	if q == nil {
		return 0
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.tasks.Len()
}

func (q *controlTaskQueue) close() []*SendTask {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.stopped = true
	pending := make([]*SendTask, 0, q.tasks.Len())
	for entry := q.tasks.Front(); entry != nil; entry = entry.Next() {
		pending = append(pending, entry.Value.(*SendTask))
	}
	q.tasks.Init()
	q.changed.Broadcast()
	return pending                                                    
}

func (sq *SendQueue) controlWorkerLoop(workerID int) {
	defer sq.wg.Done()
	for {
		reserved := false
		task := sq.controlQueue.pop(func(task *SendTask) bool {
			if time.Since(task.CreatedAt) > sq.cfg.Sender.TaskExpireTimeout {
				return true                                                       
			}
			reserved = sq.tryAcquireInflight(task.Target, "control")
			if !reserved && task.controlWaitSince.IsZero() {
				task.controlWaitSince = time.Now()
				sq.controlTargetWaits.Add(1)
			}
			return reserved
		})
		if task == nil {
			return
		}
		if !reserved {
			sq.dropExpiredTask(task, "control")
			continue
		}
		sq.sendAcquiredTask(task, workerID, "control")
	}
}
