                                                                            
                                                                            
                                                       
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dex/config"
	"dex/pb"
	"dex/txverify"
	"dex/utils"
	"dex/vm"

	"google.golang.org/protobuf/proto"
)

const maxPrivateKeyBytes = 256

type nonceRequest struct {
	Node          string `json:"node,omitempty"`
	Address       string `json:"address"`
	RequireRemote bool   `json:"require_remote"`
}

type nonceResponse struct {
	Nonce   uint64 `json:"nonce"`
	ChainID uint64 `json:"chain_id"`
	Error   string `json:"error,omitempty"`
}

type submitRequest struct {
	Node string `json:"node,omitempty"`
	Tx   string `json:"tx"`
}

type submitResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type addressRequest struct {
	Node          string `json:"node,omitempty"`
	Address       string `json:"address"`
	RequireRemote bool   `json:"require_remote"`
}

type addressResponse struct {
	Account *minerAccountStatus `json:"account,omitempty"`
	Error   string              `json:"error,omitempty"`
}

type minerAccountStatus struct {
	Address               string `json:"address"`
	Exists                bool   `json:"exists"`
	IsMiner               bool   `json:"is_miner"`
	MinerIdentityComplete bool   `json:"miner_identity_complete"`
	IP                    string `json:"ip,omitempty"`
	RewardAddress         string `json:"reward_address,omitempty"`
}

type registrationAttempt struct {
	Version                 int    `json:"version"`
	Kind                    string `json:"kind,omitempty"`
	Address                 string `json:"address"`
	RewardAddress           string `json:"reward_address,omitempty"`
	ExpectedCurrentEndpoint string `json:"expected_current_endpoint,omitempty"`
	Endpoint                string `json:"endpoint"`
	Amount                  string `json:"amount,omitempty"`
	Fee                     string `json:"fee"`
	ChainID                 uint64 `json:"chain_id"`
	Nonce                   uint64 `json:"nonce"`
	TxID                    string `json:"tx_id"`
	RawTx                   string `json:"raw_tx"`
	CreatedAt               string `json:"created_at"`
}

type fatalExit struct {
	message string
}

type registrationAttemptLock struct {
	file *os.File
}

func (lock *registrationAttemptLock) Unlock() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	unlockErr := unlockExclusiveFile(lock.file)
	closeErr := lock.file.Close()
	lock.file = nil
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

