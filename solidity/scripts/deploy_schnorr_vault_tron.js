const { isDeepStrictEqual } = require("util");
const path = require("path");
const { ethers } = require("ethers");
const tronModule = require("tronweb");
const { TronWeb } = tronModule;
const tronTransaction = tronModule.utils.transaction;

const FIELD = 21888242871839275222246405745257275088696311157297823662689037894645226208583n;
const TRON_MAINNET_CHAIN_ID = 728126428n;
const TRON_MAINNET_USDT = "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t";
const ZERO_TRON_ADDRESS = "T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwb";
const MAX_VAULT_FEE_LIMIT_SUN = 5_000_000_000;
const DEFAULT_VAULT_FEE_LIMIT_SUN = 350_000_000;
const MAX_ORIGIN_ENERGY_LIMIT = 10_000_000;
const SIGNED_TRANSACTION_OVERHEAD_BYTES = 256;
const TX_EXPIRATION_MS = 60_000;
const MAX_REFERENCE_AGE_MS = 45_000;
const MAX_CLOCK_SKEW_MS = 15_000;
const MIN_SIGNING_WINDOW_MS = 10_000;
const MAX_RPC_RESPONSE_BYTES = 8 * 1024 * 1024;

function requireEnv(name, env = process.env) {
  const value = env[name];
  if (!value || !value.trim()) throw new Error(`missing env ${name}`);
  return value.trim();
}

function normalizeHex(value) {
  const raw = String(value).trim();
  return raw.startsWith("0x") || raw.startsWith("0X") ? raw.slice(2) : raw;
}

function resolvePubkey(env = process.env) {
  if (env.VAULT_GROUP_PUBKEY && env.VAULT_GROUP_PUBKEY.trim()) {
    const raw = normalizeHex(env.VAULT_GROUP_PUBKEY);
    if (!/^[0-9a-fA-F]{128}$/.test(raw)) {
      throw new Error("VAULT_GROUP_PUBKEY must be 64 bytes hex");
    }
    return { px: `0x${raw.slice(0, 64)}`, py: `0x${raw.slice(64)}` };
  }
  return { px: requireEnv("VAULT_PX", env), py: requireEnv("VAULT_PY", env) };
}

function validatePubkey(pxInput, pyInput) {
  const px = BigInt(pxInput);
  const rawPy = BigInt(pyInput);
  if (px < 0n || rawPy < 0n || px >= FIELD || rawPy >= FIELD ||
      (px === 0n && rawPy === 0n) ||
      rawPy * rawPy % FIELD !== (px * px % FIELD * px + 3n) % FIELD) {
    throw new Error("VAULT public key must be a non-infinity BN128 curve point");
  }
  return { px, rawPy, py: rawPy > FIELD / 2n ? FIELD - rawPy : rawPy };
}

function uint64Env(name, env = process.env) {
  const value = requireEnv(name, env);
  if (!/^(0|[1-9][0-9]*)$/.test(value) || BigInt(value) > (1n << 64n) - 1n) {
    throw new Error(`${name} must be a decimal uint64`);
  }
  return BigInt(value);
}

function positiveSafeIntegerEnv(name, fallback, env = process.env) {
  const raw = env[name] && env[name].trim() ? env[name].trim() : String(fallback);
  if (!/^[1-9][0-9]*$/.test(raw)) throw new Error(`${name} must be a positive integer`);
  const value = Number(raw);
  if (!Number.isSafeInteger(value)) throw new Error(`${name} exceeds the JavaScript safe integer range`);
  return value;
}

function validateLiveDeployment(chainId) {
  if (chainId === TRON_MAINNET_CHAIN_ID && process.env.VAULT_ALLOW_MAINNET_DEPLOY !== "true") {
    throw new Error("TRON mainnet deployment requires explicit VAULT_ALLOW_MAINNET_DEPLOY=true");
  }
}

function resolveNonstandardToken(chainId, env = process.env) {
  const supplied = env.VAULT_NONSTANDARD_TOKEN && env.VAULT_NONSTANDARD_TOKEN.trim();
  if (chainId === TRON_MAINNET_CHAIN_ID) {
    if (supplied && supplied !== TRON_MAINNET_USDT) {
      throw new Error(`TRON mainnet nonstandard token must be ${TRON_MAINNET_USDT}`);
    }
    return TRON_MAINNET_USDT;
  }
  return supplied || ZERO_TRON_ADDRESS;
}

function validateRPCURL(value, name = "TRON_JSON_RPC_URL") {
  let parsed;
  try {
    parsed = new URL(value);
  } catch (_) {
    throw new Error(`${name} must be an absolute HTTP(S) URL`);
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
    throw new Error(`${name} must use HTTP(S)`);
  }
  if (parsed.username || parsed.password || parsed.hash) {
    throw new Error(`${name} must not contain credentials or a fragment`);
  }
  return parsed.toString();
}

async function parseBoundedJSONResponse(response, label) {
  if (!response.ok) throw new Error(`${label}: HTTP ${response.status}`);
  const declaredLength = response.headers && response.headers.get("content-length");
  if (declaredLength && Number(declaredLength) > MAX_RPC_RESPONSE_BYTES) {
    throw new Error(`${label}: response is too large`);
  }
  const text = await response.text();
  if (Buffer.byteLength(text, "utf8") > MAX_RPC_RESPONSE_BYTES) {
    throw new Error(`${label}: response is too large`);
  }
  let body;
  try {
    body = JSON.parse(text);
  } catch (_) {
    throw new Error(`${label}: response is not valid JSON`);
  }
  if (!body || typeof body !== "object" || Array.isArray(body)) {
    throw new Error(`${label}: response must be a JSON object`);
  }
  return body;
}

async function jsonRPC(url, method, params = []) {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), 15_000);
  try {
    const response = await fetch(url, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ jsonrpc: "2.0", id: 1, method, params }),
      signal: controller.signal,
      redirect: "error",
    });
    const body = await parseBoundedJSONResponse(response, `TRON JSON-RPC ${method}`);
    if (body.error || body.result === undefined || body.result === null) {
      throw new Error(body.error ? JSON.stringify(body.error) : "missing result");
    }
    return body.result;
  } finally {
    clearTimeout(timer);
  }
}

function nativeEndpoint(fullHost, route) {
  const endpoint = new URL(fullHost);
  endpoint.pathname = `${endpoint.pathname.replace(/\/+$/, "")}/${route.replace(/^\/+/, "")}`;
  endpoint.search = "";
  endpoint.hash = "";
  return endpoint.toString();
}

async function nativeRPC(fullHost, route, payload = {}) {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), 15_000);
  try {
    const response = await fetch(nativeEndpoint(fullHost, route), {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(payload),
      signal: controller.signal,
      redirect: "error",
    });
    const body = await parseBoundedJSONResponse(response, `TRON native RPC ${route}`);
    if (body.Error || body.error || (body.result && body.result.result === false)) {
      throw new Error(body.Error || JSON.stringify(body.error || body.result));
    }
    return body;
  } finally {
    clearTimeout(timer);
  }
}

async function verifyChainID(jsonRPCURL, expectedChainId, callJSON = jsonRPC) {
  const result = await callJSON(jsonRPCURL, "eth_chainId");
  if (!/^0x[0-9a-fA-F]+$/.test(result)) throw new Error("eth_chainId returned a non-hex quantity");
  const actual = BigInt(result);
  if (actual === 0n || actual !== expectedChainId) {
    throw new Error(`TRON RPC chain ID ${actual} does not match requested ${expectedChainId}`);
  }
  return actual;
}

function hexQuantityToSafeInteger(value, name) {
  if (typeof value !== "string" || !/^0x(?:0|[1-9a-fA-F][0-9a-fA-F]*)$/.test(value)) {
    throw new Error(`${name} returned a non-canonical hex quantity`);
  }
  const parsed = BigInt(value);
  if (parsed > BigInt(Number.MAX_SAFE_INTEGER)) throw new Error(`${name} exceeds the JavaScript safe integer range`);
  return Number(parsed);
}

