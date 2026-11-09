const { expect } = require('chai');
const fs = require('fs');
const os = require('os');
const path = require('path');
const hre = require('hardhat');
const { Wallet, ethers } = require('ethers');
const tools = require('../scripts/vault_deployment');
const {G,guardianEnvironment}=require('./helpers/guardian-deployment');
const deployment = require('../scripts/deploy_schnorr_vault');

describe('Unified Vault deployment identity and recovery', function () {
  let directory, artifact, tronArtifact;
  const env = extra => ({ VAULT_CHAIN_ID: '31337', VAULT_DEX_CHAIN_ID: '51025', VAULT_ID: '0', VAULT_EPOCH: '1', VAULT_PX: '1', VAULT_PY: '2', VAULT_OUTPUT: path.join(directory, 'vault.json'), ...guardianEnvironment(directory), ...extra });
  const options = extra => deployment.parseOptions(env(extra), 'hardhat');
  before(async function () {
    artifact = await tools.loadVaultArtifact(hre, 'SchnorrExampleV3');
    tronArtifact = await tools.loadVaultArtifact(hre, 'SchnorrExampleTronV3');
  });
  beforeEach(function () { directory = fs.mkdtempSync(path.join(os.tmpdir(), 'dex-unified-vault-')); });
  afterEach(function () { fs.rmSync(directory, { recursive: true, force: true }); });

                                                                               
                                                                                
  async function localRPC(method, params) {
    if (method !== 'eth_getBlockReceipts') return hre.ethers.provider.send(method, params);
    const block = await hre.ethers.provider.send('eth_getBlockByNumber', [params[0], false]);
    return Promise.all(block.transactions.map(hash => hre.ethers.provider.send('eth_getTransactionReceipt', [hash])));
  }

  it('requires the independent explicit DEX domain and validates public parameters', function () {
    expect(options().dexChainId).to.equal(51025n);
    for (const value of [undefined, '0', '-1', '01', '1e3', String(1n << 64n)]) expect(() => options({ VAULT_DEX_CHAIN_ID: value })).to.throw('VAULT_DEX_CHAIN_ID');
    expect(() => options({ VAULT_CHAIN_ID: '1' })).to.throw('Hardhat network');
    expect(() => options({ VAULT_PX: '0', VAULT_PY: '0' })).to.throw('BN128');
    expect(tools.resolveVaultOutput({}, 'ethereum', 1n, 51025n, 0n)).to.include(path.join('vault-deployments', 'ethereum-1-51025-0.json'));
    expect(options({ VAULT_DEPLOYER_ADDRESS: '0x1111111111111111111111111111111111111111' }).expectedDeployer).to.equal('0x1111111111111111111111111111111111111111');
    for (const address of ['', ethers.ZeroAddress, 'not-an-address']) expect(() => options({ VAULT_DEPLOYER_ADDRESS: address })).to.throw('VAULT_DEPLOYER_ADDRESS');
  });

  it('patches exactly every named immutable including inherited DEX domain', function () {
    expect(Object.values(artifact.immutableNames)).to.deep.equal(['dexChainId']);
    expect(Object.values(tronArtifact.immutableNames).sort()).to.deep.equal(['dexChainId', 'nonstandardToken']);
    const token = '0x1111111111111111111111111111111111111111';
    expect(tools.expectedVaultRuntime(artifact, 51025n)).not.to.equal(tools.expectedVaultRuntime(artifact, 51026n));
    expect(tools.expectedVaultRuntime(tronArtifact, 51025n, token)).not.to.equal(tools.expectedVaultRuntime(tronArtifact, 51025n));
    for (const broken of [
      { ...artifact, immutableNames: {} },
      { ...artifact, immutableReferences: {} },
      { ...artifact, immutableReferences: { [Object.keys(artifact.immutableNames)[0]]: [{ start: 0, length: 32 }] } },
    ]) expect(() => tools.expectedVaultRuntime(broken, 51025n)).to.throw('immutable');
  });

  it('requires the batch-only withdrawal ABI in every future deployment artifact', function () {
    expect(tools.WITHDRAW_PROTOCOL).to.equal('DEX_VAULT_MIXED_WITHDRAW_BATCH_V1');
    for (const candidate of [artifact, tronArtifact]) {
      expect(tools.validateVaultABI(candidate.abi)).to.equal(true);
      const functions = candidate.abi.filter(item => item.type === 'function').map(item => item.name);
      expect(functions).to.include.members(['withdrawBatch', 'withdrawBatchWithHandovers', 'claimReward']);
      expect(functions).not.to.include.members(['withdraw', 'withdrawWithHandover', 'changePub']);
    }
    expect(() => tools.validateVaultABI(artifact.abi.filter(item => item.name !== 'withdrawBatch')))
      .to.throw('missing withdrawBatch');
    expect(() => tools.validateVaultABI([...artifact.abi, 'function changePub(uint256,uint256,uint256,uint256,uint256,uint256)']))
      .to.throw('still exposes removed changePub');
  });

  async function local(optionsOverride = {}, providerOverride = {}, config = options()) {
    const signer = Wallet.createRandom().connect(hre.ethers.provider);
    await hre.ethers.provider.send('hardhat_setBalance', [signer.address, '0x1000000000000000000']);
    let broadcasts = 0;
    const provider = {
      getNetwork: () => hre.ethers.provider.getNetwork(),
      send: localRPC,
      broadcastTransaction: async raw => {
        const saved = JSON.parse(fs.readFileSync(config.output, 'utf8'));
        expect(saved.status).to.equal('pending');
        expect(saved.txHash).to.equal(ethers.keccak256(raw));
        expect(Number.isSafeInteger(saved.searchFromBlock)).to.equal(true);
        expect(saved.searchFromBlock).to.be.greaterThan(0);
        broadcasts++;
        return hre.ethers.provider.broadcastTransaction(raw);
      },
      getTransactionReceipt: hash => hre.ethers.provider.getTransactionReceipt(hash),
      getBlockNumber: () => hre.ethers.provider.getBlockNumber(),
      getTransaction: hash => hre.ethers.provider.getTransaction(hash),
      getCode: address => hre.ethers.provider.getCode(address),
      getBlock: number => hre.ethers.provider.getBlock(number),
      call: request => hre.ethers.provider.call(request),
      ...providerOverride,
    };
    const result = await deployment.run({ ...config, ...optionsOverride }, { artifact, provider, signer });
    return { config, provider, result, broadcasts: () => broadcasts };
  }

  it('deploys one real local Vault with the exact immutable code and preserves withdrawal authority', async function () {
    const actual = await local();
    expect(actual.broadcasts()).to.equal(1);
    expect(actual.result.depositProtocol).to.equal('DEX_VAULT_DEPOSIT_V1');
    expect(actual.result.protocol).to.equal('DEX_VAULT_MIXED_WITHDRAW_BATCH_V1');
    expect(actual.result.dexChainId).to.equal('51025');
    expect(await hre.ethers.provider.getCode(actual.result.address)).to.equal(tools.expectedVaultRuntime(artifact, 51025n));
    const noSecret = { ...actual.config };
    Object.defineProperty(noSecret, 'privateKey', { enumerable: true, get() { throw new Error('recovery read a secret'); } });
    const recovered = await deployment.run(noSecret, { artifact, provider: actual.provider });
    expect(recovered.address).to.equal(actual.result.address);
    expect(actual.broadcasts()).to.equal(1);
  });

  it('keeps pending after an uncertain broadcast and does not send again on retry', async function () {
    const config = options();
    const signer = Wallet.createRandom().connect(hre.ethers.provider);
    await hre.ethers.provider.send('hardhat_setBalance', [signer.address, '0x1000000000000000000']);
    let broadcasts = 0;
    const provider = {
      getNetwork: () => hre.ethers.provider.getNetwork(),
      getBlockNumber: () => hre.ethers.provider.getBlockNumber(),
      broadcastTransaction: async raw => { broadcasts++; await hre.ethers.provider.broadcastTransaction(raw); throw new Error('connection lost after broadcast'); },
    };
    await expect(deployment.run(config, { artifact, signer, provider })).to.be.rejectedWith('connection lost');
    const saved = JSON.parse(fs.readFileSync(config.output, 'utf8'));
    expect(saved.status).to.equal('pending');
    expect(saved.searchFromBlock).to.be.greaterThan(0);
    const pruned = new Proxy(hre.ethers.provider, { get(target, property) {
      if (property === 'getTransactionReceipt' || property === 'getTransaction') return async () => null;
      if (property === 'send') return localRPC;
      const value = target[property]; return typeof value === 'function' ? value.bind(target) : value;
    } });
    const recovered = await deployment.run(config, { artifact, provider: pruned });
    expect(recovered.txHash).to.equal(saved.txHash);
    expect(recovered.status).to.equal('verified');
    expect(broadcasts).to.equal(1);
  });

  it('rejects wrong networks before signing or reserving a journal', async function () {
    await expect(local({}, { getNetwork: async () => ({ chainId: 1n }) })).to.be.rejectedWith('RPC chain ID');
    expect(fs.existsSync(options().output)).to.equal(false);
  });

  it('rejects conflicting identity, recovery hash and changed creation evidence', async function () {
    const actual = await local();
    await expect(deployment.run({ ...actual.config, dexChainId: 9n }, { artifact, provider: actual.provider })).to.be.rejectedWith('conflicts with dexChainId');
    await expect(deployment.run({ ...actual.config, recoverTxHash: `0x${'aa'.repeat(32)}` }, { artifact, provider: actual.provider })).to.be.rejectedWith('recovery transaction differs');
    for (const override of [
      { getTransaction: async hash => ({ ...await hre.ethers.provider.getTransaction(hash), data: '0x60ff' }) },
      { getTransaction: async hash => ({ ...await hre.ethers.provider.getTransaction(hash), blockHash: `0x${'bb'.repeat(32)}` }) },
      { getCode: async () => tools.expectedVaultRuntime(artifact, 9n) },
      { getBlock: async n => ({ ...await hre.ethers.provider.getBlock(n), hash: `0x${'bb'.repeat(32)}` }) },
    ]) await expect(deployment.run(actual.config, { artifact, provider: { ...actual.provider, ...override } })).to.be.rejected;
    expect(actual.broadcasts()).to.equal(1);
  });

  it('refuses legacy receipts before any deployment and never overwrites them', async function () {
    const legacy = JSON.stringify({ protocol: 'DEX_VAULT_WITHDRAW_V1', txHash: 'old-real-transaction' });
    fs.writeFileSync(options().output, legacy);
    await expect(deployment.run(options(), { artifact, deploy: async () => { throw new Error('must not deploy'); } })).to.be.rejectedWith('journal status');
    expect(fs.readFileSync(options().output, 'utf8')).to.equal(legacy);
  });

  it('rejects a structurally valid old-protocol journal without rewriting its evidence', function () {
    const identity = tools.vaultIdentity(options(), artifact);
    const old = {
      ...identity,
      protocol: 'DEX_VAULT_WITHDRAW_V1',
      status: 'pending',
      txHash: `0x${'aa'.repeat(32)}`,
      address: '0x1111111111111111111111111111111111111111',
      deployer: '0x2222222222222222222222222222222222222222',
    };
    const serialized = JSON.stringify(old, null, 2) + '\n';
    fs.writeFileSync(options().output, serialized);
    expect(() => tools.loadVaultJournal(options().output, identity)).to.throw('conflicts with protocol');
    expect(fs.readFileSync(options().output, 'utf8')).to.equal(serialized);
  });

  it('requires explicit new-mainnet intent and uses exclusive pending reservation', async function () {
    await expect(deployment.run({ ...options(), chainId: 1n }, { artifact, deploy: async () => { throw new Error('must not deploy'); } })).to.be.rejectedWith('VAULT_ALLOW_MAINNET_DEPLOY');
    const identity = tools.vaultIdentity(options(), artifact);
    const pending = { txHash: `0x${'aa'.repeat(32)}`, address: '0x1111111111111111111111111111111111111111', deployer: '0x2222222222222222222222222222222222222222' };
    tools.writeVaultPending(options().output, identity, pending);
    expect(() => tools.writeVaultPending(options().output, identity, { ...pending, txHash: `0x${'bb'.repeat(32)}` })).to.throw();
    expect(tools.loadVaultJournal(options().output, identity).txHash).to.equal(pending.txHash);
  });

  it('recovers a verified deployment from real block receipts and full transactions when both hash indexes are absent', async function () {
    const actual = await local();
    const calls = [];
    const pruned = { ...actual.provider, getTransactionReceipt: async () => null, getTransaction: async () => null,
      send: async (method, params) => { calls.push([method, params]); return localRPC(method, params); } };
    const recovered = await deployment.run(actual.config, { artifact, provider: pruned });
    expect(recovered.address).to.equal(actual.result.address);
    expect(recovered).not.to.have.property('searchFromBlock');
    expect(calls).to.deep.equal([
      ['eth_getBlockByNumber', [ethers.toQuantity(recovered.blockNumber), false]],
      ['eth_getBlockReceipts', [ethers.toQuantity(recovered.blockNumber)]],
      ['eth_getBlockByNumber', [ethers.toQuantity(recovered.blockNumber), true]],
    ]);
    expect(actual.broadcasts()).to.equal(1);
  });

  it('saves the public scan start before broadcasting and finds a new deployment without either hash index', async function () {
    const actual = await local({}, { getTransactionReceipt: async () => null, getTransaction: async () => null });
    expect(actual.result.status).to.equal('verified');
    expect(actual.result).not.to.have.property('searchFromBlock');
    expect(actual.broadcasts()).to.equal(1);
  });

  it('uses an explicit recovery block without a journal or payer key', async function () {
    const actual = await local();
    const config = { ...actual.config, output: path.join(directory, 'recovered.json'), recoverTxHash: actual.result.txHash, recoverBlockNumber: actual.result.blockNumber };
    Object.defineProperty(config, 'privateKey', { enumerable: true, get() { throw new Error('recovery read a key'); } });
    const recovered = await deployment.run(config, { artifact, provider: { ...actual.provider, getTransactionReceipt: async () => null, getTransaction: async () => null } });
    expect(recovered.txHash).to.equal(actual.result.txHash);
    expect(actual.broadcasts()).to.equal(1);
    await expect(deployment.run({ ...actual.config, recoverBlockNumber: actual.result.blockNumber + 1 }, { artifact, provider: actual.provider })).to.be.rejectedWith('differs from verified journal');
    const wrongHeight = { ...actual.config, recoverTxHash: actual.result.txHash, output: path.join(directory, 'wrong-height.json'), recoverBlockNumber: actual.result.blockNumber + 1 };
    await hre.ethers.provider.send('evm_mine', []);
    await expect(deployment.run(wrongHeight, { artifact, provider: { ...actual.provider, getTransactionReceipt: async () => null } })).to.be.rejectedWith('exactly one matching receipt');
    expect(fs.existsSync(wrongHeight.output)).to.equal(false);
  });

  it('validates recovery block hints and rejects stale journal hints before writing', async function () {
    for (const value of ['', '0', '-1', '01', '1e3', '9007199254740993']) expect(() => options({ VAULT_RECOVER_BLOCK_NUMBER: value })).to.throw('VAULT_RECOVER_BLOCK_NUMBER');
    expect(options({ VAULT_RECOVER_BLOCK_NUMBER: '123' }).recoverBlockNumber).to.equal(123);
    const config = options(), identity = tools.vaultIdentity(config, artifact);
    const pending = { txHash: `0x${'aa'.repeat(32)}`, address: '0x1111111111111111111111111111111111111111', deployer: '0x2222222222222222222222222222222222222222', searchFromBlock: 0 };
    expect(() => tools.writeVaultPending(config.output, identity, pending)).to.throw('searchFromBlock');
    expect(fs.existsSync(config.output)).to.equal(false);
    fs.writeFileSync(config.output, JSON.stringify({ ...identity, ...pending, status: 'pending' }));
    expect(() => tools.loadVaultJournal(config.output, identity)).to.throw('searchFromBlock');
  });

  it('rejects substituted or duplicate fallback evidence and keeps the verified journal unchanged', async function () {
    const actual = await local();
    const original = fs.readFileSync(actual.config.output, 'utf8');
    const fakeHash = `0x${'bb'.repeat(32)}`;
    const mutations = [
      ['eth_getBlockReceipts', rows => rows.map(row => ({ ...row, blockNumber: ethers.toQuantity(actual.result.blockNumber + 1) }))],
      ['eth_getBlockReceipts', rows => rows.map(row => ({ ...row, blockHash: fakeHash }))],
      ['eth_getBlockReceipts', rows => rows.map(row => ({ ...row, transactionHash: fakeHash }))],
      ['eth_getBlockReceipts', rows => [...rows, ...rows]],
      ['eth_getBlockReceipts', rows => rows.map(row => ({ ...row, status: '0x0' }))],
      ['eth_getBlockByNumber', block => ({ ...block, hash: fakeHash })],
      ['eth_getBlockByNumber', block => ({ ...block, number: ethers.toQuantity(actual.result.blockNumber + 1) })],
      ['eth_getBlockByNumber', block => ({ ...block, transactions: [...block.transactions, ...block.transactions] }), true],
      ['eth_getBlockByNumber', block => ({ ...block, transactions: block.transactions.map(tx => ({ ...tx, hash: fakeHash })) }), true],
      ...['input', 'from', 'to', 'value', 'chainId', 'nonce', 'blockHash', 'blockNumber'].map(field => [
        'eth_getBlockByNumber', block => ({ ...block, transactions: block.transactions.map(tx => ({ ...tx, [field]: ({ input: '0x60ff', from: '0x1111111111111111111111111111111111111111', to: '0x1111111111111111111111111111111111111111', value: '0x1', chainId: '0x1', nonce: '0xff', blockHash: fakeHash, blockNumber: '0xffff' })[field] })) }), true,
      ]),
    ];
    for (const [changedMethod, mutate, fullOnly] of mutations) {
      const provider = { ...actual.provider, getTransactionReceipt: async () => null, getTransaction: async () => null,
        send: async (method, params) => {
          const raw = await localRPC(method, params);
          return method === changedMethod && (!fullOnly || params[1] === true) ? mutate(raw) : raw;
        } };
      await expect(deployment.run(actual.config, { artifact, provider })).to.be.rejected;
      expect(fs.readFileSync(actual.config.output, 'utf8')).to.equal(original);
    }
  });

  it('does not fall back after hash RPC errors or malformed non-null results', async function () {
    const actual = await local();
    for (const [field, value] of [
      ['getTransactionReceipt', new Error('HTTP 403')], ['getTransactionReceipt', new Error('RPC error')],
      ['getTransaction', new Error('HTTP 403')], ['getTransaction', new Error('RPC error')],
      ['getTransactionReceipt', {}], ['getTransactionReceipt', undefined], ['getTransaction', {}], ['getTransaction', undefined],
    ]) {
      let fallbackCalls = 0;
      const provider = { ...actual.provider, [field]: async () => { if (value instanceof Error) throw value; return value; }, send: async () => { fallbackCalls++; throw new Error('must not fall back'); } };
      await expect(deployment.run(actual.config, { artifact, provider })).to.be.rejected;
      expect(fallbackCalls).to.equal(0);
    }
    const recovered = await deployment.run(actual.config, { artifact, provider: { ...actual.provider, getTransaction: async () => null } });
    expect(recovered.txHash).to.equal(actual.result.txHash);
  });

  it('bounds missing-hash scans and has no fallback without a public hint', async function () {
    const hash = `0x${'aa'.repeat(32)}`;
    let calls = 0;
    const provider = { getTransactionReceipt: async () => null, getBlockNumber: async () => 1000,
      send: async (method, [tag]) => { calls++; expect(method).to.equal('eth_getBlockByNumber'); return { number: tag, hash: `0x${'bb'.repeat(32)}`, transactions: [] }; } };
    await expect(deployment.waitForDeploymentReceipt(provider, hash, 1, 1000, { searchFromBlock: 1 })).to.be.rejectedWith('reached 256 blocks');
    expect(calls).to.equal(256);
    calls = 0;
    await expect(deployment.waitForDeploymentReceipt(provider, hash, 1, 5)).to.be.rejectedWith('timeout');
    expect(calls).to.equal(0);
    await expect(deployment.waitForDeploymentReceipt({ ...provider, send: async () => new Promise(() => {}) }, hash, 1, 5, { searchFromBlock: 1 })).to.be.rejectedWith('timed out');
  });

  it('keeps confirmation waiting bounded after locating a receipt and stops on block RPC errors', async function () {
    const actual = await local();
    const before = fs.readFileSync(actual.config.output, 'utf8');
    const provider = { ...actual.provider, getTransactionReceipt: async () => null };
    await expect(deployment.run({ ...actual.config, confirmations: 2, timeoutMs: 15 }, { artifact, provider })).to.be.rejectedWith('timeout');
    expect(fs.readFileSync(actual.config.output, 'utf8')).to.equal(before);
    for (const failureMethod of ['eth_getBlockByNumber', 'eth_getBlockReceipts']) {
      let failed = false;
      const stopped = { ...provider, send: async (method, params) => {
        expect(failed).to.equal(false);
        if (method === failureMethod) { failed = true; throw new Error('HTTP 403'); }
        return localRPC(method, params);
      } };
      await expect(deployment.run(actual.config, { artifact, provider: stopped })).to.be.rejectedWith('HTTP 403');
      expect(failed).to.equal(true);
    }
    expect(actual.broadcasts()).to.equal(1);
  });

  it('rejects a wrong payer before signing, pending reservation or broadcast', async function () {
    let signed = false, broadcast = false;
    const config = options({ VAULT_DEPLOYER_ADDRESS: '0x1111111111111111111111111111111111111111' });
    const provider = { getNetwork: async () => ({ chainId: 31337n }), getBlockNumber: async () => 1,
      broadcastTransaction: async () => { broadcast = true; throw new Error('must not broadcast'); } };
    const signer = { getAddress: async () => '0x2222222222222222222222222222222222222222',
      populateTransaction: async () => { signed = true; throw new Error('must not prepare signing'); } };
    await expect(deployment.run(config, { artifact, provider, signer })).to.be.rejectedWith('VAULT_DEPLOYER_ADDRESS');
    expect(signed).to.equal(false); expect(broadcast).to.equal(false);
    expect(fs.existsSync(config.output)).to.equal(false);
  });

  it('requires the expected payer in the journal and in an explicit recovered transaction', async function () {
    const actual = await local();
    const original = fs.readFileSync(actual.config.output, 'utf8');
    const correct = { ...actual.config, expectedDeployer: actual.result.deployer };
    expect((await deployment.run(correct, { artifact, provider: actual.provider })).txHash).to.equal(actual.result.txHash);
    const wrong = { ...actual.config, expectedDeployer: '0x1111111111111111111111111111111111111111' };
    await expect(deployment.run(wrong, { artifact, provider: { getNetwork: async () => { throw new Error('must reject journal before RPC'); } } })).to.be.rejectedWith('VAULT_DEPLOYER_ADDRESS');
    expect(fs.readFileSync(actual.config.output, 'utf8')).to.equal(original);
    const explicit = { ...wrong, recoverTxHash: actual.result.txHash, output: path.join(directory, 'wrong-payer.json') };
    await expect(deployment.run(explicit, { artifact, provider: actual.provider })).to.be.rejectedWith('VAULT_DEPLOYER_ADDRESS');
    expect(fs.existsSync(explicit.output)).to.equal(false);
    expect(actual.broadcasts()).to.equal(1);
  });

  it('prints bounded error context without raw transactions or the full ethers message', function () {
    const raw = '0x' + 'ab'.repeat(20000);
    const message = deployment.formatDeploymentError({ code: 'INSUFFICIENT_FUNDS', shortMessage: 'insufficient funds for intrinsic transaction cost',
      message: `FULL_MESSAGE_MUST_NOT_APPEAR transaction=${raw}`, transaction: raw,
      info: { error: { message: 'insufficient funds: balance 0, tx cost 123' } } });
    expect(message).to.include('code=INSUFFICIENT_FUNDS').and.include('balance 0, tx cost 123').and.include('Retain the journal');
    expect(message).not.to.include(raw).and.not.to.include('FULL_MESSAGE_MUST_NOT_APPEAR');
    const untrusted = deployment.formatDeploymentError({ code: 'RPC_ERROR', shortMessage: raw, info: { error: { message: raw + '\n' + 'x'.repeat(1000) } } });
    expect(untrusted).not.to.include(raw).and.not.to.include('abababab').and.not.to.include('\n');
    expect(untrusted.length).to.be.lessThan(600);
    expect(deployment.formatDeploymentError(new Error('deployment payer differs from VAULT_DEPLOYER_ADDRESS'))).to.include('deployment payer differs');
  });

  async function replayFixture() {
    const config = options({ VAULT_REBROADCAST_TX_FILE: path.join(directory, 'original.tx') });
    const signer = Wallet.createRandom().connect(hre.ethers.provider);
    await hre.ethers.provider.send('hardhat_setBalance', [signer.address, '0x1000000000000000000']);
    const creation = await new ethers.ContractFactory(artifact.abi, artifact.bytecode).getDeployTransaction(config.px, config.rawPy, config.guardianPx, config.guardianPy, config.epoch, config.dexChainId);
    const raw = await signer.signTransaction(await signer.populateTransaction({ ...creation, value: 0n }));
    const tx = ethers.Transaction.from(raw);
    const existing = tools.writeVaultPending(config.output, tools.vaultIdentity(config, artifact), {
      txHash: tx.hash, deployer: signer.address, address: ethers.getCreateAddress({ from: signer.address, nonce: tx.nonce }), searchFromBlock: 1,
    });
    const savedBytes = fs.readFileSync(config.output, 'utf8');
    fs.writeFileSync(config.rebroadcastTxFile, raw + '\n');
    let broadcasts = 0;
    const provider = {
      getNetwork: () => hre.ethers.provider.getNetwork(), send: localRPC,
      getTransactionReceipt: hash => hre.ethers.provider.getTransactionReceipt(hash),
      getTransaction: hash => hre.ethers.provider.getTransaction(hash),
      getBlockNumber: () => hre.ethers.provider.getBlockNumber(),
      getTransactionCount: (address, tag) => hre.ethers.provider.getTransactionCount(address, tag),
      getBalance: (address, tag) => hre.ethers.provider.getBalance(address, tag),
      getCode: address => hre.ethers.provider.getCode(address),
      getBlock: number => hre.ethers.provider.getBlock(number), call: request => hre.ethers.provider.call(request),
      broadcastTransaction: async value => {
        expect(value).to.equal(raw);
        expect(fs.readFileSync(config.output, 'utf8')).to.equal(savedBytes);
        broadcasts++; return hre.ethers.provider.broadcastTransaction(value);
      },
    };
    Object.defineProperty(config, 'privateKey', { enumerable: true, get() { throw new Error('replay read a private key'); } });
    return { config, signer, raw, tx, existing, savedBytes, provider, broadcasts: () => broadcasts };
  }

  it('explicitly rebroadcasts identical bytes once with a current in-memory hint despite an old pending search window', async function () {
    const fixture = await replayFixture();
    await hre.ethers.provider.send('hardhat_mine', ['0x12c']);
    const tip = await hre.ethers.provider.getBlockNumber(), searched = [];
    fixture.provider.getTransactionReceipt = async () => null;
    fixture.provider.getTransaction = async () => null;
    fixture.provider.send = async (method, params) => { if (method === 'eth_getBlockByNumber') searched.push(Number(BigInt(params[0]))); return localRPC(method, params); };
    const summary = await deployment.validateRebroadcastTransaction(fixture.raw, fixture.config, artifact, fixture.existing);
    expect(summary).to.include({ txHash: fixture.tx.hash, deployer: fixture.signer.address, nonce: fixture.tx.nonce });
    expect(summary).not.to.have.property('raw');
    const result = await deployment.run(fixture.config, { artifact, provider: fixture.provider, signer: { getAddress() { throw new Error('must not access a signer'); } } });
    expect(result.status).to.equal('verified');
    expect(result.txHash).to.equal(fixture.existing.txHash);
    expect(result).not.to.have.property('searchFromBlock');
    expect(fixture.broadcasts()).to.equal(1);
    expect(searched.length).to.be.greaterThan(0);
    expect(searched.every(number => number >= tip + 1)).to.equal(true);
  });

  it('only verifies an already-mined original transaction and never broadcasts it again', async function () {
    const fixture = await replayFixture();
    await hre.ethers.provider.broadcastTransaction(fixture.raw);
    const result = await deployment.run(fixture.config, { artifact, provider: fixture.provider });
    expect(result.status).to.equal('verified');
    expect(fixture.broadcasts()).to.equal(0);
  });

  it('requires the explicit pending mode and mainnet acknowledgement before any replay', async function () {
    const fixture = await replayFixture();
    const publicOptions = { ...options(), rebroadcastTxFile: fixture.config.rebroadcastTxFile };
    await expect(deployment.deployEvmVault(publicOptions, artifact, null, null, { provider: {} })).to.be.rejectedWith('existing pending');
    await expect(deployment.deployEvmVault(publicOptions, artifact, { ...fixture.existing, status: 'verified' }, null, { provider: {} })).to.be.rejectedWith('existing pending');
    await expect(deployment.deployEvmVault({ ...publicOptions, chainId: 56n }, artifact, fixture.existing, null, { provider: {} })).to.be.rejectedWith('VAULT_ALLOW_MAINNET_DEPLOY');
    await expect(deployment.run({ ...options(), timeoutMs: 5 }, { artifact, provider: fixture.provider })).to.be.rejectedWith(/time.*out/);
    expect(fixture.broadcasts()).to.equal(0);
    expect(fs.readFileSync(fixture.config.output, 'utf8')).to.equal(fixture.savedBytes);
  });

  it('rejects substituted raw bytes, identity fields and unsupported files without changing the journal', async function () {
    const fixture = await replayFixture();
    for (const changed of [
      { ...fixture.existing, txHash: `0x${'ab'.repeat(32)}` },
      { ...fixture.existing, deployer: '0x1111111111111111111111111111111111111111' },
      { ...fixture.existing, address: '0x1111111111111111111111111111111111111111' },
      { ...fixture.existing, dexChainId: '7' },
    ]) await expect(deployment.validateRebroadcastTransaction(fixture.raw, fixture.config, artifact, changed)).to.be.rejected;
    const changedTx = ethers.Transaction.from(fixture.raw); changedTx.signature = null; changedTx.nonce++;
    const substituted = await fixture.signer.signTransaction(changedTx);
    fs.writeFileSync(fixture.config.rebroadcastTxFile, substituted);
    await expect(deployment.run(fixture.config, { artifact, provider: fixture.provider })).to.be.rejectedWith('differs from the original');
    fs.writeFileSync(fixture.config.rebroadcastTxFile, 'not a signed transaction');
    await expect(deployment.run(fixture.config, { artifact, provider: fixture.provider })).to.be.rejectedWith('invalid signed transaction');
    fs.writeFileSync(fixture.config.rebroadcastTxFile, 'x'.repeat(1024 * 1024 + 1));
    await expect(deployment.run(fixture.config, { artifact, provider: fixture.provider })).to.be.rejectedWith('unsafe signed transaction');
    expect(fixture.broadcasts()).to.equal(0);
    expect(fs.readFileSync(fixture.config.output, 'utf8')).to.equal(fixture.savedBytes);
  });

  it('stops a replay when nonce, balance, code or original-transaction evidence is inconsistent', async function () {
    const fixture = await replayFixture();
    const overrides = [
      { getTransactionCount: async (_address, tag) => tag === 'latest' ? fixture.tx.nonce + 1 : fixture.tx.nonce },
      { getTransactionCount: async (_address, tag) => tag === 'pending' ? fixture.tx.nonce + 1 : fixture.tx.nonce },
      { getBalance: async () => 0n },
      { getCode: async () => '0x6000' },
      { getTransactionReceipt: async () => ({ hash: fixture.tx.hash, status: 0, blockNumber: 1 }) },
      { getTransactionReceipt: async () => { throw new Error('HTTP 403'); } },
      { getTransaction: async () => ({ hash: fixture.tx.hash, blockNumber: null }) },
      { getTransaction: async () => { throw new Error('RPC error'); } },
    ];
    for (const override of overrides) {
      await expect(deployment.run(fixture.config, { artifact, provider: { ...fixture.provider, ...override } })).to.be.rejected;
      expect(fs.readFileSync(fixture.config.output, 'utf8')).to.equal(fixture.savedBytes);
      expect(fixture.broadcasts()).to.equal(0);
    }
  });

  it('attempts an uncertain explicit replay only once and leaves its original pending bytes intact', async function () {
    const fixture = await replayFixture();
    let attempted = 0;
    const provider = { ...fixture.provider, broadcastTransaction: async raw => { expect(raw).to.equal(fixture.raw); attempted++; throw new Error('connection lost after send'); } };
    await expect(deployment.run(fixture.config, { artifact, provider })).to.be.rejectedWith('connection lost');
    expect(attempted).to.equal(1);
    expect(fs.readFileSync(fixture.config.output, 'utf8')).to.equal(fixture.savedBytes);
  });
});
