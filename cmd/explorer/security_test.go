package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

                                                         
                                                                                        
                                                                      

func newServerWithAllowed(nodes ...string) *server {
	s := &server{
		defaultNodes: append([]string{}, nodes...),
		allowedNodes: make(map[string]struct{}, len(nodes)),
	}
	for _, n := range nodes {
		s.allowedNodes[n] = struct{}{}
	}
	return s
}

func TestIsNodeAllowed_BlocksUnlisted(t *testing.T) {
	s := newServerWithAllowed("127.0.0.1:9001")
	cases := map[string]bool{
		"127.0.0.1:9001":          true,
		"http://127.0.0.1:9001":   true,                   
		"https://127.0.0.1:9001":  true,                    
		"https://127.0.0.1:9001/": true,                       
		"":                        false,
		"   ":                     false,
		"attacker.example:9001":   false,
		"127.0.0.1:9002":          false,                                     
		"https://attacker/9001":   false,
	}
	for node, want := range cases {
		if got := s.isNodeAllowed(node); got != want {
			t.Errorf("isNodeAllowed(%q) = %v want %v", node, got, want)
		}
	}
}

                                                           
                                           
                                                                
                                                  

func TestIsOriginAllowed_Whitelist(t *testing.T) {
	s := newServerWithAllowed("127.0.0.1:9001")
	s.corsOrigins = []string{"https://a.example", "https://b.example"}

	cases := map[string]bool{
		"https://a.example":       true,
		"https://b.example":       true,
		"https://evil.example":    false,
		"https://a.example.evil":  false,                               
		"":                        true,                                              
	}
	for origin, want := range cases {
		req := httptest.NewRequest(http.MethodGet, "https://host/ws", nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		req.Host = "host"
		if got := s.isOriginAllowed(req); got != want {
			t.Errorf("isOriginAllowed(origin=%q, whitelist) = %v want %v", origin, got, want)
		}
	}
}

func TestIsOriginAllowed_FallbackSameOrigin(t *testing.T) {
	s := newServerWithAllowed("127.0.0.1:9001")                      

	                                 
	req := httptest.NewRequest(http.MethodGet, "https://explorer.local/ws", nil)
	req.Host = "explorer.local"
	req.Header.Set("Origin", "https://explorer.local")
	if !s.isOriginAllowed(req) {
		t.Error("same-origin should be allowed")
	}

	                      
	req2 := httptest.NewRequest(http.MethodGet, "https://explorer.local/ws", nil)
	req2.Host = "explorer.local"
	req2.Header.Set("Origin", "http://localhost:8080")
	if !s.isOriginAllowed(req2) {
		t.Error("localhost origin should be allowed in dev fallback")
	}

	                         
	req3 := httptest.NewRequest(http.MethodGet, "https://explorer.local/ws", nil)
	req3.Host = "explorer.local"
	req3.Header.Set("Origin", "https://evil.example")
	if s.isOriginAllowed(req3) {
		t.Error("random cross-origin should be rejected in dev fallback")
	}

	                
	req4 := httptest.NewRequest(http.MethodGet, "https://explorer.local/ws", nil)
	req4.Host = "explorer.local"
	req4.Header.Set("Origin", "::::not-a-url")
	if s.isOriginAllowed(req4) {
		t.Error("malformed origin should be rejected")
	}
}
