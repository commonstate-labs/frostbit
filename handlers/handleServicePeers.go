package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"dex/sender"
	"dex/servicepeer"
	"dex/types"
)

const (
	serviceExchangeRequestLimit = 64 << 10
	serviceProbeRequestLimit    = 64 << 10
	serviceGossipRequestLimit   = 4 << 20
	serviceSyncRequestLimit     = 128 << 10
	serviceControlReadTimeout   = 5 * time.Second
	serviceGossipReadTimeout    = 15 * time.Second
)

func (hm *HandlerManager) HandleServicePeerExchange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if hm.senderManager == nil || !hm.senderManager.ServicePeerEnabled() {
		http.Error(w, "service peer role unavailable", http.StatusServiceUnavailable)
		return
	}
	var request servicepeer.ExchangeRequest
	if err := decodeBoundedServiceRequestJSON(w, r, serviceExchangeRequestLimit, serviceControlReadTimeout, &request); err != nil {
		http.Error(w, "invalid service peer exchange", http.StatusBadRequest)
		return
	}
	response, err := hm.senderManager.BuildServicePeerExchange(request, extractIP(r))
	if err != nil {
		hm.Logger.Debug("[ServicePeer] exchange rejected: %v", err)
		http.Error(w, "service peer exchange rejected", http.StatusForbidden)
		return
	}
	writeServiceJSON(w, response)
}

func (hm *HandlerManager) HandleServicePeerProbe(w http.ResponseWriter, r *http.Request) {
	hm.handleServicePeerProbe(w, r, false)
}

func (hm *HandlerManager) HandleServicePeerCallback(w http.ResponseWriter, r *http.Request) {
	hm.handleServicePeerProbe(w, r, true)
}

func (hm *HandlerManager) handleServicePeerProbe(w http.ResponseWriter, r *http.Request, callback bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if hm.senderManager == nil || !hm.senderManager.ServicePeerEnabled() {
		http.Error(w, "service peer role unavailable", http.StatusServiceUnavailable)
		return
	}
	var request servicepeer.ProbeRequest
	if err := decodeBoundedServiceRequestJSON(w, r, serviceProbeRequestLimit, serviceControlReadTimeout, &request); err != nil {
		http.Error(w, "invalid service peer probe", http.StatusBadRequest)
		return
	}
	var (
		response servicepeer.ProbeResponse
		release  func()
		err      error
	)
	if callback {
		response, err = hm.senderManager.AnswerServiceCallback(request, extractIP(r))
	} else {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		response, release, err = hm.senderManager.AnswerServiceProbe(ctx, request, extractIP(r))
		cancel()
	}
	if err != nil {
		hm.Logger.Debug("[ServicePeer] probe rejected callback=%t: %v", callback, err)
		if errors.Is(err, sender.ErrServiceArchiveCapacity) || errors.Is(err, sender.ErrServiceProbeCapacity) {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		if errors.Is(err, sender.ErrServiceContributionRequired) {
			http.Error(w, err.Error(), http.StatusPreconditionRequired)
			return
		}
		if errors.Is(err, sender.ErrServiceProofUnavailable) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, "service peer probe rejected", http.StatusForbidden)
		return
	}
	if release != nil {
		defer release()
		defer beginArchiveResponse(w)()
	}
	writeServiceJSON(w, response)
}

