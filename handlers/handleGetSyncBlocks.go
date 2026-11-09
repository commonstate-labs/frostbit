package handlers

import (
	"bytes"
	"dex/consensus"
	"dex/pb"
	"dex/types"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
)

const (
	archiveSyncMaxRawResponseBytes = int64(consensus.AcceptedFrontierMaxSpan) * int64(pb.MaxTotalSize)
	archiveRequestBodyLimit        = int64(16 << 10)
	archiveRequestReadTimeout      = 5 * time.Second
	archiveSyncWriteTimeout        = 30 * time.Second
)

                                                  
func (hm *HandlerManager) HandleGetSyncBlocks(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("HandleGetSyncBlocks")
	if !hm.allowArchiveDataRequest(r) {
		writeSyncBlocksError(w, http.StatusForbidden, "authenticated service sync is required")
		return
	}
	var req types.SyncBlocksRequest
	if err := decodeArchiveJSONRequest(w, r, &req, false); err != nil {
		writeSyncBlocksError(w, http.StatusBadRequest, "invalid json request")
		return
	}

	                                                                  
	                                                                        
	                                                                        
	release, ok := hm.acquireArchiveDataServe(r)
	if !ok {
		writeSyncBlocksError(w, http.StatusServiceUnavailable, "block sync service capacity exhausted")
		return
	}
	defer release()
	resp, status, errMessage := hm.buildSyncBlocksResponse(req)
	if errMessage != "" {
		writeSyncBlocksError(w, status, errMessage)
		return
	}
	if status, errMessage := validateBoundedSyncBlocksResponse(resp); errMessage != "" {
		writeSyncBlocksError(w, status, errMessage)
		return
	}
	if !hm.authorizeArchiveDataRequest(r) {
		writeSyncBlocksError(w, http.StatusForbidden, "service peer contribution proof required")
		return
	}

	writeBoundedSyncBlocksResponse(w, resp)
}

                                                                              
                                                                               
                                                                            
                                                                               
                                                                         
func (hm *HandlerManager) allowArchiveDataRequest(r *http.Request) bool {
	if r == nil || hm == nil {
		return false
	}
	                                                                           
	                                                                            
	                                               
	if hm.peerGate != nil && hm.peerGate.isExplorerService(r) {
		return true
	}
	source, err := netip.ParseAddr(strings.TrimSpace(extractIP(r)))
	if err != nil {
		return false
	}
	source = source.Unmap()
	if hm.isExplicitArchiveLoopback(source) || hm.isExplicitPrivateArchiveDevelopment(source) {
		return true
	}
	if hm.senderManager != nil {
		if _, ok := hm.senderManager.CanServicePeerConsumeDataBySourceIP(source.String()); ok {
			return true
		}
	}
	return hm.isExactArchiveMinerRequest(r, source)
}

                                                                             
                                                                              
                                                                      
                                                                         
                                                                      
func (hm *HandlerManager) authorizeArchiveDataRequest(r *http.Request) bool {
	if r == nil || hm == nil {
		return false
	}
	if hm.peerGate != nil && hm.peerGate.isExplorerService(r) {
		return true
	}
	source, err := netip.ParseAddr(strings.TrimSpace(extractIP(r)))
	if err != nil {
		return false
	}
	source = source.Unmap()
	if hm.isExplicitArchiveLoopback(source) || hm.isExplicitPrivateArchiveDevelopment(source) || hm.isExactArchiveMinerRequest(r, source) {
		return true
	}
	if hm.senderManager == nil {
		return false
	}
	_, ok := hm.senderManager.AuthorizeServicePeerDataConsumptionBySourceIP(source.String())
	return ok
}

