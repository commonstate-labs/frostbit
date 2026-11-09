                             
package consensus

import (
	"context"
	"dex/db"
	"dex/interfaces"
	"dex/logs"
	"dex/pb"
	"dex/sender"
	"dex/servicepeer"
	"dex/stats"
	"dex/types"
	"fmt"
	"math/rand"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

                                                        
type RealTransport struct {
	nodeID        types.NodeID
	address       string
	dbManager     *db.Manager
	ctx           context.Context
	mu            sync.RWMutex
	senderManager *sender.SenderManager
	adapter       *ConsensusAdapter
	signer        interfaces.NodeSigner
	Logger        logs.Logger

	inbox          chan types.Message
	receiveQueue   chan types.Message
	receiveWorkers int

	stopOnce       sync.Once
	stopChan       chan struct{}
	wg             sync.WaitGroup
	Stats          *stats.Stats
	packetLossRate float64                                        
	minLatency     time.Duration                
	maxLatency     time.Duration                
	nodeIPCache    map[types.NodeID]string
	cacheMu        sync.RWMutex
	                                                                           
	                                                                          
	                                                                        
	                                                                           
	                                              
	bootstrapPeers map[types.NodeID]string
	servicePeers   *servicepeer.Store

	controlEnqueueTimeoutDrops atomic.Uint64
	dataEnqueueFullDrops       atomic.Uint64
	inboxForwardTimeoutDrops   atomic.Uint64
	preprocessInvalidDrops     atomic.Uint64
	inboxTimeoutByType         sync.Map                                 
	inboxTimeoutBySender       sync.Map                                 
	paused                     atomic.Int32                                 
}

                                                                    
                                                                              
func (t *RealTransport) SetServicePeerStore(store *servicepeer.Store) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.servicePeers = store
	t.mu.Unlock()
}

type RealTransportRuntimeStats struct {
	ControlEnqueueTimeoutDrops uint64
	DataEnqueueFullDrops       uint64
	InboxForwardTimeoutDrops   uint64
	PreprocessInvalidDrops     uint64
}

type RealTransportTopDrop struct {
	Key   string
	Count uint64
}

type NodeInfo struct {
	Address   string
	IP        string                                                      
	PublicKey string
	LastSeen  time.Time
	Index     uint64                
}

func NewRealTransport(nodeID types.NodeID, dbMgr *db.Manager, senderMgr *sender.SenderManager, ctx context.Context) interfaces.Transport {
	return NewRealTransportWithSimulation(nodeID, dbMgr, senderMgr, ctx, 0.0, 0, 0)
}

                                                                                                
                                                                                   
func NewRealTransportWithPacketLoss(nodeID types.NodeID, dbMgr *db.Manager, senderMgr *sender.SenderManager, ctx context.Context, packetLossRate float64) interfaces.Transport {
	return NewRealTransportWithSimulation(nodeID, dbMgr, senderMgr, ctx, packetLossRate, 0, 0)
}

                                                                        
                                                                                   
                                                                      
func NewRealTransportWithSimulation(nodeID types.NodeID, dbMgr *db.Manager, senderMgr *sender.SenderManager, ctx context.Context, packetLossRate float64, minLatency, maxLatency time.Duration) interfaces.Transport {
	rt := &RealTransport{
		nodeID:         nodeID,
		address:        string(nodeID),
		inbox:          make(chan types.Message, 1000),
		receiveQueue:   make(chan types.Message, 1000),
		receiveWorkers: 8,                                                                   
		dbManager:      dbMgr,
		senderManager:  senderMgr,
		ctx:            ctx,
		adapter:        NewConsensusAdapter(dbMgr),
		stopChan:       make(chan struct{}),
		Stats:          stats.NewStats(),
		packetLossRate: packetLossRate,
		minLatency:     minLatency,
		maxLatency:     maxLatency,
		nodeIPCache:    make(map[types.NodeID]string),
		bootstrapPeers: make(map[types.NodeID]string),
		Logger:         logs.NewNodeLogger(string(nodeID)+":transport", 500),
	}

	rt.startReceiveWorkers()
	return rt
}

                                                                          
                                                                              
                                                                                     
func (t *RealTransport) SetBootstrapPeers(routes map[types.NodeID]string) {
	if t == nil {
		return
	}
	t.cacheMu.Lock()
	defer t.cacheMu.Unlock()
	t.bootstrapPeers = make(map[types.NodeID]string, len(routes))
	for address, endpoint := range routes {
		address = types.NodeID(strings.TrimSpace(string(address)))
		endpoint = strings.TrimSpace(endpoint)
		if address == "" || endpoint == "" {
			continue
		}
		t.bootstrapPeers[address] = endpoint
	}
}

func (t *RealTransport) bootstrapPeerIDs(exclude types.NodeID, limit int) []types.NodeID {
	if t == nil || limit == 0 {
		return nil
	}
	t.cacheMu.RLock()
	peers := make([]types.NodeID, 0, len(t.bootstrapPeers))
	for address := range t.bootstrapPeers {
		if address == "" || address == exclude {
			continue
		}
		peers = append(peers, address)
	}
	t.cacheMu.RUnlock()
	sort.Slice(peers, func(i, j int) bool { return peers[i] < peers[j] })
	if limit > 0 && len(peers) > limit {
		peers = peers[:limit]
	}
	return peers
}

