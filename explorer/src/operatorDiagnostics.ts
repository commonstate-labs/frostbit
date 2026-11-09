import type { NodeSummary, OperatorNodeDiagnostics } from './types'

   
                                                                          
                                                     
   
export function nodeWithOperatorDiagnostics(
  node: NodeSummary,
  diagnostics: OperatorNodeDiagnostics | undefined,
  authorized: boolean,
): NodeSummary {
  const operatorData = authorized ? diagnostics : undefined
  return {
    ...node,
    resources: operatorData?.resources,
    block_timing: operatorData?.block_timing,
    frost_metrics: operatorData?.frost_metrics,
    is_paused: operatorData?.is_paused,
  }
}
