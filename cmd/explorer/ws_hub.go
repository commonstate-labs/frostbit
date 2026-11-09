package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"time"

	"dex/utils/peerrep"

	"github.com/cespare/xxhash/v2"
	"github.com/gorilla/websocket"
)

                                                                                                       
                                                                                              
const (
	wsAbuseThreshold               = 15
	wsAbuseBanWindow               = 2 * time.Minute
	wsMarketTradesLimit            = 40
	wsClosedPositionsPageLimit     = 50
	defaultMaxWSClients            = 10000
	defaultMaxWSSubs               = 12
	defaultMaxWSSubscriptionGroups = 2048
	defaultWSWorkers               = 16
	wsMaxMessageBytes              = 2 << 10
	wsMaxParamsBytes               = 1 << 10
	wsMaxAbuseTrackerEntries       = 4096
	wsClientSendQueueDepth         = 8
	wsMaxOutboundMessageBytes      = 1 << 20
	wsOutboundBudgetBytes          = 64 << 20
	wsOutboundBudgetUnitBytes      = 4 << 10
	wsBroadcastPayloadBudgetBytes  = 32 << 20
)

func firstNonEmpty(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

                                                                             
                                                                                                                     
func (h *wsHub) newUpgrader() websocket.Upgrader {
	return websocket.Upgrader{CheckOrigin: h.srv.isOriginAllowed}
}

                                                       
type wsMsg struct {
	Action string          `json:"action"`                   
	Topic  string          `json:"topic"`                                        
	Params json.RawMessage `json:"params,omitempty"`
}

                                                 
type wsPush struct {
	Topic string `json:"topic"`
	Data  any    `json:"data"`
}

type wsClient struct {
	hub        *wsHub
	conn       *websocket.Conn
	remote     string                                                                                  
	mu         sync.Mutex
	subs       map[string]json.RawMessage                    
	send       chan wsOutboundMessage
	sendMu     sync.Mutex
	sendClosed bool
	done       chan struct{}
}

type wsOutboundMessage struct {
	data    []byte
	release func()
}

type wsHub struct {
	srv      *server
	clients  map[*wsClient]struct{}
	mu       sync.RWMutex
	stopped  bool
	clientIP func(*http.Request) string

	maxClients int
	maxSubs    int
	workers    int
	maxGroups  int

	                                                                           
	                                                                         
	                                                              
	subMu   sync.Mutex
	subRefs map[subKey]int

	                                                                             
	                                                                                       
	lastHashMu sync.Mutex
	lastHash   map[*wsClient]map[subKey]uint64

	                                                                                             
	trigger      chan struct{}
	topicTrigger chan string
	stopCh       chan struct{}
	startedCh    chan struct{}
	doneCh       chan struct{}
	stopOnce     sync.Once
	ctx          context.Context
	cancel       context.CancelFunc

	                                                                        
	                                                                           
	                                         
	outboundSlots chan struct{}

	                                                                                                        
	abuse *peerrep.Tracker
}

type subKey struct{ topic, params string }

                                                  
                                                                                                   
                                                                                         
type topicPayload struct {
	data   json.RawMessage
	stable []byte
}

                                                                            
                                                                             
                                                                               
                                                             
func wsPayloadWeight(payload topicPayload) (int64, bool) {
	if len(payload.data) == 0 || len(payload.data) > wsMaxOutboundMessageBytes {
		return 0, false
	}
	if len(payload.stable) > wsMaxOutboundMessageBytes {
		return 0, false
	}
	return int64(len(payload.data) + len(payload.stable)), true
}

func newWSHub(srv *server, maxClients, maxSubs, maxGroups, workers int) *wsHub {
	if maxClients <= 0 {
		maxClients = defaultMaxWSClients
	}
	if maxSubs <= 0 {
		maxSubs = defaultMaxWSSubs
	}
	if workers <= 0 {
		workers = defaultWSWorkers
	}
	if maxGroups <= 0 {
		maxGroups = defaultMaxWSSubscriptionGroups
	}
	ctx, cancel := context.WithCancel(context.Background())
	h := &wsHub{
		srv:           srv,
		clients:       make(map[*wsClient]struct{}),
		maxClients:    maxClients,
		maxSubs:       maxSubs,
		workers:       workers,
		maxGroups:     maxGroups,
		subRefs:       make(map[subKey]int),
		lastHash:      make(map[*wsClient]map[subKey]uint64),
		trigger:       make(chan struct{}, 1),
		topicTrigger:  make(chan string, 1),
		stopCh:        make(chan struct{}),
		startedCh:     make(chan struct{}),
		doneCh:        make(chan struct{}),
		ctx:           ctx,
		cancel:        cancel,
		outboundSlots: make(chan struct{}, wsOutboundBudgetBytes/wsOutboundBudgetUnitBytes),
		abuse:         peerrep.New(wsAbuseThreshold, wsAbuseBanWindow),
	}
	h.abuse.SetOnBan(func(key, reason string, count int) {
		log.Printf("ws client %s banned for %s after %d abuse events: %s", key, wsAbuseBanWindow, count, reason)
		h.disconnectByRemote(key)
	})
	h.abuse.SetMaxEntries(wsMaxAbuseTrackerEntries)
	return h
}

                                                                                          
func (h *wsHub) disconnectByRemote(remote string) {
	h.mu.RLock()
	victims := make([]*wsClient, 0)
	for c := range h.clients {
		if c.remote == remote {
			victims = append(victims, c)
		}
	}
	h.mu.RUnlock()
	for _, c := range victims {
		c.conn.Close()                                       
	}
}

func (h *wsHub) requestClientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	if h != nil && h.clientIP != nil {
		return h.clientIP(r)
	}
	return explorerClientIP(r.RemoteAddr)
}

                                                                                        
func (h *wsHub) Trigger() {
	select {
	case h.trigger <- struct{}{}:
	default:
	}
}

                                                                             
                                                                               
                                          
func (h *wsHub) TriggerTopic(topic string) {
	if topic == "" {
		h.Trigger()
		return
	}
	select {
	case h.topicTrigger <- topic:
	default:
	}
}

                                                                               
                                                                    
