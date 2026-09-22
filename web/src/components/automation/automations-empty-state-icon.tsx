/**
 * LS-0095 AutomationsEmptyStateIcon — branded empty illustration for Automations/Loops.
 * Shared glyph; prefer over generic FileText EmptyState.
 */
import type { SVGProps } from 'react'

type IconProps = SVGProps<SVGSVGElement>

export function AutomationsEmptyStateIcon(props: IconProps) {
  const labelled = Boolean(props['aria-label'] || props['aria-labelledby'])
  return (
    <svg
      fill="none"
      focusable="false"
      height="72"
      viewBox="0 0 120 120"
      width="72"
      {...props}
      className={props.className ?? 'flow-automations-empty-icon'}
      aria-hidden={labelled ? props['aria-hidden'] : true}
    >
      <defs>
        <linearGradient
          id="flow-automations-empty-gradient"
          gradientUnits="userSpaceOnUse"
          x1="0"
          x2="120"
          y1="0"
          y2="120"
          spreadMethod="repeat"
        >
          <stop offset="0" stopColor="var(--theme-text-primary)" />
          <stop offset=".38" stopColor="var(--theme-text-primary)" />
          <stop offset=".5" stopColor="var(--theme-text-secondary)" />
          <stop offset=".62" stopColor="var(--theme-text-primary)" />
          <stop offset="1" stopColor="var(--theme-text-primary)" />
        </linearGradient>
      </defs>
      <ellipse
        cx="60"
        cy="60"
        rx="51"
        ry="22"
        stroke="url(#flow-automations-empty-gradient)"
        strokeWidth="1.5"
        transform="rotate(45 60 60)"
      />
      <ellipse
        cx="60"
        cy="60"
        rx="51"
        ry="22"
        stroke="var(--theme-text-secondary)"
        strokeWidth="1.5"
        transform="rotate(-45 60 60)"
      />
      <ellipse
        cx="60"
        cy="60"
        rx="50"
        ry="21"
        stroke="var(--theme-border-strong)"
        strokeWidth="1.5"
        transform="rotate(90 60 60)"
      />
    </svg>
  )
}

export default AutomationsEmptyStateIcon
