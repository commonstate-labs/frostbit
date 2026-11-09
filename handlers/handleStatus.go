package handlers

import (
	"dex/pb"
	"fmt"
	"net/http"

	"google.golang.org/protobuf/proto"
)

                     
func (hm *HandlerManager) HandleStatus(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("HandleStatus")
	if !hm.checkAuth(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	resProto := &pb.StatusResponse{
		Status: "ok",
		Info:   fmt.Sprintf("Server is running on port %s", hm.port),
	}

	resProto.LivenessState = -1                                   
	if hm.address != "" {
		if acc, _ := hm.dbManager.GetAccount(hm.address); acc != nil {
			resProto.IsMiner = acc.IsMiner
		}
		if hm.peerGate != nil && hm.peerGate.livenessTracker != nil {
			resProto.LivenessState = int32(hm.peerGate.livenessTracker.GetMinerState(hm.address))
		}
	}

	data, err := proto.Marshal(resProto)
	if err != nil {
		http.Error(w, "Failed to marshal status response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}
