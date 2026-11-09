import type { FlowRunContext } from './context.js'

/**
 * Port of api/cmd/server/agent_approval_policy.go (+ agentToolNeedsApproval in agent_stream.go) as an Agents SDK
 * `needsApproval` policy. Flow writes apply immediately, deletes (one item or many) and status updates the agent
 * wrote itself included. The only calls that ask are the tools Flow's /mcp lists with `_meta["flow/approval"] =
 * "always"` (`context.alwaysAskTools`): tools of an MCP connector set to always ask, and Flow session tools that
 * always wait for the person (file_request, a request filed from a Lark or Feishu group). A worker without that
 * information asks for nothing.
 */
export type ApprovalKind = 'connector' | 'flow'

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
  if (!context.alwaysAskTools?.includes(tool)) return null
  return tool.startsWith('external_') ? 'connector' : 'flow'
}

/** The question an approval shows. */
export function approvalPreview(kind: ApprovalKind, calls: { name: string; arguments: string }[]): string {
  const names = calls.map(call => `**${call.name}**`).join(', ')
  return kind === 'connector' ? `Run ${names} with an external connector?` : `Run ${names}?`
}