function validateNativeBlock(block, expectedNumber, label) {
  if (!block || typeof block !== "object" || Array.isArray(block) ||
      typeof block.blockID !== "string" || !/^[0-9a-fA-F]{64}$/.test(block.blockID) ||
      !block.block_header || !block.block_header.raw_data) {
    throw new Error(`${label} returned a malformed native block`);
  }
                                                                           
                                                                           
                                                                           
                                                                            
                                           
  const isGenesis = expectedNumber === 0;
  const rawNumber = block.block_header.raw_data.number;
  const rawTimestamp = block.block_header.raw_data.timestamp;
  const number = rawNumber === undefined && isGenesis ? 0 : rawNumber;
  const timestamp = rawTimestamp === undefined && isGenesis ? 0 : rawTimestamp;
  if (!Number.isSafeInteger(number) || number < 0 ||
      !Number.isSafeInteger(timestamp) || timestamp < 0 || (!isGenesis && timestamp === 0) ||
      (expectedNumber !== undefined && number !== expectedNumber)) {
    throw new Error(`${label} returned invalid block header values`);
  }
  if (block.blockID.slice(0, 16).toLowerCase() !== number.toString(16).padStart(16, "0")) {
    throw new Error(`${label} block ID does not commit to its height`);
  }
  return { number, timestamp, blockID: block.blockID.toLowerCase() };
}

function validateJSONBlock(block, expectedNumber, label) {
  if (!block || typeof block !== "object" || Array.isArray(block) ||
      typeof block.hash !== "string" || !/^0x[0-9a-fA-F]{64}$/.test(block.hash)) {
    throw new Error(`${label} returned a malformed JSON-RPC block`);
  }
  const number = hexQuantityToSafeInteger(block.number, `${label}.number`);
  if (number !== expectedNumber) throw new Error(`${label} returned the wrong block height`);
  return { number, blockID: block.hash.slice(2).toLowerCase() };
}

function validateNodeInfo(nodeInfo) {
  if (!nodeInfo || typeof nodeInfo !== "object" || Array.isArray(nodeInfo) ||
      typeof nodeInfo.block !== "string" || typeof nodeInfo.solidityBlock !== "string" ||
      !nodeInfo.configNodeInfo || typeof nodeInfo.configNodeInfo.codeVersion !== "string" ||
      !nodeInfo.configNodeInfo.codeVersion.trim()) {
    throw new Error("TRON_FULL_HOST getnodeinfo did not identify a valid java-tron node");
  }
  return {
    headHeight: parseNodeBlockSummary(nodeInfo.block, "getnodeinfo block"),
    solidityHeight: parseNodeBlockSummary(nodeInfo.solidityBlock, "getnodeinfo solidityBlock"),
  };
}

function parseNodeBlockSummary(value, label) {
  const match = /^Num:(0|[1-9][0-9]*),ID:[0-9a-fA-F]{64}$/.exec(value);
  if (!match) throw new Error(`${label} is malformed`);
  const height = Number(match[1]);
  if (!Number.isSafeInteger(height)) throw new Error(`${label} exceeds the safe integer range`);
  return height;
}

function validateChainParameters(parameters) {
  const entries = parameters && parameters.chainParameter;
  if (!Array.isArray(entries) || entries.length === 0) {
    throw new Error("TRON_FULL_HOST returned no chain parameters");
  }
  const values = new Map();
  for (const entry of entries) {
    if (!entry || typeof entry !== "object" || Array.isArray(entry) ||
        typeof entry.key !== "string" || !entry.key || values.has(entry.key)) {
      throw new Error("TRON_FULL_HOST returned malformed chain parameters");
    }
                                                                                   
                                                                                
                                                         
    const value = Object.prototype.hasOwnProperty.call(entry, "value") ? entry.value : 0;
    if (!Number.isSafeInteger(value)) {
      throw new Error("TRON_FULL_HOST returned malformed chain parameters");
    }
    values.set(entry.key, value);
  }
  if (!(values.get("getMaintenanceTimeInterval") > 0) || !(values.get("getEnergyFee") > 0) ||
      !(values.get("getTransactionFee") > 0)) {
    throw new Error("TRON_FULL_HOST is missing required positive chain parameters");
  }
  return {
    maintenanceTimeInterval: values.get("getMaintenanceTimeInterval"),
    energyFee: values.get("getEnergyFee"),
    transactionFee: values.get("getTransactionFee"),
  };
}

async function verifyTronNetworkIdentity(jsonRPCURL, fullHost, expectedChainId, options = {}) {
  const callJSON = options.jsonRPC || jsonRPC;
  const callNative = options.nativeRPC || nativeRPC;
  const nowMs = options.nowMs === undefined ? Date.now() : options.nowMs;
  if (!Number.isSafeInteger(nowMs) || nowMs <= 0) throw new Error("invalid network verification time");

  const [chainId, jsonHeightRaw, nativeHeadRaw, nodeInfo, parameters, nativeGenesisRaw, jsonGenesisRaw] =
    await Promise.all([
      verifyChainID(jsonRPCURL, expectedChainId, callJSON),
      callJSON(jsonRPCURL, "eth_blockNumber"),
      callNative(fullHost, "wallet/getnowblock", {}),
      callNative(fullHost, "wallet/getnodeinfo", {}),
      callNative(fullHost, "wallet/getchainparameters", {}),
      callNative(fullHost, "wallet/getblockbynum", { num: 0, detail: false }),
      callJSON(jsonRPCURL, "eth_getBlockByNumber", ["0x0", false]),
    ]);
  const jsonHeight = hexQuantityToSafeInteger(jsonHeightRaw, "eth_blockNumber");
  const nativeHead = validateNativeBlock(nativeHeadRaw, undefined, "getnowblock");
  const nativeGenesis = validateNativeBlock(nativeGenesisRaw, 0, "native genesis");
  const jsonGenesis = validateJSONBlock(jsonGenesisRaw, 0, "JSON-RPC genesis");
  validateNodeInfo(nodeInfo);
  const chainParameters = validateChainParameters(parameters);
  const nativeGenesisChainId = BigInt(`0x${nativeGenesis.blockID.slice(-8)}`);
  if (expectedChainId > 0xffffffffn || nativeGenesisChainId !== expectedChainId) {
    throw new Error(`TRON_FULL_HOST genesis chain ID ${nativeGenesisChainId} does not match requested ${expectedChainId}`);
  }
  if (nativeGenesis.blockID !== jsonGenesis.blockID) {
    throw new Error("TRON_FULL_HOST and TRON_JSON_RPC_URL have different genesis blocks");
  }

  const anchorHeight = Math.min(jsonHeight, nativeHead.number);
  if (anchorHeight <= 0) throw new Error("TRON RPC endpoints do not expose a usable non-genesis anchor");
  const [nativeAnchorRaw, jsonAnchorRaw] = await Promise.all([
    callNative(fullHost, "wallet/getblockbynum", { num: anchorHeight, detail: false }),
    callJSON(jsonRPCURL, "eth_getBlockByNumber", [`0x${anchorHeight.toString(16)}`, false]),
  ]);
  const nativeAnchor = validateNativeBlock(nativeAnchorRaw, anchorHeight, "native anchor");
  const jsonAnchor = validateJSONBlock(jsonAnchorRaw, anchorHeight, "JSON-RPC anchor");
  if (nativeAnchor.blockID !== jsonAnchor.blockID) {
    throw new Error("TRON_FULL_HOST and TRON_JSON_RPC_URL do not agree on the anchor block");
  }
  if (nativeAnchor.timestamp > nowMs + MAX_CLOCK_SKEW_MS ||
      nativeAnchor.timestamp < nowMs - MAX_REFERENCE_AGE_MS) {
    throw new Error("cross-verified TRON anchor timestamp is not recent enough for signing");
  }

  return {
    chainId,
    chainParameters,
    anchorHeight,
    anchorBlockID: nativeAnchor.blockID,
    blockHeader: {
      ref_block_bytes: anchorHeight.toString(16).slice(-4).padStart(4, "0"),
      ref_block_hash: nativeAnchor.blockID.slice(16, 32),
      expiration: nativeAnchor.timestamp + TX_EXPIRATION_MS,
      timestamp: nativeAnchor.timestamp,
    },
  };
}

