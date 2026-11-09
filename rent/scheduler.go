package rent

import (
	"container/heap"
	iface "dex/interfaces"
	"dex/keys"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"
)

type orderedRentStore interface {
	ScanRentOrderedAfter(prefix, startAfter string, limit int) ([]iface.KVPair, error)
}

type dueRentItem struct {
	key    string
	height uint64
	owner  string
}

type dueRentHeap []dueRentItem

func (h dueRentHeap) Len() int           { return len(h) }
func (h dueRentHeap) Less(i, j int) bool { return h[i].key < h[j].key }
func (h dueRentHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *dueRentHeap) Push(value any) {
	*h = append(*h, value.(dueRentItem))
}

func (h *dueRentHeap) Pop() any {
	old := *h
	last := len(old) - 1
	item := old[last]
	old[last] = dueRentItem{}
	*h = old[:last]
	return item
}

                                                                                
                                                                               
                                                                               
                          
type dueRentQueue struct {
	height uint64
	items  dueRentHeap
	queued map[string]struct{}
}

func newDueRentQueue(height uint64) *dueRentQueue {
	q := &dueRentQueue{height: height, queued: make(map[string]struct{})}
	heap.Init(&q.items)
	return q
}

func (q *dueRentQueue) offer(item dueRentItem) bool {
	if q == nil || item.key == "" || item.height > q.height {
		return false
	}
	if _, exists := q.queued[item.key]; exists {
		return false
	}
	q.queued[item.key] = struct{}{}
	heap.Push(&q.items, item)
	return true
}

func (q *dueRentQueue) pop() (dueRentItem, bool) {
	if q == nil || q.items.Len() == 0 {
		return dueRentItem{}, false
	}
	item := heap.Pop(&q.items).(dueRentItem)
	return item, true
}

func (q *dueRentQueue) finish(key string) {
	if q == nil || key == "" {
		return
	}
	delete(q.queued, key)
}

func (q *dueRentQueue) popBatch(limit int) []dueRentItem {
	if q == nil || limit <= 0 {
		return nil
	}
	items := make([]dueRentItem, 0, minInt(limit, q.items.Len()))
	for len(items) < limit {
		item, ok := q.pop()
		if !ok {
			break
		}
		items = append(items, item)
	}
	return items
}

func (q *dueRentQueue) peek() (dueRentItem, bool) {
	if q == nil || q.items.Len() == 0 {
		return dueRentItem{}, false
	}
	return q.items[0], true
}

func dueItemOwners(items []dueRentItem) []string {
	owners := make(map[string]struct{}, len(items))
	for _, item := range items {
		if IsRentManagedAccount(item.owner) {
			owners[item.owner] = struct{}{}
		}
	}
	return sortedOwners(owners)
}

func (r *Runtime) observeRentWork(state *RentState) {
	if r == nil || r.activeDueQueue == nil || state == nil {
		return
	}
	key := rentStateWorkKey(state)
	dueHeight, valid := parseRentExpiryHeight(key)
	if !valid || dueHeight > r.activeDueHeight {
		return
	}
	owner := rentExpiryOwner(key)
	r.activeDueQueue.offer(dueRentItem{
		key: key, height: dueHeight, owner: owner,
	})
}

                                                                             
                                                                          
                                                                               
                        
