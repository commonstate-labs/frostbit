package main

import (
	"crypto/rand"
	"dex/logs"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"go.dedis.ch/kyber/v3"
	"go.dedis.ch/kyber/v3/pairing/bn256"
	"go.dedis.ch/kyber/v3/sign/bls"
	"go.dedis.ch/kyber/v3/util/random"
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
	                                      
	suite := bn256.NewSuite()

	                                                            
	numSets := 1000
	numParticipants := 10
	allData := AllData{Sets: make([]DataSet, numSets)}

	                                                                     
	var totalAggTime time.Duration

	for i := 0; i < numSets; i++ {
		                               
		msg := make([]byte, 32)
		if _, err := rand.Read(msg); err != nil {
			logs.Error("failed to generate random message: %v", err)
		}

		                                     
		pubKeys := make([]kyber.Point, numParticipants)
		sigs := make([][]byte, numParticipants)

		for j := 0; j < numParticipants; j++ {
			                                        
			rng := random.New(rand.Reader)

			                              
			privKey, pubKey := bls.NewKeyPair(suite, rng)

			                    
			signature, err := bls.Sign(suite, privKey, msg)
			if err != nil {
				logs.Error("failed to sign: %v", err)
			}

			pubKeys[j] = pubKey
			sigs[j] = signature
		}

		                                                    
		startAgg := time.Now()

		aggPubKey := bls.AggregatePublicKeys(suite, pubKeys...)

		aggSig, err := bls.AggregateSignatures(suite, sigs...)
		if err != nil {
			logs.Error("failed to aggregate signatures: %v", err)
		}

		aggTime := time.Since(startAgg)
		totalAggTime += aggTime

		                            
		aggPubKeyBytes, err := aggPubKey.MarshalBinary()
		if err != nil {
			logs.Error("failed to marshal agg public key: %v", err)
		}

		                                                      
		allData.Sets[i] = DataSet{
			Message:      msg,
			AggPublicKey: aggPubKeyBytes,
			AggSignature: aggSig,
		}
	}

	                        
	file, err := os.Create("bls_data.json")
	if err != nil {
		logs.Error("failed to create file: %v", err)
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(allData); err != nil {
		logs.Error("failed to encode JSON: %v", err)
	}

	fmt.Printf("Successfully generated %d groups of aggregated data.\n", numSets)
	fmt.Printf("Total aggregator time for %d groups: %v\n", numSets, totalAggTime)
	fmt.Printf("Average aggregator time per group: %v\n", totalAggTime/time.Duration(numSets))
	fmt.Println("Data stored in bls_data.json")
}