function normalizeCode(value) {
  const raw = normalizeHex(value || "");
  if (!raw || raw.length % 2 !== 0 || !/^[0-9a-fA-F]+$/.test(raw)) {
    throw new Error("node returned invalid runtime bytecode");
  }
  return raw.toLowerCase();
}

function maskImmutableReferences(codeInput, immutableReferences) {
  const code = Buffer.from(normalizeCode(codeInput), "hex");
  for (const locations of Object.values(immutableReferences || {})) {
    if (!Array.isArray(locations)) throw new Error("compiler immutable references are malformed");
    for (const location of locations) {
      if (!Number.isInteger(location.start) || !Number.isInteger(location.length) ||
          location.start < 0 || location.length < 0 || location.start + location.length > code.length) {
        throw new Error("compiler immutable reference is outside runtime bytecode");
      }
      code.fill(0, location.start, location.start + location.length);
    }
  }
  return code.toString("hex");
}

function verifyRuntimeCode(actualCode, artifactCode, immutableReferences) {
  const actual = normalizeCode(actualCode);
  const expected = normalizeCode(artifactCode);
  if (actual.length !== expected.length ||
      maskImmutableReferences(actual, immutableReferences) !== maskImmutableReferences(expected, immutableReferences)) {
    throw new Error("deployed runtime code differs from the compiled artifact outside immutable values");
  }
  return `0x${actual}`;
}

function asBigInt(value, name) {
  if (typeof value === "bigint") return value;
  if (typeof value === "number") {
    if (Number.isSafeInteger(value)) return BigInt(value);
    throw new Error(`${name} returned an invalid integer`);
  }
  if (typeof value === "string" && /^(0x[0-9a-fA-F]+|[0-9]+)$/.test(value)) return BigInt(value);
  if (value && typeof value.toString === "function") {
    const rendered = value.toString(10);
    if (/^[0-9]+$/.test(rendered)) return BigInt(rendered);
  }
  throw new Error(`${name} returned an invalid integer`);
}

function nonnegativeField(value, name) {
  const parsed = asBigInt(value, name);
  if (parsed < 0n) throw new Error(`${name} returned a negative integer`);
  return parsed;
}

function optionalNonnegativeField(value, name) {
  return value === undefined || value === null ? 0n : nonnegativeField(value, name);
}

function availableResource(resources, limitName, usedName) {
  const limit = optionalNonnegativeField(resources[limitName], limitName);
  const used = optionalNonnegativeField(resources[usedName], usedName);
  return limit > used ? limit - used : 0n;
}

function validateDeploymentResources(account, resources, validated, feeLimit, chainParameters, minimumEnergy) {
  if (!account || typeof account !== "object" || Array.isArray(account) ||
      !resources || typeof resources !== "object" || Array.isArray(resources)) {
    throw new Error("TRON resource preflight returned malformed account data");
  }
  safeInteger(feeLimit, "fee limit", { min: 1, max: MAX_VAULT_FEE_LIMIT_SUN });
  safeInteger(chainParameters.energyFee, "getEnergyFee", { min: 1 });
  safeInteger(chainParameters.transactionFee, "getTransactionFee", { min: 1 });
  if (typeof minimumEnergy !== "bigint" || minimumEnergy <= 0n) {
    throw new Error("minimum deployment Energy must be a positive bigint");
  }

  const rawDataHex = validated && validated.transaction && validated.transaction.raw_data_hex;
  const rawData = normalizeHex(rawDataHex || "");
  if (!rawData || rawData.length % 2 !== 0 || !/^[0-9a-fA-F]+$/.test(rawData)) {
    throw new Error("validated deployment transaction has malformed raw_data_hex");
  }
  const estimatedBandwidthBytes = BigInt(rawData.length / 2 + SIGNED_TRANSACTION_OVERHEAD_BYTES);
  const availableBandwidth = availableResource(resources, "freeNetLimit", "freeNetUsed") +
    availableResource(resources, "NetLimit", "NetUsed");
  const paidBandwidthBytes = estimatedBandwidthBytes > availableBandwidth
    ? estimatedBandwidthBytes - availableBandwidth : 0n;
  const paidBandwidthSun = paidBandwidthBytes * BigInt(chainParameters.transactionFee);
  const liquidBalanceSun = optionalNonnegativeField(account.balance, "account balance");
  if (liquidBalanceSun < paidBandwidthSun) {
    throw new Error(`TRON deployer lacks the ${paidBandwidthSun} SUN liquid bandwidth reserve; no transaction was signed`);
  }

  const availableEnergy = availableResource(resources, "EnergyLimit", "EnergyUsed");
  const feeLimitEnergy = BigInt(feeLimit) / BigInt(chainParameters.energyFee);
  const usableEnergy = availableEnergy < feeLimitEnergy ? availableEnergy : feeLimitEnergy;
  if (usableEnergy < minimumEnergy) {
    throw new Error(
      `TRON deployer has only ${usableEnergy} usable delegated Energy; at least ${minimumEnergy} is required before signing; liquid TRX cannot replace missing Energy`,
    );
  }
  return {
    usableEnergy,
    availableEnergy,
    liquidBalanceSun,
    paidBandwidthSun,
    feeLimitEnergy,
  };
}

function sameTronAddress(tronWeb, left, right) {
  try {
    return tronWeb.address.toHex(left).toLowerCase() === tronWeb.address.toHex(right).toLowerCase();
  } catch (_) {
    return false;
  }
}

function exactKeys(value, expected, label) {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new Error(`${label} must be an object`);
  }
  const actual = Object.keys(value).sort();
  const wanted = [...expected].sort();
  if (!isDeepStrictEqual(actual, wanted)) {
    throw new Error(`${label} has unexpected or missing fields`);
  }
}

function safeInteger(value, label, { min = 0, max = Number.MAX_SAFE_INTEGER } = {}) {
  if (!Number.isSafeInteger(value) || value < min || value > max) {
    throw new Error(`${label} must be an integer in the permitted range`);
  }
  return value;
}

function normalizeTronAddressHex(tronWeb, value, label) {
  let hex;
  try {
    hex = tronWeb.address.toHex(value);
  } catch (_) {
    throw new Error(`${label} is not a TRON address`);
  }
  if (typeof hex !== "string" || !/^41[0-9a-fA-F]{40}$/.test(hex)) {
    throw new Error(`${label} is not a canonical TRON address`);
  }
  return hex.toLowerCase();
}

function constructorBytecode(tronWeb, artifact, px, rawPy, guardianPx, guardianPy, epoch, dexChainId, nonstandardToken) {
  const creationCode = normalizeHex(artifact && artifact.bytecode || "");
  if (!creationCode || creationCode.length % 2 !== 0 || !/^[0-9a-fA-F]+$/.test(creationCode)) {
    throw new Error("compiled artifact has invalid creation bytecode");
  }
  const tokenHex = normalizeTronAddressHex(tronWeb, nonstandardToken, "VAULT_NONSTANDARD_TOKEN");
  const domain = asBigInt(dexChainId, "DEX chain ID");
  if (domain <= 0n || domain > (1n << 64n) - 1n) throw new Error("Vault DEX chain ID must be a nonzero uint64");
  const encoded = ethers.AbiCoder.defaultAbiCoder().encode(
    ["uint256", "uint256", "uint256", "uint256", "uint256", "uint256", "address"],
    [px, rawPy, guardianPx, guardianPy, epoch, domain, `0x${tokenHex.slice(2)}`],
  );
  return `${creationCode}${normalizeHex(encoded)}`.toLowerCase();
}

