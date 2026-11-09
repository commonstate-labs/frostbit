                                                                              
                                                                               
                                             
package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"dex/config"
	"dex/frost/ceremony"
	"dex/utils"
	"dex/vm"

	"github.com/btcsuite/btcd/btcec/v2"
)

const (
	publicManifestNotice   = "PUBLIC NETWORK MANIFEST — NO SECRETS. Generated node keys are stored separately under secrets/ and must never be committed or reused for valuable networks."
	identityDocumentKind   = "operator-local-node-identities"
	identityDocumentNotice = "OPERATOR-LOCAL PUBLIC NODE IDENTITY MAP — NO PRIVATE KEYS. Private keys are stored separately under secrets/ and must never be committed."
	identityRoleGenesis    = "genesis-validator"
	identityRoleLateJoin   = "latejoin"
	identityDocumentSchema = 1
)

type generatedIdentity struct {
	validator config.GenesisValidatorConfig
	endpoint  config.BootstrapEndpointConfig
	secret    []byte
}

                                                                              
                                                                               
                                                                             
                                                                       
type identityDocument struct {
	SchemaVersion         int                      `json:"schema_version"`
	Kind                  string                   `json:"kind"`
	Notice                string                   `json:"_notice"`
	ChainID               uint64                   `json:"chain_id"`
	GenesisValidatorCount int                      `json:"genesis_validator_count"`
	Nodes                 []identityDocumentRecord `json:"nodes"`
}

type identityDocumentRecord struct {
	Slot     int    `json:"slot"`
	Address  string `json:"address"`
	Endpoint string `json:"endpoint"`
	Role     string `json:"role"`
}

type runtimeConfigDocument struct {
	Network  runtimeNetworkConfig          `json:"Network"`
	TxPool   *runtimeTxPoolProfileConfig   `json:"TxPool,omitempty"`
	Sender   *runtimeSenderProfileConfig   `json:"Sender,omitempty"`
	PeerGate *runtimePeerGateProfileConfig `json:"PeerGate,omitempty"`
}

type runtimeNetworkConfig struct {
	BasePort        int    `json:"BasePort"`
	MaxNodes        int    `json:"MaxNodes"`
	DefaultNumNodes int    `json:"DefaultNumNodes"`
	ByzantineNodes  int    `json:"ByzantineNodes"`
	AdvertiseHost   string `json:"advertise_host"`
}

