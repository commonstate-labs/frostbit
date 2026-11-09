import { beforeEach, describe, expect, it, vi } from 'vitest'

function provider(overrides: Record<string, unknown> = {}) {
  return {
    requestAccounts: vi.fn().mockResolvedValue(['fbc1alice']),
    getAccounts: vi.fn().mockResolvedValue(['fbc1alice']),
    signMessage: vi.fn().mockResolvedValue({
      signature: 'signature',
      address: 'fbc1alice',
      publicKey: 'public-key',
    }),
    ...overrides,
  }
}

describe('wallet authentication helpers', () => {
  beforeEach(() => {
    vi.resetModules()
    vi.stubGlobal('window', { frostbit: provider() })
  })

  it('connects and returns the structured message signature', async () => {
    const { connectWallet, signWalletMessage, wallet } = await import('./walletStore')

    await expect(connectWallet()).resolves.toBe('fbc1alice')
    await expect(signWalletMessage('challenge')).resolves.toEqual({
      signature: 'signature',
      address: 'fbc1alice',
      publicKey: 'public-key',
    })
    expect(wallet).toMatchObject({ connected: true, address: 'fbc1alice' })
    expect(window.frostbit.signMessage).toHaveBeenCalledWith('challenge')
  })

  it('updates the reactive account when the provider account changes', async () => {
    const injected = provider({ getAccounts: vi.fn().mockResolvedValue(['fbc1bob']) })
    vi.stubGlobal('window', { frostbit: injected })
    const { connectWallet, refreshWalletAccount, wallet } = await import('./walletStore')

    await connectWallet()
    await expect(refreshWalletAccount()).resolves.toBe('fbc1bob')
    expect(wallet).toMatchObject({ connected: true, address: 'fbc1bob' })
  })

  it('does not silently reconnect after an explicit disconnect', async () => {
    const { connectWallet, disconnectWallet, refreshWalletAccount, wallet } = await import('./walletStore')

    await connectWallet()
    disconnectWallet()
    await refreshWalletAccount()

    expect(wallet).toMatchObject({ connected: false, address: '' })
    expect(window.frostbit.getAccounts).not.toHaveBeenCalled()
  })

  it('rejects incomplete signing responses', async () => {
    vi.stubGlobal('window', {
      frostbit: provider({ signMessage: vi.fn().mockResolvedValue({ signature: 'signature' }) }),
    })
    const { signWalletMessage } = await import('./walletStore')

    await expect(signWalletMessage('challenge')).rejects.toThrow('incomplete signature')
  })
})
