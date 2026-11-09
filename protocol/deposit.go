package protocol

import (
	"encoding/hex"
	"fmt"
	"strings"
)

const DepositProtocolVault uint32 = 2

func IsBoundDepositProtocol(version uint32) bool {
	return version == DepositProtocolVault
}

                                                                                  
                                                                
func NormalizeExternalTxHash(chain, raw string) (string, error) {
	value := strings.TrimSpace(raw)
	switch chain {
	case "eth", "bnb", "base", "arb":
		if len(value) != 66 || !strings.EqualFold(value[:2], "0x") {
			return "", fmt.Errorf("%s transaction hash must contain 32 bytes with 0x prefix", chain)
		}
		value = value[2:]
	case "btc", "trx":
		if len(value) == 66 && strings.EqualFold(value[:2], "0x") {
			value = value[2:]
		}
	default:
		return "", fmt.Errorf("unsupported external chain %q", chain)
	}
	if len(value) != 64 {
		return "", fmt.Errorf("external transaction hash must contain exactly 32 bytes")
	}
	if _, err := hex.DecodeString(value); err != nil {
		return "", fmt.Errorf("invalid external transaction hash: %w", err)
	}
	value = strings.ToLower(value)
	if chain != "btc" && chain != "trx" {
		value = "0x" + value
	}
	return value, nil
}

func CanonicalExternalTxHash(chain, raw string) (string, error) {
	canonical, err := NormalizeExternalTxHash(chain, raw)
	if err != nil {
		return "", err
	}
	if canonical != raw {
		return "", fmt.Errorf("external transaction hash must use canonical lowercase hex without whitespace")
	}
	return canonical, nil
}
