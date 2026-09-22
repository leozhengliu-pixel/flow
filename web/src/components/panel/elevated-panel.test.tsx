import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { ElevatedPanel } from './elevated-panel'

describe('ElevatedPanel (LS-0236)', () => {
  it('defaults to elevation 1 with faint border + shadow', () => {
    render(<ElevatedPanel data-testid="panel">Body</ElevatedPanel>)
    const panel = screen.getByTestId('panel')
    expect(panel).toHaveAttribute('data-elevation', '1')
    expect(panel.className).toContain('flow-elevated-panel--border-faint')
    expect(panel.className).toContain('flow-elevated-panel--shadow-1')
  })

  it('honors disableBorder / disableShadow / skipElevation', () => {
    render(
      <ElevatedPanel
        data-testid="panel"
        elevation={2}
        disableBorder
        disableShadow
        skipElevation
      />,
    )
    const panel = screen.getByTestId('panel')
    expect(panel).not.toHaveAttribute('data-elevation')
    expect(panel.className).not.toContain('flow-elevated-panel--border-')
    expect(panel.className).not.toContain('flow-elevated-panel--shadow-')
  })
})
