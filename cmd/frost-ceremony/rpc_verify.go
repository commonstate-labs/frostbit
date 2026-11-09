package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"dex/config"
	"dex/frost/ceremony"
	"dex/frost/chain/evm"

	"golang.org/x/crypto/sha3"
)

const (
	externalRPCTimeout = 20 * time.Second
	maxRPCBodyBytes    = 4 << 20
)

var errRPCNullResult = errors.New("RPC result is null")

type onlineRPCConfig struct {
	Chains map[string]onlineChainRPC `json:"chains"`
}

type onlineChainRPC struct {
	JSONRPC              string            `json:"jsonRpc"`
	SolidityNode         string            `json:"solidityNode,omitempty"`
	FinalityPolicy       string            `json:"finalityPolicy"`
	MinConfirmations     uint64            `json:"minConfirmations"`
	DeploymentBlockHints map[string]uint64 `json:"deploymentBlockHints,omitempty"`
}

type jsonRPCClient struct {
	endpoint             string
	http                 *http.Client
	nextID               uint64
	deploymentBlockHints map[string]uint64
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      uint64          `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type rpcTransactionReceipt struct {
	TransactionHash string `json:"transactionHash"`
	ContractAddress string `json:"contractAddress"`
	BlockHash       string `json:"blockHash"`
	BlockNumber     string `json:"blockNumber"`
	Status          string `json:"status"`
}

type rpcDeploymentTransaction struct {
	Hash        string          `json:"hash"`
	From        string          `json:"from"`
	To          json.RawMessage `json:"to"`
	Input       string          `json:"input"`
	BlockHash   string          `json:"blockHash"`
	BlockNumber string          `json:"blockNumber"`
}

type rpcBlockHeader struct {
	Hash   string `json:"hash"`
	Number string `json:"number"`
}

type rpcBlockWithTransactions struct {
	rpcBlockHeader
	Transactions []rpcDeploymentTransaction `json:"transactions"`
}

type tronSolidTransactionInfo struct {
	ID              string `json:"id"`
	BlockNumber     uint64 `json:"blockNumber"`
	ContractAddress string `json:"contract_address"`
	Receipt         struct {
		Result string `json:"result"`
	} `json:"receipt"`
}

type tronSolidDeploymentTransaction struct {
	TxID string `json:"txID"`
	Ret  []struct {
		ContractRet string `json:"contractRet"`
	} `json:"ret"`
	RawData struct {
		Contracts []struct {
			Type      string `json:"type"`
			Parameter struct {
				TypeURL string `json:"type_url"`
				Value   struct {
					OwnerAddress string `json:"owner_address"`
					NewContract  struct {
						OriginAddress string `json:"origin_address"`
						Bytecode      string `json:"bytecode"`
					} `json:"new_contract"`
				} `json:"value"`
			} `json:"parameter"`
		} `json:"contract"`
	} `json:"raw_data"`
}

type tronSolidBlock struct {
	BlockID     string `json:"blockID"`
	BlockHeader struct {
		RawData struct {
			Number uint64 `json:"number"`
		} `json:"raw_data"`
	} `json:"block_header"`
}

type tronNodeInfo struct {
	SolidityBlock string `json:"solidityBlock"`
}

type verifiedFinalityAnchor struct {
	policy           string
	blockNumber      uint64
	blockHash        string
	minConfirmations uint64
}

func loadRPCConfig(path string) (*onlineRPCConfig, error) {
	data, err := readRegularFile(path, maxSmallJSONBytes)
	if err != nil {
		return nil, err
	}
	var cfg onlineRPCConfig
	if err := strictJSON(data, &cfg); err != nil {
		return nil, fmt.Errorf("decode RPC config: %w", err)
	}
	if len(cfg.Chains) != 5 {
		return nil, fmt.Errorf("RPC config must contain exactly five account chains, got %d", len(cfg.Chains))
	}
	for _, chainName := range []string{"arb", "base", "bnb", "eth", "trx"} {
		chainConfig, ok := cfg.Chains[chainName]
		if !ok {
			return nil, fmt.Errorf("RPC config is missing chain %s", chainName)
		}
		if err := validateRPCURL(chainConfig.JSONRPC); err != nil {
			return nil, fmt.Errorf("chain %s jsonRpc: %w", chainName, err)
		}
		if chainConfig.MinConfirmations == 0 || chainConfig.MinConfirmations > 100000 {
			return nil, fmt.Errorf("chain %s minConfirmations must be in 1..100000", chainName)
		}
		chainConfig.DeploymentBlockHints, err = normalizeDeploymentBlockHints(chainConfig.DeploymentBlockHints)
		if err != nil {
			return nil, fmt.Errorf("chain %s deploymentBlockHints: %w", chainName, err)
		}
		cfg.Chains[chainName] = chainConfig
		if chainName == "trx" {
			if chainConfig.FinalityPolicy != ceremony.GenesisFinalityTronSolidity {
				return nil, errors.New("chain trx finalityPolicy must be tron-solidity")
			}
			if err := validateRPCURL(chainConfig.SolidityNode); err != nil {
				return nil, fmt.Errorf("chain trx solidityNode: %w", err)
			}
		} else if chainConfig.SolidityNode != "" {
			return nil, fmt.Errorf("chain %s must not configure solidityNode", chainName)
		} else if chainName == "bnb" {
			if chainConfig.FinalityPolicy != ceremony.GenesisFinalityRPCFinalized &&
				(chainConfig.FinalityPolicy != ceremony.GenesisFinalityBNBConfirmations || chainConfig.MinConfirmations < 64) {
				return nil, errors.New("chain bnb finalityPolicy must be rpc-finalized or bnb-confirmations with at least 64 confirmations")
			}
		} else if chainConfig.FinalityPolicy != ceremony.GenesisFinalityRPCFinalized {
			return nil, fmt.Errorf("chain %s finalityPolicy must be rpc-finalized", chainName)
		}
	}
	return &cfg, nil
}

func normalizeDeploymentBlockHints(hints map[string]uint64) (map[string]uint64, error) {
	if len(hints) == 0 {
		return nil, nil
	}
	normalized := make(map[string]uint64, len(hints))
	for hash, height := range hints {
		if len(hash) != 66 || !strings.EqualFold(hash[:2], "0x") || !validNonzeroHash(hash) || height == 0 {
			return nil, errors.New("each hint requires a nonzero 0x-prefixed 32-byte transaction hash and a positive block height")
		}
		canonical := strings.ToLower(hash)
		if _, exists := normalized[canonical]; exists {
			return nil, errors.New("deployment block hints contain duplicate normalized transaction hashes")
		}
		normalized[canonical] = height
	}
	return normalized, nil
}

func validateRPCURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return errors.New("must use an HTTP(S) URL without embedded credentials")
	}
	if strings.TrimSpace(raw) != raw || parsed.Fragment != "" {
		return errors.New("URL is not canonical")
	}
	if parsed.Scheme == "http" {
		ip := net.ParseIP(parsed.Hostname())
		if ip == nil || !ip.IsLoopback() {
			return errors.New("plaintext HTTP RPC is permitted only for a literal loopback IP; remote RPC requires HTTPS")
		}
	}
	return nil
}

func verifyDeploymentsOnline(rpcConfig *onlineRPCConfig, deployments []verifiedDeployment, frostConfig config.FrostConfig, epoch uint64, artifacts map[string]expectedContractArtifact) error {
	if rpcConfig == nil {
		return errors.New("online RPC config is nil")
	}
	clients := make(map[string]*jsonRPCClient, 5)
	latestBlocks := make(map[string]uint64, 5)
	coverageHeads := make(map[string]verifiedFinalityAnchor, 5)
	requestedChains := make(map[string]bool, 5)
	for _, deployment := range deployments {
		requestedChains[deployment.chain] = true
	}
	for _, chainName := range []string{"arb", "base", "bnb", "eth", "trx"} {
		if !requestedChains[chainName] {
			continue
		}
		chainRPC := rpcConfig.Chains[chainName]
		client, err := newJSONRPCClient(chainRPC.JSONRPC)
		if err != nil {
			return fmt.Errorf("configure chain %s RPC: %w", chainName, err)
		}
		client.deploymentBlockHints, err = normalizeDeploymentBlockHints(chainRPC.DeploymentBlockHints)
		if err != nil {
			return fmt.Errorf("configure chain %s deploymentBlockHints: %w", chainName, err)
		}
		clients[chainName] = client
		var chainIDHex string
		if err := client.call("eth_chainId", []any{}, &chainIDHex); err != nil {
			return fmt.Errorf("query chain %s ID: %w", chainName, err)
		}
		chainID, err := parseRPCQuantity(chainIDHex)
		wantChainID := frostConfig.Chains[chainName].ChainID
		if err != nil || chainID != wantChainID {
			return fmt.Errorf("chain %s RPC reports chain ID %q; expected %d", chainName, chainIDHex, wantChainID)
		}
		var latestHex string
		if err := client.call("eth_blockNumber", []any{}, &latestHex); err != nil {
			return fmt.Errorf("query chain %s latest block: %w", chainName, err)
		}
		latestBlocks[chainName], err = parseRPCQuantity(latestHex)
		if err != nil {
			return fmt.Errorf("chain %s returned an invalid latest block", chainName)
		}
		if chainName != "trx" {
			anchorTag := "finalized"
			if chainRPC.FinalityPolicy == ceremony.GenesisFinalityBNBConfirmations {
				anchorTag = "latest"
			}
			anchor, anchorErr := fetchRPCBlockHeader(client, anchorTag)
			if anchorErr != nil {
				return fmt.Errorf("query chain %s %s finality anchor: %w", chainName, anchorTag, anchorErr)
			}
			                                                                 
			                                                                       
			                                                                         
			if anchor.blockNumber > latestBlocks[chainName] {
				latestBlocks[chainName] = anchor.blockNumber
			}
			coverageHeads[chainName] = anchor
		}
	}
	for index := range deployments {
		deployment := &deployments[index]
		artifact, ok := artifacts[deployment.receipt.Contract]
		if !ok {
			return fmt.Errorf("no trusted Solidity artifact for %s", deployment.receipt.Contract)
		}
		chainRPC := rpcConfig.Chains[deployment.chain]
		if err := verifyDeploymentOnline(clients[deployment.chain], deployment, epoch, latestBlocks[deployment.chain], chainRPC.MinConfirmations, artifact); err != nil {
			return fmt.Errorf("verify chain %s vault %d online: %w", deployment.chain, deployment.vaultID, err)
		}
		if deployment.chain == "trx" {
			if err := verifyTronSolidReceipt(chainRPC.SolidityNode, *deployment, deployment.blockNumber, epoch, artifact); err != nil {
				return fmt.Errorf("verify TRON solid receipt for vault %d: %w", deployment.vaultID, err)
			}
			coverage, err := fetchTronSolidHead(chainRPC.SolidityNode)
			if err != nil || coverage.blockNumber < deployment.blockNumber {
				return fmt.Errorf("query TRON solidity-node finality head: %w", err)
			}
			if err := verifyDeploymentStateAtBlock(clients[deployment.chain], *deployment, epoch, artifact, "latest"); err != nil {
				return fmt.Errorf("verify TRON vault state at current head: %w", err)
			}
			deployment.finalityPolicy = ceremony.GenesisFinalityTronSolidity
			deployment.finalityBlockNumber = deployment.blockNumber
			deployment.finalityBlockHash = deployment.blockHash
			deployment.minConfirmations = chainRPC.MinConfirmations
			continue
		}
		coverage := coverageHeads[deployment.chain]
		if coverage.blockNumber < deployment.blockNumber {
			return fmt.Errorf("deployment block %d is newer than the %s coverage head %d", deployment.blockNumber, chainRPC.FinalityPolicy, coverage.blockNumber)
		}
		if err := verifyDeploymentStateAtBlock(clients[deployment.chain], *deployment, epoch, artifact, fmt.Sprintf("0x%x", coverage.blockNumber)); err != nil {
			return fmt.Errorf("verify chain %s vault state at current %s head: %w", deployment.chain, chainRPC.FinalityPolicy, err)
		}
		deployment.finalityPolicy = chainRPC.FinalityPolicy
		deployment.minConfirmations = chainRPC.MinConfirmations
		if chainRPC.FinalityPolicy == ceremony.GenesisFinalityRPCFinalized {
			                                                                       
			                                                                    
			                                          
			deployment.finalityBlockNumber = deployment.blockNumber
			deployment.finalityBlockHash = deployment.blockHash
			continue
		}
		if deployment.blockNumber > ^uint64(0)-(chainRPC.MinConfirmations-1) {
			return errors.New("BNB confirmation target overflows uint64")
		}
		target := deployment.blockNumber + chainRPC.MinConfirmations - 1
		if latestBlocks[deployment.chain] < target {
			return fmt.Errorf("BNB deployment confirmation target %d is not yet available", target)
		}
		anchor, err := fetchRPCBlockHeader(clients[deployment.chain], fmt.Sprintf("0x%x", target))
		if err != nil || anchor.blockNumber != target {
			return fmt.Errorf("query fixed BNB confirmation anchor %d: %w", target, err)
		}
		deployment.finalityBlockNumber = anchor.blockNumber
		deployment.finalityBlockHash = anchor.blockHash
	}
	return nil
}

func fetchRPCBlockHeader(client *jsonRPCClient, tag string) (verifiedFinalityAnchor, error) {
	var block rpcBlockHeader
	if err := client.call("eth_getBlockByNumber", []any{tag, false}, &block); err != nil {
		return verifiedFinalityAnchor{}, err
	}
	number, err := parseRPCQuantity(block.Number)
	if err != nil || !validNonzeroHash(block.Hash) {
		return verifiedFinalityAnchor{}, errors.New("RPC returned an invalid finality block header")
	}
	return verifiedFinalityAnchor{blockNumber: number, blockHash: block.Hash}, nil
}

func newJSONRPCClient(endpoint string) (*jsonRPCClient, error) {
	if err := validateRPCURL(endpoint); err != nil {
		return nil, err
	}
	return &jsonRPCClient{endpoint: endpoint, http: hardenedHTTPClient()}, nil
}

func hardenedHTTPClient() *http.Client {
	return &http.Client{
		Timeout:       externalRPCTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return errors.New("RPC redirects are forbidden") },
	}
}

func (client *jsonRPCClient) call(method string, params any, destination any) error {
	client.nextID++
	requestBody, err := json.Marshal(struct {
		JSONRPC string `json:"jsonrpc"`
		ID      uint64 `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params"`
	}{"2.0", client.nextID, method, params})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), externalRPCTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.endpoint, bytes.NewReader(requestBody))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("RPC HTTP status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxRPCBodyBytes+1))
	if err != nil || len(body) == 0 || len(body) > maxRPCBodyBytes {
		return errors.New("RPC response is empty, unreadable, or too large")
	}
	if err := validateJSONStructure(body); err != nil {
		return errors.New("RPC response is invalid JSON")
	}
	var envelope jsonRPCResponse
	if err := json.Unmarshal(body, &envelope); err != nil {
		return errors.New("RPC response is invalid JSON")
	}
	if envelope.JSONRPC != "2.0" || envelope.ID != client.nextID {
		return errors.New("RPC response identity mismatch")
	}
	if envelope.Error != nil {
		return fmt.Errorf("RPC error %d: %s", envelope.Error.Code, envelope.Error.Message)
	}
	if len(envelope.Result) == 0 {
		return errors.New("RPC response has no result")
	}
	if bytes.Equal(bytes.TrimSpace(envelope.Result), []byte("null")) {
		return errRPCNullResult
	}
	if err := json.Unmarshal(envelope.Result, destination); err != nil {
		return errors.New("RPC result has an unexpected type")
	}
	return nil
}