func (t *RealTransport) bootstrapEndpoint(nodeID types.NodeID) string {
	if t == nil || nodeID == "" {
		return ""
	}
	t.cacheMu.RLock()
	endpoint := strings.TrimSpace(t.bootstrapPeers[nodeID])
	t.cacheMu.RUnlock()
	return endpoint
}

func (t *RealTransport) SetAddress(address string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.address = address
}

func (t *RealTransport) SetSigner(signer interfaces.NodeSigner) {
	t.mu.Lock()
	t.signer = signer
	nodeID := types.NodeID(t.address)
	if nodeID == "" {
		nodeID = t.nodeID
	}
	t.mu.Unlock()
	RegisterSignerPublicKey(nodeID, signer)
}

                              
func (t *RealTransport) Send(to types.NodeID, msg types.Message) error {
	if t.paused.Load() == 1 {
		return nil                            
	}
	                     
	if t.packetLossRate > 0 && rand.Float64() < t.packetLossRate {
		                                             
		                                                           
		t.Logger.Trace("[RealTransport] Packet dropped: %s -> %s, MsgType=%v", t.nodeID, to, msg.Type)
		return nil
	}

	                                                             
	if t.maxLatency > 0 && t.maxLatency > t.minLatency {
		delay := t.minLatency + time.Duration(rand.Int63n(int64(t.maxLatency-t.minLatency)))
		go func() {
			time.Sleep(delay)
			t.doSend(to, msg)
		}()
		return nil
	}

	                                 
	return t.doSend(to, msg)
}

                                  
func (t *RealTransport) doSend(to types.NodeID, msg types.Message) error {
	start := time.Now()
	t.Stats.RecordAPICall(string(msg.Type))
	targetIP, err := t.resolveMessageEndpoint(to, msg)
	if err != nil {
		t.Logger.Debug("[RealTransport] Failed to get IP for node %s: %v", to, err)
		return err
	}

	var errSend error
	switch msg.Type {
	case types.MsgPushQuery:
		errSend = t.sendPushQuery(to, targetIP, msg)
	case types.MsgPullQuery:
		errSend = t.sendPullQuery(to, targetIP, msg)
	case types.MsgChits:
		errSend = t.sendChits(to, targetIP, msg)
	case types.MsgGet:
		errSend = t.sendGet(to, targetIP, msg)
	case types.MsgPut:
		errSend = t.sendBlock(targetIP, msg)
	case types.MsgGossip:
		errSend = t.sendGossip(targetIP, msg)
	case types.MsgSyncRequest:
		errSend = t.sendSyncRequest(to, targetIP, msg)
	case types.MsgHeightQuery:
		errSend = t.sendHeightQuery(to, targetIP, msg)

	default:
		errSend = fmt.Errorf("unknown message type: %v", msg.Type)
	}

	                                         
	if duration := time.Since(start); duration > 50*time.Millisecond {
		t.Logger.Debug("[RealTransport] Send Latency: Type=%v, to=%s, duration=%v", msg.Type, to, duration)
	}

	return errSend
}

func (t *RealTransport) resolveMessageEndpoint(nodeID types.NodeID, msg types.Message) (string, error) {
	if msg.Type == types.MsgSyncRequest && msg.ServiceSync {
		return t.resolveVerifiedServiceEndpoint(nodeID)
	}
	return t.getNodeIP(nodeID)
}

                                                                            
                                                                             
                                                                                
                                              
func (t *RealTransport) resolveVerifiedServiceEndpoint(nodeID types.NodeID) (string, error) {
	if nodeID == "" {
		return "", fmt.Errorf("empty service provider nodeID")
	}
	t.mu.RLock()
	store := t.servicePeers
	t.mu.RUnlock()
	if store == nil {
		return "", fmt.Errorf("service peer store is not configured")
	}
	peer, ok := store.Get(string(nodeID))
	if !ok || peer.State != servicepeer.StateVerified {
		return "", fmt.Errorf("no verified service endpoint for address %s", nodeID)
	}
	endpoint, err := servicepeer.CanonicalPublicEndpoint(peer.Record.Endpoint)
	if err != nil || endpoint != peer.Record.Endpoint {
		if err == nil {
			err = servicepeer.ErrInvalidEndpoint
		}
		return "", fmt.Errorf("resolve verified service provider %s: %w", nodeID, err)
	}
	return endpoint, nil
}

