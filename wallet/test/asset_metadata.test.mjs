import assert from 'node:assert/strict';
import test from 'node:test';
import {
    GENESIS_ASSET_METADATA, builtinAssetForAddress, builtinAssetForNativeIdentity,
} from '../../shared/assetMetadata.mjs';
import {
    attachTokenMetadata, mergeBalanceTokenMetadata, tokenDecimals, tokenDisplayLabel,
    tokenDisplayTitle, toRawAmount, formatRawAmount,
} from '../lib/asset_metadata.js';

const assetID = label => GENESIS_ASSET_METADATA.find(asset => asset.metadata.icon_asset === label).address;

test('all fifteen canonical assets and FB are labeled and scaled before any API response', () => {
    const expected = ['FB', 'BTC', 'ETH', 'BNB', 'BASE.ETH', 'ARB.ETH', 'TRX', 'ETH.USDT', 'ETH.USDC', 'TRX.USDT', 'BASE.USDC', 'ARB.USDT', 'ARB.USDC', 'ETH.DAI', 'BNB.USDC', 'BNB.USDT'];
    assert.deepEqual(GENESIS_ASSET_METADATA.map(asset => tokenDisplayLabel(asset.address)), expected);
    for (const asset of GENESIS_ASSET_METADATA) {
        const wrongCache = { address: asset.address, symbol: 'WRONG', decimals: 8, native_chain: 'wrong-chain' };
        assert.equal(tokenDisplayLabel(asset.address, wrongCache), asset.metadata.icon_asset);
        assert.equal(tokenDecimals(asset.address, wrongCache), asset.metadata.decimals);
        assert.equal(formatRawAmount(`1${'0'.repeat(asset.metadata.decimals)}`, tokenDecimals(asset.address, wrongCache)), '1');
        assert.ok(tokenDisplayTitle(asset.address, wrongCache).includes(asset.address));
    }
});

test('ETH.USDT, BNB.USDT and FB use their own raw precision without altering token identity', () => {
    for (const [label, raw] of [['ETH.USDT', '1000000'], ['BNB.USDT', '1000000000000000000'], ['FB', '100000000']]) {
        const tokenAddress = assetID(label);
        const txDesc = { tokenAddress, amount: toRawAmount('1', tokenDecimals(tokenAddress)) };
        assert.deepEqual(txDesc, { tokenAddress: assetID(label), amount: raw });
    }
    assert.throws(() => toRawAmount('1.0000001', 6), /exceeds asset precision/);
    assert.equal(toRawAmount('1.0000000', 6), '1000000');
    assert.equal(toRawAmount('.12', 6), '120000');
    assert.equal(formatRawAmount('1234', 0), '1,234');
});

test('actual Explorer identity-only responses clear old defaults and use the shared metadata table', () => {
    const balances = Object.fromEntries(GENESIS_ASSET_METADATA.map(asset => [asset.address, { balance: '1000000', symbol: 'WRONG', decimals: 8 }]));
    const identities = GENESIS_ASSET_METADATA.map(({ metadata, ...asset }) => ({ ...asset, type: 'bridged' }));
    const enriched = attachTokenMetadata(balances, identities);
    assert.deepEqual(Object.keys(enriched), Object.keys(balances));
    for (const asset of GENESIS_ASSET_METADATA) {
        assert.equal(enriched[asset.address].address, asset.address);
        assert.equal(enriched[asset.address].display_label, asset.metadata.icon_asset);
        assert.equal(enriched[asset.address].decimals, asset.metadata.decimals);
        assert.equal(balances[asset.address].symbol, 'WRONG');
    }
});

test('symbols never select another asset metadata, including a symbol equal to a held ID', () => {
    const balances = { USDT: { balance: '1' }, customA: { balance: '1' }, customB: { balance: '1' } };
    const enriched = attachTokenMetadata(balances, [
        { address: 'customA', symbol: 'USDT', decimals: 6, native_chain: 'eth', native_contract: '0x1' },
        { address: 'customB', symbol: 'USDT', decimals: 18, native_chain: 'eth', native_contract: '0x2' },
    ]);
    assert.equal(tokenDecimals('USDT', enriched.USDT), null);
    assert.equal(enriched.USDT.address, undefined);
    assert.equal(tokenDecimals('customA', enriched.customA), 6);
    assert.equal(tokenDecimals('customB', enriched.customB), 18);
    assert.equal(tokenDisplayLabel('customA', enriched.customA), 'ETH.USDT · customA');
    assert.equal(tokenDisplayLabel('customB', enriched.customB), 'ETH.USDT · customB');
    assert.equal(tokenDecimals('customB', enriched.customA), null);
});