func (r *Runtime) ProcessBlock(height uint64, touchedOwners []string) error {
	if r == nil || r.store == nil {
		return fmt.Errorf("rent: nil runtime store")
	}
	r.stats = ProcessBlockStats{TouchedOwners: len(touchedOwners)}
	r.stats.SweepOwnerBudget = r.sweepOwnerBudget
	r.stats.SweepKeyBudget = r.sweepKeyBudget
	r.serviceStats = ServiceStats{}
	r.recycleTotals = make(map[string]*big.Int)
	defer func() { r.recycleTotals = nil }()
	r.clearReadCaches()

	                                                                             
	                                                                       
	                                                                            
	                                                  
	dueQueue := newDueRentQueue(height)
	r.activeDueQueue = dueQueue
	r.activeDueHeight = height
	defer func() {
		r.activeDueQueue = nil
		r.activeDueHeight = 0
	}()
	dueStart := time.Now()
	if err := r.discoverDueRentItems(height, dueQueue); err != nil {
		return err
	}
	r.stats.DueOwnersDuration += time.Since(dueStart)

	settleStart := time.Now()
	touchedQueue := make([]string, 0, len(touchedOwners))
	touchedSet := make(map[string]struct{}, len(touchedOwners))
	for _, owner := range touchedOwners {
		owner = strings.TrimSpace(owner)
		if !IsRentManagedAccount(owner) {
			continue
		}
		if _, exists := touchedSet[owner]; exists {
			continue
		}
		touchedSet[owner] = struct{}{}
		touchedQueue = append(touchedQueue, owner)
	}
	sort.Strings(touchedQueue)
	prefetchBatchSize := r.duePageSize()
	for start := 0; start < len(touchedQueue); start += prefetchBatchSize {
		end := minInt(start+prefetchBatchSize, len(touchedQueue))
		batch := touchedQueue[start:end]
		if err := r.prefetchRentOwners(batch); err != nil {
			return err
		}
		for _, owner := range batch {
			if err := r.settleLightOwner(owner, height); err != nil {
				return err
			}
		}
	}
	r.stats.Owners += len(touchedQueue)

	                                                                            
	                                                                            
	                                                      
	var pending []dueRentItem
	for {
		if len(pending) == 0 {
			pending = dueQueue.popBatch(prefetchBatchSize)
			if len(pending) == 0 {
				break
			}
			if err := r.prefetchRentOwners(dueItemOwners(pending)); err != nil {
				return err
			}
		}
		item := pending[0]
		if dynamic, ok := dueQueue.peek(); ok && dynamic.key < item.key {
			item, _ = dueQueue.pop()
		} else {
			pending = pending[1:]
		}
		owner := strings.TrimSpace(item.owner)
		if !IsRentManagedAccount(owner) || rentExpiryOwner(item.key) != owner {
			r.store.Del(item.key)
			dueQueue.finish(item.key)
			continue
		}
		                                                                    
		                                                                           
		state, readErr := r.readState(owner)
		if readErr != nil {
			return readErr
		}
		if state == nil || strings.TrimSpace(state.Owner) != owner || item.key != rentStateWorkKey(state) {
			r.store.Del(item.key)
			dueQueue.finish(item.key)
			continue
		}
		if !r.ownerReadsPrefetched(owner) {
			if err := r.prefetchRentOwners([]string{owner}); err != nil {
				return err
			}
			state, readErr = r.readState(owner)
			if readErr != nil {
				return readErr
			}
			if state == nil || strings.TrimSpace(state.Owner) != owner || item.key != rentStateWorkKey(state) {
				r.store.Del(item.key)
				dueQueue.finish(item.key)
				continue
			}
		}
		r.store.Del(item.key)
		r.stats.DueOwners++
		r.stats.Owners++
		if err := r.processDueOwner(owner, height); err != nil {
			return err
		}
		state, readErr = r.readState(owner)
		if readErr != nil {
			return readErr
		}
		if key := rentStateWorkKey(state); key != "" {
			nextHeight, valid := parseRentExpiryHeight(key)
			if !valid {
				return fmt.Errorf("rent: malformed canonical work key %q for %s", key, owner)
			}
			if nextHeight <= height {
				return fmt.Errorf("rent: owner %s retained due work %s after processing height %d", owner, key, height)
			}
		}
		dueQueue.finish(item.key)
	}
	postconditionStart := time.Now()
	if err := r.assertNoDueRentWork(height); err != nil {
		return err
	}
	r.stats.DueOwnersDuration += time.Since(postconditionStart)
	r.stats.SettleLoopDuration = time.Since(settleStart)
	flushStart := time.Now()
	if err := r.flushRecycleBalances(); err != nil {
		return err
	}
	r.stats.RecycleFlushDuration = time.Since(flushStart)
	r.stats.BalanceCacheHits = r.serviceStats.BalanceCacheHits
	r.stats.ClaimMarkerCacheHits = r.serviceStats.ClaimMarkerHits
	r.stats.ClaimTotalCacheHits = r.serviceStats.ClaimTotalHits
	r.stats.ClaimScans = r.serviceStats.ClaimScans
	r.stats.ClaimScanKeys = r.serviceStats.ClaimScanKeys
	r.stats.ClaimScanDuration = r.serviceStats.ClaimScanDuration
	return nil
}

