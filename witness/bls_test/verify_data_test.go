package main

import (
	"dex/logs"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"go.dedis.ch/kyber/v3/pairing/bn256"
	"go.dedis.ch/kyber/v3/sign/bls"
)

                                              
type DataSet struct {
	Message      []byte `json:"message"`
	AggPublicKey []byte `json:"agg_public_key"`
	AggSignature []byte `json:"agg_signature"`
}

type AllData struct {
	Sets []DataSet `json:"sets"`
}

func main() {
	                        
	file, err := os.Open("bls_data.json")
	if err != nil {
		logs.Error("failed to open file: %v", err)
	}
	defer file.Close()

	                 
	var allData AllData
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&allData); err != nil {
		logs.Error("failed to decode JSON: %v", err)
	}

	                      
	suite := bn256.NewSuite()

	totalSets := len(allData.Sets)
	fmt.Printf("Loaded %d groups of aggregated data from bls_data.json\n", totalSets)

	startTime := time.Now()
	validCount := 0

	                                             
	for i, dataSet := range allData.Sets {
		                               
		aggPk := suite.G2().Point()
		if err := aggPk.UnmarshalBinary(dataSet.AggPublicKey); err != nil {
			logs.Error("failed to unmarshal agg pubkey in set %d: %v", i, err)
		}

		                         
		                                            
		                                       
		err := bls.Verify(suite, aggPk, dataSet.Message, dataSet.AggSignature)
		if err != nil {
			logs.Error("Agg signature verify failed in set %d: %v", i, err)
		}
		validCount++
	}

	duration := time.Since(startTime)

	fmt.Printf("Successfully verified %d aggregated signatures.\n", validCount)
	fmt.Printf("Total verification time for %d groups: %v\n", totalSets, duration)
	fmt.Printf("Average verification time per group: %v\n", duration/time.Duration(totalSets))
}
