import {
    builtinAssetForAddress, builtinAssetForNativeIdentity, normalizeChain, normalizeContract,
} from '../../shared/assetMetadata.mjs';

const METADATA_FIELDS = [
    'decimals', 'symbol', 'address', 'name', 'native_chain', 'native_contract',
    'icon_asset', 'icon_url', 'display_label', 'metadata_address',
];

function validDecimals(value) {
                                                                             
    if (typeof value !== 'number' && (typeof value !== 'string' || !/^\d+$/.test(value))) return null;
    const decimals = Number(value);
    return Number.isInteger(decimals) && decimals >= 0 && decimals <= 255 ? decimals : null;
}

function exactMetadata(token, balance) {
    return balance?.metadata_address === token && balance?.address === token ? balance : undefined;
}

function builtinForToken(token, balance) {
    const direct = builtinAssetForAddress(token);
    if (direct) return direct;
    const exact = exactMetadata(token, balance);
    return exact ? builtinAssetForNativeIdentity(exact.native_chain, exact.native_contract) : undefined;
}

export function tokenDecimals(token, balance) {
    return validDecimals(builtinForToken(token, balance)?.metadata.decimals ?? exactMetadata(token, balance)?.decimals);
}

function shortID(token) {
    return token.length > 20 ? `${token.slice(0, 9)}…${token.slice(-6)}` : token;
}

export function tokenDisplayLabel(tokenValue, balance) {
    const token = String(tokenValue || '').trim();
    if (!token) return '-';
    const builtin = builtinForToken(token, balance);
    const metadata = builtin?.metadata || exactMetadata(token, balance);
    const chain = normalizeChain(builtin ? builtin.native_chain : balance?.native_chain).toUpperCase();
    let label = String(metadata?.icon_asset || metadata?.symbol || '').trim();
    if (label === token || /^(br_|0x|fbc1)/i.test(label) || label.length > 32) label = '';
    if (chain && label && label !== chain && !label.startsWith(`${chain}.`)) label = `${chain}.${label}`;
                                                                             
                                                                                  
    if (builtinAssetForAddress(token)) return label || token;
    return [label || chain, shortID(token)].filter(Boolean).join(' · ');
}

export function tokenDisplayTitle(token, balance) {
    const label = tokenDisplayLabel(token, balance);
    return label === token ? token : `${label}\n${token}`;
}

                                                                                    
export function attachTokenMetadata(balances, tokens = []) {
    const byAddress = new Map((tokens || []).filter(token => token?.address).map(token => [token.address, token]));
    return Object.fromEntries(Object.entries(balances || {}).map(([token, balance]) => {
        if (!balance || typeof balance !== 'object') return [token, balance];
        const info = byAddress.get(token);
        const result = { ...balance };
        if (info) {
                                                                            
                                                                                  
            for (const field of METADATA_FIELDS) delete result[field];
            result.address = token;
            result.metadata_address = token;
            result.native_chain = normalizeChain(info.native_chain);
            result.native_contract = normalizeContract(result.native_chain, info.native_contract);
            result.symbol = typeof info.symbol === 'string' ? info.symbol : '';
            result.name = typeof info.name === 'string' ? info.name : '';
            if (typeof info.icon_url === 'string' && info.icon_url.startsWith('/api/token-icon?')) {
                try {
                    const icon = new URL(info.icon_url, 'http://localhost');
                    if (icon.pathname === '/api/token-icon' && icon.searchParams.get('addr') === token) {
                        result.icon_url = info.icon_url;
                    }
                } catch {                                              }
            }
            const decimals = validDecimals(info.decimals);
            if (decimals !== null) result.decimals = decimals;
        }
        const builtin = builtinForToken(token, result);
        if (builtin) {
            Object.assign(result, builtin.metadata, {
                address: token, metadata_address: token,
                native_chain: builtin.native_chain || '', native_contract: builtin.native_contract || '',
            });
        }
        result.display_label = tokenDisplayLabel(token, result);
        return [token, result];
    }));
}

                                                                               
export function mergeBalanceTokenMetadata(balances, previous = {}) {
    const merged = Object.fromEntries(Object.entries(balances || {}).map(([token, value]) => {
        if (!value || typeof value !== 'object') return [token, value];
        const old = previous[token] || {};
        const meta = Object.fromEntries(METADATA_FIELDS.filter(field => old[field] !== undefined).map(field => [field, old[field]]));
                                                                          
                                                                   
        return [token, value.metadata_address === token ? { ...value } : { ...meta, ...value }];
    }));
    return attachTokenMetadata(merged);
}

export function toRawAmount(display, decimals) {
    const scale = validDecimals(decimals);
    if (scale === null) throw new Error('unknown asset precision');
    const text = String(display).trim();
    if (!/^(?:\d+(?:\.\d*)?|\.\d+)$/.test(text)) throw new Error('invalid amount');
    const [whole = '0', fraction = ''] = text.split('.');
    if (fraction.slice(scale).replace(/0/g, '')) throw new Error('amount exceeds asset precision');
    return (BigInt(whole || '0') * 10n ** BigInt(scale) + BigInt(fraction.slice(0, scale).padEnd(scale, '0') || '0')).toString();
}

export function formatRawAmount(raw, decimals) {
    const text = String(raw ?? '0').trim();
    const scale = validDecimals(decimals);
    if (scale === null || !/^\d+$/.test(text)) return `${text || '0'} raw`;
    const normalized = text.replace(/^0+/, '') || '0';
    const padded = normalized.padStart(scale + 1, '0');
    const whole = scale ? padded.slice(0, -scale) : padded;
    const fraction = scale ? padded.slice(-scale).replace(/0+$/, '') : '';
    const grouped = whole.replace(/\B(?=(\d{3})+(?!\d))/g, ',');
    return fraction ? `${grouped}.${fraction}` : grouped;
}
