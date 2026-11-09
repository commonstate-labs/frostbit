package consensus

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDefaultStabilityConfigUsesProductionPollDefaults(t *testing.T) {
	production := DefaultConfig().Consensus
	simulation := DefaultStabilityConfig()
	if simulation.K != production.K ||
		simulation.AlphaPreference != production.AlphaPreference ||
		simulation.AlphaConfidence != production.AlphaConfidence ||
		simulation.Beta != production.Beta ||
		simulation.PollInterval != production.PollInterval ||
		simulation.QueryTimeout != production.QueryTimeout {
		t.Fatalf("simulator poll defaults drifted from production: simulation=%+v production=%+v",
			simulation, production)
	}
	if simulation.Pipeline != maxPerHeightPollPipeline {
		t.Fatalf(
			"simulator pipeline=%d, want production per-height limit %d",
			simulation.Pipeline,
			maxPerHeightPollPipeline,
		)
	}
	if simulation.PollCapacity != maxPerHeightPollOutstanding {
		t.Fatalf(
			"simulator poll capacity=%d, want production outstanding limit %d",
			simulation.PollCapacity,
			maxPerHeightPollOutstanding,
		)
	}
}

func TestStabilitySimulatorCurrentPolicyReproducesPropagationFork(t *testing.T) {
	config := PropagationRaceStabilityConfig()
	config.Policy = StabilityPolicyCurrent

	result, err := RunStabilitySimulation(config)
	if err != nil {
		t.Fatal(err)
	}
	if result.SafetyOK {
		t.Fatalf("expected a safety violation, got finalized=%v", result.FinalizedCounts)
	}
	if result.FinalizedCounts[StabilityBlockA] == 0 ||
		result.FinalizedCounts[StabilityBlockB] == 0 {
		t.Fatalf("expected both A and B to finalize under the unsafe historical policy, got %v",
			result.FinalizedCounts)
	}
}

func TestStabilitySimulatorPersistentPreferencePoliciesConverge(t *testing.T) {
	for _, policy := range []StabilityPolicy{StabilityPolicySticky, StabilityPolicySnowball} {
		t.Run(string(policy), func(t *testing.T) {
			config := PropagationRaceStabilityConfig()
			config.Policy = policy

			result, err := RunStabilitySimulation(config)
			if err != nil {
				t.Fatal(err)
			}
			if !result.SafetyOK || !result.LivenessOK {
				t.Fatalf("expected safe convergence, got safety=%v liveness=%v finalized=%v violation=%s",
					result.SafetyOK, result.LivenessOK, result.FinalizedCounts, result.Violation)
			}
			if result.HonestFinalized != config.Nodes-config.Byzantine {
				t.Fatalf("not all honest nodes finalized: %+v", result)
			}
		})
	}
}

func TestStabilitySimulatorSnowballInitializesOnFirstCandidateDelivery(t *testing.T) {
	config := DefaultStabilityConfig()
	config.Policy = StabilityPolicySnowball
	simulation := newStabilitySimulation(config)
	node := simulation.nodes[0]

	simulation.deliverCandidate(node.id, StabilityBlockA)
	if node.decision.Preference != StabilityBlockA ||
		node.decision.ConfidencePreference != StabilityBlockA {
		t.Fatalf("first delivered candidate did not initialize Snowball preference: %+v", node.decision)
	}
	if node.decision.Confidence != 0 || node.decision.Strength[StabilityBlockA] != 0 {
		t.Fatalf("candidate delivery incorrectly added poll confidence: %+v", node.decision)
	}

	simulation.deliverCandidate(node.id, StabilityBlockB)
	if node.decision.Preference != StabilityBlockA {
		t.Fatalf("later lower-hash candidate changed preference without a poll: %+v", node.decision)
	}
	if len(simulation.trace) < 2 ||
		simulation.trace[0].Kind != "candidate" ||
		simulation.trace[1].Kind != "preference" {
		t.Fatalf("candidate/preference trace order changed: %+v", simulation.trace)
	}
}

func TestStabilitySimulatorSnowballRequiresMajorityThresholds(t *testing.T) {
	config := DefaultStabilityConfig()
	config.Policy = StabilityPolicySnowball
	config.AlphaPreference = config.K / 2
	if err := config.Validate(); err == nil {
		t.Fatal("expected non-majority Snowball preference threshold to be rejected")
	}

	config = DefaultStabilityConfig()
	config.Policy = StabilityPolicyCurrent
	config.AlphaPreference = 1
	config.AlphaConfidence = config.K / 2
	if err := config.Validate(); err == nil {
		t.Fatal("expected non-majority confidence threshold to be rejected")
	}

	config = DefaultStabilityConfig()
	config.PollCapacity = config.Pipeline - 1
	if err := config.Validate(); err == nil {
		t.Fatal("expected poll capacity below the network pipeline to be rejected")
	}
}

