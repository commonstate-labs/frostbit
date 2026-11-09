package main

import (
	"dex/interfaces"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"dex/types"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

                                                                        
                                                                                                                          
type eventsStreamMsg struct {
	Type      string `json:"type"`
	Height    uint64 `json:"height,omitempty"`
	BlockID   string `json:"block_id,omitempty"`
	Miner     string `json:"miner,omitempty"`
	TxCount   int    `json:"tx_count,omitempty"`
	Timestamp int64  `json:"ts,omitempty"`
	Count     int    `json:"count,omitempty"`
}

type eventsClient struct {
	conn      *websocket.Conn
	send      chan []byte
	closeOnce sync.Once
}

func (c *eventsClient) close() {
	if c == nil {
		return
	}
	c.closeOnce.Do(func() {
		close(c.send)
		_ = c.conn.Close()
	})
}

                                                                                                  
type eventsHub struct {
	mu       sync.RWMutex
	client   *eventsClient
	closed   bool
	upgrader websocket.Upgrader
	once     sync.Once
}

func newEventsHub() *eventsHub {
	return &eventsHub{
		upgrader: websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }},
	}
}

                                                                               
                                                               
func (h *eventsHub) attach(bus interfaces.EventBus) {
	if bus == nil {
		return
	}
	h.once.Do(func() {
		bus.Subscribe(types.EventBlockFinalized, h.onBlockFinalized)
		bus.Subscribe(types.EventSyncComplete, h.onSyncComplete)
	})
}

func (h *eventsHub) onBlockFinalized(ev interfaces.Event) {
	msg := eventsStreamMsg{Type: string(types.EventBlockFinalized), Timestamp: time.Now().UnixMilli()}
	switch d := ev.Data().(type) {
	case *types.Block:
		if d != nil {
			msg.Height = d.Header.Height
			msg.BlockID = d.ID
			msg.Miner = keys.MinerAddress(d.Header.Miner)
			msg.TxCount = len(d.Transactions)
		}
	case *pb.Block:
		if d != nil && d.Header != nil {
			msg.Height = d.Header.Height
			msg.BlockID = d.BlockHash
			msg.Miner = keys.MinerAddress(d.Header.Miner)
			msg.TxCount = len(d.Body)
		}
	}
	h.broadcast(msg)
}

func (h *eventsHub) onSyncComplete(ev interfaces.Event) {
	msg := eventsStreamMsg{Type: string(types.EventSyncComplete), Timestamp: time.Now().UnixMilli()}
	if n, ok := ev.Data().(int); ok {
		msg.Count = n
	}
	h.broadcast(msg)
}

func (h *eventsHub) broadcast(msg eventsStreamMsg) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	h.mu.RLock()
	c := h.client
	if c != nil {
		select {
		case c.send <- data:
		default:                                                       
		}
	}
	h.mu.RUnlock()
}

                                                                     
                                                                              
                                                                     
func (h *eventsHub) replaceClient(c *eventsClient) bool {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		c.close()
		return false
	}
	old := h.client
	h.client = c
	h.mu.Unlock()
	if old != nil && old != c {
		old.close()
	}
	return true
}

func (h *eventsHub) unregister(c *eventsClient) {
	h.mu.Lock()
	if h.client == c {
		h.client = nil
	}
	h.mu.Unlock()
	c.close()
}

                                                                    
func (h *eventsHub) handleWS(w http.ResponseWriter, r *http.Request) {
	                                                                     
	                                                                             
	requireExplorerServiceToken(h.handleAuthenticatedWS)(w, r)
}

func (h *eventsHub) handleAuthenticatedWS(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := &eventsClient{conn: conn, send: make(chan []byte, 128)}
	if !h.replaceClient(c) {
		return
	}
	go h.writePump(c)
	go h.readPump(c)
}

func (h *eventsHub) readPump(c *eventsClient) {
	defer func() {
		h.unregister(c)
		c.conn.Close()
	}()
	c.conn.SetReadLimit(1024)
	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			return
		}
	}
}

func (h *eventsHub) writePump(c *eventsClient) {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, nil)
				return
			}
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
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

                                                                         
func (h *eventsHub) closeAll() {
	h.mu.Lock()
	h.closed = true
	c := h.client
	h.client = nil
	h.mu.Unlock()
	if c != nil {
		c.close()
	}
	logs.Info("[EventsStream] all subscribers closed")
}
