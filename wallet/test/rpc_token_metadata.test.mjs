import assert from 'node:assert/strict';
import test from 'node:test';
import { GENESIS_ASSET_METADATA } from '../../shared/assetMetadata.mjs';
import { tokenDecimals, tokenDisplayLabel } from '../lib/asset_metadata.js';

const explorerURL = 'https://explorer.test';
const nodeAddress = 'node-a:6000';
const rentStatus = { available_fb: '1000000', spendable_fb: '250000', pending_rent_fb: '750000', unpaid_rent_fb: '0', as_of_height: 12345 };

let storageState = { explorerUrl: explorerURL, nodeAddr: nodeAddress };

globalThis.chrome = {
    storage: {
        local: {
            async get() {
                return { ...storageState };
            },
        },
    },
};

const { enrichBalanceTokenMetadata, getAccountBalances } = await import('../lib/rpc.js');

test('balance metadata queries only held Token IDs and batches them by 256', async (t) => {
    const originalFetch = globalThis.fetch;
    const tokenIDs = Array.from({ length: 257 }, (_, index) => `token-${index}`);
    const balances = Object.fromEntries(tokenIDs.map(address => [address, { amount: '1' }]));
    const tokenBatches = [];

    globalThis.fetch = async (input, init = {}) => {
        const url = new URL(String(input));
        if (url.pathname === '/api/address') {
            assert.equal(init.method, 'POST');
            assert.deepEqual(JSON.parse(String(init.body)), { node: nodeAddress, address: 'fbc1qholder' });
            return jsonResponse({ account: { balances, rent_status: rentStatus } });
        }
        if (url.pathname === '/api/tokens') {
            assert.equal(url.searchParams.get('node'), nodeAddress);
            const addresses = (url.searchParams.get('addresses') || '').split(',').filter(Boolean);
            tokenBatches.push(addresses);
            return jsonResponse({
                tokens: addresses.map((address, index) => ({ address, symbol: address, decimals: index % 18 })),
            });
        }
        assert.fail(`unexpected request: ${url}`);
    };
    t.after(() => { globalThis.fetch = originalFetch; });

    const result = await getAccountBalances('fbc1qholder');

    assert.deepEqual(tokenBatches, []);
    assert.equal(result.balances['token-0'].amount, '1');
    assert.deepEqual(result.rent_status, rentStatus);

    const enriched = await enrichBalanceTokenMetadata(result.balances);

    assert.deepEqual(tokenBatches.map(batch => batch.length), [256, 1]);
    assert.deepEqual(tokenBatches.flat(), tokenIDs);
    assert.equal(enriched['token-0'].address, 'token-0');
    assert.equal(enriched['token-256'].address, 'token-256');
});

test('node discovery failure is reported instead of querying an empty node', async (t) => {
    const originalFetch = globalThis.fetch;
    const originalStorage = storageState;
    storageState = { explorerUrl: explorerURL, nodeAddr: '' };
    const paths = [];
    globalThis.fetch = async input => {
        paths.push(new URL(String(input)).pathname);
        return { ok: false, status: 503, async text() { return 'unavailable'; } };
    };
    t.after(() => { globalThis.fetch = originalFetch; storageState = originalStorage; });

    await assert.rejects(getAccountBalances('fbc1qholder'), /Unable to discover a DEX node: HTTP 503/);
    assert.deepEqual(paths, ['/api/nodes']);
});

test('Explorer balance errors are not converted to an empty balance', async (t) => {
    const originalFetch = globalThis.fetch;
    const originalStorage = storageState;
    storageState = { explorerUrl: explorerURL, nodeAddr: nodeAddress };
    globalThis.fetch = async () => jsonResponse({ error: 'account backend unavailable' });
    t.after(() => { globalThis.fetch = originalFetch; storageState = originalStorage; });

    await assert.rejects(getAccountBalances('fbc1qholder'), /account backend unavailable/);
});

test('rent status is required and never replaced with the recorded FB balance', async (t) => {
    const originalFetch = globalThis.fetch;
    t.after(() => { globalThis.fetch = originalFetch; });
    for (const invalid of [undefined, null, {}, { ...rentStatus, spendable_fb: '-1' }, { ...rentStatus, pending_rent_fb: 1 }, { ...rentStatus, as_of_height: NaN }]) {
        globalThis.fetch = async () => jsonResponse({ account: { balances: { FB: { balance: '999999999' } }, rent_status: invalid } });
        await assert.rejects(getAccountBalances('fbc1qholder'), /invalid account rent status/);
    }
});

test('a zero spendable FB snapshot preserves pending and unpaid rent and its exact height', async (t) => {
    const originalFetch = globalThis.fetch;
    t.after(() => { globalThis.fetch = originalFetch; });
    const status = { ...rentStatus, spendable_fb: '0', unpaid_rent_fb: '999999999999999999999', as_of_height: '9007199254740993' };
    globalThis.fetch = async () => jsonResponse({ account: { balances: { FB: { balance: '1000000' } }, rent_status: status } });
    const result = await getAccountBalances('fbc1qholder');
    assert.deepEqual(result.rent_status, status);
    assert.equal(result.balances.FB.balance, '1000000');
});

test('RPC identity-only tokens resolve known labels without returning symbols or precision', async (t) => {
    const originalFetch = globalThis.fetch;
    t.after(() => { globalThis.fetch = originalFetch; });
    const identityTokens = GENESIS_ASSET_METADATA.map(({ metadata, ...identity }) => ({ ...identity, type: 'bridged' }));
    const balances = Object.fromEntries(identityTokens.map(token => [token.address, { balance: '1000000', decimals: 8, symbol: 'WRONG' }]));
    globalThis.fetch = async input => {
        assert.equal(new URL(String(input)).pathname, '/api/tokens');
        return jsonResponse({ tokens: identityTokens });
    };
    const result = await enrichBalanceTokenMetadata(balances);
    for (const token of GENESIS_ASSET_METADATA) {
        assert.equal(result[token.address].address, token.address);
        assert.equal(tokenDisplayLabel(token.address, result[token.address]), token.metadata.icon_asset);
        assert.equal(tokenDecimals(token.address, result[token.address]), token.metadata.decimals);
    }
});

test('RPC metadata does not match held IDs against another token symbol or invent missing decimals', async (t) => {
    const originalFetch = globalThis.fetch;
    t.after(() => { globalThis.fetch = originalFetch; });
    globalThis.fetch = async () => jsonResponse({ tokens: [
        { address: 'custom-id', symbol: 'victim-id', decimals: 18, native_chain: 'eth', native_contract: '0x1' },
        { address: 'no-precision', native_chain: 'base', native_contract: '0x2' },
    ] });
    const result = await enrichBalanceTokenMetadata({
        'victim-id': { balance: '1' },
        'custom-id': { balance: '2' },
        'no-precision': { balance: '3', address: 'no-precision', decimals: 8 },
    });
    assert.equal(tokenDecimals('victim-id', result['victim-id']), null);
    assert.equal(result['victim-id'].address, undefined);
    assert.equal(tokenDecimals('custom-id', result['custom-id']), 18);
    assert.equal(result['no-precision'].decimals, undefined);
    assert.equal(tokenDecimals('no-precision', result['no-precision']), null);
});

function jsonResponse(data) {
    return {
        ok: true,
        async json() { return data; },
        async text() { return JSON.stringify(data); },
    };
}
