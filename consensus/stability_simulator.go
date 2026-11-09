package consensus

import (
	"container/heap"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"time"
)

                                                                       
                                 
type StabilityPolicy string

                                                                        
type StabilityScenario string

                                                                            
                                      
type StabilityStrengthTieBreak string

const (
	                                                                         
	                                                                           
	                                                  
	StabilityPolicyCurrent StabilityPolicy = "current"

	                                                                         
	                                                                        
	StabilityPolicySticky StabilityPolicy = "sticky"

	                                                                          
	                                                                          
	                                            
	StabilityPolicySnowball StabilityPolicy = "snowball"

	                                                                         
	StabilityBlockA = "block-1-early-w0-bbbb"
	                                                                        
	                                                                     
	StabilityBlockB = "block-1-late-w0-aaaa"
	                                                                         
	                            
	StabilityBlockC = "block-1-third-w0-cccc"

	                                                                          
	                       
	StabilityScenarioPropagationRace StabilityScenario = "propagation-race"
	                                                                     
	                                                       
	StabilityScenarioFragmented StabilityScenario = "fragmented"
	                                                                          
	                                                                         
	                                                                       
	StabilityScenarioVRFNetwork StabilityScenario = "vrf-network"
	                                                                          
	                                                                         
	                                                                    
	                                                                           
	                                                                            
	                                                        
	StabilityScenarioVRFPhaseSkew StabilityScenario = "vrf-phase-skew"
	                                                                         
	                                                                          
	                                                                        
	StabilityScenarioSingleNodeNotReady StabilityScenario = "single-node-not-ready"

	                                                                        
	                                                           
	StabilityStrengthTieBreakNone StabilityStrengthTieBreak = "none"
	                                                                      
	                                                                          
	                                                                    
	                                                                   
	StabilityStrengthTieBreakCanonical StabilityStrengthTieBreak = "canonical"
)

                                                                           
                                                                  
type StabilityConfig struct {
	Policy   StabilityPolicy   `json:"policy"`
	Scenario StabilityScenario `json:"scenario"`
	Seed     int64             `json:"seed"`

	StrengthTieBreak StabilityStrengthTieBreak `json:"strength_tie_break"`

	Nodes      int `json:"nodes"`
	Byzantine  int `json:"byzantine"`
	K          int `json:"k"`
	EarlyNodes int `json:"early_nodes"`

	AlphaPreference int `json:"alpha_preference"`
	AlphaConfidence int `json:"alpha_confidence"`
	Beta            int `json:"beta"`
	Pipeline        int `json:"pipeline"`
	PollCapacity    int `json:"poll_capacity"`

	PollInterval      time.Duration `json:"poll_interval"`
	QueryTimeout      time.Duration `json:"query_timeout"`
	MinNetworkLatency time.Duration `json:"min_network_latency"`
	MaxNetworkLatency time.Duration `json:"max_network_latency"`
	ParentReadySpread time.Duration `json:"parent_ready_spread"`
	ParentReadyDelay  time.Duration `json:"parent_ready_delay"`

	CandidateBDelay  time.Duration `json:"candidate_b_delay"`
	CandidateBSpread time.Duration `json:"candidate_b_spread"`
	LatePollStart    time.Duration `json:"late_poll_start"`
	MaxVirtualTime   time.Duration `json:"max_virtual_time"`

	ExpectedProposers float64       `json:"expected_proposers"`
	GossipFanout      int           `json:"gossip_fanout"`
	GossipRelayMin    time.Duration `json:"gossip_relay_min"`
	GossipRelayMax    time.Duration `json:"gossip_relay_max"`

	FailureSwitchAfter int `json:"failure_switch_after"`
	TraceLimit         int `json:"trace_limit"`
}

                                                                                
                                                                       
func DefaultStabilityConfig() StabilityConfig {
	production := DefaultConfig().Consensus
	return StabilityConfig{
		Policy:             StabilityPolicyCurrent,
		Scenario:           StabilityScenarioPropagationRace,
		StrengthTieBreak:   StabilityStrengthTieBreakNone,
		Seed:               8483,
		Nodes:              20,
		Byzantine:          0,
		K:                  production.K,
		EarlyNodes:         3,
		AlphaPreference:    production.AlphaPreference,
		AlphaConfidence:    production.AlphaConfidence,
		Beta:               production.Beta,
		Pipeline:           maxPerHeightPollPipeline,
		PollCapacity:       maxPerHeightPollOutstanding,
		PollInterval:       production.PollInterval,
		QueryTimeout:       production.QueryTimeout,
		MinNetworkLatency:  60 * time.Millisecond,
		MaxNetworkLatency:  200 * time.Millisecond,
		ParentReadySpread:  3 * time.Second,
		ParentReadyDelay:   6 * time.Second,
		CandidateBDelay:    400 * time.Millisecond,
		CandidateBSpread:   0,
		LatePollStart:      425 * time.Millisecond,
		MaxVirtualTime:     6 * time.Second,
		ExpectedProposers:  5,
		GossipFanout:       15,
		GossipRelayMin:     50 * time.Millisecond,
		GossipRelayMax:     150 * time.Millisecond,
		FailureSwitchAfter: LegacyFailureSwitchAfter,
		TraceLimit:         5000,
	}
}

                                                                       
                                                                   