func main() {
	defer handleFatalExit()
	var (
		explorerURL        = flag.String("explorer-url", "http://127.0.0.1:8080", "Explorer base URL")
		node               = flag.String("node", "", "optional node host:port selected through Explorer")
		privateFD          = flag.Int("private-key-fd", 3, "inherited descriptor containing the hot private key")
		printAddress       = flag.Bool("print-address", false, "derive and print only the public DEX address")
		printChainID       = flag.Bool("print-chain-id", false, "load genesis.json from --config-dir and print its effective chain ID")
		printNodePort      = flag.Bool("print-node-port", false, "load config.json from --config-dir and print the node base port")
		printFrostEnabled  = flag.Bool("print-frost-enabled", false, "load frost_default.json from --config-dir and print true or false")
		configDir          = flag.String("config-dir", "", "runtime configuration directory used with --print-chain-id")
		expectedChainID    = flag.Uint64("expected-chain-id", 0, "required network chain ID; zero disables the explicit comparison")
		amount             = flag.String("amount", "3200000000", "raw FB amount to lock (minimum 32 FB)")
		reward             = flag.String("reward-address", "", "fixed payout address; empty means hot address")
		endpoint           = flag.String("endpoint", "", "required public miner host:port")
		updateEndpoint     = flag.Bool("update-endpoint", false, "submit a MinerInfoTx instead of a miner registration")
		expectedEndpoint   = flag.String("expected-current-endpoint", "", "required current endpoint when --update-endpoint is used")
		fee                = flag.String("fee", "1000", "raw FB runtime fee")
		stateFile          = flag.String("state-file", "", "required registration-attempt journal path")
		resubmitUnresolved = flag.Bool("resubmit-unresolved", false, "resubmit the exact signed transaction recorded in --state-file")
	)
	flag.Parse()
	configPrintModes := 0
	for _, enabled := range []bool{*printChainID, *printNodePort, *printFrostEnabled} {
		if enabled {
			configPrintModes++
		}
	}
	if configPrintModes > 0 {
		if configPrintModes != 1 || *printAddress || strings.TrimSpace(*configDir) == "" {
			fatal("exactly one config print mode requires --config-dir and cannot be combined with --print-address")
		}
		if *printChainID {
			genesis, err := config.LoadGenesisConfig(config.ConfigPath(*configDir, "genesis.json"))
			if err != nil {
				fatal("load genesis config: %v", err)
			}
			fmt.Println(genesis.EffectiveChainID())
			return
		}
		cfg, err := config.LoadFromDir(*configDir)
		if err != nil {
			fatal("load runtime config: %v", err)
		}
		if *printNodePort {
			fmt.Println(cfg.Network.BasePort)
		} else {
			fmt.Println(cfg.Frost.Enabled)
		}
		return
	}
	if *privateFD < 3 {
		fatal("--private-key-fd must be an inherited descriptor >= 3")
	}

	privateKey, err := readSecretFD(*privateFD)
	if err != nil {
		fatal("read private key: %v", err)
	}
	keyManager := utils.NewKeyManager()
	if err := keyManager.InitKey(privateKey); err != nil {
		fatal("parse private key: %v", err)
	}
	address := keyManager.GetAddress()
	if *printAddress {
		if strings.TrimSpace(*stateFile) != "" || *resubmitUnresolved || *expectedChainID != 0 || strings.TrimSpace(*configDir) != "" {
			fatal("--print-address cannot be combined with registration state options")
		}
		fmt.Println(address)
		return
	}
	requestedEndpoint := strings.TrimSpace(*endpoint)
	if requestedEndpoint == "" {
		fatal("--endpoint is required")
	}
	journalPath := strings.TrimSpace(*stateFile)
	if journalPath == "" {
		fatal("--state-file is required")
	}
	payout := strings.TrimSpace(*reward)
	if payout == "" {
		payout = address
	}
	registrationLock, err := lockRegistrationAttempt(journalPath)
	if err != nil {
		fatal("lock registration attempt: %v", err)
	}
	defer registrationLock.Unlock()

	client := &http.Client{Timeout: 15 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	status, err := fetchMinerStatus(ctx, client, *explorerURL, *node, address)
	if err != nil {
		fatal("load miner registration status: %v", err)
	}
	if *updateEndpoint {
		runEndpointUpdate(ctx, client, keyManager, status, endpointUpdateOptions{
			explorerURL:             *explorerURL,
			node:                    *node,
			address:                 address,
			newEndpoint:             requestedEndpoint,
			expectedCurrentEndpoint: strings.TrimSpace(*expectedEndpoint),
			fee:                     strings.TrimSpace(*fee),
			journalPath:             journalPath,
			expectedChainID:         *expectedChainID,
			resubmitUnresolved:      *resubmitUnresolved,
		})
		return
	}
	if strings.TrimSpace(*expectedEndpoint) != "" {
		fatal("--expected-current-endpoint requires --update-endpoint")
	}
	alreadyRegistered, err := validateExistingMinerRegistration(status, address, payout, requestedEndpoint)
	if err != nil {
		fatal("miner registration status: %v", err)
	}
	if alreadyRegistered {
		fmt.Printf("Miner already registered: address=%s endpoint=%s\n", address, status.IP)
		return
	}
	previous, err := guardRegistrationAttempt(journalPath, address, payout, requestedEndpoint, strings.TrimSpace(*amount), strings.TrimSpace(*fee), *resubmitUnresolved)
	if err != nil {
		fatal("registration attempt: %v", err)
	}
	if previous != nil {
		if *expectedChainID != 0 && previous.ChainID != *expectedChainID {
			fatal("registration attempt chain ID %d does not match expected chain ID %d", previous.ChainID, *expectedChainID)
		}
		rawTx, err := validateRecordedRegistration(*previous)
		if err != nil {
			fatal("registration attempt: %v", err)
		}
		if err := submit(ctx, client, *explorerURL, *node, rawTx); err != nil {
			fatal("resubmit registration tx_id=%s: %v", previous.TxID, err)
		}
		fmt.Printf("Miner registration resubmitted: address=%s tx_id=%s\n", address, previous.TxID)
		return
	}
	nonce, chainID, err := fetchNonce(ctx, client, *explorerURL, *node, address)
	if err != nil {
		fatal("load account nonce: %v", err)
	}
	if chainID == 0 {
		fatal("Explorer returned an invalid zero chain ID")
	}
	if *expectedChainID != 0 && chainID != *expectedChainID {
		fatal("Explorer chain ID %d does not match expected chain ID %d", chainID, *expectedChainID)
	}

	blsPublicKey, err := keyManager.BLSPublicKeyBytes()
	if err != nil {
		fatal("derive BLS public key: %v", err)
	}
	base := registrationBase(address, nonce, chainID, keyManager.PublicKeyBytes(), *fee)
	minerTx := &pb.MinerTx{
		Base:          base,
		Op:            pb.OrderOp_ADD,
		Amount:        strings.TrimSpace(*amount),
		RewardAddress: payout,
		BlsPublicKey:  blsPublicKey,
		Endpoint:      requestedEndpoint,
	}
	digest, err := vm.MinerRegistrationPoPDigest(minerTx)
	if err != nil {
		fatal("validate registration: %v", err)
	}
	minerTx.BlsProof, err = keyManager.SignBLS(digest)
	if err != nil {
		fatal("sign BLS proof of possession: %v", err)
	}
	anyTx := &pb.AnyTx{Content: &pb.AnyTx_MinerTx{MinerTx: minerTx}}
	txID, err := txverify.SignWithKeyManager(anyTx, keyManager)
	if err != nil {
		fatal("sign registration transaction: %v", err)
	}
	encoded, err := proto.Marshal(anyTx)
	if err != nil {
		fatal("encode registration transaction: %v", err)
	}
	attempt := registrationAttempt{
		Version:       1,
		Kind:          "registration",
		Address:       address,
		RewardAddress: payout,
		Endpoint:      requestedEndpoint,
		Amount:        strings.TrimSpace(*amount),
		Fee:           strings.TrimSpace(*fee),
		ChainID:       chainID,
		Nonce:         nonce,
		TxID:          txID,
		RawTx:         base64.StdEncoding.EncodeToString(encoded),
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
	}
	if err := writeRegistrationAttempt(journalPath, attempt); err != nil {
		fatal("persist registration attempt before submit: %v", err)
	}
	if err := submit(ctx, client, *explorerURL, *node, encoded); err != nil {
		fatal("submit registration: %v", err)
	}
	fmt.Printf("Miner registration submitted: address=%s tx_id=%s\n", address, txID)
}

type endpointUpdateOptions struct {
	explorerURL             string
	node                    string
	address                 string
	newEndpoint             string
	expectedCurrentEndpoint string
	fee                     string
	journalPath             string
	expectedChainID         uint64
	resubmitUnresolved      bool
}

func runEndpointUpdate(ctx context.Context, client *http.Client, keyManager *utils.KeyManager, status *minerAccountStatus, opts endpointUpdateOptions) {
	if opts.expectedCurrentEndpoint == "" {
		fatal("--expected-current-endpoint is required with --update-endpoint")
	}
	if opts.expectedCurrentEndpoint == opts.newEndpoint {
		fatal("endpoint update requires different current and new endpoints")
	}
	if status == nil || !status.Exists || !status.IsMiner {
		fatal("endpoint update requires an already registered miner")
	}
	if status.Address != opts.address {
		fatal("Explorer returned miner address %s for %s", status.Address, opts.address)
	}
	if !status.MinerIdentityComplete {
		fatal("endpoint update requires a complete registered miner identity")
	}
	currentEndpoint := strings.TrimSpace(status.IP)
	if currentEndpoint == opts.newEndpoint {
		fmt.Printf("Miner endpoint already updated: address=%s endpoint=%s\n", opts.address, opts.newEndpoint)
		return
	}
	if currentEndpoint != opts.expectedCurrentEndpoint {
		fatal("miner endpoint is %s, expected exactly %s", currentEndpoint, opts.expectedCurrentEndpoint)
	}

	previous, err := guardEndpointUpdateAttempt(opts.journalPath, opts.address, opts.expectedCurrentEndpoint, opts.newEndpoint, opts.fee, opts.resubmitUnresolved)
	if err != nil {
		fatal("endpoint update attempt: %v", err)
	}
	if previous != nil {
		if opts.expectedChainID != 0 && previous.ChainID != opts.expectedChainID {
			fatal("endpoint update attempt chain ID %d does not match expected chain ID %d", previous.ChainID, opts.expectedChainID)
		}
		rawTx, err := validateRecordedEndpointUpdate(*previous)
		if err != nil {
			fatal("endpoint update attempt: %v", err)
		}
		if err := submit(ctx, client, opts.explorerURL, opts.node, rawTx); err != nil {
			fatal("resubmit endpoint update tx_id=%s: %v", previous.TxID, err)
		}
		fmt.Printf("Miner endpoint update resubmitted: address=%s tx_id=%s\n", opts.address, previous.TxID)
		return
	}

	nonce, chainID, err := fetchNonce(ctx, client, opts.explorerURL, opts.node, opts.address)
	if err != nil {
		fatal("load account nonce: %v", err)
	}
	if chainID == 0 {
		fatal("Explorer returned an invalid zero chain ID")
	}
	if opts.expectedChainID != 0 && chainID != opts.expectedChainID {
		fatal("Explorer chain ID %d does not match expected chain ID %d", chainID, opts.expectedChainID)
	}

	base := registrationBase(opts.address, nonce, chainID, keyManager.PublicKeyBytes(), opts.fee)
	minerInfoTx := &pb.MinerInfoTx{Base: base, Endpoint: opts.newEndpoint}
	anyTx := &pb.AnyTx{Content: &pb.AnyTx_MinerInfoTx{MinerInfoTx: minerInfoTx}}
	txID, err := txverify.SignWithKeyManager(anyTx, keyManager)
	if err != nil {
		fatal("sign endpoint update transaction: %v", err)
	}
	encoded, err := proto.Marshal(anyTx)
	if err != nil {
		fatal("encode endpoint update transaction: %v", err)
	}
	attempt := registrationAttempt{
		Version:                 1,
		Kind:                    "endpoint_update",
		Address:                 opts.address,
		ExpectedCurrentEndpoint: opts.expectedCurrentEndpoint,
		Endpoint:                opts.newEndpoint,
		Fee:                     opts.fee,
		ChainID:                 chainID,
		Nonce:                   nonce,
		TxID:                    txID,
		RawTx:                   base64.StdEncoding.EncodeToString(encoded),
		CreatedAt:               time.Now().UTC().Format(time.RFC3339),
	}
	if err := writeRegistrationAttempt(opts.journalPath, attempt); err != nil {
		fatal("persist endpoint update attempt before submit: %v", err)
	}
	if err := submit(ctx, client, opts.explorerURL, opts.node, encoded); err != nil {
		fatal("submit endpoint update: %v", err)
	}
	fmt.Printf("Miner endpoint update submitted: address=%s tx_id=%s\n", opts.address, txID)
}

func lockRegistrationAttempt(path string) (*registrationAttemptLock, error) {
	dir := filepath.Dir(filepath.Clean(path))
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, fmt.Errorf("inspect state directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("state directory must be a real directory")
	}
	lockPath := path + ".lock"
	if existing, err := os.Lstat(lockPath); err == nil && existing.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("registration lock must not be a symbolic link")
	} else if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect registration lock: %w", err)
	}
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := lockFile.Chmod(0600); err != nil {
		lockFile.Close()
		return nil, err
	}
	locked, err := tryExclusiveFileLock(lockFile)
	if err != nil {
		lockFile.Close()
		return nil, err
	}
	if !locked {
		lockFile.Close()
		return nil, fmt.Errorf("another registration process is using %s", path)
	}
	return &registrationAttemptLock{file: lockFile}, nil
}

