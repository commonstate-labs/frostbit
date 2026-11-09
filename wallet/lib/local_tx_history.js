import { toBase64 } from './crypto.js';

                                                                          
                                                                       
                                                                        
export const LOCAL_WITHDRAW_PROOF_MAX_BYTES = 4096;

export function signedWithdrawProofForHistory(txDesc, txBytes) {
    if (txDesc?.type !== 'frost_withdraw') return '';
    if (!(txBytes instanceof Uint8Array) || txBytes.length === 0 || txBytes.length > LOCAL_WITHDRAW_PROOF_MAX_BYTES) return '';
    return toBase64(txBytes);
}
