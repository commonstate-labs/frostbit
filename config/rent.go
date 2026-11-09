package config

import (
	bridgeasset "dex/frost/asset"
	"fmt"
)

                                                                              
                                                                          
type RentExemptAsset struct {
	Token      string
	MinimumRaw string
}

var builtInRentExemptAssets = buildRentExemptAssets()

                                                                                
                                                                            
                                                  
func DefaultRentExemptAssets() []RentExemptAsset {
	return append([]RentExemptAsset(nil), builtInRentExemptAssets...)
}

func buildRentExemptAssets() []RentExemptAsset {
	dai, err := bridgeasset.Resolve("eth", "0x6B175474E89094C44Da98b954EedeAC495271d0F")
	if err != nil {
		panic(err)
	}
	assets := make([]RentExemptAsset, 0, 15)
	for _, token := range DefaultGenesisConfig().Tokens {
		if token.Type != "BRIDGED" {
			continue
		}
		var minimum string
		if token.NativeContract != "" {
			minimum = "10000000"                                          
			if token.NativeChain == "bnb" || token.Address == dai.ID {
				minimum = "10000000000000000000"
			}
		} else {
			switch token.NativeChain {
			case "btc":
				minimum = "13000"
			case "eth", "base", "arb":
				minimum = "4000000000000000"
			case "bnb":
				minimum = "14000000000000000"
			case "trx":
				minimum = "30000000"
			default:
				panic(fmt.Sprintf("missing built-in rent threshold for %s", token.Address))
			}
		}
		assets = append(assets, RentExemptAsset{Token: token.Address, MinimumRaw: minimum})
	}
	return assets
}
