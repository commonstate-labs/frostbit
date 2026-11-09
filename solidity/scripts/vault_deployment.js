                                                                                      
const fs = require('fs');
const path = require('path');
const { isDeepStrictEqual } = require('util');
const { ethers } = require('ethers');
const { TronWeb } = require('tronweb');

const DEPOSIT_PROTOCOL = 'DEX_VAULT_DEPOSIT_V1';
const WITHDRAW_PROTOCOL = 'DEX_VAULT_MIXED_WITHDRAW_BATCH_V1';
const REQUIRED_WITHDRAW_ABI = [
 'withdrawBatch((bytes32,uint256,address,uint256,uint256,address[],(bytes32,address,address,uint256,uint256)[]),(uint256,uint256,uint256),(uint256,uint256,uint256))',
 'claimReward(address,address)', 'G()',
 'withdrawBatchWithHandovers((bytes32,uint256,address,uint256,uint256,address[],(bytes32,address,address,uint256,uint256)[]),(uint256,uint256,uint256),(uint256,uint256,uint256),(uint256,uint256,uint256,uint256,uint256,uint256,uint256,uint256,uint256,uint256,uint256)[])',
 'catchUpAuthority((uint256,uint256,uint256,uint256,uint256,uint256,uint256,uint256,uint256,uint256,uint256)[])',
 'AUTHORITY_PROTOCOL()', 'MAX_HANDOVERS_PER_CALL()',
];
const REQUIRED_V2_ABI = REQUIRED_WITHDRAW_ABI;
const REMOVED_WITHDRAW_ABI_NAMES = new Set(['withdraw', 'withdrawWithHandover', 'changePub','withdrawBatchWithHandover']);
function guardianFromConfig(env=process.env) {
 const i=process.argv.indexOf('--frost-config');
 const filename=env.VAULT_FROST_CONFIG || (i>=0?process.argv[i+1]:null);
 if (!filename) throw new Error('supply --frost-config or VAULT_FROST_CONFIG; guardian public key is read only from that configuration');
 const config=JSON.parse(fs.readFileSync(path.resolve(filename),'utf8'));
 const raw=config.guardian?.accountPublicKey?.replace(/^0x/,'');
 if(typeof raw!=='string'||!/^[0-9a-fA-F]{128}$/.test(raw))throw new Error('guardian.accountPublicKey must be a nonempty 64-byte BN128 public key');
 const p=validatePubkey('0x'+raw.slice(0,64),'0x'+raw.slice(64));
 if(p.rawPy!==p.py)throw new Error('guardian.accountPublicKey must use canonical BN128 encoding');
 return {guardianPx:p.px,guardianPy:p.py};
}
function safeArtifactPath(value, directory = false) {
  const absolute = path.resolve(value), stat = fs.lstatSync(absolute);
  const same = text => process.platform === 'win32' ? text.toLowerCase() : text;
  if (stat.isSymbolicLink() || (directory ? !stat.isDirectory() : !stat.isFile() || stat.nlink !== 1 || stat.size > 64 * 1024 * 1024) || same(fs.realpathSync(absolute)) !== same(absolute)) throw new Error('unsafe Vault artifacts path');
  return absolute;
}
function resolveVaultArtifactsDirectory(value) { return safeArtifactPath(value, true); }
function vaultArtifactStore(directory) {
  const root = resolveVaultArtifactsDirectory(directory);
  const store = new (require('hardhat/internal/artifacts').Artifacts)(root), allowed = new Set();
  return { artifacts: {
    readArtifact: async name => {
      const version = contractProtocol(name);
      const source = version === 3 ? 'SchnorrVaultV3.sol' : isTronContract(name) ? 'schnorr_verify_tron.sol' : 'schnorr_verify.sol';
      const filename = safeArtifactPath(path.join(root, 'contracts', source, name + '.json'));
      const debugFile = safeArtifactPath(filename.replace(/\.json$/, '.dbg.json'));
      const debug = JSON.parse(fs.readFileSync(debugFile, 'utf8'));
      if (typeof debug.buildInfo !== 'string') throw new Error('invalid Vault artifact build-info reference');
      const build = path.resolve(path.dirname(filename), debug.buildInfo), relative = path.relative(root, build);
      if (path.isAbsolute(relative) || relative.startsWith('..')) throw new Error('Vault build-info escapes artifacts');
      safeArtifactPath(build);
      const artifact = await store.readArtifact(name);
      if (artifact.sourceName !== `contracts/${source}` || artifact.contractName !== name) throw new Error('Vault artifact factory/source mismatch');
      allowed.add(`${artifact.sourceName}:${name}`);
      return artifact;
    },
    getBuildInfo: async name => {
      if (!allowed.has(name)) throw new Error('unvalidated Vault artifact build-info');
      return store.getBuildInfo(name);
    },
  } };
}
function authorityProtocol(value) {
 if(value!==undefined && value!==3 && value!=='3')throw new Error('VAULT_AUTHORITY_PROTOCOL must be 3');
 return 3;
}
function vaultContractName(protocol,tron=false){authorityProtocol(protocol);return `SchnorrExample${tron?'Tron':''}V3`;}
function contractProtocol(name){if(!['SchnorrExampleV3','SchnorrExampleTronV3'].includes(name))throw new Error('unsupported dual-authority Vault contract');return 3;}
function isTronContract(name){contractProtocol(name);return name.includes('Tron');}
function validateVaultCapabilities(protocol,actualProtocol,limit){authorityProtocol(protocol);if(BigInt(actualProtocol)!==3n||BigInt(limit)!==8n)throw new Error('deployed Vault authority protocol/capabilities differ from V3');}
const FIELD = 21888242871839275222246405745257275088696311157297823662689037894645226208583n;
function uint64(value, name, positive = false) {
  if (typeof value === 'number' && !Number.isSafeInteger(value)) throw new Error(`${name} cannot be an unsafe JavaScript number`);
  const raw = String(value === undefined ? '' : value);
  if (!/^(0|[1-9][0-9]*)$/.test(raw) || BigInt(raw) >= 1n << 64n || (positive && BigInt(raw) === 0n)) throw new Error(`${name} must be a ${positive ? 'nonzero ' : ''}decimal uint64`);
  return BigInt(raw);
}
function validatePubkey(pxInput, pyInput) {
  const px = BigInt(pxInput), rawPy = BigInt(pyInput);
  if (px < 0n || rawPy < 0n || px >= FIELD || rawPy >= FIELD || (px === 0n && rawPy === 0n) || rawPy * rawPy % FIELD !== (px * px % FIELD * px + 3n) % FIELD) throw new Error('VAULT public key must be a non-infinity BN128 curve point');
  return { px, rawPy, py: rawPy > FIELD / 2n ? FIELD - rawPy : rawPy };
}
function tokenHex(value) {
  if (typeof value === 'string' && (value.startsWith('T') || /^41[0-9a-fA-F]{40}$/.test(value))) value = `0x${TronWeb.address.toHex(value).slice(2)}`;
  return ethers.getAddress(value).toLowerCase();
}
function validateVaultABI(abi, protocol = 3) {
  let iface;
  try { iface = new ethers.Interface(abi); } catch (_) { throw new Error('invalid unified Vault ABI'); }
  for (const signature of (REQUIRED_WITHDRAW_ABI)) {
    const fragment = iface.getFunction(signature);
    if (!fragment || fragment.format('sighash') !== signature) throw new Error(`unified Vault ABI is missing ${signature}`);
  }
  for (const fragment of iface.fragments) {
    if (fragment.type === 'function' && REMOVED_WITHDRAW_ABI_NAMES.has(fragment.name)) {
      throw new Error(`unified Vault ABI still exposes removed ${fragment.name}`);
    }
  }
  return true;
}
async function loadVaultArtifact(hre, contractName) {
  const protocol = contractProtocol(contractName);
  const artifact = await hre.artifacts.readArtifact(contractName);
  if (protocol === 3 && artifact.sourceName !== 'contracts/SchnorrVaultV3.sol') throw new Error('V3 Vault artifact has unexpected source');
  const info = await hre.artifacts.getBuildInfo(`${artifact.sourceName}:${contractName}`);
  if (!info) throw new Error('missing compiler build info; compile before deployment');
  const compiled = info.output.contracts[artifact.sourceName][contractName];
  if (artifact.bytecode !== `0x${compiled.evm.bytecode.object}` || artifact.deployedBytecode !== `0x${compiled.evm.deployedBytecode.object}` || !isDeepStrictEqual(artifact.abi, compiled.abi)) throw new Error('Vault artifact differs from its compiler build info');
  const refs = compiled.evm.deployedBytecode.immutableReferences;
  const names = {};
  function visit(node) {
    if (!node || typeof node !== 'object') return;
    if (node.nodeType === 'VariableDeclaration' && node.mutability === 'immutable' && Object.hasOwn(refs, String(node.id))) names[String(node.id)] = node.name;
    for (const value of Object.values(node)) {
      if (Array.isArray(value)) value.forEach(visit);
      else if (value && typeof value === 'object') visit(value);
    }
  }
  for (const source of Object.values(info.output.sources)) visit(source.ast);
  const types = isTronContract(contractName) ? ['uint256', 'uint256', 'uint256', 'uint256', 'uint256', 'uint256', 'address'] : ['uint256', 'uint256', 'uint256', 'uint256', 'uint256', 'uint256'];
  const ctor = artifact.abi.find(item => item.type === 'constructor');
  if (!ctor || !isDeepStrictEqual(ctor.inputs.map(item => item.type), types)) throw new Error('artifact is not the unified Vault constructor ABI');
  validateVaultABI(artifact.abi, protocol);
  const result = { contract: contractName, abi: artifact.abi, bytecode: artifact.bytecode, deployedBytecode: artifact.deployedBytecode,
    runtimeTemplate: artifact.deployedBytecode, immutableReferences: refs, immutableNames: names,
    compilerVersion: info.solcVersion, compilerSettings: info.input.settings, creationCodeHash: ethers.keccak256(artifact.bytecode) };
  expectedVaultRuntime(result, 1n, ethers.ZeroAddress);
  return result;
}
function expectedVaultRuntime(artifact, dexChainId, nonstandardToken = ethers.ZeroAddress) {
  const domain = uint64(dexChainId, 'VAULT_DEX_CHAIN_ID', true);
  contractProtocol(artifact.contract);
  const expectedNames = isTronContract(artifact.contract) ? ['dexChainId', 'nonstandardToken'] : ['dexChainId'];
  const refs = artifact.immutableReferences || {}, names = artifact.immutableNames || {};
  if (!isDeepStrictEqual(Object.values(names).sort(), expectedNames) || !isDeepStrictEqual(Object.keys(refs).sort(), Object.keys(names).sort())) throw new Error('unified Vault immutable names differ from expected fields');
  const code = ethers.getBytes(artifact.runtimeTemplate);
  const occupied = new Set();
  const words = { dexChainId: ethers.getBytes(ethers.zeroPadValue(ethers.toBeHex(domain), 32)), nonstandardToken: ethers.getBytes(ethers.zeroPadValue(tokenHex(nonstandardToken), 32)) };
  for (const [id, ranges] of Object.entries(refs)) {
    if (!Array.isArray(ranges) || ranges.length === 0) throw new Error('missing unified Vault immutable references');
    for (const { start, length } of ranges) {
      if (!Number.isSafeInteger(start) || start < 0 || length !== 32 || start + length > code.length) throw new Error('invalid unified Vault immutable slot');
      for (let i = start; i < start + length; i++) {
        if (occupied.has(i) || code[i] !== 0) throw new Error('overlapping or nonzero unified Vault immutable slot');
        occupied.add(i);
      }
      code.set(words[names[id]], start);
    }
  }
  return ethers.hexlify(code);
}
function resolveVaultOutput(env, network, chainId, dexChainId, vaultId) {
  if (!/^[a-zA-Z0-9-]+$/.test(network)) throw new Error('invalid deployment network name');
  const directory = path.resolve(env.VAULT_DEPLOYMENTS_DIR || path.join(__dirname, '../vault-deployments'));
  return path.resolve(env.VAULT_OUTPUT || path.join(directory, `${network}-${chainId}-${dexChainId}-${vaultId}.json`));
}
function vaultIdentity(options, artifact) {
  const protocol = authorityProtocol(options.authorityProtocol);
  if (contractProtocol(artifact.contract) !== protocol) throw new Error('Vault artifact authority protocol differs from requested protocol');
  if (protocol === 3) uint64(options.epoch, 'V3 VAULT_EPOCH', true);
  const point = validatePubkey(options.px, options.rawPy);
  const guardian=validatePubkey(options.guardianPx,options.guardianPy);
  if(guardian.rawPy!==guardian.py||guardian.px===point.px&&guardian.py===point.py)throw new Error("guardian key must be canonical and distinct from operator");
  if (BigInt(options.py) !== point.py) throw new Error('canonical Vault public key differs from raw key');
  const result = { schemaVersion: 1, network: options.network, chainId: uint64(options.chainId, 'VAULT_CHAIN_ID', true).toString(),
    vaultId: uint64(options.vaultId, 'VAULT_ID').toString(), initialEpoch: uint64(options.epoch, 'VAULT_EPOCH').toString(),
    dexChainId: uint64(options.dexChainId, 'VAULT_DEX_CHAIN_ID', true).toString(), contract: artifact.contract,
    px: point.px.toString(), py: point.py.toString(), rawPy: point.rawPy.toString(), guardianPx:guardian.px.toString(),guardianPy:guardian.py.toString(), protocol: WITHDRAW_PROTOCOL, depositProtocol: DEPOSIT_PROTOCOL,
    compilerVersion: artifact.compilerVersion, compilerSettings: artifact.compilerSettings, creationCodeHash: artifact.creationCodeHash };
  if (protocol === 3) result.authorityProtocol = 3;
  if (isTronContract(artifact.contract)) result.nonstandardToken = TronWeb.address.fromHex(`41${tokenHex(options.nonstandardToken).slice(2)}`);
  expectedVaultRuntime(artifact, options.dexChainId, options.nonstandardToken);
  return result;
}
function canonicalTxHash(hash, identity) {
  const tron = isTronContract(identity.contract);
  if (typeof hash !== 'string' || !(tron ? /^[0-9a-f]{64}$/ : /^0x[0-9a-f]{64}$/).test(hash) || /^0+$/.test(hash.replace(/^0x/, ''))) throw new Error('invalid canonical deployment transaction hash');
  return hash;
}
function deploymentBlockNumber(value, name) {
  if (!Number.isSafeInteger(value) || value <= 0) throw new Error(`${name} must be a positive safe block number`);
  return value;
}
function validateDetails(details, identity) {
  canonicalTxHash(details.txHash, identity);
  for (const field of ['address', 'deployer']) {
    const value = details[field];
    if (isTronContract(identity.contract)) {
      if (!TronWeb.isAddress(value) || TronWeb.address.fromHex(TronWeb.address.toHex(value)) !== value || tokenHex(value) === ethers.ZeroAddress) throw new Error(`invalid deployment ${field}`);
    } else if (typeof value !== 'string' || ethers.getAddress(value) === ethers.ZeroAddress) throw new Error(`invalid deployment ${field}`);
  }
}
function loadVaultJournal(output, expected) {
  if (!fs.existsSync(output)) return null;
  const stat = fs.lstatSync(output);
  if (!stat.isFile() || stat.isSymbolicLink() || stat.size > 1024 * 1024) throw new Error('unsafe deployment journal');
  const saved = JSON.parse(fs.readFileSync(output, 'utf8'));
  if (authorityProtocol(saved.authorityProtocol) !== contractProtocol(expected.contract)) throw new Error('deployment journal conflicts with authorityProtocol');
  if (!['pending', 'verified'].includes(saved.status)) throw new Error('invalid deployment journal status');
  for (const [key, value] of Object.entries(expected)) if (!isDeepStrictEqual(saved[key], value)) throw new Error(`deployment journal conflicts with ${key}`);
  validateDetails(saved, expected);
  if (saved.searchFromBlock !== undefined) deploymentBlockNumber(saved.searchFromBlock, 'pending searchFromBlock');
  if (saved.status === 'verified' && (!/^0x[0-9a-f]{64}$/.test(saved.runtimeCodeHash || '') || !Number.isSafeInteger(saved.blockNumber) || saved.blockNumber <= 0)) throw new Error('incomplete verified deployment journal');
  return saved;
}
function writeExclusive(output, value) {
  fs.mkdirSync(path.dirname(output), { recursive: true });
  const fd = fs.openSync(output, 'wx', 0o600);
  try { fs.writeFileSync(fd, JSON.stringify(value, null, 2) + '\n'); fs.fsyncSync(fd); } finally { fs.closeSync(fd); }
}
function writeVaultPending(output, identity, details) {
  validateDetails(details, identity);
  const pending = { ...identity, status: 'pending', txHash: details.txHash, address: details.address, deployer: details.deployer };
  if (details.searchFromBlock !== undefined) pending.searchFromBlock = deploymentBlockNumber(details.searchFromBlock, 'pending searchFromBlock');
  writeExclusive(output, pending);
  return pending;
}
function writeVaultVerified(output, identity, details, existing) {
  validateDetails(details, identity);
  if (!/^0x[0-9a-f]{64}$/.test(details.runtimeCodeHash || '') || !Number.isSafeInteger(details.blockNumber) || details.blockNumber <= 0) throw new Error('incomplete verified deployment result');
  const result = { ...identity, status: 'verified' };
  for (const key of ['txHash', 'address', 'deployer', 'runtimeCodeHash', 'blockNumber', 'deployedAt', 'minimumEnergy']) if (details[key] !== undefined) result[key] = details[key];
  if (!existing) { writeExclusive(output, result); return result; }
  let temp;
  try {
                                                                              
                                                                            
                                                                                      
    if (!isDeepStrictEqual(loadVaultJournal(output, identity), existing)) throw new Error('deployment journal changed during verification');
    if (existing.txHash !== details.txHash || tokenHex(existing.address) !== tokenHex(details.address) || tokenHex(existing.deployer) !== tokenHex(details.deployer)) throw new Error('verified deployment differs from pending transaction');
    temp = output + '.verified-' + process.pid + '-' + Date.now();
    writeExclusive(temp, result);
    fs.renameSync(temp, output);
    return result;
  } finally {
    if (temp && fs.existsSync(temp)) fs.unlinkSync(temp);
  }
}

module.exports = { guardianFromConfig, resolveVaultArtifactsDirectory, vaultArtifactStore, authorityProtocol, vaultContractName, contractProtocol, isTronContract, validateVaultCapabilities, DEPOSIT_PROTOCOL, WITHDRAW_PROTOCOL, validateVaultABI, uint64, validatePubkey, tokenHex, loadVaultArtifact, expectedVaultRuntime, deploymentBlockNumber,
  resolveVaultOutput, vaultIdentity, canonicalTxHash, loadVaultJournal, writeVaultPending, writeVaultVerified };
