package main

import (
	"container/list"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

                                                                              
                                                                               
                                                                        
  
                                                                              
                                                                             
                                                                             
type ResourceGuard struct {
	global chan struct{}
	gates  map[explorerWorkClass]chan struct{}

	mu             sync.Mutex
	clients        map[string]*explorerClientBudget
	clientLRU      *list.List
	maxClientState int
	trustedProxies []netip.Prefix
}

type explorerWorkClass uint8

const (
	explorerWorkStandard explorerWorkClass = iota
	explorerWorkHeavy
	explorerWorkAdmin
)

type explorerRouteBudget struct {
	class     explorerWorkClass
	bodyLimit int64
	timeout   time.Duration
	rate      rate.Limit
	burst     int
}

type explorerClientBudget struct {
	limits   map[explorerWorkClass]*rate.Limiter
	lruEntry *list.Element
}

const (
	explorerDefaultBodyLimit = int64(512 << 10)
	explorerWalletBodyLimit  = int64(10 << 20)
	explorerAdminBodyLimit   = int64(64 << 10)
)

func newResourceGuard(maxInflight int) *ResourceGuard {
	if maxInflight <= 0 {
		maxInflight = maxInt(32, runtime.GOMAXPROCS(0)*16)
	}
	heavy := maxInt(2, runtime.GOMAXPROCS(0)/2)
	return &ResourceGuard{
		global: make(chan struct{}, maxInflight),
		gates: map[explorerWorkClass]chan struct{}{
			explorerWorkStandard: make(chan struct{}, maxInflight),
			explorerWorkHeavy:    make(chan struct{}, heavy),
			explorerWorkAdmin:    make(chan struct{}, 1),
		},
		clients:        make(map[string]*explorerClientBudget),
		clientLRU:      list.New(),
		maxClientState: 4096,
		trustedProxies: parseTrustedProxyPrefixes(os.Getenv("EXPLORER_TRUSTED_PROXIES")),
	}
}

func (g *ResourceGuard) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		budget := explorerBudgetForRoute(r.URL.Path)
		                                                                       
		                                                                       
		if r.Method == http.MethodGet && r.URL.Path == "/api/frost/withdraw/result" && r.URL.Query().Get("summary_only") == "1" {
			budget = explorerBudgetForRoute("")
		}
		if !g.allowClient(r, budget) {
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}
		select {
		case g.global <- struct{}{}:
			defer func() { <-g.global }()
		default:
			http.Error(w, "Explorer is busy", http.StatusServiceUnavailable)
			return
		}

		gate := g.gates[budget.class]
		select {
		case gate <- struct{}{}:
			defer func() { <-gate }()
		default:
			http.Error(w, "Explorer is busy", http.StatusServiceUnavailable)
			return
		}

		if budget.bodyLimit > 0 && r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, budget.bodyLimit)
		}
		if budget.timeout > 0 {
			ctx, cancel := context.WithTimeout(r.Context(), budget.timeout)
			defer cancel()
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}

func explorerBudgetForRoute(path string) explorerRouteBudget {
	                                                                             
	                                                                            
	                                                                     
	standard := explorerRouteBudget{
		class:     explorerWorkStandard,
		bodyLimit: explorerDefaultBodyLimit,
		timeout:   10 * time.Second,
		rate:      rate.Limit(120),
		burst:     240,
	}
	heavy := explorerRouteBudget{
		class:     explorerWorkHeavy,
		bodyLimit: explorerDefaultBodyLimit,
		timeout:   8 * time.Second,
		rate:      rate.Limit(12),
		burst:     24,
	}
	switch path {
	case "/api/simulator/auth/challenge", "/api/simulator/auth/verify",
		"/api/simulator/auth/logout", "/api/simulator/config", "/api/node/pause",
		"/api/debug/toggle_miner", "/api/operator/node/diagnostics":
		return explorerAdminBudget()
	}
	switch path {
	case "/api/summary", "/api/node/details", "/api/block", "/api/address",
		"/api/txhistory", "/api/recentblocks", "/api/recenttxs",
		"/api/frost/withdraw/queue", "/api/frost/withdraw/jobs",
		"/api/frost/withdraw/page", "/api/frost/withdraw/detail", "/api/frost/withdraw/result",
		"/api/frost/withdraw/availability", "/api/frost/withdraw/batch", "/api/frost/vault/handover/page",
		"/api/frost/btc/policy", "/api/frost/withdraw/parts", "/api/frost/withdraw/part", "/api/frost/vault/migration/parts",
		"/api/witness/requests", "/api/witness/rewards", "/api/witness/my_requests", "/api/witness/list",
		"/api/frost/dkg/list", "/api/frost/dkg/progress", "/api/frost/vault/list", "/api/frost/vault/activity", "/api/frost/vault/utxos",
		"/api/frost/vault/migration/detail", "/api/frost/vault/migration/download",
		"/api/user/activity", "/api/cert/summary", "/api/referral/rebates",
		"/api/trades", "/api/klines",
		"/api/future/trades", "/api/future/klines", "/api/future/positions",
		"/api/future/orderbook", "/api/future/myorders",
		"/api/tradingpairs", "/api/tokens", "/api/tokenregistry", "/api/orderbook",
		"/api/orderbook/debug", "/api/myorders", "/api/pendingblocks", "/api/ws":
		return heavy
	case "/api/wallet/submittx":
		standard.bodyLimit = explorerWalletBodyLimit
		standard.timeout = 15 * time.Second
		standard.rate = rate.Limit(30)
		standard.burst = 60
		return standard
	default:
		return standard
	}
}