func (t *RealTransport) getNodeIP(nodeID types.NodeID) (string, error) {
	                         
	if nodeID == "" {
		return "", fmt.Errorf("empty nodeID")
	}

	                                                                                                     
	if host, _, err := net.SplitHostPort(string(nodeID)); err == nil {
		if net.ParseIP(host) != nil {
			return string(nodeID), nil
		}
	}

	                                                                        
	                                                                           
	                                                                          
	                                                                        
	                                                                        
	acc, err := t.dbManager.GetAccount(string(nodeID))
	if err == nil && acc != nil && acc.IsMiner {
		ip := strings.TrimSpace(acc.Ip)
		if ip == "" {
			if endpoint := t.bootstrapEndpoint(nodeID); endpoint != "" {
				return endpoint, nil
			}
			return "", fmt.Errorf("verified miner %s has neither a chain endpoint nor a local bootstrap route", nodeID)
		}
		t.cacheMu.Lock()
		t.nodeIPCache[nodeID] = ip
		t.cacheMu.Unlock()
		return ip, nil
	}

	                                                                          
	nodeIDStr := string(nodeID)
	if len(nodeIDStr) < 5 {
		if index, parseErr := strconv.ParseUint(nodeIDStr, 10, 64); parseErr == nil {
			minerAcc, minerErr := t.dbManager.GetMinerByIndex(index)
			if minerErr == nil && minerAcc != nil && minerAcc.IsMiner && strings.TrimSpace(minerAcc.Ip) != "" {
				ip := strings.TrimSpace(minerAcc.Ip)
				t.cacheMu.Lock()
				t.nodeIPCache[nodeID] = ip
				t.cacheMu.Unlock()
				return ip, nil
			}
		}
	}

	                                                                        
	                                                                            
	                                                        
	verified, verifyErr := t.dbManager.GetMinerParticipantsSnapshot()
	if verifyErr != nil {
		return "", fmt.Errorf("resolve verified miner set: %w", verifyErr)
	}
	if len(verified) != 0 {
		return "", fmt.Errorf("no verified endpoint for address %s", nodeID)
	}

	                                                                        
	                                                                        
	ip := t.bootstrapEndpoint(nodeID)
	if ip == "" {
		t.cacheMu.RLock()
		ip = strings.TrimSpace(t.nodeIPCache[nodeID])
		t.cacheMu.RUnlock()
	}
	if ip != "" {
		return ip, nil
	}
	if t.senderManager != nil {
		if ip = strings.TrimSpace(t.senderManager.LookupMinerIP(string(nodeID))); ip != "" {
			return ip, nil
		}
	}

	return "", fmt.Errorf("no IP for address %s", nodeID)
}

                                          
func (t *RealTransport) sendPushQuery(target types.NodeID, targetIP string, msg types.Message) error {
	t.mu.RLock()
	address := t.address
	t.mu.RUnlock()

	base, err := t.adapter.ConsensusMessageToPushQuery(msg, address)
	if err != nil {
		return fmt.Errorf("failed to convert message to PushQuery: %v", err)
	}
	return t.sendPreparedPushQuery(target, targetIP, base, nil)
}

func (t *RealTransport) sendPreparedPushQuery(
	target types.NodeID,
	targetIP string,
	base *pb.PushQuery,
	onFailure func(error),
) error {
	fail := func(err error) error {
		if onFailure != nil {
			onFailure(err)
		}
		return err
	}
	if base == nil {
		return fail(fmt.Errorf("nil prepared PushQuery"))
	}
	pq := pushQueryEnvelope(base)
	pq.Responder = string(target)
	pq.ChainId = pb.GetDefaultChainID()
	if len(pq.Nonce) != SnowmanQueryNonceSize {
		return fail(fmt.Errorf("PushQuery nonce length %d, want %d", len(pq.Nonce), SnowmanQueryNonceSize))
	}
	t.mu.RLock()
	signer := t.signer
	t.mu.RUnlock()
	if signer != nil {
		signature, err := signer.Sign(ComputePushQueryDigest(pq))
		if err != nil {
			return fail(fmt.Errorf("failed to sign PushQuery: %w", err))
		}
		pq.Signature = signature
	}

	                                                                         
	return t.senderManager.PushQueryWithFailure(targetIP, pq, onFailure)
}

                                                                            
                                                                                  
func pushQueryEnvelope(base *pb.PushQuery) *pb.PushQuery {
	query := &pb.PushQuery{
		RequestId: base.RequestId, Address: base.Address,
		ContainerIsBlock: base.ContainerIsBlock, Container: base.Container,
		RequestedHeight: base.RequestedHeight, BlockId: base.BlockId,
		Signature: base.Signature, Nonce: base.Nonce, VrfSeed: base.VrfSeed,
		SeqId: base.SeqId, Responder: base.Responder, ChainId: base.ChainId,
	}
	query.ProtoReflect().SetUnknown(base.ProtoReflect().GetUnknown())
	return query
}

func pullQueryEnvelope(base *pb.PullQuery) *pb.PullQuery {
	query := &pb.PullQuery{
		RequestId: base.RequestId, Address: base.Address, BlockId: base.BlockId,
		RequestedHeight: base.RequestedHeight, VrfSeed: base.VrfSeed,
		SeqId: base.SeqId, Responder: base.Responder, ChainId: base.ChainId,
		Signature: base.Signature, Nonce: base.Nonce,
	}
	query.ProtoReflect().SetUnknown(base.ProtoReflect().GetUnknown())
	return query
}

                            
func (t *RealTransport) sendPullQuery(target types.NodeID, targetIP string, msg types.Message) error {
	t.mu.RLock()
	address := t.address
	t.mu.RUnlock()

	base := &pb.PullQuery{
		RequestId:       msg.RequestID,
		Address:         address,
		BlockId:         msg.BlockID,
		RequestedHeight: msg.Height,
		                                                 
		VrfSeed: msg.VRFSeed,
		SeqId:   msg.SeqID,
		Nonce:   append([]byte(nil), msg.QueryNonce...),
	}
	return t.sendPreparedPullQuery(target, targetIP, base, nil)
}

