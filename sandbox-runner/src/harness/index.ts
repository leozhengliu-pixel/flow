import type { HarnessName } from '../protocol.js'
import { claudeHarness } from './claude.js'
import { codexHarness } from './codex.js'
import { opencodeHarness } from './opencode.js'
import type { HarnessAdapter } from './types.js'

const adapters: Record<HarnessName, HarnessAdapter> = {
  claudeCode: claudeHarness,
  codex: codexHarness,
  openSource: opencodeHarness,
}

export function harnessFor(name: string): HarnessAdapter {
  // Legacy values are normalized by Flow, but accept them defensively.
  const normalized = name === 'claude' ? 'claudeCode' : name === 'auto' || name === '' ? 'codex' : name
  const adapter = adapters[normalized as HarnessName]
  if (!adapter) throw new Error(`unknown harness ${JSON.stringify(name)}`)
  return adapter
}
