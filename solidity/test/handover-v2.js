const { guardianPoint, guardianSignature, dualHandover, GUARDIAN_SECRET } = require('./helpers/schnorr');
const { expect } = require('chai');
const { ethers } = require('hardhat');
const {
  SECRET_A, canonical, point, sign, item, batch, batchSignature, changeSignature,
} = require('./helpers/schnorr');

                                                                              
                                                                                
const AUTHORITY_DOMAIN = ethers.id('DEX_VAULT_AUTHORITY_HANDOVER_V3');
const coder = ethers.AbiCoder.defaultAbiCoder();

async function deployV2(dexChainId = 51025n) {
  const factory = await ethers.getContractFactory('SchnorrExampleV3');
  const vault = await factory.deploy(...await point(SECRET_A), ...await guardianPoint(), 1n, dexChainId);
  await vault.waitForDeployment();
  return vault;
}
function epochSecret(epoch) { return SECRET_A + BigInt(epoch - 1) * 101n; }
async function certificateDigest(vault, c, scope = {}) {
  return ethers.keccak256(coder.encode(
    ['bytes32', 'uint256', 'uint256', 'address', 'uint256', 'uint256', 'uint256', 'uint256', 'uint256'],
    [scope.domain ?? AUTHORITY_DOMAIN, scope.dexChainId ?? 51025n,
      scope.chainId ?? (await ethers.provider.getNetwork()).chainId,
      scope.vault ?? await vault.getAddress(), c.fromEpoch, c.toEpoch,
      c.newPx, c.newPy, c.frozenNextBatchSeq]
  ));
}
async function certificate(vault, from, cutoff = 1n, scope = {}) {
  const next = canonical(await point(epochSecret(from + 1)));
  const c = { fromEpoch: BigInt(from), toEpoch: BigInt(from + 1), newPx: next[0], newPy: next[1], frozenNextBatchSeq: cutoff };
  const signature = await sign(await certificateDigest(vault, c, scope), epochSecret(from), BigInt(17 + from));
  const guardian = await sign(await certificateDigest(vault,c,scope),GUARDIAN_SECRET,7919n);
  return { ...c, rx: signature[0], ry: signature[1], s: signature[2],guardianRx:guardian[0],guardianRy:guardian[1],guardianS:guardian[2] };
}
async function certificates(vault, count, cutoff = 1n, first = 1) {
  const result = [];
  for (let i = first; i < first + count; i++) result.push(await certificate(vault, i, cutoff));
  return result;
}

