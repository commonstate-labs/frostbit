package handlers

import (
	"dex/consensus"
	"dex/logs"
	"dex/pb"
	"dex/types"
	"io"
	"net/http"
)

func (hm *HandlerManager) HandleChits(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("HandleChits")
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	var chits pb.Chits
	if err := pb.UnmarshalSanitize(bodyBytes, &chits); err != nil {
		http.Error(w, "Invalid Chits proto", http.StatusBadRequest)
		return
	}
	if err := hm.validateChitsEnvelope(&chits); err != nil {
		hm.Logger.Warn("[Chits] rejected envelope: %v", err)
		http.Error(w, "Invalid Chits envelope", http.StatusUnauthorized)
		return
	}
	                                 
	if hm.adapter != nil {
		                                                      
		senderAddress := chits.Address

		if !hm.verifyNodeIdentity(senderAddress, &chits) {
			http.Error(w, "Invalid sender", http.StatusUnauthorized)
			return
		}

		from := types.NodeID(senderAddress)
		msg := hm.adapter.ChitsToConsensusMessage(&chits, from)

		                                                                                       
		if hm.consensusManager != nil {
			_, localAcceptedHeight := hm.consensusManager.GetLastAccepted()
			if chits.GetPreferredBlockAtHeight() > 0 &&
				chits.GetPreferredBlockAtHeight() <= localAcceptedHeight &&
				chits.GetAcceptedHeight() <= localAcceptedHeight {
				w.WriteHeader(http.StatusOK)
				return
			}
		}

		                                                    
		if rt, ok := hm.consensusManager.Transport.(*consensus.RealTransport); ok {
			if err := rt.EnqueueReceivedMessage(msg); err != nil {
				logs.Warn("[Handler] Failed to enqueue chits message: %v", err)
				                                           
				w.Header().Set("Retry-After", "1")
				http.Error(w, "Service temporarily unavailable", http.StatusServiceUnavailable)
				return
			}
		}
	}

	w.WriteHeader(http.StatusOK)
}
