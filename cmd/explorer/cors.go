package main

import (
	"net/http"
	"net/url"
	"strings"
)

const explorerCORSMethods = "GET, POST, OPTIONS"

                                                                              
                                                                             
                                                                            
                                                                           
func (s *server) withPublicCORS(guard *ResourceGuard, next http.Handler) http.Handler {
	allowedOrigins := make(map[string]struct{}, len(s.corsOrigins))
	for _, origin := range s.corsOrigins {
		allowedOrigins[origin] = struct{}{}
	}
	allowed := func(r *http.Request, origin string) bool {
		u, err := url.Parse(origin)
		if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
			return false
		}
		if u.Scheme == "chrome-extension" || u.Scheme == "moz-extension" {
			                                                                 
			                                                                     
			return u.Port() == "" && isExplorerWalletCORSPath(r.URL.Path)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return false
		}
		if len(allowedOrigins) > 0 {
			_, ok := allowedOrigins[origin]
			return ok
		}
		return s.isOriginAllowed(r)
	}
	guarded := guard.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions && r.Header.Get("Origin") != "" {
			addExplorerCORSVary(w.Header(), "Access-Control-Request-Method", "Access-Control-Request-Headers")
			method := r.Header.Get("Access-Control-Request-Method")
			if method != "" && method != http.MethodGet && method != http.MethodPost && method != http.MethodOptions {
				http.Error(w, "CORS method not allowed", http.StatusForbidden)
				return
			}
			for _, header := range strings.Split(r.Header.Get("Access-Control-Request-Headers"), ",") {
				header = strings.TrimSpace(header)
				if header != "" && !strings.EqualFold(header, "Content-Type") && !strings.EqualFold(header, simulatorCSRFHeader) {
					http.Error(w, "CORS header not allowed", http.StatusForbidden)
					return
				}
			}
			w.Header().Set("Access-Control-Allow-Methods", explorerCORSMethods)
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, "+simulatorCSRFHeader)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	}))
	denied := guard.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "CORS origin not allowed", http.StatusForbidden)
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		addExplorerCORSVary(w.Header(), "Origin")
		origin := r.Header.Get("Origin")
		if origin != "" {
			if !allowed(r, origin) {
				denied.ServeHTTP(w, r)
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		guarded.ServeHTTP(w, r)
	})
}

func isExplorerWalletCORSPath(path string) bool {
	switch path {
	case "/api/wallet/submittx", "/api/wallet/nonce", "/api/wallet/receipt", "/api/wallet/deposit-address":
		return true
	case "/api/nodes", "/api/tokens", "/api/address", "/api/token-icon":
		return true
	default:
		return false
	}
}

func addExplorerCORSVary(header http.Header, names ...string) {
	for _, name := range names {
		found := false
		for _, value := range header.Values("Vary") {
			for _, existing := range strings.Split(value, ",") {
				if existing = strings.TrimSpace(existing); existing == "*" || strings.EqualFold(existing, name) {
					found = true
					break
				}
			}
		}
		if !found {
			header.Add("Vary", name)
		}
	}
}
