import { describe, expect, it } from 'vitest'
import commandSource from '../components/command/command-menu.tsx?raw'
import agentSource from '../components/agent/agent-page.tsx?raw'
import { zhCN } from './translations'

describe('command navigation translations', () => {
  it('translates exact navigation labels and the navigation button', () => {
    const labels = [...commandSource.matchAll(/label: '(Go to [^']+)'/g)].map(match => match[1])
    expect(labels).toContain('Go to Initiatives')
    expect(agentSource).toContain('t("Open navigation")')
    for (const label of [...labels, 'Open navigation']) {
      const translated = (zhCN as Record<string, string>)[label]
      expect(translated, label).toBeDefined()
      expect(translated?.replace(/Agent|Loops/g, ''), label).not.toMatch(/[a-z]{3,}/i)
    }
  })
})
