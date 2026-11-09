package handlers

import (
	"dex/consensus"
	"dex/pb"
	"dex/types"
	"fmt"
	"io"
	"net/http"
)

                                           
func (hm *HandlerManager) HandlePushQuery(w http.ResponseWriter, r *http.Request) {
	trace := pushTraceFromRequest(r)
	hm.Stats.RecordAPICall("HandlePushQuery")
	decodeStarted := trace.begin("decode")
	pushQuery, err := decodePushQueryRequest(r)
	trace.end(pushDecode, decodeStarted)
	trace.query(pushQuery, false)
	if err != nil {
		http.Error(w, "Invalid PushQuery proto", http.StatusBadRequest)
		return
	}
	                                                                   
	                                                                   
	verifyStarted := trace.begin("verify")
	digest, err := hm.validatePushQuery(pushQuery)
	trace.end(pushVerify, verifyStarted)
	if err != nil {
		hm.Logger.Warn("[PushQuery] rejected envelope: %v", err)
		http.Error(w, "Invalid PushQuery envelope", http.StatusUnauthorized)
		return
	}
	trace.query(pushQuery, true)
	trace.begin("consensus_unavailable")
	if hm.consensusManager == nil {
		http.Error(w, "Consensus unavailable", http.StatusServiceUnavailable)
		return
	}
	acceptedStarted := trace.begin("accepted")
	_, localAccepted := hm.consensusManager.GetLastAccepted()
	trace.end(pushAccepted, acceptedStarted)
	replayStarted := trace.begin("replay_or_window")
	reservation := hm.queryReplay.reserveWithToken(pushQuery.Address, pushQuery.RequestedHeight, localAccepted, digest)
	trace.end(pushReplay, replayStarted)
	if reservation == nil {
		w.WriteHeader(http.StatusOK)
		return
	}

	                                                  
	senderAddress := pushQuery.Address
	                                     
	                      
	                            
	adapterStarted := trace.begin("adapter")
	msg, err := hm.adapter.PushQueryToConsensusMessage(pushQuery, types.NodeID(senderAddress))
	trace.end(pushAdapter, adapterStarted)
	if err != nil {
		hm.queryReplay.release(reservation)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	                                  
	                                                       
	if rt, ok := hm.consensusManager.Transport.(*consensus.RealTransport); ok {
		enqueueStarted := trace.begin("enqueue")
		err := rt.EnqueueReceivedMessage(msg)
		trace.end(pushEnqueue, enqueueStarted)
		if err != nil {
			hm.queryReplay.release(reservation)
			hm.Logger.Warn("[Handler] Failed to enqueue message: %v", err)
			                                                               
			w.Header().Set("Retry-After", "1")
			http.Error(w, "Service temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
	}

	                                                        
	trace.begin("accepted_for_processing")
	w.WriteHeader(http.StatusOK)
}

func decodePushQueryRequest(r *http.Request) (*pb.PushQuery, error) {
	if r == nil {
		return nil, fmt.Errorf("nil request")
	}
	if verified := verifiedPushQueryFromContext(r.Context()); verified != nil {
		return verified, nil
	}
	trace := pushTraceFromRequest(r)
	bodyStarted := trace.begin("body")
	bodyBytes, err := io.ReadAll(r.Body)
	trace.end(pushBody, bodyStarted)
	if trace != nil {
		trace.bodyBytes = len(bodyBytes)
	}
	if err != nil {
		return nil, fmt.Errorf("read request body: %w", err)
	}
	msg := &pb.PushQuery{}
	if err := pb.UnmarshalSanitize(bodyBytes, msg); err != nil {
		return nil, err
	}
	return msg, nil
}
