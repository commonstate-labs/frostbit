                                                                                  
const fs = require('fs');
const path = require('path');
const { createHash } = require('crypto');
const { isDeepStrictEqual } = require('util');
const { TronWeb, utils } = require('tronweb');

const DEFAULT_RECIPIENT = 'TEUnJw4ZkiFqZ2NBz5DbWDkaJJEEvAAAAA';
const MAX_PURCHASES = 100;
const MAX_PURCHASE_SUN = 600_000_000n;
const MAX_TRANSACTION_BYTES = 64 * 1024;
const DECIMAL = /^(0|[1-9][0-9]*)$/;
const check = (ok, message) => { if (!ok) throw new Error(message); };
const canonical = value => Array.isArray(value) ? value.map(canonical) : value && typeof value === 'object'
  ? Object.fromEntries(Object.keys(value).sort().map(key => [key, canonical(value[key])])) : value;
const hash = value => createHash('sha256').update(typeof value === 'string' || Buffer.isBuffer(value) ? value : JSON.stringify(canonical(value))).digest('hex');
function uint(value, name, positive = false) {
  check(typeof value === 'bigint' || typeof value === 'number' && Number.isSafeInteger(value) || typeof value === 'string' && DECIMAL.test(value), `invalid ${name}`);
  const result = BigInt(value);
  check(result >= (positive ? 1n : 0n) && result <= BigInt(Number.MAX_SAFE_INTEGER), `invalid ${name}`);
  return result;
}
function address(value, name) {
  check(typeof value === 'string' && TronWeb.isAddress(value), `invalid ${name}`);
  return TronWeb.address.fromHex(TronWeb.address.toHex(value));
}
function available(resources, limit, used) {
  check(resources && typeof resources === 'object' && !Array.isArray(resources), 'invalid TRON account resources');
  const l = uint(resources[limit] ?? 0, limit), u = uint(resources[used] ?? 0, used);
  return l > u ? l - u : 0n;
}
function planScope(plan) {
  const names = ['version', 'deploymentIdentityHash', 'creationInputHash', 'deployer', 'recipient', 'estimatedEnergy', 'targetEnergy',
    'purchaseUnits', 'unitPriceSun', 'unitEnergy', 'maxPurchaseSun', 'plannedPurchaseSun', 'bandwidthReserveSun', 'paymentBandwidthReserveSunPerUnit', 'requiredFeeLimitSun',
    'energyPriceSun', 'bandwidthPriceSun', 'marginBps', 'estimateSource', 'maxBurnSun', 'timeoutMs', 'pollMs'];
  return Object.fromEntries(names.map(name => [name, plan[name]]));
}
function planEnergyPurchase(input) {
  const deployer = address(input.deployer, 'energy payer'), recipient = address(input.recipient ?? DEFAULT_RECIPIENT, 'energy service recipient');
  check(deployer !== recipient, 'energy payer and recipient must differ');
  check(input.deploymentIdentity && typeof input.deploymentIdentity === 'object', 'deployment identity is required');
  const code = String(input.creationInput || '').replace(/^0x/i, '');
  check(/^(?:[0-9a-f]{2})+$/i.test(code), 'complete deployment creation input is required');
  const estimate = uint(input.estimateEnergy, 'deployment Energy estimate', true);
  const manual = input.targetEnergyOverride !== undefined && input.targetEnergyOverride !== null;
  const marginBps = manual ? 10000 : Number(uint(input.marginBps ?? 12000, 'Energy margin', true));
  check(marginBps >= 10000 && marginBps <= 20000, 'Energy margin must be between 100% and 200%');
  const target = manual ? uint(input.targetEnergyOverride, 'explicit deployment Energy target', true) : (estimate * BigInt(marginBps) + 9999n) / 10000n;
  const source = input.estimateSource || (manual ? 'manual-target' : 'wallet/estimateenergy');
  check(source === 'wallet/estimateenergy' || source === 'manual-target', 'unsupported deployment Energy estimate source');
  check(manual === (source === 'manual-target'), 'manual Energy target must be explicitly identified');
  const current = available(input.resources, 'EnergyLimit', 'EnergyUsed');
  const shortfall = target > current ? target - current : 0n;
  const unitEnergy = uint(input.unitEnergy ?? 130000, 'rental unit Energy', true);
  const unitPrice = uint(input.unitPriceSun ?? 6000000, 'rental unit price', true);
  const maxPurchase = uint(input.maxPurchaseSun ?? 300000000, 'maximum rental spend');
  check(maxPurchase <= MAX_PURCHASE_SUN, 'maximum rental spend exceeds 600 TRX');
  const units = (shortfall + unitEnergy - 1n) / unitEnergy, cost = units * unitPrice;
  check(units <= BigInt(MAX_PURCHASES) && cost <= maxPurchase, 'Energy rental plan exceeds the approved purchase budget');
  const reserve = uint(input.bandwidthReserveSun ?? 0, 'liquid bandwidth reserve');
  const paymentReserve = uint(input.paymentBandwidthReserveSunPerUnit ?? 0, 'per-payment bandwidth reserve');
  check(units * paymentReserve <= reserve, 'payment bandwidth reserves exceed the approved total reserve');
  const balance = uint(input.account?.balance ?? 0, 'deployer TRX balance');
  const price = uint(input.chainParameters?.energyFee, 'on-chain Energy price', true);
  const bandwidthPrice = uint(input.chainParameters?.transactionFee, 'on-chain Bandwidth price', true);
  const fee = target * price;
  check(fee <= 5_000_000_000n, 'deployment Energy target exceeds the maximum fee limit');
  check(balance >= cost + reserve, `TRON balance cannot cover ${cost + reserve} SUN of rental and bandwidth reserve`);
  check(uint(input.maxBurnSun ?? 0, 'Energy burn allowance') === 0n, 'Energy rental requires actual available resources; liquid TRX is not Energy');
  const timeoutMs = Number(uint(input.timeoutMs ?? 900000, 'rental deadline', true));
  const pollMs = Number(uint(input.pollMs ?? 3000, 'resource polling interval', true));
  check(timeoutMs <= 1800000 && pollMs >= 250 && pollMs <= 30000 && pollMs <= timeoutMs, 'rental polling or deadline is outside its bounded range');
  const plan = {
    version: 1, deploymentIdentityHash: hash(input.deploymentIdentity), creationInputHash: hash(Buffer.from(code, 'hex')),
    deployer, recipient, estimatedEnergy: estimate.toString(), targetEnergy: target.toString(), availableEnergy: current.toString(),
    shortfallEnergy: shortfall.toString(), purchaseUnits: Number(units), unitPriceSun: unitPrice.toString(), unitEnergy: unitEnergy.toString(),
    maxPurchaseSun: maxPurchase.toString(), plannedPurchaseSun: cost.toString(), bandwidthReserveSun: reserve.toString(), paymentBandwidthReserveSunPerUnit: paymentReserve.toString(),
    requiredBalanceSun: (cost + reserve).toString(), availableBalanceSun: balance.toString(), requiredFeeLimitSun: fee.toString(),
    energyPriceSun: price.toString(), bandwidthPriceSun: bandwidthPrice.toString(), marginBps, estimateSource: source, maxBurnSun: '0', timeoutMs, pollMs,
  };
  plan.planId = hash(planScope(plan));
  return plan;
}
function validatePlan(plan) {
  check(plan && plan.version === 1 && plan.planId === hash(planScope(plan)), 'invalid or changed approved Energy plan');
  for (const name of ['deploymentIdentityHash', 'creationInputHash', 'planId']) check(/^[0-9a-f]{64}$/.test(plan[name]), `invalid ${name}`);
  check(address(plan.deployer, 'energy payer') === plan.deployer && address(plan.recipient, 'energy recipient') === plan.recipient && plan.deployer !== plan.recipient, 'noncanonical Energy plan addresses');
  check(Number.isSafeInteger(plan.purchaseUnits) && plan.purchaseUnits >= 0 && plan.purchaseUnits <= MAX_PURCHASES, 'invalid approved purchase count');
  const cost = BigInt(plan.purchaseUnits) * uint(plan.unitPriceSun, 'unit price', true);
  const max = uint(plan.maxPurchaseSun, 'rental budget'), reserve = uint(plan.bandwidthReserveSun, 'bandwidth reserve');
  const paymentReserve = uint(plan.paymentBandwidthReserveSunPerUnit, 'per-payment bandwidth reserve');
  check(BigInt(plan.purchaseUnits) * paymentReserve <= reserve, 'payment bandwidth reserves exceed the approved total reserve');
  check(cost === uint(plan.plannedPurchaseSun, 'planned rental spend') && cost <= max && max <= MAX_PURCHASE_SUN, 'invalid approved rental budget');
  check(uint(plan.requiredBalanceSun, 'required TRX balance') === cost + reserve && plan.maxBurnSun === '0', 'invalid approved balance reserve');
  const target = uint(plan.targetEnergy, 'target Energy', true), price = uint(plan.energyPriceSun, 'Energy price', true);
  uint(plan.bandwidthPriceSun, 'Bandwidth price', true);
  uint(plan.unitEnergy, 'rental unit Energy', true);
  check(uint(plan.requiredFeeLimitSun, 'fee limit', true) === target * price && target * price <= 5_000_000_000n, 'invalid approved deployment fee limit');
  check(Number.isSafeInteger(plan.timeoutMs) && plan.timeoutMs > 0 && plan.timeoutMs <= 1800000 && Number.isSafeInteger(plan.pollMs) && plan.pollMs >= 250 && plan.pollMs <= 30000 && plan.pollMs <= plan.timeoutMs, 'invalid approved rental timeout');
  return plan;
}
function readJSON(filename, maxBytes = MAX_TRANSACTION_BYTES) {
  const stat = fs.lstatSync(filename);
  check(stat.isFile() && !stat.isSymbolicLink() && stat.nlink === 1 && stat.size > 0 && stat.size <= maxBytes, 'unsafe or oversized Energy purchase journal');
  return JSON.parse(fs.readFileSync(filename, 'utf8'));
}
function readEnergyPlan(directory) {
  const filename = path.join(directory, 'plan.json');
  if (!fs.existsSync(filename)) return null;
  const stored = readJSON(filename);
  check(stored.version === 1 && Number.isSafeInteger(stored.startedAtMs) && stored.startedAtMs > 0, 'invalid Energy purchase journal');
  return validatePlan(stored.plan);
}
function writeExclusive(filename, value) {
  const data = JSON.stringify(value, null, 2) + '\n';
  check(Buffer.byteLength(data) <= MAX_TRANSACTION_BYTES, 'Energy purchase journal record is too large');
  const fd = fs.openSync(filename, 'wx', 0o600);
  try { fs.writeFileSync(fd, data); fs.fsyncSync(fd); } finally { fs.closeSync(fd); }
  if (process.platform !== 'win32') {
    const parent = fs.openSync(path.dirname(filename), 'r');
    try { fs.fsyncSync(parent); } finally { fs.closeSync(parent); }
  }
}
function reserve(filename, value) {
  try { writeExclusive(filename, value); return value; }
  catch (error) { if (error.code !== 'EEXIST') throw error; return readJSON(filename); }
}
function verifyTransfer(transaction, plan, options = {}) {
  check(transaction && typeof transaction === 'object' && !Array.isArray(transaction), 'invalid Energy payment transaction');
  const raw = transaction.raw_data, contracts = raw?.contract;
  check(raw && contracts?.length === 1 && contracts[0].type === 'TransferContract', 'Energy purchase must be one plain TRX transfer');
  const contract = contracts[0], value = contract.parameter?.value;
  const hex = a => TronWeb.address.toHex(a).toLowerCase();
  check(value && value.owner_address?.toLowerCase() === hex(plan.deployer) && value.to_address?.toLowerCase() === hex(plan.recipient) && uint(value.amount, 'payment amount', true) === BigInt(plan.unitPriceSun), 'Energy payment owner, recipient or amount differs from approval');
  check(contract.parameter?.type_url === 'type.googleapis.com/protocol.TransferContract' && Object.keys(contract).every(key => ['parameter', 'type'].includes(key)), 'Energy payment has unexpected contract metadata');
  check(Object.keys(value).every(key => ['owner_address', 'to_address', 'amount'].includes(key)) && !contract.Permission_id && !raw.data && !raw.fee_limit && !raw.auths && !transaction.ret, 'Energy payment contains extra authorization, fee or memo');
  check(Object.keys(raw).every(key => ['contract', 'ref_block_bytes', 'ref_block_hash', 'expiration', 'timestamp'].includes(key)), 'Energy payment has unexpected transaction fields');
  check(typeof transaction.raw_data_hex === 'string' && /^(?:[0-9a-f]{2})+$/i.test(transaction.raw_data_hex) && transaction.raw_data_hex.length / 2 <= MAX_TRANSACTION_BYTES, 'invalid Energy payment raw bytes');
  check(/^[0-9a-f]{64}$/.test(transaction.txID || '') && utils.transaction.txCheck(transaction), 'Energy payment protobuf bytes or transaction ID differs');
  check(Number.isSafeInteger(raw.timestamp) && Number.isSafeInteger(raw.expiration) && raw.expiration > raw.timestamp && raw.expiration - raw.timestamp <= 60000, 'invalid Energy payment expiration');
  if (options.nowMs !== undefined) check(raw.timestamp <= options.nowMs + 15000 && raw.timestamp >= options.nowMs - 45000 && raw.expiration > options.nowMs + 10000, 'Energy payment anchor is stale or signing window is too short');
  if (options.blockHeader) check(['ref_block_bytes', 'ref_block_hash', 'timestamp', 'expiration'].every(name => raw[name] === options.blockHeader[name]), 'Energy payment differs from the verified network anchor');
  if (options.signed) {
    check(transaction.signature?.length === 1 && /^[0-9a-f]{130}$/i.test(transaction.signature[0]), 'Energy payment must have exactly one canonical signature');
    check(utils.crypto.ecRecover(transaction.txID, transaction.signature[0]).toLowerCase() === hex(plan.deployer), 'Energy payment signature does not belong to the payer');
  }
  else check(!transaction.signature || transaction.signature.length === 0, 'unsigned Energy payment already contains signatures');
  return transaction;
}

