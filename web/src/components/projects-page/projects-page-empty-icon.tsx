/**
 * LS-0498 ProjectsPageEmptyIcon — branded empty illustration for projects list.
 */
import type { SVGProps } from 'react'

type IconProps = SVGProps<SVGSVGElement>

export function ProjectsPageEmptyIcon(props: IconProps) {
  return (
    <svg
      aria-hidden="true"
      className="lp-projects-empty-icon"
      fill="none"
      focusable="false"
      height="64"
      viewBox="0 0 72 64"
      width="72"
      {...props}
    >
      <rect
        height="40"
        rx="8"
        stroke="currentColor"
        strokeWidth="1.4"
        width="52"
        x="10"
        y="12"
        opacity="0.85"
      />
      <path
        d="M18 24h28M18 32h22M18 40h16"
        stroke="currentColor"
        strokeLinecap="round"
        strokeWidth="1.4"
        opacity="0.55"
      />
      <circle cx="54" cy="18" fill="currentColor" opacity="0.35" r="7" />
      <path
        d="M51.5 18h5M54 15.5v5"
        stroke="var(--bg-panel, #fff)"
        strokeLinecap="round"
        strokeWidth="1.4"
      />
    </svg>
  )
}

export default ProjectsPageEmptyIcon