func (h *wsHub) Stop() {
	if h == nil {
		return
	}
	h.stopOnce.Do(func() {
		h.mu.Lock()
		h.stopped = true
		clients := make([]*wsClient, 0, len(h.clients))
		for client := range h.clients {
			clients = append(clients, client)
		}
		h.mu.Unlock()
		if h.cancel != nil {
			h.cancel()
		}
		if h.stopCh != nil {
			close(h.stopCh)
		}
		for _, client := range clients {
			_ = client.conn.Close()
		}
	})
	if h.startedCh != nil && h.doneCh != nil {
		select {
		case <-h.startedCh:
			<-h.doneCh
		default:
		}
	}
}

func (h *wsHub) register(c *wsClient) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stopped {
		return false
	}
	maxClients := h.maxClients
	if maxClients <= 0 {
		maxClients = defaultMaxWSClients
	}
	if len(h.clients) >= maxClients {
		return false
	}
	h.clients[c] = struct{}{}
	return true
}

func (h *wsHub) unregister(c *wsClient) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
	h.releaseClientSubscriptions(c)
	                                                                                                  
	                                                                                            
	h.lastHashMu.Lock()
	delete(h.lastHash, c)
	h.lastHashMu.Unlock()
}

                                                                               
                                                                            
                                  
func (h *wsHub) replaceSubscriptionLocked(c *wsClient, topic string, params json.RawMessage) bool {
	maxGroups := h.maxGroups
	if maxGroups <= 0 {
		maxGroups = defaultMaxWSSubscriptionGroups
	}
	newKey := subKey{topic: topic, params: string(params)}
	oldRaw, hadOld := c.subs[topic]
	oldKey := subKey{topic: topic, params: string(oldRaw)}
	if hadOld && oldKey == newKey {
		return true
	}

	h.subMu.Lock()
	defer h.subMu.Unlock()
	if h.subRefs == nil {
		h.subRefs = make(map[subKey]int)
	}
	newRefs := h.subRefs[newKey]
	groupsAfterRelease := len(h.subRefs)
	if hadOld && h.subRefs[oldKey] == 1 {
		groupsAfterRelease--
	}
	if newRefs == 0 && groupsAfterRelease >= maxGroups {
		return false
	}
	if hadOld {
		if refs := h.subRefs[oldKey]; refs <= 1 {
			delete(h.subRefs, oldKey)
		} else {
			h.subRefs[oldKey] = refs - 1
		}
	}
	h.subRefs[newKey]++
	c.subs[topic] = params
	return true
}

                                                                      
func (h *wsHub) removeSubscriptionLocked(c *wsClient, topic string) {
	params, exists := c.subs[topic]
	if !exists {
		return
	}
	key := subKey{topic: topic, params: string(params)}
	h.subMu.Lock()
	if refs := h.subRefs[key]; refs <= 1 {
		delete(h.subRefs, key)
	} else {
		h.subRefs[key] = refs - 1
	}
	h.subMu.Unlock()
	delete(c.subs, topic)
}

