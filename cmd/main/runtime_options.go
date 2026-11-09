package main

import (
	"bytes"
	"dex/config"
	"dex/utils"
	"encoding/hex"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
)

type runtimeOptions struct {
	Env                         string
	ConfigDir                   string
	CheckConfig                 bool
	UpgradeInfo                 bool
	AutoUpdate                  bool
	AutoUpdateDir               string
	AutoUpdateInterval          time.Duration
	AutoUpdateGHPath            string
	RetainGenesisProof          bool
	BootstrapMode               string
	PeersPath                   string
	KeyManager                  *utils.KeyManager
	PrivateKeyFD                int
	KeystorePath                string
	KeystoreImportFrom          string
	DeleteKeystoreImportSource  bool
	keystoreImportCleanup       *keystoreImportCleanup
	KeystorePasswordFD          int
	EnableArbitrage             bool
	ArbitrageKeyManager         *utils.KeyManager
	ArbitragePrivateKeyFD       int
	WindowsLocalSecretDir       string
	WindowsArbitrageSecretDir   string
	AllowWindowsLocalSecretDir  bool
	FrostWrapKeyFD              int
	FrostWrapKey                []byte
	FrostGenesisSharesDir       string
	FrostPlaintextGenesisShares bool
	ListenAddr                  string
	AdvertiseAddr               string
	ExpectedAddress             string
	ExpectedArbitrageAddress    string
	DataDir                     string
	BasePort                    int
	Resume                      bool
	NoSimulator                 bool
	AutoRegisterWitness         bool
	SimulatorInSingle           bool
	SimulatorPartitions         int
	SimulatorTargetTxsPerBlock  int
	SimulatorTransfers          bool
	SimulatorRateBlockSeconds   int
	EnableOperatorControlAPI    bool
	EnableDebugAPI              bool
	EnablePprof                 bool
	PprofBind                   string
	FrostArchiveNode            bool
	ArchiveDBDir                string
}

