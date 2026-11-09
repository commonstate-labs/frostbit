const path = require('path');
const fs = require('fs');
const { isDeepStrictEqual } = require('util');
const { Artifacts } = require('hardhat/internal/artifacts');
const deploymentTools = require('./vault_deployment');
const { ethers } = require('ethers');
const artifactStore = { artifacts: new Artifacts(path.join(__dirname, '../artifacts')) };
const NETWORK_IDS = { ethereum: 1n, base: 8453n, bnb: 56n, arbitrum: 42161n,
  sepolia: 11155111n, baseSepolia: 84532n, bnbTestnet: 97n, arbSepolia: 421614n, hardhat: 31337n };
const MAINNET_IDS = new Set([1n, 56n, 8453n, 42161n]);
const MAX_DEPLOYMENT_SEARCH_BLOCKS = 256;

function requireEnv(name) {
  const value = process.env[name];
  if (!value || !value.trim()) {
    throw new Error(`missing env ${name}`);
  }
  return value.trim();
}

function normalizeHex(value) {
  const raw = value.trim();
  return raw.startsWith("0x") || raw.startsWith("0X") ? raw.slice(2) : raw;
}

function resolvePubkey() {
  if (process.env.VAULT_GROUP_PUBKEY && process.env.VAULT_GROUP_PUBKEY.trim()) {
    const raw = normalizeHex(process.env.VAULT_GROUP_PUBKEY);
    if (!/^[0-9a-fA-F]{128}$/.test(raw)) {
      throw new Error("VAULT_GROUP_PUBKEY must be 64 bytes hex");
    }
    return { px: `0x${raw.slice(0, 64)}`, py: `0x${raw.slice(64)}` };
  }
  return { px: requireEnv("VAULT_PX"), py: requireEnv("VAULT_PY") };
}

function validatePubkey(pxInput, pyInput) {
  return deploymentTools.validatePubkey(pxInput, pyInput);
}

function uint64Env(name) {
  const value = requireEnv(name);
  if (!/^(0|[1-9][0-9]*)$/.test(value) || BigInt(value) > (1n << 64n) - 1n) {
    throw new Error(`${name} must be a decimal uint64`);
  }
  return BigInt(value);
}

function validateLiveDeployment(chainId) {
  if (MAINNET_IDS.has(chainId) && process.env.VAULT_ALLOW_MAINNET_DEPLOY !== "true") {
    throw new Error("mainnet deployment requires explicit VAULT_ALLOW_MAINNET_DEPLOY=true");
  }
}

function expectedDeployerAddress(value) {
  if (value === undefined || value === null) return null;
  if (typeof value !== 'string' || !/^0x[0-9a-fA-F]{40}$/.test(value)) throw new Error('VAULT_DEPLOYER_ADDRESS must be an EVM address');
  const address = ethers.getAddress(value);
  if (address === ethers.ZeroAddress) throw new Error('VAULT_DEPLOYER_ADDRESS cannot be zero');
  return address;
}
function checkExpectedDeployer(expected, actual) {
  if (expected !== null && ethers.getAddress(actual) !== expected) throw new Error('deployment payer differs from VAULT_DEPLOYER_ADDRESS');
}
function checkRebroadcastMode(options, existing) {
  if (!options.rebroadcastTxFile) return;
  if (!existing || existing.status !== 'pending') throw new Error('VAULT_REBROADCAST_TX_FILE requires an existing pending journal');
  if (MAINNET_IDS.has(options.chainId) && !options.allowMainnet) throw new Error('mainnet rebroadcast requires explicit VAULT_ALLOW_MAINNET_DEPLOY=true');
}

