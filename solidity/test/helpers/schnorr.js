const { ethers } = require('hardhat');

const FIELD = 21888242871839275222246405745257275088696311157297823662689037894645226208583n;
const ORDER = 21888242871839275222246405745257275088548364400416034343698204186575808495617n;
const SECRET_A = 0x0123456789abcdeffedcba9876543210n;
const GUARDIAN_SECRET = 0x999888777666555444333222111n;
const SECRET_B = 0xaabbccddeeff11223344556677889900n;
const DOMAIN = ethers.id('DEX_VAULT_MIXED_WITHDRAW_BATCH_V1');
const ITEM_TYPE = 'tuple(bytes32 requestId,address token,address to,uint256 grossAmount,uint256 rewardIndex)';
const BATCH_TYPE = `tuple(bytes32 domain,uint256 chainId,address vault,uint256 epoch,uint256 batchSeq,address[] rewardTokens,${ITEM_TYPE}[] items)`;
const coder = ethers.AbiCoder.defaultAbiCoder();

async function point(secret) {
  const data = coder.encode(['uint256', 'uint256', 'uint256'], [1n, 2n, secret]);
  return Array.from(coder.decode(['uint256', 'uint256'], await ethers.provider.call({
    to: '0x0000000000000000000000000000000000000007', data,
  })));
}

function canonical(pubkey) {
  return [pubkey[0], pubkey[1] > FIELD / 2n ? FIELD - pubkey[1] : pubkey[1]];
}

                                                                                        
async function sign(digest, secret = SECRET_A, nonce = 17n) {
  const raw = await point(secret);
  const pub = canonical(raw);
  const scalar = raw[1] > FIELD / 2n ? ORDER - secret : secret;
  const r = await point(nonce);
  const e = BigInt(ethers.solidityPackedKeccak256(
    ['uint256', 'uint256', 'uint256', 'uint256', 'bytes32'], [...r, ...pub, digest]
  )) % ORDER;
  return [...r, (nonce + e * scalar) % ORDER];
}

function item(to, overrides = {}) {
  const { fee: _removedFee, ...fields } = overrides;
  return {
    requestId: ethers.id('test-request-1'),
    token: ethers.ZeroAddress,
    to,
    grossAmount: 1001n,
    rewardIndex: 0n,
    ...fields,
  };
}

async function batch(vault, to, overrides = {}) {
  const legacyToken = overrides.token ?? ethers.ZeroAddress;
  let items = overrides.items ?? [item(to, { ...(overrides.item || {}), token: legacyToken })];
  if (overrides.token !== undefined && overrides.rewardTokens === undefined) {
    items = items.map(value => ({ ...value, token: legacyToken }));
  }
  const rewardTokens = overrides.rewardTokens ?? [...new Set(items.map(value => value.token.toLowerCase()))]
    .map(ethers.getAddress).sort((a, b) => BigInt(a) < BigInt(b) ? -1 : 1);
  const indexes = new Map(rewardTokens.map((token, index) => [token.toLowerCase(), BigInt(index)]));
  items = items.map(value => ({ ...value, rewardIndex: overrides.rewardTokens === undefined ? indexes.get(value.token.toLowerCase()) : value.rewardIndex }));
  const result = {
    domain: DOMAIN,
    chainId: (await ethers.provider.getNetwork()).chainId,
    vault: await vault.getAddress(),
    epoch: await vault.authorityNonce(),
    batchSeq: await vault.nextBatchSeq(),
    rewardTokens,
    items,
  };
  for (const key of ['domain', 'chainId', 'vault', 'epoch', 'batchSeq', 'rewardTokens']) {
    if (overrides[key] !== undefined) result[key] = overrides[key];
  }
  return result;
}

function batchDigest(value) {
  return ethers.keccak256(coder.encode([BATCH_TYPE], [value]));
}

async function batchSignature(value, secret = SECRET_A, nonce = 17n) {
  return sign(batchDigest(value), secret, nonce);
}

async function changeSignature(vault, newKey, epoch, secret = SECRET_A, overrides = {}) {
  const chainId = overrides.chainId ?? (await ethers.provider.getNetwork()).chainId;
  const address = overrides.vault ?? await vault.getAddress();
  const digest = ethers.keccak256(coder.encode(
    ['uint256', 'address', 'uint256', 'uint256', 'uint256'], [chainId, address, epoch, ...newKey]
  ));
  return sign(digest, secret);
}

async function guardianPoint() { return point(GUARDIAN_SECRET); }
async function guardianSignature(value) { return sign(batchDigest(value), GUARDIAN_SECRET, 7919n); }
async function dualHandover(vault,newKey,nextEpoch,secret=SECRET_A,scope={}) {
 const key=canonical(newKey),from=BigInt(nextEpoch)-1n;
 const digest=ethers.keccak256(coder.encode(
 ['bytes32','uint256','uint256','address','uint256','uint256','uint256','uint256','uint256'],
 [ethers.id('DEX_VAULT_AUTHORITY_HANDOVER_V3'),scope.dexChainId??await vault.dexChainId(),scope.chainId??(await ethers.provider.getNetwork()).chainId,
 scope.vault??await vault.getAddress(),from,nextEpoch,...key,scope.cutoff??await vault.nextBatchSeq()]));
 const op=await sign(digest,secret),g=await sign(digest,GUARDIAN_SECRET,7919n);
 return {fromEpoch:from,toEpoch:nextEpoch,newPx:key[0],newPy:key[1],frozenNextBatchSeq:scope.cutoff??await vault.nextBatchSeq(),
 rx:op[0],ry:op[1],s:op[2],guardianRx:g[0],guardianRy:g[1],guardianS:g[2]};
}

module.exports = {
  FIELD, ORDER, SECRET_A, SECRET_B, GUARDIAN_SECRET, guardianPoint, guardianSignature, dualHandover, DOMAIN, ITEM_TYPE, BATCH_TYPE, coder,
  point, canonical, sign, item, batch, batchDigest, batchSignature, changeSignature,
};
