   
                               
                                                                        
  
                                          
                                                                 
                               
                               
                                                                                                       
  
                                                                                                       
                                                                                  
   

import {
    generateMnemonicPhrase, isMnemonicValid, deriveAccount, deriveAccountFromRawHex,
    saveKeystore, loadKeystore, hasKeystore, clearKeystore,
    saveImportedKeys, loadImportedAccounts, hasImportedKeys,
} from '../lib/keystore.js';
import { buildAndSign, decodeAnyTx } from '../lib/proto.js';
import { enrichBalanceTokenMetadata, getAccount, getAccountBalances, isValidRentStatus, sendTx, getTxReceipt } from '../lib/rpc.js';
import { toBase64 } from '../lib/crypto.js';
import { signedWithdrawProofForHistory } from '../lib/local_tx_history.js';
import { mergeBalanceTokenMetadata } from '../lib/asset_metadata.js';
import {
    WALLET_ENV_CONFIGS_KEY, WALLET_ENV_KEY, WALLET_ENV_PROFILES,
    mergeWalletEnvConfigs, normalizeEnvConfig, normalizeWalletEnv,
} from '../lib/env.js';
import {
    isAllowedDappSender,
    isTrustedExtensionSender,
    sanitizeDappMessage,
} from '../lib/message_policy.js';
import { normalizePrivateKey } from '../lib/private_key_import.js';

                                                                                                                            
const ALLOWED_DAPP_ORIGINS = [
    /^https:\/\/([a-z0-9-]+\.)*frostbit\.io$/i,
    /^http:\/\/localhost(:\d+)?$/i,
    /^http:\/\/127\.0\.0\.1(:\d+)?$/i,
];

function isOriginAllowed(rawUrl) {
    if (!rawUrl) return false;
    try { return ALLOWED_DAPP_ORIGINS.some(re => re.test(new URL(rawUrl).origin)); }
    catch { return false; }
}

                                                                                                                                                                                                             

const _session = { unlocked: false, hasMnemonic: false, accounts: [], selectedIndex: 0, pendingRequests: [] };

let _reqIdCounter = 0;
const LOCAL_TX_HISTORY_KEY = 'frostbit_local_tx_history_v1';
const LOCAL_TX_HISTORY_LIMIT = 500;
const SELECTED_ADDR_KEY = 'frostbit_selected_address_v1';
const AUTO_LOCK_MIN_KEY = 'frostbit_auto_lock_minutes_v1';
const AUTO_LOCK_DEFAULT_MIN = 30;
const DAPP_CONNECTIONS_KEY = 'frostbit_dapp_connections_v1';
const BALANCE_CACHE_KEY = 'frostbit_balance_cache_v1';
const BALANCE_CACHE_LIMIT = 50;
const APPROVAL_POPUP_URL = 'popup/index.html?mode=approval';
const APPROVAL_POPUP_WIDTH = 540;
const APPROVAL_POPUP_HEIGHT = 720;
let _balanceCacheWrite = Promise.resolve();

async function getWalletEnvSettings() {
    const stored = await chrome.storage.local.get([WALLET_ENV_KEY, WALLET_ENV_CONFIGS_KEY, 'explorerUrl', 'nodeAddr']);
    const walletEnv = normalizeWalletEnv(stored[WALLET_ENV_KEY]);
    const envConfigs = mergeWalletEnvConfigs(stored[WALLET_ENV_CONFIGS_KEY]);
    if (!stored[WALLET_ENV_CONFIGS_KEY] && (stored.explorerUrl || stored.nodeAddr)) {
        envConfigs.dev = normalizeEnvConfig('dev', { explorerUrl: stored.explorerUrl, nodeAddr: stored.nodeAddr });
    }
    return { walletEnv, envConfigs };
}

async function getActiveWalletEnv() {
    const { [WALLET_ENV_KEY]: value } = await chrome.storage.local.get(WALLET_ENV_KEY);
    return normalizeWalletEnv(value);
}

