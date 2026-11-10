import type { SVGProps } from 'react'

type GlyphProps = Omit<SVGProps<SVGSVGElement>, 'name'> & { size?: number | string }

/**
 * Glyphs for the rows of a team's settings overview that have no counterpart in the settings sidebar. Flow's own
 * drawings on the shared 16×16 grid (`currentColor`, so a row's icon follows its tile colour).
 */
function Glyph({ size = 16, children, ...props }: GlyphProps) {
  return <svg aria-hidden="true" focusable="false" width={size} height={size} viewBox="0 0 16 16" fill="none" {...props}>{children}</svg>
}

/** A padlock with an open shackle: Access and permissions. */
export function TeamAccessGlyph(props: GlyphProps) {
  return <Glyph {...props}>
    <path d="M5.4 7V4.9a2.6 2.6 0 0 1 5.1-.7" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/>
    <path fill="currentColor" fillRule="evenodd" clipRule="evenodd" d="M4.8 7h6.4A1.8 1.8 0 0 1 13 8.8v3.4a1.8 1.8 0 0 1-1.8 1.8H4.8A1.8 1.8 0 0 1 3 12.2V8.8A1.8 1.8 0 0 1 4.8 7ZM8 9.1a1.1 1.1 0 1 0 0 2.2 1.1 1.1 0 0 0 0-2.2Z"/>
  </Glyph>
}

/** A ring whose right half is filled: Statuses. */
export function TeamStatusesGlyph(props: GlyphProps) {
  return <Glyph {...props}>
    <circle cx="8" cy="8" r="6.25" stroke="currentColor" strokeWidth="1.5"/>
    <path fill="currentColor" d="M8 3.6a4.4 4.4 0 0 1 0 8.8Z"/>
  </Glyph>
}

/** A triangle in two tones: Estimates. */
export function TeamEstimatesGlyph(props: GlyphProps) {
  return <Glyph {...props}>
    <path fill="currentColor" fillOpacity=".4" d="M8 2.3 2.7 12.1c-.4.7.1 1.4.9 1.4H8Z"/>
    <path fill="currentColor" d="M8 2.3l5.3 9.8c.4.7-.1 1.4-.9 1.4H8Z"/>
  </Glyph>
}

/** A ring with a double-headed arrow inside: Triage. */
export function TeamTriageGlyph(props: GlyphProps) {
  return <Glyph {...props}>
    <circle cx="8" cy="8" r="6.25" stroke="currentColor" strokeWidth="1.5"/>
    <path d="M5 8h6M6.7 6.2 4.9 8l1.8 1.8M9.3 6.2 11.1 8 9.3 9.8" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round"/>
  </Glyph>
}

/** An open ring with a play mark: Cycles. */
export function TeamCyclesGlyph(props: GlyphProps) {
  return <Glyph {...props}>
    <path d="M8 1.75a6.25 6.25 0 1 1-5.6 3.5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/>
    <path d="m1.4 2.6.9 3 3-.9" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
    <path fill="currentColor" d="M6.6 5.9c0-.5.5-.8.9-.5l3.1 2.1c.4.3.4.8 0 1.1l-3.1 2.1c-.4.3-.9 0-.9-.5Z"/>
  </Glyph>
}

/** A channel hash in one colour: the chat notifications row. */
export function TeamChatGlyph(props: GlyphProps) {
  return <Glyph {...props}>
    <path d="M6.1 2.3 5.2 13.7M10.8 2.3l-.9 11.4M2.6 6h10.8M2.6 10h10.8" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round"/>
  </Glyph>
}

/** A pointer inside a rounded frame: Team agents. */
export function TeamAgentsGlyph(props: GlyphProps) {
  return <Glyph {...props}>
    <path d="M7.5 2H5a3 3 0 0 0-3 3v6a3 3 0 0 0 3 3h2M10 2h1a3 3 0 0 1 3 3v1.5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/>
    <path fill="currentColor" d="M7.4 6.4c-.4-.2-.8.2-.7.6l1.4 6.2c.1.4.6.5.8.2l1.2-2.2 2.2-1.2c.3-.2.2-.7-.2-.8Z"/>
  </Glyph>
}

/** Text lines that fold into an arrow: Resolved thread summaries. */
export function TeamSummariesGlyph(props: GlyphProps) {
  return <Glyph {...props}>
    <path d="M5 2.3v6.2a3.2 3.2 0 0 0 3.2 3.2h3" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/>
    <path d="M7.2 4.6h3.4M7.2 7.2h2.4" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/>
    <path fill="currentColor" d="M11 9.4v4.6l3-2.3Z"/>
  </Glyph>
}
