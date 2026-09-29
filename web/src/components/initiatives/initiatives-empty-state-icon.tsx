/**
 * Initiatives empty-state illustration: three stacked initiative cards (the middle one highlighted and offset),
 * each with a ground line under it, an initiative glyph and three status dots. 108×80 like Linear's.
 */
import type { SVGProps } from 'react'

type IconProps = SVGProps<SVGSVGElement>

const CARDS = [
  { x: 1, y: 1, highlighted: false },
  { x: 21, y: 29, highlighted: true },
  { x: 1, y: 57, highlighted: false },
]

export function InitiativesEmptyStateIcon(props: IconProps) {
  return (
    <svg aria-hidden="true" className="li-empty-initiatives-icon" fill="none" focusable="false" height="80" viewBox="0 0 108 80" width="108" {...props}>
      {CARDS.map(({ x, y, highlighted }) => (
        <g key={y} transform={`translate(${x} ${y})`}>
          {/* Ground: the card's lower edge repeated 6px below, like a card resting on a base. */}
          <path className="li-empty-initiatives-icon__base" d="M0 8.7h86V14a8 8 0 0 1-8 8H8a8 8 0 0 1-8-8Z" strokeWidth="1.5" />
          <rect className={highlighted ? 'li-empty-initiatives-icon__card is-highlighted' : 'li-empty-initiatives-icon__card'} height="16" rx="8" strokeWidth="1.5" width="86" />
          {/* Initiative glyph: an upward arrowhead. */}
          <path className={highlighted ? 'li-empty-initiatives-icon__glyph is-highlighted' : 'li-empty-initiatives-icon__glyph'} d="M7.2 11.1 11.6 3.4a.6.6 0 0 1 1 0l4.4 7.7c.3.5-.2 1.1-.8.9l-3.6-1.4a.9.9 0 0 0-.6 0L8 12c-.6.2-1.1-.4-.8-.9Z" />
          <circle className={highlighted ? 'li-empty-initiatives-icon__dot is-bright' : 'li-empty-initiatives-icon__dot is-strong'} cx="63.5" cy="8" r="1.5" />
          <circle className="li-empty-initiatives-icon__dot is-mid" cx="70.5" cy="8" r="1.5" />
          <circle className="li-empty-initiatives-icon__dot is-dim" cx="77.5" cy="8" r="1.5" />
        </g>
      ))}
    </svg>
  )
}

export default InitiativesEmptyStateIcon
