package keys

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

                                                                            
func KeyFrostBtcUtxoAllPrefix() string { return pfxFrost + "btc_utxo_" }
func KeyFrostBtcUtxoAmountPrefix(vaultID uint32) string {
	return fmt.Sprintf("%sbtc_amount_%010d_", pfxFrost, vaultID)
}
func KeyFrostBtcUtxoAmount(vaultID uint32, amount uint64, txid string, vout uint32) string {
	return fmt.Sprintf("%s%020d_%s_%010d", KeyFrostBtcUtxoAmountPrefix(vaultID), amount, txid, vout)
}
func ParseFrostBtcUtxoAmountKey(key string) (uint32, uint64, string, uint32, error) {
	prefix := pfxFrost + "btc_amount_"
	if !strings.HasPrefix(key, prefix) {
		return 0, 0, "", 0, fmt.Errorf("invalid BTC amount key prefix")
	}
	parts := strings.Split(strings.TrimPrefix(key, prefix), "_")
	if len(parts) != 4 {
		return 0, 0, "", 0, fmt.Errorf("invalid BTC amount key")
	}
	vault, err := strconv.ParseUint(parts[0], 10, 32)
	if err != nil {
		return 0, 0, "", 0, err
	}
	amount, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return 0, 0, "", 0, err
	}
	vout, err := strconv.ParseUint(parts[3], 10, 32)
	if err != nil {
		return 0, 0, "", 0, err
	}
	decoded, err := hex.DecodeString(parts[2])
	if err != nil || len(decoded) != 32 || strings.ToLower(parts[2]) != parts[2] || key != KeyFrostBtcUtxoAmount(uint32(vault), amount, parts[2], uint32(vout)) {
		return 0, 0, "", 0, fmt.Errorf("non-canonical BTC amount key")
	}
	return uint32(vault), amount, parts[2], uint32(vout), nil
}
func KeyFrostBtcUtxoAmountBuildState() string { return pfxFrost + "btc_amount_build" }
func KeyFrostBtcWithdrawReadyPrefix(asset string) string {
	return pfxFrost + "btc_ready_" + asset + "_"
}

                                                                           
                                                                          
func KeyFrostBtcWithdrawParentSeq(asset string) string {
	return pfxFrost + "btc_parent_seq_" + asset
}
func KeyFrostBtcWithdrawReady(asset string, seq uint64, parentID string) string {
	return fmt.Sprintf("%s%020d_%s", KeyFrostBtcWithdrawReadyPrefix(asset), seq, parentID)
}
func KeyFrostBtcWithdrawPartPrefix(parentID string) string {
	return pfxFrost + "btc_part_" + parentID + "_"
}
func KeyFrostBtcWithdrawPart(parentID string, partSeq uint64) string {
	return fmt.Sprintf("%s%020d", KeyFrostBtcWithdrawPartPrefix(parentID), partSeq)
}
func KeyFrostBtcWithdrawActiveChildPrefix(asset string) string {
	return pfxFrost + "btc_active_child_" + asset + "_"
}
func KeyFrostBtcWithdrawActiveChild(asset, jobID string) string {
	return KeyFrostBtcWithdrawActiveChildPrefix(asset) + jobID
}
func KeyFrostBtcSignedPackageHash(jobID string) string { return pfxFrost + "btc_signed_hash_" + jobID }
func KeyFrostBtcMigrationChildPrefix(parentID string) string {
	return pfxFrost + "btc_migration_child_" + parentID + "_"
}
func KeyFrostBtcMigrationChild(parentID string, seq uint64) string {
	return fmt.Sprintf("%s%020d", KeyFrostBtcMigrationChildPrefix(parentID), seq)
}
func KeyFrostBtcMigrationActiveChildPrefix(parentID string) string {
	return pfxFrost + "btc_migration_active_" + parentID + "_"
}
func KeyFrostBtcMigrationActiveChild(parentID string, seq uint64) string {
	return fmt.Sprintf("%s%020d", KeyFrostBtcMigrationActiveChildPrefix(parentID), seq)
}
func KeyFrostBtcMigrationChildState(childID string) string {
	return pfxFrost + "btc_migration_state_" + childID
}
