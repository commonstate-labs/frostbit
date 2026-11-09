import test from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';
import { build } from 'esbuild';
import { getCompressedPubKey } from '../lib/crypto.js';

                                                                                 
                                                                               
const output = await build({ stdin: { contents: 'export { buildAndSign } from "./lib/proto.js"; export { pb } from "./lib/data_proto.js";', resolveDir: fileURLToPath(new URL('..', import.meta.url)) }, bundle: true, platform: 'node', format: 'cjs', write: false });
const compiled = { exports: {} };
new Function('require', 'module', 'exports', output.outputFiles[0].text)(createRequire(import.meta.url), compiled, compiled.exports);
const { buildAndSign, pb } = compiled.exports;
const rawPrivKey = new Uint8Array(32); rawPrivKey[31] = 1;
const signer = { fromAddress: 'fbc1qtest', nonce: 1, rawPrivKey, pubKeyCompressed: getCompressedPubKey(rawPrivKey), chainId: 51015 };

test('recharge signature includes unified vault protocol and receipt event identity', () => {
    const desc = { type: 'recharge_request', chain: 'ETH', nativeTxHash: `0x${'aa'.repeat(32)}`, receiverAddress: 'fbc1qreceiver', amount: '100', vaultId: 7, depositEventIndex: 2, depositProtocol: 2 };
    const encoded = buildAndSign(desc, signer);
    const tx = pb.AnyTx.decode(encoded).witnessRequestTx;
    assert.equal(tx.vaultId, 7);
    assert.equal(tx.depositEventIndex, 2);
    assert.equal(tx.depositProtocol, 2);
    for (const field of ['vaultId', 'depositEventIndex', 'depositProtocol']) {
        const modified = pb.AnyTx.decode(buildAndSign({ ...desc, [field]: desc[field] + 1 }, signer)).witnessRequestTx;
        assert.notEqual(modified.base.txId, tx.base.txId);
        assert.notDeepEqual(modified.base.signature, tx.base.signature);
    }
});

test('editable decimal nonce is encoded as the signed transaction nonce', () => {
    const encoded = buildAndSign(
        { type: 'transaction', to: 'fbc1qreceiver', tokenAddress: 'FB', amount: '1' },
        { ...signer, nonce: '42' },
    );
    const tx = pb.AnyTx.decode(encoded).transaction;
    assert.equal(Number(tx.base.nonce), 42);
});