func (r *Runtime) prefetchRentOwners(owners []string) error {
	if len(owners) == 0 {
		return nil
	}
	started := time.Now()
	if err := r.prefetchStates(owners); err != nil {
		return err
	}
	r.stats.PrefetchStatesDuration += time.Since(started)
	started = time.Now()
	if err := r.prefetchOwnerMarkers(owners); err != nil {
		return err
	}
	r.stats.PrefetchMarkersDuration += time.Since(started)
	started = time.Now()
	if err := r.prefetchNominalAssetReads(owners); err != nil {
		return err
	}
	r.stats.PrefetchAssetsDuration += time.Since(started)
	return nil
}

                                                                            
                                                                              
                                                                                
func (r *Runtime) settleLightOwner(owner string, height uint64) error {
	settled, err := r.SettleRent(owner, height)
	if err != nil {
		return err
	}
	r.stats.WrittenStates++
	if settled == nil || settled.Projection == nil {
		return nil
	}
	if settled.Projection.Mode == RentModeExempt {
		r.stats.ExemptAccounts++
	}
	if !settled.Projection.NeedsFunding {
		return nil
	}
	state := cloneRentState(settled.Projection.state)
	if state == nil || state.Work == nil ||
		(state.Mode != RentModeSweeping && !isSweepPhase(state.Work.Phase)) ||
		state.Work.NextActionHeight <= height {
		return nil
	}
	old := cloneRentState(state)
	state.Work.NextActionHeight = height
	if err := r.writeAccrualState(state, old); err != nil {
		return err
	}
	if r.stateRead != nil {
		r.stateRead[owner] = cachedRentState{state: cloneRentState(state)}
	}
	return nil
}

func (r *Runtime) settleFundingTouched(owner string, height uint64, owners []string) error {
	unique := make(map[string]struct{}, len(owners))
	for _, touched := range owners {
		touched = strings.TrimSpace(touched)
		if touched == owner || !IsRentManagedAccount(touched) {
			continue
		}
		unique[touched] = struct{}{}
	}
	queue := sortedOwners(unique)
	if err := r.prefetchRentOwners(queue); err != nil {
		return err
	}
	for _, touched := range queue {
		if err := r.settleLightOwner(touched, height); err != nil {
			return err
		}
	}
	r.stats.Owners += len(queue)
	return nil
}