func fetchMinerStatus(ctx context.Context, client *http.Client, explorerURL, node, address string) (*minerAccountStatus, error) {
	var response addressResponse
	request := addressRequest{Node: strings.TrimSpace(node), Address: address, RequireRemote: true}
	if err := postJSON(ctx, client, explorerURL, "/api/address", request, &response); err != nil {
		return nil, err
	}
	if response.Error != "" {
		return nil, fmt.Errorf("%s", response.Error)
	}
	if response.Account == nil || !response.Account.Exists {
		return response.Account, nil
	}
	if response.Account.Address != address {
		return nil, fmt.Errorf("Explorer returned account %s for requested address %s", response.Account.Address, address)
	}
	return response.Account, nil
}

func validateExistingMinerRegistration(status *minerAccountStatus, address, reward, endpoint string) (bool, error) {
	if status == nil || !status.IsMiner {
		return false, nil
	}
	if status.Address != address {
		return false, fmt.Errorf("Explorer returned miner address %s for %s", status.Address, address)
	}
	if !status.MinerIdentityComplete {
		return false, fmt.Errorf("address %s is already a miner but its public identity is incomplete", address)
	}
	if strings.TrimSpace(status.RewardAddress) != reward {
		return false, fmt.Errorf("address %s is already registered with reward address %s", address, status.RewardAddress)
	}
	if strings.TrimSpace(status.IP) != endpoint {
		return false, fmt.Errorf("address %s is already registered with endpoint %s", address, status.IP)
	}
	return true, nil
}

