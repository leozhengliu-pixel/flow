import type { AgentInputItem } from '@openai/agents'

/**
 * History compaction applied to every model call (`callModelInputFilter`); the stored history stays complete.
 *  1. Tool results from earlier requests longer than `maxOldResultChars` are cut to a head plus a note (a
 *     250-issue list stays in history otherwise and is re-sent, and billed, on every later turn).
 *  2. If the estimate still exceeds `budgetTokens`, whole earlier requests (user message up to the next user
 *     message, so call/result pairs stay together) are dropped oldest first, keeping the last `keepRequests`.
 * A summary turn (model-written compaction) can replace step 2 later; this keeps the worker provider-neutral.
 */
export type CompactionOptions = { maxOldResultChars: number; budgetTokens: number; keepRequests: number }

export const defaultCompaction = (contextWindow: number): CompactionOptions => ({ maxOldResultChars: 6_000, budgetTokens: Math.floor(contextWindow * 0.6), keepRequests: 4 })

const isUser = (item: AgentInputItem) => item.type === 'message' && item.role === 'user'
export const estimateTokens = (items: AgentInputItem[]) => Math.ceil(JSON.stringify(items).length / 3.5)

export function compactHistory(input: AgentInputItem[], options: CompactionOptions): { input: AgentInputItem[]; elided: number; dropped: number } {
  const lastUser = input.findLastIndex(isUser)
  let elided = 0
  let items = input.map((item, index) => {
    if (index >= lastUser || item.type !== 'function_call_result') return item
    const output = item.output as any
    const text: string | undefined = typeof output === 'string' ? output : output?.type === 'text' ? output.text : undefined
    if (text === undefined || text.length <= options.maxOldResultChars) return item
    elided++
    const cut = `${text.slice(0, 1_500)}\n… [${text.length - 1_500} more characters from an earlier turn were elided; call the tool again if you need them]`
    return { ...item, output: { type: 'text', text: cut } } as AgentInputItem
  })
  let dropped = 0
  while (estimateTokens(items) > options.budgetTokens) {
    const starts = items.flatMap((item, index) => (isUser(item) ? [index] : []))
    if (starts.length <= options.keepRequests) break
    items = items.slice(starts[1])
    dropped++
  }
  return { input: items, elided, dropped }
}
