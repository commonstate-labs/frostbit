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
    fromAddress: 'fbc1qcancelrequestowner',
    nonce: 7,
    rawPrivKey,
    pubKeyCompressed: getCompressedPubKey(rawPrivKey),
    fee: '1000',
    chainId: 1,
};

test('withdraw cancel signs a normal account transaction with the target request id', () => {
    const encoded = buildAndSign({
        type: 'frost_withdraw_cancel',
        withdrawId: 'withdraw-123',
    }, signer);
    const decoded = pb.AnyTx.decode(encoded).frostWithdrawCancelTx;

    assert.equal(decoded.withdrawId, 'withdraw-123');
    assert.equal(decoded.base.fromAddress, signer.fromAddress);
    assert.equal(decoded.base.nonce.toString(), '7');
    assert.equal(decoded.base.fee, '1000');
    assert.equal(decoded.base.chainId.toString(), '1');
    assert.ok(decoded.base.txId.startsWith('0x'));
    assert.ok(decoded.base.signature.length > 0);
    assert.equal(decoded.originalRequestTx.length, 0);

    const other = pb.AnyTx.decode(buildAndSign({
        type: 'frost_withdraw_cancel',
        withdrawId: 'withdraw-456',
    }, signer)).frostWithdrawCancelTx;
    assert.notEqual(other.base.txId, decoded.base.txId);
    assert.notDeepEqual(other.base.signature, decoded.base.signature);
});

test('withdraw cancel accepts an optional legacy signed-request proof as hex or typed bytes', () => {
    const encoded = buildAndSign({
        type: 'frost_withdraw_cancel',
        withdrawId: 'legacy-withdraw',
        originalRequestTx: '0x0102a0ff',
    }, signer);
    const decoded = pb.AnyTx.decode(encoded).frostWithdrawCancelTx;

    assert.deepEqual([...decoded.originalRequestTx], [1, 2, 160, 255]);
    const withoutProof = pb.AnyTx.decode(buildAndSign({
        type: 'frost_withdraw_cancel',
        withdrawId: 'legacy-withdraw',
    }, signer)).frostWithdrawCancelTx;
    assert.notEqual(withoutProof.base.txId, decoded.base.txId);
    assert.notDeepEqual(withoutProof.base.signature, decoded.base.signature);

    const typedArrayProof = pb.AnyTx.decode(buildAndSign({
        type: 'frost_withdraw_cancel',
        withdrawId: 'typed-array-proof',
        originalRequestTx: Uint8Array.from([9, 8, 7]),
    }, signer)).frostWithdrawCancelTx;
    assert.deepEqual([...typedArrayProof.originalRequestTx], [9, 8, 7]);
});

test('withdraw cancel rejects an empty request id', () => {
    assert.throws(
        () => buildAndSign({ type: 'frost_withdraw_cancel' }, signer),
        /requires withdrawId/,
    );
});

test('withdraw cancel rejects an oversized legacy proof before signing', () => {
    assert.throws(
        () => buildAndSign({
            type: 'frost_withdraw_cancel',
            withdrawId: 'legacy-withdraw',
            originalRequestTx: new Uint8Array(16 * 1024 + 1),
        }, signer),
        /exceeds 16384 bytes/,
    );
});
