package main

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

func normalizeAdvertiseAddr(raw, port string) string {
	raw = strings.TrimSpace(raw)
	port = strings.TrimSpace(port)
	if raw == "" {
		raw = "127.0.0.1"
	}
	if _, _, err := net.SplitHostPort(raw); err == nil {
		return raw
	}
	if port == "" {
		return raw
	}
	return net.JoinHostPort(raw, port)
}

func advertiseAddrFromHost(host string, port int) string {
	return normalizeAdvertiseAddr(host, strconv.Itoa(port))
}

func peerAdvertiseAddr(p PeerInfo) string {
	return normalizeAdvertiseAddr(p.AdvertiseAddr, p.Port)
}

func nodeAdvertiseAddr(node *NodeInstance) string {
	if node == nil {
		return ""
	}
	return normalizeAdvertiseAddr(node.AdvertiseAddr, node.Port)
}

                                                                            
                                                                               
                                                                            
                                                                     
func resolveNodeListenAddr(explicit, advertise, port string) (string, error) {
	explicit = strings.TrimSpace(explicit)
	port = strings.TrimSpace(port)
	if explicit == "" {
		listenAddr := ":" + port
		if localAddr := ipv4UDPAddrFromEndpoint(advertise); localAddr != nil {
			localAddr.Port = mustAtoi(port, 0)
			listenAddr = localAddr.String()
		}
		return listenAddr, nil
	}

	host, listenPort, err := net.SplitHostPort(explicit)
	if err != nil {
		return "", fmt.Errorf("--listen-addr must be numeric IPv4 host:port: %w", err)
	}
	ip := net.ParseIP(strings.TrimSpace(host)).To4()
	if ip == nil {
		return "", fmt.Errorf("--listen-addr host must be numeric IPv4")
	}
	portNumber, err := strconv.Atoi(listenPort)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return "", fmt.Errorf("--listen-addr port must be in 1..65535")
	}
	if listenPort != port {
		return "", fmt.Errorf("--listen-addr port %s must match node port %s", listenPort, port)
	}
	return net.JoinHostPort(ip.String(), listenPort), nil
}

func ipv4UDPAddrFromEndpoint(endpoint string) *net.UDPAddr {
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
	return &net.UDPAddr{IP: ip}
}
