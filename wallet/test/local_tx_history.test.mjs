import test from 'node:test';
import assert from 'node:assert/strict';
import {
    LOCAL_WITHDRAW_PROOF_MAX_BYTES,
    signedWithdrawProofForHistory,
} from '../lib/local_tx_history.js';

test('local history retains a bounded signed proof only for Frost withdraw requests', () => {
    const proof = Uint8Array.from([1, 2, 160, 255]);
    assert.equal(
        signedWithdrawProofForHistory({ type: 'frost_withdraw' }, proof),
        Buffer.from(proof).toString('base64'),
    );
    assert.equal(signedWithdrawProofForHistory({ type: 'transaction' }, proof), '');
    assert.equal(signedWithdrawProofForHistory({ type: 'frost_withdraw_cancel' }, proof), '');
});

test('local history omits empty or oversized signed withdraw proofs', () => {
    assert.equal(signedWithdrawProofForHistory({ type: 'frost_withdraw' }, new Uint8Array()), '');
    assert.equal(
        signedWithdrawProofForHistory(
            { type: 'frost_withdraw' },
            new Uint8Array(LOCAL_WITHDRAW_PROOF_MAX_BYTES + 1),
        ),
        '',
    );
});
