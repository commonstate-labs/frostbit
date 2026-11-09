   
                
                                                  
                                                                     
                                   
   

import { secp256k1 } from '@noble/curves/secp256k1';
import { sha256 } from '@noble/hashes/sha256';

   
                                                             
                                                  
                                               
                                                        
   
export function sign(rawPrivKey, data) {
    const digest = sha256(data);
    const sig = secp256k1.sign(digest, rawPrivKey);
    return sig.toCompactRawBytes();                 
}

   
                                                   
                                                  
                                                        
   
export function getCompressedPubKey(rawPrivKey) {
    return secp256k1.getPublicKey(rawPrivKey, true);
}

                                
export const toHex = (buf) => Array.from(buf).map(b => b.toString(16).padStart(2, '0')).join('');
export const fromHex = (hex) => new Uint8Array(hex.match(/.{1,2}/g).map(b => parseInt(b, 16)));

                           
export const toBase64 = (buf) => btoa(String.fromCharCode(...buf));
export const fromBase64 = (s) => new Uint8Array(atob(s).split('').map(c => c.charCodeAt(0)));