func (r *Runtime) processDueOwner(owner string, height uint64) error {
	for {
		settled, err := r.SettleRent(owner, height)
		if err != nil {
			return err
		}
		r.stats.WrittenStates++
		if settled == nil || settled.Projection == nil || !settled.Projection.NeedsFunding {
			if settled != nil && settled.Projection != nil && settled.Projection.Mode == RentModeExempt {
				r.stats.ExemptAccounts++
			}
			return nil
		}
		if settled.Projection.Mode == RentModeSweeping ||
			settled.Projection.Work != nil && isSweepPhase(settled.Projection.Work.Phase) {
			return r.SweepAccount(owner, height, settled.Projection.state)
		}
		if r.fundingAttempt == nil {
			return r.SweepAccount(owner, height, settled.Projection.state)
		}

		fundingService := NewService(r.store)
		before, readErr := fundingService.availableBalance(owner, r.policy.Token)
		if readErr != nil {
			return readErr
		}
		beforeSummary, readErr := fundingService.NominalAsset(owner, r.policy.Token)
		if readErr != nil {
			return readErr
		}
		beforeNominal, readErr := amountFromCanonical("pre-funding nominal FB", beforeSummary.Total)
		if readErr != nil {
			return readErr
		}
		required, parseErr := amountFromCanonical("required rent funding", settled.Projection.RequiredFundingFB)
		if parseErr != nil {
			return parseErr
		}
		previousMode := settled.Projection.Mode
		var work *RentWork
		if settled.Projection.Work != nil {
			copy := *settled.Projection.Work
			work = &copy
		}
		funding, err := r.fundingAttempt(owner, height, required, work)
		if err != nil {
			return err
		}
		if funding.Deferred {
			return fmt.Errorf("rent: funding attempt for %s returned incomplete deferred work at height %d", owner, height)
		}
		                                                                     
		                                                                           
		                                              
		r.invalidateOwnerReadCaches(owner)
		for _, touched := range funding.TouchedOwners {
			touched = strings.TrimSpace(touched)
			if IsRentManagedAccount(touched) {
				r.invalidateOwnerReadCaches(touched)
			}
		}
		received := new(big.Int)
		if funding.ReceivedFB != nil {
			received.Set(funding.ReceivedFB)
		}
		if received.Sign() < 0 {
			return fmt.Errorf("rent: negative funding receipt for %s", owner)
		}
		afterService := NewService(r.store)
		after, readErr := afterService.availableBalance(owner, r.policy.Token)
		if readErr != nil {
			return readErr
		}
		afterSummary, readErr := afterService.NominalAsset(owner, r.policy.Token)
		if readErr != nil {
			return readErr
		}
		afterNominal, readErr := amountFromCanonical("post-funding nominal FB", afterSummary.Total)
		if readErr != nil {
			return readErr
		}
		increase := new(big.Int).Sub(after, before)
		if increase.Sign() < 0 {
			increase.SetInt64(0)
		}
		nominalIncrease := new(big.Int).Sub(afterNominal, beforeNominal)
		if nominalIncrease.Sign() < 0 {
			nominalIncrease.SetInt64(0)
		}
		if received.Cmp(increase) > 0 || received.Cmp(nominalIncrease) > 0 {
			return fmt.Errorf("rent: funding receipt exceeds available FB increase for %s", owner)
		}

		settled, err = r.SettleRent(owner, height)
		if err != nil {
			return err
		}
		r.stats.WrittenStates++
		if err := r.settleFundingTouched(owner, height, funding.TouchedOwners); err != nil {
			return err
		}
		if settled == nil || settled.Projection == nil || !settled.Projection.NeedsFunding {
			return nil
		}

		if received.Sign() > 0 {
			nextRequired, parseErr := amountFromCanonical("remaining required rent funding", settled.Projection.RequiredFundingFB)
			if parseErr != nil {
				return parseErr
			}
			enteredActivation := previousMode == RentModeExemptDebt && settled.Projection.Mode == RentModeNeedsFunding
			if nextRequired.Cmp(required) >= 0 && !enteredActivation {
				return fmt.Errorf("rent: funding did not reduce required FB for %s at height %d", owner, height)
			}
			continue
		}
		return r.SweepAccount(owner, height, settled.Projection.state)
	}
}

                                                                           
                                                                              
                
func (r *Runtime) SettleAccount(owner string, height uint64) error {
	_, err := r.SettleRent(owner, height)
	return err
}

func (r *Runtime) duePageSize() int {
	limit := r.scanLimit
	if limit <= 0 {
		limit = DefaultScanLimit
	}
	if r.sweepOwnerBudget > 0 && r.sweepOwnerBudget < limit {
		limit = r.sweepOwnerBudget
	}
	return limit
}

                                                                            
                                                                           
