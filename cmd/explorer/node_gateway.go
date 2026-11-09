package main

import (
	"context"
	"dex/config"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

const (
	                                                                            
	                                                                  
	explorerMaxNodeResponseBytes      = int64(32 << 20)
	explorerMaxDecodedResponseBytes   = int64(32 << 20)
	explorerDefaultNodeConcurrency    = 48
	explorerDefaultPerNodeConcurrency = 12
)

var (
	errExplorerNodeNotAllowed = errors.New("node is not allowed")
	errExplorerResponseTooBig = errors.New("node response exceeds explorer response budget")
)

                                                                              
                                                                               
                                                                              
                
type nodeGateway struct {
	client *http.Client

	defaultNodes []string
	nodes        map[string]string                                                          
	allowed      map[string]struct{}

	global  chan struct{}
	perNode map[string]chan struct{}

	maxResponseBytes int64
	serviceToken     string
}

func newNodeGateway(client *http.Client, nodes []string, maxConcurrent int) *nodeGateway {
	if maxConcurrent <= 0 {
		maxConcurrent = explorerDefaultNodeConcurrency
	}
	canonical := make([]string, 0, len(nodes))
	known := make(map[string]string, len(nodes)*2)
	allowed := make(map[string]struct{}, len(nodes))
	perNode := make(map[string]chan struct{}, len(nodes))
	for _, raw := range nodes {
		node := normalizeExplorerNode(raw)
		if node == "" {
			continue
		}
		if _, duplicate := allowed[node]; duplicate {
			continue
		}
		allowed[node] = struct{}{}
		canonical = append(canonical, node)
		known[node] = node
		known[fmt.Sprintf("node-%d", len(canonical)-1)] = node
		perNode[node] = make(chan struct{}, explorerDefaultPerNodeConcurrency)
	}
	if client == nil {
		client = &http.Client{}
	}
	                                                                          
	                                                                          
	                                                                      
	baseClient := *client
	gateway := &nodeGateway{
		client:           &baseClient,
		defaultNodes:     canonical,
		nodes:            known,
		allowed:          allowed,
		global:           make(chan struct{}, maxConcurrent),
		perNode:          perNode,
		maxResponseBytes: explorerMaxNodeResponseBytes,
		serviceToken:     config.ExplorerServiceToken(),
	}
	client.Transport = nodeGatewayTransport{gateway: gateway}
	return gateway
}

type nodeGatewayTransport struct{ gateway *nodeGateway }

func (t nodeGatewayTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.gateway == nil {
		return nil, errors.New("node gateway transport is not initialized")
	}
	return t.gateway.Do(req)
}

func (g *nodeGateway) Resolve(raw string) (string, bool) {
	if g == nil {
		return normalizeExplorerNode(raw), raw != ""
	}
	value := strings.TrimSpace(raw)
	if value == "" {
		if len(g.defaultNodes) == 0 {
			return "", false
		}
		return g.defaultNodes[0], true
	}
	if node, ok := g.nodes[value]; ok {
		return node, true
	}
	node := normalizeExplorerNode(value)
	if node == "" {
		return "", false
	}
	_, ok := g.allowed[node]
	return node, ok
}

func (g *nodeGateway) IsAllowed(raw string) bool {
	_, ok := g.Resolve(raw)
	return ok
}

func (g *nodeGateway) Do(req *http.Request) (*http.Response, error) {
	if g == nil || g.client == nil {
		return nil, errors.New("node gateway is not initialized")
	}
	if req == nil || req.URL == nil {
		return nil, errors.New("invalid node request")
	}
	if !strings.EqualFold(req.URL.Scheme, "https") {
		return nil, errExplorerNodeNotAllowed
	}
	node := normalizeExplorerNode(req.URL.Host)
	if node == "" {
		return nil, errExplorerNodeNotAllowed
	}
	if _, ok := g.allowed[node]; !ok {
		return nil, errExplorerNodeNotAllowed
	}

	if err := acquireGatewaySlot(req.Context(), g.global); err != nil {
		return nil, err
	}
	perNode := g.perNode[node]
	if perNode == nil {
		<-g.global
		return nil, errExplorerNodeNotAllowed
	}
	if err := acquireGatewaySlot(req.Context(), perNode); err != nil {
		<-g.global
		return nil, err
	}

	                                                                             
	                                                                           
	                                                              
	req.Header.Set(config.ExplorerServiceHeader, "explorer")
	if g.serviceToken != "" {
		req.Header.Set(config.ExplorerServiceTokenHeader, g.serviceToken)
	}
	resp, err := g.client.Do(req)
	if err != nil {
		<-perNode
		<-g.global
		return nil, err
	}
	if resp.Body == nil {
		<-perNode
		<-g.global
		return resp, nil
	}
	if resp.ContentLength > g.maxResponseBytes {
		_ = resp.Body.Close()
		<-perNode
		<-g.global
		return nil, errExplorerResponseTooBig
	}
	resp.Body = &gatewayReadCloser{
		ReadCloser: resp.Body,
		remaining:  g.maxResponseBytes,
		release: func() {
			<-perNode
			<-g.global
		},
	}
	return resp, nil
}

func acquireGatewaySlot(ctx context.Context, gate chan struct{}) error {
	select {
	case gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type gatewayReadCloser struct {
	io.ReadCloser
	remaining int64
	release   func()
	once      sync.Once
}

func (r *gatewayReadCloser) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, errExplorerResponseTooBig
	}
	limit := r.remaining + 1
	if int64(len(p)) > limit {
		p = p[:int(limit)]
	}
	n, err := r.ReadCloser.Read(p)
	r.remaining -= int64(n)
	if r.remaining < 0 {
		return n, errExplorerResponseTooBig
	}
	return n, err
}

func (r *gatewayReadCloser) Close() error {
	err := r.ReadCloser.Close()
	r.once.Do(r.release)
	return err
}

func normalizeExplorerNode(raw string) string {
	value := strings.TrimSpace(raw)
	value = strings.TrimSuffix(value, "/")
	if value == "" {
		return ""
	}
	if strings.Contains(value, "://") {
		u, err := url.Parse(value)
		if err != nil || (!strings.EqualFold(u.Scheme, "https") && !strings.EqualFold(u.Scheme, "http")) || u.Host == "" || u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" {
			return ""
		}
		value = u.Host
	}
	if strings.ContainsAny(value, "/?#@") {
		return ""
	}
	if _, _, err := net.SplitHostPort(value); err != nil {
		return ""
	}
	return value
}

func (s *server) resolveNode(raw string) (string, bool) {
	if s != nil && s.gateway != nil {
		return s.gateway.Resolve(raw)
	}
	value := normalizeExplorerNode(raw)
	if value == "" && s != nil && len(s.defaultNodes) > 0 && strings.TrimSpace(raw) == "" {
		value = normalizeExplorerNode(s.defaultNodes[0])
	}
	if value == "" || s == nil {
		return "", false
	}
	if len(s.allowedNodes) == 0 {
		return value, true
	}
	_, ok := s.allowedNodes[value]
	return value, ok
}

func (s *server) nodeDo(req *http.Request) (*http.Response, error) {
	if s != nil && s.gateway != nil {
		return s.gateway.Do(req)
	}
	if s == nil || s.client == nil {
		return nil, errors.New("node client is not initialized")
	}
	return s.client.Do(req)
}

func writeBoundedNodeResponse(w http.ResponseWriter, resp *http.Response) {
	if resp == nil {
		http.Error(w, "node returned no response", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	body, err := readResponseBody(resp)
	if err != nil {
		http.Error(w, "node response exceeds Explorer budget", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}
