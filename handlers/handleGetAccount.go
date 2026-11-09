package handlers

import (
	"dex/pb"
	"io"
	"net/http"

	"google.golang.org/protobuf/proto"
)

                                                     
func (hm *HandlerManager) HandleGetAccount(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("HandleGetAccount")

	                  
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		resp := &pb.GetAccountResponse{Error: "failed to read request body"}
		data, _ := proto.Marshal(resp)
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.Write(data)
		return
	}

	var req pb.GetAccountRequest
	if err := pb.UnmarshalSanitize(bodyBytes, &req); err != nil {
		resp := &pb.GetAccountResponse{Error: "invalid request proto"}
		data, _ := proto.Marshal(resp)
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.Write(data)
		return
	}

	if req.Address == "" {
		resp := &pb.GetAccountResponse{Error: "address is required"}
		data, _ := proto.Marshal(resp)
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.Write(data)
		return
	}

	                                 
	account, err := hm.dbManager.GetAccount(req.Address)
	if err != nil {
		resp := &pb.GetAccountResponse{Error: "account not found: " + err.Error()}
		data, _ := proto.Marshal(resp)
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.Write(data)
		return
	}

	resp := &pb.GetAccountResponse{Account: account}

	                                                                                                    
	                                                                                             
	                                                                       
	if hm.peerGate != nil && hm.peerGate.livenessTracker != nil {
		if snap := hm.peerGate.livenessTracker.GetMinerSnapshot(req.Address); snap != nil {
			resp.Liveness = &pb.MinerLivenessInfo{
				Index:        snap.Index,
				State:        int32(snap.State),
				StateName:    livenessStateName(snap.State),
				Stake:        snap.Stake,
				JoinHeight:   snap.JoinHeight,
				ExitHeight:   snap.ExitHeight,
				TotalSlashed: snap.TotalSlashed,
				Kicked:       snap.Kicked,
			}
		}
	}

	data, _ := proto.Marshal(resp)
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.Write(data)
}

                                                                                 
                                                                                  
func livenessStateName(state int) string {
	switch state {
	case 0:
		return "probation"
	case 1:
		return "active"
	case 2:
		return "frozen"
	default:
		return "unknown"
	}
}