func (r *Runtime) discoverDueRentItems(height uint64, queue *dueRentQueue) error {
	prefix := keys.KeyRentExpiryAllPrefix()
	limit := r.duePageSize()
	if limit <= 0 {
		return fmt.Errorf("rent: invalid due page size")
	}
	if ordered, ok := r.store.(orderedRentStore); ok {
		cursor := ""
		for {
			rows, err := ordered.ScanRentOrderedAfter(prefix, cursor, limit)
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				return nil
			}
			                                                                     
			                                                                      
			                                                                    
			                                                              
			advanced := false
			for _, pair := range rows {
				if pair.Key <= cursor {
					return fmt.Errorf("rent: non-monotonic expiry scan after %q returned %q", cursor, pair.Key)
				}
				cursor = pair.Key
				advanced = true
				dueHeight, valid := parseRentExpiryHeight(pair.Key)
				if !valid {
					return fmt.Errorf("rent: malformed expiry key %q", pair.Key)
				}
				if dueHeight > height {
					return nil
				}
				owner := rentExpiryOwner(pair.Key)
				queue.offer(dueRentItem{
					key: pair.Key, height: dueHeight, owner: owner,
				})
			}
			if !advanced {
				return fmt.Errorf("rent: expiry scan made no progress after %q", cursor)
			}
		}
	}

	                                                                         
	                                                                     
	rows, err := r.store.Scan(prefix, 0)
	if err != nil {
		return err
	}
	for _, key := range sortedKeys(rows) {
		dueHeight, valid := parseRentExpiryHeight(key)
		if !valid {
			return fmt.Errorf("rent: malformed expiry key %q", key)
		}
		if dueHeight > height {
			break
		}
		queue.offer(dueRentItem{
			key: key, height: dueHeight, owner: rentExpiryOwner(key),
		})
	}
	return nil
}

func (r *Runtime) assertNoDueRentWork(height uint64) error {
	prefix := keys.KeyRentExpiryAllPrefix()
	var pairs []iface.KVPair
	if ordered, ok := r.store.(orderedRentStore); ok {
		rows, err := ordered.ScanRentOrderedAfter(prefix, "", 1)
		if err != nil {
			return err
		}
		if len(rows) > 1 {
			rows = rows[:1]
		}
		pairs = rows
	} else {
		rows, err := r.store.Scan(prefix, 0)
		if err != nil {
			return err
		}
		orderedKeys := sortedKeys(rows)
		if len(orderedKeys) > 0 {
			key := orderedKeys[0]
			pairs = []iface.KVPair{{Key: key, Value: rows[key]}}
		}
	}
	if len(pairs) == 0 {
		return nil
	}
	pair := pairs[0]
	dueHeight, valid := parseRentExpiryHeight(pair.Key)
	if !valid {
		return fmt.Errorf("rent: malformed expiry key %q after height %d", pair.Key, height)
	}
	if dueHeight <= height {
		return fmt.Errorf("rent: untracked due work %s remained after height %d", pair.Key, height)
	}
	return nil
}

func (r *Runtime) ownerReadsPrefetched(owner string) bool {
	if r.stateRead == nil || r.balanceRead == nil || r.claimMarkerRead == nil ||
		r.claimTotalRead == nil || r.ownerMarkerRead == nil {
		return false
	}
	if _, ok := r.stateRead[owner]; !ok {
		return false
	}
	if _, ok := r.ownerMarkerRead[owner]; !ok {
		return false
	}
	if _, ok := r.balanceRead[keys.KeyBalance(owner, r.policy.Token)]; !ok {
		return false
	}
	if _, ok := r.claimMarkerRead[keys.KeyAssetClaimMarker(owner, r.policy.Token)]; !ok {
		return false
	}
	if _, ok := r.claimTotalRead[keys.KeyAssetClaimTotal(owner, r.policy.Token)]; !ok {
		return false
	}
	return true
}

func parseRentExpiryHeight(key string) (uint64, bool) {
	prefix := keys.KeyRentExpiryAllPrefix()
	if !strings.HasPrefix(key, prefix) {
		return 0, false
	}
	rest := strings.TrimPrefix(key, prefix)
	if len(rest) < 21 || rest[20] != '_' {
		return 0, false
	}
	height, err := strconv.ParseUint(rest[:20], 10, 64)
	return height, err == nil
}

func rentExpiryOwner(key string) string {
	prefix := keys.KeyRentExpiryAllPrefix()
	rest := strings.TrimPrefix(key, prefix)
	if len(rest) < 21 {
		return ""
	}
	return strings.TrimSpace(rest[21:])
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