func TestStabilitySimulatorSamplingAllowsMoreSlotsThanPeers(t *testing.T) {
	config := DefaultStabilityConfig()
	config.Nodes = 2
	config.EarlyNodes = 1
	config.K = 15
	config.AlphaPreference = 8
	config.AlphaConfidence = 12
	if err := config.Validate(); err != nil {
		t.Fatalf("with-replacement slots should allow K larger than the peer population: %v", err)
	}
}

func TestStabilitySimulatorReplacementPollUsesOneWeightedPeerResponse(t *testing.T) {
	config := DefaultStabilityConfig()
	config.Policy = StabilityPolicySnowball
	config.Nodes = 2
	config.K = 15
	config.AlphaPreference = 8
	config.AlphaConfidence = 12
	config.MinNetworkLatency = 0
	config.MaxNetworkLatency = 0
	config.QueryTimeout = 10 * time.Millisecond
	config.MaxVirtualTime = 20 * time.Millisecond
	config.TraceLimit = 0

	simulation := newStabilitySimulation(config)
	simulation.clock.events = virtualEventHeap{}
	simulation.clock.nextOrder = 0
	simulation.deliverCandidate(0, StabilityBlockA)
	simulation.deliverCandidate(1, StabilityBlockA)
	simulation.startPoll(simulation.nodes[0])
	simulation.run()

	if simulation.pollSlotsSampled != config.K || simulation.pollPeerRequests != 1 {
		t.Fatalf(
			"slots/requests=%d/%d, want %d/1",
			simulation.pollSlotsSampled,
			simulation.pollPeerRequests,
			config.K,
		)
	}
	state := simulation.nodes[0].decision
	if state.Strength[StabilityBlockA] != 1 || state.Confidence != 1 {
		t.Fatalf("one weighted response did not carry all 15 slots: %+v", state)
	}
}

func TestStabilitySimulatorReplacementPollDropsAllTimedOutPeerSlots(t *testing.T) {
	config := DefaultStabilityConfig()
	config.Policy = StabilityPolicySnowball
	config.Nodes = 2
	config.K = 15
	config.AlphaPreference = 8
	config.AlphaConfidence = 12
	config.MinNetworkLatency = 10 * time.Millisecond
	config.MaxNetworkLatency = 10 * time.Millisecond
	config.QueryTimeout = time.Millisecond
	config.MaxVirtualTime = 30 * time.Millisecond
	config.TraceLimit = 0

	simulation := newStabilitySimulation(config)
	simulation.clock.events = virtualEventHeap{}
	simulation.clock.nextOrder = 0
	simulation.deliverCandidate(0, StabilityBlockA)
	simulation.deliverCandidate(1, StabilityBlockA)
	simulation.startPoll(simulation.nodes[0])
	simulation.run()

	state := simulation.nodes[0].decision
	if state.Strength[StabilityBlockA] != 0 || state.Confidence != 0 {
		t.Fatalf("late peer response resurrected timed-out slots: %+v", state)
	}
	if simulation.weakPolls != 1 {
		t.Fatalf("timed-out poll weak count=%d, want 1", simulation.weakPolls)
	}
}

func TestStabilitySimulatorSlowerPollingMitigatesDefaultRace(t *testing.T) {
	config := PropagationRaceStabilityConfig()
	config.Policy = StabilityPolicyCurrent
	config.PollInterval = 125 * time.Millisecond
	config.MaxVirtualTime = 8 * time.Second

	result, err := RunStabilitySimulation(config)
	if err != nil {
		t.Fatal(err)
	}
	if !result.SafetyOK || !result.LivenessOK {
		t.Fatalf("expected this timing to converge, got safety=%v liveness=%v finalized=%v",
			result.SafetyOK, result.LivenessOK, result.FinalizedCounts)
	}
}

func TestStabilitySimulatorSeedIsReplayable(t *testing.T) {
	config := DefaultStabilityConfig()
	first, err := RunStabilitySimulation(config)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RunStabilitySimulation(config)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("same configuration and seed produced different results")
	}
}

func TestStabilitySimulatorUsesVirtualTime(t *testing.T) {
	config := DefaultStabilityConfig()
	result, err := RunStabilitySimulation(config)
	if err != nil {
		t.Fatal(err)
	}
	if result.VirtualElapsed <= 0 {
		t.Fatalf("expected consensus events to advance virtual time, got %s", result.VirtualElapsed)
	}
	if result.VirtualElapsed > config.MaxVirtualTime {
		t.Fatalf("virtual clock exceeded its budget: %s", result.VirtualElapsed)
	}
}

