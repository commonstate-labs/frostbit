package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"dex/consensus"
)

func main() {
	os.Exit(run())
}

func run() int {
	config := consensus.DefaultStabilityConfig()
	defaultMaxVirtualTime := config.MaxVirtualTime
	policy := string(config.Policy)
	scenario := string(config.Scenario)
	strengthTieBreak := string(config.StrengthTieBreak)
	expect := "any"
	runs := 1
	printTrace := false
	jsonOutput := false
	verboseRuns := false

	flag.StringVar(&policy, "policy", policy, "preference policy: current, sticky, or snowball")
	flag.StringVar(&scenario, "scenario", scenario, "simulation scenario: propagation-race, fragmented, vrf-network, vrf-phase-skew, or single-node-not-ready")
	flag.StringVar(&strengthTieBreak, "strength-tiebreak", strengthTieBreak, "snowball equal-strength rule: none or canonical")
	flag.Int64Var(&config.Seed, "seed", config.Seed, "deterministic random seed")
	flag.IntVar(&runs, "runs", runs, "number of consecutive seeds to run")
	flag.IntVar(&config.Nodes, "nodes", config.Nodes, "total node count")
	flag.IntVar(&config.Byzantine, "byzantine", config.Byzantine, "deterministically equivocating node count")
	flag.IntVar(&config.K, "k", config.K, "sample size")
	flag.IntVar(&config.EarlyNodes, "early-nodes", config.EarlyNodes, "nodes polling before candidate B propagates")
	flag.IntVar(&config.AlphaPreference, "alpha-preference", config.AlphaPreference, "soft cumulative-preference threshold")
	flag.IntVar(&config.AlphaConfidence, "alpha-confidence", config.AlphaConfidence, "strong poll threshold")
	flag.IntVar(&config.Beta, "beta", config.Beta, "consecutive strong polls needed to finalize")
	flag.IntVar(&config.Pipeline, "pipeline", config.Pipeline, "maximum network-active polls per node")
	flag.IntVar(&config.PollCapacity, "poll-capacity", config.PollCapacity, "maximum network-active plus ordered-pending polls per node")
	flag.DurationVar(&config.PollInterval, "poll-interval", config.PollInterval, "virtual interval between poll starts")
	flag.DurationVar(&config.QueryTimeout, "query-timeout", config.QueryTimeout, "virtual poll timeout")
	flag.DurationVar(&config.MinNetworkLatency, "latency-min", config.MinNetworkLatency, "minimum virtual one-way latency")
	flag.DurationVar(&config.MaxNetworkLatency, "latency-max", config.MaxNetworkLatency, "maximum virtual one-way latency")
	flag.DurationVar(&config.ParentReadySpread, "parent-ready-spread", config.ParentReadySpread, "cross-height spread between first and last parent VM commit")
	flag.DurationVar(&config.ParentReadyDelay, "parent-ready-delay", config.ParentReadyDelay, "parent commit delay for the single-node-not-ready scenario")
	flag.DurationVar(&config.CandidateBDelay, "b-delay", config.CandidateBDelay, "virtual time when candidate B starts propagating")
	flag.DurationVar(&config.CandidateBSpread, "b-spread", config.CandidateBSpread, "additional random B propagation spread")
	flag.DurationVar(&config.LatePollStart, "late-poll-start", config.LatePollStart, "virtual time when non-early nodes start polling")
	flag.DurationVar(&config.MaxVirtualTime, "max-time", config.MaxVirtualTime, "maximum virtual experiment time")
	flag.Float64Var(&config.ExpectedProposers, "expected-proposers", config.ExpectedProposers, "window-0 expected proposer count in the vrf-network scenario")
	flag.IntVar(&config.GossipFanout, "gossip-fanout", config.GossipFanout, "candidate gossip fanout in the vrf-network scenario")
	flag.DurationVar(&config.GossipRelayMin, "gossip-relay-min", config.GossipRelayMin, "minimum virtual gossip relay delay")
	flag.DurationVar(&config.GossipRelayMax, "gossip-relay-max", config.GossipRelayMax, "maximum virtual gossip relay delay")
	flag.IntVar(&config.FailureSwitchAfter, "failure-switch-after", config.FailureSwitchAfter, "failed polls before the liveness escape may switch preference")
	flag.IntVar(&config.TraceLimit, "trace-limit", config.TraceLimit, "maximum trace events retained; 0 disables collection")
	flag.StringVar(&expect, "expect", expect, "exit assertion: any, safe, fork, or stall")
	flag.BoolVar(&printTrace, "trace", printTrace, "print retained protocol trace")
	flag.BoolVar(&jsonOutput, "json", jsonOutput, "emit results as JSON")
	flag.BoolVar(&verboseRuns, "verbose-runs", verboseRuns, "print every run instead of a batch summary")
	flag.Parse()
	maxTimeExplicit := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "max-time" {
			maxTimeExplicit = true
		}
	})

	config.Policy = consensus.StabilityPolicy(policy)
	config.Scenario = consensus.StabilityScenario(scenario)
	config.StrengthTieBreak = consensus.StabilityStrengthTieBreak(strengthTieBreak)
	if config.Scenario == consensus.StabilityScenarioSingleNodeNotReady &&
		!maxTimeExplicit && config.MaxVirtualTime <= config.ParentReadyDelay {
		config.MaxVirtualTime = config.ParentReadyDelay + defaultMaxVirtualTime
	}
	if runs < 1 {
		fmt.Fprintln(os.Stderr, "runs must be positive")
		return 2
	}
	switch expect {
	case "any", "safe", "fork", "stall":
	default:
		fmt.Fprintln(os.Stderr, "expect must be any, safe, fork, or stall")
		return 2
	}

	results := make([]consensus.StabilityResult, 0, runs)
	assertionFailed := false
	firstSeed := config.Seed
	for i := 0; i < runs; i++ {
		config.Seed = firstSeed + int64(i)
		result, err := consensus.RunStabilitySimulation(config)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid simulation configuration: %v\n", err)
			return 2
		}
		results = append(results, result)
		if !matchesExpectation(result, expect) {
			assertionFailed = true
		}
	}

	if jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(results); err != nil {
			fmt.Fprintf(os.Stderr, "encode result: %v\n", err)
			return 2
		}
	} else if len(results) == 1 || printTrace || verboseRuns {
		for index, result := range results {
			printResult(index+1, result, printTrace)
		}
	} else {
		printBatchResult(results, expect)
	}
	if assertionFailed {
		fmt.Fprintf(os.Stderr, "expectation %q was not met\n", expect)
		return 1
	}
	return 0
}

