package handlers

import (
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"strings"

	"dex/config"
)

                                                                            
                                                                            
                                                                           
                        
func requireExplorerServiceToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !explorerServiceAuthorized(r, false) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "explorer service authentication required"})
			return
		}
		next(w, r)
	}
}

func explorerServiceAuthorized(r *http.Request, allowLoopbackWithoutToken bool) bool {
	if r == nil || !strings.EqualFold(strings.TrimSpace(r.Header.Get(config.ExplorerServiceHeader)), "explorer") {
		return false
	}
	want := config.ExplorerServiceToken()
	got := strings.TrimSpace(r.Header.Get(config.ExplorerServiceTokenHeader))
	if want != "" {
		return len(got) == len(want) && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
	}
	if !allowLoopbackWithoutToken {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
