package liveness

import (
	iface "dex/interfaces"
	"dex/protocol"
	"sort"
	"sync"
)

var _ iface.LivenessLocalReporter = (*LocalReporter)(nil)
var _ iface.LivenessAtomicReportSink = (*LocalReporter)(nil)
var _ iface.LivenessAtomicHeaderReporter = (*LocalReporter)(nil)
var _ iface.LivenessOnlineReportSink = (*LocalReporter)(nil)
var _ iface.LivenessOnlineHeaderReporter = (*LocalReporter)(nil)

                                                                                    
type MinerIndexResolver interface {
	GetMinerIndex(address string) (uint32, bool)
}

                                                                                   
                                                             
type LocalReporter struct {
	resolver MinerIndexResolver

	pendingMu sync.Mutex
	pending   map[uint32]pendingObservation
	nextIndex uint32

	                                                                           
	                                                                          
	legacyBatch       *livenessReportBatch
	legacySampleRead  bool
	legacyOfflineRead bool
}

type pendingObservation struct {
	offline bool
	online  bool
}

type livenessReportBatch struct {
	sampled []uint32
	offline []uint32
	online  []uint32
}

func NewLocalReporter(resolver MinerIndexResolver) *LocalReporter {
	return &LocalReporter{resolver: resolver, pending: make(map[uint32]pendingObservation)}
}

func (r *LocalReporter) ReportSampled(addresses []string) {
	indexes := r.resolveIndexes(addresses)
	r.reportIndexes(indexes, nil)
}

func (r *LocalReporter) ReportOffline(addresses []string) {
	indexes := r.resolveIndexes(addresses)
	r.reportIndexes(nil, indexes)
}

                                                                              
                                                                      
func (r *LocalReporter) ReportLiveness(sampled, offline []string) {
	r.reportIndexes(r.resolveIndexes(sampled), r.resolveIndexes(offline))
}

func (r *LocalReporter) ReportLivenessWithOnline(sampled, offline, online []string) {
	r.reportIndexesWithOnline(r.resolveIndexes(sampled), r.resolveIndexes(offline), r.resolveIndexes(online))
}

func (r *LocalReporter) reportIndexes(sampled, offline []uint32) {
	r.reportIndexesWithOnline(sampled, offline, nil)
}

func (r *LocalReporter) reportIndexesWithOnline(sampled, offline, online []uint32) {
	if r == nil || (len(sampled) == 0 && len(offline) == 0 && len(online) == 0) {
		return
	}
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	r.ensurePendingLocked()
	for _, idx := range sampled {
		if _, exists := r.pending[idx]; !exists {
			r.pending[idx] = pendingObservation{}
		}
	}
	for _, idx := range offline {
		                                                                        
		                                                                      
		                                         
		if !r.pending[idx].online {
			r.pending[idx] = pendingObservation{offline: true}
		}
	}
	for _, idx := range online {
		                                                                                 
		r.pending[idx] = pendingObservation{online: true}
	}
}

                                                                            
                                                                             
                                                                          
                             
func (r *LocalReporter) GetLivenessReports() (sampled, offline []uint32) {
	sampled, offline, _ = r.GetLivenessReportsWithOnline()
	return sampled, offline
}

func (r *LocalReporter) GetLivenessReportsWithOnline() (sampled, offline, online []uint32) {
	if r == nil {
		return nil, nil, nil
	}
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	batch := r.drainBatchLocked()
	return append([]uint32(nil), batch.sampled...), append([]uint32(nil), batch.offline...), append([]uint32(nil), batch.online...)
}

func (r *LocalReporter) GetSampledReports() []uint32 {
	return r.getLegacyReportSide(true)
}

func (r *LocalReporter) GetOfflineReports() []uint32 {
	return r.getLegacyReportSide(false)
}

func (r *LocalReporter) resolveIndexes(addresses []string) []uint32 {
	if r == nil || r.resolver == nil || len(addresses) == 0 {
		return nil
	}
	indexes := make([]uint32, 0, len(addresses))
	for _, addr := range addresses {
		if idx, ok := r.resolver.GetMinerIndex(addr); ok {
			indexes = append(indexes, idx)
		}
	}
	return indexes
}

func (r *LocalReporter) getLegacyReportSide(sampled bool) []uint32 {
	if r == nil {
		return nil
	}
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	if r.legacyBatch == nil {
		batch := r.drainBatchLocked()
		r.legacyBatch = &batch
		r.legacySampleRead = false
		r.legacyOfflineRead = false
	}
	var out []uint32
	if sampled {
		if !r.legacySampleRead {
			out = append([]uint32(nil), r.legacyBatch.sampled...)
			r.legacySampleRead = true
		}
	} else if !r.legacyOfflineRead {
		out = append([]uint32(nil), r.legacyBatch.offline...)
		r.legacyOfflineRead = true
	}
	if r.legacySampleRead && r.legacyOfflineRead {
		r.legacyBatch = nil
	}
	return out
}

func (r *LocalReporter) ensurePendingLocked() {
	if r.pending == nil {
		r.pending = make(map[uint32]pendingObservation)
	}
}

func (r *LocalReporter) drainBatchLocked() livenessReportBatch {
	r.ensurePendingLocked()
	if len(r.pending) == 0 {
		return livenessReportBatch{}
	}
	indexes := make([]uint32, 0, len(r.pending))
	for idx := range r.pending {
		indexes = append(indexes, idx)
	}
	sort.Slice(indexes, func(i, j int) bool { return indexes[i] < indexes[j] })
	start := sort.Search(len(indexes), func(i int) bool { return indexes[i] >= r.nextIndex })
	limit := protocol.MaxLivenessReportsPerBlock
	if limit > len(indexes) {
		limit = len(indexes)
	}
	batch := livenessReportBatch{
		sampled: make([]uint32, 0, limit),
		offline: make([]uint32, 0, limit),
		online:  make([]uint32, 0, limit),
	}
	for i := 0; i < limit; i++ {
		idx := indexes[(start+i)%len(indexes)]
		observation := r.pending[idx]
		batch.sampled = append(batch.sampled, idx)
		if observation.offline {
			batch.offline = append(batch.offline, idx)
		}
		if observation.online {
			batch.online = append(batch.online, idx)
		}
		delete(r.pending, idx)
	}
	last := batch.sampled[len(batch.sampled)-1]
	if last == ^uint32(0) {
		r.nextIndex = 0
	} else {
		r.nextIndex = last + 1
	}
	                                                                 
	sort.Slice(batch.sampled, func(i, j int) bool { return batch.sampled[i] < batch.sampled[j] })
	sort.Slice(batch.offline, func(i, j int) bool { return batch.offline[i] < batch.offline[j] })
	sort.Slice(batch.online, func(i, j int) bool { return batch.online[i] < batch.online[j] })
	return batch
}
