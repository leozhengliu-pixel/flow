import { render } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { WorkflowState } from '@/types/flow'
import { ComposerStatusGlyph } from './composer-status-glyph'
import { StatusIcon } from '@/components/issue/issue-icons'

const state = (overrides: Partial<WorkflowState> = {}): WorkflowState => ({ id: 'custom', name: 'Ready', type: 'unstarted', color: '#4CB782', position: 0, ...overrides })

describe('ComposerStatusGlyph', () => {
  it.each([
    ['unstarted', 'Custom gray', '#6B6F76'],
    ['started', 'In Progress', '#F2C94C'],
    ['completed', 'Done', '#5E6AD2'],
    ['canceled', 'Canceled', '#6B6F76'],
  ] as const)('preserves the team %s color in both shared glyphs', (type, name, color) => {
    const selected = state({ teamId: 'team-1', type, name, color })
    const { container } = render(<><ComposerStatusGlyph state={selected}/><StatusIcon state={selected}/></>)
    expect(container.querySelector('svg')).toHaveAttribute('fill', color)
    const strokes = [...container.querySelectorAll('[stroke]')].filter(element => element.getAttribute('stroke') !== 'none')
    expect(strokes.length).toBeGreaterThan(0)
    for (const element of strokes) expect(element).toHaveAttribute('stroke', color)
  })
  it('uses the workflow color and compact command-menu geometry', () => {
    const { container } = render(<ComposerStatusGlyph state={state()}/>)
    const svg = container.querySelector('svg')!
    expect(svg).toHaveAttribute('width', '14')
    expect(svg).toHaveAttribute('viewBox', '0 0 14 14')
    expect(svg).toHaveAttribute('fill', '#4CB782')
    expect(svg.querySelector('rect')).toHaveAttribute('stroke-width', '1.5')
  })

  it.each([
    ['backlog', 'Backlog', 'M13.9408 7.91426'],
    ['completed', 'Done', 'M7 0C3.13401'],
    ['canceled', 'Canceled', 'M7 14C10.866'],
  ] as const)('uses the static %s silhouette', (type, name, prefix) => {
    const { container } = render(<ComposerStatusGlyph state={state({ type, name })}/>)
    expect(container.querySelector('path')!.getAttribute('d')).toMatch(new RegExp(`^${prefix.replace('.', '\\.')}`))
  })

  it('distinguishes progress from review without replacing their configured colors', () => {
    const { container, rerender } = render(<ComposerStatusGlyph state={state({ type: 'started', name: 'In Progress' })}/>)
    expect(container.querySelector('path')).toHaveAttribute('d', 'M 3.5,3.5 L3.5,0 A3.5,3.5 0 0,1 3.5, 7 z')
    rerender(<ComposerStatusGlyph state={state({ type: 'started', name: 'In Review', color: '#ff0000' })}/>)
    expect(container.querySelector('path')).toHaveAttribute('d', 'M 3.5,3.5 L3.5,0 A3.5,3.5 0 1,1 0, 3.5 z')
    expect(container.querySelector('svg')).toHaveAttribute('fill', '#ff0000')
  })
})