func guardRegistrationAttempt(path, address, reward, endpoint, amount, fee string, resubmit bool) (*registrationAttempt, error) {
	if path == "" {
		if resubmit {
			return nil, fmt.Errorf("--resubmit-unresolved requires --state-file")
		}
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if resubmit {
			return nil, fmt.Errorf("no unresolved attempt exists in %s", path)
		}
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) > 16384 {
		return nil, fmt.Errorf("state file %s is too large", path)
	}
	var previous registrationAttempt
	if err := json.Unmarshal(data, &previous); err != nil || previous.Version != 1 || previous.TxID == "" || previous.RawTx == "" {
		return nil, fmt.Errorf("state file %s is invalid", path)
	}
	if previous.Kind != "" && previous.Kind != "registration" {
		return nil, fmt.Errorf("state file %s is not a registration attempt", path)
	}
	if previous.Address != address || previous.RewardAddress != reward || previous.Endpoint != endpoint || previous.Amount != amount || previous.Fee != fee {
		return nil, fmt.Errorf("state file %s belongs to a different immutable registration intent", path)
	}
	if !resubmit {
		return nil, fmt.Errorf("unresolved tx_id=%s is already recorded; inspect it before using --resubmit-unresolved", previous.TxID)
	}
	return &previous, nil
}

