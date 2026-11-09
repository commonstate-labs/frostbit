   
                                                                      
                                                                   
   

const SECP256K1_ORDER = BigInt('0xfffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd0364141');

export function normalizePrivateKey(value) {
    if (typeof value !== 'string') {
        throw new Error('Private key must be text');
    }

    const trimmed = value.trim();
    const hex = trimmed.replace(/^0x/i, '');
    if (!/^[0-9a-fA-F]{64}$/.test(hex)) {
        throw new Error('Private key must be 32-byte hex (64 hex characters, optional 0x prefix)');
    }

    const scalar = BigInt(`0x${hex}`);
    if (scalar === 0n || scalar >= SECP256K1_ORDER) {
        throw new Error('Private key is outside the valid secp256k1 range');
    }
    return hex.toLowerCase();
}

   
                                                                              
                                                                         
   
export function parsePrivateKeysText(text) {
    if (typeof text !== 'string') throw new Error('Private-key input must be text');

    const keys = [];
    const seen = new Set();
    const lines = text.split(/\r?\n/);
    for (let i = 0; i < lines.length; i += 1) {
        const raw = lines[i].trim();
        if (!raw || raw.startsWith('#')) continue;
        const candidate = raw.replace(/\s+#.*$/, '').trim();
        try {
            const key = normalizePrivateKey(candidate);
            if (!seen.has(key)) {
                seen.add(key);
                keys.push(key);
            }
        } catch (error) {
            throw new Error(`Line ${i + 1}: ${error.message}`);
        }
    }

    if (keys.length === 0) throw new Error('No private keys found');
    return keys;
}
