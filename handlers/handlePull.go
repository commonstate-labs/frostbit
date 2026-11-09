package handlers

import (
	"dex/consensus"
	"dex/logs"
	"dex/pb"
	"dex/types"
	"io"
	"net/http"
)

                                           
func (hm *HandlerManager) HandlePullQuery(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("HandlePullQuery")
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	var pullQuery pb.PullQuery
	if err := pb.UnmarshalSanitize(bodyBytes, &pullQuery); err != nil {
		http.Error(w, "Invalid PullQuery proto", http.StatusBadRequest)
		return
	}
	                                                                            
	                                                               
	digest, err := hm.validatePullQuery(&pullQuery)
	if err != nil {
		hm.Logger.Warn("[PullQuery] rejected envelope: %v", err)
		http.Error(w, "Invalid PullQuery envelope", http.StatusUnauthorized)
		return
	}
	if hm.consensusManager == nil {
		http.Error(w, "Consensus unavailable", http.StatusServiceUnavailable)
		return
	}
	_, localAccepted := hm.consensusManager.GetLastAccepted()
	reservation := hm.queryReplay.reserveWithToken(pullQuery.Address, pullQuery.RequestedHeight, localAccepted, digest)
	if reservation == nil {
		logs.ConsensusTracef("event=query_rx peer=%s req=%d height=%d result=replay_or_window", pullQuery.Address, pullQuery.RequestId, pullQuery.RequestedHeight)
		w.WriteHeader(http.StatusOK)
		return
	}

	                                                                                                    
	msg := types.Message{
		RequestID:  pullQuery.RequestId,
		Type:       types.MsgPullQuery,
		From:       types.NodeID(pullQuery.Address),
		BlockID:    pullQuery.BlockId,
		Height:     pullQuery.RequestedHeight,
		VRFSeed:    pullQuery.VrfSeed,
		SeqID:      pullQuery.SeqId,
		QueryNonce: append([]byte(nil), pullQuery.Nonce...),
	}
	if pullQuery.RequestId%16 == 0 {
		logs.ConsensusTracef("event=query_rx peer=%s req=%d height=%d result=envelope_valid", pullQuery.Address, pullQuery.RequestId, pullQuery.RequestedHeight)
	}

	if rt, ok := hm.consensusManager.Transport.(*consensus.RealTransport); ok {
		if err := rt.EnqueueReceivedMessage(msg); err != nil {
			logs.ConsensusTracef("event=query_rx peer=%s req=%d height=%d result=queue_rejected", pullQuery.Address, pullQuery.RequestId, pullQuery.RequestedHeight)
			hm.queryReplay.release(reservation)
			hm.Logger.Warn("[Handler] Failed to enqueue PullQuery: %v", err)
			w.Header().Set("Retry-After", "1")
			http.Error(w, "Service temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
}