func TestStabilitySimulatorReportsFinalizationAndNetworkAverages(t *testing.T) {
	config := DefaultStabilityConfig()
	result, err := RunStabilitySimulation(config)
	if err != nil {
		t.Fatal(err)
	}

	if result.FirstFinalized <= 0 ||
		result.FirstFinalized > result.AverageFinalized ||
		result.AverageFinalized > result.AllFinalized {
		t.Fatalf("invalid finalization timing order: first=%s average=%s all=%s",
			result.FirstFinalized, result.AverageFinalized, result.AllFinalized)
	}
	if result.AllFinalized != result.VirtualElapsed {
		t.Fatalf("completed simulation should stop at all-finalized time: all=%s elapsed=%s",
			result.AllFinalized, result.VirtualElapsed)
	}
	if result.AverageOneWayLatency < config.MinNetworkLatency ||
		result.AverageOneWayLatency > config.MaxNetworkLatency {
		t.Fatalf("average one-way latency outside configured range: %s", result.AverageOneWayLatency)
	}
	if result.AverageRoundTrip < 2*config.MinNetworkLatency ||
		result.AverageRoundTrip > 2*config.MaxNetworkLatency {
		t.Fatalf("average RTT outside configured range: %s", result.AverageRoundTrip)
	}
	if result.RoundTripSamples == 0 || result.OneWayLatencySamples != 2*result.RoundTripSamples {
		t.Fatalf("invalid latency sample counts: one-way=%d round-trip=%d",
			result.OneWayLatencySamples, result.RoundTripSamples)
	}
}

func TestStabilitySimulatorResolvesFragmentedScenario(t *testing.T) {
	config := DefaultStabilityConfig()
	config.Policy = StabilityPolicySnowball
	config.Scenario = StabilityScenarioFragmented
	config.StrengthTieBreak = StabilityStrengthTieBreakNone
	config.TraceLimit = 0

	result, err := RunStabilitySimulation(config)
	if err != nil {
		t.Fatal(err)
	}
	if !result.SafetyOK || !result.LivenessOK {
		t.Fatalf(
			"production sampling did not safely resolve 7/6/7: safety=%v liveness=%v finalized=%v violation=%s",
			result.SafetyOK,
			result.LivenessOK,
			result.FinalizedCounts,
			result.Violation,
		)
	}
	wantInitial := map[string]int{
		StabilityBlockA: 7,
		StabilityBlockB: 6,
		StabilityBlockC: 7,
	}
	if !reflect.DeepEqual(result.InitialPreferenceCount, wantInitial) {
		t.Fatalf("initial split = %v, want %v", result.InitialPreferenceCount, wantInitial)
	}
	if len(result.FinalizedCounts) != 1 || result.HonestFinalized != config.Nodes {
		t.Fatalf("expected one candidate finalized by all honest nodes, got %v", result.FinalizedCounts)
	}
	if result.StrongPolls == 0 {
		t.Fatal("replacement slots never produced a soft/hard majority")
	}
	if result.PollSlotsSampled != result.PollsStarted*config.K {
		t.Fatalf(
			"sampled slots=%d, want polls(%d)*K(%d)",
			result.PollSlotsSampled,
			result.PollsStarted,
			config.K,
		)
	}
	if result.PollPeerRequests >= result.PollSlotsSampled {
		t.Fatalf(
			"duplicate slots were not aggregated: peer requests=%d slots=%d",
			result.PollPeerRequests,
			result.PollSlotsSampled,
		)
	}
}

func TestStabilitySimulatorCanonicalStrengthTieBreakCanForkOnLateBetterCandidate(t *testing.T) {
	config := PropagationRaceStabilityConfig()
	config.Policy = StabilityPolicySnowball
	config.StrengthTieBreak = StabilityStrengthTieBreakCanonical
	config.TraceLimit = 0

	result, err := RunStabilitySimulation(config)
	if err != nil {
		t.Fatal(err)
	}
	if result.SafetyOK {
		t.Fatalf(
			"expected late lower-hash candidate to expose a safety violation, got finalized=%v",
			result.FinalizedCounts,
		)
	}
	if result.FinalizedCounts[StabilityBlockA] == 0 ||
		result.FinalizedCounts[StabilityBlockB] == 0 {
		t.Fatalf("expected both early A and late canonical B to finalize, got %v", result.FinalizedCounts)
	}
}

func TestCanonicalStrengthTieBreakUsesStrengthBeforeCandidateOrder(t *testing.T) {
	state := PreferenceState{
		Preference: StabilityBlockA,
		Strength: map[string]int{
			StabilityBlockA: 2,
			StabilityBlockB: 1,
		},
	}
	next, changed := applyCanonicalStrengthTieBreak(state)
	if changed || next.Preference != StabilityBlockA {
		t.Fatalf("lower-strength B displaced A: %+v", next)
	}

	state.Strength[StabilityBlockB] = 2
	next, changed = applyCanonicalStrengthTieBreak(state)
	if !changed || next.Preference != StabilityBlockB {
		t.Fatalf("equal strength did not choose canonical B: changed=%v state=%+v", changed, next)
	}
	if state.Preference != StabilityBlockA {
		t.Fatalf("canonicalizer mutated input state: %+v", state)
	}
}

