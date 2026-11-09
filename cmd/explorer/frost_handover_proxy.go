package main

import "net/http"

                                                                          
                                                                           
func (s *server) handleFrostReadonlyV2Proxy(path string) http.HandlerFunc {
	proxy := s.handleFutureProxy(path)
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeJSONStatus(w, http.StatusMethodNotAllowed, APIError{Error: "method not allowed"})
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		proxy(w, r)
	}
}