function deriveContractAddressHex(txID, ownerHex) {
  if (!/^[0-9a-fA-F]{64}$/.test(txID) || !/^41[0-9a-fA-F]{40}$/.test(ownerHex)) {
    throw new Error("cannot derive contract address from malformed transaction identity");
  }
  const digest = ethers.keccak256(`0x${txID}${ownerHex}`);
  return `41${digest.slice(-40)}`.toLowerCase();
}

function expectedDeploymentBytecode(tronWeb, request) {
  const name = request.contractName || "SchnorrExampleTronV3";
  if (!["SchnorrExampleTronV3"].includes(name)) {
    throw new Error("unsupported deployment contract");
  }
  if (request.constructorData !== undefined) throw new Error("Vault constructor data must be derived from DKG parameters");
  return constructorBytecode(tronWeb, request.artifact, request.px, request.rawPy, request.guardianPx, request.guardianPy, request.epoch, request.dexChainId, request.nonstandardToken);
}

function validateUnsignedCreateSmartContract(tronWeb, unsignedInput, request) {
  let transaction;
  try {
    transaction = JSON.parse(JSON.stringify(unsignedInput));
  } catch (_) {
    throw new Error("unsigned deployment transaction is not plain JSON");
  }
  exactKeys(transaction, ["visible", "txID", "raw_data_hex", "raw_data", "contract_address"], "unsigned transaction");
  if (transaction.visible !== false) throw new Error("unsigned transaction must use canonical hex address encoding");
  if (Object.prototype.hasOwnProperty.call(transaction, "signature")) {
    throw new Error("transaction builder returned a pre-signed transaction");
  }

  const {
    artifact, deployer, px, rawPy, epoch, nonstandardToken,
    feeLimit, originEnergyLimit, blockHeader,
  } = request;
  safeInteger(feeLimit, "fee limit", { min: 1, max: MAX_VAULT_FEE_LIMIT_SUN });
  safeInteger(originEnergyLimit, "origin energy limit", { min: 1, max: MAX_ORIGIN_ENERGY_LIMIT });
  exactKeys(blockHeader, ["ref_block_bytes", "ref_block_hash", "expiration", "timestamp"], "trusted block header");
  if (!/^[0-9a-fA-F]{4}$/.test(blockHeader.ref_block_bytes) ||
      !/^[0-9a-fA-F]{16}$/.test(blockHeader.ref_block_hash)) {
    throw new Error("trusted block header has malformed TAPOS references");
  }
  safeInteger(blockHeader.timestamp, "trusted block timestamp", { min: 1 });
  safeInteger(blockHeader.expiration, "trusted transaction expiration", { min: 1 });
  if (blockHeader.expiration !== blockHeader.timestamp + TX_EXPIRATION_MS) {
    throw new Error("trusted transaction expiration is not canonical");
  }
  const nowMs = request.nowMs === undefined ? Date.now() : request.nowMs;
  safeInteger(nowMs, "validation time", { min: 1 });
  if (blockHeader.timestamp > nowMs + MAX_CLOCK_SKEW_MS ||
      blockHeader.timestamp < nowMs - MAX_REFERENCE_AGE_MS ||
      blockHeader.expiration < nowMs + MIN_SIGNING_WINDOW_MS) {
    throw new Error("trusted block header is outside the permitted signing window");
  }

  exactKeys(transaction.raw_data,
    ["contract", "ref_block_bytes", "ref_block_hash", "expiration", "timestamp", "fee_limit"],
    "unsigned raw_data");
  const raw = transaction.raw_data;
  if (!Array.isArray(raw.contract) || raw.contract.length !== 1) {
    throw new Error("unsigned transaction must contain exactly one contract operation");
  }
  if (raw.ref_block_bytes.toLowerCase() !== blockHeader.ref_block_bytes.toLowerCase() ||
      raw.ref_block_hash.toLowerCase() !== blockHeader.ref_block_hash.toLowerCase() ||
      raw.timestamp !== blockHeader.timestamp || raw.expiration !== blockHeader.expiration) {
    throw new Error("unsigned transaction TAPOS/expiration differs from the cross-verified block header");
  }
  if (raw.fee_limit !== feeLimit || raw.fee_limit > MAX_VAULT_FEE_LIMIT_SUN) {
    throw new Error("unsigned transaction fee limit differs from the requested bounded value");
  }

  const operation = raw.contract[0];
  exactKeys(operation, ["parameter", "type"], "unsigned contract operation");
  if (operation.type !== "CreateSmartContract") {
    throw new Error("unsigned transaction is not a CreateSmartContract operation");
  }
  exactKeys(operation.parameter, ["value", "type_url"], "unsigned contract parameter");
  if (operation.parameter.type_url !== "type.googleapis.com/protocol.CreateSmartContract") {
    throw new Error("unsigned transaction has the wrong CreateSmartContract type URL");
  }
  const value = operation.parameter.value;
  exactKeys(value, ["owner_address", "new_contract"], "CreateSmartContract value");
  if (Object.prototype.hasOwnProperty.call(value, "call_token_value") ||
      Object.prototype.hasOwnProperty.call(value, "token_id")) {
    throw new Error("CreateSmartContract must not transfer a TRC10 token");
  }

  const expectedOwner = normalizeTronAddressHex(tronWeb, deployer, "deployer");
  if (typeof value.owner_address !== "string" || value.owner_address.toLowerCase() !== expectedOwner) {
    throw new Error("CreateSmartContract owner differs from the deployer");
  }
  const contract = value.new_contract;
  exactKeys(contract,
    ["abi", "bytecode", "consume_user_resource_percent", "origin_energy_limit", "origin_address", "name"],
    "new contract");
  if (Object.prototype.hasOwnProperty.call(contract, "call_value") ||
      Object.prototype.hasOwnProperty.call(contract, "call_token_value") ||
      Object.prototype.hasOwnProperty.call(contract, "token_id")) {
    throw new Error("Vault creation must not transfer native or token value");
  }
  if (typeof contract.origin_address !== "string" || contract.origin_address.toLowerCase() !== expectedOwner) {
    throw new Error("Vault origin address differs from the deployer");
  }
  const expectedBytecode = expectedDeploymentBytecode(tronWeb, request);
  if (contract.name !== (request.contractName || "SchnorrExampleTronV3") ||
      contract.consume_user_resource_percent !== 100 ||
      contract.origin_energy_limit !== originEnergyLimit) {
    throw new Error("Vault creation resource settings differ from the request");
  }
  exactKeys(contract.abi, ["entrys"], "Vault ABI wrapper");
  if (!isDeepStrictEqual(contract.abi.entrys, artifact.abi)) {
    throw new Error("Vault creation ABI differs from the compiled artifact");
  }
  if (typeof contract.bytecode !== "string" || normalizeHex(contract.bytecode).toLowerCase() !== expectedBytecode) {
    throw new Error("Vault creation bytecode or constructor arguments differ from the request");
  }

  let protobuf;
  let canonicalTxID;
  let canonicalRawData;
  try {
    protobuf = tronTransaction.txJsonToPb(transaction);
    canonicalTxID = normalizeHex(tronTransaction.txPbToTxID(protobuf)).toLowerCase();
    canonicalRawData = normalizeHex(tronTransaction.txPbToRawDataHex(protobuf)).toLowerCase();
  } catch (err) {
    throw new Error(`unsigned transaction cannot be canonically encoded: ${err.message || err}`);
  }
  if (!/^[0-9a-f]{64}$/.test(canonicalTxID) ||
      typeof transaction.txID !== "string" || transaction.txID.toLowerCase() !== canonicalTxID ||
      typeof transaction.raw_data_hex !== "string" ||
      normalizeHex(transaction.raw_data_hex).toLowerCase() !== canonicalRawData) {
    throw new Error("unsigned transaction txID/raw_data_hex is not its canonical protobuf encoding");
  }
  const expectedContractAddress = deriveContractAddressHex(canonicalTxID, expectedOwner);
  if (typeof transaction.contract_address !== "string" ||
      transaction.contract_address.toLowerCase() !== expectedContractAddress) {
    throw new Error("unsigned transaction has an incorrect derived contract address");
  }
  return { transaction, txID: canonicalTxID, contractAddressHex: expectedContractAddress };
}