func TestStabilitySimulatorVRFNetworkUsesProductionEligibility(t *testing.T) {
	config := DefaultStabilityConfig()
	config.Policy = StabilityPolicySnowball
	config.Scenario = StabilityScenarioVRFNetwork
	config.TraceLimit = 0
	config.MaxVirtualTime = 2 * time.Second

	expected := 0
	foundSeed := false
	for seed := int64(1); seed <= 1000; seed++ {
		count := 0
		for nodeID := 0; nodeID < config.Nodes; nodeID++ {
			eligible, _, err := EvaluateProposalEligibility(ProposalEligibilityInput{
				ExpectedProposers: config.ExpectedProposers,
				ProposerStake:     1,
				TotalStake:        float64(config.Nodes),
				VRFOutput:         syntheticStabilityVRF(seed, nodeID, 0),
			})
			if err != nil {
				t.Fatal(err)
			}
			if eligible {
				count++
			}
		}
		if count >= 3 {
			config.Seed = seed
			expected = count
			foundSeed = true
			break
		}
	}
	if !foundSeed {
		t.Fatal("failed to find deterministic seed with at least three proposers")
	}

	result, err := RunStabilitySimulation(config)
	if err != nil {
		t.Fatal(err)
	}
	if result.ProposerCount != expected || result.CandidateCount != expected {
		t.Fatalf("pure proposer selection produced proposers/candidates=%d/%d, want %d/%d",
			result.ProposerCount, result.CandidateCount, expected, expected)
	}
	firstSeen := 0
	for _, count := range result.InitialPreferenceCount {
		firstSeen += count
	}
	if firstSeen != config.Nodes {
		t.Fatalf("candidate propagation reached %d first preferences, want %d",
			firstSeen, config.Nodes)
	}
}

func TestStabilitySimulatorFiveEqualMinersAreEligibleInWindowZero(t *testing.T) {
	config := DefaultStabilityConfig()
	config.Policy = StabilityPolicySnowball
	config.Scenario = StabilityScenarioVRFNetwork
	config.Nodes = 5
	config.TraceLimit = 0

	simulation := newStabilitySimulation(config)
	if simulation.setupErr != nil {
		t.Fatal(simulation.setupErr)
	}
	if got := len(simulation.proposerNodes); got != config.Nodes {
		t.Fatalf("W0 eligible proposers=%d, want %d", got, config.Nodes)
	}
	if got := len(simulation.candidateProposer); got != config.Nodes {
		t.Fatalf("W0 candidates=%d, want %d", got, config.Nodes)
	}
}

func TestStabilitySimulatorResolvesVRFSeed8483WithoutTieBreak(t *testing.T) {
	config := DefaultStabilityConfig()
	config.Policy = StabilityPolicySnowball
	config.Scenario = StabilityScenarioVRFNetwork
	config.StrengthTieBreak = StabilityStrengthTieBreakNone
	config.Seed = 8483
	config.MaxVirtualTime = 25 * time.Second
	config.TraceLimit = 0

	result, err := RunStabilitySimulation(config)
	if err != nil {
		t.Fatal(err)
	}
	if !result.SafetyOK || !result.LivenessOK {
		t.Fatalf(
			"production sampling did not resolve VRF seed 8483: safety=%v liveness=%v finalized=%v violation=%s",
			result.SafetyOK,
			result.LivenessOK,
			result.FinalizedCounts,
			result.Violation,
		)
	}
	if len(result.FinalizedCounts) != 1 || result.HonestFinalized != config.Nodes {
		t.Fatalf("expected one finalized candidate on all nodes, got %+v", result)
	}
}

func TestStabilitySimulatorDoesNotForkOnLateCandidate(t *testing.T) {
	config := PropagationRaceStabilityConfig()
	config.Policy = StabilityPolicySnowball
	config.StrengthTieBreak = StabilityStrengthTieBreakNone
	config.TraceLimit = 0

	result, err := RunStabilitySimulation(config)
	if err != nil {
		t.Fatal(err)
	}
	if !result.SafetyOK || !result.LivenessOK {
		t.Fatalf(
			"late candidate caused replacement Snowball failure: safety=%v liveness=%v finalized=%v violation=%s",
			result.SafetyOK,
			result.LivenessOK,
			result.FinalizedCounts,
			result.Violation,
		)
	}
	if result.FinalizedCounts[StabilityBlockA] != config.Nodes ||
		result.FinalizedCounts[StabilityBlockB] != 0 {
		t.Fatalf("late B displaced persistent A preference: %v", result.FinalizedCounts)
	}
}