function parseOptions(env = process.env, network = env.VAULT_NETWORK || env.HARDHAT_NETWORK || 'hardhat') {
                                                                              
  const raw = env.VAULT_GROUP_PUBKEY ? normalizeHex(env.VAULT_GROUP_PUBKEY) : null;
  if (raw !== null && !/^[0-9a-fA-F]{128}$/.test(raw)) throw new Error('VAULT_GROUP_PUBKEY must be 64 bytes hex');
  const point = validatePubkey(raw ? `0x${raw.slice(0, 64)}` : env.VAULT_PX, raw ? `0x${raw.slice(64)}` : env.VAULT_PY);
  const chainId = deploymentTools.uint64(env.VAULT_CHAIN_ID, 'VAULT_CHAIN_ID', true);
  const dexChainId = deploymentTools.uint64(env.VAULT_DEX_CHAIN_ID, 'VAULT_DEX_CHAIN_ID', true);
  const epoch = deploymentTools.uint64(env.VAULT_EPOCH, 'VAULT_EPOCH');
  const vaultId = deploymentTools.uint64(env.VAULT_ID, 'VAULT_ID');
  if (NETWORK_IDS[network] !== undefined && chainId !== NETWORK_IDS[network]) throw new Error('requested chain ID differs from the Hardhat network');
  const timeoutMs = Number(env.VAULT_CONFIRM_TIMEOUT_MS || 300000);
  const confirmations = Number(env.VAULT_CONFIRMATIONS || 1);
  if (!Number.isSafeInteger(timeoutMs) || timeoutMs <= 0 || !Number.isSafeInteger(confirmations) || confirmations <= 0) throw new Error('confirmation timeout and count must be positive safe integers');
  const rpcNames = { ethereum: 'ETHEREUM', base: 'BASE', bnb: 'BNB', arbitrum: 'ARBITRUM', sepolia: 'SEPOLIA', baseSepolia: 'BASE_SEPOLIA', bnbTestnet: 'BNB_TESTNET', arbSepolia: 'ARB_SEPOLIA' };
  const rpcUrl = env.VAULT_RPC_URL || env[`RPC_URL_${rpcNames[network]}`];
  if (rpcUrl) validateRPCURL(rpcUrl);
  const recoverBlockNumber = env.VAULT_RECOVER_BLOCK_NUMBER === undefined ? null : deploymentTools.deploymentBlockNumber(
    Number(deploymentTools.uint64(env.VAULT_RECOVER_BLOCK_NUMBER, 'VAULT_RECOVER_BLOCK_NUMBER', true)), 'VAULT_RECOVER_BLOCK_NUMBER');
  if (env.VAULT_REBROADCAST_TX_FILE !== undefined && !env.VAULT_REBROADCAST_TX_FILE.trim()) throw new Error('VAULT_REBROADCAST_TX_FILE cannot be empty');
  return { ...point, ...deploymentTools.guardianFromConfig(env), artifactsDir: env.VAULT_ARTIFACTS_DIR === undefined ? undefined : deploymentTools.resolveVaultArtifactsDirectory(env.VAULT_ARTIFACTS_DIR), authorityProtocol: deploymentTools.authorityProtocol(env.VAULT_AUTHORITY_PROTOCOL), network, chainId, dexChainId, epoch, vaultId, timeoutMs, confirmations,
    rpcUrl, recoverBlockNumber, expectedDeployer: expectedDeployerAddress(env.VAULT_DEPLOYER_ADDRESS),
    rebroadcastTxFile: env.VAULT_REBROADCAST_TX_FILE === undefined ? null : path.resolve(env.VAULT_REBROADCAST_TX_FILE),
    output: deploymentTools.resolveVaultOutput(env, network, chainId, dexChainId, vaultId),
    recoverTxHash: env.VAULT_RECOVER_TX_HASH || null, allowMainnet: env.VAULT_ALLOW_MAINNET_DEPLOY === 'true' };
}