func (client *jsonRPCClient) fetchDeploymentReceipt(txHash string) (rpcTransactionReceipt, error) {
	var receipt rpcTransactionReceipt
	err := client.call("eth_getTransactionReceipt", []any{txHash}, &receipt)
	if !errors.Is(err, errRPCNullResult) {
		return receipt, err
	}
	hash, decodeErr := decodeFixedHex(txHash, 32)
	if decodeErr != nil {
		return receipt, err
	}
	height, ok := client.deploymentBlockHints["0x"+hex.EncodeToString(hash)]
	if !ok || height == 0 {
		return receipt, err
	}
	                                                                          
	                                                                          
	var receipts []rpcTransactionReceipt
	if err := client.call("eth_getBlockReceipts", []any{fmt.Sprintf("0x%x", height)}, &receipts); err != nil {
		return receipt, fmt.Errorf("query hinted deployment block receipts: %w", err)
	}
	matches := 0
	for _, candidate := range receipts {
		if sameHexHash(candidate.TransactionHash, txHash) {
			receipt = candidate
			matches++
		}
	}
	if matches != 1 {
		return receipt, errors.New("hinted block must contain exactly one matching deployment receipt")
	}
	number, err := parseRPCQuantity(receipt.BlockNumber)
	if err != nil || number != height || !validNonzeroHash(receipt.BlockHash) {
		return receipt, errors.New("hinted deployment receipt has an invalid block anchor")
	}
	return receipt, nil
}