func TestStabilitySimulatorStrengthTieBreakValidation(t *testing.T) {
	config := DefaultStabilityConfig()
	config.StrengthTieBreak = StabilityStrengthTieBreak("unknown")
	if err := config.Validate(); err == nil {
		t.Fatal("expected unknown strength tie-break rule to be rejected")
	}

	config = DefaultStabilityConfig()
	config.Policy = StabilityPolicySticky
	config.StrengthTieBreak = StabilityStrengthTieBreakCanonical
	if err := config.Validate(); err == nil {
		t.Fatal("expected canonical strength tie-break with non-Snowball policy to be rejected")
	}

}

func TestStabilitySimulatorVRFNetworkOpensLaterProposalWindow(t *testing.T) {
	config := DefaultStabilityConfig()
	config.Policy = StabilityPolicySnowball
	config.Scenario = StabilityScenarioVRFNetwork
	config.ExpectedProposers = 1
	config.MaxVirtualTime = 6 * time.Second
	config.TraceLimit = 0

	expectedWindowOne := 0
	for seed := int64(1); seed <= 10000; seed++ {
		if countVirtualProposers(t, config, seed, 0, config.ExpectedProposers) != 0 {
			continue
		}
		count := countVirtualProposers(t, config, seed, 1, config.ExpectedProposers*3)
		if count == 0 {
			continue
		}
		config.Seed = seed
		expectedWindowOne = count
		break
	}
	if expectedWindowOne == 0 {
		t.Fatal("failed to find a seed with no W0 proposer and at least one W1 proposer")
	}

	result, err := RunStabilitySimulation(config)
	if err != nil {
		t.Fatal(err)
	}
	if result.ProposerCount != expectedWindowOne || result.CandidateCount != expectedWindowOne {
		t.Fatalf(
			"W1 fallback produced proposers/candidates=%d/%d, want %d/%d",
			result.ProposerCount,
			result.CandidateCount,
			expectedWindowOne,
			expectedWindowOne,
		)
	}
}

func TestStabilitySimulatorVRFDoesNotExpandKnownHeightInLaterWindows(t *testing.T) {
	config := DefaultStabilityConfig()
	config.Policy = StabilityPolicySnowball
	config.Scenario = StabilityScenarioVRFNetwork
	config.Seed = 8483
	config.Beta = 100000
	config.MaxVirtualTime = 12 * time.Second
	config.TraceLimit = 0

	expectedW0 := countVirtualProposers(
		t,
		config,
		config.Seed,
		0,
		config.ExpectedProposers,
	)
	if expectedW0 == 0 {
		t.Fatal("test seed unexpectedly has no W0 proposer")
	}
	simulation := newStabilitySimulation(config)
	if simulation.setupErr != nil {
		t.Fatal(simulation.setupErr)
	}
	simulation.run()

	if len(simulation.candidates) != expectedW0 ||
		len(simulation.candidateProposer) != len(simulation.proposerNodes) {
		t.Fatalf(
			"later windows expanded a known height: candidates=%d candidate_proposers=%d unique_proposers=%d want_w0=%d",
			len(simulation.candidates),
			len(simulation.candidateProposer),
			len(simulation.proposerNodes),
			expectedW0,
		)
	}
	for candidate := range simulation.candidates {
		if !strings.Contains(candidate, "-w0-") {
			t.Fatalf("known height gained a later-window candidate: %s", candidate)
		}
	}
}