func (t *RealTransport) sendPreparedPullQuery(
	target types.NodeID,
	targetIP string,
	base *pb.PullQuery,
	onFailure func(error),
) error {
	fail := func(err error) error {
		if onFailure != nil {
			onFailure(err)
		}
		return err
	}
	if base == nil {
		return fail(fmt.Errorf("nil prepared PullQuery"))
	}
	pq := pullQueryEnvelope(base)
	pq.Responder = string(target)
	pq.ChainId = pb.GetDefaultChainID()
	if len(pq.Nonce) != SnowmanQueryNonceSize {
		return fail(fmt.Errorf("PullQuery nonce length %d, want %d", len(pq.Nonce), SnowmanQueryNonceSize))
	}
	t.mu.RLock()
	signer := t.signer
	t.mu.RUnlock()
	if signer != nil {
		signature, err := signer.Sign(ComputePullQueryDigest(pq))
		if err != nil {
			return fail(fmt.Errorf("failed to sign PullQuery: %w", err))
		}
		pq.Signature = signature
	}

	return t.senderManager.PullQueryWithFailure(targetIP, pq, onFailure)
}

func (t *RealTransport) sendChits(target types.NodeID, targetIP string, msg types.Message) error {
	chits := t.adapter.ConsensusMessageToChits(msg)
	chits.Requester = string(target)
	chits.ChainId = pb.GetDefaultChainID()
	return t.senderManager.SendChits(targetIP, chits)
}

func (t *RealTransport) sendGet(peer types.NodeID, targetIP string, msg types.Message) error {
	                                  
	t.senderManager.PullGet(targetIP, msg.BlockID, func(block *pb.Block) {
		if block != nil {
			                       
			consensusBlock, err := t.adapter.DBBlockToConsensusWithPayload(block)
			if err != nil {
				t.Logger.Error("[RealTransport] Failed to convert block: %v", err)
				return
			}

			                        
			putMsg := types.Message{
				RequestID: msg.RequestID,
				Type:      types.MsgPut,
				From:      peer,
				Block:     consensusBlock,
				Height:    consensusBlock.Header.Height,
				BlockID:   consensusBlock.ID,
				ShortTxs:  block.ShortTxs,
			}

			                              
			if err := t.EnqueueReceivedMessage(putMsg); err != nil {
				t.Logger.Debug("[RealTransport] Failed to enqueue Put message: %v", err)
			}

			t.Logger.Debug("[RealTransport] Received block %s from %s", block.BlockHash, targetIP)
		}
	})
	return nil
}

func (t *RealTransport) sendBlock(targetIP string, msg types.Message) error {
	if msg.Block == nil {
		return fmt.Errorf("no block data to send")
	}

	dbBlock := t.adapter.ConsensusBlockToDB(msg.Block, nil)
	if dbBlock != nil && len(dbBlock.ShortTxs) == 0 && len(msg.ShortTxs) > 0 {
		dbBlock.ShortTxs = msg.ShortTxs
	}
	return t.senderManager.SendBlock(targetIP, dbBlock)
}

                                            

func (t *RealTransport) sendGossip(targetIP string, msg types.Message) error {
	if len(msg.PreparedGossip) > 0 {
		return t.senderManager.BroadcastGossipBytesToTarget(targetIP, msg.PreparedGossip)
	}
	data, err := t.prepareGossip(msg)
	if err != nil {
		return err
	}
	return t.senderManager.BroadcastGossipBytesToTarget(targetIP, data)
}

func (t *RealTransport) prepareGossip(msg types.Message) ([]byte, error) {
	if msg.Block == nil {
		return nil, fmt.Errorf("no block data to send")
	}

	payload := &types.GossipPayload{
		Block:     t.adapter.ConsensusBlockToDB(msg.Block, nil),
		RequestID: msg.RequestID,
	}
	if payload.Block != nil && len(payload.Block.ShortTxs) == 0 && len(msg.ShortTxs) > 0 {
		payload.Block.ShortTxs = msg.ShortTxs
	}
	data, err := types.EncodeGossipPayload(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to encode gossip payload: %w", err)
	}
	return data, nil
}

                                                                            
                                                                           
                                                              
func (t *RealTransport) BroadcastServiceGossip(msg types.Message, fanout int) int {
	if t == nil || t.senderManager == nil || fanout <= 0 {
		return 0
	}
	data := msg.PreparedGossip
	if len(data) == 0 {
		var err error
		data, err = t.prepareGossip(msg)
		if err != nil {
			t.Logger.Debug("[RealTransport] prepare service gossip failed: %v", err)
			return 0
		}
	}
	return t.senderManager.BroadcastServiceGossip(data, fanout)
}