func verifyDeploymentOnline(client *jsonRPCClient, deployment *verifiedDeployment, epoch, latestBlock, confirmations uint64, artifact expectedContractArtifact) error {
	if deployment == nil {
		return errors.New("deployment is nil")
	}
	receipt, err := client.fetchDeploymentReceipt(deployment.receipt.TxHash)
	if err != nil {
		return fmt.Errorf("query deployment transaction receipt: %w", err)
	}
	status, err := parseRPCQuantity(receipt.Status)
	if err != nil || status != 1 || !sameHexHash(receipt.TransactionHash, deployment.receipt.TxHash) || !validNonzeroHash(receipt.BlockHash) {
		return errors.New("deployment transaction is missing, unsuccessful, or unanchored")
	}
	contractAddress, err := evm.NormalizeAccountAddress(deployment.chain, receipt.ContractAddress, false)
	if err != nil || contractAddress != deployment.address {
		return errors.New("deployment transaction contract address does not match")
	}
	deploymentBlock, err := parseRPCQuantity(receipt.BlockNumber)
	if err != nil || latestBlock < deploymentBlock || confirmations == 0 || latestBlock-deploymentBlock+1 < confirmations {
		return fmt.Errorf("deployment has fewer than %d required confirmations", confirmations)
	}
	if deployment.receipt.DepositProtocol == unifiedVaultDepositProtocol && deployment.receipt.BlockNumber != deploymentBlock {
		return errors.New("unified Vault receipt deployment block does not match online evidence")
	}
	var block rpcBlockHeader
	if err := client.call("eth_getBlockByNumber", []any{receipt.BlockNumber, false}, &block); err != nil {
		return fmt.Errorf("query deployment block header: %w", err)
	}
	headerNumber, err := parseRPCQuantity(block.Number)
	if err != nil || headerNumber != deploymentBlock || !sameHexHash(block.Hash, receipt.BlockHash) {
		return errors.New("deployment receipt block anchor does not match its block header")
	}
	if deployment.chain != "trx" {
		if err := verifyDeploymentTransaction(client, *deployment, epoch, artifact, receipt); err != nil {
			return err
		}
	}
	deployment.blockNumber = deploymentBlock
	deployment.blockHash = receipt.BlockHash
	receiptCreationHash, _ := decodeFixedHex(deployment.receipt.CreationCodeHash, 32)
	if !bytes.Equal(keccak256(artifact.creationCode), receiptCreationHash) {
		return errors.New("deployment creation code hash does not match the trusted artifact")
	}
	if deployment.receipt.CompilerVersion != artifact.solcVersion {
		return errors.New("deployment compiler version does not match trusted build-info")
	}
	receiptSettings, err := canonicalJSON(deployment.receipt.CompilerSettings)
	if err != nil || !bytes.Equal(receiptSettings, artifact.compilerSettings) {
		return errors.New("deployment compiler settings do not match trusted build-info")
	}
	                                                                       
	                                                                          
	                                                                             
	                                                                             
	                                           
	stateBlockTag := fmt.Sprintf("0x%x", latestBlock)
	if deployment.chain == "trx" {
		                                                                  
		                                                                       
		                                                             
		stateBlockTag = "latest"
	}
	return verifyDeploymentStateAtBlock(client, *deployment, epoch, artifact, stateBlockTag)
}

