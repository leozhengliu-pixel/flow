/**
 * Per-run context handed to the Agents SDK (`run(..., { context })`). It must stay plain JSON: RunState
 * serializes it, so a run suspended on an approval or a question resumes with the same policy state in any
 * worker process.
 */
export type TurnCall = { callId: string; name: string; arguments: string }

export type FlowRunContext = {
  sessionId: string
  /** The user's latest request; approval rules read it (publish intent, approval words). */
  request: string
  /** Chat tool groups available to this request (core tools are always on); load_tools adds more. */
  loadedGroups: string[]
  /** Run-token tool allowlist; undefined = every Flow tool the token can see. */
  allowlist?: string[]
  /** The function calls of each model turn in this request, in order (from the model observer). */
  turns: TurnCall[][]
  /** Deletes that already ran in this request (sequential deletes ask before the second one). */
  deletesDone: number
  /** The user approved deletes for the rest of this request. */
  deletesApproved: boolean
  /** ask_user answers by call id, filled in by the host before it approves the held call. */
  answers: Record<string, string>
}

export function newRunContext(sessionId: string, request: string, loadedGroups: string[], allowlist?: string[]): FlowRunContext {
  return { sessionId, request, loadedGroups, allowlist, turns: [], deletesDone: 0, deletesApproved: false, answers: {} }
}

export function turnOf(context: FlowRunContext, callId: string | undefined): TurnCall[] {
  if (!callId) return []
  return context.turns.find(turn => turn.some(call => call.callId === callId)) ?? []
}
