import { render } from '@testing-library/react'
import type { ReactElement } from 'react'
import { describe, expect, it } from 'vitest'

import {
  AgentAvatarMark,
  AgentCodingGlyph,
  AgentCopyGlyph,
  AgentCursorGlyph,
  AgentHistoryGlyph,
  AgentToolbarGlyph,
  AgentWriteGlyph,
  AiBurstGlyph,
  CodeIntelligenceGlyph,
} from './agent-glyph'
import { BUILTIN_AGENT_AVATAR_URL, isBuiltinAgentAvatarUrl } from './agent-avatar-url'
import { AvatarImage, UserAvatar } from './user-avatar'

function glyphOf(ui: ReactElement) {
  const { container } = render(ui)
  return container.querySelector('svg')!
}

describe('agent glyph family', () => {
  it('draws the agent cursor like the sidebar / selection bar cursor: 16 box, one path, currentColor', () => {
    const svg = glyphOf(<AgentCursorGlyph size={14}/>)
    expect(svg).toHaveAttribute('data-agent-glyph', 'agentPointer')
    expect(svg).toHaveAttribute('viewBox', '0 0 16 16')
    expect(svg).toHaveAttribute('width', '14')
    expect(svg).toHaveAttribute('fill', 'currentColor')
    expect(svg.querySelectorAll('path')).toHaveLength(1)
    expect(svg.querySelector('path')!.getAttribute('d')).toMatch(/^M4\.07132 3\.8283C4\.04394 3\.81721/)
  })

  it('defaults to the 16px menu slot and accepts string sizes like lucide icons', () => {
    expect(glyphOf(<AgentCursorGlyph/>)).toHaveAttribute('width', '16')
    expect(glyphOf(<AgentCursorGlyph size="24"/>)).toHaveAttribute('width', '24')
  })

  it.each([
    [<AgentWriteGlyph key="w"/>, 'agentWrite', 4],
    [<AiBurstGlyph key="b"/>, 'aiBurst', 6],
    [<AgentCodingGlyph key="c"/>, 'agentCoding', 2],
    [<CodeIntelligenceGlyph key="i"/>, 'codeIntelligence', 2],
    [<AgentHistoryGlyph key="h"/>, 'chatHistory', 2],
    [<AgentToolbarGlyph key="t"/>, 'moveToToolbar', 2],
    [<AgentCopyGlyph key="m"/>, 'copyMessage', 2],
  ])('draws %#: the glyph and its paths', (ui, name, paths) => {
    const svg = glyphOf(ui)
    expect(svg).toHaveAttribute('data-agent-glyph', name)
    expect(svg).toHaveAttribute('viewBox', '0 0 16 16')
    expect(svg.querySelectorAll('path')).toHaveLength(paths)
  })

  it('the AI burst is eight rays (6 paths: 4 diagonals/axes pairs), not a lucide sparkle', () => {
    const svg = glyphOf(<AiBurstGlyph/>)
    expect(svg.querySelector('path')!.getAttribute('d')).toMatch(/^M8\.00098 11C8\.41519 11 8\.75098 11\.3358/)
    expect([...svg.querySelectorAll('path')].every(path => path.getAttribute('fill-rule') === 'evenodd')).toBe(true)
  })
})

describe('built-in agent avatar', () => {
  it('mirrors the 256 tile: a full circle with the mark at 160px offset 48', () => {
    const { container } = render(<AgentAvatarMark title="Flow"/>)
    const svg = container.querySelector('svg[data-agent-avatar]')!
    expect(svg).toHaveAttribute('viewBox', '0 0 256 256')
    expect(svg).toHaveAttribute('aria-label', 'Flow')
    const disc = svg.querySelector('rect.agent-avatar-mark__disc')!
    expect(disc).toHaveAttribute('width', '256')
    expect(disc).toHaveAttribute('rx', '128')
    const logo = svg.querySelector('svg.agent-avatar-mark__logo')!
    expect(logo).toHaveAttribute('x', '48')
    expect(logo).toHaveAttribute('y', '48')
    expect(logo).toHaveAttribute('width', '160')
    expect(logo).toHaveAttribute('viewBox', '4 4 40 40')
  })

  it('recognises the url the server gives the built-in agent', () => {
    expect(isBuiltinAgentAvatarUrl(BUILTIN_AGENT_AVATAR_URL)).toBe(true)
    expect(isBuiltinAgentAvatarUrl('/uploads/me.png')).toBe(false)
    expect(isBuiltinAgentAvatarUrl(undefined)).toBe(false)
  })

  it('UserAvatar and AvatarImage draw the tile instead of the raw image for the built-in agent only', () => {
    const agent = render(<><UserAvatar name="Flow" avatarUrl={BUILTIN_AGENT_AVATAR_URL} className="avatar"/><AvatarImage alt="Flow" className="pic" src={BUILTIN_AGENT_AVATAR_URL}/></>)
    expect(agent.container.querySelectorAll('svg[data-agent-avatar]')).toHaveLength(2)
    expect(agent.container.querySelector('img')).toBeNull()
    expect(agent.container.querySelector('span.avatar')).toHaveAttribute('data-agent-avatar-slot')
    expect(agent.container.querySelector('svg.pic')).toBeInTheDocument()
    agent.unmount()
    const person = render(<><UserAvatar name="Dev User" avatarUrl="/uploads/me.png"/><AvatarImage src="/uploads/me.png"/><UserAvatar name="Dev User"/></>)
    expect(person.container.querySelectorAll('svg[data-agent-avatar]')).toHaveLength(0)
    expect(person.container.querySelectorAll('img')).toHaveLength(2)
    expect(person.container.querySelector('[data-agent-avatar-slot]')).toBeNull()
  })
})
