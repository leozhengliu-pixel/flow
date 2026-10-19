import type { RunnerPaths } from '../config.js'
import type { StreamMapper, MapperOptions } from '../mapping/common.js'
import type { BootstrapResponse, HarnessName, Turn } from '../protocol.js'

export interface McpServerCommand {
  command: string
  args: string[]
  env: Record<string, string>
}

export interface HarnessContext {
  bootstrap: BootstrapResponse
  /** The sandbox token: the harness's API key for Flow's LLM proxy. */
  token: string
  paths: RunnerPaths
  turn: Turn
  /** The harness session to resume (previous turn), if any. */
  resumeId?: string
  /** Guidance appended to the system prompt (bootstrap guidance plus the runner's rules). */
  guidance: string
  mcp: McpServerCommand
  /** Base environment for the harness (PATH, proxies, plain env vars, FLOW_SANDBOX…). */
  env: NodeJS.ProcessEnv
}

export interface HarnessFile {
  path: string
  content: string
  mode?: number
}

export interface HarnessInvocation {
  command: string
  args: string[]
  env: NodeJS.ProcessEnv
  /** The prompt, written to stdin (avoids argv limits and option injection). */
  stdin: string
  cwd: string
  /** Config files written (0600) before spawning. */
  files: HarnessFile[]
}

export interface HarnessAdapter {
  readonly name: HarnessName
  invocation(ctx: HarnessContext): HarnessInvocation
  createMapper(options: MapperOptions): StreamMapper
  /** Paths (relative to $HOME) holding the harness's session files, saved as harness state. */
  stateDirs(): string[]
}

/** Environment variables that must never reach a harness: real provider keys. */
export const PROVIDER_KEY_VARS = [
  'ANTHROPIC_API_KEY',
  'ANTHROPIC_AUTH_TOKEN',
  'OPENAI_API_KEY',
  'OPENAI_BASE_URL',
  'CODEX_API_KEY',
  'OPENROUTER_API_KEY',
  'GEMINI_API_KEY',
  'GOOGLE_API_KEY',
  'AWS_ACCESS_KEY_ID',
  'AWS_SECRET_ACCESS_KEY',
  'AWS_SESSION_TOKEN',
  'CLAUDE_CODE_USE_BEDROCK',
  'CLAUDE_CODE_USE_VERTEX',
]

export function withoutProviderKeys(env: NodeJS.ProcessEnv): NodeJS.ProcessEnv {
  const out = { ...env }
  for (const name of PROVIDER_KEY_VARS) delete out[name]
  return out
}

/** The prompt text the harness receives: the turn prompt plus attachment links. */
export function turnPrompt(turn: Turn): string {
  const attachments = (turn.attachments ?? []).filter(Boolean)
  if (!attachments.length) return turn.prompt
  return `${turn.prompt}\n\nAttachments:\n${attachments.map((url) => `- ${url}`).join('\n')}`
}

export function resumeFailed(text: string): boolean {
  return /no conversation found|session not found|no session found|thread not found|could not find (the )?session|failed to resume|unable to resume/i.test(text)
}