func PropagationRaceStabilityConfig() StabilityConfig {
	config := DefaultStabilityConfig()
	config.MinNetworkLatency = 5 * time.Millisecond
	config.MaxNetworkLatency = 15 * time.Millisecond
	return config
}

                                                                           
func (c StabilityConfig) Validate() error {
	switch c.Policy {
	case StabilityPolicyCurrent, StabilityPolicySticky, StabilityPolicySnowball:
	default:
		return fmt.Errorf("unknown stability policy %q", c.Policy)
	}
	switch c.normalizedScenario() {
	case StabilityScenarioPropagationRace,
		StabilityScenarioFragmented,
		StabilityScenarioVRFNetwork,
		StabilityScenarioVRFPhaseSkew,
		StabilityScenarioSingleNodeNotReady:
	default:
		return fmt.Errorf("unknown stability scenario %q", c.Scenario)
	}
	switch c.normalizedStrengthTieBreak() {
	case StabilityStrengthTieBreakNone, StabilityStrengthTieBreakCanonical:
	default:
		return fmt.Errorf("unknown strength tie-break rule %q", c.StrengthTieBreak)
	}
	if c.normalizedStrengthTieBreak() != StabilityStrengthTieBreakNone &&
		c.Policy != StabilityPolicySnowball {
		return errors.New("strength tie-break requires the snowball policy")
	}
	if c.Nodes < 2 {
		return errors.New("nodes must be at least 2")
	}
	if c.Byzantine < 0 || c.Byzantine >= c.Nodes {
		return errors.New("byzantine must be in [0, nodes)")
	}
	honest := c.Nodes - c.Byzantine
	if c.EarlyNodes < 1 || c.EarlyNodes > honest {
		return fmt.Errorf("early nodes must be in [1, %d]", honest)
	}
	if c.K < 1 {
		return errors.New("K must be positive")
	}
	if c.AlphaPreference < 1 || c.AlphaPreference > c.K {
		return errors.New("alpha preference must be in [1, K]")
	}
	if c.AlphaConfidence < c.AlphaPreference || c.AlphaConfidence > c.K {
		return errors.New("alpha confidence must be in [alpha preference, K]")
	}
	if c.AlphaConfidence <= c.K/2 {
		return errors.New("alpha confidence must be a strict sample majority")
	}
	if c.Policy == StabilityPolicySnowball && c.AlphaPreference <= c.K/2 {
		return errors.New("snowball alpha preference must be a strict sample majority")
	}
	if c.Beta < 1 {
		return errors.New("beta must be positive")
	}
	if c.Pipeline < 1 {
		return errors.New("pipeline must be positive")
	}
	if c.PollCapacity < c.Pipeline {
		return errors.New("poll capacity must be at least the network pipeline")
	}
	if c.PollInterval <= 0 {
		return errors.New("poll interval must be positive")
	}
	if c.QueryTimeout <= 0 {
		return errors.New("query timeout must be positive")
	}
	if c.MinNetworkLatency < 0 || c.MaxNetworkLatency < c.MinNetworkLatency {
		return errors.New("network latency must satisfy 0 <= min <= max")
	}
	if c.ParentReadySpread < 0 {
		return errors.New("parent ready spread must not be negative")
	}
	if c.ParentReadyDelay < 0 {
		return errors.New("parent ready delay must not be negative")
	}
	if c.CandidateBDelay < 0 || c.CandidateBSpread < 0 || c.LatePollStart < 0 {
		return errors.New("virtual delays must not be negative")
	}
	if c.normalizedScenario() == StabilityScenarioFragmented &&
		(c.Nodes != 20 || c.Byzantine != 0) {
		return errors.New("fragmented scenario requires exactly 20 honest nodes")
	}
	if c.normalizedScenario() == StabilityScenarioVRFNetwork ||
		c.normalizedScenario() == StabilityScenarioVRFPhaseSkew {
		if c.ExpectedProposers <= 0 {
			return errors.New("expected proposers must be positive")
		}
		if c.GossipFanout < 1 || c.GossipFanout > c.Nodes-1 {
			return fmt.Errorf("gossip fanout must be in [1, %d]", c.Nodes-1)
		}
		if c.GossipRelayMin < 0 || c.GossipRelayMax < c.GossipRelayMin {
			return errors.New("gossip relay delay must satisfy 0 <= min <= max")
		}
		for _, multiplier := range []float64{1, 3, 6, 10} {
			if _, _, err := EvaluateProposalEligibility(ProposalEligibilityInput{
				ExpectedProposers: c.ExpectedProposers * multiplier,
				ProposerStake:     1,
				TotalStake:        float64(c.Nodes),
				VRFOutput:         make([]byte, 32),
			}); err != nil {
				return fmt.Errorf("invalid proposal eligibility configuration: %w", err)
			}
		}
	}
	if c.MaxVirtualTime <= 0 {
		return errors.New("max virtual time must be positive")
	}
	if c.FailureSwitchAfter < 1 {
		return errors.New("failure switch threshold must be positive")
	}
	if c.TraceLimit < 0 {
		return errors.New("trace limit must not be negative")
	}
	return nil
}

func (c StabilityConfig) normalizedScenario() StabilityScenario {
	if c.Scenario == "" {
		return StabilityScenarioPropagationRace
	}
	return c.Scenario
}

func (c StabilityConfig) normalizedStrengthTieBreak() StabilityStrengthTieBreak {
	if c.StrengthTieBreak == "" {
		return StabilityStrengthTieBreakNone
	}
	return c.StrengthTieBreak
}

                                                                    
type StabilityTraceEvent struct {
	At     time.Duration `json:"at"`
	Node   int           `json:"node"`
	Kind   string        `json:"kind"`
	Detail string        `json:"detail"`
}

                                                                   
type StabilityResult struct {
	Policy   StabilityPolicy   `json:"policy"`
	Scenario StabilityScenario `json:"scenario"`
	Seed     int64             `json:"seed"`

	StrengthTieBreak StabilityStrengthTieBreak `json:"strength_tie_break"`

	SafetyOK   bool   `json:"safety_ok"`
	LivenessOK bool   `json:"liveness_ok"`
	Violation  string `json:"violation,omitempty"`

	HonestNodes            int            `json:"honest_nodes"`
	HonestFinalized        int            `json:"honest_finalized"`
	FinalizedCounts        map[string]int `json:"finalized_counts"`
	PreferenceCount        map[string]int `json:"preference_counts"`
	InitialPreferenceCount map[string]int `json:"initial_preference_counts"`
	ProposerCount          int            `json:"proposer_count"`
	CandidateCount         int            `json:"candidate_count"`

	VirtualElapsed   time.Duration `json:"virtual_elapsed"`
	FirstFinalized   time.Duration `json:"first_finalized"`
	AverageFinalized time.Duration `json:"average_finalized"`
	AllFinalized     time.Duration `json:"all_finalized"`

	AverageOneWayLatency time.Duration `json:"average_one_way_latency"`
	AverageRoundTrip     time.Duration `json:"average_round_trip"`
	OneWayLatencySamples int           `json:"one_way_latency_samples"`
	RoundTripSamples     int           `json:"round_trip_samples"`

	EventsProcessed  int `json:"events_processed"`
	PollsStarted     int `json:"polls_started"`
	PollSlotsSampled int `json:"poll_slots_sampled"`
	PollPeerRequests int `json:"poll_peer_requests"`
	StrongPolls      int `json:"strong_polls"`
	WeakPolls        int `json:"weak_polls"`

	ParentNotReadyResponses int `json:"parent_not_ready_responses"`
	MaxNetworkActivePolls   int `json:"max_network_active_polls"`
	MaxOrderedPendingPolls  int `json:"max_ordered_pending_polls"`
	MaxOutstandingPolls     int `json:"max_outstanding_polls"`
	PollCapacityBlocks      int `json:"poll_capacity_blocks"`

	Trace []StabilityTraceEvent `json:"trace,omitempty"`
}

                                                                              
                                                                              
                                                                              
func RunStabilitySimulation(config StabilityConfig) (StabilityResult, error) {
	if err := config.Validate(); err != nil {
		return StabilityResult{}, err
	}
	s := newStabilitySimulation(config)
	if s.setupErr != nil {
		return StabilityResult{}, s.setupErr
	}
	s.run()
	return s.result(), nil
}

