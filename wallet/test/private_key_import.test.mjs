import test from 'node:test';
import assert from 'node:assert/strict';

import {
    normalizePrivateKey,
    parsePrivateKeysText,
} from '../lib/private_key_import.js';
import { deriveAccountFromRawHex } from '../lib/keystore.js';

const KEY_ONE = '0000000000000000000000000000000000000000000000000000000000000001';
const KEY_TWO = '0000000000000000000000000000000000000000000000000000000000000002';

test('private keys accept plain or 0x-prefixed 32-byte hex', () => {
    assert.equal(normalizePrivateKey(KEY_ONE.toUpperCase()), KEY_ONE);
    assert.equal(normalizePrivateKey(`0x${KEY_TWO}`), KEY_TWO);
    assert.equal(normalizePrivateKey(`0X${KEY_TWO.toUpperCase()}`), KEY_TWO);
});

test('private-key text supports comments and removes duplicates', () => {
    assert.deepEqual(parsePrivateKeysText(`
# primary
${KEY_ONE}
0x${KEY_TWO} # secondary
${KEY_ONE}
`), [KEY_ONE, KEY_TWO]);
});

test('private-key text reports invalid lines instead of silently ignoring them', () => {
    assert.throws(
        () => parsePrivateKeysText(`${KEY_ONE}\nnot-a-private-key`),
        /Line 2: Private key must be 32-byte hex/,
    );
    assert.throws(() => parsePrivateKeysText(' # comments only '), /No private keys found/);
});

test('private-key scalar must be inside the secp256k1 range', () => {
    assert.throws(() => normalizePrivateKey('0'.repeat(64)), /outside the valid secp256k1 range/);
    assert.throws(
        () => normalizePrivateKey('fffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd0364141'),
        /outside the valid secp256k1 range/,
    );
});

test('plain and prefixed keys derive the same FrostBit address', async () => {
    const plain = await deriveAccountFromRawHex(KEY_ONE);
    const prefixed = await deriveAccountFromRawHex(`0x${KEY_ONE}`);
    assert.equal(prefixed.address, plain.address);
    assert.match(plain.address, /^fbc1q/);
    assert.equal(plain.rawPriv.length, 32);
    assert.equal(plain.pubKeyCompressed.length, 33);
});
