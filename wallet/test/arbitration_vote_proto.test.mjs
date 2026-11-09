import test from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';
import { build } from 'esbuild';
import { getCompressedPubKey } from '../lib/crypto.js';

const output = await build({
    stdin: { contents: 'export { buildAndSign } from "./lib/proto.js"; export { pb } from "./lib/data_proto.js";', resolveDir: fileURLToPath(new URL('..', import.meta.url)) },
    bundle: true, platform: 'node', format: 'cjs', write: false,
});
const compiled = { exports: {} };
new Function('require', 'module', 'exports', output.outputFiles[0].text)(createRequire(import.meta.url), compiled, compiled.exports);
const { buildAndSign, pb } = compiled.exports;
const rawPrivKey = new Uint8Array(32); rawPrivKey[31] = 1;
const signer = { fromAddress: 'fbc1qtest', nonce: 1, rawPrivKey, pubKeyCompressed: getCompressedPubKey(rawPrivKey), chainId: 51015 };
const description = { type: 'arbitration_vote', challengeId: 'case-1', requestId: 'request-1', ballotRound: '7', vote: 'PASS' };
const decode = desc => pb.AnyTx.decode(buildAndSign(desc, signer)).arbitrationVoteTx;

test('arbitration signs the same exact uint64 ballot in the transaction and inner vote', () => {
    for (const round of ['1', '9007199254740993', '18446744073709551615']) {
        const tx = decode({ ...description, ballotRound: round });
        assert.equal(tx.ballotRound.toString(), round);
        assert.equal(tx.vote.ballotRound.toString(), round);
        assert.equal(tx.vote.requestId, description.requestId);
        assert.equal(tx.vote.witnessAddress, signer.fromAddress);
        assert.equal(tx.challengeId, description.challengeId);
        assert.ok(tx.base.signature.length > 0);
    }
});

test('changing ballot, case, request or direction changes the signed transaction', () => {
    const original = decode(description);
    for (const change of [{ ballotRound: '8' }, { challengeId: 'case-2' }, { requestId: 'request-2' }, { vote: 'FAIL' }]) {
        const changed = decode({ ...description, ...change });
        assert.notEqual(changed.base.txId, original.base.txId);
        assert.notDeepEqual(changed.base.signature, original.base.signature);
    }
});

test('arbitration direction is explicit and abstention cannot silently become PASS', () => {
    assert.equal(decode({ ...description, vote: 'PASS' }).vote.voteType, pb.WitnessVoteType.VOTE_PASS);
    assert.equal(decode({ ...description, vote: 'FAIL' }).vote.voteType, pb.WitnessVoteType.VOTE_FAIL);
    assert.equal(decode({ ...description, vote: 'ABSTAIN' }).vote.voteType, pb.WitnessVoteType.VOTE_ABSTAIN);
    for (const vote of ['', 'unknown', undefined, 0]) {
        assert.throws(() => decode({ ...description, vote }), /requires PASS, FAIL, or ABSTAIN/);
    }
});

test('invalid or imprecise ballot identity is rejected before signing', () => {
    for (const ballotRound of [undefined, null, '', '0', 0, -1, '1.5', '1e3', '01', '18446744073709551616', 9007199254740992]) {
        assert.throws(() => decode({ ...description, ballotRound }), /ballotRound/);
    }
    for (const missing of [{ challengeId: '' }, { requestId: '' }]) {
        assert.throws(() => decode({ ...description, ...missing }), /requires challengeId and requestId/);
    }
});
