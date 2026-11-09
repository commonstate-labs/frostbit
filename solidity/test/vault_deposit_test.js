const { guardianPoint, guardianSignature, dualHandover, GUARDIAN_SECRET } = require('./helpers/schnorr');
const { expect } = require('chai');
const { ethers } = require('hardhat');
const {
  SECRET_A, SECRET_B, point, item, batch, batchSignature, changeSignature,
} = require('./helpers/schnorr');

const DEX_CHAIN_ID = 51025n;
const receiver = 'fbc1qdepositreceiver';                                                               
const reentryError = ethers.id('Error(string)').slice(0, 10)
  + ethers.AbiCoder.defaultAbiCoder().encode(['string'], ['Vault: reentrant call']).slice(2);
async function deploy(name, ...args) {
  const contract = await (await ethers.getContractFactory(name)).deploy(...args);
  await contract.waitForDeployment();
  return contract;
}

for (const name of ['SchnorrExampleV3', 'SchnorrExampleTronV3']) {
  describe(`${name} integrated Vault deposits (local EVM)`, function () {
    let vault, token, user, attacker, payee;
    const extra = token => name.includes('Tron') ? [token] : [];
    beforeEach(async function () {
      [user, attacker, payee] = await ethers.getSigners();
      vault = await deploy(name, ...await point(SECRET_A), ...await guardianPoint(), 7n, DEX_CHAIN_ID, ...extra(ethers.ZeroAddress));
      token = await deploy('DepositTokenMock');
      await token.mint(user.address, 1000n);
      await token.approve(vault.target, 1000n);
    });

    it('holds a beneficiary-bound native deposit in the same Vault and pays a signed withdrawal', async function () {
      expect(await vault.dexChainId()).to.equal(DEX_CHAIN_ID);
      await expect(vault.depositNative(receiver, { value: 1000n }))
        .to.emit(vault, 'Deposited').withArgs(DEX_CHAIN_ID, vault.target, ethers.ZeroAddress, receiver, 1000n);
      expect(await ethers.provider.getBalance(vault.target)).to.equal(1000n);
      const value = await batch(vault, payee.address);
      const signature = await batchSignature(value);
      const tampered = { ...value, items: [item(attacker.address)] };
      await expect(vault.withdrawBatch(tampered, signature, await guardianSignature(tampered)))
        .to.be.revertedWith('Vault: invalid signature');
      const before = await ethers.provider.getBalance(payee.address);
      await vault.withdrawBatch(value, signature, await guardianSignature(value));
      expect(await ethers.provider.getBalance(payee.address) - before).to.equal(1000n);
      expect(await ethers.provider.getBalance(vault.target)).to.equal(0n);
    });

    it('preserves deposit domain and funds through a batch-bound authority handover', async function () {
      await vault.depositNative(receiver, { value: 1000n });
      const oldBatch = await batch(vault, payee.address, {
        items: [item(payee.address, { requestId: ethers.id('old-before-handover') })],
      });
      const nextKey = await point(SECRET_B);
      const nextBatch = await batch(vault, payee.address, { epoch: 8n });
      const handover = await dualHandover(vault, nextKey, 8n);
      await vault.withdrawBatchWithHandovers(nextBatch, await batchSignature(nextBatch, SECRET_B), await guardianSignature(nextBatch), [handover]);
      expect(await vault.dexChainId()).to.equal(DEX_CHAIN_ID);
      await expect(vault.withdrawBatch(oldBatch, await batchSignature(oldBatch), await guardianSignature(oldBatch)))
        .to.be.revertedWith('Vault: wrong batch sequence');
      expect(await ethers.provider.getBalance(vault.target)).to.equal(0n);
      await expect(vault.depositNative(receiver, { value: 2n }))
        .to.emit(vault, 'Deposited').withArgs(DEX_CHAIN_ID, vault.target, ethers.ZeroAddress, receiver, 2n);
    });

    for (const mode of [0, 1]) {
      it(`receives and withdraws exact token funds with return mode ${mode}`, async function () {
        await token.setMode(mode);
        await token.mint(vault.target, 50n);
        await expect(vault.depositToken(token.target, 100n, receiver))
          .to.emit(vault, 'Deposited').withArgs(DEX_CHAIN_ID, vault.target, token.target, receiver, 100n);
        expect(await token.balanceOf(vault.target)).to.equal(150n);
        expect(await token.balanceOf(user.address)).to.equal(900n);
        expect(await token.allowance(user.address, vault.target)).to.equal(900n);
        const value = await batch(vault, payee.address, {
          token: token.target,
          items: [item(payee.address, { grossAmount: 101n, fee: 1n })],
        });
        await vault.withdrawBatch(value, await batchSignature(value), await guardianSignature(value));
        expect(await token.balanceOf(payee.address)).to.equal(100n);
        expect(await token.balanceOf(vault.target)).to.equal(50n);
      });
    }

    for (const [mode, error] of [
      [2, 'token transfer failed'], [3, 'token transfer failed'], [4, 'inexact token receipt'],
      [5, 'invalid token return'], [6, 'token call failed'], [7, 'inexact token receipt'],
    ]) {
      it(`rolls back balances, allowance and event for rejected token mode ${mode}`, async function () {
        await token.setMode(mode);
        const beforeBlock = await ethers.provider.getBlockNumber();
        await expect(vault.depositToken(token.target, 100n, receiver)).to.be.revertedWith(`Vault: ${error}`);
        expect(await token.balanceOf(vault.target)).to.equal(0n);
        expect(await token.balanceOf(user.address)).to.equal(1000n);
        expect(await token.allowance(user.address, vault.target)).to.equal(1000n);
        expect(await vault.queryFilter(vault.filters.Deposited(), beforeBlock + 1)).to.have.length(0);
      });
    }

    it('requires exact balance query responses before and after transfer', async function () {
      for (const after of [false, true]) {
        const broken = await deploy('BrokenDepositBalanceToken', after);
        await expect(vault.depositToken(broken.target, 1n, receiver)).to.be.revertedWith('Vault: balance query failed');
        expect(await broken.transferAttempted()).to.equal(false);
      }
    });

    it('cannot use another wallet allowance or replace its beneficiary with a copied call', async function () {
      await expect(vault.connect(attacker).depositToken(token.target, 100n, 'fbc1qattacker'))
        .to.be.revertedWith('Vault: token call failed');
      expect(await token.balanceOf(user.address)).to.equal(1000n);
      expect(await token.allowance(user.address, vault.target)).to.equal(1000n);
      await token.mint(attacker.address, 100n);
      await token.connect(attacker).approve(vault.target, 100n);
      await expect(vault.connect(attacker).depositToken(token.target, 100n, 'fbc1qattacker'))
        .to.emit(vault, 'Deposited').withArgs(DEX_CHAIN_ID, vault.target, token.target, 'fbc1qattacker', 100n);
      await expect(vault.depositToken(token.target, 100n, receiver))
        .to.emit(vault, 'Deposited').withArgs(DEX_CHAIN_ID, vault.target, token.target, receiver, 100n);
      expect(await token.balanceOf(user.address)).to.equal(900n);
      expect(await token.balanceOf(attacker.address)).to.equal(0n);
    });

    it('shares the lock with signed batches and batch-bound handover during token receipt', async function () {
      await vault.depositNative(receiver, { value: 1000n });
      const value = await batch(vault, payee.address);
      const signature = await batchSignature(value);
      await token.configureCallback(vault.target, vault.interface.encodeFunctionData('withdrawBatch', [value, signature, await guardianSignature(value)]));
      await vault.depositToken(token.target, 100n, receiver);
      expect(await token.callbackSucceeded()).to.equal(false);
      expect(await token.callbackResult()).to.equal(reentryError);
      expect(await vault.consumed(ethers.id('test-request-1'))).to.equal(false);
      await vault.withdrawBatch(value, signature, await guardianSignature(value));

      const nextKey = await point(SECRET_B);
      await vault.depositNative(receiver, { value: 2n });
      const nextBatch = await batch(vault, payee.address, {
        epoch: 8n,
        items: [item(payee.address, { requestId: ethers.id('handover-lock'), grossAmount: 2n, fee: 1n })],
      });
      const change = await dualHandover(vault, nextKey, 8n);
      const handoverCall = vault.interface.encodeFunctionData(
        'withdrawBatchWithHandovers', [nextBatch, await batchSignature(nextBatch, SECRET_B), await guardianSignature(nextBatch), [change]]
      );
      await token.configureCallback(vault.target, handoverCall);
      await vault.depositToken(token.target, 100n, receiver);
      expect(await token.callbackSucceeded()).to.equal(false);
      expect(await token.callbackResult()).to.equal(reentryError);
      expect(await vault.authorityNonce()).to.equal(7n);
      await vault.withdrawBatchWithHandovers(nextBatch, await batchSignature(nextBatch, SECRET_B), await guardianSignature(nextBatch), [change]);
      expect(await vault.authorityNonce()).to.equal(8n);
    });

    it('prevents a native withdrawal recipient from recycling its payment into a deposit event', async function () {
      const callback = await deploy('DepositNativeReceiver');
      await callback.configureCallback(vault.target, vault.interface.encodeFunctionData('depositNative', [receiver]));
      await vault.depositNative(receiver, { value: 1000n });
      const value = await batch(vault, callback.target);
      const receipt = await (await vault.withdrawBatch(value, await batchSignature(value), await guardianSignature(value))).wait();
      expect(await callback.callbackSucceeded()).to.equal(false);
      expect(await ethers.provider.getBalance(callback.target)).to.equal(0n);
      expect(await vault.consumed(ethers.id('test-request-1'))).to.equal(true);
      expect(receipt.logs.filter(log => log.topics[0] === vault.interface.getEvent('Deposited').topicHash)).to.have.length(0);
    });

    it('prevents depositToken reentry from both incoming and outgoing token callbacks', async function () {
      await token.configureCallback(vault.target, vault.interface.encodeFunctionData('depositToken', [token.target, 1n, receiver]));
      await vault.depositToken(token.target, 100n, receiver);
      expect(await token.callbackResult()).to.equal(reentryError);
      const value = await batch(vault, payee.address, {
        token: token.target,
        items: [item(payee.address, { grossAmount: 101n, fee: 1n })],
      });
      await vault.withdrawBatch(value, await batchSignature(value), await guardianSignature(value));
      expect(await token.callbackSucceeded()).to.equal(false);
      expect(await token.callbackResult()).to.equal(reentryError);
      expect(await token.balanceOf(payee.address)).to.equal(100n);
    });

    it('rejects invalid domain, zero funds, unsupported token targets and empty/oversized receivers', async function () {
      for (const dexChain of [0n, 1n << 64n]) {
        await expect(deploy(name, ...await point(SECRET_A), ...await guardianPoint(), 7n, dexChain, ...extra(ethers.ZeroAddress)))
          .to.be.revertedWith('Vault: invalid DEX chain');
      }
      await expect(vault.depositNative(receiver)).to.be.revertedWith('Vault: empty amount');
      await expect(vault.depositToken(token.target, 0n, receiver)).to.be.revertedWith('Vault: empty amount');
      for (const bad of ['', 'a'.repeat(91)]) {
        await expect(vault.depositNative(bad, { value: 1n })).to.be.revertedWith('Vault: invalid receiver length');
        await expect(vault.depositToken(token.target, 1n, bad)).to.be.revertedWith('Vault: invalid receiver length');
      }
      for (const bad of [ethers.ZeroAddress, user.address]) {
        await expect(vault.depositToken(bad, 1n, receiver)).to.be.revertedWith('Vault: token has no code');
      }
      const plain = await (await user.sendTransaction({ to: vault.target, value: 1n })).wait();
      expect(plain.logs).to.have.length(0);
      expect(await ethers.provider.getBalance(vault.target)).to.equal(1n);
    });
  });
}

