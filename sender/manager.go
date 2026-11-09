package sender

import (
	"dex/config"
	"dex/db"
	"dex/logs"
	"dex/pb"
	"dex/txpool"
	"dex/types"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"
)

                                                     
type SenderManager struct {
	dbManager  *db.Manager
	txPool     *txpool.TxPool
	address    string                       
	SendQueue  *SendQueue                         
	httpClient *http.Client
	cfg        *config.Config
	nodeID     int                                      
	Logger     logs.Logger
	txGossip   *txGossipBatcher
	                                                                        
	                                                                  
	servicePeer *servicePeerRuntime
	                                                                           
	                                                        
	archiveServeInit     sync.Once
	archiveServeSem      chan struct{}
	archiveOrdinarySem   chan struct{}
	archiveProofSem      chan struct{}
	liveBlockServeSem    chan struct{}
	archiveServeMu       sync.Mutex
	archiveServeBySource map[string]archiveServeLane
	                                                                         
	                                                                            
	                                                              
	finalizedLookupMu sync.RWMutex
	finalizedLookup   FinalizedBlockLookup

	heightQueryPending sync.Map

	discoveryStartOnce  sync.Once
	discoveryStopOnce   sync.Once
	discoveryStopCh     chan struct{}
	discoveryWg         sync.WaitGroup
	discoveryLastHeight atomic.Uint64
	discoveryRound      atomic.Uint64

	                                                                                        
	                                                                                           
	                                                                                                             
	                                                                                                                         
	minerIPMu    sync.RWMutex
	minerIPCache map[string]string
}

                                                                           
                                                                                   
                                                               
type FinalizedBlockLookup interface {
	GetFinalizedBlockID(height uint64) (string, bool)
}

                             
type pullBlockByIDMessage struct {
	requestData []byte
	blockID     string
	onSuccess   func(*pb.Block)
}
type pullTxMessage struct {
	requestData []byte
	onSuccess   func(*pb.AnyTx)
	txPool      *txpool.TxPool
}

                              
func NewSenderManager(
	dbMgr *db.Manager,
	address string,
	localBindEndpoint string,
	pool *txpool.TxPool,
	nodeID int,
	logger logs.Logger,
	cfg *config.Config,
) *SenderManager {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}

	                          
	httpClient := createHttp3Client(cfg, address, localBindEndpoint)
	                                            
	queue := NewSendQueue(
		cfg.Sender.WorkerCount,
		cfg.Sender.QueueCapacity,
		httpClient,
		nodeID,
		address,
		logger,
		cfg,
	)

	sm := &SenderManager{
		dbManager:       dbMgr,
		txPool:          pool,
		address:         address,
		SendQueue:       queue,
		httpClient:      httpClient,
		cfg:             cfg,
		Logger:          logger,
		discoveryStopCh: make(chan struct{}),
		minerIPCache:    make(map[string]string),
	}
	sm.txGossip = newTxGossipBatcher(sm)
	return sm
}

                                                                            
                                                                         
                                      
func (sm *SenderManager) SetFinalizedBlockLookup(lookup FinalizedBlockLookup) {
	if sm == nil {
		return
	}
	sm.finalizedLookupMu.Lock()
	sm.finalizedLookup = lookup
	sm.finalizedLookupMu.Unlock()
}

func (sm *SenderManager) finalizedBlockID(height uint64) (string, bool) {
	if sm == nil {
		return "", false
	}
	sm.finalizedLookupMu.RLock()
	lookup := sm.finalizedLookup
	sm.finalizedLookupMu.RUnlock()
	if lookup == nil {
		return "", false
	}
	return lookup.GetFinalizedBlockID(height)
}

                                                                                         
                               
func (sm *SenderManager) SetMinerIP(address, ip string) {
	if sm == nil || address == "" {
		return
	}
	sm.minerIPMu.Lock()
	defer sm.minerIPMu.Unlock()
	if ip == "" {
		delete(sm.minerIPCache, address)
		return
	}
	sm.minerIPCache[address] = ip
}

                                                                                                     
func (sm *SenderManager) LookupMinerIP(address string) string {
	if sm == nil || address == "" {
		return ""
	}
	sm.minerIPMu.RLock()
	ip, ok := sm.minerIPCache[address]
	sm.minerIPMu.RUnlock()
	if ok && ip != "" {
		return ip
	}
	if sm.dbManager == nil {
		return ""
	}
	acc, err := sm.dbManager.GetAccount(address)
	if err != nil || acc == nil {
		return ""
	}
	return acc.Ip
}