async function validateRebroadcastTransaction(raw, options, artifact, existing) {
  if (!existing || existing.status !== 'pending') throw new Error('original transaction replay requires a pending journal');
  const identity = deploymentTools.vaultIdentity(options, artifact);
  for (const [key, value] of Object.entries(identity)) if (!isDeepStrictEqual(existing[key], value)) throw new Error(`pending journal conflicts with ${key}`);
  if (typeof raw !== 'string' || raw.length > 1024 * 1024 || !/^0x(?:[0-9a-fA-F]{2})+$/.test(raw.trim())) throw new Error('invalid signed transaction file');
  const tx = ethers.Transaction.from(raw.trim());
  const creation = await new ethers.ContractFactory(artifact.abi, artifact.bytecode).getDeployTransaction(options.px, options.rawPy, options.guardianPx, options.guardianPy, options.epoch, options.dexChainId);
  const hash = deploymentTools.canonicalTxHash(existing.txHash, identity);
  if (!tx.signature || tx.hash !== hash || tx.chainId !== options.chainId || tx.to !== null || tx.value !== 0n || tx.data !== creation.data ||
      tx.from !== ethers.getAddress(existing.deployer) || (options.recoverTxHash && options.recoverTxHash !== hash)) throw new Error('signed transaction differs from the original pending Vault creation');
  const address = ethers.getCreateAddress({ from: tx.from, nonce: tx.nonce });
  if (address !== ethers.getAddress(existing.address)) throw new Error('signed transaction creates a different pending Vault address');
  checkExpectedDeployer(expectedDeployerAddress(options.expectedDeployer), tx.from);
  if (![0, 1, 2].includes(tx.type) || tx.gasLimit <= 0n) throw new Error('unsupported original Vault transaction type or gas limit');
  const feeCap = tx.type === 2 ? tx.maxFeePerGas : tx.gasPrice;
  if (feeCap === null || feeCap < 0n) throw new Error('invalid original transaction fee');
  return { txHash: hash, address, deployer: tx.from, chainId: tx.chainId.toString(), nonce: tx.nonce, type: tx.type,
    gasLimit: tx.gasLimit.toString(), feeCap: feeCap.toString(), maxCostWei: (tx.gasLimit * feeCap + tx.value).toString() };
}

function readOriginalTransaction(filename) {
  const stat = fs.lstatSync(filename);
  if (!stat.isFile() || stat.isSymbolicLink() || stat.nlink !== 1 || stat.size <= 0 || stat.size > 1024 * 1024) throw new Error('unsafe signed transaction file');
  return fs.readFileSync(filename, 'utf8').trim();
}

function validateRPCURL(raw) {
  let url;
  try { url = new URL(raw); } catch { throw new Error('VAULT_RPC_URL or the network RPC_URL must be supplied'); }
  if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.hash) throw new Error('Vault RPC must use HTTP(S) without embedded credentials or fragments');
  return url.toString();
}