func (hm *HandlerManager) isExplicitArchiveLoopback(source netip.Addr) bool {
	if hm == nil || hm.cfg == nil || !source.Unmap().IsLoopback() {
		return false
	}
	advertise, err := netip.ParseAddr(strings.Trim(strings.TrimSpace(hm.cfg.Network.AdvertiseHost), "[]"))
	return err == nil && advertise.Unmap().IsLoopback()
}

                                                                             
                                                                             
                                                                            
                                                                          
                       
func (hm *HandlerManager) isExplicitPrivateArchiveDevelopment(source netip.Addr) bool {
	if hm == nil || hm.cfg == nil || !hm.cfg.Server.EnableDebugAPI {
		return false
	}
	source = source.Unmap()
	if !source.Is4() || !source.IsPrivate() {
		return false
	}
	advertise, err := netip.ParseAddr(strings.Trim(strings.TrimSpace(hm.cfg.Network.AdvertiseHost), "[]"))
	return err == nil && advertise.Unmap().Is4() && advertise.Unmap().IsPrivate()
}

func (hm *HandlerManager) isExactArchiveMinerRequest(r *http.Request, source netip.Addr) bool {
	if hm == nil || r == nil || hm.peerGate == nil || hm.dbManager == nil {
		return false
	}
	address := strings.TrimSpace(r.Header.Get("X-Node-Address"))
	                                                                           
	                                                                          
	                                                                            
	                                                     
	if address == "" || !hm.peerGate.isMinerOrFrozen(address) {
		return false
	}
	account, err := hm.dbManager.GetAccount(address)
	if err != nil || account == nil {
		return false
	}
	expected, err := numericEndpointIP(account.Ip)
	if err != nil {
		return false
	}
	return expected == source.Unmap()
}

                                                                        
                                                                              
                                                                            
                                                                
func numericEndpointIP(endpoint string) (netip.Addr, error) {
	addrPort, err := netip.ParseAddrPort(strings.TrimSpace(endpoint))
	if err != nil || addrPort.Port() == 0 {
		return netip.Addr{}, fmt.Errorf("invalid numeric endpoint")
	}
	address := addrPort.Addr().Unmap()
	if !address.IsValid() || address.IsUnspecified() {
		return netip.Addr{}, fmt.Errorf("invalid numeric endpoint")
	}
	return address, nil
}

                                                                             
                                                                    
func (hm *HandlerManager) allowLegacyMinerSync(r *http.Request) bool {
	return hm.allowArchiveDataRequest(r)
}

func (hm *HandlerManager) acquireArchiveDataServe(r *http.Request) (func(), bool) {
	if hm == nil || r == nil {
		return nil, false
	}
	if hm.senderManager != nil {
		if hm.isPrivilegedArchiveDataRequest(r) {
			return hm.senderManager.AcquirePrivilegedArchiveServe(extractIP(r))
		}
		return hm.senderManager.AcquireServiceSyncServe(extractIP(r))
	}
	                                                                            
	                                                                            
	                                                
	if hm.allowArchiveDataRequest(r) {
		if hm.peerGate != nil && hm.peerGate.isExplorerService(r) {
			return func() {}, true
		}
		source, err := netip.ParseAddr(strings.TrimSpace(extractIP(r)))
		if err == nil && source.Unmap().IsLoopback() {
			return func() {}, true
		}
	}
	return nil, false
}

func (hm *HandlerManager) isPrivilegedArchiveDataRequest(r *http.Request) bool {
	if hm == nil || r == nil {
		return false
	}
	if hm.peerGate != nil && hm.peerGate.isExplorerService(r) {
		return true
	}
	source, err := netip.ParseAddr(strings.TrimSpace(extractIP(r)))
	if err != nil {
		return false
	}
	source = source.Unmap()
	return hm.isExplicitArchiveLoopback(source) || hm.isExactArchiveMinerRequest(r, source)
}

func beginArchiveResponse(w http.ResponseWriter) func() {
	controller := http.NewResponseController(w)
	_ = controller.SetWriteDeadline(time.Now().Add(archiveSyncWriteTimeout))
	return func() { _ = controller.SetWriteDeadline(time.Time{}) }
}

func readArchiveRequestBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	if w == nil || r == nil || r.Body == nil {
		return nil, fmt.Errorf("missing sync-data request body")
	}
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Now().Add(archiveRequestReadTimeout))
	defer func() { _ = controller.SetReadDeadline(time.Time{}) }()

	r.Body = http.MaxBytesReader(w, r.Body, archiveRequestBodyLimit)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func decodeArchiveJSONRequest(w http.ResponseWriter, r *http.Request, dst any, allowEmpty bool) error {
	data, err := readArchiveRequestBody(w, r)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		if allowEmpty {
			return nil
		}
		return fmt.Errorf("empty sync-data request body")
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

func (hm *HandlerManager) buildSyncBlocksResponse(req types.SyncBlocksRequest) (types.SyncBlocksResponse, int, string) {
	if req.ToHeight < req.FromHeight {
		return types.SyncBlocksResponse{}, http.StatusBadRequest, "invalid height range"
	}
	                                                                        
	                                                                       
	if req.ToHeight-req.FromHeight >= consensus.AcceptedFrontierMaxSpan {
		return types.SyncBlocksResponse{}, http.StatusBadRequest, "height range exceeds accepted-frontier maximum"
	}
	if hm.consensusManager == nil || hm.dbManager == nil {
		return types.SyncBlocksResponse{}, http.StatusServiceUnavailable, "sync block storage unavailable"
	}

	resp := types.SyncBlocksResponse{
		Blocks: collectContiguousSyncBlockBundles(req.FromHeight, req.ToHeight, func(h uint64) (string, *pb.Block, bool) {
			blockID, ok := hm.consensusManager.GetFinalizedBlockID(h)
			if !ok || blockID == "" {
				hm.Logger.Warn("[HandleGetSyncBlocks] No finalized block at height %d", h)
				return "", nil, false
			}

			dbBlock, err := hm.dbManager.GetBlockByID(blockID)
			if err != nil || dbBlock == nil {
				hm.Logger.Warn("[HandleGetSyncBlocks] Finalized block %s at height %d not found in DB: %v", blockID, h, err)
				return "", nil, false
			}
			return blockID, dbBlock, true
		}),
	}
	expectedBlocks := req.ToHeight - req.FromHeight + 1
	if uint64(len(resp.Blocks)) != expectedBlocks {
		return types.SyncBlocksResponse{}, http.StatusNotFound, "requested finalized block range unavailable"
	}
	return resp, http.StatusOK, ""
}

type syncBlockLookup func(height uint64) (blockID string, block *pb.Block, ok bool)

func collectContiguousSyncBlockBundles(fromHeight, toHeight uint64, lookup syncBlockLookup) []types.SyncBlockBundle {
	capacity := int(toHeight - fromHeight + 1)
	bundles := make([]types.SyncBlockBundle, 0, capacity)
	for height := fromHeight; height <= toHeight; height++ {
		blockID, block, ok := lookup(height)
		if !ok || blockID == "" || block == nil {
			break
		}
		blockBytes, err := proto.Marshal(block)
		if err != nil {
			break
		}
		bundles = append(bundles, types.SyncBlockBundle{
			Height:     height,
			BlockID:    blockID,
			BlockBytes: blockBytes,
		})
		if height == toHeight {
			break
		}
	}
	return bundles
}

func writeSyncBlocksError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(types.SyncBlocksResponse{Error: msg})
}

func validateBoundedSyncBlocksResponse(response types.SyncBlocksResponse) (int, string) {
	var rawBytes int64
	for i, bundle := range response.Blocks {
		payloadBytes := int64(len(bundle.BlockBytes))
		if payloadBytes <= 0 || payloadBytes > int64(pb.MaxTotalSize) {
			return http.StatusInternalServerError, fmt.Sprintf("stored block payload %d exceeds service limit", i)
		}
		if rawBytes > archiveSyncMaxRawResponseBytes-payloadBytes {
			return http.StatusRequestEntityTooLarge, "sync response exceeds byte limit"
		}
		rawBytes += payloadBytes
	}
	return 0, ""
}

func writeBoundedSyncBlocksResponse(w http.ResponseWriter, response types.SyncBlocksResponse) {
	if status, errMessage := validateBoundedSyncBlocksResponse(response); errMessage != "" {
		writeSyncBlocksError(w, status, errMessage)
		return
	}
	defer beginArchiveResponse(w)()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}