async function getAutoLockMinutes() {
    const { [AUTO_LOCK_MIN_KEY]: m } = await chrome.storage.local.get(AUTO_LOCK_MIN_KEY);
    const n = Number(m);
    return Number.isFinite(n) && n > 0 ? n : AUTO_LOCK_DEFAULT_MIN;
}

async function scheduleAutoLock() {
    chrome.alarms.create('auto_lock', { delayInMinutes: await getAutoLockMinutes() });
}

async function connectedDappOrigins() {
    const { [DAPP_CONNECTIONS_KEY]: origins } = await chrome.storage.session.get(DAPP_CONNECTIONS_KEY);
    return new Set(Array.isArray(origins) ? origins.filter(v => typeof v === 'string') : []);
}

async function isDappConnected(origin) {
    return (await connectedDappOrigins()).has(origin);
}

async function grantDappConnection(origin) {
    const origins = await connectedDappOrigins();
    origins.add(origin);
    await chrome.storage.session.set({ [DAPP_CONNECTIONS_KEY]: [...origins].sort() });
}

async function clearDappConnections() {
    await chrome.storage.session.remove(DAPP_CONNECTIONS_KEY);
}

async function loadSavedSelectedAddress() {
    const { [SELECTED_ADDR_KEY]: addr } = await chrome.storage.local.get(SELECTED_ADDR_KEY);
    return typeof addr === 'string' ? addr : '';
}

async function saveSelectedAddress(addr) {
    if (addr) await chrome.storage.local.set({ [SELECTED_ADDR_KEY]: addr });
}

async function applySavedSelection() {
    const saved = await loadSavedSelectedAddress();
    if (!saved) { _session.selectedIndex = 0; return; }
    const i = _session.accounts.findIndex(a => a.address === saved);
    _session.selectedIndex = i >= 0 ? i : 0;
}

function accountRawHex(acc) {
    return Array.from(acc.rawPriv || []).map(b => b.toString(16).padStart(2, '0')).join('');
}

function mergeUniqueAccounts(...groups) {
    const out = [];
    const seen = new Set();
    for (const group of groups) {
        for (const acc of group || []) {
            if (!acc?.address || seen.has(acc.address)) continue;
            seen.add(acc.address);
            out.push(acc);
        }
    }
    return out;
}

async function saveImportedAccounts(accounts, password) {
    await saveImportedKeys((accounts || []).map(accountRawHex).filter(Boolean), password);
}

async function loadVaultAccounts(password) {
    const hasMain = await hasKeystore();
    const hasImp = await hasImportedKeys();
    const mnemonicAccounts = [];
    let importedAccounts = [];
    if (hasMain) {
        let phrase = await loadKeystore(password);
        mnemonicAccounts.push(await deriveAccount(phrase, 0));
        phrase = null;
    }
    if (hasImp) importedAccounts = await loadImportedAccounts(password);
    const accounts = mergeUniqueAccounts(mnemonicAccounts, importedAccounts);
    return { hasMain, hasImp, mnemonicAccounts, importedAccounts, accounts };
}

async function loadExistingVaultAccounts(password) {
    if (!(await hasKeystore()) && !(await hasImportedKeys())) {
        return { hasMain: false, hasImp: false, mnemonicAccounts: [], importedAccounts: [], accounts: [] };
    }
    try {
        return await loadVaultAccounts(password);
    } catch {
        throw new Error('密码错误');
    }
}

async function balanceCacheScope(address) {
    const { walletEnv, envConfigs } = await getWalletEnvSettings();
    const config = envConfigs[walletEnv];
    return JSON.stringify([walletEnv, config.explorerUrl.replace(/\/+$/, ''), config.nodeAddr, address]);
}

function carryTokenMetadata(balances, previous = {}) {
    return mergeBalanceTokenMetadata(balances, previous);
}

async function getCachedBalances(address) {
    const scope = await balanceCacheScope(address);
    const { [BALANCE_CACHE_KEY]: cache } = await chrome.storage.local.get(BALANCE_CACHE_KEY);
    const entry = cache && typeof cache === 'object' ? cache[scope] : null;
    return entry?.balances && typeof entry.balances === 'object' && isValidRentStatus(entry.rent_status) ? entry : null;
}

