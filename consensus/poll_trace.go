package consensus

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"dex/logs"
	"dex/types"
)

                                                                            
                                                                         
                                                                             
                                                                           
type pollTrace struct {
	replies                    map[types.NodeID]time.Time
	firstReplyAt, lastReplyAt  time.Time
	networkDoneAt              time.Time
	settleStartedAt, settledAt time.Time
	confidenceBefore           int
	confidenceAfter            int
	preferenceBefore           string
	preferenceAfter            string
	confidencePreferenceBefore string
	confidencePreferenceAfter  string
	maxValidVoteWeight         int
	hard                       bool
}

func newPollTrace() *pollTrace {
	return &pollTrace{replies: make(map[types.NodeID]time.Time), confidenceBefore: -1, confidenceAfter: -1}
}

func (p *pollTrace) recordReply(peer types.NodeID, at time.Time, signed bool) {
	p.replies[peer] = at
	                                                                        
	                                                                          
	if !signed {
		return
	}
	if p.firstReplyAt.IsZero() || at.Before(p.firstReplyAt) {
		p.firstReplyAt = at
	}
	if p.lastReplyAt.IsZero() || at.After(p.lastReplyAt) {
		p.lastReplyAt = at
	}
}

                                                                       
func pollTraceMillis(start, end time.Time) float64 {
	if start.IsZero() || end.IsZero() {
		return -1
	}
	return float64(end.Sub(start)) / float64(time.Millisecond)
}

func pollTraceSelected(ctx *QueryContext, reason string, hard bool, end time.Time) bool {
	return reason != "success" || !hard || ctx.seqID%16 == 0 || end.Sub(ctx.startTime) >= 500*time.Millisecond
}

                                                                             
                                                                              
func pollTracePeers(ctx *QueryContext) (sample, received, missing, votes string) {
	peers := make([]string, 0, len(ctx.poll.sampleWeights))
	for peer := range ctx.poll.sampleWeights {
		peers = append(peers, string(peer))
	}
	sort.Strings(peers)
	samples, replies, absent := []string{}, []string{}, []string{}
	for _, peer := range peers {
		id := types.NodeID(peer)
		weight := ctx.poll.sampleWeights[id]
		samples = append(samples, fmt.Sprintf("%s:%d", peer, weight))
		if preferred, ok := ctx.voters[id]; ok {
			replies = append(replies, fmt.Sprintf("%s:%d@%.3f=%s/signed:%t", peer, weight, pollTraceMillis(ctx.startTime, ctx.trace.replies[id]), preferred, len(ctx.signatures[id]) > 0))
		} else {
			absent = append(absent, fmt.Sprintf("%s:%d", peer, weight))
		}
	}
	ids := make([]string, 0, len(ctx.poll.votes))
	for id := range ctx.poll.votes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	counts := make([]string, 0, len(ids))
	for _, id := range ids {
		counts = append(counts, fmt.Sprintf("%s:%d", id, ctx.poll.votes[id]))
	}
	return strings.Join(samples, ","), strings.Join(replies, ","), strings.Join(absent, ","), strings.Join(counts, ",")
}

func emitPollNetworkTrace(ctx *QueryContext) {
	if ctx == nil || ctx.trace == nil {
		return
	}
	hard := false
	for id, weight := range ctx.poll.votes {
		if _, known := ctx.poll.startCandidates[id]; known && weight >= ctx.params.AlphaConfidence {
			hard = true
		}
	}
	reason := ctx.terminalReason
	if reason == "" {
		reason = "success"
	}
	if !pollTraceSelected(ctx, reason, hard, ctx.trace.networkDoneAt) {
		return
	}
	sample, received, missing, votes := pollTracePeers(ctx)
	logs.ConsensusTracef("event=poll_network query=%s height=%d seq=%d reason=%s network_ms=%.3f first_reply_ms=%.3f last_reply_ms=%.3f hard=%t responded_weight=%d k=%d alpha=%d sample=%q received=%q missing=%q votes=%q",
		ctx.queryKey, ctx.height, ctx.seqID, reason, pollTraceMillis(ctx.startTime, ctx.trace.networkDoneAt),
		pollTraceMillis(ctx.startTime, ctx.trace.firstReplyAt), pollTraceMillis(ctx.startTime, ctx.trace.lastReplyAt),
		hard, ctx.poll.respondedSlots, ctx.params.K, ctx.params.AlphaConfidence, sample, received, missing, votes)
}

func emitPollSettledTrace(ctx *QueryContext, reason string) {
	if ctx == nil || ctx.trace == nil || !pollTraceSelected(ctx, reason, ctx.trace.hard, ctx.trace.settledAt) {
		return
	}
	p := ctx.trace
	sample, received, missing, votes := pollTracePeers(ctx)
	logs.ConsensusTracef("event=poll_settled query=%s height=%d seq=%d reason=%s network_ms=%.3f order_wait_ms=%.3f apply_ms=%.3f total_ms=%.3f hard=%t max_valid_weight=%d alpha=%d confidence=%d->%d preference=%s->%s confidence_preference=%s->%s sample=%q received=%q missing=%q votes=%q",
		ctx.queryKey, ctx.height, ctx.seqID, reason, pollTraceMillis(ctx.startTime, p.networkDoneAt),
		pollTraceMillis(p.networkDoneAt, p.settleStartedAt), pollTraceMillis(p.settleStartedAt, p.settledAt), pollTraceMillis(ctx.startTime, p.settledAt),
		p.hard, p.maxValidVoteWeight, ctx.params.AlphaConfidence, p.confidenceBefore, p.confidenceAfter,
		p.preferenceBefore, p.preferenceAfter, p.confidencePreferenceBefore, p.confidencePreferenceAfter, sample, received, missing, votes)
}