func guardEndpointUpdateAttempt(path, address, expectedCurrentEndpoint, endpoint, fee string, resubmit bool) (*registrationAttempt, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if resubmit {
			return nil, fmt.Errorf("no unresolved attempt exists in %s", path)
		}
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) > 16384 {
		return nil, fmt.Errorf("state file %s is too large", path)
	}
	var previous registrationAttempt
	if err := json.Unmarshal(data, &previous); err != nil || previous.Version != 1 || previous.Kind != "endpoint_update" || previous.TxID == "" || previous.RawTx == "" {
		return nil, fmt.Errorf("state file %s is not a valid endpoint update attempt", path)
	}
	if previous.Address != address || previous.ExpectedCurrentEndpoint != expectedCurrentEndpoint || previous.Endpoint != endpoint || previous.Fee != fee {
		return nil, fmt.Errorf("state file %s belongs to a different immutable endpoint update intent", path)
	}
	if !resubmit {
		return nil, fmt.Errorf("unresolved tx_id=%s is already recorded; inspect it before using --resubmit-unresolved", previous.TxID)
	}
	return &previous, nil
}

func validateRecordedRegistration(attempt registrationAttempt) ([]byte, error) {
	rawTx, err := base64.StdEncoding.DecodeString(attempt.RawTx)
	if err != nil || len(rawTx) == 0 {
		return nil, fmt.Errorf("state file contains invalid raw transaction")
	}
	var anyTx pb.AnyTx
	if err := proto.Unmarshal(rawTx, &anyTx); err != nil {
		return nil, fmt.Errorf("decode recorded transaction: %w", err)
	}
	if attempt.ChainID == 0 {
		return nil, fmt.Errorf("state file contains an invalid zero chain ID")
	}
	if err := txverify.Verify(&anyTx, attempt.ChainID); err != nil {
		return nil, fmt.Errorf("verify recorded transaction: %w", err)
	}
	wrapper, ok := anyTx.GetContent().(*pb.AnyTx_MinerTx)
	if !ok || wrapper.MinerTx == nil || wrapper.MinerTx.Base == nil {
		return nil, fmt.Errorf("recorded transaction is not a miner registration")
	}
	minerTx := wrapper.MinerTx
	if anyTx.GetTxId() != attempt.TxID || minerTx.Base.TxId != attempt.TxID ||
		minerTx.Base.FromAddress != attempt.Address || minerTx.Base.ChainId != attempt.ChainID ||
		minerTx.Base.Nonce != attempt.Nonce || strings.TrimSpace(minerTx.Base.Fee) != attempt.Fee ||
		strings.TrimSpace(minerTx.Amount) != attempt.Amount ||
		strings.TrimSpace(minerTx.RewardAddress) != attempt.RewardAddress ||
		strings.TrimSpace(minerTx.Endpoint) != attempt.Endpoint || minerTx.Op != pb.OrderOp_ADD {
		return nil, fmt.Errorf("recorded transaction does not match its immutable intent")
	}
	return rawTx, nil
}

