const { expect } = require('chai');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { TronWeb } = require('tronweb');
const { planEnergyPurchase, ensureEnergy, readEnergyPlan, validatePlan, verifyTransfer, DEFAULT_RECIPIENT } = require('../scripts/tron_energy');

describe('TRON deployment Energy procurement (offline)', function () {
  this.timeout(15000);
  const directories = [];
  afterEach(() => {
    for (const directory of directories.splice(0)) {
      if (path.dirname(directory) !== os.tmpdir() || !path.basename(directory).startsWith('dex-energy-test-')) throw new Error('unexpected cleanup target');
      fs.rmSync(directory, { recursive: true, force: true });
    }
  });
  function fixture(changes = {}) {
    const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'dex-energy-test-'));
    directories.push(directory);
    const privateKey = '1'.padStart(64, '0');
    const tronWeb = new TronWeb({ fullHost: 'http://127.0.0.1:1', privateKey });
                                                                                
    tronWeb.fullNode.request = async () => { throw new Error('real RPC forbidden'); };
    const state = { now: 1800000000000, energy: 0, balance: 400000000, calls: 0, builds: 0, signs: 0, sends: [], paid: new Map(),
      deliver: true, throwAfterBroadcast: false, throwBeforeBroadcast: false, balanceAfter: null, failReceipt: false, hideReceipt: false, paymentBandwidthFee: 0, ...changes };
    const input = {
      deploymentIdentity: { contract: 'SchnorrExampleTronV3', network: 'tron-mainnet', vault: 0 },
      deployer: tronWeb.defaultAddress.base58, creationInput: '0x60006000', account: { balance: state.balance },
      resources: { EnergyLimit: state.energy }, chainParameters: { energyFee: 100, transactionFee: 1000 },
      estimateEnergy: 100000, bandwidthReserveSun: 1000000, timeoutMs: 10000, pollMs: 1000,
    };
    const plan = planEnergyPurchase(input);
    const build = tronWeb.transactionBuilder.sendTrx.bind(tronWeb.transactionBuilder), sign = tronWeb.trx.sign.bind(tronWeb.trx);
    tronWeb.transactionBuilder.sendTrx = async (...args) => { state.builds++; return build(...args); };
    tronWeb.trx.sign = async (...args) => { state.signs++; return sign(...args); };
    tronWeb.trx.sendRawTransaction = async transaction => {
      const index = state.paid.size + (state.paid.has(transaction.txID) ? 0 : 1);
      const signed = JSON.parse(fs.readFileSync(path.join(directory, 'energy', String(index).padStart(3, '0') + '.signed.json')));
      const intent = JSON.parse(fs.readFileSync(path.join(directory, 'energy', String(index).padStart(3, '0') + '.broadcast.json')));
      expect(signed.txid).to.equal(transaction.txID);
      expect(intent.txid).to.equal(transaction.txID);
      if (state.throwBeforeBroadcast) throw new Error('connection lost before node received bytes');
      state.sends.push(transaction.txID);
      if (state.paid.has(transaction.txID)) return { result: false, code: 'DUP_TRANSACTION_ERROR', txid: transaction.txID };
      state.paid.set(transaction.txID, JSON.parse(JSON.stringify(transaction)));
      state.balance -= 6000000 + state.paymentBandwidthFee;
      if (state.deliver) state.energy += 130000;
      if (state.balanceAfter !== null) state.balance = state.balanceAfter;
      state.now += 3000;
      if (state.throwAfterBroadcast) throw new Error('connection lost after node accepted');
      return { result: true, txid: transaction.txID };
    };
    const deps = {
      plan, journalPath: path.join(directory, 'energy'), tronWeb, privateKey,
      now: () => state.now, sleep: async ms => { state.now += ms; },
      freshNetworkIdentity: async () => ({
        chainParameters: { energyFee: 100, transactionFee: 1000 },
        blockHeader: { ref_block_bytes: '1234', ref_block_hash: '0123456789abcdef', timestamp: state.now - 3000, expiration: state.now + 57000 },
      }),
      nativeRPC: async (route, body) => {
        state.calls++;
        if (route === 'wallet/getaccount') return { balance: state.balance };
        if (route === 'wallet/getaccountresource') return { EnergyLimit: state.energy };
        if (route === 'wallet/gettransactionbyid') return state.paid.has(body.value) ? { ...state.paid.get(body.value), ret: [{ contractRet: 'SUCCESS' }] } : {};
        if (route === 'wallet/gettransactioninfobyid') return !state.hideReceipt && state.paid.has(body.value) ? { id: body.value, blockNumber: 100, receipt: { result: state.failReceipt ? 'FAILED' : 'SUCCESS' } } : {};
        throw new Error(`unexpected mocked native RPC ${route}`);
      },
    };
    return { directory, input, plan, deps, state };
  }
  async function rejects(promise, text) {
    let error;
    try { await promise; } catch (cause) { error = cause; }
    expect(error, 'expected failure').to.be.instanceOf(Error);
    expect(error.message).to.include(text);
  }
  it('calculates exact ceiling boundaries and the user quote without treating 4M as a live estimate', () => {
    const f = fixture();
    for (const [target, units] of [[129999, 1], [130000, 1], [130001, 2], [4000000, 31]]) {
      const plan = planEnergyPurchase({ ...f.input, estimateEnergy: target, targetEnergyOverride: target, estimateSource: 'manual-target' });
      expect(plan.purchaseUnits).to.equal(units);
      expect(plan.plannedPurchaseSun).to.equal(String(units * 6000000));
      expect(plan.targetEnergy).to.equal(String(target));
      expect(plan.marginBps).to.equal(10000);
    }
    expect(f.plan.targetEnergy).to.equal('120000');
    expect(f.plan.requiredFeeLimitSun).to.equal('12000000');
    expect(f.plan.recipient).to.equal(DEFAULT_RECIPIENT);
  });
  it('refuses insufficient balance, excessive cost, invalid estimates and liquid-Energy fallback', () => {
    const f = fixture();
    for (const change of [{ account: { balance: 6999999 } }, { estimateEnergy: 1000000000 }, { estimateEnergy: 0 },
      { maxPurchaseSun: 600000001 }, { unitEnergy: 1 }, { maxBurnSun: 1 }, { recipient: f.plan.deployer }]) {
      expect(() => planEnergyPurchase({ ...f.input, ...change })).to.throw();
    }
    const changed = { ...f.plan, recipient: f.plan.deployer };
    expect(() => validatePlan(changed)).to.throw('changed approved');
    expect(fs.existsSync(f.deps.journalPath)).to.equal(false);
  });
  it('does not buy, build or sign when actual resources are already sufficient', async () => {
    const f = fixture({ energy: 120000 });
    expect(f.plan.purchaseUnits).to.equal(0);
    const result = await ensureEnergy(f.deps);
    expect(result.availableEnergy).to.equal('120000');
    expect(f.state.builds + f.state.signs + f.state.sends.length).to.equal(0);
    const zeroBudget = planEnergyPurchase({ ...f.input, maxPurchaseSun: 0 });
    expect(zeroBudget.purchaseUnits).to.equal(0);
    expect(() => planEnergyPurchase({ ...f.input, maxPurchaseSun: 0, resources: {} })).to.throw('purchase budget');
  });
  it('persists a real SDK signed payment and broadcast intent before spending, then checks actual delivery', async () => {
    const f = fixture();
    const result = await ensureEnergy(f.deps);
    expect(result.availableEnergy).to.equal('130000');
    expect(f.state.sends).to.have.length(1);
    expect(readEnergyPlan(f.deps.journalPath).planId).to.equal(f.plan.planId);
    expect(fs.readdirSync(f.deps.journalPath).sort()).to.deep.equal(['001.broadcast.json', '001.confirmed.json', '001.signed.json', 'delivery.ready.public.json', 'plan.json']);
    const record = JSON.parse(fs.readFileSync(path.join(f.deps.journalPath, '001.signed.json')));
    expect(JSON.stringify(record)).not.to.include(f.deps.privateKey);
    expect(verifyTransfer(record.transaction, f.plan, { signed: true }).txID).to.equal(f.state.sends[0]);
  });
  it('queries the same transaction after a broadcast timeout without another payment or signature', async () => {
    const f = fixture({ throwAfterBroadcast: true });
    await rejects(ensureEnergy(f.deps), 'broadcast result is unknown');
    f.state.throwAfterBroadcast = false;
    await ensureEnergy(f.deps);
    expect(f.state.sends).to.have.length(1);
    expect(f.state.builds).to.equal(1);
    expect(f.state.signs).to.equal(1);
  });
  it('recovers an uppercase RPC signature as the same saved payment without building, signing or paying again', async () => {
    const f = fixture({ throwAfterBroadcast: true });
    await rejects(ensureEnergy(f.deps), 'broadcast result is unknown');
    f.state.throwAfterBroadcast = false;
    const savedPath = path.join(f.deps.journalPath, '001.signed.json');
    const savedBytes = fs.readFileSync(savedPath);
    const saved = JSON.parse(savedBytes);
    const uppercase = saved.transaction.signature.map(signature => signature.toUpperCase());
    expect(uppercase).not.to.deep.equal(saved.transaction.signature);
    const rpc = f.deps.nativeRPC;
    f.deps.nativeRPC = async (route, body) => {
      const result = await rpc(route, body);
      return route === 'wallet/gettransactionbyid' && result.txID ? { ...result, signature: uppercase } : result;
    };
    const before = { builds: f.state.builds, signs: f.state.signs, sends: f.state.sends.length };
    const result = await ensureEnergy(f.deps);
    expect(result.availableEnergy).to.equal('130000');
    expect(f.state.builds - before.builds).to.equal(0);
    expect(f.state.signs - before.signs).to.equal(0);
    expect(f.state.sends.length - before.sends).to.equal(0);
    expect(f.state.paid.size).to.equal(1);
    expect(fs.readFileSync(savedPath).equals(savedBytes)).to.equal(true);
    expect(fs.existsSync(path.join(f.deps.journalPath, '001.confirmed.json'))).to.equal(true);
  });
  it('rejects changed signature bytes and missing, extra or nonhex RPC signatures without repaying', async () => {
    for (const mutation of [
      signatures => [(signatures[0][0] === '0' ? '1' : '0') + signatures[0].slice(1)],
      () => [],
      signatures => [signatures[0], signatures[0]],
      signatures => ['g' + signatures[0].slice(1)],
    ]) {
      const f = fixture({ throwAfterBroadcast: true });
      await rejects(ensureEnergy(f.deps), 'broadcast result is unknown');
      f.state.throwAfterBroadcast = false;
      const rpc = f.deps.nativeRPC;
      f.deps.nativeRPC = async (route, body) => {
        const result = await rpc(route, body);
        return route === 'wallet/gettransactionbyid' && result.txID ? { ...result, signature: mutation(result.signature) } : result;
      };
      const before = { builds: f.state.builds, signs: f.state.signs, sends: f.state.sends.length };
      await rejects(ensureEnergy(f.deps), '');
      expect(f.state.builds - before.builds).to.equal(0);
      expect(f.state.signs - before.signs).to.equal(0);
      expect(f.state.sends.length - before.sends).to.equal(0);
      expect(f.state.paid.size).to.equal(1);
      expect(fs.existsSync(path.join(f.deps.journalPath, '001.confirmed.json'))).to.equal(false);
    }
  });
  it('resumes a durable broadcast intent that never reached the node using exactly the saved signature', async () => {
    const f = fixture({ throwBeforeBroadcast: true });
    await rejects(ensureEnergy(f.deps), 'broadcast result is unknown');
    const saved = JSON.parse(fs.readFileSync(path.join(f.deps.journalPath, '001.signed.json')));
    expect(fs.existsSync(path.join(f.deps.journalPath, '001.broadcast.json'))).to.equal(true);
    expect(f.state.sends).to.have.length(0);
    f.state.throwBeforeBroadcast = false;
    await ensureEnergy(f.deps);
    expect(f.state.sends).to.deep.equal([saved.txid]);
    expect(f.state.paid.get(saved.txid)).to.deep.equal(saved.transaction);
    expect(f.state.builds).to.equal(1);
    expect(f.state.signs).to.equal(1);
  });
  it('stops after a confirmed payment with no delegation and does not top up on restart', async () => {
    const f = fixture({ deliver: false });
    await rejects(ensureEnergy(f.deps), 'wait timed out');
    expect(f.state.sends).to.have.length(1);
    const calls = f.state.calls;
    await rejects(ensureEnergy(f.deps), 'wait timed out');
    expect(f.state.sends).to.have.length(1);
    expect(f.state.calls - calls).to.be.lessThan(10);
    expect(f.state.calls).to.be.lessThan(40);
                                                                           
    f.state.energy = 130000;
    await ensureEnergy(f.deps);
    expect(f.state.sends).to.have.length(1);
  });
  it('does not make a second transfer when an accepted first payment cannot be confirmed', async () => {
    const f = fixture({ hideReceipt: true });
    await rejects(ensureEnergy(f.deps), 'wait timed out');
    await rejects(ensureEnergy(f.deps), 'wait timed out');
    expect(f.state.sends).to.have.length(1);
  });
  it('refuses failed payment receipts and post-payment bandwidth insolvency', async () => {
    const failed = fixture({ failReceipt: true });
    await rejects(ensureEnergy(failed.deps), 'receipt failed');
    expect(failed.state.sends).to.have.length(1);
    const depleted = fixture({ balanceAfter: 0 });
    await rejects(ensureEnergy(depleted.deps), 'remaining deployment bandwidth');
  });
  it('does not sign a new payment when the live Bandwidth price exceeds approval', async () => {
    const f = fixture();
    const fresh = f.deps.freshNetworkIdentity;
    f.deps.freshNetworkIdentity = async () => {
      const network = await fresh();
      network.chainParameters.transactionFee++;
      return network;
    };
    await rejects(ensureEnergy(f.deps), 'Bandwidth price exceeds');
    expect(f.state.builds + f.state.signs + f.state.sends.length).to.equal(0);
  });
  it('uses separate bounded payment slots for multiple purchases', async () => {
    const f = fixture();
    f.plan = planEnergyPurchase({ ...f.input, estimateEnergy: 250000, timeoutMs: 30000 });
    f.deps.plan = f.plan;
    await ensureEnergy(f.deps);
    expect(f.plan.targetEnergy).to.equal('300000');
    expect(new Set(f.state.sends).size).to.equal(3);
    expect(fs.readdirSync(f.deps.journalPath)).to.have.length(11);
    expect(f.state.calls).to.be.lessThan(35);
  });
  it('waits through a stale final resource read after both paid slots delivered, then records real readiness', async () => {
    const f = fixture();
    f.deps.plan = planEnergyPurchase({ ...f.input, estimateEnergy: 177099 });
    expect(f.deps.plan.targetEnergy).to.equal('212519'); expect(f.deps.plan.purchaseUnits).to.equal(2);
    const rpc = f.deps.nativeRPC; let reads = 0;
    f.deps.nativeRPC = async (route, body) => {
      const value = await rpc(route, body);
      if (route === 'wallet/getaccountresource' && ++reads === 4) return { EnergyLimit: 131008 };
      return value;
    };
    const startedAt = f.state.now;
    const result = await ensureEnergy(f.deps);
    expect(result.availableEnergy).to.equal('260000'); expect(f.state.sends).to.have.length(2);
    expect(f.state.builds).to.equal(2); expect(f.state.signs).to.equal(2); expect(reads).to.equal(5);
    const evidence = JSON.parse(fs.readFileSync(path.join(f.deps.journalPath, 'delivery.ready.public.json')));
    expect(evidence.planId).to.equal(f.deps.plan.planId); expect(evidence.generation).to.equal(1); expect(evidence.status).to.equal('READY');
    expect(evidence.targetEnergy).to.equal('212519'); expect(evidence.firstAvailableEnergy).to.equal('131008'); expect(evidence.availableEnergy).to.equal('260000');
    expect(evidence.finalResourceReads).to.equal(2); expect(evidence.confirmedPaymentCount).to.equal(2); expect(evidence.paymentTxids).to.deep.equal(f.state.sends);
    expect(evidence.deadlineMs).to.equal(startedAt + f.deps.plan.timeoutMs); expect(evidence.observedAtMs).to.be.at.most(evidence.deadlineMs);
    expect(evidence.observedUTC).to.equal(new Date(evidence.observedAtMs).toISOString()); expect(evidence.chainAnchor).to.equal(null);
    expect(JSON.stringify(evidence)).not.to.include(f.deps.privateKey); expect(evidence).not.to.have.property('transaction');
                                                                             
                                                                                 
    f.state.energy = 0; f.state.now += 3600000;
    await rejects(ensureEnergy(f.deps), 'wait timed out'); expect(f.state.sends).to.have.length(2); expect(f.state.signs).to.equal(2);
  });
  it('bounds permanently stale final polling by the original deadline without buying or signing a third slot', async () => {
    const f = fixture(); f.deps.plan = planEnergyPurchase({ ...f.input, estimateEnergy: 177099 });
    const rpc = f.deps.nativeRPC; let reads = 0;
    f.deps.nativeRPC = async (route, body) => {
      const value = await rpc(route, body);
      if (route === 'wallet/getaccountresource' && ++reads >= 4) return { EnergyLimit: 131008 };
      return value;
    };
    const startedAt = f.state.now;
    await rejects(ensureEnergy(f.deps), 'wait timed out');
    expect(f.state.now).to.equal(startedAt + f.deps.plan.timeoutMs); expect(reads).to.equal(8);
    expect(f.state.sends).to.have.length(2); expect(f.state.builds).to.equal(2); expect(f.state.signs).to.equal(2);
    const file = path.join(f.deps.journalPath, 'delivery.timeout.public.json'), bytes = fs.readFileSync(file);
    const evidence = JSON.parse(bytes);
    expect(evidence.status).to.equal('TIMED_OUT'); expect(evidence.availableEnergy).to.equal('131008'); expect(evidence.finalResourceReads).to.equal(5);
    expect(evidence.observedAtMs).to.equal(evidence.deadlineMs); expect(evidence.deadlineMs).to.equal(startedAt + f.deps.plan.timeoutMs);
    expect(evidence.confirmedPaymentCount).to.equal(2); expect(evidence.paymentTxids).to.deep.equal(f.state.sends);
    expect(fs.existsSync(path.join(f.deps.journalPath, 'delivery.ready.public.json'))).to.equal(false);
    await rejects(ensureEnergy(f.deps), 'wait timed out');
    expect(f.state.sends).to.have.length(2); expect(f.state.builds).to.equal(2); expect(f.state.signs).to.equal(2);
    expect(fs.readFileSync(file).equals(bytes)).to.equal(true); expect(reads).to.be.at.most(12);
  });
  it('keeps RPC and journal work linear at 31 and the maximum 100 payment slots', async () => {
    for (const [target, count] of [[4000000, 31], [13000000, 100]]) {
      const f = fixture({ balance: 700000000 });
      f.deps.plan = planEnergyPurchase({ ...f.input, estimateEnergy: target, targetEnergyOverride: target,
        estimateSource: 'manual-target', timeoutMs: 900000, maxPurchaseSun: 600000000 });
      await ensureEnergy(f.deps);
      expect(f.state.sends).to.have.length(count);
      expect(new Set(f.state.sends).size).to.equal(count);
      expect(f.state.builds).to.equal(count);
      expect(f.state.signs).to.equal(count);
      expect(f.state.calls).to.be.at.most(5 * count + 4);
      expect(fs.readdirSync(f.deps.journalPath)).to.have.length(3 * count + 2);
    }
  });
  it('spends the exact preflight balance without reserving already consumed payment bandwidth again', async () => {
    for (const count of [1, 3, 31]) {
      const perPayment = 512000, deploymentReserve = 1000000;
      const balance = count * (6000000 + perPayment) + deploymentReserve;
      const f = fixture({ balance, paymentBandwidthFee: perPayment });
      const input = { ...f.input, estimateEnergy: count * 130000, targetEnergyOverride: count * 130000,
        estimateSource: 'manual-target', timeoutMs: 900000, account: { balance },
        bandwidthReserveSun: deploymentReserve + count * perPayment, paymentBandwidthReserveSunPerUnit: perPayment };
      f.deps.plan = planEnergyPurchase(input);
      expect(() => planEnergyPurchase({ ...input, account: { balance: balance - 1 } })).to.throw('TRON balance cannot cover');
      if (count === 3) {
        f.state.throwAfterBroadcast = true;
        await rejects(ensureEnergy(f.deps), 'broadcast result is unknown');
        f.state.throwAfterBroadcast = false;
      }
      await ensureEnergy(f.deps);
      expect(f.state.sends).to.have.length(count);
      expect(f.state.balance).to.equal(deploymentReserve);
      await ensureEnergy(f.deps);
      expect(f.state.sends).to.have.length(count);
      expect(f.state.balance).to.equal(deploymentReserve);
    }
  });
  it('refuses a repeated transaction ID across payment slots', async () => {
    const f = fixture();
    f.deps.plan = planEnergyPurchase({ ...f.input, estimateEnergy: 250000, timeoutMs: 30000 });
    const anchored = await f.deps.freshNetworkIdentity();
    f.deps.freshNetworkIdentity = async () => anchored;
    await rejects(ensureEnergy(f.deps), 'repeats an earlier transaction ID');
    expect(f.state.sends).to.have.length(1);
    expect(f.state.signs).to.equal(1);
    expect(fs.existsSync(path.join(f.deps.journalPath, '002.signed.json'))).to.equal(false);
  });
  it('never pays through a changed quote, corrupted journal, missing slot or persistence failure', async () => {
    const f = fixture();
    await ensureEnergy(f.deps);
    const changed = planEnergyPurchase({ ...f.input, estimateEnergy: 110000 });
    await rejects(ensureEnergy({ ...f.deps, plan: changed }), 'another approved');
    fs.writeFileSync(path.join(f.deps.journalPath, '001.signed.json'), '{');
    await rejects(ensureEnergy(f.deps), '');
    expect(f.state.sends).to.have.length(1);
    const broken = fixture();
    fs.mkdirSync(broken.deps.journalPath);
    fs.mkdirSync(path.join(broken.deps.journalPath, 'plan.json'));
    await rejects(ensureEnergy(broken.deps), 'unsafe');
    expect(broken.state.sends).to.have.length(0);
  });
  it('rejects mutated payment recipients, extra value, stale anchors and foreign signatures', async () => {
    const f = fixture();
    await ensureEnergy(f.deps);
    const original = JSON.parse(fs.readFileSync(path.join(f.deps.journalPath, '001.signed.json'))).transaction;
    for (const mutate of [
      tx => { tx.raw_data.contract[0].parameter.value.amount++; },
      tx => { tx.raw_data.contract[0].parameter.value.to_address = TronWeb.address.toHex(f.plan.deployer); },
      tx => { tx.raw_data.data = 'deadbeef'; },
      tx => { tx.raw_data.contract[0].Permission_id = 2; },
      tx => { tx.signature = ['00'.repeat(65)]; },
      tx => { tx.raw_data.ref_block_hash = 'ff'.repeat(8); },
    ]) {
      const tx = JSON.parse(JSON.stringify(original)); mutate(tx);
      expect(() => verifyTransfer(tx, f.plan, { signed: true })).to.throw();
    }
    expect(() => verifyTransfer(original, f.plan, { signed: true, nowMs: f.state.now + 60000 })).to.throw('stale');
  });
  it('concurrent invocations can reserve only one payment per slot', async () => {
    const f = fixture();
    const results = await Promise.allSettled([ensureEnergy(f.deps), ensureEnergy(f.deps)]);
    expect(results.some(result => result.status === 'fulfilled')).to.equal(true);
    expect(new Set(f.state.sends).size).to.equal(1);
    expect(f.state.paid.size).to.equal(1);
    expect(f.state.balance).to.equal(394000000);
    const saved = JSON.parse(fs.readFileSync(path.join(f.deps.journalPath, '001.signed.json')));
    expect(saved.txid).to.equal(f.state.sends[0]);
  });
});