func TestStabilitySimulatorVRFPhaseSkewModelsParentCommitMisalignment(t *testing.T) {
	config := DefaultStabilityConfig()
	config.Policy = StabilityPolicySnowball
	config.Scenario = StabilityScenarioVRFPhaseSkew
	config.Seed = 8483
	config.MaxVirtualTime = 25 * time.Second
	config.TraceLimit = 0

	config.ParentReadySpread = 0
	baseline, err := RunStabilitySimulation(config)
	if err != nil {
		t.Fatal(err)
	}
	ordinaryConfig := config
	ordinaryConfig.Scenario = StabilityScenarioVRFNetwork
	ordinary, err := RunStabilitySimulation(ordinaryConfig)
	if err != nil {
		t.Fatal(err)
	}
	if baseline.AllFinalized != ordinary.AllFinalized ||
		baseline.FirstFinalized != ordinary.FirstFinalized ||
		baseline.PollsStarted != ordinary.PollsStarted ||
		!reflect.DeepEqual(baseline.FinalizedCounts, ordinary.FinalizedCounts) {
		t.Fatalf(
			"zero-spread phase scenario drifted from vrf-network: ordinary=(%s,%s,%d,%v) baseline=(%s,%s,%d,%v)",
			ordinary.FirstFinalized,
			ordinary.AllFinalized,
			ordinary.PollsStarted,
			ordinary.FinalizedCounts,
			baseline.FirstFinalized,
			baseline.AllFinalized,
			baseline.PollsStarted,
			baseline.FinalizedCounts,
		)
	}
	config.ParentReadySpread = 3 * time.Second
	skewed, err := RunStabilitySimulation(config)
	if err != nil {
		t.Fatal(err)
	}

	if !baseline.SafetyOK || !baseline.LivenessOK ||
		!skewed.SafetyOK || !skewed.LivenessOK {
		t.Fatalf(
			"phase-skew comparison did not safely finish: baseline=%+v skewed=%+v",
			baseline.FinalizedCounts,
			skewed.FinalizedCounts,
		)
	}
	if baseline.ParentNotReadyResponses != 0 {
		t.Fatalf("zero-spread baseline had %d parent-not-ready responses", baseline.ParentNotReadyResponses)
	}
	if skewed.ParentNotReadyResponses == 0 || skewed.WeakPolls == 0 {
		t.Fatalf(
			"phase skew did not produce next-height abstentions/weak polls: not_ready=%d weak=%d",
			skewed.ParentNotReadyResponses,
			skewed.WeakPolls,
		)
	}
	if skewed.AllFinalized < baseline.AllFinalized+500*time.Millisecond {
		t.Fatalf(
			"3s parent-ready skew did not materially delay consensus: baseline=%s skewed=%s",
			baseline.AllFinalized,
			skewed.AllFinalized,
		)
	}
	if skewed.MaxNetworkActivePolls > config.Pipeline ||
		skewed.MaxOutstandingPolls > config.PollCapacity {
		t.Fatalf(
			"poll bounds exceeded: network=%d/%d outstanding=%d/%d",
			skewed.MaxNetworkActivePolls,
			config.Pipeline,
			skewed.MaxOutstandingPolls,
			config.PollCapacity,
		)
	}
}

func TestStabilitySimulatorSingleNodeNotReadyForSixSeconds(t *testing.T) {
	config := DefaultStabilityConfig()
	config.Policy = StabilityPolicySnowball
	config.Scenario = StabilityScenarioSingleNodeNotReady
	config.ParentReadyDelay = 6 * time.Second
	config.MaxVirtualTime = 12 * time.Second
	config.TraceLimit = 10000

	simulation := newStabilitySimulation(config)
	delayedNodeID := config.Nodes - config.Byzantine - 1
	delayedNodes := 0
	for _, node := range simulation.nodes {
		switch node.readyAt {
		case 0:
		case config.ParentReadyDelay:
			delayedNodes++
			if node.id != delayedNodeID {
				t.Fatalf("delayed node=%d, want %d", node.id, delayedNodeID)
			}
		default:
			t.Fatalf("node %d has unexpected parent-ready time %s", node.id, node.readyAt)
		}
	}
	if delayedNodes != 1 {
		t.Fatalf("delayed nodes=%d, want exactly 1", delayedNodes)
	}

	simulation.run()
	result := simulation.result()
	if !result.SafetyOK || !result.LivenessOK {
		t.Fatalf("single delayed node did not safely finish: %+v", result.FinalizedCounts)
	}
	if result.CandidateCount != 1 {
		t.Fatalf("candidate count=%d, want minimal single candidate", result.CandidateCount)
	}
	if result.ParentNotReadyResponses == 0 {
		t.Fatal("delayed node never abstained from a next-height poll")
	}
	if result.FirstFinalized >= config.ParentReadyDelay {
		t.Fatalf("ready nodes did not finalize before delayed parent: first=%s delay=%s", result.FirstFinalized, config.ParentReadyDelay)
	}
	if result.AllFinalized < config.ParentReadyDelay {
		t.Fatalf("delayed node finalized before becoming ready: all=%s delay=%s", result.AllFinalized, config.ParentReadyDelay)
	}

	deferredAtZero := false
	readyAtDelay := false
	finalizedAfterReady := false
	for _, event := range result.Trace {
		if event.Node != delayedNodeID {
			continue
		}
		switch event.Kind {
		case "candidate-deferred":
			deferredAtZero = deferredAtZero || event.At == 0
		case "parent-ready":
			readyAtDelay = readyAtDelay || event.At == config.ParentReadyDelay
		case "finalized":
			finalizedAfterReady = finalizedAfterReady || event.At >= config.ParentReadyDelay
		}
	}
	if !deferredAtZero || !readyAtDelay || !finalizedAfterReady {
		t.Fatalf(
			"missing delayed-node lifecycle: deferred_at_zero=%t ready_at_delay=%t finalized_after_ready=%t",
			deferredAtZero,
			readyAtDelay,
			finalizedAfterReady,
		)
	}
}

