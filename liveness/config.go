package liveness

import "dex/protocol"

const DefaultFreezeBlocks uint64 = 100

                               
  
                                                                                             
  
                                                                                    
  
                                                                                                  
                                                                                                    
  
                                                                                                    
                           
type Config struct {
	ActivationThreshold float64 `json:"activation_offline_ratio"`                                               
	MinSamples          int     `json:"min_samples"`                                         
	MinReporters        int     `json:"min_reporters"`                                                                     
	Threshold           float64 `json:"threshold"`                                                                    
	OfflineGraceBlocks  uint64  `json:"offline_grace_blocks"`                                                                       
	WindowFloor         int     `json:"window_floor"`                                                                      
	WindowCeil          int     `json:"window_ceil"`                                     
	SafetyMultiplier    int     `json:"safety_multiplier"`                                                                   
	FreezeBlocks        uint64  `json:"freeze_blocks"`                                    
	MinStake            uint64  `json:"min_stake"`                                  
	SlashPerReport      uint64  `json:"slash_per_report"`                                                
	PauseRatio          float64 `json:"pause_offline_ratio"`                                          
	BetaRounds          int     `json:"beta_rounds"`                                                   
	K                   int     `json:"k"`                                          
	MinActiveMiners     int     `json:"min_active_miners"`                       
}

func DefaultConfig() Config {
	return Config{
		ActivationThreshold: 0.2,
		                                
		MinSamples:         10,                                     
		MinReporters:       3,                                                   
		SafetyMultiplier:   2,                                                    
		WindowFloor:        20,                                                    
		WindowCeil:         10000,                                           
		Threshold:          0.8,                                                         
		OfflineGraceBlocks: protocol.OfflineGraceBlocks,

		                          
		FreezeBlocks:   DefaultFreezeBlocks,                                      
		MinStake:       protocol.MinMinerStakeRaw,
		SlashPerReport: protocol.MinMinerStakeRaw / 2000,                                           

		                                                                         
		BetaRounds: 15,
		K:          20,

		                             
		PauseRatio:      0.5,
		MinActiveMiners: 20,
	}
}
