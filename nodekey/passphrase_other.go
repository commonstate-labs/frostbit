//go:build !linux && !windows

package nodekey

import "errors"

func ReadPassphraseTTY(string) ([]byte, error) {
	return nil, errors.New("terminal passphrase input is unsupported on this platform; use an explicit passphrase FD")
}
