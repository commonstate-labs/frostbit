const { guardianPoint, guardianSignature, dualHandover, GUARDIAN_SECRET } = require('./helpers/schnorr');
const { expect } = require('chai');
const { ethers } = require('hardhat');
const {
  FIELD, ORDER, SECRET_A, SECRET_B, DOMAIN,
  point, canonical, sign, item, batch, batchDigest, batchSignature, changeSignature,
} = require('./helpers/schnorr');

async function deploy(name, ...args) {
  const Factory = await ethers.getContractFactory(name);
  const contract = await Factory.deploy(...args);
  await contract.waitForDeployment();
  return contract;
}

async function vaultArgs(name, secret = SECRET_A, epoch = 7n) {
  return [...await point(secret), ...await guardianPoint(), epoch, 51025n, ...(name.includes('Tron') ? [ethers.ZeroAddress] : [])];
}

describe('SchnorrVault batch withdrawal safety (local EVM)', function () {
  let vault, owner, broadcaster, recipient;

  beforeEach(async function () {
    [owner, broadcaster, recipient] = await ethers.getSigners();
    vault = await deploy('SchnorrExampleV3', ...await vaultArgs('SchnorrExample'));
    await owner.sendTransaction({ to: await vault.getAddress(), value: ethers.parseEther('10') });
  });

  it('exposes only batch withdrawal/rotation entrypoints and validates public keys/signatures', async function () {
    const functions = new Set(vault.interface.fragments.filter(x => x.type === 'function').map(x => x.name));
    expect(functions.has('withdraw')).to.equal(false);
    expect(functions.has('changePub')).to.equal(false);
    expect(functions.has('withdrawBatch')).to.equal(true);
    expect(functions.has('withdrawBatchWithHandovers')).to.equal(true);

    const Factory = await ethers.getContractFactory('SchnorrExampleV3');
    for (const key of [[0n, 0n], [1n, 1n], [FIELD, 2n]]) {
      await expect(Factory.deploy(...key, ...await guardianPoint(), 7n, 51025n)).to.be.revertedWith('Vault: invalid public key');
    }
    const raw = await point(SECRET_A);
    const digest = ethers.id('signature-check');
    expect(await vault.verifySignature(...raw, ...await sign(digest), digest)).to.equal(true);
    for (const [rx, ry, scalar] of [[0n, 0n, 1n], [1n, 1n, 1n], [FIELD, 2n, 1n], [1n, 2n, 0n], [1n, 2n, ORDER]]) {
      expect(await vault.verifySignature(...raw, rx, ry, scalar, digest)).to.equal(false);
    }
  });

  it('executes native items independently, consumes failures, and rewards the first broadcaster', async function () {
    const rejecting = await deploy('StorageReceiver');
    await rejecting.setAccepting(false);
    const value = await batch(vault, recipient.address, {
      items: [
        item(await rejecting.getAddress(), { requestId: ethers.id('native-fail'), grossAmount: 1001n, fee: 1n }),
        item(recipient.address, { requestId: ethers.id('native-ok'), grossAmount: 10001n }),
      ],
    });
    const before = await ethers.provider.getBalance(recipient.address);
    const tx = await vault.connect(broadcaster).withdrawBatch(value, await batchSignature(value), await guardianSignature(value));
    await expect(tx).to.emit(vault, 'WithdrawalAttempted')
      .withArgs(ethers.id('native-fail'), await rejecting.getAddress(), ethers.ZeroAddress, 1001n, 1n, 1000n, false)
      .and.to.emit(vault, 'WithdrawalAttempted')
      .withArgs(ethers.id('native-ok'), recipient.address, ethers.ZeroAddress, 10001n, 2n, 9999n, true);
    expect(await rejecting.received()).to.equal(0n);
    expect(await ethers.provider.getBalance(recipient.address) - before).to.equal(9999n);
    expect(await vault.consumed(ethers.id('native-fail'))).to.equal(true);
    expect(await vault.consumed(ethers.id('native-ok'))).to.equal(true);
    expect(await vault.claimableReward(ethers.ZeroAddress, broadcaster.address)).to.equal(3n);
    await expect(vault.withdrawBatch(value, await batchSignature(value, SECRET_A, 19n), await guardianSignature(value)))
      .to.be.revertedWith('Vault: wrong batch sequence');
  });

  for (const tokenName of ['StandardToken', 'NoReturnToken']) {
    it(`accepts ${tokenName} and pays net amounts`, async function () {
      const token = await deploy(tokenName);
      await token.mint(await vault.getAddress(), 10000n);
      const value = await batch(vault, recipient.address, { token: await token.getAddress() });
      await expect(vault.connect(broadcaster).withdrawBatch(value, await batchSignature(value), await guardianSignature(value)))
        .to.emit(vault, 'WithdrawalAttempted')
        .withArgs(ethers.id('test-request-1'), recipient.address, await token.getAddress(), 1001n, 1n, 1000n, true);
      expect(await token.balanceOf(recipient.address)).to.equal(1000n);
      expect(await vault.claimableReward(await token.getAddress(), broadcaster.address)).to.equal(1n);
      await vault.connect(broadcaster).claimReward(await token.getAddress(), recipient.address);
      expect(await token.balanceOf(recipient.address)).to.equal(1001n);
      expect(await vault.claimableReward(await token.getAddress(), broadcaster.address)).to.equal(0n);
    });
  }

  it('rolls back a false-return token item while allowing a later item and consumes both', async function () {
    const token = await deploy('SelectiveToken');
    const blocked = (await ethers.getSigners())[4];
    await token.mint(await vault.getAddress(), 10000n);
    await token.setBlocked(blocked.address, true);
    const value = await batch(vault, recipient.address, {
      token: await token.getAddress(),
      items: [
        item(blocked.address, { requestId: ethers.id('blocked') }),
        item(recipient.address, { requestId: ethers.id('allowed') }),
      ],
    });
    await vault.withdrawBatch(value, await batchSignature(value), await guardianSignature(value));
    expect(await token.balanceOf(blocked.address)).to.equal(0n);
    expect(await token.balanceOf(recipient.address)).to.equal(1000n);
    expect(await vault.consumed(ethers.id('blocked'))).to.equal(true);
    expect(await vault.consumed(ethers.id('allowed'))).to.equal(true);
  });

  for (const [name, args] of [
    ['FalseReturnToken', [0n]], ['MalformedReturnToken', []], ['RevertingToken', []],
    ['HugeReturnToken', []], ['GasBurnerToken', []],
  ]) {
    it(`isolates ${name}, bounds its gas/return data, and permanently consumes the item`, async function () {
      const token = await deploy(name, ...args);
      if (token.mint) await token.mint(await vault.getAddress(), 5000n);
      const value = await batch(vault, recipient.address, { token: await token.getAddress() });
      const tx = await vault.withdrawBatch(value, await batchSignature(value), await guardianSignature(value));
      await expect(tx).to.emit(vault, 'WithdrawalAttempted')
        .withArgs(ethers.id('test-request-1'), recipient.address, await token.getAddress(), 1001n, 1n, 1000n, false);
      expect(await vault.consumed(ethers.id('test-request-1'))).to.equal(true);
    });
  }

  it('treats a token address without code as one failed consumed attempt', async function () {
    const value = await batch(vault, recipient.address, { token: recipient.address });
    await expect(vault.withdrawBatch(value, await batchSignature(value), await guardianSignature(value)))
      .to.emit(vault, 'WithdrawalAttempted')
      .withArgs(ethers.id('test-request-1'), recipient.address, recipient.address, 1001n, 1n, 1000n, false);
    expect(await vault.consumed(ethers.id('test-request-1'))).to.equal(true);
  });

  it('blocks token reentrancy while preserving the outer payment', async function () {
    const token = await deploy('ReentrantToken');
    await token.mint(await vault.getAddress(), 10000n);
    const nested = await batch(vault, recipient.address, {
      token: await token.getAddress(),
      items: [item(recipient.address, { requestId: ethers.id('nested') })],
    });
    const nestedCall = vault.interface.encodeFunctionData('withdrawBatch', [nested, await batchSignature(nested), await guardianSignature(nested)]);
    await token.configure(await vault.getAddress(), nestedCall);
    const outer = await batch(vault, recipient.address, {
      token: await token.getAddress(),
      items: [item(recipient.address, { requestId: ethers.id('outer') })],
    });
    await vault.withdrawBatch(outer, await batchSignature(outer), await guardianSignature(outer));
    expect(await token.callbackSucceeded()).to.equal(false);
    expect(await token.balanceOf(recipient.address)).to.equal(1000n);
    expect(await vault.consumed(ethers.id('outer'))).to.equal(true);
    expect(await vault.consumed(ethers.id('nested'))).to.equal(false);
  });

  it('claims rewards with checks-effects-interactions and preserves credit on failure', async function () {
    const rejecting = await deploy('StorageReceiver');
    await rejecting.setAccepting(false);
    const value = await batch(vault, recipient.address);
    await vault.connect(broadcaster).withdrawBatch(value, await batchSignature(value), await guardianSignature(value));
    await expect(vault.connect(broadcaster).claimReward(ethers.ZeroAddress, await rejecting.getAddress()))
      .to.be.revertedWith('Vault: reward transfer failed');
    expect(await vault.claimableReward(ethers.ZeroAddress, broadcaster.address)).to.equal(1n);
    const before = await ethers.provider.getBalance(recipient.address);
    await expect(vault.connect(broadcaster).claimReward(ethers.ZeroAddress, recipient.address))
      .to.emit(vault, 'RewardClaimed').withArgs(broadcaster.address, ethers.ZeroAddress, recipient.address, 1n);
    expect(await ethers.provider.getBalance(recipient.address) - before).to.equal(1n);
    expect(await vault.claimableReward(ethers.ZeroAddress, broadcaster.address)).to.equal(0n);
  });

  it('preserves token reward credit when the token returns false', async function () {
    const token = await deploy('FalseReturnToken', 0n);
    await token.mint(await vault.getAddress(), 5000n);
    const value = await batch(vault, recipient.address, { token: await token.getAddress() });
    await vault.connect(broadcaster).withdrawBatch(value, await batchSignature(value), await guardianSignature(value));
    expect(await vault.claimableReward(await token.getAddress(), broadcaster.address)).to.equal(1n);
    const before = await token.balanceOf(recipient.address);
    await expect(vault.connect(broadcaster).claimReward(await token.getAddress(), recipient.address))
      .to.be.revertedWith('Vault: reward transfer failed');
    expect(await token.balanceOf(recipient.address)).to.equal(before);
    expect(await vault.claimableReward(await token.getAddress(), broadcaster.address)).to.equal(1n);
  });

  it('binds every field to the signature and rejects invalid static fields', async function () {
    const original = await batch(vault, recipient.address);
    const signature = await batchSignature(original);
    const changed = structuredClone(original);
    changed.items[0].grossAmount = 2001n;
    await expect(vault.withdrawBatch(changed, signature, await guardianSignature(changed))).to.be.revertedWith('Vault: invalid signature');

    const cases = [
      [{ domain: ethers.id('OTHER') }, 'wrong domain'],
      [{ chainId: 999n }, 'wrong target'],
      [{ vault: recipient.address }, 'wrong target'],
      [{ epoch: 8n }, 'wrong epoch'],
      [{ items: [] }, 'invalid batch size'],
      [{ items: [item(recipient.address, { requestId: ethers.ZeroHash })] }, 'empty request id'],
      [{ items: [item(recipient.address, { grossAmount: 0n })] }, 'invalid payment'],
      [{ items: [item(recipient.address, { grossAmount: 1n })] }, 'invalid payment'],
      [{ items: [item(ethers.ZeroAddress)] }, 'invalid payment'],
      [{ items: [item(await vault.getAddress())] }, 'invalid payment'],
    ];
    for (const [override, reason] of cases) {
      const value = { ...original, ...override };
      await expect(vault.withdrawBatch(value, await batchSignature(value), await guardianSignature(value))).to.be.revertedWith(`Vault: ${reason}`);
    }
  });

  it('enforces strict contiguous sequence before signature checks and stops at MaxUint256', async function () {
    const correct = await batch(vault, recipient.address);
    const ahead = { ...correct, batchSeq: 2n };
    await expect(vault.withdrawBatch(ahead, [0n, 0n, 0n], await guardianSignature(ahead))).to.be.revertedWith('Vault: wrong batch sequence');
    expect(await vault.nextBatchSeq()).to.equal(1n);

    await vault.withdrawBatch(correct, await batchSignature(correct), await guardianSignature(correct));
    expect(await vault.nextBatchSeq()).to.equal(2n);
    await expect(vault.withdrawBatch(correct, await batchSignature(correct, SECRET_A, 19n), await guardianSignature(correct)))
      .to.be.revertedWith('Vault: wrong batch sequence');
    const future = await batch(vault, recipient.address, {
      batchSeq: 3n,
      items: [item(recipient.address, { requestId: ethers.id('future-sequence') })],
    });
    await expect(vault.withdrawBatch(future, [0n, 0n, 0n], await guardianSignature(future))).to.be.revertedWith('Vault: wrong batch sequence');

    const terminal = await deploy('SchnorrVaultSequenceHarness', ...await point(SECRET_A), ...await guardianPoint(), 7n, 51025n);
    await terminal.forceNextBatchSeq(ethers.MaxUint256 - 1n);
    const finalAccepted = await batch(terminal, recipient.address, {
      batchSeq: ethers.MaxUint256 - 1n,
      items: [item(recipient.address, { requestId: ethers.id('final-sequence') })],
    });
    await terminal.withdrawBatch(finalAccepted, await batchSignature(finalAccepted), await guardianSignature(finalAccepted));
    expect(await terminal.nextBatchSeq()).to.equal(ethers.MaxUint256);
    const stopped = await batch(terminal, recipient.address, { batchSeq: ethers.MaxUint256 });
    await expect(terminal.withdrawBatch(stopped, [0n, 0n, 0n], await guardianSignature(stopped))).to.be.revertedWith('Vault: batch sequence exhausted');
  });

  it('rejects malformed reward buckets, indexes, and item-token mappings before signature checks', async function () {
    const tokenA = await deploy('StandardToken');
    const tokenB = await deploy('StandardToken');
    const [low, high] = [await tokenA.getAddress(), await tokenB.getAddress()]
      .sort((a, b) => BigInt(a) < BigInt(b) ? -1 : 1);
    const validItem = item(recipient.address, { token: low, rewardIndex: 0n });
    const base = await batch(vault, recipient.address, { rewardTokens: [low], items: [validItem] });
    const highItem = item(recipient.address, {
      requestId: ethers.id('high-bucket-item'), token: high, rewardIndex: 1n,
    });
    const secondLowItem = item(recipient.address, {
      requestId: ethers.id('second-low-item'), token: low, rewardIndex: 0n,
    });
    const cases = [
      [{ ...base, rewardTokens: [] }, 'invalid reward tokens'],
      [{ ...base, rewardTokens: [high, low], items: [validItem, highItem] }, 'reward tokens not sorted'],
      [{ ...base, rewardTokens: [low, low], items: [validItem, secondLowItem] }, 'reward tokens not sorted'],
      [{ ...base, items: [{ ...validItem, rewardIndex: 1n }] }, 'reward index out of range'],
      [{ ...base, rewardTokens: [high], items: [validItem] }, 'invalid payment'],
      [{ ...base, rewardTokens: [low, high], items: [validItem, secondLowItem] }, 'unreferenced reward token'],
    ];
    for (const [value, reason] of cases) {
      await expect(vault.withdrawBatch(value, [0n, 0n, 0n], await guardianSignature(value))).to.be.revertedWith(`Vault: ${reason}`);
      expect(await vault.nextBatchSeq()).to.equal(1n);
    }
  });

  it('uses exact ceil(gross/10000) fees at every arithmetic boundary', async function () {
    const gross = [2n, 9999n, 10000n, 10001n, ethers.MaxUint256];
    const expected = [1n, 1n, 1n, 2n, ethers.MaxUint256 / 10000n + 1n];
    const value = await batch(vault, recipient.address, {
      items: gross.map((amount, i) => item(recipient.address, {
        requestId: ethers.id(`fee-boundary-${i}`), grossAmount: amount,
      })),
    });
    const receipt = await (await vault.withdrawBatch(value, await batchSignature(value), await guardianSignature(value))).wait();
    const attempts = receipt.logs.map(log => { try { return vault.interface.parseLog(log); } catch (_) { return null; } })
      .filter(log => log?.name === 'WithdrawalAttempted');
    expect(attempts.map(log => log.args.fee)).to.deep.equal(expected);
    expect(await vault.claimableReward(ethers.ZeroAddress, owner.address))
      .to.equal(expected.reduce((sum, fee) => sum + fee, 0n));
    expect(await vault.nextBatchSeq()).to.equal(2n);
  });

  it('aggregates a mixed-asset batch once per reward token', async function () {
    const tokenA = await deploy('StandardToken');
    const tokenB = await deploy('NoReturnToken');
    for (const token of [tokenA, tokenB]) await token.mint(await vault.getAddress(), 100000n);
    const tokens = [ethers.ZeroAddress, await tokenA.getAddress(), await tokenB.getAddress()]
      .sort((a, b) => BigInt(a) < BigInt(b) ? -1 : 1);
    const index = new Map(tokens.map((token, i) => [token.toLowerCase(), BigInt(i)]));
    const items = [
      item(recipient.address, { requestId: ethers.id('mixed-native'), token: ethers.ZeroAddress, rewardIndex: index.get(ethers.ZeroAddress), grossAmount: 10001n }),
      item(recipient.address, { requestId: ethers.id('mixed-a-1'), token: await tokenA.getAddress(), rewardIndex: index.get((await tokenA.getAddress()).toLowerCase()), grossAmount: 20001n }),
      item(recipient.address, { requestId: ethers.id('mixed-a-2'), token: await tokenA.getAddress(), rewardIndex: index.get((await tokenA.getAddress()).toLowerCase()), grossAmount: 30001n }),
      item(recipient.address, { requestId: ethers.id('mixed-b'), token: await tokenB.getAddress(), rewardIndex: index.get((await tokenB.getAddress()).toLowerCase()), grossAmount: 40001n }),
    ];
    const value = await batch(vault, recipient.address, { rewardTokens: tokens, items });
    await vault.connect(broadcaster).withdrawBatch(value, await batchSignature(value), await guardianSignature(value));
    expect(await vault.claimableReward(ethers.ZeroAddress, broadcaster.address)).to.equal(2n);
    expect(await vault.claimableReward(await tokenA.getAddress(), broadcaster.address)).to.equal(7n);
    expect(await vault.claimableReward(await tokenB.getAddress(), broadcaster.address)).to.equal(5n);
    expect(await vault.nextBatchSeq()).to.equal(2n);
  });

  it('rejects duplicate, pre-consumed, and 65-item batches before interaction', async function () {
    const duplicate = await batch(vault, recipient.address, {
      items: [item(recipient.address), item(recipient.address)],
    });
    await expect(vault.withdrawBatch(duplicate, await batchSignature(duplicate), await guardianSignature(duplicate)))
      .to.be.revertedWith('Vault: duplicate request');
    expect(await vault.consumed(ethers.id('test-request-1'))).to.equal(false);

    const first = await batch(vault, recipient.address);
    await vault.withdrawBatch(first, await batchSignature(first), await guardianSignature(first));
    const mixed = await batch(vault, recipient.address, {
      items: [item(recipient.address), item(recipient.address, { requestId: ethers.id('fresh') })],
    });
    await expect(vault.withdrawBatch(mixed, await batchSignature(mixed), await guardianSignature(mixed)))
      .to.be.revertedWith('Vault: already consumed');
    expect(await vault.consumed(ethers.id('fresh'))).to.equal(false);

    const tooMany = await batch(vault, recipient.address, {
      items: Array.from({ length: 65 }, (_, i) => item(recipient.address, { requestId: ethers.id(`too-many-${i}`) })),
    });
    await expect(vault.withdrawBatch(tooMany, [0n, 0n, 0n], await guardianSignature(tooMany))).to.be.revertedWith('Vault: invalid batch size');
  });

  it('hands over only with a nonempty batch, supports idempotent new-epoch packages, and invalidates old signatures', async function () {
    const nextKey = await point(SECRET_B);
    const oldBatch = await batch(vault, recipient.address, {
      items: [item(recipient.address, { requestId: ethers.id('old-signed') })],
    });
    const oldSignature = await batchSignature(oldBatch);
    const nextEpoch = 8n;
    const handoverProof = await dualHandover(vault, nextKey, nextEpoch);
    const first = await batch(vault, recipient.address, {
      epoch: nextEpoch,
      items: [item(recipient.address, { requestId: ethers.id('handover-first') })],
    });
    await expect(vault.withdrawBatchWithHandovers(first, await batchSignature(first, SECRET_B), await guardianSignature(first), [handoverProof]))
      .to.emit(vault, 'PubChanged').withArgs(...canonical(await point(SECRET_A)), ...canonical(nextKey));
    expect(await vault.authorityNonce()).to.equal(nextEpoch);
    expect(Array.from(await vault.P())).to.deep.equal(canonical(nextKey));

    await expect(vault.withdrawBatch(oldBatch, oldSignature, await guardianSignature(oldBatch))).to.be.revertedWith('Vault: wrong batch sequence');
    const second = await batch(vault, recipient.address, {
      items: [item(recipient.address, { requestId: ethers.id('handover-second') })],
    });
    await expect(vault.withdrawBatchWithHandovers(second, await batchSignature(second, SECRET_B), await guardianSignature(second), [handoverProof]))
      .not.to.emit(vault, 'PubChanged');
    expect(await vault.consumed(ethers.id('handover-second'))).to.equal(true);

    const empty = { ...second, batchSeq: await vault.nextBatchSeq(), items: [] };
    await expect(vault.withdrawBatchWithHandovers(empty, [0n, 0n, 0n], await guardianSignature(empty), [handoverProof]))
      .to.be.revertedWith('Vault: invalid batch size');
  });

  it('advances handover even when every payment attempt fails', async function () {
    const nextKey = await point(SECRET_B);
    const token = await deploy('RevertingToken');
    const value = await batch(vault, recipient.address, {
      epoch: 8n,
      token: await token.getAddress(),
      items: [item(recipient.address, { requestId: ethers.id('failed-handover-item') })],
    });
    const handover = await dualHandover(vault, nextKey, 8n);
    await expect(vault.withdrawBatchWithHandovers(value, await batchSignature(value, SECRET_B), await guardianSignature(value), [handover]))
      .to.emit(vault, 'WithdrawalAttempted')
      .withArgs(ethers.id('failed-handover-item'), recipient.address, await token.getAddress(), 1001n, 1n, 1000n, false);
    expect(await vault.authorityNonce()).to.equal(8n);
    expect(await vault.consumed(ethers.id('failed-handover-item'))).to.equal(true);
    expect(await vault.nextBatchSeq()).to.equal(2n);
  });

  it('rolls back authority, consumption, and rewards when the top-level transaction runs out of gas', async function () {
    const nextKey = await point(SECRET_B);
    const token = await deploy('GasBurnerToken');
    const value = await batch(vault, recipient.address, {
      epoch: 8n,
      token: await token.getAddress(),
      items: Array.from({ length: 4 }, (_, i) => item(recipient.address, { requestId: ethers.id(`oog-${i}`) })),
    });
    const handover = await dualHandover(vault, nextKey, 8n);
    await expect(vault.withdrawBatchWithHandovers(value, await batchSignature(value, SECRET_B), await guardianSignature(value), [handover], { gasLimit: 350000n })).to.be.reverted;
    expect(await vault.authorityNonce()).to.equal(7n);
    expect(await vault.nextBatchSeq()).to.equal(1n);
    expect(await vault.consumed(ethers.id('oog-0'))).to.equal(false);
    expect(await vault.claimableReward(await token.getAddress(), owner.address)).to.equal(0n);
  });

  it('keeps 64 distinct gas-burning tokens and 64 cold reward slots below the 80% gas gate', async function () {
    this.timeout(120000);
    const tokens = [];
    for (let i = 0; i < 64; i++) tokens.push(await deploy('GasBurnerToken'));
    const rewardTokens = (await Promise.all(tokens.map(token => token.getAddress())))
      .sort((a, b) => BigInt(a) < BigInt(b) ? -1 : 1);
    const makeItems = prefix => rewardTokens.map((token, i) => item(
      ethers.getAddress(`0x${(1000 + i).toString(16).padStart(40, '0')}`),
      { requestId: ethers.id(`${prefix}-${i}`), token, rewardIndex: BigInt(i) }
    ));
    const normal = await batch(vault, recipient.address, { rewardTokens, items: makeItems('normal') });
    const normalReceipt = await (await vault.withdrawBatch(normal, await batchSignature(normal), await guardianSignature(normal), { gasLimit: 16777216n })).wait();

    const nextKey = await point(SECRET_B);
    const handoverBatch = await batch(vault, recipient.address, {
      epoch: 8n, rewardTokens, items: makeItems('handover'),
    });
    const handover = await dualHandover(vault, nextKey, 8n);
    const handoverReceipt = await (await vault.connect(broadcaster).withdrawBatchWithHandovers(handoverBatch, await batchSignature(handoverBatch, SECRET_B), await guardianSignature(handoverBatch), [handover], { gasLimit: 16777216n })).wait();
    console.log(`    gas: 64 distinct malicious tokens/cold rewards normal=${normalReceipt.gasUsed}, handover=${handoverReceipt.gasUsed}`);
    expect(normalReceipt.gasUsed).to.be.at.most(13421772n);
    expect(handoverReceipt.gasUsed).to.be.at.most(13421772n);
  });

  it('executes 64-item native and standard-token batches below the transaction cap', async function () {
    this.timeout(120000);
    const recipients = prefix => Array.from({ length: 64 }, (_, i) => item(
      ethers.getAddress(`0x${(3000 + i).toString(16).padStart(40, '0')}`),
      { requestId: ethers.id(`${prefix}-${i}`) }
    ));
    const native = await batch(vault, recipient.address, { items: recipients('native-64') });
    const nativeReceipt = await (await vault.withdrawBatch(native, await batchSignature(native), await guardianSignature(native), { gasLimit: 16777216n })).wait();

    const token = await deploy('StandardToken');
    await token.mint(await vault.getAddress(), 100000n);
    const standard = await batch(vault, recipient.address, {
      token: await token.getAddress(), items: recipients('standard-64'),
    });
    const standardReceipt = await (await vault.withdrawBatch(standard, await batchSignature(standard), await guardianSignature(standard), { gasLimit: 16777216n })).wait();
    console.log(`    gas: 64 native=${nativeReceipt.gasUsed}, standard-token=${standardReceipt.gasUsed}`);
    expect(nativeReceipt.gasUsed).to.be.lessThan(16777216n);
    expect(standardReceipt.gasUsed).to.be.lessThan(16777216n);
  });
});

for (const name of ['SchnorrExampleV3', 'SchnorrExampleTronV3']) {
  describe(`${name} shared batch ABI`, function () {
    it('executes the same signed native batch semantics', async function () {
      const [, recipient] = await ethers.getSigners();
      const instance = await deploy(name, ...await vaultArgs(name));
      await (await ethers.getSigners())[0].sendTransaction({ to: await instance.getAddress(), value: 5000n });
      const value = await batch(instance, recipient.address);
      await instance.withdrawBatch(value, await batchSignature(value), await guardianSignature(value));
      expect(await instance.consumed(ethers.id('test-request-1'))).to.equal(true);
    });
  });
}
