//go:build !windows

package main

import (
	"fmt"
	"strings"
)

func validatePlatformSecretOptions(opts *runtimeOptions) error {
	if strings.TrimSpace(opts.WindowsLocalSecretDir) != "" || strings.TrimSpace(opts.WindowsArbitrageSecretDir) != "" || opts.AllowWindowsLocalSecretDir {
		return fmt.Errorf("Windows local secret directory options are supported on Windows only")
	}
	return nil
}

func loadPlatformPrivateKey(opts *runtimeOptions) ([]byte, error) {
	if strings.TrimSpace(opts.WindowsLocalSecretDir) != "" || strings.TrimSpace(opts.WindowsArbitrageSecretDir) != "" || opts.AllowWindowsLocalSecretDir {
		return nil, fmt.Errorf("Windows local secret directory options are supported on Windows only")
	}
	if opts.PrivateKeyFD < 3 {
		return nil, fmt.Errorf("--private-key-fd or --keystore is required; inherited descriptors must be >= 3")
	}
	if opts.FrostWrapKeyFD >= 0 && opts.FrostWrapKeyFD < 3 {
		return nil, fmt.Errorf("--frost-wrap-key-fd must be an inherited descriptor >= 3")
	}

	raw, err := readSecretFromFD(opts.PrivateKeyFD, maxPrivateKeyFDBytes, "node-private-key")
	if err != nil {
		return nil, fmt.Errorf("read private key FD: %w", err)
	}
	defer wipeBytes(raw)
	token, err := decodePrivateKeyToken(raw)
	if err != nil {
		return nil, fmt.Errorf("parse private key FD: %w", err)
	}
	return token, nil
}

func loadPlatformArbitragePrivateKey(opts *runtimeOptions) ([]byte, error) {
	if opts.ArbitragePrivateKeyFD == -1 {
		return nil, nil
	}
	if opts.ArbitragePrivateKeyFD < 3 {
		return nil, fmt.Errorf("--arbitrage-private-key-fd must be -1 or an inherited descriptor >= 3")
	}
	raw, err := readSecretFromFD(opts.ArbitragePrivateKeyFD, maxPrivateKeyFDBytes, "arbitrage-private-key")
	if err != nil {
		return nil, fmt.Errorf("read arbitrage private key FD: %w", err)
	}
	defer wipeBytes(raw)
	token, err := decodePrivateKeyToken(raw)
	if err != nil {
		return nil, fmt.Errorf("parse arbitrage private key FD: %w", err)
	}
	return token, nil
}