func main() {
	var (
		outputDir                 = flag.String("output-dir", ".local/network", "new operator-local configuration directory")
		validators                = flag.Int("validators", 4, "number of genesis validators")
		identities                = flag.Int("identities", 0, "total number of local node identities; defaults to --validators")
		chainID                   = flag.Uint64("chain-id", 0, "required positive chain ID for the new network")
		host                      = flag.String("advertise-host", "127.0.0.1", "default bootstrap host or IP (without port)")
		basePort                  = flag.Int("base-port", 6000, "first default bootstrap port")
		endpoints                 = flag.String("endpoints", "", "optional comma-separated exact host:port list; must match --identities (or --validators when omitted)")
		validateDir               = flag.String("validate-dir", "", "read-only validation of an existing generated network directory")
		frostInputConfig          = flag.String("frost-config", "", "public FROST config containing the fixed guardian keys; required for physical20-frost-stress")
		profileName               = flag.String("runtime-profile", string(runtimeProfileGeneric), "runtime profile: generic, physical20-stress, or physical20-frost-stress")
		applyProfileDir           = flag.String("apply-runtime-profile-dir", "", "atomically apply an explicit runtime profile to an existing public config.json")
		prepareFrostCeremonyDir   = flag.String("prepare-frost-ceremony-dir", "", "atomically migrate one exact legacy physical20 FROST profile for the standalone genesis ceremony")
		validateFrostFinalFile    = flag.String("validate-frost-final-file", "", "read-only validation of a standalone-ceremony final frost_default.json")
		validateFrostRebuildFrom  = flag.String("validate-frost-rebuild-from", "", "with --validate-frost-final-file, verify a genesis rebuild preserves the current phase-one ceremony")
		genesisAllocFile          = flag.String("genesis-allocations-file", "", "optional strict public manifest of ordinary address-level genesis balances; never contains private keys")
		fundTailArbitrageAccounts = flag.Bool("fund-tail-arbitrage-accounts", false, "fund the final --validators identities as non-miner arbitrage accounts (physical20-frost-stress only)")
	)
	flag.Parse()
	profile, err := parseRuntimeProfile(*profileName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "network-init: %v\n", err)
		os.Exit(1)
	}
	applyProfileRequested := false
	prepareFrostCeremonyRequested := false
	validateFrostFinalRequested := false
	validateFrostRebuildRequested := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "apply-runtime-profile-dir" {
			applyProfileRequested = true
		}
		if f.Name == "prepare-frost-ceremony-dir" {
			prepareFrostCeremonyRequested = true
		}
		if f.Name == "validate-frost-final-file" {
			validateFrostFinalRequested = true
		}
		if f.Name == "validate-frost-rebuild-from" {
			validateFrostRebuildRequested = true
		}
	})
	if validateFrostRebuildRequested && !validateFrostFinalRequested {
		fmt.Fprintln(os.Stderr, "network-init: --validate-frost-rebuild-from requires --validate-frost-final-file")
		os.Exit(1)
	}
	if applyProfileRequested {
		allowed := map[string]bool{"apply-runtime-profile-dir": true, "runtime-profile": true}
		var incompatible string
		flag.Visit(func(f *flag.Flag) {
			if !allowed[f.Name] && incompatible == "" {
				incompatible = f.Name
			}
		})
		if incompatible != "" {
			fmt.Fprintf(os.Stderr, "network-init: --apply-runtime-profile-dir cannot be combined with --%s\n", incompatible)
			os.Exit(1)
		}
		if err := applyRuntimeProfileToExistingDirectory(*applyProfileDir, profile); err != nil {
			fmt.Fprintf(os.Stderr, "network-init: %v\n", err)
			os.Exit(1)
		}
		abs, _ := filepath.Abs(filepath.Join(*applyProfileDir, "config.json"))
		fmt.Printf("Applied runtime profile %s to public runtime config %s (no secrets read or printed)\n", profile, abs)
		return
	}
	if prepareFrostCeremonyRequested {
		allowed := map[string]bool{"prepare-frost-ceremony-dir": true, "runtime-profile": true}
		var incompatible string
		flag.Visit(func(f *flag.Flag) {
			if !allowed[f.Name] && incompatible == "" {
				incompatible = f.Name
			}
		})
		if incompatible != "" {
			fmt.Fprintf(os.Stderr, "network-init: --prepare-frost-ceremony-dir cannot be combined with --%s\n", incompatible)
			os.Exit(1)
		}
		if err := prepareFrostConfigForGenesisCeremony(*prepareFrostCeremonyDir, profile); err != nil {
			fmt.Fprintf(os.Stderr, "network-init: %v\n", err)
			os.Exit(1)
		}
		abs, _ := filepath.Abs(filepath.Join(*prepareFrostCeremonyDir, "frost_default.json"))
		fmt.Printf("Prepared public FROST profile for the standalone genesis ceremony at %s (node identities and secrets were not read or changed)\n", abs)
		return
	}
	if validateFrostFinalRequested {
		allowed := map[string]bool{"validate-frost-final-file": true, "validate-frost-rebuild-from": true, "runtime-profile": true, "validators": true}
		var incompatible string
		flag.Visit(func(f *flag.Flag) {
			if !allowed[f.Name] && incompatible == "" {
				incompatible = f.Name
			}
		})
		if incompatible != "" {
			fmt.Fprintf(os.Stderr, "network-init: --validate-frost-final-file cannot be combined with --%s\n", incompatible)
			os.Exit(1)
		}
		var validationErr error
		if validateFrostRebuildRequested {
			validationErr = validateStandaloneFrostRebuild(*validateFrostFinalFile, *validateFrostRebuildFrom, profile, *validators)
		} else {
			validationErr = validateStandaloneFrostFinalFile(*validateFrostFinalFile, profile, *validators)
		}
		if validationErr != nil {
			fmt.Fprintf(os.Stderr, "network-init: %v\n", validationErr)
			os.Exit(1)
		}
		abs, _ := filepath.Abs(*validateFrostFinalFile)
		fmt.Printf("Validated standalone-ceremony final FROST config %s read-only\n", abs)
		return
	}
	if strings.TrimSpace(*validateDir) != "" {
		allowed := map[string]bool{"validate-dir": true, "validators": true, "identities": true, "chain-id": true, "runtime-profile": true, "genesis-allocations-file": true, "fund-tail-arbitrage-accounts": true}
		var incompatible string
		flag.Visit(func(f *flag.Flag) {
			if !allowed[f.Name] && incompatible == "" {
				incompatible = f.Name
			}
		})
		if incompatible != "" {
			fmt.Fprintf(os.Stderr, "network-init: --validate-dir cannot be combined with --%s\n", incompatible)
			os.Exit(1)
		}
		identityCount, err := normalizedIdentityCount(*validators, *identities)
		if err != nil {
			fmt.Fprintf(os.Stderr, "network-init: %v\n", err)
			os.Exit(1)
		}
		if err := validateExistingNetworkTopologyWithProfileAndTailArbitrageFunding(*validateDir, *validators, identityCount, *chainID, profile, *fundTailArbitrageAccounts); err != nil {
			fmt.Fprintf(os.Stderr, "network-init: %v\n", err)
			os.Exit(1)
		}
		expectedAllocations, err := loadPublicGenesisAllocations(*genesisAllocFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "network-init: %v\n", err)
			os.Exit(1)
		}
		if len(expectedAllocations) > 0 {
			if err := validatePublicGenesisAllocationsMatchWithTailArbitrageFunding(*validateDir, expectedAllocations, profile == runtimeProfilePhysical20FrostStress, *fundTailArbitrageAccounts); err != nil {
				fmt.Fprintf(os.Stderr, "network-init: %v\n", err)
				os.Exit(1)
			}
		}
		abs, _ := filepath.Abs(*validateDir)
		fmt.Printf("Validated operator-local network directory %s read-only (no secrets printed)\n", abs)
		return
	}

	identityCount, err := normalizedIdentityCount(*validators, *identities)
	if err != nil {
		fmt.Fprintf(os.Stderr, "network-init: %v\n", err)
		os.Exit(1)
	}
	ordinaryAllocations, err := loadPublicGenesisAllocations(*genesisAllocFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "network-init: %v\n", err)
		os.Exit(1)
	}
	guardianInput, err := loadGeneratorGuardian(*frostInputConfig, profile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "network-init: %v\n", err)
		os.Exit(1)
	}
	if err := generateNetworkTopologyWithProfileAndAllocationsAndTailArbitrageFunding(*outputDir, *validators, identityCount, *chainID, *host, *basePort, *endpoints, profile, ordinaryAllocations, *fundTailArbitrageAccounts, guardianInput); err != nil {
		fmt.Fprintf(os.Stderr, "network-init: %v\n", err)
		os.Exit(1)
	}
	abs, _ := filepath.Abs(*outputDir)
	fmt.Printf("Created complete operator-local network configuration in %s\n", abs)
	fmt.Printf("Runtime config directory: %s\n", abs)
	fmt.Printf("Public peer manifest: %s\n", filepath.Join(abs, "peers.json"))
	fmt.Printf("Private node keys: %s (KEEP PRIVATE; never commit)\n", filepath.Join(abs, "secrets"))
}

