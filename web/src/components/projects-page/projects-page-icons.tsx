import type { SVGProps } from 'react'

type IconProps = SVGProps<SVGSVGElement>
export { AddViewIcon, CheckIcon, ChevronRightIcon, DisplayIcon, FilterIcon, PlusIcon, SidebarIcon } from '@/components/ui/view-action-icons'

const baseProps: IconProps = {
  'aria-hidden': true,
  focusable: false,
  height: 16,
  viewBox: '0 0 16 16',
  width: 16,
}

export function SavedViewIcon(props: IconProps) {
  return <svg {...baseProps} {...props} fill="none" stroke="currentColor" strokeWidth="1.35">
    <circle cx="5.2" cy="5.4" r="1.65" />
    <circle cx="10.9" cy="6.2" r="1.5" />
    <path d="M2.5 12c.2-2 1.25-3.1 2.7-3.1S7.7 10 7.9 12M8.4 11.8c.2-1.6 1.1-2.5 2.45-2.5 1.25 0 2.1.82 2.35 2.3" strokeLinecap="round" />
  </svg>
}
