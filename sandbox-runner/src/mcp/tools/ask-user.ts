import { mkdir, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { textResult, type McpServer } from '../server.js'

// ask_user: the agent asks the person who started the session a question.
// The harness stream mapping turns the call into an `elicitation` event
// (phase awaitingUser). The tool also leaves a marker for the runner, so the
// turn completes with status awaitingInput even if a harness names the tool
// differently. The answer arrives as the next turn (inbox kind followUp).

export const ASK_USER_TOOL = 'ask_user'

export function elicitationMarker(flowDir: string, turnId: string | undefined): string {
  return join(flowDir, `elicitation-${turnId || 'current'}.json`)
}

export function registerAskUser(server: McpServer): void {
  server.registerTool(
    ASK_USER_TOOL,
    {
      description:
        'Ask the person who started this coding session a question when you cannot proceed without their decision ' +
        '(ambiguous requirements, choosing between approaches, missing access). After calling it, stop working and end ' +
        'your turn: their answer arrives as the next message. Do not use it for routine progress updates.',
      inputSchema: {
        type: 'object',
        properties: {
          question: { type: 'string', description: 'The question, self-contained and concise.' },
          options: { type: 'array', items: { type: 'string' }, description: 'Optional answer choices.' },
        },
        required: ['question'],
        additionalProperties: false,
      },
    },
    async (args, context) => {
      const question = typeof args.question === 'string' ? args.question.trim() : ''
      if (!question) return textResult('ask_user needs a non-empty "question".', true)
      const options = Array.isArray(args.options) ? args.options.filter((option): option is string => typeof option === 'string') : []
      try {
        await mkdir(context.flowDir, { recursive: true, mode: 0o700 })
        await writeFile(elicitationMarker(context.flowDir, context.turnId), JSON.stringify({ question, options, at: new Date().toISOString() }), { mode: 0o600 })
      } catch {
        // The stream mapping still reports the elicitation.
      }
      return textResult('Your question was sent to the user. Stop now and end your turn without further tool calls; their answer will arrive as the next message.')
    },
  )
}