func (hm *HandlerManager) HandleServiceBlockGossip(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if hm.senderManager == nil || !hm.senderManager.ServicePeerEnabled() {
		http.Error(w, "service peer role unavailable", http.StatusServiceUnavailable)
		return
	}
	var envelope servicepeer.SignedPayload
	if err := decodeBoundedServiceRequestJSON(w, r, serviceGossipRequestLimit, serviceGossipReadTimeout, &envelope); err != nil {
		http.Error(w, "invalid service gossip envelope", http.StatusBadRequest)
		return
	}
	relay, err := hm.senderManager.VerifyServiceSignedPayload(envelope, sender.ServicePeerGossipPath, extractIP(r))
	if err != nil {
		hm.Logger.Debug("[ServicePeer] gossip authentication rejected: %v", err)
		http.Error(w, "service gossip authentication failed", http.StatusForbidden)
		return
	}
	if hm.handleBlockGossipBytes(w, envelope.Payload, types.NodeID(relay.NodeID)) {
		hm.senderManager.RecordServicePeerSuccess(relay.NodeID)
	} else {
		hm.senderManager.RecordServicePeerFailure(relay.NodeID)
	}
}

func (hm *HandlerManager) HandleServiceSyncBlocks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if hm.senderManager == nil || !hm.senderManager.ServicePeerEnabled() {
		writeSyncBlocksError(w, http.StatusServiceUnavailable, "service peer role unavailable")
		return
	}
	var envelope servicepeer.SignedPayload
	if err := decodeBoundedServiceRequestJSON(w, r, serviceSyncRequestLimit, serviceControlReadTimeout, &envelope); err != nil {
		writeSyncBlocksError(w, http.StatusBadRequest, "invalid service sync envelope")
		return
	}
	requester, err := hm.senderManager.VerifyServiceSignedPayload(envelope, sender.ServicePeerSyncPath, extractIP(r))
	if err != nil {
		hm.Logger.Debug("[ServicePeer] sync authentication rejected: %v", err)
		writeSyncBlocksError(w, http.StatusForbidden, "service sync authentication failed")
		return
	}
	if !hm.senderManager.CanServicePeerConsumeData(requester.NodeID) {
		writeSyncBlocksError(w, http.StatusForbidden, "service peer contribution proof required")
		return
	}

	var request types.SyncBlocksRequest
	if err := decodeBoundedServiceJSON(bytes.NewReader(envelope.Payload), serviceSyncRequestLimit, &request); err != nil {
		hm.senderManager.RecordServicePeerFailure(requester.NodeID)
		writeSyncBlocksError(w, http.StatusBadRequest, "invalid service sync request")
		return
	}
	release, ok := hm.senderManager.AcquireServiceSyncServe(extractIP(r))
	if !ok {
		writeSyncBlocksError(w, http.StatusServiceUnavailable, "service sync capacity exhausted")
		return
	}
	defer release()
	response, status, errMessage := hm.buildSyncBlocksResponse(request)
	if errMessage != "" {
		if status == http.StatusBadRequest {
			hm.senderManager.RecordServicePeerFailure(requester.NodeID)
		}
		writeSyncBlocksError(w, status, errMessage)
		return
	}
	if status, errMessage := validateBoundedSyncBlocksResponse(response); errMessage != "" {
		writeSyncBlocksError(w, status, errMessage)
		return
	}
	if !hm.senderManager.AuthorizeServicePeerDataConsumption(requester.NodeID) {
		writeSyncBlocksError(w, http.StatusForbidden, "service peer contribution proof required")
		return
	}
	writeBoundedSyncBlocksResponse(w, response)
}

func decodeBoundedServiceRequestJSON(
	w http.ResponseWriter,
	r *http.Request,
	limit int64,
	readTimeout time.Duration,
	dst any,
) error {
	if w == nil || r == nil || r.Body == nil || readTimeout <= 0 {
		return fmt.Errorf("invalid bounded service request")
	}
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Now().Add(readTimeout))
	defer func() { _ = controller.SetReadDeadline(time.Time{}) }()
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	return decodeBoundedServiceJSON(r.Body, limit, dst)
}

func decodeBoundedServiceJSON(reader io.Reader, limit int64, dst any) error {
	if reader == nil || limit <= 0 {
		return fmt.Errorf("invalid bounded JSON input")
	}
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > limit {
		return fmt.Errorf("JSON body exceeds %d bytes", limit)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func writeServiceJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