func parseRuntimeOptions() runtimeOptions {
	var opts runtimeOptions
	flag.StringVar(&opts.Env, "env", os.Getenv(config.EnvVarName), "runtime env name; defaults to DEX_ENV or dev")
	flag.StringVar(&opts.ConfigDir, "config-dir", "", "directory containing config.json/genesis.json/frost_default.json/witness_default.json/peers.json")
	flag.BoolVar(&opts.CheckConfig, "check-config", false, "validate config/frost/witness files and exit without secrets, logs, databases, or network listeners")
	flag.BoolVar(&opts.UpgradeInfo, "upgrade-info", false, "print public binary upgrade compatibility as JSON and exit without secrets, logs, databases, or network listeners")
	flag.BoolVar(&opts.AutoUpdate, "auto-update", true, "check and prepare compatible official GitHub releases inside the node process")
	flag.StringVar(&opts.AutoUpdateDir, "auto-update-dir", "", "dedicated update cache; defaults to <data-dir>/.node-updates or a per-instance user cache")
	flag.DurationVar(&opts.AutoUpdateInterval, "auto-update-interval", 10*time.Minute, "background GitHub update check interval (at least one minute)")
	flag.StringVar(&opts.AutoUpdateGHPath, "auto-update-gh-path", "/usr/bin/gh", "absolute path to GitHub CLI supporting release verify-asset")
	flag.BoolVar(&opts.RetainGenesisProof, "retain-genesis-proof", os.Getenv("DEX_RETAIN_GENESIS_PROOF") == "1", "resume an existing finalized database using its original signed genesis proof; requires an exact persisted genesis marker")
	flag.StringVar(&opts.BootstrapMode, "bootstrap-mode", "genesis", "bootstrap mode: genesis or latejoin")
	flag.StringVar(&opts.PeersPath, "peers", "", "path to peers.json; in latejoin it must retain the complete public genesis-validator manifest")
	flag.IntVar(&opts.PrivateKeyFD, "private-key-fd", -1, "alternative to --keystore: inherited Unix FD containing this node's private key as hex")
	flag.StringVar(&opts.KeystorePath, "keystore", "", "encrypted Geth V3 keystore; unlock from a real terminal or --keystore-password-fd")
	flag.StringVar(&opts.KeystoreImportFrom, "keystore-import-from", "", "import this private hex file when --keystore is absent on disk, then verify and start")
	flag.BoolVar(&opts.DeleteKeystoreImportSource, "delete-keystore-import-source", false, "on Linux, delete the matching import source after the unlocked keystore and node startup are verified")
	flag.IntVar(&opts.KeystorePasswordFD, "keystore-password-fd", -1, "inherited Unix FD containing the keystore passphrase; never a passphrase value")
	flag.BoolVar(&opts.EnableArbitrage, "enable-arbitrage", false, "enable node-local proposer arbitrage for this process; requires an independent runtime private key")
	flag.IntVar(&opts.ArbitragePrivateKeyFD, "arbitrage-private-key-fd", -1, "optional inherited Unix FD containing the independent arbitrage account private key as hex")
	flag.StringVar(&opts.WindowsLocalSecretDir, "windows-local-secret-dir", "", "Windows-only local debug directory under the repository .local tree containing exactly one <address>.key file")
	flag.StringVar(&opts.WindowsArbitrageSecretDir, "windows-arbitrage-secret-dir", "", "Windows-only local debug directory under the repository .local tree containing the independent arbitrage <address>.key file")
	flag.BoolVar(&opts.AllowWindowsLocalSecretDir, "allow-windows-local-secret-dir", false, "explicitly allow the Windows-only local debug secret directory source")
	flag.IntVar(&opts.FrostWrapKeyFD, "frost-wrap-key-fd", -1, "optional inherited Unix FD containing the 32-byte FROST wrap key as hex")
	flag.StringVar(&opts.FrostGenesisSharesDir, "frost-genesis-shares-dir", "", "optional directory containing this node's sealed standalone-DKG genesis shares")
	flag.BoolVar(&opts.FrostPlaintextGenesisShares, "frost-plaintext-genesis-shares", false, "explicit development mode: import unencrypted genesis shares on DEX chain 51025")
	flag.StringVar(&opts.ListenAddr, "listen-addr", "", "optional numeric IPv4 host:port used only for local TCP/UDP socket binding")
	flag.StringVar(&opts.AdvertiseAddr, "advertise-addr", "", "host:port advertised to peers for this node")
	flag.StringVar(&opts.ExpectedAddress, "expected-address", "", "optional public miner address that the runtime private key must derive")
	flag.StringVar(&opts.ExpectedArbitrageAddress, "expected-arbitrage-address", "", "optional public arbitrage account address that the runtime arbitrage private key must derive")
	flag.StringVar(&opts.DataDir, "data-dir", "", "data directory for this node")
	flag.IntVar(&opts.BasePort, "base-port", 0, "base node port; 0 uses config")
	flag.BoolVar(&opts.Resume, "resume", resumeFromExistingProgress, "reuse existing node data")
	flag.BoolVar(&opts.NoSimulator, "no-simulator", false, "do not start the built-in tx simulator")
	flag.BoolVar(&opts.AutoRegisterWitness, "auto-register-witness", true, "automatically stake the configured minimum once for a local genesis validator, independently of the tx simulator")
	flag.BoolVar(&opts.SimulatorInSingle, "simulator-in-single", false, "allow the built-in tx simulator in single-node process mode")
	flag.IntVar(&opts.SimulatorPartitions, "simulator-partitions", 1, "number of independent simulator processes sharing the global tx target")
	flag.IntVar(&opts.SimulatorTargetTxsPerBlock, "simulator-target-txs-per-block", int(simMaxTargetTxsPerBlock), "global simulator transaction target per block (1..5000)")
	flag.BoolVar(&opts.SimulatorTransfers, "simulator-transfers", true, "enable the simulator random-transfer stream")
	flag.IntVar(&opts.SimulatorRateBlockSeconds, "simulator-rate-block-seconds", simDefaultRateBlockSeconds, "simulator-only seconds used to convert the per-block tx target into a send rate (1..60)")
	flag.BoolVar(&opts.EnableOperatorControlAPI, "enable-operator-control-api", false, "enable only explicitly scoped Explorer-token-protected operator controls (simulator, pause, and miner toggle)")
	flag.BoolVar(&opts.EnableDebugAPI, "enable-debug-api", false, "enable sensitive Explorer-token-protected diagnostic HTTP routes")
	flag.BoolVar(&opts.EnablePprof, "enable-pprof", false, "enable the separate pprof HTTP listener")
	flag.StringVar(&opts.PprofBind, "pprof-bind", "", "pprof loopback bind host override; defaults to 127.0.0.1")
	flag.BoolVar(&opts.FrostArchiveNode, "frost-archive-node", false, "retain complete FROST external transaction artifacts and serve them for download")
	flag.StringVar(&opts.ArchiveDBDir, "archive-db-dir", "", "node-local archive database directory; empty uses database config")
	flag.Parse()
	return opts
}