function rpcHash(value, label) {
  if (typeof value !== 'string' || !/^0x[0-9a-fA-F]{64}$/.test(value) || /^0x0+$/.test(value)) throw new Error(`invalid ${label}`);
  return value.toLowerCase();
}
function rpcQuantity(value, label) {
  if (typeof value !== 'string' || !/^0x(?:0|[1-9a-fA-F][0-9a-fA-F]*)$/.test(value)) throw new Error(`invalid ${label}`);
  return BigInt(value);
}
function rpcNumber(value, label) {
  const number = Number(rpcQuantity(value, label));
  if (!Number.isSafeInteger(number)) throw new Error(`invalid ${label}`);
  return number;
}
function blockAnchor(block, height, hash) {
  if (!block || rpcNumber(block.number, 'block number') !== height || (hash && rpcHash(block.hash, 'block hash') !== hash)) throw new Error('deployment block anchor mismatch');
  return rpcHash(block.hash, 'block hash');
}
async function beforeDeadline(promise, deadline) {
  let timer;
  try {
    return await Promise.race([promise, new Promise((_, reject) => {
      timer = setTimeout(() => reject(new Error('deployment evidence query timed out; retain the journal')), Math.max(0, deadline - Date.now()));
    })]);
  } finally { clearTimeout(timer); }
}
async function receiptAtBlock(provider, txHash, height, deadline, knownBlock) {
  const tag = ethers.toQuantity(height);
  const block = knownBlock || await beforeDeadline(provider.send('eth_getBlockByNumber', [tag, false]), deadline);
  const blockHash = blockAnchor(block, height);
  const receipts = await beforeDeadline(provider.send('eth_getBlockReceipts', [tag]), deadline);
  if (!Array.isArray(receipts)) throw new Error('invalid deployment block receipts');
  const matches = receipts.filter(item => item && typeof item.transactionHash === 'string' && item.transactionHash.toLowerCase() === txHash);
  if (matches.length !== 1) throw new Error('deployment block must contain exactly one matching receipt');
  const raw = matches[0];
  if (rpcHash(raw.transactionHash, 'receipt transaction hash') !== txHash || rpcNumber(raw.blockNumber, 'receipt block number') !== height || rpcHash(raw.blockHash, 'receipt block hash') !== blockHash) throw new Error('deployment receipt block anchor mismatch');
  return { hash: txHash, blockNumber: height, blockHash, status: rpcNumber(raw.status, 'receipt status'), contractAddress: raw.contractAddress };
}
async function transactionAtBlock(provider, txHash, receipt, deadline) {
  const block = await beforeDeadline(provider.send('eth_getBlockByNumber', [ethers.toQuantity(receipt.blockNumber), true]), deadline);
  blockAnchor(block, receipt.blockNumber, receipt.blockHash);
  if (!Array.isArray(block.transactions)) throw new Error('invalid deployment block transactions');
  const matches = block.transactions.filter(item => item && typeof item.hash === 'string' && item.hash.toLowerCase() === txHash);
  if (matches.length !== 1) throw new Error('deployment block must contain exactly one matching transaction');
  const raw = matches[0];
  return { hash: rpcHash(raw.hash, 'transaction hash'), blockHash: rpcHash(raw.blockHash, 'transaction block hash'),
    blockNumber: rpcNumber(raw.blockNumber, 'transaction block number'), from: raw.from, to: raw.to,
    data: raw.input, value: rpcQuantity(raw.value, 'transaction value'), chainId: rpcQuantity(raw.chainId, 'transaction chain ID'), nonce: rpcNumber(raw.nonce, 'transaction nonce') };
}
async function waitForDeploymentReceipt(provider, txHash, confirmations, timeoutMs, hints = {}) {
  const deadline = Date.now() + timeoutMs;
  let exactBlock = hints.blockNumber == null ? null : deploymentTools.deploymentBlockNumber(hints.blockNumber, 'recovery blockNumber');
  let nextBlock = hints.searchFromBlock == null ? null : deploymentTools.deploymentBlockNumber(hints.searchFromBlock, 'pending searchFromBlock');
  let searched = 0;
  do {
    let receipt = await beforeDeadline(provider.getTransactionReceipt(txHash), deadline);
    const latest = await beforeDeadline(provider.getBlockNumber(), deadline);
    if (!Number.isSafeInteger(latest) || latest < 0) throw new Error('invalid RPC block height');
                                                                              
                                                                                 
    if (receipt === null && exactBlock !== null) receipt = await receiptAtBlock(provider, txHash, exactBlock, deadline);
    if (receipt === null && nextBlock !== null) {
      while (nextBlock <= latest && searched < MAX_DEPLOYMENT_SEARCH_BLOCKS) {
        if (Date.now() >= deadline) throw new Error('deployment evidence search timed out; retain the journal');
        const height = nextBlock++, block = await beforeDeadline(provider.send('eth_getBlockByNumber', [ethers.toQuantity(height), false]), deadline);
        blockAnchor(block, height);
        if (!Array.isArray(block.transactions) || block.transactions.some(hash => typeof hash !== 'string')) throw new Error('invalid deployment block transaction hashes');
        const matches = block.transactions.filter(hash => rpcHash(hash, 'block transaction hash') === txHash);
        if (matches.length > 1) throw new Error('duplicate deployment transaction in block');
        searched++;
        if (matches.length === 1) { receipt = await receiptAtBlock(provider, txHash, height, deadline, block); break; }
      }
      if (receipt === null && searched === MAX_DEPLOYMENT_SEARCH_BLOCKS) throw new Error('deployment search reached 256 blocks; retain the journal and supply VAULT_RECOVER_BLOCK_NUMBER');
    }
    if (receipt !== null) {
      if (!receipt || receipt.status !== 1 || receipt.hash !== txHash || !Number.isSafeInteger(receipt.blockNumber) || receipt.blockNumber <= 0) throw new Error(`deployment ${txHash} is missing or unsuccessful; retain the journal`);
      rpcHash(receipt.blockHash, 'receipt block hash');
      if (exactBlock !== null && receipt.blockNumber !== exactBlock) throw new Error('recovery block hint differs from deployment receipt');
      if (latest >= receipt.blockNumber && latest - receipt.blockNumber >= confirmations - 1) return receipt;
                                                                                
      exactBlock = receipt.blockNumber;
    }
    if (Date.now() >= deadline) break;
    await new Promise(resolve => setTimeout(resolve, Math.min(1000, deadline - Date.now())));
  } while (Date.now() < deadline);
  throw new Error(`deployment ${txHash} was not confirmed before timeout; retain the journal`);
}