func validateExistingNetworkDirectory(outputDir string, validatorCount int, chainID uint64) error {
	return validateExistingNetworkTopology(outputDir, validatorCount, validatorCount, chainID)
}

func validateExistingNetworkTopology(outputDir string, validatorCount, identityCount int, chainID uint64) error {
	return validateExistingNetworkTopologyWithProfile(outputDir, validatorCount, identityCount, chainID, runtimeProfileGeneric)
}

func validateExistingNetworkTopologyWithProfile(outputDir string, validatorCount, identityCount int, chainID uint64, profile runtimeProfile) error {
	return validateExistingNetworkTopologyWithProfileAndTailArbitrageFunding(outputDir, validatorCount, identityCount, chainID, profile, false)
}

func validateExistingNetworkTopologyWithProfileAndTailArbitrageFunding(outputDir string, validatorCount, identityCount int, chainID uint64, profile runtimeProfile, fundTailArbitrageAccounts bool) error {
	cleaned := filepath.Clean(strings.TrimSpace(outputDir))
	if cleaned == "." || strings.TrimSpace(outputDir) == "" {
		return fmt.Errorf("--validate-dir must name an operator-local network directory")
	}
	if err := validateTopologyCounts(validatorCount, identityCount); err != nil {
		return err
	}
	if err := validateTailArbitrageFundingRequest(profile, validatorCount, identityCount, fundTailArbitrageAccounts); err != nil {
		return err
	}
	if chainID == 0 {
		return fmt.Errorf("--chain-id must be positive")
	}
	info, err := os.Lstat(cleaned)
	if err != nil {
		return fmt.Errorf("inspect validation directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("--validate-dir must be a real directory, not a file or symlink")
	}
	manifest, err := config.LoadPeerManifest(filepath.Join(cleaned, "peers.json"))
	if err != nil {
		return fmt.Errorf("load existing peer manifest: %w", err)
	}
	if len(manifest.GenesisValidators) != validatorCount {
		return fmt.Errorf("validator count=%d, want %d", len(manifest.GenesisValidators), validatorCount)
	}
	if manifest.ChainID != chainID {
		return fmt.Errorf("chain_id=%d, want %d", manifest.ChainID, chainID)
	}
	return validateGeneratedNetworkTopologyWithProfileAndTailArbitrageFunding(cleaned, validatorCount, identityCount, chainID, profile, fundTailArbitrageAccounts)
}

                                                                         
                                                      
func generateNetwork(outputDir string, validatorCount int, chainID uint64, advertiseHost string, basePort int) error {
	return generateNetworkWithEndpoints(outputDir, validatorCount, chainID, advertiseHost, basePort, "")
}

func generateNetworkWithEndpoints(outputDir string, validatorCount int, chainID uint64, advertiseHost string, basePort int, endpointCSV string) error {
	return generateNetworkTopology(outputDir, validatorCount, validatorCount, chainID, advertiseHost, basePort, endpointCSV)
}

func generateNetworkTopology(outputDir string, validatorCount, identityCount int, chainID uint64, advertiseHost string, basePort int, endpointCSV string) error {
	return generateNetworkTopologyWithProfile(outputDir, validatorCount, identityCount, chainID, advertiseHost, basePort, endpointCSV, runtimeProfileGeneric)
}

func generateNetworkTopologyWithProfile(outputDir string, validatorCount, identityCount int, chainID uint64, advertiseHost string, basePort int, endpointCSV string, profile runtimeProfile, guardians ...config.GuardianConfig) error {
	return generateNetworkTopologyWithProfileAndAllocations(outputDir, validatorCount, identityCount, chainID, advertiseHost, basePort, endpointCSV, profile, nil, guardians...)
}

func generateNetworkTopologyWithProfileAndAllocations(outputDir string, validatorCount, identityCount int, chainID uint64, advertiseHost string, basePort int, endpointCSV string, profile runtimeProfile, ordinaryAllocations map[string]config.GenesisAlloc, guardians ...config.GuardianConfig) error {
	return generateNetworkTopologyWithProfileAndAllocationsAndTailArbitrageFunding(outputDir, validatorCount, identityCount, chainID, advertiseHost, basePort, endpointCSV, profile, ordinaryAllocations, false, guardians...)
}

func generateNetworkTopologyWithProfileAndAllocationsAndTailArbitrageFunding(outputDir string, validatorCount, identityCount int, chainID uint64, advertiseHost string, basePort int, endpointCSV string, profile runtimeProfile, ordinaryAllocations map[string]config.GenesisAlloc, fundTailArbitrageAccounts bool, guardians ...config.GuardianConfig) error {
	cleanOutput, err := cleanNewOutputPath(outputDir)
	if err != nil {
		return err
	}
	if err := validateTopologyCounts(validatorCount, identityCount); err != nil {
		return err
	}
	if err := validateTailArbitrageFundingRequest(profile, validatorCount, identityCount, fundTailArbitrageAccounts); err != nil {
		return err
	}
	if chainID == 0 {
		return fmt.Errorf("--chain-id must be positive")
	}
	if err := ensureOutputAbsent(cleanOutput); err != nil {
		return err
	}

	if err := requireGeneratorGuardian(profile, guardians); err != nil {
		return err
	}
	resolvedEndpoints, runtimeHost, runtimePort, err := resolveEndpoints(identityCount, advertiseHost, basePort, endpointCSV)
	if err != nil {
		return err
	}
	identities := make([]generatedIdentity, 0, identityCount)
	defer func() {
		for i := range identities {
			zeroBytes(identities[i].secret)
		}
	}()
	for index, endpoint := range resolvedEndpoints {
		identity, identityErr := newIdentity(endpoint)
		if identityErr != nil {
			return fmt.Errorf("generate validator %d: %w", index, identityErr)
		}
		identities = append(identities, identity)
	}
	return createDirectoryAtomically(cleanOutput, func(staging string) error {
		if err := writeGeneratedNetworkTopologyWithProfileAndAllocationsAndTailArbitrageFunding(staging, identities, validatorCount, identityCount, chainID, runtimeHost, runtimePort, profile, ordinaryAllocations, fundTailArbitrageAccounts, guardians...); err != nil {
			return err
		}
		return validateGeneratedNetworkTopologyWithProfileAndTailArbitrageFunding(staging, validatorCount, identityCount, chainID, profile, fundTailArbitrageAccounts)
	})
}

func normalizedIdentityCount(validatorCount, requestedIdentityCount int) (int, error) {
	identityCount := requestedIdentityCount
	if identityCount == 0 {
		identityCount = validatorCount
	}
	if err := validateTopologyCounts(validatorCount, identityCount); err != nil {
		return 0, err
	}
	return identityCount, nil
}

func validateTopologyCounts(validatorCount, identityCount int) error {
	if validatorCount < 1 || validatorCount > 1000 {
		return fmt.Errorf("--validators must be in 1..1000")
	}
	if identityCount < validatorCount || identityCount > 1000 {
		return fmt.Errorf("--identities must be in --validators..1000")
	}
	return nil
}

func cleanNewOutputPath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("--output-dir must name a new directory")
	}
	cleaned := filepath.Clean(raw)
	if cleaned == "." || filepath.Dir(cleaned) == cleaned {
		return "", fmt.Errorf("--output-dir must name a new child directory")
	}
	return cleaned, nil
}