func TestStabilitySimulatorSingleNodeNotReadyDelaysAnHonestNode(t *testing.T) {
	config := DefaultStabilityConfig()
	config.Scenario = StabilityScenarioSingleNodeNotReady
	config.Byzantine = 2

	simulation := newStabilitySimulation(config)
	delayedNodeID := config.Nodes - config.Byzantine - 1
	if simulation.nodes[delayedNodeID].readyAt != config.ParentReadyDelay {
		t.Fatalf("honest node %d readyAt=%s, want %s", delayedNodeID, simulation.nodes[delayedNodeID].readyAt, config.ParentReadyDelay)
	}
	for nodeID := config.Nodes - config.Byzantine; nodeID < config.Nodes; nodeID++ {
		if simulation.nodes[nodeID].readyAt != 0 {
			t.Fatalf("byzantine node %d was selected as delayed node", nodeID)
		}
	}
}

func TestStabilitySimulatorPollCapacitySeparatesNetworkAndOrderedPending(t *testing.T) {
	config := DefaultStabilityConfig()
	config.Policy = StabilityPolicySnowball
	config.TraceLimit = 0
	simulation := newStabilitySimulation(config)
	simulation.clock.events = virtualEventHeap{}
	simulation.clock.nextOrder = 0
	node := simulation.nodes[0]
	simulation.deliverCandidate(node.id, StabilityBlockA)

	node.networkActive = config.Pipeline
	for seq := uint64(2); seq <= 5; seq++ {
		node.pending[seq] = stabilityPollOutcome{}
	}
	started := simulation.pollsStarted
	simulation.pollTick(node.id)
	if simulation.pollsStarted != started || simulation.pollCapacityBlocks != 1 {
		t.Fatalf(
			"full outstanding queue started another poll: started=%d->%d capacity_blocks=%d",
			started,
			simulation.pollsStarted,
			simulation.pollCapacityBlocks,
		)
	}

	node.networkActive--
	simulation.pollTick(node.id)
	if simulation.pollsStarted != started+1 {
		t.Fatalf("available network/outstanding slot did not start a poll")
	}
	if node.networkActive != config.Pipeline ||
		node.networkActive+len(node.pending) != config.PollCapacity {
		t.Fatalf(
			"poll depth=%d network=%d, want capacity=%d network=%d",
			node.networkActive+len(node.pending),
			node.networkActive,
			config.PollCapacity,
			config.Pipeline,
		)
	}
}

func TestStabilitySimulatorVRFProposersInstallOwnCandidateBeforeZeroLatencyDelivery(t *testing.T) {
	config := DefaultStabilityConfig()
	config.Policy = StabilityPolicySnowball
	config.Scenario = StabilityScenarioVRFNetwork
	config.MinNetworkLatency = 0
	config.MaxNetworkLatency = 0
	config.GossipRelayMin = 0
	config.GossipRelayMax = 0
	config.MaxVirtualTime = 100 * time.Millisecond

	for seed := int64(1); seed <= 1000; seed++ {
		if countVirtualProposers(t, config, seed, 0, config.ExpectedProposers) >= 3 {
			config.Seed = seed
			break
		}
	}
	simulation := newStabilitySimulation(config)
	if simulation.setupErr != nil {
		t.Fatal(simulation.setupErr)
	}
	if len(simulation.candidateProposer) < 3 {
		t.Fatalf("test seed produced only %d W0 candidates", len(simulation.candidateProposer))
	}
	for candidate, proposer := range simulation.candidateProposer {
		if got := simulation.nodes[proposer].firstCandidate; got != candidate {
			t.Fatalf(
				"proposer %d first saw %q, want its own candidate %q",
				proposer,
				got,
				candidate,
			)
		}
	}
}

