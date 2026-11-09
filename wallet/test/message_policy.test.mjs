import test from 'node:test';
import assert from 'node:assert/strict';

import {
    isAllowedDappSender,
    isTrustedExtensionSender,
    sanitizeDappMessage,
} from '../lib/message_policy.js';

const runtimeId = 'abcdefghijklmnop';
const originAllowed = rawUrl => new URL(rawUrl).origin === 'https://app.public-network.invalid';

test('web pages cannot call approval or wallet administration methods', () => {
    for (const type of [
        'GET_PENDING_REQUESTS',
        'GET_SELECTED_ACCOUNT',
        'APPROVE_REQUEST',
        'REJECT_REQUEST',
        'CREATE_WALLET',
        'IMPORT_WALLET',
        'ADD_ACCOUNT',
        'IMPORT_PRIVATE_KEYS',
        'UNLOCK',
        'SET_SETTINGS',
        'RESET_WALLET',
    ]) {
        assert.throws(
            () => sanitizeDappMessage({ type }),
            /not available to web pages/,
            type,
        );
    }
});

test('transaction requests cannot inject approval-only fields', () => {
    const txDesc = { type: 'transaction', to: 'fbc1recipient', amount: '10' };
    assert.deepEqual(
        sanitizeDappMessage({
            type: 'SEND_TX',
            txDesc,
            fee: '0',
            accountIndex: 7,
        }),
        { type: 'SEND_TX', txDesc },
    );
});

test('message signing preserves the exact reviewed message', () => {
    assert.deepEqual(
        sanitizeDappMessage({ type: 'SIGN_MESSAGE', message: 'review me' }),
        { type: 'SIGN_MESSAGE', message: 'review me' },
    );
    assert.throws(
        () => sanitizeDappMessage({ type: 'SIGN_MESSAGE' }),
        /Invalid message/,
    );
});

test('account access uses an explicit public connection request', () => {
    assert.deepEqual(
        sanitizeDappMessage({ type: 'REQUEST_ACCOUNTS', ignored: true }),
        { type: 'REQUEST_ACCOUNTS' },
    );
    assert.deepEqual(
        sanitizeDappMessage({ type: 'GET_ACCOUNTS' }),
        { type: 'GET_ACCOUNTS' },
    );
});

test('extension UI and DApp senders are separate capabilities', () => {
    const popup = {
        id: runtimeId,
        url: `chrome-extension://${runtimeId}/popup/index.html`,
    };
    const dapp = {
        id: runtimeId,
        url: 'https://app.public-network.invalid/trade',
        tab: { id: 4, url: 'https://app.public-network.invalid/trade' },
    };

    assert.equal(isTrustedExtensionSender(popup, runtimeId), true);
    assert.equal(isAllowedDappSender(popup, runtimeId, originAllowed), false);
    assert.equal(isTrustedExtensionSender(dapp, runtimeId), false);
    assert.equal(isAllowedDappSender(dapp, runtimeId, originAllowed), true);
});

test('lookalike extension origins and non-whitelisted pages are rejected', () => {
    assert.equal(isTrustedExtensionSender({
        id: runtimeId,
        url: `https://${runtimeId}.example/popup/index.html`,
    }, runtimeId), false);

    assert.equal(isAllowedDappSender({
        id: runtimeId,
        url: 'https://evil.example/',
        tab: { id: 5, url: 'https://evil.example/' },
    }, runtimeId, originAllowed), false);
});
