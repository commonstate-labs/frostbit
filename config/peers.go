package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
)

const (
	PeerManifestSchemaVersion = 1
	PeerManifestKind          = "public-peer-manifest"
)

                                                                          
                                 
type PeerManifest struct {
	SchemaVersion      int                       `json:"schema_version"`
	Kind               string                    `json:"kind"`
	Notice             string                    `json:"_notice,omitempty"`
	ChainID            uint64                    `json:"chain_id"`
	GenesisValidators  []GenesisValidatorConfig  `json:"genesis_validators"`
	BootstrapEndpoints []BootstrapEndpointConfig `json:"bootstrap_endpoints"`
}

                                                                       
                                                                  
                                                                         
                                                                       
           
type GenesisValidatorConfig struct {
	Address       string `json:"address"`
	RewardAddress string `json:"reward_address"`
	SecpPublicKey string `json:"secp_public_key,omitempty"`
	BLSPublicKey  string `json:"bls_public_key,omitempty"`
	IsMiner       bool   `json:"is_miner"`
}

                                                                            
                                                                              
                                                                             
                                                                                
                                         
type BootstrapEndpointConfig struct {
	Address  string `json:"address"`
	Endpoint string `json:"endpoint"`
}

                                                                             
                                                                            
                                               
type PeerConfig struct {
	Address       string `json:"address"`
	RewardAddress string `json:"reward_address"`
	SecpPublicKey string `json:"secp_public_key,omitempty"`
	BLSPublicKey  string `json:"bls_public_key,omitempty"`
	Port          string `json:"port,omitempty"`
	AdvertiseAddr string `json:"advertise_addr,omitempty"`
	IsMiner       bool   `json:"is_miner"`
}

func LoadPeerManifest(path string) (*PeerManifest, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("peers manifest path is empty")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read peers file %s: %w", path, err)
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, fmt.Errorf("peers manifest %s is empty", path)
	}
	if strings.HasPrefix(trimmed, "[") {
		return nil, fmt.Errorf("peers manifest %s must be a versioned JSON object, not an array", path)
	}
	var manifest PeerManifest
	if err := decodePeersJSON(data, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse peers file %s: %w", path, err)
	}
	if manifest.SchemaVersion != PeerManifestSchemaVersion {
		return nil, fmt.Errorf("peers manifest %s schema_version=%d, want %d", path, manifest.SchemaVersion, PeerManifestSchemaVersion)
	}
	if manifest.Kind != PeerManifestKind {
		return nil, fmt.Errorf("peers manifest %s kind=%q, want %q", path, manifest.Kind, PeerManifestKind)
	}
	notice := strings.ToUpper(strings.TrimSpace(manifest.Notice))
	if notice == "" || !strings.Contains(notice, "NO SECRETS") {
		return nil, fmt.Errorf("peers manifest %s _notice must be non-empty and contain NO SECRETS", path)
	}
	if manifest.ChainID == 0 {
		return nil, fmt.Errorf("peers manifest %s chain_id must be positive", path)
	}
	if manifest.GenesisValidators == nil {
		return nil, fmt.Errorf("peers manifest %s must contain genesis_validators", path)
	}
	if manifest.BootstrapEndpoints == nil {
		return nil, fmt.Errorf("peers manifest %s must contain bootstrap_endpoints", path)
	}
	validatorAddresses := make(map[string]struct{}, len(manifest.GenesisValidators))
	for i, validator := range manifest.GenesisValidators {
		address := strings.TrimSpace(validator.Address)
		rewardAddress := strings.TrimSpace(validator.RewardAddress)
		if address == "" || rewardAddress == "" || strings.TrimSpace(validator.SecpPublicKey) == "" {
			return nil, fmt.Errorf("peers manifest %s genesis_validators[%d] requires address, reward_address, and secp_public_key", path, i)
		}
		if !validator.IsMiner {
			return nil, fmt.Errorf("peers manifest %s genesis_validators[%d] must set is_miner=true", path, i)
		}
		if strings.TrimSpace(validator.BLSPublicKey) == "" {
			return nil, fmt.Errorf("peers manifest %s genesis_validators[%d] requires bls_public_key", path, i)
		}
		if _, exists := validatorAddresses[address]; exists {
			return nil, fmt.Errorf("peers manifest %s contains duplicate genesis validator %s", path, address)
		}
		validatorAddresses[address] = struct{}{}
	}
	routeAddresses := make(map[string]struct{}, len(manifest.BootstrapEndpoints))
	routeEndpoints := make(map[string]struct{}, len(manifest.BootstrapEndpoints))
	for i, route := range manifest.BootstrapEndpoints {
		address := strings.TrimSpace(route.Address)
		endpoint := strings.TrimSpace(route.Endpoint)
		if _, exists := validatorAddresses[address]; !exists {
			return nil, fmt.Errorf("peers manifest %s bootstrap_endpoints[%d] address %s is not a genesis validator", path, i, address)
		}
		_, port, err := net.SplitHostPort(endpoint)
		if err != nil {
			return nil, fmt.Errorf("peers manifest %s bootstrap_endpoints[%d].endpoint must be host:port: %w", path, i, err)
		}
		portNumber, err := strconv.Atoi(port)
		if err != nil || portNumber < 1 || portNumber > 65535 {
			return nil, fmt.Errorf("peers manifest %s bootstrap_endpoints[%d] port must be in 1..65535", path, i)
		}
		if _, exists := routeAddresses[address]; exists {
			return nil, fmt.Errorf("peers manifest %s contains duplicate bootstrap address %s", path, address)
		}
		if _, exists := routeEndpoints[endpoint]; exists {
			return nil, fmt.Errorf("peers manifest %s contains duplicate bootstrap endpoint %s", path, endpoint)
		}
		routeAddresses[address] = struct{}{}
		routeEndpoints[endpoint] = struct{}{}
	}
	if len(routeAddresses) != len(validatorAddresses) {
		for address := range validatorAddresses {
			if _, exists := routeAddresses[address]; !exists {
				return nil, fmt.Errorf("peers manifest %s genesis validator %s has no bootstrap endpoint", path, address)
			}
		}
	}
	return &manifest, nil
}

                                                                          
                                                       
func LoadPeers(path string) ([]PeerConfig, error) {
	manifest, err := LoadPeerManifest(path)
	if err != nil {
		return nil, err
	}
	routes := make(map[string]string, len(manifest.BootstrapEndpoints))
	for _, route := range manifest.BootstrapEndpoints {
		routes[strings.TrimSpace(route.Address)] = strings.TrimSpace(route.Endpoint)
	}
	peers := make([]PeerConfig, 0, len(manifest.GenesisValidators))
	for _, validator := range manifest.GenesisValidators {
		endpoint := routes[strings.TrimSpace(validator.Address)]
		port := ""
		if _, parsedPort, splitErr := net.SplitHostPort(endpoint); splitErr == nil {
			port = parsedPort
		}
		peers = append(peers, PeerConfig{
			Address:       validator.Address,
			RewardAddress: validator.RewardAddress,
			SecpPublicKey: validator.SecpPublicKey,
			BLSPublicKey:  validator.BLSPublicKey,
			Port:          port,
			AdvertiseAddr: endpoint,
			IsMiner:       validator.IsMiner,
		})
	}
	return peers, nil
}

func decodePeersJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}