func (h *wsHub) releaseClientSubscriptions(c *wsClient) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.subs) == 0 {
		return
	}
	h.subMu.Lock()
	for topic, params := range c.subs {
		key := subKey{topic: topic, params: string(params)}
		if refs := h.subRefs[key]; refs <= 1 {
			delete(h.subRefs, key)
		} else {
			h.subRefs[key] = refs - 1
		}
	}
	h.subMu.Unlock()
	clear(c.subs)
}

                                           
func (h *wsHub) handleWS(w http.ResponseWriter, r *http.Request) {
	if h.isStopped() {
		http.Error(w, "Explorer is shutting down", http.StatusServiceUnavailable)
		return
	}
	                                                                         
	                                                                         
	                                                              
	remote := h.requestClientIP(r)
	if h.abuse.IsBanned(remote) {
		http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
		return
	}
	up := h.newUpgrader()
	conn, err := up.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade: %v", err)
		return
	}
	c := &wsClient{hub: h, conn: conn, remote: remote, subs: make(map[string]json.RawMessage), send: make(chan wsOutboundMessage, wsClientSendQueueDepth), done: make(chan struct{})}
	if !h.register(c) {
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "WebSocket capacity reached"), time.Now().Add(time.Second))
		_ = conn.Close()
		return
	}
	go c.writePump()
	go c.readPump()
}

func (h *wsHub) isStopped() bool {
	if h == nil {
		return true
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.stopped
}

func (c *wsClient) readPump() {
	defer func() {
		c.closeOutbound()
		close(c.done)
		c.hub.unregister(c)
		_ = c.conn.Close()
	}()
	c.conn.SetReadLimit(wsMaxMessageBytes)
	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error { c.conn.SetReadDeadline(time.Now().Add(60 * time.Second)); return nil })
	for {
		_, msg, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		var m wsMsg
		if json.Unmarshal(msg, &m) != nil {
			c.hub.abuse.RecordFailure(c.remote, "invalid ws msg")
			continue
		}
		if m.Action != "sub" && m.Action != "unsub" {
			c.hub.abuse.RecordFailure(c.remote, "unknown action")
			continue
		}
		if !isAllowedWSTopic(m.Topic) {
			c.hub.abuse.RecordFailure(c.remote, "unknown topic")
			continue
		}
		params, err := canonicalWSParams(m.Params)
		if err != nil {
			c.hub.abuse.RecordFailure(c.remote, "invalid topic params")
			continue
		}
		c.mu.Lock()
		switch m.Action {
		case "sub":
			maxSubs := c.hub.maxSubs
			if maxSubs <= 0 {
				maxSubs = defaultMaxWSSubs
			}
			if _, exists := c.subs[m.Topic]; !exists && len(c.subs) >= maxSubs {
				c.mu.Unlock()
				c.hub.abuse.RecordFailure(c.remote, "subscription limit exceeded")
				continue
			}
			if !c.hub.replaceSubscriptionLocked(c, m.Topic, params) {
				c.mu.Unlock()
				c.hub.abuse.RecordFailure(c.remote, "subscription group capacity reached")
				continue
			}
			c.hub.clearTopicHash(c, m.Topic)
			c.hub.Trigger()
		case "unsub":
			c.hub.removeSubscriptionLocked(c, m.Topic)
			c.hub.clearTopicHash(c, m.Topic)
		}
		c.mu.Unlock()
	}
}

func isAllowedWSTopic(topic string) bool {
	switch topic {
	case "summary", "orderbook", "futures", "withdraw_queue", "withdraw_monitor_v2", "witness_flow",
		"witness_list", "witness_my", "recent_txs", "recent_blocks", "dkg_sessions",
		"pending_blocks", "my_orders", "my_future_orders", "account", "my_positions",
		"my_closed_positions", "referral", "user_activity":
		return true
	default:
		return false
	}
}

