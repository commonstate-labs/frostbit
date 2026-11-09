import { describe, expect, it } from 'vitest'
import { nodeWithOperatorDiagnostics } from './operatorDiagnostics'
import type { NodeSummary, OperatorNodeDiagnostics } from './types'

const leakedPublicNode: NodeSummary = {
  address: 'node-a:6000',
  status: 'ok',
  current_height: 10,
  resources: { process_cpu_pct: 99 },
  block_timing: { total_ms: 99 } as NodeSummary['block_timing'],
  frost_metrics: {
    heap_alloc: 99,
    heap_sys: 99,
    num_goroutine: 99,
    frost_jobs: 99,
    frost_withdraws: 99,
    api_call_stats: { '/secret': 99 },
  },
  is_paused: true,
}

const operatorDiagnostics: OperatorNodeDiagnostics = {
  address: 'node-a:6000',
  resources: { process_cpu_pct: 12 },
  block_timing: { total_ms: 25 } as OperatorNodeDiagnostics['block_timing'],
  frost_metrics: {
    heap_alloc: 1024,
    heap_sys: 2048,
    num_goroutine: 4,
    frost_jobs: 2,
    frost_withdraws: 1,
    api_call_stats: { '/api/health': 3 },
  },
  is_paused: false,
}

describe('nodeWithOperatorDiagnostics', () => {
  it('strips sensitive fields from public summaries for unauthorized viewers', () => {
    const view = nodeWithOperatorDiagnostics(leakedPublicNode, operatorDiagnostics, false)

    expect(view).toMatchObject({ address: 'node-a:6000', status: 'ok', current_height: 10 })
    expect(view.resources).toBeUndefined()
    expect(view.block_timing).toBeUndefined()
    expect(view.frost_metrics).toBeUndefined()
    expect(view.is_paused).toBeUndefined()
  })

  it('uses only the protected response after operator authorization', () => {
    const view = nodeWithOperatorDiagnostics(leakedPublicNode, operatorDiagnostics, true)

    expect(view.resources?.process_cpu_pct).toBe(12)
    expect(view.block_timing?.total_ms).toBe(25)
    expect(view.frost_metrics?.api_call_stats).toEqual({ '/api/health': 3 })
    expect(view.is_paused).toBe(false)
  })
})
