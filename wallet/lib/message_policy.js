   
                                                                             
  
                                                                         
                                                          
   
export const DAPP_MESSAGE_TYPES = Object.freeze([
    'REQUEST_ACCOUNTS',
    'GET_ACCOUNTS',
    'IS_UNLOCKED',
    'GET_BALANCES',
    'SEND_TX',
    'SIGN_TX',
    'SIGN_MESSAGE',
]);

const DAPP_MESSAGE_TYPE_SET = new Set(DAPP_MESSAGE_TYPES);
const MAX_ADDRESS_LENGTH = 256;
const MAX_SIGN_MESSAGE_LENGTH = 64 * 1024;

export function isDappMessageType(type) {
    return typeof type === 'string' && DAPP_MESSAGE_TYPE_SET.has(type);
}

function requirePublicMessage(msg) {
    if (!msg || typeof msg !== 'object' || Array.isArray(msg)) {
        throw new Error('Invalid wallet request');
    }
    if (!isDappMessageType(msg.type)) {
        throw new Error('Wallet method is not available to web pages');
    }
}

function optionalAddress(address) {
    if (address === undefined || address === null || address === '') return undefined;
    if (typeof address !== 'string' || address.length > MAX_ADDRESS_LENGTH) {
        throw new Error('Invalid address');
    }
    return address;
}

   
                                                                            
                                                                   
   
export function sanitizeDappMessage(msg) {
    requirePublicMessage(msg);

    switch (msg.type) {
        case 'REQUEST_ACCOUNTS':
        case 'GET_ACCOUNTS':
        case 'IS_UNLOCKED':
            return { type: msg.type };

        case 'GET_BALANCES': {
            const address = optionalAddress(msg.address);
            return address === undefined
                ? { type: msg.type }
                : { type: msg.type, address };
        }

        case 'SEND_TX':
        case 'SIGN_TX':
            if (!msg.txDesc || typeof msg.txDesc !== 'object' || Array.isArray(msg.txDesc)) {
                throw new Error('Invalid transaction');
            }
            return { type: msg.type, txDesc: msg.txDesc };

        case 'SIGN_MESSAGE':
            if (typeof msg.message !== 'string' || msg.message.length > MAX_SIGN_MESSAGE_LENGTH) {
                throw new Error('Invalid message');
            }
            return { type: msg.type, message: msg.message };

        default:
            throw new Error('Wallet method is not available to web pages');
    }
}

export function isTrustedExtensionSender(sender, runtimeId) {
    if (!runtimeId || !sender || sender.id !== runtimeId) return false;
    const rawUrl = sender.url || sender.origin;
    if (typeof rawUrl !== 'string') return false;
    try {
        const url = new URL(rawUrl);
        return url.protocol === 'chrome-extension:' && url.host === runtimeId;
    } catch {
        return false;
    }
}

export function isAllowedDappSender(sender, runtimeId, isOriginAllowed) {
    if (!runtimeId || !sender || sender.id !== runtimeId || !sender.tab) return false;
    const rawUrl = sender.url || sender.tab.url;
    return typeof rawUrl === 'string' && isOriginAllowed(rawUrl);
}