async function deployEvmVault(options, artifact, existing, onPending, dependencies = {}) {
  checkRebroadcastMode(options, existing);
  const expectedDeployer = expectedDeployerAddress(options.expectedDeployer);
  if (existing) checkExpectedDeployer(expectedDeployer, existing.deployer);
  const provider = dependencies.provider || new ethers.JsonRpcProvider(validateRPCURL(options.rpcUrl), undefined, { batchMaxCount: 1 });
  try {
  const network = await provider.getNetwork();
  if (network.chainId !== options.chainId) throw new Error(`RPC chain ID ${network.chainId} differs from requested ${options.chainId}`);
  const identity = deploymentTools.vaultIdentity(options, artifact);
  const explicitHash = options.recoverTxHash ? deploymentTools.canonicalTxHash(options.recoverTxHash, identity) : null;
  if (existing && explicitHash && existing.txHash !== explicitHash) throw new Error('recovery transaction differs from the saved deployment');
  let txHash = existing ? existing.txHash : explicitHash;
  const recoverBlockNumber = options.recoverBlockNumber == null ? null : deploymentTools.deploymentBlockNumber(options.recoverBlockNumber, 'VAULT_RECOVER_BLOCK_NUMBER');
  if (!txHash && recoverBlockNumber !== null) throw new Error('VAULT_RECOVER_BLOCK_NUMBER requires an existing journal or VAULT_RECOVER_TX_HASH');
  if (existing?.status === 'verified' && recoverBlockNumber !== null && existing.blockNumber !== recoverBlockNumber) throw new Error('recovery block differs from verified journal');
  const factory = new ethers.ContractFactory(artifact.abi, artifact.bytecode);
  const creation = await factory.getDeployTransaction(options.px, options.rawPy, options.guardianPx, options.guardianPy, options.epoch, options.dexChainId);
  let pending = existing;
  let receiptBlockHint = existing?.status === 'verified' ? existing.blockNumber : recoverBlockNumber;
  let searchFromBlock = pending?.searchFromBlock;
  if (!txHash) {
    if (MAINNET_IDS.has(options.chainId) && !options.allowMainnet) throw new Error('mainnet deployment requires explicit VAULT_ALLOW_MAINNET_DEPLOY=true');
    if (typeof onPending !== 'function') throw new Error('a durable pending callback is required before deployment');
    searchFromBlock = deploymentTools.deploymentBlockNumber((await provider.getBlockNumber()) + 1, 'pending searchFromBlock');
    const signer = dependencies.signer || new ethers.Wallet(options.privateKey || requireEnv('DEPLOYER_PRIVATE_KEY'), provider);
    const deployer = await signer.getAddress();
    checkExpectedDeployer(expectedDeployer, deployer);
    const populated = await signer.populateTransaction({ ...creation, value: 0n });
    const signed = await signer.signTransaction(populated);
    const parsed = ethers.Transaction.from(signed);
    if (parsed.chainId !== options.chainId || parsed.to !== null || parsed.value !== 0n || parsed.data !== creation.data || parsed.from !== ethers.getAddress(deployer) || parsed.nonce !== populated.nonce) throw new Error('signed deployment transaction differs from requested Vault creation');
    txHash = ethers.keccak256(signed);
    pending = { txHash, deployer, address: ethers.getCreateAddress({ from: deployer, nonce: parsed.nonce }), searchFromBlock };
    await onPending(pending);
    const broadcast = await provider.broadcastTransaction(signed);
    if (!broadcast || broadcast.hash !== txHash) throw new Error(`deployment ${txHash} broadcast identity mismatch; recover the saved transaction`);
  }
  if (options.rebroadcastTxFile) {
    const raw = readOriginalTransaction(options.rebroadcastTxFile);
    const original = await validateRebroadcastTransaction(raw, options, artifact, existing);
    const deadline = Date.now() + options.timeoutMs;
    const seen = await beforeDeadline(provider.getTransactionReceipt(txHash), deadline);
    if (seen !== null) {
      if (!seen || seen.hash !== txHash || seen.status !== 1) throw new Error('original deployment receipt is malformed or unsuccessful; retain the journal');
      receiptBlockHint = deploymentTools.deploymentBlockNumber(seen.blockNumber, 'original receipt block');
    } else {
      const known = await beforeDeadline(provider.getTransaction(txHash), deadline);
      if (known !== null) {
        if (!known || known.hash !== txHash || !Number.isSafeInteger(known.blockNumber) || known.blockNumber <= 0) throw new Error('original transaction is already known or has invalid evidence; wait and verify without rebroadcasting');
        receiptBlockHint = known.blockNumber;
      } else {
                                                                               
                                                                                 
                                                                                  
        searchFromBlock = deploymentTools.deploymentBlockNumber((await beforeDeadline(provider.getBlockNumber(), deadline)) + 1, 'rebroadcast searchFromBlock');
        const [latestNonce, pendingNonce, balance, code] = await Promise.all([
          beforeDeadline(provider.getTransactionCount(original.deployer, 'latest'), deadline),
          beforeDeadline(provider.getTransactionCount(original.deployer, 'pending'), deadline),
          beforeDeadline(provider.getBalance(original.deployer, 'pending'), deadline),
          beforeDeadline(provider.getCode(original.address), deadline),
        ]);
        if (latestNonce !== original.nonce || pendingNonce !== original.nonce) throw new Error('original nonce is no longer free; retain the journal and verify the original transaction');
        if (code !== '0x') throw new Error('the pending Vault address already has code; verify it without rebroadcasting');
        if (typeof balance !== 'bigint' || balance < BigInt(original.maxCostWei)) throw new Error('pending payer balance cannot cover the unchanged original maximum transaction cost');
        receiptBlockHint = null;
        const broadcast = await beforeDeadline(provider.broadcastTransaction(raw), deadline);
        if (!broadcast || broadcast.hash !== txHash) throw new Error('original transaction rebroadcast identity mismatch; retain the journal');
      }
    }
  }
  const receipt = await waitForDeploymentReceipt(provider, txHash, options.confirmations, options.timeoutMs, {
    blockNumber: receiptBlockHint, searchFromBlock,
  });
  if (!receipt || receipt.status !== 1 || receipt.hash !== txHash || !Number.isSafeInteger(receipt.blockNumber) || receipt.blockNumber <= 0) throw new Error(`deployment ${txHash} is missing or unsuccessful; retain the journal`);
  let transaction = await provider.getTransaction(txHash);
  if (transaction === null) transaction = await transactionAtBlock(provider, txHash, receipt, Date.now() + options.timeoutMs);
  if (!transaction || transaction.hash !== txHash || transaction.to !== null || transaction.data !== creation.data || transaction.value !== 0n || transaction.chainId !== options.chainId || transaction.blockHash !== receipt.blockHash || transaction.blockNumber !== receipt.blockNumber) throw new Error('deployment transaction input or block anchor differs from the requested Vault');
  checkExpectedDeployer(expectedDeployer, transaction.from);
  const address = ethers.getCreateAddress({ from: transaction.from, nonce: transaction.nonce });
  if (!receipt.contractAddress || ethers.getAddress(receipt.contractAddress) !== address ||
      (pending && (ethers.getAddress(pending.address) !== address || ethers.getAddress(pending.deployer) !== ethers.getAddress(transaction.from)))) throw new Error('deployment address or payer differs from the saved transaction');
  const actualCode = await provider.getCode(address);
  const expectedCode = deploymentTools.expectedVaultRuntime(artifact, options.dexChainId);
  if (actualCode.toLowerCase() !== expectedCode) throw new Error('deployed Vault runtime or immutable DEX domain differs from artifact');
  const contract = new ethers.Contract(address, artifact.abi, provider);
  const [point, guardian, epoch, domain] = await Promise.all([contract.P(), contract.G(), contract.authorityNonce(), contract.dexChainId()]);
  if (guardian[0]!==options.guardianPx||guardian[1]!==options.guardianPy||point[0] !== options.px || point[1] !== options.py || epoch !== options.epoch || domain !== options.dexChainId) throw new Error('deployed Vault authority or DEX domain differs from requested parameters');
  if (deploymentTools.authorityProtocol(options.authorityProtocol) === 3) {
    const [protocol, limit] = await Promise.all([contract.AUTHORITY_PROTOCOL(), contract.MAX_HANDOVERS_PER_CALL()]);
    deploymentTools.validateVaultCapabilities(3, protocol, limit);
  }
  const runtimeCodeHash = ethers.keccak256(actualCode);
  if (existing && existing.runtimeCodeHash && existing.runtimeCodeHash !== runtimeCodeHash) throw new Error('runtime hash differs from saved verification');
  const block = await provider.getBlock(receipt.blockNumber);
  if (!block || block.hash !== receipt.blockHash || block.number !== receipt.blockNumber) throw new Error('deployment receipt block is not canonical');
  return { txHash, address, deployer: ethers.getAddress(transaction.from), runtimeCodeHash,
    blockNumber: receipt.blockNumber, deployedAt: new Date(block.timestamp * 1000).toISOString() };
  } finally { if (!dependencies.provider) provider.destroy(); }
}

