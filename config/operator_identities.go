package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

const (
	OperatorIdentitySchemaVersion = 1
	OperatorIdentityKind          = "operator-local-node-identities"
	OperatorIdentityRoleGenesis   = "genesis-validator"
	OperatorIdentityRoleLateJoin  = "latejoin"
	MaxOperatorIdentities         = 1000
)

                                                                             
                                                                                
                                          
type OperatorIdentityManifest struct {
	SchemaVersion         int                    `json:"schema_version"`
	Kind                  string                 `json:"kind"`
	Notice                string                 `json:"_notice"`
	ChainID               uint64                 `json:"chain_id"`
	GenesisValidatorCount int                    `json:"genesis_validator_count"`
	Nodes                 []OperatorNodeIdentity `json:"nodes"`
}

type OperatorNodeIdentity struct {
	Slot     int    `json:"slot"`
	Address  string `json:"address"`
	Endpoint string `json:"endpoint"`
	Role     string `json:"role"`
}

                                                                           
                                                                              
                                                              
func LoadOperatorIdentityManifest(path string, trusted *PeerManifest) (*OperatorIdentityManifest, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("operator identity path is empty")
	}
	if trusted == nil {
		return nil, fmt.Errorf("trusted peers manifest is required")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect operator identity file %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("operator identity file %s must be a regular non-symlink file", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read operator identity file %s: %w", path, err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return nil, fmt.Errorf("operator identity file %s is empty", path)
	}
	var document OperatorIdentityManifest
	if err := decodePeersJSON(data, &document); err != nil {
		return nil, fmt.Errorf("failed to parse operator identity file %s: %w", path, err)
	}
	if document.SchemaVersion != OperatorIdentitySchemaVersion {
		return nil, fmt.Errorf("operator identity schema_version=%d, want %d", document.SchemaVersion, OperatorIdentitySchemaVersion)
	}
	if document.Kind != OperatorIdentityKind {
		return nil, fmt.Errorf("operator identity kind=%q, want %q", document.Kind, OperatorIdentityKind)
	}
	notice := strings.ToUpper(strings.TrimSpace(document.Notice))
	if notice == "" || !strings.Contains(notice, "NO PRIVATE KEYS") {
		return nil, fmt.Errorf("operator identity _notice must be non-empty and contain NO PRIVATE KEYS")
	}
	if document.ChainID == 0 || document.ChainID != trusted.ChainID {
		return nil, fmt.Errorf("operator identity chain_id=%d does not match trusted peers chain_id=%d", document.ChainID, trusted.ChainID)
	}
	if document.GenesisValidatorCount != len(trusted.GenesisValidators) {
		return nil, fmt.Errorf("operator identity genesis_validator_count=%d, want %d", document.GenesisValidatorCount, len(trusted.GenesisValidators))
	}
	if document.GenesisValidatorCount < 1 {
		return nil, fmt.Errorf("operator identity requires at least one trusted genesis validator")
	}
	if document.Nodes == nil || len(document.Nodes) < document.GenesisValidatorCount || len(document.Nodes) > MaxOperatorIdentities {
		return nil, fmt.Errorf("operator identity node count=%d is outside %d..%d", len(document.Nodes), document.GenesisValidatorCount, MaxOperatorIdentities)
	}

	validatorSet := make(map[string]struct{}, len(trusted.GenesisValidators))
	for _, validator := range trusted.GenesisValidators {
		validatorSet[strings.TrimSpace(validator.Address)] = struct{}{}
	}
	routeByAddress := make(map[string]string, len(trusted.BootstrapEndpoints))
	for _, route := range trusted.BootstrapEndpoints {
		routeByAddress[strings.TrimSpace(route.Address)] = strings.TrimSpace(route.Endpoint)
	}
	addresses := make(map[string]struct{}, len(document.Nodes))
	endpoints := make(map[string]struct{}, len(document.Nodes))
	for index, node := range document.Nodes {
		if node.Slot != index {
			return nil, fmt.Errorf("operator identity nodes[%d].slot=%d, want %d", index, node.Slot, index)
		}
		if !isCanonicalOperatorAddress(node.Address) {
			return nil, fmt.Errorf("operator identity nodes[%d] has invalid address", index)
		}
		if _, exists := addresses[node.Address]; exists {
			return nil, fmt.Errorf("operator identity contains duplicate address %s", node.Address)
		}
		addresses[node.Address] = struct{}{}
		endpointKey, endpointErr := canonicalOperatorEndpoint(node.Endpoint)
		if endpointErr != nil || strings.TrimSpace(node.Endpoint) != node.Endpoint {
			return nil, fmt.Errorf("operator identity nodes[%d] has invalid endpoint", index)
		}
		if _, exists := endpoints[endpointKey]; exists {
			return nil, fmt.Errorf("operator identity contains duplicate endpoint %s", node.Endpoint)
		}
		endpoints[endpointKey] = struct{}{}

		if index < document.GenesisValidatorCount {
			if node.Role != OperatorIdentityRoleGenesis {
				return nil, fmt.Errorf("operator identity nodes[%d] must have role %s", index, OperatorIdentityRoleGenesis)
			}
			if _, exists := validatorSet[node.Address]; !exists || routeByAddress[node.Address] != node.Endpoint {
				return nil, fmt.Errorf("operator identity genesis node %s does not match trusted peers manifest", node.Address)
			}
			continue
		}
		if node.Role != OperatorIdentityRoleLateJoin {
			return nil, fmt.Errorf("operator identity nodes[%d] must have role %s", index, OperatorIdentityRoleLateJoin)
		}
		if _, exists := validatorSet[node.Address]; exists {
			return nil, fmt.Errorf("late-join identity %s is present in genesis validators", node.Address)
		}
		if _, exists := routeByAddress[node.Address]; exists {
			return nil, fmt.Errorf("late-join identity %s is present in bootstrap endpoints", node.Address)
		}
	}
	return &document, nil
}

func isCanonicalOperatorAddress(address string) bool {
	if len(address) != 43 || !strings.HasPrefix(address, "fbc1") || strings.TrimSpace(address) != address {
		return false
	}
	const charset = "023456789acdefghjklmnpqrstuvwxyz"
	for _, char := range address[4:] {
		if !strings.ContainsRune(charset, char) {
			return false
		}
	}
	return true
}

func canonicalOperatorEndpoint(endpoint string) (string, error) {
	host, portText, err := net.SplitHostPort(endpoint)
	if err != nil || strings.TrimSpace(host) == "" {
		return "", fmt.Errorf("endpoint must be host:port")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", fmt.Errorf("endpoint port must be in 1..65535")
	}
	canonicalHost := strings.ToLower(strings.TrimSuffix(host, "."))
	if ip := net.ParseIP(host); ip != nil {
		canonicalHost = ip.String()
	} else if !isCanonicalOperatorDNSName(canonicalHost) {
		return "", fmt.Errorf("endpoint host is not a canonical IP address or DNS name")
	}
	return net.JoinHostPort(canonicalHost, strconv.Itoa(port)), nil
}

func isCanonicalOperatorDNSName(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) < 1 || len(label) > 63 || !isOperatorDNSAlphaNumeric(label[0]) || !isOperatorDNSAlphaNumeric(label[len(label)-1]) {
			return false
		}
		for index := 1; index < len(label)-1; index++ {
			if !isOperatorDNSAlphaNumeric(label[index]) && label[index] != '-' {
				return false
			}
		}
	}
	return true
}

func isOperatorDNSAlphaNumeric(char byte) bool {
	return char >= 'a' && char <= 'z' || char >= '0' && char <= '9'
}