func verifyDeploymentStateAtBlock(client *jsonRPCClient, deployment verifiedDeployment, epoch uint64, artifact expectedContractArtifact, blockTag string) error {
	var codeHex string
	if err := client.call("eth_getCode", []any{deployment.address, blockTag}, &codeHex); err != nil {
		return fmt.Errorf("query deployed runtime code: %w", err)
	}
	code, err := decodeHexData(codeHex)
	if err != nil || len(code) == 0 {
		return errors.New("on-chain runtime code is empty or invalid")
	}
	receiptRuntimeHash, _ := decodeFixedHex(deployment.receipt.RuntimeCodeHash, 32)
	if !bytes.Equal(keccak256(code), receiptRuntimeHash) {
		return errors.New("on-chain runtime code hash does not match the deployment receipt")
	}
	expectedRuntime, err := expectedVaultRuntime(deployment, artifact)
	if err != nil {
		return err
	}
	if !bytes.Equal(code, expectedRuntime) {
		return errors.New("on-chain runtime code does not match the trusted Solidity artifact")
	}
	if deployment.receipt.DepositProtocol == unifiedVaultDepositProtocol {
		domain, err := callView(client, deployment.address, "dexChainId()", blockTag)
		if err != nil || len(domain) != 32 || !new(big.Int).SetBytes(domain).IsUint64() || new(big.Int).SetBytes(domain).Uint64() != deployment.dexChainID {
			return errors.New("on-chain dexChainId() does not match the ceremony DEX domain")
		}
	}
	if deployment.receipt.AuthorityProtocol != 3 || deployment.receipt.Contract != artifact.contractName {
		return errors.New("Vault authority protocol or trusted contract identity mismatch")
	}
	expectedName := "SchnorrExampleV3"
	if deployment.chain == "trx" {
		expectedName = "SchnorrExampleTronV3"
	}

	if artifact.contractName != expectedName {
		return errors.New("trusted Vault artifact does not match authority protocol")
	}
	if deployment.receipt.AuthorityProtocol == 3 {
		for signature, expected := range map[string]int64{"AUTHORITY_PROTOCOL()": 3, "MAX_HANDOVERS_PER_CALL()": 8} {
			value, err := callView(client, deployment.address, signature, blockTag)
			if err != nil || len(value) != 32 || new(big.Int).SetBytes(value).Cmp(big.NewInt(expected)) != 0 {
				return fmt.Errorf("on-chain %s does not match V3 authority capability", signature)
			}
		}
	}
	pointResult, err := callView(client, deployment.address, "P()", blockTag)
	if err != nil || len(pointResult) != 64 {
		return errors.New("P() returned an invalid ABI result")
	}
	wantX, _ := canonicalDecimal(deployment.receipt.PX, "px")
	wantY, _ := canonicalDecimal(deployment.receipt.PY, "py")
	defer wantX.SetInt64(0)
	defer wantY.SetInt64(0)
	if new(big.Int).SetBytes(pointResult[:32]).Cmp(wantX) != 0 || new(big.Int).SetBytes(pointResult[32:]).Cmp(wantY) != 0 {
		return errors.New("on-chain P() authority does not match the ceremony key")
	}
	guardianResult, err := callView(client, deployment.address, "G()", blockTag)
	guardianKey, keyErr := receiptGuardianKey(&deployment.receipt)
	if err != nil || keyErr != nil || !bytes.Equal(guardianResult, guardianKey) {
		return errors.New("on-chain G() differs from fixed guardian")
	}
	nonceResult, err := callView(client, deployment.address, "authorityNonce()", blockTag)
	if err != nil || len(nonceResult) != 32 {
		return errors.New("authorityNonce() returned an invalid ABI result")
	}
	nonce := new(big.Int).SetBytes(nonceResult)
	if !nonce.IsUint64() || nonce.Uint64() != epoch {
		return errors.New("on-chain authorityNonce() does not match the ceremony epoch")
	}
	if deployment.chain == "trx" {
		if err := verifyTronTokenGetter(client, deployment, blockTag); err != nil {
			return err
		}
	}
	return nil
}