func canonicalWSParams(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage("{}"), nil
	}
	if len(raw) > wsMaxParamsBytes {
		return nil, fmt.Errorf("params exceed %d bytes", wsMaxParamsBytes)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(canonical), nil
}

func (c *wsClient) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() { ticker.Stop(); c.conn.Close() }()
	for {
		select {
		case <-c.done:
			c.conn.WriteMessage(websocket.CloseMessage, nil)
			return
		case msg := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			err := c.conn.WriteMessage(websocket.TextMessage, msg.data)
			if msg.release != nil {
				msg.release()
			}
			if err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

                                                                                     
                                                                                       
                                                                              
func (h *wsHub) run() {
	close(h.startedCh)
	defer close(h.doneCh)
	fallback := time.NewTicker(30 * time.Second)
	defer fallback.Stop()
	for {
		select {
		case <-h.trigger:
			h.broadcast()
		case topic := <-h.topicTrigger:
			h.broadcastTopic(topic)
		case <-fallback.C:
			h.broadcast()
		case <-h.stopCh:
			return
		}
	}
}

                                                             
                                                                                          
func (h *wsHub) broadcast() {
	h.broadcastTopic("")
}

func (h *wsHub) broadcastTopic(onlyTopic string) {
	hubCtx := h.ctx
	if hubCtx == nil {
		hubCtx = context.Background()
	}
	if hubCtx.Err() != nil {
		return
	}
	h.mu.RLock()
	if len(h.clients) == 0 {
		h.mu.RUnlock()
		return
	}
	                                                                           
	                                                         
	h.subMu.Lock()
	needed := make(map[subKey]struct{}, len(h.subRefs))
	for key := range h.subRefs {
		if onlyTopic == "" || key.topic == onlyTopic {
			needed[key] = struct{}{}
		}
	}
	h.subMu.Unlock()
	clientSubs := make(map[*wsClient]map[string]string)                                  
	for c := range h.clients {
		c.mu.Lock()
		m := make(map[string]string, len(c.subs))
		for t, p := range c.subs {
			if onlyTopic != "" && t != onlyTopic {
				continue
			}
			ps := string(p)
			m[t] = ps
		}
		c.mu.Unlock()
		if len(m) > 0 {
			clientSubs[c] = m
		}
	}
	h.mu.RUnlock()

	if len(needed) == 0 {
		return
	}

	                                                                             
	                                                                   
	cache := make(map[subKey]topicPayload)
	var cacheMu sync.Mutex
	var cacheBytes int64
	var wg sync.WaitGroup
	jobs := make(chan subKey)
	workerCount := h.workers
	if workerCount <= 0 {
		workerCount = defaultWSWorkers
	}
	if workerCount > len(needed) {
		workerCount = len(needed)
	}
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				var (
					k  subKey
					ok bool
				)
				select {
				case <-hubCtx.Done():
					return
				case k, ok = <-jobs:
					if !ok {
						return
					}
				}
				payload := h.fetchTopicData(k.topic, []byte(k.params))
				if weight, fits := wsPayloadWeight(payload); fits {
					cacheMu.Lock()
					if cacheBytes+weight <= wsBroadcastPayloadBudgetBytes {
						cache[k] = payload
						cacheBytes += weight
					}
					cacheMu.Unlock()
				}
			}
		}()
	}
	for k := range needed {
		select {
		case jobs <- k:
		case <-hubCtx.Done():
			close(jobs)
			wg.Wait()
			return
		}
	}
	close(jobs)
	wg.Wait()

	                                                                              
	for c, subs := range clientSubs {
		for topic, ps := range subs {
			k := subKey{topic, ps}
			payload, ok := cache[k]
			if !ok {
				continue
			}
			hashInput := payload.stable
			if hashInput == nil {
				hashInput = payload.data
			}
			hash := xxhash.Sum64(hashInput)
			if !h.isChanged(c, k, hash) {
				continue
			}
			msg, err := json.Marshal(wsPush{Topic: topic, Data: payload.data})
			if err != nil {
				continue
			}
			release, ok := h.reserveOutbound(len(msg))
			if !ok {
				continue
			}
			if !c.enqueueOutbound(wsOutboundMessage{data: msg, release: release}) {
				continue
			}
			                                                                     
			                                                               
			h.markChanged(c, k, hash)
		}
	}
}

                                                                                    
                                                                                                        
