import { parseDetailRoute } from './detailRoutes'

export function transactionIDFromHash(hash: string): string {
  const route = parseDetailRoute(hash)
  return route?.valid && route.kind === 'tx' ? route.id : ''
}