func (t *RealTransport) sendSyncRequest(to types.NodeID, targetIP string, msg types.Message) error {
	onSuccess := func(syncResp *types.SyncBlocksResponse) {
		if syncResp == nil || len(syncResp.Blocks) == 0 {
			return
		}

		blocks := make([]*types.Block, 0, len(syncResp.Blocks))
		syncBlocksRaw := make(map[string][]byte, len(syncResp.Blocks))

		for _, bundle := range syncResp.Blocks {
			if len(bundle.BlockBytes) == 0 {
				continue
			}

			var dbBlock pb.Block
			if err := pb.UnmarshalSanitize(bundle.BlockBytes, &dbBlock); err != nil {
				t.Logger.Warn("[RealTransport] Failed to decode sync block at height %d from %s: %v", bundle.Height, to, err)
				continue
			}

			block, err := t.adapter.DBBlockToConsensus(&dbBlock)
			if err != nil {
				t.Logger.Warn("[RealTransport] Failed to convert sync block at height %d from %s: %v", bundle.Height, to, err)
				continue
			}

			blocks = append(blocks, block)
			rawCopy := make([]byte, len(bundle.BlockBytes))
			copy(rawCopy, bundle.BlockBytes)
			syncBlocksRaw[block.ID] = rawCopy
		}

		respMsg := types.Message{
			Type:       types.MsgSyncResponse,
			From:       to,
			SyncID:     msg.SyncID,
			RequestID:  msg.RequestID,
			Blocks:     blocks,
			FromHeight: msg.FromHeight,
			ToHeight:   msg.ToHeight,
		}
		if len(syncBlocksRaw) > 0 {
			respMsg.SyncBlocksRaw = syncBlocksRaw
		}

		t.inbox <- respMsg
	}
	if msg.ServiceSync {
		                                                                     
		                                                                         
		onFailure := func(sendErr error) {
			if t.inbox == nil {
				return
			}
			reason := "service sync request failed"
			if sendErr != nil {
				reason = sendErr.Error()
			}
			const maxSyncFailureReason = 512
			if len(reason) > maxSyncFailureReason {
				reason = reason[:maxSyncFailureReason]
			}
			failure := types.Message{
				Type:       types.MsgSyncResponse,
				From:       to,
				SyncID:     msg.SyncID,
				RequestID:  msg.RequestID,
				FromHeight: msg.FromHeight,
				ToHeight:   msg.ToHeight,
				SyncFailed: true,
				SyncError:  reason,
			}
			var contextDone <-chan struct{}
			if t.ctx != nil {
				contextDone = t.ctx.Done()
			}
			select {
			case t.inbox <- failure:
			case <-t.stopChan:
			case <-contextDone:
			}
		}
		return t.senderManager.SendServiceSyncBlocksRequest(string(to), targetIP, msg.FromHeight, msg.ToHeight, onSuccess, onFailure)
	}
	return t.senderManager.SendSyncBlocksRequest(targetIP, msg.FromHeight, msg.ToHeight, onSuccess)
}

func (t *RealTransport) sendHeightQuery(to types.NodeID, targetIP string, msg types.Message) error {
	return t.senderManager.SendHeightQuery(targetIP, func(resp *pb.HeightResponse) {
		if resp == nil {
			return
		}
		t.inbox <- types.Message{
			Type:          types.MsgHeightResponse,
			From:          to,
			Height:        resp.LastAcceptedHeight,
			CurrentHeight: resp.CurrentHeight,
			RequestID:     msg.RequestID,
		}
	})
}

                                                                           
                                                                              
                                                                              
func (t *RealTransport) SendAcceptedFrontierQuery(peer types.NodeID, query AcceptedFrontierQuery, callback func(AcceptedFrontierVote)) error {
	if t == nil {
		return fmt.Errorf("accepted frontier: nil transport")
	}
	if t.paused.Load() == 1 {
		return fmt.Errorf("accepted frontier: transport is paused")
	}
	if t.packetLossRate > 0 && rand.Float64() < t.packetLossRate {
		t.Logger.Trace("[AcceptedFrontier] simulated packet drop: %s -> %s", t.nodeID, peer)
		return nil
	}
	query.Nonce = append([]byte(nil), query.Nonce...)
	if t.maxLatency > 0 && t.maxLatency > t.minLatency {
		delay := t.minLatency + time.Duration(rand.Int63n(int64(t.maxLatency-t.minLatency)))
		go func() {
			time.Sleep(delay)
			if err := t.sendAcceptedFrontierQuery(peer, query, callback); err != nil {
				t.Logger.Warn("[AcceptedFrontier] delayed send to %s failed: %v", peer, err)
			}
		}()
		return nil
	}
	return t.sendAcceptedFrontierQuery(peer, query, callback)
}

func (t *RealTransport) sendAcceptedFrontierQuery(peer types.NodeID, query AcceptedFrontierQuery, callback func(AcceptedFrontierVote)) error {
	if t.senderManager == nil {
		return fmt.Errorf("accepted frontier: sender manager is not initialized")
	}
	if t.paused.Load() == 1 {
		return fmt.Errorf("accepted frontier: transport is paused")
	}
	t.mu.RLock()
	localID := types.NodeID(t.address)
	localSigner := t.signer
	t.mu.RUnlock()
	if localID == "" {
		localID = t.nodeID
	}
	if query.Requester == "" {
		query.Requester = localID
	}
	if query.Requester != localID {
		return fmt.Errorf("accepted frontier: requester %s does not match local identity %s", query.Requester, localID)
	}
	req, err := NewSignedAcceptedFrontierRequest(query, localSigner)
	if err != nil {
		return err
	}
	targetIP, err := t.getNodeIP(peer)
	if err != nil {
		return fmt.Errorf("resolve accepted frontier peer %s: %w", peer, err)
	}
	return t.senderManager.SendAcceptedFrontier(targetIP, req, func(resp *pb.AcceptedFrontierResponse) error {
		vote, verifyErr := VerifyAcceptedFrontierResponse(query, peer, resp)
		if verifyErr != nil {
			t.Logger.Warn("[AcceptedFrontier] reject response from %s: %v", peer, verifyErr)
			return verifyErr
		}
		if callback != nil {
			callback(vote)
		}
		return nil
	})
}

                                                                              
                                                                
