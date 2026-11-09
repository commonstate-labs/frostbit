package sender

import (
	"errors"
	"net"
	"net/http"
	"testing"
)

func TestP2PHTTPClientRejectsRedirects(t *testing.T) {
	client := createHttp3Client(nil, "", "")
	if client.CheckRedirect == nil {
		t.Fatal("P2P HTTP client has no redirect policy")
	}
	request, err := http.NewRequest(http.MethodGet, "https://127.0.0.1/internal", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.CheckRedirect(request, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("redirect policy error = %v, want http.ErrUseLastResponse", err)
	}
}

func TestLocalIPv4UDPAddrUsesOnlyARealLocalBindAddress(t *testing.T) {
	if got := localIPv4UDPAddr("0.0.0.0:6000"); got != nil {
		t.Fatalf("wildcard local bind = %v, want OS-selected address", got)
	}
	got := localIPv4UDPAddr("192.168.1.10:6000")
	if got == nil || !got.IP.Equal(net.ParseIP("192.168.1.10")) || got.Port != 0 {
		t.Fatalf("numeric local bind = %v, want 192.168.1.10:0", got)
	}
}
