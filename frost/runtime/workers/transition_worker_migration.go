package workers

import (
	"dex/config"
	"dex/logs"
	"dex/pb"

	"encoding/hex"
	"fmt"
	"github.com/btcsuite/btcd/chaincfg"

	"strconv"
	"strings"
)

func pickTransitionSignature(signedPkg *SignedPackage) ([]byte, error) {
	if signedPkg == nil {
		return nil, fmt.Errorf("signed package is nil")
	}
	if len(signedPkg.Signature) > 0 {
		return append([]byte(nil), signedPkg.Signature...), nil
	}
	if len(signedPkg.Signatures) == 1 && len(signedPkg.Signatures[0]) > 0 {
		return append([]byte(nil), signedPkg.Signatures[0]...), nil
	}
	return nil, fmt.Errorf("transition signing output missing single signature")
}

func (w *TransitionWorker) resolveMigrationThreshold(chain string, vaultID uint32, transition *pb.VaultTransitionState) (int, error) {
	if w.vaultProvider == nil {
		return 0, fmt.Errorf("vault provider is required to resolve the frozen migration threshold")
	}
	threshold, err := w.vaultProvider.CalculateThreshold(chain, vaultID)
	if err != nil {
		return 0, fmt.Errorf("resolve frozen migration threshold: %w", err)
	}
	if threshold <= 0 {
		return 0, fmt.Errorf("frozen migration threshold is missing")
	}
	if transition != nil && len(transition.OldCommitteeMembers) > 0 {
		committeeSize := len(transition.OldCommitteeMembers)
		if threshold > committeeSize {
			return 0, fmt.Errorf("frozen migration threshold %d exceeds old committee size %d", threshold, committeeSize)
		}
		if committeeSize >= 4 {
			expected, thresholdErr := config.ComputeVaultCommitteeThreshold(committeeSize)
			if thresholdErr != nil {
				return 0, fmt.Errorf("invalid frozen old committee: %w", thresholdErr)
			}
			if threshold != expected {
				return 0, fmt.Errorf("frozen migration threshold %d does not match old committee threshold %d", threshold, expected)
			}
		}
	}
	return threshold, nil
}

func (w *TransitionWorker) shouldCoordinateMigration(jobID, chain string, vaultID uint32, keyEpoch uint64) (bool, string) {
	if w.vaultProvider == nil || strings.TrimSpace(w.localAddress) == "" {
		return true, ""
	}
	committee, err := w.vaultProvider.VaultCommittee(chain, vaultID, keyEpoch)
	if err != nil || len(committee) == 0 {
		logs.Warn("[TransitionWorker] cannot resolve migration coordinator, fallback local execution: chain=%s vault=%d epoch=%d err=%v", chain, vaultID, keyEpoch, err)
		return true, ""
	}
	coordIndex := computeJobCoordinatorIndex(jobID, keyEpoch, committee)
	coordAddr := string(committee[coordIndex].ID)
	return w.localAddress == coordAddr, coordAddr
}

func buildBTCMigrationJobID(chain string, vaultID uint32, epochID uint64, templateHash []byte) string {
	hashHex := hex.EncodeToString(templateHash)
	if len(hashHex) > 16 {
		hashHex = hashHex[:16]
	}
	return fmt.Sprintf("migration_%s_%d_%d_%s", chain, vaultID, epochID, hashHex)
}

func migrationOutpointKey(txid string, vout uint32) string {
	return txid + ":" + strconv.FormatUint(uint64(vout), 10)
}

func resolveMigrationBTCSignatures(signedPkg *SignedPackage, inputCount int) ([][]byte, error) {
	if signedPkg == nil {
		return nil, fmt.Errorf("signed package is nil")
	}
	if inputCount <= 0 {
		return nil, fmt.Errorf("invalid BTC input count: %d", inputCount)
	}

	if len(signedPkg.Signatures) > 0 {
		if len(signedPkg.Signatures) != inputCount {
			return nil, fmt.Errorf("btc signature count mismatch: got=%d want=%d", len(signedPkg.Signatures), inputCount)
		}
		result := make([][]byte, inputCount)
		for i, sig := range signedPkg.Signatures {
			if len(sig) != 64 {
				return nil, fmt.Errorf("invalid btc signature length for input %d: %d", i, len(sig))
			}
			result[i] = append([]byte(nil), sig...)
		}
		return result, nil
	}

	if len(signedPkg.Signature) == 0 {
		return nil, fmt.Errorf("missing BTC signatures")
	}
	if inputCount == 1 {
		if len(signedPkg.Signature) != 64 {
			return nil, fmt.Errorf("invalid btc signature length: %d", len(signedPkg.Signature))
		}
		return [][]byte{append([]byte(nil), signedPkg.Signature...)}, nil
	}
	if len(signedPkg.Signature) != inputCount*64 {
		return nil, fmt.Errorf("invalid combined BTC signature length: got=%d want=%d", len(signedPkg.Signature), inputCount*64)
	}
	result := make([][]byte, inputCount)
	for i := 0; i < inputCount; i++ {
		start := i * 64
		end := start + 64
		result[i] = append([]byte(nil), signedPkg.Signature[start:end]...)
	}
	return result, nil
}

func parseAmount(amount string) uint64 {
	n, _ := strconv.ParseUint(amount, 10, 64)
	return n
}

func btcNetParamsForChain(chain string) *chaincfg.Params {
	lower := strings.ToLower(strings.TrimSpace(chain))
	switch lower {
	case "btc_testnet", "btc-testnet", "btctestnet", "testnet":
		return &chaincfg.TestNet3Params
	case "btc_regtest", "btc-regtest", "btcregtest", "regtest":
		return &chaincfg.RegressionNetParams
	case "btc_signet", "btc-signet", "btcsignet", "signet":
		return &chaincfg.SigNetParams
	default:
		return &chaincfg.MainNetParams
	}
}

                                        
func (w *TransitionWorker) getChainAdapter(chain string) (ChainAdapter, error) {
	if w.adapterFactory == nil {
		return nil, fmt.Errorf("chain adapter factory not available")
	}
	return w.adapterFactory.Adapter(chain)
}

                                                                           