func verifyDeploymentTransaction(client *jsonRPCClient, deployment verifiedDeployment, epoch uint64, artifact expectedContractArtifact, receipt rpcTransactionReceipt) error {
	var transaction rpcDeploymentTransaction
	err := client.call("eth_getTransactionByHash", []any{deployment.receipt.TxHash}, &transaction)
	if errors.Is(err, errRPCNullResult) {
		var block rpcBlockWithTransactions
		if err := client.call("eth_getBlockByNumber", []any{receipt.BlockNumber, true}, &block); err != nil {
			return fmt.Errorf("query deployment block transactions: %w", err)
		}
		number, numberErr := parseRPCQuantity(block.Number)
		receiptNumber, receiptErr := parseRPCQuantity(receipt.BlockNumber)
		if numberErr != nil || receiptErr != nil || number != receiptNumber || !validNonzeroHash(block.Hash) || !sameHexHash(block.Hash, receipt.BlockHash) {
			return errors.New("deployment transaction block does not match its anchored receipt")
		}
		matches := 0
		for _, candidate := range block.Transactions {
			if sameHexHash(candidate.Hash, deployment.receipt.TxHash) {
				transaction = candidate
				matches++
			}
		}
		if matches != 1 {
			return errors.New("deployment block must contain exactly one matching creation transaction")
		}
	} else if err != nil {
		return fmt.Errorf("query deployment transaction: %w", err)
	}
	if !sameHexHash(transaction.Hash, deployment.receipt.TxHash) ||
		!sameHexHash(transaction.BlockHash, receipt.BlockHash) || transaction.BlockNumber != receipt.BlockNumber {
		return errors.New("deployment transaction does not match its anchored receipt")
	}
	if !bytes.Equal(bytes.TrimSpace(transaction.To), []byte("null")) {
		return errors.New("deployment transaction is not a contract creation")
	}
	from, err := evm.NormalizeAccountAddress(deployment.chain, transaction.From, false)
	if err != nil {
		return errors.New("deployment transaction sender is invalid")
	}
	deployer, err := evm.NormalizeAccountAddress(deployment.chain, deployment.receipt.Deployer, false)
	if err != nil || from != deployer {
		return errors.New("deployment transaction sender does not match the deployment receipt")
	}
	input, err := decodeHexData(transaction.Input)
	if err != nil {
		return errors.New("deployment transaction input is invalid")
	}
	expected, err := expectedDeploymentInput(deployment, epoch, artifact.creationCode)
	if err != nil {
		return err
	}
	if !bytes.Equal(input, expected) {
		return errors.New("deployment transaction input does not equal trusted creation bytecode and canonical constructor arguments")
	}
	return nil
}

