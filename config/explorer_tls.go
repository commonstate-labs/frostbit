package config

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"
)

                                                                           
                                                                            
                                                                             
func ExplorerNodeTLSConfig() *tls.Config {
	cfg := &tls.Config{MinVersion: tls.VersionTLS13, InsecureSkipVerify: true}
	path := strings.TrimSpace(os.Getenv("DEX_EXPLORER_NODE_CA_FILE"))
	if path == "" {
		return cfg
	}
	verified, err := NodeTLSConfig(path, os.Getenv("DEX_EXPLORER_NODE_SERVER_NAME"))
	if err != nil {
		                                                                              
		cfg.VerifyConnection = func(tls.ConnectionState) error { return err }
		return cfg
	}
	return verified
}

func NodeTLSConfig(caFile, serverName string) (*tls.Config, error) {
	info, err := os.Stat(caFile)
	if err != nil {
		return nil, fmt.Errorf("node TLS trust file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, fmt.Errorf("node TLS trust file must be a regular file under 1 MiB")
	}
	raw, err := os.ReadFile(caFile)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(raw) {
		return nil, fmt.Errorf("node TLS trust file contains no certificates")
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: pool, ServerName: strings.TrimSpace(serverName)}, nil
}
