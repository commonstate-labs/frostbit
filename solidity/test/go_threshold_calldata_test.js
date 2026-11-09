const { guardianPoint, guardianSignature, dualHandover, GUARDIAN_SECRET } = require('./helpers/schnorr');
const { expect } = require('chai');
const { ethers } = require('hardhat');
const { SECRET_A, point, batch, batchDigest, batchSignature } = require('./helpers/schnorr');

describe('Batch ABI expected by the Go FROST adapter', function () {
  it('uses keccak256(abi.encode(batch)) and the fixed tuple ABI', async function () {
    const [, recipient] = await ethers.getSigners();
    const vault = await (await ethers.getContractFactory('SchnorrExampleV3')).deploy(
      ...await point(SECRET_A), ...await guardianPoint(), 1n, 51025n
    );
    await (await ethers.getSigners())[0].sendTransaction({ to: await vault.getAddress(), value: 5000n });
    const value = await batch(vault, recipient.address);
    const signature = await batchSignature(value);
    expect(await vault.verifySignature(...await point(SECRET_A), ...signature, batchDigest(value))).to.equal(true);
    await vault.withdrawBatch(value, signature, await guardianSignature(value));
    expect(await vault.consumed(value.items[0].requestId)).to.equal(true);
  });

  it('has no legacy single-withdraw or standalone-key-change selector', async function () {
    const artifact = await ethers.getContractFactory('SchnorrExampleV3');
    expect(artifact.interface.getFunction('withdraw')).to.equal(null);
    expect(artifact.interface.getFunction('changePub')).to.equal(null);
  });
});
