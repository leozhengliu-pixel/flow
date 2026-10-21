import { useId } from 'react'
import type { SVGProps } from 'react'
import { FLOW_LOGO_RIDGE } from '@/components/ui/flow-logo'
import { FlowGlyph } from '@/components/ui/menu-glyphs'
import './agent-glyph.css'

type GlyphProps = Omit<SVGProps<SVGSVGElement>, 'name'> & { size?: number | string }

/**
 * The agent glyph family, drawn like the icons of the reference app's agent surfaces (`FlowGlyph` paths, `currentColor`).
 * Each export takes `size` and `className` like a lucide icon, so it is a drop-in for `icon` props and `<Bot size={14}/>`.
 *
 *  - `AgentCursorGlyph`: the agent cursor: sidebar item, "Agent" corner button, composer chips, "Ask agent" buttons.
 *  - `AgentWriteGlyph`: "Write with Agent" / "Configure with Agent" / "Copy as prompt".
 *  - `AiBurstGlyph`: the AI burst: Triage Intelligence, "AI & agents" settings.
 *  - `LoopsGlyph`: Loops (nav, settings). `AgentMinimizeGlyph`, `AgentFullPageGlyph`, `AgentCloseGlyph`: the floating chat's header controls.
 *  - `AgentCodingGlyph`: "Coding sessions". `CodeIntelligenceGlyph`: "Code Intelligence". `AgentHistoryGlyph`: "Chat history".
 */
type GlyphName = 'agentPointer' | 'agentPointerFilled' | 'agentWrite' | 'aiBurst' | 'agentCoding' | 'codeIntelligence' | 'chatHistory' | 'moveToToolbar' | 'copyMessage' | 'loops' | 'chatMinimize' | 'openFullPage' | 'closeChat'

function AgentGlyphSvg({ glyph, size = 16, ...props }: GlyphProps & { glyph: GlyphName }) {
  return <FlowGlyph name={glyph} size={typeof size === 'number' ? size : Number.parseFloat(size) || 16} data-agent-glyph={glyph} {...props} />
}

export function AgentCursorGlyph(props: GlyphProps) { return <AgentGlyphSvg glyph="agentPointer" {...props} /> }
/** The filled cursor: the pressed "Hide agent" toggle of a coding session. */
export function AgentCursorFilledGlyph(props: GlyphProps) { return <AgentGlyphSvg glyph="agentPointerFilled" {...props} /> }
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
 * The built-in agent's avatar: Flow's mark drawn as a disc. The circle is the mark itself, in the primary label colour,
 * with the ridgeline cut through it (showing the page behind), so a round avatar holds no square tile. Sized by the host
 * class (100% by default).
 */
export function AgentAvatarMark({ className, size, title }: { className?: string; size?: number; title?: string }) {
  const id = `agent-avatar-${useId().replace(/[^a-zA-Z0-9_-]/g, '')}`
  const label = { role: title ? 'img' : undefined, 'aria-label': title, 'aria-hidden': title ? undefined : true } as const
  return <svg className={className ? `agent-avatar-mark ${className}` : 'agent-avatar-mark'} data-agent-avatar="" width={size} height={size} viewBox="4 4 40 40" fill="none" {...label}>
    <mask id={id} maskUnits="userSpaceOnUse" x="0" y="0" width="48" height="48">
      <rect width="48" height="48" fill="#fff"/>
      <path d={FLOW_LOGO_RIDGE} fill="none" stroke="#000" strokeWidth="5"/>
    </mask>
    <circle cx="24" cy="24" r="20" className="agent-avatar-mark__disc" mask={`url(#${id})`}/>
  </svg>
}
