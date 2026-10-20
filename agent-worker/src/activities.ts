/**
 * Flow Agent Interaction activities the worker produces (design doc §5). Durable activities go to
 * POST /internal/agent-runtime/runs/{id}/events and are upserted by `activityId`; ephemeral ones (text deltas,
 * reasoning) go to the realtime hub only. The spike logs them as JSONL.
 */
import type { ModelCallRecord } from './models.js'

export type Elicitation =
  | { kind: 'select'; callId: string; body: string; options: { label: string; value: string }[]; allowFreeText: boolean }
  | { kind: 'approval'; approval: 'connector'; callIds: string[]; body: string; options: { label: string; value: string }[] }

export type FlowActivity =
  | { type: 'thought'; at: number; body: string; ephemeral: boolean }
  | { type: 'text.delta'; at: number; delta: string; ephemeral: true }
  | { type: 'action'; at: number; activityId: string; action: string; parameter: unknown }
  | { type: 'action.result'; at: number; activityId: string; action: string; result: string; isError: boolean; change?: unknown }
  | { type: 'elicitation'; at: number; elicitation: Elicitation }
  /** ask_user's question while the model writes it: a running question part under the call id the finished
   *  elicitation (status pending, with options) replaces. */
  | { type: 'question.delta'; at: number; activityId: string; body: string; status: 'running'; ephemeral: true }
  | { type: 'response'; at: number; body: string; usage: TurnUsage }
  | { type: 'error'; at: number; body: string; usage?: TurnUsage }

export type TurnUsage = {
  modelCalls: number
  /** Model call attempts that failed (retried by the runner, or fatal). */
  failedCalls: number
  inputTokens: number
  cachedTokens: number
  outputTokens: number
  toolsOfferedMean: number
}

export function summarizeUsage(calls: ModelCallRecord[]): TurnUsage {
  const ok = calls.filter(call => !call.error)
  return {
    modelCalls: ok.length,
    failedCalls: calls.length - ok.length,
    inputTokens: ok.reduce((sum, call) => sum + call.inputTokens, 0),
    cachedTokens: ok.reduce((sum, call) => sum + call.cachedTokens, 0),
    outputTokens: ok.reduce((sum, call) => sum + call.outputTokens, 0),
    toolsOfferedMean: ok.length ? Math.round(ok.reduce((sum, call) => sum + call.toolsOffered, 0) / ok.length) : 0,
  }
}

/** One line per activity for console logs. */
export function formatActivity(activity: FlowActivity, t0: number): string {
  const at = `+${((activity.at - t0) / 1000).toFixed(2)}s`.padStart(8)
  const clip = (text: string, n = 140) => (text.length > n ? `${text.slice(0, n)}…` : text).replace(/\s+/g, ' ')
  switch (activity.type) {
    case 'thought': return `${at} thought${activity.ephemeral ? ' (ephemeral)' : ''} ${clip(activity.body)}`
    case 'text.delta': return `${at} text.delta ${JSON.stringify(clip(activity.delta, 60))}`
    case 'action': return `${at} action ${activity.action} ${clip(JSON.stringify(activity.parameter), 160)}`
    case 'action.result': return `${at} action.result ${activity.action}${activity.isError ? ' ERROR' : ''}${activity.change ? ' +change' : ''} ${clip(activity.result, 120)}`
    case 'elicitation': {
      const e = activity.elicitation
      return `${at} elicitation[${e.kind === 'select' ? 'select' : `approval:${e.approval}`}] ${clip(e.body)} options=${e.options.map(o => o.label).join('|')}`
    }
    case 'response': {
      const u = activity.usage
      return `${at} response ${clip(activity.body, 300)} | calls=${u.modelCalls} in=${u.inputTokens} cached=${u.cachedTokens} out=${u.outputTokens} tools/call=${u.toolsOfferedMean}`
    }
    case 'question.delta': return `${at} question (running) ${clip(activity.body, 80)}`
    case 'error': return `${at} error ${clip(activity.body, 300)}`
  }
}