func expectedDeploymentInput(deployment verifiedDeployment, epoch uint64, creationCode []byte) ([]byte, error) {
	if len(creationCode) == 0 {
		return nil, errors.New("trusted creation bytecode is empty")
	}
	px, err := canonicalDecimal(deployment.receipt.PX, "px")
	if err != nil || px.BitLen() > 256 {
		return nil, errors.New("deployment px is not a uint256")
	}
	defer px.SetInt64(0)
	rawY, err := canonicalDecimal(deployment.receipt.RawPY, "rawPy")
	if err != nil || rawY.BitLen() > 256 {
		return nil, errors.New("deployment rawPy is not a uint256")
	}
	defer rawY.SetInt64(0)
	input := make([]byte, len(creationCode), len(creationCode)+4*32)
	copy(input, creationCode)
	input = appendUint256(input, px)
	input = appendUint256(input, rawY)
	guardian, err := receiptGuardianKey(&deployment.receipt)
	if err != nil {
		return nil, err
	}
	input = append(input, guardian...)
	input = appendUint256(input, new(big.Int).SetUint64(epoch))
	if deployment.receipt.DepositProtocol == unifiedVaultDepositProtocol {
		if deployment.dexChainID == 0 {
			return nil, errors.New("unified Vault constructor requires a nonzero DEX domain")
		}
		input = appendUint256(input, new(big.Int).SetUint64(deployment.dexChainID))
	}
	if deployment.chain == "trx" {
		tokenWord, err := tronTokenABIWord(deployment.receipt.NonstandardToken)
		if err != nil {
			return nil, err
		}
		input = append(input, tokenWord...)
	}
	return input, nil
}