function validateSignedMatchesUnsigned(signed, validated) {
  if (!signed || typeof signed !== "object" ||
      signed.txID !== validated.transaction.txID ||
      signed.raw_data_hex !== validated.transaction.raw_data_hex ||
      signed.contract_address !== validated.transaction.contract_address ||
      !isDeepStrictEqual(signed.raw_data, validated.transaction.raw_data) ||
      !Array.isArray(signed.signature) || signed.signature.length !== 1 ||
      !/^[0-9a-fA-F]{130}$/.test(signed.signature[0])) {
    throw new Error("signer changed the validated unsigned transaction or returned an invalid signature");
  }
}

function normalizeRecoveryTxHash(value) {
  if (value === undefined || value === null || !String(value).trim()) return null;
  const txHash = normalizeHex(String(value).trim()).toLowerCase();
  if (!/^[0-9a-f]{64}$/.test(txHash) || /^0+$/.test(txHash)) {
    throw new Error("VAULT_RECOVER_TX_HASH must be a nonzero 32-byte hex hash");
  }
  return txHash;
}

function validateRecoveredDeploymentTransaction(tronWeb, onchainInput, txHash, request) {
  let onchain;
  try {
    onchain = JSON.parse(JSON.stringify(onchainInput));
  } catch (_) {
    throw new Error("recovered deployment transaction is not plain JSON");
  }
  exactKeys(onchain,
    ["raw_data", "signature", "ret", "contract_address", "raw_data_hex", "txID"],
    "recovered deployment transaction");
  if (typeof onchain.txID !== "string" || onchain.txID.toLowerCase() !== txHash ||
      !Array.isArray(onchain.ret) || onchain.ret.length !== 1 ||
      !onchain.ret[0] || onchain.ret[0].contractRet !== "SUCCESS") {
    throw new Error("recovered transaction is not the requested successful deployment");
  }
  if (!onchain.raw_data || typeof onchain.raw_data !== "object") {
    throw new Error("recovered deployment transaction has no raw_data");
  }
  const blockHeader = {
    ref_block_bytes: onchain.raw_data.ref_block_bytes,
    ref_block_hash: onchain.raw_data.ref_block_hash,
    expiration: onchain.raw_data.expiration,
    timestamp: onchain.raw_data.timestamp,
  };
  const unsigned = JSON.parse(JSON.stringify(onchain));
  delete unsigned.signature;
  delete unsigned.ret;
  unsigned.visible = false;
                                                                       
                                                                              
                                                                          
                                                                       
                                                                              
                                                  
  unsigned.raw_data.contract[0].parameter.value.new_contract.abi.entrys =
    JSON.parse(JSON.stringify(request.artifact.abi));
  const validated = validateUnsignedCreateSmartContract(tronWeb, unsigned, {
    ...request,
    blockHeader,
                                                                            
                                                                         
                                                                          
    nowMs: blockHeader.timestamp,
  });
  validateSignedMatchesUnsigned({ ...unsigned, signature: onchain.signature }, validated);
  return { validated, deployedAt: new Date(blockHeader.timestamp).toISOString() };
}

async function waitForConfirmedReceipt(fullHost, txHash, timeoutMs, options = {}) {
  const callNative = options.nativeRPC || nativeRPC;
  const now = options.now || Date.now;
  const sleep = options.sleep || ((milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds)));
  const deadline = now() + timeoutMs;
  while (now() < deadline) {
    try {
      const info = await callNative(fullHost, "wallet/gettransactioninfobyid", { value: txHash });
      if (info && Object.keys(info).length > 0) {
        if (typeof info.id !== "string" || info.id.toLowerCase() !== txHash.toLowerCase()) {
          throw new Error("TRON FullNode returned transaction info for the wrong transaction");
        }
        const blockNumber = safeInteger(info.blockNumber, "deployment receipt blockNumber", { min: 1 });
        const nodeInfo = await callNative(fullHost, "wallet/getnodeinfo", {});
        const { solidityHeight } = validateNodeInfo(nodeInfo);
        if (solidityHeight >= blockNumber) return info;
      }
    } catch (err) {
      if (!String(err && err.message || err).toLowerCase().includes("not found")) throw err;
    }
    await sleep(3_000);
  }
  throw new Error(`deployment ${txHash} was not confirmed before timeout`);
}

function parseOptions(env = process.env) {
  if (env.VAULT_TRON_ENERGY_PREVIEW !== undefined && env.VAULT_TRON_ENERGY_PREVIEW !== "" && !["true", "false"].includes(env.VAULT_TRON_ENERGY_PREVIEW)) {
    throw new Error("VAULT_TRON_ENERGY_PREVIEW must be exactly true or false");
  }
  const chainId = uint64Env("VAULT_CHAIN_ID", env);
  const dexChainId = uint64Env("VAULT_DEX_CHAIN_ID", env);
  if (dexChainId === 0n) throw new Error("VAULT_DEX_CHAIN_ID must be a nonzero uint64");
  const epoch = uint64Env("VAULT_EPOCH", env);
  const vaultId = uint64Env("VAULT_ID", env);
  const supplied = resolvePubkey(env);
  const { px, py, rawPy } = validatePubkey(supplied.px, supplied.py);
  const network = chainId === TRON_MAINNET_CHAIN_ID ? "tron-mainnet" : "tron";
  return {
    artifactsDir: env.VAULT_ARTIFACTS_DIR === undefined ? undefined : require("./vault_deployment").resolveVaultArtifactsDirectory(env.VAULT_ARTIFACTS_DIR),
    authorityProtocol: require("./vault_deployment").authorityProtocol(env.VAULT_AUTHORITY_PROTOCOL),
    network, chainId, dexChainId, epoch, vaultId, px, py, rawPy, ...require("./vault_deployment").guardianFromConfig(env),
    jsonRPCURL: validateRPCURL(requireEnv("TRON_JSON_RPC_URL", env)),
    fullHost: validateRPCURL(requireEnv("TRON_FULL_HOST", env), "TRON_FULL_HOST"),
    recoverTxHash: normalizeRecoveryTxHash(env.VAULT_RECOVER_TX_HASH),
    deployerAddress: env.VAULT_DEPLOYER_ADDRESS && env.VAULT_DEPLOYER_ADDRESS.trim(),
    nonstandardToken: resolveNonstandardToken(chainId, env),
    feeLimit: positiveSafeIntegerEnv("VAULT_FEE_LIMIT_SUN", DEFAULT_VAULT_FEE_LIMIT_SUN, env),
    originEnergyLimit: positiveSafeIntegerEnv("VAULT_ORIGIN_ENERGY_LIMIT", MAX_ORIGIN_ENERGY_LIMIT, env),
    timeoutMs: positiveSafeIntegerEnv("VAULT_CONFIRM_TIMEOUT_MS", 300_000, env),
    energyPreview: env.VAULT_TRON_ENERGY_PREVIEW === "true",
    energyPlanJSON: env.VAULT_TRON_ENERGY_PLAN_JSON,
    energyTarget: env.VAULT_TRON_ENERGY_TARGET,
    energyRecipient: env.VAULT_TRON_ENERGY_RECIPIENT,
    energyUnitPriceSun: env.VAULT_TRON_ENERGY_UNIT_PRICE_SUN,
    energyUnitEnergy: env.VAULT_TRON_ENERGY_UNIT_ENERGY,
    energyMaxPurchaseSun: env.VAULT_TRON_ENERGY_MAX_PURCHASE_SUN,
    energyMarginBps: env.VAULT_TRON_ENERGY_MARGIN_BPS,
    energyTimeoutMs: positiveSafeIntegerEnv("VAULT_TRON_ENERGY_TIMEOUT_MS", 900_000, env),
    energyPollMs: positiveSafeIntegerEnv("VAULT_TRON_ENERGY_POLL_MS", 3_000, env),
    allowMainnet: env.VAULT_ALLOW_MAINNET_DEPLOY === "true",
    output: require("./vault_deployment").resolveVaultOutput(env, network, chainId, dexChainId, vaultId),
                                                                                   
    get privateKey() { return requireEnv("DEPLOYER_PRIVATE_KEY", env); },
  };
}