func ensureOutputAbsent(outputDir string) error {
	if _, err := os.Lstat(outputDir); err == nil {
		return fmt.Errorf("output directory %s already exists; refusing to overwrite", outputDir)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect output directory: %w", err)
	}
	return nil
}

func createDirectoryAtomically(outputDir string, populate func(string) error) error {
	if err := ensureOutputAbsent(outputDir); err != nil {
		return err
	}
	parent := filepath.Dir(outputDir)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create output parent: %w", err)
	}
	staging, err := os.MkdirTemp(parent, ".network-init-tmp-")
	if err != nil {
		return fmt.Errorf("create temporary output directory: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(staging)
		}
	}()
	if err := populate(staging); err != nil {
		return err
	}
	                                                                          
	                                                                       
	if err := ensureOutputAbsent(outputDir); err != nil {
		return err
	}
	if err := os.Rename(staging, outputDir); err != nil {
		return fmt.Errorf("commit generated network directory: %w", err)
	}
	committed = true
	return nil
}

func resolveEndpoints(validatorCount int, advertiseHost string, basePort int, endpointCSV string) ([]string, string, int, error) {
	endpointCSV = strings.TrimSpace(endpointCSV)
	if endpointCSV == "" {
		advertiseHost = strings.TrimSpace(advertiseHost)
		if err := validateEndpointHost(advertiseHost); err != nil {
			return nil, "", 0, fmt.Errorf("--advertise-host: %w", err)
		}
		if basePort < 1 || basePort > 65535-(validatorCount-1) {
			return nil, "", 0, fmt.Errorf("generated ports must remain in 1..65535")
		}
		endpoints := make([]string, validatorCount)
		for index := range endpoints {
			endpoints[index] = net.JoinHostPort(advertiseHost, strconv.Itoa(basePort+index))
		}
		return endpoints, advertiseHost, basePort, nil
	}

	parts := strings.Split(endpointCSV, ",")
	if len(parts) != validatorCount {
		return nil, "", 0, fmt.Errorf("--endpoints count=%d must exactly match --validators=%d", len(parts), validatorCount)
	}
	endpoints := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	runtimeHost := ""
	runtimePort := 0
	for index, part := range parts {
		endpoint := strings.TrimSpace(part)
		host, port, err := parseEndpoint(endpoint)
		if err != nil {
			return nil, "", 0, fmt.Errorf("--endpoints item %d: %w", index, err)
		}
		identity := canonicalEndpointIdentity(host, port)
		if _, exists := seen[identity]; exists {
			return nil, "", 0, fmt.Errorf("--endpoints item %d duplicates another endpoint", index)
		}
		seen[identity] = struct{}{}
		if index == 0 {
			runtimeHost = host
			runtimePort = port
		}
		endpoints = append(endpoints, endpoint)
	}
	return endpoints, runtimeHost, runtimePort, nil
}

func parseEndpoint(endpoint string) (string, int, error) {
	if endpoint == "" {
		return "", 0, fmt.Errorf("endpoint is empty")
	}
	host, portText, err := net.SplitHostPort(endpoint)
	if err != nil {
		return "", 0, fmt.Errorf("endpoint must be host:port (IPv6 must use brackets): %w", err)
	}
	if err := validateEndpointHost(host); err != nil {
		return "", 0, err
	}
	if portText == "" {
		return "", 0, fmt.Errorf("port is empty")
	}
	for _, char := range portText {
		if char < '0' || char > '9' {
			return "", 0, fmt.Errorf("port must contain decimal digits only")
		}
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("port must be in 1..65535")
	}
	return host, port, nil
}

func validateEndpointHost(host string) error {
	if host == "" {
		return fmt.Errorf("host is empty")
	}
	if strings.TrimSpace(host) != host {
		return fmt.Errorf("host contains surrounding whitespace")
	}
	if net.ParseIP(host) != nil {
		return nil
	}
	if len(host) > 253 {
		return fmt.Errorf("host is too long")
	}
	hostname := strings.TrimSuffix(host, ".")
	if hostname == "" {
		return fmt.Errorf("host is empty")
	}
	for _, label := range strings.Split(hostname, ".") {
		if len(label) < 1 || len(label) > 63 {
			return fmt.Errorf("host contains an invalid DNS label")
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("host contains an invalid DNS label")
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '-' {
				return fmt.Errorf("host contains an invalid character")
			}
		}
	}
	return nil
}