type stabilityNode struct {
	id             int
	byzantine      bool
	known          map[string]bool
	firstCandidate string

	decision    PreferenceState
	finalizedAt time.Duration

	readyAt          time.Duration
	candidateArrival []string
	activated        map[string]bool

	networkActive int
	nextSeq       uint64
	processSeq    uint64
	pending       map[uint64]stabilityPollOutcome
}

type stabilityPoll struct {
	requester   int
	seq         uint64
	accumulator *PollAccumulator[int]
	completed   bool
}

type stabilityPollOutcome struct {
	votes map[string]int
}

type stabilitySimulation struct {
	config StabilityConfig
	random *rand.Rand
	clock  virtualClock
	nodes  []*stabilityNode

	firstFinalizedNode  int
	firstFinalizedBlock string
	violation           string
	trace               []StabilityTraceEvent
	setupErr            error

	candidates        map[string]struct{}
	candidateProposer map[string]int
	proposerNodes     map[int]struct{}
	gossipRelayed     map[string]map[int]bool

	eventsProcessed  int
	pollsStarted     int
	pollSlotsSampled int
	pollPeerRequests int
	strongPolls      int
	weakPolls        int

	parentNotReadyResponses int
	maxNetworkActivePolls   int
	maxOrderedPendingPolls  int
	maxOutstandingPolls     int
	pollCapacityBlocks      int

	oneWayLatencyTotal   time.Duration
	oneWayLatencySamples int
	roundTripTotal       time.Duration
	roundTripSamples     int
}

func newStabilitySimulation(config StabilityConfig) *stabilitySimulation {
	s := &stabilitySimulation{
		config:             config,
		random:             rand.New(rand.NewSource(config.Seed)),
		firstFinalizedNode: -1,
		candidates:         make(map[string]struct{}),
		candidateProposer:  make(map[string]int),
		proposerNodes:      make(map[int]struct{}),
		gossipRelayed:      make(map[string]map[int]bool),
	}
	s.clock.max = config.MaxVirtualTime
	heap.Init(&s.clock.events)

	honest := config.Nodes - config.Byzantine
	s.nodes = make([]*stabilityNode, config.Nodes)
	for id := 0; id < config.Nodes; id++ {
		s.nodes[id] = &stabilityNode{
			id:         id,
			byzantine:  id >= honest,
			known:      make(map[string]bool),
			activated:  make(map[string]bool),
			decision:   NewPreferenceState(),
			nextSeq:    1,
			processSeq: 1,
			pending:    make(map[uint64]stabilityPollOutcome),
		}
	}

	switch config.normalizedScenario() {
	case StabilityScenarioPropagationRace:
		s.schedulePropagationRace()
	case StabilityScenarioFragmented:
		s.scheduleFragmented()
	case StabilityScenarioVRFNetwork:
		s.scheduleVRFNetwork()
	case StabilityScenarioVRFPhaseSkew:
		s.scheduleVRFPhaseSkew()
	case StabilityScenarioSingleNodeNotReady:
		s.scheduleSingleNodeNotReady()
	}
	return s
}

func (s *stabilitySimulation) schedulePropagationRace() {
	s.registerCandidate(StabilityBlockA, -1)
	s.registerCandidate(StabilityBlockB, -1)

	                                                                       
	for id := range s.nodes {
		nodeID := id
		s.clock.schedule(0, func() {
			s.deliverCandidate(nodeID, StabilityBlockA)
		})
	}
	for id := range s.nodes {
		nodeID := id
		spread := time.Duration(0)
		if s.config.CandidateBSpread > 0 {
			spread = time.Duration(s.random.Int63n(int64(s.config.CandidateBSpread) + 1))
		}
		s.clock.schedule(s.config.CandidateBDelay+spread, func() {
			s.deliverCandidate(nodeID, StabilityBlockB)
		})
	}
	for id := range s.nodes {
		nodeID := id
		start := s.config.LatePollStart
		if id < s.config.EarlyNodes {
			start = 0
		}
		s.clock.schedule(start, func() {
			s.pollTick(nodeID)
		})
	}
}

func (s *stabilitySimulation) scheduleFragmented() {
	firstCandidate := func(nodeID int) string {
		switch {
		case nodeID < 7:
			return StabilityBlockA
		case nodeID < 13:
			return StabilityBlockB
		default:
			return StabilityBlockC
		}
	}
	candidates := []string{StabilityBlockA, StabilityBlockB, StabilityBlockC}
	proposers := []int{0, 7, 13}
	for i, candidate := range candidates {
		s.registerCandidate(candidate, proposers[i])
	}

	                                                                        
	                                                                          
	for id := range s.nodes {
		nodeID := id
		candidate := firstCandidate(id)
		s.clock.schedule(0, func() {
			s.deliverCandidate(nodeID, candidate)
		})
	}
	for id := range s.nodes {
		nodeID := id
		for _, candidate := range candidates {
			blockID := candidate
			if blockID == firstCandidate(nodeID) {
				continue
			}
			spread := time.Duration(0)
			if s.config.CandidateBSpread > 0 {
				spread = time.Duration(s.random.Int63n(int64(s.config.CandidateBSpread) + 1))
			}
			s.clock.schedule(s.config.CandidateBDelay+spread, func() {
				s.deliverCandidate(nodeID, blockID)
			})
		}
	}
	for id := range s.nodes {
		nodeID := id
		s.clock.schedule(0, func() {
			s.pollTick(nodeID)
		})
	}

	                                                                         
	                                                                     
	for i, candidate := range candidates {
		s.scheduleGossip(proposers[i], candidate, 0)
	}
}

