import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';
import { build } from 'esbuild';
import { parse, compileScript, compileTemplate } from '@vue/compiler-sfc';
import { createSSRApp } from 'vue';
import { renderToString } from '@vue/server-renderer';
import { GENESIS_ASSET_METADATA } from '../../shared/assetMetadata.mjs';

let stored = { explorerUrl: 'https://explorer.test', nodeAddr: 'node-a:6000' };
let reply = (_message, callback) => callback({});
globalThis.localStorage = { getItem: () => 'zh', setItem() {} };
globalThis.document = { body: { classList: { toggle() {} } } };
globalThis.chrome = {
    runtime: { onMessage: { addListener() {} }, sendMessage: (message, callback) => reply(message, callback) },
    alarms: { onAlarm: { addListener() {} } },
    storage: { local: {
        async get() { return structuredClone(stored); },
        async set(value) { Object.assign(stored, structuredClone(value)); },
    } },
};

async function compile(contents, directory, loader = 'js') {
    const output = await build({
        stdin: { contents, resolveDir: fileURLToPath(new URL(directory, import.meta.url)), loader },
        bundle: true, platform: 'node', format: 'cjs', write: false, external: ['vue'],
    });
    const compiled = { exports: {} };
    new Function('require', 'module', 'exports', output.outputFiles[0].text)(createRequire(import.meta.url), compiled, compiled.exports);
    return compiled.exports;
}

const source = await readFile(new URL('../src/App.vue', import.meta.url), 'utf8');
const { descriptor } = parse(source);
const script = compileScript(descriptor, { id: 'rent-test' });
const template = compileTemplate({ source: descriptor.template.content, id: 'rent-test', filename: 'App.vue', compilerOptions: { bindingMetadata: script.bindings } });
const compiledApp = await compile(`${script.content}\n${template.code}`, '../src/', 'ts');
compiledApp.default.render = compiledApp.render;
const workerSource = await readFile(new URL('../background/service_worker.js', import.meta.url), 'utf8');
const worker = await compile(`${workerSource}\nexport { saveCachedBalances, getCachedBalances, checkSpendableBalance };`, '../background/');

const status = { available_fb: '1000000', spendable_fb: '250000', pending_rent_fb: '750000', unpaid_rent_fb: '0', as_of_height: 12345 };
const usdt = GENESIS_ASSET_METADATA.find(asset => asset.metadata.icon_asset === 'ETH.USDT').address;
const snapshot = () => ({ balances: { FB: { balance: '1000000' }, [usdt]: { balance: '1000000' } }, rent_status: { ...status }, updatedAt: 123456 });

async function appState(prepare = () => {}) {
    let state;
    const app = createSSRApp({ ...compiledApp.default, setup(props, ctx) {
        state = compiledApp.default.setup(props, ctx);
        prepare(state);
        return state;
    } });
    const html = await renderToString(app);
    return { state, html };
}

test('wallet renders net FB plus recorded, pending, unpaid and height from the same snapshot', async () => {
    const { state, html } = await appState(state => {
        state.applyBalanceSnapshot(snapshot(), true);
        state.page.value = 'main';
    });
    assert.equal(state.fbBalance(), '0.0025');
    assert.match(html, /可花费余额/);
    for (const text of ['账面可用 FB', '待结算租金', '欠付租金', '余额对应区块', '12345', '0.0075 FB']) assert.ok(html.includes(text), text);
    assert.match(state.balanceStatusText.value, /上次余额/);
    assert.throws(() => state.applyBalanceSnapshot({ balances: snapshot().balances }, false), /租金余额/);
    state.resetAccountScopedData();
    assert.equal(state.fbBalance(), '—');
    assert.equal(state.rentStatus.value, null);
});

test('transfer checks amount plus fee against spendable FB and blocks unavailable rent snapshots', async () => {
    const { state } = await appState();
    state.applyBalanceSnapshot(snapshot(), false);
    state.sendToken.value = 'FB';
    state.sendTo.value = 'fbc1qreceiver';
    state.sendNonce.value = '0';
    state.sendFee.value = '1000';
    state.sendAmount.value = '0.00249';
    assert.equal(state.canSubmitSend.value, true);
    state.sendAmount.value = '0.0025';
    assert.equal(state.canSubmitSend.value, false);
    assert.match(state.sendBalanceError.value, /可花费 FB/);
    state.sendToken.value = usdt;
    state.sendAmount.value = '1';
    state.rentStatus.value = { ...status, spendable_fb: '0' };
    assert.match(state.sendBalanceError.value, /可花费 FB/);
    state.rentStatus.value = null;
    assert.equal(state.canSubmitSend.value, false);
    assert.match(state.sendBalanceError.value, /租金余额/);
});