func (sm *SenderManager) controlMaxRetries(defaultValue int) int {
	if sm == nil || sm.cfg == nil {
		return defaultValue
	}
	return sm.cfg.Sender.ControlMaxRetries
}

func (sm *SenderManager) controlTaskTimeout() time.Duration {
	if sm == nil || sm.cfg == nil || sm.cfg.Sender.ControlTaskTimeout <= 0 {
		return 1500 * time.Millisecond
	}
	return sm.cfg.Sender.ControlTaskTimeout
}

                                                                                                    
const liveBlockTaskTimeout = 5 * time.Second

                           
func (sm *SenderManager) BroadcastTx(tx *pb.AnyTx) {
	if sm == nil || tx == nil {
		return
	}
	if shouldBroadcastTxOnControlPlane(tx) {
		sm.broadcastControlPlaneTx(tx)
		return
	}
	sm.enqueueTxGossip(tx, nil)
}

func shouldBroadcastTxOnControlPlane(tx *pb.AnyTx) bool {
	if tx == nil {
		return false
	}
	switch tx.GetContent().(type) {
	case *pb.AnyTx_CertSignTx:
		return true
	default:
		return false
	}
}

const controlPlaneTxPeerLimit = 1024

func (sm *SenderManager) broadcastControlPlaneTx(tx *pb.AnyTx) {
	if sm == nil || tx == nil {
		return
	}
	ips, err := sm.getRandomGossipIPs(controlPlaneTxPeerLimit)
	if err != nil {
		logs.Error("broadcastControlPlaneTx: failed to get gossip peers: %v", err)
		return
	}
	data, err := proto.Marshal(tx)
	if err != nil {
		sm.Logger.Verbose("broadcastControlPlaneTx: proto marshal failed: %v", err)
		return
	}
	for _, ip := range ips {
		if ip == "" {
			continue
		}
		sm.SendQueue.Enqueue(&SendTask{
			Target:      ip,
			Message:     data,
			RetryCount:  0,
			MaxRetries:  sm.controlMaxRetries(2),
			SendFunc:    doSendTx,
			Priority:    PriorityControl,
			NodeAddress: sm.address,
			Timeout:     sm.controlTaskTimeout(),
		})
	}
}