func (s *stabilitySimulation) scheduleVRFNetwork() {
	                                                                          
	                                                                     
	                                                                        
	                                                                
	s.proposeVRFWindow(0, s.config.ExpectedProposers)
	for _, window := range []struct {
		index              int
		at                 time.Duration
		expectedMultiplier float64
	}{
		{index: 1, at: 5 * time.Second, expectedMultiplier: 3},
		{index: 2, at: 10 * time.Second, expectedMultiplier: 6},
		{index: 3, at: 20 * time.Second, expectedMultiplier: 10},
	} {
		window := window
		s.clock.schedule(window.at, func() {
			s.proposeVRFWindow(
				window.index,
				s.config.ExpectedProposers*window.expectedMultiplier,
			)
		})
	}
	for id := range s.nodes {
		nodeID := id
		s.clock.schedule(0, func() {
			s.pollTick(nodeID)
		})
	}
}

func (s *stabilitySimulation) scheduleVRFPhaseSkew() {
	s.assignParentReadyTimes()

	                                                                       
	                                                               
	                                                                       
	                                 
	for id, node := range s.nodes {
		nodeID := id
		readyAt := node.readyAt
		s.clock.schedule(readyAt, func() {
			s.addTrace(nodeID, "parent-ready", "parent commit completed")
			s.activateKnownCandidates(s.nodes[nodeID])
			s.proposeVRFNodeWindow(nodeID, 0, s.config.ExpectedProposers)
		})
	}
	for id, node := range s.nodes {
		nodeID := id
		readyAt := node.readyAt
		for _, window := range []struct {
			index              int
			after              time.Duration
			expectedMultiplier float64
		}{
			{index: 1, after: 5 * time.Second, expectedMultiplier: 3},
			{index: 2, after: 10 * time.Second, expectedMultiplier: 6},
			{index: 3, after: 20 * time.Second, expectedMultiplier: 10},
		} {
			window := window
			s.clock.schedule(readyAt+window.after, func() {
				s.proposeVRFNodeWindow(
					nodeID,
					window.index,
					s.config.ExpectedProposers*window.expectedMultiplier,
				)
			})
		}
	}
	for id, node := range s.nodes {
		nodeID := id
		s.clock.schedule(node.readyAt, func() {
			s.pollTick(nodeID)
		})
	}
}

func (s *stabilitySimulation) scheduleSingleNodeNotReady() {
	s.registerCandidate(StabilityBlockA, -1)

	                                                                       
	                                                                             
	                                         
	delayedNodeID := s.config.Nodes - s.config.Byzantine - 1
	s.nodes[delayedNodeID].readyAt = s.config.ParentReadyDelay
	for id := range s.nodes {
		nodeID := id
		s.clock.schedule(0, func() {
			s.deliverCandidate(nodeID, StabilityBlockA)
		})
	}

	                                                                          
	                                                                            
	                                                                           
	                                                
	for id, node := range s.nodes {
		nodeID := id
		readyAt := node.readyAt
		s.clock.schedule(readyAt, func() {
			if nodeID == delayedNodeID {
				s.addTrace(nodeID, "parent-ready", "parent commit completed")
			}
			s.activateKnownCandidates(s.nodes[nodeID])
			s.pollTick(nodeID)
		})
	}
}

func (s *stabilitySimulation) assignParentReadyTimes() {
	if s.config.ParentReadySpread == 0 || len(s.nodes) == 1 {
		return
	}
	random := rand.New(rand.NewSource(s.config.Seed ^ int64(0x5eed5eed)))
	for rank, nodeID := range random.Perm(len(s.nodes)) {
		s.nodes[nodeID].readyAt = time.Duration(
			int64(s.config.ParentReadySpread) * int64(rank) / int64(len(s.nodes)-1),
		)
	}
}

func (s *stabilitySimulation) proposeVRFWindow(
	window int,
	expectedProposers float64,
) {
	type proposal struct {
		nodeID    int
		candidate string
	}
	proposals := make([]proposal, 0)
	for id, node := range s.nodes {
		                                                                   
		                                                                    
		                                                                 
		if node.decision.Finalized {
			continue
		}
		                                                                  
		                                                                    
		                                                           
		if _, proposed := s.proposerNodes[id]; proposed || len(node.known) > 0 {
			continue
		}
		vrfOutput := syntheticStabilityVRF(s.config.Seed, id, window)
		eligible, _, err := EvaluateProposalEligibility(ProposalEligibilityInput{
			ExpectedProposers: expectedProposers,
			ProposerStake:     1,
			TotalStake:        float64(s.config.Nodes),
			VRFOutput:         vrfOutput,
		})
		if err != nil {
			s.setupErr = fmt.Errorf(
				"evaluate virtual proposer %d in window %d: %w",
				id,
				window,
				err,
			)
			return
		}
		if !eligible {
			continue
		}
		proposals = append(proposals, proposal{
			nodeID:    id,
			candidate: syntheticStabilityCandidate(s.config.Seed, id, window, vrfOutput),
		})
	}

	                                                                   
	                                                                         
	                                                                      
	for _, proposal := range proposals {
		s.registerCandidate(proposal.candidate, proposal.nodeID)
		s.deliverCandidate(proposal.nodeID, proposal.candidate)
	}
	for _, proposal := range proposals {
		s.scheduleGossip(proposal.nodeID, proposal.candidate, s.clock.now)
	}
}

