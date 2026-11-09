package sender

import (
	"context"
	"crypto/tls"
	"dex/config"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

type headerRoundTripper struct {
	rt      http.RoundTripper
	address string
}

func (h *headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	if clone.Header == nil {
		clone.Header = make(http.Header)
	}
	clone.Header.Set("X-Node-Address", h.address)
	clone.Header.Set("Accept-Encoding", "gzip")
	return h.rt.RoundTrip(clone)
}

func createHttp3Client(cfg *config.Config, selfAddress, localEndpoint string) *http.Client {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}

	tlsCfg := &tls.Config{
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS13,
		MaxVersion:         tls.VersionTLS13,
		ClientSessionCache: tls.NewLRUClientSessionCache(128),
		NextProtos:         []string{"h3", "h3-29", "h3-28", "h3-27"},
	}
	quicCfg := &quic.Config{
		KeepAlivePeriod: 10 * time.Second,
		MaxIdleTimeout:  5 * time.Minute,
		Allow0RTT:       false,
	}
	var transportMu sync.Mutex
	var qt *quic.Transport
	localUDPAddr := localIPv4UDPAddr(localEndpoint)
	getTransport := func() (*quic.Transport, error) {
		transportMu.Lock()
		defer transportMu.Unlock()
		if qt != nil {
			return qt, nil
		}
		udpConn, err := net.ListenUDP("udp4", localUDPAddr)
		if err != nil {
			return nil, err
		}
		qt = &quic.Transport{Conn: udpConn}
		return qt, nil
	}

	tr := &http3.Transport{
		DisableCompression: true,
		TLSClientConfig:    tlsCfg,
		QUICConfig:         quicCfg,
		Dial: func(ctx context.Context, addr string, tlsCfg *tls.Config, cfg *quic.Config) (*quic.Conn, error) {
			qt, err := getTransport()
			if err != nil {
				return nil, err
			}
			udpAddr, err := net.ResolveUDPAddr("udp4", addr)
			if err != nil {
				return nil, err
			}
			return qt.DialEarly(ctx, udpAddr, tlsCfg, cfg)
		},
	}

	return &http.Client{
		Transport: &headerRoundTripper{rt: tr, address: selfAddress},
		Timeout:   cfg.Network.ConnectionTimeout,
		                                                                          
		                                                                         
		                                                     
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func localIPv4UDPAddr(endpoint string) *net.UDPAddr {
	host := strings.TrimSpace(endpoint)
	if host == "" {
		return nil
	}
	if splitHost, _, err := net.SplitHostPort(host); err == nil {
		host = splitHost
	}
	host = strings.Trim(host, "[]")
	ip := net.ParseIP(host).To4()
	if ip == nil || ip.Equal(net.IPv4zero) {
		return nil
	}
	return &net.UDPAddr{IP: ip, Port: 0}
}
