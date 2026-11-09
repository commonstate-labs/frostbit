const { expect } = require('chai');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { ethers } = require('ethers');
const transport = require('../scripts/vault_e2e_chain.cjs');
const energy = require('../scripts/tron_energy');
const { createHash } = require('crypto');

describe('Real Vault E2E transport (offline)', function () {
  const roots = [];
  afterEach(() => {
    for (const dir of roots.splice(0)) {
      if (path.dirname(dir) !== os.tmpdir() || !path.basename(dir).startsWith('vault-e2e-test-')) throw new Error('unsafe fixture cleanup');
      fs.rmSync(dir, { recursive: true, force: true });
    }
  });
  function fixture(overrides = {}) {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'vault-e2e-test-')); roots.push(dir);
    const input = {
      chain: 'base', chainId: '8453', rpcUrl: 'http://127.0.0.1:1', vaultAddress: '0x2222222222222222222222222222222222222222',
      expectedPublic: { dexChainId: '51025', epoch: '1', P: ['1', '2'], G: ['1368015179489954701390400359078579693043519447331113978918064868415326638035', '9918110051302171585080402603319702774565515993150576347155970296011118125764'] },
      operation: { kind: 'depositNative', receiver: 'fbc1qlqmychls2swfdxe3qzgc433vl2anue3eeg8fa2', amount: '1000000000000' },
      maxPrincipalAtomic: '1000000000000', maxFeeAtomic: '10000000000000', extraFeeReserveAtomic: '1000000000000',
      journalDir: path.join(dir, 'operation'), timeoutMs: 1000, pollMs: 100, ...overrides,
    };
    const state = { now: 1800000000000, calls: [], sends: [], nonce: 5, gasPrice: 100000000n, sequence: 1n, sent: null, throwAfterSend: false, receipt: null, badDomain: false, attemptSucceeded: true, expectedCredits: {}, tokenCredits: {}, tokenBalance: 1000000n, allowance: 0n };
    const wallet = new ethers.Wallet('0x' + '1'.padStart(64, '0'));
    const rpc = async (method, params = []) => {
      state.calls.push(method);
      switch (method) {
        case 'eth_chainId': return ethers.toQuantity(input.chainId);
        case 'eth_getCode': return '0x60006000';
        case 'eth_getBalance': {
          let balance = 1000000000000000000n;
          if (state.receipt && params[1] !== '0x64') {
            balance += state.expectedCredits[String(params[0]).toLowerCase()] || 0n;
            if (String(params[0]).toLowerCase() === wallet.address.toLowerCase() && input.operation.kind === 'withdraw') balance -= 50000n * state.sent.gasPrice;
          }
          return ethers.toQuantity(balance);
        }
        case 'eth_gasPrice': return ethers.toQuantity(state.gasPrice);
        case 'eth_getBlockByNumber': return { number: params[0] === 'latest' ? '0x64' : params[0], hash: state.blockHash || ethers.id('canonical-receipt-block'), transactions: state.receipt ? [state.receipt.transactionHash] : [], baseFeePerGas: ethers.toQuantity(state.baseFee || 50000000n) };
        case 'eth_estimateGas': return ethers.toQuantity(50000);
        case 'eth_blockNumber': return ethers.toQuantity(100);
        case 'eth_getTransactionCount': return ethers.toQuantity(state.nonce);
        case 'eth_call': {
          if (params[0].to.toLowerCase() === '0x3333333333333333333333333333333333333333') {
            const parsed = transport.ERC20.parseTransaction(params[0]), method = parsed.name;
            let balance = state.tokenBalance;
            if (method === 'balanceOf' && state.receipt && params[1] !== '0x64') balance += state.tokenCredits[parsed.args[0].toLowerCase()] || 0n;
            return transport.ERC20.encodeFunctionResult(method, [{ balanceOf: balance, allowance: state.allowance, decimals: 6 }[method]]);
          }
          const parsed = transport.iface.parseTransaction(params[0]);
          if (parsed.name === 'consumed') return transport.iface.encodeFunctionResult('consumed', [state.badConsumed ? !Boolean(state.receipt) : Boolean(state.receipt)]);
          if (parsed.name === 'claimableReward') {
            expect(parsed.args[1]).to.equal(wallet.address);
            const reward = state.receipt && params[1] !== '0x64' ? state.rewards?.[parsed.args[0].toLowerCase()] || 0n : 0n;
            return transport.iface.encodeFunctionResult('claimableReward', [state.badReward ? reward + 1n : reward]);
          }
          const values = { P: input.expectedPublic.P, G: input.expectedPublic.G, dexChainId: [state.badDomain ? 51026 : 51025], authorityNonce: [1], nextBatchSeq: [state.sequence], AUTHORITY_PROTOCOL: [3] };
          return transport.iface.encodeFunctionResult(parsed.name, values[parsed.name]);
        }
        case 'eth_sendRawTransaction': {
          const replacementPath = path.join(input.journalDir, 'replacement.json');
          const saved = JSON.parse(fs.readFileSync(path.join(input.journalDir, fs.existsSync(replacementPath) ? 'replacement.json' : 'prepared.json')));
          const intent = JSON.parse(fs.readFileSync(path.join(input.journalDir, saved.parentHash ? 'replacement-broadcast.json' : 'broadcast.json')));
          expect(saved.raw).to.equal(params[0]); expect(intent.hash).to.equal(saved.hash);
          state.sent = ethers.Transaction.from(params[0]); state.sends.push(state.sent.hash);
          const logs = [];
          if (input.operation.kind === 'approveToken') {
            state.allowance = BigInt(input.operation.amount); state.nonce++;
            const event = transport.ERC20.encodeEventLog(transport.ERC20.getEvent('Approval'), [wallet.address, input.vaultAddress, input.operation.amount]); logs.push({ address: input.operation.token, ...event });
          } else if (input.operation.kind === 'depositNative' || input.operation.kind === 'depositToken') {
            const token = input.operation.kind === 'depositToken' ? input.operation.token : ethers.ZeroAddress;
            if (input.operation.kind === 'depositToken') {
              state.allowance -= BigInt(input.operation.amount); state.tokenBalance -= BigInt(input.operation.amount);
              logs.push({ address: input.operation.token, topics: [ethers.id('Transfer(address,address,uint256)')], data: '0x' });
            }
            const event = transport.iface.encodeEventLog(transport.iface.getEvent('Deposited'), [51025, input.vaultAddress, token, input.operation.receiver, input.operation.amount]); logs.push({ address: input.vaultAddress, ...event });
          } else {
            const { batch } = transport.validateWithdrawal(transport.normalizeInput(input));
            for (const item of batch.items) {
              const fee = (item.grossAmount + 9999n) / 10000n, net = item.grossAmount - fee;
              state.rewards ||= {}; state.rewards[item.token.toLowerCase()] = (state.rewards[item.token.toLowerCase()] || 0n) + fee;
              const event = transport.iface.encodeEventLog(transport.iface.getEvent('WithdrawalAttempted'), [item.requestId, item.to, item.token, item.grossAmount, fee, net, state.attemptSucceeded]); logs.push({ address: input.vaultAddress, ...event });
              const credits = item.token === ethers.ZeroAddress ? state.expectedCredits : state.tokenCredits;
              credits[item.to.toLowerCase()] = (credits[item.to.toLowerCase()] || 0n) + net;
            }
            const digest = ethers.keccak256(ethers.AbiCoder.defaultAbiCoder().encode([transport.BATCH], [batch]));
            const event = transport.iface.encodeEventLog(transport.iface.getEvent('WithdrawalBatchExecuted'), [digest, batch.epoch, batch.batchSeq, wallet.address, batch.items.length, batch.rewardTokens.length]); logs.push({ address: input.vaultAddress, ...event });
            state.sequence++;
          }
          state.receipt = { status: '0x1', transactionHash: state.sent.hash, blockNumber: '0x65', blockHash: ethers.id('canonical-receipt-block'), transactionIndex: '0x0', gasUsed: '0xc350', effectiveGasPrice: ethers.toQuantity(state.sent.gasPrice), logs };
          if (state.throwAfterSend) throw new Error('network response lost');
          return state.sent.hash;
        }
        case 'eth_getTransactionReceipt': return !state.receipt || params[0].toLowerCase() === state.receipt.transactionHash.toLowerCase() ? state.receipt : null;
        case 'eth_getTransactionByHash': return state.visibleTx || null;
        default: throw new Error('unexpected mock RPC: ' + method);
      }
    };
    const dependencies = { privateKey: '1'.padStart(64, '0'), rpc, now: () => state.now, sleep: async ms => { state.now += ms; } };
    return { dir, input, state, dependencies, wallet };
  }
  it('quotes without signing or payment and binds both custody keys', async () => {
    const f = fixture();
    const result = await transport.runCommand('plan', f.input, f.dependencies);
    expect(result.nonce).to.equal(5); expect(result.reservedFeeAtomic).to.equal('8500000000000');
    expect(f.state.sends).to.have.length(0); expect(fs.existsSync(f.input.journalDir)).to.equal(false);
    f.state.badDomain = true;
    await expect(transport.runCommand('plan', f.input, f.dependencies)).to.be.rejectedWith('domain/epoch');
  });
  it('broadcasts only after raw bytes and intent are durable, then resumes without another payment', async () => {
    const f = fixture();
    const first = await transport.runCommand('execute', f.input, f.dependencies);
    expect(first.deposit.amount).to.equal(f.input.operation.amount); expect(first.deposit.depositEventIndex).to.equal(0);
    f.state.nonce = 99; f.state.gasPrice = 999999999999n;
    const second = await transport.runCommand('execute', f.input, f.dependencies);
    expect(second).to.deep.equal(first); expect(f.state.sends).to.have.length(1);
  });
  it('recovers a lost broadcast response by querying the saved hash, without changing its nonce', async () => {
    const f = fixture(); f.state.throwAfterSend = true;
    await expect(transport.runCommand('execute', f.input, f.dependencies)).to.be.rejectedWith('resume this journal');
    f.state.nonce = 999;
    const recovered = await transport.runCommand('execute', f.input, f.dependencies);
    expect(recovered.transactionHash).to.equal(f.state.sent.hash); expect(f.state.sends).to.have.length(1);
  });
  it('refuses a changed receiver or fee approval in an existing raw journal', async () => {
    const f = fixture(); await transport.runCommand('prepare', f.input, f.dependencies);
    f.input.operation.amount = '999';
    await expect(transport.runCommand('execute', f.input, f.dependencies)).to.be.rejectedWith('another payer, intent or budget');
    expect(f.state.sends).to.have.length(0);
  });
  it('refuses over-budget fees and missing L2 reserve before signing', async () => {
    const f = fixture({ maxFeeAtomic: '1' });
    await expect(transport.runCommand('plan', f.input, f.dependencies)).to.be.rejectedWith('approved cap');
    delete f.input.extraFeeReserveAtomic; f.input.maxFeeAtomic = '10000000000000';
    await expect(transport.runCommand('plan', f.input, f.dependencies)).to.be.rejectedWith('data-fee reserve');
    expect(f.state.sends).to.have.length(0);
  });
  it('rejects mismatched, duplicate and missing guardian batch expectations', () => {
    const f = fixture();
    const batch = [ethers.id('DEX_VAULT_MIXED_WITHDRAW_BATCH_V1'), 8453, f.input.vaultAddress, 1, 1, [ethers.ZeroAddress], [[ethers.id('one'), ethers.ZeroAddress, f.wallet.address, 100, 0]]];
    f.input.operation = { kind: 'withdraw', batchSeq: '1', calldata: transport.iface.encodeFunctionData('withdrawBatch', [batch, [1, 2, 3], [4, 5, 6]]), expectedItems: [{ requestId: ethers.id('one'), token: ethers.ZeroAddress, to: f.wallet.address, grossAmount: '100' }] };
    expect(() => transport.normalizeInput(f.input)).not.to.throw();
    f.input.operation.expectedItems[0].grossAmount = '101';
    expect(() => transport.normalizeInput(f.input)).to.throw('differs');
    f.input.operation.expectedItems[0].grossAmount = '100'; f.input.operation.calldata = transport.iface.encodeFunctionData('withdrawBatch', [batch, [1, 2, 3], [0, 0, 0]]);
    expect(() => transport.normalizeInput(f.input)).to.throw('guardianSignature');
  });
  function tronFixture(changes = {}) {
    const f = fixture({ chain: 'trx', chainId: '728126428', nativeUrl: 'http://127.0.0.1:1', operation: { kind: 'depositNative', receiver: 'fbc1qlqmychls2swfdxe3qzgc433vl2anue3eeg8fa2', amount: '1000000' }, maxPrincipalAtomic: '1000000', maxFeeAtomic: '15000000', maxFeeLimitSun: '15000000', maxEnergyPurchaseSun: '12000000', pollMs: 250, ...changes });
    const { TronWeb } = require('tronweb');
    const originalRPC = f.dependencies.rpc;
    const height = 123456, genesis = '0'.repeat(56) + BigInt(f.input.chainId).toString(16).padStart(8, '0');
    const id = number => number.toString(16).padStart(16, '0') + 'b'.repeat(48);
    const block = number => number === 0 ? { blockID: genesis, block_header: { raw_data: {} } } : { blockID: id(number), block_header: { raw_data: { number, timestamp: f.state.now - 3000 } } };
    f.state.energy = 100000; f.state.broadcasters = 0;
    f.dependencies.rpc = async (method, params) => {
      if (method === 'eth_getBlockByNumber') { const number = Number(BigInt(params[0])); return { number: params[0], hash: '0x' + (number === 0 ? genesis : id(number)) }; }
      if (method === 'eth_blockNumber') return ethers.toQuantity(height);
      if (method === 'eth_getBalance') return ethers.toQuantity(50000000);
      return originalRPC(method, params);
    };
    f.dependencies.native = async (route, body) => {
      if (route === 'wallet/getnowblock') return block(height);
      if (route === 'wallet/getblockbynum') return block(body.num);
      if (route === 'wallet/getnodeinfo') return { block: `Num:${height},ID:${id(height)}`, solidityBlock: `Num:${height - 1},ID:${id(height - 1)}`, configNodeInfo: { codeVersion: '4.8.0' } };
      if (route === 'wallet/getchainparameters') return { chainParameter: [{ key: 'getEnergyFee', value: 100 }, { key: 'getTransactionFee', value: 1000 }, { key: 'getMaintenanceTimeInterval', value: 21600000 }] };
      if (route === 'wallet/getaccount') return { balance: 50000000 };
      if (route === 'wallet/getaccountresource') return { EnergyLimit: f.state.energy };
      if (route === 'wallet/estimateenergy') { expect(body.call_value).to.equal(1000000); return { result: { result: true }, energy_required: 36743 }; }
      if (route === 'wallet/gettransactionbyid') return {};
      if (route === 'wallet/gettransactioninfobyid') return f.state.nativeReceipt || {};
      if (route === 'wallet/broadcasttransaction') {
        f.state.broadcasters++;
        const prepared = JSON.parse(fs.readFileSync(path.join(f.input.journalDir, 'prepared.json')));
        const intent = JSON.parse(fs.readFileSync(path.join(f.input.journalDir, 'broadcast.json')));
        expect(prepared.hash).to.equal(body.txID); expect(intent.hash).to.equal(body.txID);
        const event = transport.iface.encodeEventLog(transport.iface.getEvent('Deposited'), [51025, f.input.vaultAddress, ethers.ZeroAddress, f.input.operation.receiver, f.input.operation.amount]);
        f.state.receipt = { status: '0x1', transactionHash: '0x' + body.txID, blockNumber: ethers.toQuantity(height + 1), blockHash: '0x' + id(height + 1), transactionIndex: '0x0', gasUsed: '0x0', effectiveGasPrice: '0x0', logs: [{ address: f.input.vaultAddress, ...event }] };
        f.state.nativeReceipt = { id: body.txID, blockNumber: height + 1, fee: 300000, receipt: { result: 'SUCCESS', energy_fee: 0 } };
        return { result: true, txid: body.txID };
      }
      throw new Error('unexpected native mock: ' + route);
    };
    f.dependencies.tronWeb = new TronWeb({ fullHost: f.input.nativeUrl, privateKey: '1'.padStart(64, '0') });
    f.dependencies.tronWeb.fullNode.request = async () => { throw new Error('real RPC forbidden'); };
    return f;
  }
  it('constructs a bounded native TRON call locally with the verified anchor and validates its owner signature', async () => {
    const f = tronFixture();
    const plan = await transport.runCommand('plan', f.input, f.dependencies);
    expect(plan.energyPlan.purchaseUnits).to.equal(0); expect(plan.energyPlan.targetEnergy).to.equal('44092');
    const result = await transport.runCommand('execute', f.input, f.dependencies);
    expect(result.deposit.amount).to.equal('1000000'); expect(result.energyRental.callEnergyBurnSun).to.equal('0'); expect(result.actualFeeAtomic).to.equal('300000');
    const prepared = JSON.parse(fs.readFileSync(path.join(f.input.journalDir, 'prepared.json')));
    expect(prepared.transaction.raw_data.contract[0].parameter.value.call_value).to.equal(1000000);
    expect(prepared.transaction.raw_data.fee_limit).to.equal(4409200);
    expect(prepared.transaction.signature).to.have.length(1); expect(f.state.broadcasters).to.equal(1);
    await transport.runCommand('execute', f.input, f.dependencies); expect(f.state.broadcasters).to.equal(1);
  });
  it('never silently replaces an expired prepared TRON transaction', async () => {
    const f = tronFixture();
    await transport.runCommand('prepare', f.input, f.dependencies);
    const prepared = JSON.parse(fs.readFileSync(path.join(f.input.journalDir, 'prepared.json')));
    f.state.now += 61000;
    await expect(transport.runCommand('execute', f.input, f.dependencies)).to.be.rejectedWith('never automatically replace');
    expect(JSON.parse(fs.readFileSync(path.join(f.input.journalDir, 'prepared.json'))).hash).to.equal(prepared.hash); expect(f.state.broadcasters).to.equal(0);
  });
  it('rejects excessive TRON fee_limit, Energy shortages, extra token authorization and altered call value', async () => {
    const f = tronFixture({ maxFeeLimitSun: '1' });
    await expect(transport.runCommand('prepare', f.input, f.dependencies)).to.be.rejectedWith('below estimated Energy');
    f.input.maxFeeLimitSun = '15000000';
                                                                                    
    const good = tronFixture(); await transport.runCommand('prepare', good.input, good.dependencies);
    const prepared = JSON.parse(fs.readFileSync(path.join(good.input.journalDir, 'prepared.json')));
    const normalized = transport.normalizeInput(good.input);
    prepared.transaction.raw_data.contract[0].parameter.value.call_value++;
    expect(() => transport.validateTronTrigger(prepared.transaction, normalized, good.wallet.address, true)).to.throw('value mismatch');
    prepared.transaction.raw_data.contract[0].parameter.value.call_token_value = 1;
    expect(() => transport.validateTronTrigger(prepared.transaction, normalized, good.wallet.address, true)).to.throw('authorization/token');
    expect(good.state.broadcasters).to.equal(0);
  });
  it('accepts a legitimate JSON-RPC null receipt but rejects missing result and null chain identity', async () => {
    const response = body => async () => new Response(JSON.stringify(body), { status: 200 });
    expect(await transport.jsonRPC('http://127.0.0.1:1', 'eth_getTransactionReceipt', ['0x1234'], response({ jsonrpc: '2.0', id: 1, result: null }))).to.equal(null);
    await expect(transport.jsonRPC('http://127.0.0.1:1', 'eth_getTransactionReceipt', [], response({ jsonrpc: '2.0', id: 1 }))).to.be.rejectedWith('malformed');
    await expect(transport.jsonRPC('http://127.0.0.1:1', 'eth_chainId', [], response({ jsonrpc: '2.0', id: 1, result: null }))).to.be.rejectedWith('no result');
  });
  function withdrawalFixture(count = 2) {
    const f = fixture();
    const items = Array.from({ length: count }, (_, index) => [ethers.id(`request-${index}`), ethers.ZeroAddress, f.wallet.address, 1001n, 0]);
    const batch = [ethers.id('DEX_VAULT_MIXED_WITHDRAW_BATCH_V1'), f.input.chainId, f.input.vaultAddress, 1, 1, [ethers.ZeroAddress], items];
    f.input.operation = { kind: 'withdraw', batchSeq: '1', calldata: transport.iface.encodeFunctionData('withdrawBatch', [batch, [1, 2, 3], [4, 5, 6]]), expectedItems: items.map(i => ({ requestId: i[0], token: i[1], to: i[2], grossAmount: i[3].toString() })) };
    return f;
  }
  it('verifies a two-request atomic batch including both signatures, fee rounding, net balance and exactly one sequence increment', async () => {
    const f = withdrawalFixture();
    const result = await transport.runCommand('execute', f.input, f.dependencies);
    expect(result.withdrawal.itemCount).to.equal(2); expect(result.withdrawal.allAttemptsSucceeded).to.equal(true); expect(result.withdrawal.balanceDeltasVerified).to.equal(true);
    expect(result.withdrawal.requestIds).to.deep.equal(f.input.operation.expectedItems.map(i => i.requestId)); expect(f.state.sequence).to.equal(2n); expect(f.state.sends).to.have.length(1);
    await transport.runCommand('execute', f.input, f.dependencies); expect(f.state.sends).to.have.length(1);
  });
  it('accepts 63/64-item calldata and rejects 65 before any network access', async () => {
    const work = [];
    for (const n of [1, 63, 64]) {
      const f = withdrawalFixture(n); const result = await transport.runCommand('execute', f.input, f.dependencies);
      expect(result.withdrawal.itemCount).to.equal(n); expect(f.state.sends).to.have.length(1);
                                                                          
                                                                             
      work.push(f.state.calls.length - 2 * n); expect(f.state.calls.length).to.be.at.most(40 + 2 * n);
    }
    expect(work[1]).to.equal(work[0]); expect(work[2]).to.equal(work[0]);
    const invalid = withdrawalFixture(65);
    await expect(transport.runCommand('execute', invalid.input, invalid.dependencies)).to.be.rejectedWith('outside 1..64'); expect(invalid.state.calls).to.have.length(0);
  });
  it('rejects consumed requests before signing and verifies consumed and reward state after payment', async () => {
    const prior = withdrawalFixture(); prior.state.badConsumed = true;
    await expect(transport.runCommand('prepare', prior.input, prior.dependencies)).to.be.rejectedWith('already consumed'); expect(prior.state.sends).to.have.length(0);
    const reward = withdrawalFixture(); await transport.runCommand('prepare', reward.input, reward.dependencies);
    const rpc = reward.dependencies.rpc;
    reward.dependencies.rpc = async (method, params) => {
      if (method === 'eth_call' && transport.iface.parseTransaction(params[0])?.name === 'claimableReward' && reward.state.receipt) return transport.iface.encodeFunctionResult('claimableReward', [0]);
      return rpc(method, params);
    };
    await expect(transport.runCommand('execute', reward.input, reward.dependencies)).to.be.rejectedWith('claimable reward delta'); expect(reward.state.sends).to.have.length(1);
    const consumed = withdrawalFixture(); await transport.runCommand('prepare', consumed.input, consumed.dependencies);
    const consumedRPC = consumed.dependencies.rpc;
    consumed.dependencies.rpc = async (method, params) => {
      if (method === 'eth_call' && transport.iface.parseTransaction(params[0])?.name === 'consumed' && consumed.state.receipt) return transport.iface.encodeFunctionResult('consumed', [false]);
      return consumedRPC(method, params);
    };
    await expect(transport.runCommand('execute', consumed.input, consumed.dependencies)).to.be.rejectedWith('did not consume'); expect(consumed.state.sends).to.have.length(1);
  });
  it('does not call a successful receipt a payout when an item failed after being consumed', async () => {
    const f = withdrawalFixture(); f.state.attemptSucceeded = false;
    await expect(transport.runCommand('execute', f.input, f.dependencies)).to.be.rejectedWith('batch is consumed');
    expect(fs.existsSync(path.join(f.input.journalDir, 'result.json'))).to.equal(false); expect(f.state.sequence).to.equal(2n);
    await expect(transport.runCommand('execute', f.input, f.dependencies)).to.be.rejectedWith('batch is consumed'); expect(f.state.sends).to.have.length(1);
  });
  function rejectionFixture(changes = {}) {
    const f = fixture({ maxFeeAtomic: '100000000000000', ...changes });
    const rpc = f.dependencies.rpc;
    f.state.rejectFee = true;
    f.dependencies.rpc = async (method, params) => {
      if (method === 'eth_sendRawTransaction' && f.state.rejectFee) {
        const err = new Error('maxFeePerGas 125000000 < baseFee 126000000'); err.rpcCode = -32000; err.rpcMessage = err.message; throw err;
      }
      return rpc(method, params);
    };
    return f;
  }
  it('explicitly replaces one known fee rejection with the same intent and nonce, retaining the original raw bytes', async () => {
    const f = rejectionFixture();
    await expect(transport.runCommand('execute', f.input, f.dependencies)).to.be.rejectedWith('resume this journal');
    const originalText = fs.readFileSync(path.join(f.input.journalDir, 'prepared.json'), 'utf8'), broadcastText = fs.readFileSync(path.join(f.input.journalDir, 'broadcast.json'), 'utf8');
    const original = JSON.parse(originalText); expect(fs.existsSync(path.join(f.input.journalDir, 'rejection.json'))).to.equal(true);
    f.state.rejectFee = false; f.state.baseFee = 126000000n;
    const result = await transport.runCommand('replace-rejected-fee', f.input, f.dependencies);
    const replacement = JSON.parse(fs.readFileSync(path.join(f.input.journalDir, 'replacement.json')));
    const tx = ethers.Transaction.from(replacement.raw), old = ethers.Transaction.from(original.raw);
    expect(tx.gasPrice).to.equal(252000000n); expect(tx.nonce).to.equal(old.nonce); expect(tx.data).to.equal(old.data); expect(tx.value).to.equal(old.value);
    expect(replacement.parentHash).to.equal(original.hash); expect(result.transactionHash).to.equal(replacement.hash);
    expect(fs.readFileSync(path.join(f.input.journalDir, 'prepared.json'), 'utf8')).to.equal(originalText); expect(fs.readFileSync(path.join(f.input.journalDir, 'broadcast.json'), 'utf8')).to.equal(broadcastText);
    await transport.runCommand('replace-rejected-fee', f.input, f.dependencies); expect(f.state.sends).to.have.length(1);
  });
  it('refuses fee replacement without real rejection, when old tx exists, on nonce change, or above the original budget', async () => {
    const empty = fixture(); await transport.runCommand('prepare', empty.input, empty.dependencies);
    await expect(transport.runCommand('replace-rejected-fee', empty.input, empty.dependencies)).to.be.rejected;
    for (const state of [{ visibleTx: { hash: 'pending' } }, { nonce: 6 }, { gasPrice: 100000000000n }]) {
      const f = rejectionFixture(); await expect(transport.runCommand('execute', f.input, f.dependencies)).to.be.rejected;
      Object.assign(f.state, state); f.state.rejectFee = false; f.state.baseFee = 126000000n;
      await expect(transport.runCommand('replace-rejected-fee', f.input, f.dependencies)).to.be.rejected;
      expect(fs.existsSync(path.join(f.input.journalDir, 'replacement.json'))).to.equal(false); expect(f.state.sends).to.have.length(0);
    }
  });
  it('adopts an old candidate mined after a replacement was saved instead of sending two payments', async () => {
    const f = rejectionFixture(); await expect(transport.runCommand('execute', f.input, f.dependencies)).to.be.rejected;
    const original = JSON.parse(fs.readFileSync(path.join(f.input.journalDir, 'prepared.json')));
    f.state.rejectFee = false; f.state.baseFee = 126000000n;
    const normalRPC = f.dependencies.rpc;
    let sentReplacement = false;
    f.dependencies.rpc = async (method, params) => {
      if (method === 'eth_sendRawTransaction') { sentReplacement = true; return ethers.keccak256(params[0]); }
      if (method === 'eth_getTransactionReceipt' && sentReplacement && params[0] === original.hash) {
        const event = transport.iface.encodeEventLog(transport.iface.getEvent('Deposited'), [51025, f.input.vaultAddress, ethers.ZeroAddress, f.input.operation.receiver, f.input.operation.amount]);
        f.state.receipt = { status: '0x1', transactionHash: original.hash, blockNumber: '0x65', blockHash: ethers.id('canonical-receipt-block'), transactionIndex: '0x0', gasUsed: '0xc350', effectiveGasPrice: original.quoted.gasPrice, logs: [{ address: f.input.vaultAddress, ...event }] };
        return f.state.receipt;
      }
      return normalRPC(method, params);
    };
    const result = await transport.runCommand('replace-rejected-fee', f.input, f.dependencies);
    expect(result.transactionHash).to.equal(original.hash);
    expect((await transport.runCommand('execute', f.input, f.dependencies)).transactionHash).to.equal(original.hash);
  });
  it('inspects only requested existing token balances and uses an exact approval before token deposit', async () => {
    const token = '0x3333333333333333333333333333333333333333', f = fixture({ tokenAddresses: [token, token] });
    const inspected = await transport.runCommand('inspect', f.input, f.dependencies);
    expect(inspected.tokenBalances).to.deep.equal([{ token, balanceAtomic: '1000000', decimals: 6 }]);
    f.input.operation = { kind: 'depositToken', token, amount: '1000', receiver: 'fbc1qlqmychls2swfdxe3qzgc433vl2anue3eeg8fa2' };
    await expect(transport.runCommand('plan', f.input, f.dependencies)).to.be.rejectedWith('approved first');
    f.input.operation = { kind: 'approveToken', token, amount: '1000' };
    const approval = await transport.runCommand('execute', f.input, f.dependencies);
    expect(approval.approval.amount).to.equal('1000'); expect(approval.approval.limitedApproval).to.equal(true); expect(f.state.sent.to).to.equal(token);
    f.input.journalDir = path.join(f.dir, 'deposit-token'); f.state.receipt = null;
    f.input.operation = { kind: 'depositToken', token, amount: '1000', receiver: 'fbc1qlqmychls2swfdxe3qzgc433vl2anue3eeg8fa2' };
    const deposited = await transport.runCommand('execute', f.input, f.dependencies);
    expect(deposited.deposit.nativeContract).to.equal(token); expect(deposited.deposit.depositEventIndex).to.equal(1); expect(f.state.allowance).to.equal(0n); expect(f.state.sends).to.have.length(2);
  });
  it('rejects a token deposit with insufficient actual balance, a zero token, or excessive approval', async () => {
    const f = fixture();
    f.input.operation = { kind: 'depositToken', token: '0x3333333333333333333333333333333333333333', amount: '1000001', receiver: f.input.operation.receiver };
    await expect(transport.runCommand('plan', f.input, f.dependencies)).to.be.rejectedWith('balance is insufficient');
    f.input.operation.token = ethers.ZeroAddress;
    expect(() => transport.normalizeInput(f.input)).to.throw('registered token');
    f.input.operation = { kind: 'approveToken', token: '0x3333333333333333333333333333333333333333', amount: ethers.MaxUint256.toString() };
    expect(() => transport.normalizeInput(f.input)).to.throw('principal limit'); expect(f.state.sends).to.have.length(0);
  });
  it('checks mixed native/token payouts independently while broadcasting one batch and charging gas only once', async () => {
    const f = withdrawalFixture(), token = '0x3333333333333333333333333333333333333333';
    const items = [[ethers.id('native-item'), ethers.ZeroAddress, f.wallet.address, 1001n, 0], [ethers.id('token-item'), token, f.wallet.address, 999n, 1]];
    const batch = [ethers.id('DEX_VAULT_MIXED_WITHDRAW_BATCH_V1'), f.input.chainId, f.input.vaultAddress, 1, 1, [ethers.ZeroAddress, token], items];
    f.input.operation = { kind: 'withdraw', batchSeq: '1', calldata: transport.iface.encodeFunctionData('withdrawBatch', [batch, [1, 2, 3], [4, 5, 6]]), expectedItems: items.map(i => ({ requestId: i[0], token: i[1], to: i[2], grossAmount: i[3].toString() })) };
    const result = await transport.runCommand('execute', f.input, f.dependencies);
    expect(result.withdrawal.balanceDeltasVerified).to.equal(true); expect(result.withdrawal.itemCount).to.equal(2); expect(f.state.sends).to.have.length(1);
  });
  it('mutates only guardian scalar for a zero-cost eth_call and queries consumed for the post-payment replay control', async () => {
    const f = withdrawalFixture(); f.input.simulationMutation = 'guardianSignature';
    const rpc = f.dependencies.rpc;
    f.dependencies.rpc = async (method, params) => {
      if (method === 'eth_call' && transport.iface.parseTransaction(params[0])?.name === 'withdrawBatch') {
        const parsed = transport.iface.parseTransaction(params[0]); expect(parsed.args.guardianSignature.s).to.equal(5n);
        throw new Error('execution reverted: invalid signature');
      }
      return rpc(method, params);
    };
    await expect(transport.runCommand('simulate', f.input, f.dependencies)).to.be.rejectedWith('invalid signature'); expect(f.state.sends).to.have.length(0);
    f.dependencies.rpc = rpc; delete f.input.simulationMutation;
    await transport.runCommand('execute', f.input, f.dependencies); f.input.simulationCheckConsumed = true;
    expect(await transport.runCommand('simulate', f.input, f.dependencies)).to.deep.equal({ allConsumed: true, simulatedOnly: true }); expect(f.state.sends).to.have.length(1);
  });
  async function observedButUnverifiedFixture() {
    const f = withdrawalFixture(), rpc = f.dependencies.rpc;
    let failed = false;
    f.dependencies.rpc = async (method, params) => {
      if (method === 'eth_getBalance' && f.state.receipt && !failed) {
        failed = true;
        const location = path.join(f.input.journalDir, `receipt-location-${f.state.sent.hash.slice(2)}.json`);
        expect(fs.existsSync(location)).to.equal(true);                                              
        throw new Error('fixture validation RPC timeout after payment');
      }
      return rpc(method, params);
    };
    await expect(transport.runCommand('execute', f.input, f.dependencies)).to.be.rejectedWith('validation RPC timeout');
    expect(f.state.sends).to.have.length(1); expect(fs.existsSync(path.join(f.input.journalDir, 'result.json'))).to.equal(false);
    f.locationPath = path.join(f.input.journalDir, `receipt-location-${f.state.sent.hash.slice(2)}.json`);
    f.observed = structuredClone(f.state.receipt); f.baseRPC = rpc; f.fallbackCalls = [];
    f.dependencies.rpc = async (method, params) => {
      f.fallbackCalls.push([method, params]);
      if (method === 'eth_getTransactionReceipt') return null;
      if (method === 'eth_getBlockReceipts') { expect(params).to.deep.equal(['0x65']); return [f.observed]; }
      return rpc(method, params);
    };
    return f;
  }
  it('recovers an observed payment after validation failure and hash-index pruning using only its exact canonical block', async () => {
    const f = await observedButUnverifiedFixture(); f.state.nonce = 99;
    const rawBefore = fs.readFileSync(path.join(f.input.journalDir, 'prepared.json'), 'utf8');
    const result = await transport.runCommand('execute', f.input, f.dependencies);
    expect(result.withdrawal.allAttemptsSucceeded && result.withdrawal.allConsumedVerified && result.withdrawal.rewardDeltasVerified).to.equal(true);
    expect(result.transactionHash).to.equal(f.observed.transactionHash); expect(f.state.sends).to.have.length(1);
    expect(fs.readFileSync(path.join(f.input.journalDir, 'prepared.json'), 'utf8')).to.equal(rawBefore);
    expect(f.fallbackCalls.filter(([method]) => method === 'eth_getBlockReceipts')).to.have.length(1);
    expect(f.fallbackCalls.filter(([method]) => method === 'eth_getBlockByNumber')).to.have.length(2);
    expect(f.fallbackCalls.some(([method]) => /getLogs|trace|sendRaw/.test(method))).to.equal(false);
  });
  it('fsyncs location before the first canonical-header await and keeps replacement candidate hints separate', async () => {
    const first = fixture(), rpc = first.dependencies.rpc;
    first.dependencies.rpc = async (method, params) => {
      if (method === 'eth_getBlockByNumber' && first.state.receipt) {
        expect(fs.existsSync(path.join(first.input.journalDir, `receipt-location-${first.state.sent.hash.slice(2)}.json`))).to.equal(true);
        throw new Error('fixture first canonical header unavailable');
      }
      return rpc(method, params);
    };
    await expect(transport.runCommand('execute', first.input, first.dependencies)).to.be.rejectedWith('first canonical header unavailable'); expect(first.state.sends).to.have.length(1);
    const f = rejectionFixture(); await expect(transport.runCommand('execute', f.input, f.dependencies)).to.be.rejected;
    const original = JSON.parse(fs.readFileSync(path.join(f.input.journalDir, 'prepared.json'))); f.state.rejectFee = false; f.state.baseFee = 126000000n;
    const base = f.dependencies.rpc;
    f.dependencies.rpc = async (m, p) => { if (m === 'eth_getBalance' && f.state.receipt) throw new Error('fixture replacement validation unavailable'); return base(m, p); };
    await expect(transport.runCommand('replace-rejected-fee', f.input, f.dependencies)).to.be.rejectedWith('replacement validation unavailable');
    const replacement = JSON.parse(fs.readFileSync(path.join(f.input.journalDir, 'replacement.json')));
    expect(fs.existsSync(path.join(f.input.journalDir, `receipt-location-${replacement.hash.slice(2)}.json`))).to.equal(true);
    expect(fs.existsSync(path.join(f.input.journalDir, `receipt-location-${original.hash.slice(2)}.json`))).to.equal(false);
    f.dependencies.rpc = async (m, p) => m === 'eth_getTransactionReceipt' ? null : m === 'eth_getBlockReceipts' ? [f.state.receipt] : base(m, p);
    expect((await transport.runCommand('execute', f.input, f.dependencies)).transactionHash).to.equal(replacement.hash); expect(f.state.sends).to.have.length(1);
  });
  it('fails closed without rebroadcast for tampered hints, missing/ambiguous receipts, reorgs, stale headers and exact-query errors', async () => {
    const mutations = [
      async f => { const hint = JSON.parse(fs.readFileSync(f.locationPath)); hint.identity = 'ab'.repeat(32); fs.writeFileSync(f.locationPath, JSON.stringify(hint)); },
      async f => { const hint = JSON.parse(fs.readFileSync(f.locationPath)); hint.hash = ethers.id('another candidate'); fs.writeFileSync(f.locationPath, JSON.stringify(hint)); },
      async f => { const hint = JSON.parse(fs.readFileSync(f.locationPath)); hint.chainId = '1'; fs.writeFileSync(f.locationPath, JSON.stringify(hint)); },
      async f => { const rpc = f.dependencies.rpc; f.dependencies.rpc = (m, p) => m === 'eth_getBlockReceipts' ? Promise.resolve([]) : rpc(m, p); },
      async f => { const rpc = f.dependencies.rpc; f.dependencies.rpc = (m, p) => m === 'eth_getBlockReceipts' ? Promise.resolve([f.observed, f.observed]) : rpc(m, p); },
      async f => { f.state.blockHash = ethers.id('reorg'); },
      async f => { f.observed.blockNumber = '0x66'; },
      async f => { f.observed.blockHash = ethers.id('wrong block'); },
      async f => { f.observed.transactionIndex = '0x1'; },
      async f => { f.observed.logs[0].removed = true; },
      async f => { f.observed.status = '0x0'; },
      async f => { const rpc = f.dependencies.rpc; f.dependencies.rpc = (m, p) => { if (m === 'eth_getBlockReceipts') throw new Error('fixture exact-query timeout'); return rpc(m, p); }; },
      async f => { const rpc = f.dependencies.rpc; let headers = 0; f.dependencies.rpc = async (m, p) => { const value = await rpc(m, p); if (m === 'eth_getBlockByNumber' && ++headers === 2) return { ...value, hash: ethers.id('late reorg') }; return value; }; },
      async f => { const event = transport.iface.parseLog(f.observed.logs[0]), args = Array.from(event.args); args[3] += 1n; f.observed.logs[0] = { address: f.input.vaultAddress, ...transport.iface.encodeEventLog(transport.iface.getEvent('WithdrawalAttempted'), args) }; },
    ];
    for (const mutate of mutations) {
      const f = await observedButUnverifiedFixture(); await mutate(f);
      await expect(transport.runCommand('execute', f.input, f.dependencies)).to.be.rejected;
      expect(f.state.sends).to.have.length(1); expect(fs.existsSync(path.join(f.input.journalDir, 'result.json'))).to.equal(false);
    }
  });
  it('uses a legacy receipt only as a location hint and never assumes TRON supports block receipt lookup', async () => {
    const f = await observedButUnverifiedFixture(); fs.unlinkSync(f.locationPath);
    fs.writeFileSync(path.join(f.input.journalDir, 'receipt.json'), JSON.stringify(f.observed));
    await transport.runCommand('execute', f.input, f.dependencies);
    expect(fs.existsSync(f.locationPath)).to.equal(true); expect(f.state.sends).to.have.length(1);
    const tron = tronFixture(), rpc = tron.dependencies.rpc;
    tron.dependencies.rpc = async (m, p) => { if (m === 'eth_getBalance' && tron.state.receipt) throw new Error('fixture postreceipt failure'); return rpc(m, p); };
    await expect(transport.runCommand('execute', tron.input, tron.dependencies)).to.be.rejectedWith('postreceipt failure');
    tron.dependencies.rpc = async (m, p) => { expect(m).not.to.equal('eth_getBlockReceipts'); return m === 'eth_getTransactionReceipt' ? null : rpc(m, p); };
    await expect(transport.runCommand('execute', tron.input, tron.dependencies)).to.be.rejectedWith('TRON requires fresh JSON and native'); expect(tron.state.broadcasters).to.equal(1);
  });
  it('documents the remaining pre-observation crash window without scanning history or changing nonce', async () => {
    const f = fixture(); f.state.throwAfterSend = true;
    await expect(transport.runCommand('execute', f.input, f.dependencies)).to.be.rejectedWith('resume this journal');
    expect(fs.readdirSync(f.input.journalDir).some(name => name.startsWith('receipt-location-'))).to.equal(false);
    const rpc = f.dependencies.rpc; let replayedSavedRaw = false;
    f.dependencies.rpc = async (method, params) => {
      if (method === 'eth_getTransactionReceipt') return null;
      if (method === 'eth_sendRawTransaction') { replayedSavedRaw = true; expect(ethers.keccak256(params[0])).to.equal(f.state.sent.hash); throw new Error('nonce too low'); }
      expect(method).not.to.match(/getLogs|BlockReceipts/); return rpc(method, params);
    };
    await expect(transport.runCommand('execute', f.input, f.dependencies)).to.be.rejectedWith('nonce too low');
    expect(replayedSavedRaw).to.equal(true); expect(f.state.sends).to.have.length(1);
  });
  async function recoveryFixture() {
    const f = tronFixture({ keyFile: path.join(os.tmpdir(), 'fixture-key-must-not-be-read'), maxFeeAtomic: '23317000', maxEnergyPurchaseSun: '23317000', maxFeeLimitSun: '1000000000', timeoutMs: 30000 });
    const { TronWeb } = require('tronweb');
    f.input.journalDir = path.join(f.dir, 'external', 'trx-withdraw-1'); f.state.energy = 0; f.state.balance = 14960000; f.state.rentalSends = 0;
    const items = [0, 1].map(i => [ethers.id('recovery-' + i), ethers.ZeroAddress, f.wallet.address, 400000, 0]);
    const batch = [ethers.id('DEX_VAULT_MIXED_WITHDRAW_BATCH_V1'), f.input.chainId, f.input.vaultAddress, 1, 1, [ethers.ZeroAddress], items];
    f.input.operation = { kind: 'withdraw', batchSeq: '1', calldata: transport.iface.encodeFunctionData('withdrawBatch', [batch, [1, 2, 3], [4, 5, 6]]), expectedItems: items.map(i => ({ requestId: i[0], token: i[1], to: i[2], grossAmount: String(i[3]) })) };
    const paid = new Map(), originalNative = f.dependencies.native, originalRPC = f.dependencies.rpc;
    f.dependencies.rpc = (m, p) => m === 'eth_getBalance' ? Promise.resolve(ethers.toQuantity(f.state.balance)) : originalRPC(m, p);
    f.dependencies.native = async (route, body) => {
      if (route === 'wallet/estimateenergy') { expect(body.call_value).to.equal(0); return { result: { result: true }, energy_required: 177099 }; }
      if (route === 'wallet/getaccountresource') return { EnergyLimit: f.state.energy };
      if (route === 'wallet/getaccount') return { balance: f.state.balance };
      if (route === 'wallet/gettransactionbyid') return paid.has(body.value) ? { ...paid.get(body.value), ret: [{ contractRet: 'SUCCESS' }] } : {};
      if (route === 'wallet/gettransactioninfobyid' && paid.has(body.value)) return { id: body.value, blockNumber: 123450, fee: 268000, receipt: {} };
      if (route === 'wallet/broadcasttransaction') {
        f.state.broadcasters++; const logs = []; let net = 0n, reward = 0n;
        for (const item of items) { const gross = BigInt(item[3]), fee = (gross + 9999n) / 10000n; net += gross - fee; reward += fee; logs.push({ address: f.input.vaultAddress, ...transport.iface.encodeEventLog(transport.iface.getEvent('WithdrawalAttempted'), [item[0], item[2], item[1], gross, fee, gross - fee, true]) }); }
        const parsed = transport.validateWithdrawal(transport.normalizeInput(f.input)).batch, digest = ethers.keccak256(ethers.AbiCoder.defaultAbiCoder().encode([transport.BATCH], [parsed]));
        logs.push({ address: f.input.vaultAddress, ...transport.iface.encodeEventLog(transport.iface.getEvent('WithdrawalBatchExecuted'), [digest, 1, 1, f.wallet.address, 2, 1]) });
        f.state.rewards = { [ethers.ZeroAddress.toLowerCase()]: reward }; f.state.sequence = 2n; f.state.balance += Number(net) - 300000;
        f.state.receipt = { status: '0x1', transactionHash: '0x' + body.txID, blockNumber: ethers.toQuantity(123457), blockHash: '0x' + (123457).toString(16).padStart(16, '0') + 'b'.repeat(48), transactionIndex: '0x0', logs };
        f.state.nativeReceipt = { id: body.txID, blockNumber: 123457, fee: 300000, receipt: { result: 'SUCCESS', energy_fee: 0 } }; return { result: true, txid: body.txID };
      }
      return originalNative(route, body);
    };
    f.dependencies.tronWeb.trx.sendRawTransaction = async transaction => {
      if (paid.has(transaction.txID)) return { result: false, code: 'DUP_TRANSACTION_ERROR', txid: transaction.txID };
      paid.set(transaction.txID, structuredClone(transaction)); f.state.rentalSends++; f.state.balance -= 6268000; f.state.energy += 130000; f.state.now += 3000;
      if (f.state.loseRentalResponse) throw new Error('fixture lost rental response');
      return { result: true, txid: transaction.txID };
    };
    const old = structuredClone(f.input), oldPlan = await transport.runCommand('plan', old, f.dependencies);
    fs.mkdirSync(path.join(old.journalDir, 'energy'), { recursive: true }); transport.durable(path.join(old.journalDir, 'plan.json'), oldPlan);
    transport.durable(path.join(old.journalDir, 'energy', 'plan.json'), { version: 1, plan: oldPlan.energyPlan, startedAtMs: f.state.now - 7200000 });
    for (let i = 0; i < 2; i++) {
      const timestamp = f.state.now - 7200000 + i * 3000;
      const unsigned = await f.dependencies.tronWeb.transactionBuilder.sendTrx(oldPlan.energyPlan.recipient, 6000000, oldPlan.energyPlan.deployer, { blockHeader: { ref_block_bytes: '1234', ref_block_hash: '0123456789abcdef', timestamp, expiration: timestamp + 57000 } });
      const transaction = await f.dependencies.tronWeb.trx.sign(unsigned, '1'.padStart(64, '0')); paid.set(transaction.txID, structuredClone(transaction));
      const stem = path.join(old.journalDir, 'energy', String(i + 1).padStart(3, '0'));
      transport.durable(stem + '.signed.json', { version: 1, planId: oldPlan.energyPlan.planId, index: i, beforeEnergy: String(i * 131008), txid: transaction.txID, transaction });
      for (const suffix of ['broadcast', 'confirmed']) transport.durable(stem + '.' + suffix + '.json', { version: 1, planId: oldPlan.energyPlan.planId, txid: transaction.txID });
    }
    const before = path.join(f.dir, 'revisions', 'before'); fs.mkdirSync(before, { recursive: true }); const parentFile = path.join(before, 'trx-withdraw-1.input.json'); transport.durable(parentFile, old);
    const sha = file => createHash('sha256').update(fs.readFileSync(file)).digest('hex');
    f.input = { ...old, journalDir: path.join(f.dir, 'external', 'trx-withdraw-1-g002'), maxFeeAtomic: '27500000', maxEnergyPurchaseSun: '24000000', energyRecovery: { version: 1, generation: 2, parentInputFile: parentFile, parentInputSha256: sha(parentFile), parentJournalDir: old.journalDir, parentPlanSha256: sha(path.join(old.journalDir, 'plan.json')), parentEnergyPlanSha256: sha(path.join(old.journalDir, 'energy', 'plan.json')), parentIdentity: oldPlan.identity, expectedPaidTxids: [...paid.keys()].sort(), expectedCarryoverFeeAtomic: '12536000' } };
    const all = dir => fs.readdirSync(dir, { withFileTypes: true }).flatMap(e => e.isDirectory() ? all(path.join(dir, e.name)) : [path.join(dir, e.name)]);
    f.parentHashes = Object.fromEntries(all(old.journalDir).map(file => [file, sha(file)])); f.sha = sha; f.old = old; f.paid = paid;
    return f;
  }
  it('carries verified expired parent expenses into a distinct generation-2 quote and final result without reading a key for reconciliation', async () => {
    const f = await recoveryFixture();
    const reconciliation = await transport.runCommand('reconcile-energy', f.input, { native: f.dependencies.native, now: f.dependencies.now });
    expect(reconciliation.actualFeeAtomic).to.equal('12536000'); expect(reconciliation.expenses).to.have.length(2);
    const plan = await transport.runCommand('plan', f.input, f.dependencies);
    expect(plan.carryoverFeeAtomic).to.equal('12536000'); expect(plan.incrementalReservedFeeAtomic).to.equal('14917000'); expect(plan.reservedFeeAtomic).to.equal('27453000');
    expect(plan.energyPlan.maxPurchaseSun).to.equal('12000000'); expect(plan.energyPlan.purchaseUnits).to.equal(2); expect(f.state.rentalSends).to.equal(0);
    const result = await transport.runCommand('execute', f.input, f.dependencies);
    expect(result.actualFeeAtomic).to.equal('25372000'); expect(result.incrementalActualFeeAtomic).to.equal('12836000'); expect(result.carryoverHash).to.equal(plan.carryoverHash);
    expect(result.rentalExpenses).to.have.length(4); expect(f.state.rentalSends).to.equal(2); expect(f.state.broadcasters).to.equal(1);
    expect(result.withdrawal.allConsumedVerified && result.withdrawal.rewardDeltasVerified).to.equal(true);
    await transport.runCommand('execute', f.input, f.dependencies); expect(f.state.rentalSends).to.equal(2); expect(f.state.broadcasters).to.equal(1);
    for (const [file, hash] of Object.entries(f.parentHashes)) expect(f.sha(file)).to.equal(hash);
  });
  it('rejects forged carryover, duplicate/missing parent payments, changed calldata or parent raw before new signing or spending', async () => {
    const changes = [
      f => { f.input.energyRecovery.expectedCarryoverFeeAtomic = '1'; },
      f => { f.input.energyRecovery.expectedPaidTxids = [f.input.energyRecovery.expectedPaidTxids[0], f.input.energyRecovery.expectedPaidTxids[0]]; },
      f => { f.input.energyRecovery.expectedPaidTxids.pop(); },
      f => { transport.durable(path.join(f.old.journalDir, 'prepared.json'), {}); },
      f => { f.input.energyRecovery.parentIdentity = 'ab'.repeat(32); },
      f => { f.input.operation.expectedItems[0].grossAmount = '1002'; },
      f => { f.paid.clear(); },
      f => { f.input.energyRecovery.parentInputSha256 = 'ab'.repeat(32); },
      f => { f.input.journalDir = path.join(f.dir, 'external', 'trx-withdraw-1-g003'); },
      f => { f.input.energyRecovery.parentInputFile = path.join(f.dir, '..', 'outside-parent.json'); },
      f => { fs.unlinkSync(path.join(f.old.journalDir, 'energy', '002.confirmed.json')); },
      f => { const rpc = f.dependencies.native; f.dependencies.native = async (route, body) => { const value = await rpc(route, body); return route === 'wallet/gettransactioninfobyid' && f.paid.has(body.value) ? { ...value, fee: 0 } : value; }; },
    ];
    for (const change of changes) { const f = await recoveryFixture(); let signs = 0; const sign = f.dependencies.tronWeb.trx.sign.bind(f.dependencies.tronWeb.trx); f.dependencies.tronWeb.trx.sign = (...args) => { signs++; return sign(...args); }; change(f); await expect(transport.runCommand('prepare', f.input, f.dependencies)).to.be.rejected; expect(signs).to.equal(0); expect(f.state.rentalSends).to.equal(0); expect(f.state.broadcasters).to.equal(0); expect(fs.existsSync(path.join(f.input.journalDir, 'prepared.json'))).to.equal(false); }
  });
  it('reports partial generation-2 paid fees and resumes an unknown broadcast by the same bytes without reusing expired parent energy', async () => {
    const f = await recoveryFixture(); f.state.loseRentalResponse = true;
    await expect(transport.runCommand('execute', f.input, f.dependencies)).to.be.rejectedWith('broadcast result is unknown');
    const paid = await transport.runCommand('reconcile-energy', f.input, { native: f.dependencies.native, now: f.dependencies.now });
    expect(paid.actualFeeAtomic).to.equal('18804000'); expect(paid.incrementalFeeAtomic).to.equal('6268000'); expect(paid.expenses).to.have.length(3);
    expect(f.state.rentalSends).to.equal(1); expect(f.state.broadcasters).to.equal(0);
    const signedPath = path.join(f.input.journalDir, 'energy', '001.signed.json'), before = fs.readFileSync(signedPath);
    f.state.loseRentalResponse = false; await transport.runCommand('execute', f.input, f.dependencies);
    expect(fs.readFileSync(signedPath).equals(before)).to.equal(true); expect(f.state.rentalSends).to.equal(2); expect(f.state.broadcasters).to.equal(1);
    const changed = structuredClone(f.input); delete changed.energyRecovery;
    await expect(transport.runCommand('execute', changed, f.dependencies)).to.be.rejectedWith('requires explicit recovery');
  });
  it('binds prepared recovery metadata and handles post-ready stale resources within the original deadline without new payments', async () => {
    const f = await recoveryFixture(), rpc = f.dependencies.native; let stale = false;
    f.dependencies.native = async (route, body) => {
      const value = await rpc(route, body);
      if (route === 'wallet/getaccountresource' && !stale && fs.existsSync(path.join(f.input.journalDir, 'energy', 'delivery.ready.public.json'))) { stale = true; return { EnergyLimit: 131008 }; }
      return value;
    };
    await transport.runCommand('prepare', f.input, f.dependencies); expect(stale).to.equal(true); expect(f.state.rentalSends).to.equal(2); expect(f.state.broadcasters).to.equal(0);
    const alias = path.join(f.dir, 'revisions', 'before', 'immutable-parent-alias.json'); fs.copyFileSync(f.input.energyRecovery.parentInputFile, alias);
    f.input.energyRecovery.parentInputFile = alias;
    await expect(transport.runCommand('execute', f.input, f.dependencies)).to.be.rejectedWith('carryover lineage changed'); expect(f.state.rentalSends).to.equal(2); expect(f.state.broadcasters).to.equal(0);
    const blocked = await recoveryFixture(), original = blocked.dependencies.native;
    blocked.dependencies.native = async (route, body) => {
      const value = await original(route, body);
      if (route === 'wallet/getaccountresource' && fs.existsSync(path.join(blocked.input.journalDir, 'energy', 'delivery.ready.public.json'))) return { EnergyLimit: 131008 };
      return value;
    };
    await expect(transport.runCommand('prepare', blocked.input, blocked.dependencies)).to.be.rejectedWith('original deadline');
    expect(fs.existsSync(path.join(blocked.input.journalDir, 'prepared.json'))).to.equal(false); expect(blocked.state.rentalSends).to.equal(2); expect(blocked.state.broadcasters).to.equal(0);
    const paid = await transport.runCommand('reconcile-energy', blocked.input, { native: blocked.dependencies.native, now: blocked.dependencies.now }); expect(paid.actualFeeAtomic).to.equal('25072000');
    await expect(transport.runCommand('prepare', blocked.input, blocked.dependencies)).to.be.rejected;
    expect(blocked.state.rentalSends).to.equal(2); expect(blocked.state.broadcasters).to.equal(0);
  });
  it('handles Windows parent path casing without escaping or looping outside the run', async () => {
    if (process.platform !== 'win32') return;
    const f = await recoveryFixture(); f.input.energyRecovery.parentInputFile = f.input.energyRecovery.parentInputFile.toLowerCase(); f.input.energyRecovery.parentJournalDir = f.input.energyRecovery.parentJournalDir.toLowerCase();
    expect((await transport.runCommand('reconcile-energy', f.input, { native: f.dependencies.native, now: f.dependencies.now })).actualFeeAtomic).to.equal('12536000'); expect(f.state.rentalSends).to.equal(0);
  });
  it('rejects excess cash before new rental signing and rechecks cash, Energy and fees before broadcasting a saved raw', async () => {
    const tooRich = await recoveryFixture(); tooRich.state.balance = 50000000;
    let signs = 0; const sign = tooRich.dependencies.tronWeb.trx.sign.bind(tooRich.dependencies.tronWeb.trx); tooRich.dependencies.tronWeb.trx.sign = (...args) => { signs++; return sign(...args); };
    await expect(transport.runCommand('prepare', tooRich.input, tooRich.dependencies)).to.be.rejectedWith('cash exceeds unspent');
    expect(signs).to.equal(0); expect(tooRich.state.rentalSends).to.equal(0); expect(tooRich.state.broadcasters).to.equal(0);
    const f = await recoveryFixture(); await transport.runCommand('prepare', f.input, f.dependencies);
    const file = path.join(f.input.journalDir, 'prepared.json'), raw = fs.readFileSync(file);
    f.state.balance += 1000000;
    await expect(transport.runCommand('execute', f.input, f.dependencies)).to.be.rejectedWith('cash exceeds unspent'); expect(f.state.broadcasters).to.equal(0);
    f.state.balance -= 1000000; f.state.energy = 131008;
    await expect(transport.runCommand('execute', f.input, f.dependencies)).to.be.rejectedWith('fresh TRON delegated Energy'); expect(f.state.broadcasters).to.equal(0);
    f.state.energy = 260000; const native = f.dependencies.native;
    f.dependencies.native = async (route, body) => { const value = await native(route, body); if (route === 'wallet/getchainparameters') return { chainParameter: value.chainParameter.map(item => item.key === 'getEnergyFee' ? { ...item, value: 200 } : item) }; return value; };
    await expect(transport.runCommand('execute', f.input, f.dependencies)).to.be.rejectedWith('fresh TRON fee parameters'); expect(f.state.broadcasters).to.equal(0);
    f.dependencies.native = native;
    const result = await transport.runCommand('execute', f.input, f.dependencies);
    expect(f.state.broadcasters).to.equal(1); expect(f.state.rentalSends).to.equal(2); expect(fs.readFileSync(file).equals(raw)).to.equal(true);
    expect(f.state.balance).to.be.greaterThan(27500000 - 12536000 - 12536000);                                         
    f.state.balance = 50000000; expect((await transport.runCommand('execute', f.input, f.dependencies)).actualFeeAtomic).to.equal(result.actualFeeAtomic); expect(f.state.broadcasters).to.equal(1);
  });
  it('reuses the saved purchase plan after paid slots when the first resumed resource sample is stale', async () => {
    const f = await recoveryFixture(), native = f.dependencies.native; let stopped = false;
    f.dependencies.native = async (route, body) => { if (!stopped && route === 'wallet/getnowblock' && fs.existsSync(path.join(f.input.journalDir, 'energy', 'delivery.ready.public.json'))) { stopped = true; throw new Error('fixture stopped after rentals before withdrawal'); } return native(route, body); };
    await expect(transport.runCommand('prepare', f.input, f.dependencies)).to.be.rejectedWith('stopped after rentals'); expect(f.state.rentalSends).to.equal(2); expect(f.state.broadcasters).to.equal(0);
    let low = false;
    f.dependencies.native = async (route, body) => { const value = await native(route, body); if (!low && route === 'wallet/getaccountresource') { low = true; return { EnergyLimit: 131008 }; } return value; };
    const plan = await transport.runCommand('plan', f.input, f.dependencies);
    expect(plan.resumedApprovedPlan).to.equal(true); expect(plan.knownIncrementalFeeAtomic).to.equal('12536000'); expect(plan.futureReservedFeeAtomic).to.equal('2381000');
    expect(f.state.balance).to.equal(2424000);                                                                            
    await transport.runCommand('execute', f.input, f.dependencies); expect(low).to.equal(true); expect(f.state.rentalSends).to.equal(2); expect(f.state.broadcasters).to.equal(1);
  });
});