func (s *stabilitySimulation) proposeVRFNodeWindow(
	nodeID int,
	window int,
	expectedProposers float64,
) {
	node := s.nodes[nodeID]
	if node.decision.Finalized || s.clock.now < node.readyAt {
		return
	}
	if _, proposed := s.proposerNodes[nodeID]; proposed || len(node.known) > 0 {
		return
	}
	vrfOutput := syntheticStabilityVRF(s.config.Seed, nodeID, window)
	eligible, _, err := EvaluateProposalEligibility(ProposalEligibilityInput{
		ExpectedProposers: expectedProposers,
		ProposerStake:     1,
		TotalStake:        float64(s.config.Nodes),
		VRFOutput:         vrfOutput,
	})
	if err != nil {
		s.setupErr = fmt.Errorf(
			"evaluate virtual proposer %d in window %d: %w",
			nodeID,
			window,
			err,
		)
		return
	}
	if !eligible {
		return
	}
	candidate := syntheticStabilityCandidate(s.config.Seed, nodeID, window, vrfOutput)
	s.registerCandidate(candidate, nodeID)
	s.deliverCandidate(nodeID, candidate)
	s.scheduleGossip(nodeID, candidate, s.clock.now)
}

func (s *stabilitySimulation) registerCandidate(blockID string, proposer int) {
	if blockID == "" {
		return
	}
	if _, exists := s.candidates[blockID]; !exists {
		s.candidates[blockID] = struct{}{}
	}
	if proposer < 0 {
		return
	}
	s.candidateProposer[blockID] = proposer
	s.proposerNodes[proposer] = struct{}{}
}

func syntheticStabilityVRF(seed int64, nodeID, window int) []byte {
	sum := sha256.Sum256([]byte(fmt.Sprintf(
		"consensus-sim/vrf/%d/%d/%d",
		seed,
		nodeID,
		window,
	)))
	return append([]byte(nil), sum[:]...)
}

func syntheticStabilityCandidate(seed int64, nodeID, window int, vrfOutput []byte) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf(
		"consensus-sim/block/%d/%d/%d/%x",
		seed,
		nodeID,
		window,
		vrfOutput,
	)))
	return fmt.Sprintf("block-1-node-%02d-w%d-%x", nodeID, window, sum[:])
}

func (s *stabilitySimulation) run() {
	for s.clock.events.Len() > 0 {
		event := heap.Pop(&s.clock.events).(*virtualEvent)
		if event.at > s.config.MaxVirtualTime {
			s.clock.now = s.config.MaxVirtualTime
			break
		}
		s.clock.now = event.at
		event.action()
		s.eventsProcessed++
		if s.allHonestFinalized() {
			return
		}
	}
	if !s.allHonestFinalized() && s.clock.now < s.config.MaxVirtualTime {
		s.clock.now = s.config.MaxVirtualTime
	}
}

func (s *stabilitySimulation) deliverCandidate(nodeID int, blockID string) bool {
	node := s.nodes[nodeID]
	if node.known[blockID] {
		return false
	}
	node.known[blockID] = true
	node.candidateArrival = append(node.candidateArrival, blockID)
	if node.firstCandidate == "" {
		node.firstCandidate = blockID
	}
	s.addTrace(nodeID, "candidate", fmt.Sprintf("received %s", blockID))
	if s.clock.now < node.readyAt {
		s.addTrace(nodeID, "candidate-deferred", fmt.Sprintf(
			"waiting for parent until %s",
			node.readyAt,
		))
		return true
	}
	s.activateCandidate(node, blockID)
	return true
}

func (s *stabilitySimulation) activateKnownCandidates(node *stabilityNode) {
	for _, blockID := range node.candidateArrival {
		s.activateCandidate(node, blockID)
	}
}

func (s *stabilitySimulation) activateCandidate(node *stabilityNode, blockID string) {
	if node.activated[blockID] {
		return
	}
	node.activated[blockID] = true
	if s.config.Policy == StabilityPolicySnowball {
		previous := node.decision.Preference
		node.decision = AddSnowballCandidate(node.decision, blockID)
		tieBreakChanged := false
		if s.config.normalizedStrengthTieBreak() == StabilityStrengthTieBreakCanonical {
			node.decision, tieBreakChanged = applyCanonicalStrengthTieBreak(node.decision)
		}
		if previous != node.decision.Preference {
			reason := PreferenceChangeInitialFallback
			if previous != "" && tieBreakChanged {
				reason = PreferenceChangeReason("canonical-strength-tie")
			}
			s.addTrace(node.id, "preference", fmt.Sprintf(
				"%s -> %s (%s)",
				stabilityBlockLabel(previous),
				stabilityBlockLabel(node.decision.Preference),
				reason,
			))
		}
	}
}

func applyCanonicalStrengthTieBreak(current PreferenceState) (PreferenceState, bool) {
	next := current.Clone()
	if current.Finalized {
		return next, false
	}
	canonical := selectStrongestPreference(next.Strength)
	if canonical == "" || canonical == next.Preference {
		return next, false
	}
	next.Preference = canonical
	return next, true
}

func (s *stabilitySimulation) pollTick(nodeID int) {
	node := s.nodes[nodeID]
	if !node.decision.Finalized && s.clock.now >= node.readyAt && len(node.known) > 0 {
		outstanding := node.networkActive + len(node.pending)
		switch {
		case outstanding >= s.config.PollCapacity:
			s.pollCapacityBlocks++
		case node.networkActive < s.config.Pipeline:
			s.startPoll(node)
		}
	}
	if !node.decision.Finalized {
		s.clock.schedule(s.clock.now+s.config.PollInterval, func() {
			s.pollTick(nodeID)
		})
	}
}