func validateRecordedEndpointUpdate(attempt registrationAttempt) ([]byte, error) {
	rawTx, err := base64.StdEncoding.DecodeString(attempt.RawTx)
	if err != nil || len(rawTx) == 0 {
		return nil, fmt.Errorf("state file contains invalid raw transaction")
	}
	var anyTx pb.AnyTx
	if err := proto.Unmarshal(rawTx, &anyTx); err != nil {
		return nil, fmt.Errorf("decode recorded transaction: %w", err)
	}
	if attempt.Kind != "endpoint_update" || attempt.ChainID == 0 {
		return nil, fmt.Errorf("state file contains an invalid endpoint update intent")
	}
	if err := txverify.Verify(&anyTx, attempt.ChainID); err != nil {
		return nil, fmt.Errorf("verify recorded transaction: %w", err)
	}
	wrapper, ok := anyTx.GetContent().(*pb.AnyTx_MinerInfoTx)
	if !ok || wrapper.MinerInfoTx == nil || wrapper.MinerInfoTx.Base == nil {
		return nil, fmt.Errorf("recorded transaction is not a miner endpoint update")
	}
	minerInfoTx := wrapper.MinerInfoTx
	if anyTx.GetTxId() != attempt.TxID || minerInfoTx.Base.TxId != attempt.TxID ||
		minerInfoTx.Base.FromAddress != attempt.Address || minerInfoTx.Base.ChainId != attempt.ChainID ||
		minerInfoTx.Base.Nonce != attempt.Nonce || strings.TrimSpace(minerInfoTx.Base.Fee) != attempt.Fee ||
		strings.TrimSpace(minerInfoTx.Endpoint) != attempt.Endpoint {
		return nil, fmt.Errorf("recorded endpoint update transaction does not match its immutable intent")
	}
	return rawTx, nil
}