test('unknown or invalid precision stays raw and cannot become an ordinary transfer', () => {
    const id = 'br_unknown_123456789012345678901234567890';
    for (const decimals of [undefined, null, '', true, -1, 1.5, '1.5', 256, Infinity]) {
        const enriched = attachTokenMetadata({ [id]: { balance: '1000000', decimals: 8, symbol: 'OLD' } }, [
            { address: id, native_chain: 'eth', native_contract: '0xunknown', decimals },
        ])[id];
        assert.equal(tokenDecimals(id, enriched), null);
        assert.equal(formatRawAmount('1000000', tokenDecimals(id, enriched)), '1000000 raw');
        assert.throws(() => toRawAmount('1', tokenDecimals(id, enriched)), /unknown asset precision/);
        assert.equal(tokenDisplayLabel(id, enriched), 'ETH · br_unknow…567890');
    }
                                                                           
                                                                            
    assert.equal(tokenDecimals(id, { address: id, symbol: 'OLD', decimals: 8 }), null);
});

test('native identity normalization is shared, and TRON contracts retain case', () => {
    const bnbUSDT = builtinAssetForAddress(assetID('BNB.USDT'));
    assert.equal(builtinAssetForNativeIdentity('bsc', bnbUSDT.native_contract.toUpperCase()), bnbUSDT);
    const arbUSDT = builtinAssetForAddress(assetID('ARB.USDT'));
    assert.equal(builtinAssetForNativeIdentity('arbitrum-one', arbUSDT.native_contract), arbUSDT);
    const trxUSDT = builtinAssetForAddress(assetID('TRX.USDT'));
    assert.equal(builtinAssetForNativeIdentity('TRX', trxUSDT.native_contract), trxUSDT);
    assert.equal(builtinAssetForNativeIdentity('trx', trxUSDT.native_contract.toLowerCase()), undefined);

    const token = 'br_dynamic_uni';
    const meta = attachTokenMetadata({ [token]: { balance: '1000000000000000000' } }, [
        { address: token, native_chain: 'ETH', native_contract: '0x1f9840a85d5aF5bf1D1762F925BDADdC4201F984' },
    ])[token];
    assert.equal(tokenDisplayLabel(token, meta), 'ETH.UNI · br_dynamic_uni');
    assert.equal(tokenDecimals(token, meta), 18);
});

test('balance-only refresh and JSON cache restore preserve exact dynamic metadata without stale balances', () => {
    const id = 'br_dynamic';
    const enriched = attachTokenMetadata({ [id]: { balance: '1000000' } }, [
        { address: id, native_chain: 'ARB', native_contract: '0x123', symbol: 'TEST', decimals: '6' },
    ]);
    const restored = JSON.parse(JSON.stringify(enriched));
    const refreshed = mergeBalanceTokenMetadata({ [id]: { balance: '2000000' } }, restored);
    assert.equal(refreshed[id].balance, '2000000');
    assert.equal(refreshed[id].native_contract, '0x123');
    assert.equal(refreshed[id].metadata_address, id);
    assert.equal(tokenDecimals(id, refreshed[id]), 6);
    assert.equal(tokenDisplayLabel(id, refreshed[id]), 'ARB.TEST · br_dynamic');
    const missingPrecision = attachTokenMetadata(refreshed, [{ address: id, native_chain: 'arb', native_contract: '0x123' }]);
    assert.equal(tokenDecimals(id, mergeBalanceTokenMetadata(missingPrecision, refreshed)[id]), null);
    assert.deepEqual(mergeBalanceTokenMetadata({}, refreshed), {});
});

test('backend icon URL follows only the exact held AssetID and survives a balance refresh', () => {
    const first = `br_${'a'.repeat(64)}`;
    const second = `br_${'b'.repeat(64)}`;
    const icon = `/api/token-icon?node=node-a&addr=${first}`;
    const enriched = attachTokenMetadata({ [first]: { balance: '1' }, [second]: { balance: '2' } }, [
        { address: first, native_chain: 'eth', icon_url: icon },
        { address: second, native_chain: 'eth', icon_url: icon },
    ]);
    assert.equal(enriched[first].icon_url, icon);
    assert.equal(enriched[second].icon_url, undefined);
    assert.equal(mergeBalanceTokenMetadata({ [first]: { balance: '3' } }, enriched)[first].icon_url, icon);
});