func resolvePeersPath(configDir, explicit string) string {
	if strings.TrimSpace(explicit) != "" {
		return explicit
	}
	return config.ConfigPath(configDir, "peers.json")
}

func loadPeerInfoList(path string) ([]PeerInfo, error) {
	_, peers, err := loadPeerManifestInfo(path)
	return peers, err
}

func loadPeerManifestInfo(path string) (*config.PeerManifest, []PeerInfo, error) {
	manifest, err := config.LoadPeerManifest(path)
	if err != nil {
		return nil, nil, err
	}
	peers, err := publicPeerInfoList(manifest.GenesisValidators, manifest.BootstrapEndpoints)
	if err != nil {
		return nil, nil, err
	}
	return manifest, peers, nil
}

func publicPeerInfoList(validators []config.GenesisValidatorConfig, routes []config.BootstrapEndpointConfig) ([]PeerInfo, error) {
	type routeInfo struct {
		endpoint string
		port     string
	}
	routesByAddress := make(map[string]routeInfo, len(routes))
	endpointOwners := make(map[string]string, len(routes))
	for sourceIndex, route := range routes {
		address := strings.TrimSpace(route.Address)
		endpoint := strings.TrimSpace(route.Endpoint)
		if address == "" || endpoint == "" {
			return nil, fmt.Errorf("bootstrap_endpoints[%d] requires address and endpoint", sourceIndex)
		}
		_, port, err := net.SplitHostPort(endpoint)
		if err != nil {
			return nil, fmt.Errorf("bootstrap_endpoints[%d].endpoint must be host:port: %w", sourceIndex, err)
		}
		portNumber, err := strconv.Atoi(port)
		if err != nil || portNumber < 1 || portNumber > 65535 {
			return nil, fmt.Errorf("bootstrap_endpoints[%d] port must be in 1..65535", sourceIndex)
		}
		if _, exists := routesByAddress[address]; exists {
			return nil, fmt.Errorf("bootstrap_endpoints contains duplicate address %s", address)
		}
		if owner, exists := endpointOwners[endpoint]; exists {
			return nil, fmt.Errorf("bootstrap endpoint %s is assigned to both %s and %s", endpoint, owner, address)
		}
		routesByAddress[address] = routeInfo{endpoint: endpoint, port: port}
		endpointOwners[endpoint] = address
	}

	peers := make([]PeerInfo, 0, len(validators))
	addresses := make(map[string]struct{}, len(validators))
	blsOwners := make(map[string]string, len(validators))
	for sourceIndex, pc := range validators {
		secpPublicKey, err := parseConfiguredSecpPublicKey(pc.SecpPublicKey)
		if err != nil {
			return nil, fmt.Errorf("genesis_validators[%d].secp_public_key invalid: %w", sourceIndex, err)
		}
		blsPublicKey, err := parseConfiguredBLSPublicKey(pc.BLSPublicKey)
		if err != nil {
			return nil, fmt.Errorf("genesis_validators[%d].bls_public_key invalid: %w", sourceIndex, err)
		}
		route := routesByAddress[strings.TrimSpace(pc.Address)]
		peer, err := normalizePeerIdentity(PeerInfo{
			Index:         sourceIndex,
			Address:       pc.Address,
			RewardAddress: pc.RewardAddress,
			SecpPublicKey: secpPublicKey,
			BLSPublicKey:  blsPublicKey,
			Port:          route.port,
			AdvertiseAddr: route.endpoint,
			IsMiner:       pc.IsMiner,
		})
		if err != nil {
			return nil, err
		}
		if _, exists := addresses[peer.Address]; exists {
			return nil, fmt.Errorf("genesis_validators contains duplicate address %s", peer.Address)
		}
		if len(peer.BLSPublicKey) > 0 {
			blsKey := hex.EncodeToString(peer.BLSPublicKey)
			if owner, exists := blsOwners[blsKey]; exists && owner != peer.Address {
				return nil, fmt.Errorf("genesis_validators %s and %s share one BLS public key", owner, peer.Address)
			}
			blsOwners[blsKey] = peer.Address
		}
		addresses[peer.Address] = struct{}{}
		peers = append(peers, peer)
	}
	for address := range routesByAddress {
		if _, exists := addresses[address]; !exists {
			return nil, fmt.Errorf("bootstrap endpoint address %s is not a genesis validator", address)
		}
	}
	sort.Slice(peers, func(i, j int) bool { return peers[i].Address < peers[j].Address })
	for i := range peers {
		peers[i].Index = i
	}
	return peers, nil
}

