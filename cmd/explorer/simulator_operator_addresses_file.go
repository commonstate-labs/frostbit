package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"dex/config"
	"dex/utils"
)

const simulatorOperatorAddressesFileMaxBytes = int64(16 << 10)

func resolveSimulatorOperatorAddresses(rawAddresses, rawFilePath string) (string, error) {
	filePath := strings.TrimSpace(rawFilePath)
	if filePath == "" {
		return rawAddresses, nil
	}
	if strings.TrimSpace(rawAddresses) != "" {
		return "", fmt.Errorf("--simulator-operator-addresses-file cannot be combined with --simulator-operator-addresses or %s", simulatorOperatorAddressesEnv)
	}
	return loadSimulatorOperatorAddressesFile(filePath)
}

func validateSimulatorOperatorServiceToken(rawAddresses, rawToken string) error {
	if strings.TrimSpace(rawAddresses) != "" {
		if err := config.ValidateExplorerServiceToken(rawToken); err != nil {
			return fmt.Errorf("Explorer operator controls require a valid service token: %w", err)
		}
	}
	return nil
}

func loadSimulatorOperatorAddressesFile(path string) (string, error) {
	entry, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("read simulator operator addresses file: %w", err)
	}
	if entry.Mode()&os.ModeSymlink != 0 || !entry.Mode().IsRegular() {
		return "", errors.New("simulator operator addresses file must be a regular, non-symlink file")
	}
	if entry.Size() > simulatorOperatorAddressesFileMaxBytes {
		return "", fmt.Errorf("simulator operator addresses file exceeds %d bytes", simulatorOperatorAddressesFileMaxBytes)
	}

	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open simulator operator addresses file: %w", err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("stat simulator operator addresses file: %w", err)
	}
	if !opened.Mode().IsRegular() || !os.SameFile(entry, opened) {
		return "", errors.New("simulator operator addresses file changed while it was being opened")
	}

	payload, err := io.ReadAll(io.LimitReader(file, simulatorOperatorAddressesFileMaxBytes+1))
	if err != nil {
		return "", fmt.Errorf("read simulator operator addresses file: %w", err)
	}
	if int64(len(payload)) > simulatorOperatorAddressesFileMaxBytes {
		return "", fmt.Errorf("simulator operator addresses file exceeds %d bytes", simulatorOperatorAddressesFileMaxBytes)
	}
	scanner := bufio.NewScanner(bytes.NewReader(payload))
	scanner.Buffer(make([]byte, 1024), int(simulatorOperatorAddressesFileMaxBytes+1))
	addresses := make([]string, 0, simulatorOperatorMaxAddresses)
	seen := make(map[string]struct{}, simulatorOperatorMaxAddresses)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !utils.IsDexAddress(line) {
			return "", fmt.Errorf("simulator operator addresses file line %d is not a canonical fbc1 address", lineNumber)
		}
		if _, duplicate := seen[line]; duplicate {
			return "", fmt.Errorf("simulator operator addresses file line %d repeats address %s", lineNumber, line)
		}
		seen[line] = struct{}{}
		addresses = append(addresses, line)
		if len(addresses) > simulatorOperatorMaxAddresses {
			return "", fmt.Errorf("simulator operator address count exceeds %d", simulatorOperatorMaxAddresses)
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read simulator operator addresses file: %w", err)
	}
	if len(addresses) == 0 {
		return "", errors.New("simulator operator addresses file contains no addresses")
	}
	return strings.Join(addresses, ","), nil
}
