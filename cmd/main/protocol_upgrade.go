package main

import (
	"fmt"

	"dex/keys"
	"dex/pb"
	"dex/protocol"

	"google.golang.org/protobuf/proto"
)

type protocolStateReader interface {
	Read(key string) (string, error)
}

type protocolHeightReader interface {
	GetFinalizedStateHeight() (uint64, bool, error)
}

type protocolStartupState struct {
	Version uint64
	Pending *protocol.Upgrade
}

func readPersistedProtocolVersion(reader protocolStateReader) (uint64, error) {
	if reader == nil {
		return 0, fmt.Errorf("protocol state reader is unavailable")
	}
	raw, err := reader.Read(keys.KeyProtocolVersion())
	if err != nil {
		if isNotFoundError(err) {
			return protocol.LegacyVersion, nil
		}
		return 0, err
	}
	return protocol.ParseStateVersion([]byte(raw))
}

                                                                         
                                                                     
func validatePersistedProtocolUpgradeState(reader protocolStateReader, chainID, finalizedHeight uint64) (protocolStartupState, error) {
	if chainID == 0 {
		return protocolStartupState{}, fmt.Errorf("protocol upgrade validation requires a network chain ID")
	}
	if err := protocol.ValidateUpgradeSchedule(chainID); err != nil {
		return protocolStartupState{}, err
	}
	version, err := readPersistedProtocolVersion(reader)
	if err != nil {
		return protocolStartupState{}, fmt.Errorf("read protocol version: %w", err)
	}
	if version > protocol.CurrentVersion {
		return protocolStartupState{}, fmt.Errorf("state protocol version %d is newer than binary support %d", version, protocol.CurrentVersion)
	}
	status := protocolStartupState{Version: version}
	for _, upgrade := range protocol.UpgradeSchedule(chainID) {
		receipt, receiptErr := reader.Read(keys.KeyProtocolUpgradeReceipt(upgrade.Version))
		receiptExists := receiptErr == nil
		if receiptErr != nil && !isNotFoundError(receiptErr) {
			return protocolStartupState{}, fmt.Errorf("read protocol upgrade receipt v%d: %w", upgrade.Version, receiptErr)
		}
		if receiptExists && receipt != protocol.UpgradeReceiptValue(upgrade) {
			return protocolStartupState{}, fmt.Errorf("conflicting protocol upgrade receipt v%d", upgrade.Version)
		}
		if receiptExists && version < upgrade.Version {
			return protocolStartupState{}, fmt.Errorf("protocol upgrade receipt v%d exists while state version is v%d", upgrade.Version, version)
		}
		if finalizedHeight >= upgrade.ActivationHeight {
			if version < upgrade.Version || !receiptExists {
				return protocolStartupState{}, fmt.Errorf(
					"protocol upgrade v%d (%s) was not applied at height %d; state is v%d at finalized height %d",
					upgrade.Version, upgrade.ID, upgrade.ActivationHeight, version, finalizedHeight,
				)
			}
			continue
		}
		if version < upgrade.Version && status.Pending == nil {
			copyUpgrade := upgrade
			status.Pending = &copyUpgrade
		}
	}
	return status, nil
}

func currentProtocolHeight(reader protocolStateReader) (uint64, error) {
	if heights, ok := reader.(protocolHeightReader); ok {
		height, _, err := heights.GetFinalizedStateHeight()
		return height, err
	}
	return 0, nil
}

func pendingLegacyBTCFeeCompatibility(reader protocolStateReader, chainID uint64) (bool, error) {
	version, err := readPersistedProtocolVersion(reader)
	if err != nil {
		return false, err
	}
	finalizedHeight, err := currentProtocolHeight(reader)
	if err != nil {
		return false, fmt.Errorf("read finalized protocol height: %w", err)
	}
	return protocol.UpgradePending(
		chainID,
		finalizedHeight,
		version,
		protocol.UpgradeBTCFeeConfigV2,
	), nil
}

func frostVaultConfigMatchesProtocol(stateConfig, expectedStateConfig *pb.FrostVaultConfig, allowLegacyBTCFee bool) bool {
	if stateConfig == nil || expectedStateConfig == nil {
		return false
	}
	if proto.Equal(stateConfig, expectedStateConfig) {
		return true
	}
	if !allowLegacyBTCFee || stateConfig.BtcFeeSatsPerVbyte != 0 ||
		expectedStateConfig.BtcFeeSatsPerVbyte != protocol.BTCFeeSatsPerVByteV2 {
		return false
	}
	legacy := proto.Clone(stateConfig).(*pb.FrostVaultConfig)
	legacy.BtcFeeSatsPerVbyte = expectedStateConfig.BtcFeeSatsPerVbyte
	return proto.Equal(legacy, expectedStateConfig)
}
