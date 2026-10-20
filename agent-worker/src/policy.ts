import type { FlowRunContext } from './context.js'

/**
 * Port of api/cmd/server/agent_approval_policy.go (+ agentToolNeedsApproval in agent_stream.go) as an Agents SDK
 * `needsApproval` policy. Flow writes apply immediately, deletes (one item or many) and status updates the agent
 * wrote itself included. The only calls that ask are tools of an MCP connector set to always ask
 * (`context.alwaysAskTools`); a worker without that connector information asks for nothing.
 */
export type ApprovalKind = 'connector'

const approvalWords = new Set([
  'approve', 'approved', 'yes', 'y', 'ok', 'okay', 'sure', 'confirm', 'confirmed', 'proceed', 'go ahead', 'do it', 'yes please', 'lgtm',
  '批准', '同意', '确认', '确定', '是', '是的', '好', '好的', '可以', '继续', '执行', '通过', '行', '没问题',
])

/** agentApprovalAnswer: a reply that approves (anything else rejects). */
export function isApprovalWord(text: string): boolean {
  const normalized = text.trim().toLowerCase().replace(/[ .!。！~]+$/u, '').split(/\s+/).join(' ')
  return approvalWords.has(normalized)
}

export function approvalKind(context: FlowRunContext, tool: string): ApprovalKind | null {
  return tool.startsWith('external_') && context.alwaysAskTools?.includes(tool) ? 'connector' : null
}

/** The question an approval shows. */
export function approvalPreview(_kind: ApprovalKind, calls: { name: string; arguments: string }[]): string {
  return `Run ${calls.map(call => `**${call.name}**`).join(', ')} with an external connector?`
}