async function ensureEnergy(options) {
  const { tronWeb, privateKey, nativeRPC, freshNetworkIdentity } = options;
  const plan = validatePlan(options.plan);
  const generation = options.generation ?? 1;
  check(generation === 1 || generation === 2, 'unsupported Energy evidence generation');
  check(typeof nativeRPC === 'function' && typeof freshNetworkIdentity === 'function', 'Energy procurement requires validated node access');
  const now = options.now || Date.now, sleep = options.sleep || (ms => new Promise(resolve => setTimeout(resolve, ms)));
  const directory = path.resolve(options.journalPath);
  fs.mkdirSync(directory, { recursive: true, mode: 0o700 });
  check(fs.lstatSync(directory).isDirectory() && !fs.lstatSync(directory).isSymbolicLink(), 'unsafe Energy purchase journal directory');
  const files = fs.readdirSync(directory);
  check(files.length <= 3 + MAX_PURCHASES * 3 && (files.length === 0 || files.includes('plan.json')), 'incomplete or oversized Energy purchase journal');
  let lastSlot = 0;
  for (const name of files) {
    if (name === 'plan.json' || name === 'delivery.ready.public.json' || name === 'delivery.timeout.public.json') continue;
    const match = /^(\d{3})\.(signed|broadcast|confirmed)\.json$/.exec(name);
    check(match && Number(match[1]) >= 1 && Number(match[1]) <= plan.purchaseUnits, 'unexpected Energy purchase journal record');
    lastSlot = Math.max(lastSlot, Number(match[1]));
  }
  for (let previous = 1; previous <= lastSlot; previous++) check(fs.existsSync(path.join(directory, String(previous).padStart(3, '0') + '.signed.json')), 'Energy purchase journal has a missing payment slot');
  const stored = reserve(path.join(directory, 'plan.json'), { version: 1, plan, startedAtMs: now() });
  validatePlan(stored.plan);
  check(stored.plan.planId === plan.planId && isDeepStrictEqual(planScope(stored.plan), planScope(plan)), 'Energy journal belongs to another approved deployment or budget');
  check(Number.isSafeInteger(stored.startedAtMs) && stored.startedAtMs > 0, 'invalid Energy journal start time');
  const deadline = stored.startedAtMs + plan.timeoutMs;
  const target = BigInt(plan.targetEnergy), unit = BigInt(plan.unitEnergy), price = BigInt(plan.unitPriceSun), reserveSun = BigInt(plan.bandwidthReserveSun);
  const paymentReserve = BigInt(plan.paymentBandwidthReserveSunPerUnit);
  const deploymentReserve = reserveSun - BigInt(plan.purchaseUnits) * paymentReserve;
  let confirmedCount = 0;
  const snapshot = async () => {
    const [account, resources] = await Promise.all([
      nativeRPC('wallet/getaccount', { address: plan.deployer, visible: true }),
      nativeRPC('wallet/getaccountresource', { address: plan.deployer, visible: true }),
    ]);
    return { account, resources, energy: available(resources, 'EnergyLimit', 'EnergyUsed'), balance: uint(account?.balance ?? 0, 'deployer TRX balance') };
  };
  const pause = async () => {
    check(now() < deadline, 'Energy payment or delegation wait timed out; preserve the journal and do not buy again');
    await sleep(Math.min(plan.pollMs, deadline - now()));
  };
  const lookup = async (leg) => {
    const body = await nativeRPC('wallet/gettransactionbyid', { value: leg.txid });
    if (!body?.txID) return { seen: false, confirmed: false };
    const unsigned = JSON.parse(JSON.stringify(body)); delete unsigned.ret;
    verifyTransfer(unsigned, plan, { signed: true });
    const sameSignatures = body.signature.length === leg.transaction.signature.length &&
      body.signature.every((signature, index) => signature.toLowerCase() === leg.transaction.signature[index].toLowerCase());
    check(body.txID === leg.txid && body.raw_data_hex === leg.transaction.raw_data_hex && sameSignatures, 'chain payment differs from the saved signed transfer');
    check(!body.ret?.some(ret => ret.contractRet !== 'SUCCESS'), 'Energy purchase transfer failed on chain');
    const info = await nativeRPC('wallet/gettransactioninfobyid', { value: leg.txid });
    if (!info?.id) return { seen: true, confirmed: false };
    check(info.id === leg.txid && (!info.result || info.result === 'SUCCESS') && (!info.receipt?.result || info.receipt.result === 'SUCCESS'), 'Energy purchase receipt failed or differs from payment');
    return { seen: true, confirmed: Number.isSafeInteger(info.blockNumber) && info.blockNumber > 0, networkFeeSun: uint(info.fee ?? 0, 'Energy payment network fee').toString(), blockNumber: info.blockNumber };
  };
  let current = await snapshot();
  const paymentIDs = new Set();
  for (let index = 0; index < plan.purchaseUnits; index++) {
    const stem = path.join(directory, String(index + 1).padStart(3, '0'));
    const signedPath = stem + '.signed.json', intentPath = stem + '.broadcast.json', confirmedPath = stem + '.confirmed.json';
    const exists = fs.existsSync(signedPath);
    if (!exists && current.energy >= target) break;
    if (!exists) {
      check(now() < deadline, 'Energy purchase deadline expired; no new payment was made');
      const remainingReserve = reserveSun - BigInt(confirmedCount) * paymentReserve;
      check(current.balance >= BigInt(plan.purchaseUnits - index) * price + remainingReserve, 'TRON balance no longer covers the approved rental and bandwidth reserve');
      const identity = await freshNetworkIdentity();
      check(uint(identity.chainParameters?.energyFee, 'fresh Energy price', true) <= BigInt(plan.energyPriceSun), 'Energy price exceeds the approved deployment fee limit');
      check(uint(identity.chainParameters?.transactionFee, 'fresh Bandwidth price', true) <= BigInt(plan.bandwidthPriceSun), 'Bandwidth price exceeds the approved reserve; review a new plan');
      const unsigned = await tronWeb.transactionBuilder.sendTrx(plan.recipient, Number(price), plan.deployer, { blockHeader: identity.blockHeader });
      verifyTransfer(unsigned, plan, { nowMs: now(), blockHeader: identity.blockHeader });
      check(!paymentIDs.has(unsigned.txID), 'fresh Energy payment repeats an earlier transaction ID; wait for a new network anchor');
      const signed = await tronWeb.trx.sign(JSON.parse(JSON.stringify(unsigned)), privateKey);
      verifyTransfer(signed, plan, { signed: true, nowMs: now(), blockHeader: identity.blockHeader });
      check(signed.txID === unsigned.txID && signed.raw_data_hex === unsigned.raw_data_hex && isDeepStrictEqual(signed.raw_data, unsigned.raw_data), 'Energy payment changed while signing');
      reserve(signedPath, { version: 1, planId: plan.planId, index, beforeEnergy: current.energy.toString(), txid: signed.txID, transaction: signed });
    }
    const leg = readJSON(signedPath);
    check(leg.version === 1 && leg.planId === plan.planId && leg.index === index && leg.txid === leg.transaction?.txID, 'Energy payment slot belongs to another plan');
    uint(leg.beforeEnergy, 'pre-payment Energy');
    verifyTransfer(leg.transaction, plan, { signed: true });
    check(!paymentIDs.has(leg.txid), 'Energy purchase journal repeats a transaction ID across payment slots');
    paymentIDs.add(leg.txid);
    const hadIntent = fs.existsSync(intentPath);
    const validateIntent = () => {
      const intent = readJSON(intentPath);
      check(intent.version === 1 && intent.planId === plan.planId && intent.txid === leg.txid, 'Energy broadcast journal is inconsistent');
    };
    if (hadIntent) validateIntent();
    let status = await lookup(leg);
    if (!status.confirmed && !status.seen) {
      check(now() < deadline && leg.transaction.raw_data.expiration > now() + 10000, 'saved payment is expired or too near expiry; no replacement payment was created');
                                                                             
                                                                                
      let ownsIntent = false;
      if (!hadIntent) {
        try { writeExclusive(intentPath, { version: 1, planId: plan.planId, txid: leg.txid }); ownsIntent = true; }
        catch (error) { if (error.code !== 'EEXIST') throw error; }
      }
      validateIntent();
                                                                               
                                                                               
      if (ownsIntent || hadIntent) {
        let result;
        try { result = await tronWeb.trx.sendRawTransaction(leg.transaction); }
        catch (_) { throw new Error(`Energy payment broadcast result is unknown: ${leg.txid}; resume this journal without replacing the payment`); }
        check(result?.result === true || result?.code === 'DUP_TRANSACTION_ERROR', `Energy payment was not accepted: ${leg.txid}; inspect the saved transfer before continuing`);
        check(!result.txid || result.txid === leg.txid, 'Energy broadcaster returned another transaction ID');
        status = await lookup(leg);
      }
    }
    while (!status.confirmed) {
      check(leg.transaction.raw_data.expiration > now(), `Energy payment result remains unknown: ${leg.txid}; no replacement payment was created`);
      await pause();
      status = await lookup(leg);
    }
    const receipt = reserve(confirmedPath, { version: 1, planId: plan.planId, txid: leg.txid });
    check(receipt.version === 1 && receipt.planId === plan.planId && receipt.txid === leg.txid, 'Energy confirmation journal is inconsistent');
    confirmedCount++;
    if (options.onPaymentConfirmed) await options.onPaymentConfirmed({ version: 1, planId: plan.planId, slot: index + 1, txid: leg.txid, payer: plan.deployer, recipient: plan.recipient, rentalPrincipalSun: price.toString(), networkFeeSun: status.networkFeeSun, blockNumber: status.blockNumber });
    const delivered = BigInt(leg.beforeEnergy) + unit < target ? BigInt(leg.beforeEnergy) + unit : target;
    current = await snapshot();
    while (current.energy < delivered) { await pause(); current = await snapshot(); }
  }
                                                                            
                                                                          
                                                                            
  const firstReadAtMs = now();
  current = await snapshot();
  const firstAvailableEnergy = current.energy.toString();
  let resourceReads = 1;
  const recordDelivery = status => {
    const observedAtMs = now();
    const evidence = {
      version: 1, generation, planId: plan.planId, deploymentIdentityHash: plan.deploymentIdentityHash,
      deployer: plan.deployer, status, targetEnergy: target.toString(),
      firstReadAtMs, firstReadUTC: new Date(firstReadAtMs).toISOString(), observedAtMs, observedUTC: new Date(observedAtMs).toISOString(), deadlineMs: deadline, deadlineUTC: new Date(deadline).toISOString(),
      firstAvailableEnergy, availableEnergy: current.energy.toString(),
      availableBalanceSun: current.balance.toString(), finalResourceReads: resourceReads,
      confirmedPaymentCount: confirmedCount, paymentTxids: [...paymentIDs],
      chainAnchor: null, chainAnchorNote: 'The resource RPC reply does not expose a block anchor; timestamps describe local receipt of fresh resource replies.',
      note: 'Historical final-stage observation only; every resume reads actual resources again. No purchase is authorized by this evidence.',
    };
    const file = path.join(directory, status === 'READY' ? 'delivery.ready.public.json' : 'delivery.timeout.public.json');
    const saved = reserve(file, evidence);
    check(saved.version === 1 && saved.generation === generation && saved.planId === plan.planId && saved.deployer === plan.deployer && saved.targetEnergy === target.toString() && saved.status === status, 'Energy delivery evidence belongs to another plan');
  };
  while (current.energy < target) {
    if (now() >= deadline) {
      recordDelivery('TIMED_OUT');
      throw new Error('Energy payment or delegation wait timed out; preserve the journal and do not buy again');
    }
    await pause();
    current = await snapshot(); resourceReads++;
  }
  check(current.balance >= deploymentReserve, 'TRON balance cannot cover the remaining deployment bandwidth reserve');
  recordDelivery('READY');
  return { account: current.account, resources: current.resources, availableEnergy: current.energy.toString(), availableBalanceSun: current.balance.toString() };
}

module.exports = { DEFAULT_RECIPIENT, MAX_PURCHASES, MAX_PURCHASE_SUN, planEnergyPurchase, readEnergyPlan, validatePlan, verifyTransfer, ensureEnergy };
