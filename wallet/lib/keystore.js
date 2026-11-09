   
                  
                                                                          
  
                                                                         
  
                                                                                     
                                                                       
   

import { generateMnemonic, mnemonicToSeedSync, validateMnemonic } from '@scure/bip39';
import { wordlist } from '@scure/bip39/wordlists/english';
import { HDKey } from '@scure/bip32';
import { secp256k1 } from '@noble/curves/secp256k1';
import { sha256 } from '@noble/hashes/sha256';
import { ripemd160 } from '@noble/hashes/ripemd160';
import { bech32 } from '@scure/base';
import { getCompressedPubKey, fromHex } from './crypto.js';
import { normalizePrivateKey } from './private_key_import.js';

const COIN_TYPE = 9999;
const BASE_PATH = `m/44'/${COIN_TYPE}'/0'/0`;

                                                                                                                                                                                                                    

export const generateMnemonicPhrase = () => generateMnemonic(wordlist, 128);
export const isMnemonicValid = (phrase) => validateMnemonic(phrase, wordlist);

                                                                                                                                                              

   
                                                                                
                                                
                                                            
   
export function deriveAddress(compressedPubKey) {
    const hash160 = ripemd160(sha256(compressedPubKey));
    const words = bech32.toWords(hash160);
    return bech32.encode('fbc', new Uint8Array([0, ...words]));
}

                                                                                                                                                                                  

export async function deriveAccount(phrase, index = 0) {
    const seed = mnemonicToSeedSync(phrase);
    const child = HDKey.fromMasterSeed(seed).derive(`${BASE_PATH}/${index}`);
    return _accountFromRawPriv(child.privateKey);
}

export async function deriveAccountFromRawHex(hexKey) {
    const raw = fromHex(normalizePrivateKey(hexKey));
    return _accountFromRawPriv(raw);
}

                                                                              
async function _accountFromRawPriv(rawPriv) {
    const pubCompressed = getCompressedPubKey(rawPriv);             
    const address = deriveAddress(pubCompressed);
    return { rawPriv, pubKeyCompressed: pubCompressed, address };
}

                                                                                                                                                                              

const STORAGE_KEY = 'frostbit_keystore';
const IMPORTED_KEY = 'frostbit_imported_keys';

async function _aesEncrypt(plaintext, password) {
    const enc = new TextEncoder();
    const km = await crypto.subtle.importKey('raw', enc.encode(password), 'PBKDF2', false, ['deriveKey']);
    const salt = crypto.getRandomValues(new Uint8Array(16));
    const aesKey = await crypto.subtle.deriveKey(
        { name: 'PBKDF2', salt, iterations: 600_000, hash: 'SHA-256' },
        km, { name: 'AES-GCM', length: 256 }, false, ['encrypt']
    );
    const iv = crypto.getRandomValues(new Uint8Array(12));
    const ct = await crypto.subtle.encrypt({ name: 'AES-GCM', iv }, aesKey, enc.encode(plaintext));
    return { salt: Array.from(salt), iv: Array.from(iv), ct: Array.from(new Uint8Array(ct)), version: 1 };
}

async function _aesDecrypt(ks, password) {
    const enc = new TextEncoder();
    const km = await crypto.subtle.importKey('raw', enc.encode(password), 'PBKDF2', false, ['deriveKey']);
    const aesKey = await crypto.subtle.deriveKey(
        { name: 'PBKDF2', salt: new Uint8Array(ks.salt), iterations: 600_000, hash: 'SHA-256' },
        km, { name: 'AES-GCM', length: 256 }, false, ['decrypt']
    );
    const plain = await crypto.subtle.decrypt(
        { name: 'AES-GCM', iv: new Uint8Array(ks.iv) }, aesKey, new Uint8Array(ks.ct)
    );
    return new TextDecoder().decode(plain);
}

export async function saveKeystore(phrase, password) {
    await chrome.storage.local.set({ [STORAGE_KEY]: await _aesEncrypt(phrase, password) });
}

export async function loadKeystore(password) {
    const { [STORAGE_KEY]: ks } = await chrome.storage.local.get(STORAGE_KEY);
    if (!ks) throw new Error('No keystore found');
    return _aesDecrypt(ks, password);
}

export async function hasKeystore() {
    const { [STORAGE_KEY]: ks } = await chrome.storage.local.get(STORAGE_KEY);
    return !!ks;
}

export async function clearKeystore() {
    await chrome.storage.local.remove([STORAGE_KEY, IMPORTED_KEY]);
}

                                                                                                                                                                             

   
                                                                  
                            
   
export async function saveImportedKeys(hexKeys, password) {
    if (!Array.isArray(hexKeys) || hexKeys.length === 0) {
        await chrome.storage.local.remove(IMPORTED_KEY);
        return;
    }
    const payload = JSON.stringify([...new Set(hexKeys.map(normalizePrivateKey))]);
    await chrome.storage.local.set({ [IMPORTED_KEY]: await _aesEncrypt(payload, password) });
}

   
                                                  
                                               
   
export async function loadImportedAccounts(password) {
    const { [IMPORTED_KEY]: ks } = await chrome.storage.local.get(IMPORTED_KEY);
    if (!ks) return [];
    const json = await _aesDecrypt(ks, password);
    const hexKeys = JSON.parse(json);
    if (!Array.isArray(hexKeys)) throw new Error('Invalid imported-key store');
    return Promise.all(hexKeys.map(k => deriveAccountFromRawHex(k)));
}

export async function hasImportedKeys() {
    const { [IMPORTED_KEY]: ks } = await chrome.storage.local.get(IMPORTED_KEY);
    return !!ks;
}