async function saveCachedBalances(address, balances, rent_status, updatedAt = Date.now(), metadataOnly = false) {
    const scope = await balanceCacheScope(address);
    let saved;
    _balanceCacheWrite = _balanceCacheWrite.catch(() => {}).then(async () => {
        const { [BALANCE_CACHE_KEY]: stored } = await chrome.storage.local.get(BALANCE_CACHE_KEY);
        const cache = stored && typeof stored === 'object' ? { ...stored } : {};
        if (metadataOnly && cache[scope]?.updatedAt !== updatedAt) {
            saved = cache[scope];
            return;
        }
        saved = { balances: carryTokenMetadata(balances, cache[scope]?.balances), rent_status, updatedAt };
        cache[scope] = saved;
        const stale = Object.entries(cache)
            .sort((left, right) => Number(right[1]?.updatedAt || 0) - Number(left[1]?.updatedAt || 0))
            .slice(BALANCE_CACHE_LIMIT);
        for (const [key] of stale) delete cache[key];
        await chrome.storage.local.set({ [BALANCE_CACHE_KEY]: cache });
    });
    await _balanceCacheWrite;
    return saved;
}

async function checkSpendableBalance(address, txDesc, fee) {
    if (!/^\d+$/.test(String(fee))) throw new Error('Fee must be a non-negative integer');
    const snapshot = await getAccountBalances(address);
    let requiredFB = BigInt(fee);
    if (txDesc?.type === 'transaction') {
        if (!/^\d+$/.test(String(txDesc.amount))) throw new Error('Invalid transfer amount');
        const amount = BigInt(txDesc.amount);
        if (txDesc.tokenAddress === 'FB') requiredFB += amount;
        else if (amount > BigInt(snapshot.balances[txDesc.tokenAddress]?.balance || '0')) throw new Error('Insufficient token balance');
    }
    if (txDesc?.type === 'witness_challenge') {
        if (!/^\d+$/.test(String(txDesc.stakeAmount))) throw new Error('Invalid challenge stake');
        requiredFB += BigInt(txDesc.stakeAmount);
    }
    if (requiredFB > BigInt(snapshot.rent_status.spendable_fb)) throw new Error('Insufficient spendable FB after rent for the amount and fee');
}

async function importPrivateKeyAccounts(hexKeys, password, fallbackSelectedAddress = '') {
    if (!Array.isArray(hexKeys) || hexKeys.length === 0) {
        throw new Error('No keys provided');
    }

    const normalizedKeys = [...new Set(hexKeys.map(normalizePrivateKey))];
    const vault = await loadExistingVaultAccounts(password);
    const existing = new Set(vault.accounts.map(a => a.address));
    const newAccounts = [];
    for (const key of normalizedKeys) {
        const account = await deriveAccountFromRawHex(key);
        if (!existing.has(account.address)) {
            existing.add(account.address);
            newAccounts.push(account);
        }
    }

    const mainAddresses = new Set(vault.mnemonicAccounts.map(a => a.address));
    const importedAccounts = mergeUniqueAccounts(vault.importedAccounts, newAccounts)
        .filter(account => !mainAddresses.has(account.address));
    await saveImportedAccounts(importedAccounts, password);

    const selectedAddress = newAccounts[0]?.address
        || fallbackSelectedAddress
        || vault.accounts[0]?.address
        || '';
    const accounts = mergeUniqueAccounts(vault.mnemonicAccounts, importedAccounts);
    if (accounts.length === 0) throw new Error('No accounts imported');
    await unlockWithAccounts(accounts, vault.hasMain, password, selectedAddress);
    return { added: newAccounts.map(account => account.address), selectedAddress };
}

