import type { SVGProps } from 'react'

type IconProps = SVGProps<SVGSVGElement>
export { CheckIcon, ChevronRightIcon, DisplayIcon, FilterIcon, SidebarIcon } from '@/components/ui/view-action-icons'

const baseProps: IconProps = {
  'aria-hidden': true,
  focusable: false,
  height: 16,
  viewBox: '0 0 16 16',
  width: 16,
}

export function PlusIcon(props: IconProps) {
  return <svg {...baseProps} {...props}><path d="M8.75 4a.75.75 0 0 0-1.5 0v3.25H4a.75.75 0 0 0 0 1.5h3.25V12a.75.75 0 0 0 1.5 0V8.75H12a.75.75 0 0 0 0-1.5H8.75V4Z" /></svg>
}


export function AddViewIcon(props: IconProps) {
  return <svg {...baseProps} {...props}>
    <path fillRule="evenodd" d="M6.974 1.345a1.67 1.67 0 0 1 2.034-.002l5.542 4.252a1.143 1.143 0 0 1 0 1.81l-5.542 4.252a1.67 1.67 0 0 1-2.034-.002L1.449 7.404a1.143 1.143 0 0 1 0-1.808l5.525-4.251ZM8 3.25a.75.75 0 0 1 .75.75v1.75h1.75a.75.75 0 0 1 0 1.5H8.75V9a.75.75 0 0 1-1.5 0V7.25H5.5a.75.75 0 0 1 0-1.5h1.75V4A.75.75 0 0 1 8 3.25Z" clipRule="evenodd" />
    <path d="M1.15 9.798a.7.7 0 0 1 1.016-.14l4.344 3.337a2.093 2.093 0 0 0 2.649 0l4.675-3.336a.7.7 0 0 1 1.016.14.79.79 0 0 1-.133 1.072l-4.675 3.336a3.49 3.49 0 0 1-4.415 0l-4.344-3.336a.79.79 0 0 1-.133-1.073Z" />
  </svg>
}

export function SavedViewIcon(props: IconProps) {
  return <svg {...baseProps} {...props} fill="none" stroke="currentColor" strokeWidth="1.35">
    <circle cx="5.2" cy="5.4" r="1.65" />
    <circle cx="10.9" cy="6.2" r="1.5" />
    <path d="M2.5 12c.2-2 1.25-3.1 2.7-3.1S7.7 10 7.9 12M8.4 11.8c.2-1.6 1.1-2.5 2.45-2.5 1.25 0 2.1.82 2.35 2.3" strokeLinecap="round" />
  </svg>
}