func appendUint256(destination []byte, value *big.Int) []byte {
	word := make([]byte, 32)
	value.FillBytes(word)
	return append(destination, word...)
}

func tronTokenABIWord(raw string) ([]byte, error) {
	if raw != tronMainnetUSDTAddress {
		return nil, errors.New("TRON immutable token must be the fixed mainnet USDT contract")
	}
	normalized, err := evm.NormalizeAccountAddress("trx", raw, false)
	if err != nil {
		return nil, errors.New("deployment receipt contains an invalid TRON nonstandard token")
	}
	address, err := decodeFixedHex(normalized, 20)
	if err != nil {
		return nil, errors.New("deployment receipt contains an invalid TRON nonstandard token")
	}
	word := make([]byte, 32)
	copy(word[12:], address)
	return word, nil
}

func verifyTronTokenGetter(client *jsonRPCClient, deployment verifiedDeployment, blockTag string) error {
	result, err := callView(client, deployment.address, "nonstandardToken()", blockTag)
	if err != nil || len(result) != 32 {
		return errors.New("nonstandardToken() returned an invalid ABI address")
	}
	expected, err := tronTokenABIWord(deployment.receipt.NonstandardToken)
	if err != nil {
		return err
	}
	if !bytes.Equal(result, expected) {
		return errors.New("on-chain nonstandardToken() does not match fixed TRON USDT")
	}
	return nil
}

func fetchTronSolidHead(endpoint string) (verifiedFinalityAnchor, error) {
	data, err := callTronSolidityNode(endpoint, "/wallet/getnodeinfo", []byte("{}"))
	if err != nil {
		return verifiedFinalityAnchor{}, err
	}
	var node tronNodeInfo
	if err := json.Unmarshal(data, &node); err != nil {
		return verifiedFinalityAnchor{}, errors.New("TRON FullNode returned invalid node info")
	}
	const prefix = "Num:"
	const separator = ",ID:"
	if !strings.HasPrefix(node.SolidityBlock, prefix) {
		return verifiedFinalityAnchor{}, errors.New("TRON FullNode returned an invalid solidity head")
	}
	parts := strings.Split(strings.TrimPrefix(node.SolidityBlock, prefix), separator)
	if len(parts) != 2 {
		return verifiedFinalityAnchor{}, errors.New("TRON FullNode returned an invalid solidity head")
	}
	height, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil || height == 0 || !validNonzeroHash(parts[1]) {
		return verifiedFinalityAnchor{}, errors.New("TRON FullNode returned an invalid solidity head")
	}
	return verifiedFinalityAnchor{blockNumber: height, blockHash: parts[1]}, nil
}