async function unlockWithAccounts(accounts, hasMnemonic, password, selectedAddress = '') {
    _session.hasMnemonic = hasMnemonic;
    _session.accounts = accounts;
    _session.unlocked = true;
    if (selectedAddress) {
        const i = _session.accounts.findIndex(a => a.address === selectedAddress);
        _session.selectedIndex = i >= 0 ? i : 0;
        await saveSelectedAddress(selectedAccount()?.address || selectedAddress);
    } else {
        await applySavedSelection();
    }
    await chrome.storage.session.set({ sessionPassword: password });
    await scheduleAutoLock();
}

async function getLocalTxHistory() {
    const walletEnv = await getActiveWalletEnv();
    const key = `${LOCAL_TX_HISTORY_KEY}:${walletEnv}`;
    const { [key]: items, [LOCAL_TX_HISTORY_KEY]: legacyItems } = await chrome.storage.local.get([key, LOCAL_TX_HISTORY_KEY]);
    if (Array.isArray(items)) return items;
    if (walletEnv === 'dev' && Array.isArray(legacyItems)) return legacyItems;
    return Array.isArray(items) ? items : [];
}

async function appendLocalTxHistory(item) {
    const walletEnv = await getActiveWalletEnv();
    const key = `${LOCAL_TX_HISTORY_KEY}:${walletEnv}`;
    const items = await getLocalTxHistory();
    items.unshift(item);
    if (items.length > LOCAL_TX_HISTORY_LIMIT) {
        items.length = LOCAL_TX_HISTORY_LIMIT;
    }
    await chrome.storage.local.set({ [key]: items });
}

async function tryRehydrateSession() {
    if (_session.unlocked) return true;
    const { sessionPassword } = await chrome.storage.session.get('sessionPassword');
    if (!sessionPassword) return false;

    try {
        const vault = await loadVaultAccounts(sessionPassword);
        if (vault.accounts.length === 0) throw new Error('No accounts');
        _session.hasMnemonic = vault.hasMain;
        _session.accounts = vault.accounts;
        await applySavedSelection();
        _session.unlocked = true;
        return true;
    } catch (e) {
        await chrome.storage.session.remove('sessionPassword');
        return false;
    }
}

async function requireUnlocked() {
    if (!(await tryRehydrateSession())) throw new Error('Wallet is locked');
}

function selectedAccount() {
    return _session.accounts[_session.selectedIndex];
}

function readAccountNonce(accountInfo) {
    const value = accountInfo?.nonce ?? accountInfo?.account?.nonce;
    const text = String(value ?? '').trim();
    if (!/^\d+$/.test(text) || BigInt(text) > 0xffffffffffffffffn) {
        throw new Error('Explorer returned an invalid account nonce');
    }
    return text;
}

function normalizeRequestedNonce(value, fallback) {
    const text = String(value === undefined || value === null || value === '' ? fallback : value).trim();
    if (!/^\d+$/.test(text) || BigInt(text) > 0xffffffffffffffffn) {
        throw new Error('Nonce must be an integer from 0 to 18446744073709551615');
    }
    return text;
}

function readChainId(accountInfo) {
    const id = accountInfo?.chain_id ?? accountInfo?.chainId;
    const n = Number(id);
    return Number.isFinite(n) && n > 0 ? n : undefined;
}

                                                                                                                                                                                                                          