func parseConfiguredSecpPublicKey(raw string) ([]byte, error) {
	decoded, err := decodeConfiguredPublicKey(raw)
	if err != nil || len(decoded) == 0 {
		return decoded, err
	}
	return canonicalSecpPublicKey(decoded)
}

func parseConfiguredBLSPublicKey(raw string) ([]byte, error) {
	decoded, err := decodeConfiguredPublicKey(raw)
	if err != nil || len(decoded) == 0 {
		return decoded, err
	}
	return canonicalBLSPublicKey(decoded)
}

func decodeConfiguredPublicKey(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	raw = strings.TrimPrefix(strings.TrimPrefix(raw, "0x"), "0X")
	decoded, err := hex.DecodeString(raw)
	if err != nil {
		return nil, err
	}
	if len(decoded) == 0 {
		return nil, fmt.Errorf("empty public key")
	}
	return decoded, nil
}

func canonicalSecpPublicKey(raw []byte) ([]byte, error) {
	pubKey, err := btcec.ParsePubKey(raw)
	if err != nil {
		return nil, err
	}
	return pubKey.SerializeCompressed(), nil
}

func canonicalBLSPublicKey(raw []byte) ([]byte, error) {
	pubKey, err := utils.DeserializePublicKey(raw)
	if err != nil {
		return nil, err
	}
	return utils.SerializePublicKey(pubKey)
}

                                                                              
                                                  