func (h *wsHub) clearTopicHash(c *wsClient, topic string) {
	h.lastHashMu.Lock()
	defer h.lastHashMu.Unlock()
	m, ok := h.lastHash[c]
	if !ok {
		return
	}
	for k := range m {
		if k.topic == topic {
			delete(m, k)
		}
	}
}

                                                                                                              
func (h *wsHub) markChanged(c *wsClient, k subKey, hash uint64) bool {
	                                                                         
	                                                                            
	                                      
	h.mu.RLock()
	if _, active := h.clients[c]; !active {
		h.mu.RUnlock()
		return false
	}
	defer h.mu.RUnlock()
	h.lastHashMu.Lock()
	defer h.lastHashMu.Unlock()
	if h.lastHash == nil {
		h.lastHash = make(map[*wsClient]map[subKey]uint64)
	}
	m, ok := h.lastHash[c]
	if !ok {
		m = make(map[subKey]uint64)
		h.lastHash[c] = m
	}
	if prev, ok := m[k]; ok && prev == hash {
		return false
	}
	m[k] = hash
	return true
}

func (h *wsHub) isChanged(c *wsClient, k subKey, hash uint64) bool {
	h.lastHashMu.Lock()
	defer h.lastHashMu.Unlock()
	if previous, ok := h.lastHash[c][k]; ok && previous == hash {
		return false
	}
	return true
}

func (h *wsHub) reserveOutbound(size int) (func(), bool) {
	if size <= 0 || size > wsMaxOutboundMessageBytes {
		return nil, false
	}
	if h.outboundSlots == nil {
		return func() {}, true
	}
	units := (size + wsOutboundBudgetUnitBytes - 1) / wsOutboundBudgetUnitBytes
	acquired := 0
	for acquired < units {
		select {
		case h.outboundSlots <- struct{}{}:
			acquired++
		default:
			for acquired > 0 {
				<-h.outboundSlots
				acquired--
			}
			return nil, false
		}
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			for i := 0; i < units; i++ {
				<-h.outboundSlots
			}
		})
	}, true
}

func (c *wsClient) enqueueOutbound(message wsOutboundMessage) bool {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.sendClosed {
		if message.release != nil {
			message.release()
		}
		return false
	}
	select {
	case c.send <- message:
		return true
	default:
		if message.release != nil {
			message.release()
		}
		return false
	}
}