func verifyTronSolidReceipt(endpoint string, deployment verifiedDeployment, expectedBlock, epoch uint64, artifact expectedContractArtifact) error {
	solidHead, err := fetchTronSolidHead(endpoint)
	if err != nil {
		return err
	}
	if solidHead.blockNumber < expectedBlock {
		return errors.New("TRON FullNode solidity head has not covered the deployment block")
	}
	body, _ := json.Marshal(map[string]any{"value": strings.TrimPrefix(deployment.receipt.TxHash, "0x"), "visible": true})
	data, err := callTronSolidityNode(endpoint, "/wallet/gettransactioninfobyid", body)
	if err != nil {
		return err
	}
	var info tronSolidTransactionInfo
	if err := json.Unmarshal(data, &info); err != nil || info.Receipt.Result != "SUCCESS" || !sameHexHash(info.ID, deployment.receipt.TxHash) {
		return errors.New("TRON solidity node has not finalized a successful deployment transaction")
	}
	address, err := evm.NormalizeAccountAddress("trx", info.ContractAddress, false)
	if err != nil || address != deployment.address || expectedBlock == 0 || info.BlockNumber != expectedBlock {
		return errors.New("TRON solidity node deployment address or block is invalid")
	}
	blockBody, _ := json.Marshal(map[string]any{"num": expectedBlock, "visible": true})
	blockData, err := callTronSolidityNode(endpoint, "/wallet/getblockbynum", blockBody)
	if err != nil {
		return err
	}
	var block tronSolidBlock
	if err := json.Unmarshal(blockData, &block); err != nil || block.BlockHeader.RawData.Number != expectedBlock || !sameHexHash(block.BlockID, deployment.blockHash) {
		return errors.New("TRON solidity node block does not match the JSON-RPC receipt anchor")
	}

	rawData, err := callTronSolidityNode(endpoint, "/wallet/gettransactionbyid", body)
	if err != nil {
		return err
	}
	var transaction tronSolidDeploymentTransaction
	if err := json.Unmarshal(rawData, &transaction); err != nil || !sameHexHash(transaction.TxID, deployment.receipt.TxHash) || len(transaction.Ret) != 1 || transaction.Ret[0].ContractRet != "SUCCESS" || len(transaction.RawData.Contracts) != 1 {
		return errors.New("TRON solidity node returned an invalid deployment transaction")
	}
	contract := transaction.RawData.Contracts[0]
	if contract.Type != "CreateSmartContract" || contract.Parameter.TypeURL != "type.googleapis.com/protocol.CreateSmartContract" {
		return errors.New("TRON deployment transaction is not CreateSmartContract")
	}
	deployer, err := evm.NormalizeAccountAddress("trx", deployment.receipt.Deployer, false)
	if err != nil {
		return errors.New("TRON deployment receipt deployer is invalid")
	}
	owner, ownerErr := evm.NormalizeAccountAddress("trx", contract.Parameter.Value.OwnerAddress, false)
	origin, originErr := evm.NormalizeAccountAddress("trx", contract.Parameter.Value.NewContract.OriginAddress, false)
	if ownerErr != nil || originErr != nil || owner != deployer || origin != deployer {
		return errors.New("TRON deployment transaction owner or origin does not match the deployer")
	}
	actualInput, err := decodeHexData("0x" + strings.TrimPrefix(strings.TrimPrefix(contract.Parameter.Value.NewContract.Bytecode, "0x"), "0X"))
	if err != nil {
		return errors.New("TRON deployment transaction bytecode is invalid")
	}
	expectedInput, err := expectedDeploymentInput(deployment, epoch, artifact.creationCode)
	if err != nil {
		return err
	}
	if !bytes.Equal(actualInput, expectedInput) {
		return errors.New("TRON deployment transaction does not contain trusted creation bytecode and canonical constructor arguments")
	}
	return nil
}

func callTronSolidityNode(endpoint, path string, body []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), externalRPCTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(endpoint, "/")+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := hardenedHTTPClient().Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("solidity node HTTP status %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxRPCBodyBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxRPCBodyBytes {
		return nil, errors.New("invalid solidity node response")
	}
	if err := validateJSONStructure(data); err != nil {
		return nil, errors.New("invalid solidity node response")
	}
	return data, nil
}

func callView(client *jsonRPCClient, address, signature, blockTag string) ([]byte, error) {
	selector := keccak256([]byte(signature))[:4]
	var result string
	if blockTag == "" {
		return nil, errors.New("eth_call requires a fixed block anchor")
	}
	if err := client.call("eth_call", []any{map[string]string{"to": address, "data": "0x" + hex.EncodeToString(selector)}, blockTag}, &result); err != nil {
		return nil, fmt.Errorf("call %s: %w", signature, err)
	}
	return decodeHexData(result)
}

func parseRPCQuantity(value string) (uint64, error) {
	if !strings.HasPrefix(value, "0x") || len(value) < 3 || len(value) > 18 || len(value) > 3 && value[2] == '0' {
		return 0, errors.New("noncanonical RPC quantity")
	}
	return strconv.ParseUint(value[2:], 16, 64)
}

func decodeHexData(value string) ([]byte, error) {
	if !strings.HasPrefix(value, "0x") || len(value)%2 != 0 {
		return nil, errors.New("invalid hex data")
	}
	return hex.DecodeString(value[2:])
}

func decodeFixedHex(value string, size int) ([]byte, error) {
	trimmed := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(value), "0x"), "0X")
	if len(trimmed) != size*2 {
		return nil, errors.New("invalid fixed-width hex")
	}
	return hex.DecodeString(trimmed)
}

func sameHexHash(left, right string) bool {
	a, errA := decodeFixedHex(left, 32)
	b, errB := decodeFixedHex(right, 32)
	return errA == nil && errB == nil && bytes.Equal(a, b)
}

func validNonzeroHash(value string) bool {
	decoded, err := decodeFixedHex(value, 32)
	return err == nil && !allZero(decoded)
}

func keccak256(data []byte) []byte {
	hash := sha3.NewLegacyKeccak256()
	_, _ = hash.Write(data)
	return hash.Sum(nil)
}