func (s *stabilitySimulation) startPoll(node *stabilityNode) {
	peers := s.samplePollPeers(node.id, s.config.K)
	sampleWeights := make(map[int]int, len(peers))
	for _, peer := range peers {
		sampleWeights[peer.Value] = peer.Weight
	}
	startCandidates := make(map[string]struct{}, len(node.known))
	for candidate, known := range node.known {
		if known {
			startCandidates[candidate] = struct{}{}
		}
	}
	accumulator, err := NewPollAccumulator(
		s.config.K,
		s.config.AlphaPreference,
		s.config.AlphaConfidence,
		sampleWeights,
		startCandidates,
	)
	if err != nil {
		panic(fmt.Sprintf("validated simulator produced invalid poll: %v", err))
	}
	queryCandidate := node.decision.Preference
	pushCandidate := ""
	if proposer, exists := s.candidateProposer[queryCandidate]; exists && proposer == node.id {
		pushCandidate = queryCandidate
	}
	poll := &stabilityPoll{
		requester:   node.id,
		seq:         node.nextSeq,
		accumulator: accumulator,
	}
	node.nextSeq++
	node.networkActive++
	s.observePollDepth(node)
	s.pollsStarted++
	s.pollSlotsSampled += s.config.K
	s.pollPeerRequests += len(peers)

	for _, peer := range peers {
		peerID := peer.Value
		outbound := s.networkLatency()
		inbound := s.networkLatency()
		s.oneWayLatencyTotal += outbound + inbound
		s.oneWayLatencySamples += 2
		s.roundTripTotal += outbound + inbound
		s.roundTripSamples++
		requestAt := s.clock.now + outbound
		s.clock.schedule(requestAt, func() {
			if pushCandidate != "" && s.deliverCandidate(peerID, pushCandidate) {
				s.scheduleGossip(
					peerID,
					pushCandidate,
					requestAt+s.gossipRelayLatency(),
				)
			}
			if pushCandidate != "" || queryCandidate == "" || s.nodes[peerID].known[queryCandidate] {
				s.scheduleChitResponse(poll, peerID, node.id, requestAt+inbound)
				return
			}

			                                                               
			                                                                    
			                                                                   
			                                         
			s.addTrace(peerID, "get", fmt.Sprintf(
				"requested %s from node %d",
				stabilityBlockLabel(queryCandidate),
				node.id,
			))
			getLatency := s.networkLatency()
			putLatency := s.networkLatency()
			s.oneWayLatencyTotal += getLatency + putLatency
			s.oneWayLatencySamples += 2
			s.roundTripTotal += getLatency + putLatency
			putAt := requestAt + getLatency + putLatency
			s.clock.schedule(putAt, func() {
				s.addTrace(peerID, "put", fmt.Sprintf(
					"received %s from node %d",
					stabilityBlockLabel(queryCandidate),
					node.id,
				))
				if s.deliverCandidate(peerID, queryCandidate) {
					s.scheduleGossip(
						peerID,
						queryCandidate,
						putAt+s.gossipRelayLatency(),
					)
				}
				s.scheduleChitResponse(poll, peerID, node.id, putAt+inbound)
			})
		})
	}
	s.clock.schedule(s.clock.now+s.config.QueryTimeout, func() {
		s.completePoll(poll)
	})
}

func (s *stabilitySimulation) scheduleChitResponse(
	poll *stabilityPoll,
	responderID int,
	requesterID int,
	at time.Duration,
) {
	vote := s.chit(responderID, requesterID)
	s.clock.schedule(at, func() {
		s.receivePollResponse(poll, responderID, vote)
	})
}

func (s *stabilitySimulation) scheduleGossip(from int, blockID string, at time.Duration) {
	if from < 0 || from >= len(s.nodes) || blockID == "" || at > s.config.MaxVirtualTime {
		return
	}
	relayed := s.gossipRelayed[blockID]
	if relayed == nil {
		relayed = make(map[int]bool)
		s.gossipRelayed[blockID] = relayed
	}
	if relayed[from] {
		return
	}
	relayed[from] = true

	fanout := s.config.GossipFanout
	if fanout <= 0 {
		return
	}
	if fanout > len(s.nodes)-1 {
		fanout = len(s.nodes) - 1
	}
	for _, peer := range s.samplePeers(from, fanout) {
		peerID := peer
		arrival := at + s.networkLatency()
		s.clock.schedule(arrival, func() {
			if s.deliverCandidate(peerID, blockID) {
				s.scheduleGossip(
					peerID,
					blockID,
					arrival+s.gossipRelayLatency(),
				)
			}
		})
	}
}

func (s *stabilitySimulation) receivePollResponse(
	poll *stabilityPoll,
	responderID int,
	vote string,
) {
	if poll.completed {
		return
	}
	result := poll.accumulator.Record(responderID, vote)
	if result.Completed {
		s.completePoll(poll)
	}
}

func (s *stabilitySimulation) completePoll(poll *stabilityPoll) {
	if poll.completed {
		return
	}
	poll.completed = true
	node := s.nodes[poll.requester]
	if node.networkActive > 0 {
		node.networkActive--
	}
	outcome := poll.accumulator.Close()
	node.pending[poll.seq] = stabilityPollOutcome{votes: outcome.Votes}
	s.observePollDepth(node)
	s.processCompletedPolls(node)
}

func (s *stabilitySimulation) observePollDepth(node *stabilityNode) {
	if node.networkActive > s.maxNetworkActivePolls {
		s.maxNetworkActivePolls = node.networkActive
	}
	if len(node.pending) > s.maxOrderedPendingPolls {
		s.maxOrderedPendingPolls = len(node.pending)
	}
	outstanding := node.networkActive + len(node.pending)
	if outstanding > s.maxOutstandingPolls {
		s.maxOutstandingPolls = outstanding
	}
}

func (s *stabilitySimulation) processCompletedPolls(node *stabilityNode) {
	for {
		outcome, ok := node.pending[node.processSeq]
		if !ok {
			return
		}
		delete(node.pending, node.processSeq)
		node.processSeq++
		if node.decision.Finalized {
			continue
		}
		s.applyPreferencePoll(node, outcome)
	}
}