func canonicalEndpointIdentity(host string, port int) string {
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	} else {
		host = strings.ToLower(strings.TrimSuffix(host, "."))
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

func writeGeneratedNetwork(outputDir string, identities []generatedIdentity, validatorCount int, chainID uint64, runtimeHost string, runtimePort int) error {
	return writeGeneratedNetworkTopology(outputDir, identities, validatorCount, validatorCount, chainID, runtimeHost, runtimePort)
}

func writeGeneratedNetworkTopology(outputDir string, identities []generatedIdentity, validatorCount, identityCount int, chainID uint64, runtimeHost string, runtimePort int) error {
	return writeGeneratedNetworkTopologyWithProfile(outputDir, identities, validatorCount, identityCount, chainID, runtimeHost, runtimePort, runtimeProfileGeneric)
}

func writeGeneratedNetworkTopologyWithProfile(outputDir string, identities []generatedIdentity, validatorCount, identityCount int, chainID uint64, runtimeHost string, runtimePort int, profile runtimeProfile, guardians ...config.GuardianConfig) error {
	return writeGeneratedNetworkTopologyWithProfileAndAllocations(outputDir, identities, validatorCount, identityCount, chainID, runtimeHost, runtimePort, profile, nil, guardians...)
}

func writeGeneratedNetworkTopologyWithProfileAndAllocations(outputDir string, identities []generatedIdentity, validatorCount, identityCount int, chainID uint64, runtimeHost string, runtimePort int, profile runtimeProfile, ordinaryAllocations map[string]config.GenesisAlloc, guardians ...config.GuardianConfig) error {
	return writeGeneratedNetworkTopologyWithProfileAndAllocationsAndTailArbitrageFunding(outputDir, identities, validatorCount, identityCount, chainID, runtimeHost, runtimePort, profile, ordinaryAllocations, false, guardians...)
}

func writeGeneratedNetworkTopologyWithProfileAndAllocationsAndTailArbitrageFunding(outputDir string, identities []generatedIdentity, validatorCount, identityCount int, chainID uint64, runtimeHost string, runtimePort int, profile runtimeProfile, ordinaryAllocations map[string]config.GenesisAlloc, fundTailArbitrageAccounts bool, guardians ...config.GuardianConfig) error {
	if err := validateTopologyCounts(validatorCount, identityCount); err != nil {
		return err
	}
	if err := validateTailArbitrageFundingRequest(profile, validatorCount, identityCount, fundTailArbitrageAccounts); err != nil {
		return err
	}
	if len(identities) != identityCount {
		return fmt.Errorf("generated identity count=%d, want %d", len(identities), identityCount)
	}
	secretDir := filepath.Join(outputDir, "secrets")
	if err := os.Mkdir(secretDir, 0o700); err != nil {
		return fmt.Errorf("create secret directory: %w", err)
	}

	manifest := config.PeerManifest{
		SchemaVersion:      config.PeerManifestSchemaVersion,
		Kind:               config.PeerManifestKind,
		Notice:             publicManifestNotice,
		ChainID:            chainID,
		GenesisValidators:  make([]config.GenesisValidatorConfig, 0, validatorCount),
		BootstrapEndpoints: make([]config.BootstrapEndpointConfig, 0, validatorCount),
	}
	identityMap := identityDocument{
		SchemaVersion:         identityDocumentSchema,
		Kind:                  identityDocumentKind,
		Notice:                identityDocumentNotice,
		ChainID:               chainID,
		GenesisValidatorCount: validatorCount,
		Nodes:                 make([]identityDocumentRecord, 0, identityCount),
	}
	genesis, err := defaultGenesisForRuntimeProfile(profile)
	if err != nil {
		return err
	}
	genesis.ChainID = chainID
	genesis.Alloc = make(map[string]config.GenesisAlloc, validatorCount)
	for slot, identity := range identities {
		secretData := make([]byte, len(identity.secret)+1)
		copy(secretData, identity.secret)
		secretData[len(secretData)-1] = '\n'
		secretPath := filepath.Join(secretDir, identity.validator.Address+".key")
		writeErr := writeExclusive(secretPath, secretData, 0o600)
		zeroBytes(secretData)
		if writeErr != nil {
			return fmt.Errorf("write node secret for %s: %w", identity.validator.Address, writeErr)
		}
		role := identityRoleLateJoin
		if slot < validatorCount {
			role = identityRoleGenesis
		}
		identityMap.Nodes = append(identityMap.Nodes, identityDocumentRecord{
			Slot:     slot,
			Address:  identity.validator.Address,
			Endpoint: identity.endpoint.Endpoint,
			Role:     role,
		})
	}

	                                                                          
	                                                                           
	                               
	genesisIdentities := append([]generatedIdentity(nil), identities[:validatorCount]...)
	sort.Slice(genesisIdentities, func(i, j int) bool {
		return genesisIdentities[i].validator.Address < genesisIdentities[j].validator.Address
	})
	for _, identity := range genesisIdentities {
		manifest.GenesisValidators = append(manifest.GenesisValidators, identity.validator)
		manifest.BootstrapEndpoints = append(manifest.BootstrapEndpoints, identity.endpoint)
		allocation, err := genesisAllocationForRuntimeProfile(profile, genesis.Tokens, validatorCount)
		if err != nil {
			return err
		}
		genesis.Alloc[identity.validator.Address] = allocation
	}
	if fundTailArbitrageAccounts {
		allocation, err := tailArbitrageAllocationForRuntimeProfile(profile, genesis.Tokens)
		if err != nil {
			return err
		}
		for _, identity := range identities[identityCount-validatorCount:] {
			if _, exists := genesis.Alloc[identity.validator.Address]; exists {
				return fmt.Errorf("tail arbitrage identity %s overlaps an existing genesis allocation", identity.validator.Address)
			}
			genesis.Alloc[identity.validator.Address] = cloneGenesisAllocation(allocation)
		}
	}
	excludedIdentities := make(map[string]struct{}, len(identities))
	for _, identity := range identities {
		excludedIdentities[identity.validator.Address] = struct{}{}
	}
	if err := mergePublicGenesisAllocations(genesis, excludedIdentities, ordinaryAllocations, profile == runtimeProfilePhysical20FrostStress); err != nil {
		return fmt.Errorf("merge public genesis allocations: %w", err)
	}
	if err := validateGenesisAllocationsWithinSupply(genesis); err != nil {
		return fmt.Errorf("public genesis allocations exceed token supply: %w", err)
	}

	defaults := config.DefaultConfig()
	maxNodes := defaults.Network.MaxNodes
	if identityCount > maxNodes {
		maxNodes = identityCount
	}
	runtimeConfig := runtimeConfigDocument{Network: runtimeNetworkConfig{
		BasePort:        runtimePort,
		MaxNodes:        maxNodes,
		DefaultNumNodes: identityCount,
		ByzantineNodes:  0,
		AdvertiseHost:   runtimeHost,
	}}
	if err := applyRuntimeProfileToDocument(&runtimeConfig, profile); err != nil {
		return err
	}
	frostConfig, err := frostConfigForRuntimeProfile(profile, guardians...)
	if err != nil {
		return err
	}
	witnessConfig, err := witnessConfigForRuntimeProfile(profile)
	if err != nil {
		return err
	}

	files := []struct {
		name  string
		value any
	}{
		{"config.json", runtimeConfig},
		{"genesis.json", genesis},
		{"frost_default.json", frostConfig},
		{"witness_default.json", witnessConfig},
		{"peers.json", manifest},
		{"identities.json", identityMap},
	}
	for _, file := range files {
		if err := writeJSONExclusive(filepath.Join(outputDir, file.name), file.value, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", file.name, err)
		}
	}
	return nil
}

func validateGeneratedNetworkDirectory(outputDir string, validatorCount int, chainID uint64) error {
	return validateGeneratedNetworkTopology(outputDir, validatorCount, validatorCount, chainID)
}

func validateGeneratedNetworkTopology(outputDir string, validatorCount, identityCount int, chainID uint64) error {
	return validateGeneratedNetworkTopologyWithProfile(outputDir, validatorCount, identityCount, chainID, runtimeProfileGeneric)
}

func validateGeneratedNetworkTopologyWithProfile(outputDir string, validatorCount, identityCount int, chainID uint64, profile runtimeProfile) error {
	return validateGeneratedNetworkTopologyWithProfileAndTailArbitrageFunding(outputDir, validatorCount, identityCount, chainID, profile, false)
}

func validateGeneratedNetworkTopologyWithProfileAndTailArbitrageFunding(outputDir string, validatorCount, identityCount int, chainID uint64, profile runtimeProfile, fundTailArbitrageAccounts bool) error {
	if err := validateTopologyCounts(validatorCount, identityCount); err != nil {
		return err
	}
	if err := validateTailArbitrageFundingRequest(profile, validatorCount, identityCount, fundTailArbitrageAccounts); err != nil {
		return err
	}
	runtimeConfig, err := config.LoadFromDir(outputDir)
	if err != nil {
		return fmt.Errorf("validate generated runtime config: %w", err)
	}
	if runtimeConfig.Witness.Enabled {
		return fmt.Errorf("generated runtime config must disable Witness")
	}
	if runtimeConfig.Network.DefaultNumNodes != identityCount {
		return fmt.Errorf("generated runtime node count=%d, want %d", runtimeConfig.Network.DefaultNumNodes, identityCount)
	}
	if err := validateRuntimeProfileConfig(runtimeConfig, profile); err != nil {
		return fmt.Errorf("validate generated runtime profile: %w", err)
	}
	frostConfig, err := config.LoadFrostConfig(filepath.Join(outputDir, "frost_default.json"))
	if err != nil {
		return fmt.Errorf("validate generated FROST config: %w", err)
	}
	if err := validateFrostConfigForRuntimeProfile(frostConfig, profile, validatorCount); err != nil {
		return fmt.Errorf("validate generated FROST profile: %w", err)
	}
	witnessConfig, err := config.LoadWitnessConfig(filepath.Join(outputDir, "witness_default.json"))
	if err != nil {
		return fmt.Errorf("validate generated Witness config: %w", err)
	}
	if witnessConfig.Enabled {
		return fmt.Errorf("generated Witness config must be disabled")
	}
	if err := validateWitnessConfigForRuntimeProfile(witnessConfig, profile); err != nil {
		return fmt.Errorf("validate generated Witness profile: %w", err)
	}
	genesis, err := config.LoadGenesisConfig(filepath.Join(outputDir, "genesis.json"))
	if err != nil {
		return fmt.Errorf("validate generated genesis config: %w", err)
	}
	manifest, err := config.LoadPeerManifest(filepath.Join(outputDir, "peers.json"))
	if err != nil {
		return fmt.Errorf("validate generated peer manifest: %w", err)
	}
	if genesis.EffectiveChainID() != chainID || manifest.ChainID != chainID || genesis.EffectiveChainID() != manifest.ChainID {
		return fmt.Errorf("generated chain IDs are inconsistent")
	}
	if err := config.ValidateFreshFrostGenesisStakePolicy(frostConfig, manifest, genesis); err != nil {
		return fmt.Errorf("validate generated FROST genesis ranking: %w", err)
	}
	if _, hasCeremonyResults := frostConfigWithoutCeremonyResults(frostConfig); hasCeremonyResults {
		if err := ceremony.ValidateConfiguredGenesisDeploymentEnvironment(frostConfig, manifest, chainID, utils.VerifyECDSASignature); err != nil {
			return fmt.Errorf("validate generated signed FROST deployment proof: %w", err)
		}
	}
	if len(manifest.GenesisValidators) != validatorCount || len(manifest.BootstrapEndpoints) != validatorCount {
		return fmt.Errorf("generated validator or endpoint count is inconsistent")
	}
	if _, exists := genesis.Alloc["default"]; exists {
		return fmt.Errorf("generated genesis must use address-level allocations and must not contain default")
	}
	if len(genesis.Alloc) < validatorCount {
		return fmt.Errorf("generated genesis allocation count=%d, want at least %d", len(genesis.Alloc), validatorCount)
	}
	identities, err := loadIdentityDocumentForValidation(outputDir, manifest, validatorCount, identityCount, chainID)
	if err != nil {
		return err
	}
	tailArbitrageSet := make(map[string]struct{})
	if fundTailArbitrageAccounts {
		for _, identity := range identities[identityCount-validatorCount:] {
			tailArbitrageSet[identity.Address] = struct{}{}
		}
	}
	if profile == runtimeProfilePhysical20FrostStress {
		if err := validatePhysical20FrostStressGenesisWithTailArbitrageFunding(genesis, manifest.GenesisValidators, tailArbitrageSet); err != nil {
			return fmt.Errorf("validate generated FROST stress genesis profile: %w", err)
		}
	}
	validatorSet := make(map[string]struct{}, len(manifest.GenesisValidators))
	for _, validator := range manifest.GenesisValidators {
		validatorSet[validator.Address] = struct{}{}
	}
	excludedIdentities := make(map[string]struct{}, len(identities))
	for _, identity := range identities {
		excludedIdentities[identity.Address] = struct{}{}
	}
	ordinaryAllocations := make(map[string]config.GenesisAlloc)
	for address, allocation := range genesis.Alloc {
		if _, validator := validatorSet[address]; validator {
			continue
		}
		if _, tailArbitrage := tailArbitrageSet[address]; tailArbitrage {
			continue
		}
		ordinaryAllocations[address] = allocation
	}
	if err := validateOrdinaryGenesisAllocations(ordinaryAllocations, genesis.Tokens, excludedIdentities, profile == runtimeProfilePhysical20FrostStress); err != nil {
		return fmt.Errorf("validate ordinary genesis allocations: %w", err)
	}
	if err := validateGenesisAllocationsWithinSupply(genesis); err != nil {
		return fmt.Errorf("generated genesis allocations exceed token supply: %w", err)
	}

	secretEntries, err := os.ReadDir(filepath.Join(outputDir, "secrets"))
	if err != nil {
		return fmt.Errorf("validate generated secrets directory: %w", err)
	}
	if len(secretEntries) != identityCount {
		return fmt.Errorf("generated secret count=%d, want %d", len(secretEntries), identityCount)
	}
	secretNames := make(map[string]struct{}, len(secretEntries))
	for _, entry := range secretEntries {
		secretNames[entry.Name()] = struct{}{}
	}
	minimumStake := new(big.Int).Set(vm.MinMinerStake)
	for _, validator := range manifest.GenesisValidators {
		allocation, exists := genesis.Alloc[validator.Address]
		if !exists {
			return fmt.Errorf("genesis allocation is missing for validator %s", validator.Address)
		}
		fb, exists := allocation.Balances["FB"]
		if !exists {
			return fmt.Errorf("genesis FB allocation is missing for validator %s", validator.Address)
		}
		locked, ok := new(big.Int).SetString(strings.TrimSpace(fb.MinerLockedBalance), 10)
		if !ok || locked.Cmp(minimumStake) < 0 {
			return fmt.Errorf("genesis miner stake is invalid for validator %s", validator.Address)
		}
	}
	for _, identity := range identities {
		secretName := identity.Address + ".key"
		if _, exists := secretNames[secretName]; !exists {
			return fmt.Errorf("generated secret is missing for node %s", identity.Address)
		}
		delete(secretNames, secretName)
		secretPath := filepath.Join(outputDir, "secrets", secretName)
		if err := validateGeneratedSecret(secretPath, identity.Address); err != nil {
			return err
		}
	}
	if len(secretNames) != 0 {
		return fmt.Errorf("generated secrets directory contains unexpected entries")
	}
	return nil
}

func loadIdentityDocumentForValidation(outputDir string, manifest *config.PeerManifest, validatorCount, identityCount int, chainID uint64) ([]identityDocumentRecord, error) {
	path := filepath.Join(outputDir, "identities.json")
	document, err := loadIdentityDocument(path)
	if err != nil {
		                                                                         
		                                                                     
		                                                                             
		if !os.IsNotExist(err) || identityCount != validatorCount {
			return nil, fmt.Errorf("validate generated identity map: %w", err)
		}
		routes := make(map[string]string, len(manifest.BootstrapEndpoints))
		for _, route := range manifest.BootstrapEndpoints {
			routes[route.Address] = route.Endpoint
		}
		legacy := make([]identityDocumentRecord, 0, validatorCount)
		for slot, validator := range manifest.GenesisValidators {
			legacy = append(legacy, identityDocumentRecord{Slot: slot, Address: validator.Address, Endpoint: routes[validator.Address], Role: identityRoleGenesis})
		}
		return legacy, nil
	}
	if document.SchemaVersion != identityDocumentSchema || document.Kind != identityDocumentKind {
		return nil, fmt.Errorf("identity map schema or kind is invalid")
	}
	if notice := strings.ToUpper(strings.TrimSpace(document.Notice)); notice == "" || !strings.Contains(notice, "NO PRIVATE KEYS") {
		return nil, fmt.Errorf("identity map _notice must be non-empty and contain NO PRIVATE KEYS")
	}
	if document.ChainID != chainID {
		return nil, fmt.Errorf("identity map chain_id=%d, want %d", document.ChainID, chainID)
	}
	if document.GenesisValidatorCount != validatorCount {
		return nil, fmt.Errorf("identity map genesis_validator_count=%d, want %d", document.GenesisValidatorCount, validatorCount)
	}
	if len(document.Nodes) != identityCount {
		return nil, fmt.Errorf("identity map node count=%d, want %d", len(document.Nodes), identityCount)
	}

	validatorSet := make(map[string]struct{}, len(manifest.GenesisValidators))
	for _, validator := range manifest.GenesisValidators {
		validatorSet[validator.Address] = struct{}{}
	}
	routeByAddress := make(map[string]string, len(manifest.BootstrapEndpoints))
	for _, route := range manifest.BootstrapEndpoints {
		routeByAddress[route.Address] = route.Endpoint
	}
	addresses := make(map[string]struct{}, identityCount)
	endpoints := make(map[string]struct{}, identityCount)
	for index, node := range document.Nodes {
		if node.Slot != index {
			return nil, fmt.Errorf("identity map nodes[%d].slot=%d, want %d", index, node.Slot, index)
		}
		if node.Address == "" || strings.TrimSpace(node.Address) != node.Address || !strings.HasPrefix(node.Address, "fbc1") {
			return nil, fmt.Errorf("identity map nodes[%d] has invalid address", index)
		}
		if _, exists := addresses[node.Address]; exists {
			return nil, fmt.Errorf("identity map contains duplicate address %s", node.Address)
		}
		addresses[node.Address] = struct{}{}
		host, port, parseErr := parseEndpoint(node.Endpoint)
		if parseErr != nil || strings.TrimSpace(node.Endpoint) != node.Endpoint {
			return nil, fmt.Errorf("identity map nodes[%d] endpoint is invalid", index)
		}
		endpointKey := canonicalEndpointIdentity(host, port)
		if _, exists := endpoints[endpointKey]; exists {
			return nil, fmt.Errorf("identity map contains duplicate endpoint %s", node.Endpoint)
		}
		endpoints[endpointKey] = struct{}{}

		if index < validatorCount {
			if node.Role != identityRoleGenesis {
				return nil, fmt.Errorf("identity map nodes[%d] must have role %s", index, identityRoleGenesis)
			}
			if _, exists := validatorSet[node.Address]; !exists || routeByAddress[node.Address] != node.Endpoint {
				return nil, fmt.Errorf("identity map genesis node %s does not match peers.json", node.Address)
			}
		} else {
			if node.Role != identityRoleLateJoin {
				return nil, fmt.Errorf("identity map nodes[%d] must have role %s", index, identityRoleLateJoin)
			}
			if _, exists := validatorSet[node.Address]; exists {
				return nil, fmt.Errorf("late-join identity %s is present in genesis validators", node.Address)
			}
			if _, exists := routeByAddress[node.Address]; exists {
				return nil, fmt.Errorf("late-join identity %s is present in bootstrap endpoints", node.Address)
			}
		}
	}
	return document.Nodes, nil
}

func loadIdentityDocument(path string) (*identityDocument, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var document identityDocument
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("unexpected trailing JSON value")
		}
		return nil, err
	}
	return &document, nil
}