func (sm *SenderManager) enqueueTxGossip(tx *pb.AnyTx, ips []string) {
	if sm == nil || tx == nil {
		return
	}
	if sm.txGossip != nil && sm.txGossip.Enqueue(tx, ips) {
		return
	}
	if len(ips) == 0 {
		var err error
		ips, err = sm.getRandomGossipIPs(sm.txGossipFanout())
		if err != nil {
			logs.Error("enqueueTxGossip: failed to get gossip peers: %v", err)
			return
		}
	}
	data, err := proto.Marshal(tx)
	if err != nil {
		sm.Logger.Verbose("enqueueTxGossip: proto marshal failed: %v", err)
		return
	}
	for _, ip := range ips {
		sm.SendQueue.Enqueue(&SendTask{
			Target:      ip,
			Message:     data,
			RetryCount:  0,
			MaxRetries:  0,
			SendFunc:    doSendTx,
			Priority:    PriorityData,
			NodeAddress: sm.address,
		})
	}
}

                                               
func (sm *SenderManager) SendTxToAllPeers(tx *pb.AnyTx) {
	if sm == nil || tx == nil {
		return
	}
	ips, err := sm.getRandomGossipIPs(20)
	if err != nil {
		logs.Error("SendTxToAllPeers: failed to get gossip peers: %v", err)
		return
	}
	sm.enqueueTxGossip(tx, ips)
}

                     
func (sm *SenderManager) PullTx(peerAddr, txID string, onSuccess func(*pb.AnyTx)) {
	getDataMsg := &pb.GetData{TxId: txID}
	data, err := proto.Marshal(getDataMsg)
	if err != nil {
		logs.Debug("[PullTx] marshal failed: %v", err)
		return
	}

	ip, err := sm.AddressToIP(peerAddr)
	if err != nil {
		logs.Debug("[PullTx] get address failed: %v", err)
		return
	}

	msg := &pullTxMessage{
		requestData: data,
		onSuccess:   onSuccess,
		txPool:      sm.txPool,
	}

	task := &SendTask{
		Target:      ip,
		Message:     msg,
		RetryCount:  0,
		MaxRetries:  3,
		SendFunc:    doSendGetDataWithManager,
		Priority:    PriorityData,                            
		NodeAddress: sm.address,
	}
	sm.SendQueue.Enqueue(task)
}

                                  
func (sm *SenderManager) PullGet(targetIP string, blockID string, onSuccess func(*pb.Block)) {
	                            
	req := &pb.GetBlockByIDRequest{BlockId: blockID}

	data, err := proto.Marshal(req)
	if err != nil {
		logs.Debug("[PullGet] marshal failed: %v", err)
		return
	}

	                                     
	msg := &pullBlockByIDMessage{
		requestData: data,
		blockID:     blockID,
		onSuccess:   onSuccess,
	}

	task := &SendTask{
		Target:      targetIP,
		Message:     msg,
		RetryCount:  0,
		MaxRetries:  0,                                                                    
		SendFunc:    doSendGetBlockByID,
		Priority:    PriorityImmediate,
		NodeAddress: sm.address,
		Timeout:     liveBlockTaskTimeout,
	}
	sm.SendQueue.Enqueue(task)
}

                              
func (sm *SenderManager) PullBlock(targetAddress string, height uint64, onSuccess func(*pb.Block)) {
	ip := sm.LookupMinerIP(targetAddress)
	if ip == "" {
		logs.Debug("[PullBlock] IP is empty for address %s", targetAddress)
		return
	}

	req := &pb.GetBlockRequest{Height: height}
	data, err := proto.Marshal(req)
	if err != nil {
		logs.Debug("[PullBlock] marshal GetBlockRequest error: %v", err)
		return
	}

	msg := &pullBlockMessage{
		requestData: data,
		onSuccess:   onSuccess,
	}

	task := &SendTask{
		Target:      ip,
		Message:     msg,
		RetryCount:  0,
		MaxRetries:  1,
		SendFunc:    doSendGetBlock,
		Priority:    PriorityData,                            
		NodeAddress: sm.address,
	}
	sm.SendQueue.Enqueue(task)
}

                     
func (sm *SenderManager) BatchGetTxs(peerAddress string, shortHashes map[string]bool, onSuccess func([]*pb.AnyTx)) {
	var result [][]byte
	for hexStr := range shortHashes {
		bytes, err := hex.DecodeString(hexStr)
		if err != nil {
			continue
		}
		result = append(result, bytes)
	}

	reqMsg := &pb.BatchGetShortTxRequest{
		ShortHashes: result,
	}
	data, err := proto.Marshal(reqMsg)
	if err != nil {
		logs.Warn("failed to marshal BatchGetDataRequest: %v", err)
		return
	}

	ip, err := sm.AddressToIP(peerAddress)
	if err != nil {
		logs.Warn("failed to get IP for peerAddress %s: %v", peerAddress, err)
		return
	}

	msg := &pullBatchTxMessage{
		requestData: data,
		onSuccess:   onSuccess,
	}

	task := &SendTask{
		Target:      ip,
		Message:     msg,
		RetryCount:  0,
		MaxRetries:  1,
		SendFunc:    doSendBatchGetTxs,
		Priority:    PriorityData,                                  
		NodeAddress: sm.address,
	}
	sm.SendQueue.Enqueue(task)
}

                              
func (sm *SenderManager) BroadcastGossipToTarget(targetAddr string, payload *types.GossipPayload) error {
	data, err := types.EncodeGossipPayload(payload)
	if err != nil {
		return fmt.Errorf("failed to encode gossip payload: %w", err)
	}
	return sm.BroadcastGossipBytesToTarget(targetAddr, data)
}

                                                                           
                                                                                
                                                          
func (sm *SenderManager) BroadcastGossipBytesToTarget(targetAddr string, data []byte) error {
	if targetAddr == "" {
		return nil
	}
	task := &SendTask{
		Target:      targetAddr,
		Message:     data,
		RetryCount:  0,
		MaxRetries:  0,
		SendFunc:    doSendToOnePeer,
		Priority:    PriorityData,                             
		NodeAddress: sm.address,
	}
	if !sm.SendQueue.Enqueue(task) {
		return fmt.Errorf("gossip to %s was not admitted to send queue", targetAddr)
	}
	return nil
}

                                     
func (sm *SenderManager) PushQuery(peerAddr string, pq *pb.PushQuery) {
	_ = sm.PushQueryWithFailure(peerAddr, pq, nil)
}

                                                                              
                                                                               
                                                        
func (sm *SenderManager) PushQueryWithFailure(peerAddr string, pq *pb.PushQuery, onFailure func(error)) error {
	                                         
	pq.Address = sm.address                               
	data, err := proto.Marshal(pq)
	if err != nil {
		logs.Debug("[PushQuery] marshal fail: %v", err)
		if onFailure != nil {
			onFailure(err)
		}
		return err
	}

	ip, err := sm.AddressToIP(peerAddr)
	if err != nil || ip == "" {
		logs.Debug("[PushQuery] resolve ip err: %v", err)
		if err == nil {
			err = fmt.Errorf("empty resolved peer address")
		}
		if onFailure != nil {
			onFailure(err)
		}
		return err
	}

	msg := &pushQueryMsg{requestData: data}
	task := &SendTask{
		Target:         ip,
		Message:        msg,
		MaxRetries:     0,
		SendFunc:       doSendPushQuery,
		TraceKind:      "push",
		TraceRequestID: pq.RequestId,
		TraceHeight:    pq.RequestedHeight,
		Priority:       PriorityImmediate,                                                       
		NodeAddress:    sm.address,
		Timeout:        max(sm.controlTaskTimeout(), liveBlockTaskTimeout),
		OnFailure:      onFailure,
	}
	if !sm.SendQueue.Enqueue(task) {
		return fmt.Errorf("PushQuery to %s was not admitted to send queue", peerAddr)
	}
	return nil
}

                                               
func (sm *SenderManager) PullQuery(peerAddr string, pq *pb.PullQuery) {
	_ = sm.PullQueryWithFailure(peerAddr, pq, nil)
}

