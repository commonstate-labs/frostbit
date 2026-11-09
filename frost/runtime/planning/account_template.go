package planning

import (
	"fmt"
	"math/big"
	"sort"
	"strings"

	chainpkg "dex/frost/chain"
	"dex/keys"
	"dex/pb"

	"golang.org/x/crypto/sha3"
	"google.golang.org/protobuf/proto"
)

const defaultEVMWithdrawMethodSignature = "withdrawBatch((bytes32,uint256,address,uint256,uint256,address[],(bytes32,address,address,uint256,uint256)[]),(uint256,uint256,uint256),(uint256,uint256,uint256))"

type withdrawTemplateItem struct {
	WithdrawID string
	LiveSeq    uint64
	Asset      string
	To         string
	Gross      string
}

                                                                              
                                                                              
                                                              
func BuildAccountTemplateParams(
	stateReader ChainStateReader,
	chainName string,
	asset string,                                                               
	vaultID uint32,
	keyEpoch uint64,
	withdrawIDs []string,
	amountOverrides map[string]string,
) (chainpkg.WithdrawTemplateParams, uint64, error) {
	if len(withdrawIDs) == 0 || len(withdrawIDs) > chainpkg.MaxWithdrawalsPerBatch {
		return chainpkg.WithdrawTemplateParams{}, 0, fmt.Errorf("invalid withdraw_ids length")
	}
	normalizedChain := chainpkg.NormalizeChain(chainName)
	withdrawKeys := make([]string, len(withdrawIDs))
	for i, wid := range withdrawIDs {
		withdrawKeys[i] = keys.KeyFrostWithdraw(wid)
	}
	withdrawValues, err := getMany(stateReader, withdrawKeys)
	if err != nil {
		return chainpkg.WithdrawTemplateParams{}, 0, err
	}
	items := make([]withdrawTemplateItem, 0, len(withdrawIDs))
	tokenKeys := make([]string, 0, len(withdrawIDs))
	tokenSeen := make(map[string]struct{}, len(withdrawIDs))
	for i, wid := range withdrawIDs {
		data := withdrawValues[withdrawKeys[i]]
		if len(data) == 0 {
			return chainpkg.WithdrawTemplateParams{}, 0, fmt.Errorf("withdraw not found: %s", wid)
		}
		var state pb.FrostWithdrawState
		if err := proto.Unmarshal(data, &state); err != nil {
			return chainpkg.WithdrawTemplateParams{}, 0, fmt.Errorf("parse withdraw state %s: %w", wid, err)
		}
		if state.WithdrawId != wid || chainpkg.NormalizeChain(state.Chain) != normalizedChain || state.VaultId != vaultID || state.LiveSeq == 0 || strings.TrimSpace(state.To) == "" {
			return chainpkg.WithdrawTemplateParams{}, 0, fmt.Errorf("withdraw scope mismatch: %s", wid)
		}
		gross, err := chainpkg.ParseWithdrawAmount(state.GrossAmount)
		if err != nil {
			return chainpkg.WithdrawTemplateParams{}, 0, fmt.Errorf("invalid withdraw gross for %s: %w", wid, err)
		}
		if amountOverrides != nil {
			if override, ok := amountOverrides[wid]; ok {
				gross, err = chainpkg.ParseWithdrawAmount(override)
				if err != nil {
					return chainpkg.WithdrawTemplateParams{}, 0, fmt.Errorf("invalid withdraw amount override for %s: %w", wid, err)
				}
			}
		}
		fee := new(big.Int).Quo(new(big.Int).Add(new(big.Int).Set(gross), big.NewInt(9999)), big.NewInt(10000))
		net := new(big.Int).Sub(new(big.Int).Set(gross), fee)
		if state.FeeVersion != 1 || state.FeeAmount != fee.String() || state.NetAmount != net.String() || net.Sign() <= 0 {
			return chainpkg.WithdrawTemplateParams{}, 0, fmt.Errorf("withdraw fee fields mismatch: %s", wid)
		}
		items = append(items, withdrawTemplateItem{WithdrawID: wid, LiveSeq: state.LiveSeq, Asset: state.Asset, To: state.To, Gross: gross.String()})
		if _, ok := tokenSeen[state.Asset]; !ok {
			tokenSeen[state.Asset] = struct{}{}
			tokenKeys = append(tokenKeys, keys.KeyToken(state.Asset))
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].LiveSeq == items[j].LiveSeq {
			return items[i].WithdrawID < items[j].WithdrawID
		}
		return items[i].LiveSeq < items[j].LiveSeq
	})
	for i := 1; i < len(items); i++ {
		if items[i].LiveSeq <= items[i-1].LiveSeq {
			return chainpkg.WithdrawTemplateParams{}, 0, fmt.Errorf("duplicate or unordered live sequence")
		}
	}

	tokenValues, err := getMany(stateReader, tokenKeys)
	if err != nil {
		return chainpkg.WithdrawTemplateParams{}, 0, err
	}
	nativeContracts := make(map[string]string, len(tokenSeen))
	for tokenAddr := range tokenSeen {
		data := tokenValues[keys.KeyToken(tokenAddr)]
		if len(data) == 0 {
			return chainpkg.WithdrawTemplateParams{}, 0, fmt.Errorf("token not found: %s", tokenAddr)
		}
		var token pb.Token
		if proto.Unmarshal(data, &token) != nil || token.Address != tokenAddr || token.Type != pb.TokenType_TOKEN_BRIDGED || chainpkg.NormalizeChain(token.NativeChain) != normalizedChain {
			return chainpkg.WithdrawTemplateParams{}, 0, fmt.Errorf("token metadata mismatch: %s", tokenAddr)
		}
		nativeContracts[tokenAddr] = strings.TrimSpace(token.NativeContract)
	}

	sortedWithdrawIDs := make([]string, len(items))
	outputs := make([]chainpkg.WithdrawOutput, len(items))
	for i, item := range items {
		sortedWithdrawIDs[i] = item.WithdrawID
		outputs[i] = chainpkg.WithdrawOutput{WithdrawID: item.WithdrawID, To: item.To, Amount: item.Gross, Token: nativeContracts[item.Asset]}
	}
	batchSeqData, exists, err := stateReader.Get(keys.KeyFrostVaultDEXNextBatchSeq(normalizedChain, vaultID))
	if err != nil {
		return chainpkg.WithdrawTemplateParams{}, 0, err
	}
	batchSeq := "1"
	if exists && len(batchSeqData) > 0 {
		batchSeq = string(batchSeqData)
	}
	params := chainpkg.WithdrawTemplateParams{
		Chain: normalizedChain, VaultID: vaultID, KeyEpoch: keyEpoch,
		WithdrawIDs: sortedWithdrawIDs, Outputs: outputs,
		Nonce: items[0].LiveSeq, BatchSeq: batchSeq,
	}
	if isEVMAccountChain(normalizedChain) {
		cfg, err := resolveVaultRefConfig(stateReader, normalizedChain, vaultID)
		if err != nil {
			return chainpkg.WithdrawTemplateParams{}, 0, err
		}
		if cfg.AuthorityProtocol != 3 || cfg.DepositDexChainId == 0 || len(cfg.GuardianPubkey) != 64 {
			return chainpkg.WithdrawTemplateParams{}, 0, fmt.Errorf("invalid dual-authority protocol config")
		}
		params.AuthorityProtocol = 3
		params.DexChainID = cfg.DepositDexChainId
		params.GuardianPubkey = append([]byte(nil), cfg.GuardianPubkey...)
		params.ContractAddr = strings.TrimSpace(cfg.VaultRefs[vaultID])
		params.MethodID = computeMethodID(defaultEVMWithdrawMethodSignature)
	}
	return params, items[0].LiveSeq, nil
}

