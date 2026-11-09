package main

import (
	"dex/consensus"
	"dex/logs"
	"dex/stats"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

type latencyEntry struct {
	Name    string
	Summary stats.LatencySummary
}

type gcSnapshot struct {
	takenAt       time.Time
	numGC         uint32
	pauseTotalNs  uint64
	totalAlloc    uint64
	heapAlloc     uint64
	heapInuse     uint64
	heapObjects   uint64
	nextGC        uint64
	mallocs       uint64
	frees         uint64
	gcCPUFraction float64
}

                              
type APICallStats struct {
	sync.RWMutex
	                                          
	CallCounts map[string]uint64
	                                                
	NodeCallCounts map[int]map[string]uint64
}

                           
var globalAPIStats = &APICallStats{
	CallCounts:     make(map[string]uint64),
	NodeCallCounts: make(map[int]map[string]uint64),
}

var gcMonitorOnce sync.Once

func monitorMetrics(nodes []*NodeInstance) {
	gcMonitorOnce.Do(func() {
		go monitorGCStats()
	})

	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	                                                               
	lastCallCounts := make(map[int]map[string]uint64)

	for range ticker.C {
		                                          
		currentStats := make(map[string]uint64)
		nodeStats := make(map[int]map[string]uint64)

		for _, node := range nodes {
			if node == nil || node.SenderManager == nil {
				continue
			}

			                                                 
			sendQueueLen := node.SenderManager.SendQueue.QueueLen()

			                           
			node.SenderManager.SendQueue.InflightMutex.RLock()
			inflightCopy := make(map[string]int32)
			totalInflight := int32(0)
			for k, v := range node.SenderManager.SendQueue.InflightMap {
				inflightCopy[k] = v
				totalInflight += v
			}
			node.SenderManager.SendQueue.InflightMutex.RUnlock()

			                        
			recvQueueLen := 0
			                                                                              
			          
			    

			                                    
			if node.HandlerManager != nil {
				apiStats := node.HandlerManager.Stats.GetAPICallStats()

				                                       
				nodeStats[node.ID] = apiStats

				                                    
				if lastCallCounts[node.ID] == nil {
					lastCallCounts[node.ID] = make(map[string]uint64)
				}

				for apiName, currentCount := range apiStats {
					                              
					delta := currentCount
					if lastCount, exists := lastCallCounts[node.ID][apiName]; exists {
						delta = currentCount - lastCount
					}

					                     
					currentStats[apiName] += delta

					                     
					lastCallCounts[node.ID][apiName] = currentCount
				}
			}

			                     
			if sendQueueLen > 0 || totalInflight > 0 || recvQueueLen > 0 {
				logs.Info("[Metrics] Node %d: SendQ=%d, Inflight=%d, RecvQ=%d",
					node.ID, sendQueueLen, totalInflight, recvQueueLen)
			}
		}

		                              
		globalAPIStats.Lock()
		for apiName, delta := range currentStats {
			globalAPIStats.CallCounts[apiName] += delta
		}
		for nodeID, apis := range nodeStats {
			globalAPIStats.NodeCallCounts[nodeID] = apis
		}
		globalAPIStats.Unlock()

		printAPICallStatistics()
	}
}

                                                                                   
func monitorQueueStats(nodes []*NodeInstance) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		logs.Info("\n========== Queue Status @ %s ==========", time.Now().Format("15:04:05"))
		for _, node := range nodes {
			if node == nil {
				continue
			}

			channelStats := collectNodeQueueStats(node)
			sort.Slice(channelStats, func(i, j int) bool {
				if channelStats[i].Module != channelStats[j].Module {
					return channelStats[i].Module < channelStats[j].Module
				}
				return channelStats[i].Name < channelStats[j].Name
			})

			logs.Info("[Queue] Node %d (%s)", node.ID, node.Address)
			if len(channelStats) == 0 {
				logs.Info("  (no queue stats available)")
				continue
			}

			for _, cs := range channelStats {
				logs.Info("  %-10s %-16s len=%5d cap=%5d usage=%6.2f%%",
					cs.Module, cs.Name, cs.Len, cs.Cap, cs.Usage*100)
			}

			                                                                                            
			if node.SenderManager != nil && node.SenderManager.SendQueue != nil {
				sq := node.SenderManager.SendQueue
				sq.InflightMutex.RLock()
				inflightTargets := len(sq.InflightMap)
				totalInflight := int32(0)
				for _, v := range sq.InflightMap {
					totalInflight += v
				}
				sq.InflightMutex.RUnlock()
				logs.Info("  %-10s %-16s total=%5d targets=%5d",
					"SendQueue", "inflight", totalInflight, inflightTargets)

				runtimeStats := sq.GetRuntimeStats()
				logs.Info("  %-10s %-16s timer=%5d sleep=%5d",
					"SendQueue", "nextAttempt",
					runtimeStats.DelayedTimerBacklog, runtimeStats.NextAttemptSleeping)
				logs.Info("  %-10s %-16s control=%5d data=%5d immediate=%5d",
					"SendQueue", "drop.queueFull",
					runtimeStats.DropControlFull, runtimeStats.DropDataFull, runtimeStats.DropImmediateFull)
				logs.Info("  %-10s %-16s stale=%5d overload=%5d requeue=%5d",
					"SendQueue", "drop/requeue",
					runtimeStats.DropStaleWorker, runtimeStats.DropStaleOverload, runtimeStats.InflightRequeue)
				logs.Info("  %-10s %-16s exhausted=%5d expired=%5d",
					"SendQueue", "retry.giveup",
					runtimeStats.RetryExhausted, runtimeStats.RetryExpired)
				logs.Info("  %-10s %-16s ok=%5d err=%5d timeout=%5d",
					"SendQueue", "send.result",
					runtimeStats.SendSuccess, runtimeStats.SendError, runtimeStats.SendTimeout)
				logs.Info("  %-10s %-16s skipped=%5d",
					"SendQueue", "peer.health",
					runtimeStats.HealthSkipped)

				printLatencyTopN("SendQueue", sq.GetLatencyStats(true), 3, "sendqueue.do_send")
			}

			if node.ConsensusManager != nil && node.ConsensusManager.Node != nil {
				runtimeStats := node.ConsensusManager.Node.GetRuntimeStats()
				usage := 0.0
				if runtimeStats.SemCapacity > 0 {
					usage = float64(runtimeStats.SemInUse) / float64(runtimeStats.SemCapacity) * 100
				}
				logs.Info("  %-10s %-16s inUse=%5d cap=%5d peak=%5d usage=%6.2f%% wait100=%5d wait1s=%5d",
					"HandleMsg", "sem",
					runtimeStats.SemInUse, runtimeStats.SemCapacity, runtimeStats.SemPeak, usage,
					runtimeStats.SlowWait100ms, runtimeStats.SlowWait1s)

				for _, item := range node.ConsensusManager.Node.GetHandleMsgTypeStats(true, 3) {
					logs.Info("  %-10s %-16s type=%-16s in=%4d start=%5d done=%5d wait100=%5d wait1s=%5d",
						"HandleMsg", "type.top",
						item.Type, item.InFlight, item.Started, item.Completed, item.SlowWait100ms, item.SlowWait1s)
				}

				printLatencyTopN("HandleMsg", node.ConsensusManager.Node.GetHandleMsgLatencyStats(true), 3, "node.handle_msg")
			}

			if node.ConsensusManager != nil && node.ConsensusManager.Transport != nil {
				if rt, ok := node.ConsensusManager.Transport.(*consensus.RealTransport); ok {
					runtimeStats := rt.GetRuntimeStats()
					logs.Info("  %-10s %-16s controlTimeout=%5d dataFull=%5d inboxTimeout=%5d invalid=%5d",
						"Transport", "drop",
						runtimeStats.ControlEnqueueTimeoutDrops,
						runtimeStats.DataEnqueueFullDrops,
						runtimeStats.InboxForwardTimeoutDrops,
						runtimeStats.PreprocessInvalidDrops)

					for _, item := range rt.GetTopInboxTimeoutByType(true, 3) {
						logs.Info("  %-10s %-16s type=%-16s timeout=%5d",
							"Transport", "timeout.type", item.Key, item.Count)
					}
					for _, item := range rt.GetTopInboxTimeoutBySender(true, 3) {
						logs.Info("  %-10s %-16s from=%-16s timeout=%5d",
							"Transport", "timeout.from", item.Key, item.Count)
					}
				}
			}

			if node.HandlerManager != nil && node.HandlerManager.Stats != nil {
				printLatencyTopN("Handler", node.HandlerManager.Stats.GetLatencyStats(true), 5, "handler")
			}
		}
		logs.Info("========================================")
	}
}

