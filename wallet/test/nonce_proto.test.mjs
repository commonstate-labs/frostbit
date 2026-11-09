import test from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';
import { build } from 'esbuild';
import { getCompressedPubKey } from '../lib/crypto.js';

                                                                                           
const output = await build({
    stdin: {
        contents: 'export { buildAndSign, buildAndSignTransferTx } from "./lib/proto.js"; export { pb } from "./lib/data_proto.js"; export { util } from "protobufjs/minimal";',
        resolveDir: fileURLToPath(new URL('..', import.meta.url)),
    },
    bundle: true, platform: 'node', format: 'cjs', write: false,
});
const compiled = { exports: {} };
new Function('require', 'module', 'exports', output.outputFiles[0].text)(createRequire(import.meta.url), compiled, compiled.exports);
const { buildAndSign, buildAndSignTransferTx, pb, util } = compiled.exports;
                                                              
const rawPrivKey = new Uint8Array(32); rawPrivKey[31] = 1;
const signer = {
    fromAddress: 'fbc1qofflinefixture', nonce: 0, rawPrivKey,
    pubKeyCompressed: getCompressedPubKey(rawPrivKey), fee: '1000', chainId: 1,
};
const transfer = { type: 'transaction', to: 'fbc1qofflinereceiver', tokenAddress: 'FB', amount: '100000' };
const directTransfer = nonce => buildAndSignTransferTx({ ...signer, nonce, toAddress: transfer.to, tokenAddress: transfer.tokenAddress, amount: transfer.amount });
const uint64 = value => util.Long.fromString(value, true);

                                                                               
                                                                           
const ZERO_NONCE_GO_TX_ID = '0x88c509be80423f48ee522c9de229829f63c49feb089592a21d67c554f149e9ec';

test('zero nonce in number, decimal text and Long forms matches the Go canonical ID', () => {
    for (const nonce of [0, '0', '00', '000', uint64('0')]) {
        for (const bytes of [directTransfer(nonce), buildAndSign(transfer, { ...signer, nonce })]) {
            const tx = pb.AnyTx.decode(bytes).transaction;
            assert.equal(tx.base.txId, ZERO_NONCE_GO_TX_ID, `nonce ${typeof nonce}: ${nonce}`);
            assert.equal(tx.base.nonce.toString(), '0');
            assert.equal(Object.hasOwn(tx.base, 'nonce'), false, 'proto3 default must not be on the wire');
        }
    }
});

test('nonzero nonce representations produce the same signed bytes', () => {
    const expected = directTransfer(1);
    for (const nonce of ['1', uint64('1')]) {
        assert.deepEqual(directTransfer(nonce), expected);
        assert.deepEqual(buildAndSign(transfer, { ...signer, nonce }), expected);
    }
});

test('uint64 nonces above the JS safe-integer range keep their exact value', () => {
    for (const value of ['9007199254740993', '18446744073709551615']) {
        const bytes = directTransfer(value);
        assert.equal(pb.AnyTx.decode(bytes).transaction.base.nonce.toString(), value);
        assert.deepEqual(directTransfer(uint64(value)), bytes);
    }
});

test('signing leaves caller-owned Long values intact, including zero limbs', () => {
    for (const value of ['0', '1', '4294967296', '9007199254740993', '18446744073709551615']) {
        const nonce = uint64(value);
        const original = { ...nonce };
        directTransfer(nonce);
        assert.deepEqual({ ...nonce }, original, `mutated Long ${value}`);
        assert.equal(nonce.toString(), value);
        assert.deepEqual(directTransfer(Object.freeze(uint64(value))), directTransfer(value));
    }
});

test('literal string zero amounts and fees remain present in signed protobuf', () => {
    for (const value of ['0', '00', '000']) {
        const tx = pb.AnyTx.decode(buildAndSign({ ...transfer, amount: value }, { ...signer, fee: value, nonce: '0' })).transaction;
        assert.equal(tx.amount, value);
        assert.equal(tx.base.fee, value);
        assert.equal(Object.hasOwn(tx, 'amount'), true);
        assert.equal(Object.hasOwn(tx.base, 'fee'), true);
        assert.deepEqual([...tx.base.publicKeys.keys[1]], [...signer.pubKeyCompressed]);
    }
});

test('the generic non-transfer signing path also omits text zero nonce', () => {
    const desc = { type: 'set_referrer', referrerAddress: 'fbc1qreferrer' };
    const expected = buildAndSign(desc, { ...signer, nonce: 0 });
    for (const nonce of ['0', '00', '000', uint64('0')]) {
        const bytes = buildAndSign(desc, { ...signer, nonce });
        assert.deepEqual(bytes, expected);
        assert.equal(Object.hasOwn(pb.AnyTx.decode(bytes).setReferrerTx.base, 'nonce'), false);
    }
});

test('witness round zero has the same canonical encoding as an omitted proto3 integer', () => {
    const desc = { type: 'witness_vote', requestId: 'request-1', vote: 'PASS' };
    const expected = buildAndSign({ ...desc, round: 0 }, signer);
    const bytes = buildAndSign({ ...desc, round: '0' }, signer);
    assert.deepEqual(bytes, expected);
    assert.equal(Object.hasOwn(pb.AnyTx.decode(bytes).witnessVoteTx.vote, 'round'), false);
});

for (const field of ['nativeVout', 'vaultId', 'depositEventIndex', 'depositProtocol']) {
    test(`recharge ${field} text zero matches numeric zero without altering string fees`, () => {
        const desc = {
            type: 'recharge_request', chain: 'ETH', nativeTxHash: `0x${'aa'.repeat(32)}`,
            receiverAddress: 'fbc1qreceiver', amount: '100', rechargeFee: '0',
        };
        const expected = buildAndSign({ ...desc, [field]: 0 }, signer);
        const bytes = buildAndSign({ ...desc, [field]: '0' }, signer);
        assert.deepEqual(bytes, expected);
        const tx = pb.AnyTx.decode(bytes).witnessRequestTx;
        assert.equal(Object.hasOwn(tx, field), false);
        assert.equal(tx.rechargeFee, '0');
    });
}