func isEVMAccountChain(chainName string) bool {
	switch chainpkg.NormalizeChain(chainName) {
	case chainpkg.ChainETH, chainpkg.ChainBNB, chainpkg.ChainBASE, chainpkg.ChainARB, chainpkg.ChainTRX:
		return true
	default:
		return false
	}
}

func resolveAssetNativeContract(stateReader ChainStateReader, chainName, asset string) (string, error) {
	if strings.TrimSpace(asset) == "" {
		return "", nil
	}
	data, exists, err := stateReader.Get(keys.KeyToken(asset))
	if err != nil {
		return "", err
	}
	if !exists || len(data) == 0 {
		return "", nil
	}
	var token pb.Token
	if err := proto.Unmarshal(data, &token); err != nil {
		return "", fmt.Errorf("parse token %s: %w", asset, err)
	}
	if token.Type != pb.TokenType_TOKEN_BRIDGED {
		return "", nil
	}
	tokenChain := chainpkg.NormalizeChain(token.NativeChain)
	if tokenChain != "" && tokenChain != chainpkg.NormalizeChain(chainName) {
		return "", fmt.Errorf("token native chain mismatch: asset=%s token_chain=%s withdraw_chain=%s", asset, token.NativeChain, chainName)
	}
	return strings.TrimSpace(token.NativeContract), nil
}

func resolveVaultRefConfig(stateReader ChainStateReader, chainName string, vaultID uint32) (*pb.FrostVaultConfig, error) {
	raw, found, err := stateReader.Get(keys.KeyFrostVaultConfig(chainName, 0))
	if err != nil {
		return nil, err
	}
	var cfg pb.FrostVaultConfig
	if !found || proto.Unmarshal(raw, &cfg) != nil || int(vaultID) >= len(cfg.VaultRefs) || strings.TrimSpace(cfg.VaultRefs[vaultID]) == "" {
		return nil, fmt.Errorf("vault ref/config unavailable for %s/%d", chainName, vaultID)
	}
	return &cfg, nil
}

func computeMethodID(signature string) []byte {
	h := sha3.NewLegacyKeccak256()
	h.Write([]byte(signature))
	sum := h.Sum(nil)
	out := make([]byte, 4)
	copy(out, sum[:4])
	return out
}