func normalizePeerIdentity(p PeerInfo) (PeerInfo, error) {
	p.Address = strings.TrimSpace(p.Address)
	p.RewardAddress = strings.TrimSpace(p.RewardAddress)
	if p.Address == "" {
		return p, fmt.Errorf("peer %d requires address", p.Index)
	}
	if !p.IsMiner {
		return p, fmt.Errorf("peer %d genesis validator must set is_miner=true", p.Index)
	}
	if !utils.IsDexAddress(p.RewardAddress) {
		return p, fmt.Errorf("peer %d requires canonical reward_address", p.Index)
	}

	if len(p.SecpPublicKey) > 0 {
		canonical, err := canonicalSecpPublicKey(p.SecpPublicKey)
		if err != nil {
			return p, fmt.Errorf("peer %d secp_public_key invalid: %w", p.Index, err)
		}
		p.SecpPublicKey = canonical
	}
	if len(p.BLSPublicKey) > 0 {
		canonical, err := canonicalBLSPublicKey(p.BLSPublicKey)
		if err != nil {
			return p, fmt.Errorf("peer %d bls_public_key invalid: %w", p.Index, err)
		}
		p.BLSPublicKey = canonical
	}

	if len(p.SecpPublicKey) == 0 {
		return p, fmt.Errorf("peer %d requires secp_public_key", p.Index)
	}

	derivedAddress, err := utils.AddressFromSecp256k1PubKey(p.SecpPublicKey)
	if err != nil {
		return p, fmt.Errorf("peer %d address derive failed: %w", p.Index, err)
	}
	if p.Address != "" && p.Address != derivedAddress {
		return p, fmt.Errorf("peer %d address does not match secp_public_key", p.Index)
	}
	p.Address = derivedAddress
	if len(p.BLSPublicKey) == 0 {
		return p, fmt.Errorf("peer %d genesis validator requires bls_public_key", p.Index)
	}
	return p, nil
}

                                                                                   
func deriveLocalPeerIdentity(km *utils.KeyManager) (PeerInfo, error) {
	if km == nil || !km.HasPrivateKey() {
		return PeerInfo{}, fmt.Errorf("node runtime identity is missing")
	}
	blsPublicKey, err := km.BLSPublicKeyBytes()
	if err != nil {
		return PeerInfo{}, fmt.Errorf("derive node BLS public key: %w", err)
	}
	return PeerInfo{Address: km.GetAddress(), SecpPublicKey: km.PublicKeyBytes(), BLSPublicKey: blsPublicKey}, nil
}

func validateLocalPeerIdentity(configured PeerInfo, local PeerInfo) error {
	if configured.Address != local.Address {
		return fmt.Errorf("node private key derives address %s, which is not manifest identity %s", local.Address, configured.Address)
	}
	if !bytes.Equal(configured.SecpPublicKey, local.SecpPublicKey) {
		return fmt.Errorf("node private key does not match manifest secp_public_key for %s", configured.Address)
	}
	if len(configured.BLSPublicKey) > 0 && !bytes.Equal(configured.BLSPublicKey, local.BLSPublicKey) {
		return fmt.Errorf("node private key does not match manifest bls_public_key for %s", configured.Address)
	}
	return nil
}

func validateExpectedAddress(expected, derived string) error {
	return validateExpectedAddressForRole(expected, derived, "--expected-address", "node private key")
}

func validateExpectedArbitrageAddress(expected, derived string) error {
	return validateExpectedAddressForRole(expected, derived, "--expected-arbitrage-address", "arbitrage private key")
}

func validateIndependentArbitrageAddress(minerAddress, arbitrageAddress string) error {
	minerAddress = strings.TrimSpace(minerAddress)
	arbitrageAddress = strings.TrimSpace(arbitrageAddress)
	if minerAddress == "" || arbitrageAddress == "" {
		return fmt.Errorf("miner and arbitrage addresses must both be present")
	}
	if minerAddress == arbitrageAddress {
		return fmt.Errorf("arbitrage account address must differ from miner address")
	}
	return nil
}

func validateExpectedAddressForRole(expected, derived, flagName, keyLabel string) error {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return nil
	}
	if !utils.IsDexAddress(expected) {
		return fmt.Errorf("%s must be a canonical DEX address", flagName)
	}
	if expected != derived {
		return fmt.Errorf("%s derives address %s, not %s %s", keyLabel, derived, flagName, expected)
	}
	return nil
}

func nodeDataPath(base, address string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		return filepath.Join("data", address)
	}
	return base
}
