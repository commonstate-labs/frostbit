package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/btcsuite/btcd/btcec/v2"
)

const (
	maxSmallJSONBytes = 8 << 20
	maxLargeJSONBytes = 64 << 20
	maxSecretFDBytes  = 256
)

func readRegularFile(path string, maxBytes int) ([]byte, error) {
	abs, err := checkedExistingPath(path, false)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("input %s is not a regular file", abs)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o022 != 0 {
		return nil, fmt.Errorf("input %s is writable by group or others", abs)
	}
	if info.Size() <= 0 || info.Size() > int64(maxBytes) {
		return nil, fmt.Errorf("input %s is empty or exceeds %d bytes", abs, maxBytes)
	}
	file, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return nil, fmt.Errorf("input %s changed while opening", abs)
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(maxBytes+1)))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > maxBytes {
		return nil, fmt.Errorf("input %s is empty or exceeds %d bytes", abs, maxBytes)
	}
	final, err := os.Lstat(abs)
	if err != nil || final.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, final) || final.Size() != info.Size() || !final.ModTime().Equal(info.ModTime()) {
		wipe(data)
		return nil, fmt.Errorf("input %s changed while reading", abs)
	}
	return data, nil
}

func writeExclusiveJSON(path string, value any, mode os.FileMode) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	defer wipe(data)
	return writeExclusive(path, data, mode)
}

func writeExclusive(path string, data []byte, mode os.FileMode) error {
	abs, err := checkedNewPath(path)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("create output %s: %w", abs, err)
	}
	created := true
	defer func() {
		_ = file.Close()
		if created {
			_ = os.Remove(abs)
		}
	}()
	remaining := data
	for len(remaining) > 0 {
		written, err := file.Write(remaining)
		if err != nil {
			return fmt.Errorf("write output %s: %w", abs, err)
		}
		if written == 0 {
			return fmt.Errorf("write output %s: %w", abs, io.ErrShortWrite)
		}
		remaining = remaining[written:]
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync output %s: %w", abs, err)
	}
	if err := file.Chmod(mode); err != nil {
		return fmt.Errorf("set output mode %s: %w", abs, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close output %s: %w", abs, err)
	}
	if err := syncOutputDirectory(filepath.Dir(abs)); err != nil {
		return fmt.Errorf("sync output directory for %s: %w", abs, err)
	}
	created = false
	return nil
}