async function run(options, dependencies = {}) {
  const artifact = dependencies.artifact || await deploymentTools.loadVaultArtifact(options.artifactsDir ? deploymentTools.vaultArtifactStore(options.artifactsDir) : dependencies.hre || artifactStore, deploymentTools.vaultContractName(options.authorityProtocol));
  const identity = deploymentTools.vaultIdentity(options, artifact);
  let existing = deploymentTools.loadVaultJournal(options.output, identity);
  checkRebroadcastMode(options, existing);
  if (existing) checkExpectedDeployer(expectedDeployerAddress(options.expectedDeployer), existing.deployer);
  if (options.recoverTxHash) deploymentTools.canonicalTxHash(options.recoverTxHash, identity);
  if (!existing && !options.recoverTxHash && MAINNET_IDS.has(options.chainId) && !options.allowMainnet) throw new Error('mainnet deployment requires explicit VAULT_ALLOW_MAINNET_DEPLOY=true');
                                                                                    
  const safeOptions = Object.fromEntries(Object.keys(options).filter(key => key !== 'privateKey').map(key => [key, options[key]]));
  if (!existing && !options.recoverTxHash && Object.hasOwn(options, 'privateKey')) safeOptions.privateKey = options.privateKey;
  const deploy = dependencies.deploy || deployEvmVault;
  const details = await deploy(safeOptions, artifact, existing, async pending => { existing = deploymentTools.writeVaultPending(options.output, identity, pending); }, dependencies);
  return deploymentTools.writeVaultVerified(options.output, identity, details, existing);
}

