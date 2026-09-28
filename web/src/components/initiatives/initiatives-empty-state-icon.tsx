/**
 * LS-0325 InitiativesEmptyStateIcon — branded empty illustration (SVG, not CSS bars).
 */
import type { SVGProps } from 'react'

type IconProps = SVGProps<SVGSVGElement>

export function InitiativesEmptyStateIcon(props: IconProps) {
  return (
    <svg
      aria-hidden="true"
      className="li-empty-initiatives-icon"
      fill="none"
      focusable="false"
      height="78"
      viewBox="0 0 110 78"
      width="110"
      {...props}
    >
      <g stroke="currentColor" strokeWidth="1.25" opacity="0.85">
        <rect height="22" rx="11" transform="skewX(8)" width="88" x="2" y="2" />
        <rect height="22" rx="11" transform="skewX(8)" width="88" x="18" y="28" />
        <rect height="22" rx="11" transform="skewX(8)" width="88" x="2" y="54" />
      </g>
      <g fill="currentColor" opacity="0.55">
        <circle cx="88" cy="10" r="1.4" />
        <circle cx="94" cy="10" r="1.4" />
        <circle cx="100" cy="10" r="1.4" />
        <circle cx="102" cy="36" r="1.4" />
        <circle cx="108" cy="36" r="1.4" />
        <circle cx="88" cy="62" r="1.4" />
        <circle cx="94" cy="62" r="1.4" />
        <circle cx="100" cy="62" r="1.4" />
      </g>
      <path
        d="M14 9.5 18 13.5 14 17.5"
        fill="none"
        opacity="0.7"
        stroke="currentColor"
        strokeLinecap="round"
        strokeLinejoin="round"
        strokeWidth="1.5"
      />
      <path
        d="M30 35.5 34 39.5 30 43.5"
        fill="none"
        opacity="0.7"
        stroke="currentColor"
        strokeLinecap="round"
        strokeLinejoin="round"
        strokeWidth="1.5"
      />
      <path
        d="M14 61.5 18 65.5 14 69.5"
        fill="none"
        opacity="0.7"
        stroke="currentColor"
        strokeLinecap="round"
        strokeLinejoin="round"
        strokeWidth="1.5"
      />
    </svg>
  )
}

export default InitiativesEmptyStateIcon
