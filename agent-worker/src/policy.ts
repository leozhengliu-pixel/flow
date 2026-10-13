import { turnOf, type FlowRunContext } from './context.js'

/**
 * Port of api/cmd/server/agent_approval_policy.go (+ agentToolNeedsApproval in agent_stream.go) as an Agents SDK
 * `needsApproval` policy. Flow writes apply immediately except:
 *  - external_* connector tools, which act outside Flow: every call asks;
 *  - deleting more than one item: a model turn whose delete_* calls name two or more targets asks once for the
 *    whole turn; a second sequential delete in the same request asks once for the rest of the request;
 *  - publishing a project or initiative update the agent wrote itself: a NEW save_status_update when the user's
 *    request did not ask to post or publish.
 * A request that is itself an approval word ("yes", "批准") never asks again.
 */
export type ApprovalKind = 'connector' | 'delete' | 'publish'

const publishRequest = /\b(post|publish|share|send|submit)\b|发布|发表|发出|发送|贴出|提交|分享/i

const approvalWords = new Set([
  'approve', 'approved', 'yes', 'y', 'ok', 'okay', 'sure', 'confirm', 'confirmed', 'proceed', 'go ahead', 'do it', 'yes please', 'lgtm',
  '批准', '同意', '确认', '确定', '是', '是的', '好', '好的', '可以', '继续', '执行', '通过', '行', '没问题',
])

/** agentApprovalAnswer: a reply that approves (anything else rejects). */
export function isApprovalWord(text: string): boolean {
  const normalized = text.trim().toLowerCase().replace(/[ .!。！~]+$/u, '').split(/\s+/).join(' ')
  return approvalWords.has(normalized)
}

/** agentDeleteReferences: the items a delete call names. */
export function deleteReferences(args: unknown): string[] {
  if (!args || typeof args !== 'object') return []
  const refs: string[] = []
  for (const [key, value] of Object.entries(args as Record<string, unknown>)) {
    if (!(key === 'id' || key === 'ids' || key === 'commentId' || key === 'draftId' || key.endsWith('Ids'))) continue
    if (typeof value === 'string' && value.trim()) refs.push(value.trim())
    if (Array.isArray(value)) for (const item of value) if (typeof item === 'string' && item.trim()) refs.push(item.trim())
  }
  return refs
}

function parseArgs(raw: unknown): Record<string, unknown> {
  if (raw && typeof raw === 'object') return raw as Record<string, unknown>
  if (typeof raw !== 'string' || !raw) return {}
  try {
    const parsed = JSON.parse(raw)
    return parsed && typeof parsed === 'object' ? parsed : {}
  } catch {
    return {}
  }
}

export function approvalKind(context: FlowRunContext, tool: string, input: unknown, callId?: string): ApprovalKind | null {
  if (tool.startsWith('external_')) return 'connector'
  if (isApprovalWord(context.request)) return null
  const args = parseArgs(input)
  if (tool.startsWith('delete_')) {
    if (context.deletesApproved) return null
    const turn = turnOf(context, callId)
    const sameTurn = turn.length ? turn.filter(call => call.name.startsWith('delete_')) : [{ callId: callId ?? '', name: tool, arguments: JSON.stringify(args) }]
    const targets = sameTurn.flatMap(call => deleteReferences(parseArgs(call.arguments)))
    if (targets.length > 1) return 'delete'
    // Sequential deletes (one per turn): the second delete of one request asks for the rest of the request, so at
    // most one item — restorable from the trash — goes before the question.
    return context.deletesDone > 0 ? 'delete' : null
  }
  if (tool === 'save_status_update' && !String(args.id ?? '').trim() && !publishRequest.test(context.request)) return 'publish'
  return null
}

/** The readable preview an approval shows (agentApprovalPreview, minus the server-side lookups). */
export function approvalPreview(kind: ApprovalKind, calls: { name: string; arguments: string }[]): string {
  if (kind === 'connector') return `Run ${calls.map(call => `**${call.name}**`).join(', ')} with an external connector?`
  if (kind === 'publish') {
    const args = parseArgs(calls[0]?.arguments)
    const body = String(args.body ?? '').trim()
    const target = args.initiative ? `initiative ${args.initiative}` : args.project ? `project ${args.project}` : 'the project'
    return `Post this update to ${target}?\n\n${body.split('\n').map(line => `> ${line}`).join('\n')}${args.health ? `\n\nHealth: ${args.health}` : ''}`
  }
  const targets = calls.flatMap(call => deleteReferences(parseArgs(call.arguments)).map(ref => `- ${call.name.replace('delete_', '')} ${ref}`))
  return `Delete these ${targets.length} items? They go to the trash and can be restored.\n${targets.join('\n')}`
}