func (sm *SenderManager) PullQueryWithFailure(peerAddr string, pq *pb.PullQuery, onFailure func(error)) error {
	data, err := proto.Marshal(pq)
	if err != nil {
		logs.Debug("[PullQuery] marshal fail: %v", err)
		if onFailure != nil {
			onFailure(err)
		}
		return err
	}

	ip, err := sm.AddressToIP(peerAddr)
	if err != nil || ip == "" {
		logs.Debug("[PullQuery] resolve ip err: %v", err)
		if err == nil {
			err = fmt.Errorf("empty resolved peer address")
		}
		if onFailure != nil {
			onFailure(err)
		}
		return err
	}

	msg := &pullQueryMsg{requestData: data}
	task := &SendTask{
		Target:         ip,
		Message:        msg,
		MaxRetries:     0,
		SendFunc:       doSendPullQuery,
		TraceKind:      "pull",
		TraceRequestID: pq.RequestId,
		TraceHeight:    pq.RequestedHeight,
		Priority:       PriorityControl,                      
		NodeAddress:    sm.address,
		Timeout:        sm.controlTaskTimeout(),
		OnFailure:      onFailure,
	}
	if !sm.SendQueue.Enqueue(task) {
		return fmt.Errorf("PullQuery to %s was not admitted to send queue", peerAddr)
	}
	return nil
}

               

                                           
func (sm *SenderManager) getRandomMiners(count int) ([]*pb.Account, error) {
	                                                   
	return sm.dbManager.GetRandomMinersFast(count)
}

                                                                    
func (sm *SenderManager) getRandomGossipIPs(count int) ([]string, error) {
	selfIP := ""
	if acc, err := sm.dbManager.GetAccount(sm.address); err == nil && acc != nil {
		selfIP = acc.Ip
	}
	return sm.dbManager.GetRandomGossipIPs(count, selfIP)
}

                                                                                                     
                                                                                            
