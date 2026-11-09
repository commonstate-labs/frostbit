                                                                              
                                                                          
package custodyevidence

type Get func(string) ([]byte, bool, error)

type BTCOutputs struct {
	TxID          string   `json:"txid"`
	InternalVouts []uint32 `json:"internal_vouts"`
	KnownArtifact bool     `json:"known_artifact"`
}