func writeRegistrationAttempt(path string, attempt registrationAttempt) error {
	if path == "" {
		return nil
	}
	dir := filepath.Dir(filepath.Clean(path))
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("inspect state directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("state directory must be a real directory")
	}
	if existing, err := os.Lstat(path); err == nil && existing.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("state file must not be a symbolic link")
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("inspect state file: %w", err)
	}
	encoded, err := json.MarshalIndent(attempt, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	temporary, err := os.CreateTemp(dir, ".registration-attempt-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(encoded); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return syncParentDirectory(path)
}

                                                                          
                                                                         
func registrationBase(address string, nonce, chainID uint64, publicKey []byte, fee string) *pb.BaseMessage {
	return &pb.BaseMessage{
		FromAddress: address,
		Nonce:       nonce,
		Fee:         strings.TrimSpace(fee),
		ChainId:     chainID,
		PublicKeys: &pb.PublicKeys{Keys: map[int32][]byte{
			int32(pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1_DEX): append([]byte(nil), publicKey...),
		}},
	}
}

func readSecretFD(fd int) (string, error) {
	file := os.NewFile(uintptr(fd), "miner-private-key")
	if file == nil {
		return "", fmt.Errorf("descriptor %d is unavailable", fd)
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxPrivateKeyBytes+1))
	if err != nil {
		return "", err
	}
	defer func() {
		for i := range raw {
			raw[i] = 0
		}
	}()
	if len(raw) > maxPrivateKeyBytes {
		return "", fmt.Errorf("secret exceeds %d bytes", maxPrivateKeyBytes)
	}
	fields := strings.Fields(string(raw))
	if len(fields) != 1 {
		return "", fmt.Errorf("descriptor must contain exactly one private-key token")
	}
	return fields[0], nil
}

func fetchNonce(ctx context.Context, client *http.Client, explorerURL, node, address string) (uint64, uint64, error) {
	var response nonceResponse
	request := nonceRequest{Node: strings.TrimSpace(node), Address: address, RequireRemote: true}
	if err := postJSON(ctx, client, explorerURL, "/api/wallet/nonce", request, &response); err != nil {
		return 0, 0, err
	}
	if response.Error != "" {
		return 0, 0, fmt.Errorf("%s", response.Error)
	}
	return response.Nonce, response.ChainID, nil
}

func submit(ctx context.Context, client *http.Client, explorerURL, node string, tx []byte) error {
	var response submitResponse
	request := submitRequest{Node: strings.TrimSpace(node), Tx: base64.StdEncoding.EncodeToString(tx)}
	if err := postJSON(ctx, client, explorerURL, "/api/wallet/submittx", request, &response); err != nil {
		return err
	}
	if response.Error != "" {
		return fmt.Errorf("%s", response.Error)
	}
	if !response.OK {
		return fmt.Errorf("Explorer did not acknowledge the transaction")
	}
	return nil
}

func postJSON(ctx context.Context, client *http.Client, baseURL, path string, request, response any) error {
	encoded, err := json.Marshal(request)
	if err != nil {
		return err
	}
	url := strings.TrimRight(strings.TrimSpace(baseURL), "/") + path
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpResponse, err := client.Do(httpRequest)
	if err != nil {
		return err
	}
	defer httpResponse.Body.Close()
	if httpResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(httpResponse.Body, 4096))
		return fmt.Errorf("HTTP %d: %s", httpResponse.StatusCode, strings.TrimSpace(string(body)))
	}
	decoder := json.NewDecoder(io.LimitReader(httpResponse.Body, 1<<20))
	if err := decoder.Decode(response); err != nil {
		return err
	}
	return nil
}

func fatal(format string, args ...any) {
	panic(fatalExit{message: fmt.Sprintf(format, args...)})
}

func handleFatalExit() {
	recovered := recover()
	if recovered == nil {
		return
	}
	if failure, ok := recovered.(fatalExit); ok {
		fmt.Fprintf(os.Stderr, "miner-register: %s\n", failure.message)
		os.Exit(1)
	}
	panic(recovered)
}
