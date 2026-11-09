package consensus

import (
	"dex/logs"
	"dex/types"
	"time"
)

func (qm *QueryManager) traceReceivedChit(msg types.Message, poll *Poll, result string, started time.Time) {
	age := int64(-1)
	if poll != nil {
		age = started.Sub(poll.startTime).Milliseconds()
	}
	handled := time.Since(started)
	if result == "verified" && age < 500 && handled < 200*time.Millisecond && msg.RequestID%16 != 0 {
		return
	}
	logs.ConsensusTracef("event=chit_rx node=%s peer=%s req=%d height=%d accepted=%d result=%s abstain=%t poll_age_ms=%d handle_ms=%d",
		qm.nodeID, msg.From, msg.RequestID, msg.PreferredIDHeight, msg.AcceptedHeight, result, msg.PreferredID == "", age, handled.Milliseconds())
}

func traceBuiltChit(node, peer types.NodeID, request uint32, height uint64, preferred, result string, started time.Time) {
	elapsed := time.Since(started)
	if result == "submitted" && preferred != "" && elapsed < 200*time.Millisecond && request%16 != 0 {
		return
	}
	logs.ConsensusTracef("event=chit_build node=%s peer=%s req=%d height=%d result=%s abstain=%t build_ms=%d",
		node, peer, request, height, result, preferred == "", elapsed.Milliseconds())
}
