import { reactive } from 'vue'

export interface FrostBitSignMessageResult {
  signature: string
  address: string
  publicKey: string
}

export interface FrostBitProvider {
  requestAccounts(): Promise<string[]>
  getAccounts(): Promise<string[]>
  signMessage(message: string): Promise<FrostBitSignMessageResult>
  [method: string]: any
}

export const wallet = reactive({ address: '', connected: false })

declare global {
                                                                           
                                                                
  interface Window { frostbit?: any }
}

let reconnectSuppressed = false

function setWalletAccount(address = ''): string {
  const next = String(address || '').trim()
  wallet.address = next
  wallet.connected = next.length > 0
  return next
}

function provider(): FrostBitProvider {
  if (!window.frostbit) throw new Error('Please install the FrostBit Wallet extension first')
  return window.frostbit as FrostBitProvider
}

export async function connectWallet(): Promise<string> {
  const accounts = await provider().requestAccounts()
  const address = setWalletAccount(accounts?.[0])
  if (!address) throw new Error('No wallet account was selected')
  reconnectSuppressed = false
  return address
}

                                                                          
                                        
export async function refreshWalletAccount(): Promise<string> {
  if (!window.frostbit || (reconnectSuppressed && !wallet.connected)) return wallet.address
  const accounts = await (window.frostbit as FrostBitProvider).getAccounts()
  return setWalletAccount(accounts?.[0])
}

export async function signWalletMessage(message: string): Promise<FrostBitSignMessageResult> {
  const result = await provider().signMessage(message)
  if (!result || typeof result !== 'object') throw new Error('Wallet returned an invalid signature')

  const signature = String(result.signature || '').trim()
  const address = String(result.address || '').trim()
  const publicKey = String(result.publicKey || '').trim()
  if (!signature || !address || !publicKey) throw new Error('Wallet returned an incomplete signature')
  return { signature, address, publicKey }
}

export function disconnectWallet() {
  reconnectSuppressed = true
  setWalletAccount()
}

export function shortAddr(addr: string) {
  return addr ? `${addr.slice(0, 8)}…${addr.slice(-6)}` : ''
}