function validateVaultOptions(options) {
  require("./vault_deployment").authorityProtocol(options.authorityProtocol);
  for (const name of ["chainId", "dexChainId", "epoch", "vaultId"]) {
    if (typeof options[name] !== "bigint" || options[name] < 0n || options[name] > (1n << 64n) - 1n) {
      throw new Error(`${name} must be a uint64`);
    }
  }
  if (options.dexChainId === 0n) throw new Error("Vault DEX chain ID must be a nonzero uint64");
  if (require("./vault_deployment").authorityProtocol(options.authorityProtocol) === 3 && options.epoch === 0n) throw new Error("V3 VAULT_EPOCH must be nonzero");
  if (options.chainId === 0n || options.chainId > 0xffffffffn) throw new Error("TRON chain ID must be a positive uint32");
  const network = options.chainId === TRON_MAINNET_CHAIN_ID ? "tron-mainnet" : "tron";
  if (options.network !== network) throw new Error("TRON network name differs from chain ID");
  const key = validatePubkey(options.px, options.rawPy);
  const guardian = validatePubkey(options.guardianPx, options.guardianPy);
  if (guardian.rawPy !== guardian.py || guardian.px === key.px && guardian.py === key.py) throw new Error("guardian key must be canonical and distinct from operator");
  if (options.py !== key.py) throw new Error("Vault canonical public key differs from raw DKG point");
  safeInteger(options.feeLimit, "fee limit", { min: 1, max: MAX_VAULT_FEE_LIMIT_SUN });
  safeInteger(options.originEnergyLimit, "origin energy limit", { min: 1, max: MAX_ORIGIN_ENERGY_LIMIT });
  safeInteger(options.timeoutMs, "confirmation timeout", { min: 1 });
  validateRPCURL(options.jsonRPCURL);
  validateRPCURL(options.fullHost, "TRON_FULL_HOST");
  if (options.chainId === TRON_MAINNET_CHAIN_ID && options.nonstandardToken !== TRON_MAINNET_USDT) {
    throw new Error(`TRON mainnet nonstandard token must be ${TRON_MAINNET_USDT}`);
  }
}

async function estimateDeploymentEnergy(options, creationInput, ownerHex, callNative) {
  try {
    const estimate = await callNative(options.fullHost, "wallet/estimateenergy", {
      owner_address: ownerHex, data: creationInput, call_value: 0,
    });
    const energy = asBigInt(estimate.energy_required, "estimated deployment Energy");
    if (estimate.result?.result !== true || energy <= 0n) throw new Error("creation estimate unavailable");
    return { estimateEnergy: energy, estimateSource: "wallet/estimateenergy" };
  } catch (_) {
    const manual = options.energyTarget;
    if (typeof manual !== "string" || !/^[1-9][0-9]*$/.test(manual)) {
      throw new Error("TRON creation Energy estimation unavailable; an explicit reviewed VAULT_TRON_ENERGY_TARGET is required");
    }
    return { estimateEnergy: BigInt(manual), targetEnergyOverride: BigInt(manual), estimateSource: "manual-target" };
  }
}

function parseEnergyAuthorization(raw) {
  if (raw === undefined || raw === null || raw === "") return null;
  if (typeof raw !== "string" || Buffer.byteLength(raw, "utf8") > 64 * 1024) throw new Error("invalid approved TRON Energy plan JSON");
  let plan;
  try { plan = JSON.parse(raw); } catch (_) { throw new Error("invalid approved TRON Energy plan JSON"); }
  if (!plan || typeof plan !== "object" || Array.isArray(plan)) throw new Error("invalid approved TRON Energy plan JSON");
  return plan;
}

function validateEnergyAuthorization(approved, live, network) {
  for (const field of ["version", "deploymentIdentityHash", "creationInputHash", "deployer", "recipient", "unitPriceSun", "unitEnergy", "maxPurchaseSun"]) {
    if (approved[field] !== live[field]) throw new Error(`approved TRON Energy plan conflicts with ${field}`);
  }
  for (const field of ["targetEnergy", "purchaseUnits", "bandwidthReserveSun", "paymentBandwidthReserveSunPerUnit", "requiredFeeLimitSun"]) {
    if (asBigInt(live[field], field) > asBigInt(approved[field], `approved ${field}`)) {
      throw new Error(`live TRON Energy ${field} exceeds the approved plan; review a new preview`);
    }
  }
  const requiredFee = asBigInt(approved.targetEnergy, "approved target Energy") * BigInt(network.chainParameters.energyFee);
  if (requiredFee > asBigInt(approved.requiredFeeLimitSun, "approved fee limit") || requiredFee > BigInt(MAX_VAULT_FEE_LIMIT_SUN)) {
    throw new Error("live TRON Energy price exceeds the approved fee limit");
  }
  if (BigInt(network.chainParameters.transactionFee) > asBigInt(approved.bandwidthPriceSun, "approved Bandwidth price")) {
    throw new Error("live TRON Bandwidth price exceeds the approved reserve");
  }
  return approved;
}

async function quoteTronEnergy(options, artifact, tronWeb, deployer, network, dependencies = {}) {
  const { planEnergyPurchase, readEnergyPlan, validatePlan } = require("./tron_energy");
  const callNative = dependencies.nativeRPC || nativeRPC;
  const creationInput = constructorBytecode(tronWeb, artifact, options.px, options.rawPy, options.guardianPx, options.guardianPy, options.epoch, options.dexChainId, options.nonstandardToken);
  const ownerHex = normalizeTronAddressHex(tronWeb, deployer, "deployer");
  const [estimate, account, resources] = await Promise.all([
    estimateDeploymentEnergy(options, creationInput, ownerHex, callNative),
    callNative(options.fullHost, "wallet/getaccount", { address: deployer, visible: true }),
    callNative(options.fullHost, "wallet/getaccountresource", { address: deployer, visible: true }),
  ]);
  const input = {
    deploymentIdentity: require("./vault_deployment").vaultIdentity(options, artifact), deployer, creationInput,
    chainParameters: network.chainParameters, account, resources, ...estimate,
    recipient: options.energyRecipient, unitPriceSun: options.energyUnitPriceSun, unitEnergy: options.energyUnitEnergy,
    maxPurchaseSun: options.energyMaxPurchaseSun, marginBps: options.energyMarginBps, bandwidthReserveSun: 0n,
    paymentBandwidthReserveSunPerUnit: 0n,
    timeoutMs: options.energyTimeoutMs, pollMs: options.energyPollMs,
  };
  const preliminary = planEnergyPurchase(input);
                                                                              
                                                                              
  const bandwidthBytes = BigInt(creationInput.length / 2 + Buffer.byteLength(JSON.stringify(artifact.abi), "utf8") + 2048) + BigInt(preliminary.purchaseUnits) * 512n;
  const live = planEnergyPurchase({
    ...input,
    bandwidthReserveSun: bandwidthBytes * BigInt(network.chainParameters.transactionFee),
    paymentBandwidthReserveSunPerUnit: 512n * BigInt(network.chainParameters.transactionFee),
  });
  if (BigInt(live.requiredFeeLimitSun) > BigInt(MAX_VAULT_FEE_LIMIT_SUN)) throw new Error("estimated TRON deployment exceeds the maximum permitted fee limit");
  const saved = readEnergyPlan(options.output + ".energy");
  const providedRaw = parseEnergyAuthorization(options.energyPlanJSON);
  const provided = providedRaw ? validatePlan(providedRaw) : null;
  if (provided && saved && provided.planId !== saved.planId) throw new Error("approved TRON Energy plan differs from the saved purchase plan");
  const approved = provided || saved;
  const plan = approved ? validateEnergyAuthorization(approved, live, network) : live;
  return { plan, live, approved: !!approved, account, resources, creationInput };
}