func captureGCSnapshot() gcSnapshot {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return gcSnapshot{
		takenAt:       time.Now(),
		numGC:         ms.NumGC,
		pauseTotalNs:  ms.PauseTotalNs,
		totalAlloc:    ms.TotalAlloc,
		heapAlloc:     ms.HeapAlloc,
		heapInuse:     ms.HeapInuse,
		heapObjects:   ms.HeapObjects,
		nextGC:        ms.NextGC,
		mallocs:       ms.Mallocs,
		frees:         ms.Frees,
		gcCPUFraction: ms.GCCPUFraction,
	}
}

                                                             
func monitorGCStats() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	prev := captureGCSnapshot()
	for range ticker.C {
		cur := captureGCSnapshot()

		seconds := cur.takenAt.Sub(prev.takenAt).Seconds()
		if seconds <= 0 {
			seconds = 1
		}

		allocRateMBs := float64(cur.totalAlloc-prev.totalAlloc) / seconds / (1024 * 1024)
		heapAllocMB := float64(cur.heapAlloc) / (1024 * 1024)
		heapInuseMB := float64(cur.heapInuse) / (1024 * 1024)
		nextGCMB := float64(cur.nextGC) / (1024 * 1024)
		heapDeltaMB := float64(int64(cur.heapAlloc)-int64(prev.heapAlloc)) / (1024 * 1024)
		objDelta := int64(cur.heapObjects) - int64(prev.heapObjects)
		mallocRate := float64(cur.mallocs-prev.mallocs) / seconds
		freeRate := float64(cur.frees-prev.frees) / seconds
		gcDelta := cur.numGC - prev.numGC
		pauseDeltaMs := float64(cur.pauseTotalNs-prev.pauseTotalNs) / float64(time.Millisecond)

		logs.Info(
			"[GC] 10s alloc=%.1fMB/s heap=%.1fMB(inuse=%.1fMB,delta=%+.1fMB) objs=%d(delta=%+d) malloc=%.0f/s free=%.0f/s gc=%d pause=%.2fms nextGC=%.1fMB gcCPU=%.2f%% goroutines=%d",
			allocRateMBs,
			heapAllocMB, heapInuseMB, heapDeltaMB,
			cur.heapObjects, objDelta,
			mallocRate, freeRate,
			gcDelta, pauseDeltaMs,
			nextGCMB, cur.gcCPUFraction*100,
			runtime.NumGoroutine(),
		)

		if allocRateMBs >= 256 || gcDelta >= 5 || pauseDeltaMs >= 200 {
			logs.Warn(
				"[GC] pressure alloc=%.1fMB/s gc=%d pause=%.2fms heap=%.1fMB objsDelta=%+d",
				allocRateMBs, gcDelta, pauseDeltaMs, heapAllocMB, objDelta,
			)
		}

		prev = cur
	}
}