test('approval checks fees and challenge stake against spendable FB and rejects failed balance loads', async () => {
    const { state } = await appState();
    state.accounts.value = ['fbc1qsigner'];
    state.approveAccountIdx.value = 0;
    state.pendingRequests.value = [{ id: 1, type: 'SEND_TX', txDesc: { type: 'set_referrer' } }];
    state.approveFee.value = '250001';
    reply = (_message, callback) => callback(snapshot());
    await state.loadApproveBalances();
    assert.equal(state.approveSignerFbDisplay.value, '0.0025');
    assert.match(state.approveBalanceCheckError.value, /可花费 FB/);
    state.approveFee.value = '1000';
    assert.equal(state.approveBalanceCheckError.value, '');
    state.pendingRequests.value[0].txDesc = { type: 'witness_challenge', stakeAmount: '250000' };
    assert.equal(state.challengeBalanceEnough.value, false);
    assert.match(state.approveBalanceCheckError.value, /可花费 FB/);
    reply = (_message, callback) => callback({ error: 'offline' });
    await state.loadApproveBalances();
    assert.equal(state.approveRentStatus.value, null);
    assert.match(state.approveBalanceCheckError.value, /offline/);
});

test('late approval balance responses cannot replace the selected account snapshot', async () => {
    const { state } = await appState();
    state.accounts.value = ['fbc1qfirst', 'fbc1qsecond'];
    state.pendingRequests.value = [{ id: 1, type: 'SEND_TX', txDesc: { type: 'set_referrer' } }];
    const pending = [];
    reply = (message, callback) => pending.push({ message, callback });
    state.approveAccountIdx.value = 0;
    const first = state.loadApproveBalances();
    state.approveAccountIdx.value = 1;
    const second = state.loadApproveBalances();
    pending[1].callback({ ...snapshot(), rent_status: { ...status, spendable_fb: '2' } });
    await second;
    pending[0].callback(snapshot());
    await first;
    assert.equal(state.approveRentStatus.value.spendable_fb, '2');
});

test('balance cache restores rent status and ignores late metadata for an older snapshot', async () => {
    const initial = snapshot();
    await worker.saveCachedBalances('fbc1qholder', initial.balances, initial.rent_status, 1);
    assert.deepEqual((await worker.getCachedBalances('fbc1qholder')).rent_status, status);
    const fresh = { ...status, spendable_fb: '1', as_of_height: 12346 };
    await worker.saveCachedBalances('fbc1qholder', initial.balances, fresh, 2);
    await worker.saveCachedBalances('fbc1qholder', initial.balances, status, 1, true);
    const cached = await worker.getCachedBalances('fbc1qholder');
    assert.equal(cached.updatedAt, 2);
    assert.deepEqual(cached.rent_status, fresh);
    await worker.saveCachedBalances('fbc1qholder', initial.balances, undefined, 3);
    assert.equal(await worker.getCachedBalances('fbc1qholder'), null);
});

test('background checks a fresh rent snapshot before transfer or approval signing', async t => {
    const originalFetch = globalThis.fetch;
    t.after(() => { globalThis.fetch = originalFetch; });
    globalThis.fetch = async () => ({ ok: true, async json() { return { account: { balances: snapshot().balances, rent_status: status } }; } });
    await worker.checkSpendableBalance('fbc1qholder', { type: 'transaction', tokenAddress: 'FB', amount: '249000' }, '1000');
    await assert.rejects(worker.checkSpendableBalance('fbc1qholder', { type: 'transaction', tokenAddress: 'FB', amount: '250000' }, '1000'), /spendable FB/);
    await assert.rejects(worker.checkSpendableBalance('fbc1qholder', { type: 'set_referrer' }, '250001'), /spendable FB/);
    await assert.rejects(worker.checkSpendableBalance('fbc1qholder', { type: 'witness_challenge', stakeAmount: '250000' }, '1000'), /spendable FB/);
});