func (s *stabilitySimulation) applyPreferencePoll(node *stabilityNode, outcome stabilityPollOutcome) {
	mode := PreferenceModeSnowflake
	alphaPreference := s.config.AlphaConfidence
	if s.config.Policy == StabilityPolicySnowball {
		mode = PreferenceModeSnowball
		alphaPreference = s.config.AlphaPreference
	}
	previousPreference := node.decision.Preference
	next, transition := AdvancePreferenceState(node.decision, PreferencePollInput{
		Mode:               mode,
		Candidates:         s.knownCandidates(node),
		Votes:              outcome.votes,
		AlphaPreference:    alphaPreference,
		AlphaConfidence:    s.config.AlphaConfidence,
		FailureSwitchAfter: s.config.FailureSwitchAfter,
	})
	preCanonicalPreference := next.Preference
	if s.config.normalizedStrengthTieBreak() == StabilityStrengthTieBreakCanonical {
		next, _ = applyCanonicalStrengthTieBreak(next)
	}
	node.decision = next

	if previousPreference != node.decision.Preference {
		reason := transition.ChangeReason
		if preCanonicalPreference != node.decision.Preference || !transition.PreferenceChanged {
			reason = PreferenceChangeReason("canonical-strength-tie")
		}
		s.addTrace(node.id, "preference", fmt.Sprintf(
			"%s -> %s (%s)",
			stabilityBlockLabel(previousPreference),
			stabilityBlockLabel(node.decision.Preference),
			reason,
		))
	}
	if transition.StrongPoll {
		s.strongPolls++
		detail := fmt.Sprintf(
			"%s=%d confidence=%d",
			stabilityBlockLabel(transition.Winner),
			transition.WinnerVotes,
			node.decision.Confidence,
		)
		if mode == PreferenceModeSnowball {
			detail = fmt.Sprintf(
				"%s=%d strength=%d preference=%s confidence_for=%s confidence=%d",
				stabilityBlockLabel(transition.Winner),
				transition.WinnerVotes,
				node.decision.Strength[transition.Winner],
				stabilityBlockLabel(node.decision.Preference),
				stabilityBlockLabel(node.decision.ConfidencePreference),
				node.decision.Confidence,
			)
		}
		s.addTrace(node.id, "strong-poll", detail)
	} else {
		s.weakPolls++
		s.addTrace(node.id, "weak-poll", fmt.Sprintf(
			"winner=%s votes=%d failures=%d",
			stabilityBlockLabel(transition.Winner),
			transition.WinnerVotes,
			node.decision.ConsecutiveFailures,
		))
	}
	s.finalizeIfReady(node)
}

func (s *stabilitySimulation) finalizeIfReady(node *stabilityNode) {
	if !CanFinalizePreferenceState(node.decision, s.config.Beta) {
		return
	}
	s.finalize(node, preferenceToFinalize(node.decision))
}

func (s *stabilitySimulation) finalize(node *stabilityNode, blockID string) {
	if node.decision.Finalized || blockID == "" || preferenceToFinalize(node.decision) != blockID {
		return
	}
	node.decision = FinalizePreferenceState(node.decision)
	node.finalizedAt = s.clock.now
	s.addTrace(node.id, "finalized", stabilityBlockLabel(blockID))
	if node.byzantine {
		return
	}
	if s.firstFinalizedBlock == "" {
		s.firstFinalizedBlock = blockID
		s.firstFinalizedNode = node.id
		return
	}
	if blockID != s.firstFinalizedBlock && s.violation == "" {
		s.violation = fmt.Sprintf(
			"honest node %d finalized %s after honest node %d finalized %s",
			node.id,
			stabilityBlockLabel(blockID),
			s.firstFinalizedNode,
			stabilityBlockLabel(s.firstFinalizedBlock),
		)
		s.addTrace(-1, "safety-violation", s.violation)
	}
}

func (s *stabilitySimulation) chit(responderID, requesterID int) string {
	node := s.nodes[responderID]
	if s.clock.now < node.readyAt {
		s.parentNotReadyResponses++
		s.addTrace(responderID, "parent-not-ready", fmt.Sprintf(
			"abstained from node %d next-height poll",
			requesterID,
		))
		return ""
	}
	if node.byzantine {
		                                                                     
		preferred := StabilityBlockB
		if requesterID < s.config.EarlyNodes {
			preferred = StabilityBlockA
		}
		if node.known[preferred] {
			return preferred
		}
		_, chit := ResolvePreferenceChit(NewPreferenceState(), PreferenceChitInput{
			Candidates: s.knownCandidates(node),
		})
		return chit.Preference
	}
	if node.decision.Finalized {
		return node.decision.Preference
	}
	if s.config.Policy == StabilityPolicySnowball {
		return node.decision.Preference
	}
	next, transition := ResolvePreferenceChit(node.decision, PreferenceChitInput{
		Candidates:      s.knownCandidates(node),
		PersistFallback: s.config.Policy == StabilityPolicySticky,
	})
	node.decision = next
	if transition.PreferenceChanged {
		s.addTrace(node.id, "preference", fmt.Sprintf(
			"- -> %s (%s)",
			stabilityBlockLabel(transition.Preference),
			transition.ChangeReason,
		))
	}
	return transition.Preference
}

func (s *stabilitySimulation) knownCandidates(node *stabilityNode) []string {
	candidates := make([]string, 0, len(node.known))
	for blockID := range node.known {
		candidates = append(candidates, blockID)
	}
	sort.Strings(candidates)
	return candidates
}

func (s *stabilitySimulation) samplePeers(exclude, count int) []int {
	peers := make([]int, 0, len(s.nodes)-1)
	for id := range s.nodes {
		if id != exclude {
			peers = append(peers, id)
		}
	}
	for i := 0; i < count; i++ {
		j := i + s.random.Intn(len(peers)-i)
		peers[i], peers[j] = peers[j], peers[i]
	}
	return peers[:count]
}

func (s *stabilitySimulation) samplePollPeers(exclude, slots int) []WeightedSample[int] {
	population := make([]int, 0, len(s.nodes)-1)
	for id := range s.nodes {
		if id != exclude {
			population = append(population, id)
		}
	}
	seed := make([]byte, 32)
	_, _ = s.random.Read(seed)
	return BuildReplacementSample(population, slots, seed)
}

func (s *stabilitySimulation) networkLatency() time.Duration {
	if s.config.MaxNetworkLatency == s.config.MinNetworkLatency {
		return s.config.MinNetworkLatency
	}
	width := int64(s.config.MaxNetworkLatency-s.config.MinNetworkLatency) + 1
	return s.config.MinNetworkLatency + time.Duration(s.random.Int63n(width))
}

