package chain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"dex/keys"
	"dex/pb"
	"dex/protocol"
)

type BTCIsolationReader func(string) ([]byte, bool, error)

                                                                           
                                                                           
func BTCIsolationActive(read BTCIsolationReader) (bool, error) {
	raw, found, err := read(keys.KeyProtocolVersion())
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	version, err := protocol.ParseStateVersion(raw)
	if err != nil {
		return false, err
	}
	if version > protocol.CurrentVersion {
		return false, fmt.Errorf("unsupported state protocol version %d", version)
	}
	if version < uint64(protocol.BTCIsolationVersion) {
		return false, nil
	}
	raw, found, err = read(keys.KeyProtocolUpgradeReceipt(uint64(protocol.BTCIsolationVersion)))
	if err != nil {
		return false, err
	}
	if !found {
		return false, fmt.Errorf("BTC isolation activation receipt is missing")
	}
	text := string(raw)
	const prefix = "version=3;activation_height="
	suffix := ";id=" + protocol.UpgradeBTCIsolationV3
	if !strings.HasPrefix(text, prefix) || !strings.HasSuffix(text, suffix) {
		return false, fmt.Errorf("invalid BTC isolation activation receipt")
	}
	heightRaw := strings.TrimSuffix(strings.TrimPrefix(text, prefix), suffix)
	height, err := strconv.ParseUint(heightRaw, 10, 64)
	if err != nil || height == 0 || strconv.FormatUint(height, 10) != heightRaw {
		return false, fmt.Errorf("invalid BTC isolation activation height")
	}
	return true, nil
}

func btcIsolationHash(domain string, fields ...[]byte) []byte {
	all := make([][]byte, 0, len(fields)+1)
	all = append(all, []byte(domain))
	all = append(all, fields...)
	digest := sha256.Sum256(encodeMigrationFields(all...))
	return digest[:]
}

                                                                               
                                                                              
func BTCOriginID(network uint64, btcNetwork, txid string, vout uint32, legacy bool) []byte {
	domain := "DEX_BTC_ORIGIN_V3"
	if legacy {
		domain = "DEX_BTC_LEGACY_BOUNDARY_V3"
	}
	return btcIsolationHash(domain, uint64Bytes(network), []byte(btcNetwork), []byte(txid), uint32Bytes(vout))
}

type BTCWithdrawPartIntent struct {
	Network      uint64
	BTCNetwork   string
	ParentID     string
	PartSeq      uint64
	VaultID      uint32
	KeyEpoch     uint64
	Gross        uint64
	Fee          uint64
	OriginID     []byte
	TemplateHash []byte
}

func BTCWithdrawPartJobID(v BTCWithdrawPartIntent) string {
	hash := btcIsolationHash("DEX_BTC_WITHDRAW_PART_V3", uint64Bytes(v.Network), []byte(v.BTCNetwork), []byte(v.ParentID), uint64Bytes(v.PartSeq), uint32Bytes(v.VaultID), uint64Bytes(v.KeyEpoch), uint64Bytes(v.Gross), uint64Bytes(v.Fee), v.OriginID, v.TemplateHash)
	return hex.EncodeToString(hash)
}

func BTCMigrationChildIntentHash(network uint64, parentID string, seq uint64, originID, templateHash []byte) []byte {
	return btcIsolationHash("DEX_BTC_MIGRATION_CHILD_V3", uint64Bytes(network), []byte(parentID), uint64Bytes(seq), originID, templateHash)
}
func BTCMigrationChildID(parentID string, seq uint64, childIntentHash []byte) string {
	hash := btcIsolationHash("DEX_BTC_MIGRATION_CHILD_ID_V3", []byte(parentID), uint64Bytes(seq), childIntentHash)
	return "migration_part_" + hex.EncodeToString(hash)
}
func BTCMigrationManifestAccumulate(previous, childIntentHash []byte) []byte {
	return btcIsolationHash("DEX_BTC_MIGRATION_MANIFEST_STEP_V3", previous, childIntentHash)
}
func BTCMigrationManifestIntentHash(network uint64, btcNetwork string, v *pb.FrostVaultMigrationState) []byte {
	return btcIsolationHash("DEX_BTC_MIGRATION_MANIFEST_V3", uint64Bytes(network), []byte(btcNetwork), []byte(v.MigrationId), uint32Bytes(v.VaultId), uint64Bytes(v.OldEpoch), uint64Bytes(v.NewEpoch), v.OldGroupPubkey, v.NewGroupPubkey, v.BtcManifestHash, uint64Bytes(v.BtcChildCount), []byte(v.BtcTotalInputAmount), []byte(v.BtcTotalOutputAmount), []byte(v.BtcTotalFeeAmount), uint32Bytes(v.BtcProtocolVersion), uint32Bytes(v.AuthorityProtocol))
}
