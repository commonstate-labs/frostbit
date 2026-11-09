package config

import "fmt"

                                                                            
                                                                             
                                                                         
func ComputeFrostThreshold(committeeSize int, configuredRatio float64) (int, error) {
	if float32(configuredRatio) != float32(0.75) {
		return 0, fmt.Errorf("FROST threshold ratio must be exactly 0.75")
	}
	return ComputeVaultCommitteeThreshold(committeeSize)
}
