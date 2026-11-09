package main

import (
	"net"
	"net/http"
)

                                                                           
                                                                                
                          
func wrapPprofHandler(next http.Handler, maxInflight int) http.Handler {
	if maxInflight < 1 {
		maxInflight = 1
	}
	slots := make(chan struct{}, maxInflight)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackRemote(r.RemoteAddr) {
			http.Error(w, "pprof is loopback-only", http.StatusForbidden)
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			http.Error(w, "pprof busy", http.StatusServiceUnavailable)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopbackRemote(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
