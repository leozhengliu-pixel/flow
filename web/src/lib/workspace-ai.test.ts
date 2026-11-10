import { expect, it } from 'vitest'
import { workspaceAIAllowed } from './workspace-ai'

it('follows the workspace AI policy: HIPAA mode and the AI switch', () => {
  expect(workspaceAIAllowed(undefined)).toBe(true)
  expect(workspaceAIAllowed({ featureFlags: {} })).toBe(true)
  expect(workspaceAIAllowed({ featureFlags: { 'ai-agent': true } })).toBe(true)
  expect(workspaceAIAllowed({ featureFlags: { 'ai-agent': false } })).toBe(false)
  expect(workspaceAIAllowed({ featureFlags: { ai: false } })).toBe(false)
  // The current switch wins over the older one, as on the server.
  expect(workspaceAIAllowed({ featureFlags: { ai: false, 'ai-agent': true } })).toBe(true)
  expect(workspaceAIAllowed({ featureFlags: { 'ai-agent': true }, hipaaCompliance: true })).toBe(false)
})
