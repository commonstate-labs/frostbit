package main

import (
	"strings"
	"testing"
)

func emptySecretOptions() runtimeOptions {
	return runtimeOptions{PrivateKeyFD: -1, KeystorePasswordFD: -1, ArbitragePrivateKeyFD: -1, FrostWrapKeyFD: -1}
}
func TestRuntimeKeystoreRejectsConflictingAndInvalidSources(t *testing.T) {
	cases := []struct {
		name string
		set  func(*runtimeOptions)
		want string
	}{
		{"invalid raw FD", func(o *runtimeOptions) { o.PrivateKeyFD = 0 }, "descriptor >= 3"},
		{"negative raw FD", func(o *runtimeOptions) { o.PrivateKeyFD = -2 }, "descriptor >= 3"},
		{"raw FD", func(o *runtimeOptions) { o.KeystorePath = "miner.json"; o.PrivateKeyFD = 3 }, "conflicts"},
		{"raw debug directory", func(o *runtimeOptions) { o.KeystorePath = "miner.json"; o.WindowsLocalSecretDir = ".local/key" }, "conflicts"},
		{"password FD without file", func(o *runtimeOptions) { o.KeystorePasswordFD = 3 }, "requires --keystore"},
		{"stdin password", func(o *runtimeOptions) { o.KeystorePath = "miner.json"; o.KeystorePasswordFD = 0 }, "descriptor >= 3"},
		{"duplicate password and wrap", func(o *runtimeOptions) { o.KeystorePath = "miner.json"; o.KeystorePasswordFD = 3; o.FrostWrapKeyFD = 3 }, "different inherited descriptors"},
		{"stdin wrap", func(o *runtimeOptions) { o.KeystorePath = "miner.json"; o.FrostWrapKeyFD = 0 }, "descriptor >= 3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := emptySecretOptions()
			tc.set(&opts)
			err := loadRuntimeSecrets(&opts)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v want %q", err, tc.want)
			}
			if opts.KeyManager != nil || opts.ArbitrageKeyManager != nil {
				t.Fatal("invalid source published an identity")
			}
		})
	}
}