async function main() {
  const options = parseOptions();
  const result = await run(options);
  console.log(JSON.stringify({ address: result.address, txHash: result.txHash, receipt: options.output, status: result.status, dexChainId: result.dexChainId, authorityProtocol: result.authorityProtocol }));
}

function formatDeploymentError(error) {
  const concise = (value, limit) => typeof value === 'string' ? value
    .replace(/(?:0x)?[0-9a-fA-F]{64,}/g, '<hex redacted>')
    .replace(/((?:private.?key|mnemonic|seed.?phrase)\s*[:=]\s*)\S+/gi, '$1<redacted>')
    .replace(/[\r\n\t]+/g, ' ').slice(0, limit) : '';
  const code = typeof error?.code === 'string' && /^[A-Z0-9_]{1,48}$/.test(error.code) ? error.code : 'DEPLOYMENT_ERROR';
                                                                             
                                                                                 
  const short = concise(error?.shortMessage || (error?.code ? '' : error?.message), 240);
  const rpc = concise(error?.info?.error?.message || error?.error?.message, 200);
  return [`code=${code}`, short && `shortMessage=${short}`, rpc && `rpc=${rpc}`,
    'Deployment was not verified. Retain the journal and verify the original transaction before any new payment.'].filter(Boolean).join('; ');
}

                                                                                 
                                                                                    

if (require.main === module) {
  main().catch((err) => {
    console.error(formatDeploymentError(err));
    process.exitCode = 1;
  });
}

module.exports = { validatePubkey, resolvePubkey, uint64Env, validateLiveDeployment, parseOptions, deployEvmVault, run, waitForDeploymentReceipt, formatDeploymentError, validateRebroadcastTransaction };
