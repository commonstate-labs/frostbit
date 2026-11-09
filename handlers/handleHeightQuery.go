package handlers

import (
	"dex/pb"
	"net/http"

	"google.golang.org/protobuf/proto"
)

func (hm *HandlerManager) HandleHeightQuery(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("HandleHeightQuery")
	                                    
	_, height := hm.consensusManager.GetLastAccepted()
	currentHeight := hm.consensusManager.GetCurrentHeight()
	pendingCount := hm.consensusManager.GetPendingBlocksCount()

	                                         
	var pendingTxCount uint32
	if hm.txPool != nil {
		pendingTxCount = uint32(hm.txPool.PendingLen())
	}

	                     
	syncStatus := hm.consensusManager.GetSyncStatus()

	resp := &pb.HeightResponse{                           
		LastAcceptedHeight: height,
		CurrentHeight:      currentHeight,
		Address:            hm.address,
		PendingBlocksCount: uint32(pendingCount),
		PendingTxCount:     pendingTxCount,
		IsSyncing:          syncStatus.IsSyncing,
		SyncTargetHeight:   syncStatus.SyncTargetHeight,
	}

	data, _ := proto.Marshal(resp)
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.Write(data)
}
