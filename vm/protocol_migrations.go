package vm

import (
	"fmt"
	"strconv"

	"dex/keys"
	"dex/pb"
	"dex/protocol"

	"google.golang.org/protobuf/proto"
)

                                                                              
                                                           
func (x *Executor) SetNetworkChainID(chainID uint64) {
	x.networkChainID = chainID
}

func protocolVersionFromState(sv StateView) (uint64, error) {
	raw, exists, err := sv.Get(keys.KeyProtocolVersion())
	if err != nil {
		return 0, err
	}
	if !exists {
		return protocol.LegacyVersion, nil
	}
	version, err := protocol.ParseStateVersion(raw)
	if err != nil {
		return 0, err
	}
	return version, nil
}

                                                                               
                                                                            
                                                                         
func (x *Executor) applyProtocolMigrations(sv StateView, height uint64) error {
	if x == nil || sv == nil || x.networkChainID == 0 {
		return nil
	}
	if err := protocol.ValidateUpgradeSchedule(x.networkChainID); err != nil {
		return err
	}
	version, err := protocolVersionFromState(sv)
	if err != nil {
		return fmt.Errorf("load protocol version: %w", err)
	}
	if version > protocol.CurrentVersion {
		return fmt.Errorf("state protocol version %d is newer than binary support %d", version, protocol.CurrentVersion)
	}

	for _, upgrade := range protocol.UpgradeSchedule(x.networkChainID) {
		if height < upgrade.ActivationHeight {
			break
		}
		receiptKey := keys.KeyProtocolUpgradeReceipt(upgrade.Version)
		receiptValue := protocol.UpgradeReceiptValue(upgrade)
		existing, receiptExists, readErr := sv.Get(receiptKey)
		if readErr != nil {
			return fmt.Errorf("load protocol upgrade receipt v%d: %w", upgrade.Version, readErr)
		} else if receiptExists && string(existing) != receiptValue {
			return fmt.Errorf("conflicting protocol upgrade receipt v%d", upgrade.Version)
		}

		appliedNow := false
		if version < upgrade.Version {
			if version+1 != upgrade.Version {
				return fmt.Errorf("cannot apply protocol upgrade v%d from state v%d", upgrade.Version, version)
			}
			if err := applyProtocolUpgrade(sv, upgrade); err != nil {
				return fmt.Errorf("apply protocol upgrade v%d (%s): %w", upgrade.Version, upgrade.ID, err)
			}
			version = upgrade.Version
			sv.Set(keys.KeyProtocolVersion(), []byte(strconv.FormatUint(version, 10)))
			appliedNow = true
		}
		if !receiptExists {
			if !appliedNow {
				return fmt.Errorf("state protocol version v%d is missing upgrade receipt v%d", version, upgrade.Version)
			}
			sv.Set(receiptKey, []byte(receiptValue))
		}
	}
	return nil
}

func applyProtocolUpgrade(sv StateView, upgrade protocol.Upgrade) error {
	switch upgrade.ID {
	case protocol.UpgradeBTCFeeConfigV2:
		return migrateBTCFeeConfigV2(sv)
	case protocol.UpgradeBTCIsolationV3:
		return migrateBTCIsolationV3(sv)
	default:
		return fmt.Errorf("binary does not implement migration %q", upgrade.ID)
	}
}

func migrateBTCFeeConfigV2(sv StateView) error {
	key := keys.KeyFrostVaultConfig("btc", 0)
	raw, exists, err := sv.Get(key)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("BTC vault config is missing")
	}
	var cfg pb.FrostVaultConfig
	if err := proto.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("decode BTC vault config: %w", err)
	}
	switch cfg.BtcFeeSatsPerVbyte {
	case 0:
		cfg.BtcFeeSatsPerVbyte = protocol.BTCFeeSatsPerVByteV2
		encoded, marshalErr := proto.Marshal(&cfg)
		if marshalErr != nil {
			return fmt.Errorf("encode BTC vault config: %w", marshalErr)
		}
		sv.Set(key, encoded)
	case protocol.BTCFeeSatsPerVByteV2:
		                                                         
	default:
		return fmt.Errorf("BTC fee rate is %d, want legacy 0 or v2 value %d", cfg.BtcFeeSatsPerVbyte, protocol.BTCFeeSatsPerVByteV2)
	}
	return nil
}
