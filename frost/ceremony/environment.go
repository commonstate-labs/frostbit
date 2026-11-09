package ceremony

import (
	"crypto/sha256"
	"encoding/json"
	"errors"

	"dex/config"
)

                                                                               
                                                                                
func ComputePeerManifestDigest(manifest *config.PeerManifest) ([]byte, error) {
	if manifest == nil || manifest.ChainID == 0 {
		return nil, errors.New("invalid peer manifest for ceremony")
	}
	return environmentDigest("DEX_CEREMONY_PEERS_V1", manifest)
}

                                                                             
                                                                               
                                                                               
func ComputeFrostConfigDigest(cfg config.FrostConfig) ([]byte, error) {
	if !cfg.Enabled || len(cfg.Chains) == 0 {
		return nil, errors.New("FROST must be enabled for a ceremony")
	}
	copyCfg := cfg
	copyCfg.Guardian.Endpoint = ""
	copyCfg.GenesisDeploymentManifest = nil
	copyCfg.Chains = make(map[string]config.ChainFrostConfig, len(cfg.Chains))
	for name, chainCfg := range cfg.Chains {
		chainCfg.VaultRefs = nil
		chainCfg.InitialGroupPubkeys = nil
		chainCfg.DepositDexChainID = 0
		chainCfg.DepositProtocol = 0
		chainCfg.AuthorityProtocol = 0
		copyCfg.Chains[name] = chainCfg
	}
	return environmentDigest("DEX_CEREMONY_FROST_CONFIG_V1", &copyCfg)
}

                                                                         
                                                                        
                                                                           
func ComputeConfiguredFrostConfigDigest(cfg config.FrostConfig) ([]byte, error) {
	if !cfg.Enabled || len(cfg.Chains) == 0 {
		return nil, errors.New("FROST must be enabled for configured genesis")
	}
	copyCfg := cfg
	copyCfg.Guardian.Endpoint = ""
	copyCfg.GenesisDeploymentManifest = nil
	return environmentDigest("DEX_CONFIGURED_FROST_CONFIG_V1", &copyCfg)
}

func environmentDigest(domain string, value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(domain + "\x00"))
	_, _ = hash.Write(encoded)
	return hash.Sum(nil), nil
}
