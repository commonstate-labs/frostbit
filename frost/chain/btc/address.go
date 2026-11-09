package btc

import (
	"fmt"
	"strings"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
)

                                                                             
                                               
func NetworkParams(network string) (*chaincfg.Params, error) {
	switch strings.ToLower(strings.TrimSpace(network)) {
	case "mainnet", "main":
		return &chaincfg.MainNetParams, nil
	case "testnet", "testnet3", "test":
		return &chaincfg.TestNet3Params, nil
	case "regtest", "regression":
		return &chaincfg.RegressionNetParams, nil
	case "signet":
		return &chaincfg.SigNetParams, nil
	default:
		return nil, fmt.Errorf("unsupported bitcoin network: %s", network)
	}
}

                                                                              
                                                                        
func NormalizeAddressForNetwork(raw, network string) (string, []byte, error) {
	params, err := NetworkParams(network)
	if err != nil {
		return "", nil, err
	}
	addr, err := btcutil.DecodeAddress(strings.TrimSpace(raw), params)
	if err != nil || !addr.IsForNet(params) {
		return "", nil, fmt.Errorf("invalid bitcoin address for %s", network)
	}
	script, err := txscript.PayToAddrScript(addr)
	if err != nil {
		return "", nil, fmt.Errorf("unsupported bitcoin address: %w", err)
	}
	return addr.EncodeAddress(), script, nil
}

func AddressScriptForNetwork(raw, network string) ([]byte, error) {
	_, script, err := NormalizeAddressForNetwork(raw, network)
	return script, err
}

                                                                            
                                                                             
                                                                             
func AddressScript(raw string) ([]byte, error) {
	for _, network := range []string{"mainnet", "testnet3", "regtest", "signet"} {
		if script, err := AddressScriptForNetwork(raw, network); err == nil {
			return script, nil
		}
	}
	return nil, fmt.Errorf("invalid bitcoin address")
}