async function previewTronEnergyPlan(options, artifact, dependencies = {}) {
  validateVaultOptions(options);
  if (!options.deployerAddress) throw new Error("TRON Energy preview requires VAULT_DEPLOYER_ADDRESS");
  const shared = require("./vault_deployment");
  if (options.recoverTxHash || shared.loadVaultJournal(options.output, shared.vaultIdentity(options, artifact))) {
    throw new Error("existing TRON deployment must use read-only recovery, without an Energy purchase plan");
  }
  const callNative = dependencies.nativeRPC || nativeRPC, callJSON = dependencies.jsonRPC || jsonRPC;
  const now = dependencies.now || Date.now;
  const network = await verifyTronNetworkIdentity(options.jsonRPCURL, options.fullHost, options.chainId, { nativeRPC: callNative, jsonRPC: callJSON, nowMs: now() });
  const tronWeb = (dependencies.createTronWeb || (config => new TronWeb(config)))({ fullHost: options.fullHost });
  const deployer = tronWeb.address.fromHex(normalizeTronAddressHex(tronWeb, options.deployerAddress, "VAULT_DEPLOYER_ADDRESS"));
  const quote = await quoteTronEnergy(options, artifact, tronWeb, deployer, network, dependencies);
  return {
    ...quote.plan,
    currentAvailableEnergy: availableResource(quote.resources, "EnergyLimit", "EnergyUsed").toString(),
    currentBalanceSun: optionalNonnegativeField(quote.account.balance, "account balance").toString(),
    currentRequiredBalanceSun: quote.live.requiredBalanceSun,
  };
}

                                                                               
                                                                             
async function deployTronVault(options, artifact, existing, onPending, dependencies = {}) {
  validateVaultOptions(options);
  if (!artifact || artifact.contract !== require("./vault_deployment").vaultContractName(options.authorityProtocol, true)) throw new Error("expected unified TRON Vault artifact");
  const savedHash = existing ? normalizeRecoveryTxHash(existing.txHash) : null;
  const explicitHash = normalizeRecoveryTxHash(options.recoverTxHash);
  if (existing && !savedHash) throw new Error("saved Vault deployment has no transaction hash; refusing a new deployment");
  if (savedHash && explicitHash && savedHash !== explicitHash) throw new Error("recovery transaction differs from saved deployment");
  const recoveryHash = savedHash || explicitHash;
  if (!recoveryHash && options.chainId === TRON_MAINNET_CHAIN_ID && !options.allowMainnet) {
    throw new Error("TRON mainnet deployment requires explicit VAULT_ALLOW_MAINNET_DEPLOY=true");
  }
  if (!recoveryHash && typeof onPending !== "function") throw new Error("Vault deployment requires durable pending storage before broadcast");
  const privateKey = recoveryHash ? undefined : options.privateKey;
  if (!recoveryHash && (typeof privateKey !== "string" || !/^[0-9a-fA-F]{64}$/.test(privateKey))) {
    throw new Error("a valid deployer private key is required for a new Vault deployment");
  }
  const callNative = dependencies.nativeRPC || nativeRPC;
  const callJSON = dependencies.jsonRPC || jsonRPC;
  const now = dependencies.now || Date.now;
  let identity = await verifyTronNetworkIdentity(options.jsonRPCURL, options.fullHost, options.chainId, {
    nativeRPC: callNative, jsonRPC: callJSON, nowMs: now(),
  });
  const createTronWeb = dependencies.createTronWeb || ((config) => new TronWeb(config));
  const tronWeb = createTronWeb(recoveryHash ? { fullHost: options.fullHost } : { fullHost: options.fullHost, privateKey });
  const { px, py, rawPy, guardianPx, guardianPy, epoch, dexChainId, nonstandardToken, originEnergyLimit } = options;
  let feeLimit = options.feeLimit;
  if (!tronWeb.isAddress(nonstandardToken)) throw new Error("VAULT_NONSTANDARD_TOKEN is not a TRON address");
  let deployer, txHash, address, deployedAt, onchain;
  if (recoveryHash) {
    onchain = await callNative(options.fullHost, "wallet/gettransactionbyid", { value: recoveryHash });
    const owner = onchain?.raw_data?.contract?.[0]?.parameter?.value?.owner_address;
    deployer = tronWeb.address.fromHex(normalizeTronAddressHex(tronWeb, owner, "recovered deployer"));
    if (existing && !sameTronAddress(tronWeb, deployer, existing.deployer)) throw new Error("recovered deployer differs from saved deployment");
    tronWeb.setAddress(deployer);
  } else {
    deployer = tronWeb.defaultAddress.base58;
    if (!deployer || !tronWeb.isAddress(deployer)) throw new Error("DEPLOYER_PRIVATE_KEY did not derive a TRON address");
  }
  if (options.deployerAddress && !sameTronAddress(tronWeb, deployer, options.deployerAddress)) {
    throw new Error("deployer differs from VAULT_DEPLOYER_ADDRESS");
  }
  const request = { artifact, contractName: artifact.contract, deployer, px, rawPy, guardianPx, guardianPy, epoch, dexChainId, nonstandardToken, feeLimit, originEnergyLimit };
  if (recoveryHash) {
                                                                                    
    request.feeLimit = onchain.raw_data.fee_limit;
    request.originEnergyLimit = onchain.raw_data.contract[0].parameter.value.new_contract.origin_energy_limit;
    const recovered = validateRecoveredDeploymentTransaction(tronWeb, onchain, recoveryHash, request);
    txHash = recovered.validated.txID;
    address = tronWeb.address.fromHex(recovered.validated.contractAddressHex);
    deployedAt = recovered.deployedAt;
    if (existing && !sameTronAddress(tronWeb, address, existing.address)) throw new Error("recovered address differs from saved deployment");
  } else {
    const quote = await quoteTronEnergy(options, artifact, tronWeb, deployer, identity, dependencies);
    const targetEnergy = BigInt(quote.plan.targetEnergy);
    if (!quote.approved && BigInt(quote.plan.purchaseUnits) > 0n) {
      throw new Error("TRON delegated Energy is insufficient; review a preview and supply VAULT_TRON_ENERGY_PLAN_JSON before purchasing");
    }
    if (quote.approved) {
      await require("./tron_energy").ensureEnergy({
        plan: quote.plan, journalPath: options.output + ".energy", tronWeb, privateKey,
        nativeRPC: (route, payload) => callNative(options.fullHost, route, payload),
        freshNetworkIdentity: () => verifyTronNetworkIdentity(options.jsonRPCURL, options.fullHost, options.chainId, { nativeRPC: callNative, jsonRPC: callJSON, nowMs: now() }),
        now, sleep: dependencies.sleep,
      });
    }
    identity = await verifyTronNetworkIdentity(options.jsonRPCURL, options.fullHost, options.chainId, { nativeRPC: callNative, jsonRPC: callJSON, nowMs: now() });
    if (BigInt(identity.chainParameters.transactionFee) > BigInt(quote.plan.bandwidthPriceSun)) throw new Error("live TRON Bandwidth price exceeds the approved reserve");
    const currentRequiredFee = targetEnergy * BigInt(identity.chainParameters.energyFee);
    if (currentRequiredFee > BigInt(quote.plan.requiredFeeLimitSun) || currentRequiredFee > BigInt(MAX_VAULT_FEE_LIMIT_SUN)) throw new Error("live TRON Energy price exceeds the approved fee limit");
    feeLimit = Number(currentRequiredFee);
    request.feeLimit = feeLimit;
    const unsigned = await tronWeb.transactionBuilder.createSmartContract({
      abi: artifact.abi, bytecode: artifact.bytecode, name: artifact.contract,
      feeLimit, callValue: 0, userFeePercentage: 100, originEnergyLimit,
      parameters: [px.toString(), rawPy.toString(), guardianPx.toString(), guardianPy.toString(), epoch.toString(), dexChainId.toString(), nonstandardToken],
      blockHeader: identity.blockHeader,
    }, deployer);
    const validated = validateUnsignedCreateSmartContract(tronWeb, unsigned, { ...request, blockHeader: identity.blockHeader, nowMs: now() });
    address = tronWeb.address.fromHex(validated.contractAddressHex);
    const [account, resources] = await Promise.all([
      callNative(options.fullHost, "wallet/getaccount", { address: deployer, visible: true }),
      callNative(options.fullHost, "wallet/getaccountresource", { address: deployer, visible: true }),
    ]);
    validateDeploymentResources(account, resources, validated, feeLimit, identity.chainParameters, targetEnergy);
    validateUnsignedCreateSmartContract(tronWeb, validated.transaction, { ...request, blockHeader: identity.blockHeader, nowMs: now() });
    const signed = await tronWeb.trx.sign(JSON.parse(JSON.stringify(validated.transaction)), privateKey);
    validateSignedMatchesUnsigned(signed, validated);
    txHash = validated.txID;
    deployedAt = new Date(identity.blockHeader.timestamp).toISOString();
    await onPending({ txHash, address, deployer });
    const broadcast = await tronWeb.trx.sendRawTransaction(signed);
    if (!broadcast || broadcast.result !== true) throw new Error(`TRON deployment ${txHash} broadcast was not accepted; recover the saved transaction`);
    if (broadcast.txid && normalizeRecoveryTxHash(broadcast.txid) !== txHash) throw new Error("TRON broadcaster returned another transaction hash; recover the saved transaction");
  }
  const receipt = await waitForConfirmedReceipt(options.fullHost, txHash, options.timeoutMs, { nativeRPC: callNative, now, sleep: dependencies.sleep });
  if (!receipt.receipt || receipt.receipt.result !== "SUCCESS" || (receipt.result && receipt.result !== "SUCCESS")) {
    throw new Error(`TRON deployment failed on-chain: ${receipt.receipt && receipt.receipt.result || "missing receipt result"}`);
  }
  if (!receipt.contract_address || !sameTronAddress(tronWeb, receipt.contract_address, address)) {
    throw new Error("confirmed deployment contract address differs from the signed transaction");
  }
  if (existing?.status === "verified" && existing.blockNumber !== receipt.blockNumber) {
    throw new Error("deployment block differs from saved verification");
  }
  const info = await tronWeb.trx.getContractInfo(address);
  const expectedRuntime = require("./vault_deployment").expectedVaultRuntime(artifact, dexChainId, nonstandardToken);
  const runtimeCode = verifyRuntimeCode(info.runtimecode, expectedRuntime, {});
  const contract = await tronWeb.contract(artifact.abi, address);
  const [actualP, actualGuardian, actualNonce, actualDomain, actualToken] = await Promise.all([
    contract.P().call(), contract.G().call(), contract.authorityNonce().call(), contract.dexChainId().call(), contract.nonstandardToken().call(),
  ]);
  if (asBigInt(actualGuardian[0],"G.x")!==guardianPx||asBigInt(actualGuardian[1],"G.y")!==guardianPy||asBigInt(actualP[0], "P.x") !== px || asBigInt(actualP[1], "P.y") !== py ||
      asBigInt(actualNonce, "authorityNonce") !== epoch || asBigInt(actualDomain, "dexChainId") !== dexChainId ||
      !sameTronAddress(tronWeb, actualToken, nonstandardToken)) {
    throw new Error("deployed TRON Vault state differs from requested DKG parameters or DEX domain");
  }
  if (require("./vault_deployment").authorityProtocol(options.authorityProtocol) === 3) {
    const [protocol, limit] = await Promise.all([contract.AUTHORITY_PROTOCOL().call(), contract.MAX_HANDOVERS_PER_CALL().call()]);
    require("./vault_deployment").validateVaultCapabilities(3, protocol, limit);
  }
  const runtimeCodeHash = ethers.keccak256(runtimeCode);
  if (existing?.runtimeCodeHash && existing.runtimeCodeHash.toLowerCase() !== runtimeCodeHash) throw new Error("Vault runtime hash differs from saved verification");
  return { address, deployer, txHash, deployedAt, runtimeCodeHash, blockNumber: receipt.blockNumber };
}