describe('SchnorrVaultV2 consecutive authority safety (local EVM)', function () {
  let vault, owner, broadcaster, recipient;
  beforeEach(async function () {
    [owner, broadcaster, recipient] = await ethers.getSigners();
    vault = await deployV2();
    await owner.sendTransaction({ to: await vault.getAddress(), value: ethers.parseEther('1') });
  });

  for (const count of [7, 8, 9]) {
    it(`bounds real signed proof chains at ${count} and atomically pays the latest epoch`, async function () {
      const proofs = await certificates(vault, count);
      const value = await batch(vault, recipient.address, { epoch: BigInt(count + 1) });
      const signature = await batchSignature(value, epochSecret(count + 1));
      if (count > 8) {
        await expect(vault.connect(broadcaster).withdrawBatchWithHandovers(value, signature, await guardianSignature(value), proofs))
          .to.be.revertedWith('Vault: too many handovers');
        expect(await vault.authorityNonce()).to.equal(1n);
        expect(await vault.nextBatchSeq()).to.equal(1n);
        expect(await vault.appliedCertificateDigest(2n)).to.equal(ethers.ZeroHash);
        return;
      }
      const before = await ethers.provider.getBalance(recipient.address);
      const tx = await vault.connect(broadcaster).withdrawBatchWithHandovers(value, signature, await guardianSignature(value), proofs);
      const receipt = await tx.wait();
      console.log(`[handover-v2 gas] combined proofs=${count} gas=${receipt.gasUsed}`);
      expect(await vault.authorityNonce()).to.equal(BigInt(count + 1));
      expect(await vault.nextBatchSeq()).to.equal(2n);
      expect(await ethers.provider.getBalance(recipient.address) - before).to.equal(1000n);
      expect(await vault.claimableReward(ethers.ZeroAddress, broadcaster.address)).to.equal(1n);
      expect(await vault.consumed(ethers.id('test-request-1'))).to.equal(true);
    });
  }

  it('bounds 64 malicious tokens and cold rewards plus eight authority proofs below the 80% gas gate', async function () {
    this.timeout(120000);
    const tokenFactory = await ethers.getContractFactory('GasBurnerToken');
    const tokens = [];
    for (let i = 0; i < 64; i++) {
      const token = await tokenFactory.deploy();
      await token.waitForDeployment();
      tokens.push(await token.getAddress());
    }
    const rewardTokens = tokens.sort((a, b) => BigInt(a) < BigInt(b) ? -1 : 1);
    const items = rewardTokens.map((token, i) => item(
      ethers.getAddress(`0x${(12000 + i).toString(16).padStart(40, '0')}`),
      { requestId: ethers.id(`v2-worst-${i}`), token, rewardIndex: BigInt(i) }
    ));
    const proofs = await certificates(vault, 8);
    const value = await batch(vault, recipient.address, { epoch: 9n, rewardTokens, items });
    const receipt = await (await vault.connect(broadcaster).withdrawBatchWithHandovers(value, await batchSignature(value, epochSecret(9)), await guardianSignature(value), proofs, { gasLimit: 16777216n })).wait();
    console.log(`[handover-v2 gas] proofs=8 items=64 distinct malicious tokens/cold rewards gas=${receipt.gasUsed}`);
    expect(receipt.gasUsed).to.be.at.most(13421772n);
    expect(await vault.authorityNonce()).to.equal(9n);
    expect(await vault.nextBatchSeq()).to.equal(2n);
    for (let i = 0; i < 64; i++) {
      expect(await vault.consumed(ethers.id(`v2-worst-${i}`))).to.equal(true);
      expect(await vault.claimableReward(rewardTokens[i], broadcaster.address)).to.equal(1n);
    }
  });
  it('segments a history beyond the per-call cap without dummy withdrawals', async function () {
    const proofs = await certificates(vault, 9);
    const before = await ethers.provider.getBalance(await vault.getAddress());
    const catchup = await (await vault.connect(broadcaster).catchUpAuthority(proofs.slice(0, 8))).wait();
    console.log(`[handover-v2 gas] catchup proofs=8 gas=${catchup.gasUsed}`);
    expect(await vault.authorityNonce()).to.equal(9n);
    expect(await vault.nextBatchSeq()).to.equal(1n);
    expect(await ethers.provider.getBalance(await vault.getAddress())).to.equal(before);
    expect(await vault.consumed(ethers.id('test-request-1'))).to.equal(false);
    expect(await vault.claimableReward(ethers.ZeroAddress, broadcaster.address)).to.equal(0n);
    const value = await batch(vault, recipient.address, { epoch: 10n });
    const receipt = await (await vault.connect(broadcaster).withdrawBatchWithHandovers(value, await batchSignature(value, epochSecret(10)), await guardianSignature(value), proofs.slice(8))).wait();
    console.log(`[handover-v2 gas] combined proofs=1 after segment gas=${receipt.gasUsed}`);
    expect(await vault.authorityNonce()).to.equal(10n);
    expect(await vault.nextBatchSeq()).to.equal(2n);
  });

  it('preserves an old signed obligation until its frozen sequence cutoff is paid', async function () {
    const oldValue = await batch(vault, recipient.address, { item: { requestId: ethers.id('old-signed') } });
    const oldSignature = await batchSignature(oldValue, epochSecret(1));
    const proofs = await certificates(vault, 2, 2n);
    const latest = await batch(vault, recipient.address, {
      epoch: 3n, batchSeq: 2n, item: { requestId: ethers.id('latest-signed') },
    });
    const latestSignature = await batchSignature(latest, epochSecret(3));
    await expect(vault.catchUpAuthority(proofs)).to.be.revertedWith('Vault: pending old batches');
    await expect(vault.withdrawBatchWithHandovers(latest, latestSignature, await guardianSignature(latest), proofs)).to.be.revertedWith('Vault: pending old batches');
    expect(await vault.authorityNonce()).to.equal(1n);
    expect(await vault.consumed(ethers.id('old-signed'))).to.equal(false);
    await vault.connect(broadcaster).withdrawBatch(oldValue, oldSignature, await guardianSignature(oldValue));
    await vault.connect(broadcaster).withdrawBatchWithHandovers(latest, latestSignature, await guardianSignature(latest), proofs);
    expect(await vault.authorityNonce()).to.equal(3n);
    expect(await vault.nextBatchSeq()).to.equal(3n);
    expect(await vault.consumed(ethers.id('old-signed'))).to.equal(true);
    expect(await vault.consumed(ethers.id('latest-signed'))).to.equal(true);
    expect(await vault.claimableReward(ethers.ZeroAddress, broadcaster.address)).to.equal(2n);
  });

  it('retries exact applied prefixes after the sequence advances, but rejects changed bodies', async function () {
    const proofs = await certificates(vault, 2);
    const first = await batch(vault, recipient.address, { epoch: 3n });
    await vault.connect(broadcaster).withdrawBatchWithHandovers(first, await batchSignature(first, epochSecret(3)), await guardianSignature(first), proofs);
    await vault.catchUpAuthority(proofs);
    expect(await vault.authorityNonce()).to.equal(3n);
    expect(await vault.nextBatchSeq()).to.equal(2n);
    const changed = proofs.map(c => ({ ...c }));
    changed[0].frozenNextBatchSeq = 2n;
    await expect(vault.catchUpAuthority(changed)).to.be.revertedWith('Vault: conflicting certificate');
    expect(await vault.appliedCertificateDigest(2n)).to.equal(await certificateDigest(vault, proofs[0]));
    const next = await batch(vault, recipient.address, { item: { requestId: ethers.id('second-batch') } });
    await vault.connect(broadcaster).withdrawBatchWithHandovers(next, await batchSignature(next, epochSecret(3)), await guardianSignature(next), proofs);
    expect(await vault.nextBatchSeq()).to.equal(3n);
  });

  it('rejects missing and nonadjacent intermediate authorities without changing state', async function () {
    const proofs = await certificates(vault, 3);
    await expect(vault.catchUpAuthority(proofs.slice(1))).to.be.revertedWith('Vault: missing prior handover');
    await expect(vault.catchUpAuthority([proofs[0], proofs[2]])).to.be.revertedWith('Vault: broken handover chain');
    await expect(vault.catchUpAuthority([{ ...proofs[0], toEpoch: 3n }])).to.be.revertedWith('Vault: nonconsecutive handover');
    expect(await vault.authorityNonce()).to.equal(1n);
    expect(await vault.appliedCertificateDigest(2n)).to.equal(ethers.ZeroHash);
  });

  it('atomically reverts every authority prefix if a later signature or withdrawal fails', async function () {
    const proofs = await certificates(vault, 2);
    const badProofs = proofs.map(c => ({ ...c }));
    badProofs[1].s = (badProofs[1].s + 1n);
    await expect(vault.catchUpAuthority(badProofs)).to.be.revertedWith('Vault: invalid pubupdate signature');
    const value = await batch(vault, recipient.address, { epoch: 3n });
    await expect(vault.withdrawBatchWithHandovers(value, await batchSignature(value, epochSecret(1)), await guardianSignature(value), proofs))
      .to.be.revertedWith('Vault: invalid signature');
    expect(await vault.authorityNonce()).to.equal(1n);
    expect(await vault.nextBatchSeq()).to.equal(1n);
    expect(await vault.appliedCertificateDigest(2n)).to.equal(ethers.ZeroHash);
    expect(await vault.appliedCertificateDigest(3n)).to.equal(ethers.ZeroHash);
    expect(await vault.consumed(ethers.id('test-request-1'))).to.equal(false);
  });

  it('binds the V2 domain, both chain domains and exact target Vault in real signatures', async function () {
    const correct = await certificate(vault, 1);
    expect(await vault.handoverDigest(correct)).to.equal(await certificateDigest(vault, correct));
    for (const scope of [
      { domain: ethers.id('DEX_VAULT_AUTHORITY_HANDOVER_V1') },
      { dexChainId: 51026n }, { chainId: 1n },
      { vault: recipient.address },
    ]) {
      await expect(vault.catchUpAuthority([await certificate(vault, 1, 1n, scope)]))
        .to.be.revertedWith('Vault: invalid pubupdate signature');
    }
    await vault.catchUpAuthority([correct]);
    expect(await vault.authorityNonce()).to.equal(2n);
  });

  it('does not expose the old single-authority handover ABI', async function () {
    expect(vault.interface.getFunction('withdrawBatchWithHandover')).to.equal(null);
    expect(await vault.authorityNonce()).to.equal(1n);
  });

  it('keeps consumed request IDs across authority changes and forbids replay in a new batch', async function () {
    const first = await batch(vault, recipient.address);
    await vault.withdrawBatch(first, await batchSignature(first, epochSecret(1)), await guardianSignature(first));
    const proof = await certificate(vault, 1, 2n);
    await vault.catchUpAuthority([proof]);
    const duplicate = await batch(vault, recipient.address);
    await expect(vault.withdrawBatch(duplicate, await batchSignature(duplicate, epochSecret(2)), await guardianSignature(duplicate)))
      .to.be.revertedWith('Vault: already consumed');
    expect(await vault.nextBatchSeq()).to.equal(2n);
    expect(await vault.consumed(ethers.id('test-request-1'))).to.equal(true);
  });
});
