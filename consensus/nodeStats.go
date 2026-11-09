package consensus

import (
	"dex/interfaces"
	"dex/types"
	"sync"
)

                                                      

type PreferenceSwitch struct {
	BlockID         string                        
	Confidence      int                            
	Winner          string
	AlphaPreference int
	AlphaConfidence int
}

type NodeStats struct {
	Mu               sync.Mutex
	QueriesSent      uint32
	QueriesReceived  uint32
	ChitsResponded   uint32
	QueriesPerHeight map[uint64]uint32
	BlocksProposed   uint32
	GossipsReceived  uint32

	events interfaces.EventBus

	                                  
	prefHistBuf  []PreferenceSwitch                         
	prefHistCap  int                                               
	prefHistHead int                                                      
	prefHistLen  int                                                       
}

func NewNodeStats(events interfaces.EventBus) *NodeStats {

	historyCap := 256
	stats := &NodeStats{
		QueriesPerHeight: make(map[uint64]uint32),
		events:           events,
		prefHistCap:      historyCap,
	}
	stats.prefHistBuf = make([]PreferenceSwitch, historyCap)

	stats.events.Subscribe(types.EventPreferenceChanged, func(e interfaces.Event) {
		if preferenceSwitch, ok := e.Data().(PreferenceSwitch); ok {
			stats.pushPreferenceSwitch(preferenceSwitch)
		}
	})
	return stats
}

                                                      

                                      
func (s *NodeStats) pushPreferenceSwitch(ps PreferenceSwitch) {
	if s.prefHistCap == 0 {
		return                      
	}
	s.Mu.Lock()
	s.prefHistBuf[s.prefHistHead] = ps
	s.prefHistHead = (s.prefHistHead + 1) % s.prefHistCap
	if s.prefHistLen < s.prefHistCap {
		s.prefHistLen++
	}
	s.Mu.Unlock()
}

                                                                              
func (s *NodeStats) GetPreferenceSwitchHistory() []PreferenceSwitch {
	return s.prefHistBuf
}

                                                                                             
func (s *NodeStats) CleanupOldHeights(belowHeight uint64) int {
	s.Mu.Lock()
	defer s.Mu.Unlock()

	count := 0
	for h := range s.QueriesPerHeight {
		if h < belowHeight {
			delete(s.QueriesPerHeight, h)
			count++
		}
	}
	return count
}