func syncOutputDirectory(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func createExclusiveDirectory(path string, mode os.FileMode) (string, error) {
	abs, err := checkedNewPath(path)
	if err != nil {
		return "", err
	}
	if err := os.Mkdir(abs, mode); err != nil {
		return "", fmt.Errorf("create output directory %s: %w", abs, err)
	}
	if err := os.Chmod(abs, mode); err != nil {
		_ = os.Remove(abs)
		return "", fmt.Errorf("set output directory mode %s: %w", abs, err)
	}
	return abs, nil
}

func checkedExistingPath(path string, wantDirectory bool) (string, error) {
	abs, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil || strings.TrimSpace(path) == "" {
		return "", errors.New("path is empty or invalid")
	}
	if err := rejectSymlinkAncestors(abs); err != nil {
		return "", err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("symlink paths are forbidden: %s", abs)
	}
	if wantDirectory && !info.IsDir() {
		return "", fmt.Errorf("path %s is not a directory", abs)
	}
	return abs, nil
}

func checkedNewPath(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", errors.New("output path is empty")
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(abs); err == nil {
		return "", fmt.Errorf("refusing to overwrite existing path %s", abs)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	parent := filepath.Dir(abs)
	if err := rejectSymlinkAncestors(parent); err != nil {
		return "", err
	}
	info, err := os.Lstat(parent)
	if err != nil {
		return "", fmt.Errorf("output parent %s is unavailable: %w", parent, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("output parent %s is not a directory", parent)
	}
	return abs, nil
}

func rejectSymlinkAncestors(path string) error {
	current, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for {
		info, statErr := os.Lstat(current)
		if statErr != nil {
			return fmt.Errorf("inspect path %s: %w", current, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink paths are forbidden: %s", current)
		}
		if runtime.GOOS != "windows" && info.IsDir() && info.Mode().Perm()&0o022 != 0 && info.Mode()&os.ModeSticky == 0 {
			return fmt.Errorf("path ancestor is writable by group or others: %s", current)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}

func strictJSON(data []byte, destination any) error {
	if len(data) == 0 || len(data) > maxLargeJSONBytes {
		return errors.New("JSON input is empty or too large")
	}
	if err := validateJSONStructure(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("trailing JSON data")
	}
	return nil
}

func validateJSONStructure(data []byte) error {
	if len(data) == 0 || len(data) > maxLargeJSONBytes {
		return errors.New("JSON input is empty or too large")
	}
	duplicates := json.NewDecoder(bytes.NewReader(data))
	duplicates.UseNumber()
	if err := rejectDuplicateValue(duplicates, 0); err != nil {
		return err
	}
	if _, err := duplicates.Token(); err != io.EOF {
		return errors.New("trailing JSON data")
	}
	return nil
}

func rejectDuplicateValue(decoder *json.Decoder, depth int) error {
	if depth > 64 {
		return errors.New("JSON nesting is too deep")
	}
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	delimiter, structured := token.(json.Delim)
	if !structured {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			nameToken, err := decoder.Token()
			if err != nil {
				return errors.New("invalid JSON object field")
			}
			name, ok := nameToken.(string)
			if !ok {
				return errors.New("invalid JSON object field name")
			}
			if _, duplicate := seen[name]; duplicate {
				return fmt.Errorf("duplicate JSON field %q", name)
			}
			seen[name] = struct{}{}
			if err := rejectDuplicateValue(decoder, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return errors.New("unterminated JSON object")
		}
	case '[':
		for decoder.More() {
			if err := rejectDuplicateValue(decoder, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return errors.New("unterminated JSON array")
		}
	default:
		return errors.New("invalid JSON delimiter")
	}
	return nil
}

func listStrictJSONFiles(path string) ([]string, error) {
	abs, err := checkedExistingPath(path, true)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			return nil, fmt.Errorf("directory %s contains a non-regular JSON entry %q", abs, entry.Name())
		}
		full := filepath.Join(abs, entry.Name())
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("directory %s contains an invalid entry %q", abs, entry.Name())
		}
		files = append(files, full)
	}
	sort.Strings(files)
	return files, nil
}

func readSecretFD(fd int, label string) ([]byte, error) {
	if fd < 3 {
		return nil, fmt.Errorf("%s must be supplied through an inherited descriptor >= 3", label)
	}
	file := os.NewFile(uintptr(fd), label)
	if file == nil {
		return nil, fmt.Errorf("%s descriptor is unavailable", label)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.IsDir() || info.Mode()&os.ModeDevice != 0 || !info.Mode().IsRegular() && info.Mode()&os.ModeNamedPipe == 0 {
		return nil, fmt.Errorf("%s descriptor must be a regular file or pipe", label)
	}
	if info.Mode().IsRegular() {
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
			return nil, fmt.Errorf("%s file permissions must not grant group or other access", label)
		}
		if info.Size() <= 0 || info.Size() > maxSecretFDBytes {
			return nil, fmt.Errorf("%s descriptor is empty or too large", label)
		}
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxSecretFDBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s descriptor: %w", label, err)
	}
	defer wipe(raw)
	if len(raw) == 0 || len(raw) > maxSecretFDBytes {
		return nil, fmt.Errorf("%s descriptor is empty or too large", label)
	}
	fields := bytes.Fields(raw)
	if len(fields) != 1 {
		return nil, fmt.Errorf("%s descriptor must contain exactly one token", label)
	}
	return bytes.Clone(fields[0]), nil
}

func decode32ByteHex(token []byte, label string) ([]byte, error) {
	defer wipe(token)
	if bytes.HasPrefix(token, []byte("0x")) || bytes.HasPrefix(token, []byte("0X")) {
		token = token[2:]
	}
	if len(token) != 64 {
		return nil, fmt.Errorf("%s must contain exactly 32 bytes of hex", label)
	}
	decoded := make([]byte, 32)
	if _, err := hex.Decode(decoded, token); err != nil {
		wipe(decoded)
		return nil, fmt.Errorf("%s is not valid hex", label)
	}
	return decoded, nil
}

func readIdentityKey(fd int) (*btcec.PrivateKey, []byte, error) {
	token, err := readSecretFD(fd, "identity private key")
	if err != nil {
		return nil, nil, err
	}
	raw, err := decode32ByteHex(token, "identity private key")
	if err != nil {
		return nil, nil, err
	}
	privateKey, publicKey := btcec.PrivKeyFromBytes(raw)
	if !bytes.Equal(privateKey.Serialize(), raw) || privateKey.Key.IsZero() || publicKey == nil {
		privateKey.Zero()
		wipe(raw)
		return nil, nil, errors.New("identity private key is outside the secp256k1 scalar field")
	}
	return privateKey, raw, nil
}

func readWrapKey(fd int) ([]byte, error) {
	token, err := readSecretFD(fd, "FROST wrap key")
	if err != nil {
		return nil, err
	}
	return decode32ByteHex(token, "FROST wrap key")
}

func wipe(data []byte) {
	clear(data)
	runtime.KeepAlive(data)
}
