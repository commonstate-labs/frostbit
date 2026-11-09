package nodekey

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
)

// ReadPassphraseFD 消费并关闭显式继承的FD；不从环境或非TTY标准输入猜测口令。
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

// readBounded 只分配一次，额外一个字节用于检测超长；返回切片未使用的尾部始终为零。
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
	// 一次分配有界缓冲，避免 append 扩容留下多份口令副本。
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