chrome.runtime.onMessage.addListener((rawMsg, sender, reply) => {
    const runtimeId = chrome.runtime.id;
    const fromExtensionUi = isTrustedExtensionSender(sender, runtimeId);
    const fromDapp = isAllowedDappSender(sender, runtimeId, isOriginAllowed);

    if (!fromExtensionUi && !fromDapp) {
        console.warn('[FrostBit] rejected message from untrusted sender:', sender?.url || sender?.origin || 'unknown');
        reply({ error: 'Untrusted wallet message sender' });
        return false;
    }

    let msg = rawMsg;
    let dappOrigin = '';
    if (fromDapp) {
        try {
                                                                            
                                              
            msg = sanitizeDappMessage(rawMsg);
            dappOrigin = new URL(sender.url || sender.tab.url).origin;
        } catch (e) {
            reply({ error: e.message });
            return false;
        }
    } else {
                                                                              
                                                                       
        chrome.storage.session.get('sessionPassword').then(async ({ sessionPassword }) => {
            if (sessionPassword) await scheduleAutoLock();
        });
    }

    if (fromDapp && (msg.type === 'GET_ACCOUNTS' || msg.type === 'GET_BALANCES')) {
        (async () => {
            if (!(await isDappConnected(dappOrigin))) {
                if (msg.type === 'GET_ACCOUNTS') return { accounts: [] };
                throw new Error('Connect this site to the wallet first');
            }
            await requireUnlocked();
            if (msg.type === 'GET_ACCOUNTS') {
                return { accounts: [selectedAccount().address] };
            }
            return handleMessage(msg);
        })().then(reply).catch(e => reply({ error: e.message }));
        return true;
    }

                                                                              
                                                               
    if (fromDapp && (
        msg.type === 'REQUEST_ACCOUNTS'
        || msg.type === 'SEND_TX'
        || msg.type === 'SIGN_TX'
        || msg.type === 'SIGN_MESSAGE'
    )) {
        const reqUrl = sender.url || sender.tab.url;
        const reqId = ++_reqIdCounter;
        _session.pendingRequests.push({
            id: reqId,
            type: msg.type,
            payload: msg,
            txDesc: msg.type === 'REQUEST_ACCOUNTS'
                ? { type: 'connect', origin: dappOrigin }
                : msg.type === 'SIGN_MESSAGE'
                    ? { type: 'sign_message', message: msg.message }
                    : msg.txDesc,
            reply,
            url: reqUrl,
            origin: dappOrigin,
        });
        chrome.windows.create({
            url: APPROVAL_POPUP_URL,
            type: 'popup',
            width: APPROVAL_POPUP_WIDTH,
            height: APPROVAL_POPUP_HEIGHT,
            focused: true,
        });
        return true;
    }
    handleMessage(msg).then(reply).catch(e => reply({ error: e.message }));
    return true;
});

