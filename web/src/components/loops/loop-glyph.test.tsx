import { render } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { LoopIcon, loopIconColor } from './loop-glyph'

describe('LoopIcon', () => {
  it.each([
    ['autofix-bugs', 'Bug', 'svg.lucide-bug', '#26b5ce'],
    ['slack-qa', 'Slack', 'svg.lucide-message-square', '#9b8afb'],
    ['weekly-wrap', 'Calendar', 'svg.lucide-calendar-days', '#5e6ad2'],
    ['feature-request-report', 'Lightbulb', 'svg.lucide-file-text', '#4cb782'],
    ['security-alerts', 'Shield', 'svg.lucide-shield-alert', '#eb5757'],
  ])('uses Linear’s %s icon for loops created from the template', (templateId, icon, selector, color) => {
    const { container } = render(<LoopIcon source={{ templateId, icon }} />)
    expect(container.querySelector(selector)).not.toBeNull()
    expect(container.querySelector('svg')).toHaveAttribute('width', '14')
    expect(loopIconColor({ templateId, icon })).toBe(color)
  })

  it('shows the orange Triage glyph for the triage agent', () => {
    const { container } = render(<LoopIcon source={{ templateId: 'triage-agent', icon: 'Triage', color: '#f2994a' }} />)
    expect(container.querySelector('.status-glyph')).not.toBeNull()
    expect(loopIconColor({ templateId: 'triage-agent', icon: 'Triage', color: '#f2994a' })).toBe('var(--inbox-status-triage)')
  })

  it('keeps an icon or color the user picked', () => {
    const { container } = render(<LoopIcon source={{ templateId: 'autofix-bugs', icon: 'Rocket', color: '#123456' }} />)
    expect(container.querySelector('svg.lucide-bug')).toBeNull()
    expect(loopIconColor({ templateId: 'autofix-bugs', icon: 'Bug', color: '#123456' })).toBe('#123456')
    expect(loopIconColor({ icon: 'Rocket', color: '#123456' })).toBe('#123456')
  })
})
