import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  OPERATOR_DIAGNOSTICS_REFRESH_MS,
  createOperatorDiagnosticsPoller,
} from './operatorDiagnosticsPolling'

describe('operator diagnostics polling', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('refreshes immediately and then at the low-frequency interval', () => {
    const refresh = vi.fn()
    const poller = createOperatorDiagnosticsPoller(refresh)

    poller.sync(true)
    expect(refresh).toHaveBeenCalledTimes(1)

    vi.advanceTimersByTime(OPERATOR_DIAGNOSTICS_REFRESH_MS * 2)
    expect(refresh).toHaveBeenCalledTimes(3)
    poller.stop()
  })

  it('does not run while disabled and stops on authorization loss or close', () => {
    const refresh = vi.fn()
    const poller = createOperatorDiagnosticsPoller(refresh)

    poller.sync(false)
    vi.advanceTimersByTime(OPERATOR_DIAGNOSTICS_REFRESH_MS)
    expect(refresh).not.toHaveBeenCalled()

    poller.sync(true)
    expect(refresh).toHaveBeenCalledTimes(1)
    poller.sync(false)
    vi.advanceTimersByTime(OPERATOR_DIAGNOSTICS_REFRESH_MS * 2)
    expect(refresh).toHaveBeenCalledTimes(1)
  })
})
