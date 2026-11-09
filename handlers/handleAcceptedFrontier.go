package handlers

import (
	"dex/consensus"
	"dex/pb"
	"dex/types"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"google.golang.org/protobuf/proto"
)

type acceptedFrontierResponseSigner interface {
	SignAcceptedFrontierResponse(consensus.AcceptedFrontierQuery, consensus.AcceptedFrontierVote) (*pb.AcceptedFrontierResponse, error)
}

var errAcceptedFrontierEmergencyStop = errors.New("accepted frontier: emergency stop active")
var errAcceptedFrontierRequesterLimited = errors.New("accepted frontier: requester rate limited")

                                                                             
                                                                          
                                                                          
func (hm *HandlerManager) HandleAcceptedFrontier(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("HandleAcceptedFrontier")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if hm.emergency != nil && hm.emergency.IsStopped() {
		http.Error(w, "emergency stop active", http.StatusServiceUnavailable)
		return
	}
	const maxRequestBytes = int64(16 << 10)
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBytes+1))
	if err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if int64(len(body)) > maxRequestBytes {
		http.Error(w, "accepted frontier request too large", http.StatusRequestEntityTooLarge)
		return
	}
	var req pb.AcceptedFrontierRequest
	if err := pb.UnmarshalSanitize(body, &req); err != nil {
		http.Error(w, "invalid accepted frontier request", http.StatusBadRequest)
		return
	}
	cacheKey := acceptedFrontierReplayKey(&req)
	if encoded, exists := hm.getAcceptedFrontierResponseCache().get(cacheKey); exists {
		writeAcceptedFrontierResponse(w, encoded)
		return
	}

	query, err := consensus.VerifyAcceptedFrontierRequest(&req)
	if err != nil {
		hm.Logger.Warn("[AcceptedFrontier] rejected request: %v", err)
		http.Error(w, "invalid accepted frontier request", http.StatusUnauthorized)
		return
	}
	if hm.consensusManager == nil || hm.address == "" {
		http.Error(w, "consensus unavailable", http.StatusServiceUnavailable)
		return
	}

	                                                                          
	                                                                             
	                                       
	encoded, err := hm.getAcceptedFrontierResponseCache().getOrBuildAdmitted(cacheKey, func() error {
		if !hm.getAcceptedFrontierRequesterLimiter().allow(query.Requester, time.Now()) {
			return errAcceptedFrontierRequesterLimited
		}
		return nil
	}, func() ([]byte, error) {
		return hm.buildAcceptedFrontierResponse(query)
	})
	if err != nil {
		if errors.Is(err, errAcceptedFrontierEmergencyStop) {
			http.Error(w, "emergency stop active", http.StatusServiceUnavailable)
			return
		}
		if errors.Is(err, errAcceptedFrontierRequesterLimited) {
			http.Error(w, "accepted frontier requester rate limited", http.StatusTooManyRequests)
			return
		}
		hm.Logger.Warn("[AcceptedFrontier] response build failed: %v", err)
		http.Error(w, "accepted frontier signing unavailable", http.StatusServiceUnavailable)
		return
	}
	writeAcceptedFrontierResponse(w, encoded)
}

func writeAcceptedFrontierResponse(w http.ResponseWriter, encoded []byte) {
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(encoded)
}

func (hm *HandlerManager) getAcceptedFrontierResponseCache() *acceptedFrontierResponseCache {
	hm.acceptedFrontierCacheOnce.Do(func() {
		if hm.acceptedFrontierCache == nil {
			hm.acceptedFrontierCache = newAcceptedFrontierResponseCache(acceptedFrontierResponseCacheCapacity)
		}
	})
	return hm.acceptedFrontierCache
}

func (hm *HandlerManager) getAcceptedFrontierRequesterLimiter() *acceptedFrontierRequesterLimiter {
	hm.acceptedFrontierLimiterOnce.Do(func() {
		if hm.acceptedFrontierLimiter == nil {
			hm.acceptedFrontierLimiter = newAcceptedFrontierRequesterLimiter(
				acceptedFrontierRequesterRatePerSecond,
				acceptedFrontierRequesterBurst,
				acceptedFrontierRequesterLimitCapacity,
			)
		}
	})
	return hm.acceptedFrontierLimiter
}

func (hm *HandlerManager) buildAcceptedFrontierResponse(query consensus.AcceptedFrontierQuery) ([]byte, error) {
	_, acceptedHeight := hm.consensusManager.GetLastAccepted()
	vote := consensus.AcceptedFrontierVote{
		Peer:         types.NodeID(hm.address),
		Status:       consensus.AcceptedFrontierStatusUnavailable,
		TargetHeight: query.TargetHeight,
	}

	anchorID, anchorAvailable := hm.consensusManager.GetFinalizedBlockID(query.AnchorHeight)
	if anchorAvailable {
		vote.ObservedAnchorID = anchorID
		switch {
		case anchorID != query.AnchorBlockID:
			vote.Status = consensus.AcceptedFrontierStatusAnchorMismatch
		case acceptedHeight < query.TargetHeight:
			vote.Status = consensus.AcceptedFrontierStatusBelowTarget
			vote.AcceptedHeight = acceptedHeight
		default:
			targetID, targetAvailable := hm.consensusManager.GetFinalizedBlockID(query.TargetHeight)
			if targetAvailable {
				vote.Status = consensus.AcceptedFrontierStatusOK
				vote.AcceptedHeight = acceptedHeight
				vote.TargetBlockID = targetID
			}
		}
	}
	if hm.emergency != nil && hm.emergency.IsStopped() {
		return nil, errAcceptedFrontierEmergencyStop
	}

	resp, err := hm.signAcceptedFrontierResponse(query, vote)
	if err != nil {
		return nil, fmt.Errorf("sign accepted frontier response: %w", err)
	}
	encoded, err := proto.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("marshal accepted frontier response: %w", err)
	}
	return encoded, nil
}

func (hm *HandlerManager) signAcceptedFrontierResponse(query consensus.AcceptedFrontierQuery, vote consensus.AcceptedFrontierVote) (*pb.AcceptedFrontierResponse, error) {
	if hm.consensusManager != nil {
		if signer, ok := hm.consensusManager.Transport.(acceptedFrontierResponseSigner); ok {
			return signer.SignAcceptedFrontierResponse(query, vote)
		}
	}
	if hm.keyMgr != nil {
		return consensus.NewSignedAcceptedFrontierResponse(query, vote, hm.keyMgr)
	}
	return nil, fmt.Errorf("no accepted frontier response signer configured")
}
