// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, type App } from 'vue'
import { createI18n } from 'vue-i18n'
import en from './locales/en'
import HandoverClaimActions from './components/frost/HandoverClaimActions.vue'
import { validateWithdrawResult } from './withdrawClaim'
import { signedResult } from './withdrawClaimFixtures.test-helper'
const mocks = vi.hoisted(() => ({ state: vi.fn(), submit: vi.fn(), attempt: vi.fn(), page: vi.fn(), batch: vi.fn() }))
vi.mock('./api', () => ({ fetchFrostHandoverPage: mocks.page, fetchFrostWithdrawBatch: mocks.batch }))
vi.mock('./externalWalletGateway', async original => {
 const actual = await original<any>()
 return { ...actual, externalWalletGateway: { withdrawal: { readV2State: mocks.state, submitV2Step: mocks.submit, readV2StepAttempt: mocks.attempt, readSubmission: async () => undefined } } }
})
let app: App | undefined
async function flush() { for(let i=0;i<12;i++){await Promise.resolve();await nextTick()} }
async function click(root: HTMLElement, label: string) { const button = Array.from(root.querySelectorAll('button')).find(b=>b.textContent?.includes(label)); if(!button)throw Error(label);button.click();await flush() }
afterEach(()=>{app?.unmount();vi.resetAllMocks()})
function mount(epoch: string, submitted = vi.fn()) {
 const result = signedResult({ authority_protocol: 3, dex_chain_id:'51025', epoch })
 const root = document.createElement('div')
 app=createApp({render:()=>h(HandoverClaimActions,{node:'node-1',call:validateWithdrawResult(result),connection:{kind:'evm',account:`0x${'44'.repeat(20)}`,legacy:false,provider:{request:vi.fn()}},feeLimitTRX:'100',onSubmitted:submitted})})
 app.use(createI18n({legacy:false,locale:'en',messages:{en}}));app.mount(root)
 return root
}
function pages() { mocks.page.mockImplementation(async (_node,_chain,_vault,from,to)=>{
 const certificates=[];let cursor=BigInt(from)
 while(cursor<BigInt(to)&&certificates.length<8){certificates.push({from_epoch:String(cursor),to_epoch:String(cursor+1n),new_px:'1',new_py:'2',frozen_next_batch_seq:'7',rx:'3',ry:'4',s:'5',guardian_rx:'6',guardian_ry:'7',guardian_s:'8'});cursor++}
 return {authority_protocol: 3,dex_chain_id:'51025',chain_id:'31337',contract:`0x${'11'.repeat(20)}`,chain:'ETH',vault_id:1,from_epoch:from,target_epoch:to,next_epoch:String(cursor),has_more:cursor<BigInt(to),certificates}
 }) }
describe('explicit V2 claiming actions',()=>{
 it('prepares 9 transitions over bounded actions and sends only once per explicit confirmation',async()=>{
  pages();mocks.state.mockResolvedValue({epoch:'1',nextBatchSeq:'7'});mocks.submit.mockResolvedValue(`0x${'aa'.repeat(32)}`);mocks.attempt.mockResolvedValue('confirmed')
  const emitted=vi.fn(),root=mount('10',emitted)
  expect(mocks.submit).not.toHaveBeenCalled()
  await click(root,'Prepare next action')
  expect(root.textContent).toContain('1 → 9 (8 handovers)')
  expect(mocks.submit).not.toHaveBeenCalled()
  await click(root,'Confirm and send')
  expect(mocks.submit).toHaveBeenCalledTimes(1);expect(emitted).not.toHaveBeenCalled()
  mocks.state.mockResolvedValue({epoch:'9',nextBatchSeq:'7'})
  await click(root,'Check external')
  expect(root.textContent).toContain('9 → 10 (1 handovers)')
  expect(mocks.submit).toHaveBeenCalledTimes(1)
  await click(root,'Confirm and send')
  expect(mocks.submit).toHaveBeenCalledTimes(2);expect(emitted).toHaveBeenCalledWith(`0x${'aa'.repeat(32)}`)
 })
 it('keeps an unchanged pending attempt pending and allows a reverted attempt to be prepared again',async()=>{
  pages();mocks.state.mockResolvedValue({epoch:'1',nextBatchSeq:'7'});mocks.submit.mockResolvedValue(`0x${'aa'.repeat(32)}`);mocks.attempt.mockResolvedValue('pending')
  const root=mount('2')
  await click(root,'Prepare next action');await click(root,'Confirm and send');await click(root,'Check external')
  expect(root.textContent).toContain('waiting for on-chain execution');expect(mocks.submit).toHaveBeenCalledTimes(1)
  mocks.attempt.mockResolvedValue('failed');await click(root,'Check external')
  expect(root.textContent).toContain('Confirm and send');expect(mocks.submit).toHaveBeenCalledTimes(1)
 })
})