func (sm *SenderManager) AddressToIP(peerAddr string) (string, error) {
	                                                             
	if host, port, err := net.SplitHostPort(peerAddr); err == nil && host != "" && port != "" {
		return peerAddr, nil
	}

	                                                               
	if parsedIP := net.ParseIP(peerAddr); parsedIP != nil {
		return peerAddr, nil
	}

	                                                          
	if index, err := strconv.ParseUint(peerAddr, 10, 64); err == nil && len(peerAddr) < 5 {
		account, err := sm.dbManager.GetMinerByIndex(index)
		if err == nil && account != nil {
			if ip := sm.LookupMinerIP(account.Address); ip != "" {
				return ip, nil
			}
		}
	}

	                                                                        
	if ip := sm.LookupMinerIP(peerAddr); ip != "" {
		return ip, nil
	}
	return "", fmt.Errorf("no endpoint configured for address %s", peerAddr)
}

                                          
func doSendGetDataWithManager(t *SendTask, client *http.Client) error {
	pm, ok := t.Message.(*pullTxMessage)
	if !ok {
		return fmt.Errorf("doSendGetData: t.Message is not *pullTxMessage")
	}

	respBytes, err := postProtobufAndRead(t, client, "doSendGetData", "/getdata", pm.requestData)
	if err != nil {
		return err
	}

	var anyTx pb.AnyTx
	if err := proto.Unmarshal(respBytes, &anyTx); err != nil {
		return fmt.Errorf("doSendGetData: unmarshal AnyTx fail: %v", err)
	}

	                              
	if pm.txPool != nil {
		txId := anyTx.GetTxId()
		if txId != "" {
			if err := pm.txPool.CacheAnyTx(&anyTx); err != nil {
				logs.Debug("[doSendGetData] storeAnyTx fail: %v", err)
			}
		}
	}

	if pm.onSuccess != nil {
		pm.onSuccess(&anyTx)
	}

	return nil
}

                                             
func (sm *SenderManager) SendChits(targetAddress string, chits *pb.Chits) error {
	chits.Address = sm.address                         
	data, err := proto.Marshal(chits)
	if err != nil {
		logs.Debug("[SendChits] marshal failed: %v", err)
		return err
	}

	ip, err := sm.AddressToIP(targetAddress)
	if err != nil {
		logs.Debug("[SendChits] failed to get IP for address %s: %v", targetAddress, err)
		return err
	}

	msg := &chitsMessage{
		requestData: data,
	}

	task := &SendTask{
		Target:         ip,
		Message:        msg,
		RetryCount:     0,
		MaxRetries:     0,
		SendFunc:       doSendChits,
		TraceKind:      "chit",
		TraceRequestID: chits.RequestId,
		TraceHeight:    chits.PreferredBlockAtHeight,
		Priority:       PriorityControl,                                                      
		NodeAddress:    sm.address,
		Timeout:        sm.controlTaskTimeout(),
	}
	sm.SendQueue.Enqueue(task)
	return nil
}

                           
func (sm *SenderManager) SendBlock(targetAddress string, block *pb.Block) error {
	data, err := proto.Marshal(block)
	if err != nil {
		logs.Debug("[SendBlock] marshal failed: %v", err)
		return err
	}

	ip, err := sm.AddressToIP(targetAddress)
	if err != nil {
		logs.Debug("[SendBlock] failed to get IP for address %s: %v", targetAddress, err)
		return err
	}

	msg := &blockMessage{
		requestData: data,
	}

	task := &SendTask{
		Target:      ip,
		Message:     msg,
		RetryCount:  0,
		MaxRetries:  2,
		SendFunc:    doSendBlock,
		Priority:    PriorityControl,
		NodeAddress: sm.address,
	}
	sm.SendQueue.Enqueue(task)
	return nil
}

                                           
func (sm *SenderManager) SendHeightQuery(targetAddress string, onSuccess func(*pb.HeightResponse)) error {
	ip, err := sm.AddressToIP(targetAddress)
	if err != nil {
		logs.Debug("[SendHeightQuery] failed to get IP for address %s: %v", targetAddress, err)
		return err
	}
	if _, loaded := sm.heightQueryPending.LoadOrStore(ip, struct{}{}); loaded {
		return nil
	}
	done := func() { sm.heightQueryPending.Delete(ip) }

	                                 
	req := &pb.StatusRequest{}                                                           
	data, err := proto.Marshal(req)
	if err != nil {
		done()
		logs.Debug("[SendHeightQuery] marshal failed: %v", err)
		return err
	}

	msg := &heightQueryMessage{
		requestData: data,
		onSuccess:   onSuccess,
	}

	task := &SendTask{
		Target:      ip,
		Message:     msg,
		RetryCount:  0,
		MaxRetries:  sm.controlMaxRetries(2),
		SendFunc:    doSendHeightQuery,
		Priority:    PriorityControl,
		NodeAddress: sm.address,
		Timeout:     sm.controlTaskTimeout(),
		OnDone:      done,
	}
	sm.SendQueue.Enqueue(task)
	return nil
}

                                                                              
func (sm *SenderManager) SendAcceptedFrontier(targetAddress string, req *pb.AcceptedFrontierRequest, onSuccess func(*pb.AcceptedFrontierResponse) error) error {
	if req == nil {
		return fmt.Errorf("accepted frontier request is nil")
	}
	data, err := proto.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal accepted frontier request: %w", err)
	}
	ip, err := sm.AddressToIP(targetAddress)
	if err != nil {
		return fmt.Errorf("resolve accepted frontier peer %s: %w", targetAddress, err)
	}
	msg := &acceptedFrontierMessage{requestData: data, onSuccess: onSuccess}
	task := &SendTask{
		Target:      ip,
		Message:     msg,
		RetryCount:  0,
		MaxRetries:  sm.controlMaxRetries(0),
		SendFunc:    doSendAcceptedFrontier,
		Priority:    PriorityControl,
		NodeAddress: sm.address,
		Timeout:     sm.controlTaskTimeout(),
	}
	if !sm.SendQueue.Enqueue(task) {
		return fmt.Errorf("accepted frontier request to %s was not admitted to send queue", targetAddress)
	}
	return nil
}

                                                                                 
func (sm *SenderManager) SendSyncBlocksRequest(
	targetAddress string,
	fromHeight,
	toHeight uint64,
	onSuccess func(*types.SyncBlocksResponse),
) error {
	ip, err := sm.AddressToIP(targetAddress)
	if err != nil {
		logs.Debug("[SendSyncBlocksRequest] failed to get IP for address %s: %v", targetAddress, err)
		return err
	}

	msg := &syncRequestMessage{
		fromHeight:           fromHeight,
		toHeight:             toHeight,
		onSuccessWithBundles: onSuccess,
	}

	task := &SendTask{
		Target:      ip,
		Message:     msg,
		RetryCount:  0,
		MaxRetries:  0,
		SendFunc:    doSendSyncRequest,
		Priority:    PriorityControl,
		NodeAddress: sm.address,
	}
	if !sm.SendQueue.Enqueue(task) {
		return fmt.Errorf("sync block request to %s was not admitted to send queue", targetAddress)
	}
	return nil
}

                                                
func (sm *SenderManager) Stop() {
	if sm.txGossip != nil {
		sm.txGossip.Stop()
	}
	if sm.servicePeer != nil && sm.servicePeer.stopCancel != nil {
		sm.servicePeer.stopCancel()
	}
	sm.discoveryStopOnce.Do(func() {
		if sm.discoveryStopCh != nil {
			close(sm.discoveryStopCh)
		}
	})
	sm.discoveryWg.Wait()
	if sm.servicePeer != nil {
		sm.servicePeer.probeWg.Wait()
	}
	if sm.SendQueue != nil {
		sm.SendQueue.Stop()
	}
}
