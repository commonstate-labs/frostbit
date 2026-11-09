package vm

import (
	"fmt"
	"math/big"
	"strings"

	bridgeasset "dex/frost/asset"
	frostchain "dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/frost/chain/evm"
	"dex/keys"
	"dex/pb"

	"github.com/btcsuite/btcd/btcutil"
)

                                                                          
                                                                             
                                                                              
                                                                        
func validateFrostWithdrawAdmission(
	sv StateView,
	chainName string,
	tokenAddr string,
	destination string,
	token *pb.Token,
	amount *big.Int,
) (string, error) {
	chainName = frostchain.NormalizeChain(chainName)
	if token == nil || token.Type != pb.TokenType_TOKEN_BRIDGED {
		return "", fmt.Errorf("withdraw token must be bridged")
	}
	if strings.TrimSpace(token.Address) == "" || token.Address != tokenAddr {
		return "", fmt.Errorf("token record address mismatch")
	}
	if frostchain.NormalizeChain(token.NativeChain) != chainName {
		return "", fmt.Errorf("token native chain mismatch")
	}
	identity, err := bridgeasset.Resolve(chainName, token.NativeContract)
	if err != nil {
		return "", err
	}
	if identity.ID != tokenAddr || token.NativeChain != identity.Chain || token.NativeContract != identity.Contract {
		return "", fmt.Errorf("token record is not canonically addressed")
	}
	indexed, exists, err := sv.Get(keys.KeyNativeTokenIndex(identity.Chain, identity.Contract))
	if err != nil {
		return "", err
	}
	if !exists || string(indexed) != identity.ID {
		return "", fmt.Errorf("token native index mismatch")
	}
	if amount == nil || amount.Sign() <= 0 {
		return "", fmt.Errorf("withdraw amount must be positive")
	}

	cfg, err := loadFrostVaultConfig(sv, chainName)
	if err != nil {
		return "", err
	}
	if frostchain.NormalizeChain(cfg.Chain) != chainName {
		return "", fmt.Errorf("vault config chain mismatch for %s", chainName)
	}
	if cfg.VaultCount == 0 {
		return "", fmt.Errorf("vault config has no vaults for %s", chainName)
	}

	switch chainName {
	case frostchain.ChainBTC:
		return validateBTCWithdrawAdmission(cfg, tokenAddr, token, destination, amount)
	case frostchain.ChainETH, frostchain.ChainBNB, frostchain.ChainBASE, frostchain.ChainARB, frostchain.ChainTRX:
		return validateEVMWithdrawAdmission(cfg, chainName, tokenAddr, token, destination)
	default:
		return "", fmt.Errorf("withdrawals are not implemented for chain %s", chainName)
	}
}

func validateBTCWithdrawAdmission(
	cfg *pb.FrostVaultConfig,
	tokenAddr string,
	token *pb.Token,
	destination string,
	amount *big.Int,
) (string, error) {
	if cfg.SignAlgo != frostchain.SignAlgoBTC {
		return "", fmt.Errorf("invalid BTC vault signing algorithm")
	}
	if strings.TrimSpace(token.NativeContract) != "" {
		return "", fmt.Errorf("unsupported BTC withdrawal asset %s", tokenAddr)
	}
	if _, err := btc.NetworkParams(cfg.BtcNetwork); err != nil {
		return "", err
	}
	if !amount.IsUint64() || amount.Uint64() > btcutil.MaxSatoshi {
		return "", fmt.Errorf("BTC withdrawal amount exceeds max money")
	}
	if amount.Uint64() < btc.MinimumWithdrawAmountSats {
		return "", fmt.Errorf("BTC withdrawal amount is below minimum: got %d sat, minimum %d sat", amount.Uint64(), btc.MinimumWithdrawAmountSats)
	}
	canonical, _, err := btc.NormalizeAddressForNetwork(destination, cfg.BtcNetwork)
	if err != nil {
		return "", err
	}
	return canonical, nil
}

func validateEVMWithdrawAdmission(
	cfg *pb.FrostVaultConfig,
	chainName string,
	tokenAddr string,
	token *pb.Token,
	destination string,
) (string, error) {
	if cfg.SignAlgo != frostchain.SignAlgoETH {
		return "", fmt.Errorf("invalid EVM vault signing algorithm")
	}
	if cfg.EvmChainId == 0 {
		return "", fmt.Errorf("missing EVM chain ID for %s", chainName)
	}
	if len(cfg.VaultRefs) < int(cfg.VaultCount) {
		return "", fmt.Errorf("missing EVM vault references for %s", chainName)
	}
	for vaultID := uint32(0); vaultID < cfg.VaultCount; vaultID++ {
		if _, err := evm.NormalizeAccountAddress(chainName, cfg.VaultRefs[vaultID], false); err != nil {
			return "", fmt.Errorf("invalid EVM vault reference %d for %s: %w", vaultID, chainName, err)
		}
	}

	contract := strings.TrimSpace(token.NativeContract)
	if contract == "" {
	} else if _, err := evm.NormalizeAccountAddress(chainName, contract, false); err != nil {
		return "", fmt.Errorf("invalid native contract for token %s: %w", tokenAddr, err)
	}

	canonical, err := evm.NormalizeAccountAddress(chainName, destination, false)
	if err != nil {
		return "", err
	}
	return canonical, nil
}
