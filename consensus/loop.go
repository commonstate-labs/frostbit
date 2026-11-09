package consensus

import (
	"time"
)

                                               
            
                                               

func RunLoop() {
	                                                                      
	                                                                                                           
	                                        
	config := DefaultConfig()

	                                       
	                                                                    
	                                                                        

	Logf("Starting Decoupled Snowman Consensus...\n")

	network := NewNetworkManager(config)
	if err := network.CreateNodes(); err != nil {
		Logf("Failed to create consensus simulation nodes: %v\n", err)
		return
	}

	programStart := time.Now()
	network.Start()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	lastHeight := uint64(0)
	for range ticker.C {
		minHeight, allDone := network.CheckProgress()
		if minHeight > lastHeight {
			Logf("\n✅ All honest nodes reached consensus on height %d\n", minHeight)
			lastHeight = minHeight
		}

		if allDone {
			totalTime := time.Since(programStart)
			Logf("\n🎉 All heights completed! Total time: %v\n", totalTime)

			time.Sleep(1 * time.Second)

			Logf("\n\n===== FINAL RESULTS =====")
			network.PrintStatus()
			network.PrintFinalResults()

			Logf("\n--- Time Statistics ---")
			Logf("Total Time: %v\n", totalTime)
			Logf("Average/Height: %v\n", totalTime/time.Duration(config.Consensus.NumHeights))

			return
		}
	}
}