func (c *wsClient) closeOutbound() {
	c.sendMu.Lock()
	c.sendClosed = true
	for {
		select {
		case message := <-c.send:
			if message.release != nil {
				message.release()
			}
		default:
			c.sendMu.Unlock()
			return
		}
	}
}

                                                                           
func (h *wsHub) fetchTopicData(topic string, params []byte) topicPayload {
	baseCtx := h.ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	timeout := h.srv.timeout
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	ctx, cancel := context.WithTimeout(baseCtx, timeout)
	defer cancel()

	plain := func(raw json.RawMessage) topicPayload { return topicPayload{data: raw} }

	switch topic {
	case "summary":
		return h.fetchSummaryData(ctx, params)
	case "orderbook":
		return plain(h.fetchOrderBookData(ctx, params))
	case "futures":
		return plain(h.fetchFuturesData(ctx, params))
	case "withdraw_queue":
		return plain(h.fetchWithdrawQueueData(ctx, params))
	case "withdraw_monitor_v2":
		return plain(h.fetchWithdrawMonitorInvalidationData(params))
	case "witness_flow":
		return plain(h.invokeHandlerJSON(ctx, h.srv.handleWitnessRequests, "GET", h.buildQueryURL("/api/witness/requests", params, "node"), nil))
	case "witness_list":
		return plain(h.invokeHandlerJSON(ctx, h.srv.handleWitnessList, "GET", h.buildQueryURL("/api/witness/list", params, "node"), nil))
	case "witness_my":
		return plain(h.fetchWitnessMyData(ctx, params))
	case "recent_txs":
		return plain(h.invokeHandlerJSON(ctx, h.srv.handleRecentTxs, "POST", "/api/recenttxs", bytes.NewReader(params)))
	case "recent_blocks":
		return plain(h.invokeHandlerJSON(ctx, h.srv.handleRecentBlocks, "POST", "/api/recentblocks", bytes.NewReader(params)))
	case "dkg_sessions":
		return plain(h.invokeHandlerJSON(ctx, h.srv.handleFrostDKGSessions, "GET", h.buildQueryURL("/api/frost/dkg/list", params, "node"), nil))
	case "pending_blocks":
		return plain(h.invokeHandlerJSON(ctx, h.srv.handlePendingBlocks, "POST", "/api/pendingblocks", bytes.NewReader(params)))
	case "my_orders":
		return plain(h.invokeHandlerJSON(ctx, h.srv.handleMyOrders, "GET", h.buildQueryURL("/api/myorders", params, "node", "address", "pair", "status"), nil))
	case "my_future_orders":
		return plain(h.invokeHandlerJSON(ctx, h.srv.handleFutureMyOrders, "GET", h.buildQueryURL("/api/future/myorders", params, "node", "address", "pair", "status"), nil))
	case "account":
		p := parseSimpleParams(params)
		if p["address"] == "" || (p["node"] != "" && !h.srv.isNodeAllowed(p["node"])) {
			return topicPayload{}
		}
		return plain(h.invokeHandlerJSON(ctx, h.srv.handleAddress, "POST", "/api/address", bytes.NewReader(params)))
	case "my_positions":
		p := parseSimpleParams(params)
		p["node"] = h.srv.pickNode(p["node"])
		if p["node"] == "" || p["address"] == "" {
			return topicPayload{}
		}
		v := url.Values{}
		v.Set("owner", p["address"])
		v.Set("status", firstNonEmpty(p["status"], "OPEN"))
		v.Set("offset", firstNonEmpty(p["offset"], "0"))
		v.Set("limit", firstNonEmpty(p["limit"], "500"))
		return plain(h.proxyGetRaw(ctx, p["node"], "/future/positions?"+boundedExplorerProxyQuery(v).Encode()))
	case "my_closed_positions":
		p := parseSimpleParams(params)
		owner := firstNonEmpty(p["owner"], p["address"])
		if owner == "" {
			raw, _ := json.Marshal(APIError{Error: "owner is required", Code: "INVALID_PARAMS"})
			return plain(withWSRequestID(raw, p["request_id"]))
		}
		v := url.Values{}
		v.Set("owner", owner)
		v.Set("status", firstNonEmpty(p["status"], "closed"))
		v.Set("cursor", p["cursor"])
		v.Set("limit", strconv.Itoa(boundedReadModelInt(p["limit"], 10, 1, wsClosedPositionsPageLimit)))
		raw := h.invokeHandlerJSONIncludingErrors(ctx, h.srv.handleFuturePositionsReadModel, "GET", "/api/future/positions?"+v.Encode(), nil)
		return plain(withWSRequestID(raw, p["request_id"]))
	case "referral":
		return plain(h.fetchReferralData(ctx, params))
	case "user_activity":
		return plain(h.invokeHandlerJSON(ctx, h.srv.handleUserActivity, "GET", h.buildQueryURL("/api/user/activity", params, "node", "address", "kind", "offset", "limit"), nil))
	}
	return topicPayload{}
}

func withWSRequestID(raw json.RawMessage, requestID string) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	encodedID, err := json.Marshal(requestID)
	if err != nil {
		return nil
	}
	payload["request_id"] = encodedID
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return encoded
}

                                                                                   
func (h *wsHub) invokeHandlerJSON(ctx context.Context, handler http.HandlerFunc, method, path string, body io.Reader) json.RawMessage {
	req := httptest.NewRequest(method, path, body).WithContext(ctx)
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code >= 400 {
		return nil
	}
	return json.RawMessage(rec.Body.Bytes())
}

                                                                            
                                                                          
                                                                           
                                                                    