func collectNodeQueueStats(node *NodeInstance) []stats.ChannelStat {
	var result []stats.ChannelStat
	if node == nil {
		return result
	}

	if node.SenderManager != nil && node.SenderManager.SendQueue != nil {
		result = append(result, node.SenderManager.SendQueue.GetChannelStats()...)
	}
	if node.ConsensusManager != nil && node.ConsensusManager.Transport != nil {
		if rt, ok := node.ConsensusManager.Transport.(*consensus.RealTransport); ok {
			result = append(result, rt.GetChannelStats()...)
		}
	}
	if node.TxPool != nil {
		result = append(result, node.TxPool.GetChannelStats()...)
	}
	if node.DBManager != nil {
		result = append(result, node.DBManager.GetChannelStats()...)
	}

	return result
}

func printLatencyTopN(module string, summary map[string]stats.LatencySummary, topN int, prefix string) {
	top := selectTopLatency(summary, topN, prefix)
	if len(top) == 0 {
		return
	}
	for _, item := range top {
		logs.Info("  %-10s %-16s key=%s count=%5d p95=%-10v p99=%-10v max=%v",
			module, "latency",
			item.Name, item.Summary.Count, item.Summary.P95, item.Summary.P99, item.Summary.Max)
	}
}

func selectTopLatency(summary map[string]stats.LatencySummary, topN int, prefix string) []latencyEntry {
	if len(summary) == 0 || topN <= 0 {
		return nil
	}

	entries := make([]latencyEntry, 0, len(summary))
	for name, s := range summary {
		if s.Count == 0 {
			continue
		}
		if prefix != "" && !strings.HasPrefix(name, prefix) {
			continue
		}
		entries = append(entries, latencyEntry{Name: name, Summary: s})
	}
	if len(entries) == 0 {
		return nil
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Summary.P95 != entries[j].Summary.P95 {
			return entries[i].Summary.P95 > entries[j].Summary.P95
		}
		if entries[i].Summary.P99 != entries[j].Summary.P99 {
			return entries[i].Summary.P99 > entries[j].Summary.P99
		}
		return entries[i].Name < entries[j].Name
	})

	if len(entries) > topN {
		entries = entries[:topN]
	}
	return entries
}

                        
func printAPICallStatistics() {
	globalAPIStats.RLock()
	defer globalAPIStats.RUnlock()

	if len(globalAPIStats.CallCounts) == 0 {
		return
	}

	logs.Info("\n========== API Call Statistics ==========")
	logs.Info("Global API Call Counts:")

	                        
	var apiNames []string
	for apiName := range globalAPIStats.CallCounts {
		apiNames = append(apiNames, apiName)
	}
	sort.Strings(apiNames)

	                     
	totalCalls := uint64(0)
	for _, apiName := range apiNames {
		count := globalAPIStats.CallCounts[apiName]
		totalCalls += count
		logs.Info("  %-30s: %10d calls", apiName, count)
	}
	logs.Info("  %-30s: %10d calls", "TOTAL", totalCalls)

	                              
	logs.Info("\nAPI Call Frequency Analysis:")
	if totalCalls > 0 {
		for _, apiName := range apiNames {
			count := globalAPIStats.CallCounts[apiName]
			percentage := float64(count) * 100.0 / float64(totalCalls)

			                                 
			barLength := int(percentage / 2)
			if barLength > 40 {
				barLength = 40
			}
			bar := strings.Repeat("█", barLength)

			logs.Info("  %-25s: %6.2f%% %s", apiName, percentage, bar)
		}
	}

	logs.Info("==========================================")
}

               
func monitorProgress(nodes []*NodeInstance) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		logs.Info("\n=== Consensus Progress ===")
		for _, node := range nodes {
			if node == nil || node.ConsensusManager == nil {
				continue
			}
			pref, height := node.ConsensusManager.GetLastAccepted()
			logs.Info("Node %d: Height=%d, LastAccepted=%s",
				node.ID, height, pref)
		}
		logs.Info("==========================")
	}
}

                                                                                          
func monitorMinerParticipantsByEpoch(nodes []*NodeInstance) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		for _, node := range nodes {
			if node == nil || node.DBManager == nil || node.ConsensusManager == nil {
				continue
			}

			_, height := node.ConsensusManager.GetLastAccepted()
			if err := node.DBManager.RefreshMinerParticipantsByHeight(height); err != nil {
				logs.Debug("[MinerCacheMonitor] node=%d refresh failed: %v", node.ID, err)
			}
		}
	}
}