func (t *RealTransport) SignAcceptedFrontierResponse(query AcceptedFrontierQuery, vote AcceptedFrontierVote) (*pb.AcceptedFrontierResponse, error) {
	if t == nil {
		return nil, fmt.Errorf("accepted frontier: nil transport")
	}
	if t.paused.Load() == 1 {
		return nil, fmt.Errorf("accepted frontier: transport is paused")
	}
	t.mu.RLock()
	localID := types.NodeID(t.address)
	localSigner := t.signer
	t.mu.RUnlock()
	if localID == "" {
		localID = t.nodeID
	}
	if vote.Peer == "" {
		vote.Peer = localID
	}
	if vote.Peer != localID {
		return nil, fmt.Errorf("accepted frontier: responder %s does not match local identity %s", vote.Peer, localID)
	}
	return NewSignedAcceptedFrontierResponse(query, vote, localSigner)
}

func (t *RealTransport) FetchStateSnapshotShards(peer types.NodeID, targetHeight uint64) (*types.StateSnapshotShardsResponse, error) {
	if t.senderManager == nil {
		return nil, fmt.Errorf("sender manager is not initialized")
	}
	targetIP, err := t.getNodeIP(peer)
	if err != nil {
		return nil, fmt.Errorf("resolve snapshot peer %s: %w", peer, err)
	}
	return t.senderManager.FetchStateSnapshotShards(targetIP, targetHeight)
}

func (t *RealTransport) FetchStateCheckpointFiles(peer types.NodeID, targetHeight uint64) (string, func(), error) {
	if t.senderManager == nil {
		return "", nil, fmt.Errorf("sender manager is not initialized")
	}
	targetIP, err := t.getNodeIP(peer)
	if err != nil {
		return "", nil, fmt.Errorf("resolve checkpoint peer %s: %w", peer, err)
	}
	return t.senderManager.FetchStateCheckpointFiles(targetIP, targetHeight)
}

func (t *RealTransport) FetchBlockStateRoot(peer types.NodeID, height uint64) ([]byte, error) {
	if t.senderManager == nil {
		return nil, fmt.Errorf("sender manager is not initialized")
	}
	targetIP, err := t.getNodeIP(peer)
	if err != nil {
		return nil, err
	}
	return t.senderManager.FetchBlockStateRoot(targetIP, height)
}

func (t *RealTransport) FetchCertChain(peer types.NodeID, fromHeight, toHeight, interval uint64) ([][]byte, error) {
	if t.senderManager == nil {
		return nil, fmt.Errorf("sender manager is not initialized")
	}
	targetIP, err := t.getNodeIP(peer)
	if err != nil {
		return nil, err
	}
	return t.senderManager.FetchCertChain(targetIP, fromHeight, toHeight, interval)
}

func (t *RealTransport) FetchFinalizedBlock(peer types.NodeID, height uint64) (*pb.Block, error) {
	if t.senderManager == nil {
		return nil, fmt.Errorf("sender manager is not initialized")
	}
	targetIP, err := t.getNodeIP(peer)
	if err != nil {
		return nil, err
	}
	return t.senderManager.FetchFinalizedBlock(targetIP, height)
}

func (t *RealTransport) GetReceiveQueueLen() int {
	return len(t.receiveQueue)
}

                                                      
func (t *RealTransport) GetChannelStats() []stats.ChannelStat {
	return []stats.ChannelStat{
		stats.NewChannelStat("inbox", "Transport", len(t.inbox), cap(t.inbox)),
		stats.NewChannelStat("receiveQueue", "Transport", len(t.receiveQueue), cap(t.receiveQueue)),
	}
}
func (t *RealTransport) EnqueueReceivedMessage(msg types.Message) error {
	                                    
	isControlMessage := false
	switch msg.Type {
	case types.MsgPushQuery, types.MsgPullQuery:
		isControlMessage = true
	case types.MsgChits:
		isControlMessage = true
	}

	if isControlMessage {
		                                 
		select {
		case t.receiveQueue <- msg:
			return nil
		case <-time.After(50 * time.Millisecond):
			t.controlEnqueueTimeoutDrops.Add(1)
			t.Logger.Warn(
				"[RealTransport] Control message queue full local=%s type=%s from=%s receiveQueue=%d/%d",
				t.nodeID, msg.Type, msg.From, len(t.receiveQueue), cap(t.receiveQueue),
			)
			return fmt.Errorf("receive queue full for control message")
		}
	} else {
		                              
		select {
		case t.receiveQueue <- msg:
			return nil
		default:
			t.dataEnqueueFullDrops.Add(1)
			t.Logger.Debug(
				"[RealTransport] Data message queue full local=%s type=%s from=%s receiveQueue=%d/%d",
				t.nodeID, msg.Type, msg.From, len(t.receiveQueue), cap(t.receiveQueue),
			)
			return fmt.Errorf("receive queue full for data message")
		}
	}
}