func explorerAdminBudget() explorerRouteBudget {
	return explorerRouteBudget{
		class:     explorerWorkAdmin,
		bodyLimit: explorerAdminBodyLimit,
		timeout:   10 * time.Second,
		rate:      rate.Limit(5),
		burst:     10,
	}
}

func (g *ResourceGuard) WrapAdmin(next http.Handler, token string) http.Handler {
	trimmedToken := strings.TrimSpace(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackRemote(r.RemoteAddr) {
			if trimmedToken == "" || r.Header.Get("X-Explorer-Admin-Token") != trimmedToken {
				http.Error(w, "admin authentication required", http.StatusForbidden)
				return
			}
		} else if trimmedToken != "" && r.Header.Get("X-Explorer-Admin-Token") != trimmedToken {
			http.Error(w, "admin authentication required", http.StatusForbidden)
			return
		}

		budget := explorerAdminBudget()
		if !g.allowClientWithBudget(r, budget) {
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}
		select {
		case g.global <- struct{}{}:
			defer func() { <-g.global }()
		default:
			http.Error(w, "Explorer is busy", http.StatusServiceUnavailable)
			return
		}
		gate := g.gates[budget.class]
		select {
		case gate <- struct{}{}:
			defer func() { <-gate }()
		default:
			http.Error(w, "Admin plane is busy", http.StatusServiceUnavailable)
			return
		}
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, budget.bodyLimit)
		}
		ctx, cancel := context.WithTimeout(r.Context(), budget.timeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (g *ResourceGuard) allowClient(r *http.Request, budget explorerRouteBudget) bool {
	return g.allowClientWithBudget(r, budget)
}

func (g *ResourceGuard) allowClientWithBudget(r *http.Request, budget explorerRouteBudget) bool {
	key := g.clientIP(r)

	g.mu.Lock()
	state := g.clients[key]
	if state == nil {
		maxClientState := g.maxClientState
		if maxClientState <= 0 {
			maxClientState = 1
		}
		if len(g.clients) >= maxClientState {
			g.evictOldestClientLocked()
		}
		state = &explorerClientBudget{limits: make(map[explorerWorkClass]*rate.Limiter)}
		if g.clientLRU == nil {
			g.clientLRU = list.New()
		}
		state.lruEntry = g.clientLRU.PushBack(key)
		g.clients[key] = state
	} else {
		if g.clientLRU == nil {
			g.clientLRU = list.New()
		}
		if state.lruEntry == nil {
			state.lruEntry = g.clientLRU.PushBack(key)
		} else {
			g.clientLRU.MoveToBack(state.lruEntry)
		}
	}
	limiter := state.limits[budget.class]
	if limiter == nil {
		limiter = rate.NewLimiter(budget.rate, budget.burst)
		state.limits[budget.class] = limiter
	}
	g.mu.Unlock()
	return limiter.Allow()
}

const maxForwardedProxyHops = 32

                                                                        
                                                                              
                                                                              
                                   
func (g *ResourceGuard) clientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	remoteText := explorerClientIP(r.RemoteAddr)
	remote, err := netip.ParseAddr(strings.Trim(remoteText, "[]"))
	if err != nil {
		return remoteText
	}
	remote = remote.Unmap()
	if !g.isTrustedProxy(remote) {
		return remote.String()
	}

	values := r.Header.Values("X-Forwarded-For")
	if len(values) == 0 {
		return remote.String()
	}
	hops := strings.Split(strings.Join(values, ","), ",")
	if len(hops) == 0 || len(hops) > maxForwardedProxyHops {
		return remote.String()
	}

	current := remote
	for i := len(hops) - 1; i >= 0; i-- {
		if !g.isTrustedProxy(current) {
			return current.String()
		}
		hop, err := netip.ParseAddr(strings.Trim(strings.TrimSpace(hops[i]), "[]"))
		if err != nil {
			return remote.String()
		}
		current = hop.Unmap()
	}
	return current.String()
}

func (g *ResourceGuard) isTrustedProxy(ip netip.Addr) bool {
	for _, prefix := range g.trustedProxies {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

func parseTrustedProxyPrefixes(raw string) []netip.Prefix {
	const maxTrustedProxyPrefixes = 128
	prefixes := make([]netip.Prefix, 0)
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if len(prefixes) >= maxTrustedProxyPrefixes {
			break
		}
		if prefix, err := netip.ParsePrefix(item); err == nil {
			if prefix.Addr().Is4In6() {
				                                                                  
				                                             
				continue
			}
			prefixes = append(prefixes, prefix.Masked())
			continue
		}
		if ip, err := netip.ParseAddr(strings.Trim(item, "[]")); err == nil {
			ip = ip.Unmap()
			prefixes = append(prefixes, netip.PrefixFrom(ip, ip.BitLen()))
		}
	}
	return prefixes
}

func (g *ResourceGuard) evictOldestClientLocked() {
	if g.clientLRU == nil {
		return
	}
	oldest := g.clientLRU.Front()
	if oldest == nil {
		return
	}
	g.clientLRU.Remove(oldest)
	if key, ok := oldest.Value.(string); ok {
		delete(g.clients, key)
	}
}

func explorerClientIP(remote string) string {
	host, _, err := net.SplitHostPort(remote)
	if err == nil && host != "" {
		return host
	}
	return remote
}

func isLoopbackRemote(remote string) bool {
	host := explorerClientIP(remote)
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isLoopbackListenAddress(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func explorerServerError(err error) error {
	if err == nil || err == http.ErrServerClosed {
		return nil
	}
	return fmt.Errorf("explorer server: %w", err)
}
