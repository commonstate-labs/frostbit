package protocol

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const (
	                                                                             
	                                                
	LegacyVersion uint64 = 1

	                                                                           
	                                                                          
	                                                                  
	CurrentVersion uint64 = 3

	UpgradeBTCFeeConfigV2 = "btc-fee-config-v2"
	BTCFeeSatsPerVByteV2  = uint64(25)
	UpgradeBTCIsolationV3 = "btc-withdraw-isolation-v3"
)

                                                                            
                                                                         
                                                                             
                                                       
type Upgrade struct {
	Version          uint64 `json:"version"`
	ActivationHeight uint64 `json:"activation_height"`
	ID               string `json:"id"`
}

                                                                              
                                                                                 
                                                                               
var compiledUpgradeSchedules = map[uint64][]Upgrade{}

                                                                              
func UpgradeSchedule(chainID uint64) []Upgrade {
	schedule := append([]Upgrade(nil), compiledUpgradeSchedules[chainID]...)
	sort.Slice(schedule, func(i, j int) bool { return schedule[i].Version < schedule[j].Version })
	return schedule
}

func ValidateUpgradeSchedule(chainID uint64) error {
	previousVersion := LegacyVersion
	previousHeight := uint64(0)
	for _, upgrade := range UpgradeSchedule(chainID) {
		if upgrade.Version != previousVersion+1 {
			return fmt.Errorf("protocol upgrade versions must be consecutive: chain=%d have=%d want=%d", chainID, upgrade.Version, previousVersion+1)
		}
		if upgrade.Version > CurrentVersion {
			return fmt.Errorf("protocol upgrade version %d exceeds binary support %d", upgrade.Version, CurrentVersion)
		}
		if upgrade.ActivationHeight == 0 || upgrade.ActivationHeight <= previousHeight {
			return fmt.Errorf("protocol upgrade heights must be positive and increasing: chain=%d version=%d height=%d", chainID, upgrade.Version, upgrade.ActivationHeight)
		}
		if upgrade.ID == "" {
			return fmt.Errorf("protocol upgrade ID is empty: chain=%d version=%d", chainID, upgrade.Version)
		}
		previousVersion = upgrade.Version
		previousHeight = upgrade.ActivationHeight
	}
	return nil
}

                                                                               
                                                                            
                                                                          
                
func UpgradePending(chainID, finalizedHeight, stateVersion uint64, upgradeID string) bool {
	for _, upgrade := range UpgradeSchedule(chainID) {
		if upgrade.ID == upgradeID {
			return stateVersion < upgrade.Version && finalizedHeight < upgrade.ActivationHeight
		}
	}
	return false
}

func UpgradeReceiptValue(upgrade Upgrade) string {
	return fmt.Sprintf("version=%d;activation_height=%d;id=%s", upgrade.Version, upgrade.ActivationHeight, upgrade.ID)
}

                                                                          
                                                                           
func ParseStateVersion(raw []byte) (uint64, error) {
	value := string(raw)
	if value == "" || strings.TrimSpace(value) != value {
		return 0, fmt.Errorf("protocol version marker is empty or non-canonical")
	}
	version, err := strconv.ParseUint(value, 10, 64)
	if err != nil || version == 0 || strconv.FormatUint(version, 10) != value {
		return 0, fmt.Errorf("invalid protocol version marker %q", value)
	}
	return version, nil
}