func (t *RealTransport) Receive() <-chan types.Message {
	return t.inbox
}

                                                                    
                                                                             
                                                                            
                                                                               
                                                                              
func (t *RealTransport) BroadcastQuery(
	msg types.Message,
	peers []types.NodeID,
	onFailure func(types.NodeID, error),
) {
	t.mu.RLock()
	address := t.address
	t.mu.RUnlock()

	var (
		pushBase *pb.PushQuery
		pullBase *pb.PullQuery
		err      error
	)
	switch msg.Type {
	case types.MsgPushQuery:
		pushBase, err = t.adapter.ConsensusMessageToPushQuery(msg, address)
	case types.MsgPullQuery:
		pullBase = &pb.PullQuery{
			RequestId:       msg.RequestID,
			Address:         address,
			BlockId:         msg.BlockID,
			RequestedHeight: msg.Height,
			VrfSeed:         msg.VRFSeed,
			SeqId:           msg.SeqID,
			Nonce:           append([]byte(nil), msg.QueryNonce...),
		}
	default:
		err = fmt.Errorf("BroadcastQuery called with message type %s", msg.Type)
	}
	if err != nil {
		for _, peer := range peers {
			t.reportQuerySendFailure(peer, err, onFailure)
		}
		return
	}

	for _, peer := range peers {
		if t.paused.Load() == 1 {
			continue
		}
		if t.packetLossRate > 0 && rand.Float64() < t.packetLossRate {
			t.Logger.Trace("[RealTransport] Packet dropped: %s -> %s, MsgType=%v", t.nodeID, peer, msg.Type)
			continue
		}

		dispatch := func(target types.NodeID) {
			start := time.Now()
			t.Stats.RecordAPICall(string(msg.Type))
			targetIP, resolveErr := t.getNodeIP(target)
			if resolveErr != nil {
				t.Logger.Debug("[RealTransport] Failed to get IP for node %s: %v", target, resolveErr)
				t.reportQuerySendFailure(target, resolveErr, onFailure)
				return
			}
			failure := func(sendErr error) {
				t.reportQuerySendFailure(target, sendErr, onFailure)
			}
			var sendErr error
			if pushBase != nil {
				sendErr = t.sendPreparedPushQuery(target, targetIP, pushBase, failure)
			} else {
				sendErr = t.sendPreparedPullQuery(target, targetIP, pullBase, failure)
			}
			if sendErr != nil {
				t.Logger.Debug("[RealTransport] Failed to send query to peer %s: %v", target, sendErr)
			}
			if duration := time.Since(start); duration > 50*time.Millisecond {
				t.Logger.Debug("[RealTransport] Send Latency: Type=%v, to=%s, duration=%v", msg.Type, target, duration)
			}
		}

		if t.maxLatency > 0 && t.maxLatency > t.minLatency {
			delay := t.minLatency + time.Duration(rand.Int63n(int64(t.maxLatency-t.minLatency)))
			go func(target types.NodeID) {
				time.Sleep(delay)
				dispatch(target)
			}(peer)
			continue
		}
		dispatch(peer)
	}
}

func (t *RealTransport) reportQuerySendFailure(
	peer types.NodeID,
	err error,
	onFailure func(types.NodeID, error),
) {
	if onFailure == nil || err == nil {
		return
	}
	                                                                     
	                                                                         
	                                                    
	go onFailure(peer, err)
}

func (t *RealTransport) Broadcast(msg types.Message, peers []types.NodeID) {
	if msg.Type == types.MsgPullQuery || msg.Type == types.MsgPushQuery {
		t.BroadcastQuery(msg, peers, nil)
		return
	}
	if msg.Type == types.MsgGossip && len(msg.PreparedGossip) == 0 {
		data, err := t.prepareGossip(msg)
		if err != nil {
			t.Logger.Debug("[RealTransport] Failed to prepare gossip: %v", err)
			return
		}
		msg.PreparedGossip = data
	}
	for _, peer := range peers {
		go func(p types.NodeID) {
			if err := t.Send(p, msg); err != nil {
				t.Logger.Debug("[RealTransport] Failed to send to peer %s: %v", p, err)
			}
		}(peer)
	}
}

