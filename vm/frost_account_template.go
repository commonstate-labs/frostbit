package vm

import (
	"fmt"
	"sort"

	"dex/frost/chain"
	"dex/frost/chain/evm"
	"dex/frost/runtime/planning"
	"dex/keys"
	"dex/pb"
)

type planningStateViewReader struct {
	sv StateView
}

func (r *planningStateViewReader) Get(key string) ([]byte, bool, error) {
	return r.sv.Get(key)
}

func (r *planningStateViewReader) Scan(prefix string, fn func(k string, v []byte) bool) error {
	kvs, err := r.sv.Scan(prefix)
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(kvs))
	for k := range kvs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !fn(k, kvs[k]) {
			break
		}
	}
	return nil
}

func (r *planningStateViewReader) GetMany(keys []string) (map[string][]byte, error) {
	if getter, ok := r.sv.(interface {
		GetMany([]string) (map[string][]byte, error)
	}); ok {
		return getter.GetMany(keys)
	}
	out := make(map[string][]byte, len(keys))
	for _, key := range keys {
		if key == "" {
			continue
		}
		v, exists, err := r.sv.Get(key)
		if err != nil {
			return nil, err
		}
		if exists {
			out[key] = v
		}
	}
	return out, nil
}

func buildCanonicalAccountTemplate(
	sv StateView,
	chainName string,
	asset string,
	vaultID uint32,
	keyEpoch uint64,
	withdrawIDs []string,
) (*chain.TemplateResult, uint64, error) {
	normalizedChain := chain.NormalizeChain(chainName)
	adapter, err := accountChainTemplateAdapter(sv, normalizedChain)
	if err != nil {
		return nil, 0, err
	}

	reader := &planningStateViewReader{sv: sv}
	params, firstSeq, err := planning.BuildAccountTemplateParams(reader, normalizedChain, asset, vaultID, keyEpoch, withdrawIDs, nil)
	if err != nil {
		return nil, 0, err
	}

	result, err := adapter.BuildWithdrawTemplate(params)
	if err != nil {
		return nil, 0, err
	}
	return result, firstSeq, nil
}

func accountChainTemplateAdapter(sv StateView, chainName string) (chain.ChainAdapter, error) {
	normalized := chain.NormalizeChain(chainName)
	switch normalized {
	case chain.ChainETH, chain.ChainBNB, chain.ChainBASE, chain.ChainARB, chain.ChainTRX:
		return evm.NewEVMAdapter(normalized, evmChainIDFromState(sv, normalized)), nil
	default:
		return nil, fmt.Errorf("unsupported account template chain: %s", chainName)
	}
}

func evmChainIDFromState(sv StateView, chainName string) uint64 {
	if sv == nil {
		return evm.DefaultChainID(chainName)
	}
	data, exists, err := sv.Get(keys.KeyFrostVaultConfig(chain.NormalizeChain(chainName), 0))
	if err != nil || !exists || len(data) == 0 {
		return evm.DefaultChainID(chainName)
	}
	var cfg pb.FrostVaultConfig
	if err := unmarshalProtoCompat(data, &cfg); err != nil || cfg.EvmChainId == 0 {
		return evm.DefaultChainID(chainName)
	}
	return cfg.EvmChainId
}
