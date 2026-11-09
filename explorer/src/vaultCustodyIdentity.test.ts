// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, type App } from 'vue'
import { createI18n } from 'vue-i18n'
import DkgTimeline from './components/frost/DkgTimeline.vue'
import en from './locales/en'
import type { DKGSession, FrostVaultOverview } from './types'

const mocks = vi.hoisted(() => ({ vaults: vi.fn(), activity: vi.fn(), progress: vi.fn(), migration: vi.fn(), subscribe: vi.fn() }))
vi.mock('./api', () => ({ fetchFrostVaults: mocks.vaults, fetchFrostVaultActivity: mocks.activity, fetchFrostDKGProgress: mocks.progress, fetchFrostMigrationDetail: mocks.migration }))
vi.mock('./useWebSocket', () => ({ subscribe: mocks.subscribe, unsubscribe: vi.fn() }))
vi.mock('mermaid', () => ({ default: { initialize: vi.fn(), render: vi.fn(async () => ({ svg: '<svg />' })) } }))

let app: App<Element> | undefined
let root: HTMLDivElement | undefined
beforeEach(() => {
  vi.clearAllMocks()
  mocks.subscribe.mockImplementation((_topic, _params, callback) => callback([]))
  mocks.activity.mockResolvedValue({ items: [], page: 1, page_size: 6, has_more: false })
})
afterEach(() => { app?.unmount(); root?.remove() })

async function mountVault(changes: Partial<FrostVaultOverview>, sessions: DKGSession[] = []) {
  mocks.subscribe.mockImplementation((_topic, _params, callback) => callback(sessions))
  mocks.vaults.mockResolvedValue([{
    chain: 'eth', vault_id: 0, key_epoch: 1, sign_algo: 'SCHNORR_ALT_BN128', status: 'ACTIVE',
    active_since_height: 1, committee_members: ['a', 'b', 'c'], committee_threshold: 2, group_pubkey: 'operator-key', ...changes,
  }])
  root = document.createElement('div')
  document.body.appendChild(root)
  app = createApp({ render: () => h(DkgTimeline, { node: 'node-1' }) })
  app.use(createI18n({ legacy: false, locale: 'en', messages: { en } }))
  app.mount(root)
  for (let i = 0; i < 4; i++) { await Promise.resolve(); await nextTick() }
  root.querySelector<HTMLElement>('.card-top')!.click()
  for (let i = 0; i < 4; i++) { await Promise.resolve(); await nextTick() }
  return root
}

describe('vault custody identity', () => {
  it('names operator and fixed guardian keys from the existing vault overview request', async () => {
    const root = await mountVault({ authority_protocol: 3, guardian_pubkey: 'guardian-key' })
    const identity = root.querySelector('[data-testid="vault-guardian-identity"]')!
    expect(root.textContent).toContain('OPERATOR COMMITTEE THRESHOLD PUBLIC KEY')
    expect(root.textContent).toContain('operator-key')
    expect(identity.textContent).toContain('FIXED GUARDIAN PUBLIC KEY')
    expect(identity.textContent).toContain('guardian-key')
    expect(identity.textContent).toContain('BN128')
    expect(identity.textContent).toContain('V3')
    expect(root.querySelector('[data-testid="vault-custody-policy"]')?.textContent).toContain('not another threshold committee')
    expect(mocks.vaults).toHaveBeenCalledTimes(1)
    expect(mocks.progress).not.toHaveBeenCalled()
  })

  it('shows missing custody configuration as unknown', async () => {
    const root = await mountVault({})
    expect(root.querySelector('[data-testid="vault-guardian-identity"]')?.textContent).toContain('Unknown')
    expect(root.querySelector('[data-testid="vault-custody-policy"]')?.textContent).toContain('unavailable')
    expect(root.querySelector('[data-testid="vault-custody-policy"]')?.textContent).not.toContain('must also authorize')
  })

  it('keeps the current operator key distinct from the pending DKG key', async () => {
    const root = await mountVault({ authority_protocol: 3, guardian_pubkey: 'guardian-key' }, [{
      chain: 'eth', vault_id: 0, epoch_id: 2, sign_algo: 'SCHNORR_ALT_BN128', trigger_height: 10,
      old_committee_members: ['a', 'b', 'c'], new_committee_members: ['b', 'c', 'd'], dkg_status: 'KEY_READY',
      dkg_session_id: 'next-dkg', dkg_threshold_t: 2, dkg_n: 3, dkg_commit_deadline: 0,
      dkg_dispute_deadline: 0, dkg_sharing_deadline: 0, dkg_attempt: 1,
      validation_status: 'PASSED', lifecycle: 'DRAINING', vault_status: 'ACTIVE', new_group_pubkey: 'pending-key',
    }])
    expect(root.querySelector('[data-testid="vault-operator-identity"]')?.textContent).toContain('operator-key')
    expect(root.querySelector('[data-testid="vault-operator-identity"]')?.textContent).not.toContain('pending-key')
    expect(root.querySelector('[data-testid="vault-dkg-new-key"]')?.textContent).toContain('pending-key')
    expect(mocks.vaults).toHaveBeenCalledTimes(1)
  })
})
