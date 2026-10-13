import { tool, type RunContext } from '@openai/agents'
import type { FlowRunContext } from './context.js'
import { toolGroups } from './tool-groups.js'

/**
 * Host tools: served by the worker itself, never by Flow's /mcp.
 *
 * ask_user (agent_ask_user.go) uses the SDK's approval interruption: `needsApproval: true` makes the run stop
 * with a RunToolApprovalItem carrying the question and options. The host shows them as a select elicitation;
 * when the user answers it stores the answer in `context.answers[callId]` and approves the item, and the tool
 * then returns the answer to the model. The interrupted RunState serializes (context included), so the wait can
 * outlive the process.
 */
export const askUserTool = tool<any, FlowRunContext>({
  name: 'ask_user',
  description:
    'Ask the user to choose between options and stop until they answer. The app shows the question with the options as buttons; ' +
    'the user picks one or types something else. Use it when a required value is genuinely ambiguous — not for choices you can ' +
    'reasonably make yourself. Ask one question per call and call nothing else in the same turn.',
  strict: false,
  parameters: {
    type: 'object',
    required: ['question', 'options'],
    properties: {
      question: { type: 'string', description: 'The question, as you would say it to the user (markdown allowed)' },
      options: {
        type: 'array',
        minItems: 2,
        maxItems: 6,
        items: {
          type: 'object',
          required: ['label'],
          properties: {
            label: { type: 'string', description: 'Button text, 1-4 words' },
            value: { type: 'string', description: 'What the answer means, if different from the label' },
          },
          additionalProperties: false,
        },
      },
      allowFreeText: { type: 'boolean', description: 'Whether the user may answer with something else instead (default true)' },
    },
    additionalProperties: false,
  },
  needsApproval: true,
  execute: async (_input: unknown, runContext?: RunContext<FlowRunContext>, details?: { toolCall?: { callId?: string } }) => {
    const callId = details?.toolCall?.callId ?? ''
    const answer = runContext?.context.answers[callId]
    return JSON.stringify(answer === undefined ? { answer: null, note: 'The user did not answer.' } : { answer })
  },
})

export type AskUserRequest = { question: string; options: { label: string; value: string }[]; allowFreeText: boolean }

/** Reads an ask_user call's arguments into the elicitation the UI shows (parseAskUser). */
export function parseAskUser(raw: string | undefined): AskUserRequest {
  let args: any = {}
  try {
    args = JSON.parse(raw ?? '{}')
  } catch {
    // shown as a free-text question
  }
  const seen = new Set<string>()
  const options: AskUserRequest['options'] = []
  for (const option of Array.isArray(args.options) ? args.options : []) {
    const label = String(option?.label ?? '').trim()
    const value = String(option?.value ?? '').trim() || label
    if (!label || label.length > 80 || value.length > 500 || seen.has(value.toLowerCase())) continue
    seen.add(value.toLowerCase())
    options.push({ label, value })
  }
  return { question: String(args.question ?? '').trim(), options: options.slice(0, 6), allowFreeText: args.allowFreeText !== false }
}

/** load_tools (agentLoadToolsTool): adds tool groups to this request; the next model turn offers their tools. */
export const loadToolsTool = tool<any, FlowRunContext>({
  name: 'load_tools',
  description:
    'Load more Flow tools for this request. Groups: ' + toolGroups.map(group => `${group.name} (${group.summary})`).join('; ') + '.',
  strict: false,
  parameters: {
    type: 'object',
    required: ['groups'],
    properties: { groups: { type: 'array', items: { type: 'string', enum: toolGroups.map(group => group.name) } } },
    additionalProperties: false,
  },
  execute: async (input: any, runContext?: RunContext<FlowRunContext>) => {
    const requested: string[] = Array.isArray(input?.groups) ? input.groups : []
    const unknown = requested.filter(name => !toolGroups.some(group => group.name === name))
    if (!requested.length || unknown.length) return JSON.stringify({ error: unknown.length ? `unknown group ${unknown[0]}` : 'groups is required' })
    const context = runContext!.context
    context.loadedGroups = [...new Set([...context.loadedGroups, ...requested])]
    const tools = toolGroups.filter(group => requested.includes(group.name)).flatMap(group => group.tools)
    return JSON.stringify({ loaded: requested, tools, note: 'These tools are available from your next step.' })
  },
})
