//go:build linux

package nodekey

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// ReadPassphraseTTY 只从控制终端无回显读取；systemd 无TTY时明确失败。
func ReadPassphraseTTY(prompt string) ([]byte, error) {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, errors.New("no controlling terminal; provide an explicit passphrase FD")
	}
	defer f.Close()
	fd := int(f.Fd())
	old, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return nil, errors.New("passphrase input must be a terminal")
	}
	next := *old
	next.Lflag &^= unix.ECHO
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, &next); err != nil {
		return nil, errors.New("cannot disable terminal echo")
	}
	defer unix.IoctlSetTermios(fd, unix.TCSETS, old)
	if _, err := fmt.Fprint(f, prompt); err != nil {
		return nil, errors.New("write terminal prompt failed")
	}
	defer fmt.Fprintln(f)
	return readTerminalLine(f)
}