func (s *stabilitySimulation) gossipRelayLatency() time.Duration {
	if s.config.GossipRelayMax <= s.config.GossipRelayMin {
		return s.config.GossipRelayMin
	}
	width := int64(s.config.GossipRelayMax-s.config.GossipRelayMin) + 1
	return s.config.GossipRelayMin + time.Duration(s.random.Int63n(width))
}

func (s *stabilitySimulation) allHonestFinalized() bool {
	for _, node := range s.nodes {
		if !node.byzantine && !node.decision.Finalized {
			return false
		}
	}
	return true
}

func (s *stabilitySimulation) addTrace(node int, kind, detail string) {
	if s.config.TraceLimit == 0 || len(s.trace) >= s.config.TraceLimit {
		return
	}
	s.trace = append(s.trace, StabilityTraceEvent{
		At:     s.clock.now,
		Node:   node,
		Kind:   kind,
		Detail: detail,
	})
}

func (s *stabilitySimulation) result() StabilityResult {
	finalized := make(map[string]int)
	preferences := make(map[string]int)
	initialPreferences := make(map[string]int)
	honestFinalized := 0
	var firstFinalized time.Duration
	var totalFinalized time.Duration
	var lastFinalized time.Duration
	for _, node := range s.nodes {
		if node.byzantine {
			continue
		}
		if node.decision.Finalized {
			finalized[node.decision.Preference]++
			honestFinalized++
			totalFinalized += node.finalizedAt
			if firstFinalized == 0 || node.finalizedAt < firstFinalized {
				firstFinalized = node.finalizedAt
			}
			if node.finalizedAt > lastFinalized {
				lastFinalized = node.finalizedAt
			}
		}
		preference := node.decision.Preference
		if preference == "" {
			preference = "<none>"
		}
		preferences[preference]++
		firstCandidate := node.firstCandidate
		if firstCandidate == "" {
			firstCandidate = "<none>"
		}
		initialPreferences[firstCandidate]++
	}
	honest := s.config.Nodes - s.config.Byzantine
	safetyOK := len(finalized) <= 1
	livenessOK := honestFinalized == honest && len(finalized) == 1
	averageFinalized := time.Duration(0)
	if honestFinalized > 0 {
		averageFinalized = totalFinalized / time.Duration(honestFinalized)
	}
	allFinalized := time.Duration(0)
	if honestFinalized == honest {
		allFinalized = lastFinalized
	}
	averageOneWayLatency := time.Duration(0)
	if s.oneWayLatencySamples > 0 {
		averageOneWayLatency = s.oneWayLatencyTotal / time.Duration(s.oneWayLatencySamples)
	}
	averageRoundTrip := time.Duration(0)
	if s.roundTripSamples > 0 {
		averageRoundTrip = s.roundTripTotal / time.Duration(s.roundTripSamples)
	}
	return StabilityResult{
		Policy:                  s.config.Policy,
		Scenario:                s.config.normalizedScenario(),
		StrengthTieBreak:        s.config.normalizedStrengthTieBreak(),
		Seed:                    s.config.Seed,
		SafetyOK:                safetyOK,
		LivenessOK:              livenessOK,
		Violation:               s.violation,
		HonestNodes:             honest,
		HonestFinalized:         honestFinalized,
		FinalizedCounts:         finalized,
		PreferenceCount:         preferences,
		InitialPreferenceCount:  initialPreferences,
		ProposerCount:           len(s.proposerNodes),
		CandidateCount:          len(s.candidates),
		VirtualElapsed:          s.clock.now,
		FirstFinalized:          firstFinalized,
		AverageFinalized:        averageFinalized,
		AllFinalized:            allFinalized,
		AverageOneWayLatency:    averageOneWayLatency,
		AverageRoundTrip:        averageRoundTrip,
		OneWayLatencySamples:    s.oneWayLatencySamples,
		RoundTripSamples:        s.roundTripSamples,
		EventsProcessed:         s.eventsProcessed,
		PollsStarted:            s.pollsStarted,
		PollSlotsSampled:        s.pollSlotsSampled,
		PollPeerRequests:        s.pollPeerRequests,
		StrongPolls:             s.strongPolls,
		WeakPolls:               s.weakPolls,
		ParentNotReadyResponses: s.parentNotReadyResponses,
		MaxNetworkActivePolls:   s.maxNetworkActivePolls,
		MaxOrderedPendingPolls:  s.maxOrderedPendingPolls,
		MaxOutstandingPolls:     s.maxOutstandingPolls,
		PollCapacityBlocks:      s.pollCapacityBlocks,
		Trace:                   append([]StabilityTraceEvent(nil), s.trace...),
	}
}

func stabilityBlockLabel(value string) string {
	switch value {
	case StabilityBlockA:
		return "A"
	case StabilityBlockB:
		return "B"
	case StabilityBlockC:
		return "C"
	}
	if value == "" {
		return "-"
	}
	return value
}

type virtualEvent struct {
	at     time.Duration
	order  uint64
	action func()
}

type virtualEventHeap []*virtualEvent

func (h virtualEventHeap) Len() int { return len(h) }

func (h virtualEventHeap) Less(i, j int) bool {
	if h[i].at == h[j].at {
		return h[i].order < h[j].order
	}
	return h[i].at < h[j].at
}

func (h virtualEventHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *virtualEventHeap) Push(value any) {
	*h = append(*h, value.(*virtualEvent))
}

func (h *virtualEventHeap) Pop() any {
	old := *h
	last := len(old) - 1
	event := old[last]
	old[last] = nil
	*h = old[:last]
	return event
}

type virtualClock struct {
	now       time.Duration
	max       time.Duration
	nextOrder uint64
	events    virtualEventHeap
}

func (c *virtualClock) schedule(at time.Duration, action func()) {
	if at < c.now {
		panic("virtual clock cannot schedule an event in the past")
	}
	if at > c.max {
		return
	}
	c.nextOrder++
	heap.Push(&c.events, &virtualEvent{
		at:     at,
		order:  c.nextOrder,
		action: action,
	})
}

                                                                        
func SortedStabilityCounts(counts map[string]int) []string {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, fmt.Sprintf("%s=%d", stabilityBlockLabel(key), counts[key]))
	}
	return result
}
