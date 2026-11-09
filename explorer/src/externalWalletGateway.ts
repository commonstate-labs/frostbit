import {
  approveDepositToken,
  readDepositProof,
  readDepositTokenDecimals,
  readDepositTokenMetadata,
  verifyDepositVault,
} from './deposit'
import { externalWalletCore } from './externalWallet'

export {
  ExternalWalletConnectionError,
  externalWalletErrorMessage,
  type BTCBroadcastTransaction,
  type ExternalWalletConnection,
  type ExternalWalletID,
  type ExternalWalletKind,
  type ExternalVaultAuthority,
  type ExternalVaultAuthorityCall,
  type ExternalWithdrawalCacheIdentity,
  type ExternalWithdrawalExecution,
  type ExternalWithdrawalExecutionState,
} from './externalWallet'

                                                                        
                                                                              
                                                                            
                                                       
export const externalWalletGateway = {
  ...externalWalletCore,
  tokens: {
    readDecimals: readDepositTokenDecimals,
    readMetadata: readDepositTokenMetadata,
  },
  deposit: {
    readTokenDecimals: readDepositTokenDecimals,
    readTokenMetadata: readDepositTokenMetadata,
    verifyVault: verifyDepositVault,
    approveToken: approveDepositToken,
    readProof: readDepositProof,
  },
} as const
