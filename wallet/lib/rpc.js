   
             
                                                                                           
                                                 
   

import { toBase64 } from './crypto.js';
import { attachTokenMetadata } from './asset_metadata.js';
import {
    WALLET_ENV_CONFIGS_KEY, WALLET_ENV_KEY,
    mergeWalletEnvConfigs, normalizeWalletEnv,
} from './env.js';

const TIMEOUT_MS = 15_000;
const TOKEN_ADDRESS_BATCH_SIZE = 256;

async function getActiveEnvConfig() {
    const stored = await chrome.storage.local.get([WALLET_ENV_KEY, WALLET_ENV_CONFIGS_KEY, 'explorerUrl', 'nodeAddr']);
    const walletEnv = normalizeWalletEnv(stored[WALLET_ENV_KEY]);
    const envConfigs = mergeWalletEnvConfigs(stored[WALLET_ENV_CONFIGS_KEY]);
    if (!stored[WALLET_ENV_CONFIGS_KEY] && (stored.explorerUrl || stored.nodeAddr)) {
        envConfigs.dev = {
            explorerUrl: stored.explorerUrl || envConfigs.dev.explorerUrl,
            nodeAddr: stored.nodeAddr || envConfigs.dev.nodeAddr,
        };
    }
    return { walletEnv, ...envConfigs[walletEnv] };
}

async function getExplorerUrl() {
    const { explorerUrl } = await getActiveEnvConfig();
    return explorerUrl.replace(/\/+$/, '');
}

let _cachedNode = '';
let _cachedNodeKey = '';

async function getDefaultNode() {
    const config = await getActiveEnvConfig();
    if (config.nodeAddr) return config.nodeAddr;
    const cacheKey = `${config.walletEnv}:${config.explorerUrl}`;
                                                                                   
    if (_cachedNode && _cachedNodeKey === cacheKey) return _cachedNode;
    try {
        const base = config.explorerUrl.replace(/\/+$/, '');
        const res = await fetch(`${base}/api/nodes`, { signal: AbortSignal.timeout(3000) });
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const data = await res.json();
        const first = String(data.nodes?.[0] || '').trim();
        if (!first) throw new Error('Explorer returned no available nodes');
        _cachedNode = first;
        _cachedNodeKey = cacheKey;
        return first;
    } catch (e) {
        throw new Error(`Unable to discover a DEX node: ${e?.message || e}`);
    }
}

async function postJSON(path, body) {
    const base = await getExplorerUrl();
    const res = await fetch(`${base}${path}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
        signal: AbortSignal.timeout(TIMEOUT_MS),
    });
    if (!res.ok) throw new Error(`HTTP ${res.status}: ${await res.text()}`);
    return res.json();
}

async function getJSON(path) {
    const base = await getExplorerUrl();
    const res = await fetch(`${base}${path}`, { signal: AbortSignal.timeout(TIMEOUT_MS) });
    if (!res.ok) throw new Error(`HTTP ${res.status}: ${await res.text()}`);
    return res.json();
}

export async function getTokenList(node = '', addresses = []) {
    const uniqueAddresses = Array.from(new Set(
        (Array.isArray(addresses) ? addresses : [])
            .map(address => String(address || '').trim())
            .filter(Boolean),
    ));
    if (uniqueAddresses.length === 0) return [];
    const selectedNode = node || await getDefaultNode();

    const tokens = [];
    for (let offset = 0; offset < uniqueAddresses.length; offset += TOKEN_ADDRESS_BATCH_SIZE) {
        const params = new URLSearchParams();
        if (selectedNode) params.set('node', selectedNode);
        params.set('addresses', uniqueAddresses.slice(offset, offset + TOKEN_ADDRESS_BATCH_SIZE).join(','));
        const res = await getJSON(`/api/tokens?${params.toString()}`);
        if (res?.error) throw new Error(res.error);
        if (Array.isArray(res?.tokens)) tokens.push(...res.tokens);
    }
    return tokens;
}

   
                                             
                          
                                       
   
export async function getAccount(address) {
    const node = await getDefaultNode();
    const res = await postJSON('/api/wallet/nonce', { node, address });
    if (res?.error) throw new Error(res.error);
    return res;
}

export function isValidRentStatus(status) {
    if (!status || typeof status !== 'object' || Array.isArray(status)) return false;
    for (const field of ['available_fb', 'spendable_fb', 'pending_rent_fb', 'unpaid_rent_fb']) {
        if (typeof status[field] !== 'string' || !/^\d+$/.test(status[field])) return false;
    }
    const height = status.as_of_height;
    return (typeof height === 'number' && Number.isSafeInteger(height) && height >= 0)
        || (typeof height === 'string' && /^\d+$/.test(height));
}

   
                           
                                                
                                                        
   
export async function getAccountBalances(address) {
    const node = await getDefaultNode();
    const res = await postJSON('/api/address', { node, address });
    if (res?.error) throw new Error(res.error);
    if (!res?.account) {
        throw new Error('Explorer returned an invalid balance response');
    }
    const balances = res.account.balances ?? {};
    if (!balances || typeof balances !== 'object' || Array.isArray(balances)) throw new Error('Explorer returned an invalid balance response');
    const rent_status = res.account.rent_status;
    if (!isValidRentStatus(rent_status)) throw new Error('Explorer returned an invalid account rent status');
    return { balances, rent_status, updatedAt: Date.now() };
}

                                                                                            
export async function enrichBalanceTokenMetadata(balances) {
    const snapshot = Object.fromEntries(Object.entries(balances || {}).map(([token, balance]) => [
        token,
        balance && typeof balance === 'object' ? { ...balance } : balance,
    ]));
    const node = await getDefaultNode();
    return attachTokenMetadata(snapshot, await getTokenList(node, Object.keys(snapshot)));
}

   
                                                           
                                                                             
                                 
   
export async function sendTx(anyTxBytes) {
    const node = await getDefaultNode();
    const tx = toBase64(anyTxBytes);
    const res = await postJSON('/api/wallet/submittx', { node, tx });
    if (res?.error) throw new Error(res.error);
    return res;
}

   
                     
                       
   
export async function getTxReceipt(txId) {
    const node = await getDefaultNode();
    return postJSON('/api/wallet/receipt', { node, tx_id: txId });
}

                                                     
export async function setExplorerUrl(url) {
    const stored = await chrome.storage.local.get([WALLET_ENV_KEY, WALLET_ENV_CONFIGS_KEY]);
    const walletEnv = normalizeWalletEnv(stored[WALLET_ENV_KEY]);
    const envConfigs = mergeWalletEnvConfigs(stored[WALLET_ENV_CONFIGS_KEY]);
    envConfigs[walletEnv] = { ...envConfigs[walletEnv], explorerUrl: url };
    await chrome.storage.local.set({ [WALLET_ENV_CONFIGS_KEY]: envConfigs, explorerUrl: url });
}

                                                                          
export async function setDefaultNode(addr) {
    const stored = await chrome.storage.local.get([WALLET_ENV_KEY, WALLET_ENV_CONFIGS_KEY]);
    const walletEnv = normalizeWalletEnv(stored[WALLET_ENV_KEY]);
    const envConfigs = mergeWalletEnvConfigs(stored[WALLET_ENV_CONFIGS_KEY]);
    envConfigs[walletEnv] = { ...envConfigs[walletEnv], nodeAddr: addr };
    await chrome.storage.local.set({ [WALLET_ENV_CONFIGS_KEY]: envConfigs, nodeAddr: addr });
}