func printBatchResult(results []consensus.StabilityResult, expect string) {
	safetyFailures := 0
	livenessFailures := 0
	stalledRuns := 0
	mismatchSeeds := make([]string, 0)
	var firstFinalized durationAverage
	var nodeFinalized durationAverage
	var allFinalized durationAverage
	var oneWayLatency durationAverage
	var roundTrip durationAverage
	proposerTotal := 0
	candidateTotal := 0
	pollSlotsTotal := 0
	pollPeerRequestsTotal := 0
	parentNotReadyTotal := 0
	pollCapacityBlocksTotal := 0
	maxNetworkActive := 0
	maxOrderedPending := 0
	maxOutstanding := 0
	minProposers, maxProposers := results[0].ProposerCount, results[0].ProposerCount
	minCandidates, maxCandidates := results[0].CandidateCount, results[0].CandidateCount
	for _, result := range results {
		if !result.SafetyOK {
			safetyFailures++
		}
		if !result.LivenessOK {
			livenessFailures++
		}
		if result.SafetyOK && !result.LivenessOK {
			stalledRuns++
		}
		if !matchesExpectation(result, expect) && len(mismatchSeeds) < 20 {
			mismatchSeeds = append(mismatchSeeds, fmt.Sprint(result.Seed))
		}
		proposerTotal += result.ProposerCount
		candidateTotal += result.CandidateCount
		pollSlotsTotal += result.PollSlotsSampled
		pollPeerRequestsTotal += result.PollPeerRequests
		parentNotReadyTotal += result.ParentNotReadyResponses
		pollCapacityBlocksTotal += result.PollCapacityBlocks
		if result.MaxNetworkActivePolls > maxNetworkActive {
			maxNetworkActive = result.MaxNetworkActivePolls
		}
		if result.MaxOrderedPendingPolls > maxOrderedPending {
			maxOrderedPending = result.MaxOrderedPendingPolls
		}
		if result.MaxOutstandingPolls > maxOutstanding {
			maxOutstanding = result.MaxOutstandingPolls
		}
		if result.ProposerCount < minProposers {
			minProposers = result.ProposerCount
		}
		if result.ProposerCount > maxProposers {
			maxProposers = result.ProposerCount
		}
		if result.CandidateCount < minCandidates {
			minCandidates = result.CandidateCount
		}
		if result.CandidateCount > maxCandidates {
			maxCandidates = result.CandidateCount
		}
		if result.HonestFinalized > 0 {
			firstFinalized.add(result.FirstFinalized, 1)
			nodeFinalized.add(result.AverageFinalized, result.HonestFinalized)
		}
		if result.HonestFinalized == result.HonestNodes {
			allFinalized.add(result.AllFinalized, 1)
		}
		oneWayLatency.add(result.AverageOneWayLatency, result.OneWayLatencySamples)
		roundTrip.add(result.AverageRoundTrip, result.RoundTripSamples)
	}
	fmt.Printf(
		"runs=%d scenario=%s policy=%s strength_tiebreak=%s seeds=%d..%d safety_failures=%d liveness_failures=%d stalled_runs=%d expectation=%s\n",
		len(results),
		results[0].Scenario,
		results[0].Policy,
		results[0].StrengthTieBreak,
		results[0].Seed,
		results[len(results)-1].Seed,
		safetyFailures,
		livenessFailures,
		stalledRuns,
		expect,
	)
	fmt.Printf(
		"proposals avg_proposers=%.2f proposer_range=%d..%d avg_candidates=%.2f candidate_range=%d..%d\n",
		float64(proposerTotal)/float64(len(results)),
		minProposers,
		maxProposers,
		float64(candidateTotal)/float64(len(results)),
		minCandidates,
		maxCandidates,
	)
	fmt.Printf(
		"timing avg_first_finalized=%s avg_node_finalized=%s avg_height_finalized=%s completed_runs=%d/%d\n",
		firstFinalized.format(),
		nodeFinalized.format(),
		allFinalized.format(),
		allFinalized.samples,
		len(results),
	)
	fmt.Printf(
		"network avg_one_way_latency=%s avg_round_trip=%s sampled_round_trips=%d\n",
		oneWayLatency.format(),
		roundTrip.format(),
		roundTrip.samples,
	)
	fmt.Printf(
		"poll_sampling logical_slots=%d unique_peer_requests=%d request_ratio=%.4f\n",
		pollSlotsTotal,
		pollPeerRequestsTotal,
		safeRatio(pollPeerRequestsTotal, pollSlotsTotal),
	)
	if results[0].Scenario == consensus.StabilityScenarioVRFPhaseSkew ||
		results[0].Scenario == consensus.StabilityScenarioSingleNodeNotReady {
		metricName := "phase_skew"
		if results[0].Scenario == consensus.StabilityScenarioSingleNodeNotReady {
			metricName = "readiness"
		}
		fmt.Printf(
			"%s parent_not_ready_responses=%d max_network_active=%d max_ordered_pending=%d max_outstanding=%d poll_capacity_blocks=%d\n",
			metricName,
			parentNotReadyTotal,
			maxNetworkActive,
			maxOrderedPending,
			maxOutstanding,
			pollCapacityBlocksTotal,
		)
	}
	if len(mismatchSeeds) > 0 {
		fmt.Printf("first_mismatching_seeds=[%s]\n", strings.Join(mismatchSeeds, ", "))
	}
}

