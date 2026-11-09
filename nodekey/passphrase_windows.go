//go:build windows

package nodekey

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func ReadPassphraseTTY(prompt string) ([]byte, error) {
	h := windows.Handle(os.Stdin.Fd())
	var old uint32
	if err := windows.GetConsoleMode(h, &old); err != nil {
		return nil, errors.New("passphrase input must be an interactive console")
	}
	if err := windows.SetConsoleMode(h, old&^windows.ENABLE_ECHO_INPUT); err != nil {
		return nil, errors.New("cannot disable console echo")
	}
	defer windows.SetConsoleMode(h, old)
	if _, err := fmt.Fprint(os.Stderr, prompt); err != nil {
		return nil, errors.New("write console prompt failed")
	}
	defer fmt.Fprintln(os.Stderr)
	return readTerminalLine(os.Stdin)
}