func TestStabilitySimulatorPullQueryFetchesUnknownCandidateBeforeChit(t *testing.T) {
	config := DefaultStabilityConfig()
	config.Policy = StabilityPolicySnowball
	config.Scenario = StabilityScenarioVRFNetwork
	config.MinNetworkLatency = time.Millisecond
	config.MaxNetworkLatency = time.Millisecond
	config.QueryTimeout = 20 * time.Millisecond
	config.MaxVirtualTime = 100 * time.Millisecond
	config.GossipRelayMin = time.Second
	config.GossipRelayMax = time.Second
	config.TraceLimit = 1000

	for seed := int64(1); seed <= 1000; seed++ {
		count := countVirtualProposers(t, config, seed, 0, config.ExpectedProposers)
		if count > 0 && count < config.Nodes-1 {
			config.Seed = seed
			break
		}
	}
	simulation := newStabilitySimulation(config)
	if simulation.setupErr != nil {
		t.Fatal(simulation.setupErr)
	}
	if len(simulation.candidateProposer) == 0 {
		t.Fatal("test seed produced no W0 candidate")
	}

	var candidate string
	var proposer int
	for blockID, nodeID := range simulation.candidateProposer {
		candidate = blockID
		proposer = nodeID
		break
	}
	requester := -1
	for nodeID := range simulation.nodes {
		if nodeID != proposer {
			if _, isProposer := simulation.proposerNodes[nodeID]; !isProposer {
				requester = nodeID
				break
			}
		}
	}
	if requester < 0 {
		t.Fatal("test seed left no non-proposer requester")
	}

	                                                                         
	                 
	simulation.clock.events = virtualEventHeap{}
	simulation.clock.nextOrder = 0
	simulation.trace = nil
	simulation.deliverCandidate(requester, candidate)
	knownBefore := nodesKnowingCandidate(simulation, candidate)
	simulation.startPoll(simulation.nodes[requester])
	simulation.run()

	if got := nodesKnowingCandidate(simulation, candidate); got <= knownBefore {
		t.Fatalf("PullQuery did not spread candidate: before=%d after=%d", knownBefore, got)
	}
	sawGet := false
	sawPut := false
	for _, event := range simulation.trace {
		sawGet = sawGet || event.Kind == "get"
		sawPut = sawPut || event.Kind == "put"
	}
	if !sawGet || !sawPut {
		t.Fatalf("missing PullQuery recovery trace: get=%v put=%v", sawGet, sawPut)
	}
}

func TestStabilitySimulatorFinalizedNodeDoesNotProposeOldHeightInLaterWindow(t *testing.T) {
	config := DefaultStabilityConfig()
	config.Policy = StabilityPolicySnowball
	config.Scenario = StabilityScenarioVRFNetwork
	targetNode := 0
	foundSeed := false

	for seed := int64(1); seed <= 1000; seed++ {
		eligible, _, err := EvaluateProposalEligibility(ProposalEligibilityInput{
			ExpectedProposers: config.ExpectedProposers * 3,
			ProposerStake:     1,
			TotalStake:        float64(config.Nodes),
			VRFOutput:         syntheticStabilityVRF(seed, targetNode, 1),
		})
		if err != nil {
			t.Fatal(err)
		}
		if eligible &&
			countVirtualProposers(t, config, seed, 1, config.ExpectedProposers*3) >= 2 {
			config.Seed = seed
			foundSeed = true
			break
		}
	}
	if !foundSeed {
		t.Fatal("failed to find W1 seed eligible for finalized target and another node")
	}

	simulation := newStabilitySimulation(config)
	if simulation.setupErr != nil {
		t.Fatal(simulation.setupErr)
	}
	simulation.candidates = make(map[string]struct{})
	simulation.candidateProposer = make(map[string]int)
	simulation.proposerNodes = make(map[int]struct{})
	simulation.gossipRelayed = make(map[string]map[int]bool)
	for _, node := range simulation.nodes {
		node.known = make(map[string]bool)
		node.firstCandidate = ""
		node.decision = NewPreferenceState()
	}
	simulation.nodes[targetNode].decision.Finalized = true

	targetVRF := syntheticStabilityVRF(config.Seed, targetNode, 1)
	targetCandidate := syntheticStabilityCandidate(config.Seed, targetNode, 1, targetVRF)
	simulation.proposeVRFWindow(1, config.ExpectedProposers*3)
	if simulation.setupErr != nil {
		t.Fatal(simulation.setupErr)
	}
	if _, exists := simulation.candidates[targetCandidate]; exists {
		t.Fatalf("finalized node %d proposed old-height candidate %s", targetNode, targetCandidate)
	}
	if len(simulation.candidates) == 0 {
		t.Fatal("expected at least one non-finalized node to propose in W1")
	}
}

func countVirtualProposers(
	t *testing.T,
	config StabilityConfig,
	seed int64,
	window int,
	expectedProposers float64,
) int {
	t.Helper()
	count := 0
	for nodeID := 0; nodeID < config.Nodes; nodeID++ {
		eligible, _, err := EvaluateProposalEligibility(ProposalEligibilityInput{
			ExpectedProposers: expectedProposers,
			ProposerStake:     1,
			TotalStake:        float64(config.Nodes),
			VRFOutput:         syntheticStabilityVRF(seed, nodeID, window),
		})
		if err != nil {
			t.Fatal(err)
		}
		if eligible {
			count++
		}
	}
	return count
}

func nodesKnowingCandidate(simulation *stabilitySimulation, candidate string) int {
	count := 0
	for _, node := range simulation.nodes {
		if node.known[candidate] {
			count++
		}
	}
	return count
}
