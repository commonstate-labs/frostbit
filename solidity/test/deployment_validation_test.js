const { guardianPoint, guardianSignature, dualHandover, GUARDIAN_SECRET } = require('./helpers/schnorr');
const { expect } = require('chai');
const { validatePubkey, resolvePubkey, uint64Env, validateLiveDeployment } = require('../scripts/deploy_schnorr_vault');
const { FIELD } = require('./helpers/schnorr');

describe('Deployment parameter validation', function () {
  it('requires explicit mainnet deployment intent while allowing local verification', function () {
    const previous = process.env.VAULT_ALLOW_MAINNET_DEPLOY;
    try {
      delete process.env.VAULT_ALLOW_MAINNET_DEPLOY;
      for (const chainId of [1n, 56n, 8453n, 42161n]) {
        expect(() => validateLiveDeployment(chainId)).to.throw('explicit');
      }
      expect(() => validateLiveDeployment(31337n)).not.to.throw();
      process.env.VAULT_ALLOW_MAINNET_DEPLOY = 'true';
      expect(() => validateLiveDeployment(1n)).not.to.throw();
    } finally {
      if (previous === undefined) delete process.env.VAULT_ALLOW_MAINNET_DEPLOY;
      else process.env.VAULT_ALLOW_MAINNET_DEPLOY = previous;
    }
  });
  it('accepts a curve point and canonicalizes high Y', function () {
    expect(validatePubkey('1', FIELD - 2n)).to.deep.equal({ px: 1n, rawPy: FIELD - 2n, py: 2n });
  });
  it('rejects invalid / zero / out-of-range points', function () {
    for (const pair of [[0n, 0n], [1n, 1n], [FIELD, 2n], [-1n, 2n]]) {
      expect(() => validatePubkey(...pair)).to.throw('BN128 curve point');
    }
  });
  it('requires exact hexadecimal group key encoding', function () {
    const previous = process.env.VAULT_GROUP_PUBKEY;
    try {
      process.env.VAULT_GROUP_PUBKEY = 'z'.repeat(128);
      expect(resolvePubkey).to.throw('64 bytes hex');
    } finally {
      if (previous === undefined) delete process.env.VAULT_GROUP_PUBKEY;
      else process.env.VAULT_GROUP_PUBKEY = previous;
    }
  });
  it('requires a supplied nonnegative integer epoch within DEX uint64 bounds', function () {
    const name = 'VAULT_TEST_EPOCH';
    for (const value of ['-1', '1.2', '1e3', ((1n << 64n)).toString()]) {
      process.env[name] = value;
      expect(() => uint64Env(name)).to.throw('decimal uint64');
    }
    delete process.env[name];
    expect(() => uint64Env(name)).to.throw('missing env');
  });
});
