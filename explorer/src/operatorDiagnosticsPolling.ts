export const OPERATOR_DIAGNOSTICS_REFRESH_MS = 10_000

export interface OperatorDiagnosticsPoller {
  sync(enabled: boolean): void
  stop(): void
}

export function createOperatorDiagnosticsPoller(
  refresh: () => void | Promise<void>,
  intervalMs = OPERATOR_DIAGNOSTICS_REFRESH_MS,
): OperatorDiagnosticsPoller {
  let timer: ReturnType<typeof setInterval> | null = null

  function stop() {
    if (timer === null) return
    clearInterval(timer)
    timer = null
  }

  function sync(enabled: boolean) {
    stop()
    if (!enabled) return
    void refresh()
    timer = setInterval(() => { void refresh() }, intervalMs)
  }

  return { sync, stop }
}