describe('Integrated TRON Vault fixed false-return deposit token (local EVM)', function () {
  it('allows exact configured deposits while the strict batch payout reports false-return failure', async function () {
    const [user, payee] = await ethers.getSigners();
    const token = await deploy('DepositTokenMock');
    const vault = await deploy('SchnorrExampleTron', ...await point(SECRET_A), ...await guardianPoint(), 7n, DEX_CHAIN_ID, token.target);
    await token.mint(user.address, 1000n);
    await token.approve(vault.target, 1000n);
    await token.setMode(2);
    await expect(vault.depositToken(token.target, 100n, receiver))
      .to.emit(vault, 'Deposited').withArgs(DEX_CHAIN_ID, vault.target, token.target, receiver, 100n);
    const value = await batch(vault, payee.address, {
      token: token.target,
      items: [item(payee.address, { grossAmount: 101n, fee: 1n })],
    });
    await expect(vault.withdrawBatch(value, await batchSignature(value), await guardianSignature(value)))
      .to.emit(vault, 'WithdrawalAttempted')
      .withArgs(ethers.id('test-request-1'), payee.address, token.target, 101n, 1n, 100n, false);
    expect(await token.balanceOf(payee.address)).to.equal(0n);
    for (const mode of [3, 4, 7]) {
      await token.setMode(mode);
      await expect(vault.depositToken(token.target, 100n, receiver)).to.be.revertedWith('Vault: inexact token receipt');
      expect(await token.balanceOf(vault.target)).to.equal(100n);
      expect(await token.balanceOf(user.address)).to.equal(900n);
      expect(await token.allowance(user.address, vault.target)).to.equal(900n);
    }
    const other = await deploy('DepositTokenMock');
    await other.mint(user.address, 100n);
    await other.approve(vault.target, 100n);
    await other.setMode(2);
    await expect(vault.depositToken(other.target, 100n, receiver)).to.be.revertedWith('Vault: token transfer failed');
    expect(await other.balanceOf(user.address)).to.equal(100n);
  });
});
