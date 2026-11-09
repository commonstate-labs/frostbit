                                                                       
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"time"

	"dex/config"
	"dex/frost/guardian"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: frost-guardian keygen --out <private-key-file> | serve --keys <file> --frost-config <file> --node-url <own-validating-node>")
	}
	switch args[0] {
	case "keygen":
		fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
		out := fs.String("out", "", "new private key file; never overwritten")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *out == "" {
			return fmt.Errorf("--out is required")
		}
		btc, account, err := guardian.GenerateKeyFile(*out)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"guardian": config.GuardianConfig{BTCPublicKey: btc, AccountPublicKey: account}})
	case "serve":
		fs := flag.NewFlagSet("serve", flag.ContinueOnError)
		keyPath := fs.String("keys", "", "private guardian key file")
		cfgPath := fs.String("frost-config", "", "active frost_default.json containing pinned public keys")
		nodeURL := fs.String("node-url", "", "guardian owner's independently validating DEX node")
		nodeCA := fs.String("node-ca-file", "", "trusted validating node PEM certificate/CA file")
		nodeServerName := fs.String("node-server-name", "", "TLS name matching the validating node certificate")
		listen := fs.String("listen", "127.0.0.1:9445", "guardian listen address")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *keyPath == "" || *cfgPath == "" || *nodeURL == "" {
			return fmt.Errorf("--keys, --frost-config and --node-url are required")
		}
		resolver, err := validatingNodeClient(*nodeURL, *nodeCA, *nodeServerName)
		if err != nil {
			return err
		}
		cfg, err := config.LoadFrostConfig(*cfgPath)
		if err != nil {
			return err
		}
		btc, err := cfg.GuardianPublicKey("btc")
		if err != nil {
			return err
		}
		account, err := cfg.GuardianPublicKey("eth")
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(*keyPath)
		if err != nil {
			return err
		}
		var keys guardian.KeyFile
		if err = json.Unmarshal(raw, &keys); err != nil {
			return err
		}
		signer, err := guardian.NewSigner(keys, btc, account, resolver)
		if err != nil {
			return err
		}
		server := &http.Server{Addr: *listen, Handler: signer, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 45 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		go func() {
			<-ctx.Done()
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = server.Shutdown(shutdown)
		}()
		fmt.Fprintln(os.Stderr, "guardian signer listening on", *listen)
		err = server.ListenAndServe()
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func validatingNodeClient(endpoint, caFile, serverName string) (*guardian.HTTPClient, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("invalid validating node URL")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	loopback := host == "localhost" || ip != nil && ip.IsLoopback()
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return nil, fmt.Errorf("validating node must use HTTPS, or HTTP on loopback")
	}
	client := guardian.NewHTTPClient(endpoint)
	if caFile != "" {
		if u.Scheme != "https" {
			return nil, fmt.Errorf("node CA requires HTTPS")
		}
		tlsConfig, err := config.NodeTLSConfig(caFile, serverName)
		if err != nil {
			return nil, err
		}
		client.Client.Transport = &http.Transport{TLSClientConfig: tlsConfig}
	} else if serverName != "" {
		return nil, fmt.Errorf("node server name requires a CA file")
	}
	return client, nil
}
