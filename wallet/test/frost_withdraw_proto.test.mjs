import test from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { build } from 'esbuild';
import { fileURLToPath } from 'node:url';
import { getCompressedPubKey } from '../lib/crypto.js';

const output = await build({
    stdin: {
        contents: 'export { buildAndSign } from "./lib/proto.js"; export { pb } from "./lib/data_proto.js";',
        resolveDir: fileURLToPath(new URL('..', import.meta.url)),
    },
    bundle: true,
    platform: 'node',
    format: 'cjs',
    write: false,
});
const compiled = { exports: {} };
new Function('require', 'module', 'exports', output.outputFiles[0].text)(createRequire(import.meta.url), compiled, compiled.exports);
const { buildAndSign, pb } = compiled.exports;

const rawPrivKey = new Uint8Array(32);
rawPrivKey[31] = 1;
const signer = {
    fromAddress: 'fbc1qwithdrawowner',
    nonce: 9,
    rawPrivKey,
    pubKeyCompressed: getCompressedPubKey(rawPrivKey),
    fee: '1000',
    chainId: 1,
};

test('withdraw request signs only the gross amount and derives no user-selected broadcaster fee', () => {
    const encoded = buildAndSign({
        type: 'frost_withdraw',
        asset: 'br_eth',
        toAddress: '0x2222222222222222222222222222222222222222',
        amount: '10001',
                                                                             
        broadcastFee: '9999',
    }, signer);
    const decoded = pb.AnyTx.decode(encoded).frostWithdrawRequestTx;

    assert.equal(decoded.asset, 'br_eth');
    assert.equal(decoded.to, '0x2222222222222222222222222222222222222222');
    assert.equal(decoded.amount, '10001');
    assert.equal(Object.hasOwn(decoded, 'broadcastFee'), false);
});

test('BTC v3 budgets are signed exactly and changing either changes authorization', () => {
    const desc = { type: 'frost_withdraw', asset: 'br_btc', toAddress: 'bc1ptest', amount: '100000000', btcProtocolVersion: 3, btcMaxTotalFee: '9007199254740993', btcMaxParts: 4096 };
    const encoded = buildAndSign(desc, signer);
    const decoded = pb.AnyTx.decode(encoded).frostWithdrawRequestTx;
    assert.equal(decoded.btcProtocolVersion, 3);
    assert.equal(decoded.btcMaxTotalFee, '9007199254740993');
    assert.equal(decoded.btcMaxParts, 4096);
    assert.notDeepEqual(buildAndSign({ ...desc, btcMaxParts: 4095 }, signer), encoded);
    assert.notDeepEqual(buildAndSign({ ...desc, btcMaxTotalFee: '9007199254740994' }, signer), encoded);
});

test('BTC signing rejects unspecified, excessive and lossy authorization budgets', () => {
    const desc = { type: 'frost_withdraw', asset: 'br_btc', toAddress: 'bc1ptest', amount: '1000000', btcProtocolVersion: 3, btcMaxTotalFee: '1000', btcMaxParts: 4 };
    for (const fields of [{ btcMaxParts: 0 }, { btcMaxParts: 4097 }, { btcMaxParts: 1.5 }, { btcMaxTotalFee: '01' }, { btcMaxTotalFee: 9007199254740993 }, { btcMaxTotalFee: '18446744073709551616' }, { btcProtocolVersion: 2 }, { btcProtocolVersion: 0 }]) {
        assert.throws(() => buildAndSign({ ...desc, ...fields }, signer));
    }
});
