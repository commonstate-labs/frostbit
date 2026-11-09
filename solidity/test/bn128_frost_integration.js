const { guardianPoint, guardianSignature, dualHandover, GUARDIAN_SECRET } = require('./helpers/schnorr');
                                                                                 
const { expect } = require('chai');
const { ethers } = require('hardhat');
const vectors = require('./bn128_frost_vectors.json').vectors;
const { SECRET_A, point, canonical, batch, batchSignature } = require('./helpers/schnorr');

describe('Go FROST / Solidity compatibility', function () {
  it('verifies every persisted Go BN128 threshold signature', async function () {
    const Factory = await ethers.getContractFactory('SchnorrExampleV3');
    const vault = await Factory.deploy(...await point(SECRET_A), ...await guardianPoint(), 1n, 51025n);
    expect(vectors.length).to.be.greaterThan(0);
    for (const v of vectors) {
      expect(await vault.verifySignature(...['px', 'py', 'rx', 'ry', 's', 'msg_hash'].map(k => '0x' + v[k])), v.name).to.equal(true);
    }
  });

  it('uses the published Go fixture authority for a domain-bound withdrawal', async function () {
    const v = vectors.find(v => v.name === 'withdraw');
    const raw = await point(SECRET_A);
    expect(canonical(raw)).to.deep.equal(canonical([BigInt('0x' + v.px), BigInt('0x' + v.py)]));
    const Factory = await ethers.getContractFactory('SchnorrExampleV3');
    const vault = await Factory.deploy('0x' + v.px, '0x' + v.py, ...await guardianPoint(), 7n, 51025n);
    const [sender, recipient] = await ethers.getSigners();
    await sender.sendTransaction({ to: await vault.getAddress(), value: 1000n });
    const value = await batch(vault, recipient.address);
    const before = await ethers.provider.getBalance(recipient.address);
    await vault.withdrawBatch(value, await batchSignature(value), await guardianSignature(value));
    expect(await ethers.provider.getBalance(recipient.address) - before).to.equal(1000n);
  });
});