async function run(options, dependencies = {}) {
  validateVaultOptions(options);
  const shared = require("./vault_deployment");
                                                                              
                                                                               
  const artifact = dependencies.artifact || await shared.loadVaultArtifact(shared.vaultArtifactStore(options.artifactsDir || path.join(__dirname, "../artifacts")), shared.vaultContractName(options.authorityProtocol, true));
  const identity = shared.vaultIdentity(options, artifact);
  let current = shared.loadVaultJournal(options.output, identity);
  const onPending = async details => {
    current = shared.writeVaultPending(options.output, identity, details);
  };
  const details = await deployTronVault(options, artifact, current, onPending, dependencies);
  return shared.writeVaultVerified(options.output, identity, details, current);
}

async function main() {
  const options = parseOptions();
  if (options.energyPreview) {
    const shared = require("./vault_deployment");
    const artifact = await shared.loadVaultArtifact(shared.vaultArtifactStore(options.artifactsDir || path.join(__dirname, "../artifacts")), shared.vaultContractName(options.authorityProtocol, true));
    console.log(JSON.stringify(await previewTronEnergyPlan(options, artifact)));
    return;
  }
  const record = await run(options);
  console.log(`address=${record.address}`);
  console.log(`tx=${record.txHash}`);
  console.log(`receipt=${options.output}`);
}

if (require.main === module) {
  main().catch((err) => {
    console.error(err && err.message ? err.message : err);
    process.exitCode = 1;
  });
}

module.exports = {
  parseOptions,
  run,
  deployTronVault,
  estimateDeploymentEnergy,
  previewTronEnergyPlan,
  quoteTronEnergy,
  validateEnergyAuthorization,
  FIELD,
  TRON_MAINNET_CHAIN_ID,
  TRON_MAINNET_USDT,
  ZERO_TRON_ADDRESS,
  MAX_VAULT_FEE_LIMIT_SUN,
  DEFAULT_VAULT_FEE_LIMIT_SUN,
  MAX_ORIGIN_ENERGY_LIMIT,
  TX_EXPIRATION_MS,
  validatePubkey,
  resolvePubkey,
  uint64Env,
  positiveSafeIntegerEnv,
  validateLiveDeployment,
  resolveNonstandardToken,
  validateRPCURL,
  jsonRPC,
  nativeRPC,
  safeInteger,
  normalizeTronAddressHex,
  sameTronAddress,
  verifyChainID,
  validateNativeBlock,
  validateJSONBlock,
  validateChainParameters,
  verifyTronNetworkIdentity,
  maskImmutableReferences,
  verifyRuntimeCode,
  asBigInt,
  validateDeploymentResources,
  waitForConfirmedReceipt,
  constructorBytecode,
  deriveContractAddressHex,
  validateUnsignedCreateSmartContract,
  validateSignedMatchesUnsigned,
  normalizeRecoveryTxHash,
  validateRecoveredDeploymentTransaction,
};
