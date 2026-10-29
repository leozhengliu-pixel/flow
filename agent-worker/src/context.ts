/**
 * Per-run context handed to the Agents SDK (`run(..., { context })`). It must stay plain JSON: RunState
 * serializes it, so a run suspended on an approval or a question resumes with the same policy state in any
 * worker process.
 */
export type TurnCall = { callId: string; name: string; arguments: string }

export type FlowRunContext = {
  sessionId: string
  /** The user's latest request. */
  request: string
  /** Chat tool groups available to this request (core tools are always on); load_tools adds more. */
  loadedGroups: string[]
  /** Run-token tool allowlist; undefined = every Flow tool the token can see. */
  allowlist?: string[]
  /** The function calls of each model turn in this request, in order (from the model observer). */
  turns: TurnCall[][]
  /** Connector (external_) tools whose connector is set to always ask; the only calls that wait for approval. */
  alwaysAskTools?: string[]
  /** ask_user answers by call id, filled in by the host before it approves the held call. */
  answers: Record<string, string>
  /** Every tool Flow lists is offered (a loop run: Flow lists exactly the loop's tools). */
  allTools?: boolean
}

export function newRunContext(sessionId: string, request: string, loadedGroups: string[], allowlist?: string[]): FlowRunContext {
  return { sessionId, request, loadedGroups, allowlist, turns: [], answers: {} }
}