func matchesExpectation(result consensus.StabilityResult, expect string) bool {
	switch expect {
	case "safe":
		return result.SafetyOK && result.LivenessOK
	case "fork":
		return !result.SafetyOK
	case "stall":
		return result.SafetyOK && !result.LivenessOK
	default:
		return true
	}
}

func printResult(runNumber int, result consensus.StabilityResult, printTrace bool) {
	safety := "PASS"
	if !result.SafetyOK {
		safety = "FAIL"
	}
	liveness := "PASS"
	if !result.LivenessOK {
		liveness = "FAIL"
	}
	fmt.Printf(
		"run=%d seed=%d scenario=%s policy=%s strength_tiebreak=%s virtual=%s events=%d polls=%d\n",
		runNumber,
		result.Seed,
		result.Scenario,
		result.Policy,
		result.StrengthTieBreak,
		result.VirtualElapsed,
		result.EventsProcessed,
		result.PollsStarted,
	)
	fmt.Printf(
		"safety=%s liveness=%s finalized=[%s] preferences=[%s] strong_polls=%d weak_polls=%d\n",
		safety,
		liveness,
		strings.Join(consensus.SortedStabilityCounts(result.FinalizedCounts), ", "),
		strings.Join(consensus.SortedStabilityCounts(result.PreferenceCount), ", "),
		result.StrongPolls,
		result.WeakPolls,
	)
	fmt.Printf(
		"proposals proposers=%d candidates=%d initial_preferences=[%s]\n",
		result.ProposerCount,
		result.CandidateCount,
		strings.Join(consensus.SortedStabilityCounts(result.InitialPreferenceCount), ", "),
	)
	fmt.Printf(
		"timing first_finalized=%s avg_node_finalized=%s all_finalized=%s\n",
		formatDuration(result.FirstFinalized, result.HonestFinalized > 0),
		formatDuration(result.AverageFinalized, result.HonestFinalized > 0),
		formatDuration(result.AllFinalized, result.HonestFinalized == result.HonestNodes),
	)
	fmt.Printf(
		"network avg_one_way_latency=%s avg_round_trip=%s sampled_round_trips=%d\n",
		formatDuration(result.AverageOneWayLatency, result.OneWayLatencySamples > 0),
		formatDuration(result.AverageRoundTrip, result.RoundTripSamples > 0),
		result.RoundTripSamples,
	)
	fmt.Printf(
		"poll_sampling logical_slots=%d unique_peer_requests=%d request_ratio=%.4f\n",
		result.PollSlotsSampled,
		result.PollPeerRequests,
		safeRatio(result.PollPeerRequests, result.PollSlotsSampled),
	)
	if result.Scenario == consensus.StabilityScenarioVRFPhaseSkew ||
		result.Scenario == consensus.StabilityScenarioSingleNodeNotReady {
		metricName := "phase_skew"
		if result.Scenario == consensus.StabilityScenarioSingleNodeNotReady {
			metricName = "readiness"
		}
		fmt.Printf(
			"%s parent_not_ready_responses=%d max_network_active=%d max_ordered_pending=%d max_outstanding=%d poll_capacity_blocks=%d\n",
			metricName,
			result.ParentNotReadyResponses,
			result.MaxNetworkActivePolls,
			result.MaxOrderedPendingPolls,
			result.MaxOutstandingPolls,
			result.PollCapacityBlocks,
		)
	}
	if result.Violation != "" {
		fmt.Printf("violation: %s\n", result.Violation)
	}
	if !printTrace {
		return
	}
	for _, event := range result.Trace {
		node := "global"
		if event.Node >= 0 {
			node = fmt.Sprintf("node-%02d", event.Node)
		}
		fmt.Printf("%9s %-7s %-16s %s\n", event.At, node, event.Kind, event.Detail)
	}
}

type durationAverage struct {
	total   time.Duration
	samples int
}

func (a *durationAverage) add(average time.Duration, samples int) {
	if samples <= 0 {
		return
	}
	a.total += average * time.Duration(samples)
	a.samples += samples
}

func (a durationAverage) format() string {
	if a.samples == 0 {
		return "n/a"
	}
	return (a.total / time.Duration(a.samples)).String()
}

func formatDuration(value time.Duration, available bool) string {
	if !available {
		return "n/a"
	}
	return value.String()
}

func safeRatio(numerator, denominator int) float64 {
	if denominator <= 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}
