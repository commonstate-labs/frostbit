// @vitest-environment happy-dom

import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, type App } from 'vue'
import { createI18n } from 'vue-i18n'
import MigrationTransactionDialog from './components/frost/MigrationTransactionDialog.vue'
import en from './locales/en'
import type { FrostMigrationDetail } from './api'

let app: App<Element> | undefined
let root: HTMLDivElement | undefined

afterEach(() => {
  app?.unmount()
  root?.remove()
  document.body.innerHTML = ''
})

describe('migration transaction dialog', () => {
  function accountDetail(guardianS?: string): FrostMigrationDetail {
    return {
      migration_id: 'migration-eth-0-2', chain: 'ETH', vault_id: 0,
      old_epoch: 1, new_epoch: 2, status: 'SIGNED', intent_height: 100, signed_height: 110,
      no_funds: false, template_hash: 'hash', raw_size_bytes: 0,
      account: {
        authority_protocol: 3, dex_chain_id: '51025', kind: 'evm', chain_id: '1',
        contract_address: '0x1111111111111111111111111111111111111111', method: 'withdrawBatchWithHandovers',
        old_pubkey_x: '1', old_pubkey_y: '2', new_pubkey_x: '3', new_pubkey_y: '4', next_epoch: 2,
        signature_rx: '5', signature_ry: '6', signature_s: '7', guardian_rx: '8', guardian_ry: '9', guardian_s: guardianS, calldata: '',
      },
    }
  }

  function mountDetail(detail: FrostMigrationDetail) {
    root = document.createElement('div')
    document.body.appendChild(root)
    app = createApp({ render: () => h(MigrationTransactionDialog, { open: true, loading: false, error: '', detail, node: 'node.example:6000' }) })
    app.use(createI18n({ legacy: false, locale: 'en', messages: { en } }))
    app.mount(root)
  }

  it('shows both finalized handover signatures while keeping external execution separate', () => {
    mountDetail(accountDetail('10'))
    expect(document.querySelector('[data-testid="migration-operator-signature"]')?.textContent).toContain('s: 7')
    expect(document.querySelector('[data-testid="migration-guardian-signature"]')?.textContent).toContain('s: 10')
    expect(document.querySelector('[data-testid="migration-dual-signature-finalized"]')?.textContent).toContain('permanently stored on-chain')
    expect(document.querySelector('[data-testid="migration-dual-signature-finalized"]')?.textContent).toContain('does not mean the external handover has executed')
  })

  it.each([undefined, '0'])('does not claim finalized dual authorization for a missing or zero guardian signature: %s', guardianS => {
    mountDetail(accountDetail(guardianS))
    expect(document.querySelector('[data-testid="migration-guardian-signature"]')?.textContent).toContain('Complete signature data is unavailable')
    expect(document.querySelector('[data-testid="migration-dual-signature-finalized"]')).toBeNull()
  })

  it('shows each UTXO, output, totals, fee and raw transaction', () => {
    const detail: FrostMigrationDetail = {
      migration_id: 'migration-btc-0-2', chain: 'BTC', vault_id: 0,
      old_epoch: 1, new_epoch: 2, status: 'SIGNED', intent_height: 100, signed_height: 110,
      no_funds: false, template_hash: 'hash', txid: 'root-txid', raw_tx_hex: '02000000', raw_size_bytes: 4,
      btc: {
        version: 2, lock_time: 0, network: 'testnet3', total_input: '125000', total_output: '124500', fee: '500',
        inputs: [{ txid: 'parent-txid', vout: 3, amount: '125000', sequence: 4294967295, script_pubkey: '5120abcd' }],
        outputs: [{ address: 'tb1p-new-vault', amount: '124500' }],
      },
    }
    root = document.createElement('div')
    document.body.appendChild(root)
    app = createApp({ render: () => h(MigrationTransactionDialog, { open: true, loading: false, error: '', detail, node: 'node.example:6000' }) })
    app.use(createI18n({ legacy: false, locale: 'en', messages: { en } }))
    app.mount(root)

    const text = document.body.textContent || ''
    expect(text).toContain('Input UTXOs')
    expect(document.querySelector('[data-testid="btc-migration-dual-signature-hint"]')?.textContent).toContain('operator threshold signature and a fixed guardian signature')
    expect(text).toContain('parent-txid')
    expect(text).toContain('VOUT3')
    expect(text).toContain('Transaction outputs')
    expect(text).toContain('tb1p-new-vault')
    expect(text).toContain('0.00125 BTC · 125,000 sat')
    expect(text).toContain('0.000005 BTC · 500 sat')
    expect(document.body.querySelector('textarea')?.value).toBe('02000000')
  })

  it('shows an account handover proof that will execute with the first withdrawal batch', () => {
    const detail: FrostMigrationDetail = {
      migration_id: 'migration-eth-0-2', chain: 'ETH', vault_id: 0,
      old_epoch: 1, new_epoch: 2, status: 'SIGNED', intent_height: 100, signed_height: 110,
      no_funds: false, template_hash: 'hash', raw_tx_hex: '', raw_size_bytes: 0,
      account: {
        kind: 'evm', chain_id: '1', contract_address: '0x1111111111111111111111111111111111111111',
        method: 'withdrawBatchWithHandover',
        old_pubkey_x: '1', old_pubkey_y: '2', new_pubkey_x: '3', new_pubkey_y: '4', next_epoch: 2,
        signature_rx: '5', signature_ry: '6', signature_s: '7', calldata: '',
      },
    }
    root = document.createElement('div')
    document.body.appendChild(root)
    app = createApp({ render: () => h(MigrationTransactionDialog, { open: true, loading: false, error: '', detail, node: 'node.example:6000' }) })
    app.use(createI18n({ legacy: false, locale: 'en', messages: { en } }))
    app.mount(root)

    const text = document.body.textContent || ''
    expect(text).toContain('Contract interaction')
    expect(text).toContain('0x1111111111111111111111111111111111111111')
    expect(text).toContain('withdrawBatchWithHandover')
    expect(text).toContain('Connect wallet and inspect contract')
    expect(text).toContain('first signed withdrawal batch')
    expect(document.querySelector('[data-testid="migration-dual-signature-finalized"]')).toBeNull()
  })
})
