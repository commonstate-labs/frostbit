package main

import (
	"context"
	"dex/config"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

                                                                                                                               
type nodeEventMsg struct {
	Type      string `json:"type"`
	Height    uint64 `json:"height,omitempty"`
	BlockID   string `json:"block_id,omitempty"`
	Miner     string `json:"miner,omitempty"`
	TxCount   int    `json:"tx_count,omitempty"`
	Timestamp int64  `json:"ts,omitempty"`
	Count     int    `json:"count,omitempty"`
}

                                                                              
                                                                                    
type nodeEventsClient struct {
	nodes     []string
	signal    chan struct{}                                                                                  
	events    chan nodeEventMsg
	dialer    *websocket.Dialer
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	seenMu    sync.Mutex
	seen      map[string]time.Time                                                          
	seenTTL   time.Duration
	lastClean time.Time

	connsMu  sync.Mutex
	conns    map[*websocket.Conn]struct{}
	stopOnce sync.Once
}

const (
	nodeEventMaxMessageBytes = 8 << 10
	nodeEventMaxSeen         = 4096
)

func newNodeEventsClient(nodes []string) *nodeEventsClient {
	ctx, cancel := context.WithCancel(context.Background())
	return &nodeEventsClient{
		nodes:  nodes,
		signal: make(chan struct{}, 1),
		events: make(chan nodeEventMsg, 128),
		dialer: &websocket.Dialer{
			HandshakeTimeout: 10 * time.Second,
			TLSClientConfig:  config.ExplorerNodeTLSConfig(),
		},
		ctx:     ctx,
		cancel:  cancel,
		seen:    make(map[string]time.Time),
		seenTTL: 30 * time.Second,
		conns:   make(map[*websocket.Conn]struct{}),
	}
}

                                                
func (c *nodeEventsClient) Start() {
	for _, n := range c.nodes {
		c.wg.Add(1)
		go c.runNode(n)
	}
}

func (c *nodeEventsClient) Stop() {
	c.stopOnce.Do(func() {
		c.cancel()
		c.connsMu.Lock()
		connections := make([]*websocket.Conn, 0, len(c.conns))
		for conn := range c.conns {
			connections = append(connections, conn)
		}
		c.connsMu.Unlock()
		for _, conn := range connections {
			_ = conn.Close()
		}
		c.wg.Wait()
		close(c.signal)
		close(c.events)
	})
}

                                                                                           
                                                                            
func (c *nodeEventsClient) Signal() <-chan struct{} {
	return c.signal
}

func (c *nodeEventsClient) Events() <-chan nodeEventMsg {
	return c.events
}

func (c *nodeEventsClient) runNode(node string) {
	defer c.wg.Done()
	url := "wss://" + node + "/events/stream"
	backoff := time.Second
	for {
		if c.ctx.Err() != nil {
			return
		}
		if err := c.runOnce(url); err != nil && c.ctx.Err() == nil {
			log.Printf("[events] %s disconnected: %v, reconnect in %v", node, err, backoff)
		}
		select {
		case <-c.ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (c *nodeEventsClient) runOnce(url string) error {
	header := make(http.Header)
	header.Set(config.ExplorerServiceHeader, "explorer")
	if token := config.ExplorerServiceToken(); token != "" {
		header.Set(config.ExplorerServiceTokenHeader, token)
	}
	conn, _, err := c.dialer.DialContext(c.ctx, url, header)
	if err != nil {
		return err
	}
	c.trackConn(conn)
	defer func() {
		c.untrackConn(conn)
		_ = conn.Close()
	}()
	if err := c.ctx.Err(); err != nil {
		return err
	}

	                                                                                
	c.notify()

	conn.SetReadLimit(nodeEventMaxMessageBytes)
	conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	conn.SetPingHandler(func(appData string) error {
		conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(10*time.Second))
	})
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		return nil
	})

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		var msg nodeEventMsg
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		if c.shouldIgnore(msg) {
			continue
		}
		c.publish(msg)
	}
}

                                                                                        
func (c *nodeEventsClient) shouldIgnore(msg nodeEventMsg) bool {
	if msg.BlockID == "" {
		return false                                                            
	}
	c.seenMu.Lock()
	defer c.seenMu.Unlock()
	now := time.Now()
	if now.Sub(c.lastClean) > c.seenTTL {
		for k, t := range c.seen {
			if now.Sub(t) > c.seenTTL {
				delete(c.seen, k)
			}
		}
		c.lastClean = now
	}
	if _, ok := c.seen[msg.BlockID]; ok {
		return true
	}
	if len(c.seen) >= nodeEventMaxSeen {
		var oldestKey string
		var oldest time.Time
		for key, seenAt := range c.seen {
			if oldestKey == "" || seenAt.Before(oldest) {
				oldestKey = key
				oldest = seenAt
			}
		}
		if oldestKey != "" {
			delete(c.seen, oldestKey)
		}
	}
	c.seen[msg.BlockID] = now
	return false
}

func (c *nodeEventsClient) trackConn(conn *websocket.Conn) {
	c.connsMu.Lock()
	c.conns[conn] = struct{}{}
	c.connsMu.Unlock()
}

func (c *nodeEventsClient) untrackConn(conn *websocket.Conn) {
	c.connsMu.Lock()
	delete(c.conns, conn)
	c.connsMu.Unlock()
}

                                                                                              
func (c *nodeEventsClient) notify() {
	select {
	case c.signal <- struct{}{}:
	default:
	}
}

func (c *nodeEventsClient) publish(msg nodeEventMsg) {
	c.notify()
	select {
	case c.events <- msg:
	default:
	}
}
