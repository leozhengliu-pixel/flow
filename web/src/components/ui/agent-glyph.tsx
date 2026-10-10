import { useId } from 'react'
import type { SVGProps } from 'react'
import { FLOW_LOGO_RIDGE } from '@/components/ui/flow-logo'
import { LinearGlyph } from '@/components/ui/menu-glyphs'
import './agent-glyph.css'

type GlyphProps = Omit<SVGProps<SVGSVGElement>, 'name'> & { size?: number | string }

/**
 * The agent glyph family, drawn like the icons of Linear's agent surfaces (`LinearGlyph` paths, `currentColor`).
 * Each export takes `size` and `className` like a lucide icon, so it is a drop-in for `icon` props and `<Bot size={14}/>`.
 *
 *  - `AgentCursorGlyph`: the agent cursor: sidebar item, "Agent" corner button, composer chips, "Ask agent" buttons.
 *  - `AgentWriteGlyph`: "Write with Agent" / "Configure with Agent" / "Copy as prompt".
 *  - `AiBurstGlyph`: the AI burst: Triage Intelligence, "AI & Agents" settings.
 *  - `LoopsGlyph`: Loops (nav, settings). `AgentMinimizeGlyph`, `AgentFullPageGlyph`, `AgentCloseGlyph`: the floating chat's header controls.
 *  - `AgentCodingGlyph`: "Coding sessions". `CodeIntelligenceGlyph`: "Code Intelligence". `AgentHistoryGlyph`: "Chat history".
 */
type GlyphName = 'agentPointer' | 'agentWrite' | 'aiBurst' | 'agentCoding' | 'codeIntelligence' | 'chatHistory' | 'moveToToolbar' | 'copyMessage' | 'loops' | 'chatMinimize' | 'openFullPage' | 'closeChat'

function AgentGlyphSvg({ glyph, size = 16, ...props }: GlyphProps & { glyph: GlyphName }) {
  return <LinearGlyph name={glyph} size={typeof size === 'number' ? size : Number.parseFloat(size) || 16} data-agent-glyph={glyph} {...props} />
}

export function AgentCursorGlyph(props: GlyphProps) { return <AgentGlyphSvg glyph="agentPointer" {...props} /> }
export function AgentWriteGlyph(props: GlyphProps) { return <AgentGlyphSvg glyph="agentWrite" {...props} /> }
export function AiBurstGlyph(props: GlyphProps) { return <AgentGlyphSvg glyph="aiBurst" {...props} /> }
export function AgentCodingGlyph(props: GlyphProps) { return <AgentGlyphSvg glyph="agentCoding" {...props} /> }
export function CodeIntelligenceGlyph(props: GlyphProps) { return <AgentGlyphSvg glyph="codeIntelligence" {...props} /> }
export function AgentHistoryGlyph(props: GlyphProps) { return <AgentGlyphSvg glyph="chatHistory" {...props} /> }
export function AgentCopyGlyph(props: GlyphProps) { return <AgentGlyphSvg glyph="copyMessage" {...props} /> }
export function LoopsGlyph(props: GlyphProps) { return <AgentGlyphSvg glyph="loops" {...props} /> }
export function AgentMinimizeGlyph(props: GlyphProps) { return <AgentGlyphSvg glyph="chatMinimize" {...props} /> }
export function AgentFullPageGlyph(props: GlyphProps) { return <AgentGlyphSvg glyph="openFullPage" {...props} /> }
export function AgentCloseGlyph(props: GlyphProps) { return <AgentGlyphSvg glyph="closeChat" {...props} /> }
export function AgentToolbarGlyph(props: GlyphProps) { return <AgentGlyphSvg glyph="moveToToolbar" {...props} /> }

/**
 * The built-in agent's avatar, structured like Linear's "Linear Agent" avatar: a full-size circle in the primary label
 * colour with the product mark at 62.5% of the box (160 of 256) in the page background colour, so it inverts with the theme.
 * Linear draws its own logo there; Flow draws its ridgeline mark. Sized by the host class (100% by default).
 */
export function AgentAvatarMark({ className, size, title }: { className?: string; size?: number; title?: string }) {
  const id = `agent-avatar-${useId().replace(/[^a-zA-Z0-9_-]/g, '')}`
  const label = { role: title ? 'img' : undefined, 'aria-label': title, 'aria-hidden': title ? undefined : true } as const
  return <svg className={className ? `agent-avatar-mark ${className}` : 'agent-avatar-mark'} data-agent-avatar="" width={size} height={size} viewBox="0 0 256 256" fill="none" {...label}>
    <rect width="256" height="256" rx="128" className="agent-avatar-mark__disc"/>
    <svg x="48" y="48" width="160" height="160" viewBox="4 4 40 40" className="agent-avatar-mark__logo">
      <mask id={id} maskUnits="userSpaceOnUse" x="0" y="0" width="48" height="48">
        <rect width="48" height="48" fill="#fff"/>
        <path d={FLOW_LOGO_RIDGE} fill="none" stroke="#000" strokeWidth="5"/>
      </mask>
      <rect x="4" y="4" width="40" height="40" rx="10" mask={`url(#${id})`}/>
    </svg>
  </svg>
}