func (h *wsHub) invokeHandlerJSONIncludingErrors(ctx context.Context, handler http.HandlerFunc, method, path string, body io.Reader) json.RawMessage {
	req := httptest.NewRequest(method, path, body).WithContext(ctx)
	rec := httptest.NewRecorder()
	handler(rec, req)
	raw := bytes.TrimSpace(rec.Body.Bytes())
	if len(raw) == 0 || !json.Valid(raw) {
		return nil
	}
	return json.RawMessage(raw)
}

                                                                                       
func parseSimpleParams(params []byte) map[string]string {
	m := make(map[string]interface{})
	json.Unmarshal(params, &m)
	out := make(map[string]string, len(m))
	for k, v := range m {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

                                                    
                                                                                                                                   
func (h *wsHub) buildQueryURL(path string, params []byte, keys ...string) string {
	p := parseSimpleParams(params)
	q := url.Values{}
	for _, k := range keys {
		v, ok := p[k]
		if !ok || v == "" {
			continue
		}
		if k == "node" && !h.srv.isNodeAllowed(v) {
			log.Printf("ws buildQueryURL: node %q not in whitelist, dropping", v)
			continue
		}
		q.Set(k, v)
	}
	if len(q) == 0 {
		return path
	}
	return path + "?" + q.Encode()
}

                  

type summarySubParams struct {
	Nodes        []string `json:"nodes"`
	IncludeBlock bool     `json:"include_block"`
	                                                                     
	IncludeFrost bool `json:"include_frost"`
}

func (h *wsHub) fetchSummaryData(ctx context.Context, params []byte) topicPayload {
	var p summarySubParams
	json.Unmarshal(params, &p)
	nodes := h.srv.selectSummaryNodes(p.Nodes)
	if len(nodes) == 0 {
		nodes = h.srv.selectSummaryNodes(h.srv.defaultNodes)
	}
	start := time.Now()
	results, errs := h.srv.collectSummary(ctx, nodes, p.IncludeBlock)
	                                                      
	                                                                                                        
	stableNodes := make([]nodeSummary, len(results))
	for i, n := range results {
		n.LatencyMs = 0
		stableNodes[i] = n
	}
	stable, _ := json.Marshal(summaryResponse{Nodes: stableNodes, Errors: errs, Selected: nodes})
	resp := summaryResponse{
		GeneratedAt: time.Now().Format(time.RFC3339),
		Nodes:       results,
		Errors:      errs,
		Selected:    nodes,
		ElapsedMs:   time.Since(start).Milliseconds(),
	}
	data, _ := json.Marshal(resp)
	return topicPayload{data: data, stable: stable}
}

                    

type orderbookSubParams struct {
	Node string `json:"node"`
	Pair string `json:"pair"`
}

type orderbookPushData struct {
	Orderbook any `json:"orderbook"`
	Trades    any `json:"trades"`
}

func (h *wsHub) fetchOrderBookData(ctx context.Context, params []byte) json.RawMessage {
	var p orderbookSubParams
	json.Unmarshal(params, &p)
	if p.Node == "" || p.Pair == "" {
		return nil
	}
	ob, err1 := h.srv.fetchOrderBookFromNode(ctx, p.Node, p.Pair)
	trades, err2 := h.srv.fetchTradesFromNode(ctx, p.Node, p.Pair, wsMarketTradesLimit)
	if err1 != nil && err2 != nil {
		return nil
	}
	data, _ := json.Marshal(orderbookPushData{Orderbook: ob, Trades: trades})
	return data
}

                  

type futuresSubParams struct {
	Node string `json:"node"`
	Pair string `json:"pair"`
}

func (h *wsHub) fetchFuturesData(ctx context.Context, params []byte) json.RawMessage {
	var p futuresSubParams
	json.Unmarshal(params, &p)
	if p.Node == "" || p.Pair == "" {
		return nil
	}
	                                           
	type result struct {
		key  string
		data json.RawMessage
	}
	ch := make(chan result, 3)
	paths := []struct{ key, path string }{
		{"market", fmt.Sprintf("/future/market?pair=%s", url.QueryEscape(p.Pair))},
		{"orderbook", fmt.Sprintf("/future/orderbook?pair=%s", url.QueryEscape(p.Pair))},
		{"trades", fmt.Sprintf("/future/trades?pair=%s&limit=%d", url.QueryEscape(p.Pair), wsMarketTradesLimit)},
	}
	for _, pp := range paths {
		go func(key, path string) {
			raw := h.proxyGetRaw(ctx, p.Node, path)
			ch <- result{key, raw}
		}(pp.key, pp.path)
	}
	out := make(map[string]json.RawMessage, 3)
	for i := 0; i < 3; i++ {
		r := <-ch
		if r.data == nil || !json.Valid(r.data) {
			out[r.key] = futuresUpstreamError(r.key)
			continue
		}
		out[r.key] = r.data
	}
	data, _ := json.Marshal(out)
	return data
}

func futuresUpstreamError(source string) json.RawMessage {
	data, _ := json.Marshal(map[string]string{
		"error": fmt.Sprintf("%s upstream request failed", source),
	})
	return data
}

                                              

func (h *wsHub) fetchWithdrawQueueData(ctx context.Context, params []byte) json.RawMessage {
	p := parseSimpleParams(params)
	resp, err := h.srv.fetchMergedFrostWithdrawMonitorStates(
		ctx,
		p["node"],
		p["chain"],
		p["asset"],
		parseExplorerWithdrawMonitorLimit(p["limit"]),
		parseExplorerWithdrawMonitorCompletedLimit(p["completed_limit"]),
	)
	if err != nil {
		return nil
	}
	queue, _ := json.Marshal(buildFrostWithdrawQueueItems(resp.States))
	jobs, _ := json.Marshal(buildFrostWithdrawJobItems(resp.States))
	out := map[string]json.RawMessage{}
	if queue != nil {
		out["queue"] = queue
	}
	if jobs != nil {
		out["jobs"] = jobs
	}
	if len(out) == 0 {
		return nil
	}
	data, _ := json.Marshal(out)
	return data
}

                                                                              
                                                                            
                                                   
func (h *wsHub) fetchWithdrawMonitorInvalidationData(params []byte) json.RawMessage {
	p := parseSimpleParams(params)
	node := h.srv.pickNode(p["node"])
	if node == "" {
		return nil
	}
	data, _ := json.Marshal(map[string]any{
		"invalidated": true,
		"revision":    h.srv.frostWithdrawMonitorRevision(),
	})
	return data
}

                                                

func (h *wsHub) fetchWitnessMyData(ctx context.Context, params []byte) json.RawMessage {
	p := parseSimpleParams(params)
	node := h.srv.pickNode(p["node"])
	                                             
	myReqs := h.invokeHandlerJSON(ctx, h.srv.handleWitnessMyRequests, "GET", fmt.Sprintf("/api/witness/my_requests?node=%s&witness=%s", p["node"], p["address"]), nil)
	list := h.invokeHandlerJSON(ctx, h.srv.handleWitnessList, "GET", h.buildQueryURL("/api/witness/list", params, "node"), nil)
	cfg := h.proxyGetRaw(ctx, node, "/witness/config")
	out := map[string]json.RawMessage{}
	if myReqs != nil {
		out["my_requests"] = myReqs
	}
	if list != nil {
		out["witnesses"] = list
	}
	if cfg != nil {
		out["witness_config"] = cfg
	}
	if hresp, err := h.srv.fetchHeight(ctx, node); err == nil && hresp != nil {
		if hb, err := json.Marshal(hresp.LastAcceptedHeight); err == nil {
			out["current_height"] = hb
		}
	}
	if len(out) == 0 {
		return nil
	}
	data, _ := json.Marshal(out)
	return data
}

                                          

func (h *wsHub) fetchReferralData(ctx context.Context, params []byte) json.RawMessage {
	p := parseSimpleParams(params)
	p["node"] = h.srv.pickNode(p["node"])
	if p["node"] == "" || p["address"] == "" {
		return nil
	}
	query := url.Values{}
	query.Set("address", p["address"])
	info := h.proxyGetRaw(ctx, p["node"], "/referral/info?"+query.Encode())
	addrBody, _ := json.Marshal(map[string]string{"node": p["node"], "address": p["address"]})
	addr := h.invokeHandlerJSON(ctx, h.srv.handleAddress, "POST", "/api/address", bytes.NewReader(addrBody))
	if info == nil && addr == nil {
		return json.RawMessage(`{"error":"account data unavailable"}`)
	}
	out := map[string]json.RawMessage{}
	if info != nil {
		out["info"] = info
	} else {
		out["info_error"] = json.RawMessage("true")
	}
	if addr != nil {
		out["address"] = addr
	} else {
		out["address_error"] = json.RawMessage("true")
	}
	data, _ := json.Marshal(out)
	return data
}

                                                                
                                                                                                                
func (h *wsHub) proxyGetRaw(ctx context.Context, node, path string) json.RawMessage {
	rawNode := node
	node = h.srv.pickNode(node)
	if node == "" {
		log.Printf("ws proxyGetRaw: node %q not in whitelist, blocked", rawNode)
		return nil
	}
	url := fmt.Sprintf("https://%s%s", node, path)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil
	}
	resp, err := h.srv.nodeDo(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil
	}
	body, err := readResponseBody(resp)
	if err != nil {
		return nil
	}
	return json.RawMessage(body)
}