func (t *RealTransport) SamplePeers(exclude types.NodeID, count int) []types.NodeID {
	miners, err := t.dbManager.GetRandomMinersFast(count + 1)

	if err != nil {
		t.Logger.Debug("[RealTransport] Failed to get random miners: %v", err)
	}

	peers := make([]types.NodeID, 0, count)
	for _, m := range miners {
		id := types.NodeID(m.Address)
		if id == exclude {
			continue
		}
		peers = append(peers, id)
		if len(peers) >= count {
			break
		}
	}
	                                                                           
	                                                                           
	                                                                             
	if len(peers) == 0 {
		verified, verifyErr := t.dbManager.GetMinerParticipantsSnapshot()
		if verifyErr == nil && len(verified) == 0 {
			peers = append(peers, t.bootstrapPeerIDs(exclude, count)...)
		}
	}
	return peers
}

                                                                                
                                                                                  
                                                           
func (t *RealTransport) SampleGossipPeers(exclude types.NodeID, count int) []types.NodeID {
	selfIP := ""
	if acc, err := t.dbManager.GetAccount(t.address); err == nil && acc != nil {
		selfIP = acc.Ip
	}
	ips, err := t.dbManager.GetRandomGossipIPs(count+1, selfIP)
	if err != nil {
		t.Logger.Error("[RealTransport] SampleGossipPeers failed: %v", err)
		return nil
	}

	peers := make([]types.NodeID, 0, count)
	for _, ip := range ips {
		id := types.NodeID(ip)
		if id == exclude {
			continue
		}
		peers = append(peers, id)
		if len(peers) >= count {
			break
		}
	}
	return peers
}

                                                                                    
func (t *RealTransport) GetAllPeers(exclude types.NodeID) []types.NodeID {
	allMiners, err := t.dbManager.GetMinerParticipantsSnapshot()
	if err != nil {
		t.Logger.Debug("[RealTransport] GetAllPeers failed: %v", err)
		return nil
	}
	if len(allMiners) == 0 {
		return t.bootstrapPeerIDs(exclude, -1)
	}

	peers := make([]types.NodeID, 0, len(allMiners))
	seen := make(map[types.NodeID]struct{}, len(allMiners))
	for _, m := range allMiners {
		if m == nil || m.Address == "" {
			continue
		}
		id := types.NodeID(m.Address)
		if id == exclude {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		peers = append(peers, id)
	}

	sort.Slice(peers, func(i, j int) bool {
		return peers[i] < peers[j]
	})
	return peers
}

func (t *RealTransport) startReceiveWorkers() {
	for i := 0; i < t.receiveWorkers; i++ {
		t.wg.Add(1)
		go t.receiveWorker(i)
	}

	t.Logger.Info("[RealTransport] Started %d receive workers", t.receiveWorkers)
}

func (t *RealTransport) receiveWorker(workerID int) {
	defer t.wg.Done()

	for {
		select {
		case <-t.stopChan:
			return
		case msg := <-t.receiveQueue:
			if err := t.preprocessMessage(&msg); err != nil {
				t.preprocessInvalidDrops.Add(1)
				t.Logger.Debug("[RealTransport] Worker %d: Invalid message from %s: %v",
					workerID, msg.From, err)
				continue
			}

			select {
			case t.inbox <- msg:
				t.Logger.Trace("[RealTransport] Worker %d: Processed message type %d from %s",
					workerID, msg.Type, msg.From)
			case <-time.After(5 * time.Second):
				t.inboxForwardTimeoutDrops.Add(1)
				t.addCounter(&t.inboxTimeoutByType, normalizeCounterKey(string(msg.Type)))
				t.addCounter(&t.inboxTimeoutBySender, normalizeCounterKey(string(msg.From)))
				t.Logger.Warn(
					"[RealTransport] Worker %d: Timeout sending to inbox local=%s type=%s from=%s request=%d block=%s receiveQueue=%d/%d inbox=%d/%d",
					workerID,
					t.nodeID,
					msg.Type,
					msg.From,
					msg.RequestID,
					msg.BlockID,
					len(t.receiveQueue), cap(t.receiveQueue),
					len(t.inbox), cap(t.inbox),
				)
			case <-t.stopChan:
				return
			}
		}
	}
}

func (t *RealTransport) preprocessMessage(msg *types.Message) error {
	if msg == nil {
		return fmt.Errorf("nil message")
	}
	if msg.From == "" {
		return fmt.Errorf("empty sender ID")
	}
	return nil
}

func (t *RealTransport) Close() {
	t.stopOnce.Do(func() {
		close(t.stopChan)
		t.wg.Wait()
		close(t.receiveQueue)
		close(t.inbox)
		t.Logger.Info("[RealTransport] Closed")
	})
}

func (t *RealTransport) GetRuntimeStats() RealTransportRuntimeStats {
	if t == nil {
		return RealTransportRuntimeStats{}
	}
	return RealTransportRuntimeStats{
		ControlEnqueueTimeoutDrops: t.controlEnqueueTimeoutDrops.Load(),
		DataEnqueueFullDrops:       t.dataEnqueueFullDrops.Load(),
		InboxForwardTimeoutDrops:   t.inboxForwardTimeoutDrops.Load(),
		PreprocessInvalidDrops:     t.preprocessInvalidDrops.Load(),
	}
}

func (t *RealTransport) GetTopInboxTimeoutByType(reset bool, topN int) []RealTransportTopDrop {
	if t == nil {
		return nil
	}
	return snapshotCounterMap(&t.inboxTimeoutByType, reset, topN)
}

func (t *RealTransport) GetTopInboxTimeoutBySender(reset bool, topN int) []RealTransportTopDrop {
	if t == nil {
		return nil
	}
	return snapshotCounterMap(&t.inboxTimeoutBySender, reset, topN)
}

func (t *RealTransport) addCounter(counterMap *sync.Map, key string) {
	if key == "" {
		return
	}
	if existing, ok := counterMap.Load(key); ok {
		if counter, ok := existing.(*atomic.Uint64); ok && counter != nil {
			counter.Add(1)
			return
		}
	}

	counter := &atomic.Uint64{}
	actual, _ := counterMap.LoadOrStore(key, counter)
	if typed, ok := actual.(*atomic.Uint64); ok && typed != nil {
		typed.Add(1)
	}
}

func snapshotCounterMap(counterMap *sync.Map, reset bool, topN int) []RealTransportTopDrop {
	if counterMap == nil {
		return nil
	}

	result := make([]RealTransportTopDrop, 0, 8)
	counterMap.Range(func(k, v any) bool {
		key, ok := k.(string)
		if !ok {
			return true
		}
		counter, ok := v.(*atomic.Uint64)
		if !ok || counter == nil {
			return true
		}

		var value uint64
		if reset {
			value = counter.Swap(0)
		} else {
			value = counter.Load()
		}
		if value > 0 {
			result = append(result, RealTransportTopDrop{Key: key, Count: value})
		}
		return true
	})

	if len(result) == 0 {
		return nil
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Count != result[j].Count {
			return result[i].Count > result[j].Count
		}
		return result[i].Key < result[j].Key
	})

	if topN > 0 && len(result) > topN {
		result = result[:topN]
	}
	return result
}

func normalizeCounterKey(key string) string {
	if key == "" {
		return "unknown"
	}
	return key
}
