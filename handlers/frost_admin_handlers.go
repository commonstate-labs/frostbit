                                   
                     

package handlers

import (
	"dex/consensus"
	"dex/keys"
	"dex/pb"
	"fmt"
	"net/http"
	"runtime"
	"time"

	"google.golang.org/protobuf/proto"
)

var startTime = time.Now()

const debugMetricsCacheTTL = 7 * time.Second

                               
func (hm *HandlerManager) HandleGetHealth(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("GetHealth")

	resp := &pb.HealthResponse{
		Status:    "healthy",
		Uptime:    time.Since(startTime).String(),
		Version:   "1.0.0",
		GoVersion: runtime.Version(),
		NumCpu:    int32(runtime.NumCPU()),
	}

	                        
	data, err := proto.Marshal(resp)
	if err != nil {
		http.Error(w, "failed to marshal response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-protobuf")
	w.Write(data)
}

                                
func (hm *HandlerManager) HandleGetMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if hm.Stats != nil {
		hm.Stats.RecordAPICall("GetMetrics")
	}

	data, err := hm.cachedMetricsPayload(time.Now(), hm.buildMetricsPayload)
	if err != nil {
		http.Error(w, "failed to collect metrics", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	_, _ = w.Write(data)
}

func (hm *HandlerManager) cachedMetricsPayload(now time.Time, build func() ([]byte, error)) ([]byte, error) {
	hm.metricsCacheMu.Lock()
	defer hm.metricsCacheMu.Unlock()
	if len(hm.metricsCachePayload) > 0 && now.Before(hm.metricsCacheExpiry) {
		return hm.metricsCachePayload, nil
	}
	if build == nil {
		return nil, fmt.Errorf("metrics builder is not configured")
	}
	payload, err := build()
	if err != nil {
		return nil, err
	}
	hm.metricsCachePayload = payload
	hm.metricsCacheExpiry = now.Add(debugMetricsCacheTTL)
	return payload, nil
}

func (hm *HandlerManager) buildMetricsPayload() ([]byte, error) {

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	                            
	frostJobs := 0
	frostWithdraws := 0

	              
	if hm.dbManager != nil {
		jobResults, err := hm.dbManager.Scan(keys.KeyFrostWithdrawJobPrefix())
		if err != nil {
			return nil, fmt.Errorf("scan FROST jobs: %w", err)
		}
		frostJobs = countFrostWithdrawJobs(jobResults)

		                                                                        
		                                                                       
		withdrawResults, err := hm.dbManager.Scan(keys.KeyFrostWithdraw(""))
		if err != nil {
			return nil, fmt.Errorf("scan FROST withdraws: %w", err)
		}
		frostWithdraws = countFrostWithdraws(withdrawResults)
	}

	                          
	var apiCallStats map[string]uint64
	if hm.Stats != nil {
		apiCallStats = hm.Stats.GetAPICallStats()
	}

	                        
	var channelStats []*pb.ChannelStat

	                                         
	if hm.senderManager != nil && hm.senderManager.SendQueue != nil {
		for _, stat := range hm.senderManager.SendQueue.GetChannelStats() {
			channelStats = append(channelStats, &pb.ChannelStat{
				Name:   stat.Name,
				Module: stat.Module,
				Len:    int32(stat.Len),
				Cap:    int32(stat.Cap),
				Usage:  stat.Usage,
			})
		}
	}

	                                                        
	if hm.consensusManager != nil && hm.consensusManager.Transport != nil {
		if rt, ok := hm.consensusManager.Transport.(*consensus.RealTransport); ok {
			for _, stat := range rt.GetChannelStats() {
				channelStats = append(channelStats, &pb.ChannelStat{
					Name:   stat.Name,
					Module: stat.Module,
					Len:    int32(stat.Len),
					Cap:    int32(stat.Cap),
					Usage:  stat.Usage,
				})
			}
		}
	}

	                                  
	if hm.txPool != nil {
		for _, stat := range hm.txPool.GetChannelStats() {
			channelStats = append(channelStats, &pb.ChannelStat{
				Name:   stat.Name,
				Module: stat.Module,
				Len:    int32(stat.Len),
				Cap:    int32(stat.Cap),
				Usage:  stat.Usage,
			})
		}
	}

	                                      
	if hm.dbManager != nil {
		for _, stat := range hm.dbManager.GetChannelStats() {
			channelStats = append(channelStats, &pb.ChannelStat{
				Name:   stat.Name,
				Module: stat.Module,
				Len:    int32(stat.Len),
				Cap:    int32(stat.Cap),
				Usage:  stat.Usage,
			})
		}
	}

	resp := &pb.MetricsResponse{
		HeapAlloc:      m.HeapAlloc,
		HeapSys:        m.HeapSys,
		NumGoroutine:   int32(runtime.NumGoroutine()),
		FrostJobs:      int32(frostJobs),
		FrostWithdraws: int32(frostWithdraws),
		ApiCallStats:   apiCallStats,
		ChannelStats:   channelStats,
	}

	return proto.Marshal(resp)
}

func countFrostWithdrawJobs(records map[string][]byte) int {
	count := 0
	for key, raw := range records {
		var state pb.FrostWithdrawJobState
		if err := proto.Unmarshal(raw, &state); err == nil && state.JobId != "" && key == keys.KeyFrostWithdrawJob(state.JobId) {
			count++
		}
	}
	return count
}

func countFrostWithdraws(records map[string][]byte) int {
	count := 0
	for key, raw := range records {
		var state pb.FrostWithdrawState
		if err := proto.Unmarshal(raw, &state); err == nil && state.WithdrawId != "" && key == keys.KeyFrostWithdraw(state.WithdrawId) {
			count++
		}
	}
	return count
}
