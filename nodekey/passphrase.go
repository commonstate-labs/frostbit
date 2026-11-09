package nodekey

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
)

                                                                                                       
func ReadPassphraseFD(fd int) ([]byte, error) {
	if fd < 3 {
		return nil, errors.New("passphrase FD must be an inherited descriptor >= 3")
	}
	f := os.NewFile(uintptr(fd), "keystore-passphrase")
	if f == nil {
		return nil, errors.New("invalid passphrase FD")
	}
	defer f.Close()
	raw, err := readBounded(f, MaxPassphraseBytes+2)
	if err != nil {
		Wipe(raw)
		return nil, errors.New("read passphrase FD failed")
	}
	return normalizePassphrase(raw)
}

func normalizePassphrase(raw []byte) ([]byte, error) {
	owned := raw
	if bytes.HasSuffix(raw, []byte("\r\n")) {
		raw = raw[:len(raw)-2]
	} else if bytes.HasSuffix(raw, []byte("\n")) {
		raw = raw[:len(raw)-1]
	}
	if err := validatePassword(raw); err != nil {
		Wipe(owned)
		return nil, err
	}
	clear(owned[len(raw):])
	return raw, nil
}

                                                                                                                     
func readBounded(r io.Reader, limit int) ([]byte, error) {
	buffer := make([]byte, limit+1)
	n, err := io.ReadFull(r, buffer)
	if (err != nil && err != io.EOF && err != io.ErrUnexpectedEOF) || n > limit {
		Wipe(buffer)
		return nil, errors.New("input exceeds size limit or could not be read")
	}
	return buffer[:n], nil
}

func readTerminalLine(f *os.File) ([]byte, error) {
	                                                                             
	data := make([]byte, 0, MaxPassphraseBytes+2)
	var one [1]byte
	defer Wipe(one[:])
	for len(data) <= MaxPassphraseBytes+1 {
		n, err := f.Read(one[:])
		if n > 0 {
			data = append(data, one[0])
			if one[0] == '\n' {
				return normalizePassphrase(data)
			}
		}
		if err != nil {
			if err == io.EOF && len(data) > 0 {
				return normalizePassphrase(data)
			}
			Wipe(data)
			return nil, fmt.Errorf("read terminal passphrase: %w", err)
		}
	}
	Wipe(data)
	return nil, errors.New("passphrase exceeds size limit")
}