async function handleMessage(msg) {
    switch (msg.type) {

                                                                                                                                                                                                             
        case 'GET_PENDING_REQUESTS':
            return { requests: _session.pendingRequests.map(r => ({ id: r.id, type: r.type, txDesc: r.txDesc, url: r.url })) };

        case 'APPROVE_REQUEST': {
            const reqIdx = _session.pendingRequests.findIndex(r => r.id === msg.id);
            if (reqIdx === -1) throw new Error('Request not found');
            const req = _session.pendingRequests.splice(reqIdx, 1)[0];
                                       
            const prevIdx = _session.selectedIndex;
            if (Number.isFinite(msg.accountIndex) && msg.accountIndex >= 0 && msg.accountIndex < _session.accounts.length)
                _session.selectedIndex = msg.accountIndex;
            try {
                let res;
                if (req.type === 'REQUEST_ACCOUNTS') {
                    await requireUnlocked();
                    await grantDappConnection(req.origin);
                    res = { address: selectedAccount().address };
                } else {
                    res = await handleMessage({ ...req.payload, fee: msg.fee });
                }
                req.reply(res);
                return { ok: true };
            } catch (e) { req.reply({ error: e.message }); throw e; }
            finally { _session.selectedIndex = prevIdx; }
        }

        case 'REJECT_REQUEST': {
            const reqIdx = _session.pendingRequests.findIndex(r => r.id === msg.id);
            if (reqIdx === -1) throw new Error('Request not found');
            _session.pendingRequests.splice(reqIdx, 1)[0].reply({ error: 'User rejected the request' });
            return { ok: true };
        }

                                                                                                                                                                                                                

        case 'HAS_WALLET':
            return { has: (await hasKeystore()) || (await hasImportedKeys()) };

        case 'GENERATE_MNEMONIC':
            return { mnemonic: generateMnemonicPhrase() };

        case 'CREATE_WALLET':
        case 'IMPORT_WALLET': {
            const { mnemonic, password } = msg;
            if (!isMnemonicValid(mnemonic)) throw new Error('Invalid mnemonic');
            const vault = await loadExistingVaultAccounts(password);
            const mainAccount = await deriveAccount(mnemonic, 0);
            const importedAccounts = mergeUniqueAccounts(vault.accounts).filter(a => a.address !== mainAccount.address);
            await saveKeystore(mnemonic, password);
            await saveImportedAccounts(importedAccounts, password);
            await unlockWithAccounts(mergeUniqueAccounts([mainAccount], importedAccounts), true, password, mainAccount.address);
                                                                                    
            return { address: mainAccount.address };
        }

                                                                                                                                                                                                              

        case 'UNLOCK': {
            const hasMain = await hasKeystore();
            const hasImp = await hasImportedKeys();
            if (!hasMain && !hasImp) throw new Error('请先创建或导入钱包');
            let vault;
            try {
                vault = await loadVaultAccounts(msg.password);
            } catch { throw new Error('密码错误'); }
            if (vault.accounts.length === 0) throw new Error('密码错误');
            await unlockWithAccounts(vault.accounts, vault.hasMain, msg.password);
            return { address: selectedAccount().address };
        }

        case 'LOCK':
            _session.unlocked = false;
            _session.hasMnemonic = false;
            _session.accounts = [];
            for (const r of _session.pendingRequests) { try { r.reply({ error: 'Wallet locked' }); } catch { } }
            _session.pendingRequests = [];
            await chrome.storage.session.remove('sessionPassword');
            await clearDappConnections();
            chrome.alarms.clear('auto_lock');
            return { ok: true };

        case 'IS_UNLOCKED':
            return { unlocked: await tryRehydrateSession() };

                                                                                                                                                                                                                      

        case 'GET_ACCOUNTS':
            await requireUnlocked();
            return {
                accounts: _session.accounts.map(a => a.address),
                selectedAddress: selectedAccount()?.address || '',
                hasMnemonic: _session.hasMnemonic,
            };

        case 'GET_SELECTED_ACCOUNT':
            await requireUnlocked();
            return { address: selectedAccount().address };

        case 'ADD_ACCOUNT': {
            await requireUnlocked();
            if (!_session.hasMnemonic) throw new Error('No mnemonic (imported-key wallet cannot add derived accounts)');
                                                                                                     
            const { sessionPassword } = await chrome.storage.session.get('sessionPassword');
            if (!sessionPassword) throw new Error('Wallet is locked');
            let phrase = await loadKeystore(sessionPassword);
            const existing = new Set(_session.accounts.map(a => a.address));
            let idx = 0;
            let acc;
            do {
                acc = await deriveAccount(phrase, idx++);
            } while (existing.has(acc.address) && idx < 1000);
            phrase = null;
            if (existing.has(acc.address)) throw new Error('No unused mnemonic account found');
            const vault = await loadExistingVaultAccounts(sessionPassword);
            const importedAccounts = mergeUniqueAccounts(vault.importedAccounts, [acc])
                .filter(a => !vault.mnemonicAccounts.some(m => m.address === a.address));
            await saveImportedAccounts(importedAccounts, sessionPassword);
            await unlockWithAccounts(mergeUniqueAccounts(vault.mnemonicAccounts, importedAccounts), vault.hasMain, sessionPassword, acc.address);
            return { address: acc.address, index: idx - 1 };
        }

        case 'SELECT_ACCOUNT':
            await requireUnlocked();
            if (msg.index < 0 || msg.index >= _session.accounts.length)
                throw new Error('Invalid account index');
            _session.selectedIndex = msg.index;
            await saveSelectedAddress(selectedAccount().address);
            return { address: selectedAccount().address };

                                                                             
        case 'IMPORT_ACCOUNTS_FILE': {
            const { hexKeys, password } = msg;
            return importPrivateKeyAccounts(hexKeys, password);
        }

                                                                                           
        case 'IMPORT_PRIVATE_KEYS': {
            await requireUnlocked();
            const { sessionPassword } = await chrome.storage.session.get('sessionPassword');
            if (!sessionPassword) throw new Error('Wallet is locked');
            return importPrivateKeyAccounts(msg.hexKeys, sessionPassword, selectedAccount()?.address || '');
        }

                                                                                                                                                                                                                

        case 'GET_BALANCES':
        case 'GET_BALANCES_FAST': {
            await requireUnlocked();
            const addr = msg.address || selectedAccount().address;
            const result = await getAccountBalances(addr);
            let balances = result.balances;
            if (msg.type === 'GET_BALANCES') {
                try { balances = await enrichBalanceTokenMetadata(balances); } catch { }
            }
            let cached = { balances, rent_status: result.rent_status, updatedAt: result.updatedAt };
            try { cached = await saveCachedBalances(addr, balances, result.rent_status, result.updatedAt); } catch { }
            return { ...cached, cached: false };
        }

        case 'GET_CACHED_BALANCES': {
            await requireUnlocked();
            const addr = msg.address || selectedAccount().address;
            const cached = await getCachedBalances(addr);
            return cached ? { ...cached, cached: true } : { balances: {}, updatedAt: 0, cached: false };
        }

        case 'GET_BALANCE_METADATA': {
            await requireUnlocked();
            const addr = msg.address || selectedAccount().address;
            const cached = await getCachedBalances(addr);
            if (!cached) return { balances: {}, updatedAt: 0 };
            const balances = await enrichBalanceTokenMetadata(cached.balances);
            try { return await saveCachedBalances(addr, balances, cached.rent_status, cached.updatedAt, true); }
            catch { return { balances, rent_status: cached.rent_status, updatedAt: cached.updatedAt }; }
        }

        case 'GET_NONCE': {
            await requireUnlocked();
            const addr = msg.address || selectedAccount().address;
            const accountInfo = await getAccount(addr);
            return {
                address: accountInfo?.address || addr,
                nonce: readAccountNonce(accountInfo),
                chainId: readChainId(accountInfo),
            };
        }

                                                                                                                                                                                                                      

        case 'SEND_TX': {
            await requireUnlocked();
            const acc = selectedAccount();
            const { txDesc } = msg;
            const fee = msg.fee ?? '1000';
            await checkSpendableBalance(acc.address, txDesc, fee);
            const accountInfo = await getAccount(acc.address);
            const nonce = normalizeRequestedNonce(msg.nonce, readAccountNonce(accountInfo));
            const chainId = readChainId(accountInfo);
            console.log('[SEND_TX] building:', txDesc.type, txDesc);
            const txBytes = buildAndSign(txDesc, {
                fromAddress: acc.address, nonce,
                rawPrivKey: acc.rawPriv, pubKeyCompressed: acc.pubKeyCompressed, fee, chainId,
            });
            console.log('[SEND_TX] built, submitting...');
            const submitRes = await sendTx(txBytes);
            console.log('[SEND_TX] submitRes:', JSON.stringify(submitRes));
            if (submitRes?.ok !== true) throw new Error('submit tx failed');

            const anyTx = decodeAnyTx(txBytes);
            let txId = '';
            for (const key of Object.keys(anyTx)) {
                if (anyTx[key] && anyTx[key].base && anyTx[key].base.txId) {
                    txId = anyTx[key].base.txId;
                    break;
                }
            }

            const signedTxBase64 = signedWithdrawProofForHistory(txDesc, txBytes);
            await appendLocalTxHistory({
                localId: `${Date.now()}_${Math.random().toString(36).slice(2)}`,
                createdAt: Date.now(),
                fromAddress: acc.address,
                txType: txDesc?.type || '',
                toAddress: txDesc?.to || txDesc?.toAddress || '',
                tokenAddress: txDesc?.tokenAddress || txDesc?.asset || '',
                amount: txDesc?.amount || '',
                txId,
                txDesc,
                ...(signedTxBase64 ? { signedTxBase64 } : {}),
            });

            return { ok: true, txId };
        }

        case 'GET_LOCAL_TX_HISTORY': {
            await requireUnlocked();
            const address = msg.address || selectedAccount().address;
            const limit = Number.isFinite(msg.limit) ? Math.max(1, Math.floor(msg.limit)) : 200;
            const all = await getLocalTxHistory();
            const filtered = all.filter(item => item?.fromAddress === address);
            return { items: filtered.slice(0, limit) };
        }

                                                                                                                                                                                               

        case 'SIGN_TX': {
            await requireUnlocked();
            const acc = selectedAccount();
            const { txDesc } = msg;
            const fee = msg.fee ?? '1000';
            await checkSpendableBalance(acc.address, txDesc, fee);
            const accountInfo = await getAccount(acc.address);
            const nonce = readAccountNonce(accountInfo);
            const chainId = readChainId(accountInfo);
            const txBytes = buildAndSign(txDesc, {
                fromAddress: acc.address, nonce,
                rawPrivKey: acc.rawPriv, pubKeyCompressed: acc.pubKeyCompressed, fee, chainId,
            });
            return { signedTx: toBase64(txBytes) };
        }

        case 'SIGN_MESSAGE': {
            await requireUnlocked();
            const acc = selectedAccount();
            const msgBytes = new TextEncoder().encode(msg.message);
            const { sign } = await import('../lib/crypto.js');
            const sig = sign(acc.rawPriv, msgBytes);
            return {
                signature: toBase64(sig),
                address: acc.address,
                publicKey: toBase64(acc.pubKeyCompressed),
            };
        }

                                                                                                                                                                                                             

        case 'GET_RECEIPT':
            return getTxReceipt(msg.txId);

                                                                                                                                                                                                             

        case 'SET_SETTINGS': {
            const lockMin = Math.max(1, Math.min(1440, Math.floor(Number(msg.autoLockMinutes)) || AUTO_LOCK_DEFAULT_MIN));
            const current = await getWalletEnvSettings();
            const walletEnv = normalizeWalletEnv(msg.walletEnv || current.walletEnv);
            const envConfigs = mergeWalletEnvConfigs(msg.envConfigs || current.envConfigs);
            envConfigs[walletEnv] = normalizeEnvConfig(walletEnv, {
                explorerUrl: msg.explorerUrl,
                nodeAddr: msg.nodeAddr,
            });
            await chrome.storage.local.set({
                [WALLET_ENV_KEY]: walletEnv,
                [WALLET_ENV_CONFIGS_KEY]: envConfigs,
                explorerUrl: envConfigs[walletEnv].explorerUrl,
                nodeAddr: envConfigs[walletEnv].nodeAddr,
                [AUTO_LOCK_MIN_KEY]: lockMin,
            });
            if (_session.unlocked) await scheduleAutoLock();
            return { ok: true };
        }

        case 'GET_SETTINGS': {
            const { walletEnv, envConfigs } = await getWalletEnvSettings();
            return {
                walletEnv,
                envConfigs,
                envProfiles: WALLET_ENV_PROFILES,
                explorerUrl: envConfigs[walletEnv].explorerUrl,
                nodeAddr: envConfigs[walletEnv].nodeAddr,
                autoLockMinutes: await getAutoLockMinutes(),
            };
        }

                                                                                                                                                                                                                   

        case 'RESET_WALLET':
            await clearKeystore();
            _session.unlocked = false;
            _session.hasMnemonic = false;
            _session.accounts = [];
            await chrome.storage.local.remove([
                LOCAL_TX_HISTORY_KEY,
                ...WALLET_ENV_PROFILES.map(profile => `${LOCAL_TX_HISTORY_KEY}:${profile.key}`),
                SELECTED_ADDR_KEY,
                BALANCE_CACHE_KEY,
            ]);
            await chrome.storage.session.remove('sessionPassword');
            await clearDappConnections();
            chrome.alarms.clear('auto_lock');
            return { ok: true };

        default:
            throw new Error(`Unknown message type: ${msg.type}`);
    }
}

                                    
chrome.alarms.onAlarm.addListener(async a => {
    if (a.name === 'auto_lock') {
        _session.unlocked = false;
        _session.hasMnemonic = false;
        _session.accounts = [];
        await chrome.storage.session.remove('sessionPassword');
        await clearDappConnections();
    }
});