func validateGeneratedSecret(path, expectedAddress string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("validate node secret for %s: %w", expectedAddress, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("node secret for %s is not a regular file", expectedAddress)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read node secret for %s: %w", expectedAddress, err)
	}
	defer zeroBytes(data)
	encoded := bytes.TrimSpace(data)
	decoded := make([]byte, hex.DecodedLen(len(encoded)))
	decodedLength, err := hex.Decode(decoded, encoded)
	if err != nil || decodedLength != btcec.PrivKeyBytesLen || len(decoded) != btcec.PrivKeyBytesLen {
		return fmt.Errorf("node secret for %s is not a canonical private key", expectedAddress)
	}
	defer zeroBytes(decoded)
	privateKey, _ := btcec.PrivKeyFromBytes(decoded)
	if !bytes.Equal(privateKey.Serialize(), decoded) {
		return fmt.Errorf("node secret for %s is not a canonical private key", expectedAddress)
	}
	address, err := utils.DeriveDexAddress(privateKey)
	if err != nil || address != expectedAddress {
		return fmt.Errorf("node secret does not match public address %s", expectedAddress)
	}
	return nil
}

func newIdentity(endpoint string) (generatedIdentity, error) {
	if _, _, err := parseEndpoint(endpoint); err != nil {
		return generatedIdentity{}, err
	}
	privateKey, err := btcec.NewPrivateKey()
	if err != nil {
		return generatedIdentity{}, err
	}
	secretBytes := privateKey.Serialize()
	defer zeroBytes(secretBytes)
	secretHex := make([]byte, hex.EncodedLen(len(secretBytes)))
	hex.Encode(secretHex, secretBytes)
	address, err := utils.DeriveDexAddress(privateKey)
	if err != nil {
		zeroBytes(secretHex)
		return generatedIdentity{}, err
	}
	blsPoint, err := utils.GetPublicKey(privateKey.ToECDSA())
	if err != nil {
		zeroBytes(secretHex)
		return generatedIdentity{}, err
	}
	blsPublicKey, err := utils.SerializePublicKey(blsPoint)
	if err != nil {
		zeroBytes(secretHex)
		return generatedIdentity{}, err
	}
	return generatedIdentity{
		validator: config.GenesisValidatorConfig{
			Address:       address,
			RewardAddress: address,
			SecpPublicKey: hex.EncodeToString(privateKey.PubKey().SerializeCompressed()),
			BLSPublicKey:  hex.EncodeToString(blsPublicKey),
			IsMiner:       true,
		},
		endpoint: config.BootstrapEndpointConfig{
			Address:  address,
			Endpoint: endpoint,
		},
		secret: secretHex,
	}, nil
}

func writeJSONExclusive(path string, value any, mode os.FileMode) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeExclusive(path, append(encoded, '\n'), mode)
}

func writeExclusive(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func zeroBytes(data []byte) {
	for index := range data {
		data[index] = 0
	}
}
